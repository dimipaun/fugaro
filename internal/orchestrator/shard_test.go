package orchestrator_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The two nodes D2735 puts beside the state node (F34): one per imported plan
// and one for the standing backlog. Each holds a State, each keeps today's
// caps, and each is read and saved by its own ETag under the one project lease
// — so what these tests pin is that a store for a shard is the store the state
// node already had, pointed at another node, and that the state node is left
// exactly where it was.
//
// The one thing the split costs is what F44 settles, and TestPlanNodeRoundTrip
// reads it from the other node's side: the validator resolves an item's
// `depends_on` inside the one state it validates, so a dependency never crosses
// nodes.
//
// Everything runs on internal/gcpfake's Realtime Database fake: no live cloud,
// no clock, no key and no secret.

const (
	planFolder = "orchestrator-graph-estimates"
	otherPlan  = "stateless-orchestrator"
)

// TestPlanNodeRoundTrip: one imported plan has a node of its own, at
// `orchestrator/<id>/plans/<plan>`, created by the first write and read back
// from it — and the state node beside it is not where that write lands.
//
// Mutation: point the plan's shard at PathState (drop the PlansNode segment)
// and the state node comes back carrying the plan's item, which is F34 again:
// one node for a plan of a hundred tasks, every open issue and the folded
// backlog.
func TestPlanNodeRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	db := rtdbClient(t, f)
	statePath := mustPath(t, orchestrator.PathState)

	path, err := orchestrator.PathPlan(daemonID, planFolder)
	if err != nil {
		t.Fatalf("building the plan's node path: %v", err)
	}
	if want := mustPath(t, orchestrator.PathNode) + "/" + orchestrator.PlansNode + "/" + planFolder; path != want {
		t.Fatalf("a plan's node is %q, want %q", path, want)
	}
	node := mustShardNode(t, db, &stubLease{held: true}, orchestrator.PlanShard(planFolder))
	if node.Path() != path {
		t.Fatalf("the store writes %q, and the path this package builds is %q", node.Path(), path)
	}

	// Created on the first write: before it, the node is this project's empty
	// state and the database carries nothing there.
	if f.Value(path) != nil {
		t.Fatalf("the plan's node carries %v before anything wrote it", f.Value(path))
	}
	s, etag, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("the first load of a plan's node: %v", err)
	}
	if etag == "" {
		t.Fatal("an absent plan node loaded with no ETag: the first save would have no precondition")
	}
	if s.SchemaVersion != orchestrator.SchemaVersion || s.AgentID != daemonID || s.Role != orchestrator.RoleOrchestrator {
		t.Fatalf("an absent plan node loaded as %+v, not this project's empty state", s)
	}

	s.WorkItems = []orchestrator.WorkItem{shardItem("wi-x0", "the store's plan and backlog nodes")}
	if err := node.Save(ctx, s, etag); err != nil {
		t.Fatalf("creating the plan's node: %v", err)
	}

	// Read back through the store and out of the database: what a later daemon
	// loads is the document that was written, not the value this test held.
	back, backEtag, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("reloading the plan's node: %v", err)
	}
	if backEtag == etag {
		t.Errorf("the plan node's ETag is %q before and after a write", backEtag)
	}
	item, ok := back.Item("wi-x0")
	if !ok || item.Brief != "the store's plan and backlog nodes" || len(back.WorkItems) != 1 {
		t.Fatalf("the plan's node reads back as %+v", back.WorkItems)
	}
	if got := nodeObject(t, f.Value(path), "the plan's node"); nodeText(t, got, "agent_id") != daemonID {
		t.Errorf("the plan's node was written as %v", got)
	}
	// And the state node is not where that write landed: D2735 leaves it, and
	// its readers, exactly as they were.
	if f.Value(statePath) != nil {
		t.Errorf("writing the plan's node created the state node: %v", f.Value(statePath))
	}

	// F44, from the other side of the split: an item of a plan node may not
	// depend on an item of another node, because the validator resolves
	// `depends_on` inside the one state it validates. The backlog node really
	// holds the item named, so the refusal is the node's and not a missing
	// item's.
	backlog := mustShardNode(t, db, &stubLease{held: true}, orchestrator.ShardBacklog)
	bs, bsEtag, err := backlog.Load(ctx)
	if err != nil {
		t.Fatalf("loading the backlog's node: %v", err)
	}
	bs.WorkItems = []orchestrator.WorkItem{shardItem("wi-backlog", "an item of the standing backlog")}
	if err := backlog.Save(ctx, bs, bsEtag); err != nil {
		t.Fatalf("creating the backlog's node: %v", err)
	}

	across := back
	across.WorkItems = []orchestrator.WorkItem{
		shardItem("wi-x0", "the store's plan and backlog nodes"),
		func() orchestrator.WorkItem {
			w := shardItem("wi-x1", "a task that waits for the backlog")
			w.DependsOn = []string{"wi-backlog"}
			return w
		}(),
	}
	if err := node.Save(ctx, across, backEtag); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Fatalf("a plan item depending on another node's item ended as %v, not ErrInvalid: a dependency that crosses nodes is one nothing resolves (F44)", err)
	}
	if got := nodeObject(t, f.Value(path), "the plan's node"); len(listAt(t, got, "work_items")) != 1 {
		t.Errorf("the refused save wrote %v", got)
	}
	// The same item with its dependency in the same node is a document this
	// release writes, so the refusal above is the cross-node one.
	same := back
	same.WorkItems = append(same.WorkItems, func() orchestrator.WorkItem {
		w := shardItem("wi-backlog", "the same task, declared in its own node")
		return w
	}(), func() orchestrator.WorkItem {
		w := shardItem("wi-x1", "a task that waits for the backlog")
		w.DependsOn = []string{"wi-backlog"}
		return w
	}())
	if err := node.Save(ctx, same, backEtag); err != nil {
		t.Fatalf("a plan item depending on an item of its own node: %v", err)
	}

	// And a plan's node keeps today's caps: D2735 gives each node the state's
	// own State and the state's own bounds, so a plan node is refused the
	// document the state node would be refused, and the refusal stops the same
	// spawns (F34). Nothing is special-cased per node, and this is what says so.
	over, overEtag, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("loading the plan's node: %v", err)
	}
	over.WorkItems = make([]orchestrator.WorkItem, 0, orchestrator.MaxWorkItems+1)
	for i := range orchestrator.MaxWorkItems + 1 {
		over.WorkItems = append(over.WorkItems, shardItem(fmt.Sprintf("wi-%d", i), "b"))
	}
	if err := node.Save(ctx, over, overEtag); !errors.Is(err, orchestrator.ErrTooLarge) {
		t.Fatalf("a plan node carrying %d items ended as %v, not ErrTooLarge: every node keeps the state's caps", orchestrator.MaxWorkItems+1, err)
	}
}

// TestBacklogNodeRoundTrip: the standing backlog has its own node,
// `orchestrator/<id>/backlog`, holding the project's spec-less items (D2715) —
// created on the first write, read back from it, and written nowhere else.
func TestBacklogNodeRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	db := rtdbClient(t, f)

	path := mustPath(t, orchestrator.PathBacklog)
	if want := mustPath(t, orchestrator.PathNode) + "/" + orchestrator.BacklogNode; path != want {
		t.Fatalf("the backlog's node is %q, want %q", path, want)
	}
	node := mustShardNode(t, db, &stubLease{held: true}, orchestrator.ShardBacklog)
	if node.Path() != path {
		t.Fatalf("the store writes %q, and the path this package builds is %q", node.Path(), path)
	}
	if f.Value(path) != nil {
		t.Fatalf("the backlog's node carries %v before anything wrote it", f.Value(path))
	}

	s, etag, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("the first load of the backlog's node: %v", err)
	}
	if etag == "" {
		t.Fatal("an absent backlog node loaded with no ETag: the first save would have no precondition")
	}
	if s.SchemaVersion != orchestrator.SchemaVersion || s.AgentID != daemonID || s.Role != orchestrator.RoleOrchestrator {
		t.Fatalf("an absent backlog node loaded as %+v, not this project's empty state", s)
	}

	// A backlog item is the project's work with no spec: it belongs to no plan,
	// which is the whole of what D2715 means by the standing backlog.
	s.WorkItems = []orchestrator.WorkItem{shardItem("wi-b1", "let us fix this")}
	if err := node.Save(ctx, s, etag); err != nil {
		t.Fatalf("creating the backlog's node: %v", err)
	}

	back, _, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("reloading the backlog's node: %v", err)
	}
	item, ok := back.Item("wi-b1")
	if !ok || len(back.WorkItems) != 1 {
		t.Fatalf("the backlog's node reads back as %+v", back.WorkItems)
	}
	if item.Spec != "" {
		t.Errorf("the backlog's item came back with the spec %q: the standing backlog is the work with none", item.Spec)
	}
	if item.Phase != orchestrator.PhaseQueued {
		t.Errorf("the backlog's item came back %s, and an item nothing has launched is queued", item.Phase)
	}
	// The write landed at the backlog's node and nowhere else.
	for _, other := range []string{mustPath(t, orchestrator.PathState), mustPath(t, orchestrator.PathPlans)} {
		if f.Value(other) != nil {
			t.Errorf("writing the backlog's node created %s: %v", other, f.Value(other))
		}
	}
}

// TestSaveOfOneNodeKeepsOthersETags is G9's rule read across the whole subtree:
// every node is saved under its own ETag, so a write to one moves no other
// node's version — and the one project lease is the fence over all of them, a
// daemon that lost the id writing none of them.
//
// This is the test D2735's "each saved by its own ETag under the one project
// lease" is pinned by, and it is the reason a plan of a hundred tasks cannot
// cost the state node a lost race: nothing but the plan's own node moves when
// the plan is written.
//
// Mutation: give the plan and the backlog shards the state node's path, and the
// plan's write moves the state's ETag (and the state's document with it); drop
// the lease check and a daemon that lost the id writes a plan node.
func TestSaveOfOneNodeKeepsOthersETags(t *testing.T) {
	ctx := context.Background()

	t.Run("a save to one node moves no other node's ETag", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		db := rtdbClient(t, f)
		lease := &stubLease{held: true}

		state := mustNode(t, db, lease)
		plan := mustShardNode(t, db, lease, orchestrator.PlanShard(planFolder))
		other := mustShardNode(t, db, lease, orchestrator.PlanShard(otherPlan))
		backlog := mustShardNode(t, db, lease, orchestrator.ShardBacklog)
		nodes := []*orchestrator.Node{state, plan, other, backlog}

		// Each node is created by its own first save. The goals differ so that
		// the four documents do — a node holding another node's bytes is a
		// node this test could not tell apart from it.
		before := map[string]string{}
		docs := map[string]any{}
		for i, n := range nodes {
			s, etag, err := n.Load(ctx)
			if err != nil {
				t.Fatalf("loading %s: %v", n.Path(), err)
			}
			s.Goal = "node " + string(rune('a'+i))
			if err := n.Save(ctx, s, etag); err != nil {
				t.Fatalf("creating %s: %v", n.Path(), err)
			}
			_, after, err := n.Load(ctx)
			if err != nil {
				t.Fatalf("reading back %s: %v", n.Path(), err)
			}
			before[n.Path()] = string(after)
			docs[n.Path()] = f.Value(n.Path())
		}
		if len(before) != 4 {
			t.Fatalf("the four nodes read back as %v: two shards share one path", before)
		}

		// The plan is written again, with an item: the write a tick makes on
		// the node it is working on.
		s, etag, err := plan.Load(ctx)
		if err != nil {
			t.Fatalf("loading the plan's node: %v", err)
		}
		s.WorkItems = []orchestrator.WorkItem{shardItem("wi-x0", "the store's plan and backlog nodes")}
		if err := plan.Save(ctx, s, etag); err != nil {
			t.Fatalf("saving the plan's node: %v", err)
		}
		_, moved, err := plan.Load(ctx)
		if err != nil {
			t.Fatalf("re-reading the plan's node: %v", err)
		}
		if string(moved) == before[plan.Path()] {
			t.Fatalf("the plan's ETag is still %q after a write: the save did not land on its own node", moved)
		}

		for _, n := range nodes {
			if n == plan {
				continue
			}
			_, etag, err := n.Load(ctx)
			if err != nil {
				t.Fatalf("re-reading %s: %v", n.Path(), err)
			}
			if string(etag) != before[n.Path()] {
				t.Errorf("writing the plan's node moved %s's ETag from %q to %q: the nodes are not separate, and every write is every node's lost race",
					n.Path(), before[n.Path()], etag)
			}
			if got := f.Value(n.Path()); !reflect.DeepEqual(got, docs[n.Path()]) {
				t.Errorf("writing the plan's node changed %s: %v, was %v", n.Path(), got, docs[n.Path()])
			}
		}

		// And the two plans are two nodes under the one plans parent, which is
		// what a reader lists to find every plan the project has imported.
		plans := nodeObject(t, f.Value(mustPath(t, orchestrator.PathPlans)), "the plans node")
		if len(plans) != 2 {
			t.Fatalf("the plans node carries %v, want one node per plan", plans)
		}
		for _, name := range []string{planFolder, otherPlan} {
			if _, ok := plans[name]; !ok {
				t.Errorf("the plans node carries no node for %q: %v", name, plans)
			}
		}
	})

	t.Run("the one project lease fences every node", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		db := rtdbClient(t, f)
		lost := &stubLease{held: false}

		for _, n := range []*orchestrator.Node{
			mustNode(t, db, lost),
			mustShardNode(t, db, lost, orchestrator.PlanShard(planFolder)),
			mustShardNode(t, db, lost, orchestrator.ShardBacklog),
		} {
			s, etag, err := n.Load(ctx)
			if err != nil {
				t.Fatalf("loading %s: %v", n.Path(), err)
			}
			if err := n.Save(ctx, s, etag); !errors.Is(err, orchestrator.ErrLeaseLost) {
				t.Fatalf("saving %s without the lease ended as %v, not ErrLeaseLost", n.Path(), err)
			}
			if f.Value(n.Path()) != nil {
				t.Errorf("a daemon that lost the id wrote %s: %v", n.Path(), f.Value(n.Path()))
			}
		}
		if lost.checks != 3 {
			t.Errorf("the lease was checked %d times for three refused writes: every node is written under the one project lease", lost.checks)
		}
	})

	t.Run("a plan name that is not a folder name is refused at the store", func(t *testing.T) {
		for _, bad := range []string{"", "Orchestrator Graph Estimates", "a/b", "plans/../state", "graph.estimates"} {
			if _, err := orchestrator.NewNode(orchestrator.NodeOptions{
				DB: rtdbClient(t, gcpfake.NewRTDB(t)), AgentID: daemonID, Role: orchestrator.RoleOrchestrator,
				Shard: orchestrator.PlanShard(bad),
			}); !errors.Is(err, orchestrator.ErrInvalid) {
				t.Errorf("a plan named %q built a store (%v): a name the database refuses is refused here, where it is a Go error, and not in the middle of a tick (D1231)", bad, err)
			}
		}
		// The name is folded the way an item's spec is, so the plan the import
		// writes and the plan the tick reads are one node and not two.
		upper, err := orchestrator.PathPlan(daemonID, "Orchestrator-Graph-Estimates")
		if err != nil {
			t.Fatalf("a plan folder with an upper-case letter: %v", err)
		}
		lower, err := orchestrator.PathPlan(daemonID, planFolder)
		if err != nil {
			t.Fatalf("building the plan's node path: %v", err)
		}
		if upper != lower {
			t.Errorf("the plan node is %q in one spelling and %q in the other: a plan whose two spellings differed would be a plan in two nodes", upper, lower)
		}
	})

	t.Run("a shard that names no node is refused at the store", func(t *testing.T) {
		// Shard is a string, so a caller can put any value in it, and the
		// store reads whatever it is given as a plan unless it is one of the
		// three: `Shard("lease")` would write `orchestrator/<id>/plans/lease`,
		// a node no reader of this subtree ever looks at, and the save would
		// report success. So a shard that names no node is refused where the
		// store is built, with the plan names above and for their reason.
		for _, bad := range []orchestrator.Shard{
			"lease", "state", "audit", "plans", "plans/", "pland/x", "backlog/", "/backlog",
		} {
			if _, err := orchestrator.NewNode(orchestrator.NodeOptions{
				DB: rtdbClient(t, gcpfake.NewRTDB(t)), AgentID: daemonID, Role: orchestrator.RoleOrchestrator,
				Shard: bad,
			}); !errors.Is(err, orchestrator.ErrInvalid) {
				t.Errorf("the shard %q built a store (%v): a shard is one of the three nodes or a plan's own, and anything else is a node no reader looks at", bad, err)
			}
		}
		// And the three that are nodes still build, so what is refused above is
		// the shard's own shape and not every shard.
		for _, ok := range []orchestrator.Shard{
			orchestrator.ShardState, orchestrator.ShardBacklog, orchestrator.PlanShard(planFolder),
		} {
			mustShardNode(t, rtdbClient(t, gcpfake.NewRTDB(t)), nil, ok)
		}
	})
}

// mustShardNode is mustNode for a node other than the state node: a plan's or
// the backlog's.
func mustShardNode(t *testing.T, db orchestrator.CAS, lease orchestrator.LeaseGuard, shard orchestrator.Shard) *orchestrator.Node {
	t.Helper()
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{
		DB: db, AgentID: daemonID, Role: orchestrator.RoleOrchestrator, Lease: lease, Shard: shard,
	})
	if err != nil {
		t.Fatalf("building the %q node: %v", shard, err)
	}
	return node
}

// shardItem is one work item of these tests: enough of an item for the
// validator to take it, so that a save here fails for the node's reason and
// never for the item's.
func shardItem(id, brief string) orchestrator.WorkItem {
	return orchestrator.WorkItem{
		ID:        id,
		Title:     brief,
		Repo:      "owner/repo",
		Recipe:    "default",
		RigorTier: "small",
		Brief:     brief,
		Phase:     orchestrator.PhaseQueued,
	}
}
