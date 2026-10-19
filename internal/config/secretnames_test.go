package config

import (
	"strings"
	"testing"
)

// TestHarnessOAuthSecretShapes pins the two families' shapes: the bare,
// default credential's name and the whole <name>-prefixed namespace behind
// it, for the harness Fugaro drives (Claude) and the one it reserves ahead of
// (Codex). A name merely shaped like a credential's is one — the namespace is
// reserved whether or not an account of it is configured.
func TestHarnessOAuthSecretShapes(t *testing.T) {
	for name, want := range map[string]struct{ claude, codex bool }{
		"claude-oauth-token":          {claude: true},
		"claude-oauth-token-acme":     {claude: true},
		"claude-oauth-token-":         {claude: true},
		"codex-oauth-token":           {codex: true},
		"codex-oauth-token-acme":      {codex: true},
		"codex-oauth-token-":          {codex: true},
		"codex-oauth":                 {},
		"codex-token":                 {},
		"anthropic-api-key":           {},
		"claudia-oauth-token":         {}, // not the Claude prefix
		"codex-oauth-tokens":          {}, // a longer word, not the namespace
		"my-codex-oauth-token":        {}, // the prefix starts the name
		"claude-oauth-token-acme/bad": {claude: true},
	} {
		if got := ClaudeOAuthSecret(name); got != want.claude {
			t.Errorf("ClaudeOAuthSecret(%q) = %v, want %v", name, got, want.claude)
		}
		if got := CodexOAuthSecret(name); got != want.codex {
			t.Errorf("CodexOAuthSecret(%q) = %v, want %v", name, got, want.codex)
		}
		if got, wantAny := HarnessOAuthSecret(name), want.claude || want.codex; got != wantAny {
			t.Errorf("HarnessOAuthSecret(%q) = %v, want %v", name, got, wantAny)
		}
	}
}

// TestClaudeAccountSecretIsTheReservedName holds ClaudeAccountSecret and the
// reserved name together: the default account's derived secret IS the
// ReservedSecrets key the platform mounts, so a change to one that left the
// other behind would mount nothing.
func TestClaudeAccountSecretIsTheReservedName(t *testing.T) {
	if got := ClaudeAccountSecret(DefaultClaudeAccount); got != ClaudeOAuthToken {
		t.Fatalf("ClaudeAccountSecret(default) = %q, want %q", got, ClaudeOAuthToken)
	}
	if _, ok := ReservedSecrets[ClaudeAccountSecret(DefaultClaudeAccount)]; !ok {
		t.Fatalf("the default account's secret %q is not in ReservedSecrets", ClaudeOAuthToken)
	}
	// Codex's name is reserved, and deliberately not a mount: ReservedSecrets
	// is the names the platform mounts, and nothing mounts a Codex credential
	// yet (config.go's comment on the map).
	if _, ok := ReservedSecrets[CodexOAuthToken]; ok {
		t.Fatalf("%q is in ReservedSecrets: nothing mounts a Codex credential yet", CodexOAuthToken)
	}
}

// TestProviderSecretConventionIsTheEntryName is the convention the owner
// ruled for on 2026-10-18: a provider's key is named after its entry in the
// local config, which is how docs/multi-model.md §2 spells its examples.
func TestProviderSecretConventionIsTheEntryName(t *testing.T) {
	for name, want := range map[string]string{
		"openrouter": "openrouter-api-key",
		"deepseek":   "deepseek-api-key",
		"anthropic":  "anthropic-api-key",
		"a":          "a-api-key",
	} {
		if got := ProviderSecretConvention(name); got != want {
			t.Errorf("ProviderSecretConvention(%q) = %q, want %q", name, got, want)
		}
	}
	// The one spelling that already existed is this convention's, so the
	// Anthropic key needs no rename to be on it.
	if AnthropicSecret != ProviderSecretConvention("anthropic") {
		t.Fatalf("the reserved Anthropic key %q is not the convention's %q", AnthropicSecret, ProviderSecretConvention("anthropic"))
	}
}

// TestReservedProviderSecret: the conventional name belongs to the provider
// entry it names, whatever that provider's .secret is written as — which is
// what makes a workflow's value stored under it a capture waiting for a
// rename. It is not reserved from the provider itself, and a name that is
// nobody's convention is nobody's reservation.
func TestReservedProviderSecret(t *testing.T) {
	providers := map[string]ModelProvider{
		"openrouter": {Secret: "openrouter-api-key"},
		"deepseek":   {Secret: "ds-key"}, // off convention: its name is still reserved for it
	}
	for _, tc := range []struct {
		secret, want string
		ok           bool
	}{
		{"openrouter-api-key", "openrouter", true},
		{"deepseek-api-key", "deepseek", true},
		{"ds-key", "", false},
		{"openrouter-key", "", false},
		{"openrouter-api-keys", "", false},
		{"", "", false},
	} {
		name, ok := ReservedProviderSecret(providers, tc.secret)
		if name != tc.want || ok != tc.ok {
			t.Errorf("ReservedProviderSecret(%q) = (%q, %v), want (%q, %v)", tc.secret, name, ok, tc.want, tc.ok)
		}
	}
	if name, ok := ReservedProviderSecret(nil, "openrouter-api-key"); ok || name != "" {
		t.Fatalf("with no providers configured nothing is reserved: (%q, %v)", name, ok)
	}
}

// TestSecretNameOffConvention: the warning is per provider, names the entry's
// own name and the name the convention gives it, and says both halves that
// make it a warning rather than a refusal — the key keeps working, and the
// conventional name is reserved for that provider either way.
func TestSecretNameOffConvention(t *testing.T) {
	p := ModelProvider{Secret: "my-openrouter-key"}
	msg := SecretNameOffConvention("openrouter", p)
	for _, want := range []string{"my-openrouter-key", "openrouter-api-key", "nothing is renamed", "reserved"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the warning does not say %q: %s", want, msg)
		}
	}
	if got := SecretNameOffConvention("openrouter", ModelProvider{Secret: ProviderSecretConvention("openrouter")}); got != "" {
		t.Fatalf("a key on convention is warned about: %q", got)
	}
}

// TestProviderSecretWarnings: one warning per off-convention provider, in
// name order, each at the field the owner edits, and none for a provider
// whose key is named the way the convention names it.
func TestProviderSecretWarnings(t *testing.T) {
	ps := ProviderSecretWarnings(map[string]ModelProvider{
		"openrouter": {Secret: "openrouter-api-key"}, // on convention
		"zed":        {Secret: "zed-key"},
		"alpha":      {Secret: "alpha-key"},
	})
	got := problemsText(ps)
	if strings.Contains(got, "providers.openrouter") {
		t.Errorf("an on-convention provider is warned about:\n%s", got)
	}
	if len(ps) != 2 || ps[0].Path != "providers.alpha.secret" || ps[1].Path != "providers.zed.secret" {
		t.Fatalf("warnings = %s, want alpha then zed at .secret", got)
	}
	if ps := ProviderSecretWarnings(map[string]ModelProvider{"openrouter": {Secret: "openrouter-api-key"}}); len(ps) != 0 {
		t.Fatalf("warnings = %s, want none", problemsText(ps))
	}
}

// TestValidateModelProvidersRefusesHarnessOAuthSecretNames: a provider's key
// may not be stored under a harness's OAuth credential name, bare or in its
// namespace. The Codex half is reserved ahead of the harness (owner ruling of
// 2026-10-18); the Claude half is the same hole on the side that is live — a
// provider claiming claude-oauth-token-acme would mount, into every repository
// it allows, a secret `fugaro secrets set` refuses to write for anything but a
// configured, allowed account.
func TestValidateModelProvidersRefusesHarnessOAuthSecretNames(t *testing.T) {
	for _, secret := range []string{
		"claude-oauth-token-acme", "codex-oauth-token", "codex-oauth-token-acme",
	} {
		p := okProvider()
		p.Secret = secret
		ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p})
		if !hasProblem(ps, "providers.openrouter.secret", "harness's OAuth credential name", 0) {
			t.Errorf("secret %q: problems = %s", secret, problemsText(ps))
		}
	}
	// The bare claude-oauth-token is ReservedSecrets' own key, and the map's
	// refusal (which fires first) is the one that speaks for it.
	{
		p := okProvider()
		p.Secret = ClaudeOAuthToken
		if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p}); !hasProblem(ps, "providers.openrouter.secret", "Fugaro already uses", 0) {
			t.Errorf("the bare claude-oauth-token: problems = %s", problemsText(ps))
		}
	}
	// The exhaustive reserved-name half is the map's, not this one's, and its
	// message stays its own.
	p := okProvider()
	p.Secret = "anthropic-api-key"
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p}); !hasProblem(ps, "providers.openrouter.secret", "Fugaro already uses", 0) {
		t.Errorf("a ReservedSecrets name: problems = %s", problemsText(ps))
	}
}

// TestValidateModelProvidersRefusesAnotherProvidersConvention: <p>-api-key
// belongs to p's key whether or not p stores its key there, so a *second*
// provider claiming it is refused — the same collision as two providers
// sharing one secret, with the capture delayed to the day p's key is renamed
// to the convention. p itself is not refused its own name, and a name that is
// nobody's convention is an ordinary secret name.
func TestValidateModelProvidersRefusesAnotherProvidersConvention(t *testing.T) {
	deepseek := okProvider()
	deepseek.Secret = "ds-key"
	deepseek.Models = []string{"qwen/*"}
	openrouter := okProvider() // secret openrouter-api-key, the convention's

	// openrouter's .secret is NOT the convention for deepseek; deepseek's is
	// off-convention, so this is not the duplicate rule firing.
	thief := okProvider()
	thief.Secret = "deepseek-api-key"
	thief.Models = []string{"moonshot/*"}
	ps := ValidateModelProviders(map[string]ModelProvider{
		"openrouter": {Secret: "other-key"}, "deepseek": deepseek, "thief": thief,
	})
	if !hasProblem(ps, "providers.thief.secret", "conventional name of provider deepseek's key", 0) {
		t.Fatalf("problems = %s", problemsText(ps))
	}

	// On convention, and off convention, both accepted for their own entry.
	for _, p := range []ModelProvider{openrouter, deepseek} {
		if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p}); len(ps) != 0 {
			t.Errorf("secret %q: problems = %s", p.Secret, problemsText(ps))
		}
	}
	// A name no entry's convention gives it is an ordinary secret name: the
	// convention warns (ProviderSecretWarnings), it never refuses.
	p := okProvider()
	p.Secret = "some-other-name"
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p}); len(ps) != 0 {
		t.Errorf("an off-convention name was refused: %s", problemsText(ps))
	}
}

// TestValidateModelProvidersConventionDoesNotShadowTheDuplicateRule: two
// providers sharing one secret is refused by the sharper message, even when
// that secret is also somebody's conventional name.
func TestValidateModelProvidersConventionDoesNotShadowTheDuplicateRule(t *testing.T) {
	alpha := okProvider()
	alpha.Secret = "alpha-api-key"
	alpha.Models = []string{"deepseek/*"}
	zed := okProvider()
	zed.Secret = "alpha-api-key"
	zed.Models = []string{"qwen/*"}
	ps := ValidateModelProviders(map[string]ModelProvider{"alpha": alpha, "zed": zed})
	if !hasProblem(ps, "providers.zed.secret", "already the secret of provider alpha", 0) {
		t.Fatalf("problems = %s", problemsText(ps))
	}
}

// TestConventionNeverReservesAPlatformName: a provider entry may be named so
// that its conventional name is one the platform already owns — the entry
// `anthropic` gives anthropic-api-key, the direct Anthropic key, and an entry
// named after a harness family gives a name in that family. Such a name is
// never the provider's (ValidateModelProviders refuses it as its .secret), so
// it is not reserved for the provider either: reserving it would refuse the
// platform's own key to `secrets set` and init. And the warning stays quiet
// for that entry, since the name it would advise is one the provider may not
// use.
func TestConventionNeverReservesAPlatformName(t *testing.T) {
	providers := map[string]ModelProvider{
		"anthropic":          {Secret: "anthropic-proxy-key"},
		"codex-oauth-token":  {Secret: "cx-key"},
		"claude-oauth-token": {Secret: "cl-key"},
	}
	for _, secret := range []string{AnthropicSecret, "codex-oauth-token-api-key", "claude-oauth-token-api-key"} {
		if name, ok := ReservedProviderSecret(providers, secret); ok {
			t.Errorf("ReservedProviderSecret(%q) = (%q, true): a platform name is reserved for a provider", secret, name)
		}
	}
	for name, p := range providers {
		if msg := SecretNameOffConvention(name, p); msg != "" {
			t.Errorf("provider %s is advised to use a name it may not use: %s", name, msg)
		}
	}
}

// TestCheckWorkflowProviderSecrets: a workflow secret named after a
// configured provider's key is refused, with the same wording family the
// sibling reservations in Validate use and at the same path. The name belongs
// to that provider's key whatever its .secret is written as today
// (ReservedProviderSecret), so a value a workflow stored under it would be
// read as the provider's credential the day the key is renamed to the
// convention — and `fugaro secrets set` refuses to store one, so the job would
// mount a name that can have no version.
func TestCheckWorkflowProviderSecrets(t *testing.T) {
	providers := map[string]ModelProvider{
		"openrouter": {Secret: "openrouter-api-key"},
		"deepseek":   {Secret: "ds-key"}, // off convention: its name is still reserved for it
	}
	cfg := &Config{Workflows: map[string]Workflow{
		"web": {Secrets: []Secret{
			{Name: "openrouter-api-key", Env: "OPENROUTER_KEY"},
			{Name: "deepseek-api-key", Env: "DEEPSEEK_KEY"},
			{Name: "my-own-secret", Env: "MINE"},
		}},
	}}
	ps := CheckWorkflowProviderSecrets(cfg, providers)
	if len(ps) != 2 {
		t.Fatalf("problems = %s", problemsText(ps))
	}
	if !hasProblem(ps, "workflows.web.secrets[0].name", "reserved for model provider openrouter's key", 0) {
		t.Errorf("problems = %s", problemsText(ps))
	}
	if !hasProblem(ps, "workflows.web.secrets[1].name", "reserved for model provider deepseek's key", 0) {
		t.Errorf("problems = %s", problemsText(ps))
	}
	// The refusal names the provider, the conventional name and the way out,
	// and never a value.
	msg := ps[0].Message
	for _, want := range []string{"openrouter", "openrouter-api-key"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name %q: %s", want, msg)
		}
	}

	// A name no configured provider's convention gives it is an ordinary
	// workflow secret, and with no providers at all nothing is reserved.
	own := &Config{Workflows: map[string]Workflow{"web": {Secrets: []Secret{{Name: "openrouter-api-key", Env: "K"}}}}}
	if ps := CheckWorkflowProviderSecrets(own, nil); len(ps) != 0 {
		t.Errorf("with no providers configured nothing is reserved: %s", problemsText(ps))
	}
	// The platform's own name is not a provider's reservation
	// (conventionUsable): an entry named anthropic does not reserve
	// anthropic-api-key from a workflow, whose refusal is Validate's own
	// (ReservedSecrets) and not this rule's.
	anthropic := map[string]ModelProvider{"anthropic": {Secret: "anthropic-proxy-key"}}
	if ps := CheckWorkflowProviderSecrets(own, anthropic); len(ps) != 0 {
		t.Errorf("a platform name was reserved for a provider: %s", problemsText(ps))
	}
}
