// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The workspace screen (portal plan P3.4; spec §Workspaces, keys and licences).
//
// Three cases here are the plan's own: the revealed bundle is shown once and is
// gone when the person navigates away and back, an unbound entitlement says how
// to bind it, and a licence that changed shows the refresh line. The rest pin
// the parts of the plan's sentence a reader would otherwise take on trust --
// the seats and the renewal date, the air-gapped file instead of a reveal, the
// trial and open-source buttons, and a refusal shown as the API wrote it.
//
// **The bundle case is the one that matters.** A key and its renewal secret are
// the only credential the portal ever hands a browser, and the failure it
// guards is a screen that keeps them: in a module variable, in sessionStorage,
// or by asking the API for the reveal again on the next mount. So that case
// navigates rather than reloading, and it counts the calls -- one reveal, and
// none on the way back.
//
// **The API is a fake `ApiClient`, and it is the seam.** Every request the app
// makes goes through `src/api/client.ts`; what this suite has to see is the
// path and the body this screen sends, not the transport, so the fake answers
// the paths below and refuses anything else. Nothing here reaches the network,
// and the two credential-shaped values are fixture text that is not a key.

import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { API_BASE, ApiError } from '../../api/client'
import type { ApiCall, ApiClient } from '../../api/client'
import type { Entitlement } from '../../api/types/entitlements'
import type { Workspace, WorkspaceBind } from '../../api/types/workspaces'
import { SessionProvider } from '../../auth/session'
import type { AuthPort, SignedInUser } from '../../auth/session'
import { route } from './route'
import { WorkspacesScreen } from './workspaces'

afterEach(cleanup)

const BOUND_AT = '2026-02-11T08:00:00Z'

// Fixture text, and deliberately not shaped like the credentials it stands in
// for: a key is `fgk_…` and a secret is 32 bytes, and neither belongs in a file
// a person reads.
const KEY = 'a-fixture-value-that-is-not-a-licence-key'
const SECRET = 'a-fixture-value-that-is-not-the-renewal-secret'
// The second workspace's, because a reveal rotates the secret and answers once
// per workspace per day (P3.2): a screen that kept only the last one would
// throw away the other workspace's only copy of its new key.
const SECOND_KEY = 'another-fixture-value-that-is-not-a-licence-key'
const SECOND_SECRET = 'another-fixture-value-that-is-not-the-renewal-secret'
const LICENCE = 'a-fixture-value-that-is-not-a-licence-file'

const REVEALS: Record<string, { key: string; renewal_secret: string }> = {
  ws_example: { key: KEY, renewal_secret: SECRET },
  ws_second: { key: SECOND_KEY, renewal_secret: SECOND_SECRET },
}

function unbound(): Workspace {
  return {
    id: 'ws_example',
    organisation_id: 'org_example',
    name: 'Example portal',
    project_id: null,
    entitlement_id: 'ent_1',
    created_at: '2026-02-10T11:00:00Z',
    bound_at: null,
  }
}

function bound(): Workspace {
  return { ...unbound(), project_id: 'example-portal', bound_at: BOUND_AT }
}

function second(): Workspace {
  return { ...bound(), id: 'ws_second', name: 'Second portal', project_id: 'second-portal' }
}

function paid(): Entitlement {
  return {
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
    bound_at: null,
    rebindable_at: null,
  }
}

function secondPaid(): Entitlement {
  return { ...paid(), id: 'ent_2', workspace_id: 'ws_second' }
}

interface Portal {
  workspaces: Workspace[]
  entitlements: Entitlement[]
}

interface Call {
  path: string
  method: string
  body: unknown
}

interface Refusal {
  status: number
  code: string
  message: string
}

/**
 * The API this screen talks to: the two lists it reads, the four acts it asks
 * for, and a 404 for anything else -- a path this screen reaches that the
 * routes below do not name is a screen that would fail against the real API,
 * and it fails here instead.
 *
 * Binding mutates the state the two lists are answered from, because that is
 * what the API does: the screen reloads afterwards rather than being handed a
 * patched record, and a fake that answered the new state without the reload
 * would hide a screen that never reads it.
 */
function portal(state: Portal, refusals: Record<string, Refusal> = {}) {
  const calls: Call[] = []
  const request = async <T,>(path: string, call: ApiCall = {}): Promise<T> => {
    const method = call.method ?? 'GET'
    // The base the real client joins every path to: the routes below are the
    // API's own (`/api/…`, the prefix Hosting rewrites), so a screen that wrote
    // a path outside it would be reaching an origin nothing serves.
    const wire = `${API_BASE}${path}`
    calls.push({ path: wire, method, body: call.body })
    const refusal = refusals[`${method} ${wire}`]
    if (refusal !== undefined) {
      throw new ApiError(refusal.status, { code: refusal.code, message: refusal.message })
    }
    return answer(state, method, wire, call.body) as T
  }
  const client: ApiClient = { request }
  return { client, calls }
}

function answer(state: Portal, method: string, path: string, body: unknown): unknown {
  const pathname = new URL(path, 'https://app.fugaro.dev').pathname
  if (method === 'GET' && pathname === '/api/workspaces') {
    return { workspaces: state.workspaces }
  }
  if (method === 'GET' && pathname === '/api/entitlements') {
    return { entitlements: state.entitlements }
  }
  if (method === 'POST' && pathname === '/api/workspaces/bind') {
    const bind = body as WorkspaceBind
    for (const workspace of state.workspaces) {
      if (workspace.entitlement_id === bind.entitlement_id) {
        workspace.project_id = bind.project_id
        workspace.bound_at = BOUND_AT
      }
    }
    for (const entitlement of state.entitlements) {
      if (entitlement.id === bind.entitlement_id) {
        entitlement.bound_at = BOUND_AT
      }
    }
    return undefined
  }
  if (method === 'POST' && pathname === '/api/keys/reveal') {
    const reveal = body as { workspace_id: string }
    const revealed = REVEALS[reveal.workspace_id]
    if (revealed === undefined) {
      throw new ApiError(404, {
        code: 'not_found',
        message: `This suite has no key for ${reveal.workspace_id}.`,
      })
    }
    return {
      key_id: `key_${reveal.workspace_id}`,
      key: revealed.key,
      renewal_secret: revealed.renewal_secret,
      paid_through: '2026-03-10T11:00:00Z',
    }
  }
  if (method === 'GET' && pathname === '/api/keys/licence') {
    return { key_id: 'key_1', licence: LICENCE, paid_through: '2026-03-10T11:00:00Z' }
  }
  if (method === 'POST' && (pathname === '/api/trial' || pathname === '/api/opensource')) {
    return undefined
  }
  throw new ApiError(404, {
    code: 'not_found',
    message: `This suite has no route for ${method} ${pathname}.`,
  })
}

/**
 * The screen, on a router with one other path to walk to and a probe that says
 * where the router thinks it is -- which is what the refresh marker's case
 * reads to see the marker spent.
 */
function show(client: ApiClient, at = '/workspaces'): void {
  render(
    <MemoryRouter initialEntries={[at]}>
      <Routes>
        <Route
          path="/workspaces"
          element={
            <>
              <Leave />
              <Where />
              <WorkspacesScreen client={client} />
            </>
          }
        />
        <Route path="/signin" element={<Return />} />
      </Routes>
    </MemoryRouter>,
  )
}

function Where() {
  const location = useLocation()
  return <p>{`the router is at ${location.pathname}${location.search}`}</p>
}

function Leave() {
  const navigate = useNavigate()
  return (
    <button type="button" onClick={() => void navigate('/signin')}>
      Go elsewhere
    </button>
  )
}

function Return() {
  const navigate = useNavigate()
  return (
    <button type="button" onClick={() => void navigate('/workspaces')}>
      Come back
    </button>
  )
}

function card(name: string): HTMLElement {
  return screen.getByRole('region', { name })
}

interface WireCall {
  url: string
  method: string
  headers: Record<string, string>
}

/** A response the client can read, without a real `fetch` behind it. */
function reply(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response
}

/**
 * The transport the route's *own* client is built over. The suite's other cases
 * hand the screen an `ApiClient`; the route makes its own from the session, so
 * what a route case has to watch is `fetch` itself.
 *
 * A request with no bearer token is refused with 401, which is what the API
 * does. That is the whole point of the two cases below: the answer to a request
 * made before the session was restored is a refusal, and a screen that showed
 * it would be showing a person a wall for something that was only slow.
 */
function wire(state: Portal): WireCall[] {
  const calls: WireCall[] = []
  const fetch = ((url: string, init: RequestInit = {}) => {
    const headers = (init.headers ?? {}) as Record<string, string>
    const method = init.method ?? 'GET'
    calls.push({ url, method, headers })
    if (headers.Authorization === undefined) {
      return Promise.resolve(
        reply(401, { error: { code: 'unauthenticated', message: 'Sign in to continue.' } }),
      )
    }
    const body = typeof init.body === 'string' ? JSON.parse(init.body) : undefined
    return Promise.resolve(reply(200, answer(state, method, url, body)))
  }) as unknown as typeof globalThis.fetch
  vi.spyOn(globalThis, 'fetch').mockImplementation(fetch)
  return calls
}

/**
 * A session that answers when the test says so, which is what a full page load
 * is: Firebase restores `currentUser` asynchronously, so the first render
 * happens with nobody signed in and `idToken()` answers null until it does
 * (`src/auth/firebase.ts`).
 */
function pending(token: string) {
  const person: SignedInUser = {
    uid: 'uid_example',
    email: 'person@example.com',
    provider: 'google.com',
    authTime: 1,
  }
  let current: SignedInUser | null = null
  let answer: ((user: SignedInUser | null) => void) | null = null
  const port: AuthPort = {
    watch: (onUser) => {
      answer = onUser
      return () => {
        answer = null
      }
    },
    idToken: () => Promise.resolve(current === null ? null : token),
    signIn: () => Promise.resolve(),
    sendSignInLink: () => Promise.resolve(),
    isSignInLink: () => false,
    completeSignInLink: () => Promise.resolve(),
    signOut: () => Promise.resolve(),
  }
  function restore(user: SignedInUser | null): void {
    current = user
    act(() => {
      answer?.(user)
    })
  }
  return {
    port,
    signedIn: () => restore(person),
    signedOut: () => restore(null),
  }
}

/** The route itself, mounted the way `src/app.tsx` mounts it. */
function showRoute(session: ReturnType<typeof pending>): void {
  render(
    <SessionProvider port={session.port}>
      <MemoryRouter initialEntries={['/workspaces']}>
        <Routes>
          <Route path={route.path} element={route.element} />
          <Route path="/signin" element={<p>the sign-in screen</p>} />
        </Routes>
      </MemoryRouter>
    </SessionProvider>,
  )
}

describe('the workspace route', () => {
  it('mounts the screen only once it knows who is signed in', async () => {
    const session = pending('a-fixture-id-token')
    const calls = wire({ workspaces: [bound()], entitlements: [paid()] })
    showRoute(session)

    // Nothing may be asked for while the session is still being restored: a
    // request without the token is one the API refuses, and the screen's read
    // runs once over a client that never changes, so that refusal would be
    // what the person is left looking at.
    await act(async () => {})
    expect(calls).toHaveLength(0)

    session.signedIn()

    const workspace = await screen.findByRole('region', { name: 'Example portal' })
    expect(within(workspace).getByText('example-portal')).toBeDefined()
    expect(calls.map((call) => call.url)).toEqual(['/api/workspaces', '/api/entitlements'])
    for (const call of calls) {
      expect(call.headers.Authorization).toBe('Bearer a-fixture-id-token')
    }
  })

  it('sends a visitor with no session to the sign-in screen', async () => {
    const session = pending('a-fixture-id-token')
    const calls = wire({ workspaces: [bound()], entitlements: [paid()] })
    showRoute(session)

    session.signedOut()

    expect(await screen.findByText('the sign-in screen')).toBeDefined()
    expect(calls).toHaveLength(0)
  })
})

describe('the workspace screen', () => {
  it('shows a workspace’s licence, its seats and its renewal date', async () => {
    const { client } = portal({ workspaces: [bound()], entitlements: [paid()] })
    show(client)

    const workspace = await screen.findByRole('region', { name: 'Example portal' })
    expect(within(workspace).getByText('example-portal')).toBeDefined()
    expect(within(workspace).getByText(/3 seats/)).toBeDefined()
    expect(within(workspace).getByText(/renews on 10 March 2026/)).toBeDefined()
    expect(screen.queryByText(/fugaro licence refresh/)).toBeNull()
  })

  it('an unbound entitlement shows how to bind', async () => {
    const { client } = portal({ workspaces: [unbound()], entitlements: [paid()] })
    show(client)

    const workspace = await screen.findByRole('region', { name: 'Example portal' })
    expect(within(workspace).getByText('No Firebase project yet')).toBeDefined()

    const pending = screen.getByRole('region', { name: 'Licences not bound yet' })
    expect(within(pending).getByText(/is not bound yet/)).toBeDefined()
    expect(within(pending).getByText(/fugaro setup/)).toBeDefined()
    expect(within(pending).getByLabelText('Firebase project id')).toBeDefined()
  })

  it('binds the entitlement to the project id a person types', async () => {
    const { client, calls } = portal({ workspaces: [unbound()], entitlements: [paid()] })
    show(client)

    const pending = await screen.findByRole('region', { name: 'Licences not bound yet' })
    fireEvent.change(within(pending).getByLabelText('Firebase project id'), {
      target: { value: 'example-portal' },
    })
    fireEvent.click(within(pending).getByRole('button', { name: 'Bind' }))

    await waitFor(() => {
      expect(calls).toContainEqual({
        path: '/api/workspaces/bind',
        method: 'POST',
        body: { entitlement_id: 'ent_1', project_id: 'example-portal' },
      })
    })
    // The screen reads the state back rather than patching its own copy, so the
    // card is bound only once the reload has answered.
    await waitFor(() => {
      expect(within(card('Example portal')).getByText('example-portal')).toBeDefined()
    })
    expect(screen.queryByRole('region', { name: 'Licences not bound yet' })).toBeNull()
  })

  it('shows a licence granted outside any workspace, and binds it', async () => {
    // Sign-up's free seat is granted to the organisation before any workspace
    // exists (F6, D2184, D2199): the entitlement names no workspace, no card
    // can match it, and binding is the only way it ever reaches one.
    const { client, calls } = portal({
      workspaces: [],
      entitlements: [{ ...paid(), id: 'ent_org', workspace_id: null }],
    })
    show(client)

    const pending = await screen.findByRole('region', { name: 'Licences not bound yet' })
    expect(within(pending).getByText(/3 seats/)).toBeDefined()

    fireEvent.change(within(pending).getByLabelText('Firebase project id'), {
      target: { value: 'example-portal' },
    })
    fireEvent.click(within(pending).getByRole('button', { name: 'Bind' }))

    await waitFor(() => {
      expect(calls).toContainEqual({
        path: '/api/workspaces/bind',
        method: 'POST',
        body: { entitlement_id: 'ent_org', project_id: 'example-portal' },
      })
    })
    await waitFor(() => {
      expect(screen.queryByRole('region', { name: 'Licences not bound yet' })).toBeNull()
    })
  })

  it('rebinds a bound workspace to another project', async () => {
    const { client, calls } = portal({ workspaces: [bound()], entitlements: [paid()] })
    show(client)

    const workspace = await screen.findByRole('region', { name: 'Example portal' })
    fireEvent.change(within(workspace).getByLabelText('Firebase project id'), {
      target: { value: 'second-portal' },
    })
    fireEvent.click(within(workspace).getByRole('button', { name: 'Rebind' }))

    await waitFor(() => {
      expect(calls).toContainEqual({
        path: '/api/workspaces/bind',
        method: 'POST',
        body: { entitlement_id: 'ent_1', project_id: 'second-portal' },
      })
    })
    await waitFor(() => {
      expect(within(card('Example portal')).getByText('second-portal')).toBeDefined()
    })
  })

  it('shows the bundle once, and clears it when the person navigates away and back', async () => {
    const { client, calls } = portal({ workspaces: [bound()], entitlements: [paid()] })
    show(client)

    const workspace = await screen.findByRole('region', { name: 'Example portal' })
    fireEvent.click(within(workspace).getByRole('button', { name: 'Reveal the key' }))

    expect(await screen.findByText(KEY)).toBeDefined()
    expect(screen.getByText(SECRET)).toBeDefined()

    fireEvent.click(screen.getByRole('button', { name: 'Go elsewhere' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Come back' }))
    await screen.findByRole('region', { name: 'Example portal' })

    expect(screen.queryByText(KEY)).toBeNull()
    expect(screen.queryByText(SECRET)).toBeNull()
    expect(calls.filter((call) => call.path === '/api/keys/reveal')).toHaveLength(1)
  })

  it('keeps each workspace’s revealed key on its own card', async () => {
    const { client } = portal({
      workspaces: [bound(), second()],
      entitlements: [paid(), secondPaid()],
    })
    show(client)

    const first = await screen.findByRole('region', { name: 'Example portal' })
    fireEvent.click(within(first).getByRole('button', { name: 'Reveal the key' }))
    expect(await within(first).findByText(KEY)).toBeDefined()

    const other = card('Second portal')
    fireEvent.click(within(other).getByRole('button', { name: 'Reveal the key' }))
    expect(await within(other).findByText(SECOND_KEY)).toBeDefined()

    // A reveal rotates the secret and answers once per workspace per day (P3.2),
    // so the first workspace's bundle is the only copy of its new key there
    // will be until tomorrow: revealing the second must not take it off the
    // screen, and each key has to be on the card of the workspace it belongs to.
    expect(within(card('Example portal')).getByText(KEY)).toBeDefined()
    expect(within(card('Example portal')).getByText(SECRET)).toBeDefined()
    expect(within(card('Second portal')).getByText(SECOND_SECRET)).toBeDefined()
    expect(
      within(card('Second portal')).getByRole('heading', { name: 'The key for Second portal' }),
    ).toBeDefined()
  })

  it('copies the key it showed', async () => {
    const writeText = vi.fn(() => Promise.resolve())
    Object.defineProperty(window.navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { client } = portal({ workspaces: [bound()], entitlements: [paid()] })
    show(client)

    const workspace = await screen.findByRole('region', { name: 'Example portal' })
    fireEvent.click(within(workspace).getByRole('button', { name: 'Reveal the key' }))
    await screen.findByText(KEY)

    fireEvent.click(screen.getByRole('button', { name: 'Copy the key' }))
    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(KEY)
    })
  })

  it('offers the air-gapped file rather than a reveal', async () => {
    const { client } = portal({
      workspaces: [bound()],
      entitlements: [{ ...paid(), air_gapped: true }],
    })
    show(client)

    const workspace = await screen.findByRole('region', { name: 'Example portal' })
    expect(within(workspace).queryByRole('button', { name: 'Reveal the key' })).toBeNull()
    // An air-gapped key is never revoked, so D2206 refuses the rebind as well:
    // a second binding would mint a second copy of it for good.
    expect(within(workspace).queryByRole('button', { name: 'Rebind' })).toBeNull()
    expect(within(workspace).queryByLabelText('Firebase project id')).toBeNull()

    fireEvent.click(within(workspace).getByRole('button', { name: 'Get the licence file' }))

    const link = await screen.findByRole('link', { name: 'Download the licence file' })
    expect(link.getAttribute('download')).toBe('fugaro-licence.txt')
    expect(decodeURIComponent(link.getAttribute('href') ?? '')).toContain(LICENCE)
  })

  it('offers no key for a workspace whose licence has lapsed', async () => {
    // A workspace keeps its project after its licence goes, so the card still
    // names it -- and offers nothing that only a licence could answer with.
    const { client, calls } = portal({
      workspaces: [{ ...bound(), entitlement_id: null }],
      entitlements: [],
    })
    show(client)

    const workspace = await screen.findByRole('region', { name: 'Example portal' })
    expect(within(workspace).getByText('example-portal')).toBeDefined()
    expect(within(workspace).queryByRole('button', { name: 'Reveal the key' })).toBeNull()
    expect(within(workspace).queryByRole('button', { name: 'Get the licence file' })).toBeNull()
    expect(within(workspace).queryByRole('button', { name: 'Rebind' })).toBeNull()
    expect(within(workspace).getByRole('button', { name: 'Start a trial' })).toBeDefined()
    expect(calls.filter((call) => call.path === '/api/keys/reveal')).toHaveLength(0)
  })

  it('shows the refresh line after a licence changed, and spends the marker', async () => {
    const { client } = portal({ workspaces: [bound()], entitlements: [paid()] })
    show(client, '/workspaces?licence=changed')

    expect(await screen.findByText(/fugaro licence refresh/)).toBeDefined()
    // The marker was the way here and not a step of its own, so the address the
    // router holds no longer carries it: a reload, or a link back, would
    // otherwise tell somebody their purchase had just landed a second time.
    expect(await screen.findByText('the router is at /workspaces')).toBeDefined()
  })

  it('offers a trial to a workspace with nothing granted', async () => {
    const { client, calls } = portal({
      workspaces: [{ ...unbound(), entitlement_id: null }],
      entitlements: [],
    })
    show(client)

    const workspace = await screen.findByRole('region', { name: 'Example portal' })
    fireEvent.click(within(workspace).getByRole('button', { name: 'Start a trial' }))

    await waitFor(() => {
      expect(calls).toContainEqual({
        path: '/api/trial',
        method: 'POST',
        body: { workspace_id: 'ws_example' },
      })
    })
  })

  it('offers an open-source request to a workspace with nothing granted', async () => {
    const { client, calls } = portal({
      workspaces: [{ ...unbound(), entitlement_id: null }],
      entitlements: [],
    })
    show(client)

    const workspace = await screen.findByRole('region', { name: 'Example portal' })
    fireEvent.click(within(workspace).getByRole('button', { name: 'Request an open-source licence' }))

    await waitFor(() => {
      expect(calls).toContainEqual({
        path: '/api/opensource',
        method: 'POST',
        body: { workspace_id: 'ws_example' },
      })
    })
  })

  it('shows the sentence the API wrote for a refusal', async () => {
    const { client } = portal({ workspaces: [unbound()], entitlements: [paid()] }, {
      'POST /api/workspaces/bind': {
        status: 409,
        code: 'project_taken',
        message: 'That Firebase project is bound to another workspace.',
      },
    })
    show(client)

    const pending = await screen.findByRole('region', { name: 'Licences not bound yet' })
    fireEvent.change(within(pending).getByLabelText('Firebase project id'), {
      target: { value: 'example-portal' },
    })
    fireEvent.click(within(pending).getByRole('button', { name: 'Bind' }))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe('That Firebase project is bound to another workspace.')
  })
})
