package orchestrator_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/escalation"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// The ladder's executor, rungs 0 to 2 (E10a).
//
// What is pinned here is the walking and never the deciding: the decision is
// E1's and is tested there, and the readings, the launcher, the comment and the
// log are all a stub, so every assertion below is about what the cycle did with
// an answer it was given. The store is the real one (internal/gcpfake), so the
// ledger these tests read back is a document this release accepted.

// ladderStub is the ladder's four seams as a test sets them: the readings by
// item, the launches it was asked for, the comments it was handed and the lines
// it was given. A launch is recorded and nothing is started.
//
// running is what makes a crash watchable without one: while it is set, a run
// the stub was asked to start and that has not finished comes back as the
// reading's Running, which is exactly the state a cycle that died between the
// start and the ledger write leaves behind.
type ladderStub struct {
	t          *testing.T
	reads      map[string]orchestrator.LadderReading
	running    bool
	readErr    error
	launchErr  error
	commentErr error
	launched   []orchestrator.LadderRequest
	comments   []string
	lines      []orchestrator.Entry
}

func (l *ladderStub) seams(th escalation.Thresholds) orchestrator.LadderSeams {
	return orchestrator.LadderSeams{Thresholds: th, Read: l, Launch: l, Comment: l, Log: l}
}

func (l *ladderStub) LadderFacts(_ context.Context, item orchestrator.WorkItem) (orchestrator.LadderReading, error) {
	if l.readErr != nil {
		return orchestrator.LadderReading{}, l.readErr
	}
	read, ok := l.reads[item.ID]
	if !ok {
		l.t.Fatalf("the ladder read %q, which this test gave no reading for", item.ID)
	}
	if l.running {
		for _, req := range l.launched {
			if req.Item.ID == item.ID {
				read.Read, read.Running = true, req.RunID
				break
			}
		}
	}
	return read, nil
}

func (l *ladderStub) LaunchLadder(_ context.Context, req orchestrator.LadderRequest) error {
	if l.launchErr != nil {
		return l.launchErr
	}
	l.launched = append(l.launched, req)
	return nil
}

func (l *ladderStub) LadderComment(_ context.Context, _ orchestrator.WorkItem, body string) error {
	if l.commentErr != nil {
		return l.commentErr
	}
	l.comments = append(l.comments, body)
	return nil
}

func (l *ladderStub) Append(_ context.Context, entry orchestrator.Entry) error {
	l.lines = append(l.lines, entry)
	return nil
}

// ladderTick is a tick wired to the ladder stub over one state node.
func ladderTick(t *testing.T, s orchestrator.State, now time.Time, runs ...string) (*harness, *ladderStub) {
	t.Helper()
	h := newHarness(t, s, now, runs...)
	l := &ladderStub{t: t, reads: map[string]orchestrator.LadderReading{}}
	h.tick.Ladder = l.seams(escalation.DefaultThresholds())
	return h, l
}

// reading is what the daemon read about an item: a mode, an age, a head and no
// churn. Read true, because a reading that was not made is a person's question
// and not rung 1.
func reading(mode rigor.Mode, elapsed time.Duration, head string) orchestrator.LadderReading {
	return orchestrator.LadderReading{Read: true, Mode: mode, Elapsed: elapsed, Churn: escalation.KindNone, HeadSHA: head}
}

// spentOn is one item of these tests' shape: an item whose approval a retry
// spent, whose own two runs under it failed, and the ledger the test hands it.
// An item in this position is one both the planner and the ladder's rung 1 gate
// hold nothing for, so what a tick does with it is the executor's and nothing
// else's.
func spentOn(t *testing.T, id string, now time.Time, e *orchestrator.Escalation, deps ...string) (orchestrator.WorkItem, []orchestrator.Intent) {
	t.Helper()
	at := now.Add(-2 * time.Hour)
	w := spentItem(t, id, now, at)
	w.Phase = orchestrator.PhaseFailed
	w.DependsOn = deps
	w.Escalation = e
	return w, twoAttempts(id, at, now.Add(-100*time.Minute), w)
}

// stack is a node carrying the items and intents a test built.
func stack(t *testing.T, now time.Time, items []orchestrator.WorkItem, intents []orchestrator.Intent) orchestrator.State {
	t.Helper()
	s := o6Node(t, now, items...)
	s.Intents = intents
	return s
}

// rebased is the ledger of an item whose cheap step is spent: it is at rung 0,
// and the transition records the head the rebase ran at, which is what stops
// the ladder from rebasing that head again.
func rebased(now time.Time, head string, outer int) *orchestrator.Escalation {
	e := ledger(escalation.R0, outer, step("", escalation.R0, escalation.ReasonRebase, now.Add(-time.Hour), head))
	return &e
}

// rungs is the ledger's rungs, oldest first.
func rungs(e *orchestrator.Escalation) []string {
	if e == nil {
		return nil
	}
	out := make([]string, 0, len(e.Transitions))
	for _, tr := range e.Transitions {
		out = append(out, string(tr.To))
	}
	return out
}

// TestTickR0OncePerHead is G4's first bound: rung 0 is the cheap step and it is
// spent by the head it looked at, so a head already rebased is never rebased
// again — while a head the item has not seen is, because a rebase is what moves
// a stalled pull request onto a base that moved under it.
//
// Mutation: record the rung without the head it was decided at and the same
// rebase is launched again on the next cycle.
func TestTickR0OncePerHead(t *testing.T) {
	now := o6Now()
	w, its := spentOn(t, "wi-1", now, nil)
	h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{w}, its), now,
		"20261107-120000-c0de", "20261107-130000-c0de")
	l.reads["wi-1"] = reading(rigor.Pro, time.Hour, headA)

	h.step()
	if len(l.launched) != 1 {
		t.Fatalf("the ladder launched %d runs for an item stalled at a head no rebase has seen", len(l.launched))
	}
	req := l.launched[0]
	if req.Recipe != orchestrator.RecipeRebase || req.Rung != escalation.R0 || req.Grade != "" {
		t.Fatalf("the launch is %+v, and rung 0 is a rebase that runs at the item's own configuration", req)
	}
	if req.Item.ID != "wi-1" || req.RunID == "" {
		t.Fatalf("the launch is %+v, and rung 0 starts an item of this node", req)
	}

	e := itemAt(t, h.state(), "wi-1").Escalation
	if e == nil || e.Rung != escalation.R0 || e.OuterAttempts != 1 {
		t.Fatalf("the ledger reads %+v after the rebase, and rung 0 spends one outer run", e)
	}
	if len(e.Transitions) != 1 || e.Transitions[0].HeadSHA != headA || e.Transitions[0].Reason != escalation.ReasonRebase {
		t.Fatalf("the transition is %+v, and it records the head the rebase was spent by", e.Transitions)
	}

	h.step()
	if len(l.launched) != 1 {
		t.Fatalf("the ladder launched %+v for a head it has already rebased", l.launched[1:])
	}

	l.reads["wi-1"] = reading(rigor.Pro, time.Hour, headB)
	h.step()
	if len(l.launched) != 2 {
		t.Fatalf("the ladder launched %d runs, and a head no rebase has seen is rung 0's", len(l.launched))
	}
	if got := rungs(itemAt(t, h.state(), "wi-1").Escalation); len(got) != 2 || got[1] != string(escalation.R0) {
		t.Fatalf("the ledger reads %v, and a second rebase is a second rung 0 transition", got)
	}
}

// TestTickR2OnceThenR3Or4 is the red step and G4's second bound: the one
// stronger single pass runs at most once, and a second look at the same item
// moves it on — to the merge or the split the rung past it is — rather than
// spending a second rung 2 run.
//
// Mutation: record the transition and leave the ledger at the rung it was at,
// and the next cycle launches a second `escalate`.
func TestTickR2OnceThenR3Or4(t *testing.T) {
	now := o6Now()
	w, its := spentOn(t, "wi-1", now, rebased(now, headA, 1))
	h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{w}, its), now, "20261107-120000-c0de")
	l.reads["wi-1"] = reading(rigor.POC, 4*time.Hour, headA)

	h.step()
	if len(l.launched) != 1 {
		t.Fatalf("the ladder launched %d runs for an item past its rung 2 threshold", len(l.launched))
	}
	req := l.launched[0]
	if req.Recipe != orchestrator.RecipeEscalate || req.Rung != escalation.R2 || req.Grade != escalation.Strong {
		t.Fatalf("the launch is %+v, and rung 2 is the one stronger pass at the row's grade", req)
	}
	e := itemAt(t, h.state(), "wi-1").Escalation
	if e.Rung != escalation.R2 || e.OuterAttempts != 2 {
		t.Fatalf("the ledger reads %+v, and the rung 2 run is the second outer run", e)
	}
	if len(e.Transitions) != 2 || e.Transitions[1].Reason != escalation.ReasonAttempts {
		t.Fatalf("the transitions are %+v, and the count of normal runs is what sent it to rung 2", e.Transitions)
	}
	if len(l.comments) != 1 {
		t.Fatalf("the ladder posted %d comments for one move", len(l.comments))
	}

	h.step()
	if len(l.launched) != 1 {
		t.Fatalf("the ladder launched %+v, and the one rung 2 run is spent", l.launched[1:])
	}
	e = itemAt(t, h.state(), "wi-1").Escalation
	if got := rungs(e); len(got) != 3 || got[2] != string(escalation.R3) {
		t.Fatalf("the ledger reads %v, and what the one rung 2 run leaves is the rung past it", got)
	}
	if e.Transitions[2].Reason != escalation.ReasonR2Clean {
		t.Fatalf("the third transition is %+v, and a review that left nothing blocking merges", e.Transitions[2])
	}
}

// TestTickCriticalPathHalvesAndUsesStrongest is F15 and G7's payoff: the same
// facts that leave an ordinary item to its own loop send the item another
// approved, unfinished item waits on to rung 2, at the strongest grade — the
// flag scales every clock the decision reads and raises the grade the one
// stronger pass runs at.
//
// Mutation: return the row's grade without reading the flag and the launch
// below is a strong run over an item the repository is waiting for.
func TestTickCriticalPathHalvesAndUsesStrongest(t *testing.T) {
	now := o6Now()
	one, its := spentOn(t, "wi-1", now, rebased(now, headA, 1))
	two, it2 := spentOn(t, "wi-2", now, rebased(now, headA, 1), "wi-1")
	h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{one, two}, append(its, it2...)), now,
		"20261107-120000-c0de")
	l.reads["wi-1"] = reading(rigor.Pro, 5*time.Hour, headA)
	l.reads["wi-2"] = reading(rigor.Pro, 5*time.Hour, headA)

	h.step()

	if len(l.launched) != 1 || l.launched[0].Item.ID != "wi-1" {
		t.Fatalf("the ladder launched %+v, and only the item another one waits on escalates", l.launched)
	}
	req := l.launched[0]
	if req.Rung != escalation.R2 || req.Grade != escalation.Strongest {
		t.Fatalf("the launch is %+v, and a critical-path rung 2 runs at the strongest grade", req)
	}
	e1 := itemAt(t, h.state(), "wi-1").Escalation
	if e1.Rung != escalation.R2 || len(e1.Transitions) != 2 {
		t.Fatalf("the ledger of the item on the critical path reads %+v", e1)
	}
	if tr := e1.Transitions[1]; tr.Reason != escalation.ReasonCriticalPath {
		t.Fatalf("the transition is %+v, and the halved clock is why it escalated before its mode's own", tr)
	}

	// The item nothing waits on has the same age, the same attempts and the
	// same mode, and six hours is past no threshold of Pro's.
	e2 := itemAt(t, h.state(), "wi-2").Escalation
	if e2.Rung != escalation.R0 || len(rungs(e2)) != 1 {
		t.Fatalf("the item nothing waits on reads %+v, and nothing has escalated it", e2)
	}
}

// TestTickCrashMidRungIsIdempotent is G4 across a crash: the run a rung started
// is what the reading names, and a cycle that died between the start and the
// ledger write leaves exactly that — so the cycle after it leaves the item
// alone rather than starting the same rung a second time.
//
// Mutation: decide from the node alone and ignore a ladder run that has not
// reported, and the cycle below starts a second rebase for one head.
func TestTickCrashMidRungIsIdempotent(t *testing.T) {
	now := o6Now()
	w, its := spentOn(t, "wi-1", now, nil)
	h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{w}, its), now, "20261107-120000-c0de")
	l.reads["wi-1"] = reading(rigor.Pro, time.Hour, headA)

	// The crash: a run this daemon started, and the ledger write that never
	// landed. The reading is what a restart has instead of the write.
	l.running = true
	l.launched = []orchestrator.LadderRequest{{
		Item: orchestrator.WorkItem{ID: "wi-1"}, RunID: "20261107-110000-c0de",
		Recipe: orchestrator.RecipeRebase, Rung: escalation.R0,
	}}
	before := h.rev()

	h.step()

	if len(l.launched) != 1 {
		t.Fatalf("the ladder launched %+v over a rung whose run has not reported", l.launched[1:])
	}
	if h.mint.calls != 0 {
		t.Fatalf("the cycle minted %d run ids, and a rung already in flight is not started again", h.mint.calls)
	}
	if got := h.rev(); got != before {
		t.Fatalf("the node moved from revision %d to %d, and a cycle that decides nothing writes nothing", before, got)
	}
	if e := itemAt(t, h.state(), "wi-1").Escalation; e != nil {
		t.Fatalf("the ledger reads %+v, and a rung in flight is recorded once", e)
	}
}

// TestTickUnsetGradeRefuses is G5 through the executor: a grade no layer set is
// a model nobody chose, and the ladder asks a person instead of running the one
// stronger pass at whatever a default would have been. The question is asked
// once, under the item's own id, so a daemon that restarts into it asks nothing
// more.
func TestTickUnsetGradeRefuses(t *testing.T) {
	now := o6Now()
	w, its := spentOn(t, "wi-1", now, rebased(now, headA, 1))
	h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{w}, its), now)
	th := escalation.DefaultThresholds()
	row := th.Rows[rigor.POC]
	row.Grade = ""
	th.Rows[rigor.POC] = row
	h.tick.Ladder = l.seams(th)
	l.reads["wi-1"] = reading(rigor.POC, 4*time.Hour, headA)

	h.step()

	if len(l.launched) != 0 {
		t.Fatalf("the ladder launched %+v at a grade nobody set", l.launched)
	}
	got := h.state()
	d, ok := got.Decision("wi-1-escalation")
	if !ok {
		t.Fatal("the ladder refused to decide a rung and left no question for a person")
	}
	if d.WaitingOn != orchestrator.OnPerson {
		t.Errorf("the question waits on %q, and a grade nobody set is a person's to set", d.WaitingOn)
	}
	if len(h.sink.got) != 1 || h.sink.got[0].Subject != "wi-1-escalation" {
		t.Fatalf("the cycle told a person %+v, and a refusal is one question", h.sink.got)
	}
	if e := itemAt(t, got, "wi-1").Escalation; e.Rung != escalation.R0 || len(rungs(e)) != 1 {
		t.Fatalf("the ledger reads %+v, and a refusal moves no rung", e)
	}

	h.step()
	if len(h.sink.got) != 1 {
		t.Fatalf("the cycle asked a person again: %+v", h.sink.got[1:])
	}
	if len(itemAt(t, h.state(), "wi-1").Escalation.Transitions) != 1 {
		t.Fatal("the second cycle moved a ledger over a refusal")
	}
}

// TestTickDependsOnCycleDoesNotLoop is G7's fail-closed half: DependsOn is a
// graph a session writes and a cycle in it is a graph somebody wrote, so the
// critical-path flag is read off the node in one pass — and two items that wait
// on each other are both on a critical path, and the cycle ends.
//
// Mutation: walk each item's dependencies recursively and this test never
// returns.
func TestTickDependsOnCycleDoesNotLoop(t *testing.T) {
	now := o6Now()
	one, its := spentOn(t, "wi-1", now, rebased(now, headA, 1), "wi-2")
	two, it2 := spentOn(t, "wi-2", now, rebased(now, headA, 1), "wi-1")
	h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{one, two}, append(its, it2...)), now,
		"20261107-120000-c0de", "20261107-130000-c0de")
	l.reads["wi-1"] = reading(rigor.Pro, 5*time.Hour, headA)
	l.reads["wi-2"] = reading(rigor.Pro, 5*time.Hour, headA)

	h.step()

	if len(l.launched) != 2 {
		t.Fatalf("the ladder launched %+v, and both items of a cycle are on a critical path", l.launched)
	}
	for _, req := range l.launched {
		if req.Rung != escalation.R2 || req.Grade != escalation.Strongest {
			t.Fatalf("the launch is %+v, and a critical-path rung 2 runs at the strongest grade", req)
		}
	}
	if e := itemAt(t, h.state(), "wi-1").Escalation; e.Rung != escalation.R2 {
		t.Fatalf("the ledger of wi-1 reads %+v", e)
	}
	if e := itemAt(t, h.state(), "wi-2").Escalation; e.Rung != escalation.R2 {
		t.Fatalf("the ledger of wi-2 reads %+v", e)
	}
}

// TestTickCostCeilingEscalates is D2038 and G9: a ceiling a person set is a
// stop on spend that comes before the cheap step and before the clock, and an
// item past it goes to the rung that ends the spending rather than to another
// run.
//
// Mutation: read the ceiling after the cheap step, or not at all, and the item
// below is left where it is with the spend still climbing.
func TestTickCostCeilingEscalates(t *testing.T) {
	now := o6Now()
	one, its := spentOn(t, "wi-1", now, rebased(now, headA, 1))
	two, it2 := spentOn(t, "wi-2", now, rebased(now, headA, 1))
	h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{one, two}, append(its, it2...)), now)
	th := escalation.DefaultThresholds()
	th.CostCeiling = 20
	h.tick.Ladder = l.seams(th)
	over := reading(rigor.Pro, 0, headA)
	over.CostUSD = 25
	l.reads["wi-1"] = over
	l.reads["wi-2"] = reading(rigor.Pro, 0, headA)

	h.step()

	if len(l.launched) != 0 {
		t.Fatalf("the ladder launched %+v for an item past its cost ceiling", l.launched)
	}
	e := itemAt(t, h.state(), "wi-1").Escalation
	if e.Rung != escalation.R3 {
		t.Fatalf("the ledger reads %+v, and the cost ceiling is a stop and not another run", e)
	}
	if tr := e.Transitions[len(e.Transitions)-1]; tr.Reason != escalation.ReasonCost {
		t.Fatalf("the transition is %+v, and the spend is what ended the climb", tr)
	}
	if other := itemAt(t, h.state(), "wi-2").Escalation; other.Rung != escalation.R0 || len(rungs(other)) != 1 {
		t.Fatalf("the item under no ceiling reads %+v, and nothing sends it past rung 1", other)
	}
}

// TestTickWritesTransitionMarker is E8 read back: every move ends a comment
// with the marker naming the run that wrote it and the rung the item moved to,
// and the log keeps one line per move under the id the move derives.
//
// Mutation: post the comment without the marker and a later follow-up cannot
// tell the ladder's comment from an agent's quoted text.
func TestTickWritesTransitionMarker(t *testing.T) {
	now := o6Now()
	w, its := spentOn(t, "wi-1", now, nil)
	h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{w}, its), now, "20261107-120000-c0de")
	l.reads["wi-1"] = reading(rigor.Pro, time.Hour, headA)

	h.step()

	if len(l.comments) != 1 {
		t.Fatalf("the ladder posted %d comments for one move", len(l.comments))
	}
	runID, rung, ok := gitprov.TransitionOf(l.comments[0])
	if !ok || rung != string(escalation.R0) {
		t.Fatalf("the comment %q carries no transition marker for rung 0", l.comments[0])
	}
	if runID != l.launched[0].RunID {
		t.Fatalf("the marker names run %q and the rung started %q", runID, l.launched[0].RunID)
	}
	if len(l.lines) != 1 {
		t.Fatalf("the log holds %d lines for one move", len(l.lines))
	}
	entry := l.lines[0]
	if entry.Subject != "wi-1" || entry.Transition != "ladder-r0-1" {
		t.Fatalf("the line is %+v, and a move is logged under the item and the rung", entry)
	}
	if !strings.Contains(entry.Text, string(escalation.ReasonRebase)) || !strings.Contains(entry.Text, string(escalation.R0)) {
		t.Fatalf("the line reads %q, and the reason and the rung are what it records", entry.Text)
	}
}

// TestTickLaunchesWithinTheItemsApproval is G15 and D2042: every run the ladder
// starts is covered by the item's own approval — the one standing on it, or the
// one a retry spent — and an item nobody approved, or one whose spent approval
// covers no attempt of the spec as it now stands, is left exactly where it is.
//
// Mutation: launch without reading ladderCoveredAt and the first case below
// starts a rebase under an approval that does not exist.
func TestTickLaunchesWithinTheItemsApproval(t *testing.T) {
	now := o6Now()

	t.Run("an item nobody approved starts nothing", func(t *testing.T) {
		s := stack(t, now, []orchestrator.WorkItem{workItemOf("wi-1")}, nil)
		h, l := ladderTick(t, s, now)
		l.reads["wi-1"] = reading(rigor.Pro, time.Hour, headA)

		h.step()

		if len(l.launched) != 0 || len(l.comments) != 0 {
			t.Fatalf("the ladder acted %+v on an item nobody approved", l.launched)
		}
		if e := itemAt(t, h.state(), "wi-1").Escalation; e != nil {
			t.Fatalf("the ledger reads %+v, and the ladder has no approval of its own", e)
		}
	})

	t.Run("an item a spent approval covers nothing of starts nothing", func(t *testing.T) {
		w := spentItem(t, "wi-1", now, now.Add(-2*time.Hour))
		w.Phase = orchestrator.PhaseFailed
		h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{w}, nil), now)
		l.reads["wi-1"] = reading(rigor.Pro, time.Hour, headA)

		h.step()

		if len(l.launched) != 0 {
			t.Fatalf("the ladder launched %+v on an approval that covers no attempt of the item", l.launched)
		}
		if e := itemAt(t, h.state(), "wi-1").Escalation; e != nil {
			t.Fatalf("the ledger reads %+v, and nothing covered the run", e)
		}
	})

	t.Run("an item its approval covers starts the run", func(t *testing.T) {
		w, its := spentOn(t, "wi-1", now, nil)
		h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{w}, its), now, "20261107-120000-c0de")
		l.reads["wi-1"] = reading(rigor.Pro, time.Hour, headA)

		h.step()

		if len(l.launched) != 1 || l.launched[0].Recipe != orchestrator.RecipeRebase {
			t.Fatalf("the ladder launched %+v for an item its approval covers", l.launched)
		}
	})
}

// TestTickUnapprovedDependsOnDoesNotHalve is the other half of G7: the flag
// halves every clock the decision reads and raises the grade of the one
// stronger pass, and an item an agent added and nobody approved must reach
// neither — so the same facts that escalate for an approved waiter leave the
// item alone for an unapproved one.
//
// Mutation: drop the approval from the flag and the second case below climbs on
// a dependency nobody has agreed to.
func TestTickUnapprovedDependsOnDoesNotHalve(t *testing.T) {
	now := o6Now()

	t.Run("a person approved the item that waits", func(t *testing.T) {
		one, its := spentOn(t, "wi-1", now, rebased(now, headA, 1))
		two, it2 := spentOn(t, "wi-2", now, rebased(now, headA, 1), "wi-1")
		h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{one, two}, append(its, it2...)), now,
			"20261107-120000-c0de")
		l.reads["wi-1"] = reading(rigor.Pro, 5*time.Hour, headA)
		l.reads["wi-2"] = reading(rigor.Pro, 5*time.Hour, headA)

		h.step()

		if len(l.launched) != 1 || l.launched[0].Grade != escalation.Strongest {
			t.Fatalf("the ladder launched %+v, and an approved waiter puts the item on a critical path", l.launched)
		}
	})

	t.Run("nobody approved the item that waits", func(t *testing.T) {
		pending := workItemOf("wi-2")
		pending.DependsOn = []string{"wi-1"}
		one, its := spentOn(t, "wi-1", now, rebased(now, headA, 1))
		h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{one, pending}, its), now)
		l.reads["wi-1"] = reading(rigor.Pro, 5*time.Hour, headA)
		l.reads["wi-2"] = reading(rigor.Pro, 5*time.Hour, headA)

		h.step()

		if len(l.launched) != 0 {
			t.Fatalf("the ladder launched %+v on a dependency nobody approved", l.launched)
		}
		if e := itemAt(t, h.state(), "wi-1").Escalation; e.Rung != escalation.R0 || len(rungs(e)) != 1 {
			t.Fatalf("the ledger reads %+v, and an unapproved waiter halves nothing", e)
		}
	})
}

// TestTickR0AtMostTwicePerItem is G4's third bound and D2042's: two rebases are
// what an item may spend on rung 0, so a third head — a rebase's own push, and
// the push after that — is not another cheap step but the item's own loop.
//
// Mutation: count the rebases by the ledger's rung instead of its transitions
// and the loop MaxRebases ends reopens: every new head is rung 0 again.
func TestTickR0AtMostTwicePerItem(t *testing.T) {
	now := o6Now()
	w, its := spentOn(t, "wi-1", now, nil)
	h, l := ladderTick(t, stack(t, now, []orchestrator.WorkItem{w}, its), now,
		"20261107-120000-c0de", "20261107-130000-c0de")
	l.reads["wi-1"] = reading(rigor.Pro, time.Hour, headA)
	h.step()

	l.reads["wi-1"] = reading(rigor.Pro, time.Hour, headB)
	h.step()

	l.reads["wi-1"] = reading(rigor.Pro, time.Hour, "1c2d3e4f5061728394a5b6c7d8e9f01234567890")
	h.step()

	if len(l.launched) != 2 {
		t.Fatalf("the ladder launched %d runs, and rung 0 is two rebases an item", len(l.launched))
	}
	e := itemAt(t, h.state(), "wi-1").Escalation
	if got := rungs(e); len(got) != 2 || got[0] != string(escalation.R0) || got[1] != string(escalation.R0) {
		t.Fatalf("the ledger reads %v, and the third head bought no third rebase", got)
	}
	if e.OuterAttempts != 2 {
		t.Fatalf("the ledger counts %d outer runs, and two rebases are two", e.OuterAttempts)
	}
}

// itemAt is one work item of a state a test read back.
func itemAt(t *testing.T, s orchestrator.State, id string) orchestrator.WorkItem {
	t.Helper()
	w, ok := s.Item(id)
	if !ok {
		t.Fatalf("the node carries no work item %q", id)
	}
	return w
}
