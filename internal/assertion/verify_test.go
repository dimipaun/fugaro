package assertion

// P1's tests: one per row of the plan's global table that names P1, plus the
// rows of the acceptance sentence itself. The table's rows here are
//
//   - an assertion replayed (the same jti), with a wrong aud, expired, badly
//     signed, naming an unknown kid, or with its alg swapped to none or to an
//     HMAC-family one;
//   - a JWKS fetch that fails: the cache serves until its bound, the rotation
//     overlap accepts the previous key, and a key past the overlap is refused --
//     a pinned one included, which ends when a valid set at or above the build's
//     seq floor omits it or at its pinned not-after date;
//   - a fetched set with a missing or bad root signature, past its exp, or with
//     a seq below the build's floor or below the highest one the store kept, and
//     a key pinned in the build verifying with no fetch at all;
//   - a jti, spent through the compare-and-set store rather than in process
//     memory, so two verifiers sharing one store refuse the second presentation.
//
// Everything here is hermetic: the keys are drawn by the test, no value is a
// credential, the key-set document is served by a transport of one function,
// and the clock is the test's. The red step of the task is
// TestVerifyRefusesAReplayedAssertion: it failed before this package existed.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/mobilesession"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The test's clock: one instant every case is written against, so an assertion
// that is unexpired here is unexpired for a reason a reader can see.
var testNow = time.Unix(1794960000, 0)

// testIssuer and testKeysURL are this file's placeholders. They are `.invalid`
// hosts -- reserved, and resolvable by nobody -- so a fetch that a test did not
// mean to happen is a transport of the test's own or nothing, never a live call.
const (
	testIssuer   = "https://identity.example.invalid"
	testKeysURL  = "https://identity.example.invalid/keys.jws"
	testRootKID  = "root-test"
	testSignKID  = "signing-test"
	testOtherKID = "signing-other"
)

// b64 is the spelling every key, signature and segment in this contract uses:
// unpadded base64url (RFC 4648 §5).
func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// testKey draws a throwaway Ed25519 key pair, as every signing test in this tree
// does: no private half is written to a file, a fixture or a vector, and the one
// the test signs with dies with the test.
func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("drawing a test key: %v", err)
	}
	return pub, priv
}

// jwkOf is one key as a key set carries it: OKP/Ed25519, unpadded base64url.
func jwkOf(kid string, pub ed25519.PublicKey) mobilesession.JWK {
	return mobilesession.JWK{Kty: "OKP", Crv: "Ed25519", X: b64(pub), Kid: kid, Alg: mobilesession.KeySetAlg, Use: "sig"}
}

// goodClaims is the claim set A1 signs for this project: the eleven claims of
// C1's Claims, unexpired at testNow, for the audience and challenge a caller
// binds a verification to, and with a life of exactly the contract's: an
// assertion that has been alive for ten seconds of AssertionLifetime.
func goodClaims() map[string]any {
	return map[string]any{
		"iss":            testIssuer,
		"aud":            testAudience,
		"sub":            "104729384756102938475",
		"email":          "dimi@example.invalid",
		"email_verified": true,
		"provider":       "google",
		"auth_time":      testNow.Add(-time.Minute).Unix(),
		"jti":            "test-jti-1",
		"iat":            testNow.Add(-10 * time.Second).Unix(),
		"exp":            testNow.Add(-10 * time.Second).Add(mobilesession.AssertionLifetime).Unix(),
		"challenge":      testChallenge,
	}
}

// The binding every case in this file verifies against, unless it is the case
// that is about the binding.
const (
	testAudience  = "https://mobile-example.a-run.invalid"
	testChallenge = "test-challenge-1"
)

// signAssertion builds a compact EdDSA JWT over claims with a test's own key.
// The header carries the alg and the kid, and the signature covers the header
// and the payload exactly as they are serialised here -- which is what makes an
// edit to either of them a signature that does not verify.
func signAssertion(t *testing.T, priv ed25519.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	head, err := json.Marshal(map[string]string{"alg": "EdDSA", "kid": kid, "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshalling the assertion header: %v", err)
	}
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling the assertion claims: %v", err)
	}
	signing := b64(head) + "." + b64(body)
	return signing + "." + b64(ed25519.Sign(priv, []byte(signing)))
}

// keySetDoc is a signed key set exactly as C1 fixes it: the base64url of the
// header and of the payload as this repository's own structs serialise them,
// joined by a dot, signed by the root key with Ed25519.
func keySetDoc(t *testing.T, rootPriv ed25519.PrivateKey, seq int64, exp time.Time, keys ...mobilesession.JWK) string {
	t.Helper()
	head, err := json.Marshal(mobilesession.KeySetHeader{Alg: mobilesession.KeySetAlg, Kid: testRootKID, Typ: mobilesession.KeySetTyp})
	if err != nil {
		t.Fatalf("marshalling the key set's header: %v", err)
	}
	payload, err := json.Marshal(mobilesession.KeySet{
		Format: mobilesession.KeySetFormat,
		Seq:    seq,
		Iat:    testNow.Unix(),
		Exp:    exp.Unix(),
		JWKS:   mobilesession.JWKS{Keys: keys},
	})
	if err != nil {
		t.Fatalf("marshalling the key set's payload: %v", err)
	}
	signing := b64(head) + "." + b64(payload)
	return signing + "." + b64(ed25519.Sign(rootPriv, []byte(signing)))
}

// validUntil is the day a set signed for a test stops being valid: the 180 days
// of O18 measured from the test's clock, so an unexpired set is one that says
// so.
func validUntil() time.Time { return testNow.Add(180 * 24 * time.Hour) }

// keySetServer is the pinned URL's transport: a document a test can replace
// under a running verifier, a count of the reads that happened, and a switch to
// make the read fail. It is a RoundTripper and not a server, so a test that
// reaches the network is a test that fails.
type keySetServer struct {
	mu     sync.Mutex
	body   string
	status int
	calls  int
	fail   bool
	hang   bool
	hold   chan struct{}
}

func (s *keySetServer) set(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body, s.status, s.fail, s.hang = body, http.StatusOK, false, false
}

func (s *keySetServer) down(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = fail
}

// hangUp makes the endpoint accept the request and then say nothing at all,
// until the request's context ends: the socket a fetch can wait on forever is
// the one this test stands in.
func (s *keySetServer) hangUp() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hang = true
}

func (s *keySetServer) reads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// holdNext makes the next request wait until the returned function is called,
// so that a case about what happens *while* a read is under way holds it there
// rather than racing a sleep.
func (s *keySetServer) holdNext() func() {
	release := make(chan struct{})
	s.mu.Lock()
	s.hold = release
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.hold = nil
		s.mu.Unlock()
		close(release)
	}
}

// RoundTrip answers from a snapshot of this server's state, so a request that
// waits holds nothing of the server's: a hung fetch that held this mutex would
// be a test that deadlocked rather than one that measured a hang.
func (s *keySetServer) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.calls++
	hang, hold, fail, status, body := s.hang, s.hold, s.fail, s.status, s.body
	s.mu.Unlock()
	if hang {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}
	if hold != nil {
		select {
		case <-hold:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	if fail {
		return nil, errors.New("the key endpoint is unreachable")
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
		Request:    r,
	}, nil
}

// fetchStarted waits until the endpoint has been reached, so that a case about
// what happens *during* a read can be sure one is under way.
func (s *keySetServer) fetchStarted(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.reads() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the key endpoint was never reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func (s *keySetServer) client() *http.Client { return &http.Client{Transport: s} }

// memoryClaims is C1's ClaimStore as a test needs it: a value is claimed once,
// and the life it was claimed for is kept so a case can say how long a jti was
// remembered. It is deliberately shared between verifiers in the replay case --
// the store, and not the verifier, is what makes a jti single-use.
type memoryClaims struct {
	mu   sync.Mutex
	seen map[string]bool
	ttl  time.Duration
	fail bool
}

func newMemoryClaims() *memoryClaims { return &memoryClaims{seen: map[string]bool{}} }

func (m *memoryClaims) Claim(_ context.Context, namespace, key string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return false, errors.New("the store is down")
	}
	m.ttl = ttl
	id := namespace + "\x00" + key
	if m.seen[id] {
		return false, nil
	}
	m.seen[id] = true
	return true, nil
}

// memorySeq is the highest key-set sequence a verifier has kept, and false for
// a set at or below it (the answer that refuses a stale document).
type memorySeq struct {
	mu   sync.Mutex
	high int64
	fail bool
}

func (m *memorySeq) Observe(_ context.Context, seq int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return false, errors.New("the store is down")
	}
	if seq < m.high {
		return false, nil
	}
	m.high = seq
	return true, nil
}

// pins builds the build's trust anchors for a test: one root, one floor, and
// whatever signing keys the case is about.
func pins(root ed25519.PublicKey, floor int64, keys ...PinnedKey) Pins {
	return Pins{
		Issuer:    testIssuer,
		JWKSURL:   testKeysURL,
		RootKeyID: testRootKID,
		RootKey:   root,
		Keys:      keys,
		SeqFloor:  floor,
	}
}

// pinned is one signing key as a build pin: the key, and the day it stops being
// accepted.
func pinned(kid string, pub ed25519.PublicKey) PinnedKey {
	return PinnedKey{KID: kid, Key: pub, NotAfter: testNow.Add(200 * 24 * time.Hour)}
}

// newTestVerifier is the verifier every case starts from: the pins it is given,
// the endpoint's transport, a claim store and a sequence store of the test's,
// and the test's clock.
func newTestVerifier(t *testing.T, p Pins, srv *keySetServer, claims mobilesession.ClaimStore, seq SeqFloorStore) *Verifier {
	t.Helper()
	v, err := New(claims,
		WithPins(p),
		WithHTTPClient(srv.client()),
		WithClock(func() time.Time { return testNow }),
		WithSeqFloorStore(seq),
	)
	if err != nil {
		t.Fatalf("building the verifier: %v", err)
	}
	return v
}

// refusalField returns the field a refusal names, whichever of the two typed
// refusals it is, and fails when the error is not a typed refusal at all. Every
// case below goes through it, so "every refusal is a typed error naming the
// field" is not a claim one test makes about itself.
func refusalField(t *testing.T, err error) string {
	t.Helper()
	var r *Refusal
	if errors.As(err, &r) {
		if r.Field == "" || r.Reason == "" {
			t.Fatalf("a Refusal names the field %q and the reason %q: a refusal that names neither is a sentence nobody can act on", r.Field, r.Reason)
		}
		return r.Field
	}
	var k *KeySetRefusal
	if errors.As(err, &k) {
		if k.Field == "" || k.Reason == "" {
			t.Fatalf("a KeySetRefusal names the field %q and the reason %q", k.Field, k.Reason)
		}
		return k.Field
	}
	t.Fatalf("the refusal is %v (%T), which is not a typed refusal naming a field: P1's acceptance is that every refusal is one", err, err)
	return ""
}

// verify is the call every case makes, with the file's own binding.
func verify(t *testing.T, v *Verifier, assertion string) (mobilesession.Claims, error) {
	t.Helper()
	return v.Verify(context.Background(), assertion, mobilesession.Binding{Audience: testAudience, Challenge: testChallenge})
}

// accepted is the happy path, written once: a fresh root and signing key, the
// signing key pinned in the build, and an assertion that verifies with no fetch
// at all -- which is O7's "a key pinned in the build verifies with no fetch".
func TestVerifyAcceptsAPinnedKeyWithNoFetch(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub)))

	v := newTestVerifier(t, pins(rootPub, 7, pinned(testSignKID, signPub)), srv, newMemoryClaims(), &memorySeq{})
	claims, err := verify(t, v, signAssertion(t, signPriv, testSignKID, goodClaims()))
	if err != nil {
		t.Fatalf("the verifier refused an assertion signed by a pinned key: %v", err)
	}
	if claims.Email != "dimi@example.invalid" || claims.Subject != "104729384756102938475" || claims.Provider != "google" {
		t.Errorf("the verifier answered %+v, want the claims the assertion carried", claims)
	}
	if got := srv.reads(); got != 0 {
		t.Errorf("the verifier read the key endpoint %d time(s) for an assertion signed by a pinned key: O7 pins the key so that no fetch is needed, and a fetch nobody needed is the dependency the pin exists to remove", got)
	}
}

// The task's red step: the same assertion twice. The second presentation must be
// refused, and it is the store that refuses it -- two verifiers that share one
// store are the several instances of F39, and process memory would have let the
// second one through.
func TestVerifyRefusesAReplayedAssertion(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub)))

	store := newMemoryClaims()
	p := pins(rootPub, 7, pinned(testSignKID, signPub))
	assertion := signAssertion(t, signPriv, testSignKID, goodClaims())

	first := newTestVerifier(t, p, srv, store, &memorySeq{})
	if _, err := verify(t, first, assertion); err != nil {
		t.Fatalf("the first presentation of a fresh assertion was refused: %v", err)
	}
	second := newTestVerifier(t, p, srv, store, &memorySeq{})
	_, err := verify(t, second, assertion)
	if err == nil {
		t.Fatal("the second presentation of one assertion was accepted: a jti is single-use through C1's store, and a verifier that remembers it in its own process admits a replay on the instance next door")
	}
	if got := refusalField(t, err); got != "jti" {
		t.Errorf("the replay was refused naming %q, want \"jti\": the refusal names the field that failed, and the field here is the assertion's own id", got)
	}

	// The life the jti is remembered for: the assertion's own life plus the
	// skew, so a store that forgot at exp would admit the replay the moment the
	// assertion lapsed -- and a store that remembered for a day would be a table
	// of every id every caller ever sent.
	fresh := goodClaims()
	want := time.Unix(fresh["exp"].(int64), 0).Sub(testNow) + ClockSkew
	if store.ttl != want {
		t.Errorf("the jti was claimed for %s, want %s (the assertion's own life plus the skew)", store.ttl, want)
	}
}

// A different jti is a different assertion, and the store must not refuse it:
// the check above is a replay rule, not "one assertion per process".
func TestVerifyAcceptsASecondAssertionWithAnotherJTI(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub)))
	v := newTestVerifier(t, pins(rootPub, 7, pinned(testSignKID, signPub)), srv, newMemoryClaims(), &memorySeq{})

	for _, jti := range []string{"test-jti-1", "test-jti-2"} {
		claims := goodClaims()
		claims["jti"] = jti
		if _, err := verify(t, v, signAssertion(t, signPriv, testSignKID, claims)); err != nil {
			t.Fatalf("the assertion with jti %q was refused: %v", jti, err)
		}
	}
}

// Every refusal P1 makes, one case each, through one harness: the case says
// what is wrong, the field the refusal must name, and -- where a value could
// have been repeated -- that it was not. This is the plan's table rows for P1
// and the acceptance's own list of checks.
func TestVerifyRefusesEveryRowOfTheTable(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	otherPub, _ := testKey(t)
	set := keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub), jwkOf(testOtherKID, otherPub))

	cases := []struct {
		name  string
		field string
		// assertion is built from goodClaims with this edit; an empty mutator
		// means the case carries its own document.
		mutate func(c map[string]any)
		raw    func(t *testing.T, v *Verifier) string
		bind   *mobilesession.Binding
		why    string
	}{
		{
			name: "a wrong audience", field: "aud",
			mutate: func(c map[string]any) { c["aud"] = "https://elsewhere.a-run.invalid" },
			why:    "an assertion minted for another project is worthless here, which is the whole reason aud is a claim (F39)",
		},
		{
			name: "a wrong challenge", field: "challenge",
			mutate: func(c map[string]any) { c["challenge"] = "test-challenge-2" },
			why:    "the challenge is what binds the assertion to this pairing or this restore, so one bound elsewhere is refused even inside its two minutes",
		},
		{
			name: "a wrong issuer", field: "iss",
			mutate: func(c map[string]any) { c["iss"] = "https://elsewhere.example.invalid" },
			why:    "the issuer is pinned in the build (O7): a pin the caller can pass in the assertion is no pin at all",
		},
		{
			name: "an expired assertion", field: "exp",
			mutate: func(c map[string]any) {
				c["iat"] = testNow.Add(-time.Hour).Unix()
				c["exp"] = testNow.Add(-30 * time.Minute).Unix()
			},
			why: "an assertion past its life is refused, and the skew does not reach half an hour back",
		},
		{
			name: "an assertion issued in the future", field: "iat",
			mutate: func(c map[string]any) {
				c["iat"] = testNow.Add(time.Hour).Unix()
				c["exp"] = testNow.Add(2 * time.Hour).Unix()
			},
			why: "a clock far ahead of ours is a signer we cannot bound, so the window is bounded on both sides",
		},
		{
			name: "an expiry that is not after its issue", field: "exp",
			mutate: func(c map[string]any) { c["exp"] = c["iat"].(int64) },
			why:    "a zero-length assertion is not one, and reading it as an unexpired one would be reading two claims as one",
		},
		{
			name: "a life longer than the contract allows", field: "exp",
			mutate: func(c map[string]any) {
				c["iat"] = testNow.Unix()
				c["exp"] = testNow.Add(mobilesession.AssertionLifetime + time.Second).Unix()
			},
			why: "the contract gives an assertion a two-minute life, and a signer that says its assertion lives longer is not minting this contract's document",
		},
		{
			name: "no jti at all", field: "jti",
			mutate: func(c map[string]any) { delete(c, "jti") },
			why:    "an assertion with no id cannot be spent once, so it is refused rather than accepted with single use impossible",
		},
		{
			name: "no subject", field: "sub",
			mutate: func(c map[string]any) { delete(c, "sub") },
			why:    "the provider's stable id is what a member record keeps (F9), and an assertion without one identifies nobody",
		},
		{
			name: "no email", field: "email",
			mutate: func(c map[string]any) { c["email"] = "" },
			why:    "the address is what the invite, the roster and the member record are keyed by",
		},
		{
			name: "no provider", field: "provider",
			mutate: func(c map[string]any) { delete(c, "provider") },
			why:    "the accepted-provider policy has nothing to check without it",
		},
		{
			name: "email_verified of the wrong type", field: "email_verified",
			mutate: func(c map[string]any) { c["email_verified"] = "true" },
			why:    "a claim read at its own type: a string that reads like a bool is a document whose claims are not the ones this verifier models",
		},
		{
			name: "a claim this verifier does not read", field: "claims",
			mutate: func(c map[string]any) { c["nbf"] = testNow.Add(-time.Hour).Unix() },
			why:    "the claim set is closed: a claim read by nobody is a claim a signer could put anything in, and the next reader would have to decide what it means",
		},
		{
			name: "an audience the caller did not ask for", field: "aud",
			bind: &mobilesession.Binding{Challenge: testChallenge},
			why:  "a caller that asked to verify nothing is refused rather than answered: an expectation read off the thing it judges is no check",
		},
		{
			name: "a challenge the caller did not ask for", field: "challenge",
			bind: &mobilesession.Binding{Audience: testAudience},
			why:  "the same, for the challenge: a binding with no challenge admits an assertion bound to another pairing",
		},
		{
			name: "an alg that is not EdDSA", field: "alg",
			raw: func(t *testing.T, v *Verifier) string {
				head, _ := json.Marshal(map[string]string{"alg": "none", "kid": testSignKID, "typ": "JWT"})
				body, _ := json.Marshal(goodClaims())
				return b64(head) + "." + b64(body) + "."
			},
			why: "alg=none is a signature nobody signed, and it is the first thing every verifier has to refuse",
		},
		{
			name: "an HMAC-family alg", field: "alg",
			raw: func(t *testing.T, v *Verifier) string {
				head, _ := json.Marshal(map[string]string{"alg": "HS256", "kid": testSignKID, "typ": "JWT"})
				body, _ := json.Marshal(goodClaims())
				return b64(head) + "." + b64(body) + "." + b64([]byte("not-a-signature"))
			},
			why: "an HMAC alg would verify with the public key as a shared secret, which is the classic confusion attack",
		},
		{
			name: "a kid the pinned set and the build do not hold", field: "kid",
			raw: func(t *testing.T, v *Verifier) string {
				return signAssertion(t, signPriv, "signing-unknown", goodClaims())
			},
			why: "a kid is a hint about which key to look for and never evidence, so one nobody publishes is refused rather than guessed at",
		},
		{
			name: "a bad signature", field: "signature",
			raw: func(t *testing.T, v *Verifier) string {
				// The right key id and the right claims, signed by a key that is
				// not the one the kid names.
				_, wrongKey := testKey(t)
				return signAssertion(t, wrongKey, testSignKID, goodClaims())
			},
			why: "the signature is the whole of what makes the claims readable",
		},
		{
			name: "a tampered payload", field: "signature",
			raw: func(t *testing.T, v *Verifier) string {
				raw := signAssertion(t, signPriv, testSignKID, goodClaims())
				parts := strings.Split(raw, ".")
				body, _ := json.Marshal(goodClaims())
				// One byte changed in the payload: the signature covers the
				// payload as it arrived, so it stops verifying.
				tampered := strings.Replace(string(body), "104729384756102938475", "104729384756102938476", 1)
				return parts[0] + "." + b64([]byte(tampered)) + "." + parts[2]
			},
			why: "a payload edited after signing is a signature that does not verify, which is what bounds the claims",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &keySetServer{}
			srv.set(set)
			v := newTestVerifier(t, pins(rootPub, 7, pinned(testSignKID, signPub)), srv, newMemoryClaims(), &memorySeq{})
			var raw string
			switch {
			case tc.raw != nil:
				raw = tc.raw(t, v)
			default:
				claims := goodClaims()
				if tc.mutate != nil {
					tc.mutate(claims)
				}
				raw = signAssertion(t, signPriv, testSignKID, claims)
			}
			want := mobilesession.Binding{Audience: testAudience, Challenge: testChallenge}
			if tc.bind != nil {
				want = *tc.bind
			}
			got, err := v.Verify(context.Background(), raw, want)
			if err == nil {
				t.Fatalf("%s was accepted, and it must not be: %s", tc.name, tc.why)
			}
			if got != (mobilesession.Claims{}) {
				t.Errorf("a refused verification answered %+v: a refusal is never a partial claim set, so a caller that ignored the error still holds nothing", got)
			}
			if field := refusalField(t, err); field != tc.field {
				t.Errorf("%s was refused naming %q, want %q: the refusal names the field that failed, which is what makes it actionable without reading the assertion", tc.name, field, tc.field)
			}
			if strings.Contains(err.Error(), raw) || (len(raw) > 32 && strings.Contains(err.Error(), raw[:32])) {
				t.Errorf("%s was refused with a message carrying the assertion: a refusal reaches a log line and an answer body, and the assertion is a document a stranger sent", tc.name)
			}
		})
	}
}

// The finding behind the table's last row, at the size that makes it bite: a
// signer that dates its assertion three hundred years ahead. The jti is
// remembered for the assertion's life plus the skew, and that life is read as
// exp minus now -- so an exp far enough ahead for the subtraction to saturate is
// a jti forgotten after one minute while the assertion verifies for centuries,
// which is a replay the store never gets the chance to refuse. The refusal names
// exp, and the jti is never claimed: an assertion refused for its life must not
// spend the id of a claim set that was never answered.
func TestVerifyRefusesAnAssertionThatOutlivesTheContract(t *testing.T) {
	rootPub, _ := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	claims := goodClaims()
	claims["iat"] = testNow.Unix()
	claims["exp"] = testNow.AddDate(300, 0, 0).Unix()
	assertion := signAssertion(t, signPriv, testSignKID, claims)

	store := newMemoryClaims()
	v := newTestVerifier(t, pins(rootPub, 7, pinned(testSignKID, signPub)), srv, store, &memorySeq{})
	_, err := verify(t, v, assertion)
	if err == nil {
		t.Fatal("an assertion dated three hundred years ahead was accepted: the contract's life is two minutes, and the id of an assertion that outlives the store's memory of it is an id that can be replayed for as long as the assertion verifies")
	}
	if field := refusalField(t, err); field != "exp" {
		t.Errorf("the refusal names %q, want \"exp\"", field)
	}
	if store.ttl != 0 {
		t.Errorf("the jti was claimed for %s, and an assertion refused for its life must not spend one", store.ttl)
	}
	if got := srv.reads(); got != 0 {
		t.Errorf("the assertion was refused after %d read(s) of the key endpoint, want 0: the key is pinned and the claims are what failed", got)
	}
}

// The shapes a compact JWT can arrive in that are not one: no segments, too
// many, an empty one, a payload that is not JSON, a signature that is not
// base64url or carries padding, and a token longer than any assertion.
func TestVerifyRefusesWhatIsNotACompactAssertion(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub)))
	good := signAssertion(t, signPriv, testSignKID, goodClaims())
	parts := strings.Split(good, ".")

	long := strings.Repeat("a", maxAssertionBytes+1) + "." + strings.Repeat("b", 8) + ".c"
	for _, tc := range []struct{ name, raw string }{
		{"an empty string", ""},
		{"no dots at all", "not-an-assertion"},
		{"two segments", parts[0] + "." + parts[1]},
		{"four segments", good + ".extra"},
		{"an empty header", "." + parts[1] + "." + parts[2]},
		{"a payload that is not JSON", parts[0] + "." + b64([]byte("not json")) + "." + parts[2]},
		{"a signature that is not base64url", parts[0] + "." + parts[1] + ".!!!not-base64!!!"},
		{"a padded signature", parts[0] + "." + parts[1] + "." + parts[2] + "=="},
		{"a token past any assertion's size", long},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestVerifier(t, pins(rootPub, 7, pinned(testSignKID, signPub)), srv, newMemoryClaims(), &memorySeq{})
			if _, err := verify(t, v, tc.raw); err == nil {
				t.Fatalf("the verifier accepted %s as an assertion: %q", tc.name, tc.raw)
			} else if field := refusalField(t, err); field == "" {
				t.Fatalf("%s was refused naming no field: %v", tc.name, err)
			}
		})
	}
}

// A header carrying a field this verifier does not model is refused, and that
// is a security property rather than tidiness: an assertion whose header says
// where to find its key ("jku", "x5u") would be an assertion that brings its own
// key, which is the one thing the pin exists to stop.
func TestVerifyRefusesAHeaderItDoesNotModel(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, _ := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub)))
	v := newTestVerifier(t, pins(rootPub, 7, pinned(testSignKID, signPub)), srv, newMemoryClaims(), &memorySeq{})

	head, _ := json.Marshal(map[string]string{"alg": "EdDSA", "kid": testSignKID, "typ": "JWT", "jku": "https://evil.invalid/keys"})
	body, _ := json.Marshal(goodClaims())
	raw := b64(head) + "." + b64(body) + "." + b64([]byte("not-a-signature"))
	if _, err := verify(t, v, raw); err == nil {
		t.Fatal("an assertion whose header names a key URL was accepted: a header this verifier does not model is an instruction it would otherwise follow")
	} else if field := refusalField(t, err); field != "header" {
		t.Errorf("the refusal names %q, want \"header\"", field)
	}
}

// The other half of O7: a kid that is not pinned is taken from a fetched set,
// and the fetch is one read of the pinned URL per cache life.
func TestVerifyFetchesTheKeySetForAKidTheBuildDoesNotPin(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub)))
	// The build pins a key that is not the one signing here, so this path is the
	// fetched one and nothing about it is a pin.
	unrelatedPub, _ := testKey(t)
	v := newTestVerifier(t, pins(rootPub, 7, pinned("signing-build", unrelatedPub)), srv, newMemoryClaims(), &memorySeq{})

	if _, err := verify(t, v, signAssertion(t, signPriv, testSignKID, goodClaims())); err != nil {
		t.Fatalf("an assertion signed by a fetched, root-signed key was refused: %v", err)
	}
	if got := srv.reads(); got != 1 {
		t.Errorf("the verifier read the key endpoint %d time(s), want 1: a clean cache costs one read", got)
	}
	// A second assertion is answered from the cache: the set is valid for 180
	// days (O18) and the cache is bounded by the set's own life.
	claims := goodClaims()
	claims["jti"] = "test-jti-2"
	if _, err := verify(t, v, signAssertion(t, signPriv, testSignKID, claims)); err != nil {
		t.Fatalf("the second assertion was refused: %v", err)
	}
	if got := srv.reads(); got != 1 {
		t.Errorf("the verifier read the key endpoint %d times for two assertions, want 1: the set's own exp bounds the cache", got)
	}
}

// A pinned key ends at its not-after date, and it ends when a valid set at or
// above the build's floor omits it. Both halves of the round-5 resolution,
// and the second one is why a rotation needs no release.
func TestVerifyRefusesAPinnedKeyAValidSetOmits(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	keptPub, keptPriv := testKey(t)
	gonePub, gonePriv := testKey(t)
	srv := &keySetServer{}
	// The served set holds the key rotation left behind and not the one the build
	// pinned: the owner has rotated it out, so the pin is dead.
	srv.set(keySetDoc(t, rootPriv, 8, validUntil(), jwkOf("signing-new", keptPub)))
	p := pins(rootPub, 7, pinned(testSignKID, gonePub))
	v := newTestVerifier(t, p, srv, newMemoryClaims(), &memorySeq{})

	// The set is read by an assertion the build does not pin, which is what makes
	// the omitting set known here at all.
	if _, err := verify(t, v, signAssertion(t, keptPriv, "signing-new", goodClaims())); err != nil {
		t.Fatalf("an assertion signed by the served set's key was refused: %v", err)
	}
	if _, err := verify(t, v, signAssertion(t, gonePriv, testSignKID, goodClaims())); err == nil {
		t.Fatal("the pinned key was accepted after a valid set at or above the floor omitted it: a pinned key ends when the project rotates it out, and that end is what makes a lost root and a compromised key both recoverable without waiting for the release that pins the next one")
	} else if field := refusalField(t, err); field != "kid" {
		t.Errorf("the refusal names %q, want \"kid\"", field)
	}
}

// The other half: a pinned key whose not-after date has passed is refused with
// no set in sight, which is what a build that stopped being maintained looks
// like.
func TestVerifyRefusesAPinnedKeyPastItsNotAfter(t *testing.T) {
	rootPub, _ := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	v := newTestVerifier(t, pins(rootPub, 7, PinnedKey{KID: testSignKID, Key: signPub, NotAfter: testNow.Add(-time.Second)}), srv, newMemoryClaims(), &memorySeq{})

	if _, err := verify(t, v, signAssertion(t, signPriv, testSignKID, goodClaims())); err == nil {
		t.Fatal("a pinned key past its not-after date was accepted: the date is the end of the rotation overlap, and a build that never learns about the next rotation is a build running on a withdrawn key")
	} else if field := refusalField(t, err); field != "kid" {
		t.Errorf("the refusal names %q, want \"kid\"", field)
	}
}

// A kid the set does not hold is one more read and then a refusal: a token
// naming a kid the cached set lacks is the signal that the project rotated, and
// without the extra read a rotation would refuse every honest assertion until
// the cache lapsed. The read is rate-limited, so a caller naming random kids
// cannot make this front door read the endpoint in a loop.
func TestVerifyReadsAgainForARotatedKeyIDThenRateLimits(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	oldPub, oldPriv := testKey(t)
	newPub, newPriv := testKey(t)
	srv := &keySetServer{}
	buildPub, _ := testKey(t)
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf("signing-old", oldPub)))
	v := newTestVerifier(t, pins(rootPub, 7, pinned("signing-build", buildPub)), srv, newMemoryClaims(), &memorySeq{})

	// The set is read once, and what this process holds from here on is the one
	// the project published before the rotation below.
	oldClaims := goodClaims()
	oldClaims["jti"] = "test-jti-old"
	if _, err := verify(t, v, signAssertion(t, oldPriv, "signing-old", oldClaims)); err != nil {
		t.Fatalf("the first assertion was refused: %v", err)
	}

	// The overlap (90 days with 30 days of overlap, O4): the published set now
	// holds the current key and the previous one, and the previous key keeps
	// verifying -- from the set already held, because the rotation is authority
	// over the keys and not over the assertions signed a moment ago.
	srv.set(keySetDoc(t, rootPriv, 8, validUntil(), jwkOf("signing-new", newPub), jwkOf("signing-old", oldPub)))
	overlap := goodClaims()
	overlap["jti"] = "test-jti-overlap"
	if _, err := verify(t, v, signAssertion(t, oldPriv, "signing-old", overlap)); err != nil {
		t.Fatalf("the previous key was refused during the overlap: %v", err)
	}
	before := srv.reads()
	claims := goodClaims()
	claims["jti"] = "test-jti-2"
	if _, err := verify(t, v, signAssertion(t, newPriv, "signing-new", claims)); err != nil {
		t.Fatalf("an assertion signed by a newly published key was refused: %v", err)
	}
	if srv.reads() <= before {
		t.Error("the verifier did not read the set again for a kid the cached one lacked: a rotation would otherwise refuse every honest assertion until the cache lapsed")
	}
	// Now a kid nobody publishes, twice: the second must be answered without
	// another read.
	claims["jti"] = "test-jti-3"
	_, _ = verify(t, v, signAssertion(t, oldPriv, "signing-nobody", claims))
	rinsed := srv.reads()
	claims["jti"] = "test-jti-4"
	_, err := verify(t, v, signAssertion(t, oldPriv, "signing-nobody", claims))
	if err == nil {
		t.Fatal("an assertion naming a kid nobody publishes was accepted")
	}
	if refusalField(t, err) != "kid" {
		t.Errorf("the refusal names %q, want \"kid\"", refusalField(t, err))
	}
	if srv.reads() != rinsed {
		t.Errorf("the verifier read the key endpoint again for a second unknown kid (%d reads, was %d): the extra read is rate-limited so that a caller naming random kids cannot make this service read the endpoint in a loop", srv.reads(), rinsed)
	}
}

// The cache's bound is what turns an outage into a delay rather than an outage
// of the project's own (the plan's row): while the fetch fails, the set the
// verifier holds keeps answering, and it stops answering at the bound.
func TestVerifyServesTheCacheWhileTheEndpointIsDownThenStops(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, testNow.Add(time.Minute), jwkOf(testSignKID, signPub)))

	// A clock the case can move: the set lapses, the endpoint is down, and the
	// verifier answers from what it holds.
	now := testNow
	var mu sync.Mutex
	outageBuildPub, _ := testKey(t)
	v, err := New(newMemoryClaims(),
		WithPins(pins(rootPub, 7, pinned("signing-build", outageBuildPub))),
		WithHTTPClient(srv.client()),
		WithClock(func() time.Time { mu.Lock(); defer mu.Unlock(); return now }),
		WithSeqFloorStore(&memorySeq{}),
	)
	if err != nil {
		t.Fatalf("building the verifier: %v", err)
	}
	if _, err := verify(t, v, signAssertion(t, signPriv, testSignKID, goodClaims())); err != nil {
		t.Fatalf("the first assertion was refused: %v", err)
	}
	srv.down(true)

	claims := goodClaims()
	claims["jti"] = "test-jti-2"
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	// Just past the set's own exp, with the endpoint down: the held set answers.
	advance(2 * time.Minute)
	if _, err := verify(t, v, signAssertion(t, signPriv, testSignKID, claims)); err != nil {
		t.Fatalf("an assertion was refused while the endpoint was down and the held set was inside its bound: an outage of the identity project's key endpoint must not become an outage of every pairing in the project (I1's row): %v", err)
	}
	// Past the bound: nothing left to serve, so the assertion is refused rather
	// than verified against a set whose validity is long gone.
	advance(KeySetMaxStale + time.Minute)
	claims["jti"] = "test-jti-3"
	if _, err := verify(t, v, signAssertion(t, signPriv, testSignKID, claims)); err == nil {
		t.Fatalf("an assertion was verified against a set more than %s past its own exp: the bound is what keeps a withdrawn key from verifying forever", KeySetMaxStale)
	}
}

// A read is the one thing here that can wait on a stranger's socket, and it
// must not be a thing the rest of the process waits with. A verification signed
// by a pinned key needs no fetch at all (O7), and it must keep needing none
// while another request's fetch is hanging: that socket is the identity
// project's and this pairing is this project's, and neither is the other's
// hostage.
func TestVerifyDoesNotWaitForAFetchInFlight(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	buildPub, _ := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub)))
	v := newTestVerifier(t, pins(rootPub, 7, pinned(testSignKID, signPub), pinned("signing-elsewhere", buildPub)), srv, newMemoryClaims(), &memorySeq{})

	// One verification naming a key id nobody holds, which is what reaches the
	// endpoint -- and the endpoint never answers this one.
	srv.hangUp()
	hanging, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		claims := goodClaims()
		claims["jti"] = "test-jti-hanging"
		_, _ = v.Verify(hanging, signAssertion(t, signPriv, "signing-fetched", claims), mobilesession.Binding{Audience: testAudience, Challenge: testChallenge})
	}()
	srv.fetchStarted(t)

	// The pinned verification, on its own context and behind a guard: if it
	// waits for the socket, this fails here rather than hanging the package.
	answered := make(chan error, 1)
	go func() {
		_, err := verify(t, v, signAssertion(t, signPriv, testSignKID, goodClaims()))
		answered <- err
	}()
	select {
	case err := <-answered:
		if err != nil {
			t.Fatalf("a pinned key's assertion was refused while a fetch hung: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a pinned key's verification waited for a fetch in flight: a read that holds the cache's mutex across a socket makes one hung connection to the identity project's endpoint every assertion in this process stalled behind it, the pinned ones included")
	}
	stop()
	<-done
}

// The other half of the same bound: a fetch that never answers must not be a
// verification that never answers. The bound is the cache's own rather than the
// HTTP client's, so a deployment that configured no client timeout is still a
// deployment whose pairing recovers.
func TestVerifyBoundsAKeySetFetch(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	buildPub, _ := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub)))
	srv.hangUp()
	// The build pins another key, so this assertion is the fetch path.
	v := newTestVerifier(t, pins(rootPub, 7, pinned("signing-elsewhere", buildPub)), srv, newMemoryClaims(), &memorySeq{})
	// The test's own bound rather than keySetFetchTimeout: ten seconds of wall
	// clock in every run of this package, to prove a constant, is ten seconds
	// nobody gets back.
	v.keys.fetchTimeout = 50 * time.Millisecond

	answered := make(chan error, 1)
	go func() {
		_, err := verify(t, v, signAssertion(t, signPriv, testSignKID, goodClaims()))
		answered <- err
	}()
	select {
	case err := <-answered:
		if err == nil {
			t.Fatal("an assertion was accepted from a key set that never arrived")
		}
		if field := refusalField(t, err); field != "document" {
			t.Errorf("the refusal names %q, want \"document\": a fetch that ran out of time is a key set that could not be read", field)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a fetch that never answers was not bounded: a read of the pinned URL has to end on its own timeout, or one hung connection is every verification in this process stuck behind it")
	}
}

// The envelope is what makes a fetched set trustworthy at all: without the
// root's signature, whoever can change what the URL serves can publish keys of
// their own and mint assertions for any project (F38).
func TestVerifyRefusesAKeySetEnvelope(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	impostorPub, impostorPriv := testKey(t)
	signPub, signPriv := testKey(t)
	keys := []mobilesession.JWK{jwkOf(testSignKID, signPub)}
	otherPub, _ := testKey(t)

	// The build pins a key that is not the one signing, so every case below is
	// the fetched path.
	buildPub, _ := testKey(t)
	buildPins := pins(rootPub, 7, pinned("signing-build", buildPub))

	cases := []struct {
		name  string
		field string
		body  string
		why   string
	}{
		{
			name: "signed by another root key", field: "signature",
			body: keySetDoc(t, impostorPriv, 8, validUntil(), keys...),
			why:  "a set the pinned root did not sign is a set a stranger chose",
		},
		{
			name: "with the payload edited after signing", field: "signature",
			body: func() string {
				raw := keySetDoc(t, rootPriv, 8, validUntil(), keys...)
				parts := strings.Split(raw, ".")
				payload, _ := json.Marshal(mobilesession.KeySet{
					Format: mobilesession.KeySetFormat, Seq: 8, Iat: testNow.Unix(), Exp: validUntil().Unix(),
					JWKS: mobilesession.JWKS{Keys: []mobilesession.JWK{jwkOf(testSignKID, impostorPub)}},
				})
				return parts[0] + "." + b64(payload) + "." + parts[2]
			}(),
			why: "the signature covers the payload as it arrived, so a key swapped after signing is a document that does not verify",
		},
		{
			name: "past its exp", field: "exp",
			body: keySetDoc(t, rootPriv, 8, testNow.Add(-time.Minute), keys...),
			why:  "a set is valid 180 days (O18) and one past that is refused: a rotation that never happened is a rotation the project meant to make",
		},
		{
			name: "below the build's seq floor", field: "seq",
			body: keySetDoc(t, rootPriv, 6, validUntil(), keys...),
			why:  "the floor is the sequence of the set the release pinned (P8), and a set below it is a cache serving a document older than the build",
		},
		{
			name: "with the wrong format tag", field: "format",
			body: func() string {
				head, _ := json.Marshal(mobilesession.KeySetHeader{Alg: mobilesession.KeySetAlg, Kid: testRootKID, Typ: "fugaro-key-set+jws"})
				payload, _ := json.Marshal(map[string]any{"format": "something-else", "seq": 8, "iat": testNow.Unix(), "exp": validUntil().Unix(), "jwks": mobilesession.JWKS{Keys: keys}})
				signing := b64(head) + "." + b64(payload)
				return signing + "." + b64(ed25519.Sign(rootPriv, []byte(signing)))
			}(),
			why: "the tag is what tells a key set from any other document the root key signs",
		},
		{
			name: "naming another root key id", field: "kid",
			body: func() string {
				head, _ := json.Marshal(mobilesession.KeySetHeader{Alg: mobilesession.KeySetAlg, Kid: "root-elsewhere", Typ: mobilesession.KeySetTyp})
				payload, _ := json.Marshal(mobilesession.KeySet{Format: mobilesession.KeySetFormat, Seq: 8, Iat: testNow.Unix(), Exp: validUntil().Unix(), JWKS: mobilesession.JWKS{Keys: keys}})
				signing := b64(head) + "." + b64(payload)
				return signing + "." + b64(ed25519.Sign(rootPriv, []byte(signing)))
			}(),
			why: "the header's kid is the root's, and a reader that verified with whatever the header named would verify with a key the document brought",
		},
		{
			name: "with no keys at all", field: "keys",
			body: keySetDoc(t, rootPriv, 8, validUntil()),
			why:  "a set holding no signing key is not a key set",
		},
		{
			name: "with two keys sharing a key id", field: "keys",
			body: keySetDoc(t, rootPriv, 8, validUntil(), jwkOf(testSignKID, signPub), jwkOf(testSignKID, otherPub)),
			why:  "a set that names one key id twice leaves which key signs a question with two answers",
		},
		{
			name: "with a key of another curve", field: "keys",
			body: keySetDoc(t, rootPriv, 8, validUntil(), mobilesession.JWK{Kty: "OKP", Crv: "X25519", X: b64(signPub), Kid: testSignKID, Alg: mobilesession.KeySetAlg}),
			why:  "EdDSA is the only algorithm this contract names, and a key of another curve is a key nothing here can verify with",
		},
		{
			name: "with a key that is not base64url", field: "keys",
			body: keySetDoc(t, rootPriv, 8, validUntil(), mobilesession.JWK{Kty: "OKP", Crv: "Ed25519", X: "not base64url!", Kid: testSignKID, Alg: mobilesession.KeySetAlg}),
			why:  "a key is read strictly rather than leniently: a padded or standard-alphabet spelling is not the key it would decode to",
		},
		{
			name: "with a key carrying no key id", field: "keys",
			body: keySetDoc(t, rootPriv, 8, validUntil(), mobilesession.JWK{Kty: "OKP", Crv: "Ed25519", X: b64(signPub), Alg: mobilesession.KeySetAlg}),
			why:  "a key with no id is one no assertion can name",
		},
		{
			name: "with a field this reader does not model", field: "document",
			body: func() string {
				head, _ := json.Marshal(mobilesession.KeySetHeader{Alg: mobilesession.KeySetAlg, Kid: testRootKID, Typ: mobilesession.KeySetTyp})
				payload, _ := json.Marshal(map[string]any{"format": mobilesession.KeySetFormat, "seq": 8, "iat": testNow.Unix(), "exp": validUntil().Unix(), "jwks": mobilesession.JWKS{Keys: keys}, "note": "hello"})
				signing := b64(head) + "." + b64(payload)
				return signing + "." + b64(ed25519.Sign(rootPriv, []byte(signing)))
			}(),
			why: "the envelope is a wire shape and it decodes strictly, so a field nobody models is a document this reader cannot hold to the contract",
		},
		{
			name: "that is not a document at all", field: "document",
			body: "this is not a key set",
			why:  "an answer from the endpoint that is not a key set is refused rather than parsed hopefully",
		},
		{
			name: "with a signature that is not base64url", field: "signature",
			body: func() string {
				raw := keySetDoc(t, rootPriv, 8, validUntil(), keys...)
				return raw[:strings.LastIndex(raw, ".")+1] + "!!not-base64!!"
			}(),
			why: "the signature is decoded strictly, like every other value in this contract",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &keySetServer{}
			srv.set(tc.body)
			v := newTestVerifier(t, buildPins, srv, newMemoryClaims(), &memorySeq{})
			if _, err := verify(t, v, signAssertion(t, signPriv, testSignKID, goodClaims())); err == nil {
				t.Fatalf("an assertion was accepted from a key set %s: %s", tc.name, tc.why)
			} else if field := refusalField(t, err); field != tc.field {
				t.Errorf("the refusal names %q, want %q: %v", field, tc.field, err)
			}
		})
	}
}

// The sequence is persistent, not per process: the highest one a store kept is
// what refuses a stale document that a cache serves after a newer one was read.
func TestVerifyRefusesAKeySetBelowTheSequenceTheStoreKept(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	oldPub, oldPriv := testKey(t)
	newPub, newPriv := testKey(t)
	srv := &keySetServer{}
	seq := &memorySeq{}

	// One verifier reads the newer set first, which is what raises the store's
	// high-water; the second verifier is the instance next door, sharing the
	// store and holding no memory of its own.
	srv.set(keySetDoc(t, rootPriv, 9, validUntil(), jwkOf("signing-new", newPub)))
	seqBuildPub, _ := testKey(t)
	first := newTestVerifier(t, pins(rootPub, 1, pinned("signing-build", seqBuildPub)), srv, newMemoryClaims(), seq)
	if _, err := verify(t, first, signAssertion(t, newPriv, "signing-new", goodClaims())); err != nil {
		t.Fatalf("the newer set was refused: %v", err)
	}

	srv.set(keySetDoc(t, rootPriv, 8, validUntil(), jwkOf("signing-old", oldPub)))
	second := newTestVerifier(t, pins(rootPub, 1, pinned("signing-build", seqBuildPub)), srv, newMemoryClaims(), seq)
	if _, err := verify(t, second, signAssertion(t, oldPriv, "signing-old", goodClaims())); err == nil {
		t.Fatal("a set below the highest sequence the store kept was accepted: a stale document served by a cache would walk every project back onto older keys, which is what the persistent sequence exists to stop")
	} else if field := refusalField(t, err); field != "seq" {
		t.Errorf("the refusal names %q, want \"seq\"", field)
	}
}

// A store the verifier cannot reach is a refusal and never an acceptance: the
// verifier's own stores are what make a jti single-use and a sequence
// persistent, and one that failed silently would hand back the security
// property it was asked for.
func TestVerifyRefusesWhenAStoreFails(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub)))
	p := pins(rootPub, 7, pinned(testSignKID, signPub))

	t.Run("the claim store", func(t *testing.T) {
		claims := newMemoryClaims()
		claims.fail = true
		v := newTestVerifier(t, p, srv, claims, &memorySeq{})
		_, err := verify(t, v, signAssertion(t, signPriv, testSignKID, goodClaims()))
		if err == nil {
			t.Fatal("an assertion was accepted while the jti could not be claimed: single use is the store's, so a store that failed is an assertion nobody can spend")
		}
		if !errors.Is(err, ErrStore) {
			t.Errorf("the refusal is %v, which does not wrap ErrStore: a caller has to be able to tell a store that failed from an assertion that was refused", err)
		}
	})

	t.Run("the sequence store", func(t *testing.T) {
		seq := &memorySeq{fail: true}
		// The build pins no key, so the fetch path is the one that has to keep the
		// sequence.
		unrelated, _ := testKey(t)
		v := newTestVerifier(t, pins(rootPub, 7, pinned("signing-build", unrelated)), srv, newMemoryClaims(), seq)
		_, err := verify(t, v, signAssertion(t, signPriv, testSignKID, goodClaims()))
		if err == nil {
			t.Fatal("an assertion was accepted while the key set's sequence could not be kept")
		}
		if !errors.Is(err, ErrStore) {
			t.Errorf("the refusal is %v, which does not wrap ErrStore", err)
		}
	})
}

// The verifier the service builds is built the same way every time, and a pins
// set or a store that cannot work is refused where it is built rather than at
// every assertion: a pin that cannot verify anything fails closed on every
// sign-in with a message about the sign-in, which is the failure mode the
// licence key's reader and mcpauth's both refuse at construction.
func TestNewRefusesAPinOrAStoreItCannotUse(t *testing.T) {
	rootPub, _ := testKey(t)
	signPub, _ := testKey(t)
	good := pins(rootPub, 7, pinned(testSignKID, signPub))

	cases := []struct {
		name   string
		store  mobilesession.ClaimStore
		seq    SeqFloorStore
		mutate func(p *Pins)
		why    string
	}{
		{name: "with no claim store at all", store: nil, seq: &memorySeq{}, why: "a verifier that cannot claim a jti cannot make one single-use"},
		{name: "with no sequence store at all", store: newMemoryClaims(), seq: nil, why: "the sequence the store kept is half of what refuses a stale set"},
		{name: "with no issuer", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) { p.Issuer = "" }},
		{name: "with an issuer that is not a URL", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) { p.Issuer = "identity.example.invalid" }},
		{name: "with no keys URL", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) { p.JWKSURL = "" }},
		{name: "with a keys URL that is not https", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) {
			p.JWKSURL = "http://identity.example.invalid/keys.jws"
		}},
		{name: "with a keys URL carrying a query", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) {
			p.JWKSURL = "https://identity.example.invalid/keys.jws?v=2"
		}},
		{name: "with no root key", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) { p.RootKey = nil }},
		{name: "with a root key of the wrong size", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) { p.RootKey = ed25519.PublicKey{1, 2, 3} }},
		{name: "with no root key id", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) { p.RootKeyID = "" }},
		{name: "with no pinned signing key", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) { p.Keys = nil }},
		{name: "with a pinned key of the wrong size", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) {
			p.Keys = []PinnedKey{{KID: testSignKID, Key: ed25519.PublicKey{9}, NotAfter: testNow.Add(time.Hour)}}
		}},
		{name: "with a pinned key and no not-after date", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) {
			p.Keys = []PinnedKey{{KID: testSignKID, Key: signPub}}
		}},
		{name: "with a sequence floor below one", store: newMemoryClaims(), seq: &memorySeq{}, mutate: func(p *Pins) { p.SeqFloor = 0 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := good
			p.Keys = append([]PinnedKey(nil), good.Keys...)
			if tc.mutate != nil {
				tc.mutate(&p)
			}
			v, err := New(tc.store, WithPins(p), WithSeqFloorStore(tc.seq))
			if err == nil {
				t.Fatalf("a verifier was built %s: a pin or a store that cannot work is refused where it is built, and %s", tc.name, tc.why)
			}
			if v != nil {
				t.Error("a refused construction answered a verifier: a caller that ignored the error would hold one that verifies nothing")
			}
		})
	}
}

// The build's own pins, until P8 replaces them with the production ones, have
// to be usable: a root key of the right size, at least one signing key with an
// id and a not-after date, an issuer and a keys URL that parse, and a floor of
// at least one. What P8 changes is the values and not the shape, so this test
// keeps passing when it lands.
func TestVerifyBuildPinsAreUsable(t *testing.T) {
	p := DefaultPins()
	if err := validatePins(p); err != nil {
		t.Fatalf("the build's pins are not usable: %v -- P1 ships placeholders from a generated test key and P8 replaces their values, and a build whose pins cannot be read refuses every assertion with a message about the assertion", err)
	}
	if len(p.Keys) == 0 {
		t.Fatal("the build pins no signing key")
	}
	// The reference instant is the release window these placeholders are
	// written in, and not time.Now(): the placeholder dates below are past any
	// release that would still carry them, and a test that read the wall clock
	// would begin failing on a day nothing changed.
	for _, k := range p.Keys {
		if !k.NotAfter.After(time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("the pinned key %q stops being accepted at %s, which is in the past: the pin's end is the rotation overlap's end, and a build past it refuses every assertion it signs", k.KID, k.NotAfter)
		}
	}
	if u, err := url.Parse(p.JWKSURL); err != nil || u.Scheme != "https" {
		t.Errorf("the pinned keys URL is %q: the set is fetched from a pinned https URL and from nowhere else", p.JWKSURL)
	}
}

// The verifier is one value a whole service uses at once, so concurrency is a
// claim the code has to carry: the key set cache and the two stores are shared
// state, and this is the test that fails under -race the moment a lock or a copy
// goes missing.
func TestVerifyIsSafeForConcurrentUse(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub)))
	// The build pins one key and the set holds another, so every call below
	// reads the set: the cache is shared state in the middle of this and not
	// beside it.
	buildPub, _ := testKey(t)
	v := newTestVerifier(t, pins(rootPub, 7, pinned("signing-build", buildPub)), srv, newMemoryClaims(), &memorySeq{})

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claims := goodClaims()
			claims["jti"] = fmt.Sprintf("test-jti-concurrent-%d", i)
			if _, err := verify(t, v, signAssertion(t, signPriv, testSignKID, claims)); err != nil {
				t.Errorf("a concurrent verification was refused: %v", err)
			}
		}()
	}
	wg.Wait()
}

// Every path P1 takes is one refusal type or another, and each of them says
// what failed without repeating what the caller sent. This is the acceptance's
// last sentence, made a test rather than a property of the code above.
func TestVerifyRefusalsCarryNoValue(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf(testSignKID, signPub)))
	v := newTestVerifier(t, pins(rootPub, 7, pinned(testSignKID, signPub)), srv, newMemoryClaims(), &memorySeq{})

	claims := goodClaims()
	claims["jti"] = "a-value-that-must-not-be-echoed"
	raw := signAssertion(t, signPriv, testSignKID, claims)
	// First use spends the jti; the second is the replay whose refusal must not
	// carry the assertion or the id it named.
	if _, err := verify(t, v, raw); err != nil {
		t.Fatalf("the first presentation was refused: %v", err)
	}
	_, err := verify(t, v, raw)
	if err == nil {
		t.Fatal("the replay was accepted")
	}
	if strings.Contains(err.Error(), "a-value-that-must-not-be-echoed") {
		t.Errorf("the refusal repeats the jti: a refusal reaches a log line and an answer body, and a value a stranger chose is neither: %v", err)
	}
}

// --- the durable sequence floor ---

// seqFloorDB is the store the durable floor is kept in: the project's
// compare-and-set store, which internal/gcpfake serves hermetically (the ETag
// rule and the null-node creation included) and which a service reads through
// the real client. A test that reached a live database would fail here rather
// than in a deployment.
func seqFloorDB(t *testing.T) (*gcpfake.RTDB, *rtdb.Client) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "operator-id-token" }})
	if err != nil {
		t.Fatalf("building the store client: %v", err)
	}
	return f, c
}

// The acceptance's own sentence: the highest sequence is kept "in C1's
// compare-and-set store", not in process memory -- so what one instance has
// accepted is what the instance next door refuses, and a restart inherits the
// floor rather than starting again.
func TestSeqFloorKeepsTheHighestInTheStore(t *testing.T) {
	_, store := seqFloorDB(t)
	ctx := context.Background()
	const path = "assertion/key-set-seq"

	one, err := NewSeqFloor(store, path)
	if err != nil {
		t.Fatalf("building the floor: %v", err)
	}
	two, err := NewSeqFloor(store, path)
	if err != nil {
		t.Fatalf("building the second instance's floor: %v", err)
	}

	// A node nothing has written yet holds no floor, so the first set read is
	// kept.
	if ok, err := one.Observe(ctx, 7); err != nil || !ok {
		t.Fatalf("Observe(7) on a store holding no mark = %v, %v; want true: the first key set read has nothing above it", ok, err)
	}
	// The instance next door has nothing of its own to remember it by: the mark
	// is the store's.
	if ok, err := two.Observe(ctx, 6); err != nil {
		t.Fatalf("Observe(6) on the second instance: %v", err)
	} else if ok {
		t.Fatal("a set below the sequence the store kept was accepted by the instance next door: the mark is the store's, and a floor in this process is the one the plan's O7 forbids")
	}
	// The sequence the mark already stands at is the set the project publishes
	// now, and the acceptance admits a set "no lower than" the highest kept: a
	// floor is not a lock, so the instance next door keeps the current set.
	if ok, err := two.Observe(ctx, 7); err != nil || !ok {
		t.Errorf("Observe(7) at the mark = %v, %v; want true: a second instance, or a restart, would otherwise refuse the key set the project publishes now until the identity project published a higher one", ok, err)
	}

	// A restart is a new floor object over the same node: it inherits what the
	// store holds instead of beginning again, keeping what is at the mark and
	// refusing what is below it.
	restarted, err := NewSeqFloor(store, path)
	if err != nil {
		t.Fatalf("building the restarted instance's floor: %v", err)
	}
	if ok, err := restarted.Observe(ctx, 6); err != nil || ok {
		t.Errorf("Observe(6) after a restart = %v, %v; want false: a restart is not a second chance for a set the project has moved past", ok, err)
	}
	if ok, err := restarted.Observe(ctx, 7); err != nil || !ok {
		t.Errorf("Observe(7) after a restart = %v, %v; want true: the mark the restart inherits is the set it is asked about", ok, err)
	}

	// Only a higher set moves the mark, and then the set it was raised from is
	// below it.
	if ok, err := one.Observe(ctx, 8); err != nil || !ok {
		t.Fatalf("Observe(8) above the mark = %v, %v; want true", ok, err)
	}
	if ok, err := two.Observe(ctx, 7); err != nil || ok {
		t.Errorf("Observe(7) below the raised mark = %v, %v; want false", ok, err)
	}
	if ok, err := restarted.Observe(ctx, 8); err != nil || !ok {
		t.Errorf("Observe(8) at the raised mark = %v, %v; want true", ok, err)
	}

	// The node holds the highest sequence any instance accepted.
	var held int64
	found, err := store.Get(ctx, path, &held)
	if err != nil || !found {
		t.Fatalf("reading the mark back: found=%v err=%v", found, err)
	}
	if held != 8 {
		t.Errorf("the stored mark is %d, want 8: the node holds the highest sequence any instance accepted", held)
	}

	// Two projects are two nodes: a mark under one path says nothing about
	// another.
	other, err := NewSeqFloor(store, "assertion/other-project/key-set-seq")
	if err != nil {
		t.Fatalf("building another project's floor: %v", err)
	}
	if ok, err := other.Observe(ctx, 1); err != nil || !ok {
		t.Errorf("Observe(1) under another project's path = %v, %v; want true: the mark is the project's own node", ok, err)
	}
}

// Eight instances presenting one sequence that no store holds yet: every one of
// them keeps it, and the node ends up holding that one mark. A sequence is a
// floor and not a lock, so a race for it is not a race one caller wins -- and
// the losers must not lower the mark on their way out.
func TestSeqFloorAcceptsOneSequenceFromEveryInstance(t *testing.T) {
	_, store := seqFloorDB(t)
	ctx := context.Background()
	floor, err := NewSeqFloor(store, "assertion/raced-seq")
	if err != nil {
		t.Fatalf("building the floor: %v", err)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		kept    int
		refused int
		failed  error
	)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := floor.Observe(ctx, 5)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				failed = err
			case ok:
				kept++
			default:
				refused++
			}
		}()
	}
	wg.Wait()
	if failed != nil {
		t.Fatalf("an instance racing for the mark failed: %v", failed)
	}
	if kept != 8 || refused != 0 {
		t.Errorf("of eight instances presenting sequence 5 at once, %d kept it and %d were refused: the eight are presenting the same, current set, and the mark they leave behind is one floor rather than eight", kept, refused)
	}
	var held int64
	found, err := store.Get(ctx, "assertion/raced-seq", &held)
	if err != nil || !found || held != 5 {
		t.Errorf("after the race the node holds %d (found=%v, err=%v), want 5: the eight raised one mark, and no instance wrote a lower one over it", held, found, err)
	}
	if ok, err := floor.Observe(ctx, 4); err != nil || ok {
		t.Errorf("Observe(4) after the race = %v, %v; want false: a lost race leaves the mark where the winner put it", ok, err)
	}
	if ok, err := floor.Observe(ctx, 5); err != nil || !ok {
		t.Errorf("Observe(5) after the race = %v, %v; want true", ok, err)
	}
	if ok, err := floor.Observe(ctx, 6); err != nil || !ok {
		t.Errorf("Observe(6) after the race = %v, %v; want true: a set above the mark is the rotation this floor is here to admit", ok, err)
	}
}

// stubStore is CompareAndSet a test answers by hand: the value the node holds,
// what a read or a write should fail with, and a count of the writes tried.
type stubStore struct {
	mu       sync.Mutex
	value    int64
	found    bool
	etag     string
	readErr  error
	writeErr error
	writes   int
}

func (s *stubStore) GetETag(_ context.Context, _ string, out any) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return "", false, s.readErr
	}
	if s.found {
		if held, ok := out.(*int64); ok {
			*held = s.value
		}
	}
	return s.etag, s.found, nil
}

func (s *stubStore) PutIfMatch(_ context.Context, _, _ string, _ any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	return s.writeErr
}

// A store that fails is an error and never a floor: the sequence is the second
// half of what refuses a stale document, and a failure that answered "no mark
// yet" would accept one.
func TestSeqFloorRefusesAStoreThatFails(t *testing.T) {
	floor, err := NewSeqFloor(&stubStore{readErr: errors.New("the store is down")}, "assertion/down")
	if err != nil {
		t.Fatalf("building the floor: %v", err)
	}
	ok, err := floor.Observe(context.Background(), 5)
	if err == nil {
		t.Fatal("a floor nobody could read answered a sequence")
	}
	if ok {
		t.Error("a floor whose store failed reported the sequence as kept")
	}
	if !errors.Is(err, ErrStore) {
		t.Errorf("the failure is %v, which does not wrap ErrStore: a caller has to be able to tell a store that failed from a set that was refused", err)
	}

	// A write that fails for any reason but a lost race is the same answer: the
	// mark was not raised, so the set is not accepted.
	writeErr := &stubStore{etag: "etag-1", writeErr: errors.New("the store refused the write")}
	floor, err = NewSeqFloor(writeErr, "assertion/unwritable")
	if err != nil {
		t.Fatalf("building the floor: %v", err)
	}
	if ok, err := floor.Observe(context.Background(), 5); ok || !errors.Is(err, ErrStore) {
		t.Errorf("Observe(5) with an unwritable store = %v, %v; want false and ErrStore", ok, err)
	}
	if writeErr.writes != 1 {
		t.Errorf("the floor wrote %d times, want 1: a failure that is not a lost race is not a reason to try again", writeErr.writes)
	}
}

// A store that loses the race for ever is a store whose answer this verifier
// does not have. The bound is what keeps that a failure rather than a loop.
func TestSeqFloorGivesUpOnAStoreThatAlwaysRaces(t *testing.T) {
	store := &stubStore{etag: "null_etag", writeErr: rtdb.ErrPrecondition}
	floor, err := NewSeqFloor(store, "assertion/always-raced")
	if err != nil {
		t.Fatalf("building the floor: %v", err)
	}
	ok, err := floor.Observe(context.Background(), 5)
	if ok || err == nil {
		t.Fatalf("Observe(5) against a store that never lands = %v, %v; want false and an error", ok, err)
	}
	if !errors.Is(err, ErrStore) {
		t.Errorf("the failure is %v, which does not wrap ErrStore", err)
	}
	if store.writes != maxSeqFloorRaces {
		t.Errorf("the floor wrote %d times, want the bound %d: a store that never lands must end in a failure rather than in a loop", store.writes, maxSeqFloorRaces)
	}
}

// A node holding something that is not a sequence is not a mark this verifier
// can compare against, and guessing at it would be guessing at the floor.
func TestSeqFloorRefusesAMarkItCannotRead(t *testing.T) {
	fake, store := seqFloorDB(t)
	fake.Set("assertion/stray-seq", "seven")
	floor, err := NewSeqFloor(store, "assertion/stray-seq")
	if err != nil {
		t.Fatalf("building the floor: %v", err)
	}
	if ok, err := floor.Observe(context.Background(), 5); ok || !errors.Is(err, ErrStore) {
		t.Errorf("Observe(5) with %q in the node = %v, %v; want false and ErrStore", "seven", ok, err)
	}
}

// A sequence is at least one -- the envelope's own floor is -- so a zero never
// becomes a mark, and a floor asked for without a store or a path is refused
// where it is built rather than at the first assertion.
func TestNewSeqFloorRefusesWhatItCannotKeep(t *testing.T) {
	ctx := context.Background()

	store := &stubStore{etag: "null_etag"}
	floor, err := NewSeqFloor(store, "assertion/zero-seq")
	if err != nil {
		t.Fatalf("building the floor: %v", err)
	}
	for _, seq := range []int64{0, -1} {
		if ok, err := floor.Observe(ctx, seq); ok || err == nil {
			t.Errorf("Observe(%d) = %v, %v; want false and an error", seq, ok, err)
		}
	}
	if store.writes != 0 {
		t.Errorf("the floor wrote %d times for a sequence below one, want none", store.writes)
	}

	for _, tc := range []struct {
		name  string
		store CompareAndSet
		path  string
	}{
		{name: "with no store", store: nil, path: "assertion/key-set-seq"},
		{name: "with no path", store: store, path: ""},
		{name: "with a leading separator", store: store, path: "/assertion/key-set-seq"},
		{name: "with a trailing separator", store: store, path: "assertion/key-set-seq/"},
		{name: "with a doubled separator", store: store, path: "assertion//key-set-seq"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSeqFloor(tc.store, tc.path); err == nil {
				t.Errorf("a floor %s was built: it would keep no mark, and every verification would be refused for a reason about the deployment rather than about the assertion", tc.name)
			}
		})
	}
}

// The durable floor is the one the verifier runs on, so the rule it enforces is
// tested through a verifier and not only through the store: the instance next
// door reads a newer key set, and this instance -- a second verifier over its
// own floor object, the same node -- is served the older document.
func TestVerifyRefusesAKeySetBelowTheSequenceTheDurableStoreKept(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	oldPub, oldPriv := testKey(t)
	newPub, newPriv := testKey(t)
	buildPub, _ := testKey(t)
	srv := &keySetServer{}
	_, store := seqFloorDB(t)
	const path = "assertion/key-set-seq"
	p := pins(rootPub, 1, pinned("signing-build", buildPub))

	floor, err := NewSeqFloor(store, path)
	if err != nil {
		t.Fatalf("building the floor: %v", err)
	}
	srv.set(keySetDoc(t, rootPriv, 9, validUntil(), jwkOf("signing-new", newPub)))
	first := newTestVerifier(t, p, srv, newMemoryClaims(), floor)
	if _, err := verify(t, first, signAssertion(t, newPriv, "signing-new", goodClaims())); err != nil {
		t.Fatalf("the newer key set was refused: %v", err)
	}

	restarted, err := NewSeqFloor(store, path)
	if err != nil {
		t.Fatalf("building the second instance's floor: %v", err)
	}
	srv.set(keySetDoc(t, rootPriv, 8, validUntil(), jwkOf("signing-old", oldPub)))
	second := newTestVerifier(t, p, srv, newMemoryClaims(), restarted)
	_, err = verify(t, second, signAssertion(t, oldPriv, "signing-old", goodClaims()))
	if err == nil {
		t.Fatal("a document below the sequence the durable store kept was accepted: a cache serving an older set would walk a project back onto keys another instance has already moved past")
	}
	if field := refusalField(t, err); field != "seq" {
		t.Errorf("the refusal names %q, want \"seq\"", field)
	}
}

// The instance next door must keep the key set the first one kept. The
// acceptance admits a sequence "no lower than" the highest one stored, and P8
// pins a release's own key-set sequence as the floor -- so the document a build
// is released against is exactly the one at the mark, on every instance and
// after every restart. Refusing it would refuse every non-pinned key id until
// the identity project published a higher set.
func TestVerifyAcceptsTheCurrentKeySetInTheInstanceNextDoor(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	_, store := seqFloorDB(t)
	const path = "assertion/key-set-seq"
	// The floor is the sequence of the key set this release is pinned to. The
	// build pins one key, which is not the one this document names: the fetch
	// path is the one this case is about.
	buildPub, _ := testKey(t)
	p := pins(rootPub, 8, pinned("signing-build", buildPub))
	srv.set(keySetDoc(t, rootPriv, 8, validUntil(), jwkOf("signing-rotated", signPub)))

	first, err := NewSeqFloor(store, path)
	if err != nil {
		t.Fatalf("building the floor: %v", err)
	}
	claims := goodClaims()
	claims["jti"] = "test-jti-first-instance"
	firstV := newTestVerifier(t, p, srv, newMemoryClaims(), first)
	if _, err := verify(t, firstV, signAssertion(t, signPriv, "signing-rotated", claims)); err != nil {
		t.Fatalf("the first instance refused the current key set: %v", err)
	}

	// A restart is a second verifier: its own key set cache, its own floor
	// object, the same node -- and the same document is the current one there.
	second, err := NewSeqFloor(store, path)
	if err != nil {
		t.Fatalf("building the second instance's floor: %v", err)
	}
	claims = goodClaims()
	claims["jti"] = "test-jti-second-instance"
	secondV := newTestVerifier(t, p, srv, newMemoryClaims(), second)
	if _, err := verify(t, secondV, signAssertion(t, signPriv, "signing-rotated", claims)); err != nil {
		t.Fatalf("the instance next door refused the key set the first one accepted: %v", err)
	}
}

// A withdrawn pinned key must end on an instance that has read nothing. The
// rotation is what says so, and the durable mark is where this instance learns
// that a rotation happened at all: without it, a fresh Cloud Run instance whose
// cache is empty verifies with the pinned key the project has withdrawn -- a
// leaked one included -- with no fetch, for as long as its build is deployed.
// The mark is the store's rather than the cache's, so "somewhere, a set above
// the floor has been kept" is enough to make this instance go and hold the set
// before it uses a pin.
func TestVerifyRefusesAWithdrawnPinnedKeyOnAnInstanceThatNeverReadASet(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	keptPub, keptPriv := testKey(t)
	gonePub, gonePriv := testKey(t)
	srv := &keySetServer{}
	_, store := seqFloorDB(t)
	const path = "assertion/key-set-seq"
	// The build pins the key the project has rotated out, and its floor is the
	// sequence of the set it was released against. The document served now is
	// above that floor and holds the key the rotation left behind.
	p := pins(rootPub, 8, pinned("signing-gone", gonePub))
	srv.set(keySetDoc(t, rootPriv, 9, validUntil(), jwkOf("signing-kept", keptPub)))

	first, err := NewSeqFloor(store, path)
	if err != nil {
		t.Fatalf("building the first instance's floor: %v", err)
	}
	claims := goodClaims()
	claims["jti"] = "test-jti-first-instance"
	// An assertion the build does not pin is what moves the mark above the floor.
	if _, err := verify(t, newTestVerifier(t, p, srv, newMemoryClaims(), first), signAssertion(t, keptPriv, "signing-kept", claims)); err != nil {
		t.Fatalf("the first instance refused the current key set: %v", err)
	}

	// The instance next door: its own key set cache, which has read nothing, its
	// own floor object, the same durable node.
	second, err := NewSeqFloor(store, path)
	if err != nil {
		t.Fatalf("building the second instance's floor: %v", err)
	}
	claims = goodClaims()
	claims["jti"] = "test-jti-second-instance"
	_, err = verify(t, newTestVerifier(t, p, srv, newMemoryClaims(), second), signAssertion(t, gonePriv, "signing-gone", claims))
	if err == nil {
		t.Fatal("an instance whose cache had read nothing accepted an assertion signed by a pinned key the project had rotated out: the durable mark is what says a set above the floor was kept, and an instance that ignores it verifies with a withdrawn key -- a leaked one included -- until the release that pins the next one")
	}
	if field := refusalField(t, err); field != "kid" {
		t.Errorf("the refusal names %q, want \"kid\"", field)
	}
	if reads := srv.reads(); reads != 2 {
		t.Errorf("the key endpoint was read %d time(s), want 2 -- one for each instance: the instance that had read nothing holds the set before it uses a pin the store says has moved", reads)
	}
}

// An unauthenticated caller must not be able to park the key set for everyone
// else. A key id is looked up before any signature is verified, so any caller
// can name one this process has to fetch; if that fetch ran on the caller's own
// context, a request that names one and dies would abort the read and leave the
// failure in the cache, and every caller inside the read interval -- a minute --
// would be refused over a document nobody ever failed to read.
func TestVerifyACancelledCallerDoesNotEndTheKeySetRead(t *testing.T) {
	rootPub, rootPriv := testKey(t)
	signPub, signPriv := testKey(t)
	srv := &keySetServer{}
	srv.set(keySetDoc(t, rootPriv, 7, validUntil(), jwkOf("signing-rotated", signPub)))
	// No pinned key, so the fetch path is the one that has to answer, and the
	// read's own bound is long: the only thing in this case that could end the
	// read is the caller.
	buildPub, _ := testKey(t)
	v := newTestVerifier(t, pins(rootPub, 1, pinned("signing-build", buildPub)), srv, newMemoryClaims(), &memorySeq{})
	v.keys.fetchTimeout = 30 * time.Second

	release := srv.holdNext()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	triggered := make(chan struct{})
	go func() {
		defer close(triggered)
		claims := goodClaims()
		claims["jti"] = "test-jti-cancelled"
		_, _ = v.Verify(ctx, signAssertion(t, signPriv, "signing-rotated", claims), mobilesession.Binding{Audience: testAudience, Challenge: testChallenge})
	}()

	srv.fetchStarted(t)
	cancel()
	select {
	case <-triggered:
	case <-time.After(5 * time.Second):
		t.Fatal("the verification of a caller that cancelled did not end: the read it triggered is the cache's and not the caller's to wait for")
	}

	// The read the caller abandoned still finishes, and its answer is the one
	// the next caller gets -- with no second read of the endpoint, which is the
	// interval the abandoned failure would otherwise have spent refusing.
	release()
	claims := goodClaims()
	claims["jti"] = "test-jti-after-cancellation"
	if _, err := verify(t, v, signAssertion(t, signPriv, "signing-rotated", claims)); err != nil {
		t.Fatalf("a verification after a caller cancelled was refused: %v", err)
	}
	if reads := srv.reads(); reads != 1 {
		t.Errorf("the endpoint was read %d times, want 1: the read the cancelled caller triggered is the one that answered", reads)
	}
}
