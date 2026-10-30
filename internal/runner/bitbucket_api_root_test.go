package runner

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
)

// These are T2.8's runner half (D474): a cloud run sends its Bitbucket token
// only to Bitbucket's own API or to a root the owner allow-listed in the
// published policy. The decision is asked on values -- openProvider with the
// memoized policy read pre-filled the way projectPolicy leaves it -- because
// that is where the security property lives: which root is refused, and that
// the refusal lands before the provider is opened, so no request carries the
// token. The policy read itself is internal/projectpolicy's and
// internal/runner/projectpolicy_test.go's.

const bitbucketRootToken = "bitbucket-token-value-for-tests"

// bitbucketRootRun is a run whose only business is its Bitbucket API root: the
// job's environment (the token and the override), the opener, and the policy
// read filled in as the memoized read would leave it. The zero Deps.Local is
// the cloud (gateway.go): these runs are cloud runs unless a test says
// otherwise.
func bitbucketRootRun(t *testing.T, env []string, p *projectpolicy.Policy, found bool) *run {
	t.Helper()
	r := &run{
		d: Deps{
			Env:          env,
			Log:          slog.New(slog.DiscardHandler),
			Now:          time.Now,
			OpenProvider: providers.FromEnv(env, http.DefaultClient, nil),
		},
		spec: &task.Spec{Version: 1, RunID: "20261030-000000-abcd", Repo: "owner/repo-a",
			Ref: "main", Workflow: "app", Task: "Add a feature"},
	}
	r.projPolicy = policyRead{done: true, policy: p, found: found}
	return r
}

// bitbucketRootEnv is the job's environment with the token and root the test
// names, the two variables providers.FromEnv reads for a Bitbucket run.
func bitbucketRootEnv(root string) []string {
	return []string{
		providers.EnvBitbucketToken + "=" + bitbucketRootToken,
		providers.EnvBitbucketAPIURL + "=" + root,
	}
}

// TestCloudRunRefusesAnotherBitbucketAPIRoot is T2.8's red step: today the
// override in FUGARO_BITBUCKET_API_URL is honoured whatever it names, so a
// launcher who overrode the job's environment could have the run send the
// token to a host of their own. The refusal names the variable, the refused
// root by its host alone, and the remedy.
func TestCloudRunRefusesAnotherBitbucketAPIRoot(t *testing.T) {
	r := bitbucketRootRun(t, bitbucketRootEnv("https://bitbucket.example.com/2.0"), nil, false)
	err := r.openProvider(context.Background(), gitprov.KindBitbucket, "the --provider flag")
	if err == nil {
		t.Fatal("a cloud run opened its Bitbucket provider with a root no policy allows")
	}
	for _, want := range []string{providers.EnvBitbucketAPIURL, "bitbucket.example.com", "fugaro project policy publish"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestCloudRunAcceptsAnOwnerAllowListedBitbucketAPIRoot: a root the owner
// published in bitbucket_api_roots is the one other place the token may go, so
// the run opens its provider as it always did.
func TestCloudRunAcceptsAnOwnerAllowListedBitbucketAPIRoot(t *testing.T) {
	const root = "https://bitbucket.example.com/api"
	r := bitbucketRootRun(t, bitbucketRootEnv(root), &projectpolicy.Policy{BitbucketAPIRoots: []string{root}}, true)
	if err := r.openProvider(context.Background(), gitprov.KindBitbucket, "the --provider flag"); err != nil {
		t.Fatalf("a run whose root the policy allow-lists could not open its provider: %v", err)
	}
	if r.auth.Token != bitbucketRootToken {
		t.Errorf("the run's git credentials are %q, want the job's token", r.auth.Token)
	}
}

// TestCloudRunAcceptsTheDefaultBitbucketAPIRoot: Bitbucket's own API is always
// allowed, whatever the policy says -- including the installation that has
// published no policy at all, whose runs must keep working.
func TestCloudRunAcceptsTheDefaultBitbucketAPIRoot(t *testing.T) {
	r := bitbucketRootRun(t, bitbucketRootEnv(bitbucket.DefaultBaseURL), nil, false)
	if err := r.openProvider(context.Background(), gitprov.KindBitbucket, "the --provider flag"); err != nil {
		t.Fatalf("a run on Bitbucket's own API could not open its provider: %v", err)
	}
}

// TestCloudRunRefusesARootThatOnlySharesAPrefix: the match is an equality on
// the canonical root, never a prefix test. A host that merely begins with an
// allowed one is another host, and a root that hides its host behind userinfo
// is the host after the "@".
func TestCloudRunRefusesARootThatOnlySharesAPrefix(t *testing.T) {
	p := &projectpolicy.Policy{BitbucketAPIRoots: []string{"https://api.bitbucket.org"}}
	for _, root := range []string{
		"https://api.bitbucket.org.evil.example/2.0",
		"https://api.bitbucket.org/2.0@other.example",
	} {
		r := bitbucketRootRun(t, bitbucketRootEnv(root), p, true)
		if err := r.openProvider(context.Background(), gitprov.KindBitbucket, "the --provider flag"); err == nil {
			t.Errorf("a root that only shares a prefix with an allowed one was accepted: %s", root)
		}
	}
}

// TestLocalRunKeepsTheBitbucketAPIRootOverride: a local invocation is the
// developer's own machine and its own credentials, so the pin is not theirs to
// hold -- the override reaches the host it names, as it always did.
func TestLocalRunKeepsTheBitbucketAPIRootOverride(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got.Add(1)
		if !strings.HasPrefix(req.URL.Path, "/2.0/repositories/") {
			t.Errorf("the request reached %s, want the override's /2.0 root", req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"is_private":true}`))
	}))
	defer srv.Close()
	env := bitbucketRootEnv(srv.URL + "/2.0")
	r := bitbucketRootRun(t, env, nil, false)
	r.d.Local = true
	r.d.OpenProvider = providers.FromEnv(env, srv.Client(), nil)
	if err := r.openProvider(context.Background(), gitprov.KindBitbucket, "the --provider flag"); err != nil {
		t.Fatalf("a local run could not open its provider with the override: %v", err)
	}
	if _, err := r.provider.Repository(context.Background()); err != nil {
		t.Fatalf("reading the repository through the override: %v", err)
	}
	if got.Load() == 0 {
		t.Error("the local run's API call did not reach the override's host")
	}
}

// TestRefusalHappensBeforeAnyProviderRequest: the refusal lands before the
// provider opens, so the token never reaches a socket. The transport counts
// every request, and the fixture's host is the refused one.
func TestRefusalHappensBeforeAnyProviderRequest(t *testing.T) {
	var requests atomic.Int64
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("no request may carry the token")
	})
	env := bitbucketRootEnv("https://evil.example/2.0")
	r := bitbucketRootRun(t, env, nil, false)
	r.d.OpenProvider = providers.FromEnv(env, &http.Client{Transport: transport}, nil)
	if err := r.openProvider(context.Background(), gitprov.KindBitbucket, "the --provider flag"); err == nil {
		t.Fatal("a cloud run opened its Bitbucket provider with a root no policy allows")
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d requests were made before the refusal, want none", n)
	}
}

// TestGitHubRunIgnoresTheBitbucketRoot: FUGARO_BITBUCKET_API_URL is out of
// scope for a GitHub run, which neither reads it nor is held to it. The opener
// reports the kind it was asked for, so the test sees the GitHub path reach it
// rather than a Bitbucket-root refusal.
func TestGitHubRunIgnoresTheBitbucketRoot(t *testing.T) {
	env := bitbucketRootEnv("https://evil.example/2.0")
	var opened string
	r := bitbucketRootRun(t, env, nil, false)
	r.d.OpenProvider = func(_ context.Context, kind, _ string) (gitprov.Provider, []string, error) {
		opened = kind
		return nil, nil, errors.New("the stub opener was reached")
	}
	err := r.openProvider(context.Background(), gitprov.KindGitHub, "the --provider flag")
	if opened != gitprov.KindGitHub {
		t.Fatalf("the github opener was not reached (opened %q): %v", opened, err)
	}
	if strings.Contains(err.Error(), providers.EnvBitbucketAPIURL) {
		t.Errorf("a github run was refused over the Bitbucket root: %v", err)
	}
}

// roundTripFunc adapts a function to http.RoundTripper, the shape a counting
// transport needs.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
