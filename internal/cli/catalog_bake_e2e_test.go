package cli

// catalog_bake_e2e_test.go is I2's own file: the two halves of one catalog
// build, held together. A build of an entry serves every repository the
// project's map puts on it (D2301), so its definition carries one bake stage
// per repository and its clone step needs one credential over all of them —
// which no credential this process holds is. That credential is the
// installation's credentials service's (B1, internal/buildtoken), and the call
// is made by the client gitcred_build.go ships; the stub C2 left while the two
// halves were built side by side is deleted, so what runs below is the shipped
// client against the service and not a recording seam.
//
// The service is the real one (internal/credsvc.Server) on a loopback listener,
// and the GitHub it mints at is the recorded exchange of a two-repository mint
// (testdata/github_build_token_two_repos.json, replayed by
// internal/gitprov/httpfixture, which fails the test on a request body naming
// any other repositories or permissions). So what the build asks for, what the
// service narrows it to and what reaches the credential file are one run of the
// shipped path rather than three readings of it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/imagebake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// buildE2EBuildID is the build the credential step names. It is the shape a
	// Cloud Build id has, which is the shape the service's own reader holds the
	// field to (B1).
	buildE2EBuildID = "8f3c1a2b-0000-4000-8000-0000000000e2"
	// buildE2EIdentity is the project's build identity: the account a catalog
	// build runs as (B5) and the one caller class the build route admits (B1).
	buildE2EIdentity = "build-identity@proj-1234.iam.gserviceaccount.com"
	// buildE2EIdentityToken is what the metadata stand-in answers. The class
	// check itself is internal/credsvc's own test — the claim and key-set
	// reading lives there, with the fixture it needs — so this is a marker: the
	// test reads it back out of the header the route was handed, which is what
	// pins that the step presented its own identity and nothing else.
	buildE2EIdentityToken = "build-identity-token-not-a-real-credential"
)

// TestCatalogBuildBakesTwoReposThroughTheService: one entry whose map puts two
// repositories on it, built once and baked into one image, with the one
// credential that clones both asked of the credentials service.
//
// The two halves are two commands and cannot be one: this repository's `image
// build` composes the definition and submits the Cloud Build, and the credential
// step of that build is the CLI's entry mode running inside it. So the test
// composes the build and then runs the step with the plan's own repositories.
//
// No submitted build runs that step yet: the step the template carries is the
// one-repository mode (images/derived/cloudbuild.yaml), and the env
// internal/backend/gcp/catalog.go composes for it from credentialEnv names the
// provider, the repository and the App ID — no entry, no bake plan's
// repositories, no build id and no FUGARO_CREDENTIALS_SERVICE_URL
// (internal/backend/gcp/build.go). A submitted bake therefore still clones with
// the one-repository mint narrowed to its own repository, and what hands the
// entry mode to it is the plan's B3 and B6, which write that file after B8 (the
// shared-files table in docs/shared-jobs-default/implementation-plan.md). This
// file holds the entry mode's two halves together against the service until
// that wiring exists.
func TestCatalogBuildBakesTwoReposThroughTheService(t *testing.T) {
	// The second repository the project's map puts on the entry the build's own
	// repository runs: two repositories on one entry is D2301's ordinary case,
	// and it is the case no credential this build holds can serve.
	const second = "acme/lib"
	fb, gcs, f := catalogBuildCheckoutFor(t, cliMinimalYAML, catalogBuildYAML, gitprov.KindGitHub, catalogBuildAppID)
	addRepoToProject(t, second, gitprov.KindGitHub, catalogBuildAppID)
	// The map is keyed by provider (repo_images, D3370): one key per repository,
	// under the provider the project serves it at, which is GitHub here for both.
	f.appendConfig(t, "repo_images:\n  github/acme/app: tools\n  github/"+second+": tools\nbake_repos: true\n")

	_, stderr, err := executeBuild(t, "image", "build", "tools", "--base", "b:1")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	appSlug := mustSlug(gitprov.KindGitHub, "acme/app")
	libSlug := mustSlug(gitprov.KindGitHub, second)
	appDir, err := imagebake.Path(appSlug)
	if err != nil {
		t.Fatal(err)
	}
	libDir, err := imagebake.Path(libSlug)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := catalogCopy(t, gcs, "tools")
	for _, dir := range []string{appDir, libDir} {
		if !strings.Contains(got, "# fugaro bake: "+dir) {
			t.Errorf("the copy has no stage baking a checkout at %s: every repository the map puts on the entry is baked into its image:\n%s", dir, got)
		}
	}
	// The build's own repository is the bake stage BakedDir and the smoke read a
	// definition by, and it is the last one: the build's arguments name that
	// repository and no other's (imagebake.Append).
	if baked, ok := imagebake.BakedDir([]byte(got)); !ok || baked != appDir {
		t.Errorf("the copy bakes at %q, %v; want the build's own repository last at %s", baked, ok, appDir)
	}
	// The entry's other repository is the stage beside it, and that stage spells
	// its own clone URL: the build holds no commit, no branch and no argument of
	// a repository it does not run for.
	if !strings.Contains(got, "https://github.com/acme/lib.git") {
		t.Errorf("the copy has no stage cloning the second repository from its own URL:\n%s", got)
	}
	// The operator is told which repositories the image serves, each with the
	// directory its checkout is at: "bake your repos" reads like the whole
	// project, and this build's map puts two of them on this entry.
	if want := "bakes the checkouts of " + second + " at " + libDir + ", acme/app at " + appDir; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want the bake line %q", stderr, want)
	}
	// What the build pins and tags is the copy it wrote, both stages and all:
	// the record and the nightly rebuild both read this one object.
	if subs := catalogBuildSubs(t, fb); subs["_CATALOG_TAG"] != sha256Hex([]byte(got)) {
		t.Errorf("_CATALOG_TAG = %v, want the composed definition's sha256 %s", subs["_CATALOG_TAG"], sha256Hex([]byte(got)))
	}

	// The credential for both repositories, which is the service's answer and
	// not this build's: the entry mode asks the installation's credentials
	// service for one credential over the entry being built, and the service
	// reads the repositories a build may be given from the published image map
	// for that entry (B1, rule 8).
	gitCredentialEnv(t)
	// What ships reads the service's address out of the environment, and the
	// developer's own must not be the one this test calls.
	t.Setenv(envCredentialsServiceURL, "")
	route := newBuildRoute(t, projectpolicy.ImageMap{"acme/app": "tools", second: "tools"})
	out := filepath.Join(t.TempDir(), "git-credentials")
	stdout, stderr, err := execute(t, "image", "git-credential", "--provider", gitprov.KindGitHub,
		"--entry", "tools", "--build-id", buildE2EBuildID, "--ttl-seconds", "900",
		"--repos", "acme/app", "--repos", second, "--out", out)
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("the credential step printed %q / %q: its output is a build log, and a token is not for one", stdout, stderr)
	}
	// The credential the service minted, written for the one host the answer
	// names: the file is what the build's clone reads, and nothing of the
	// request is in it.
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://x-access-token:build-token-not-a-real-credential@github.com\n"; string(data) != want {
		t.Errorf("the credential file holds %q, want %q", data, want)
	}
	fi, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("the credential file's mode is %v, want 0600: it is a credential the build clones with", fi.Mode().Perm())
	}
	// The build presented its own identity: the token the metadata server minted
	// for the service's own URL, which is what Cloud Run IAM admits the call with
	// and the whole of what the route reads the caller's claims for. It is minted
	// per call rather than cached, and the audience is the service's address and
	// nothing this process chose.
	if got, want := route.presented(), []string{buildE2EIdentityToken}; !slices.Equal(got, want) {
		t.Errorf("the route was presented %v, want the metadata server's token %v", got, want)
	}
	if got, want := route.metadataAudiences(), []string{route.url}; !slices.Equal(got, want) {
		t.Errorf("the build minted its identity for %v, want the service's own address %v", got, want)
	}
	// What the service issued is audited (C1.13): one line naming the project's
	// build identity as its caller and the build route's GitHub purpose, and no
	// run, no job and no repository — a build holds no run identity, is a job of
	// no repository, and an issuance over several repositories that named one of
	// them would say it was for that one.
	lines := route.auditLines(t)
	if len(lines) != 1 {
		t.Fatalf("the service audited %d acts, want the one issuance: %v", len(lines), lines)
	}
	for field, want := range map[string]any{
		"caller":     buildE2EIdentity,
		"credential": config.GitHubAppKeySecret,
		"purpose":    credsvc.PurposeBuildToken,
		"status":     credsvc.StatusOK,
		"run":        "",
		"repository": "",
		"job":        "",
	} {
		if got := lines[0][field]; got != want {
			t.Errorf("the audit line's %s = %v, want %v", field, got, want)
		}
	}

	// A repository the entry's map does not put on it is refused by the service,
	// by name, and nothing is written: this build's one credential reaches this
	// entry's repositories and no others, and a step that widened the request, or
	// fell back to a credential of its own, would be the reach this contract
	// exists to bound (F19, rule 8).
	t.Run("a repository the entry's map does not put on it", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "git-credentials")
		_, _, err := execute(t, "image", "git-credential", "--provider", gitprov.KindGitHub,
			"--entry", "tools", "--build-id", buildE2EBuildID, "--ttl-seconds", "900",
			"--repos", "acme/app", "--repos", "acme/other", "--out", out)
		if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "acme/other") {
			t.Errorf("exit %d, err %v: want the service's refusal, naming the repository", ExitCode(err), err)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("a credential file was written for a request the service refused")
		}
		// The refusal is the SERVICE's, which is what makes the two assertions
		// above statements about the shipped path: a step that refused this
		// repository itself, or that never reached the route, would leave the
		// service with nothing to record.
		refusals := route.auditLines(t)
		if len(refusals) != 2 {
			t.Fatalf("the service audited %d acts, want the issuance and the refusal: %v", len(refusals), refusals)
		}
		if got := refusals[1]["status"]; got != credsvc.ErrorRequestRefused {
			t.Errorf("the refusal's audit line says %v, want %s", got, credsvc.ErrorRequestRefused)
		}
		if got := refusals[1]["caller"]; got != buildE2EIdentity {
			t.Errorf("the refusal's audit line names the caller %v, want %s", got, buildE2EIdentity)
		}
	})
}

// buildRoute is the installation's credentials service as a catalog build
// reaches it: the real service on a loopback listener, the metadata stand-in the
// build's identity token is minted at, and the fakes one call of the build route
// needs.
type buildRoute struct {
	// url is the service's own address: what a build mints its identity token for
	// and the value the step reads out of its environment. checkBuildServiceURL
	// admits http on loopback for exactly this reason, so the deployment's rule
	// (https) is unchanged.
	url string
	// verifier is the caller class the route verifies, recording the token each
	// call presented.
	verifier *buildRouteVerifier
	// audit is where the service's audited acts are written, as the JSON objects
	// it writes them as (C1.13).
	audit *buildRouteLog

	mu        sync.Mutex
	audiences []string
}

// newBuildRoute starts the service over the imageMap the published policy holds
// and points the build's environment at it: the service's address, the metadata
// stand-in and the App ID. The App key is the service's own and is read out of
// its bundle — the step holds the id and never the key.
func newBuildRoute(t *testing.T, imageMap projectpolicy.ImageMap) *buildRoute {
	t.Helper()
	gh := httpfixture.Serve(t, cliTestdataPath(t, "github_build_token_two_repos.json"))
	r := &buildRoute{verifier: &buildRouteVerifier{caller: buildE2EIdentity}, audit: &buildRouteLog{}}
	meta := httptest.NewServer(http.HandlerFunc(r.serveMetadata))
	t.Cleanup(meta.Close)
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	// The installation's repository table, in the key space the route reads a
	// repository in (buildEntryRepos): the slug of a repository's name against
	// the canonical name the published policy speaks.
	repos := make(map[string]string, len(imageMap))
	for name := range imageMap {
		canonical, err := projectpolicy.Canonical(gitprov.KindGitHub, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		slug, err := task.Slug(gitprov.KindGitHub, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		repos[slug] = canonical
	}
	srv, err := credsvc.NewServer(credsvc.Config{
		Auth:    r.verifier,
		Bundles: &credsvc.BundleLoader{Source: buildRouteBundle{doc: buildRouteBundleDoc(t)}},
		Policy:  buildRoutePolicy{imageMap: imageMap},
		// One database serves the state and the slips, as a deployment's does,
		// and the two stores are literals inside this call for a reason
		// internal/creds' boundary test states: a store this package binds to a
		// name is a store it can claim through, so a creds store is admitted
		// here only where it leaves the file, handed to the service.
		State:       &creds.RTDBStore{DB: client},
		Kills:       buildRouteKills{},
		JobAccounts: []credsvc.JobAccount{{Email: buildE2EIdentity, Slug: mustSlug(gitprov.KindGitHub, "acme/app"), Repo: "acme/app"}},
		Repos:       repos,
		Client:      &http.Client{Transport: mintFixtureTransport{base: gh}},
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:          client,
		Probes:      client,
		Slips:       &creds.RTDBStore{DB: client},
		AuditLog:    slog.NewJSONHandler(r.audit, nil),
		// BaseURL is the Bitbucket half's — the address a grant points the
		// build's git at — and this entry's repositories are GitHub's.
	})
	if err != nil {
		t.Fatalf("building the credentials service: %v", err)
	}
	service := httptest.NewServer(srv)
	t.Cleanup(service.Close)
	r.url = service.URL
	t.Setenv(envCredentialsServiceURL, service.URL)
	t.Setenv(buildMetadataHostEnv, strings.TrimPrefix(meta.URL, "http://"))
	t.Setenv(gitCredAppIDEnv, catalogBuildAppID)
	return r
}

// serveMetadata stands in for the platform's metadata server, which is where the
// build's identity token is minted and the one place it may be read from
// (GCE_METADATA_HOST points the step here). It answers the platform's own
// endpoint and nothing else, so a step that asked for another one, or stopped
// sending the header the server requires of a caller, fails rather than being
// answered something.
func (r *buildRoute) serveMetadata(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.audiences = append(r.audiences, req.URL.Query().Get("audience"))
	r.mu.Unlock()
	if req.Header.Get("Metadata-Flavor") != "Google" || req.URL.Path != buildMetadataIdentityPath {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	_, _ = io.WriteString(w, buildE2EIdentityToken)
}

// presented is the token every call of the route was handed, in order.
func (r *buildRoute) presented() []string { return r.verifier.presented() }

// metadataAudiences is the audience of every metadata request the step made.
func (r *buildRoute) metadataAudiences() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.audiences...)
}

// auditLines is the service's audit lines, parsed as the JSON objects they are,
// so an assertion reads a field rather than a substring of a line.
func (r *buildRoute) auditLines(t *testing.T) []map[string]any {
	t.Helper()
	raw := r.audit.text()
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	lines := make([]map[string]any, 0, 1)
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("the service's audit line %q is not the JSON object it writes: %v", line, err)
		}
		lines = append(lines, fields)
	}
	return lines
}

// buildRouteLog is a log buffer the test reads while the service's own goroutine
// writes it. The issuance is audited before the answer is written and the client
// reads the answer, so the read below follows the write in time — and the lock is
// what makes that a fact rather than a hope.
type buildRouteLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *buildRouteLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *buildRouteLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// buildRouteVerifier is the caller class the build route verifies. A deployment
// builds it out of Google's own key set (C1.10's Authenticator, which
// internal/credsvc's own test covers); what this test needs of it is the one
// thing the route asks — which class the caller is — and the token it was
// handed, so that what the step presented is pinned rather than assumed. The
// service's other classes are not this test's and are refused.
type buildRouteVerifier struct {
	caller string

	mu    sync.Mutex
	asked []string
}

// errNoBuildRoute refuses every class this test's service does not serve.
var errNoBuildRoute = errors.New("this test's credentials service serves the build route alone")

func (v *buildRouteVerifier) VerifyBuildCaller(_ context.Context, tok string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.asked = append(v.asked, tok)
	return v.caller, nil
}

func (v *buildRouteVerifier) presented() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.asked...)
}

func (v *buildRouteVerifier) Verify(context.Context, credsvc.Request) (credsvc.Identity, error) {
	return credsvc.Identity{}, errNoBuildRoute
}

func (v *buildRouteVerifier) VerifyVerifier(context.Context, string) (string, error) {
	return "", errNoBuildRoute
}

func (v *buildRouteVerifier) VerifyReadCaller(context.Context, string) (string, error) {
	return "", errNoBuildRoute
}

func (v *buildRouteVerifier) VerifyPersonCaller(context.Context, string) (string, error) {
	return "", errNoBuildRoute
}

func (v *buildRouteVerifier) VerifyOrchestrator(context.Context, string) (string, error) {
	return "", errNoBuildRoute
}

func (v *buildRouteVerifier) VerifyLauncher(context.Context, string) (credsvc.LauncherCaller, error) {
	return credsvc.LauncherCaller{}, errNoBuildRoute
}

func (v *buildRouteVerifier) VerifyRepoBuild(context.Context, string) (credsvc.RepoBuildAccount, error) {
	return credsvc.RepoBuildAccount{}, errNoBuildRoute
}

// buildRoutePolicy is the published policy the route narrows a call against: the
// image map, which is where the entry's repositories come from (rule 8, D1906).
// The build route reads the shared node and no repository's own.
type buildRoutePolicy struct{ imageMap projectpolicy.ImageMap }

func (p buildRoutePolicy) Shared(context.Context) (*projectpolicy.Policy, bool, error) {
	return &projectpolicy.Policy{ImageMap: p.imageMap}, true, nil
}

func (p buildRoutePolicy) Admissions(context.Context, string) (*projectpolicy.RepoPolicy, bool, error) {
	return nil, false, nil
}

// buildRouteKills is the operator's stop switches, both off: a stopped service
// issues nothing, which is not what this test is about.
type buildRouteKills struct{}

func (buildRouteKills) Stop(context.Context, string) (bool, string, error) { return false, "", nil }

// buildRouteBundle is the one bundle document the service reads: the GitHub
// App's key, which is the whole of what the route's GitHub half needs and the
// one credential the service holds itself.
type buildRouteBundle struct{ doc []byte }

func (b buildRouteBundle) Latest(context.Context) ([]byte, error) { return b.doc, nil }

// buildRouteBundleDoc is that document: the App key under the name the resolver
// answers the kind by, and nothing else. The key is generated by the test
// (appKeyPEM), so no test file holds one.
func buildRouteBundleDoc(t *testing.T) []byte {
	t.Helper()
	key := "  " + strings.ReplaceAll(strings.TrimRight(appKeyPEM(t), "\n"), "\n", "\n  ")
	return []byte(config.GitHubAppKeySecret + ": |\n" + key + "\n")
}

// cliTestdataPath is a file in this package's testdata, resolved by this file's
// own location rather than by the working directory: the checkout fixture chdirs
// into a temporary repository (checkoutWith), where "testdata" names that
// checkout's own directory and not this package's.
func cliTestdataPath(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("this test file's own path cannot be read")
	}
	return filepath.Join(filepath.Dir(file), "testdata", name)
}

// mintFixtureTransport sends the service's GitHub calls to the fixture replaying
// them and refuses every other destination. The service builds
// https://api.github.com itself — no configuration and no caller can name another
// host — so the destination is asserted here rather than assumed, and a test
// whose service reached anywhere else fails instead of leaving the process.
type mintFixtureTransport struct{ base *httpfixture.Server }

func (h mintFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.github.com" {
		return nil, fmt.Errorf("the service sent a mint request to %s, and a mint reaches api.github.com alone", r.URL)
	}
	base, err := url.Parse(h.base.URL)
	if err != nil {
		return nil, err
	}
	c := r.Clone(r.Context())
	c.URL.Scheme, c.URL.Host = base.Scheme, base.Host
	c.Host = base.Host
	return http.DefaultTransport.RoundTrip(c)
}
