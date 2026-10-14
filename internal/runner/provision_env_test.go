package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitops"
)

// TestProvisionEnv (review M5): mise install runs the repository's own code —
// a backend such as `cargo:` or `go:` runs the tool's install script, and a
// `[hooks]`/`[env]` template runs whatever the config says — so its
// environment is the run's with the two things it must never see removed:
// the model credentials, and the gateway's provider keys. The one thing
// added is GITHUB_TOKEN (mise's own GitHub API calls use it for the rate
// limit), and it is added only when the caller passes one: an empty token
// leaves the variable out entirely rather than shadowing whatever the job's
// own environment carries with an empty value.
func TestProvisionEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=model-secret",
		"CLAUDE_CODE_OAUTH_TOKEN=oauth-secret",
		"ANTHROPIC_AUTH_TOKEN=auth-secret",
		"FUGARO_PROVIDER_KEY_OPENROUTER=provider-secret",
		"GITHUB_TOKEN=planted-secret",
		"HOME=/home/fugaro",
	}
	got := provisionEnv(env, "run-token")
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		if slices.Contains(gitops.ModelCredentialVars, k) {
			t.Errorf("mise would see the model credential %s=%q", k, v)
		}
		if strings.HasPrefix(k, "FUGARO_PROVIDER_KEY_") {
			t.Errorf("mise would see the gateway's provider key %s", k)
		}
		if k == "GITHUB_TOKEN" && v != "run-token" {
			t.Errorf("GITHUB_TOKEN = %q, want the caller's token only", v)
		}
	}
	want := []string{"PATH=/usr/bin", "HOME=/home/fugaro", "GITHUB_TOKEN=run-token"}
	if !slices.Equal(got, want) {
		t.Fatalf("provisionEnv = %v, want %v", got, want)
	}
	// No token: the variable is gone, not empty.
	got = provisionEnv(env, "")
	if slices.Contains(got, "GITHUB_TOKEN=") {
		t.Fatalf("provisionEnv with no token = %v, want no GITHUB_TOKEN at all", got)
	}
	if !slices.Equal(got, []string{"PATH=/usr/bin", "HOME=/home/fugaro"}) {
		t.Fatalf("provisionEnv with no token = %v", got)
	}
}
