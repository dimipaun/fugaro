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

import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
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
import type { OpsLists } from '../../api/types/ops'
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

// The month's one customer is a reseller's, and named apart from the customer
// the lookup below draws: a report and a lookup are two different records, and
// a fixture that spelled them the same would make a screen that drew the wrong
// one of them pass.
const MONTH: ReportMonth = {
  month: '2026-02',
  direct_micros: 24000000,
  reseller_micros: 12000000,
  customers: [
    {
      organisation_id: 'org_sold',
      name: 'Sold By Reseller Ltd',
      channel: 'reseller',
      amount_micros: 12000000,
    },
  ],
}

const LISTS: OpsLists = { email_domains: ['example.com'], microsoft_directories: [] }

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
    'GET /api/ops/organisations': { body: [CUSTOMER] },
    'GET /api/ops/workspaces': { body: [WORKSPACE] },
    'GET /api/ops/entitlements': { body: [ENTITLEMENT] },
    'GET /api/ops/members': { body: [member('owner')] },
    'GET /api/ops/keys': { body: [KEY] },
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
  /** The SDK's silent token refresh: the same person, a new session object. */
  refresh: () => void
  client: ApiClient
}

/** The screen as the router mounts it, with the session and the API faked. */
function show(user: SignedInUser | null, replies: Record<string, Reply>): Shown {
  const server = serve(replies)
  let listener: ((user: SignedInUser | null) => void) | null = null
  // A sign-in answers the session itself. The person signing in again is the
  // same person -- the same uid, a new sign-in instant -- and the port the real
  // one is watched through is the SDK's token listener, which fires when the
  // token changes and not when the uid does (`src/auth/firebase.ts`). A fake
  // that let a case push a user from outside a sign-in would hand the screen a
  // notification it can never really get, and a screen that only worked under
  // one would pass here and never work in production.
  const signIn = vi.fn(async () => {
    listener?.(person(0))
  })
  // A refresh is the other thing the token listener really does: Firebase
  // renews the token silently about hourly and hands the session the same
  // person again -- the same uid and the claims the token already carried --
  // so this pushes what the SDK pushes rather than a user invented here.
  const refresh = (): void => {
    listener?.(user === null ? null : { ...user })
  }
  const port: AuthPort = {
    watch: (onUser) => {
      listener = onUser
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
  return {
    ...server,
    signIn,
    refresh,
    client,
  }
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

/**
 * Sends the form a named button belongs to. The click on a submit button is a
 * browser's own step and not jsdom's (the sign-in suite submits the form the
 * same way), which is why the button's presence is asserted and its form is
 * sent here.
 */
function send(name: string): void {
  fireEvent.submit(screen.getByRole('button', { name }).closest('form')!)
}

function fillMint(): void {
  fireEvent.change(screen.getByLabelText('Workspace'), { target: { value: 'ws_example' } })
  fireEvent.change(screen.getByLabelText('Row'), { target: { value: 'team-monthly' } })
  fireEvent.change(screen.getByLabelText('Seats'), { target: { value: '5' } })
  fireEvent.change(screen.getByLabelText('Ends'), { target: { value: '2026-03-10' } })
  fireEvent.change(screen.getByLabelText('Check interval'), { target: { value: 'P7D' } })
}

describe('the operations screens', () => {
  it('offers a member the reads and none of the action buttons', async () => {
    show(person(0), routes({ 'GET /api/ops/membership': { body: member('member') } }))

    await screen.findByRole('heading', { name: 'The queue' })
    expect(screen.getByText(/members read/)).toBeDefined()
    // The reads are drawn, which is what the buttons' absence is measured
    // against: a screen that drew nothing at all would pass the loop below.
    expect(await screen.findByText(/Sold By Reseller Ltd/)).toBeDefined()
    expect(screen.getByRole('button', { name: 'Look up' })).toBeDefined()
    // The two lists are a change, and a member is shown them all the same: a
    // field that is read out and not written is a read, and the door the
    // operations team signs in through is something its members may look at.
    expect((await screen.findByLabelText('Email domains')).getAttribute('readonly')).not.toBeNull()

    for (const name of ACTIONS) {
      expect(screen.queryByRole('button', { name })).toBeNull()
    }
  })

  it('reads the membership once for a person, however often their token moves', async () => {
    const shown = show(person(0), routes())
    await screen.findByRole('heading', { name: 'Terms and keys' })
    expect(shown.sent('GET', '/api/ops/membership')).toHaveLength(1)

    await act(async () => {
      shown.refresh()
    })

    // The read is keyed on who is signed in, so a refresh under the same person
    // asks nothing again: one more read would be one more audited read of a
    // customer's record (D456), and one that failed would unmount every section
    // and take a half-filled form with it.
    expect(shown.sent('GET', '/api/ops/membership')).toHaveLength(1)
    expect(screen.getByRole('heading', { name: 'Terms and keys' })).toBeDefined()
  })

  it('asks for a fresh sign-in instead of sending a mint', async () => {
    const shown = show(person(10 * 60 * 1000), routes())
    await screen.findByRole('heading', { name: 'Terms and keys' })
    fillMint()

    send('Mint')

    const asked = await screen.findByRole('alert')
    expect(asked.textContent).toMatch(/five minutes/)
    expect(shown.sent('POST', '/api/ops/mints')).toHaveLength(0)
    // Nothing was sent, so nothing landed: a screen that said otherwise would
    // be claiming a mint that never left the browser.
    expect(screen.queryByText(/The term is granted/)).toBeNull()

    fireEvent.click(within(asked).getByRole('button', { name: 'Sign in again' }))
    expect(shown.signIn).toHaveBeenCalledWith('google')
  })

  it('sends the change once the person has signed in again, and stops asking', async () => {
    const shown = show(person(10 * 60 * 1000), routes({ 'POST /api/ops/mints': { body: ENTITLEMENT } }))
    await screen.findByRole('heading', { name: 'Terms and keys' })
    fillMint()
    send('Mint')
    await screen.findByRole('alert')
    expect(shown.sent('POST', '/api/ops/mints')).toHaveLength(0)

    fireEvent.click(screen.getByRole('button', { name: 'Sign in again' }))

    // The same change, sent again by the person: the fields it was going to
    // send are still there, because nothing landed and nothing was cleared,
    // and the prompt is gone because it has been answered. That the send below
    // goes through at all is the point of the case: the sign-in answered the
    // session with a fresh instant through the port's own watch, so a guard
    // still reading the old one would refuse this second send too.
    await waitFor(() => {
      expect(screen.queryByText(/five minutes/)).toBeNull()
    })
    send('Mint')
    await waitFor(() => {
      expect(shown.sent('POST', '/api/ops/mints')).toHaveLength(1)
    })
    expect(shown.sent('POST', '/api/ops/mints')[0]?.body).toEqual({
      workspace_id: 'ws_example',
      row: 'team-monthly',
      seats: 5,
      ends_at: '2026-03-10T00:00:00.000Z',
      check_interval: 'P7D',
    })
  })

  it('sends a mint under a sign-in of the last five minutes, in the minter’s own shape', async () => {
    const shown = show(person(0), routes({ 'POST /api/ops/mints': { body: ENTITLEMENT } }))
    await screen.findByRole('heading', { name: 'Terms and keys' })
    fillMint()

    send('Mint')

    await waitFor(() => {
      expect(shown.sent('POST', '/api/ops/mints')).toHaveLength(1)
    })
    expect(shown.sent('POST', '/api/ops/mints')[0]?.body).toEqual({
      workspace_id: 'ws_example',
      row: 'team-monthly',
      seats: 5,
      ends_at: '2026-03-10T00:00:00.000Z',
      check_interval: 'P7D',
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

    send('Mint')

    await screen.findByText('This sign-in is older than five minutes, so it cannot mint.')
    // A change the API refused did not land, and a form that said it had would
    // be telling the operator to stop when there is still a change to send.
    expect(screen.queryByText(/The term is granted/)).toBeNull()
  })

  it('says what landed, in the action’s own words, and clears the form it sent', async () => {
    const shown = show(
      person(0),
      routes({
        'POST /api/ops/mints': { body: ENTITLEMENT },
        'POST /api/ops/entitlement-actions': { body: ENTITLEMENT },
      }),
    )
    await screen.findByRole('heading', { name: 'Terms and keys' })
    fillMint()

    send('Mint')

    // The API's answer is the only thing that says a change arrived, and the
    // fields it sent are emptied with it: a form that kept them would send the
    // same term again on a second press.
    expect(await screen.findByText(/The term is granted/)).toBeDefined()
    expect((screen.getByLabelText('Workspace') as HTMLInputElement).value).toBe('')

    fireEvent.change(screen.getByLabelText('Entitlement'), { target: { value: 'ent_1' } })
    fireEvent.click(screen.getByRole('button', { name: 'Revoke' }))

    // The sentence names the action that was sent: the four buttons share one
    // form, so "a change landed" would leave the operator guessing which one.
    expect(
      await screen.findByText('The term is revoked, with every key it ever minted (F29).'),
    ).toBeDefined()
    expect(shown.sent('POST', '/api/ops/entitlement-actions')[0]?.body).toEqual({
      entitlement_id: 'ent_1',
      action: 'revoke',
      seats: null,
      ends_at: null,
      reason: null,
    })
    // The id went with the change, so the next press has nothing to send until
    // a person names a term again.
    expect((screen.getByLabelText('Entitlement') as HTMLInputElement).value).toBe('')
    expect(screen.getByRole('button', { name: 'Revoke' }).hasAttribute('disabled')).toBe(true)
  })

  it('decides a parked event from its own facts, and never draws the body it keeps', async () => {
    const shown = show(person(0), routes({ 'POST /api/ops/events/resolutions': { body: EVENT } }))
    await screen.findByRole('heading', { name: 'The queue' })
    expect(await screen.findByText(/evt_1/)).toBeDefined()
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

  it('attaches a parked event only once the term to attach it to is named', async () => {
    const shown = show(person(0), routes({ 'POST /api/ops/events/resolutions': { body: EVENT } }))
    await screen.findByRole('heading', { name: 'The queue' })
    await screen.findByText(/evt_1/)

    // The field the attach reads is empty, and empty is not the null a
    // dismissal sends: it would name a term nobody has, so the button that
    // would send it is not offered.
    fireEvent.click(screen.getByRole('button', { name: 'Attach' }))
    expect(screen.getByRole('button', { name: 'Attach' }).hasAttribute('disabled')).toBe(true)

    fireEvent.change(screen.getByLabelText('Entitlement to attach to'), {
      target: { value: 'ent_1' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Attach' }))

    await waitFor(() => {
      expect(shown.sent('POST', '/api/ops/events/resolutions')).toHaveLength(1)
    })
    expect(shown.sent('POST', '/api/ops/events/resolutions')[0]?.body).toEqual({
      id: 'evt_1',
      decision: 'attach',
      entitlement_id: 'ent_1',
    })
  })

  it('loads the month again when Show is pressed after a refusal', async () => {
    const replies = routes({
      'GET /api/ops/ledger': refusal(503, 'unavailable', 'The ledger is not answering right now.'),
    })
    show(person(0), replies)
    await screen.findByText('The ledger is not answering right now.')

    // The refusal is the load's end, not a pause in it: a section that said
    // "Loading…" beside the sentence would be telling the operator to wait for
    // a month that is never coming.
    expect(within(screen.getByRole('region', { name: 'The ledger' })).queryByText('Loading…')).toBeNull()

    // The month the form holds is the month that was just refused, so a Show
    // that did nothing but clear the sentence would leave the section loading
    // for ever: the load has to run again.
    replies['GET /api/ops/ledger'] = { body: [ENTRY] }
    send('Show the ledger')

    expect(await screen.findByText(/24.00 USD/)).toBeDefined()
    expect(screen.queryByText('The ledger is not answering right now.')).toBeNull()
  })

  it('loads the report again when Show is pressed after a refusal', async () => {
    const replies = routes({
      'GET /api/ops/reports': refusal(503, 'unavailable', 'The reports are not answering right now.'),
    })
    show(person(0), replies)
    await screen.findByText('The reports are not answering right now.')

    replies['GET /api/ops/reports'] = { body: MONTH }
    send('Show the report')

    expect(await screen.findByText(/24.00 direct/)).toBeDefined()
    expect(screen.queryByText('The reports are not answering right now.')).toBeNull()
  })

  it('asks for the parked events again when the queue is refused', async () => {
    const replies = routes({
      'GET /api/ops/events': refusal(503, 'unavailable', 'The queue is not answering right now.'),
    })
    const shown = show(person(0), replies)
    await screen.findByText('The queue is not answering right now.')
    expect(within(screen.getByRole('region', { name: 'The queue' })).queryByText('Loading…')).toBeNull()

    // The queue is read once, so the sentence is the only thing left to press:
    // without it a refused read is a queue nobody decides until a reload, and a
    // reload is what loses the form the operator was in the middle of.
    replies['GET /api/ops/events'] = { body: [EVENT] }
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))

    expect(await screen.findByText(/evt_1/)).toBeDefined()
    expect(screen.queryByText('The queue is not answering right now.')).toBeNull()
    expect(shown.sent('GET', '/api/ops/events')).toHaveLength(2)
  })

  it('reads the audit log again when it is refused', async () => {
    const replies = routes({
      'GET /api/ops/audit': refusal(503, 'unavailable', 'The audit log is not answering right now.'),
    })
    const shown = show(person(0), replies)
    await screen.findByText('The audit log is not answering right now.')
    expect(
      within(screen.getByRole('region', { name: 'The audit log' })).queryByText('Loading…'),
    ).toBeNull()

    replies['GET /api/ops/audit'] = { body: [AUDIT] }
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))

    expect(await screen.findByText(/entitlement.revoke/)).toBeDefined()
    expect(screen.queryByText('The audit log is not answering right now.')).toBeNull()
    expect(shown.sent('GET', '/api/ops/audit')).toHaveLength(2)
  })

  it('reads a customer’s records, and every id an action would name', async () => {
    const shown = show(person(0), routes())
    await screen.findByRole('heading', { name: 'Look up a customer' })

    fireEvent.change(screen.getByLabelText('Organisation, workspace or project id'), {
      target: { value: 'example' },
    })
    send('Look up')

    expect(await screen.findByText(/Example Ltd/)).toBeDefined()
    expect(shown.sent('GET', '/api/ops/organisations')[0]?.query.get('q')).toBe('example')

    fireEvent.click(screen.getByRole('button', { name: 'Show the organisation' }))

    // The point of the reads: every id the other sections send is found here,
    // and a key is drawn as its metadata and never as a secret (F5).
    expect(await screen.findByText(/ws_example/)).toBeDefined()
    expect(await screen.findByText(/key_1/)).toBeDefined()
    expect(shown.sent('GET', '/api/ops/workspaces')[0]?.query.get('organisation_id')).toBe(
      'org_example',
    )
    expect(shown.sent('GET', '/api/ops/keys')[0]?.query.get('workspace_id')).toBe('ws_example')
    expect(screen.getByRole('heading', { name: 'Members' })).toBeDefined()
  })

  it('sends a change, a transfer and the lists in the API’s own shapes', async () => {
    const shown = show(
      person(0),
      routes({
        'POST /api/ops/entitlement-actions': { body: ENTITLEMENT },
        'POST /api/ops/project-transfers': { body: WORKSPACE },
        'PUT /api/ops/lists': { body: LISTS },
      }),
    )
    await screen.findByRole('heading', { name: 'Terms and keys' })

    fireEvent.change(screen.getByLabelText('Entitlement'), { target: { value: 'ent_1' } })
    fireEvent.change(screen.getByLabelText('New seats'), { target: { value: '8' } })
    fireEvent.click(screen.getByRole('button', { name: 'Change seats' }))
    await waitFor(() => {
      expect(shown.sent('POST', '/api/ops/entitlement-actions')).toHaveLength(1)
    })
    // An end is read by `extend` alone, so the other three actions carry none:
    // a body that sent one would be a field the reader ignores.
    expect(shown.sent('POST', '/api/ops/entitlement-actions')[0]?.body).toEqual({
      entitlement_id: 'ent_1',
      action: 'seats',
      seats: 8,
      ends_at: null,
      reason: null,
    })

    fireEvent.change(screen.getByLabelText('Project id'), {
      target: { value: 'example-portal' },
    })
    fireEvent.change(screen.getByLabelText('Workspace that keeps it'), {
      target: { value: 'ws_example' },
    })
    send('Transfer the project id')
    await waitFor(() => {
      expect(shown.sent('POST', '/api/ops/project-transfers')).toHaveLength(1)
    })
    expect(shown.sent('POST', '/api/ops/project-transfers')[0]?.body).toEqual({
      project_id: 'example-portal',
      workspace_id: 'ws_example',
      reason: null,
    })

    // The directories go out as `microsoft_directories`, the amended D2207's
    // name for them: a body carrying the old `microsoft_tenants` is one the API
    // refuses, and the two lists are the only place that name is sent at all.
    fireEvent.change(screen.getByLabelText('Email domains'), {
      target: { value: 'example.com\nexample.org' },
    })
    fireEvent.change(screen.getByLabelText('Microsoft directories'), {
      target: { value: 'a-directory-id' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save the lists' }))
    await waitFor(() => {
      expect(shown.sent('PUT', '/api/ops/lists')).toHaveLength(1)
    })
    expect(shown.sent('PUT', '/api/ops/lists')[0]?.body).toEqual({
      email_domains: ['example.com', 'example.org'],
      microsoft_directories: ['a-directory-id'],
    })
  })

  it('says the audit log it draws is only the newest page, and not the whole log', async () => {
    show(person(0), routes())
    await screen.findByRole('heading', { name: 'The audit log' })

    expect(await screen.findByText(/newest page/)).toBeDefined()
    // The entry is drawn under the sentence, so a truncated log is never read
    // as the whole of what operations did (D456).
    expect(screen.getByText(/ops@example\.com/)).toBeDefined()
  })

  it('sends no check interval for an air-gapped term, and waits for one otherwise', async () => {
    const shown = show(person(0), routes({ 'POST /api/ops/mints': { body: ENTITLEMENT } }))
    await screen.findByRole('heading', { name: 'Terms and keys' })

    fireEvent.change(screen.getByLabelText('Workspace'), { target: { value: 'ws_example' } })
    fireEvent.change(screen.getByLabelText('Row'), { target: { value: 'team-monthly' } })
    fireEvent.change(screen.getByLabelText('Seats'), { target: { value: '5' } })
    fireEvent.change(screen.getByLabelText('Ends'), { target: { value: '2026-03-10' } })

    // A null interval is the air-gapped row and nothing else (F28), so an empty
    // field is not a term that never checks in: the button waits for the
    // operator to say which of the two they mean.
    expect(screen.getByRole('button', { name: 'Mint' }).hasAttribute('disabled')).toBe(true)

    fireEvent.click(screen.getByLabelText(/air-gapped row/))
    send('Mint')

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

  it('offers each change only once the field that change reads is filled', async () => {
    show(person(0), routes())
    await screen.findByRole('heading', { name: 'Terms and keys' })

    const offered = (name: string): boolean =>
      !screen.getByRole('button', { name }).hasAttribute('disabled')

    // No term is named, so no action is offered at all.
    for (const name of ['Extend', 'Change seats', 'Comp', 'Revoke']) {
      expect(offered(name)).toBe(false)
    }

    fireEvent.change(screen.getByLabelText('Entitlement'), { target: { value: 'ent_1' } })

    // A term alone is enough for the two actions that read nothing else, and
    // not for the two that read a field of their own: `0` seats and an absent
    // end are bodies the API refuses, and the form is where that is visible.
    expect(offered('Comp')).toBe(true)
    expect(offered('Revoke')).toBe(true)
    expect(offered('Extend')).toBe(false)
    expect(offered('Change seats')).toBe(false)

    fireEvent.change(screen.getByLabelText('New seats'), { target: { value: '8' } })
    expect(offered('Change seats')).toBe(true)
    expect(offered('Extend')).toBe(false)

    fireEvent.change(screen.getByLabelText('New end'), { target: { value: '2026-04-10' } })
    expect(offered('Extend')).toBe(true)
  })

  it('refuses the screens to a caller who is not in the operations organisation', async () => {
    show(person(0), routes({ 'GET /api/ops/membership': refusal(403, 'not_operations', 'This account is not in the operations organisation.') }))

    await screen.findByText('This account is not in the operations organisation.')
    expect(screen.queryByRole('heading', { name: 'The queue' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Mint' })).toBeNull()
  })
})
