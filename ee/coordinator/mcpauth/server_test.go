// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator"
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
	fs := newFakeStore(t)
	counting := &countingStore{Store: fs.Store, keys: map[string]bool{}}
	v := verifierOver(t, staticKeys{testKid: &testKey().PublicKey})
	srv, err := NewServer(cfg, counting, v, WithServerClock(func() time.Time { return testNow }))
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
