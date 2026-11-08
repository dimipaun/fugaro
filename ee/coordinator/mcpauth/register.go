// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// This file is POST /oauth/register: Dynamic Client Registration (RFC 7591),
// the door a standard MCP client walks through before any person has signed in.
//
// **It is open, and that is the owner's answer** (D928, spec.md §3). A client
// registers with no credential at all, because a client that had to be
// provisioned first would not be a standard client. What keeps openness from
// being a hole is that a registration grants nothing: the record it writes
// carries no token, no family and no limits, so a registered client reaches no
// tool until a person signs in and consents, and an unattended caller cannot be
// made out of one at all: the client credentials grant is refused outright
// (D929 amended, M-f), so there is no path from a registration to a token with
// nobody behind it.
//
// **The secret is returned once and only its hash is kept** (spec.md §3, §6).
// The record is the same shape an operator's pre-registration writes, which is
// why the field exists at all: D929's unattended caller is a client with a
// secret, and one record type serves both doors. What the response advertises is
// `token_endpoint_auth_method: none` and not `client_secret_basic`, because that
// is what is true of the two grants this server answers: neither requires the
// secret, and the document would otherwise tell a native client to keep a
// long-lived value on a device for nothing.
//
// **A redirect URI is checked here and again at authorize, against the same
// rule** (D928), and this is the earlier of the two doors on purpose: a client
// that could never receive a code here is refused with a message naming the
// rule, rather than registering and failing at a person's sign-in.

const (
	// maxClientNameBytes bounds the name a client gives itself. The name is a
	// stranger's text and it is shown on a page a person consents on, so it is
	// held to printable ASCII within a header-sized bound rather than stored
	// as it arrived.
	maxClientNameBytes = 200
	// maxRedirectURIs bounds how many URIs one registration may name. A
	// registration is open to everyone, and a list that is unbounded is a
	// record whose size a stranger chooses.
	maxRedirectURIs = 10
)

// ErrRedirectURI is a redirect URI this installation will not send a code to.
// Every refusal of one is this error, whether the URI was unparseable, carried
// a scheme this server does not redirect to, or was simply not one the project
// allows — because the rule is one rule (checkRedirectURI) and the caller that
// reads it is a person reading a message, not a branch.
var ErrRedirectURI = errors.New("mcpauth: the redirect URI is not one this installation sends a code to")

// registrationRequest is RFC 7591's client metadata, as much of it as this
// server reads. The rest of the specification's fields are deliberately not
// modelled: a field this server would ignore is a field a client could set and
// believe it had been honoured, and RFC 7591 says a server MAY ignore what it
// does not implement. What it does not ignore is the redirect URI list, which
// is the whole of what a registration is for here.
type registrationRequest struct {
	// RedirectURIs are the URIs this client will receive a code at, each
	// checked against D928's rule before the record is written.
	RedirectURIs []string `json:"redirect_uris"`
	// ClientName is what the client calls itself, shown on the consent page.
	ClientName string `json:"client_name"`
}

// registrationResponse is RFC 7591 §3.2.1's answer. The secret is in it and
// nowhere else: the record holds the hash, and there is no path that returns
// the value a second time.
type registrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	Scope                   string   `json:"scope"`
}

// serveRegister writes one client registration.
//
// The two things it does not do are the two a registration endpoint is usually
// asked to: it grants nothing (no token, no family, no limit) and it requires
// no credential. What it does do is check every redirect URI before anything is
// written, so a refusal leaves no record at all.
func (s *Server) serveRegister(w http.ResponseWriter, r *http.Request) {
	var req registrationRequest
	if err := readJSONBody(w, r, &req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			"the registration body is not the JSON object RFC 7591 asks for: this endpoint reads a JSON client metadata document and nothing else"))
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			"a registration names at least one redirect URI: it is the only address this installation would send a code to, and a client that names none could never receive one"))
		return
	}
	if len(req.RedirectURIs) > maxRedirectURIs {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			fmt.Sprintf("a registration names at most %d redirect URIs", maxRedirectURIs)))
		return
	}
	for _, uri := range req.RedirectURIs {
		if err := s.checkRedirectURI(uri); err != nil {
			// The message names the rule and the URI the caller sent. Both are
			// the caller's own text and neither is a secret: a redirect URI is
			// registered to be shown to a person on the consent page, and a
			// refusal that did not name it would leave a client author guessing
			// which of its several URIs failed.
			writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(), err.Error()))
			return
		}
	}
	name := req.ClientName
	if name != "" && !printableClientName(name, maxClientNameBytes) {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			fmt.Sprintf("client_name is not a printable name of at most %d bytes", maxClientNameBytes)))
		return
	}

	id, err := NewID()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(), "the registration could not be written"))
		return
	}
	secret, err := NewValue()
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(), "the registration could not be written"))
		return
	}
	client := Client{
		ID:           id,
		Name:         name,
		RedirectURIs: slices.Clone(req.RedirectURIs),
		SecretHash:   TokenHash(secret),
		CreatedAt:    s.now(),
	}
	if err := s.store.PutClient(r.Context(), client); err != nil {
		// Nothing about the store's own failure is repeated to a stranger: a
		// public endpoint's answer says that it failed and no more, and the
		// detail belongs in the operator's log rather than in the client's.
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(), "the registration could not be written"))
		return
	}
	writeJSONStatus(w, http.StatusCreated, registrationResponse{
		ClientID:                id,
		ClientSecret:            secret,
		ClientIDIssuedAt:        client.CreatedAt.Unix(),
		ClientName:              name,
		RedirectURIs:            client.RedirectURIs,
		TokenEndpointAuthMethod: string(oidc.AuthMethodNone),
		GrantTypes:              []string{string(oidc.GrantTypeCode), string(oidc.GrantTypeRefreshToken)},
		ResponseTypes:           []string{"code"},
		Scope:                   strings.Join(ScopeVocabulary, " "),
	})
}

// checkRedirectURI is D928's rule, in the one place it is written: a loopback
// address is always allowed, because that is how a native client receives a
// code, and every other URI has to match the project's own allowlist exactly.
//
// **Exact match, no wildcards, no prefix matching, no custom schemes.** Each of
// those is a way for a URI that is not the one the project wrote down to be
// treated as if it were: `https://client.example.net/callback.evil` passes a
// prefix rule, `https://client.example.net/` passes a wildcard on the path, and
// a custom scheme is a URI the coordinator cannot reason about at all. The
// allowlist is a line in the project's own configuration and never a constant
// here, so a hosted client is allowlisted by the person who owns the
// installation.
func (s *Server) checkRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %q is not a URL", ErrRedirectURI, raw)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("%w: %q does not name an http or https address, and this installation redirects to neither a custom scheme nor anything else", ErrRedirectURI, raw)
	case u.Host == "":
		return fmt.Errorf("%w: %q names no host", ErrRedirectURI, raw)
	case u.User != nil:
		return fmt.Errorf("%w: %q carries credentials in its authority", ErrRedirectURI, raw)
	case u.Fragment != "":
		// RFC 6749 §3.1.2: the redirection endpoint URI must not include a
		// fragment, because the fragment of the request is the client's own
		// and a code written into it would be read by the browser and sent
		// nowhere.
		return fmt.Errorf("%w: %q carries a fragment", ErrRedirectURI, raw)
	}
	if isLoopbackRedirect(u) {
		return nil
	}
	if slices.Contains(s.cfg.RedirectAllowlist, raw) {
		return nil
	}
	return fmt.Errorf("%w: %q is neither a loopback address nor a URI on this project's own allowlist", ErrRedirectURI, raw)
}

// isLoopbackRedirect reports whether a URI is one of the two forms a native
// client receives a code at: `http://127.0.0.1:<port>` and `http://localhost:
// <port>` (D928), plus the IPv6 loopback, which is the same address written the
// other way.
//
// The host is compared as a whole and never as a suffix, so
// `http://127.0.0.1.example.net/cb` and `http://localhost.example.net/cb` are
// not loopback addresses — they are somebody else's hosts, and a suffix test
// would send a person's code to them. The host is also the parsed one: an
// authority that reads `127.0.0.1@evil.example.net` has `evil.example.net` as
// its host and is refused by the check above rather than read as loopback.
func isLoopbackRedirect(u *url.URL) bool {
	if u.Scheme != "http" {
		return false
	}
	switch host := u.Hostname(); {
	case host == "127.0.0.1", host == "::1":
		return true
	default:
		// A host is case-insensitive (RFC 3986 §3.2.2), and `LOCALHOST` is the
		// same name written the way a client's configuration happened to.
		return strings.EqualFold(host, "localhost")
	}
}

// printableClientName reports whether a client's name for itself is at most max
// bytes of printable ASCII, spaces included.
//
// It is deliberately not the package's printableASCII, whose floor is one above
// the space: that rule is the email fold's, where a space would make an address
// that is not one, and a client that called itself "an editor" would be refused
// by it. What is refused here is a control character — the name is rendered
// into a page a person reads, and html/template escapes the markup while
// nothing escapes a terminal escape sequence.
func printableClientName(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// readJSONBody decodes one JSON object from the request body under this
// package's size bound.
//
// It is deliberately NOT the strict decoder the store reads its own records
// with. The document here is a stranger's, written against RFC 7591, and a
// client that sends a metadata field this server does not implement is a client
// behaving exactly as the specification allows — refusing it for an unknown
// field would refuse every client that is more complete than this endpoint.
// What is decoded is the two fields above, and everything else is read past.
func readJSONBody(w http.ResponseWriter, r *http.Request, into any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(into); err != nil {
		return err
	}
	return nil
}
