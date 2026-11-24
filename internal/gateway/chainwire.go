package gateway

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dimipaun/fugaro/internal/routing"
)

// The gateway's half of the chain wire: the names a model call and its answer
// carry between this gateway and the credentials service, and the reader of
// the answer's chain members. It is contract C1 of
// docs/model-credentials-routing/implementation-plan.md — the mirror of
// internal/credsvc/chain_wire.go — and docs/model-credentials-routing/
// contract-table.md is the document a reader outside this package reads.
//
// **Why the names are mirrored and not imported.** internal/credsvc imports
// this package (the exported helpers the service forwards with, C1.8), so this
// package cannot import it without a cycle. The names are therefore spelled a
// second time here, and chainwire_test.go — an external test file, which may
// import both — holds them to the wire's own one by one, holds every one of
// them apart from the names the wire already carried, and pins the
// request-side rule below. This is the shape the provider wire already uses
// (providerwire.go, F26), and the reason is the same. The SHAPES are not
// mirrored: routing.SourceName is declared once, in internal/routing, which
// both sides import (chain_names.go says why).
//
// **What travels which way.** The stage header is the gateway's OWN and goes
// out on every call this gateway makes to the service when the service serves
// this run (Options.ChainServed): it is set here from the run's current stage,
// and the caller's copy is dropped before it is (serviceUpstream). The three
// answer members travel the other way: the service writes them only to a
// caller that sent the stage header (SentStage, R24/F57), the gateway reads
// them into the step's record hook (ChainAnswerFunc) and strips them before
// the agent sees the answer (forward.go's dropResponseHeaders). No request
// header names a model, and HeaderModel below is not, and may never become,
// one: the service reads the model a call is for from the request's body.

// HeaderStage names the stage a model call is for: the run's current stage
// (Stage.Name — "implement", "review", "checkpoint", the runner's own names),
// which is what the service records a fallback against. It is the request
// header of the rule above: a caller that sends it is a gateway of this
// contract and is answered with the source, the fallback and the served model,
// and a caller that does not is answered exactly as it is today.
//
// It is the gateway's own header, and this package is the only caller that may
// set it (serviceUpstream): the gateway sets it from the run's current stage
// and drops the caller's copy before it does, and it sends the header only
// where the service really serves this run. That drop is what closes the
// disclosure the guard leaves open — an agent that could send this header
// would make the service answer in full, and a release-behind gateway passes
// an answer header it does not know straight to the agent (R24, F57,
// contract-table.md's "What the guard is not").
const HeaderStage = "X-Fugaro-Stage"

// The answer headers of a chain-serving call, as the service writes them
// (credsvc.HeaderSource and its neighbours): the run's own accounting of which
// credential source served a call, and never a credential value. Every one of
// them is written only to a caller that sent the stage header, every one is
// read into the step's record hook below, and every one is stripped before the
// agent sees the answer (forward.go's dropResponseHeaders adds all three).
const (
	// HeaderSource is the JSON of a routing.SourceName: the kind of source
	// that served this call and the name of the one that kind names, empty for
	// the two kinds that are a single credential of the project. It is JSON
	// rather than a pair of headers so that the shape has one spelling on the
	// wire and in the run's record.
	HeaderSource = "X-Fugaro-Source"
	// HeaderFallback is FallbackYes when this call was served by a source that
	// is not the chain's first and FallbackNo when it was. Both values are
	// written, so no reader has to decide what an absent member means, and it
	// is per CALL: the run folds it over a stage's calls (ChainAnswerFunc).
	HeaderFallback = "X-Fugaro-Fallback"
	// HeaderModel is the canonical id of the model that served this call — the
	// model the ledger prices it by, which is not always the model the agent
	// asked for. It is the ANSWER direction only: the service reads the model a
	// call is for from the request's body, and this constant is never sent on
	// a request.
	HeaderModel = "X-Fugaro-Model"
)

// The two values HeaderFallback carries, as the service writes them
// (credsvc.FallbackYes, credsvc.FallbackNo). They are named rather than left
// to each side's literal so that a gateway asking "did this call fall back?"
// and a service answering it cannot disagree about which string means yes:
// cost.fallback_usd is the sum of the calls this member marks, and a
// disagreement would misprice a run silently.
const (
	FallbackYes = "1"
	FallbackNo  = "0"
)

// chainAnswerHeaders reads the answer's three chain members: what served this
// call, whether the source was the chain's first, and the model that served.
// present is false for an answer that carries none of them, which is every
// answer of a service built before this contract and is not an error: the
// gateway sends the stage header when the service serves this run, and a
// service that does not read it answers in the pre-contract shape, so the run
// goes on with no source recorded rather than failing on a member the service
// never promised to write.
//
// An answer that carries only part of them, or a member that is not what this
// contract writes, is an error rather than a value: the members are what the
// run's record and its ledger charge by, so a run that could not read them
// halts (serviceAnswer) instead of recording a source it guessed at. Every
// sentence here names the member and never its value: the values arrive as
// header text from another process.
//
// It is the shape of routing.SourceName that is read back rather than its
// members one by one, and the decode tolerates a member it does not know: that
// shape is declared once for both sides and a later release may add to it, and
// a reader that refused it would halt every run in flight against a service
// one release ahead.
func chainAnswerHeaders(h http.Header) (ChainAnswer, bool, error) {
	source, haveSource, err := responseHeader(h, HeaderSource)
	if err != nil {
		return ChainAnswer{}, false, err
	}
	fallback, haveFallback, err := responseHeader(h, HeaderFallback)
	if err != nil {
		return ChainAnswer{}, false, err
	}
	model, haveModel, err := responseHeader(h, HeaderModel)
	if err != nil {
		return ChainAnswer{}, false, err
	}
	if !haveSource && !haveFallback && !haveModel {
		return ChainAnswer{}, false, nil
	}
	if !haveSource || !haveFallback || !haveModel {
		return ChainAnswer{}, false, errors.New("the answer carries only part of this contract's chain members")
	}
	var name routing.SourceName
	if err := json.Unmarshal([]byte(source), &name); err != nil {
		return ChainAnswer{}, false, errors.New("the answer's source isn't the JSON of a source name")
	}
	if !routing.ValidKind(name.Kind) {
		// The five kinds are a closed set (routing.ValidKind), and a sixth is
		// a source this release has no reader for: the run's record and the
		// console both switch on the kind, so an unknown one is an answer this
		// gateway cannot account a call by rather than a name to pass on.
		return ChainAnswer{}, false, errors.New("the answer's source names a kind this contract doesn't carry")
	}
	if name.Name != "" && !printableValue(name.Name) {
		return ChainAnswer{}, false, errors.New("the answer's source name isn't one clean header value")
	}
	if fallback != FallbackYes && fallback != FallbackNo {
		return ChainAnswer{}, false, errors.New("the answer's fallback flag isn't one this contract carries")
	}
	if !printableValue(model) {
		return ChainAnswer{}, false, errors.New("the answer's served model isn't one clean header value")
	}
	return ChainAnswer{Source: name, Fallback: fallback == FallbackYes, Model: model}, true, nil
}
