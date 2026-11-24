package assertion

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/mobilesession"
)

const (
	// KeySetMaxStale is how long past its own validity a held key set keeps
	// answering assertions while the pinned URL cannot be read. It is the bound
	// the plan asks for and it is deliberately not unbounded: while the identity
	// project's key endpoint is down, a project must not stop pairing -- that
	// would make one outage two -- but a set whose validity is long gone is a
	// set nothing has confirmed, and the keys in it may have been withdrawn
	// since. A day covers an outage that somebody will hear about the same day,
	// and it is short enough that a forgotten key cannot outlive the release
	// that pinned it by much.
	KeySetMaxStale = 24 * time.Hour
	// keySetMinReadInterval bounds how often the pinned URL is read. Two reads
	// happen for a key id the held set does not hold -- the ordinary case when
	// the identity project has just rotated (A2 publishes the new key before it
	// signs with it) -- so an assertion naming random key ids cannot make this
	// service read the endpoint once per request.
	keySetMinReadInterval = time.Minute
	// maxKeySetBytes bounds the document before it is parsed. It is generous
	// beside a set of a few Ed25519 keys and small beside anything worth
	// fetching.
	maxKeySetBytes = 64 << 10
)

// heldSet is a key set that has been accepted: the signing keys it holds, and
// the instant its own validity ends. Nothing about the envelope is kept, because
// nothing else about it is read again.
type heldSet struct {
	exp  time.Time
	keys map[string]ed25519.PublicKey
}

// keySetCache is the verifier's key set: the document a signed key set was
// fetched from, the one it holds, and the rules for reading another. It is the
// whole of what this package knows about the network, and everything it does is
// in service of O7's sentence -- a pinned key verifies with no fetch, any other
// key comes from a set the pinned root key signed.
//
// It is bounded three ways and each bound is load-bearing:
//
//   - a held set answers until its own exp (180 days, O18), so an ordinary
//     assertion costs no round trip and the project does not depend on the
//     identity project's endpoint being up to pair a phone;
//   - past that exp, the held set keeps answering for KeySetMaxStale while
//     reads fail, so an outage of that endpoint is a delay rather than an
//     outage here;
//   - past the bound, or with nothing ever held, an assertion is refused, and
//     the refusal names the key set rather than the assertion, because the
//     assertion may be perfectly good and the document unreadable.
//
// Reads are rate-limited to one a minute, and the rate limit applies to the
// extra read an unknown key id causes as well: without it, a request naming a
// random kid would be a request that makes this service read a URL.
type keySetCache struct {
	pins   Pins
	client *http.Client
	now    func() time.Time
	seq    SeqFloorStore

	mu sync.Mutex
	// held is the last accepted set, and it is replaced and never amended: a
	// set that fails any check leaves this one exactly as it was.
	held *heldSet
	// err is why the last read failed, kept so that a rate-limited call can
	// answer with the reason rather than with silence.
	err error
	// lastRead is when the URL was last read for a set, whether or not it
	// answered.
	lastRead time.Time
	// rotated is when it was last read because an assertion named a key id the
	// held set does not hold. The two are gated separately and the second
	// starts zero, so the read that picks a rotation up is never the one the
	// first read's interval refuses: a project whose identity project rotated a
	// second ago must not refuse every assertion until the minute is up.
	rotated time.Time
}

func newKeySetCache(pins Pins, client *http.Client, now func() time.Time, seq SeqFloorStore) *keySetCache {
	return &keySetCache{pins: pins, client: client, now: now, seq: seq}
}

// omitsPinned reports whether the held set is a valid one -- at or above the
// build's floor, root-signed, inside its validity when it was accepted -- that
// does not hold the key id. It is how a pinned key ends before its not-after
// date: the project rotated it out, and the rotation is authority over the pin.
//
// The answer does not lapse with the held set's validity. A set that was valid
// when it was read said what the project published, and that a key is no longer
// published is not something an outage un-says. It is also the safe direction:
// the set cannot be *un*-seen, and the alternative -- trusting a withdrawn key
// again because the endpoint went down -- is exactly what a rotation is for.
func (c *keySetCache) omitsPinned(kid string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held == nil {
		return false
	}
	_, held := c.held.keys[kid]
	return !held
}

// key is a signing key from the set, read and verified if it is not held yet.
// An id the held set does not hold costs one more read, because that is the
// signal the project rotated; the read is rate-limited, so the second assertion
// naming a key nobody publishes in a minute is refused without one.
func (c *keySetCache) key(ctx context.Context, kid string) (ed25519.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	set, err := c.setLocked(ctx)
	if err != nil {
		return nil, err
	}
	if key, held := set.keys[kid]; held {
		return key, nil
	}
	c.readLocked(ctx, true)
	if c.held != nil {
		if key, held := c.held.keys[kid]; held {
			return key, nil
		}
	}
	return nil, refuse("kid", "no key set this verifier can read holds the key id this assertion names")
}

// setLocked is the set to verify against: the held one while it is inside its
// own validity, and otherwise what a read gives, with the held one answering
// while a failed read is inside its bound. It is called with c.mu held.
func (c *keySetCache) setLocked(ctx context.Context) (*heldSet, error) {
	now := c.now()
	if c.held != nil && now.Before(c.held.exp) {
		return c.held, nil
	}
	c.readLocked(ctx, false)
	if c.held != nil && now.Before(c.held.exp.Add(KeySetMaxStale)) {
		return c.held, nil
	}
	if c.err != nil {
		return nil, c.err
	}
	return nil, refuseSet("document", "no key set has been read")
}

// readLocked reads and verifies the set, at most once per keySetMinReadInterval
// per reason for reading. A read that fails changes nothing but c.err: the held
// set stays, because a document that could not be read is not evidence about the
// one that was. It is called with c.mu held.
func (c *keySetCache) readLocked(ctx context.Context, rotation bool) {
	now := c.now()
	gate := &c.lastRead
	if rotation {
		gate = &c.rotated
	}
	if !gate.IsZero() && now.Sub(*gate) < keySetMinReadInterval {
		return
	}
	*gate, c.lastRead = now, now
	set, err := c.fetch(ctx)
	if err != nil {
		c.err = err
		return
	}
	c.held, c.err = set, nil
}

// fetch reads the pinned URL once and verifies what it finds. The error it
// returns is always one of this package's refusals or ErrStore: a caller reads
// them, and an error from net/http carries a URL and a transport's text, which
// is not what a refusal reaches a log line with.
func (c *keySetCache) fetch(ctx context.Context) (*heldSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.pins.JWKSURL, nil)
	if err != nil {
		return nil, refuseSet("document", "the pinned key set URL cannot be requested")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, refuseSet("document", "the pinned key set could not be read")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, refuseSet("document", "the pinned key set URL answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxKeySetBytes+1))
	if err != nil {
		return nil, refuseSet("document", "the pinned key set could not be read")
	}
	if len(body) > maxKeySetBytes {
		return nil, refuseSet("document", "the pinned key set is larger than the %d bytes this verifier reads", maxKeySetBytes)
	}
	return parseKeySet(ctx, body, c.pins, c.now(), c.seq)
}

// parseKeySet verifies one served document: the envelope the pinned root key
// signed over the identity project's JWKS (O7, A4), and the keys inside it. The
// envelope is the whole of what makes a fetched set trustworthy -- without it,
// whoever can change what the URL serves can publish keys of their own and sign
// assertions for any project (F38) -- so every check here is a refusal and none
// of them is a warning.
//
// The sequence is checked twice and for two different reasons: against the floor
// this build pins (P8), which is a fact about the release, and against the
// highest the store has kept, which is a fact about what this service has
// already seen -- and it is the store's answer, not this process's, so that the
// instance next door cannot be walked back onto older keys by a cache.
func parseKeySet(ctx context.Context, body []byte, pins Pins, now time.Time, seq SeqFloorStore) (*heldSet, error) {
	parts := strings.Split(string(body), ".")
	if len(parts) != assertionSegments {
		return nil, refuseSet("document", "the key set is not a signed document of %d segments", assertionSegments)
	}
	if strings.ContainsRune(parts[0], '=') || strings.ContainsRune(parts[1], '=') {
		return nil, refuseSet("document", "a segment of the key set is padded, and this contract spells every segment unpadded base64url")
	}
	if strings.ContainsRune(parts[2], '=') {
		return nil, refuseSet("signature", "the key set's signature is padded, and this contract spells every segment unpadded base64url")
	}
	header, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return nil, refuseSet("document", "a segment of the key set is not unpadded base64url")
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return nil, refuseSet("document", "a segment of the key set is not unpadded base64url")
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil {
		return nil, refuseSet("signature", "the key set's signature is not unpadded base64url")
	}

	var head mobilesession.KeySetHeader
	dec := json.NewDecoder(bytes.NewReader(header))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&head); err != nil {
		return nil, refuseSet("document", "the key set's header is not one this contract models")
	}
	if head.Alg != mobilesession.KeySetAlg {
		return nil, refuseSet("alg", "the key set is signed with an algorithm this contract does not name")
	}
	if head.Typ != mobilesession.KeySetTyp {
		return nil, refuseSet("typ", "the key set's header is not a key set's")
	}
	if head.Kid != pins.RootKeyID {
		return nil, refuseSet("kid", "the key set was signed by a root key this build does not pin")
	}
	if !ed25519.Verify(pins.RootKey, []byte(parts[0]+"."+parts[1]), signature) {
		return nil, refuseSet("signature", "the root key this build pins did not sign this key set")
	}

	var set mobilesession.KeySet
	dec = json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&set); err != nil {
		return nil, refuseSet("document", "the key set's payload is not one this contract models")
	}
	if set.Format != mobilesession.KeySetFormat {
		return nil, refuseSet("format", "the payload is not a key set of this contract's format")
	}
	if set.Seq < pins.SeqFloor {
		return nil, refuseSet("seq", "the key set's sequence is below the floor this build pins")
	}
	exp := time.Unix(set.Exp, 0)
	if !now.Before(exp.Add(ClockSkew)) {
		return nil, refuseSet("exp", "the key set's own validity has passed")
	}
	seen, err := seq.Observe(ctx, set.Seq)
	if err != nil {
		return nil, fmt.Errorf("%w: the key set's sequence could not be kept: %v", ErrStore, err)
	}
	if !seen {
		return nil, refuseSet("seq", "a key set at or above this one's sequence has already been read")
	}
	if len(set.JWKS.Keys) == 0 {
		return nil, refuseSet("keys", "the key set holds no signing key")
	}
	keys := make(map[string]ed25519.PublicKey, len(set.JWKS.Keys))
	for _, key := range set.JWKS.Keys {
		if key.Kid == "" {
			return nil, refuseSet("keys", "a key in the set carries no key id")
		}
		if _, twice := keys[key.Kid]; twice {
			return nil, refuseSet("keys", "two keys in the set share a key id, so which one signs is not a question with one answer")
		}
		if key.Kty != "OKP" || key.Crv != "Ed25519" {
			return nil, refuseSet("keys", "a key in the set is not an Ed25519 key")
		}
		if key.Alg != "" && key.Alg != mobilesession.KeySetAlg {
			return nil, refuseSet("keys", "a key in the set names an algorithm this contract does not")
		}
		if key.Use != "" && key.Use != "sig" {
			return nil, refuseSet("keys", "a key in the set is not for signatures")
		}
		if strings.ContainsRune(key.X, '=') {
			return nil, refuseSet("keys", "a key in the set is spelled with padding, and this contract spells every key unpadded base64url")
		}
		raw, err := base64.RawURLEncoding.Strict().DecodeString(key.X)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, refuseSet("keys", "a key in the set is not an Ed25519 public key of %d bytes", ed25519.PublicKeySize)
		}
		keys[key.Kid] = ed25519.PublicKey(raw)
	}
	return &heldSet{exp: exp, keys: keys}, nil
}
