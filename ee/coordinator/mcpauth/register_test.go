// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// This file is M5's half of the registration endpoint: D928's rule at the
// earlier of its two doors, and the one property that makes an open
// registration endpoint safe — a registration grants nothing.
//
// The tests here drive the real endpoint over the fake bucket, so what they
// read back is what a client would: the RFC 7591 response, and the record the
// store holds afterwards.

// register posts one client registration and returns the client id and the
// secret the response carried.
//
// It fails the test rather than returning an error: every test that calls it is
// about what happens after a registration that worked, and the ones that are
// about a registration that did not call the endpoint themselves.
func (ts *testServer) register(t *testing.T, name string, redirectURIs ...string) (id, secret string) {
	t.Helper()
	rec := ts.postJSON(t, RegisterPath, registrationRequest{RedirectURIs: redirectURIs, ClientName: name})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST %s answered %d (%s); want 201 with the registration", RegisterPath, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var out registrationResponse
	decodeBody(t, rec, &out)
	if out.ClientID == "" || out.ClientSecret == "" {
		t.Fatalf("the registration answered %+v; want a client id and a secret", out)
	}
	return out.ClientID, out.ClientSecret
}

// postJSON posts a JSON body to one of the server's paths.
func (ts *testServer) postJSON(t *testing.T, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling the request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	return ts.do(t, req)
}

// decodeBody decodes a response body, failing the test when it is not the JSON
// the endpoint promised.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("the answer is not JSON: %v (body: %s)", err, strings.TrimSpace(rec.Body.String()))
	}
}

// TestRegisterIsOpenAndGrantsNothing is D928's first rule: the endpoint takes a
// registration from anyone, and what it writes reaches nothing.
//
// "Grants nothing" is checked as the three things a registration could have
// granted: a token record, a family and a code. A registered client that held
// any of them would be a client that reached the project's budget before a
// person signed in, which is the whole of what an open registration endpoint
// must not do.
func TestRegisterIsOpenAndGrantsNothing(t *testing.T) {
	ts := newTestServer(t)

	// No credential of any kind: this is the request a standard MCP client
	// makes, and it is the reason the endpoint is open at all.
	rec := ts.postJSON(t, RegisterPath, registrationRequest{RedirectURIs: []string{testClientRedirect}, ClientName: "an editor"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST %s answered %d (%s); want 201 with the registration", RegisterPath, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var out registrationResponse
	decodeBody(t, rec, &out)
	if out.ClientID == "" || out.ClientSecret == "" {
		t.Fatalf("the registration answered %+v; want a client id and a secret", out)
	}
	id, secret := out.ClientID, out.ClientSecret

	client, err := ts.store.GetClient(t.Context(), id)
	if err != nil {
		t.Fatalf("the registration was answered but the record is not in the store: %v", err)
	}
	if client.Name != "an editor" || len(client.RedirectURIs) != 1 || client.RedirectURIs[0] != testClientRedirect {
		t.Errorf("the stored record is %+v; want the client the registration described", client)
	}

	// The response says what is true of the client it just wrote: the two
	// grants this server answers, and no authentication method at the token
	// endpoint, because neither grant requires the secret. Advertising
	// `client_secret_basic` would be telling a native client to keep a
	// long-lived value on a device for nothing.
	if out.TokenEndpointAuthMethod != string(oidc.AuthMethodNone) {
		t.Errorf("the registration advertises token_endpoint_auth_method %q; want %q: both grants this server answers are proved by the code and its PKCE verifier or by the refresh token itself, so no secret is required and the document has to say so", out.TokenEndpointAuthMethod, oidc.AuthMethodNone)
	}
	if len(out.GrantTypes) != 2 || out.GrantTypes[0] != string(oidc.GrantTypeCode) || out.GrantTypes[1] != string(oidc.GrantTypeRefreshToken) {
		t.Errorf("the registration advertises grant_types %v; want the code and refresh grants alone: the client credentials grant is refused at the token endpoint (D929 amended), so advertising it would be telling a client about a flow that answers unsupported_grant_type", out.GrantTypes)
	}

	// The secret is returned once and only its hash is at rest (spec.md §3).
	if client.SecretHash != TokenHash(secret) {
		t.Errorf("the record holds SecretHash %q; want the hash of the secret the response carried", client.SecretHash)
	}
	for key, data := range ts.store.objects(t) {
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("%s holds the client secret: the response is the one place it is ever shown", key)
		}
		if !strings.HasPrefix(key, clientPrefix) {
			t.Errorf("the registration wrote %s, which is not a client record: a registration grants nothing, so it writes one client and no token, family, request or code", key)
		}
	}

	// And there is no grant that would turn a registration into spend: the one
	// that had no person behind it is refused outright (D929 amended, M-f).
	rec = ts.postForm(t, TokenPath, url.Values{
		"grant_type":    {string(GrantClientCredentials)},
		"client_id":     {id},
		"client_secret": {secret},
	})
	if rec.Code == http.StatusOK {
		t.Errorf("a registered client got a token from the client credentials grant: an unattended caller cannot be made out of an open registration endpoint (D929 amended)")
	}
}

// TestRegistrationRefusesANonLoopbackURI is the owner's tightening of D928: a
// URI that is not loopback and not on the project's allowlist is refused at
// registration, which is the earlier door, rather than at a person's sign-in.
//
// The refusal has to leave nothing behind. A client record written beside a
// refused URI would be a registration that half happened, and the person would
// meet the failure later with no way to see where it came from.
func TestRegistrationRefusesANonLoopbackURI(t *testing.T) {
	ts := newTestServer(t)

	for _, uri := range []string{
		"https://client.example.org/callback",
		"https://evil.example.net/callback",
		// The traps a prefix, suffix or wildcard rule would fall into: each of
		// these is a host that is not the allowlisted one, written to look like
		// it from one side or the other.
		"https://client.example.net.evil.example.org/callback",
		"https://evil.example.org/client.example.net/callback",
		"http://127.0.0.1.example.org/callback",
		"http://localhost.example.org/callback",
		// And the same trap on the other side of the host: these *end* in a
		// loopback name, so a rule that matched a suffix would take them. The
		// host is compared as a whole, and both directions of the mistake are
		// pinned here rather than only the one a reader thinks of first.
		"http://evil-127.0.0.1/callback",
		"http://evil-localhost/callback",
		"http://evil-127.0.0.1:8976/callback",
		// A URI that would send the code to the browser rather than to a
		// client, and one this server cannot reason about at all.
		"https://client.example.net/callback#fragment",
		"com.example.client:/callback",
	} {
		rec := ts.postJSON(t, RegisterPath, registrationRequest{RedirectURIs: []string{uri}})
		if rec.Code == http.StatusCreated {
			t.Errorf("POST %s registered %q: a non-loopback URI that is not on the project's own allowlist is refused here, and a client that could never receive a code is refused at this door rather than at a person's sign-in (D928)", RegisterPath, uri)
		}
	}
	if got := ts.store.objects(t); len(got) != 0 {
		t.Errorf("a refused registration left %d objects behind: the refusals have to happen before anything is written", len(got))
	}
}

// TestRegistrationRefusesAURIWithUserinfo: a redirect URI that carries
// credentials in its authority is refused, and it is refused for that reason
// rather than for one of its neighbours.
//
// Each URI below is a loopback address written with a userinfo part in front of
// it, which is the one shape where the other rules would admit it: the host is
// 127.0.0.1, localhost or ::1, so isLoopbackRedirect would take it, and the
// userinfo is the only thing wrong with it. That matters because the userinfo is
// what a reader of a URI does not see: `http://127.0.0.1@evil.example.org/` is
// somebody else's host, and `http://person-1@127.0.0.1/` is this one written to
// look like it carries a credential. The control at the end is the same three
// addresses without it, which do register.
func TestRegistrationRefusesAURIWithUserinfo(t *testing.T) {
	ts := newTestServer(t)

	for _, uri := range []string{
		"http://person-1@127.0.0.1:8976/callback",
		"http://person-1@localhost:8976/callback",
		"http://person-1@[::1]:8976/callback",
	} {
		rec := ts.postJSON(t, RegisterPath, registrationRequest{RedirectURIs: []string{uri}})
		if rec.Code == http.StatusCreated {
			t.Errorf("POST %s registered %q: a URI carrying credentials in its authority is refused, and this one is loopback otherwise, so the authority is the only thing this refusal can be about", RegisterPath, uri)
		}
	}
	for _, uri := range []string{
		"http://127.0.0.1:8976/callback",
		"http://localhost:8976/callback",
		"http://[::1]:8976/callback",
	} {
		if rec := ts.postJSON(t, RegisterPath, registrationRequest{RedirectURIs: []string{uri}}); rec.Code != http.StatusCreated {
			t.Errorf("POST %s refused %q (%d): the same address without the userinfo is a loopback URI D928 always allows, so the three refusals above are about the authority and not about the address", RegisterPath, uri, rec.Code)
		}
	}
	if got := ts.store.objects(t); len(got) != 3 {
		t.Errorf("the store holds %d objects; want the three control registrations and nothing from the refused three", len(got))
	}
}

// TestRegistrationRefusesALoopbackURIWithAFragment is RFC 6749 §3.1.2's rule at
// the one door where a fragment could still be admitted: every URI below is a
// loopback address, which isLoopbackRedirect takes on sight, so the fragment is
// the only thing wrong with it.
//
// The fragment row the non-loopback test carries cannot hold this rule. That
// URI's host is off the allowlist, so the exact-match refusal below answers the
// same way whether the fragment arm is there or not, and deleting the arm
// changes nothing a test can see. Here the address is one D928 always allows,
// so the fragment arm is the only rule that can refuse it — and the controls at
// the end are the same three addresses without a fragment, which do register.
func TestRegistrationRefusesALoopbackURIWithAFragment(t *testing.T) {
	ts := newTestServer(t)

	for _, uri := range []string{
		"http://127.0.0.1:8976/callback#fragment",
		"http://localhost:8976/callback#fragment",
		"http://[::1]:8976/callback#fragment",
	} {
		rec := ts.postJSON(t, RegisterPath, registrationRequest{RedirectURIs: []string{uri}})
		if rec.Code == http.StatusCreated {
			t.Errorf("POST %s registered %q: a redirection URI must not carry a fragment (RFC 6749 §3.1.2), and this one is a loopback address otherwise, so the fragment is the only thing this refusal can be about", RegisterPath, uri)
		}
	}
	for _, uri := range []string{
		"http://127.0.0.1:8976/callback",
		"http://localhost:8976/callback",
		"http://[::1]:8976/callback",
	} {
		if rec := ts.postJSON(t, RegisterPath, registrationRequest{RedirectURIs: []string{uri}}); rec.Code != http.StatusCreated {
			t.Errorf("POST %s refused %q (%d): the same address without the fragment is a loopback URI D928 always allows, so the three refusals above are about the fragment and not about the address", RegisterPath, uri, rec.Code)
		}
	}
	if got := ts.store.objects(t); len(got) != 3 {
		t.Errorf("the store holds %d objects; want the three control registrations and nothing from the refused three", len(got))
	}
}

// TestRegistrationBoundsWhatItAccepts is the endpoint's three bounds that are
// not about the shape of a URI: a registration that names no redirect URI, one
// that names more than the endpoint keeps, and a name that is not printable.
//
// Each is a bound on what an unauthenticated caller may make this endpoint
// write, which is the residual risk §8 accepts: a client that names no redirect
// URI could never receive a code, the count is what keeps one request from
// writing an unbounded record, and the name is rendered into the consent page
// of §3, where a control character is the page lying to the person reading it —
// html/template escapes markup and escapes nothing else.
func TestRegistrationBoundsWhatItAccepts(t *testing.T) {
	ts := newTestServer(t)

	tooMany := make([]string, 0, maxRedirectURIs+1)
	for i := 0; i <= maxRedirectURIs; i++ {
		tooMany = append(tooMany, testClientRedirect)
	}
	for _, tc := range []struct {
		what string
		req  registrationRequest
	}{
		{"a registration that names no redirect URI", registrationRequest{RedirectURIs: nil}},
		{"a registration that names more than the endpoint keeps", registrationRequest{RedirectURIs: tooMany}},
		{"a name carrying a control character", registrationRequest{RedirectURIs: []string{testClientRedirect}, ClientName: "an editor\x07"}},
		{"a name carrying a byte no page renders as one", registrationRequest{RedirectURIs: []string{testClientRedirect}, ClientName: "an ‮editor"}},
		{"a name longer than the endpoint keeps", registrationRequest{RedirectURIs: []string{testClientRedirect}, ClientName: strings.Repeat("a", maxClientNameBytes+1)}},
	} {
		rec := ts.postJSON(t, RegisterPath, tc.req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s accepted %s (%d: %s); want a refusal naming the rule", RegisterPath, tc.what, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	if got := ts.store.objects(t); len(got) != 0 {
		t.Errorf("the store holds %d objects after five refused registrations; want none: a registration that is refused writes nothing", len(got))
	}

	// The boundary itself is admitted, so the refusals above are about the
	// bounds and not about registrations with a name.
	if rec := ts.postJSON(t, RegisterPath, registrationRequest{
		RedirectURIs: []string{testClientRedirect},
		ClientName:   strings.Repeat("a", maxClientNameBytes),
	}); rec.Code != http.StatusCreated {
		t.Errorf("POST %s refused a name of exactly %d bytes (%d: %s); want the registration: the bound is inclusive", RegisterPath, maxClientNameBytes, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if got := ts.store.objects(t); len(got) != 1 {
		t.Errorf("the store holds %d objects; want the one registration that was accepted", len(got))
	}
}

// TestRedirectURIMustBeLoopbackOrAllowlisted is the rule the two doors share,
// stated as the list of what it admits: the loopback forms a native client
// receives a code at, and the one hosted callback the project's own
// configuration names.
func TestRedirectURIMustBeLoopbackOrAllowlisted(t *testing.T) {
	ts := newTestServer(t)

	for _, uri := range []string{
		// Loopback is always allowed, at whatever port the client's listener
		// happened to take, and under both spellings of the address (D928).
		"http://127.0.0.1:8976/callback",
		"http://127.0.0.1:0/callback",
		"http://localhost:8976/callback",
		"http://LOCALHOST:8976/callback",
		"http://[::1]:8976/callback",
		// The project's own allowlist, matched exactly.
		testAllowlistedRedirect,
	} {
		if rec := ts.postJSON(t, RegisterPath, registrationRequest{RedirectURIs: []string{uri}}); rec.Code != http.StatusCreated {
			t.Errorf("POST %s refused %q (%d): loopback is always allowed for a native client and every other URI has to match the project's own allowlist exactly (D928)", RegisterPath, uri, rec.Code)
		}
	}
	for _, uri := range []string{
		// The allowlisted URI with anything at all changed: a path, a case, a
		// query, a port.
		"https://client.example.net/callback/",
		"https://client.example.net/Callback",
		"https://client.example.net:8443/callback",
		"https://client.example.net/callback?next=https://evil.example.org",
		// An https loopback address is not the http one D928 names, and a
		// scheme this server does not redirect to is refused whatever its host.
		"https://127.0.0.1:8976/callback",
		"ftp://127.0.0.1:8976/callback",
		// Not absolute, and carrying credentials in its authority.
		"/callback",
		"http://127.0.0.1@evil.example.org/callback",
	} {
		if rec := ts.postJSON(t, RegisterPath, registrationRequest{RedirectURIs: []string{uri}}); rec.Code == http.StatusCreated {
			t.Errorf("POST %s registered %q: the match is exact and the loopback forms are the two D928 names, so a near miss is a URI the project never wrote down", RegisterPath, uri)
		}
	}
}

// postRegistration posts one registration from one source, which is the header
// the platform's own front end sets: registerSource reads the rightmost entry
// of X-Forwarded-For, so a test has to be able to put one there. An empty
// source leaves the header off, which is the request a peer that is not behind
// a front end makes — an httptest request's own RemoteAddr.
func (ts *testServer) postRegistration(t *testing.T, source string) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(registrationRequest{RedirectURIs: []string{testClientRedirect}, ClientName: "a client"})
	if err != nil {
		t.Fatalf("marshalling the request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, RegisterPath, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if source != "" {
		req.Header.Set("X-Forwarded-For", source)
	}
	return ts.do(t, req)
}

// TestRegistrationIsRateLimitedPerSource is M12's first bound (spec.md §8,
// reconciliation.md §4a): the endpoint is open to everyone, and a source may
// make a bounded number of registrations in a window.
//
// Three properties are the bound and each is asserted on its own: it counts a
// *source*, so one source's window is not another's and not everybody's; the
// source is the one the platform's front end appended rather than one the
// caller wrote, so varying a spoofed header buys no fresh window; and it is a
// *window*, so the refusal is temporary and the same source is admitted again
// after it. What the refusal must not do is write anything or say anything
// about the caller, which the last two assertions hold.
func TestRegistrationIsRateLimitedPerSource(t *testing.T) {
	const (
		oneSource = "198.51.100.7"
		spoofed   = "203.0.113.9"
		otherOne  = "192.0.2.44"
	)
	now := testNow
	clock := func() time.Time { return now }
	ts := newTestServerOver(t, testConfig(), clock, nil)

	for i := range registerPerWindow {
		if rec := ts.postRegistration(t, oneSource); rec.Code != http.StatusCreated {
			t.Fatalf("registration %d of %d from one source answered %d (%s); want 201: the bound is a window's worth of registrations and not one per source", i+1, registerPerWindow, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	held := len(ts.store.objects(t))
	rec := ts.postRegistration(t, oneSource)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("registration %d from one source answered %d (%s); want 429: an endpoint open to everyone with no bound on one source is a store a stranger fills", registerPerWindow+1, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("the refusal carries no Retry-After: the bound is a window, and a client told only 'no' has nothing to do about it")
	}
	if body := rec.Body.String(); !strings.Contains(body, "temporarily_unavailable") {
		t.Errorf("the refusal answered %s; want RFC 6749 §5.2's code for a bound that is temporary", strings.TrimSpace(body))
	}
	// Nothing about the caller is in the answer, the source least of all.
	for _, secret := range []string{oneSource, spoofed, otherOne} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("the refusal quotes %s: a refusal is not a place an address is written down, and one caller's answer is not another's to read", secret)
		}
	}
	// A refused registration writes nothing: the bound is about what reaches
	// the store, and a refusal that had already written a record would be a
	// bound that bounded nothing.
	if after := len(ts.store.objects(t)); after != held {
		t.Errorf("the refused registration left %d records behind", after-held)
	}

	// The rightmost entry is the source, so a caller that puts a fresh name of
	// its own at the front of the chain is still the same source — which is
	// the whole reason the leftmost entry is not the one counted, since that
	// is the one a caller writes.
	if rec := ts.postRegistration(t, spoofed+", "+oneSource); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a registration whose chain ends in a source past its window answered %d; want 429: the entry that counts is the one the platform's front end appended, not the one the caller sent", rec.Code)
	}
	// And the bound is per source and not global: one source's window must not
	// refuse everybody else's registration, which is a denial of service this
	// door would be performing on itself.
	if rec := ts.postRegistration(t, otherOne); rec.Code != http.StatusCreated {
		t.Errorf("another source answered %d (%s) while a different source was past its window; want 201: a global bound would let one caller stop every other client from registering", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	// The window rolls: the same source is admitted again in the next one.
	now = now.Add(registerWindow)
	if rec := ts.postRegistration(t, oneSource); rec.Code != http.StatusCreated {
		t.Errorf("the same source answered %d (%s) in the window after its own; want 201: the bound is a rate and not a ban", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	// The header is the platform's and not the only source there is: a request
	// with none is counted by its peer address, and those count together.
	for i := range registerPerWindow {
		if rec := ts.postRegistration(t, ""); rec.Code != http.StatusCreated {
			t.Fatalf("registration %d of %d from a peer with no forwarded header answered %d (%s); want 201", i+1, registerPerWindow, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
	if rec := ts.postRegistration(t, ""); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a peer with no forwarded header answered %d past its window; want 429: a request whose source is only its address is not a request with no source", rec.Code)
	}
	// And the two are two sources: the peer's window is not the spoofed one's.
	if rec := ts.postRegistration(t, spoofed); rec.Code != http.StatusCreated {
		t.Errorf("a source the header names answered %d while the peer address was past its window; want 201: the two are counted apart", rec.Code)
	}
}

// TestRegistrationsExpireAndAreSwept is M12's second bound (spec.md §8,
// reconciliation.md §4a), and it is the one that has to reach the three shapes
// the store holds for somebody who has proved nothing: the registration, the
// pending authorize request and the code.
//
// **What is asserted is what stays as much as what goes**, because the two
// directions are not symmetric: a record kept too long is the growth this bound
// exists to end, and a record removed too early is a person's sign-in ended by
// housekeeping. So every plant below has a twin: a registration with no family
// goes and one a family names stays, an expired flow goes and a live one stays,
// a spent code goes with its unspent twin, and a record whose moment has not
// *passed* — equal to now — stays for all three.
//
// **And a spent code has a third reading, which is not about the code at all**:
// its marker is the record a second presentation acts on — the already-redeemed
// answer and the family the first exchange minted, which is what a replay
// revokes (the review of this branch found the marker going at the code's own
// moment, one CodeTTL after the exchange, which ended both). So the code's own
// moment is where its marker *may* go and the family it names is what decides
// whether it does: the plant bound to a live family stays and is still answered
// as redeemed, and the one whose family was revoked goes.
func TestRegistrationsExpireAndAreSwept(t *testing.T) {
	ctx := t.Context()
	now := testNow
	fs := newFakeStoreOver(t, func() time.Time { return now }, nil)
	store := fs.Store

	old := now.Add(-RegistrationTTL - time.Hour)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []Client{
		{ID: "a-registration-nobody-signed-in-through", CreatedAt: old},
		{ID: "a-client-a-family-still-names", CreatedAt: old},
		{ID: "a-registration-made-just-now", CreatedAt: now.Add(-time.Hour)},
		{ID: "a-registration-exactly-at-its-lifetime", CreatedAt: now.Add(-RegistrationTTL)},
		{ID: "an-operators-own-client"},
	} {
		must(store.PutClient(ctx, c))
	}
	must(store.PutFamily(ctx, Family{
		ID:          "the-family-a-client-still-has",
		ClientID:    "a-client-a-family-still-names",
		Identity:    "user:person@example.com",
		RefreshHash: TokenHash("the refresh token that family holds"),
		CreatedAt:   now.Add(-time.Hour),
	}, 0))

	for _, r := range []Request{
		{ID: "a-flow-a-stranger-abandoned", ExpiresAt: now.Add(-time.Minute)},
		{ID: "a-flow-that-was-answered", ExpiresAt: now.Add(-time.Minute)},
		{ID: "a-flow-exactly-at-its-moment", ExpiresAt: now},
		{ID: "a-flow-waiting-for-its-consent", ExpiresAt: now.Add(time.Minute)},
	} {
		must(store.PutRequest(ctx, r))
	}
	if _, err := store.TakeRequest(ctx, "a-flow-that-was-answered"); err != nil {
		t.Fatalf("answering a flow: %v", err)
	}

	expired := TokenHash("a code that expired")
	redeemed := TokenHash("a code that expired and was redeemed")
	orphaned := TokenHash("a code whose exchange's family was revoked")
	live := TokenHash("a code still redeemable")
	for _, code := range []Code{
		{Hash: expired, ClientID: "a-registration-made-just-now", ExpiresAt: now.Add(-time.Minute)},
		{Hash: redeemed, ClientID: "a-registration-made-just-now", ExpiresAt: now.Add(-time.Minute)},
		{Hash: orphaned, ClientID: "a-registration-made-just-now", ExpiresAt: now.Add(-time.Minute)},
		{Hash: live, ClientID: "a-registration-made-just-now", ExpiresAt: now.Add(time.Minute)},
	} {
		must(store.PutCode(ctx, code))
	}
	if _, err := store.ConsumeCode(ctx, redeemed); err != nil {
		t.Fatalf("redeeming a code: %v", err)
	}
	must(store.BindCodeFamily(ctx, redeemed, "the-family-a-client-still-has"))
	if _, err := store.ConsumeCode(ctx, orphaned); err != nil {
		t.Fatalf("redeeming a code: %v", err)
	}
	must(store.PutFamily(ctx, Family{
		ID:          "the-family-that-was-revoked",
		ClientID:    "a-registration-made-just-now",
		Identity:    "user:person@example.com",
		RefreshHash: TokenHash("the refresh token that family held"),
		CreatedAt:   now.Add(-time.Hour),
	}, 0))
	must(store.BindCodeFamily(ctx, orphaned, "the-family-that-was-revoked"))
	must(store.RevokeFamily(ctx, "the-family-that-was-revoked"))
	// A spent record is a marker and not the record any more, so the moment it
	// was readable at is the one field that has to survive both writes: the
	// sweep below can only remove it by that field.
	marker := spentMarkerIn(fs.objects(t)[codePrefix+redeemed])
	if !marker.Consumed || !marker.ExpiresAt.Equal(now.Add(-time.Minute)) {
		t.Errorf("the spent code's marker is %+v; want the moment the code stopped being redeemable kept across the spend and the bind", marker)
	}

	swept, err := store.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if want := (Swept{Registrations: 1, Requests: 2, Codes: 2}); swept != want {
		t.Errorf("the sweep removed %+v; want %+v", swept, want)
	}
	held := fs.objects(t)
	for _, key := range []string{
		clientPrefix + "a-client-a-family-still-names",
		clientPrefix + "a-registration-made-just-now",
		clientPrefix + "a-registration-exactly-at-its-lifetime",
		clientPrefix + "an-operators-own-client",
		familyPrefix + "the-family-a-client-still-has",
		requestPrefix + "a-flow-waiting-for-its-consent",
		requestPrefix + "a-flow-exactly-at-its-moment",
		codePrefix + live,
		codePrefix + redeemed,
	} {
		if _, ok := held[key]; !ok {
			t.Errorf("the sweep removed %s; a registration a family names, an operator's own record, a live flow, a code still redeemable, a spent code whose family can still be revoked and a record whose moment has not passed are all records this bound does not reach", key)
		}
	}
	for _, key := range []string{
		clientPrefix + "a-registration-nobody-signed-in-through",
		requestPrefix + "a-flow-a-stranger-abandoned",
		requestPrefix + "a-flow-that-was-answered",
		codePrefix + expired,
		codePrefix + orphaned,
	} {
		if _, ok := held[key]; ok {
			t.Errorf("the sweep left %s: it is a record nobody has asked for since its own moment passed — and, for the spent code, since the family its marker named was revoked — which is exactly what a sweep is for", key)
		}
	}

	// And what the marker kept is kept for: the second presentation of a
	// redeemed code is still answered as already redeemed, and still names the
	// family the first exchange minted, which is the record the caller revokes
	// (OAuth 2.1 §4.1.3). A sweep that removed it at the code's own moment
	// would answer this as an unknown code and leave that family alive, and the
	// same replay of the orphaned code — whose family is gone — has nothing
	// left to revoke and is answered as the store answers any code it does not
	// hold.
	if replay, err := store.ConsumeCode(ctx, redeemed); !errors.Is(err, ErrCodeUsed) || replay.FamilyID != "the-family-a-client-still-has" {
		t.Errorf("a second presentation of a redeemed code after the sweep answered %+v (%v); want %v naming the family the first exchange minted", replay, err, ErrCodeUsed)
	}
	if _, err := store.ConsumeCode(ctx, orphaned); !errors.Is(err, ErrCodeNotFound) {
		t.Errorf("a second presentation of a code whose family was revoked answered %v after the sweep; want %v: its marker stands for a revocation nothing could act on, so the sweep is what removes it", err, ErrCodeNotFound)
	}

	// A second sweep finds nothing, and that is what makes the interval safe:
	// a sweep running every fifteen minutes over a store that has already been
	// swept must be a list and a read of what is left rather than a second
	// round of removals.
	again, err := store.SweepExpired(ctx)
	if err != nil {
		t.Fatalf("the second SweepExpired: %v", err)
	}
	if again != (Swept{}) {
		t.Errorf("the second sweep removed %+v; want nothing", again)
	}
}

// TestTheDoorSweepsTheRecordsItsOwnTrafficWrites is how the sweep above is
// reached at all: nobody schedules it, so it is the unauthenticated writes —
// the registration and the authorize request, the two places a stranger can
// leave a record — that trigger it, at most once per interval.
//
// The interval is asserted as well as the trigger, and it is the half that
// matters under a loop: a sweep per request would be a full list of the store
// performed for whoever is knocking, which is a bound the endpoint's own traffic
// could turn into the load it was written to prevent.
func TestTheDoorSweepsTheRecordsItsOwnTrafficWrites(t *testing.T) {
	ctx := t.Context()
	now := testNow
	clock := func() time.Time { return now }
	// The store reads the server's clock here, because an installation is one
	// process with one clock and this test is about records one half stamps
	// and the other removes.
	ts := newTestServerClocked(t, testConfig(), clock, clock, nil)

	held := func(key string) bool {
		_, ok := ts.store.objects(t)[key]
		return ok
	}
	plant := func(id string) string {
		t.Helper()
		if err := ts.store.PutRequest(ctx, Request{ID: id, ExpiresAt: now.Add(-time.Minute)}); err != nil {
			t.Fatalf("planting an expired flow: %v", err)
		}
		return requestPrefix + id
	}

	// The first registration is the first trigger, and the record a stranger
	// abandoned before it is what it removes.
	first := plant("a-flow-a-stranger-abandoned-before-anybody-registered")
	if rec := ts.postRegistration(t, ""); rec.Code != http.StatusCreated {
		t.Fatalf("the first registration answered %d (%s)", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if held(first) {
		t.Errorf("%s is still there after a registration: the sweep is triggered by the traffic that writes the records it removes, and nothing else runs it", first)
	}

	// A registration inside the same interval does not run a second sweep.
	second := plant("a-flow-a-stranger-abandoned-inside-the-interval")
	if rec := ts.postRegistration(t, ""); rec.Code != http.StatusCreated {
		t.Fatalf("the second registration answered %d (%s)", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if !held(second) {
		t.Errorf("%s was removed by a second registration in the same interval: a sweep per request is a full list of the store performed for whoever is knocking", second)
	}

	// The next interval runs it again, on the same trigger.
	now = now.Add(sweepInterval)
	if rec := ts.postRegistration(t, ""); rec.Code != http.StatusCreated {
		t.Fatalf("the third registration answered %d (%s)", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if held(second) {
		t.Errorf("%s survived the interval after it was abandoned: the interval is a bound on how often the sweep runs and never a claim that its work is done", second)
	}

	// And the authorization request is the other trigger: it is the other
	// unauthenticated write of the flow, so a loop that registers one client
	// and then asks for a flow forever is a loop the registration endpoint
	// never sees again.
	clientID, _ := ts.register(t, "a client that asks for flows", testClientRedirect)
	now = now.Add(sweepInterval)
	third := plant("a-flow-a-stranger-abandoned-between-authorizations")
	if binding, _ := ts.startFlow(t, authorizeQuery(clientID, testClientRedirect)); binding == "" {
		t.Fatalf("the authorization request rendered no consent page")
	}
	if held(third) {
		t.Errorf("%s survived an authorization request: the flow a stranger abandons is written by that path and has to be swept by it too", third)
	}
}
