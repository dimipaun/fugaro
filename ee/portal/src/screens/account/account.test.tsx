// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The account screen (portal plan P2.4; D2195, spec §Legal and privacy).
//
// **What this suite pins is who may press what, on the claim the screen makes
// about an API it reads rather than one it was told.** The organisation's
// delete button is drawn for an `owner` and for nobody else, and the screen
// reads that out of the member list by the signed-in person's own uid — so this
// suite answers the list to an owner, to an admin and to a plain member, and
// answers the read itself as refused (`not_permitted`, which is what a member's
// read of it earns), and pins that only the owner's press is a delete. A button
// drawn for an admin would be one whose every press the API refuses, which is
// the defect P2.3's members screen exists to avoid.
//
// **Every press is read as the request the API is handed**, because that is the
// whole of what a deletion is: the method and the path the client was asked for
// — `DELETE /api/orgs/<id>` and `DELETE /api/account` — and, on a refusal, that
// the API's own sentence is what the person reads. The account's refusal is the
// one this matters most for: "you are the only owner of an organisation" is a
// state the person can change, and the sentence that says so is the API's.
//
// **Neither deletion asks a question first.** There is no dialog to drive and no
// text to type back here, and the suite pins that by pressing once: this
// repository's commands never wait on an input, and the portal's screen is the
// same shape. What stands in the confirmation's place is the sentence beside
// the button, which the first case reads.
//
// The edges at the end are the URL's and the session's: a value that cannot be
// an organisation is read as none (so nothing is fetched for it), and nobody
// signed in is sent to the sign-in screen rather than to an API that would
// refuse them.
//
// Hermetic: the port is a fake and `fetch` is a queue of answers.

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Membership } from '../../api/types/orgs'
import type { AuthPort, SignedInUser } from '../../auth/session'
import { SessionProvider } from '../../auth/session'
import { AccountScreen } from './account'

// The suite runs without the runner's globals, so React Testing Library cannot
// register its own cleanup.
afterEach(cleanup)

const ORGANISATION_ID = 'org_example'

const OWNER: Membership = {
  organisation_id: ORGANISATION_ID,
  uid: 'uid_dana',
  email: 'dana@example.com',
  role: 'owner',
  created_at: '2026-02-10T11:00:00Z',
  last_used_at: '2026-02-11T08:00:00Z',
}

// An admin decides everything else in the portal (D2186) and not this: deleting
// the organisation is the owner's alone, so a suite that only ever signed in
// the owner could not tell the two apart.
const ADMIN: Membership = { ...OWNER, uid: 'uid_pia', email: 'pia@example.com', role: 'admin' }
const MEMBER: Membership = { ...OWNER, uid: 'uid_ravi', email: 'ravi@example.com', role: 'member' }

const PERSON: SignedInUser = {
  uid: OWNER.uid,
  email: OWNER.email,
  provider: 'google.com',
  authTime: 1_700_000_000_000,
}

/** The token of the person holding one of the memberships above: the screen
 * finds its own place in the list by the token's uid, so a case about an admin
 * or a plain member has to be signed in as one. */
function asMember(member: Membership): SignedInUser {
  return { ...PERSON, uid: member.uid, email: member.email }
}

function port(user: SignedInUser | null = PERSON, overrides: Partial<AuthPort> = {}): AuthPort {
  return {
    watch: (onUser) => {
      onUser(user)
      return () => {}
    },
    idToken: vi.fn(() => Promise.resolve('token')),
    signIn: vi.fn(() => Promise.resolve()),
    sendSignInLink: vi.fn(() => Promise.resolve()),
    isSignInLink: () => false,
    completeSignInLink: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
    ...overrides,
  }
}

/** A response the client can read, without a real `fetch` behind it. */
function json(status: number, body?: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response
}

/** The refusal P2.1 answers a plain member's read of the member list with, which
 * is the same refusal the API gives them for a deletion. */
function notPermitted(): Response {
  return json(403, {
    error: {
      code: 'not_permitted',
      message: 'Only an organisation’s owners and admins may read its members.',
    },
  })
}

/** A `fetch` that answers each request from the queue, and records what it was asked. */
function transport(answers: Response[]): ReturnType<typeof vi.fn> {
  const fetch = vi.fn(() => {
    const next = answers.shift()
    if (next === undefined) {
      throw new Error('the test did not answer this request')
    }
    return Promise.resolve(next)
  })
  vi.stubGlobal('fetch', fetch)
  return fetch
}

afterEach(() => {
  vi.unstubAllGlobals()
})

/** Where the router ended up, so the screen's own navigation is readable. */
function Landing() {
  const location = useLocation()
  return <p>{location.pathname + location.search}</p>
}

function mount(path: string, auth: AuthPort = port()): void {
  render(
    <SessionProvider port={auth}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/account" element={<AccountScreen />} />
          <Route path="*" element={<Landing />} />
        </Routes>
      </MemoryRouter>
    </SessionProvider>,
  )
}

describe('the account screen', () => {
  it('deletes the organisation an owner presses for, and lands on the list afterwards', async () => {
    const fetch = transport([json(200, { members: [OWNER] }), json(204)])
    mount(`/account?org=${ORGANISATION_ID}`)

    const button = await screen.findByRole('button', { name: 'Delete this organisation' })
    // The sentence and not a dialog: this is what stands where a confirmation
    // step would, and a screen that grew one would fail the press below.
    expect(screen.getByText(/cancels the subscriptions it is running/)).toBeDefined()
    fireEvent.click(button)

    expect(await screen.findByText('/orgs')).toBeDefined()
    expect(fetch).toHaveBeenCalledWith(
      `/api/orgs/${ORGANISATION_ID}`,
      expect.objectContaining({ method: 'DELETE' }),
    )
  })

  it('draws no way to delete an organisation for an admin', async () => {
    const fetch = transport([json(200, { members: [OWNER, ADMIN] })])
    mount(`/account?org=${ORGANISATION_ID}`, port(asMember(ADMIN)))

    // The account's own half is drawn — the screen is reachable and readable
    // for everybody — and the organisation's is not. An admin decides
    // everything else in the portal (D2186) and not this.
    expect(await screen.findByRole('button', { name: 'Delete my account' })).toBeDefined()
    expect(screen.queryByRole('button', { name: 'Delete this organisation' })).toBeNull()
    expect(screen.getByText(/Only an organisation’s owner can delete it/)).toBeDefined()
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('draws no way to delete an organisation when the member list is refused', async () => {
    transport([notPermitted()])
    mount(`/account?org=${ORGANISATION_ID}`)

    expect(await screen.findByRole('button', { name: 'Delete my account' })).toBeDefined()
    expect(screen.queryByRole('button', { name: 'Delete this organisation' })).toBeNull()
    // A refusal is the reader's standing and not a failure of the page: the
    // members screen draws it the same way, with no alert on it.
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('draws a plain member no organisation button either', async () => {
    transport([json(200, { members: [OWNER, MEMBER] })])
    mount(`/account?org=${ORGANISATION_ID}`, port(asMember(MEMBER)))

    expect(await screen.findByRole('button', { name: 'Delete my account' })).toBeDefined()
    expect(screen.queryByRole('button', { name: 'Delete this organisation' })).toBeNull()
    expect(screen.getByText(/Only an organisation’s owner can delete it/)).toBeDefined()
  })

  it('draws the API’s own sentence when the organisation cannot be deleted', async () => {
    const fetch = transport([
      json(200, { members: [OWNER] }),
      json(404, {
        error: { code: 'not_found', message: 'This organisation is not one you may delete.' },
      }),
    ])
    mount(`/account?org=${ORGANISATION_ID}`)

    fireEvent.click(await screen.findByRole('button', { name: 'Delete this organisation' }))

    expect(await screen.findByRole('alert')).toBeDefined()
    expect(screen.getByText('This organisation is not one you may delete.')).toBeDefined()
    // A refused deletion navigates nowhere: the screen the person is on is the
    // one the answer is about.
    expect(screen.queryByText('/orgs')).toBeNull()
    expect(fetch).toHaveBeenCalledWith(
      `/api/orgs/${ORGANISATION_ID}`,
      expect.objectContaining({ method: 'DELETE' }),
    )
    // The read and the refusal, and no delete of the account with them.
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('deletes the account and signs out, with no organisation in the URL at all', async () => {
    const auth = port()
    const fetch = transport([json(204)])
    mount('/account', auth)

    fireEvent.click(await screen.findByRole('button', { name: 'Delete my account' }))

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith('/api/account', expect.objectContaining({ method: 'DELETE' }))
    })
    // The sign-out follows the API's answer and never precedes it: signing out
    // first would leave an account nobody is signed in to delete.
    await waitFor(() => {
      expect(auth.signOut).toHaveBeenCalled()
    })
    // The account's half needs no organisation, so nothing was read for one.
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('draws the API’s own sentence when the account is the only owner of an organisation', async () => {
    const auth = port()
    const fetch = transport([
      json(409, {
        error: {
          code: 'sole_owner',
          message:
            'You are the only owner of an organisation, so it cannot be left without one. Delete that organisation, or make somebody else an owner, and then delete your account.',
        },
      }),
    ])
    mount('/account', auth)

    fireEvent.click(await screen.findByRole('button', { name: 'Delete my account' }))

    expect(await screen.findByRole('alert')).toBeDefined()
    expect(screen.getByText(/the only owner of an organisation/)).toBeDefined()
    // Nothing is signed out over a refusal: the account is still there, and the
    // person has something to do about it.
    expect(auth.signOut).not.toHaveBeenCalled()
    expect(fetch).toHaveBeenCalledWith('/api/account', expect.objectContaining({ method: 'DELETE' }))
  })

  it('reads a URL that cannot name an organisation as none, and fetches nothing for it', async () => {
    const fetch = transport([])
    mount('/account?org=..')

    expect(await screen.findByRole('button', { name: 'Delete my account' })).toBeDefined()
    expect(screen.queryByRole('button', { name: 'Delete this organisation' })).toBeNull()
    expect(screen.getByText(/names no organisation/)).toBeDefined()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('sends nobody signed in to the sign-in screen', async () => {
    transport([])
    mount(`/account?org=${ORGANISATION_ID}`, port(null))

    expect(await screen.findByText('/signin')).toBeDefined()
  })
})
