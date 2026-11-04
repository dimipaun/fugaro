// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
// through it as themselves.
//
// The two classes now resolve the service from two different places, and that
// difference is F23's whole point read the other way round: the job has no
// project config file at all and its service URL is the variable Terraform sets
// on it (FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL), while a person's command
// reads the block the project published and has no variable at all (the owner's
// ruling of 2026-11-03). A person's machine that carries the job's variable
// reads the service the project published and never the one the variable names.
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
//   - TestLocalStatusBitbucketThroughService: the person's half, end to end,
//     with the service published in the project's own configuration.
//   - TestLocalStatusBitbucketAnotherWorkspaceRefused: B5's named refusal.
//   - TestLocalStatusBitbucketPointsAtFromStoreWithoutService: a project that
//     publishes no service is one whose Bitbucket read is refused, and the
//     refusal names what a person can do instead.

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

// setBitbucketReadSeams replaces the two seams the JOB's Bitbucket read reaches
// the outside world through — the job's identity token and the provider adapter
// — and returns the Options the command built for the adapter, so a test can
// read back what the command passed rather than what the fake API then
// received.
//
// The adapter is the REAL one, pointed at the fake API by rewriting the one
// field a test may rewrite (BB4's own arrangement in coordinator_show_test.go).
// The identity seam is the job's: the sweep presents the account the service's
// coordinator class verifies, and a person's identity here would be admitted
// under the wrong class.
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

// setServiceClientSeam points every read mint's calls at mint's own client.
//
// It is the same seam the show tests set and for the same reason: the fake
// service is a TLS server no machine trusts (a project's published URL is https
// by validation, so a hermetic fake has to be one), while the client a real
// path builds is the hardened one that trusts nothing. It is nil on every real
// path.
func setServiceClientSeam(t *testing.T, mint *readMintServer) {
	t.Helper()
	prev := coordinatorServiceClient
	coordinatorServiceClient = func() *http.Client { return mint.client() }
	t.Cleanup(func() { coordinatorServiceClient = prev })
}

// bitbucketSweepFixture is one tick over a Bitbucket repository: the sweep
// fixture, the real provider opener (the fixture's own GitHub fake is replaced
// back), the fake read mint and the fake API.
type bitbucketSweepFixture struct {
	*coordinatorSweepFixture
	mint    *readMintServer
	api     *bitbucketAPI
	opened  *bitbucket.Options
	revoked *[]string
}

// newBitbucketSweepFixture drives the sweep over the Bitbucket repository
// acme/legacy, with the read mint's URL set from the JOB's variable when
// serviceOn. The job's variable is the only source there is for this class: it
// has no project config file, so nothing else could turn the read on.
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

	mint := newReadMintServer(t, bitbucket.ServiceReadPath)
	setServiceClientSeam(t, mint)
	t.Setenv(envCoordinatorCredentialsServiceURL, "")
	if serviceOn {
		t.Setenv(envCoordinatorCredentialsServiceURL, mint.srv.URL)
	}
	api := newBitbucketAPI(t, "acme", "legacy", bitbucketSweepPR)
	return &bitbucketSweepFixture{
		coordinatorSweepFixture: f,
		mint:                    mint,
		api:                     api,
		opened:                  setBitbucketReadSeams(t, api),
		revoked:                 setCoordinatorMint(t, nil),
	}
}

// TestSweepReadsBitbucketThroughService: a tick over a Bitbucket repository
// reads it through the installation's credentials service rather than marking
// it unsupported, and every link of that chain is held here — the repository
// the mint was asked for, the identity it was presented with, the token the API
// saw and the URL that was called.
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
	paths, bodies, idents := f.mint.asked()
	if len(paths) != 1 || paths[0] != bitbucket.ServiceReadPath {
		t.Fatalf("the read mint was asked on %v, want one call on %s", paths, bitbucket.ServiceReadPath)
	}
	if want := `{"repository":"acme/legacy"}`; bodies[0] != want {
		t.Errorf("the read mint was asked for %s, want %s", bodies[0], want)
	}
	if idents[0] != "Bearer "+jobIdentity {
		t.Errorf("the read mint was presented %q, want the coordinator job's own identity", idents[0])
	}

	// The read itself: the API was asked for the one pull request, with the
	// token the service minted.
	if paths, tokens := f.api.paths, f.api.tokens; len(paths) != 1 || paths[0] != "/2.0/repositories/acme/legacy/pullrequests/12" {
		t.Fatalf("the API was asked for %v, want one pull-request read", paths)
	} else if tokens[0] != "Bearer "+f.mint.tokenFor("acme/legacy") {
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
	for _, secret := range []string{f.mint.tokenFor("acme/legacy"), jobIdentity} {
		if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
			t.Errorf("the tick printed a credential:\n%s%s", out, errOut)
		}
	}
}

// TestSweepGitHubUnchangedWithService: the coordinator job's own service URL
// changes nothing about a GitHub tick (G8). The tick reads exactly what it read
// before — the same report, the same view — and the read mint is never called:
// GitHub is read with the App's credential, and the read consumer has nothing
// to do with it.
func TestSweepGitHubUnchangedWithService(t *testing.T) {
	mint := newReadMintServer(t, bitbucket.ServiceReadPath)
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
	if paths, _, _ := mint.asked(); len(paths) != 0 {
		t.Errorf("a GitHub tick minted at the read mint: %v", paths)
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
	if paths, _, _ := f.mint.asked(); len(paths) != 0 {
		t.Errorf("a tick with no service URL minted anyway: %v", paths)
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
		if token == f.mint.tokenFor("acme/legacy") {
			t.Errorf("the read mint's token was handed to the GitHub revoke")
		}
	}
}

// bitbucketStatusFixture is a person's local read of one Bitbucket repository:
// the fixture's local config lists it, and the fake service — when the test
// publishes one — is the project's own credentials_service block, which is the
// only source a person's read has.
type bitbucketStatusFixture struct {
	f      *cloudFixture
	mint   *readMintServer
	api    *bitbucketAPI
	opened *bitbucket.Options
}

// newBitbucketStatusFixture builds the fixture with mint published as the
// project's credentials service, or with none published at all when mint is
// nil — the two states a person's machine is in, and the two the tests below
// tell apart.
func newBitbucketStatusFixture(t *testing.T, mint *readMintServer) *bitbucketStatusFixture {
	t.Helper()
	f := newCloudFixture(t)
	f.appendConfig(t, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")
	t.Setenv(providers.EnvCredentialsServiceURL, "")
	t.Setenv(providers.EnvBitbucketAPIURL, "")
	t.Setenv(envCoordinatorCredentialsServiceURL, "")
	seedBitbucketRun(t, f, "acme/legacy", "20261013-150000-bb51", bitbucketSweepPR)
	if mint != nil {
		f.publishService(t, mint.srv.URL)
		setServiceClientSeam(t, mint)
	}

	api := newBitbucketAPI(t, "acme", "legacy", bitbucketSweepPR)
	prevID, prevPerson, prevProvider := coordinatorIdentityToken, coordinatorPersonIdentity, coordinatorBitbucketProvider
	coordinatorPersonIdentity = func(context.Context, string) (string, error) { return personIdentity, nil }
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
		coordinatorIdentityToken, coordinatorPersonIdentity, coordinatorBitbucketProvider = prevID, prevPerson, prevProvider
	})
	return &bitbucketStatusFixture{f: f, mint: mint, api: api, opened: seen}
}

// TestLocalStatusBitbucketThroughService: a person's `coordinator status` on a
// Bitbucket repository reads it through the service the PROJECT published
// (BB5, and the owner's ruling of 2026-11-03), presenting their own identity
// and not the job's, and the view the command prints is the host's answer.
func TestLocalStatusBitbucketThroughService(t *testing.T) {
	// The job's variable names a second service that must not be called: a
	// person's command is the person class, and the job's URL on a machine that
	// is not the job is a variable nothing here may read (F23).
	jobMint := newReadMintServer(t, bitbucket.ServiceReadPath)
	t.Setenv(envCoordinatorCredentialsServiceURL, jobMint.srv.URL)
	mint := newReadMintServer(t, bitbucket.ServiceReadPath)
	f := newBitbucketStatusFixture(t, mint)

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

	paths, bodies, idents := f.mint.asked()
	if len(paths) != 1 || paths[0] != bitbucket.ServiceReadPath || bodies[0] != `{"repository":"acme/legacy"}` {
		t.Fatalf("the read mint was asked %v / %v, want one call for acme/legacy", paths, bodies)
	}
	if idents[0] != "Bearer "+personIdentity {
		t.Errorf("the read mint was presented %q, want the person's own identity", idents[0])
	}
	if paths, _, _ := jobMint.asked(); len(paths) != 0 {
		t.Errorf("a person's read minted at the URL %s names, which is the job's variable: %v", envCoordinatorCredentialsServiceURL, paths)
	}
	if tokens := f.api.tokens; len(tokens) != 1 || tokens[0] != "Bearer "+f.mint.tokenFor("acme/legacy") {
		t.Errorf("the API was authenticated with %v, want the token the service minted", tokens)
	}
	if f.opened.BaseURL != bitbucket.DefaultBaseURL {
		t.Errorf("the provider was opened on %q, want %s", f.opened.BaseURL, bitbucket.DefaultBaseURL)
	}
	if !strings.Contains(out, "acme/legacy") {
		t.Errorf("the view does not name the repository: %s", out)
	}
	for _, secret := range []string{f.mint.tokenFor("acme/legacy"), personIdentity} {
		if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
			t.Errorf("the read printed a credential:\n%s%s", out, errOut)
		}
	}
}

// newRefusedReadMint is a credentials service that refuses every read mint with
// one of its own codes and statuses, the way the service's own answer carries
// them (credsvc's WireError): the error word and, below it, the sentence an
// operator reads.
func newRefusedReadMint(t *testing.T, code string, status int) *readMintServer {
	t.Helper()
	m := newReadMintServer(t, bitbucket.ServiceReadPath)
	m.refuse(t, code, status)
	return m
}

// TestLocalStatusBitbucketAnotherWorkspaceRefused: a repository outside the
// read consumer's workspace is refused BY NAME and not read half-way (B5, G11).
// The refusal is the mint's own — one code, carried through the client — and
// the command prints no view for the repository: a repository the service would
// not mint for is not a repository with nothing in flight.
func TestLocalStatusBitbucketAnotherWorkspaceRefused(t *testing.T) {
	mint := newRefusedReadMint(t, "request_refused", http.StatusForbidden)
	f := newBitbucketStatusFixture(t, mint)

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

// TestLocalStatusBitbucketPointsAtFromStoreWithoutService: with no service
// published by the project, a Bitbucket repository is refused exactly as it was
// before BB4 — and the refusal says what a person can do about it, which is the
// stored view (`--from-store`, no host read and no key).
//
// The GitHub half beside it is the other property of the same rule rather than
// a leftover: a repository whose runs name no pull request is read with no
// credential at all, because the read returns before it opens a provider, so a
// project that publishes no service still reads the repositories that have
// nothing in flight.
func TestLocalStatusBitbucketPointsAtFromStoreWithoutService(t *testing.T) {
	f := newBitbucketStatusFixture(t, nil)

	out, errOut, err := execute(t, "coordinator", "status", "--repo", "acme/legacy", "--since", "0")
	if err == nil {
		t.Fatalf("a Bitbucket repository was read with no service published:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--from-store") {
		t.Errorf("the refusal = %q, want it to point at --from-store (stderr: %s)", err.Error(), errOut)
	}
	if len(f.api.paths) != 0 {
		t.Errorf("a read that published no service reached the API: %v", f.api.paths)
	}

	// The GitHub half: no runs name a pull request, so nothing is opened and
	// nothing is minted — the read is a view with no records rather than a
	// refusal.
	githubOut, githubErr, err := execute(t, "coordinator", "status", "--repo", "acme/app", "--since", "0", "--json")
	if err != nil {
		t.Fatalf("a GitHub repository with no work in flight = %v (stderr: %s)", err, githubErr)
	}
	if !strings.Contains(githubOut, "acme/app") {
		t.Errorf("the GitHub view does not name the repository: %s", githubOut)
	}
}
