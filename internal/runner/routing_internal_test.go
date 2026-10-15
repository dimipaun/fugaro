package runner

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// routedRun builds the minimal run the routing switch needs: a coder pin, the
// gateway on (api-key auth, budget enforcing), one provider route claiming the
// route's own Claude ID with allow_data_to written, and the switch delivered
// through the policy node (D1a's only delivery path).
func routedRun(t *testing.T, sw *config.RoutingClaude, coder, reviewer string) *run {
	t.Helper()
	r := &run{
		cfg: &config.Config{Agent: config.Agent{
			Auth:   "api-key",
			Models: config.ModelRoles{Coder: coder, Reviewer: reviewer},
		}},
		spend: Spend{Mode: "enforce"},
		d: Deps{Providers: map[string]config.ModelProvider{
			"openrouter": {
				Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
				Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5.5"},
				AllowDataTo: []string{"acme/site"}, RouteFeePct: 5,
			},
		}},
	}
	r.projPolicy = policyRead{done: true, found: sw != nil, policy: &projectpolicy.Policy{Routing: &config.Routing{Claude: sw}}}
	return r
}

// TestStagePinsUseTheRouteIDOnlyWhenOptedIn (plan Task 1b.2): a Claude pin the
// switch names is rendered as the route's own ID, and with it the whole run —
// the gateway registers the route and prices the call on it — moves to the
// route. An ID the switch does not name is untouched.
//
// Hand mutations this fails on: mapping the canonical ID to the wrong route ID
// (the pins would name a model no route claims); leaving the pins canonical
// (the call goes direct and is charged the direct Claude rate); resolving every
// Claude ID rather than the ones the list names (the un-opted reviewer moves).
func TestStagePinsUseTheRouteIDOnlyWhenOptedIn(t *testing.T) {
	const canonical, routed = "claude-sonnet-5-5", "anthropic/claude-sonnet-5.5"
	r := routedRun(t, &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{canonical}}, canonical, "claude-opus-5")
	if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
		t.Fatalf("applyRouting: %v", err)
	}
	pins := r.stagePins("implement")
	if pins["CLAUDE_CODE_SUBAGENT_MODEL"] != routed || pins["ANTHROPIC_DEFAULT_SONNET_MODEL"] != routed {
		t.Errorf("opted-in coder pins = %+v, want the route's own ID %q", pins, routed)
	}
	// A routed model is a provider model: the stage's pins carry the provider
	// route's own variables, which the gateway refuses the fields these
	// disable on (internal/runner/agentenv.go, forward.go's routedKeys).
	if pins["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"] != "1" {
		t.Errorf("opted-in coder pins = %+v, want the provider-route variables", pins)
	}
	// The reviewer is Claude but a model the list does not name: it stays
	// canonical, exactly as today.
	rev := r.stagePins("review")
	if rev["CLAUDE_CODE_SUBAGENT_MODEL"] != "claude-opus-5" {
		t.Errorf("reviewer pins = %+v, want the canonical pin untouched: the explicit list is the opt-in (D1a)", rev)
	}
	if _, ok := rev["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"]; ok {
		t.Error("the un-opted reviewer stage carries the provider-route variables")
	}
	// The route the run's own pins name is registered with the gateway: a run
	// whose pins moved but whose routes did not would send a routed ID to the
	// Anthropic upstream.
	routes, err := r.gatewayRoutes()
	if err != nil {
		t.Fatalf("gatewayRoutes: %v", err)
	}
	if len(routes) != 1 || routes[0].Name != "openrouter" || routes[0].FeePct != 5 {
		t.Errorf("gatewayRoutes() = %+v, want the openrouter route with its fee", routes)
	}
}

// TestClaudePinWithoutOptInIsUnchanged: an absent switch, and a switch whose
// list names other models, leave every pin exactly as it is (D1b). The
// mutation: applying the switch to every Claude ID once it is written.
func TestClaudePinWithoutOptInIsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		sw   *config.RoutingClaude
	}{
		{"no switch at all", nil},
		{"via: direct", &config.RoutingClaude{Via: config.RouteDirect, Models: []string{"claude-sonnet-5-5"}}},
		{"another model opted in", &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{"claude-opus-5-5"}}},
	} {
		r := routedRun(t, tc.sw, "claude-sonnet-5-5", "claude-opus-5-5")
		found := tc.sw != nil
		if err := r.applyRouting(r.projPolicy.policy, found); err != nil {
			t.Fatalf("%s: applyRouting: %v", tc.name, err)
		}
		pins := r.stagePins("implement")
		if pins["CLAUDE_CODE_SUBAGENT_MODEL"] != "claude-sonnet-5-5" {
			t.Errorf("%s: coder pins = %+v, want the canonical ID", tc.name, pins)
		}
		if routes, err := r.gatewayRoutes(); err != nil || len(routes) != 0 {
			t.Errorf("%s: gatewayRoutes() = %+v (%v), want no route: nothing is routed", tc.name, routes, err)
		}
	}
}

// TestRoutedClaudeIsPricedFromTheRouteRowNotTheDirectRate: what a routed Claude
// call is charged is the route's row — here model_prices, the catalog's Claude
// rows not being shipped yet — and never the direct Anthropic row the canonical
// ID would have been priced from. The pin the run carries is the route's ID, so
// the price lookup and the charge are about that ID; with no price for it the
// run is refused loudly rather than falling back to the cheaper, wrong row.
//
// Hand mutation this fails on: checking the pins as written (canonical), which
// passes on the embedded Claude row and charges the direct rate for a call the
// provider served.
func TestRoutedClaudeIsPricedFromTheRouteRowNotTheDirectRate(t *testing.T) {
	const canonical, routed = "claude-sonnet-5-5", "anthropic/claude-sonnet-5.5"
	r := routedRun(t, &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{canonical}}, canonical, canonical)
	r.cfg.Agent.Models.Background = canonical
	table, err := config.PriceTable(r.d.Providers, nil, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Guard the premise: the canonical ID is priced (the embedded Claude row),
	// so a run that failed to route would pass the pin check.
	if _, ok := table.Lookup(canonical); !ok {
		t.Fatal("the embedded table has no row for the canonical ID: this test's premise is gone")
	}
	if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
		t.Fatalf("applyRouting: %v", err)
	}
	if ps := config.CheckPins(r.cfg.Agent, table); len(ps) == 0 {
		t.Fatal("a routed Claude pin with no price for the route's row was accepted: the direct Anthropic row must not price it")
	} else if got := ps[0].String(); !strings.Contains(got, routed) {
		t.Errorf("the refusal does not name the route's ID: %s", got)
	}
	// The owner prices the route's row under model_prices: the same pin is
	// then priced, and the row it is priced from is the route's.
	priced, err := config.PriceTable(r.d.Providers, overridesFor(routed), nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ps := config.CheckPins(r.cfg.Agent, priced); len(ps) > 0 {
		t.Fatalf("the route's row priced the pin, and the run still refuses: %s", ps[0].String())
	}
	direct, _ := priced.Lookup(canonical)
	route, ok := priced.Lookup(routed)
	if !ok || route.Rates.InputPerM == direct.Rates.InputPerM {
		t.Errorf("the routed row's rates are the direct row's: the charge would be the direct-path Claude rate")
	}
}

// TestRoutingRefusesAPinItCannotResolve: a switch that names a pin whose route
// is gone — no provider entry claims it, or the canonical ID has no map row —
// halts the run naming the model, instead of quietly sending the call direct.
//
// Hand mutation this fails on: resolve-or-ignore, which is the fall-through D1b
// exists to prevent.
func TestRoutingRefusesAPinItCannotResolve(t *testing.T) {
	// No route claims the ID the switch names: the providers map is empty of it.
	r := routedRun(t, &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}}, "claude-sonnet-5-5", "claude-opus-5")
	r.d.Providers = map[string]config.ModelProvider{}
	if err := r.applyRouting(r.projPolicy.policy, true); err == nil {
		t.Error("a switch naming a model no route claims was applied without a refusal")
	} else if !strings.Contains(err.Error(), "claude-sonnet-5-5") {
		t.Errorf("the refusal does not name the model: %v", err)
	}
	// An ID the canonical map does not map, claimed by a route: the map is the
	// authority, so the run refuses with the map's own reason.
	r = routedRun(t, &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{"claude-sonnet-5"}}, "claude-sonnet-5", "claude-opus-5")
	r.d.Providers = map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"anthropic/claude-sonnet-5"}, AllowDataTo: []string{"*"},
	}}
	err := r.applyRouting(r.projPolicy.policy, true)
	if err == nil {
		t.Fatal("a Claude ID with no canonical-map row was routed")
	}
	if !strings.Contains(err.Error(), "no route's own ID has been measured") {
		t.Errorf("the refusal does not carry the map's reason: %v", err)
	}
}

// TestRoutingAutoFollowsTheAnthropicKey: via: auto is key-presence driven, as
// the design spells it — the route with no key version in the job, the direct
// path (the pins untouched) with one.
//
// Hand mutation this fails on: treating auto as openrouter unconditionally,
// which routes a project that has a key away from Anthropic.
func TestRoutingAutoFollowsTheAnthropicKey(t *testing.T) {
	const canonical = "claude-sonnet-5-5"
	sw := &config.RoutingClaude{Via: config.RouteAuto, Models: []string{canonical}}
	r := routedRun(t, sw, canonical, canonical)
	if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
		t.Fatalf("auto without a key: %v", err)
	}
	if pins := r.stagePins("implement"); pins["CLAUDE_CODE_SUBAGENT_MODEL"] != "anthropic/claude-sonnet-5.5" {
		t.Errorf("auto without a key: pins = %+v, want the route's ID", pins)
	}
	r = routedRun(t, sw, canonical, canonical)
	r.d.Env = []string{"ANTHROPIC_API_KEY=sk-ant-test"}
	if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
		t.Fatalf("auto with a key: %v", err)
	}
	if pins := r.stagePins("implement"); pins["CLAUDE_CODE_SUBAGENT_MODEL"] != canonical {
		t.Errorf("auto with a key: pins = %+v, want the direct path (D1b: auto prefers direct when a key exists)", pins)
	}
}

// overridesFor prices one model by hand, the owner's way out of a route the
// shipped tables do not price.
func overridesFor(id string) pricing.Overrides {
	return pricing.Overrides{id: {InputPerM: 1, OutputPerM: 2, CacheRead: 0.1, CacheWrite5m: 1.25, CacheWrite1h: 2}}
}
