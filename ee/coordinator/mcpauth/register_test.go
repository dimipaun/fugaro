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
// "Grants nothing" is checked as the four things a registration could have
// granted: a token record, a family, a limit, and a code. A registered client
// that held any of them would be a client that reached the project's budget
// before a person signed in, which is the whole of what an open registration
// endpoint must not do.
func TestRegisterIsOpenAndGrantsNothing(t *testing.T) {
	ts := newTestServer(t)

	// No credential of any kind: this is the request a standard MCP client
	// makes, and it is the reason the endpoint is open at all.
	id, secret := ts.register(t, "an editor", testClientRedirect)

	client, err := ts.store.GetClient(t.Context(), id)
	if err != nil {
		t.Fatalf("the registration was answered but the record is not in the store: %v", err)
	}
	if client.Name != "an editor" || len(client.RedirectURIs) != 1 || client.RedirectURIs[0] != testClientRedirect {
		t.Errorf("the stored record is %+v; want the client the registration described", client)
	}
	if len(client.Limits.Scopes) != 0 {
		t.Errorf("the registration wrote limits (%+v): a stranger's registration chooses no limit and grants none, and a client that arrived with one would be an unattended caller made out of an open endpoint (D929)", client.Limits)
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

	// And the grant that would turn a registration into spend is refused,
	// because the record it wrote names no limits.
	rec := ts.postForm(t, TokenPath, url.Values{
		"grant_type":    {string(GrantClientCredentials)},
		"client_id":     {id},
		"client_secret": {secret},
	})
	if rec.Code == http.StatusOK {
		t.Errorf("a registered client got a token from the client credentials grant: an unattended token with no limits is the one token §7 refuses to mint (D929)")
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
