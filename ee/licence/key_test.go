// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package licence

// The verifier's tests (licence-key-v2 plan K1.1) and its generator of the
// vector file the console's verifier is read against (K1.4).
//
// **Every key here is minted in the process that reads it.** A fixture file
// holding a private half would be key material in a repository whatever it is
// called (.claude/rules/ee.md), so newFixtureKey generates a keypair per call
// and the private half never leaves memory. What testdata/vectors_v1.json
// carries is a public half and signed key texts, which is what a verifier's
// fixture is allowed to carry and what the console's parity test reads.
//
// **The older format's fixture stays beside it.** `testdata/vectors.json` is
// the vectors of the `<key id>.<payload>.<signature>` key this package no
// longer verifies (K7.3): the console's reader of that format and the app's
// still test against it, so the file leaves with those two readers rather than
// with the Go verifier.
//
// The keys are shaped as they are so that the file is the *only* shared thing
// the two verifiers have: the claim sets are the ones a licence server would
// mint (a whole policy in the key, times as RFC 3339 stamps, `check_interval`
// as an ISO 8601 duration), the clock is one fixed instant the whole file is
// read at, and each vector sits on a boundary or a refusal the two verifiers
// have to agree on exactly. A vector set whose answers depended on the day the
// test ran would prove nothing on the day it was regenerated.
//
// The package's own rules are tested here too: no file of the package may reach
// the network or import outside the verifier's list, and the CLI's key file and
// the one environment variable it hands a key over in are read the way
// docs/licensing/licence-key.md says.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// envHelperVar marks the re-run of this test binary that
// TestTheEnvironmentKeyIsRemovedBeforeAnyChildStarts starts as its child. It is
// what TestMain reads to leave the environment alone in that child: the child's
// whole purpose is to report the environment its parent handed it.
const envHelperVar = "FUGARO_LICENCE_ENV_HELPER"

// TestMain keeps the tests off the developer's own machine: no handed-over key
// in the ambient environment decides a store's behaviour, and the two
// directories a store would look in are temporary ones. The child the
// environment test starts is the exception, above.
func TestMain(m *testing.M) {
	if os.Getenv(envHelperVar) == "" {
		os.Unsetenv(EnvironmentKey)
		for _, name := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "HOME"} {
			dir, err := os.MkdirTemp("", "fugaro-test-licence-")
			if err != nil {
				continue
			}
			os.Setenv(name, dir)
		}
	}
	os.Exit(m.Run())
}

// fixtureKey is a throwaway Ed25519 keypair, generated per call: the private
// half is what mints the keys and the vectors, and it is never written
// anywhere.
type fixtureKey struct {
	id   string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func newFixtureKey(t *testing.T, id string) fixtureKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating the fixture key %s: %v", id, err)
	}
	return fixtureKey{id: id, priv: priv, pub: pub}
}

// public is the half a keyring holds, as the vectors file spells it.
func (f fixtureKey) public() string {
	return base64.RawURLEncoding.EncodeToString(f.pub)
}

// ringOf is the keyring a verifier is asked with: the fixture's id and no
// other, which is what makes TestStagingKeyIDIsRefused a test of the keyring
// rather than of the staging key's own bytes.
func ringOf(f fixtureKey) Keyring {
	return Keyring{f.id: f.pub}
}

// The two key ids the vectors are signed under. Neither is a production id and
// neither ever becomes one: TestStagingKeyIDIsRefused holds both out of the
// compiled keyring, and the staging id is the one a sandbox installation is
// minted under (docs/licensing/licence-key.md, "Test-mode keys"), which every
// production build refuses for the id alone.
const (
	fixtureV1KeyID = "licence-test-fixture-v1"
	stagingV1KeyID = "licence-staging-sandbox-v1"
)

// firebaseProjectV1 is the workspace Firebase project id the tests bind a key
// to. It is a test project's id and not any real one: what the verifier
// compares is two strings, and the test supplies both.
const firebaseProjectV1 = "fugaro-test-project"

// upgradeMessage is the spec's refusal for a key of a version this build does
// not read, word for word (spec section 4). It is written out here rather than
// read from ErrNewerVersion, so that a change to the sentinel's text is a
// failing test rather than a test that agrees with whatever the text became.
const upgradeMessage = "This licence key requires a newer version of Fugaro. Upgrade Fugaro to use it."

// The canaries the ordering test plants in a payload whose signature does not
// verify. Each is a string no refusal may ever contain: a claim read out of an
// unverified payload is text an attacker chose, and a refusal is a sentence a
// user pastes into a ticket.
const (
	canaryNote         = "a-note-no-refusal-may-name"
	canaryWorkspace    = "a-workspace-no-refusal-may-name"
	canaryOrganisation = "an-organisation-no-refusal-may-name"
	canaryKeyID        = "a-key-id-no-refusal-may-name"
)

// v1Now is the clock every v1 test reads a key at, and the instant the keys are
// issued from: a fixed date rather than time.Now, because a key's state is the
// thing under test and a clock that moved would move it.
var v1Now = time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)

// atV1 is a day offset from v1Now, which is how the tests place a key's term and
// its boundaries.
func atV1(days int) time.Time { return v1Now.Add(time.Duration(days) * 24 * time.Hour) }

// v1Claims is the claim set the tests start from: a paid Team key, bound to the
// test workspace, checking daily.
func v1Claims() Claims {
	interval := "P1D"
	return Claims{
		V:                 versionV1,
		KID:               fixtureV1KeyID,
		KeyID:             "lk_test_0001",
		WorkspaceID:       firebaseProjectV1,
		Organisation:      "example.com",
		Tier:              TierTeam,
		Seats:             5,
		IssuedAt:          atV1(0),
		PaidThrough:       atV1(365),
		GraceDays:         30,
		RenewalMode:       RenewalAuto,
		RenewBeforeDays:   7,
		CheckInterval:     &interval,
		RenewalSecretHash: "a-hash-of-the-renewal-secret-and-not-the-secret",
		Warnings:          []WarningV1{{Roles: []string{"admin"}, From: "grace_start"}},
		Kind:              KindPaid,
		Note:              "a test key",
	}
}

// signClaims mints a v1 key text for a claim set: the payload is the claim
// set marshalled as it is, the signature is over exactly those bytes, and the
// id travels inside the payload rather than in front of it.
func (f fixtureKey) signClaims(t *testing.T, c Claims) string {
	t.Helper()
	payload, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshalling the claim set: %v", err)
	}
	return f.signRaw(t, payload)
}

// signRaw mints a v1 key text over bytes that are not a claim set at all,
// which is the only way to reach the refusals that sit behind a verified
// signature.
func (f fixtureKey) signRaw(t *testing.T, payload []byte) string {
	t.Helper()
	return encodeKey(payload, ed25519.Sign(f.priv, payload))
}

// encodeKey renders a v1 key text the way the format writes one: the prefix,
// the payload and the signature, both unpadded base64url.
func encodeKey(payload, signature []byte) string {
	return keyPrefixV1 +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}

// sameClaims compares two claim sets, holding the two stamps to the instant
// they name rather than to the wall-clock bookkeeping time.Time carries
// alongside it.
func sameClaims(a, b Claims) bool {
	if !a.IssuedAt.Equal(b.IssuedAt) || !a.PaidThrough.Equal(b.PaidThrough) {
		return false
	}
	a.IssuedAt, b.IssuedAt = time.Time{}, time.Time{}
	a.PaidThrough, b.PaidThrough = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

// refusedAs holds a refusal to its sentinel, the zero claim set and no state: a
// verifier that answered a claim set beside an error would be handing a caller
// something to read out of a key it refused.
func refusedAs(t *testing.T, got Claims, state State, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("the key answered %v (%s), want %v", err, state, want)
	}
	if !reflect.DeepEqual(got, Claims{}) || state != "" {
		t.Fatalf("a refused key answered the claim set %+v in state %s, want neither: nothing is read out of a key this verifier refuses", got, state)
	}
}

// TestVerifyFgkKey is the round trip: a whole v1 key verifies against a keyring
// that holds its id, answers its claims and the state the clock puts it in, and
// a text that is not the format is refused as malformed before any keyring is
// consulted.
func TestVerifyFgkKey(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)
	want := v1Claims()
	key := fixture.signClaims(t, want)

	if !strings.HasPrefix(key, keyPrefixV1) {
		t.Fatalf("a v1 key text is %q, want the %s prefix: it is what makes a key recognisable in a log and a scanner (spec section 3.1)", key, keyPrefixV1)
	}
	if parts := strings.Split(key, "."); len(parts) != 2 {
		t.Fatalf("a v1 key text is %d parts, want the payload and the signature", len(parts))
	}

	got, state, err := Verify([]byte(key), atV1(30), ringOf(fixture), firebaseProjectV1)
	if err != nil {
		t.Fatalf("a v1 key inside its term: %v", err)
	}
	if state != Active {
		t.Fatalf("a key 30 days into a 365-day term is %s, want %s", state, Active)
	}
	if !sameClaims(got, want) {
		t.Fatalf("the key answered %+v, want %+v", got, want)
	}

	// The text is read as it is written: the prefix leads, there are exactly two
	// parts, and both are base64url. Every one of these is refused before the
	// keyring is consulted.
	rest := strings.TrimPrefix(key, keyPrefixV1)
	parts := strings.Split(rest, ".")
	for _, tt := range []struct {
		name string
		key  string
	}{
		{"no prefix", rest},
		{"one part", keyPrefixV1 + parts[0]},
		{"three parts", key + ".AAAA"},
		{"an empty payload part", keyPrefixV1 + "." + parts[1]},
		{"an empty signature part", keyPrefixV1 + parts[0] + "."},
		{"a part outside the alphabet", keyPrefixV1 + "%%%.%%%"},
		{"a part that is not a whole number of bytes", keyPrefixV1 + "A." + parts[1]},
		// Go's base64 skips a carriage return or a newline wherever it meets
		// one, so both of these decode to the key above and verify unless the
		// parts are held to the alphabet: a text with a line folded into it
		// would be a second spelling of one key, and one key has one spelling.
		{"a payload part with a newline in it", keyPrefixV1 + parts[0][:4] + "\n" + parts[0][4:] + "." + parts[1]},
		{"a signature part with a carriage return in it", keyPrefixV1 + parts[0] + "." + parts[1][:4] + "\r\n" + parts[1][4:]},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, state, err := Verify([]byte(tt.key), atV1(30), ringOf(fixture), firebaseProjectV1)
			refusedAs(t, got, state, err, ErrMalformedV1)
		})
	}

	// The rule above is about the *inside* of a part, not the key's outer
	// whitespace: a key pasted with a newline after it is the key, and it
	// answers its claims.
	got, state, err = Verify([]byte(key+"\n"), atV1(30), ringOf(fixture), firebaseProjectV1)
	if err != nil {
		t.Fatalf("a key with a trailing newline is refused: %v -- a key arrives by paste, and the whitespace around the text is not part of it", err)
	}
	if state != Active || !sameClaims(got, want) {
		t.Fatalf("a key with a trailing newline answered the claim set %+v in state %s, want the key's own", got, state)
	}

	// A payload signed as it is spelled and then re-spelled is a different byte
	// string: the signature is over the raw bytes, so it fails rather than
	// verifying as the same claims.
	signed, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	respelled := bytes.Replace(signed, []byte(`":`), []byte(`": `), 1)
	got, state, err = Verify([]byte(encodeKey(respelled, ed25519.Sign(fixture.priv, signed))), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrSignature)

	// And an id no keyring holds is refused for the id alone.
	foreign := want
	foreign.KID = "licence-someone-elses-key-v1"
	got, state, err = Verify([]byte(fixture.signClaims(t, foreign)), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrUnknownKeyID)
}

// TestNoClaimIsReadBeforeTheSignatureVerifies is the rule every later task
// consumes: the signature is verified over the payload's raw bytes before any
// claim is read, and no claim of a payload whose signature did not verify
// reaches a refusal. The canaries below are planted in the claims and in the
// text itself; a refusal that named one would be a refusal echoing text an
// attacker chose.
func TestNoClaimIsReadBeforeTheSignatureVerifies(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)
	stranger := newFixtureKey(t, "licence-a-stranger-v1")

	// A claim set carrying canaries, signed by a key the ring does not hold
	// under the id the payload names. The signature fails, so the claims behind
	// it are never read.
	planted := v1Claims()
	planted.Note = canaryNote
	planted.WorkspaceID = canaryWorkspace
	planted.Organisation = canaryOrganisation
	planted.KeyID = canaryKeyID
	payload, err := json.Marshal(planted)
	if err != nil {
		t.Fatal(err)
	}
	got, state, err := Verify([]byte(encodeKey(payload, ed25519.Sign(stranger.priv, payload))), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrSignature)
	for _, canary := range []string{canaryNote, canaryWorkspace, canaryOrganisation, canaryKeyID} {
		if strings.Contains(err.Error(), canary) {
			t.Errorf("the signature refusal is %q, and it names %q: a claim of a payload whose signature did not verify is text an attacker chose", err, canary)
		}
	}

	// The same payload with a claim this build cannot read *and* a signature
	// that does not verify: the refusal is the signature's, and it is the case
	// that says the claim decode never ran, because a verifier that decoded
	// first would answer ErrClaims here.
	broken := append(bytes.TrimSuffix(payload, []byte("}")), []byte(`,"seats":"five"}`)...)
	got, state, err = Verify([]byte(encodeKey(broken, ed25519.Sign(stranger.priv, broken))), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrSignature)

	// An id the ring does not hold, refused for the id alone: the refusal names
	// no part of the payload, and the payload's id is one of the canaries.
	unknown := planted
	unknown.KID = canaryKeyID
	got, state, err = Verify([]byte(fixture.signClaims(t, unknown)), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrUnknownKeyID)
	if strings.Contains(err.Error(), canaryKeyID) {
		t.Errorf("the unknown-id refusal is %q, and it names %q: the id is a claim of an unverified payload", err, canaryKeyID)
	}

	// A payload that is not a claim set at all names no keyring entry, so there
	// is nothing to verify it with: it is refused as malformed, and the refusal
	// carries neither the bytes nor the error encoding/json would have written
	// about them.
	for _, tt := range []struct {
		name    string
		payload []byte
	}{
		{"not JSON at all", []byte("this is " + canaryNote)},
		{"a JSON array", []byte(`["` + canaryNote + `"]`)},
		{"JSON null", []byte("null")},
		{"an object with no id", []byte(`{"v":1,"note":"` + canaryNote + `"}`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, state, err := Verify([]byte(encodeKey(tt.payload, ed25519.Sign(fixture.priv, tt.payload))), atV1(30), ringOf(fixture), firebaseProjectV1)
			refusedAs(t, got, state, err, ErrMalformedV1)
			if strings.Contains(err.Error(), canaryNote) {
				t.Errorf("the refusal is %q, and it names %q: the text behind an unverified signature is text an attacker chose", err, canaryNote)
			}
		})
	}
}

// TestHigherPolicyVersionIsRefusedNotFree holds the spec's section 4 rule: a key
// of a version this build does not read is refused with the upgrade message and
// never falls back to Free. The refusal is the version's alone and is reached
// before the claim set is decoded: the payload below carries a claim this build
// cannot decode at all -- `seats` retyped to a string, which is a version 2 the
// spec's "a field never changes meaning, retyping bumps the version" allows --
// so a verifier that decoded the claim set first answers the claim refusal here
// instead of the upgrade message.
func TestHigherPolicyVersionIsRefusedNotFree(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)

	payload, err := json.Marshal(map[string]any{
		"v":              2,
		"kid":            fixtureV1KeyID,
		"key_id":         "lk_from_the_future",
		"workspace_id":   firebaseProjectV1,
		"organisation":   "example.com",
		"tier":           TierEnterprise,
		"seats":          "fifty",
		"issued_at":      atV1(0),
		"paid_through":   atV1(365),
		"grace_days":     30,
		"renewal_mode":   RenewalAuto,
		"check_interval": "P1D",
		"entitlements":   []string{"sso"},
		"kind":           KindPaid,
	})
	if err != nil {
		t.Fatal(err)
	}
	key := fixture.signRaw(t, payload)

	got, state, err := Verify([]byte(key), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrNewerVersion)
	if err.Error() != upgradeMessage {
		t.Fatalf("a version 2 key is refused with %q, want the message the spec pins word for word: %q", err, upgradeMessage)
	}
	if state != "" {
		t.Fatalf("a refused key answered the state %s: a key this build does not read is not a key it can place, and it never falls back to Free", state)
	}

	// A signed key with no version at all is not a v1 key either, and it is
	// refused as a claim set rather than read as one.
	noVersion, err := json.Marshal(map[string]any{
		"kid":          fixtureV1KeyID,
		"workspace_id": firebaseProjectV1,
		"tier":         TierTeam,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, state, err = Verify([]byte(fixture.signRaw(t, noVersion)), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrClaims)
}

// TestUnknownFieldsInASignedKeyAreIgnored holds D2146: a field this build does
// not know is ignored, so that a field added within version 1 has a safe
// default in a build that predates it. The exception is the signature's, which
// is why TestUnknownFieldsNeedAVerifiedSignature is the other half of this
// pair.
func TestUnknownFieldsInASignedKeyAreIgnored(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)
	want := v1Claims()

	signed, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	payload := append(bytes.TrimSuffix(signed, []byte("}")), []byte(`,"entitlements":["sso"],"covered_until":"2027-11-12T00:00:00Z"}`)...)

	got, state, err := Verify([]byte(fixture.signRaw(t, payload)), atV1(30), ringOf(fixture), firebaseProjectV1)
	if err != nil {
		t.Fatalf("a signed key carrying two fields this build does not know: %v", err)
	}
	if state != Active {
		t.Fatalf("the key is %s, want %s", state, Active)
	}
	if !sameClaims(got, want) {
		t.Fatalf("the key answered %+v, want the claim set it carries: %+v", got, want)
	}

	// A field this build *does* know, carrying the wrong type, is still an
	// error: the tolerance is for fields nobody here has heard of, not for a
	// claim set whose own shape is broken.
	broken := append(bytes.TrimSuffix(signed, []byte("}")), []byte(`,"seats":"five"}`)...)
	got, state, err = Verify([]byte(fixture.signRaw(t, broken)), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrClaims)

	// And bytes after the claim set are not a second claim set to ignore: the
	// decode runs to the end of the payload.
	trailing := append(append([]byte{}, signed...), []byte(`{}`)...)
	got, state, err = Verify([]byte(fixture.signRaw(t, trailing)), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrClaims)
}

// TestUnknownFieldsNeedAVerifiedSignature is the other half of D2146: the
// unknown-field tolerance belongs to a *signed* key and to nothing else. A text
// with no signature is never read at all, and a payload whose signature does
// not verify never reaches the decode the tolerance lives in.
func TestUnknownFieldsNeedAVerifiedSignature(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)
	stranger := newFixtureKey(t, "licence-a-stranger-v1")

	// A v1 text with no signature part: there is nothing to verify, so there is
	// no key here to read.
	payload, err := json.Marshal(v1Claims())
	if err != nil {
		t.Fatal(err)
	}
	got, state, err := Verify([]byte(keyPrefixV1+base64.RawURLEncoding.EncodeToString(payload)), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrMalformedV1)

	// A payload with an unknown field whose signature does not verify is
	// refused for the signature: the tolerance is applied after verification,
	// never before it.
	unknown := append(bytes.TrimSuffix(payload, []byte("}")), []byte(`,"entitlements":["sso"]}`)...)
	got, state, err = Verify([]byte(encodeKey(unknown, ed25519.Sign(stranger.priv, unknown))), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrSignature)
}

// TestStagingKeyIDIsRefused is the staging refusal: a production keyring holds
// production ids only, so a sandbox key is refused for its id alone (F17).
func TestStagingKeyIDIsRefused(t *testing.T) {
	staging := newFixtureKey(t, stagingV1KeyID)
	claims := v1Claims()
	claims.KID = stagingV1KeyID

	got, state, err := Verify([]byte(staging.signClaims(t, claims)), atV1(30), productionKeyring, firebaseProjectV1)
	refusedAs(t, got, state, err, ErrUnknownKeyID)

	for _, id := range []string{fixtureV1KeyID, stagingV1KeyID} {
		if _, held := productionKeyring[id]; held {
			t.Fatalf("the production keyring holds %s: a test or staging key must never license a build, and the keyring is what decides", id)
		}
	}
	file := loadVectors(t)
	for id := range file.Keyring {
		if _, held := productionKeyring[id]; held {
			t.Fatalf("testdata/vectors_v1.json is signed under %s, which the production keyring holds", id)
		}
	}
	if _, held := productionKeyring[file.Staging.ID]; held {
		t.Fatalf("the production keyring holds the vectors' staging id %s", file.Staging.ID)
	}
}

// TestKeyBeyondTheTierLifetimeIsRefused holds the tier's maximum lifetime,
// which the format keeps as `paid_through - issued_at` plus the thirty-day lead
// a renewal is minted inside (T26). The ceiling is read from both sides for
// every tier: at it the key is good, a second past it is refused.
func TestKeyBeyondTheTierLifetimeIsRefused(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)

	for tier, maximum := range tierMaxTerm {
		t.Run(tier, func(t *testing.T) {
			atCeiling := v1Claims()
			atCeiling.Tier = tier
			atCeiling.IssuedAt = v1Now
			atCeiling.PaidThrough = v1Now.Add(maximum)

			if _, state, err := Verify([]byte(fixture.signClaims(t, atCeiling)), atV1(30), ringOf(fixture), firebaseProjectV1); err != nil {
				t.Fatalf("a %s key at the longest term its tier allows: %v", tier, err)
			} else if state != Active {
				t.Fatalf("a %s key at the longest term its tier allows is %s, want %s", tier, state, Active)
			}

			past := atCeiling
			past.PaidThrough = v1Now.Add(maximum + time.Second)
			got, state, err := Verify([]byte(fixture.signClaims(t, past)), atV1(30), ringOf(fixture), firebaseProjectV1)
			refusedAs(t, got, state, err, ErrLifetime)
		})
	}

	// The gross case beside the boundary: a term far past the maximum, signed
	// properly, is refused for its length alone.
	over := v1Claims()
	over.IssuedAt = atV1(-500)
	over.PaidThrough = atV1(100)
	got, state, err := Verify([]byte(fixture.signClaims(t, over)), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrLifetime)
}

// TestGraceUsesTheKeysOwnDays holds the grace to the key's own `grace_days` and
// never to a number compiled into this build (D2133, D2138): two keys that
// expire at the same instant, carrying different graces, are in different
// states at the same clock reading.
func TestGraceUsesTheKeysOwnDays(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)
	paidThrough := atV1(0)

	for _, tt := range []struct {
		name  string
		grace int
		at    time.Time
		want  State
	}{
		{"the last second of the term", 30, paidThrough, Active},
		{"a second past the term, in grace", 30, paidThrough.Add(time.Second), Grace},
		{"the last second of a thirty-day grace", 30, paidThrough.Add(30 * 24 * time.Hour), Grace},
		{"a second past a thirty-day grace", 30, paidThrough.Add(30*24*time.Hour + time.Second), Expired},
		{"ten days into a forty-five day grace", 45, paidThrough.Add(10 * 24 * time.Hour), Grace},
		{"the same instant with no grace at all", 0, paidThrough.Add(10 * 24 * time.Hour), Expired},
		{"a second past a term with no grace", 0, paidThrough.Add(time.Second), Expired},
		{"a second past a term whose grace is negative", -1, paidThrough.Add(time.Second), Expired},
	} {
		t.Run(tt.name, func(t *testing.T) {
			claims := v1Claims()
			claims.IssuedAt = paidThrough.Add(-365 * 24 * time.Hour)
			claims.PaidThrough = paidThrough
			claims.GraceDays = tt.grace

			_, state, err := Verify([]byte(fixture.signClaims(t, claims)), tt.at, ringOf(fixture), firebaseProjectV1)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if state != tt.want {
				t.Fatalf("a key with %d days of grace read at %s is %s, want %s", tt.grace, tt.at, state, tt.want)
			}
		})
	}
}

// TestGraceDaysPastADurationIsTheLongestGrace pins graceDuration's clamp, which
// nothing else holds: a duration in nanoseconds runs out at about 106,751 days,
// and with the clamp deleted the multiply wraps, so a key whose grace_days is
// past what a duration holds answers a window far shorter than the one it
// states -- expired, for a grace a licence server meant to outlast the term.
func TestGraceDaysPastADurationIsTheLongestGrace(t *testing.T) {
	longest := graceDuration(MaxIntervalDays + 1)
	if longest < 36500*24*time.Hour {
		t.Fatalf("a grace past what a duration holds is %v, want the longest grace there is: the arithmetic wrapped instead of stopping at the bound", longest)
	}
	for _, days := range []int{MaxIntervalDays + 1, 1 << 40, (1 << 62) - 1} {
		if got := graceDuration(days); got != longest {
			t.Fatalf("graceDuration(%d) is %v, want %v: every grace past the bound is the same grace", days, got, longest)
		}
	}

	// End to end, because the state is what a caller reads: a key with a grace
	// longer than any duration is still in grace a century after its paid term.
	fixture := newFixtureKey(t, fixtureV1KeyID)
	claims := v1Claims()
	claims.PaidThrough = atV1(-36500)
	claims.IssuedAt = atV1(-36500 - 365)
	claims.GraceDays = 1 << 40
	_, state, err := Verify([]byte(fixture.signClaims(t, claims)), atV1(0), ringOf(fixture), firebaseProjectV1)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if state != Grace {
		t.Fatalf("a key with %d days of grace read a century past its paid term is %s, want %s: a grace past what a duration holds is the longest grace, not a shorter window", claims.GraceDays, state, Grace)
	}
}

// TestParseISODurationRefusesWhatItCannotHold pins the bound on the two forms
// F16 admits. A duration in nanoseconds runs out at about 106,751 days, so the
// parser stops at MaxIntervalDays and refuses a longer text rather than letting
// the multiply wrap it into a window far shorter than the key says; `P<n>W`
// multiplies by seven, so its bound is the same span counted in weeks.
//
// Nothing else holds either guard: with the bound deleted the whole package is
// green, and `P99999999999999999999D` answers a wrapped duration and no error.
func TestParseISODurationRefusesWhatItCannotHold(t *testing.T) {
	longest := time.Duration(MaxIntervalDays) * 24 * time.Hour
	for _, tt := range []struct {
		name string
		text string
		want time.Duration
		ok   bool
	}{
		{"the longest count of days there is", fmt.Sprintf("P%dD", MaxIntervalDays), longest, true},
		{"one day past it", fmt.Sprintf("P%dD", MaxIntervalDays+1), 0, false},
		{"the longest count of weeks there is", fmt.Sprintf("P%dW", MaxIntervalDays/7), longest, true},
		{"one week past it", fmt.Sprintf("P%dW", MaxIntervalDays/7+1), 0, false},
		{"a run of digits nobody meant", "P99999999999999999999D", 0, false},
		{"the same run in weeks", "P99999999999999999999W", 0, false},
		{"the largest number an int64 holds", "P9223372036854775807D", 0, false},
		{"no interval at all", "P0D", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseISODuration(tt.text)
			if !tt.ok {
				if !errors.Is(err, ErrDuration) {
					t.Fatalf("ParseISODuration(%q) is %v and %v, want the duration refusal: a span past what a duration holds must not be wrapped into a shorter one", tt.text, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseISODuration(%q): %v", tt.text, err)
			}
			if got != tt.want {
				t.Fatalf("ParseISODuration(%q) is %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// TestKeyWithoutWorkspaceIDIsRefused: the workspace is what binds a key to one
// installation, so a key carrying none is a claim set this verifier cannot
// place (D2134 as amended by TN2). The refusal names no value.
func TestKeyWithoutWorkspaceIDIsRefused(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)

	for _, tt := range []struct {
		name        string
		workspaceID string
		drop        bool
	}{
		{"an empty workspace id", "", false},
		{"no workspace id field at all", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			claims := v1Claims()
			claims.WorkspaceID = tt.workspaceID
			var key string
			if tt.drop {
				key = fixture.signRaw(t, dropFieldV1(t, claims, "workspace_id"))
			} else {
				key = fixture.signClaims(t, claims)
			}

			got, state, err := Verify([]byte(key), atV1(30), ringOf(fixture), firebaseProjectV1)
			refusedAs(t, got, state, err, ErrClaims)
		})
	}
}

// TestWorkspaceIDMustMatchTheFirebaseProject: the claim is compared with the id
// the caller read from the project's own config, and the two must be equal. An
// empty id from the caller never matches, so a caller that could not read one
// refuses every key rather than accepting any.
func TestWorkspaceIDMustMatchTheFirebaseProject(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)
	claims := v1Claims()
	claims.WorkspaceID = "a-workspace-firebase-project"
	key := fixture.signClaims(t, claims)

	if _, state, err := Verify([]byte(key), atV1(30), ringOf(fixture), "a-workspace-firebase-project"); err != nil {
		t.Fatalf("a key read in the workspace it is bound to: %v", err)
	} else if state != Active {
		t.Fatalf("the key is %s, want %s", state, Active)
	}

	for _, tt := range []struct {
		name string
		id   string
	}{
		{"another project's id", "another-firebase-project"},
		{"no id at all", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, state, err := Verify([]byte(key), atV1(30), ringOf(fixture), tt.id)
			refusedAs(t, got, state, err, ErrWorkspace)
			for _, value := range []string{claims.WorkspaceID, tt.id} {
				if value == "" {
					continue
				}
				if strings.Contains(err.Error(), value) {
					t.Errorf("the workspace refusal is %q, and it names %q: neither id is a secret, and neither belongs in a sentence a user pastes into a ticket", err, value)
				}
			}
		})
	}
}

// dropFieldV1 answers a claim set's payload with one field removed, which is the
// only way to build a key that does not carry the field at all rather than
// carrying it empty.
func dropFieldV1(t *testing.T, c Claims, field string) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	signed, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(signed, &object); err != nil {
		t.Fatal(err)
	}
	delete(object, field)
	trimmed, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return trimmed
}

// The v1 vector file: the claim sets, states and refusals the Go verifier and
// the console's v1 verifier (K1.4) are both read against.
const vectorsFile = "testdata/vectors_v1.json"

// vectorFile is that file's shape. It is JSON a TypeScript verifier reads
// without a Go type, so it carries strings, numbers and booleans only -- no Go
// spelling of a state or a refusal, just the names the two verifiers agree on.
type vectorFile struct {
	// Note is what the file is and what it is not: throwaway test keys, never a
	// production one.
	Note string `json:"note"`
	// Now is the clock every vector is read at, in Unix seconds.
	Now int64 `json:"now"`
	// FirebaseProjectID is the workspace id the verifier is asked with: the
	// whole file is one workspace, because the binding is what a v1 key is
	// checked against and a per-vector id would hide a verifier that ignored it.
	FirebaseProjectID string `json:"firebase_project_id"`
	// Keyring is the public half of each key a verifier is asked with, named by
	// key id.
	Keyring map[string]string `json:"keyring"`
	// Staging is the sandbox key, deliberately not in Keyring.
	Staging vectorKey `json:"staging"`
	// Vectors is one key and what a verifier must answer for it.
	Vectors []vectorCase `json:"vectors"`
}

type vectorKey struct {
	Note      string `json:"note"`
	ID        string `json:"id"`
	PublicKey string `json:"public_key"`
}

type vectorCase struct {
	Name string `json:"name"`
	Key  string `json:"key"`
	// State and Claims are what an accepted key answers; Refused is the part a
	// refused one fails on. Exactly one half is set.
	State   string  `json:"state,omitempty"`
	Claims  *Claims `json:"claims,omitempty"`
	Refused string  `json:"refused,omitempty"`
}

// vectorRefusals names each refusal as the file spells it. The console's
// verifier reads the same names out of the same file.
var vectorRefusals = map[string]error{
	"malformed":      ErrMalformedV1,
	"unknown-key-id": ErrUnknownKeyID,
	"signature":      ErrSignature,
	"claims":         ErrClaims,
	"lifetime":       ErrLifetime,
	"newer-version":  ErrNewerVersion,
	"workspace":      ErrWorkspace,
	"duration":       ErrDuration,
}

// boundaryVectors names the vectors that sit on a second a verifier can get
// wrong while every other vector still answers the same: both ends of the paid
// term, both ends of the grace, and the tier's longest term from both sides.
var boundaryVectors = []string{
	"a key at the second its paid term ends",
	"a key a second into its grace",
	"a key at the last second of its grace",
	"a key a second past its grace",
	"a key at the longest term its tier allows",
	"a term a second past the tier's maximum",
	"a check interval longer than a duration holds",
	"a grace longer than a duration holds",
}

// loadVectors reads the v1 file the two verifiers are read against.
func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	data, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatalf("%s: %v -- the vectors are the set the console's verifier shares with this one, so a verifier whose tests cannot read them proves nothing", vectorsFile, err)
	}
	var file vectorFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("%s is not the JSON this file's shape declares: %v", vectorsFile, err)
	}
	return file
}

// ringV1 is the keyring the v1 vectors are asked with: the file's own keys,
// decoded.
func (f vectorFile) ring(t *testing.T) Keyring {
	t.Helper()
	ring := make(Keyring, len(f.Keyring))
	for id, encoded := range f.Keyring {
		key, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("the keyring's %s is not base64url: %v", id, err)
		}
		if len(key) != ed25519.PublicKeySize {
			t.Fatalf("the keyring's %s is %d bytes, and an Ed25519 public key is %d", id, len(key), ed25519.PublicKeySize)
		}
		ring[id] = ed25519.PublicKey(key)
	}
	return ring
}

// vectorNamed answers the vector a test names, so a test about one vector
// fails by name rather than by index if the file is reordered.
func (f vectorFile) vectorNamed(t *testing.T, name string) vectorCase {
	t.Helper()
	for _, v := range f.Vectors {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("%s carries no vector named %q", vectorsFile, name)
	return vectorCase{}
}

// TestVectorsRoundTrip replays the whole file through this verifier: every
// vector's answer, every refusal, and the boundaries by name. It is what keeps
// the file the console reads honest about the verifier it claims to describe.
func TestVectorsRoundTrip(t *testing.T) {
	file := loadVectors(t)
	ring := file.ring(t)
	now := time.Unix(file.Now, 0)

	if len(file.Keyring) != 1 {
		t.Fatalf("the vectors' keyring holds %d keys, want exactly the fixture's one", len(file.Keyring))
	}
	if len(file.Vectors) < 15 {
		t.Fatalf("the vectors file carries %d vectors, and it was written to carry both halves", len(file.Vectors))
	}

	seen := map[string]bool{}
	for _, v := range file.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			claims, state, err := Verify([]byte(v.Key), now, ring, file.FirebaseProjectID)
			if v.Refused != "" {
				want, ok := vectorRefusals[v.Refused]
				if !ok {
					t.Fatalf("the vector names the refusal %q, which is not one of the eight the v1 verifiers share", v.Refused)
				}
				if !errors.Is(err, want) {
					t.Fatalf("the vector is read as %v (%s), want %v", err, state, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("%v", err)
			}
			if State(v.State) != state {
				t.Fatalf("the vector is %s, want %s", state, v.State)
			}
			if v.Claims == nil {
				t.Fatalf("an accepted vector carries no claim set, so nothing holds the payload to the terms it states")
			}
			if !sameClaims(claims, *v.Claims) {
				t.Fatalf("the key's claims are %+v, want %+v", claims, *v.Claims)
			}
		})
		seen[v.State+v.Refused] = true
	}

	// Each refusal and each state is in the file: a vector set that lost its
	// negative half would still round-trip green.
	for name := range vectorRefusals {
		if !seen[name] {
			t.Errorf("no vector in the file is refused as %s", name)
		}
	}
	for _, want := range []string{string(Active), string(Grace), string(Expired)} {
		if !seen[want] {
			t.Errorf("no vector in the file is %s", want)
		}
	}
	for _, name := range boundaryVectors {
		file.vectorNamed(t, name)
	}
}

// TestGenerateVectors rewrites testdata/vectors_v1.json. It is skipped unless
// the environment asks for it, so a plain `go test` never rewrites a file under
// a reviewer's feet: the signatures are freshly minted on every run and a
// rewritten file is a diff nobody asked for. Run it when the claim set, a state
// or a refusal changes:
//
//	FUGARO_LICENCE_WRITE_VECTORS_V1=1 go test -tags ee ./ee/licence/ -run TestGenerateVectors -count=1
func TestGenerateVectors(t *testing.T) {
	if os.Getenv("FUGARO_LICENCE_WRITE_VECTORS_V1") == "" {
		t.Skip("set FUGARO_LICENCE_WRITE_VECTORS_V1=1 to rewrite testdata/vectors_v1.json")
	}
	data, err := json.MarshalIndent(generateVectors(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vectorsFile, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", vectorsFile)
}

// generateVectors mints the v1 file's vectors. Every signature is made here,
// in this process, with a keypair thrown away when the test ends: the file
// holds the public half and the signed key texts and nothing a verifier could
// mint with (.claude/rules/ee.md).
func generateVectors(t *testing.T) vectorFile {
	t.Helper()
	fixture := newFixtureKey(t, fixtureV1KeyID)
	staging := newFixtureKey(t, stagingV1KeyID)

	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	days := func(n int) time.Time { return now.Add(time.Duration(n) * 24 * time.Hour) }

	// base is the claim set every vector starts from: a paid Team key bound to
	// the file's workspace, whose term and grace each vector then moves.
	base := func() Claims {
		interval := "P1D"
		return Claims{
			V:                 versionV1,
			KID:               fixture.id,
			KeyID:             "lk_vectors_0001",
			WorkspaceID:       "fugaro-test-project",
			Organisation:      "example.com",
			Tier:              TierTeam,
			Seats:             5,
			IssuedAt:          days(-30),
			PaidThrough:       days(335),
			GraceDays:         30,
			RenewalMode:       RenewalAuto,
			RenewBeforeDays:   7,
			CheckInterval:     &interval,
			RenewalSecretHash: "a-hash-of-the-renewal-secret-and-not-the-secret",
			Warnings:          []WarningV1{{Roles: []string{"admin"}, From: "grace_start"}},
			Kind:              KindPaid,
			Note:              "a test key",
		}
	}

	file := vectorFile{
		Note:              "The claim sets, states and refusals the Go v1 verifier and the console's are both read against (licence-key-v2 plan K1.1, K1.4; spec sections 3, 4 and 6). Every key here is minted by a throwaway test keypair whose private half was generated in the process that wrote this file and never written down; no build trusts it, and ee/licence/key.go's production keyring holds no id at all until S2.9 publishes the owner's production key. `now` is the clock every vector is read at, `firebase_project_id` is the workspace the verifier is asked with, `state` and `claims` are what an accepted key answers, and `refused` is the part a refused one fails on, named as the two verifiers name it.",
		Now:               now.Unix(),
		FirebaseProjectID: "fugaro-test-project",
		Keyring:           map[string]string{fixture.id: fixture.public()},
		Staging: vectorKey{
			Note:      "The sandbox key: minted under the staging id for tests and for a sandbox installation, and in no production keyring, so a production verifier refuses it for the id alone.",
			ID:        staging.id,
			PublicKey: staging.public(),
		},
	}

	accept := func(name string, key string, state State, claims Claims) {
		file.Vectors = append(file.Vectors, vectorCase{Name: name, Key: key, State: string(state), Claims: &claims})
	}
	refuse := func(name, key, refusal string) {
		file.Vectors = append(file.Vectors, vectorCase{Name: name, Key: key, Refused: refusal})
	}
	sign := func(c Claims) string { return fixture.signClaims(t, c) }

	// The paid term and the two ends of the grace. The clock is one instant for
	// the whole file, so each boundary is a key whose paid_through sits at that
	// distance from it.
	active := base()
	accept("a team key inside its paid term", sign(active), Active, active)

	atTermEnd := base()
	atTermEnd.IssuedAt = days(-365)
	atTermEnd.PaidThrough = days(0)
	accept("a key at the second its paid term ends", sign(atTermEnd), Active, atTermEnd)

	intoGrace := base()
	intoGrace.IssuedAt = days(-366)
	intoGrace.PaidThrough = days(0).Add(-time.Second)
	accept("a key a second into its grace", sign(intoGrace), Grace, intoGrace)

	lastGraceSecond := base()
	lastGraceSecond.IssuedAt = days(-395)
	lastGraceSecond.PaidThrough = days(-30)
	accept("a key at the last second of its grace", sign(lastGraceSecond), Grace, lastGraceSecond)

	pastGrace := base()
	pastGrace.IssuedAt = days(-396)
	pastGrace.PaidThrough = days(-30).Add(-time.Second)
	accept("a key a second past its grace", sign(pastGrace), Expired, pastGrace)

	longPast := base()
	longPast.IssuedAt = days(-455)
	longPast.PaidThrough = days(-90)
	accept("a key long past its grace", sign(longPast), Expired, longPast)

	// The tier's longest term from both sides, taken from the verifier's own
	// table so a key minted at the ceiling stays inside it.
	maximum := tierMaxTerm[TierTeam]
	atCeiling := base()
	atCeiling.IssuedAt = days(-30)
	atCeiling.PaidThrough = days(-30).Add(maximum)
	accept("a key at the longest term its tier allows", sign(atCeiling), Active, atCeiling)

	pastCeiling := base()
	pastCeiling.IssuedAt = days(-30)
	pastCeiling.PaidThrough = days(-30).Add(maximum + time.Second)
	refuse("a term a second past the tier's maximum", sign(pastCeiling), "lifetime")

	// A field added within version 1 is ignored by a build that predates it, so
	// this vector is accepted with the claims it knows (D2146).
	withUnknown := base()
	signed, err := json.Marshal(withUnknown)
	if err != nil {
		t.Fatal(err)
	}
	extended := append(bytes.TrimSuffix(signed, []byte("}")), []byte(`,"entitlements":["sso"]}`)...)
	accept("a signed key carrying a field this build does not know", fixture.signRaw(t, extended), Active, withUnknown)

	// The refusals. Each one moves exactly one thing away from a good key.
	badInterval := base()
	hourly := "PT1H"
	badInterval.CheckInterval = &hourly
	refuse("a check interval this verifier does not read", sign(badInterval), "duration")

	// The two bounds on a span. An interval past what a duration holds is not an
	// interval this verifier reads, and a grace past it is the longest grace
	// there is rather than the shorter window the arithmetic would wrap into.
	absurdInterval := base()
	absurd := "P99999999999999999999D"
	absurdInterval.CheckInterval = &absurd
	refuse("a check interval longer than a duration holds", sign(absurdInterval), "duration")

	longGrace := base()
	longGrace.IssuedAt = days(-400)
	longGrace.PaidThrough = days(-30)
	longGrace.GraceDays = 1 << 40
	accept("a grace longer than a duration holds", sign(longGrace), Grace, longGrace)

	noWorkspace := base()
	noWorkspace.WorkspaceID = ""
	refuse("a key with no workspace id", sign(noWorkspace), "claims")

	otherWorkspace := base()
	otherWorkspace.WorkspaceID = "another-firebase-project"
	refuse("a key bound to another workspace", sign(otherWorkspace), "workspace")

	unknownTier := base()
	unknownTier.Tier = "unlimited"
	refuse("a tier this verifier does not know", sign(unknownTier), "claims")

	// The version 2 payload carries a claim this build cannot decode at all --
	// `seats` retyped to a string, which is what a version bump allows -- so
	// this is the vector that says the version is read before the claim set is:
	// a verifier that decoded first would answer `claims` here.
	newer := base()
	newer.V = 2
	newerSpelled, err := json.Marshal(newer)
	if err != nil {
		t.Fatal(err)
	}
	newerPayload := append(bytes.TrimSuffix(newerSpelled, []byte("}")), []byte(`,"seats":"fifty"}`)...)
	refuse("a key of a version this build does not read", fixture.signRaw(t, newerPayload), "newer-version")

	foreign := base()
	foreign.KID = "licence-someone-elses-key-v1"
	refuse("a key id this build does not hold", sign(foreign), "unknown-key-id")

	stagingClaims := base()
	stagingClaims.KID = staging.id
	refuse("a staging key", staging.signClaims(t, stagingClaims), "unknown-key-id")

	// A payload signed as it is spelled and then re-spelled: the signature is
	// over the raw bytes, so one key has one spelling.
	spelled, err := json.Marshal(base())
	if err != nil {
		t.Fatal(err)
	}
	respelled := bytes.Replace(spelled, []byte(`":`), []byte(`": `), 1)
	refuse("a payload signed as it is spelled, re-spelled", encodeKey(respelled, ed25519.Sign(fixture.priv, spelled)), "signature")

	// A payload changed after it was signed, in a way this build cannot read:
	// the answer is the signature's, and this is the vector that says the claim
	// decode waits for it. A verifier that read the claims first would answer
	// the claim refusal here.
	broken := append(bytes.TrimSuffix(spelled, []byte("}")), []byte(`,"seats":"five"}`)...)
	refuse("a claim set this build cannot read behind a signature that does not verify", encodeKey(broken, ed25519.Sign(fixture.priv, spelled)), "signature")

	// A payload that is not a claim set, and the three texts that are not the
	// format: the shape and the payload are refused before any keyring is
	// consulted, which is why a key with no id in it is a malformed text rather
	// than an unknown one.
	refuse("a payload that is not a claim set", fixture.signRaw(t, []byte("this is not a claim set")), "malformed")
	refuse("a key text that is not a v1 key", "a-key-text-with-no-prefix.QUJD.REVG", "malformed")
	good := sign(base())
	parts := strings.Split(strings.TrimPrefix(good, keyPrefixV1), ".")
	refuse("a key text with one part", keyPrefixV1+parts[0], "malformed")
	refuse("a part that is not base64url", keyPrefixV1+"%%%.%%%", "malformed")

	return file
}

// openTestStore answers the CLI's store over a config directory of the test's
// own: the environment is read through the caller-supplied getenv, which is
// what keeps this off the developer's own machine.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(func(name string) string {
		if name == "XDG_CONFIG_HOME" {
			return dir
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if dir := filepath.Dir(store.path); dir == "" || !strings.HasSuffix(filepath.ToSlash(dir), "/fugaro") {
		t.Fatalf("the store's key file is %s, want it under the CLI's own config directory", store.path)
	}
	return store
}

// writeKeyFile writes the store's key file at a mode the test chooses: the mode
// is half of what the store reads.
func writeKeyFile(t *testing.T, store *Store, mode fs.FileMode, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(store.path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile keeps an existing file's mode, so a second call at a different
	// mode has to set it.
	if err := os.Chmod(store.path, mode); err != nil {
		t.Fatal(err)
	}
}

// TestTheKeyFileIsReadAtMostTwoLines holds the CLI's own storage to the design:
// one key per line, at most two, because two is what the renewal's overlap
// installs (T26, docs/licensing/licence-key.md, "Storage and hand-off").
func TestTheKeyFileIsReadAtMostTwoLines(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)
	later := v1Claims()
	later.PaidThrough = atV1(400)
	first := fixture.signClaims(t, v1Claims())
	second := fixture.signClaims(t, later)

	store := openTestStore(t)
	writeKeyFile(t, store, 0o600, first+"\n\n"+second+"\n")
	keys, err := store.Keys()
	if err != nil {
		t.Fatalf("a file with two keys and a blank line: %v", err)
	}
	if len(keys) != 2 || string(keys[0]) != first || string(keys[1]) != second {
		t.Fatalf("the store read %q, want the file's two keys in order", keys)
	}

	// A key file another local user can read is the file F41 is about, and the
	// design keeps it at 0600. The refusal is the store's because nothing else
	// writes the file: the operator does, or the console hands a key over in the
	// environment instead.
	writeKeyFile(t, store, 0o644, first+"\n")
	if _, err := store.Keys(); !errors.Is(err, ErrKeyFile) {
		t.Fatalf("a key file of mode 0644 answered %v, want ErrKeyFile", err)
	}
	writeKeyFile(t, store, 0o600, first+"\n"+second+"\n"+first+"\n")
	if _, err := store.Keys(); !errors.Is(err, ErrKeyFile) {
		t.Fatalf("a key file with three keys answered %v, want ErrKeyFile", err)
	}
}

// TestNoKeyIsTheStoreBeingEmpty holds F43: a fresh installation and an upgraded
// console 0.112.0 have no key, and that is a state rather than a failure.
func TestNoKeyIsTheStoreBeingEmpty(t *testing.T) {
	store := openTestStore(t)
	keys, err := store.Keys()
	if err != nil {
		t.Fatalf("a store with no key file: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("a store with no key file read %q", keys)
	}
}

func TestTheEnvironmentKeyIsRemovedBeforeAnyChildStarts(t *testing.T) {
	// This test re-runs its own binary to be a child process: what a child of
	// the CLI sees is the environment as it stands when the child is started,
	// and printing it from another process is the only reading of that.
	if os.Getenv(envHelperVar) == "1" {
		fmt.Print(os.Getenv(EnvironmentKey))
		os.Exit(0)
	}

	fixture := newFixtureKey(t, fixtureV1KeyID)
	key := fixture.signClaims(t, v1Claims())
	t.Setenv(EnvironmentKey, key)

	// The control: before the registration point a child of this process does
	// inherit the variable, so the reading below is not an empty child that
	// would have printed nothing whatever the CLI did.
	if got := childEnvironmentKey(t); got != key {
		t.Fatalf("the helper process read %q before the registration point, want the handed-over key: the reading itself is what this test measures", got)
	}

	// The registration point's own call, with no config directory in reach at
	// all: a handed-over key is the whole installation, so the store must be
	// usable without one.
	store, err := Open(func(string) string { return "" })
	if err != nil {
		t.Fatalf("Open with a key handed over in the environment: %v", err)
	}
	if got := childEnvironmentKey(t); got != "" {
		t.Fatalf("a child started after the registration point read %q from %s: the CLI starts git, the plugin install, terraform and the runner, and one variable name left in the environment reaches all four", got, EnvironmentKey)
	}
	if _, still := os.LookupEnv(EnvironmentKey); still {
		t.Fatalf("%s is still in this process's environment after the registration point", EnvironmentKey)
	}
	keys, err := store.Keys()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(keys) != 1 || string(keys[0]) != key {
		t.Fatalf("the store read %q, want the key the environment handed over", keys)
	}
}

// childEnvironmentKey answers what a child of this process reads from
// FUGARO_LICENCE_KEY, by starting this test binary and having it print.
func childEnvironmentKey(t *testing.T) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestTheEnvironmentKeyIsRemovedBeforeAnyChildStarts")
	cmd.Env = append(os.Environ(), envHelperVar+"=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the helper process: %v", err)
	}
	return string(out)
}

// TestLicencePackageImportsNoNetwork holds the rule the verifier is built
// under: a key check makes no network call, so it cannot phone home, look a key
// up or check a revocation -- there is no server to ask (D308).
//
// The rule is two readings of the same thing. No file here, test or not, may
// import a net package. And the package's own files may import nothing outside
// the exact list below, so a call that reached a client of this project's own
// (an internal http package) is a deliberate change to the list rather than an
// import nobody noticed -- and so crypto/rand, which a verifier has no business
// with because it never mints, is refused too.
func TestLicencePackageImportsNoNetwork(t *testing.T) {
	allowed := []string{
		"bytes",           // the claim set is decoded from the payload's own bytes
		"crypto/ed25519",  // the one algorithm a key is signed with (D308)
		"encoding/base64", // the key text's two parts
		"encoding/json",   // the claim set
		"errors",          // the refusals
		"fmt",             // a refusal names the part that failed
		"io",              // the strict decode reads to the end of the payload
		"io/fs",           // the key file's absence is a state and not an error (F43)
		"os",              // the key file, and the one variable taken out of the environment
		"path/filepath",   // the key file's place under the config directory
		"runtime",         // the platform the config directory is the platform's own
		"strings",         // the key text's two parts, and the file's lines
		"time",            // the clock the caller passes and the grace against it
		"github.com/dimipaun/fugaro/internal/localcfg", // the CLI's own config directory
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 3 {
		t.Fatalf("the scan read %d Go files, want the package's own three: a scan of nothing proves nothing", len(files))
	}
	for _, path := range files {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, imp := range f.Imports {
			imported, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s imports %s, which is not a readable string", path, imp.Path.Value)
			}
			if imported == "net" || strings.HasPrefix(imported, "net/") {
				t.Errorf("%s imports %s: the key check answers offline and asks no server anything (D308)", path, imported)
				continue
			}
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			if !slices.Contains(allowed, imported) {
				t.Errorf("%s imports %s, which is not on the verifier's import list; a new import here is a decision a reviewer reads, and the list is what keeps a network client out", path, imported)
			}
		}
	}
}
