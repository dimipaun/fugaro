package runner

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
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
func cascadeRun(t *testing.T, sw *config.RoutingClaude) *run {
	t.Helper()
	r := routedRun(t, sw, "claude-sonnet-5-5", "claude-opus-5")
	r.cfg.Agent.Auth = config.AuthCascade
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
