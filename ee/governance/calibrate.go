// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package governance

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/tier"
)

// The tier calibration: what the runs of a tier really cost and took against
// what they were expected to, and the default a weekly report suggests for them
// (docs/task-governance/implementation-plan.md T8.3, spec §6, D527, D530).
//
// It is born here and reaches the report through the digest's extra-section
// hook (E1.2, design ee-boundary-moves.md §5.4): the digest stays MIT, carries
// the per-run figures a calibration reads and renders none of them itself, and
// an untagged build prints the report it printed before the hook existed
// (D521, D528). The other half of T8.3 -- the expected-scope drift flag -- is
// the core's, because it halts nothing and a report line needs no licence.
//
// Nothing here is applied (G9). No key of the governance is written, no run
// reads a suggestion, and governance.Defaults answers the figures it shipped
// after any number of reports: the tier a run is dispatched at is the
// launcher's decision (spec §3), and a suggestion is a figure a person writes
// into the policy.

// The window a calibration reads and the bounds it suggests within (plan
// T8.3).
const (
	// WindowRuns is the most runs of one tier a calibration reads: the last
	// of them, so a tier that changed shape is calibrated on what it does now.
	WindowRuns = 20
	// WindowDays is how far back from that tier's newest run it reads. The two
	// are a window and not a requirement: whichever holds fewer runs is the one
	// that counts.
	WindowDays = 14
	// SuggestionFloor and SuggestionCeiling bound how far a suggestion may
	// move from the expectation the window's runs carried. A ratio outside
	// them is a window too small or a tier being used for work it is not for,
	// and moving a default further on that evidence would be guessing (G9).
	SuggestionFloor   = 0.5
	SuggestionCeiling = 2.0
)

// Calibration is one tier's window and the suggestion it gives.
//
// Every field is a figure or a value of the closed tier vocabulary, so a
// section built from it carries nothing a record wrote (G1).
type Calibration struct {
	// Tier is the tier, one of tier.Tiers: the rows print in the vocabulary's
	// order.
	Tier tier.Tier
	// Runs is how many runs the window held for the tier, after the
	// governance-halted ones were left out (spec §6): what a tripwire stopped
	// is the tripwire's figure and not the tier's, so counting it would
	// calibrate a tier on runs that were never allowed to run as one.
	Runs int
	// ExpectedUSD and ExpectedMinutes are the median expectation the window's
	// runs carried: the base a suggestion is a multiple of. The runs' own
	// expectation and not the figure Fugaro ships, because a repository whose
	// layer tightened a tier asked for that figure and is calibrated against
	// it. Zero when no run of the window carried one.
	ExpectedUSD     budget.Micros
	ExpectedMinutes int64
	// CostRatio and DurationRatio are the medians of the runs' own
	// actual/expected ratios, and CostKnown and DurationKnown say whether the
	// window held a figure to take one from (G5): a tier whose runs recorded
	// no duration has no duration ratio, which is not a ratio of zero, and its
	// suggestion is the base it already carried.
	CostRatio     float64
	CostKnown     bool
	DurationRatio float64
	DurationKnown bool
	// SuggestedUSD and SuggestedMinutes are the tier's expectations moved by
	// those ratios and held inside the bounds. A suggestion, and nothing else
	// (G9): it is printed, never written.
	SuggestedUSD     budget.Micros
	SuggestedMinutes int64
}

// Sections is the digest's extra-section hook as a tagged build registers it
// (E1.2, design §5.4): the tier calibration of a weekly report, and no section
// at all for any other period.
//
// The weekly cadence is the plan's (T8.3). A day's report holds a handful of
// runs and a month's or a year's holds a window no longer than this one, so
// the same suggestion would print under a period it is not a figure of; the
// window is what the section is about, and a week is the period that shows it
// without repeating it.
func Sections(d digest.Digest) []digest.Section {
	if d.Period.Dim != digest.ByWeek {
		return nil
	}
	cal := Calibrate(d)
	if len(cal) == 0 {
		return nil
	}
	return []digest.Section{{Title: "Tier calibration", Body: calibrationMarkdown(cal)}}
}

// Calibrate is the window's calibration, one row per tier the window holds
// runs of, in the vocabulary's order (plan T8.3, spec §6).
//
// The window is the report's own expectations -- every run of the period that
// carried a tier -- read per tier: the plan's window is "per tier, over the
// last 20 runs or 14 days", so each tier is calibrated on its own runs and on
// none of another's. One window cut over every tier's expectations at once
// would be a window of the report rather than of a tier: a busy tier would
// push a quiet one out of the section entirely, and the busy one would be
// calibrated on fewer runs than its own last twenty.
//
// A tier the window holds no run of gets no row: there is nothing to calibrate
// it from, and a suggestion invented from nothing would be a figure no run
// produced.
func Calibrate(d digest.Digest) []Calibration {
	out := make([]Calibration, 0, len(tier.Tiers))
	for _, t := range tier.Tiers {
		if window := calibrateWindow(tierRuns(d.Expectations, t)); len(window) > 0 {
			out = append(out, calibrate(t, window))
		}
	}
	return out
}

// tierRuns is the digest's expectations of tier t, in finish order: the field
// is written in that order (digest.Build), and "the last" a window keeps is a
// fact of the runs rather than of the order a caller handed them over in (G3),
// so the order is made here instead of trusted.
func tierRuns(exp []digest.Expectation, t tier.Tier) []digest.Expectation {
	var out []digest.Expectation
	for _, e := range exp {
		if e.Tier == string(t) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b digest.Expectation) int { return a.Finished.Compare(b.Finished) })
	return out
}

// calibrateWindow is one tier's runs narrowed to its window: the runs no older
// than WindowDays before the tier's newest one, and of those the last
// WindowRuns. The two are a window and not a requirement: whichever holds
// fewer runs is the one that counts.
//
// The governance-halted runs are left out before the sample is cut rather than
// after: the window bounds the sample a median is taken over, and a window of
// 20 runs of which 8 were halted would otherwise be a median of 12. The 14 days
// are measured back from the tier's newest run, halted or not, because what
// ages is the tier's own last use of it and a tripwire stopping a run does not
// move that.
func calibrateWindow(runs []digest.Expectation) []digest.Expectation {
	if len(runs) == 0 {
		return nil
	}
	cutoff := runs[len(runs)-1].Finished.AddDate(0, 0, -WindowDays)
	kept := make([]digest.Expectation, 0, len(runs))
	for _, e := range runs {
		if !e.Governance && !e.Finished.Before(cutoff) {
			kept = append(kept, e)
		}
	}
	if len(kept) > WindowRuns {
		kept = kept[len(kept)-WindowRuns:]
	}
	return kept
}

// calibrate is one tier's runs as its row: the medians of what the runs were
// expected to cost and take, of what they really did against those
// expectations, and of where a default should sit.
//
// A run that carried no expectation of its own is not a ratio and not part of
// a base: a zero expectation is the absence of one (G5), and dividing by it
// would read a run nobody expected anything of as an infinite overspend.
func calibrate(t tier.Tier, runs []digest.Expectation) Calibration {
	c := Calibration{Tier: t, Runs: len(runs)}

	var (
		baseUSD []budget.Micros
		ratios  []float64
	)
	for _, e := range runs {
		if e.ExpectedUSD > 0 {
			baseUSD = append(baseUSD, e.ExpectedUSD)
			ratios = append(ratios, float64(e.Billed)/float64(e.ExpectedUSD))
		}
	}
	if len(ratios) > 0 {
		c.ExpectedUSD = median(baseUSD)
		c.CostRatio, c.CostKnown = median(ratios), true
		c.SuggestedUSD = suggest(c.ExpectedUSD, c.CostRatio)
	}

	var (
		baseMinutes []int64
		durRatios   []float64
	)
	for _, e := range runs {
		if e.ExpectedMinutes > 0 && e.DurationKnown {
			baseMinutes = append(baseMinutes, e.ExpectedMinutes)
			durRatios = append(durRatios, e.Duration.Minutes()/float64(e.ExpectedMinutes))
		}
	}
	if len(durRatios) > 0 {
		c.ExpectedMinutes = median(baseMinutes)
		c.DurationRatio, c.DurationKnown = median(durRatios), true
		c.SuggestedMinutes = suggestMinutes(c.ExpectedMinutes, c.DurationRatio)
	}
	return c
}

// suggest is base moved by ratio and held inside the bounds, in the unit base
// is in. A ratio at or past a bound is the bound: the window is evidence about
// the figure it holds and not a reason to move a default further than the
// bound allows (G9).
//
// The figure is exact and not rounded to whole cents, which is the report's own
// money renderer and the section's own claim: a base of $0.25 held at the floor
// is $0.125, and a suggestion rounded up to $0.13 would be one the bounds the
// section prints above it do not hold.
func suggest(base budget.Micros, ratio float64) budget.Micros {
	if base <= 0 {
		return 0
	}
	return budget.Micros(math.Round(float64(base) * boundedRatio(ratio)))
}

// suggestMinutes is suggest for a duration in whole minutes, never zero: a
// tier's expectation of nothing is no expectation at all, and a suggestion of
// it would be a tier that expects a run to be instant.
func suggestMinutes(base int64, ratio float64) int64 {
	if base <= 0 {
		return 0
	}
	return max(int64(math.Round(float64(base)*boundedRatio(ratio))), 1)
}

// boundedRatio is ratio held inside SuggestionFloor..SuggestionCeiling.
func boundedRatio(ratio float64) float64 {
	return math.Min(math.Max(ratio, SuggestionFloor), SuggestionCeiling)
}

// median is the median of vs, which must not be empty: the middle value, or
// the mean of the two middle ones when the count is even. The mean of two
// integers is integer division, which is the nearest unit the median of a
// figure can be.
func median[T ~int64 | ~float64](vs []T) T {
	s := slices.Clone(vs)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// calibrationMarkdown is the section's body: the window and the bounds it
// suggests within, the rows, and the line that says what a suggestion is.
func calibrationMarkdown(cal []Calibration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Suggested tier defaults from the last %d runs or %d days of this report, whichever is fewer, by the median actual/expected of each tier's own runs. Runs a governance tripwire stopped are left out, and every suggestion is held between %gx and %gx the expectation those runs carried. Nothing is applied: a suggestion is a figure a person writes into the policy, and no run reads it (G9).\n\n",
		WindowRuns, WindowDays, SuggestionFloor, SuggestionCeiling)
	b.WriteString("| tier | runs | cost | duration | expected | suggested |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, c := range cal {
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s |\n",
			c.Tier, c.Runs,
			ratio(c.CostRatio, c.CostKnown), ratio(c.DurationRatio, c.DurationKnown),
			tierFigure(c.ExpectedUSD, c.ExpectedMinutes, c.CostKnown, c.DurationKnown),
			tierFigure(c.SuggestedUSD, c.SuggestedMinutes, c.CostKnown, c.DurationKnown))
	}
	return b.String()
}

// ratio is one of the row's ratios, or "n/a" when the window held no figure to
// take one from: an absent ratio is printed as absent and never as zero (G5).
func ratio(r float64, known bool) string {
	if !known {
		return "n/a"
	}
	return fmt.Sprintf("%.2fx", r)
}

// tierFigure is a tier's cost and duration as the table's cell: the dollars
// and the minutes, each "n/a" when the window held no figure for it.
func tierFigure(usd budget.Micros, minutes int64, costKnown, durationKnown bool) string {
	cost := "n/a"
	if costKnown {
		cost = digest.USD(usd)
	}
	dur := "n/a"
	if durationKnown {
		dur = fmt.Sprintf("%dm", minutes)
	}
	return cost + " / " + dur
}
