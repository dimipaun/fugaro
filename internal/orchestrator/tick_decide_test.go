package orchestrator_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The decision agent in the tick (O13).
//
// What these tests pin is the wake itself: which cases a cycle hands the agent
// (the spec's "When to wake the LLM" table names three, and the other rows stay
// code however many times the node is read again), that a case is judged once
// per attempt whatever the cycle is repeated, and that a paused daemon judges
// nothing at all. The agent here is a stub and the log a map: O12's own runner,
// its bounds and its brief are that package's tests, and what this file is
// about is the decision to call it.

// o13Stub is the decision agent and the audit log a cycle reaches through one
// value (DecideSeams), so a test asserts on the wake and not on how the seam
// was wired.
//
// The default answer commits the state it was handed. That is deliberate: O12's
// runner answers a state carrying its own record of the call and whatever patch
// the answer asked for, and none of that is what these tests read — the fence,
// the order of the cases and the count of the calls are, and a stub that wrote
// something of its own would make every assertion here a test of the stub.
type o13Stub struct {
	// cases is every wake this agent was handed, in the order it was handed
	// them: the assertion that a heartbeat never appears here is the whole of
	// what "heartbeats never wake it" means.
	cases []orchestrator.DecideCase
	// log is what the log took, and judged is what it now answers from: the
	// real log derives one id per case and a repeated cycle reads it back.
	log    []orchestrator.Entry
	judged map[string]bool
	// answer is what this call answers with; nil is the default answer below.
	answer func(orchestrator.State, orchestrator.DecideCase) (orchestrator.DecideResult, error)
}

func (s *o13Stub) seams() orchestrator.DecideSeams {
	s.judged = map[string]bool{}
	return orchestrator.DecideSeams{Agent: s, Log: s}
}

func (s *o13Stub) Decide(_ context.Context, state orchestrator.State, c orchestrator.DecideCase) (orchestrator.DecideResult, error) {
	s.cases = append(s.cases, c)
	if s.answer != nil {
		return s.answer(state, c)
	}
	return orchestrator.DecideResult{
		Next:     state,
		Outcome:  orchestrator.CycleProgress,
		Answered: true,
		Text:     "judged " + c.ItemID,
	}, nil
}

// Judged answers from the lines this log has already taken, which is what the
// node answers from.
func (s *o13Stub) Judged(_ context.Context, id string) (bool, error) { return s.judged[id], nil }

// Append derives the id the log itself derives, so the line a cycle writes is
// the line the next cycle reads.
func (s *o13Stub) Append(_ context.Context, e orchestrator.Entry) error {
	e.ID = orchestrator.EntryID(e.Subject, e.Transition)
	s.judged[e.ID] = true
	s.log = append(s.log, e)
	return nil
}

// reasons is the stub's cases as the wake reasons and items they carry, which
// is what a test compares against: the order of the spec's table is the order
// of this list.
func (s *o13Stub) reasons() []string {
	out := make([]string, 0, len(s.cases))
	for _, c := range s.cases {
		out = append(out, c.ItemID+" "+string(c.Reason))
	}
	return out
}

// o13Failed is an item whose approval is spent and whose last attempt under it
// failed: the spec's "task failed, retries exhausted". Its approval is cleared
// and the marker is what is left of it, which is the state a retry leaves.
func o13Failed(t *testing.T, now time.Time) (orchestrator.WorkItem, orchestrator.Intent) {
	t.Helper()
	at := now.Add(-3 * time.Hour)
	w := workItemOf("wi-retries")
	w.Phase = orchestrator.PhaseFailed
	w.Attempts = 1
	spent := at.UTC()
	w.SpentApprovalAt = &spent
	return w, orchestrator.Intent{
		ID:         orchestrator.IntentID(w.ID, 1),
		RunID:      "20261107-080000-aaaa",
		SpecDigest: orchestrator.SpecDigest(w),
		ApprovalAt: at.UTC(),
		At:         at.UTC(),
		Outcome:    orchestrator.OutcomeFailed,
	}
}

// o13Stuck is an item whose attempt never started and is past the window a
// claim is good for: the spec's "task stuck past timeout". The approval still
// stands, which is what makes the attempt one recovery re-issues rather than
// one it abandons.
func o13Stuck(t *testing.T, now time.Time) (orchestrator.WorkItem, orchestrator.Intent) {
	t.Helper()
	return o6OpenItem("wi-stuck", "20261107-090000-bbbb", now.Add(-3*time.Hour), now.Add(-time.Hour))
}

// o13Ambiguous is an item in a phase an attempt is still expected from, whose
// newest attempt reported an outcome that phase has no move for: the spec's
// "ambiguous review result". The run succeeded and the item is still queued —
// queued reaches running, failed, blocked and cancelled, and the phase table
// has no edge to done — so the fold recorded the ending and left the phase
// where it stood.
func o13Ambiguous(t *testing.T, now time.Time) (orchestrator.WorkItem, orchestrator.Intent) {
	t.Helper()
	at := now.Add(-4 * time.Hour)
	w := workItemOf("wi-ambiguous")
	w.Attempts = 1
	return w, orchestrator.Intent{
		ID:         orchestrator.IntentID(w.ID, 1),
		RunID:      "20261107-070000-cccc",
		SpecDigest: orchestrator.SpecDigest(w),
		ApprovalAt: at.UTC(),
		At:         at.UTC(),
		Outcome:    orchestrator.OutcomeSucceeded,
	}
}

// TestTickWakesTheAgentOnlyForJudgementCases: the cycle hands the agent exactly
// the three cases the spec's table names, in the order that table states them,
// and one case a cycle — and a case it has already judged is never handed over
// again, however many times the node is read.
//
// The two items beside them are the rows that stay code: a run that started and
// has not reported is a heartbeat, whatever its age, and a run that reported an
// outcome its item's phase does have a move for is folded by the cycle itself.
func TestTickWakesTheAgentOnlyForJudgementCases(t *testing.T) {
	now := o6Now()
	failed, failedIntent := o13Failed(t, now)
	stuck, stuckIntent := o13Stuck(t, now)
	ambiguous, ambiguousIntent := o13Ambiguous(t, now)
	// A run that started and has not reported: the heartbeat row, three hours
	// in, which is long past every window this release has and is still code's.
	beat, beatIntent := o6OpenItem("wi-beat", "20261107-100000-dddd", now.Add(-3*time.Hour), now.Add(-3*time.Hour))
	// A run that reported an outcome the item's phase moves to: the completed
	// row, which the fold lands in the same cycle.
	done, doneIntent := o6OpenItem("wi-folded", "20261107-110000-eeee", now.Add(-90*time.Minute), now.Add(-80*time.Minute))

	h := newHarness(t, tickState(t, now,
		[]orchestrator.WorkItem{failed, stuck, ambiguous, beat, done},
		[]orchestrator.Intent{failedIntent, stuckIntent, ambiguousIntent, beatIntent, doneIntent}), now,
		"20261107-120000-ffff")
	h.reader.reports = []orchestrator.RunReport{
		{RunID: beatIntent.RunID, Started: true},
		{RunID: doneIntent.RunID, Started: true, Finished: true, Outcome: orchestrator.OutcomeSucceeded},
	}
	stub := &o13Stub{}
	h.tick.Decide = stub.seams()

	// One case a cycle, in the table's order: the first cycle hands over the
	// retries case, and the two behind it wait for the cycles after it.
	rep := h.step()
	if rep.Decided != 1 || len(stub.cases) != 1 {
		t.Fatalf("the cycle made %d judgement(s) and reports %d, want one: %+v", len(stub.cases), rep.Decided, stub.reasons())
	}
	if got := stub.reasons(); got[0] != "wi-retries retries-exhausted" {
		t.Fatalf("the cycle judged %v, and the table puts retries exhausted first", got)
	}
	if got := h.state(); item(t, got, "wi-folded").Phase != orchestrator.PhaseDone {
		t.Errorf("the finished run left its item at %q, want the fold to have moved it to done", item(t, got, "wi-folded").Phase)
	}

	want := []string{"wi-retries retries-exhausted", "wi-stuck stuck", "wi-ambiguous ambiguous-result"}
	for i, next := range want[1:] {
		rep = h.step()
		if rep.Decided != 1 || len(stub.cases) != i+2 {
			t.Fatalf("the cycle before %q made %d judgement(s), want one case a cycle", next, len(stub.cases))
		}
		if got := stub.reasons(); got[len(got)-1] != next {
			t.Fatalf("the cycle judged %v, want %q next in the table's order", got, next)
		}
	}
	if got := stub.reasons(); !slices.Equal(got, want) {
		t.Fatalf("the cycles judged %v, want the table's three cases and nothing else", got)
	}
	for _, c := range stub.cases {
		if c.ItemID == "wi-beat" {
			t.Errorf("a heartbeat woke the agent: %+v", c)
		}
	}

	// The line each case left is the fence: the next cycle over the same node
	// finds every case judged and makes no call at all.
	if len(stub.log) != 3 {
		t.Fatalf("the cycles logged %d line(s), want one per judgement: %+v", len(stub.log), stub.log)
	}
	for i, want := range []string{
		"wi-retries:decide-retries-exhausted-1",
		"wi-stuck:decide-stuck-1",
		"wi-ambiguous:decide-ambiguous-result-1",
	} {
		if got := stub.log[i].ID; got != want {
			t.Errorf("line %d is %q, want %q", i, got, want)
		}
	}
	rep = h.step()
	if len(stub.cases) != 3 || rep.Decided != 0 {
		t.Errorf("a repeated cycle judged %d case(s) and reports %d, want none: the log is the fence", len(stub.cases), rep.Decided)
	}
}

// TestHeartbeatsNeverWakeTheAgent: a cycle whose only news is a run that
// started and has not reported judges nothing — the heartbeat row of the table
// is code's, however long the run has been going — and it is not a cycle the
// breaker counts either, because a long run is a run that is working.
//
// The first attempt beside it is the other half of the same rule: what code
// does with a run of its own, it does without a model call.
func TestHeartbeatsNeverWakeTheAgent(t *testing.T) {
	now := o6Now()
	beat, beatIntent := o6OpenItem("wi-beat", "20261107-100000-dddd", now.Add(-3*time.Hour), now.Add(-3*time.Hour))
	first := o6Item("wi-first", orchestrator.PhaseQueued, now.Add(-time.Hour))

	h := newHarness(t, tickState(t, now,
		[]orchestrator.WorkItem{beat, first},
		[]orchestrator.Intent{beatIntent}), now, "20261107-120000-ffff")
	h.reader.reports = []orchestrator.RunReport{{RunID: beatIntent.RunID, Started: true}}
	stub := &o13Stub{}
	h.tick.Decide = stub.seams()

	rep := h.step()
	if len(stub.cases) != 0 {
		t.Fatalf("a heartbeat woke the agent with %+v", stub.reasons())
	}
	if len(stub.log) != 0 {
		t.Errorf("the cycle logged %d line(s) for judgements it did not make: %+v", len(stub.log), stub.log)
	}
	if rep.Outcome != orchestrator.CycleProgress || len(h.launcher.started) != 1 {
		t.Errorf("the cycle ended %q having started %d run(s), want the first attempt started as progress",
			rep.Outcome, len(h.launcher.started))
	}
}

// TestBreakerPausesTheDaemon: three cycles in a row that achieved nothing raise
// the breaker's question, and from the cycle after that the daemon judges
// nothing at all — the pause is a person's to lift (D1228), and a daemon that
// kept spending on judgements while it asked what to do would be spending on
// the thing it is asking about.
//
// The case the paused cycle leaves behind is the point: it is still on the node
// and still unjudged, and it is not the pause that resolved it.
func TestBreakerPausesTheDaemon(t *testing.T) {
	now := o6Now()
	ambiguous, ambiguousIntent := o13Ambiguous(t, now)
	h := newHarness(t, tickState(t, now,
		[]orchestrator.WorkItem{ambiguous}, []orchestrator.Intent{ambiguousIntent}), now)
	h.tick.Breaker = &orchestrator.Breaker{}
	stub := &o13Stub{}
	// A call that answered nothing: the money is spent and the cycle achieved
	// nothing, which is the outcome the breaker counts (G10).
	stub.answer = func(state orchestrator.State, _ orchestrator.DecideCase) (orchestrator.DecideResult, error) {
		return orchestrator.DecideResult{Next: state, Outcome: orchestrator.CycleEmpty}, nil
	}
	h.tick.Decide = stub.seams()

	for i := 1; i <= orchestrator.BreakerThreshold; i++ {
		rep := h.step()
		if rep.Outcome != orchestrator.CycleEmpty {
			t.Fatalf("cycle %d ended %q, want the empties a trip is counted from", i, rep.Outcome)
		}
		if asked := orchestrator.BreakerOpen(h.state()); asked != (i == orchestrator.BreakerThreshold) {
			t.Fatalf("after cycle %d of %d the question is %v, and the threshold is where it is asked",
				i, orchestrator.BreakerThreshold, asked)
		}
	}
	raised := h.state().Decisions
	if len(raised) != 1 || !orchestrator.BreakerOpen(h.state()) {
		t.Fatalf("the node carries %+v after the threshold, want the breaker's one unanswered question", raised)
	}
	if !strings.HasPrefix(raised[0].ID, orchestrator.BreakerDecisionID) {
		t.Errorf("the question is %q, want the breaker's own prefix %q", raised[0].ID, orchestrator.BreakerDecisionID)
	}
	if raised[0].WaitingOn != orchestrator.OnPerson {
		t.Errorf("the question waits on %q, want a person: nobody else may lift the pause", raised[0].WaitingOn)
	}

	calls, judged := len(stub.cases), len(stub.log)
	rep := h.step()
	if len(stub.cases) != calls || len(stub.log) != judged {
		t.Fatalf("a paused daemon judged %v, want nothing while the question stands", stub.reasons()[calls:])
	}
	if rep.Decided != 0 {
		t.Errorf("the paused cycle reports %d judgement(s)", rep.Decided)
	}
	if got := len(h.state().Decisions); got != 1 {
		t.Errorf("the paused cycle left %d question(s) on the node, want the one it inherited", got)
	}
}

// item is one work item as the state carries it, and a test that reads a field
// off an id the state does not carry is a test that read the wrong id.
func item(t *testing.T, s orchestrator.State, id string) orchestrator.WorkItem {
	t.Helper()
	w, ok := s.Item(id)
	if !ok {
		t.Fatalf("the state carries no item %q: %+v", id, s.WorkItems)
	}
	return w
}
