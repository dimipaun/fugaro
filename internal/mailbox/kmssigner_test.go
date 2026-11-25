package mailbox

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The consumer's signing key in Cloud KMS (mobile-identity-pairing J6, O12 and
// O17): the signer, over a key generated in this process so that no key ring is
// reached and no private key material exists in any file here, and the published
// contract the two Go sides of the key agree on.
//
// What the plan's verification names, and what each test below proves: the
// signature is over the value's own canonical bytes in the signer's direction,
// and it verifies under the public half of the key the key ring signed with
// (TestKMSSignerSignsAValueThePhoneVerifies); the direction is the signer's and
// a value signed for one node does not verify in the other
// (TestKMSSignerSignsOneNodeAndNotTheOther); an answer that is not a 64-byte
// signature is a per-write refusal and not a value written as signed
// (TestKMSSignerRefusesWhatIsNotASignature); a value the canonical form refuses
// is refused before anything is signed (TestKMSSignerRefusesAValueNothingCanonicalises);
// and a signer is built only with a client, a key version and a node
// (TestKMSSignerNeedsAClientAVersionAndANode).
//
// The published half is tested as the contract it is: one spelling
// (TestConsumerKeySpellingIsOneSpelling), one fingerprint over the decoded bytes
// (TestConsumerKeyFingerprintPinsTheBytes), the node's own shape and version
// (TestConsumerKeyNodeIsThePublishedShape), and the key's resource name as the
// Terraform module composes it (TestConsumerKeyNamesAreTheDeployments — the same
// two literals are read out of the module itself by internal/infra's
// TestMailboxKeyNamesAreTheGoSpellings, which is where Go can see the `.tf`).

// fakeKeyRing is one asymmetric key version's signing call over an Ed25519 key
// this process generated: the seam's whole surface, with no service, no
// credential and no network. `answer` is the failure and damage injection — a
// truncated signature, a call that fails — because a key ring that answers
// something that is not a signature is the case the signer's own check exists
// for, and no real key ring produces it on request.
type fakeKeyRing struct {
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
	answer func(payload []byte) ([]byte, error)

	versions []string
	payloads [][]byte
}

func newFakeKeyRing(t *testing.T) *fakeKeyRing {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating the test's Ed25519 key: %v", err)
	}
	return &fakeKeyRing{pub: pub, priv: priv}
}

func (f *fakeKeyRing) SignAsymmetric(_ context.Context, keyVersion string, payload []byte) ([]byte, error) {
	f.versions = append(f.versions, keyVersion)
	f.payloads = append(f.payloads, payload)
	if f.answer != nil {
		return f.answer(payload)
	}
	return ed25519.Sign(f.priv, payload), nil
}

// kmsSignerFor builds the signer one test needs, or fails the test: every
// constructor refusal has its own test below, so a helper that swallowed one
// would hide the refusal it is there to test.
func kmsSignerFor(t *testing.T, ring AsymmetricSigner, version, direction string) *KMSSigner {
	t.Helper()
	signer, err := NewKMSSigner(ring, version, direction)
	if err != nil {
		t.Fatalf("building the %s signer: %v", direction, err)
	}
	return signer
}

// consumerValue is a reply in the shape the outbox writes: a record with a
// string, a number, a boolean, a list and a nested object, so that what is
// signed is a whole canonical serialisation and not one field of it. `mac` and
// `sig` are absent — the signature is added last, and what it covers is the
// value before either is there (§8.2.1).
func consumerValue() Record {
	return Record{
		"at":      int64(1764000000000),
		"id":      "reply-7",
		"text":    "two done, one running",
		"done":    true,
		"agents":  []any{"alpha", "beta"},
		"session": map[string]any{"id": "s-1", "rigor": "careful"},
	}
}

func TestKMSSignerSignsAValueThePhoneVerifies(t *testing.T) {
	ring := newFakeKeyRing(t)
	signer := kmsSignerFor(t, ring, ConsumerCryptoKeyVersionPath("proj-1234", "us-central1"), directionOutbox)

	value := consumerValue()
	sig, err := signer.Signature(context.Background(), value)
	if err != nil {
		t.Fatalf("signing a reply: %v", err)
	}
	// The spelling: unpadded base64url, and exactly the 64 bytes an Ed25519
	// signature is (J5r's fixed length in the rules).
	raw, err := base64.RawURLEncoding.Strict().DecodeString(sig)
	if err != nil {
		t.Fatalf("the signature %q is not unpadded base64url: %v", sig, err)
	}
	if len(raw) != ConsumerSignatureSize {
		t.Fatalf("the signature is %d bytes, and an Ed25519 signature is %d", len(raw), ConsumerSignatureSize)
	}
	if strings.ContainsAny(sig, "+/=") {
		t.Errorf("the signature %q carries a character of the padded or standard alphabet, so the phone's verifier would read other bytes", sig)
	}
	// What was signed is the value's canonical bytes in this signer's
	// direction, and nothing else: the phone rebuilds exactly these bytes from
	// the node it read, so a signature over another serialisation — or over
	// another direction — would verify nowhere.
	payload, err := CanonicalBytes(value, directionOutbox)
	if err != nil {
		t.Fatalf("the value's canonical bytes: %v", err)
	}
	if !ed25519.Verify(ring.pub, payload, raw) {
		t.Fatal("the signature does not verify under the public half of the key the key ring signed with: what the phone pins would accept nothing this signer wrote")
	}
	// The key version travels to the call as the deployment names it: an
	// asymmetric key has no primary version, so a signer that named another
	// version would be signing under a key nobody published.
	if len(ring.versions) != 1 || ring.versions[0] != ConsumerCryptoKeyVersionPath("proj-1234", "us-central1") {
		t.Fatalf("the signing call named %v, want the one key version the signer holds", ring.versions)
	}
	if signer.Direction() != directionOutbox {
		t.Errorf("Direction() = %q, want %q", signer.Direction(), directionOutbox)
	}
	if signer.KeyVersion() == "" || strings.Contains(signer.String(), sig) {
		t.Errorf("String() = %q, and a log line about the signer names the key version and never a signature", signer.String())
	}
}

func TestKMSSignerSignsOneNodeAndNotTheOther(t *testing.T) {
	ring := newFakeKeyRing(t)
	value := consumerValue()
	payload, err := CanonicalBytes(value, directionOutbox)
	if err != nil {
		t.Fatalf("the value's canonical bytes: %v", err)
	}
	// The direction is in the signed bytes (§8.1), so the same value and the
	// same key produce two signatures, and neither verifies in the other node:
	// a reply replayed as a heartbeat is a value the phone rejects, which is
	// the whole of why the direction is the signer's and not the caller's.
	outbox := kmsSignerFor(t, ring, ConsumerKeyVersionName, directionOutbox)
	heartbeat := kmsSignerFor(t, ring, ConsumerKeyVersionName, directionHeartbeat)
	outSig, err := outbox.Signature(context.Background(), value)
	if err != nil {
		t.Fatalf("signing for the outbox: %v", err)
	}
	hbSig, err := heartbeat.Signature(context.Background(), value)
	if err != nil {
		t.Fatalf("signing for the heartbeat: %v", err)
	}
	if outSig == hbSig {
		t.Fatal("the two nodes signed one value with one signature: the direction is not in the signed bytes, so a reply would verify as a heartbeat")
	}
	out, _ := base64.RawURLEncoding.Strict().DecodeString(outSig)
	hb, _ := base64.RawURLEncoding.Strict().DecodeString(hbSig)
	if !ed25519.Verify(ring.pub, payload, out) {
		t.Error("the outbox signature does not verify over the outbox's canonical bytes")
	}
	hbPayload, err := CanonicalBytes(value, directionHeartbeat)
	if err != nil {
		t.Fatalf("the heartbeat's canonical bytes: %v", err)
	}
	if !ed25519.Verify(ring.pub, hbPayload, hb) {
		t.Error("the heartbeat signature does not verify over the heartbeat's canonical bytes")
	}
	if ed25519.Verify(ring.pub, hbPayload, out) {
		t.Error("the outbox signature verifies in the heartbeat direction, so a reply replayed as a heartbeat would be accepted")
	}
	if hbPayload == nil || string(hbPayload) == string(payload) {
		t.Error("the two directions serialise one value to the same bytes, so the direction is not covered")
	}
}

func TestKMSSignerRefusesWhatIsNotASignature(t *testing.T) {
	// A key ring's answer that is not a 64-byte signature is a value this
	// consumer must not write as signed: the phone would reject it with no
	// reason, and the ledger would record a reply that never verified. The
	// refusal is ErrUnsignable's — M4's per-write row, nothing written for this
	// value and the reply owed again on the next pass — and it keeps the key
	// ring's own reason as the inner error.
	for _, tc := range []struct {
		name   string
		answer func([]byte) ([]byte, error)
	}{
		{"a truncated signature", func([]byte) ([]byte, error) { return []byte{1, 2, 3}, nil }},
		{"an empty signature", func([]byte) ([]byte, error) { return nil, nil }},
		{"an over-long signature", func([]byte) ([]byte, error) { return make([]byte, ConsumerSignatureSize+1), nil }},
		{"a signing call that failed", func([]byte) ([]byte, error) { return nil, errors.New("the key ring is unreachable") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ring := newFakeKeyRing(t)
			ring.answer = tc.answer
			signer := kmsSignerFor(t, ring, ConsumerKeyVersionName, directionOutbox)
			sig, err := signer.Signature(context.Background(), consumerValue())
			if !errors.Is(err, ErrUnsignable) {
				t.Fatalf("signing answered (%q, %v), want ErrUnsignable: nothing is written for a value that could not be signed", sig, err)
			}
			if sig != "" {
				t.Errorf("the refused signature is %q, and a refusal writes nothing at all", sig)
			}
		})
	}
}

func TestKMSSignerRefusesAValueNothingCanonicalises(t *testing.T) {
	ring := newFakeKeyRing(t)
	signer := kmsSignerFor(t, ring, ConsumerKeyVersionName, directionOutbox)
	// A value the canonical form refuses is an error and not a signature: the
	// bytes a reader would compute are not the bytes this side would have
	// signed (§3.5), and no signing call is made for it.
	for _, tc := range []struct {
		name  string
		value Record
	}{
		{"a number that is not a safe integer", Record{"at": 1.5}},
		{"a null", Record{"at": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sig, err := signer.Signature(context.Background(), tc.value)
			if !errors.Is(err, ErrUnsignable) || sig != "" {
				t.Fatalf("signing answered (%q, %v), want ErrUnsignable and no signature", sig, err)
			}
		})
	}
	if len(ring.payloads) != 0 {
		t.Fatalf("the key ring was called %d time(s) for values nothing canonicalises, and a value no reader can serialise is one this side never signs", len(ring.payloads))
	}
}

func TestKMSSignerNeedsAClientAVersionAndANode(t *testing.T) {
	ring := newFakeKeyRing(t)
	if _, err := NewKMSSigner(nil, ConsumerKeyVersionName, directionOutbox); err == nil {
		t.Error("a signer with no client was built, and it could sign nothing")
	}
	if _, err := NewKMSSigner(ring, "   ", directionOutbox); err == nil {
		t.Error("a signer naming no key version was built, and an asymmetric key has no primary version for the call to fall back to")
	}
	for _, direction := range []string{"", directionInbox, "outbox ", "Outbox"} {
		if _, err := NewKMSSigner(ring, ConsumerKeyVersionName, direction); err == nil {
			t.Errorf("a signer for %q was built, and the consumer writes the %s and the %s node and nothing else", direction, directionOutbox, directionHeartbeat)
		}
	}
	// One deep case for the package's own refusal edges: a payload that is
	// empty or over the bound is refused before a key ring is reached, and the
	// bound is what keeps a record this package should never have built from
	// being read as a key that failed.
	signer := kmsSignerFor(t, ring, ConsumerKeyVersionName, directionOutbox)
	if _, err := signer.sign(context.Background(), nil); err == nil {
		t.Error("an empty payload was signed, and a signature over no bytes is one every empty payload shares")
	}
	big := make([]byte, maxSignBytes+1)
	if _, err := signer.sign(context.Background(), big); err == nil {
		t.Errorf("a %d-byte payload was signed, over the %d a signing call reads", len(big), maxSignBytes)
	}
	if len(ring.payloads) != 0 {
		t.Fatalf("the key ring was called %d time(s) with a payload the signer refuses", len(ring.payloads))
	}
	var nilSigner *KMSSigner
	if sig, err := nilSigner.Signature(context.Background(), consumerValue()); !errors.Is(err, ErrUnsignable) || sig != "" {
		t.Errorf("a nil signer answered (%q, %v), want ErrUnsignable", sig, err)
	}
}

func TestConsumerKeySpellingIsOneSpelling(t *testing.T) {
	key := make([]byte, ConsumerPublicKeySize)
	for i := range key {
		key[i] = byte(i*7 + 3)
	}
	// The top bit is set, and that is the point: unlike the phone's X25519
	// device key, an Ed25519 public key has no discarded bit, so the spelling
	// here must not import token.DecodeDeviceKey's rule.
	key[ConsumerPublicKeySize-1] |= 0x80
	spelled := EncodeConsumerPublicKey(key)
	if len(spelled) != 43 {
		t.Fatalf("the spelling is %d characters, and 32 bytes of unpadded base64url are 43", len(spelled))
	}
	back, err := DecodeConsumerPublicKey(spelled)
	if err != nil {
		t.Fatalf("decoding %q: %v", spelled, err)
	}
	if string(back) != string(key) {
		t.Fatalf("the round trip changed the key: %x, want %x", back, key)
	}
	// Everything that is not that one spelling is refused rather than repaired:
	// a repaired key is a key whose fingerprint the phone computes over other
	// bytes, and the pin would then accept nothing.
	for _, bad := range []string{
		base64.URLEncoding.EncodeToString(key),
		base64.StdEncoding.EncodeToString(key),
		spelled + "=",
		spelled + "A",
		spelled[:42],
		"+" + spelled[1:],
		"",
		"not-a-key",
	} {
		if _, err := DecodeConsumerPublicKey(bad); err == nil {
			t.Errorf("DecodeConsumerPublicKey(%q) accepted a spelling that is not the one spelling of a consumer public key", bad)
		}
	}
	// The trailing character's discarded bits are what Strict() refuses: the
	// last character of a 32-byte key carries four significant bits and two the
	// encoding discards, so the sixteen characters whose low two bits are clear
	// are canonical (each of them spells another key, which is not this test's
	// business) and the forty-eight whose discarded bits are set are refused
	// rather than folded back into these same 32 bytes.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	refused := 0
	for i := 0; i < len(alphabet); i++ {
		candidate := spelled[:len(spelled)-1] + string(alphabet[i])
		_, err := DecodeConsumerPublicKey(candidate)
		if i%4 != 0 {
			if err == nil {
				t.Errorf("DecodeConsumerPublicKey(%q) folded a non-canonical trailing character back into the key's own bytes, so one key has more than one spelling", candidate)
				continue
			}
			refused++
			continue
		}
		if err != nil {
			t.Errorf("DecodeConsumerPublicKey(%q) refused a canonical trailing character: %v", candidate, err)
		}
	}
	if refused != 48 {
		t.Errorf("%d of the 48 non-canonical trailing characters were refused, and every one of them decodes to these same 32 bytes", refused)
	}
}

func TestConsumerKeyFingerprintPinsTheBytes(t *testing.T) {
	key := make([]byte, ConsumerPublicKeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	// The fingerprint is over the decoded bytes and never over a spelling (O16,
	// pair_invite.go's pairConsumerKeyFingerprint): the two sides of a pairing
	// compute it from different spellings of one key, and only a hash of the
	// bytes makes them agree. It is a full SHA-256 in unpadded base64url, not
	// token.Fingerprint's eight human-typed characters — those are for a person
	// to compare and these have to make a forged key infeasible.
	sum := ConsumerKeyFingerprint(key)
	if len(sum) != 43 {
		t.Fatalf("the fingerprint is %q (%d characters), and a SHA-256 in unpadded base64url is 43", sum, len(sum))
	}
	if strings.ContainsAny(sum, "+/=") {
		t.Errorf("the fingerprint %q is not unpadded base64url", sum)
	}
	if other := ConsumerKeyFingerprint(append([]byte{}, key...)); other != sum {
		t.Errorf("the same key fingerprinted twice answered %q and %q", sum, other)
	}
	changed := append([]byte{}, key...)
	changed[0] ^= 0x01
	if ConsumerKeyFingerprint(changed) == sum {
		t.Error("two different keys share a fingerprint, so a forged key would pass the pin")
	}
}

func TestConsumerKeyNodeIsThePublishedShape(t *testing.T) {
	key := make([]byte, ConsumerPublicKeySize)
	for i := range key {
		key[i] = byte(i)
	}
	node := ConsumerKeyNode{V: ConsumerKeyNodeVersion, PublicKey: EncodeConsumerPublicKey(key)}
	want := `{"v":1,"public_key":"` + EncodeConsumerPublicKey(key) + `"}`
	encoded, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshalling the published node: %v", err)
	}
	if string(encoded) != want {
		t.Fatalf("the published node is %s, want %s: the two fields are the node's whole shape, and a reader pins the version", encoded, want)
	}
	if ConsumerKeyNodeVersion != 1 || ConsumerKeyNodePath != "config/admin/mailbox_key" {
		t.Fatalf("the node is published at %s v%d, and internal/cli's reader (pairConsumerKeyPath, pairConsumerKeyVersion) pins the same two; TestConsumerKeyNodeIsThePairReader there fails when they drift", ConsumerKeyNodePath, ConsumerKeyNodeVersion)
	}
}

func TestConsumerKeyNamesAreTheDeployments(t *testing.T) {
	// The module composes the key's resource name out of the installation's own
	// project and region around two literals, and the grant's crypto_key_id in
	// the plan is that composed name (mailbox_key.tf, mailboxSignerGrant in
	// internal/infra/tf/cover.go). The literals are held to the module's by
	// internal/infra's TestMailboxKeyNamesAreTheGoSpellings, which reads the
	// `.tf` files; this pins the composition both sides use.
	want := "projects/proj-1234/locations/europe-west1/keyRings/fugaro-mailbox/cryptoKeys/fugaro-mailbox-consumer"
	if got := ConsumerCryptoKeyPath("proj-1234", "europe-west1"); got != want {
		t.Fatalf("ConsumerCryptoKeyPath = %q, want %q", got, want)
	}
	if got, want := ConsumerCryptoKeyVersionPath("proj-1234", "europe-west1"), want+"/cryptoKeyVersions/1"; got != want {
		t.Fatalf("ConsumerCryptoKeyVersionPath = %q, want %q", got, want)
	}
	// A signer built on the version path the deployment reads the public half
	// under is the signer whose signatures the phone verifies: one key version,
	// named in one place.
	if ConsumerKeyAlgorithm != "EC_SIGN_ED25519" {
		t.Fatalf("the algorithm is %q, and the module creates an EC_SIGN_ED25519 key", ConsumerKeyAlgorithm)
	}
}
