package config

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
)

func okProvider() ModelProvider {
	return ModelProvider{
		Kind: ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer",
		Secret: "openrouter-api-key", RouteFeePct: 5.5, Models: []string{"deepseek/*"},
		AllowDataTo: []string{"edgeappinc/fugarosandbox", "dimipaun/fugaro"},
	}
}

func problemsText(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.Path + ": " + p.Message + "\n")
	}
	return b.String()
}

func TestProviderValid(t *testing.T) {
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": okProvider()}); len(ps) != 0 {
		t.Fatalf("problems: %s", problemsText(ps))
	}
}

func TestProviderOverlapRejected(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"deepseek/*", "deepseek/deepseek-v4-flash"},
		{"deepseek/*", "deepseek/v4*"},
		{"qwen/qwen3", "qwen/qwen3"},
	} {
		a, b := okProvider(), okProvider()
		a.Models, b.Models = []string{tc.a}, []string{tc.b}
		b.Secret = "other-key"
		ps := ValidateModelProviders(map[string]ModelProvider{"one": a, "two": b})
		if !strings.Contains(problemsText(ps), "overlaps") {
			t.Errorf("%q vs %q: not rejected: %q", tc.a, tc.b, problemsText(ps))
		}
	}
	a, b := okProvider(), okProvider()
	b.Models, b.Secret = []string{"qwen/*"}, "other-key"
	if ps := ValidateModelProviders(map[string]ModelProvider{"one": a, "two": b}); len(ps) != 0 {
		t.Errorf("disjoint patterns rejected: %s", problemsText(ps))
	}
}

func TestProviderHTTPRefusedExceptLoopback(t *testing.T) {
	for url, ok := range map[string]bool{
		"https://openrouter.ai/api": true, "http://127.0.0.1:8123": true, "http://localhost:9": true, "http://[::1]:9": true,
		"http://openrouter.ai/api": false, "http://10.0.0.1": false, "ftp://x.test": false, "openrouter.ai": false,
		"https://user:pw@openrouter.ai": false, "https://openrouter.ai/?k=v": false,
	} {
		p := okProvider()
		p.BaseURL = url
		got := len(ValidateModelProviders(map[string]ModelProvider{"p": p})) == 0
		if got != ok {
			t.Errorf("base_url %q accepted = %v, want %v", url, got, ok)
		}
	}
}

func TestFeeBounds(t *testing.T) {
	for fee, ok := range map[float64]bool{0: true, 5.5: true, MaxRouteFeePct: true, -0.1: false, MaxRouteFeePct + 1: false} {
		p := okProvider()
		p.RouteFeePct = fee
		if got := len(ValidateModelProviders(map[string]ModelProvider{"p": p})) == 0; got != ok {
			t.Errorf("fee %v accepted = %v, want %v", fee, got, ok)
		}
	}
}

func TestProviderFieldRules(t *testing.T) {
	for name, mut := range map[string]func(*ModelProvider){
		"kind":          func(p *ModelProvider) { p.Kind = "openai" },
		"auth":          func(p *ModelProvider) { p.Auth = "basic" },
		"secret value":  func(p *ModelProvider) { p.Secret = "sk-or-v1-ABC DEF" },
		"reserved":      func(p *ModelProvider) { p.Secret = "anthropic-api-key" },
		"no models":     func(p *ModelProvider) { p.Models = nil },
		"claude":        func(p *ModelProvider) { p.Models = []string{"claude-*"} },
		"claude prefix": func(p *ModelProvider) { p.Models = []string{"c*"} },
		"wildcard":      func(p *ModelProvider) { p.Models = []string{"*"} },
		"bad repo":      func(p *ModelProvider) { p.AllowDataTo = []string{"fugaro"} },
	} {
		p := okProvider()
		mut(&p)
		if ps := ValidateModelProviders(map[string]ModelProvider{"p": p}); len(ps) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	a, b := okProvider(), okProvider()
	b.Models = []string{"qwen/*"}
	if ps := ValidateModelProviders(map[string]ModelProvider{"one": a, "two": b}); !strings.Contains(problemsText(ps), "own key") {
		t.Errorf("shared secret not rejected: %q", problemsText(ps))
	}
}

func TestProviderFor(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	if n, p, ok := ProviderFor(ps, "deepseek/deepseek-v4-flash"); !ok || n != "openrouter" || !p.AllowsData("dimipaun/fugaro") || p.AllowsData("edgeappinc/edgeweb") {
		t.Errorf("ProviderFor = %q %v", n, ok)
	}
	if _, _, ok := ProviderFor(ps, "claude-sonnet-5-5"); ok {
		t.Error("a Claude model was claimed")
	}
}

func TestOAuthWithProviderModelRefused(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	a := Agent{Auth: "oauth", Models: ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: "claude-opus-5-5", Background: "claude-haiku-4-5"}}
	got := problemsText(CheckProviderAuth(a, ps))
	if !strings.Contains(got, "agent.models.coder") || !strings.Contains(got, "oauth") || !strings.Contains(got, "api-key") || strings.Contains(got, "reviewer") {
		t.Errorf("problems = %q", got)
	}
}

func TestVertexWithProviderModelRefused(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	a := Agent{Auth: "vertex", Model: "deepseek/deepseek-v4-flash"}
	got := problemsText(CheckProviderAuth(a, ps))
	if !strings.Contains(got, "vertex") || !strings.Contains(got, "agent.models.coder") {
		t.Errorf("problems = %q", got)
	}
}

func TestAPIKeyWithProviderModelAllowed(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	a := Agent{Auth: "api-key", Models: ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: "claude-opus-5-5"}}
	if got := CheckProviderAuth(a, ps); len(got) != 0 {
		t.Errorf("problems = %v", got)
	}
	if got := CheckProviderAuth(Agent{Auth: "oauth", Model: "claude-sonnet-5-5"}, ps); len(got) != 0 {
		t.Errorf("an all-Claude oauth run refused: %v", got)
	}
}

func TestProviderKeyEnv(t *testing.T) {
	p := okProvider()
	p.Secret = "openrouter-api-key"
	if got := p.SecretEnv(); got != "FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY" || !IsProviderKeyEnv(got) || !IsProviderKeyEnv("fugaro_provider_key_x") {
		t.Errorf("SecretEnv = %q", got)
	}
	// A workflow secret can never be mounted as a provider key's variable.
	if !ReservedEnv(p.SecretEnv()) {
		t.Errorf("%s is not a reserved variable", p.SecretEnv())
	}
	if IsProviderKeyEnv("ANTHROPIC_API_KEY") {
		t.Error("ANTHROPIC_API_KEY is not a provider key variable")
	}
	if n, ok := ProviderBySecret(map[string]ModelProvider{"openrouter": p}, "openrouter-api-key"); !ok || n != "openrouter" {
		t.Errorf("ProviderBySecret = %q %v", n, ok)
	}
	if _, ok := ProviderBySecret(map[string]ModelProvider{"openrouter": p}, "npm-token"); ok {
		t.Error("npm-token is a provider secret")
	}
}

func TestAllowsDataIgnoresCase(t *testing.T) {
	p := ModelProvider{AllowDataTo: []string{"EdgeAppInc/FugaroSandbox"}}
	for repo, want := range map[string]bool{
		"EdgeAppInc/FugaroSandbox": true, "edgeappinc/fugarosandbox": true,
		"edgeappinc/edgeweb": false, "": false,
	} {
		if got := p.AllowsData(repo); got != want {
			t.Errorf("AllowsData(%q) = %v", repo, got)
		}
	}
}

func providerPrices(t *testing.T) *pricing.Table {
	t.Helper()
	return pricing.Embedded()
}

// A repository's fugaro.yaml cannot carry a provider, a base URL, a key or a
// price: the strict decode refuses the keys, so the owner's local config is
// the only place a provider exists.
func TestRepoCannotAddProvider(t *testing.T) {
	for _, doc := range []string{
		"providers:\n  evil:\n    kind: anthropic-compat\n    base_url: https://evil.example\n",
		"agent:\n  base_url: https://evil.example\n",
		"agent:\n  provider: {base_url: https://evil.example}\n",
		"model_prices:\n  deepseek/deepseek-v4-flash: {input: 0}\n",
		"budget:\n  providers: {}\n",
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("fugaro.yaml accepted:\n%s", doc)
		}
	}
}

func TestAllowDataToRequired(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	a := Agent{Auth: "api-key", Models: ModelRoles{Coder: "deepseek/deepseek-v4-flash", Reviewer: "claude-opus-5-5", Background: "claude-haiku-4-5"}}
	prices := providerPrices(t)
	if got := CheckProviderPolicy(a, nil, "dimipaun/fugaro", ps, prices); len(got) != 0 {
		t.Errorf("an allowed repository refused: %v", got)
	}
	if got := CheckProviderPolicy(a, nil, "DimiPaun/Fugaro", ps, prices); len(got) != 0 {
		t.Errorf("repository match is case-sensitive: %v", got)
	}
	got := problemsText(CheckProviderPolicy(a, nil, "edgeappinc/edgeweb", ps, prices))
	if !strings.Contains(got, "agent.models.coder") || !strings.Contains(got, "allow_data_to") || !strings.Contains(got, "edgeappinc/edgeweb") {
		t.Errorf("problems = %q", got)
	}
	// Nothing lists the repository when the provider has no allow_data_to.
	p := okProvider()
	p.AllowDataTo = nil
	if got := CheckProviderPolicy(a, nil, "dimipaun/fugaro", map[string]ModelProvider{"openrouter": p}, prices); len(got) != 1 {
		t.Errorf("empty allow_data_to: %v", got)
	}
}

func TestProviderPolicyUnclaimedAndUnpriced(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	prices := providerPrices(t)
	for _, tc := range []struct {
		model, want string
	}{
		{"openai/gpt-9", "no provider"},
		{"deepseek/deepseek-v4-flash:free", "variant"},
		{"deepseek/unpriced-model", "no price"},
	} {
		a := Agent{Auth: "api-key", Model: tc.model}
		got := problemsText(CheckProviderPolicy(a, nil, "dimipaun/fugaro", ps, prices))
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: problems = %q, want %q", tc.model, got, tc.want)
		}
	}
	// Claude IDs and aliases are not this rule's business.
	if got := CheckProviderPolicy(Agent{Model: "sonnet"}, nil, "x/y", ps, prices); len(got) != 0 {
		t.Errorf("alias refused: %v", got)
	}
}

func TestProviderPolicyAllowedModelsEntries(t *testing.T) {
	ps := map[string]ModelProvider{"openrouter": okProvider()}
	a := Agent{Auth: "api-key", Model: "claude-sonnet-5-5"}
	got := problemsText(CheckProviderPolicy(a, []string{"claude-sonnet-5-5", "deepseek/deepseek-v4-flash"}, "edgeappinc/edgeweb", ps, providerPrices(t)))
	if !strings.Contains(got, "budget.allowed_models[1]") || !strings.Contains(got, "allow_data_to") {
		t.Errorf("problems = %q", got)
	}
}
