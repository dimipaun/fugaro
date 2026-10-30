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

// TestValidateGitAuthorClosedVocabulary pins git.author's vocabulary (A2a):
// the key is the repository's opt-in for the launcher's own identity as the
// commit author (D604, D606), so a value outside it is a launcher's bug
// rather than a run's opinion, exactly as git.provider's two values are.
// The empty value is legal and is the key absent: it is today's behaviour,
// the run's own identity authoring its commits.
//
// The refusal names both values, because that sentence is what an author
// reads to fix the file — the same reason git.provider's does.
func TestValidateGitAuthorClosedVocabulary(t *testing.T) {
	const base = `
version: 1
project: aurora
git: { provider: github }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`
	cfg, ps := Parse([]byte(strings.Replace(base, "git: { provider: github }", "git: { provider: github, author: launcher }", 1)))
	if len(ps) > 0 {
		t.Fatalf("git.author: launcher was refused: %v", ps)
	}
	if cfg.Git.Author != GitAuthorLauncher {
		t.Fatalf("git.author = %q, want %q", cfg.Git.Author, GitAuthorLauncher)
	}
	for _, author := range []string{"", GitAuthorFugaro, GitAuthorLauncher} {
		cfg.Git.Author = author
		if got := problemsText(Validate(cfg)); got != "" {
			t.Errorf("git.author = %q was refused: %q", author, got)
		}
	}
	// A near miss — the value with the wrong case — is refused at its own
	// path, naming both values it could have been.
	cfg.Git.Author = "Fugaro"
	got := problemsText(Validate(cfg))
	if !strings.Contains(got, "git.author") {
		t.Errorf("an unknown git.author was not refused at its own path: %q", got)
	}
	for _, want := range []string{GitAuthorFugaro, GitAuthorLauncher} {
		if !strings.Contains(got, want) {
			t.Errorf("the git.author refusal does not name %q: %q", want, got)
		}
	}
}
