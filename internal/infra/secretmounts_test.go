package infra

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// SecretMounts is what a job mounts: the git credential always, the one
// Claude credential agent.auth names (none for vertex), the allowed
// providers' keys only with api-key, and the workflow's own secrets.
func TestSecretMounts(t *testing.T) {
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"openrouter": {Secret: "openrouter-api-key", AllowDataTo: []string{"acme/app"}},
		"elsewhere":  {Secret: "elsewhere-key", AllowDataTo: []string{"acme/other"}},
	}}
	w := config.Workflow{Secrets: []config.Secret{{Name: "npm-token", Env: "NPM_TOKEN"}}}
	names := func(auth string) []string {
		ms, err := SecretMounts("bitbucket-token", &config.Config{Agent: config.Agent{Auth: auth}}, "app", config.DefaultClaudeAccount, w, lc, "acme/app")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range ms {
			out = append(out, m.Logical)
		}
		return out
	}
	for auth, want := range map[string][]string{
		"vertex":  {"bitbucket-token", "npm-token"},
		"oauth":   {"bitbucket-token", "claude-oauth-token", "npm-token"},
		"api-key": {"bitbucket-token", "anthropic-api-key", "openrouter-api-key", "npm-token"},
	} {
		if got := names(auth); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", auth, got, want)
		}
	}
	// A named account's token is mounted under its own derived secret, not
	// the bare default: config.ClaudeAccountSecret is the one place that
	// decides the name, so this is the same behavior as calling it directly.
	ms, err := SecretMounts("bitbucket-token", &config.Config{Agent: config.Agent{Auth: "oauth"}}, "app", "acme-personal", w, lc, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	var claude string
	for _, m := range ms {
		if m.Env == config.ReservedSecrets["claude-oauth-token"] {
			claude = m.Logical
		}
	}
	if claude != "claude-oauth-token-acme-personal" {
		t.Errorf("named account acme-personal mounts logical secret %q, want claude-oauth-token-acme-personal", claude)
	}
	// Each mount carries the variable its secret becomes.
	ms, err = SecretMounts("github-app-key", &config.Config{Agent: config.Agent{Auth: "api-key"}}, "app", config.DefaultClaudeAccount, w, lc, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	wantEnv := []SecretMount{
		{"github-app-key", "FUGARO_GITHUB_APP_PRIVATE_KEY"},
		{"anthropic-api-key", "ANTHROPIC_API_KEY"},
		{"openrouter-api-key", lc.Providers["openrouter"].SecretEnv()},
		{"npm-token", "NPM_TOKEN"},
	}
	if !slices.Equal(ms, wantEnv) {
		t.Errorf("mounts %v, want %v", ms, wantEnv)
	}
	// A workflow may not declare a provider's key.
	bad := config.Workflow{Secrets: []config.Secret{{Name: "openrouter-api-key", Env: "X"}}}
	if _, err := SecretMounts("bitbucket-token", &config.Config{}, "app", config.DefaultClaudeAccount, bad, lc, "acme/app"); err == nil {
		t.Error("a workflow secret naming a provider's key was accepted")
	}
}

// GitSecret is the git credential's logical name per provider, the one source
// the spec and the secrets stage share.
func TestGitSecret(t *testing.T) {
	for provider, want := range map[string]string{"github": "github-app-key", "bitbucket": "bitbucket-token"} {
		if got, ok := GitSecret(provider); !ok || got != want {
			t.Errorf("GitSecret(%q) = %q, %v", provider, got, ok)
		}
	}
	if got, ok := GitSecret("gitlab"); ok || got != "" {
		t.Errorf("GitSecret(gitlab) = %q, %v", got, ok)
	}
}

// TestSecretMountsNoListProviderAvailableToAnyRepo proves the automatic
// consequence of Task 1's AllowsData change: spec.go's per-provider mount
// condition is unedited, and a provider with no allow_data_to at all is
// now eligible for every repository (design §3a).
func TestSecretMountsNoListProviderAvailableToAnyRepo(t *testing.T) {
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"openrouter": {Secret: "openrouter-api-key"}, // no AllowDataTo at all
	}}
	w := config.Workflow{}
	ms, err := SecretMounts("bitbucket-token", &config.Config{Agent: config.Agent{Auth: "api-key"}}, "app", config.DefaultClaudeAccount, w, lc, "acme/never-named")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range ms {
		if m.Logical == "openrouter-api-key" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mounts = %+v, want openrouter-api-key present for a repo the no-list provider never named (design §3a)", ms)
	}
}

// TestSecretMountsListedProviderStillRefusesUnlistedRepo is the paired
// mutation-proof case: P7 must not weaken a real, non-empty, non-wildcard
// list.
func TestSecretMountsListedProviderStillRefusesUnlistedRepo(t *testing.T) {
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"openrouter": {Secret: "openrouter-api-key", AllowDataTo: []string{"acme/app"}},
	}}
	w := config.Workflow{}
	ms, err := SecretMounts("bitbucket-token", &config.Config{Agent: config.Agent{Auth: "api-key"}}, "app", config.DefaultClaudeAccount, w, lc, "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Logical == "openrouter-api-key" {
			t.Fatalf("mounts = %+v, want no openrouter-api-key for a repo an explicit, non-empty list excludes (P7 must not weaken this case)", ms)
		}
	}
}

// TestSecretMountsRequiredSecretsIsTheMountSet: RequiredSecrets (what `fugaro
// secrets ls` checks for MISSING) is exactly the union of what the jobs of
// the repository's workflows mount, per provider and per auth, less the
// optional mounts (OptionalSecret) a job runs without, and never a
// name no job of it mounts: the other provider's git credential, a model
// credential another auth names, or the bare default token in place of a
// named account's.
func TestSecretMountsRequiredSecretsIsTheMountSet(t *testing.T) {
	lc := &localcfg.Config{
		Providers: map[string]config.ModelProvider{
			"openrouter": {Secret: "openrouter-api-key", AllowDataTo: []string{"acme/app"}},
			"elsewhere":  {Secret: "elsewhere-key", AllowDataTo: []string{"acme/other"}},
		},
		ClaudeAccounts: map[string]config.ClaudeAccount{
			"work":  {AllowDataTo: []string{"acme/app"}},
			"other": {AllowDataTo: []string{"acme/other"}},
		},
	}
	cfg := func(provider, auth string) *config.Config {
		return &config.Config{
			Git:   config.Git{Provider: provider},
			Agent: config.Agent{Auth: auth},
			Workflows: map[string]config.Workflow{
				"web": {Secrets: []config.Secret{{Name: "npm-token", Env: "NPM_TOKEN"}}},
				"api": {Secrets: []config.Secret{{Name: "pypi-token", Env: "PYPI_TOKEN"}}},
			},
		}
	}
	for _, c := range []struct {
		provider, auth string
		want           []string
	}{
		{"github", "vertex", []string{"github-app-key", "npm-token", "pypi-token"}},
		{"bitbucket", "vertex", []string{"bitbucket-token", "npm-token", "pypi-token"}},
		{"github", "oauth", []string{"claude-oauth-token", "claude-oauth-token-work", "github-app-key", "npm-token", "pypi-token"}},
		// anthropic-api-key is mounted under api-key but optional (OptionalSecret:
		// the job mounts it only when it has a version), so it is not required.
		{"github", "api-key", []string{"github-app-key", "npm-token", "openrouter-api-key", "pypi-token"}},
	} {
		got, err := RequiredSecrets(cfg(c.provider, c.auth), lc, "acme/app")
		if err != nil {
			t.Fatalf("%s/%s: %v", c.provider, c.auth, err)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s/%s: %v, want %v", c.provider, c.auth, got, c.want)
		}
		// The same set the deployed jobs mount, variant by variant, less the
		// mounts a job may go without.
		var mounted []string
		for _, name := range []string{"api", "web"} {
			for _, tok := range WorkflowAccounts(cfg(c.provider, c.auth), lc, "acme/app") {
				git, _ := GitSecret(c.provider)
				ms, err := SecretMounts(git, cfg(c.provider, c.auth), name, tok, cfg(c.provider, c.auth).Workflows[name], lc, "acme/app")
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range ms {
					if !slices.Contains(mounted, m.Logical) && !OptionalSecret(m.Logical) {
						mounted = append(mounted, m.Logical)
					}
				}
			}
		}
		slices.Sort(mounted)
		if !slices.Equal(got, mounted) {
			t.Errorf("%s/%s: required %v, mounted %v", c.provider, c.auth, got, mounted)
		}
	}
	// A provider that is neither is refused, not guessed at.
	if _, err := RequiredSecrets(cfg("gitlab", "vertex"), lc, "acme/app"); err == nil {
		t.Error("an unknown git provider gave a required set")
	}
}
