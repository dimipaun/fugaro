package projectpolicy

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecodeAbsentIsNotFound(t *testing.T) {
	for _, raw := range []string{"", "  \n", "null", " null "} {
		p, found, err := Decode([]byte(raw))
		if p != nil || found || err != nil {
			t.Errorf("Decode(%q) = (%v, %v, %v), want (nil, false, nil): an absent node is today's behaviour", raw, p, found, err)
		}
	}
}

func TestDecodeIgnoresUnknownKeys(t *testing.T) {
	// A newer Fugaro's policy must not brick this runner on a field it does
	// not know (P8): unknown keys, at the top level and inside budget, are
	// ignored. Version is the one thing that may refuse a node.
	p, found, err := Decode([]byte(`{
		"version": 1,
		"budget": {"mode": "observe", "perRunUsd": 5, "future_key": true},
		"providers": {"openrouter": {"base_url": "https://openrouter.ai/api/v1", "secret": "openrouter-key"}},
		"future": {"anything": [1, 2, 3]}
	}`))
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want a policy", p, found, err)
	}
	if p.Budget.Mode != "observe" {
		t.Errorf("Budget.Mode = %q, want observe", p.Budget.Mode)
	}
	// A key that is present but not documented is ignored, including a
	// camelCase spelling of a documented one; the documented keys that are
	// absent stay zero (the block names at least one documented key).
	if p.Budget.PerRunUSD != 0 {
		t.Errorf("Budget.PerRunUSD = %v: an undocumented key must not decode", p.Budget.PerRunUSD)
	}
	if p.Providers["openrouter"].Secret != "openrouter-key" {
		t.Errorf("Providers = %v, want the openrouter entry", p.Providers)
	}
}

func TestDecodeRefusesBadVersion(t *testing.T) {
	for name, raw := range map[string]string{
		"no version":     `{"budget": {"mode": "observe"}}`,
		"version 0":      `{"version": 0}`,
		"version 2":      `{"version": 2}`,
		"a string":       `{"version": "1"}`,
		"a float":        `{"version": 1.5}`,
		"a bool":         `{"version": true}`,
		"not an object":  `[1, 2]`,
		"a bare number":  `7`,
		"truncated json": `{"version": 1`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil {
			t.Errorf("%s: Decode(%s) = (%v, %v), want an error and no policy", name, raw, p, err)
		}
		if !found {
			t.Errorf("%s: the node exists and cannot be used, so found must be true (fail closed)", name)
		}
	}
	// The version message is the shared constant, so the runner and the CLI
	// print the same sentence.
	_, _, err := Decode([]byte(`{"version": 2}`))
	if err == nil || !strings.Contains(err.Error(), "policy is version 2") {
		t.Errorf("the version refusal must use Skew, got %v", err)
	}
}

// TestDecodeReadsTheDocumentedSnakeCaseKeys is the test that would have caught
// a missing json tag: every documented key, field by field.
func TestDecodeReadsTheDocumentedSnakeCaseKeys(t *testing.T) {
	p, found, err := Decode([]byte(`{
		"version": 1,
		"budget": {"mode": "enforce", "per_run_usd": 12.5, "allowed_models": ["claude-sonnet-5"], "max_run_tokens": 250000},
		"allow_repos": ["github/acme/*", "bitbucket/acme/app"]
	}`))
	if err != nil || !found {
		t.Fatalf("Decode = (%v, %v, %v)", p, found, err)
	}
	if p.Version != 1 {
		t.Errorf("Version = %d, want 1", p.Version)
	}
	if p.Budget.Mode != "enforce" {
		t.Errorf("Mode = %q, want enforce", p.Budget.Mode)
	}
	if p.Budget.PerRunUSD != 12.5 {
		t.Errorf("PerRunUSD = %v, want 12.5", p.Budget.PerRunUSD)
	}
	if len(p.Budget.AllowedModels) != 1 || p.Budget.AllowedModels[0] != "claude-sonnet-5" {
		t.Errorf("AllowedModels = %v", p.Budget.AllowedModels)
	}
	if p.Budget.MaxRunTokens != 250000 {
		t.Errorf("MaxRunTokens = %d, want 250000", p.Budget.MaxRunTokens)
	}
	if len(p.AllowRepos) != 2 || p.AllowRepos[0] != "github/acme/*" {
		t.Errorf("AllowRepos = %v", p.AllowRepos)
	}
	if p.Routing != nil {
		t.Errorf("Routing = %v, want nil (absent is direct, D1b)", p.Routing)
	}
}

// TestDecodeRefusesABudgetWhoseKeysAllMiss: a hand-authored or cross-version
// policy written in camelCase must not decode to a zero per-run cap and a zero
// token ceiling, both of which the runner would enforce.
func TestDecodeRefusesABudgetWhoseKeysAllMiss(t *testing.T) {
	for _, raw := range []string{
		`{"version": 1, "budget": {"perRunUsd": 5}}`,
		`{"version": 1, "budget": {"MaxRunTokens": 1000}}`,
		`{"version": 1, "budget": 5}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err == nil || p != nil || !found {
			t.Errorf("Decode(%s) = (%v, %v, %v), want an error on an existing node", raw, p, found, err)
		}
	}
}

// TestDecodeAcceptsAPartialBudgetAndAnEmptyOne: a budget block that names some
// of its keys leaves the rest at zero, exactly as fugaro.yaml's own budget:
// block does; an empty block is the policy saying it sets no budget.
func TestDecodeAcceptsAPartialBudgetAndAnEmptyOne(t *testing.T) {
	p, _, err := Decode([]byte(`{"version": 1, "budget": {"mode": "observe"}}`))
	if err != nil {
		t.Fatalf("a partial budget must be valid: %v", err)
	}
	if p.Budget.Mode != "observe" || p.Budget.PerRunUSD != 0 || p.Budget.MaxRunTokens != 0 {
		t.Errorf("a partial budget = %+v, want observe with zero ceilings", p.Budget)
	}
	p, _, err = Decode([]byte(`{"version": 1, "budget": {}}`))
	if err != nil {
		t.Fatalf("an empty budget must be valid: %v", err)
	}
	if !reflect.DeepEqual(p.Budget, Budget{}) {
		t.Errorf("an empty budget = %+v, want the zero Budget", p.Budget)
	}
	p, _, err = Decode([]byte(`{"version": 1}`))
	if err != nil {
		t.Fatalf("a policy with no budget block must be valid: %v", err)
	}
	if !reflect.DeepEqual(p.Budget, Budget{}) {
		t.Errorf("no budget block = %+v, want the zero Budget", p.Budget)
	}
}

func TestDecodeRefusesBadAllowRepoPattern(t *testing.T) {
	for _, pattern := range []string{"", "acme/*", "github", "github/acme", "GitHub/acme/app", "github/acme/x y"} {
		if _, _, err := Decode([]byte(`{"version": 1, "allow_repos": ["` + pattern + `"]}`)); err == nil {
			t.Errorf("allow_repos [%q] must be refused", pattern)
		}
	}
	if _, _, err := Decode([]byte(`{"version": 1, "allow_repos": "github/acme/*"}`)); err == nil {
		t.Error("allow_repos that is not a list of strings must be refused")
	}
	// A single "*" is the design's explicit choice: every repository.
	p, _, err := Decode([]byte(`{"version": 1, "allow_repos": ["*"]}`))
	if err != nil || len(p.AllowRepos) != 1 || p.AllowRepos[0] != "*" {
		t.Errorf("allow_repos [\"*\"] must be valid, got (%v, %v)", p, err)
	}
}

func TestDecodeRefusesAProviderWithASecretValue(t *testing.T) {
	// The node is readable by every run token; a provider entry carries a
	// secret NAME. Decode cannot tell a name from a value, but it must at
	// least carry whatever it is given through unchanged, so the CLI (1.6)
	// and the review can see it.
	p, _, err := Decode([]byte(`{"version": 1, "providers": {"openrouter": {"secret": "projects/p/secrets/openrouter-key/versions/3"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Providers["openrouter"].Secret; got != "projects/p/secrets/openrouter-key/versions/3" {
		t.Errorf("Secret = %q, want the name unchanged", got)
	}
}
