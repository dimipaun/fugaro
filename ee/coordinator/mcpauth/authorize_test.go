// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// This file is M5's half of the flow: the authorize endpoint's two methods, the
// token endpoint's two interactive grants, and the four rules the design puts
// between them — PKCE, single-use codes, exact redirect URIs and D928's CSRF
// binding.
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

// authorizeRequest makes the authorization request, carrying the person's
// Firebase ID token when there is one.
func (ts *testServer) authorizeRequest(t *testing.T, q url.Values, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, AuthorizePath+"?"+q.Encode(), nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return ts.do(t, req)
}

// consentBindingRE reads the binding out of the consent page, which is how a
// browser would carry it: the test does not know the record's id in advance and
// must not.
var consentBindingRE = regexp.MustCompile(`name="request_id" value="([^"]+)"`)

// startFlow makes one whole authorization request with the person's ID token
// and answers the binding the consent page carries.
func (ts *testServer) startFlow(t *testing.T, q url.Values) string {
	t.Helper()
	rec := ts.authorizeRequest(t, q, acceptedToken(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s answered %d (%s); want the consent page: the ID token is valid, the client is registered and the request is complete", AuthorizePath, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	m := consentBindingRE.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("the consent page carries no request_id: without the flow's own binding there is nothing a consent can be checked against, and D928's CSRF rule has nothing to hold")
	}
	return m[1]
}

// consent answers the page above.
func (ts *testServer) consent(t *testing.T, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return ts.postForm(t, AuthorizePath, form)
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

// runFlow is one whole authorization: register, authorize, consent, exchange.
func (ts *testServer) runFlow(t *testing.T, redirect string) (clientID string, tokens tokenResponse) {
	t.Helper()
	clientID, _ = ts.register(t, "an editor", redirect)
	binding := ts.startFlow(t, authorizeQuery(clientID, redirect))
	code := codeOf(t, ts.consent(t, url.Values{"request_id": {binding}, "decision": {"allow"}}))
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
	if got := oauthErrorOf(t, ts.authorizeRequest(t, q, acceptedToken(t)), host); got != string(oidc.InvalidRequest) {
		t.Errorf("an authorization request with no code_challenge was refused with %q; want %q, and it has to be refused rather than answered", got, oidc.InvalidRequest)
	}

	q = authorizeQuery(clientID, testClientRedirect)
	q.Set("code_challenge_method", "plain")
	if got := oauthErrorOf(t, ts.authorizeRequest(t, q, acceptedToken(t)), host); got != string(oidc.InvalidRequest) {
		t.Errorf("an authorization request with a plain challenge was refused with %q; want %q: a plain challenge is the verifier itself and this server accepts S256 alone", got, oidc.InvalidRequest)
	}

	// And the exchange is where the challenge is actually checked: a code
	// issued for one verifier is not redeemable with another.
	binding := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code := codeOf(t, ts.consent(t, url.Values{"request_id": {binding}, "decision": {"allow"}}))
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
	binding := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code := codeOf(t, ts.consent(t, url.Values{"request_id": {binding}, "decision": {"allow"}}))
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
	binding = ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code = codeOf(t, ts.consent(t, url.Values{"request_id": {binding}, "decision": {"allow"}}))
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
		"http://127.0.0.1:8976/callback/",
		"http://127.0.0.1:8977/callback",
		"http://127.0.0.1:8976/Callback",
		"https://client.example.net/callback",
		"",
	} {
		rec := ts.authorizeRequest(t, authorizeQuery(clientID, uri), acceptedToken(t))
		if rec.Code == http.StatusOK {
			t.Errorf("GET %s rendered a consent page for redirect_uri %q, which this client did not register: the match is exact (D928)", AuthorizePath, uri)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("GET %s answered redirect_uri %q with a redirect to %q: an error must not be sent to a URI the client did not register", AuthorizePath, uri, loc)
		}
	}

	if rec := ts.authorizeRequest(t, authorizeQuery(clientID, testClientRedirect), acceptedToken(t)); rec.Code != http.StatusOK {
		t.Errorf("GET %s refused the URI the client registered (%d): the rule is exactness, not exclusion", AuthorizePath, rec.Code)
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
func TestConsentNamesTheRedirectHost(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testAllowlistedRedirect)

	rec := ts.authorizeRequest(t, authorizeQuery(clientID, testAllowlistedRedirect), acceptedToken(t))
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
	if !strings.Contains(page, "user:"+testEmail) {
		t.Errorf("the consent page does not name the identity the sign-in proved (%q): the person is consenting on behalf of somebody, and the page has to say who", "user:"+testEmail)
	}
}

// TestConsentIsCSRFBound is D928's third tightening: the consent carries the
// flow's own binding, a consent that does not is refused, and the state returns
// on the redirect unaltered.
//
// The third assertion is the one that holds the whole rule: a consent that
// carried the right binding AND a redirect URI of its own choosing still goes
// where the record says, because the record is the only place any of it is read
// from. Without that, a binding would be a token an attacker could present
// beside values of their own.
func TestConsentIsCSRFBound(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	host := redirectHost(testClientRedirect)

	binding := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))

	for _, form := range []url.Values{
		{"decision": {"allow"}},
		{"request_id": {"not-the-binding"}, "decision": {"allow"}},
	} {
		rec := ts.consent(t, form)
		if rec.Code == http.StatusFound {
			t.Errorf("a consent carrying %v was honoured: a consent that does not carry the flow's own binding is refused rather than answered, or an attacker's client finishes a sign-in the person started for another", form)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("a consent carrying %v answered with a redirect to %q: nothing is known about the flow, so there is nowhere safe to send an answer", form, loc)
		}
	}
	if got := ts.store.objects(t); len(got) != 2 {
		t.Errorf("the store holds %d objects after two refused consents; want the client and the pending request and no code", len(got))
	}

	// The real consent, with a redirect URI of the caller's own choosing beside
	// the binding: the code goes to the record's URI, and the state comes back
	// as the client sent it.
	rec := ts.consent(t, url.Values{
		"request_id":   {binding},
		"decision":     {"allow"},
		"redirect_uri": {"https://evil.example.org/callback"},
	})
	if rec.Code != http.StatusFound {
		t.Fatalf("the consent answered %d (%s); want a redirect to the client's own URI", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("the redirect is not a URL: %v", err)
	}
	if u.Host != host {
		t.Errorf("the code was sent to %q; want the URI the flow was recorded with (%q): everything the consent decides is read out of the record and never out of the consent", u.Host, host)
	}
	if got := u.Query().Get("state"); got != testState {
		t.Errorf("the redirect carries state %q; want %q unaltered (D928)", got, testState)
	}
	if u.Query().Get("code") == "" {
		t.Errorf("the redirect carries no code: %s", u.RawQuery)
	}

	// And the flow is answered once.
	if again := ts.consent(t, url.Values{"request_id": {binding}, "decision": {"allow"}}); again.Code == http.StatusFound {
		t.Errorf("the same binding was answered twice: one binding answers one consent, and the second is refused rather than honoured")
	}
}
