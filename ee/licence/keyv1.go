// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package licence

// The v1 licence key (decision D2130, spec sections 3, 4 and 6): the text
// `fgk_<base64url payload>.<base64url signature>`, an Ed25519 signature over
// the payload's raw bytes, and a claim set that carries the whole of a key's
// policy -- its tier, term, grace, renewal and warning windows -- rather than
// leaving any of it compiled into Fugaro.
//
// This file is **additive** beside key.go's older `<id>.<payload>.<signature>`
// format. Nothing here reads or writes the old one, and nothing in the old one
// reads this: the two verifiers coexist until K7.3 deletes the old format and
// renames these types to the package's Claims and Verify.
//
// Four rules shape the check, and the first three are consequences of this
// format rather than preferences:
//
//   - **The signature is verified over the raw bytes before any claim is
//     decoded, and no claim of an unverified payload is ever echoed.** The old
//     format wrote the key id in the clear in front of the payload, which is
//     what let it consult the keyring before decoding anything; a v1 key writes
//     its id inside the payload, so there is nothing to verify the signature
//     *with* until that one field has been read. peekV1 therefore reads `kid`
//     and `v` and nothing else, the signature is verified over the payload's
//     raw bytes next, and the claim set is decoded only after it has. Every
//     refusal before that point carries no part of the payload at all -- not
//     the id, which is a claim of an unverified payload like any other, and not
//     the text that failed to decode -- because the text behind a signature
//     nobody has verified is text an attacker chose.
//   - **Unknown fields are ignored, and only here** (D2146). Every other wire
//     format this project writes decodes strictly (CLAUDE.md, "Config and wire
//     formats decode strictly"), and the spec's "unknown optional fields are
//     ignored" is a named exception for a *signed* v1 key: the tolerance is
//     applied to a payload whose signature has already verified, and to nothing
//     else. A key text with no signature, and the older format, stay strict.
//   - **The workspace binds the key.** `workspace_id` is required and names the
//     workspace's Firebase project id, which the verifier compares with the id
//     the caller passes in -- K3.1 and the console read that id from the
//     project's own config in the same read as the key (D2134 as amended by
//     TN2). A key with no workspace id is a claim set this verifier cannot
//     place; a key bound to another workspace is refused. Neither value is
//     echoed: they are not secrets, but a refusal is a sentence a user pastes
//     into a ticket, and the sentence does not need either one to be useful.
//   - **The renewal secret is never a claim.** The payload carries only
//     `renewal_secret_hash` (D2135); the secret itself lives in the
//     installation's Secret Manager, and the hash in the key is not a signing
//     key -- any reader of the runs bucket holds it.
//
// There is still no network call. TestLicencePackageImportsNoNetwork holds the
// imports of every file in this package, this one included, and the scheduled
// job that talks to the licence server is a separate command (K4.1).

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

const (
	// keyPrefixV1 is what makes a v1 key recognisable in a log, a secret
	// scanner and a support ticket, which is the prefix's whole reason (spec
	// section 3.1).
	keyPrefixV1 = "fgk_"
	// versionV1 is the only policy version this verifier reads. A higher one is
	// refused with the upgrade message and is never read as anything else: it
	// never falls back to Free (spec section 4).
	versionV1 = 1
)

// The renewal modes a key may carry. They are names the claim set carries and
// this verifier places; what each one makes an installation do is the renewal
// job's (K4.1), and `none` is the air-gapped key that runs to its expiry and
// never contacts the server.
const (
	RenewalAuto   = "auto"
	RenewalManual = "manual"
	RenewalNone   = "none"
)

// The kinds a key may be issued as. A comped key is one the founder issued by
// hand and has an expiry like any other; a trial is a Team key without payment.
const (
	KindPaid   = "paid"
	KindTrial  = "trial"
	KindComped = "comped"
)

// Active is a v1 key inside its paid term: now <= paid_through. The other two
// states of the v1 set are key.go's Grace and Expired, whose values this format
// writes too: the state a key is in is one name shared by every reader of the
// key, so only the first of the three is new here, and K7.3 is the task that
// moves all three together into the file that survives.
const Active State = "active"

// The v1 refusals. The five shared with the older format are key.go's; the four
// below are this format's own, and every one of them is a sentinel rather than
// a message built at the failure, so a caller can answer it with errors.Is.
var (
	// ErrMalformedV1 is a key text that is not `fgk_` followed by two non-empty
	// base64url parts, or whose payload names no keyring entry at all. It is
	// separate from the older format's ErrMalformed because the two say
	// different things: a v1 key has two parts, not three, and its first part
	// is a payload rather than a key id.
	ErrMalformedV1 = errors.New("licence: the key is not a v1 key: fgk_<payload>.<signature>")

	// ErrNewerVersion is a signed key whose `v` is higher than this build reads.
	// Its text is the message the spec pins, word for word, and the verifier
	// returns it unwrapped so that a caller prints exactly it: the key is
	// refused, and never silently read as Free (spec section 4).
	ErrNewerVersion = errors.New("This licence key requires a newer version of Fugaro. Upgrade Fugaro to use it.")

	// ErrWorkspace is a key bound to a workspace other than the caller's, or a
	// caller with no Firebase project id to compare against. It names neither
	// value: the comparison is what failed, and the two ids are not secrets but
	// neither is a sentence a user needs in a refusal.
	ErrWorkspace = errors.New("licence: the key is bound to a different workspace")

	// ErrDuration is a `check_interval` that is not one of the two ISO 8601
	// duration forms this verifier reads (F16). Anything else -- `PT1H`, `P1M`,
	// `1 day` -- is refused rather than read as a shorter interval than it says,
	// and `null` is how a key says it never checks.
	ErrDuration = errors.New("licence: the check interval is not a duration this verifier reads")
)

// ClaimsV1 is the claim set a v1 key's payload holds: the whole of the licence's
// terms as this verifier reads them (spec section 3.2).
//
// The times are `time.Time` because the format writes them as RFC 3339 stamps
// (`"issued_at": "2026-11-12T00:00:00Z"`), and the decode below is the only
// reader of them: a key whose stamp is not one is a claim set this verifier
// cannot place.
type ClaimsV1 struct {
	// V is the policy version. This verifier reads 1 and refuses anything
	// higher with ErrNewerVersion (spec section 4).
	V int `json:"v"`
	// KID names the signing key that produced the signature, so the keyring can
	// rotate by carrying a second entry (spec section 5). It is the one claim
	// read before the signature verifies, because it is what selects the public
	// key that verifies it.
	KID string `json:"kid"`
	// KeyID is the key's own identity, used for revocation and support. It is
	// not the signing key's id and it is not a secret.
	KeyID string `json:"key_id"`
	// WorkspaceID is required and names the workspace's Firebase project id: the
	// key is refused with no workspace id at all and refused when this differs
	// from the caller's project (D2134 as amended by TN2).
	WorkspaceID string `json:"workspace_id"`
	// Organisation is what the key was issued to: a domain, an individual as an
	// organisation of one, or an Order Form id. Nothing compares it with the
	// installation it is read at, which is the accepted limit TN2 keeps rather
	// than a check this verifier makes.
	Organisation string `json:"organisation"`
	// Tier is one of key.go's three names.
	Tier string `json:"tier"`
	// Seats is what the organisation bought. It is compared and shown here and
	// enforced only on the `ee` surfaces (D2132, held with K3.4).
	Seats int `json:"seats"`
	// IssuedAt is when the key was minted and PaidThrough the end of its paid
	// period. The key is active until PaidThrough, in grace for its own
	// GraceDays after it, and expired beyond both.
	IssuedAt    time.Time `json:"issued_at"`
	PaidThrough time.Time `json:"paid_through"`
	// GraceDays is this key's own grace, not a number compiled into Fugaro: it
	// is what lets a trial carry none and an air-gapped key carry the full
	// thirty (D2133, D2138).
	GraceDays int `json:"grace_days"`
	// RenewalMode is one of the three names above.
	RenewalMode string `json:"renewal_mode"`
	// RenewBeforeDays is how early the renewal job starts asking for the next
	// key. This verifier carries it and does not act on it.
	RenewBeforeDays int `json:"renew_before_days"`
	// CheckInterval is the ISO 8601 duration between revocation checks, or nil
	// for a key that never checks (an offline key). It is separate from expiry:
	// a monthly key may check daily.
	CheckInterval *string `json:"check_interval"`
	// RenewalSecretHash is the hash of the per-key renewal secret and never the
	// secret (D2135). The secret is sent to the licence server by the renewal
	// job alone; nothing in a key can renew with this.
	RenewalSecretHash string `json:"renewal_secret_hash"`
	// Warnings is who is warned and from when, each window written as
	// `grace_start` or `grace_start+<n>d`. This verifier carries them; K3.5
	// reads them.
	Warnings []WarningV1 `json:"warnings"`
	// Kind is one of the three names above.
	Kind string `json:"kind"`
	// Note is free text shown to admins only.
	Note string `json:"note"`
}

// WarningV1 is one warning window: the roles it reaches and the moment it opens
// at, counted from the start of the key's grace.
type WarningV1 struct {
	Roles []string `json:"roles"`
	From  string   `json:"from"`
}

// VerifyV1 answers what a keyring of the caller's makes of one v1 key: its
// claims and the state it is in against now.
//
// The ring is a parameter rather than the compiled-in productionKeyring because
// the callers that exist so far hold their own: the owner's mint tool checks
// what it minted against the public half it was given (K1.3), and the console
// holds the ring it ships (K1.4). K3.1 is the first caller that reads a project
// key, and it passes the same ring every other `ee` surface does.
//
// firebaseProjectID is the workspace's own Firebase project id, read by the
// caller from the project's config in the same read as the key (TN2). An empty
// one never accepts a key: a workspace id that matches nothing is a mismatch
// like any other.
//
// The caller supplies the clock rather than this reading one, so what a key
// reads as at a given instant is a value a test can hold.
func VerifyV1(raw []byte, now time.Time, ring Keyring, firebaseProjectID string) (ClaimsV1, State, error) {
	payload, signature, err := splitKeyV1(raw)
	if err != nil {
		return ClaimsV1{}, "", err
	}
	// The id is read here and nowhere else before the signature verifies, and
	// it is not echoed: it names the keyring entry, and until the signature
	// over the payload has verified it is text the payload's author chose.
	kid, version, ok := peekV1(payload)
	if !ok {
		return ClaimsV1{}, "", ErrMalformedV1
	}
	pub, held := ring[kid]
	if !held {
		return ClaimsV1{}, "", ErrUnknownKeyID
	}
	if !ed25519.Verify(pub, payload, signature) {
		return ClaimsV1{}, "", ErrSignature
	}
	// Past this line the payload is the one the ring signed, so what it says is
	// a claim rather than a guess. The version is read before the claim set is
	// decoded, so a key of a version this build does not know is refused with
	// the upgrade message whatever shape its payload has.
	if version > versionV1 {
		return ClaimsV1{}, "", ErrNewerVersion
	}
	if version < versionV1 {
		return ClaimsV1{}, "", fmt.Errorf("%w: the policy version is not one this verifier reads", ErrClaims)
	}
	claims, err := decodeClaimsV1(payload)
	if err != nil {
		return ClaimsV1{}, "", fmt.Errorf("%w: %v", ErrClaims, err)
	}
	if err := validateV1(claims, firebaseProjectID); err != nil {
		return ClaimsV1{}, "", err
	}
	return claims, stateV1(claims, now), nil
}

// splitKeyV1 reads a v1 key text as it is written: the `fgk_` prefix, then a
// payload and a signature, both unpadded base64url. The outer whitespace is
// trimmed because a key arrives by paste as often as by file.
//
// The two parts are decoded here rather than through key.go's decodePart, which
// names the key id in its refusal: a v1 key has no id in the clear, and the
// refusal for a part that will not decode must not carry one.
//
// Both parts are checked against the alphabet (key.go's isBase64url) before
// either is decoded, and that check is what the strict decoder does not give:
// Go's base64 skips carriage returns and newlines wherever it meets them, so a
// text with one folded into a part decodes to the same key as the text without
// it and verifies. A malleable encoding is a second spelling of a key, and one
// key has one spelling -- the rule key.go's decodePart states and this keeps.
// Outer whitespace is still trimmed above, because a key arrives by paste.
func splitKeyV1(raw []byte) (payload, signature []byte, err error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), keyPrefixV1)
	if !ok {
		return nil, nil, ErrMalformedV1
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || !isBase64url(parts[0]) || !isBase64url(parts[1]) {
		return nil, nil, ErrMalformedV1
	}
	payload, err = base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return nil, nil, ErrMalformedV1
	}
	signature, err = base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return nil, nil, ErrMalformedV1
	}
	return payload, signature, nil
}

// peekV1 reads the two fields that have to be known before the signature can be
// verified: the id that names the keyring entry, and the policy version that
// decides whether this build reads the key at all. Everything else waits until
// the signature has verified.
//
// A payload that is not a JSON object, or that carries no id, names no keyring
// entry, so there is no key to verify it with and no claim that can be placed:
// it answers false and the caller refuses the text as malformed. The refusal
// carries none of the payload -- the error this reads from encoding/json names
// a character or a field of text nobody has verified, which is exactly what the
// first rule of this file keeps out of a message.
//
// The read takes one JSON value and leaves whatever follows it alone. Bytes
// after the object are the claim decode's to refuse, which is where the older
// format refuses them too; catching them here instead would answer a different
// refusal for the same payload depending on which format it was written in.
//
// `v` is read as the JSON integer the format writes it as. A payload spelling
// it any other way -- a string, a fraction -- names no version this verifier
// can compare, and the text is refused as malformed rather than read as a
// version it might be. That is still a refusal and never a fall back to Free:
// what it is not is the upgrade message, which is reserved for a key that says
// which version it is.
func peekV1(payload []byte) (kid string, version int, ok bool) {
	var head struct {
		KID string `json:"kid"`
		V   int    `json:"v"`
	}
	if err := json.NewDecoder(bytes.NewReader(payload)).Decode(&head); err != nil {
		return "", 0, false
	}
	if head.KID == "" {
		return "", 0, false
	}
	return head.KID, head.V, true
}

// decodeClaimsV1 reads the claim set out of a payload whose signature has
// already verified. Unlike the older format's decodeClaims it does **not**
// disallow unknown fields: D2146 makes a signed v1 key the one wire format in
// this project that ignores them, so that a field added within version 1 has a
// safe default in a build that predates it (spec section 4).
//
// Everything else stays strict. The decode still runs to the end of the payload
// -- a second object after the claim set is a payload this verifier cannot
// place rather than one it reads and ignores the rest of -- and a known field
// carrying the wrong type is still an error.
func decodeClaimsV1(payload []byte) (ClaimsV1, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	var claims ClaimsV1
	if err := dec.Decode(&claims); err != nil {
		return ClaimsV1{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ClaimsV1{}, errors.New("there is more after the claim set")
	}
	return claims, nil
}

// validateV1 holds a verified claim set to the terms of the format: the
// workspace it binds to, a tier, renewal mode and kind this verifier knows, a
// check interval of one of the two duration forms it reads, and a term no
// longer than the tier's own maximum.
//
// The maximum is key.go's table and the same thirty-day lead: the longest key
// the design mints is a renewal that lands on the first day it may, and a
// ceiling of the bare term would refuse the very renewal the overlap exists
// for. It bounds one key's length from that key's own `issued_at`, which is the
// minter's to choose and is therefore not a bound on a leaked signing key --
// the accepted limit the old format states as well. A term that runs backwards
// is left to the state it produces rather than refused for its length: a
// `paid_through` before its own `issued_at` is a key already past, and reading
// it as expired is what the format's own terms say.
func validateV1(claims ClaimsV1, firebaseProjectID string) error {
	if claims.WorkspaceID == "" {
		return fmt.Errorf("%w: the key carries no workspace id", ErrClaims)
	}
	if claims.WorkspaceID != firebaseProjectID {
		return ErrWorkspace
	}
	maximum, known := tierMaxTerm[claims.Tier]
	if !known {
		return fmt.Errorf("%w: the tier is not one this verifier knows", ErrClaims)
	}
	if !knownRenewalMode(claims.RenewalMode) {
		return fmt.Errorf("%w: the renewal mode is not one this verifier knows", ErrClaims)
	}
	if !knownKind(claims.Kind) {
		return fmt.Errorf("%w: the kind is not one this verifier knows", ErrClaims)
	}
	if claims.CheckInterval != nil {
		if _, err := ParseISODuration(*claims.CheckInterval); err != nil {
			return err
		}
	}
	if claims.PaidThrough.After(claims.IssuedAt) && claims.PaidThrough.Sub(claims.IssuedAt) > maximum {
		return ErrLifetime
	}
	return nil
}

// stateV1 places a key against the clock: inside its paid term, inside the
// grace the key itself carries, or past both. The grace is the key's own
// `grace_days` and never a number this build holds, which is what lets one
// trial carry none and one air-gapped key carry thirty (D2133, D2138).
func stateV1(claims ClaimsV1, now time.Time) State {
	if !now.After(claims.PaidThrough) {
		return Active
	}
	if now.Sub(claims.PaidThrough) <= graceDuration(claims.GraceDays) {
		return Grace
	}
	return Expired
}

// graceDuration is a key's own grace as a duration, clamped so that a claim set
// carrying an absurd `grace_days` cannot wrap the arithmetic into a window
// shorter than the key states: past the longest duration there is, the grace is
// that longest duration and the key stays in grace for as long as this verifier
// can say. A negative `grace_days` is no grace at all.
func graceDuration(days int) time.Duration {
	if days <= 0 {
		return 0
	}
	if int64(days) > maxIntervalDays {
		return time.Duration(1<<62 - 1)
	}
	return time.Duration(days) * 24 * time.Hour
}

// knownRenewalMode answers whether a renewal mode is one of the three names the
// format gives the field.
func knownRenewalMode(mode string) bool {
	switch mode {
	case RenewalAuto, RenewalManual, RenewalNone:
		return true
	}
	return false
}

// knownKind answers whether a kind is one of the three names the format gives
// the field.
func knownKind(kind string) bool {
	switch kind {
	case KindPaid, KindTrial, KindComped:
		return true
	}
	return false
}

// maxIntervalDays is the longest span, in days, that still fits a time.Duration
// with room for the multiply below. A duration in nanoseconds runs out at about
// 106,751 days, and the parser and the grace arithmetic both stop here so that
// neither wraps a value too large into a window far shorter than the key says.
const maxIntervalDays = (1 << 62) / (24 * 60 * 60 * 1_000_000_000)

// ParseISODuration reads the ISO 8601 durations a v1 key may carry: `P<n>D` and
// `P<n>W`, and nothing else (F16). `PT1H`, `P1M`, `P1Y` and a duration spelled
// any other way are refused rather than approximated, because the two forms
// here are the ones the licence server writes and a check interval that means
// something other than what it says is worse than a key that will not load.
//
// A zero-length interval parses to a zero duration rather than to the refusal:
// `P0D` is a duration, and `null` is how a key says it never checks at all.
//
// The refusal carries none of the text: this is exported for the callers that
// read a key's policy, and a caller may hand it a claim set nobody has verified
// yet.
func ParseISODuration(text string) (time.Duration, error) {
	if len(text) < 3 || text[0] != 'P' {
		return 0, ErrDuration
	}
	value, ok := decimalDigits(text[1 : len(text)-1])
	if !ok {
		return 0, ErrDuration
	}
	switch text[len(text)-1] {
	case 'D':
		return time.Duration(value) * 24 * time.Hour, nil
	case 'W':
		if value > maxIntervalDays/7 {
			return 0, ErrDuration
		}
		return time.Duration(value) * 7 * 24 * time.Hour, nil
	default:
		return 0, ErrDuration
	}
}

// decimalDigits reads a run of ASCII digits as a number of days, refusing an
// empty run and one too long to be a duration. The bound is applied digit by
// digit, so the multiply inside the loop cannot overflow before the check that
// would reject the value: a text of a thousand nines answers false rather than
// wrapping into a small number of days.
func decimalDigits(text string) (int64, bool) {
	if text == "" {
		return 0, false
	}
	var days int64
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		days = days*10 + int64(c-'0')
		if days > maxIntervalDays {
			return 0, false
		}
	}
	return days, true
}
