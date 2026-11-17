// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The sealer (portal plan P1.7, D2182). What these tests hold is that a
// handover bundle goes in, comes back as itself, and never comes back as
// anything else: a reveal hands a successor a key and a renewal secret, so a
// bundle opened to the wrong bytes is a wrong credential mailed to a person who
// then cannot renew what they were given.
//
// The key here is generated inside the test's own process and is a symmetric one
// over AES-GCM, which is what stands in for the handover key's encrypt and
// decrypt: nothing here signs anything, and no signing key of any kind is
// touched (.claude/rules/ee.md R5 — the sealer is a different ring from the
// signer's for exactly this reason, Check 571).

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"hash/crc32"
	"io"
	"strings"
	"testing"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// fakeHandoverRing is the handover ring as the sealer sees it: one symmetric
// key, its own encrypt and decrypt, and the integrity fields Cloud KMS fills in
// on the way back.
type fakeHandoverRing struct {
	aead cipher.AEAD
	name string

	failWith     error
	skipSealed   bool
	corruptSeal  bool
	skipVerified bool
	last         any
}

func newFakeHandoverRing(t *testing.T) *fakeHandoverRing {
	t.Helper()
	// The suite has no home directory and no network (CLAUDE.md), and a key
	// generated here is the same kind of value the ring's is: 32 bytes this
	// process holds and nothing outside the call reads.
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		t.Fatalf("generating the handover key: %v", err)
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		t.Fatalf("building the handover cipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("building the handover aead: %v", err)
	}
	return &fakeHandoverRing{
		aead: aead,
		name: "projects/portal/locations/europe-west1/keyRings/fugaro-portal-handover/cryptoKeys/handover",
	}
}

func (f *fakeHandoverRing) Encrypt(_ context.Context, req *kmspb.EncryptRequest, _ ...gax.CallOption) (*kmspb.EncryptResponse, error) {
	f.last = req
	if f.failWith != nil {
		return nil, f.failWith
	}
	if req.GetPlaintextCrc32C() == nil || req.GetPlaintextCrc32C().Value != int64(crc32.Checksum(req.GetPlaintext(), crcTable)) {
		return nil, errors.New("the request carried no CRC32C of the bytes it asked to seal")
	}
	answer := &kmspb.EncryptResponse{
		Name:                    f.name,
		VerifiedPlaintextCrc32C: !f.skipVerified,
	}
	if f.skipSealed {
		return answer, nil
	}
	nonce := make([]byte, f.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := f.aead.Seal(nonce, nonce, req.GetPlaintext(), nil)
	answer.CiphertextCrc32C = wrapperspb.Int64(int64(crc32.Checksum(sealed, crcTable)))
	if f.corruptSeal {
		sealed[0] ^= 0xff
	}
	answer.Ciphertext = sealed
	return answer, nil
}

func (f *fakeHandoverRing) Decrypt(_ context.Context, req *kmspb.DecryptRequest, _ ...gax.CallOption) (*kmspb.DecryptResponse, error) {
	f.last = req
	if f.failWith != nil {
		return nil, f.failWith
	}
	if req.GetCiphertextCrc32C() == nil || req.GetCiphertextCrc32C().Value != int64(crc32.Checksum(req.GetCiphertext(), crcTable)) {
		return nil, errors.New("the request carried no CRC32C of the bytes it asked to open")
	}
	sealed := req.GetCiphertext()
	if len(sealed) < f.aead.NonceSize() {
		return nil, errors.New("the sealed bundle is shorter than a nonce")
	}
	plaintext, err := f.aead.Open(nil, sealed[:f.aead.NonceSize()], sealed[f.aead.NonceSize():], nil)
	if err != nil {
		return nil, err
	}
	answer := &kmspb.DecryptResponse{Plaintext: plaintext}
	answer.PlaintextCrc32C = wrapperspb.Int64(int64(crc32.Checksum(plaintext, crcTable)))
	if f.corruptSeal {
		// The plaintext travels damaged while the sum beside it is the sum of
		// what was really opened.
		answer.Plaintext[0] ^= 0xff
	}
	return answer, nil
}

// TestKMSSealerRoundTrips is the task's own: what the sealer sealed is what it
// opens, byte for byte, and the ciphertext in between is not the plaintext.
func TestKMSSealerRoundTrips(t *testing.T) {
	ring := newFakeHandoverRing(t)
	sealer, err := NewKMSSealer(ring, ring.name)
	if err != nil {
		t.Fatalf("building the sealer: %v", err)
	}
	// The bundle is the shape D2182 stores: the successor's key text and its
	// renewal secret, as one JSON document with the punctuation a credential
	// carries.
	bundle := []byte(`{"key":"FG-1-STAGING-0001.eyJ0aWVyIjoicHJvIn0.c2ln","renewal_secret":"r3n3w-0001"}`)
	sealed, err := sealer.Seal(context.Background(), bundle)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}
	if strings.Contains(string(sealed), "renewal_secret") {
		t.Fatal("the ciphertext carries the plaintext, and a record storing it would hold a live credential")
	}
	opened, err := sealer.Open(context.Background(), sealed)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if string(opened) != string(bundle) {
		t.Fatalf("the bundle opened to %q, and what was sealed is %q", opened, bundle)
	}
	// The calls named the configured key and not a version of it: encrypt and
	// decrypt are answered by the key, which picks its primary version.
	if got := ring.last.(*kmspb.DecryptRequest).GetName(); got != ring.name {
		t.Fatalf("the open call named %q, and the deployment configured %q", got, ring.name)
	}
}

// TestKMSSealerRefusesWhatItCannotTrust holds the refusals, one fault at a time.
// Each is a way a reveal would otherwise hand over a credential nobody can use
// or one that is not the one that was stored.
func TestKMSSealerRefusesWhatItCannotTrust(t *testing.T) {
	cases := []struct {
		name  string
		fault func(*fakeHandoverRing)
		wants string
	}{
		{"the ring did not check the bundle it sealed", func(f *fakeHandoverRing) { f.skipVerified = true }, "did not check"},
		{"the ring answered no ciphertext", func(f *fakeHandoverRing) { f.skipSealed = true }, "no ciphertext"},
		{"the ciphertext is not the one the ring counted", func(f *fakeHandoverRing) { f.corruptSeal = true }, "not the ones it counted"},
		{"the call failed", func(f *fakeHandoverRing) { f.failWith = errors.New("permission denied") }, "encrypt call"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ring := newFakeHandoverRing(t)
			tc.fault(ring)
			sealer, err := NewKMSSealer(ring, ring.name)
			if err != nil {
				t.Fatalf("building the sealer: %v", err)
			}
			if _, err := sealer.Seal(context.Background(), []byte("a handover")); err == nil {
				t.Fatal("the sealer answered a ciphertext for an answer it should have refused")
			} else if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("the refusal reads %q, and it should name %q", err, tc.wants)
			}
		})
	}
	// And the opening side: a plaintext that is not the one the ring counted is
	// refused rather than revealed.
	t.Run("the plaintext is not the one the ring counted", func(t *testing.T) {
		ring := newFakeHandoverRing(t)
		sealer, err := NewKMSSealer(ring, ring.name)
		if err != nil {
			t.Fatalf("building the sealer: %v", err)
		}
		sealed, err := sealer.Seal(context.Background(), []byte("a handover"))
		if err != nil {
			t.Fatalf("sealing: %v", err)
		}
		ring.corruptSeal = true
		if _, err := sealer.Open(context.Background(), sealed); err == nil {
			t.Fatal("the sealer opened a plaintext whose bytes are not the ones the ring counted")
		} else if !strings.Contains(err.Error(), "not what was sealed") {
			t.Fatalf("the refusal reads %q", err)
		}
	})
}

// TestKMSSealerRefusesAnEmptyBundle holds the two ends that have no meaning: no
// bytes to seal, and no bytes to open. A plaintext key in the store and a
// handover that opens to nothing are both worse than a refusal.
func TestKMSSealerRefusesAnEmptyBundle(t *testing.T) {
	ring := newFakeHandoverRing(t)
	sealer, err := NewKMSSealer(ring, ring.name)
	if err != nil {
		t.Fatalf("building the sealer: %v", err)
	}
	if _, err := sealer.Seal(context.Background(), nil); err == nil {
		t.Fatal("the sealer sealed nothing, and a seal over no bytes stores no handover")
	}
	if _, err := sealer.Open(context.Background(), nil); err == nil {
		t.Fatal("the sealer opened nothing")
	}
	if _, err := sealer.Seal(context.Background(), make([]byte, maxSealBytes+1)); err == nil {
		t.Fatal("the sealer sent a bundle over the call's own bound")
	}
	if _, err := NewKMSSealer(ring, ""); err == nil {
		t.Fatal("a sealer was built with no key, and a handover sealed under nothing is a plaintext key in the store")
	}
	if _, err := NewKMSSealer(nil, ring.name); err == nil {
		t.Fatal("a sealer was built with no client")
	}
}
