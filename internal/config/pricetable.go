package config

import (
	"maps"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// PriceTable is the one place a price table is built, so no two surfaces can
// disagree about what a model costs (design D5, plan P5): the embedded Claude
// rows, then a row for every model a catalog-eligible route claims, priced
// from the built-in provider price catalog at the tier and bound that route's
// switches select, then the owner's model_prices applied **last**, so an
// override still wins. The run's gateway, `validate`, `diagnose` and
// `budget prices` all call it, so `validate` cannot refuse a pin the gateway
// would price.
//
// providers is the owner's route table — the local config's providers block,
// or the job's FUGARO_MODEL_PROVIDERS, which is the same table for a run.
// nil (or empty) is a machine with no route, and then the result is the
// embedded table with the overrides, exactly as it was before the catalog
// existed: a model with no price is still refused rather than priced from a
// marketplace guess. cat is the loaded catalog; nil adds no catalog row.
// now is the clock the catalog's staleness is measured against.
//
// The error is the refusal path: a malformed catalog, or a route whose
// switches cannot price one of the models it claims (a require_zdr route on a
// model with no ZDR-eligible endpoint), fails the whole table rather than
// leaving the model out — an owner who hit it needs to see it before a run
// charges anything.
func PriceTable(providers map[string]ModelProvider, overrides pricing.Overrides, cat *catalog.File, now time.Time) (*pricing.Table, error) {
	t, err := pricing.ApplyCatalog(pricing.Embedded(), cat, catalogRoutes(providers, cat, overrides), now)
	if err != nil {
		return nil, err
	}
	return t.With(overrides)
}

// catalogRoutes are the routes the catalog may price, each with the model IDs
// it claims. A route that is not catalog-eligible is left out entirely, so a
// vendor's own endpoint never picks up OpenRouter's marketplace price for a
// model ID it happens to serve too (design D5, plan P6): the catalog is
// OpenRouter's prices, and everything else is priced under model_prices.
//
// A model the owner has priced under model_prices is left out as well. It is
// the second way out of a route the catalog cannot price (design D9, plan
// P8): an owner who prices the model by hand has said what it costs, so the
// route's switches have nothing left to decide, and the row would only be
// replaced by the override a moment later anyway.
func catalogRoutes(providers map[string]ModelProvider, cat *catalog.File, overrides pricing.Overrides) []pricing.CatalogRoute {
	if cat == nil || len(cat.Models) == 0 {
		return nil
	}
	ids := slices.Sorted(maps.Keys(cat.Models))
	var routes []pricing.CatalogRoute
	for _, name := range sortedKeys(providers) {
		p := providers[name]
		if !CatalogEligible(p.BaseURL) {
			continue
		}
		var claimed []string
		for _, id := range ids {
			if _, priced := overrides[id]; priced {
				continue
			}
			if p.Matches(id) {
				claimed = append(claimed, id)
			}
		}
		if len(claimed) == 0 {
			continue
		}
		routes = append(routes, pricing.CatalogRoute{
			Name: name, Models: claimed,
			RequireZDR: p.RequireZDROn(), Policy: p.PricePolicySet(),
		})
	}
	return routes
}
