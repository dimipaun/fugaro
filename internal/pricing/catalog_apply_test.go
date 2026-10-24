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
// cannot express gets no row, with the model, the dimension and both prices
// in the recorded refusal, never a clamp.
func TestApplyCatalogRefusesMultiplierAboveMax(t *testing.T) {
	e := entry()
	e.All["cache_read_per_m"] = bound("0.1", "100") // 100 / 4 = 25x, over the limit of 10
	got := compile(t, catalogFile("2026-10-14", e), route(false, "highest"))
	if _, ok := got.Lookup("vendor/model"); ok {
		t.Fatal("a 25x cache read was accepted")
	}
	why := got.Refusal("vendor/model")
	for _, want := range []string{"vendor/model", "cache_read_per_m", "2026-10-14"} {
		if !strings.Contains(why, want) {
			t.Errorf("the refusal does not name %q: %v", want, why)
		}
	}
	// Clamping would have priced it at 10x: it must not have.
	if strings.Contains(why, "clamp") {
		t.Errorf("the refusal suggests clamping: %v", why)
	}
}

// TestApplyCatalogRefusesATierWithNoInputOrOutput pins the reachable
// catalogRates branch for the two dimensions every row needs. The loader
// checks that a tier prices *something* (catalog.validate), not that it
// prices these two, so a file whose tier prices only a cache dimension
// passes Load and arrives here -- where a row without them would price every
// token at nothing. It is a refusal of that model, never a failed table
// (design D5, P8): the reason is on the table for the surfaces that name the
// model, and every other row still prices.
func TestApplyCatalogRefusesATierWithNoInputOrOutput(t *testing.T) {
	for _, c := range []struct {
		name string
		tier map[string]catalog.Bound
		want string
	}{
		{"no input", map[string]catalog.Bound{"output_per_m": bound("2", "8")}, "prices no input_per_m"},
		{"no output", map[string]catalog.Bound{"input_per_m": bound("1", "4")}, "prices no output_per_m"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := entry()
			e.All, e.ZDR = c.tier, nil
			got := compile(t, catalogFile("2026-10-14", e), route(false, "cheapest"))
			if _, priced := got.Lookup("vendor/model"); priced {
				t.Fatal("a tier with no input or output price got a row")
			}
			why := got.Refusal("vendor/model")
			for _, want := range []string{"vendor/model", c.want} {
				if !strings.Contains(why, want) {
					t.Errorf("the refusal does not name %q: %v", want, why)
				}
			}
			// The refusal is that model's alone: the embedded rows are
			// still priced, so one unpriceable row cannot fail a run.
			if len(got.Models) != len(Embedded().Models) {
				t.Errorf("%d rows, want the embedded table's %d", len(got.Models), len(Embedded().Models))
			}
		})
	}
}

// TestApplyCatalogErrorsOnAMalformedFile pins the one thing ApplyCatalog
// itself fails on (its doc comment's error contract): a file it cannot read
// as a catalog at all -- here a checked_at that is not a date -- is an
// error, and no table comes back. A model it merely cannot price is the
// refusal above, never this.
func TestApplyCatalogErrorsOnAMalformedFile(t *testing.T) {
	f := catalogFile("the 14th of October", entry())
	got, err := ApplyCatalog(Embedded(), f, route(false, "cheapest"), catalogNow)
	if err == nil {
		t.Fatalf("a catalog with no readable checked_at compiled: %d rows", len(got.Models))
	}
	if got != nil {
		t.Error("a failed build returned a table")
	}
	for _, want := range []string{"malformed", "checked_at", "the 14th of October"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
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
// whose model has no ZDR-eligible endpoint gets no row, and the table records
// why — the model, the switch and both ways out — rather than pricing an
// empty set. Nothing falls back to the all tier, and Lookup fails, so nothing
// can be charged for it either.
func TestApplyCatalogRefusesEmptyZDRTier(t *testing.T) {
	e := entry()
	e.ZDR, e.ZDREndpoints = nil, 0
	f := catalogFile("2026-10-14", e)
	got := compile(t, f, route(true, "cheapest"))
	if _, ok := got.Lookup("vendor/model"); ok {
		t.Fatal("a require_zdr route on a model with no ZDR tier was priced")
	}
	why := got.Refusal("vendor/model")
	if why == "" {
		t.Fatal("the refusal was not recorded")
	}
	for _, want := range []string{"vendor/model", "require_zdr", "model_prices"} {
		if !strings.Contains(why, want) {
			t.Errorf("the refusal does not name %q: %v", want, why)
		}
	}
	// The same model is fine on require_zdr: false.
	if m, ok := compile(t, f, route(false, "cheapest")).Lookup("vendor/model"); !ok || m.Catalog.Tier != "all" {
		t.Errorf("require_zdr: false did not price the all tier: %+v", m.Catalog)
	}
}

// TestApplyCatalogRefusesOnlyTheModelNamed pins the scope of the refusal: a
// route's pattern claims a family, and a model of that family the catalog
// cannot price is refused where it is named — not by failing the table for
// every other model the same route claims. A model nobody names must not
// make a run's table unbuildable, or one new non-ZDR model at OpenRouter
// would break every default route on upgrade.
func TestApplyCatalogRefusesOnlyTheModelNamed(t *testing.T) {
	e := entry()
	e.ZDR, e.ZDREndpoints = nil, 0 // "vendor/model" has no ZDR tier
	f := catalogFile("2026-10-14", e)
	ok := entry() // "vendor/other" has both tiers
	f.Models["vendor/other"] = ok
	r := []CatalogRoute{{Name: "openrouter", Models: []string{"vendor/model", "vendor/other"}, RequireZDR: true}}
	got := compile(t, f, r)
	if _, priced := got.Lookup("vendor/other"); !priced {
		t.Fatal("the model the catalog can price got no row: one unpriceable model failed the table")
	}
	if _, priced := got.Lookup("vendor/model"); priced {
		t.Fatal("the model with no ZDR tier was priced anyway")
	}
	if got.Refusal("vendor/model") == "" {
		t.Error("the refused model is not recorded")
	}
	if got.Refusal("vendor/other") != "" {
		t.Errorf("a priced model carries a refusal: %q", got.Refusal("vendor/other"))
	}
	// The same route with the switch off prices both, so the refusal is
	// about the tier and not about the family.
	both := compile(t, f, []CatalogRoute{{Name: "openrouter", Models: []string{"vendor/model", "vendor/other"}, RequireZDR: false}})
	for _, id := range []string{"vendor/model", "vendor/other"} {
		if _, priced := both.Lookup(id); !priced {
			t.Errorf("require_zdr: false did not price %s", id)
		}
	}
}

// TestApplyCatalogRefusalClearedByOverride pins the way out: an owner who
// prices a refused model under model_prices gets a row, and the refusal goes
// with it — the surfaces judge the pin by the table, so a stale refusal would
// refuse a model that now has a price.
func TestApplyCatalogRefusalClearedByOverride(t *testing.T) {
	e := entry()
	e.ZDR, e.ZDREndpoints = nil, 0
	f := catalogFile("2026-10-14", e)
	tbl, err := ApplyCatalog(Embedded(), f, route(true, "cheapest"), catalogNow)
	if err != nil {
		t.Fatal(err)
	}
	if tbl.Refusal("vendor/model") == "" {
		t.Fatal("no refusal to clear")
	}
	with, err := tbl.With(Overrides{"vendor/model": {InputPerM: 7, OutputPerM: 9}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := with.Lookup("vendor/model"); !ok {
		t.Fatal("the override did not price the model")
	}
	if why := with.Refusal("vendor/model"); why != "" {
		t.Errorf("the refusal survived the override: %q", why)
	}
	// The raw list too, not only the answer Refusal gives: Refusal consults
	// the rows first, so an override that priced the model but left the
	// reason in the map would still answer "" here while every surface that
	// reads the list directly — budget prices prints it — kept reporting a
	// model the owner has priced.
	if why, ok := with.Refused["vendor/model"]; ok {
		t.Errorf("the override left the reason in the table's refusal list: %q", why)
	}
	// And the refusal is still on the table it came from: With copies.
	if tbl.Refusal("vendor/model") == "" {
		t.Error("With cleared the refusal on the table it was called on")
	}
}

// TestApplyCatalogSetsProvenance pins the row's provenance: the tier, the
// bound, the endpoint counts, the catalog's own source and date, and the
// tier's dearest prices, which are what the operator surfaces size the
// undercharge with.
func TestApplyCatalogSetsProvenance(t *testing.T) {
	m, _ := compile(t, catalogFile("2026-10-14", entry()), route(true, "highest")).Lookup("vendor/model")
	c := m.Catalog
	if c == nil {
		t.Fatal("no provenance")
	}
	want := CatalogPrice{Tier: "zdr", Bound: "max", Endpoints: 3, ZdrEndpoints: 2,
		Source: "https://openrouter.ai/api/v1/models", CheckedAt: "2026-10-14",
		TierInputMax: 4, TierOutputMax: 8}
	if *c != want {
		t.Errorf("provenance %+v, want %+v", *c, want)
	}
}

// TestCatalogRangeSizesTheUndercharge pins the numbers the operator surfaces
// print for a row charged at the cheapest end: what the dearest endpoint of
// the tier prices and the multiple of the price charged, so the size of
// the undercharge is on the surface rather than only in a document (design
// D6, P11). A row already at the dearest end has no undercharge to size.
func TestCatalogRangeSizesTheUndercharge(t *testing.T) {
	m, _ := compile(t, catalogFile("2026-10-14", entry()), route(true, "cheapest")).Lookup("vendor/model")
	want := "a call served by the dearest endpoint of the tier can be billed $4/M in and $8/M out, 2.0x the input price charged and 2.0x the output price charged"
	if got := m.CatalogRange(); got != want {
		t.Errorf("CatalogRange() = %q, want %q", got, want)
	}
	// The multiple is the figure the docs quote, so it is rounded to the one
	// decimal they state: the shipped deepseek row's zdr tier is 8.571...x
	// on input and 13.333...x on output, printed as 8.6x and 13.3x (design
	// §2, P11), and a raw float64 would put "8.571428571428571x" on the
	// operator's screen beside a document saying 8.6x. The fixture below
	// keeps the 2026-10-14 row (0.01584 charged) that the figure was first
	// written for, so the rounding it pins is the harsher one: 37.878...x.
	rounded := catalogFile("2026-10-14", catalog.Entry{
		ContextTokens: 1000, MaxOutputTokens: 100, Endpoints: 3, ZDREndpoints: 2,
		All: map[string]catalog.Bound{
			"input_per_m":  bound("0.01584", "0.6"),
			"output_per_m": bound("0.18", "2.4"),
		},
		ZDR: map[string]catalog.Bound{
			"input_per_m":  bound("0.01584", "0.6"),
			"output_per_m": bound("0.18", "2.4"),
		},
	})
	m, _ = compile(t, rounded, route(true, "cheapest")).Lookup("vendor/model")
	want = "a call served by the dearest endpoint of the tier can be billed $0.6/M in and $2.4/M out, 37.9x the input price charged and 13.3x the output price charged"
	if got := m.CatalogRange(); got != want {
		t.Errorf("CatalogRange() = %q, want %q", got, want)
	}
	// A tier whose dearest endpoint prices what is already charged has no
	// undercharge, so the multiple is left out rather than printed as "1.0x"
	// — a warning with no number behind it (the shipped qwen/qwen3.8-flash
	// row, which has one endpoint per dimension and no zdr tier at all).
	flat := catalogFile("2026-10-14", catalog.Entry{
		ContextTokens: 1000, MaxOutputTokens: 100, Endpoints: 1, ZDREndpoints: 0,
		All: map[string]catalog.Bound{
			"input_per_m":  bound("0.5", "0.5"),
			"output_per_m": bound("1", "1"),
		},
	})
	m, _ = compile(t, flat, route(false, "cheapest")).Lookup("vendor/model")
	want = "a call served by the dearest endpoint of the tier can be billed $0.5/M in and $1/M out"
	if got := m.CatalogRange(); got != want {
		t.Errorf("CatalogRange() = %q, want %q", got, want)
	}
	high, _ := compile(t, catalogFile("2026-10-14", entry()), route(true, "highest")).Lookup("vendor/model")
	if got := high.CatalogRange(); got != "" {
		t.Errorf("a row at the dearest bound sizes an undercharge: %q", got)
	}
	// A hand-priced row has no catalog provenance, and so no range: the
	// owner's number is the price, not the tier's range.
	ov, err := Embedded().With(Overrides{"vendor/model": {InputPerM: 7, OutputPerM: 9}})
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := ov.Lookup("vendor/model"); m.CatalogRange() != "" {
		t.Errorf("an override kept the catalog's range: %q", m.CatalogRange())
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
// compiles every row under all four tier × policy combinations. A shipped row
// that trips the MaxMultiplier refusal is a test failure here rather than a
// refusal on someone's first run, and a model whose ZDR tier is empty under
// require_zdr gets no row and a recorded refusal naming the switch.
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
				if err != nil {
					t.Errorf("%s require_zdr=%v policy=%s: %v", id, requireZDR, policy, err)
					continue
				}
				noZDR := len(f.Models[id].ZDR) == 0
				if requireZDR && noZDR {
					// The P8 case: no row, and the refusal names the switch
					// and both ways out — never a table that fails.
					if _, priced := got.Lookup(id); priced {
						t.Errorf("%s has no zdr tier but a require_zdr route priced it", id)
					}
					why := got.Refusal(id)
					if !strings.Contains(why, "require_zdr") || !strings.Contains(why, "model_prices") {
						t.Errorf("%s: the refusal does not name the switch and both ways out: %q", id, why)
					}
					continue
				}
				if why := got.Refusal(id); why != "" {
					t.Errorf("%s: refused though the catalog prices it: %q", id, why)
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
