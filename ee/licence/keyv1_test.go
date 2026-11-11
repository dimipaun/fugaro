// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package licence

// The v1 verifier's tests (licence-key-v2 plan K1.1), and the generator of the
// vector file the console's v1 verifier is read against (K1.4).
//
// **Every key here is minted in the process that reads it.** A fixture file
// holding a private half would be key material in a repository whatever it is
// called (.claude/rules/ee.md), so newFixtureKey -- key_test.go's, reused here
// rather than written again -- generates a keypair per call and the private
// half never leaves memory. What testdata/vectors_v1.json carries is a public
// half and signed key texts, which is what a verifier's fixture is allowed to
// carry and what the console's parity test reads.
//
// The keys are shaped as they are so that the file is the *only* shared thing
// the two verifiers have: the claim sets are the ones a licence server would
// mint (a whole policy in the key, times as RFC 3339 stamps, `check_interval`
// as an ISO 8601 duration), the clock is one fixed instant the whole file is
// read at, and each vector sits on a boundary or a refusal the two verifiers
// have to agree on exactly. A vector set whose answers depended on the day the
// test ran would prove nothing on the day it was regenerated.

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The two v1 key ids the vectors are signed under. They are separate from the
// older format's ids on purpose: a key id is a name a keyring holds, and two
// files that spelled the same id with two different public halves would be one
// name meaning two things. Neither ever becomes a production id -- the
// production keyring is empty, and TestStagingKeyIDIsRefusedV1 holds both out
// of it.
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
func v1Claims() ClaimsV1 {
	interval := "P1D"
	return ClaimsV1{
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

// signClaimsV1 mints a v1 key text for a claim set: the payload is the claim
// set marshalled as it is, the signature is over exactly those bytes, and the
// id travels inside the payload rather than in front of it.
func (f fixtureKey) signClaimsV1(t *testing.T, c ClaimsV1) string {
	t.Helper()
	payload, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshalling the claim set: %v", err)
	}
	return f.signRawV1(t, payload)
}

// signRawV1 mints a v1 key text over bytes that are not a claim set at all,
// which is the only way to reach the refusals that sit behind a verified
// signature.
func (f fixtureKey) signRawV1(t *testing.T, payload []byte) string {
	t.Helper()
	return encodeKeyV1(payload, ed25519.Sign(f.priv, payload))
}

// encodeKeyV1 renders a v1 key text the way the format writes one: the prefix,
// the payload and the signature, both unpadded base64url.
func encodeKeyV1(payload, signature []byte) string {
	return keyPrefixV1 +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}

// sameClaimsV1 compares two claim sets, holding the two stamps to the instant
// they name rather than to the wall-clock bookkeeping time.Time carries
// alongside it.
func sameClaimsV1(a, b ClaimsV1) bool {
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
func refusedAs(t *testing.T, got ClaimsV1, state State, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("the key answered %v (%s), want %v", err, state, want)
	}
	if !reflect.DeepEqual(got, ClaimsV1{}) || state != "" {
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
	key := fixture.signClaimsV1(t, want)

	if !strings.HasPrefix(key, keyPrefixV1) {
		t.Fatalf("a v1 key text is %q, want the %s prefix: it is what makes a key recognisable in a log and a scanner (spec section 3.1)", key, keyPrefixV1)
	}
	if parts := strings.Split(key, "."); len(parts) != 2 {
		t.Fatalf("a v1 key text is %d parts, want the payload and the signature", len(parts))
	}

	got, state, err := VerifyV1([]byte(key), atV1(30), ringOf(fixture), firebaseProjectV1)
	if err != nil {
		t.Fatalf("a v1 key inside its term: %v", err)
	}
	if state != Active {
		t.Fatalf("a key 30 days into a 365-day term is %s, want %s", state, Active)
	}
	if !sameClaimsV1(got, want) {
		t.Fatalf("the key answered %+v, want %+v", got, want)
	}

	// The text is read as it is written: the prefix leads, there are exactly two
	// parts, and both are base64url. Every one of these is refused before the
	// keyring is consulted, and the older format -- which is what a text without
	// the prefix is -- is one of them.
	rest := strings.TrimPrefix(key, keyPrefixV1)
	parts := strings.Split(rest, ".")
	older := fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 1, Organisation: "example.com", Issued: 1, Expires: 4000000000})
	for _, tt := range []struct {
		name string
		key  string
	}{
		{"the older format", older},
		{"no prefix", rest},
		{"one part", keyPrefixV1 + parts[0]},
		{"three parts", key + ".AAAA"},
		{"an empty payload part", keyPrefixV1 + "." + parts[1]},
		{"an empty signature part", keyPrefixV1 + parts[0] + "."},
		{"a part outside the alphabet", keyPrefixV1 + "%%%.%%%"},
		{"a part that is not a whole number of bytes", keyPrefixV1 + "A." + parts[1]},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, state, err := VerifyV1([]byte(tt.key), atV1(30), ringOf(fixture), firebaseProjectV1)
			refusedAs(t, got, state, err, ErrMalformedV1)
		})
	}

	// A payload signed as it is spelled and then re-spelled is a different byte
	// string: the signature is over the raw bytes, so it fails rather than
	// verifying as the same claims.
	signed, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	respelled := bytes.Replace(signed, []byte(`":`), []byte(`": `), 1)
	got, state, err = VerifyV1([]byte(encodeKeyV1(respelled, ed25519.Sign(fixture.priv, signed))), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrSignature)

	// And an id no keyring holds is refused for the id alone.
	foreign := want
	foreign.KID = "licence-someone-elses-key-v1"
	got, state, err = VerifyV1([]byte(fixture.signClaimsV1(t, foreign)), atV1(30), ringOf(fixture), firebaseProjectV1)
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
	got, state, err := VerifyV1([]byte(encodeKeyV1(payload, ed25519.Sign(stranger.priv, payload))), atV1(30), ringOf(fixture), firebaseProjectV1)
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
	got, state, err = VerifyV1([]byte(encodeKeyV1(broken, ed25519.Sign(stranger.priv, broken))), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrSignature)

	// An id the ring does not hold, refused for the id alone: the refusal names
	// no part of the payload, and the payload's id is one of the canaries.
	unknown := planted
	unknown.KID = canaryKeyID
	got, state, err = VerifyV1([]byte(fixture.signClaimsV1(t, unknown)), atV1(30), ringOf(fixture), firebaseProjectV1)
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
			got, state, err := VerifyV1([]byte(encodeKeyV1(tt.payload, ed25519.Sign(fixture.priv, tt.payload))), atV1(30), ringOf(fixture), firebaseProjectV1)
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
	key := fixture.signRawV1(t, payload)

	got, state, err := VerifyV1([]byte(key), atV1(30), ringOf(fixture), firebaseProjectV1)
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
	got, state, err = VerifyV1([]byte(fixture.signRawV1(t, noVersion)), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrClaims)
}

// TestUnknownFieldsInASignedKeyAreIgnored holds D2146: a field this build does
// not know is ignored, so that a field added within version 1 has a safe
// default in a build that predates it. The exception is the signature's, which
// is why TestUnsignedFormatsStayStrict is the other half of this pair.
func TestUnknownFieldsInASignedKeyAreIgnored(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)
	want := v1Claims()

	signed, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	payload := append(bytes.TrimSuffix(signed, []byte("}")), []byte(`,"entitlements":["sso"],"covered_until":"2027-11-12T00:00:00Z"}`)...)

	got, state, err := VerifyV1([]byte(fixture.signRawV1(t, payload)), atV1(30), ringOf(fixture), firebaseProjectV1)
	if err != nil {
		t.Fatalf("a signed key carrying two fields this build does not know: %v", err)
	}
	if state != Active {
		t.Fatalf("the key is %s, want %s", state, Active)
	}
	if !sameClaimsV1(got, want) {
		t.Fatalf("the key answered %+v, want the claim set it carries: %+v", got, want)
	}

	// A field this build *does* know, carrying the wrong type, is still an
	// error: the tolerance is for fields nobody here has heard of, not for a
	// claim set whose own shape is broken.
	broken := append(bytes.TrimSuffix(signed, []byte("}")), []byte(`,"seats":"five"}`)...)
	got, state, err = VerifyV1([]byte(fixture.signRawV1(t, broken)), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrClaims)

	// And bytes after the claim set are not a second claim set to ignore: the
	// decode runs to the end of the payload.
	trailing := append(append([]byte{}, signed...), []byte(`{}`)...)
	got, state, err = VerifyV1([]byte(fixture.signRawV1(t, trailing)), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrClaims)
}

// TestUnsignedFormatsStayStrict is the other half of D2146: the unknown-field
// tolerance belongs to a *signed* v1 key and to nothing else. A text with no
// signature is never read at all, the older format stays strict, and a payload
// whose signature does not verify never reaches the decode the tolerance lives
// in.
func TestUnsignedFormatsStayStrict(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)
	stranger := newFixtureKey(t, "licence-a-stranger-v1")

	// A v1 text with no signature part: there is nothing to verify, so there is
	// no key here to read.
	payload, err := json.Marshal(v1Claims())
	if err != nil {
		t.Fatal(err)
	}
	got, state, err := VerifyV1([]byte(keyPrefixV1+base64.RawURLEncoding.EncodeToString(payload)), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrMalformedV1)

	// The older format still decodes strictly: an unknown field is an error
	// there, and this verifier's tolerance does not reach it.
	old := fixture.signRaw(t, []byte(fmt.Sprintf(`{"tier":"team","features":[],"seats":1,"organisation":"example.com","issued":%d,"expires":%d,"entitlements":["sso"]}`, atV1(-10).Unix(), atV1(355).Unix())))
	if _, _, err := verify([]byte(old), atV1(30), ringOf(fixture)); !errors.Is(err, ErrClaims) {
		t.Fatalf("the older format answered %v for an unknown field, want ErrClaims: the unknown-field exception is a signed v1 key's alone", err)
	}

	// A v1 payload with an unknown field whose signature does not verify is
	// refused for the signature: the tolerance is applied after verification,
	// never before it.
	unknown := append(bytes.TrimSuffix(payload, []byte("}")), []byte(`,"entitlements":["sso"]}`)...)
	got, state, err = VerifyV1([]byte(encodeKeyV1(unknown, ed25519.Sign(stranger.priv, unknown))), atV1(30), ringOf(fixture), firebaseProjectV1)
	refusedAs(t, got, state, err, ErrSignature)
}

// TestStagingKeyIDIsRefusedV1 is the staging refusal for the v1 format: a
// production keyring holds production ids only, so a sandbox key is refused for
// its id alone (F17). The suffix is V1 because the older format's test of the
// same name is still in the package until K7.3 removes it.
func TestStagingKeyIDIsRefusedV1(t *testing.T) {
	staging := newFixtureKey(t, stagingV1KeyID)
	claims := v1Claims()
	claims.KID = stagingV1KeyID

	got, state, err := VerifyV1([]byte(staging.signClaimsV1(t, claims)), atV1(30), productionKeyring, firebaseProjectV1)
	refusedAs(t, got, state, err, ErrUnknownKeyID)

	for _, id := range []string{fixtureV1KeyID, stagingV1KeyID} {
		if _, held := productionKeyring[id]; held {
			t.Fatalf("the production keyring holds %s: a test or staging key must never license a build, and the keyring is what decides", id)
		}
	}
	file := loadVectorsV1(t)
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
func TestKeyBeyondTheTierLifetimeIsRefusedV1(t *testing.T) {
	fixture := newFixtureKey(t, fixtureV1KeyID)

	for tier, maximum := range tierMaxTerm {
		t.Run(tier, func(t *testing.T) {
			atCeiling := v1Claims()
			atCeiling.Tier = tier
			atCeiling.IssuedAt = v1Now
			atCeiling.PaidThrough = v1Now.Add(maximum)

			if _, state, err := VerifyV1([]byte(fixture.signClaimsV1(t, atCeiling)), atV1(30), ringOf(fixture), firebaseProjectV1); err != nil {
				t.Fatalf("a %s key at the longest term its tier allows: %v", tier, err)
			} else if state != Active {
				t.Fatalf("a %s key at the longest term its tier allows is %s, want %s", tier, state, Active)
			}

			past := atCeiling
			past.PaidThrough = v1Now.Add(maximum + time.Second)
			got, state, err := VerifyV1([]byte(fixture.signClaimsV1(t, past)), atV1(30), ringOf(fixture), firebaseProjectV1)
			refusedAs(t, got, state, err, ErrLifetime)
		})
	}

	// The gross case beside the boundary: a term far past the maximum, signed
	// properly, is refused for its length alone.
	over := v1Claims()
	over.IssuedAt = atV1(-500)
	over.PaidThrough = atV1(100)
	got, state, err := VerifyV1([]byte(fixture.signClaimsV1(t, over)), atV1(30), ringOf(fixture), firebaseProjectV1)
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
	} {
		t.Run(tt.name, func(t *testing.T) {
			claims := v1Claims()
			claims.IssuedAt = paidThrough.Add(-365 * 24 * time.Hour)
			claims.PaidThrough = paidThrough
			claims.GraceDays = tt.grace

			_, state, err := VerifyV1([]byte(fixture.signClaimsV1(t, claims)), tt.at, ringOf(fixture), firebaseProjectV1)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if state != tt.want {
				t.Fatalf("a key with %d days of grace read at %s is %s, want %s", tt.grace, tt.at, state, tt.want)
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
				key = fixture.signRawV1(t, dropFieldV1(t, claims, "workspace_id"))
			} else {
				key = fixture.signClaimsV1(t, claims)
			}

			got, state, err := VerifyV1([]byte(key), atV1(30), ringOf(fixture), firebaseProjectV1)
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
	key := fixture.signClaimsV1(t, claims)

	if _, state, err := VerifyV1([]byte(key), atV1(30), ringOf(fixture), "a-workspace-firebase-project"); err != nil {
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
			got, state, err := VerifyV1([]byte(key), atV1(30), ringOf(fixture), tt.id)
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
func dropFieldV1(t *testing.T, c ClaimsV1, field string) []byte {
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
const vectorsV1File = "testdata/vectors_v1.json"

// vectorFileV1 is that file's shape. It is JSON a TypeScript verifier reads
// without a Go type, so it carries strings, numbers and booleans only -- no Go
// spelling of a state or a refusal, just the names the two verifiers agree on.
type vectorFileV1 struct {
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
	Staging vectorKeyV1 `json:"staging"`
	// Vectors is one key and what a verifier must answer for it.
	Vectors []vectorCaseV1 `json:"vectors"`
}

type vectorKeyV1 struct {
	Note      string `json:"note"`
	ID        string `json:"id"`
	PublicKey string `json:"public_key"`
}

type vectorCaseV1 struct {
	Name string `json:"name"`
	Key  string `json:"key"`
	// State and Claims are what an accepted key answers; Refused is the part a
	// refused one fails on. Exactly one half is set.
	State   string    `json:"state,omitempty"`
	Claims  *ClaimsV1 `json:"claims,omitempty"`
	Refused string    `json:"refused,omitempty"`
}

// vectorRefusalsV1 names each v1 refusal as the file spells it. The five the
// older format shares are here under the names the three older verifiers
// already use; the four this format adds are its own.
var vectorRefusalsV1 = map[string]error{
	"malformed":      ErrMalformedV1,
	"unknown-key-id": ErrUnknownKeyID,
	"signature":      ErrSignature,
	"claims":         ErrClaims,
	"lifetime":       ErrLifetime,
	"newer-version":  ErrNewerVersion,
	"workspace":      ErrWorkspace,
	"duration":       ErrDuration,
}

// boundaryVectorsV1 names the vectors that sit on a second a verifier can get
// wrong while every other vector still answers the same: both ends of the paid
// term, both ends of the grace, and the tier's longest term from both sides.
var boundaryVectorsV1 = []string{
	"a key at the second its paid term ends",
	"a key a second into its grace",
	"a key at the last second of its grace",
	"a key a second past its grace",
	"a key at the longest term its tier allows",
	"a term a second past the tier's maximum",
}

// loadVectorsV1 reads the v1 file the two verifiers are read against.
func loadVectorsV1(t *testing.T) vectorFileV1 {
	t.Helper()
	data, err := os.ReadFile(vectorsV1File)
	if err != nil {
		t.Fatalf("%s: %v -- the vectors are the set the console's verifier shares with this one, so a verifier whose tests cannot read them proves nothing", vectorsV1File, err)
	}
	var file vectorFileV1
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("%s is not the JSON this file's shape declares: %v", vectorsV1File, err)
	}
	return file
}

// ringV1 is the keyring the v1 vectors are asked with: the file's own keys,
// decoded.
func (f vectorFileV1) ring(t *testing.T) Keyring {
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

// vectorNamedV1 answers the vector a test names, so a test about one vector
// fails by name rather than by index if the file is reordered.
func (f vectorFileV1) vectorNamedV1(t *testing.T, name string) vectorCaseV1 {
	t.Helper()
	for _, v := range f.Vectors {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("%s carries no vector named %q", vectorsV1File, name)
	return vectorCaseV1{}
}

// TestVectorsV1RoundTrip replays the whole file through this verifier: every
// vector's answer, every refusal, and the boundaries by name. It is what keeps
// the file the console reads honest about the verifier it claims to describe.
func TestVectorsV1RoundTrip(t *testing.T) {
	file := loadVectorsV1(t)
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
			claims, state, err := VerifyV1([]byte(v.Key), now, ring, file.FirebaseProjectID)
			if v.Refused != "" {
				want, ok := vectorRefusalsV1[v.Refused]
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
			if !sameClaimsV1(claims, *v.Claims) {
				t.Fatalf("the key's claims are %+v, want %+v", claims, *v.Claims)
			}
		})
		seen[v.State+v.Refused] = true
	}

	// Each refusal and each state is in the file: a vector set that lost its
	// negative half would still round-trip green.
	for name := range vectorRefusalsV1 {
		if !seen[name] {
			t.Errorf("no vector in the file is refused as %s", name)
		}
	}
	for _, want := range []string{string(Active), string(Grace), string(Expired)} {
		if !seen[want] {
			t.Errorf("no vector in the file is %s", want)
		}
	}
	for _, name := range boundaryVectorsV1 {
		file.vectorNamedV1(t, name)
	}
}

// TestGenerateVectorsV1 rewrites testdata/vectors_v1.json. It is skipped unless
// the environment asks for it, so a plain `go test` never rewrites a file under
// a reviewer's feet: the signatures are freshly minted on every run and a
// rewritten file is a diff nobody asked for. Run it when the claim set, a state
// or a refusal changes:
//
//	FUGARO_LICENCE_WRITE_VECTORS_V1=1 go test -tags ee ./ee/licence/ -run TestGenerateVectorsV1 -count=1
func TestGenerateVectorsV1(t *testing.T) {
	if os.Getenv("FUGARO_LICENCE_WRITE_VECTORS_V1") == "" {
		t.Skip("set FUGARO_LICENCE_WRITE_VECTORS_V1=1 to rewrite testdata/vectors_v1.json")
	}
	data, err := json.MarshalIndent(generateVectorsV1(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vectorsV1File, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", vectorsV1File)
}

// generateVectorsV1 mints the v1 file's vectors. Every signature is made here,
// in this process, with a keypair thrown away when the test ends: the file
// holds the public half and the signed key texts and nothing a verifier could
// mint with (.claude/rules/ee.md).
func generateVectorsV1(t *testing.T) vectorFileV1 {
	t.Helper()
	fixture := newFixtureKey(t, fixtureV1KeyID)
	staging := newFixtureKey(t, stagingV1KeyID)

	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	days := func(n int) time.Time { return now.Add(time.Duration(n) * 24 * time.Hour) }

	// base is the claim set every vector starts from: a paid Team key bound to
	// the file's workspace, whose term and grace each vector then moves.
	base := func() ClaimsV1 {
		interval := "P1D"
		return ClaimsV1{
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

	file := vectorFileV1{
		Note:              "The claim sets, states and refusals the Go v1 verifier and the console's are both read against (licence-key-v2 plan K1.1, K1.4; spec sections 3, 4 and 6). Every key here is minted by a throwaway test keypair whose private half was generated in the process that wrote this file and never written down; no build trusts it, and ee/licence/keyv1.go's production keyring holds no id at all until S2.9 publishes the owner's production key. `now` is the clock every vector is read at, `firebase_project_id` is the workspace the verifier is asked with, `state` and `claims` are what an accepted key answers, and `refused` is the part a refused one fails on, named as the two verifiers name it.",
		Now:               now.Unix(),
		FirebaseProjectID: "fugaro-test-project",
		Keyring:           map[string]string{fixture.id: fixture.public()},
		Staging: vectorKeyV1{
			Note:      "The sandbox key: minted under the staging id for tests and for a sandbox installation, and in no production keyring, so a production verifier refuses it for the id alone.",
			ID:        staging.id,
			PublicKey: staging.public(),
		},
	}

	accept := func(name string, key string, state State, claims ClaimsV1) {
		file.Vectors = append(file.Vectors, vectorCaseV1{Name: name, Key: key, State: string(state), Claims: &claims})
	}
	refuse := func(name, key, refusal string) {
		file.Vectors = append(file.Vectors, vectorCaseV1{Name: name, Key: key, Refused: refusal})
	}
	sign := func(c ClaimsV1) string { return fixture.signClaimsV1(t, c) }

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
	accept("a signed key carrying a field this build does not know", fixture.signRawV1(t, extended), Active, withUnknown)

	// The refusals. Each one moves exactly one thing away from a good key.
	badInterval := base()
	hourly := "PT1H"
	badInterval.CheckInterval = &hourly
	refuse("a check interval this verifier does not read", sign(badInterval), "duration")

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
	refuse("a key of a version this build does not read", fixture.signRawV1(t, newerPayload), "newer-version")

	foreign := base()
	foreign.KID = "licence-someone-elses-key-v1"
	refuse("a key id this build does not hold", sign(foreign), "unknown-key-id")

	stagingClaims := base()
	stagingClaims.KID = staging.id
	refuse("a staging key", staging.signClaimsV1(t, stagingClaims), "unknown-key-id")

	// A payload signed as it is spelled and then re-spelled: the signature is
	// over the raw bytes, so one key has one spelling.
	spelled, err := json.Marshal(base())
	if err != nil {
		t.Fatal(err)
	}
	respelled := bytes.Replace(spelled, []byte(`":`), []byte(`": `), 1)
	refuse("a payload signed as it is spelled, re-spelled", encodeKeyV1(respelled, ed25519.Sign(fixture.priv, spelled)), "signature")

	// A payload changed after it was signed, in a way this build cannot read:
	// the answer is the signature's, and this is the vector that says the claim
	// decode waits for it. A verifier that read the claims first would answer
	// the claim refusal here.
	broken := append(bytes.TrimSuffix(spelled, []byte("}")), []byte(`,"seats":"five"}`)...)
	refuse("a claim set this build cannot read behind a signature that does not verify", encodeKeyV1(broken, ed25519.Sign(fixture.priv, spelled)), "signature")

	// A payload that is not a claim set, and the three texts that are not the
	// format: the shape and the payload are refused before any keyring is
	// consulted, which is why a key with no id in it is a malformed text rather
	// than an unknown one.
	refuse("a payload that is not a claim set", fixture.signRawV1(t, []byte("this is not a claim set")), "malformed")
	refuse("a key text that is not a v1 key", fixture.signClaims(t, Claims{Tier: TierTeam, Features: []string{}, Seats: 1, Organisation: "example.com", Issued: 1, Expires: 4000000000}), "malformed")
	good := sign(base())
	parts := strings.Split(strings.TrimPrefix(good, keyPrefixV1), ".")
	refuse("a key text with one part", keyPrefixV1+parts[0], "malformed")
	refuse("a part that is not base64url", keyPrefixV1+"%%%.%%%", "malformed")

	return file
}
