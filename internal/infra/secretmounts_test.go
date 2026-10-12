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
