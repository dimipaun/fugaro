package pricing

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// catalogNow is the clock every test compiles against, so no test depends
// on the day it runs.
var catalogNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func bound(min, max string) catalog.Bound {
	return catalog.Bound{Min: min, Max: max, MinOf: "a", MaxOf: "c"}
}

// catalogFile is a one-model fixture: the entry the test passes and the
// catalog's own date.
func catalogFile(checkedAt string, e catalog.Entry) *catalog.File {
	return &catalog.File{
		Version: catalog.Version, CheckedAt: checkedAt,
		Sources: []string{"https://openrouter.ai/api/v1/models"},
		Models:  map[string]catalog.Entry{"vendor/model": e},
	}
}

// entry is a model priced over two tiers with three dimensions, the shape
// the generator emits.
func entry() catalog.Entry {
	return catalog.Entry{
		ContextTokens: 1000, MaxOutputTokens: 100, Endpoints: 3, ZDREndpoints: 2,
		// Every number is exact in binary, so the compiled ratios can be
		// asserted exactly rather than with a tolerance.
		All: map[string]catalog.Bound{
			"input_per_m":      bound("1", "4"),
			"output_per_m":     bound("2", "8"),
			"cache_read_per_m": bound("0.125", "2"),
		},
		ZDR: map[string]catalog.Bound{
			"input_per_m":      bound("2", "4"),
			"output_per_m":     bound("4", "8"),
			"cache_read_per_m": bound("0.5", "2"),
		},
	}
}

func route(requireZDR bool, policy string) []CatalogRoute {
	return []CatalogRoute{{Name: "openrouter", Models: []string{"vendor/model"}, RequireZDR: requireZDR, Policy: policy}}
}

func compile(t *testing.T, f *catalog.File, routes []CatalogRoute) *Table {
	t.Helper()
	got, err := ApplyCatalog(Embedded(), f, routes, catalogNow)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestApplyCatalogCompilesTierMinMax pins the four tier × policy
// combinations: the charged rates are the selected tier's selected bound,
// and each cache dimension is that bound's price over that bound's input
// price.
func TestApplyCatalogCompilesTierMinMax(t *testing.T) {
	cases := []struct {
		name       string
		requireZDR bool
		policy     string
		want       Rates
	}{
		{"all/cheapest", false, "cheapest", Rates{InputPerM: 1, OutputPerM: 2, CacheRead: 0.125, CacheWrite5m: DefaultCacheWrite5m, CacheWrite1h: DefaultCacheWrite1h}},
		{"all/highest", false, "highest", Rates{InputPerM: 4, OutputPerM: 8, CacheRead: 0.5, CacheWrite5m: DefaultCacheWrite5m, CacheWrite1h: DefaultCacheWrite1h}},
		{"zdr/cheapest", true, "cheapest", Rates{InputPerM: 2, OutputPerM: 4, CacheRead: 0.25, CacheWrite5m: DefaultCacheWrite5m, CacheWrite1h: DefaultCacheWrite1h}},
		{"zdr/highest", true, "highest", Rates{InputPerM: 4, OutputPerM: 8, CacheRead: 0.5, CacheWrite5m: DefaultCacheWrite5m, CacheWrite1h: DefaultCacheWrite1h}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, ok := compile(t, catalogFile("2026-10-14", entry()), route(c.requireZDR, c.policy)).Lookup("vendor/model")
			if !ok {
				t.Fatal("no row")
			}
			if m.Rates != c.want {
				t.Errorf("rates %+v, want %+v", m.Rates, c.want)
			}
			if m.ContextTokens != 1000 || m.MaxOutputTokens != 100 {
				t.Errorf("context %d, output %d; want the file's 1000 and 100", m.ContextTokens, m.MaxOutputTokens)
			}
			if m.ImageTokens != 0 {
				t.Errorf("ImageTokens is %d: a catalog row refuses images", m.ImageTokens)
			}
		})
	}
	// The zero-value policy is cheapest, not the accident of an unset field
	// reaching the wrong branch.
	m, _ := compile(t, catalogFile("2026-10-14", entry()), route(false, "")).Lookup("vendor/model")
	if m.Rates.InputPerM != 1 {
		t.Errorf("an empty policy charged input %v, want the cheapest bound 1", m.Rates.InputPerM)
	}
}

// TestApplyCatalogUsesWriteRatios pins that a tier which prices a write is
// charged its own ratio, not the table's default.
func TestApplyCatalogUsesWriteRatios(t *testing.T) {
	e := entry()
	e.All["cache_write_5m_per_m"] = bound("2.5", "10")
	e.All["cache_write_1h_per_m"] = bound("3", "12")
	got := compile(t, catalogFile("2026-10-14", e), route(false, "cheapest"))
	m, _ := got.Lookup("vendor/model")
	if m.Rates.CacheWrite5m != 2.5 {
		t.Errorf("CacheWrite5m is %v, want 2.5 (2.5/1, the tier's own ratio, not the 1.25 default)", m.Rates.CacheWrite5m)
	}
	if m.Rates.CacheWrite1h != 3 {
		t.Errorf("CacheWrite1h is %v, want 3", m.Rates.CacheWrite1h)
	}
	m, _ = compile(t, catalogFile("2026-10-14", e), route(false, "highest")).Lookup("vendor/model")
	if m.Rates.CacheWrite5m != 2.5 || m.Rates.CacheWrite1h != 3 {
		t.Errorf("at the highest bound the writes are %v/%v, want 10/4 and 12/4", m.Rates.CacheWrite5m, m.Rates.CacheWrite1h)
	}
}

// TestApplyCatalogOmitsDimensionUsesDefaults pins the fold for a tier that
// prices no cache dimension, asserted as values: a cache read is exactly
// 1.0 — never 0, which would make it free, and never DefaultCacheRead —
// and the writes are exactly 1.25 and 2, never 1.0.
func TestApplyCatalogOmitsDimensionUsesDefaults(t *testing.T) {
	e := entry()
	delete(e.All, "cache_read_per_m")
	delete(e.ZDR, "cache_read_per_m")
	got := compile(t, catalogFile("2026-10-14", e), route(false, "cheapest"))
	m, _ := got.Lookup("vendor/model")
	if m.Rates.CacheRead != 1.0 {
		t.Errorf("CacheRead is %v, want exactly 1.0 (the input price), not 0 and not DefaultCacheRead %v", m.Rates.CacheRead, DefaultCacheRead)
	}
	if m.Rates.CacheWrite5m != DefaultCacheWrite5m || m.Rates.CacheWrite1h != DefaultCacheWrite1h {
		t.Errorf("the writes are %v/%v, want %v/%v", m.Rates.CacheWrite5m, m.Rates.CacheWrite1h, DefaultCacheWrite5m, DefaultCacheWrite1h)
	}
	if m.Rates.WebSearchPer1k != 0 {
		t.Errorf("WebSearchPer1k is %v, want 0: a tier that prices no search charges none", m.Rates.WebSearchPer1k)
	}
}

// TestApplyCatalogCarriesWebSearch pins the one dimension with no fold: a
// tier that prices a search charges it.
func TestApplyCatalogCarriesWebSearch(t *testing.T) {
	e := entry()
	e.All["web_search_per_1k"] = bound("10", "12")
	m, _ := compile(t, catalogFile("2026-10-14", e), route(false, "cheapest")).Lookup("vendor/model")
	if m.Rates.WebSearchPer1k != 10 {
		t.Errorf("WebSearchPer1k is %v, want 10", m.Rates.WebSearchPer1k)
	}
}

// TestApplyCatalogRefusesMultiplierAboveMax pins that a ratio the table
// cannot express is refused with the model, the dimension and both prices,
// never clamped.
func TestApplyCatalogRefusesMultiplierAboveMax(t *testing.T) {
	e := entry()
	e.All["cache_read_per_m"] = bound("0.1", "100") // 100 / 4 = 25x, over the limit of 10
	_, err := ApplyCatalog(Embedded(), catalogFile("2026-10-14", e), route(false, "highest"), catalogNow)
	if err == nil {
		t.Fatal("a 25x cache read was accepted")
	}
	for _, want := range []string{"vendor/model", "cache_read_per_m", "2026-10-14"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
	// Clamping would have priced it at 10x: it must not have.
	if strings.Contains(err.Error(), "clamp") {
		t.Errorf("the error suggests clamping: %v", err)
	}
}

// TestApplyCatalogStaleForcesTierMax pins design D10: past the catalog's
// own 30-day threshold the bound is the selected tier's maximum whatever
// the policy asked for, and the row says it is stale. Inverting or dropping
// the check fails here.
func TestApplyCatalogStaleForcesTierMax(t *testing.T) {
	f := catalogFile("2026-01-01", entry()) // 292 days before catalogNow
	m, _ := compile(t, f, route(false, "cheapest")).Lookup("vendor/model")
	if m.Rates.InputPerM != 4 || m.Rates.OutputPerM != 8 {
		t.Errorf("a stale catalog charged %v/%v under cheapest, want the tier maximum 4/8", m.Rates.InputPerM, m.Rates.OutputPerM)
	}
	if m.Catalog == nil || !m.Catalog.Stale {
		t.Errorf("the row is not marked stale: %+v", m.Catalog)
	}
	if m.Catalog.Bound != "max" {
		t.Errorf("the stale row's bound is %q, want max", m.Catalog.Bound)
	}
	// Inside the window the policy's own bound stands.
	fresh := catalogFile("2026-10-14", entry())
	m, _ = compile(t, fresh, route(false, "cheapest")).Lookup("vendor/model")
	if m.Rates.InputPerM != 1 {
		t.Errorf("a six-day-old catalog charged %v under cheapest, want 1", m.Rates.InputPerM)
	}
	// The threshold is 30 days, not the Claude table's 90: 31 days is stale.
	m, _ = compile(t, catalogFile("2026-09-19", entry()), route(false, "cheapest")).Lookup("vendor/model")
	if !m.Catalog.Stale {
		t.Error("a 31-day-old catalog is not stale")
	}
}

// TestApplyCatalogRefusesEmptyZDRTier pins design D9: a require_zdr route
// whose model has no ZDR-eligible endpoint refuses rather than pricing an
// empty set, and nothing falls back to the all tier.
func TestApplyCatalogRefusesEmptyZDRTier(t *testing.T) {
	e := entry()
	e.ZDR, e.ZDREndpoints = nil, 0
	f := catalogFile("2026-10-14", e)
	_, err := ApplyCatalog(Embedded(), f, route(true, "cheapest"), catalogNow)
	if err == nil {
		t.Fatal("a require_zdr route on a model with no ZDR tier was priced")
	}
	for _, want := range []string{"vendor/model", "require_zdr", "model_prices"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
	// The same model is fine on require_zdr: false.
	if m, ok := compile(t, f, route(false, "cheapest")).Lookup("vendor/model"); !ok || m.Catalog.Tier != "all" {
		t.Errorf("require_zdr: false did not price the all tier: %+v", m.Catalog)
	}
}

// TestApplyCatalogSetsProvenance pins the row's provenance: the tier, the
// bound, the endpoint counts, the catalog's own source and date.
func TestApplyCatalogSetsProvenance(t *testing.T) {
	m, _ := compile(t, catalogFile("2026-10-14", entry()), route(true, "highest")).Lookup("vendor/model")
	c := m.Catalog
	if c == nil {
		t.Fatal("no provenance")
	}
	want := CatalogPrice{Tier: "zdr", Bound: "max", Endpoints: 3, ZdrEndpoints: 2,
		Source: "https://openrouter.ai/api/v1/models", CheckedAt: "2026-10-14"}
	if *c != want {
		t.Errorf("provenance %+v, want %+v", *c, want)
	}
}

// TestOverrideClearsCatalogProvenance pins that an owner's model_prices
// replaces the row whole: the rates are the override's and the catalog's
// provenance is gone, exactly as Unverified and PriceSource are cleared.
func TestOverrideClearsCatalogProvenance(t *testing.T) {
	got := compile(t, catalogFile("2026-10-14", entry()), route(false, "cheapest"))
	m, _ := got.Lookup("vendor/model")
	if m.Catalog == nil {
		t.Fatal("no catalog row to override")
	}
	o := Overrides{"vendor/model": {InputPerM: 7, OutputPerM: 9, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.5}}
	with, err := got.With(o)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = with.Lookup("vendor/model")
	if m.Catalog != nil {
		t.Errorf("the override left the catalog provenance: %+v", m.Catalog)
	}
	if m.Rates.InputPerM != 7 || m.Rates.OutputPerM != 9 || m.Rates.CacheRead != 0.5 {
		t.Errorf("rates %+v, want the override's whole row, not a merge with the catalog's", m.Rates)
	}
}

// TestApplyCatalogLeavesUnpricedModelAlone pins the route scope: a route
// that claims a model the catalog does not price gets no row, so the
// existing refusal stands rather than a marketplace guess.
func TestApplyCatalogLeavesUnpricedModelAlone(t *testing.T) {
	routes := []CatalogRoute{{Name: "openrouter", Models: []string{"vendor/model", "vendor/other"}}}
	got := compile(t, catalogFile("2026-10-14", entry()), routes)
	if _, ok := got.Lookup("vendor/other"); ok {
		t.Error("a model the catalog does not price got a row")
	}
	if _, ok := got.Lookup("vendor/model"); !ok {
		t.Error("the priced model got no row")
	}
	// A route with no resolved model adds nothing at all.
	empty := compile(t, catalogFile("2026-10-14", entry()), []CatalogRoute{{Name: "openrouter"}})
	if len(empty.Models) != len(Embedded().Models) {
		t.Errorf("a route with no model changed the table: %d rows, want %d", len(empty.Models), len(Embedded().Models))
	}
}

// TestApplyCatalogNoFileIsTodayBehavior pins that a machine with no catalog
// (a nil file, or a build without one) prices exactly as before.
func TestApplyCatalogNoFileIsTodayBehavior(t *testing.T) {
	for _, f := range []*catalog.File{nil, {Version: 1, CheckedAt: "2026-10-14", Models: map[string]catalog.Entry{}}} {
		got, err := ApplyCatalog(Embedded(), f, route(false, "cheapest"), catalogNow)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Models) != len(Embedded().Models) {
			t.Errorf("%d rows, want the embedded table's %d", len(got.Models), len(Embedded().Models))
		}
	}
}

// TestShippedCatalogCompilesAllCombos loads the checked-in catalog.json and
// compiles every row under all four tier × policy combinations. A shipped
// row that trips the MaxMultiplier refusal or an empty tier is a test
// failure here rather than a refusal on someone's first run.
func TestShippedCatalogCompilesAllCombos(t *testing.T) {
	f, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(f.Models))
	for id := range f.Models {
		ids = append(ids, id)
	}
	for _, id := range ids {
		for _, requireZDR := range []bool{false, true} {
			for _, policy := range []string{"cheapest", "highest"} {
				routes := []CatalogRoute{{Name: "openrouter", Models: []string{id}, RequireZDR: requireZDR, Policy: policy}}
				got, err := ApplyCatalog(Embedded(), f, routes, catalogNow)
				noZDR := len(f.Models[id].ZDR) == 0
				if err != nil {
					if requireZDR && noZDR {
						// The P8 case: this is the refusal, not a failure.
						if !strings.Contains(err.Error(), "require_zdr") {
							t.Errorf("%s: the refusal does not name the switch: %v", id, err)
						}
						continue
					}
					t.Errorf("%s require_zdr=%v policy=%s: %v", id, requireZDR, policy, err)
					continue
				}
				if requireZDR && noZDR {
					t.Errorf("%s has no zdr tier but a require_zdr route priced it", id)
					continue
				}
				m, ok := got.Lookup(id)
				if !ok {
					t.Errorf("%s: no row", id)
					continue
				}
				if m.ImageTokens != 0 {
					t.Errorf("%s: ImageTokens is %d, want 0 (images refused)", id, m.ImageTokens)
				}
				if m.Catalog == nil || m.Catalog.Tier == "" || m.Catalog.Bound == "" {
					t.Errorf("%s: no provenance", id)
					continue
				}
				if m.Rates.Validate() != nil {
					t.Errorf("%s: the compiled rates are invalid: %v", id, m.Rates.Validate())
				}
			}
		}
	}
}
