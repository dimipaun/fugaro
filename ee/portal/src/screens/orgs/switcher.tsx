// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The organisation switcher (portal plan P2.3; spec §Tenancy, D2207).
//
// **A person belongs to many organisations and works in one at a time, and the
// choice is the URL.** The switcher's links carry the organisation in the query
// (`/members?org=…`), so the organisation a screen is about is one the person
// can see, bookmark and send, and no shared state has to be introduced for it:
// the plan's rule is that a screen task adds its own directory and edits no
// other file, and a context holding the current organisation would be exactly
// such a file.
//
// **The one refusal the switcher makes itself is the work-account rule, and it
// is D2207's.** An operations or reseller organisation is reached with Google
// (and, for a reseller, Microsoft) only; the API refuses an emailed-link or
// password identity for a membership of one on every request (P1.3, P2.1), so
// the switcher opens `src/screens/sso/dialog.tsx` instead of following the link
// and the person reads why. A reseller's Microsoft identity is let through
// here: whether its directory is one operations pinned (P5.1) is a list this
// screen does not hold, and the API is where the answer is.
//
// **The organisation the API recorded as last used is the one marked.** That is
// the whole of "they land in the last one used" (spec §Tenancy, P2.1's
// `TestLastUsedOrg`): the record is the API's, and the screen only draws it.

import { useState } from 'react'
import { Link } from 'react-router-dom'
import type { Membership, Organisation } from '../../api/types/orgs'
import { useSession } from '../../auth/session'
import { PROVIDER_OF, requiresSSO, signInMethods } from '../../auth/sso'
import type { SignInProvider } from '../../auth/sso'
import { SsoDialog } from '../sso/dialog'

/** One organisation with the membership that puts this person in it. */
export interface OrganisationEntry {
  organisation: Organisation
  membership: Membership
}

/** Where an organisation's members are. Spelled once: the switcher's links and
 * the screen that creates an organisation both land on it. */
export function membersPath(organisationId: string): string {
  return `/members?org=${encodeURIComponent(organisationId)}`
}

/** Where an organisation's settings are. */
export function settingsPath(organisationId: string): string {
  return `/settings?org=${encodeURIComponent(organisationId)}`
}

/**
 * True when this organisation's own rule turns this identity away, as far as a
 * screen without operations' pinned directory list can tell (D2207): operations
 * is Google only, and a reseller is Google or a Microsoft directory operations
 * has pinned.
 */
function refusesIdentity(type: Organisation['type'], provider: SignInProvider): boolean {
  if (!requiresSSO(type)) {
    return false
  }
  if (type === 'reseller' && provider === 'microsoft.com') {
    return false
  }
  return !signInMethods(type).some((method) => PROVIDER_OF[method] === provider)
}

export function OrganisationSwitcher({
  entries,
  selectedId,
}: {
  entries: readonly OrganisationEntry[]
  selectedId: string | null
}) {
  const { state } = useSession()
  const [refused, setRefused] = useState<Organisation | null>(null)

  return (
    <>
      <nav aria-label="Your organisations">
        <ul>
          {entries.map(({ organisation }) => (
            <li key={organisation.id}>
              <Link
                to={membersPath(organisation.id)}
                aria-current={organisation.id === selectedId ? 'true' : undefined}
                onClick={(event) => {
                  if (state.status !== 'signed-in') {
                    return
                  }
                  if (refusesIdentity(organisation.type, state.user.provider)) {
                    event.preventDefault()
                    setRefused(organisation)
                  }
                }}
              >
                {organisation.name}
              </Link>{' '}
              <Link
                to={settingsPath(organisation.id)}
                aria-label={`Settings for ${organisation.name}`}
              >
                Settings
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      {refused === null ? null : (
        <SsoDialog
          organisation={{ type: refused.type }}
          onDismiss={() => {
            setRefused(null)
          }}
        />
      )}
    </>
  )
}
