package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readCorpus(t *testing.T, kind, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "config", kind, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestBudgetBlockValid(t *testing.T) {
	cfg, ps := Parse(readCorpus(t, "valid", "budget-policy.yaml"))
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	b := cfg.Budget
	if b == nil || b.Mode != "enforce" || b.PerRunUSD != 2.5 || len(b.AllowedModels) != 3 {
		t.Fatalf("budget = %+v", b)
	}
	// enforce without a cap is valid here: the owner's ceiling may supply it.
	y := minimalYAML + "agent:\n  auth: api-key\nbudget:\n  mode: enforce\n"
	if _, ps := Parse([]byte(y)); len(ps) > 0 {
		t.Errorf("enforce without per_run_usd: %v", ps)
	}
}

func TestBudgetBlockInvalid(t *testing.T) {
	for name, want := range map[string]string{
		"budget-mode.yaml":             "budget.mode",
		"budget-per-run-negative.yaml": "budget.per_run_usd",
		"budget-per-day.yaml":          "budget.per_day_usd",
		"budget-allowed-empty.yaml":    "budget.allowed_models",
		"budget-allowed-alias.yaml":    "budget.allowed_models[0]",
		"budget-prices.yaml":           "model_prices",
	} {
		cfg, ps := Parse(readCorpus(t, "invalid", name))
		if cfg != nil || len(ps) == 0 {
			t.Errorf("%s: parsed, want invalid", name)
			continue
		}
		if !strings.Contains(ps[0].String(), want) {
			t.Errorf("%s: problem %q does not mention %q", name, ps[0], want)
		}
	}
}

func TestPerDayRefused(t *testing.T) {
	_, ps := Parse(readCorpus(t, "invalid", "budget-per-day.yaml"))
	if len(ps) != 1 || ps[0].Path != "budget.per_day_usd" || !strings.Contains(ps[0].Message, "not supported yet (M9b)") {
		t.Fatalf("problems = %v", ps)
	}
}

func TestModelPricesNotAllowedInFugaroYaml(t *testing.T) {
	_, ps := Parse(readCorpus(t, "invalid", "budget-prices.yaml"))
	if len(ps) == 0 || !strings.Contains(ps[0].Message, "model_prices") {
		t.Fatalf("problems = %v", ps)
	}
}

func TestPolicyOfIgnoresOtherKeys(t *testing.T) {
	p, err := PolicyOf([]byte(`
version: 7
mystery: [1, 2]
git: nonsense
agent:
  auth: not-an-auth
  review_rounds: 99
  max_run_tokens: 1000
  max_output_tokens: { coder: 64000 }
budget:
  mode: observe
  per_run_usd: 3
  allowed_models: [claude-sonnet-5-5]
workflows: 12
`))
	if err != nil {
		t.Fatal(err)
	}
	want := Policy{Mode: "observe", PerRunUSD: 3, MaxRunTokens: 1000, MaxOutputTokens: RoleTokens{Coder: 64000}, AllowedModels: []string{"claude-sonnet-5-5"}}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("policy = %+v, want %+v", p, want)
	}
	for _, empty := range []string{"", "version: 1\nproject: x\n", "budget:\n"} {
		if p, err := PolicyOf([]byte(empty)); err != nil || !reflect.DeepEqual(p, Policy{}) {
			t.Errorf("PolicyOf(%q) = %+v, %v; want zero", empty, p, err)
		}
	}
}

func TestPolicyOfInvalidIsError(t *testing.T) {
	for name, y := range map[string]string{
		"mode":         "budget: { mode: strict }",
		"negative cap": "budget: { per_run_usd: -1 }",
		"per day":      "budget: { per_day_usd: 4 }",
		"empty list":   "budget: { allowed_models: [] }",
		"alias":        "budget: { allowed_models: [opus] }",
		"not a list":   "budget: { allowed_models: claude-opus-5-5 }",
		"cap a string": "budget: { per_run_usd: lots }",
		"neg tokens":   "agent: { max_run_tokens: -1 }",
		"output limit": "agent: { max_output_tokens: { coder: 999999 } }",
		"not yaml":     "budget: [",
	} {
		if _, err := PolicyOf([]byte(y)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// The policy blocks are decoded strictly: a misspelt key would otherwise be
// "no policy" and drop the team's limit silently.
func TestPolicyOfStrictInsideBlocks(t *testing.T) {
	for name, c := range map[string]struct{ y, key string }{
		"budget typo":        {"budget: { per_run_usdd: 2 }", "per_run_usdd"},
		"allowed typo":       {"budget: { allowed_model: [claude-sonnet-5-5] }", "allowed_model"},
		"model_prices":       {"budget: { model_prices: {} }", "model_prices"},
		"run tokens typo":    {"agent: { max_run_token: 5 }", "max_run_token"},
		"output typo":        {"agent: { max_output_token: { coder: 5 } }", "max_output_token"},
		"output role typo":   {"agent: { max_output_tokens: { coder2: 5 } }", "coder2"},
		"budget not mapping": {"budget: 5", "budget"},
		"agent not mapping":  {"agent: [1]", "agent"},
		"duplicate budget":   {"budget: { mode: off }\nbudget: { mode: off }", "budget"},
	} {
		_, err := PolicyOf([]byte(c.y))
		if err == nil || !strings.Contains(err.Error(), c.key) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, c.key)
		}
	}
	// Other agent keys, and unknown keys elsewhere, stay lenient.
	if _, err := PolicyOf([]byte("surprise: 1\nagent: { auth: x, review_rounds: 99, max_run_tokens: 5 }\nbudget: { mode: off }")); err != nil {
		t.Errorf("lenient parts: %v", err)
	}
}
