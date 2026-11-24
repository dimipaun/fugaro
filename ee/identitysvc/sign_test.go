// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// The assertion signer (mobile identity plan task A1). What these tests hold is
// what no later task can state from the outside: the assertion is minted only
// for the one audience and the one challenge the caller named, over a key the
// served set published and inside the two minutes O4 accepted — and that every
// path that is not that whole answer signs nothing at all.
//
// Nothing here holds a key. The KMS ring is faked with a key generated inside
// the test's own process, the same way ee/keyservice/kmssigner_test.go does
// (.claude/rules/ee.md R5), and the ID token is signed with a second generated
// key — RSA, because that is what Firebase's own securetoken signer publishes.
// The tokens and assertions in this file are built at run time from those keys
// and never spelled out, so no literal here is a credential or a bearer.

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/mobilesession"
)

const (
	// The identity project the signer is deployed for, as its own Firebase
	// project id, and the issuer its tokens carry. Both are deployment values
	// and not secrets.
	testProject = "fugaro-ident"
	testKeyID   = "identity-2026-11"
	testEmail   = "dana@acme.example"
	testUID     = "firebase-uid-dana"
	testSubject = "google-sub-1122334455"
)

// fakeKMS is Cloud KMS as the signer sees it: one key version's own private
// half, which never leaves this process, and a count of the signatures it was
// asked for — the count is what makes "it signed nothing" a fact rather than a
// claim about the answer.
type fakeKMS struct {
	key      ed25519.PrivateKey
	failWith error
	// short answers a signature of the wrong length, which is an answer no
	// Ed25519 verifier accepts.
	short bool
	calls int
}

func (f *fakeKMS) Sign(_ context.Context, payload []byte) ([]byte, error) {
	f.calls++
	if f.failWith != nil {
		return nil, f.failWith
	}
	if len(payload) == 0 {
		return nil, errors.New("the signer asked for a signature over no bytes")
	}
	sig := ed25519.Sign(f.key, payload)
	if f.short {
		return sig[:32], nil
	}
	return sig, nil
}

// fakeKeys is Google's published key set for this project's ID tokens, by kid:
// one key, generated here.
type fakeKeys struct {
	kid  string
	key  *rsa.PublicKey
	fail error
}

func (f *fakeKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	if kid != f.kid {
		return nil, fmt.Errorf("%w: the test's set holds %s", ErrUnknownKeyID, f.kid)
	}
	return f.key, nil
}

// staticSet is the served, root-signed key set as the signer reads it.
type staticSet struct {
	kids []string
	exp  time.Time
	fail error
}

func (s staticSet) Current(context.Context) (ServedSet, error) {
	if s.fail != nil {
		return ServedSet{}, s.fail
	}
	return ServedSet{KIDs: s.kids, ExpiresAt: s.exp}, nil
}

// fixture is one signer with its fakes, and the clock the tests move.
type fixture struct {
	kms  *fakeKMS
	keys *fakeKeys
	set  staticSet
	now  time.Time
	pub  ed25519.PublicKey

	verifier *IDTokenVerifier
	signer   *Signer
	tokenKey *rsa.PrivateKey
}

// newFixture builds a deployed signer's whole shape out of generated keys.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	edPub, edPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating the signing key: %v", err)
	}
	tokenKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating the ID token's key: %v", err)
	}
	f := &fixture{
		kms:      &fakeKMS{key: edPriv},
		keys:     &fakeKeys{kid: testKeyID, key: &tokenKey.PublicKey},
		set:      staticSet{kids: []string{testKeyID}, exp: time.Now().Add(90 * 24 * time.Hour)},
		now:      time.Now().UTC(),
		pub:      edPub,
		tokenKey: tokenKey,
	}
	clock := WithClock(func() time.Time { return f.now })
	verifier, err := NewIDTokenVerifier(testProject, f.keys, clock)
	if err != nil {
		t.Fatalf("building the ID token verifier: %v", err)
	}
	signer, err := NewSigner(SignerConfig{
		Issuer: IssuerPrefix + testProject,
		KeyID:  testKeyID,
		KMS:    f.kms,
		Set:    f.set,
		Tokens: verifier,
	}, clock)
	if err != nil {
		t.Fatalf("building the signer: %v", err)
	}
	f.verifier, f.signer = verifier, signer
	return f
}

// token mints a Firebase ID token of this project, shaped as the identity
// project's own Firebase mints one, with mutate free to break one thing.
func (f *fixture) token(t *testing.T, mutate func(claims map[string]any)) string {
	t.Helper()
	claims := map[string]any{
		"iss":            IssuerPrefix + testProject,
		"aud":            testProject,
		"sub":            testUID,
		"user_id":        testUID,
		"iat":            f.now.Add(-time.Minute).Unix(),
		"exp":            f.now.Add(time.Hour).Unix(),
		"auth_time":      f.now.Add(-10 * time.Minute).Unix(),
		"email":          testEmail,
		"email_verified": true,
		"firebase": map[string]any{
			"sign_in_provider": "google.com",
			"identities":       map[string]any{"google.com": []any{testSubject}},
		},
	}
	if mutate != nil {
		mutate(claims)
	}
	return signJWT(t, f.tokenKey, testKeyID, claims)
}

// signJWT builds one compact RS256 JWT, which is the only shape Firebase's own
// signer mints and the only one the verifier reads.
func signJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	head, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	if err != nil {
		t.Fatalf("encoding the header: %v", err)
	}
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("encoding the claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing the token: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// readAssertion splits a minted assertion and checks its signature with the
// key's public half, answering the header and the claims as they were signed.
func readAssertion(t *testing.T, pub ed25519.PublicKey, raw string) (map[string]any, map[string]any) {
	t.Helper()
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("the assertion is %d segments, want 3", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("the signature is not base64url: %v", err)
	}
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		t.Fatal("the assertion does not verify with the signing key's public half")
	}
	var head, claims map[string]any
	if err := json.Unmarshal(decode(t, parts[0]), &head); err != nil {
		t.Fatalf("the header is not JSON: %v", err)
	}
	if err := json.Unmarshal(decode(t, parts[1]), &claims); err != nil {
		t.Fatalf("the claims are not JSON: %v", err)
	}
	return head, claims
}

func decode(t *testing.T, segment string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("a segment is not base64url: %v", err)
	}
	return raw
}

// quietLogs is the logger the handler tests that are not about logging inject:
// a refusal is written to the platform's log in a deployment, and a test that
// prints one per case would bury the run's own output.
func quietLogs() HandlerOption {
	return WithLogger(func(string, ...any) {})
}

// TestSignMintsAnAssertionForExactlyTheNamedProject is the task's own: one
// verified ID token in, one assertion out, bound to the one audience and the
// one challenge the caller named, over a key the served set published, inside
// O4's two minutes, carrying the claims D2792 fixes and nothing else.
func TestSignMintsAnAssertionForExactlyTheNamedProject(t *testing.T) {
	f := newFixture(t)
	const challenge = "2f6b0c1d-4e5a-4a1f-9a44-1c0f8f2b7d31"
	raw, err := f.signer.Sign(context.Background(), f.token(t, nil), "https://edge.acme.example", challenge)
	if err != nil {
		t.Fatalf("signing an assertion: %v", err)
	}
	head, claims := readAssertion(t, f.pub, raw)
	if head["alg"] != "EdDSA" {
		t.Fatalf("the assertion was signed with %v, and an Ed25519 KMS key is EdDSA", head["alg"])
	}
	if head["typ"] != "JWT" {
		t.Fatalf("the header's typ is %v, want JWT", head["typ"])
	}
	// The kid is what the project's verifier looks the key up by, so it has to
	// be the one the served set published.
	if head["kid"] != testKeyID {
		t.Fatalf("the assertion names key %v, and the served set published %s", head["kid"], testKeyID)
	}
	want := []string{"aud", "auth_time", "challenge", "email", "email_verified", "exp", "iat", "iss", "jti", "provider", "sub"}
	got := make([]string, 0, len(claims))
	for k := range claims {
		got = append(got, k)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("the assertion carries %v, and D2792's claim set is %v", got, want)
	}
	if claims["aud"] != "https://edge.acme.example" {
		t.Fatalf("the assertion's aud is %v, and the caller named one project", claims["aud"])
	}
	if claims["challenge"] != challenge {
		t.Fatalf("the assertion's challenge is %v, and the caller named one", claims["challenge"])
	}
	if claims["iss"] != IssuerPrefix+testProject {
		t.Fatalf("the assertion's iss is %v, and the identity project's issuer is %s", claims["iss"], IssuerPrefix+testProject)
	}
	if claims["email"] != testEmail {
		t.Fatalf("the assertion's email is %v, want %s", claims["email"], testEmail)
	}
	if claims["email_verified"] != true {
		t.Fatalf("the assertion's email_verified is %v, and the token's was true", claims["email_verified"])
	}
	if claims["provider"] != "google.com" {
		t.Fatalf("the assertion's provider is %v, want google.com", claims["provider"])
	}
	// The provider's own stable id and not the Firebase uid: a member is keyed
	// on email plus the provider's id (D2785, F9).
	if claims["sub"] != testSubject {
		t.Fatalf("the assertion's sub is %v, and the provider's stable id is %s", claims["sub"], testSubject)
	}
	if claims["auth_time"] != float64(f.now.Add(-10*time.Minute).Unix()) {
		t.Fatalf("the assertion's auth_time is %v, and the sign-in was ten minutes before now", claims["auth_time"])
	}
	iat, exp := claims["iat"].(float64), claims["exp"].(float64)
	if int64(exp-iat) != int64(AssertionTTL.Seconds()) {
		t.Fatalf("the assertion lives %v, and O4's lifetime is %s", time.Duration(exp-iat)*time.Second, AssertionTTL)
	}
	if int64(iat) != f.now.Unix() {
		t.Fatalf("the assertion was issued at %v, and this fixture's clock is %v", int64(iat), f.now.Unix())
	}
	// One assertion is one use: two mints of one token are two documents.
	again, err := f.signer.Sign(context.Background(), f.token(t, nil), "https://edge.acme.example", challenge)
	if err != nil {
		t.Fatalf("signing a second assertion: %v", err)
	}
	_, other := readAssertion(t, f.pub, again)
	if claims["jti"] == other["jti"] {
		t.Fatal("two assertions carry one jti, and a single-use id is what makes an assertion unreplayable")
	}
	if f.kms.calls != 2 {
		t.Fatalf("the key ring signed %d time(s), want one per assertion", f.kms.calls)
	}
}

// TestSignRefusesAnAudienceTheCallerDidNotName is the red step: an assertion is
// bound to exactly one project, so a request that names none is refused rather
// than given the audience of some other caller — and nothing is signed.
func TestSignRefusesAnAudienceTheCallerDidNotName(t *testing.T) {
	f := newFixture(t)
	for _, audience := range []string{"", "   ", strings.Repeat("a", maxAudienceBytes+1), "two words", "a\x00b"} {
		f.kms.calls = 0
		if _, err := f.signer.Sign(context.Background(), f.token(t, nil), audience, "nonce-1"); err == nil {
			t.Fatalf("an assertion was minted for the audience %q, and an unreadable one is no project at all", audience)
		} else if !errors.Is(err, ErrNoAudience) {
			t.Fatalf("the refusal of %q reads %q, and it should be ErrNoAudience", audience, err)
		}
		if f.kms.calls != 0 {
			t.Fatalf("the key ring was asked to sign for the audience %q", audience)
		}
	}
}

// TestSignRefusesAChallengeItWasNotGiven: an assertion with no challenge is one
// any project could replay into another flow, so a missing one is a refusal.
func TestSignRefusesAChallengeItWasNotGiven(t *testing.T) {
	f := newFixture(t)
	for _, challenge := range []string{"", "  ", strings.Repeat("c", maxChallengeBytes+1), "a\x01b"} {
		f.kms.calls = 0
		if _, err := f.signer.Sign(context.Background(), f.token(t, nil), "https://edge.acme.example", challenge); err == nil {
			t.Fatalf("an assertion was minted with the challenge %q", challenge)
		} else if !errors.Is(err, ErrNoChallenge) {
			t.Fatalf("the refusal of %q reads %q, and it should be ErrNoChallenge", challenge, err)
		}
		if f.kms.calls != 0 {
			t.Fatalf("the key ring was asked to sign with the challenge %q", challenge)
		}
	}
}

// TestSignRefusesAnUnverifiedPasswordEmail: a password sign-in's address is the
// whole of what proves it belongs to the person (D2785), so an unverified one
// is refused here. A federated sign-in's flag travels instead: whether an
// unverified Microsoft or Apple address is admissible is the project's
// accepted-provider policy and not this service's (D2785, P3).
func TestSignRefusesAnUnverifiedPasswordEmail(t *testing.T) {
	f := newFixture(t)
	password := func(claims map[string]any) {
		claims["firebase"] = map[string]any{"sign_in_provider": "password", "identities": map[string]any{"email": []any{testEmail}}}
	}
	raw, err := f.signer.Sign(context.Background(), f.token(t, func(claims map[string]any) {
		password(claims)
		claims["email_verified"] = false
	}), "https://edge.acme.example", "nonce-1")
	if err == nil {
		t.Fatal("an unverified password address was signed into an assertion")
	}
	if !errors.Is(err, ErrUnverifiedIDToken) {
		t.Fatalf("the refusal reads %q, and an unverified address is a refusal of the token", err)
	}
	if raw != "" {
		t.Fatal("a refused request answered an assertion")
	}
	if f.kms.calls != 0 {
		t.Fatal("the key ring signed for an unverified password address")
	}
	// A password sign-in whose address is verified is signed, with the uid of
	// the Firebase account as the subject: Firebase's own identity map carries
	// no provider id for one.
	verified, err := f.signer.Sign(context.Background(), f.token(t, password), "https://edge.acme.example", "nonce-1")
	if err != nil {
		t.Fatalf("signing a verified password sign-in: %v", err)
	}
	_, claims := readAssertion(t, f.pub, verified)
	if claims["provider"] != "password" {
		t.Fatalf("the assertion's provider is %v, want password", claims["provider"])
	}
	if claims["sub"] != testUID {
		t.Fatalf("the assertion's sub is %v, and a password sign-in's provider id is the Firebase uid", claims["sub"])
	}
	// And a federated sign-in whose provider says the address is unverified is
	// carried rather than refused here.
	federated, err := f.signer.Sign(context.Background(), f.token(t, func(claims map[string]any) {
		claims["email_verified"] = false
	}), "https://edge.acme.example", "nonce-1")
	if err != nil {
		t.Fatalf("signing a federated sign-in with an unverified address: %v", err)
	}
	if _, claims := readAssertion(t, f.pub, federated); claims["email_verified"] != false {
		t.Fatalf("the assertion's email_verified is %v, and the token said false", claims["email_verified"])
	}
}

// TestSignVerifiesTheIDTokenFirst: nothing in the token is read before its
// signature covers it, and every token this project did not mint — or minted
// for somebody else, or past its window — is refused with nothing signed.
func TestSignVerifiesTheIDTokenFirst(t *testing.T) {
	f := newFixture(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a second key: %v", err)
	}
	cases := []struct {
		name   string
		token  func() string
		reason string
	}{
		{"not a JWT", func() string { return "not.a" }, "the ID token is not a JWT"},
		{"signed by nobody's key", func() string { return signJWT(t, other, testKeyID, map[string]any{"aud": testProject}) }, "does not verify"},
		{"an unknown kid", func() string { return signJWT(t, f.tokenKey, "identity-2025-01", nil) }, "holds no key"},
		{"another project's audience", func() string {
			return f.token(t, func(claims map[string]any) { claims["aud"] = "some-other-project" })
		}, "another audience"},
		{"another project's issuer", func() string {
			return f.token(t, func(claims map[string]any) { claims["iss"] = IssuerPrefix + "some-other-project" })
		}, "not issued by this project"},
		{"expired", func() string {
			return f.token(t, func(claims map[string]any) { claims["exp"] = f.now.Add(-time.Minute).Unix() })
		}, "expired"},
		{"issued in the future", func() string {
			return f.token(t, func(claims map[string]any) { claims["iat"] = f.now.Add(time.Hour).Unix() })
		}, "future"},
		{"not valid yet", func() string {
			return f.token(t, func(claims map[string]any) { claims["nbf"] = f.now.Add(time.Hour).Unix() })
		}, "not valid yet"},
		{"two uids", func() string {
			return f.token(t, func(claims map[string]any) { claims["user_id"] = "another-uid" })
		}, "no readable uid"},
		{"no uid", func() string {
			return f.token(t, func(claims map[string]any) { delete(claims, "sub"); delete(claims, "user_id") })
		}, "no readable uid"},
		{"an anonymous sign-in", func() string {
			return f.token(t, func(claims map[string]any) {
				delete(claims, "email")
				claims["firebase"] = map[string]any{"sign_in_provider": "anonymous"}
			})
		}, "address"},
		{"a provider this code cannot read", func() string {
			return f.token(t, func(claims map[string]any) { delete(claims, "firebase") })
		}, "provider"},
		{"no authentication time", func() string {
			return f.token(t, func(claims map[string]any) { delete(claims, "auth_time") })
		}, "authentication"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.kms.calls = 0
			if _, err := f.signer.Sign(context.Background(), tc.token(), "https://edge.acme.example", "nonce-1"); err == nil {
				t.Fatal("an assertion was signed for an ID token this project did not mint")
			} else if !errors.Is(err, ErrUnverifiedIDToken) {
				t.Fatalf("the refusal reads %q, and every one of these is a refusal of the token", err)
			} else if !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("the refusal reads %q, and it should name %q", err, tc.reason)
			}
			if f.kms.calls != 0 {
				t.Fatal("the key ring signed for a token the service refused")
			}
		})
	}
}

// TestSignRefusesAKeyTheServedSetDoesNotCarry: the owner publishes the key set
// before the signer uses a key (Check 737), so a kid the served set does not
// list — or a set past its life, or one that could not be read — is a refusal
// rather than an assertion signed under a key nobody published.
func TestSignRefusesAKeyTheServedSetDoesNotCarry(t *testing.T) {
	const reason = "served key set"
	cases := []struct {
		name string
		set  staticSet
	}{
		{"the set lists another key", staticSet{kids: []string{"identity-2026-12"}, exp: time.Now().Add(time.Hour)}},
		{"the set lists no key", staticSet{kids: nil, exp: time.Now().Add(time.Hour)}},
		{"the set has expired", staticSet{kids: []string{testKeyID}, exp: time.Now().Add(-time.Minute)}},
		{"the set could not be read", staticSet{fail: errors.New("the bucket answered nothing")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.set = tc.set
			signer, err := NewSigner(SignerConfig{
				Issuer: IssuerPrefix + testProject, KeyID: testKeyID, KMS: f.kms, Set: f.set, Tokens: f.verifier,
			}, WithClock(func() time.Time { return f.now }))
			if err != nil {
				t.Fatalf("building the signer: %v", err)
			}
			if _, err := signer.Sign(context.Background(), f.token(t, nil), "https://edge.acme.example", "nonce-1"); err == nil {
				t.Fatal("an assertion was signed under a key the served set did not carry")
			} else if !errors.Is(err, ErrKeyNotServed) {
				t.Fatalf("the refusal reads %q, and it should be ErrKeyNotServed", err)
			} else if !strings.Contains(err.Error(), reason) {
				t.Fatalf("the refusal reads %q, and it should name the %s", err, reason)
			}
			if f.kms.calls != 0 {
				t.Fatal("the key ring signed under a key the served set did not carry")
			}
		})
	}
}

// TestSignRefusesAnAnswerThatIsNotAnEd25519Signature: a KMS answer that is
// short, or an error, is a refusal rather than an assertion assembled around
// bytes no project can verify.
func TestSignRefusesAnAnswerThatIsNotAnEd25519Signature(t *testing.T) {
	f := newFixture(t)
	f.kms.short = true
	if _, err := f.signer.Sign(context.Background(), f.token(t, nil), "https://edge.acme.example", "nonce-1"); err == nil {
		t.Fatal("a 32-byte signature was assembled into an assertion")
	}
	f.kms.short, f.kms.failWith = false, errors.New("permission denied")
	if _, err := f.signer.Sign(context.Background(), f.token(t, nil), "https://edge.acme.example", "nonce-1"); err == nil {
		t.Fatal("an assertion was answered for a signing call that failed")
	}
}

// TestSignReadsTheServedSetTheOwnerPublished: the signer reads the root-signed
// document A4's command writes and A2 serves for the two things it needs — the
// kids it lists and the moment the set stops being valid — and refuses a
// document that is not one, because a set it cannot read is a set that
// published nothing.
//
// The document built here is C1's envelope with its keys nested under `jwks`,
// which is the shape A4's command writes: a reader that looked for `keys` at the
// top of the payload would refuse every set the owner publishes, and the
// identity service would refuse every assertion with it.
func TestSignReadsTheServedSetTheOwnerPublished(t *testing.T) {
	envelope := signJWT(t, rootKey(t), "root-2026", map[string]any{
		"format": mobilesession.KeySetFormat,
		"seq":    7,
		"iat":    time.Now().Unix(),
		"exp":    time.Now().Add(180 * 24 * time.Hour).Unix(),
		"jwks":   map[string]any{"keys": []any{map[string]any{"kty": "OKP", "crv": "Ed25519", "kid": testKeyID}}},
	})
	set, err := ParseServedSet([]byte(envelope))
	if err != nil {
		t.Fatalf("reading the served set: %v", err)
	}
	if !slices.Equal(set.KIDs, []string{testKeyID}) {
		t.Fatalf("the set lists %v, want %s", set.KIDs, testKeyID)
	}
	if !set.ExpiresAt.After(time.Now()) {
		t.Fatalf("the set expires at %v, and 180 days from now is later", set.ExpiresAt)
	}
	for _, doc := range []string{"", "a plain JWKS document", `{"keys":[]}`, "a.b", envelope[:len(envelope)-1] + "x.y"} {
		if _, err := ParseServedSet([]byte(doc)); err == nil {
			t.Fatalf("the set %q was read, and a document that is not one publishes nothing", doc)
		}
	}
	// A document whose payload is a set with no expiry, or with no keys, is not
	// a signed set either: both are read as a refusal rather than as a set that
	// happens to be valid forever.
	for _, claims := range []map[string]any{
		{"format": mobilesession.KeySetFormat, "seq": 7, "jwks": map[string]any{"keys": []any{map[string]any{"kid": testKeyID}}}},
		{"format": mobilesession.KeySetFormat, "seq": 7, "exp": time.Now().Add(time.Hour).Unix(), "jwks": map[string]any{"keys": []any{}}},
	} {
		if _, err := ParseServedSet([]byte(signJWT(t, rootKey(t), "root-2026", claims))); err == nil {
			t.Fatalf("the payload %v was read as a served set", claims)
		}
	}
}

// rootKey builds a throwaway RSA key for the envelopes above: nothing verifies the
// root's signature here (that is A2's and P1's), and what these cases read is
// the payload.
func rootKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	return key
}

// TestSignHandlerAnswersAnAssertion: the route takes a bearer token and the two
// names, answers the assertion, and refuses everything else with a code a
// client can branch on and no value out of the request.
func TestSignHandlerAnswersAnAssertion(t *testing.T) {
	f := newFixture(t)
	handler := NewHandler(f.signer, quietLogs())
	srv := httptest.NewServer(handler.Mux())
	defer srv.Close()
	post := func(t *testing.T, auth, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, srv.URL+RouteAssertion, strings.NewReader(body))
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("posting the request: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	token, challenge := f.token(t, nil), "3b0e6f7a-1c2d-4e3f-8a90-5b6c7d8e9f01"
	resp := post(t, token, fmt.Sprintf(`{"audience":"https://edge.acme.example","challenge":%q}`, challenge))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the assertion route answered %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("the answer is %q, and this service answers JSON", ct)
	}
	var answer AssertionAnswer
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	_, claims := readAssertion(t, f.pub, answer.Assertion)
	if claims["aud"] != "https://edge.acme.example" || claims["challenge"] != challenge {
		t.Fatalf("the answered assertion is bound to %v/%v", claims["aud"], claims["challenge"])
	}

	// A body that is not this route's request, with the reason's code.
	for _, body := range []string{
		`{"audience":"https://edge.acme.example"}`,
		`{"audience":"https://edge.acme.example","challenge":"` + challenge + `","extra":1}`,
		`{"audience":` + challenge + `}`,
		`not JSON`,
	} {
		if resp := post(t, token, body); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("the body %s answered %d, want 400", body, resp.StatusCode)
		}
	}
	// A caller with no bearer token, and one whose token this project did not
	// mint: both are the token's own refusal.
	if resp := post(t, "", `{"audience":"https://edge.acme.example","challenge":"`+challenge+`"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request with no bearer token answered %d, want 401", resp.StatusCode)
	}
	if resp := post(t, "not-a-token", `{"audience":"https://edge.acme.example","challenge":"`+challenge+`"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request with a token nobody minted answered %d, want 401", resp.StatusCode)
	}
	// A route this service does not have, and a method it does not answer.
	req, err := http.NewRequest(http.MethodGet, srv.URL+RouteAssertion, nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if resp, err := http.DefaultClient.Do(req); err != nil {
		t.Fatalf("getting the route: %v", err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("a GET answered %d, want 405", resp.StatusCode)
		}
	}
}

// TestSignHandlerRefusesWhenTheKeySetIsUnreadable: a fault of this service's
// own is answered 503 rather than as a refusal of somebody's token, so an
// operator looks at the deployment and not at a person's phone. The request
// carries a token this project really minted, so what the answer turns on is
// whose fault the read is and not whether a token was there at all.
func TestSignHandlerRefusesWhenTheKeySetIsUnreadable(t *testing.T) {
	f := newFixture(t)
	// The fixture's verifier is replaced by one whose key endpoint cannot be
	// read: the token below is real, and the only thing wrong with this
	// deployment is that it cannot reach Google's published keys.
	verifier, err := NewIDTokenVerifier(testProject, &fakeKeys{fail: errors.New("the key endpoint timed out")},
		WithClock(func() time.Time { return f.now }))
	if err != nil {
		t.Fatalf("building the verifier: %v", err)
	}
	signer, err := NewSigner(SignerConfig{
		Issuer: IssuerPrefix + testProject, KeyID: testKeyID, KMS: f.kms, Set: f.set, Tokens: verifier,
	}, WithClock(func() time.Time { return f.now }))
	if err != nil {
		t.Fatalf("building the signer: %v", err)
	}
	srv := httptest.NewServer(NewHandler(signer, quietLogs()).Mux())
	defer srv.Close()
	req, err := http.NewRequest(http.MethodPost, srv.URL+RouteAssertion,
		strings.NewReader(`{"audience":"https://edge.acme.example","challenge":"nonce-1"}`))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token(t, nil))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("posting the request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a key endpoint that could not be read answered %d, want 503", resp.StatusCode)
	}
}

// TestSignLogsNoToken: a bearer token is a stranger's credential, so no line
// this service writes carries one — and the lines it does write are the
// refusals' own, which is what makes this not a test of silence.
func TestSignLogsNoToken(t *testing.T) {
	f := newFixture(t)
	var lines []string
	handler := NewHandler(f.signer, WithLogger(func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}))
	token := f.token(t, nil)
	srv := httptest.NewServer(handler.Mux())
	defer srv.Close()
	post := func(auth, body string) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+RouteAssertion, strings.NewReader(body))
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("posting the request: %v", err)
		}
		resp.Body.Close()
	}
	post(token, `{"audience":"https://edge.acme.example","challenge":"nonce-1"}`)
	post("a-token-this-service-never-minted", `{"audience":"https://edge.acme.example","challenge":"nonce-1"}`)
	post(token, `{"audience":"https://edge.acme.example"}`)
	if len(lines) == 0 {
		t.Fatal("no refusal was logged at all, so this test would pass by silence")
	}
	for _, line := range lines {
		for _, secret := range []string{token, "a-token-this-service-never-minted", testEmail, testSubject, testUID} {
			if strings.Contains(line, secret) {
				t.Fatalf("a log line carries a value out of the request: %q", line)
			}
		}
	}
}

// TestSignNeedsItsOwnConfiguration: every seam a deployment must hold is
// refused where it is missing, because a signer built without one would report
// the mistake as a refusal of somebody's token.
func TestSignNeedsItsOwnConfiguration(t *testing.T) {
	f := newFixture(t)
	if _, err := NewSigner(SignerConfig{KeyID: testKeyID, KMS: f.kms, Set: f.set, Tokens: f.verifier}); err == nil {
		t.Fatal("a signer was built with no issuer")
	}
	if _, err := NewSigner(SignerConfig{Issuer: "x", KMS: f.kms, Set: f.set, Tokens: f.verifier}); err == nil {
		t.Fatal("a signer was built with no key id")
	}
	if _, err := NewSigner(SignerConfig{Issuer: "x", KeyID: testKeyID, Set: f.set, Tokens: f.verifier}); err == nil {
		t.Fatal("a signer was built with no key ring")
	}
	if _, err := NewSigner(SignerConfig{Issuer: "x", KeyID: testKeyID, KMS: f.kms, Tokens: f.verifier}); err == nil {
		t.Fatal("a signer was built with no served key set")
	}
	if _, err := NewSigner(SignerConfig{Issuer: "x", KeyID: testKeyID, KMS: f.kms, Set: f.set}); err == nil {
		t.Fatal("a signer was built with no ID token verifier")
	}
	if _, err := NewIDTokenVerifier("", f.keys); err == nil {
		t.Fatal("a verifier was built with no project")
	}
	if _, err := NewIDTokenVerifier(testProject, nil); err == nil {
		t.Fatal("a verifier was built with no key set")
	}
}
