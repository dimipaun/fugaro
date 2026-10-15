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
