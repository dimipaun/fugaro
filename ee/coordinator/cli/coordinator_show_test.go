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
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
)

// The four tests below are `coordinator show`'s (coordinator v2 C3, D816): one
// pull request as the host has it, read through the coordinator's own read-only
// installation token and nothing else.
//
//   - TestCoordinatorShowPrintsTheHostsBody drives the command's own wiring —
//     no opener and no mint replaced — against a hermetic host, and reads the
//     document back as data. The fixture is the whole assertion: the mint's
//     request body is the two-read set and the pull-request read carries that
//     token's own Authorization header, so a read authenticated with the run's
//     set (two writes among them) fails on the fixture's own check.
//   - TestCoordinatorShowRefusesUnderTheCredentialsService is the property
//     reconciliation F9 says this read inherits: where the installation's
//     credentials service holds the App's key, this command holds none and
//     refuses, naming the service. It is the half a later change could
//     silently lose.
//   - TestCoordinatorShowRefusesAPullRequestTheHostDoesNotHave: a pull request
//     the host does not have is a refusal carrying the host's own status, and
//     never an empty document.
//   - TestCoordinatorShowRefusesAMalformedArgumentBeforeAnyRead: a --repo or
//     --pr that is not one is refused before the host is reached at all.

// showFixtureEnv is the environment every test here reads through: the App's
// ID and a fresh key (the mint's whole input), the credentials service off (the
// key is this process's), and the pinned GitHub root pointed at srv.
func showFixtureEnv(t *testing.T, root string) func([]string, func(string)) (coordinator.ProviderOpener, error) {
	t.Helper()
	realOpen := openCoordinatorProvider
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	t.Setenv(providers.EnvCredentialsServiceURL, "")
	// The read mint's own variable is the one thing that turns a Bitbucket
	// repository from a refusal into a read (BB4), so every test in this file
	// states which case it is: the two that refuse one below are refusals only
	// while nothing published a service URL, and a developer whose shell has
	// one exported must not change what they assert.
	t.Setenv(envBitbucketReadServiceURL, "")
	setCoordinatorGitHubRoot(t, root)
	return realOpen
}

func TestCoordinatorShowPrintsTheHostsBody(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_show.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the document is not JSON (%v):\n%s", err, out)
	}
	for key, want := range map[string]any{
		"number": float64(1458),
		"url":    "https://github.com/acme/app/pull/1458",
		"title":  "Add search",
		"state":  "open",
		"draft":  true,
		"branch": "topic/search",
		"base":   "main",
		"author": "1001",
	} {
		if doc[key] != want {
			t.Errorf("the document's %s is %#v, want %#v:\n%s", key, doc[key], want, out)
		}
	}
	body, _ := doc["body"].(string)
	if !strings.Contains(body, "Adds a search box to the header.") {
		t.Errorf("the document does not carry the host's body: %#v\n%s", doc["body"], out)
	}

	// The document is the host's answer and nothing else: every key it carries
	// is one of the shape's, and a field the host did not answer is absent
	// rather than empty (PRDocument).
	want := []string{"author", "base", "body", "branch", "draft", "number", "state", "title", "url"}
	got := make([]string, 0, len(doc))
	for key := range doc {
		got = append(got, key)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("the document carries %v, want the host's own answer %v", got, want)
	}

	// The installation token the read was authenticated with is the one value
	// this path holds, and it reaches no output (the rule every read of the
	// coordinator's keeps).
	if strings.Contains(out, "ghs_fixture") {
		t.Errorf("the document carries the installation token:\n%s", out)
	}
}

// The same document, drawn for a person: the command prints the host's words
// as text and never as a link or an element, and the body is printed through
// the sanitiser that strips what a terminal would act on.
func TestCoordinatorShowPrintsTheHostsBodyAsText(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_show.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	for _, want := range []string{"acme/app#1458", "Add search", "topic/search", "Adds a search box to the header."} {
		if !strings.Contains(out, want) {
			t.Errorf("the text output does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ghs_fixture") {
		t.Errorf("the text output carries the installation token:\n%s", out)
	}
}

// The refusal reconciliation F9 says this read inherits, and the one a later
// change could lose without noticing: with the installation's credentials
// service in the environment, the App's key is the service's and this command
// holds none, so it refuses (exit 1) naming the service — before it reads the
// App's ID, opens a provider or mints anything. The empty fixture is what makes
// that last half a claim rather than a reading of the code: a request that
// arrived would fail the test rather than the network being asked.
func TestCoordinatorShowRefusesUnderTheCredentialsService(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_no_requests.json"))
	realOpen := showFixtureEnv(t, srv.URL)
	t.Setenv(providers.EnvCredentialsServiceURL, "https://credentials.example")

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	out, _, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err == nil {
		t.Fatalf("the credentials service holds the App's key and the command read one anyway: %s", out)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("the service holds the key: exit %d, want %d: the environment the caller is running in is the caller's to fix",
			fcli.ExitCode(err), fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), providers.EnvCredentialsServiceURL) {
		t.Errorf("the service holds the key: %v, want a failure naming %s", err, providers.EnvCredentialsServiceURL)
	}
	if out != "" {
		t.Errorf("a refused read printed a document: %q", out)
	}
}

// A pull request the host does not have is a refusal carrying the host's own
// status, and never an empty document: an empty one would draw a pull request
// with no description, which is a different fact from "the host has no such
// pull request".
func TestCoordinatorShowRefusesAPullRequestTheHostDoesNotHave(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_show_missing.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	out, _, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "9999", "--json")
	if err == nil {
		t.Fatalf("the host has no pull request 9999 and the command printed one: %s", out)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("the refusal is %v, want the host's own status in it", err)
	}
	if fcli.ExitCode(err) != fcli.ExitRemoteError {
		t.Errorf("exit %d, want %d: the host refused the read, and that is the host's failure",
			fcli.ExitCode(err), fcli.ExitRemoteError)
	}
	if out != "" {
		t.Errorf("a refused read printed a document: %q", out)
	}
}

// A repository on a host this read does not serve is refused with the
// coordinator's own reason and never silently, and the provider is read from
// the repository's own config entry (C1's field, as the stored view carries it).
// The empty fixture is the other half: nothing is opened and nothing is minted
// for a repository whose read is refused before it starts.
func TestCoordinatorShowRefusesABitbucketRepository(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_no_requests.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	f := newCoordinatorFixture(t, nil)
	f.appendConfig(t, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")
	setCoordinatorHost(t, realOpen)

	_, _, err := execute(t, "coordinator", "show", "--repo", "acme/legacy", "--pr", "12", "--json")
	if err == nil {
		t.Fatal("a bitbucket repository was read through the coordinator's github credential")
	}
	if fcli.ExitCode(err) != fcli.ExitUserError || !strings.Contains(err.Error(), "cannot be widened to a project") {
		t.Errorf("a bitbucket repository: exit %d, %v", fcli.ExitCode(err), err)
	} else if strings.Contains(err.Error(), "legacy") {
		t.Errorf("the refusal names the repository rather than the reason: %v", err)
	}
}

// An argument that is not one is refused before the host is reached: the empty
// fixture would fail on any request, so what this holds is that no read is made
// for a --repo that is not owner/name or a --pr that is not a pull request
// number.
func TestCoordinatorShowRefusesAMalformedArgumentBeforeAnyRead(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_no_requests.json"))
	realOpen := showFixtureEnv(t, srv.URL)

	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	for _, args := range [][]string{
		{"coordinator", "show", "--repo", "acme", "--pr", "1458", "--json"},
		{"coordinator", "show", "--repo", "acme/app/extra", "--pr", "1458", "--json"},
		{"coordinator", "show", "--repo", "acme/app", "--json"},
		{"coordinator", "show", "--repo", "acme/app", "--pr", "0", "--json"},
		{"coordinator", "show", "--repo", "acme/app", "--pr", "-1", "--json"},
		{"coordinator", "show", "--repo", "acme/app", "--pr", "not-a-number", "--json"},
	} {
		out, _, err := execute(t, args...)
		if err == nil {
			t.Errorf("%v was accepted: %s", args, out)
			continue
		}
		if fcli.ExitCode(err) != fcli.ExitUserError {
			t.Errorf("%v: exit %d, want %d: the argv is the caller's to fix", args, fcli.ExitCode(err), fcli.ExitUserError)
		}
		if out != "" {
			t.Errorf("%v printed a document: %q", args, out)
		}
	}
}

// BB4 adds four to `coordinator show`'s set (the four above): the same read,
// on a Bitbucket repository, through the installation's credentials service
// and no credential of this command's own.
//
//   - TestCoordinatorShowBitbucketThroughService drives the whole chain with
//     both of its ends fake and neither of its wiring replaced: the command,
//     the project-wide read, the real bitbucket adapter and the real read-mint
//     client, against a hermetic credentials service and a hermetic API. What
//     the fake API sees is the token the fake service minted, which is what
//     makes "the read is authenticated with the minted token and with nothing
//     else" a reading rather than a claim.
//   - TestCoordinatorShowGitHubUnchanged: with no service URL anywhere, a
//     GitHub read is the read it was before BB4, and a Bitbucket repository is
//     the refusal it was (G8).
//   - TestCoordinatorShowGitHubUnchangedWithReadServiceURL: the person's own
//     variable does not touch the GitHub path, which is the whole reason it is
//     not FUGARO_CREDENTIALS_SERVICE_URL (F2, F23).
//   - TestServicePathRefusesAPIRootOverride: with FUGARO_BITBUCKET_API_URL set,
//     the provider is still opened on the root this code pins, and the minted
//     token goes to that root and never to the override (G10, T2.5).

// readMintServer is the credentials service's read mint as this command sees
// it: one route, one bearer identity per call, one repository named in the
// body, and the answer shape the contract has. Its token is built here and
// reaches no file, no log and no assertion's failure text.
type readMintServer struct {
	srv *httptest.Server
	// token and expires are what the service hands back; the repository is
	// echoed from the request, so a caller that asked for one and was answered
	// for another is a case this fake cannot hide.
	token   string
	expires time.Time

	mu     sync.Mutex
	bodies []string
	idents []string
}

func newReadMintServer(t *testing.T) *readMintServer {
	t.Helper()
	m := &readMintServer{token: "fake-read-token-not-a-credential", expires: time.Now().Add(2 * time.Hour)}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != bitbucket.ServiceReadPath {
			t.Errorf("the read mint was asked for %s, want %s", r.URL.Path, bitbucket.ServiceReadPath)
		}
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.bodies = append(m.bodies, string(body))
		m.idents = append(m.idents, r.Header.Get("Authorization"))
		m.mu.Unlock()
		var req struct {
			Repository string `json:"repository"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      m.token,
			"expires_at": m.expires.UTC().Format(time.RFC3339Nano),
			"repository": req.Repository,
		})
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// asked is the bodies the service was asked with, in order, and idents is the
// Authorization header of each.
func (m *readMintServer) asked() (bodies, idents []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.bodies), slices.Clone(m.idents)
}

// bitbucketAPI is Bitbucket's own end, as far as one pull request read reaches
// it: the path, the bearer token, and the document.
type bitbucketAPI struct {
	srv *httptest.Server

	mu     sync.Mutex
	paths  []string
	tokens []string
	// stray is every request for something other than the pull-request read,
	// which is how a test notices the adapter reaching for more than one call.
	stray []string
}

// newBitbucketAPI answers one pull request's read for workspace/slug/number.
func newBitbucketAPI(t *testing.T, workspace, slug string, number int) *bitbucketAPI {
	t.Helper()
	a := &bitbucketAPI{}
	want := "/2.0/repositories/" + workspace + "/" + slug + "/pullrequests/" + strconv.Itoa(number)
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		a.mu.Lock()
		a.paths = append(a.paths, r.URL.Path)
		a.tokens = append(a.tokens, auth)
		if r.URL.Path != want {
			a.stray = append(a.stray, r.URL.Path)
		}
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          number,
			"title":       "Add search",
			"description": "Adds a search box to the header.",
			"state":       "OPEN",
			"draft":       false,
			"links":       map[string]any{"html": map[string]any{"href": "https://bitbucket.org/" + workspace + "/" + slug + "/pull-requests/" + strconv.Itoa(number)}},
			"author":      map[string]any{"account_id": "account-1"},
			"source":      map[string]any{"branch": map[string]any{"name": "topic/search"}},
			"destination": map[string]any{"branch": map[string]any{"name": "main"}},
		})
	}))
	t.Cleanup(a.srv.Close)
	return a
}

// bitbucketReadFixture is the environment one Bitbucket read is made in: the
// App credential the opener is always built on (this command builds one before
// it knows which host it is reading, and the Bitbucket path then holds none of
// it), the credentials service off for the GitHub half, and the read mint's own
// variable pointed at the fake service.
func bitbucketReadFixture(t *testing.T, mint *readMintServer) {
	t.Helper()
	t.Setenv(providers.EnvGitHubAppID, "12345")
	t.Setenv(providers.EnvGitHubAppKey, appKeyPEM(t))
	t.Setenv(providers.EnvCredentialsServiceURL, "")
	t.Setenv(envBitbucketReadServiceURL, mint.srv.URL)
}

// personIdentity is the caller's own Google ID token as this command mints it:
// a value the fake service can recognise, and the one thing that proves the
// read presented the person and not a run identity or the App's key.
const personIdentity = "fake-identity-token-not-a-credential"

// setBitbucketSeams replaces the two seams the Bitbucket path reaches the
// outside world through: the caller's identity, and the adapter the provider is
// opened as. The adapter is the REAL one — the fake API stands in for
// bitbucket.org by rewriting the ONE field a test may rewrite, and the Options
// as the command built them are handed back for the test to read.
func setBitbucketSeams(t *testing.T, api *bitbucketAPI) *bitbucket.Options {
	t.Helper()
	seen := &bitbucket.Options{}
	prevID, prevProvider := coordinatorBitbucketIDToken, coordinatorBitbucketProvider
	coordinatorBitbucketIDToken = func(context.Context) (string, error) { return personIdentity, nil }
	coordinatorBitbucketProvider = func(o bitbucket.Options) (gitprov.Provider, error) {
		*seen = o
		o.Warn = nil
		if api != nil {
			o.BaseURL = api.srv.URL + "/2.0"
		}
		return bitbucket.New(o)
	}
	t.Cleanup(func() { coordinatorBitbucketIDToken, coordinatorBitbucketProvider = prevID, prevProvider })
	return seen
}

// TestCoordinatorShowBitbucketThroughService: a person's `coordinator show` on
// a Bitbucket repository reads it through the installation's credentials
// service, and every link of that chain is held here — the repository the
// command names is the one repository the mint is asked for, the identity it
// presents is the person's own and not a run identity, the token the service
// mints is the token the API sees, and none of it reaches the output.
func TestCoordinatorShowBitbucketThroughService(t *testing.T) {
	mint := newReadMintServer(t)
	api := newBitbucketAPI(t, "acme", "legacy", 12)
	bitbucketReadFixture(t, mint)
	seen := setBitbucketSeams(t, api)

	f := newCloudFixture(t)
	f.appendConfig(t, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/legacy", "--pr", "12", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the document is not JSON (%v):\n%s", err, out)
	}
	for key, want := range map[string]any{
		"number": float64(12),
		"url":    "https://bitbucket.org/acme/legacy/pull-requests/12",
		"title":  "Add search",
		"state":  "open",
		"branch": "topic/search",
		"base":   "main",
		"author": "account-1",
	} {
		if doc[key] != want {
			t.Errorf("the document's %s is %#v, want %#v:\n%s", key, doc[key], want, out)
		}
	}
	if body, _ := doc["body"].(string); !strings.Contains(body, "Adds a search box to the header.") {
		t.Errorf("the document does not carry the host's body: %#v\n%s", doc["body"], out)
	}
	// A pull request the host does not call a draft: the field the document
	// omits (PRDocument.Draft is omitempty), so what is read is that it is not
	// there rather than that it is false.
	if d, ok := doc["draft"]; ok && d != false {
		t.Errorf("the document calls a ready pull request a draft: %#v", d)
	}

	// The mint: one call, for the one repository the command was asked about,
	// in the spelling the installation uses.
	bodies, idents := mint.asked()
	if len(bodies) != 1 {
		t.Fatalf("the read mint was called %d times for one read, want one: %v", len(bodies), bodies)
	}
	if want := `{"repository":"acme/legacy"}`; bodies[0] != want {
		t.Errorf("the read mint was asked for %s, want %s", bodies[0], want)
	}
	// The person, presenting their own identity and no run identity: the mint
	// admits a person by that token and refuses a caller that arrives as a job
	// (G4), so a header here that is not this caller's own is the whole
	// failure the class exists to prevent.
	if idents[0] != "Bearer "+personIdentity {
		t.Errorf("the read mint was presented %q, want the caller's own identity", idents[0])
	}
	// And the read itself: the API saw the token the service minted, as a
	// bearer, on the one call a pull-request read makes.
	paths, tokens := api.paths, api.tokens
	if len(paths) != 1 || paths[0] != "/2.0/repositories/acme/legacy/pullrequests/12" {
		t.Fatalf("the API was asked for %v, want one pull-request read", paths)
	}
	if tokens[0] != "Bearer "+mint.token {
		t.Errorf("the API was authenticated with %q, want the token the service minted", tokens[0])
	}
	if len(api.stray) != 0 {
		t.Errorf("the read reached %v beyond the pull request it asked for", api.stray)
	}
	// The provider is opened on the root this code pins, whatever the
	// environment carries (G10): the test rewrites the field afterwards, so
	// what it reads here is what the command passed.
	if seen.BaseURL != bitbucket.DefaultBaseURL {
		t.Errorf("the provider was opened on %q, want %s", seen.BaseURL, bitbucket.DefaultBaseURL)
	}
	if seen.Workspace != "acme" || seen.Slug != "legacy" {
		t.Errorf("the provider was opened on %s/%s, want the repository the command named", seen.Workspace, seen.Slug)
	}
	if seen.Token != "" {
		t.Errorf("the provider was opened with a token of its own: the read is minted, not held")
	}
	// Neither credential — the minted token or the identity that asked for it
	// — reaches the output.
	for _, secret := range []string{mint.token, personIdentity} {
		if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
			t.Errorf("the read printed a credential:\n%s", out)
		}
	}
}

// TestCoordinatorShowGitHubUnchanged: with no service URL published anywhere,
// nothing about a GitHub read changes (G8) and a Bitbucket repository is
// refused exactly as it was before BB4.
func TestCoordinatorShowGitHubUnchanged(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_show.json"))
	realOpen := showFixtureEnv(t, srv.URL)
	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	f := newCloudFixture(t)
	f.appendConfig(t, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err != nil {
		t.Fatalf("a github read without the service: %v (stderr: %s)", err, errOut)
	}
	if !strings.Contains(out, `"title": "Add search"`) {
		t.Errorf("the github read is not the one it was:\n%s", out)
	}

	// The other half of G8: without the service, Bitbucket is refused with the
	// project-wide read's own reason and not read at all.
	_, _, err = execute(t, "coordinator", "show", "--repo", "acme/legacy", "--pr", "12", "--json")
	if err == nil {
		t.Fatal("a bitbucket repository was read with no credentials service published")
	}
	if !errors.Is(err, coordinator.ErrBitbucket) && !strings.Contains(err.Error(), "cannot be widened to a project") {
		t.Errorf("the bitbucket refusal is %v, want the project-wide read's own reason", err)
	}
}

// TestCoordinatorShowGitHubUnchangedWithReadServiceURL: the person's own
// variable is not FUGARO_CREDENTIALS_SERVICE_URL, and this is the property
// that makes the difference worth having — setting it turns the Bitbucket read
// on and leaves the GitHub one exactly where it was (F2, F23).
func TestCoordinatorShowGitHubUnchangedWithReadServiceURL(t *testing.T) {
	mint := newReadMintServer(t)
	srv := httpfixture.Serve(t, filepath.Join("testdata", "coordinator_show.json"))
	realOpen := showFixtureEnv(t, srv.URL)
	t.Setenv(envBitbucketReadServiceURL, mint.srv.URL)
	newCoordinatorFixture(t, nil)
	setCoordinatorHost(t, realOpen)

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err != nil {
		t.Fatalf("the read mint's own variable turned the github read off: %v (stderr: %s)", err, errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the document is not JSON (%v):\n%s", err, out)
	}
	if doc["title"] != "Add search" || doc["number"] != float64(1458) {
		t.Errorf("the github read is not the one it was:\n%s", out)
	}
	// And the service was not asked for anything: this read is GitHub's, and
	// its credential is the App's.
	if bodies, _ := mint.asked(); len(bodies) != 0 {
		t.Errorf("the read mint was called for a github read: %v", bodies)
	}
}

// TestServicePathRefusesAPIRootOverride: FUGARO_BITBUCKET_API_URL cannot move
// where a service-minted token is sent (G10, T2.5). The variable is set for the
// whole test and a server is listening on it, so the assertion is about where
// the call went and not about what the code says.
func TestServicePathRefusesAPIRootOverride(t *testing.T) {
	mint := newReadMintServer(t)
	api := newBitbucketAPI(t, "acme", "legacy", 12)

	var overridden int
	override := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		overridden++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer override.Close()
	t.Setenv(providers.EnvBitbucketAPIURL, override.URL)

	bitbucketReadFixture(t, mint)
	t.Setenv(providers.EnvBitbucketAPIURL, override.URL)
	seen := setBitbucketSeams(t, api)

	f := newCloudFixture(t)
	f.appendConfig(t, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/legacy", "--pr", "12", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	if !strings.Contains(out, "Add search") {
		t.Errorf("the read did not answer:\n%s", out)
	}
	if seen.BaseURL != bitbucket.DefaultBaseURL {
		t.Errorf("the provider was opened on %q, want the root this code pins (%s)", seen.BaseURL, bitbucket.DefaultBaseURL)
	}
	if overridden != 0 {
		t.Errorf("the read reached the overridden API root %d times", overridden)
	}
	if _, tokens := api.paths, api.tokens; len(tokens) != 1 || tokens[0] != "Bearer "+mint.token {
		t.Errorf("the minted token went somewhere unexpected: %v", api.tokens)
	}
	// The other half of the pin, and the one the seam above could hide: the
	// environment a provider is opened with carries the pinned root and not
	// the launcher's, so nothing that reads the environment can be pointed
	// elsewhere by it either.
	env := coordinatorProviderEnv(os.Environ())
	for _, kv := range env {
		if strings.HasPrefix(kv, providers.EnvBitbucketAPIURL+"=") && kv != providers.EnvBitbucketAPIURL+"="+bitbucket.DefaultBaseURL {
			t.Errorf("the provider's environment carries %s, want the pinned root", kv)
		}
	}
}
