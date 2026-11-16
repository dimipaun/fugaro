// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Paddle checkout (portal plan P1.4; F20). The shapes are
// `ee/portal/schemas/checkout.schema.json` and their samples are
// `ee/portal/schemas/fixtures/checkout.*.json`, held to each other by
// `test/types.test.ts`.
//
// **The transaction is created server-side and the overlay is handed an id and
// nothing else** (spec §Customer experience, design §5.2): prices, custom data
// and the customer's identity are the API's, and `CheckoutSession` carries the
// transaction id the overlay opens. A shape that carried an amount would be a
// price the browser chose.

/** Buying a licence for one workspace, with the terms' version accepted. */
export interface CheckoutCreate {
  workspace_id: string
  terms_version: string
}

/** What the overlay is opened with. */
export interface CheckoutSession {
  transaction_id: string
}

/** Everything `checkout.schema.json` describes. */
export type Checkout = CheckoutCreate | CheckoutSession
