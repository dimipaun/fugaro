// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Billing (portal plan P4.3; spec §Customer experience).
//
// The portal mirrors what Paddle says and never decides it: invoices, payment
// methods and cancellation live in Paddle's own customer portal, and what is
// here is the state a customer's screens read — the subscription, its seats and
// when it next renews. A seat change is asked for here and applied there, and
// the event that comes back is what reissues the key (P3.6), which is why the
// seats on this shape are Paddle's quantity and not a number the portal keeps
// for itself.

import "time"

// WireSubscription is one workspace's Paddle subscription.
type WireSubscription struct {
	// ID is Paddle's subscription id and not a portal id: it is what a refund,
	// a proration or a support conversation is looked up by, and the mirror
	// exists so that a lookup does not have to reach Paddle.
	ID             string `json:"id"`
	OrganisationID string `json:"organisation_id"`
	WorkspaceID    string `json:"workspace_id"`
	// State is Paddle's own: active, trialing, past_due, paused or canceled.
	// It is spelled as Paddle spells it, because the portal's screens and the
	// status Paddle's own customer portal shows must not disagree.
	State string `json:"state"`
	// Seats is the subscription's quantity, and a seat change updates it here
	// and at Paddle rather than replacing the subscription: one workspace has
	// one (D2192).
	Seats   int    `json:"seats"`
	PriceID string `json:"price_id"`
	// StartsAt, RenewsAt and EndsAt are the term as Paddle states it. RenewsAt
	// is null for a subscription that will not renew, and EndsAt is null while
	// nothing has ended it: a term that ran out is stated by those two dates,
	// and `canceled` is the one state a cancellation was recorded for.
	StartsAt    time.Time  `json:"starts_at"`
	RenewsAt    *time.Time `json:"renews_at"`
	EndsAt      *time.Time `json:"ends_at"`
	CancelledAt *time.Time `json:"cancelled_at"`
}

// WireSeatChange is an owner's or admin's request to buy more seats.
type WireSeatChange struct {
	// Seats is the whole quantity and not an increment: asking for the number
	// a customer believes they are buying is what makes a lost reply safe to
	// send again.
	Seats int `json:"seats"`
}

// WireCustomerPortal is what a billing screen is handed when it asks for
// Paddle's own customer portal: the session's URL, which is where invoices,
// payment methods and cancellation live.
//
// It is registered like the other shapes of this area, so all three readers of
// it are held to each other (wire.go): this type, the `portal` def of
// `ee/portal/schemas/billing.schema.json` and the fixture
// `ee/portal/schemas/fixtures/billing.portal.json`, with the SPA's own
// `CustomerPortal` (`ee/portal/src/api/types/billing.ts`) read against that
// same fixture by `ee/portal/test/types.test.ts`. The field is spelled the way
// the screen reads it.
type WireCustomerPortal struct {
	URL string `json:"url"`
}

func init() {
	registerWire("billing.subscription", func() any { return new(WireSubscription) })
	registerWire("billing.seats", func() any { return new(WireSeatChange) })
	registerWire("billing.portal", func() any { return new(WireCustomerPortal) })
}
