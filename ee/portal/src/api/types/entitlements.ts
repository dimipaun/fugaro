// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Entitlements: what an organisation has been granted, and the operations mint
// that turns one into a key (portal plan P1.4; F20). The shapes are
// `ee/portal/schemas/entitlements.schema.json` and their samples are
// `ee/portal/schemas/fixtures/entitlements.*.json`, held to each other by
// `test/types.test.ts`.

/** Where an entitlement came from. */
export type EntitlementSource =
  | 'purchase'
  | 'free_seat'
  | 'trial'
  | 'open_source'
  | 'operations'
  | 'reseller'

/** The product row an entitlement is sold on. */
export type Tier = 'community' | 'team' | 'enterprise'

/** Whether the term is paid for, trialled, or comped. */
export type EntitlementKind = 'paid' | 'trial' | 'comped'

export type EntitlementStatus = 'active' | 'expired' | 'revoked' | 'superseded'

/**
 * One grant, bound to a workspace or still waiting for one. The key is minted
 * at binding and never here, so an unbound entitlement is a real state and not
 * an error.
 */
export interface Entitlement {
  id: string
  organisation_id: string
  workspace_id: string | null
  source: EntitlementSource
  row: string
  tier: Tier
  kind: EntitlementKind
  seats: number
  starts_at: string
  ends_at: string
  status: EntitlementStatus
  /** Issued once as a file, never revealed and never rebound (D2206). */
  air_gapped: boolean
  created_at: string
  bound_at: string | null
  /** When a rebind is allowed again, or null while nothing holds one back. */
  rebindable_at: string | null
}

/**
 * The operations team's mint. It carries no secret: the minter signs and the
 * secret never travels through this shape.
 */
export interface EntitlementMint {
  workspace_id: string
  row: string
  seats: number
  ends_at: string
  /** Null takes the row's own interval; the minter bounds it either way. */
  check_interval: string | null
}

/** Everything `entitlements.schema.json` describes. */
export type Entitlements = Entitlement | EntitlementMint
