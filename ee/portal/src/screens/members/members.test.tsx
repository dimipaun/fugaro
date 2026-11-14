// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The members screen (portal plan P2.3; spec §Tenancy, F40).
//
// **What this suite pins is who may do what, and that the list is the API's.**
// An owner invites an address with the role they chose and withdraws an
// invitation through the list; a member reads the same page and is offered
// neither. The role comes from the person's own membership in the list the API
// answered, which is the same place the API reads it (P2.1: a role lives in the
// membership and never in the token), so a screen that guessed it from the
// person's address would be refused here.
//
// The two cases at the end are the screen's own edges: a URL that names no
// organisation is a person who never chose one, and nobody signed in is sent to
// the sign-in screen rather than to an API that would refuse them.
//
// Hermetic: the port is a fake and `fetch` is a queue of answers.

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Invite } from '../../api/types/invites'
import type { Membership } from '../../api/types/orgs'
import type { AuthPort, SignedInUser } from '../../auth/session'
import { SessionProvider } from '../../auth/session'
import { MembersScreen } from './members'

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

const COLLEAGUE: Membership = {
  organisation_id: ORGANISATION_ID,
  uid: 'uid_ravi',
  email: 'ravi@example.com',
  role: 'member',
  created_at: '2026-02-12T09:00:00Z',
  last_used_at: null,
}

const PENDING: Invite = {
  id: 'inv_1',
  organisation_id: ORGANISATION_ID,
  email: 'sam@example.com',
  role: 'member',
  state: 'pending',
  invited_by: 'dana@example.com',
  created_at: '2026-02-10T11:00:00Z',
  expires_at: '2026-02-17T11:00:00Z',
  accepted_at: null,
}

const REVOKED: Invite = { ...PENDING, state: 'revoked' }

const PERSON: SignedInUser = {
  uid: OWNER.uid,
  email: OWNER.email,
  provider: 'google.com',
  authTime: 1_700_000_000_000,
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
          <Route path="/members" element={<MembersScreen />} />
          <Route path="*" element={<Landing />} />
        </Routes>
      </MemoryRouter>
    </SessionProvider>,
  )
}

describe('the members screen', () => {
  it('lists the members, and the invitations the API answered', async () => {
    transport([
      json(200, [OWNER, COLLEAGUE]),
      json(200, [PENDING]),
    ])
    mount(`/members?org=${ORGANISATION_ID}`)

    expect(await screen.findByText(COLLEAGUE.email)).toBeDefined()
    expect(screen.getByText(OWNER.email)).toBeDefined()
    expect(screen.getByText(PENDING.email)).toBeDefined()
    expect(screen.getByText('Pending')).toBeDefined()
  })

  it('invites an address with the role chosen, and shows it waiting', async () => {
    const fetch = transport([
      json(200, [OWNER]),
      json(200, []),
      json(200, PENDING),
    ])
    mount(`/members?org=${ORGANISATION_ID}`)

    fireEvent.change(await screen.findByLabelText('Email address'), {
      target: { value: 'sam@example.com' },
    })
    fireEvent.change(screen.getByLabelText('Role'), { target: { value: 'member' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send invitation' }))

    expect(await screen.findByText(PENDING.email)).toBeDefined()
    expect(fetch).toHaveBeenCalledWith(
      `/api/orgs/${ORGANISATION_ID}/invites`,
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ email: 'sam@example.com', role: 'member' }),
      }),
    )
  })

  it('withdraws a pending invitation, and reads the list the API answers now', async () => {
    const fetch = transport([
      json(200, [OWNER]),
      json(200, [PENDING]),
      json(204),
      json(200, [REVOKED]),
    ])
    mount(`/members?org=${ORGANISATION_ID}`)

    fireEvent.click(
      await screen.findByRole('button', { name: `Withdraw the invitation to ${PENDING.email}` }),
    )

    await waitFor(() => {
      expect(screen.getByText('Revoked')).toBeDefined()
    })
    expect(fetch).toHaveBeenCalledWith(
      `/api/orgs/${ORGANISATION_ID}/invites/${PENDING.id}`,
      expect.objectContaining({ method: 'DELETE' }),
    )
  })

  it('offers a member no way to invite or withdraw', async () => {
    transport([json(200, [OWNER, COLLEAGUE]), json(200, [PENDING])])
    mount(
      `/members?org=${ORGANISATION_ID}`,
      port({ ...PERSON, uid: COLLEAGUE.uid, email: COLLEAGUE.email }),
    )

    expect(await screen.findByText(PENDING.email)).toBeDefined()
    expect(screen.queryByLabelText('Email address')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Send invitation' })).toBeNull()
    expect(
      screen.queryByRole('button', { name: `Withdraw the invitation to ${PENDING.email}` }),
    ).toBeNull()
  })

  it('sends a person whose URL names no organisation to the organisations screen', async () => {
    transport([])
    mount('/members')

    expect(await screen.findByText('/orgs')).toBeDefined()
  })

  it('asks for an organisation, and fetches nothing, when the URL names no id', async () => {
    const fetch = transport([])
    mount('/members?org=..')

    expect(await screen.findByText('/orgs')).toBeDefined()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('keeps a URL’s own value in one path segment', async () => {
    const fetch = transport([json(200, [OWNER]), json(200, [])])
    mount('/members?org=../..%2Fx')

    expect(await screen.findByText(OWNER.email)).toBeDefined()
    expect(fetch).toHaveBeenCalledWith('/api/orgs/..%2F..%2Fx/members', expect.anything())
  })

  it('sends a person nobody is signed in as to the sign-in screen', async () => {
    transport([])
    mount(`/members?org=${ORGANISATION_ID}`, port(null))

    expect(await screen.findByText('/signin')).toBeDefined()
  })
})
