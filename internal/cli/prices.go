package cli

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// loadCatalog is the embedded catalog behind a variable, like this package's
// other seams (doctorLookPath, readSecretValue). The file a released binary
// reads is valid — TestShippedCatalogLoads and the generator's own Parse keep
// it so — which leaves the surfaces' reporting of a catalog that will not
// load, and of a row the compile refuses, with no way to be exercised from a
// test.
var loadCatalog = catalog.Load

// priceTableFor is the table every CLI pricing surface builds: config.PriceTable
// over the owner's provider routes and model_prices, with the built-in price
// catalog. One function, so `validate`, `diagnose` and `budget prices` cannot
// price a model differently from the run (design D5, plan P5).
func priceTableFor(providers map[string]config.ModelProvider, ov pricing.Overrides, now time.Time) (*pricing.Table, error) {
	cat, err := loadCatalog()
	if err != nil {
		return nil, err
	}
	return config.PriceTable(providers, ov, cat, now)
}

// priceTable is priceTableFor for a selected project config, with the
// overrides it holds: lc nil (no project selected) is the embedded table with
// no routes and no overrides. The override set is returned as well, because
// `budget prices` marks the rows it priced.
func priceTable(lc *localcfg.Config, now time.Time) (*pricing.Table, pricing.Overrides, error) {
	if lc == nil {
		t, err := priceTableFor(nil, nil, now)
		return t, nil, err
	}
	ov, err := lc.Overrides()
	if err != nil {
		return nil, nil, err
	}
	t, err := priceTableFor(lc.Providers, ov, now)
	return t, ov, err
}

// modelPriceUndercuts names every price the owner's own model_prices hold below
// the cheapest endpoint the built-in catalog prices that model at on the tier
// its routes select — one line per dimension, with both figures
// (pricing.UndercutsCatalog, SA4.3; finding F15, plan R4/RS3). It is what
// `fugaro update` and `fugaro validate` report, and neither refuses: an owner
// may hold a real price below the marketplace's, which is the point of
// model_prices.
//
// The routes are the same ones every pricing surface here builds
// (priceTableFor, config.CatalogRoutes): the owner's own, so the price an
// override is below is the price the route that claims the model would have
// charged it without the override.
//
// The documents this reads are the ones the surfaces before it have already
// judged: an override that does not validate and a catalog that will not load
// each have a sentence of their own, about the key that wrote them, and a
// second one from here would be a worse copy of it.
func modelPriceUndercuts(lc *localcfg.Config) []string {
	if lc == nil || len(lc.ModelPrices) == 0 {
		return nil
	}
	ov, err := lc.Overrides()
	if err != nil {
		return nil
	}
	cat, err := loadCatalog()
	if err != nil || cat == nil {
		return nil
	}
	routes := config.CatalogRoutes(lc.Providers, cat, ov)
	var out []string
	for _, id := range slices.Sorted(maps.Keys(ov)) {
		out = append(out, pricing.UndercutsCatalog(id, ov[id], routes)...)
	}
	return out
}

// undercutProblems is modelPriceUndercuts in the shape `fugaro validate`
// reports a line in: the message's own first clause is the config path it is
// about (`model_prices.<model>.<dim>`), and the printer puts that path where
// every other problem of this command puts the key the owner has to edit.
func undercutProblems(lc *localcfg.Config) []config.Problem {
	var out []config.Problem
	for _, msg := range modelPriceUndercuts(lc) {
		path, rest, ok := strings.Cut(msg, " is ")
		if !ok {
			out = append(out, config.Problem{Message: msg})
			continue
		}
		out = append(out, config.Problem{Path: path, Message: "is " + rest})
	}
	return out
}
