package orchestrator_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The claims' tests (CG4, D3209).
//
// A project has several orchestrators (D3206, D3207) and they read one plan in
// common: a plan task's identity is the plan's folder and the task (D2704), so
// two orchestrators that imported one plan mean one piece of work by it. The
// claim is what keeps one of them at it — written before the item's work starts
// (Starter) and read by every other orchestrator of the project the same way —
// and the fence is the store's if-match, as it is for the lease: of two
// orchestrators that both read one absent claim node exactly one write lands,
// and the loser re-reads and re-decides. A claim is held until the item it
// stands on is done or cancelled, and never taken over before that — the
// owner's ruling of 2026-11-23, which dropped the lease grace this task was
// first written with, because a daemon deletes its lease node on every clean
// exit while the runs it started are still going.
//
// **The red step that was run and watched fail** (CG4's, and the one this file
// exists for): with the take rewritten to write after a lost race — re-read the
// ETag and write anyway, which is what "drop the if-match" means in code —
// TestClaimFirstWins reports both orchestrators holding one plan task, and with
// Take answering that it took every claim, TestLaunchSkipsClaimed starts the
// second run of an item another orchestrator is already on. Restored; see the
// pull request.
//
// Everything runs on internal/gcpfake's Realtime Database fake, which honours
// if-match: no live cloud, no clock but the one a test sets, no key and no
// secret.

const (
	// The two orchestrators of one project these tests drive: `fugaro` is the
	// project's own node and `fugaro~night` a named one (D3206, D3207). Both
	// are ids C-ORCH admits and nothing more: a claim is keyed by, and read
	// back through, an orchestrator id, which is not agentRE (that carries no
	// `~` until CG1a).
	cg4First = "fugaro"
	cg4Rival = "fugaro~night"

	// cg4Second is the project's other orchestrator where a cycle is driven
	// rather than a take: an id a node path can carry (agentRE), which the `~`
	// of cg4Rival cannot until CG1a. The plan task is what the claim is keyed
	// by, so the two cycles of the test hold it under the same node whatever
	// their own ids are.
	cg4Second = "orchestrator-b"

	// cg4PlanTask is one node of a plan both orchestrators imported: the
	// identity two of them share (D2704), which is what makes a claim on it one
	// node for the whole project (D3209).
	cg4PlanTask = "demo/A"

	// cg4Beat is the daemon a staged lease node names. A lease's holder is a
	// daemon and not an orchestrator id (Lease), so a test that stages a lease
	// node beside a claim names one.
	cg4Beat = "20261107-120000-aaaa"
)

// cg4Ref is the claim reference of cg4PlanTask as one orchestrator builds it.
func cg4Ref(t *testing.T, agentID string) orchestrator.ItemRef {
	t.Helper()
	return orchestrator.ItemRef{Orchestrator: agentID, Item: cg4PlanTask}
}

// cg4Path is that item's claim node, built through the package's own ClaimPath
// rather than spelled here: a path this package builds is never one a test
// wrote, so a wrong path is a failure here rather than a node nobody read.
func cg4Path(t *testing.T, agentID string) string {
	t.Helper()
	path, err := orchestrator.ClaimPath(cg4Ref(t, agentID))
	if err != nil {
		t.Fatalf("building the claim path: %v", err)
	}
	return path
}

// Keys is the shallow listing the claims are refreshed through: a claim is read
// and written through a CAS and listed through a Keys — orchestrator.ClaimStore
// is both — and this double is what a test counts a claim's writes on, so it
// needs both halves. The listing itself is not counted here: what a refresh is
// held to is the node it wrote and the ones it left alone.
func (c *countingCAS) Keys(ctx context.Context, path string) ([]string, error) {
	return c.inner.Keys(ctx, path)
}

// cg4Claim is one orchestrator's claims over the fake, with the clock the test
// set: nothing here reads a real one.
func cg4Claim(t *testing.T, db orchestrator.ClaimStore, agentID string, now orchestrator.ServerNow) *orchestrator.Claim {
	t.Helper()
	claim, err := orchestrator.NewClaim(db, agentID, now)
	if err != nil {
		t.Fatalf("building the claims of %q: %v", agentID, err)
	}
	return claim
}

// cg4Held is what the claim node carries: the holder it names and the server
// time it was written at.
func cg4Held(t *testing.T, f *gcpfake.RTDB, path string) (string, string) {
	t.Helper()
	doc := nodeObject(t, f.Value(path), "the claim")
	return nodeText(t, doc, "holder"), nodeText(t, doc, "at")
}

// TestClaimFirstWins is the single-claim fence and CG4's headline test: two
// orchestrators of one project have the same plan task in front of them, and
// exactly one of them takes it.
//
// The loser's read is the real read, held open until the winner has written, so
// both formed their decision from the same absent node and the same ETag —
// which is the race that matters — and both then ask for the claim. Exactly one
// write lands: the loser's is refused rtdb.ErrPrecondition, it re-reads, finds
// the winner's claim and answers false without writing again. An orchestrator
// that forced its write past the 412 would put two runs on one plan task.
//
// Mutation: drop the if-match from Take (write after a lost race without
// re-deciding) and both orchestrators report the item theirs.
func TestClaimFirstWins(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	now := o6Now()

	path := cg4Path(t, cg4First)
	if other := cg4Path(t, cg4Rival); other != path {
		t.Fatalf("the two orchestrators claim %q and %q: a plan task's claim is one node for the project", path, other)
	}
	if want := orchestrator.ClaimsRoot + "/" + budget.Key(cg4PlanTask); path != want {
		t.Errorf("the claim of %q is at %q, not under the project's claims node", cg4PlanTask, path)
	}
	if orchestrator.ValidOrchestratorID(orchestrator.ClaimsKey) {
		t.Errorf("%q is an orchestrator id, so a reader listing the subtree's ids cannot tell the claims node from one", orchestrator.ClaimsKey)
	}

	read := make(chan struct{})
	hold := make(chan struct{})
	loserDB := &countingCAS{inner: rtdbClient(t, f), afterRead: func() { close(read); <-hold }}
	winnerDB := &countingCAS{inner: rtdbClient(t, f)}

	winner := cg4Claim(t, winnerDB, cg4First, clockAt(now))
	loser := cg4Claim(t, loserDB, cg4Rival, clockAt(now))

	type outcome struct {
		taken bool
		err   error
	}
	loserDone := make(chan outcome, 1)
	go func() {
		taken, err := loser.Take(ctx, cg4Ref(t, cg4Rival))
		loserDone <- outcome{taken, err}
	}()

	<-read
	taken, err := winner.Take(ctx, cg4Ref(t, cg4First))
	if err != nil {
		t.Fatalf("the winner's take: %v", err)
	}
	if !taken {
		t.Fatal("the winner read an empty claim node and was not given the claim")
	}
	close(hold)

	var lost outcome
	select {
	case lost = <-loserDone:
	case <-time.After(30 * time.Second):
		t.Fatal("the loser's take never finished: a lost race must not spin")
	}

	if lost.err != nil {
		t.Fatalf("the loser's take ended in an error rather than a lost race: %v", lost.err)
	}
	if lost.taken {
		t.Error("both orchestrators hold the claim on one plan task")
	}

	// The loser's take saw an empty claim node — so it really did try to write —
	// and the store refused it. That refusal is the fence, and it is one write:
	// an orchestrator that lost the race re-reads and re-decides.
	if loserDB.puts != 1 {
		t.Errorf("the loser wrote %d time(s); a lost race is one refused write and a re-read", loserDB.puts)
	}
	if len(loserDB.errs) != 1 || !errors.Is(loserDB.errs[0], rtdb.ErrPrecondition) {
		t.Errorf("the loser's write was refused with %v, not the store's precondition error", loserDB.errs)
	}
	if !loserDB.absent {
		t.Error("the loser did not read an empty claim node, so the race this test stages was not the race it ran")
	}

	// The node carries the winner's own claim and nothing of the loser's.
	holder, at := cg4Held(t, f, path)
	if holder != cg4First {
		t.Errorf("the claim names %q after the race, not the winner: both writes landed", holder)
	}
	if at != now.Format(time.RFC3339) {
		t.Errorf("the claim is stamped %q, not the server time it was taken at (%s)", at, now.Format(time.RFC3339))
	}

	// A claim another orchestrator holds is skipped: the loser's next take is
	// refused by the node, not by the ETag, and it writes nothing at all.
	again, err := loser.Take(ctx, cg4Ref(t, cg4Rival))
	if err != nil {
		t.Fatalf("the loser's second take: %v", err)
	}
	if again {
		t.Error("the loser took the claim of an item another orchestrator holds")
	}
	if loserDB.puts != 1 {
		t.Errorf("the loser wrote %d time(s) in total; another orchestrator's claim is never written over", loserDB.puts)
	}

	// An orchestrator that already holds the item is given it back without a
	// write: a claim is kept alive by its holder's own beat, not by rewriting
	// its node, so a daemon that asks for its own claim every cycle moves no
	// ETag the project's other readers would have to re-read.
	mine, err := winner.Take(ctx, cg4Ref(t, cg4First))
	if err != nil {
		t.Fatalf("the winner's second take: %v", err)
	}
	if !mine {
		t.Error("the holder of a claim was not given its own claim back")
	}
	if winnerDB.puts != 1 {
		t.Errorf("the winner wrote %d time(s); its own claim is kept alive by its own beat", winnerDB.puts)
	}
	if holder, _ := cg4Held(t, f, path); holder != cg4First {
		t.Errorf("the claim names %q after the holder asked for it again", holder)
	}
}

// TestLaunchSkipsClaimed is the fence where it matters: an orchestrator that
// finds the item claimed starts nothing for it, and one that finds it free
// starts the run it planned.
//
// The plan is the same in both halves — one approved plan item, one launch —
// so what changes between them is the claim alone, and the launch that does
// happen proves the skip was the claim rather than a plan that held nothing.
// The rival has no lease node at all, which is the clean exit this rule exists
// for: a daemon that stopped driving while its run is still going keeps the
// item, and the claim comes off only once the item is finished.
//
// Mutation: have Starter.claim answer that every claim is this daemon's (or
// drop the call) and the second half's run is started while another
// orchestrator holds the item.
func TestLaunchSkipsClaimed(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	now := o6Now()
	approvedAt := now.Add(-time.Minute)

	item := cg4PlanItem("wi-1", approvedAt)
	s := o6Node(t, now, item)
	s.AgentID = cg4First

	actions := orchestrator.Plan(s, orchestrator.RunView{}, o6Caps(), now)
	a := onlyAction(t, actions)
	if a.Kind != orchestrator.ActionLaunch || a.ItemID != item.ID {
		t.Fatalf("the plan holds %s for %q, not the launch of the item", a.Kind, a.ItemID)
	}

	// The rival took the item before this daemon planned it: the item is the
	// rival's work and not this daemon's.
	rival := cg4Claim(t, rtdbClient(t, f), cg4Rival, clockAt(now))
	if took, err := rival.Take(ctx, orchestrator.ClaimRef(cg4Rival, item)); err != nil || !took {
		t.Fatalf("the rival's take: took=%v, err=%v", took, err)
	}

	l := &o6Launcher{}
	m := &o6Mint{t: t, runs: []string{"20261107-120000-aaaa"}}
	commits := 0
	d := orchestrator.Starter{
		Launcher: l,
		Commit:   func(_ context.Context, _ orchestrator.State) error { commits++; return nil },
		Mint:     m.mint,
		Claims:   cg4Claim(t, rtdbClient(t, f), cg4First, clockAt(now)),
	}
	next, err := d.Start(ctx, s, a, now)
	if !errors.Is(err, orchestrator.ErrItemClaimed) {
		t.Fatalf("the launch of a claimed item ended in %v, not ErrItemClaimed", err)
	}
	if next.Rev != s.Rev || len(next.Intents) != 0 {
		t.Errorf("the skipped launch left %+v, and a claim held elsewhere is nothing this daemon's state records", next)
	}
	if len(l.asked) != 0 || commits != 0 || m.calls != 0 {
		t.Errorf("a claimed item was asked of the launcher %d time(s), committed %d time(s) and minted %d run id(s)", len(l.asked), commits, m.calls)
	}
	if holder, _ := cg4Held(t, f, cg4Path(t, cg4First)); holder != cg4Rival {
		t.Errorf("the claim names %q after a daemon skipped it, not the rival", holder)
	}

	// The rival finishes the item and releases its claim — the release a
	// cycle's own read of the node makes (Refresh) — and then the same plan and
	// the same daemon launch.
	settled := map[string]bool{cg4PathOf(t, orchestrator.ClaimRef(cg4Rival, item)): true}
	if err := rival.Refresh(ctx, settled); err != nil {
		t.Fatalf("the rival's release: %v", err)
	}
	if v := f.Value(cg4Path(t, cg4Rival)); v != nil {
		t.Fatalf("the rival's claim is still on the node as %#v after the item finished", v)
	}
	if _, err := d.Start(ctx, s, a, now); err != nil {
		t.Fatalf("the launch of an item no orchestrator holds: %v", err)
	}
	if len(l.asked) != 1 || m.calls != 1 || commits != 1 {
		t.Fatalf("the free item was asked of the launcher %d time(s), minted %d run id(s) and committed %d time(s)", len(l.asked), m.calls, commits)
	}
	if holder, _ := cg4Held(t, f, cg4Path(t, cg4First)); holder != cg4First {
		t.Errorf("the claim names %q after this daemon started the item", holder)
	}
}

// TestOnePlanTaskOneLaunchAcrossTwoTicks is the claim at the level a daemon
// really meets it: two orchestrators of one project each run a cycle over their
// own copy of one imported plan, and one plan task is one run between them.
//
// It is the whole path and not the take alone: the second cycle reads its own
// node, plans the same launch from it and hands it to its own starter, and the
// claim the first cycle left standing is what stops it there — the second cycle
// starts no run, mints no run id and carries nothing, and the item in its own
// node stands exactly where it did. The first cycle's run proves the skip was
// the claim rather than a plan that held nothing.
//
// Mutation (run, restored): take `Claims: c.tick.Claims` out of the starter the
// cycle builds (tick.go, carry) and both cycles start a run for one plan task,
// which is the two pull requests for one piece of work D3209 exists to stop.
// Nothing else in the cycle fences it: the two orchestrators hold two nodes and
// two revisions, so their states' own compare-and-swaps never meet.
func TestOnePlanTaskOneLaunchAcrossTwoTicks(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Minute)
	item := cg4PlanItem("wi-1", approvedAt)

	f := gcpfake.NewRTDB(t)
	first := cg4OrchestratorAt(t, f, cg4First, "demo", now, item, "20261107-120000-aaaa")
	second := cg4OrchestratorAt(t, f, cg4Second, "demo", now, item, "20261107-120000-bbbb")

	if rep := first.step(); rep.Carried != 1 {
		t.Fatalf("the first cycle carried %d action(s) for the plan's one approved item", rep.Carried)
	}
	if got := first.launcher.started; len(got) != 1 {
		t.Fatalf("the first cycle started %v, want the one run for the plan task", got)
	}

	rep := second.step()
	if rep.Carried != 0 {
		t.Fatalf("the second cycle carried %d action(s) for the plan task the first orchestration holds: one claim is one run for one plan task, and this is the second (D3209)", rep.Carried)
	}
	if asked := second.launcher.asked; len(asked) != 0 {
		t.Errorf("the second cycle asked its launcher to start %v for an item another orchestration holds", asked)
	}
	if second.mint.calls != 0 {
		t.Errorf("the second cycle minted %d run id(s) for an item it does not hold", second.mint.calls)
	}
	if holder, _ := cg4Held(t, f, cg4Path(t, cg4First)); holder != cg4First {
		t.Errorf("the claim names %q after both cycles, not the orchestration that took it", holder)
	}
	if s := second.planState(); len(s.Intents) != 0 {
		t.Errorf("the second cycle's node records %d attempt(s), and an item held elsewhere is nothing this node writes", len(s.Intents))
	} else if w, ok := s.Item(item.ID); !ok || w.Phase != orchestrator.PhaseQueued {
		t.Errorf("the second cycle's item reads back as %+v, and an item another orchestration holds is left where it stands", w)
	}
}

// TestCycleReleasesTheClaimOfAnItemThatFinished: the release is the cycle's own
// read of its nodes and not a step that must follow the run's own report, which
// is what makes it survive the two ways a release is missed — a cycle that died
// after the write that finished the item had landed, and a release whose own
// write was refused by a moved ETag. Either leaves a claim standing on an item
// nobody is on, and the first cycle that reads the node afterwards takes it off
// (Refresh, settledClaimPaths).
func TestCycleReleasesTheClaimOfAnItemThatFinished(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Minute)
	item := cg4PlanItem("wi-1", approvedAt)
	const runID = "20261107-120000-aaaa"

	f := gcpfake.NewRTDB(t)
	o := cg4OrchestratorAt(t, f, cg4First, "demo", now, item, runID)
	if rep := o.step(); rep.Carried != 1 {
		t.Fatalf("the first cycle carried %d action(s) for the plan's one approved item", rep.Carried)
	}
	if v := f.Value(cg4Path(t, cg4First)); v == nil {
		t.Fatal("the claim node is empty after the launch it was taken for")
	}

	// The run finished, and the cycle that reads that report folds the item done
	// and releases the claim on the same pass: the item is the only thing that
	// releases a claim, and nothing has to have moved for this cycle to release
	// it.
	o.reader.reports = []orchestrator.RunReport{{RunID: runID, Started: true, Finished: true, Outcome: orchestrator.OutcomeSucceeded}}
	if rep := o.step(); rep.Folded != 1 {
		t.Fatalf("the cycle folded %d finished runs where the item's own reported", rep.Folded)
	}
	if w, ok := o.planState().Item(item.ID); !ok || w.Phase != orchestrator.PhaseDone {
		t.Fatalf("the item reads back as %+v after its run reported", w)
	}
	if v := f.Value(cg4Path(t, cg4First)); v != nil {
		t.Errorf("the claim is still on the node as %#v after the item finished: a plan task is released when its item is done or cancelled and at no other time (D3209)", v)
	}
}

// cg4Orchestrator is one orchestrator of one project at a cycle: the state node
// and the plan's node beside it, both its own, and the claims, launcher and
// mint its cycle acts through. Two of these over one database are the two
// orchestrators CG4 is about, and neither shares a node with the other.
type cg4Orchestrator struct {
	t        *testing.T
	tick     *orchestrator.Tick
	launcher *o6Launcher
	mint     *o6Mint
	reader   *tickReader
	plan     *orchestrator.Node
}

// cg4OrchestratorAt builds one orchestrator of the test's project over the
// fake: its state node, the plan's node, and one tick over the two. Its claims
// are its own, the way a daemon's are (the daemon builds one guard and hands it
// to the tick), so a test can read the node a cycle that skips an item was
// refused by.
func cg4OrchestratorAt(t *testing.T, f *gcpfake.RTDB, agentID, plan string, now time.Time, item orchestrator.WorkItem, run string) *cg4Orchestrator {
	t.Helper()
	db := rtdbClient(t, f)
	o := &cg4Orchestrator{
		t:        t,
		launcher: &o6Launcher{},
		mint:     &o6Mint{t: t, runs: []string{run}},
		reader:   &tickReader{},
	}
	state := cg4SeedNode(t, db, agentID, orchestrator.ShardState, o6Node(t, now))
	o.plan = cg4SeedNode(t, db, agentID, orchestrator.PlanShard(plan), o6Node(t, now, item))
	o.tick = &orchestrator.Tick{
		Store:    state,
		Nodes:    []orchestrator.Store{o.plan},
		Runs:     o.reader,
		Launcher: o.launcher,
		Mint:     o.mint.mint,
		Caps:     o6Caps(),
		Claims:   cg4Claim(t, db, agentID, clockAt(now)),
		Now:      clockAt(now),
		Notifier: &tickSink{},
	}
	return o
}

// cg4SeedNode builds one node of an orchestrator id and writes the test's state
// into it. The state carries the id whose node it is, which is what the node's
// own save holds it to (owns).
func cg4SeedNode(t *testing.T, db orchestrator.CAS, agentID string, shard orchestrator.Shard, s orchestrator.State) *orchestrator.Node {
	t.Helper()
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{DB: db, AgentID: agentID, Role: orchestrator.RoleOrchestrator, Shard: shard})
	if err != nil {
		t.Fatalf("building the %q node of %q: %v", shard, agentID, err)
	}
	s.AgentID = agentID
	_, etag, err := node.Load(context.Background())
	if err != nil {
		t.Fatalf("reading the empty %q node of %q: %v", shard, agentID, err)
	}
	if err := node.Save(context.Background(), s, etag); err != nil {
		t.Fatalf("seeding the %q node of %q: %v", shard, agentID, err)
	}
	return node
}

// step runs one cycle of this orchestrator. A cycle these tests did not set up
// to fail is one that failed for a reason the test is about to name.
func (o *cg4Orchestrator) step() orchestrator.Report {
	o.t.Helper()
	rep, err := o.tick.Step(context.Background())
	if err != nil {
		o.t.Fatalf("the cycle ended as %v", err)
	}
	return rep
}

// planState is the plan's node as it now stands, read back through its own
// store rather than remembered from what the cycle returned.
func (o *cg4Orchestrator) planState() orchestrator.State {
	o.t.Helper()
	s, _, err := o.plan.Load(context.Background())
	if err != nil {
		o.t.Fatalf("reading the plan's node back: %v", err)
	}
	return s
}

// cg4PlanItem is a work item standing for one node of a plan: the declaration
// the O6 helpers give, with the plan task two orchestrators of a project name
// alike, approved as it now stands.
func cg4PlanItem(id string, approvedAt time.Time) orchestrator.WorkItem {
	w := workItemOf(id)
	w.PlanTask = cg4PlanTask
	return o6ApprovedBy(w, approvedAt)
}

// TestClaimHeldWhileTheItemIsUnfinished is the other half of a claim's life:
// nothing about its holder's lease frees it, and no stamp on the node is stale
// enough to hand the item to a rival — a plan task is released when its item is
// done or cancelled and at no other time (the owner's ruling of 2026-11-23).
//
// Each case is one claim another orchestrator holds, read against the same
// server clock, with the lease node the case names: nothing, a beat a moment
// ago, a beat an hour stale, a beat the server could not have written yet, and a
// node this release cannot read at all. In every one of them the take answers
// false and writes nothing, which is the whole of the rule — a closed laptop
// lid, a clean `--once` exit and a daemon gone for good all leave the item with
// the orchestrator that holds it.
//
// Mutation: answer the take from the holder's lease — free the claim when the
// beat has lapsed, or when there is no lease node at all — and every case below
// takes an item whose run is still going, which is how two orchestrators end up
// starting two runs for one plan task.
func TestClaimHeldWhileTheItemIsUnfinished(t *testing.T) {
	ctx := context.Background()
	now := o6Now()

	for _, tc := range []struct {
		name string
		// lease is the holder's lease node as the case stages it: a beat, a
		// value that is not a lease, or nothing at all.
		lease any
	}{
		{name: "a beat inside the ttl", lease: map[string]any{"holder": cg4Beat, "at": now.Add(-orchestrator.LeaseTTL / 2).UTC().Format(time.RFC3339)}},
		{name: "a beat lapsed past the ttl", lease: map[string]any{"holder": cg4Beat, "at": now.Add(-orchestrator.LeaseTTL - time.Second).UTC().Format(time.RFC3339)}},
		{name: "a beat lapsed by an hour", lease: map[string]any{"holder": cg4Beat, "at": now.Add(-time.Hour).UTC().Format(time.RFC3339)}},
		{name: "a beat the server could not have written yet", lease: map[string]any{"holder": cg4Beat, "at": now.Add(orchestrator.LeaseSkew + time.Second).UTC().Format(time.RFC3339)}},
		{name: "a lease node this release cannot read", lease: "not a lease"},
		// The clean exit: the daemon deleted its lease node on the way out
		// (Lease.Release) while the runs it started are still in flight. The
		// item is still its own, and the claim's own stamp — an hour old here,
		// because a daemon that is gone never re-stamps it — decides nothing.
		{name: "no lease node at all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			path := cg4Path(t, cg4First)
			claimAt := now.Add(-time.Hour)
			f.Set(path, map[string]any{"holder": cg4Rival, "at": claimAt.UTC().Format(time.RFC3339)})
			if tc.lease != nil {
				leasePath := orchestrator.SubtreeRoot + "/" + budget.Key(cg4Rival) + "/" + orchestrator.LeaseNode
				f.Set(leasePath, tc.lease)
			}
			before := f.Value(path)

			db := &countingCAS{inner: rtdbClient(t, f)}
			claims := cg4Claim(t, db, cg4First, clockAt(now))

			taken, err := claims.Take(ctx, cg4Ref(t, cg4First))
			if err != nil {
				t.Fatalf("the take over a held claim: %v", err)
			}
			if taken {
				t.Fatal("the take took an item whose holder's lease says nothing about whether its run is going")
			}
			if db.puts != 0 {
				t.Errorf("another orchestrator's claim was written %d time(s)", db.puts)
			}
			if got := f.Value(path); !reflect.DeepEqual(got, before) {
				t.Errorf("the claim node now carries %#v, and a claim that is not this daemon's is left as it stands", got)
			}
			if holder, at := cg4Held(t, f, path); holder != cg4Rival || at != claimAt.UTC().Format(time.RFC3339) {
				t.Errorf("the claim now names %q at %q, not the holder that took it", holder, at)
			}
		})
	}
}

// cg4NoStrayKeys refuses the listing of any subtree but the claims root and
// this orchestrator's own items: what a refresh lists is exactly what it may
// hold (Refresh), so a walk into a rival's own items fails here rather than
// reading them.
type cg4NoStrayKeys struct {
	*countingCAS
	own string
}

func (k cg4NoStrayKeys) Keys(ctx context.Context, path string) ([]string, error) {
	if path != orchestrator.ClaimsRoot && path != orchestrator.ClaimsRoot+"/"+k.own {
		return nil, fmt.Errorf("the refresh listed %q, which is not this orchestrator's subtree", path)
	}
	return k.countingCAS.Keys(ctx, path)
}

// TestClaimRefresh is the holder's own half of a claim's life: the node records
// the server time its holder last confirmed the item was still its own, and the
// beat is what re-writes that stamp (Refresh with nothing settled).
//
// What it re-stamps is only its own. A claim a rival holds is left alone (a
// refresh is not a takeover), a claim node this release cannot read is left for
// Take to refuse rather than repaired by a beat, and another orchestrator's own
// items are not even listed.
//
// Mutation: re-stamp whatever the listing turns up and the rival's claim below
// comes back naming this orchestrator — an item taken by a daemon that only
// meant to keep its own — and walk every key of the claims node and the
// listing of the rival's subtree is refused here.
func TestClaimRefresh(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	now := o6Now()
	later := now.Add(time.Minute)

	mine := cg4PlanItem("wi-1", now.Add(-time.Hour))
	myItem := workItemOf("wi-2")
	rivalPlan := cg4PlanItem("wi-3", now.Add(-time.Hour))
	rivalPlan.PlanTask = "demo/B"
	rivalItem := workItemOf("wi-2")
	unreadable := cg4PlanItem("wi-4", now.Add(-time.Hour))
	unreadable.PlanTask = "demo/C"

	rival := cg4Claim(t, rtdbClient(t, f), cg4Rival, clockAt(now))
	for _, ref := range []orchestrator.ItemRef{
		orchestrator.ClaimRef(cg4Rival, rivalPlan),
		orchestrator.ClaimRef(cg4Rival, rivalItem),
	} {
		if took, err := rival.Take(ctx, ref); err != nil || !took {
			t.Fatalf("the rival's take of %q: took=%v, err=%v", ref.Item, took, err)
		}
	}

	db := &cg4NoStrayKeys{countingCAS: &countingCAS{inner: rtdbClient(t, f)}, own: cg4First}
	claims := cg4Claim(t, db, cg4First, clockAt(now))
	for _, ref := range []orchestrator.ItemRef{
		orchestrator.ClaimRef(cg4First, mine),
		orchestrator.ClaimRef(cg4First, myItem),
	} {
		if took, err := claims.Take(ctx, ref); err != nil || !took {
			t.Fatalf("the take of %q: took=%v, err=%v", ref.Item, took, err)
		}
	}

	// A claim node this release cannot read: the refresh passes over it, and
	// Take is what refuses it.
	brokenPath := cg4PathOf(t, orchestrator.ClaimRef(cg4First, unreadable))
	f.Set(brokenPath, "not a claim")
	broken := f.Value(brokenPath)
	rivalPlanPath := cg4PathOf(t, orchestrator.ClaimRef(cg4Rival, rivalPlan))
	rivalItemPath := cg4PathOf(t, orchestrator.ClaimRef(cg4Rival, rivalItem))

	// The refresh runs on a daemon whose clock has moved on, and with nothing
	// settled: what it writes is the server time of the beat.
	beat := cg4Claim(t, db, cg4First, clockAt(later))
	writes := db.puts
	if err := beat.Refresh(ctx, nil); err != nil {
		t.Fatalf("the refresh of the claims this daemon holds: %v", err)
	}
	if got := db.puts - writes; got != 2 {
		t.Errorf("the refresh wrote %d claim node(s), and it holds two", got)
	}
	for _, tc := range []struct {
		what string
		ref  orchestrator.ItemRef
		by   string
	}{
		{"the plan task it claimed", orchestrator.ClaimRef(cg4First, mine), cg4First},
		{"its own backlog item", orchestrator.ClaimRef(cg4First, myItem), cg4First},
	} {
		path := cg4PathOf(t, tc.ref)
		holder, at := cg4Held(t, f, path)
		if holder != tc.by {
			t.Errorf("%s is claimed by %q after the refresh, not %q", tc.what, holder, tc.by)
		}
		if at != later.UTC().Format(time.RFC3339) {
			t.Errorf("%s is stamped %q after the refresh, not the beat's own server time (%s): the node records when its holder last confirmed the item was still its own", tc.what, at, later.UTC().Format(time.RFC3339))
		}
	}
	for _, tc := range []struct {
		what string
		path string
	}{
		{"the plan task a rival claimed", rivalPlanPath},
		{"the backlog item a rival claimed", rivalItemPath},
	} {
		holder, at := cg4Held(t, f, tc.path)
		if holder != cg4Rival || at != now.UTC().Format(time.RFC3339) {
			t.Errorf("%s now names %q at %q, and a refresh re-stamps only its own", tc.what, holder, at)
		}
	}
	if got := f.Value(brokenPath); !reflect.DeepEqual(got, broken) {
		t.Errorf("the unreadable claim node now carries %#v, and a refresh neither reads past it nor repairs it", got)
	}
}

// cg4PathOf is one item's claim node, through the package's own ClaimPath: a
// path a test spelled itself would pass on a node the package never reads.
func cg4PathOf(t *testing.T, ref orchestrator.ItemRef) string {
	t.Helper()
	path, err := orchestrator.ClaimPath(ref)
	if err != nil {
		t.Fatalf("building the claim path of %q: %v", ref.Item, err)
	}
	return path
}

// TestClaimReleasedWhenTheItemSettles is the claim coming off the item that is
// finished: a plan task or a backlog item that is done or cancelled is work
// nobody is on, and the claim on it is what would keep another orchestrator from
// taking the item the next time one of them has it in front of them.
//
// The release is the cycle's own read of the nodes (Refresh with what they
// say), so a node that finished its work in an earlier cycle releases the claim
// on its next read rather than on the write that finished it: a cycle that died
// between the two, or a release the server refused, is retried — and the item
// is the only thing that releases a claim (the owner's ruling of 2026-11-23).
//
// Mutation: release only what the caller says moved, and the second half below
// leaves the claim of an item that settled before the read standing for ever.
func TestClaimReleasedWhenTheItemSettles(t *testing.T) {
	ctx := context.Background()
	now := o6Now()

	// Both phases that finish an item: a task whose run landed its work and one
	// a person cancelled are the same thing to the claim — work nobody is on.
	for _, phase := range []orchestrator.Phase{orchestrator.PhaseDone, orchestrator.PhaseCancelled} {
		t.Run(string(phase), func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			item := workItemOf("wi-1")
			item.PlanTask = cg4PlanTask
			item.Phase = orchestrator.PhaseRunning
			item.RunID = "20261107-120000-aaaa"
			item.Attempts = 1
			s := o6Node(t, now, item)
			s.AgentID = cg4First

			claims := cg4Claim(t, rtdbClient(t, f), cg4First, clockAt(now))
			ref := orchestrator.ClaimRef(cg4First, item)
			if took, err := claims.Take(ctx, ref); err != nil || !took {
				t.Fatalf("the take before the item settles: took=%v, err=%v", took, err)
			}
			path, err := orchestrator.ClaimPath(ref)
			if err != nil {
				t.Fatalf("building the claim path: %v", err)
			}

			// What a cycle reads at its own last step: the claim path of every
			// item its nodes carry finished.
			settledPhase := phase
			after := o6Apply(t, s, orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{ID: item.ID, Phase: &settledPhase}}, orchestrator.Actor{Class: orchestrator.Daemon, Now: now})
			settled := map[string]bool{}
			for _, w := range after.WorkItems {
				if !w.Phase.Done() {
					continue
				}
				p, err := orchestrator.ClaimPath(orchestrator.ClaimRef(after.AgentID, w))
				if err != nil {
					t.Fatalf("building the claim path of %q: %v", w.ID, err)
				}
				settled[p] = true
			}
			if !settled[path] {
				t.Fatalf("the node's %s item is claimed at %v, and the cycle's own reading does not name it", phase, settled)
			}

			if err := claims.Refresh(ctx, settled); err != nil {
				t.Fatalf("releasing the claims of what settled: %v", err)
			}
			if v := f.Value(path); v != nil {
				t.Errorf("the claim is still on the node as %#v after the item settled", v)
			}

			// The same read again releases nothing and fails nothing: the node is
			// gone, which is the state a release that already landed left behind.
			if err := claims.Refresh(ctx, settled); err != nil {
				t.Fatalf("the second release pass: %v", err)
			}
			if v := f.Value(path); v != nil {
				t.Errorf("the claim came back as %#v on a second pass over the same finished item", v)
			}

			// And a claim taken on an item that settled before the read is released
			// by it: the item is finished whatever cycle wrote it, so the claim of
			// a holder that died after the write landed does not stand for ever.
			if took, err := claims.Take(ctx, ref); err != nil || !took {
				t.Fatalf("the take of a settled item: took=%v, err=%v", took, err)
			}
			if err := claims.Refresh(ctx, settled); err != nil {
				t.Fatalf("the pass over a node whose item had already settled when the cycle read it: %v", err)
			}
			if v := f.Value(path); v != nil {
				t.Errorf("the claim is still on the node as %#v, and the item was finished before the read that releases it", v)
			}
		})
	}
}

// TestClaimFailsClosedOnWhatItCannotRead is the state node's own rule on the
// claim: a node this release cannot read is a refusal, never a claim of nobody.
// Reading a broken claim as free is exactly how two orchestrators end up on one
// plan task — and with the claim's release owned by the item rather than by the
// holder's lease (the owner's ruling of 2026-11-23), an unreadable node is the
// only reading left that could be answered the wrong way: a claim stands until
// its item finishes, whatever its holder's lease node says.
//
// Mutation: treat an undecodable claim node as absent, and every case below
// takes an item that may be another orchestrator's.
func TestClaimFailsClosedOnWhatItCannotRead(t *testing.T) {
	ctx := context.Background()
	now := o6Now()

	for _, tc := range []struct {
		name string
		doc  any
	}{
		{"a holder that is not an orchestrator id", map[string]any{"holder": "", "at": now.Format(time.RFC3339)}},
		{"a field this release does not model", map[string]any{"holder": cg4Rival, "at": now.Format(time.RFC3339), "boot": "x"}},
		{"a value that is not a document", "not a claim"},
	} {
		t.Run("the claim node: "+tc.name, func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			path := cg4Path(t, cg4First)
			f.Set(path, tc.doc)
			before := f.Value(path)
			db := &countingCAS{inner: rtdbClient(t, f)}

			taken, err := cg4Claim(t, db, cg4First, clockAt(now)).Take(ctx, cg4Ref(t, cg4First))
			if err == nil {
				t.Fatal("an unreadable claim node was read as a free item")
			}
			if taken {
				t.Error("an unreadable claim node was taken")
			}
			if db.puts != 0 {
				t.Errorf("an unreadable claim node was written %d time(s)", db.puts)
			}
			if got := f.Value(path); !reflect.DeepEqual(got, before) {
				t.Errorf("the unreadable claim node now carries %#v, and a refusal changes nothing", got)
			}
		})
	}

	// The claim node itself is the only node the take reads: a holder's lease
	// node beside a readable claim changes nothing, whatever it carries.
	for _, tc := range []struct {
		name string
		doc  any
	}{
		{"a lease naming no holder", map[string]any{"holder": "", "at": now.Format(time.RFC3339)}},
		{"a lease field this release does not model", map[string]any{"holder": cg4Beat, "at": now.Format(time.RFC3339), "boot": "x"}},
		{"a value that is not a lease", "not a lease"},
	} {
		t.Run("the holder's lease: "+tc.name, func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			path := cg4Path(t, cg4First)
			f.Set(path, map[string]any{"holder": cg4Rival, "at": now.Add(-time.Hour).UTC().Format(time.RFC3339)})
			leasePath := orchestrator.SubtreeRoot + "/" + budget.Key(cg4Rival) + "/" + orchestrator.LeaseNode
			f.Set(leasePath, tc.doc)
			db := &countingCAS{inner: rtdbClient(t, f)}

			taken, err := cg4Claim(t, db, cg4First, clockAt(now)).Take(ctx, cg4Ref(t, cg4First))
			if err != nil {
				t.Fatalf("the take over a holder whose lease cannot be read: %v", err)
			}
			if taken {
				t.Error("an item was taken from a holder, and the claim's release is its item's and not its lease's")
			}
			if db.puts != 0 {
				t.Errorf("the claim of another orchestrator was written %d time(s)", db.puts)
			}
			if holder, _ := cg4Held(t, f, path); holder != cg4Rival {
				t.Errorf("the claim names %q, and another orchestrator's claim is never written over", holder)
			}
		})
	}
}
