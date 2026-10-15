package cli

import (
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// priceTableFor is the table every CLI pricing surface builds: config.PriceTable
// over the owner's provider routes and model_prices, with the built-in price
// catalog. One function, so `validate`, `diagnose` and `budget prices` cannot
// price a model differently from the run (design D5, plan P5).
func priceTableFor(providers map[string]config.ModelProvider, ov pricing.Overrides) (*pricing.Table, error) {
	cat, err := catalog.Load()
	if err != nil {
		return nil, err
	}
	return config.PriceTable(providers, ov, cat, time.Now())
}

// priceTable is priceTableFor for a selected project config, with the
// overrides it holds: lc nil (no project selected) is the embedded table with
// no routes and no overrides. The override set is returned as well, because
// `budget prices` marks the rows it priced.
func priceTable(lc *localcfg.Config) (*pricing.Table, pricing.Overrides, error) {
	if lc == nil {
		t, err := priceTableFor(nil, nil)
		return t, nil, err
	}
	ov, err := lc.Overrides()
	if err != nil {
		return nil, nil, err
	}
	t, err := priceTableFor(lc.Providers, ov)
	return t, ov, err
}
