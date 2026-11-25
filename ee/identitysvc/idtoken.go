// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// The identity project's Firebase ID token, verified (mobile identity plan task
// A1). This is the only document this service takes from a caller, and it is
// the whole of what a person's sign-in proves: the assertion minted from it
// says who signed in and nothing more.
//
// **What it is.** A Firebase ID token of the identity project's own project
// (D2781), so it is verified against the keys Google publishes for the
// `securetoken` signer — ONE set for every Firebase project in the world, which
// is why a project tells its tokens from another's by the `aud`/`iss` pair and
// not by any per-project key — against this project's own id as the audience,
// against its securetoken issuer, and against the window the token carries.
//
// **What it reads beyond that.** The sign-in's provider
// (`firebase.sign_in_provider`), the provider's own stable id for the person
// (`firebase.identities`), and `auth_time` — the moment the person actually
// authenticated, which is what a project's "recent enough" rule is applied to
// and which is NOT the token's issue time, since a token refreshes hourly over
// a sign-in that may be a day old.
//
// **The one rule of its own.** A password sign-in whose address the project has
// not verified is refused here. An email-and-password sign-in has no provider
// behind it at all: the verified address is the whole of what proves the person
// owns it, so signing one into an assertion would be asserting an address
// nobody proved (D2785). A federated sign-in's `email_verified` travels
// instead — which providers a project accepts and on what evidence is that
// project's accepted-provider policy (D2785, task P3), and a signer that
// decided it would take a decision the policy owns.
//
// **Why this is a third reading of the same rules.** mcpauth's front door and
// the workspaces signer each verify a Firebase ID token, and this service may
// call neither: the front door refuses an unverified address for every provider,
// which is the rule above turned into a policy, and the workspaces signer's
// verifier is unexported (its file says so and reports the duplication as a
// finding for the owner). What holds this copy to the others is that the rules
// are Firebase's own and are pinned by tests here for every way they can be
// broken — the signature, the audience, the issuer, the window, the uid — the
// same list the other two hold.

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

	"github.com/dimipaun/fugaro/internal/mobileauth"
)

const (
	// IssuerPrefix is how Firebase spells the securetoken issuer of a project:
	// the bare project id is appended to it. Both the ID token's `iss` and the
	// assertion's own `iss` use it, which is what lets a project compare the two
	// without holding a per-project secret.
	IssuerPrefix = "https://securetoken.google.com/"

	// providerPassword is `firebase.sign_in_provider` for an email-and-password
	// sign-in, the one provider whose verified address is the whole proof of
	// ownership.
	providerPassword = "password"

	// idTokenClockSkew is how far ahead of this service's clock a token's `iat`
	// or `nbf` may be. Two minutes, as the other two verifiers take it: clocks
	// disagree by seconds, and this is the bound on how much of that a token may
	// claim rather than a licence to accept an old one.
	idTokenClockSkew = 2 * time.Minute

	// maxIDTokenBytes bounds a presented token before it is parsed. It is one
	// header value, and one past the bound is refused at the door.
	maxIDTokenBytes = 8 << 10

	// maxProviderBytes bounds the provider name this verifier reads.
	maxProviderBytes = 64
)

// ErrUnverifiedIDToken is every ID token this service does not admit:
// malformed, signed by a key nobody published, minted for another project,
// expired, or carrying a person this code cannot read. Like every refusal here
// it repeats no value out of the token — the token is a bearer, and a refusal
// reaches a log line and an answer body.
var ErrUnverifiedIDToken = errors.New("identitysvc: the identity project's Firebase ID token is not verified")

// ErrUnknownKeyID is the published set holding no key with the id the token
// names. It is a refusal of THAT token and not a failure of this service, which
// is the distinction Verify keeps: a caller that read a rotation as an outage
// would look for a bad token where the fault is a set that has not refreshed.
var ErrUnknownKeyID = errors.New("identitysvc: the published key set holds no key with the token's key id")

// KeySource is the published Firebase signing key set, by key id: in a
// deployment, Google's JWKS for the securetoken signer, which one global set
// serves for every project. It is an interface here so the suite can hold a set
// generated in its own process.
type KeySource interface {
	// Key returns the public key with this id, ErrUnknownKeyID when the set
	// holds none, or the source's own error when the set could not be read at
	// all — a source that answered its own error for a missing key would make a
	// rotation indistinguishable from an outage.
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// Identity is who the identity project's ID token attests, and how they signed
// in. It carries no token and no credential: it is what the assertion is minted
// from.
type Identity struct {
	// Subject is the provider's own stable id for the person when the token
	// carries one (`firebase.identities`), and the Firebase account's uid when
	// it does not — an email-and-password sign-in's provider writes no id of its
	// own, and the uid is stable per account.
	Subject string
	// UID is the Firebase account's own id — the `sub`/`user_id` pair above —
	// and the value this project's own nodes are keyed on (K1's paired list is
	// one node per uid). It is Subject for a sign-in whose provider writes no id
	// of its own and differs from it otherwise, and it is not a secret: what the
	// sealed paired list hides is its contents, not who the person is (spec's
	// resolved decisions).
	UID string
	// Email is the address the project attested, folded the way this
	// repository's member records fold one, and EmailVerified is the project's
	// own statement about it.
	Email         string
	EmailVerified bool
	// Provider is `firebase.sign_in_provider`. An empty one is refused: a
	// sign-in whose provider this code cannot read is one whose admission would
	// rest on a value nobody checked.
	Provider string
	// AuthTime is when the person authenticated, in unix seconds.
	AuthTime int64
}

// IDTokenVerifier verifies a Firebase ID token of one identity project.
type IDTokenVerifier struct {
	project string
	keys    KeySource
	now     func() time.Time
}

// NewIDTokenVerifier returns the verifier for one identity project. The project
// and the key set are both required: a verifier with no project would admit
// every Firebase project's tokens, and one with no keys can verify nothing.
func NewIDTokenVerifier(project string, keys KeySource, opts ...Option) (*IDTokenVerifier, error) {
	if project == "" {
		return nil, errors.New("identitysvc: an ID token verifier needs the identity project's own id, and without it every project's tokens have this project's audience")
	}
	if keys == nil {
		return nil, errors.New("identitysvc: an ID token verifier needs the published key set the tokens are checked against")
	}
	v := &IDTokenVerifier{project: project, keys: keys, now: time.Now}
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	if o.now != nil {
		v.now = o.now
	}
	return v, nil
}

// Verify checks one presented token and answers the person it attests.
//
// The order is the order a caller can act on: the signature first, because
// nothing in the claims is worth reading before it, then who the token is for,
// then its window, then the identity's own fields. Every failure that is the
// token's is ErrUnverifiedIDToken carrying the rule that refused it; a key set
// that could not be read at all is the source's own error and stays one, so a
// deployment fault is never answered as a person's bad sign-in.
func (v *IDTokenVerifier) Verify(ctx context.Context, raw string) (Identity, error) {
	head, claims, sig, err := splitJWT(raw)
	if err != nil {
		return Identity{}, refuseTokenf("the ID token is not a JWT")
	}
	if alg := claimString(head, "alg"); alg != "RS256" {
		return Identity{}, refuseTokenf("the ID token is not signed with RS256")
	}
	key, err := v.keys.Key(ctx, claimString(head, "kid"))
	if err != nil {
		if errors.Is(err, ErrUnknownKeyID) {
			return Identity{}, fmt.Errorf("%w: %w", ErrUnverifiedIDToken, err)
		}
		return Identity{}, err
	}
	if err := verifyRS256(key, raw, sig); err != nil {
		return Identity{}, refuseTokenf("the ID token's signature does not verify")
	}
	// The audience of a Firebase ID token is the BARE project id, never a URL,
	// and the issuer is that id under the securetoken prefix: together they are
	// what tells this project's tokens from every other Firebase project's.
	if aud := claimString(claims, "aud"); aud != v.project {
		return Identity{}, refuseTokenf("the ID token was issued for another audience")
	}
	if iss := claimString(claims, "iss"); iss != IssuerPrefix+v.project {
		return Identity{}, refuseTokenf("the ID token was not issued by this project's Firebase")
	}
	now := v.now()
	if exp, ok := claimInt(claims, "exp"); !ok || !now.Before(time.Unix(exp, 0)) {
		return Identity{}, refuseTokenf("the ID token has expired")
	}
	if iat, ok := claimInt(claims, "iat"); ok && time.Unix(iat, 0).After(now.Add(idTokenClockSkew)) {
		return Identity{}, refuseTokenf("the ID token is issued in the future")
	}
	if nbf, ok := claimInt(claims, "nbf"); ok && time.Unix(nbf, 0).After(now.Add(idTokenClockSkew)) {
		return Identity{}, refuseTokenf("the ID token is not valid yet")
	}
	// Both claims that carry the uid are required and must agree: `sub` is the
	// one a project's own rules honour, and `user_id` is the copy Firebase
	// writes beside it, so a token naming a person in one and nothing or
	// somebody else in the other is refused rather than read as either.
	sub, uid := claimString(claims, "sub"), claimString(claims, "user_id")
	if sub == "" || uid == "" || sub != uid {
		return Identity{}, refuseTokenf("the identity carries no readable uid")
	}
	// The provider before the address, because the address's own rule depends on
	// it. A sign-in whose provider this code cannot read is refused rather than
	// carried as empty: the assertion would then say nothing about how the
	// person signed in, and a project's accepted-provider policy would be
	// comparing the empty string.
	provider := signInProvider(claims)
	if provider == "" || len(provider) > maxProviderBytes {
		return Identity{}, refuseTokenf("the identity carries no readable sign-in provider")
	}
	claimed := claimString(claims, "email")
	email, err := mobileauth.NormalizeEmail(claimed)
	if err != nil {
		return Identity{}, refuseTokenf("the identity's address is not one this service can read")
	}
	verified, ok := claims["email_verified"].(bool)
	if !ok {
		return Identity{}, refuseTokenf("the identity's address carries no verified statement")
	}
	if provider == providerPassword && !verified {
		return Identity{}, refuseTokenf("the sign-in is a password one whose address the project has not verified")
	}
	// `auth_time` is required: a project's "recent enough" rule is applied to
	// it, and a token without one would be admitted with a zero that reads as
	// 1970 — either refused by every policy, or, worse, read as recent by a
	// comparison that did not check.
	authTime, ok := claimInt(claims, "auth_time")
	if !ok || authTime <= 0 {
		return Identity{}, refuseTokenf("the identity carries no readable authentication time")
	}
	return Identity{
		Subject:       subjectOf(claims, provider, sub),
		UID:           uid,
		Email:         email,
		EmailVerified: verified,
		Provider:      provider,
		AuthTime:      authTime,
	}, nil
}

// subjectOf reads the provider's own stable id for the person, which Firebase
// writes under `firebase.identities` per provider — the value the workspace
// keys a member on beside the address, so that an address reassigned to somebody
// else does not inherit the first person's pairings (D2785, F9). A provider that
// writes none (an email-and-password sign-in writes the address itself under
// `email`, not an id) answers the Firebase uid, which is itself stable per
// account.
func subjectOf(claims map[string]any, provider, uid string) string {
	fb, _ := claims["firebase"].(map[string]any)
	identities, _ := fb["identities"].(map[string]any)
	ids, _ := identities[provider].([]any)
	if len(ids) == 0 {
		return uid
	}
	id, _ := ids[0].(string)
	if id == "" {
		return uid
	}
	return id
}

// signInProvider reads `firebase.sign_in_provider` out of verified claims. The
// object is written by Firebase itself and not by a project's custom claims,
// which is why the provider is read from there and from nowhere else.
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

// splitJWT breaks a compact JWT into its header, its claims and its signature
// segment, refusing anything that is not exactly three non-empty segments. The
// claims come back as a decoded object so that the caller reads them once,
// strictly.
func splitJWT(raw string) (head, claims map[string]any, sig string, err error) {
	if raw == "" || len(raw) > maxIDTokenBytes {
		return nil, nil, "", errors.New("not a JWT of a usable size")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, nil, "", errors.New("not a compact JWT")
	}
	if head, err = decodeSegment(parts[0]); err != nil {
		return nil, nil, "", err
	}
	if claims, err = decodeSegment(parts[1]); err != nil {
		return nil, nil, "", err
	}
	return head, claims, parts[2], nil
}

// decodeSegment decodes one JWT segment: base64url, then exactly one JSON
// object. A segment carrying anything after the object is refused rather than
// half-read, because a payload that is one claim set and then another is not a
// payload whose claims are the ones that were signed.
func decodeSegment(s string) (map[string]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, errors.New("a JWT segment is not base64url")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, errors.New("a JWT segment is not a JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("a JWT segment carries more than one JSON value")
	}
	return out, nil
}

// verifyRS256 checks a compact JWT's signature against key. The signing input
// is the token up to its signature segment — the header and the payload as they
// arrived — so a claim edited after signing is a signature that does not verify,
// which is the whole of what makes the claims readable.
func verifyRS256(key *rsa.PublicKey, raw, sig string) error {
	if key == nil {
		return errors.New("identitysvc: no key")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return errors.New("identitysvc: the signature is not base64url")
	}
	sum := sha256.Sum256([]byte(raw[:len(raw)-len(sig)-1]))
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], decoded)
}

// claimString reads a claim a token must carry as a string, answering the empty
// string for anything else: every caller compares the value or requires it to
// be non-empty, so a claim of the wrong type is refused by the rule it was read
// for rather than by a message about its type.
func claimString(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// claimInt reads a numeric claim as the exact integer the signer wrote, and
// reports false for anything else — including a fractional or out-of-range
// value, which a conversion to int64 would silently narrow into a window nobody
// wrote.
func claimInt(m map[string]any, k string) (int64, bool) {
	n, ok := m[k].(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}
