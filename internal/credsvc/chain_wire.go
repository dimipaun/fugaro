package credsvc

import (
	"net/http"
	"regexp"

	"github.com/dimipaun/fugaro/internal/routing"
)

// The chain wire: what a model call tells the service about the stage it is
// for, and what the service's answer tells the gateway about the source that
// served it. It is contract C1 of
// docs/model-credentials-routing/implementation-plan.md, and
// docs/model-credentials-routing/contract-table.md is the document a reader
// outside this package reads: that file says in prose what this one declares,
// including the run record's new members and the shape of the published table.
//
// It is written down once, here, because three sides read it and none of them
// owns the other two: the service's chain walk answers with it (CS1), a run's
// gateway sends the stage and reads the answer (CS2) and the run's record and
// ledger take the answer's figures (CS8).
//
// **Two rules, and a test pins each.**
//
// The first is that the answer's new members are sent ONLY to a caller that
// sent the stage header (R24, F57). A release-behind gateway is a gateway
// built before this contract: it drops the answer headers it knows and passes
// an unknown one through to the agent, and it decodes a refusal body strictly,
// so a member it has never heard of is a decode error rather than something it
// ignores. Sending it a `source` it does not know is therefore not harmless —
// it is either the agent reading the name of a credential source or the
// gateway failing on its own service's answer. The stage header is how a caller
// says "I am this contract's gateway, answer me in full", and a call that does
// not send it meets exactly the answer it meets today. SentStage is that rule
// written once, so the walk, the refusal and the record cannot each decide it
// differently; and any member this contract adds later — a refusal member, a
// new answer header — is covered by the same rule, because the rule is about
// the caller and not about the member.
//
// The second is that the service reads the model from the request's BODY and
// never from a header. There is no request header naming a model and there
// must never be one: a header is written by the caller and read by every hop,
// while the body's `model` member is the call the agent actually made, which
// is the only thing the table may be resolved against. A call whose body names
// no model is refused (CS1); a call whose header named one would be a call
// whose routing could be chosen by whoever set the header. The answer's Model
// header is the other direction — the service telling the gateway which model
// served — and it is never read on a request.
//
// **The names the gateway needs are mirrored, not imported.** internal/credsvc
// imports internal/gateway (the exported helpers the service forwards with),
// so internal/gateway cannot import this package: what the gateway sends and
// reads is declared in internal/gateway/chainwire.go as well (CS2), and a test
// in that package holds the two spellings to each other and holds every one of
// them apart from the names of the wire that was already there. This is the
// shape the provider wire already uses (provider_wire.go, F26), and the reason
// is the same. The SHAPES are not mirrored: routing.SourceName is declared once
// in internal/routing, which both sides import.

// HeaderStage names the stage a model call is for: the gateway's current stage
// (gateway.Stage.Name — "implement", "review", "checkpoint", the runner's own
// names). It is the request header of the rule above: a caller that sends it is
// this contract's gateway and is answered in full, and a caller that does not
// is answered exactly as it is today.
//
// It is the stage's NAME and never a model: what a call is routed to is the
// model in its body, and the stage only tells the service which step of the run
// the call belongs to, which is what the answer's fallback member is recorded
// against (CS8's per-stage member).
//
// It is the caller's claim and decides nothing on its own: the run is placed
// from its verified identity as it always was (HeaderRunIdentity, HeaderSlip),
// and this header says nothing about which repository the call is for. What it
// changes is the shape of the answer, which is why a value the service does not
// accept as a stage name is answered in the pre-contract shape rather than
// refused as a call (SentStage).
//
// **It is the gateway's own header, and the gateway is the only caller that may
// set it.** The gateway sets it from the run's current stage and drops the
// caller's copy before it does — the same rule, in the same place, as
// HeaderProvider (internal/gateway/service.go's serviceUpstream deletes the
// names the gateway owns before setting its own; the model path drops nothing
// by prefix). That is a requirement and not tidiness, because this header is
// the whole of R24's guard: an agent that can send it makes the service answer
// with the source, the fallback and the served model, and a release-behind
// gateway — the one gateway the guard exists to keep working — passes an answer
// header it does not know straight to the agent.
//
// **That drop ships in CS2, and it is the whole of what the guard's protection
// is worth.** The service reads this header and cannot tell the gateway's own
// from one the agent wrote, so before CS2's drop exists an agent behind a
// pre-CS2 gateway sends it and reads the three members back through that
// gateway's own pass-through — the gateway the guard is for is the gateway the
// guard cannot protect. It is therefore a compatibility rule and not a boundary
// between the gateway and the agent: it buys the ordinary case (a caller that
// sends no stage header meets today's answer byte for byte), CS2's request side
// closes the rest for the gateway CS2 builds, and the image update closes it for
// every gateway older than that. A later task may not assume the disclosure is
// closed for a run whose gateway predates CS2.
const HeaderStage = "X-Fugaro-Stage"

// maxStageNameBytes bounds a stage name. A stage name is recorded in the run's
// record and in an audit line, so it is held to a shape a record can carry
// rather than to the 512 bytes a header value may be.
const maxStageNameBytes = 64

// stageNameRE is a stage name: a lower-case letter, then lower-case letters,
// digits, hyphens and underscores. The underscore is not decoration — the
// runner's own stage names include `review_first` — and the set is otherwise
// the job-name rule's, because a stage name is the same kind of value: an
// identifier this repository's code writes and a record displays.
var stageNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// ValidStageName reports whether s may be the stage a call reports it is for
// (HeaderStage). It is a narrower rule than the header-value rule on purpose,
// the same way ValidJobName is: the value is recorded in the run's record and
// in the audit line, so a value that could not be a stage name — a quote, a
// backslash, a space, 512 bytes of anything — is not accepted as one.
//
// It is a shape and not a closed set, because the set of stages is the
// runner's: a stage this file has never heard of is a stage the runner added,
// and a closed set here would refuse every call of it. What it refuses is a
// value no stage name could be.
func ValidStageName(s string) bool {
	return len(s) <= maxStageNameBytes && stageNameRE.MatchString(s)
}

// SentStage reports whether the caller of this request sent the stage header
// (R24, F57): whether it is a gateway of this contract, which is answered with
// the source, the fallback and the served model, or one built before it, which
// is answered exactly as it is today.
//
// A value that is not a stage name is not a stage header, and that is the
// fail-closed direction: a caller sending garbage gets the pre-contract answer
// and never the new members, so nothing about this contract can be reached by
// sending a malformed header. It is also why the check is here and not at each
// use: every member added under this rule asks this one question, and a second
// reading of the header is how a member comes to be sent to a caller that
// never claimed to understand it.
func SentStage(h http.Header) bool {
	return ValidStageName(h.Get(HeaderStage))
}

// The answer headers of a chain-serving call. Every one of them is written
// only when SentStage is true, and every one of them is stripped by the
// gateway before the agent sees the answer (CS2's dropResponseHeaders list):
// they are the run's own accounting, and the agent that made the call has no
// business reading which credential source served it.
const (
	// HeaderSource is the JSON of a routing.SourceName: the kind of source
	// that served this call, and the name of the one that kind names. It is
	// never a credential value — a pool's name, a provider's name, or nothing
	// at all for the two kinds that are a single credential of the project —
	// which is what lets it travel on a response at all.
	//
	// It is JSON rather than a pair of headers, and rather than a bare string,
	// so that the shape has one spelling on the wire and in the record: the
	// member the run writes (stages[].source) is this object, and a reader of
	// either sees the same two members. SetSwitchHeader already carries JSON
	// in a header here, so nothing new is asked of the transport.
	HeaderSource = "X-Fugaro-Source"
	// HeaderFallback is "1" when this call was served by a source that is not
	// the chain's first, and "0" when it was. It is written on every chain
	// answer, both values, so that a reader never has to decide what an absent
	// member means — and it is per CALL, not per stage: the run's record folds
	// it into one member per stage (true when any call of the stage fell back,
	// CS8), and the service has no business holding a stage's calls together.
	//
	// A fallback call is real money on a paid source, which is why it is
	// carried at all: cost.fallback_usd is the sum of the calls this member
	// marks (CS8, D140).
	HeaderFallback = "X-Fugaro-Fallback"
	// HeaderModel is the canonical id of the model that served this call — the
	// model the gateway prices it by. It is written because what served is not
	// always what the agent asked for: a source may be a written downgrade (a
	// source entry naming a model) or a route whose own spelling differs, and
	// pricing the call by the requested model would charge the wrong model's
	// price. The agent is shown the model it asked for and never this one
	// (CS3's restore), so this header is the only place the served model
	// travels.
	//
	// It is the ANSWER direction only. The service reads the model a call is
	// for from the request's body and never from a header (the rule above);
	// this constant is not, and may never become, a request header.
	HeaderModel = "X-Fugaro-Model"
)

// The two values HeaderFallback carries, written by the service and read by the
// gateway. They are named here rather than left to each side's literal so that
// a gateway asking "did this call fall back?" and a service answering it cannot
// disagree about which string means yes — a disagreement that would be silent
// and would misprice a call (cost.fallback_usd is the sum of the calls this
// member marks).
const (
	// FallbackYes is a call served by a source that is not the chain's first.
	FallbackYes = "1"
	// FallbackNo is a call served by the chain's first source.
	FallbackNo = "0"
)

// LegacyBridge is the interface the release-1 bridge implements (MG3): the
// old access record, `creds/repoauth/<slug>`, read as the chain it stands for.
// It exists so that the service's chain walk (CS1) can be written against an
// interface and not against a file format, and so that the bridge is one
// implementation behind one seam — the walk reads the table first and consults
// this only for a run whose image predates the table (D3196, R25).
//
// It answers the sources the record gives and whether it has any. The second
// answer is not a convenience: a repository with no old record and a repository
// whose record is empty are different situations, and the walk refuses the
// first and skips the second. It answers routing.Source values rather than a
// mode and an account name, because the whole point of the bridge is that an
// older run's call lands on the sources that repository's runs go out on today:
// the record is derived from the table on every write of either, so it gains no
// source the table does not have — and it is NOT a subset of the table, because
// an entry the checks refuse is in no table and is left on the record (D3196).
// What keeps a call of this release off the record is the walk's rule that it
// reads the record only for a run whose image predates the table, never the
// record's width.
type LegacyBridge interface {
	OldSources(repository string) ([]routing.Source, bool)
}

// LegacyChain is the bridge the walk consults. The contract ships before the
// bridge: C1 declares this and a stub answers "no old record" (chain_stub.go),
// MG3 writes the real one (legacy_auth.go) and the deployment assigns it, over
// the database client the rest of the service is built with (internal/cli's
// credentialsServiceConfig), and X4 removes both the bridge and this seam.
var LegacyChain LegacyBridge
