package token

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// The seal is the second factor the token object gets
// (docs/design/budget-token-channel.md §7.1, D18): the launcher encrypts the
// custom token under a key it generates for one launch, so what a peer run of
// a shared job reads out of the bucket is ciphertext worth nothing without
// that key. The key never goes into the bucket: it travels in the marker the
// launcher already writes (FUGARO_BUDGET_TOKEN, D15) and is handed to exactly
// one execution.
//
// The envelope is version byte || 12-byte nonce || ciphertext || 16-byte tag,
// AES-256-GCM, and the additional authenticated data is the object's own name
// (ObjectKey) followed by the version byte (aad). The name is what binds a
// sealed blob to the object it was stored under, so a copy of one run's object
// cannot be opened as another run's token even if the key were the same; the
// per-launch key, not the AAD, is what makes a sideways copy fail in practice,
// because the two runs' keys differ. The version byte is bound as well as
// checked, so an envelope is openable only by code that knows which format it
// is holding. The version byte is first so that a later format is refused
// rather than misread as this one.
//
// This is a **capability, not a boundary**: it removes the value of what a
// peer can reach, not the reach. A reader that holds both the object and the
// key (an operator with the bucket grant and an audit-log view, or a reader a
// hostile run hands the bytes to) recovers the token — the seal has no third
// factor and claims no more than §7.1's own words.
const (
	// KeySize is the seal key's length in bytes. The key is generated at 32
	// bytes and carried as one unpadded base64url string (43 characters),
	// and it is a key and not a passphrase: there is no KDF anywhere in the
	// channel.
	KeySize = 32
	// EnvelopeVersion is the envelope's first byte. A sealed object starts
	// with it; a plaintext token object does not (a JWT starts with 'e').
	EnvelopeVersion byte = 1
	// nonceSize is AES-GCM's standard nonce.
	nonceSize = 12
	// tagSize is AES-GCM's tag, which Seal appends to the ciphertext and
	// Open is given back.
	tagSize = 16
	// minEnvelope is a version byte, a nonce and a tag: an envelope whose
	// plaintext is empty is still this long, so anything shorter is not one.
	minEnvelope = 1 + nonceSize + tagSize
)

// keyReader is NewKey's random source: crypto/rand, replaced only by tests.
var keyReader io.Reader = rand.Reader

// NewKey draws a fresh seal key from crypto/rand: KeySize bytes, one per
// launch. The launcher calls it for every mint, a --retry included, so no key
// is ever in front of two executions (D17), and the key is never derived from
// anything a peer could compute (the run's names, the token). A failed draw is
// an error and never a fallback to a plaintext object.
func NewKey() ([]byte, error) {
	// io.ReadFull, not a single Read: every one of the KeySize bytes is
	// drawn, or there is no key.
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(keyReader, key); err != nil {
		return nil, fmt.Errorf("budget token: generating the seal key failed: %w", err)
	}
	return key, nil
}

// EncodeKey renders key as the marker carries it: one unpadded base64url
// string. It is the form the runner registers with its redactor, because it
// is the form a print of the environment value would show.
func EncodeKey(key []byte) string { return base64.RawURLEncoding.EncodeToString(key) }

// keyEncoding is the one spelling of a key the channel has: unpadded base64url,
// strictly decoded. Strict() is what makes this "the one spelling" rather than
// "one of the spellings": 32 bytes is three whole base64 groups plus two
// characters, so the last character carries four significant bits and two that
// this encoding discards — three other characters decode to the same 32 bytes —
// and Strict() refuses them. Without it a marker's value would have three
// spellings the redactor never learned, and a run that printed the raw key
// would print one of the three the registration missed.
var keyEncoding = base64.RawURLEncoding.Strict()

// ParseKey reads a marker's value as a seal key. The parse is strict, because
// the value decides whether the run unseals anything at all: it must be
// exactly one unpadded base64url string of base64.RawURLEncoding.EncodedLen(KeySize)
// characters, in the canonical spelling of those bytes (see keyEncoding), and
// anything else — whitespace, '=' padding, a second segment, a non-canonical
// trailing character, a shorter or longer key — is refused. A refusal here
// stops the run (internal/runner's MalformedEnv), so it never falls back to
// reading the object as a plaintext token.
func ParseKey(s string) ([]byte, error) {
	if len(s) != keyEncoding.EncodedLen(KeySize) {
		return nil, fmt.Errorf("%w: it must be one unpadded base64url string of %d characters decoding to %d bytes", ErrSealKey, keyEncoding.EncodedLen(KeySize), KeySize)
	}
	key, err := keyEncoding.DecodeString(s)
	if err != nil || len(key) != KeySize {
		return nil, fmt.Errorf("%w: it must be one unpadded base64url string of %d characters decoding to %d bytes", ErrSealKey, keyEncoding.EncodedLen(KeySize), KeySize)
	}
	return key, nil
}

// Sealed reports whether data starts with the envelope this runner writes,
// which is what tells a sealed object from a plaintext one. It is a shape
// test and not a proof: what makes the bytes a token is Unseal's tag check.
func Sealed(data []byte) bool { return len(data) >= minEnvelope && data[0] == EnvelopeVersion }

// Seal returns plaintext in the sealed envelope, bound to name (the object's
// own key, ObjectKey) and the version byte as additional authenticated data
// (aad). A fresh nonce is drawn for every call: a retry re-seals under a new
// key and a new nonce (§7.1, D17).
func Seal(key []byte, name string, plaintext []byte) ([]byte, error) {
	g, err := sealAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("budget token: generating the seal's nonce failed: %w", err)
	}
	out := make([]byte, 0, minEnvelope+len(plaintext))
	out = append(out, EnvelopeVersion)
	out = append(out, nonce...)
	return g.Seal(out, nonce, plaintext, aad(name, EnvelopeVersion)), nil
}

// aad is the additional authenticated data of an envelope: the object's own
// name, a zero byte, and the envelope's version byte. The version byte is
// bound as well as the name even though Unseal checks it before it opens
// anything, because the check is a statement about *this* build's format and
// the binding is a statement about the bytes: a later version's envelope can
// only ever be opened by code that knows it is that version. The zero byte is
// the separator, and an object name cannot contain one (ObjectKey's segments
// are a slug, a run id and a suffix), so the two parts cannot be rearranged
// into another pair that hashes the same.
func aad(name string, version byte) []byte {
	out := make([]byte, 0, len(name)+2)
	out = append(out, name...)
	out = append(out, 0, version)
	return out
}

// Unseal opens envelope with key for the object stored under name, and
// refuses everything else. Its refusals are the ones §7.4's table lists, and
// every one of them is a refusal and never a fallback to the bytes:
//
//   - a key that is not KeySize bytes is ErrSealKey (the parse that produced
//     it is strict, so this is damage to the runner rather than to the job);
//   - something too short to be an envelope, a plaintext token object, or an
//     envelope from a newer format is ErrNotSealed: a plaintext object is
//     refused once the launcher seals, and a mixed deployment is not an
//     unsealing one;
//   - a tag that does not verify is ErrUnseal, and it covers three cases with
//     one check, exactly as GCM does: the object was tampered with, it was
//     sealed for another launch's key, or it was sealed for another name. The
//     error says all three rather than guessing, and the caller's refusal is
//     the same for each.
func Unseal(key []byte, name string, envelope []byte) ([]byte, error) {
	g, err := sealAEAD(key)
	if err != nil {
		return nil, err
	}
	switch {
	case len(envelope) < minEnvelope:
		return nil, fmt.Errorf("%w: the object is %d bytes and a sealed envelope is at least %d", ErrNotSealed, len(envelope), minEnvelope)
	case envelope[0] != EnvelopeVersion:
		return nil, fmt.Errorf("%w: the object's first byte is %#x and this runner reads version %#x, so this is not a sealed token: a plaintext object is refused once the launcher seals, and an envelope from a newer format is refused rather than misread", ErrNotSealed, envelope[0], EnvelopeVersion)
	}
	plain, err := g.Open(nil, envelope[1:1+nonceSize], envelope[1+nonceSize:], aad(name, envelope[0]))
	if err != nil {
		return nil, fmt.Errorf("%w: the tag did not verify, so the object is not a token sealed for this execution's key under %s: it was tampered with, sealed for another launch, or sealed for another object", ErrUnseal, name)
	}
	return plain, nil
}

// sealAEAD is the cipher both halves use, so the key's length is checked once
// and the two cannot disagree about the algorithm. The check is what keeps the
// key at AES-256: without it aes.NewCipher would happily take 16 or 24 bytes
// and Sealed/Unsealed would be AES-128 or AES-192 under a key the channel
// never generated.
func sealAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: the key is %d bytes and a seal key is %d", ErrSealKey, len(key), KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSealKey, err)
	}
	return cipher.NewGCM(block)
}
