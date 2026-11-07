// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package oauth

// The workspace's signer: the one place a workspace Firebase token is ever
// presented, and the one place a member-bound assertion is minted
// (docs/workspaces/spec.md §3, §8's second reachable surface).
//
// **What it verifies, and against what.** The workspace token is a Firebase ID
// token of the WORKSPACE's own project (D1290), so it is verified against
// Google's published Firebase signing keys (ONE global set, which is why the
// project tells tokens apart by its `aud`/`iss` pair and not by any per-project
// keys), against the workspace's own project as the audience and its securetoken
// issuer, against `email_verified`, and against the window the token carries.
//
// **Those rules are a second implementation, and that is reported rather than
// solved here.** They are the rules the MCP front door already applies to a
// Firebase token (ee/coordinator/mcpauth, M6), and this file may not call that
// verifier: no non-test file here may name that package at all, because its own
// guard refuses any file a binary links which names it until the task that wires
// the front door carries spec.md §8's two bounds (the note on Limits in
// workspace.go says what the guard is for). M6 met the same question against
// `credsvc`'s verifiers and ruled it the same way — "the right fix is to extract
// the verifier, and that is a finding for the owner rather than a refactor in
// this task" — and this is that finding a second time. What holds the two copies
// together is a test in this package (TestSignerVerifiesWhatTheFrontDoorVerifies),
// which IS allowed to name the front door: it runs one table of ID tokens through
// both verifiers and requires the same verdict from each, so a rule that moved on
// one side fails here rather than in production. Extracting the verifier into a
// package both can use, without naming the front door, is the owner's call.
//
// **What it adds to what the front door reads** is `firebase.sign_in_provider`,
// the provider pin of finding 7: `custom` and `anonymous` are refused before an
// assertion is minted, so the member's own re-check is the last line rather than
// the only one.
//
// **What it mints is bound to one member.** The assertion's `aud` is the
// exchange URL of the member the workspace's own per-member table declares
// (§5), so an assertion minted for one member is refused at another for the
// audience alone; its `jti` is fresh and single-use; and its window is short.
// It carries the person's address, the provider that signed them in and the
// workspace's project, and nothing else — no member token, no scope and no
// credential.
//
// **What it does not do: answer "which members are there?"** The mint takes one
// member's exchange URL as its argument and knows no member table, so there is
// no call here that could enumerate a group, and no answer of this file's
// varies with a member the caller did not name (spec.md §8).
//
// The signer's own deployment — what serves it, where the private key lives and
// how a member's exchange URL is looked up — is not this file's: what is here
// is the half a test can hold, verified token in and assertion out.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/mobileauth"
)

// The refusals of a workspace Firebase ID token. They are sentinels of their
// own rather than ErrUnverifiedAssertion's because the two documents arrive at
// two different ends of the flow from two different senders: one is a person's
// sign-in token at the workspace's signer, the other is a workspace's assertion
// at a member. A caller that had to tell them apart by reading a message would
// be a caller deciding from a string.
var (
	// ErrUnverifiedIDToken is every workspace ID token this signer does not
	// admit: malformed, signed by a key nobody published, minted for another
	// project, expired, or carrying claims it cannot read. Like every refusal in
	// this package it repeats no value out of the token: the token is a bearer,
	// and a refusal reaches a log line and an answer body.
	ErrUnverifiedIDToken = errors.New("oauth: the workspace Firebase ID token is not verified")
	// ErrUnknownKeyID is the published set holding no key with the id the token
	// names. It is the key source's own sentinel, and it is separate from
	// ErrUnverifiedIDToken so that a deployment can tell "a set that has not
	// refreshed yet" from "a token nobody signed".
	ErrUnknownKeyID = errors.New("oauth: the published key set holds no key with that id")
)

// KeySource is the published Firebase signing key set, by key id: in a
// deployment, Google's JWKS for Firebase tokens, which one global set serves for
// every project. It is an interface here for the reason the file comment gives —
// this package names no front door — so the concrete set is handed in by
// whatever wires the signer.
type KeySource interface {
	// Key returns the public key with this id, ErrUnknownKeyID when the set holds
	// none, or the source's own error when the set could not be read at all.
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// ErrProviderNotPinned is a token whose `firebase.sign_in_provider` is outside
// the pinned set (finding 7): `custom` and `anonymous` are the two the finding
// names, and any other value is refused by the same rule — the set is the
// providers the workspace's own Firebase carries, and a provider it does not
// carry is one the exchange never sees.
var ErrProviderNotPinned = errors.New("oauth: the sign-in provider is not one this workspace pins")

// DefaultAssertionTTL is how long a minted assertion lives. It is deliberately
// shorter than the longest one a member accepts (maxAssertionLifetime): the
// assertion travels from the workspace's signer to a person to the member's
// consent page, and a person completes that in a minute or two.
const DefaultAssertionTTL = 5 * time.Minute

// SignerConfig is what the workspace's own configuration gives the signer.
type SignerConfig struct {
	// FirebaseProject is the WORKSPACE's own Firebase project id: the ID
	// token's audience, and the issuer the assertion names. It is the workspace's
	// and not any member's (D1290).
	FirebaseProject string
	// AssertionKeyPEM is the private half of the key members pin. It is a
	// secret of the workspace's own deployment and it is never a member's: the
	// member holds only the public half, which is what makes an assertion
	// unforgeable at a member rather than merely unreadable.
	AssertionKeyPEM string
	// Keys is the published key set the ID tokens are verified against. In a
	// deployment it is Google's JWKS over the workspace's Firebase; a test hands
	// it a set of its own.
	Keys KeySource
	// Providers is the pinned provider set. Empty means PinnedProviders.
	Providers []string
	// TTL is how long a minted assertion lives. Zero means DefaultAssertionTTL.
	TTL time.Duration
}

// SignerOption configures a Signer.
type SignerOption func(*Signer)

// WithSignerClock sets the clock the assertion's window and the ID token's
// expiry are read against (tests).
func WithSignerClock(now func() time.Time) SignerOption {
	return func(s *Signer) { s.now = now }
}

// WithSignerIDs sets the source of the assertion's `jti` (tests). One jti is
// one assertion and one use, so a deployment leaves this alone: freshValue, 32
// random bytes.
func WithSignerIDs(newID func() (string, error)) SignerOption {
	return func(s *Signer) { s.newJTI = newID }
}

// Signer is the workspace's signer: the verified ID token in, the
// member-bound assertion out.
type Signer struct {
	project   string
	key       *rsa.PrivateKey
	keys      KeySource
	providers []string
	ttl       time.Duration
	now       func() time.Time
	newJTI    func() (string, error)
}

// NewSigner returns the signer for one workspace.
//
// Every missing argument is refused here rather than at the first sign-in: a
// signer with no project, no key or no key set is a deployment mistake, and one
// built anyway would report it as a refusal of somebody's token.
func NewSigner(cfg SignerConfig, opts ...SignerOption) (*Signer, error) {
	if cfg.FirebaseProject == "" {
		return nil, errors.New("oauth: a signer needs the workspace's own Firebase project id")
	}
	if cfg.Keys == nil {
		return nil, errors.New("oauth: a signer needs the published key set the ID tokens are verified against")
	}
	key, err := parseSigningKey(cfg.AssertionKeyPEM)
	if err != nil {
		return nil, err
	}
	s := &Signer{
		project:   cfg.FirebaseProject,
		key:       key,
		keys:      cfg.Keys,
		providers: append([]string(nil), cfg.Providers...),
		ttl:       cfg.TTL,
		now:       time.Now,
		newJTI:    freshValue,
	}
	if len(s.providers) == 0 {
		s.providers = append(s.providers, PinnedProviders...)
	}
	if s.ttl == 0 {
		s.ttl = DefaultAssertionTTL
	}
	for _, o := range opts {
		o(s)
	}
	if s.ttl <= 0 || s.ttl > maxAssertionLifetime {
		return nil, fmt.Errorf("oauth: a signer whose assertions live %s mints ones no member accepts", s.ttl)
	}
	return s, nil
}

// Mint verifies a workspace Firebase ID token and answers the assertion bound
// to exchangeURL: the exchange URL of the ONE member the caller is answering
// for, as the workspace's own per-member table declares it (§5).
//
// The order is the order a caller can act on: the token is verified first, and
// the provider is read out of the claims that verification admitted — nothing in
// the payload is worth reading before the signature covers it — then the
// member's own URL is held to the shape an audience may have, and only then is an
// assertion minted. A refusal repeats nothing out of the token: the token is a
// stranger's bearer.
func (s *Signer) Mint(ctx context.Context, idToken, exchangeURL string) (string, error) {
	person, err := s.verifyIDToken(ctx, idToken)
	if err != nil {
		return "", err
	}
	if !slices.Contains(s.providers, person.Provider) {
		return "", fmt.Errorf("%w: the workspace's Firebase carries the pinned set", ErrProviderNotPinned)
	}
	if err := exchangeURLOK(exchangeURL); err != nil {
		return "", err
	}
	jti, err := s.newJTI()
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	return mintAssertion(s.key, assertion{
		Issuer:        WorkspaceIssuer(s.project),
		Audience:      exchangeURL,
		Subject:       s.project,
		IssuedAt:      now.Unix(),
		ExpiresAt:     now.Add(s.ttl).Unix(),
		JTI:           jti,
		Email:         person.Email,
		EmailVerified: true,
		Provider:      person.Provider,
	})
}

// signedInPerson is who a workspace Firebase ID token attests and how they
// signed in: the address the workspace will assert, and the provider the
// member's own re-check is put to.
type signedInPerson struct {
	Email    string
	Provider string
}

// verifyIDToken verifies a Firebase ID token of the workspace's own project.
//
// The order of the checks is the order a caller can act on: the signature first,
// because nothing in the claims is worth reading before it, then who the token
// is for, then its window, then the identity's own fields — the same order the
// front door's verifier uses, and the same rules (the file comment says why they
// are a second copy and what holds the two together).
func (s *Signer) verifyIDToken(ctx context.Context, raw string) (signedInPerson, error) {
	head, claims, sig, err := splitAssertion(raw)
	if err != nil {
		return signedInPerson{}, refuseTokenf("the ID token is not a JWT")
	}
	if alg, _ := head["alg"].(string); alg != assertionAlg {
		return signedInPerson{}, refuseTokenf("the ID token is not signed with %s", assertionAlg)
	}
	key, err := s.keys.Key(ctx, tokenString(head, "kid"))
	if err != nil {
		if errors.Is(err, ErrUnknownKeyID) {
			// A kid nobody published is a refusal of THIS token, not a failure of
			// the service, and the key source's message names no kid: the kid is
			// the caller's text and this package keeps no-echo.
			return signedInPerson{}, fmt.Errorf("%w: %w", ErrUnverifiedIDToken, err)
		}
		return signedInPerson{}, err
	}
	if err := verifyRS256(key, raw, sig); err != nil {
		return signedInPerson{}, refuseTokenf("the ID token's signature does not verify")
	}
	// The audience of a Firebase ID token is the BARE project id, never the
	// workspace's URL and never a member's: it is the workspace's own project,
	// which is what tells one workspace's tokens from another's.
	if aud := tokenString(claims, "aud"); aud != s.project {
		return signedInPerson{}, refuseTokenf("the ID token was issued for another audience")
	}
	if iss := tokenString(claims, "iss"); iss != WorkspaceIssuer(s.project) {
		return signedInPerson{}, refuseTokenf("the ID token was not issued by this project's Firebase")
	}
	if verified, ok := claims["email_verified"].(bool); !ok || !verified {
		return signedInPerson{}, refuseTokenf("the identity's address is not verified")
	}
	now := s.now()
	if exp, ok := tokenInt(claims, "exp"); !ok || !now.Before(time.Unix(exp, 0)) {
		return signedInPerson{}, refuseTokenf("the ID token has expired")
	}
	if iat, ok := tokenInt(claims, "iat"); ok && time.Unix(iat, 0).After(now.Add(assertionClockSkew)) {
		return signedInPerson{}, refuseTokenf("the ID token is issued in the future")
	}
	if nbf, ok := tokenInt(claims, "nbf"); ok && time.Unix(nbf, 0).After(now.Add(assertionClockSkew)) {
		return signedInPerson{}, refuseTokenf("the ID token is not valid yet")
	}
	// Both claims that carry the uid are required and must agree: sub is the one
	// the workspace's own rules honour as auth.uid, and user_id is the copy
	// Firebase writes beside it, so a token naming a person in one and nothing or
	// somebody else in the other is refused rather than read as either.
	sub, uid := tokenString(claims, "sub"), tokenString(claims, "user_id")
	if sub == "" || uid == "" || sub != uid {
		return signedInPerson{}, refuseTokenf("the identity carries no readable uid")
	}
	email := tokenString(claims, "email")
	if _, err := mobileauth.NormalizeEmail(email); err != nil {
		return signedInPerson{}, refuseTokenf("the identity's address is not one this signer can read")
	}
	// The provider last, and read out of the claims the signature just admitted:
	// finding 7's pin is applied by the caller, and a token whose provider this
	// code cannot read answers the empty provider, which no pinned set holds —
	// one refusal an operator can act on rather than three that name the shape of
	// a claim.
	return signedInPerson{Email: email, Provider: signInProvider(claims)}, nil
}

// signInProvider reads `firebase.sign_in_provider` out of verified claims. An
// absent, empty or non-string value answers the empty provider, which the pinned
// set refuses (ErrProviderNotPinned): a token whose provider this code cannot
// read is a token whose admission would rest on a value nobody checked.
func signInProvider(claims map[string]any) string {
	fb, ok := claims["firebase"].(map[string]any)
	if !ok {
		return ""
	}
	provider, _ := fb["sign_in_provider"].(string)
	return provider
}

// refuseTokenf builds a refusal of an ID token: every one of them is
// ErrUnverifiedIDToken with the rule that refused it, and none of them repeats
// the token's bytes.
func refuseTokenf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnverifiedIDToken, fmt.Sprintf(format, args...))
}

// tokenString reads a claim a token must carry as a string, answering the empty
// string for anything else. It is deliberately lenient about the shape and
// strict about the meaning: every caller compares the value or requires it to be
// non-empty, so a claim of the wrong type is refused by the rule it was read for
// rather than by a message about its type — the same reading signInProvider
// takes, and the one the front door's verifier takes of the header's kid.
func tokenString(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// tokenInt reads a numeric claim as the exact integer the signer wrote, and
// reports false for anything else — including a fractional or out-of-range
// value, which a conversion to int64 would silently narrow into a window nobody
// wrote.
func tokenInt(m map[string]any, k string) (int64, bool) {
	n, ok := m[k].(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}

// freshValue is a fresh assertion id: 32 random bytes, hex. It is a value and
// never a secret — the jti is carried in the assertion in the clear and its hash
// is what a member stores (D1296) — so its only requirements are that it is
// unpredictable and that no two mints share one.
func freshValue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oauth: reading randomness: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
