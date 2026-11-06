// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"net/http"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// This file is the two metadata documents a client reads before it has a
// token, and they are the only two paths of this server that answer a
// stranger with something other than a refusal (spec.md §2).
//
// **The resource's document is this package's own** (RFC 9728): it names the
// resource — the coordinator's own URL, which is the audience the access
// tokens carry — and points at the authorization server, which here is the
// same origin, because the coordinator issues its own tokens and never passes
// a Firebase token through as a bearer (spec.md §2, the metadata shape).
//
// **The authorization server's document is the library's own struct**
// (zitadel/oidc, D921): `oidc.DiscoveryConfiguration` is RFC 8414's shape, and
// building the document through it is what keeps this server's answer in the
// vocabulary a standard client parses. What it deliberately does NOT carry is
// the OpenID provider's half — no `jwks_uri`, no `userinfo_endpoint`, no
// `subject_types_supported`, no ID-token signing algorithm — because this
// server issues no ID token of its own and is not an OpenID provider for the
// project (spec.md §3, "What the server is not").

// protectedResource is RFC 9728's document. It is this package's own type and
// not the library's, because RFC 9728 is the resource server's half of the
// flow and the library carries the authorization server's.
type protectedResource struct {
	// Resource is the protected resource's identifier: the coordinator's own
	// URL, and the audience of every access token this server mints.
	Resource string `json:"resource"`
	// AuthorizationServers names where a client registers and gets a token.
	// It is this same origin: one service, one origin, which is what D920's
	// public endpoint is.
	AuthorizationServers []string `json:"authorization_servers"`
	// BearerMethodsSupported is the one method this server reads a token
	// from. A query parameter or a form field would put a bearer token in a
	// URL, and a URL reaches a log line.
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	// ScopesSupported is D927's closed vocabulary, from the one place it is
	// written.
	ScopesSupported []string `json:"scopes_supported"`
}

func (s *Server) serveProtectedResource(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, protectedResource{
		Resource:               s.cfg.Issuer,
		AuthorizationServers:   []string{s.cfg.Issuer},
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        ScopeVocabulary,
	})
}

// serveAuthorizationServer answers RFC 8414's document.
//
// `response_types_supported` and `grant_types_supported` are the whole of what
// this server does and are stated rather than defaulted: the library's struct
// documents its own defaults for the fields a caller leaves empty, and a client
// reading "implicit" or "client_secret_basic" as a default would be reading
// about a flow this server has no code for.
//
// The `*_auth_methods` lists carry `none` beside the two secret-carrying ones
// because D928's registration is open: a native client registers with no secret
// at all, and a client that authenticates nothing at the token endpoint is a
// client whose authorization code and PKCE verifier are the whole proof.
func (s *Server) serveAuthorizationServer(w http.ResponseWriter, _ *http.Request) {
	issuer := s.cfg.Issuer
	writeJSON(w, &oidc.DiscoveryConfiguration{
		Issuer:                                 issuer,
		AuthorizationEndpoint:                  issuer + AuthorizePath,
		TokenEndpoint:                          issuer + TokenPath,
		RevocationEndpoint:                     issuer + RevokePath,
		RegistrationEndpoint:                   issuer + RegisterPath,
		ScopesSupported:                        ScopeVocabulary,
		ResponseTypesSupported:                 []string{"code"},
		ResponseModesSupported:                 []string{"query"},
		GrantTypesSupported:                    []oidc.GrantType{oidc.GrantTypeCode, oidc.GrantTypeRefreshToken, oidc.GrantTypeClientCredentials},
		CodeChallengeMethodsSupported:          []oidc.CodeChallengeMethod{oidc.CodeChallengeMethodS256},
		TokenEndpointAuthMethodsSupported:      []oidc.AuthMethod{oidc.AuthMethodNone, oidc.AuthMethodBasic, oidc.AuthMethodPost},
		RevocationEndpointAuthMethodsSupported: []oidc.AuthMethod{oidc.AuthMethodNone, oidc.AuthMethodBasic, oidc.AuthMethodPost},
	})
}
