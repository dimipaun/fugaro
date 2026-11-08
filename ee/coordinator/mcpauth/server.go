// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"

	"github.com/dimipaun/fugaro/ee/coordinator"
)

// The paths this authorization server answers on (spec.md §2). They are
// constants rather than a configuration because they are the contract a
// standard MCP client is written against: the two metadata documents are where
// RFC 9728 and RFC 8414 say they are, and the four endpoints are the ones the
// owner's spec names. A project that moved one would be a project no client
// could register against.
const (
	// ProtectedResourcePath is the resource server's own metadata (RFC 9728),
	// which is the URL the 401's WWW-Authenticate header names.
	ProtectedResourcePath = "/.well-known/oauth-protected-resource"
	// AuthorizationServerPath is the authorization server's metadata (RFC
	// 8414).
	AuthorizationServerPath = "/.well-known/oauth-authorization-server"
	// RegisterPath is Dynamic Client Registration (RFC 7591).
	RegisterPath = "/oauth/register"
	// AuthorizePath is the authorization endpoint: GET renders the consent a
	// person reads, POST is that person's answer to it.
	AuthorizePath = "/oauth/authorize"
	// TokenPath is the token endpoint: the two grants of D923 and D930 — the
	// authorization code and the rotating refresh token — and no other. D929's
	// client credentials grant is off (the amendment of 2026-11-04, M-f):
	// ParseGrantType still names it so that serveToken can refuse it as the
	// grant this server does not serve rather than as a value nobody knows.
	TokenPath = "/oauth/token"
	// RevokePath is revocation (RFC 7009): one delete of a token family
	// (D930).
	RevokePath = "/oauth/revoke"
	// MCPPath is the MCP endpoint itself: the resource server of M7, which is
	// every request that is not one of the seven above. M5 mounts the refusal
	// a caller with no token gets there, and M7 replaces it with the handler
	// that reads a token, resolves the identity it acts for and answers
	// JSON-RPC (resource.go).
	MCPPath = "/mcp"
)

// The lifetimes this server issues. Both are short on purpose and neither is
// configurable: an authorization code is a value that travels through a
// browser, and the pending request it answers is a flow a person is in the
// middle of.
const (
	// CodeTTL is how long an authorization code lives. RFC 6749 §4.1.2 asks
	// for a short lifetime and gives no number; this is the one a person's
	// browser round trip fits in many times over.
	CodeTTL = 2 * time.Minute
	// RequestTTL is how long a pending authorize request waits for its
	// consent — long enough for a person to sign in on a paired device.
	RequestTTL = 10 * time.Minute
	// maxFormBytes bounds a request body. Every body here is a small form or
	// a small JSON document; one past the bound is refused rather than read.
	maxFormBytes = 64 << 10
)

// Config is what the project's own configuration gives this server. It is the
// D928 answer's other half: the redirect-URI allowlist is a line in the
// project's configuration and never a constant in the coordinator, so a hosted
// client's callback is allowlisted by the person who owns the installation
// rather than by a release of the product.
type Config struct {
	// Issuer is the coordinator's own URL: the authorization server's
	// identifier, the resource every token is minted for, and the origin
	// every endpoint above is reached on. It is scheme and host and nothing
	// else, because it is joined with those paths.
	Issuer string
	// RedirectAllowlist is the project's own list of redirect URIs a client
	// may register beyond loopback (D928). A URI is matched exactly: no
	// wildcard, no prefix and no case folding.
	RedirectAllowlist []string
	// Limits are the project's own limits for the tokens this server issues.
	// Every token is narrowed against them (Limits.Narrow), so a project that
	// names a cap or a window is never widened by anything a client asks for,
	// and a project that names nothing restricts nothing.
	Limits Limits
}

// Server is the authorization server: the two metadata documents, the four
// endpoints, and the 401 that starts the flow. It holds the store every record
// is written through (D935) and the verifier the authorize endpoint consumes a
// Firebase ID token with (D923), and nothing else — no signing key, no model
// credential and no second client to any store.
//
// M7 adds two things it serves with rather than from: the tools a client may
// call (tools), which it answers tools/list and tools/call from, and the
// installation's member lists (roster), which are what decides who a token
// acts for at a door that is on the public internet (D933). Its logger is where
// a refusal at that door is recorded (finding M-e) and is never a store: the
// refusal is a log line and a body, and nothing else.
//
// M10 adds the audit: the structured line every call and every refusal leaves
// (audit), which is a sink of its own beside the refusal line above and not the
// same one — a reader greps one for the door's acts and the other for the door's
// loop — and the record those calls come to, written through the store the
// server already holds (audit_record.go).
type Server struct {
	cfg      Config
	store    Store
	verifier *Verifier
	tools    *Registry
	roster   Roster
	audit    *Auditor
	now      func() time.Time
	logger   *slog.Logger
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithServerClock sets the clock the codes, the pending requests and the token
// expiries are read against (tests).
func WithServerClock(now func() time.Time) ServerOption {
	return func(s *Server) { s.now = now }
}

// WithRoster sets the installation's own launcher and operator lists, which
// are where an identity is resolved (D933) and which this package cannot read
// for itself: they are the installation's published state, written by an
// operator, and identity.go's Roster is the interface a later task implements
// over it.
//
// It is an option rather than an argument of NewServer, and the difference is
// stated rather than left to be read off the signature: NewServer refuses what
// it cannot work *at all* without, and a server with no roster is a server
// that admits nobody — the fail-closed default rather than a mistake, which is
// what lets M5's own tests, the front door's dormancy (D936) and every test
// that is not about identity build a server without one.
func WithRoster(roster Roster) ServerOption {
	return func(s *Server) { s.roster = roster }
}

// WithLogger sets the logger the refusals of the MCP endpoint are written to
// (finding M-e: an unauthenticated refusal is logged and never written to the
// database). The default is slog.Default(), which is where the coordinator's own
// process logs already go, so an installation that configures nothing still
// records a stranger's loop; a test passes a logger over its own buffer.
func WithLogger(logger *slog.Logger) ServerOption {
	return func(s *Server) { s.logger = logger }
}

// WithAuditor sets where this door's audit lines are written (audit.go), which
// is a sink of its own rather than the logger above: M7's refusal line is one
// line on the process log and M10's audit is the structured line the plan's §4
// asks for, and an installation arms the second one at the place it configures
// Cloud Logging.
//
// The default is the process's own log — slog.Default(), the same place the
// refusal line goes by default — because the audit line is the durable copy of
// what this door did (the amendment of 2026-11-04) and an installation that
// configures nothing must not therefore keep nothing. A nil auditor sets
// nothing, the way a nil logger sets nothing: a caller that has no auditor does
// not get one that drops lines.
func WithAuditor(a *Auditor) ServerOption {
	return func(s *Server) {
		if a != nil {
			s.audit = a
		}
	}
}

// NewServer returns the authorization server for one project.
//
// Every argument is refused when it is missing rather than at the first call: a
// server with no store has nowhere to write a client or a code, one with no
// verifier cannot answer the sign-in step, and one whose issuer is not an
// origin builds metadata documents naming URLs that do not exist. Each of those
// is a deployment mistake, and a server built anyway would report it as a
// refusal of somebody's request.
func NewServer(cfg Config, store Store, verifier *Verifier, opts ...ServerOption) (*Server, error) {
	if store == nil {
		return nil, errors.New("mcpauth: an authorization server needs the store its records go through")
	}
	if verifier == nil {
		return nil, errors.New("mcpauth: an authorization server needs a verifier: the authorize endpoint is the one place a Firebase ID token is consumed")
	}
	if err := issuerURL(cfg.Issuer); err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, store: store, verifier: verifier, tools: NewRegistry(), now: time.Now, logger: slog.Default()}
	for _, o := range opts {
		o(s)
	}
	// After the options, so that a server told where its audit goes keeps that
	// and one told nothing is armed with the process's own log (WithAuditor).
	// slog.Default() always has a handler, so the error is unreachable here.
	if s.audit == nil {
		a, err := NewAuditor(slog.Default().Handler())
		if err != nil {
			return nil, err
		}
		s.audit = a
	}
	return s, nil
}

// issuerURL checks that an issuer is an origin: a scheme, a host and nothing
// else. The paths above are appended to it, so a value carrying a path, a query
// or a fragment would produce metadata documents naming endpoints nobody
// serves — and https is required because the tokens this server issues are
// bearers over a public endpoint (D920).
func issuerURL(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil {
		return fmt.Errorf("mcpauth: the issuer is not a URL: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("mcpauth: the issuer %q is not an https origin: the endpoints and the metadata documents are built by appending paths to it, and a token audience has to be a URL a client can check", issuer)
	}
	return nil
}

// Mount adds every route of this server to the service M4 creates. It is the
// whole of what M5 does to that service, and M7 adds the MCP endpoint's own
// handler the same way: the one route below that is not one of the
// authorization server's six.
//
// The routes are the two metadata documents, the four endpoints and the MCP
// endpoint, and they are mounted on the standard library's mux: the patterns
// below are exact paths and one method each, which is the whole of what is
// needed here.
func (s *Server) Mount(svc *coordinator.Service) {
	svc.Handle(ProtectedResourcePath, http.HandlerFunc(s.serveProtectedResource))
	svc.Handle(AuthorizationServerPath, http.HandlerFunc(s.serveAuthorizationServer))
	svc.Handle("POST "+RegisterPath, http.HandlerFunc(s.serveRegister))
	svc.Handle("GET "+AuthorizePath, http.HandlerFunc(s.serveAuthorizeRequest))
	svc.Handle("POST "+AuthorizePath, http.HandlerFunc(s.serveAuthorizeConsent))
	svc.Handle("POST "+TokenPath, http.HandlerFunc(s.serveToken))
	svc.Handle("POST "+RevokePath, http.HandlerFunc(s.serveRevoke))
	svc.Handle(MCPPath, http.HandlerFunc(s.serveMCP))
}

// readForm parses a form body with the server's own bound and returns the
// refusal a caller sees when it is not one. Every endpoint below takes a form
// and none of them takes JSON, so this is one function rather than five.
func readForm(w http.ResponseWriter, r *http.Request) (url.Values, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("the request body is not a form this endpoint can read: %w", err)
	}
	return r.PostForm, nil
}

// writeJSONStatus writes a JSON body with a status. The encoders here never
// fail on the shapes this package writes, and a failure has nowhere to go once
// the status line is out: the body is what a client reads, and half of it is
// not a body.
//
// Nothing this package writes may be cached, and the rule is stated here rather
// than at each caller because it is true of every answer: the bodies carry
// access and refresh tokens, an authorization code, a client's secret and the
// metadata documents' own URLs, and a shared cache holding any of them is a
// credential or an installation a client is sent to by somebody else. The one
// answer that does not come through here (serveRevoke) sets the same two headers
// itself.
func writeJSONStatus(w http.ResponseWriter, status int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "the answer could not be encoded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// writeJSON is writeJSONStatus for a document: 200, and never cached, because
// these documents name this installation's own URLs and a shared cache holding
// one is a client sent to an installation that is not this one.
func writeJSON(w http.ResponseWriter, body any) {
	writeJSONStatus(w, http.StatusOK, body)
}

// oauthError builds the body RFC 6749 §5.2 asks for out of the library's own
// error type and constants (zitadel/oidc, D921), so a client reading `error`
// gets a value out of the specification's vocabulary rather than one this
// package invented.
//
// The kind is taken from one of the library's own constructors rather than
// named: `ErrorType` is an exported field of an unexported type, so the
// constants can be read and copied but not spelled as a parameter's type.
func oauthError(template *oidc.Error, description string) *oidc.Error {
	return &oidc.Error{ErrorType: template.ErrorType, Description: description}
}

// writeOAuthError answers with the OAuth error body RFC 6749 §5.2 asks for.
//
// The three values this door must never echo — a token, a code and a PKCE
// verifier — are in no description it writes: a description is written at the
// site that refused. Two sites do carry text the caller chose, and both are
// deliberate: registration names the redirect URI it refused, which is a URI
// registered to be shown to a person and one a client author could otherwise
// only guess at among several, and a limits refusal names the scope it will not
// mint. Neither is a secret and neither is read back out of a store.
func writeOAuthError(w http.ResponseWriter, status int, e *oidc.Error) {
	writeJSONStatus(w, status, e)
}

// clientRefusal answers a client authentication that failed, with the header
// RFC 6749 §5.2 requires of that answer: a client that authenticated with the
// Authorization header gets a 401 naming the scheme this server accepts there,
// instead of a 401 that leaves it to guess the scheme again.
//
// Basic is the scheme, and the realm is the issuer: they are the two facts the
// rule asks for — the scheme the client used, and the protection space it was
// refused in. A request that carried no header is answered the same way it
// always was, because §5.2 makes the 401 a MAY outside the header case and the
// form's own two fields are already the scheme.
func (s *Server) clientRefusal(w http.ResponseWriter, r *http.Request, description string) {
	if r.Header.Get("Authorization") != "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="`+s.cfg.Issuer+`"`)
	}
	writeOAuthError(w, http.StatusUnauthorized, oauthError(oidc.ErrInvalidClient(), description))
}
