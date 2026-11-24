package orchestrator_test

// Dependencies across orchestrators (CG3, D3208, D3220).
//
// `depends_on` may name `<orchestrator>/<item>`, and the orchestrator a cycle
// works reads that item's phase off the other orchestrator's shards each tick —
// the referenced ones alone, and never by listing them. A bare name still
// resolves inside the one node the item is on (F44, F17), so the same dependency
// written without its orchestrator is refused wherever the item it names lives
// on another node.
//
// A cycle across orchestrators cannot be waited out: every item in it waits for
// another, and no cycle of the loop breaks one. Each orchestrator sets its own
// items in the cycle blocked, with the cycle on their notes and a notification
// for the person who edits a dependency to break it (D3220) — and it writes
// nothing of the other orchestrator's, which is what makes the repair safe to
// run from either end and impossible to run from one.
//
// Everything here runs over internal/gcpfake's Realtime Database fake and the
// tick harness in tick_test.go: no live cloud, no clock of this machine, no key
// and no secret.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// otherID is the second orchestrator of these tests: a project-name-shaped id,
// so the reference rule admits it (ParseItemRef), and one this release's node
// check takes — a named `<project>~<name>` id is CG1a's and is not what a
// reference's shape is about.
const otherID = "beta"

// TestCrossDependsOnValidates is the first half of D3208: a dependency written
// as `<orchestrator>/<item>` is a shape the validator admits, and one it does
// not admit is still refused. Whether the reference resolves is not a shape at
// all — a node sees only its own items, so the tick's reading is what resolves
// one, and a reference nothing resolves holds the item that names it.
func TestCrossDependsOnValidates(t *testing.T) {
	now := o6Now()

	t.Run("a reference to another orchestrator's item is a dependency", func(t *testing.T) {
		s := stateWithAQueuedItem(t, now)
		s.WorkItems[0].DependsOn = []string{otherID + "/wi-9"}
		if err := orchestrator.ValidateState(s); err != nil {
			t.Fatalf("an item depending on %s/wi-9 was refused: %v", otherID, err)
		}
	})

	t.Run("a plan task of another orchestrator is one too", func(t *testing.T) {
		s := stateWithAQueuedItem(t, now)
		s.WorkItems[0].DependsOn = []string{otherID + "/demo/C"}
		if err := orchestrator.ValidateState(s); err != nil {
			t.Fatalf("an item depending on the plan task %s/demo/C was refused: %v", otherID, err)
		}
	})

	t.Run("a reference to this orchestrator's own item is one too", func(t *testing.T) {
		s := stateWithAQueuedItem(t, now)
		s.WorkItems[0].DependsOn = []string{daemonID + "/wi-2"}
		if err := orchestrator.ValidateState(s); err != nil {
			t.Fatalf("an item depending on %s/wi-2, an item of its own orchestrator, was refused: %v", daemonID, err)
		}
	})

	t.Run("an item may not depend on itself by reference", func(t *testing.T) {
		s := stateWithAQueuedItem(t, now)
		s.WorkItems[0].DependsOn = []string{daemonID + "/wi-1"}
		err := orchestrator.ValidateState(s)
		if !errors.Is(err, orchestrator.ErrInvalid) {
			t.Fatalf("an item depending on itself as %s/wi-1 was accepted: %v", daemonID, err)
		}
	})

	t.Run("a reference listed twice is refused like a bare one", func(t *testing.T) {
		s := stateWithAQueuedItem(t, now)
		s.WorkItems[0].DependsOn = []string{otherID + "/wi-9", otherID + "/wi-9"}
		if err := orchestrator.ValidateState(s); !errors.Is(err, orchestrator.ErrInvalid) {
			t.Fatalf("the same reference written twice was accepted: %v", err)
		}
	})

	t.Run("a reference the rule does not admit is refused", func(t *testing.T) {
		for _, dep := range []string{
			"Beta/wi-9",       // not an orchestrator id: a project name is lower-case
			otherID + "/",     // names no item
			otherID + "/wi 9", // not a work item id and not a plan task
			"wi-9/",           // an orchestrator half that is an item id
		} {
			s := stateWithAQueuedItem(t, now)
			s.WorkItems[0].DependsOn = []string{dep}
			if err := orchestrator.ValidateState(s); !errors.Is(err, orchestrator.ErrInvalid) {
				t.Errorf("the dependency %q was accepted: %v", dep, err)
			}
		}
	})
}

// TestCrossShardStillRefused is F44 and F17 unchanged: a bare name resolves in
// the node the item is on and nowhere else, however real the item it names is
// on the node beside it. The same dependency written as a reference is the one
// the same node accepts, which is the whole of what D3208 adds.
func TestCrossShardStillRefused(t *testing.T) {
	now := o6Now()
	db := rtdbClient(t, gcpfake.NewRTDB(t))

	// The state node really carries the item the plan item names: the refusal
	// below is about where a bare name resolves and not about an item nobody
	// wrote.
	state := stateWithAQueuedItem(t, now)
	state.WorkItems = append(state.WorkItems, shardItem("wi-2", "work of the state node"))
	mustSaveState(t, mustNode(t, db, nil), state)

	plan := stateWithAQueuedItem(t, now)
	plan.WorkItems[0].DependsOn = []string{"wi-2"}
	planNode := mustShardNode(t, db, nil, orchestrator.PlanShard(planFolder))
	if err := saveState(planNode, plan); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("a plan item depending on a bare wi-2, which only the state node carries, was accepted: %v", err)
	}

	plan.WorkItems[0].DependsOn = []string{daemonID + "/wi-2"}
	if err := saveState(planNode, plan); err != nil {
		t.Fatalf("the same dependency written as %s/wi-2 was refused: %v", daemonID, err)
	}
}

// TestTickWaitsOnOtherOrchestrator is the other half of D3208: the item waits
// on the phase the reference resolved to, and the cycle reads only the
// orchestrators its own items name — one id each, and never the whole project.
func TestTickWaitsOnOtherOrchestrator(t *testing.T) {
	now := o6Now()
	const runID = "20261107-090000-abcd"

	// own is the item the cycle plans: queued, approved, and depending on the
	// reference the sub-test is about. The dependency is written before the
	// approval, so the digest covers the item the person read.
	own := func(dep string) orchestrator.WorkItem {
		w := workItemOf("wi-1")
		w.DependsOn = []string{dep}
		return o6ApprovedBy(w, now.Add(-time.Hour))
	}
	// far is another orchestrator's item, on the shard a test puts it on.
	far := func(id string, phase orchestrator.Phase) orchestrator.WorkItem {
		w := shardItem(id, "the other orchestrator's work")
		w.Phase = phase
		return w
	}
	// farShards is the state one orchestrator's shards read as: the item, and
	// the node it is on first.
	farShards := func(items ...orchestrator.WorkItem) map[string][]orchestrator.State {
		s := stateWithAQueuedItem(t, now)
		s.AgentID = otherID
		s.WorkItems = items
		return map[string][]orchestrator.State{otherID: {s}}
	}

	t.Run("an unfinished dependency holds the item", func(t *testing.T) {
		reader := &crossReader{shards: farShards(far("wi-9", orchestrator.PhaseQueued))}
		h := crossTick(t, o6Node(t, now, own(otherID+"/wi-9")), now, reader, runID)

		rep := h.step()
		if len(h.launcher.asked) != 0 {
			t.Fatalf("the cycle started %v for an item whose dependency in %s is not done", h.launcher.asked, otherID)
		}
		if rep.Carried != 0 {
			t.Errorf("the cycle carried %d actions for an item that is waiting", rep.Carried)
		}
		if w, _ := h.state().Item("wi-1"); w.Phase != orchestrator.PhaseQueued {
			t.Errorf("the item is %s, and a dependency that is not done holds it queued", w.Phase)
		}
		if !slices.Equal(reader.asked, []string{otherID}) {
			t.Errorf("the cycle read %v, and the only orchestrator its items name is %s", reader.asked, otherID)
		}
	})

	t.Run("a dependency that is done starts the item", func(t *testing.T) {
		reader := &crossReader{shards: farShards(far("wi-9", orchestrator.PhaseDone))}
		h := crossTick(t, o6Node(t, now, own(otherID+"/wi-9")), now, reader, runID)

		rep := h.step()
		if !slices.Equal(h.launcher.asked, []string{runID}) {
			t.Fatalf("the cycle started %v, and %s/wi-9 is done", h.launcher.asked, otherID)
		}
		if rep.Carried != 1 {
			t.Errorf("the cycle carried %d actions for an item whose dependency is done", rep.Carried)
		}
		if w, _ := h.state().Item("wi-1"); w.RunID != runID {
			t.Errorf("the item recorded run %q, and the cycle started %q", w.RunID, runID)
		}
	})

	t.Run("a plan task of the other orchestrator resolves in its shards", func(t *testing.T) {
		// The item is on the second shard, so a reading that searched only the
		// first would not find it: what a reference resolves against is every
		// node the named orchestrator has (D3208, ReadShards).
		empty := stateWithAQueuedItem(t, now)
		empty.AgentID = otherID
		empty.WorkItems = []orchestrator.WorkItem{}
		plan := far("wi-9", orchestrator.PhaseDone)
		plan.PlanTask = "demo/C"
		planState := stateWithAQueuedItem(t, now)
		planState.AgentID = otherID
		planState.WorkItems = []orchestrator.WorkItem{plan}
		reader := &crossReader{shards: map[string][]orchestrator.State{
			otherID: {empty, planState},
		}}

		h := crossTick(t, o6Node(t, now, own(otherID+"/demo/C")), now, reader, runID)
		h.step()
		if !slices.Equal(h.launcher.asked, []string{runID}) {
			t.Fatalf("the cycle started %v, and %s's plan task demo/C is done on its second shard", h.launcher.asked, otherID)
		}
	})

	t.Run("a reference nothing resolves holds the item", func(t *testing.T) {
		reader := &crossReader{}
		h := crossTick(t, o6Node(t, now, own(otherID+"/wi-9")), now, reader, runID)

		h.step()
		if len(h.launcher.asked) != 0 {
			t.Fatalf("the cycle started %v for a reference nothing resolves", h.launcher.asked)
		}
	})

	t.Run("a reference to this orchestrator's own item is read off its own nodes", func(t *testing.T) {
		reader := &crossReader{}
		done := workItemOf("wi-2")
		done.Phase = orchestrator.PhaseDone
		h := crossTick(t, o6Node(t, now, own(daemonID+"/wi-2"), done), now, reader, runID)

		h.step()
		if !slices.Equal(h.launcher.asked, []string{runID}) {
			t.Fatalf("the cycle started %v, and its own %s/wi-2 is done", h.launcher.asked, daemonID)
		}
		if len(reader.asked) != 0 {
			t.Errorf("the cycle read %v for a reference to an item of its own orchestrator", reader.asked)
		}
	})
}

// TestCrossCycleBlocksWithEscalation is D3220: the item that sits on a cycle
// across orchestrators is set blocked, its note names the cycle, and the person
// who can break it is told — a cycle nothing in the loop can wait out or repair
// is the one reading a person has to get.
func TestCrossCycleBlocksWithEscalation(t *testing.T) {
	now := o6Now()
	h, _ := crossCycle(t, now)

	rep := h.step()
	if rep.Blocked != 1 {
		t.Fatalf("the cycle blocked %d items where one of its own is on the cycle", rep.Blocked)
	}
	if rep.Outcome != orchestrator.CycleProgress {
		t.Errorf("a cycle that blocked an item ended %q, and stopping a deadlocked item is work", rep.Outcome)
	}
	if len(h.launcher.asked) != 0 {
		t.Errorf("the cycle started %v for an item it then blocked", h.launcher.asked)
	}

	want := daemonID + "/wi-1 -> " + otherID + "/wi-9 -> " + daemonID + "/wi-1"
	w, ok := h.state().Item("wi-1")
	if !ok {
		t.Fatalf("the node no longer carries wi-1")
	}
	if w.Phase != orchestrator.PhaseBlocked {
		t.Fatalf("the item is %s after the cycle found the deadlock, not blocked", w.Phase)
	}
	if !strings.Contains(w.Note, want) {
		t.Errorf("the item's note is %q, and the cycle it is on is %q", w.Note, want)
	}

	told := h.sink.cycleBlocked()
	if len(told) != 1 {
		t.Fatalf("the cycle told a person %d times about the deadlock, and it blocked one item", len(told))
	}
	if told[0].Subject != "wi-1" || !strings.Contains(told[0].Text, want) {
		t.Errorf("the notification is about %q (%q), and the cycle is %q", told[0].Subject, told[0].Text, want)
	}

	entries := auditEntries(t, h.db, daemonID)
	if len(entries) != 1 {
		t.Fatalf("the log carries %d lines for one blocked item", len(entries))
	}
	if entries[0].Subject != "wi-1" || entries[0].Transition != "cycle-blocked" {
		t.Errorf("the line is %q/%q, and the item blocked is wi-1", entries[0].Subject, entries[0].Transition)
	}
	if !strings.Contains(entries[0].Text, want) {
		t.Errorf("the line's text is %q, and the cycle is %q", entries[0].Text, want)
	}
}

// TestCrossCycleWritesOnlyOwnState is the amendment D3220 carries: each
// orchestrator in the cycle sets its own items blocked and never writes
// another's. What this test holds is the far orchestrator's node itself — its
// revision, its item's phase, and its log — read back out of the same database
// the cycle read it from.
func TestCrossCycleWritesOnlyOwnState(t *testing.T) {
	now := o6Now()
	h, far := crossCycle(t, now)
	before := storedRev(t, h.db, far)

	rep := h.step()
	if rep.Blocked != 1 {
		t.Fatalf("the cycle blocked %d items where one of its own is on the cycle", rep.Blocked)
	}
	// The work happened, so what the assertions below read is a cycle that acted
	// and not one that did nothing at all.
	if w, _ := h.state().Item("wi-1"); w.Phase != orchestrator.PhaseBlocked {
		t.Fatalf("the cycle's own item is %s, and this test is about what it did to the other orchestrator", w.Phase)
	}

	if after := storedRev(t, h.db, far); after != before {
		t.Fatalf("the other orchestrator's node moved from revision %d to %d", before, after)
	}
	back, _, err := far.Load(context.Background())
	if err != nil {
		t.Fatalf("reading the other orchestrator's node back: %v", err)
	}
	w, ok := back.Item("wi-9")
	if !ok {
		t.Fatalf("%s's node no longer carries wi-9", otherID)
	}
	if w.Phase != orchestrator.PhaseQueued {
		t.Errorf("%s's item is %s, and nothing of that orchestrator's is this cycle's to write", otherID, w.Phase)
	}
	if entries := auditEntries(t, h.db, otherID); len(entries) != 0 {
		t.Errorf("the cycle wrote %d lines into %s's log", len(entries), otherID)
	}
}

// crossCycle is the deadlock two orchestrators can be in: an item of this
// orchestrator's depending on one of another's, and that one depending back —
// the cycle D3220 is about, built through the reading D3208 resolves.
func crossCycle(t *testing.T, now time.Time) (*harness, *orchestrator.Node) {
	t.Helper()
	return crossCycleOver(t, now, workItemOf("wi-1"))
}

// crossCycleOver is crossCycle with this cycle's own item as the caller built
// it, because what blockable has to leave alone is the item a run is moving
// through: running, and review, where the run has reported and a pull request
// waits for its merge. A test reaches either by handing the item in that phase
// with the run that is carrying it.
func crossCycleOver(t *testing.T, now time.Time, own orchestrator.WorkItem) (*harness, *orchestrator.Node) {
	t.Helper()
	own.DependsOn = []string{otherID + "/wi-9"}
	other := shardItem("wi-9", "the other orchestrator's work")
	other.DependsOn = []string{daemonID + "/wi-1"}
	far := stateWithAQueuedItem(t, now)
	far.AgentID = otherID
	far.WorkItems = []orchestrator.WorkItem{other}
	return twoOrchestrators(t, o6Node(t, now, o6ApprovedBy(own, now.Add(-time.Hour))), far, now)
}

// TestCrossCycleLeavesAStartedItemAlone is the rule the cycle's own write
// follows: it blocks an item that is waiting on the deadlock, and an item a run
// is moving through is not one. Review is the case that matters — the phase
// table would move it to blocked and let the write through, and the write is a
// repair that cannot be undone, because blocked has no edge to done and the
// merge the item is waiting for is the only thing that can finish it (D2705).
func TestCrossCycleLeavesAStartedItemAlone(t *testing.T) {
	now := o6Now()
	for _, phase := range []orchestrator.Phase{orchestrator.PhaseRunning, orchestrator.PhaseReview} {
		t.Run(string(phase), func(t *testing.T) {
			own := workItemOf("wi-1")
			own.Phase = phase
			own.RunID = "20261107-090000-abcd"
			h, _ := crossCycleOver(t, now, own)

			rep := h.step()
			if rep.Blocked != 0 {
				t.Fatalf("the cycle blocked %d items, and the one on it is %s with a run carrying it", rep.Blocked, phase)
			}
			w, ok := h.state().Item("wi-1")
			if !ok {
				t.Fatalf("the node no longer carries wi-1")
			}
			if w.Phase == orchestrator.PhaseBlocked {
				t.Fatalf("the cycle set an item in %s blocked, and blocked has no move to done", phase)
			}
			if told := h.sink.cycleBlocked(); len(told) != 0 {
				t.Errorf("the cycle told a person about a deadlock it did not have: %v", told)
			}
			if entries := auditEntries(t, h.db, daemonID); len(entries) != 0 {
				t.Errorf("the cycle wrote %d lines into the log for an item it did not block", len(entries))
			}
		})
	}
}

// twoOrchestrators is one cycle's own node and another orchestrator's node in
// one fake database, and the tick over the first of them: the second is read
// through the same client the first is, which is the reading the daemon wires
// (CrossShards, D3208) — so a test of what a cycle does to the other
// orchestrator reads the document itself and not a seam's memory of it.
//
// The minter is given no run id at all: a cycle over these two nodes has
// nothing to launch, and a start that reached the launcher would fail the test
// rather than pass quietly.
func twoOrchestrators(t *testing.T, own, far orchestrator.State, now time.Time) (*harness, *orchestrator.Node) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	db := rtdbClient(t, f)
	ownNode := mustNode(t, db, nil)
	farNode, err := orchestrator.NewNode(orchestrator.NodeOptions{
		DB: db, AgentID: otherID, Role: orchestrator.RoleOrchestrator,
	})
	if err != nil {
		t.Fatalf("building %s's node: %v", otherID, err)
	}
	mustSaveState(t, ownNode, own)
	mustSaveState(t, farNode, far)

	h := &harness{
		t: t, node: ownNode, db: f, reader: &tickReader{}, launcher: &o6Launcher{},
		mint: &o6Mint{t: t}, sink: &tickSink{},
	}
	h.tick = &orchestrator.Tick{
		Store:    ownNode,
		Runs:     h.reader,
		Launcher: h.launcher,
		Mint:     h.mint.mint,
		Caps:     o6Caps(),
		Now:      clockAt(now),
		Notifier: h.sink,
		Audit:    mustAudit(t, db, clockAt(now)),
		Cross:    orchestrator.CrossShards{DB: db, Role: orchestrator.RoleOrchestrator},
	}
	return h, farNode
}

// crossTick is newHarness with the cross reference seam wired: one state node,
// the reader a test hands it, and the log the cycle's own lines land in.
func crossTick(t *testing.T, s orchestrator.State, now time.Time, cross orchestrator.CrossReader, runs ...string) *harness {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	db := rtdbClient(t, f)
	node := mustNode(t, db, nil)
	mustSaveState(t, node, s)

	h := &harness{
		t: t, node: node, db: f, reader: &tickReader{}, launcher: &o6Launcher{},
		mint: &o6Mint{t: t, runs: runs}, sink: &tickSink{},
	}
	h.tick = &orchestrator.Tick{
		Store:    node,
		Runs:     h.reader,
		Launcher: h.launcher,
		Mint:     h.mint.mint,
		Caps:     o6Caps(),
		Now:      clockAt(now),
		Notifier: h.sink,
		Audit:    mustAudit(t, db, clockAt(now)),
		Cross:    cross,
	}
	return h
}

// crossReader is a CrossReader a test sets: the shards each orchestrator
// answers with, and the ids it was asked for, in order — which is how a test
// holds the cycle to reading the referenced orchestrators alone.
type crossReader struct {
	shards map[string][]orchestrator.State
	asked  []string
	err    error
}

func (r *crossReader) Shards(_ context.Context, agentID string) ([]orchestrator.State, error) {
	r.asked = append(r.asked, agentID)
	if r.err != nil {
		return nil, r.err
	}
	return r.shards[agentID], nil
}

// cycleBlocked is the notifications of that kind the sink was handed.
func (s *tickSink) cycleBlocked() []orchestrator.Notification {
	var out []orchestrator.Notification
	for _, n := range s.got {
		if n.Kind == orchestrator.NotifyCycleBlocked {
			out = append(out, n)
		}
	}
	return out
}

// mustSaveState writes a state at a node through the node's own store, so that
// what a test reads back is a document this release accepted.
func mustSaveState(t *testing.T, node *orchestrator.Node, s orchestrator.State) {
	t.Helper()
	if err := saveState(node, s); err != nil {
		t.Fatalf("seeding a node with the test's state: %v", err)
	}
}

// saveState is that write, returning what the node refused rather than failing:
// a test about a refusal is one that means to read the error.
func saveState(node *orchestrator.Node, s orchestrator.State) error {
	ctx := context.Background()
	_, etag, err := node.Load(ctx)
	if err != nil {
		return err
	}
	return node.Save(ctx, s, etag)
}

// auditEntries is one orchestrator's log as it stands.
func auditEntries(t *testing.T, f *gcpfake.RTDB, agentID string) []orchestrator.Entry {
	t.Helper()
	read, err := orchestrator.ReadAudit(context.Background(), rtdbClient(t, f), agentID, 0)
	if err != nil {
		t.Fatalf("reading %s's audit log: %v", agentID, err)
	}
	return read.Entries
}
