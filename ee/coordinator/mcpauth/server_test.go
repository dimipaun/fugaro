// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// testIssuer is the coordinator's own URL: the authorization server's
// identifier, the resource the tokens are minted for, and the origin every
// endpoint of this file is mounted on. It is a documentation address and not a
// real installation's.
const testIssuer = "https://coordinator.example.com"

// testClientRedirect is the redirect URI the tests register and ask with: a
// loopback address, which D928 always allows, so a test that is not about the
// allowlist does not have to be on it.
const testClientRedirect = "http://127.0.0.1:8976/callback"

// testAllowlistedRedirect is the hosted callback the project's own
// configuration allows (D928). It is on the allowlist below and nowhere else.
const testAllowlistedRedirect = "https://client.example.net/callback"

func testConfig() Config {
	return Config{
		Issuer:            testIssuer,
		RedirectAllowlist: []string{testAllowlistedRedirect},
		Limits:            Limits{Scopes: []string{ScopeTasksRead, ScopeTasksWrite}},
	}
}

// testServer is the whole authorization server over a bucket the fake serves,
// mounted on the service M4 creates — the shape every test in this package
// drives, because the endpoints are only reachable through it.
type testServer struct {
	*Server
	svc   *coordinator.Service
	store *fakeStore
	// counting is the store the server was handed, wrapped: the "nothing is
	// written outside the store" test reads what it saw.
	counting *countingStore
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	return newTestServerWith(t, testConfig())
}

func newTestServerWith(t *testing.T, cfg Config) *testServer {
	t.Helper()
	return newTestServerClock(t, cfg, func() time.Time { return testNow })
}

// newTestServerClock is newTestServerWith over a clock the caller owns. The two
// TTLs — the authorization request's and the code's — are bounds nothing but
// time reaches, so the tests that pin them move a clock instead of waiting for
// one; every other test takes the pinned one above.
func newTestServerClock(t *testing.T, cfg Config, now func() time.Time) *testServer {
	t.Helper()
	return newTestServerOver(t, cfg, now, nil)
}

// newTestServerOver is newTestServerClock with one more seam: wrap sits between
// the bucket and the record layer, so a test that needs a competing writer to
// land inside a store operation the server is running can interleave one. Every
// other test passes nil, and the server's store is the counting store over a
// plain bucket below it.
//
// The options are M7's addition and they are the same list NewServer takes: a
// server's member lists are an option rather than an argument (WithRoster), so
// a test of the MCP endpoint has to be able to pass one. No caller here passes
// any, and a server built without one admits nobody.
func newTestServerOver(t *testing.T, cfg Config, now func() time.Time, wrap func(objects) objects, opts ...ServerOption) *testServer {
	t.Helper()
	fs := newFakeStoreOver(t, wrap)
	counting := &countingStore{Store: fs.Store, keys: map[string]bool{}}
	v := verifierOver(t, staticKeys{testKid: &testKey().PublicKey})
	srv, err := NewServer(cfg, counting, v, append([]ServerOption{WithServerClock(now)}, opts...)...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	svc := coordinator.NewService()
	srv.Mount(svc)
	return &testServer{Server: srv, svc: svc, store: fs, counting: counting}
}

// countingStore is the store a test hands the server, recording every key a
// write named. It is how TestNoRecordIsWrittenOutsideTheStore tells a record
// that arrived through the interface from one that did not.
type countingStore struct {
	Store
	keys map[string]bool
}

func (c *countingStore) note(key string) { c.keys[key] = true }

func (c *countingStore) PutClient(ctx context.Context, cl Client) error {
	c.note(clientPrefix + cl.ID)
	return c.Store.PutClient(ctx, cl)
}

func (c *countingStore) PutToken(ctx context.Context, r TokenRecord) error {
	c.note(tokenPrefix + r.Hash)
	return c.Store.PutToken(ctx, r)
}

func (c *countingStore) PutFamily(ctx context.Context, f Family, gen int64) error {
	c.note(familyPrefix + f.ID)
	return c.Store.PutFamily(ctx, f, gen)
}

func (c *countingStore) PutRequest(ctx context.Context, r Request) error {
	c.note(requestPrefix + r.ID)
	return c.Store.PutRequest(ctx, r)
}

func (c *countingStore) PutCode(ctx context.Context, code Code) error {
	c.note(codePrefix + code.Hash)
	return c.Store.PutCode(ctx, code)
}

func (ts *testServer) do(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	ts.svc.Handler().ServeHTTP(rec, req)
	return rec
}

// get asks one of the server's own paths.
func (ts *testServer) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	return ts.do(t, httptest.NewRequest(http.MethodGet, path, nil))
}

// postForm posts a form to one of the server's paths.
func (ts *testServer) postForm(t *testing.T, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return ts.postFormWithAuth(t, path, form, "")
}

// postFormWithAuth posts a form with an Authorization header, which is how a
// client authenticates with client_secret_basic (RFC 6749 §2.3.1). The header
// is passed as its raw text so that a test can present one that is malformed.
func (ts *testServer) postFormWithAuth(t *testing.T, path string, form url.Values, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	return ts.do(t, req)
}

// basicAuth is the Authorization header a client_secret_basic client sends.
func basicAuth(id, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+secret))
}

// TestNoServerIsBuiltWithoutItsStoreItsVerifierOrAnOrigin is NewServer's three
// refusals, one per argument it cannot do its work without. Each is a
// deployment mistake rather than a request's: a server with no store has
// nowhere to write a client or a code, one with no verifier cannot answer the
// sign-in step, and one whose issuer is not an origin names endpoints nobody
// serves in the metadata documents it publishes. Each has to be caught where
// the server is built, because a server built anyway would report it as a
// refusal of somebody's request.
func TestNoServerIsBuiltWithoutItsStoreItsVerifierOrAnOrigin(t *testing.T) {
	store := newFakeStore(t).Store
	verifier := verifierOver(t, staticKeys{testKid: &testKey().PublicKey})

	for _, tc := range []struct {
		what     string
		cfg      Config
		store    Store
		verifier *Verifier
	}{
		{"no store", testConfig(), nil, verifier},
		{"no verifier", testConfig(), store, nil},
		{"an issuer that is not an origin", Config{Issuer: "http://coordinator.example.com"}, store, verifier},
	} {
		t.Run(tc.what, func(t *testing.T) {
			srv, err := NewServer(tc.cfg, tc.store, tc.verifier)
			if err == nil {
				t.Errorf("NewServer with %s = a server; want a refusal", tc.what)
			}
			if srv != nil {
				t.Errorf("NewServer refused %s and handed back a server anyway", tc.what)
			}
		})
	}

	// The same three arguments with nothing missing are built, so the refusals
	// above are three guards and not a constructor that refuses everything.
	if srv, err := NewServer(testConfig(), store, verifier); err != nil {
		t.Fatalf("NewServer with every argument: %v", err)
	} else if srv == nil {
		t.Error("NewServer returned no server and no error")
	}
}

// TestNoIssuerThatIsNotAnOriginIsAccepted is the guard over the issuer and the
// audience together, and it is the one that keeps the metadata documents
// resolvable: the issuer is the authorization server's identifier, the resource
// every token is minted for, and the origin each endpoint above is appended to.
// A value carrying a path, a query, a fragment or credentials would produce
// documents naming URLs nobody serves and an audience a client cannot check; a
// scheme other than https is refused for the reason D920 requires one, a bearer
// token over a public endpoint. The accepted values are here beside the refused
// ones, so a guard that refused everything would fail this test too.
func TestNoIssuerThatIsNotAnOriginIsAccepted(t *testing.T) {
	for _, tc := range []struct {
		what   string
		issuer string
		ok     bool
	}{
		{"an origin", "https://coordinator.example.com", true},
		{"an origin on a port", "https://coordinator.example.com:8443", true},
		{"a scheme that is not https", "http://coordinator.example.com", false},
		{"a trailing slash", "https://coordinator.example.com/", false},
		{"a path", "https://coordinator.example.com/oauth", false},
		{"a query", "https://coordinator.example.com?project=p", false},
		{"a fragment", "https://coordinator.example.com#fragment", false},
		{"credentials", "https://person@coordinator.example.com", false},
		{"no host", "https://", false},
		{"no scheme", "coordinator.example.com", false},
		{"an empty string", "", false},
		{"not a URL at all", "https://coordinator.example.com/\x7f", false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			err := issuerURL(tc.issuer)
			if tc.ok && err != nil {
				t.Errorf("issuerURL(%q) = %v; want it accepted", tc.issuer, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("issuerURL(%q) = nil; want it refused", tc.issuer)
			}
		})
	}
}

// TestNoTokenIsA401NamingTheMetadataURL is M5's red step. A client that reaches
// the MCP endpoint with no token has to be told where to go next: the answer is
// 401 with a WWW-Authenticate header naming the protected-resource metadata
// document (RFC 9728), and that document has to be served by this same server —
// a header pointing at a URL nobody answers is a client that cannot start the
// flow at all, which is the whole of D920's "the bearer token is the gate".
//
// It failed before the authorization server existed: the MCP endpoint answered
// 404, so there was no header and no metadata document to resolve it against.
func TestNoTokenIsA401NamingTheMetadataURL(t *testing.T) {
	ts := newTestServer(t)
	rec := ts.do(t, httptest.NewRequest(http.MethodPost, MCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("the MCP endpoint answered %d to a call with no bearer token, and the answer has to be 401: it is the first step of the flow (spec.md §2), and a 200 or a 404 leaves a client with nothing to do", rec.Code)
	}
	challenge := rec.Header().Get("WWW-Authenticate")
	if !strings.HasPrefix(challenge, "Bearer ") {
		t.Fatalf("the 401 carries WWW-Authenticate %q, which does not name a Bearer challenge: RFC 9728 puts the metadata document's URL in that header and nowhere else", challenge)
	}
	target := resourceMetadataURL(t, challenge)
	if want := testIssuer + ProtectedResourcePath; target != want {
		t.Errorf("the challenge names %q and this server's metadata document is at %q: a header naming another origin is a client sent to somebody else's server", target, want)
	}
	// The URL the header named has to resolve — on this server, at the path it
	// names, with a document that names this resource.
	rec = ts.get(t, ProtectedResourcePath)
	if rec.Code != http.StatusOK {
		t.Fatalf("the metadata document the 401 named answered %d: a client that follows the header has to find the document", rec.Code)
	}
	var doc protectedResource
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the metadata document is not the JSON RFC 9728 asks for: %v", err)
	}
	if doc.Resource != testIssuer {
		t.Errorf("the metadata document names the resource %q and this server's own URL is %q", doc.Resource, testIssuer)
	}
}

// resourceMetadataURL reads the resource_metadata parameter out of a
// WWW-Authenticate challenge.
func resourceMetadataURL(t *testing.T, challenge string) string {
	t.Helper()
	const param = `resource_metadata="`
	i := strings.Index(challenge, param)
	if i < 0 {
		t.Fatalf("the challenge %q carries no resource_metadata parameter: without it a client cannot find the document, which is the one thing the header is for", challenge)
	}
	rest := challenge[i+len(param):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("the challenge %q opens resource_metadata and never closes it", challenge)
	}
	return rest[:j]
}

// TestNoRecordIsWrittenOutsideTheStore is D935 held as a fact about this
// package rather than as a rule about it: every write the endpoints make
// arrives through M2's Store, and none of them opens a database or a bucket of
// its own.
//
// The whole surface is driven — a registration, an authorization, a consent, an
// exchange, a rotation and a revocation — and what is compared afterwards is
// the bucket itself against the keys the store the server was handed was asked
// to write. A record the endpoints wrote by some other route would be a key
// nothing in that set names, and the store the server holds is the only one it
// has: a second client to the store's own backend is what this test exists to
// make impossible to add quietly.
func TestNoRecordIsWrittenOutsideTheStore(t *testing.T) {
	ts := newTestServer(t)

	clientID, secret := ts.register(t, "an editor", testClientRedirect)
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code := codeOf(t, ts.approve(t, binding, number))
	rec, tokens := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("the code exchange answered %d (%s)", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	// The two writes that are not creates, so the comparison below covers them
	// as well: a rotation replaces a family and a revocation deletes one.
	if rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
	}); rec.Code != http.StatusOK {
		t.Fatalf("the refresh grant answered %d (%s)", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if rec := ts.postForm(t, RevokePath, url.Values{
		"token":         {tokens.AccessToken},
		"client_id":     {clientID},
		"client_secret": {secret},
	}); rec.Code != http.StatusOK {
		t.Fatalf("the revocation answered %d (%s); RFC 7009 answers 200 to a revocation", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	at := ts.store.objects(t)
	if len(at) == 0 {
		t.Fatal("the store holds nothing after a whole flow: the comparison below would pass while writing nowhere at all")
	}
	for key := range at {
		if !ts.counting.keys[key] {
			t.Errorf("%s is in the store and no call of the Store interface wrote it: every record these endpoints write goes through M2's Store, and nothing here names the bucket or the project's database (D935)", key)
		}
	}
	// And the flow really did reach every kind of record, so the check above is
	// looking at a store that holds what the endpoints write rather than at one
	// that happens to hold a client.
	for _, prefix := range []string{clientPrefix, tokenPrefix, requestPrefix, codePrefix} {
		found := false
		for key := range at {
			if strings.HasPrefix(key, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the store holds no record under %q after a whole flow: the endpoint that writes one did not, so this test is not covering it", prefix)
		}
	}
}

// TestARevokeTheStoreRefusesIsNotAnswered200: revoking is where a client is told
// its own leak is closed, and a store that refused the delete is not a
// revocation — the answer a caller acts on has to be the failure.
//
// The client is the one party who cannot find out afterwards: the token it is
// revoking is the one the thief holds, so the next call with it is the thief's,
// and a 200 over a family still alive is the signal that never comes. The store
// already reports the refusal (store.go's delete, and
// TestARevokeTheStoreRefusesIsNotARevoke holds that half); this test is that report
// reaching the caller as a 500 whose body says nothing was revoked, with the
// token read afterwards to prove the answer was true.
func TestARevokeTheStoreRefusesIsNotAnswered200(t *testing.T) {
	ts := newTestServer(t)
	clientID, tokens := ts.runFlow(t, testClientRedirect)

	held, err := ts.store.LookupByHash(context.Background(), TokenHash(tokens.AccessToken))
	if err != nil {
		t.Fatalf("LookupByHash: %v", err)
	}
	// Every write to this family is refused from here, which is what a retention
	// policy, a hold, a VPC-SC perimeter or a mistyped IAM change answers.
	ts.store.gcs.DenyWrites(testBucket(), familyPrefix+held.FamilyID)

	answer := ts.postForm(t, RevokePath, url.Values{
		"token":     {tokens.AccessToken},
		"client_id": {clientID},
	})
	if answer.Code != http.StatusInternalServerError || refusalOf(t, answer).Error != string(oidc.ServerError) {
		t.Errorf("a revocation the store refused answered %d (%s); want 500 %s: the client that asked cannot check afterwards, so an answer that reads as done over a family still alive is the leak it was revoking",
			answer.Code, strings.TrimSpace(answer.Body.String()), oidc.ServerError)
	}
	if _, err := ts.store.LookupByHash(context.Background(), TokenHash(tokens.AccessToken)); err != nil {
		t.Errorf("the token of the family a refused revocation answered 500 for = %v; want it still resolving: the answer is a report of a refusal, not a refusal of something that happened anyway", err)
	}
}

// unreadableStore is a store whose lookup fails, which is the other half of the
// same rule: a token this installation never issued is RFC 7009's 200, and a
// store that could not be read is not that — it is a request whose outcome is
// unknown, and an answer that read as done would be a claim nobody made.
type unreadableStore struct{ Store }

func (unreadableStore) LookupByHash(context.Context, string) (TokenRecord, error) {
	return TokenRecord{}, errors.New("the store could not be read")
}

// TestARevokeTheStoreCouldNotBeReadIsNotAnswered200 is the lookup's half of the
// test above, and it is a separate test because the two failures are told apart
// in the code: ErrTokenNotFound is the absence RFC 7009 answers 200 to, and any
// other error is the store failing to answer at all.
func TestARevokeTheStoreCouldNotBeReadIsNotAnswered200(t *testing.T) {
	ts := newTestServer(t)
	clientID, tokens := ts.runFlow(t, testClientRedirect)

	// The flow above ran against the real store, so what is replaced here is a
	// working store and not a broken one that everything else also stands on.
	ts.Server.store = unreadableStore{Store: ts.store}

	answer := ts.postForm(t, RevokePath, url.Values{
		"token":     {tokens.AccessToken},
		"client_id": {clientID},
	})
	if answer.Code != http.StatusInternalServerError || refusalOf(t, answer).Error != string(oidc.ServerError) {
		t.Errorf("a revocation whose lookup failed answered %d (%s); want 500 %s: the family is where it was, and a client told its leak was closed would not ask again",
			answer.Code, strings.TrimSpace(answer.Body.String()), oidc.ServerError)
	}
}

// TestNoAnswerOfThisServerIsCacheable: every answer these endpoints write says
// no-store, and it is one rule rather than a line each caller remembers
// (writeJSONStatus).
//
// It is worth a test of its own because every answer here carries something a
// shared cache must not keep: an access token, a rotating refresh token, a
// client's secret, an authorization code, or a URL naming this installation
// rather than another one. The registration answer is the case that made the
// rule a rule — it carried the client's secret and was the one credential-
// bearing answer with no such header — so what this pins is the property and
// not the one endpoint that was missing it.
func TestNoAnswerOfThisServerIsCacheable(t *testing.T) {
	ts := newTestServer(t)
	clientID, tokens := ts.runFlow(t, testClientRedirect)

	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	redirect := ts.approve(t, binding, number)
	if redirect.Code != http.StatusFound {
		t.Fatalf("the approval answered %d (%s); want the redirect that carries the code", redirect.Code, strings.TrimSpace(redirect.Body.String()))
	}
	tokenAnswer, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
	})
	if tokenAnswer.Code != http.StatusOK {
		t.Fatalf("the refresh grant answered %d; want a token pair", tokenAnswer.Code)
	}

	for _, answer := range []struct {
		name string
		rec  *httptest.ResponseRecorder
	}{
		{"the protected-resource metadata document", ts.get(t, ProtectedResourcePath)},
		{"the authorization server's metadata document", ts.get(t, AuthorizationServerPath)},
		{"the registration answer, which carries the client's secret", ts.postJSON(t, RegisterPath, registrationRequest{RedirectURIs: []string{testClientRedirect}, ClientName: "an editor"})},
		{"the consent page", ts.authorizeRequest(t, authorizeQuery(clientID, testClientRedirect))},
		{"a refusal page", ts.get(t, AuthorizePath+"?client_id=no-such-client&redirect_uri="+url.QueryEscape(testClientRedirect))},
		{"the redirect that carries the code", redirect},
		{"an OAuth error body", ts.postForm(t, TokenPath, url.Values{"grant_type": {"client_credentials"}, "client_id": {clientID}})},
		{"the token answer, which carries the pair", tokenAnswer},
		{"the MCP endpoint's 401", ts.get(t, MCPPath)},
		{"the revocation answer", ts.postForm(t, RevokePath, url.Values{"token": {tokens.AccessToken}, "client_id": {clientID}})},
	} {
		if got := answer.rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s carries Cache-Control %q; want no-store: every answer of this server carries a token, a secret, a code or an address of this installation, and a shared cache holding one is a credential kept, or a client sent, by somebody else", answer.name, got)
		}
	}
}

// TestNoBinaryLinksTheFrontDoorYet is the guard the open registration endpoint
// owes: it ships with neither of the two bounds spec.md §8 relies on — no
// per-source rate limit, and no expiry or sweep behind the registration the
// endpoint writes — so the exposure is latent only while nothing a binary
// builds reaches this package. The owner's ruling of 2026-11-07 added the
// second half of what a task that wires the front door owes: M5 is built
// dormant, and the paired-device approval M-a asks for is M5b, a task of its
// own and a hard prerequisite (implementation-plan.md §3). A task that wires
// the front door deliberately deletes this test and carries those bounds and
// M5b with it; until then, a non-test file that names this package fails here
// rather than turning the exposure reachable in a release nobody re-read the
// paragraph for.
func TestNoBinaryLinksTheFrontDoorYet(t *testing.T) {
	const pkg = "github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	root := filepath.Join("..", "..", "..")
	for _, dir := range []string{"cmd", "internal", "ee"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), pkg) {
				t.Errorf("%s links the MCP front door, which mounts an open registration endpoint with neither a rate limit nor a sweep and approves a sign-in on the page that asked for it; a task that wires it carries those two bounds and M5b, the paired-device approval, and deletes this guard", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
}

// TestTheFormBoundIsWhatStopsABodyPastIt is readForm's own bound, at one of the
// endpoints it stands in front of.
//
// It is the only per-request memory bound these endpoints have. Both the token
// endpoint and the registration endpoint are public and unauthenticated, and
// the rate limit spec.md §8 relies on is not built (reconciliation.md §4a, the
// two bounds no task carries), so what a caller may make this process hold is
// decided here and nowhere else: without the MaxBytesReader, net/http's own
// ParseForm cap — 10 MiB — is what a request body reaches, which is a hundred
// and sixty times this bound.
//
// The body below is a well-formed form that is one byte past the bound, so the
// refusal is about the size and not about the form: the control at the end is
// the same form at exactly the bound, which is read and answered on its merits.
// A test that only asserted the status would not hold the rule — with the
// MaxBytesReader gone the request is read, and the endpoint answers 401 for a
// missing client instead — so what is asserted is the refusal naming an
// unreadable body.
func TestTheFormBoundIsWhatStopsABodyPastIt(t *testing.T) {
	ts := newTestServer(t)

	// The form is padded to a body of exactly n bytes, so the two requests
	// below differ in nothing but whether they are past the bound.
	bodyOf := func(n int) url.Values {
		form := url.Values{
			"grant_type": {string(GrantAuthorizationCode)},
			"padding":    {""},
		}
		form.Set("padding", strings.Repeat("a", n-len(form.Encode())))
		return form
	}
	if got := len(bodyOf(maxFormBytes).Encode()); got != maxFormBytes {
		t.Fatalf("the padded form encodes to %d bytes; want exactly %d: the two requests below have to differ in size and nothing else", got, maxFormBytes)
	}

	rec := ts.postForm(t, TokenPath, bodyOf(maxFormBytes+1))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST %s with a %d-byte body answered %d (%s); want 400: the endpoint reads a bounded body, and a caller that sends more is refused rather than read", TokenPath, maxFormBytes+1, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if body := rec.Body.String(); !strings.Contains(body, "not a form this endpoint can read") {
		t.Errorf("the refusal for a %d-byte body is %s; want it naming the body it could not read: with the bound gone the same request is read and refused for a missing client, and a test that only checked the status would pass either way", maxFormBytes+1, strings.TrimSpace(body))
	}

	// The bound is inclusive, and what the same request gets at the bound is
	// the endpoint's own answer to a token request with no client.
	if rec := ts.postForm(t, TokenPath, bodyOf(maxFormBytes)); rec.Code != http.StatusUnauthorized {
		t.Errorf("POST %s with a body of exactly %d bytes answered %d (%s); want the endpoint's own 401 for a request that names no client: the bound is inclusive, so the refusal above is about the size and not about forms", TokenPath, maxFormBytes, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}
