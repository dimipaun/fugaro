// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The project-wide Bitbucket read (docs/bitbucket-credentials/implementation-plan.md,
// BB5). Two commands gain it and they gain it as two different CALLERS: the
// aggregation job sweeps a Bitbucket repository through the read mint as the
// coordinator account, and a person's local `coordinator status` reads one
// through it as themselves, each from its own variable — F23's whole point,
// because the job's FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL and the person's
// FUGARO_BITBUCKET_READ_SERVICE_URL name the same service and may not stand for
// each other.
//
// What each test here holds:
//
//   - TestSweepReadsBitbucketThroughService: the tick reads a Bitbucket
//     repository instead of marking it stale, and every link of that chain is
//     read back — the repository the mint was asked for, the identity presented
//     (the job's own account, never a person's), the token the API saw, and the
//     URL that was called.
//   - TestSweepGitHubUnchangedWithService and TestSweepBitbucketUnchangedWithoutService:
//     G8's two halves — the variable changes nothing about a GitHub tick, and
//     without it a Bitbucket repository is what it was before BB4.
//   - TestSweepRefusesAPIRootOverride: G10 on the tick's own path.
//   - TestSweepNeverRevokesBitbucketTokenAtGitHub: the record the command
//     revokes from is its GitHub mints, and a read token never enters it.
//   - TestLocalStatusBitbucketThroughService: the person's half, end to end.
//   - TestLocalStatusBitbucketAnotherWorkspaceRefused: B5's named refusal.
//   - TestLocalStatusBitbucketPointsAtFromStoreWithoutService: what the
//     refusal says where the person named no service, and that the GitHub read
//     is untouched by any of it.

// bitbucketSweepPR is the pull request the fixtures' runs name and the fake
// Bitbucket API answers. One number is enough: the read is what is under test,
// and a repository whose runs name no pull request is never asked about at all
// (Project.PullRequests returns before it opens).
const bitbucketSweepPR = 12

// jobIdentity is the coordinator job's own Google ID token as the platform
// mints it: a value the fake service can recognise, and the one thing that
// proves the sweep presented the JOB — the account the service's coordinator
// class verifies (G4) — rather than a person's identity or the App's key.
const jobIdentity = "fake-job-identity-token-not-a-credential"

// seedBitbucketRun writes one run of a Bitbucket repository into a fixture's
// bucket, under that repository's own slug.
//
// The slug is the reason this is not coordinatorSeeds: the runs bucket's tree
// is keyed by the provider's slug (task.Slug), so a run stored under GitHub's
// spelling would be invisible to a Bitbucket read — the sweep would read a
// repository with no work in flight, never ask the host about anything, and a
// test would pass over a read that never happened.
func seedBitbucketRun(t *testing.T, f *cloudFixture, repo, id string, pr int) {
	t.Helper()
	ctx := context.Background()
	b, err := blob.OpenBucket(ctx, f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	slug, err := task.Slug(gitprov.KindBitbucket, repo)
	if err != nil {
		t.Fatal(err)
	}
	branch := "fugaro/" + id
	store := runstore.Open(b, slug, id)
	spec := &task.Spec{Version: 1, RunID: id, Repo: repo, Ref: "main", Workflow: "web", Task: "x",
		Branch: branch, PR: pr, PreviousRun: "20261012-090000-0000"}
	if err := store.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, LaunchedBy: "someone@example.com"}); err != nil {
		t.Fatal(err)
	}
	rec := &runstore.Record{Version: 1, RunID: id, Repo: repo, Workflow: "web",
		Status: runstore.StatusSucceeded, Branch: branch, BaseBranch: "main"}
	rec.PR = &runstore.PRRef{Number: pr, URL: "https://bitbucket.org/" + repo + "/pull-requests/" + strconv.Itoa(pr)}
	if err := store.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

// viewOf is one repository's view document and stale marker as the fake
// database holds them.
func viewOf(t *testing.T, f *coordinatorSweepFixture, provider, repo string) (map[string]any, map[string]any) {
	t.Helper()
	slug, err := task.Slug(provider, repo)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := f.db.Value(coordinator.PathView(slug)).(map[string]any)
	s, _ := f.db.Value(coordinator.PathStale(slug)).(map[string]any)
	return v, s
}

// setBitbucketReadSeams replaces the three seams the Bitbucket read reaches the
// outside world through — the two identity tokens and the provider adapter —
// and returns the Options the command built for the adapter, so a test can read
// back what the command passed rather than what the fake API then received.
//
// The adapter is the REAL one, pointed at the fake API by rewriting the one
// field a test may rewrite (BB4's own arrangement in coordinator_show_test.go).
// The two identity seams are both replaced: the sweep's, which is the job's own
// account, and the person's, whose replacement FAILS — a command that presented
// a person's identity where the job's is verified would be admitted under the
// wrong class, and this is how that is noticed rather than read about.
func setBitbucketReadSeams(t *testing.T, api *bitbucketAPI) *bitbucket.Options {
	t.Helper()
	seen := &bitbucket.Options{}
	prevID, prevProvider := coordinatorIdentityToken, coordinatorBitbucketProvider
	coordinatorIdentityToken = func(context.Context, string) (string, error) { return jobIdentity, nil }
	coordinatorBitbucketProvider = func(o bitbucket.Options) (gitprov.Provider, error) {
		*seen = o
		o.Warn = nil
		if api != nil {
			o.BaseURL = api.srv.URL + "/2.0"
		}
		return bitbucket.New(o)
	}
	t.Cleanup(func() { coordinatorIdentityToken, coordinatorBitbucketProvider = prevID, prevProvider })
	return seen
}

// bitbucketSweepFixture is one tick over a Bitbucket repository: the sweep
// fixture, the real provider opener (the fixture's own GitHub fake is replaced
// back), the fake read mint and the fake API.
type bitbucketSweepFixture struct {
	*coordinatorSweepFixture
	mint *readMintServer
	// personMint is the service the PERSON's variable names on this job's
	// environment: the tick must never call it (F23).
	personMint *readMintServer
	api        *bitbucketAPI
	opened     *bitbucket.Options
	revoked    *[]string
}

// newBitbucketSweepFixture drives the sweep over the Bitbucket repository
// acme/legacy, with the read mint's URL set from the JOB's variable when
// serviceOn, and the PERSON's variable set to a second, identical fake that the
// tick must never call (F23: the two names are not each other).
func newBitbucketSweepFixture(t *testing.T, serviceOn bool) *bitbucketSweepFixture {
	t.Helper()
	// The opener the fixture's own GitHub fake replaces: the tick's Bitbucket
	// read has to reach the real one, which passes DefaultBaseURL to the
	// adapter and strips the environment's roots (G10).
	realOpen := openCoordinatorProvider
	f := newCoordinatorSweepFixture(t, nil)
	setCoordinatorHost(t, realOpen)
	t.Setenv(providers.EnvCredentialsServiceURL, "")
	t.Setenv(providers.EnvBitbucketAPIURL, "")
	t.Setenv(envCoordinatorRepos, `[{"provider":"bitbucket","repo":"acme/legacy"}]`)
	seedBitbucketRun(t, f.cloudFixture, "acme/legacy", "20261013-150000-bb50", bitbucketSweepPR)

	mint := newReadMintServer(t)
	personMint := newReadMintServer(t)
	t.Setenv(envBitbucketReadServiceURL, personMint.srv.URL)
	t.Setenv(envCoordinatorCredentialsServiceURL, "")
	if serviceOn {
		t.Setenv(envCoordinatorCredentialsServiceURL, mint.srv.URL)
	}
	api := newBitbucketAPI(t, "acme", "legacy", bitbucketSweepPR)
	return &bitbucketSweepFixture{
		coordinatorSweepFixture: f,
		mint:                    mint,
		personMint:              personMint,
		api:                     api,
		opened:                  setBitbucketReadSeams(t, api),
		revoked:                 setCoordinatorMint(t, nil),
	}
}

// TestSweepReadsBitbucketThroughService: a tick over a Bitbucket repository
// reads it through the installation's credentials service rather than marking
// it unsupported, and every link of that chain is held here — the repository
// the mint was asked for, the identity it was presented with, the token the API
// saw, the URL that was called, and the person's own variable left alone.
func TestSweepReadsBitbucketThroughService(t *testing.T) {
	f := newBitbucketSweepFixture(t, true)

	out, errOut, err := execute(t, "coordinator", "sweep")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	view, stale := viewOf(t, f.coordinatorSweepFixture, gitprov.KindBitbucket, "acme/legacy")
	if stale != nil {
		t.Fatalf("a swept Bitbucket repository is stale (%v): the read did not go through the service", stale)
	}
	if view == nil {
		t.Fatalf("the tick wrote no view for the Bitbucket repository; output %q", out)
	}
	if !strings.Contains(out, "acme/legacy: ") || !strings.Contains(out, "1 pull requests read") {
		t.Errorf("the tick's report = %q, want the repository reconciled with its one pull request read", out)
	}

	// The mint: one call, for the one repository the tick asked about, in the
	// spelling the installation uses.
	bodies, idents := f.mint.asked()
	if len(bodies) != 1 {
		t.Fatalf("the read mint was called %d times for one repository, want one: %v", len(bodies), bodies)
	}
	if want := `{"repository":"acme/legacy"}`; bodies[0] != want {
		t.Errorf("the read mint was asked for %s, want %s", bodies[0], want)
	}
	if idents[0] != "Bearer "+jobIdentity {
		t.Errorf("the read mint was presented %q, want the coordinator job's own identity", idents[0])
	}
	// And the person's variable named a second service that must not be the
	// one the tick called (F23): the two names are not each other, and a job
	// that minted as the person would be the wrong class at the front door.
	if bodies, _ := f.personMint.asked(); len(bodies) != 0 {
		t.Errorf("the tick minted at the URL %s names, which is a person's variable and not the job's: %v", envBitbucketReadServiceURL, bodies)
	}

	// The read itself: the API was asked for the one pull request, with the
	// token the service minted.
	if paths, tokens := f.api.paths, f.api.tokens; len(paths) != 1 || paths[0] != "/2.0/repositories/acme/legacy/pullrequests/12" {
		t.Fatalf("the API was asked for %v, want one pull-request read", paths)
	} else if tokens[0] != "Bearer "+f.mint.token {
		t.Errorf("the API was authenticated with %q, want the token the service minted", tokens[0])
	}
	if len(f.api.stray) != 0 {
		t.Errorf("the read reached %v beyond the pull request it asked for", f.api.stray)
	}

	// The adapter saw the repository the tick asked about, no token of its own,
	// and the pinned API root (G10): the test rewrites that field afterwards,
	// so what is read here is what the command passed.
	if f.opened.BaseURL != bitbucket.DefaultBaseURL {
		t.Errorf("the provider was opened on %q, want %s", f.opened.BaseURL, bitbucket.DefaultBaseURL)
	}
	if f.opened.Workspace != "acme" || f.opened.Slug != "legacy" {
		t.Errorf("the provider was opened on %s/%s, want the repository the tick swept", f.opened.Workspace, f.opened.Slug)
	}
	if f.opened.Token != "" {
		t.Errorf("the provider was opened with a Bitbucket token of its own: the read is minted, not held")
	}

	// Neither credential — the minted token or the identity that asked for it —
	// reaches what the tick prints.
	for _, secret := range []string{f.mint.token, jobIdentity} {
		if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
			t.Errorf("the tick printed a credential:\n%s%s", out, errOut)
		}
	}
}

// TestSweepGitHubUnchangedWithService: the coordinator job's own service URL
// changes nothing about a GitHub tick (G8, F23). The tick reads exactly what it
// read before — the same report, the same view — and the read mint is never
// called: GitHub is read with the App's credential, and the read consumer has
// nothing to do with it.
func TestSweepGitHubUnchangedWithService(t *testing.T) {
	mint := newReadMintServer(t)
	t.Setenv(envCoordinatorCredentialsServiceURL, mint.srv.URL)
	f := newCoordinatorSweepFixture(t, coordinatorSeeds(), coordinatorPRs()...)

	out, errOut, err := execute(t, "coordinator", "sweep")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	view, stale := f.view()
	if view == nil || stale != nil {
		t.Fatalf("the GitHub tick with a service URL set wrote view %v and marker %v", view, stale)
	}
	if !strings.Contains(out, "acme/app: ") || !strings.Contains(out, "swept 1 repositories: 1 reconciled, 0 stale") {
		t.Errorf("the tick's report = %q, want the report a GitHub tick always writes", out)
	}
	if bodies, _ := mint.asked(); len(bodies) != 0 {
		t.Errorf("a GitHub tick minted at the read mint: %v", bodies)
	}
}

// TestSweepBitbucketUnchangedWithoutService: with no service URL on the job, a
// Bitbucket repository is what it was before BB4 — recorded stale with
// StaleUnsupported, which is the marker the tick's own report carries — and the
// tick still exits 0: one such repository does not fail a project's other
// views.
func TestSweepBitbucketUnchangedWithoutService(t *testing.T) {
	f := newBitbucketSweepFixture(t, false)

	out, errOut, err := execute(t, "coordinator", "sweep")
	if err != nil {
		t.Fatalf("a Bitbucket repository the job has no service for failed the tick: %v (stderr: %s)", err, errOut)
	}
	view, stale := viewOf(t, f.coordinatorSweepFixture, gitprov.KindBitbucket, "acme/legacy")
	if stale == nil || stale["reason"] != coordinator.StaleUnsupported {
		t.Fatalf("the marker = %v, want %q", stale, coordinator.StaleUnsupported)
	}
	if view != nil {
		t.Errorf("a repository the read does not serve was given a view: %v", view)
	}
	if !strings.Contains(out, "stale") || !strings.Contains(out, "0 reconciled, 1 stale") {
		t.Errorf("the tick's report = %q, want the repository reported stale", out)
	}
	if bodies, _ := f.mint.asked(); len(bodies) != 0 {
		t.Errorf("a tick with no service URL minted anyway: %v", bodies)
	}
}

// TestSweepRefusesAPIRootOverride: the token the service mints reaches the API
// root this code pins and never one the environment names (G10). The variable
// is set here and the provider is read back: the root is an argument the
// command passes, so a launcher's value cannot move it.
func TestSweepRefusesAPIRootOverride(t *testing.T) {
	f := newBitbucketSweepFixture(t, true)
	t.Setenv(providers.EnvBitbucketAPIURL, "https://bitbucket.example.invalid/2.0")

	out, errOut, err := execute(t, "coordinator", "sweep")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	if f.opened.BaseURL != bitbucket.DefaultBaseURL {
		t.Errorf("the provider was opened on %q with %s set, want %s", f.opened.BaseURL, providers.EnvBitbucketAPIURL, bitbucket.DefaultBaseURL)
	}
	// And the read really went to the pinned root's own client: the fake API
	// stands in for it, so a tick that had reached the override would have
	// asked for nothing at all and stayed stale.
	if paths := f.api.paths; len(paths) != 1 {
		t.Errorf("the API was asked for %v, want the one pull-request read", paths)
	}
	if !strings.Contains(out, "acme/legacy: ") || !strings.Contains(out, "1 reconciled, 0 stale") {
		t.Errorf("the tick's report = %q, want the repository reconciled on the pinned root", out)
	}
}

// TestSweepNeverRevokesBitbucketTokenAtGitHub: the tokens the command takes
// back are the ones its own mint handed out, and the revoke is GitHub's own
// (DELETE /installation/token) — so a Bitbucket read token must never enter
// that record. The read mint's token is the service's and expires on its own;
// sending it to GitHub would be a credential leaving this process for a host it
// has nothing to do with.
func TestSweepNeverRevokesBitbucketTokenAtGitHub(t *testing.T) {
	f := newBitbucketSweepFixture(t, true)
	revoked := &[]string{}
	minted := &[]string{}
	prevMint, prevRevoke := coordinatorMint, coordinatorRevoke
	coordinatorMint = func(_ context.Context, repo string) (string, time.Time, error) {
		*minted = append(*minted, repo)
		return "ghs_fixture_" + strings.ReplaceAll(repo, "/", "_"), time.Now().Add(time.Hour), nil
	}
	coordinatorRevoke = func(_ context.Context, token string) error {
		*revoked = append(*revoked, token)
		return nil
	}
	t.Cleanup(func() { coordinatorMint, coordinatorRevoke = prevMint, prevRevoke })

	if _, errOut, err := execute(t, "coordinator", "sweep"); err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	if len(*minted) != 0 {
		t.Errorf("a Bitbucket tick minted GitHub installation tokens for %v, want none", *minted)
	}
	if len(*revoked) != 0 {
		t.Errorf("a Bitbucket tick revoked %v, want nothing: the read token is not this command's to take back", *revoked)
	}
	for _, token := range *revoked {
		if token == f.mint.token {
			t.Errorf("the read mint's token was handed to the GitHub revoke")
		}
	}
}

// bitbucketStatusFixture is a person's local read of one Bitbucket repository:
// the fixture's local config lists it, the App credential the opener is always
// built on is set, and the person's own variable names the fake service.
type bitbucketStatusFixture struct {
	f      *cloudFixture
	mint   *readMintServer
	api    *bitbucketAPI
	opened *bitbucket.Options
}

func newBitbucketStatusFixture(t *testing.T, serviceOn bool) *bitbucketStatusFixture {
	t.Helper()
	f := newCloudFixture(t)
	f.appendConfig(t, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	t.Setenv(providers.EnvCredentialsServiceURL, "")
	t.Setenv(providers.EnvBitbucketAPIURL, "")
	t.Setenv(envCoordinatorCredentialsServiceURL, "")
	t.Setenv(envBitbucketReadServiceURL, "")
	seedBitbucketRun(t, f, "acme/legacy", "20261013-150000-bb51", bitbucketSweepPR)

	mint := newReadMintServer(t)
	if serviceOn {
		t.Setenv(envBitbucketReadServiceURL, mint.srv.URL)
	}
	api := newBitbucketAPI(t, "acme", "legacy", bitbucketSweepPR)
	prevID, prevPerson, prevProvider := coordinatorIdentityToken, coordinatorBitbucketIDToken, coordinatorBitbucketProvider
	coordinatorBitbucketIDToken = func(context.Context) (string, error) { return personIdentity, nil }
	// The job's seam is the one this command must NOT use: a person's status
	// read presents the person (G4), and a metadata-server identity here would
	// be the coordinator class arriving on a machine that is not the job.
	coordinatorIdentityToken = func(context.Context, string) (string, error) {
		return "", errors.New("a person's command asked the metadata server for the job's identity")
	}
	seen := &bitbucket.Options{}
	coordinatorBitbucketProvider = func(o bitbucket.Options) (gitprov.Provider, error) {
		*seen = o
		o.Warn = nil
		o.BaseURL = api.srv.URL + "/2.0"
		return bitbucket.New(o)
	}
	t.Cleanup(func() {
		coordinatorIdentityToken, coordinatorBitbucketIDToken, coordinatorBitbucketProvider = prevID, prevPerson, prevProvider
	})
	return &bitbucketStatusFixture{f: f, mint: mint, api: api, opened: seen}
}

// TestLocalStatusBitbucketThroughService: a person's `coordinator status` on a
// Bitbucket repository reads it through the read mint under their OWN variable
// (BB5), presenting their own identity and not the job's, and the view the
// command prints is the host's answer.
func TestLocalStatusBitbucketThroughService(t *testing.T) {
	// The job's variable names a second service that must not be called: a
	// person's command is the person class, and the job's URL on a machine
	// that is not the job is a variable nothing here may read (F23).
	jobMint := newReadMintServer(t)
	t.Setenv(envCoordinatorCredentialsServiceURL, jobMint.srv.URL)
	f := newBitbucketStatusFixture(t, true)

	out, errOut, err := execute(t, "coordinator", "status", "--repo", "acme/legacy", "--since", "0", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var view map[string]any
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("the view is not JSON (%v):\n%s", err, out)
	}
	// The view's records carry the pull request the host answered for the one
	// number the repository's runs name.
	records, _ := view["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("the view holds %d records, want the one run's: %v", len(records), view)
	}
	rec, _ := records[0].(map[string]any)
	pr, _ := rec["pr"].(map[string]any)
	if pr["title"] != "Add search" || pr["number"] != float64(bitbucketSweepPR) {
		t.Errorf("the view's pull request = %v, want the host's own answer", rec["pr"])
	}

	bodies, idents := f.mint.asked()
	if len(bodies) != 1 || bodies[0] != `{"repository":"acme/legacy"}` {
		t.Fatalf("the read mint was asked %v, want one call for acme/legacy", bodies)
	}
	if idents[0] != "Bearer "+personIdentity {
		t.Errorf("the read mint was presented %q, want the person's own identity", idents[0])
	}
	if bodies, _ := jobMint.asked(); len(bodies) != 0 {
		t.Errorf("a person's read minted at the URL %s names, which is the job's variable: %v", envCoordinatorCredentialsServiceURL, bodies)
	}
	if tokens := f.api.tokens; len(tokens) != 1 || tokens[0] != "Bearer "+f.mint.token {
		t.Errorf("the API was authenticated with %v, want the token the service minted", tokens)
	}
	if f.opened.BaseURL != bitbucket.DefaultBaseURL {
		t.Errorf("the provider was opened on %q, want %s", f.opened.BaseURL, bitbucket.DefaultBaseURL)
	}
	if !strings.Contains(out, "acme/legacy") {
		t.Errorf("the view does not name the repository: %s", out)
	}
	for _, secret := range []string{f.mint.token, personIdentity} {
		if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
			t.Errorf("the read printed a credential:\n%s%s", out, errOut)
		}
	}
}

// refusedReadMint is a credentials service that refuses every read mint with
// one of its own codes and statuses, the way the service's own answer carries
// them (credsvc's WireError): the error word and, below it, the sentence an
// operator reads.
func newRefusedReadMint(t *testing.T, code string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != bitbucket.ServiceReadPath {
			t.Errorf("the read mint was asked for %s, want %s", r.URL.Path, bitbucket.ServiceReadPath)
		}
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   code,
			"message": "the repository is not in the workspace this service's read consumer is scoped to",
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestLocalStatusBitbucketAnotherWorkspaceRefused: a repository outside the
// read consumer's workspace is refused BY NAME and not read half-way (B5, G11).
// The refusal is the mint's own — one code, carried through the client — and
// the command prints no view for the repository: a repository the service would
// not mint for is not a repository with nothing in flight.
func TestLocalStatusBitbucketAnotherWorkspaceRefused(t *testing.T) {
	srv := newRefusedReadMint(t, "request_refused", http.StatusForbidden)
	f := newBitbucketStatusFixture(t, false)
	t.Setenv(envBitbucketReadServiceURL, srv.URL)

	out, errOut, err := execute(t, "coordinator", "status", "--repo", "acme/legacy", "--since", "0")
	if err == nil {
		t.Fatalf("a repository the read mint refuses was read anyway:\n%s", out)
	}
	// The named reason: the service's own word for what it refused, which is
	// what tells an operator this is the repository's workspace and not an
	// outage. The service's prose is never repeated — it is another writer's
	// text and it can carry the caller's own — so the code is what is named.
	for _, want := range []string{"request_refused", "403"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal = %q, want it to name %q", err.Error(), want)
		}
	}
	if strings.Contains(out, "records") || strings.Contains(out, "pull_requests") {
		t.Errorf("the command printed a view for a repository the mint refused:\n%s", out)
	}
	if len(f.api.paths) != 0 {
		t.Errorf("the command read the API anyway: %v", f.api.paths)
	}
	if strings.Contains(errOut, personIdentity) || strings.Contains(err.Error(), personIdentity) {
		t.Errorf("the refusal carries the caller's identity: %s%s", errOut, err)
	}
}

// TestLocalStatusBitbucketPointsAtFromStoreWithoutService: with no service URL
// named anywhere, a Bitbucket repository is refused exactly as it was before
// BB4 — and the refusal says what a person can do about it, which is the stored
// view (`--from-store`, no host read and no key). The GitHub read beside it is
// untouched by any of it (G8).
func TestLocalStatusBitbucketPointsAtFromStoreWithoutService(t *testing.T) {
	f := newBitbucketStatusFixture(t, false)
	// The local config's published block is the other half of the person's
	// sources, and it is deliberately NOT one for status (BB4's own reading):
	// the presence of a credentials_service block must not turn this read on.
	f.f.appendConfig(t, "credentials_service:\n  url: https://credentials-service.example.invalid\n")
	t.Setenv(envBitbucketReadServiceURL, "")

	out, errOut, err := execute(t, "coordinator", "status", "--repo", "acme/legacy", "--since", "0")
	if err == nil {
		t.Fatalf("a Bitbucket repository was read with no service URL:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--from-store") {
		t.Errorf("the refusal = %q, want it to point at --from-store (stderr: %s)", err.Error(), errOut)
	}
	if bodies, _ := f.mint.asked(); len(bodies) != 0 {
		t.Errorf("the read mint was called for a read that named no service: %v", bodies)
	}

	// The GitHub half of the same fixture is a read as it always was: the
	// person's Bitbucket variable names no service and changes nothing here.
	githubOut, githubErr, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--since", "0", "--json")
	if err != nil {
		t.Fatalf("a GitHub read with no Bitbucket service URL = %v (stderr: %s)", err, githubErr)
	}
	if !strings.Contains(githubOut, "acme/app") {
		t.Errorf("the GitHub view does not name the repository: %s", githubOut)
	}
}
