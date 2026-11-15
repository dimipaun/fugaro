// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The two `licence login` suites' shared half (portal plan P3.5): the fixtures,
// the fake API and the fake session both pages are read through.
//
// It is one module rather than a copy in each suite because the two pages talk
// to one service: what they share is a contract -- `/entitlements`,
// `/workspaces/bind` and K2.4's `/cli/device` -- and two fakes of it would be
// two places that contract could drift, with a device page approving a licence
// the authorize page has never heard of. Nothing here is imported by a screen,
// so nothing here reaches the bundle.
//
// No fixture is shaped like a credential: the token, the codes and the names
// the screens handle are all text that is obviously not one (the plan's rule for
// this directory, and the reason a test secret is built rather than written).

import { vi } from 'vitest'
import { API_BASE, ApiError } from '../../api/client'
import type { ApiCall, ApiClient } from '../../api/client'
import type { Entitlement } from '../../api/types/entitlements'
import type { AuthPort, SignedInUser } from '../../auth/session'

/** The text a fake session answers as the ID token. Not a token: it is read by
 * nothing and it is here so a case can see it arrive where the service reads
 * it. */
export const ID_TOKEN = 'a-fixture-value-that-is-not-an-id-token'

/** The code a terminal would have printed. Eight characters of the alphabet
 * K2.4 draws a code from, and obviously a fixture rather than one. */
export const TYPED_CODE = 'TESTCODE'

/** What K2.4 answers for that code: the code as the service read it, in its two
 * groups -- which is not the text that was typed, and is what the approval
 * carries. */
export const READ_CODE = 'TEST-CODE'

/** The installation the code belongs to -- its Firebase project id (K3.3), and
 * the name both pages draw as untrusted text. */
export const INSTALLATION = 'example-portal'

export const ORGANISATION = 'Example Ltd'

export function signedIn(): SignedInUser {
  return {
    uid: 'uid_example',
    email: 'owner@example.test',
    provider: 'google.com',
    authTime: Date.parse('2026-02-11T08:00:00Z'),
  }
}

/**
 * The session port, with whoever the case says is signed in.
 *
 * It answers the person at once rather than on a later tick: the provider
 * subscribes in an effect, so the answer is a state update in the mount the
 * effect runs in, and a case does not have to wait for a timer to see a screen.
 * A case that has to watch the gate itself uses `session()` instead.
 */
export function authPort(user: SignedInUser | null, token: string | null = ID_TOKEN): AuthPort {
  return {
    watch: (onUser) => {
      onUser(user)
      return () => undefined
    },
    idToken: () => Promise.resolve(token),
    signIn: () => Promise.resolve(),
    sendSignInLink: () => Promise.resolve(),
    isSignInLink: () => false,
    completeSignInLink: () => Promise.resolve(),
    signOut: () => Promise.resolve(),
  }
}

/**
 * A session that answers when the case says so, which is what a full page load
 * is: Firebase restores `currentUser` asynchronously, so the first render has
 * nobody signed in and `idToken()` answers null until it does. `signedIn()` and
 * `signedOut()` are that answer arriving, and they are the only ways a case
 * moves this session -- which is how the route's two gates are watched.
 */
export function session(token: string = ID_TOKEN) {
  const person = signedIn()
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
    answer?.(user)
  }
  return { port, signedIn: () => restore(person), signedOut: () => restore(null) }
}

/** A licence the organisation holds, granted and running. */
export function pending(): Entitlement {
  return {
    id: 'ent_example',
    organisation_id: 'org_example',
    workspace_id: null,
    source: 'purchase',
    row: 'team-monthly',
    tier: 'team',
    kind: 'paid',
    seats: 3,
    starts_at: '2026-02-01T09:00:00Z',
    ends_at: '2026-03-01T09:00:00Z',
    status: 'active',
    air_gapped: false,
    created_at: '2026-02-01T09:00:00Z',
    bound_at: null,
    rebindable_at: null,
  }
}

/** A second one, for the case where the person has a choice to make. */
export function secondPending(): Entitlement {
  return { ...pending(), id: 'ent_second', row: 'team-annual', seats: 10 }
}

/** A licence already bound to an installation, which neither page offers. */
export function alreadyBound(): Entitlement {
  return {
    ...pending(),
    id: 'ent_bound',
    workspace_id: 'ws_example',
    bound_at: '2026-02-02T09:00:00Z',
  }
}

/** A licence that is over, which neither page offers either. */
export function expired(): Entitlement {
  return { ...pending(), id: 'ent_expired', status: 'expired' }
}

export interface Call {
  path: string
  method: string
  body: unknown
}

/** What a case refuses, keyed by `<METHOD> <path>`, the path with the API base
 * the real client joins every one of them to. */
export interface Refusal {
  status: number
  code: string
  message: string
}

export interface PortalOptions {
  refusals?: Record<string, Refusal>
  /** The name K2.4 answers for a code, for the case where it is a string no
   * page may draw as markup. */
  installation?: string
  organisation?: string
}

export interface Portal {
  client: ApiClient
  calls: Call[]
  /** The codes an approval was sent for, in the order they were sent. */
  approved: string[]
}

/**
 * The API both pages talk to: the organisation's licences, the binding, and
 * K2.4's device route in its two readings -- the code alone is the lookup, the
 * code with `confirm` is the approval. Anything else is a 404, because a path a
 * page reaches that this fake does not name is a page that would fail against
 * the real API.
 *
 * Binding mutates the licence it names, as the API does: a page that showed a
 * licence as bound without the API saying so would be a page inventing state.
 */
export function portalApi(
  licences: Entitlement[],
  options: PortalOptions = {},
): Portal {
  const calls: Call[] = []
  const approved: string[] = []
  const request = async <T,>(path: string, call: ApiCall = {}): Promise<T> => {
    const method = call.method ?? 'GET'
    // The base the real client joins every path to: a page that wrote a path
    // outside it would be reaching an origin nothing serves, and the wire path
    // is what the routes below name.
    const wire = `${API_BASE}${path}`
    calls.push({ path: wire, method, body: call.body })
    const refusal = options.refusals?.[`${method} ${wire}`]
    if (refusal !== undefined) {
      throw new ApiError(refusal.status, { code: refusal.code, message: refusal.message })
    }
    return answer(licences, approved, options, method, wire, call.body) as T
  }
  return { client: { request }, calls, approved }
}

function answer(
  licences: Entitlement[],
  approved: string[],
  options: PortalOptions,
  method: string,
  path: string,
  body: unknown,
): unknown {
  const pathname = new URL(path, 'https://app.fugaro.dev').pathname
  if (method === 'GET' && pathname === `${API_BASE}/entitlements`) {
    return { entitlements: licences }
  }
  if (method === 'POST' && pathname === `${API_BASE}/workspaces/bind`) {
    const bind = body as { entitlement_id: string; project_id: string }
    for (const entitlement of licences) {
      if (entitlement.id === bind.entitlement_id) {
        entitlement.workspace_id = `ws_${bind.project_id}`
        entitlement.bound_at = '2026-02-11T08:00:00Z'
      }
    }
    return undefined
  }
  if (method === 'POST' && pathname === `${API_BASE}/cli/device`) {
    const call = body as { user_code: string; confirm?: boolean }
    if (call.confirm === true) {
      approved.push(call.user_code)
      return undefined
    }
    return {
      installation: options.installation ?? INSTALLATION,
      organisation: options.organisation ?? ORGANISATION,
      user_code: READ_CODE,
    }
  }
  throw new ApiError(404, {
    code: 'not_found',
    message: `This suite has no route for ${method} ${pathname}.`,
  })
}

export interface WireCall {
  url: string
  method: string
  headers: Record<string, string>
}

/** A response the client can read, without a real `fetch` behind it. */
export function reply(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response
}

/**
 * The transport the route's *own* client is built over. A screen case hands the
 * screen an `ApiClient`; the route makes its own from the session, so what a
 * route case has to watch is `fetch` itself.
 *
 * A request with no bearer token is refused with 401, which is what the API
 * does: the answer to a request made before the session was restored is a
 * refusal, and a page that showed it would be showing a person a wall for
 * something that was only slow.
 */
export function wire(licences: Entitlement[], options: PortalOptions = {}): WireCall[] {
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
    try {
      return Promise.resolve(reply(200, answer(licences, [], options, method, url, body)))
    } catch (error: unknown) {
      return Promise.resolve(
        error instanceof ApiError
          ? reply(error.status, { error: { code: error.code, message: error.message } })
          : reply(500, { error: { code: 'internal', message: 'This suite broke.' } }),
      )
    }
  }) as unknown as typeof globalThis.fetch
  vi.spyOn(globalThis, 'fetch').mockImplementation(fetch)
  return calls
}
