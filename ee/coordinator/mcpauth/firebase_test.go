// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

// M6's tests: the Firebase ID token at the authorize endpoint.
//
// Two things are held here, and they are the two the plan's red step and the
// design's §8 lean on. First, the token checks themselves: the issuer is built
// from the project id, the audience is the bare project id, the signature is
// the published key's, and a refusal names the check that failed and never the
// value that failed it. Second, the one consumer: the token is accepted at
// GET /oauth/authorize and is never a grant at POST /oauth/token, and no code
// path in this package mints one.
//
// Every token here is signed by a key the test generates, and the key set the
// verifier reads is served by the test's own server or handed to it as a fixed
// map. Nothing in this tree is a usable key: testdata/mcpauth/jwks.json is the
// published document's SHAPE, with a placeholder where the modulus goes, and
// the test that reads it puts a modulus of its own in. A fixture that held a
// real key would be key material in the repository, which ee/'s own rule
// refuses.

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
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// testProject is the bare Firebase project id: the audience every admitted
	// token carries, and the suffix of its issuer. testOtherProject is another
	// installation's, which no token of this one may name.
	testProject      = "project-a"
	testOtherProject = "project-b"
	testKid          = "published-key-1"
	testUID          = "person-1"
	testEmail        = "person-1@example.com"
	// testMarker is a string that cannot be mistaken for a real token: it is
	// the value the no-echo tests look for in a refusal.
	testMarker = "marker-firebase-id-token-value-do-not-echo"
)

// testNow is the clock every test verifies against, so a token's window is the
// test's own arithmetic rather than the wall clock's.
var testNow = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

// testKey is the key every token here is signed with, generated once: a
// 2048-bit keygen is the slowest thing in this package's tests.
var testKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("mcpauth test: generating a key: " + err.Error())
	}
	return k
})

// staticKeys is a KeySource over a fixed set: the shape of a cached set, with
// the same "not here" answer the real one gives.
type staticKeys map[string]*rsa.PublicKey

func (s staticKeys) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	k, ok := s[kid]
	if !ok {
		// No kid in the error, exactly as the real source does it: the kid is
		// the caller's own text.
		return nil, fmt.Errorf("%w: the token names a key id Google does not publish", ErrUnknownKeyID)
	}
	return k, nil
}

// signJWT returns a compact RS256 JWT signed by key.
func signJWT(t *testing.T, key *rsa.PrivateKey, kid, alg string, claims map[string]any) string {
	t.Helper()
	head, err := json.Marshal(map[string]any{"alg": alg, "kid": kid, "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshalling the header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling the claims: %v", err)
	}
	signing := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// idTokenClaims is a token this installation admits: the identity the project's
// own signer produced, inside its window.
func idTokenClaims(project string) map[string]any {
	return map[string]any{
		"iss":            "https://securetoken.google.com/" + project,
		"aud":            project,
		"sub":            testUID,
		"user_id":        testUID,
		"email":          testEmail,
		"email_verified": true,
		"iat":            testNow.Add(-time.Minute).Unix(),
		"exp":            testNow.Add(time.Hour).Unix(),
	}
}

// verifierOver returns a verifier over the given key set, with the test's own
// clock: no test in this file reads the wall clock.
func verifierOver(t *testing.T, keys KeySource) *Verifier {
	t.Helper()
	v, err := NewVerifier(testProject, keys, WithVerifyClock(func() time.Time { return testNow }))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// acceptedToken is the token the authorize endpoint takes: a well-formed one,
// signed by the published key, for this project.
func acceptedToken(t *testing.T) string {
	t.Helper()
	return signJWT(t, testKey(), testKid, "RS256", idTokenClaims(testProject))
}

// TestTokenFromAnotherProjectIsRefused is M6's red step. A token whose audience
// is another installation's project id is refused, and the refusal names
// neither the token nor that audience: a verifier that admitted it would read
// one project's identity at another project's front door, and a refusal that
// echoed either value would put a token in a log line with a public address.
func TestTokenFromAnotherProjectIsRefused(t *testing.T) {
	v := verifierOver(t, staticKeys{testKid: &testKey().PublicKey})
	tok := signJWT(t, testKey(), testKid, "RS256", idTokenClaims(testOtherProject))

	id, err := v.VerifyIDToken(context.Background(), tok)
	if err == nil {
		t.Fatalf("VerifyIDToken admitted %s, whose audience is another project's", testMarker)
	}
	if !errors.Is(err, ErrUnverifiedIDToken) {
		t.Errorf("VerifyIDToken refused another project's token with %v, which is not a refusal of the identity: a caller cannot tell a token this project will not read from a key set it could not read", err)
	}
	if id != (Identity{}) {
		t.Errorf("VerifyIDToken answered an identity beside a refusal: %+v", id)
	}
	msg := err.Error()
	if strings.Contains(msg, tok) {
		t.Errorf("the refusal names the token itself: %q", msg)
	}
	if strings.Contains(msg, testOtherProject) {
		t.Errorf("the refusal names the audience that failed it (%q): which project a token was minted for is a fact about somebody else's installation", msg)
	}
}

// TestWrongIssuerIsRefused: the issuer is built from the project id, so a token
// that names another project's issuer is refused even when its audience is this
// project's.
func TestWrongIssuerIsRefused(t *testing.T) {
	v := verifierOver(t, staticKeys{testKid: &testKey().PublicKey})
	claims := idTokenClaims(testProject)
	claims["iss"] = "https://securetoken.google.com/" + testOtherProject

	if _, err := v.VerifyIDToken(context.Background(), signJWT(t, testKey(), testKid, "RS256", claims)); !errors.Is(err, ErrUnverifiedIDToken) {
		t.Errorf("a token issued by another project's Firebase was refused with %v, which is not a refusal of the identity", err)
	}
}

// TestExpiredTokenIsRefused: the window is the token's own, and it is read
// against the verifier's clock.
func TestExpiredTokenIsRefused(t *testing.T) {
	v := verifierOver(t, staticKeys{testKid: &testKey().PublicKey})
	claims := idTokenClaims(testProject)
	claims["exp"] = testNow.Add(-time.Minute).Unix()

	if _, err := v.VerifyIDToken(context.Background(), signJWT(t, testKey(), testKid, "RS256", claims)); !errors.Is(err, ErrUnverifiedIDToken) {
		t.Errorf("an expired token was refused with %v, which is not a refusal of the identity", err)
	}
}

// TestATokenSignedByAnotherKeyIsRefused: the set holds a key for the token's
// key id, and it is not the one that signed. The kid is a hint and never the
// proof.
func TestATokenSignedByAnotherKeyIsRefused(t *testing.T) {
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a second key: %v", err)
	}
	v := verifierOver(t, staticKeys{testKid: &other.PublicKey})

	if _, err := v.VerifyIDToken(context.Background(), acceptedToken(t)); !errors.Is(err, ErrUnverifiedIDToken) {
		t.Errorf("a token signed by another key than the published one was refused with %v, which is not a refusal of the identity", err)
	}
}

// TestRefusalNeverEchoesTheToken: every refusal path, over malformed tokens and
// refused claims alike, and not one of them repeats the token, any part of it,
// or a value the token carried.
func TestRefusalNeverEchoesTheToken(t *testing.T) {
	v := verifierOver(t, staticKeys{testKid: &testKey().PublicKey})

	expired := idTokenClaims(testProject)
	expired["exp"] = testNow.Add(-time.Minute).Unix()
	unverifiedEmail := idTokenClaims(testProject)
	unverifiedEmail["email_verified"] = false
	wrongAudience := idTokenClaims(testProject)
	wrongAudience["aud"] = testOtherProject
	future := idTokenClaims(testProject)
	future["iat"] = testNow.Add(2 * time.Hour).Unix()
	notYet := idTokenClaims(testProject)
	notYet["nbf"] = testNow.Add(2 * time.Hour).Unix()
	noUID := idTokenClaims(testProject)
	delete(noUID, "sub")
	twoUIDs := idTokenClaims(testProject)
	twoUIDs["user_id"] = "person-2"

	cases := map[string]string{
		"empty":                           "",
		"not a jwt":                       testMarker,
		"two segments":                    "eyJhbGciOiJSUzI1NiJ9." + testMarker,
		"a segment that is not base64url": "!!." + testMarker + ".!!",
		"a key id the set does not hold":  signJWT(t, testKey(), "another-kid", "RS256", idTokenClaims(testProject)),
		"a token signed another way":      signJWT(t, testKey(), testKid, "HS256", idTokenClaims(testProject)),
		"an expired token":                signJWT(t, testKey(), testKid, "RS256", expired),
		"an unverified address":           signJWT(t, testKey(), testKid, "RS256", unverifiedEmail),
		"another audience":                signJWT(t, testKey(), testKid, "RS256", wrongAudience),
		"issued in the future":            signJWT(t, testKey(), testKid, "RS256", future),
		"not valid yet":                   signJWT(t, testKey(), testKid, "RS256", notYet),
		"no uid":                          signJWT(t, testKey(), testKid, "RS256", noUID),
		"two different uids":              signJWT(t, testKey(), testKid, "RS256", twoUIDs),
	}

	for name, tok := range cases {
		_, err := v.VerifyIDToken(context.Background(), tok)
		if err == nil {
			t.Errorf("%s: VerifyIDToken admitted a token it must refuse (%s)", name, testMarker)
			continue
		}
		msg := err.Error()
		// The first eight characters are the head of the token's own header
		// segment: a message that carried them would be quoting the token even
		// though it never carried the whole string.
		for _, forbidden := range []string{tok, testMarker, testProject, testEmail} {
			if forbidden != "" && strings.Contains(msg, forbidden) {
				t.Errorf("%s: the refusal carries a value out of the token or the project: %q", name, msg)
			}
		}
		if len(tok) >= 8 && strings.Contains(msg, tok[:8]) {
			t.Errorf("%s: the refusal carries the head of the token: %q", name, msg)
		}
	}
}

// TestTheIdentityChecksAreRefusals holds the checks the credentials service
// already applies to an identity this repository trusts: an unverified address,
// an address that is not printable ASCII, a uid the claims do not agree on, and
// a window that has not opened.
func TestTheIdentityChecksAreRefusals(t *testing.T) {
	v := verifierOver(t, staticKeys{testKid: &testKey().PublicKey})

	cases := map[string]func(claims map[string]any){
		"no address":                   func(c map[string]any) { delete(c, "email") },
		"an empty address":             func(c map[string]any) { c["email"] = "" },
		"an address with a space":      func(c map[string]any) { c["email"] = "person-1 @example.com" },
		"an address that is not ASCII": func(c map[string]any) { c["email"] = "person-1@exаmple.com" },
		"an oversized address":         func(c map[string]any) { c["email"] = strings.Repeat("a", 512) + "@example.com" },
		"no user_id":                   func(c map[string]any) { delete(c, "user_id") },
		"no expiry":                    func(c map[string]any) { delete(c, "exp") },
	}
	for name, mutate := range cases {
		claims := idTokenClaims(testProject)
		mutate(claims)
		if _, err := v.VerifyIDToken(context.Background(), signJWT(t, testKey(), testKid, "RS256", claims)); !errors.Is(err, ErrUnverifiedIDToken) {
			t.Errorf("%s: refused with %v, which is not a refusal of the identity", name, err)
		}
	}
}

// TestTheAdmittedIdentityIsTheTokensOwn: what a caller gets back is the uid and
// the folded address, so the list lookup that follows (D933) compares what the
// token actually asserted.
func TestTheAdmittedIdentityIsTheTokensOwn(t *testing.T) {
	v := verifierOver(t, staticKeys{testKid: &testKey().PublicKey})
	claims := idTokenClaims(testProject)
	claims["email"] = "Person-1@Example.COM"

	id, err := v.VerifyIDToken(context.Background(), signJWT(t, testKey(), testKid, "RS256", claims))
	if err != nil {
		t.Fatalf("VerifyIDToken refused a token this project admits: %v", err)
	}
	if id.UID != testUID || id.Email != testEmail {
		t.Errorf("VerifyIDToken answered %+v, and the identity a token asserts is its uid and its folded address", id)
	}
}

// TestAVerifierNeedsAProjectAndAKeySet: an unconfigured verifier is refused at
// construction rather than becoming one that admits nothing or, worse, one
// nobody can tell is unconfigured.
func TestAVerifierNeedsAProjectAndAKeySet(t *testing.T) {
	if _, err := NewVerifier("", staticKeys{}); err == nil {
		t.Error("NewVerifier accepted an empty project id: the issuer and the audience are both built from it, so a verifier without one is a verifier that refuses everything while looking configured")
	}
	if _, err := NewVerifier(testProject, nil); err == nil {
		t.Error("NewVerifier accepted a nil key set: a verifier with nowhere to read a key is one whose refusals look like a stranger's bad token")
	}
}

// TestTheTokenEndpointRefusesAFirebaseIDToken: the ID token has exactly one
// consumer. The same token the authorize endpoint takes is refused as a grant
// at the token endpoint, because a grant is an authorization code or a refresh
// token and the ID token is neither.
func TestTheTokenEndpointRefusesAFirebaseIDToken(t *testing.T) {
	v := verifierOver(t, staticKeys{testKid: &testKey().PublicKey})
	tok := acceptedToken(t)
	if _, err := v.VerifyIDToken(context.Background(), tok); err != nil {
		t.Fatalf("the authorize endpoint refused the token this test signs: %v", err)
	}

	for _, want := range []GrantType{GrantAuthorizationCode, GrantRefreshToken, GrantClientCredentials} {
		if got, err := ParseGrantType(string(want)); err != nil || got != want {
			t.Errorf("ParseGrantType(%q) = %q, %v; the token endpoint's own grants are the three D929 and D930 name", want, got, err)
		}
	}
	for _, grant := range []string{
		tok,
		"firebase_id_token",
		"id_token",
		"urn:ietf:params:oauth:grant-type:firebase-id-token",
		"",
		"password",
	} {
		got, err := ParseGrantType(grant)
		if err == nil {
			t.Errorf("ParseGrantType(%s) admitted a grant type that is not one of the endpoint's three: a Firebase ID token presented as a grant is the one thing the endpoint must never read", testMarker)
			continue
		}
		if !errors.Is(err, ErrUnsupportedGrant) {
			t.Errorf("ParseGrantType refused with %v, which is not the endpoint's own sentinel: the token endpoint answers unsupported_grant_type from it", err)
		}
		if got != "" {
			t.Errorf("ParseGrantType(%s) answered %q beside a refusal", testMarker, got)
		}
		if strings.Contains(err.Error(), tok) || strings.Contains(err.Error(), grant) && grant != "" {
			t.Errorf("the refusal repeats the grant that failed it: %q", err)
		}
	}
}

// TestTheCoordinatorMintsNoCustomToken: the other half of D923's answer. The
// paired device signs a person in and the coordinator only verifies what that
// session produced, so no path here holds a signing key or reaches a signer.
//
// A negative about a code path is not something a call can prove, so this is a
// scan of the package's own source, in the shape internal/eeboundary uses for
// the licence boundary: it reads text, at one commit, and it names what it
// cannot see (a build step, a second module, a file copied in later). What it
// can see is every substring only a minting path has.
func TestTheCoordinatorMintsNoCustomToken(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package's sources: %v", err)
	}
	read := 0
	for _, name := range sources {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		read++
		for _, forbidden := range []string{
			"SignPKCS1v15",
			"PRIVATE KEY",
			"rsa.PrivateKey",
			"crypto.Signer",
			"iamcredentials",
			"serviceAccountTokenCreator",
			"CustomToken",
			"firebase.google.com/go",
		} {
			if strings.Contains(string(body), forbidden) {
				t.Errorf("%s carries %q: the coordinator holds no signer role and mints no Firebase token of its own, so a token this package made would be an identity the paired device never approved", name, forbidden)
			}
		}
	}
	if read == 0 {
		t.Fatalf("the scan read no source at all: a rule that finds nothing because it looked nowhere is a rule that passes while the package ships a minter")
	}
}

// TestThePublishedKeySetIsReadFromTheDocumentShape: the key source reads the
// document Google publishes, and the fixture holds that document's shape and no
// usable key.
func TestThePublishedKeySetIsReadFromTheDocumentShape(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "mcpauth", "jwks.json"))
	if err != nil {
		t.Fatalf("reading the published set's fixture: %v", err)
	}

	// As it stands the fixture carries a placeholder where the modulus goes, so
	// this tree holds no key a token could be signed with.
	unusable := keysOver(t, raw)
	if key, err := unusable.Key(context.Background(), testKid); err == nil {
		t.Fatalf("the fixture's placeholder key was read as a usable key (%v): a fixture that holds a real key is key material in the repository", key)
	} else if !errors.Is(err, ErrUnavailable) {
		t.Errorf("a set this code cannot read was refused with %v: an unreadable key set is the service's failure and not the token's, and the two are told apart downstream", err)
	}

	// With this test's own modulus in the placeholder's place, the same document
	// verifies a token: what the fixture pins is the shape, which is what a
	// checked-in document can honestly pin.
	keys := keysOver(t, jwksWithModulus(t, raw, &testKey().PublicKey))
	key, err := keys.Key(context.Background(), testKid)
	if err != nil {
		t.Fatalf("the published set's document shape was not read: %v", err)
	}
	if key.N.Cmp(testKey().PublicKey.N) != 0 || key.E != testKey().PublicKey.E {
		t.Fatalf("the key read out of the document is not the one the document carried: n=%v e=%d", key.N, key.E)
	}
	if _, err := verifierOver(t, keys).VerifyIDToken(context.Background(), acceptedToken(t)); err != nil {
		t.Errorf("a token signed by the published key was refused: %v", err)
	}
}

// TestAnUnreadableKeySetIsNotATokenRefusal: the two failures a verifier can
// have are told apart. A key endpoint that answered nothing, or answered
// something this code cannot read, is ErrUnavailable; a token naming a key
// nobody published is a refusal of the token itself.
func TestAnUnreadableKeySetIsNotATokenRefusal(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)

	v := verifierOver(t, NewSecureTokenKeys(
		WithKeysEndpoint(failing.URL),
		WithKeysClock(func() time.Time { return testNow }),
	))
	_, err := v.VerifyIDToken(context.Background(), acceptedToken(t))
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("a key endpoint that answered 500 produced %v, which is not the service's own failure", err)
	}
	if errors.Is(err, ErrUnverifiedIDToken) {
		t.Errorf("a key endpoint that answered 500 was reported as a refusal of the token: an operator reading that would look for a bad token where the fault is the key set")
	}

	unknown := keysOver(t, jwksFor(t, map[string]*rsa.PublicKey{testKid: &testKey().PublicKey}))
	_, err = verifierOver(t, unknown).VerifyIDToken(context.Background(), signJWT(t, testKey(), "another-kid", "RS256", idTokenClaims(testProject)))
	if !errors.Is(err, ErrUnverifiedIDToken) || !errors.Is(err, ErrUnknownKeyID) {
		t.Errorf("a token naming a key nobody published produced %v, and it is a refusal of that token and of nothing else", err)
	}
}

// serveKeys answers with one document, at the endpoint the key source is
// pointed at. It is where Google's own endpoint would be, and nothing in this
// test reaches the network.
func serveKeys(t *testing.T, doc []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(doc); err != nil {
			t.Errorf("writing the key set: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// keysOver is a key source over one served document.
func keysOver(t *testing.T, doc []byte) *SecureTokenKeys {
	t.Helper()
	srv := serveKeys(t, doc)
	return NewSecureTokenKeys(
		WithKeysEndpoint(srv.URL),
		WithKeysClock(func() time.Time { return testNow }),
	)
}

// jwksFor renders the published document for a set of keys, in the shape the
// fixture carries.
func jwksFor(t *testing.T, keys map[string]*rsa.PublicKey) []byte {
	t.Helper()
	entries := make([]map[string]string, 0, len(keys))
	for kid, key := range keys {
		entries = append(entries, map[string]string{
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		})
	}
	doc, err := json.Marshal(map[string]any{"keys": entries})
	if err != nil {
		t.Fatalf("rendering the published set: %v", err)
	}
	return doc
}

// jwksWithModulus puts key's modulus where the fixture's placeholder is.
func jwksWithModulus(t *testing.T, raw []byte, key *rsa.PublicKey) []byte {
	t.Helper()
	var doc struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the published set's fixture is not the document this code reads: %v", err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("the fixture carries %d keys and this test substitutes one modulus", len(doc.Keys))
	}
	doc.Keys[0]["n"] = base64.RawURLEncoding.EncodeToString(key.N.Bytes())
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("rewriting the published set: %v", err)
	}
	return out
}
