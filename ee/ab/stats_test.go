// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package ab

// The figures an A/B report is read through, and the rule that turns them into
// a recommendation (plan task A4.2, D563, D565).
//
// Every value pinned below is a published table value, an exact closed form or
// the interval's own defining equation -- never this implementation's own
// output. A known-value test written from the code it tests pins nothing: it
// agrees with whatever the code does, including a wrong figure.
//
// What is not a test of its own is asserted where it belongs: that the rule
// reads only the decisive pairs and the prices (TestRecommendKeep), that the
// correction is Holm's and not a flat Bonferroni (TestHolmOrdering), and that
// nothing in the chain draws a random number or reads a map (TestReportDeterministic).

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/stats"
)

// signTestCases is the p column of the sign test: 2 * sum_{i<=min(wins,losses)}
// C(n,i) / 2^n, capped at 1. The first rows are exact fractions of a power of
// two, so they are the same figure on every machine; the rest carry the
// published four decimals and the exact tail above them.
var signTestCases = []struct {
	what         string
	wins, losses int
	p            float64
}{
	{"no pairs", 0, 0, 1},
	{"the smallest possible split", 1, 0, 1}, // 2 * (1/2), capped
	{"an even split", 4, 4, 1},
	{"an even split at the minimum", 10, 10, 1},
	{"ten of ten", 10, 0, 0.001953125},                    // 2 / 2^10
	{"nine of ten", 9, 1, 0.021484375},                    // 2 * 11 / 2^10
	{"eight of ten", 8, 2, 0.109375},                      // 2 * 56 / 2^10
	{"seven of ten", 7, 3, 0.34375},                       // 2 * 176 / 2^10
	{"all twenty", 20, 0, 1.9073486328125e-06},            // 2 / 2^20
	{"nineteen of twenty", 19, 1, 4.00543212890625e-05},   // 2 * 21 / 2^20
	{"seventeen of twenty", 17, 3, 0.0025768280029296875}, // 2 * 1351 / 2^20
	{"sixteen of twenty", 16, 4, 0.01181793212890625},     // 2 * 6196 / 2^20
	{"fifteen of twenty", 15, 5, 0.04138946533203125},     // 2 * 21700 / 2^20
	{"fourteen of twenty", 14, 6, 0.11531829833984375},
	{"twelve of twenty", 12, 8, 0.5034446716308594},
}

// TestSignTestKnownValues pins the p-value against the exact binomial tail.
func TestSignTestKnownValues(t *testing.T) {
	for _, c := range signTestCases {
		got, _, _ := SignTest(c.wins, c.losses)
		if math.Abs(got-c.p) > 1e-12 {
			t.Errorf("SignTest(%d, %d) p = %.17g, want %.17g (%s)", c.wins, c.losses, got, c.p, c.what)
		}
		// Neither count names an arm, so the other side's p is the same
		// figure: a test that only ever hands in the winning side would not
		// notice a p-value built from the wrong count.
		if other, _, _ := SignTest(c.losses, c.wins); math.Abs(other-got) > 1e-12 {
			t.Errorf("SignTest(%d, %d) p = %v, but SignTest(%d, %d) p = %v: the sign test is two-sided",
				c.losses, c.wins, other, c.wins, c.losses, got)
		}
	}
}

// TestClopperPearsonKnownValues pins both interval endpoints against published
// Clopper-Pearson tables (to their published four decimals), against the closed
// form the two extreme splits have, and against the tail equations that define
// them.
func TestClopperPearsonKnownValues(t *testing.T) {
	cases := []struct {
		n, wins int
		lo, hi  float64
		source  string
	}{
		{10, 0, 0, 0.3084971078187607, "the closed form"},
		{10, 1, 0.0025285785444618125, 0.4450161170281953, "a table"},
		{10, 2, 0.025210726326833355, 0.5560954623076415, "a table"},
		{10, 5, 0.1870860284473988, 0.8129139715526015, "a table"},
		{10, 10, 0.6915028921812394, 1, "the closed form"},
		{20, 0, 0, 0.1684334709830853, "the closed form"},
		{20, 5, 0.0865714691014346, 0.49104587170795744, "a table"},
		{20, 10, 0.27195784956079205, 0.7280421504392083, "a table"},
		{20, 12, 0.3605425873074898, 0.8088099392746928, "a table"},
		{20, 13, 0.40781146546717195, 0.8460907952154588, "a table"},
		{20, 15, 0.5089541282920427, 0.9134285308985655, "a table"},
		{20, 18, 0.6830172859809178, 0.9876514728297052, "a table"},
		{30, 21, 0.5060409585374464, 0.8526548152452988, "a table"},
		{100, 60, 0.49720915042233493, 0.6967052312971227, "a table"},
	}
	for _, c := range cases {
		_, lo, hi := SignTest(c.wins, c.n-c.wins)
		if math.Abs(lo-c.lo) > 1e-9 || math.Abs(hi-c.hi) > 1e-9 {
			t.Errorf("SignTest(%d, %d) = [%.17g, %.17g], want [%.17g, %.17g] (%s)",
				c.wins, c.n-c.wins, lo, hi, c.lo, c.hi, c.source)
		}
	}
	// The two extreme splits are exact rather than tabulated: lo for n wins of
	// n is the win share whose upper tail is Alpha/2, which is 0.025^(1/n),
	// and hi for no win at all is its mirror image. Pinned to floating point,
	// not to a table's four decimals.
	for _, c := range []struct {
		n, wins int
		want    float64
		which   string
	}{
		{10, 10, math.Pow(0.025, 1.0/10), "lo"},
		{20, 20, math.Pow(0.025, 1.0/20), "lo"},
		{10, 0, 1 - math.Pow(0.025, 1.0/10), "hi"},
		{20, 0, 1 - math.Pow(0.025, 1.0/20), "hi"},
	} {
		_, lo, hi := SignTest(c.wins, c.n-c.wins)
		got := lo
		if c.which == "hi" {
			got = hi
		}
		if math.Abs(got-c.want) > 1e-12 {
			t.Errorf("SignTest(%d, %d) %s = %.17g, want %.17g", c.wins, c.n-c.wins, c.which, got, c.want)
		}
	}
	// The definition rather than a table: lo is the win share whose upper tail
	// is Alpha/2, and hi the one whose lower tail is Alpha/2. binomTailBelow
	// is written the plain way -- integer coefficients, powers of p -- so this
	// reads the interval's definition and not the implementation above it.
	for _, c := range []struct{ n, wins int }{{20, 5}, {20, 15}, {10, 2}, {30, 21}} {
		_, lo, hi := SignTest(c.wins, c.n-c.wins)
		if got := binomTailBelow(c.n, c.wins-1, lo); math.Abs(got-(1-Alpha/2)) > 1e-9 {
			t.Errorf("P(X <= %d | n=%d, p=lo=%.17g) = %.17g, want %.17g: lo is where the upper tail is Alpha/2",
				c.wins-1, c.n, lo, got, 1-Alpha/2)
		}
		if got := binomTailBelow(c.n, c.wins, hi); math.Abs(got-Alpha/2) > 1e-9 {
			t.Errorf("P(X <= %d | n=%d, p=hi=%.17g) = %.17g, want %.17g: hi is where the lower tail is Alpha/2",
				c.wins, c.n, hi, got, Alpha/2)
		}
	}
	// Wins and losses of 0 are a group with nothing to read: the interval is
	// the whole of [0,1], and nothing divided by a zero pair count reached it.
	if p, lo, hi := SignTest(0, 0); p != 1 || lo != 0 || hi != 1 {
		t.Errorf("SignTest(0, 0) = (%v, %v, %v), want (1, 0, 1)", p, lo, hi)
	}
}

// binomTailBelow is P(X <= k) for X ~ Binomial(n, p), computed the plain way:
// exact integer coefficients and floating-point powers. It exists so the
// defining equations above are checked against a second, independent reading of
// the same distribution, and it is the test's own -- the code under test sums in
// log space and bisects.
func binomTailBelow(n, k int, p float64) float64 {
	sum := 0.0
	for i := 0; i <= k; i++ {
		sum += float64(binomCoefficient(n, i)) * math.Pow(p, float64(i)) * math.Pow(1-p, float64(n-i))
	}
	return sum
}

func binomCoefficient(n, k int) int {
	c := 1
	for i := 1; i <= k; i++ {
		c = c * (n - i + 1) / i
	}
	return c
}

// TestHolmOrdering pins Holm's step-down correction by its ordering: the
// smallest p is read against Alpha/m, the next against Alpha/(m-1) and so on,
// and the first failure stops the walk. The p-values are the sign test's own
// (TestSignTestKnownValues), so what is pinned is a report's real input.
func TestHolmOrdering(t *testing.T) {
	// Sixteen of twenty pairs is 0.0118 and eighteen of twenty is 0.0004; the
	// group at 0.0414 would clear Alpha on its own (0.0414 < 0.05) and is still
	// not called a result, because the walk stopped at it. The group at 0.0118
	// is called one although a flat Bonferroni (Alpha/5 = 0.01) would not:
	// that difference is the whole reason the correction is Holm's.
	ps := []float64{0.5034446716308594, 0.01181793212890625, 1, 0.0004024505615234375, 0.04138946533203125}
	want := []bool{false, true, false, true, false}
	got := Holm(ps)
	if !slices.Equal(got, want) {
		t.Errorf("Holm(%v) = %v, want %v", ps, got, want)
	}
	// The step-down property itself, on the same input: every hypothesis under
	// its own threshold is rejected, and nothing after the first failure is. A
	// test that only compared against a table could be passed by an
	// implementation reading every p against Alpha/m.
	order := make([]int, len(ps))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(ps[a], ps[b]) })
	for i, idx := range order {
		threshold := Alpha / float64(len(ps)-i)
		if ps[idx] <= threshold && !got[idx] {
			t.Errorf("Holm(%v) keeps p = %v, under its threshold %v", ps, ps[idx], threshold)
		}
		if ps[idx] > threshold {
			for _, later := range order[i:] {
				if got[later] {
					t.Errorf("Holm(%v) rejects p = %v after p = %v failed its threshold %v", ps, ps[later], ps[idx], threshold)
				}
			}
		}
	}
	// Holm never rejects less than a flat Bonferroni at the same level, which
	// is what makes the correction usable: it is that bound, not a weaker one.
	for i, p := range ps {
		if p <= Alpha/float64(len(ps)) && !got[i] {
			t.Errorf("Holm(%v) keeps p = %v at index %d, which even a flat Bonferroni rejects", ps, p, i)
		}
	}
	// One group, one threshold: Alpha itself, not Alpha/1 of something else. An
	// empty input is an empty answer and not a panic.
	if got := Holm([]float64{0.04}); !slices.Equal(got, []bool{true}) {
		t.Errorf("Holm([0.04]) = %v, want [true]", got)
	}
	if got := Holm([]float64{0.06}); !slices.Equal(got, []bool{false}) {
		t.Errorf("Holm([0.06]) = %v, want [false]", got)
	}
	if got := Holm(nil); len(got) != 0 {
		t.Errorf("Holm(nil) = %v, want no hypotheses", got)
	}
}

// TestBelowMinimumNoConclusion pins D563's gate: fewer than MinRuns decisive
// pairs is no conclusion, however lopsided -- a perfect nineteen is not a
// result, and the twentieth pair makes the same evidence one.
func TestBelowMinimumNoConclusion(t *testing.T) {
	// D563's minimum is internal/stats' convention, the figure a thin group is
	// suppressed rather than approximated at (F15). Pinned equal here rather
	// than imported there: the two packages answer different questions, and a
	// drift between them would be two reports disagreeing about what a sample
	// is.
	if MinRuns != stats.MinRuns {
		t.Errorf("MinRuns = %d, want internal/stats' %d: D563 reuses the minimum-sample convention", MinRuns, stats.MinRuns)
	}
	below := Summary{Wins: MinRuns - 1, CostRatios: []float64{0.5}}
	rec := Recommend(below, 0)
	if rec.Decision != DecisionNoConclusion || rec.Rule != RuleBelowMinimum {
		t.Errorf("Recommend(%d wins, 0 losses) = %s/%s, want %s/%s: a conclusion needs %d decisive pairs",
			below.Wins, rec.Decision, rec.Rule, DecisionNoConclusion, RuleBelowMinimum, MinRuns)
	}
	if rec.Decisive != below.Wins {
		t.Errorf("Recommend(%d wins, 0 losses).Decisive = %d, want %d", below.Wins, rec.Decisive, below.Wins)
	}
	// The conclusion is withheld, not the arithmetic: a report shows the
	// interval beside the conclusion it is too wide to carry.
	if _, lo, hi := SignTest(below.Wins, 0); rec.Lo != lo || rec.Hi != hi {
		t.Errorf("Recommend(%d wins, 0 losses) = [%v, %v], want the group's own [%v, %v]",
			below.Wins, rec.Lo, rec.Hi, lo, hi)
	}
	at := Summary{Wins: MinRuns, CostRatios: []float64{0.5}}
	rec = Recommend(at, 0)
	if rec.Decision != DecisionChallenger || rec.Rule != RuleBetterWithinRate {
		t.Errorf("Recommend(%d wins, 0 losses, half price, rate 0) = %s/%s, want %s/%s: the twentieth decisive pair is a sample",
			MinRuns, rec.Decision, rec.Rule, DecisionChallenger, RuleBetterWithinRate)
	}
}

// TestRecommendCheaperNotWorse pins D565's first region: the challenger is not
// worse -- the upper bound of its loss share below LossTolerance -- and cheaper.
func TestRecommendCheaperNotWorse(t *testing.T) {
	// Thirteen of twenty: lo = 0.40781, so the challenger's loss share is at
	// most 0.59219, just inside the 0.6 tolerance. lo is below a half, so the
	// affirmative region cannot be the one that fired.
	rec := Recommend(Summary{Wins: 13, Losses: 7, CostRatios: []float64{0.9, 0.8, 0.85}}, 0)
	if rec.Decision != DecisionChallenger || rec.Rule != RuleCheaperNotWorse {
		t.Errorf("thirteen of twenty at 0.85 of the price = %s/%s, want %s/%s",
			rec.Decision, rec.Rule, DecisionChallenger, RuleCheaperNotWorse)
	}
	if math.Abs(rec.CostRatio-0.85) > 1e-12 {
		t.Errorf("CostRatio = %v, want the median of the three paired ratios: 0.85", rec.CostRatio)
	}
	// The loss bound is read at the tolerance and not at a half: the same
	// thirteen of twenty with the same saving is what the rule is for, while
	// twelve of twenty (1-lo = 0.639) is over it and half the price does not
	// change that.
	rec = Recommend(Summary{Wins: 12, Losses: 8, CostRatios: []float64{0.5, 0.5}}, 0)
	if rec.Decision != DecisionKeep || rec.Rule != RuleKeep {
		t.Errorf("twelve of twenty at half the price = %s/%s, want %s/%s: the bound on the loss share is over the tolerance",
			rec.Decision, rec.Rule, DecisionKeep, RuleKeep)
	}
	// A saving is not a saving at the same price: 1 is not below 1.
	if rec := Recommend(Summary{Wins: 13, Losses: 7, CostRatios: []float64{1, 1, 1}}, 0); rec.Decision != DecisionKeep {
		t.Errorf("thirteen of twenty at the champion's own price = %s/%s, want %s: the same price is no reason to switch",
			rec.Decision, rec.Rule, DecisionKeep)
	}
}

// TestRecommendBetterWithinRate pins D565's second region: the challenger is
// clearly better -- the lower bound of its win share above a half -- and its
// median extra cost per pair is within the mode's exchange rate.
func TestRecommendBetterWithinRate(t *testing.T) {
	// Eighteen of twenty: lo = 0.68302. At Pro's 0.5, 1.4 is 40% more per pair.
	rec := Recommend(Summary{Wins: 18, Losses: 2, CostRatios: []float64{1.4}}, 0.5)
	if rec.Decision != DecisionChallenger || rec.Rule != RuleBetterWithinRate {
		t.Errorf("eighteen of twenty at 1.4x and rate 0.5 = %s/%s, want %s/%s",
			rec.Decision, rec.Rule, DecisionChallenger, RuleBetterWithinRate)
	}
	// Critical's 2.0 accepts up to three times the champion's price and no
	// more: 2.9 is 1.9 extra, 3.1 is 2.1.
	if rec := Recommend(Summary{Wins: 18, Losses: 2, CostRatios: []float64{2.9}}, 2.0); rec.Decision != DecisionChallenger {
		t.Errorf("eighteen of twenty at 2.9x and rate 2.0 = %s/%s, want the challenger", rec.Decision, rec.Rule)
	}
	if rec := Recommend(Summary{Wins: 18, Losses: 2, CostRatios: []float64{3.1}}, 2.0); rec.Decision != DecisionKeep {
		t.Errorf("eighteen of twenty at 3.1x and rate 2.0 = %s/%s, want the champion: 2.1 extra is over the rate",
			rec.Decision, rec.Rule)
	}
	// A rate of 0 is "no extra cost is worth it", not "never": a challenger
	// that is clearly better and no dearer still wins this region.
	if rec := Recommend(Summary{Wins: 18, Losses: 2, CostRatios: []float64{1}}, 0); rec.Decision != DecisionChallenger {
		t.Errorf("eighteen of twenty at the same price and rate 0 = %s/%s, want the challenger", rec.Decision, rec.Rule)
	}
	// Both regions describe eighteen of twenty at 0.8 of the price. The
	// affirmative one is reported: it is the stronger claim, and a reader of
	// the price region knows its evidence was only "not worse".
	rec = Recommend(Summary{Wins: 18, Losses: 2, CostRatios: []float64{0.8}}, 0.5)
	if rec.Decision != DecisionChallenger || rec.Rule != RuleBetterWithinRate {
		t.Errorf("eighteen of twenty at 0.8x and rate 0.5 = %s/%s, want %s/%s",
			rec.Decision, rec.Rule, DecisionChallenger, RuleBetterWithinRate)
	}
	// The rate is the extra cost as a fraction of the champion's, and it is a
	// parameter because it belongs to the mode: one group, two rates, two
	// answers (D565's defaults are config.ABExchangeRateDefault).
	if rec := Recommend(Summary{Wins: 18, Losses: 2, CostRatios: []float64{1.4}}, 0.25); rec.Decision != DecisionKeep {
		t.Errorf("eighteen of twenty at 1.4x and rate 0.25 = %s/%s, want the champion", rec.Decision, rec.Rule)
	}
}

// TestRecommendKeep pins the third region, and the figures the rule refuses to
// read rather than read as favourable.
func TestRecommendKeep(t *testing.T) {
	// No pair carrying both arms' cost is not a cheap pair: with no ratio to
	// read, the challenger's price is unknown, and an unknown price is not one
	// this rule recommends a change of default on.
	rec := Recommend(Summary{Wins: 18, Losses: 2}, 2.0)
	if rec.Decision != DecisionKeep || rec.Rule != RuleKeep || rec.CostRatio != 0 {
		t.Errorf("eighteen of twenty with no cost figure = %s/%s at ratio %v, want %s/%s at 0",
			rec.Decision, rec.Rule, rec.CostRatio, DecisionKeep, RuleKeep)
	}
	// A ratio that is not a finite number -- a pair whose champion cost 0 -- is
	// skipped for the same reason, never read as an infinite saving.
	if rec := Recommend(Summary{Wins: 18, Losses: 2, CostRatios: []float64{math.Inf(1), math.NaN()}}, 2.0); rec.Decision != DecisionKeep {
		t.Errorf("eighteen of twenty with no finite cost ratio = %s/%s, want %s", rec.Decision, rec.Rule, DecisionKeep)
	}
	// The halts are context the report shows and the rule never reads: a halt
	// is already a loss among the decisive pairs (D567), so moving the counts
	// must not move the recommendation.
	base := Summary{Wins: 13, Losses: 7, CostRatios: []float64{0.9, 0.8, 0.85}}
	withHalts := base
	withHalts.ChampionHalts, withHalts.ChallengerHalts = 9, 2
	if a, b := Recommend(base, 0), Recommend(withHalts, 0); a != b {
		t.Errorf("halts changed the recommendation: %+v, then %+v", a, b)
	}
	// A tie is not a decisive pair and cannot move a decision either (D561).
	withTies := base
	withTies.Ties = 40
	if a, b := Recommend(base, 0), Recommend(withTies, 0); a != b {
		t.Errorf("ties changed the recommendation: %+v, then %+v", a, b)
	}
}

// TestReportDeterministic pins the other half of the plan's constraint: the
// same records give the same report. Nothing in the chain may depend on a map's
// iteration order, a random number or a clock, so the same input is compared
// bit for bit and the order-independence is checked rather than assumed.
func TestReportDeterministic(t *testing.T) {
	type group struct {
		name string
		sum  Summary
		rate float64
	}
	groups := []group{
		{"pro/small", Summary{Wins: 15, Losses: 5, Ties: 3, CostRatios: []float64{0.8, 1.4, 0.35}, ChampionHalts: 1, ChallengerHalts: 4}, 0.5},
		{"pro/medium", Summary{Wins: 4, Losses: 6, Ties: 1, CostRatios: []float64{1.1}, ChampionHalts: 2}, 0.5},
		{"poc", Summary{Wins: 18, Losses: 2, CostRatios: []float64{0.9, 0.95}, ChallengerHalts: 1}, 0},
		{"critical", Summary{Wins: 9, Losses: 11, Ties: 5, CostRatios: []float64{2.7, 3.5}}, 2.0},
	}
	read := func(in []group) []string {
		ps := make([]float64, 0, len(in))
		for _, g := range in {
			p, _, _ := SignTest(g.sum.Wins, g.sum.Losses)
			ps = append(ps, p)
		}
		holm := Holm(ps)
		rows := make([]string, 0, len(in))
		for i, g := range in {
			rec := Recommend(g.sum, g.rate)
			rows = append(rows, fmt.Sprintf("%s|%s|%s|%t|%d|%.17g|%.17g|%.17g|%.17g|%.17g",
				g.name, rec.Decision, rec.Rule, holm[i], rec.Decisive, rec.P, rec.Lo, rec.Hi, rec.CostRatio, rec.Rate))
		}
		slices.Sort(rows)
		return rows
	}
	first := read(groups)
	for i := range 200 {
		if got := read(groups); !slices.Equal(got, first) {
			t.Fatalf("read %d gave %v, but the first read gave %v: the same records must give the same report", i, got, first)
		}
	}
	shuffled := []group{groups[3], groups[0], groups[2], groups[1]}
	if got := read(shuffled); !slices.Equal(got, first) {
		t.Errorf("reading the groups in another order gave %v, want %v", got, first)
	}
	// The floats themselves, bit for bit: two sums over the same terms in
	// another order would print the same to four decimals and differ here.
	for _, g := range groups {
		p1, lo1, hi1 := SignTest(g.sum.Wins, g.sum.Losses)
		p2, lo2, hi2 := SignTest(g.sum.Wins, g.sum.Losses)
		if math.Float64bits(p1) != math.Float64bits(p2) || math.Float64bits(lo1) != math.Float64bits(lo2) || math.Float64bits(hi1) != math.Float64bits(hi2) {
			t.Errorf("%s: two reads of one group gave (%v, %v, %v) then (%v, %v, %v)", g.name, p1, lo1, hi1, p2, lo2, hi2)
		}
		if c1, c2 := MedianRatio(g.sum.CostRatios), MedianRatio(g.sum.CostRatios); math.Float64bits(c1) != math.Float64bits(c2) {
			t.Errorf("%s: two medians of one group gave %v then %v", g.name, c1, c2)
		}
	}
	// Holm's decision belongs to the hypothesis and not to its position: every
	// ordering of one report's p-values gives each group the same answer.
	ps := []float64{0.0004024505615234375, 0.04138946533203125, 0.5034446716308594, 1}
	want := Holm(ps)
	for _, perm := range permutations([]int{0, 1, 2, 3}) {
		got := Holm([]float64{ps[perm[0]], ps[perm[1]], ps[perm[2]], ps[perm[3]]})
		for i, src := range perm {
			if got[i] != want[src] {
				t.Errorf("Holm over %v rejects p = %v at %v, but over %v it does not: the correction is order-independent",
					perm, ps[src], got[i], ps)
			}
		}
	}
}

// permutations is every ordering of idx, so the order-independence above is
// checked on all of them and not on a hand-picked pair.
func permutations(idx []int) [][]int {
	if len(idx) <= 1 {
		return [][]int{slices.Clone(idx)}
	}
	var out [][]int
	for i := range idx {
		rest := slices.Concat(idx[:i], idx[i+1:])
		for _, tail := range permutations(rest) {
			out = append(out, append([]int{idx[i]}, tail...))
		}
	}
	slices.SortFunc(out, func(a, b []int) int {
		for i := range a {
			if c := cmp.Compare(a[i], b[i]); c != 0 {
				return c
			}
		}
		return 0
	})
	return out
}

// TestMedianRatio pins the median of paired cost ratios: the type-7
// interpolation internal/stats uses, so a median means one thing in both
// reports, and no figure at all from a sample with nothing finite in it.
func TestMedianRatio(t *testing.T) {
	cases := []struct {
		what   string
		ratios []float64
		want   float64
	}{
		{"nothing to read", nil, 0},
		{"an empty sample", []float64{}, 0},
		{"one pair", []float64{1.25}, 1.25},
		{"an odd sample", []float64{0.9, 0.4, 1.7}, 0.9},
		{"an even sample", []float64{0.9, 0.4}, 0.65},
		{"ordered or not", []float64{3, 1, 2, 0}, 1.5},
		{"only non-finite", []float64{math.Inf(1), math.Inf(-1), math.NaN()}, 0},
		{"non-finite skipped", []float64{math.Inf(1), 0.5, 0.7}, 0.6},
	}
	for _, c := range cases {
		before := slices.Clone(c.ratios)
		if got := MedianRatio(c.ratios); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("MedianRatio(%v) = %v, want %v (%s)", c.ratios, got, c.want, c.what)
		}
		if !slices.Equal(c.ratios, before) {
			t.Errorf("%s: MedianRatio modified its input: %v", c.what, c.ratios)
		}
	}
}
