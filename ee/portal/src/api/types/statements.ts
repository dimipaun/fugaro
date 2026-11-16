// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// A reseller's monthly statement and a recorded payment against it (portal plan
// P1.4; F20). The shapes are `ee/portal/schemas/statements.schema.json` and
// their samples are `ee/portal/schemas/fixtures/statements.*.json`, held to
// each other by `test/types.test.ts`.

/** One customer's share of a statement, accrued over the month. */
export interface StatementLine {
  organisation_id: string
  amount_micros: number
}

/** One month's statement: what the reseller accrued and what it has paid. */
export interface Statement {
  id: string
  reseller_organisation_id: string
  /** The month in UTC, as `YYYY-MM`. */
  month: string
  accrued_micros: number
  paid_micros: number
  state: 'open' | 'paid'
  lines: StatementLine[]
  issued_at: string
  paid_at: string | null
}

/** A payment operations recorded against a statement. */
export interface StatementPayment {
  amount_micros: number
  paid_at: string
  reference: string
}

/** Everything `statements.schema.json` describes. */
export type Statements = Statement | StatementPayment
