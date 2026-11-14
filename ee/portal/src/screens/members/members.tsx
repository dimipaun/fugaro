// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The members screen: who is in the organisation, and who has been asked
// (portal plan P2.3; spec §Tenancy, F40).
//
// **Who decides is read out of the member list, and every part of that is a
// claim this screen makes rather than an answer it has been given.** The API
// behind it is unwritten, so there is no answer to read: the screen takes the
// list to carry the caller's own membership, finds it by uid, and draws the
// invitation form and the Withdraw buttons only for an `owner` or an `admin`
// (`WireMembership.role`). Refusing the list to a plain member — 403
// `not_permitted` — is the other half of the same claim, and a refusal is drawn
// as the state it would be rather than as a failure: a member then gets the page
// with no member list and no write surface on it. `members.test.tsx` answers the
// list to a member as well as refusing it, so neither half can change alone.
// Getting either wrong is the defect this screen exists to avoid: a member drawn
// an owner's buttons, whose every invitation and withdrawal the API then refuses.
//
// **A refused invitations read is an empty page and not an error**, on the same
// footing and for the same reason: the invitation list is addresses and who
// invited whom, so an API that keeps it to owners and admins refuses it to
// exactly the people it refuses the member list to. The page draws, nothing is
// listed, and there is no alert on it.
//
// **An invitation is an address and not a person** (spec §Tenancy): the list
// shows the address it is bound to and the state it has reached, and
// withdrawing one re-reads the list rather than removing the row. A revoked
// invitation is a state and not a deletion -- a person asking what happened to
// the invitation they sent is asking about a row that is still there. The
// invitation routes are P2.2's and unwritten, so the shape read for them is a
// claim this screen makes and its suite pins (`InvitesAnswer`) rather than one
// read out of an API, which is what keeps the two tasks from disagreeing
// silently.
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

/**
 * One organisation's members, in the shape this screen claims for the answer:
 * the list under its own name, which is how this API answers a list everywhere
 * else (P2.1's `WireOrgList`, `WireOrgMembers`), and each entry carrying the
 * role this screen gates its write surfaces on. The route is P2.1's and
 * unwritten, so the claim is pinned in `members.test.tsx` rather than read out
 * of an API.
 */
interface MembersAnswer {
  members: Membership[]
}

/**
 * One organisation's invitations, as `GET /orgs/{id}/invites` will answer them.
 * The route is P2.2's and unwritten, so the shape is a claim this screen makes
 * and its suite pins: an object with the list under its own name, which is what
 * this API answers everywhere else (P2.1's `WireOrgList`, `WireOrgMembers`).
 * A bare array read here and an object answered there would be a screen that
 * throws during render, which no error boundary above it would catch.
 */
interface InvitesAnswer {
  invites: Invite[]
}

/** A list that was refused is `null` and not an empty one: an empty list is a
 * fact about the organisation, and a refusal is a fact about the reader. */
type Load =
  | { status: 'loading' }
  | { status: 'failed'; message: string }
  | { status: 'ready'; members: Membership[] | null; invites: Invite[] | null }

/** The sentence a person is shown for a failure. */
function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

/** True when this refusal is the one this screen expects for a read it claims
 * is kept to owners and admins (`not_permitted`) — a reader's standing, and not
 * a failure of the page. */
function isNotPermitted(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'not_permitted'
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
    const refused = (error: unknown): null | Promise<never> =>
      isNotPermitted(error) ? null : Promise.reject(error)
    const base = `/orgs/${encodeURIComponent(organisationId)}`
    void (async () => {
      try {
        const [answer, invitations] = await Promise.all([
          api.request<MembersAnswer>(`${base}/members`).catch(refused),
          api.request<InvitesAnswer>(`${base}/invites`).catch(refused),
        ])
        if (live) {
          setLoad({
            status: 'ready',
            members: answer?.members ?? null,
            invites: invitations?.invites ?? null,
          })
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
          ? { ...current, invites: [created, ...(current.invites ?? [])] }
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
      const invitations = await api.request<InvitesAnswer>(
        `/orgs/${encodeURIComponent(id)}/invites`,
      )
      setLoad((current) =>
        current.status === 'ready' ? { ...current, invites: invitations.invites } : current,
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
  const members = load.status === 'ready' ? load.members : null
  const mine = members?.find((member) => member.uid === session.user.uid) ?? null
  const decides = mine !== null && (mine.role === 'owner' || mine.role === 'admin')

  return (
    <main>
      <h1>Members</h1>
      <p>
        <Link to="/orgs">Your organisations</Link>
      </p>
      {load.status === 'failed' ? <p role="alert">{load.message}</p> : null}

      {load.status === 'ready' && members === null ? (
        <p>Who belongs to this organisation is not yours to read or to change.</p>
      ) : null}

      {members === null ? null : (
        <section>
          <h2>Members</h2>
          <ul>
            {members.map((member) => (
              <li key={member.uid}>
                <span>{member.email}</span> <span>{ROLE_LABELS[member.role]}</span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {load.status === 'ready' && load.invites !== null ? (
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
