// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Invitations into an organisation (portal plan P1.4; F20). The shapes are
// `ee/portal/schemas/invites.schema.json` and their samples are
// `ee/portal/schemas/fixtures/invites.*.json`, held to each other by
// `test/types.test.ts`.

import type { Role } from './orgs'

/** Where an invitation has got to. */
export type InviteState = 'pending' | 'accepted' | 'revoked' | 'expired'

/** One invitation. Before acceptance it knows an address and not a person. */
export interface Invite {
  id: string
  organisation_id: string
  /** The address the invitation is bound to, and the only one that accepts it. */
  email: string
  role: Role
  state: InviteState
  /** The uid of the member who invited them. */
  invited_by: string
  created_at: string
  expires_at: string
  accepted_at: string | null
}

/** An invitation being sent. The inviter and the organisation come from the session. */
export interface InviteCreate {
  email: string
  role: Role
}

/** Everything `invites.schema.json` describes. */
export type Invites = Invite | InviteCreate
