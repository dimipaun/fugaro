// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The organisation settings screen (portal plan P2.3; spec §Tenancy).
//
// **The organisation is read out of the person's own tenancy.** `GET /orgs` is
// what the API answers a member with — the organisations they belong to, and
// nothing else (P2.1's `WireOrgList`) — and one of them is all this screen
// needs: the organisation it draws. Reading it once as a list and picking the
// one the URL names is also what answers a URL naming an organisation the
// person is not in, which no single-organisation read distinguishes from an
// organisation that is not there at all.
//
// **The screen draws and writes nothing.** The type is set once and never moved
// (a customer organisation stays one), the terms are the sign-up's record and
// there is none for the operations organisation — which is a record that does
// not exist rather than an empty one — and the name is the one field a person
// would change: renaming is a later task's (`ee/portal/schemas/orgs.schema.json`,
// and no route of P2.1's answers it), so there is nothing here to write it with.
// A form that PATCHed a path nothing mounts would be a button whose only
// outcome is a 404.
//
// **The URL names the organisation, and a URL that names none or names one the
// person is not in is answered rather than drawn empty.** Both are states a
// person reaches by editing an address or following a stale link, and a page
// that drew nothing would look like a portal with an organisation that has no
// settings. The value is text a person can edit, so it is read through the
// switcher's reader (`organisationIdIn`).

import { useEffect, useMemo, useState } from 'react'
import { Link, Navigate, useSearchParams } from 'react-router-dom'
import { ApiError, createApiClient } from '../../api/client'
import type { OrganisationType } from '../../api/types/orgs'
import { useSession } from '../../auth/session'
import { SIGN_IN_PATH } from '../../routes'
import type { Tenancy } from '../orgs/orgs'
import { organisationIdIn } from '../orgs/switcher'

const TYPE_LABELS: Record<OrganisationType, string> = {
  operations: 'Operations',
  customer: 'Customer',
  reseller: 'Reseller',
}

const GENERIC_FAILURE = 'The portal could not read this organisation. Try again.'

type Load =
  | { status: 'loading' }
  | { status: 'failed'; message: string }
  | { status: 'ready'; tenancy: Tenancy }

/** The sentence a person is shown for a failure. */
function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

/**
 * The day an instant falls on. The API writes every instant in UTC, so its own
 * first ten characters are that day and no calendar has to be asked.
 */
function day(instant: string): string {
  return instant.slice(0, 'YYYY-MM-DD'.length)
}

export function SettingsScreen() {
  const { state: session, auth } = useSession()
  const api = useMemo(() => createApiClient({ token: () => auth.idToken() }), [auth])
  const [params] = useSearchParams()
  const organisationId = organisationIdIn(params.get('org'))
  const [load, setLoad] = useState<Load>({ status: 'loading' })

  useEffect(() => {
    if (session.status !== 'signed-in' || organisationId === null) {
      return
    }
    let live = true
    void (async () => {
      try {
        const tenancy = await api.request<Tenancy>('/orgs')
        if (live) {
          setLoad({ status: 'ready', tenancy })
        }
      } catch (error) {
        if (live) {
          setLoad({ status: 'failed', message: sentence(error) })
        }
      }
    })()
    return () => {
      live = false
    }
  }, [api, session.status, organisationId])

  if (session.status === 'signed-out') {
    return <Navigate to={SIGN_IN_PATH} replace />
  }

  if (organisationId === null) {
    return <Navigate to="/orgs" replace />
  }

  if (session.status === 'loading' || load.status === 'loading') {
    return (
      <main>
        <p role="status">Loading…</p>
      </main>
    )
  }

  const selected: string = organisationId
  const tenancy = load.status === 'ready' ? load.tenancy : null
  const organisation = tenancy?.organisations.find((one) => one.id === selected)

  if (load.status === 'failed') {
    return (
      <main>
        <h1>Settings</h1>
        <p role="alert">{load.message}</p>
        <p>
          <Link to="/orgs">Your organisations</Link>
        </p>
      </main>
    )
  }

  if (organisation === undefined) {
    return (
      <main>
        <h1>Settings</h1>
        <p role="alert">That organisation is not one you belong to.</p>
        <p>
          <Link to="/orgs">Your organisations</Link>
        </p>
      </main>
    )
  }

  return (
    <main>
      <h1>{organisation.name}</h1>
      <p>
        <Link to="/orgs">Your organisations</Link>
      </p>
      <dl>
        <dt>Type</dt>
        <dd>{TYPE_LABELS[organisation.type]}</dd>
        <dt>Created</dt>
        <dd>
          <time dateTime={organisation.created_at}>{day(organisation.created_at)}</time>
        </dd>
        <dt>Terms</dt>
        <dd>
          {organisation.terms === null ? (
            'No terms were accepted for this organisation.'
          ) : (
            <>
              Version {organisation.terms.version}, accepted on{' '}
              <time dateTime={organisation.terms.accepted_at}>
                {day(organisation.terms.accepted_at)}
              </time>
            </>
          )}
        </dd>
      </dl>
    </main>
  )
}
