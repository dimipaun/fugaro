// Package stats computes the distributions fugaro stats runs publishes: the
// minimum, the median and the 90th percentile of a run's wall-clock duration
// and of its total cost, grouped by recipe over a window.
//
// It exists for one page (the caps-and-costs guide, website task W1.5) and it
// is written around two rules. No figure is drawn from fewer than MinRuns
// runs: a group below the threshold is suppressed rather than approximated,
// because a range read off four runs reads as a promise precisely because it
// is a range. And no identifier can reach the output: Run carries a recipe
// name, a duration, a cost and whether the compute part is an estimate --
// never a repository, run ID, branch, pull request, task text, person or
// commit -- so the aggregation cannot publish an identifier it was never
// handed.
//
// There is deliberately no mean here, and no field for one: an average of a
// long-tailed distribution describes nobody (reconciliation D97).
package stats

import (
	"maps"
	"math"
	"slices"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// MinRuns is the least runs a group may carry a range from (reconciliation
// D97). It is the flag's default; a caller that means to look at a thinner
// window passes a smaller number deliberately.
const MinRuns = 20

// Run is one run as the aggregation sees it: a recipe name, the wall-clock
// duration, the total cost and whether the compute part is an estimate. It is
// deliberately narrower than runstore.Record -- the caller maps the record
// into it -- so that a field this package never receives is a field it can
// never publish.
type Run struct {
	// Recipe is the run's recipe, as the record names it.
	Recipe string
	// DurationS is the run's wall-clock duration in seconds.
	DurationS float64
	// CostUSD is the run's total billed cost, in USD.
	CostUSD float64
	// ComputeNotEstimated is true when the run's compute cost was not
	// estimated (runstore.Cost.ComputeEstimated is false), so its CostUSD
	// is a lower bound rather than a figure: the count is published beside
	// the range, never folded into it.
	ComputeNotEstimated bool
	// NoFigure is true for a record no figure can be drawn from: a run that
	// has not finished, a record written before the cost breakdown existed,
	// or a result.json that cannot be read at all. It is counted and never
	// grouped -- a sample must not read smaller than it is.
	NoFigure bool
}

// Percentiles are the three figures published for one measure. Min and
// Median and P90, and no mean of any kind: the struct has no field for one.
type Percentiles struct {
	Min    float64 `json:"min"`
	Median float64 `json:"median"`
	P90    float64 `json:"p90"`
}

// Group is one recipe's published figures over the window.
type Group struct {
	// Recipe is the recipe name, one the tree ships: the group key a reader
	// can look up in docs/recipes.md.
	Recipe string
	// Runs is the sample size every figure below was drawn from.
	Runs int
	// ComputeNotEstimated counts the group's runs whose compute cost was
	// not estimated.
	ComputeNotEstimated int
	// DurationS is the run's wall-clock duration, in seconds.
	DurationS Percentiles
	// CostUSD is the run's total cost, in USD.
	CostUSD Percentiles
}

// Result is everything one window yielded. The counts add up:
// sum(Group.Runs) + RunsBelowMinRuns + RunsUnknownRecipe == Runs, and
// RunsWithoutFigure counts the records no figure could be drawn from at all.
type Result struct {
	// Groups are the published groups, by recipe name ascending.
	Groups []Group
	// Runs counts the records a figure could be drawn from: every one the
	// caller handed in that was not NoFigure.
	Runs int
	// RunsBelowMinRuns counts the runs of groups too thin to publish.
	RunsBelowMinRuns int
	// RunsUnknownRecipe counts the runs whose recipe the tree does not
	// ship, so their group key would be a name no reader can look up.
	RunsUnknownRecipe int
	// ComputeNotEstimated counts every run whose compute cost was *not*
	// estimated, published groups or not: it is the count of runs whose
	// CostUSD is a lower bound rather than a measurement.
	ComputeNotEstimated int
	// RunsWithoutFigure counts the records read that carry no figure, the
	// ones that cannot be read at all among them.
	RunsWithoutFigure int
}

// Aggregate groups runs by recipe and computes each group's figures. A group
// with fewer than minRuns runs is suppressed: its runs are counted in
// RunsBelowMinRuns and no range is published for it. A run whose recipe the
// tree does not ship is counted in RunsUnknownRecipe and not grouped at all:
// a group key is a name a reader can look up (reconciliation D96).
func Aggregate(runs []Run, minRuns int) Result {
	var res Result
	type acc struct {
		durations, costs []float64
		notEstimated     int
	}
	groups := map[string]*acc{}
	for _, r := range runs {
		if r.NoFigure {
			res.RunsWithoutFigure++
			continue
		}
		res.Runs++
		if r.ComputeNotEstimated {
			res.ComputeNotEstimated++
		}
		name, shipped := ShippedName(r.Recipe)
		if !shipped {
			res.RunsUnknownRecipe++
			continue
		}
		g := groups[name]
		if g == nil {
			g = &acc{}
			groups[name] = g
		}
		g.durations = append(g.durations, r.DurationS)
		g.costs = append(g.costs, r.CostUSD)
		if r.ComputeNotEstimated {
			g.notEstimated++
		}
	}
	for _, name := range slices.Sorted(maps.Keys(groups)) {
		g := groups[name]
		if len(g.durations) < minRuns {
			res.RunsBelowMinRuns += len(g.durations)
			continue
		}
		res.Groups = append(res.Groups, Group{
			Recipe:              name,
			Runs:                len(g.durations),
			ComputeNotEstimated: g.notEstimated,
			DurationS:           roundSeconds(PercentilesOf(g.durations)),
			CostUSD:             roundCents(PercentilesOf(g.costs)),
		})
	}
	return res
}

// ShippedName maps a record's recipe name to the name the tree ships today,
// and reports false for a name it does not ship: a catalog name retired in
// 0.7.0 (recipe.Renamed) keeps its figures under the name the catalog carries
// now, and a name neither the catalog nor the rename table knows -- a recipe
// of a project layer's own, or a record whose recipe is empty -- is one a
// reader cannot look up, so it is never published as a group key.
func ShippedName(name string) (string, bool) {
	if to, ok := recipe.Renamed[name]; ok {
		name = to
	}
	if !slices.Contains(recipe.CatalogNames(), name) {
		return "", false
	}
	return name, true
}

// PercentilesOf is the min, median and 90th percentile of xs, interpolated
// between the closest ranks (the method R calls type 7 and numpy's default),
// so the median of an even sample is the mean of its two middle values. xs is
// not modified. The values are raw: Aggregate rounds them to what is
// published.
func PercentilesOf(xs []float64) Percentiles {
	sorted := slices.Clone(xs)
	slices.Sort(sorted)
	return Percentiles{
		Min:    percentile(sorted, 0),
		Median: percentile(sorted, 0.5),
		P90:    percentile(sorted, 0.9),
	}
}

// percentile is the p-th percentile of sorted (ascending). An empty slice is
// zero: Aggregate never publishes an empty group.
func percentile(sorted []float64, p float64) float64 {
	switch len(sorted) {
	case 0:
		return 0
	case 1:
		return sorted[0]
	}
	idx := p * float64(len(sorted)-1)
	lo, hi := int(math.Floor(idx)), int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (idx-float64(lo))*(sorted[hi]-sorted[lo])
}

// roundSeconds rounds a duration's three figures to whole seconds, and
// roundCents a cost's to whole cents: a published range is the number a
// reader compares, not float noise like 1809.9999999999998.
func roundSeconds(p Percentiles) Percentiles {
	return Percentiles{math.Round(p.Min), math.Round(p.Median), math.Round(p.P90)}
}

func roundCents(p Percentiles) Percentiles {
	return Percentiles{cents(p.Min), cents(p.Median), cents(p.P90)}
}

func cents(v float64) float64 { return math.Round(v*100) / 100 }
