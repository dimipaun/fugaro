package runner

// WS18's runner half: the service a run's git traffic reaches is the one its
// own credential route names for the Bitbucket kind (D2979, R3) -- the
// workspace's, at the address the record carries, or this project's own, whose
// address is the one the job's environment names, which is every run with no
// record. What the forwarder does with that answer is pinned in
// internal/gateway/gitforward_route_test.go; what is pinned here is the wiring
// between the two -- the record the run reads and the listener bootstrap starts
// for it -- because a run that read the record and started the forwarder for
// its own project's service anyway would pass every gateway-level test.
//
// The fixtures are gitproxy_test.go's (proxiedRun, pushIdentity, startForward)
// with bitbucket_identities_test.go's database client and
// git_service_internal_test.go's two stand-ins: the credentials service as a
// run's git path meets it, and the job's metadata server. The fetch is made
// with real git and the environment the run builds, as
// TestProxiedFetchFollowsThisExecutionsForwarder makes its own, because the
// claim is about where traffic ends up and not about a field the run holds.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// routeGitRun is a run whose repository's route record is raw (nil for a
// repository that has published none), read from a fake database as bootstrap
// reads it, and whose job's environment names serviceURL as the credentials
// service of its own project. The record is published through the contract's
// own writer, so what the run reads is the bytes a member's own `fugaro update`
// leaves at creds.RoutePath(slug) (C2, WS9).
//
// The store is filled in because the read is by slug: a run's record is its
// repository's, and readRoute asks the store which one that is. The identity
// the run's read was exchanged with is the fixture's, and the database client
// is the fake's, exactly as identitiesRun leaves the two.
//
// proxied is whether the repository's published policy admits the Bitbucket
// proxy. The git tests below are proxies -- the fetch goes through the run's
// own forwarder, which is what they are about -- and the github one is not: a
// proxied run opens no github provider at all (openProvider's first arm), so a
// github kind served from a workspace is a run without the proxy.
func routeGitRun(t *testing.T, serviceURL string, record *creds.Route, proxied bool) *run {
	t.Helper()
	slug, err := task.Slug("bitbucket", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	fake := gcpfake.NewRTDB(t)
	if record != nil {
		raw, err := creds.RouteRecord(*record)
		if err != nil {
			t.Fatalf("the record a member's own update would publish was refused by its own writer: %v", err)
		}
		fake.Set(creds.RoutePath(slug), json.RawMessage(raw))
	}
	db, err := rtdb.New(fake.URL, rtdb.Auth{IDToken: func() string { return "run-id-token" }})
	if err != nil {
		t.Fatal(err)
	}
	meta := gitMetadataFake(t, gitServiceIDToken)
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { _ = bucket.Close() })
	seed := func(r *run) {
		r.d.Env = proxyEnvSet(r.d.Env, providers.EnvCredentialsServiceURL, serviceURL)
		r.d.Env = proxyEnvSet(r.d.Env, metadataHostEnv, strings.TrimPrefix(meta.URL, "http://"))
		r.d.Store = runstore.Open(bucket, slug, svcRunID)
		r.ident.done, r.ident.db = true, db
	}
	id := pushIdentity(t)
	if proxied {
		return proxiedRun(t, id, seed)
	}
	return svcRun(t, config.AuthCascade, func(r *run) {
		r.d.Env = []string{}
		r.env = []string{"PATH=/usr/bin"}
		r.spec = &task.Spec{Version: 1, RunID: svcRunID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
		r.ident.tok = id
		seed(r)
	})
}

// fetchThroughTheForwarder fetches the run's own repository with real git and
// the environment the run builds: the insteadOf rewrite puts every Bitbucket
// URL on this execution's listener, and the listener sends the request to
// whichever service it was started for. The fetch fails -- neither stand-in is
// a git server -- and what is asked of it is where it got to.
func fetchThroughTheForwarder(t *testing.T, r *run) {
	t.Helper()
	startForward(t, r)
	const remote = "https://bitbucket.org/acme/app.git"
	r.credURL = gitops.CredentialURL(r.forwarderRemote(remote))
	vars, err := r.authVars()
	if err != nil {
		t.Fatalf("building the run's git credentials: %v", err)
	}
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	testutil.Git(t, dir, "remote", "add", "origin", remote)
	repo, err := gitops.Open(dir, gitops.WithVars(nil, vars))
	if err != nil {
		t.Fatalf("opening the checkout: %v", err)
	}
	if err := repo.FetchBase(context.Background(), "main"); err == nil {
		t.Error("a fetch from a service that is not a git server succeeded")
	}
}

// TestGitTrafficFollowsTheRoutesAddress: a run whose own record serves the
// Bitbucket kind from the workspace fetches through the workspace's service,
// and its project's own service is not reached at all. The job's environment
// names a service too -- the member's own, which is what a run with no record
// reaches -- and it is not the one traffic goes to: the address is the record's
// (R3, F32).
//
// The identity the request carries is the run's own and the job is the job the
// run reports, as it is against the member's own service (F30, O4): what a
// workspace's service verifies a run by is the token that admitted it.
//
// Mutation (run, restored): hand envLookup's answer to
// gateway.GitForwardOptions.Service in startProxiedGit -- the fetch reaches the
// project's stand-in and the workspace's is sent nothing.
func TestGitTrafficFollowsTheRoutesAddress(t *testing.T) {
	ws := newGitServiceFake(t, time.Now())
	proj := newGitServiceFake(t, time.Now())
	r := routeGitRun(t, proj.srv.URL, &creds.Route{
		Version: creds.RouteVersion, Address: ws.srv.URL, Bitbucket: creds.HolderWorkspace,
	}, true)
	fetchThroughTheForwarder(t, r)

	calls := ws.mintCalls()
	if len(calls) != 1 {
		t.Fatalf("the workspace's service was reached %d times, want the run's one fetch", len(calls))
	}
	if want := gateway.GitProxyPrefix + "acme/app.git/info/refs"; calls[0].path != want {
		t.Errorf("the fetch reached the workspace's service at %q, want %q", calls[0].path, want)
	}
	if want := "Bearer " + gitServiceIDToken; calls[0].auth != want {
		t.Errorf("the fetch carries the job identity %q, want %q", calls[0].auth, want)
	}
	if want := r.runIDToken(); calls[0].run != want || want == "" {
		t.Errorf("the fetch carries the run identity %q, want the run's own token", calls[0].run)
	}
	if want := svcJob; calls[0].job != want {
		t.Errorf("the fetch reports job %q, want %q", calls[0].job, want)
	}
	if got := proj.mintCalls(); len(got) != 0 {
		t.Errorf("this run's own service was reached %d times, want none: the record serves the Bitbucket kind from the workspace (D2979)", len(got))
	}
}

// TestNoRouteGitGoesToTheProject: a run whose repository has published no
// record is today's run, byte for byte -- its git goes to the service its job's
// environment names, and the workspace's is not reached. The record is what
// moves a kind (R4): a repository that never pinned an address is served
// exactly as it was before the record existed.
func TestNoRouteGitGoesToTheProject(t *testing.T) {
	ws := newGitServiceFake(t, time.Now())
	proj := newGitServiceFake(t, time.Now())
	r := routeGitRun(t, proj.srv.URL, nil, true)
	fetchThroughTheForwarder(t, r)

	calls := proj.mintCalls()
	if len(calls) != 1 {
		t.Fatalf("this run's own service was reached %d times, want the run's one fetch", len(calls))
	}
	if want := gateway.GitProxyPrefix + "acme/app.git/info/refs"; calls[0].path != want {
		t.Errorf("the fetch reached this run's own service at %q, want %q", calls[0].path, want)
	}
	if got := ws.mintCalls(); len(got) != 0 {
		t.Errorf("a run with no credential route reached the workspace's service %d times, want none: the project's own service is where every kind was served before the record existed", len(got))
	}
}

// recordingStandIn puts a stand-in in front of svc and keeps every request's
// headers as they went on the wire. A mint's headers are the other half of
// what the run sends, and the only half the stand-in itself does not keep: the
// adapter's request is what a slip would have to travel on, and nothing in the
// mint's own path builds one (R18).
func recordingStandIn(t *testing.T, svc *gitServiceFake) (*httptest.Server, func() []http.Header) {
	t.Helper()
	var mu sync.Mutex
	var seen []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		seen = append(seen, req.Header.Clone())
		mu.Unlock()
		svc.srv.Config.Handler.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []http.Header {
		mu.Lock()
		defer mu.Unlock()
		return append([]http.Header(nil), seen...)
	}
}

// noSlipOnTheMint fails when a mint carried anything of the run's beyond the
// two headers the contract puts on a call to a credentials service: the slip
// and the secret its claim answered are the model path's (R18, D2984) and a
// git mint to a workspace's service carries neither, so a mint that suddenly
// did would be a run naming the member's model budget to a workspace's
// service.
func noSlipOnTheMint(t *testing.T, seen []http.Header) {
	t.Helper()
	identities := map[string]bool{gateway.HeaderRunIdentity: true, gateway.HeaderRunJob: true}
	for _, h := range seen {
		for name := range h {
			if !strings.HasPrefix(name, "X-Fugaro-") || identities[name] {
				continue
			}
			if name == gateway.HeaderSlip || name == gateway.HeaderSlipClaim {
				t.Errorf("the mint carried %s: a call to the workspace's service carries the run's identity and its job, and no slip (R18)", name)
				continue
			}
			t.Errorf("the mint carried a header of the run's own: %s", name)
		}
	}
}

// TestGitHubMintFollowsTheRoute: the run's own forge token request follows the
// same answer its git traffic does (WS18, R3, F32). A record that serves the
// github kind from the workspace's service means the mint is made at the
// workspace's address, with the run's two identities and the App's ID the job
// holds -- and the provider is the keyless one, so the job's own App key is
// never read, whatever the environment carries.
//
// The two subtests are the two ways this can go wrong, and each is a run the
// other cannot see. A run that pointed the mint back at
// EnvCredentialsServiceURL mints at its project's own service, which the first
// subtest -- the job of a member whose own service is on -- reaches and
// answers, so the second stand-in's count catches it. A run that opened the
// provider the way a job with no route does reads the job's App key, which the
// second subtest -- a job that names no service of its own, the shape a route
// to a workspace's service leaves it in -- catches, because the planted key
// could not be read into a provider at all.
func TestGitHubMintFollowsTheRoute(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		// ownService is whether the job's environment names a credentials
		// service of its own project: true for a member who has the service on
		// (its address is the run's fallback, and the mint must not take it),
		// false for a job that names none at all.
		ownService bool
	}{
		{name: "the job names a service of its own", ownService: true},
		{name: "the job names none of its own", ownService: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := newGitServiceFake(t, now)
			proj := newGitServiceFake(t, now)
			stand, headers := recordingStandIn(t, ws)
			serviceURL := ""
			if tc.ownService {
				serviceURL = proj.srv.URL
			}
			r := routeGitRun(t, serviceURL, &creds.Route{
				Version: creds.RouteVersion, Address: stand.URL, GitHub: creds.HolderWorkspace,
			}, false)
			// The App's ID is the job's own even when the key is not (C2.1),
			// and every spelling of the key the platform injects for a job
			// whose key IS its own is planted with a value no opener could
			// read: a junk PEM, and a path that is not a file.
			r.d.Env = proxyEnvSet(r.d.Env, providers.EnvGitHubAppID, "1234")
			r.d.Env = proxyEnvSet(r.d.Env, providers.EnvGitHubAppKey, "not-a-pem-key")
			r.d.Env = proxyEnvSet(r.d.Env, providers.EnvGitHubAppKeyFile, "/nonexistent/app-key.pem")
			// The opener a run with no route uses, so the failure of a run
			// that fell back to it is the refusal of the key it would have
			// read rather than a nil opener.
			r.d.OpenProvider = providers.FromEnv(r.d.Env, nil, nil)
			r.d.Now = func() time.Time { return now }

			if err := r.openProvider(context.Background(), gitprov.KindGitHub, "the workflow"); err != nil {
				t.Fatalf("opening the github provider of a run whose record serves the kind from the workspace: %v", err)
			}

			calls := ws.mintCalls()
			if len(calls) != 1 {
				t.Fatalf("the workspace's service answered %d mints, want the run's one", len(calls))
			}
			call := calls[0]
			if want := "/v1/github/installation-token"; call.path != want {
				t.Errorf("the mint was asked at %s, want %s", call.path, want)
			}
			if want := "Bearer " + gitServiceIDToken; call.auth != want {
				t.Errorf("the mint's job identity is %q, want %q", call.auth, want)
			}
			if want := r.runIDToken(); call.run != want || want == "" {
				t.Errorf("the mint's run identity is %q, want the run's own token", call.run)
			}
			if want := svcJob; call.job != want {
				t.Errorf("the mint reports job %q, want %q", call.job, want)
			}
			if want := `{"app_id":"1234"}`; call.body != want {
				t.Errorf("the mint's body is %s, want %s", call.body, want)
			}
			if got := proj.mintCalls(); len(got) != 0 {
				t.Errorf("this run's own service was reached %d times, want none: the record serves the github kind from the workspace (D2979)", len(got))
			}
			noSlipOnTheMint(t, headers())
		})
	}
}
