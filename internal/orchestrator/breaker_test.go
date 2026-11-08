package orchestrator_test

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// TestBreakerRaisesADecision: a daemon that has ended three cycles in a row
// with nothing done has stopped being useful, and the fourth thing it does is
// ask a person. The question is raised as the streak reaches the threshold and
// not on every cycle after it.
func TestBreakerRaisesADecision(t *testing.T) {
	var b orchestrator.Breaker
	for i := range orchestrator.BreakerThreshold - 1 {
		if b.Observe(orchestrator.CycleEmpty) {
			t.Fatalf("the breaker tripped after %d cycle(s)", i+1)
		}
	}
	if !b.Observe(orchestrator.CycleFailed) {
		t.Fatalf("the breaker did not trip on %d cycles in a row", orchestrator.BreakerThreshold)
	}
	if !b.Tripped() {
		t.Error("the breaker does not report that it has tripped")
	}
	if n := orchestrator.BreakerStreak(&b); n != orchestrator.BreakerThreshold {
		t.Errorf("the streak reads %d, not %d", n, orchestrator.BreakerThreshold)
	}
	if b.Observe(orchestrator.CycleEmpty) {
		t.Error("a fourth cycle raised the same question a second time")
	}

	// The question waits on a person: the daemon does not answer it itself.
	now := o6Now()
	op := b.Decision(now)
	if op.Op != orchestrator.OpDecisionAdd || op.Decision == nil {
		t.Fatalf("the breaker raised %+v", op)
	}
	if !strings.HasPrefix(op.Decision.ID, orchestrator.BreakerDecisionID) || op.Decision.WaitingOn != orchestrator.OnPerson {
		t.Errorf("the breaker's decision is %+v", op.Decision)
	}
	if op.Decision.Question == "" {
		t.Error("the breaker's decision carries no question")
	}

	// A question a person has answered is one the daemon can ask again: an
	// answered decision keeps its id in the list and a second write of that id
	// is refused, so the id belongs to the trip and not to the breaker.
	var answered orchestrator.Breaker
	for range orchestrator.BreakerThreshold {
		answered.Observe(orchestrator.CycleEmpty)
	}
	again := answered.Decision(now.Add(time.Hour))
	if again.Decision == nil || again.Decision.ID == op.Decision.ID {
		t.Errorf("the second trip raised %+v, and a streak after an answer is a question of its own", again.Decision)
	}

	// A cycle that did something is a daemon that is working, and the streak
	// starts again from nothing.
	if b.Observe(orchestrator.CycleProgress) {
		t.Error("a cycle that did work tripped the breaker")
	}
	if b.Tripped() || orchestrator.BreakerStreak(&b) != 0 {
		t.Errorf("the streak after a cycle that did work is %d", orchestrator.BreakerStreak(&b))
	}
	for range orchestrator.BreakerThreshold - 1 {
		b.Observe(orchestrator.CycleEmpty)
	}
	if !b.Observe(orchestrator.CycleEmpty) {
		t.Error("the breaker did not trip a second time after a cycle that did work")
	}

	// What it counts is cycles in a row: one that did something breaks the run
	// of them.
	var broken orchestrator.Breaker
	broken.Observe(orchestrator.CycleFailed)
	broken.Observe(orchestrator.CycleProgress)
	broken.Observe(orchestrator.CycleFailed)
	broken.Observe(orchestrator.CycleFailed)
	if broken.Tripped() {
		t.Error("the breaker counted two failures around a good cycle as three in a row")
	}
	broken.Observe(orchestrator.CycleEmpty)
	if !broken.Tripped() {
		t.Fatal("the third failure in a row did not trip it")
	}
	// Reset is the same clearing, for a caller that has dealt with the streak
	// another way.
	broken.Reset()
	if broken.Tripped() || orchestrator.BreakerStreak(&broken) != 0 {
		t.Errorf("Reset left the breaker at %d", orchestrator.BreakerStreak(&broken))
	}
}

// TestBreakerPausesAcrossARestart: what stops the daemon is the question on the
// node, not the count in the process. A crash loop, a supervisor restart or the
// daemon's own version-check exit leaves a fresh Breaker behind, and a pause
// that lived only in the old process would be lifted by that restart while the
// question still stands — the opposite of what the caps do, and a way for a
// crash loop to spend what the breaker is there to stop. What ends the pause is
// a person's answer, which is the same write that resolves the decision.
func TestBreakerPausesAcrossARestart(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	s := o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, approvedAt))

	// A question that is not the breaker's is not a pause: the daemon asks
	// people things, and only the trip stops it.
	s = o6Apply(t, s, orchestrator.Op{Op: orchestrator.OpDecisionAdd, Decision: &orchestrator.DecisionOp{
		ID: "ask-the-owner", Question: "Which brief?", WaitingOn: orchestrator.OnPerson,
	}}, orchestrator.Actor{Class: orchestrator.Daemon, Now: now})
	if orchestrator.BreakerOpen(s) {
		t.Fatal("an unanswered decision that is not the breaker's stopped the daemon")
	}
	if got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now); len(got) != 1 {
		t.Fatalf("the plan beside an ordinary question is %+v", got)
	}

	// The daemon tripped, wrote its question and died. Nothing of it is left.
	var tripped orchestrator.Breaker
	for range orchestrator.BreakerThreshold {
		tripped.Observe(orchestrator.CycleEmpty)
	}
	s = o6Apply(t, s, tripped.Decision(now), orchestrator.Actor{Class: orchestrator.Daemon, Now: now})
	if n := len(s.Decisions); n != 2 {
		t.Fatalf("the node carries %d decision(s): %+v", n, s.Decisions)
	}

	// The restart: a fresh breaker has counted nothing, and the pause has to be
	// read off the node, where the question is.
	var restarted orchestrator.Breaker
	if restarted.Tripped() {
		t.Error("a breaker that has seen no cycle reports that it has tripped")
	}
	if !orchestrator.BreakerOpen(s) {
		t.Fatal("the node carries the trip's unanswered question and does not read as open")
	}
	if got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now); len(got) != 0 {
		t.Fatalf("the restarted daemon planned %+v over the question its predecessor left", got)
	}
	// A cycle that did work is not an answer: it clears this process's streak,
	// and the question is still standing.
	restarted.Observe(orchestrator.CycleProgress)
	if got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now); len(got) != 0 {
		t.Fatalf("a cycle that did work lifted a question no person has answered: %+v", got)
	}

	// A person answers, and the same node plans the launch again.
	answered := o6Apply(t, s, orchestrator.Op{Op: orchestrator.OpDecisionResolve, Resolve: &orchestrator.ResolveOp{
		ID: s.Decisions[1].ID, Answer: "carry on",
	}}, orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: now.Add(time.Minute)})
	if orchestrator.BreakerOpen(answered) {
		t.Error("the answered question still reads as the breaker being open")
	}
	a := onlyAction(t, orchestrator.Plan(answered, orchestrator.RunView{}, o6Caps(), now.Add(time.Minute)))
	if a.Kind != orchestrator.ActionLaunch || a.ItemID != "wi-1" {
		t.Fatalf("the plan after the answer is %+v", a)
	}
}

// TestStillInFlightTicksDoNotTripTheBreaker: a run takes as long as it takes,
// and every tick while it does ends "still in flight". Counting those would
// stop a daemon that is working exactly as it should, so they count toward
// nothing — neither as a failure nor as a reason to fold another one.
func TestStillInFlightTicksDoNotTripTheBreaker(t *testing.T) {
	var b orchestrator.Breaker
	for i := range orchestrator.BreakerThreshold * 3 {
		if b.Observe(orchestrator.CycleInFlight) {
			t.Fatalf("the breaker tripped on %d tick(s) that were still in flight", i+1)
		}
	}
	if b.Tripped() {
		t.Error("the breaker tripped on ticks that were still in flight")
	}
	if n := orchestrator.BreakerStreak(&b); n != 0 {
		t.Errorf("the in-flight ticks left a streak of %d", n)
	}

	// An in-flight tick between two failures breaks the run of them just as a
	// cycle that did something does.
	var c orchestrator.Breaker
	c.Observe(orchestrator.CycleFailed)
	c.Observe(orchestrator.CycleInFlight)
	c.Observe(orchestrator.CycleEmpty)
	c.Observe(orchestrator.CycleEmpty)
	if c.Tripped() {
		t.Error("the breaker counted an in-flight tick as one of two failures in a row")
	}

	// An outcome this release does not know neither counts nor clears: the
	// streak stays where it was rather than being read as progress.
	var d orchestrator.Breaker
	d.Observe(orchestrator.CycleEmpty)
	d.Observe(orchestrator.CycleOutcome("something this release does not know"))
	if n := orchestrator.BreakerStreak(&d); n != 1 {
		t.Errorf("a cycle with an unknown outcome left the streak at %d", n)
	}
	if d.Tripped() {
		t.Error("a cycle with an unknown outcome tripped the breaker")
	}

	// One report per streak, even with an unknown outcome after the trip: the
	// report belongs to the cycle that reached the threshold, and a second one
	// would ask a question already asked.
	var e orchestrator.Breaker
	for range orchestrator.BreakerThreshold {
		e.Observe(orchestrator.CycleEmpty)
	}
	if e.Observe(orchestrator.CycleOutcome("something this release does not know")) {
		t.Error("a cycle with an unknown outcome reported the trip a second time")
	}
	if !e.Tripped() {
		t.Error("a cycle with an unknown outcome cleared the trip")
	}
}
