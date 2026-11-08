// Package providers opens the real git provider a repository's fugaro.yaml
// names, with credentials from the runner's environment, which the platform
// injects from Secret Manager (design §6). See docs/git-providers.md.
package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	EnvBitbucketToken   = "FUGARO_BITBUCKET_TOKEN"             // repository, workspace or project access token
	EnvBitbucketAPIURL  = "FUGARO_BITBUCKET_API_URL"           // optional API root override
	EnvGitHubAppID      = "FUGARO_GITHUB_APP_ID"               // GitHub App ID (or client ID)
	EnvGitHubAppKey     = "FUGARO_GITHUB_APP_PRIVATE_KEY"      // the App's private key, PEM
	EnvGitHubAppKeyFile = "FUGARO_GITHUB_APP_PRIVATE_KEY_FILE" // or a file holding it
	EnvGitHubAPIURL     = "FUGARO_GITHUB_API_URL"              // optional API root override
	// EnvCredentialsServiceURL is where a job learns the URL of its
	// installation's credentials service (C1.15, D146/D147). It is set on the
	// job only when the installation has the service on, so its presence is
	// the "the service is on" fact: when it is set, the App's private key is
	// the service's and the github provider is opened keyless, its tokens
	// coming from the service (C2.2, D145) -- see FromEnv.
	EnvCredentialsServiceURL = "FUGARO_CREDENTIALS_SERVICE_URL"
)

// minSecretLen matches the agent package's redaction floor: a shorter
// value cannot be redacted without mangling ordinary text.
const minSecretLen = 4

// ErrNoCredential identifies the failure of an Opener whose environment holds
// no credential for the provider it was asked for: `FUGARO_BITBUCKET_TOKEN is
// not set` is the one such failure today. A caller that has another place to
// look for the value -- the CLI's catalog build reads the project's
// bitbucket-token secret -- recognises it with errors.Is rather than by
// reading the message, which stays the sentence it has always been.
var ErrNoCredential = errors.New("the provider's credential is not in the environment")

// notSetError is that failure. Its message is what secret has always returned;
// only the match is new, through ErrNoCredential, so a caller that says where
// else the value lives appends to the text instead of rewriting it.
type notSetError struct{ name, what string }

func (e *notSetError) Error() string {
	return fmt.Sprintf("%s is not set: the provider needs %s (see docs/git-providers.md)", e.name, e.what)
}

// Is matches ErrNoCredential, so errors.Is finds it through every %w the
// opener's failure is wrapped in on its way to a caller.
func (e *notSetError) Is(target error) bool { return target == ErrNoCredential }

// FromEnv returns an Opener that opens providers with credentials from env
// (KEY=VALUE pairs, usually os.Environ()). hc is the HTTP client adapters
// use; nil means their default. warn receives adapter warnings meant for
// the run's log, such as the Bitbucket adapter's one-time "labels aren't
// supported" (bitbucket.Options.Warn) or the GitHub adapter's one-time
// "could not read the App's identity" (github.Options.Warn); nil drops them.
//
// Each call of the returned Opener builds a fresh Provider, and a
// Provider's state lives for as long as it does: the Bitbucket labels
// warning fires once per Provider, and the GitHub installation-token cache
// is per Provider. A run must therefore open its provider once and reuse
// it, as the runner does.
//
// The []string the Opener returns holds only the static secret read from
// env: the Bitbucket token, or the GitHub App's private key PEM. It cannot
// hold GitHub installation tokens (ghs_…), which are minted later, on each
// Provider.GitAuth call that finds the cached one too close to expiry. A
// caller must redact every GitAuth.Token (and GitAuth.Env value) it is
// ever handed, not just the first.
//
// With the credentials service on (EnvCredentialsServiceURL set) the github
// provider is opened without an App key and no static secret is returned for
// it: the key is the service's, and the installation tokens it mints are
// still the caller's to redact (C2.2, D145).
func FromEnv(env []string, hc *http.Client, warn func(string)) gitprov.Opener {
	vars := envVars(env)
	return func(_ context.Context, kind, repo string) (gitprov.Provider, []string, error) {
		owner, name, ok := gitprov.SplitRepo(repo)
		if !ok {
			return nil, nil, fmt.Errorf("repository %q must look like owner/name", repo)
		}
		switch kind {
		case gitprov.KindBitbucket:
			token, err := secret(vars, EnvBitbucketToken, "a Bitbucket access token")
			if err != nil {
				return nil, nil, err
			}
			p, err := bitbucket.New(bitbucket.Options{Workspace: owner, Slug: name, Token: token, BaseURL: vars[EnvBitbucketAPIURL], HTTP: hc, Warn: warn})
			return p, []string{token}, err
		case gitprov.KindGitHub:
			appID := vars[EnvGitHubAppID]
			if appID == "" {
				return nil, nil, fmt.Errorf("%s is not set: the github provider needs the GitHub App's ID (see docs/git-providers.md)", EnvGitHubAppID)
			}
			// With the installation's credentials service on, the App's key
			// is the service's (C2.2, D145): this job holds none, and none
			// is read -- not even when one is somehow present in the
			// environment, because a run that reached for a key it was not
			// given would be a credential the platform meant to withhold.
			// The provider is opened keyless and refuses every token until
			// the runner installs the service's (Provider.UseServiceTokens),
			// so there is no in-job path to mint one.
			if vars[EnvCredentialsServiceURL] != "" {
				p, err := github.New(github.Options{
					Owner: owner, Repo: name, AppID: appID, ServiceHeldKey: true,
					BaseURL: vars[EnvGitHubAPIURL], HTTP: hc, Warn: warn,
				})
				return p, nil, err
			}
			pemData, err := appKey(vars)
			if err != nil {
				return nil, nil, err
			}
			key, err := github.ParsePrivateKey([]byte(pemData))
			if err != nil {
				return nil, nil, err
			}
			p, err := github.New(github.Options{Owner: owner, Repo: name, AppID: appID, PrivateKey: key, BaseURL: vars[EnvGitHubAPIURL], HTTP: hc, Warn: warn})
			return p, []string{pemData}, err
		default:
			return nil, nil, fmt.Errorf("unknown git provider %q (want %s or %s)", kind, gitprov.KindGitHub, gitprov.KindBitbucket)
		}
	}
}

// Proxied returns an Opener for a proxied Bitbucket run (B1.7, G1, G9): the
// repository's published block admits it with proxy: true, the installation
// has its credentials service on, and the job therefore holds no Bitbucket
// token at all -- EnvBitbucketToken is not read, whatever the environment
// carries, because after the flip (B1.13 part two) there is nothing there to
// read and a run that reached for a token it was not given would be one the
// platform meant to withhold.
//
// forwarder is the runner's loopback forwarder URL
// (gateway.GitForward.URL: "http://127.0.0.1:<port>" plus the proxy route's
// prefix). Every request the adapter makes is sent there instead of to
// Bitbucket, and the adapter's own BaseURL stays Bitbucket's API root: the URL
// a request is built against is what paging compares a next link to
// (httpjson.Client.PagePath refuses one on another host), so the transport is
// the only thing that moves it and the path it moves carries the API root's
// own "2.0" segment, which is the segment the service's classifier reads.
//
// hc is the client the forwarder is called with; nil means the default one.
// warn is FromEnv's.
func Proxied(env []string, forwarder string, hc *http.Client, warn func(string)) gitprov.Opener {
	vars := envVars(env)
	return func(_ context.Context, kind, repo string) (gitprov.Provider, []string, error) {
		if kind != gitprov.KindBitbucket {
			return nil, nil, fmt.Errorf("unknown git provider %q (a proxied run reaches %s and nothing else)", kind, gitprov.KindBitbucket)
		}
		// Belt and braces: the caller decides proxied-ness from the published
		// block AND this variable (G9), so an opener asked for a proxied
		// provider without one has no service to forward to -- there is no
		// in-job token to fall back to (G5, D143), and opening one anyway
		// would be a run that fails at its first git command instead of here.
		if vars[EnvCredentialsServiceURL] == "" {
			return nil, nil, fmt.Errorf("%s is not set: a proxied run's Bitbucket requests are forwarded to the installation's credentials service, and this job names none", EnvCredentialsServiceURL)
		}
		owner, name, ok := gitprov.SplitRepo(repo)
		if !ok {
			return nil, nil, fmt.Errorf("repository %q must look like owner/name", repo)
		}
		// EnvBitbucketAPIURL is not read: the API root of a proxied run is the
		// forwarder's, and the service decides the upstream (plan T2.8).
		p, err := bitbucket.New(bitbucket.Options{
			Workspace: owner, Slug: name, Proxied: true,
			HTTP: proxiedClient(forwarder, hc), Warn: warn,
		})
		return p, nil, err
	}
}

// proxiedClient is the client a proxied adapter makes its requests with: every
// request is sent to the forwarder, and the adapter's own URL -- scheme, host,
// path and query -- is what it was built against, with the forwarder's path
// put in front of it.
//
// The rewrite is in the transport and not in BaseURL for one reason: paging.
// A next link Bitbucket answers with is on api.bitbucket.org, and PagePath
// accepts it only because the BaseURL the adapter compares it to is that same
// host and path -- so the API root has to stay what Bitbucket says it is while
// the wire it goes out on is the loopback one.
func proxiedClient(forwarder string, hc *http.Client) *http.Client {
	base := http.DefaultTransport
	if hc != nil && hc.Transport != nil {
		base = hc.Transport
	}
	c := &http.Client{Transport: forwardTransport{forwarder: forwarder, base: base}}
	if hc != nil {
		// The caller's own client, less its transport: its timeout and its
		// redirect policy are its own, and this is the one field a proxied
		// run has to replace.
		cp := *hc
		cp.Transport = c.Transport
		c = &cp
	}
	return c
}

// forwardTransport sends every request to the forwarder. The forwarder is on
// the loopback address, so no proxy from the environment is consulted for it
// (net/http skips one for a loopback host) and no TLS is involved: what the
// credentials service sees is the path the adapter built, unchanged, which is
// what makes the two readings of it -- the adapter's and the service's
// classifier's -- one reading (G13).
type forwardTransport struct {
	forwarder string // the forwarder's URL, ending in the proxy route's prefix
	base      http.RoundTripper
}

func (t forwardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f, err := url.Parse(t.forwarder)
	if err != nil || f.Host == "" {
		return nil, fmt.Errorf("gitprov/providers: %q is not the forwarder's URL", t.forwarder)
	}
	u := *req.URL
	u.Scheme, u.Host = f.Scheme, f.Host
	// One slash, not two: the service reads what follows the route's prefix,
	// and what it must read is the path the adapter built ("2.0/...").
	u.Path = strings.TrimSuffix(f.Path, "/") + req.URL.Path
	u.RawPath = ""
	out := req.Clone(req.Context())
	out.URL = &u
	out.Host = ""
	return t.base.RoundTrip(out)
}

func envVars(env []string) map[string]string {
	vars := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	return vars
}

// AppKey returns the GitHub App's private key PEM from env (KEY=VALUE pairs,
// usually os.Environ()), reading the file EnvGitHubAppKeyFile names when the
// key is not in the environment itself.
//
// It is exported for the caller that mints an installation token of its own
// rather than letting a provider mint one — the coordinator's read-only token
// (ee/coordinator.Project) — so that the mint and the opener cannot
// disagree about where the App's key is. A caller that mints tokens itself
// must redact the PEM out of every failure it reports, as FromEnv's own
// comment says of the value it returns.
func AppKey(env []string) (string, error) {
	return appKey(envVars(env))
}

func secret(vars map[string]string, name, what string) (string, error) {
	v := vars[name]
	if v == "" {
		return "", &notSetError{name: name, what: what}
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
