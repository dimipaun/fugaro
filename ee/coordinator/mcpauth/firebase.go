// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package mcpauth is the coordinator's MCP front door: the authorization
// server a standard MCP client registers with and signs in to, the resource
// server that reads the bearer token it issues, and the token records behind
// both (docs/mcp-server/implementation-plan.md, tasks M1-M10; the design is
// docs/mcp-server/spec.md, the decisions D920-D934 are its reconciliation).
//
// The package is the front door's home because the coordinator is where the
// front door lives (D196): a project runs one coordinator, and this half of it
// answers strangers on the public internet (D920), which is why every rule here
// is about what a caller may reach rather than about what the coordinator can
// do.
//
// This file is M6: Firebase at the authorize endpoint. The person's sign-in is
// approved by a paired device and the session that approval produced is the
// signer (D923), so the coordinator only ever VERIFIES an ID token — it mints
// none, it holds no signer role, and there is no code path here that could
// answer "this person signed in" for somebody who did not. What is verified is
// the token the installation's own project signs: the issuer is built from the
// project id, the audience is compared against the bare project id, and the
// signature is checked against the keys Google publishes, which is the same set
// of rules internal/credsvc applies to the identities this repository already
// trusts.
//
// It is a second implementation of those rules and not a shared call: credsvc
// is another service's package, with its own configuration surface and its own
// caller classes, and its verification is a method on an Authenticator that
// admits a run's budget identity or an installation's operators rather than a
// person at an OAuth authorize endpoint. The right fix, if a reviewer judges one
// necessary, is to extract the verifier and let both call it; that is a change
// to a package this task does not own, so it is a finding for the owner rather
// than a refactor here.
package mcpauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	// maxIDTokenBytes bounds a presented ID token before it is parsed: a token
	// is one header value or one form field, and one past the bound is refused
	// at the door rather than decoded.
	maxIDTokenBytes = 8 << 10
	// idTokenClockSkew is how far ahead of this service's clock a token's iat
	// or nbf may be. The service's clock and the signer's are not the same
	// clock, and a token that is a minute early is a token a person is holding.
	idTokenClockSkew = 2 * time.Minute
	// secureTokenIssuerPrefix is the issuer Firebase writes into an ID token of
	// project P: the prefix with P appended. It is built rather than compared
	// loosely, so a token naming another project's issuer cannot be read as
	// this one's.
	secureTokenIssuerPrefix = "https://securetoken.google.com/"
	// maxEmailBytes bounds the address claim. Every address the identity
	// provider mints is a header-sized value, and the address here is text a
	// stranger's token chose.
	maxEmailBytes = 512
)

// ErrUnverifiedIDToken is a Firebase ID token this front door does not admit.
// Every refusal of a token is this error, whether the token was malformed, was
// signed by a key nobody published, named another project or carried
// unreadable claims — and none of them repeats a value out of the token, which
// is the rule the whole package keeps: a refusal reaches a log line and an
// answer body, and neither is a place for a stranger's text.
var ErrUnverifiedIDToken = errors.New("mcpauth: the Firebase ID token is not verified")

// Identity is a person as the installation's own identity provider attests
// them. It is what a verified ID token proves: who signed in, and the address
// that sign-in was for. It is NOT a permission — whether that identity may call
// anything is the project's launcher and operator lists' answer (D933), read
// where the token is turned into a call rather than here.
type Identity struct {
	// UID is the Firebase uid the token names: the sub claim, which the rules
	// themselves honour as auth.uid.
	UID string
	// Email is the address the token asserts, folded to lower case as ASCII.
	// The fold is this package's, not the token's, so a list entry is compared
	// against one spelling of an address rather than against the several a
	// Unicode fold would let a claim choose.
	Email string
}

// Verifier verifies the Firebase ID tokens this installation's project signs.
type Verifier struct {
	projectID string
	keys      KeySource
	now       func() time.Time
}

// VerifierOption configures a Verifier.
type VerifierOption func(*Verifier)

// WithVerifyClock sets the clock the token's window is read against (tests).
func WithVerifyClock(now func() time.Time) VerifierOption {
	return func(v *Verifier) { v.now = now }
}

// NewVerifier returns the verifier for one Firebase project, reading the
// published key set through keys.
//
// An empty project id or no key set at all is refused here rather than at the
// first call: both are configuration mistakes, and a verifier built anyway
// would refuse every person with a message that reads like a bad token.
func NewVerifier(projectID string, keys KeySource, opts ...VerifierOption) (*Verifier, error) {
	if projectID == "" {
		return nil, errors.New("mcpauth: a verifier needs the project id the tokens are issued for")
	}
	if keys == nil {
		return nil, errors.New("mcpauth: a verifier needs a key source: the published set is the only proof a token has")
	}
	v := &Verifier{projectID: projectID, keys: keys, now: time.Now}
	for _, o := range opts {
		o(v)
	}
	return v, nil
}

// VerifyIDToken verifies a Firebase ID token and answers the person it attests,
// or ErrUnverifiedIDToken, or ErrUnavailable when the key set could not be read
// at all (the key source's own sentinel).
//
// It is called from GET /oauth/authorize and from nowhere else (D923). The ID
// token is the proof of the sign-in step, and the single consumer is what makes
// it safe to run in a service that answers strangers: a token that is never a
// grant cannot be replayed at the token endpoint, and a token that is never a
// bearer cannot reach a tool. Any later path that accepts one — a second
// argument, a "just this once" branch — is a finding rather than a convenience,
// and the token endpoint's own grant vocabulary (ParseGrantType) has no member
// for it.
//
// The order of the checks is the order a caller can act on: the signature
// first, because nothing in the claims is worth reading before it, then who the
// token is for, then its window, then the identity's own fields.
func (v *Verifier) VerifyIDToken(ctx context.Context, raw string) (Identity, error) {
	head, claims, err := decodeIDToken(raw)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: the ID token is not a JWT", ErrUnverifiedIDToken)
	}
	if alg := claimString(head, "alg"); alg != "RS256" {
		return Identity{}, fmt.Errorf("%w: the ID token is not signed with RS256", ErrUnverifiedIDToken)
	}
	key, err := v.keys.Key(ctx, claimString(head, "kid"))
	if err != nil {
		if errors.Is(err, ErrUnknownKeyID) {
			// A kid nobody published is a refusal of THIS token, not a failure
			// of the service, and the key source's own message names no kid:
			// the kid is the caller's text and the package keeps no-echo.
			return Identity{}, fmt.Errorf("%w: %w", ErrUnverifiedIDToken, err)
		}
		return Identity{}, err
	}
	if err := verifyRS256(raw, key); err != nil {
		return Identity{}, fmt.Errorf("%w: the ID token's signature does not verify", ErrUnverifiedIDToken)
	}
	if aud := claimString(claims, "aud"); aud != v.projectID {
		// The audience of a Firebase ID token is the BARE project id — not the
		// coordinator's URL, which is this service's own audience on every other
		// route of the repository.
		return Identity{}, fmt.Errorf("%w: the ID token was issued for another audience", ErrUnverifiedIDToken)
	}
	if iss := claimString(claims, "iss"); iss != secureTokenIssuerPrefix+v.projectID {
		return Identity{}, fmt.Errorf("%w: the ID token was not issued by this project's Firebase", ErrUnverifiedIDToken)
	}
	if verified, ok := claims["email_verified"].(bool); !ok || !verified {
		// An unverified address on the project's lists is an address its holder
		// has not proved is theirs, and the lists are the whole permission
		// model here (D933).
		return Identity{}, fmt.Errorf("%w: the identity's email is not verified", ErrUnverifiedIDToken)
	}
	now := v.now()
	if exp, ok := claimInt(claims, "exp"); !ok || !now.Before(time.Unix(exp, 0)) {
		return Identity{}, fmt.Errorf("%w: the ID token has expired", ErrUnverifiedIDToken)
	}
	if iat, ok := claimInt(claims, "iat"); ok && time.Unix(iat, 0).After(now.Add(idTokenClockSkew)) {
		return Identity{}, fmt.Errorf("%w: the ID token is issued in the future", ErrUnverifiedIDToken)
	}
	if nbf, ok := claimInt(claims, "nbf"); ok && time.Unix(nbf, 0).After(now.Add(idTokenClockSkew)) {
		return Identity{}, fmt.Errorf("%w: the ID token is not valid yet", ErrUnverifiedIDToken)
	}
	// Both claims that carry the uid are required and must agree: sub is the one
	// the rules honour (auth.uid) and user_id is the copy Firebase writes beside
	// it, so a token naming a person in one and nothing or somebody else in the
	// other is refused rather than read as either.
	sub, uid := claimString(claims, "sub"), claimString(claims, "user_id")
	if sub == "" || uid == "" {
		return Identity{}, fmt.Errorf("%w: the identity carries no uid (sub and user_id are both required)", ErrUnverifiedIDToken)
	}
	if sub != uid {
		return Identity{}, fmt.Errorf("%w: the identity names two different uids", ErrUnverifiedIDToken)
	}
	email, ok := foldEmail(claimString(claims, "email"))
	if !ok {
		return Identity{}, fmt.Errorf("%w: the identity's email claim is not an address this front door can read", ErrUnverifiedIDToken)
	}
	return Identity{UID: sub, Email: email}, nil
}

// ErrUnsupportedGrant is a POST /oauth/token request whose grant_type is not
// one of the three this endpoint accepts.
var ErrUnsupportedGrant = errors.New("mcpauth: the token endpoint does not accept that grant type")

// GrantType is the grant a POST /oauth/token request presents.
//
// The vocabulary is CLOSED, and it lives here rather than beside the endpoint's
// dispatch because the rule it encodes is D923's and not a convenience of the
// endpoint: the Firebase ID token is consumed at GET /oauth/authorize and is
// never a grant, so the token endpoint has no member for it. A grant the
// endpoint does not know is refused rather than treated as none — an endpoint
// that read an unknown type as "no grant" would be one whose refusal a client
// cannot tell from its own mistake.
type GrantType string

const (
	// GrantAuthorizationCode is the code a person's consent produced.
	GrantAuthorizationCode GrantType = "authorization_code"
	// GrantRefreshToken is a rotating refresh token (D930).
	GrantRefreshToken GrantType = "refresh_token"
	// GrantClientCredentials is the unattended caller's grant (D929).
	GrantClientCredentials GrantType = "client_credentials"
)

// ParseGrantType reads a presented grant_type. Anything outside the three is
// refused, and the refusal names the rule and never the value: an ID token
// pasted into grant_type is the caller's own text, and it is exactly the text a
// refusal must not repeat.
func ParseGrantType(s string) (GrantType, error) {
	switch g := GrantType(s); g {
	case GrantAuthorizationCode, GrantRefreshToken, GrantClientCredentials:
		return g, nil
	}
	return "", fmt.Errorf("%w: the Firebase ID token is consumed at the authorize endpoint and is never a grant", ErrUnsupportedGrant)
}

// decodeIDToken decodes a compact JWT's header and payload WITHOUT verifying
// it, and nothing it answers is trusted on its own: VerifyIDToken holds the
// signature against the published keys before it reads a claim.
func decodeIDToken(tok string) (head, claims map[string]any, err error) {
	if tok == "" || len(tok) > maxIDTokenBytes {
		return nil, nil, errors.New("mcpauth: not a JWT of a usable size")
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, nil, errors.New("mcpauth: not a compact JWT")
	}
	if head, err = decodeSegment(parts[0]); err != nil {
		return nil, nil, err
	}
	if claims, err = decodeSegment(parts[1]); err != nil {
		return nil, nil, err
	}
	return head, claims, nil
}

// decodeSegment decodes one JWT segment: base64url, then exactly one JSON
// object. A segment carrying anything after the object is refused rather than
// half-read, because a payload that is one claim set and then another is not a
// payload whose claims are the ones that were signed.
func decodeSegment(s string) (map[string]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, errors.New("mcpauth: a JWT segment is not base64url")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, errors.New("mcpauth: a JWT segment is not a JSON object")
	}
	// Token, not More: More reports whether the next non-space byte is not a
	// closing bracket, so a segment ending in `}` would read as nothing left.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("mcpauth: a JWT segment carries more than one JSON value")
	}
	return out, nil
}

// verifyRS256 checks a compact JWT's RS256 signature against key.
func verifyRS256(tok string, key *rsa.PublicKey) error {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return errors.New("mcpauth: not a compact JWT")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return errors.New("mcpauth: the signature is not base64url")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig)
}

// foldEmail folds an address to lower case as ASCII, reporting false for
// anything that is not printable ASCII (no space) within the header bound.
//
// The ASCII-only rule is why this is not strings.ToLower alone: that folds by
// Unicode rules, so a claim whose spelling the issuer let the caller choose
// would be matched against a list entry it is not — U+212A KELVIN SIGN
// lowercases to 'k' and U+0130 to 'i'. Every address the identity provider
// mints is ASCII, so nothing a real person holds is lost by the rule.
func foldEmail(email string) (string, bool) {
	if !printableASCII(email, maxEmailBytes) {
		return "", false
	}
	return strings.ToLower(email), true
}

// printableASCII reports whether s is non-empty, at most max bytes, and
// printable ASCII with no space.
func printableASCII(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func claimString(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func claimInt(m map[string]any, k string) (int64, bool) {
	switch v := m[k].(type) {
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case float64:
		return int64(v), true
	}
	return 0, false
}
