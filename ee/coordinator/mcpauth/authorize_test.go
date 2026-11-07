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

// TestAuthorizeRefusesAnUnverifiedSignIn: the authorize endpoint's sign-in step
// is what the whole consent page rests on, and neither of the two ways it can
// fail renders one.
//
// The two ways are the two branches of verifySignIn: a request that carries no
// Bearer credential at all, and one that carries a credential this installation
// will not verify — here, a token another project's Firebase signed, which is
// the refusal M6's own tests hold one layer down. What this test adds is that
// the endpoint does not go on regardless: an unverified sign-in is a 401 page,
// never a consent page and never a redirect to the client, because there is no
// person yet whose consent the page could be about.
func TestAuthorizeRefusesAnUnverifiedSignIn(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)
	otherProjectToken := signJWT(t, testKey(), testKid, "RS256", idTokenClaims(testOtherProject))

	for _, tc := range []struct{ what, header string }{
		{"no Authorization header at all", ""},
		{"a scheme that is not Bearer", "Basic cGVyc29uLTE6bWFya2Vy"},
		{"Bearer with no token after it", "Bearer"},
		{"Bearer with an empty token", "Bearer "},
		{"a token another installation's Firebase signed", "Bearer " + otherProjectToken},
	} {
		req := httptest.NewRequest(http.MethodGet, AuthorizePath+"?"+authorizeQuery(clientID, testClientRedirect).Encode(), nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		rec := ts.do(t, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("an authorization request with %s answered %d (%s); want 401: an unverified sign-in is refused rather than rendered as a consent page", tc.what, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("an authorization request with %s answered with a redirect to %q: nothing is known about the person yet, so there is no flow to send an answer to", tc.what, loc)
		}
		if strings.Contains(rec.Body.String(), otherProjectToken) {
			t.Errorf("the refusal for %s repeats the token: a token rendered into a page is a token in a browser's history", tc.what)
		}
	}
	// Nothing was recorded for any of them: the pending request is what the
	// consent page binds to, and a refused sign-in writes none.
	if got := ts.store.objects(t); len(got) != 1 {
		t.Errorf("the store holds %d objects after five refused sign-ins; want the one client record and no pending request", len(got))
	}

	// The same request with a token this installation signed is answered — under
	// either spelling of the scheme, which RFC 7235 §2.1 makes case-insensitive —
	// so the refusals above are about the token and not about the request.
	for _, scheme := range []string{"Bearer ", "bearer "} {
		req := httptest.NewRequest(http.MethodGet, AuthorizePath+"?"+authorizeQuery(clientID, testClientRedirect).Encode(), nil)
		req.Header.Set("Authorization", scheme+acceptedToken(t))
		if rec := ts.do(t, req); rec.Code != http.StatusOK {
			t.Errorf("the same request with %q answered %d (%s); want the consent page", scheme, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
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

	binding := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect))
	code := codeOf(t, ts.consent(t, url.Values{"request_id": {binding}, "decision": {"allow"}}))

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

// TestClientCredentialsNeedsTheSecret is D929's other half: the one grant with no
// person behind it is the one grant proved by the client secret, and a client
// that presents none is refused before its record is even read for limits.
//
// The pair below is the whole rule. Without the secret the answer is 401; with
// it, a registration's own record — which names no limits — answers 400. Neither
// is a token, and the first is what keeps an unattended caller from being made
// out of a registration nobody proved.
func TestClientCredentialsNeedsTheSecret(t *testing.T) {
	ts := newTestServer(t)
	clientID, secret := ts.register(t, "an editor", testClientRedirect)

	rec := ts.postForm(t, TokenPath, url.Values{
		"grant_type": {string(GrantClientCredentials)},
		"client_id":  {clientID},
	})
	if rec.Code != http.StatusUnauthorized || refusalOf(t, rec).Error != string(oidc.InvalidClient) {
		t.Errorf("the client credentials grant with no secret answered %d (%s); want 401 %s: this grant is proved by the secret and by nothing else", rec.Code, strings.TrimSpace(rec.Body.String()), oidc.InvalidClient)
	}

	rec = ts.postForm(t, TokenPath, url.Values{
		"grant_type":    {string(GrantClientCredentials)},
		"client_id":     {clientID},
		"client_secret": {secret},
	})
	if rec.Code != http.StatusBadRequest || refusalOf(t, rec).Error != string(oidc.UnauthorizedClient) {
		t.Errorf("the client credentials grant with the secret but no limits answered %d (%s); want 400 %s: an unattended token without limits is the one token §7 refuses to mint (D929)", rec.Code, strings.TrimSpace(rec.Body.String()), oidc.UnauthorizedClient)
	}
}
