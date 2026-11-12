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
	// A refusal, not a cycle that achieved nothing: the outcome is left unset,
	// which the breaker neither counts nor clears (orchestrator.Breaker), so a
	// daemon held back by its own cap is not a daemon that is spinning.
	if d.Outcome != "" {
		t.Errorf("a refused call ended the cycle as %q, and the breaker reads that as a cycle that achieved nothing", d.Outcome)
	}
	if calls := testutil.FakeClaudeCalls(t, bin); len(calls) != 0 {
		t.Errorf("the runner started %d calls under a cap", len(calls))
	}
}

// TestACapRefusalDoesNotTripTheBreaker is the other half of "a refusal and not
// a failure" (ErrCallCap): the caps are 6 an hour and 40 a day, so a daemon
// that reaches one is a daemon whose bound is working. Reading each refusal as
// a cycle that ended with nothing done reaches the breaker's threshold in three
// of them, and the trip pauses every spawn on the node and asks a person what
// to do about a loop that is behaving.
func TestACapRefusalDoesNotTripTheBreaker(t *testing.T) {
	now := mustNow(t)
	hour := theState(t, func(s *orchestrator.State) { s.Decisions = decisionsAt(now, 6, 5*time.Minute) })
	bin := testutil.FakeClaude(t, `{"calls":[{"structured":`+theAnswer+`}]}`)
	r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels()}
	b := &orchestrator.Breaker{}
	for i := range orchestrator.BreakerThreshold {
		d, err := r.Decide(context.Background(), decide.Request{State: hour, ItemID: "wi-1"})
		if !errors.Is(err, decide.ErrCallCap) {
			t.Fatalf("refusal %d ended as %v, not ErrCallCap", i+1, err)
		}
		if b.Observe(d.Outcome) {
			t.Fatalf("refusal %d was the cycle that reached the breaker's threshold", i+1)
		}
	}
	if b.Tripped() {
		t.Error("three cap refusals in a row tripped the breaker, and the pause it raises stops every spawn until a person answers")
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
	if d.Outcome != "" {
		t.Errorf("the refused call ended the cycle as %q, and the breaker reads that as a cycle that achieved nothing", d.Outcome)
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

func TestTheCallRecordIsNotAQuestionInTheSummary(t *testing.T) {
	// The caps need a record per call in the document, and the summary is what
	// a person and a session read: one line per record would be a summary of
	// nothing else — the cap's forty fill it — and the facts are what would
	// fall out of it. The record carries a name of its own
	// (orchestrator.DecisionCallID) and the summary is the reader that reads
	// it, so this pins the two together: the records stay in the document,
	// where the caps count them, and out of the text a person reads.
	now := mustNow(t)
	state := theState(t, func(s *orchestrator.State) {
		s.Facts["ci-flake"] = orchestrator.Fact{Value: "the review round retried twice", By: orchestrator.Daemon}
		s.Decisions = []orchestrator.Decision{{
			ID: "ask-ci-flake", Question: "Which of the two should this wait for?",
			WaitingOn: orchestrator.OnPerson, At: now,
		}}
	})
	// The question above is in the hour's count too — every decision is, which
	// is the count being a bound on the money rather than on the answers — so
	// what the eight records have to add is eight.
	before := orchestrator.DecisionCalls(state, now).DecisionsHour
	for range 8 {
		next, err := decide.RecordCall(state, "wi-1", now)
		if err != nil {
			t.Fatalf("a call's record ended as %v", err)
		}
		state = next
	}
	if got := orchestrator.DecisionCalls(state, now).DecisionsHour; got != before+8 {
		t.Fatalf("the node counts %d calls after eight of them, want %d: the records are what the caps read", got, before+8)
	}

	summary := orchestrator.Summarize(state)
	if strings.Contains(summary, "decide-call") {
		t.Errorf("the summary prints the loop's own call records:\n%s", summary)
	}
	if !strings.Contains(summary, "decisions: 1, 1 waiting") {
		t.Errorf("the summary does not count the one question and only it:\n%s", summary)
	}
	if !strings.Contains(summary, "Which of the two should this wait for?") {
		t.Errorf("the summary dropped a question somebody asked:\n%s", summary)
	}
	if !strings.Contains(summary, "ci-flake") {
		t.Errorf("the call records pushed the facts out of the summary:\n%s", summary)
	}
}

func TestTheLedgerKeepsOnlyTheCallsTheCapsCount(t *testing.T) {
	// The ledger is the count, and the document that holds it is capped at
	// MaxStateBytes with nothing in this tree calling retention
	// (orchestrator.Retain): a day's calls plus the day before's would be ~15
	// KiB, and a node at that point can neither decide nor be written to — every
	// write over the cap is refused, a person's approve included. So the record
	// that goes in takes out the ledger's own records the caps have stopped
	// counting: a call stamped before the day the new one is made in is one
	// DecisionCalls already ignores.
	now := mustNow(t)
	yesterday := now.Add(-25 * time.Hour)
	state := theState(t, func(s *orchestrator.State) {
		// A question somebody asked, and the answer to it, from the same
		// yesterday: what the ledger may take out is its own bookkeeping and
		// never a person's.
		answered := yesterday.Add(time.Minute)
		s.Decisions = []orchestrator.Decision{{
			ID: "ask-yesterday", Question: "Which of the two should this wait for?",
			WaitingOn: orchestrator.OnPerson, At: yesterday,
			Answer: "the second", By: orchestrator.Person, ResolvedAt: &answered,
		}}
	})
	for range 3 {
		next, err := decide.RecordCall(state, "wi-1", yesterday)
		if err != nil {
			t.Fatalf("yesterday's record ended as %v", err)
		}
		state = next
	}
	if got := len(state.Decisions); got != 4 {
		t.Fatalf("the ledger carries %d decisions, want 4: %+v", got, state.Decisions)
	}
	before := orchestrator.DecisionCalls(state, now)

	next, err := decide.RecordCall(state, "wi-1", now)
	if err != nil {
		t.Fatalf("the call's record ended as %v", err)
	}
	for _, d := range next.Decisions {
		if strings.HasPrefix(d.ID, orchestrator.DecisionCallID) && d.At.UTC().Before(now.UTC().Truncate(24*time.Hour)) {
			t.Errorf("the ledger kept %s from before the day the caps count", d.ID)
		}
	}
	if got := len(next.Decisions); got != 2 {
		t.Fatalf("the ledger carries %d decisions, want 2 — today's call and the person's question: %+v", got, next.Decisions)
	}
	if _, ok := next.Decision("ask-yesterday"); !ok {
		t.Error("the ledger dropped a question a person asked and answered, which is not its own bookkeeping")
	}
	// The one direction a cap must never move: dropping a record the count
	// reads would raise the allowance a cycle has left.
	if got, want := orchestrator.DecisionCalls(next, now), before; got.DecisionsDay != want.DecisionsDay+1 || got.DecisionsHour != want.DecisionsHour+1 {
		t.Errorf("the new record moved the counts to %+v from %+v", got, want)
	}
	if _, err := orchestrator.EncodeState(next); err != nil {
		t.Errorf("the state the ledger produced is not one this release writes: %v", err)
	}
}

func TestACallTheDocumentCannotHoldIsRefused(t *testing.T) {
	// A record the document cannot take is a call the caps cannot count, and a
	// call they cannot count is spending past every bound the loop has. So it
	// is refused before the child starts — nothing is spent — and it is a
	// refusal rather than a failure: the outcome is unset, so the breaker does
	// not read the node's own size as a loop that achieved nothing.
	now := mustNow(t)
	full := theState(t, nil)
	for i := range orchestrator.MaxFacts {
		full.Facts[fmt.Sprintf("fact-%02d", i)] = orchestrator.Fact{
			Value: strings.Repeat("v", orchestrator.MaxFactValueRunes), By: orchestrator.Daemon,
		}
	}
	if !orchestrator.OverCap(full) {
		t.Fatalf("the state this test built is %d bytes and inside the cap, so nothing here is full", len(mustEncode(t, full)))
	}

	if _, err := decide.RecordCall(full, "wi-1", now); !errors.Is(err, decide.ErrNoRoom) {
		t.Fatalf("a record for a document with no room ended as %v, not ErrNoRoom", err)
	}

	bin := testutil.FakeClaude(t, `{"calls":[{"structured":`+theAnswer+`}]}`)
	r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels()}
	d, err := r.Decide(context.Background(), decide.Request{State: full, ItemID: "wi-1"})
	if !errors.Is(err, decide.ErrNoRoom) {
		t.Fatalf("the call ended as %v, not ErrNoRoom", err)
	}
	if calls := testutil.FakeClaudeCalls(t, bin); len(calls) != 0 {
		t.Errorf("the runner started %d calls the caps cannot count", len(calls))
	}
	if d.Next.Rev != 0 || d.Next.AgentID != "" {
		t.Errorf("a call that was never made handed back %+v", d.Next)
	}
	if d.Outcome != "" {
		t.Errorf("a call that was never made ended the cycle as %q", d.Outcome)
	}
	if (&orchestrator.Breaker{}).Observe(d.Outcome) {
		t.Error("the ledger's own size was read as a cycle that reached the breaker's threshold")
	}
}

// mustEncode is the state as this release writes it, for a test that needs the
// size of one.
func mustEncode(t *testing.T, s orchestrator.State) []byte {
	t.Helper()
	raw, err := orchestrator.EncodeState(s)
	if err != nil {
		t.Fatalf("the state this test built is not one this release writes: %v", err)
	}
	return raw
}
