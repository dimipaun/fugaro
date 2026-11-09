// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"html/template"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// This file is the authorization endpoint: GET renders the page a person reads
// and approves from, POST is the approval itself.
//
// **The GET verifies nothing, and the approval carries the proof** (D923,
// amended by M-a on 2026-11-04). The Firebase ID token the person's paired
// device produces arrives in the approval POST's *body* and nowhere else — never
// in a query parameter of either method, because a URL reaches a log line and a
// browser's history, and never at `POST /oauth/token`, because a token that is
// never a grant cannot be replayed at one. The GET is an unauthenticated
// navigation: it checks what the client asked for, records the flow, and renders
// the page. Nothing about a person is settled until the approval arrives.
//
// **The approval is single-use and bound to the flow.** The pending request is
// the record of everything the approval decides — the client, the redirect URI,
// the challenge, the state and the scopes — and the approval presents the
// record's own random id. `Store.TakeRequest` spends it, so one binding answers
// one approval, and an attacker who read the id out of a browser's history
// cannot answer the flow the person started: the answer also has to carry the
// token that person's device produced. Nothing in the approval can substitute a
// value of its own — a redirect URI of the caller's own choosing beside the
// binding still goes to the record's URI.
//
// **Number matching is two acts, and the second one is M5b's.** The flow
// carries its own six-digit number and the page shows it, and the number is
// presented back **on the paired device**: the device's act is a POST of its
// own — the flow's id, the number the person read off the screen, and the
// Firebase ID token that device's own session produced — and it is that act
// which records the approval (approval.go). **The page's own answer carries
// neither the number nor the token**: it is the binding and the person's
// decision, and it mints the code for a flow the device approved. So a page
// that rendered a flow of its own displays that flow's number and still cannot
// approve it, because the number has to arrive from the device beside a token
// this server verifies (M-a's first requirement).
//
// What M5b does not build is H1's device-held signing key: the approval is
// bound to the device's session, which is the residual risk H1 accepts and
// spec.md §8 documents — a device token is a custom token any launcher can
// mint, and the signing key that would bind the act to one device is in no
// task.
//
// **The client is shown as UNVERIFIED** (M-a, the unverified client name).
// `client_name` is the client's own assertion about itself — RFC 7591 takes it
// from the registration and nothing checks it — so the page renders it escaped
// and says plainly that nothing checked it, beside the host the code will
// actually be sent to. A page that showed a self-chosen name as though it were
// an identity would be the page an attacker's client uses to look like a client
// the person already trusts.

const (
	// maxStateBytes bounds the client's own `state`. D928 makes the state a
	// value this server keeps and returns unaltered, so it is bounded rather
	// than truncated: a request that carries more than this is refused, because
	// a state that was cut down would come back altered.
	maxStateBytes = 512
	// maxChallengeBytes is what an S256 challenge is and no more: RFC 7636 §4.2
	// makes the challenge the base64url encoding of a SHA-256 digest, which is
	// 43 characters without padding, so a longer value is one no verifier
	// derives. It is bounded here beside the state for the same reason the
	// state is — this GET is unauthenticated and writes the record, so an
	// unbounded value in that record is one an anonymous caller leaves in the
	// installation's database for the record's whole life.
	maxChallengeBytes = 43
	// approvalDigits is how many decimal digits a flow's number carries. Six is
	// what makes a number a person can read off a screen and type back, and it
	// is short enough that the page can show it as one word.
	approvalDigits = 6
)

// approvalNumberMax is the exclusive bound on a flow's number: the digits above,
// as 10^n.
var approvalNumberMax = new(big.Int).Exp(big.NewInt(10), big.NewInt(approvalDigits), nil)

// consentPage is the page a person reads. It is rendered from a constant
// template, so nothing a caller sent is ever part of the markup: the client's
// name, the redirect host, the scopes and the number are all values substituted
// into it, and html/template escapes each in the context it appears in — which
// is what makes the client's self-asserted name safe to show at all.
var consentPage = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Authorize this client</title></head>
<body>
<h1>Authorize this client</h1>
<p><strong>{{.ClientName}}</strong> asks to call this project's MCP endpoint.</p>
<p><strong>This client is not verified.</strong> The name above is the one it
gave when it registered, and nothing in this installation checked it.</p>
<p>If you allow it, the authorization code will be sent to
<strong>{{.Host}}</strong>.</p>
<p>It will be able to:</p>
<ul>{{range .Scopes}}<li>{{.}}</li>{{end}}</ul>
<p>This flow's number is <strong>{{.Number}}</strong>. Approve this flow on the
device this installation paired with you: that device takes this number, and
the sign-in its own session produces is what proves who is approving. Reading
the number off this screen and entering it there is what shows you approved the
request that is in front of you.</p>
<p>Nothing is approved from this page. Once the device has approved this flow,
answer here: Allow sends the code to the host above, and Deny ends the
flow.</p>
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
// asked for, records the flow, and renders the page the approval answers. It
// verifies nothing about a person, because a person has not presented anything
// yet.
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

	// A token in a URL is refused before anything else is read. It is the one
	// shape this endpoint must never accept, and the refusal is a page rather
	// than a redirect because what the request carried is a bearer credential:
	// echoing it back to an address would be the second place it lands.
	if carried := firstOf(q, "id_token", "access_token"); carried != "" {
		refusePage(w, http.StatusBadRequest, "This authorization request carries a token in its URL. A token in a query parameter is a token in a log line and in a browser's history, so the Firebase ID token is read from the body of the approval POST and from nowhere else.")
		return
	}
	// The state is bounded and refused rather than truncated, and the refusal is
	// a page because the bound exists to keep an unbounded value out of the
	// answer: D928 returns the state unaltered, so a state this server will not
	// hold is a request it will not answer.
	if len(state) > maxStateBytes {
		refusePage(w, http.StatusBadRequest, "This authorization request carries a state longer than this server keeps. The state is returned unaltered (D928), so a state that cannot be kept whole is refused rather than cut down.")
		return
	}

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
		// RFC 6749 §4.1.2.1's own code for this refusal. The library carries a
		// constructor for every error its own flows raise and none for this one,
		// because a flow it runs does not refuse a response type — so the value
		// is written here, which is also where the rule it answers lives.
		s.redirectError(w, r, redirectURI, state, oauthError(&oidc.Error{ErrorType: "unsupported_response_type"},
			"this authorization server issues authorization codes: response_type has to be code"))
		return
	}
	// RFC 8707: a client may name the resource it wants a token for, and a
	// resource this server does not serve is refused rather than ignored. The
	// one resource here is the coordinator's own URL, which is the audience
	// every token this server mints already carries (spec.md §2), so a request
	// naming anything else is asking for a token this server cannot issue.
	for _, target := range q["resource"] {
		if target != s.cfg.Issuer {
			s.redirectError(w, r, redirectURI, state, oauthError(oidc.ErrInvalidTarget(),
				"the resource this authorization request names is not one this authorization server issues tokens for"))
			return
		}
	}
	challenge := q.Get("code_challenge")
	if challenge == "" {
		s.redirectError(w, r, redirectURI, state, oauthError(oidc.ErrInvalidRequest(),
			"code_challenge is required: every client of this authorization server uses PKCE with S256, and the exchange refuses a code that was issued without one"))
		return
	}
	// Refused rather than stored, exactly as the state above is: the challenge
	// goes into the record, and no verifier derives a challenge longer than an
	// S256 one, so a value this size is a request that could not have come from
	// a client of this server.
	if len(challenge) > maxChallengeBytes {
		s.redirectError(w, r, redirectURI, state, oauthError(oidc.ErrInvalidRequest(),
			"code_challenge is longer than an S256 challenge can be: the challenge is the base64url SHA-256 digest of the verifier, which is 43 characters, so this one is refused rather than kept"))
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

	id, err := NewID()
	if err != nil {
		refusePage(w, http.StatusInternalServerError, "This authorization request could not be recorded.")
		return
	}
	number, err := approvalNumber()
	if err != nil {
		refusePage(w, http.StatusInternalServerError, "This authorization request could not be recorded.")
		return
	}
	pending := Request{
		ID:          id,
		Number:      number,
		ClientID:    client.ID,
		RedirectURI: redirectURI,
		Challenge:   challenge,
		State:       state,
		Scopes:      scopes,
		ExpiresAt:   s.now().Add(RequestTTL),
	}
	if err := s.store.PutRequest(r.Context(), pending); err != nil {
		refusePage(w, http.StatusInternalServerError, "This authorization request could not be recorded.")
		return
	}
	writeConsentPage(w, consentView{
		ClientName: consentClientName(client),
		Host:       redirectHost(redirectURI),
		Scopes:     scopes,
		Number:     number,
		RequestID:  id,
		Action:     AuthorizePath,
	})
}

// serveAuthorizeConsent is this endpoint's POST, and M5b makes it two acts
// rather than one: the **paired device's approval** (approval.go), which is
// what carries the flow's number and the ID token, and the **page's answer**,
// which carries the flow's binding and the person's decision and no credential
// at all. They are told apart by their bodies, and a body that is both is
// refused — that shape is M5's page posting its own number back beside a token,
// which is the approval this endpoint no longer takes (M-a's first
// requirement).
//
// Both go through the endpoint rather than through a path of their own because
// the paths of this front door are mounted in server.go's `Mount`
// (approval.go's note): a route added here would be one nothing mounts.
func (s *Server) serveAuthorizeConsent(w http.ResponseWriter, r *http.Request) {
	// The same rule the GET keeps, over the same two names: a token in a URL is
	// a token in a log line, and `access_token` is the name a caller reaches for
	// when what it carries is a bearer rather than an ID token.
	if carried := firstOf(r.URL.Query(), "id_token", "access_token"); carried != "" {
		refusePage(w, http.StatusBadRequest, "This approval carries a token in its URL. A token in a query parameter is a token in a log line, so the approval reads the ID token from the form body and refuses one presented any other way.")
		return
	}
	form, err := readForm(w, r)
	if err != nil {
		refusePage(w, http.StatusBadRequest, "This approval is not a form this endpoint can read.")
		return
	}
	switch device, page := isDeviceAct(form), servesThePage(form); {
	case device && page:
		refusePage(w, http.StatusBadRequest, deviceApprovalRefusal)
	case device:
		s.serveDeviceApproval(w, r, form)
	case page:
		s.servePageAnswer(w, r, form)
	default:
		refusePage(w, http.StatusBadRequest, "This request is neither a device's approval nor a page's answer to a flow. A device approves with the flow's id, its number and the ID token of its own sign-in, and a page answers with the flow's binding and its decision.")
	}
}

// servePageAnswer is the page's own answer to a flow: the binding and the
// person's decision, which is what the page above posts (M5b).
//
// The binding is spent first and everything is judged afterwards, which is the
// fail-closed direction this endpoint takes on purpose: an answer that fails
// any of the checks below has still answered the flow, and the alternative — a
// binding that survives a refused answer — is a page a stranger can keep
// guessing against.
//
// The flow has to carry an approval by the time this runs, and that check is
// the one M5b adds here: the identity the code acts for is the device's
// (`pending.Approval`), and a page that minted without one would be the page
// approving on its own.
func (s *Server) servePageAnswer(w http.ResponseWriter, r *http.Request, form url.Values) {
	pending, err := s.store.TakeRequest(r.Context(), form.Get("request_id"))
	switch {
	case errors.Is(err, ErrRequestNotFound), errors.Is(err, ErrRequestUsed):
		refusePage(w, http.StatusBadRequest, "This approval does not carry the binding of a flow this installation is waiting for. A flow is answered once, and only from the page it was rendered on.")
		return
	case err != nil:
		refusePage(w, http.StatusInternalServerError, "This approval could not be read.")
		return
	}
	if s.now().After(pending.ExpiresAt) {
		s.redirectError(w, r, pending.RedirectURI, pending.State, oauthError(oidc.ErrAccessDenied(),
			"the authorization request expired before it was answered"))
		return
	}
	// The person's answer is read before the approval is, because the page
	// renders one form behind both buttons: a Deny is the same submission as an
	// Allow, and nobody refusing a client has a reason to have approved the
	// flow on a device first. The client is waiting on its own callback, so a
	// deny that ended on a refusal page would be an answer this server never
	// sends (RFC 6749 §4.1.2.1). The binding is what makes the deny an answer
	// to this flow, and it is spent by the time this is read.
	if form.Get("decision") != "allow" {
		s.redirectError(w, r, pending.RedirectURI, pending.State, oauthError(oidc.ErrAccessDenied(),
			"the person did not allow this client to call the MCP endpoint"))
		return
	}
	// The approval, which is the whole of what makes this page's Allow an
	// approval: the identity the code acts for is the one the paired device's
	// sign-in proved, and this page carries no credential of its own (M5b). A
	// flow no device approved mints nothing, and the person is told where to
	// approve it rather than left with a button that does nothing.
	if pending.Approval == nil {
		refusePage(w, http.StatusBadRequest, "This flow has not been approved on the device this installation paired with you. Approve it there first — the number on this page is the one the device takes — and answer here afterwards.")
		return
	}

	code, err := NewValue()
	if err != nil {
		refusePage(w, http.StatusInternalServerError, "This approval could not be recorded.")
		return
	}
	if err := s.store.PutCode(r.Context(), Code{
		Hash:        TokenHash(code),
		ClientID:    pending.ClientID,
		RedirectURI: pending.RedirectURI,
		Challenge:   pending.Challenge,
		Identity:    pending.Approval.Identity,
		Scopes:      pending.Scopes,
		ExpiresAt:   s.now().Add(CodeTTL),
	}); err != nil {
		refusePage(w, http.StatusInternalServerError, "This approval could not be recorded.")
		return
	}
	s.redirectWith(w, r, pending.RedirectURI, pending.State, url.Values{"code": {code}})
}

// verifySignIn verifies the Firebase ID token an approval carried and answers
// the identity it attests, as the entry the project's own lists are written in.
//
// The token is a parameter and not a request, and that is the point: the one
// place this endpoint reads it from is the approval POST's body, and a function
// that took the request would be a function the next caller could hand a header
// or a query value to. A token that reached a URL has already been written to a
// log line; the endpoint refuses one that arrives that way rather than
// accepting it, and this is where "the body and nowhere else" is enforced.
//
// The entry is `user:<email>`, built from the address the verifier folded to
// ASCII lower case, and the fold is this endpoint's own: it is not the rule the
// project's own lists are read by, and nothing here claims the CLI agrees with
// it. `internal/cli/roles.go` places and compares a member exactly
// (`operatorMember` and `launcherMember` are a `slices.Contains` over the text),
// `internal/cli/user.go`'s `userMember` writes the member it was handed without
// folding it, and the one comparison over these members that folds at all is the
// local self-removal guard's `strings.EqualFold` — a Unicode fold, which is what
// `foldEmail` above exists to avoid. That the addresses the CLI derives are
// lower-cased too (`internal/cli/init_members.go`, `internal/cli/pair.go`) makes
// the two agree in practice; it is not a rule either side reads the other by,
// and the spelling M7 matches these lists with is M7's to state. The uid is
// deliberately not the entry — D933's lists name people by address, and a uid
// would be an identity no list can hold.
func (s *Server) verifySignIn(ctx context.Context, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", ErrUnverifiedIDToken
	}
	identity, err := s.verifier.VerifyIDToken(ctx, strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	return "user:" + identity.Email, nil
}

// approvalNumber is the flow's own number: `approvalDigits` decimal digits, from
// the system's randomness.
//
// It is not a secret and not a credential — it is displayed on the page and
// presented back — so what it has to be is unpredictable before the page exists,
// which is what keeps an attacker from committing to a value and then arranging
// for a person to answer a flow that carries it.
func approvalNumber() (string, error) {
	n, err := rand.Int(rand.Reader, approvalNumberMax)
	if err != nil {
		return "", fmt.Errorf("mcpauth: reading randomness: %w", err)
	}
	return fmt.Sprintf("%0*d", approvalDigits, n.Int64()), nil
}

// firstOf answers the first of the named parameters that carries a value, which
// is how a request is refused for carrying a token in its URL without this
// package ever choosing which parameter to read a token from.
func firstOf(q url.Values, names ...string) string {
	for _, name := range names {
		if value := q.Get(name); value != "" {
			return value
		}
	}
	return ""
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
	Host       string
	Scopes     []string
	Number     string
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
// refusal rather than a half-written page: a person must never approve on a
// page that does not say what they are approving.
func writeConsentPage(w http.ResponseWriter, view consentView) {
	var body strings.Builder
	if err := consentPage.Execute(&body, view); err != nil {
		refusePage(w, http.StatusInternalServerError, "The consent page could not be rendered.")
		return
	}
	noFraming(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_, _ = w.Write([]byte(body.String()))
}

// noFraming refuses to be framed, on every page a person reads.
//
// A consent page is the one page in this flow a person acts on, and a page an
// attacker can put in an iframe is a page whose Allow button can be placed
// under a click the person meant for something else. `frame-ancestors 'none'`
// is the rule and `X-Frame-Options: DENY` is the same rule for a browser that
// reads the older header; both are set because the set of browsers that read
// only one of them is not empty.
func noFraming(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
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
	noFraming(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	// The message is a constant written at the site that refused, never a
	// value a caller sent, so there is nothing here to escape.
	_, _ = fmt.Fprintf(w, refusalPage, message)
}
