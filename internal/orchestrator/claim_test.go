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
// and the loser re-reads and re-decides. A claim is held while its holder's
// lease beats and for ClaimGrace past its last beat, and it comes off when the
// item is done or cancelled.
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

	// cg4PlanTask is one node of a plan both orchestrators imported: the
	// identity two of them share (D2704), which is what makes a claim on it one
	// node for the whole project (D3209).
	cg4PlanTask = "demo/A"

	// cg4Beat is the daemon a seeded lease node names. A lease's holder is a
	// daemon and not an orchestrator id (Lease), and a test that stages a live
	// holder needs one that is a holder.
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

// cg4Lease seeds an orchestrator's lease node with one beat at at: a claim's
// liveness is its holder's lease, so a test stages a live holder by writing one
// and a gone one by writing none at all.
//
// The path is spelled from the subtree's own constants because PathLease still
// holds every id to agentRE, which carries no `~`, so a named orchestrator's
// lease has no builder until CG1a — claim.go's claimLeasePath says the same.
func cg4Lease(t *testing.T, f *gcpfake.RTDB, holder string, at time.Time) {
	t.Helper()
	if !orchestrator.ValidOrchestratorID(holder) {
		t.Fatalf("%q is not an orchestrator id, so it has no lease node", holder)
	}
	path := orchestrator.SubtreeRoot + "/" + budget.Key(holder) + "/" + orchestrator.LeaseNode
	f.Set(path, map[string]any{"holder": cg4Beat, "at": at.UTC().Format(time.RFC3339)})
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
// a holder that is still driving and answers false without writing again. An
// orchestrator that forced its write past the 412 would put two runs on one
// plan task.
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

	// Both are driving: what this test is about is the claim and not a dead
	// holder, which is the case the next test stages.
	cg4Lease(t, f, cg4First, now)
	cg4Lease(t, f, cg4Rival, now)

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
		t.Error("the loser took the claim a live holder is still working under")
	}
	if loserDB.puts != 1 {
		t.Errorf("the loser wrote %d time(s) in total; a live holder's claim is never written over", loserDB.puts)
	}

	// An orchestrator that already holds the item is given it back without a
	// write: a claim is kept alive by its holder's lease, not by rewriting its
	// node, so a daemon that asks for its own claim every cycle moves no ETag
	// the project's other readers would have to re-read.
	mine, err := winner.Take(ctx, cg4Ref(t, cg4First))
	if err != nil {
		t.Fatalf("the winner's second take: %v", err)
	}
	if !mine {
		t.Error("the holder of a claim was not given its own claim back")
	}
	if winnerDB.puts != 1 {
		t.Errorf("the winner wrote %d time(s); its own claim is kept alive by its lease", winnerDB.puts)
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

	// The rival took the item before this daemon planned it, and it is driving:
	// the item is the rival's work and not this daemon's.
	cg4Lease(t, f, cg4Rival, now)
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

	// The rival finishes the item and releases its claim: the same plan, the
	// same daemon, and now the launch happens.
	if freed, err := rival.Release(ctx, orchestrator.ClaimRef(cg4Rival, item)); err != nil || !freed {
		t.Fatalf("the rival's release: freed=%v, err=%v", freed, err)
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

// cg4PlanItem is a work item standing for one node of a plan: the declaration
// the O6 helpers give, with the plan task two orchestrators of a project name
// alike, approved as it now stands.
func cg4PlanItem(id string, approvedAt time.Time) orchestrator.WorkItem {
	w := workItemOf(id)
	w.PlanTask = cg4PlanTask
	return o6ApprovedBy(w, approvedAt)
}

// TestClaimReleasedOnLapse is the other half of a claim's life: a claim outlives
// its holder's last beat by ClaimGrace and no longer, so an orchestrator whose
// laptop lid closed does not keep an item from the project for ever.
//
// Each case is one beat on the holder's lease node, read against the same
// server clock. The cases with no lease node are the clean exit: a daemon
// releases its lease on the way out (Lease.Release) while the runs it started
// are still going, so what the grace is measured from there is the claim's own
// stamp — the last time the holder was known to be driving, which its beat
// re-writes for as long as it drives (Refresh). Reading that node as "gone at
// once" is what hands a restarted daemon's in-flight item to a rival.
//
// Mutation: read every claim that is not this daemon's as free (drop the
// holder-live read) and the first two cases take a holder that is still
// driving; read a claim whose holder is inside the grace as free — the absent
// lease node measured from nothing, or from the day the item was taken — and a
// rival starts a second run of a task whose run is still going.
func TestClaimReleasedOnLapse(t *testing.T) {
	ctx := context.Background()
	now := o6Now()

	// cg4StaleClaim is a claim taken long before the reading, which is what a
	// claim whose holder went away for good looks like.
	cg4StaleClaim := now.Add(-time.Hour)

	for _, tc := range []struct {
		name string
		// beat is the holder's last beat, on a lease node the case has.
		beat time.Time
		// claimedAt is the claim's own stamp, which is what the absent-lease
		// cases are measured against.
		claimedAt time.Time
		lease     bool
		live      bool
	}{
		{name: "a beat inside the ttl", beat: now.Add(-orchestrator.LeaseTTL / 2), claimedAt: cg4StaleClaim, lease: true, live: true},
		{name: "a beat lapsed inside the grace", beat: now.Add(-orchestrator.LeaseTTL - orchestrator.ClaimGrace/2), claimedAt: cg4StaleClaim, lease: true, live: true},
		{name: "a beat lapsed by exactly a lease and a grace", beat: now.Add(-orchestrator.LeaseTTL - orchestrator.ClaimGrace), claimedAt: now, lease: true, live: false},
		{name: "a beat lapsed past the grace", beat: now.Add(-orchestrator.LeaseTTL - orchestrator.ClaimGrace - time.Second), claimedAt: now, lease: true, live: false},
		{name: "a beat the server could not have written yet", beat: now.Add(orchestrator.LeaseSkew + time.Second), claimedAt: now, lease: true, live: false},
		// The clean exit: no lease node, and the claim's own stamp decides. A
		// claim stamped a moment ago is a daemon that has just stopped driving —
		// an upgrade restart or a stop while its runs are in flight — and the
		// item stays its own until the grace has passed.
		{name: "no lease node, a claim stamped a moment ago", claimedAt: now.Add(-2 * time.Second), live: true},
		{name: "no lease node, a claim stamped inside the grace", claimedAt: now.Add(-orchestrator.ClaimGrace / 2), live: true},
		{name: "no lease node, a claim stamped by exactly the grace", claimedAt: now.Add(-orchestrator.ClaimGrace), live: false},
		{name: "no lease node, a claim stamped past the grace", claimedAt: now.Add(-orchestrator.ClaimGrace - time.Second), live: false},
		{name: "no lease node, a claim stamped before the holder went", claimedAt: cg4StaleClaim, live: false},
		{name: "no lease node, a claim the server could not have written yet", claimedAt: now.Add(orchestrator.LeaseSkew + time.Second), live: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			path := cg4Path(t, cg4First)
			f.Set(path, map[string]any{"holder": cg4Rival, "at": tc.claimedAt.UTC().Format(time.RFC3339)})
			if tc.lease {
				cg4Lease(t, f, cg4Rival, tc.beat)
			}
			rivalAt := nodeObject(t, f.Value(path), "the claim")

			db := &countingCAS{inner: rtdbClient(t, f)}
			claims := cg4Claim(t, db, cg4First, clockAt(now))

			taken, err := claims.Take(ctx, cg4Ref(t, cg4First))
			if err != nil {
				t.Fatalf("the take over a lapsed claim: %v", err)
			}
			if taken != !tc.live {
				t.Fatalf("the take answered %v for a holder that is live=%v", taken, tc.live)
			}
			holder, at := cg4Held(t, f, path)
			if tc.live {
				if db.puts != 0 {
					t.Errorf("a claim a live holder is working under was written %d time(s)", db.puts)
				}
				if holder != cg4Rival || nodeText(t, rivalAt, "at") != at {
					t.Errorf("the claim now names %q at %q, and the live holder's own claim is %q at %q", holder, at, cg4Rival, nodeText(t, rivalAt, "at"))
				}
				return
			}
			if db.puts != 1 {
				t.Errorf("the take over a gone holder wrote %d time(s), not once", db.puts)
			}
			if holder != cg4First {
				t.Errorf("the claim names %q after the takeover, not the orchestrator that took it", holder)
			}
			if at != now.Format(time.RFC3339) {
				t.Errorf("the taken-over claim is stamped %q, not the server time of the takeover (%s)", at, now.Format(time.RFC3339))
			}

			// The item has moved to the taker, and the orchestrator that held it
			// is the one that skips it now.
			cg4Lease(t, f, cg4First, now)
			held, err := cg4Claim(t, rtdbClient(t, f), cg4Rival, clockAt(now)).Take(ctx, cg4Ref(t, cg4Rival))
			if err != nil {
				t.Fatalf("the gone holder's next take: %v", err)
			}
			if held {
				t.Error("the orchestrator whose claim was taken over still holds the item")
			}
			if holder, _ := cg4Held(t, f, path); holder != cg4First {
				t.Errorf("the claim names %q after the loser asked for it again, not the taker", holder)
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

// TestClaimRefresh is the other half of the lapse above: the stamp a rival
// measures a missing lease node against is the claim's own, and the holder is
// what re-writes it — on the beat, so that a daemon that is driving keeps its
// items through the clean exit that deletes its lease node.
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

	// The refresh runs on a daemon whose clock has moved on: what it writes is
	// the server time of the beat, which is what the next rival reads.
	beat := cg4Claim(t, db, cg4First, clockAt(later))
	writes := db.puts
	if err := beat.Refresh(ctx); err != nil {
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
			t.Errorf("%s is stamped %q after the refresh, not the beat's own server time (%s): a rival measures a missing lease node from this", tc.what, at, later.UTC().Format(time.RFC3339))
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
// nobody is on, and the claim on it is what would keep another orchestrator
// from taking the item the next time one of them has it in front of them.
//
// Only what moved is released: a node that finished its work in an earlier
// cycle released its claims then, and a claim left standing by a cycle that died
// is released by the holder's lease lapsing (Take) rather than by a read of
// every item a plan has ever finished.
//
// Mutation: release every finished item the node carries (drop the before-read)
// and the third case below writes over a claim nothing this cycle settled.
func TestClaimReleasedWhenTheItemSettles(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	now := o6Now()

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

	done := orchestrator.PhaseDone
	after := o6Apply(t, s, orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{ID: item.ID, Phase: &done}}, orchestrator.Actor{Class: orchestrator.Daemon, Now: now})

	released, err := orchestrator.SettledClaims(ctx, claims, s, after)
	if err != nil {
		t.Fatalf("releasing the claims of what settled: %v", err)
	}
	if released != 1 {
		t.Errorf("the cycle released %d claim(s) for one item that settled", released)
	}
	if v := f.Value(path); v != nil {
		t.Errorf("the claim is still on the node as %#v after the item settled", v)
	}

	// The same pair again releases nothing: what settled in an earlier cycle is
	// not this cycle's to release.
	again, err := orchestrator.SettledClaims(ctx, claims, s, after)
	if err != nil {
		t.Fatalf("the second release pass: %v", err)
	}
	if again != 0 {
		t.Errorf("the pass released %d claim(s) for an item that had already settled", again)
	}

	// A claim on an item that settled before this cycle is not released by it:
	// the node finished that work in an earlier cycle, which is where its claim
	// came off.
	if took, err := claims.Take(ctx, ref); err != nil || !took {
		t.Fatalf("the take of a settled item: took=%v, err=%v", took, err)
	}
	settled := o6Apply(t, s, orchestrator.Op{Op: orchestrator.OpWorkItemUpsert, WorkItem: &orchestrator.WorkItemOp{ID: item.ID, Phase: &done}}, orchestrator.Actor{Class: orchestrator.Daemon, Now: now})
	left, err := orchestrator.SettledClaims(ctx, claims, settled, settled)
	if err != nil {
		t.Fatalf("the pass over a node whose item had already settled: %v", err)
	}
	if left != 0 {
		t.Errorf("the pass released %d claim(s) for an item that was already settled when the cycle read the node", left)
	}
	if holder, _ := cg4Held(t, f, path); holder != cg4First {
		t.Errorf("the claim names %q, and the pass over what had already settled is not what releases it", holder)
	}
}

// TestClaimFailsClosedOnWhatItCannotRead is the state node's own rule on the
// claim: a node this release cannot read is a refusal, never a claim of nobody,
// and a holder it cannot read is a live one rather than a gone one. Reading a
// broken claim as free — or a holder it could not model as a dead one — is
// exactly how two orchestrators end up on one plan task.
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

	// A holder whose lease node this release cannot read is a live holder: it
	// must not be taken for a dead one and its item taken from it.
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
				t.Error("an item was taken from a holder this release could not read")
			}
			if db.puts != 0 {
				t.Errorf("the claim of a holder this release could not read was written %d time(s)", db.puts)
			}
			if holder, _ := cg4Held(t, f, path); holder != cg4Rival {
				t.Errorf("the claim names %q, and a holder this release cannot read keeps it", holder)
			}
		})
	}
}
