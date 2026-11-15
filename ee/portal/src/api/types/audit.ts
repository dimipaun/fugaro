// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The audit log (portal plan P1.4; F20). The shape is
// `ee/portal/schemas/audit.schema.json` and its sample is
// `ee/portal/schemas/fixtures/audit.entry.json`, held to each other by
// `test/types.test.ts`.
//
// **Every operations action is audited, reads included** (plan, global
// constraints), which is why there is a `read` flag rather than a second
// table: an operations member looking a customer up is an event a customer is
// entitled to see accounted for, and the flag says which kind it was without a
// reader having to know the action vocabulary.

/** One audited action. */
export interface AuditEntry {
  id: string
  at: string
  actor_uid: string
  /** Personal data, and the reason this shape is operations-only. */
  actor_email: string
  /** Null for an action that spans organisations. */
  organisation_id: string | null
  action: string
  /** The workspace, entitlement, key or member the action was about. */
  target_id: string | null
  /** True for a lookup, false for a change: reads are audited too. */
  read: boolean
  detail: string | null
}

/** Everything `audit.schema.json` describes. */
export type Audit = AuditEntry
