// Package providers opens the real git provider a repository's fugaro.yaml
// names, with credentials from the runner's environment, which the platform
// injects from Secret Manager (design §6). See docs/git-providers.md.
package providers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
)

// Environment variables the runner reads its provider credentials from.
// None of them ever reaches the agent: it gets a derived token instead
// (FUGARO_GIT_TOKEN, and GH_TOKEN on GitHub).
const (
	EnvBitbucketToken   = "FUGARO_BITBUCKET_TOKEN"             // repository access token
	EnvBitbucketAPIURL  = "FUGARO_BITBUCKET_API_URL"           // optional API root override
	EnvGitHubAppID      = "FUGARO_GITHUB_APP_ID"               // GitHub App ID (or client ID)
	EnvGitHubAppKey     = "FUGARO_GITHUB_APP_PRIVATE_KEY"      // the App's private key, PEM
	EnvGitHubAppKeyFile = "FUGARO_GITHUB_APP_PRIVATE_KEY_FILE" // or a file holding it
	EnvGitHubAPIURL     = "FUGARO_GITHUB_API_URL"              // optional API root override
)

// minSecretLen matches the agent package's redaction floor: a shorter
// value cannot be redacted without mangling ordinary text.
const minSecretLen = 4

// FromEnv returns an Opener that opens providers with credentials from env
// (KEY=VALUE pairs, usually os.Environ()). hc is the HTTP client adapters
// use; nil means their default.
//
// FromEnv must be called once per run: it closes over env and hc but keeps
// no other state itself, so calling the returned Opener more than once for
// the same repo builds a fresh *bitbucket.Provider or *github.Provider each
// time. The runner (Task 9) must call it exactly once per run and reuse the
// single Provider it returns, both so that a Bitbucket adapter's labels
// warning (bitbucket.Options.Warn) fires at most once, and so that a
// GitHub adapter's installation-token cache (tokenSource) is actually
// shared across the run instead of re-minting on every use.
//
// The []string FromEnv's Opener returns holds only the static secret it
// read from env: the Bitbucket token, or the GitHub App's private key PEM.
// It does not include installation tokens GitHub mints later (ghs_...),
// because those do not exist yet when the Opener runs. The runner must
// additionally redact the token from every Provider.GitAuth call: each
// call can mint a fresh token (GitAuth refreshes when the cached one would
// expire within minValid), and every value it ever returns must be added
// to the redaction list, not just the first.
func FromEnv(env []string, hc *http.Client) gitprov.Opener {
	vars := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	return func(_ context.Context, kind, repo string) (gitprov.Provider, []string, error) {
		owner, name, ok := gitprov.SplitRepo(repo)
		if !ok {
			return nil, nil, fmt.Errorf("repository %q must look like owner/name", repo)
		}
		switch kind {
		case gitprov.KindBitbucket:
			token, err := secret(vars, EnvBitbucketToken, "a Bitbucket repository access token")
			if err != nil {
				return nil, nil, err
			}
			p, err := bitbucket.New(bitbucket.Options{Workspace: owner, Slug: name, Token: token, BaseURL: vars[EnvBitbucketAPIURL], HTTP: hc})
			return p, []string{token}, err
		case gitprov.KindGitHub:
			appID := vars[EnvGitHubAppID]
			if appID == "" {
				return nil, nil, fmt.Errorf("%s is not set: the github provider needs the GitHub App's ID (see docs/git-providers.md)", EnvGitHubAppID)
			}
			pemData, err := appKey(vars)
			if err != nil {
				return nil, nil, err
			}
			key, err := github.ParsePrivateKey([]byte(pemData))
			if err != nil {
				return nil, nil, err
			}
			p, err := github.New(github.Options{Owner: owner, Repo: name, AppID: appID, PrivateKey: key, BaseURL: vars[EnvGitHubAPIURL], HTTP: hc})
			return p, []string{pemData}, err
		default:
			return nil, nil, fmt.Errorf("unknown git provider %q (want %s or %s)", kind, gitprov.KindGitHub, gitprov.KindBitbucket)
		}
	}
}

func secret(vars map[string]string, name, what string) (string, error) {
	v := vars[name]
	if v == "" {
		return "", fmt.Errorf("%s is not set: the provider needs %s (see docs/git-providers.md)", name, what)
	}
	if len(v) < minSecretLen {
		return "", fmt.Errorf("%s is shorter than %d bytes, so it cannot be redacted safely", name, minSecretLen)
	}
	return v, nil
}

func appKey(vars map[string]string) (string, error) {
	if v := vars[EnvGitHubAppKey]; v != "" {
		return v, nil
	}
	if path := vars[EnvGitHubAppKeyFile]; path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", EnvGitHubAppKeyFile, err)
		}
		return string(data), nil
	}
	return "", fmt.Errorf("neither %s nor %s is set: the github provider needs the GitHub App's private key (see docs/git-providers.md)", EnvGitHubAppKey, EnvGitHubAppKeyFile)
}
