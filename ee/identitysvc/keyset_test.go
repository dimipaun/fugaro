// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// The root key and the signed key set (mobile identity plan task A4). What these
// tests hold is what no owner-run check can state afterwards: the envelope is the
// one C1's vector fixes, byte for byte, the signature is over exactly those bytes
// and verifies with the root key's public half, a set whose `seq` is not above
// the last signed one is refused before the root key is asked for a signature at
// all, and nothing here — no document, no printed value — carries private
// material.
//
// Nothing here holds a key. The key ring is faked with a key generated inside the
// test's own process and its private half never leaves it, the way
// ee/keyservice/kmssigner_test.go and ee/identitysvc/cmd/identitysvc do
// (.claude/rules/ee.md R5). The vector's own root private key was discarded when
// it was drawn, so the vector is replayed rather than re-signed: what is compared
// is the signing input, and the vector's signature is verified against the root
// public key it carries — which is the reading that says both halves agree.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/mobilesession"
)

// testRootKID is the root key's own id, the one the owner's ceremony gives the
// key and Check 737 reads. It is a public name and not a secret.
const testRootKID = "root-2026-11"

// testRootVersion is a root key version's resource name, shaped like the one the
// root key project's apply creates.
const testRootVersion = "projects/fugaro-root/locations/europe-west1/keyRings/root/cryptoKeys/keyset/cryptoVersions/1"

// fakeRootRing is Cloud KMS as the root key signer sees it: one key version, its
// public half it answers as a PEM document, and the integrity fields the service
// fills in on the way back. Every fault below is one way an answer can arrive
// damaged or be about another key, and each is refused rather than assembled into
// a document every client would fetch and refuse.
type fakeRootRing struct {
	name   string
	key    ed25519.PrivateKey
	public ed25519.PublicKey

	failSign     error
	failPublic   error
	skipVerified bool
	skipSign     bool
	corruptSig   bool
	signOther    bool
	// pem replaces the public half's document: a damaged PEM, a PEM of another
	// key type, and a document that is not a PEM at all.
	pem           string
	algorithm     kmspb.CryptoKeyVersion_CryptoKeyVersionAlgorithm
	corruptPEMCRC bool

	signCalls   int
	publicCalls int
}

func (f *fakeRootRing) AsymmetricSign(_ context.Context, req *kmspb.AsymmetricSignRequest, _ ...gax.CallOption) (*kmspb.AsymmetricSignResponse, error) {
	f.signCalls++
	if f.failSign != nil {
		return nil, f.failSign
	}
	if req.GetDataCrc32C() == nil || req.GetDataCrc32C().Value != int64(crc32.Checksum(req.GetData(), rootKeyCRC)) {
		return nil, errors.New("the request carried no CRC32C of the bytes it asked to sign")
	}
	if req.GetDigest() != nil {
		return nil, errors.New("the request handed the ring a digest, and an Ed25519 key signs the payload itself")
	}
	answer := &kmspb.AsymmetricSignResponse{
		Name:               f.name,
		VerifiedDataCrc32C: !f.skipVerified,
	}
	if f.signOther {
		answer.Name = strings.TrimSuffix(f.name, "/cryptoVersions/1") + "/cryptoVersions/2"
	}
	if f.skipSign {
		return answer, nil
	}
	signature := ed25519.Sign(f.key, req.GetData())
	answer.SignatureCrc32C = wrapperspb.Int64(int64(crc32.Checksum(signature, rootKeyCRC)))
	if f.corruptSig {
		signature[0] ^= 0xff
	}
	answer.Signature = signature
	return answer, nil
}

func (f *fakeRootRing) GetPublicKey(_ context.Context, req *kmspb.GetPublicKeyRequest, _ ...gax.CallOption) (*kmspb.PublicKey, error) {
	f.publicCalls++
	if f.failPublic != nil {
		return nil, f.failPublic
	}
	if req.GetName() != f.name {
		return nil, errors.New("the call named a key version the ring does not hold")
	}
	algorithm := f.algorithm
	if algorithm == kmspb.CryptoKeyVersion_CRYPTO_KEY_VERSION_ALGORITHM_UNSPECIFIED {
		algorithm = kmspb.CryptoKeyVersion_EC_SIGN_ED25519
	}
	document := f.pem
	if document == "" {
		encoded, err := x509.MarshalPKIXPublicKey(f.public)
		if err != nil {
			return nil, err
		}
		document = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded}))
	}
	answer := &kmspb.PublicKey{
		Name:      f.name,
		Algorithm: algorithm,
		Pem:       document,
	}
	// What the service counts is the bytes it answered, so a fault that damages
	// the document without touching the sum beside it is the shape a truncated
	// answer has.
	counted := int64(crc32.Checksum([]byte(document), rootKeyCRC))
	if f.corruptPEMCRC {
		counted++
	}
	answer.PemCrc32C = wrapperspb.Int64(counted)
	return answer, nil
}

// rootRingFixture builds one root key ring over a key this test holds the public
// half of, and the signer the command signs through.
func rootRingFixture(t *testing.T) (*fakeRootRing, *RootKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating the root key: %v", err)
	}
	ring := &fakeRootRing{name: testRootVersion, key: private, public: public}
	root, err := NewRootKey(ring, testRootVersion)
	if err != nil {
		t.Fatalf("building the root key signer: %v", err)
	}
	return ring, root
}

// signingKeyJWKS is one published signing key, generated here: an
// identity-service key as C1's contract holds it.
func signingKeyJWKS(t *testing.T, kid string) mobilesession.JWKS {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a signing key: %v", err)
	}
	return mobilesession.JWKS{Keys: []mobilesession.JWK{{
		Kty: "OKP",
		Crv: "Ed25519",
		X:   base64.RawURLEncoding.EncodeToString(public),
		Kid: kid,
		Alg: mobilesession.KeySetAlg,
		Use: "sig",
	}}}
}

// signingInput splits a document into the bytes the root key signed and the
// signature beside them.
func signingInput(t *testing.T, document []byte) (string, []byte) {
	t.Helper()
	segments := strings.Split(string(document), ".")
	if len(segments) != 3 {
		t.Fatalf("the signed key set has %d segment(s), and a JWS compact serialization has three", len(segments))
	}
	signature, err := base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		t.Fatalf("the signature is not base64url: %v", err)
	}
	return segments[0] + "." + segments[1], signature
}

// keySetPayload decodes the payload a document carries.
func keySetPayload(t *testing.T, document []byte) mobilesession.KeySet {
	t.Helper()
	input, _ := signingInput(t, document)
	encoded := strings.SplitN(input, ".", 2)[1]
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("the payload is not base64url: %v", err)
	}
	var payload mobilesession.KeySet
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("the payload is not a key set: %v", err)
	}
	if payload.Format != mobilesession.KeySetFormat {
		t.Fatalf("the payload's format is %q, want %q", payload.Format, mobilesession.KeySetFormat)
	}
	return payload
}

// TestKeysetMatchesC1sVector is the task's interface: the envelope A4 builds is
// the one C1's vectors fix, in the bytes and not in the shape.
//
// C1's vector ships the signing input as its two base64url halves and a real
// signature over them, made with a root private key that was drawn for the
// vector alone and discarded — so the two halves this task builds for the same
// inputs are compared with the vector's, and the vector's own signature is
// verified against the root public key it carries. Both readings are needed: the
// first says the bytes agree, and the second says the bytes are the ones the
// vector's signature is over, which is what a change to the field order, an
// added space or a reordered key would break.
func TestKeysetMatchesC1sVector(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean("../../shared/mobile-session/vectors.json"))
	if err != nil {
		t.Fatalf("reading the contract's vectors: %v", err)
	}
	var vectors struct {
		KeySet struct {
			Header struct {
				Alg string `json:"alg"`
				Kid string `json:"kid"`
				Typ string `json:"typ"`
			} `json:"header"`
			Payload struct {
				Format string             `json:"format"`
				Seq    int64              `json:"seq"`
				Iat    int64              `json:"iat"`
				Exp    int64              `json:"exp"`
				JWKS   mobilesession.JWKS `json:"jwks"`
			} `json:"payload"`
			SigningInputParts []string          `json:"signing_input_parts"`
			Signature         string            `json:"signature"`
			RootPublicKey     mobilesession.JWK `json:"root_public_key"`
		} `json:"key_set"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("decoding the contract's vectors: %v", err)
	}
	set := vectors.KeySet
	if len(set.SigningInputParts) != 2 || set.Signature == "" {
		t.Fatal("the vectors carry no signed key set, and the envelope this task writes is the one they fix")
	}
	if set.Header.Alg != mobilesession.KeySetAlg || set.Header.Typ != mobilesession.KeySetTyp {
		t.Fatalf("the vector's header carries %s/%s, and the contract fixes %s/%s", set.Header.Alg, set.Header.Typ, mobilesession.KeySetAlg, mobilesession.KeySetTyp)
	}

	// The vector is a real signature over its own signing input: this is what
	// makes the byte comparison below a statement about the contract rather than
	// about two documents that happen to agree.
	input := []string{set.SigningInputParts[0], set.SigningInputParts[1]}
	signingInputBytes := strings.Join(input, ".")
	rootPublic, err := base64.RawURLEncoding.DecodeString(set.RootPublicKey.X)
	if err != nil {
		t.Fatalf("the vector's root public key is not base64url: %v", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(set.Signature)
	if err != nil {
		t.Fatalf("the vector's signature is not base64url: %v", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(rootPublic), []byte(signingInputBytes), signature) {
		t.Fatal("the vector's own signature does not verify with the root public key it carries, so the file is not a signed key set")
	}

	_, root := rootRingFixture(t)
	document, rootJWK, err := SignKeySet(context.Background(), root, KeySetRequest{
		JWKS:    set.Payload.JWKS,
		Seq:     set.Payload.Seq,
		RootKID: set.Header.Kid,
	}, time.Unix(set.Payload.Iat, 0).UTC())
	if err != nil {
		t.Fatalf("signing the vector's set: %v", err)
	}
	got, _ := signingInput(t, document)
	if got != signingInputBytes {
		t.Fatalf("the signing input is\n%s\nand the contract's vector holds\n%s\nthe payload's field order, an added space or a key field moved would each break every client that verifies a set", got, signingInputBytes)
	}
	// The expiry is the vector's too, which is O18's 180 days as C1 wrote it.
	payload := keySetPayload(t, document)
	if payload.Exp != set.Payload.Exp {
		t.Errorf("the set expires at %d, and the vector's expires at %d", payload.Exp, set.Payload.Exp)
	}
	if got := payload.JWKS.Keys[0].Alg; got != mobilesession.KeySetAlg {
		t.Errorf("the published key carries the algorithm %q, want %q", got, mobilesession.KeySetAlg)
	}
	if rootJWK.X == "" || rootJWK.Kid == "" {
		t.Errorf("the root key's public half came back as %+v, and the ceremony records it", rootJWK)
	}
}

// TestKeysetSignatureVerifiesWithTheRootKey: what the command writes is a
// signature over the signing input by the key version it named, and it verifies
// with that key's public half — which is what P1 fetches, what P8 pins, and the
// whole of what makes a fetched key set trustworthy.
func TestKeysetSignatureVerifiesWithTheRootKey(t *testing.T) {
	ring, root := rootRingFixture(t)
	now := time.Unix(1794960000, 0).UTC()
	document, rootJWK, err := SignKeySet(context.Background(), root, KeySetRequest{
		JWKS:    signingKeyJWKS(t, "signing-2026-11"),
		Seq:     7,
		RootKID: testRootKID,
	}, now)
	if err != nil {
		t.Fatalf("signing a key set: %v", err)
	}
	input, signature := signingInput(t, document)
	if len(signature) != ed25519.SignatureSize {
		t.Fatalf("the signature is %d bytes, and an Ed25519 one is %d", len(signature), ed25519.SignatureSize)
	}
	if !ed25519.Verify(ring.public, []byte(input), signature) {
		t.Fatal("the signature does not verify with the root key's public half, so every client refuses the set")
	}
	// The printed public half is the one that signed, and not a value the caller
	// typed beside the command: GetPublicKey is read from the key version that
	// signed, and it is the same key by construction.
	printed, err := base64.RawURLEncoding.DecodeString(rootJWK.X)
	if err != nil {
		t.Fatalf("the printed public half is not base64url: %v", err)
	}
	if !slices.Equal(printed, []byte(ring.public)) {
		t.Error("the printed public half is not the key that signed, and P8 pins what this prints")
	}
	if rootJWK.Kty != "OKP" || rootJWK.Crv != "Ed25519" || rootJWK.Kid != testRootKID {
		t.Errorf("the printed public half is %+v, and the ceremony records an OKP/Ed25519 key under the root's kid", rootJWK)
	}

	// A set signed twice over one payload is one signature, because Ed25519 is
	// deterministic: the document is a function of its inputs and not of a draw.
}

// TestKeysetExpiresOneHundredAndEightyDaysOn is O18: a signed set is valid 180
// days on from the instant it was signed, which is the figure A3's alert fires
// 30 days before and the one Check 737 reads back.
func TestKeysetExpiresOneHundredAndEightyDaysOn(t *testing.T) {
	_, root := rootRingFixture(t)
	now := time.Unix(1794960000, 0).UTC()
	document, _, err := SignKeySet(context.Background(), root, KeySetRequest{
		JWKS:    signingKeyJWKS(t, "signing-2026-11"),
		Seq:     1,
		RootKID: testRootKID,
	}, now)
	if err != nil {
		t.Fatalf("signing a key set: %v", err)
	}
	payload := keySetPayload(t, document)
	if payload.Iat != now.Unix() {
		t.Errorf("the set was issued at %d, and the clock read %d", payload.Iat, now.Unix())
	}
	if got := time.Duration(payload.Exp-payload.Iat) * time.Second; got != KeySetLifetime {
		t.Errorf("the set is valid for %s, and O18 fixes %s", got, KeySetLifetime)
	}
	if want := now.Add(KeySetLifetime).Unix(); payload.Exp != want {
		t.Errorf("the set expires at %d, want %d", payload.Exp, want)
	}
}

// TestKeysetTheServiceReadsTheSetThisCommandWrites is the seam between the two
// halves of the ceremony: the document A4's command writes is the document A2's
// service reads, and nothing between them renames a field.
//
// The service reads the set for the kid of every key it publishes and for the
// moment the set stops being valid, and it refuses a set it cannot read — so a
// reader that looked for the keys anywhere but where this writer puts them would
// refuse the owner's first published set and, with it, every assertion. The two
// are held together here rather than in each package's own test, because what is
// being checked is that they agree.
func TestKeysetTheServiceReadsTheSetThisCommandWrites(t *testing.T) {
	_, root := rootRingFixture(t)
	now := time.Unix(1794960000, 0).UTC()
	const signingKID = "signing-2026-11"
	document, _, err := SignKeySet(context.Background(), root, KeySetRequest{
		JWKS:    signingKeyJWKS(t, signingKID),
		Seq:     1,
		RootKID: testRootKID,
	}, now)
	if err != nil {
		t.Fatalf("signing a key set: %v", err)
	}
	served, err := ParseServedSet(document)
	if err != nil {
		t.Fatalf("the identity service cannot read the set this command writes, so it refuses every assertion: %v", err)
	}
	if !slices.Equal(served.KIDs, []string{signingKID}) {
		t.Errorf("the service reads the kids %v, and the set publishes %s", served.KIDs, signingKID)
	}
	if want := now.Add(KeySetLifetime); !served.ExpiresAt.Equal(want) {
		t.Errorf("the service reads the set as valid until %s, and the signed set expires at %s", served.ExpiresAt, want)
	}
}

// TestKeysetRefusesASeqNotAboveTheLastSignedOne is the plan's red step: a set
// whose `seq` equals the last signed one is refused, and it is refused before
// the root key is asked for a signature at all.
//
// What it protects is the one thing the envelope's sequence number is for: a
// verifier refuses a set below the floor its build pinned or the highest one it
// has stored, so a lower set served by a cache or written by a stale command
// walks a project back onto keys it has already rotated out.
func TestKeysetRefusesASeqNotAboveTheLastSignedOne(t *testing.T) {
	_, root := rootRingFixture(t)
	previous, _, err := SignKeySet(context.Background(), root, KeySetRequest{
		JWKS:    signingKeyJWKS(t, "signing-2026-10"),
		Seq:     7,
		RootKID: testRootKID,
	}, time.Unix(1794000000, 0).UTC())
	if err != nil {
		t.Fatalf("signing the set being replaced: %v", err)
	}
	ring, root := rootRingFixture(t)
	for _, seq := range []int64{7, 6, 1} {
		_, _, err := SignKeySet(context.Background(), root, KeySetRequest{
			JWKS:     signingKeyJWKS(t, "signing-2026-11"),
			Seq:      seq,
			RootKID:  testRootKID,
			Previous: previous,
		}, time.Unix(1794960000, 0).UTC())
		if err == nil {
			t.Fatalf("the seq %d was signed over a set at 7, and a verifier refuses a set below the highest one it has stored", seq)
		}
		if !strings.Contains(err.Error(), "not above") {
			t.Errorf("the refusal reads %q, and it should say the seq is not above the last signed one", err)
		}
	}
	if ring.signCalls != 0 {
		t.Fatalf("the root key was asked to sign %d time(s) for a set this command will not publish, and a signature the command does not publish is one an owner cannot account for", ring.signCalls)
	}
	// The next one, one above, is signed: the refusal is about going backwards
	// and not about a set that replaces another.
	if _, _, err := SignKeySet(context.Background(), root, KeySetRequest{
		JWKS:     signingKeyJWKS(t, "signing-2026-11"),
		Seq:      8,
		RootKID:  testRootKID,
		Previous: previous,
	}, time.Unix(1794960000, 0).UTC()); err != nil {
		t.Fatalf("signing the set after the previous one: %v", err)
	}
	// A previous document whose seq cannot be read is a refusal and not a first
	// publish: this command cannot then tell whether the new number is above it.
	if _, _, err := SignKeySet(context.Background(), root, KeySetRequest{
		JWKS:     signingKeyJWKS(t, "signing-2026-11"),
		Seq:      8,
		RootKID:  testRootKID,
		Previous: []byte("not.a.document"),
	}, time.Unix(1794960000, 0).UTC()); err == nil {
		t.Fatal("a document whose seq cannot be read was signed over, and the command cannot then tell where the new set stands")
	}
}

// TestKeysetRefusesWhatItCannotSign: every input that is not a key set a
// verifier could use is refused before the root key is asked to sign it, so that
// a document the command would not publish is one the root key never signed.
func TestKeysetRefusesWhatItCannotSign(t *testing.T) {
	key := func(mutate func(*mobilesession.JWK)) mobilesession.JWKS {
		jwks := signingKeyJWKS(t, "signing-2026-11")
		mutate(&jwks.Keys[0])
		return jwks
	}
	cases := []struct {
		name string
		req  KeySetRequest
		want string
	}{
		{"no keys at all", KeySetRequest{JWKS: mobilesession.JWKS{}, Seq: 1, RootKID: testRootKID}, "no keys"},
		{"another key type", KeySetRequest{JWKS: key(func(k *mobilesession.JWK) { k.Kty = "RSA" }), Seq: 1, RootKID: testRootKID}, "OKP"},
		{"another curve", KeySetRequest{JWKS: key(func(k *mobilesession.JWK) { k.Crv = "P-256" }), Seq: 1, RootKID: testRootKID}, "Ed25519"},
		{"another algorithm", KeySetRequest{JWKS: key(func(k *mobilesession.JWK) { k.Alg = "ES256" }), Seq: 1, RootKID: testRootKID}, "only one the contract holds"},
		{"another use", KeySetRequest{JWKS: key(func(k *mobilesession.JWK) { k.Use = "enc" }), Seq: 1, RootKID: testRootKID}, `use`},
		{"a public half that is not base64url", KeySetRequest{JWKS: key(func(k *mobilesession.JWK) { k.X = "not base64url!" }), Seq: 1, RootKID: testRootKID}, "base64url"},
		{"a public half of the wrong length", KeySetRequest{JWKS: key(func(k *mobilesession.JWK) { k.X = base64.RawURLEncoding.EncodeToString([]byte("short")) }), Seq: 1, RootKID: testRootKID}, "32"},
		{"a key with no id", KeySetRequest{JWKS: key(func(k *mobilesession.JWK) { k.Kid = "  " }), Seq: 1, RootKID: testRootKID}, "no key id"},
		{"no root kid", KeySetRequest{JWKS: signingKeyJWKS(t, "signing-2026-11"), Seq: 1}, "root key's kid"},
		{"a seq of zero", KeySetRequest{JWKS: signingKeyJWKS(t, "signing-2026-11"), Seq: 0, RootKID: testRootKID}, "positive number"},
		{"a negative seq", KeySetRequest{JWKS: signingKeyJWKS(t, "signing-2026-11"), Seq: -1, RootKID: testRootKID}, "positive number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ring, root := rootRingFixture(t)
			if _, _, err := SignKeySet(context.Background(), root, tc.req, time.Unix(1794960000, 0).UTC()); err == nil {
				t.Fatal("a key set was signed over an input no verifier can use")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal reads %q, and it should name %q", err, tc.want)
			}
			if ring.signCalls != 0 {
				t.Fatalf("the root key was asked to sign %d time(s) over an input this command refuses", ring.signCalls)
			}
		})
	}

	// Two keys under one id: a verifier reading that id cannot tell which one
	// signed, so the set is refused rather than served.
	ring, root := rootRingFixture(t)
	duplicate := signingKeyJWKS(t, "signing-2026-11")
	duplicate.Keys = append(duplicate.Keys, duplicate.Keys[0])
	if _, _, err := SignKeySet(context.Background(), root, KeySetRequest{JWKS: duplicate, Seq: 1, RootKID: testRootKID}, time.Unix(1794960000, 0).UTC()); err == nil {
		t.Fatal("a set with two keys under one id was signed")
	}
	if ring.signCalls != 0 {
		t.Fatal("the root key was asked to sign a set with two keys under one id")
	}
}

// TestKeysetRefusesASigningKeyRingItCannotUse: every answer from the key ring
// that is not a whole signature by the version this command named, and every
// public half that is not that version's Ed25519 key, is refused rather than
// assembled into a document clients fetch and refuse.
func TestKeysetRefusesASigningKeyRingItCannotUse(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating an RSA key: %v", err)
	}
	rsaPEM, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatalf("encoding an RSA public key: %v", err)
	}
	req := func(t *testing.T) KeySetRequest {
		return KeySetRequest{JWKS: signingKeyJWKS(t, "signing-2026-11"), Seq: 1, RootKID: testRootKID}
	}
	cases := []struct {
		name  string
		fault func(*fakeRootRing)
		want  string
	}{
		{"the ring did not check the signing input", func(f *fakeRootRing) { f.skipVerified = true }, "did not check"},
		{"the ring answered no signature", func(f *fakeRootRing) { f.skipSign = true }, "empty signature"},
		{"the signature is not the one the ring counted", func(f *fakeRootRing) { f.corruptSig = true }, "not the ones it counted"},
		{"the ring signed with another version", func(f *fakeRootRing) { f.signOther = true }, "signed with"},
		{"the signing call failed", func(f *fakeRootRing) { f.failSign = errors.New("permission denied") }, "signing API"},
		{"the public key read failed", func(f *fakeRootRing) { f.failPublic = errors.New("permission denied") }, "public half"},
		{"the key version is not an Ed25519 one", func(f *fakeRootRing) { f.algorithm = kmspb.CryptoKeyVersion_RSA_SIGN_PKCS1_2048_SHA256 }, "EC_SIGN_ED25519"},
		{"the public half is another key type", func(f *fakeRootRing) {
			f.pem = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: rsaPEM}))
		}, "Ed25519"},
		{"the public half is not a PEM document", func(f *fakeRootRing) { f.pem = "not a pem document" }, "PEM"},
		{"the public half is not the one the ring counted", func(f *fakeRootRing) { f.corruptPEMCRC = true }, "not the ones it counted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ring, root := rootRingFixture(t)
			tc.fault(ring)
			if _, _, err := SignKeySet(context.Background(), root, req(t), time.Unix(1794960000, 0).UTC()); err == nil {
				t.Fatal("a key set was answered for a key ring that cannot sign one")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal reads %q, and it should name %q", err, tc.want)
			}
		})
	}
}

// TestKeysetRefusesASignerItCannotBuild: an asymmetric key has no primary
// version to fall back to, so a signer built with no version or no client would
// fail at the ceremony rather than at the start.
func TestKeysetRefusesASignerItCannotBuild(t *testing.T) {
	ring, _ := rootRingFixture(t)
	if _, err := NewRootKey(ring, "   "); err == nil {
		t.Fatal("a root key signer was built with no key version")
	}
	if _, err := NewRootKey(nil, testRootVersion); err == nil {
		t.Fatal("a root key signer was built with no client")
	}
	root, err := NewRootKey(ring, " "+testRootVersion+" ")
	if err != nil {
		t.Fatalf("building a signer over a padded version name: %v", err)
	}
	if _, _, err := SignKeySet(context.Background(), root, KeySetRequest{JWKS: signingKeyJWKS(t, "signing-2026-11"), Seq: 1, RootKID: testRootKID}, time.Unix(1794960000, 0).UTC()); err != nil {
		t.Fatalf("signing with a version name that has whitespace around it: %v", err)
	}
}

// TestKeysetCarriesNoPrivateMaterial: nothing the command writes or prints holds
// a private half (.claude/rules/ee.md R5). The document is a public one by
// construction — a JWK has no field a private key could travel in — and this
// holds it on the bytes, which is what a reader of the served URL gets.
func TestKeysetCarriesNoPrivateMaterial(t *testing.T) {
	ring, root := rootRingFixture(t)
	document, rootJWK, err := SignKeySet(context.Background(), root, KeySetRequest{
		JWKS:    signingKeyJWKS(t, "signing-2026-11"),
		Seq:     1,
		RootKID: testRootKID,
	}, time.Unix(1794960000, 0).UTC())
	if err != nil {
		t.Fatalf("signing a key set: %v", err)
	}
	for _, needle := range []string{"PRIVATE KEY", "private", `"d"`, "-----BEGIN"} {
		if strings.Contains(string(document), needle) {
			t.Errorf("the signed key set carries %q, and a served document holds public halves only", needle)
		}
	}
	payload := keySetPayload(t, document)
	for _, key := range payload.JWKS.Keys {
		if key.Kty != "OKP" || key.Crv != "Ed25519" || key.Kid == "" || key.X == "" {
			t.Errorf("a published key is %+v, and a verifier needs kty, crv, kid and x", key)
		}
	}
	// The root key's own public half is printed and its private half is not in
	// this process at all: what the fake holds is the test's, and the signer has
	// no accessor for it.
	if strings.Contains(rootJWK.X, string(ring.key)) || strings.Contains(rootJWK.X, base64.RawURLEncoding.EncodeToString(ring.key)) {
		t.Fatal("the printed root key carries the private half")
	}
	if rootJWK.Use != "sig" || rootJWK.Alg != mobilesession.KeySetAlg {
		t.Errorf("the printed root key is %+v, and a signing key's alg and use are %s and \"sig\"", rootJWK, mobilesession.KeySetAlg)
	}
}

// TestKeysetParsesThePlainJWKS: the command is given the JWKS A2 serves, which
// carries no `alg` and no `use`, and the set it signs carries both — the two
// documents describe the same key and the envelope is the one C1's contract
// holds.
func TestKeysetParsesThePlainJWKS(t *testing.T) {
	plain := `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"signing-2026-11","x":"j22h5_4vmc0wyywl-Y5eNzRPt6zWzShHzianUOOz-JE"}]}`
	jwks, err := ParseKeySetJWKS([]byte(plain))
	if err != nil {
		t.Fatalf("parsing the plain JWKS: %v", err)
	}
	key := jwks.Keys[0]
	if key.Alg != mobilesession.KeySetAlg || key.Use != "sig" {
		t.Errorf("the parsed key is %+v, and the signed set carries %s and \"sig\"", key, mobilesession.KeySetAlg)
	}
	if key.Kid != "signing-2026-11" || key.X == "" {
		t.Errorf("the parsed key is %+v, and it should carry the id and public half it was given", key)
	}
	for _, bad := range []string{
		`{"keys":[]}`,
		`{"keys":[{"kty":"RSA","crv":"Ed25519","kid":"k","x":"j22h5_4vmc0wyywl-Y5eNzRPt6zWzShHzianUOOz-JE"}]}`,
		`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k","x":"j22h5_4vmc0wyywl-Y5eNzRPt6zWzShHzianUOOz-JE","thumbprint":"nope"}]}`,
		`not json`,
	} {
		if _, err := ParseKeySetJWKS([]byte(bad)); err == nil {
			t.Errorf("%s parsed as a key set, and the command signs what a verifier can use or nothing", bad)
		}
	}
}

// TestKeysetRefusesACodingAgentsSession is O15's first fence: an agent's session
// cannot run the ceremony. It is the classification internal/agentenv makes —
// an agent-set marker refuses whatever the terminal, and the IDE extension's
// marker alone refuses only where no real terminal is on the run's input — and
// the second half is what lets a person run it from their own IDE terminal.
//
// The fence reads agentenv.Markers itself rather than one variable, so a marker
// the tree adds is one this path is refused on.
func TestKeysetRefusesACodingAgentsSession(t *testing.T) {
	env := func(pairs map[string]string) func(string) string {
		return func(k string) string { return pairs[k] }
	}
	// The fence reads agentenv.AgentMarkers and agentenv.IDEMarker, so a marker
	// the tree adds is one this path is refused on.
	for _, marker := range agentenv.AgentMarkers {
		err := RefuseAgentSession(env(map[string]string{marker: "1"}), true)
		if err == nil {
			t.Fatalf("the marker %s did not refuse the signing command, and an agent's session must not run the ceremony (O15)", marker)
		}
		if !strings.Contains(err.Error(), marker) || !strings.Contains(err.Error(), "agent") {
			t.Errorf("the refusal for %s reads %q, and it should name the marker and say an agent's session is the reason", marker, err)
		}
	}
	if err := RefuseAgentSession(env(nil), true); err != nil {
		t.Errorf("a person's own terminal was refused: %v", err)
	}
	// The one case that is refused by the absence of a terminal: the IDE
	// extension's marker with no terminal on the run's input is an agent's tool
	// call, and it is the same reading internal/agentenv.Session makes.
	if err := RefuseAgentSession(env(map[string]string{agentenv.IDEMarker: "1"}), false); err == nil {
		t.Error("an agent's tool call carrying only the IDE marker was allowed to sign a key set")
	}
	if err := RefuseAgentSession(env(map[string]string{agentenv.IDEMarker: "1"}), true); err != nil {
		t.Errorf("a person's own IDE terminal was refused: %v", err)
	}
}
