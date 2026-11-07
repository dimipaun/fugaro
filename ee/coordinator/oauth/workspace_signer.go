// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package oauth

// The workspace's signer: the one place a workspace Firebase token is ever
// presented, and the one place a member-bound assertion is minted
// (docs/workspaces/spec.md §3, §8's second reachable surface).
//
// **What it verifies, and against what.** The workspace token is a Firebase ID
// token of the WORKSPACE's own project (D1290), so it is verified the way this
// repository already verifies Firebase tokens — against Google's published
// Firebase signing keys (one global set, which is why the project tells tokens
// apart by its `aud`/`iss` pair and not by any per-project keys), against the
// workspace's own project as the audience and its securetoken issuer, and
// against `email_verified` — and this file does not reimplement those rules:
// it calls mcpauth's Verifier, the MCP front door's own reading of them
// (ee/coordinator/mcpauth, M6). What it adds is the fact that front door does
// not read — `firebase.sign_in_provider`, the provider pin of finding 7 — and
// the mint below.
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
	"crypto/rsa"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
)

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
	// deployment it is mcpauth.SecureTokenKeys over Google's JWKS URL; a test
	// hands it a set of its own.
	Keys mcpauth.KeySource
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
// one assertion and one use, so a deployment leaves this alone: mcpauth's
// NewValue, 32 random bytes.
func WithSignerIDs(newID func() (string, error)) SignerOption {
	return func(s *Signer) { s.newJTI = newID }
}

// Signer is the workspace's signer: the verified ID token in, the
// member-bound assertion out.
type Signer struct {
	project   string
	key       *rsa.PrivateKey
	verifier  *mcpauth.Verifier
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
	verifier, err := mcpauth.NewVerifier(cfg.FirebaseProject, cfg.Keys)
	if err != nil {
		return nil, err
	}
	s := &Signer{
		project:   cfg.FirebaseProject,
		key:       key,
		verifier:  verifier,
		providers: append([]string(nil), cfg.Providers...),
		ttl:       cfg.TTL,
		now:       time.Now,
		newJTI:    mcpauth.NewValue,
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
// The order is the order a caller can act on: the token is verified first —
// nothing in its claims is worth reading before that — then the provider is
// read out of the bytes that were just verified, then the member's own URL is
// held to the shape an audience may have, and only then is an assertion minted.
// A refusal repeats nothing out of the token: the token is a stranger's bearer.
func (s *Signer) Mint(ctx context.Context, idToken, exchangeURL string) (string, error) {
	identity, err := s.verifier.VerifyIDToken(ctx, idToken)
	if err != nil {
		return "", err
	}
	// Read AFTER the verification and out of the same bytes it admitted: the
	// signature covers the payload, so what is read here is what was signed,
	// and splitSignInProvider decodes it as strictly as the verifier did. The
	// alternative — teaching mcpauth's Verifier to report the provider itself —
	// is a change to the MCP design's own file and is reported to the owner
	// rather than made here.
	provider, err := splitSignInProvider(idToken)
	if err != nil {
		return "", fmt.Errorf("oauth: the ID token names no sign-in provider this workspace can read: %w", err)
	}
	if !slices.Contains(s.providers, provider) {
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
		Email:         identity.Email,
		EmailVerified: true,
		Provider:      provider,
	})
}
