// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The default policy per tier (spec section 12.1), with D2133 and D2138 applied:
// the table every self-serve key is minted from, so that two keys of one tier
// carry one policy and every number a key states about itself comes from the key
// rather than from anything compiled into Fugaro (spec section 1).
//
// The rows are named rather than the tiers, because two rows are one tier: a
// Team key bought monthly and one bought annually differ in their lifetime
// alone, and an air-gapped Enterprise key differs from a connected one in its
// renewal mode and check interval. A row is what the minter is asked for, so
// `RequestV1.Policy` names one of these and the minter looks it up here.
//
// The table is a value and not a set of constants on the claim set because a
// key's policy is data: ee/licence's ClaimsV1 carries each of these fields into
// the key at issue, and this is the only place the server decides what a fresh
// key of a tier says. Overriding a row for one deal is the admin page's (K2.6)
// and is not read here.
//
// Alongside the table are the two things a minted key carries that belong to the
// key itself and not to its tier: its own `key_id`, and the renewal secret whose
// hash goes into the claim (D2135).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// The rows of the table, one per column of spec section 12.1. They are the
// names a mint request carries, and they are written as the table's own headings
// so that a reader comparing the two reads the same words in both.
const (
	PolicyTeamMonthly         = "team-monthly"
	PolicyTeamAnnual          = "team-annual"
	PolicyEnterprise          = "enterprise"
	PolicyEnterpriseAirGapped = "enterprise-air-gapped"
	PolicyTrial               = "trial"
)

// PolicyFreeSeat is D2184 and D2199's free seat: the one row that is not a
// column of section 12.1, because nobody buys it. It is Team at one seat, its
// kind is `comped` rather than `paid`, and it renews automatically: its holder
// pays nothing, so a key that lapsed would drop a customer's colleague to Free
// for a reason that is not about them (F39, D2203).
const PolicyFreeSeat = "free-seat"

const (
	// monthTerm is the lifetime of the monthly row and yearTerm of the annual
	// ones. A month is 30 days here for the reason the grace and the renewal
	// lead are (B17, T26): it is the same span, and a purchase's own term
	// arrives from the merchant's event in days in any case (K2.5).
	monthTerm = 30 * 24 * time.Hour
	yearTerm  = 365 * 24 * time.Hour
	// trialTerm is section 12.1's fourteen days, which is the trial of D2136.
	trialTerm = 14 * 24 * time.Hour

	// graceDays is D2133's 30: the spec's 14 becomes 30, and every row carries
	// it except the trial, whose grace D2138 rules is none at all.
	graceDays = 30
	// airGappedGraceDays is D2138's 30 for a key that never contacts the
	// server, which is the one row where grace is the whole of what stands
	// between the key's end and a lapse nobody could have prevented.
	airGappedGraceDays = 30
	// trialGraceDays is D2138's 0: a trial that has run out is over, and the
	// project drops to Free rather than keeping the paid features for another
	// month.
	trialGraceDays = 0

	// renewalLeadDays is how early the renewal job starts asking for the next
	// key. Section 12.1 does not state it and the payload example of section
	// 3.2 writes 7, so a connected paid row carries 7 and the two rows that
	// renew by hand or not at all carry none.
	renewalLeadDays = 7
)

// dailyCheck is the check interval of every connected row: revocation is
// separate from expiry, so a monthly key checks daily (spec section 8), and it
// is the one duration form the design reads as "every day" (F16).
const dailyCheck = "P1D"

// connectedTerm is D2202's thirty days: the longest one key of a row whose
// installation contacts the server may run, whatever its row is sold for.
//
// The bound is the rebind multiplier's (F38). A key is what ends an
// installation's access, and an installation that has been rebound to somebody
// else keeps answering from the copy it holds until that copy ends, so a key
// sold for a year is a year of the entitlement still working somewhere nobody
// is looking. Thirty days is the window the server's own answer closes in, and
// it is why the plan accepts that an installation with no renewal job of its
// own must refresh every month or lapse after its grace.
//
// A row whose key never checks has no such window to close and keeps its term
// (D2202). Which of the two a row is, is its own `CheckInterval`: the same
// column that says a key cannot be revoked by contact says it is not sold
// short either.
const connectedTerm = 30 * 24 * time.Hour

// The bounds F28 puts on the check interval an operations mint may name: a key
// that checks more often than daily costs load for nothing, and one that checks
// less often than monthly carries a revocation window longer than the term
// every connected key runs (D2202). They are durations because the bound is on
// what a spelling means, not on how it is spelled: `P1W` is inside them and
// `P7D` is the same interval.
const (
	minCheckInterval = 24 * time.Hour
	maxCheckInterval = 30 * 24 * time.Hour
)

// checkIntervalInBounds refuses an interval a caller named that is outside
// F28's `P1D`–`P30D`, and it parses the spelling through the verifier's own
// reader: a duration no verifier reads (`PT1H`, `P1M`, `30d`) is refused here
// rather than minted into a key that will not load.
//
// `P0D` parses, to a zero duration, and it is below the shortest of the two
// bounds rather than a way of saying a key never checks: `null` is the only way
// a key says that, and it is the air-gapped row's and no request's (F28).
func checkIntervalInBounds(interval string) error {
	span, err := licence.ParseISODuration(interval)
	if err != nil {
		return fmt.Errorf("the check interval %q is not one a key reads: %w", interval, err)
	}
	if span < minCheckInterval || span > maxCheckInterval {
		return fmt.Errorf("the check interval %q is outside the daily-to-monthly span an operations mint may name (F28)", interval)
	}
	return nil
}

// checkSecretHash refuses a hash a caller asks a key to carry that is not one
// this service could have written: D2135's is the hex sha256 of a 256-bit
// secret, so it is 64 lower-case hex digits and nothing else. The shape is the
// whole check — the minter cannot know which entitlement a hash belongs to,
// that is the store's — and it is what keeps the field from becoming a way to
// write a secret, a truncated hash or an empty string into a claim every reader
// of the runs bucket holds.
func checkSecretHash(hash string) error {
	if len(hash) != sha256.Size*2 || strings.ToLower(hash) != hash {
		return errors.New("a kept renewal secret hash is 64 lower-case hex digits (D2135), and this is not one")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return errors.New("a kept renewal secret hash is 64 lower-case hex digits (D2135), and this is not one")
	}
	return nil
}

// maxKeyTerm is the longest one key of a row may run from its own `issued_at`:
// D2202's thirty days for a row whose key checks the server, and the row's own
// term for the air-gapped row that never does.
func maxKeyTerm(policy Policy) time.Duration {
	if policy.CheckInterval == nil || policy.Term < connectedTerm {
		return policy.Term
	}
	return connectedTerm
}

// Policy is one row: the whole of what a fresh key of one tier carries, as
// ee/licence's ClaimsV1 reads it back.
type Policy struct {
	// Name is the row's name and what a mint request asks for.
	Name string
	// Tier is the tier every key of this row carries.
	Tier string
	// Kind is `paid` or `trial`; a `comped` key is the founder's and carries a
	// row of its own when the admin page mints one (K2.6).
	Kind string
	// Term is the row's lifetime, and it is what a mint request that names no
	// term of its own is minted for: a first purchase is the plan's own period.
	// A renewal names one, because it covers the rest of the term the customer
	// is in rather than a fresh one. It is also the longest a key of this row
	// may ever run, so a row whose key checks the server is minted shorter than
	// this and never longer (D2202; `maxKeyTerm`).
	Term time.Duration
	// Seats is the row's own seat count, or 0 for a row whose seats the caller
	// names. One row has one: the free seat is one seat and not a plan, so a
	// request that asks it for another count is refused rather than minted into
	// a key that hands out seats nobody granted (D2184, D2199).
	Seats int
	// GraceDays is this key's own grace, written into the key (D2133).
	GraceDays int
	// RenewalMode is one of licence's three names: `auto` for a connected paid
	// key, `none` for the air-gapped key and the trial, which nothing renews.
	RenewalMode string
	// RenewBeforeDays is how early the job starts trying, and 0 for a row whose
	// mode is `none`.
	RenewBeforeDays int
	// CheckInterval is the revocation check interval, or nil for a key that
	// never contacts the server at all (spec section 8).
	CheckInterval *string
	// Warnings is who is warned and from when, counted from the start of grace.
	// A row with no grace carries none, because no window of it ever opens.
	Warnings []licence.WarningV1
}

// defaultPolicies is spec section 12.1's table, one entry per column.
var defaultPolicies = []Policy{
	{
		Name:            PolicyTeamMonthly,
		Tier:            licence.TierTeam,
		Kind:            licence.KindPaid,
		Term:            monthTerm,
		GraceDays:       graceDays,
		RenewalMode:     licence.RenewalAuto,
		RenewBeforeDays: renewalLeadDays,
		CheckInterval:   daily(),
		Warnings:        defaultWarnings(),
	},
	{
		Name:            PolicyTeamAnnual,
		Tier:            licence.TierTeam,
		Kind:            licence.KindPaid,
		Term:            yearTerm,
		GraceDays:       graceDays,
		RenewalMode:     licence.RenewalAuto,
		RenewBeforeDays: renewalLeadDays,
		CheckInterval:   daily(),
		Warnings:        defaultWarnings(),
	},
	{
		Name:            PolicyEnterprise,
		Tier:            licence.TierEnterprise,
		Kind:            licence.KindPaid,
		Term:            yearTerm,
		GraceDays:       graceDays,
		RenewalMode:     licence.RenewalAuto,
		RenewBeforeDays: renewalLeadDays,
		CheckInterval:   daily(),
		Warnings:        defaultWarnings(),
	},
	{
		Name:            PolicyEnterpriseAirGapped,
		Tier:            licence.TierEnterprise,
		Kind:            licence.KindPaid,
		Term:            yearTerm,
		GraceDays:       airGappedGraceDays,
		RenewalMode:     licence.RenewalNone,
		RenewBeforeDays: 0,
		CheckInterval:   nil,
		Warnings:        defaultWarnings(),
	},
	{
		Name:            PolicyTrial,
		Tier:            licence.TierTeam,
		Kind:            licence.KindTrial,
		Term:            trialTerm,
		GraceDays:       trialGraceDays,
		RenewalMode:     licence.RenewalNone,
		RenewBeforeDays: 0,
		CheckInterval:   daily(),
		Warnings:        nil,
	},
	{
		Name:            PolicyFreeSeat,
		Tier:            licence.TierTeam,
		Kind:            licence.KindComped,
		Term:            yearTerm,
		Seats:           1,
		GraceDays:       graceDays,
		RenewalMode:     licence.RenewalAuto,
		RenewBeforeDays: renewalLeadDays,
		CheckInterval:   daily(),
		Warnings:        defaultWarnings(),
	},
}

// DefaultPolicy answers the row a name names. The row is copied out rather than
// handed over, so that a caller which edits what it was given — a warning
// appended to the list before a key is minted — cannot change what the next mint
// of that tier carries. What the row points at is copied too, for the same
// reason: a written-through check interval would move the interval every later
// key of that row is checked at, which is a key's policy changed by a caller.
func DefaultPolicy(name string) (Policy, bool) {
	for _, policy := range defaultPolicies {
		if policy.Name != name {
			continue
		}
		if policy.CheckInterval != nil {
			interval := *policy.CheckInterval
			policy.CheckInterval = &interval
		}
		if len(policy.Warnings) > 0 {
			warnings := make([]licence.WarningV1, 0, len(policy.Warnings))
			for _, warning := range policy.Warnings {
				warning.Roles = append([]string(nil), warning.Roles...)
				warnings = append(warnings, warning)
			}
			policy.Warnings = warnings
		}
		return policy, true
	}
	return Policy{}, false
}

// daily is a row's check interval: a fresh pointer per row, so that a caller
// which writes through one cannot move the interval every other row checks at.
func daily() *string {
	interval := dailyCheck
	return &interval
}

// defaultWarnings is spec section 7.1's table as the payload example of section
// 3.2 spells it: admins are told from the first day of grace, and everyone from
// the second week, so a developer can nudge an admin before the key lapses.
func defaultWarnings() []licence.WarningV1 {
	return []licence.WarningV1{
		{Roles: []string{"admin"}, From: "grace_start"},
		{Roles: []string{"admin", "operator", "viewer"}, From: "grace_start+7d"},
	}
}

// keyIDPrefix is what makes a key id recognisable in a support ticket and in a
// store's list, which is the prefix's whole reason (spec section 3.2).
const keyIDPrefix = "lk_"

// newKeyID names one key. It is random rather than a counter because the id
// names a key and not a position in a list: two keys minted at once must not be
// able to become one, and an id quoted in a ticket tells its reader nothing
// about how many keys the service has issued.
func newKeyID() (string, error) {
	random, err := newSecret()
	if err != nil {
		return "", fmt.Errorf("the key id: %w", err)
	}
	return keyIDPrefix + random, nil
}

// newRenewalSecret is D2135's per-key renewal secret, and the hash of it that
// the key carries in its place. It is 256 bits from the system's CSPRNG, whole
// and never derived; the secret goes to the holder alone, as the second line of
// the bundle every issue path shows once, while the hash is in the key, which
// every reader of the runs bucket holds.
//
// That asymmetry is the amendment D2135 carries: the renewal call sends the
// secret and the server compares a hash, rather than presenting an HMAC keyed by
// something a bucket reader can read — which would have made the reader able to
// renew.
func newRenewalSecret() (secret, hash string, err error) {
	secret, err = newSecret()
	if err != nil {
		return "", "", fmt.Errorf("the renewal secret: %w", err)
	}
	return secret, digest(secret), nil
}
