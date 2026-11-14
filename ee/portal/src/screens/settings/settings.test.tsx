// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The organisation settings screen (portal plan P2.3; spec §Tenancy).
//
// **The screen reads and draws, and it writes nothing.** The organisation it
// draws is found in the person's own tenancy — `GET /orgs`, which is the list
// P2.1 answers and the same read the organisations screen makes — and what it
// draws is what that answer carries: the name, the type, when it was created and
// the terms it accepted.
//
// The last case before the edges is the one that pins the absence: **there is no
// rename here**, and there cannot be one. A rename is a PATCH of the
// organisation, no route of P2.1 mounts one, and the schema calls it a later
// task's (`ee/portal/schemas/orgs.schema.json`), so a form would be a button
// whose only outcome is a 404. A name field appearing here again is a decision
// for whoever mounts that route, and it fails this case until they make it.
//
// The edges at the end are the URL's: a person who names no organisation, an id
// that cannot be one, and an organisation the person does not belong to — which
// the list is what distinguishes from an organisation that is not there at all.
//
// Hermetic: the port is a fake and `fetch` is a queue of answers.

import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Organisation } from '../../api/types/orgs'
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

/** The operations organisation, whose terms are a record that does not exist. */
const OPERATIONS: Organisation = {
  id: 'org_ops',
  name: 'Fugaro operations',
  type: 'operations',
  terms: null,
  reseller_id: null,
  created_at: '2026-01-01T00:00:00Z',
}

const PERSON: SignedInUser = {
  uid: 'uid_dana',
  email: 'dana@example.com',
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
    const fetch = transport([json(200, { organisations: [EXAMPLE] })])
    mount(`/settings?org=${EXAMPLE.id}`)

    expect(await screen.findByRole('heading', { name: EXAMPLE.name })).toBeDefined()
    expect(screen.getByText('Customer')).toBeDefined()
    expect(screen.getByText(/2026-10-27/)).toBeDefined()
    expect(fetch).toHaveBeenCalledWith('/api/orgs', expect.anything())
  })

  it('shows an operations organisation the absence of terms rather than an empty date', async () => {
    transport([json(200, { organisations: [OPERATIONS] })])
    mount(`/settings?org=${OPERATIONS.id}`)

    expect(await screen.findByRole('heading', { name: OPERATIONS.name })).toBeDefined()
    expect(screen.getByText('Operations')).toBeDefined()
    expect(screen.getByText(/No terms were accepted/)).toBeDefined()
  })

  it('draws no way to change the name, because no route answers a rename', async () => {
    const fetch = transport([json(200, { organisations: [EXAMPLE] })])
    mount(`/settings?org=${EXAMPLE.id}`)

    expect(await screen.findByRole('heading', { name: EXAMPLE.name })).toBeDefined()
    expect(screen.queryByRole('textbox')).toBeNull()
    expect(screen.queryByRole('button')).toBeNull()
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('sends a person whose URL names no organisation to the organisations screen', async () => {
    transport([])
    mount('/settings')

    expect(await screen.findByText('/orgs')).toBeDefined()
  })

  it('asks for an organisation, and fetches nothing, when the URL names no id', async () => {
    const fetch = transport([])
    mount('/settings?org=..')

    expect(await screen.findByText('/orgs')).toBeDefined()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('says so when the URL names an organisation the person is not in', async () => {
    transport([json(200, { organisations: [EXAMPLE] })])
    mount('/settings?org=org_somebody_else')

    expect(await screen.findByText(/not one you belong to/)).toBeDefined()
  })
})
