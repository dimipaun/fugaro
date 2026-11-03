// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
)

// `coordinator show` is a PERSON's read (the owner's ruling of 2026-11-03), and
// these tests are written to that class: the installation token comes from the
// credentials service the project's own configuration publishes, the App's key
// stays at the service, and this process reads no environment variable to find
// either.
//
//   - TestCoordinatorShowPrintsTheHostsBody drives the whole chain — the
//     command, the project-wide read, the real read-mint client, the real
//     GitHub adapter — with both of its ends fake and none of its wiring
//     replaced, and reads the document back as data.
//   - TestCoordinatorShowPrintsTheHostsBodyAsText: the same document drawn for
//     a person.
//   - TestCoordinatorShowReadsBothHostsWithNoVariablesSet is the ruling held as
//     one test: with every variable this read could reach for cleared, a GitHub
//     pull request and a Bitbucket one are both read through the service the
//     project published.
//   - TestCoordinatorShowNeverReadsTheRunsServiceURL: FUGARO_CREDENTIALS_SERVICE_URL
//     is the RUN's name for the service (F2, F23) and it decides nothing here —
//     it neither moves the read off the project's own service nor turns a
//     project that publishes none into one that reads.
//   - TestCoordinatorShowRefusesWithoutAService: the other half of the same
//     rule — a project that publishes no credentials_service block has no host
//     read at all, and the refusal is the caller's.
//   - TestCoordinatorShowRefusesAPullRequestTheHostDoesNotHave: a pull request
//     the host does not have is a refusal carrying the host's own status, and
//     never an empty document.
//   - TestCoordinatorShowRefusesAMalformedArgumentBeforeAnyRead: a --repo or
//     --pr that is not one is refused before the host is reached at all.
//   - TestCoordinatorShowPrintsNoEscapeFromTheHostsText: the sanitiser, at the
//     point a person's terminal is the surface — the host's escapes are gone
//     from the text and still in the document.
//   - TestCoordinatorShowNeverPrintsTheTokenTheHostEchoed: the redaction, on
//     the class where the token never passes the reader's hands — the source
//     the provider was opened with is what knows it (gitprov.TokenHolder).
//   - TestCoordinatorShowNeverReadsTheAppCredential: the App's ID and key are a
//     run's and the service's, and a person's read neither uses them nor refuses
//     over them — the test a mutant that reaches for them dies on.
//   - TestCoordinatorShowBitbucketThroughService and
//     TestServicePathRefusesAPIRootOverride: BB4's Bitbucket half, on the same
//     class and now off the same source as the GitHub one.

// personIdentity is the caller's own Google ID token as this command mints it:
// a value the fake service can recognise, and the one thing that proves the
// read presented the person and not a run identity or the App's key.
const personIdentity = "fake-identity-token-not-a-credential"

// readMintServer is the credentials service's read mint as this command sees
// it: the routes a test allows, one bearer identity per call, one repository
// named in the body, and the answer shape the contract has. Its token is built
// here and reaches no file, no log and no assertion's failure text.
//
// It is a TLS server, and it has to be: a project's published URL is https by
// validation (localcfg.validCredentialsServiceURL), so the fake a test
// publishes there is one no machine trusts and the command is handed its client
// as the tests' own seam (coordinatorServiceClient).
type readMintServer struct {
	srv *httptest.Server
	// expires is what the service hands back with every token.
	expires time.Time

	mu     sync.Mutex
	paths  []string
	bodies []string
	idents []string
	// refusal, when set, is what every call is answered with instead of a
	// token: the service's own wire shape (credsvc's WireError), an error word
	// and the status beside it.
	refusal *readMintRefusal
}

// readMintRefusal is a fake service's refusal: the code the client is meant to
// carry up and the status it arrives with.
type readMintRefusal struct {
	code   string
	status int
}

// refuse makes every later call answer with this service's own refusal rather
// than a token. It is set before the command runs, and it is guarded by the
// same lock the handler takes, so a test that sets it and a read that reads it
// never race.
func (m *readMintServer) refuse(t *testing.T, code string, status int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refusal = &readMintRefusal{code: code, status: status}
}

// newReadMintServer starts a fake read mint that serves the routes named — one
// for a test about a single host, two for the test that reads both — and fails
// the test on any other path, because a read that asked the wrong route is a
// read that reached a mint the installation does not have.
func newReadMintServer(t *testing.T, paths ...string) *readMintServer {
	t.Helper()
	m := &readMintServer{expires: time.Now().Add(2 * time.Hour)}
	m.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(paths, r.URL.Path) {
			t.Errorf("the read mint was asked for %s, want one of %v", r.URL.Path, paths)
		}
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.paths = append(m.paths, r.URL.Path)
		m.bodies = append(m.bodies, string(body))
		m.idents = append(m.idents, r.Header.Get("Authorization"))
		refusal := m.refusal
		m.mu.Unlock()
		var req struct {
			Repository string `json:"repository"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		if refusal != nil {
			w.WriteHeader(refusal.status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":   refusal.code,
				"message": "the repository is not in the workspace this service's read consumer is scoped to",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      m.tokenFor(req.Repository),
			"expires_at": m.expires.UTC().Format(time.RFC3339Nano),
			"repository": req.Repository,
		})
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// asked is the routes, bodies and identities the service was asked with, in
// order.
func (m *readMintServer) asked() (paths, bodies, idents []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.paths), slices.Clone(m.bodies), slices.Clone(m.idents)
}

// client is the client that trusts this fake's certificate, and the value the
// tests hand the command as its client seam.
func (m *readMintServer) client() *http.Client { return m.srv.Client() }

// tokenFor is the installation token this fake mints for repo. It is a
// function of the repository because the contract is: the service mints for ONE
// repository per call and the answer names it, so a fake that handed one token
// to every repository could not tell a read that asked for the wrong one — and
// "the API saw the token minted for the repository it read" is exactly the
// assertion the tests below want to make.
func (m *readMintServer) tokenFor(repo string) string {
	return "fake-read-token-not-a-credential-" + strings.ReplaceAll(repo, "/", "_")
}

// githubAPI is GitHub's own end, as far as one pull request read reaches it:
// the path, the bearer token, and the document — the same answer
// testdata/coordinator_show.json records, so what the adapter parses here is
// what it parses against the recorded exchange.
type githubAPI struct {
	srv *httptest.Server
	// seen is the Options the command built for the adapter, before this fake
	// rewrites the one field a test may rewrite.
	seen *github.Options

	mu     sync.Mutex
	paths  []string
	tokens []string
	// stray is every request for something other than the pull-request read,
	// which is how a test notices the read reaching for more than one call.
	stray []string
	// status and doc, when status is set, are what the pull-request read is
	// answered with instead of the ordinary document: how a test drives a host
	// that refuses, or one whose answer carries bytes a terminal would act on.
	status int
	doc    map[string]any
}

// newGitHubAPI answers one pull request's read for owner/repo/number.
func newGitHubAPI(t *testing.T, owner, repo string, number int) *githubAPI {
	t.Helper()
	a := &githubAPI{seen: &github.Options{}}
	want := "/repos/" + owner + "/" + repo + "/pulls/" + strconv.Itoa(number)
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		a.mu.Lock()
		a.paths = append(a.paths, r.URL.Path)
		a.tokens = append(a.tokens, auth)
		if r.URL.Path != want {
			a.stray = append(a.stray, r.URL.Path)
		}
		status, doc := a.status, a.doc
		a.mu.Unlock()
		if r.URL.Path != want {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(doc)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":   number,
			"state":    "open",
			"merged":   false,
			"draft":    true,
			"html_url": "https://github.com/" + owner + "/" + repo + "/pull/" + strconv.Itoa(number),
			"node_id":  "PR_kwDOA" + strconv.Itoa(number),
			"title":    "Add search",
			"body":     "Adds a search box to the header.\n\nCloses #1200.",
			"user":     map[string]any{"login": "person-1", "id": 1001, "type": "User"},
			"head":     map[string]any{"ref": "topic/search", "sha": "0123456789abcdef0123456789abcdef01234567", "repo": map[string]any{"full_name": owner + "/" + repo}},
			"base":     map[string]any{"ref": "main"},
		})
	}))
	t.Cleanup(a.srv.Close)
	return a
}

// answers makes this fake answer the pull-request read with status and doc
// instead of the ordinary document. It is set before the command runs, and it
// is written under the same lock the handler reads it under, so a test that
// sets it and a read that reads it never race.
func (a *githubAPI) answers(status int, doc map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status, a.doc = status, doc
}

// setPersonSeams replaces the three seams a person's GitHub read reaches the
// outside world through — the caller's identity, the adapter the provider is
// opened as, and the client a read mint's call is made with — and returns the
// Options the command built for the adapter, so a test reads back what the
// command passed rather than what the fake API then received.
//
// The adapter is the REAL one, pointed at the fake API by rewriting the one
// field a test may rewrite. The identity is the person's, and it is the person
// the service verifies (G4): a read that presented a job's identity would be
// admitted under the wrong class, and this is how that is noticed rather than
// read about.
func setPersonSeams(t *testing.T, api *githubAPI, client *http.Client) *github.Options {
	t.Helper()
	prevAPI, prevClient := coordinatorGitHubReadProvider, coordinatorServiceClient
	setPersonIdentity(t)
	coordinatorGitHubReadProvider = func(o github.Options) (gitprov.Provider, error) {
		*api.seen = o
		o.Warn = nil
		o.BaseURL = api.srv.URL
		return github.New(o)
	}
	coordinatorServiceClient = func() *http.Client { return client }
	t.Cleanup(func() {
		coordinatorGitHubReadProvider, coordinatorServiceClient = prevAPI, prevClient
	})
	return api.seen
}

// setPersonIdentity replaces the person's own identity — their Google ID token,
// minted from their own credentials — with the fixture's stand-in. No test
// machine has a person's credentials, and the service's person class verifies
// exactly this value (G4), so a read that presented anything else would be
// admitted under the wrong class rather than quietly pass.
func setPersonIdentity(t *testing.T) {
	t.Helper()
	prev := coordinatorPersonIdentity
	coordinatorPersonIdentity = func(context.Context, string) (string, error) { return personIdentity, nil }
	t.Cleanup(func() { coordinatorPersonIdentity = prev })
}

// personShowFixture is the fixture one person's `coordinator show` is made in:
// the project's own published credentials service at mint's URL, the App ID the
// fixture's config records for acme/app, and nothing in the environment naming
// either.
//
// repos are further entries of the config's own repos mapping, appended before
// the service is published: the block is the last thing the config carries, so
// a line added after it would land inside the block's own mapping.
func personShowFixture(t *testing.T, mint *readMintServer, repos ...string) *cloudFixture {
	t.Helper()
	f := newCloudFixture(t)
	for _, r := range repos {
		f.appendConfig(t, r)
	}
	f.publishService(t, mint.srv.URL)
	return f
}

func TestCoordinatorShowPrintsTheHostsBody(t *testing.T) {
	mint := newReadMintServer(t, github.ServiceReadPath)
	api := newGitHubAPI(t, "acme", "app", 1458)
	seen := setPersonSeams(t, api, mint.client())
	personShowFixture(t, mint)

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

	// The mint: one call, on the read mint's own route, for the one repository
	// the command was asked about and as the App the project records for it.
	// The App ID is the field a person's read has to supply — the service's
	// environment does not hold it — and it comes from the project's
	// configuration and never from FUGARO_GITHUB_APP_ID.
	paths, bodies, idents := mint.asked()
	if len(paths) != 1 || paths[0] != github.ServiceReadPath {
		t.Fatalf("the read mint was asked on %v, want one call on %s", paths, github.ServiceReadPath)
	}
	if want := `{"repository":"acme/app","app_id":"` + appIDFixture + `"}`; bodies[0] != want {
		t.Errorf("the read mint was asked for %s, want %s", bodies[0], want)
	}
	if idents[0] != "Bearer "+personIdentity {
		t.Errorf("the read mint was presented %q, want the caller's own identity", idents[0])
	}

	// The provider the command opened: the repository the caller named, the App
	// the project records, the key held by the service and not by this process,
	// and the API root this code pins (D474) rather than one the environment
	// could name.
	if seen.Owner != "acme" || seen.Repo != "app" {
		t.Errorf("the provider was opened on %s/%s, want the repository the caller named", seen.Owner, seen.Repo)
	}
	if seen.AppID != appIDFixture {
		t.Errorf("the provider was opened as App %q, want the project's own recorded %q", seen.AppID, appIDFixture)
	}
	if !seen.ServiceHeldKey {
		t.Error("the provider was opened with a key of its own: a person's machine holds no GitHub App key")
	}
	if seen.PrivateKey != nil {
		t.Error("the provider was opened with a private key: the App's key is the service's and this process holds none")
	}
	if seen.BaseURL != github.DefaultBaseURL {
		t.Errorf("the provider was opened on %q, want the pinned %s", seen.BaseURL, github.DefaultBaseURL)
	}

	// And the read itself: the API saw the token the service minted, as a
	// bearer, on the one call a pull-request read makes.
	apiPaths, tokens := api.paths, api.tokens
	if len(apiPaths) != 1 || apiPaths[0] != "/repos/acme/app/pulls/1458" {
		t.Fatalf("the API was asked for %v, want one pull-request read", apiPaths)
	}
	if tokens[0] != "Bearer "+mint.tokenFor("acme/app") {
		t.Errorf("the API was authenticated with %q, want the token the service minted", tokens[0])
	}
	if len(api.stray) != 0 {
		t.Errorf("the read reached %v beyond the pull request it asked for", api.stray)
	}

	// Neither credential — the minted token or the identity that asked for it —
	// reaches the output.
	for _, secret := range []string{mint.tokenFor("acme/app"), personIdentity} {
		if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
			t.Errorf("the read printed a credential:\n%s", out)
		}
	}
}

// The same document, drawn for a person: the command prints the host's words
// as text and never as a link or an element, and the body is printed through
// the sanitiser that strips what a terminal would act on.
func TestCoordinatorShowPrintsTheHostsBodyAsText(t *testing.T) {
	mint := newReadMintServer(t, github.ServiceReadPath)
	api := newGitHubAPI(t, "acme", "app", 1458)
	setPersonSeams(t, api, mint.client())
	personShowFixture(t, mint)

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	for _, want := range []string{"acme/app#1458", "Add search", "topic/search", "Adds a search box to the header."} {
		if !strings.Contains(out, want) {
			t.Errorf("the text output does not carry %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{mint.tokenFor("acme/app"), personIdentity} {
		if strings.Contains(out, secret) {
			t.Errorf("the text output carries a credential:\n%s", out)
		}
	}
}

// A person's read never asks for the App credential, and this is the test the
// mutant is killed by: the environment here carries the App's ID and a key that
// is not a key, which is a credential coordinatorAppCredential refuses over
// (github.ParsePrivateKey, exit 1). The read below succeeds anyway, so a
// person's path that called it — to use it, or merely to check for it — fails
// here rather than in a review.
//
// The App's key is the strongest credential the installation has and a person's
// machine holds none of it: the service holds it, and what a person's read
// carries instead is the App's ID, which is not a secret and comes from the
// project's own configuration (coordinatorAppID).
func TestCoordinatorShowNeverReadsTheAppCredential(t *testing.T) {
	mint := newReadMintServer(t, github.ServiceReadPath)
	api := newGitHubAPI(t, "acme", "app", 1458)
	setPersonSeams(t, api, mint.client())
	personShowFixture(t, mint)
	// Set AFTER the fixture, so nothing the fixture does can clear them: these
	// are the values the read would meet if it looked.
	t.Setenv(providers.EnvGitHubAppID, "987654")
	t.Setenv(providers.EnvGitHubAppKey, "not-an-app-key")

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err != nil {
		t.Fatalf("a person's read met the App credential: %v (stderr: %s)", err, errOut)
	}
	if !strings.Contains(out, `"title": "Add search"`) {
		t.Errorf("the read did not answer with the host's document:\n%s", out)
	}
	// The service is where the token came from, so the read is the one this
	// test means and not one that never happened.
	if paths, _, _ := mint.asked(); len(paths) != 1 || paths[0] != github.ServiceReadPath {
		t.Errorf("the read mint was asked on %v, want one call on %s", paths, github.ServiceReadPath)
	}
	// And the key the environment carries reaches nothing this command prints:
	// a read that refused over it would be the mutant, and a read that reported
	// it would be worse.
	for _, secret := range []string{"987654", "not-an-app-key", personIdentity} {
		if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
			t.Errorf("the read printed a credential:\n%s%s", out, errOut)
		}
	}
}

// The owner's ruling of 2026-11-03, held as one test: with every variable this
// read could reach for cleared, a person's `coordinator show` resolves the
// service from the project's own published configuration — for a GitHub pull
// request and for a Bitbucket one — and both reads succeed against the fake
// service that URL names.
//
// The two hosts are one installation and one service here, which is what they
// are: the block publishes one URL, and both read mints are served by it. What
// the cleared variables prove is the other half — nothing in the environment
// supplies the service, the App ID, the App key or either API root, so a
// machine that exports nothing reads exactly as one that exports everything.
func TestCoordinatorShowReadsBothHostsWithNoVariablesSet(t *testing.T) {
	for _, v := range []string{
		providers.EnvGitHubAppID,
		providers.EnvGitHubAppKey,
		providers.EnvGitHubAppKeyFile,
		providers.EnvCredentialsServiceURL,
		providers.EnvGitHubAPIURL,
		providers.EnvBitbucketAPIURL,
		providers.EnvBitbucketToken,
		envCoordinatorCredentialsServiceURL,
	} {
		t.Setenv(v, "")
	}

	mint := newReadMintServer(t, github.ServiceReadPath, bitbucket.ServiceReadPath)
	ghAPI := newGitHubAPI(t, "acme", "app", 1458)
	bbAPI := newBitbucketAPI(t, "acme", "legacy", 12)
	setPersonSeams(t, ghAPI, mint.client())
	setBitbucketSeams(t, bbAPI)

	personShowFixture(t, mint, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err != nil {
		t.Fatalf("the github read with no variable set: %v (stderr: %s)", err, errOut)
	}
	if !strings.Contains(out, `"title": "Add search"`) {
		t.Errorf("the github read did not answer with the host's document:\n%s", out)
	}

	bbOut, bbErr, err := execute(t, "coordinator", "show", "--repo", "acme/legacy", "--pr", "12", "--json")
	if err != nil {
		t.Fatalf("the bitbucket read with no variable set: %v (stderr: %s)", err, bbErr)
	}
	if !strings.Contains(bbOut, `"title": "Add search"`) {
		t.Errorf("the bitbucket read did not answer with the host's document:\n%s", bbOut)
	}

	// Both reads went to the one service the configuration publishes, each on
	// its own host's route: the service is the installation's, and which mint a
	// repository is read through is the host's fact and not a second URL.
	paths, bodies, idents := mint.asked()
	if !slices.Equal(paths, []string{github.ServiceReadPath, bitbucket.ServiceReadPath}) {
		t.Errorf("the read mint was asked on %v, want the github route then the bitbucket one", paths)
	}
	if want := []string{
		`{"repository":"acme/app","app_id":"` + appIDFixture + `"}`,
		`{"repository":"acme/legacy"}`,
	}; !slices.Equal(bodies, want) {
		t.Errorf("the read mint was asked for %v, want %v", bodies, want)
	}
	for _, id := range idents {
		if id != "Bearer "+personIdentity {
			t.Errorf("the read mint was presented %q, want the caller's own identity", id)
		}
	}
	// And each host's API saw the token the service minted, not one of the
	// other read's.
	if tokens := ghAPI.tokens; len(tokens) != 1 || tokens[0] != "Bearer "+mint.tokenFor("acme/app") {
		t.Errorf("the github API was authenticated with %v, want the token the service minted", tokens)
	}
	if tokens := bbAPI.tokens; len(tokens) != 1 || tokens[0] != "Bearer "+mint.tokenFor("acme/legacy") {
		t.Errorf("the bitbucket API was authenticated with %v, want the token the service minted", tokens)
	}
}

// FUGARO_CREDENTIALS_SERVICE_URL is the RUN's own name for the credentials
// service (F2, F23) and it decides nothing about a person's read — not where
// the read goes, and not whether it happens at all.
//
// The first half is the property the configuration-first order buys: a person's
// machine that happens to carry the run's variable reads the service the
// PROJECT published and never the one the variable names, so the person's own
// Google ID token — a bearer credential — is presented at one host and one host
// only. The second half is the same rule from the other side: the variable does
// not turn a project that publishes no service into one that reads, because a
// person's read has no other credential to fall back to.
func TestCoordinatorShowNeverReadsTheRunsServiceURL(t *testing.T) {
	// The service the run's variable names: it serves both routes and must
	// never be called.
	runsMint := newReadMintServer(t, github.ServiceReadPath, bitbucket.ServiceReadPath)
	t.Setenv(providers.EnvCredentialsServiceURL, runsMint.srv.URL)

	mint := newReadMintServer(t, github.ServiceReadPath)
	api := newGitHubAPI(t, "acme", "app", 1458)
	setPersonSeams(t, api, mint.client())
	personShowFixture(t, mint)

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	if !strings.Contains(out, `"title": "Add search"`) {
		t.Errorf("the read did not answer:\n%s", out)
	}
	if paths, bodies, _ := runsMint.asked(); len(paths) != 0 || len(bodies) != 0 {
		t.Errorf("the read minted at the URL %s names, which is the run's and not the project's: %v",
			providers.EnvCredentialsServiceURL, bodies)
	}
	if paths, _, _ := mint.asked(); len(paths) != 1 {
		t.Errorf("the project's own service was asked %v, want one read mint", paths)
	}

	// The second half: a project that publishes no block reads nothing, and the
	// run's variable does not stand in for one.
	newCloudFixture(t)
	_, _, err = execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err == nil {
		t.Fatal("the run's service URL turned a person's read on")
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("a project with no service: exit %d, want %d", fcli.ExitCode(err), fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), "credentials service") {
		t.Errorf("the refusal = %v, want it to name the credentials service", err)
	}
	if paths, _, _ := mint.asked(); len(paths) != 1 {
		t.Errorf("a project with no service minted anyway: %v", paths)
	}
	if paths, _, _ := runsMint.asked(); len(paths) != 0 {
		t.Errorf("a project with no service minted at the run's variable: %v", paths)
	}
}

// A project that publishes no credentials_service block has no host read at
// all. A person's machine holds no GitHub App key (the owner's ruling of
// 2026-11-03), so there is no credential to fall back to, and the refusal is
// the caller's (exit 1) with the one thing that fixes it named.
func TestCoordinatorShowRefusesWithoutAService(t *testing.T) {
	mint := newReadMintServer(t, github.ServiceReadPath, bitbucket.ServiceReadPath)
	api := newGitHubAPI(t, "acme", "app", 1458)
	setPersonSeams(t, api, mint.client())

	f := newCloudFixture(t)
	f.appendConfig(t, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")

	out, _, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err == nil {
		t.Fatalf("a github read with no service published succeeded: %s", out)
	}
	if fcli.ExitCode(err) != fcli.ExitUserError {
		t.Errorf("no service: exit %d, want %d: the project's configuration is the caller's to fix",
			fcli.ExitCode(err), fcli.ExitUserError)
	}
	if !strings.Contains(err.Error(), "credentials service") {
		t.Errorf("the refusal = %v, want it to name the credentials service", err)
	}
	if out != "" {
		t.Errorf("a refused read printed a document: %q", out)
	}

	// The Bitbucket half of the same project: the project-wide read's own
	// refusal, which names the reason and never the repository.
	_, _, err = execute(t, "coordinator", "show", "--repo", "acme/legacy", "--pr", "12", "--json")
	if err == nil {
		t.Fatal("a bitbucket repository was read with no service published")
	}
	if fcli.ExitCode(err) != fcli.ExitUserError || !strings.Contains(err.Error(), "cannot be widened to a project") {
		t.Errorf("a bitbucket repository with no service: exit %d, %v", fcli.ExitCode(err), err)
	} else if strings.Contains(err.Error(), "legacy") {
		t.Errorf("the refusal names the repository rather than the reason: %v", err)
	}

	// Nothing was minted and nothing was read: the fakes would have failed the
	// test rather than the network being asked.
	if paths, _, _ := mint.asked(); len(paths) != 0 {
		t.Errorf("a read with no service minted anyway: %v", paths)
	}
	if len(api.paths) != 0 {
		t.Errorf("a read with no service reached the API: %v", api.paths)
	}
}

// A pull request the host does not have is a refusal carrying the host's own
// status, and never an empty document: an empty one would draw a pull request
// with no description, which is a different fact from "the host has no such
// pull request".
func TestCoordinatorShowRefusesAPullRequestTheHostDoesNotHave(t *testing.T) {
	mint := newReadMintServer(t, github.ServiceReadPath)
	api := newGitHubAPI(t, "acme", "app", 1458)
	setPersonSeams(t, api, mint.client())
	personShowFixture(t, mint)

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

// An argument that is not one is refused before the host is reached: the fakes
// would fail on any request, so what this holds is that no read is made for a
// --repo that is not owner/name or a --pr that is not a pull request number.
func TestCoordinatorShowRefusesAMalformedArgumentBeforeAnyRead(t *testing.T) {
	mint := newReadMintServer(t, github.ServiceReadPath, bitbucket.ServiceReadPath)
	api := newGitHubAPI(t, "acme", "app", 1458)
	setPersonSeams(t, api, mint.client())
	personShowFixture(t, mint)

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
	if paths, _, _ := mint.asked(); len(paths) != 0 {
		t.Errorf("a malformed argument minted anyway: %v", paths)
	}
	if len(api.paths) != 0 {
		t.Errorf("a malformed argument reached the host: %v", api.paths)
	}
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

// setBitbucketSeams replaces the adapter the Bitbucket path is opened as. The
// adapter is the REAL one — the fake API stands in for bitbucket.org by
// rewriting the ONE field a test may rewrite, and the Options as the command
// built them are handed back for the test to read.
//
// There is no identity seam here any more: a Bitbucket read's identity is the
// person's, minted once for the class (coordinatorPersonIdentity), and it is
// the same value the GitHub read presents — which is the point of the class
// rather than a coincidence.
func setBitbucketSeams(t *testing.T, api *bitbucketAPI) *bitbucket.Options {
	t.Helper()
	seen := &bitbucket.Options{}
	prevProvider := coordinatorBitbucketProvider
	coordinatorBitbucketProvider = func(o bitbucket.Options) (gitprov.Provider, error) {
		*seen = o
		o.Warn = nil
		if api != nil {
			o.BaseURL = api.srv.URL + "/2.0"
		}
		return bitbucket.New(o)
	}
	t.Cleanup(func() { coordinatorBitbucketProvider = prevProvider })
	return seen
}

// TestCoordinatorShowBitbucketThroughService: a person's `coordinator show` on
// a Bitbucket repository reads it through the installation's credentials
// service, and every link of that chain is held here — the repository the
// command names is the one repository the mint is asked for, the identity it
// presents is the person's own and not a run identity, the token the service
// mints is the token the API sees, and none of it reaches the output.
func TestCoordinatorShowBitbucketThroughService(t *testing.T) {
	mint := newReadMintServer(t, bitbucket.ServiceReadPath)
	api := newBitbucketAPI(t, "acme", "legacy", 12)
	setPersonSeams(t, newGitHubAPI(t, "acme", "app", 1458), mint.client())
	seen := setBitbucketSeams(t, api)

	personShowFixture(t, mint, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")

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

	// The mint: one call, on the Bitbucket read mint's own route, for the one
	// repository the command was asked about, in the spelling the installation
	// uses.
	paths, bodies, idents := mint.asked()
	if len(paths) != 1 || paths[0] != bitbucket.ServiceReadPath {
		t.Fatalf("the read mint was asked on %v, want one call on %s", paths, bitbucket.ServiceReadPath)
	}
	if want := `{"repository":"acme/legacy"}`; bodies[0] != want {
		t.Errorf("the read mint was asked for %s, want %s", bodies[0], want)
	}
	// The person, presenting their own identity and no run identity: the mint
	// admits a person by that token and refuses a caller that arrives as a job
	// (G4), so a header here that is not this caller's own is the whole failure
	// the class exists to prevent.
	if idents[0] != "Bearer "+personIdentity {
		t.Errorf("the read mint was presented %q, want the caller's own identity", idents[0])
	}
	// And the read itself: the API saw the token the service minted, as a
	// bearer, on the one call a pull-request read makes.
	apiPaths, tokens := api.paths, api.tokens
	if len(apiPaths) != 1 || apiPaths[0] != "/2.0/repositories/acme/legacy/pullrequests/12" {
		t.Fatalf("the API was asked for %v, want one pull-request read", apiPaths)
	}
	if tokens[0] != "Bearer "+mint.tokenFor("acme/legacy") {
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
	for _, secret := range []string{mint.tokenFor("acme/legacy"), personIdentity} {
		if strings.Contains(out, secret) || strings.Contains(errOut, secret) {
			t.Errorf("the read printed a credential:\n%s", out)
		}
	}
}

// TestCoordinatorShowPrintsNoEscapeFromTheHostsText: the description and the
// title are `coordinator show`'s two fields carrying a stranger's bytes, and
// the text output is the surface that draws them for a person. The fake host's
// title and body each carry a screen-clear (ESC [ 2J) and an OSC-8 hyperlink
// (ESC ] 8 ; ; … BEL) — a sequence a terminal acts on and a reader cannot see —
// so what this holds is the sanitiser's contract at the point it is reached: no
// ESC and no BEL anywhere in the output, and the host's readable words still
// there. The second half is why: a command that printed nothing would satisfy
// the first on its own, and a reader that sanitised the document instead of the
// line would satisfy it by inventing a field (§6.15 — the document is the
// host's own answer).
func TestCoordinatorShowPrintsNoEscapeFromTheHostsText(t *testing.T) {
	const (
		clear    = "\x1b[2J"
		link     = "\x1b]8;;https://evil.example\x07click\x1b]8;;\x07"
		title    = "Add search" + clear + link
		bodyText = "Adds a search box to the header." + clear + "\n\nCloses #1200.\n\n" + link
	)

	mint := newReadMintServer(t, github.ServiceReadPath)
	api := newGitHubAPI(t, "acme", "app", 1458)
	api.answers(http.StatusOK, map[string]any{
		"number": 1458, "state": "open", "merged": false, "draft": true,
		"html_url": "https://github.com/acme/app/pull/1458", "node_id": "PR_kwDOA1458",
		"title": title, "body": bodyText,
		"user": map[string]any{"login": "person-1", "id": 1001, "type": "User"},
		"head": map[string]any{"ref": "topic/search", "sha": "0123456789abcdef0123456789abcdef01234567", "repo": map[string]any{"full_name": "acme/app"}},
		"base": map[string]any{"ref": "main"},
	})
	setPersonSeams(t, api, mint.client())
	personShowFixture(t, mint)

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458")
	if err != nil {
		t.Fatalf("%v (stderr: %s)", err, errOut)
	}
	for _, bad := range []struct {
		name string
		r    rune
	}{{"ESC", 0x1b}, {"BEL", 0x07}} {
		if i := strings.IndexRune(out, bad.r); i >= 0 {
			t.Errorf("the text output carries %s at byte %d, so the host's own bytes reached the terminal:\n%q", bad.name, i, out)
		}
	}
	for _, want := range []string{"Add search", "Adds a search box to the header.", "Closes #1200."} {
		if !strings.Contains(out, want) {
			t.Errorf("the text output does not carry %q, so the sanitiser dropped the host's words and not only its escapes:\n%q", want, out)
		}
	}
	if strings.Contains(out, mint.tokenFor("acme/app")) {
		t.Errorf("the text output carries the installation token:\n%q", out)
	}

	// The document is the other half of the boundary: it is the host's answer
	// as data, and the escapes are still in it. A decoder at the far edge
	// sanitises for its own surface; the CLI's text renderer is the one that
	// does it here, and a rewrite of the field would be a field this read
	// invented.
	docOut, _, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(docOut), &doc); err != nil {
		t.Fatalf("the document is not JSON (%v):\n%s", err, docOut)
	}
	for _, field := range []struct{ name, value string }{{"title", doc.Title}, {"body", doc.Body}} {
		if !strings.ContainsRune(field.value, 0x1b) || !strings.ContainsRune(field.value, 0x07) {
			t.Errorf("the document's %s = %q, want the host's own bytes: the read sanitised the data rather than the line", field.name, field.value)
		}
	}
}

// TestCoordinatorShowNeverPrintsTheTokenTheHostEchoed: the failure path of the
// single read, end to end, on the class that makes it now. The fake host answers
// the pull-request read with a 500 whose body quotes the Authorization header
// back — the shape a proxy or a debugging host really produces — so the text the
// provider hands the reader carries the installation token.
//
// The token is the one the CREDENTIALS SERVICE minted, and that is the half this
// test exists for: the person's reader holds no App key and mints nothing
// itself, so the only thing that knows the value is the source the provider was
// opened with (gitprov.TokenHolder) — a redactor built from the opener's secrets
// alone would miss it. The assertion is the command's whole output and the
// failure it reports: the token in neither, the marker in the failure, and the
// host's status as the exit code.
func TestCoordinatorShowNeverPrintsTheTokenTheHostEchoed(t *testing.T) {
	mint := newReadMintServer(t, github.ServiceReadPath)
	api := newGitHubAPI(t, "acme", "app", 1458)
	// The token the service mints is known before the read: it is a function of
	// the repository, which is what lets the fake host quote it back.
	api.answers(http.StatusInternalServerError, map[string]any{
		"message": "Internal Server Error (Authorization: token " + mint.tokenFor("acme/app") + ")",
	})
	setPersonSeams(t, api, mint.client())
	personShowFixture(t, mint)

	out, errOut, err := execute(t, "coordinator", "show", "--repo", "acme/app", "--pr", "1458", "--json")
	if err == nil {
		t.Fatalf("the host answered 500 and the command printed a document: %s", out)
	}
	for _, got := range []string{out, errOut, err.Error()} {
		if strings.Contains(got, mint.tokenFor("acme/app")) {
			t.Errorf("the installation token the host echoed reached the output: %q", got)
		}
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("the failure is %v, want the token redacted in it", err)
	}
	if fcli.ExitCode(err) != fcli.ExitRemoteError {
		t.Errorf("exit %d, want %d: the host refused the read, and that is the host's failure", fcli.ExitCode(err), fcli.ExitRemoteError)
	}
}

// TestServicePathRefusesAPIRootOverride: FUGARO_BITBUCKET_API_URL cannot move
// where a service-minted token is sent (G10, T2.5). The variable is set for the
// whole test and a server is listening on it, so the assertion is about where
// the call went and not about what the code says.
func TestServicePathRefusesAPIRootOverride(t *testing.T) {
	mint := newReadMintServer(t, bitbucket.ServiceReadPath)
	api := newBitbucketAPI(t, "acme", "legacy", 12)

	var overridden int
	override := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		overridden++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer override.Close()
	t.Setenv(providers.EnvBitbucketAPIURL, override.URL)

	setPersonSeams(t, newGitHubAPI(t, "acme", "app", 1458), mint.client())
	seen := setBitbucketSeams(t, api)

	personShowFixture(t, mint, "  acme/legacy: { provider: bitbucket, base_branch: main, workflows: [web] }\n")

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
	if tokens := api.tokens; len(tokens) != 1 || tokens[0] != "Bearer "+mint.tokenFor("acme/legacy") {
		t.Errorf("the minted token went somewhere unexpected: %v", tokens)
	}
}
