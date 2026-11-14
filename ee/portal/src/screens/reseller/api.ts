// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// What the reseller screens ask the API for (portal plan P6.4; spec §Resellers).
//
// **Three reads and no writes.** A reseller's screens are a window on what it
// sold and what it owes: the customers it created, each customer's keys, and
// the months it accrues against its credit limit. There is deliberately no
// mint and no reveal here -- the plan gives this task the reads and names no
// action.
//
// **What a reseller may see of a key is its metadata, and that is a shape and
// not a habit.** F5 rules that a reseller and operations read key metadata
// only -- key id, seats, tier, renewal date and last refresh -- because
// revealing a key rotates it and belongs to the customer organisation's owners
// and admins. The metadata shape is that reading written down: `KeyMetadata` in
// `src/api/types/keys.ts` has no field a key or a renewal secret could arrive
// in, so a screen over it cannot draw one it was never sent.
//
// **The port is the seam, and the screens own it.** `ResellerApi` is what a
// screen is handed, so a suite can hand it a fake and nothing in this directory
// reaches a network (the plan's hermetic-test rule). `resellerApi` is the one
// implementation over `src/api/client.ts`, and this file is the one place the
// SPA spells the paths: the routes are the area's own -- P6.1's reseller routes
// and P6.3's statements -- and are registered there, while the shapes either
// side of them are the ones `src/api/types/` already carries.

import type { ApiClient } from '../../api/client'
import type { KeyMetadata } from '../../api/types/keys'
import type { ResellerCustomer } from '../../api/types/resellers'
import type { Statement } from '../../api/types/statements'

/** What a reseller reads: its customers, their keys, and its own statements. */
export interface ResellerApi {
  customers(): Promise<ResellerCustomer[]>
  /** One customer's keys, as metadata. It answers a customer this reseller sold to. */
  keys(organisationId: string): Promise<KeyMetadata[]>
  statements(): Promise<Statement[]>
}

/** The customers this reseller created. It answers its own, from the token. */
export const CUSTOMERS_PATH = '/resellers/customers'

/** One sold-to customer's keys. A reseller reads its own customers' and no others. */
export function customerKeysPath(organisationId: string): string {
  return `${CUSTOMERS_PATH}/${encodeURIComponent(organisationId)}/keys`
}

/** This reseller's statements, newest first. */
export const STATEMENTS_PATH = '/statements'

export function resellerApi(client: ApiClient): ResellerApi {
  return {
    customers: () => client.request<ResellerCustomer[]>(CUSTOMERS_PATH),
    keys: (organisationId) => client.request<KeyMetadata[]>(customerKeysPath(organisationId)),
    statements: () => client.request<Statement[]>(STATEMENTS_PATH),
  }
}
