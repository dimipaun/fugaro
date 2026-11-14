// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The operations screens (portal plan P5.3; D2187, D2207, F5, F9, F16).
//
// **Two readings, and they are the two the API enforces anyway.** Who may act
// (an owner or an admin of the operations organisation, and nobody else, D2187)
// and how fresh their sign-in has to be (five minutes, on the token's own
// `auth_time`, D2187). The screens are a courtesy in both cases -- P5.1 refuses
// a member and a stale sign-in whatever this app draws -- and the point of
// drawing them is that nobody meets the refusal: a member is offered the reads
// and no button at all, and a change under an old sign-in asks for a fresh one
// instead of being sent and refused.
//
// **The third reading is a thing the screen must not do.** The parked Paddle
// queue is decided here, and the queue's shape carries the body Paddle sent --
// a buyer's own records, kept as received (F16, D457). The screen decides an
// event from the event's own facts and never draws the body, so this suite
// reads the whole document and refuses to find the buyer in it.
//
// Nothing here reaches the network or an account: the auth port is a fake, and
// the fetch is one that answers the routes a case names.

import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApiClient } from '../../api/client'
import type { ApiClient } from '../../api/client'
import type { AuditEntry } from '../../api/types/audit'
import type { Entitlement } from '../../api/types/entitlements'
import type { KeyMetadata } from '../../api/types/keys'
import type { LedgerEntry, LedgerEvent } from '../../api/types/ledger'
import type { Membership, Organisation, Role } from '../../api/types/orgs'
import type { ReportMonth } from '../../api/types/reports'
import type { Workspace } from '../../api/types/workspaces'
import type { AuthPort, SignedInUser } from '../../auth/session'
import { SessionProvider } from '../../auth/session'
import type { OpsListsWire } from './api'
import { opsApi } from './api'
import { OpsScreen } from './ops'

afterEach(cleanup)

// What the parked event's stored body holds, and what no screen may draw: a
// buyer's address is Paddle's record and counsel's retention item, not the
// operations team's to read out of a queue (F16).
const BUYER = 'buyer@example.com'

const OPERATIONS: Organisation = {
  id: 'org_operations',
  name: 'Fugaro',
  type: 'operations',
  terms: null,
  reseller_id: null,
  created_at: '2026-01-01T00:00:00Z',
}

const CUSTOMER: Organisation = {
  id: 'org_example',
  name: 'Example Ltd',
  type: 'customer',
  terms: null,
  reseller_id: null,
  created_at: '2026-01-01T00:00:00Z',
}

const WORKSPACE: Workspace = {
  id: 'ws_example',
  organisation_id: 'org_example',
  name: 'Example',
  project_id: 'example-portal',
  entitlement_id: 'ent_1',
  created_at: '2026-02-10T11:00:00Z',
  bound_at: '2026-02-11T08:00:00Z',
}

const ENTITLEMENT: Entitlement = {
  id: 'ent_1',
  organisation_id: 'org_example',
  workspace_id: 'ws_example',
  source: 'purchase',
  row: 'team-monthly',
  tier: 'team',
  kind: 'paid',
  seats: 3,
  starts_at: '2026-02-10T11:00:00Z',
  ends_at: '2026-03-10T11:00:00Z',
  status: 'active',
  air_gapped: false,
  created_at: '2026-02-10T11:00:00Z',
  bound_at: '2026-02-11T08:00:00Z',
  rebindable_at: null,
}

const KEY: KeyMetadata = {
  key_id: 'key_1',
  entitlement_id: 'ent_1',
  workspace_id: 'example-portal',
  tier: 'team',
  kind: 'paid',
  seats: 3,
  issued_at: '2026-02-11T08:00:00Z',
  paid_through: '2026-03-10T11:00:00Z',
  last_seen: null,
}

const EVENT: LedgerEvent = {
  id: 'evt_1',
  received_at: '2026-02-11T08:00:00Z',
  signature: 'a-fixture-value-that-is-not-a-signature',
  notification: 'transaction.completed',
  body: `{"event_id":"evt_1","customer_email":"${BUYER}"}`,
  state: 'parked',
  entitlement_id: null,
}

const ENTRY: LedgerEntry = {
  id: 'evt_1',
  at: '2026-02-10T11:00:00Z',
  kind: 'purchase',
  organisation_id: 'org_example',
  workspace_id: 'ws_example',
  entitlement_id: 'ent_1',
  amount_micros: 24000000,
  currency: 'USD',
  seats: 3,
  channel: 'direct',
}

const AUDIT: AuditEntry = {
  id: 'aud_1',
  at: '2026-02-11T08:00:00Z',
  actor_uid: 'uid-ops',
  actor_email: 'ops@example.com',
  organisation_id: 'org_example',
  action: 'entitlement.revoke',
  target_id: 'ent_1',
  read: false,
  detail: 'the customer asked for it',
}

const MONTH: ReportMonth = {
  month: '2026-02',
  direct_micros: 24000000,
  reseller_micros: 0,
  customers: [{ organisation_id: 'org_example', name: 'Example Ltd', channel: 'direct', amount_micros: 24000000 }],
}

const LISTS: OpsListsWire = { email_domains: ['example.com'], microsoft_directories: [] }

/** The membership row the API answers about the signed-in person. */
function member(role: Role): Membership {
  return {
    organisation_id: OPERATIONS.id,
    uid: 'uid-ops',
    email: 'ops@example.com',
    role,
    created_at: '2026-01-01T00:00:00Z',
    last_used_at: '2026-02-11T08:00:00Z',
  }
}

/** A person whose sign-in is `age` milliseconds old. */
function person(age: number): SignedInUser {
  return {
    uid: 'uid-ops',
    email: 'ops@example.com',
    provider: 'google.com',
    authTime: Date.now() - age,
  }
}

interface Reply {
  status?: number
  body?: unknown
}

interface Request {
  method: string
  path: string
  query: URLSearchParams
  body: unknown
}

interface Server {
  fetch: typeof globalThis.fetch
  requests: Request[]
  sent(method: string, path: string): Request[]
}

/** A refusal the API wrote, in the one shape every refusal has (P1.0). */
function refusal(status: number, code: string, message: string): Reply {
  return { status, body: { error: { code, message } } }
}

/**
 * A reply the API client can read. `Response` itself is not built here: the
 * client reads `ok`, `status` and `json` and nothing else, and a fake that
 * needed a real one would be a fake that needs a network stack.
 */
function answer(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response
}

/** The routes every case needs, and the one route a case is about. */
function routes(overrides: Record<string, Reply> = {}): Record<string, Reply> {
  return {
    'GET /api/ops/membership': { body: member('owner') },
    'GET /api/ops/events': { body: [EVENT] },
    'GET /api/ops/ledger': { body: [ENTRY] },
    'GET /api/ops/audit': { body: [AUDIT] },
    'GET /api/ops/reports': { body: MONTH },
    'GET /api/ops/lists': { body: LISTS },
    ...overrides,
  }
}

function serve(replies: Record<string, Reply>): Server {
  const requests: Request[] = []
  const fetch = (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input), 'https://portal.example.test')
    const method = init?.method ?? 'GET'
    requests.push({
      method,
      path: url.pathname,
      query: url.searchParams,
      body: init?.body === undefined ? null : (JSON.parse(String(init.body)) as unknown),
    })
    const reply = replies[`${method} ${url.pathname}`]
    if (reply === undefined) {
      return Promise.resolve(
        answer(404, {
          error: { code: 'no_route', message: `this case answers no ${method} ${url.pathname}` },
        }),
      )
    }
    return Promise.resolve(answer(reply.status ?? 200, reply.body))
  }
  return {
    fetch: fetch as typeof globalThis.fetch,
    requests,
    sent: (method, path) => requests.filter((one) => one.method === method && one.path === path),
  }
}

interface Shown extends Server {
  signIn: ReturnType<typeof vi.fn>
  client: ApiClient
}

/** The screen as the router mounts it, with the session and the API faked. */
function show(user: SignedInUser | null, replies: Record<string, Reply>): Shown {
  const server = serve(replies)
  const signIn = vi.fn(() => Promise.resolve())
  const port: AuthPort = {
    watch: (onUser) => {
      onUser(user)
      return () => {}
    },
    idToken: () => Promise.resolve('an-id-token'),
    signIn,
    sendSignInLink: () => Promise.resolve(),
    isSignInLink: () => false,
    completeSignInLink: () => Promise.resolve(),
    signOut: () => Promise.resolve(),
  }
  const client = createApiClient({ token: () => Promise.resolve('an-id-token'), fetch: server.fetch })
  render(
    <SessionProvider port={port}>
      <OpsScreen api={opsApi(client)} />
    </SessionProvider>,
  )
  return { ...server, signIn, client }
}

/** The action buttons: every one of them changes something. */
const ACTIONS = [
  'Mint',
  'Extend',
  'Change seats',
  'Comp',
  'Revoke',
  'Transfer the project id',
  'Cut the handover short',
  'Attach',
  'Dismiss',
  'Save the lists',
]

function fillMint(): void {
  fireEvent.change(screen.getByLabelText('Workspace'), { target: { value: 'ws_example' } })
  fireEvent.change(screen.getByLabelText('Row'), { target: { value: 'team-monthly' } })
  fireEvent.change(screen.getByLabelText('Seats'), { target: { value: '5' } })
  fireEvent.change(screen.getByLabelText('Ends'), { target: { value: '2026-03-10' } })
}

describe('the operations screens', () => {
  it('offers a member the reads and none of the action buttons', async () => {
    show(person(0), routes({ 'GET /api/ops/membership': { body: member('member') } }))

    await screen.findByRole('heading', { name: 'The queue' })
    expect(screen.getByText(/members read/)).toBeDefined()
    // The reads are drawn, which is what the buttons' absence is measured
    // against: a screen that drew nothing at all would pass the loop below.
    expect(screen.getByText('Example Ltd')).toBeDefined()
    expect(screen.getByRole('button', { name: 'Look up' })).toBeDefined()

    for (const name of ACTIONS) {
      expect(screen.queryByRole('button', { name })).toBeNull()
    }
  })

  it('asks for a fresh sign-in instead of sending a mint', async () => {
    const shown = show(person(10 * 60 * 1000), routes())
    await screen.findByRole('heading', { name: 'Terms and keys' })
    fillMint()

    fireEvent.click(screen.getByRole('button', { name: 'Mint' }))

    const asked = await screen.findByRole('alert')
    expect(asked.textContent).toMatch(/five minutes/)
    expect(shown.sent('POST', '/api/ops/mints')).toHaveLength(0)

    fireEvent.click(within(asked).getByRole('button', { name: 'Sign in again' }))
    expect(shown.signIn).toHaveBeenCalledWith('google')
  })

  it('sends a mint under a sign-in of the last five minutes, in the minter’s own shape', async () => {
    const shown = show(person(0), routes({ 'POST /api/ops/mints': { body: ENTITLEMENT } }))
    await screen.findByRole('heading', { name: 'Terms and keys' })
    fillMint()

    fireEvent.click(screen.getByRole('button', { name: 'Mint' }))

    await waitFor(() => {
      expect(shown.sent('POST', '/api/ops/mints')).toHaveLength(1)
    })
    expect(shown.sent('POST', '/api/ops/mints')[0]?.body).toEqual({
      workspace_id: 'ws_example',
      row: 'team-monthly',
      seats: 5,
      ends_at: '2026-03-10T00:00:00.000Z',
      check_interval: null,
    })
  })

  it('shows a refusal as the API wrote it', async () => {
    show(
      person(0),
      routes({
        'POST /api/ops/mints': refusal(403, 'stale_sign_in', 'This sign-in is older than five minutes, so it cannot mint.'),
      }),
    )
    await screen.findByRole('heading', { name: 'Terms and keys' })
    fillMint()

    fireEvent.click(screen.getByRole('button', { name: 'Mint' }))

    await screen.findByText('This sign-in is older than five minutes, so it cannot mint.')
  })

  it('decides a parked event from its own facts, and never draws the body it keeps', async () => {
    const shown = show(person(0), routes({ 'POST /api/ops/events/resolutions': { body: EVENT } }))
    await screen.findByRole('heading', { name: 'The queue' })
    expect(screen.getByText(/evt_1/)).toBeDefined()
    expect(document.body.textContent).not.toContain(BUYER)

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))

    await waitFor(() => {
      expect(shown.sent('POST', '/api/ops/events/resolutions')).toHaveLength(1)
    })
    expect(shown.sent('POST', '/api/ops/events/resolutions')[0]?.body).toEqual({
      id: 'evt_1',
      decision: 'dismiss',
      entitlement_id: null,
    })
  })

  it('refuses the screens to a caller who is not in the operations organisation', async () => {
    show(person(0), routes({ 'GET /api/ops/membership': refusal(403, 'not_operations', 'This account is not in the operations organisation.') }))

    await screen.findByText('This account is not in the operations organisation.')
    expect(screen.queryByRole('heading', { name: 'The queue' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Mint' })).toBeNull()
  })
})
