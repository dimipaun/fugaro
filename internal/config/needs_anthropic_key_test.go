package config

import "testing"

// TestNeedsAnthropicKey: the model ID alone decides the route, so a pin no
// allowed provider serves (a Claude pin above all) needs the Anthropic key,
// and one every pin's provider may serve does not (design
// docs/design/project-centric.md §6.1).
func TestNeedsAnthropicKey(t *testing.T) {
	open := map[string]ModelProvider{
		"openrouter": {Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"}},
	}
	claudeOnly := Agent{Model: "claude-sonnet-5-5"}
	providerOnly := Agent{Model: "deepseek/deepseek-v4-flash"}
	for _, tc := range []struct {
		name      string
		agent     Agent
		providers map[string]ModelProvider
		repo      string
		want      bool
	}{
		{"a Claude pin", claudeOnly, open, "acme/app", true},
		{"a Claude pin with no providers at all", claudeOnly, nil, "acme/app", true},
		{"every pin served by a provider that may have the code", providerOnly, open, "acme/app", false},
		{"the reviewer still goes direct", Agent{Model: "deepseek/deepseek-v4-flash", Models: ModelRoles{Reviewer: "claude-opus-5-5"}}, open, "acme/app", true},
		{"the background model still goes direct", Agent{Model: "deepseek/deepseek-v4-flash", Models: ModelRoles{Background: "claude-haiku-4-5"}}, open, "acme/app", true},
		{"a repository the provider may not serve", providerOnly, open, "acme/other", true},
		{"a model no provider claims", Agent{Model: "openai/gpt-9"}, open, "acme/app", true},
		{"a provider open to every repository", providerOnly, map[string]ModelProvider{
			"openrouter": {Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
				Secret: "openrouter-api-key", Models: []string{"deepseek/*"}},
		}, "acme/other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsAnthropicKey(tc.agent, tc.providers, tc.repo); got != tc.want {
				t.Errorf("NeedsAnthropicKey = %v, want %v", got, tc.want)
			}
		})
	}
}
