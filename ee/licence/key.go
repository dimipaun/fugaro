// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package licence checks a licence key: one Ed25519 signature over a JSON claim
// set, verified offline against a keyring compiled into the build.
//
// The design is docs/licensing/licence-key.md (subscriptions plan S2.1) and the
// task that builds it is S2.3. Three rules of that design are this file's whole
// shape:
//
//   - **The key id leads.** The key text is `<key id>.<payload>.<signature>`,
//     all three base64url, and an id the keyring does not hold is refused
//     before either other part is decoded. That is what makes a compiled-in
//     public key rotate, and it is what makes a staging key refused rather than
//     half-accepted.
//   - **The signature is checked over the payload's raw bytes, before any JSON
//     is parsed** (docs/licensing/licence-key.md, "Raw bytes before claims"), so
//     a re-spelled claim set fails the signature instead of verifying as the
//     same claims, and no claim is ever read out of a payload whose signature
//     did not verify. Every refusal past the shape check names the key id and the
//     part that failed, and none of them names a claim: a claim from an
//     unverified payload is text an attacker chose. The shape check itself names
//     nothing, because a text that is not three parts has no id to name.
//   - **There is no network call.** Nothing here asks a server whether a key is
//     good, which is also why there is no revocation: a key works until it
//     expires (D308). TestLicencePackageImportsNoNetwork holds the imports.
//
// Nothing in the core imports this package and no core caller reaches it: the
// check runs at the registration point (ee/register), and store.go is the CLI's
// own keeper of the key file and of the one variable the console hands a
// spawned CLI over.
package licence

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// The tiers a key can be issued for. They are names the claim set carries and
// the verifier places; the Terms' terms for them are the Terms' own words.
const (
	TierCommunity  = "community"
	TierTeam       = "team"
	TierEnterprise = "enterprise"
)

// grace is the 30 days a key keeps working after `expires` (B17). Only after it
// does the paid side lapse to the read-only state, which keeps reading, cancel
// and the kill switch (D436, T12); the MIT CLI is unaffected throughout.
const grace = 30 * 24 * time.Hour

// termLead is the 30 days before a key's expiry that the key following it is
// minted (T26). It is why a tier's maximum term is the tier's own term plus
// this lead: the longest key the design mints is a renewal that lands on the
// first day it may, and a ceiling of the bare term would refuse the very
// renewal the overlap exists for.
const termLead = 30 * 24 * time.Hour

// year is the longest term the ruled issuance gives a key today: section
// 10.2(b)'s open-source key is "valid for one year" and the Team and Enterprise
// annual terms run for a year, with the three-month commercial evaluation key
// well under it (docs/licensing/licence-key.md, "Limits, accepted").
const year = 365 * 24 * time.Hour

// tierMaxTerm is the longest term a key of each tier may be given. The three
// are equal today and they are a table rather than one number because the
// design states the bound per tier: granting a tier a longer one is a line here
// and never a change to the key format.
var tierMaxTerm = map[string]time.Duration{
	TierCommunity:  year + termLead,
	TierTeam:       year + termLead,
	TierEnterprise: year + termLead,
}

// State is what an installed key is against the clock the caller supplies.
type State string

const (
	// Valid is a key inside its term: now <= expires.
	Valid State = "valid"
	// Grace is the 30 days after the term ends: the key still works, and no
	// new key has to have arrived yet (T26).
	Grace State = "grace"
	// Expired is past the grace. It is a state of the key and not a refusal:
	// what an installation does with it is the lapse design's (S6.1).
	Expired State = "expired"
)

// Claims is the claim set a key's payload holds, and the whole of the licence's
// own terms as a verifier reads them. The key id is not a claim: it leads the
// key text in the clear, which is what lets the keyring be consulted before
// anything is decoded.
type Claims struct {
	// Tier is one of the three names above.
	Tier string `json:"tier"`
	// Features are the paid features the key turns on.
	Features []string `json:"features"`
	// Seats is what the organisation bought. It is compared and shown and never
	// enforced (D439).
	Seats int `json:"seats"`
	// Organisation is what the key was issued to: a domain, an individual as an
	// organisation of one, or an Order Form id. Nothing in the product compares
	// it with the installation it is read at, which is one of the accepted
	// limits rather than a check this verifier makes.
	Organisation string `json:"organisation"`
	// Issued is when the key was minted and Expires the end of its term, both
	// Unix seconds. They are the whole window: there is no covered-versions
	// date and no release-date stamping (O4).
	Issued  int64 `json:"issued"`
	Expires int64 `json:"expires"`
}

// The refusals. A key outside the three states is one of these rather than a
// fourth state, and a refusal names the key id and the part that failed.
var (
	// ErrMalformed is a key text that is not three non-empty base64url parts.
	ErrMalformed = errors.New("licence: the key is not three base64url parts")
	// ErrUnknownKeyID is an id the keyring does not hold, including a staging
	// key's: the id is refused before the payload or the signature is read.
	ErrUnknownKeyID = errors.New("licence: the key id is not in the keyring")
	// ErrSignature is a signature that does not verify over the payload's raw
	// bytes.
	ErrSignature = errors.New("licence: the signature does not verify")
	// ErrClaims is a verified payload this verifier cannot read as a claim set:
	// not JSON, a field it does not know, a tier it does not have, or no
	// organisation to bind the key to.
	ErrClaims = errors.New("licence: the claim set is not one this verifier knows")
	// ErrLifetime is a term longer than its tier allows.
	ErrLifetime = errors.New("licence: the term is longer than the tier allows")
	// ErrNoKey is an installation with no key at all, which is a state rather
	// than a failure: a fresh install, an upgraded console 0.112.0 and a free
	// evaluation that never asked for one all have none (F43).
	ErrNoKey = errors.New("licence: no key is installed")
	// ErrKeyFile is the CLI's own key file being unusable: a mode any other
	// local user can read, or more keys than the design installs.
	ErrKeyFile = errors.New("licence: the key file cannot be used")
)

// Keyring is a verifier's public keys, named by the key id the key text carries
// in the clear. The ids are compared as the text they are written as, so two
// spellings that would decode to the same bytes are two ids and neither is held
// unless it is.
type Keyring map[string]ed25519.PublicKey

// productionKeyring is the ring a released build trusts, and it is **empty
// here**. The owner creates the production signing keys (S3.8) and the task
// after this one publishes their public halves in this table (S2.9); until then
// every key is refused for its id alone, so nothing verifies in a build of this
// task. A test key never lands here: TestTestKeyIDIsNotInTheProductionKeyring
// is the half that holds the fixture's id out, and the staging key's refusal is
// the other.
var productionKeyring = Keyring{}

// Verify answers what the compiled-in keyring makes of one key: its claims and
// the state it is in against now.
//
// A refusal is one of the sentinels above, wrapped with the key id the text
// carried -- a text that is not three parts carries none, and splitKey answers
// bare for it -- and never with a claim. The caller supplies the clock rather
// than reading one, so what
// a key reads as at a given instant is a value a test can hold; the check reads
// the machine's clock in production, which is also the accepted limit that a
// clock set back extends `expires` (D308).
func Verify(raw []byte, now time.Time) (Claims, State, error) {
	return verify(raw, now, productionKeyring)
}

// verify is the check against a keyring of the caller's: the production ring
// for the CLI, and the vectors' own for the tests that have to exercise a key
// this build does not trust.
func verify(raw []byte, now time.Time, ring Keyring) (Claims, State, error) {
	id, encodedPayload, encodedSignature, err := splitKey(raw)
	if err != nil {
		return Claims{}, "", err
	}
	// The keyring is consulted before either other part is decoded: an id this
	// build does not hold is a key it cannot place, and the parts behind an id
	// nobody knows are text nobody has a reason to read.
	pub, held := ring[id]
	if !held {
		return Claims{}, "", fmt.Errorf("%w: %s", ErrUnknownKeyID, id)
	}
	payload, err := decodePart(encodedPayload, "the payload", id)
	if err != nil {
		return Claims{}, "", err
	}
	signature, err := decodePart(encodedSignature, "the signature", id)
	if err != nil {
		return Claims{}, "", err
	}
	if !ed25519.Verify(pub, payload, signature) {
		return Claims{}, "", fmt.Errorf("%w: %s", ErrSignature, id)
	}
	claims, err := decodeClaims(payload)
	if err != nil {
		return Claims{}, "", fmt.Errorf("%w: %s: %v", ErrClaims, id, err)
	}
	if err := validate(claims); err != nil {
		return Claims{}, "", fmt.Errorf("%w: %s", err, id)
	}
	return claims, state(claims, now), nil
}

// splitKey reads the key text's three parts as they are written: the order is
// the design's (the id leads) and the shape is checked here, while the decoding
// waits until the id has been placed.
//
// The outer whitespace is trimmed because a key arrives by paste as often as by
// file: a trailing newline is a newline and not a malformed key.
func splitKey(raw []byte) (id, payload, signature string, err error) {
	parts := strings.Split(strings.TrimSpace(string(raw)), ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", ErrMalformed
	}
	return parts[0], parts[1], parts[2], nil
}

// decodePart decodes one of a key's base64url parts. RawURLEncoding refuses the
// padded spelling and Strict refuses a non-canonical one, so two texts that
// decode to the same bytes are not both accepted: a malleable encoding is a
// second spelling of a key, and one key has one spelling.
func decodePart(encoded, part, id string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %s", ErrMalformed, id, part)
	}
	return decoded, nil
}

// decodeClaims reads the claim set out of a payload whose signature has already
// verified. The decode is strict -- a field this verifier does not know is an
// error rather than something ignored, and so is anything after the object --
// because a claim set is a wire format this project writes and every other
// reader of it (the console's and the app's verifiers) reads the same one.
func decodeClaims(payload []byte) (Claims, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	var claims Claims
	if err := dec.Decode(&claims); err != nil {
		return Claims{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Claims{}, errors.New("there is more after the claim set")
	}
	return claims, nil
}

// validate holds the claims to the format's own terms: a tier this verifier
// knows, an organisation the key binds to, and a term no longer than the tier's
// maximum.
//
// The maximum bounds one key's length from that key's own `issued`, which is
// not the same as bounding a leaked signing key: `issued` is the minter's to
// choose, so a key minted with an `issued` far in the future passes this and
// then reads valid for as long as its minter wants. That hole is stated in the
// design and is not closed here.
func validate(claims Claims) error {
	if claims.Organisation == "" {
		return ErrClaims
	}
	maximum, known := tierMaxTerm[claims.Tier]
	if !known {
		return ErrClaims
	}
	if claims.Expires > claims.Issued {
		// The difference is taken in unsigned arithmetic so that a term near
		// the ends of the range cannot wrap into a short one.
		if uint64(claims.Expires)-uint64(claims.Issued) > uint64(maximum/time.Second) {
			return ErrLifetime
		}
	}
	return nil
}

// state places a key against the clock: inside its term, inside the grace that
// follows it, or past both.
func state(claims Claims, now time.Time) State {
	seconds := now.Unix()
	if seconds <= claims.Expires {
		return Valid
	}
	if uint64(seconds)-uint64(claims.Expires) <= uint64(grace/time.Second) {
		return Grace
	}
	return Expired
}
