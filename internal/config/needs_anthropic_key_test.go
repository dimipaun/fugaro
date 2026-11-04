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

// TestNeedsAnthropicKeyReadsALevelModel: a review level's own model is a pin
// of its own (agent.levels.lN.model, config.ResolveLevels L3.1), and it is the
// model its stage runs in place of the role slot it sits in (stageModel,
// L4.5). The three role models alone therefore under-report the run: with
// every pin a provider may serve and level 3 on a Claude model, the run does
// call Anthropic at L3, and this rule has to ask for the key (L4.5b).
func TestNeedsAnthropicKeyReadsALevelModel(t *testing.T) {
	open := map[string]ModelProvider{
		"openrouter": {Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"}},
	}
	pins := Agent{Model: "deepseek/deepseek-v4-flash"}
	for _, tc := range []struct {
		name   string
		levels ReviewLevels
		want   bool
	}{
		{"no level names a model", ReviewLevels{}, false},
		{"l1 runs a Claude model", ReviewLevels{L1: &LevelSetting{Model: "claude-haiku-4-5"}}, true},
		{"l2 runs a Claude model", ReviewLevels{L2: &LevelSetting{Model: "claude-sonnet-5-5"}}, true},
		{"l3 runs a Claude model", ReviewLevels{L3: &LevelSetting{Model: "claude-opus-5-5"}}, true},
		{"l3 runs a Claude model beside an effort on it", ReviewLevels{L3: &LevelSetting{Model: "claude-opus-5-5", Effort: "xhigh"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ag := pins
			ag.Levels = tc.levels
			if got := NeedsAnthropicKey(ag, open, "acme/app"); got != tc.want {
				t.Errorf("NeedsAnthropicKey = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNeedsAnthropicKeyLevelModelServedByAProvider is the other half, and it
// is why the rule reads the level's model rather than counting every level:
// only a model no allowed provider claims goes direct. A level model the
// route serving this repository takes needs no key, and the same model on a
// route that may not see this repository does (design
// docs/design/project-centric.md §6.1).
func TestNeedsAnthropicKeyLevelModelServedByAProvider(t *testing.T) {
	open := map[string]ModelProvider{
		"openrouter": {Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"}},
	}
	for _, tc := range []struct {
		name   string
		levels ReviewLevels
		repo   string
		want   bool
	}{
		{"l3 on a model the route serves", ReviewLevels{L3: &LevelSetting{Model: "deepseek/deepseek-v4-pro"}}, "acme/app", false},
		{"l2 on a model the route serves", ReviewLevels{L2: &LevelSetting{Model: "deepseek/deepseek-v4-pro"}}, "acme/app", false},
		{"l1 on a model the route serves", ReviewLevels{L1: &LevelSetting{Model: "deepseek/deepseek-v4-pro"}}, "acme/app", false},
		{"l3 on a model the route may not serve this repository", ReviewLevels{L3: &LevelSetting{Model: "deepseek/deepseek-v4-pro"}}, "acme/other", true},
		{"l3 on a model no provider claims", ReviewLevels{L3: &LevelSetting{Model: "openai/gpt-9"}}, "acme/app", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ag := Agent{Model: "deepseek/deepseek-v4-flash", Levels: tc.levels}
			if got := NeedsAnthropicKey(ag, open, tc.repo); got != tc.want {
				t.Errorf("NeedsAnthropicKey(repo %q) = %v, want %v", tc.repo, got, tc.want)
			}
		})
	}
}

// TestAnthropicKeyNeeded: the mode decides whether the gateway exists at all
// (only enforce builds one), so without it every call goes direct to Anthropic
// however the pins read, and with it the pins decide -- NeedsAnthropicKey's
// rule. Only api-key runs ever need the key: oauth has its own token and
// Vertex the job's account. Both init and validate ask this one function, so
// they cannot disagree about when the owner is asked for the key.
func TestAnthropicKeyNeeded(t *testing.T) {
	open := map[string]ModelProvider{
		"openrouter": {Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
			Secret: "openrouter-api-key", Models: []string{"deepseek/*"}, AllowDataTo: []string{"acme/app"}},
	}
	claudeAPI := Agent{Auth: "api-key", Model: "claude-sonnet-5-5"}
	providerAPI := Agent{Auth: "api-key", Model: "deepseek/deepseek-v4-flash"}
	for _, tc := range []struct {
		name      string
		agent     Agent
		providers map[string]ModelProvider
		repo      string
		mode      string
		want      bool
	}{
		{"a Claude pin, enforcing: the pin goes direct", claudeAPI, open, "acme/app", "enforce", true},
		{"every pin served by a provider, enforcing", providerAPI, open, "acme/app", "enforce", false},
		{"every pin served by a provider, observing: no gateway, so all direct", providerAPI, open, "acme/app", "observe", true},
		{"every pin served by a provider, off: no gateway either", providerAPI, open, "acme/app", "off", true},
		{"every pin served by a provider, no mode at all", providerAPI, open, "acme/app", "", true},
		{"a Claude pin, oauth, observing", Agent{Auth: "oauth", Model: "claude-sonnet-5-5"}, nil, "acme/app", "observe", false},
		{"a Claude pin, oauth, enforcing", Agent{Auth: "oauth", Model: "claude-sonnet-5-5"}, nil, "acme/app", "enforce", false},
		{"Vertex, observing: the job's own account", Agent{Auth: "vertex", Model: "claude-sonnet-5-5"}, nil, "acme/app", "observe", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AnthropicKeyNeeded(tc.agent, tc.providers, tc.repo, tc.mode); got != tc.want {
				t.Errorf("AnthropicKeyNeeded(mode %q) = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}
