// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package main

// The owner's key-set signing command (mobile identity plan task A4). What these
// tests hold is what the ceremony itself cannot state: that the fence closes
// before a single byte is read, that every missing value is a refusal and never a
// question (there is no input to read and no prompt to answer), that a set whose
// `seq` is not above the one at the destination is refused with the root key
// never asked to sign, and that the document written is the envelope A2 serves
// and the root public half is the only key material that reaches the terminal.
//
// Nothing here holds a key. The key ring is faked with a key generated in the
// test's own process, and the destination is a file bucket under t.TempDir(), so
// the whole command — fence, flags, JWKS, signature, write — runs with no
// network, no account and no cloud (.claude/rules/ee.md R5).

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dimipaun/fugaro/ee/identitysvc"
	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/mobilesession"
)

const (
	commandRootKID     = "root-2026-11"
	commandRootVersion = "projects/fugaro-root/locations/europe-west1/keyRings/root/cryptoKeys/keyset/cryptoVersions/1"
	commandJWKSKID     = "identity-2026-11"
)

var commandCRC = crc32.MakeTable(crc32.Castagnoli)

// testRing is Cloud KMS as this command sees it: one key version that signs the
// bytes it is handed and answers its own public half. The private half is
// generated here and never leaves the process.
type testRing struct {
	key     ed25519.PrivateKey
	version string

	failSign  error
	signCalls int
}

func (r *testRing) AsymmetricSign(_ context.Context, req *kmspb.AsymmetricSignRequest, _ ...gax.CallOption) (*kmspb.AsymmetricSignResponse, error) {
	r.signCalls++
	if r.failSign != nil {
		return nil, r.failSign
	}
	if req.GetName() != r.version {
		return nil, fmt.Errorf("the signer was asked to sign under %q", req.GetName())
	}
	if req.GetDataCrc32C() == nil || req.GetDataCrc32C().Value != int64(crc32.Checksum(req.GetData(), commandCRC)) {
		return nil, errors.New("the request carried no CRC32C of the bytes it asked to sign")
	}
	signature := ed25519.Sign(r.key, req.GetData())
	return &kmspb.AsymmetricSignResponse{
		Name:               r.version,
		Signature:          signature,
		SignatureCrc32C:    wrapperspb.Int64(int64(crc32.Checksum(signature, commandCRC))),
		VerifiedDataCrc32C: true,
	}, nil
}

func (r *testRing) GetPublicKey(_ context.Context, req *kmspb.GetPublicKeyRequest, _ ...gax.CallOption) (*kmspb.PublicKey, error) {
	if req.GetName() != r.version {
		return nil, fmt.Errorf("the public half of %q was asked for", req.GetName())
	}
	encoded, err := x509.MarshalPKIXPublicKey(r.key.Public())
	if err != nil {
		return nil, err
	}
	document := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded}))
	return &kmspb.PublicKey{
		Name:      r.version,
		Algorithm: kmspb.CryptoKeyVersion_EC_SIGN_ED25519,
		Pem:       document,
		PemCrc32C: wrapperspb.Int64(int64(crc32.Checksum([]byte(document), commandCRC))),
	}, nil
}

// commandFixture is one command about to run: its key ring, the JWKS file it is
// given, and the destination object it writes.
type commandFixture struct {
	ring        *testRing
	jwksPath    string
	destination string
	object      string
}

func newCommandFixture(t *testing.T) *commandFixture {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating the test root key: %v", err)
	}
	dir := t.TempDir()
	jwksPath := filepath.Join(dir, "jwks.json")
	writeFile(t, jwksPath, commandJWKS(t, commandJWKSKID))
	// A file bucket is a directory, and this one is the destination a real
	// bucket stands in for: it has to be there before the command opens it.
	if err := os.MkdirAll(filepath.Join(dir, "bucket"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &commandFixture{
		ring:        &testRing{key: private, version: commandRootVersion},
		jwksPath:    jwksPath,
		destination: "file://" + filepath.Join(dir, "bucket", "keyset.jws"),
		object:      filepath.Join(dir, "bucket", "keyset.jws"),
	}
}

// args is a full, correct invocation: every flag a required value, nothing left
// to a default.
func (f *commandFixture) args(extra ...string) []string {
	args := []string{
		"-key-version", commandRootVersion,
		"-root-kid", commandRootKID,
		"-jwks", f.jwksPath,
		"-seq", "1",
		"-iat", "2026-11-24T09:00:00Z",
		"-out", f.destination,
	}
	return append(args, extra...)
}

// open answers the ring this fixture holds whatever the command asked to open.
func (f *commandFixture) open(context.Context) (identitysvc.RootKeyRing, func() error, error) {
	return f.ring, func() error { return nil }, nil
}

func TestKeysetSignCommandRefusesACodingAgentsSession(t *testing.T) {
	fixture := newCommandFixture(t)
	for _, marker := range agentenv.AgentMarkers {
		t.Run(marker, func(t *testing.T) {
			var out strings.Builder
			err := run(fixture.args(), &out, environment{getenv: envWith(marker, "1"), terminal: true}, fixture.open)
			if err == nil {
				t.Fatalf("%s is set and the command signed a key set anyway", marker)
			}
			if !strings.Contains(err.Error(), "coding agent's session") || !strings.Contains(err.Error(), marker) {
				t.Fatalf("the refusal names neither the fence nor %s: %v", marker, err)
			}
			if fixture.ring.signCalls != 0 {
				t.Fatalf("the fence runs after the reading: the root key was asked to sign %d time(s)", fixture.ring.signCalls)
			}
			if _, err := os.Stat(fixture.object); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a refused run wrote %s", fixture.object)
			}
			if out.Len() != 0 {
				t.Fatalf("a refused run printed %q", out.String())
			}
		})
	}
	// The IDE extension's marker is not an agent when a person is typing in
	// their own IDE terminal, which is what the terminal flag decides.
	var out strings.Builder
	err := run(nil, &out, environment{getenv: envWith(agentenv.IDEMarker, "1"), terminal: true}, fixture.open)
	if err != nil && strings.Contains(err.Error(), "coding agent's session") {
		t.Fatalf("a person's own IDE terminal was refused as an agent's session: %v", err)
	}
	if err := run(nil, &out, environment{getenv: envWith(agentenv.IDEMarker, "1"), terminal: false}, fixture.open); err == nil {
		t.Fatal("the IDE marker with no terminal signed a key set")
	}
}

func TestKeysetSignCommandAsksForNothing(t *testing.T) {
	fixture := newCommandFixture(t)
	// With no flags at all: every value it needs is missing, and it names each
	// one rather than waiting for a terminal that is not there.
	var out strings.Builder
	err := run(nil, &out, environment{getenv: envWith("", ""), terminal: false}, fixture.open)
	if err == nil {
		t.Fatal("the command ran with no flags")
	}
	for _, flag := range []string{"-key-version", "-root-kid", "-jwks", "-out"} {
		if !strings.Contains(err.Error(), flag) {
			t.Fatalf("the refusal does not name %s: %v", flag, err)
		}
	}
	if fixture.ring.signCalls != 0 {
		t.Fatalf("the root key was asked to sign %d time(s) with no flags given", fixture.ring.signCalls)
	}
	for name, args := range map[string][]string{
		"a seq of zero":              append(fixture.args(), "-seq", "0"),
		"an iat that is not a stamp": append(fixture.args(), "-iat", "yesterday"),
		"a word that is not a flag":  append([]string{"sign"}, fixture.args()...),
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(args, &out, environment{getenv: envWith("", ""), terminal: false}, fixture.open); err == nil {
				t.Fatalf("%s was accepted", name)
			}
			if fixture.ring.signCalls != 0 {
				t.Fatalf("%s reached the root key", name)
			}
		})
	}
}

func TestKeysetSignCommandSignsTheKeySetItIsGiven(t *testing.T) {
	fixture := newCommandFixture(t)
	var out strings.Builder
	if err := run(fixture.args(), &out, environment{getenv: envWith("", ""), terminal: true}, fixture.open); err != nil {
		t.Fatalf("signing a key set at a person's terminal: %v", err)
	}
	document, err := os.ReadFile(fixture.object)
	if err != nil {
		t.Fatalf("the signed key set was not written to the destination: %v", err)
	}
	segments := strings.Split(string(document), ".")
	if len(segments) != 3 {
		t.Fatalf("the document is not a compact JWS: %d segment(s)", len(segments))
	}
	header := decodeSegment(t, segments[0])
	var gotHeader mobilesession.KeySetHeader
	decodeJSON(t, header, &gotHeader)
	if gotHeader.Alg != mobilesession.KeySetAlg || gotHeader.Typ != mobilesession.KeySetTyp || gotHeader.Kid != commandRootKID {
		t.Fatalf("the header is %+v", gotHeader)
	}
	var keySet mobilesession.KeySet
	decodeJSON(t, decodeSegment(t, segments[1]), &keySet)
	if keySet.Format != mobilesession.KeySetFormat || keySet.Seq != 1 {
		t.Fatalf("the payload is %+v", keySet)
	}
	signedAt, err := time.Parse(time.RFC3339, "2026-11-24T09:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if keySet.Iat != signedAt.Unix() || keySet.Exp != signedAt.Add(180*24*time.Hour).Unix() {
		t.Fatalf("the set is valid %d..%d, and O18 fixes %d..%d",
			keySet.Iat, keySet.Exp, signedAt.Unix(), signedAt.Add(180*24*time.Hour).Unix())
	}
	if len(keySet.JWKS.Keys) != 1 || keySet.JWKS.Keys[0].Kid != commandJWKSKID || keySet.JWKS.Keys[0].Alg != mobilesession.KeySetAlg {
		t.Fatalf("the payload does not carry the key it was given: %+v", keySet.JWKS.Keys)
	}
	signature := decodeSegment(t, segments[2])
	if !ed25519.Verify(fixture.ring.key.Public().(ed25519.PublicKey), []byte(segments[0]+"."+segments[1]), signature) {
		t.Fatal("the root key's signature does not verify over the signing input")
	}
	// What a person reads: the root public half, the dates, and no private
	// material anywhere on the terminal (O7, Check 737).
	printed := out.String()
	expected := base64.RawURLEncoding.EncodeToString(fixture.ring.key.Public().(ed25519.PublicKey))
	if !strings.Contains(printed, expected) {
		t.Fatalf("the root public key was not printed:\n%s", printed)
	}
	for _, forbidden := range []string{"PRIVATE", "private", strings.ToUpper(base64.RawURLEncoding.EncodeToString(fixture.ring.key))} {
		if strings.Contains(printed, forbidden) {
			t.Fatalf("the terminal output carries private material (%q):\n%s", forbidden, printed)
		}
	}
}

func TestKeysetSignCommandRefusesASeqNotAboveTheLastSignedOne(t *testing.T) {
	fixture := newCommandFixture(t)
	var out strings.Builder
	// The first set is signed at 4, and the destination holds it from here on.
	args := fixture.args()
	setSeq(args, 4)
	if err := run(args, &out, environment{getenv: envWith("", ""), terminal: true}, fixture.open); err != nil {
		t.Fatalf("signing the first set: %v", err)
	}
	before, err := os.ReadFile(fixture.object)
	if err != nil {
		t.Fatal(err)
	}
	signed := fixture.ring.signCalls

	second := &testRing{key: fixture.ring.key, version: commandRootVersion}
	fixture.ring = second
	for _, seq := range []int64{4, 3} {
		lowered := fixture.args()
		setSeq(lowered, seq)
		err := run(lowered, &out, environment{getenv: envWith("", ""), terminal: true}, fixture.open)
		if err == nil {
			t.Fatalf("a set at seq %d was signed over the one at 4", seq)
		}
		if !strings.Contains(err.Error(), "not above") {
			t.Fatalf("the refusal does not say the seq is not above the last one: %v", err)
		}
		if second.signCalls != 0 {
			t.Fatalf("the seq %d was refused after the root key was asked to sign", seq)
		}
	}
	after, err := os.ReadFile(fixture.object)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a refused run overwrote the set at the destination")
	}
	if signed == 0 {
		t.Fatal("the first set was never signed, so the refusal above proves nothing")
	}
	// A set above it is signed: the refusal is about the number and not about
	// re-signing at all.
	higher := fixture.args()
	setSeq(higher, 5)
	if err := run(higher, &out, environment{getenv: envWith("", ""), terminal: true}, fixture.open); err != nil {
		t.Fatalf("a set at seq 5 over the set at 4: %v", err)
	}
	if second.signCalls != 1 {
		t.Fatalf("the root key was asked to sign %d times for one set", second.signCalls)
	}
}

// TestKeysetSignCommandRefusesADestinationItCannotRead: the guard the seq floor
// rests on. The set being replaced is read out of the bucket the command is
// about to write to, and a refused read must be a refusal — a 403 the owner
// meets (their identity holds the object's name but not the prefix's listing) is
// not the identity project's first set, and reading it as one signs a set over a
// document this command could not read, which is the one shape the seq refusal
// exists to prevent. Cloud KMS's answer to a missing object under a grant
// conditioned on a prefix is 403 rather than 404, so "absent" and "unreadable"
// are one line apart here and the whole floor hangs on the difference.
func TestKeysetSignCommandRefusesADestinationItCannotRead(t *testing.T) {
	ctx := context.Background()
	const bucketName, object = "fugaro-identity-keyset", "keyset.json"

	refused := gcpfake.NewGCS(t)
	refused.AddBucket(bucketName, 1, nil)
	refused.AnswerMissingAs403(bucketName, object)
	document, err := readPreviousSet(ctx, refused.Bucket(t, bucketName), object)
	if err == nil {
		t.Fatal("a read of the destination this command was refused was taken for the identity project's first set")
	}
	if !errors.Is(err, blobx.ErrForbidden) {
		t.Fatalf("the refusal does not carry what the bucket answered: %v", err)
	}
	if !strings.Contains(err.Error(), "reading the key set being replaced") {
		t.Fatalf("the refusal does not say which read failed: %v", err)
	}
	if document != nil {
		t.Fatalf("a refused read answered %d byte(s)", len(document))
	}

	// The same read where the bucket does answer with absence: the identity
	// project's first set, which the command is meant to sign.
	empty := gcpfake.NewGCS(t)
	empty.AddBucket(bucketName, 1, nil)
	emptyBucket := empty.Bucket(t, bucketName)
	if document, err := readPreviousSet(ctx, emptyBucket, object); err != nil || document != nil {
		t.Fatalf("an absent destination is the first set: %q, %v", document, err)
	}

	// And the read the guard exists for: the document already served, which is
	// what the seq is compared against.
	empty.Put(bucketName, object, []byte("the set that is served"))
	document, err = readPreviousSet(ctx, emptyBucket, object)
	if err != nil {
		t.Fatalf("reading the set at the destination: %v", err)
	}
	if string(document) != "the set that is served" {
		t.Fatalf("the destination answered %q", document)
	}
}

func TestKeysetSignCommandRefusesWhatItCannotSign(t *testing.T) {
	cases := map[string]struct {
		change func(*commandFixture) []string
		wants  string
	}{
		"a JWKS holding a key no verifier accepts": {
			change: func(f *commandFixture) []string {
				writeFile(t, f.jwksPath, `{"keys":[{"kty":"EC","crv":"P-256","kid":"nope","x":"AAAA"}]}`)
				return f.args()
			},
			wants: "OKP/Ed25519",
		},
		"a JWKS with no key at all": {
			change: func(f *commandFixture) []string {
				writeFile(t, f.jwksPath, `{"keys":[]}`)
				return f.args()
			},
			wants: "nothing to sign",
		},
		"a JWKS file that is not there": {
			change: func(f *commandFixture) []string {
				return replaceFlag(f.args(), "-jwks", filepath.Join(t.TempDir(), "missing.json"))
			},
			wants: "reading the JWKS",
		},
		"an empty JWKS file": {
			change: func(f *commandFixture) []string {
				writeFile(t, f.jwksPath, "")
				return f.args()
			},
			wants: "empty",
		},
		"a destination that is not a bucket URL": {
			change: func(f *commandFixture) []string {
				return replaceFlag(f.args(), "-out", "/tmp/keyset.jws")
			},
			wants: "scheme",
		},
		"a destination naming no object": {
			change: func(f *commandFixture) []string {
				return replaceFlag(f.args(), "-out", "gs://fugaro-identity-keyset")
			},
			wants: "names no object",
		},
		"a destination carrying a query": {
			change: func(f *commandFixture) []string {
				return replaceFlag(f.args(), "-out", "gs://fugaro-identity-keyset/keyset.jws?metadata=skip")
			},
			wants: "query",
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newCommandFixture(t)
			args := test.change(fixture)
			err := run(args, &strings.Builder{}, environment{getenv: envWith("", ""), terminal: true}, fixture.open)
			if err == nil {
				t.Fatalf("%s was signed", name)
			}
			if !strings.Contains(err.Error(), test.wants) {
				t.Fatalf("the refusal does not name %q: %v", test.wants, err)
			}
			if fixture.ring.signCalls != 0 {
				t.Fatalf("%s reached the root key", name)
			}
			if _, err := os.Stat(fixture.object); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s wrote %s", name, fixture.object)
			}
		})
	}
}

func TestKeysetSignCommandRefusesAKeyItCannotSignWith(t *testing.T) {
	fixture := newCommandFixture(t)
	fixture.ring.failSign = errors.New("Permission 'cloudkms.cryptoKeyVersions.useToSign' denied")
	var out strings.Builder
	err := run(fixture.args(), &out, environment{getenv: envWith("", ""), terminal: true}, fixture.open)
	if err == nil {
		t.Fatal("a refused signature was accepted")
	}
	if !strings.Contains(err.Error(), "useToSign") {
		t.Fatalf("the refusal does not carry what Cloud KMS said: %v", err)
	}
	if _, err := os.Stat(fixture.object); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused signature still wrote %s", fixture.object)
	}
}

// TestKeysetSignCommandReadsTheDestinationItIsGiven: the destination splits
// into the bucket that is opened and the object that is written, and the object
// is the whole path under the bucket — a nested name is the object and never a
// directory the driver drops.
func TestKeysetSignCommandReadsTheDestinationItIsGiven(t *testing.T) {
	for _, c := range []struct{ destination, bucket, object string }{
		{"gs://fugaro-identity-keyset/keyset.json", "gs://fugaro-identity-keyset/", "keyset.json"},
		{"gs://fugaro-identity-keyset/rotation/keyset.json", "gs://fugaro-identity-keyset/", "rotation/keyset.json"},
		{"mem://bucket/keyset.json", "mem://bucket/", "keyset.json"},
		{"file:///tmp/rehearsal/keyset.json", "file:///tmp/rehearsal", "keyset.json"},
	} {
		t.Run(c.destination, func(t *testing.T) {
			bucket, object, err := splitObjectURL(c.destination)
			if err != nil {
				t.Fatalf("%s was refused: %v", c.destination, err)
			}
			if bucket != c.bucket || object != c.object {
				t.Fatalf("%s reads as bucket %q object %q, want bucket %q object %q", c.destination, bucket, object, c.bucket, c.object)
			}
		})
	}
}

// envWith answers an environment holding one marker and nothing else.
func envWith(marker, value string) func(string) string {
	return func(name string) string {
		if marker != "" && name == marker {
			return value
		}
		return ""
	}
}

// setSeq replaces the seq this invocation signs at.
func setSeq(args []string, seq int64) {
	replaceFlag(args, "-seq", fmt.Sprint(seq))
}

// replaceFlag replaces a flag's value in place and answers the same slice, so a
// case reads as "this invocation, with one value changed".
func replaceFlag(args []string, flag, value string) []string {
	for i, word := range args {
		if word == flag && i+1 < len(args) {
			args[i+1] = value
			return args
		}
	}
	panic("the flag " + flag + " is not in the invocation")
}

// commandJWKS is the identity service's published keys, as A2 serves them: the
// public halves and nothing else.
func commandJWKS(t *testing.T, kid string) string {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a published key: %v", err)
	}
	document, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP",
		"crv": "Ed25519",
		"kid": kid,
		"x":   base64.RawURLEncoding.EncodeToString(public),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(document)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func decodeSegment(t *testing.T, segment string) []byte {
	t.Helper()
	decoded, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("a JWS segment is not base64url: %v", err)
	}
	return decoded
}

func decodeJSON(t *testing.T, document []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(document, into); err != nil {
		t.Fatalf("decoding %s: %v", document, err)
	}
}
