// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// What the reseller screens ask the API for (portal plan P6.4; D2188).
//
// **Two reads and no writes.** A reseller's screens are a window on what it
// sold and what it owes (spec §Resellers): the customers it created, and the
// months it accrues against its credit limit. There is deliberately no mint
// here -- the plan gives this task the two reads and names no action -- and
// there is deliberately no key: `resellers.schema.json`'s customer carries the
// link, and a reseller is never a member of its customers' organisations
// (P6.1), so the API has nothing of theirs to hand it (F5).
//
// **The port is the seam, and the screens own it.** `ResellerApi` is what a
// screen is handed, so a suite can hand it a fake and nothing in this directory
// reaches a network (the plan's hermetic-test rule). `resellerApi` is the one
// implementation over `src/api/client.ts`, and this file is the one place the
// SPA spells the two paths: the routes are the area's own (P6.1's reseller
// routes, P6.3's statements) and are registered there, while the shapes either
// side of them are the ones `src/api/types/` already carries.

import type { ApiClient } from '../../api/client'
import type { ResellerCustomer } from '../../api/types/resellers'
import type { Statement } from '../../api/types/statements'

/** The customers a reseller sold to, and its own monthly statements. */
export interface ResellerApi {
  customers(): Promise<ResellerCustomer[]>
  statements(): Promise<Statement[]>
}

/** The customers this reseller created. It answers its own, from the token. */
export const CUSTOMERS_PATH = '/resellers/customers'

/** This reseller's statements, newest first. */
export const STATEMENTS_PATH = '/statements'

export function resellerApi(client: ApiClient): ResellerApi {
  return {
    customers: () => client.request<ResellerCustomer[]>(CUSTOMERS_PATH),
    statements: () => client.request<Statement[]>(STATEMENTS_PATH),
  }
}
