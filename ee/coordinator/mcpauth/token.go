// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
)

// Token is what an access token carries (spec.md §6): the identity it acts
// for, the client it was issued to, the limits it is held to, and its expiry.
// There is no session behind it — the 2026-07-28 revision removed
// protocol-level sessions, so every request carries its own bearer token and
// every request is validated from this shape.
//
// It is a value and not a record. What is stored is a hash and never this
// (D930), and the value itself is shown once at mint: the shape is defined
// here, minted by the mint, and read by the resource server's validation on
// every call.
type Token struct {
	// Identity is what the token acts for: the exact `user:` entry of the
	// project's launcher or operator list that a person signed in as (D923,
	// D933), or an unattended client's own identity (D929). Which list entries
	// are read at all is the front door's rule and not this shape's — a
	// `group:`, `domain:` or `serviceAccount:` entry is refused where the
	// identity is resolved.
	Identity string
	// ClientID is the client registration the token was issued to. It is
	// what revocation and rotation are counted by: a refresh token's family
	// is the client's, so a token is never anonymous about who holds it.
	ClientID string
	// Limits are the token's own limits, already narrowed against the
	// project's when the token was minted (Narrow). They are held verbatim:
	// a token is not a second place a limit is decided.
	Limits Limits
	// ExpiresAt is when the token stops being valid. Access tokens are
	// short-lived (D930), and the zero time is refused rather than read as
	// "never": a token with no expiry is one that is never refused for age.
	ExpiresAt time.Time
}

// ErrTokenIncomplete refuses a token shape the front door could never resolve:
// no identity to authorize, no client to count its family by, or no expiry to
// bound it. It is refused at mint, where the reason can be shown to the
// operator, rather than stored and refused on every call.
var ErrTokenIncomplete = errors.New("a token must carry an identity, a client id and an expiry")

// Validate is the mint-time check of the token's shape, and the one place the
// mint asks whether a token may be issued at all: the four things this shape
// needs and the limits' own check (Limits.Validate), which is where a window
// that admits nothing and an unknown scope are refused.
func (t Token) Validate() error {
	switch {
	case t.Identity == "":
		return fmt.Errorf("mcpauth: identity is empty: %w", ErrTokenIncomplete)
	case t.ClientID == "":
		return fmt.Errorf("mcpauth: client id is empty: %w", ErrTokenIncomplete)
	case t.ExpiresAt.IsZero():
		return fmt.Errorf("mcpauth: expiry is unset: %w", ErrTokenIncomplete)
	}
	return t.Limits.Validate()
}

// This half of the file is POST /oauth/token and POST /oauth/revoke: the two
// grants D923 and D930 name, and the one delete that ends a token family.
//
// **The grant vocabulary is closed** (ParseGrantType): the authorization code a
// person's consent produced and the rotating refresh token. A Firebase ID token
// is not among them and never will be — it is consumed at the authorize
// endpoint and is not a grant anywhere (D923).
//
// **The client credentials grant is refused** (D929 amended, M-f: it stays off
// until it has an owner). ParseGrantType still names it, because the parser is
// M6's file and the vocabulary it holds is the specification's; what this
// endpoint does with it is refuse it, here, before any client is looked up, and
// the metadata does not advertise it. Nothing in this package mints a token for
// a client and no person: the limits such a token would carry, the client's own
// record's, have gone with the grant.
//
// **What proves an exchange depends on which grant it is** (spec.md §8: "the
// token endpoint needs the refresh token or the client secret"). Both remaining
// grants are public client grants: the code is bound to the client, the redirect
// URI and the code challenge, and the refresh token is itself a value shown
// once, so a secret is not required for either — a client secret a native client
// had to keep would be a second long-lived value on a device, which is the thing
// PKCE exists to avoid, and that is why the metadata advertises
// token_endpoint_auth_method "none". A secret that IS presented is verified on
// every grant, so a client that sends the wrong one is refused rather than
// quietly read as a client that sent none.

// tokenResponse is RFC 6749 §5.1's answer. `expires_in` is a number of seconds,
// and `refresh_token` is absent for the one grant that has no refresh half.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

// serveToken is the token endpoint.
func (s *Server) serveToken(w http.ResponseWriter, r *http.Request) {
	form, err := readForm(w, r)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			"the token request is not a form this endpoint can read: this endpoint takes application/x-www-form-urlencoded and nothing else"))
		return
	}
	grant, err := ParseGrantType(form.Get("grant_type"))
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrUnsupportedGrantType(), err.Error()))
		return
	}
	if grant == GrantClientCredentials {
		// Refused before the client is read, and refused as an unsupported
		// grant rather than as a client that may not have one: D929 amended
		// keeps this grant off, and an answer that depended on which client
		// asked would be a way to learn which client ids exist.
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrUnsupportedGrantType(),
			"the client credentials grant is not one this authorization server supports: it supports the authorization code grant and the refresh token grant"))
		return
	}
	clientID, secret, err := clientAuth(r, form)
	if err != nil {
		s.clientRefusal(w, r, err.Error())
		return
	}
	client, err := s.store.GetClient(r.Context(), clientID)
	if err != nil {
		// The store's own reason is not repeated: which client ids exist is
		// not something an unauthenticated caller of a public endpoint learns.
		s.clientRefusal(w, r, "the client id is not one this installation registered")
		return
	}
	if secret != "" && !secretMatches(client.SecretHash, secret) {
		s.clientRefusal(w, r, "the client secret is not the one this client registered with")
		return
	}

	switch grant {
	case GrantAuthorizationCode:
		s.exchangeCode(w, r, form, client)
	case GrantRefreshToken:
		s.exchangeRefresh(w, r, form, client)
	}
}

// exchangeCode is the authorization code grant (RFC 6749 §4.1.3).
//
// The code is spent by the first exchange that presents it, *before* the
// exchange is judged — including before the redirect URI and the verifier are
// checked. That is the fail-closed direction and it is deliberate: a code that
// survived a refused exchange is a code an attacker who read it out of a
// browser's history can keep guessing verifiers against, and a client whose
// exchange was refused starts the flow again, which is one page. Everything the
// code stands for is read out of the record and never out of the request, so an
// exchange cannot retarget a code that was issued for somewhere else.
func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request, form url.Values, client Client) {
	value := form.Get("code")
	if value == "" {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			"the authorization code grant carries the code the consent produced"))
		return
	}
	code, err := s.store.ConsumeCode(r.Context(), TokenHash(value))
	switch {
	case errors.Is(err, ErrCodeNotFound):
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"the authorization code is not one this installation issued, or it is no longer redeemable"))
		return
	case errors.Is(err, ErrCodeUsed):
		// OAuth 2.1 §4.1.3: a code presented twice is not merely refused, it
		// revokes what its first exchange issued. Two parties hold a code only
		// if one of them read it somewhere it should not have been, and the
		// family's tokens are the thing that must stop working while that is
		// being sorted out. A failure to revoke is not reported: the refusal is
		// the same either way, and a store that answered differently would be
		// telling the replayer whether the family it wants to kill still lives.
		if code.FamilyID != "" {
			_ = s.store.RevokeFamily(r.Context(), code.FamilyID)
		}
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"the authorization code was already redeemed: a code is single-use, and the second exchange of one is refused rather than answered twice"))
		return
	case err != nil:
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(),
			"the authorization code could not be read"))
		return
	}
	if code.ClientID != client.ID {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"the authorization code was issued to another client"))
		return
	}
	if code.RedirectURI != form.Get("redirect_uri") {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"redirect_uri is not the one the code was issued for: the exchange has to present it again, exactly"))
		return
	}
	if s.now().After(code.ExpiresAt) {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"the authorization code has expired"))
		return
	}
	// The library's own PKCE check (zitadel/oidc, D921). The method is S256
	// because that is the only method the authorize endpoint issues a code
	// for: the method is not a field of the record, and a plain challenge never
	// became one.
	if !oidc.VerifyCodeChallenge(&oidc.CodeChallenge{Challenge: code.Challenge, Method: oidc.CodeChallengeMethodS256}, form.Get("code_verifier")) {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"the code_verifier does not match the code_challenge the code was issued against"))
		return
	}
	// The code's scopes are what the person consented to, narrowed against the
	// installation's own limits. Nothing else narrows them: the client's record
	// holds no limits of its own, because the only grant that read them was the
	// client credentials grant and it is refused.
	//
	// The window and the Hotfix flag come from the installation alone, because
	// the record carries no window: Narrow takes the tighter side, so this
	// token's Hotfix is false whatever the project allows. Nothing in the flow
	// turns it on — no control on the approval page sets it — and where a person
	// would is a product decision the plan has not taken, so the flag is
	// narrower here than spec.md §6 describes rather than different: a token
	// that cannot ask for Hotfix is the fail-closed side of that rule.
	limits, err := Limits{Scopes: code.Scopes}.Narrow(s.cfg.Limits)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"the limits of this authorization are not ones this installation mints a token for: "+err.Error()))
		return
	}
	access, refresh, err := MintAt(r.Context(), s.store, code.Identity, client, limits, s.now())
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(),
			"the token could not be minted"))
		return
	}
	// The code's spent marker learns which family it minted, so that a replay
	// of the code has something to revoke. It is written after the mint
	// because the family is the mint's, and a marker written before it would
	// name a family that a failed mint never created.
	rec, err := s.store.LookupByHash(r.Context(), TokenHash(access))
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(),
			"the token could not be read back"))
		return
	}
	if err := s.store.BindCodeFamily(r.Context(), TokenHash(value), rec.FamilyID); err != nil {
		// The pair is out but the code cannot be made to answer for it, so a
		// replay would be a refusal that revokes nothing. The family is
		// revoked here instead: the token is worthless either way, and a
		// family that stops working is the fail-closed half of the two.
		_ = s.store.RevokeFamily(r.Context(), rec.FamilyID)
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(),
			"the authorization code could not be bound to the token it minted"))
		return
	}
	writeToken(w, access, refresh, limits.Scopes)
}

// exchangeRefresh is the refresh grant (RFC 6749 §6, D930): one rotation, and a
// token that was already rotated away is the theft signal that revokes the
// family rather than a miss.
//
// The record is read before the rotation for one reason: the rotation is the
// store's own compare-and-swap and it is the thing that must not be asked to
// decide something the caller could have refused. A presented token whose
// record names another client is refused here, before the family has moved.
func (s *Server) exchangeRefresh(w http.ResponseWriter, r *http.Request, form url.Values, client Client) {
	presented := form.Get("refresh_token")
	if presented == "" {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			"the refresh token grant carries the refresh token"))
		return
	}
	rec, err := s.store.LookupByHash(r.Context(), TokenHash(presented))
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"the refresh token is not one this installation issued, or its family was revoked"))
		return
	}
	if rec.Kind != KindRefresh {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"the token presented at the refresh grant is not a refresh token: an access token never extends its own family"))
		return
	}
	if rec.Token.ClientID != client.ID {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"the refresh token was issued to another client"))
		return
	}
	access, refresh, err := RotateAt(r.Context(), s.store, presented, s.now())
	switch {
	case errors.Is(err, ErrRefreshReplay), errors.Is(err, ErrWrongTokenKind):
		// The family is revoked by the store before this answer is written, so
		// the client that holds the current token has stopped working too. That
		// is D930's answer and not an accident of the refusal.
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"the refresh token was already rotated away: the family is revoked and the client has to authorize again"))
		return
	case errors.Is(err, ErrConflict):
		// Two exchanges of one refresh token landed at once — a double-submit,
		// or a client that retried — and the store's swap made the other one
		// the winner. The loser holds a token the winner has already rotated
		// away, so a 5xx here is the wrong answer twice over: it is what an
		// ordinary HTTP client retries, and the retry presents that spent
		// token, which is D930's theft signal — the answer would revoke the
		// family the winner is holding, and one double-submit would be a
		// logout. invalid_grant is terminal and carries no invitation to try
		// again, and the token the loser holds really is spent. What would keep
		// the loser working is spec.md §6's grace window, in which the loser is
		// answered with the winner's own new pair; no grace window is built.
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidGrant(),
			"the refresh token was rotated away by a concurrent exchange of the same token: this token is spent, and the client has to authorize again"))
		return
	case err != nil:
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(),
			"the refresh token could not be rotated"))
		return
	}
	rec, err = s.store.LookupByHash(r.Context(), TokenHash(access))
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, oauthError(oidc.ErrServerError(),
			"the token could not be read back"))
		return
	}
	writeToken(w, access, refresh, rec.Token.Limits.Scopes)
}

// serveRevoke is RFC 7009's revocation endpoint (D930): one delete of the
// token's whole family, after which no token of it resolves at all.
//
// It answers 200 to everything the specification says to answer 200 to — a
// token that was revoked, a token that is not one this installation issued, and
// a token that belongs to another client — because a revocation endpoint that
// told a caller which tokens exist would be an oracle. A token that IS this
// client's revokes its family; everything else does nothing.
//
// The client id is read from the request and not from the token, and the two
// have to agree: a caller may revoke what it holds, and nothing else.
func (s *Server) serveRevoke(w http.ResponseWriter, r *http.Request) {
	form, err := readForm(w, r)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, oauthError(oidc.ErrInvalidRequest(),
			"the revocation request is not a form this endpoint can read"))
		return
	}
	clientID, secret, err := clientAuth(r, form)
	if err != nil {
		s.clientRefusal(w, r, err.Error())
		return
	}
	client, err := s.store.GetClient(r.Context(), clientID)
	if err != nil {
		s.clientRefusal(w, r, "the client id is not one this installation registered")
		return
	}
	if secret != "" && !secretMatches(client.SecretHash, secret) {
		s.clientRefusal(w, r, "the client secret is not the one this client registered with")
		return
	}
	if value := form.Get("token"); value != "" {
		if rec, err := s.store.LookupByHash(r.Context(), TokenHash(value)); err == nil && rec.Token.ClientID == client.ID {
			// A failure to delete is not reported either: the endpoint's own
			// contract is that a client cannot tell a revocation that happened
			// from one that did not, and the next call with the token is what
			// shows which it was.
			_ = s.store.RevokeFamily(r.Context(), rec.FamilyID)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)
}

// clientAuth reads the client's own credentials, from the Authorization header
// or from the form, and refuses a request that carries both.
//
// The rule that a request carries one of the two and not both is RFC 6749
// §2.3.1's, and it is checked rather than assumed because the two are not
// equivalent: a proxy that read one of them and the server that read the other
// would be two parties disagreeing about who is calling.
func clientAuth(r *http.Request, form url.Values) (id, secret string, err error) {
	id, secret = form.Get("client_id"), form.Get("client_secret")
	if header := r.Header.Get("Authorization"); header != "" {
		user, pass, ok := r.BasicAuth()
		if !ok {
			return "", "", errors.New("the Authorization header is not a client_secret_basic credential")
		}
		if id != "" || secret != "" {
			return "", "", errors.New("the client authenticated twice: a request carries either the Authorization header or the form's client_id and client_secret, and not both")
		}
		return user, pass, nil
	}
	if id == "" {
		return "", "", errors.New("the request names no client: it carries client_id, or authenticates with the Authorization header")
	}
	return id, secret, nil
}

// secretMatches compares a presented secret against the hash a registration
// stored. The comparison is over the hashes rather than over the values, which
// makes it constant-time in the one thing that matters: the stored hash is
// fixed-length, so the comparison cannot leak how much of a guess was right.
func secretMatches(storedHash, presented string) bool {
	if storedHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(TokenHash(presented)), []byte(storedHash)) == 1
}

// writeToken answers a grant with the minted pair.
func writeToken(w http.ResponseWriter, access, refresh string, scopes []string) {
	writeJSONStatus(w, http.StatusOK, tokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int64(AccessTokenTTL.Seconds()),
		RefreshToken: refresh,
		Scope:        strings.Join(scopes, " "),
	})
}
