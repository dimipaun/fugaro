package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

const deepseek = "deepseek/deepseek-v4-flash"

// providerGW is a run whose coder is the provider's model, on repository
// acme/app, with the owner's provider allowing the repos in allow.
func providerGW(t *testing.T, cfg string, allow ...string) *gw {
	t.Helper()
	g := newGW(t, cfg, "observe", "")
	g.deps.Providers = map[string]config.ModelProvider{"openrouter": {
		Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: allow,
	}}
	return g
}

func providerPinned(t *testing.T) string {
	return strings.Replace(gwConfig(t, ""), "coder: "+sonnet, "coder: "+deepseek, 1)
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
		refusedBeforeAnyCall(t, providerGW(t, cfg, "acme/app"), "vertex", "api-key")
	})
	t.Run("the repository's own allowed_models names an unapproved provider model", func(t *testing.T) {
		cfg := gwConfig(t, "") + "budget:\n  allowed_models: [" + sonnet + ", " + haiku + ", " + deepseek + "]\n"
		refusedBeforeAnyCall(t, providerGW(t, cfg, "acme/other"), "budget.allowed_models[2]", "allow_data_to")
	})
	t.Run("the owner's allow-list does not carry it", func(t *testing.T) {
		g := providerGW(t, providerPinned(t), "acme/app")
		g.deps.Spend = ceilingOf(t, runner.AllowedModelsEnv, sonnet+","+haiku)
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
