// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The switcher and the screen that mounts it (portal plan P2.3; spec §Tenancy).
//
// **Two shapes of case, because the switcher has two jobs.** Rendered with the
// organisations it is given, it is the list: one link per organisation, into
// that organisation's own screens, with the one the person is in marked. MOUNTED
// by the organisations screen, it is the answer to "which organisation am I
// working in", and the screen is what reads the API: it lands the person on the
// organisation the API recorded as last used, and a new organisation takes them
// to it.
//
// The one refusal the switcher makes on its own -- an operations organisation
// chosen by an emailed-link identity -- is `sso.test.tsx`'s, where the dialog
// it opens is what is being read.
//
// Hermetic: the port is a fake and `fetch` is a queue of answers, so no test
// reaches Firebase or the API. What the queue pins is the request itself: the
// path, the method and the body a screen sends are the API's contract, and a
// screen that quietly changed one would otherwise pass every test it has.

import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Membership, Organisation } from '../../api/types/orgs'
import type { AuthPort, SignedInUser } from '../../auth/session'
import { SessionProvider } from '../../auth/session'
import { OrgsScreen } from './orgs'
import { OrganisationSwitcher, organisationIdIn } from './switcher'

// The suite runs without the runner's globals, so React Testing Library cannot
// register its own cleanup.
afterEach(cleanup)

// The fixtures are `ee/portal/schemas/fixtures/orgs.*.json`'s samples with a
// second organisation beside them: the shapes the schema suite validates are
// the shapes these screens read, and two of them are what a list needs to say
// anything.
const EXAMPLE: Organisation = {
  id: 'org_example',
  name: 'Example Ltd',
  type: 'customer',
  terms: { version: '2026-10-27', accepted_at: '2026-02-10T11:00:00Z' },
  reseller_id: null,
  created_at: '2026-02-10T11:00:00Z',
}

const OPERATIONS: Organisation = {
  id: 'org_ops',
  name: 'Fugaro operations',
  type: 'operations',
  terms: null,
  reseller_id: null,
  created_at: '2026-01-01T00:00:00Z',
}

// The organisation the person last worked in is deliberately NOT the first the
// API lists: with the two in the same order, "the last used one" and "the first
// one listed" are the same id and a screen could answer either and pass.
const TENANCY: { organisations: Organisation[]; memberships: Membership[] } = {
  organisations: [OPERATIONS, EXAMPLE],
  memberships: [
    {
      organisation_id: EXAMPLE.id,
      uid: 'uid_dana',
      email: 'dana@example.com',
      role: 'owner',
      created_at: '2026-02-10T11:00:00Z',
      last_used_at: '2026-02-11T08:00:00Z',
    },
    {
      organisation_id: OPERATIONS.id,
      uid: 'uid_dana',
      email: 'dana@example.com',
      role: 'member',
      created_at: '2026-01-02T00:00:00Z',
      last_used_at: null,
    },
  ],
}

const CREATED: Organisation = {
  id: 'org_northwind',
  name: 'Northwind',
  type: 'customer',
  terms: { version: '2026-10-27', accepted_at: '2026-03-01T00:00:00Z' },
  reseller_id: null,
  created_at: '2026-03-01T00:00:00Z',
}

const PERSON: SignedInUser = {
  uid: 'uid_dana',
  email: 'dana@example.com',
  provider: 'google.com',
  authTime: 1_700_000_000_000,
}

function port(overrides: Partial<AuthPort> = {}): AuthPort {
  return {
    watch: (onUser) => {
      onUser(PERSON)
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

/** Where the router ended up, so a screen's own navigation is readable. */
function Landing() {
  const location = useLocation()
  return <p>{location.pathname + location.search}</p>
}

function mount(path: string, auth: AuthPort = port()): void {
  render(
    <SessionProvider port={auth}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/orgs" element={<OrgsScreen />} />
          <Route path="*" element={<Landing />} />
        </Routes>
      </MemoryRouter>
    </SessionProvider>,
  )
}

describe('the organisation switcher', () => {
  it('links each organisation into its own screens', () => {
    render(
      <SessionProvider port={port()}>
        <MemoryRouter>
          <OrganisationSwitcher
            entries={[
              { organisation: EXAMPLE, membership: TENANCY.memberships[0]! },
              { organisation: OPERATIONS, membership: TENANCY.memberships[1]! },
            ]}
            selectedId={null}
          />
        </MemoryRouter>
      </SessionProvider>,
    )

    expect(screen.getByRole('link', { name: EXAMPLE.name }).getAttribute('href')).toBe(
      `/members?org=${EXAMPLE.id}`,
    )
    expect(
      screen.getByRole('link', { name: `Settings for ${EXAMPLE.name}` }).getAttribute('href'),
    ).toBe(`/settings?org=${EXAMPLE.id}`)
  })

  it('marks the organisation the person is in, and no other', () => {
    render(
      <SessionProvider port={port()}>
        <MemoryRouter>
          <OrganisationSwitcher
            entries={[
              { organisation: EXAMPLE, membership: TENANCY.memberships[0]! },
              { organisation: OPERATIONS, membership: TENANCY.memberships[1]! },
            ]}
            selectedId={EXAMPLE.id}
          />
        </MemoryRouter>
      </SessionProvider>,
    )

    expect(screen.getByRole('link', { name: EXAMPLE.name }).getAttribute('aria-current')).toBe(
      'true',
    )
    expect(
      screen.getByRole('link', { name: OPERATIONS.name }).getAttribute('aria-current'),
    ).toBeNull()
  })
})

describe('the organisations screen', () => {
  it('lists the organisations, and lands on the one the API recorded as last used', async () => {
    const fetch = transport([json(200, TENANCY)])
    mount('/orgs')

    const used = await screen.findByRole('link', { name: EXAMPLE.name })
    expect(used.getAttribute('aria-current')).toBe('true')
    expect(
      screen.getByRole('link', { name: OPERATIONS.name }).getAttribute('aria-current'),
    ).toBeNull()
    expect(fetch).toHaveBeenCalledWith(
      '/api/orgs',
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: 'Bearer token' }),
      }),
    )
  })

  it('falls back to the first organisation the API listed when none was ever used', async () => {
    const unused = {
      organisations: TENANCY.organisations,
      memberships: TENANCY.memberships.map((one) => ({ ...one, last_used_at: null })),
    }
    transport([json(200, unused)])
    mount('/orgs')

    const first = await screen.findByRole('link', { name: OPERATIONS.name })
    expect(first.getAttribute('aria-current')).toBe('true')
    expect(
      screen.getByRole('link', { name: EXAMPLE.name }).getAttribute('aria-current'),
    ).toBeNull()
  })

  it('goes to the members of the organisation the person chooses', async () => {
    transport([json(200, TENANCY)])
    mount('/orgs')

    fireEvent.click(await screen.findByRole('link', { name: OPERATIONS.name }))

    expect(await screen.findByText(`/members?org=${OPERATIONS.id}`)).toBeDefined()
  })

  it('creates an organisation and goes to it', async () => {
    const fetch = transport([json(200, TENANCY), json(200, CREATED)])
    mount('/orgs')

    fireEvent.change(await screen.findByLabelText('Name'), { target: { value: 'Northwind' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create organisation' }))

    expect(await screen.findByText(`/members?org=${CREATED.id}`)).toBeDefined()
    expect(fetch).toHaveBeenCalledWith(
      '/api/orgs',
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ name: 'Northwind' }) }),
    )
  })
})

describe('the organisation a screen’s URL names', () => {
  it('reads back the id the switcher’s own links carry, and refuses what cannot be one', () => {
    expect(organisationIdIn(EXAMPLE.id)).toBe(EXAMPLE.id)
    expect(organisationIdIn('a b')).toBe('a b')

    // `.` and `..` are the segments a browser resolves before the request is
    // sent, so `/api/orgs/../members` would be fetched as `/api/members`, with
    // the ID token on it; they are also what encoding cannot make inert. An
    // empty query names nobody.
    expect(organisationIdIn('..')).toBeNull()
    expect(organisationIdIn('.')).toBeNull()
    expect(organisationIdIn('')).toBeNull()
    expect(organisationIdIn(null)).toBeNull()
  })
})
