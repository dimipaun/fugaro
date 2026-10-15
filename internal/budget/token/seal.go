package token

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
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
// AES-256-GCM, and the object's own name (ObjectKey) is the additional
// authenticated data. The AAD is what binds a sealed blob to the name it was
// stored under, so a copy of one run's object cannot be opened as another
// run's token even if the key were the same; the per-launch key, not the AAD,
// is what makes a sideways copy fail in practice, because the two runs' keys
// differ. The version byte is first so that a later format is refused rather
// than misread as this one.
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

// EncodeKey renders key as the marker carries it: one unpadded base64url
// string. It is the form the runner registers with its redactor, because it
// is the form a print of the environment value would show.
func EncodeKey(key []byte) string { return base64.RawURLEncoding.EncodeToString(key) }

// ParseKey reads a marker's value as a seal key. The parse is strict, because
// the value decides whether the run unseals anything at all: it must be
// exactly one unpadded base64url string of base64.RawURLEncoding.EncodedLen(KeySize)
// characters decoding to KeySize bytes, and anything else — whitespace, '=' padding, a second segment, a
// shorter or longer key — is refused. A refusal here stops the run
// (internal/runner's MalformedEnv), so it never falls back to reading the
// object as a plaintext token.
func ParseKey(s string) ([]byte, error) {
	if len(s) != base64.RawURLEncoding.EncodedLen(KeySize) {
		return nil, fmt.Errorf("%w: it must be one unpadded base64url string of %d characters decoding to %d bytes", ErrSealKey, base64.RawURLEncoding.EncodedLen(KeySize), KeySize)
	}
	key, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(key) != KeySize {
		return nil, fmt.Errorf("%w: it must be one unpadded base64url string of %d characters decoding to %d bytes", ErrSealKey, base64.RawURLEncoding.EncodedLen(KeySize), KeySize)
	}
	return key, nil
}

// Sealed reports whether data starts with the envelope this runner writes,
// which is what tells a sealed object from a plaintext one. It is a shape
// test and not a proof: what makes the bytes a token is Unseal's tag check.
func Sealed(data []byte) bool { return len(data) >= minEnvelope && data[0] == EnvelopeVersion }

// Seal returns plaintext in the sealed envelope, bound to name (the object's
// own key, ObjectKey) as additional authenticated data. A fresh nonce is
// drawn for every call: a retry re-seals under a new key and a new nonce
// (§7.1, D17).
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
	return g.Seal(out, nonce, plaintext, []byte(name)), nil
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
	plain, err := g.Open(nil, envelope[1:1+nonceSize], envelope[1+nonceSize:], []byte(name))
	if err != nil {
		return nil, fmt.Errorf("%w: the tag did not verify, so the object is not a token sealed for this execution's key under %s: it was tampered with, sealed for another launch, or sealed for another object", ErrUnseal, name)
	}
	return plain, nil
}

// sealAEAD is the cipher both halves use, so the key's length is checked once
// and the two cannot disagree about the algorithm.
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
