package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// ClaimTTL is how long an attempt whose run has reported nothing is left alone
// before recovery acts on it: the same ten minutes internal/runstore gives a
// launch claim, and the window O8's launcher takes a stale claim over after.
// The package carries its own number because it imports no launch machinery —
// a plan is as pure as the state it is made of.
const ClaimTTL = 10 * time.Minute

// Spec is what a launch starts from: the declarative half of a work item,
// which is the half a person approves.
type Spec struct {
	ItemID    string
	Title     string
	Repo      string
	Recipe    string
	RigorTier string
	Brief     string
}

// SpecOf is the launch spec of a work item. It reads the item's own fields and
// nothing beside them, and SpecDigest reads the item, so what a launcher is
// asked to start is the spec that was approved or nothing at all.
func SpecOf(w WorkItem) Spec {
	return Spec{
		ItemID:    w.ID,
		Title:     w.Title,
		Repo:      w.Repo,
		Recipe:    w.Recipe,
		RigorTier: w.RigorTier,
		Brief:     w.Brief,
	}
}

// Launcher starts one run for one attempt. It is handed the run id the state
// has already recorded and returns the run id it started; a launcher that
// comes back with another one is refused, because a second run id is a run the
// state knows nothing about (O8 implements this over `fugaro run`; the state
// package imports no command).
type Launcher interface {
	Launch(ctx context.Context, spec Spec, runID string) (string, error)
}

// Run is what a tick's read-only view of the runs knows about one run id.
type Run struct {
	RunID   string
	Started bool
}

// RunView is what a plan reads about the runs behind the intents. A run that
// is not in it is one that never started, which is the answer recovery acts
// on.
type RunView struct {
	Runs []Run
}

// Run returns what the view knows about a run id.
func (v RunView) Run(id string) Run {
	for _, r := range v.Runs {
		if r.RunID == id {
			return r
		}
	}
	return Run{RunID: id}
}

// ActionKind is what a plan decided to do about one item.
type ActionKind string

const (
	// ActionLaunch starts the first attempt under a person's approval.
	ActionLaunch ActionKind = "launch"
	// ActionRetry starts the one further attempt an approval whose attempt
	// failed still covers.
	ActionRetry ActionKind = "retry"
	// ActionReissue starts an attempt whose run id is already recorded and
	// whose run never started. It is the same launch, not a second one.
	ActionReissue ActionKind = "reissue"
	// ActionAbandon closes an attempt nothing may start.
	ActionAbandon ActionKind = "abandon"
)

// Action is one thing a plan decided: the item, the attempt and the intent it
// is for, and the spec and digest a launch would carry. RunID is set on a
// re-issue, where the run id is the one the state already recorded.
type Action struct {
	Kind       ActionKind
	ItemID     string
	IntentID   string
	Attempt    int
	RunID      string
	Spec       Spec
	Digest     Digest
	ApprovalAt time.Time
	Reason     string
}

// Plan is one cycle's decision: the launches and retries the state, the view
// of the runs and the caps allow, in the order the items are declared. It
// reads and changes nothing — a plan is a proposal, and the tick that carries
// it out commits each intent before anything starts.
//
// It hesitates in the only safe direction. Nothing is planned with the kill
// switch on, nothing while the node is over its size cap (the intents a count
// is read against would no longer be the whole record), nothing for an item
// with an attempt that has not reported, nothing for an item nobody approved,
// nothing whose spec has changed since the approval, and nothing while a
// dependency is unfinished. What is left is a first attempt under a person's
// approval, or the one retry that approval still covers.
func Plan(s State, view RunView, caps Caps, now time.Time) []Action {
	if caps.KillSwitch || OverCap(s) {
		return nil
	}
	counts := CountsOf(s, now)
	var actions []Action
	planned := 0
	for _, w := range s.WorkItems {
		if it, ok := openIntentOf(s, w.ID); ok {
			// A run is in flight or never started, and what is left to decide
			// is that attempt's own recovery. It is not capped: a re-issue
			// starts the attempt that was already committed and counted, so it
			// spends nothing a cap is there to bound.
			if a, ok := recovery(s, view, w, it, now); ok {
				actions = append(actions, a)
			}
			continue
		}
		a, ok := nextAttempt(s, w)
		if !ok {
			continue
		}
		if !counts.Room(caps, planned) {
			// The caps bound the cycle's whole list, so an item that does not
			// fit is passed over rather than being planned past them — and the
			// items after it still get their own recovery.
			continue
		}
		actions = append(actions, a)
		planned++
	}
	return actions
}

// CommitFunc persists a state a tick has changed. It is the node's write and
// the whole of the atomicity here: a commit that failed wrote nothing, and
// what the starter does after one is nothing.
type CommitFunc func(ctx context.Context, s State) error

// MintFunc mints the run id for one attempt. It is a function because the
// state package knows no run ids (internal/task mints them in the daemon), and
// what it hands back has to be a fresh one: the same id twice is the same run.
type MintFunc func(now time.Time) (string, error)

// Starter carries out the actions a plan called for, intent first: it mints
// the run id, commits the intent that records it, and asks the launcher to
// start it only then. A crash between any two of those leaves a state that
// says which run id belongs to which attempt, so the next tick re-issues that
// same launch instead of minting a second one — which is the whole of what
// makes a daemon that dies mid-cycle spend once (D1231).
type Starter struct {
	Launcher Launcher
	Commit   CommitFunc
	Mint     MintFunc
}

// Start carries out one action and returns the state the node now holds. A
// refusal — a commit that did not land, a launcher that would not start, an
// action built on a state somebody else has since changed — is an error, and
// what it leaves behind is exactly what was committed: an intent for a launch
// that did not happen is the record recovery re-issues. A launch and a retry
// record their attempt before anything starts, and a re-issue is checked
// against the state in hand before it starts, so nothing here starts a run the
// state does not carry.
func (d Starter) Start(ctx context.Context, s State, a Action, now time.Time) (State, error) {
	switch a.Kind {
	case ActionLaunch, ActionRetry:
		return d.begin(ctx, s, a, now)
	case ActionReissue:
		w, err := reissuable(s, a)
		if err != nil {
			return s, err
		}
		return s, d.start(ctx, SpecOf(w), a.RunID)
	case ActionAbandon:
		return d.abandon(ctx, s, a, now)
	}
	return s, fmt.Errorf("%w: %q is not an action this release carries out", ErrInvalid, safetext.OneLine(string(a.Kind)))
}

// begin is the intent-first start of a new attempt: mint the run id, commit
// the intent that records it, and start it only once the write has landed.
func (d Starter) begin(ctx context.Context, s State, a Action, now time.Time) (State, error) {
	if d.Mint == nil || d.Commit == nil || d.Launcher == nil {
		return s, errors.New("orchestrator: the starter carries no launcher, no commit or no mint, and a start it cannot record is one it must not make")
	}
	runID, err := d.Mint(now)
	if err != nil {
		return s, err
	}
	op := WorkItemOp{
		ID:     a.ItemID,
		RunID:  &runID,
		Intent: &IntentOp{ID: a.IntentID, RunID: &runID, SpecDigest: &a.Digest, ApprovalAt: &a.ApprovalAt},
	}
	if w, ok := s.Item(a.ItemID); ok && w.Phase != PhaseQueued {
		// failed and blocked reach queued on their way to an attempt, and the
		// phase table has no direct edge from either: the same write that
		// declares the attempt moves the item first.
		queued := PhaseQueued
		op.Phase = &queued
	}
	next, err := Apply(s, Patch{BaseRev: s.Rev, Ops: []Op{{Op: OpWorkItemUpsert, WorkItem: &op}}}, Actor{Class: Daemon, Now: now})
	if err != nil {
		return s, err
	}
	if err := d.Commit(ctx, next); err != nil {
		return s, err
	}
	return next, d.start(ctx, a.Spec, runID)
}

// start asks the launcher for one run id and refuses an answer that is not the
// id the state recorded.
func (d Starter) start(ctx context.Context, spec Spec, runID string) error {
	if d.Launcher == nil {
		return errors.New("orchestrator: the starter carries no launcher, and a start it cannot make is one it must not claim")
	}
	got, err := d.Launcher.Launch(ctx, spec, runID)
	if err != nil {
		return err
	}
	if got != runID {
		return fmt.Errorf("%w: the launcher started %q for the launch recorded as %q, and a second run id is a run the state does not carry", ErrInvalid, safetext.OneLine(got), safetext.OneLine(runID))
	}
	return nil
}

// close abandons an attempt nothing may start. The intent records that nothing
// ran under its run id, and the item is left blocked with the reason on it,
// where a person can see it: an item left running with no run behind it is one
// no tick could ever pick up again.
func (d Starter) abandon(ctx context.Context, s State, a Action, now time.Time) (State, error) {
	if d.Commit == nil {
		return s, errors.New("orchestrator: the starter carries no commit, and a change it cannot record is one it must not make")
	}
	outcome := OutcomeAbandoned
	op := WorkItemOp{ID: a.ItemID, Intent: &IntentOp{ID: a.IntentID, Outcome: &outcome}}
	if w, ok := s.Item(a.ItemID); ok && !w.Phase.Done() && w.Phase != PhaseBlocked {
		blocked := PhaseBlocked
		op.Phase = &blocked
	}
	if a.Reason != "" {
		note := a.Reason
		op.Note = &note
	}
	next, err := Apply(s, Patch{BaseRev: s.Rev, Ops: []Op{{Op: OpWorkItemUpsert, WorkItem: &op}}}, Actor{Class: Daemon, Now: now})
	if err != nil {
		return s, err
	}
	if err := d.Commit(ctx, next); err != nil {
		return s, err
	}
	return next, nil
}
