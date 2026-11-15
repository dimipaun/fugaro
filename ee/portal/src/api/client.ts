// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The portal's one way to reach the API (portal plan P1.4; D2188, F20). Every
// screen goes through this client, and the SPA reaches data no other way: the
// design's rule is that Firestore's rules deny every client (P1.5), so a
// request that did not come from here would be one the database refuses
// anyway.
//
// **The base is a constant and not a setting.** The API is reached on the
// portal's own origin, where Hosting rewrites `/api/**` to the Cloud Run
// service (P1.0), and a configurable base would be a seam that moves every
// request, credentials included, to whatever host the value named. A path is
// therefore checked before it is joined (`apiPath`): a protocol-relative path
// (`//elsewhere.example/x`) or an absolute one is refused here rather than
// being fetched, because `fetch` would happily send the ID token to that host.
//
// **The token is asked for at every request and never held.** The alternative
// -- read it once when the client is built -- keeps sending a token that
// `signOut` has already invalidated, and a stale token is not a small mistake:
// it is a request that succeeds for somebody who has signed out. So the client
// holds a function, and the session is what answers it.
//
// **A refusal is decoded into one shape, and anything else is a transport
// failure.** The API answers every failure with `error.schema.json`'s body
// (P1.0's uniform error shape); a body that is not that shape -- an HTML error
// page from a proxy, a truncated answer -- is reported as one generic sentence
// and is never shown to a reader as though the API had said it. Nothing here
// logs a body, a header or a token.

import type { ApiRefusal, Fault } from './types/error'

/** The one path prefix the API answers on. */
export const API_BASE = '/api'

/** The methods this client sends. A method outside the set is not a typo a
 * person makes; it is a caller reaching for something the API does not offer. */
export type ApiMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export interface ApiCall {
  method?: ApiMethod
  /** Serialized as JSON. Nothing sends a body with a GET. */
  body?: unknown
  signal?: AbortSignal
}

/** A refusal the API answered, or a failure to read its answer as one. */
export class ApiError extends Error {
  /** The API's own code, or `unreadable` when the answer was not its shape. */
  readonly code: string
  /** The HTTP status, kept for a caller that switches on it; screens read `code`. */
  readonly status: number

  constructor(status: number, fault: Fault) {
    super(fault.message)
    this.name = 'ApiError'
    this.code = fault.code
    this.status = status
  }
}

export interface ApiClient {
  request<T>(path: string, call?: ApiCall): Promise<T>
}

export interface ApiClientOptions {
  /** The current ID token, or null when nobody is signed in. */
  token: () => Promise<string | null>
  /** Injected so a test never reaches the network. */
  fetch?: typeof globalThis.fetch
}

/**
 * Joins a path to the API's base, refusing anything that could leave the
 * origin. A path is what the API's own routes are written as (`/orgs`), and a
 * caller that passes a host is a bug rather than a feature.
 */
function apiPath(path: string): string {
  if (!path.startsWith('/') || path.startsWith('//') || path.includes('://')) {
    throw new Error(`an API path must be a path under ${API_BASE}: ${path}`)
  }
  return API_BASE + path
}

/** Reads a refusal body, or answers the generic fault for one that is not it. */
async function refusal(response: Response): Promise<Fault> {
  const unreadable: Fault = {
    code: 'unreadable',
    message: 'The portal could not read the server’s answer. Try again.',
  }
  let parsed: unknown
  try {
    parsed = await response.json()
  } catch {
    return unreadable
  }
  if (typeof parsed !== 'object' || parsed === null || !('error' in parsed)) {
    return unreadable
  }
  const fault = (parsed as ApiRefusal).error
  if (typeof fault?.code !== 'string' || typeof fault.message !== 'string') {
    return unreadable
  }
  return fault
}

export function createApiClient(options: ApiClientOptions): ApiClient {
  const doFetch = options.fetch ?? globalThis.fetch
  return {
    async request<T>(path: string, call: ApiCall = {}): Promise<T> {
      const method = call.method ?? 'GET'
      const headers: Record<string, string> = { Accept: 'application/json' }
      const token = await options.token()
      if (token !== null) {
        headers.Authorization = `Bearer ${token}`
      }
      let body: string | undefined
      if (call.body !== undefined) {
        headers['Content-Type'] = 'application/json'
        body = JSON.stringify(call.body)
      }
      const response = await doFetch(apiPath(path), {
        method,
        headers,
        body,
        signal: call.signal,
        // The portal's credential is the bearer token above and never a cookie,
        // so no cookie is sent along with it.
        credentials: 'omit',
      })
      if (!response.ok) {
        throw new ApiError(response.status, await refusal(response))
      }
      if (response.status === 204) {
        return undefined as T
      }
      return (await response.json()) as T
    },
  }
}
