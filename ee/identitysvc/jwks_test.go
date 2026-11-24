// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package identitysvc

// The published key material (plan A2): the plain JWKS, the root-signed key-set
// document served beside it, and the rotation that decides which signing keys
// are in either one at a moment.
//
// The keys here are generated inside this test's own process and their private
// halves never leave it — nothing under ee/ holds, reads or reaches a signing
// key (.claude/rules/ee.md R5). The document the file-backed tests serve is
// bytes no signature is over: this service publishes what A4's command wrote
// and never reads its signature, which is a consumer's work (P1), and a test
// whose document verified would be holding the one property this half must not
// have.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// day is one rotation day, so the cadence the tests step through reads as the
// plan states it (90-day period, 30-day overlap).
const day = 24 * time.Hour

// rotationStart is the instant every test's first key started signing.
var rotationStart = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// signingKeyAt generates a throwaway key whose public half is what the tests
// publish, and hands back the private half so a test can assert it is nowhere
// in the document.
func signingKeyAt(t *testing.T, kid string, notBefore time.Time) (SigningKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating the %s key: %v", kid, err)
	}
	return SigningKey{KID: kid, Public: pub, NotBefore: notBefore}, priv
}

// kids is the key ids of a set, in the order the set holds them.
func kids(keys []SigningKey) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.KID)
	}
	return out
}

// signedKeySetDocument builds the envelope C1's vector fixes in the shape this
// half reads: a JWS compact serialization whose payload carries the claims, and
// a signature segment that is not one.
func signedKeySetDocument(t *testing.T, claims map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshalling the claims: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA"}`))
	signature := base64.RawURLEncoding.EncodeToString([]byte("not a signature"))
	return []byte(header + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + signature)
}

// keySetWith builds the published set over a document written to a temporary
// file, with the clock pinned to now.
func keySetWith(t *testing.T, keys []SigningKey, document []byte, now time.Time) *KeySet {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keyset.jws")
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatalf("writing the signed key set: %v", err)
	}
	source, err := NewFileKeySet(path)
	if err != nil {
		t.Fatalf("reading the signed key set from %s: %v", path, err)
	}
	rotation, err := NewRotation(keys)
	if err != nil {
		t.Fatalf("building the rotation: %v", err)
	}
	ks, err := NewKeySet(rotation, source, WithKeySetClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("building the key set: %v", err)
	}
	return ks
}

// TestRotationPublishesTheOverlap is the task's red step: with only the current
// key published, the key an assertion minted a moment before the rotation names
// is nowhere, and every verifier holding that assertion refuses it until it
// fetches again. The previous key is published for the overlap, and for exactly
// that long.
func TestRotationPublishesTheOverlap(t *testing.T) {
	first, _ := signingKeyAt(t, "k1", rotationStart)
	second, _ := signingKeyAt(t, "k2", rotationStart.Add(RotationPeriod))
	// The keys arrive in the order a store hands them back, which is not the
	// order they started in.
	rotation, err := NewRotation([]SigningKey{second, first})
	if err != nil {
		t.Fatalf("building the rotation: %v", err)
	}

	at := func(d time.Duration) []string { return kids(rotation.Published(second.NotBefore.Add(d))) }

	if got := at(-time.Hour); !slices.Equal(got, []string{"k1"}) {
		t.Errorf("an hour before the second key started, the set is %v, and a key that has not started signing is not published", got)
	}
	if got := at(time.Hour); !slices.Equal(got, []string{"k2", "k1"}) {
		t.Errorf("an hour into the overlap the set is %v, and an assertion minted an hour ago names k1", got)
	}
	if got := at(RotationOverlap - time.Second); !slices.Equal(got, []string{"k2", "k1"}) {
		t.Errorf("a second before the overlap ends the set is %v, and the last instant of the overlap still publishes the previous key", got)
	}
	if got := at(RotationOverlap); !slices.Equal(got, []string{"k2"}) {
		t.Errorf("at the instant the overlap ends the set is %v, and a key past the overlap is not published", got)
	}
}

// TestRotationPublishesOnlyTheKeyBeforeTheCurrent holds the other end of the
// overlap: a rotation is the current key and the one it replaced, never a third
// — a key two rotations old is one no assertion inside the assertion's own life
// can name.
func TestRotationPublishesOnlyTheKeyBeforeTheCurrent(t *testing.T) {
	first, _ := signingKeyAt(t, "k1", rotationStart)
	second, _ := signingKeyAt(t, "k2", rotationStart.Add(RotationPeriod))
	third, _ := signingKeyAt(t, "k3", rotationStart.Add(2*RotationPeriod))
	rotation, err := NewRotation([]SigningKey{first, second, third})
	if err != nil {
		t.Fatalf("building the rotation: %v", err)
	}
	if got := kids(rotation.Published(third.NotBefore.Add(time.Hour))); !slices.Equal(got, []string{"k3", "k2"}) {
		t.Errorf("an hour into the third key's overlap the set is %v, and the key two rotations back is past its own overlap", got)
	}
}

// TestRotationCurrent names the key that signs at an instant: the newest one
// that has started, and none at all before the first key's own start.
func TestRotationCurrent(t *testing.T) {
	first, _ := signingKeyAt(t, "k1", rotationStart)
	second, _ := signingKeyAt(t, "k2", rotationStart.Add(RotationPeriod))
	rotation, err := NewRotation([]SigningKey{first, second})
	if err != nil {
		t.Fatalf("building the rotation: %v", err)
	}
	for _, tc := range []struct {
		name string
		at   time.Time
		want string
		any  bool
	}{
		{"before the first key", rotationStart.Add(-time.Hour), "", false},
		{"at the first key's start", rotationStart, "k1", true},
		{"during the overlap", second.NotBefore.Add(time.Hour), "k2", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rotation.Current(tc.at)
			if ok != tc.any {
				t.Fatalf("Current(%s) says %v, and this instant %s a signing key", tc.at, ok, map[bool]string{true: "has", false: "has no"}[tc.any])
			}
			if ok && got.KID != tc.want {
				t.Errorf("Current(%s) is %s, and the newest key that has started is %s", tc.at, got.KID, tc.want)
			}
		})
	}
}

// TestRotationRefusesAnUnusableSet: a rotation that cannot publish what it was
// handed is refused where it is built, and not at the first fetch, because the
// failure it would otherwise produce is a set a verifier reads and refuses
// every assertion against.
func TestRotationRefusesAnUnusableSet(t *testing.T) {
	good, _ := signingKeyAt(t, "k1", rotationStart)
	other, _ := signingKeyAt(t, "k2", rotationStart.Add(RotationPeriod))
	short, _ := signingKeyAt(t, "k3", rotationStart.Add(2*RotationPeriod))
	short.Public = short.Public[:16]
	nameless := good
	nameless.KID = ""
	publess := good
	publess.Public = nil
	undated := good
	undated.NotBefore = time.Time{}

	for _, tc := range []struct {
		name string
		keys []SigningKey
	}{
		{"no key at all", nil},
		{"a key with no key id", []SigningKey{nameless}},
		{"a key with no public half", []SigningKey{publess}},
		{"a public half that is not an Ed25519 one", []SigningKey{short}},
		{"a key with no start time", []SigningKey{undated}},
		{"two keys under one key id", []SigningKey{good, good}},
		{"two distinct keys under one key id", []SigningKey{good, {KID: good.KID, Public: other.Public, NotBefore: other.NotBefore}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewRotation(tc.keys); err == nil {
				t.Fatal("the rotation was built, and a set this service cannot publish is one it must refuse where it is built")
			}
		})
	}
}

// TestRotationRefusesTwoKeysAtOneInstant: with two keys that started signing at
// the same instant, the order between them is the store's and not the set's, so
// Current — and with it the key an assertion is signed by — would be whichever
// of the two the store happened to hand back first, and the same set would sign
// differently from one read to the next. A rotation puts one key after another
// in time, so a tie is refused where the set is built, in either order, and the
// refusal names both keys that collided.
func TestRotationRefusesTwoKeysAtOneInstant(t *testing.T) {
	first, _ := signingKeyAt(t, "k1", rotationStart)
	tied, _ := signingKeyAt(t, "k2", rotationStart)
	for _, keys := range [][]SigningKey{{first, tied}, {tied, first}} {
		_, err := NewRotation(keys)
		if err == nil {
			t.Fatalf("a rotation was built over %v, and two keys at one instant have no order the set itself states", kids(keys))
		}
		for _, kid := range []string{"k1", "k2"} {
			if !strings.Contains(err.Error(), kid) {
				t.Errorf("the refusal says %q, and it must name both keys that collided: %s is missing", err, kid)
			}
		}
	}
}

// TestJWKSPublishesTheCurrentKeys: what a reader of the plain set gets is the
// keys of the moment, in RFC 7517's OKP form, with the key id a verifier looks
// a token's header up by.
func TestJWKSPublishesTheCurrentKeys(t *testing.T) {
	first, _ := signingKeyAt(t, "k1", rotationStart)
	second, _ := signingKeyAt(t, "k2", rotationStart.Add(RotationPeriod))
	ks := keySetWith(t, []SigningKey{first, second}, signedKeySetDocument(t, map[string]any{"exp": 1}), second.NotBefore.Add(time.Hour))

	document, err := json.Marshal(ks.JWKS())
	if err != nil {
		t.Fatalf("marshalling the JWKS: %v", err)
	}
	var published struct {
		Keys []struct {
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			Kid string `json:"kid"`
			X   string `json:"x"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(document, &published); err != nil {
		t.Fatalf("reading back %s: %v", document, err)
	}
	if len(published.Keys) != 2 {
		t.Fatalf("the set holds %d key(s) during the overlap, and both the current and the previous key are published: %s", len(published.Keys), document)
	}
	if got := []string{published.Keys[0].Kid, published.Keys[1].Kid}; !slices.Equal(got, []string{"k2", "k1"}) {
		t.Errorf("the set names %v, and the current key comes first", got)
	}
	for i, want := range []SigningKey{second, first} {
		got := published.Keys[i]
		if got.Kty != "OKP" || got.Crv != "Ed25519" {
			t.Errorf("key %s is %s/%s, and an Ed25519 key is OKP/Ed25519", got.Kid, got.Kty, got.Crv)
		}
		if want := base64.RawURLEncoding.EncodeToString(want.Public); got.X != want {
			t.Errorf("key %s publishes x=%q, and its public half is %q", got.Kid, got.X, want)
		}
	}
}

// TestJWKSCarriesNoPrivateMaterial: the published document holds a key id and a
// public half, and nothing of the private half — a set that carried one would
// publish the one thing the whole design keeps in Cloud KMS.
func TestJWKSCarriesNoPrivateMaterial(t *testing.T) {
	key, priv := signingKeyAt(t, "k1", rotationStart)
	ks := keySetWith(t, []SigningKey{key}, signedKeySetDocument(t, map[string]any{"exp": 1}), rotationStart.Add(time.Hour))
	document, err := json.Marshal(ks.JWKS())
	if err != nil {
		t.Fatalf("marshalling the JWKS: %v", err)
	}
	if bytes.Contains(document, priv) || bytes.Contains(document, priv.Seed()) {
		t.Fatal("the published set carries the private half, and the private half never leaves Cloud KMS")
	}
	var raw struct {
		Keys []map[string]json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(document, &raw); err != nil {
		t.Fatalf("reading back %s: %v", document, err)
	}
	for _, key := range raw.Keys {
		for member := range key {
			if !slices.Contains([]string{"kty", "crv", "kid", "x"}, member) {
				t.Errorf("a published key carries a %q member, and a public JWK is kty, crv, kid and x", member)
			}
		}
	}
}

// TestJWKSIsEmptyBeforeTheFirstKey: a service whose first key has not started
// answers an empty set rather than a null one, so a reader that iterates what
// it fetched iterates nothing.
func TestJWKSIsEmptyBeforeTheFirstKey(t *testing.T) {
	key, _ := signingKeyAt(t, "k1", rotationStart)
	ks := keySetWith(t, []SigningKey{key}, signedKeySetDocument(t, map[string]any{"exp": 1}), rotationStart.Add(-time.Hour))
	document, err := json.Marshal(ks.JWKS())
	if err != nil {
		t.Fatalf("marshalling the JWKS: %v", err)
	}
	if string(document) != `{"keys":[]}` {
		t.Errorf("the set before the first key is %s, and an empty set is an empty list", document)
	}
}

// TestJWKSServesTheSignedKeySetUnchanged: the pinned URL answers A4's document
// byte for byte, and the plain JWKS is beside it. This half holds no root key
// and cannot sign one (O7), so a byte it changed would be a document no root
// key ever signed.
func TestJWKSServesTheSignedKeySetUnchanged(t *testing.T) {
	key, _ := signingKeyAt(t, "k1", rotationStart)
	document := signedKeySetDocument(t, map[string]any{"seq": 4, "iat": 1, "exp": 2})
	ks := keySetWith(t, []SigningKey{key}, document, rotationStart.Add(time.Hour))

	mux := http.NewServeMux()
	ks.Mount(mux)

	signed := get(t, mux, RouteKeySet)
	if !bytes.Equal(signed.body, document) {
		t.Errorf("the pinned URL answers %q, and A4's document is %q", signed.body, document)
	}
	if signed.code != http.StatusOK {
		t.Errorf("the pinned URL answers %d, and a published document is a 200", signed.code)
	}
	if signed.contentType != "application/jose" {
		t.Errorf("the pinned URL answers Content-Type %q, and a JWS travels as application/jose", signed.contentType)
	}

	plain := get(t, mux, RouteJWKS)
	if plain.code != http.StatusOK {
		t.Errorf("the JWKS URL answers %d, and a published set is a 200", plain.code)
	}
	if plain.contentType != "application/jwk-set+json" {
		t.Errorf("the JWKS URL answers Content-Type %q, and a JWK set travels as application/jwk-set+json", plain.contentType)
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(plain.body, &set); err != nil {
		t.Fatalf("the JWKS URL answers %q, which is not a key set: %v", plain.body, err)
	}
	if len(set.Keys) != 1 || set.Keys[0].Kid != "k1" {
		t.Errorf("the JWKS URL answers %q, and it is the key the rotation publishes", plain.body)
	}
}

// TestJWKSRefusesAMethodItDoesNotServe: both documents are reads, and a write
// to either is refused where it is routed rather than by a handler.
func TestJWKSRefusesAMethodItDoesNotServe(t *testing.T) {
	key, _ := signingKeyAt(t, "k1", rotationStart)
	ks := keySetWith(t, []SigningKey{key}, signedKeySetDocument(t, map[string]any{"exp": 1}), rotationStart.Add(time.Hour))
	mux := http.NewServeMux()
	ks.Mount(mux)
	for _, path := range []string{RouteKeySet, RouteJWKS} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s answers %d, and neither document has a write", path, rec.Code)
		}
	}
}

// TestJWKSDaysLeft: the figure A3's alert fires on is the whole days between
// now and the newest signed set's expiry, and a set past its expiry is a figure
// below zero rather than one that reads as valid.
func TestJWKSDaysLeft(t *testing.T) {
	key, _ := signingKeyAt(t, "k1", rotationStart)
	expiry := rotationStart.Add(180 * day)
	document := signedKeySetDocument(t, map[string]any{"seq": 1, "iat": rotationStart.Unix(), "exp": expiry.Unix()})

	for _, tc := range []struct {
		name string
		now  time.Time
		want int
	}{
		{"the day it was signed", rotationStart, 180},
		{"thirty days before it expires", expiry.Add(-30 * day), 30},
		{"a second inside thirty days", expiry.Add(-30*day + time.Second), 29},
		{"the instant it expires", expiry, 0},
		{"an hour past its expiry", expiry.Add(time.Hour), -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ks := keySetWith(t, []SigningKey{key}, document, tc.now)
			got, err := ks.DaysLeft(context.Background())
			if err != nil {
				t.Fatalf("reading the days left: %v", err)
			}
			if got != tc.want {
				t.Errorf("on %s the set has %d day(s) left, and it expires on %s", tc.now, got, expiry)
			}
		})
	}
}

// TestJWKSRefusesAnUnreadableSet: a document this half cannot read its expiry
// out of is an error and not a figure, because a set that reports no expiry is
// one A3's alert never fires on.
func TestJWKSRefusesAnUnreadableSet(t *testing.T) {
	key, _ := signingKeyAt(t, "k1", rotationStart)
	for _, tc := range []struct {
		name     string
		document []byte
	}{
		{"an empty document", nil},
		{"a document that is not a JWS", []byte("not a signed key set")},
		{"a JWS with two segments", []byte("eyJhbGciOiJFZERTQSJ9.eyJleHAiOjF9")},
		{"a payload that is not JSON", []byte("eyJhbGciOiJFZERTQSJ9." + base64.RawURLEncoding.EncodeToString([]byte("{")) + ".c2ln")},
		{"a payload with no expiry", signedKeySetDocument(t, map[string]any{"seq": 1})},
		{"a payload whose expiry is zero", signedKeySetDocument(t, map[string]any{"exp": 0})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ks := keySetWith(t, []SigningKey{key}, tc.document, rotationStart.Add(time.Hour))
			if _, err := ks.DaysLeft(context.Background()); err == nil {
				t.Fatal("the days left were read out of a document that carries no expiry")
			}
		})
	}
}

// TestKeySetExpiryReadsC1sVector holds keySetExpiry to the envelope C1 fixed:
// the vector file is the contract's data half, the app's tests read the same
// bytes, and the one member this half reads out of a set is the payload's
// `exp`. Feeding the vector's own compact serialization through the reader is
// what keeps the figure A3's alert fires on the one the other side signed,
// rather than a shape this half invented. The file carries the signing input as
// its base64url halves and the signature beside them, so the compact form the
// reader takes is assembled here: a three-part token committed to the file is a
// shape a secret scanner refuses (images/trivy-secret.yaml), and the catalog
// build's scan is the gate this repository must not be the first to fail.
func TestKeySetExpiryReadsC1sVector(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean("../../shared/mobile-session/vectors.json"))
	if err != nil {
		t.Fatalf("reading the contract's vectors: %v", err)
	}
	var vectors struct {
		KeySet struct {
			SigningInputParts []string `json:"signing_input_parts"`
			Signature         string   `json:"signature"`
			Payload           struct {
				Exp int64 `json:"exp"`
			} `json:"payload"`
		} `json:"key_set"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatalf("decoding the contract's vectors: %v", err)
	}
	if len(vectors.KeySet.SigningInputParts) != 2 || vectors.KeySet.Signature == "" || vectors.KeySet.Payload.Exp == 0 {
		t.Fatal("the vectors carry no signed key set with an expiry, and the form this half reads is the one they fix")
	}
	parts := append(slices.Clone(vectors.KeySet.SigningInputParts), vectors.KeySet.Signature)
	compact := strings.Join(parts, ".")
	expiry, err := keySetExpiry([]byte(compact))
	if err != nil {
		t.Fatalf("reading the expiry of the vector's signed key set: %v", err)
	}
	if want := time.Unix(vectors.KeySet.Payload.Exp, 0).UTC(); !expiry.Equal(want) {
		t.Errorf("the vector's key set expires at %s, and keySetExpiry read %s", want, expiry)
	}
}

// TestJWKSRefusesWhatItCannotServe: a set built with nothing to publish, and a
// document source that answers nothing — no file, or a file holding no bytes —
// are both refused rather than served as an empty answer a verifier would read
// as a real one.
func TestJWKSRefusesWhatItCannotServe(t *testing.T) {
	key, _ := signingKeyAt(t, "k1", rotationStart)
	rotation, err := NewRotation([]SigningKey{key})
	if err != nil {
		t.Fatalf("building the rotation: %v", err)
	}
	source, err := NewFileKeySet(filepath.Join(t.TempDir(), "keyset.jws"))
	if err != nil {
		t.Fatalf("building the file source: %v", err)
	}
	if _, err := NewKeySet(nil, source); err == nil {
		t.Error("a key set was built over no rotation")
	}
	if _, err := NewKeySet(rotation, nil); err == nil {
		t.Error("a key set was built with no document to serve")
	}
	if _, err := NewFileKeySet(""); err == nil {
		t.Error("a file source was built over an empty path")
	}

	// The document is not there — the state a service is in before the owner's
	// first signing command — and then it is there and holds nothing, which is
	// what a write that was cut short leaves behind. Neither is a key set, and
	// serving either would answer a verifier with a document no root key
	// signed: both readings fail rather than answer, and the pinned URL tells
	// the verifier to come back.
	empty := filepath.Join(t.TempDir(), "keyset.jws")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("writing an empty signed key set: %v", err)
	}
	emptySource, err := NewFileKeySet(empty)
	if err != nil {
		t.Fatalf("building the file source over %s: %v", empty, err)
	}

	for _, tc := range []struct {
		name   string
		source SignedKeySet
	}{
		{"no document published", source},
		{"an empty document", emptySource},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ks, err := NewKeySet(rotation, tc.source, WithKeySetClock(func() time.Time { return rotationStart.Add(time.Hour) }))
			if err != nil {
				t.Fatalf("building the key set: %v", err)
			}
			if _, err := ks.DaysLeft(context.Background()); err == nil {
				t.Error("the days left were read out of a document that carries no expiry")
			}
			mux := http.NewServeMux()
			ks.Mount(mux)
			if got := get(t, mux, RouteKeySet); got.code != http.StatusServiceUnavailable {
				t.Errorf("the pinned URL answers %d when there is %s, and a verifier is told to come back rather than handed a document no root key signed", got.code, tc.name)
			}
		})
	}
}

// answer is one served response, read whole.
type answer struct {
	code        int
	contentType string
	body        []byte
}

// get serves one GET from the mux and reads it back.
func get(t *testing.T, mux *http.ServeMux, path string) answer {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("reading the answer to GET %s: %v", path, err)
	}
	return answer{code: rec.Code, contentType: rec.Result().Header.Get("Content-Type"), body: body}
}
