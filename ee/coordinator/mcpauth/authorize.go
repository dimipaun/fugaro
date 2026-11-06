// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// This file is the authorization endpoint: GET renders the page a person
// consents on, POST is that person's answer to it.
//
// **The sign-in is verified at the GET, and the ID token is never a grant**
// (D923). The person approves the sign-in on a device paired to the
// installation, that device's session produces a Firebase ID token, and the
// token arrives here in the `Authorization` header and nowhere else — never in
// a query parameter, because a URL reaches a log line, and never at
// `POST /oauth/token`, because a token that is never a grant cannot be replayed
// at one. What the GET verifies is written into the pending request, so the
// identity the code will act for is settled before the page is drawn and the
// consent cannot name it.
//
// **The consent carries the flow's own binding and nothing else.** The pending
// request is the record of everything the consent decides — the client, the
// redirect URI, the challenge, the state, the scopes and the identity — and the
// consent presents the record's own random id. That is D928's CSRF binding
// stated as a fact about the wire rather than as a check: there is no field in
// the consent an attacker can substitute to make the code go somewhere else, or
// act for somebody else, or carry a different state back. A consent that
// carries no binding, or one that was already answered, is refused
// (ErrRequestNotFound, ErrRequestUsed).
//
// **The page names the host the code will be sent to** (D928, the owner's
// answer of 2026-11-04), so a person consents to somewhere rather than to an
// opaque client id.

// consentPage is the page a person reads. It is rendered from a constant
// template, so nothing a caller sent is ever part of the markup: the client's
// name, the redirect host and the scopes are all values substituted into it,
// and html/template escapes each in the context it appears in.
var consentPage = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Authorize this client</title></head>
<body>
<h1>Authorize this client</h1>
<p><strong>{{.ClientName}}</strong> asks to call this project's MCP endpoint as
<strong>{{.Identity}}</strong>.</p>
<p>If you allow it, the authorization code will be sent to
<strong>{{.Host}}</strong>.</p>
<p>It will be able to:</p>
<ul>{{range .Scopes}}<li>{{.}}</li>{{end}}</ul>
<form method="post" action="{{.Action}}">
<input type="hidden" name="request_id" value="{{.RequestID}}">
<button type="submit" name="decision" value="allow">Allow</button>
<button type="submit" name="decision" value="deny">Deny</button>
</form>
</body>
</html>
`))

// refusalPage is what a caller gets when this server will not go on with the
// flow at all. It is HTML rather than an OAuth error body because its reader is
// a person in a browser: every refusal that is the client's to handle is
// redirected back to the client instead (redirectError), and this is the one
// that cannot be — the flow is not known to be the client's, so there is no
// address it would be safe to send the answer to.
const refusalPage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>This request was refused</title></head>
<body><h1>This request was refused</h1><p>%s</p></body>
</html>
`

// serveAuthorizeRequest is the authorization request: it checks what the client
// asked for, verifies the sign-in the person's paired device produced, and
// renders the page they consent on.
//
// The order is the order a caller can act on. The client and its redirect URI
// come first, because until both are the client's own there is no address this
// server may send an error to — and a refusal that redirected to a URI the
// client did not register is the open redirect the whole rule exists to
// prevent. Everything after that point is redirected back with the `state` the
// client sent (RFC 6749 §4.1.2.1).
func (s *Server) serveAuthorizeRequest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID, redirectURI, state := q.Get("client_id"), q.Get("redirect_uri"), q.Get("state")

	client, err := s.store.GetClient(r.Context(), clientID)
	if err != nil {
		refusePage(w, http.StatusBadRequest, "This authorization request names a client this installation does not know. Register a client first.")
		return
	}
	if !slices.Contains(client.RedirectURIs, redirectURI) {
		refusePage(w, http.StatusBadRequest, "This authorization request names a redirect URI this client did not register. The URI is matched exactly against the registered list.")
		return
	}
	// The same rule as registration, applied again (D928): a record written by
	// an operator's pre-registration is a record this door did not write, and
	// the rule is the project's rather than the door's.
	if err := s.checkRedirectURI(redirectURI); err != nil {
		refusePage(w, http.StatusBadRequest, "This authorization request names a redirect URI this installation does not send codes to.")
		return
	}

	if rt := q.Get("response_type"); rt != "code" {
		s.redirectError(w, r, redirectURI, state, oauthError(oidc.ErrUnsupportedGrantType(),
			"this authorization server issues authorization codes: response_type has to be code"))
		return
	}
	challenge := q.Get("code_challenge")
	if challenge == "" {
		s.redirectError(w, r, redirectURI, state, oauthError(oidc.ErrInvalidRequest(),
			"code_challenge is required: every client of this authorization server uses PKCE with S256, and the exchange refuses a code that was issued without one"))
		return
	}
	switch method := oidc.CodeChallengeMethod(q.Get("code_challenge_method")); method {
	case oidc.CodeChallengeMethodS256:
	case "":
		// RFC 7636 §4.3 defaults an absent method to `plain`, and this server
		// does not take that default: a plain challenge is the verifier itself
		// in the authorization request, so it protects nothing. The client is
		// told to say S256 rather than being silently read as one.
		s.redirectError(w, r, redirectURI, state, oauthError(oidc.ErrInvalidRequest(),
			"code_challenge_method has to be S256: this authorization server does not accept a plain code challenge, because a plain challenge is the verifier itself"))
		return
	default:
		s.redirectError(w, r, redirectURI, state, oauthError(oidc.ErrInvalidRequest(),
			"code_challenge_method has to be S256, which is the only method this authorization server accepts"))
		return
	}
	scopes, err := requestedScopes(q.Get("scope"))
	if err != nil {
		s.redirectError(w, r, redirectURI, state, oauthError(oidc.ErrInvalidScope(), err.Error()))
		return
	}

	identity, err := s.verifySignIn(r)
	if err != nil {
		// The sign-in step, and it is not the client's failure: a person who
		// has not approved anything gets a page telling them what to do, and
		// the client is not told anything that would let it retry in a loop.
		refusePage(w, http.StatusUnauthorized, "The sign-in was not verified. Approve this client on a device paired to the installation, and present the Firebase ID token that approval produced as a bearer token on this request.")
		return
	}

	id, err := NewID()
	if err != nil {
		refusePage(w, http.StatusInternalServerError, "This authorization request could not be recorded.")
		return
	}
	pending := Request{
		ID:          id,
		ClientID:    client.ID,
		RedirectURI: redirectURI,
		Challenge:   challenge,
		State:       state,
		Scopes:      scopes,
		Identity:    identity,
		ExpiresAt:   s.now().Add(RequestTTL),
	}
	if err := s.store.PutRequest(r.Context(), pending); err != nil {
		refusePage(w, http.StatusInternalServerError, "This authorization request could not be recorded.")
		return
	}
	writeConsentPage(w, consentView{
		ClientName: consentClientName(client),
		Identity:   identity,
		Host:       redirectHost(redirectURI),
		Scopes:     scopes,
		RequestID:  id,
		Action:     AuthorizePath,
	})
}

// serveAuthorizeConsent is the person's answer to the page above.
//
// It carries the flow's binding and the decision, and nothing else: every value
// the code is written with comes out of the record the binding names. A consent
// whose binding is unknown is refused, and one whose binding was already spent
// is refused with the same page — the flow is answered once.
func (s *Server) serveAuthorizeConsent(w http.ResponseWriter, r *http.Request) {
	form, err := readForm(w, r)
	if err != nil {
		refusePage(w, http.StatusBadRequest, "This consent is not a form this endpoint can read.")
		return
	}
	pending, err := s.store.TakeRequest(r.Context(), form.Get("request_id"))
	switch {
	case errors.Is(err, ErrRequestNotFound), errors.Is(err, ErrRequestUsed):
		refusePage(w, http.StatusBadRequest, "This consent does not carry the binding of a flow this installation is waiting for. A flow is answered once, and only from the page it was rendered on.")
		return
	case err != nil:
		refusePage(w, http.StatusInternalServerError, "This consent could not be read.")
		return
	}
	if s.now().After(pending.ExpiresAt) {
		s.redirectError(w, r, pending.RedirectURI, pending.State, oauthError(oidc.ErrAccessDenied(),
			"the authorization request expired before it was answered"))
		return
	}
	if form.Get("decision") != "allow" {
		s.redirectError(w, r, pending.RedirectURI, pending.State, oauthError(oidc.ErrAccessDenied(),
			"the person did not allow this client to call the MCP endpoint"))
		return
	}

	code, err := NewValue()
	if err != nil {
		refusePage(w, http.StatusInternalServerError, "This consent could not be recorded.")
		return
	}
	if err := s.store.PutCode(r.Context(), Code{
		Hash:        TokenHash(code),
		ClientID:    pending.ClientID,
		RedirectURI: pending.RedirectURI,
		Challenge:   pending.Challenge,
		Identity:    pending.Identity,
		Scopes:      pending.Scopes,
		ExpiresAt:   s.now().Add(CodeTTL),
	}); err != nil {
		refusePage(w, http.StatusInternalServerError, "This consent could not be recorded.")
		return
	}
	s.redirectWith(w, r, pending.RedirectURI, pending.State, url.Values{"code": {code}})
}

// verifySignIn reads the Firebase ID token the person's paired device produced
// and answers the identity it attests, as the entry the project's own lists are
// written in.
//
// The entry is `user:<email>` and it is built from the verifier's own folded
// address, which is the same spelling `fugaro user` writes and the same fold
// its comparison applies: the front door's identity and the CLI's list are one
// vocabulary, so an identity that signed in here is an identity that command
// would have accepted. The uid is deliberately not the entry — D933's lists
// name people by address, and a uid would be an identity no list can hold.
//
// The header is the only place the token is read from, and that is the
// package's own rule rather than a convenience: a token in a query parameter is
// a token in a log line, and the ID token is a bearer for exactly one endpoint.
func (s *Server) verifySignIn(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	const scheme = "Bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", ErrUnverifiedIDToken
	}
	identity, err := s.verifier.VerifyIDToken(r.Context(), strings.TrimSpace(header[len(scheme):]))
	if err != nil {
		return "", err
	}
	return "user:" + identity.Email, nil
}

// requestedScopes reads the scope parameter against D927's closed vocabulary,
// and answers the whole vocabulary when the client asked for none: an
// authorization request with no scope asks for the default, and the default
// here is what the front door mints.
//
// A scope outside the vocabulary is refused and named rather than dropped. A
// server that ignored one would leave its holder believing it holds a
// permission it does not, which is the silent narrowing D927's answer refuses.
func requestedScopes(raw string) ([]string, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return slices.Clone(ScopeVocabulary), nil
	}
	out := make([]string, 0, len(fields))
	for _, s := range fields {
		if !KnownScope(s) {
			return nil, fmt.Errorf("the scope %q is not one this authorization server mints: it mints %s", s, strings.Join(ScopeVocabulary, " and "))
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out, nil
}

// consentView is what the consent template is rendered with.
type consentView struct {
	ClientName string
	Identity   string
	Host       string
	Scopes     []string
	RequestID  string
	Action     string
}

// consentClientName is the name shown for a client that gave none. RFC 7591
// makes client_name optional, and a page that showed an empty strong tag would
// be asking a person to consent to nothing at all.
func consentClientName(c Client) string {
	if c.Name == "" {
		return "A client that did not give a name"
	}
	return c.Name
}

// redirectHost is the host of a redirect URI: what the consent page names, and
// the only part of the address a person can judge. The scheme and the path are
// the client's own choice of URL shape; the host is who receives the code.
func redirectHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// A URI that reached here was checked by checkRedirectURI, so this is
		// unreachable — and a page that named nothing would still be a page a
		// person could consent on, which is the failure mode to avoid.
		return raw
	}
	return u.Host
}

// writeConsentPage renders the page above. A template that cannot render is a
// bug in the constant above rather than a caller's mistake, and the answer is a
// refusal rather than a half-written page: a person must never consent on a
// page that does not say what they are consenting to.
func writeConsentPage(w http.ResponseWriter, view consentView) {
	var body strings.Builder
	if err := consentPage.Execute(&body, view); err != nil {
		refusePage(w, http.StatusInternalServerError, "The consent page could not be rendered.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_, _ = w.Write([]byte(body.String()))
}

// redirectError answers an authorization request that got as far as being the
// client's own: the error goes back to the client's redirect URI, with the
// `state` it sent, which is what RFC 6749 §4.1.2.1 asks for and what lets a
// client tell its own failed request from somebody else's.
func (s *Server) redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state string, e *oidc.Error) {
	values := url.Values{"error": {string(e.ErrorType)}}
	if e.Description != "" {
		values.Set("error_description", e.Description)
	}
	s.redirectWith(w, r, redirectURI, state, values)
}

// redirectWith sends the browser back to the client with the given parameters
// and the `state` unaltered.
//
// The state is set last and only when there is one, and it is written through
// url.Values, so a state that carried characters needing escaping comes back
// decoded to exactly what the client sent. That is the whole of D928's "the
// state returns unaltered": the coordinator keeps the value and never reads it.
func (s *Server) redirectWith(w http.ResponseWriter, r *http.Request, redirectURI, state string, values url.Values) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		refusePage(w, http.StatusBadRequest, "This authorization request names a redirect URI that cannot be answered.")
		return
	}
	q := u.Query()
	for k, vs := range values {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// refusePage is a refusal a person reads, and it is deliberately terse: the
// text says which rule was hit and nothing about the installation, the client
// or the identity, because this endpoint answers strangers.
func refusePage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	// The message is a constant written at the site that refused, never a
	// value a caller sent, so there is nothing here to escape.
	_, _ = fmt.Fprintf(w, refusalPage, message)
}
