// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Entitlements (portal plan P3.1, P5.1, P6.2; D2196, D2199).
//
// Every grant is an entitlement: a purchase, the free seat, a trial, an
// operations grant, a reseller's sale and an open-source approval are one shape,
// because what the rest of the portal asks about a customer is what they hold
// and until when, never where it came from. Source is kept beside it because
// reporting and the reseller's own accrual do need to know (D2189, P5.2).
//
// An entitlement is created before it is bound (F25): it names the workspace it
// belongs to as soon as it exists — the purchase carries it (D2192) — and the
// key is minted at binding, so nothing here is a key.
//
// The term runs from payment and not from binding (D2197), which is why StartsAt
// is the purchase's instant: a customer who pays and sets their project up a
// month later holds the month they paid for, not a fresh one.

import "time"

// WireEntitlement is one entitlement as the API answers it.
type WireEntitlement struct {
	ID             string `json:"id"`
	OrganisationID string `json:"organisation_id"`
	// WorkspaceID is the portal workspace this entitlement is for, or null for
	// one the operations team granted outside any workspace. It is not the
	// Firebase project id: that is the key's, and it is known only once the
	// workspace is bound.
	WorkspaceID *string `json:"workspace_id"`
	// Source is where the grant came from: purchase, free_seat, trial,
	// open_source, operations or reseller.
	Source string `json:"source"`
	// Row is the policy row the key is minted under, by its name in policy.go,
	// and it is looked up and never trusted: the row's tier, term, grace and
	// check interval are the minter's and a caller may set a term only inside
	// them (F28, P1.8).
	Row string `json:"row"`
	// Tier is one of community, team and enterprise, and Kind is one of paid,
	// trial and comped: the two values a released CLI's key already carries
	// (ee/licence), so the portal can describe an entitlement in the words the
	// installation will read back.
	Tier string `json:"tier"`
	Kind string `json:"kind"`
	// Seats is how many people may act, and it is the entitlement's rather than
	// the key's: a seat change reissues the key with the same renewal secret
	// (D2200) and this number is what both sides of that reissue read.
	Seats int `json:"seats"`
	// StartsAt and EndsAt are the term, and StartsAt is the payment's instant
	// (D2197). A reveal mints a key ending on the term's end and never past it
	// (F30), which is why the term is here and not on the key alone.
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	// Status is one of active, expired, revoked and superseded. Superseded is
	// the free seat or trial a purchase replaced in its workspace (D2203,
	// D2204), and a revoked one is every key it ever minted (F29).
	Status string `json:"status"`
	// AirGapped says the key is issued as a file and never checks in (D2206):
	// it is issued once, it is never revealed, and no secret exists for it.
	AirGapped bool      `json:"air_gapped"`
	CreatedAt time.Time `json:"created_at"`
	// BoundAt is when a key was first minted for it, and null while it is
	// unbound. An entitlement somebody paid for and never bound is the state
	// the binding reminder is about (P7.1).
	BoundAt *time.Time `json:"bound_at"`
	// RebindableAt is the instant an owner or admin may rebind it, and null
	// while nothing has ever been bound. The first rebind after a binding is
	// not held by the 30-day clock, so an owner can undo a binding somebody
	// else made (D2198, P3.1).
	RebindableAt *time.Time `json:"rebindable_at"`
}

// WireEntitlementMint is operations' and a reseller's request to grant a term
// (P5.1, P6.2). It names a row, seats, an end and a check interval, and it names
// no policy of its own part from those: the tier, the grace and the renewal mode
// are the row's (D2133, F28), and the minter caps the end at the row's term
// (P1.8). A reseller's request is the same shape because it is the same act
// under a credit limit (D2189).
type WireEntitlementMint struct {
	WorkspaceID string `json:"workspace_id"`
	Row         string `json:"row"`
	Seats       int    `json:"seats"`
	// EndsAt is the end the caller asks for, capped at the row's own term: an
	// operations grant may run shorter than the row and never longer.
	EndsAt time.Time `json:"ends_at"`
	// CheckInterval is how often the installation asks whether its key is still
	// good, and it is 1 to 30 days. Null is the air-gapped row and nothing
	// else: a key that never checks is a key revocation cannot reach (F28).
	CheckInterval *string `json:"check_interval"`
}

func init() {
	registerWire("entitlements.entitlement", func() any { return new(WireEntitlement) })
	registerWire("entitlements.mint", func() any { return new(WireEntitlementMint) })
}
