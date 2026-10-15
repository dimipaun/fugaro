package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
)

func pinnedAgent() Agent {
	return Agent{Models: ModelRoles{Coder: "claude-opus-5-5", Reviewer: "claude-sonnet-5-5", Background: "claude-haiku-4-5"}}
}

func paths(ps []Problem) string {
	var s []string
	for _, p := range ps {
		s = append(s, p.Path)
	}
	return strings.Join(s, ",")
}

// TestPinnedModelsAreTheThreePins pins the one list of a run's pins: the
// models PinnedModels returns are the three the paths above name, in the same
// order, with an empty name where the config pins nothing rather than the
// entry dropped. The runner asks for those three to decide what a stale
// catalog means for a run (internal/runner/gateway.go), so a set that drifted
// from the one CheckPins and PinWarnings judge would judge some other run.
func TestPinnedModelsAreTheThreePins(t *testing.T) {
	a := pinnedAgent()
	want := []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5"}
	if got := a.PinnedModels(); !slices.Equal(got, want) {
		t.Errorf("PinnedModels() = %q, want %q", got, want)
	}
	if len(a.PinnedModels()) != len(pinPaths) {
		t.Fatalf("PinnedModels returns %d models for %d paths", len(a.PinnedModels()), len(pinPaths))
	}
	// Every path in pinPaths is the path CheckPins reports for that pin.
	ps := CheckPins(a, pricing.Embedded())
	if got := paths(ps); got != "" {
		t.Fatalf("the fixture is not pinnable (%s): %q", got, ps)
	}
	// An agent.model with no role overrides pins the coder and the reviewer
	// to it, and the background model to nothing: the empty entry stays.
	b := Agent{Model: "claude-sonnet-5-5"}
	want = []string{"claude-sonnet-5-5", "claude-sonnet-5-5", ""}
	if got := b.PinnedModels(); !slices.Equal(got, want) {
		t.Errorf("an agent.model-only config pinned %q, want %q", got, want)
	}
	ps = CheckPins(b, pricing.Embedded())
	if got := paths(ps); got != "agent.models.background" {
		t.Errorf("CheckPins reported %q, want the background pin only", got)
	}
}

// placeholderPrices is Embedded() with the placeholder row a provider model
// used to get from it, built by hand: the built-in catalog prices provider
// models now, so no embedded row carries Unverified, but the flag, its source
// and its date are still what a pin warning reads.
func placeholderPrices() *pricing.Table {
	tbl := pricing.Embedded()
	tbl.Models["deepseek/deepseek-v4-flash"] = pricing.Model{
		ID: "deepseek/deepseek-v4-flash", Unverified: true,
		PriceSource: "placeholder, not verified: no provider price was read", PriceCheckedAt: "2026-10-03",
		Rates: pricing.Rates{InputPerM: 1, OutputPerM: 4, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1},
	}
	return tbl
}

// TestCatalogPriceWarns: a pin the built-in catalog priced says so — which
// tier, how many endpoints, which end of its range is charged — and names the
// two switches that move it, so an owner who wants the other end (or the
// other tier) is told how.
func TestCatalogPriceWarns(t *testing.T) {
	ws := PinWarnings(providerAgent("deepseek/deepseek-v4-flash"), providerPrices(t))
	if len(ws) != 2 || ws[0].Code != CodeCatalogPrice {
		t.Fatalf("warnings: %v", ws)
	}
	for _, want := range []string{"built-in provider price catalog", "zdr", "cheapest", "require_zdr", "price_policy: highest", "model_prices"} {
		if !strings.Contains(ws[0].Message, want) {
			t.Errorf("the catalog note does not name %q: %s", want, ws[0].Message)
		}
	}
}

// TestCatalogPriceWarnsTheUndercharge pins that the warning carries the
// numbers and not only the words: the dearest end of the tier and the multiple
// of the price charged, so an owner reading the pin sees the size of the
// undercharge the cheapest end leaves (design D6, P11). The fixture's zdr tier
// prices input 0.5..1 and output 1..2, so a cheapest row is 2x on each, stated
// to the one decimal the documents quote.
func TestCatalogPriceWarnsTheUndercharge(t *testing.T) {
	ws := PinWarnings(pinned(catModel), mustTable(t, providers(nil), nil, fixtureCatalog("2026-10-14")))
	if len(ws) == 0 {
		t.Fatal("no warning")
	}
	msg := ws[0].Message
	for _, want := range []string{"can be billed $1/M in and $2/M out", "2.0x the input price charged and 2.0x the output price charged"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the warning lacks %q: %s", want, msg)
		}
	}
	// The dearest end leaves nothing to size, so the numbers are absent
	// there: the row already charges what the dearest endpoint prices.
	high := mustTable(t, providers(func(p *ModelProvider) { p.PricePolicy = PricePolicyHighest }), nil, fixtureCatalog("2026-10-14"))
	ws = PinWarnings(pinned(catModel), high)
	if len(ws) == 0 {
		t.Fatal("no warning at the dearest bound")
	}
	if strings.Contains(ws[0].Message, "can be billed") {
		t.Errorf("a row at the dearest bound sizes an undercharge: %s", ws[0].Message)
	}
}

func TestCheckPinsPasses(t *testing.T) {
	if ps := CheckPins(pinnedAgent(), pricing.Embedded()); len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
	// agent.model covers the roles models leaves out.
	a := Agent{Model: "claude-sonnet-5-5", Models: ModelRoles{Background: "claude-haiku-4-5"}}
	if ps := CheckPins(a, pricing.Embedded()); len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
}

func TestCheckPinsAliasRefused(t *testing.T) {
	a := pinnedAgent()
	a.Models.Reviewer = "sonnet"
	a.Models.Coder = "claude-opus-5-5[1m]"
	ps := CheckPins(a, pricing.Embedded())
	if paths(ps) != "agent.models.coder,agent.models.reviewer" {
		t.Fatalf("problems: %v", ps)
	}
	if !strings.Contains(ps[1].Message, "alias") {
		t.Errorf("message %q does not say alias", ps[1].Message)
	}
}

func TestCheckPinsUnknownIDRefused(t *testing.T) {
	a := pinnedAgent()
	a.Models.Background = "claude-nonesuch-9"
	ps := CheckPins(a, pricing.Embedded())
	if paths(ps) != "agent.models.background" || !strings.Contains(ps[0].Message, "price") {
		t.Fatalf("problems: %v", ps)
	}
}

func TestCheckPinsBackgroundRequired(t *testing.T) {
	a := pinnedAgent()
	a.Models.Background = ""
	if ps := CheckPins(a, pricing.Embedded()); paths(ps) != "agent.models.background" {
		t.Fatalf("problems: %v", ps)
	}
	// With no model at all, the coder and reviewer have no pin either.
	if ps := CheckPins(Agent{}, pricing.Embedded()); paths(ps) != "agent.models.coder,agent.models.reviewer,agent.models.background" {
		t.Fatalf("problems: %v", ps)
	}
}

func TestCheckPinsOverrideTable(t *testing.T) {
	a := pinnedAgent()
	a.Models.Coder = "claude-acme-1"
	if ps := CheckPins(a, pricing.Embedded()); len(ps) != 1 {
		t.Fatalf("without the override: %v", ps)
	}
	one := 1.0
	tbl, err := pricing.Embedded().With(pricing.Overrides{"claude-acme-1": {InputPerM: one, OutputPerM: one}})
	if err != nil {
		t.Fatal(err)
	}
	if ps := CheckPins(a, tbl); len(ps) != 0 {
		t.Fatalf("with the override: %v", ps)
	}
}

func TestCheckAllowed(t *testing.T) {
	a := pinnedAgent()
	all := []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5"}
	if ps := CheckAllowed(a, policy.Merge(policy.Layer{}), nil); ps != nil {
		t.Fatalf("no list must allow everything: %v", ps)
	}
	if ps := CheckAllowed(a, policy.Merge(policy.Layer{AllowedModels: all}), all); len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
	e := policy.Merge(policy.Layer{AllowedModels: all}, policy.Layer{AllowedModels: []string{"claude-haiku-4-5"}})
	ps := CheckAllowed(a, e, all)
	if paths(ps) != "agent.models.coder,agent.models.reviewer" {
		t.Fatalf("problems: %v", ps)
	}
	if m := ps[0].Message; !strings.Contains(m, "claude-opus-5-5") || !strings.Contains(m, "default-branch") || !strings.Contains(m, "claude-haiku-4-5") {
		t.Errorf("message %q", m)
	}
	// An empty non-nil list denies every model, however it came about.
	e = policy.Merge(policy.Layer{AllowedModels: all}, policy.Layer{AllowedModels: []string{"other"}})
	if ps := CheckAllowed(a, e, all); len(ps) != 3 || !strings.Contains(ps[0].Message, "none") || !strings.Contains(ps[0].Message, "the project's allow-list: "+strings.Join(all, ", ")) {
		t.Fatalf("deny-all: %v", ps)
	}
	// A list the published policy set is the ceiling itself (SourcePolicy):
	// naming it again under the "project's allow-list" label would print the
	// same node twice, in the wrong words.
	e = policy.Merge(policy.Layer{AllowedModels: []string{"claude-haiku-4-5"}})
	e.Sources[policy.KeyAllowedModels] = policy.SourcePolicy
	ps = CheckAllowed(a, e, e.AllowedModels)
	if paths(ps) != "agent.models.coder,agent.models.reviewer" {
		t.Fatalf("policy-sourced list: %v", ps)
	}
	if m := ps[0].Message; !strings.Contains(m, "allowed_models from policy: claude-haiku-4-5") ||
		strings.Contains(m, "the project's allow-list") {
		t.Errorf("policy-sourced list message %q", m)
	}
	// A role with no model can't be shown to be on the list.
	a.Models.Reviewer = ""
	if ps := CheckAllowed(a, policy.Merge(policy.Layer{AllowedModels: all}), all); paths(ps) != "agent.models.reviewer" {
		t.Fatalf("problems: %v", ps)
	}
}

func TestCheckAllowedQuotesModelNames(t *testing.T) {
	a := pinnedAgent()
	a.Models.Coder = "[x](http://evil)`@me\n"
	ps := CheckAllowed(a, policy.Merge(policy.Layer{AllowedModels: []string{"claude-haiku-4-5"}}), nil)
	if len(ps) == 0 || !strings.Contains(ps[0].Message, "`[x](http://evil)?@me?`") {
		t.Fatalf("problems: %v", ps)
	}
	if got := CodeSpan("a`b\x00c"); got != "`a?b?c`" {
		t.Fatalf("CodeSpan = %q", got)
	}
}

func providerAgent(coder string) Agent {
	return Agent{Models: ModelRoles{Coder: coder, Reviewer: "claude-sonnet-5-5", Background: coder}}
}

func TestUnpricedModelRefused(t *testing.T) {
	// A provider model with no price anywhere is refused at the pin check,
	// never silently charged.
	ps := CheckPins(providerAgent("qwen/qwen3-coder"), pricing.Embedded())
	if paths(ps) != "agent.models.coder,agent.models.background" || !strings.Contains(ps[0].Message, "no price") {
		t.Fatalf("problems: %v", ps)
	}
	// The catalog's row for the route's model is a price, so it passes.
	if ps := CheckPins(providerAgent("deepseek/deepseek-v4-flash"), providerPrices(t)); len(ps) != 0 {
		t.Fatalf("catalog row refused: %v", ps)
	}
	// A variant suffix is a different model: no price.
	if ps := CheckPins(providerAgent("deepseek/deepseek-v4-flash:free"), pricing.Embedded()); len(ps) == 0 {
		t.Fatal("a variant suffix was priced")
	}
}

func TestUnverifiedPriceWarns(t *testing.T) {
	// The placeholder row a provider model used to get from the embedded
	// table (the built-in catalog prices provider models now): built here so
	// the flag, its source and its date stay covered.
	ws := PinWarnings(providerAgent("deepseek/deepseek-v4-flash"), placeholderPrices())
	if paths(ws) != "agent.models.coder,agent.models.background" || ws[0].Code != CodeUnverifiedPrice ||
		!strings.Contains(ws[0].Message, "unverified") || !strings.Contains(ws[0].Message, "model_prices") {
		t.Fatalf("warnings: %v", ws)
	}
	if ws := PinWarnings(pinnedAgent(), pricing.Embedded()); len(ws) != 0 {
		t.Fatalf("Claude pins warned: %v", ws)
	}
	// The owner's price wins and silences the warning.
	tbl, err := pricing.Embedded().With(pricing.Overrides{"deepseek/deepseek-v4-flash": {InputPerM: 0.1, OutputPerM: 0.2}})
	if err != nil {
		t.Fatal(err)
	}
	// ...but leaving the cache multipliers at 0 is its own warning.
	ws = PinWarnings(providerAgent("deepseek/deepseek-v4-flash"), tbl)
	if paths(ws) != "agent.models.coder,agent.models.background" || ws[0].Code != pricing.CodeCacheRateDefaulted {
		t.Fatalf("an override without cache rates: %v", ws)
	}
	full, err := pricing.Embedded().With(pricing.Overrides{"deepseek/deepseek-v4-flash": {InputPerM: 0.1, OutputPerM: 0.2, CacheWrite5m: 1, CacheWrite1h: 1, CacheRead: 0.1}})
	if err != nil {
		t.Fatal(err)
	}
	if ws := PinWarnings(providerAgent("deepseek/deepseek-v4-flash"), full); len(ws) != 0 {
		t.Fatalf("an owner's full price still warns: %v", ws)
	}
}
