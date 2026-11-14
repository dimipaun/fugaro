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
// **Choosing an organisation is the one thing the switcher writes** (P2.1):
// `POST /orgs/{id}/select` records the person's own last use of it, and it is
// what the list is ordered by, so the next time they open the portal they land
// on it (spec §Tenancy, P2.1's `TestLastUsedOrg`). The write is sent with the
// navigation rather than before it: the API answers nothing, the screen the
// person is going to is the one that was asked for either way, and a write that
// failed is not worth interrupting a navigation over — what it costs is that
// this organisation is not the one they land on next time.
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
// **The organisation the API put first is the one marked.** The list it answers
// is already ordered by last use, the most recent first and the never-used last
// (P2.1's `organisationsOf`), so "they land in the last one used" is the API's
// ordering and the screen only draws its head as the current one.

import { useMemo, useState } from 'react'
import type { MouseEvent } from 'react'
import { Link } from 'react-router-dom'
import { createApiClient } from '../../api/client'
import type { Organisation } from '../../api/types/orgs'
import { useSession } from '../../auth/session'
import { PROVIDER_OF, requiresSSO, signInMethods } from '../../auth/sso'
import type { SignInProvider } from '../../auth/sso'
import { SsoDialog } from '../sso/dialog'

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
 * The organisation a screen's URL names, or null when the value cannot be one.
 *
 * `?org=` is text a person can edit, and a screen turns it into a segment of an
 * API path (`/orgs/<id>/members`). The client refuses a path that leaves the
 * origin but not a `..` segment inside one (`apiPath`), so `/orgs/../../x/members`
 * would be fetched as `/x/members`, with the ID token on it. `.` and `..` are
 * refused here -- they are the one value encoding does not make inert -- and a
 * screen encodes every other value where it builds the path. A query that names
 * nobody is not an organisation either.
 */
export function organisationIdIn(param: string | null): string | null {
  if (param === null || param === '' || param === '.' || param === '..') {
    return null
  }
  return param
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
  organisations,
  selectedId,
}: {
  organisations: readonly Organisation[]
  selectedId: string | null
}) {
  const { state, auth } = useSession()
  const api = useMemo(() => createApiClient({ token: () => auth.idToken() }), [auth])
  const [refused, setRefused] = useState<Organisation | null>(null)

  function choose(organisation: Organisation): void {
    void api
      .request<void>(`/orgs/${encodeURIComponent(organisation.id)}/select`, { method: 'POST' })
      .catch(() => {})
  }

  /** Following either of an organisation's links is choosing it. */
  function follow(organisation: Organisation, event: MouseEvent<HTMLAnchorElement>): void {
    if (state.status !== 'signed-in') {
      return
    }
    if (refusesIdentity(organisation.type, state.user.provider)) {
      event.preventDefault()
      setRefused(organisation)
      return
    }
    choose(organisation)
  }

  return (
    <>
      <nav aria-label="Your organisations">
        <ul>
          {organisations.map((organisation) => (
            <li key={organisation.id}>
              <Link
                to={membersPath(organisation.id)}
                aria-current={organisation.id === selectedId ? 'true' : undefined}
                onClick={(event) => {
                  follow(organisation, event)
                }}
              >
                {organisation.name}
              </Link>{' '}
              <Link
                to={settingsPath(organisation.id)}
                aria-label={`Settings for ${organisation.name}`}
                onClick={(event) => {
                  follow(organisation, event)
                }}
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
