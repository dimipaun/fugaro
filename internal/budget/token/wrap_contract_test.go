package token_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// wrappedJobName is a job's full resource name in the one shape the launcher
// wraps the digest of: the project, the location and the job id a run's job
// carries on Cloud Batch.
const wrappedJobName = "projects/acme-dev/locations/us-central1/jobs/fugaro-acme-web-20261120-120000-abcd"

// wrappedClock is a wrap time and not a plausible one: the digest and this
// instant are the two fields R1 puts INSIDE the plaintext, so the test reads
// them back out of the ciphertext rather than off the wire beside it.
var wrappedClock = time.Unix(1_763_000_000, 0).UTC()

// wrappedKey is a seal key of the one length the channel has, in bytes a wrong
// parse cannot invent.
func wrappedKey() []byte { return bytes.Repeat([]byte{0x5a}, token.KeySize) }

// TestWrappedEnvelopeRoundTrips is R1's and R5's shape proof: the launcher's
// Wrap produces the one envelope spelling this installation's job variable
// carries, and the whole of it — the digest, the wrap time and the seal key —
// comes back out of the plaintext, which is where R1 requires those two facts
// to sit and never beside the blob.
//
// The decryption is crypto/rsa's own OAEP-SHA-256, which is the algorithm the
// service's KMS key is created with: a wrap this test cannot open through it is
// a wrap Cloud KMS would refuse, so the round trip covers the padding and the
// hash the contract pins and not only its base64.
func TestWrappedEnvelopeRoundTrips(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, token.WrappedRSAKeyBits)
	if err != nil {
		t.Fatalf("generating the installation's key: %v", err)
	}
	digest := token.JobDigest(wrappedJobName)
	key := wrappedKey()

	blob, err := token.Wrap(&priv.PublicKey, digest, wrappedClock, key)
	if err != nil {
		t.Fatalf("token.Wrap: %v", err)
	}
	// The wire form is one unpadded base64url string of the envelope exactly:
	// no padding, no second segment, and the version byte first.
	if strings.ContainsAny(blob, "=+/") {
		t.Fatalf("the blob is %q, which is not one unpadded base64url string", blob)
	}
	if want := base64.RawURLEncoding.EncodedLen(token.WrappedEnvelopeSize); len(blob) != want {
		t.Fatalf("the blob is %d characters and an envelope of this contract is %d", len(blob), want)
	}

	raw, err := base64.RawURLEncoding.DecodeString(blob)
	if err != nil {
		t.Fatalf("decoding the blob the package wrote: %v", err)
	}
	if raw[0] != token.WrappedEnvelopeVersion {
		t.Fatalf("the envelope's first byte is %#x and this contract's version is %#x", raw[0], token.WrappedEnvelopeVersion)
	}

	ciphertext, err := token.ParseWrapped(blob)
	if err != nil {
		t.Fatalf("token.ParseWrapped: %v", err)
	}
	if len(ciphertext) != token.WrappedCiphertextSize {
		t.Fatalf("the ciphertext is %d bytes and RSA-2048 hands back %d", len(ciphertext), token.WrappedCiphertextSize)
	}
	plain, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, priv, ciphertext, nil)
	if err != nil {
		t.Fatalf("the service's own decryption refused the blob: %v", err)
	}
	if len(plain) != token.WrappedPlaintextSize {
		t.Fatalf("the plaintext is %d bytes and this contract's layout is %d", len(plain), token.WrappedPlaintextSize)
	}
	if token.WrappedPlaintextSize > token.WrappedMaxPlaintext {
		t.Fatalf("the plaintext is %d bytes and OAEP-SHA-256 over a 2048-bit key carries at most %d", token.WrappedPlaintextSize, token.WrappedMaxPlaintext)
	}

	got, err := token.ParseWrappedPlaintext(plain)
	if err != nil {
		t.Fatalf("token.ParseWrappedPlaintext: %v", err)
	}
	if got.Digest != digest {
		t.Fatalf("the digest came back %x and the launcher wrapped %x", got.Digest, digest)
	}
	if !got.WrappedAt.Equal(wrappedClock) {
		t.Fatalf("the wrap time came back %v and the launcher wrapped %v", got.WrappedAt, wrappedClock)
	}
	if !bytes.Equal(got.SealKey, key) {
		t.Fatalf("the seal key came back %x and the launcher wrapped %x", got.SealKey, key)
	}

	// The digest's spelling is the registration's key, so it round-trips too:
	// one spelling, strictly decoded, as every other value of this package's.
	spelled := token.EncodeDigest(digest)
	back, err := token.ParseDigest(spelled)
	if err != nil {
		t.Fatalf("token.ParseDigest(%q): %v", spelled, err)
	}
	if back != digest {
		t.Fatalf("the digest came back %x from its own spelling and was %x", back, digest)
	}
	if digestAgain := token.JobDigest(wrappedJobName); digestAgain != digest {
		t.Fatalf("the digest of one job name is not one value: %x then %x", digest, digestAgain)
	}
	if same := token.JobDigest(wrappedJobName + "-other"); same == digest {
		t.Fatal("two job names have one digest, so the digest names no job")
	}
	// The key never prints: every verb of an opened blob answers the redaction
	// (R7), which is the package's own rule for the material it holds.
	for _, format := range []string{"%v", "%+v", "%#v", "%x", "%s"} {
		printed := fmt.Sprintf(format, got)
		if strings.Contains(printed, base64.RawURLEncoding.EncodeToString(key)) || strings.Contains(printed, string(key)) {
			t.Fatalf("%s of an opened blob printed the seal key: %s", format, printed)
		}
	}
}

// TestWrapRefusesAKeyOrASealKeyOfAnotherSize: the two shapes Wrap's own guard
// names are refusals HERE and not a hope that something downstream says so. A
// key that is not the installation's 2048-bit asymmetric decryption key is one
// the service's KMS could not open the blob with, and a seal key of another
// length is an envelope of another size: both would be found out at release,
// after the VM is up and the launch is paid for, so both stop the wrap.
func TestWrapRefusesAKeyOrASealKeyOfAnotherSize(t *testing.T) {
	digest := token.JobDigest(wrappedJobName)
	key := wrappedKey()
	priv, err := rsa.GenerateKey(rand.Reader, token.WrappedRSAKeyBits)
	if err != nil {
		t.Fatalf("generating the installation's key: %v", err)
	}
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generating a smaller key: %v", err)
	}
	large, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatalf("generating a larger key: %v", err)
	}

	for _, tc := range []struct {
		name string
		pub  *rsa.PublicKey
	}{
		{"no key at all", nil},
		{"a 1024-bit key", &small.PublicKey},
		{"a 3072-bit key", &large.PublicKey},
	} {
		if _, err := token.Wrap(tc.pub, digest, wrappedClock, key); !errors.Is(err, token.ErrWrapped) {
			t.Errorf("%s was wrapped to: %v", tc.name, err)
		}
	}
	for _, tc := range []struct {
		name string
		seal []byte
	}{
		{"no seal key", nil},
		{"a seal key one byte short", key[:token.KeySize-1]},
		{"a seal key one byte long", append(append([]byte(nil), key...), 0x00)},
	} {
		if _, err := token.Wrap(&priv.PublicKey, digest, wrappedClock, tc.seal); !errors.Is(err, token.ErrWrapped) {
			t.Errorf("%s was wrapped: %v", tc.name, err)
		}
	}
}

// TestWrappedEnvelopeRefusesAForeignVersion is the envelope's first byte read
// as what it is: a version. A blob of a later format is refused rather than
// misread as this one, which is seal.go's own rule for its own envelope — and
// a bad spelling is refused too, so the run stops at a shape it can name rather
// than at a KMS call that fails with something else.
func TestWrappedEnvelopeRefusesAForeignVersion(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, token.WrappedRSAKeyBits)
	if err != nil {
		t.Fatalf("generating the installation's key: %v", err)
	}
	blob, err := token.Wrap(&priv.PublicKey, token.JobDigest(wrappedJobName), wrappedClock, wrappedKey())
	if err != nil {
		t.Fatalf("token.Wrap: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(blob)
	if err != nil {
		t.Fatalf("decoding the blob the package wrote: %v", err)
	}

	foreign := append([]byte(nil), raw...)
	foreign[0] = token.WrappedEnvelopeVersion + 1
	if _, err := token.ParseWrapped(base64.RawURLEncoding.EncodeToString(foreign)); !errors.Is(err, token.ErrWrapped) {
		t.Fatalf("a blob of a later version was not refused as one: %v", err)
	}

	for _, bad := range []struct {
		name string
		blob string
	}{
		{"a plaintext marker", token.EncodeKey(wrappedKey())},
		{"a padded spelling", blob + "="},
		{"the standard alphabet", strings.NewReplacer("_", "/", "-", "+").Replace(blob)},
		{"a truncated blob", blob[:len(blob)-2]},
		{"empty", ""},
		{"the plaintext itself", wrappedJobName},
	} {
		if _, err := token.ParseWrapped(bad.blob); err == nil {
			t.Fatalf("%s was accepted as a wrapped seal key", bad.name)
		}
	}

	// The version byte is inside the ciphertext's own length, so a plaintext of
	// the wrong length is refused rather than read for the fields it has: a
	// short one would be a key read off the front of one it does not hold.
	if _, err := token.ParseWrappedPlaintext(make([]byte, token.WrappedPlaintextSize-1)); !errors.Is(err, token.ErrWrapped) {
		t.Fatalf("a plaintext one byte short was not refused as one: %v", err)
	}
}
