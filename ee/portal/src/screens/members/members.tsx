// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The members screen: who is in the organisation, and who has been asked
// (portal plan P2.3; spec §Tenancy, F40).
//
// **Who may decide is read from the membership, never guessed.** The screen
// finds the signed-in person in the list the API answered and offers the
// invitation form to owners and admins only (D2195: owners and admins are who
// invite). Nothing here is enforcement -- the API refuses a member's write --
// and it is what keeps the refusal from being how a person finds out. A role
// read from the token would be a role only the identity provider could take
// away, which is why neither side keeps it there.
//
// **An invitation is an address and not a person** (spec §Tenancy): the list
// shows the address it is bound to and the state it has reached, and
// withdrawing one re-reads the list rather than removing the row. A revoked
// invitation is a state and not a deletion -- a person asking what happened to
// the invitation they sent is asking about a row that is still there.
//
// **The organisation is the URL's, and a person with none is sent to choose
// one.** `?org=` is what the switcher put there (P2.3's `switcher.tsx`), so a
// screen that found none is one nobody selected an organisation for.
//
// **Nothing here takes the URL at its word.** Its value is text a person can
// edit (`?org=../..`) and it is a segment of every path this screen builds, so
// it is read through the switcher's own reader (`organisationIdIn`), which is
// the inverse of the encoder that wrote it, and it is encoded where a path is
// built. The client's `apiPath` is what refuses a path that leaves the origin
// and is not what refuses this: a `..` segment is inside `/api` and is resolved
// by the browser before the request is sent.

import { useEffect, useMemo, useState } from 'react'
import type { FormEvent } from 'react'
import { Link, Navigate, useSearchParams } from 'react-router-dom'
import { ApiError, createApiClient } from '../../api/client'
import type { Invite, InviteCreate, InviteState } from '../../api/types/invites'
import type { Membership, Role } from '../../api/types/orgs'
import { useSession } from '../../auth/session'
import { SIGN_IN_PATH } from '../../routes'
import { organisationIdIn } from '../orgs/switcher'

const ROLE_LABELS: Record<Role, string> = {
  owner: 'Owner',
  admin: 'Admin',
  member: 'Member',
}

const STATE_LABELS: Record<InviteState, string> = {
  pending: 'Pending',
  accepted: 'Accepted',
  revoked: 'Revoked',
  expired: 'Expired',
}

const GENERIC_FAILURE = 'The portal could not read this organisation’s members. Try again.'

type Load =
  | { status: 'loading' }
  | { status: 'failed'; message: string }
  | { status: 'ready'; members: Membership[]; invites: Invite[] }

/** The sentence a person is shown for a failure. */
function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

export function MembersScreen() {
  const { state: session, auth } = useSession()
  const api = useMemo(() => createApiClient({ token: () => auth.idToken() }), [auth])
  const [params] = useSearchParams()
  const organisationId = organisationIdIn(params.get('org'))
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Role>('member')
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  useEffect(() => {
    if (session.status !== 'signed-in' || organisationId === null) {
      return
    }
    let live = true
    void (async () => {
      try {
        const [members, invites] = await Promise.all([
          api.request<Membership[]>(`/orgs/${encodeURIComponent(organisationId)}/members`),
          api.request<Invite[]>(`/orgs/${encodeURIComponent(organisationId)}/invites`),
        ])
        if (live) {
          setLoad({ status: 'ready', members, invites })
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

  async function invite(id: string, event: FormEvent): Promise<void> {
    event.preventDefault()
    setBusy(true)
    setFailure(null)
    const body: InviteCreate = { email: email.trim(), role }
    try {
      const created = await api.request<Invite>(`/orgs/${encodeURIComponent(id)}/invites`, {
        method: 'POST',
        body,
      })
      setLoad((current) =>
        current.status === 'ready'
          ? { ...current, invites: [created, ...current.invites] }
          : current,
      )
      setEmail('')
    } catch (error) {
      setFailure(sentence(error))
    } finally {
      setBusy(false)
    }
  }

  async function withdraw(id: string, inviteId: string): Promise<void> {
    setBusy(true)
    setFailure(null)
    try {
      await api.request<void>(`/orgs/${encodeURIComponent(id)}/invites/${inviteId}`, {
        method: 'DELETE',
      })
      const invites = await api.request<Invite[]>(`/orgs/${encodeURIComponent(id)}/invites`)
      setLoad((current) => (current.status === 'ready' ? { ...current, invites } : current))
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
  const mine =
    load.status === 'ready'
      ? load.members.find((member) => member.uid === session.user.uid)
      : undefined
  const decides = mine !== undefined && (mine.role === 'owner' || mine.role === 'admin')

  return (
    <main>
      <h1>Members</h1>
      <p>
        <Link to="/orgs">Your organisations</Link>
      </p>
      {load.status === 'failed' ? <p role="alert">{load.message}</p> : null}

      {load.status === 'ready' ? (
        <section>
          <h2>Members</h2>
          <ul>
            {load.members.map((member) => (
              <li key={member.uid}>
                <span>{member.email}</span> <span>{ROLE_LABELS[member.role]}</span>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {load.status === 'ready' ? (
        <section>
          <h2>Invitations</h2>
          {load.invites.length === 0 ? (
            <p>Nobody has been invited to this organisation yet.</p>
          ) : (
            <ul>
              {load.invites.map((invite) => (
                <li key={invite.id}>
                  <span>{invite.email}</span> <span>{ROLE_LABELS[invite.role]}</span>{' '}
                  <span>{STATE_LABELS[invite.state]}</span>
                  {decides && invite.state === 'pending' ? (
                    <button
                      type="button"
                      className="secondary"
                      disabled={busy}
                      aria-label={`Withdraw the invitation to ${invite.email}`}
                      onClick={() => void withdraw(selected, invite.id)}
                    >
                      Withdraw
                    </button>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </section>
      ) : null}

      {decides ? (
        <section>
          <h2>Invite somebody</h2>
          <form onSubmit={(event) => void invite(selected, event)}>
            <label htmlFor="invite-email">Email address</label>
            <input
              id="invite-email"
              name="email"
              type="email"
              required
              value={email}
              onChange={(event) => {
                setEmail(event.target.value)
              }}
            />
            <label htmlFor="invite-role">Role</label>
            <select
              id="invite-role"
              name="role"
              value={role}
              onChange={(event) => {
                setRole(event.target.value as Role)
              }}
            >
              <option value="member">Member</option>
              <option value="admin">Admin</option>
              <option value="owner">Owner</option>
            </select>
            <button type="submit" disabled={busy}>
              Send invitation
            </button>
          </form>
        </section>
      ) : null}

      {failure === null ? null : <p role="alert">{failure}</p>}
    </main>
  )
}
