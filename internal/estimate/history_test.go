package estimate

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/tier"
)

// historyReadAt is the instant every table in these tests was built at, so the
// cache's own stamp is a figure a test can spell.
var historyReadAt = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// historyRun is one run record as the table reads it: a tier, a rigor mode, a
// status, a wall-clock duration in minutes and a cost in dollars. A mode
// outside the vocabulary writes no rigor block, a negative cost writes no cost
// breakdown at all (a record from before the breakdown existed), and every
// record starts at the same instant so two of them with the same duration are
// the same sample.
func historyRun(tr tier.Tier, mode rigor.Mode, status runstore.Status, minutes, cost float64) runstore.Record {
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	finish := start.Add(time.Duration(minutes * float64(time.Minute)))
	rec := runstore.Record{
		Status:     status,
		StartedAt:  start,
		FinishedAt: &finish,
		Tier:       &runstore.TierRecord{Tier: tr},
	}
	if rigor.Known(mode) {
		rec.Rigor = &runstore.RigorRecord{Mode: mode, From: rigor.FromDefault, At: runstore.RigorAtBootstrap}
	}
	if cost >= 0 {
		c := runstore.NewCost(cost, 0, runstore.BasisAPIList)
		rec.Cost = &c
	}
	return rec
}

// historySeries is one record per duration, all of the same tier, mode and
// status, each carrying the cost at the same index. A costs list shorter than
// the durations, nil included, writes no cost figure for the rest.
func historySeries(tr tier.Tier, mode rigor.Mode, status runstore.Status, minutes, costs []float64) []runstore.Record {
	out := make([]runstore.Record, 0, len(minutes))
	for i, m := range minutes {
		cost := -1.0
		if i < len(costs) {
			cost = costs[i]
		}
		out = append(out, historyRun(tr, mode, status, m, cost))
	}
	return out
}

// sameRange reports whether two ranges are the same figures. The figures are
// interpolated floats, so the comparison is to a nanosecond rather than to the
// last bit: a test that spells 17.5 out should not fail on a rounding.
func sameRange(got, want Range) bool {
	const eps = 1e-9
	return math.Abs(got.Low-want.Low) < eps && math.Abs(got.High-want.High) < eps
}

// TestTableFallsBackTierThenSeed: D2713's order of answers. A (tier, rigor)
// group with MinRuns runs or more answers for itself; below that the tier
// alone answers, every run of the tier whatever mode it resolved, if it reaches
// MinRuns; and a tier nothing reaches answers its seed minutes, widened to
// SeedLow..SeedHigh, with no cost figure at all (D2712).
//
// The durations are chosen so the arithmetic is a reader's own: medium/pro is
// five runs of 10..50 minutes, so p25/p50/p75 are 20/30/40 and the widening
// under WideRuns runs takes the range to 15-45; medium/critical is two runs of
// 60 and 70 minutes, whose group is too thin and whose tier is not (seven runs
// of 10..70 minutes, p25/p50/p75 of 25/40/55, widened to 17.5-62.5).
func TestTableFallsBackTierThenSeed(t *testing.T) {
	recs := historySeries(tier.Medium, rigor.Pro, runstore.StatusSucceeded,
		[]float64{10, 20, 30, 40, 50}, []float64{1, 2, 3, 4, 5})
	recs = append(recs, historySeries(tier.Medium, rigor.Critical, runstore.StatusSucceeded,
		[]float64{60, 70}, []float64{6, 7})...)
	table := Build(recs, historyReadAt)

	history := table.Answer(tier.Medium, rigor.Pro)
	if history.Basis != BasisHistory {
		t.Errorf("(medium, pro) basis = %q, want %q", history.Basis, BasisHistory)
	}
	if history.Runs != 5 {
		t.Errorf("(medium, pro) runs = %d, want 5", history.Runs)
	}
	if want := (Range{Low: 15, High: 45}); !sameRange(history.Minutes, want) {
		t.Errorf("(medium, pro) minutes = %v, want %v", history.Minutes, want)
	}
	if !history.HasCost {
		t.Fatal("(medium, pro) has no cost: five runs carried one")
	}
	if want := (Range{Low: 1.5, High: 4.5}); !sameRange(history.Cost, want) {
		t.Errorf("(medium, pro) cost = %v, want %v", history.Cost, want)
	}

	byTier := table.Answer(tier.Medium, rigor.Critical)
	if byTier.Basis != BasisTier {
		t.Errorf("(medium, critical) basis = %q, want %q: two runs are under MinRuns", byTier.Basis, BasisTier)
	}
	if byTier.Runs != 7 {
		t.Errorf("(medium, critical) runs = %d, want 7: the tier's own group", byTier.Runs)
	}
	if want := (Range{Low: 17.5, High: 62.5}); !sameRange(byTier.Minutes, want) {
		t.Errorf("(medium, critical) minutes = %v, want %v", byTier.Minutes, want)
	}
	if want := (Range{Low: 1.75, High: 6.25}); !byTier.HasCost || !sameRange(byTier.Cost, want) {
		t.Errorf("(medium, critical) cost = %v (%v), want %v", byTier.Cost, byTier.HasCost, want)
	}

	seeded := table.Answer(tier.Large, rigor.Pro)
	if seeded.Basis != BasisSeed {
		t.Errorf("(large, pro) basis = %q, want %q", seeded.Basis, BasisSeed)
	}
	if seeded.Runs != 0 {
		t.Errorf("(large, pro) runs = %d, want 0: a seed is not a sample", seeded.Runs)
	}
	if want := (Range{Low: 120, High: 480}); !sameRange(seeded.Minutes, want) {
		t.Errorf("(large, pro) minutes = %v, want %v (240 minutes, widened)", seeded.Minutes, want)
	}
	if seeded.HasCost {
		t.Errorf("(large, pro) cost = %v, want no figure: there is no cost seed", seeded.Cost)
	}

	if got := table.Answer(tier.Trivial, rigor.POC); got.Basis != BasisSeed || !sameRange(got.Minutes, Range{Low: 5, High: 20}) {
		t.Errorf("(trivial, poc) = %+v, want the 10-minute seed widened to 5-20", got)
	}
}

// TestTableWidensUnderTwenty: D2713's widening. A range drawn from under
// WideRuns runs is widened by Widening on each side of the median, and one
// drawn from WideRuns or more is the plain p25-p75. The median does not move:
// it is the figure a reader takes as the estimate.
func TestTableWidensUnderTwenty(t *testing.T) {
	ten := make([]float64, 0, 10)
	for i := 1; i <= 10; i++ {
		ten = append(ten, float64(i)*10)
	}
	table := Build(historySeries(tier.Small, rigor.Pro, runstore.StatusSucceeded, ten, nil), historyReadAt)
	// p25/p50/p75 of 10..100 are 32.5/55/77.5, and each half of that grows
	// 1.5x: 55 - 1.5*(55-32.5) = 21.25 and 55 + 1.5*(77.5-55) = 88.75.
	if got := table.Answer(tier.Small, rigor.Pro); !sameRange(got.Minutes, Range{Low: 21.25, High: 88.75}) {
		t.Errorf("ten runs = %v, want the widened 21.25-88.75", got.Minutes)
	}

	twenty := make([]float64, 0, 20)
	for i := 1; i <= 20; i++ {
		twenty = append(twenty, float64(i))
	}
	table = Build(historySeries(tier.Small, rigor.Pro, runstore.StatusSucceeded, twenty, nil), historyReadAt)
	// p25/p50/p75 of 1..20 are 5.75/10.5/15.25, published as they are.
	if got := table.Answer(tier.Small, rigor.Pro); !sameRange(got.Minutes, Range{Low: 5.75, High: 15.25}) {
		t.Errorf("twenty runs = %v, want the plain 5.75-15.25", got.Minutes)
	}

	// A skewed sample widens below zero: five runs of 1, 1, 5, 5 and 9 minutes
	// are p25/p50/p75 of 1/5/5, whose low half reaches 5 - 1.5*(5-1) = -1. A
	// plan may not print a negative minute or dollar figure, so the low is
	// floored at zero and the high, here the median itself, is left alone. The
	// costs are the same five figures, so the same floor answers for them.
	skewed := Build(historySeries(tier.Medium, rigor.Pro, runstore.StatusSucceeded,
		[]float64{1, 1, 5, 5, 9}, []float64{1, 1, 5, 5, 9}), historyReadAt).Answer(tier.Medium, rigor.Pro)
	if want := (Range{Low: 0, High: 5}); !sameRange(skewed.Minutes, want) {
		t.Errorf("a skewed five runs = %v, want the floored %v", skewed.Minutes, want)
	}
	if want := (Range{Low: 0, High: 5}); !skewed.HasCost || !sameRange(skewed.Cost, want) {
		t.Errorf("a skewed five runs' cost = %v (%v), want the floored %v", skewed.Cost, skewed.HasCost, want)
	}
}

// TestNoCostSeed: D2712's "no cost defaults". A seeded answer carries no cost
// figure at all, and neither does a group whose runs were recorded before the
// cost breakdown existed: the figure reads "no history yet" rather than a zero
// a reader would take for free.
func TestNoCostSeed(t *testing.T) {
	table := Build(nil, historyReadAt)
	seeded := table.Answer(tier.Medium, rigor.Pro)
	if seeded.HasCost {
		t.Errorf("a seeded answer carries cost %v, want no figure", seeded.Cost)
	}
	if seeded.Cost != (Range{}) {
		t.Errorf("a seeded answer's cost = %v, want the zero range beside HasCost false", seeded.Cost)
	}

	// Five runs of the tier, none of them with a cost breakdown.
	recs := historySeries(tier.Small, rigor.Pro, runstore.StatusSucceeded, []float64{10, 20, 30, 40, 50}, []float64{-1, -1, -1, -1, -1})
	thin := Build(recs, historyReadAt)
	got := thin.Answer(tier.Small, rigor.Pro)
	if got.Basis != BasisHistory || got.Runs != 5 {
		t.Fatalf("five runs = %+v, want a history answer from five runs", got)
	}
	if got.HasCost {
		t.Errorf("cost = %v, want no figure: none of the five records carried one", got.Cost)
	}

	// The same five runs with a cost each do carry one.
	paid := historySeries(tier.Small, rigor.Pro, runstore.StatusSucceeded, []float64{10, 20, 30, 40, 50}, []float64{1, 1, 1, 1, 1})
	withCost := Build(paid, historyReadAt).Answer(tier.Small, rigor.Pro)
	if !withCost.HasCost || !sameRange(withCost.Cost, Range{Low: 1, High: 1}) {
		t.Errorf("cost = %v (%v), want the five runs' own 1-1", withCost.Cost, withCost.HasCost)
	}

	// A group wide enough to publish a plain range can still be thin on cost:
	// twenty-five runs of which only the five oldest carried a breakdown are
	// a sample of twenty-five for the minutes and of five for the dollars, and
	// it is the dollars' own five that say whether they are widened.
	mixed := historySeries(tier.Medium, rigor.Pro, runstore.StatusSucceeded,
		[]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25},
		[]float64{1, 2, 3, 4, 5})
	mixedAnswer := Build(mixed, historyReadAt).Answer(tier.Medium, rigor.Pro)
	if want := (Range{Low: 7, High: 19}); !sameRange(mixedAnswer.Minutes, want) {
		t.Errorf("minutes = %v, want the twenty-five runs' own %v", mixedAnswer.Minutes, want)
	}
	if want := (Range{Low: 1.5, High: 4.5}); !mixedAnswer.HasCost || !sameRange(mixedAnswer.Cost, want) {
		t.Errorf("cost = %v (%v), want the five paid runs' widened %v", mixedAnswer.Cost, mixedAnswer.HasCost, want)
	}
}

// TestExcludesUnfinishedRuns: the runs no figure can be drawn from are counted
// and never sampled. A run that never finished has no duration, an
// infra_error is a fault of the platform rather than a measure of the work, and
// a cancelled run was stopped at whatever moment a person chose -- none of the
// three says how long the work takes, so all three are left out. A run that
// ended failed is not one of them: it took the time it took, and its duration
// is a real sample of the tier.
func TestExcludesUnfinishedRuns(t *testing.T) {
	recs := historySeries(tier.Small, rigor.Pro, runstore.StatusSucceeded,
		[]float64{10, 20, 30, 40, 50}, []float64{1, 2, 3, 4, 5})
	recs = append(recs, historyRun(tier.Small, rigor.Pro, runstore.StatusFailed, 60, 6))
	recs = append(recs, historyRun(tier.Small, rigor.Pro, runstore.StatusInfraError, 900, 90))
	recs = append(recs, historyRun(tier.Small, rigor.Pro, runstore.StatusCancelled, 900, 90))
	running := historyRun(tier.Small, rigor.Pro, runstore.StatusRunning, 900, 90)
	recs = append(recs, running)

	// The same four records of another tier, of which only three count.
	recs = append(recs, historySeries(tier.Large, rigor.Pro, runstore.StatusSucceeded,
		[]float64{100, 110, 120}, []float64{1, 2, 3})...)
	recs = append(recs, historyRun(tier.Large, rigor.Pro, runstore.StatusInfraError, 900, 90))
	recs = append(recs, historyRun(tier.Large, rigor.Pro, runstore.StatusCancelled, 900, 90))
	never := historyRun(tier.Large, rigor.Pro, runstore.StatusRunning, 900, 90)
	never.FinishedAt = nil
	recs = append(recs, never)

	table := Build(recs, historyReadAt)

	small := table.Answer(tier.Small, rigor.Pro)
	if small.Runs != 6 {
		t.Errorf("small runs = %d, want 6: five succeeded and one failed, never the fault or the cancellation", small.Runs)
	}
	// 10..60 minutes: p25/p50/p75 are 22.5/35/47.5, widened to 16.25-53.75.
	if !sameRange(small.Minutes, Range{Low: 16.25, High: 53.75}) {
		t.Errorf("small minutes = %v, want the six samples' 16.25-53.75", small.Minutes)
	}

	large := table.Answer(tier.Large, rigor.Pro)
	if large.Basis != BasisSeed {
		t.Errorf("large basis = %q, want %q: three usable runs are under MinRuns", large.Basis, BasisSeed)
	}
	if want := (Range{Low: 120, High: 480}); !sameRange(large.Minutes, want) {
		t.Errorf("large minutes = %v, want the seed %v", large.Minutes, want)
	}
}

// TestTableCacheRoundTrip: the file is the table. `fugaro plan status` writes
// what it read from the runs bucket and the commands that may not reach the
// network read it back, so a round trip has to be lossless, the file has to be
// private, and a file this release does not read has to be refused rather than
// half-read.
func TestTableCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	getenv := func(key string) string {
		if key == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
	path, err := CachePath(getenv, "linux")
	if err != nil {
		t.Fatalf("CachePath = %v", err)
	}
	if want := filepath.Join(dir, "fugaro", "estimate", "history.json"); path != want {
		t.Errorf("CachePath = %q, want %q", path, want)
	}

	recs := historySeries(tier.Medium, rigor.Pro, runstore.StatusSucceeded,
		[]float64{10, 20, 30, 40, 50}, []float64{1, 2, 3, 4, 5})
	table := Build(recs, historyReadAt)
	if err := table.WriteCache(path); err != nil {
		t.Fatalf("WriteCache = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s is mode %o, want 600", path, perm)
	}

	back, err := ReadCache(path)
	if err != nil {
		t.Fatalf("ReadCache = %v", err)
	}
	if !reflect.DeepEqual(back, table) {
		t.Errorf("the cache round trip lost something:\nread %+v\nwant %+v", back, table)
	}
	if got := back.Answer(tier.Medium, rigor.Pro); !sameRange(got.Minutes, Range{Low: 15, High: 45}) {
		t.Errorf("the table read back answers %v, want 15-45", got.Minutes)
	}

	t.Run("a file that is not there is no cache", func(t *testing.T) {
		_, err := ReadCache(filepath.Join(dir, "fugaro", "estimate", "absent.json"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("ReadCache of a missing file = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("a version this release does not read is refused", func(t *testing.T) {
		bumped := filepath.Join(dir, "bumped.json")
		body := `{"schema_version":` + strconv.Itoa(CacheVersion+1) + `,"written_at":"2026-11-16T12:00:00Z","groups":[]}`
		if err := os.WriteFile(bumped, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadCache(bumped); err == nil {
			t.Fatal("ReadCache accepted a cache from another release")
		}
	})

	t.Run("an unknown field is refused", func(t *testing.T) {
		strange := filepath.Join(dir, "strange.json")
		body := `{"schema_version":1,"written_at":"2026-11-16T12:00:00Z","groups":[],"future":true}`
		if err := os.WriteFile(strange, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadCache(strange); err == nil {
			t.Fatal("ReadCache accepted a field this release does not read")
		}
	})
}
