// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The KMS signer (portal plan P1.7). What these tests hold is the one property
// no other task can state from the outside: the signature the minter mails is
// the one the *key ring* made over the bytes this service built, and not a
// signature some file in this repository made.
//
// The key here is generated inside the test's own process and its private half
// never leaves it — nothing under ee/ holds, reads or reaches a signing key
// (.claude/rules/ee.md R5). What the fake stands for is Cloud KMS's side of the
// call: it signs with the private half it holds and answers the CRC32C Cloud KMS
// answers beside it, so Sign's own checks run for real and the signature that
// comes back is verified against the public half the way a released verifier
// verifies it.

import (
	"context"
	"crypto/ed25519"
	"errors"
	"hash/crc32"
	"strings"
	"testing"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// fakeKeyRing is Cloud KMS as the signer sees it: one key version, its own
// private half, and the integrity fields the service fills in on the way back.
type fakeKeyRing struct {
	name string
	key  ed25519.PrivateKey

	// What the fake does differently, one fault at a time.
	failWith       error
	skipSignature  bool
	corruptSig     bool
	skipVerified   bool
	signWithOther  bool
	lastRequest    *kmspb.AsymmetricSignRequest
	lastSignatures int
}

func (f *fakeKeyRing) AsymmetricSign(_ context.Context, req *kmspb.AsymmetricSignRequest, _ ...gax.CallOption) (*kmspb.AsymmetricSignResponse, error) {
	f.lastRequest = req
	if f.failWith != nil {
		return nil, f.failWith
	}
	// A key ring that was asked to sign answers the same way whether or not it
	// agrees with the CRC32C it was sent (the flag is what says so), so the
	// request's own sum is only read here to be sure the service sent one.
	if req.GetDataCrc32C() == nil || req.GetDataCrc32C().Value != int64(crc32.Checksum(req.GetData(), crcTable)) {
		return nil, errors.New("the request carried no CRC32C of the bytes it asked to sign")
	}
	answer := &kmspb.AsymmetricSignResponse{
		Name:               f.name,
		VerifiedDataCrc32C: !f.skipVerified,
	}
	if f.signWithOther {
		answer.Name = strings.TrimSuffix(f.name, "/cryptoVersions/1") + "/cryptoVersions/2"
	}
	if f.skipSignature {
		return answer, nil
	}
	f.lastSignatures++
	signature := ed25519.Sign(f.key, req.GetData())
	answer.SignatureCrc32C = wrapperspb.Int64(int64(crc32.Checksum(signature, crcTable)))
	if f.corruptSig {
		// The bytes travel damaged while the sum beside them is the sum of
		// what the ring really made, which is the one shape a truncated
		// answer has.
		signature[0] ^= 0xff
	}
	answer.Signature = signature
	return answer, nil
}

// fakeKeyRingFor builds one key ring with a key this test holds the public half
// of, which is what the assertion verifies with.
func fakeKeyRingFor(t *testing.T) (*fakeKeyRing, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating the ring's key: %v", err)
	}
	// The name is a key version's resource name, which is what the minter's own
	// configuration carries (FUGARO_SIGNER_KEY_VERSION).
	return &fakeKeyRing{
		name: "projects/portal/locations/europe-west1/keyRings/fugaro-keys/cryptoKeys/licence/cryptoVersions/1",
		key:  priv,
	}, pub
}

// TestKMSSignerVerifiesWithTheKeyring is the task's own: the signer's signature
// verifies with the ring's public half over exactly the bytes it was handed,
// which is what makes the key the minter mails one a released verifier accepts.
func TestKMSSignerVerifiesWithTheKeyring(t *testing.T) {
	ring, pub := fakeKeyRingFor(t)
	signer, err := NewKMSSigner(ring, ring.name)
	if err != nil {
		t.Fatalf("building the signer: %v", err)
	}
	// A claim set shaped like the ones the minter signs — small, JSON, and the
	// same bytes twice has to give the same signature.
	payload := []byte(`{"key":"FG-1-STAGING-0001","tier":"pro","exp":1794000000,"ver":1}`)
	first, err := signer.Sign(context.Background(), payload)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	if len(first) != ed25519.SignatureSize {
		t.Fatalf("the signature is %d bytes, and an Ed25519 one is %d", len(first), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, payload, first) {
		t.Fatal("the signature does not verify with the ring's public half, so the key the minter would mail verifies nowhere")
	}
	// The request named the configured key version and the payload as data: a
	// digest would be signed as opaque bytes and verify nowhere (S2.1).
	if got := ring.lastRequest.GetName(); got != ring.name {
		t.Fatalf("the call named %q, and the deployment configured %q", got, ring.name)
	}
	if got := ring.lastRequest.GetData(); string(got) != string(payload) {
		t.Fatalf("the call asked for %q to be signed, and this service built %q", got, payload)
	}
	if ring.lastRequest.GetDigest() != nil {
		t.Fatal("the call handed the ring a digest, and an Ed25519 key signs the payload itself")
	}
	again, err := signer.Sign(context.Background(), payload)
	if err != nil {
		t.Fatalf("signing again: %v", err)
	}
	if string(again) != string(first) {
		t.Fatal("two signatures over one payload differ, and Ed25519 is deterministic")
	}
}

// TestKMSSignerRefusesWhatItCannotVerify is the other half: every answer that is
// not a whole signature is refused rather than mailed as a key. Each case is a
// way a key would otherwise be assembled from bytes nobody can use.
func TestKMSSignerRefusesWhatItCannotVerify(t *testing.T) {
	cases := []struct {
		name  string
		fault func(*fakeKeyRing)
		wants string
	}{
		{"the ring did not check the payload", func(f *fakeKeyRing) { f.skipVerified = true }, "did not check"},
		{"the ring answered no signature", func(f *fakeKeyRing) { f.skipSignature = true }, "empty signature"},
		{"the signature is not the one the ring counted", func(f *fakeKeyRing) { f.corruptSig = true }, "not the ones it counted"},
		{"the ring signed with another version", func(f *fakeKeyRing) { f.signWithOther = true }, "signed with"},
		{"the call failed", func(f *fakeKeyRing) { f.failWith = errors.New("permission denied") }, "signing API"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ring, _ := fakeKeyRingFor(t)
			tc.fault(ring)
			signer, err := NewKMSSigner(ring, ring.name)
			if err != nil {
				t.Fatalf("building the signer: %v", err)
			}
			if _, err := signer.Sign(context.Background(), []byte("a claim set")); err == nil {
				t.Fatal("the signer answered a signature for an answer it should have refused")
			} else if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("the refusal reads %q, and it should name %q", err, tc.wants)
			}
			// A refused call is one the service did not act on: nothing was
			// signed for the empty-signature and no-payload cases, and the one
			// case where the ring did sign is the one it then refused itself.
			if ring.corruptSig && ring.lastSignatures != 1 {
				t.Fatalf("the ring was asked to sign %d times", ring.lastSignatures)
			}
		})
	}
}

// TestKMSSignerRefusesAPayloadItShouldNotSign holds the two bounds: no bytes at
// all, and more than a signing call reads. Both are requests this service should
// never have built, and both are refused before they reach the ring.
func TestKMSSignerRefusesAPayloadItShouldNotSign(t *testing.T) {
	ring, _ := fakeKeyRingFor(t)
	signer, err := NewKMSSigner(ring, ring.name)
	if err != nil {
		t.Fatalf("building the signer: %v", err)
	}
	if _, err := signer.Sign(context.Background(), nil); err == nil {
		t.Fatal("the signer signed an empty payload, and an empty signature crank is one every empty payload shares")
	}
	if ring.lastRequest != nil {
		t.Fatal("the empty payload reached the key ring")
	}
	if _, err := signer.Sign(context.Background(), make([]byte, maxSignBytes+1)); err == nil {
		t.Fatal("the signer sent a payload over the call's own bound")
	}
	if ring.lastRequest != nil {
		t.Fatal("the oversized payload reached the key ring")
	}
}

// TestKMSSignerNeedsAKeyVersion holds the one thing a signer cannot default: an
// asymmetric key has no primary version, so a signer built without a name would
// fail at the first mint instead of at the start.
func TestKMSSignerNeedsAKeyVersion(t *testing.T) {
	ring, _ := fakeKeyRingFor(t)
	if _, err := NewKMSSigner(ring, "   "); err == nil {
		t.Fatal("a signer was built with no key version")
	}
	if _, err := NewKMSSigner(nil, ring.name); err == nil {
		t.Fatal("a signer was built with no client")
	}
}
