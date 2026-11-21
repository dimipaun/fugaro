package token

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// The wrapped form of the seal key (R1, R5 of
// docs/batch-run-identity/reconciliation.md): what a launcher puts in a Cloud
// Batch job after encrypting the seal key to the installation's KMS public key,
// and what the credentials service opens with that key's private half.
//
// seal.go's seal carries the key the channel needs to one execution; this
// envelope carries that same key to one JOB, and the two are not one mechanism.
// The seal is symmetric and the key travels with the marker; here the launcher
// holds only a PUBLIC key, so a launcher cannot open what another launcher
// wrapped, and the private half never leaves Cloud KMS. Both are capabilities
// and not boundaries, in seal.go's own words: what this removes is the value of
// a blob a reader of the job can reach, and it removes no reach.
//
// The envelope is
//
//	version byte || RSA-OAEP(SHA-256, empty label) ciphertext
//
// and the plaintext, which never appears on the wire beside it, is
//
//	job digest (32) || wrap time (8, big-endian Unix seconds) || seal key (32)
//
// **The digest and the wrap time are INSIDE the ciphertext** and never beside
// it (R1's first guard). They are what the service holds the release to — the
// VM asking must be a VM of the job the digest names, and the job must have
// been created within a short bound of the wrap time — so a reader who could
// edit either one beside the blob could point one run's release at another
// job's VM. Encryption alone does not stop that: the public key is public, so
// anyone can wrap anything, which is why R1 says the encryption carries no
// authenticity and needs none.
//
// **The padding and the hash.** RSA-OAEP with SHA-256 for both the message
// digest and MGF1, and an empty label, which is the one algorithm Cloud KMS
// names RSA_DECRYPT_OAEP_2048_SHA256: the service's key is created with it
// (BR5) and Wrap is what a launcher produces for it, so the two names below are
// one fact written twice and not a choice either side makes.
//
// **The size bound.** A 2048-bit key hands back a 256-byte ciphertext and
// carries at most 190 bytes of OAEP-SHA-256 plaintext, so the envelope is 257
// bytes and this contract's 72-byte plaintext sits well inside it. Wrap refuses
// a key that is not 2048 bits rather than writing a blob of another size, and
// ParseWrapped refuses one of another size rather than handing KMS something
// that is not this envelope.
const (
	// BudgetTokenWrappedEnv is the ordinary job variable the wrapped blob
	// travels in (R5). FUGARO_BUDGET_TOKEN keeps its one meaning — the marker
	// whose value IS the seal key — and is never set on a Batch job, because a
	// Batch job prints its variables (F3): a job carrying both is malformed and
	// the run stops without reading either.
	BudgetTokenWrappedEnv = "FUGARO_BUDGET_TOKEN_WRAPPED"

	// WrappedEnvelopeVersion is the envelope's first byte, and it is first so
	// that a later format is refused rather than misread as this one,
	// seal.go's rule for its own envelope. There is one version byte and not
	// two: it covers the plaintext's layout as well as the ciphertext's, so a
	// later layout is a later version.
	WrappedEnvelopeVersion byte = 1

	// WrappedDigestSize is the job digest's length: SHA-256 of the job's FULL
	// resource name, so the digest names a job and not a run.
	WrappedDigestSize = sha256.Size

	// wrappedTimeSize is the wrap time: eight big-endian bytes of Unix
	// seconds. Seconds and not nanoseconds, because the bound the service holds
	// it to is minutes wide and a finer clock would only tempt the two sides to
	// compare digits that a launcher's clock cannot be trusted for.
	wrappedTimeSize = 8

	// WrappedPlaintextSize is the plaintext's fixed length — the digest, the
	// wrap time and the seal key, in that order. Fixed, so a plaintext of any
	// other length is refused rather than read for the fields it happens to
	// have.
	WrappedPlaintextSize = WrappedDigestSize + wrappedTimeSize + KeySize

	// WrappedCiphertextSize is the ciphertext's length: RSA-2048, which is the
	// key BR5 creates and the only size Wrap writes or ParseWrapped admits.
	WrappedCiphertextSize = 256

	// WrappedMaxPlaintext is the most OAEP-SHA-256 over a 2048-bit key can
	// carry: 256 - 2*32 - 2 bytes. WrappedPlaintextSize is checked against it
	// in the tests, and it is stated here because the bound belongs to the
	// envelope and not to whoever next adds a field.
	WrappedMaxPlaintext = WrappedCiphertextSize - 2*sha256.Size - 2

	// WrappedEnvelopeSize is one version byte and one ciphertext, which is the
	// blob's whole length.
	WrappedEnvelopeSize = 1 + WrappedCiphertextSize

	// WrappedRSAKeyBits is the modulus Wrap requires and BR5 creates: the
	// algorithm's name in Cloud KMS carries the size, so a key of another size
	// is another algorithm and another contract.
	WrappedRSAKeyBits = 2048
)

// ErrWrapped means a value is not a wrapped seal key of this contract: not one
// unpadded base64url string of one envelope, of this version, whose plaintext
// is this layout. Every refusal here is a refusal and never a repair — a
// re-spelling would be a second way to name one blob — and the run stops on it,
// as a malformed marker stops it.
var ErrWrapped = errors.New("budget token: not a wrapped seal key")

// wrappedEncoding is the one spelling of an envelope: unpadded base64url,
// strictly decoded, as seal.go's keyEncoding and device.go's
// deviceKeyEncoding are. Strict() is what makes it ONE spelling: the last
// character of a 257-byte value carries bits this encoding discards, so
// without it a blob would have several spellings the redactor never learned.
var wrappedEncoding = base64.RawURLEncoding.Strict()

// digestEncoding is the one spelling of a job digest: unpadded base64url,
// strictly decoded. It is the key a registration is written under on the run's
// budget record, and a Realtime Database key may not hold '.', '#', '$', '[',
// ']' or '/', none of which base64url spells.
var digestEncoding = base64.RawURLEncoding.Strict()

// JobDigest is the job digest: SHA-256 of the job's FULL resource name, the
// spelling the platform gives a job (projects/<p>/locations/<l>/jobs/<id>).
// Both sides of the channel compute it this way and neither is given the
// other's answer: the launcher wraps it, the service reads it out of the
// plaintext and compares it with the entry the launcher registered.
//
// A job's name and not a run's: the name is what the launcher holds before the
// job exists and what the link from a VM to its job answers with, while the id
// Google assigns at creation is D3108's second call and not this digest.
func JobDigest(jobName string) [WrappedDigestSize]byte { return sha256.Sum256([]byte(jobName)) }

// EncodeDigest spells a digest the one way a registration's key may hold it.
func EncodeDigest(digest [WrappedDigestSize]byte) string {
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

// ParseDigest reads a registration's key back as the digest it names. The parse
// is strict for wrappedEncoding's reason, and a digest of another length is
// refused: nothing may be looked up under a spelling this contract never wrote.
func ParseDigest(s string) ([WrappedDigestSize]byte, error) {
	var digest [WrappedDigestSize]byte
	refuse := fmt.Errorf("%w: a job digest is one unpadded base64url string of %d characters decoding to %d bytes", ErrWrapped, digestEncoding.EncodedLen(WrappedDigestSize), WrappedDigestSize)
	if len(s) != digestEncoding.EncodedLen(WrappedDigestSize) {
		return digest, refuse
	}
	raw, err := digestEncoding.DecodeString(s)
	if err != nil || len(raw) != WrappedDigestSize {
		return digest, refuse
	}
	copy(digest[:], raw)
	return digest, nil
}

// Wrapped is an opened blob: the job the launcher wrapped it for, when it was
// wrapped, and the seal key.
//
// No format verb prints the key. %v, %+v and %#v answer String and Format, as
// DeviceToken's and Delivery's do, because R7's rule is that the blob is
// treated as a secret in output and this struct is what a caller holds once it
// has stopped being opaque. The digest and the time ARE printed deliberately:
// they are the two facts the service records and audits, and a print that named
// neither would send an operator to the KMS key's logs to find out which job a
// release was for.
type Wrapped struct {
	// Digest is the job the blob was wrapped for, as JobDigest computes it.
	Digest [WrappedDigestSize]byte
	// WrappedAt is when the launcher wrapped it, to the second.
	WrappedAt time.Time
	// SealKey is the key the sealed object is opened with, KeySize bytes. It is
	// the one secret of this contract and it leaves through this field alone.
	SealKey []byte
}

func (w Wrapped) String() string {
	return "token.Wrapped{job digest: " + EncodeDigest(w.Digest) + ", wrapped at: " + w.WrappedAt.UTC().Format(time.RFC3339) + ", seal key redacted}"
}

// Format answers every verb with the redacted String, so %x and %q of a Wrapped
// are as safe as %v. seal.go's and keychain.go's rule: the material leaves
// through SealKey, which no printer reads.
func (w Wrapped) Format(state fmt.State, _ rune) { io.WriteString(state, w.String()) }

func (w Wrapped) GoString() string { return w.String() }

// Wrap encrypts the seal key to pub, together with the job digest and the wrap
// time, and returns the blob in its one wire spelling — the value a Batch job's
// BudgetTokenWrappedEnv variable carries.
//
// The two facts R1 requires inside the plaintext are arguments of this function
// and not fields a caller could set afterwards, which is the whole of how the
// contract keeps them inside: there is no way to build a blob with a digest
// beside it.
//
// A key that is not WrappedRSAKeyBits, a digest that is not
// WrappedDigestSize, a seal key that is not KeySize, and a public key that is
// not RSA are each refused. They are shapes and not policy: whether the key is
// the INSTALLATION's is R13's, read by doctor from the published block, and
// whether the job is one this launcher may name is the platform's.
func Wrap(pub *rsa.PublicKey, digest [WrappedDigestSize]byte, wrappedAt time.Time, sealKey []byte) (string, error) {
	if pub == nil || pub.N.BitLen() != WrappedRSAKeyBits {
		return "", fmt.Errorf("%w: the installation's key is the %d-bit asymmetric decryption key of this contract, and this is not one", ErrWrapped, WrappedRSAKeyBits)
	}
	if len(sealKey) != KeySize {
		return "", fmt.Errorf("%w: the seal key is %d bytes and a seal key is %d", ErrWrapped, len(sealKey), KeySize)
	}
	plain := make([]byte, 0, WrappedPlaintextSize)
	plain = append(plain, digest[:]...)
	plain = binary.BigEndian.AppendUint64(plain, uint64(wrappedAt.Unix()))
	plain = append(plain, sealKey...)
	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, plain, nil)
	if err != nil {
		return "", fmt.Errorf("budget token: wrapping the seal key failed: %w", err)
	}
	if len(ciphertext) != WrappedCiphertextSize {
		return "", fmt.Errorf("%w: the envelope came out %d bytes and one of this contract is %d", ErrWrapped, len(ciphertext), WrappedCiphertextSize)
	}
	return EncodeWrapped(ciphertext), nil
}

// EncodeWrapped spells an envelope the one way the wire has for it: the
// version byte, then the ciphertext, as one unpadded base64url string. Wrap
// calls it, and it is exported because a test and a diagnostic hold the pieces
// rather than the whole — the version byte is written here and by nothing else,
// so no caller can put another version's bytes behind this contract's byte.
func EncodeWrapped(ciphertext []byte) string {
	return base64.RawURLEncoding.EncodeToString(append([]byte{WrappedEnvelopeVersion}, ciphertext...))
}

// ParseWrapped reads a blob's one wire spelling and returns the ciphertext the
// installation's KMS key opens. The version byte is checked here and NOT
// returned: a reader that could hold it separately could pass it on with
// another version's bytes.
//
// Every failure is ErrWrapped: a value of the wrong length, a padded or
// non-canonical spelling, the standard alphabet, a second segment, and a
// version that is not this one. The bytes are not validated as an RSA
// ciphertext — that is KMS's answer to give, and this side must not claim to
// have given it.
func ParseWrapped(blob string) ([]byte, error) {
	refuse := fmt.Errorf("%w: it must be one unpadded base64url string of %d characters holding version %#x and one %d-byte ciphertext", ErrWrapped, wrappedEncoding.EncodedLen(WrappedEnvelopeSize), WrappedEnvelopeVersion, WrappedCiphertextSize)
	if len(blob) != wrappedEncoding.EncodedLen(WrappedEnvelopeSize) {
		return nil, refuse
	}
	raw, err := wrappedEncoding.DecodeString(blob)
	if err != nil || len(raw) != WrappedEnvelopeSize {
		return nil, refuse
	}
	if raw[0] != WrappedEnvelopeVersion {
		return nil, fmt.Errorf("%w: the envelope's first byte is %#x and this code knows version %#x, so it is a format from another release rather than this one", ErrWrapped, raw[0], WrappedEnvelopeVersion)
	}
	return raw[1:], nil
}

// ParseWrappedPlaintext reads what the installation's key handed back: the job
// digest, the wrap time and the seal key, in that order (R1). The service calls
// it with KMS's answer and nothing else — the plaintext never travels and is
// never stored. The runner does not call it: the release route hands a run the
// key, and the digest and the wrap time are the service's to hold a caller to.
//
// A plaintext of any other length is refused rather than read for the fields it
// has, so a shorter value cannot be read as a key taken off the front of
// another layout.
func ParseWrappedPlaintext(plain []byte) (Wrapped, error) {
	if len(plain) != WrappedPlaintextSize {
		return Wrapped{}, fmt.Errorf("%w: the plaintext is %d bytes and this contract's layout is %d — the job digest, the wrap time and the seal key", ErrWrapped, len(plain), WrappedPlaintextSize)
	}
	var w Wrapped
	copy(w.Digest[:], plain[:WrappedDigestSize])
	seconds := int64(binary.BigEndian.Uint64(plain[WrappedDigestSize : WrappedDigestSize+wrappedTimeSize]))
	w.WrappedAt = time.Unix(seconds, 0).UTC()
	w.SealKey = append([]byte(nil), plain[WrappedPlaintextSize-KeySize:]...)
	return w, nil
}
