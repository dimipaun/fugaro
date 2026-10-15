package runner_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

const deepseek = "deepseek/deepseek-v4-flash"

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
	g := newGW(t, cfg, mode, capUSD)
	compat, up := anthropicfake.New(t, script...)
	g.compat = compat
	providers := map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: up.URL, Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: allow,
	}}
	g.deps.Providers = providers
	// The job's environment carries the same routes (init --repo writes
	// FUGARO_MODEL_PROVIDERS from the owner's local config) and the run's
	// price table is built from them (P5): without a route no model is
	// claimed, so the built-in catalog has no row for it. newGW built the
	// spend before the routes existed, so rebuild it with them.
	raw, err := json.Marshal(providers)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{runner.BudgetModeEnv: mode, runner.MaxRunUSDEnv: capUSD, runner.ModelProvidersEnv: string(raw)}
	spend, err := runner.SpendFromEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	// That table, read from the shipped catalog a day after its own
	// checked_at rather than at the wall clock: the catalog's 30 days
	// (catalog.StaleAfter) must not decide from today's date whether a test
	// prices a route at the cheapest end of its tier or the dearest one.
	// Anything a test wants to say about the clock it says by assigning
	// g.deps.Spend.Prices itself (TestResultJSONCatalogStale).
	if spend.Prices != nil {
		spend.Prices = freshCatalogTable(t, providers, nil)
	}
	g.deps.Spend = spend
	withProviderKey(g.harness)
	return g
}

// providerPinned has the provider's model as the coder. Its first-line
// review is off, which `auto` would turn on (providerFirstLine): the tests
// that use it script one review.
func providerPinned(t *testing.T) string {
	return strings.Replace(gwConfig(t, "  first_line_review: off\n"), "coder: "+sonnet, "coder: "+deepseek, 1)
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
		// The ceiling the job carries names the routes too (init --repo
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
