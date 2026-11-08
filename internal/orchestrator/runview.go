// The run view a tick fills: what the runs behind the state's intents reported
// (O7).
//
// The state records one thing about a run that no run can write: the intent
// the daemon committed before it started anything, which carries the run id the
// daemon minted for that attempt (D1231). Everything a tick folds, it folds
// through that record, and the reader below is asked about the run ids the
// intents carry and about nothing else. That is the whole of the join, and it
// is what keeps a run from naming the item its work lands on: a run's own
// record — its heartbeat, its title, its label — is the run's to write, so a
// fold that read one would let whoever can write a run record write an item's
// phase.

package orchestrator

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// RunReport is what the reader knows about one run id the state recorded:
// whether its run ever started, and how the attempt ended once it has.
//
// Outcome is the orchestrator's own vocabulary and not a run record's status
// (state.go): the reader is the one that reads both and names one in terms of
// the other. A run that started and has not reported yet carries no outcome,
// which is what leaves its item running.
type RunReport struct {
	RunID    string
	Started  bool
	Finished bool
	Outcome  Outcome
}

// RunViewReader reads the runs the state recorded. It is handed the run ids of
// the attempts that have not reported and answers one report per id it knows,
// or an error for a read that did not happen. The daemon implements it over the
// run records; nothing in this package reaches a store of its own, which is
// what keeps a tick a deterministic pass over the node.
type RunViewReader interface {
	Runs(ctx context.Context, runIDs []string) ([]RunReport, error)
}

// foldPhase is the phase an item takes when the run behind its attempt reports
// this outcome, and false for an outcome no run reports. An outcome it does not
// read is refused by the caller rather than guessed at: the alternative is an
// item whose attempt has ended and whose phase says it is still going, with
// nothing on the node that a person could read as the reason.
func foldPhase(o Outcome) (Phase, bool) {
	switch o {
	case OutcomeSucceeded:
		return PhaseDone, true
	case OutcomeFailed, OutcomeRefused:
		// A refusal is an attempt like any other (O6): a run started and came
		// back saying it would not do the work, and the item is not one whose
		// phase says it is going. It is not a failure a retry answers —
		// failedUnder reads the outcome — so the two share a phase and not a
		// consequence.
		return PhaseFailed, true
	case OutcomeCancelled:
		return PhaseCancelled, true
	}
	return "", false
}

// openRunIDs is the run ids a tick asks the reader about: the attempts that
// have not reported. An attempt that reported is history — its outcome is the
// state's own, and a reader that answered late for it would have nothing to
// write over.
func openRunIDs(s State) []string {
	var ids []string
	for _, it := range s.Intents {
		if it.Outcome == "" {
			ids = append(ids, it.RunID)
		}
	}
	return ids
}

// hasOpenIntent reports whether any attempt of the node has not reported, which
// is a run in flight or one the platform may still be starting.
func hasOpenIntent(s State) bool {
	for _, it := range s.Intents {
		if it.Outcome == "" {
			return true
		}
	}
	return false
}

// viewOf is what a plan reads about the runs: the reports as O6's RunView. A
// run the reader did not answer about is not in it, which is the answer
// recovery acts on — an attempt whose run never started.
func viewOf(reports []RunReport) RunView {
	view := RunView{Runs: make([]Run, 0, len(reports))}
	for _, r := range reports {
		view.Runs = append(view.Runs, Run{RunID: r.RunID, Started: r.Started})
	}
	return view
}

// foldOps is the fold itself: what the finished runs mean for the items, as the
// ops that record it and the notifications that go with them.
//
// It walks the intents, not the reports — the join is the daemon's own record
// of which run belongs to which attempt — and a report for a run id no intent
// carries is never looked at. Each finished attempt becomes one op: the
// intent's outcome, and the item's phase when the phase table allows the move.
// A phase that does not is left alone rather than refused: the item was moved
// by somebody else while the run was going, and the outcome is still the thing
// to record, because it is what stops the attempt from being re-issued.
func foldOps(s State, reports []RunReport) ([]Op, []Notification, error) {
	byRun := make(map[string]RunReport, len(reports))
	for _, r := range reports {
		byRun[r.RunID] = r
	}
	var ops []Op
	var notes []Notification
	for _, it := range s.Intents {
		if it.Outcome != "" {
			continue
		}
		// An intent whose item the state no longer carries has no phase to
		// move, and an upsert for it would be refused for want of a
		// declaration: what is left is retention's business (state.go).
		w, ok := s.Item(IntentItem(it.ID))
		if !ok {
			continue
		}
		r, ok := byRun[it.RunID]
		if !ok || !r.Finished {
			continue
		}
		phase, known := foldPhase(r.Outcome)
		if !known {
			return nil, nil, fmt.Errorf("%w: the run %q reported %q for the attempt %q, and that is not an outcome a run reports",
				ErrInvalid, safetext.OneLine(it.RunID), safetext.OneLine(string(r.Outcome)), safetext.OneLine(it.ID))
		}
		outcome := r.Outcome
		op := WorkItemOp{ID: w.ID, Intent: &IntentOp{ID: it.ID, Outcome: &outcome}}
		if !w.Phase.Done() && w.Phase.CanMoveTo(phase) {
			op.Phase = &phase
		}
		ops = append(ops, Op{Op: OpWorkItemUpsert, WorkItem: &op})
		notes = append(notes, Notification{
			Kind:    NotifyRunFinished,
			Subject: it.RunID,
			Text:    fmt.Sprintf("run %s ended %s for %q", it.RunID, outcome, w.Title),
		})
		if len(ops) >= MaxOps {
			// One op per finished attempt, and a patch carries at most MaxOps:
			// the rest are folded by the next tick, which is what level
			// triggered means. This release's MaxIntents is what makes the
			// bound unreachable; a release that raised it without raising this
			// one would leave an oversized patch refused on every tick.
			break
		}
	}
	return ops, notes, nil
}
