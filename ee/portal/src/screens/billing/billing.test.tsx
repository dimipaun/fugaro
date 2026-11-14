// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The billing screen (portal plan P4.4; spec §Customer experience, D2192).
//
// The three facts the plan names are here, and a fourth reading beside them:
// the seats, the renewal date, and the link out to Paddle's customer portal.
// What each of them is *sourced from* is half the reading -- the screen decides
// nothing, so the sample below is a subscription Paddle reported, and the test
// that shows the renewal date also asserts the instant it was formatted from is
// still on the element.
//
// **The transport is stubbed and the session is a fake.** `createApiClient`
// reads `globalThis.fetch` when it is built, so a test that replaces it never
// reaches the network, and the token the client sends is the fake port's own --
// which is how the request the API will serve is pinned here rather than in a
// comment. The paths are spelled out in the assertions on purpose: a test that
// built them from the screen's own constants would agree with whatever the
// screen happened to do.

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { Link, MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Subscription } from '../../api/types/billing'
import type { AuthPort, SignedInUser } from '../../auth/session'
import { SessionProvider } from '../../auth/session'
import { BillingScreen } from './billing'
import { route } from './route'

// The suite runs without the runner's globals, so React Testing Library cannot
// register its own cleanup.
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
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
 * The requests the screen makes, answered from the list given. A request with
 * no answer left is a test that did not expect it, and the throw says which.
 */
function stubFetch(answers: (() => Response | Promise<Response>)[]): Call[] {
  const calls: Call[] = []
  vi.stubGlobal('fetch', ((url: string, init: RequestInit) => {
    calls.push({ url, init })
    const next = answers.shift()
    if (next === undefined) {
      throw new Error('the test did not answer this request')
    }
    return Promise.resolve(next())
  }) as unknown as typeof globalThis.fetch)
  return calls
}

function header(call: Call, name: string): string | undefined {
  return (call.init.headers as Record<string, string>)[name]
}

/** The person the fake session answers as, with the token the requests carry. */
const PERSON: SignedInUser = {
  uid: 'uid_example',
  email: 'ada@example.com',
  provider: 'google.com',
  authTime: 1770800000000,
}

/** The person who replaces them under a mounted screen, with a token of their own. */
const OTHER: SignedInUser = {
  uid: 'uid_other',
  email: 'grace@example.com',
  provider: 'google.com',
  authTime: 1770900000000,
}

/** A port for a person -- signed in by default, whose token the requests carry. */
function port(who: SignedInUser | null = PERSON): AuthPort {
  return {
    watch: (onUser) => {
      onUser(who)
      return () => {}
    },
    idToken: () => Promise.resolve(who === null ? null : 'token-one'),
    signIn: () => Promise.resolve(),
    sendSignInLink: () => Promise.resolve(),
    isSignInLink: () => false,
    completeSignInLink: () => Promise.resolve(),
    signOut: () => Promise.resolve(),
  }
}

/**
 * A port the test drives itself, the way the SDK's does: the stored session is
 * read before `onAuthStateChanged` fires, so until `tell()` the screen is
 * mounted with the session unanswered -- the moment this screen must not ask in
 * -- and afterwards it reports whoever the test names, one person after another
 * if it is asked to. The token a request carries is the current person's, so a
 * change of person is visible in what the next request sends.
 */
function pendingPort(): { auth: AuthPort; tell: (user: SignedInUser | null) => void } {
  let current: SignedInUser | null = null
  let notify: ((user: SignedInUser | null) => void) | null = null
  return {
    auth: {
      watch: (onUser) => {
        notify = onUser
        return () => {}
      },
      idToken: () => Promise.resolve(current === null ? null : `token-${current.uid}`),
      signIn: () => Promise.resolve(),
      sendSignInLink: () => Promise.resolve(),
      isSignInLink: () => false,
      completeSignInLink: () => Promise.resolve(),
      signOut: () => Promise.resolve(),
    },
    tell: (user) => {
      current = user
      notify?.(user)
    },
  }
}

function show(
  path = '/billing/ws_example',
  pattern = '/billing/:workspaceId',
  auth: AuthPort = port(),
): void {
  render(
    <SessionProvider port={auth}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path={pattern} element={<BillingScreen />} />
        </Routes>
      </MemoryRouter>
    </SessionProvider>,
  )
}

/**
 * The screen on a router whose workspace can be changed, with a link standing
 * in for whatever a person follows to go from one workspace's billing to
 * another's -- one screen, two workspaces, which is the case a route parameter
 * changing under a mounted screen makes.
 */
function showTwoWorkspaces(): void {
  render(
    <SessionProvider port={port()}>
      <MemoryRouter initialEntries={['/billing/ws_a']}>
        <Link to="/billing/ws_b">Go to B</Link>
        <Routes>
          <Route path="/billing/:workspaceId" element={<BillingScreen />} />
        </Routes>
      </MemoryRouter>
    </SessionProvider>,
  )
}

/** The value of one row of the facts list, read from the term a person reads. */
function fact(term: string): string {
  const value = screen.getByText(term).nextElementSibling
  if (value === null) {
    throw new Error(`${term} has no value beside it`)
  }
  return value.textContent ?? ''
}

/** The sample `ee/portal/schemas/fixtures/billing.subscription.json` carries. */
const SUBSCRIPTION: Subscription = {
  id: 'sub_1',
  organisation_id: 'org_example',
  workspace_id: 'ws_example',
  state: 'active',
  seats: 3,
  price_id: 'pri_team_monthly',
  starts_at: '2026-02-10T11:00:00Z',
  renews_at: '2026-03-10T11:00:00Z',
  ends_at: null,
  cancelled_at: null,
}

const SEATS = 'New seat count'
const PORTAL_BUTTON = 'Invoices and payment methods'
const GENERIC = 'The portal could not read your billing. Try again.'
const PORTAL_FAILURE = 'The portal could not open Paddle’s customer portal. Try again.'
const SIGN_IN = 'Sign in to see this workspace’s billing.'
const NO_ANSWER = 'The portal did not say what the subscription is. Try again.'

describe('the billing screen', () => {
  it('shows the seats, the state and the renewal date Paddle reported', async () => {
    const calls = stubFetch([() => answer(200, SUBSCRIPTION)])
    show()

    await screen.findByText('Seats')
    expect(fact('Status')).toBe('active')
    expect(fact('Seats')).toBe('3')
    expect(fact('Renews on')).toBe('10 March 2026')
    // The instant is Paddle's and is kept: a screen that showed the day it had
    // worked out for itself would be a date the customer cannot compare with
    // the one Paddle's own portal shows.
    expect(screen.getByText('10 March 2026').getAttribute('datetime')).toBe(
      '2026-03-10T11:00:00Z',
    )

    expect(calls).toHaveLength(1)
    expect(calls[0]!.url).toBe('/api/workspaces/ws_example/subscription')
    expect(calls[0]!.init.method).toBe('GET')
    expect(header(calls[0]!, 'Authorization')).toBe('Bearer token-one')
    expect(calls[0]!.init.credentials).toBe('omit')
  })

  it('shows a subscription that will not renew, and when its term ends', async () => {
    stubFetch([
      () =>
        answer(200, {
          ...SUBSCRIPTION,
          state: 'canceled',
          renews_at: null,
          ends_at: '2026-03-31T11:00:00Z',
          cancelled_at: '2026-03-01T09:00:00Z',
        } satisfies Subscription),
    ])
    show()

    await screen.findByText('Renews on')
    expect(fact('Renews on')).toBe('Does not renew')
    expect(fact('Ends on')).toBe('31 March 2026')
    expect(fact('Status')).toBe('canceled')
  })

  it('buys the whole quantity a person asks for, and shows what came back', async () => {
    const calls = stubFetch([
      () => answer(200, SUBSCRIPTION),
      () => answer(200, { ...SUBSCRIPTION, seats: 5 } satisfies Subscription),
    ])
    show()

    const field = (await screen.findByLabelText(SEATS)) as HTMLInputElement
    expect(field.value).toBe('3')

    fireEvent.change(field, { target: { value: '5' } })
    fireEvent.submit(field.closest('form')!)

    await waitFor(() => {
      expect(fact('Seats')).toBe('5')
    })
    expect(calls[1]!.url).toBe('/api/workspaces/ws_example/subscription/seats')
    expect(calls[1]!.init.method).toBe('POST')
    expect(calls[1]!.init.body).toBe('{"seats":5}')
    expect((await screen.findByRole('status')).textContent).toBe(
      'The subscription now has 5 seats.',
    )
  })

  it('refuses a quantity the schema would refuse, without asking the API', async () => {
    const calls = stubFetch([() => answer(200, SUBSCRIPTION)])
    show()

    const field = (await screen.findByLabelText(SEATS)) as HTMLInputElement
    fireEvent.change(field, { target: { value: '0' } })
    fireEvent.submit(field.closest('form')!)

    expect((await screen.findByRole('alert')).textContent).toBe(
      'Enter a whole number of seats, one or more.',
    )
    expect(calls).toHaveLength(1)
  })

  it('believes the newest seat request when the replies come back in the other order', async () => {
    let answerFive: ((response: Response) => void) | null = null
    const calls = stubFetch([
      () => answer(200, SUBSCRIPTION),
      () => new Promise<Response>((resolve) => {
        answerFive = resolve
      }),
      () => answer(200, { ...SUBSCRIPTION, seats: 7 } satisfies Subscription),
    ])
    show()

    const field = (await screen.findByLabelText(SEATS)) as HTMLInputElement
    fireEvent.change(field, { target: { value: '5' } })
    fireEvent.submit(field.closest('form')!)
    fireEvent.change(field, { target: { value: '7' } })
    fireEvent.submit(field.closest('form')!)

    // Seven is what the person last asked for and what Paddle was told last, and
    // it is answered first; five is answered afterwards, out of order.
    await waitFor(() => {
      expect(fact('Seats')).toBe('7')
    })
    act(() => {
      answerFive?.(answer(200, { ...SUBSCRIPTION, seats: 5 }))
    })
    await act(async () => {
      await Promise.resolve()
    })

    // The older answer applied last would leave the screen saying five while
    // Paddle took seven, and the field beside it -- a whole quantity -- invites
    // the click that buys back down.
    expect(fact('Seats')).toBe('7')
    expect(field.value).toBe('7')
    expect(screen.getByRole('status').textContent).toBe('The subscription now has 7 seats.')
    expect(calls).toHaveLength(3)
  })

  it('asks the API for Paddle’s customer portal link when it is wanted', async () => {
    const calls = stubFetch([
      () => answer(200, SUBSCRIPTION),
      () => answer(200, { url: 'https://sandbox-paddle.example/portal/session-1' }),
    ])
    show()
    await screen.findByText('Seats')

    // Nothing is asked for until the person asks: a session Paddle hands out at
    // page load is a session that is stale by the time it is clicked.
    expect(calls).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: PORTAL_BUTTON }))

    const link = await screen.findByRole('link', { name: /Paddle/ })
    expect(link.getAttribute('href')).toBe('https://sandbox-paddle.example/portal/session-1')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
    expect(calls[1]!.url).toBe('/api/workspaces/ws_example/subscription/portal')
    expect(calls[1]!.init.method).toBe('GET')
  })

  it('never follows a customer-portal answer that is not Paddle’s https', async () => {
    stubFetch([() => answer(200, SUBSCRIPTION), () => answer(200, { url: 'javascript:alert(1)' })])
    show()
    await screen.findByText('Seats')

    fireEvent.click(screen.getByRole('button', { name: PORTAL_BUTTON }))

    expect((await screen.findByRole('alert')).textContent).toBe(PORTAL_FAILURE)
    expect(screen.queryByRole('link')).toBeNull()
  })

  it('shows the sentence the API wrote for a refusal', async () => {
    stubFetch([
      () =>
        answer(404, {
          error: { code: 'no_subscription', message: 'This workspace has no subscription.' },
        }),
    ])
    show()

    expect((await screen.findByRole('alert')).textContent).toBe(
      'This workspace has no subscription.',
    )
  })

  it('answers anything else with one sentence of its own', async () => {
    vi.stubGlobal('fetch', () => Promise.reject(new TypeError('Failed to fetch')))
    show()

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe(GENERIC)
    expect(alert.textContent).not.toContain('Failed to fetch')
  })

  it('waits for the answer rather than showing an empty subscription', () => {
    vi.stubGlobal('fetch', () => new Promise(() => {}))
    show()

    expect(screen.getByRole('status').textContent).toBe('Loading…')
    expect(screen.queryByText('Seats')).toBeNull()
  })

  it('asks nothing until the session has been answered, and then asks with the token', async () => {
    const calls = stubFetch([() => answer(200, SUBSCRIPTION)])
    const { auth, tell } = pendingPort()
    show('/billing/ws_example', '/billing/:workspaceId', auth)

    // The session is still being read, so there is no token to send yet: a
    // request made here would go out bare, and the API's refusal to it would be
    // what a signed-in customer was shown, with nothing on this screen to ask
    // again. So the screen waits, and says it is waiting.
    expect(calls).toHaveLength(0)
    expect(screen.getByRole('status').textContent).toBe('Loading…')
    expect(screen.queryByRole('alert')).toBeNull()

    // A request the screen had already started would have been made by now:
    // the token is asked for first, and that answer is a promise.
    await act(async () => {
      await Promise.resolve()
    })
    expect(calls).toHaveLength(0)

    act(() => {
      tell(PERSON)
    })

    await screen.findByText('Seats')
    expect(calls).toHaveLength(1)
    expect(calls[0]!.url).toBe('/api/workspaces/ws_example/subscription')
    expect(header(calls[0]!, 'Authorization')).toBe('Bearer token-uid_example')
  })

  it('tells somebody signed out to sign in, rather than asking without a token', async () => {
    const calls = stubFetch([() => answer(200, SUBSCRIPTION)])
    show('/billing/ws_example', '/billing/:workspaceId', port(null))

    expect(screen.getByText(SIGN_IN).textContent).toBe(SIGN_IN)
    // A request this screen should not make is one microtask away -- the client
    // asks for the token first -- so the assertions wait that microtask out: a
    // guard checked only against the render they follow would be no guard.
    await act(async () => {
      await Promise.resolve()
    })
    expect(calls).toHaveLength(0)
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('draws none of the subscription once the session has gone', async () => {
    stubFetch([() => answer(200, SUBSCRIPTION)])
    const { auth, tell } = pendingPort()
    show('/billing/ws_example', '/billing/:workspaceId', auth)

    act(() => {
      tell(PERSON)
    })
    await screen.findByText('Seats')

    act(() => {
      tell(null)
    })

    // What was on screen was one workspace's billing, shown to somebody who had
    // signed in to read it: a session that ends is not a reason to keep drawing
    // it, and the seat field is the sharp end -- a whole quantity, so a click
    // would move a subscription the person on this machine no longer is.
    expect(screen.getByText(SIGN_IN).textContent).toBe(SIGN_IN)
    expect(screen.queryByText('Seats')).toBeNull()
    expect(screen.queryByLabelText(SEATS)).toBeNull()
    expect(screen.queryByRole('button', { name: PORTAL_BUTTON })).toBeNull()
  })

  it('draws nothing of the last session to the person who replaces it', async () => {
    const calls = stubFetch([
      () => answer(200, SUBSCRIPTION),
      () =>
        answer(403, {
          error: { code: 'not_a_member', message: 'You are not a member of this workspace.' },
        }),
    ])
    const { auth, tell } = pendingPort()
    show('/billing/ws_example', '/billing/:workspaceId', auth)

    act(() => {
      tell(PERSON)
    })
    await screen.findByText('Seats')

    // The SDK reports one person after another with no sign-out between them,
    // and this path is unchanged: whoever is next is not the person the facts
    // on screen were read for. The API is asked again, as them.
    act(() => {
      tell(OTHER)
    })
    await waitFor(() => {
      expect(calls).toHaveLength(2)
    })
    expect(calls[1]!.url).toBe('/api/workspaces/ws_example/subscription')
    expect(header(calls[1]!, 'Authorization')).toBe('Bearer token-uid_other')

    // A refusal does not put the last session's subscription back: beside it
    // sits a field carrying a whole quantity, and one click would move a
    // subscription belonging to somebody who is no longer at this machine.
    expect((await screen.findByRole('alert')).textContent).toBe(
      'You are not a member of this workspace.',
    )
    expect(screen.queryByText('Seats')).toBeNull()
    expect(screen.queryByLabelText(SEATS)).toBeNull()
    expect(screen.queryByRole('button', { name: PORTAL_BUTTON })).toBeNull()
  })

  it('answers the path and the workspace its own route declares', async () => {
    const calls = stubFetch([() => answer(200, SUBSCRIPTION)])
    render(
      <SessionProvider port={port()}>
        <MemoryRouter initialEntries={['/billing/ws_example']}>
          <Routes>
            <Route path={route.path} element={route.element} />
          </Routes>
        </MemoryRouter>
      </SessionProvider>,
    )

    await screen.findByText('Seats')
    expect(calls[0]!.url).toBe('/api/workspaces/ws_example/subscription')
  })

  it('starts clean when the path names another workspace', async () => {
    stubFetch([
      () => answer(404, { error: { code: 'no_subscription', message: 'A said no.' } }),
      () => answer(200, { ...SUBSCRIPTION, workspace_id: 'ws_b', seats: 5 } satisfies Subscription),
    ])
    showTwoWorkspaces()

    expect((await screen.findByRole('alert')).textContent).toBe('A said no.')
    fireEvent.click(screen.getByRole('link', { name: 'Go to B' }))

    // A refusal raised about one workspace is not a fact about the next one,
    // and neither is the seat count that came with it: the field carries a
    // whole quantity, so one click would move B's subscription to it.
    await waitFor(() => {
      expect(screen.queryByRole('alert')).toBeNull()
    })
    expect(fact('Seats')).toBe('5')
    expect((screen.getByLabelText(SEATS) as HTMLInputElement).value).toBe('5')
  })

  it('draws nothing of the workspace it left while the next one is on its way', async () => {
    stubFetch([() => answer(200, SUBSCRIPTION), () => new Promise<Response>(() => {})])
    showTwoWorkspaces()

    await screen.findByText('Seats')
    fireEvent.click(screen.getByRole('link', { name: 'Go to B' }))

    expect(screen.queryByText('Seats')).toBeNull()
    expect(screen.queryByLabelText(SEATS)).toBeNull()
    expect(screen.getByRole('status').textContent).toBe('Loading…')
  })

  it('refuses a 2xx with no body rather than keeping nothing as a subscription', async () => {
    // A 204 is a success the client resolves to `undefined`, which is not a
    // subscription: kept, the next render would read its fields and take the
    // screen down with no boundary above it.
    stubFetch([() => answer(204)])
    show()

    expect((await screen.findByRole('alert')).textContent).toBe(NO_ANSWER)
    expect(screen.getByRole('heading', { name: 'Billing' }).textContent).toBe('Billing')
    expect(screen.queryByText('Seats')).toBeNull()
  })

  it('refuses a seat change confirmed without the subscription that came of it', async () => {
    const calls = stubFetch([() => answer(200, SUBSCRIPTION), () => answer(204)])
    show()

    const field = (await screen.findByLabelText(SEATS)) as HTMLInputElement
    fireEvent.change(field, { target: { value: '5' } })
    fireEvent.submit(field.closest('form')!)

    expect((await screen.findByRole('alert')).textContent).toBe(NO_ANSWER)
    // What was last reported stands, and is not contradicted: the change may
    // well have landed, and the screen says only what it was told.
    expect(fact('Seats')).toBe('3')
    expect(calls).toHaveLength(2)
  })

  it('says so when it is mounted with no workspace in the path', () => {
    show('/billing', '/billing')

    expect(screen.getByRole('alert').textContent).toBe(
      'The portal opened billing without a workspace.',
    )
  })

  it.each(['.', '..'])('refuses a workspace of %s, rather than asking the API', async (segment) => {
    const calls = stubFetch([() => answer(200, SUBSCRIPTION)])
    show(`/billing/${segment}`, '/billing/:workspaceId')

    // A dot segment is not an id: joined into the API's path it is normalised
    // away before the request goes out -- `/api/workspaces/../subscription` is
    // `/api/subscription` -- so a workspace of `..` would ask the API about
    // something else entirely, with this person's token on the request.
    expect(screen.getByRole('alert').textContent).toBe(
      'The portal opened billing without a workspace.',
    )
    await act(async () => {
      await Promise.resolve()
    })
    expect(calls).toHaveLength(0)
  })
})
