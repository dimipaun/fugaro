// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The organisations screen: the tenancy a person has, and the way into each of
// their organisations (portal plan P2.3; spec §Tenancy).
//
// **One request, because the answer is one document.** The screen's reader is
// the person themselves -- the organisations they belong to and their
// membership in each -- and the API answers that as one shape, because a
// screen that read the organisations first and then asked about each one would
// be several seconds of a page drawing itself in pieces. The shape is spelled
// here rather than in `src/api/types/orgs.ts`: that file describes the objects
// the API's area owns (P2.1), and a screen's envelope belongs to the screen
// that reads it.
//
// **The last-used organisation is read, not guessed.** A membership's
// `last_used_at` is what the API wrote when the person last worked in that
// organisation and is what the switcher lands them on (P2.1). Null means they
// never have, which is not a time: a tenancy with nothing used falls back to
// the first organisation the API listed, so the screen always names one.
//
// **A new organisation takes the person to it.** The screen could re-read the
// tenancy, but the API's answer to the creation is the organisation itself, and
// the members screen of an organisation nobody else is in yet is exactly what
// an owner needs next.

import { useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError, createApiClient } from '../../api/client'
import type { Membership, Organisation, OrganisationCreate } from '../../api/types/orgs'
import { useSession } from '../../auth/session'
import { SIGN_IN_PATH } from '../../routes'
import { OrganisationSwitcher, membersPath } from './switcher'
import type { OrganisationEntry } from './switcher'

/** The organisations a person belongs to, and their membership in each. */
export interface Tenancy {
  organisations: Organisation[]
  memberships: Membership[]
}

type Load =
  | { status: 'loading' }
  | { status: 'failed'; message: string }
  | { status: 'ready'; tenancy: Tenancy }

const GENERIC_FAILURE = 'The portal could not read your organisations. Try again.'

/** The sentence a person is shown for a failure. */
function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

/**
 * The organisation the person last worked in, or the first one the API listed.
 * `last_used_at` is written in UTC, so the newest is the greatest string.
 */
function landedOn(tenancy: Tenancy): string | null {
  let lastUsed: string | null = null
  let usedAt: string | null = null
  for (const membership of tenancy.memberships) {
    if (membership.last_used_at === null) {
      continue
    }
    if (usedAt === null || membership.last_used_at > usedAt) {
      lastUsed = membership.organisation_id
      usedAt = membership.last_used_at
    }
  }
  return lastUsed ?? tenancy.organisations[0]?.id ?? null
}

/** The organisations with their memberships, for the organisations the person is in. */
function entriesOf(tenancy: Tenancy): OrganisationEntry[] {
  const memberships = new Map(tenancy.memberships.map((one) => [one.organisation_id, one]))
  return tenancy.organisations.flatMap((organisation) => {
    const membership = memberships.get(organisation.id)
    return membership === undefined ? [] : [{ organisation, membership }]
  })
}

export function OrgsScreen() {
  const { state: session, auth } = useSession()
  const api = useMemo(() => createApiClient({ token: () => auth.idToken() }), [auth])
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  useEffect(() => {
    if (session.status !== 'signed-in') {
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
  }, [api, session.status])

  async function create(event: FormEvent): Promise<void> {
    event.preventDefault()
    setBusy(true)
    setFailure(null)
    const body: OrganisationCreate = { name: name.trim() }
    try {
      const created = await api.request<Organisation>('/orgs', { method: 'POST', body })
      navigate(membersPath(created.id))
    } catch (error) {
      setFailure(sentence(error))
    } finally {
      setBusy(false)
    }
  }

  if (session.status === 'signed-out') {
    return <Navigate to={SIGN_IN_PATH} replace />
  }

  if (session.status === 'loading' || load.status === 'loading') {
    return (
      <main>
        <p role="status">Loading…</p>
      </main>
    )
  }

  const tenancy = load.status === 'ready' ? load.tenancy : null
  const entries = tenancy === null ? [] : entriesOf(tenancy)
  const selectedId = tenancy === null ? null : (params.get('org') ?? landedOn(tenancy))

  return (
    <main>
      <h1>Your organisations</h1>
      {load.status === 'failed' ? <p role="alert">{load.message}</p> : null}
      {entries.length === 0 && load.status === 'ready' ? (
        <p>You do not belong to an organisation yet. Create one below.</p>
      ) : null}
      {entries.length === 0 ? null : (
        <OrganisationSwitcher entries={entries} selectedId={selectedId} />
      )}

      <h2>Create an organisation</h2>
      <form onSubmit={(event) => void create(event)}>
        <label htmlFor="organisation-name">Name</label>
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
          Create organisation
        </button>
      </form>
      {failure === null ? null : <p role="alert">{failure}</p>}
    </main>
  )
}
