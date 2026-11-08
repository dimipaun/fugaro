package orchestrator_test

import (
	"errors"
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
	op, ok := b.Decision(o6Node(t, now), now)
	if !ok || op.Op != orchestrator.OpDecisionAdd || op.Decision == nil {
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
	asked := o6Apply(t, o6Node(t, now), op, orchestrator.Actor{Class: orchestrator.Daemon, Now: now})
	asked = o6Apply(t, asked, orchestrator.Op{Op: orchestrator.OpDecisionResolve, Resolve: &orchestrator.ResolveOp{
		ID: op.Decision.ID, Answer: "carry on",
	}}, orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: now})
	again, ok := answered.Decision(asked, now.Add(time.Hour))
	if !ok || again.Decision.ID == op.Decision.ID {
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
	question, ok := tripped.Decision(s, now)
	if !ok {
		t.Fatal("the tripped breaker had no question to raise")
	}
	s = o6Apply(t, s, question, orchestrator.Actor{Class: orchestrator.Daemon, Now: now})
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

	// The restarted daemon runs its cycles, and every one of them ends empty
	// because the pause it inherited refuses every plan — so it counts to the
	// threshold again. It has nothing to ask: the question this streak would
	// raise is the one already on the node, and a second one beside it is a
	// pause a person has to answer twice — and one that outlives the answer to
	// either of them, because the pause is every unanswered question.
	for range orchestrator.BreakerThreshold {
		if !restarted.Observe(orchestrator.CycleEmpty) {
			continue
		}
		raised, ok := restarted.Decision(s, now.Add(time.Hour))
		if ok {
			t.Fatalf("the restarted daemon raised %+v beside the question that stopped it", raised.Decision)
		}
	}
	if n := len(s.Decisions); n != 2 {
		t.Fatalf("the restarted daemon left %d decision(s) on the node: %+v", n, s.Decisions)
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

// TestBreakerAsksAgainAfterAnAnswer: a daemon is long-lived, and the count is
// the process's while the answer is the node's, so a breaker that tripped and
// was answered by a person has to be re-armed by the loop that owns it. Without
// it the streak stands at the threshold for the life of the process, Observe
// never reports again, and the guard is spent after one trip — a daemon that
// spins on empty cycles for hours and asks nobody.
func TestBreakerAsksAgainAfterAnAnswer(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	s := o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, approvedAt))
	var b orchestrator.Breaker

	// One cycle of the driver's loop: re-arm a breaker whose question a person
	// has answered — nothing else does — observe how the cycle ended, and
	// commit the question when this is the cycle that reached the threshold.
	cycles := func(at time.Time, n int, rearm bool) int {
		raised := 0
		for range n {
			if rearm && !orchestrator.BreakerOpen(s) && b.Tripped() {
				b.Reset()
			}
			if b.Observe(orchestrator.CycleEmpty) {
				question, ok := b.Decision(s, at)
				if !ok {
					t.Fatalf("the cycle that reached the threshold had no question to raise over %+v", s.Decisions)
				}
				s = o6Apply(t, s, question, orchestrator.Actor{Class: orchestrator.Daemon, Now: at})
				raised++
			}
		}
		return raised
	}

	if n := cycles(now, orchestrator.BreakerThreshold, true); n != 1 {
		t.Fatalf("the first streak raised %d question(s)", n)
	}
	if !orchestrator.BreakerOpen(s) {
		t.Fatal("the node carries the trip's question and does not read as open")
	}

	// A person answers, and the daemon plans again.
	s = o6Apply(t, s, orchestrator.Op{Op: orchestrator.OpDecisionResolve, Resolve: &orchestrator.ResolveOp{
		ID: s.Decisions[0].ID, Answer: "carry on",
	}}, orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: now.Add(time.Minute)})
	if orchestrator.BreakerOpen(s) {
		t.Fatal("the answered question still reads as the breaker being open")
	}

	// The same process, unarmed: ten more empty cycles raise nothing, because
	// the report belongs to the cycle that reaches the threshold and this
	// breaker reached it an hour ago. This is the state the re-arm is for.
	if n := cycles(now.Add(time.Hour), 10, false); n != 0 {
		t.Fatalf("a breaker nobody re-armed raised %d question(s) after the answer", n)
	}
	if got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now.Add(time.Hour)); len(got) != 1 {
		t.Fatalf("the plan after the answer is %+v, and nothing is holding it back", got)
	}

	// The re-arm the loop owes it: the next run of empty cycles asks again, and
	// the question is its own rather than the answered one's.
	if n := cycles(now.Add(time.Hour), orchestrator.BreakerThreshold, true); n != 1 {
		t.Fatalf("the streak after the answer raised %d question(s), and the guard has to be usable more than once per process", n)
	}
	if len(s.Decisions) != 2 || s.Decisions[1].ID == s.Decisions[0].ID || !orchestrator.BreakerOpen(s) {
		t.Fatalf("the second trip left %+v, and an answered decision keeps its id in the list", s.Decisions)
	}
	if got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("the second trip planned %+v over its own question", got)
	}
}

// TestSessionCannotForgeTheBreakerPause: the pause is a decision, and adding
// one takes no class, so the prefix alone would let a session — or a decision
// agent — stop every spawn on the node until a person resolved a question no
// breaker asked. What carries that power is the daemon's own guard, so the
// write is the daemon's: a class that is not the daemon asking for the
// breaker's prefix is refused. Every other question stays any class's to ask,
// which is what keeps the daemon's agents able to ask a person things.
func TestSessionCannotForgeTheBreakerPause(t *testing.T) {
	now := o6Now()
	s := o6Node(t, now, o6Item("wi-1", orchestrator.PhaseQueued, now.Add(-time.Hour)))
	ask := func(id string) orchestrator.Op {
		return orchestrator.Op{Op: orchestrator.OpDecisionAdd, Decision: &orchestrator.DecisionOp{
			ID: id, Question: "Stop everything?", WaitingOn: orchestrator.OnPerson,
		}}
	}
	for _, class := range []orchestrator.Class{orchestrator.Session, orchestrator.DecisionAgent, orchestrator.Person} {
		if _, err := applyAs(t, s, class, "session:20261107-120000-abcd", now, ask(orchestrator.BreakerDecisionID+"-forged")); !errors.Is(err, orchestrator.ErrActorNotAllowed) {
			t.Fatalf("a %s actor halted the spawns: err = %v, want %v", class, err, orchestrator.ErrActorNotAllowed)
		}
	}
	if orchestrator.BreakerOpen(s) {
		t.Fatal("a refused write still stopped the daemon")
	}

	// The daemon's own trip lands, and the question nobody's breaker asked is
	// still a class's to add — an agent that cannot ask a person anything is no
	// use.
	s = o6Apply(t, s, ask(orchestrator.BreakerDecisionID+"-20261107T120000Z"), orchestrator.Actor{Class: orchestrator.Daemon, Now: now})
	s = o6Apply(t, s, ask("ask-the-owner"), orchestrator.Actor{Class: orchestrator.DecisionAgent, Now: now})
	if !orchestrator.BreakerOpen(s) {
		t.Fatal("the daemon's own question did not stop the spawns")
	}
	if got := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now); len(got) != 0 {
		t.Fatalf("the plan beside the daemon's question is %+v", got)
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
