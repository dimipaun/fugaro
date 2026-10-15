package config

import (
	"strings"
	"testing"
)

// claimProviders is the owner's side of a layer claim: one route that
// serves a non-Claude model, and one that serves another.
func claimProviders() map[string]ModelProvider {
	return map[string]ModelProvider{
		"openrouter": {Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"}},
		"other": {Kind: ModelProviderKind, BaseURL: "https://other.example/api", Auth: "bearer",
			Secret: "other-api-key", Models: []string{"qwen/*"}},
	}
}

// Review Focus 3: the layer key refuses every spelling a claim may not take,
// with the same rule a route entry is judged by (CheckClaudeClaim).
func TestLayerClaimMustBeExplicit(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  string
		ok   bool
	}{
		{"one explicit ID", `["anthropic/claude-sonnet-5.5"]`, true},
		{"a wildcard", `["anthropic/*"]`, false},
		{"a claude pattern", `["anthropic/claude-*"]`, false},
		{"a bare claude spelling", `["claude-sonnet-5-5"]`, false},
		{"a variant", `["anthropic/claude-sonnet-5.5:free"]`, false},
		{"a 1m spelling", `["anthropic/claude-sonnet-5.5[1m]"]`, false},
		{"another provider's model", `["deepseek/deepseek-v4-flash"]`, false},
		{"an empty list", `[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := "version: 1\nproject: acme\ngcp_project: acme-fugaro\nclaude_models:\n  openrouter: " + tc.ids + "\n"
			_, ps := ParseProjectLayer([]byte(text), testAnchor)
			if tc.ok && len(ps) > 0 {
				t.Fatalf("problems = %v", ps)
			}
			if !tc.ok && len(ps) == 0 {
				t.Fatal("accepted, want refused")
			}
		})
	}
}

// The key is top-level, and a repository cannot write it: Resolve decodes
// the merged tree into Config with KnownFields, so the only place
// claude_models can be written is the layer file.
//
// The repository file below is otherwise VALID — the assertion on the bare
// base is what says so, so the refusal is the added key's and not something
// else in the file. Mutation that makes this fail: add a
// `ClaudeModels map[string][]string \`yaml:"claude_models"\“ field to
// Config (the key becomes one Config knows, and a repository's fugaro.yaml
// carrying it resolves clean).
func TestLayerClaimIsATopLevelKey(t *testing.T) {
	under := "version: 1\nproject: acme\ngcp_project: acme-fugaro\ndefaults:\n  claude_models:\n    openrouter: [\"anthropic/claude-sonnet-5.5\"]\n"
	if _, ps := ParseProjectLayer([]byte(under), testAnchor); len(ps) == 0 {
		t.Fatal("a claude_models under defaults: was accepted")
	} else if !strings.Contains(ps[0].Message, "is not a fugaro.yaml key the project layer knows") {
		t.Fatalf("problems = %v", ps)
	}
	const base = "version: 1\nproject: acme\ngcp_project: acme-fugaro\ngit: { provider: github }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	if _, _, ps := Resolve([]byte(base), nil); len(ps) > 0 {
		t.Fatalf("the base file is not valid, so the refusal below says nothing about the key: %v", ps)
	}
	_, _, ps := Resolve([]byte(base+"claude_models:\n  openrouter: [\"anthropic/claude-sonnet-5.5\"]\n"), nil)
	if len(ps) == 0 {
		t.Fatal("a repository's fugaro.yaml with claude_models: was accepted")
	}
	if !strings.Contains(ps[0].Message, "claude_models") {
		t.Fatalf("the refusal does not name the key: %v", ps)
	}
}

func TestApplyClaudeClaims(t *testing.T) {
	// P8: the named route gains the ID, everything else is untouched.
	got, ps := ApplyClaudeClaims(claimProviders(), map[string][]string{"openrouter": {"anthropic/claude-sonnet-5.5"}})
	if len(ps) > 0 {
		t.Fatalf("problems = %v", ps)
	}
	if !slicesEqual(got["openrouter"].Models, []string{"deepseek/*", "anthropic/claude-sonnet-5.5"}) {
		t.Fatalf("models = %v", got["openrouter"].Models)
	}
	if !slicesEqual(got["other"].Models, []string{"qwen/*"}) {
		t.Fatalf("other = %v", got["other"].Models)
	}
	// The input is a copy's source, never appended to.
	if !slicesEqual(claimProviders()["openrouter"].Models, []string{"deepseek/*"}) {
		t.Fatal("the input's models were appended to")
	}
	if got["openrouter"].BaseURL != "https://openrouter.ai/api" || got["openrouter"].Secret != "openrouter-api-key" {
		t.Fatal("the claim changed the route itself")
	}
	// A claim the route already names is not duplicated.
	again, ps := ApplyClaudeClaims(got, map[string][]string{"openrouter": {"anthropic/claude-sonnet-5.5"}})
	if len(ps) > 0 || !slicesEqual(again["openrouter"].Models, []string{"deepseek/*", "anthropic/claude-sonnet-5.5"}) {
		t.Fatalf("again = %v, problems = %v", again["openrouter"].Models, ps)
	}
}

// P8: the layer may only add to a route the owner defined.
func TestApplyClaudeClaimsRefusesAnUnknownProvider(t *testing.T) {
	_, ps := ApplyClaudeClaims(claimProviders(), map[string][]string{"openrouter": {"anthropic/claude-sonnet-5.5"}, "nosuch": {"anthropic/claude-opus-5.5"}})
	if len(ps) != 1 || ps[0].Path != "claude_models.nosuch" || !strings.Contains(ps[0].Message, "does not define") {
		t.Fatalf("problems = %v", ps)
	}
}

// The overlap rule runs on the merged result: a claim another provider's
// pattern already matches is a config-time problem, not a gateway that
// refuses to start.
func TestLayerClaimOverlapRefused(t *testing.T) {
	providers := map[string]ModelProvider{
		"openrouter": {Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"deepseek/*"}},
		"anthropic-thief": {Kind: ModelProviderKind, BaseURL: "https://thief.example/api", Auth: "bearer",
			Secret: "thief-api-key", Models: []string{"anthropic/*"}},
	}
	_, ps := ApplyClaudeClaims(providers, map[string][]string{"openrouter": {"anthropic/claude-sonnet-5.5"}})
	if len(ps) == 0 || !strings.Contains(ps[0].Message, "overlaps") {
		t.Fatalf("problems = %v", ps)
	}
	// A claim that names no provider at all is refused before the merge.
	if _, ps := ApplyClaudeClaims(nil, map[string][]string{"openrouter": {"anthropic/claude-sonnet-5.5"}}); len(ps) == 0 {
		t.Fatal("a claim with no providers at all was accepted")
	}
}

// ClaimsOnRoutes is what a run merges with: a claim naming a provider the
// run's own table does not carry is a route this repository does not have,
// dropped silently — the run cannot tell it from a name nothing defines, and
// the surfaces that hold the installation config's whole table report it
// (ApplyClaudeClaims) instead.
func TestClaimsOnRoutes(t *testing.T) {
	claim := []string{"anthropic/claude-sonnet-5.5"}
	providers := claimProviders()
	got := ClaimsOnRoutes(providers, map[string][]string{"openrouter": claim, "elsewhere": {"anthropic/claude-opus-5.5"}})
	if len(got) != 1 || !slicesEqual(got["openrouter"], claim) {
		t.Fatalf("got = %v", got)
	}
	if ClaimsOnRoutes(providers, nil) != nil {
		t.Fatal("no claims must stay nil")
	}
	none := map[string][]string{"nosuch": claim}
	if len(ClaimsOnRoutes(providers, none)) != 0 {
		t.Fatal("a claim for no route at all was kept")
	}
	// And the merge it feeds then changes nothing, with no problem at all.
	merged, ps := ApplyClaudeClaims(providers, ClaimsOnRoutes(providers, none))
	if len(ps) != 0 || !slicesEqual(merged["openrouter"].Models, []string{"deepseek/*"}) ||
		!slicesEqual(merged["other"].Models, []string{"qwen/*"}) {
		t.Fatalf("merged = %v, problems = %v", merged, ps)
	}
}

// No claims: providers come back exactly as they were, so every path
// without a layer is unchanged.
func TestApplyClaudeClaimsWithoutClaims(t *testing.T) {
	providers := claimProviders()
	got, ps := ApplyClaudeClaims(providers, nil)
	if len(ps) > 0 {
		t.Fatalf("problems = %v", ps)
	}
	if len(got) != len(providers) || !slicesEqual(got["openrouter"].Models, providers["openrouter"].Models) {
		t.Fatalf("got = %v", got)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
