// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The organisation settings screen (portal plan P2.3; spec §Tenancy).
//
// **The organisation is read out of the person's own tenancy.** `GET /orgs` is
// what the API answers a member with -- the organisations they belong to and
// their membership in each -- and both things this screen needs are in it: the
// organisation to draw and the role that decides whether this person may
// rename it. Reading it twice, once as an organisation and once as a
// membership list, would be two requests for one document. The type is spelled
// here rather than in `src/api/types/orgs.ts` for the reason the organisations
// screen gives: the objects are the API's area file's, and the screen's
// envelope is the screen's.
//
// **Only what the API can answer is drawn, and nothing is a decision this file
// makes.** The type is set once and never moved (a customer organisation stays
// one), the terms are the sign-up's record and there is none for the
// operations organisation -- which is a record that does not exist rather than
// an empty one -- and the name is the one field an owner or an admin may
// change (D2195). A member is shown the same facts without the form, and the
// API would refuse the write either way.
//
// **The URL names the organisation, and a URL that names none or names one the
// person is not in is answered rather than drawn empty.** Both are states a
// person reaches by editing an address or following a stale link, and a page
// that drew nothing would look like a portal with an organisation that has no
// settings.

import { useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { Link, Navigate, useSearchParams } from 'react-router-dom'
import { ApiError, createApiClient } from '../../api/client'
import type { Membership, Organisation, OrganisationType } from '../../api/types/orgs'
import { useSession } from '../../auth/session'
import { SIGN_IN_PATH } from '../../routes'
import type { Tenancy } from '../orgs/orgs'

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
  const organisationId = params.get('org')
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  useEffect(() => {
    if (session.status !== 'signed-in' || organisationId === null) {
      return
    }
    let live = true
    void (async () => {
      try {
        const tenancy = await api.request<Tenancy>('/orgs')
        const found = tenancy.organisations.find((one) => one.id === organisationId)
        if (live) {
          setLoad({ status: 'ready', tenancy })
          // The field starts on the name the API answered: the person is
          // editing it, not typing it from nothing.
          setName(found?.name ?? '')
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

  async function rename(id: string, event: FormEvent): Promise<void> {
    event.preventDefault()
    setBusy(true)
    setFailure(null)
    try {
      const saved = await api.request<Organisation>(`/orgs/${id}`, {
        method: 'PATCH',
        body: { name: name.trim() },
      })
      setLoad((current) =>
        current.status === 'ready'
          ? {
              status: 'ready',
              tenancy: {
                ...current.tenancy,
                organisations: current.tenancy.organisations.map((organisation) =>
                  organisation.id === saved.id ? saved : organisation,
                ),
              },
            }
          : current,
      )
    } catch (error) {
      setFailure(sentence(error))
    } finally {
      setBusy(false)
    }
  }

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
  const mine: Membership | undefined = tenancy?.memberships.find(
    (membership) => membership.organisation_id === selected && membership.uid === session.user.uid,
  )
  const decides = mine !== undefined && (mine.role === 'owner' || mine.role === 'admin')

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

      {decides ? (
        <section>
          <h2>Name</h2>
          <form onSubmit={(event) => void rename(selected, event)}>
            <label htmlFor="organisation-name">Organisation name</label>
            <input
              id="organisation-name"
              name="name"
              required
              maxLength={64}
              value={name}
              onChange={(event) => {
                setName(event.target.value)
              }}
            />
            <button type="submit" disabled={busy}>
              Save name
            </button>
          </form>
        </section>
      ) : null}

      {failure === null ? null : <p role="alert">{failure}</p>}
    </main>
  )
}
