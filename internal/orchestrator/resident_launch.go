// The launcher half of the resident orchestrator (mobile plan task P5.1d; the
// design is docs/mobile/design/always-on-orchestrator.md, sections 4.1 and
// 4.2).
//
// WHAT THIS FILE IS. Section 4.2's second bullet is the launcher: "for a launch
// request, takes the launch claim, mints the budget token, and starts the job
// execution. That is `fugaro run`'s existing path with a service account behind
// it", and Q10's answer is that the resident orchestrator "reuses
// internal/orchestrator's launcher, lease and state rather than rebuilding
// them". So what is here is the three STEPS of one launch in that order, each
// one a seam of its own — the claim is internal/runstore's, the token is
// internal/budget/token's, the start is internal/backend's — and nothing of
// `fugaro run`'s machinery written a second time: what a caller composes them
// with is launch.go's Starter, the same object the operator daemon's cycle
// drives (tick.go's carry), handed a different Launcher.
//
// THE AGENT DOES NOT RUN HERE. A task is a Cloud Run JOB execution, discarded
// at the end (the design's section 4.2, in as many words): this process starts
// one and never becomes one, which is why the last step is a start and not a
// run — nothing in this file waits for a run, reads its record or holds its
// result.
//
// WHY A RESIDENT PASS CARRIES OUT RE-ISSUES AND NOTHING ELSE. A node's writes
// stay with `fugaro orchestrator patch` and the person's own credential (D1223,
// D1241): the service account reaches the database over IAM, whose one read
// permission is `firebasedatabase.instances.get` and whose write —
// `firebasedatabase.instances.update` — is deliberately not bound. So the
// intent is on the node before this process is asked to start anything: a
// person's patch commits the attempt under the approval they gave it, and what
// is left for the resident service is the attempt that record already carries.
// That is ActionReissue exactly — a launch whose run id is recorded and whose
// run never started — which Starter.Start carries out without a commit, and
// every other action a plan derives (a first launch, a retry, an abandon) is a
// write this process does not have. ResidentRequests is that split, and a
// caller that ignored it would meet Starter.begin's own refusal rather than
// start a run nothing recorded ("a start it cannot record is one it must not
// make"), because a resident pass's starter carries no commit.
//
// THE VIEW IT IS HANDED IS THE RUNS BUCKET'S, AND A DEPLOYMENT MAY GIVE IT
// NONE. recovery (intent.go) reads one fact off the run records — whether the
// run an intent names ever started — and a pass whose RunView is empty reads
// every run id as one that never started. The direction a re-issue takes is the
// safe one: the SAME run id and never a second one, and the start seam's
// contract below is what makes that true rather than lucky — a run that already
// has a launch record is answered with the execution that record names, which
// is what `fugaro run`'s own path does before it starts anything
// (internal/runstore's launch.json is create-if-absent, and internal/cli's
// existingLaunch reads it). ClaimTTL is the second fence around the same
// question: nothing here is re-issued inside the ten minutes a launch beside it
// may still be in flight.
//
// HOW IT FAILS. Every step refuses rather than starting something it cannot
// finish: a seam this deployment does not carry, a claim another launcher
// holds, a mint that failed, a start the platform refused. Nothing here logs —
// this is a library, and the process that serves it writes the line
// (internal/cli/orchestrator_serve.go) — and no refusal quotes a value a caller
// handed in except through safetext: a run id, a claim's holder and another
// library's error text are all text from outside this process.

package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// ClaimFunc takes one run's launch claim and answers whether this caller holds
// it. It is internal/runstore's Store.Claim: create-if-absent under the run's
// own prefix, so the caller that gets it is the one actor between that run's
// task and its launch record — the only mechanism in this tree that keeps two
// actors from starting one run twice (F4). taken is false when another holder
// has it, and holder is then that holder's name, for the line a caller writes.
type ClaimFunc func(ctx context.Context, runID string, at time.Time) (taken bool, holder string, err error)

// ReleaseFunc gives one run's claim back, which is what a launcher owes a run
// whose launch it did not make: a claim left behind is a run that waits out
// ClaimTTL for nothing. internal/runstore's conditional delete of the run's
// launching object is it.
type ReleaseFunc func(ctx context.Context, runID string) error

// TokenFunc mints one run's budget token and leaves it where the run reads it,
// sealed under a key of its own (internal/budget/token's Mint and PutObject,
// under tokens/). The key comes back with it because the ONE execution the
// token is for is the only thing that may open it (D15): it travels in that
// execution's environment as the marker its container reads, and an execution
// handed another key cannot open the token its own run id names. A mint that
// refused wrote nothing — PutObject is create-if-absent — so a caller may
// always treat an error here as "no token exists".
type TokenFunc func(ctx context.Context, spec Spec, runID string) (sealKey []byte, err error)

// StartFunc starts one run's job execution, and it is `fugaro run`'s own path
// behind a service account (the design's section 4.2): it writes the run's task
// record and its launch record — the plan task and the external id a Spec
// carries are written there rather than read by a launcher off the state — and
// it starts the job execution with the seal key its token was sealed under
// (internal/backend's Launch, with LaunchSpec.SecretEnv, D15).
//
// A run that already has a launch record is answered with the execution that
// record names rather than started a second time: `fugaro run` reads it before
// it starts anything (internal/cli's existingLaunch) and the runstore's
// launch.json is create-if-absent, so this is the path's own behaviour and not
// an extra promise — it is what makes a re-issue of a run that did start
// harmless, which is the case recovery's ClaimTTL window cannot rule out on a
// pass whose RunView is empty.
type StartFunc func(ctx context.Context, spec Spec, runID string, sealKey []byte) (string, error)

// ResidentLaunch is the launcher half: the Launcher a resident pass hands to
// Starter, and the three steps of one launch in the order the design lists
// them. It is a Launcher and nothing more — what a pass reads to find the
// requests, and what it does with the answer, belong to the caller
// (internal/cli/orchestrator_serve.go).
type ResidentLaunch struct {
	// Claim is the run's launch claim. Required: a launcher that cannot claim
	// a run is one that starts runs another launcher may be starting.
	Claim ClaimFunc
	// Release gives a claim back when the launch it was taken for was not made.
	// Optional, and a launcher without one KEEPS a claim it could not use,
	// which is the direction that starts nothing twice.
	Release ReleaseFunc
	// Token mints the run's sealed budget token. Required.
	Token TokenFunc
	// Start starts the run's job execution. Required.
	Start StartFunc
	// Now is the clock the claim's stamp is written from; a nil one is
	// time.Now, which is what a deployed revision has. That stamp is a bucket
	// object's and not a node's, and it is the wall clock a later reader judges
	// a stale claim by, so it is not the database's server clock D1230
	// requires of the state's own stamps.
	Now func() time.Time
}

var _ Launcher = (*ResidentLaunch)(nil)

// Launch takes the run's claim, mints its budget token and starts its job
// execution, in that order and never any other.
//
// The order is the design's and each step depends on the one before it: the
// claim is what says this process is the one launching this run, the token is
// what the run spends under, and the execution is handed the key that opens it.
// A refusal at any step leaves the state of the world the step before it left,
// with one exception that is deliberate: a claim taken and then not used goes
// back (when the launcher carries a release seam), unless the start itself
// failed — a request the platform may have accepted before it failed is a run
// that may be going, and a released claim beside the next pass's re-issue of
// the same run id is exactly the second start the claim exists to prevent.
//
// A launcher that comes back with another run id is refused by Starter.start
// and not here: the identity of a start is the state's own record, and this
// file does not keep a second spelling of that check.
func (r *ResidentLaunch) Launch(ctx context.Context, spec Spec, runID string) (string, error) {
	if err := r.complete(); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	taken, holder, err := r.Claim(ctx, runID, now)
	if err != nil {
		return "", fmt.Errorf("orchestrator: run %s: its launch claim could not be taken, so nothing was started: %s",
			safetext.Field(runID), safetext.OneLine(err.Error()))
	}
	if !taken {
		return "", fmt.Errorf("orchestrator: run %s is claimed by %s, and a launcher starts nothing it does not hold: another launcher is between that run's task and its launch record (internal/runstore's claiming)",
			safetext.Field(runID), safetext.OneLine(holder))
	}
	key, err := r.Token(ctx, spec, runID)
	if err != nil {
		// The token is written before any execution exists, so a mint that
		// failed cannot have started anything: the claim goes back, or the run
		// waits out ClaimTTL for a launch nothing made.
		return "", r.release(ctx, runID, fmt.Errorf("orchestrator: run %s: its budget token was not minted, so nothing was started: %s",
			safetext.Field(runID), safetext.OneLine(err.Error())))
	}
	started, err := r.Start(ctx, spec, runID, key)
	if err != nil {
		// The claim STAYS, for the reason in the doc comment above: the request
		// may have been accepted before it failed, and the safe reading of an
		// unknown start is that the run is going.
		return "", fmt.Errorf("orchestrator: run %s was not started, and its launch claim is kept so nothing starts it again before the claim goes back: %s",
			safetext.Field(runID), safetext.OneLine(err.Error()))
	}
	return started, nil
}

// complete is the fail-closed guard every launch begins with, Starter.begin's
// own shape: a step this launcher has no seam for is a launch it cannot make,
// and the refusal names the step rather than leaving a nil to be called.
func (r *ResidentLaunch) complete() error {
	var missing []string
	if r.Claim == nil {
		missing = append(missing, "the run's launch claim")
	}
	if r.Token == nil {
		missing = append(missing, "its budget token")
	}
	if r.Start == nil {
		missing = append(missing, "the job execution")
	}
	if len(missing) > 0 {
		return fmt.Errorf("orchestrator: the resident launcher carries no seam for %s, and a launch it cannot make is one it must not claim",
			strings.Join(missing, ", "))
	}
	return nil
}

// release gives a claim back and folds a release that failed into the refusal
// rather than swallowing it: a claim left behind is a run that waits ClaimTTL
// for a launch nothing made, which is what the line has to say out loud. The
// cause is what comes back either way, so the refusal a caller sees is still
// the mint's.
func (r *ResidentLaunch) release(ctx context.Context, runID string, cause error) error {
	if r.Release == nil {
		return cause
	}
	if err := r.Release(ctx, runID); err != nil {
		return fmt.Errorf("%w (and the claim of run %s could not be given back, so that run waits %s for a launch nothing made: %s)",
			cause, safetext.Field(runID), ClaimTTL, safetext.OneLine(err.Error()))
	}
	return cause
}

// ResidentRequests is one node's plan split the way a resident pass may act on
// it. requests are the launch requests the node's own record already carries:
// the re-issues of attempts whose run never started, which a starter holding a
// launcher and no commit carries out — the attempt was committed before this
// process woke, and starting it is a start and not a write. others are the
// actions a plan derived that ARE a write's: a first launch, a retry, an
// abandon. They are handed back rather than dropped, so that a caller can say
// how many steps it left and why, and they belong to whoever holds the node's
// credential (`fugaro orchestrator patch`, D1223/D1241).
//
// It is Plan's own reading of the node and nothing beside it: the same items in
// the same order (an important follow-up ahead of new work), the same caps, the
// same breaker, and the same ClaimTTL window. A caller that hands the ZERO Caps
// — a pass that cannot read the installation's guardrails reads no cap at all,
// and the Tick's own rule is that the zero Caps plans nothing — gets no first
// launch and no retry, and its requests all the same: a re-issue starts the
// attempt that was already committed and counted, so it spends nothing a cap is
// there to bound (planNode's own reason).
//
// What no part of this reads is Queue: the items waiting for a person are not a
// launcher's work — nothing may start them (G6, F21) — and the list a message
// to a person is built from is the consumer half's (P5.1f). The caps and the
// breaker reach a plan through Plan, which is what makes this a split of the
// daemon's own reading rather than a second planning of the same node.
func ResidentRequests(s State, view RunView, caps Caps, now time.Time) (requests, others []Action) {
	for _, a := range Plan(s, view, caps, now) {
		if a.Kind == ActionReissue {
			requests = append(requests, a)
			continue
		}
		others = append(others, a)
	}
	return requests, others
}
