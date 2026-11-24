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

// cg4Claim is one orchestrator's claims over the fake, with the clock the test
// set: nothing here reads a real one.
func cg4Claim(t *testing.T, db orchestrator.CAS, agentID string, now orchestrator.ServerNow) *orchestrator.Claim {
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
// The plan is the same in all three steps — one approved plan item, one launch —
// so what changes between them is the claim alone, and the launch that does
// happen proves the skip was the claim rather than a plan that held nothing. The
// rival has no lease node at all, which is the clean exit this rule exists for:
// a daemon that stopped driving while its run is still going keeps the item, and
// nothing about its lease frees it (the owner's ruling of 2026-11-23).
//
// The middle step is the second run the claim exists to stop, and it is the
// reason a finished plan task's claim is recorded rather than deleted: the rival
// finishes the item, and this daemon — whose own node still carries the task
// approved — is refused exactly as it was while the work was going (D3209).
//
// Mutation: have Starter.claim answer that every claim is this daemon's (or drop
// the call) and the second step's run is started while another orchestrator
// holds the item.
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

	// The rival finishes the item, and the cycle's own read of its nodes names
	// it finished (HeldClaims), which is what Refresh settles its claim from:
	// the node is marked finished and stands, so this daemon — which has no way
	// to learn that the task the rival ran is done — is refused again rather
	// than starting the second run of it.
	held := []orchestrator.Held{{Ref: orchestrator.ClaimRef(cg4Rival, item), Finished: true}}
	if err := rival.Refresh(ctx, held); err != nil {
		t.Fatalf("the rival's settlement of its own finished item: %v", err)
	}
	doc := nodeObject(t, f.Value(cg4Path(t, cg4Rival)), "the claim")
	if fin, _ := doc["finished"].(bool); !fin {
		t.Fatalf("the claim node is %#v after the item finished, and the finish is what keeps the task from being run twice", doc)
	}
	if _, err := d.Start(ctx, s, a, now); !errors.Is(err, orchestrator.ErrItemClaimed) {
		t.Fatalf("the launch of a plan task another orchestrator has finished ended in %v, not ErrItemClaimed", err)
	}
	if len(l.asked) != 0 || commits != 0 || m.calls != 0 {
		t.Errorf("a finished plan task was asked of the launcher %d time(s), committed %d time(s) and minted %d run id(s)", len(l.asked), commits, m.calls)
	}

	// And with no claim standing at all — an item nobody has taken — the same
	// plan and the same daemon start the run they planned, which is the half
	// that proves the claim is what the two other steps were refused by.
	f.Set(cg4Path(t, cg4Rival), nil)
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

// TestAClaimSkipIsInFlightNotEmpty is how the skip reads for the breaker. Two
// orchestrators of one project is the deployment the claim exists for
// (D3206/D3207), and there the loser of a claim skips every action the winner
// holds. A cycle whose only work was skipped is waiting on that other
// orchestrator, not one that achieved nothing, and it has to read that way: read
// as empty, three in a row reach the breaker's threshold and pause the node,
// which refuses this daemon's own unclaimed items too — the pause the finding on
// this cycle named (D1228, D3209).
//
// The skip is counted, and the count is what the outcome is read from: the
// loser's own node never moved, so nothing else in the cycle says the work is
// going anywhere.
//
// Mutation (run, restored): take `c.rep.Skipped++` out of carry's ErrItemClaimed
// branch and the cycle reads CycleEmpty, so the third of them trips the breaker
// and this test fails on it.
func TestAClaimSkipIsInFlightNotEmpty(t *testing.T) {
	now := o6Now()
	item := cg4PlanItem("wi-1", now.Add(-time.Minute))

	f := gcpfake.NewRTDB(t)
	first := cg4OrchestratorAt(t, f, cg4First, "demo", now, item, "20261107-120000-aaaa")
	second := cg4OrchestratorAt(t, f, cg4Second, "demo", now, item, "20261107-120000-bbbb")
	if rep := first.step(); rep.Carried != 1 {
		t.Fatalf("the first cycle carried %d action(s) for the plan's one approved item", rep.Carried)
	}

	var breaker orchestrator.Breaker
	for i := 1; i <= orchestrator.BreakerThreshold; i++ {
		rep := second.step()
		if rep.Skipped != 1 {
			t.Fatalf("the loser's cycle %d reports %d skipped action(s) for the one plan task the other orchestration holds", i, rep.Skipped)
		}
		if rep.Outcome != orchestrator.CycleInFlight {
			t.Fatalf("the loser's cycle %d reads as %s, and a cycle whose work is going on another orchestration is in flight rather than empty (D3209)", i, rep.Outcome)
		}
		if breaker.Observe(rep.Outcome) {
			t.Fatalf("cycle %d tripped the breaker: %d cycles in a row spent skipping what another orchestration holds is the two-orchestrator deployment working, and the pause would refuse this daemon's own unclaimed items too (D1228)", i, orchestrator.BreakerThreshold)
		}
	}
}

// TestAClaimSkipLeavesTheCycleItsRoom is what a skip costs the cycle that made
// it: nothing. An item another orchestrator holds is work this daemon was not
// going to start, so the room the plan gave it goes back to the cycle and the
// node is planned again for whatever that room reaches — the item the plan had
// no allowance left for is started in this same cycle.
//
// The case is the one a two-orchestrator project lives in. A finished plan
// task's claim stands for good (this file's header) and the rival's own copy of
// that task reads approved for good, because nothing syncs the two nodes: a
// project whose list starts with tasks another orchestrator holds would spend
// every cycle's whole allowance on skips, never plan past them and never start
// the work that is its own — with `Skipped > 0` reading as in flight, so the
// breaker never says anything about it either (outcome). Here the allowance is
// exactly the items held elsewhere, so a cycle that spent it on skips starts
// nothing at all.
//
// Mutation (run, restored): leave the tally alone on a skip (drop `c.planned--`)
// and the item that is this daemon's own is never planned, so this cycle
// carries nothing and the item stands queued.
func TestAClaimSkipLeavesTheCycleItsRoom(t *testing.T) {
	ctx := context.Background()
	now := o6Now()
	approvedAt := now.Add(-time.Minute)

	// The plan node's own order, which is the order the allowance is spent in:
	// the two tasks the rival holds, then the one this orchestrator may start.
	items := []orchestrator.WorkItem{
		cg4Task("wi-1", "demo/A", approvedAt),
		cg4Task("wi-2", "demo/B", approvedAt),
		cg4Task("wi-3", "demo/C", approvedAt),
	}
	theirs, mine := items[:2], items[2]

	f := gcpfake.NewRTDB(t)
	o := cg4OrchestratorWith(t, f, cg4First, "demo", now, "20261107-120000-aaaa",
		withCaps(func(c *orchestrator.Caps) { c.MaxPerCycle = len(theirs) }), items...)

	// The rival took both tasks of the two it holds and finished them, which is
	// the claim that never comes off again: its node is not one this project
	// updates, so the hold is what this cycle meets every time.
	rival := cg4Claim(t, rtdbClient(t, f), cg4Rival, clockAt(now))
	held := make([]orchestrator.Held, 0, len(theirs))
	for _, w := range theirs {
		ref := orchestrator.ClaimRef(cg4Rival, w)
		if took, err := rival.Take(ctx, ref); err != nil || !took {
			t.Fatalf("the rival's take of %q: took=%v, err=%v", w.PlanTask, took, err)
		}
		held = append(held, orchestrator.Held{Ref: ref, Finished: true})
	}
	if err := rival.Refresh(ctx, held); err != nil {
		t.Fatalf("the rival's settlement of the tasks it finished: %v", err)
	}

	rep := o.step()
	if rep.Skipped != len(theirs) {
		t.Fatalf("the cycle skipped %d action(s) where %d of the node's items are held elsewhere", rep.Skipped, len(theirs))
	}
	if rep.Carried != 1 {
		t.Fatalf("the cycle carried %d action(s), and the room the %d skips gave back is what reaches the item that is this daemon's own (D3209)", rep.Carried, len(theirs))
	}
	if got := o.launcher.asked; len(got) != 1 {
		t.Fatalf("the cycle asked its launcher to start %v, want the one run for %q", got, mine.ID)
	}
	if o.mint.calls != 1 {
		t.Errorf("the cycle minted %d run id(s) for the one item it started", o.mint.calls)
	}

	s := o.planState()
	if w, ok := s.Item(mine.ID); !ok || w.Phase != orchestrator.PhaseRunning {
		t.Errorf("the item that is this daemon's own reads back as %+v, and the room a skip leaves is room the plan spends on it", w)
	}
	for _, w := range theirs {
		if got, ok := s.Item(w.ID); !ok || got.Phase != orchestrator.PhaseQueued {
			t.Errorf("the held item %q reads back as %+v, and an item another orchestrator holds is left exactly where it stands", w.ID, got)
		}
	}
	if holder, _ := cg4Held(t, f, cg4PathOf(t, orchestrator.ClaimRef(cg4First, mine))); holder != cg4First {
		t.Errorf("the claim on the item the cycle started names %q, not the orchestration that started it", holder)
	}
}

// TestAFinishedPlanTaskIsNotTakenByARival is the second run the claim exists to
// stop, arriving from the other side: the orchestrator that held the plan task
// finishes it, and the rival that skipped it still carries the task approved in
// its own node — which nothing updates, because the two orchestrators hold two
// nodes and the plan's own copy of the task is each node's own.
//
// So the finish must not free the item: the holder's cycle records it on the
// claim node the project shares (marking it, not deleting it), and the rival's
// next cycle skips the task exactly as it did while the work was going. Deleting
// the node instead is what the finding on this test reported: the rival, whose
// own copy reads approved, takes a path that is not there and starts a second run
// of one piece of work — two pull requests for one plan task (D3209).
//
// Mutation (run, restored): release a finished plan task the way a work item is
// released (delete its claim node) and the rival's last cycle carries the task the
// holder has finished, minting and starting a second run for it.
func TestAFinishedPlanTaskIsNotTakenByARival(t *testing.T) {
	now := o6Now()
	approvedAt := now.Add(-time.Minute)
	item := cg4PlanItem("wi-1", approvedAt)
	const runID = "20261107-120000-aaaa"

	f := gcpfake.NewRTDB(t)
	holder := cg4OrchestratorAt(t, f, cg4First, "demo", now, item, runID)
	rival := cg4OrchestratorAt(t, f, cg4Second, "demo", now, item, "20261107-120000-bbbb")

	if rep := holder.step(); rep.Carried != 1 {
		t.Fatalf("the first cycle carried %d action(s) for the plan's one approved item", rep.Carried)
	}
	if v := f.Value(cg4Path(t, cg4First)); v == nil {
		t.Fatal("the claim node is empty after the launch it was taken for")
	}
	if rep := rival.step(); rep.Carried != 0 || rep.Skipped != 1 {
		t.Fatalf("the rival's cycle carried %d action(s) and skipped %d where the holder is on the plan task", rep.Carried, rep.Skipped)
	}

	// The holder's run finished, and the cycle that reads that report folds the
	// item done and settles the claim on the same pass: the item is the only
	// thing that settles a claim, and nothing has to have moved for this cycle
	// to settle it.
	holder.reader.reports = []orchestrator.RunReport{{RunID: runID, Started: true, Finished: true, Outcome: orchestrator.OutcomeSucceeded}}
	if rep := holder.step(); rep.Folded != 1 {
		t.Fatalf("the cycle folded %d finished runs where the item's own reported", rep.Folded)
	}
	if w, ok := holder.planState().Item(item.ID); !ok || w.Phase != orchestrator.PhaseDone {
		t.Fatalf("the item reads back as %+v after its run reported", w)
	}
	doc := nodeObject(t, f.Value(cg4Path(t, cg4First)), "the claim")
	if fin, _ := doc["finished"].(bool); !fin {
		t.Fatalf("the claim node is %#v after the plan task finished, and the finish is recorded on the node rather than the node being taken away", doc)
	}

	// The rival's own node still carries the task approved, so this is the cycle
	// that would start the second run if the finish had freed the item: it asks
	// its launcher for nothing, mints nothing, and leaves its item queued.
	askedBefore := len(rival.launcher.asked)
	rep := rival.step()
	if rep.Carried != 0 || rep.Skipped != 1 || rep.Outcome != orchestrator.CycleInFlight {
		t.Fatalf("the rival's cycle after the plan task finished carried %d action(s), skipped %d and read as %s, and one plan task is one run for the whole project (D3209)", rep.Carried, rep.Skipped, rep.Outcome)
	}
	if len(rival.launcher.asked) != askedBefore {
		t.Errorf("the rival asked its launcher to start %v for a plan task the other orchestration has finished", rival.launcher.asked[askedBefore:])
	}
	if rival.mint.calls != 0 {
		t.Errorf("the rival minted %d run id(s) for a plan task the other orchestration has finished", rival.mint.calls)
	}
	if w, ok := rival.planState().Item(item.ID); !ok || w.Phase != orchestrator.PhaseQueued {
		t.Errorf("the rival's item reads back as %+v, and an item it does not hold is left where it stands", w)
	}
	if holder, _ := cg4Held(t, f, cg4Path(t, cg4First)); holder != cg4First {
		t.Errorf("the claim names %q after the finish, not the orchestration that finished the task", holder)
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
	return cg4OrchestratorWith(t, f, agentID, plan, now, run, o6Caps(), item)
}

// cg4OrchestratorWith is cg4OrchestratorAt over the items and the caps a test
// means: a plan node carries several tasks, and a test of what a cycle spends
// its allowance on has to set that allowance.
func cg4OrchestratorWith(t *testing.T, f *gcpfake.RTDB, agentID, plan string, now time.Time, run string, caps orchestrator.Caps, items ...orchestrator.WorkItem) *cg4Orchestrator {
	t.Helper()
	db := rtdbClient(t, f)
	o := &cg4Orchestrator{
		t:        t,
		launcher: &o6Launcher{},
		mint:     &o6Mint{t: t, runs: []string{run}},
		reader:   &tickReader{},
	}
	state := cg4SeedNode(t, db, agentID, orchestrator.ShardState, o6Node(t, now))
	o.plan = cg4SeedNode(t, db, agentID, orchestrator.PlanShard(plan), o6Node(t, now, items...))
	o.tick = &orchestrator.Tick{
		Store:    state,
		Nodes:    []orchestrator.Store{o.plan},
		Runs:     o.reader,
		Launcher: o.launcher,
		Mint:     o.mint.mint,
		Caps:     caps,
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
	return cg4Task(id, cg4PlanTask, approvedAt)
}

// cg4Task is that item for one named plan task, which is what a claim is keyed
// by (ClaimRef): a node carrying several tasks of one plan carries several
// claims.
func cg4Task(id, planTask string, approvedAt time.Time) orchestrator.WorkItem {
	w := workItemOf(id)
	w.PlanTask = planTask
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

// TestClaimRefresh is the holder's own half of a claim's life: the node records
// the server time its holder last confirmed the item was still its own, and the
// beat is what re-writes that stamp (Refresh over the items this daemon's own
// nodes carry, Tick.Held).
//
// What it re-stamps is only its own, and what it reads is one claim node per item
// it holds. A claim a rival holds is left alone (a refresh is not a takeover) and
// a claim node this release cannot read is left for Take to refuse rather than
// repaired by a beat — and neither is read at all unless it is one of the items
// this daemon's own nodes carry.
//
// That last part is the count below, and the reason it is the count: a finished
// plan task's claim is recorded and stands for good (claim.go's header), so the
// claims node is not a list but the project's whole claim history — one node per
// plan task it ever claimed, none of them ever going away. A refresh that walked
// it would read one node more for every claim the project ever made, on every
// cycle and every beat, for ever; a refresh that reads what it holds does not.
//
// Mutation: list the claims node and read every node under it (the walk this
// refresh replaced) and the reads below follow the finished claims staged here
// instead of staying the claims this daemon holds.
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

	db := &countingCAS{inner: rtdbClient(t, f)}
	rival := cg4Claim(t, db, cg4Rival, clockAt(now))

	// The project's plan-task history, which is what the claims node accumulates:
	// tasks a rival claimed and finished, every claim of which stands for good and
	// not one of which is this daemon's to read.
	finished := func(from, to int) {
		t.Helper()
		held := make([]orchestrator.Held, 0, to-from)
		for i := from; i < to; i++ {
			ref := orchestrator.ItemRef{Orchestrator: cg4Rival, Item: fmt.Sprintf("demo/T%02d", i)}
			if took, err := rival.Take(ctx, ref); err != nil || !took {
				t.Fatalf("the rival's take of %q: took=%v, err=%v", ref.Item, took, err)
			}
			held = append(held, orchestrator.Held{Ref: ref, Finished: true})
		}
		if err := rival.Refresh(ctx, held); err != nil {
			t.Fatalf("the rival's settlement of the tasks it finished: %v", err)
		}
	}
	finished(0, 25)

	// Two claims of the rival's own, one of each shape, neither of which the
	// refresh below re-stamps.
	for _, ref := range []orchestrator.ItemRef{
		orchestrator.ClaimRef(cg4Rival, rivalPlan),
		orchestrator.ClaimRef(cg4Rival, rivalItem),
	} {
		if took, err := rival.Take(ctx, ref); err != nil || !took {
			t.Fatalf("the rival's take of %q: took=%v, err=%v", ref.Item, took, err)
		}
	}

	claims := cg4Claim(t, db, cg4First, clockAt(now))
	for _, ref := range []orchestrator.ItemRef{
		orchestrator.ClaimRef(cg4First, mine),
		orchestrator.ClaimRef(cg4First, myItem),
	} {
		if took, err := claims.Take(ctx, ref); err != nil || !took {
			t.Fatalf("the take of %q: took=%v, err=%v", ref.Item, took, err)
		}
	}
	// A claim node this release cannot read, handed as one of this daemon's own:
	// the refresh passes over it, and Take is what refuses it.
	brokenPath := cg4PathOf(t, orchestrator.ClaimRef(cg4First, unreadable))
	f.Set(brokenPath, "not a claim")
	broken := f.Value(brokenPath)
	rivalPlanPath := cg4PathOf(t, orchestrator.ClaimRef(cg4Rival, rivalPlan))
	rivalItemPath := cg4PathOf(t, orchestrator.ClaimRef(cg4Rival, rivalItem))

	// What this daemon's own nodes carry: the two items it holds, and the one
	// node it cannot read.
	held := []orchestrator.Held{
		{Ref: orchestrator.ClaimRef(cg4First, mine)},
		{Ref: orchestrator.ClaimRef(cg4First, myItem)},
		{Ref: orchestrator.ClaimRef(cg4First, unreadable)},
	}

	// The refresh runs on a daemon whose clock has moved on: what it writes is
	// the server time of the beat.
	beat := cg4Claim(t, db, cg4First, clockAt(later))
	reads, writes := db.gets, db.puts
	if err := beat.Refresh(ctx, held); err != nil {
		t.Fatalf("the refresh of the claims this daemon holds: %v", err)
	}
	if got := db.gets - reads; got != len(held) {
		t.Errorf("the refresh read %d claim node(s) for the %d it holds, and what it reads is the items this daemon's own nodes carry", got, len(held))
	}
	if got := db.puts - writes; got != 2 {
		t.Errorf("the refresh wrote %d claim node(s), and it holds two", got)
	}

	// And the history grows without the refresh reading a node more: the claims
	// of the tasks the rival finished are not this daemon's and are not read.
	finished(25, 50)
	reads = db.gets
	if err := beat.Refresh(ctx, held); err != nil {
		t.Fatalf("the refresh over a longer claim history: %v", err)
	}
	if got := db.gets - reads; got != len(held) {
		t.Errorf("the refresh read %d claim node(s) once the project had finished 50 plan tasks, and the %d it holds did not change: what a refresh reads is bounded by the work in flight, never by the claims the project has ever made", got, len(held))
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

// TestClaimOnAFinishedItem is what a finished item does to the claim standing on
// it, for the two kinds of item a claim keys differently (ClaimRef).
//
// A plan task's claim is **recorded, not deleted**. Every orchestrator of the
// project that imported the plan carries the task in its own node, and that copy
// still reads approved long after the holder's run for it has finished, so a path
// deleted on the finish is an item nobody has taken to that orchestrator: it takes
// it and starts a second run of one piece of work — the two pull requests this
// claim exists to prevent, arrived at from the other side. The node therefore
// stays, marked finished, and the rival keeps skipping it exactly as it did while
// the work was going.
//
// A work item's claim is one orchestrator's own — its id is minted inside one node
// (D26) — so nothing else can be misled by its absence, and its node comes off when
// the item is done or cancelled: the claims this one node keeps are then bounded by
// the work in flight rather than by everything it ever ran.
//
// The settlement is the cycle's own read of the nodes (Refresh with what they
// say), so an item that finished in an earlier cycle is settled on the next read
// rather than on the write that finished it: a cycle that died between the two, or
// a write the server refused, is retried. The item is the only thing that settles a
// claim, and a finished plan task's is settled while it stands (the owner's ruling
// of 2026-11-23).
//
// Mutation: settle a plan task's claim the way a work item's is (delete the node)
// and the rival below takes the task the holder has finished and starts a second
// run of it — the finding this test answers; settle nothing and the node never says
// the task is done.
func TestClaimOnAFinishedItem(t *testing.T) {
	ctx := context.Background()
	now := o6Now()

	for _, kind := range []struct {
		name     string
		planTask string
		// stands is whether the claim node is still there once the item is
		// finished: it is for a plan task, which is the project's own record
		// that the task is done, and it is not for a work item.
		stands bool
	}{
		{name: "a plan task", planTask: cg4PlanTask, stands: true},
		{name: "a work item", stands: false},
	} {
		for _, phase := range []orchestrator.Phase{orchestrator.PhaseDone, orchestrator.PhaseCancelled} {
			t.Run(kind.name+"/"+string(phase), func(t *testing.T) {
				f := gcpfake.NewRTDB(t)
				item := workItemOf("wi-1")
				item.PlanTask = kind.planTask
				item.Phase = orchestrator.PhaseRunning
				item.RunID = "20261107-120000-aaaa"
				item.Attempts = 1
				s := o6Node(t, now, item)
				s.AgentID = cg4First
				// An item no attempt was ever committed for: nothing took a
				// claim on it, so the cycle's own reading does not name it.
				s.WorkItems = append(s.WorkItems, workItemOf("wi-2"))

				db := &countingCAS{inner: rtdbClient(t, f)}
				claims := cg4Claim(t, db, cg4First, clockAt(now))
				ref := orchestrator.ClaimRef(cg4First, item)
				if took, err := claims.Take(ctx, ref); err != nil || !took {
					t.Fatalf("the take before the item settles: took=%v, err=%v", took, err)
				}
				path := cg4PathOf(t, ref)

				// What a cycle reads at its own last step: the claim of every
				// item its nodes carry that has been started, as the nodes now
				// read (HeldClaims).
				settledPhase := phase
				after := o6Apply(t, s, orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{ID: item.ID, Phase: &settledPhase}}, orchestrator.Actor{Class: orchestrator.Daemon, Now: now})
				held, err := orchestrator.HeldClaims([]orchestrator.State{after})
				if err != nil {
					t.Fatalf("reading the claim of what this node carries: %v", err)
				}
				got := -1
				for i, h := range held {
					if h.Ref == ref {
						got = i
					}
				}
				if got < 0 {
					t.Fatalf("the node's %s item is claimed at %v, and the cycle's own reading does not name it", phase, path)
				}
				if !held[got].Finished {
					t.Fatalf("the node's %s item is claimed at %v, and the cycle's own reading does not name it finished", phase, path)
				}
				for _, h := range held {
					if h.Ref.Item == "wi-2" {
						t.Fatalf("the cycle's reading names %q, an item of this node no attempt was ever committed for: what a refresh reads is the work in flight and never a node's whole history", h.Ref.Item)
					}
				}

				if err := claims.Refresh(ctx, held); err != nil {
					t.Fatalf("settling the claims of what finished: %v", err)
				}
				v := f.Value(path)
				if !kind.stands {
					if v != nil {
						t.Errorf("the claim is still on the node as %#v after a work item finished, and a work item's id is one node's own", v)
					}
				} else {
					if v == nil {
						t.Fatalf("the claim is gone after the plan task finished, and a rival whose own node still carries the task approved takes a freed node as unclaimed work and starts a second run of it (D3209)")
					}
					doc := nodeObject(t, v, "the claim")
					if holder := nodeText(t, doc, "holder"); holder != cg4First {
						t.Errorf("the finished plan task's claim names %q, not the orchestration that finished it", holder)
					}
					if fin, _ := doc["finished"].(bool); !fin {
						t.Errorf("the claim node is %#v, and a plan task its holder finished is recorded as finished", doc)
					}

					// A rival keeps skipping it, and the holder may take its own
					// item again: the task is finished for the project, and
					// putting it back to work is that orchestrator's own act.
					rivalDB := &countingCAS{inner: rtdbClient(t, f)}
					rival := cg4Claim(t, rivalDB, cg4Rival, clockAt(now))
					if took, err := rival.Take(ctx, ref); err != nil || took {
						t.Errorf("the rival's take of a finished plan task: took=%v, err=%v, and one plan task is one run for the whole project", took, err)
					}
					if rivalDB.puts != 0 {
						t.Errorf("the rival wrote %d time(s) over a claim that is not its own", rivalDB.puts)
					}
					if took, err := claims.Take(ctx, ref); err != nil || !took {
						t.Errorf("the holder's take of its own finished plan task: took=%v, err=%v, and the node names this orchestrator", took, err)
					}
				}

				// A second pass over the same finished item writes nothing: the
				// record is what it is, and the beat does not rewrite it.
				before := db.puts
				if err := claims.Refresh(ctx, held); err != nil {
					t.Fatalf("the second pass over the same finished item: %v", err)
				}
				if db.puts != before {
					t.Errorf("the second pass wrote the finished item's claim %d time(s)", db.puts-before)
				}

				if kind.stands {
					// An item the caller's reading does not name as finished —
					// the item put back to work, whose node the next reading
					// carries as unfinished — is re-stamped, so the claim stops
					// reading finished while the work is going again.
					if err := claims.Refresh(ctx, []orchestrator.Held{{Ref: ref}}); err != nil {
						t.Fatalf("re-stamping the claim of a plan task put back to work: %v", err)
					}
					doc := nodeObject(t, f.Value(path), "the claim")
					if fin, _ := doc["finished"].(bool); fin {
						t.Errorf("the claim node is %#v with the item back to work, and the node records a finish only while the item is finished", doc)
					}
					return
				}

				// And a claim taken again on an item that is already finished is
				// released by that same read: the item is finished whatever cycle
				// wrote it, so the claim of a holder that died after the write
				// landed does not stand for ever.
				if took, err := claims.Take(ctx, ref); err != nil || !took {
					t.Fatalf("the take of a finished item: took=%v, err=%v", took, err)
				}
				if err := claims.Refresh(ctx, held); err != nil {
					t.Fatalf("the pass over a node whose item had already finished when the cycle read it: %v", err)
				}
				if f.Value(path) != nil {
					t.Errorf("the claim is still on the node as %#v, and the item was finished before the read that releases it", f.Value(path))
				}
			})
		}
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
