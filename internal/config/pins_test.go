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

// TestPinnedModelsAreTheFourPins pins the one list of a run's pins: the
// models PinnedModels returns are the four the paths above name, in the same
// order, with an empty name where the config pins nothing rather than the
// entry dropped. The runner asks for those four to decide what a stale
// catalog means for a run (internal/runner/gateway.go), so a set that drifted
// from the one CheckPins and PinWarnings judge would judge some other run.
func TestPinnedModelsAreTheFourPins(t *testing.T) {
	a := pinnedAgent()
	want := []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5", ""}
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
	// to it, and the background model and the vision slot to nothing: the
	// empty entries stay.
	b := Agent{Model: "claude-sonnet-5-5"}
	want = []string{"claude-sonnet-5-5", "claude-sonnet-5-5", "", ""}
	if got := b.PinnedModels(); !slices.Equal(got, want) {
		t.Errorf("an agent.model-only config pinned %q, want %q", got, want)
	}
	ps = CheckPins(b, pricing.Embedded())
	if got := paths(ps); got != "agent.models.background" {
		t.Errorf("CheckPins reported %q, want the background pin only", got)
	}
}

// TestPinPathsNameTheVisionSlot (V1, D2007): the fourth pin path is
// agent.models.vision, and it is the one pin a run may do without. pins() drops
// it while it is empty rather than handing it to the checks, which is what
// makes the slot's absence unjudged everywhere the three roles' absences are
// judged ("is required with a budget") — and it hands it over the moment the
// repository writes a model, so the price rule and the allow-list reach it as
// they reach a role.
func TestPinPathsNameTheVisionSlot(t *testing.T) {
	if len(pinPaths) != 4 || pinPaths[3] != "agent.models.vision" {
		t.Fatalf("pinPaths = %q, want the vision slot as the fourth", pinPaths)
	}
	if got := pins(pinnedAgent()); len(got) != 3 {
		t.Errorf("pins() of a run with no vision model = %v, want the three roles", got)
	}
	a := pinnedAgent()
	a.Models.Vision = "deepseek/deepseek-v4.1-flash"
	got := pins(a)
	if len(got) != 4 || got[3].path != pinPaths[3] || got[3].model != a.Models.Vision {
		t.Fatalf("pins() = %v, want the vision model at %s", got, pinPaths[3])
	}
	if ps := CheckPins(a, pricing.Embedded()); paths(ps) != "agent.models.vision" {
		t.Errorf("an unpriced vision model: CheckPins reported %q, want agent.models.vision", paths(ps))
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

// craftFixture is the access half's documents, as an installation that writes
// them holds them: one Claude account, one provider entry, and a pool over the
// two. Neither entry admits acme/app, which is the repository every case below
// judges; the cases that need an admitting half say so by name.
func craftFixture() CraftAccess {
	return CraftAccess{
		Repo:        "acme/app",
		Credentials: []Credential{{Name: "bmb", Type: CredentialTypeClaudeOAuth}, {Name: "acme", Type: CredentialTypeOpenRouter}},
		Pools:       []Pool{{Name: "shared", Members: []string{"bmb", "acme"}}},
		Accounts:    map[string]ClaudeAccount{"bmb": {AllowDataTo: []string{"other/repo"}}},
		Providers:   map[string]ModelProvider{"acme": {Kind: ModelProviderKind, BaseURL: "https://acme.example/api", Auth: "bearer", Secret: "acme-api-key", Models: []string{"acme/*"}, AllowDataTo: []string{"other/repo"}}},
	}
}

// TestCraftModelOutsideTheAllowListRefused (D1665, MC15): a craft's model is
// bounded by budget.allowed_models exactly as a role pin is, at the key of the
// document that wrote it, and the same `allowed` predicate answers both — so a
// craft that names a route ID is admitted where the switch routes it and
// nowhere else, as a pin is.
//
// Red step: this passes on the tree before MC15 with no problem reported, a
// craft model nothing on the list covers reaching the gateway.
func TestCraftModelOutsideTheAllowListRefused(t *testing.T) {
	a := pinnedAgent()
	list := []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5"}
	e := policy.Merge(policy.Layer{AllowedModels: list})
	off := []CraftModel{{Craft: CraftCoder, Model: "claude-fable-5-1"}}
	ps := CheckAllowed(a, e, list, nil, nil, off...)
	if paths(ps) != "agent.crafts.coder.model" {
		t.Fatalf("problems: %v", ps)
	}
	if m := ps[0].Message; !strings.Contains(m, "claude-fable-5-1") || !strings.Contains(m, "not an allowed model") || !strings.Contains(m, "claude-haiku-4-5") {
		t.Errorf("message %q", m)
	}
	// A craft on the list passes, and the three pins are untouched by the
	// parameter: one problem, for the craft.
	if ps := CheckAllowed(a, e, list, nil, nil, []CraftModel{{Craft: CraftReview, Model: "claude-haiku-4-5"}}...); len(ps) != 0 {
		t.Fatalf("a craft on the list: %v", ps)
	}
	// The path is the key in the document that wrote it (craft.Layer.CraftKey),
	// which is the project layer's for a craft it set.
	layer := []CraftModel{{Craft: CraftSeniorReview, Model: "claude-fable-5-1", Path: "defaults.agent.crafts.senior-review.model"}}
	if got := paths(CheckAllowed(a, e, list, nil, nil, layer...)); got != "defaults.agent.crafts.senior-review.model" {
		t.Fatalf("problems reported at %q, want the project layer's key", got)
	}
	// The allow-list's own admission is the one the pins use: a craft whose
	// model the switch routes is admitted by the canonical ID on the list,
	// where a pin naming the route ID is, and refused where the switch routes
	// nothing — at the craft's key, beside the same craft's own list refusal.
	routed := []CraftModel{{Craft: CraftCoder, Model: "anthropic/claude-sonnet-5.5"}}
	if ps := CheckAllowed(a, e, list, routedSwitch(), routedRoutes(), routed...); len(ps) != 0 {
		t.Fatalf("a routed craft model under the list that names it: %v", ps)
	}
	ps = CheckAllowed(a, e, list, routedSwitch(), nil, routed...)
	if !strings.Contains(paths(ps), "agent.crafts.coder.model") {
		t.Fatalf("a routed craft model with no route claiming it: %v", ps)
	}
}

// TestCraftModelWithNoRouteOrNoPriceRefused (D1665, MC15): the two checks a
// craft's model passes that the role pins pass — a route carries it, and a
// price row covers it. The route half is judged whether or not the run has an
// allow-list, because the run's own bootstrap halts on a craft model no route
// resolves (internal/runner/routing.go's applyRouting) and the point of the
// panel is to say so before the task is written.
func TestCraftModelWithNoRouteOrNoPriceRefused(t *testing.T) {
	a := pinnedAgent()
	noList := policy.Merge(policy.Layer{})
	// A provider model no provider entry claims: the pin rule's own refusal.
	ps := CheckAllowed(a, noList, nil, nil, nil, CraftModel{Craft: CraftCoder, Model: "qwen/qwen3-coder"})
	if paths(ps) != "agent.crafts.coder.model" || !strings.Contains(ps[0].Message, "no provider serves it") {
		t.Fatalf("a craft model no provider carries: %v", ps)
	}
	// A Claude ID the switch names and this configuration cannot resolve: the
	// switch routes nothing rather than sending the call direct (D1b).
	ps = CheckAllowed(a, noList, nil, routedSwitch(), nil, CraftModel{Craft: CraftReview, Model: "claude-sonnet-5-5"})
	if paths(ps) != "agent.crafts.review.model" || !strings.Contains(ps[0].Message, "cannot resolve its route") {
		t.Fatalf("a craft model the switch names with no route: %v", ps)
	}
	// The same model under the switch that routes it, and a Claude ID the
	// switch does not name (which goes direct): neither is a problem.
	if ps := CheckAllowed(a, noList, nil, routedSwitch(), routedRoutes(), CraftModel{Craft: CraftReview, Model: "claude-sonnet-5-5"}); len(ps) != 0 {
		t.Fatalf("a routed craft model: %v", ps)
	}
	if ps := CheckAllowed(a, noList, nil, routedSwitch(), routedRoutes(), CraftModel{Craft: CraftReview, Model: "claude-opus-5-5"}); len(ps) != 0 {
		t.Fatalf("a craft model the switch does not name: %v", ps)
	}
	// The price half: the table CheckPins already takes covers a craft's model
	// too, and the alias rule is the pins' own.
	ps = CheckPins(a, pricing.Embedded(), CraftModel{Craft: CraftReview, Model: "qwen/qwen3-coder"})
	if paths(ps) != "agent.crafts.review.model" || !strings.Contains(ps[0].Message, "no price") {
		t.Fatalf("an unpriced craft model: %v", ps)
	}
	if ps := CheckPins(a, providerPrices(t), CraftModel{Craft: CraftReview, Model: "deepseek/deepseek-v4-flash"}); len(ps) != 0 {
		t.Fatalf("a priced craft model: %v", ps)
	}
	ps = CheckPins(a, pricing.Embedded(), CraftModel{Craft: CraftCoder, Model: "sonnet"})
	if paths(ps) != "agent.crafts.coder.model" || !strings.Contains(ps[0].Message, "alias") {
		t.Fatalf("an alias craft model: %v", ps)
	}
}

// TestCraftModelOnAnAccessThatDoesNotAdmitTheRepoRefused (F10, D1627, MC15):
// a craft's model cannot be served by an access that admits this repository to
// nothing. A pool is admitted when at least one of its members is — the
// members that do not admit it are skipped and recorded, and only a pool with
// no admitting member leaves the stage with nothing — and a credential by its
// own list.
func TestCraftModelOnAnAccessThatDoesNotAdmitTheRepoRefused(t *testing.T) {
	docs := craftFixture()
	craft := func(access string) []CraftModel {
		return []CraftModel{{Craft: CraftCoder, Model: "claude-sonnet-5-5", Access: access}}
	}
	ps := CheckCraftAccess(craft("shared"), docs)
	if paths(ps) != "agent.crafts.coder.model" {
		t.Fatalf("a pool no member of which admits the repository: %v", ps)
	}
	for _, want := range []string{"shared", "acme/app", "allow_data_to", "allow_repos", "D1627"} {
		if !strings.Contains(ps[0].Message, want) {
			t.Errorf("the pool refusal does not name %q: %s", want, ps[0].Message)
		}
	}
	// A credential of its own that does not admit it: the same verdict, in the
	// sentence that names the fix that applies to one credential.
	ps = CheckCraftAccess(craft("bmb"), docs)
	if paths(ps) != "agent.crafts.coder.model" || !strings.Contains(ps[0].Message, "the credential") {
		t.Fatalf("a credential that does not admit the repository: %v", ps)
	}
	// One admitting member is enough, whichever kind it is (D1627's skip), and
	// the built-in vertex admits every repository because there is no provider
	// to be admitted to (D1624).
	admitting := craftFixture()
	admitting.Accounts["bmb"] = ClaudeAccount{AllowDataTo: []string{"acme/app"}}
	if ps := CheckCraftAccess(craft("shared"), admitting); len(ps) != 0 {
		t.Fatalf("a pool with one admitting member: %v", ps)
	}
	if ps := CheckCraftAccess(craft(CredentialVertex), docs); len(ps) != 0 {
		t.Fatalf("the built-in vertex: %v", ps)
	}
	// A provider entry that admits the repository, and one the document does
	// not carry at all: the second is refused, which is the run's own
	// fail-closed answer for a member whose provider it does not have.
	admitting.Providers["acme"] = ModelProvider{Kind: ModelProviderKind, BaseURL: "https://acme.example/api", Auth: "bearer", Secret: "acme-api-key", Models: []string{"acme/*"}, AllowDataTo: []string{"acme/app"}}
	if ps := CheckCraftAccess(craft("acme"), admitting); len(ps) != 0 {
		t.Fatalf("an admitting provider: %v", ps)
	}
	ghost := craftFixture()
	ghost.Credentials = append(ghost.Credentials, Credential{Name: "ghost", Type: CredentialTypeOpenRouter})
	if ps := CheckCraftAccess(craft("ghost"), ghost); len(ps) != 1 {
		t.Fatalf("an openrouter credential with no provider entry: %v", ps)
	}
	// A name no document holds is the built-in vertex (D1624), which admits
	// every repository: that it resolves to nothing at all is the resolver's
	// own problem, reported at the access key and not here.
	if ps := CheckCraftAccess(craft("unheld"), docs); len(ps) != 0 {
		t.Fatalf("a name no document holds: %v", ps)
	}
	// The key in the document that wrote the craft is where the refusal lands.
	layer := []CraftModel{{Craft: CraftReview, Model: "claude-haiku-4-5", Path: "crafts.review.model", Access: "shared"}}
	if got := paths(CheckCraftAccess(layer, docs)); got != "crafts.review.model" {
		t.Fatalf("the refusal was reported at %q, want the installation config's key", got)
	}
}

// TestUnsetCraftModelsChangeNoVerdict (F17, MC15): a craft that names no model
// anywhere adds no problem and changes no verdict, on every one of the four
// checks — so an installation that writes no craft model is judged exactly as
// it was before this parameter existed. The last case is the control: the same
// access with a craft that does name a model is refused, so the emptiness
// above is the F17 rule and not a check that never runs.
func TestUnsetCraftModelsChangeNoVerdict(t *testing.T) {
	a := pinnedAgent()
	list := []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5"}
	e := policy.Merge(policy.Layer{AllowedModels: list})
	unset := []CraftModel{
		{Craft: CraftCoder},
		{Craft: CraftReview, Path: "defaults.agent.crafts.review.model"},
		{Craft: CraftSeniorReview},
		{Craft: CraftSecurityReview, Path: "crafts.security-review.model"},
	}
	if got, want := paths(CheckAllowed(a, e, list, nil, nil, unset...)), paths(CheckAllowed(a, e, list, nil, nil)); got != want {
		t.Errorf("an unset craft changed the allow-list verdict: %q, want %q", got, want)
	}
	if ps := CheckPins(a, pricing.Embedded(), unset...); len(ps) != 0 {
		t.Errorf("an unset craft is judged by the price rule: %v", ps)
	}
	// An unset craft is not "required with a budget" either: the role slot
	// below it is what serves its stages, and that slot the pins loop judges.
	if got, want := paths(CheckPins(a, pricing.Embedded(), unset...)), paths(CheckPins(a, pricing.Embedded())); got != want {
		t.Errorf("an unset craft changed the price verdict: %q, want %q", got, want)
	}
	docs := craftFixture()
	if ps := CheckCraftAccess(unset, docs); len(ps) != 0 {
		t.Errorf("an unset craft is refused for its access: %v", ps)
	}
	// The control: one of those crafts with a model on the same access is
	// refused, so the three verdicts above are F17 and not a dead check.
	named := []CraftModel{{Craft: CraftCoder, Model: "claude-sonnet-5-5", Access: "shared"}}
	if ps := CheckCraftAccess(named, docs); len(ps) != 1 {
		t.Errorf("the control: %v", ps)
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

// TestPinWarningsCoverALevelModel: a review level's own model is a model the
// run charges (L3.1, agent.levels.lN.model), so the pins' warnings are its
// too — the placeholder price an unverified row carries and the catalog row a
// stale catalog forced to the dearest end — each under the key the owner wrote
// and each naming the level, which is all a surface that shows a warning
// without its path (diagnose, the runner's log line) has to go on.
func TestPinWarningsCoverALevelModel(t *testing.T) {
	a := pinnedAgent()
	a.Levels = ReviewLevels{L3: &LevelSetting{Model: "deepseek/deepseek-v4-flash"}}
	ws := PinWarnings(a, placeholderPrices())
	if got := paths(ws); got != "agent.levels.l3.model" {
		t.Fatalf("an unverified level model warned under %q: %v", got, ws)
	}
	if ws[0].Code != CodeUnverifiedPrice || !strings.Contains(ws[0].Message, "level l3") || !strings.Contains(ws[0].Message, "unverified") {
		t.Errorf("the level model's warning: %+v", ws[0])
	}
	// A level with no model of its own, and a level whose model is one of the
	// run's pins, are that pin: the run resolves every level that runs onto a
	// model (the runner's setLevelModel), so a warning of their own would say
	// the background pin's sentence again for the level.
	b := pinnedAgent()
	b.Levels = ReviewLevels{L1: &LevelSetting{Model: "claude-haiku-4-5"}, L3: &LevelSetting{Effort: "high"}}
	if ws := PinWarnings(b, pricing.Embedded()); len(ws) != 0 {
		t.Errorf("a level on a pin, and a level with no model of its own: %v", ws)
	}
	// The stale-catalog warning reaches a level model too.
	b = pinnedAgent()
	b.Levels = ReviewLevels{L2: &LevelSetting{Model: catModel}}
	ws = PinWarnings(b, mustTable(t, providers(nil), nil, fixtureCatalog("2026-03-01")))
	if got := paths(ws); got != "agent.levels.l2.model" {
		t.Fatalf("a stale level model warned under %q: %v", got, ws)
	}
	if ws[0].Code != CodeCatalogPrice || !strings.Contains(ws[0].Message, "stale") || !strings.Contains(ws[0].Message, "level l2") {
		t.Errorf("the stale level model's warning: %+v", ws[0])
	}
	// ...which is the same answer for a provider model the pins already carry:
	// the level's copy is not a second pin.
	c := providerAgent("deepseek/deepseek-v4-flash")
	c.Levels = ReviewLevels{L3: &LevelSetting{Model: "deepseek/deepseek-v4-flash"}}
	if got := paths(PinWarnings(c, placeholderPrices())); got != "agent.models.coder,agent.models.background" {
		t.Errorf("a level model that is a pin warned under %q", got)
	}
	// Two levels that name one model of their own get one warning each, under
	// their own key: a warning is about a place in the file, as the coder's and
	// the background's two warnings for one model have always been.
	d := pinnedAgent()
	d.Levels = ReviewLevels{L1: &LevelSetting{Model: catModel}, L3: &LevelSetting{Model: catModel}}
	ws = PinWarnings(d, mustTable(t, providers(nil), nil, fixtureCatalog("2026-10-14")))
	if got := paths(ws); got != "agent.levels.l1.model,agent.levels.l3.model" {
		t.Fatalf("two levels on one model warned under %q: %v", got, ws)
	}
	if !strings.Contains(ws[0].Message, "level l1") || !strings.Contains(ws[1].Message, "level l3") {
		t.Errorf("the two warnings do not name their level: %v", ws)
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
