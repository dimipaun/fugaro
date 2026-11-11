package decide_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/orchestrator/decide"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// decisionsAt is n decisions stamped 1, 1+spacing, ... before now: the calls
// the node has already recorded, which is the only place the caps read.
func decisionsAt(now time.Time, n int, spacing time.Duration) []orchestrator.Decision {
	out := make([]orchestrator.Decision, 0, n)
	at := now.Add(-time.Minute)
	for i := range n {
		out = append(out, orchestrator.Decision{
			ID: fmt.Sprintf("ask-%d", i), Question: "what should it do next?",
			WaitingOn: orchestrator.OnDaemon, At: at,
		})
		at = at.Add(-spacing)
	}
	return out
}

func TestHourlyAndDailyCapsStopCalls(t *testing.T) {
	now, _ := testNow()
	bounds := decide.Bounds{}

	hour := theState(t, func(s *orchestrator.State) { s.Decisions = decisionsAt(now, 6, 5*time.Minute) })
	err := bounds.Allow(hour, now)
	if !errors.Is(err, decide.ErrCallCap) {
		t.Fatalf("six calls in the hour ended as %v, not ErrCallCap", err)
	}
	if !strings.Contains(err.Error(), "hour") {
		t.Errorf("the refusal does not say which window it is: %v", err)
	}

	// Forty across the day with five of them inside the hour: the hour is
	// under its cap and the day is not, so the day is what refuses.
	day := theState(t, func(s *orchestrator.State) { s.Decisions = decisionsAt(now, 40, 7*time.Minute) })
	if got := orchestrator.DecisionCalls(day, now); got.DecisionsHour != 5 || got.DecisionsDay != 40 {
		t.Fatalf("the fixture counts %d calls in the hour and %d today, want 5 and 40", got.DecisionsHour, got.DecisionsDay)
	}
	err = bounds.Allow(day, now)
	if !errors.Is(err, decide.ErrCallCap) {
		t.Fatalf("forty calls in the day ended as %v, not ErrCallCap", err)
	}
	if !strings.Contains(err.Error(), "today") {
		t.Errorf("the refusal does not say which window it is: %v", err)
	}

	under := theState(t, func(s *orchestrator.State) { s.Decisions = decisionsAt(now, 5, 7*time.Minute) })
	if err := bounds.Allow(under, now); err != nil {
		t.Errorf("five calls in the hour and the day ended as %v", err)
	}

	// A cap refuses before anything is spent: no child is started at all.
	bin := testutil.FakeClaude(t, `{"calls":[{"structured":`+theAnswer+`}]}`)
	r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels()}
	d, err := r.Decide(context.Background(), decide.Request{State: hour, ItemID: "wi-1"})
	if !errors.Is(err, decide.ErrCallCap) {
		t.Fatalf("a call under the cap ended as %v", err)
	}
	if d.Outcome != orchestrator.CycleEmpty {
		t.Errorf("a refused call is a cycle that ended %q, want %q", d.Outcome, orchestrator.CycleEmpty)
	}
	if calls := testutil.FakeClaudeCalls(t, bin); len(calls) != 0 {
		t.Errorf("the runner started %d calls under a cap", len(calls))
	}
}

// callsScript is a fake claude paid for n calls, each answering with one
// scripted result.
func callsScript(n int, result string) string {
	repeated := make([]string, n)
	for i := range repeated {
		repeated[i] = result
	}
	return `{"calls":[` + strings.Join(repeated, ",") + `]}`
}

func TestEveryCallCountsSoTheSeventhIsRefused(t *testing.T) {
	// Six an hour is D1228's bound and the only dollar control on the person's
	// own login, and it bounds nothing unless each call leaves a record the
	// next one's cap reads. Every answer here is the package's own fact_set —
	// an op that adds no decision of its own — so what the node counts below
	// can only be the calls' own records.
	ctx := context.Background()
	state := theState(t, nil)
	bin := testutil.FakeClaude(t, callsScript(6, `{"structured":`+theAnswer+`}`))
	r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels()}

	for i := range 6 {
		d, err := r.Decide(ctx, decide.Request{State: state, ItemID: "wi-1", Paths: []string{"docs/x.md"}})
		if err != nil {
			t.Fatalf("call %d of the six ended as %v", i+1, err)
		}
		if d.Outcome != orchestrator.CycleProgress {
			t.Fatalf("call %d ended the cycle as %q, want %q", i+1, d.Outcome, orchestrator.CycleProgress)
		}
		// What a caller does with a decision: commit the state it came back
		// with, which is where the call's record is.
		state = d.Next
		if got := orchestrator.DecisionCalls(state, mustNow(t)).DecisionsHour; got != i+1 {
			t.Fatalf("after %d calls the node counts %d in the hour, want %d", i+1, got, i+1)
		}
	}

	d, err := r.Decide(ctx, decide.Request{State: state, ItemID: "wi-1", Paths: []string{"docs/x.md"}})
	if !errors.Is(err, decide.ErrCallCap) {
		t.Fatalf("the seventh call ended as %v, not ErrCallCap", err)
	}
	if d.Outcome != orchestrator.CycleEmpty {
		t.Errorf("the refused call ended the cycle as %q, want %q", d.Outcome, orchestrator.CycleEmpty)
	}
	if calls := testutil.FakeClaudeCalls(t, bin); len(calls) != 6 {
		t.Errorf("the runner started %d calls, want 6: the seventh spends nothing", len(calls))
	}
}

func TestACallThatFailedCountsToo(t *testing.T) {
	// The money is spent whatever the child answers, so a call that failed
	// leaves its record too: otherwise a model that keeps failing — or a
	// `claude` that will not start — is a loop that spends past every cap,
	// while every one of its cycles is a failure the breaker counts (G10).
	ctx := context.Background()
	state := theState(t, nil)
	bin := testutil.FakeClaude(t, callsScript(6, `{"is_error":true,"subtype":"error_max_turns"}`))
	r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels()}

	for i := range 6 {
		d, err := r.Decide(ctx, decide.Request{State: state, ItemID: "wi-1", Paths: []string{"docs/x.md"}})
		if !errors.Is(err, decide.ErrFailedCall) {
			t.Fatalf("call %d ended as %v, not ErrFailedCall", i+1, err)
		}
		state = d.Next
		if got := orchestrator.DecisionCalls(state, mustNow(t)).DecisionsHour; got != i+1 {
			t.Fatalf("after %d failed calls the node counts %d, want %d", i+1, got, i+1)
		}
	}
	if _, err := r.Decide(ctx, decide.Request{State: state, ItemID: "wi-1", Paths: []string{"docs/x.md"}}); !errors.Is(err, decide.ErrCallCap) {
		t.Fatalf("the seventh call ended as %v, not ErrCallCap", err)
	}
}

func TestTwoCallsInTheSameSecondAreTwoRecords(t *testing.T) {
	// The server clock can give two calls one stamp — a test's clock does it
	// every time — and a record keyed by the stamp would turn the second call
	// into a refused patch, which the runner reports as a failure: the caps
	// would stop counting exactly when a loop is calling fastest.
	now := mustNow(t)
	state := theState(t, nil)
	for i := range 3 {
		next, err := decide.RecordCall(state, "wi-1", now)
		if err != nil {
			t.Fatalf("call %d's record ended as %v", i+1, err)
		}
		state = next
		if got := orchestrator.DecisionCalls(state, now).DecisionsHour; got != i+1 {
			t.Fatalf("after %d calls the node counts %d, want %d", i+1, got, i+1)
		}
		// Closed as it is written: an open record is a question nobody owes an
		// answer to and retention never drops it, so a record per call would
		// fill the document until the daemon stopped on OverCap.
		if record := state.Decisions[len(state.Decisions)-1]; !record.Resolved() {
			t.Error("the call's record is an open question, which retention keeps for good")
		}
	}
}

func TestCountersSurviveARestart(t *testing.T) {
	now, _ := testNow()
	full := theState(t, func(s *orchestrator.State) { s.Decisions = decisionsAt(now, 6, 5*time.Minute) })
	bin := testutil.FakeClaude(t, `{"calls":[{"structured":`+theAnswer+`}]}`)

	// Each of these is a runner that has just started and counted nothing of
	// its own, and each is refused by what the node carries.
	for i := range 3 {
		r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels()}
		if _, err := r.Decide(context.Background(), decide.Request{State: full, ItemID: "wi-1"}); !errors.Is(err, decide.ErrCallCap) {
			t.Fatalf("the %d. runner that counted nothing ended as %v, not ErrCallCap", i+1, err)
		}
	}

	// And what is counted is the derivation at the server time, not the
	// counters the document last wrote: a node written six calls ago says six
	// in both fields, and every one of those calls has since left the hour.
	stale := theState(t, func(s *orchestrator.State) {
		s.Decisions = decisionsAt(now, 6, 5*time.Minute)
		s.Counters = orchestrator.Counters{DecisionsHour: 6, DecisionsDay: 6}
	})
	aged := theState(t, func(s *orchestrator.State) {
		s.Decisions = decisionsAt(now.Add(-90*time.Minute), 6, 5*time.Minute)
		s.Counters = stale.Counters
	})
	if err := (decide.Bounds{}).Allow(aged, now); err != nil {
		t.Errorf("a node whose six calls have left the hour ended as %v, want the call allowed", err)
	}
}
