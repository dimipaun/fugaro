// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The checkout screen (portal plan P4.1; F15, D2192).
//
// **Paddle's library is a module fake and the API's transport is a stub.**
// Nothing here reaches the network: the client is built over its own `fetch`
// seam, and Paddle's Checkout is faked at the import the screen makes, which is
// what lets these cases drive the screen's real path rather than a prop that
// only the suite uses. That is also where the two assertions worth making are
// made -- what is asked of the API, and what the overlay is handed -- because
// those are the boundary F15 is about.
//
// **The acceptance is a fact of the order and not a decoration.** The version
// the page shows travels in the request body, the request is made by the button
// and not by the page, and a person who has not ticked the box cannot buy by
// dispatch: each of the three is a case below.

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { Link, MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApiClient } from '../../api/client'
import type { ApiClient } from '../../api/client'
import { CheckoutScreen, TERMS_URL, TERMS_VERSION } from './checkout'

/** Paddle's own event names, as the fake library below spells them. */
const COMPLETED = 'checkout.completed'
const CLOSED = 'checkout.closed'

/** The build's client token. Paddle's client-side token is public by design --
 * it is in the page for anyone who opens the checkout -- and this is fixture
 * text shaped like it, not a credential. */
const CLIENT_TOKEN = 'fixture-paddle-client-token-not-a-credential'

const ACCEPT = /I accept them on behalf of the business I am buying for/
const PAY = 'Continue to payment'
const OVERLAY_FAILURE = 'The portal could not open Paddle’s payment window. Try again.'
const NO_WORKSPACE = 'The portal opened checkout without a workspace.'

/**
 * Paddle's library, as this suite hands it to the screen: `initializePaddle`
 * records what the screen configured (the token and the environment) and
 * answers the instance, whose `Checkout.open` records what the payment was
 * opened with.
 */
const paddle = vi.hoisted(() => ({
  initialize: vi.fn(),
  open: vi.fn(),
  asked: [] as Record<string, unknown>[],
}))

vi.mock('@paddle/paddle-js', () => ({
  initializePaddle: (options: Record<string, unknown>) => {
    paddle.asked.push(options)
    return paddle.initialize(options)
  },
  CheckoutEventNames: { CHECKOUT_COMPLETED: 'checkout.completed' },
}))

afterEach(() => {
  cleanup()
  vi.unstubAllEnvs()
})

beforeEach(() => {
  paddle.asked.length = 0
  paddle.initialize.mockReset()
  paddle.open.mockReset()
  paddle.initialize.mockResolvedValue({ Checkout: { open: paddle.open } })
  // A staging portal: the environment below is the one a deploy carries, and
  // the case that matters for it is that Paddle is told sandbox and not live.
  vi.stubEnv('VITE_PADDLE_CLIENT_TOKEN', CLIENT_TOKEN)
  vi.stubEnv('VITE_PORTAL_ENVIRONMENT', 'staging')
})

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

/**
 * The client the screen is mounted with, and every request it makes. The
 * transport is replaced through the client's own seam rather than as a global,
 * so nothing here reaches the network and no other suite's `fetch` is touched.
 */
function api(answers: Response[]): { client: ApiClient; calls: Call[] } {
  const calls: Call[] = []
  const client = createApiClient({
    token: () => Promise.resolve('token-one'),
    fetch: ((url: string, init: RequestInit) => {
      calls.push({ url, init })
      const next = answers.shift()
      if (next === undefined) {
        throw new Error('the test did not answer this request')
      }
      return Promise.resolve(next)
    }) as unknown as typeof globalThis.fetch,
  })
  return { client, calls }
}

/** The address the app is on, drawn, so a navigation is a fact a case reads. */
function Where() {
  const location = useLocation()
  return <p>{location.pathname + location.search}</p>
}

function show(
  client: ApiClient,
  path = '/checkout/ws_example',
  pattern = '/checkout/:workspaceId',
): void {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path={pattern} element={<CheckoutScreen client={client} />} />
        <Route path="/workspaces" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  )
}

function accept(): void {
  fireEvent.click(screen.getByRole('checkbox', { name: ACCEPT }))
}

function pay(): void {
  fireEvent.click(screen.getByRole('button', { name: PAY }))
}

/** Paddle's own word to the screen: the event it was configured with, fired. */
function paddleSays(name: string): void {
  const options = paddle.asked.at(-1)
  const callback = options?.['eventCallback'] as ((event: { name: string }) => void) | undefined
  if (callback === undefined) {
    throw new Error('the screen never configured Paddle, so there is no event to fire')
  }
  act(() => {
    callback({ name })
  })
}

describe('the checkout screen', () => {
  it('asks for nothing at all until the terms are accepted', async () => {
    const { client, calls } = api([])
    show(client)

    // The terms are a document a person can read, named by the version this
    // purchase is under: what is accepted is a dated text and not "the terms".
    const link = screen.getByRole('link', { name: /Subscription Terms/ })
    expect(link.getAttribute('href')).toBe(TERMS_URL)
    expect(screen.getByText(new RegExp(TERMS_VERSION))).toBeDefined()

    const box = screen.getByRole('checkbox', { name: ACCEPT })
    const button = screen.getByRole('button', { name: PAY })
    expect((box as HTMLInputElement).checked).toBe(false)
    expect((button as HTMLButtonElement).disabled).toBe(true)

    // Ticking the box is not buying: the transaction is asked for by the button.
    accept()
    expect((button as HTMLButtonElement).disabled).toBe(false)
    await Promise.resolve()
    expect(calls).toHaveLength(0)
  })

  it('buys with the workspace and the version accepted, and hands the overlay the id alone', async () => {
    const { client, calls } = api([answer(201, { transaction_id: 'txn_01' })])
    show(client)
    accept()
    pay()

    await waitFor(() => {
      expect(paddle.open).toHaveBeenCalledTimes(1)
    })

    expect(calls).toHaveLength(1)
    const call = calls[0]!
    expect(call.url).toBe('/api/checkout')
    expect(call.init.method).toBe('POST')
    expect((call.init.headers as Record<string, string>)['Authorization']).toBe('Bearer token-one')
    expect((call.init.headers as Record<string, string>)['Content-Type']).toBe('application/json')
    expect(call.init.credentials).toBe('omit')

    // The body is those two facts and nothing else. The organisation, the price
    // and Paddle's custom data are the API's to build (F15), and the API's own
    // strict decoding refuses a body that names one rather than ignoring it.
    const body = JSON.parse(String(call.init.body)) as Record<string, unknown>
    expect(Object.keys(body).sort()).toEqual(['terms_version', 'workspace_id'])
    expect(body).toEqual({ workspace_id: 'ws_example', terms_version: TERMS_VERSION })

    // What the overlay is opened with: the transaction, and no second field --
    // no items, no price id, nothing a page chose.
    expect(paddle.open).toHaveBeenCalledWith({ transactionId: 'txn_01' })

    // And the deployment's own configuration: this build's client token, and
    // sandbox because the portal's own environment is staging (P0.2).
    expect(paddle.asked).toHaveLength(1)
    expect(paddle.asked[0]).toMatchObject({ token: CLIENT_TOKEN, environment: 'sandbox' })
  })

  it('lands on the workspaces with the refresh marker when Paddle says the money is through', async () => {
    const { client } = api([answer(201, { transaction_id: 'txn_01' })])
    show(client)
    accept()
    pay()
    await waitFor(() => {
      expect(paddle.open).toHaveBeenCalledTimes(1)
    })

    // Closing the window is not paying: somebody who changed their mind is
    // where they were, with the button to open it again.
    paddleSays(CLOSED)
    expect(screen.queryByText('/workspaces?licence=changed')).toBeNull()

    // Paddle's completion is what a purchase is, and the marker is the
    // workspace screen's own (P3.4): a purchase reaches an installation through
    // `fugaro licence refresh` and never through the browser.
    paddleSays(COMPLETED)
    expect(await screen.findByText('/workspaces?licence=changed')).toBeDefined()
  })

  it('shows the API’s refusal as the API wrote it, and opens no window', async () => {
    const { client, calls } = api([
      answer(409, {
        error: {
          code: 'already_subscribed',
          message:
            'That workspace already has a running subscription. Change its seats from its billing page instead.',
        },
      }),
    ])
    show(client)
    accept()
    pay()

    expect((await screen.findByRole('alert')).textContent).toBe(
      'That workspace already has a running subscription. Change its seats from its billing page instead.',
    )
    expect(paddle.initialize).not.toHaveBeenCalled()
    expect(calls).toHaveLength(1)
  })

  it('claims no payment when the window cannot be opened', async () => {
    paddle.initialize.mockRejectedValue(new Error('the library did not load'))
    const { client } = api([answer(201, { transaction_id: 'txn_01' })])
    show(client)
    accept()
    pay()

    expect((await screen.findByRole('alert')).textContent).toBe(OVERLAY_FAILURE)
    // Nothing says a payment happened: the sentence is this screen's own and
    // the button is there to be pressed again.
    expect(screen.getByRole('button', { name: PAY })).toBeDefined()
  })

  it('says so, without reaching Paddle at all, when the build carries no token', async () => {
    vi.stubEnv('VITE_PADDLE_CLIENT_TOKEN', '')
    const { client, calls } = api([answer(201, { transaction_id: 'txn_01' })])
    show(client)
    accept()
    pay()

    expect((await screen.findByRole('alert')).textContent).toBe(OVERLAY_FAILURE)
    // The API created the transaction before the overlay was asked for -- an
    // unpaid transaction is a draft and nothing else -- and the library was
    // never loaded, so no third-party script reached a page that cannot pay.
    expect(calls).toHaveLength(1)
    expect(paddle.initialize).not.toHaveBeenCalled()
  })

  it.each<[string, string]>([
    ['/checkout', '/checkout'],
    ['/checkout/.', '/checkout/:workspaceId'],
    ['/checkout/..', '/checkout/:workspaceId'],
  ])('refuses %s, rather than asking the API about it', async (path, pattern) => {
    const { client, calls } = api([])
    show(client, path, pattern)

    // A dot segment is not an id: joined into the API's path it is normalised
    // away before the request goes out, so a workspace of `..` would ask about
    // a different route with this person's token on it. Nothing is asked, and
    // there is nothing to buy with.
    expect(screen.getByRole('alert').textContent).toBe(NO_WORKSPACE)
    expect(screen.queryByRole('button', { name: PAY })).toBeNull()
    await Promise.resolve()
    expect(calls).toHaveLength(0)
  })

  it('does not pull somebody who has left the checkout into the workspaces', async () => {
    const { client } = api([answer(201, { transaction_id: 'txn_01' })])
    render(
      <MemoryRouter initialEntries={['/checkout/ws_example']}>
        <Link to="/elsewhere">Leave the checkout</Link>
        <Routes>
          <Route path="/checkout/:workspaceId" element={<CheckoutScreen client={client} />} />
          <Route path="/workspaces" element={<Where />} />
          <Route path="/elsewhere" element={<Where />} />
        </Routes>
      </MemoryRouter>,
    )
    accept()
    pay()
    await waitFor(() => {
      expect(paddle.open).toHaveBeenCalledTimes(1)
    })

    fireEvent.click(screen.getByRole('link', { name: 'Leave the checkout' }))
    expect(await screen.findByText('/elsewhere')).toBeDefined()

    // Paddle's window outlives this screen, and a payment made after the person
    // left belongs to the page they are on now -- which is not this one.
    paddleSays(COMPLETED)
    expect(screen.getByText('/elsewhere')).toBeDefined()
  })
})
