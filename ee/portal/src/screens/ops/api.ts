// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// What the operations screens ask the API, and nothing about how one draws it
// (portal plan P5.3; P5.1 owns the other half of this contract).
//
// **The paths are the SPA's half of one contract, written once.** The plan pins
// no path for the operations API, so they are spelled here rather than in each
// section: P5.1's `api_ops.go` registers these through P1.0's registry, and a
// reconciliation between the two moves one file instead of seven. The verbs are
// the ones the requests are, and every body is a shape that already has three
// readers (`wire_ops.go`, `schemas/ops.schema.json`, `src/api/types/ops.ts`).
//
// **Every answer is a shape the portal already carries.** A read's answer is
// an array of the shape its area declares -- an organisation, a membership, an
// entitlement, key metadata, a ledger event or entry, an audit entry, a month
// of reports -- and never a shape invented for a screen (F20: the API's shapes
// are the schemas both sides test against). That is also why there is no route
// here for the queue of applications a person decides (`queue.go`'s): it has no
// wire shape yet, so the screens draw the parked-event queue, whose items are
// `ledger.event`s, and P5.1's `queue_decision` waits for the shape its items
// would be read as.
//
// **Nothing here carries a key.** Operations sees metadata (F5), and the one
// thing it may do with a secret is take a handover away: cutting one short is a
// route with an id in the path and no body, because the request has nothing to
// say beyond which key it is about.

import type { ApiClient } from '../../api/client'
import type { AuditEntry } from '../../api/types/audit'
import type { Entitlement, EntitlementMint } from '../../api/types/entitlements'
import type { KeyMetadata } from '../../api/types/keys'
import type { LedgerEntry, LedgerEvent } from '../../api/types/ledger'
import type { Membership, Organisation } from '../../api/types/orgs'
import type {
  OpsEntitlementAction,
  OpsEventResolution,
  OpsProjectTransfer,
} from '../../api/types/ops'
import type { ReportMonth } from '../../api/types/reports'
import type { Workspace } from '../../api/types/workspaces'

/**
 * The two lists as the wire spells them, which is `ops.schema.json`'s
 * `microsoft_directories` and not `OpsLists`' `microsoft_tenants`: D2207 was
 * amended on 2026-11-13 to pin the Microsoft (Entra ID) directory a verified
 * token names, and `src/api/types/ops.ts` still carries the name the amendment
 * replaced, so a request typed by it would send a field `wire_ops.go` refuses
 * (its decode is strict). The rename is that file's, and this is the name the
 * API answers and reads today.
 */
export interface OpsListsWire {
  email_domains: string[]
  microsoft_directories: string[]
}

/** Everything the operations screens ask of the API. */
export interface OpsApi {
  /** The signed-in person's own place in the operations organisation, which is
   * what says whether the screens may change anything (D2187). */
  membership(): Promise<Membership>
  lookup(term: string): Promise<Organisation[]>
  workspaces(organisationID: string): Promise<Workspace[]>
  entitlements(organisationID: string): Promise<Entitlement[]>
  members(organisationID: string): Promise<Membership[]>
  keys(workspaceID: string): Promise<KeyMetadata[]>
  /** The events the API could not place, which are a person's to decide (F16). */
  parkedEvents(): Promise<LedgerEvent[]>
  resolveEvent(resolution: OpsEventResolution): Promise<LedgerEvent>
  ledger(month: string): Promise<LedgerEntry[]>
  audit(): Promise<AuditEntry[]>
  report(month: string): Promise<ReportMonth>
  lists(): Promise<OpsListsWire>
  saveLists(lists: OpsListsWire): Promise<OpsListsWire>
  mint(mint: EntitlementMint): Promise<Entitlement>
  act(action: OpsEntitlementAction): Promise<Entitlement>
  transfer(transfer: OpsProjectTransfer): Promise<Workspace>
  cutHandover(keyID: string): Promise<void>
}

function query(params: Record<string, string>): string {
  return new URLSearchParams(params).toString()
}

export function opsApi(client: ApiClient): OpsApi {
  return {
    membership: () => client.request<Membership>('/ops/membership'),
    lookup: (term) =>
      client.request<Organisation[]>(`/ops/organisations?${query({ q: term })}`),
    workspaces: (organisationID) =>
      client.request<Workspace[]>(`/ops/workspaces?${query({ organisation_id: organisationID })}`),
    entitlements: (organisationID) =>
      client.request<Entitlement[]>(
        `/ops/entitlements?${query({ organisation_id: organisationID })}`,
      ),
    members: (organisationID) =>
      client.request<Membership[]>(`/ops/members?${query({ organisation_id: organisationID })}`),
    keys: (workspaceID) =>
      client.request<KeyMetadata[]>(`/ops/keys?${query({ workspace_id: workspaceID })}`),
    parkedEvents: () =>
      client.request<LedgerEvent[]>(`/ops/events?${query({ state: 'parked' })}`),
    resolveEvent: (resolution) =>
      client.request<LedgerEvent>('/ops/events/resolutions', { method: 'POST', body: resolution }),
    ledger: (month) => client.request<LedgerEntry[]>(`/ops/ledger?${query({ month })}`),
    audit: () => client.request<AuditEntry[]>('/ops/audit'),
    report: (month) => client.request<ReportMonth>(`/ops/reports?${query({ month })}`),
    lists: () => client.request<OpsListsWire>('/ops/lists'),
    saveLists: (lists) => client.request<OpsListsWire>('/ops/lists', { method: 'PUT', body: lists }),
    mint: (mint) => client.request<Entitlement>('/ops/mints', { method: 'POST', body: mint }),
    act: (action) =>
      client.request<Entitlement>('/ops/entitlement-actions', { method: 'POST', body: action }),
    transfer: (transfer) =>
      client.request<Workspace>('/ops/project-transfers', { method: 'POST', body: transfer }),
    cutHandover: (keyID) =>
      client.request<void>(`/ops/keys/${encodeURIComponent(keyID)}/handover-cut`, {
        method: 'POST',
      }),
  }
}
