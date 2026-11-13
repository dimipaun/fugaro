// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The licence site's sign-in, authorize and device code (K2.4; spec §11.3,
// §11.4; D2144, TN3). Every test here drives the routes through the handler,
// because what these routes owe is an answer to a caller: the uniform refusal,
// the code that is good once, the key handed to the account whose verified
// address bought the licence.
//
// The ID tokens are signed by a key generated as the test runs and never read
// from a file (.claude/rules/ee.md, R5: no private half in a fixture), and they
// are verified by the coordinator's own verifier rather than by a stand-in for
// one. That is the point of the wiring below: what these tests pin is that the
// intake reads the licence project's tokens by the rules `mcpauth` already
// states — the audience, the issuer, the verified address — so a second
// implementation of them would pass a stub here and fail this file.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
)

const (
	// licenceProject is the owner's licence project (TN3, D2144): the Firebase
	// project whose tokens the site's sign-in reads, and the audience every
	// admitted token carries. It is not a secret and it is not a customer's
	// workspace project — that distinction is what the sign-in is for.
	licenceProject = "fugaro-licence-test"
	// anotherProject is an installation this service must not read a token for,
	// and anotherKeyID is a key id the licence project does not publish.
	anotherProject = "another-installation-test"
	anotherKeyID   = "published-key-not-this-one"
	licenceKeyID   = "published-key-1"

	// signInUID and signInAddress are the identity an admitted token carries,
	// and the address is the buyer's: a licence is bought by an address
	// (`buyerAddress`), and the account that holds it is the account whose
	// **verified** address is that one.
	signInUID     = "person-1"
	signInAddress = "buyer@acme.example"
	signInOrg     = "acme.example"

	// The CLI's loopback listener (K3.3): the literal address, the port it
	// bound, and the path §11.3's redirect lands on. Neither the port nor the
	// origin is real.
	loopbackPort   = "8765"
	loopbackTarget = "http://127.0.0.1:" + loopbackPort + callbackPath
	signInState    = "the-clis-own-state-0123456789"

	// hostileRedirect is where an authorize request that named it would send the
	// code if this service ever echoed a caller's own target back.
	hostileRedirect = "http://collect.example/callback"
)

// signInKey is the licence project's signing key, generated once for the whole
// file: a 2048-bit keygen is the slowest thing here and no test needs its own.
var signInKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("keyservice test: generating the licence project's key: " + err.Error())
	}
	return key
})

// publishedKeys is the licence project's published key set in a test. It is the
// shape `mcpauth.KeySource` names, with the same "not here" answer the real one
// gives, and it holds a public half alone (R5).
type publishedKeys map[string]*rsa.PublicKey

func (p publishedKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	key, known := p[kid]
	if !known {
		return nil, fmt.Errorf("%w: the licence project publishes no key with that id", mcpauth.ErrUnknownKeyID)
	}
	return key, nil
}

// signIDToken is one ID token as the licence project's own signer would produce
// it: a compact RS256 JWT.
func signIDToken(t *testing.T, key *rsa.PrivateKey, kid, alg string, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": alg, "kid": kid, "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshalling the header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling the claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing the token: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// licenceClaims is a token the site's sign-in admits: the identity the licence
// project's signer produced, inside the window the fixture's clock is at.
func (f *fixture) licenceClaims(project string) map[string]any {
	return map[string]any{
		"iss":            "https://securetoken.google.com/" + project,
		"aud":            project,
		"sub":            signInUID,
		"user_id":        signInUID,
		"email":          signInAddress,
		"email_verified": true,
		"iat":            f.now.Add(-time.Minute).Unix(),
		"exp":            f.now.Add(time.Hour).Unix(),
	}
}

// acceptedToken is the one-liner the ordinary case is written against: the
// licence project's own token for the buyer's address.
func (f *fixture) acceptedToken(t *testing.T) string {
	t.Helper()
	return signIDToken(t, signInKey(), licenceKeyID, "RS256", f.licenceClaims(licenceProject))
}

// tokenWith is the same token with one claim changed, which is how a test names
// the rule it is about rather than the shape of a token.
func (f *fixture) tokenWith(t *testing.T, claim string, value any) string {
	t.Helper()
	claims := f.licenceClaims(licenceProject)
	claims[claim] = value
	return signIDToken(t, signInKey(), licenceKeyID, "RS256", claims)
}

// signingIn is the fixture with the two doors these routes need wired: the
// licence project's own verifier behind Deps.SignIn, and a v1 minter behind
// Deps.MintsV1 — the same minter the fixture signs with, so a key a test reads
// back is one ee/licence's verifier accepts.
func signingIn(t *testing.T) (*fixture, *v1Spy) {
	t.Helper()
	return signingInWithin(t, Limits{Requests: 50, Window: time.Hour})
}

// signingInWithin is the same fixture with the intake's own rate the test's,
// which is what a test about the throttle needs and no other test ever meets.
func signingInWithin(t *testing.T, limits Limits) (*fixture, *v1Spy) {
	t.Helper()
	return signingInWith(t, func(f *fixture) { f.cfg.Throttle = limits })
}

// signingInWith is the fixture the two above are built on, with one more edit
// for the tests about the store. The mint is the one write on this path, so what
// a store that refuses the write — or that takes longer over it than the poll
// interval — does to a code is a thing only a swapped store can show.
func signingInWith(t *testing.T, edit func(*fixture)) (*fixture, *v1Spy) {
	t.Helper()
	spy := &v1Spy{}
	f := newFixtureWith(t, Production, func(f *fixture) {
		spy.inner = Minter{Signer: f.signer, Keys: f.keys, Environment: f.env, Now: f.clock}
		f.deps.MintsV1 = spy
		f.cfg.Throttle = Limits{Requests: 50, Window: time.Hour}
		verifier, err := mcpauth.NewVerifier(licenceProject,
			publishedKeys{licenceKeyID: &signInKey().PublicKey},
			mcpauth.WithVerifyClock(f.clock))
		if err != nil {
			t.Fatalf("building the licence project's verifier: %v", err)
		}
		f.deps.SignIn = verifier
		edit(f)
	})
	return f, spy
}

// subscribed is the fixture with a subscription the buyer's address bought:
// what the purchaser binding binds to. The purchase is the merchant's own event
// (§5.1), so the subscription, the record and the buyer's address are the ones
// the paid path really writes rather than ones a test invented.
func subscribed(t *testing.T) (*fixture, *v1Spy) {
	t.Helper()
	f, spy := signingIn(t)
	f.purchase("evt-signin", signInOrg, 5, 365)
	return f, spy
}

// subscribedWithStore is `subscribed` behind a store of the test's choosing,
// built around the fixture's own store so the subscription the merchant's
// purchase wrote is the one the mint reads back. The purchase is delivered
// before the swap and the intake is built again around it, so the store the
// wrapper sits on has the purchase in it and every write through the wrapper is
// this flow's own.
func subscribedWithStore(t *testing.T, wrap func(*fixture, *Memory) Store) (*fixture, *v1Spy) {
	t.Helper()
	f, spy := signingInWith(t, func(*fixture) {})
	f.purchase("evt-signin", signInOrg, 5, 365)
	f.deps.Store = wrap(f, f.store)
	f.start()
	return f, spy
}

// slowIssuances is the store as a deployment whose write outlives the poll
// interval: the fixture's clock moves by what the write "took", so a test can
// put the next poll a full interval behind the first one's claim while the first
// one's mint is still running.
type slowIssuances struct {
	*Memory
	took    time.Duration
	advance func(time.Duration)
}

func (s *slowIssuances) SaveIssuance(ctx context.Context, r Record) error {
	if err := s.Memory.SaveIssuance(ctx, r); err != nil {
		return err
	}
	s.advance(s.took)
	return nil
}

// freshVerifier is a PKCE verifier generated as the test runs: 256 bits of the
// system's randomness, which is what the CLI's own verifier is (RFC 7636's
// bound is 43 characters, and this is exactly that).
func freshVerifier(t *testing.T) string {
	t.Helper()
	return freshSecret(t)
}

// pkceChallengeOf is the S256 challenge a verifier is exchanged against.
func pkceChallengeOf(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorizeQuery is the query the CLI sends to §11.3's route (K3.3's URL): the
// loopback port, its own state and the challenge.
func authorizeQuery(verifier string) url.Values {
	return url.Values{
		"port":                  {loopbackPort},
		"state":                 {signInState},
		"code_challenge":        {pkceChallengeOf(verifier)},
		"code_challenge_method": {pkceMethod},
	}
}

// signedIn submits the sign-in form the way the site's page does.
func (f *fixture) signedIn(t *testing.T, token, verifier string) *httptest.ResponseRecorder {
	t.Helper()
	form := authorizeQuery(verifier)
	form.Set("redirect_uri", loopbackTarget)
	form.Set("id_token", token)
	return f.post(RouteAuthorize, form)
}

// redirected reads §11.3's step 4 back: the code and the state out of the
// Location header, and the target they were sent to — which every caller of
// this helper also checks is the CLI's own listener and nothing else.
func redirected(t *testing.T, rec *httptest.ResponseRecorder) (code, state, target string) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("the sign-in answered %d, want %d", rec.Code, http.StatusFound)
	}
	address, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parsing the redirect %q: %v", rec.Header().Get("Location"), err)
	}
	query := address.Query()
	return query.Get("code"), query.Get("state"), address.Scheme + "://" + address.Host + address.Path
}

// exchanged exchanges one authorize code for the bundle, with the verifier the
// challenge was made from.
func (f *fixture) exchanged(code, verifier string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.postJSON(RouteToken, map[string]string{"code": code, "code_verifier": verifier})
}

// bundle is the two lines every issue path shows once (D2135): the key and the
// secret that replaces it.
type bundle struct {
	Key           string `json:"key"`
	RenewalSecret string `json:"renewal_secret"`
}

// decodeBundle reads one of these routes' answers as the CLI reads it.
func decodeBundle(t *testing.T, rec *httptest.ResponseRecorder) bundle {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("the exchange answered %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var answer bundle
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("reading the answer %q: %v", rec.Body.String(), err)
	}
	if answer.Key == "" || answer.RenewalSecret == "" {
		t.Fatalf("the answer %q carries no key and no renewal secret", rec.Body.String())
	}
	return answer
}

// postJSON submits one of the flow's program-to-program calls.
func (f *fixture) postJSON(route string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.postJSONFrom(applicantIP, route, body)
}

func (f *fixture) postJSONFrom(ip, route string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		f.t.Fatalf("marshalling the call: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+route, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// TestAuthorizeRefusesANonLoopbackRedirect is §11.3's redirect target pinned:
// the code is a bearer credential for one exchange, and one sent to an address
// a caller can still choose is the key itself. So the target is the literal
// loopback address the CLI's listener is on, on the path the spec writes, and
// every spelling that is another address — a name, another scheme, a query, a
// port that is not a port, two spellings that disagree — is refused before
// anything is signed in.
func TestAuthorizeRefusesANonLoopbackRedirect(t *testing.T) {
	f, _ := signingIn(t)
	verifier := freshVerifier(t)
	cases := map[string]url.Values{
		"another host":                withRedirect("http://collect.example/callback", ""),
		"another scheme":              withRedirect("https://127.0.0.1:"+loopbackPort+callbackPath, ""),
		"a name and not an address":   withRedirect("http://localhost:"+loopbackPort+callbackPath, ""),
		"another path":                withRedirect("http://127.0.0.1:"+loopbackPort+"/elsewhere", ""),
		"a query on the target":       withRedirect(loopbackTarget+"?extra=1", ""),
		"a fragment":                  withRedirect(loopbackTarget+"#x", ""),
		"credentials in the target":   withRedirect("http://user@127.0.0.1:"+loopbackPort+callbackPath, ""),
		"a port that is not one":      withRedirect("", "80x"),
		"a port out of range":         withRedirect("", "70000"),
		"a port of zero":              withRedirect("", "0"),
		"a padded port":               withRedirect("", " "+loopbackPort),
		"two spellings that disagree": withRedirect("http://127.0.0.1:9999"+callbackPath, loopbackPort),
	}
	for name, query := range cases {
		query.Set("state", signInState)
		query.Set("code_challenge", pkceChallengeOf(verifier))
		query.Set("code_challenge_method", pkceMethod)
		if rec := f.get(RouteAuthorize, query); rec.Code != http.StatusForbidden {
			t.Errorf("the authorize form with %s answered %d, want %d: %s", name, rec.Code, http.StatusForbidden, rec.Body.String())
		}
	}
	// The address this service does send a code to is the one it rebuilt from
	// the port, and never a value the caller wrote: what the form carries back
	// is the literal loopback target and nothing else.
	rec := f.get(RouteAuthorize, authorizeQuery(verifier))
	if rec.Code != http.StatusOK {
		t.Fatalf("the loopback target's own form answered %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, loopbackTarget) {
		t.Errorf("the sign-in form does not carry the listener's own target %q: %s", loopbackTarget, body)
	}
}

// withRedirect is the same query with one spelling of the target set: the
// redirect_uri, the port, or both.
func withRedirect(redirectURI, port string) url.Values {
	query := url.Values{}
	if redirectURI != "" {
		query.Set("redirect_uri", redirectURI)
	}
	if port != "" {
		query.Set("port", port)
	}
	return query
}

// TestAuthorizeKeepsTheStateAndSendsTheCodeToTheListener: the state is the
// CLI's own value, handed over and compared by it when the callback arrives, so
// it comes back exactly as it was sent, and the code comes back with it to the
// listener the CLI is on.
func TestAuthorizeKeepsTheStateAndSendsTheCodeToTheListener(t *testing.T) {
	f, spy := subscribed(t)
	verifier := freshVerifier(t)
	form := f.get(RouteAuthorize, authorizeQuery(verifier))
	if form.Code != http.StatusOK {
		t.Fatalf("the sign-in form answered %d, want %d", form.Code, http.StatusOK)
	}
	// The form carries the person's token back, so it is not a page a shared
	// cache holds.
	if cache := form.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("the sign-in page is cacheable (%q), and it carries the person's own token", cache)
	}
	rec := f.signedIn(t, f.acceptedToken(t), verifier)
	code, state, target := redirected(t, rec)
	if state != signInState {
		t.Errorf("the redirect carries the state %q, want the one the CLI sent (%q): the CLI compares it, and a value this service chose is one it cannot check", state, signInState)
	}
	if target != loopbackTarget {
		t.Errorf("the code was sent to %q, want %q", target, loopbackTarget)
	}
	if code == "" {
		t.Fatal("the redirect carries no code")
	}
	// Nothing is minted at the sign-in: a browser that never follows the
	// redirect — the ordinary ending of a person who changes their mind —
	// leaves a code with a five-minute life and no key behind it.
	if spy.minted() != 0 {
		t.Errorf("the sign-in itself minted %d key(s), want none: the key is minted at the exchange", spy.minted())
	}
}

// TestCodeExchangeRefusesAHostileTarget is the other half of the redirect
// rule, and the one that matters: the exchange takes no target at all. A
// request that names one — a caller trying to move its own code to an address
// of its choosing — is refused for the field these routes never read, and the
// only listener a code was ever bound to is the loopback one the sign-in
// validated.
func TestCodeExchangeRefusesAHostileTarget(t *testing.T) {
	f, _ := subscribed(t)
	verifier := freshVerifier(t)
	code, _, _ := redirected(t, f.signedIn(t, f.acceptedToken(t), verifier))
	rec := f.postJSON(RouteToken, map[string]string{
		"code":          code,
		"code_verifier": verifier,
		"redirect_uri":  hostileRedirect,
	})
	if rec.Code == http.StatusOK {
		t.Fatalf("the exchange read a redirect target out of the request: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "127.0.0.1") || strings.Contains(rec.Body.String(), hostileRedirect) {
		t.Errorf("the refusal names a target: %s", rec.Body.String())
	}
	// The code is still the CLI's: a request that was refused for its own
	// unknown field spent nothing.
	if rec := f.exchanged(code, verifier); rec.Code != http.StatusOK {
		t.Errorf("the exchange answered %d after a request naming an unknown field was refused, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// TestCodeExchangeMintsThePurchasersLicence: the exchange is where the key is
// minted, and what it mints is the subscription the account's own address
// bought — the workspace the checkout bound (TN2), the seats and the term it
// bought, and the rest of the term the customer is in rather than a fresh
// period nobody paid for.
func TestCodeExchangeMintsThePurchasersLicence(t *testing.T) {
	f, spy := subscribed(t)
	verifier := freshVerifier(t)
	code, _, _ := redirected(t, f.signedIn(t, f.acceptedToken(t), verifier))
	answer := decodeBundle(t, f.exchanged(code, verifier))
	if spy.minted() != 1 {
		t.Fatalf("the exchange minted %d key(s), want one", spy.minted())
	}
	asked := spy.asked[0]
	if asked.WorkspaceID != workspaceID {
		t.Errorf("the key binds to the workspace %q, want %q", asked.WorkspaceID, workspaceID)
	}
	if asked.Organisation != signInOrg || asked.Seats != 5 {
		t.Errorf("the key is minted for %q with %d seat(s), want the subscription's own (%q, 5)", asked.Organisation, asked.Seats, signInOrg)
	}
	if asked.Policy != PolicyTeamAnnual {
		t.Errorf("the key is minted under %q, want %q: the row the subscription's term is sold under", asked.Policy, PolicyTeamAnnual)
	}
	if asked.TermDays != 365 {
		t.Errorf("the key covers %d day(s), want the 365 the subscription has: a sign-in covers the rest of the paid term and never a fresh one", asked.TermDays)
	}
	// The key is a real one, read back through the released verifier, and it
	// carries the workspace the checkout bound.
	claims := v1Verified(t, f, answer.Key)
	if claims.WorkspaceID != workspaceID {
		t.Errorf("the key's own claim names the workspace %q, want %q", claims.WorkspaceID, workspaceID)
	}
	// The renewal secret is the one the key carries the hash of (D2135): the
	// record is what a renewal reads, and a secret beside a key is live only if
	// the two agree.
	state, held, err := f.store.KeyState(context.Background(), claims.KeyID)
	if err != nil || !held {
		t.Fatalf("the key's state is not in the store (%v)", err)
	}
	if state.RenewalSecretHash != secretHash(answer.RenewalSecret) {
		t.Error("the renewal secret handed to the account is not the one the key's state holds the hash of")
	}
	records := f.records()
	last := records[len(records)-1]
	if !strings.HasPrefix(last.Source, "signin:") || last.Email != signInAddress {
		t.Errorf("the record written is %q for %q, want a sign-in record for %q", last.Source, last.Email, signInAddress)
	}
}

// TestCodeIsSingleUse: an authorization code is good once. The second exchange
// of the same code — the shape of a code read off a log or out of a browser's
// history — is refused, and the refusal is the sign-in's own uniform sentence,
// which says nothing about whether the code was ever real.
func TestCodeIsSingleUse(t *testing.T) {
	f, spy := subscribed(t)
	verifier := freshVerifier(t)
	code, _, _ := redirected(t, f.signedIn(t, f.acceptedToken(t), verifier))
	first := decodeBundle(t, f.exchanged(code, verifier))
	second := f.exchanged(code, verifier)
	if second.Code != http.StatusForbidden {
		t.Fatalf("the second exchange of one code answered %d, want %d: %s", second.Code, http.StatusForbidden, second.Body.String())
	}
	if body := strings.TrimSpace(second.Body.String()); body != refuseSignIn {
		t.Errorf("the second exchange was refused with %q, want the uniform sentence %q", body, refuseSignIn)
	}
	if spy.minted() != 1 {
		t.Errorf("the code was exchanged twice and minted %d key(s), want one", spy.minted())
	}
	// The first exchange is the one that worked: a second exchange answers
	// nothing about the key it handed over.
	if strings.Contains(second.Body.String(), first.Key) {
		t.Error("the refusal of a spent code repeats the key the first exchange handed over")
	}
}

// TestAuthorizeCodeLivesFiveMinutes: the code's window is the other bound on it
// and it is checked in the same locked read as the spent flag, so a code past it
// mints nothing — and the refusal is the sign-in's own uniform sentence, which
// says nothing about whether the code was ever real.
func TestAuthorizeCodeLivesFiveMinutes(t *testing.T) {
	f, spy := subscribed(t)
	verifier := freshVerifier(t)
	expired, _, _ := redirected(t, f.signedIn(t, f.acceptedToken(t), verifier))
	f.advance(authorizeCodeTTL)
	late := f.exchanged(expired, verifier)
	if late.Code != http.StatusForbidden {
		t.Errorf("an exchange after the code's life answered %d, want %d: %s", late.Code, http.StatusForbidden, late.Body.String())
	}
	if body := strings.TrimSpace(late.Body.String()); body != refuseSignIn {
		t.Errorf("an expired code was refused with %q, want the uniform sentence %q", body, refuseSignIn)
	}
	if spy.minted() != 0 {
		t.Errorf("an expired code minted %d key(s), want none", spy.minted())
	}
	// A second inside the window is still the window: what the route refuses is
	// an expired code and not every exchange that arrives late.
	fresh, _, _ := redirected(t, f.signedIn(t, f.acceptedToken(t), verifier))
	f.advance(authorizeCodeTTL - time.Second)
	decodeBundle(t, f.exchanged(fresh, verifier))
}

// TestCodeExchangeHandsTheCodeBackWhenTheMintFails: the code is spent before the
// key is minted, so a mint that failed has to hand it back — otherwise a person
// whose exchange failed on the service's side signs in at the terminal all over
// again because the minter was briefly unhappy. Handing it back is safe because
// the code is only ever spent by a caller that proved the verifier, and the
// retry has to prove it again.
func TestCodeExchangeHandsTheCodeBackWhenTheMintFails(t *testing.T) {
	f, _ := subscribedWithStore(t, func(_ *fixture, store *Memory) Store {
		return &failingIssuances{Memory: store, refuse: 1}
	})
	verifier := freshVerifier(t)
	code, _, _ := redirected(t, f.signedIn(t, f.acceptedToken(t), verifier))
	failed := f.exchanged(code, verifier)
	if failed.Code == http.StatusOK {
		t.Fatalf("an exchange whose mint failed answered %d: %s", failed.Code, failed.Body.String())
	}
	// The same code and the same verifier: the sign-in is not asked for again.
	answer := decodeBundle(t, f.exchanged(code, verifier))
	if claims := v1Verified(t, f, answer.Key); claims.WorkspaceID != workspaceID {
		t.Errorf("the retried exchange minted for the workspace %q, want %q", claims.WorkspaceID, workspaceID)
	}
}

// TestCodeAndDeviceAnswersAreNotCacheable: every answer these routes give that
// carries a credential is `no-store` — the exchange's key and its renewal
// secret, the device start's device code and the short code a person types, and
// both of the poll's. Nothing else would notice the header going: a shared cache
// holding one of them is a key, or a code that buys one, handed to whoever asks
// for that URL next.
func TestCodeAndDeviceAnswersAreNotCacheable(t *testing.T) {
	f, _ := subscribed(t)
	verifier := freshVerifier(t)
	code, _, _ := redirected(t, f.signedIn(t, f.acceptedToken(t), verifier))
	notCacheable := func(what string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s answered %d, want %d: %s", what, rec.Code, http.StatusOK, rec.Body.String())
		}
		if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
			t.Errorf("%s is cacheable (%q), and what it carries is a credential", what, cache)
		}
	}
	exchange := f.exchanged(code, verifier)
	notCacheable("the exchange's answer", exchange)
	if !strings.Contains(exchange.Body.String(), `"renewal_secret"`) {
		t.Fatalf("the exchange's answer carries no renewal secret to keep out of a cache: %s", exchange.Body.String())
	}
	start := f.postJSON(RouteDeviceStart, map[string]string{"installation": "acme-laptop"})
	notCacheable("the device start's answer", start)
	var started deviceStartAnswer
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil {
		t.Fatalf("reading the start's answer %q: %v", start.Body.String(), err)
	}
	if started.DeviceCode == "" || started.UserCode == "" {
		t.Fatalf("the start's answer carries no code to keep out of a cache: %s", start.Body.String())
	}
	f.advance(devicePollInterval)
	pending := f.pollDevice(t, started.DeviceCode)
	notCacheable("a poll waiting for its person", pending)
	f.approveDevice(t, started.UserCode, f.acceptedToken(t))
	f.advance(devicePollInterval)
	answered := f.pollDevice(t, started.DeviceCode)
	notCacheable("the poll's answer", answered)
	if !strings.Contains(answered.Body.String(), `"renewal_secret"`) {
		t.Fatalf("the poll's answer carries no renewal secret to keep out of a cache: %s", answered.Body.String())
	}
}

// TestCodeExchangeRefusesAnAccountHoldingNoLicence: the purchaser binding cuts
// both ways, and this is the other one. A token for an address that never
// bought anything — a stranger's, or an address this service has never seen —
// is a verified sign-in that holds nothing to hand a key for.
func TestCodeExchangeRefusesAnAccountHoldingNoLicence(t *testing.T) {
	f, spy := subscribed(t)
	verifier := freshVerifier(t)
	token := f.tokenWith(t, "email", "stranger@elsewhere.example")
	rec := f.signedIn(t, token, verifier)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a sign-in for an address holding no licence answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "stranger@elsewhere.example") {
		t.Errorf("the refusal repeats the address that holds nothing: %s", rec.Body.String())
	}
	if spy.minted() != 0 {
		t.Errorf("an account holding no licence minted %d key(s), want none", spy.minted())
	}
}

// TestPKCEVerifierIsRequired: the challenge is what binds the code to the
// client that asked for it, so the exchange needs the verifier — and the check
// is made *before* the code is spent, so a caller who has read a code and not
// the verifier cannot burn the holder's flow. `plain` is refused at the door
// for the same reason: a plain challenge is the verifier itself.
func TestPKCEVerifierIsRequired(t *testing.T) {
	f, spy := subscribed(t)
	verifier := freshVerifier(t)
	code, _, _ := redirected(t, f.signedIn(t, f.acceptedToken(t), verifier))
	presented := map[string]string{
		"no verifier at all":           "",
		"another client's verifier":    freshVerifier(t),
		"a verifier that is too short": "short",
	}
	for name, wrong := range presented {
		rec := f.exchanged(code, wrong)
		if rec.Code != http.StatusForbidden {
			t.Errorf("the exchange with %s answered %d, want %d: %s", name, rec.Code, http.StatusForbidden, rec.Body.String())
		}
		if body := strings.TrimSpace(rec.Body.String()); body != refuseSignIn {
			t.Errorf("the exchange with %s was refused with %q, want the uniform sentence", name, body)
		}
	}
	if spy.minted() != 0 {
		t.Errorf("a wrong verifier minted %d key(s), want none", spy.minted())
	}
	// A refusal spends nothing: the holder's own exchange still works, which is
	// what makes a wrong verifier a failed guess rather than a denial of the
	// person's own sign-in.
	decodeBundle(t, f.exchanged(code, verifier))
	// The plain method is refused before a code exists to protect.
	plain := authorizeQuery(verifier)
	plain.Set("code_challenge", verifier)
	plain.Set("code_challenge_method", "plain")
	if rec := f.get(RouteAuthorize, plain); rec.Code != http.StatusForbidden {
		t.Errorf("the authorize form with a plain challenge answered %d, want %d: a plain challenge is the verifier, and a caller that could ask for one could exchange a code it read out of a URL", rec.Code, http.StatusForbidden)
	}
}

// TestDeviceCodeNamesTheInstallationBeforeItApproves: §11.4's second step is
// two requests, and the first is the one that names what is asking. The name is
// a stranger's text and the page shows it as text — escaped, so a name carrying
// markup is a name and not a document — which is the whole of the defence
// against a code typed somewhere other than where it was printed.
func TestDeviceCodeNamesTheInstallationBeforeItApproves(t *testing.T) {
	f, spy := subscribed(t)
	start := f.startDevice(t, "acme-laptop <script>alert(1)</script>")
	confirm := f.post(RouteDeviceApprove, url.Values{
		"user_code": {start.UserCode},
		"id_token":  {f.acceptedToken(t)},
	})
	if confirm.Code != http.StatusOK {
		t.Fatalf("the confirmation page answered %d, want %d: %s", confirm.Code, http.StatusOK, confirm.Body.String())
	}
	body := confirm.Body.String()
	if !strings.Contains(body, "acme-laptop") {
		t.Errorf("the confirmation page does not name the installation that is asking: %s", body)
	}
	if strings.Contains(body, "<script>") {
		t.Errorf("the confirmation page rendered the installation's own text as markup: %s", body)
	}
	if strings.Contains(body, start.DeviceCode) {
		t.Error("the confirmation page carries the device code, which is the CLI's own secret and not the person's to hold")
	}
	if spy.minted() != 0 {
		t.Errorf("naming the installation minted %d key(s), want none: the key is minted at the poll", spy.minted())
	}
	// Nothing is approved until the person confirms, so a terminal that is never
	// answered is never handed a licence.
	if rec := f.pollDevice(t, start.DeviceCode); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), DevicePending) {
		t.Errorf("the poll before the confirmation answered %d: %s, want %s", rec.Code, rec.Body.String(), DevicePending)
	}
}

// TestDeviceCodeAnswersTheKeyOnceApproved is §11.4 end to end: the CLI starts,
// the person signs in and confirms, and the poll that follows is answered with
// the bundle — once. What is minted is the same account's subscription as the
// browser flow's, because it is the same binding: the key goes to the account
// whose verified address bought the licence, whichever door it came through.
func TestDeviceCodeAnswersTheKeyOnceApproved(t *testing.T) {
	f, spy := subscribed(t)
	start := f.startDevice(t, "acme-laptop")
	f.advance(devicePollInterval)
	if rec := f.pollDevice(t, start.DeviceCode); !strings.Contains(rec.Body.String(), DevicePending) {
		t.Fatalf("the first poll answered %s, want %s: nobody has approved the code yet", rec.Body.String(), DevicePending)
	}
	f.approveDevice(t, start.UserCode, f.acceptedToken(t))
	f.advance(devicePollInterval)
	answer := f.pollBundle(t, start.DeviceCode)
	claims := v1Verified(t, f, answer.Key)
	if claims.WorkspaceID != workspaceID {
		t.Errorf("the key the device flow handed over names the workspace %q, want %q", claims.WorkspaceID, workspaceID)
	}
	if asked := spy.asked[0]; asked.TermDays != 365 || asked.Seats != 5 {
		t.Errorf("the device flow minted %d day(s) for %d seat(s), want the subscription's own (365, 5)", asked.TermDays, asked.Seats)
	}
	// The code answers once. A second poll is a code this service is not
	// holding, and it is refused rather than answered twice: the bundle is a
	// bearer credential and a second copy is a second holder.
	f.advance(devicePollInterval)
	again := f.pollDevice(t, start.DeviceCode)
	if again.Code != http.StatusForbidden {
		t.Errorf("the second poll of an answered code answered %d, want %d: %s", again.Code, http.StatusForbidden, again.Body.String())
	}
	if strings.Contains(again.Body.String(), answer.Key) {
		t.Error("the second poll repeated the key")
	}
	// The verification address is built from the intake's own origin and not
	// from anything a caller sent (§7.2), so a caller cannot point a person's
	// browser anywhere it likes.
	if !strings.HasPrefix(start.VerificationURI, intakeOrigin) {
		t.Errorf("the verification address is %q, want one built from the intake's own origin %q", start.VerificationURI, intakeOrigin)
	}
}

// TestDeviceCodeAnswersOneKeyWhenItsMintOutlivesThePoll: the approved code is
// taken out of the table **before** the key is minted, so a second poll that
// arrives while the first one's mint is still running finds nothing. A code
// dropped only after the mint returns would hand a second key to a second poll,
// and a store slower than the five-second interval the start advertises is all
// it takes: one approval, two bearer credentials.
func TestDeviceCodeAnswersOneKeyWhenItsMintOutlivesThePoll(t *testing.T) {
	f, spy := subscribedWithStore(t, func(f *fixture, store *Memory) Store {
		return &slowIssuances{Memory: store, took: devicePollInterval, advance: f.advance}
	})
	start := f.startDevice(t, "acme-laptop")
	f.approveDevice(t, start.UserCode, f.acceptedToken(t))
	f.advance(devicePollInterval)
	first := f.pollBundle(t, start.DeviceCode)
	// The mint took a whole interval, so this poll is inside the code's life and
	// past the rate the start asked for: it reaches the code, and the code is
	// gone.
	again := f.pollDevice(t, start.DeviceCode)
	if again.Code != http.StatusForbidden {
		t.Errorf("a poll arriving behind a slow mint answered %d, want %d: %s", again.Code, http.StatusForbidden, again.Body.String())
	}
	if body := strings.TrimSpace(again.Body.String()); body != refuseDevice {
		t.Errorf("the second poll was refused with %q, want %q", body, refuseDevice)
	}
	if strings.Contains(again.Body.String(), first.Key) {
		t.Error("the second poll repeated the key the first one was answered with")
	}
	if spy.minted() != 1 {
		t.Errorf("one approval minted %d key(s), want one", spy.minted())
	}
}

// TestDeviceCodeHandsItsCodeBackWhenTheMintFails is the other half of taking the
// code before the mint: a poll that failed hands it back, because the person
// approved this installation once and the key that failed is one nobody
// received. Without the hand-back the terminal polls a code this service has
// dropped and is told, for the rest of the ten minutes, that its approval was
// never there.
func TestDeviceCodeHandsItsCodeBackWhenTheMintFails(t *testing.T) {
	f, _ := subscribedWithStore(t, func(_ *fixture, store *Memory) Store {
		return &failingIssuances{Memory: store, refuse: 1}
	})
	start := f.startDevice(t, "acme-laptop")
	f.approveDevice(t, start.UserCode, f.acceptedToken(t))
	f.advance(devicePollInterval)
	failed := f.pollDevice(t, start.DeviceCode)
	if failed.Code == http.StatusOK {
		t.Fatalf("a poll whose mint failed answered %d: %s", failed.Code, failed.Body.String())
	}
	f.advance(devicePollInterval)
	answer := f.pollBundle(t, start.DeviceCode)
	if claims := v1Verified(t, f, answer.Key); claims.WorkspaceID != workspaceID {
		t.Errorf("the retried poll minted for the workspace %q, want %q", claims.WorkspaceID, workspaceID)
	}
}

// TestDeviceCodeExpiresAndIsRateLimited: the short code is eight characters, so
// what bounds a guess is not its entropy but its life and the rate it may be
// asked about — ten minutes and one poll every five seconds, which is what the
// start's own answer tells the CLI.
func TestDeviceCodeExpiresAndIsRateLimited(t *testing.T) {
	f, _ := subscribed(t)
	start := f.startDevice(t, "acme-laptop")
	if start.ExpiresIn != int(deviceCodeTTL/time.Second) || start.Interval != int(devicePollInterval/time.Second) {
		t.Errorf("the start answered expires_in=%d interval=%d, want %d and %d", start.ExpiresIn, start.Interval, int(deviceCodeTTL/time.Second), int(devicePollInterval/time.Second))
	}
	f.advance(devicePollInterval)
	f.pollDevice(t, start.DeviceCode)
	// A poll inside the interval is told to slow down rather than served, and
	// the answer names the interval the CLI is to wait.
	early := f.pollDevice(t, start.DeviceCode)
	if early.Code != http.StatusTooManyRequests {
		t.Fatalf("a poll inside the interval answered %d, want %d: %s", early.Code, http.StatusTooManyRequests, early.Body.String())
	}
	if retry := early.Header().Get("Retry-After"); retry != "5" {
		t.Errorf("the slow-down names %q seconds, want 5", retry)
	}
	// The code's own life is the bound, and it is the start's answer read back.
	f.advance(deviceCodeTTL)
	if rec := f.pollDevice(t, start.DeviceCode); rec.Code != http.StatusForbidden {
		t.Errorf("a poll after the code's life answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	// An expired code is not approved into existence either: the person's
	// confirmation reaches nothing.
	expired := f.startDevice(t, "acme-laptop")
	f.advance(deviceCodeTTL)
	done := f.post(RouteDeviceApprove, url.Values{
		"user_code": {expired.UserCode},
		"id_token":  {f.acceptedToken(t)},
		"confirm":   {"1"},
	})
	if done.Code == http.StatusOK {
		t.Errorf("a confirmation for an expired code answered %d: %s", done.Code, done.Body.String())
	}
}

// TestUnknownAndExpiredDeviceCodesAnswerAlike: the poll is reachable by anyone
// and the codes are short, so what it must never be is a way to ask whether a
// code exists. A code that was never issued and one that has run out are
// answered with the same status and the same sentence.
func TestUnknownAndExpiredDeviceCodesAnswerAlike(t *testing.T) {
	f, _ := subscribed(t)
	start := f.startDevice(t, "acme-laptop")
	f.advance(deviceCodeTTL)
	expired := f.pollDevice(t, start.DeviceCode)
	unknown := f.pollDevice(t, freshSecret(t))
	if unknown.Code != expired.Code || unknown.Body.String() != expired.Body.String() {
		t.Errorf("an unknown code is answered %d %q and an expired one %d %q: the difference is a lookup",
			unknown.Code, unknown.Body.String(), expired.Code, expired.Body.String())
	}
	if expired.Code != http.StatusForbidden {
		t.Errorf("an expired code answered %d, want %d", expired.Code, http.StatusForbidden)
	}
}

// TestTokenFromAnotherProjectIsRefused: the sign-in reads the **licence
// project's** tokens and no other project's (D2144, TN3). A token a customer's
// own workspace project signed — or another installation's — proves only that
// somebody controls that project, so it is refused before any claim in it is
// read, and the refusal repeats neither the token nor the audience that failed
// it.
func TestTokenFromAnotherProjectIsRefused(t *testing.T) {
	f, spy := subscribed(t)
	verifier := freshVerifier(t)
	token := signIDToken(t, signInKey(), licenceKeyID, "RS256", f.licenceClaims(anotherProject))
	rec := f.signedIn(t, token, verifier)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a token minted for another project answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if body := strings.TrimSpace(rec.Body.String()); body != refuseSignIn {
		t.Errorf("the refusal is %q, want the uniform sentence", body)
	}
	if strings.Contains(rec.Body.String(), anotherProject) || strings.Contains(rec.Body.String(), token) {
		t.Errorf("the refusal names the token or the project that failed it: %s", rec.Body.String())
	}
	if spy.minted() != 0 {
		t.Errorf("another project's token minted %d key(s), want none", spy.minted())
	}
	// The same is true of the issuer, which is built from the same project id.
	wrongIssuer := f.tokenWith(t, "iss", "https://securetoken.google.com/"+anotherProject)
	if rec := f.signedIn(t, wrongIssuer, verifier); rec.Code != http.StatusForbidden {
		t.Errorf("a token another project issued answered %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// TestTokenFromAnUnverifiedEmailIsRefused: the purchaser binding is the
// address, so a token asserting an address its holder never proved must not
// reach a key — otherwise anybody could bind themselves to somebody else's
// licence by typing their address into a Firebase account. The verifier refuses
// it, and it is refused before this service reads a claim: the account the
// sign-in answers is the **verified** one or there is no sign-in.
func TestTokenFromAnUnverifiedEmailIsRefused(t *testing.T) {
	f, spy := subscribed(t)
	verifier := freshVerifier(t)
	unverified := f.tokenWith(t, "email_verified", false)
	if rec := f.signedIn(t, unverified, verifier); rec.Code != http.StatusForbidden {
		t.Errorf("a token whose address is unverified answered %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	// A missing claim is not a verified one either: the binding reads a claim
	// that is true and never a claim that is merely absent.
	absent := f.licenceClaims(licenceProject)
	delete(absent, "email_verified")
	if rec := f.signedIn(t, signIDToken(t, signInKey(), licenceKeyID, "RS256", absent), verifier); rec.Code != http.StatusForbidden {
		t.Errorf("a token with no email_verified claim answered %d, want %d", rec.Code, http.StatusForbidden)
	}
	// A token with no address at all: without one there is nothing to bind the
	// key to.
	none := f.licenceClaims(licenceProject)
	delete(none, "email")
	if rec := f.signedIn(t, signIDToken(t, signInKey(), licenceKeyID, "RS256", none), verifier); rec.Code != http.StatusForbidden {
		t.Errorf("a token asserting no address answered %d, want %d", rec.Code, http.StatusForbidden)
	}
	if spy.minted() != 0 {
		t.Errorf("an unverified address minted %d key(s), want none", spy.minted())
	}
}

// TestTokenFromASignerTheProjectDoesNotPublishIsRefused: a token that is
// well-formed and inside its window and signed by a key the licence project
// does not publish — or signed another way — is refused, and the refusal is the
// same uniform sentence as every other.
func TestTokenFromASignerTheProjectDoesNotPublishIsRefused(t *testing.T) {
	f, spy := subscribed(t)
	verifier := freshVerifier(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating another key: %v", err)
	}
	cases := map[string]string{
		"a key id the project does not publish": signIDToken(t, signInKey(), anotherKeyID, "RS256", f.licenceClaims(licenceProject)),
		"another key's signature":               signIDToken(t, other, licenceKeyID, "RS256", f.licenceClaims(licenceProject)),
		"a token signed another way":            signIDToken(t, signInKey(), licenceKeyID, "HS256", f.licenceClaims(licenceProject)),
		"not a token at all":                    "not-a-token",
	}
	for name, token := range cases {
		rec := f.signedIn(t, token, verifier)
		if rec.Code != http.StatusForbidden {
			t.Errorf("the sign-in with %s answered %d, want %d: %s", name, rec.Code, http.StatusForbidden, rec.Body.String())
		}
		if body := strings.TrimSpace(rec.Body.String()); body != refuseSignIn {
			t.Errorf("the sign-in with %s was refused with %q, want the uniform sentence", name, body)
		}
	}
	if spy.minted() != 0 {
		t.Errorf("a token this project did not sign minted %d key(s), want none", spy.minted())
	}
}

// deviceStartAnswer is §11.4's first answer as K3.3's CLI reads it.
type deviceStartAnswer struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// startDevice is §11.4's first call.
func (f *fixture) startDevice(t *testing.T, installation string) deviceStartAnswer {
	t.Helper()
	rec := f.postJSON(RouteDeviceStart, map[string]string{"installation": installation})
	if rec.Code != http.StatusOK {
		t.Fatalf("the device start answered %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var answer deviceStartAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("reading the start's answer %q: %v", rec.Body.String(), err)
	}
	if answer.DeviceCode == "" || answer.UserCode == "" || answer.VerificationURI == "" {
		t.Fatalf("the start's answer %q is missing something the CLI needs", rec.Body.String())
	}
	return answer
}

// pollDevice is §11.4's third call.
func (f *fixture) pollDevice(t *testing.T, device string) *httptest.ResponseRecorder {
	t.Helper()
	return f.postJSON(RouteDevicePoll, map[string]string{"device_code": device})
}

// pollBundle is a poll that is answered with the bundle.
func (f *fixture) pollBundle(t *testing.T, device string) bundle {
	t.Helper()
	rec := f.pollDevice(t, device)
	if !strings.Contains(rec.Body.String(), DeviceOK) {
		t.Fatalf("the poll answered %d %s, want the bundle: the code was approved", rec.Code, rec.Body.String())
	}
	var answer bundle
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("reading the poll's answer %q: %v", rec.Body.String(), err)
	}
	if answer.Key == "" || answer.RenewalSecret == "" {
		t.Fatalf("the poll's answer %q carries no key and no renewal secret", rec.Body.String())
	}
	return answer
}

// approveDevice is the person's two steps: the code typed, then the
// confirmation read and submitted.
func (f *fixture) approveDevice(t *testing.T, userCode, token string) {
	t.Helper()
	confirm := f.post(RouteDeviceApprove, url.Values{"user_code": {userCode}, "id_token": {token}})
	if confirm.Code != http.StatusOK {
		t.Fatalf("the confirmation page answered %d, want %d: %s", confirm.Code, http.StatusOK, confirm.Body.String())
	}
	done := f.post(RouteDeviceApprove, url.Values{
		"user_code": {userCode},
		"id_token":  {token},
		"confirm":   {"1"},
	})
	if done.Code != http.StatusOK {
		t.Fatalf("the confirmation answered %d, want %d: %s", done.Code, http.StatusOK, done.Body.String())
	}
}
