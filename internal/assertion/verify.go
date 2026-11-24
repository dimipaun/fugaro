// Package assertion verifies the assertion the identity project signs for a
// project (P1, D2792): the compact EdDSA JWT that says which Google identity a
// phone signed in with, minted by A1 in Cloud KMS and read here.
//
// A verification is a chain of checks and every one of them is a refusal when it
// fails: the token is a compact JWS of three base64url segments and nothing
// else, its header carries the one algorithm this contract names, the key the
// header names is either pinned in this build or taken from a key set the
// pinned root key signed, the signature verifies over the bytes that arrived,
// the claims are exactly the eleven C1 fixes, the issuer is the pinned one, the
// audience and the challenge are the caller's expectation and not a value read
// off the assertion, the lifetime holds within a bounded skew, and the two
// things this verifier remembers are remembered in the service's store rather
// than in this process: the jti is spent once through C1's compare-and-set
// store, and the highest key-set sequence ever accepted is kept the same way, so
// two credsvc instances are one place a value is spent and one floor under a
// stale document (F39, O7).
//
// What this package is not: it holds no session, no roster and no policy. It
// turns an assertion into a claim set or into a typed refusal naming the field
// that failed, and the caller decides what an identity that verified may do
// (P3's policy, K2's restore). Every value a caller can be wrong about is its
// own parameter and never read from the document it judges.
package assertion

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const (
	// maxAssertionBytes bounds a token before a byte of it is decoded: an
	// assertion is a signature and eleven short claims (A1 signs it inside two
	// minutes of a sign-in), so anything at this size is a document a stranger
	// sent rather than one this contract describes.
	maxAssertionBytes = 8 << 10
	// ClockSkew is how far either side of an instant this verifier is willing to
	// be wrong about the time, in both directions: an assertion's exp is read
	// with it, its iat is read with it, and a jti is remembered for its life
	// plus it. It is one minute because the life it is added to is two
	// (AssertionLifetime): the skew has to be small beside the window it widens.
	ClockSkew = time.Minute
	// assertionAlg is the only algorithm an assertion may be signed with. The
	// identity project signs in Cloud KMS with an Ed25519 key, so a header
	// naming anything else is a document that is not this contract's -- and
	// "none" and the HMAC family are the two an attacker writes first.
	assertionAlg = "EdDSA"
	// assertionTyp is the media type of an assertion's header. It is checked
	// when present and not required, because A1's own header carries it (A2's
	// signer) and a token without one is still a token if everything else holds.
	assertionTyp = "JWT"
	// assertionSegments is what "compact" means: header, payload, signature.
	assertionSegments = 3
)

// ErrStore is a store this verifier needs failing. It is a sentinel and not a
// refusal, because it says nothing about the assertion: a claim store that is
// down is an assertion nobody can spend and a sequence store that is down is a
// key set nobody can hold to, and both are refusals -- the verifier fails closed
// -- but a caller that wants to answer 503 rather than 401 can tell the two
// apart with errors.Is.
var ErrStore = errors.New("assertion: a store the verifier needs failed")

// Refusal is a refused assertion: the field that failed and why, in words a
// person can act on. It never carries a value out of the assertion -- no claim,
// no segment, no key -- because a refusal reaches a log line and an answer body,
// and everything in an assertion is a document a stranger sent.
type Refusal struct {
	// Field is the name of the thing that failed, as the claim is spelled in
	// the assertion ("iss", "aud", "exp", "jti") or as this package calls the
	// part that is not a claim ("assertion", "header", "claims", "signature").
	Field string
	// Reason says what was wrong with it, without repeating it.
	Reason string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("assertion refused (%s): %s", r.Field, r.Reason)
}

// KeySetRefusal is a refused key set: the document the pinned URL served, or
// the envelope it arrived in, is not one this contract describes. It is separate
// from Refusal because it is a different thing that failed -- nothing about it
// is about the assertion's claims -- and a caller that logs one should know
// whether the assertion or the key set is the problem.
type KeySetRefusal struct {
	// Field is the part of the set that failed: "document", "alg", "typ",
	// "kid", "signature", "format", "seq", "exp" or "keys".
	Field string
	// Reason says what was wrong with it, without echoing a key, an id or a
	// document.
	Reason string
}

func (r *KeySetRefusal) Error() string {
	return fmt.Sprintf("key set refused (%s): %s", r.Field, r.Reason)
}

func refuse(field, format string, args ...any) error {
	return &Refusal{Field: field, Reason: fmt.Sprintf(format, args...)}
}

func refuseSet(field, format string, args ...any) error {
	return &KeySetRefusal{Field: field, Reason: fmt.Sprintf(format, args...)}
}

// SeqFloorStore keeps the highest key-set sequence this verifier has accepted,
// which is the persistent half of O7: "Each release pins the seq of its key set
// as a floor (P8), and the verifier keeps the highest seq seen in C1's
// compare-and-set store, not in process memory" -- so that a cache serving a
// stale document to the instance next door cannot walk a project back onto
// older keys.
//
// C1's ClaimStore cannot express this. Claim(namespace, key, ttl) answers one
// question -- has this value been spent -- and a sequence is a high-water mark
// rather than a value that is spent: a store built on Claim would refuse every
// set after the first. The interface therefore lives here, and this package
// ships both halves of it: NewSeqFloor, the durable one a service runs, and
// MemorySeqFloor, which is one process and is not.
type SeqFloorStore interface {
	// Observe records seq and reports whether it is at or above the highest
	// sequence this store has kept. False is a refusal and never an error, the
	// same shape Claim has: a document older than one already accepted is the
	// ordinary case the store exists for. An error is the store failing, and
	// the verifier fails closed on it.
	Observe(ctx context.Context, seq int64) (bool, error)
}

// MemorySeqFloor is a SeqFloorStore in this process's memory. It is enough for
// one instance and for tests, and it is *not* what the cluster runs: two
// instances of the public service are two high-water marks, and the sequence a
// build pins is then the only floor that holds across them. It is exported
// because P3 and K2 have to build a verifier before I1 builds the durable one,
// and it is named for what it is so that the wiring that replaces it is a
// search away.
func MemorySeqFloor() SeqFloorStore { return &memorySeqFloor{} }

type memorySeqFloor struct {
	mu    sync.Mutex
	high  int64
	fresh bool
}

func (m *memorySeqFloor) Observe(_ context.Context, seq int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fresh && seq < m.high {
		return false, nil
	}
	m.high, m.fresh = seq, true
	return true, nil
}

// CompareAndSet is the store a durable sequence floor is kept in: the two
// operations a high-water mark needs and nothing else. A mark is read and then
// raised, and the raise must not race the instance next door into lowering it,
// so the read answers with the tag the node carries and the write lands only
// while that tag still stands -- a lost race is the store's own precondition
// failure and this reader reads again. *rtdb.Client is the implementation a
// service passes: the same store C1's claim store sits on, so a project has one
// place its stores live.
type CompareAndSet interface {
	// GetETag reads the node at path into out, and answers the tag its value
	// carries now and whether the node was there at all.
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	// PutIfMatch writes v at path only while the tag still stands, and answers
	// rtdb.ErrPrecondition when another writer moved the node first.
	PutIfMatch(ctx context.Context, path, etag string, v any) error
}

// The client a service passes satisfies the interface above; it is pinned here
// so that a change to either side is a build failure rather than a wiring
// failure in a deployment that would then keep no floor at all.
var _ CompareAndSet = (*rtdb.Client)(nil)

// maxSeqFloorRaces bounds the read-raise loop below. A lost race is another
// instance raising the floor in the same instant, which is a handful of writers
// at worst; a store that loses that race for ever is a store whose answer this
// verifier does not have, and failing closed is the answer then, not looping.
const maxSeqFloorRaces = 8

// seqFloor is the durable SeqFloorStore: the highest sequence any instance has
// accepted, kept in a store the instances share, under one path.
type seqFloor struct {
	store CompareAndSet
	path  string
}

// NewSeqFloor is the sequence floor a service runs, and the one the acceptance
// names: the highest sequence is kept "in C1's compare-and-set store", so an
// instance that has already seen a key set cannot be walked back onto older
// keys by a cache serving it to the instance next door, and a restart is not a
// second chance for a set this project has moved past.
//
// path names the node holding one project's mark, so it is the project's own
// and not a shared one: two projects keep two floors. The store must answer for
// every verification -- a mark nobody can read is an error, never a zero -- and
// the verifier refuses the key set rather than accepting one nothing vouches
// for.
func NewSeqFloor(store CompareAndSet, path string) (SeqFloorStore, error) {
	if store == nil {
		return nil, errors.New("assertion: a sequence floor needs a compare-and-set store")
	}
	if err := checkSeqFloorPath(path); err != nil {
		return nil, err
	}
	return &seqFloor{store: store, path: path}, nil
}

// checkSeqFloorPath refuses the spellings of a path this package can see are
// wrong -- a node is named by segments joined by a single separator, with none
// leading, trailing or doubled -- and leaves the rest to the store, which
// refuses at the first read whatever it cannot write.
func checkSeqFloorPath(path string) error {
	switch {
	case path == "":
		return errors.New("assertion: a sequence floor needs the path of the node it keeps")
	case strings.HasPrefix(path, "/"), strings.HasSuffix(path, "/"), strings.Contains(path, "//"):
		return fmt.Errorf("assertion: %q is not a node path: segments joined by one separator, none leading, trailing or doubled", path)
	}
	return nil
}

func (f *seqFloor) Observe(ctx context.Context, seq int64) (bool, error) {
	if seq < 1 {
		return false, errors.New("assertion: a key set sequence is at least 1")
	}
	for race := 0; race < maxSeqFloorRaces; race++ {
		var held int64
		etag, found, err := f.store.GetETag(ctx, f.path, &held)
		if err != nil {
			return false, fmt.Errorf("%w: the key set's sequence could not be read: %v", ErrStore, err)
		}
		// A sequence no lower than the mark is kept, and the mark is a floor
		// rather than a lock: the set that *is* the highest one kept is the
		// document the project publishes now, so the instance next door and the
		// one that restarts both accept it. Only a set below the mark is a
		// stale document, and only it is refused.
		if found && held > seq {
			return false, nil
		}
		if found && held == seq {
			return true, nil
		}
		// The write only ever raises the mark: held was below seq whenever it
		// was found at all, and a node nothing has written yet is raised to seq
		// by the write that found it absent.
		switch err := f.store.PutIfMatch(ctx, f.path, etag, seq); {
		case err == nil:
			return true, nil
		case errors.Is(err, rtdb.ErrPrecondition):
			// Another instance raised the mark between this read and this
			// write; read again and answer from what is there now.
		default:
			return false, fmt.Errorf("%w: the key set's sequence could not be kept: %v", ErrStore, err)
		}
	}
	return false, fmt.Errorf("%w: the key set's sequence was raised by other instances faster than this one could keep it", ErrStore)
}

// The verifier's own settings, all of them fixed at construction. A verifier is
// built once per process from the build's pins and the service's stores, so
// nothing here is read per assertion and nothing is mutable afterwards.
type options struct {
	pins   Pins
	client *http.Client
	now    func() time.Time
	seq    SeqFloorStore
}

// Option is one setting of a verifier. There is no option for a key, an issuer
// or a store per call: the trust anchor is the build's (O7), and a caller that
// could pass a store could pass one that remembers nothing, which is why C1's
// Verifier takes the assertion and the expected binding and nothing else.
type Option func(*options)

// WithPins replaces the build's pins. Tests and P8 use it; a service does not.
func WithPins(p Pins) Option { return func(o *options) { o.pins = p } }

// WithHTTPClient is the client the key set is read with. It exists so that a
// test can serve the set from a function and so that a deployment can set a
// timeout; the URL read is the pinned one either way.
func WithHTTPClient(c *http.Client) Option { return func(o *options) { o.client = c } }

// WithClock is the clock this verifier reads instants with. Tests use it to
// stand on either side of an exp without sleeping.
func WithClock(now func() time.Time) Option { return func(o *options) { o.now = now } }

// WithSeqFloorStore is the store that keeps the highest key set sequence seen.
func WithSeqFloorStore(s SeqFloorStore) Option { return func(o *options) { o.seq = s } }

// Verifier verifies assertions (C1's Verifier). It holds the pins, the key set
// cache and the two stores, and it is safe for concurrent use: the cache is
// guarded by its own lock and everything else is read-only.
type Verifier struct {
	pins   Pins
	claims mobilesession.ClaimStore
	now    func() time.Time
	keys   *keySetCache
}

// The one line that holds this package to C1: the interface P3 and K2 call is
// the contract's, and a signature that drifted from it would otherwise fail in
// their code rather than in this file.
var _ mobilesession.Verifier = (*Verifier)(nil)

// New builds the verifier this service verifies assertions with. It refuses a
// claim store or a sequence store it does not have and a pin set it cannot use,
// once, here, rather than at every sign-in: a verifier that cannot claim a jti
// cannot make one single-use, and a verifier whose issuer is empty refuses every
// assertion with a message about the assertion.
func New(claims mobilesession.ClaimStore, opts ...Option) (*Verifier, error) {
	if claims == nil {
		return nil, errors.New("assertion: a verifier needs C1's claim store: single use is the store's, and a verifier without one would admit a replay the instance next door already saw (F39)")
	}
	o := options{pins: DefaultPins(), client: http.DefaultClient, now: time.Now}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if err := validatePins(o.pins); err != nil {
		return nil, fmt.Errorf("assertion: the build's pins cannot be used: %w", err)
	}
	if o.seq == nil {
		return nil, errors.New("assertion: a verifier needs a sequence-floor store: it is where the highest key set sequence seen is kept, and the plan's O7 keeps it out of process memory; NewSeqFloor is the durable one, and MemorySeqFloor is not it")
	}
	if o.client == nil || o.now == nil {
		return nil, errors.New("assertion: the verifier was built with no HTTP client or no clock")
	}
	return &Verifier{
		pins:   o.pins,
		claims: claims,
		now:    o.now,
		keys:   newKeySetCache(o.pins, o.client, o.now, o.seq),
	}, nil
}

// Pins answers the pins this verifier was built with, which is what a caller
// logs once at start-up to say which identity project it trusts.
func (v *Verifier) Pins() Pins { return v.pins }

// Verify checks one assertion against what the caller expects it to be bound to
// and answers the claim set it carried, or a typed refusal naming the field
// that failed.
//
// The order is the order of the dependency: the token is parsed, its algorithm
// and key id are read, the key is found (pinned, or from a set the pinned root
// signed), the signature is checked over the bytes that arrived, and only then
// are the claims read -- a claim set read before the signature verified is a
// claim set a stranger wrote. The jti is spent last, after everything else has
// held: an assertion that is going to be refused anyway must not spend its id,
// and an id spent before its signature verified would let a stranger burn
// assertions it cannot sign. The pinned key's own conditions come between the
// signature and the claims for the same reason (pinStands): they are remote, and
// a stranger's token is refused first.
func (v *Verifier) Verify(ctx context.Context, assertion string, want mobilesession.Binding) (mobilesession.Claims, error) {
	if want.Audience == "" {
		return mobilesession.Claims{}, refuse("aud", "the caller asked for no audience: an expectation read off the assertion it judges is no check at all")
	}
	if want.Challenge == "" {
		return mobilesession.Claims{}, refuse("challenge", "the caller asked for no challenge: without one an assertion bound to another pairing would verify here")
	}

	head, payload, sig, signing, err := parseAssertion(assertion)
	if err != nil {
		return mobilesession.Claims{}, err
	}
	if head.Alg != assertionAlg {
		return mobilesession.Claims{}, refuse("alg", "the assertion is not signed with %s", assertionAlg)
	}
	if head.Typ != "" && head.Typ != assertionTyp {
		return mobilesession.Claims{}, refuse("typ", "the assertion is not a JWT")
	}
	if head.Kid == "" {
		return mobilesession.Claims{}, refuse("kid", "the assertion's header names no signing key")
	}

	// The pinned key's own conditions are consulted after the signature and not
	// before it, which is the order the jti store is spent in and for the same
	// reason: a pinned key id is a string any caller can send, and both of the
	// things consulted for one are remote -- the compare-and-set store, and, once
	// a set above the one in hand has been kept, the pinned URL.
	pinned, isPinned := v.pinnedKey(head.Kid)
	if isPinned {
		if !ed25519.Verify(pinned.Key, []byte(signing), sig) {
			return mobilesession.Claims{}, refuse("signature", "the signature does not verify against the key this key id names")
		}
		if err := v.pinStands(ctx, pinned); err != nil {
			return mobilesession.Claims{}, err
		}
	} else {
		key, err := v.keys.key(ctx, head.Kid)
		if err != nil {
			return mobilesession.Claims{}, err
		}
		if !ed25519.Verify(key, []byte(signing), sig) {
			return mobilesession.Claims{}, refuse("signature", "the signature does not verify against the key this key id names")
		}
	}

	claims, err := readClaims(payload)
	if err != nil {
		return mobilesession.Claims{}, err
	}
	now := v.now()
	if claims.Issuer != v.pins.Issuer {
		return mobilesession.Claims{}, refuse("iss", "the assertion was not issued by the identity project this build pins")
	}
	if claims.Audience != want.Audience {
		return mobilesession.Claims{}, refuse("aud", "the assertion was signed for another audience")
	}
	if claims.Challenge != want.Challenge {
		return mobilesession.Claims{}, refuse("challenge", "the assertion was signed against another challenge")
	}
	if !now.Before(time.Unix(claims.ExpiresAt, 0).Add(ClockSkew)) {
		return mobilesession.Claims{}, refuse("exp", "the assertion expired more than %s ago", ClockSkew)
	}
	if time.Unix(claims.IssuedAt, 0).After(now.Add(ClockSkew)) {
		return mobilesession.Claims{}, refuse("iat", "the assertion was issued more than %s in the future", ClockSkew)
	}
	if claims.ExpiresAt <= claims.IssuedAt {
		return mobilesession.Claims{}, refuse("exp", "the assertion's expiry is not after its issue, so its life is not a life")
	}
	// The contract gives an assertion a life (AssertionLifetime), and a signer
	// that says its assertion lives longer is not minting this contract's
	// document. The bound is also what keeps the arithmetic that remembers the
	// jti inside the range it can hold: the jti's life is read as exp minus now,
	// and an exp far enough ahead for that subtraction to saturate would be a
	// jti forgotten long before the assertion stopped verifying.
	if time.Unix(claims.ExpiresAt, 0).Sub(time.Unix(claims.IssuedAt, 0)) > mobilesession.AssertionLifetime {
		return mobilesession.Claims{}, refuse("exp", "the assertion's life is longer than the %s this contract allows", mobilesession.AssertionLifetime)
	}
	if err := v.spend(ctx, claims, now); err != nil {
		return mobilesession.Claims{}, err
	}
	return claims, nil
}

// pinnedKey is the key this build pins for a key id, and whether it pins one at
// all. It reads nothing and asks nobody: it is the lookup that has to come
// before a signature, and everything that can refuse a pinned key (pinStands)
// comes after one.
func (v *Verifier) pinnedKey(kid string) (PinnedKey, bool) {
	for _, pinned := range v.pins.Keys {
		if pinned.KID == kid {
			return pinned, true
		}
	}
	return PinnedKey{}, false
}

// pinStands is what a key id this build pins still has to pass once the
// signature over it holds: the key set says the project has not rotated it out,
// and the build's own not-after date has not passed.
//
// The omitting set is consulted here, for a pinned kid too, because a pinned key
// ends when a valid set at or above the floor no longer holds it: a rotation is
// authority over the pin, and a build that kept verifying with a key the project
// had withdrawn would be a build that cannot be recovered from a key that
// leaked. Both checks are remote -- the store is C1's compare-and-set node, and
// a rotation this process has not read past makes it read the pinned URL -- so a
// token that does not carry the pin's signature is refused before either is
// reached, and a stranger cannot make this service pay a round trip by naming a
// pinned key id. A set is read here only when the store says one above the set
// in hand has been kept somewhere; a build that sees only pinned keys and has
// read past every rotation therefore reads nothing, which is O7's whole point.
func (v *Verifier) pinStands(ctx context.Context, pinned PinnedKey) error {
	ended, err := v.keys.endsPin(ctx, pinned.KID)
	if err != nil {
		return err
	}
	if ended {
		return refuse("kid", "a valid key set at or above this build's sequence floor no longer holds this key id")
	}
	if !v.now().Before(pinned.NotAfter) {
		return refuse("kid", "the key pinned for this key id is past the not-after date this build carries")
	}
	return nil
}

// The header of an assertion, and nothing else: a JWS header may carry a key URL
// ("jku", "x5u"), a certificate chain or a critical extension, and a verifier
// that read one would be a verifier an assertion could point at a key set of its
// own choosing. The decode below is strict, so a header carrying any of them is
// refused rather than read past.
type assertionHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// parseAssertion splits a compact JWS, decodes its three segments strictly and
// answers the header, the payload, the signature and the signing input -- the
// bytes the signature covers, which are the first two segments as they arrived
// and never a re-serialisation of them.
func parseAssertion(raw string) (assertionHeader, []byte, []byte, string, error) {
	var head assertionHeader
	if raw == "" {
		return head, nil, nil, "", refuse("assertion", "the assertion is empty")
	}
	if len(raw) > maxAssertionBytes {
		return head, nil, nil, "", refuse("assertion", "the assertion is %d bytes, past the %d an assertion can be", len(raw), maxAssertionBytes)
	}
	parts := strings.Split(raw, ".")
	if len(parts) != assertionSegments {
		return head, nil, nil, "", refuse("assertion", "a compact assertion has %d segments and this one has %d", assertionSegments, len(parts))
	}
	header, err := decodeSegment(parts[0], "assertion")
	if err != nil {
		return head, nil, nil, "", err
	}
	payload, err := decodeSegment(parts[1], "assertion")
	if err != nil {
		return head, nil, nil, "", err
	}
	sig, err := decodeSegment(parts[2], "signature")
	if err != nil {
		return head, nil, nil, "", err
	}
	dec := json.NewDecoder(bytes.NewReader(header))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&head); err != nil {
		return head, nil, nil, "", refuse("header", "the assertion's header is not one this verifier models")
	}
	return head, payload, sig, parts[0] + "." + parts[1], nil
}

// decodeSegment decodes one base64url segment of a compact JWS. The encoding is
// raw (unpadded) and strict, and the padding is refused here rather than left to
// the decoder: a padded or re-padded spelling is a different string, and a
// verifier that accepted two spellings of one signature would be a verifier two
// caches could disagree about.
func decodeSegment(segment, field string) ([]byte, error) {
	if strings.ContainsRune(segment, '=') {
		return nil, refuse(field, "a segment is padded, and this contract spells every segment unpadded base64url")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(segment)
	if err != nil {
		return nil, refuse(field, "a segment is not unpadded base64url")
	}
	return raw, nil
}

// claimNames is the closed set of claims this verifier reads, taken from
// mobilesession.Claims itself so that the contract and the verifier cannot drift
// apart: a claim C1 adds is one this verifier reads the moment it is declared,
// and until then it is a claim no signer may put in an assertion.
var claimNames = func() map[string]bool {
	names := make(map[string]bool)
	t := reflect.TypeFor[mobilesession.Claims]()
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			names[name] = true
		}
	}
	return names
}()

// readClaims reads the eleven claims C1 fixes out of a verified payload. It is
// the claims' own reader and not json.Unmarshal's convenience: every claim must
// be present and of the type it is declared with, because a claim read as its
// zero value is a claim nobody wrote -- an empty aud would otherwise equal an
// empty expectation and an email_verified of "false" the string would otherwise
// read as a bool.
func readClaims(payload []byte) (mobilesession.Claims, error) {
	var carried map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(payload))
	if err := dec.Decode(&carried); err != nil || carried == nil {
		return mobilesession.Claims{}, refuse("assertion", "the payload is not a JSON object of claims")
	}
	// One object and nothing after it: a second JSON value in the payload is a
	// document two readers could read differently.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return mobilesession.Claims{}, refuse("assertion", "the payload carries more than one JSON value")
	}
	for name := range carried {
		if !claimNames[name] {
			return mobilesession.Claims{}, refuse("claims", "the assertion carries a claim this verifier does not read")
		}
	}
	for name := range claimNames {
		if _, ok := carried[name]; !ok {
			return mobilesession.Claims{}, refuse(name, "the assertion carries no such claim")
		}
	}
	var claims mobilesession.Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		var wrongType *json.UnmarshalTypeError
		if errors.As(err, &wrongType) && wrongType.Field != "" {
			return mobilesession.Claims{}, refuse(wrongType.Field, "the claim is not of the type this verifier reads")
		}
		return mobilesession.Claims{}, refuse("assertion", "the claim set is not one this verifier reads")
	}
	for _, claim := range []struct{ name, value string }{
		{"iss", claims.Issuer},
		{"aud", claims.Audience},
		{"sub", claims.Subject},
		{"email", claims.Email},
		{"provider", claims.Provider},
		{"jti", claims.JTI},
		{"challenge", claims.Challenge},
	} {
		if claim.value == "" {
			return mobilesession.Claims{}, refuse(claim.name, "the claim is empty")
		}
	}
	return claims, nil
}
