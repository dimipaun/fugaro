package config

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/pricing/catalog"
)

// catNow is the clock every test here builds a table at, so no test depends
// on the day it runs. The fixture catalog is dated 2026-10-14, six days
// before it: inside the catalog's own 30-day window.
var catNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// catModel is the fixture model the route claims and the catalog prices. The
// numbers are exact in binary, so the compiled ratios can be asserted as
// values rather than with a tolerance: every cache read is half the input
// price at every tier and bound.
const catModel = "deepseek/deepseek-fixture"

// fixtureCatalog is a one-model catalog over two tiers: the all-endpoints set
// (input 0.25..1, output 0.5..2) and the ZDR subset (input 0.5..1, output
// 1..2), each with a cache read at half the input price and no write and no
// search — the shape the DeepSeek rows of the shipped file have.
func fixtureCatalog(checkedAt string) *catalog.File {
	bound := func(min, max string) catalog.Bound {
		return catalog.Bound{Min: min, Max: max, MinOf: "a", MaxOf: "b"}
	}
	return &catalog.File{
		Version: catalog.Version, CheckedAt: checkedAt,
		Sources: []string{"https://openrouter.ai/api/v1/models"},
		Models: map[string]catalog.Entry{catModel: {
			ContextTokens: 200_000, MaxOutputTokens: 64_000, Endpoints: 3, ZDREndpoints: 2,
			All: map[string]catalog.Bound{
				"input_per_m":      bound("0.25", "1"),
				"output_per_m":     bound("0.5", "2"),
				"cache_read_per_m": bound("0.125", "0.5"),
			},
			ZDR: map[string]catalog.Bound{
				"input_per_m":      bound("0.5", "1"),
				"output_per_m":     bound("1", "2"),
				"cache_read_per_m": bound("0.25", "0.5"),
			},
		}},
	}
}

// providers is the owner's route table with the fixture route: OpenRouter,
// claiming the fixture model, with mut applied to the two switches.
func providers(mut func(*ModelProvider)) map[string]ModelProvider {
	p := okProvider()
	p.Models = []string{"deepseek/*"}
	if mut != nil {
		mut(&p)
	}
	return map[string]ModelProvider{"openrouter": p}
}

func mustTable(t *testing.T, ps map[string]ModelProvider, ov pricing.Overrides, cat *catalog.File) *pricing.Table {
	t.Helper()
	tbl, err := PriceTable(ps, ov, cat, catNow)
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

// usage is one fixed call, so two tables' charges are comparable as numbers.
var usage = pricing.Usage{Input: 1000, Output: 1000}

// TestCatalogSkipsNonOpenRouterRoute pins the route eligibility (design D5,
// plan P6): a vendor's own endpoint never picks up OpenRouter's marketplace
// price for a model ID it happens to serve too. The same route at OpenRouter
// does get the row, so the test fails if the host check is dropped rather
// than merely never passing.
func TestCatalogSkipsNonOpenRouterRoute(t *testing.T) {
	for _, url := range []string{"https://api.deepseek.com/anthropic", "https://openrouter.ai.evil.test/api", "https://bedrock-runtime.us-east-1.amazonaws.com"} {
		ps := providers(func(p *ModelProvider) { p.BaseURL = url })
		tbl := mustTable(t, ps, nil, fixtureCatalog("2026-10-14"))
		if m, ok := tbl.Lookup(catModel); ok {
			t.Errorf("base_url %s: the catalog priced %s at %v/%v", url, catModel, m.Rates.InputPerM, m.Rates.OutputPerM)
		}
	}
	tbl := mustTable(t, providers(nil), nil, fixtureCatalog("2026-10-14"))
	if m, ok := tbl.Lookup(catModel); !ok || m.Catalog == nil {
		t.Fatalf("the OpenRouter route did not price %s: %+v", catModel, m.Catalog)
	}
}

// TestCatalogSkipsUnclaimedModel pins the other half of the route scope: a
// model no route's patterns claim gets no row, so a pin on it is still
// refused rather than priced from a marketplace guess.
func TestCatalogSkipsUnclaimedModel(t *testing.T) {
	cat := fixtureCatalog("2026-10-14")
	// The catalog prices a second model, which the route's patterns do not
	// claim (and a Claude row, which no provider may claim at all).
	e := cat.Models[catModel]
	e.ContextTokens, e.MaxOutputTokens = 1000, 100
	cat.Models["qwen/qwen-fixture"] = e
	tbl := mustTable(t, providers(nil), nil, cat)
	if _, ok := tbl.Lookup("qwen/qwen-fixture"); ok {
		t.Error("a model no route claims was priced")
	}
	if _, ok := tbl.Lookup(catModel); !ok {
		t.Error("the claimed model got no row")
	}
	// With no provider route at all the table is the embedded one: today's
	// behavior, including today's refusal of a model with no price.
	bare := mustTable(t, nil, nil, cat)
	if len(bare.Models) != len(pricing.Embedded().Models) {
		t.Errorf("a machine with no route got %d rows, want the embedded table's %d", len(bare.Models), len(pricing.Embedded().Models))
	}
	if _, ok := bare.Lookup(catModel); ok {
		t.Error("a machine with no route priced a provider model")
	}
}

// TestRequireZDRFalseUsesAllTier pins the switch by the rate charged, not by
// the field surviving a round trip (Review Focus 3): unset prices the
// ZDR-eligible endpoints, false prices every endpoint the model is served by,
// and the two charges differ.
func TestRequireZDRFalseUsesAllTier(t *testing.T) {
	zdr := mustTable(t, providers(nil), nil, fixtureCatalog("2026-10-14"))
	m, ok := zdr.Lookup(catModel)
	if !ok {
		t.Fatal("no row")
	}
	if m.Catalog.Tier != "zdr" {
		t.Errorf("an unset require_zdr priced the %q tier, want zdr", m.Catalog.Tier)
	}
	if got := m.Rates.Cost(usage); got != 1500 {
		t.Errorf("require_zdr unset charged %d µ$ for 1000/1000 tokens, want 1500 (input 0.5, output 1)", got)
	}
	no := false
	all := mustTable(t, providers(func(p *ModelProvider) { p.RequireZDR = &no }), nil, fixtureCatalog("2026-10-14"))
	m, _ = all.Lookup(catModel)
	if m.Catalog.Tier != "all" {
		t.Errorf("require_zdr: false priced the %q tier, want all", m.Catalog.Tier)
	}
	if got := m.Rates.Cost(usage); got != 750 {
		t.Errorf("require_zdr: false charged %d µ$ for 1000/1000 tokens, want 750 (input 0.25, output 0.5)", got)
	}
	if m.Rates.CacheRead != 0.5 {
		t.Errorf("the all tier's cache read is %v, want 0.5 (0.125/0.25)", m.Rates.CacheRead)
	}
}

// TestPricePolicyHighestChargesTierMax pins the second switch by the exact
// charge (Review Focus 4): highest charges the top of the selected tier's
// range, not the same number as cheapest.
func TestPricePolicyHighestChargesTierMax(t *testing.T) {
	cheap := mustTable(t, providers(nil), nil, fixtureCatalog("2026-10-14"))
	m, _ := cheap.Lookup(catModel)
	if m.Catalog.Bound != "min" {
		t.Errorf("the default policy charged the %q bound, want min", m.Catalog.Bound)
	}
	if got := m.Rates.Cost(usage); got != 1500 {
		t.Errorf("cheapest charged %d µ$, want 1500", got)
	}
	high := mustTable(t, providers(func(p *ModelProvider) { p.PricePolicy = PricePolicyHighest }), nil, fixtureCatalog("2026-10-14"))
	m, _ = high.Lookup(catModel)
	if m.Catalog.Bound != "max" {
		t.Errorf("price_policy: highest charged the %q bound, want max", m.Catalog.Bound)
	}
	if got := m.Rates.Cost(usage); got != 3000 {
		t.Errorf("highest charged %d µ$, want 3000 (input 1, output 2)", got)
	}
}

// TestPricePolicyZeroValueIsCheapest pins that the default is the switch's
// own default and not the accident of an unset field reaching the wrong
// branch: an empty price_policy and an explicit cheapest are one table.
func TestPricePolicyZeroValueIsCheapest(t *testing.T) {
	unset := mustTable(t, providers(nil), nil, fixtureCatalog("2026-10-14"))
	explicit := mustTable(t, providers(func(p *ModelProvider) { p.PricePolicy = PricePolicyCheapest }), nil, fixtureCatalog("2026-10-14"))
	a, _ := unset.Lookup(catModel)
	b, _ := explicit.Lookup(catModel)
	if a.Rates != b.Rates || a.Catalog.Bound != b.Catalog.Bound {
		t.Errorf("unset priced %+v (%s), cheapest %+v (%s)", a.Rates, a.Catalog.Bound, b.Rates, b.Catalog.Bound)
	}
	if a.Rates.InputPerM != 0.5 {
		t.Errorf("the default policy priced input at %v, want the zdr minimum 0.5", a.Rates.InputPerM)
	}
}

// TestStaleCatalogChargesTierMax pins design D10: past the catalog's own
// 30-day threshold the charge is the selected tier's maximum whatever the
// policy asked for, and the row says it is stale. Inverting or dropping the
// check fails here, in the direction that matters (an old catalog must
// over-count, never under-price).
func TestStaleCatalogChargesTierMax(t *testing.T) {
	stale := mustTable(t, providers(nil), nil, fixtureCatalog("2026-03-01")) // 233 days before catNow
	m, _ := stale.Lookup(catModel)
	if got := m.Rates.Cost(usage); got != 3000 {
		t.Errorf("a stale catalog charged %d µ$ under cheapest, want the tier maximum 3000", got)
	}
	if m.Catalog == nil || !m.Catalog.Stale || m.Catalog.Bound != "max" {
		t.Errorf("the stale row is not marked: %+v", m.Catalog)
	}
	// Inside the window the policy's own bound stands.
	fresh := mustTable(t, providers(nil), nil, fixtureCatalog("2026-09-25")) // 25 days
	m, _ = fresh.Lookup(catModel)
	if m.Catalog.Stale || m.Rates.Cost(usage) != 1500 {
		t.Errorf("a 25-day-old catalog charged %d µ$ (stale %v), want the cheapest 1500", m.Rates.Cost(usage), m.Catalog.Stale)
	}
	// 31 days is stale: the threshold is the catalog's 30 days, not the
	// Claude table's 90.
	old := mustTable(t, providers(nil), nil, fixtureCatalog("2026-09-19"))
	if m, _ := old.Lookup(catModel); !m.Catalog.Stale {
		t.Error("a 31-day-old catalog is not stale")
	}
}

// TestOverrideBeatsCatalog pins Review Focus 1: the owner's model_prices is
// applied last and replaces the row whole — the charged rates are the
// override's, not a merge of the override's input with the catalog's output —
// and the catalog's provenance is gone with it.
func TestOverrideBeatsCatalog(t *testing.T) {
	ov := pricing.Overrides{catModel: {InputPerM: 7, OutputPerM: 9, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.5}}
	tbl := mustTable(t, providers(nil), ov, fixtureCatalog("2026-10-14"))
	m, ok := tbl.Lookup(catModel)
	if !ok {
		t.Fatal("no row")
	}
	want := pricing.Rates{InputPerM: 7, OutputPerM: 9, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.5}
	if m.Rates != want {
		t.Errorf("rates %+v, want the override's %+v", m.Rates, want)
	}
	if m.Catalog != nil {
		t.Errorf("the override left the catalog provenance: %+v", m.Catalog)
	}
	if got := m.Rates.Cost(usage); got != 16_000 {
		t.Errorf("the charge is %d µ$, want the override's 16000", got)
	}
	// A model the override prices is not refused by the route's switches: an
	// owner's number is the other way out of a route the catalog cannot
	// price (design D9).
	if _, err := PriceTable(providers(nil), ov, noZDRCatalog(), catNow); err != nil {
		t.Errorf("a hand-priced model on a require_zdr route was refused: %v", err)
	}
}

// TestOverrideKeepsTheCatalogRowsLimits pins the reason a hand-priced catalog
// model still gets its row: Table.With replaces the rates and clears the
// provenance, but it keeps the row's limits — and a row the override created
// out of nothing has neither, so the gateway would fall back to the table's
// default output ceiling and stop refusing a max_tokens above the model's real
// maximum. The row is materialized for its limits alone; the rates are the
// override's, and a model the owner prices is still no refusal whatever the
// route's switches (design D9, P8).
func TestOverrideKeepsTheCatalogRowsLimits(t *testing.T) {
	ov := pricing.Overrides{catModel: {InputPerM: 7, OutputPerM: 9}}
	m, ok := mustTable(t, providers(nil), ov, fixtureCatalog("2026-10-14")).Lookup(catModel)
	if !ok {
		t.Fatal("no row")
	}
	if m.ContextTokens != 200_000 || m.MaxOutputTokens != 64_000 {
		t.Errorf("the overridden catalog row has context=%d output=%d, want the catalog's 200000/64000", m.ContextTokens, m.MaxOutputTokens)
	}
	if m.Rates.InputPerM != 7 || m.Rates.OutputPerM != 9 {
		t.Errorf("the rates are %v/%v, want the override's 7/9", m.Rates.InputPerM, m.Rates.OutputPerM)
	}
	if m.Catalog != nil {
		t.Errorf("the override left the catalog provenance: %+v", m.Catalog)
	}
	// A model the catalog does not price keeps the old shape: there is no row
	// to keep the limits of, so the override's row stands alone.
	elsewhere := pricing.Overrides{"qwen/qwen-elsewhere": {InputPerM: 1, OutputPerM: 2}}
	m, ok = mustTable(t, providers(nil), elsewhere, fixtureCatalog("2026-10-14")).Lookup("qwen/qwen-elsewhere")
	if !ok || m.ContextTokens != 0 || m.MaxOutputTokens != 0 {
		t.Errorf("a model the catalog does not price got limits: %+v", m)
	}
	// The D9 escape still works, and now keeps the limits too: a require_zdr
	// route whose model has no ZDR tier is refused, and an owner who prices
	// that model by hand gets a table (with the catalog's limits).
	no := providers(func(p *ModelProvider) { p.Models = []string{"deepseek/*"} })
	tbl, err := PriceTable(no, ov, noZDRCatalog(), catNow)
	if err != nil {
		t.Fatalf("a hand-priced model on a require_zdr route was refused: %v", err)
	}
	if m, _ := tbl.Lookup(catModel); m.ContextTokens != 200_000 || m.MaxOutputTokens != 64_000 {
		t.Errorf("the hand-priced way out lost the catalog's limits: context=%d output=%d", m.ContextTokens, m.MaxOutputTokens)
	}
}

// TestPinRefusedNamesTheSwitchAndBothWaysOut pins Review Focus 14 and design
// P8: a pin on a model the catalog cannot price under require_zdr is refused
// with the model, the switch and both ways out — the message is the whole fix
// path for the owner who hits it. Nothing falls back to the all tier.
func TestPinRefusedNamesTheSwitchAndBothWaysOut(t *testing.T) {
	tbl := mustTable(t, providers(nil), nil, noZDRCatalog())
	if _, ok := tbl.Lookup(catModel); ok {
		t.Fatal("a require_zdr route on a model with no ZDR tier was priced")
	}
	ps := CheckPins(pinned(catModel), tbl)
	if len(ps) == 0 {
		t.Fatal("the pin was not refused")
	}
	msg := ps[0].Message
	for _, want := range []string{catModel, "require_zdr", "require_zdr: false", "model_prices"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name %q: %v", want, msg)
		}
	}
	// The same route with require_zdr: false prices the all tier, so the
	// refusal is about the tier and not about the model.
	no := false
	tbl = mustTable(t, providers(func(p *ModelProvider) { p.RequireZDR = &no }), nil, noZDRCatalog())
	if m, ok := tbl.Lookup(catModel); !ok || m.Catalog.Tier != "all" {
		t.Errorf("require_zdr: false did not price the all tier: %+v", m.Catalog)
	}
	if ps := CheckPins(pinned(catModel), tbl); len(ps) != 0 {
		t.Errorf("a priced model was refused: %v", problems(ps))
	}
}

// TestUnnamedNonZDRModelDoesNotFailTheTable pins the scope of that refusal: a
// route's pattern claims a whole family, so a model of that family the
// catalog cannot price under require_zdr is refused where it is named — a
// pin, or a call — and never by failing the table for the models the run
// actually uses. A default `qwen/*` route is the shape: the shipped file
// carries one qwen model with no ZDR-eligible endpoint, and a run pinned to a
// hand-priced qwen model of the same family must still build its table.
func TestUnnamedNonZDRModelDoesNotFailTheTable(t *testing.T) {
	cat := fixtureCatalog("2026-10-14")
	// A second model of the family with no ZDR tier, as the shipped file's
	// qwen/qwen3.8-flash has none. Nothing names it.
	e := cat.Models[catModel]
	e.ZDR, e.ZDREndpoints = nil, 0
	cat.Models["deepseek/deepseek-no-zdr"] = e

	// The pin is the other model of the family, priced by hand: the owner's
	// own number is D9's second way out, so the table must build for it.
	ov := pricing.Overrides{catModel: {InputPerM: 7, OutputPerM: 9}}
	tbl := mustTable(t, providers(nil), ov, cat)
	m, ok := tbl.Lookup(catModel)
	if !ok || m.Rates.InputPerM != 7 {
		t.Fatalf("the pin is not priced by the override: %+v", m)
	}
	if m.ContextTokens != 200_000 || m.MaxOutputTokens != 64_000 {
		t.Errorf("the hand-priced model lost the catalog's limits: %+v", m)
	}
	if _, ok := tbl.Lookup("deepseek/deepseek-no-zdr"); ok {
		t.Error("a model no pin names was priced under require_zdr")
	}
	if ps := CheckPins(pinned(catModel), tbl); len(ps) != 0 {
		t.Errorf("a run pinned to the priceable model was refused: %v", problems(ps))
	}
	// Naming the model the catalog cannot price still refuses, with the
	// switch and both ways out.
	ps := CheckPins(pinned("deepseek/deepseek-no-zdr"), tbl)
	if len(ps) == 0 {
		t.Fatal("naming the model with no ZDR tier was not refused")
	}
	for _, want := range []string{"deepseek/deepseek-no-zdr", "require_zdr: false", "model_prices"} {
		if !strings.Contains(ps[0].Message, want) {
			t.Errorf("the refusal does not name %q: %v", want, ps[0].Message)
		}
	}
	// The gateway's backstop reads the same recorded refusal, so a call
	// naming the model is refused with the fix path rather than the generic
	// "has no price".
	if why := tbl.Refusal("deepseek/deepseek-no-zdr"); !strings.Contains(why, "require_zdr: false") {
		t.Errorf("the table records no refusal for the model: %q", why)
	}
}

// pinned is an Agent pinned to one model in all three roles, so CheckPins
// judges exactly that model.
func pinned(model string) Agent {
	return Agent{Models: ModelRoles{Coder: model, Reviewer: model, Background: model}}
}

// problems renders a Problem list for a test failure.
func problems(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.Path + ": " + p.Message + "; ")
	}
	return b.String()
}

// noZDRCatalog is the fixture with the ZDR tier removed, the shape a model
// OpenRouter serves only from data-retaining endpoints has.
func noZDRCatalog() *catalog.File {
	cat := fixtureCatalog("2026-10-14")
	e := cat.Models[catModel]
	e.ZDR, e.ZDREndpoints = nil, 0
	cat.Models[catModel] = e
	return cat
}

// TestShippedCatalogPricesTheRouteModels is the end-to-end sanity check: the
// catalog the binary embeds prices the DeepSeek rows an OpenRouter route
// claims, and qwen — which has no ZDR-eligible endpoint — is not a failure of
// the table: it gets no row, and a pin that names it is refused with the
// switch and both ways out, while require_zdr: false prices it. A shipped file
// that cannot compile is caught in package pricing; this catches a builder
// that never hands it the route.
func TestShippedCatalogPricesTheRouteModels(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	routes := providers(nil) // deepseek/*
	tbl := mustTable(t, routes, nil, cat)
	n := 0
	for id := range cat.Models {
		m, ok := tbl.Lookup(id)
		if !strings.HasPrefix(id, "deepseek/") {
			if ok {
				t.Errorf("%s is claimed by no route but was priced", id)
			}
			continue
		}
		if !ok {
			t.Errorf("%s is claimed by the route but has no row", id)
			continue
		}
		n++
		if m.Catalog == nil || m.Catalog.Tier != "zdr" || m.Catalog.Endpoints == 0 || m.Catalog.CheckedAt != cat.CheckedAt {
			t.Errorf("%s: provenance %+v", id, m.Catalog)
		}
	}
	if n == 0 {
		t.Fatal("the shipped catalog priced none of the route's models")
	}
	// The qwen route claims a model with no ZDR-eligible endpoint. Nothing
	// names it, so the table builds and the model simply has no row; a pin on
	// it is refused with the switch, not with a table that fails for every
	// qwen model the route claims.
	qwen := providers(func(p *ModelProvider) { p.Models = []string{"qwen/*"} })
	tbl = mustTable(t, qwen, nil, cat)
	if _, ok := tbl.Lookup("qwen/qwen3.8-flash"); ok {
		t.Error("a require_zdr route priced qwen: the shipped file gives it no ZDR tier")
	}
	ps := CheckPins(pinned("qwen/qwen3.8-flash"), tbl)
	if len(ps) == 0 || !strings.Contains(ps[0].Message, "require_zdr: false") {
		t.Errorf("a pin on qwen was not refused with the switch and both ways out: %v", problems(ps))
	}
	no := false
	qwen = providers(func(p *ModelProvider) { p.Models = []string{"qwen/*"}; p.RequireZDR = &no })
	if m, ok := mustTable(t, qwen, nil, cat).Lookup("qwen/qwen3.8-flash"); !ok || m.Catalog.Tier != "all" {
		t.Errorf("require_zdr: false did not price qwen: %+v", m.Catalog)
	}
}
