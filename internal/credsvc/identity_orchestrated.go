package credsvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// The orchestrator's class at the issuer: what a resident orchestrator's ask may
// be issued, and the record it is bound to (LM24 of docs/launcher-does-not-mint,
// D3125 as amended by D3136, F34).
//
// WHY THIS CLASS IS JUDGED AGAINST A RECORD AND NOT AGAINST A PERSON. The trace
// (F34) found that nothing in the cloud judged what the resident orchestrator
// asks for: `residentLauncher` runs no gate block and no access check on purpose,
// and what it starts is a re-issue of an attempt the node already carries. The
// first answer — judge the launch scope of the person who committed the attempt —
// was withdrawn after review round 4 read the node: it is closed to every
// signed-in identity (`orchestrator`, write false) and written over IAM by the
// database admins alone, the same people who write the roles records, so a
// launcher with a modified CLI cannot write it; and an attempt names no committer
// (`orchestrator.Intent` holds an id, a run id, a spec digest, two times and an
// outcome), so the person to judge would be the admin's own word. What is left to
// bind is the record itself: the issuer serves the class only a run id that is a
// recorded, unstarted attempt of the repository the ask names, and never one with
// context repositories. A compromised orchestrator account can then launch only
// what an admin's patch already committed (D3136).
//
// WHERE THE RECORD IS READ, AND WHAT IT CANNOT SHOW. The record is the
// orchestrator's own state node as internal/orchestrator stores it
// (orchestrator.ReadState), whose Intents are the attempts committed BEFORE
// anything is started: a person's patch commits one under the approval they gave
// it (orchestrator's commitIntent), and the resident pass only ever starts what
// that record already carries. An intent's ID is the item and the attempt joined
// (orchestrator.IntentID), its RunID is the run id minted for that attempt, and
// its Outcome is empty until the run reports — which is the whole of what
// "unstarted" means on this side of the wire. **The record cannot tell an
// attempt nothing has started from one whose run is in flight**: both are the
// open intent the resident pass re-issues, and a run's own start is the runs
// bucket's fact and not the node's. That half is judged where it already is: the
// ticket route refuses a ticket that was already claimed or that stands beside an
// outcome (R4, LM1), and a claimed ticket is exactly a run that started — its
// claim is the run's first call. The two together are D3136's "recorded,
// unstarted attempt"; this file is the record half of it.
//
// WHAT THE RECORD SHOWS, AND THE GATE AGREES ABOUT — BY BEING ASKED. The whole
// of the launcher's own judgement of an attempt is asked of the record here,
// through orchestrator.ReissuableAttempt, which is `reissuable`'s record half:
// the item the attempt names, the open intent that is exactly this attempt
// under this run id, an item the record has not finished (done or cancelled),
// an item still the spec the attempt was committed for, and an approval that
// covers the attempt or is the one it spent. One function rather than a second
// copy of those checks, because a copy is where the two come apart: a launcher
// with a modified CLI must not be able to ask the issuer for what the launcher
// itself refuses. Work a person called off is not re-issued, an item edited
// since the attempt was committed is not started under the spec the person
// withdrew, and an approval a person has replaced since is not spent again —
// and none of those waits on the daemon's own abandon of the intent
// (`recovery`, whose notes say which of the three closed it): the intent stays
// open in the record until the daemon writes that, and the issuer refuses it
// whether or not the daemon has got there.
//
// THE TWO SIDES ARE SPELLED DIFFERENTLY, AND ONE OF THEM IS DERIVED. An item
// carries its repository canonically — `owner/name`, as
// orchestrator-state.schema.json pins it — and an ask carries the storage slug
// a launch and a run are keyed by (task.Slug: `acme-web-3130f6e03f9febba`,
// which never holds a slash), because that slug is what the ticket record's
// path and the run's own `fs` claim are built from. Neither spelling can be
// turned into the other — the slug is a hash — so the record's own direction is
// the one taken: the ask names the item's repository when the item's canonical
// name derives the ask's slug under one of the kinds a repository may use
// (askNamesTheItemsRepo). Reading the two as one string would refuse every
// honest ask.
//
// WHAT THIS FILE DOES NOT DO. No committer is read and no scope is judged for
// this class (D3136): the check never asks the roles-and-scopes resolver about an
// orchestrator, and it hands every OTHER class to the check it was built with
// (LM23's resolver, which judges persons and roster service accounts). The one
// audit line a refusal earns is the ROUTE's — audit.go's rule is one line per
// refusal once the caller's identity is verified, and this check holds no
// auditor, so a line written here would be a second one. What this file returns
// is the refusal itself, which the ticket route answers with the contract's
// `identity_refused` (contract.md) and records once.

// OrchestratorRecord is the orchestrator's own record as the issuer reads it
// (D3136): the state node internal/orchestrator keeps for the installation's
// resident orchestrator, whose Intents are the attempts it committed before
// starting anything.
//
// It is an interface and not the node itself because the reader is I1's to build
// over the database the service already holds — orchestrator.ReadState over the
// service's own client, for the id the installation's orchestrator serves — and
// because a check that reached for a transport of its own would be a second place
// the service's account is used. An error is the SERVICE's own failure and is
// never a verdict about the caller (R16's ScopeFault rule): a node that could not
// be read must not be answered as an ask that is not the record's.
type OrchestratorRecord interface {
	// State answers the orchestrator's own node as internal/orchestrator stores
	// it: the work items it carries and the intents of the attempts committed
	// under them. An absent node is the orchestrator's empty state rather than
	// an error, as it is for every reader of that node — and an empty state
	// carries no attempt, so the ask is refused rather than admitted.
	State(ctx context.Context) (orchestrator.State, error)
}

// ErrOrchestratedAsk is the refusal of an ask in the orchestrator's class that
// the orchestrator's own record does not carry as one its gate would start: a
// run id the record never recorded, an attempt that is not the one that run id
// belongs to, an item the record does not carry, an item the record has
// finished (done or cancelled) or edited since the attempt was committed, an
// approval a person has replaced since, an attempt that has ended, an attempt
// of another repository, or an ask that names a context repository at all
// (D3136).
//
// It is one value because the route's answer is one: every one of those is the
// contract's `identity_refused`, and a caller branches on the code and not on
// the sentence (identitywire's refusal codes). The sentence the wrap carries
// names which of them it was, for the operator reading the line.
var ErrOrchestratedAsk = errors.New("credsvc: the orchestrator's record does not carry that unstarted attempt")

// OrchestratedFault is what an ask of the orchestrator's class is answered when
// the orchestrator's own record cannot be read: the service's own failure
// (R16's ScopeFault rule), with the caller the service verified carried beside
// it.
//
// WHY THE CALLER TRAVELS HERE. The audit rule is one line per refusal once the
// caller's identity is verified (audit.go), and the line is written from the
// identity the service verified — which for this class is an address and a
// class and NOT a run identity: a person's address, a roster service account's,
// the orchestrator daemon's, none of which is a run of this installation. C1's
// *ScopeFault carries a Caller of its own, but that field is a run Identity
// (scope.go), so it cannot name this class's caller, and the front door answers
// a fault whose Caller is nil with the service's own failure and NO audit line
// at all (refuseResolve, server.go): the one line the rule requires of a
// verified caller's refusal would be no line. So the caller travels beside the
// fault instead, and whoever answers the class — the ticket route, which holds
// the caller it verified — writes that line from this field, as the slip grant
// route writes a launcher's (refuseGrant) and the mobile routes write a
// member's. Handing this fault to a reader that knows only *ScopeFault is
// therefore a way to lose the line, and that is what the field exists to
// prevent.
type OrchestratedFault struct {
	// ScopeFault is the fault itself: what failed, and the reader's own error.
	// It is embedded so that every reader that knows C1's fault — the front
	// door's own reading of the three answers included — still reads this as
	// the service's failure and never as a verdict about the caller.
	*ScopeFault
	// Caller is the identity this service verified for the ask: what the
	// refusal's one line is written from, and never a value read out of the
	// ask or the record.
	Caller IdentityCaller
}

// Unwrap keeps C1's fault in the chain beside the caller: a reader that asks
// for a *ScopeFault (errors.As) finds it here, and one that asks for this type
// finds the caller with it.
func (e *OrchestratedFault) Unwrap() error { return e.ScopeFault }

// NewOrchestratedScope builds the ticket route's scope check with the
// orchestrator's class judged against the orchestrator's own record (LM24).
//
// next is the check for every class that is not the orchestrator's — LM23's
// resolver, which judges the launch scope of a person or a roster service
// account and leaves this class alone. It is required rather than optional: a
// check that answered the other classes itself would be a check that judged no
// scope, and one that admitted them would be a hole the service could not see.
//
// record is the orchestrator's own record, and it is required for the same
// reason read the other way: a check that read none would admit every ask the
// class makes, which is the state this task ends.
func NewOrchestratedScope(next ScopeCheck, record OrchestratorRecord) (ScopeCheck, error) {
	if next == nil {
		return nil, errors.New("credsvc: the orchestrator's check needs the check for every other class, and one that judged no scope would admit a caller the resolver refuses")
	}
	if record == nil {
		return nil, errors.New("credsvc: the orchestrator's check needs the orchestrator's own record, and a check that read none would admit every ask the class makes")
	}
	return orchestratedScope{next: next, record: record}, nil
}

// orchestratedScope is the check itself: the orchestrator's class judged here,
// every other class handed to next.
type orchestratedScope struct {
	next   ScopeCheck
	record OrchestratorRecord
}

var _ ScopeCheck = orchestratedScope{}

// Check judges one ask. The class is the service's own reading of the caller
// (LM3's verification), never text from the call, so a caller cannot choose
// which arm of this check it meets.
func (c orchestratedScope) Check(ctx context.Context, caller IdentityCaller, ask identitywire.TicketRequest) error {
	if caller.Class != identitywire.ClassOrchestrator {
		return c.next.Check(ctx, caller, ask)
	}
	return c.checkOrchestrated(ctx, caller, ask)
}

// checkOrchestrated is the orchestrator's arm: the ask is bound to the record
// and to nothing else. The cheap refusals come first so that an ask that cannot
// be the record's does not read the node at all.
func (c orchestratedScope) checkOrchestrated(ctx context.Context, caller IdentityCaller, ask identitywire.TicketRequest) error {
	if len(ask.ContextRepos) != 0 {
		// The list is signed into the run's identity and a reader of that
		// identity holds a signature and not a role (F31), so an ask that names
		// one is refused whole rather than signed with the list dropped: what
		// the class may be issued is an attempt of its own record and nothing
		// beside it (D3136).
		return fmt.Errorf("%w: the ask names %d context repositories, and the orchestrator's class is issued none", ErrOrchestratedAsk, len(ask.ContextRepos))
	}
	if !ask.Attempt.Valid() {
		// An ask that names no attempt — or one the orchestrator's own spelling
		// could not have written — has nothing to look up, and two references
		// that mean one attempt must not both be looked up (AttemptID.Valid).
		return fmt.Errorf("%w: the ask names no attempt of the orchestrator's own record (%q)", ErrOrchestratedAsk, safetext.OneLine(string(ask.Attempt)))
	}
	state, err := c.record.State(ctx)
	if err != nil {
		// The record could not be read, which is this service's own failure and
		// not a refusal of the caller: the route answers it as one
		// (StatusUnavailable with ErrorStateUnavailable, R16's ScopeFault rule)
		// and writes the line its verified caller earns — from the caller this
		// fault carries, because this class's caller holds no run identity for
		// C1's ScopeFault.Caller to name (OrchestratedFault).
		return &OrchestratedFault{
			ScopeFault: &ScopeFault{What: "the orchestrator's own record", Err: err},
			Caller:     caller,
		}
	}
	attempt := string(ask.Attempt)
	item, err := orchestrator.ReissuableAttempt(state, attempt, ask.Run)
	if err != nil {
		// The record does not carry this attempt as one the orchestrator's own
		// gate would start. The judgement is the gate's own
		// (orchestrator.ReissuableAttempt, which is reissuable's record half),
		// so a launcher with a modified CLI cannot ask the issuer for what the
		// launcher itself refuses — and the gate's sentence is carried into
		// this one, so the operator reads which of them it was: no such work
		// item, no open attempt recorded under that run id, an item the record
		// has finished (done or cancelled), an item edited since the attempt
		// was committed, or an approval a person has replaced since.
		return fmt.Errorf("%w: the orchestrator's record does not carry the attempt %q of run %q as one its own gate would start: %v", ErrOrchestratedAsk, safetext.OneLine(attempt), safetext.OneLine(ask.Run), err)
	}
	if !askNamesTheItemsRepo(ask.Slug, item.Repo) {
		// The attempt is a recorded one, and it is the record's for another
		// repository: what the class may be issued is an attempt OF THE
		// REPOSITORY the ask names (D3136), so an ask that points one item's
		// attempt at another repository's run is refused.
		return fmt.Errorf("%w: the attempt %q is the record's for %q, and the ask's slug %q is not that repository's", ErrOrchestratedAsk, safetext.OneLine(attempt), safetext.OneLine(item.Repo), safetext.OneLine(ask.Slug))
	}
	return nil
}

// askNamesTheItemsRepo reports whether askSlug is a storage slug of itemRepo:
// the ask's own spelling of the repository, against the record's.
//
// The record is canonical (`owner/name`) and the ask is a slug
// (task.Slug — "acme-web-3130f6e03f9febba"), and the slug is a hash of the
// provider kind and the canonical path, so only the record's direction
// resolves: the ask names the item's repository when one of the kinds a
// repository may use (config.Providers) derives the ask's slug from the item's
// canonical name. The kinds are that list rather than a second spelling of it
// because internal/runner's own derivation — the run's, whose admission check
// and this one must agree about which repository a slug is — is held to the
// same list, and a kind this file added would be a repository the service
// admits and no job serves.
//
// Both kinds answer for one canonical name, and that is the whole of what the
// record can say: an item carries no provider kind (one installation may hold a
// GitHub and a Bitbucket repository of one name, and the slug is what keeps
// them apart), so which of the two jobs serves the run is not this check's
// question — the account the ask names must be that slug's job's account (R4),
// and that is where the two are told apart.
//
// "fake" is not among them and costs nothing: a fake provider is a local run,
// which reaches no cloud service at all. A slug none of them derives is not the
// item's repository's, and a slug two of them derive is a hash collision rather
// than a second repository — the run's own derivation is what refuses that one,
// as it refuses a job that no kind serves.
func askNamesTheItemsRepo(askSlug, itemRepo string) bool {
	for _, kind := range config.Providers {
		if slug, err := task.Slug(kind, itemRepo); err == nil && slug == askSlug {
			return true
		}
	}
	return false
}
