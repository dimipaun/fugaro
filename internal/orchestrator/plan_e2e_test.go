package orchestrator_test

// A plan's graph meeting the daemon's own steps (W1): adoption of a run already
// in flight, the plan's import of the same graph, and what a finished run
// reported about the order of the work.
//
// Every piece of this is some other task's own test — adoption's (R4), the
// import's (G1), the fold's (E3) — and what is tested here is the seam between
// them, which is the one thing no package can pin alone: an item adoption wrote
// into a plan's own node is the item the import meets rather than a second one
// beside it (D2702, D2746), and a `blocks` entry a run reported lands as an edge
// of the plan's node rather than of the state node (F44, D2735). The three run
// over internal/gcpfake and the tick harness in tick_test.go, so the node each
// one reads back is a document this release accepted.

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// planTick is a tick over one plan's own node: the harness of tick_test.go with
// the store the plan's items live in (D2735) rather than the state node, and the
// audit log the fold's refusals would leave a line in, so that "no edge was
// refused" is a claim about the node and not about a log nobody wired.
func planTick(t *testing.T, plan string, s orchestrator.State, now time.Time, runs ...string) *harness {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	node := mustShardNode(t, rtdbClient(t, f), nil, orchestrator.PlanShard(plan))
	ctx := context.Background()
	_, etag, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("reading the empty plan node: %v", err)
	}
	if err := node.Save(ctx, s, etag); err != nil {
		t.Fatalf("seeding the plan's node with the test's state: %v", err)
	}
	h := &harness{
		t:        t,
		node:     node,
		db:       f,
		reader:   &tickReader{},
		launcher: &o6Launcher{},
		mint:     &o6Mint{t: t, runs: runs},
		sink:     &tickSink{},
	}
	h.tick = &orchestrator.Tick{
		Store:    node,
		Runs:     h.reader,
		Launcher: h.launcher,
		Mint:     h.mint.mint,
		Caps:     o6Caps(),
		Now:      clockAt(now),
		Notifier: h.sink,
		Audit:    mustAudit(t, rtdbClient(t, f), clockAt(now)),
	}
	return h
}

// planFact is the value of the one fact of a node whose key starts with prefix,
// or "" when the node carries none.
func planFact(s orchestrator.State, prefix string) string {
	for key, fact := range s.Facts {
		if strings.HasPrefix(key, prefix) {
			return fact.Value
		}
	}
	return ""
}

// TestAdoptionMeetsImport is D2702 and D2746 end to end: a run already in flight
// is adopted into the plan's own node before any session imports the plan, and
// the import then meets that item on its plan task instead of writing a second
// item beside it — so the node holds one item per task of the graph, the two
// tasks that wait on the first wait on the id the adopted item really has, and
// the run's own brief and phase are left exactly as adoption wrote them.
//
// Mutation: key adoption's item by anything but the run's plan task, or have the
// import match on the graph's node id, and the node carries two items for A (or
// a dependency naming an id no node holds, which the validator refuses).
func TestAdoptionMeetsImport(t *testing.T) {
	at := o6Now()
	f, stateNode, _, adopted := adopt(t, at, adoptRun(runA, "demo/A"))
	if adopted != 1 {
		t.Fatalf("adoption wrote %d items for one run in flight", adopted)
	}
	// The hand-over a session in a checkout acts on: adoption names the plan it
	// could not import itself (D2721, R5), and the import below is what that
	// fact asks for.
	if value := planFact(nodeState(t, stateNode), "plan_in_flight_demo"); !strings.Contains(value, "demo") {
		t.Errorf("the state node's fact about the adopted plan reads %q, and one fact per plan is what hands it to a session", value)
	}

	ctx := context.Background()
	node := mustShardNode(t, rtdbClient(t, f), &stubLease{held: true}, orchestrator.PlanShard("demo"))
	s, etag, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("reading the plan's node adoption wrote: %v", err)
	}
	if len(s.WorkItems) != 1 || s.WorkItems[0].ID != runA || s.WorkItems[0].PlanTask != "demo/A" {
		t.Fatalf("the plan's node carries %+v, and adoption writes the run as the item of the task it stands for", s.WorkItems)
	}

	p, ids, err := orchestrator.PlanImportPatch(s, planImportInput())
	if err != nil {
		t.Fatalf("the import refused a plan whose first task was already in flight: %v", err)
	}
	// A's item is the adopted one, and the list the person is shown names the id
	// the node really holds rather than the graph's.
	if !slices.Equal(ids, []string{runA, "B", "C"}) {
		t.Fatalf("the import names the items %v, want the adopted run's id in A's place (%s)", ids, runA)
	}
	if _, ok := planImportOp(p, runA); ok {
		t.Error("the import wrote an op over the item a run in flight is working from (D2704)")
	}
	next := applyPlanImport(t, s, p, at)
	if err := node.Save(ctx, next, etag); err != nil {
		t.Fatalf("saving the imported plan's node: %v", err)
	}

	after := planNodeState(t, f, "demo")
	if len(after.WorkItems) != 3 {
		t.Fatalf("the plan's node holds %d items, and the graph has three tasks with A's already adopted", len(after.WorkItems))
	}
	if _, ok := after.Item("A"); ok {
		t.Error("the node carries an item under the graph's own id as well as the adopted one: one task is one item (D2746)")
	}
	w, ok := after.Item(runA)
	if !ok {
		t.Fatalf("the import dropped the adopted item %s", runA)
	}
	if w.Phase != orchestrator.PhaseRunning || !w.Adopted || w.Brief != "the brief the run was launched with" || w.Approved != nil {
		t.Errorf("the adopted item reads %+v, and the import rewrote what the run in flight is working from", w)
	}
	for _, id := range []string{"B", "C"} {
		d, ok := after.Item(id)
		if !ok {
			t.Fatalf("the graph's task %q was not imported", id)
		}
		if !slices.Equal(d.DependsOn, []string{runA}) {
			t.Errorf("%q depends on %v, and the graph's edge names A — which is the adopted item %s (D2746)", id, d.DependsOn, runA)
		}
		if d.Spec != "demo" || d.Phase != orchestrator.PhaseQueued || d.Approved != nil {
			t.Errorf("%q reads %+v, and the import writes the plan's items queued and unapproved", id, d)
		}
	}
}

// TestDiscoveredEdgeEndToEnd is E3 at the node a plan's runs are folded in: a
// finished run reports one `blocks` entry naming another task of its own plan,
// and the daemon adds the edge in front of that item — in the plan's node, and
// not in the state node — un-approving it with one fact naming the item, the
// edge and the run (D2709, D2740).
//
// The item that waits is approved and queued, so the cycle that adds the edge is
// a cycle that would have launched it: what keeps it back is the edge the fold
// landed in the same patch, which is the whole reason the two are one change.
//
// Mutation: fold the report against the state node, or skip the approval the
// edge moves, and the assertions below fail — an item on the wrong node, or a
// run started for work a person approved before the graph moved under it.
func TestDiscoveredEdgeEndToEnd(t *testing.T) {
	now := o6Now()
	run, attempt := foldRun(runA, runA, now.Add(-time.Hour), now.Add(-time.Minute), planTask("demo/A"))
	s := planImportState()
	s.WorkItems = []orchestrator.WorkItem{run, foldItem("D", orchestrator.PhaseQueued, now.Add(-time.Hour), planTask("demo/D"))}
	s.Intents = []orchestrator.Intent{attempt}

	h := planTick(t, "demo", s, now)
	h.reader.reports = []orchestrator.RunReport{foldReport(runA, orchestrator.OutcomeSucceeded,
		[]runstore.DiscoveredItem{namesTask("demo/D")}, nil)}
	h.step()

	after := h.state()
	d := foldItemOf(t, after, "D")
	if !slices.Equal(d.DependsOn, []string{runA}) {
		t.Fatalf("the item the run blocks waits for %v, and the run's own item is %s", d.DependsOn, runA)
	}
	if d.Phase != orchestrator.PhaseQueued || d.Approved != nil {
		t.Errorf("the item reads %s with approval %+v, and an edge moves the spec a person approved, so the approval goes with it (D2740)", d.Phase, d.Approved)
	}
	if w := foldItemOf(t, after, runA); w.Phase != orchestrator.PhaseDone {
		t.Errorf("the run's own item is %s after its run succeeded, and the edge and the outcome land in one patch", w.Phase)
	}
	if value := planFact(after, "graph_edge_"); !strings.Contains(value, "blocks D") || !strings.Contains(value, runA) {
		t.Errorf("the node's edge fact reads %q, and it names which run, which edge and which item the person must look at (D2740)", value)
	}
	if lines := foldLog(t, h); lines != nil {
		t.Errorf("the fold refused an edge it should have added: %v", lines)
	}
	if len(h.launcher.asked) != 0 {
		t.Errorf("the cycle started %v, and the edge took the item it would have started out of the plan", h.launcher.asked)
	}
}
