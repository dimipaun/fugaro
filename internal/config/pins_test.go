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
	if ps := CheckAllowed(a, policy.Merge(policy.Layer{}), nil, nil, nil); ps != nil {
		t.Fatalf("no list must allow everything: %v", ps)
	}
	if ps := CheckAllowed(a, policy.Merge(policy.Layer{AllowedModels: all}), all, nil, nil); len(ps) != 0 {
		t.Fatalf("problems: %v", ps)
	}
	e := policy.Merge(policy.Layer{AllowedModels: all}, policy.Layer{AllowedModels: []string{"claude-haiku-4-5"}})
	ps := CheckAllowed(a, e, all, nil, nil)
	if paths(ps) != "agent.models.coder,agent.models.reviewer" {
		t.Fatalf("problems: %v", ps)
	}
	if m := ps[0].Message; !strings.Contains(m, "claude-opus-5-5") || !strings.Contains(m, "default-branch") || !strings.Contains(m, "claude-haiku-4-5") {
		t.Errorf("message %q", m)
	}
	// An empty non-nil list denies every model, however it came about.
	e = policy.Merge(policy.Layer{AllowedModels: all}, policy.Layer{AllowedModels: []string{"other"}})
	if ps := CheckAllowed(a, e, all, nil, nil); len(ps) != 3 || !strings.Contains(ps[0].Message, "none") || !strings.Contains(ps[0].Message, "the project's allow-list: "+strings.Join(all, ", ")) {
		t.Fatalf("deny-all: %v", ps)
	}
	// A list the published policy set is the ceiling itself (SourcePolicy):
	// naming it again under the "project's allow-list" label would print the
	// same node twice, in the wrong words.
	e = policy.Merge(policy.Layer{AllowedModels: []string{"claude-haiku-4-5"}})
	e.Sources[policy.KeyAllowedModels] = policy.SourcePolicy
	ps = CheckAllowed(a, e, e.AllowedModels, nil, nil)
	if paths(ps) != "agent.models.coder,agent.models.reviewer" {
		t.Fatalf("policy-sourced list: %v", ps)
	}
	if m := ps[0].Message; !strings.Contains(m, "allowed_models from policy: claude-haiku-4-5") ||
		strings.Contains(m, "the project's allow-list") {
		t.Errorf("policy-sourced list message %q", m)
	}
	// A role with no model can't be shown to be on the list.
	a.Models.Reviewer = ""
	if ps := CheckAllowed(a, policy.Merge(policy.Layer{AllowedModels: all}), all, nil, nil); paths(ps) != "agent.models.reviewer" {
		t.Fatalf("problems: %v", ps)
	}
}

// routedSwitch is the switch the routed fixtures route through: via openrouter,
// naming the canonical ID, with one route entry claiming the map's pair and
// allow_data_to written (D1b's safeguard, so the resolution is a real one).
func routedSwitch() *Routing {
	return &Routing{Claude: &RoutingClaude{Via: RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}}}
}

// routedRoutes are the routes routedSwitch resolves through.
func routedRoutes() map[string]ModelProvider {
	return map[string]ModelProvider{"openrouter": {
		Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5.5"}, AllowDataTo: []string{"*"},
	}}
}

// TestCheckAllowedAdmitsARoutedPin (plan Task 1b.2): a pin the routing switch
// routed names the route's own ID, and the allow-list the owner wrote names the
// model — one model, one entry. The list admits the routed call, and it admits
// nothing else: a model it does not name is still refused whether or not it
// carries a route's spelling.
//
// Hand mutation this fails on: comparing the pin literally, which refuses every
// routed call under the allow-list an enforcing installation always has — the
// switch would be unusable exactly where the policy is strictest.
func TestCheckAllowedAdmitsARoutedPin(t *testing.T) {
	const routed = "anthropic/claude-sonnet-5.5"
	a := Agent{Models: ModelRoles{Coder: routed, Reviewer: "anthropic/claude-opus-5.5", Background: routed}}
	sw, routes := routedSwitch(), routedRoutes()
	// The list names the coder's model, not the reviewer's: the routed coder is
	// admitted and the routed reviewer is refused. (The reviewer's route ID is
	// opus's, which this switch does not name — the refusal is about the model,
	// not about the spelling.)
	e := policy.Merge(policy.Layer{AllowedModels: []string{"claude-sonnet-5-5"}})
	if ps := CheckAllowed(a, e, nil, sw, routes); paths(ps) != "agent.models.reviewer" {
		t.Fatalf("problems: %v", ps)
	}
	// A list that names neither model, and a route whose model the list does
	// not name: the map admits the canonical ID of a route's row and nothing
	// else, so neither the spelling nor the route is a way onto the list.
	e = policy.Merge(policy.Layer{AllowedModels: []string{"claude-haiku-4-5"}})
	if ps := CheckAllowed(a, e, nil, sw, routes); paths(ps) != "agent.models.coder,agent.models.reviewer,agent.models.background" {
		t.Fatalf("problems: %v", ps)
	}
	a.Models.Coder = "deepseek/deepseek-v4-flash"
	if ps := CheckAllowed(a, policy.Merge(policy.Layer{AllowedModels: []string{"claude-sonnet-5-5"}}), nil, sw, routes); paths(ps) != "agent.models.coder,agent.models.reviewer" {
		t.Fatalf("a provider model the map does not carry: %v", ps)
	}
}

// TestCheckAllowedAdmitsARouteIDOnlyWhereTheSwitchRoutesIt is the security half
// of the pair above, and the review finding that rewrote it: a repository's own
// pin may name a route's ID — that is a well-formed model ID, and the config
// loader accepts it — and the allow-list must not take that spelling as a way
// past the owner's two decisions. A route ID is admitted only when the switch
// routes the model to *that* route, which is the resolution the run itself
// performs; without a switch, with a switch that names another model, and with
// no route claiming it, the allow-list refuses it.
//
// Hand mutation this fails on: admitting any anthropic/<id> by its canonical ID
// alone (the behaviour this finding removed), which let a pin the owner's switch
// never opted in reach the route the allow-list was supposed to bound.
func TestCheckAllowedAdmitsARouteIDOnlyWhereTheSwitchRoutesIt(t *testing.T) {
	const routed = "anthropic/claude-sonnet-5.5"
	a := Agent{Models: ModelRoles{Coder: routed, Reviewer: "claude-opus-5-5", Background: routed}}
	e := policy.Merge(policy.Layer{AllowedModels: []string{"claude-sonnet-5-5", "claude-opus-5-5"}})
	// No switch at all: D1b's absent means direct, so nothing routes this pin
	// and the allow-list judges the spelling it was given.
	if ps := CheckAllowed(a, e, nil, nil, routedRoutes()); paths(ps) != "agent.models.coder,agent.models.background" {
		t.Fatalf("no switch: problems = %v, want the route ID's two roles refused", ps)
	}
	// A switch that names another model: the same refusal, with the routes
	// present and resolvable.
	other := &Routing{Claude: &RoutingClaude{Via: RouteOpenRouter, Models: []string{"claude-fable-5-1"}}}
	if ps := CheckAllowed(a, e, nil, other, routedRoutes()); paths(ps) != "agent.models.coder,agent.models.background" {
		t.Fatalf("a switch naming another model: problems = %v", ps)
	}
	// A switch that names this model with no route claiming it: RoutingClaudeFor
	// resolves nothing, so the ID is still not admitted — never admitted by the
	// fold, the spelling or the map alone.
	if ps := CheckAllowed(a, e, nil, routedSwitch(), nil); paths(ps) != "agent.models.coder,agent.models.background" {
		t.Fatalf("a switch with no route: problems = %v", ps)
	}
	// The switch that does route it: admitted, and only where it routes it.
	if ps := CheckAllowed(a, e, nil, routedSwitch(), routedRoutes()); len(ps) != 0 {
		t.Fatalf("the switch routes the model to this ID and the pin was refused: %v", ps)
	}
	// A near-miss spelling claimed by a route is not the map's pair, so the
	// switch resolves nothing and the allow-list admits nothing: one character
	// away from the admitted case, and refused.
	near := "anthropic/claude-sonnet-5-5"
	a.Models.Coder, a.Models.Background = near, near
	routes := routedRoutes()
	routes["openrouter"] = ModelProvider{Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{near}, AllowDataTo: []string{"*"}}
	if ps := CheckAllowed(a, e, nil, routedSwitch(), routes); paths(ps) != "agent.models.coder,agent.models.background" {
		t.Fatalf("a claim that is not the map's pair: problems = %v", ps)
	}
}

func TestCheckAllowedQuotesModelNames(t *testing.T) {
	a := pinnedAgent()
	a.Models.Coder = "[x](http://evil)`@me\n"
	ps := CheckAllowed(a, policy.Merge(policy.Layer{AllowedModels: []string{"claude-haiku-4-5"}}), nil, nil, nil)
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
