package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

type modelsSearchOptions struct {
	cloud cloudOptions
}

func newModelsSearchCmd() *cobra.Command {
	var o modelsSearchOptions
	cmd := &cobra.Command{
		Use:   "search <text>",
		Short: "Find a model in the built-in price catalog and the Claude table",
		Long: "Find a model this project could run, by a substring of its ID.\n\n" +
			"The search reads this binary's own tables — the built-in provider price\n" +
			"catalog and the embedded Claude table — and the selected project config,\n" +
			"and reaches nothing. A hit shows where its price comes from, the range it\n" +
			"is charged over and the route of this project that claims it, so a reader\n" +
			"knows whether the model is usable before running anything. A miss names the\n" +
			"one way to use a model the catalog does not carry: price it yourself.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runModelsSearch(cmd, &o, args[0]) },
	}
	addCloudFlags(cmd, &o.cloud)
	return cmd
}

// modelsSearchRow is one hit: a catalog entry's tier, an embedded Claude row,
// or a model the owner priced themselves.
type modelsSearchRow struct {
	ID     string
	Source string
	// Tier is the catalog tier the row is charged over: zdr or all. "" on
	// every other row.
	Tier string
	// Input and Output are the range the tier prices, or the single price of
	// a row that has no range.
	Input, Output string
	// Endpoints says how many endpoints the row's tier covers.
	Endpoints string
	// Route is the route of this project that claims the model, or "" when
	// no route serves it.
	Route     string
	Context   int64
	MaxOutput int64
}

func runModelsSearch(cmd *cobra.Command, o *modelsSearchOptions, text string) error {
	lc, err := pricesConfig(cmd, o.cloud)
	if err != nil {
		return err
	}
	rows, err := modelsSearchRows(lc, text)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if len(rows) == 0 {
		fmt.Fprintf(w, "no model matches %q\n", oneLine(text))
		fmt.Fprintf(w, "a model the catalog does not carry is usable once the owner prices it: fugaro models add %s --price-in X --price-out Y\n", oneLine(text))
		return nil
	}
	fmt.Fprintf(w, "models matching %q\n\n", oneLine(text))
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tSOURCE\tTIER\tINPUT/M\tOUTPUT/M\tENDPOINTS\tROUTE\tNOTES")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", oneLine(r.ID), r.Source, dash(r.Tier),
			r.Input, r.Output, dash(r.Endpoints), routeCell(r.Route), modelsLimits(r))
	}
	return tw.Flush()
}

// modelsSearchRows is every hit of text, case-insensitively by substring, in
// ID order: the catalog's tiers, the embedded Claude table's rows, and the
// models the owner's own model_prices prices — which is what a miss tells a
// reader to write, so a model already priced that way is found rather than
// reported missing.
//
// The owner's overrides decide which rows they replace, so a table that will
// not build is this function's error rather than a search that answers from
// the catalog alone: a row shown where the owner set a price is a wrong price,
// not a missing one, and a model they priced would vanish from the hits.
// `models ls` and `budget prices` surface the same error (priceTable).
func modelsSearchRows(lc *localcfg.Config, text string) ([]modelsSearchRow, error) {
	needle := strings.ToLower(text)
	out := []modelsSearchRow{}
	cat, err := loadCatalog()
	if err != nil {
		return nil, err
	}
	table, ov, err := priceTable(lc, budgetNow())
	if err != nil {
		return nil, err
	}
	local := map[string]priceRow{}
	for _, r := range priceRowsFor(table, ov) {
		if r.Local {
			local[r.ID] = r
		}
	}
	for _, id := range slices.Sorted(maps.Keys(cat.Models)) {
		if !strings.Contains(strings.ToLower(id), needle) {
			continue
		}
		if _, ok := local[id]; ok {
			continue
		}
		e := cat.Models[id]
		for _, tier := range []string{"zdr", "all"} {
			bounds, ok := tierBounds(e, tier)
			if !ok {
				continue
			}
			out = append(out, modelsSearchRow{
				ID: id, Source: "catalog", Tier: tier,
				Input: rangeCell(bounds, "input_per_m"), Output: rangeCell(bounds, "output_per_m"),
				Endpoints: endpointsCell(e, tier), Route: modelsRouteOf(lc, id),
				Context: e.ContextTokens, MaxOutput: e.MaxOutputTokens,
			})
		}
	}
	embedded := pricing.Embedded()
	for _, id := range slices.Sorted(maps.Keys(embedded.Models)) {
		if !strings.Contains(strings.ToLower(id), needle) {
			continue
		}
		if _, ok := local[id]; ok {
			continue
		}
		m := embedded.Models[id]
		out = append(out, modelsSearchRow{
			ID: id, Source: "embedded", Input: priceUSD(m.Rates.InputPerM), Output: priceUSD(m.Rates.OutputPerM),
			Route: modelsRouteOf(lc, id), Context: m.ContextTokens, MaxOutput: m.MaxOutputTokens,
		})
	}
	for _, id := range slices.Sorted(maps.Keys(local)) {
		if !strings.Contains(strings.ToLower(id), needle) {
			continue
		}
		r := local[id]
		out = append(out, modelsSearchRow{
			ID: id, Source: "local", Input: priceUSD(r.InputPerM), Output: priceUSD(r.OutputPerM),
			Route: modelsRouteOf(lc, id),
		})
	}
	return out, nil
}

// tierBounds is a catalog entry's bounds for one tier: the zdr tier is absent
// on a model with no ZDR-eligible endpoint (design D9), and the search does not
// invent the row it would need.
func tierBounds(e catalog.Entry, tier string) (map[string]catalog.Bound, bool) {
	if tier == "zdr" {
		if len(e.ZDR) == 0 {
			return nil, false
		}
		return e.ZDR, true
	}
	return e.All, true
}

// endpointsCell is how many endpoints the row's tier covers, of every endpoint
// the model is served by: the two differ on the zdr tier, which is the whole
// reason both numbers are shown.
func endpointsCell(e catalog.Entry, tier string) string {
	if tier == "zdr" {
		return fmt.Sprintf("%d of %d endpoints", e.ZDREndpoints, e.Endpoints)
	}
	return fmt.Sprintf("%d endpoints", e.Endpoints)
}

// rangeCell is one dimension's price range over a tier: the two ends when they
// differ, one number when they do not, and a dash for a dimension no endpoint
// of the tier prices.
func rangeCell(bounds map[string]catalog.Bound, dim string) string {
	b, ok := bounds[dim]
	if !ok {
		return "—"
	}
	if b.Min == b.Max {
		return "$" + b.Min
	}
	return "$" + b.Min + " – $" + b.Max
}

// modelsLimits is what the model accepts, in the units a reader compares
// against the model's own page.
func modelsLimits(r modelsSearchRow) string {
	context := "context unknown"
	if r.Context > 0 {
		context = fmt.Sprintf("context %d tokens", r.Context)
	}
	output := "max output unknown"
	if r.MaxOutput > 0 {
		output = fmt.Sprintf("max output %d tokens", r.MaxOutput)
	}
	return context + ", " + output
}

// routeCell is the route a hit's row is about, or the words for a model no
// route of this project serves: a hit with no route is refused at launch, and
// a blank cell would not say so.
func routeCell(route string) string {
	if route == "" {
		return "no route"
	}
	return oneLine(route)
}

// priceUSD is one price, US dollars per million tokens.
func priceUSD(perM float64) string {
	return fmt.Sprintf("$%g", perM)
}
