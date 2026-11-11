// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package ab is the model A/B policy: which runs a project samples, which
// challenger may run, how a pair is scored, what its statistics recommend and
// who judges it (docs/model-ab-testing, D550-D573). It is the paid half of the
// feature -- the core keeps the task's and the record's ab block, the shadow
// runner and the hooks, and nothing here is reached by an untagged build.
//
// This file is the statistics and the utility rule (plan task A4.2, D563,
// D565): the figures one group's pairs are read through, and the rule that
// turns them into a recommendation. Nothing here is a writer. R3's "reporting,
// not switching" leaves the recommendation a value a report prints, and no code
// in this package reaches a default or a run.
package ab

import (
	"cmp"
	"math"
	"slices"

	"github.com/dimipaun/fugaro/internal/stats"
)

// Alpha is the level every figure here is read at (D563): the Clopper-Pearson
// intervals are two-sided 95% intervals, the sign test is read at 5%, and
// Holm's correction holds the family-wise error rate at 5% across one report's
// groups. One level rather than one per figure, so a reader comparing a p-value
// with an interval is comparing two readings of the same test.
const Alpha = 0.05

// MinRuns is the least decisive pairs a group may carry a conclusion from
// (D563): internal/stats' minimum-sample figure itself, not a second copy of it,
// so the A/B report and `fugaro stats runs` can never come to disagree about
// what a sample is. That package suppresses a group below it rather than
// approximating one, and this one returns DecisionNoConclusion for the same
// reason. A decisive pair is a win or a loss; a tie says nothing about which arm
// is better, so it is not one, and a group of twenty ties is below the minimum
// however many pairs it ran.
const MinRuns = stats.MinRuns

// LossTolerance is the Clopper-Pearson upper bound of the challenger's share of
// losses among a group's decisive pairs that D565's "not worse" is measured
// against: the price region asks for a bound below it, so a group exactly on it
// keeps the champion. It is a bar a challenger clears to be recommended, and
// deliberately not a demonstration that it loses no more often than the
// champion -- a group just under it is still consistent with a challenger
// losing close to three pairs in five, which is the weak claim "not worse" is,
// and the reason that region also asks for a lower price.
const LossTolerance = 0.6

// SignTest reads a group's decisive pairs. p is the exact two-sided sign test's
// p-value -- the probability, under "either arm takes a pair with probability a
// half", of a split at least as lopsided as wins out of wins+losses -- and lo
// and hi are the Clopper-Pearson 95% interval for the challenger's share of
// those pairs (D563). Both are exact: no normal approximation, no continuity
// correction, no random number, so the same two counts always give the same
// three figures.
//
// lo and hi are what the utility rule reads (D565). The challenger's loss share
// is one minus its win share, so 1-lo is the upper bound on how often it loses,
// and lo itself is the lower bound on how often it wins.
//
// Wins and losses of 0 are a group with nothing to read: p is 1 and the
// interval is the whole of [0,1]. A negative count is a caller's bug and
// panics: a figure drawn from a count that cannot happen would put a number in
// a report that no pair produced.
func SignTest(wins, losses int) (p, lo, hi float64) {
	if wins < 0 || losses < 0 {
		panic("ab.SignTest: negative count: a group's pairs are counted, so a count below zero is a caller's bug and not a thin sample")
	}
	n := wins + losses
	if n == 0 {
		return 1, 0, 1
	}
	p = math.Min(1, 2*binomTail(n, min(wins, losses), 0.5))
	// lo is the win share whose upper tail is Alpha/2: n wins of n is a tail
	// of nothing but the split itself, which is how 0.025^(1/n) falls out. hi
	// is the mirror, and no win at all has no lower bound to find.
	lo = 0
	if wins > 0 {
		lo = tailCrossing(n, wins-1, 1-Alpha/2)
	}
	hi = 1
	if wins < n {
		hi = tailCrossing(n, wins, Alpha/2)
	}
	return p, lo, hi
}

// binomTail is P(X <= k) for X ~ Binomial(n, p): the one tail the sign test's
// p-value and both interval endpoints are read from, since the p-value is twice
// binomTail(n, min(wins,losses), 0.5) and an endpoint is the p where a tail
// crosses half of Alpha.
//
// The terms are carried as logarithms and summed under a running scale, rather
// than in p's own units, for two reasons of arithmetic. The terms of one sum
// span every order of magnitude -- at n = 300 the largest is 0.046 and one a
// hundred and thirty successes into the tail is 3.7e-60 -- and a raw sum would
// drop the small ones against the large rather than add them. And the
// coefficient alone leaves the smallest and largest doubles behind long before
// the term it belongs to does: C(1030,515) is 2.9e308, past the largest double,
// while its term is 0.025. No coefficient is ever formed here.
func binomTail(n, k int, p float64) float64 {
	if k < 0 {
		return 0
	}
	if k >= n {
		return 1
	}
	if p <= 0 {
		return 1
	}
	if p >= 1 {
		return 0
	}
	logp, logq := math.Log(p), math.Log1p(-p)
	// log C(n,i) p^i q^(n-i), carried term by term -- C(n,i+1)/C(n,i) is
	// (n-i)/(i+1) -- and folded into a running sum whose scale is the largest
	// term seen so far, so no term is ever added to one that dwarfs it.
	logTerm := float64(n) * logq
	largest, sum := logTerm, 1.0
	for i := 1; i <= k; i++ {
		logTerm += math.Log(float64(n-i+1)/float64(i)) + logp - logq
		if logTerm > largest {
			sum = sum*math.Exp(largest-logTerm) + 1
			largest = logTerm
		} else {
			sum += math.Exp(logTerm - largest)
		}
	}
	return math.Exp(largest) * sum
}

// tailCrossing is the largest p whose binomTail(n, k, p) is still at least
// target: the tail only falls as p rises, so it is the p where the tail passes
// the target, and the endpoint the interval is defined by. Bisection rather
// than a closed form, because the endpoints must be the inverse of the very
// tail the p-value is drawn from and that inverse has no elementary form. Sixty
// halvings put the bracket far below a double's resolution.
func tailCrossing(n, k int, target float64) float64 {
	lo, hi := 0.0, 1.0
	for range 60 {
		mid := (lo + hi) / 2
		if binomTail(n, k, mid) >= target {
			lo = mid
		} else {
			hi = mid
		}
	}
	// lo is itself a p whose tail is at least target, and the largest one
	// tested: the bracket's midpoint could sit on the far side of the crossing
	// and make an endpoint the interval's own definition does not allow.
	return lo
}

// Holm is Holm's step-down correction across the groups of one report (D563):
// given each group's sign-test p-value, in any order, it reports which of them
// the report may call a result at Alpha's family-wise level. The smallest p is
// read against Alpha/m, the next against Alpha/(m-1) and so on, and the first
// that fails stops the walk: a group is called a result only if it would have
// been one with every larger p taken out of the family.
//
// The correction is what keeps a report of modes × tiers × model pairs honest.
// At a twentieth per group, a report that reads twenty groups finds a winner in
// one of them by chance alone, and the reader has no way to tell that winner
// from a real one.
//
// Each p is a sign test's, in [0,1]. The answer belongs to the hypothesis and
// not to its position: the same p-values in another order give each group the
// same reading, and a tie in p-values is settled by position, which cannot
// change an answer, since equal p-values pass or fail together.
func Holm(ps []float64) []bool {
	order := make([]int, len(ps))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(ps[a], ps[b]) })
	reject := make([]bool, len(ps))
	for i, idx := range order {
		if ps[idx] > Alpha/float64(len(ps)-i) {
			break
		}
		reject[idx] = true
	}
	return reject
}

// MedianRatio is the median of the paired cost ratios the caller passes, one
// per pair, the challenger's dollars over the champion's (D563) -- the figure
// D565's exchange rate is measured against. It is internal/stats' interpolation
// rather than a second copy of one: an even sample's median is the mean of its
// two middle values in both, so a median means one thing in the two reports a
// reader sees.
//
// A ratio that is not a finite number is skipped rather than read: a pair whose
// champion cost 0 has no ratio, and a pair one arm carries no figure for has
// none either. The caller's slice is not modified, and 0 is returned when
// nothing finite is left -- the rule asks HasCost before it reads a 0, so an
// unknown price is never a cheap one.
func MedianRatio(ratios []float64) float64 {
	return stats.PercentilesOf(finiteRatios(ratios)).Median
}

// finiteRatios is the ratios of a group that can be read: one ratio per pair
// whose arms both carry a cost figure, in the caller's order. An empty answer is
// a group with no price at all, which is not the same state as a group whose
// challenger cost nothing -- that one is 0 in every ratio and comes back here as
// a full slice of zeros. The caller tells the two apart by the slice being
// empty, and never by a ratio, since a ratio of 0 is a price.
func finiteRatios(ratios []float64) []float64 {
	xs := make([]float64, 0, len(ratios))
	for _, r := range ratios {
		if !math.IsNaN(r) && !math.IsInf(r, 0) {
			xs = append(xs, r)
		}
	}
	return xs
}

// Summary is one group's evidence: the pairs a report counts and the figures
// the rule reads. A group is a rigor mode × tier × model pair (D560, D564), and
// a summary carries no identifier beyond the two models' own -- no person, no
// task, no run, no repository: what a report is never handed it can never
// publish.
type Summary struct {
	// Wins and Losses are the decisive pairs, from the challenger's side: a
	// pair the challenger took and a pair it lost. A halt of either arm is
	// already one of the two (D567), and so is a pair whose arm never
	// produced a final head (D561).
	Wins, Losses int
	// Ties are the pairs the arms drew on every signal their mode reads
	// (D561). They are counted and shown and never read: a tie says nothing
	// about which arm is better, so no figure below is drawn from one.
	Ties int
	// CostRatios is one ratio per pair that carries both arms' cost, the
	// challenger's dollars over the champion's (D563). The order does not
	// matter, and a pair with no figure for either arm simply has no entry.
	CostRatios []float64
	// ChampionHalts and ChallengerHalts are the halts per arm: context a
	// report shows (D567) and the rule never reads. A halt is already a loss
	// above; this is the figure that says how often the cap, rather than the
	// work, decided a pair, which is what a reader needs to know before
	// acting on a cheaper challenger's halt rate.
	ChampionHalts, ChallengerHalts int
}

// Decision is what the rule advises for one group, in the closed vocabulary the
// report's JSON carries (R3): run the challenger, keep the champion, or say
// nothing at all.
type Decision string

const (
	// DecisionChallenger advises the challenger as the default for this
	// group: it is either clearly better within the mode's exchange rate or
	// not worse at a lower price.
	DecisionChallenger Decision = "challenger"
	// DecisionKeep advises the champion: the challenger is dearer than the
	// mode pays for, or measurably worse.
	DecisionKeep Decision = "keep"
	// DecisionNoConclusion is the answer for a group below MinRuns decisive
	// pairs (D563). It is not DecisionKeep: "the sample cannot say" and "the
	// sample says no" are different answers, and a reader who saw them
	// merged would read the second where the first was meant.
	DecisionNoConclusion Decision = "no-conclusion"
)

// Rule is which region fired, the why beside the decision (R3): a reader needs
// it, because a cheaper challenger and a better one are not the same finding
// even when both say "challenger".
type Rule string

const (
	// RuleCheaperNotWorse is D565's price region: the challenger's loss share
	// is bounded below LossTolerance and it costs less per pair, so it is free
	// quality at worst.
	RuleCheaperNotWorse Rule = "cheaper-not-worse"
	// RuleBetterWithinRate is D565's quality region: the lower bound of the
	// challenger's win share is above a half, and what it costs extra per pair
	// is inside the mode's exchange rate.
	RuleBetterWithinRate Rule = "better-within-rate"
	// RuleKeep is D565's third region: neither of the other two holds, so the
	// champion is the default the pairs do not argue against.
	RuleKeep Rule = "keep"
	// RuleBelowMinimum is the gate before any region: fewer than MinRuns
	// decisive pairs (D563). It is not one of D565's three regions -- it is
	// what stops a region from being read at all.
	RuleBelowMinimum Rule = "below-minimum"
)

// Recommendation is the rule's answer for one group: the figures it was read
// through, so a reader can check the arithmetic, and the rule that fired, so
// the advice is explainable rather than mysterious (D565, R3). It is the object
// a report's JSON carries; nothing here writes it anywhere.
type Recommendation struct {
	// Decision is the advice, Rule the region behind it.
	Decision Decision
	Rule     Rule
	// Decisive is the wins plus losses every figure here was drawn from.
	Decisive int
	// P, Lo and Hi are SignTest's three figures for the group: the exact
	// sign test's p-value and the Clopper-Pearson bounds of the challenger's
	// share of the decisive pairs. They are reported even when the decision is
	// DecisionNoConclusion, where the interval is the reason.
	P, Lo, Hi float64
	// CostRatio is the group's median paired cost ratio, challenger over
	// champion (MedianRatio), and HasCost says whether it is a figure at all:
	// false means no pair carried both arms' costs, which is no price, while
	// true with a ratio of 0 means the challenger was free. The two are read
	// differently by the rule, so they cannot be one value.
	CostRatio float64
	HasCost   bool
	// Rate is the extra cost per pair the group was read at, as a fraction of
	// the champion's cost: the mode's own figure (D565), passed in rather than
	// looked up here, because which mode a group belongs to is the report's
	// business and not this rule's.
	Rate float64
}

// Recommend applies D565's utility rule to one group at one mode's exchange
// rate. The regions, in the order they are read:
//
//   - the challenger is clearly better and its price is within the mode's rate
//     -- the lower bound of its win share above a half, and a median cost ratio
//     no more than 1+rate, rate being the extra cost as a fraction of the
//     champion's;
//   - the challenger is not worse and cheaper -- the upper bound of its loss
//     share (1-Lo) below LossTolerance, and a median cost ratio below 1;
//   - otherwise the champion stays.
//
// The affirmative region is read first: a challenger that is both cheaper and
// better is reported for the better of the two reasons, which is the stronger
// claim, and a reader who sees the price region knows its evidence was "not
// worse" and nothing more.
//
// A rate of 0 -- POC, and the floor of any mode a layer leaves out -- is "no
// extra cost is worth this group", not "never": a challenger that is clearly
// better and no dearer still wins the first region. The same price is not a
// saving, so the price region wants a ratio below 1.
//
// A group with no cost figure keeps the champion whatever its pairs did: an
// unknown price is not a cheap one, and this rule advises a change of default.
// A challenger that cost nothing is the opposite case and not the same one: it
// is priced, at the lowest price there is, so it is read like any other
// challenger and can win either region.
//
// A group below MinRuns decisive pairs gets DecisionNoConclusion and no region
// is read (D563). Every figure is still computed and returned: a report shows
// the interval beside the conclusion it is too narrow a sample to carry.
func Recommend(group Summary, rate float64) Recommendation {
	ratios := finiteRatios(group.CostRatios)
	decide := func(d Decision, r Rule) Recommendation {
		p, lo, hi := SignTest(group.Wins, group.Losses)
		return Recommendation{
			Decision: d, Rule: r,
			Decisive: group.Wins + group.Losses,
			P:        p, Lo: lo, Hi: hi,
			CostRatio: stats.PercentilesOf(ratios).Median,
			HasCost:   len(ratios) > 0,
			Rate:      rate,
		}
	}
	if group.Wins+group.Losses < MinRuns {
		return decide(DecisionNoConclusion, RuleBelowMinimum)
	}
	rec := decide(DecisionKeep, RuleKeep)
	switch {
	case rec.HasCost && rec.Lo > 0.5 && rec.CostRatio-1 <= rate:
		rec.Decision, rec.Rule = DecisionChallenger, RuleBetterWithinRate
	case rec.HasCost && 1-rec.Lo < LossTolerance && rec.CostRatio < 1:
		rec.Decision, rec.Rule = DecisionChallenger, RuleCheaperNotWorse
	}
	return rec
}
