package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/task"
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
		// The route's key is mounted, as a job's environment has it: the
		// placeholder is not a credential, and gatewayRoutes refuses to build a
		// route without one.
		d: Deps{
			// A sink, so a run that logs during the switch (the Vertex/auto skip)
			// does not reach a nil logger.
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Env: []string{config.ProviderKeyEnv("openrouter-api-key") + "=" + routedProviderKey},
			Providers: map[string]config.ModelProvider{
				"openrouter": {
					Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
					Secret: "openrouter-api-key",
					// Two of the route's own Claude IDs, so a switch can name one
					// this run does not pin and still be a switch that resolves.
					Models:      []string{"anthropic/claude-sonnet-5.5", "anthropic/claude-fable-5.1"},
					AllowDataTo: []string{"acme/site"}, RouteFeePct: 5,
				},
			},
		},
	}
	r.projPolicy = policyRead{done: true, found: sw != nil, policy: &projectpolicy.Policy{Routing: &config.Routing{Claude: sw}}}
	return r
}

// routedProviderKey is the placeholder the route's key is mounted as. It is not
// a credential and never was one: the runner refuses to build a route without a
// value of the right shape, and nothing here talks to a provider.
const routedProviderKey = "sk-or-v1-0123456789abcdef-not-a-real-key"

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
	// The background model is pinned to a Claude ID the switch does not name, so
	// the reviewer's stage is about the reviewer alone: left empty it falls back
	// to the coder (config.EffectiveBackground), which is the routed model, and
	// that fallback is asserted on its own below.
	r.cfg.Agent.Models.Background = "claude-opus-5"
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
	// An agent that pins no background model sends the coder's, and the coder's
	// is now the route's: the fallback moves with the pin it falls back to, so a
	// stage still sends a model the gateway routes and prices.
	r.cfg.Agent.Models.Background = ""
	if got := r.backgroundModel(); got != routed {
		t.Errorf("backgroundModel() = %q, want the routed coder %q", got, routed)
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
		{"another model opted in", &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{"claude-fable-5-1"}}},
	} {
		// Neither pin is one the switch names: the reviewer is a Claude ID no
		// case opts in, so every case is about a switch that routes nothing
		// this run pins. A switch that *does* name a pin with no route is
		// TestRoutingRefusesAPinItCannotResolve's.
		r := routedRun(t, tc.sw, "claude-sonnet-5-5", "claude-opus-5")
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

// TestRoutingDoesNothingWithTheGatewayOff (plan Task 1b.2 step 3): routing
// exists only with the gateway, so with the budget off nothing changes — not
// for a switch that names a pin, and not for one this run could not resolve
// either: the switch has nothing to act on. A pin reaches the agent as --model
// on every stage, where a route's own ID is a model Anthropic does not serve,
// so a budget-off run keeps the direct spelling and stagePins keeps returning
// nil.
//
// Hand mutation this fails on: applying the switch before the gateway check,
// which hands a mode-off run the route's ID — and halts it on a switch it has
// nothing to route.
func TestRoutingDoesNothingWithTheGatewayOff(t *testing.T) {
	const canonical = "claude-sonnet-5-5"
	r := routedRun(t, &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{canonical}}, canonical, canonical)
	r.spend = Spend{Mode: "off"}
	r.d.Providers = map[string]config.ModelProvider{} // not even resolvable
	if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
		t.Fatalf("a budget-off run was halted by the switch: %v", err)
	}
	if got := r.cfg.Agent.ModelFor(config.RoleCoder); got != canonical {
		t.Errorf("the budget-off run's coder pin = %q, want %q: routing exists only with the gateway (D11)", got, canonical)
	}
	if pins := r.stagePins("implement"); pins != nil {
		t.Errorf("stagePins = %+v with the gateway off, want none", pins)
	}
}

// TestAutoDoesNotRouteAVertexRun (review finding on this change): via: auto
// asks whether the run has a Claude credential of its own, and a Vertex run
// has one — Vertex serves Claude under the project's own GCP project, and its
// jobs carry no Anthropic API key, so the key-presence question would answer
// "no key, route it" about a run that has a route already. The switch is
// skipped, with one line saying so, and the pins keep Claude's own IDs.
//
// via: openrouter is not skipped: it names the route outright, and a Vertex run
// that carries it is refused downstream by config.CheckProviderAuth ("a run
// does not mix credentials") rather than quietly routed.
//
// Hand mutation this fails on: treating auto as openrouter for every credential
// (the resolver's own answer, before the caller that knows the credential
// prefers the direct path), which routes a Vertex run's Claude prompts to a
// third party the operator never chose for it.
func TestAutoDoesNotRouteAVertexRun(t *testing.T) {
	const canonical = "claude-sonnet-5-5"
	sw := &config.RoutingClaude{Via: config.RouteAuto, Models: []string{canonical}}
	r := routedRun(t, sw, canonical, canonical)
	r.cfg.Agent.Auth = "vertex"
	var logs bytes.Buffer
	r.d.Log = slog.New(slog.NewTextHandler(&logs, nil))
	if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
		t.Fatalf("the auto switch halted a Vertex run: %v", err)
	}
	if got := r.cfg.Agent.ModelFor(config.RoleCoder); got != canonical {
		t.Errorf("the Vertex run's coder pin = %q, want %q: auto never routes a Vertex run", got, canonical)
	}
	if pins := r.stagePins("implement"); pins["CLAUDE_CODE_SUBAGENT_MODEL"] != canonical {
		t.Errorf("pins = %+v, want the direct spelling", pins)
	}
	if !strings.Contains(logs.String(), "routing_auto_vertex") {
		t.Errorf("the skip is silent: the run's log does not carry it:\n%s", logs.String())
	}
	// The contrast, on the same fixture: openrouter is carried out, so the skip
	// is about auto and not about Vertex.
	via := &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{canonical}}
	r = routedRun(t, via, canonical, canonical)
	r.cfg.Agent.Auth = "vertex"
	if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
		t.Fatalf("an explicit openrouter switch halted a Vertex run: %v", err)
	}
	if got := r.cfg.Agent.ModelFor(config.RoleCoder); got != "anthropic/claude-sonnet-5.5" {
		t.Errorf("via: openrouter left the Vertex run's pin at %q, want the route's ID", got)
	}
}

// TestRoutingInertReasonIsTheLadderTheRunWalks is the review finding that made
// the decision shared: `fugaro validate` and `fugaro doctor` report what a run
// does with the switch — which runs it halts and which route they take — and
// both re-derived that from the gateway alone, so validate claimed a halt no
// Vertex run makes (auto never routes one) and one only a keyless run makes
// (auto keeps the pins direct when the job has an ANTHROPIC_API_KEY), while
// doctor claimed the halt for every run. applyRouting now branches on this one
// function, and the readers ask it: one decision, four states, and the tests
// above and beside this one pin the run's own end of it
// (TestRoutingDoesNothingWithTheGatewayOff, TestAutoDoesNotRouteAVertexRun,
// TestRoutingAutoFollowsTheAnthropicKey).
//
// Hand mutation this fails on: dropping the key-presence branch, which claims
// the halt for the run that goes direct, or the Vertex one, which routes a
// Vertex run's Claude prompts through a third party.
func TestRoutingInertReasonIsTheLadderTheRunWalks(t *testing.T) {
	const canonical = "claude-sonnet-5-5"
	sw := func(via string) *config.RoutingClaude {
		return &config.RoutingClaude{Via: via, Models: []string{canonical}}
	}
	for _, tc := range []struct {
		name                        string
		sw                          *config.RoutingClaude
		auth                        string
		gatewayOn, anthropicKeyStrd bool
		want                        RoutingInert
	}{
		{"a switch, a gateway and a keyless api-key job", sw(config.RouteOpenRouter), "api-key", true, false, RoutingActs},
		{"that job with a key stored", sw(config.RouteOpenRouter), "api-key", true, true, RoutingActs},
		{"via: auto, keyless", sw(config.RouteAuto), "api-key", true, false, RoutingActs},
		{"via: auto with a key stored", sw(config.RouteAuto), "api-key", true, true, RoutingInertAutoKey},
		{"via: auto in a Vertex run", sw(config.RouteAuto), "vertex", true, false, RoutingInertAutoVertex},
		{"via: direct", sw(config.RouteDirect), "api-key", true, false, RoutingInertViaDirect},
		{"no gateway (the budget is off), whatever the via", sw(config.RouteAuto), "api-key", false, false, RoutingInertNoGateway},
		{"no gateway (the credential is not a model API's)", sw(config.RouteOpenRouter), "oauth", false, false, RoutingInertNoGateway},
		{"no switch at all", nil, "api-key", true, false, RoutingInertNoSwitch},
		// The cascade acts only on the workflow that asked for it (C1.19, O2):
		// every other credential of the same project keeps what it had.
		{"via: cascade in an api-key run", sw(config.RouteCascade), "api-key", true, false, RoutingInertCascadeOtherAuth},
		{"via: cascade in a Vertex run", sw(config.RouteCascade), "vertex", true, false, RoutingInertCascadeOtherAuth},
		{"via: cascade in a cascade run", sw(config.RouteCascade), config.AuthCascade, true, false, RoutingActs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RoutingInertReason(tc.sw, tc.auth, tc.gatewayOn, tc.anthropicKeyStrd); got != tc.want {
				t.Errorf("RoutingInertReason(%v, %q, gateway %v, key %v) = %q, want %q", tc.sw, tc.auth, tc.gatewayOn, tc.anthropicKeyStrd, got, tc.want)
			}
		})
	}
	// GatewayOn is the other half of the same reading, and the run asks it
	// through one function: the budget mode and the credential, nothing else.
	for _, tc := range []struct {
		auth, mode string
		want       bool
	}{
		{"api-key", policy.ModeEnforce, true},
		{"api-key", policy.ModeObserve, true},
		{"vertex", policy.ModeObserve, true},
		{"vertex", policy.ModeEnforce, true},
		{"vertex", policy.ModeOff, false},
		{"api-key", "", false},
		{"oauth", policy.ModeEnforce, false},
		{"", policy.ModeObserve, false},
	} {
		if got := GatewayOn(tc.auth, tc.mode); got != tc.want {
			t.Errorf("GatewayOn(%q, %q) = %v, want %v", tc.auth, tc.mode, got, tc.want)
		}
	}
}

// TestAllowedAdmitsARouteIDTheSwitchDoesNotApply is the runner half of
// internal/config/pins.go's `allowed`, and the review finding that rewrote its
// comment: config.CheckAllowed judges a pin by the switch's *resolution* — the
// same RoutingClaudeFor the run uses — and it is asked on every run, the
// gateway's or not (checkBudget asks it before gatewayOn is consulted).
// applyRouting, the switch's *application*, skips the states where the run has
// nothing to route: the gateway off, via auto under a Vertex run, via auto with
// an ANTHROPIC_API_KEY in the job. In those states a pin spelled as a route's
// own ID is admitted by the allow-list and keeps the spelling the configuration
// gave it — what admits it there is the operator's own claim on the route (the
// entry's allow_data_to, D1b's safeguard) plus the canonical model on the list,
// not the switch, which is what the comment on `allowed` now says instead of
// "the run was going to route it anyway".
//
// One fixture, four states, so the difference between them is the switch's
// application and nothing else: the reviewer's pin moves exactly where the
// switch is applied.
//
// Hand mutation this fails on: dropping a skip from applyRouting (the
// gateway-off one, then the auto/key one) — the reviewer's pin moves in a state
// that routes nothing, and the test fails naming it, which is the whole reason
// the check cannot promise that the run takes the route.
func TestAllowedAdmitsARouteIDTheSwitchDoesNotApply(t *testing.T) {
	const canonical, routed = "claude-sonnet-5-5", "anthropic/claude-sonnet-5.5"
	list := policy.Merge(policy.Layer{AllowedModels: []string{canonical}})
	for _, tc := range []struct {
		name, via string
		prepare   func(*run)
		wantPin   string
	}{
		{"the switch applies (via openrouter, the gateway on)", config.RouteOpenRouter, nil, routed},
		{"the gateway is off (the budget is off)", config.RouteOpenRouter, func(r *run) { r.spend = Spend{Mode: "off"} }, canonical},
		{"via auto in a Vertex run", config.RouteAuto, func(r *run) { r.cfg.Agent.Auth = "vertex" }, canonical},
		{"via auto with an Anthropic key in the job", config.RouteAuto, func(r *run) {
			r.d.Env = append(r.d.Env, "ANTHROPIC_API_KEY=not-a-real-key")
		}, canonical},
	} {
		// The operator pinned the route's own ID for the coder — the claim the
		// allow-list is asked about — and the model's own ID for the reviewer,
		// which is the pin the switch moves. The background model is named too,
		// so the allow-list's three questions are about two filled-in roles
		// rather than about the empty one.
		r := routedRun(t, &config.RoutingClaude{Via: tc.via, Models: []string{canonical}}, routed, canonical)
		r.cfg.Agent.Models.Background = canonical
		if tc.prepare != nil {
			tc.prepare(r)
		}
		if ps := config.CheckAllowed(r.cfg.Agent, list, nil, r.routingSwitch(), r.modelProviders()); len(ps) > 0 {
			t.Errorf("%s: CheckAllowed = %s, want the route-ID pin admitted", tc.name, problemsText(ps))
		}
		if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
			t.Fatalf("%s: applyRouting: %v", tc.name, err)
		}
		if got := r.cfg.Agent.ModelFor(config.RoleReviewer); got != tc.wantPin {
			t.Errorf("%s: the reviewer pin = %q, want %q", tc.name, got, tc.wantPin)
		}
		if got := r.cfg.Agent.ModelFor(config.RoleCoder); got != routed {
			t.Errorf("%s: the coder pin = %q, want the operator's own spelling %q", tc.name, got, routed)
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
	// A placeholder, not a key: the run only asks whether one is present.
	r.d.Env = append(r.d.Env, "ANTHROPIC_API_KEY=not-a-real-anthropic-key")
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

// TestGatewayOnForCascade (C1.19): a cascade run's calls go through the
// gateway, which is what carries them to the credentials service — so the
// budget's mode decides it exactly as it does for the other two credentials.
// The mutation: leaving cascade out of GatewayOn's list, which would leave a
// cascade run with no gateway to attach a credential and no way to spend.
func TestGatewayOnForCascade(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{policy.ModeObserve, true},
		{policy.ModeEnforce, true},
		{"", false},
		{"off", false},
	} {
		if got := GatewayOn(config.AuthCascade, tc.mode); got != tc.want {
			t.Errorf("GatewayOn(cascade, %q) = %v, want %v", tc.mode, got, tc.want)
		}
	}
	// run.gatewayOn is the same answer for a run that holds the mode.
	r := cascadeRun(t, nil)
	for _, mode := range []string{policy.ModeObserve, policy.ModeEnforce} {
		r.spend.Mode = mode
		if !r.gatewayOn() {
			t.Errorf("a cascade run with the budget %s does not route through the gateway", mode)
		}
	}
	r.spend.Mode = "off"
	if r.gatewayOn() {
		t.Error("a cascade run with the budget off routes through the gateway")
	}
}

// cascadeRun is the minimal cascade run: agent.auth cascade, the gateway on,
// and whatever switch the case hands it through the policy node.
//
// The node also publishes the model allow-list the credentials service holds
// every call of a cascade run to (the owner's ruling of 2026-10-22): a cascade
// run of a project that publishes none is refused in bootstrap
// (TestCascadeRefusedWithoutAllowList), so the fixture a test uses to get *past*
// that arm has to carry one — the node is the only layer that service reads.
func cascadeRun(t *testing.T, sw *config.RoutingClaude) *run {
	t.Helper()
	r := routedRun(t, sw, "claude-sonnet-5-5", "claude-opus-5")
	r.cfg.Agent.Auth = config.AuthCascade
	r.projPolicy.policy.Budget = projectpolicy.Budget{Mode: policy.ModeObserve, AllowedModels: []string{"claude-sonnet-5-5"}}
	return r
}

// TestCascadeRefusedWithoutPolicySwitch (O2, C1.19): a repository asks for the
// cascade and only the operator's published policy turns it on. A cascade run
// whose policy carries no switch, another via, or no policy at all is refused
// before it opens anything — and the refusal names the setting to write and
// where, so the operator can act on it.
//
// Hand mutations this fails on: acting on the switch without checking it (the
// run proceeds and its calls are forwarded to a service the operator never
// enabled); accepting via: auto (a spelling O3 rejected, which would read as
// "follow the credential" for a run that has none of its own); refusing an
// absent policy node with a message that does not name the setting.
func TestCascadeRefusedWithoutPolicySwitch(t *testing.T) {
	for _, tc := range []struct {
		name string
		sw   *config.RoutingClaude
	}{
		{"no policy node at all", nil},
		{"a switch with no via", &config.RoutingClaude{Models: []string{"claude-sonnet-5-5"}}},
		{"via: direct", &config.RoutingClaude{Via: config.RouteDirect}},
		{"via: auto", &config.RoutingClaude{Via: config.RouteAuto}},
		{"an openrouter switch", &config.RoutingClaude{Via: config.RouteOpenRouter, Models: []string{"claude-sonnet-5-5"}}},
	} {
		r := cascadeRun(t, tc.sw)
		err := r.applyRouting(r.projPolicy.policy, tc.sw != nil)
		if err == nil {
			t.Fatalf("%s: a cascade run ran with no cascade switch", tc.name)
		}
		for _, want := range []string{config.AuthCascade, "routing.claude", config.RouteCascade, "project policy publish"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal does not name %q: %v", tc.name, want, err)
			}
		}
	}
	// The refusal happens whatever the budget mode is: it is the policy that
	// enables the cascade, not the gateway.
	r := cascadeRun(t, nil)
	r.spend.Mode = "off"
	if err := r.applyRouting(r.projPolicy.policy, false); err == nil {
		t.Error("a cascade run with the budget off ran with no cascade switch")
	}
	// With via: cascade the run proceeds — and the switch routes nothing of
	// its own: the cascade names a credential, not a model, so every pin keeps
	// Claude's own ID (D133, D150).
	sw := &config.RoutingClaude{Via: config.RouteCascade}
	r = cascadeRun(t, sw)
	if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
		t.Fatalf("a cascade run with the switch: %v", err)
	}
	cascadePins := r.stagePins("implement")
	if cascadePins["CLAUDE_CODE_SUBAGENT_MODEL"] != "claude-sonnet-5-5" {
		t.Errorf("cascade pins = %+v, want the canonical ID: the cascade routes no model", cascadePins)
	}
	if _, ok := cascadePins["CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"]; ok {
		t.Error("a cascade stage carries a provider route's variables")
	}
	// The switch is inert for every other credential: an installation writes
	// it once, for its cascade workflows, and its api-key runs are untouched.
	// oauth is not in this list: its calls never go through the gateway, so
	// its stages carry no pins at all (RoutingInertNoGateway).
	for _, auth := range []string{"api-key", "vertex"} {
		r := cascadeRun(t, sw)
		r.cfg.Agent.Auth = auth
		if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
			t.Errorf("%s: applyRouting: %v", auth, err)
		}
		if pins := r.stagePins("implement"); pins["CLAUDE_CODE_SUBAGENT_MODEL"] != "claude-sonnet-5-5" {
			t.Errorf("%s: pins = %+v, want the canonical ID: via: cascade is inert for it", auth, pins)
		}
	}
}

// TestLocalCascadeIsRefusedAsALocalRun (D149, C1.19): a local `fugaro exec` on
// a cascade file is refused for what it is, and *before* the switch is judged.
// `fugaro exec` never cascades — it runs on the developer's own credentials and
// has no path to a private service — so the O2 refusal's remedy (the operator
// publishes the switch) cannot fix this run, and a developer who published
// nothing has to be told the fix that is theirs (use api-key or oauth locally)
// rather than sent at a policy that would change nothing for a local run.
//
// The order is the point: applyRouting judges the auth here, in bootstrap, for
// every local run; the other place D149 is said (serviceUpstream) is behind the
// gateway, reached only by a run whose mode turns it on — and a local cascade
// file is usually mode-off or unset, where agent.BuildEnv answers instead — so
// a local run whose mode *is* on was refused for the switch until this arm
// moved ahead of it.
//
// Mutation (run, restored): move the local arm back below the O2 check and this
// test's first case fails with the operator's remedy; drop it entirely and the
// run is refused by agent.BuildEnv's own message instead.
func TestLocalCascadeIsRefusedAsALocalRun(t *testing.T) {
	r := cascadeRun(t, nil)
	r.d.Local = true
	err := r.applyRouting(r.projPolicy.policy, false)
	if err == nil {
		t.Fatal("a local cascade run was admitted")
	}
	for _, want := range []string{execName, "D149", "api-key or oauth"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the local refusal does not name %q: %v", want, err)
		}
	}
	for _, refuse := range []string{"routing.claude", "project policy publish"} {
		if strings.Contains(err.Error(), refuse) {
			t.Errorf("a local run is sent at the switch's remedy (%q), which no local invocation can be fixed by: %v", refuse, err)
		}
	}
	// The same file on a job keeps the operator's remedy: the local refusal is
	// about the invocation, not about the cascade.
	r = cascadeRun(t, nil)
	if err := r.applyRouting(r.projPolicy.policy, false); err == nil || !strings.Contains(err.Error(), "project policy publish") {
		t.Errorf("a cascade job with no switch: %v, want the O2 refusal naming the policy", err)
	}
}

// TestCascadeRefusedWithoutAllowList (the owner's ruling of 2026-10-22, C1.11):
// the credentials service carries every call a cascade run makes and holds each
// of them to the project's published budget.allowed_models, and it refuses a
// project that publishes none — so such a run could make no call at all. It is
// refused here, in bootstrap, rather than left to halt on its first forwarded
// call: the launch refuses the same file one step earlier (internal/cli's
// cascadeLaunchRefusal), `fugaro validate` reports it to the author
// (cascadeProblems), and this is the run's own side of it, for a job that
// started some other way.
//
// What is judged is the *node's* list: the service reads the published policy
// and nothing else, so the job environment's ceiling — the other layer a run's
// merged allow-list comes from — cannot stand in for it, and the second case
// below is what says so.
//
// Mutation (run, restored): drop the arm and the first case passes with err =
// nil (the run is left to be refused call by call by the service); judge the
// run's merged or ceiling list instead of the node's and the second case is
// admitted for a run whose every call the service refuses.
func TestCascadeRefusedWithoutAllowList(t *testing.T) {
	r := cascadeRun(t, &config.RoutingClaude{Via: config.RouteCascade})
	r.projPolicy.policy.Budget.AllowedModels = nil
	err := r.applyRouting(r.projPolicy.policy, true)
	if err == nil {
		t.Fatal("a cascade run whose policy publishes no allow-list was admitted")
	}
	for _, want := range []string{config.AuthCascade, "budget.allowed_models", "project policy publish"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// The ceiling is another layer, and not the one the service reads: a run
	// whose job environment carries the allow-list still has none at the node,
	// and every call of it would be refused.
	r.spend.AllowedModels = []string{"claude-sonnet-5-5"}
	if err := r.applyRouting(r.projPolicy.policy, true); err == nil {
		t.Error("a cascade run whose ceiling publishes the allow-list and whose policy does not was admitted: the ceiling never reaches the credentials service")
	}
	// The node's list is the way out, and the run proceeds with it. via:
	// cascade routes no model of its own, so the pins stay Claude's (D133,
	// D150) — TestCascadeRefusedWithoutPolicySwitch asserts that in full.
	r.projPolicy.policy.Budget.AllowedModels = []string{"claude-sonnet-5-5"}
	if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
		t.Errorf("a cascade run whose policy publishes the allow-list: %v", err)
	}
	// The rule is the cascade's: nothing reads the allow-list for an auth whose
	// calls the gateway does not forward to the credentials service, so an
	// api-key run of the same project is not refused by it (via: cascade is
	// inert for that credential, RoutingInertCascadeOtherAuth).
	other := cascadeRun(t, &config.RoutingClaude{Via: config.RouteCascade})
	other.projPolicy.policy.Budget.AllowedModels = nil
	other.cfg.Agent.Auth = "api-key"
	if err := other.applyRouting(other.projPolicy.policy, true); err != nil {
		t.Errorf("an api-key run of a project whose policy publishes no allow-list: %v", err)
	}
}

// reviewClassRun is the minimal review class run (CSR13): a run the launcher
// marked as a review (spec.Mode, the same field finishLaunch sends a run to the
// variant on) and placed in the review-only variant of its profile's job — the
// two facts the class is keyed on — carrying the auth and the budget mode the
// case hands it, a job environment that names the installation's credentials
// service, and a published policy that carries the model allow-list the service
// holds every call it brings to.
func reviewClassRun(t *testing.T, auth, mode string) *run {
	t.Helper()
	r := svcRun(t, auth, nil)
	r.d.Env = append(r.d.Env, credentialsServiceURLEnv+"="+reviewClassServiceURL)
	r.spec = &task.Spec{Version: 1, RunID: "20261106-042141-abcd", Repo: "acme/app", Ref: "main",
		Workflow: "app", Task: "Review the pull request", Mode: recipe.ModeReview}
	r.job = jobTriple{Profile: "standard", Provider: "github",
		Account: config.ReviewVariantAccount("acme-personal") + config.ReviewVariantSuffix, Class: classReview}
	r.spend = Spend{Mode: mode}
	r.projPolicy = policyRead{done: true, found: true, policy: &projectpolicy.Policy{
		Version: projectpolicy.Version,
		Budget:  projectpolicy.Budget{Mode: policy.ModeEnforce, AllowedModels: []string{reviewClassModel}},
	}}
	return r
}

// reviewClassServiceURL is the installation's own credentials service as a
// fixture spells it, and reviewClassToken is the gateway's per-run token: both
// are placeholders, and neither is a credential (nothing here calls anything).
const (
	reviewClassServiceURL = "https://creds.example.invalid"
	reviewClassToken      = "gw-run-token-placeholder"
	reviewClassModel      = "claude-sonnet-5-5"
)

// TestReviewRunIsGatewayOnWhateverItsAuth (CSR13, O1/D143): a review class
// run's model path is its class and not agent.auth. The review job's own
// configuration is oauth — a token CSR7's variant deliberately mounts nowhere —
// and the class makes the run's calls the credentials service's: the gateway is
// on, its upstream is the service's KindService, and the environment the agent
// is built with is the class's spelling, which carries the gateway's per-run
// token and requires no secret of the job's.
//
// Red today: GatewayOn("oauth", enforce) is false, so a review class run starts
// no gateway — stagePins returns nil, the managed settings carry no gateway
// variable, and agent.BuildEnv's oauth arm demands a CLI token the variant does
// not mount — and the reviewer's calls have no path to a model at all unless
// the operator publishes the coding cascade's consent and the repository writes
// agent.auth: cascade, the two things spec.md §2 says a review job does not
// need.
func TestReviewRunIsGatewayOnWhateverItsAuth(t *testing.T) {
	for _, auth := range []string{"oauth", config.AuthCascade} {
		t.Run(auth, func(t *testing.T) {
			r := reviewClassRun(t, auth, policy.ModeEnforce)
			if !r.gatewayOn() {
				t.Fatalf("a review class run whose agent.auth is %s has no gateway: its reviewer's calls have no path to a model", auth)
			}
			r.sess = svcSession(t)
			up, err := r.gatewayUpstream(context.Background())
			if err != nil {
				t.Fatalf("the upstream of a review class run with agent.auth %s: %v", auth, err)
			}
			if up.Kind != gateway.KindService || up.BaseURL != reviewClassServiceURL {
				t.Errorf("upstream = %+v, want the credentials service's kind and URL: the class's calls are the service's", up)
			}
			// The environment the agent is built with is the class's spelling,
			// and no secret of the job's is required to build it: the gateway
			// holds the credential and presents its own per-run token.
			env, _, err := agent.BuildEnv(r.d.Env, agent.EnvSpec{
				Auth: r.modelAuth(), Gateway: &agent.Gateway{BaseURL: "http://127.0.0.1:1", Token: reviewClassToken},
			})
			if err != nil {
				t.Fatalf("building the agent's environment for a review class run with agent.auth %s: %v", auth, err)
			}
			got := map[string]string{}
			for _, kv := range env {
				if k, v, ok := strings.Cut(kv, "="); ok {
					got[k] = v
				}
			}
			if got["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:1" || got["ANTHROPIC_API_KEY"] != reviewClassToken {
				t.Errorf("the agent's environment routes to %q with %q, want the gateway's URL and its per-run token", got["ANTHROPIC_BASE_URL"], got["ANTHROPIC_API_KEY"])
			}
			if v, ok := got["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
				t.Errorf("a review class run's agent carries CLAUDE_CODE_OAUTH_TOKEN (%q): the variant mounts no token of its own", v)
			}
		})
	}
}

// TestReviewRunSkipsTheCodingCascadeSwitch (CSR13, O2): the class is the
// consent the operator's switch stands for in a coding run, so a review needs
// no routing.claude at all to be admitted — and nothing more than the switch is
// skipped: the class is decided by the run's mode and the job it was admitted
// to, so the same policy still refuses a *coding* cascade run, and the
// environment marker a launcher writes (FUGARO_REVIEW_JOB) buys a coding run
// nothing.
//
// Mutation (run, restored): key the class on the environment marker instead of
// the run's mode and the job — the third case is admitted to the class, and a
// container that writes one variable trades its own credential for another
// class's. Skip the allow-list arm along with O2's → the second case of
// TestReviewRunIsStillHeldToTheAllowList fails.
func TestReviewRunSkipsTheCodingCascadeSwitch(t *testing.T) {
	// No routing.claude anywhere in the policy: a coding run with
	// agent.auth: cascade is refused, and the review class run is not.
	r := reviewClassRun(t, "oauth", policy.ModeEnforce)
	if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
		t.Fatalf("a review class run whose policy publishes no switch: %v", err)
	}
	coding := cascadeRun(t, nil)
	coding.d.Env = append(coding.d.Env, EnvReviewJob+"=1")
	if err := coding.applyRouting(coding.projPolicy.policy, false); err == nil {
		t.Fatal("a coding cascade run with no published switch was admitted")
	} else if !strings.Contains(err.Error(), "project policy publish") {
		t.Errorf("the coding refusal is not O2's: %v", err)
	}
	// The spoof: a coding-mode run in the shared job whose environment carries
	// the marker. It keeps its own configuration's auth, so with agent.auth:
	// cascade O2's arm refuses it — the marker never gives it the class.
	spoof := cascadeRun(t, nil)
	spoof.d.Env = append(spoof.d.Env, EnvReviewJob+"=1")
	if spoof.modelAuth() != config.AuthCascade {
		t.Fatalf("the spoof's own auth is %q", spoof.modelAuth())
	}
	if err := spoof.applyRouting(spoof.projPolicy.policy, false); err == nil {
		t.Fatal("a coding run whose environment carries the review marker was admitted with no published switch: the marker buys the class")
	}
}

// TestReviewRunIsStillHeldToTheAllowList (CSR13, C1.11): the arm O2's skip does
// *not* carry with it. The credentials service holds every /v1/messages call it
// carries to the project's budget.allowed_models and refuses a project that
// publishes none — the node is read before the class is resolved
// (internal/credsvc/server.go) — so a review class run of such a project could
// make no call at all, whether its repository wrote oauth or cascade. Both are
// refused in bootstrap rather than left to halt on the first forwarded call.
//
// "The switch does not apply" and "no rule applies" are one word apart in the
// wrong direction, which is why this is a test of its own.
//
// Mutation (run, restored): skip the allow-list arm along with O2's for a class
// run. Both cases are admitted and the reviewer's first call is refused by the
// service instead.
func TestReviewRunIsStillHeldToTheAllowList(t *testing.T) {
	for _, auth := range []string{"oauth", config.AuthCascade} {
		t.Run(auth, func(t *testing.T) {
			r := reviewClassRun(t, auth, policy.ModeEnforce)
			r.projPolicy.policy.Budget.AllowedModels = nil
			err := r.applyRouting(r.projPolicy.policy, true)
			if err == nil {
				t.Fatal("a review class run whose policy publishes no allow-list was admitted: the credentials service would refuse its first call")
			}
			for _, want := range []string{"budget.allowed_models", "project policy publish"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			// The ceiling is another layer, and not the one the service reads.
			r.spend.AllowedModels = []string{reviewClassModel}
			if err := r.applyRouting(r.projPolicy.policy, true); err == nil {
				t.Error("a review class run whose ceiling publishes the allow-list and whose policy does not was admitted: the ceiling never reaches the credentials service")
			}
			// The node's own list is the way out.
			r.projPolicy.policy.Budget.AllowedModels = []string{reviewClassModel}
			if err := r.applyRouting(r.projPolicy.policy, true); err != nil {
				t.Errorf("a review class run whose policy publishes the allow-list: %v", err)
			}
		})
	}
}

// TestCodingRunKeepsItsOwnModelPath (CSR13): the second direction of the whole
// task, and the one a careless accessor breaks. Every auth a coding run has
// today keeps the gateway state, the upstream and the environment variables it
// had: the class moves nothing for a run that is not a review, and the
// configuration is not rewritten — it is an accessor, so the record's cost
// basis and the account walk read the file the repository wrote.
//
// Mutation (run, restored): make the class a rewrite of r.cfg.Agent.Auth rather
// than an accessor — the oauth case alone moves (its gateway would come on, and
// its agent's environment would be the cascade's), and so do the record's
// basis and the account walk with it.
func TestCodingRunKeepsItsOwnModelPath(t *testing.T) {
	for _, tc := range []struct {
		auth          string
		wantGatewayOn bool
		wantUpstream  string
	}{
		{"oauth", false, ""},
		{"api-key", true, "anthropic"},
		{"vertex", true, "vertex"},
		{config.AuthCascade, true, gateway.KindService},
	} {
		t.Run(tc.auth, func(t *testing.T) {
			r := codingRun(t, tc.auth, policy.ModeEnforce)
			if got := r.modelAuth(); got != tc.auth {
				t.Fatalf("modelAuth() = %q, want the configuration's own %q", got, tc.auth)
			}
			if got := r.gatewayOn(); got != tc.wantGatewayOn {
				t.Errorf("gatewayOn() = %v, want %v", got, tc.wantGatewayOn)
			}
			if !tc.wantGatewayOn {
				return
			}
			r.sess = svcSession(t)
			up, err := r.gatewayUpstream(context.Background())
			if err != nil {
				t.Fatalf("gatewayUpstream: %v", err)
			}
			if up.Kind != tc.wantUpstream {
				t.Errorf("upstream kind = %q, want %q", up.Kind, tc.wantUpstream)
			}
		})
	}
}

// codingRun is reviewClassRun with the two facts of the class taken away: the
// launcher's mode is the ordinary one and the job the platform reports is the
// shared coding job of the fixture's profile. Everything else — the job
// environment, the policy — is the same, so the two fixtures differ in nothing
// but the class.
func codingRun(t *testing.T, auth, mode string) *run {
	t.Helper()
	r := svcRun(t, auth, nil)
	r.d.Env = append(r.d.Env,
		credentialsServiceURLEnv+"="+reviewClassServiceURL,
		"ANTHROPIC_VERTEX_PROJECT_ID=aurora-fp", "CLOUD_ML_REGION=europe-west1")
	r.spec = &task.Spec{Version: 1, RunID: "20261106-042141-abcd", Repo: "acme/app", Ref: "main",
		Workflow: "app", Task: "Add a feature"}
	r.job = jobTriple{Profile: "standard", Provider: "github", Account: "acme-personal"}
	r.spend = Spend{Mode: mode}
	r.projPolicy = policyRead{done: true, found: true, policy: &projectpolicy.Policy{Version: projectpolicy.Version}}
	if auth == "vertex" {
		// A test token source: nothing here dials Google, and a run with a
		// test upstream must never be paired with real credentials.
		r.d.VertexTokens = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "ya29.placeholder"})
	}
	return r
}

// withoutCredentialsService is a fixture's job environment with the credentials
// service's URL taken out: the installation that has not opted into one (D147).
func withoutCredentialsService(r *run) *run {
	out := make([]string, 0, len(r.d.Env))
	for _, kv := range r.d.Env {
		if strings.HasPrefix(kv, credentialsServiceURLEnv+"=") {
			continue
		}
		out = append(out, kv)
	}
	r.d.Env = out
	return r
}

// TestReviewRunCarriesNoCredentialOfItsOwn (CSR13, O1, D143): the shape CSR7's
// variant was built to make true. The class's run holds neither an Anthropic
// key nor a Claude CLI token — the job's environment is asked for one and is
// given the gateway's per-run token under ANTHROPIC_API_KEY, which is what
// Claude Code presents at the gateway — and the managed settings carry the
// same address and the same token, so a stage that reads either file is routed
// at the gateway and holds nothing of the job's.
//
// Mutation (run, restored): build the agent's environment from the repository's
// own auth instead of the class's → BuildEnv's oauth arm refuses the run
// outright for a missing CLAUDE_CODE_OAUTH_TOKEN, and the planted key would be
// the value under ANTHROPIC_API_KEY if the gateway were dropped.
func TestReviewRunCarriesNoCredentialOfItsOwn(t *testing.T) {
	const plantedKey = "sk-ant-PLANTED-0123456789abcdefghijklmnop"
	const plantedToken = "CLI-TOKEN-PLANTED-0123456789"
	r := reviewClassRun(t, "oauth", policy.ModeEnforce)
	r.d.Env = append(r.d.Env, "ANTHROPIC_API_KEY="+plantedKey, "CLAUDE_CODE_OAUTH_TOKEN="+plantedToken)
	r.d.ManagedSettingsPath = filepath.Join(t.TempDir(), "managed-settings.json")
	r.gwAgent = &agent.Gateway{BaseURL: "http://127.0.0.1:1", Token: reviewClassToken}

	env, _, err := agent.BuildEnv(r.d.Env, agent.EnvSpec{Auth: r.modelAuth(), Gateway: r.gateway()})
	if err != nil {
		t.Fatalf("building the agent's environment: %v", err)
	}
	got := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			got[k] = v
		}
	}
	if got["ANTHROPIC_API_KEY"] != reviewClassToken {
		t.Errorf("the agent's ANTHROPIC_API_KEY is %q, want the gateway's per-run token: the job's own key must never reach it", got["ANTHROPIC_API_KEY"])
	}
	if _, ok := got["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		t.Error("a review class run's agent carries CLAUDE_CODE_OAUTH_TOKEN: the variant mounts no token of its own")
	}
	if err := r.writeManagedSettings("review", nil); err != nil {
		t.Fatalf("writing the managed settings: %v", err)
	}
	raw, err := os.ReadFile(r.managedPath())
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("the managed settings are not JSON: %v", err)
	}
	if settings.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:1" || settings.Env["ANTHROPIC_API_KEY"] != reviewClassToken {
		t.Errorf("the managed settings route to %q with %q, want the gateway's URL and its per-run token", settings.Env["ANTHROPIC_BASE_URL"], settings.Env["ANTHROPIC_API_KEY"])
	}
	if strings.Contains(string(raw), plantedKey) || strings.Contains(string(raw), plantedToken) {
		t.Error("the managed settings carry a credential of the job's")
	}
}

// TestReviewRunUpstreamRefusesWithoutAService (CSR13, D147/D143): the cascade
// arm's own refusal, reached through the class. An installation that has not
// opted into a credentials service has no service to call and the run has no
// credential of its own to fall back to — the job's environment is asked for an
// Anthropic key and is refused rather than quietly served by it. The second
// case is the same run with a test upstream in Deps: a cascade run's
// destination is the installation's own service and nothing else, so a test
// that set both is told rather than silently sent to the service.
//
// Mutation (run, restored): let the class answer with no service URL — the run
// is admitted with an upstream nobody chose, and its first call reaches
// Anthropic on a credential the class never picked.
func TestReviewRunUpstreamRefusesWithoutAService(t *testing.T) {
	const plantedKey = "sk-ant-PLANTED-0123456789abcdefghijklmnop"
	r := withoutCredentialsService(reviewClassRun(t, "oauth", policy.ModeEnforce))
	r.d.Env = append(r.d.Env, "ANTHROPIC_API_KEY="+plantedKey)
	r.sess = svcSession(t)
	up, err := r.gatewayUpstream(context.Background())
	if err == nil {
		t.Fatalf("a review class run with no credentials service built the upstream %+v", up)
	}
	for _, want := range []string{credentialsServiceURLEnv, "D147", "D143"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), plantedKey) {
		t.Errorf("the refusal prints the job's own key: %v", err)
	}
	// A test upstream paired with a service run: the class is refused the same
	// way, because its destination is the installation's service.
	r = reviewClassRun(t, "oauth", policy.ModeEnforce)
	r.d.GatewayUpstream = "http://test-upstream.invalid"
	r.sess = svcSession(t)
	if _, err := r.gatewayUpstream(context.Background()); err == nil || !strings.Contains(err.Error(), credentialsServiceURLEnv) {
		t.Errorf("a review class run with a test upstream and a service: %v, want the service's own refusal", err)
	}
}

// TestReviewJobModelPathIsKeyedOnTheRunClass (CSR13): what the class is keyed
// on, and the correction this task was re-read for. It is the run's mode and
// the job the run was admitted to, never the environment marker the launcher
// writes (EnvReviewJob, the read r.reviewJob performs): the marker is a
// permission the caller states (reviewMayComment), and a credential may not be
// chosen by a variable a container can carry.
//
// The three directions:
//
//   - a run in the review variant, mode: review, whose environment carries no
//     marker at all is given the class — nothing reads the variable;
//   - a run in the review variant whose mode is not review is refused admission
//     by CSR8's own door (checkJobClass) before this accessor is asked, so the
//     marker buys it no class;
//   - a coding-mode run in the shared job whose environment carries
//     FUGARO_REVIEW_JOB=1 is not given the class: it keeps its own
//     configuration's auth (and with agent.auth: cascade O2's arm then refuses
//     it, TestReviewRunSkipsTheCodingCascadeSwitch's third case).
//
// Mutation (run, restored): key the class on the marker instead of the run's
// mode and the job — the third direction is given the class, and a container
// that writes one variable trades the App key and the Claude token it holds for
// another class's credential.
func TestReviewJobModelPathIsKeyedOnTheRunClass(t *testing.T) {
	// The marker is nowhere in this run's environment: the class is not read
	// from it.
	r := reviewClassRun(t, "oauth", policy.ModeEnforce)
	if envLookup(r.d.Env, EnvReviewJob) != "" {
		t.Fatalf("the fixture carries %s already", EnvReviewJob)
	}
	if got := r.modelAuth(); got != config.AuthCascade {
		t.Errorf("a review class run with no marker: modelAuth() = %q, want %s", got, config.AuthCascade)
	}
	// A run in the review variant whose mode is not review: the class check
	// (CSR8) refuses it before any model path is chosen.
	notReview := codingRun(t, "oauth", policy.ModeEnforce)
	notReview.job = r.job
	notReview.d.Env = append(notReview.d.Env, EnvReviewJob+"=1")
	if err := notReview.checkJobClass(notReview.job); err == nil {
		t.Fatal("a coding-mode run was admitted to the review variant's job")
	}
	if got := notReview.modelAuth(); got != "oauth" {
		t.Errorf("a run the class check refuses: modelAuth() = %q, want its own configuration's auth", got)
	}
	// The spoof: a coding-mode run in the shared job carrying the marker.
	spoof := codingRun(t, "oauth", policy.ModeEnforce)
	spoof.d.Env = append(spoof.d.Env, EnvReviewJob+"=1")
	if got := spoof.modelAuth(); got != "oauth" {
		t.Errorf("a coding run carrying %s: modelAuth() = %q, want its own configuration's auth", EnvReviewJob, got)
	}
	if spoof.gatewayOn() {
		t.Error("a coding oauth run carrying the review marker was given a gateway: the marker bought it the class")
	}
}

// TestReviewRunWithTheBudgetOffNeverReachesACredentialOfItsOwn (CSR13, Open
// item 6's answer of 2026-11-05/06): a cloud review does not run with the
// installation's budget off. The class's calls are the credentials service's,
// the service is reached through the gateway, and a mode of off (or none) is
// what leaves the run nowhere to send them — so the class refuses in bootstrap,
// naming budget.mode published as observe or enforce as the fix, rather than
// falling through to agent.BuildEnv's cascade arm, whose sentence is written
// for a coding run and names a fix a review job cannot apply.
//
// The class does not force the gateway on: GatewayOn(cascade, "") is false, and
// the reading Open item 6 refused is one where a mode the installation set to
// off would be overridden by the class.
//
// Mutation (run, restored): force the gateway on in the class's own path — the
// run's gateway would be "on" in a mode the gateway is not built for, and the
// refusal below is never reached. Drop the arm and the run stops at
// agent.BuildEnv's cascade sentence instead, which names the wrong fix.
func TestReviewRunWithTheBudgetOffNeverReachesACredentialOfItsOwn(t *testing.T) {
	for _, mode := range []string{"", policy.ModeOff} {
		t.Run("mode "+mode, func(t *testing.T) {
			r := reviewClassRun(t, "oauth", mode)
			if r.gatewayOn() {
				t.Fatal("a review class run with the budget off routes through the gateway")
			}
			err := r.applyRouting(r.projPolicy.policy, true)
			if err == nil {
				t.Fatal("a review class run with the installation's budget off was admitted: its calls have nowhere to go")
			}
			for _, want := range []string{"budget.mode", policy.ModeObserve, policy.ModeEnforce, "project policy publish"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			// It is the class's own sentence and not agent.BuildEnv's: that one
			// is about a coding run's cascade and names a fix a review job's own
			// file cannot apply.
			if strings.Contains(err.Error(), "this agent has none") {
				t.Errorf("the refusal is agent.BuildEnv's cascade sentence: %v", err)
			}
			// And the class offers one credential and no fallback: built with no
			// gateway, the agent's environment is refused rather than given one
			// of the job's.
			if _, _, err := agent.BuildEnv(r.d.Env, agent.EnvSpec{Auth: r.modelAuth()}); err == nil {
				t.Error("a review class run with no gateway built an agent environment: it would carry a credential nobody chose")
			}
		})
	}
}

// TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn (CSR13, O1 and the owner's cut
// of 2026-11-06): a repository's own api-key configuration cannot produce an
// api-key review. The class's one credential is the pairing account's tier-A
// OAuth one (reviewCandidates), and the api-key review is a later release, so
// the run is refused in bootstrap with a sentence that names the api-key review
// and the release it is in — and nothing in 0.20.0 reads as though it works.
// vertex is refused the same way: it is a credential of the repository's, and
// the review variant mounts none of them (CSR7).
//
// The refusal is the auth's and not the budget's, so it is the same sentence
// whatever the mode says: an api-key review is refused before a mode can be
// asked about.
//
// Mutation (run, restored): let a repository's own agent.auth: api-key give the
// review run a key — the run is admitted and its first call is served by a
// credential the class never picked.
func TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn(t *testing.T) {
	for _, auth := range []string{"api-key", "vertex"} {
		for _, mode := range []string{policy.ModeEnforce, policy.ModeOff} {
			t.Run(auth+"/"+mode, func(t *testing.T) {
				r := reviewClassRun(t, auth, mode)
				err := r.applyRouting(r.projPolicy.policy, true)
				if err == nil {
					t.Fatal("a review whose repository writes a credential of its own was admitted")
				}
				for _, want := range []string{auth, "api-key review", "later release", "0.20.0 ships the OAuth review alone", "oauth"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not name %q: %v", want, err)
					}
				}
				// The class is an accessor and not a rewrite: the repository's
				// own file is what the record's basis and the account walk read.
				if r.cfg.Agent.Auth != auth {
					t.Errorf("the run's own auth was rewritten to %q", r.cfg.Agent.Auth)
				}
			})
		}
	}
}
