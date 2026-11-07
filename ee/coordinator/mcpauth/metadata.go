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
// **Both documents are served from one table** (M-c). Every fact either of them
// states — the issuer, the resource, the four endpoints, D927's scope
// vocabulary, the grants, the response types, the challenge methods, the
// authentication methods — is written once, in metadataTable, and each document
// is a view of it. Two documents that each carried their own literals would be
// two places the same fact lives, and the one that drifted would be a document
// telling a client about an endpoint that is not there or a flow this server
// does not answer.
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

// metadataTable is every fact the two metadata documents state, in one place.
//
// It is built from the server's own configuration rather than written as a
// constant, because the issuer is the project's: the same table over another
// installation's issuer is that installation's documents, and every path in it
// is the constant the route is mounted at rather than a second spelling of one:
// the four live in server.go, and this file reads them from there.
type metadataTable struct {
	// Issuer is the authorization server's identifier and the origin every
	// endpoint below is reached on. It is the coordinator's own URL, which is
	// also what the access tokens carry as their audience.
	Issuer string
	// Resource is the protected resource's identifier (RFC 9728): the same URL,
	// because the coordinator issues its own tokens for itself (D920).
	Resource string
	// The four endpoints, each the issuer joined with the path server.go
	// declares. They are here rather than in the document literal for the
	// reason the whole table exists: the RFC 9728 document points at the
	// authorization server, and a client that read one document and then the
	// other has to arrive at the same place.
	AuthorizeEndpoint    string
	TokenEndpoint        string
	RevocationEndpoint   string
	RegistrationEndpoint string
	// Scopes is D927's closed vocabulary, from the one place it is written.
	Scopes []string
	// BearerMethods is the one method this server reads a token from. A query
	// parameter or a form field would put a bearer token in a URL, and a URL
	// reaches a log line.
	BearerMethods []string
	// ResponseTypes and ResponseModes are the whole of what this server
	// answers with, stated rather than defaulted: the library's struct
	// documents its own defaults for the fields a caller leaves empty, and a
	// client reading "implicit" as a default would be reading about a flow this
	// server has no code for.
	ResponseTypes []string
	ResponseModes []string
	// Grants names two grants and not three: the client credentials grant is
	// refused at the token endpoint (D929 amended), so advertising it would be
	// the document telling a client about a flow that answers
	// `unsupported_grant_type`.
	Grants []oidc.GrantType
	// CodeChallengeMethods is S256 alone: a plain challenge is refused at the
	// authorize endpoint, and a client that read it as supported would be
	// refused there.
	CodeChallengeMethods []oidc.CodeChallengeMethod
	// TokenAuthMethods and RevocationAuthMethods carry `none` alone, because it
	// is what is true: both grants this server answers are public client grants
	// whose proof is the code with its PKCE verifier and the refresh token
	// itself, and neither requires a secret. A secret a registration was issued
	// is still verified when a client presents one, but a document advertising
	// `client_secret_basic` would be telling a client to hold a long-lived
	// value on a device for no reason.
	TokenAuthMethods      []oidc.AuthMethod
	RevocationAuthMethods []oidc.AuthMethod
}

// metadata is the table above, built from this server's own configuration.
func (s *Server) metadata() metadataTable {
	issuer := s.cfg.Issuer
	return metadataTable{
		Issuer:                issuer,
		Resource:              issuer,
		AuthorizeEndpoint:     issuer + AuthorizePath,
		TokenEndpoint:         issuer + TokenPath,
		RevocationEndpoint:    issuer + RevokePath,
		RegistrationEndpoint:  issuer + RegisterPath,
		Scopes:                ScopeVocabulary,
		BearerMethods:         []string{"header"},
		ResponseTypes:         []string{"code"},
		ResponseModes:         []string{"query"},
		Grants:                []oidc.GrantType{oidc.GrantTypeCode, oidc.GrantTypeRefreshToken},
		CodeChallengeMethods:  []oidc.CodeChallengeMethod{oidc.CodeChallengeMethodS256},
		TokenAuthMethods:      []oidc.AuthMethod{oidc.AuthMethodNone},
		RevocationAuthMethods: []oidc.AuthMethod{oidc.AuthMethodNone},
	}
}

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
	// from.
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	// ScopesSupported is D927's closed vocabulary.
	ScopesSupported []string `json:"scopes_supported"`
}

func (s *Server) serveProtectedResource(w http.ResponseWriter, _ *http.Request) {
	t := s.metadata()
	writeJSON(w, protectedResource{
		Resource:               t.Resource,
		AuthorizationServers:   []string{t.Issuer},
		BearerMethodsSupported: t.BearerMethods,
		ScopesSupported:        t.Scopes,
	})
}

// serveAuthorizationServer answers RFC 8414's document, as the authorization
// server's half of the table above.
func (s *Server) serveAuthorizationServer(w http.ResponseWriter, _ *http.Request) {
	t := s.metadata()
	writeJSON(w, &oidc.DiscoveryConfiguration{
		Issuer:                                 t.Issuer,
		AuthorizationEndpoint:                  t.AuthorizeEndpoint,
		TokenEndpoint:                          t.TokenEndpoint,
		RevocationEndpoint:                     t.RevocationEndpoint,
		RegistrationEndpoint:                   t.RegistrationEndpoint,
		ScopesSupported:                        t.Scopes,
		ResponseTypesSupported:                 t.ResponseTypes,
		ResponseModesSupported:                 t.ResponseModes,
		GrantTypesSupported:                    t.Grants,
		CodeChallengeMethodsSupported:          t.CodeChallengeMethods,
		TokenEndpointAuthMethodsSupported:      t.TokenAuthMethods,
		RevocationEndpointAuthMethodsSupported: t.RevocationAuthMethods,
	})
}
