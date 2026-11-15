// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The sales ledger and the raw Paddle events it is derived from (portal plan
// P1.4; F20). The shapes are `ee/portal/schemas/ledger.schema.json` and their
// samples are `ee/portal/schemas/fixtures/ledger.*.json`, held to each other
// by `test/types.test.ts`.
//
// The two are separate on purpose (spec §Operations, design §5.2): an event is
// stored exactly as Paddle sent it, before anything has decided what it means,
// and an entry is what the API derived from it. The ledger is read from the
// entries, and an event nobody could decode is a stored event with a state of
// its own rather than a missing row.

/** What became of one received event. */
export type EventState = 'stored' | 'undecodable' | 'processed' | 'parked' | 'dismissed'

/** One Paddle delivery, kept as received. */
export interface LedgerEvent {
  id: string
  received_at: string
  /** The signature header, kept beside the body it signed. */
  signature: string
  notification: string
  body: string
  state: EventState
  /** The entitlement it was resolved to, or null while it is parked. */
  entitlement_id: string | null
}

/** What a settled event meant: one line of the sales ledger. */
export type LedgerKind = 'purchase' | 'renewal' | 'refund' | 'chargeback' | 'adjustment'

/** Whether the sale was Fugaro's own or a reseller's. */
export type Channel = 'direct' | 'reseller'

/** One line of the ledger, derived from the stored events. */
export interface LedgerEntry {
  id: string
  at: string
  kind: LedgerKind
  organisation_id: string
  workspace_id: string | null
  entitlement_id: string | null
  /** Micros of the currency's smallest unit, so no float is in the path. */
  amount_micros: number
  currency: string
  seats: number
  channel: Channel
}

/** Everything `ledger.schema.json` describes. */
export type Ledger = LedgerEvent | LedgerEntry
