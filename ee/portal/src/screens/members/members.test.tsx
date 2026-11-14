// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The members screen (portal plan P2.3; spec §Tenancy, F40).
//
// **What this suite pins is who may do what, on the two claims the screen makes
// about an API that is not written yet.** The screen draws the invite form and
// the Withdraw buttons for an `owner` or an `admin` of the organisation, read
// out of the member list by the signed-in person's uid — so this suite answers
// the list to a plain member and pins that no write surface is drawn, which is
// the case that would otherwise be a member shown an owner's buttons and refused
// by the API on every press. The other claim is that the list is refused to a
// member to begin with: 403 `not_permitted`, which this suite answers the way
// such a refusal arrives, and which the screen draws as a state rather than as
// a failure — no member list, no write surface, no error.
//
// The signed-in person in those cases is the same person the list is about, and
// that is the whole of what the screen reads them from: uid to find the
// membership, `role` on it to decide. Neither is checked here against a rule
// kept somewhere else, because there is nowhere else for it to be kept.
//
// The two cases at the end are the screen's own edges: a URL that names no
// organisation is a person who never chose one, and nobody signed in is sent to
// the sign-in screen rather than to an API that would refuse them.
//
// **The invitations are answered here in a shape, not in whatever P2.2 sends.**
// That task owns the route and is unwritten, so this suite is half of the
// contract: it answers the list under its own name — which is how this API
// answers a list everywhere else (P2.1's `WireOrgList`, `WireOrgMembers`) — and
// every case that reads invitations goes through the one helper that writes it.
// A P2.2 that answered a bare array would fail here, and it should: the screen
// reads the list out of that field, so on a bare array it would read `undefined`
// and throw during render, which no error boundary above it would catch.
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

// An admin is the other role that decides (the screen's gate is `owner` or
// `admin`), and a suite that only ever signed in the owner could not tell the
// two apart.
const ADMIN: Membership = {
  organisation_id: ORGANISATION_ID,
  uid: 'uid_pia',
  email: 'pia@example.com',
  role: 'admin',
  created_at: '2026-02-11T09:00:00Z',
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

/** The refusal P2.1 answers a plain member's read of the member list with. */
function notPermitted(): Response {
  return json(403, {
    error: {
      code: 'not_permitted',
      message: 'Only an organisation’s owners and admins may read its members.',
    },
  })
}

/**
 * The invitations answer, in the shape this suite pins for it: the list under
 * its own name, which is how this API answers a list everywhere else (P2.1's
 * `WireOrgList`, `WireOrgMembers`). P2.2 owns the route and is unwritten, so
 * this is the shape it has to answer for this screen to draw — a bare array
 * answered there throws here, during render, with no error boundary above.
 */
function invitations(list: Invite[]): Response {
  return json(200, { invites: list })
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
    transport([json(200, { members: [OWNER, COLLEAGUE] }), invitations([PENDING])])
    mount(`/members?org=${ORGANISATION_ID}`)

    expect(await screen.findByText(COLLEAGUE.email)).toBeDefined()
    expect(screen.getByText(OWNER.email)).toBeDefined()
    expect(screen.getByText(PENDING.email)).toBeDefined()
    expect(screen.getByText('Pending')).toBeDefined()
  })

  it('invites an address with the role chosen, and shows it waiting', async () => {
    const fetch = transport([
      json(200, { members: [OWNER] }),
      invitations([]),
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
      json(200, { members: [OWNER] }),
      invitations([PENDING]),
      json(204),
      invitations([REVOKED]),
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

  it('draws a member the invitations and no member list, rather than an error', async () => {
    transport([notPermitted(), invitations([PENDING])])
    mount(
      `/members?org=${ORGANISATION_ID}`,
      port({ ...PERSON, uid: COLLEAGUE.uid, email: COLLEAGUE.email }),
    )

    expect(await screen.findByText(PENDING.email)).toBeDefined()
    expect(screen.queryByText(OWNER.email)).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByLabelText('Email address')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Send invitation' })).toBeNull()
    expect(
      screen.queryByRole('button', { name: `Withdraw the invitation to ${PENDING.email}` }),
    ).toBeNull()
  })

  it('draws a member the list and still no write surface, when the list is answered', async () => {
    // The half the refusal does not cover: an API that answers the list to a
    // member — which nothing yet forbids — must not get them an owner's buttons,
    // since every invitation and withdrawal pressed there is a refusal.
    transport([json(200, { members: [OWNER, COLLEAGUE] }), invitations([PENDING])])
    mount(
      `/members?org=${ORGANISATION_ID}`,
      port({ ...PERSON, uid: COLLEAGUE.uid, email: COLLEAGUE.email }),
    )

    expect(await screen.findByText(OWNER.email)).toBeDefined()
    expect(screen.getByText(PENDING.email)).toBeDefined()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByLabelText('Email address')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Send invitation' })).toBeNull()
    expect(
      screen.queryByRole('button', { name: `Withdraw the invitation to ${PENDING.email}` }),
    ).toBeNull()
  })

  it('draws an admin the write surfaces, which is the other half of the gate', async () => {
    transport([json(200, { members: [OWNER, ADMIN] }), invitations([PENDING])])
    mount(`/members?org=${ORGANISATION_ID}`, port({ ...PERSON, uid: ADMIN.uid, email: ADMIN.email }))

    expect(await screen.findByRole('button', { name: 'Send invitation' })).toBeDefined()
    expect(
      screen.getByRole('button', { name: `Withdraw the invitation to ${PENDING.email}` }),
    ).toBeDefined()
  })

  it('draws a refused invitations read as an empty page and not an error', async () => {
    transport([json(200, { members: [OWNER, COLLEAGUE] }), notPermitted()])
    mount(
      `/members?org=${ORGANISATION_ID}`,
      port({ ...PERSON, uid: COLLEAGUE.uid, email: COLLEAGUE.email }),
    )

    expect(await screen.findByText(OWNER.email)).toBeDefined()
    // The list is drawn, and the invitations section is not — not even the
    // "nobody has been invited" line, which would be a claim about the
    // organisation made by somebody who was just refused a look at it.
    expect(screen.queryByText('Nobody has been invited to this organisation yet.')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByLabelText('Email address')).toBeNull()
  })

  it('reads the member list once, and not again for a member the API refused', async () => {
    const fetch = transport([notPermitted(), invitations([])])
    mount(`/members?org=${ORGANISATION_ID}`)

    expect(await screen.findByText('Nobody has been invited to this organisation yet.')).toBeDefined()
    expect(fetch).toHaveBeenCalledTimes(2)
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
    const fetch = transport([json(200, { members: [OWNER] }), invitations([])])
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
