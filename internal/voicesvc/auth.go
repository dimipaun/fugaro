package voicesvc

// The hop's front door: nothing else in the package may act on a call before
// Authenticator.VerifyDevice has admitted it. It is SECURITY-SENSITIVE and is
// reviewed as such.
//
// What it verifies is a Firebase Auth ID token — the credential the phone
// already holds as item (1) of D111's two items — for a uid of the `m~` family
// a paired device is minted with (internal/budget/token's MintDevice, D165).
// It is the verification internal/credsvc/auth.go does for a run's identity,
// applied to a device instead of a job: the same signature check against
// Google's published certificates, the same issuer and audience check against
// the installation's Firebase project, and the same rule that both claims
// carrying the uid must be present and agree. What is new is the uid's shape
// and the claim beside it (mu), and both are what make a run's identity
// unusable here.
//
// Why it is not the credentials service's Authenticator, reused: that
// verification is bound to a caller that is a job of this installation — it
// checks the Google ID token's email claim against the installation's job
// service accounts, and a run identity against a job, a live outcome node and
// a set of run claims (fb, fpr, fx, fp). A phone holds none of those, and a
// device token that also carried a run's claims would be a run identity and a
// device at once (MintDevice's own comment). Q2 was answered 2026-10-19: the
// hop is a new minimal service and not the credentials service.
//
// Three properties hold here and a test pins each:
//
//   - **A uid that is not a device's is refused by name.** `r~<slug>~<run>`,
//     `p~<session>` and an account's own uid all fail the shape check below,
//     which is the whole of "a run identity is refused as a device".
//   - **No value a caller presents is echoed.** A refusal names what is wrong
//     with the identity and never the token — including the key id, which is
//     8 KiB of caller-chosen bytes if the caller wants it to be, so the
//     refusal says the key id is not one Google publishes and does not repeat
//     it.
//   - **An unreadable key set is not a refusal of the caller.** It is
//     state_unavailable, because the difference between "this token is bad"
//     and "this service cannot tell" is the difference between an app that
//     falls back to local and an operator who is told to look at the service.

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrUnverified is a caller this service does not admit: the umbrella every
// identity refusal unwraps to, so a caller can answer the wire's
// token_unverified without reading a message.
var ErrUnverified = errors.New("voicesvc: the device identity is not verified")

// ErrUnknownKeyID is a token whose key id Google does not publish. It is a
// refusal of the token and not a failure of the service: the key set was read
// and does not hold it.
var ErrUnknownKeyID = errors.New("voicesvc: no published key for the token's key id")

const (
	// maxTokenBytes bounds the ID token this service will look at. A Firebase
	// ID token is about 1 KiB; the bound is what keeps a caller from handing
	// this process an arbitrarily large string to decode, and it is checked
	// before anything is parsed.
	maxTokenBytes = 8 << 10
	// clockSkew is the allowance on iat, and only on iat: an ID token minted
	// by a phone whose clock is slightly ahead is not a forged one. exp is
	// NOT skewed — a token past its hour is refused at the instant it says.
	clockSkew = 2 * time.Minute
	// deviceUIDChars is the length of a device uid's random part: 128 bits in
	// unpadded base64url, which is what internal/budget/token draws
	// (deviceUIDBytes). It is spelled here as the length of the shape this
	// service accepts, and TestDeviceShapeIsTheMintersOwn holds it to the
	// minter's.
	deviceUIDChars = 22
)

// deviceUIDPrefix is the family a device's uid belongs to. It is spelled again
// here rather than imported: internal/budget/token's constant is unexported,
// and widening it would widen the budget token package's own surface for a
// reader that only needs to know which family it is looking at.
// TestDeviceShapeIsTheMintersOwn is what holds the two spellings to one answer.
const deviceUIDPrefix = "m~"

// deviceClaim is the custom claim a device token carries: `mu`, the uid it was
// minted for. The rules require it to equal auth.uid (rules.go's userClaim),
// so a token that reached this service without it would be a token the
// database itself would not admit — and this service checks it for that
// reason and not as a second opinion.
const deviceClaim = "mu"

// KeySource is where a token's signing key comes from: the key id in the
// token's header is looked up and the RSA public key returned. It is
// structurally internal/credsvc's KeySource, and the production
// implementation is that package's GoogleKeys — the cache of Google's
// published certificates, read with a Cache-Control lifetime and refreshed on
// an unknown key id. The interface is declared here so this package's tests
// need no network and no certificate, and so the hop depends on the shape of
// the check and not on the credentials service.
type KeySource interface {
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// Identity is a verified caller: the device's uid, and nothing else.
//
// There is deliberately no project, no email and no role here. The project is
// the request's claim and not the token's (§4.4's first limit), and the
// device node's own fields — the administered email and the resolved role
// (mobileauth's NodeFieldEmail and NodeFieldRole) — are what `fugaro pair ls`
// and the rules read. This service needs the uid and the fact that the node
// exists; reading more would be reading what it does not enforce.
type Identity struct {
	// UID is the device's Firebase uid, `m~` and 22 base64url characters. It
	// is a key the operator prints and not a credential.
	UID string
}

// AuthConfig configures the device verifier.
type AuthConfig struct {
	// FirebaseProject is the installation's Firebase project: the audience
	// every ID token of it is minted for, and the project whose issuer prefix
	// the token must carry.
	FirebaseProject string
	// Keys is the source of the signing keys. It must be set.
	Keys KeySource
	// Now is the clock, replaced only by tests.
	Now func() time.Time
}

// Authenticator verifies a device's ID token. It is safe for concurrent use.
type Authenticator struct {
	cfg AuthConfig
	now func() time.Time
}

// NewAuthenticator returns the verifier, refusing a configuration that could
// not verify anything: an empty Firebase project would accept a token minted
// for any project, and a missing key source would accept none.
func NewAuthenticator(cfg AuthConfig) (*Authenticator, error) {
	if cfg.FirebaseProject == "" {
		return nil, errors.New("voicesvc: the device verifier needs the installation's Firebase project: it is the audience every ID token of it is minted for, and an empty one would accept a token issued for any project")
	}
	if cfg.Keys == nil {
		return nil, errors.New("voicesvc: the device verifier needs a key source")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Authenticator{cfg: cfg, now: now}, nil
}

// VerifyDevice verifies an ID token and returns the device it names.
//
// The order of the checks is the order of what they cost and what they prove:
// the shape of the token first (no decoding of a string that is not a JWT), the
// signature next (nothing in an unsigned document is a fact), and only then
// the claims. A token that fails any of them is ErrUnverified with the reason
// the app branches on, except an unreadable key set, which is not the
// caller's failure and is answered as state.
func (a *Authenticator) VerifyDevice(ctx context.Context, tok string) (Identity, error) {
	if tok == "" {
		return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: no ID token was presented", ErrUnverified))
	}
	if len(tok) > maxTokenBytes {
		return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: the ID token is over this service's bound of %d bytes", ErrUnverified, maxTokenBytes))
	}
	head, claims, err := decodeJWT(tok)
	if err != nil {
		return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: the ID token is not a JWT", ErrUnverified))
	}
	if alg := claimString(head, "alg"); alg != "RS256" {
		return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: the ID token is not signed with RS256", ErrUnverified))
	}
	key, err := a.cfg.Keys.Key(ctx, claimString(head, "kid"))
	if err != nil {
		if errors.Is(err, ErrUnknownKeyID) {
			return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: %w", ErrUnverified, err))
		}
		// The key set itself could not be read. That is this service's own
		// state and not the caller's, so it is NOT ErrUnverified: an app that
		// read it as a bad token would drop the token it holds.
		return Identity{}, refuse(ReasonStateUnavailable, err)
	}
	if err := verifySignature(tok, key); err != nil {
		return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: the ID token's signature does not verify", ErrUnverified))
	}

	now := a.now()
	if aud := claimString(claims, "aud"); aud != a.cfg.FirebaseProject {
		return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: the ID token was issued for another Firebase project", ErrUnverified))
	}
	if iss := claimString(claims, "iss"); iss != firebaseIssuer(a.cfg.FirebaseProject) {
		return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: the ID token was not issued by this installation's Firebase", ErrUnverified))
	}
	if exp, ok := claimInt(claims, "exp"); !ok || !now.Before(time.Unix(exp, 0)) {
		return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: the ID token has expired", ErrUnverified))
	}
	if iat, ok := claimInt(claims, "iat"); ok && time.Unix(iat, 0).After(now.Add(clockSkew)) {
		return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: the ID token is issued in the future", ErrUnverified))
	}

	// The uid, and the rule that makes this a DEVICE's call. Both claims that
	// carry it are required and must agree (verifyRun's argument, one family
	// over): reading whichever one is present would admit a token that names
	// one uid in sub and another — or nothing — in user_id.
	sub, uid := claimString(claims, "sub"), claimString(claims, "user_id")
	if sub == "" || uid == "" {
		return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: the ID token carries no uid (sub and user_id are both required)", ErrUnverified))
	}
	if sub != uid {
		return Identity{}, refuse(ReasonTokenUnverified, fmt.Errorf("%w: the ID token names two different uids", ErrUnverified))
	}
	if !IsDeviceUID(sub) {
		// The refusal names the family and not the value. A run's identity
		// lands here, which is the check the task exists for: `r~<slug>~<run>`
		// is not a device, and this service is not one a run may reach.
		return Identity{}, refuse(ReasonNotADevice, fmt.Errorf("%w: the uid is not a device's: a device's uid is %s and %d unpadded base64url characters (D165), and a run's (%s), a pairing session's (%s) and an account's own are all refused here", ErrUnverified, deviceUIDPrefix, deviceUIDChars, "r~<slug>~<run>", "p~…"))
	}
	if mu := claimString(claims, deviceClaim); mu != sub {
		// The database's own rule, checked here for its reason: the rules
		// admit a device by mu agreeing with auth.uid AND the node existing,
		// so a token missing this claim could not read anything even if this
		// service admitted it.
		return Identity{}, refuse(ReasonNotADevice, fmt.Errorf("%w: the ID token's %s claim does not name the uid it was issued for, so the database would not admit it either", ErrUnverified, deviceClaim))
	}
	return Identity{UID: sub}, nil
}

// IsDeviceUID reports whether uid is a device's: the `m~` family and the
// exact random-part length internal/budget/token draws. The length is checked
// and not only the prefix, because a uid of the wrong length is a name no
// minter of this tree produced, and the node lookup below is a database path
// built from this string.
func IsDeviceUID(uid string) bool {
	rest, ok := strings.CutPrefix(uid, deviceUIDPrefix)
	if !ok || len(rest) != deviceUIDChars {
		return false
	}
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// firebaseIssuer is the issuer a Firebase ID token of this project carries.
func firebaseIssuer(firebaseProject string) string {
	return "https://securetoken.google.com/" + firebaseProject
}

// decodeJWT splits a compact JWS and decodes its header and claims. It
// verifies nothing: the signature is verifySignature's, and a caller that read
// a claim out of here before that had read an unsigned document.
func decodeJWT(tok string) (head, claims map[string]any, err error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, nil, errors.New("not a JWS")
	}
	head, err = decodeSegment(parts[0])
	if err != nil {
		return nil, nil, err
	}
	claims, err = decodeSegment(parts[1])
	if err != nil {
		return nil, nil, err
	}
	return head, claims, nil
}

// decodeSegment decodes one base64url segment into a JSON object.
func decodeSegment(s string) (map[string]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// verifySignature checks the token's RS256 signature against key, over the
// exact bytes of the signing input.
func verifySignature(tok string, key *rsa.PublicKey) error {
	i := strings.LastIndex(tok, ".")
	if i < 0 {
		return errors.New("not a JWS")
	}
	sig, err := base64.RawURLEncoding.DecodeString(tok[i+1:])
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(tok[:i]))
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig)
}

// claimString reads a string claim, and "" for one that is absent or is not a
// string: every caller of it compares against a value it requires, so a
// non-string is refused by the comparison rather than by a branch of its own.
func claimString(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// claimInt reads a numeric claim. JSON numbers decode as float64, and a claim
// that is not one is absent rather than zero.
func claimInt(m map[string]any, k string) (int64, bool) {
	f, ok := m[k].(float64)
	if !ok {
		return 0, false
	}
	return int64(f), true
}
