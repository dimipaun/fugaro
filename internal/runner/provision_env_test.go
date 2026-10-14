package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
)

// TestProvisionEnv (review M5, and this round's high finding): mise install
// runs the repository's own code — a backend such as `cargo:` or `go:` runs
// the tool's install script, a `[hooks]`/`[env]` template runs whatever the
// config says — and on a follow-up the config is the pull request branch's,
// written by an agent. So its environment is the run's with everything
// Fugaro's own or secret removed: the model credentials, the gateway's
// provider keys, and the static git-provider credentials (the App private
// key and Bitbucket token the derived GITHUB_TOKEN is minted from). The one
// thing added is GITHUB_TOKEN (mise's own GitHub API calls use it for the
// rate limit), and it is added only when the caller passes one: an empty
// token leaves the variable out entirely rather than shadowing whatever the
// job's own environment carries with an empty value.
func TestProvisionEnv(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=model-secret",
		"CLAUDE_CODE_OAUTH_TOKEN=oauth-secret",
		"ANTHROPIC_AUTH_TOKEN=auth-secret",
		"FUGARO_PROVIDER_KEY_OPENROUTER=provider-secret",
		"GITHUB_TOKEN=planted-secret",
		"GH_TOKEN=planted-gh-secret",
		// The static git-provider credentials, the ones the run's own git
		// calls and its GITHUB_TOKEN are minted from: a mise config on a
		// follow-up's branch could otherwise read them and mint an
		// installation token for every repository the App covers.
		providers.EnvGitHubAppKey + "=app-private-key-secret",
		providers.EnvGitHubAppKeyFile + "=/secrets/app.pem",
		providers.EnvGitHubAppID + "=1234567",
		providers.EnvBitbucketToken + "=bitbucket-secret",
		providers.EnvGitHubAPIURL + "=https://api.github.com",
		"FUGARO_GIT_TOKEN=derived-git-secret",
		"FUGARO_CHECKOUT=clone",
		// What a mise backend does need, and must keep.
		"MISE_DATA_DIR=/home/fugaro/.local/share/mise",
		"HOME=/home/fugaro",
	}
	got := provisionEnv(env, "run-token")
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		if slices.Contains(gitops.ModelCredentialVars, k) {
			t.Errorf("mise would see the model credential %s=%q", k, v)
		}
		if strings.HasPrefix(k, "FUGARO_") {
			t.Errorf("mise would see Fugaro's own %s=%q", k, v)
		}
		if k == "GITHUB_TOKEN" && v != "run-token" {
			t.Errorf("GITHUB_TOKEN = %q, want the caller's token only", v)
		}
	}
	// Everything the job needs for an install survives: PATH, the toolchain
	// home, and MISE_*.
	want := []string{
		"PATH=/usr/bin",
		"MISE_DATA_DIR=/home/fugaro/.local/share/mise",
		"HOME=/home/fugaro",
		"GITHUB_TOKEN=run-token",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("provisionEnv = %v, want %v", got, want)
	}
	// No token: the variable is gone, not empty.
	noToken := []string{"PATH=/usr/bin", "MISE_DATA_DIR=/home/fugaro/.local/share/mise", "HOME=/home/fugaro"}
	got = provisionEnv(env, "")
	if slices.Contains(got, "GITHUB_TOKEN=") {
		t.Fatalf("provisionEnv with no token = %v, want no GITHUB_TOKEN at all", got)
	}
	if !slices.Equal(got, noToken) {
		t.Fatalf("provisionEnv with no token = %v, want %v", got, noToken)
	}
}
