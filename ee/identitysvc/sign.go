// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// The identity project's assertion signer (mobile identity plan task A1,
// D2781/D2792): one verified Firebase ID token in, one project-bound assertion
// out, signed through Cloud KMS with an Ed25519 key.
//
// **What the assertion is for.** A project's security service accepts a person
// on a document the identity project signed, and never on the person's own
// sign-in token: a raw Firebase ID token has the identity project as its
// audience, so a project that took one would be accepting a credential minted
// for somebody else and replayable at every other project inside its lifetime
// (spec.md, "Why a workspace-bound assertion instead of the raw ID token"). The
// assertion therefore names exactly ONE audience and carries the ONE challenge
// the caller asked for, and it lives two minutes (O4): long enough to cross from
// this service to a project, short enough that a copy of it is worth little.
//
// **What it will not do.** It signs only with a key the served, root-signed key
// set has published and only while that set is unexpired: the owner creates a
// KMS key version and signs the set that contains it before the signer uses it
// (Check 737, O7's round-5 resolution), so a signer that could sign under an
// unpublished key would be signing assertions no project can verify, and one
// that signed anyway after a set lapsed would be signing under a key its owner
// has retired. Every one of those paths is a refusal and not a smaller
// assertion, and none of them reaches the key ring: TestSignRefuses* holds each.
//
// **The claim names are C1's.** The set here — `iss`, `aud`, `sub`, `email`,
// `email_verified`, `provider`, `auth_time`, `jti`, `iat`, `exp`, `challenge` —
// is the one the plan fixes for the assertion and pairing contract
// (internal/mobilesession, task C1), which fixes its own vectors; the challenge
// claim carries the QR nonce at pairing and a service-issued challenge at
// restore (D2792). The reading is as strict as this repository's other wire
// decoders: the payload is one claim set, and a project that read the claims it
// knows and ignored the rest would accept whatever a signer chose to put beside
// them.
//
// The rest of the deployment is not this file's: the KMS key ring, the JWKS
// route and the key-set publication are A2's and A3's, and the project-side
// verifier is P1's, in internal/ where nothing outside ee/ can reach it (F11).

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/mobilesession"
)

const (
	// AssertionTTL is how long a minted assertion lives. Two minutes is the
	// figure O4 accepted: an assertion travels from this service to a person's
	// phone to a project's security service, and that is seconds, while a copy
	// of the document that outlives the hand-off is a replay window.
	AssertionTTL = 2 * time.Minute

	// assertionAlg is the only algorithm this service mints with. An Ed25519
	// KMS key signs EdDSA and nothing else, and the header is read by the
	// project's verifier, which refuses any other alg — `none` included, because
	// a signature nobody made is not a signature.
	assertionAlg = "EdDSA"
	assertionTyp = "JWT"

	// maxAudienceBytes and maxChallengeBytes bound the two names a caller sends
	// before they are written into a signed document. Both are names of things,
	// not credentials: an audience is a project's own identifier and a challenge
	// is a nonce.
	maxAudienceBytes  = 512
	maxChallengeBytes = 512

	// maxServedSetBytes bounds the served key-set document before it is read.
	// It holds a handful of public keys and three numbers.
	maxServedSetBytes = 64 << 10
)

// The refusals of a request this signer will not sign for. They are sentinels
// of their own rather than one error with a message, because a caller decides
// what to do from which one it got: a token that is not this project's is the
// person's to fix, a request naming no audience or no challenge is the app's,
// and a key set this service cannot use is the deployment's. None of them
// repeats a value out of the request (the token is a bearer and reaches a log
// line and an answer body).
var (
	// ErrNoAudience is a request that names no project, or names one that is not
	// a name: an assertion is bound to exactly one audience, so a service that
	// substituted one of its own would mint a document its holder could replay
	// anywhere.
	ErrNoAudience = errors.New("identitysvc: the request names no audience an assertion may be bound to")
	// ErrNoChallenge is a request carrying no challenge. The challenge is what
	// ties an assertion to the one pairing or restore it was asked for (D2792),
	// so an assertion minted without one is minted for nothing in particular.
	ErrNoChallenge = errors.New("identitysvc: the request carries no challenge an assertion may be bound to")
	// ErrKeyNotServed is the signing key not being one the served, unexpired,
	// root-signed key set publishes. It is the fail-closed half of O7's round-5
	// resolution: the signer signs only under a key its owner has published and
	// only while the publication is in its life.
	ErrKeyNotServed = errors.New("identitysvc: the signing key is not in the served key set, or the set is no longer in its life")
)

// Assertion is an assertion's claims. Every field is written here and read by
// the project's verifier (P1), and the JSON keys are the wire contract both
// halves are held to.
type Assertion struct {
	// Issuer is the identity project's own securetoken issuer. It is how a
	// project tells an assertion of this project's from one of another's
	// without reading a key: Google publishes one key set for every Firebase
	// project, so the `iss`/`aud` pair is the whole of that distinction.
	Issuer string
	// Audience is the ONE project the assertion is for, as its caller named it.
	Audience string
	// Subject is the provider's own stable id for the person — the value
	// `firebase.identities` carries for the provider that signed them in — or,
	// for a sign-in whose provider writes none (email and password), the
	// Firebase account's uid. It is what a member record is keyed on beside the
	// address, so that a reassigned address does not inherit the first person's
	// pairings (D2785, F9).
	Subject string
	// Email is the address the identity project attested, as the token carried
	// it, and EmailVerified is its own statement about that address. The flag
	// travels rather than being decided here: which providers a project accepts
	// and on what evidence is the project's own policy (D2785, task P3).
	Email         string
	EmailVerified bool
	// Provider is the `firebase.sign_in_provider` the sign-in used.
	Provider string
	// AuthTime is when the person last authenticated with the provider, in unix
	// seconds. It is the claim a project's "recent enough" rule is applied to
	// (24 hours by O4), and it is not the token's issue time: a token refreshes
	// hourly while the sign-in behind it may be a day old.
	AuthTime int64
	// JTI is the assertion's own id, fresh for every mint. It is what makes an
	// assertion single-use at the project's compare-and-set store, and it is a
	// value and never a secret.
	JTI string
	// IssuedAt and ExpiresAt bound the assertion's life, in unix seconds.
	IssuedAt  int64
	ExpiresAt int64
	// Challenge is the QR nonce of a pairing or a service-issued restore
	// challenge, the one the caller named.
	Challenge string
}

// claims is the assertion as the wire carries it. It is a map and not the
// struct: the struct is this package's reading, and the keys are the contract.
func (a Assertion) claims() map[string]any {
	return map[string]any{
		"iss":            a.Issuer,
		"aud":            a.Audience,
		"sub":            a.Subject,
		"email":          a.Email,
		"email_verified": a.EmailVerified,
		"provider":       a.Provider,
		"auth_time":      a.AuthTime,
		"jti":            a.JTI,
		"iat":            a.IssuedAt,
		"exp":            a.ExpiresAt,
		"challenge":      a.Challenge,
	}
}

// KMSigner is the whole of what this package needs of Cloud KMS: sign bytes
// with the one asymmetric key version this deployment is configured with. The
// seam is this narrow so the suite can hold a signing key generated in its own
// process (.claude/rules/ee.md R5: no key material in a file here), and the
// program wires the real call in cmd/identitysvc.
type KMSigner interface {
	Sign(ctx context.Context, payload []byte) ([]byte, error)
}

// ServedSet is the served, root-signed key set as far as the signer reads it:
// the id of every signing key the set publishes, and the moment the set stops
// being valid.
type ServedSet struct {
	KIDs      []string
	ExpiresAt time.Time
}

// KeySetSource answers the set the deployment currently serves. In a deployment
// it is the document A4's signing command wrote and A2 serves, read from the
// bucket only the owner writes and this service reads (A3); a test hands in a
// set of its own.
type KeySetSource interface {
	Current(ctx context.Context) (ServedSet, error)
}

// SignerConfig is what a deployment gives the signer. Every field is required:
// a signer missing one would fail at the first request rather than at the
// start, which is the shape a deploy is most expensive in.
type SignerConfig struct {
	// Issuer is the identity project's securetoken issuer, the `iss` every
	// assertion carries. It is derived from the project id and never read from
	// a request.
	Issuer string
	// KeyID is the `kid` of the KMS key version that signs. The owner's key
	// ceremony pairs it with the key version, and the served set carries it
	// (Check 737).
	KeyID string
	// KMS signs the assertion's signing input.
	KMS KMSigner
	// Set is the served, root-signed key set the key above must appear in.
	Set KeySetSource
	// Tokens verifies the identity project's Firebase ID token a caller
	// presents. It is held here rather than by the handler so that a signer
	// cannot be reached with an unverified token at all: Sign's first act is
	// that verification.
	Tokens *IDTokenVerifier
}

// Option configures the verifier and the signer. The two share one option type
// because they share one clock, and a test that moved one and not the other
// would be testing a window no deployment has.
type Option func(*options)

type options struct {
	now    func() time.Time
	newJTI func() (string, error)
}

// WithClock sets the clock the token's window, the set's life and the
// assertion's own window are read against (tests).
func WithClock(now func() time.Time) Option {
	return func(o *options) { o.now = now }
}

// WithIDs sets the source of an assertion's `jti` (tests). One jti is one
// assertion and one use, so a deployment leaves this alone: freshID, 32 random
// bytes.
func WithIDs(newID func() (string, error)) Option {
	return func(o *options) { o.newJTI = newID }
}

// Signer mints the project-bound assertion.
type Signer struct {
	issuer string
	keyID  string
	kms    KMSigner
	set    KeySetSource
	tokens *IDTokenVerifier
	now    func() time.Time
	newJTI func() (string, error)
}

// NewSigner returns the signer for one identity project. Every missing argument
// is refused here rather than at the first request: a signer with no issuer, no
// key id, no key ring, no key set or no verifier is a deployment mistake, and
// one built anyway would report it as a refusal of somebody's sign-in.
func NewSigner(cfg SignerConfig, opts ...Option) (*Signer, error) {
	for _, f := range []struct {
		name  string
		value string
	}{{"issuer", cfg.Issuer}, {"key id", cfg.KeyID}} {
		if strings.TrimSpace(f.value) == "" {
			return nil, fmt.Errorf("identitysvc: a signer needs an %s, and an assertion without one verifies nowhere", f.name)
		}
	}
	if cfg.KMS == nil {
		return nil, errors.New("identitysvc: a signer needs the key ring it signs through")
	}
	if cfg.Set == nil {
		return nil, errors.New("identitysvc: a signer needs the served key set it signs under")
	}
	if cfg.Tokens == nil {
		return nil, errors.New("identitysvc: a signer needs the ID token verifier it admits a caller by")
	}
	s := &Signer{
		issuer: cfg.Issuer, keyID: cfg.KeyID, kms: cfg.KMS, set: cfg.Set, tokens: cfg.Tokens,
		now: time.Now, newJTI: freshID,
	}
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	if o.now != nil {
		s.now = o.now
	}
	if o.newJTI != nil {
		s.newJTI = o.newJTI
	}
	return s, nil
}

// Sign verifies an identity project's Firebase ID token and answers the
// assertion bound to exactly this audience and this challenge.
//
// The order is the order a caller can act on: the token first, because nothing
// in it is worth reading before its signature covers it and because a refusal
// of the person must not depend on this deployment's state; then the two names
// the assertion is bound to; then the key set, whose answer decides whether
// this service may sign at all; and only then the key ring. Nothing is signed
// until every one of those has passed, which is what the tests count.
func (s *Signer) Sign(ctx context.Context, idToken, audience, challenge string) (string, error) {
	person, err := s.tokens.Verify(ctx, idToken)
	if err != nil {
		return "", err
	}
	audience, err = readName(audience, maxAudienceBytes, ErrNoAudience)
	if err != nil {
		return "", err
	}
	challenge, err = readName(challenge, maxChallengeBytes, ErrNoChallenge)
	if err != nil {
		return "", err
	}
	if err := s.served(ctx); err != nil {
		return "", err
	}
	jti, err := s.newJTI()
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	return s.mint(ctx, Assertion{
		Issuer:        s.issuer,
		Audience:      audience,
		Subject:       person.Subject,
		Email:         person.Email,
		EmailVerified: person.EmailVerified,
		Provider:      person.Provider,
		AuthTime:      person.AuthTime,
		JTI:           jti,
		IssuedAt:      now.Unix(),
		ExpiresAt:     now.Add(AssertionTTL).Unix(),
		Challenge:     challenge,
	})
}

// served holds the key set's own answer: the key this service signs with must
// be one the set publishes, and the set must still be inside its life. A set
// that could not be read is a refusal too — signing then would be signing under
// a key whose publication nobody has seen, which is exactly what an expired or
// unreadable set cannot rule out.
func (s *Signer) served(ctx context.Context) error {
	set, err := s.set.Current(ctx)
	if err != nil {
		return fmt.Errorf("%w: it could not be read: %w", ErrKeyNotServed, err)
	}
	if !s.now().UTC().Before(set.ExpiresAt) {
		return fmt.Errorf("%w: the set's own life ended at %s", ErrKeyNotServed, set.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if !slices.Contains(set.KIDs, s.keyID) {
		return fmt.Errorf("%w: the set lists %d signing key(s) and not the one this service signs with", ErrKeyNotServed, len(set.KIDs))
	}
	return nil
}

// mint builds the compact JWS and has the key ring sign it.
//
// The header carries the kid, because the project's verifier selects the public
// key by it, and it carries no other field: an assertion's alg is EdDSA and its
// typ is JWT in every mint, so anything else in the header would be a value a
// reader could be asked to trust.
func (s *Signer) mint(ctx context.Context, a Assertion) (string, error) {
	head, err := json.Marshal(map[string]string{"alg": assertionAlg, "typ": assertionTyp, "kid": s.keyID})
	if err != nil {
		return "", fmt.Errorf("identitysvc: encoding the assertion's header: %w", err)
	}
	body, err := json.Marshal(a.claims())
	if err != nil {
		return "", fmt.Errorf("identitysvc: encoding the assertion's claims: %w", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	sig, err := s.kms.Sign(ctx, []byte(signing))
	if err != nil {
		return "", fmt.Errorf("identitysvc: signing the assertion: %w", err)
	}
	// The length is checked and not the signature: a verifier is what checks a
	// signature, and this service holds no public half to check it with. What a
	// length says is that the answer is an Ed25519 signature at all rather than
	// a truncated or foreign one an assertion would be assembled around.
	if len(sig) != ed25519.SignatureSize {
		return "", fmt.Errorf("identitysvc: the key ring answered a %d-byte signature, and an Ed25519 one is %d", len(sig), ed25519.SignatureSize)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// readName validates one of the two names an assertion is bound to: an
// audience or a challenge. It must be present, bounded and printable ASCII
// without whitespace — the shape every other identifier this repository signs
// has — because a name that carried a control or a space is one two writers
// would spell differently, and the project comparing it would be comparing
// bytes that never meant the same thing.
func readName(value string, max int, refuse error) (string, error) {
	if value == "" || len(value) > max {
		return "", refuse
	}
	for i := 0; i < len(value); i++ {
		if value[i] <= ' ' || value[i] > '~' {
			return "", refuse
		}
	}
	return value, nil
}

// ParseServedSet reads the served, root-signed key set — the compact JWS A4's
// signing command writes and A2 serves — for the two things this service needs
// from it: the kid of every signing key it publishes, and the moment its own
// life ends.
//
// The envelope's verification is deliberately NOT here. That the root key
// signed the document, and that its `seq` is at or above the floor, is the
// publisher's and the verifier's (A2, A4, P1), and a second copy of those rules
// in the signer would be a second place for them to drift. What this reads is a
// decision about this service's OWN key — whether the key it holds is one the
// set has published, and whether the set is still in its life — and every way
// the document can fail to answer that is a refusal rather than a key nobody
// published.
func ParseServedSet(doc []byte) (ServedSet, error) {
	if len(doc) == 0 || len(doc) > maxServedSetBytes {
		return ServedSet{}, errors.New("identitysvc: the served key set is empty or larger than a key set")
	}
	parts := strings.Split(strings.TrimSpace(string(doc)), ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return ServedSet{}, errors.New("identitysvc: the served key set is not a signed document")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ServedSet{}, errors.New("identitysvc: the served key set's payload is not base64url")
	}
	// The payload is C1's envelope (internal/mobilesession) — `format`, `seq`,
	// `iat`, `exp`, and the project's key set nested under `jwks` — which is the
	// shape A4's command writes, so the document is read as the contract's own
	// type rather than as a second copy of it here that can drift from what is
	// published.
	var envelope mobilesession.KeySet
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ServedSet{}, errors.New("identitysvc: the served key set's payload is not a key set")
	}
	kids := make([]string, 0, len(envelope.JWKS.Keys))
	for _, k := range envelope.JWKS.Keys {
		if k.Kid != "" {
			kids = append(kids, k.Kid)
		}
	}
	if len(kids) == 0 {
		return ServedSet{}, errors.New("identitysvc: the served key set publishes no key")
	}
	if envelope.Exp <= 0 {
		return ServedSet{}, errors.New("identitysvc: the served key set carries no expiry, and a set that never ends is one the signer could never stop using")
	}
	return ServedSet{KIDs: kids, ExpiresAt: time.Unix(envelope.Exp, 0).UTC()}, nil
}

// freshID is a fresh assertion id: 32 random bytes, hex. It is a value and
// never a secret — the assertion carries it in the clear and a project stores
// its hash — so its requirements are that it is unpredictable and that no two
// mints share one.
func freshID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("identitysvc: reading randomness: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
