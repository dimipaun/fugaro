package voicesvc

// The tests' shared scaffolding: a token signer and its key source, a database
// that is internal/gcpfake's Realtime Database fake behind the real
// internal/rtdb client, and the two fakes that stand in for state a test needs
// to make fail on purpose.
//
// Everything here is hermetic (Global Constraint 10): the key is generated in
// the test, the certificates are never fetched, the database is the fake in
// this process and there is no network and no home directory anywhere in this
// package's tests.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// testProject is the Firebase project the tests' tokens are minted for. It is
// not a real project and is not shaped like one beyond being a name: the
// verifier only ever compares it.
const testProject = "fugaro-voice-test"

// testKID is the key id the test signer publishes under.
const testKID = "test-key-1"

// testUID is a device uid of the shape a paired device has: `m~` and 22
// unpadded base64url characters. It is a key an operator prints and not a
// credential, and it is not secret-shaped.
const testUID = "m~AAAAAAAAAAAAAAAAAAAAAA"

// testSlug is a project slug of the shape internal/task.Slug writes, which is
// also the key the policy node lives under.
const testSlug = "fugaro-aaaaaaaaaaaaaaaa"

// testRunUID is a run's identity, the shape internal/budget/token mints for a
// job: `r~<slug>~<run>`. It is here to be REFUSED — a run is not a device and
// this service is not one a run may reach.
const testRunUID = "r~my-repo-0123456789abcdef~1a2b3c4d"

// testNow is the clock every test's token is minted against.
var testNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// testKey is a signing key generated in the test, its source, and the tokens
// it mints.
type testKey struct {
	priv *rsa.PrivateKey
	kid  string
	// keyErr, when set, is what Key returns for a kid it does not know: a test
	// sets it to prove that an unreadable key set is state and not a refusal.
	keyErr error
	// keys is how many lookups happened, for the test that a token is not
	// looked up before its shape is checked.
	lookups int
}

func newTestKey(t *testing.T) *testKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating the test key: %v", err)
	}
	return &testKey{priv: priv, kid: testKID}
}

// Key is the KeySource. An unknown kid is ErrUnknownKeyID — a refusal of the
// token — unless the test set keyErr, which stands for the key set being
// unreadable and takes precedence over every lookup.
func (k *testKey) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	k.lookups++
	if k.keyErr != nil {
		return nil, k.keyErr
	}
	if kid != k.kid {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKeyID, "the key id is not one Google publishes")
	}
	return &k.priv.PublicKey, nil
}

// claims is the base of every token a test mints: the standard Firebase ID
// token members for a device. A test overrides what it is testing.
func (k *testKey) claims() map[string]any {
	return map[string]any{
		"iss":     firebaseIssuer(testProject),
		"aud":     testProject,
		"iat":     testNow.Unix(),
		"exp":     testNow.Add(time.Hour).Unix(),
		"sub":     testUID,
		"user_id": testUID,
		"mu":      testUID,
	}
}

// token mints a signed ID token for the claims a test wants. It signs with the
// test key under the test key id unless the test overrode them, and it is the
// only way a test produces a token: a test that wants a bad signature mints a
// good token and edits it, so nothing here signs something this service must
// refuse.
func (k *testKey) token(t *testing.T, claims map[string]any) string {
	t.Helper()
	return k.signed(t, map[string]any{"alg": "RS256", "kid": k.kid, "typ": "JWT"}, claims)
}

// signed builds a compact JWS. It is used by the tests that need a header this
// service must refuse (alg: none, a missing kid) as well as by token.
func (k *testKey) signed(t *testing.T, header, claims map[string]any) string {
	t.Helper()
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshalling the header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling the claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// testAuthenticator returns the verifier over the test key and the fixed clock.
func testAuthenticator(t *testing.T, k *testKey) *Authenticator {
	t.Helper()
	a, err := NewAuthenticator(AuthConfig{
		FirebaseProject: testProject,
		Keys:            k,
		Now:             func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return a
}

// testDB is the installation's Realtime Database in this process: the fake
// behind the real client, so the paths, the escaping and the error kinds the
// hop meets in production are the ones a test exercises.
type testDB struct {
	fake *gcpfake.RTDB
	// Client is the real internal/rtdb client over the fake.
	Client *rtdb.Client
}

func newTestDB(t *testing.T) *testDB {
	t.Helper()
	fake := gcpfake.NewRTDB(t)
	c, err := rtdb.New(fake.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("rtdb.New over the fake: %v", err)
	}
	return &testDB{fake: fake, Client: c}
}

// Set seeds a node.
func (d *testDB) Set(path string, v any) { d.fake.Set(path, v) }

// Delete removes a node, which is what revocation does to a device node
// (D166).
func (d *testDB) Delete(path string) { d.fake.Set(path, nil) }

// DenyRead makes every read of path fail the way a rule refusal does, so a
// test can prove that an unreadable node is state and not an answer.
func (d *testDB) DenyRead(path string) { d.fake.DenyRead(path) }

// failGetter is a Getter whose reads always fail, for the tests that need the
// database itself to be the thing that went wrong.
type failGetter struct{ err error }

func (f failGetter) Get(ctx context.Context, path string, out any) (bool, error) {
	return false, f.err
}

// errBoom is a failure that is not a refusal and not an absent node.
var errBoom = errors.New("the database is unreachable")

// base64Segment encodes one JWT segment the way a compact JWS spells it, for
// the tests that build a token out of parts.
func base64Segment(t *testing.T, s string) string {
	t.Helper()
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

// fakeDevices is a DeviceReader a test decides by hand, for the cases where the
// node's existence is not what is being tested.
type fakeDevices struct {
	live map[string]bool
	err  error
}

func (f fakeDevices) DeviceExists(ctx context.Context, uid string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if !f.live[uid] {
		return false, refuse(ReasonDeviceUnknown, errors.New("voicesvc: no device node exists for this uid"))
	}
	return true, nil
}

// fakeVoices is a VoiceReader a test decides by hand: it decodes raw through
// the real reader, so a test's node is read by the code that reads production's.
type fakeVoices struct {
	raw  []byte
	err  error
	slug string
}

func (f *fakeVoices) Voice(ctx context.Context, slug string) (projectpolicy.Voice, error) {
	f.slug = slug
	if f.err != nil {
		return projectpolicy.LocalVoice(), f.err
	}
	if f.raw == nil {
		return projectpolicy.LocalVoice(), nil
	}
	return projectpolicy.DecodeVoice(f.raw)
}
