// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The operations team's requests: what it does to an entitlement, a project
// id's transfer, the two lists it sets, and the two queues it decides (portal
// plan P1.4; F20). The shapes are `ee/portal/schemas/ops.schema.json` and their
// samples are `ee/portal/schemas/fixtures/ops.*.json`, held to each other by
// `test/types.test.ts`.
//
// Every one of them is a **request**, not a record: the operations organisation
// acts across tenants and the audit log is where the record of it lives, which
// is why nothing here carries an actor or an instant.

/** What operations does to an entitlement. */
export type EntitlementActionKind = 'extend' | 'seats' | 'comp' | 'revoke'

/** One operations action on one entitlement. */
export interface OpsEntitlementAction {
  entitlement_id: string
  action: EntitlementActionKind
  /** The new seat count, for `seats`. */
  seats: number | null
  /** The new end, for `extend` and `comp`. */
  ends_at: string | null
  reason: string | null
}

/** Moving a project id to another workspace, which only operations may do (D2204). */
export interface OpsProjectTransfer {
  project_id: string
  workspace_id: string
  reason: string | null
}

/** The two lists operations sets: the sign-in domains, and the pinned tenants. */
export interface OpsLists {
  email_domains: string[]
  microsoft_tenants: string[]
}

/** Deciding one open-source request in the queue. */
export interface OpsQueueDecision {
  id: string
  decision: 'approved' | 'refused'
  note: string | null
}

/** Resolving one parked Paddle event: attached to an entitlement, or dismissed. */
export interface OpsEventResolution {
  id: string
  decision: 'attach' | 'dismiss'
  entitlement_id: string | null
}

/** Everything `ops.schema.json` describes. */
export type Ops =
  | OpsEntitlementAction
  | OpsProjectTransfer
  | OpsLists
  | OpsQueueDecision
  | OpsEventResolution
