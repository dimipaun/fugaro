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
