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
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// routeGitRun is a proxied run whose repository's route record is raw (nil for
// a repository that has published none), read from a fake database as bootstrap
// reads it, and whose job's environment names serviceURL as the credentials
// service of its own project. The record is published through the contract's
// own writer, so what the run reads is the bytes a member's own `fugaro update`
// leaves at creds.RoutePath(slug) (C2, WS9).
//
// The store is filled in because the read is by slug: a run's record is its
// repository's, and readRoute asks the store which one that is. The identity
// the run's read was exchanged with is the fixture's, and the database client
// is the fake's, exactly as identitiesRun leaves the two.
func routeGitRun(t *testing.T, serviceURL string, record *creds.Route) *run {
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
	return proxiedRun(t, pushIdentity(t), func(r *run) {
		r.d.Env = proxyEnvSet(r.d.Env, providers.EnvCredentialsServiceURL, serviceURL)
		r.d.Env = proxyEnvSet(r.d.Env, metadataHostEnv, strings.TrimPrefix(meta.URL, "http://"))
		r.d.Store = runstore.Open(bucket, slug, svcRunID)
		r.ident.done, r.ident.db = true, db
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
	})
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
	r := routeGitRun(t, proj.srv.URL, nil)
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
