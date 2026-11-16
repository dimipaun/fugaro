// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The API client (portal plan P1.4; D2188, P1.0's uniform error shape).
//
// Two of these are security readings rather than plumbing. The token is asked
// for at **every** request, because a client that read it once keeps sending a
// token `signOut` has already invalidated; and a path that could leave the
// origin (`//elsewhere.example/x`, an absolute URL) is refused before anything
// is fetched, because `fetch` would send the bearer token to that host without
// a second thought.
//
// The transport is injected and answers from memory: no test here touches the
// network, and none needs an account.

import { describe, expect, it } from 'vitest'
import { ApiError, createApiClient, type ApiClient } from '../src/api/client'

interface Call {
  url: string
  init: RequestInit
}

/** A response the client can read, without a real `fetch` behind it. */
function answer(status: number, body?: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
  } as unknown as Response
}

/** One that cannot be read as JSON at all, which is what a proxy error page is. */
function unreadable(status: number): Response {
  return {
    ok: false,
    status,
    json: () => Promise.reject(new SyntaxError('Unexpected token <')),
  } as unknown as Response
}

function client(
  answers: (() => Response)[],
  token: () => Promise<string | null> = () => Promise.resolve(null),
): { calls: Call[]; api: ApiClient } {
  const calls: Call[] = []
  const fetch = ((url: string, init: RequestInit) => {
    calls.push({ url, init })
    const next = answers.shift()
    if (next === undefined) {
      throw new Error('the test did not answer this request')
    }
    return Promise.resolve(next())
  }) as unknown as typeof globalThis.fetch
  return { calls, api: createApiClient({ token, fetch }) }
}

function header(call: Call, name: string): string | undefined {
  return (call.init.headers as Record<string, string>)[name]
}

describe('the API client', () => {
  it('asks for the token at every request and never holds it', async () => {
    let current: string | null = 'token-one'
    const { calls, api } = client(
      [() => answer(200, {}), () => answer(200, {})],
      () => Promise.resolve(current),
    )

    await api.request('/orgs')
    current = 'token-two'
    await api.request('/orgs')

    expect(header(calls[0]!, 'Authorization')).toBe('Bearer token-one')
    expect(header(calls[1]!, 'Authorization')).toBe('Bearer token-two')
  })

  it('sends no authorization at all when nobody is signed in', async () => {
    const { calls, api } = client([() => answer(200, {})])
    await api.request('/orgs')
    expect(header(calls[0]!, 'Authorization')).toBeUndefined()
  })

  it('refuses a path that could leave the origin, before fetching anything', async () => {
    const { calls, api } = client([])

    await expect(api.request('//elsewhere.example/orgs')).rejects.toThrow(/must be a path/)
    await expect(api.request('https://elsewhere.example/orgs')).rejects.toThrow(/must be a path/)
    await expect(api.request('orgs')).rejects.toThrow(/must be a path/)
    expect(calls).toEqual([])
  })

  it('joins the base, and sends nothing but the token as a credential', async () => {
    const { calls, api } = client([() => answer(200, { ok: true })], () =>
      Promise.resolve('token-one'),
    )

    await api.request('/orgs', { method: 'POST', body: { name: 'Acme' } })

    const call = calls[0]!
    expect(call.url).toBe('/api/orgs')
    expect(call.init.method).toBe('POST')
    expect(call.init.body).toBe('{"name":"Acme"}')
    expect(call.init.credentials).toBe('omit')
    expect(header(call, 'Content-Type')).toBe('application/json')
    expect(header(call, 'Accept')).toBe('application/json')
  })

  it('sends no body and no content type for a read', async () => {
    const { calls, api } = client([() => answer(200, {})])
    await api.request('/orgs')
    expect(calls[0]!.init.method).toBe('GET')
    expect(calls[0]!.init.body).toBeUndefined()
    expect(header(calls[0]!, 'Content-Type')).toBeUndefined()
  })

  it('reads the refusal the API wrote', async () => {
    const { api } = client([
      () => answer(403, { error: { code: 'sso_required', message: 'Use your work account.' } }),
    ])

    const failure = await api.request('/orgs').catch((error: unknown) => error)
    expect(failure).toBeInstanceOf(ApiError)
    expect((failure as ApiError).code).toBe('sso_required')
    expect((failure as ApiError).status).toBe(403)
    expect((failure as ApiError).message).toBe('Use your work account.')
  })

  it('answers a body that is not its shape with one sentence of its own', async () => {
    const page = '<html><body>502 Bad Gateway</body></html>'
    const { api } = client([
      () => unreadable(502),
      () => answer(500, { message: page }),
      () => answer(500, { error: { code: 42 } }),
    ])

    for (const request of [api.request('/orgs'), api.request('/orgs'), api.request('/orgs')]) {
      const failure = (await request.catch((error: unknown) => error)) as ApiError
      expect(failure.code).toBe('unreadable')
      expect(failure.message).not.toContain('502 Bad Gateway')
      expect(failure.message).toContain('could not read')
    }
  })

  it('asks for no body when the answer has none', async () => {
    const { api } = client([
      () =>
        ({
          ok: true,
          status: 204,
          json: () => Promise.reject(new Error('a 204 has no body to read')),
        }) as unknown as Response,
    ])

    await expect(api.request('/orgs')).resolves.toBeUndefined()
  })
})
