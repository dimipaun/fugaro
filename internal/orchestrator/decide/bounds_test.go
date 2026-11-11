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
