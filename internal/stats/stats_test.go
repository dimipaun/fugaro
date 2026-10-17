package stats

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// TestStatsPercentilesInterpolate: the three figures are read off the sorted
// sample by linear interpolation, so an even sample's median is the mean of
// its two middle values.
func TestStatsPercentilesInterpolate(t *testing.T) {
	var xs []float64
	for i := 1; i <= 20; i++ {
		xs = append(xs, float64(i))
	}
	before := append([]float64(nil), xs...)
	got := PercentilesOf(xs)
	want := Percentiles{Min: 1, Median: 10.5, P90: 18.1}
	if got != want {
		t.Errorf("PercentilesOf(1..20) = %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(xs, before) {
		t.Errorf("PercentilesOf modified its input: %v", xs)
	}
	if got := PercentilesOf([]float64{7}); got != (Percentiles{Min: 7, Median: 7, P90: 7}) {
		t.Errorf("PercentilesOf(one value) = %+v", got)
	}
	if got := PercentilesOf(nil); got != (Percentiles{}) {
		t.Errorf("PercentilesOf(nil) = %+v, want the zero value", got)
	}
	if got := PercentilesOf([]float64{2, 1}); got != (Percentiles{Min: 1, Median: 1.5, P90: 1.9}) {
		t.Errorf("PercentilesOf(2, 1) = %+v", got)
	}
}

// catalogRun is a run of a catalog recipe with one figure, for the tests that
// only care which group it lands in.
func catalogRun(name string, durationS, costUSD float64) Run {
	return Run{Recipe: name, RecipeSource: string(recipe.SourceCatalog), DurationS: durationS, CostUSD: costUSD}
}

// TestStatsAggregatesByRecipe: groups are by recipe, ascending, each with its
// own sample size, and the published figures are rounded to a second and a
// cent.
func TestStatsAggregatesByRecipe(t *testing.T) {
	var runs []Run
	for i := 1; i <= 20; i++ {
		runs = append(runs, catalogRun("standard", float64(i)*100, float64(i)*0.1))
	}
	for i := 1; i <= 20; i++ {
		r := catalogRun("solo", float64(i)*10.4, 1.006)
		r.ComputeNotEstimated = i == 1
		runs = append(runs, r)
	}
	res := Aggregate(runs, MinRuns)
	if res.Runs != 40 || res.RunsUnknownRecipe != 0 || res.RunsBelowMinRuns != 0 || res.RunsWithoutFigure != 0 {
		t.Fatalf("counts = %+v", res)
	}
	if len(res.Groups) != 2 || res.Groups[0].Recipe != "solo" || res.Groups[1].Recipe != "standard" {
		t.Fatalf("groups = %+v, want solo then standard", res.Groups)
	}
	solo := res.Groups[0]
	if solo.Runs != 20 || solo.ComputeNotEstimated != 1 {
		t.Errorf("solo group = %+v", solo)
	}
	if want := (Percentiles{Min: 10, Median: 109, P90: 188}); solo.DurationS != want {
		t.Errorf("solo durations = %+v, want %+v (rounded to the second)", solo.DurationS, want)
	}
	if want := (Percentiles{Min: 1.01, Median: 1.01, P90: 1.01}); solo.CostUSD != want {
		t.Errorf("solo costs = %+v, want %+v (rounded to the cent)", solo.CostUSD, want)
	}
	std := res.Groups[1]
	if want := (Percentiles{Min: 100, Median: 1050, P90: 1810}); std.DurationS != want {
		t.Errorf("standard durations = %+v, want %+v", std.DurationS, want)
	}
	if want := (Percentiles{Min: 0.1, Median: 1.05, P90: 1.81}); std.CostUSD != want {
		t.Errorf("standard costs = %+v, want %+v", std.CostUSD, want)
	}
	if res.ComputeNotEstimated != 1 {
		t.Errorf("ComputeNotEstimated = %d, want 1", res.ComputeNotEstimated)
	}
}

// TestStatsDropsRecipesTheTreeDoesNotShip: a group key is a name a reader can
// look up, so a name internal/recipe's catalog does not carry is dropped and
// counted rather than published -- and a catalog name retired in 0.7.0 is
// grouped under the name the tree ships today.
func TestStatsDropsRecipesTheTreeDoesNotShip(t *testing.T) {
	var runs []Run
	for range 20 {
		runs = append(runs, catalogRun("claude-solo", 60, 1))
	}
	for range 20 {
		runs = append(runs, Run{Recipe: "acme-house-style", RecipeSource: string(recipe.SourceRepo), DurationS: 60, CostUSD: 1})
	}
	for range 20 {
		runs = append(runs, Run{DurationS: 60, CostUSD: 1})
	}
	res := Aggregate(runs, MinRuns)
	if len(res.Groups) != 1 || res.Groups[0].Recipe != "solo" || res.Groups[0].Runs != 20 {
		t.Fatalf("groups = %+v, want one group named solo with 20 runs", res.Groups)
	}
	if res.RunsUnknownRecipe != 40 {
		t.Errorf("RunsUnknownRecipe = %d, want 40", res.RunsUnknownRecipe)
	}
	if res.Runs != 60 {
		t.Errorf("Runs = %d, want 60", res.Runs)
	}
}

// TestStatsDropsARepoRecipeThatShadowsTheCatalog: a repository or project
// recipe may reuse a catalog name (internal/recipe's resolveRecipe lets it
// shadow the built-in one), so the name alone is not the recipe: a group is
// published only for a run whose recipe came from the catalog. Otherwise one
// repository's own loop would be published under a built-in label, and its
// figures -- the very thing a reader compares against -- would be its own.
func TestStatsDropsARepoRecipeThatShadowsTheCatalog(t *testing.T) {
	var runs []Run
	for i := 1; i <= 20; i++ {
		runs = append(runs, catalogRun("standard", float64(i)*100, float64(i)*0.1))
	}
	// 20 runs of a repository's own "standard" at about $500: if these were
	// grouped with the catalog's, the p90 would read about $513.10.
	for i := 1; i <= 20; i++ {
		runs = append(runs, Run{Recipe: "standard", RecipeSource: string(recipe.SourceRepo), DurationS: 900, CostUSD: float64(i) * 500})
	}
	for range 20 {
		runs = append(runs, Run{Recipe: "standard", RecipeSource: string(recipe.SourceProject), DurationS: 900, CostUSD: 500})
	}
	res := Aggregate(runs, MinRuns)
	if len(res.Groups) != 1 {
		t.Fatalf("groups = %+v, want only the catalog's standard", res.Groups)
	}
	std := res.Groups[0]
	if std.Recipe != "standard" || std.Runs != 20 {
		t.Fatalf("group = %+v, want the catalog's 20 runs", std)
	}
	if want := (Percentiles{Min: 0.1, Median: 1.05, P90: 1.81}); std.CostUSD != want {
		t.Errorf("costs = %+v, want the catalog's %+v: a repo recipe's own loop is not published under a built-in label", std.CostUSD, want)
	}
	if res.RunsUnknownRecipe != 40 {
		t.Errorf("RunsUnknownRecipe = %d, want the 40 shadowing runs counted", res.RunsUnknownRecipe)
	}
}

// TestStatsSuppressesGroupsBelowMinRuns: a thin group is suppressed, its runs
// are counted, and a lower threshold publishes it deliberately.
func TestStatsSuppressesGroupsBelowMinRuns(t *testing.T) {
	var runs []Run
	for i := 1; i <= 4; i++ {
		runs = append(runs, catalogRun("premium", 60, 1))
	}
	res := Aggregate(runs, MinRuns)
	if len(res.Groups) != 0 || res.Runs != 4 || res.RunsBelowMinRuns != 4 {
		t.Fatalf("Aggregate(min 20) = %+v, want the group suppressed", res)
	}
	res = Aggregate(runs, 4)
	if len(res.Groups) != 1 || res.Groups[0].Recipe != "premium" || res.Groups[0].Runs != 4 || res.RunsBelowMinRuns != 0 {
		t.Fatalf("Aggregate(min 4) = %+v, want the group published", res)
	}
}

// TestStatsCountsRunsWithoutAFigure: a record no figure can be drawn from is
// counted, never grouped, so the sample does not read smaller than it is.
func TestStatsCountsRunsWithoutAFigure(t *testing.T) {
	runs := []Run{{Recipe: "standard", RecipeSource: string(recipe.SourceCatalog), NoFigure: true}}
	for i := 1; i <= 20; i++ {
		runs = append(runs, catalogRun("standard", 60, 1))
	}
	res := Aggregate(runs, MinRuns)
	if res.RunsWithoutFigure != 1 || res.Runs != 20 {
		t.Fatalf("Aggregate = %+v, want 1 record without a figure and 20 runs", res)
	}
	if len(res.Groups) != 1 || res.Groups[0].Runs != 20 {
		t.Fatalf("groups = %+v, want one group of 20", res.Groups)
	}
}

// TestStatsPublishesNoMean: no struct here has a field for a mean, so no mean
// can be published (reconciliation D97).
func TestStatsPublishesNoMean(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(Percentiles{}), reflect.TypeOf(Group{}), reflect.TypeOf(Result{})} {
		for i := range typ.NumField() {
			name := strings.ToLower(typ.Field(i).Name)
			if strings.Contains(name, "mean") || strings.Contains(name, "avg") || strings.Contains(name, "average") {
				t.Errorf("%s.%s is a mean: a range is published, never an average", typ.Name(), typ.Field(i).Name)
			}
		}
	}
}
