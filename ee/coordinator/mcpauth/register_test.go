// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
