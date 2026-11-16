// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Monthly sales reports, split by channel (portal plan P1.4; F20). The shapes
// are `ee/portal/schemas/reports.schema.json` and their samples are
// `ee/portal/schemas/fixtures/reports.*.json`, held to each other by
// `test/types.test.ts`.

import type { Channel } from './ledger'

/** One customer's line in a month. */
export interface ReportCustomer {
  organisation_id: string
  name: string
  channel: Channel
  amount_micros: number
}

/** One month: the two channel totals and the customers behind them. */
export interface ReportMonth {
  /** The month in UTC, as `YYYY-MM`. */
  month: string
  direct_micros: number
  reseller_micros: number
  customers: ReportCustomer[]
}

/** Everything `reports.schema.json` describes. */
export type Reports = ReportMonth | ReportCustomer
