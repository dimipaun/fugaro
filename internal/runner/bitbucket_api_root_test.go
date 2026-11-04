package runner

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestBitbucketAPIRootRefusalNamesHostOnly: the refusal is a string the run
// publishes -- into its record, its log and the failure an owner reads -- and
// the root it refuses is one a launcher wrote in the job's environment. The
// host is what its reader needs (it is the half of the root that says who was
// asked for the token, and the half the match is made on); the rest of the
// root is not: a path or a query is a place a launcher puts a credential, and
// a root carrying one in its userinfo is exactly the shape this refusal is
// about. The token itself is in that same environment, and the message never
// quotes it.
func TestBitbucketAPIRootRefusalNamesHostOnly(t *testing.T) {
	for name, root := range map[string]string{
		"path and query": "https://evil.example/private-root/workspace-a?access_token=" + bitbucketRootToken,
		"userinfo":       "https://person-1:" + bitbucketRootToken + "@evil.example/private-root",
	} {
		t.Run(name, func(t *testing.T) {
			r := bitbucketRootRun(t, bitbucketRootEnv(root), nil, false)
			err := r.openProvider(context.Background(), gitprov.KindBitbucket, "the --provider flag")
			if err == nil {
				t.Fatal("a cloud run opened its Bitbucket provider with a root no policy allows")
			}
			msg := err.Error()
			for _, want := range []string{providers.EnvBitbucketAPIURL, "evil.example", "fugaro project policy publish"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not name %q: %v", want, msg)
				}
			}
			for _, never := range []string{"private-root", "workspace-a", "access_token", "person-1", bitbucketRootToken} {
				if strings.Contains(msg, never) {
					t.Errorf("the refusal carries %q, which is part of the refused root: %v", never, msg)
				}
			}
		})
	}
}

// TestBitbucketAPIRootUnreadablePolicy: a policy node that cannot be read is
// neither a policy that allows nothing nor an installation without one
// (projectPolicy's third outcome), and the run does not read it as either: an
// override is refused when the allow-list could not be fetched, rather than
// sent to a root no one has allowed, and the refusal is the read's own, so the
// owner sees why the run stopped. Bitbucket's own root is decided before the
// policy is asked for and is not affected here.
func TestBitbucketAPIRootUnreadablePolicy(t *testing.T) {
	env := bitbucketRootEnv("https://evil.example/2.0")
	var requests atomic.Int64
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("no request may carry the token")
	})
	readErr := errors.New("reading the project policy: the database could not be reached")
	r := bitbucketRootRun(t, env, nil, false)
	r.projPolicy = policyRead{done: true, err: readErr}
	r.d.OpenProvider = providers.FromEnv(env, &http.Client{Transport: transport}, nil)
	err := r.openProvider(context.Background(), gitprov.KindBitbucket, "the --provider flag")
	if err == nil {
		t.Fatal("a cloud run whose policy could not be read opened its Bitbucket provider with an override")
	}
	if !errors.Is(err, readErr) {
		t.Errorf("the refusal is not the policy read's own: %v", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d requests were made before the refusal, want none", n)
	}
}

// TestRemoveStampHooksRefusesAPathNotDirectlyUnderTempDir: removeStampHooks
// takes each of the run's two hook directories out root and all, so it removes
// the parent of the directory it is given and refuses one whose root is not
// directly under the temporary directory -- a repository's own hooks, if the
// two fields ever came to name one (writeStampHooks, whose MkdirTemp roots are
// the only shape that reaches it). The run's own directory is still removed,
// which is the other half of the guard.
//
// It is here, in the package's own test file, and not in stamp_test.go beside
// the stamp's tests: removeStampHooks is unexported and reads the two fields
// directly, so the state it refuses cannot be built from runner_test.
func TestRemoveStampHooksRefusesAPathNotDirectlyUnderTempDir(t *testing.T) {
	r := &run{d: Deps{Log: slog.New(slog.DiscardHandler)}}
	// Both directories are the test's to remove whatever the guard does with
	// them. The run-shaped one matters: it is made where writeStampHooks makes
	// its own, directly under the temporary directory, so a guard that stopped
	// working would leave a fugaro-stamp-* directory in /tmp behind every run
	// of this test — and a test that pins the removal is exactly the one that
	// cannot rely on it.
	elsewhere := filepath.Join(t.TempDir(), "hooks")
	r.stampHooks = filepath.Join(elsewhere, "agent")
	if err := os.MkdirAll(r.stampHooks, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(elsewhere) })
	own, err := os.MkdirTemp("", "fugaro-stamp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(own) })
	r.runnerHooks = filepath.Join(own, "runner")
	if err := os.MkdirAll(r.runnerHooks, 0o755); err != nil {
		t.Fatal(err)
	}
	r.removeStampHooks()
	if _, err := os.Stat(r.stampHooks); err != nil {
		t.Errorf("a hook directory that is not the run's own was removed: %v", err)
	}
	if _, err := os.Stat(r.runnerHooks); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the run's own hook directory survived the run: %v", err)
	}
}

// roundTripFunc adapts a function to http.RoundTripper, the shape a counting
// transport needs.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
