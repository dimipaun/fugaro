// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The billing mirror and a seat purchase (portal plan P1.4; F20). The shapes
// are `ee/portal/schemas/billing.schema.json` and their samples are
// `ee/portal/schemas/fixtures/billing.*.json`, held to each other by
// `test/types.test.ts`.
//
// The subscription below is a **mirror**: Paddle is where the subscription
// lives, and this is what the portal last read from its events, so a screen
// that shows a renewal date is showing a fact Paddle sent rather than one the
// browser worked out.

/** Where a subscription is, as Paddle last reported it. */
export type SubscriptionState = 'active' | 'trialing' | 'past_due' | 'paused' | 'canceled'

/** One Paddle subscription, one per workspace (D2192). */
export interface Subscription {
  id: string
  organisation_id: string
  workspace_id: string
  state: SubscriptionState
  seats: number
  price_id: string
  starts_at: string
  renews_at: string | null
  ends_at: string | null
  cancelled_at: string | null
}

/** Buying more seats: the quantity the subscription is moved to. */
export interface SubscriptionSeats {
  seats: number
}

/** Everything `billing.schema.json` describes. */
export type Billing = Subscription | SubscriptionSeats
