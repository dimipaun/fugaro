package orchestrator_test

import (
	"testing"

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
	op := b.Decision()
	if op.Op != orchestrator.OpDecisionAdd || op.Decision == nil {
		t.Fatalf("the breaker raised %+v", op)
	}
	if op.Decision.ID != orchestrator.BreakerDecisionID || op.Decision.WaitingOn != orchestrator.OnPerson {
		t.Errorf("the breaker's decision is %+v", op.Decision)
	}
	if op.Decision.Question == "" {
		t.Error("the breaker's decision carries no question")
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
}
