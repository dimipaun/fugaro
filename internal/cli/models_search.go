package cli

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

type modelsSearchOptions struct {
	cloud cloudOptions
	json  bool
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
	cmd.Flags().BoolVar(&o.json, "json", false, "print machine-readable output")
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
	// Bounds is the tier's own price ranges, as the catalog file writes them:
	// the read prints one cell per dimension out of it (rangeCell) and the
	// document one range per dimension. nil on a row with a single price.
	Bounds map[string]catalog.Bound
	// InputPerM and OutputPerM are that single price, US dollars per million
	// tokens: the embedded Claude table's, or the owner's own.
	InputPerM, OutputPerM float64
	// Endpoints is how many endpoints serve the model, and ZDREndpoints how
	// many of them are ZDR-eligible: the two numbers behind the read's "N of M
	// endpoints". Both 0 on a row the catalog does not price.
	Endpoints, ZDREndpoints int
	// Route is the route of this project that claims the model, or "" when
	// no route serves it.
	Route     string
	Context   int64
	MaxOutput int64
}

// modelsSearchDocVersion is the document's first version, and the number a
// member added later keeps: schemas/models-search.schema.json pins it.
const modelsSearchDocVersion = 1

// modelsSearchDoc is what `fugaro models search --json` prints, under
// schemas/models-search.schema.json (D784): the hits the read prints, as data
// rather than as the cells of a table.
type modelsSearchDoc struct {
	V int `json:"v"`
	// Query is the term the search was given, as the caller wrote it.
	Query string `json:"query"`
	// Models is one object per hit, in the order the read prints them, and is
	// empty on a miss rather than absent.
	Models []modelsSearchHit `json:"models"`
}

// modelsSearchHit is one hit: the row the read prints, with the price as the
// range it is charged over rather than the cell the read formats.
type modelsSearchHit struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	// Tier is the catalog tier the price is over — zdr or all — and is absent
	// on a row with no tier: the embedded table's and the owner's own.
	Tier string `json:"tier,omitempty"`
	// InputPerM and OutputPerM are the range the row is charged over, US
	// dollars per million tokens. The two ends are equal on a row with a
	// single price, and a dimension the tier prices no endpoint for is absent
	// rather than a zero (D7(a)).
	InputPerM  *modelsSearchRange `json:"input_per_m,omitempty"`
	OutputPerM *modelsSearchRange `json:"output_per_m,omitempty"`
	// Endpoints is how many endpoints the model is served by, and
	// ZDREndpoints how many of them are ZDR-eligible: the read's "N of M" is
	// these two. Both absent on a row the catalog does not price.
	Endpoints    int `json:"endpoints,omitempty"`
	ZDREndpoints int `json:"zdr_endpoints,omitempty"`
	// Route is the route of this project that claims the model, and "" when no
	// route serves it: a launch refuses such a hit, and the read says "no
	// route" where the document leaves the member empty.
	Route string `json:"route"`
	// ContextTokens and MaxOutputTokens are what the model accepts, 0 when the
	// table reports neither.
	ContextTokens   int64 `json:"context_tokens,omitempty"`
	MaxOutputTokens int64 `json:"max_output_tokens,omitempty"`
}

// modelsSearchRange is one dimension's price over the row's endpoints, US
// dollars per million tokens.
type modelsSearchRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
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
	if o.json {
		return writeJSON(w, modelsSearchDocOf(text, rows))
	}
	if len(rows) == 0 {
		fmt.Fprintf(w, "no model matches %q\n", oneLine(text))
		fmt.Fprintf(w, "a model the catalog does not carry is usable once the owner prices it: fugaro models add %s --price-in X --price-out Y\n", oneLine(text))
		return nil
	}
	fmt.Fprintf(w, "models matching %q\n\n", oneLine(text))
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tSOURCE\tTIER\tINPUT/M\tOUTPUT/M\tENDPOINTS\tROUTE\tNOTES")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", cellOneLine(r.ID), r.Source, dash(r.Tier),
			dimCell(r, "input_per_m", r.InputPerM), dimCell(r, "output_per_m", r.OutputPerM), endpointsCell(r),
			routeCell(r.Route), modelsLimits(r))
	}
	return tw.Flush()
}

// modelsSearchDocOf is the read as data: the same hits, in the same order.
func modelsSearchDocOf(text string, rows []modelsSearchRow) modelsSearchDoc {
	doc := modelsSearchDoc{V: modelsSearchDocVersion, Query: text, Models: make([]modelsSearchHit, 0, len(rows))}
	for _, r := range rows {
		doc.Models = append(doc.Models, modelsSearchHitOf(r))
	}
	return doc
}

// modelsSearchHitOf is one row's document form. The IDs and the term are data
// here and are not put through the terminal's own escaping (cellOneLine):
// encoding/json escapes a control character as it writes the string, and a
// consumer reads back the bytes the table holds (safetext.go).
func modelsSearchHitOf(r modelsSearchRow) modelsSearchHit {
	h := modelsSearchHit{
		ID: r.ID, Source: r.Source, Tier: r.Tier, Route: r.Route,
		Endpoints: r.Endpoints, ZDREndpoints: r.ZDREndpoints,
		ContextTokens: r.Context, MaxOutputTokens: r.MaxOutput,
	}
	if r.Bounds == nil {
		h.InputPerM = &modelsSearchRange{Min: r.InputPerM, Max: r.InputPerM}
		h.OutputPerM = &modelsSearchRange{Min: r.OutputPerM, Max: r.OutputPerM}
		return h
	}
	h.InputPerM = priceRangeOf(r.Bounds, "input_per_m")
	h.OutputPerM = priceRangeOf(r.Bounds, "output_per_m")
	return h
}

// priceRangeOf is one dimension's range out of the catalog's own bounds: the
// file's decimal strings read as the numbers a consumer adds up. A dimension
// the tier prices no endpoint for has no range rather than a zero, and a bound
// that is not a plain decimal price cannot reach here: the catalog's loader
// refuses one as it parses the file (catalog.Bound).
func priceRangeOf(bounds map[string]catalog.Bound, dim string) *modelsSearchRange {
	b, ok := bounds[dim]
	if !ok {
		return nil
	}
	min, _ := strconv.ParseFloat(b.Min, 64)
	max, _ := strconv.ParseFloat(b.Max, 64)
	return &modelsSearchRange{Min: min, Max: max}
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
	// The models the owner's own model_prices replaced, and the price each was
	// replaced by, read off the overrides rather than out of priceRowsFor: the
	// search needs two fields of those rows, and the builder would allocate a
	// full row for every model in the table to answer it. A key is resolved
	// through the table exactly as the builder resolves it, so an alias key
	// marks the model it names and a key the table does not have is a model the
	// override added.
	local := map[string]pricing.Model{}
	for k := range ov {
		if m, ok := table.Lookup(k); ok {
			local[m.ID] = m
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
				ID: id, Source: modelsSourceCatalog, Tier: tier, Bounds: bounds,
				Endpoints: e.Endpoints, ZDREndpoints: e.ZDREndpoints, Route: modelsRouteOf(lc, id),
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
			ID: id, Source: modelsSourceEmbedded, InputPerM: m.Rates.InputPerM, OutputPerM: m.Rates.OutputPerM,
			Route: modelsRouteOf(lc, id), Context: m.ContextTokens, MaxOutput: m.MaxOutputTokens,
		})
	}
	for _, id := range slices.Sorted(maps.Keys(local)) {
		if !strings.Contains(strings.ToLower(id), needle) {
			continue
		}
		m := local[id]
		out = append(out, modelsSearchRow{
			ID: id, Source: modelsSourceLocal, InputPerM: m.Rates.InputPerM, OutputPerM: m.Rates.OutputPerM,
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
func endpointsCell(r modelsSearchRow) string {
	switch r.Tier {
	case "zdr":
		return fmt.Sprintf("%d of %d endpoints", r.ZDREndpoints, r.Endpoints)
	case "":
		return "—"
	default:
		return fmt.Sprintf("%d endpoints", r.Endpoints)
	}
}

// dimCell is one dimension's price cell: the range the row's tier prices when
// it has one, and the row's single price otherwise.
func dimCell(r modelsSearchRow, dim string, perM float64) string {
	if r.Bounds != nil {
		return rangeCell(r.Bounds, dim)
	}
	return priceUSD(perM)
}

// rangeCell is one dimension's price range over a tier: the two ends when they
// differ, one number when they do not, and a dash for a dimension no endpoint
// of the tier prices. The bounds are strings out of the catalog file, so they
// are escaped like every other value of this table: a bound carrying a newline
// or a tab would break the row's columns (cellOneLine).
func rangeCell(bounds map[string]catalog.Bound, dim string) string {
	b, ok := bounds[dim]
	if !ok {
		return "—"
	}
	if b.Min == b.Max {
		return "$" + cellOneLine(b.Min)
	}
	return "$" + cellOneLine(b.Min) + " – $" + cellOneLine(b.Max)
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
	return cellOneLine(route)
}

// priceUSD is one price, US dollars per million tokens.
func priceUSD(perM float64) string {
	return fmt.Sprintf("$%g", perM)
}
