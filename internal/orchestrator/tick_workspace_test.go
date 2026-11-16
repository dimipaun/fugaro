package orchestrator_test

// A workspace's refusal in the fold (O19, D2022 (a)).
//
// The workspace work put the marker in the run's own record and nothing in this
// package reads it: whether a record that carries one is a refusal the
// workspace's credentials service answered is ee/'s rule, asked through the
// workspace seam. What is pinned here is what the core does with the answer —
// the cycle asks a person at once instead of spending BreakerThreshold more
// attempts into the same refusal, the question is the breaker's own
// (waiting_on: person, the id the daemon alone may write), the pause is the one
// Plan already refuses over, and it lifts itself after WorkspacePauseWindow.
//
// The rule over the marker is not here and must not be: a test that spelled the
// shape of a corroborated refusal would be the second place that rule lives.
// The seam a case installs stands in for it, and the exact combinations are
// pinned in ee/orchestrator's TestWorkspaceRefusedReadsOnlyTheRunRecord.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// workspaceClaims installs a seam that believes every marker a record carries,
// which is the worst case a forged one can reach: the forger got the shape
// right and the build's rule has no corroboration left to refuse it with. It is
// what makes the window the load-bearing half of the bound, so the cases below
// drive it rather than a seam that answers false.
func workspaceClaims(t *testing.T) {
	t.Helper()
	orchestrator.ResetWorkspace(t)
	if err := orchestrator.SetWorkspace(orchestrator.WorkspaceFuncs{
		Refused: func(r orchestrator.RunReport) bool { return r.WorkspaceRefusal != "" },
	}); err != nil {
		t.Fatalf("installing the workspace seam: %v", err)
	}
}

// workspaceRefusedRun is the report a run the workspace stopped leaves: the
// attempt ended, refused, with the marker and the halt the record carries.
func workspaceRefusedRun(runID, marker string) orchestrator.RunReport {
	return orchestrator.RunReport{
		RunID: runID, Started: true, Finished: true,
		Outcome:          orchestrator.OutcomeRefused,
		WorkspaceRefusal: marker,
		Halt: orchestrator.Halt{
			Reason: "budget_unavailable",
			Scope:  "run",
			Detail: "the credentials service cannot read the state it must read (state_unavailable)",
		},
	}
}

// workspaceQueue is the node a refusal lands on: the item whose attempt the
// workspace refused, and a second approved item the queue would start next.
// The second one is what makes "pauses the queue" a claim about the daemon
// rather than about the refused item, which was never going to be retried
// anyway (G4).
func workspaceQueue(t *testing.T, now time.Time, runID string) (orchestrator.State, []string) {
	t.Helper()
	refused, it := o6OpenItem("wi-1", runID, now.Add(-time.Hour), now.Add(-30*time.Minute))
	next := o6Item("wi-2", orchestrator.PhaseQueued, now.Add(-time.Hour))
	return tickState(t, now, []orchestrator.WorkItem{refused, next}, []orchestrator.Intent{it}), []string{runID}
}

// TestAWorkspaceRefusalPausesTheQueueAtOnce is O19's red step and D2022 (a):
// one run came back refused by the workspace, and the daemon stops starting
// work in the same cycle it folded it rather than after BreakerThreshold more
// attempts — each of those an item's approval — into a refusal that already
// said what it had to say. The pause is not a mechanism of its own: it is the
// breaker's question on the node, which is what Plan already refuses over.
func TestAWorkspaceRefusalPausesTheQueueAtOnce(t *testing.T) {
	workspaceClaims(t)
	now := o6Now()
	const runID = "20261107-090000-abcd"
	s, runs := workspaceQueue(t, now, runID)

	h := newHarness(t, s, now, "20261107-090001-beef")
	h.tick.Breaker = &orchestrator.Breaker{}
	h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID, "stop")}

	rep := h.step()
	if rep.Folded != 1 {
		t.Fatalf("the cycle folded %d runs where one reported", rep.Folded)
	}
	got := h.state()

	// The refused item is not relaunched — a refusal is never retried — and the
	// item beside it is not started either: the queue is what stopped.
	if n := len(orchestrator.Plan(got, orchestrator.RunView{}, o6Caps(), now)); n != 0 {
		t.Fatalf("the cycle after the refusal planned %d action(s): a workspace's refusal has to stop the whole queue, not the one item it landed on", n)
	}
	if w, ok := got.Item("wi-1"); !ok || w.Phase != orchestrator.PhaseFailed {
		t.Errorf("the refused item is %+v, and a refusal ends the attempt as an ordinary one does (G4)", w)
	}
	if w, ok := got.Item("wi-2"); !ok || w.Phase != orchestrator.PhaseQueued {
		t.Errorf("the item beside the refusal is %+v, and nothing may start it while the pause stands", w)
	}
	if len(runs) != 1 {
		t.Fatalf("this test seeds %d run(s)", len(runs))
	}
}

// TestARefusedRunWhoseWriteFailsIsAskedAboutOnTheRetry pins the pause against
// the write it travels in: the outcome that spends the attempt and the question
// that stops the queue are one patch, so the pause lands in the write that
// lands the outcome or in no write at all.
//
// Both halves are what a second write would lose, and the second is the one
// that fails open: the outcome lands, the attempt has reported, openRunIDs
// never asks the reader about it again, and the refusal is gone — no question,
// no line, and the queue carries on into the workspace's refusal, which is the
// opposite of "at once".
func TestARefusedRunWhoseWriteFailsIsAskedAboutOnTheRetry(t *testing.T) {
	now := o6Now()
	const runID = "20261107-090000-abcd"

	t.Run("the write the fold carries is refused", func(t *testing.T) {
		workspaceClaims(t)
		s, _ := workspaceQueue(t, now, runID)

		h := newHarness(t, s, now, "20261107-090001-beef")
		h.tick.Breaker = &orchestrator.Breaker{}
		h.tick.Store = &refusingStore{Store: h.node}
		h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID, "stop")}

		if _, err := h.tick.Step(context.Background()); !errors.Is(err, orchestrator.ErrStateMoved) {
			t.Fatalf("the cycle over a refused run whose write was refused ended as %v, want the store's own refusal", err)
		}
		// Nothing landed — neither the outcome nor the question — and the
		// attempt is still unreported, which is what makes the refusal visible
		// to the next cycle at all.
		got := h.state()
		if n := len(got.Decisions); n != 0 {
			t.Fatalf("the node carries %d decision(s) after a write that did not land: %+v", n, got.Decisions)
		}
		if it := intentByID(t, got, "wi-1:1"); it.Outcome != "" {
			t.Errorf("the intent is %+v, and a write that did not land leaves the attempt unreported", it)
		}
		if w, ok := got.Item("wi-1"); !ok || w.Phase != orchestrator.PhaseRunning {
			t.Errorf("the item is %+v, and a write that did not land leaves it where it was", w)
		}

		// The retry: the same report folded again, and this time the outcome
		// and the question are one write, so the pause stands before the cycle
		// plans.
		h.step()
		got = h.state()
		if n := len(got.Decisions); n != 1 {
			t.Fatalf("the retry left %d decision(s): %+v", n, got.Decisions)
		}
		if !orchestrator.BreakerOpen(got, now) {
			t.Error("the retry folded the refusal but the node carries no pause")
		}
		if w, ok := got.Item("wi-2"); !ok || w.Phase != orchestrator.PhaseQueued {
			t.Errorf("the item beside the refusal is %+v, and the pause the retry raised has to hold it", w)
		}
	})

	t.Run("the write after it is refused", func(t *testing.T) {
		workspaceClaims(t)
		s, _ := workspaceQueue(t, now, runID)

		h := newHarness(t, s, now, "20261107-090001-beef")
		h.tick.Breaker = &orchestrator.Breaker{}
		h.tick.Store = &refusingStore{Store: h.node, allowed: 1}
		h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID, "stop")}

		// The question is in the fold's write, so there is no second one to
		// lose it: a store that refuses the write after it leaves the cycle
		// finished and the pause standing.
		if _, err := h.tick.Step(context.Background()); err != nil {
			t.Fatalf("a store that refused the write after the fold's ended the cycle as %v: the outcome and the pause have to be one write", err)
		}
		got := h.state()
		if n := len(got.Decisions); n != 1 {
			t.Fatalf("the node carries %d decision(s) after the write that landed: %+v", n, got.Decisions)
		}
		if !orchestrator.BreakerOpen(got, now) {
			t.Error("the write that landed left no pause: the question went out in a second write that this store refused")
		}
		if w, ok := got.Item("wi-2"); !ok || w.Phase != orchestrator.PhaseQueued {
			t.Errorf("the item beside the refusal is %+v, and the pause has to hold it", w)
		}

		// And the queue stays stopped: the next cycle asks nothing more and
		// starts nothing, which is what a pause lost with the second write
		// would have given up here — the attempt has reported, so the refusal
		// is never folded again.
		h.reader.reports = nil
		h.step()
		got = h.state()
		if n := len(got.Decisions); n != 1 {
			t.Errorf("the cycle after the refusal left %d decision(s), want the one question: %+v", n, got.Decisions)
		}
		if w, ok := got.Item("wi-2"); !ok || w.Phase != orchestrator.PhaseQueued {
			t.Errorf("the item beside the refusal is %+v after another cycle, and nothing may start it while the pause stands", w)
		}

	})
}

// TestNRefusalsAcrossNWindowsLeaveTheNodeBounded pins the half of the pause that
// keeps the daemon able to write at all. A workspace refusing an hour raises a
// question an hour, and nothing retires one — retention stops at an open
// decision and the loop calls no retention pass at all — so the document fills:
// past MaxStateBytes and MaxDecisions every write from every class is refused
// with ErrTooLarge, a person's included, with nothing in the loop able to bring
// the node back under.
//
// The lapsed questions go out with the next one the fold raises, so a window's
// worth of refusals leaves the node at one question and still writable.
func TestNRefusalsAcrossNWindowsLeaveTheNodeBounded(t *testing.T) {
	workspaceClaims(t)
	now := o6Now()
	const refusals = 24
	runID := func(k int) string { return fmt.Sprintf("20261107-090000-%04x", k) }

	items := make([]orchestrator.WorkItem, 0, refusals)
	intents := make([]orchestrator.Intent, 0, refusals)
	for k := range refusals {
		w, it := o6OpenItem(fmt.Sprintf("wi-%d", k), runID(k), now.Add(-time.Hour), now.Add(-30*time.Minute))
		items = append(items, w)
		intents = append(intents, it)
	}
	h := newHarness(t, tickState(t, now, items, intents), now)
	h.tick.Breaker = &orchestrator.Breaker{}

	for k := range refusals {
		h.tick.Now = clockAt(now.Add(time.Duration(k) * orchestrator.WorkspacePauseWindow))
		h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID(k), "stop")}
		h.step()
	}

	got := h.state()
	if n := len(got.Decisions); n != 1 {
		t.Fatalf("the node carries %d decision(s) after %d refusals across as many windows, want the one the last raised: a question per window that nothing retires fills the document", n, refusals)
	}
	if orchestrator.OverCap(got) {
		t.Errorf("the node is %d bytes after %d refusals, over MaxStateBytes", stateBytes(t, got), refusals)
	}

	// And it is still writable, which is what the bound is for: a person's
	// answer to the question that stands lands.
	at := now.Add(time.Duration(refusals) * orchestrator.WorkspacePauseWindow)
	if _, err := orchestrator.Apply(got, orchestrator.Patch{BaseRev: got.Rev, Ops: []orchestrator.Op{{
		Op:      orchestrator.OpDecisionResolve,
		Resolve: &orchestrator.ResolveOp{ID: got.Decisions[0].ID, Answer: "carry on"},
	}}}, orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: at}); err != nil {
		t.Fatalf("a person's answer after %d refusals was refused (%v): nothing in the loop can bring a node over the cap back under", refusals, err)
	}
}

// refusingStore is the harness's own store with one write it will not take, the
// one after the allowed ones, which is what a person's or a session's
// concurrent write looks like to the cycle that lost the ETag under it.
type refusingStore struct {
	orchestrator.Store
	// allowed is how many writes go through before the refused one; the zero
	// value refuses the first.
	allowed int
	writes  int
}

func (s *refusingStore) Save(ctx context.Context, st orchestrator.State, etag orchestrator.ETag) error {
	s.writes++
	if s.writes == s.allowed+1 {
		return fmt.Errorf("the store refused write %d: %w", s.writes, orchestrator.ErrStateMoved)
	}
	return s.Store.Save(ctx, st, etag)
}

// TestAWorkspaceRefusalRaisesOnePersonDecision pins the question the trip
// raises: it is the breaker's own — the id only the daemon may write, the same
// prefix BreakerOpen reads the pause off, and waiting_on: person, which is what
// makes a person the one who lifts it — and it carries the workspace's reason,
// because the person reading it is the one who decides what to do about it. One
// refusal is one question: the node never carries two.
func TestAWorkspaceRefusalRaisesOnePersonDecision(t *testing.T) {
	workspaceClaims(t)
	now := o6Now()
	const runID = "20261107-090000-abcd"
	s, _ := workspaceQueue(t, now, runID)

	h := newHarness(t, s, now, "20261107-090001-beef")
	h.tick.Breaker = &orchestrator.Breaker{}
	h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID, "cap")}
	h.step()

	got := h.state()
	if len(got.Decisions) != 1 {
		t.Fatalf("the node carries %d decision(s) after one refused run: %+v", len(got.Decisions), got.Decisions)
	}
	d := got.Decisions[0]
	if !strings.HasPrefix(d.ID, orchestrator.WorkspaceDecisionID+"-") {
		t.Errorf("the question's id is %q, want the trip's own under %q: the pause is read off that prefix", d.ID, orchestrator.WorkspaceDecisionID)
	}
	if !strings.HasPrefix(d.ID, orchestrator.BreakerDecisionID+"-") {
		t.Errorf("the question's id is %q, which is not under the breaker's prefix %q: a decision that is not the breaker's would not pause anything", d.ID, orchestrator.BreakerDecisionID)
	}
	if d.WaitingOn != orchestrator.OnPerson {
		t.Errorf("the question waits on %q, want a person: nobody else may lift the pause", d.WaitingOn)
	}
	if !strings.Contains(d.Question, "cap") {
		t.Errorf("the question is %q, and it has to name the refusal the run's record carried", d.Question)
	}
	if !strings.Contains(d.Question, now.Add(orchestrator.WorkspacePauseWindow).UTC().Format(time.RFC3339)) {
		t.Errorf("the question is %q, and a pause that lifts itself has to say when", d.Question)
	}
	if n := len(h.sink.got); n != 2 {
		t.Fatalf("the cycle told a person %d thing(s), want the finished run and the question: %+v", n, h.sink.got)
	}
	if last := h.sink.got[len(h.sink.got)-1]; last.Kind != orchestrator.NotifyDecisionNeeded || last.Subject != d.ID {
		t.Errorf("the last thing the cycle said is %+v, want the question it committed", last)
	}

	// A second cycle over the same node asks nothing more: the pause is every
	// unanswered question of the prefix, so a second one would have a person
	// answer the question they can see and leave the daemon stopped by the
	// other.
	h.reader.reports = nil
	h.step()
	if n := len(h.state().Decisions); n != 1 {
		t.Errorf("the node carries %d decision(s) after a second cycle, want the one question the trip raised", n)
	}
}

// TestAWorkspaceRefusalLeavesAnAuditLine pins the line the trip writes beside
// the question: which run the workspace refused, under the id the run and the
// transition derive, so the person answering can read what happened without
// replaying the state. The log is evidence and not the fence — a daemon with no
// log still stops — which is why the line is written whether or not this cycle
// raised the question and why a log that cannot be written does not fail the
// pause.
func TestAWorkspaceRefusalLeavesAnAuditLine(t *testing.T) {
	workspaceClaims(t)
	now := o6Now()
	const runID = "20261107-090000-abcd"
	s, _ := workspaceQueue(t, now, runID)

	h := newHarness(t, s, now, "20261107-090001-beef")
	h.tick.Audit = mustAudit(t, rtdbClient(t, h.db), clockAt(now))
	h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID, "settle")}
	h.step()

	path := mustPath(t, orchestrator.PathAudit)
	log := nodeObject(t, h.db.Value(path), "the audit log")
	if len(log) != 1 {
		t.Fatalf("the log carries %d line(s) for one refused run: %v", len(log), logKeys(log))
	}
	entry := nodeObject(t, log[runID+":workspace-refused"], "the refusal's line")
	if got := nodeText(t, entry, "subject"); got != runID {
		t.Errorf("the line's subject is %q, want the run the workspace refused (%q)", got, runID)
	}
	if got := nodeText(t, entry, "transition"); got != "workspace-refused" {
		t.Errorf("the line's transition is %q, want workspace-refused", got)
	}
	if got := nodeText(t, entry, "text"); !strings.Contains(got, "settle") {
		t.Errorf("the line is %q, and it has to say which of the workspace's refusals it was", got)
	}

	// A log the daemon cannot write costs the evidence and not the stop: the
	// pause is the node's question and the trip has already landed.
	t.Run("a log that cannot be written does not lift the pause", func(t *testing.T) {
		h := newHarness(t, s, now, "20261107-090001-beef")
		h.tick.Breaker = &orchestrator.Breaker{}
		h.tick.Audit = mustAudit(t, refusingPatchDB{}, clockAt(now))
		h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID, "settle")}
		if _, err := h.tick.Step(context.Background()); err == nil {
			t.Error("the cycle reported nothing for a line its log refused")
		}
		if !orchestrator.BreakerOpen(h.state(), now) {
			t.Error("a log the daemon could not write lifted the pause: the question is the node's and the line is only evidence")
		}
	})
}

// refusingPatchDB is a log that will not take a line, which is what a database
// that refused the append looks like to the trip.
type refusingPatchDB struct{}

func (refusingPatchDB) Patch(context.Context, string, map[string]any) error {
	return errors.New("the database refused the append")
}

// TestAnOrdinaryRefusalStillCountsTowardTheBreaker: the marker is not the
// refusal. A build whose seam does not claim the record — the untagged default,
// and any build whose rule the marker fails — folds it as the ordinary refusal
// O6's rule already counts: the attempt is spent, the item is not relaunched,
// and the queue carries on to the next item. Nothing pauses early, and nothing
// pauses that would not have paused before this task.
func TestAnOrdinaryRefusalStillCountsTowardTheBreaker(t *testing.T) {
	orchestrator.ResetWorkspace(t)
	now := o6Now()
	const runID = "20261107-090000-abcd"
	s, _ := workspaceQueue(t, now, runID)

	h := newHarness(t, s, now, "20261107-090001-beef")
	h.tick.Breaker = &orchestrator.Breaker{}
	h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID, "stop")}
	rep := h.step()

	got := h.state()
	if n := len(got.Decisions); n != 0 {
		t.Fatalf("a refusal no seam claimed raised %d question(s): %+v", n, got.Decisions)
	}
	if w, ok := got.Item("wi-1"); !ok || w.Phase != orchestrator.PhaseFailed {
		t.Errorf("the refused item is %+v, want the failed phase an ordinary refusal leaves", w)
	}
	if rep.Carried != 1 {
		t.Errorf("the cycle carried %d action(s), want the next item's launch: a refusal no rule claimed must not stop the queue", rep.Carried)
	}
	if w, ok := got.Item("wi-2"); !ok || w.Phase != orchestrator.PhaseRunning {
		t.Errorf("the item beside the refusal is %+v, want the launch the queue carried on with", w)
	}

	// O6's rule is the one that still governs: three cycles that do nothing
	// raise the streak's own question, which is not the workspace's — so a
	// refusal the build did not claim neither pauses early nor spends the guard.
	quiet := newHarness(t, tickState(t, now, nil, nil), now)
	quiet.tick.Breaker = &orchestrator.Breaker{}
	for range orchestrator.BreakerThreshold - 1 {
		quiet.step()
		if n := len(quiet.state().Decisions); n != 0 {
			t.Fatalf("the breaker asked before its threshold: %+v", quiet.state().Decisions)
		}
	}
	quiet.step()
	raised := quiet.state().Decisions
	if len(raised) != 1 || !strings.HasPrefix(raised[0].ID, orchestrator.BreakerDecisionID+"-") {
		t.Fatalf("the node carries %+v after the threshold, want the streak's own question", raised)
	}
	if strings.HasPrefix(raised[0].ID, orchestrator.WorkspaceDecisionID) {
		t.Errorf("the streak raised the workspace's question %q, which only a refusal may raise", raised[0].ID)
	}
}

// TestOnlyAPersonLiftsTheWorkspacePause pins who ends the pause and what else
// does not. Inside the window the question is the node's and a person's answer
// is the one write that lifts it — the daemon cannot resolve its own question,
// which is the rule that makes the pause a pause rather than a sleep — and the
// answer ends it early, before the window would have.
func TestOnlyAPersonLiftsTheWorkspacePause(t *testing.T) {
	workspaceClaims(t)
	now := o6Now()
	const runID = "20261107-090000-abcd"
	s, _ := workspaceQueue(t, now, runID)

	h := newHarness(t, s, now, "20261107-090001-beef")
	h.tick.Breaker = &orchestrator.Breaker{}
	h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID, "stop")}
	h.step()

	got := h.state()
	if !orchestrator.BreakerOpen(got, now) {
		t.Fatal("the trip left no pause on the node")
	}
	id := got.Decisions[0].ID

	// The daemon's own write is refused: a class that is not a person may not
	// close the question it raised, so a loop cannot lift its own pause.
	for _, class := range []orchestrator.Class{orchestrator.Daemon, orchestrator.Session, orchestrator.DecisionAgent} {
		if _, err := applyAs(t, got, class, "", now, orchestrator.Op{Op: orchestrator.OpDecisionResolve,
			Resolve: &orchestrator.ResolveOp{ID: id, Answer: "carry on"}}); !errors.Is(err, orchestrator.ErrActorNotAllowed) {
			t.Errorf("a %s actor resolved the workspace's question: err = %v, want %v", class, err, orchestrator.ErrActorNotAllowed)
		}
	}
	if !orchestrator.BreakerOpen(got, now) {
		t.Error("a refused write lifted the pause")
	}

	// A person answers, and the same node plans the launch again — before the
	// window, which is the whole of what "a person can lift it earlier" means.
	answered := o6Apply(t, got, orchestrator.Op{Op: orchestrator.OpDecisionResolve,
		Resolve: &orchestrator.ResolveOp{ID: id, Answer: "carry on"}},
		orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: now.Add(time.Minute)})
	if orchestrator.BreakerOpen(answered, now.Add(time.Minute)) {
		t.Error("the answered question still reads as the pause")
	}
	if n := len(orchestrator.Plan(answered, orchestrator.RunView{}, o6Caps(), now.Add(time.Minute))); n == 0 {
		t.Error("the plan after a person's answer holds nothing, and the queue has to be the daemon's again")
	}
}

// TestAWorkspacePauseExpires is the half of the bound that does not rest on the
// forger's honesty: however the pause was raised, it holds the queue for
// WorkspacePauseWindow and then it is not a pause any more. Nothing retires the
// question — it stays on the node for a person to read and to answer, and an
// answer after the window changes nothing — which is what keeps the log and the
// record of what the daemon asked.
func TestAWorkspacePauseExpires(t *testing.T) {
	workspaceClaims(t)
	now := o6Now()
	const runID = "20261107-090000-abcd"
	s, _ := workspaceQueue(t, now, runID)

	h := newHarness(t, s, now, "20261107-090001-beef")
	h.tick.Breaker = &orchestrator.Breaker{}
	h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID, "stop")}
	h.step()

	got := h.state()
	if !orchestrator.BreakerOpen(got, now) {
		t.Fatal("the trip left no pause on the node")
	}
	// A window is not a moment: the pause holds right up to the last instant
	// and is gone at the window's own edge.
	if !orchestrator.BreakerOpen(got, now.Add(orchestrator.WorkspacePauseWindow-time.Second)) {
		t.Error("the pause lifted before its window was up")
	}
	if orchestrator.BreakerOpen(got, now.Add(orchestrator.WorkspacePauseWindow)) {
		t.Error("the pause still holds at the window's edge, and the bound is the whole reason the marker may raise one")
	}

	// What the expiry buys is the queue: the same node plans again, with the
	// question still on it and unanswered.
	later := now.Add(orchestrator.WorkspacePauseWindow)
	if n := len(orchestrator.Plan(got, orchestrator.RunView{}, o6Caps(), later)); n == 0 {
		t.Error("the plan after the window holds nothing, and an expired pause is not a pause")
	}
	if len(got.Decisions) != 1 || got.Decisions[0].Resolved() {
		t.Errorf("the node carries %+v, and the expired question is left where it is for a person to read", got.Decisions)
	}

	// A person who answers it afterwards writes over a question nothing was
	// waiting on, and the answer changes nothing.
	answered := o6Apply(t, got, orchestrator.Op{Op: orchestrator.OpDecisionResolve,
		Resolve: &orchestrator.ResolveOp{ID: got.Decisions[0].ID, Answer: "carry on"}},
		orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: later})
	if n := len(orchestrator.Plan(answered, orchestrator.RunView{}, o6Caps(), later)); n == 0 {
		t.Error("the plan after a late answer holds nothing")
	}
}

// TestAForgedMarkerPausesAtMostTheWindow is the bound over the marker a run
// writes into its own record (SECURITY.md, "Runs of one repository can see each
// other"): a run's job account writes result.json, so a hostile run can put a
// workspace's marker in its own record with no workspace's service involved.
//
// The two halves are pinned here as the two ends of one claim. A record that
// claims the workspace stopped it, under a build whose seam does not claim it —
// which is every record whose halt is not the shape the workspace's own path
// writes, and the exact combinations are ee/orchestrator's
// TestWorkspaceRefusedReadsOnlyTheRunRecord — raises no pause at all: the
// forger has to write a refusal's shape, not its word. And a claim the build
// does believe, which is the worst a forger can reach, holds the queue for one
// WorkspacePauseWindow and no longer: the pause is a question with an expiry
// and not a lock, and a person who wants the queue stopped for longer stops it
// the way the other stops work, with the kill switch.
func TestAForgedMarkerPausesAtMostTheWindow(t *testing.T) {
	now := o6Now()
	const runID = "20261107-090000-abcd"
	s, _ := workspaceQueue(t, now, runID)

	t.Run("a claim the build does not believe raises nothing", func(t *testing.T) {
		orchestrator.ResetWorkspace(t)
		h := newHarness(t, s, now, "20261107-090001-beef")
		h.tick.Breaker = &orchestrator.Breaker{}
		h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID, "stop")}
		rep := h.step()
		if n := len(h.state().Decisions); n != 0 {
			t.Errorf("a marker the build's seam did not claim raised %d question(s)", n)
		}
		if rep.Carried != 1 {
			t.Errorf("the cycle carried %d action(s) beside an unclaimed marker, want the next item's launch", rep.Carried)
		}
	})

	t.Run("a claim the build believes expires", func(t *testing.T) {
		workspaceClaims(t)
		h := newHarness(t, s, now, "20261107-090001-beef")
		h.tick.Breaker = &orchestrator.Breaker{}
		h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(runID, "stop")}
		h.step()
		got := h.state()
		if !orchestrator.BreakerOpen(got, now) {
			t.Fatal("a believed marker raised no pause, so this half of the bound proves nothing")
		}
		if orchestrator.BreakerOpen(got, now.Add(orchestrator.WorkspacePauseWindow)) {
			t.Error("a believed marker paused the queue past its window, which is the one thing the bound promises a forger cannot do")
		}
	})
}
