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
// override still wins. A model the owner has priced is materialized from the
// catalog for its limits alone (CatalogRoute.Overridden), so the override
// replaces the price and not the ceiling. The run's gateway, `validate`,
// `diagnose` and `budget prices` all call it, so `validate` cannot refuse a
// pin the gateway would price.
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
// A model the owner has priced under model_prices goes in the route's
// Overridden list rather than its Models one. The catalog compiles no rates for
// it — the owner's number is the second way out of a route the catalog cannot
// price (design D9, plan P8), so its switches are no refusal there — but the
// row is still materialized for the file's limits: Table.With replaces the
// rates and clears the provenance, and it keeps the limits, so an override
// applied over no row at all would raise the model's ceiling to the table's
// defaults and the gateway would stop refusing a max_tokens above the model's
// real maximum.
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
		var claimed, priced []string
		for _, id := range ids {
			if !p.Matches(id) {
				continue
			}
			if _, hand := overrides[id]; hand {
				priced = append(priced, id)
				continue
			}
			claimed = append(claimed, id)
		}
		if len(claimed) == 0 && len(priced) == 0 {
			continue
		}
		routes = append(routes, pricing.CatalogRoute{
			Name: name, Models: claimed, Overridden: priced,
			RequireZDR: p.RequireZDROn(), Policy: p.PricePolicySet(),
		})
	}
	return routes
}
