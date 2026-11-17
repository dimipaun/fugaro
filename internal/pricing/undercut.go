package pricing

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"

	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// loadCatalog is the embedded catalog behind a variable, the same seam the CLI
// and the runner have (internal/cli, internal/runner): a released binary's file
// is valid — the generator validates its own output with catalog.Parse and a
// compile-time test prices every shipped row — so a check that has to be
// exercised against a file a test can state has no other way to get one.
var loadCatalog = catalog.Load

// The units the messages below name a price in. They are the file's own two
// (catalog.File): dollars per million tokens, and dollars per 1,000 searches.
const (
	perMillionTokens    = "per million tokens"
	perThousandSearches = "per 1,000 searches"
)

// UndercutsCatalog names every dimension of an override that prices a model
// below the cheapest end of the catalog row the model would have without it:
// one message per dimension, with the override's price and the catalog's. It is
// the check R4 reports from `fugaro update` and `fugaro validate` (finding
// F15). Nothing is refused here — an owner may hold a real price below the
// marketplace's, which is the point of model_prices — so the messages carry the
// numbers and leave the decision where the file is.
//
// The comparison cannot be made on the effective table: Table.With replaces an
// owner's model whole and an override row carries Lo = Hi = Rates, so the row
// that held the price being undercut is gone from it. The row is therefore
// compiled again here, as if the model were not overridden, by the same
// catalogModel every other surface prices through (design D5).
//
// routes is the caller's route table (internal/config's, the shape
// ApplyCatalog takes): a model the owner priced by hand appears in a route's
// Overridden list rather than its Models one, and both are read as a claim
// here, because the question is what the route would have priced the model at.
// The tier is the route's own — zdr unless the route sets require_zdr: false
// — and the end compared against is that tier's cheapest, which the route's
// price_policy and the catalog's staleness do not move: the floor is the
// tier's own whatever is charged (design D6, finding F11).
//
// A model the catalog does not price returns nothing, and so does one no route
// claims or one the catalog cannot price under the route's switches: there is
// no catalog price to be below.
func UndercutsCatalog(model string, override Override, routes []CatalogRoute) []string {
	file, err := loadCatalog()
	if err != nil || file == nil {
		return nil
	}
	e, ok := file.Models[model]
	if !ok {
		return nil
	}
	row, ok := catalogRowAsClaimed(model, e, routes, file)
	if !ok {
		return nil
	}
	// The override's cache multipliers are read as the table will read them:
	// the 0 an owner left in a field is raised to the input price by
	// Table.With, and a price of nothing that nothing charges would be
	// reported as an undercut of every tier.
	r := override.Rates
	defaultCacheRates(model, &r)
	tier := pricedTier(e, row)
	var out []string
	for _, dim := range catalog.Dimensions {
		// A dimension the tier prices no endpoint of is absent from it, and the
		// row's Lo carries the compiler's fold there so the row prices
		// something rather than nothing: a fold is not a price of the
		// catalog's, and an override below it undercuts nobody.
		if _, priced := tier[dim]; !priced {
			continue
		}
		ovPrice, unit := dimensionPrice(dim, r)
		loPrice, _ := dimensionPrice(dim, row.Lo)
		if unit == "" || loPrice <= 0 || ovPrice >= loPrice {
			continue
		}
		out = append(out, fmt.Sprintf("model_prices.%s.%s is %s and the cheapest endpoint the built-in price catalog prices for it on the %s tier is %s (checked %s): the override undercuts the catalog",
			model, dim, money(ovPrice, unit), row.Catalog.Tier, money(loPrice, unit), row.Catalog.CheckedAt))
	}
	return out
}

// catalogRowAsClaimed compiles the row the routes would give the model if the
// owner had not priced it, or reports that they would give it none. It walks
// the routes the way ApplyCatalog does — by name, last one winning — so the
// price this check names is the price that surface would have charged, and not
// a second answer from a second ordering.
func catalogRowAsClaimed(model string, e catalog.Entry, routes []CatalogRoute, file *catalog.File) (Model, bool) {
	source := ""
	if len(file.Sources) > 0 {
		source = file.Sources[0]
	}
	rs := slices.Clone(routes)
	sort.Slice(rs, func(i, j int) bool { return rs[i].Name < rs[j].Name })
	var row Model
	var found bool
	for _, r := range rs {
		if !slices.Contains(r.Models, model) && !slices.Contains(r.Overridden, model) {
			continue
		}
		m, err := catalogModel(model, r, e, source, file.CheckedAt, false)
		if err != nil {
			continue
		}
		row, found = m, true
	}
	return row, found
}

// pricedTier is the endpoints the compiled row's prices came from: the tier the
// row names (Model.Catalog.Tier), which is the choice catalogModel made, and
// not a second reading of the route's switches.
func pricedTier(e catalog.Entry, row Model) map[string]catalog.Bound {
	if row.Catalog != nil && row.Catalog.Tier == "zdr" {
		return e.ZDR
	}
	return e.All
}

// dimensionPrice is the price a whole set of rates gives one of the catalog's
// dimensions, in the file's own unit, or "" for a dimension this build does not
// read. A cache dimension is a ratio of the input price in Rates, so its price
// is that ratio applied to the set's own input price: the file's minimum is a
// dollars figure, and a ratio weighed against another row's ratio would compare
// numbers that are not prices.
func dimensionPrice(dim string, r Rates) (float64, string) {
	switch dim {
	case "input_per_m":
		return r.InputPerM, perMillionTokens
	case "output_per_m":
		return r.OutputPerM, perMillionTokens
	case "cache_read_per_m":
		return r.InputPerM * r.CacheRead, perMillionTokens
	case "cache_write_5m_per_m":
		return r.InputPerM * r.CacheWrite5m, perMillionTokens
	case "cache_write_1h_per_m":
		return r.InputPerM * r.CacheWrite1h, perMillionTokens
	case "web_search_per_1k":
		return r.WebSearchPer1k, perThousandSearches
	}
	return 0, ""
}

// money is one price as the messages print it: dollars in decimal and never in
// exponent notation, so the fraction of a cent a cache multiplier often comes
// to reads as $0.000042 rather than 4.2e-05. The figure is rounded at a
// billionth of a dollar first, because a cache price is the product of two
// float64 rates and the arithmetic leaves tails no price has: 1.5 × 0.2 is
// 0.30000000000000004, which is not a number to show an owner.
func money(v float64, unit string) string {
	return "$" + strconv.FormatFloat(math.Round(v*1e9)/1e9, 'f', -1, 64) + " " + unit
}
