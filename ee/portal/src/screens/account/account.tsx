// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The account screen (portal plan P2.4; D2195, spec §Legal and privacy).
//
// **The two acts this screen is for are both deletions, and neither asks a
// question first.** A confirmation step is not this repository's shape (the
// no-prompt policy's own rule, read at the portal's door as at the CLI's), so
// what stands in its place is a sentence: what deleting the organisation or the
// account does, in the terms the API would answer a failure in, next to the one
// button that does it. A person who wants the act presses it; a person who does
// not, does not.
//
// **The organisation half is drawn only for its owner, and that is a claim the
// screen makes from the member list rather than an answer it has been given.**
// `GET /orgs/{id}/members` carries every membership with its role (P2.1's
// `WireOrgMembers`), the screen finds its own by uid, and the button appears
// only for `owner` — the API refuses every other caller with `not_permitted`,
// which is the same refusal the members screen draws as an empty page rather
// than an error. Drawing the button for anybody else would be a button whose
// every press the API refuses, which is the defect P2.3's members screen exists
// to avoid.
//
// **The account half needs no organisation**, and the URL's `?org=` is the
// switcher's (P2.3's `switcher.tsx`), the way it is for the settings and members
// screens: it names which organisation this page may delete. A person who is in
// none still reads this screen and still deletes their account — which is
// exactly the person D2195's sole-owner refusal sends here after they have
// deleted the organisation they owned.
//
// **A refusal is drawn as the API's own sentence**, and that is deliberate for
// the account: "you are the only owner of an organisation" is a state the
// person can change, and the sentence that says so is the API's rather than a
// second copy of it here that could drift from the rule. Nothing is signed out
// and nothing is navigated on a refusal: the screen the person is on is the one
// the answer is about.
//
// **The URL's value is not taken at its word.** It is text a person can edit and
// it becomes a segment of the API path below, so it is read through the
// switcher's own reader (`organisationIdIn`) and encoded where the path is
// built.

import { useEffect, useMemo, useState } from 'react'
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError, createApiClient } from '../../api/client'
import type { Membership, OrganisationMembers } from '../../api/types/orgs'
import { useSession } from '../../auth/session'
import { SIGN_IN_PATH } from '../../routes'
import { organisationIdIn } from '../orgs/switcher'

const GENERIC_FAILURE = 'The portal could not read this organisation. Try again.'

/** The sentence a person is shown for a failure. */
function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

/** True when this refusal is the one a read kept to owners and admins earns
 * (`not_permitted`) — a reader's standing, and not a failure of the page. */
function isNotPermitted(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'not_permitted'
}

/** A member list that was refused is `null` and not an empty one: an empty list
 * is a fact about the organisation, and a refusal is a fact about the reader. */
type Load =
  | { status: 'loading' }
  | { status: 'failed'; message: string }
  | { status: 'ready'; members: Membership[] | null }

export function AccountScreen() {
  const { state: session, auth } = useSession()
  const api = useMemo(() => createApiClient({ token: () => auth.idToken() }), [auth])
  const [params] = useSearchParams()
  const organisationId = organisationIdIn(params.get('org'))
  const navigate = useNavigate()
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  useEffect(() => {
    if (session.status !== 'signed-in' || organisationId === null) {
      return
    }
    let live = true
    void (async () => {
      try {
        const answer = await api.request<OrganisationMembers>(
          `/orgs/${encodeURIComponent(organisationId)}/members`,
        )
        if (live) {
          setLoad({ status: 'ready', members: answer.members })
        }
      } catch (error) {
        if (!live) {
          return
        }
        if (isNotPermitted(error)) {
          setLoad({ status: 'ready', members: null })
        } else {
          setLoad({ status: 'failed', message: sentence(error) })
        }
      }
    })()
    return () => {
      live = false
    }
  }, [api, session.status, organisationId])

  async function deleteOrganisation(id: string): Promise<void> {
    setBusy(true)
    setFailure(null)
    try {
      await api.request<void>(`/orgs/${encodeURIComponent(id)}`, { method: 'DELETE' })
      // The organisation this page is about is gone, so the page it drew is
      // about nothing: the list is where the person can see what is left.
      navigate('/orgs')
    } catch (error) {
      setFailure(sentence(error))
    } finally {
      setBusy(false)
    }
  }

  async function deleteAccount(): Promise<void> {
    setBusy(true)
    setFailure(null)
    try {
      await api.request<void>('/account', { method: 'DELETE' })
      // The sign-in is gone at the API, so the session this browser holds is
      // stale and signing out is what stops the app sending it; the screen then
      // sends the person to sign in, which the session's own state does.
      await auth.signOut()
    } catch (error) {
      setFailure(sentence(error))
    } finally {
      setBusy(false)
    }
  }

  if (session.status === 'signed-out') {
    return <Navigate to={SIGN_IN_PATH} replace />
  }

  if (session.status === 'loading' || (organisationId !== null && load.status === 'loading')) {
    return (
      <main>
        <p role="status">Loading…</p>
      </main>
    )
  }

  const members = load.status === 'ready' ? load.members : null
  const mine =
    session.status === 'signed-in'
      ? (members?.find((member) => member.uid === session.user.uid) ?? null)
      : null
  const owns = mine !== null && mine.role === 'owner' && organisationId !== null

  return (
    <main>
      <h1>Your account</h1>
      <p>
        <Link to="/orgs">Your organisations</Link>
      </p>

      {load.status === 'failed' ? <p role="alert">{load.message}</p> : null}
      {failure === null ? null : <p role="alert">{failure}</p>}

      {organisationId === null ? (
        <p>This address names no organisation, so there is nothing here to delete but your account.</p>
      ) : null}

      {organisationId !== null && load.status === 'ready' && !owns ? (
        <p>Only an organisation&rsquo;s owner can delete it. Ask one of them to, if that is what you want.</p>
      ) : null}

      {owns ? (
        <section>
          <h2>Delete this organisation</h2>
          <p>
            Deleting the organisation cancels the subscriptions it is running and revokes the
            keys of every licence it holds. Its workspaces, its invitations and everybody&rsquo;s
            membership of it go with it. This cannot be undone.
          </p>
          <button
            type="button"
            disabled={busy}
            onClick={() => void deleteOrganisation(organisationId)}
          >
            Delete this organisation
          </button>
        </section>
      ) : null}

      <section>
        <h2>Delete your account</h2>
        <p>
          Deleting your account removes every membership you hold and every invitation bound to
          your address, and deletes your sign-in. The organisations you are in stay, and what they
          bought stays with them. This cannot be undone.
        </p>
        <button type="button" disabled={busy} onClick={() => void deleteAccount()}>
          Delete my account
        </button>
      </section>
    </main>
  )
}
