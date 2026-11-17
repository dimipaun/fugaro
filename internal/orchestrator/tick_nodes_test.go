package orchestrator_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The tick over a project's whole subtree (X4, D2743).
//
// D2735 gives a project a node per imported plan and one for the standing
// backlog beside the state node, and D2743 says what the caps count: every node
// of the project, and never one node on its own. F53 is the defect these tests
// pin — `CountsOf` (`caps.go`) counted one state, so a project that imported
// three plans would have had the allowance its policy names several times over.
//
// Each test is about one half of the acceptance:
//
//   - TestCapsCountedAcrossNodes: max_running and the spawn windows are read
//     over every node, so an attempt recorded in a plan's node or the backlog's
//     holds the state node's own launches back — and the same state node, with
//     no node beside it, launches.
//   - TestMainNodeUnaffectedByFullBacklog: one node over its own cap plans
//     nothing and stops nothing else; the state node beside it launches.
//   - TestLaunchesFromEveryNode: with room, the cycle plans and carries a launch
//     from every node, each landing on the node whose item it is.
//
// Everything runs over internal/gcpfake's Realtime Database fake: no live
// cloud, no clock of this machine, no key and no secret.

// withShard seeds one node beside the state node — a plan's or the backlog's —
// and hands it to the tick, so a test drives a cycle over the whole subtree. The
// order the nodes are added in is the order the tick works them, the state node
// first (D2743, and see Tick.Nodes).
func (h *harness) withShard(t *testing.T, shard orchestrator.Shard, s orchestrator.State) *orchestrator.Node {
	t.Helper()
	node := mustShardNode(t, rtdbClient(t, h.db), nil, shard)
	ctx := context.Background()
	_, etag, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("reading the empty %q node: %v", shard, err)
	}
	if err := node.Save(ctx, s, etag); err != nil {
		t.Fatalf("seeding the %q node with the test's state: %v", shard, err)
	}
	h.tick.Nodes = append(h.tick.Nodes, node)
	return node
}

// shardState is one node beside the state node read back through its own store,
// rather than remembered from what the tick returned.
func shardState(t *testing.T, node *orchestrator.Node) orchestrator.State {
	t.Helper()
	s, _, err := node.Load(context.Background())
	if err != nil {
		t.Fatalf("reading %s back: %v", node.Path(), err)
	}
	return s
}

// readyNode is a node carrying one item a person approved and nothing has
// launched: the shape a plan starts from, in whatever node the item sits.
func readyNode(t *testing.T, now time.Time, id string, approvedAt time.Time) orchestrator.State {
	t.Helper()
	return o6Node(t, now, o6Item(id, orchestrator.PhaseQueued, approvedAt))
}

// frozenStore is one node a test hands the tick whole: the state it answers and
// the writes it takes in memory. It exists for the one state a real store cannot
// carry — a node over its size cap, which the validator refuses on the way in
// and on the way out (validate.go) — because that is the case OverCap is read
// for: a document that is on the node and is no longer one this release would
// have written.
type frozenStore struct {
	s     orchestrator.State
	etag  orchestrator.ETag
	saves int
}

func (f *frozenStore) Load(context.Context) (orchestrator.State, orchestrator.ETag, error) {
	return f.s, f.etag, nil
}

func (f *frozenStore) Save(_ context.Context, s orchestrator.State, _ orchestrator.ETag) error {
	f.s = s
	f.etag += "+"
	f.saves++
	return nil
}

// TestCapsCountedAcrossNodes is D2743 read at the tick: the counts a cycle plans
// under are the sum over every node it works, so a run or an attempt recorded in
// a plan's node or the backlog's holds back the launches of the state node
// beside it (F53).
//
// The cycle that shows it is the same state node, the same item and the same
// caps twice: once with nothing beside the node — where the item starts — and
// once with a plan's node holding the attempt that spends the allowance. What
// changed between the two runs is the node the count spans, and nothing else.
//
// Mutation: count one state (the defect F53 reports, `CountsOf` of the node
// being planned) and both cycles below launch the state node's item, whatever
// the plan's node carries.
func TestCapsCountedAcrossNodes(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)

	// Two nodes with a run in flight each — an attempt committed and unreported
	// — one of them stamped half a minute ago and the other half an hour ago: the
	// two are counted alike in some windows and differently in others, so a sum
	// that dropped either node, or counted one twice, cannot read as the figure
	// below.
	t.Run("a node's counts are added to the project's", func(t *testing.T) {
		mainItem, mainInFlight := o6OpenItem("wi-main", "20261107-115900-aaaa", approvedAt, now.Add(-30*time.Second))
		main := o6Node(t, now, mainItem)
		main.Intents = []orchestrator.Intent{mainInFlight}
		planItem, planInFlight := o6OpenItem("wi-plan", "20261107-115900-bbbb", approvedAt, now.Add(-30*time.Minute))
		plan := o6Node(t, now, planItem)
		plan.Intents = []orchestrator.Intent{planInFlight}

		want := orchestrator.Counts{Running: 2, Minute: 1, Hour: 2, Day: 2}
		if got := orchestrator.CountsOfAll([]orchestrator.State{main, plan}, now); got != want {
			t.Fatalf("the project counts %+v where its two nodes say %+v", got, want)
		}
		// And one node alone is what CountsOf has always said: this is the two
		// readings added and not a second way of counting one.
		if got, one := orchestrator.CountsOfAll([]orchestrator.State{main}, now), orchestrator.CountsOf(main, now); got != one {
			t.Errorf("a project of one node counts %+v where CountsOf says %+v", got, one)
		}
		// What the sum is worth at the running cap: either node on its own has
		// room for one more run under max_running = 2, and the project the two
		// make does not.
		two := withCaps(func(c *orchestrator.Caps) { c.MaxRunning = 2 })
		for _, one := range []struct {
			what string
			s    orchestrator.State
		}{{"the state node's", main}, {"the plan's", plan}} {
			if !orchestrator.CountsOf(one.s, now).Room(two, 0) {
				t.Fatalf("%s own reading has no room under max_running = 2: %+v", one.what, orchestrator.CountsOf(one.s, now))
			}
		}
		if want.Room(two, 0) {
			t.Errorf("the project's reading %+v has room for a launch beside two runs in flight and a running cap of 2", want)
		}
		// And at the minute's: the state node's attempt is the one in the window,
		// so the plan's node alone has room under a minute cap of 1 and the
		// project the two make has none.
		spawn := withCaps(func(c *orchestrator.Caps) { c.SpawnPerMinute = 1 })
		if !orchestrator.CountsOf(plan, now).Room(spawn, 0) {
			t.Fatalf("the plan node's own reading has no room under a minute cap of 1: %+v", orchestrator.CountsOf(plan, now))
		}
		if want.Room(spawn, 0) {
			t.Errorf("the project's reading %+v has room under a minute cap of 1, and the state node's attempt is in the minute", want)
		}
	})

	t.Run("max_running counts a plan node's run in flight", func(t *testing.T) {
		const planRun = "20261107-115900-bbbb"
		inFlightItem, inFlight := o6OpenItem("wi-plan", planRun, approvedAt, now.Add(-time.Minute))
		planState := o6Node(t, now, inFlightItem)
		planState.Intents = []orchestrator.Intent{inFlight}

		// The control: the same state node, the same item and the same caps with
		// no node beside it. The item starts, so what holds it below is the run
		// the plan's node records and not the item.
		alone := newHarness(t, readyNode(t, now, "wi-main", approvedAt), now, "20261107-120000-aaaa")
		alone.tick.Caps = withCaps(func(c *orchestrator.Caps) { c.MaxRunning = 1 })
		if rep := alone.step(); rep.Carried != 1 {
			t.Fatalf("a cycle over the state node alone carried %d action(s), and its item is approved and ready", rep.Carried)
		}

		h := newHarness(t, readyNode(t, now, "wi-main", approvedAt), now, "20261107-120000-bbbb")
		h.tick.Caps = withCaps(func(c *orchestrator.Caps) { c.MaxRunning = 1 })
		plan := h.withShard(t, orchestrator.PlanShard(planFolder), planState)

		rep := h.step()
		if rep.Carried != 0 {
			t.Fatalf("the cycle carried %d action(s) while the plan's node holds the project's one run in flight and max_running is 1", rep.Carried)
		}
		s := h.state()
		if w, ok := s.Item("wi-main"); !ok || w.Phase != orchestrator.PhaseQueued || w.RunID != "" {
			t.Fatalf("the state node's item reads back as %+v after a cycle with no room", w)
		}
		if len(s.Intents) != 0 {
			t.Errorf("the state node committed %d attempt(s) with no room for one", len(s.Intents))
		}
		// The cycle read the plan node's own attempt — the caps cannot be counted
		// over a node nobody read — and left it alone: the run it records is
		// inside its claim window, so recovery has nothing to re-issue either.
		if !slices.Contains(h.reader.asked, planRun) {
			t.Errorf("the cycle asked the reader about %v, and the plan's node records the attempt %q", h.reader.asked, planRun)
		}
		if got := shardState(t, plan); len(got.Intents) != 1 || got.Intents[0].RunID != planRun || got.Intents[0].Outcome != "" {
			t.Errorf("the plan node reads back as %+v after a cycle that had nothing to do on it", got.Intents)
		}
	})

	t.Run("the spawn windows count a plan node's attempts", func(t *testing.T) {
		// An attempt that reported half a minute ago: it is in no run in flight,
		// and the minute that spawn caps count over is where it sits. It is
		// recorded in the plan's node, and the state node's own intents are
		// empty, so nothing about the state node holds its item back.
		spentItem, spent := o6FailedItem("wi-plan", approvedAt, now.Add(-30*time.Second))
		planState := o6Node(t, now, spentItem)
		planState.Intents = []orchestrator.Intent{spent}
		tight := func(t *testing.T, perMinute int, runs ...string) *harness {
			t.Helper()
			h := newHarness(t, readyNode(t, now, "wi-main", approvedAt), now, runs...)
			h.tick.Caps = withCaps(func(c *orchestrator.Caps) { c.SpawnPerMinute = perMinute })
			h.withShard(t, orchestrator.PlanShard(planFolder), planState)
			return h
		}

		held := tight(t, 1, "20261107-120000-aaaa")
		if rep := held.step(); rep.Carried != 0 {
			t.Fatalf("the cycle carried %d action(s) with the minute cap spent by a plan node's attempt", rep.Carried)
		}
		if w, ok := held.state().Item("wi-main"); !ok || w.Phase != orchestrator.PhaseQueued {
			t.Errorf("the state node's item reads back as %+v, and the plan's attempt is what the minute counted", w)
		}
		// Widening the minute by one lets the launch through, so what held it was
		// the count and not the item: the state node is planned first and the
		// project's allowance is spent on the work a person queued there.
		wider := tight(t, 2, "20261107-120000-bbbb")
		rep := wider.step()
		if rep.Carried != 1 {
			t.Fatalf("the cycle carried %d action(s) where widening the minute to two leaves room for one", rep.Carried)
		}
		s := wider.state()
		w, ok := s.Item("wi-main")
		if !ok || w.RunID != "20261107-120000-bbbb" {
			t.Fatalf("the state node's item reads back as %+v after the launch", w)
		}
	})
}

// TestMainNodeUnaffectedByFullBacklog is the acceptance's second half: a node
// over its own cap stops the launches of that node alone, and the nodes beside
// it carry on (D2743, and D2720's headroom rule — no node can be filled from
// outside, so a backlog at its bound is a state the daemon must survive rather
// than one it writes).
//
// The backlog under test is over MaxStateBytes, which no write of this release
// can leave on a node (validate.go refuses the document on both sides), so the
// tick meets it as the reading OverCap exists for: a frozenStore hands it back
// as it stands. What the cycle does with it is the whole assertion — the item
// beside it starts, the blocked node is planned from for nothing and is not
// written to at all.
//
// Mutation: read OverCap over the project rather than over the node being
// planned (or drop it from planNode) and the state node starts nothing because
// the backlog is full — the defect D2735's split exists to end.
func TestMainNodeUnaffectedByFullBacklog(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	// The run the over-cap node's own item holds in the last sub-test below: an
	// attempt committed and unreported, with a report that says it succeeded —
	// the fold's own op on the node beside the state node.
	const overCapRun = "20261107-090000-abcd"

	// B4's ingest stops 20 items below the backlog's caps, and this is the node
	// past them: the fixture's long tail of history and its long briefs, with one
	// item a person approved and nothing has launched — the item a plan starts
	// from, so a cycle that launched nothing from this node is one that read the
	// cap and not the item.
	full := bloatedState(t, now)
	i := slices.IndexFunc(full.WorkItems, func(w orchestrator.WorkItem) bool { return w.ID == "wi-approved" })
	if i < 0 {
		t.Fatal("the fixture carries no approved item")
	}
	full.WorkItems[i] = approvedItem(t, "wi-approved", now, approvedAt)
	if w := full.WorkItems[i]; w.Approved == nil || w.Approved.SpecDigest != orchestrator.SpecDigest(w) {
		t.Fatalf("the item under test %+v is not approved over the spec it has", w.Approved)
	}
	if !orchestrator.OverCap(full) {
		t.Fatalf("the node under test is %d bytes and the cap is %d", stateBytes(t, full), orchestrator.MaxStateBytes)
	}

	t.Run("a backlog over its cap stops its own launches only", func(t *testing.T) {
		h := newHarness(t, readyNode(t, now, "wi-main", approvedAt), now, "20261107-120000-aaaa")
		backlog := &frozenStore{s: full, etag: "etag-backlog"}
		h.tick.Nodes = append(h.tick.Nodes, backlog)

		rep := h.step()
		if rep.Carried != 1 {
			t.Fatalf("the cycle carried %d action(s) beside a backlog over its cap, and the state node's item is ready", rep.Carried)
		}
		s := h.state()
		w, ok := s.Item("wi-main")
		if !ok || w.Phase != orchestrator.PhaseRunning || w.RunID != "20261107-120000-aaaa" {
			t.Fatalf("the state node's item reads back as %+v: the node beside it is over its own cap", w)
		}
		if backlog.saves != 0 {
			t.Errorf("the cycle wrote the node it planned nothing from %d time(s)", backlog.saves)
		}
		if got, found := backlog.s.Item("wi-approved"); !found || got.Phase != orchestrator.PhaseFailed {
			t.Errorf("the over-cap node's item reads back as %+v, and a node over its cap starts nothing", got)
		}
	})

	t.Run("an over-cap node holding a run that reports stops nothing", func(t *testing.T) {
		// The same node, with an item whose attempt is committed and a run that
		// has since reported: the fold of that node has ops to write, and Apply
		// checks the state a patch is made from as well as the one it makes, so
		// the write the fold derives is one the validator refuses — and that
		// refusal is this step's error, which ends the whole cycle before the
		// state node's own merge and launches. A node over its cap is that
		// node's affair, so the fold is skipped there as the plan is, and the
		// state node beside it carries on.
		spent, attempt := o6OpenItem("wi-approved", overCapRun, approvedAt, now.Add(-2*time.Hour))
		held := full
		held.WorkItems = slices.Clone(full.WorkItems)
		held.WorkItems[i] = spent
		held.Intents = []orchestrator.Intent{attempt}
		if !orchestrator.OverCap(held) {
			t.Fatalf("the node under test is %d bytes and the cap is %d", stateBytes(t, held), orchestrator.MaxStateBytes)
		}

		h := newHarness(t, readyNode(t, now, "wi-main", approvedAt), now, "20261107-120000-aaaa")
		backlog := &frozenStore{s: held, etag: "etag-backlog"}
		h.tick.Nodes = append(h.tick.Nodes, backlog)
		h.reader.reports = []orchestrator.RunReport{{
			RunID: overCapRun, Started: true, Finished: true, Outcome: orchestrator.OutcomeSucceeded,
		}}

		rep := h.step()
		if rep.Carried != 1 {
			t.Fatalf("the cycle carried %d action(s) beside an over-cap node whose own run reported", rep.Carried)
		}
		if rep.Folded != 0 {
			t.Errorf("the cycle folded %d run(s) into a node no write of this release may land on", rep.Folded)
		}
		if backlog.saves != 0 {
			t.Errorf("the cycle wrote the node it planned nothing from %d time(s)", backlog.saves)
		}
		s := h.state()
		w, ok := s.Item("wi-main")
		if !ok || w.Phase != orchestrator.PhaseRunning || w.RunID != "20261107-120000-aaaa" {
			t.Fatalf("the state node's item reads back as %+v, and the node beside it is over its own cap", w)
		}
	})

	t.Run("the same item in a backlog with room is launched", func(t *testing.T) {
		// The same approved item, in a backlog small enough to plan from: it is
		// launched, so what stopped it above was the node's own size and not the
		// item or the caps of the project.
		room := o6Node(t, now, approvedItem(t, "wi-approved", now, approvedAt))
		h := newHarness(t, readyNode(t, now, "wi-main", approvedAt), now, "20261107-120000-bbbb", "20261107-120000-cccc")
		backlog := &frozenStore{s: room, etag: "etag-backlog"}
		h.tick.Nodes = append(h.tick.Nodes, backlog)

		rep := h.step()
		if rep.Carried != 2 {
			t.Fatalf("the cycle carried %d action(s) where both nodes have an approved item ready", rep.Carried)
		}
		if backlog.saves == 0 {
			t.Fatal("the backlog's item was planned and the intent behind the launch was never committed to its node")
		}
		got, ok := backlog.s.Item("wi-approved")
		if !ok || got.Phase != orchestrator.PhaseRunning || got.RunID != "20261107-120000-cccc" {
			t.Fatalf("the backlog's item reads back as %+v after its launch", got)
		}
		if n := len(backlog.s.Intents); n != 1 {
			t.Errorf("the backlog's node recorded %d attempt(s) for one launch", n)
		}
	})
}

// TestLaunchesFromEveryNode is the acceptance's first half at the carry: with
// room, a cycle plans from every node it works and each launch lands on the node
// whose item it is (D2735) — the state node first, then the plan and backlog
// nodes in the order the tick carries them, which is the order one project's
// allowance is spent in.
//
// Mutation: plan only the state node (or carry only it) and the two nodes beside
// it keep their items queued and their nodes unwritten, whatever room the caps
// leave.
func TestLaunchesFromEveryNode(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const (
		mainRun    = "20261107-120000-aaaa"
		planRun    = "20261107-120000-bbbb"
		backlogRun = "20261107-120000-cccc"
	)
	h := newHarness(t, readyNode(t, now, "wi-main", approvedAt), now, mainRun, planRun, backlogRun)
	plan := h.withShard(t, orchestrator.PlanShard(planFolder), readyNode(t, now, "wi-plan", approvedAt))
	backlog := h.withShard(t, orchestrator.ShardBacklog, readyNode(t, now, "wi-backlog", approvedAt))

	rep := h.step()
	if rep.Carried != 3 {
		t.Fatalf("the cycle carried %d action(s) where each of its three nodes has an approved item ready", rep.Carried)
	}
	if rep.Outcome != orchestrator.CycleProgress {
		t.Errorf("a cycle that started three runs ended %q", rep.Outcome)
	}
	if got, want := h.launcher.started, []string{mainRun, planRun, backlogRun}; !slices.Equal(got, want) {
		t.Fatalf("the cycle started %v, and the nodes are worked in the order %v", got, want)
	}
	for i, id := range []string{"wi-main", "wi-plan", "wi-backlog"} {
		if got := h.launcher.specs[i].ItemID; got != id {
			t.Errorf("launch %d started the item %q where the cycle planned %q", i, got, id)
		}
	}

	// Every launch landed on its own node: the item is running there, under the
	// run id the cycle minted for it, and no node carries another node's item.
	for _, tc := range []struct {
		what string
		node *orchestrator.Node
		s    orchestrator.State
		id   string
		run  string
	}{
		{"the state node", h.node, h.state(), "wi-main", mainRun},
		{"the plan's node", plan, shardState(t, plan), "wi-plan", planRun},
		{"the backlog's node", backlog, shardState(t, backlog), "wi-backlog", backlogRun},
	} {
		w, ok := tc.s.Item(tc.id)
		if !ok || w.Phase != orchestrator.PhaseRunning || w.RunID != tc.run {
			t.Errorf("%s reads back as %+v, and %s was launched there under %q", tc.what, w, tc.id, tc.run)
		}
		if n := len(tc.s.Intents); n != 1 {
			t.Fatalf("%s records %d attempt(s) for one launch", tc.what, n)
		}
		if got := tc.s.Intents[0]; got.RunID != tc.run || got.Outcome != "" {
			t.Errorf("%s records the attempt %+v, and the run it started has not reported", tc.what, got)
		}
		for _, other := range []string{"wi-main", "wi-plan", "wi-backlog"} {
			if other == tc.id {
				continue
			}
			if _, ok := tc.s.Item(other); ok {
				t.Errorf("%s carries %s, which belongs to another node", tc.what, other)
			}
		}
	}
}

// TestAFinishedRunInAPlanNodeFoldsThere: a run recorded in a plan's node is that
// node's to fold, and the fold lands there and nowhere else (D2735) — the state
// node beside it is not written with the plan's item, and the cycle counts the
// fold it made once.
//
// Without it nothing in the plan's node ever moves: the report is read, the
// item stays in `running` because nothing carried the outcome into it, and no
// launch from that node happens again — the item holds its own attempt open for
// good, which is the item the state node's own fold cannot see.
//
// Mutation: fold only the state node and the plan node's item reads back
// `running` with its attempt unreported, while the same run on the state node
// would have moved it.
func TestAFinishedRunInAPlanNodeFoldsThere(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const planRun = "20261107-090000-abcd"

	// The plan's item with a run that has since succeeded and left no pull
	// request: the fold's own reading, and the one that moves the item to `done`.
	finished, finishedIntent := o6OpenItem("wi-plan", planRun, approvedAt, now.Add(-time.Hour))
	planState := o6Node(t, now, finished)
	planState.Intents = []orchestrator.Intent{finishedIntent}

	h := newHarness(t, readyNode(t, now, "wi-main", approvedAt), now, "20261107-120000-aaaa")
	plan := h.withShard(t, orchestrator.PlanShard(planFolder), planState)
	h.reader.reports = []orchestrator.RunReport{{
		RunID: planRun, Started: true, Finished: true, Outcome: orchestrator.OutcomeSucceeded,
	}}

	rep := h.step()
	if rep.Folded != 1 {
		t.Fatalf("the cycle folded %d run(s) where the plan's node records one that reported", rep.Folded)
	}
	if rep.Carried != 1 {
		t.Errorf("the cycle carried %d action(s), and the state node's own item is approved and ready", rep.Carried)
	}

	got := shardState(t, plan)
	w, ok := got.Item("wi-plan")
	if !ok || w.Phase != orchestrator.PhaseDone {
		t.Fatalf("the plan node's item reads back as %+v, and its run succeeded without a pull request", w)
	}
	if len(got.Intents) != 1 || got.Intents[0].Outcome != orchestrator.OutcomeSucceeded {
		t.Fatalf("the plan node's attempt reads back as %+v, and its run reported succeeded", got.Intents)
	}
	// The state node took its own launch and nothing of the plan's: one item,
	// one attempt, and neither of them the plan node's.
	main := h.state()
	if _, ok := main.Item("wi-plan"); ok {
		t.Errorf("the state node carries the plan's item: %+v", main.WorkItems)
	}
	if len(main.Intents) != 1 || main.Intents[0].RunID != "20261107-120000-aaaa" {
		t.Errorf("the state node's attempts are %+v, and only the run this cycle started is its own", main.Intents)
	}
}

// TestTheMergeReadReachesEveryNode: an item waiting on its pull request is read
// and moved in whatever node carries it (D2735). A cycle that read the state
// node alone would leave a plan node's item in `review` for good — its
// dependents inside that node would never start, and outcome() reads every node's
// wait as a cycle in flight, so the daemon would report CycleInFlight for as long
// as the project stands.
//
// The two items are on the same repository and in different nodes, so the one
// listing this cadence makes answers for both: the read is per repository
// (D2742) and not one per node, which is what keeps a project's node count out
// of the host's budget.
//
// Mutation: run the merge with the state node bound, whatever the read found
// (the defect the review found, c.merge with node 0 for every op) and the plan
// node's item stays in `review`, its dependent never starts, and the cycle ends
// CycleInFlight.
func TestTheMergeReadReachesEveryNode(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const afterRun = "20261107-120000-bbbb"

	// Both nodes hold an item whose attempt reported succeeded having left a pull
	// request, and the plan's node holds a third that waits on one of them: what
	// the merge unlocks is a launch inside the node the merge had to reach.
	mainItem, mainAttempt := mergeReviewItem("wi-main", mergeRepo, mergeRunA, approvedAt)
	planItem, planAttempt := mergeReviewItem("wi-plan", mergeRepo, mergeRunB, approvedAt)
	after := mergeDependent("wi-after", mergeRepo, []string{"wi-plan"}, approvedAt)
	mainState := o6Node(t, now, mainItem)
	mainState.Intents = []orchestrator.Intent{mainAttempt}
	planState := o6Node(t, now, planItem, after)
	planState.Intents = []orchestrator.Intent{planAttempt}

	h := newMergeWaitHarness(t, mainState, now, afterRun)
	plan := h.withShard(t, orchestrator.PlanShard(planFolder), planState)
	h.reader.reports = []orchestrator.RunReport{mergeReport(mergeRunA, 12), mergeReport(mergeRunB, 14)}
	h.reads.states[mergeKey(mergeRepo, 12)] = orchestrator.MergeMerged
	h.reads.states[mergeKey(mergeRepo, 14)] = orchestrator.MergeMerged

	rep := h.step()
	if rep.Merged != 2 {
		t.Fatalf("the cycle merged %d item(s) where each of its nodes carries one whose pull request merged", rep.Merged)
	}
	if got, want := h.reads.lists, []string{mergeRepo}; !slices.Equal(got, want) {
		t.Errorf("the cadence listed %v, and one repository is read once for every node its items are on", got)
	}
	if got, want := h.reads.gets, []string{mergeKey(mergeRepo, 12), mergeKey(mergeRepo, 14)}; !slices.Equal(got, want) {
		t.Errorf("the cadence read the pull requests %v, and the nodes are worked in the order %v", got, want)
	}

	if w, ok := h.state().Item("wi-main"); !ok || w.Phase != orchestrator.PhaseDone {
		t.Errorf("the state node's item reads back as %+v, and its pull request merged", w)
	}
	got := shardState(t, plan)
	if w, ok := got.Item("wi-plan"); !ok || w.Phase != orchestrator.PhaseDone {
		t.Fatalf("the plan node's item reads back as %+v, and its pull request merged", w)
	}
	w, ok := got.Item("wi-after")
	if !ok || w.Phase != orchestrator.PhaseRunning || w.RunID != afterRun {
		t.Fatalf("the plan node's dependent reads back as %+v, and the item it waits on merged in this same cycle", w)
	}
	if rep.Carried != 1 {
		t.Errorf("the cycle carried %d action(s) where the plan node's dependent is the only item left to start", rep.Carried)
	}
	if rep.Outcome != orchestrator.CycleProgress {
		t.Errorf("a cycle that moved two items and started one ended %q", rep.Outcome)
	}
}

// TestTheDaemonsPauseHoldsEveryNode: the question that stops the daemon is the
// state node's whatever node the run behind it belonged to, so a cycle that
// plans from the nodes beside it has to read the pause over all of them — a
// plan node or the backlog that read it off its own state would find none and
// start work into the refusal it was raised for (O19, D2022 (a)).
//
// Both questions are the same pause (BreakerOpen): the one a workspace's
// refusal raises on the cycle it lands, and the one a tripped breaker leaves
// behind. A cycle that folded a refusal raises it in the fold — before it plans
// anything — so the nodes beside it are held in that same cycle.
//
// Mutation: read the pause off the node being planned (planNode's own state,
// where it lived while the daemon had one node) and the plan's item is launched
// in the cycle the workspace refusal was folded, and again while the state
// node's question stands unanswered.
func TestTheDaemonsPauseHoldsEveryNode(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Hour)
	const refusedRun = "20261107-090000-abcd"

	t.Run("a refusal folded this cycle holds the nodes beside it", func(t *testing.T) {
		workspaceClaims(t)
		// The refusal ends wi-1 and holds wi-2, which is the state node's own
		// launch: two run ids, so a cycle that launched from either node has
		// minted one.
		s, _ := workspaceQueue(t, now, refusedRun)
		h := newHarness(t, s, now, "20261107-090001-beef", "20261107-090001-cafe")
		h.tick.Breaker = &orchestrator.Breaker{}
		plan := h.withShard(t, orchestrator.PlanShard(planFolder), readyNode(t, now, "wi-plan", approvedAt))
		h.reader.reports = []orchestrator.RunReport{workspaceRefusedRun(refusedRun, "stop")}

		rep := h.step()
		if rep.Folded != 1 {
			t.Fatalf("the cycle folded %d run(s) where the refused one reported", rep.Folded)
		}
		if rep.Carried != 0 {
			t.Fatalf("the cycle carried %d action(s) in the cycle it folded the workspace's refusal", rep.Carried)
		}
		if got := h.launcher.started; len(got) != 0 {
			t.Fatalf("the cycle started %v, and the pause a workspace's refusal raises stops every node of the project", got)
		}
		got := shardState(t, plan)
		if w, ok := got.Item("wi-plan"); !ok || w.Phase != orchestrator.PhaseQueued || w.RunID != "" {
			t.Errorf("the plan node's item reads back as %+v, and nothing may start it while the pause stands", w)
		}
		if n := len(got.Intents); n != 0 {
			t.Errorf("the plan node recorded %d attempt(s) in a cycle that started nothing", n)
		}
		if !orchestrator.BreakerOpen(h.state(), now) {
			t.Error("the cycle folded the refusal and left the daemon without the pause that stops it")
		}
	})

	t.Run("a standing question holds the nodes beside it", func(t *testing.T) {
		// The same two ready nodes twice: once with the state node carrying the
		// question a tripped breaker left and once with nothing on it. The
		// launches in the second are what says the first was held by the pause
		// and not by the fixture.
		open := func(t *testing.T, s orchestrator.State, runs ...string) (*harness, *orchestrator.Node) {
			t.Helper()
			h := newHarness(t, s, now, runs...)
			return h, h.withShard(t, orchestrator.PlanShard(planFolder), readyNode(t, now, "wi-plan", approvedAt))
		}

		held, plan := open(t, breakerQuestion(t, readyNode(t, now, "wi-main", approvedAt), now),
			"20261107-120001-aaaa", "20261107-120001-bbbb")
		if rep := held.step(); rep.Carried != 0 {
			t.Fatalf("the cycle carried %d action(s) while the state node's question stands unanswered", rep.Carried)
		}
		if got := held.launcher.started; len(got) != 0 {
			t.Fatalf("the cycle started %v, and the pause a tripped breaker raised stops the whole project", got)
		}
		got := shardState(t, plan)
		if w, ok := got.Item("wi-plan"); !ok || w.Phase != orchestrator.PhaseQueued || w.RunID != "" {
			t.Errorf("the plan node's item reads back as %+v, and nothing may start it while the pause stands", w)
		}
		if n := len(got.Intents); n != 0 {
			t.Errorf("the plan node recorded %d attempt(s) while the daemon was paused", n)
		}

		control, controlPlan := open(t, readyNode(t, now, "wi-main", approvedAt),
			"20261107-120001-cccc", "20261107-120001-dddd")
		if rep := control.step(); rep.Carried != 2 {
			t.Fatalf("the same two nodes with nothing on the state node carried %d action(s)", rep.Carried)
		}
		if w, ok := shardState(t, controlPlan).Item("wi-plan"); !ok || w.RunID != "20261107-120001-dddd" {
			t.Errorf("the plan node's item reads back as %+v, and the same node launched with no pause standing", w)
		}
	})
}

// breakerQuestion is the state node carrying the question a tripped breaker
// left and nobody has answered: the pause a plan refuses over, added the way
// the daemon's own loop adds it (BreakerDecisionID's prefix is the daemon's to
// write, patch.go's applyDecisionAdd).
func breakerQuestion(t *testing.T, s orchestrator.State, now time.Time) orchestrator.State {
	t.Helper()
	return o6Apply(t, s, orchestrator.Op{Op: orchestrator.OpDecisionAdd, Decision: &orchestrator.DecisionOp{
		ID:        orchestrator.BreakerDecisionID + "-" + now.UTC().Format("20060102T150405Z"),
		Question:  "The orchestrator has ended three cycles in a row without starting or finishing anything. What should it do?",
		WaitingOn: orchestrator.OnPerson,
	}}, orchestrator.Actor{Class: orchestrator.Daemon, Now: now})
}

// unreadableStore is a node whose document will not load — what a corrupted or
// half-written one looks like from the cycle's side — and whose writes are
// counted, so a test can say that nothing landed on it.
type unreadableStore struct {
	err   error
	saves int
}

func (u *unreadableStore) Load(context.Context) (orchestrator.State, orchestrator.ETag, error) {
	return orchestrator.State{}, "", u.err
}

func (u *unreadableStore) Save(context.Context, orchestrator.State, orchestrator.ETag) error {
	u.saves++
	return u.err
}

// TestAProjectNobodyCanReadAsksNothing is the guard X4's own step needs: a cycle
// now reads a node per imported plan and one for the backlog, so a project whose
// first node will not load is a cycle that read nothing at all — and the breaker,
// which counts the failed cycles, must not raise its question against a node it
// never read. The trip is the third cycle of the streak (BreakerThreshold), and
// what this pins is that the question is not written from a cycle with no node
// behind it.
//
// Mutation: bind the state node without checking that the cycle read anything
// and a daemon whose node is corrupted panics — which is a crash loop where the
// breaker was supposed to stop one.
func TestAProjectNobodyCanReadAsksNothing(t *testing.T) {
	now := o6Now()
	h := newHarness(t, stateWithAQueuedItem(t, now), now)
	broken := &unreadableStore{err: errors.New("the document is not a state this release reads")}
	h.tick.Store = broken
	h.tick.Breaker = &orchestrator.Breaker{}

	for i := range orchestrator.BreakerThreshold {
		if _, err := h.tick.Step(context.Background()); !errors.Is(err, broken.err) {
			t.Fatalf("cycle %d ended as %v, want the store's own failure", i+1, err)
		}
	}
	if !h.tick.Breaker.Tripped() {
		t.Fatalf("%d failed cycles left the breaker untripped, and the threshold is %d", orchestrator.BreakerThreshold, orchestrator.BreakerThreshold)
	}
	if broken.saves != 0 {
		t.Errorf("%d write(s) landed on a node whose document never loaded", broken.saves)
	}
}
