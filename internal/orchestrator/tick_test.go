package orchestrator_test

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The reconcile tick's tests (O7).
//
// A tick is the whole of what a daemon does: read the node, fold what the runs
// behind its intents reported, plan, carry the plan out, and commit. These
// tests drive it over the real store (internal/gcpfake) with a hand-made run
// view, so what is pinned here is the loop itself: a finished run reaching the
// item's phase, a second tick that changes nothing it already did, the one
// notification per event, and a tick that never calls a model.
//
// The launcher, the caps and the planner are O6's own tests; here they are the
// surroundings a tick is wired to.

// tickReader is the run view as a test sets it: the reports it holds and the
// run ids it was asked about. It answers the ids it was asked about, the way a
// reader reading one run record per id does; blunt makes it answer everything
// it holds, whichever ids it was asked for, which is how a reader that joined
// on something else would look.
type tickReader struct {
	reports []orchestrator.RunReport
	asked   []string
	err     error
	blunt   bool
}

func (r *tickReader) Runs(_ context.Context, runIDs []string) ([]orchestrator.RunReport, error) {
	r.asked = append(r.asked, runIDs...)
	if r.err != nil {
		return nil, r.err
	}
	var out []orchestrator.RunReport
	for _, rep := range r.reports {
		if r.blunt || slices.Contains(runIDs, rep.RunID) {
			out = append(out, rep)
		}
	}
	return out, nil
}

// tickSink is a Notifier a test reads: what it was told, and the failure it is
// set to answer with.
type tickSink struct {
	got  []orchestrator.Notification
	err  error
	refs []string
}

func (s *tickSink) Notify(_ context.Context, n orchestrator.Notification) error {
	if s.err != nil {
		return s.err
	}
	s.got = append(s.got, n)
	return nil
}

// harness is a tick and the four things it acts through, so a test asserts on
// what the tick did rather than on how it was wired.
type harness struct {
	t        *testing.T
	node     *orchestrator.Node
	db       *gcpfake.RTDB
	reader   *tickReader
	launcher *o6Launcher
	mint     *o6Mint
	sink     *tickSink
	tick     *orchestrator.Tick
}

// newHarness is a tick over one state node: the node is seeded with the state
// the test hands it through the store's own write, so what the tick reads back
// is a document this release accepted, and the run ids are the ones the minter
// hands out in order.
func newHarness(t *testing.T, s orchestrator.State, now time.Time, runs ...string) *harness {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	node := mustNode(t, rtdbClient(t, f), nil)
	ctx := context.Background()
	_, etag, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("reading the empty node: %v", err)
	}
	if err := node.Save(ctx, s, etag); err != nil {
		t.Fatalf("seeding the node with the test's state: %v", err)
	}
	h := &harness{
		t:        t,
		node:     node,
		db:       f,
		reader:   &tickReader{},
		launcher: &o6Launcher{},
		mint:     &o6Mint{t: t, runs: runs},
		sink:     &tickSink{},
	}
	h.tick = &orchestrator.Tick{
		Store:    node,
		Runs:     h.reader,
		Launcher: h.launcher,
		Mint:     h.mint.mint,
		Caps:     o6Caps(),
		Now:      clockAt(now),
		Notifier: h.sink,
	}
	return h
}

// step runs one cycle of the tick. A cycle these tests did not set up to fail
// is a cycle that failed for a reason the test is about to name, so it stops
// here rather than returning.
func (h *harness) step() orchestrator.Report {
	h.t.Helper()
	rep, err := h.tick.Step(context.Background())
	if err != nil {
		h.t.Fatalf("the tick ended as %v", err)
	}
	return rep
}

// state is the node as it now stands, read back through the store rather than
// remembered from what the tick returned.
func (h *harness) state() orchestrator.State {
	h.t.Helper()
	s, _, err := h.node.Load(context.Background())
	if err != nil {
		h.t.Fatalf("reading the node back: %v", err)
	}
	return s
}

// rev is the revision the node itself carries.
func (h *harness) rev() int64 { return storedRev(h.t, h.db, h.node) }

// tickState is the state a tick test starts from: the items the test wants and
// the intents that go with them.
func tickState(t *testing.T, now time.Time, items []orchestrator.WorkItem, intents []orchestrator.Intent) orchestrator.State {
	t.Helper()
	s := stateWithAQueuedItem(t, now)
	s.WorkItems = items
	if intents == nil {
		intents = []orchestrator.Intent{}
	}
	s.Intents = intents
	return s
}

// TestTickMarksAFinishedRunDone is what a tick is for: the run behind an
// attempt reported, and the item takes the phase that outcome means, in the
// write the intent's own outcome lands in.
func TestTickMarksAFinishedRunDone(t *testing.T) {
	now := o6Now()
	const runID = "20261107-090000-abcd"

	t.Run("a run that succeeded leaves the item done", func(t *testing.T) {
		w, it := o6OpenItem("wi-1", runID, now.Add(-time.Hour), now.Add(-30*time.Minute))
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
		h.reader.reports = []orchestrator.RunReport{{RunID: runID, Started: true, Finished: true, Outcome: orchestrator.OutcomeSucceeded}}

		rep := h.step()
		if rep.Folded != 1 {
			t.Fatalf("the tick folded %d finished runs where one reported", rep.Folded)
		}
		if rep.Outcome != orchestrator.CycleProgress {
			t.Errorf("a cycle that folded a finished run ended %q, and a run reaching its item is progress", rep.Outcome)
		}

		got := h.state()
		w1, ok := got.Item("wi-1")
		if !ok {
			t.Fatalf("the node no longer carries wi-1")
		}
		if w1.Phase != orchestrator.PhaseDone {
			t.Errorf("the item is %s after its run succeeded, not done", w1.Phase)
		}
		it1 := intentByID(t, got, "wi-1:1")
		if it1.Outcome != orchestrator.OutcomeSucceeded {
			t.Errorf("the intent ended %q, and the run reported succeeded", it1.Outcome)
		}
		if w1.Approved != nil {
			t.Errorf("a finished item still carries an approval, which a later tick could launch under")
		}
		if len(h.sink.got) != 1 || h.sink.got[0].Kind != orchestrator.NotifyRunFinished || h.sink.got[0].Subject != runID {
			t.Errorf("the tick told a person %+v, and one finished run is one notification about it", h.sink.got)
		}
	})

	// The item a person moved while its run was going is what the fold's phase
	// guard is for. The outcome is still the thing to record — it is what stops
	// the attempt from being re-issued — while the phase is a move the table
	// refuses, and a patch one op of which is refused fails the whole cycle
	// before it plans anything: a fold that insisted on the phase would wedge
	// the daemon on every tick, not once.
	t.Run("a run that ends after a person cancelled the item records the outcome only", func(t *testing.T) {
		w, it := o6OpenItem("wi-1", runID, now.Add(-time.Hour), now.Add(-30*time.Minute))
		w.Phase = orchestrator.PhaseCancelled
		// A node cannot carry a done item that keeps an approval (validate.go),
		// so the approval a person's cancellation leaves behind is gone.
		w.Approved = nil
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
		h.reader.reports = []orchestrator.RunReport{{RunID: runID, Started: true, Finished: true, Outcome: orchestrator.OutcomeSucceeded}}

		rep := h.step()
		if rep.Folded != 1 {
			t.Fatalf("the tick folded %d finished runs where one reported", rep.Folded)
		}
		got := h.state()
		if w1 := got.WorkItems[0]; w1.Phase != orchestrator.PhaseCancelled {
			t.Errorf("the item is %s, and a run's outcome is not what undoes a person's cancellation", w1.Phase)
		}
		if o := intentByID(t, got, "wi-1:1").Outcome; o != orchestrator.OutcomeSucceeded {
			t.Errorf("the attempt ended %q, and its run reported succeeded", o)
		}
		if second := h.step(); second.Folded != 0 {
			t.Errorf("a second tick folded %d runs, and the attempt it folded had reported", second.Folded)
		}
	})

	// The mapping is the fold's whole decision, so it is pinned as a table
	// rather than one case at a time: an outcome a run reports is a phase, and
	// anything else is refused rather than guessed.
	t.Run("the outcome a run reported is the phase the item takes", func(t *testing.T) {
		for _, tc := range []struct {
			outcome orchestrator.Outcome
			phase   orchestrator.Phase
			known   bool
		}{
			{orchestrator.OutcomeSucceeded, orchestrator.PhaseDone, true},
			{orchestrator.OutcomeFailed, orchestrator.PhaseFailed, true},
			{orchestrator.OutcomeRefused, orchestrator.PhaseFailed, true},
			{orchestrator.OutcomeCancelled, orchestrator.PhaseCancelled, true},
			{orchestrator.OutcomeAbandoned, "", false},
			{orchestrator.Outcome(""), "", false},
			{orchestrator.Outcome("halted"), "", false},
		} {
			phase, known := orchestrator.FoldPhase(tc.outcome)
			if known != tc.known || phase != tc.phase {
				t.Errorf("%q folds to (%q, %v), and this release reads it as (%q, %v)", tc.outcome, phase, known, tc.phase, tc.known)
			}
		}
	})

	t.Run("a failed run fails the item and the one retry follows in the same cycle", func(t *testing.T) {
		const retryID = "20261107-120000-bbbb"
		w, it := o6OpenItem("wi-1", runID, now.Add(-time.Hour), now.Add(-30*time.Minute))
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now, retryID)
		h.reader.reports = []orchestrator.RunReport{{RunID: runID, Started: true, Finished: true, Outcome: orchestrator.OutcomeFailed}}

		rep := h.step()
		got := h.state()
		if o := intentByID(t, got, "wi-1:1").Outcome; o != orchestrator.OutcomeFailed {
			t.Errorf("the first attempt ended %q, and its run reported failed", o)
		}
		if rep.Carried != 1 {
			t.Fatalf("the cycle carried %d actions, and the approval's one retry is left", rep.Carried)
		}
		w1 := got.WorkItems[0]
		if w1.Phase != orchestrator.PhaseRunning || w1.RunID != retryID || w1.Attempts != 2 {
			t.Errorf("the item is %+v, and a failure under a standing approval is retried once", w1)
		}
		if len(h.sink.got) != 1 || h.sink.got[0].Kind != orchestrator.NotifyRunFinished {
			t.Errorf("the cycle told a person %+v about the run that ended", h.sink.got)
		}
	})

	t.Run("a run that started and has not reported changes nothing", func(t *testing.T) {
		w, it := o6OpenItem("wi-1", runID, now.Add(-time.Hour), now.Add(-30*time.Minute))
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
		before := h.rev()
		h.reader.reports = []orchestrator.RunReport{{RunID: runID, Started: true}}

		rep := h.step()
		if rep.Folded != 0 || rep.Carried != 0 {
			t.Errorf("a tick with a run in flight folded %d and carried %d, and it has nothing to do", rep.Folded, rep.Carried)
		}
		if h.rev() != before {
			t.Errorf("a tick with a run in flight wrote the node")
		}
		if len(h.sink.got) != 0 {
			t.Errorf("a tick with a run in flight told a person %+v", h.sink.got)
		}
		if got := h.state().WorkItems[0].Phase; got != orchestrator.PhaseRunning {
			t.Errorf("the item is %s while its run is still going", got)
		}
	})
}

// TestTickIsIdempotentOnRepeat is the rule D1229 rests on: a tick derives what
// to do from the node and the run view, so running it twice over one node is
// the same as running it once. The second tick must not launch the item again
// — the attempt it committed is a run the state carries — and must write
// nothing at all.
//
// Red step: with a commit that did not write the node (the state the tick
// returned kept in memory only), the second tick reads the node as it was
// before the first, plans the same launch, mints a second run id and starts a
// second run. Watched fail; restored; see the pull request.
func TestTickIsIdempotentOnRepeat(t *testing.T) {
	now := o6Now()
	const runID = "20261107-120000-aaaa"

	t.Run("a second tick launches nothing and writes nothing", func(t *testing.T) {
		w := o6Item("wi-1", orchestrator.PhaseQueued, now.Add(-time.Hour))
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, nil), now, runID)

		first := h.step()
		if first.Carried != 1 || first.Folded != 0 {
			t.Fatalf("the first tick carried %d and folded %d, and it was to start one attempt", first.Carried, first.Folded)
		}
		launched := slices.Clone(h.launcher.started)
		if len(launched) != 1 || launched[0] != runID {
			t.Fatalf("the first tick started %v, not the one attempt it minted (%s)", launched, runID)
		}
		asked := slices.Clone(h.reader.asked)
		if len(asked) != 0 {
			t.Fatalf("the first tick asked about %v before anything was recorded", asked)
		}
		after := h.rev()

		// The run the first tick started is going, which is what the second
		// tick's view of it says.
		h.reader.reports = []orchestrator.RunReport{{RunID: runID, Started: true}}
		second := h.step()
		if len(h.launcher.started) != 1 {
			t.Fatalf("the second tick started %v and the first had already started the attempt", h.launcher.started)
		}
		if h.mint.calls != 1 {
			t.Errorf("the tick minted %d run ids for one attempt", h.mint.calls)
		}
		if h.rev() != after {
			t.Errorf("the second tick wrote the node, and a level-triggered tick that has nothing to do writes nothing")
		}
		if second.Carried != 0 || second.Folded != 0 {
			t.Errorf("the second tick carried %d and folded %d", second.Carried, second.Folded)
		}
		if second.Outcome != orchestrator.CycleInFlight {
			t.Errorf("a cycle whose only news is a run still going ended %q", second.Outcome)
		}
	})

	t.Run("past the claim window it re-issues the recorded run id, never a second one", func(t *testing.T) {
		at := now.Add(-2 * orchestrator.ClaimTTL)
		w, it := o6OpenItem("wi-1", runID, at.Add(-time.Hour), at)
		h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)

		rep := h.step()
		if rep.Carried != 1 {
			t.Fatalf("the tick carried %d attempts for an attempt whose run never started", rep.Carried)
		}
		if len(h.launcher.asked) != 1 || h.launcher.asked[0] != runID {
			t.Fatalf("the tick asked the launcher for %v, and the attempt was recorded under %s", h.launcher.asked, runID)
		}
		if h.mint.calls != 0 {
			t.Errorf("the tick minted %d run ids while re-issuing one that was already recorded", h.mint.calls)
		}
	})
}

// TestTickNotifiesOnceForADecision is the other half of the notifications: the
// question a tripped breaker raises is told to a person once, not once per
// tick. A level-triggered loop has no cursor, so what keeps it to one is that
// it says only what it writes — the cycle that commits the question says so,
// and every cycle after it finds the question there and says nothing.
func TestTickNotifiesOnceForADecision(t *testing.T) {
	now := o6Now()
	h := newHarness(t, tickState(t, now, nil, nil), now)
	h.tick.Breaker = &orchestrator.Breaker{}

	var outcomes []orchestrator.CycleOutcome
	for i := 0; i < orchestrator.BreakerThreshold; i++ {
		outcomes = append(outcomes, h.step().Outcome)
	}
	for i, o := range outcomes {
		if o != orchestrator.CycleEmpty {
			t.Fatalf("cycle %d of an idle node ended %q, and a cycle with nothing to do and nothing done is empty", i+1, o)
		}
	}

	got := h.state()
	if len(got.Decisions) != 1 {
		t.Fatalf("the node carries %d decisions after %d idle cycles", len(got.Decisions), orchestrator.BreakerThreshold)
	}
	d := got.Decisions[0]
	if !strings.HasPrefix(d.ID, orchestrator.BreakerDecisionID+"-") || d.WaitingOn != orchestrator.OnPerson || d.Resolved() {
		t.Fatalf("the tripped breaker left %+v, and it asks a person a question nobody has answered", d)
	}
	if len(h.sink.got) != 1 {
		t.Fatalf("a person was told %d times about one question", len(h.sink.got))
	}
	if n := h.sink.got[0]; n.Kind != orchestrator.NotifyDecisionNeeded || n.Subject != d.ID {
		t.Errorf("the notification is %+v, and the question it is about is %s", n, d.ID)
	}

	// A fourth idle cycle: the question stands, and the daemon says nothing
	// about it a second time.
	after := h.rev()
	fourth := h.step()
	if fourth.Outcome != orchestrator.CycleEmpty {
		t.Errorf("the fourth idle cycle ended %q", fourth.Outcome)
	}
	if h.rev() != after || len(h.state().Decisions) != 1 {
		t.Errorf("the fourth cycle wrote the node: a second question is the one thing a tripped breaker must not ask")
	}
	if len(h.sink.got) != 1 {
		t.Errorf("a person was told %d times about one question", len(h.sink.got))
	}

	// A sink that refuses: the question is on the node, which is the record,
	// and the cycle that could not tell anybody about it still did its work.
	t.Run("a sink that will not take it does not fail the cycle", func(t *testing.T) {
		h := newHarness(t, tickState(t, now, nil, nil), now)
		h.tick.Breaker = &orchestrator.Breaker{}
		h.sink.err = errors.New("no notification channel here")
		var asked orchestrator.Report
		for i := 0; i < orchestrator.BreakerThreshold; i++ {
			asked = h.step()
			if asked.Outcome != orchestrator.CycleEmpty {
				t.Fatalf("cycle %d ended %q", i+1, asked.Outcome)
			}
		}
		if asked.NotifyErr == nil {
			t.Errorf("a notification the sink refused was not reported on the cycle that made it")
		}
		if got := h.state(); len(got.Decisions) != 1 {
			t.Errorf("the node carries %d questions, and the one the breaker asked is the record", len(got.Decisions))
		}
	})
}

// TestTickLeavesAPersonDecisionOpen is the class fence as the daemon meets it:
// a question a person has to answer is answered by a person, so a tick neither
// closes it nor acts as if it were closed — it reads the node, finds nothing it
// may do, and writes nothing.
func TestTickLeavesAPersonDecisionOpen(t *testing.T) {
	now := o6Now()
	s := tickState(t, now, nil, nil)
	s = o6Apply(t, s, orchestrator.Op{Op: orchestrator.OpDecisionAdd, Decision: &orchestrator.DecisionOp{
		ID:        "which-repo",
		Question:  "Which repository should the work land in?",
		WaitingOn: orchestrator.OnPerson,
	}}, orchestrator.Actor{Class: orchestrator.Session, Now: now.Add(-time.Hour)})

	h := newHarness(t, s, now)
	before := h.rev()
	rep := h.step()

	if rep.Outcome != orchestrator.CycleEmpty {
		t.Errorf("a cycle with one unanswered question and nothing to do ended %q", rep.Outcome)
	}
	if h.rev() != before {
		t.Errorf("the tick wrote the node, and it had nothing to write")
	}
	got := h.state()
	if len(got.Decisions) != 1 || got.Decisions[0].Resolved() {
		t.Fatalf("the tick closed a question that waits on a person: %+v", got.Decisions)
	}
	if len(h.sink.got) != 0 {
		t.Errorf("the tick told a person %+v about a question it did not write", h.sink.got)
	}
}

// TestTickJoinsOnTheRecordedRunIDOnly is the join the fold is allowed to make:
// the run id the daemon itself recorded in the intent, and nothing a run says
// about itself. Two things are pinned. The tick asks about the open attempts
// and no others, so a report for a run id the state never recorded has nothing
// to land on; and a report that arrives for an attempt that already reported
// changes nothing, which is what keeps a late reader from rewriting an item
// whose phase has moved on.
func TestTickJoinsOnTheRecordedRunIDOnly(t *testing.T) {
	now := o6Now()
	const (
		first  = "20261107-090000-aaaa"
		second = "20261107-100000-bbbb"
		other  = "20261107-110000-cccc"
	)

	// An item whose first attempt failed under its approval and whose retry is
	// in flight: attempt 1 is closed, attempt 2 is the open one, and the
	// approval is the spent one the retry's own declaration left.
	approvalAt := now.Add(-2 * time.Hour)
	w, it := o6OpenItem("wi-1", second, approvalAt, now.Add(-30*time.Minute))
	w.Attempts = 2
	w.Approved = nil
	spent := approvalAt.UTC()
	w.SpentApprovalAt = &spent
	it.ID = orchestrator.IntentID("wi-1", 2)
	first1 := intentFor(t, "wi-1", 1, approvalAt, approvalAt)
	first1.RunID = first
	first1.Outcome = orchestrator.OutcomeFailed

	h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{first1, it}), now)
	// The reader holds three reports and answers whatever it is asked: a run no
	// intent names, the closed attempt's run, and the open attempt's run.
	h.reader.blunt = true
	h.reader.reports = []orchestrator.RunReport{
		{RunID: other, Started: true, Finished: true, Outcome: orchestrator.OutcomeSucceeded},
		{RunID: first, Started: true, Finished: true, Outcome: orchestrator.OutcomeSucceeded},
		{RunID: second, Started: true, Finished: true, Outcome: orchestrator.OutcomeFailed},
	}

	rep := h.step()
	if got := h.reader.asked; len(got) != 1 || got[0] != second {
		t.Fatalf("the tick asked about %v, and the only attempt that has not reported is %s", got, second)
	}
	if rep.Folded != 1 {
		t.Fatalf("the tick folded %d reports, and only the open attempt's run belongs to an item that has not heard from it", rep.Folded)
	}
	got := h.state()
	if phase := got.WorkItems[0].Phase; phase != orchestrator.PhaseFailed {
		t.Errorf("the item is %s, and the run the daemon recorded for its open attempt failed", phase)
	}
	if o := intentByID(t, got, "wi-1:2").Outcome; o != orchestrator.OutcomeFailed {
		t.Errorf("the open attempt ended %q", o)
	}
	if o := intentByID(t, got, "wi-1:1").Outcome; o != orchestrator.OutcomeFailed {
		t.Errorf("the closed attempt's outcome was rewritten to %q by a report for a run that already reported", o)
	}
}

// TestTickMakesNoModelCall is D1221's rule as an import check, and it is the
// package's own imports that can be checked. The closure cannot be: this
// package already reaches internal/agent on main — the state node imports
// internal/budget for its key escaping (O3), and budget imports
// internal/backend, internal/gateway and internal/runstore, and runstore
// imports internal/agent — so a whole-closure ban would fail before this task's
// code was written. What a tick's own files may not do is import one, and a
// string check over the source would pass on an import added behind a build tag
// or inside another package's file, which is why this asks the toolchain.
func TestTickMakesNoModelCall(t *testing.T) {
	direct := goList(t, "-f", `{{join .Imports " "}}`, "./internal/orchestrator")
	for _, banned := range []string{"internal/agent", "internal/gateway", "internal/runner", "internal/cli"} {
		if strings.Contains(direct, "fugaro/"+banned) {
			t.Errorf("the reconcile tick imports %s, and a tick is a deterministic pass over the node (D1221)", banned)
		}
	}
	// The closure is checked for the one package the core must never reach: a
	// command. A loop that imports internal/cli is a loop a command's flags can
	// change (O6).
	if closure := goList(t, "-deps", "./internal/orchestrator"); strings.Contains(closure, "fugaro/internal/cli") {
		t.Errorf("the core reaches internal/cli, and nothing under internal/orchestrator may import a command")
	}
}

// goList is one `go list` over this package, run from the module root.
func goList(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", append(append([]string{"list"}, args...), "./internal/orchestrator")...)
	cmd.Dir = "../.."
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list %v: %v\n%s", args, err, raw)
	}
	return string(raw)
}

// TestStillInFlightTicksAreNotFailures is the breaker's false positive, seen
// from the tick: a run that is going is not a cycle that achieved nothing, so
// any number of ticks while it runs must not count toward the streak that asks
// a person and pauses the daemon.
func TestStillInFlightTicksAreNotFailures(t *testing.T) {
	now := o6Now()
	const runID = "20261107-090000-abcd"

	w, it := o6OpenItem("wi-1", runID, now.Add(-time.Hour), now.Add(-30*time.Minute))
	h := newHarness(t, tickState(t, now, []orchestrator.WorkItem{w}, []orchestrator.Intent{it}), now)
	h.tick.Breaker = &orchestrator.Breaker{}
	h.reader.reports = []orchestrator.RunReport{{RunID: runID, Started: true}}

	before := h.rev()
	for i := 0; i < orchestrator.BreakerThreshold+2; i++ {
		rep := h.step()
		if rep.Outcome != orchestrator.CycleInFlight {
			t.Fatalf("cycle %d with a run going ended %q", i+1, rep.Outcome)
		}
	}
	if h.rev() != before {
		t.Errorf("ticks with a run going wrote the node")
	}
	if got := h.state(); len(got.Decisions) != 0 {
		t.Errorf("a run in flight tripped the breaker: %+v", got.Decisions)
	}
	if len(h.launcher.started) != 0 {
		t.Errorf("a run in flight was started again: %v", h.launcher.started)
	}
}

// intentByID is one intent of the node, which a fold's test asserts on by id.
func intentByID(t *testing.T, s orchestrator.State, id string) orchestrator.Intent {
	t.Helper()
	for _, it := range s.Intents {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("the node carries no intent %q: %+v", id, s.Intents)
	return orchestrator.Intent{}
}
