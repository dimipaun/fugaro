package budget_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// byTier is one tier's real spend on each of the four leaves C1.3's rules
// allow: the run's lifetime ledger, the run's share of the day, the
// repository's day counters and the project's. It is read through the path
// builders, so a leaf written anywhere else is not what this sees.
func byTier(f *fixture, d int64, tier string) [4]int64 {
	return [4]int64{
		f.num(budget.PathRunByTier(slug, runID, tier) + "/spent"),
		f.num(budget.PathSpendRunByTier(d, slug, runID, tier) + "/spent"),
		f.num(budget.PathSpendRepoByTier(d, slug, tier) + "/spent"),
		f.num(budget.PathSpendGlobalByTier(d, tier) + "/spent"),
	}
}

// TestByTierSplitCarriesTheSettledMoney (C1.16, D140): a cascade run's
// settled money is split by the tier that paid for it, on all four leaves
// the rules carry, in the same write as the usage it is a split of. A tier-A
// call is in no leaf at all: it is the run's subscription, and it settles as
// notional -- the figure the counters already had a home for -- so it feeds
// no cap and no split. The tier the service's answer spells "B" is the
// rules' `b`.
func TestByTierSplitCarriesTheSettledMoney(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	ctx := context.Background()
	d := today()
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	used := budget.Micros(900_000)
	s.SetUsage(func() budget.Micros { return used })
	rep := gateway.StageReport{
		Calls: 3, Used: used, Tokens: 1200,
		ByModel:  map[string]pricing.Micros{"claude-sonnet-5-5": used},
		ByTier:   map[string]pricing.Micros{gateway.TierB: 600_000, gateway.TierC: 300_000},
		Notional: 200_000,
	}
	if err := s.Lease().Report(ctx, rep); err != nil {
		t.Fatal(err)
	}
	for tier, want := range map[string]int64{budget.TierB: 600_000, budget.TierC: 300_000} {
		for i, got := range byTier(f, d, tier) {
			if got != want {
				t.Fatalf("tier %s leaf %d = %d, want %d", tier, i, got, want)
			}
		}
	}
	// The split is not the total: the money is counted once, in `spent`, and
	// the notional figure of the tier-A call is on the leaf an oauth run's
	// own notional figure already uses.
	if f.run("spent") != 900_000 || f.repo(d, "spent") != 900_000 || f.global(d, "spent") != 900_000 {
		t.Fatalf("spent: run %d, repo %d, global %d", f.run("spent"), f.repo(d, "spent"), f.global(d, "spent"))
	}
	if f.run("notional") != 200_000 || f.repo(d, "notional") != 200_000 || f.global(d, "notional") != 200_000 {
		t.Fatalf("notional: run %d, repo %d, global %d", f.run("notional"), f.repo(d, "notional"), f.global(d, "notional"))
	}
	if f.repo(d, "counted") != 1_000_000 || f.global(d, "counted") != 1_000_000 || f.run("reserved") != 1_000_000 {
		t.Fatalf("the split moved a counted figure: repo %d, global %d, reserved %d", f.repo(d, "counted"), f.global(d, "counted"), f.run("reserved"))
	}
}

// TestByTierSplitDoesNotStallOnTheDayThatIsSpent (C1.16): a run that crosses
// UTC midnight keeps splitting. By then yesterday's room is gone -- a day's
// by-tier leaf reaches its reservation as the day is spent, and released
// rises only at Close -- so a round bounded by *every* candidate day's room
// would write nothing at all, for the rest of the run, while `spent` kept
// landing: the split a cascade run exists to show would stop, silently, at
// the first midnight. What bounds the run's lifetime leaf is the run's own
// room; the days' rooms only say how much of it lands where, and a day with
// none of its own is skipped rather than allowed to bound the round.
func TestByTierSplitDoesNotStallOnTheDayThatIsSpent(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	ctx := context.Background()
	d0 := today()
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	used := budget.Micros(1_000_000)
	s.SetUsage(func() budget.Micros { return used })
	runB := budget.PathRunByTier(slug, runID, budget.TierB) + "/spent"
	if err := s.Lease().Report(ctx, gateway.StageReport{Calls: 1, Used: 1_000_000, ByTier: map[string]pricing.Micros{gateway.TierB: 1_000_000}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first day's whole split", func() bool { return f.num(runB) == 1_000_000 })
	if f.share(d0, "spent") != 1_000_000 {
		t.Fatalf("the first day settled %d", f.share(d0, "spent"))
	}

	// The day turns: d0 holds a reservation of 1000000, all of it spent and
	// none of it released -- room 0 for the rest of the run. The grant that
	// follows is the new day's (and the session sees the new day at once: it
	// reads the day from the database's clock, the Date header of the answer
	// to the read this grant begins with).
	d1 := d0 + 1
	f.db.SetClock(func() time.Time { return noon.Add(24 * time.Hour) })
	if _, err := s.Lease().Grant(ctx, 500_000); err != nil {
		t.Fatal(err)
	}
	used = 1_500_000
	if err := s.Lease().Report(ctx, gateway.StageReport{Calls: 1, Used: 1_500_000, ByTier: map[string]pricing.Micros{gateway.TierB: 500_000}}); err != nil {
		t.Fatal(err)
	}
	// The totals settle onto today (yesterday's share has no room left).
	waitFor(t, "the new day's money to settle", func() bool { return f.run("spent") == 1_500_000 && f.share(d1, "spent") == 500_000 })
	// The split follows the money, on the run's leaf and on the new day's
	// three -- and it lands quickly: several heartbeats is many rounds.
	time.Sleep(150 * time.Millisecond)
	if got, want := byTier(f, d1, budget.TierB), [4]int64{1_500_000, 500_000, 500_000, 500_000}; got != want {
		t.Fatalf("the new day's split = %v, want %v (run, share, repo, global)", got, want)
	}
	if got, want := byTier(f, d0, budget.TierB), [4]int64{1_500_000, 1_000_000, 1_000_000, 1_000_000}; got != want {
		t.Fatalf("the spent day's split = %v, want %v: nothing may be moved onto it again, and the run's own leaf is where the whole split is", got, want)
	}
	// Nothing of it is pending any more: a split that stalled keeps writing
	// what it could not write, and this one has nothing left to write.
	time.Sleep(150 * time.Millisecond)
	if got := f.num(runB); got != 1_500_000 {
		t.Fatalf("the split moved past the money reported: %d", got)
	}
	if h, ok := f.nextHalt(100 * time.Millisecond); ok {
		t.Fatalf("a run splitting across midnight was halted: %+v", h)
	}
}

// TestByTierSplitMovesAtMostOneLargestLeasePerWrite (C1.16): the two shared
// day counters validate an increase of at most limits.maxReserveMicros, and
// the four leaves of one round are written in one atomic update, so one
// round moves at most that much and the rest waits for the next one. A
// writer that ignored the bound would have the whole update -- the totals
// beside it included -- refused in production.
func TestByTierSplitMovesAtMostOneLargestLeasePerWrite(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	ctx := context.Background()
	// Three largest-lease grants: a reservation big enough to hold the
	// split, which is what the leaves under the run's own nodes are bounded
	// by -- this test is about the shared counters' own bound.
	for i := 0; i < 3; i++ {
		if _, err := s.Lease().Grant(ctx, 5_000_000); err != nil {
			t.Fatal(err)
		}
	}
	s.SetUsage(func() budget.Micros { return 6_000_000 })
	if err := s.Lease().Report(ctx, gateway.StageReport{Calls: 4, Used: 6_000_000, ByTier: map[string]pricing.Micros{gateway.TierC: 6_000_000}}); err != nil {
		t.Fatal(err)
	}
	runC := budget.PathRunByTier(slug, runID, budget.TierC) + "/spent"
	waitFor(t, "the split to land whole", func() bool { return f.num(runC) == 6_000_000 })
	var steps []int64
	for _, r := range f.db.Requests() {
		if r.Method == http.MethodGet {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(r.Body, &body); err != nil {
			continue // a stream or a delete, not this write's shape
		}
		v, ok := body[runC].(float64)
		if !ok {
			continue
		}
		steps = append(steps, int64(v))
	}
	if len(steps) < 2 {
		t.Fatalf("the split was written in %d steps (%v): 6.00 against a largest lease of 5.00 is two", len(steps), steps)
	}
	var prev int64
	for _, v := range steps {
		if v-prev > 5_000_000 {
			t.Fatalf("one write moved the split by %d, more than the 5000000 the shared counters allow: %v", v-prev, steps)
		}
		if v <= prev {
			t.Fatalf("the split's steps are not increasing: %v", steps)
		}
		prev = v
	}
}

// TestByTierSplitDropsATierWithNoLeaf (C1.16, C1.3): the tier a stage report
// carries came off the wire, from the credentials service's answer, and the
// rules have a leaf for `b` and `c` and nothing else. A tier outside them is
// dropped from the split rather than turned into a path: building one would
// panic (the path builders refuse a tier with no leaf), and the panic is a
// fail-closed halt, which is worse than the display split losing a row --
// the money itself is in the totals either way. Nothing is written under the
// key, so a database that never heard of tier A stays that way.
func TestByTierSplitDropsATierWithNoLeaf(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	ctx := context.Background()
	d := today()
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	s.SetUsage(func() budget.Micros { return 300_000 })
	if err := s.Lease().Report(ctx, gateway.StageReport{
		Calls: 2, Used: 300_000,
		ByTier: map[string]pricing.Micros{"A": 100_000, gateway.TierB: 200_000, "quota": 50_000},
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the tier the rules know to land", func() bool {
		return f.num(budget.PathRunByTier(slug, runID, budget.TierB)+"/spent") == 200_000
	})
	if got := f.num(budget.PathRunByTier(slug, runID, budget.TierB) + "/spent"); got != 200_000 {
		t.Fatalf("tier b = %d", got)
	}
	for _, path := range []string{
		budget.PathRun(slug, runID) + "/byTier",
		budget.PathSpendRun(d, slug, runID) + "/byTier",
		budget.PathSpendRepo(d, slug) + "/byTier",
		budget.PathSpendGlobal(d) + "/byTier",
	} {
		got, _ := f.db.Value(path).(map[string]any)
		if len(got) != 1 {
			t.Fatalf("%s holds %v, want the one tier the rules have a leaf for", path, got)
		}
		if _, ok := got[budget.TierB]; !ok {
			t.Fatalf("%s holds %v, want only %s", path, got, budget.TierB)
		}
	}
	if h, ok := f.nextHalt(100 * time.Millisecond); ok {
		t.Fatalf("a tier with no leaf halted the run: %+v", h)
	}
}

// TestCascadeRunHaltsWhenRulesRefuseTheByTierCounters (C1.16, C1.17): the
// by-tier leaves are as new as the cascade, and a database whose rules
// predate them is a deployment C1.3's marker can miss. What the run meets is
// a permission refusal on the write that carried the split, and
// probeByTierKeys tells it from a blip the way checkCascadeKeys does for the
// registry entry: the same update, without the split's leaves.
//
// Refused with them and accepted without them, the run halts named -- the
// redeploy is the fix -- and the write is not retried. The totals of that
// same update land: the run keeps its spend and loses only the rows that say
// which tier paid it, which is the direction the split's own status
// (display-only, C1.3) makes safe.
func TestCascadeRunHaltsWhenRulesRefuseTheByTierCounters(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	ctx := context.Background()
	d := today()
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	s.SetUsage(func() budget.Micros { return 300_000 })
	// The document such a database has deployed refuses every write under its
	// byTier nodes (the $other branch of a document that never heard of the
	// child). Deny is that rule shape: a path and every node above or below
	// it. DenyKeys would not do -- it matches a key at the top level of a
	// write's *value*, and a multi-path update keys its leaves by path.
	for _, path := range []string{
		budget.PathRun(slug, runID) + "/byTier",
		budget.PathSpendRun(d, slug, runID) + "/byTier",
		budget.PathSpendRepo(d, slug) + "/byTier",
		budget.PathSpendGlobal(d) + "/byTier",
	} {
		f.db.Deny(path)
	}
	err := s.Lease().Report(ctx, gateway.StageReport{Calls: 1, Used: 300_000, ByTier: map[string]pricing.Micros{gateway.TierB: 300_000}})
	if err == nil {
		t.Fatal("a refused split reported no error")
	}
	h, ok := f.nextHalt(3 * time.Second)
	if !ok {
		t.Fatal("the run was not halted; a refused by-tier counter must halt it named")
	}
	if h.Reason != budget.ReasonCredsRulesDenied || h.Scope != budget.ScopeRun {
		t.Fatalf("halt = %+v, want %s/%s", h, budget.ReasonCredsRulesDenied, budget.ScopeRun)
	}
	if !contains(h.Detail, "fugaro init --firebase") {
		t.Errorf("the halt's detail does not name the redeploy: %q", h.Detail)
	}
	// The totals of the refused update landed without the split.
	if f.run("spent") != 300_000 || f.repo(d, "spent") != 300_000 {
		t.Fatalf("the run lost its own spend to a refused split: run %d, repo %d", f.run("spent"), f.repo(d, "spent"))
	}
	runB := budget.PathRunByTier(slug, runID, budget.TierB) + "/spent"
	if got := f.num(runB); got != 0 {
		t.Fatalf("the split was written into rules that refuse it: %d", got)
	}
	time.Sleep(150 * time.Millisecond) // several heartbeats
	if got := f.num(runB); got != 0 {
		t.Fatalf("the split was retried after the halt: %d", got)
	}
	if h2, ok := f.nextHalt(100 * time.Millisecond); ok {
		t.Fatalf("a second halt: %+v", h2)
	}
}

// TestByTierProbeDoesNotHaltOnARacedSharedCounter (C1.16, C1.17): every
// cascade run in a repository writes the two shared day counters, so an
// update carrying the split can be refused for a reason that is not the
// deployed rules at all -- another run settled its own share of the day
// between this run's read and its patch, leaving the value this run meant to
// write below the one the rules now see. The leaves under the run's own
// nodes cannot be refused that way (one session writes them, serialized), so
// the probe asks the rules about those first: a write the rules take there
// says the split is known and the refusal was the race, and the run retries
// instead of halting an operator whose database is fine with a redeploy it
// does not need.
func TestByTierProbeDoesNotHaltOnARacedSharedCounter(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	ctx := context.Background()
	d := today()
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	s.SetUsage(func() budget.Micros { return 300_000 })
	// The other run's settlement of the same day's tier-B share: it is on
	// the shared counters, above what this run is about to write, and the
	// rules refuse the update that follows it (a decrease). The fake does
	// not model the rules, so DenyNext is that refusal; the values are the
	// other run's, and they are what the shared rows hold afterwards.
	f.db.Set(budget.PathSpendRepoByTier(d, slug, budget.TierB)+"/spent", 400_000)
	f.db.Set(budget.PathSpendGlobalByTier(d, budget.TierB)+"/spent", 900_000)
	f.db.DenyNext(1)
	if err := s.Lease().Report(ctx, gateway.StageReport{Calls: 1, Used: 300_000,
		ByTier: map[string]pricing.Micros{gateway.TierB: 300_000}}); err != nil {
		t.Fatalf("a split refused by another run's write reported %v", err)
	}
	if h, ok := f.nextHalt(300 * time.Millisecond); ok {
		t.Fatalf("another cascade run's settlement halted this one: %+v", h)
	}
	// The run's money landed, and so did the two leaves under its own nodes:
	// the probe's write carried them, the rules took it, and the round is off
	// the pending figures.
	if got := f.run("spent"); got != 300_000 {
		t.Errorf("the run's own spend is %d, want 300000", got)
	}
	if got := f.share(d, "spent"); got != 300_000 {
		t.Errorf("the run's share of the day is %d, want 300000", got)
	}
	if got := f.num(budget.PathRunByTier(slug, runID, budget.TierB) + "/spent"); got != 300_000 {
		t.Errorf("the run's own tier-B counter is %d, want 300000", got)
	}
	if got := f.num(budget.PathSpendRunByTier(d, slug, runID, budget.TierB) + "/spent"); got != 300_000 {
		t.Errorf("the run's own tier-B share of the day is %d, want 300000", got)
	}
	// The two shared rows are what the probe could not write -- writing them
	// is what another run's update refused -- so this run's round is not in
	// them; they hold the other run's own figures and nothing of this one's.
	// The money and the run's own rows are unaffected, the split is a
	// display figure nothing decides on (C1.3), and the alternative -- a
	// retry that moved the run's own leaves a second time, or the halt this
	// test exists to rule out -- is worse for the run than a shared row one
	// settlement behind.
	if got := f.repo(d, "byTier/b/spent"); got != 400_000 {
		t.Errorf("the repository's tier-B row is %d, want the other run's 400000", got)
	}
	if got := f.global(d, "byTier/b/spent"); got != 900_000 {
		t.Errorf("the project's tier-B row is %d, want the other run's 900000", got)
	}
	// And the run is not left half-broken: the next round settles its money
	// and splits all four leaves again, from the values it reads.
	s.SetUsage(func() budget.Micros { return 400_000 })
	if err := s.Lease().Report(ctx, gateway.StageReport{Calls: 1, Used: 100_000,
		ByTier: map[string]pricing.Micros{gateway.TierB: 100_000}}); err != nil {
		t.Fatalf("the round after the race reported %v", err)
	}
	want := [4]int64{400_000, 400_000, 500_000, 1_000_000}
	if got := byTier(f, d, budget.TierB); got != want {
		t.Errorf("the four tier-B leaves are %v, want %v", got, want)
	}
	if h, ok := f.nextHalt(100 * time.Millisecond); ok {
		t.Fatalf("a second halt: %+v", h)
	}
}

// TestByTierProbeIsNotABlanketDrop (C1.16): the probe is not a run that
// stops splitting whenever a write is refused. The same split the test above
// has refused is written and kept when the rules know it -- otherwise that
// test would pass for a run that never wrote a by-tier leaf at all.
func TestByTierProbeIsNotABlanketDrop(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	ctx := context.Background()
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	s.SetUsage(func() budget.Micros { return 300_000 })
	if err := s.Lease().Report(ctx, gateway.StageReport{Calls: 1, Used: 300_000, ByTier: map[string]pricing.Micros{gateway.TierB: 300_000}}); err != nil {
		t.Fatal(err)
	}
	runB := budget.PathRunByTier(slug, runID, budget.TierB) + "/spent"
	if got := f.num(runB); got != 300_000 {
		t.Fatalf("the split is %d, want the 300000 the stage reported", got)
	}
	if h, ok := f.nextHalt(100 * time.Millisecond); ok {
		t.Fatalf("a run whose rules accept the split was halted: %+v", h)
	}
}

// TestNoTierRunWritesNoByTierLeaf (C1.16, C1.3): the by-tier leaves are the
// cascade's, and a run whose calls carry no tier writes none of them --
// including a run whose money is entirely notional (an oauth run). That is
// the "written only by a cascade run" the rules' opt-in is for, and it is
// what keeps a repository that never opted in from meeting a rules document
// that does not carry the leaves.
func TestNoTierRunWritesNoByTierLeaf(t *testing.T) {
	f := newFixture(t)
	s := f.open()
	if h, err := s.Admit(context.Background(), budget.AdmitOptions{}); h != nil || err != nil {
		t.Fatal(h, err)
	}
	if err := s.Start(context.Background(), budget.AgentEntry{Repo: "acme/app", Auth: "oauth"}); err != nil {
		t.Fatal(err)
	}
	defer s.Finish(context.Background(), "succeeded")
	d := today()
	if err := s.AddNotional(context.Background(), 400_000, nil); err != nil {
		t.Fatal(err)
	}
	if f.run("notional") != 400_000 {
		t.Fatalf("notional = %d", f.run("notional"))
	}
	for _, tier := range []string{budget.TierB, budget.TierC} {
		for i, got := range byTier(f, d, tier) {
			if got != 0 {
				t.Fatalf("a run with no tier wrote leaf %d of %s: %d", i, tier, got)
			}
		}
	}
	if v := f.db.Value(budget.PathRunByTier(slug, runID, budget.TierB)); v != nil {
		t.Fatalf("a run with no tier wrote %v", v)
	}
}

// TestByTierSplitStaysInsideTheReservation (C1.16): every leaf is validated
// on its own, and on the run's own nodes the rules bound each one by that
// node's reservation -- so a split larger than what the run holds is not
// written in one go, and what it could not write stays pending. A lease that
// raises the reservation is what unblocks the rest: the run's spend is
// accounted when it is settled and the split follows the money, never ahead
// of it.
func TestByTierSplitStaysInsideTheReservation(t *testing.T) {
	f := newFixture(t)
	s := f.started(budget.AgentEntry{Stage: "implement"})
	ctx := context.Background()
	if _, err := s.Lease().Grant(ctx, 1_000_000); err != nil {
		t.Fatal(err)
	}
	s.SetUsage(func() budget.Micros { return 1_400_000 })
	// The run reports more tier-B money than it holds a reservation for: the
	// leaf may not pass reserved - released, so only part of it is written.
	if err := s.Lease().Report(ctx, gateway.StageReport{Calls: 2, Used: 1_400_000, ByTier: map[string]pricing.Micros{gateway.TierB: 1_400_000}}); err != nil {
		t.Fatal(err)
	}
	runB := budget.PathRunByTier(slug, runID, budget.TierB) + "/spent"
	waitFor(t, "the split to reach the reservation", func() bool { return f.num(runB) == 1_000_000 })
	// Several heartbeats later it has not moved past it: the rest of the
	// split is pending, not written where the rules would refuse it.
	time.Sleep(150 * time.Millisecond)
	if got := f.num(runB); got != 1_000_000 {
		t.Fatalf("the split wrote %d, want the 1000000 the run holds a reservation for", got)
	}
	// A second lease raises the reservation, and the pending part of the
	// split lands -- no further than the money that was reported.
	again, err := s.Lease().Grant(ctx, 500_000)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pending split to land", func() bool { return f.num(runB) == 1_400_000 })
	time.Sleep(150 * time.Millisecond)
	if got, want := f.run("reserved"), 1_000_000+int64(again); got != want {
		t.Fatalf("reserved = %d, want %d", got, want)
	}
	if got := f.num(runB); got != 1_400_000 {
		t.Fatalf("the split grew past the reported money: %d", got)
	}
}
