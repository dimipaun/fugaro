// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The organisations screen: the tenancy a person has, and the way into each of
// their organisations (portal plan P2.3; spec §Tenancy).
//
// **The answer is the API's list and it is organisations alone** (P2.1's
// `WireOrgList`): the person's own organisations, ordered the way the switcher
// needs them — the one they last worked in first — and nothing about their
// memberships. A person's role is per organisation and the list is not a place
// it is needed: what a screen decides from a role, the API decides from the
// membership the request uses, and a member learns they decide nothing from the
// refusal rather than from a copy of the rule here.
//
// **The organisation the API put first is the one they land on.** The ordering
// is the API's (P2.1's `organisationsOf`: most recently used first, a
// never-used organisation last, ties by id), and "they land in the last one used"
// is that ordering read at its head. A tenancy with nothing used yet is not a
// special case: the head is then the first organisation the API listed, so the
// screen always names one.
//
// **A new organisation takes the person to it.** The screen could re-read the
// tenancy, but the API's answer to the creation is the organisation itself, and
// the members screen of an organisation nobody else is in yet is exactly what
// an owner needs next. Sign-up is also the one selection nobody makes — P2.1's
// `signUp` records the founder's membership as their selection — so creating one
// and going to it leaves nothing for the switcher to write.

import { useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError, createApiClient } from '../../api/client'
import type { Organisation, OrganisationCreate } from '../../api/types/orgs'
import { useSession } from '../../auth/session'
import { SIGN_IN_PATH } from '../../routes'
import { OrganisationSwitcher, membersPath } from './switcher'

/** The organisations a person belongs to, as `GET /orgs` answers them. */
export interface Tenancy {
  organisations: Organisation[]
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

  const organisations = load.status === 'ready' ? load.tenancy.organisations : []
  const selectedId = organisations.some((one) => one.id === params.get('org'))
    ? params.get('org')
    : (organisations[0]?.id ?? null)

  return (
    <main>
      <h1>Your organisations</h1>
      {load.status === 'failed' ? <p role="alert">{load.message}</p> : null}
      {organisations.length === 0 && load.status === 'ready' ? (
        <p>You do not belong to an organisation yet. Create one below.</p>
      ) : null}
      {organisations.length === 0 ? null : (
        <OrganisationSwitcher organisations={organisations} selectedId={selectedId} />
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
