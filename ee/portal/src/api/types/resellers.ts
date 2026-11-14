// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Resellers, their customers and the relink operations performs (portal plan
// P1.4; F20). The shapes are `ee/portal/schemas/resellers.schema.json` and
// their samples are `ee/portal/schemas/fixtures/resellers.*.json`, held to each
// other by `test/types.test.ts`.
//
// A reseller is **not a member of its customers' organisations** (P6.1), so
// nothing here carries a customer's members, its keys or a secret: the shapes
// are the link, the limit and the rates, which is everything a reseller is
// entitled to know about a customer it sold to.

/** One wholesale rate, per product row. */
export interface ResellerRate {
  row: string
  daily_rate_micros: number
}

/** One reseller organisation, with the terms operations set for it. */
export interface Reseller {
  organisation_id: string
  /** Minting beyond this is refused; there is no prepaid credit (D2189). */
  credit_limit_micros: number
  rates: ResellerRate[]
  created_at: string
}

/** One customer a reseller sold to. */
export interface ResellerCustomer {
  organisation_id: string
  name: string
  reseller_organisation_id: string
  linked_at: string
}

/** Relinking a customer to another reseller, or to none. */
export interface ResellerRelink {
  customer_organisation_id: string
  reseller_organisation_id: string
}

/** Everything `resellers.schema.json` describes. */
export type Resellers = Reseller | ResellerCustomer | ResellerRelink
