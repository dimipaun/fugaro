package runner_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

const deepseek = "deepseek/deepseek-v4-flash"

// claimedSenior is the Claude ID the claim fixtures route: one explicit
// "anthropic/<id>" a route names, the only shape the mixed-credentials
// opt-in admits (config.CheckClaudeClaim, Task 5). A pattern, a ":variant",
// a "[1m]" spelling or a bare claude-* name is refused at the route, so this
// is the whole permission the owner grants.
const claimedSenior = "anthropic/claude-sonnet-5.5"

// claimPricesJSON is the owner's model_prices row for the claimed ID, as the
// job's ceiling carries it (FUGARO_MODEL_PRICES, written by fugaro update from
// the project config): until the price catalog ships an anthropic/<id> row
// (P11) this override is the only price the ID has, and the price table keys
// it whatever the routes say (P4). Its rates are deliberately not the direct
// Claude row's for the same family (claude-sonnet-5-5 is $2/$10 per million
// tokens, internal/pricing/table.go), so a run that charged the reviewer at
// the direct-path rate shows in the figures (TestClaimedClaudeSeniorCharge).
const claimPricesJSON = `{"anthropic/claude-sonnet-5.5": {"input_per_m": 3, "output_per_m": 15}}`

// providerGW is a run whose coder is the provider's model, on repository
// acme/app, with the owner's provider allowing the repos in allow.
func providerGW(t *testing.T, cfg string, allow ...string) *gw {
	t.Helper()
	return providerRun(t, cfg, "enforce", "50", nil, allow...)
}

// providerRun is providerGW in a budget mode with a cap, whose provider
// answers with script. The provider's key is mounted as the job does.
func providerRun(t *testing.T, cfg, mode, capUSD string, script []anthropicfake.Reply, allow ...string) *gw {
	t.Helper()
	return routeRun(t, cfg, mode, capUSD, []string{"deepseek/*"}, "", script, allow...)
}

// routeRun is providerRun with the route's models and the owner's
// model_prices overrides given outright: the provider's fake, the routes as
// the job's environment carries them (FUGARO_MODEL_PROVIDERS) and the run's
// price table, built from those same routes and overrides. pricesJSON is
// FUGARO_MODEL_PRICES as fugaro update writes it ("" for none), so the claimed
// fixtures price their ID through the shipped parse path and not around it.
func routeRun(t *testing.T, cfg, mode, capUSD string, models []string, pricesJSON string, script []anthropicfake.Reply, allow ...string) *gw {
	t.Helper()
	g := newGW(t, cfg, mode, capUSD)
	wireRoutes(t, g, models, pricesJSON, script, allow...)
	return g
}

// wireProviders gives a harness the owner's provider: the route the job
// carries (Deps.Providers and FUGARO_MODEL_PROVIDERS, which the run's price
// table is built from), the key mounted as the job does it, and the fake
// upstream the provider's calls reach. It is separate from providerRun because
// a test may already have built its harness for another reason (the policy
// node's, say) and still want the route.
func wireProviders(t *testing.T, g *gw, script []anthropicfake.Reply, allow ...string) {
	t.Helper()
	wireRoutes(t, g, []string{"deepseek/*"}, "", script, allow...)
}

// wireRoutes is wireProviders with the route's models and the owner's
// model_prices overrides (FUGARO_MODEL_PRICES as fugaro update writes it, "" for
// none) given outright, so a claimed fixture prices its ID through the shipped
// parse path and not around it.
func wireRoutes(t *testing.T, g *gw, models []string, pricesJSON string, script []anthropicfake.Reply, allow ...string) {
	t.Helper()
	compat, up := anthropicfake.New(t, script...)
	g.compat = compat
	providers := map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: up.URL, Auth: "bearer",
		Secret: "openrouter-api-key", Models: models, AllowDataTo: allow,
	}}
	g.deps.Providers = providers
	// The job's environment carries the same routes (fugaro update writes
	// FUGARO_MODEL_PROVIDERS from the owner's local config) and the run's
	// price table is built from them (P5): without a route no model is
	// claimed, so the built-in catalog has no row for it. newGW built the
	// spend before the routes existed, so rebuild it with them.
	raw, err := json.Marshal(providers)
	if err != nil {
		t.Fatal(err)
	}
	// The mode and the cap the harness already carries, read back off its
	// Spend: the caller passed them to newGW, and this rebuild is about the
	// routes.
	env := map[string]string{runner.BudgetModeEnv: g.deps.Spend.Mode, runner.ModelProvidersEnv: string(raw)}
	if g.deps.Spend.Cap > 0 {
		env[runner.MaxRunUSDEnv] = strconv.FormatFloat(g.deps.Spend.Cap.USD(), 'f', -1, 64)
	}
	if pricesJSON != "" {
		env[runner.ModelPricesEnv] = pricesJSON
	}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	ov, err := pricing.ParseOverrides(pricesJSON)
	if err != nil {
		t.Fatal(err)
	}
	// That table, read from the shipped catalog a day after its own
	// checked_at rather than at the wall clock: the catalog's 30 days
	// (catalog.StaleAfter) must not decide from today's date whether a test
	// prices a route at the cheapest end of its tier or the dearest one.
	// Anything a test wants to say about the clock it says by assigning
	// g.deps.Spend.Prices itself (TestResultJSONCatalogStale). The overrides
	// are the ones the environment carried, parsed by the shipped path, so
	// what the run charges is the owner's row wherever the catalog has none.
	if spend.Prices != nil {
		spend.Prices = freshCatalogTable(t, providers, ov)
	}
	g.deps.Spend = spend
	withProviderKey(g.harness)
}

// providerPinned has the provider's model as the coder. Its first-line
// review is off, which `auto` would turn on (providerFirstLine): the tests
// that use it script one review.
func providerPinned(t *testing.T) string {
	// first_line_rounds comes with the first line: the cases built on this
	// fixture script one cheap round, and an unset knob would take the
	// resolved mode's row instead (internal/rigor's DefaultRounds).
	return strings.Replace(gwConfig(t, "  first_line_review: off\n  first_line_rounds: 1\n"), "coder: "+sonnet, "coder: "+deepseek, 1)
}

// providerFirstLine is providerPinned with first_line_review set to mode
// ("" leaves it to the default, auto).
func providerFirstLine(t *testing.T, mode string) string {
	t.Helper()
	extra := ""
	if mode != "" {
		extra = "  first_line_review: " + mode + "\n"
	}
	return strings.Replace(providerPinned(t), "  first_line_review: off\n", extra, 1)
}

// claimedPinned is providerPinned with the reviewer on the claimed Claude ID
// instead of Claude's own: the coder stays the route's deepseek model, and
// the background stays the explicit Claude haiku -- if it were unset it would
// become the coder's model (config.EffectiveBackground) and the reviewer
// stage would carry the provider's variables for a reason these fixtures are
// not about (Review Focus 10). The first line is off, as providerPinned has
// it: the derived-recipe half of Task 6 step 4 is what turns on `auto`.
func claimedPinned(t *testing.T) string {
	t.Helper()
	return claimedSeniorFor(t, providerPinned(t))
}

// claimedRecipe is providerPinned with its first_line_review line replaced by
// extra ("" leaves the setting to its default, auto) and the reviewer on the
// claimed ID, so a test can put `recipe:` where the setting was.
func claimedRecipe(t *testing.T, extra string) string {
	t.Helper()
	cfg := strings.Replace(providerPinned(t), "  first_line_review: off\n", extra, 1)
	if cfg == providerPinned(t) {
		t.Fatal("providerPinned has no first_line_review line")
	}
	return claimedSeniorFor(t, cfg)
}

// claimedSeniorOnly is claimedPinned with the coder back on Claude's own
// model: the only provider-served pin left is the claimed senior, so a
// refusal that names agent.models.reviewer can only be about the claim. The
// run reports the first problem it finds (the coder's row comes first in
// CheckProviderGateway), which is why one case needs this shape.
func claimedSeniorOnly(t *testing.T) string {
	t.Helper()
	out := strings.Replace(claimedPinned(t), "coder: "+deepseek, "coder: "+sonnet, 1)
	if out == claimedPinned(t) {
		t.Fatal("claimedPinned has no coder line")
	}
	return out
}

// claimedSeniorFor is cfg with the reviewer pinned to the claimed Claude ID.
func claimedSeniorFor(t *testing.T, cfg string) string {
	t.Helper()
	out := strings.Replace(cfg, "reviewer: "+sonnet, "reviewer: "+claimedSenior, 1)
	if out == cfg {
		t.Fatal("the fixture has no reviewer line")
	}
	return out
}

// claimedRun is the Task 6 fixture: a route that names the claimed Claude ID
// explicitly (among its models), the reviewer on that ID and the coder on the
// route's deepseek model, the owner's model_prices row for the claimed ID and
// the provider's key mounted. claimedRunUnclaimed is the control: the same
// ceiling, the same price row, no route naming the ID.
func claimedRun(t *testing.T, cfg, mode, capUSD string, script []anthropicfake.Reply, allow ...string) *gw {
	t.Helper()
	return routeRun(t, cfg, mode, capUSD, claimedRoutes(), claimPricesJSON, script, allow...)
}

func claimedRunUnclaimed(t *testing.T, cfg, mode, capUSD string, script []anthropicfake.Reply, allow ...string) *gw {
	t.Helper()
	return routeRun(t, cfg, mode, capUSD, []string{"deepseek/*"}, claimPricesJSON, script, allow...)
}

// claimedRoutes are the route's models when it claims the Claude ID: the
// coder's model and the one explicit anthropic/<id>.
func claimedRoutes() []string { return []string{"deepseek/*", claimedSenior} }

// refusedClaimedRun is refusedBeforeAnyCall for the claimed fixtures, with
// the two carried review items: neither upstream may have seen a call, and
// the run must not have asked for a credential. The last one is asserted by
// unmounting the provider's key first: a run that reached for it would fail
// with "not mounted" instead of the policy refusal, so the substrings asked
// for below are what tells the two apart.
func refusedClaimedRun(t *testing.T, g *gw, want ...string) {
	t.Helper()
	g.deps.Env = filterEnv(g.deps.Env, orProvider.SecretEnv())
	refusedBeforeAnyCall(t, g, want...)
	if n := g.compat.Count(); n != 0 {
		t.Errorf("the provider upstream saw %d calls of a refused run", n)
	}
	if strings.Contains(g.logs.String(), providerKey) {
		t.Error("the log carries the provider key")
	}
}

// refusedBeforeAnyCall asserts the run ended as an infra_error naming every
// want, with no agent started and nothing sent to any upstream.
func refusedBeforeAnyCall(t *testing.T, g *gw, want ...string) {
	t.Helper()
	rec, err := g.run(t)
	if err == nil || rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	for _, w := range want {
		if !strings.Contains(rec.Reason, w) {
			t.Errorf("reason %q lacks %q", rec.Reason, w)
		}
	}
	if len(g.agent.calls) != 0 {
		t.Error("the agent ran")
	}
	if g.fake.Count() != 0 {
		t.Errorf("the upstream saw %d calls", g.fake.Count())
	}
}

func TestNoCallBeforeRefusal(t *testing.T) {
	t.Run("repository not in allow_data_to", func(t *testing.T) {
		refusedBeforeAnyCall(t, providerGW(t, providerPinned(t), "acme/other"), "allow_data_to", "agent.models.coder", "acme/app")
	})
	t.Run("no provider claims the model", func(t *testing.T) {
		g := providerGW(t, providerPinned(t), "acme/app")
		g.deps.Providers = nil
		refusedBeforeAnyCall(t, g, "no provider serves it", "agent.models.coder")
	})
	t.Run("a variant", func(t *testing.T) {
		cfg := strings.Replace(providerPinned(t), deepseek, deepseek+":free", 1)
		refusedBeforeAnyCall(t, providerGW(t, cfg, "acme/app"), "variant")
	})
	t.Run("oauth", func(t *testing.T) {
		cfg := strings.Replace(providerPinned(t), "auth: api-key", "auth: oauth", 1)
		refusedBeforeAnyCall(t, providerGW(t, cfg, "acme/app"), "oauth", "api-key")
	})
	t.Run("vertex", func(t *testing.T) {
		cfg := strings.Replace(providerPinned(t), "auth: api-key", "auth: vertex", 1)
		refusedBeforeAnyCall(t, providerRun(t, cfg, "observe", "", nil, "acme/app"), "vertex", "api-key")
	})
	t.Run("the repository's own allowed_models names an unapproved provider model", func(t *testing.T) {
		cfg := gwConfig(t, "") + "budget:\n  allowed_models: [" + sonnet + ", " + haiku + ", " + deepseek + "]\n"
		refusedBeforeAnyCall(t, providerGW(t, cfg, "acme/other"), "budget.allowed_models[2]", "allow_data_to")
	})
	t.Run("the owner's allow-list does not carry it", func(t *testing.T) {
		g := providerGW(t, providerPinned(t), "acme/app")
		// The ceiling the job carries names the routes too (fugaro update
		// writes FUGARO_MODEL_PROVIDERS from the owner's local config): a
		// ceiling built without them has no price for the route's model and
		// refuses for that instead, which is not what this case is about.
		raw, err := json.Marshal(g.deps.Providers)
		if err != nil {
			t.Fatal(err)
		}
		g.deps.Spend = ceilingOf(t, runner.BudgetModeEnv, "enforce", runner.MaxRunUSDEnv, "5",
			runner.AllowedModelsEnv, sonnet+","+haiku, runner.ModelProvidersEnv, string(raw))
		refusedBeforeAnyCall(t, g, "agent.models.coder", "allowed_models from ceiling")
	})
}

// TestAllowDataToStillGatesProviderRoutes (plan Task 2.1, Review Focus 8; the
// plan files it under internal/gateway, where it cannot live: internal/gateway
// has no repository identity at all and never reads allow_data_to -- the gate
// is the runner's provider-policy check, enforced before any call, and this
// file is where the rest of that check is tested). D2 stores keys
// project-wide, and this pins that it changes nothing about which
// repositories' code a provider route may carry: the data-policy gate is
// unchanged, and a key mounted in a shared job is still no permission.
func TestAllowDataToStillGatesProviderRoutes(t *testing.T) {
	t.Run("a repository the list does not name is refused", func(t *testing.T) {
		refusedBeforeAnyCall(t, providerGW(t, providerPinned(t), "acme/other"), "allow_data_to", "acme/app")
	})
	t.Run("a repository the list names runs", func(t *testing.T) {
		g := providerGW(t, providerPinned(t), "acme/app")
		rec, err := g.run(t, implement("feature"), review("ship", 0))
		if err != nil || rec.Status != runstore.StatusSucceeded {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
	})
}

// TestUnclaimedClaudeSeniorRefused pins Review Focus 6 and 1 at run level:
// the claimed ID is priced -- the owner's model_prices row is there, and the
// price table accepts it whatever the routes say (P4) -- and the run is
// refused anyway, before any call, because no route names it. The price
// table's acceptance is never permission; the claim is the route entry.
func TestUnclaimedClaudeSeniorRefused(t *testing.T) {
	g := claimedRunUnclaimed(t, claimedPinned(t), "enforce", "50", nil, "acme/app")
	refusedClaimedRun(t, g,
		"agent.models.reviewer", claimedSenior,
		"is a Claude model", "go direct to Anthropic", "no provider serves it",
		"naming that exact ID in a route's models")
	// The contrast, so the refusal is about the claim and not about the ID:
	// the same configuration with the route naming it runs (provider_run_test).
	if _, _, ok := config.ProviderFor(g.deps.Providers, claimedSenior); ok {
		t.Fatal("the control fixture claims the ID: it is not a control")
	}
}

// TestClaimedClaudeSeniorAllowDataToRefused pins the first of the three
// refusals a claim must not erode (Review Focus 12, the security story): a
// route that names the Claude ID grants passage through the claim rule and
// nothing else. The owner's allow_data_to still decides whose code may be
// sent there, for the claimed senior exactly as for the coder.
func TestClaimedClaudeSeniorAllowDataToRefused(t *testing.T) {
	g := claimedRun(t, claimedPinned(t), "enforce", "50", nil, "acme/other")
	refusedClaimedRun(t, g, "allow_data_to", "agent.models.reviewer", "agent.models.coder", "acme/app")
}

// TestClaimedClaudeSeniorOAuthRefused pins the second: an oauth token is a
// Claude subscription credential and is never sent to another provider,
// however Claude the pin looks (CheckProviderAuth). This is the case an owner
// reading only the docs would walk into (Review Focus 12).
func TestClaimedClaudeSeniorOAuthRefused(t *testing.T) {
	cfg := strings.Replace(claimedPinned(t), "auth: api-key", "auth: oauth", 1)
	if cfg == claimedPinned(t) {
		t.Fatal("the fixture has no auth line")
	}
	g := claimedRun(t, cfg, "enforce", "50", nil, "acme/app")
	refusedClaimedRun(t, g,
		"agent.models.reviewer", claimedSenior,
		"never sent to another provider", "agent.auth: api-key")
}

// TestClaimedClaudeSeniorObserveRefused pins the third: observe never refuses
// a call and a provider's calls are real dollars, so a claimed Claude senior
// needs budget.mode enforce like any other routed model (CheckProviderGateway).
// The coder is Claude's own model here, so the claimed senior is the only
// provider-served pin and the refusal this run reports is about the claim.
func TestClaimedClaudeSeniorObserveRefused(t *testing.T) {
	g := claimedRun(t, claimedSeniorOnly(t), "observe", "50", nil, "acme/app")
	// The runner reports this refusal by its message alone
	// (checkProviderModels returns ps[0].Message), so the pin's path is not
	// in it -- the claimed ID is, which is what makes the case about the
	// claim. The path is asserted where the problem is built (Task 5's
	// TestClaimedClaudeRouteKeepsEveryOtherRefusal).
	refusedClaimedRun(t, g,
		claimedSenior,
		"observe never refuses a call", "budget.mode enforce")
}

func TestProviderModelApprovedBootstraps(t *testing.T) {
	g := providerGW(t, providerPinned(t), "ACME/App")
	rec, err := g.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestGatewayUnchangedWhenNoCatalogRow (price-catalog plan Task 4): a route
// whose model the built-in catalog does not price leaves the run's table
// exactly as it was before the catalog existed — the embedded table plus the
// owner's overrides — so the run is refused at bootstrap with the same
// message, before any call, rather than priced from a marketplace guess. The
// route is a real one (deepseek/*, which the catalog's rows are keyed by
// model ID, not by pattern), so the refusal is about the missing row and not
// about the route.
func TestGatewayUnchangedWhenNoCatalogRow(t *testing.T) {
	const missing = "deepseek/deepseek-v9-not-in-the-catalog"
	g := providerGW(t, strings.Replace(providerPinned(t), deepseek, missing, 1), "acme/app")
	refusedBeforeAnyCall(t, g, "`"+missing+"` has no price", "model_prices")
}
