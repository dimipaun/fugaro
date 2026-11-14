// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The default policy per tier (spec section 12.1 with D2133 and D2138 applied):
// the rows every self-serve key is minted from, held here against the spec's own
// table, so that a row changing is a change a reader finds in both places rather
// than a number that moved on its own.

import (
	"context"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// TestFreeSeatRowIsComped: D2184 and D2199 — the free seat is one seat of Team,
// its kind is `comped` because nobody bought it, and it renews automatically so
// that the colleague it was granted to does not drop to Free while the
// organisation's own dealing is what carries them. It is a row of the table like
// any other, and the key it mints is one the released verifier reads.
func TestFreeSeatRowIsComped(t *testing.T) {
	row, known := DefaultPolicy(PolicyFreeSeat)
	if !known {
		t.Fatal("the table holds no free-seat row, and D2199 grants the seat by minting one")
	}
	if row.Tier != licence.TierTeam || row.Kind != licence.KindComped || row.Seats != 1 {
		t.Errorf("the free seat is %s/%s at %d seat(s), want Team, comped and one (D2184, D2199)", row.Tier, row.Kind, row.Seats)
	}
	if row.RenewalMode != licence.RenewalAuto || row.RenewBeforeDays != renewalLeadDays {
		t.Errorf("the free seat renews %s %d day(s) early, want %s and %d: nothing buys it, so nothing but a renewal keeps its holder off Free",
			row.RenewalMode, row.RenewBeforeDays, licence.RenewalAuto, renewalLeadDays)
	}
	if row.CheckInterval == nil {
		t.Error("the free seat's key carries no check interval, want one: a comped key is revoked by contact like any other (F29)")
	}

	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}
	request := func(seats int) RequestV1 {
		return RequestV1{
			Environment:  Production,
			Policy:       PolicyFreeSeat,
			WorkspaceID:  workspaceID,
			Organisation: "acme.example",
			Seats:        seats,
		}
	}
	minted, err := minter.MintV1(context.Background(), request(1))
	if err != nil {
		t.Fatalf("minting a free seat: %v", err)
	}
	claims := v1Verified(t, f, minted.Key)
	if claims.Tier != licence.TierTeam || claims.Kind != licence.KindComped || claims.Seats != 1 {
		t.Errorf("the free seat's key claims %s/%s at %d seat(s), want Team, comped and one (D2184)", claims.Tier, claims.Kind, claims.Seats)
	}
	if claims.RenewalMode != licence.RenewalAuto {
		t.Errorf("the free seat's key renews %s, want %s (D2199)", claims.RenewalMode, licence.RenewalAuto)
	}
	if run := claims.PaidThrough.Sub(claims.IssuedAt); run != connectedTerm {
		t.Errorf("the free seat's key runs %s, want %s: it is a row whose key checks the server, and D2202 caps the key and not the entitlement", run, connectedTerm)
	}
	// The key is a month of the free seat and the record is the year of it: this
	// request names no term at all, and a record that carried the term named —
	// none — would leave the entitlement's own length to be read off the key it
	// minted, so a caller rebuilding one from it would end a year-long free seat a
	// month after it began (S3.5).
	if minted.Record.TermDays != 365 {
		t.Errorf("the free seat's record stands for %d day(s), want the row's own 365: the request named no term, and the record is the entitlement and not the key", minted.Record.TermDays)
	}
	if want := minted.Record.Issued.Add(365 * day); !entitlementEnd(minted.Record).Equal(want) {
		t.Errorf("the free seat's record ends its term at %s, want %s", entitlementEnd(minted.Record), want)
	}

	// The seat is the row's and not a caller's: an operations mint that asks the
	// free-seat row for a hundred seats has been given a hundred by nothing.
	if _, err := minter.MintV1(context.Background(), request(100)); err == nil {
		t.Error("the free-seat row minted a key for 100 seat(s), want a refusal: the free seat is one seat (D2184)")
	}
}

// TestDefaultPolicyPerTier holds each row to section 12.1's column of the same
// name, with the two decisions applied: grace is 30 everywhere (D2133) except
// the trial's none and the air-gapped key's 30 (D2138), a trial's
// `check_interval` is `P1D`, and an air-gapped key's is null — the one row whose
// key never contacts the server at all. The free seat is the row the table gains
// from D2184 and D2199, and the one row whose seats are its own.
func TestDefaultPolicyPerTier(t *testing.T) {
	daily := dailyCheck
	for _, tc := range []struct {
		name            string
		tier            string
		kind            string
		term            time.Duration
		seats           int
		graceDays       int
		renewalMode     string
		checkInterval   *string
		renewBeforeDays int
	}{
		{name: PolicyTeamMonthly, tier: licence.TierTeam, kind: licence.KindPaid, term: 30 * day, graceDays: 30, renewalMode: licence.RenewalAuto, checkInterval: &daily, renewBeforeDays: 7},
		{name: PolicyTeamAnnual, tier: licence.TierTeam, kind: licence.KindPaid, term: 365 * day, graceDays: 30, renewalMode: licence.RenewalAuto, checkInterval: &daily, renewBeforeDays: 7},
		{name: PolicyEnterprise, tier: licence.TierEnterprise, kind: licence.KindPaid, term: 365 * day, graceDays: 30, renewalMode: licence.RenewalAuto, checkInterval: &daily, renewBeforeDays: 7},
		{name: PolicyEnterpriseAirGapped, tier: licence.TierEnterprise, kind: licence.KindPaid, term: 365 * day, graceDays: 30, renewalMode: licence.RenewalNone, checkInterval: nil, renewBeforeDays: 0},
		{name: PolicyTrial, tier: licence.TierTeam, kind: licence.KindTrial, term: 14 * day, seats: 1, graceDays: 0, renewalMode: licence.RenewalNone, checkInterval: &daily, renewBeforeDays: 0},
		{name: PolicyFreeSeat, tier: licence.TierTeam, kind: licence.KindComped, term: 365 * day, seats: 1, graceDays: 30, renewalMode: licence.RenewalAuto, checkInterval: &daily, renewBeforeDays: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy, known := DefaultPolicy(tc.name)
			if !known {
				t.Fatalf("no default policy is named %q, and the minter is asked for it by name", tc.name)
			}
			if policy.Name != tc.name {
				t.Errorf("the row is named %q and was looked up as %q", policy.Name, tc.name)
			}
			if policy.Tier != tc.tier || policy.Kind != tc.kind || policy.Term != tc.term || policy.GraceDays != tc.graceDays {
				t.Errorf("the row is %+v, want the tier %s, kind %s, a term of %s and %d day(s) of grace (section 12.1, D2133, D2138)",
					policy, tc.tier, tc.kind, tc.term, tc.graceDays)
			}
			if policy.Seats != tc.seats {
				t.Errorf("the row names %d seat(s), want %d: a row that names none is one whose seats the caller names", policy.Seats, tc.seats)
			}
			if policy.RenewalMode != tc.renewalMode || policy.RenewBeforeDays != tc.renewBeforeDays {
				t.Errorf("the row renews %s %d day(s) early, want %s and %d", policy.RenewalMode, policy.RenewBeforeDays, tc.renewalMode, tc.renewBeforeDays)
			}
			switch {
			case tc.checkInterval == nil && policy.CheckInterval != nil:
				t.Errorf("the row checks every %q, want no check interval at all: this key never contacts the server (section 8)", *policy.CheckInterval)
			case tc.checkInterval != nil && policy.CheckInterval == nil:
				t.Errorf("the row carries no check interval, want %q", *tc.checkInterval)
			case tc.checkInterval != nil && *policy.CheckInterval != *tc.checkInterval:
				t.Errorf("the row checks every %q, want %q", *policy.CheckInterval, *tc.checkInterval)
			}
			if policy.CheckInterval != nil {
				if _, err := licence.ParseISODuration(*policy.CheckInterval); err != nil {
					t.Errorf("the row's check interval %q is not one the verifier reads, so every key minted from this row would be refused: %v", *policy.CheckInterval, err)
				}
			}
			if _, known := tierMaxTerm[policy.Tier]; !known {
				t.Errorf("the row's tier %q has no ceiling in the minter's own table, so no key of it can be minted", policy.Tier)
			}
			// The warnings of section 7.1 count from the start of grace, so a
			// row with no grace carries none: the windows could never open.
			if policy.GraceDays == 0 {
				if len(policy.Warnings) != 0 {
					t.Errorf("a row with no grace carries %d warning window(s), want none: no window of it ever opens", len(policy.Warnings))
				}
				return
			}
			if len(policy.Warnings) != 2 ||
				policy.Warnings[0].From != "grace_start" || len(policy.Warnings[0].Roles) != 1 || policy.Warnings[0].Roles[0] != "admin" ||
				policy.Warnings[1].From != "grace_start+7d" || len(policy.Warnings[1].Roles) != 3 {
				t.Errorf("the row's warnings are %+v, want section 7.1's two windows: admins from the first day of grace and everyone from the second week", policy.Warnings)
			}
		})
	}

	// Five columns and the free seat, one row per name: a name the table does
	// not hold is a refusal rather than a fallback to some other tier's policy.
	if len(defaultPolicies) != 6 {
		t.Errorf("the table holds %d row(s), want section 12.1's five columns and the free seat of D2184", len(defaultPolicies))
	}
	seen := map[string]bool{}
	for _, policy := range defaultPolicies {
		if seen[policy.Name] {
			t.Errorf("the table holds two rows named %q, and a mint request names a row by that one word", policy.Name)
		}
		seen[policy.Name] = true
	}
	if _, known := DefaultPolicy("team-plus"); known {
		t.Error("the table answered a policy named team-plus, want a refusal: a key is minted from a row and never from a fallback")
	}
	if _, known := DefaultPolicy(""); known {
		t.Error("the table answered a policy named by an empty string, want a refusal")
	}

	// What the lookup answers is a copy, down to what the row points at: a caller
	// that edits a row before a mint must not move the policy every later key of
	// that tier is minted under.
	policy, _ := DefaultPolicy(PolicyTeamMonthly)
	policy.Warnings[0].Roles[0] = "viewer"
	policy.Warnings[0].From = "never"
	*policy.CheckInterval = "P30D"
	again, _ := DefaultPolicy(PolicyTeamMonthly)
	if again.Warnings[0].Roles[0] != "admin" || again.Warnings[0].From != "grace_start" {
		t.Errorf("editing a looked-up row changed the table: the next mint of the tier would warn %+v", again.Warnings[0])
	}
	if *again.CheckInterval != dailyCheck {
		t.Errorf("editing a looked-up row's check interval changed the table: the next mint of the tier would check every %q", *again.CheckInterval)
	}
}
