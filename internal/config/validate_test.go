package config

import (
	"strings"
	"testing"
)

// TestAllowedModelClaimNeedsARoute pins the one consequence of the claim-blind
// pricing half that a reader is most likely to get wrong (D1): a
// budget.allowed_models entry of `anthropic/claude-sonnet-5.5` now passes
// config-time validation -- CheckModelID judges the spelling, and under P4 the
// spelling is legal -- and is refused **only** by CheckProviderPolicy, which
// walks the allow-list beside the pins. Nothing is unguarded (the allow-list
// was never the gate for who may route a model), but a writer must not assume
// a bad entry fails at parse time.
func TestAllowedModelClaimNeedsARoute(t *testing.T) {
	if msg := CheckModelID(claimID); msg != "" {
		t.Errorf("CheckModelID(%q) = %q, want accepted: the spelling is legal for a claim", claimID, msg)
	}
	const doc = `
version: 1
project: aurora
git: { provider: github }
agent:
  auth: api-key
  models: { coder: deepseek/deepseek-v4-flash, reviewer: claude-sonnet-5-5 }
budget:
  mode: enforce
  per_run_usd: 2
  allowed_models: ["deepseek/deepseek-v4-flash", "claude-sonnet-5-5", "anthropic/claude-sonnet-5.5"]
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	cfg, problems := Parse([]byte(doc))
	if len(problems) > 0 {
		t.Fatalf("the file was refused at parse time, so this test proves nothing: %v", problems)
	}
	if got := problemsText(validateBudget(cfg.Budget)); got != "" {
		t.Fatalf("validateBudget refused the entry: %q", got)
	}
	// No route names the ID: the entry is refused by the provider rule, at
	// its own index, with the same message the pin gets.
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	got := problemsText(CheckProviderPolicy(cfg.Agent, cfg.Budget.AllowedModels, "dimipaun/fugaro", ps, claimPrices(t, ps)))
	if !strings.Contains(got, "budget.allowed_models[2]") || !strings.Contains(got, "no provider serves it") || !strings.Contains(got, "is a Claude model") {
		t.Errorf("an unclaimed allow-list entry was not refused: %q", got)
	}
	// The route names exactly that ID: the same file has nothing left to
	// refuse.
	p := okProvider()
	p.Models = []string{"deepseek/*", claimID}
	ps = map[string]ModelProvider{"openrouter": p}
	if got := problemsText(CheckProviderPolicy(cfg.Agent, cfg.Budget.AllowedModels, "dimipaun/fugaro", ps, claimPrices(t, ps))); got != "" {
		t.Errorf("a claimed allow-list entry was refused: %q", got)
	}
}
