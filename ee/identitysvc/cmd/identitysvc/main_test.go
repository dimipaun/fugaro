// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package main

// The identity service's program (mobile identity plan task A1). What these
// tests hold is the half of the deployment no test in ee/identitysvc can state:
// the KMS answer is the one the *key ring* made over the bytes this program
// built, and the environment this program starts on is exactly the one it names
// — every other FUGARO_ variable, every missing one and every empty one is a
// refusal to start rather than a default nobody chose.
//
// The key here is generated inside the test's own process and its private half
// never leaves it: nothing under ee/ holds, reads or reaches a signing key
// (.claude/rules/ee.md R5). What the fake stands for is Cloud KMS's side of the
// call — it signs with the private half it holds and answers the CRC32C Cloud
// KMS answers beside it — so Sign's own checks run for real and the signature
// that comes back is verified against the public half the way a project's
// verifier verifies it. The pattern is ee/keyservice/kmssigner_test.go's, which
// holds the same checks on the minter's own signer.

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
	// request's own sum is read here only to be sure the service sent one.
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
		// The bytes travel damaged while the sum beside them is the sum of what
		// the ring really made, which is the one shape a truncated answer has.
		signature[0] ^= 0xff
	}
	answer.Signature = signature
	return answer, nil
}

// fakeKeyRingFor builds one key ring with a key this test holds the public half
// of, which is what the assertion's signature is verified with.
func fakeKeyRingFor(t *testing.T) (*fakeKeyRing, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating the ring's key: %v", err)
	}
	// The name is a key version's resource name, which is what the deployment's
	// FUGARO_IDENTITY_KEY_VERSION carries.
	return &fakeKeyRing{
		name: "projects/fugaro-ident/locations/europe-west1/keyRings/identity/cryptoKeys/assertion/cryptoVersions/1",
		key:  priv,
	}, pub
}

// TestKMSSignerVerifiesWithTheKeyring is the task's own: the signature the
// signer answers is the one the *key ring* made, over exactly the bytes this
// program built, and it verifies with the ring's public half — which is what
// makes an assertion a project's verifier accepts.
func TestKMSSignerVerifiesWithTheKeyring(t *testing.T) {
	ring, pub := fakeKeyRingFor(t)
	signer, err := newKMSSigner(ring, ring.name)
	if err != nil {
		t.Fatalf("building the signer: %v", err)
	}
	// A signing input shaped like the ones this service signs: two base64url
	// segments, and the same bytes twice has to give the same signature.
	payload := []byte("eyJhbGciOiJFZERTQSIsImtpZCI6ImlkZW50aXR5LTIwMjYtMTEifQ.eyJhdWQiOiJlZGdlIn0")
	first, err := signer.Sign(context.Background(), payload)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	if len(first) != ed25519.SignatureSize {
		t.Fatalf("the signature is %d bytes, and an Ed25519 one is %d", len(first), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, payload, first) {
		t.Fatal("the signature does not verify with the ring's public half, so the assertion verifies nowhere")
	}
	// The request named the configured key version and the payload as data: a
	// digest would be signed as opaque bytes and verify nowhere.
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
// not a whole signature is refused rather than assembled into an assertion no
// project can verify. Each case is a way a document would otherwise be minted
// around bytes nobody can use.
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
			signer, err := newKMSSigner(ring, ring.name)
			if err != nil {
				t.Fatalf("building the signer: %v", err)
			}
			if _, err := signer.Sign(context.Background(), []byte("a signing input")); err == nil {
				t.Fatal("the signer answered a signature for an answer it should have refused")
			} else if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("the refusal reads %q, and it should name %q", err, tc.wants)
			}
			// A refused call is one the service did not act on: nothing was
			// signed for the empty-signature case, and the one case where the
			// ring did sign is the one it then refused itself.
			if ring.corruptSig && ring.lastSignatures != 1 {
				t.Fatalf("the ring was asked to sign %d times", ring.lastSignatures)
			}
		})
	}
}

// TestKMSSignerRefusesAPayloadItShouldNotSign: a signature over no bytes is one
// every empty payload shares, so it is refused before the ring rather than read
// as a key ring that failed.
func TestKMSSignerRefusesAPayloadItShouldNotSign(t *testing.T) {
	ring, _ := fakeKeyRingFor(t)
	signer, err := newKMSSigner(ring, ring.name)
	if err != nil {
		t.Fatalf("building the signer: %v", err)
	}
	if _, err := signer.Sign(context.Background(), nil); err == nil {
		t.Fatal("the signer signed an empty payload, and an empty signature crank is one every empty payload shares")
	}
	if ring.lastRequest != nil {
		t.Fatal("the empty payload reached the key ring")
	}
}

// TestKMSSignerNeedsAKeyVersion holds the one thing a signer cannot default: an
// asymmetric key has no primary version, so a signer built without a name would
// fail at the first sign-in instead of at the start.
func TestKMSSignerNeedsAKeyVersion(t *testing.T) {
	ring, _ := fakeKeyRingFor(t)
	if _, err := newKMSSigner(ring, "   "); err == nil {
		t.Fatal("a signer was built with no key version")
	}
	if _, err := newKMSSigner(nil, ring.name); err == nil {
		t.Fatal("a signer was built with no client")
	}
}

// configEnv is one deployment's environment, and the value of one variable in
// it replaced or removed: every case below is this program starting, or
// refusing to.
func configEnv(t *testing.T, mutate func(env map[string]string)) []string {
	t.Helper()
	env := map[string]string{
		varProject:    "fugaro-ident",
		varKeyID:      "identity-2026-11",
		varKeyVersion: "projects/fugaro-ident/locations/europe-west1/keyRings/identity/cryptoKeys/assertion/cryptoVersions/1",
		varKeySet:     "gs://fugaro-ident-keys/keyset.jws",
		// A variable outside this program's namespace is not its business.
		"PATH": "/usr/bin",
	}
	if mutate != nil {
		mutate(env)
	}
	out := make([]string, 0, len(env))
	for name, value := range env {
		out = append(out, name+"="+value)
	}
	return out
}

// TestLoadConfigReadsThisProgramsOwnEnvironment: the four variables this service
// needs are read into the configuration a deployment runs as.
func TestLoadConfigReadsThisProgramsOwnEnvironment(t *testing.T) {
	cfg, err := loadConfig(configEnv(t, nil))
	if err != nil {
		t.Fatalf("loading the environment: %v", err)
	}
	if cfg.Project != "fugaro-ident" {
		t.Fatalf("the project is %q, and the environment named fugaro-ident", cfg.Project)
	}
	if cfg.KeyID != "identity-2026-11" {
		t.Fatalf("the key id is %q, and the environment named identity-2026-11", cfg.KeyID)
	}
	if !strings.HasSuffix(cfg.KeyVersion, "/cryptoVersions/1") {
		t.Fatalf("the key version is %q, and the environment named a key version's resource name", cfg.KeyVersion)
	}
	if cfg.KeySetURL != "gs://fugaro-ident-keys/keyset.jws" {
		t.Fatalf("the key set's location is %q, and the environment named the bucket object", cfg.KeySetURL)
	}
}

// TestLoadConfigRefusesAnEnvironmentItCannotRun is the fence: a variable that is
// missing, empty, spelled wrongly or set for another service is a refusal to
// start. Each of them would otherwise run as a default nobody chose — and a
// defaulted key id or project id here is an assertion signed for a project
// nobody named.
func TestLoadConfigRefusesAnEnvironmentItCannotRun(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(env map[string]string)
		wants  string
	}{
		{"no project", func(env map[string]string) { delete(env, varProject) }, varProject},
		{"no key id", func(env map[string]string) { delete(env, varKeyID) }, varKeyID},
		{"no key version", func(env map[string]string) { delete(env, varKeyVersion) }, varKeyVersion},
		{"no key set", func(env map[string]string) { delete(env, varKeySet) }, varKeySet},
		{"an empty project", func(env map[string]string) { env[varProject] = "  " }, varProject},
		{"an empty key set", func(env map[string]string) { env[varKeySet] = "" }, varKeySet},
		{"a variable of another service", func(env map[string]string) { env["FUGARO_PORTAL_URL"] = "https://portal.example" }, "FUGARO_PORTAL_URL"},
		{"a misspelled variable", func(env map[string]string) { env["FUGARO_IDENTITY_KEYID"] = "identity-2026-11" }, "FUGARO_IDENTITY_KEYID"},
		{"a key set that is not a URI", func(env map[string]string) { env[varKeySet] = "://nope" }, varKeySet},
		{"a key set with no scheme", func(env map[string]string) { env[varKeySet] = "fugaro-ident-keys/keyset.jws" }, "not a bucket URI"},
		{"a key set naming no object", func(env map[string]string) { env[varKeySet] = "gs://fugaro-ident-keys" }, "names no object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadConfig(configEnv(t, tc.mutate))
			if err == nil {
				t.Fatalf("the environment was read as %+v, and this program cannot start on it", cfg)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("the refusal reads %q, and it should name %q", err, tc.wants)
			}
		})
	}
	// A line that is not a NAME=value pair at all is refused as well: the
	// platform's own environment is not this program's to interpret, but a
	// malformed entry would otherwise be skipped as though it were absent.
	if _, err := loadConfig([]string{"not a pair"}); err == nil {
		t.Fatal("an environment holding a line that is not NAME=value was read")
	}
}
