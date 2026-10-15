package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// staleCatalog is the shipped catalog with its checked_at moved far enough
// back that every row it prices is stale: the run measures the catalog's own
// 30 days (catalog.StaleAfter), not the Anthropic table's 90.
func staleCatalog(t *testing.T) *catalog.File {
	t.Helper()
	f, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	old := *f
	old.CheckedAt = "2026-01-01"
	return &old
}

// shippedCatalogChecked is the checked_at of the catalog this repository
// ships, as a time. Tests that read a rate out of the shipped catalog measure
// from here rather than from the wall clock: the catalog has its own 30-day
// freshness window (catalog.StaleAfter), and past it every row it prices is
// charged at the dearest end of its tier, so a test written against the
// cheapest end would start failing on a date no code change marked.
func shippedCatalogChecked(t *testing.T) time.Time {
	t.Helper()
	f, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	d, err := time.Parse("2006-01-02", f.CheckedAt)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// freshCatalogTable is the run's own price table for routes, read from the
// shipped catalog the day after its own checked_at: inside the window, so
// each tier's cheapest end is what it charges, whatever today's date is.
func freshCatalogTable(t *testing.T, providers map[string]config.ModelProvider, ov pricing.Overrides) *pricing.Table {
	t.Helper()
	f, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := config.PriceTable(providers, ov, f, shippedCatalogChecked(t).Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

// TestResultJSONCatalogStale: a stale catalog charges the dearest end of
// every tier it prices and says so in both places the owner looks — one WARN
// per run and cost.catalog_stale in result.json (design §7, P7, Task 5
// step 5).
func TestResultJSONCatalogStale(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := providerRun(t, providerPinned(t), "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, use)}, "acme/app")
	tbl, err := config.PriceTable(g.deps.Providers, nil, staleCatalog(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m, ok := tbl.Lookup(deepseek)
	if !ok || m.Catalog == nil || !m.Catalog.Stale || m.Catalog.Bound != "max" {
		t.Fatalf("the fixture table is not stale: %+v", m.Catalog)
	}
	// The run's own table, as the job's ceiling built it.
	g.deps.Spend.Prices = tbl

	var status int
	var body string
	rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Cost == nil || !rec.Cost.CatalogStale {
		t.Fatalf("cost = %+v: a stale catalog is not recorded", rec.Cost)
	}
	raw, err := json.Marshal(rec.Cost)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"catalog_stale":true`) {
		t.Errorf("result.json's cost lacks catalog_stale: %s", raw)
	}
	stored, err := g.store.ReadRecord(context.Background())
	if err != nil || stored.Cost == nil || !stored.Cost.CatalogStale {
		t.Fatalf("stored = %+v, %v", stored.Cost, err)
	}
	if logs := g.logs.String(); !strings.Contains(logs, "budget: price catalog stale") {
		t.Errorf("the run did not warn about the stale catalog:\n%s", logs)
	}
}

// TestStaleCatalogRowNothingChargedIsNotStale pins the other side of the
// flag: a table holds a stale catalog row as soon as one catalog-eligible
// route claims one, so `catalog_stale` and the WARN are about what this run
// charged, never about the table. Here the same stale table builds, but the
// run's pinned model is the owner's own price under model_prices: nothing was
// charged at the catalog's dearest end, so nothing is stale about the run.
func TestStaleCatalogRowNothingChargedIsNotStale(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := providerRun(t, providerPinned(t), "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, use)}, "acme/app")
	// The stale table of the test above, with the pinned model priced by
	// hand: the route still claims the catalog's other models, whose stale
	// rows nothing here calls.
	ov := pricing.Overrides{deepseek: {InputPerM: 1, OutputPerM: 2}}
	tbl, err := config.PriceTable(g.deps.Providers, ov, staleCatalog(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := tbl.Lookup(deepseek); !ok || m.Catalog != nil {
		t.Fatalf("the pinned model is not hand-priced: %+v", m.Catalog)
	}
	if !catalogHoldsAStaleRow(tbl) {
		t.Fatal("the fixture table holds no stale row: the test says nothing")
	}
	g.deps.Spend.Prices = tbl

	var status int
	var body string
	rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Cost == nil || rec.Cost.CatalogStale {
		t.Fatalf("cost = %+v: a stale row nothing charged for set the flag", rec.Cost)
	}
	if logs := g.logs.String(); strings.Contains(logs, "price catalog stale") {
		t.Errorf("a stale row no pin names warned:\n%s", logs)
	}
}

// catalogHoldsAStaleRow is the whole-table question the run no longer asks,
// kept here so the test above proves the table really is stale.
func catalogHoldsAStaleRow(t *pricing.Table) bool {
	for _, m := range t.Models {
		if m.Catalog != nil && m.Catalog.Stale {
			return true
		}
	}
	return false
}

// TestFreshCatalogIsNotStale: the same run with the shipped catalog records
// no staleness and warns nothing, so the flag means the catalog and not the
// run.
func TestFreshCatalogIsNotStale(t *testing.T) {
	use := pricing.Usage{Input: 1000, Output: 500}
	g := providerRun(t, providerPinned(t), "enforce", "50",
		[]anthropicfake.Reply{anthropicfake.Compat{}.MessageOK(deepseek, use)}, "acme/app")
	var status int
	var body string
	rec, err := g.run(t, callDeepseek(&status, &body, implement("feature")), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Cost == nil || rec.Cost.CatalogStale {
		t.Fatalf("cost = %+v: the shipped catalog is marked stale", rec.Cost)
	}
	if logs := g.logs.String(); strings.Contains(logs, "price catalog stale") {
		t.Errorf("a fresh catalog warned:\n%s", logs)
	}
}
