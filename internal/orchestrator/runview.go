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

	"github.com/dimipaun/fugaro/internal/runstore"
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
	// PRNumber is the pull request the run left, and 0 for a run that left
	// none. It is the record's own answer (result.json's `pr`, F25), so a run
	// that opened one carries it from the moment it did, and it never moves
	// after that.
	//
	// The fold reads it to tell work that is finished from work that is
	// waiting on a merge (D2705): a succeeded run that left a pull request
	// leaves its item in `review`. The merge read reads it for the number the
	// host is asked about, which is why it is here and not a second read of
	// the record: one attempt left one pull request, and both readers of that
	// fact read it from the report.
	PRNumber int
	// WorkspaceRefusal is the marker the run's record carries when the
	// workspace's own credentials service refused it, and which of the
	// workspace's refusals it was (runstore.Halt.WorkspaceRefusal, O19). It is
	// empty for every run no workspace refused.
	//
	// It is here and not read off Halt because the two are separate facts a
	// reader fills from separate places in the record, and because the seam
	// that answers whether this is a workspace's refusal is handed both: the
	// marker is the run's own write, so a rule over it has to hold it against
	// the halt beside it rather than believe it (ee/orchestrator).
	WorkspaceRefusal string
	// Halt is the halt the run's record carries, as the record states it. A
	// run that ended without one carries the zero Halt.
	Halt Halt
	// Discovered is what the run reported about the order of the work: the
	// items its own work blocks and the ones it found it needed, each named by
	// a plan task or by a path an item's footprint covers, with the run's own
	// one-line why (X2, D2709). It is nil for a run that reported nothing,
	// which is the record it always wrote.
	//
	// It is here because the fold is the only reader of it and the record is
	// the only place it lives: every entry is the run's own agent's words, so
	// the fold bounds it again on read (runstore.Discovered.Bounded) and reads
	// every entry as a claim about a graph rather than as an order — what a run
	// can reach is an edge in front of an item that has not launched, and the
	// fence decides whether even that lands (fold_discovered.go).
	Discovered *runstore.Discovered
	// ConflictPaths are the paths a rebase or a push of this run met a conflict
	// on, in the order it met them (X2, D2710). A run records paths and never
	// an item: it cannot know which pull request it met, so which item of the
	// graph each path is about is what the daemon matches them against, and
	// nothing a run says names that item.
	ConflictPaths []string
	// Pause is the node the credentials service wrote for this run when it
	// suspended itself (C3, D3189), and nil for a run it wrote none for. It is
	// the service's own account of the pause — the instant the run comes back,
	// and whether its launcher cancelled the pause before then — and never the
	// job's: a suspended run that named its own resume time would be an agent
	// choosing when to spend, which is why the node is not written by the run
	// that is suspended (R8, R19).
	//
	// It is here because the fold is the only reader of it: what a person is
	// told about a run that suspended itself is the pause (notify.go), and the
	// instant cannot be read off the record, which carries none. The reader
	// fills it from the node the service wrote, and only for a record that ended
	// `paused` (internal/cli's orchestratorRunView.pauseNode): a run the service
	// wrote no node for carries none, and its report is the ending it had.
	Pause *runstore.PauseNode
}

// Halt is the halt a run's record carries, as the record states it: what
// stopped the run, at what scope, and the sentence the run's operator reads.
// It is the three fields a refusal is corroborated against and no more — the
// fold decides nothing else with them, and nothing here reads a database or a
// clock.
type Halt struct {
	Reason string
	Scope  string
	Detail string
}

// RunViewReader reads the runs the state recorded. It is handed the run ids of
// the attempts that have not reported — and the run ids of the items waiting on
// a merge, whose attempts have reported but whose pull requests the merge read
// is about — and answers one report per id it knows, or an error for a read
// that did not happen. The daemon implements it over the run records; nothing in
// this package reaches a store of its own, which is what keeps a tick a
// deterministic pass over the node.
type RunViewReader interface {
	Runs(ctx context.Context, runIDs []string) ([]RunReport, error)
}

// foldPhase is the phase an item takes when the run behind its attempt reports
// this outcome and left, or did not leave, a pull request, and false for an
// outcome no run reports. An outcome it does not read is refused by the caller
// rather than guessed at: the alternative is an item whose attempt has ended
// and whose phase says it is still going, with nothing on the node that a
// person could read as the reason.
//
// leftPR is D2705's whole distinction: a succeeded run that left a pull
// request leaves the item in `review`, where it waits for that pull request to
// merge, and one that left none is `done` at once — there is nothing to wait
// for, and an item held in `review` for a merge that will never come would
// hold everything that depends on it for ever. What leaves `review` is the
// merge read, the only reader that can say a pull request merged or closed.
func foldPhase(o Outcome, leftPR bool) (Phase, bool) {
	switch o {
	case OutcomeSucceeded:
		if leftPR {
			return PhaseReview, true
		}
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

// noticeForRun is what a tick tells a person about one attempt that reported:
// the line the fold has always said, or — where the chain's exhaustion is why
// the attempt ended — the pause that is holding it or the halt that replaced
// it (C3, notify.go). Both readings are read off what the report already
// carries and neither is judged here: a paused node is the service's own
// record that this run is coming back at an instant, and `no_credential` is
// the halt the service's own answer or a refused `/pause` leaves, so the fold
// says what the two sides wrote and decides nothing about time or credits.
//
// A paused attempt is told as a pause and not as the outcome the report
// carries: the run neither finished the work nor failed at it — it stopped,
// and the chain is in flight until the orchestrator resumes it (D3195), which
// is the reading a person needs. Which outcome a reader gives a run whose
// record says `paused` is that reader's own work (PR11, PR12) and this notice
// does not depend on it: what makes it a pause is the node, not the word.
func noticeForRun(runID, title string, r RunReport, outcome Outcome) Notification {
	switch {
	case r.Pause != nil:
		return pausedNotice(runID, title, *r.Pause)
	case r.Halt.Reason == string(runstore.HaltNoCredential):
		return pauseHaltedNotice(runID, r.Halt)
	}
	return Notification{
		Kind:    NotifyRunFinished,
		Subject: runID,
		Text:    fmt.Sprintf("run %s ended %s for %q", runID, outcome, title),
	}
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
		phase, known := foldPhase(r.Outcome, r.PRNumber > 0)
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
		notes = append(notes, noticeForRun(it.RunID, w.Title, r, outcome))
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
