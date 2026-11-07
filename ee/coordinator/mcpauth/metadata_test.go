// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// This file is M5's metadata half: the two documents a client reads before it
// has anything, and the property both of them have to have — every URL in them
// is served by the server that wrote them.
//
// A metadata document is the one answer this service gives a stranger with no
// credential, so a document naming an endpoint that is not here is a client
// sent somewhere else with no error to notice. That is what the test below
// checks, endpoint by endpoint, by asking this server for each URL it read.

// TestMetadataPointsAtItsOwnServer: both documents describe this installation
// and nothing else.
func TestMetadataPointsAtItsOwnServer(t *testing.T) {
	ts := newTestServer(t)

	rec := ts.get(t, ProtectedResourcePath)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s answered %d; the resource metadata document is where the 401 sends a client", ProtectedResourcePath, rec.Code)
	}
	var resource protectedResource
	decodeBody(t, rec, &resource)
	if resource.Resource != testIssuer {
		t.Errorf("the resource metadata names %q as the resource; this server's own URL is %q, and the resource is the audience every token it mints carries", resource.Resource, testIssuer)
	}
	if len(resource.AuthorizationServers) != 1 || resource.AuthorizationServers[0] != testIssuer {
		t.Errorf("the resource metadata names %v as its authorization servers; want this same origin (%q), because one service issues its own tokens (D920)", resource.AuthorizationServers, testIssuer)
	}
	if len(resource.BearerMethodsSupported) != 1 || resource.BearerMethodsSupported[0] != "header" {
		t.Errorf("the resource metadata names %v as bearer methods; want the header alone, because a token in a query parameter is a token in a log line", resource.BearerMethodsSupported)
	}

	rec = ts.get(t, AuthorizationServerPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s answered %d; a client that read the resource metadata document has to find the authorization server's", AuthorizationServerPath, rec.Code)
	}
	var as oidc.DiscoveryConfiguration
	decodeBody(t, rec, &as)
	if as.Issuer != testIssuer {
		t.Errorf("the authorization server's document names the issuer %q; want %q, which is what every token's audience and every endpoint below are built from", as.Issuer, testIssuer)
	}

	// Every endpoint the document names is one this server answers, at the
	// path it names, under the issuer it names. The list is read from the
	// document rather than written here, so an endpoint added to the document
	// without a route fails this test rather than passing it.
	for _, endpoint := range []struct {
		name, url, method string
	}{
		{"authorization_endpoint", as.AuthorizationEndpoint, http.MethodGet},
		{"token_endpoint", as.TokenEndpoint, http.MethodPost},
		{"revocation_endpoint", as.RevocationEndpoint, http.MethodPost},
		{"registration_endpoint", as.RegistrationEndpoint, http.MethodPost},
	} {
		if !strings.HasPrefix(endpoint.url, testIssuer+"/") {
			t.Errorf("%s is %q, which is not a path on this issuer (%q): a client follows these URLs, so one naming another origin is a client sent to somebody else's server", endpoint.name, endpoint.url, testIssuer)
			continue
		}
		path := strings.TrimPrefix(endpoint.url, testIssuer)
		if rec := ts.do(t, httptest.NewRequest(endpoint.method, path, nil)); rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s is %q and this server answers %d there: the document names an endpoint nobody serves", endpoint.name, endpoint.url, rec.Code)
		}
	}

	// The document is not an OpenID provider's. This server issues no ID token
	// of its own and holds no user directory (spec.md §3), and a jwks_uri or a
	// userinfo_endpoint in it would be a claim that it does.
	body := strings.TrimSpace(rec.Body.String())
	for _, absent := range []string{"jwks_uri", "userinfo_endpoint", "subject_types_supported", "id_token_signing_alg_values_supported", "end_session_endpoint"} {
		if strings.Contains(body, absent) {
			t.Errorf("the authorization server's document carries %q: this server is not an OpenID provider for the project, and a client reading that field would be reading about a flow it has no code for", absent)
		}
	}
	if len(as.ResponseTypesSupported) != 1 || as.ResponseTypesSupported[0] != "code" {
		t.Errorf("response_types_supported is %v; want the code flow alone, which is the only one this server implements", as.ResponseTypesSupported)
	}
	if len(as.CodeChallengeMethodsSupported) != 1 || as.CodeChallengeMethodsSupported[0] != oidc.CodeChallengeMethodS256 {
		t.Errorf("code_challenge_methods_supported is %v; want S256 alone: a plain challenge is refused at the authorize endpoint, and a client that read it as supported would be refused there", as.CodeChallengeMethodsSupported)
	}
	if len(as.ScopesSupported) != len(ScopeVocabulary) {
		t.Errorf("scopes_supported is %v; want D927's closed vocabulary (%v)", as.ScopesSupported, ScopeVocabulary)
	}
	// The two documents are served from one table, so the facts they share are
	// the same fact: the resource document's authorization server is the issuer
	// every endpoint in the other document is built on, and the scope vocabulary
	// is one list and not two.
	if len(resource.AuthorizationServers) != 1 || resource.AuthorizationServers[0] != as.Issuer {
		t.Errorf("the resource metadata names %v as its authorization servers and the authorization server's document names the issuer %q: both documents are views of one table, so these cannot be two different servers", resource.AuthorizationServers, as.Issuer)
	}
	if strings.Join(resource.ScopesSupported, " ") != strings.Join(as.ScopesSupported, " ") {
		t.Errorf("the two documents name different scopes (%v and %v): D927's vocabulary is written once", resource.ScopesSupported, as.ScopesSupported)
	}
}

// TestMetadataAdvertisesOnlyTheGrantsTheTokenEndpointAnswers is M-c's "only the
// code and refresh grants" held as an agreement between two things rather than
// as two lists that happen to match: what the document advertises is what the
// endpoint answers, and what the endpoint refuses is not advertised.
//
// The client credentials grant is the case that matters (D929 amended): it is
// built as a refusal, so a document that still named it would send a client into
// a flow whose only answer is `unsupported_grant_type`.
func TestMetadataAdvertisesOnlyTheGrantsTheTokenEndpointAnswers(t *testing.T) {
	ts := newTestServer(t)
	clientID, _ := ts.register(t, "an editor", testClientRedirect)

	var as oidc.DiscoveryConfiguration
	decodeBody(t, ts.get(t, AuthorizationServerPath), &as)

	advertised := map[string]bool{}
	for _, g := range as.GrantTypesSupported {
		advertised[string(g)] = true
		// An advertised grant is answered by the token endpoint. The request
		// below is incomplete for both of the grants this server has, so the
		// answer is a refusal — but not the one that means "this endpoint does
		// not know that grant".
		rec := ts.postForm(t, TokenPath, url.Values{"grant_type": {string(g)}, "client_id": {clientID}})
		if got := refusalOf(t, rec).Error; got == string(oidc.UnsupportedGrantType) {
			t.Errorf("grant_types_supported names %q and the token endpoint answers it with %q: the document names a grant nobody serves", g, got)
		}
	}
	for _, g := range []oidc.GrantType{oidc.GrantTypeCode, oidc.GrantTypeRefreshToken} {
		if !advertised[string(g)] {
			t.Errorf("grant_types_supported is %v and does not name %q: a client that reads the document has to find the grants the endpoint answers", as.GrantTypesSupported, g)
		}
	}

	// And the grant this server refuses is not advertised. The two halves of
	// that rule are the whole of the finding.
	if advertised[string(oidc.GrantTypeClientCredentials)] {
		t.Errorf("grant_types_supported is %v and names the client credentials grant: the endpoint refuses it (D929 amended, M-f), so the document would be advertising a flow that answers %q", as.GrantTypesSupported, oidc.UnsupportedGrantType)
	}
	rec := ts.postForm(t, TokenPath, url.Values{"grant_type": {string(oidc.GrantTypeClientCredentials)}, "client_id": {clientID}})
	if got := refusalOf(t, rec).Error; got != string(oidc.UnsupportedGrantType) {
		t.Errorf("the client credentials grant answered %q; want %q: the grant this task builds is the refusal", got, oidc.UnsupportedGrantType)
	}

	// Both documents advertise no secret-carrying method, because neither grant
	// this server answers requires one.
	for _, methods := range [][]oidc.AuthMethod{as.TokenEndpointAuthMethodsSupported, as.RevocationEndpointAuthMethodsSupported} {
		if len(methods) != 1 || methods[0] != oidc.AuthMethodNone {
			t.Errorf("an auth methods list is %v; want %q alone: the code and refresh grants are proved by the code with its PKCE verifier and by the refresh token itself, so a document advertising client_secret_basic would be telling a native client to keep a long-lived value on a device for nothing", methods, oidc.AuthMethodNone)
		}
	}
}
