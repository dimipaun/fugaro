// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The organisation settings screen (portal plan P2.3; spec §Tenancy).
//
// **The screen reads the person's own tenancy, which is where the role is.**
// `GET /orgs` answers the organisations somebody belongs to and their
// memberships in them, and the membership is what says whether this person may
// rename the organisation: owners and admins do (D2195's list of who decides),
// a member reads the name and the terms. Nothing here is enforcement -- the API
// refuses a member's write -- and it is what keeps the refusal from being how a
// person finds out.
//
// The last two cases are the screen's edges: a URL naming no organisation, and
// a URL naming one the person is not in.
//
// Hermetic: the port is a fake and `fetch` is a queue of answers.

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Membership, Organisation } from '../../api/types/orgs'
import type { AuthPort, SignedInUser } from '../../auth/session'
import { SessionProvider } from '../../auth/session'
import { SettingsScreen } from './settings'

// The suite runs without the runner's globals, so React Testing Library cannot
// register its own cleanup.
afterEach(cleanup)

const EXAMPLE: Organisation = {
  id: 'org_example',
  name: 'Example Ltd',
  type: 'customer',
  terms: { version: '2026-10-27', accepted_at: '2026-02-10T11:00:00Z' },
  reseller_id: null,
  created_at: '2026-02-10T11:00:00Z',
}

const RENAMED: Organisation = { ...EXAMPLE, name: 'Example Holdings' }

const RENAMED_MEMBERSHIP: Membership = {
  organisation_id: EXAMPLE.id,
  uid: 'uid_dana',
  email: 'dana@example.com',
  role: 'owner',
  created_at: '2026-02-10T11:00:00Z',
  last_used_at: '2026-02-11T08:00:00Z',
}

const PERSON: SignedInUser = {
  uid: RENAMED_MEMBERSHIP.uid,
  email: RENAMED_MEMBERSHIP.email,
  provider: 'google.com',
  authTime: 1_700_000_000_000,
}

function tenancy(membership: Membership): {
  organisations: Organisation[]
  memberships: Membership[]
} {
  return { organisations: [EXAMPLE], memberships: [membership] }
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
function json(status: number, body: unknown): Response {
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
          <Route path="/settings" element={<SettingsScreen />} />
          <Route path="*" element={<Landing />} />
        </Routes>
      </MemoryRouter>
    </SessionProvider>,
  )
}

describe('the organisation settings screen', () => {
  it('shows the organisation, its type and the terms it accepted', async () => {
    transport([json(200, tenancy(RENAMED_MEMBERSHIP))])
    mount(`/settings?org=${EXAMPLE.id}`)

    expect(await screen.findByRole('heading', { name: EXAMPLE.name })).toBeDefined()
    expect(screen.getByText('Customer')).toBeDefined()
    expect(screen.getByText(/2026-10-27/)).toBeDefined()
  })

  it('renames the organisation', async () => {
    const fetch = transport([json(200, tenancy(RENAMED_MEMBERSHIP)), json(200, RENAMED)])
    mount(`/settings?org=${EXAMPLE.id}`)

    fireEvent.change(await screen.findByLabelText('Organisation name'), {
      target: { value: RENAMED.name },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save name' }))

    expect(await screen.findByRole('heading', { name: RENAMED.name })).toBeDefined()
    expect(fetch).toHaveBeenCalledWith(
      `/api/orgs/${EXAMPLE.id}`,
      expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ name: RENAMED.name }) }),
    )
  })

  it('shows a member the name and no way to change it', async () => {
    transport([json(200, tenancy({ ...RENAMED_MEMBERSHIP, role: 'member' }))])
    mount(`/settings?org=${EXAMPLE.id}`)

    expect(await screen.findByRole('heading', { name: EXAMPLE.name })).toBeDefined()
    expect(screen.queryByLabelText('Organisation name')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Save name' })).toBeNull()
  })

  it('sends a person whose URL names no organisation to the organisations screen', async () => {
    transport([])
    mount('/settings')

    expect(await screen.findByText('/orgs')).toBeDefined()
  })

  it('says so when the URL names an organisation the person is not in', async () => {
    transport([json(200, tenancy(RENAMED_MEMBERSHIP))])
    mount('/settings?org=org_somebody_else')

    expect(await screen.findByText(/not one you belong to/)).toBeDefined()
  })
})
