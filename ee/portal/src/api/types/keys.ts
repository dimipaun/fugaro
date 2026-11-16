// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Keys: the metadata anybody may see, the reveal bundle, and the air-gapped
// licence file (portal plan P1.4; F20). The shapes are
// `ee/portal/schemas/keys.schema.json` and their samples are
// `ee/portal/schemas/fixtures/keys.*.json`, held to each other by
// `test/types.test.ts`.
//
// **`KeyReveal` is the only shape in the portal that carries a secret.** The
// key and its renewal secret arrive once, are shown once and are never stored
// by the app: everything else -- the screens, the lists, the operations views
// -- reads `KeyMetadata`, which has no room for one. That is why the two are
// separate types rather than one with optional fields, and why a screen that
// reads metadata cannot accidentally draw a secret it was never sent.

import type { EntitlementKind, Tier } from './entitlements'

/** What is known about a key without holding it. */
export interface KeyMetadata {
  key_id: string
  entitlement_id: string
  workspace_id: string
  tier: Tier
  kind: EntitlementKind
  seats: number
  issued_at: string
  paid_through: string
  /** When the installation last refreshed, or null while it never has. */
  last_seen: string | null
}

/** A rotation's answer: the newest key, shown once and never fetched again. */
export interface KeyReveal {
  key_id: string
  key: string
  renewal_secret: string
  paid_through: string
}

/** The air-gapped file, which holds no secret and is downloaded instead. */
export interface KeyLicence {
  key_id: string
  licence: string
  paid_through: string
}

/** Everything `keys.schema.json` describes. */
export type Keys = KeyMetadata | KeyReveal | KeyLicence
