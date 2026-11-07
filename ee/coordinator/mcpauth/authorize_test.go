// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// This file is M5's half of the flow: the authorize endpoint's two methods, the
// token endpoint's two interactive grants, and the rules the design puts
// between them — PKCE, single-use codes, exact redirect URIs, D928's CSRF
// binding, and M-a's approval (a single-use POST carrying the person's Firebase
// ID token in its body and the number the page showed).
//
// Every test here drives the endpoints a real client drives, over the fake
// bucket, with the ID token M6's own tests sign. Nothing reaches into the
// package: what a client can see is what these tests assert on.

const (
	// testVerifier is the PKCE verifier every test exchanges with. It is a
	// marker string and not a secret: it is never stored, and the challenge
	// derived from it is what the flow carries.
	testVerifier = "verifier-4f2a1c9d-marker-never-stored"
	// testState is the client's own state. It carries a space and an ampersand
	// on purpose: D928's "the state returns unaltered" is only held by a test
	// whose state needs escaping, because a value that survives an unescaped
	// round trip proves nothing about one that would not.
	testState = "state-4f2a 1c9d & more"
	// testOtherRedirect is a second client's loopback callback, on a port of its
	// own. The tests that hand one client's code or refresh token to another
	// need two registrations, and two registrations need two redirect URIs: a
	// client that registered the first client's URI would be refused at the
	// authorize endpoint for a reason that has nothing to do with the binding
	// under test.
	testOtherRedirect = "http://127.0.0.1:8977/callback"
)

// testChallenge is the S256 challenge of testVerifier, computed here rather
// than with the package's own helper: a test that derived the challenge the way
// the server verifies it would agree with a wrong server.
func testChallenge() string {
	sum := sha256.Sum256([]byte(testVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorizeQuery is the authorization request a standard client makes.
func authorizeQuery(clientID, redirect string) url.Values {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"code_challenge":        {testChallenge()},
		"code_challenge_method": {"S256"},
		"state":                 {testState},
	}
}

// authorizeRequest makes the authorization request. It carries no credential:
// M-a's approval is a POST, and the GET is an unauthenticated navigation that
// renders the page the approval answers.
func (ts *testServer) authorizeRequest(t *testing.T, q url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return ts.do(t, httptest.NewRequest(http.MethodGet, AuthorizePath+"?"+q.Encode(), nil))
}

// The two things the consent page carries and an approval has to present back.
// They are read out of the page the way a person reads them: the test does not
// know either value in advance and must not.
var (
	consentBindingRE = regexp.MustCompile(`name="request_id" value="([^"]+)"`)
	consentNumberRE  = regexp.MustCompile(`This flow's number is <strong>([0-9]+)</strong>`)
)

// startFlow makes one whole authorization request and answers the binding and
// the number the consent page carries.
func (ts *testServer) startFlow(t *testing.T, q url.Values) (binding, number string) {
	t.Helper()
	rec := ts.authorizeRequest(t, q)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s answered %d (%s); want the consent page: the client is registered and the request is complete, and the GET verifies nothing about a person", AuthorizePath, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	page := rec.Body.String()
	m := consentBindingRE.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the consent page carries no request_id: without the flow's own binding there is nothing an approval can be checked against, and D928's CSRF rule has nothing to hold")
	}
	n := consentNumberRE.FindStringSubmatch(page)
	if n == nil {
		t.Fatalf("the consent page carries no number: M-a's number matching is the check that ties an approval to the page that asked, and a page that shows no number cannot be matched against")
	}
	return m[1], n[1]
}

// consent posts one approval to the authorize endpoint.
func (ts *testServer) consent(t *testing.T, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return ts.postForm(t, AuthorizePath, form)
}

// approve posts the approval the page above asks for: the binding, the number
// the page showed, and the Firebase ID token the sign-in produced — all three
// in the form body, which is the only place M-a allows the token to be.
func (ts *testServer) approve(t *testing.T, binding, number string) *httptest.ResponseRecorder {
	t.Helper()
	return ts.consent(t, url.Values{
		"request_id": {binding},
		"number":     {number},
		"id_token":   {acceptedToken(t)},
		"decision":   {"allow"},
	})
}

// codeOf reads the code out of a consent's redirect, which is where a client
// gets it: the test reads it off the wire rather than out of the store.
func codeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("the consent answered %d (%s); want a redirect to the client's own URI", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("the redirect is not a URL: %v", err)
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatalf("the redirect to %s carries no code: %s", u.Host, u.RawQuery)
	}
	return code
}

// exchange posts one grant to the token endpoint and decodes a successful
// answer.
func (ts *testServer) exchange(t *testing.T, form url.Values) (*httptest.ResponseRecorder, tokenResponse) {
	t.Helper()
	rec := ts.postForm(t, TokenPath, form)
	var out tokenResponse
	if rec.Code == http.StatusOK {
		decodeBody(t, rec, &out)
	}
	return rec, out
}

// exchangeWithAuth posts one grant to the token endpoint with an Authorization
// header — client_secret_basic — and decodes a successful answer.
func (ts *testServer) exchangeWithAuth(t *testing.T, form url.Values, authorization string) (*httptest.ResponseRecorder, tokenResponse) {
	t.Helper()
	rec := ts.postFormWithAuth(t, TokenPath, form, authorization)
	var out tokenResponse
	if rec.Code == http.StatusOK {
		decodeBody(t, rec, &out)
	}
	return rec, out
}

// runFlow is one whole authorization: register, authorize, consent, exchange.
func (ts *testServer) runFlow(t *testing.T, redirect string) (clientID string, tokens tokenResponse) {
	t.Helper()
	clientID, _ = ts.register(t, "an editor", redirect)
	binding, number := ts.startFlow(t, authorizeQuery(clientID, redirect))
	code := codeOf(t, ts.approve(t, binding, number))
	rec, tokens := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {redirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("the code exchange answered %d (%s); want a token pair", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	return clientID, tokens
}

// oauthErrorOf reads the `error` a redirect carried, and checks the redirect
// went where the flow says it should have.
func oauthErrorOf(t *testing.T, rec *httptest.ResponseRecorder, wantHost string) string {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("the endpoint answered %d (%s); want a redirect carrying the error back to the client", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("the redirect is not a URL: %v", err)
	}
	if u.Host != wantHost {
		t.Errorf("the error was redirected to %q; want the client's own host %q", u.Host, wantHost)
	}
	if got := u.Query().Get("state"); got != testState {
		t.Errorf("the redirect carries state %q; want the client's own state %q unaltered (D928)", got, testState)
	}
	return u.Query().Get("error")
}

// TestPKCEIsRequiredAndPlainIsRefused: S256 is required and a plain challenge
// is refused, both at the authorization request and at the exchange.
//
// The plain case is the one worth stating: RFC 7636 §4.3 makes an absent method
// mean `plain`, and a server that took that default would accept a challenge
// that is the verifier itself in the authorization request — which protects
// nothing, because the verifier is then sitting in a URL that reaches a log
// line.
func TestPKCEIsRequiredAndPlainIsRefused(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	host := redirectHost(testClientRedirect)

	q := authorizeQuery(clientID, testClientRedirect)
	q.Del("code_challenge")
	if got := oauthErrorOf(t, ts.authorizeRequest(t, q), host); got != string(oidc.InvalidRequest) {
		t.Errorf("an authorization request with no code_challenge was refused with %q; want %q, and it has to be refused rather than answered", got, oidc.InvalidRequest)
	}

	q = authorizeQuery(clientID, testClientRedirect)
	q.Set("code_challenge_method", "plain")
	if got := oauthErrorOf(t, ts.authorizeRequest(t, q), host); got != string(oidc.InvalidRequest) {
		t.Errorf("an authorization request with a plain challenge was refused with %q; want %q: a plain challenge is the verifier itself and this server accepts S256 alone", got, oidc.InvalidRequest)
	}

	// The method omitted rather than named. RFC 7636 §4.3 makes an absent
	// method mean `plain`, so this is the request above with the default left
	// in, and the server has to refuse it for the same reason rather than
	// reading the default: a client that said nothing has said plain, and a
	// challenge that is the verifier protects nothing.
	q = authorizeQuery(clientID, testClientRedirect)
	q.Del("code_challenge_method")
	if got := oauthErrorOf(t, ts.authorizeRequest(t, q), host); got != string(oidc.InvalidRequest) {
		t.Errorf("an authorization request that names no code_challenge_method was refused with %q; want %q: an absent method defaults to plain (RFC 7636 §4.3), and this server tells the client to say S256 rather than reading the default", got, oidc.InvalidRequest)
	}

	// And the exchange is where the challenge is actually checked: a code
	// issued for one verifier is not redeemable with another.
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code := codeOf(t, ts.approve(t, binding, number))
	rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier + "-not-the-one"},
	})
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), string(oidc.InvalidGrant)) {
		t.Errorf("the exchange refused a wrong verifier with %d (%s); want invalid_grant", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// TestCodeIsSingleUseAndBoundToTheRedirectURI: one code, one exchange, and only
// for the redirect URI it was issued for.
//
// Both halves are read off the wire: the code comes out of the consent's
// redirect, which is where a client gets it, and the second exchange presents
// the same value again.
func TestCodeIsSingleUseAndBoundToTheRedirectURI(t *testing.T) {
	ts := newTestServer(t)
	clientID, tokens := ts.runFlow(t, testClientRedirect)
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("the flow answered %+v; want an access token and a refresh token", tokens)
	}

	// The same code again. It has to be refused, and the refusal has to name
	// the rule rather than the value: the code is the caller's own text.
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code := codeOf(t, ts.approve(t, binding, number))
	form := url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	}
	if rec, _ := ts.exchange(t, form); rec.Code != http.StatusOK {
		t.Fatalf("the first exchange of a fresh code answered %d (%s)", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	rec, _ := ts.exchange(t, form)
	if rec.Code == http.StatusOK {
		t.Errorf("the second exchange of one code minted a second token pair: a code is single-use (spec.md §3), and two redemptions of one are the replay PKCE exists to bound")
	}
	if !strings.Contains(rec.Body.String(), string(oidc.InvalidGrant)) {
		t.Errorf("the second exchange answered %d (%s); want invalid_grant", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if strings.Contains(rec.Body.String(), code) {
		t.Errorf("the refusal repeats the code: %s", strings.TrimSpace(rec.Body.String()))
	}

	// The same code, presented for another redirect URI.
	binding, number = ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code = codeOf(t, ts.approve(t, binding, number))
	rec, _ = ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {"http://127.0.0.1:9999/callback"},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	})
	if rec.Code == http.StatusOK {
		t.Errorf("the exchange accepted a redirect_uri the code was not issued for: the code is bound to the client, the redirect URI and the challenge, and the exchange presents the URI again rather than choosing one")
	}
}

// TestAReplayedCodeRevokesWhatItMinted is OAuth 2.1 §4.1.3's SHOULD, built: a
// code presented twice does not merely get refused, it revokes the token family
// its first exchange issued.
//
// Two parties hold one code only if one of them read it somewhere it should not
// have been, and the tokens that exchange minted are exactly what must stop
// working while that is being sorted out. The second half is what makes it a
// revocation rather than a sentence in a refusal: the refresh token the first
// exchange handed out is dead afterwards.
func TestAReplayedCodeRevokesWhatItMinted(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)

	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code := codeOf(t, ts.approve(t, binding, number))
	form := url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	}
	rec, tokens := ts.exchange(t, form)
	if rec.Code != http.StatusOK {
		t.Fatalf("the first exchange answered %d (%s); want a token pair", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	if rec, _ := ts.exchange(t, form); rec.Code == http.StatusOK {
		t.Fatalf("the replay of the code answered with a token pair")
	}
	// The family the first exchange minted is gone, so the refresh token it
	// handed out no longer rotates.
	if rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
	}); rec.Code == http.StatusOK {
		t.Errorf("the refresh token the first exchange minted still rotates after the code was replayed: OAuth 2.1 §4.1.3 makes a replay revoke the tokens that exchange issued, because two parties holding one code is the theft the revocation is for")
	}
}

// TestRedirectURIMustMatchExactly: at the authorize endpoint the redirect URI
// has to be one the client registered, character for character.
//
// The refusals here are pages rather than redirects, and that is the point of
// the order the endpoint checks in: until the URI is the client's own there is
// no address this server may send an error to, and answering with a redirect to
// a URI the client did not register is the open redirect the rule exists to
// prevent.
func TestRedirectURIMustMatchExactly(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)

	for _, uri := range []string{
		// A URI that extends the registered one, and one it extends: the two
		// directions a prefix or a suffix rule would fall into. Both are
		// loopback, so checkRedirectURI admits them and the registered-list
		// match is the only thing that can refuse them.
		"http://127.0.0.1:8976/callback/",
		"http://127.0.0.1:8976/callback?next=https://evil.example.org",
		"http://127.0.0.1:8976/callbac",
		"http://127.0.0.1:8976/callback/../callback",
		// And URIs that differ in the parts a person does not read.
		"http://127.0.0.1:8977/callback",
		"http://127.0.0.1:8976/Callback",
		"https://client.example.net/callback",
		"",
	} {
		rec := ts.authorizeRequest(t, authorizeQuery(clientID, uri))
		if rec.Code == http.StatusOK {
			t.Errorf("GET %s rendered a consent page for redirect_uri %q, which this client did not register: the match is exact (D928)", AuthorizePath, uri)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("GET %s answered redirect_uri %q with a redirect to %q: an error must not be sent to a URI the client did not register", AuthorizePath, uri, loc)
		}
	}

	if rec := ts.authorizeRequest(t, authorizeQuery(clientID, testClientRedirect)); rec.Code != http.StatusOK {
		t.Errorf("GET %s refused the URI the client registered (%d): the rule is exactness, not exclusion", AuthorizePath, rec.Code)
	}
}

// TestAuthorizeRefusesWhatItCannotAnswer: the three requests this endpoint
// refuses by name rather than by ignoring — a response type it does not issue, a
// resource it does not serve, and a state it will not hold.
//
// The first two are redirects with the client's own state, because by then the
// flow is known to be the client's. The third is a page, and that is the point
// of it: D928 returns the state unaltered, so a state this server will not hold
// is one it must not echo back either.
func TestAuthorizeRefusesWhatItCannotAnswer(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	host := redirectHost(testClientRedirect)

	// A response type this server does not issue. The code is RFC 6749
	// §4.1.2.1's own, and it is not `unsupported_grant_type`: what was asked
	// for is a response type at the authorization endpoint, and the answer has
	// to name the thing that was wrong.
	for _, rt := range []string{"token", "id_token", "code token", ""} {
		q := authorizeQuery(clientID, testClientRedirect)
		if rt == "" {
			q.Del("response_type")
		} else {
			q.Set("response_type", rt)
		}
		if got := oauthErrorOf(t, ts.authorizeRequest(t, q), host); got != "unsupported_response_type" {
			t.Errorf("an authorization request with response_type %q was refused with %q; want unsupported_response_type: this server issues authorization codes, and the error has to name the parameter that was wrong", rt, got)
		}
	}

	// RFC 8707: a resource this server does not serve is refused with
	// `invalid_target` rather than ignored. Ignoring it would mint a token for
	// a resource the client did not ask for and did not get told about.
	for _, target := range []string{"https://elsewhere.example.org", testIssuer + "/mcp", "not-a-url"} {
		q := authorizeQuery(clientID, testClientRedirect)
		q.Set("resource", target)
		if got := oauthErrorOf(t, ts.authorizeRequest(t, q), host); got != string(oidc.InvalidTarget) {
			t.Errorf("an authorization request naming the resource %q was refused with %q; want %q: this server mints tokens for its own URL and for nothing else", target, got, oidc.InvalidTarget)
		}
	}
	// The one resource it does serve is answered, so the refusals above are
	// about the resource and not about the parameter.
	q := authorizeQuery(clientID, testClientRedirect)
	q.Set("resource", testIssuer)
	if rec := ts.authorizeRequest(t, q); rec.Code != http.StatusOK {
		t.Errorf("an authorization request naming this server's own URL as its resource answered %d (%s); want the consent page: that is the audience every token here carries", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// A state longer than this server holds. The refusal is a page with no
	// Location, and the state appears nowhere in it.
	long := strings.Repeat("s", maxStateBytes+1)
	q = authorizeQuery(clientID, testClientRedirect)
	q.Set("state", long)
	rec := ts.authorizeRequest(t, q)
	if rec.Code == http.StatusOK {
		t.Errorf("an authorization request carrying a state of %d bytes rendered the consent page: the state is kept and returned unaltered (D928), so it is bounded rather than accepted", len(long))
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("an authorization request carrying an over-long state answered with a redirect to %q: the bound exists to keep the value out of the answer, and a redirect would put it back in one", loc)
	}
	if strings.Contains(rec.Body.String(), long) {
		t.Error("the refusal echoes the over-long state: the state that was refused is not a value this server holds, and reflecting it is how a bound turns into no bound at all")
	}
}

// TestAnUnknownScopeIsRefusedNotDropped is D927's rule at the door it is
// checked on first, and M-c's "before the library sees it": a scope outside the
// vocabulary is refused, and it is refused while there is still nothing to read
// it out of.
//
// The scope is the one value in an authorization request that a client chooses
// and that decides what the token may do, so the failure this test has to rule
// out is the quiet one: a server that dropped the scope it did not know would
// leave its holder believing it holds a permission the token does not carry,
// and every later check would be reading a shorter list than the client asked
// for with nothing anywhere saying so.
//
// "Before the library sees it" is held as the store's own state rather than as
// an ordering claim in a comment: every refusal below is answered while the
// store holds the client record and nothing else, so no pending request, no
// code and no token exists for a request this server refused.
func TestAnUnknownScopeIsRefusedNotDropped(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	host := redirectHost(testClientRedirect)

	for _, c := range []struct{ scope, unknown string }{
		{"runs:read", "runs:read"},
		// Near misses of a scope this server does mint, written to look like
		// one from one side or the other: a suffix, a case, a third field.
		{"tasks:admin", "tasks:admin"},
		{"Tasks:read", "Tasks:read"},
		{"tasks:read:all", "tasks:read:all"},
		// A known scope beside the unknown one. The refusal has to be about
		// the scope this server does not mint and not about a request that
		// carried more than one.
		{"tasks:read runs:read", "runs:read"},
	} {
		q := authorizeQuery(clientID, testClientRedirect)
		q.Set("scope", c.scope)
		rec := ts.authorizeRequest(t, q)
		if got := oauthErrorOf(t, rec, host); got != string(oidc.InvalidScope) {
			t.Errorf("an authorization request asking for the scope %q was refused with %q; want %q: an unknown scope is refused rather than dropped, because a token that quietly lost one leaves its holder with a permission it does not have", c.scope, got, oidc.InvalidScope)
		}
		u, err := url.Parse(rec.Header().Get("Location"))
		if err != nil {
			t.Fatalf("the refusal of the scope %q is not a URL: %v", c.scope, err)
		}
		// The scope the client asked for is named, because that is the one
		// thing that lets a client fix its own request; the vocabulary it is
		// measured against is in the message and not in the query.
		if !strings.Contains(u.Query().Get("error_description"), c.unknown) {
			t.Errorf("the refusal of the scope %q says %q; want %q named in it: a client that is told only that a scope is wrong, with four of them, has nothing to act on", c.scope, u.Query().Get("error_description"), c.unknown)
		}
	}

	// The control: a scope this server does mint is answered, so the refusals
	// above are about the scopes and not about the parameter being read at all.
	q := authorizeQuery(clientID, testClientRedirect)
	q.Set("scope", ScopeTasksRead)
	if rec := ts.authorizeRequest(t, q); rec.Code != http.StatusOK {
		t.Errorf("an authorization request asking for %q answered %d (%s); want the consent page: that scope is one of the two D927 mints", ScopeTasksRead, rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// The five refusals wrote nothing, and the one accepted request wrote one
	// pending request: the store holds the client this test registered and that
	// one flow, so the check above happened before any record of the scope
	// existed to be read back.
	objects := ts.store.objects(t)
	if len(objects) != 2 {
		t.Errorf("the store holds %d objects; want the one client and the one pending request of the accepted flow: a refused scope is refused before anything is written, and a request recorded for it would be a record of a flow this server will never answer", len(objects))
	}
	for key := range objects {
		if !strings.HasPrefix(key, clientPrefix) && !strings.HasPrefix(key, requestPrefix) {
			t.Errorf("a refused scope left %s behind: the refusals happen before the request, the code or any token is written", key)
		}
	}
}

// TestTheInstallationsLimitsNarrowTheTokenTheFlowMints is the one place the
// installation's own policy enters the flow: the code carries what the person
// consented to, and the token it is exchanged for carries that narrowed against
// the project's own limits.
//
// The configuration below grants one of the two scopes D927 mints while the
// flow asks for both, which is the only shape where the narrowing can be seen:
// with both granted the intersection is the request, and a server that minted
// the code's scopes unchanged would answer the same token. Both halves of the
// answer are read — the `scope` a client reads, and the limits the record
// holds, which are what the resource server enforces on every call.
func TestTheInstallationsLimitsNarrowTheTokenTheFlowMints(t *testing.T) {
	cfg := testConfig()
	cfg.Limits = Limits{Scopes: []string{ScopeTasksRead}}
	ts := newTestServerWith(t, cfg)

	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	q := authorizeQuery(clientID, testClientRedirect)
	q.Set("scope", ScopeTasksRead+" "+ScopeTasksWrite)
	binding, number := ts.startFlow(t, q)
	code := codeOf(t, ts.approve(t, binding, number))
	rec, tokens := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("the exchange answered %d (%s); want a token pair: the installation grants one of the two scopes the consent carried, so the request is answerable", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if tokens.Scope != ScopeTasksRead {
		t.Errorf("the token came back with scope %q; want %q: the token is the consent narrowed against the installation's own limits, not the consent read back", tokens.Scope, ScopeTasksRead)
	}
	record, err := ts.store.LookupByHash(context.Background(), TokenHash(tokens.AccessToken))
	if err != nil {
		t.Fatalf("reading the access token's record: %v", err)
	}
	if got := strings.Join(record.Token.Limits.Scopes, " "); got != ScopeTasksRead {
		t.Errorf("the access token's record carries the scopes %q; want %q: the record is what the resource server enforces on every call, and a record wider than the installation's own limits is the policy not reaching the token at all", got, ScopeTasksRead)
	}
}

// TestAScopeTheInstallationDoesNotGrantIsRefusedNotWidened is the other half of
// the same rule: a code carrying only a scope the installation does not grant is
// not exchanged for a token at all.
//
// The two doors check different things, which is why this one is reachable: the
// authorize endpoint measures the requested scope against D927's vocabulary,
// and that vocabulary is the same for every installation. Whether this
// installation grants it is its own policy, and the exchange is where that is
// read. The failure this rules out is the quiet one — a token minted with the
// scope the consent asked for because the consent asked for it — which would
// leave a client holding a permission the installation does not give with
// nothing afterwards saying so.
func TestAScopeTheInstallationDoesNotGrantIsRefusedNotWidened(t *testing.T) {
	cfg := testConfig()
	cfg.Limits = Limits{Scopes: []string{ScopeTasksRead}}
	ts := newTestServerWith(t, cfg)

	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	q := authorizeQuery(clientID, testClientRedirect)
	q.Set("scope", ScopeTasksWrite)
	binding, number := ts.startFlow(t, q)
	code := codeOf(t, ts.approve(t, binding, number))

	rec, tokens := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	})
	if rec.Code == http.StatusOK {
		t.Fatalf("the exchange minted %+v for a code carrying %q, which this installation does not grant", tokens, ScopeTasksWrite)
	}
	if got := refusalOf(t, rec).Error; got != string(oidc.InvalidGrant) {
		t.Errorf("the exchange refused a scope this installation does not grant with %q; want %q", got, oidc.InvalidGrant)
	}
	// Nothing was minted and no family was opened: the store holds the client
	// and the spent code, and a token record would be the widening itself.
	for key := range ts.store.objects(t) {
		if strings.HasPrefix(key, tokenPrefix) || strings.HasPrefix(key, familyPrefix) {
			t.Errorf("%s exists after an exchange this installation refused: a refused authorization mints no token and opens no family", key)
		}
	}
}

// TestRefreshTokenIsSingleUse is D930 at the endpoint: a refresh token rotates
// on every use, and the token that was rotated away is the theft signal that
// revokes the family.
//
// The last assertion is the one that makes rotation a lifetime mechanism rather
// than a budget one: after the replay, the token the legitimate holder was just
// given stops working too.
func TestRefreshTokenIsSingleUse(t *testing.T) {
	ts := newTestServer(t)
	clientID, tokens := ts.runFlow(t, testClientRedirect)

	rec, rotated := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("the refresh grant answered %d (%s); want a rotated pair", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if rotated.RefreshToken == "" || rotated.RefreshToken == tokens.RefreshToken {
		t.Fatalf("the refresh grant answered refresh_token %q; want a fresh one: the refresh token rotates on every use (D930)", rotated.RefreshToken)
	}

	rec, _ = ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
	})
	if rec.Code == http.StatusOK {
		t.Errorf("the refresh grant accepted a token that was already rotated away: rotation is what makes a theft detectable, and answering it with a new pair makes it silent")
	}
	if !strings.Contains(rec.Body.String(), string(oidc.InvalidGrant)) {
		t.Errorf("the replay answered %d (%s); want invalid_grant", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if strings.Contains(rec.Body.String(), tokens.RefreshToken) {
		t.Errorf("the refusal repeats the refresh token: %s", strings.TrimSpace(rec.Body.String()))
	}

	// The family is revoked, so the pair the replay's own rotation produced is
	// dead as well: neither the thief's token nor the legitimate holder's
	// resolves.
	if rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {rotated.RefreshToken},
		"client_id":     {clientID},
	}); rec.Code == http.StatusOK {
		t.Errorf("the token the replay rotated to still works: D930's answer to a replayed refresh token is one delete of the whole family, so both halves stop working and the event is the audit record's")
	}
}

// TestConsentNamesTheRedirectHost is the owner's tightening of D928: the page a
// person consents on names the host their code will be sent to, so they consent
// to somewhere rather than to an opaque client id.
//
// It is also where M-a's "the client is shown as unverified" is held: the name
// beside that host is the client's own assertion about itself, so the page says
// so. A page that showed `an editor` as though something had checked it would be
// the page an attacker's client uses to look like one the person trusts.
func TestConsentNamesTheRedirectHost(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testAllowlistedRedirect)

	rec := ts.authorizeRequest(t, authorizeQuery(clientID, testAllowlistedRedirect))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s answered %d (%s); want the consent page", AuthorizePath, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	page := rec.Body.String()
	if !strings.Contains(page, "client.example.net") {
		t.Errorf("the consent page does not name the redirect URI's host: a person consenting to a client id alone has not been told where their code goes (D928)")
	}
	if !strings.Contains(page, "an editor") {
		t.Errorf("the consent page does not name the client: the page is what a person reads before allowing it")
	}
	if !strings.Contains(page, "This client is not verified") {
		t.Errorf("the consent page shows the client's own name without saying that nothing checked it: client_name is a stranger's assertion, and M-a requires the client to be marked unverified on the page a person approves from")
	}
	if strings.Contains(page, "user:"+testEmail) {
		t.Errorf("the consent page names the identity %q before any approval has arrived: the GET verifies nothing (M-a), so an identity on this page would be one the page invented", "user:"+testEmail)
	}
	// The page is not framable: an Allow button in an attacker's iframe is a
	// click the person meant for something else.
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("the consent page carries Content-Security-Policy %q; want frame-ancestors 'none', because a consent page an attacker can frame is a page whose Allow button can be placed under somebody else's click", csp)
	}
	if xfo := rec.Header().Get("X-Frame-Options"); xfo != "DENY" {
		t.Errorf("the consent page carries X-Frame-Options %q; want DENY, which is the same rule for a browser that does not read frame-ancestors", xfo)
	}
}

// TestConsentIsCSRFBound is D928's third tightening: the approval carries the
// flow's own binding, an approval that does not is refused, and the state
// returns on the redirect unaltered.
//
// The third assertion is the one that holds the whole rule: an approval that
// carried the right binding, the right number and a redirect URI of its own
// choosing still goes where the record says, because the record is the only
// place any of it is read from. Without that, a binding would be a token an
// attacker could present beside values of their own.
//
// The refusals are 400 and not 500, which is asserted rather than left to the
// redirect check: this endpoint is a public POST, so every way of naming no
// pending flow — an absent request_id, an empty one, one that cannot be a key
// segment, one that names nothing — is the caller's mistake and has to read as
// one. A 500 here would be the server reporting a stranger's typing as its own
// fault, and would tell a caller which of its guesses was shaped like a real
// binding.
func TestConsentIsCSRFBound(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	host := redirectHost(testClientRedirect)

	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))

	for _, form := range []url.Values{
		{"decision": {"allow"}},
		{"request_id": {""}, "decision": {"allow"}},
		{"request_id": {"not-the-binding"}, "decision": {"allow"}},
		{"request_id": {"a/b"}, "decision": {"allow"}},
		{"request_id": {".."}, "decision": {"allow"}},
	} {
		rec := ts.consent(t, form)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("an approval carrying %v answered %d (%s); want 400: an approval that does not carry the flow's own binding is refused as the caller's own mistake, not answered and not reported as the server's", form, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("an approval carrying %v answered with a redirect to %q: nothing is known about the flow, so there is nowhere safe to send an answer", form, loc)
		}
	}
	if got := ts.store.objects(t); len(got) != 2 {
		t.Errorf("the store holds %d objects after four refused approvals; want the client and the pending request and no code", len(got))
	}

	// The real approval, with a redirect URI of the caller's own choosing beside
	// the binding: the code goes to the record's URI, and the state comes back
	// as the client sent it.
	rec := ts.consent(t, url.Values{
		"request_id":   {binding},
		"number":       {number},
		"id_token":     {acceptedToken(t)},
		"decision":     {"allow"},
		"redirect_uri": {"https://evil.example.org/callback"},
	})
	if rec.Code != http.StatusFound {
		t.Fatalf("the approval answered %d (%s); want a redirect to the client's own URI", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("the redirect is not a URL: %v", err)
	}
	if u.Host != host {
		t.Errorf("the code was sent to %q; want the URI the flow was recorded with (%q): everything the approval decides is read out of the record and never out of the approval", u.Host, host)
	}
	if got := u.Query().Get("state"); got != testState {
		t.Errorf("the redirect carries state %q; want %q unaltered (D928)", got, testState)
	}
	if u.Query().Get("code") == "" {
		t.Errorf("the redirect carries no code: %s", u.RawQuery)
	}

	// And the flow is answered once.
	if again := ts.approve(t, binding, number); again.Code == http.StatusFound {
		t.Errorf("the same binding was answered twice: one binding answers one approval, and the second is refused rather than honoured")
	}
}

// TestTheFlowBindsTheIdentityTheProjectsListsCarry is the `user:<address>`
// entry the sign-in step writes into every record the flow makes, which is the
// value D933's launcher and operator lists are written in and M7 matches them
// against.
//
// It is the one thing in the flow that no other test can see. The token below
// is signed for an address spelled in mixed case, so the entry has to be the
// folded one: a verifySignIn that returned the bare address, or the address as
// the issuer spelled it, passes every other test in this package. And the entry
// is read back out of the store through the minted tokens rather than asserted
// against a literal a test wrote — what is checked here is what the flow
// produced.
func TestTheFlowBindsTheIdentityTheProjectsListsCarry(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)

	claims := idTokenClaims(testProject)
	claims["email"] = "Person-1@Example.COM"
	tok := signJWT(t, testKey(), testKid, "RS256", claims)

	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code := codeOf(t, ts.consent(t, url.Values{
		"request_id": {binding},
		"number":     {number},
		"id_token":   {tok},
		"decision":   {"allow"},
	}))
	rec, tokens := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("the exchange answered %d (%s); want a token pair", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	want := "user:" + testEmail
	for _, tc := range []struct{ what, value string }{
		{"access token", tokens.AccessToken},
		{"refresh token", tokens.RefreshToken},
	} {
		record, err := ts.store.LookupByHash(context.Background(), TokenHash(tc.value))
		if err != nil {
			t.Fatalf("reading the %s's record: %v", tc.what, err)
		}
		if got := record.Token.Identity; got != want {
			t.Errorf("the %s's record carries Identity %q; want %q: the entry is `user:` and the address the verifier folded to ASCII lower case, because that is the spelling the project's own launcher and operator lists hold (D933)", tc.what, got, want)
		}
	}
}

// TestTheApprovalRefusesAnUnverifiedSignIn: the sign-in step is what the whole
// approval rests on, and neither of the two ways it can fail issues a code.
//
// The two ways are the two branches of verifySignIn: an approval that carries no
// token at all, and one that carries a token this installation will not verify —
// here, a token another project's Firebase signed, which is the refusal M6's own
// tests hold one layer down. What this test adds is that the endpoint does not
// go on regardless: an unverified sign-in is a 401 page, never a code and never
// a redirect to the client, because there is no person whose approval this was.
func TestTheApprovalRefusesAnUnverifiedSignIn(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	otherProjectToken := signJWT(t, testKey(), testKid, "RS256", idTokenClaims(testOtherProject))

	for _, tc := range []struct{ what, token string }{
		{"no token at all", ""},
		{"a token that is not a JWT", "not-a-jwt"},
		{"a token another installation's Firebase signed", otherProjectToken},
	} {
		binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
		rec := ts.consent(t, url.Values{
			"request_id": {binding},
			"number":     {number},
			"id_token":   {tc.token},
			"decision":   {"allow"},
		})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("an approval carrying %s answered %d (%s); want 401: an unverified sign-in is refused rather than answered with a code", tc.what, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("an approval carrying %s answered with a redirect to %q: nothing is known about the person, so there is no flow to send an answer to", tc.what, loc)
		}
		if tc.token != "" && strings.Contains(rec.Body.String(), tc.token) {
			t.Errorf("the refusal for %s repeats the token: a token rendered into a page is a token in a browser's history", tc.what)
		}
	}
	// No code was written for any of them: the client and the three pending
	// requests are what is in the store, and a refused approval writes no code.
	at := ts.store.objects(t)
	if len(at) != 4 {
		t.Errorf("the store holds %d objects after three refused approvals; want the one client record, the three pending requests and no code", len(at))
	}
	for key := range at {
		if strings.HasPrefix(key, codePrefix) {
			t.Errorf("%s is a code written by an approval whose sign-in did not verify", key)
		}
	}

	// The same approval with a token this installation signed is answered, so the
	// refusals above are about the token and not about the request.
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	if rec := ts.approve(t, binding, number); rec.Code != http.StatusFound {
		t.Errorf("the same approval with a token this installation signed answered %d (%s); want the code redirect", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// TestTheApprovalNeedsTheNumberThePageShowed is M-a's number check at the
// endpoint: an approval that does not present the flow's own number is refused,
// whatever else it carries.
//
// What that holds is the page binding — the number the page rendered has to
// come back with the approval, so an approval that never saw the page is
// refused. What it does not hold, and this test does not claim, is M-a's
// device-side approval: the number is displayed and echoed on the same page, so
// a page rendering a flow of its own knows its own number too
// (ee/coordinator/mcpauth/authorize.go's note, and the follow-up's finding for
// the owner).
func TestTheApprovalNeedsTheNumberThePageShowed(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)

	for _, number := range []string{"", "000000", "not-a-number"} {
		binding, shown := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
		if number == "000000" && shown == "000000" {
			// A one-in-a-million draw; the control below would then be
			// indistinguishable from the refusals.
			t.Skip("the flow's number happened to be the one this case presents")
		}
		rec := ts.consent(t, url.Values{
			"request_id": {binding},
			"number":     {number},
			"id_token":   {acceptedToken(t)},
			"decision":   {"allow"},
		})
		if rec.Code == http.StatusFound {
			t.Errorf("an approval presenting the number %q was answered with a code, and the page showed %q: the approval has to present the number the page showed, or the check is a hidden field's theatre", number, shown)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("an approval presenting the number %q answered with a redirect to %q: the number is checked before the flow is answered, so there is nothing to redirect to", number, loc)
		}
	}

	// The control: the number the page showed is answered, so the refusals above
	// are about the number and not about the rest of the approval.
	binding, shown := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	if rec := ts.approve(t, binding, shown); rec.Code != http.StatusFound {
		t.Errorf("the approval carrying the number the page showed answered %d (%s); want the code redirect", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// TestADenyIsAnsweredWithoutANumberOrAToken is RFC 6749 §4.1.2.1's error
// response stated as a fact about the wire: a deny is an answer to the flow, and
// it is the answer a person who does not trust the client gives.
//
// The page renders one form behind both buttons, so Deny submits the same
// request as Allow — and nobody refusing a client has a reason to paste the
// flow's number or an ID token into it. The client is waiting on its own
// callback, so a deny that ended on a refusal page would be an answer this
// server never sends. What a deny does have to carry is the flow's binding,
// which is what makes it an answer to this flow rather than to any other.
func TestADenyIsAnsweredWithoutANumberOrAToken(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	binding, _ := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))

	rec := ts.consent(t, url.Values{
		"request_id": {binding},
		"decision":   {"deny"},
	})
	if rec.Code != http.StatusFound {
		t.Fatalf("a deny carrying only the flow's binding answered %d (%s); want the redirect the client is waiting on", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("the redirect is not a URL: %v", err)
	}
	if u.Host != redirectHost(testClientRedirect) {
		t.Errorf("the deny was sent to %q; want the URI the flow was recorded with (%q)", u.Host, testClientRedirect)
	}
	if got := u.Query().Get("error"); got != string(oidc.ErrAccessDenied().ErrorType) {
		t.Errorf("the deny redirect carries error %q; want %q (RFC 6749 §4.1.2.1)", got, string(oidc.ErrAccessDenied().ErrorType))
	}
	if got := u.Query().Get("state"); got != testState {
		t.Errorf("the deny redirect carries state %q; want %q unaltered (D928)", got, testState)
	}
	if u.Query().Get("code") != "" {
		t.Errorf("the deny redirect carries a code: %s", u.RawQuery)
	}
	if got := ts.store.objects(t); len(got) != 2 {
		t.Errorf("the store holds %d objects after a deny; want the client and the spent flow and no code", len(got))
	}

	// And the deny answered the flow: the same binding is not answered twice.
	if again := ts.consent(t, url.Values{"request_id": {binding}, "decision": {"deny"}}); again.Code == http.StatusFound {
		t.Errorf("a deny was answered twice on one binding: one binding answers one approval")
	}
}

// TestAnExpiredRequestIsAnsweredWithTheClientsOwnError is RequestTTL stated as a
// fact about the wire: a page left open past the flow's own lifetime is over,
// and the client waiting on its callback is told so instead of being left to
// poll a flow nobody can answer any more.
//
// The check is the whole of the bound — the record carries its expiry and the
// endpoint is the only thing that reads it — so a page whose flow expired but
// still answered would make RequestTTL a field and not a limit.
func TestAnExpiredRequestIsAnsweredWithTheClientsOwnError(t *testing.T) {
	now := testNow
	ts := newTestServerClock(t, testConfig(), func() time.Time { return now })
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))

	now = testNow.Add(RequestTTL + time.Second)
	// The approval is a whole, correct approval: the binding, the number the
	// page showed and a token that verifies. Only the clock has moved, so the
	// only thing that can refuse it is the lifetime.
	rec := ts.approve(t, binding, number)
	if rec.Code != http.StatusFound {
		t.Fatalf("an approval of an expired request answered %d (%s); want the redirect the client is waiting on, carrying the flow's own error", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("the redirect is not a URL: %v", err)
	}
	if u.Host != redirectHost(testClientRedirect) {
		t.Errorf("the expired flow was answered to %q; want the URI the flow was recorded with (%q)", u.Host, testClientRedirect)
	}
	if got := u.Query().Get("error"); got != string(oidc.ErrAccessDenied().ErrorType) {
		t.Errorf("the expired flow carried error %q; want %q", got, string(oidc.ErrAccessDenied().ErrorType))
	}
	if got := u.Query().Get("state"); got != testState {
		t.Errorf("the expired flow carried state %q; want %q unaltered (D928)", got, testState)
	}
	if u.Query().Get("code") != "" {
		t.Errorf("an expired request produced a code: %s", u.RawQuery)
	}
	if got := ts.store.objects(t); len(got) != 2 {
		t.Errorf("the store holds %d objects after an expired request was answered; want the client and the spent flow and no code", len(got))
	}
}

// TestAnExpiredCodeIsRefused is CodeTTL stated as a fact about the wire, and
// the other half of "a code that is spent is spent": a code past its lifetime
// is refused even when everything else about the exchange is right.
//
// The check is the whole of the bound, as above: nothing sweeps a code, so a
// code's lifetime exists exactly as far as this comparison reaches.
func TestAnExpiredCodeIsRefused(t *testing.T) {
	now := testNow
	ts := newTestServerClock(t, testConfig(), func() time.Time { return now })
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code := codeOf(t, ts.approve(t, binding, number))

	now = testNow.Add(CodeTTL + time.Second)
	rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"code_verifier": {testVerifier},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("the exchange of an expired code answered %d (%s); want a refused grant", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if got := refusalOf(t, rec).Error; got != string(oidc.ErrInvalidGrant().ErrorType) {
		t.Errorf("the exchange of an expired code answered %q; want %q (RFC 6749 §5.2)", got, string(oidc.ErrInvalidGrant().ErrorType))
	}
}

// TestAnIDTokenInAQueryStringIsRefused is M-a stated as a fact about the wire:
// the Firebase ID token is accepted in the body of the approval POST and
// nowhere else.
//
// A query parameter is the shape that matters, because it is the shape that ends
// up somewhere else: in a proxy's log, in a browser's history, in a Referer
// header. Both methods refuse one rather than reading past it — the GET because
// there is nothing on it that could read a token, and the POST because the body
// is where the token belongs.
func TestAnIDTokenInAQueryStringIsRefused(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	token := acceptedToken(t)

	q := authorizeQuery(clientID, testClientRedirect)
	q.Set("id_token", token)
	rec := ts.authorizeRequest(t, q)
	if rec.Code == http.StatusOK {
		t.Errorf("GET %s carrying an ID token in its query string rendered the consent page: a token in a URL reaches a log line and a browser's history, and this endpoint must never accept one that way (M-a)", AuthorizePath)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("GET %s carrying an ID token in its query string answered with a redirect to %q: the refusal has to be a page, because a redirect would put the token in a second place", AuthorizePath, loc)
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Error("the refusal repeats the token: a token rendered into a page is a token in a browser's history")
	}
	// A refusal is a page a person reads too, so it is not framable either: the
	// rule is about every page this endpoint renders and not only the consent.
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("the refusal page carries Content-Security-Policy %q; want frame-ancestors 'none'", csp)
	}
	if xfo := rec.Header().Get("X-Frame-Options"); xfo != "DENY" {
		t.Errorf("the refusal page carries X-Frame-Options %q; want DENY", xfo)
	}

	// The approval itself, carrying the token in its query string *and* a valid
	// one in its body. This is the case that makes the refusal load-bearing: an
	// endpoint that read the query would answer it with a code, and a URL that
	// carried the token has already been written to a log line, so the request
	// is refused rather than answered.
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	req := httptest.NewRequest(http.MethodPost,
		AuthorizePath+"?id_token="+url.QueryEscape(token),
		strings.NewReader(url.Values{
			"request_id": {binding},
			"number":     {number},
			"id_token":   {token},
			"decision":   {"allow"},
		}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = ts.do(t, req)
	if rec.Code == http.StatusFound {
		t.Errorf("an approval carrying an ID token in its query string was answered with a code: the body is the only place the token is read from, and a token that reached a URL has already been written to a log line")
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("an approval carrying an ID token in its query string answered with a redirect to %q", loc)
	}

	// And the same approval with the token in the body alone is answered, so the
	// refusal above is about where the token was and not about the token.
	binding, number = ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	if rec := ts.approve(t, binding, number); rec.Code != http.StatusFound {
		t.Errorf("the same approval with the token in its body alone answered %d (%s); want the code redirect", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// `access_token` is the other name a caller reaches for when what it carries
	// is a bearer, and both methods refuse it the same way: a rule kept for one
	// parameter name and not the other is a rule about which name the caller
	// chose.
	q = authorizeQuery(clientID, testClientRedirect)
	q.Set("access_token", token)
	if rec := ts.authorizeRequest(t, q); rec.Code == http.StatusOK {
		t.Errorf("GET %s carrying an access token in its query string rendered the consent page: a bearer in a URL reaches a log line and a browser's history", AuthorizePath)
	}
	binding, number = ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	req = httptest.NewRequest(http.MethodPost,
		AuthorizePath+"?access_token="+url.QueryEscape(token),
		strings.NewReader(url.Values{
			"request_id": {binding},
			"number":     {number},
			"id_token":   {token},
			"decision":   {"allow"},
		}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := ts.do(t, req); rec.Code == http.StatusFound {
		t.Errorf("an approval carrying an access token in its query string was answered with a code: a token that reached a URL has already been written to a log line")
	}
}

// refusalOf reads the OAuth error a token or revocation endpoint answered with.
// It is not the redirect reader above: these two endpoints answer a refusal in
// the body, because there is no redirect URI to send it to and no page to render.
type oauthRefusal struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

func refusalOf(t *testing.T, rec *httptest.ResponseRecorder) oauthRefusal {
	t.Helper()
	var out oauthRefusal
	decodeBody(t, rec, &out)
	return out
}

// TestTokenEndpointRefusesTheWrongClientSecret: a client that presents a secret
// which is not the one it registered with is refused at the token endpoint — on
// every grant, including the two that do not require one.
//
// Those two are the reason the check exists at all. A native client is not asked
// to keep a secret (PKCE is what proves its code), so a secret it does present
// is one it believes in, and reading a wrong one as "presented none" would hand
// a token to a client that is confused about its own credentials — the one
// confusion an attacker who has half a client's configuration can arrange.
func TestTokenEndpointRefusesTheWrongClientSecret(t *testing.T) {
	ts := newTestServer(t)
	clientID, tokens := ts.runFlow(t, testClientRedirect)

	rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
		"client_secret": {"not-the-secret-this-client-registered-with"},
	})
	if rec.Code != http.StatusUnauthorized || refusalOf(t, rec).Error != string(oidc.InvalidClient) {
		t.Errorf("the refresh grant with a wrong client secret answered %d (%s); want 401 %s: a secret that is presented is verified, and a wrong one is not read as a client that presented none", rec.Code, strings.TrimSpace(rec.Body.String()), oidc.InvalidClient)
	}
	// And the family is where it was: a refused authentication spends nothing.
	if rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
	}); rec.Code != http.StatusOK {
		t.Errorf("the refresh grant with no secret answered %d (%s); want the rotation: the refusal above is about the secret and not about the token", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// TestClientSecretBasicIsReadAndRefusedBesideTheForm: a client authenticates
// with the Authorization header or with the form's own pair, both are read, and
// a request that carries both is refused (RFC 6749 §2.3.1).
//
// The header half is not decoration. A standard MCP client sends
// client_secret_basic, so the header path is the one most callers take, and the
// rule that the two are exclusive is checked rather than assumed because the two
// are not equivalent: a proxy that read one of them and this server that read
// the other would be two parties disagreeing about who is calling.
//
// Every refusal below is answered before the grant is read, so the code the
// success case at the end spends is still unspent when it gets there — which is
// also what says the checks run in that order.
func TestClientSecretBasicIsReadAndRefusedBesideTheForm(t *testing.T) {
	ts := newTestServer(t)
	clientID, secret := ts.register(t, "an editor", testClientRedirect)
	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code := codeOf(t, ts.approve(t, binding, number))

	codeGrant := func() url.Values {
		return url.Values{
			"grant_type":    {string(GrantAuthorizationCode)},
			"code":          {code},
			"redirect_uri":  {testClientRedirect},
			"code_verifier": {testVerifier},
		}
	}

	// The header and the form together: refused, and refused as the client
	// having authenticated twice rather than as one of the two being wrong.
	both := codeGrant()
	both.Set("client_id", clientID)
	both.Set("client_secret", secret)
	if rec := ts.postFormWithAuth(t, TokenPath, both, basicAuth(clientID, secret)); rec.Code != http.StatusUnauthorized ||
		refusalOf(t, rec).Error != string(oidc.InvalidClient) {
		t.Errorf("a request carrying client_secret_basic and the form's own pair answered %d (%s); want 401 %s", rec.Code, strings.TrimSpace(rec.Body.String()), oidc.InvalidClient)
	} else if got := refusalOf(t, rec).Description; !strings.Contains(got, "twice") {
		t.Errorf("the refusal for authenticating twice says %q; want it to name the rule, so a client is told which of the two it has to drop", got)
	}

	// A header that is not a Basic credential at all: refused, and never
	// ignored in favour of the form's own pair, which is valid here — a server
	// that read one and a proxy that read the other would be two parties
	// disagreeing about who is calling.
	malformed := codeGrant()
	malformed.Set("client_id", clientID)
	malformed.Set("client_secret", secret)
	if rec := ts.postFormWithAuth(t, TokenPath, malformed, "Basic not-base64-not-a-credential"); rec.Code != http.StatusUnauthorized ||
		refusalOf(t, rec).Error != string(oidc.InvalidClient) {
		t.Errorf("an Authorization header that is not client_secret_basic answered %d (%s); want 401 %s", rec.Code, strings.TrimSpace(rec.Body.String()), oidc.InvalidClient)
	} else if got := refusalOf(t, rec).Description; !strings.Contains(got, "client_secret_basic") {
		t.Errorf("the refusal for a header that is not a Basic credential says %q; want it to name the credential the header has to be, so a client is told what to send instead of being read as one that sent nothing", got)
	}

	// A header carrying the wrong secret: the secret is verified wherever it
	// arrived from.
	if rec := ts.postFormWithAuth(t, TokenPath, codeGrant(), basicAuth(clientID, "not-the-secret-this-client-registered-with")); rec.Code != http.StatusUnauthorized ||
		refusalOf(t, rec).Error != string(oidc.InvalidClient) {
		t.Errorf("client_secret_basic with a wrong secret answered %d (%s); want 401 %s", rec.Code, strings.TrimSpace(rec.Body.String()), oidc.InvalidClient)
	}

	// And the header alone authenticates: the form carries no client_id and no
	// client_secret, and the grant is answered.
	rec, tokens := ts.exchangeWithAuth(t, codeGrant(), basicAuth(clientID, secret))
	if rec.Code != http.StatusOK || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("the code grant authenticated by client_secret_basic alone answered %d (%s); want a token pair: the header is one of the two ways a client authenticates, and the form's pair is the other", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// TestAClientAuthRefusalNamesTheScheme is RFC 6749 §5.2's own rule for the 401
// this server answers a failed client authentication with: when the client
// authenticated with the Authorization header, that answer carries a
// WWW-Authenticate naming the scheme, so a client that guessed one is told
// which scheme this server reads instead of being left to guess again.
//
// The header is there exactly when the client used one, which is the case §5.2
// makes the MUST: a request whose credentials are in the form used no scheme,
// and its answer is the one it always was.
func TestAClientAuthRefusalNamesTheScheme(t *testing.T) {
	ts := newTestServer(t)
	clientID, tokens := ts.runFlow(t, testClientRedirect)
	refused := basicAuth(clientID, "not-the-secret-this-client-registered-with")

	for _, tc := range []struct {
		what string
		path string
		form url.Values
	}{
		{"the token endpoint", TokenPath, url.Values{
			"grant_type":    {string(GrantAuthorizationCode)},
			"code":          {"not-a-code-this-installation-issued"},
			"redirect_uri":  {testClientRedirect},
			"code_verifier": {testVerifier},
		}},
		{"the revocation endpoint", RevokePath, url.Values{"token": {tokens.RefreshToken}}},
	} {
		rec := ts.postFormWithAuth(t, tc.path, tc.form, refused)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s answered client_secret_basic with a wrong secret as %d (%s); want 401", tc.what, rec.Code, strings.TrimSpace(rec.Body.String()))
			continue
		}
		challenge := rec.Header().Get("WWW-Authenticate")
		if !strings.HasPrefix(challenge, "Basic ") || !strings.Contains(challenge, testIssuer) {
			t.Errorf("%s answered 401 with WWW-Authenticate %q; want a Basic challenge naming this installation: the header tells a client which scheme this server reads from the Authorization header, and it is the one place that answer is written", tc.what, challenge)
		}
	}

	// The same refusal with the credentials in the form. No scheme was used, so
	// there is none to name, and the answer names none.
	form := url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {"not-a-code-this-installation-issued"},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {clientID},
		"client_secret": {"not-the-secret-this-client-registered-with"},
		"code_verifier": {testVerifier},
	}
	rec := ts.postForm(t, TokenPath, form)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("the token endpoint answered a wrong client_secret in the form as %d (%s); want 401", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("a refusal of credentials carried in the form sent WWW-Authenticate %q; want none: the header names the scheme the client authenticated with, and this one authenticated with the form's own two fields", got)
	}
}

// TestRevokeRefusesTheWrongClientSecret: the revocation endpoint authenticates
// its caller, and a revocation that did not authenticate revokes nothing.
//
// The endpoint answers 200 to every token it is asked to revoke (RFC 7009), so
// its secret check is the one place it refuses at all — and the second half
// below is what makes the refusal mean something: the family is still there
// afterwards, read by rotating the very token the refused call named.
func TestRevokeRefusesTheWrongClientSecret(t *testing.T) {
	ts := newTestServer(t)
	clientID, tokens := ts.runFlow(t, testClientRedirect)

	rec := ts.postForm(t, RevokePath, url.Values{
		"token":         {tokens.RefreshToken},
		"client_id":     {clientID},
		"client_secret": {"not-the-secret-this-client-registered-with"},
	})
	if rec.Code != http.StatusUnauthorized || refusalOf(t, rec).Error != string(oidc.InvalidClient) {
		t.Errorf("the revocation with a wrong client secret answered %d (%s); want 401 %s", rec.Code, strings.TrimSpace(rec.Body.String()), oidc.InvalidClient)
	}
	if rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
	}); rec.Code != http.StatusOK {
		t.Errorf("the refresh token stopped rotating after a refused revocation (%d): a revocation that did not authenticate deletes no family", rec.Code)
	}
}

// TestRevokeRefusesATokenAnotherClientHolds is the second half of "a caller may
// revoke what it holds, and nothing else" (token.go): the secret check above
// refuses a caller that cannot authenticate, and this refuses one that
// authenticates perfectly well with its own credentials and names somebody
// else's token.
//
// The token endpoint answers 200 to every revocation (RFC 7009), so nothing in
// the response tells the two apart and the property has to be read where it
// lives: the family is still there afterwards, which is checked by rotating the
// very token the refused call named. The control at the end is the same call by
// the client that does hold the token — without it, a server that revoked
// nothing at all would pass the first half.
func TestRevokeRefusesATokenAnotherClientHolds(t *testing.T) {
	ts := newTestServer(t)
	holderID, holderSecret := ts.register(t, "an editor", testClientRedirect)
	otherID, otherSecret := ts.register(t, "a second client", testOtherRedirect)

	binding, number := ts.startFlow(t, authorizeQuery(holderID, testClientRedirect))
	code := codeOf(t, ts.approve(t, binding, number))
	rec, tokens := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {holderID},
		"code_verifier": {testVerifier},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("the code exchange answered %d (%s); want a token pair", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	rec = ts.postForm(t, RevokePath, url.Values{
		"token":         {tokens.RefreshToken},
		"client_id":     {otherID},
		"client_secret": {otherSecret},
	})
	if rec.Code != http.StatusOK {
		t.Errorf("a second client authenticating with its own credentials answered %d (%s); want 200: RFC 7009 answers every revocation the same way, so the refusal has to be that the token is not revoked", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	rec, rotated := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {holderID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("the holder's refresh token stopped rotating after another client named it (%d: %s); want the pair: a caller may revoke what it holds, and nothing else", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// The control: the client that does hold the token revokes it, and the same
	// rotation is refused afterwards.
	rec = ts.postForm(t, RevokePath, url.Values{
		"token":         {rotated.RefreshToken},
		"client_id":     {holderID},
		"client_secret": {holderSecret},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("the holder's own revocation answered %d (%s); want 200", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	rec, _ = ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {rotated.RefreshToken},
		"client_id":     {holderID},
	})
	if rec.Code != http.StatusBadRequest || refusalOf(t, rec).Error != string(oidc.InvalidGrant) {
		t.Errorf("the revoked token rotated anyway with %d (%s); want 400 %s: the holder's own revocation has to be the one that ends the family, or the first half above proves nothing", rec.Code, strings.TrimSpace(rec.Body.String()), oidc.InvalidGrant)
	}
}

// TestCodeIsBoundToTheClientThatRequestedIt: a code is redeemed by the client it
// was issued to, and by no other.
//
// The second half is the fail-closed direction this endpoint takes on purpose:
// the code is spent by the exchange that presents it, before the client is even
// looked at, so the client the code was issued to cannot redeem it afterwards
// either. One page of re-authorization is what that costs, and a code an
// attacker who read it out of a browser's history can keep guessing verifiers
// against is what it buys.
func TestCodeIsBoundToTheClientThatRequestedIt(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	otherID, _ := ts.register(t, "a second client", testOtherRedirect)

	binding, number := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code := codeOf(t, ts.approve(t, binding, number))

	form := url.Values{
		"grant_type":    {string(GrantAuthorizationCode)},
		"code":          {code},
		"redirect_uri":  {testClientRedirect},
		"client_id":     {otherID},
		"code_verifier": {testVerifier},
	}
	rec, _ := ts.exchange(t, form)
	if rec.Code != http.StatusBadRequest || refusalOf(t, rec).Error != string(oidc.InvalidGrant) {
		t.Errorf("a second client exchanged another client's code with %d (%s); want 400 %s: the code is bound to the client it was issued to", rec.Code, strings.TrimSpace(rec.Body.String()), oidc.InvalidGrant)
	}

	form.Set("client_id", clientID)
	if rec, _ := ts.exchange(t, form); rec.Code == http.StatusOK {
		t.Errorf("the client the code was issued to redeemed it after another client had presented it: the exchange spends the code before it is judged, so a refused exchange leaves nothing to keep guessing verifiers against")
	}
}

// TestRefreshTokenIsBoundToItsClient: a refresh token is rotated by the client it
// was issued to, and by no other — and the refusal leaves the family where it
// was, so the client that holds the token keeps working.
//
// That last property is why the check is made before the rotation rather than by
// it: the store's compare-and-swap must not be asked to decide something the
// caller could have refused, because its answer to a replay of a token it does
// not know is to delete the whole family.
func TestRefreshTokenIsBoundToItsClient(t *testing.T) {
	ts := newTestServer(t)
	clientID, tokens := ts.runFlow(t, testClientRedirect)
	otherID, _ := ts.register(t, "a second client", testOtherRedirect)

	rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {otherID},
	})
	if rec.Code != http.StatusBadRequest || refusalOf(t, rec).Error != string(oidc.InvalidGrant) {
		t.Errorf("a second client rotated another client's refresh token with %d (%s); want 400 %s: the refresh token is bound to the client it was issued to", rec.Code, strings.TrimSpace(rec.Body.String()), oidc.InvalidGrant)
	}
	if rec, _ := ts.exchange(t, url.Values{
		"grant_type":    {string(GrantRefreshToken)},
		"refresh_token": {tokens.RefreshToken},
		"client_id":     {clientID},
	}); rec.Code != http.StatusOK {
		t.Errorf("the refresh token stopped rotating after another client presented it (%d): the client binding is checked before the rotation, so a refusal does not revoke the family of the client that holds the token", rec.Code)
	}
}

// TestClientCredentialsIsRefused is D929 amended (finding M-f): the client
// credentials grant stays off until it has an owner, so the token endpoint
// refuses it — and the task builds the refusal, not the grant.
//
// The refusal is `unsupported_grant_type` and it is made before the client is
// read, which the last case below is what holds: an unknown client id gets the
// same answer as a registered one, so the refusal is about the grant and not
// about who asked. An answer that depended on the client would be a way to learn
// which client ids exist from a public endpoint.
func TestClientCredentialsIsRefused(t *testing.T) {
	ts := newTestServer(t)
	clientID, secret := ts.register(t, "an editor", testClientRedirect)

	for _, form := range []url.Values{
		{"grant_type": {string(GrantClientCredentials)}, "client_id": {clientID}},
		{"grant_type": {string(GrantClientCredentials)}, "client_id": {clientID}, "client_secret": {secret}},
		{"grant_type": {string(GrantClientCredentials)}, "client_id": {"a-client-this-installation-never-registered"}},
	} {
		rec := ts.postForm(t, TokenPath, form)
		if rec.Code != http.StatusBadRequest || refusalOf(t, rec).Error != string(oidc.UnsupportedGrantType) {
			t.Errorf("the client credentials grant answered %d (%s); want 400 %s: D929 amended keeps the grant off until it has an owner, and this task builds the refusal rather than the grant", rec.Code, strings.TrimSpace(rec.Body.String()), oidc.UnsupportedGrantType)
		}
		if strings.Contains(rec.Body.String(), "access_token") {
			t.Errorf("the client credentials grant answered with a token: %s", strings.TrimSpace(rec.Body.String()))
		}
	}
	// Nothing was minted: a refused grant writes no family and no token record,
	// so the store holds the client and nothing else.
	at := ts.store.objects(t)
	if len(at) != 1 {
		t.Errorf("the store holds %d objects after three refused client-credentials requests; want the one client record", len(at))
	}
	for key := range at {
		if strings.HasPrefix(key, familyPrefix) || strings.HasPrefix(key, tokenPrefix) {
			t.Errorf("%s was written by a grant this server refuses: no token and no family may exist for a client credentials request", key)
		}
	}
}

// TestTheAuthorizeDoorRereadsTheURIRule is D928's rule at its second door: a
// client record this endpoint did not write — an operator's pre-registration,
// which D929 allows — is judged at the request by the same rule registration
// applies, so a URI the project does not send codes to is refused here too.
//
// The record is written into the store directly, because /oauth/register cannot
// produce one: it validates before it writes. That is the whole case for the
// second check — it exists for the door that did not write the record.
func TestTheAuthorizeDoorRereadsTheURIRule(t *testing.T) {
	const (
		clientID     = "a-pre-registered-client"
		offAllowlist = "https://somewhere.example.org/callback"
	)
	ts := newTestServer(t)
	if err := ts.store.PutClient(context.Background(), Client{
		ID:           clientID,
		Name:         "an operator's own client",
		RedirectURIs: []string{offAllowlist, testClientRedirect},
	}); err != nil {
		t.Fatalf("writing the pre-registered client: %v", err)
	}

	rec := ts.authorizeRequest(t, authorizeQuery(clientID, offAllowlist))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET %s named a redirect URI this project does not send codes to, listed by a record this door did not write, and answered %d (%s); want the refusal page: the rule is the project's, not the door's (D928)",
			AuthorizePath, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	// The other URI in the same record is loopback, which the rule always
	// allows — so the refusal above is the rule and not the record.
	if rec := ts.authorizeRequest(t, authorizeQuery(clientID, testClientRedirect)); rec.Code != http.StatusOK {
		t.Errorf("GET %s named the loopback URI the same record lists and answered %d (%s); want the consent page: loopback is always allowed, and the refusal above is therefore about the URI and not about the client",
			AuthorizePath, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}
