// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The reseller screens (portal plan P6.4; spec §Resellers, D2189, D2190).
//
// **What this suite is for.** A reseller sees the customers it sold to and the
// monthly statements it owes Fugaro for, and it sees nothing else: it is never a
// member of its customers' organisations (P6.1), so no key, no secret and no
// reveal reaches these screens (F5). The last case below is that reading of the
// spec held as a test rather than as a habit, because the screen that shows a
// customer is exactly where such a value would be added by somebody who meant
// well.
//
// **The API is a port, and it is a fake here.** `ResellerScreen` is given its
// own `ResellerApi` (see `api.ts` for why that seam exists): nothing here
// reaches a network, and no case needs an account. The refusal case builds its
// `ApiError` by hand rather than through a client, so what is asserted is the
// screen's reading of a refusal and not the client's decoding of one -- which
// `test/api.test.ts` already pins.
//
// **The route is read from the other side, because the port above is what a
// person never sees.** The app mounts `route.element`, and what builds the port
// there is the session: a case at the end of this file renders that element
// with a fake `AuthPort` and a stubbed `fetch`, so the wiring is covered rather
// than assumed. It is the one place here that names the API's paths as this
// screen spells them (`api.ts` explains why they are spelled at all).
//
// The rendering paths a case reaches are named when the case is written, not
// quoted: a heading by its role, a section by its own label, and the file's one
// sentence of its own by value, so a rewording that loses the promise fails here
// rather than passing with a screen that says something else.

import { act, cleanup, render, screen, within } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../api/client'
import type { AuthPort, SignedInUser } from '../../auth/session'
import { SessionProvider } from '../../auth/session'
import type { KeyMetadata } from '../../api/types/keys'
import type { ResellerCustomer } from '../../api/types/resellers'
import type { Statement } from '../../api/types/statements'
import { SIGN_IN_PATH, routes } from '../../routes'
import { customerKeysPath } from './api'
import type { ResellerApi } from './api'
import { ResellerScreen } from './reseller'
import { route } from './route'

// The suite runs without the runner's globals, so React Testing Library cannot
// register its own cleanup: a screen a case rendered is still in the document
// when the next case asks for a heading. The route cases stub `fetch`, which is
// the runner's global rather than a spy, so it is undone by hand.
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

// Two customers and two lines, and no two figures the same: the month's
// accrual is the sum of its lines, so a screen that drew the statement's total
// where a line's amount belongs is caught rather than passing on a coincidence.
const CUSTOMERS: ResellerCustomer[] = [
  {
    organisation_id: 'org_acme',
    name: 'Acme Ltd',
    reseller_organisation_id: 'org_reseller',
    linked_at: '2026-02-10T11:00:00Z',
  },
  {
    organisation_id: 'org_borealis',
    name: 'Borealis Inc',
    reseller_organisation_id: 'org_reseller',
    linked_at: '2026-02-12T09:00:00Z',
  },
]

// Acme holds two keys and Borealis none, so one render carries both the
// metadata a reseller reads and the customer it sold to that has nothing bound
// yet. Neither key carries anything a key cannot be shown with: `KeyMetadata`
// has no room for a secret (`src/api/types/keys.ts`), which is what F5's
// metadata-only rule leaves a reseller reading.
const KEYS: Record<string, KeyMetadata[]> = {
  org_acme: [
    {
      key_id: 'key_1',
      entitlement_id: 'ent_1',
      workspace_id: 'acme-prod',
      tier: 'team',
      kind: 'paid',
      seats: 5,
      issued_at: '2026-02-10T11:00:00Z',
      paid_through: '2026-03-10T11:00:00Z',
      last_seen: '2026-02-20T07:15:00Z',
    },
    {
      key_id: 'key_2',
      entitlement_id: 'ent_2',
      workspace_id: 'acme-staging',
      tier: 'team',
      kind: 'trial',
      seats: 2,
      issued_at: '2026-02-11T08:00:00Z',
      paid_through: '2026-03-11T08:00:00Z',
      // An air-gapped key never asks, which is what a null `last_seen` says.
      last_seen: null,
    },
  ],
  org_borealis: [],
}

const STATEMENT: Statement = {
  id: 'stm_1',
  reseller_organisation_id: 'org_reseller',
  month: '2026-02',
  accrued_micros: 30_000_000,
  paid_micros: 0,
  state: 'open',
  lines: [
    { organisation_id: 'org_acme', amount_micros: 24_000_000 },
    { organisation_id: 'org_borealis', amount_micros: 6_000_000 },
  ],
  issued_at: '2026-03-01T00:05:00Z',
  paid_at: null,
}

/** The screen's own sentence for a failure that was not the API's refusal. */
const GENERIC_FAILURE = 'The portal could not read your reseller account. Try again.'

/** A port that answers with the two samples above, unless a case says otherwise. */
function api(overrides: Partial<ResellerApi> = {}): ResellerApi {
  return {
    customers: () => Promise.resolve(CUSTOMERS),
    keys: (organisationId) => Promise.resolve(KEYS[organisationId] ?? []),
    statements: () => Promise.resolve([STATEMENT]),
    ...overrides,
  }
}

describe('the path one customer’s keys are read at', () => {
  it('keeps the organisation id inside its own segment, whatever the id carries', () => {
    // The id is data -- it comes from the customers list, and nothing about a
    // customer's own account should decide which route of the API this
    // reseller's bearer token is sent to. Left raw, a `/` or a `..` in it would
    // address another path of the same API (the client's own check refuses a
    // path that *leaves* the origin, and one built here never does), so the
    // encoding is the guard and this is what holds it.
    expect(customerKeysPath('a/../b')).toBe('/resellers/customers/a%2F..%2Fb/keys')
    expect(customerKeysPath('a/b')).toBe('/resellers/customers/a%2Fb/keys')
    // The ordinary id is unchanged: an encoding that mangled one a customer
    // really has would be a screen that cannot read its own customers.
    expect(customerKeysPath('org_acme')).toBe('/resellers/customers/org_acme/keys')
  })
})

describe('the reseller screen', () => {
  it('is the screen the app collects for its own path', () => {
    expect(route.path).toBe('/reseller')
    expect(routes.map((registered) => registered.path)).toContain('/reseller')
  })

  it('lists the customers the reseller sold to, with the day each was linked', async () => {
    render(<ResellerScreen api={api()} />)

    const customers = within(await screen.findByRole('region', { name: 'Customers' }))
    expect(customers.getByText('Acme Ltd')).toBeDefined()
    expect(customers.getByText('org_acme')).toBeDefined()
    expect(customers.getByText(/Linked 2026-02-10/)).toBeDefined()
  })

  it('shows each customer’s keys and renewal dates, which is what a reseller may see of them', async () => {
    render(<ResellerScreen api={api()} />)

    const customers = within(await screen.findByRole('region', { name: 'Customers' }))
    const key = customers.getByRole('row', { name: /key_1/ })
    expect(key.textContent).toContain('acme-prod')
    expect(key.textContent).toContain('team')
    expect(key.textContent).toContain('5')
    // The renewal date is the term's end, and the last refresh is what tells a
    // reseller which of its customers' installations is still running.
    expect(key.textContent).toContain('2026-03-10')
    expect(key.textContent).toContain('2026-02-20')
    // A key that never refreshed says so rather than drawing an empty cell.
    expect(customers.getByRole('row', { name: /key_2/ }).textContent).toContain('Never')
    expect(customers.getByText('No keys yet.')).toBeDefined()
  })

  it('shows each month’s statement, what accrued, what was paid and which customer moved', async () => {
    render(<ResellerScreen api={api()} />)

    const statements = within(await screen.findByRole('region', { name: 'Monthly statements' }))
    expect(statements.getByRole('heading', { name: '2026-02' })).toBeDefined()
    expect(statements.getByText('Open')).toBeDefined()
    expect(statements.getByText('$30.00')).toBeDefined()
    expect(statements.getByText('$0.00')).toBeDefined()
    // A line carries the customer's own name, which is what a reseller
    // recognises: the id it was given is there to act on, not to read.
    expect(statements.getByRole('row', { name: /Acme Ltd/ }).textContent).toContain('$24.00')
    expect(statements.getByRole('row', { name: /Borealis Inc/ }).textContent).toContain('$6.00')
  })

  it('offers no reveal, and shows nothing of a key but its metadata', async () => {
    render(<ResellerScreen api={api()} />)

    await screen.findByRole('region', { name: 'Customers' })
    // A reseller reads metadata only (F5): the screen may draw no credential,
    // offer no reveal, and hold no field a secret could be pasted into.
    expect(screen.queryByRole('button', { name: /reveal/i })).toBeNull()
    expect(document.body.textContent ?? '').not.toMatch(/reveal|secret|fgk_/i)
    expect(document.querySelector('input')).toBeNull()
  })

  it('asks for the keys of the customers it read, and of nobody else', async () => {
    const asked: string[] = []
    render(
      <ResellerScreen
        api={api({
          keys: (organisationId) => {
            asked.push(organisationId)
            return Promise.resolve(KEYS[organisationId] ?? [])
          },
        })}
      />,
    )

    await screen.findByRole('region', { name: 'Customers' })
    expect(asked).toEqual(['org_acme', 'org_borealis'])
  })

  it('fails the whole screen when one customer’s keys cannot be read', async () => {
    render(
      <ResellerScreen
        api={api({
          keys: (organisationId) =>
            organisationId === 'org_borealis'
              ? Promise.reject(
                  new ApiError(503, {
                    code: 'unavailable',
                    message: 'That customer’s keys could not be read.',
                  }),
                )
              : Promise.resolve(KEYS[organisationId] ?? []),
        })}
      />,
    )

    expect((await screen.findByRole('alert')).textContent).toBe('That customer’s keys could not be read.')
    // A read that failed is not a customer with nothing bound. Turning one into
    // the other -- an empty list, drawn as "No keys yet." -- would tell a
    // reseller its customer's installation never ran `fugaro setup`, which is a
    // fact of its own and a chase nobody should be sent on: the screen says the
    // read failed and draws no part of the page, not even the customer it did
    // read.
    expect(screen.queryByText('No keys yet.')).toBeNull()
    expect(screen.queryByText('Acme Ltd')).toBeNull()
    expect(screen.queryByRole('region', { name: 'Customers' })).toBeNull()
  })

  it('says a reseller has nothing yet rather than drawing empty tables', async () => {
    render(
      <ResellerScreen api={api({ customers: () => Promise.resolve([]), statements: () => Promise.resolve([]) })} />,
    )

    expect(await screen.findByText('No customers yet.')).toBeDefined()
    expect(screen.getByText('No statements yet.')).toBeDefined()
  })

  it('says it is loading until the API has answered', () => {
    const pending = new Promise<never>(() => {})
    render(<ResellerScreen api={api({ customers: () => pending, statements: () => pending })} />)

    expect(screen.getByRole('status').textContent).toBe('Loading…')
  })

  it('shows a refusal as the API wrote it', async () => {
    render(
      <ResellerScreen
        api={api({
          customers: () =>
            Promise.reject(new ApiError(403, { code: 'refused', message: 'This account is not a reseller.' })),
        })}
      />,
    )

    expect((await screen.findByRole('alert')).textContent).toBe('This account is not a reseller.')
  })

  it('shows one sentence of its own for a failure the API did not write', async () => {
    render(<ResellerScreen api={api({ statements: () => Promise.reject(new Error('the SDK said something')) })} />)

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe(GENERIC_FAILURE)
    expect(alert.textContent).not.toContain('the SDK said something')
  })
})

// What a person is signed in as, and the token the API client is handed below.
const PERSON: SignedInUser = {
  uid: 'uid-1',
  email: 'reseller@example.com',
  provider: 'google.com',
  authTime: 1_700_000_000_000,
}

const TOKEN = 'token-one'

/**
 * A session port whose first answer the case gives, so `loading` can be held
 * open: `answer` is the callback the provider subscribed with, and it throws
 * rather than quietly doing nothing when the route never subscribed at all --
 * which is itself a reading of the code under test.
 */
function session(overrides: Partial<AuthPort> = {}): {
  auth: AuthPort
  answer: (user: SignedInUser | null) => void
} {
  let subscribed: ((user: SignedInUser | null) => void) | null = null
  const auth: AuthPort = {
    watch: (onUser) => {
      subscribed = onUser
      return () => {}
    },
    idToken: () => Promise.resolve(TOKEN),
    signIn: () => Promise.resolve(),
    sendSignInLink: () => Promise.resolve(),
    isSignInLink: () => false,
    completeSignInLink: () => Promise.resolve(),
    signOut: () => Promise.resolve(),
    ...overrides,
  }
  return {
    auth,
    answer: (user) => {
      if (subscribed === null) {
        throw new Error('the route never watched the session')
      }
      subscribed(user)
    },
  }
}

/** One request the route's own API client sent, as the client sent it. */
interface Sent {
  url: string
  headers: Record<string, string>
}

/** The API's answers, by the path this screen's `api.ts` spells. */
const ANSWERS: Record<string, unknown> = {
  '/api/resellers/customers': CUSTOMERS,
  '/api/statements': [STATEMENT],
  '/api/resellers/customers/org_acme/keys': KEYS.org_acme,
  '/api/resellers/customers/org_borealis/keys': [],
}

/**
 * A `fetch` that answers from the table above and records what was asked for.
 * The route builds its own client -- that is the wiring under test -- so the
 * transport it will use is the runner's global, and this is what stands there.
 */
function transport(sent: Sent[]): void {
  vi.stubGlobal('fetch', (url: string, init: { headers: Record<string, string> }) => {
    sent.push({ url, headers: init.headers })
    return Promise.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve(ANSWERS[url] ?? null),
    } as unknown as Response)
  })
}

/**
 * The route as the app mounts it: collected by the glob, rendered at its own
 * path inside a router. The sign-in path is a stand-in route rather than the
 * sign-in screen, so what a redirect asserts is the path -- the fact
 * `src/routes.ts` holds -- and not that screen's own copy.
 */
function shell(auth: AuthPort): ReactElement {
  return (
    <MemoryRouter initialEntries={[route.path]}>
      <SessionProvider port={auth}>
        <Routes>
          <Route path={route.path} element={route.element} />
          <Route path={SIGN_IN_PATH} element={<p>the sign-in screen answers this path</p>} />
          <Route path="*" element={<p>no screen answers this path</p>} />
        </Routes>
      </SessionProvider>
    </MemoryRouter>
  )
}

describe('the reseller route', () => {
  it('sends a visitor with no session to sign in, and reads nothing for them', async () => {
    const sent: Sent[] = []
    transport(sent)
    const { auth, answer } = session()

    render(shell(auth))
    await act(async () => {
      answer(null)
    })

    expect(screen.getByText('the sign-in screen answers this path')).toBeDefined()
    expect(screen.queryByRole('region', { name: 'Customers' })).toBeNull()
    expect(sent).toEqual([])
  })

  it('reads nothing until the session has answered, then reads with the token it holds', async () => {
    const sent: Sent[] = []
    transport(sent)
    const { auth, answer } = session()

    render(shell(auth))

    // The SDK has not answered yet. The flush is what makes this an assertion
    // about the read rather than about the render: the client asks the port for
    // a token before it sends anything, so a screen that read while loading
    // would have sent its requests by the end of this turn -- and, the port
    // being what its screen keys on, would keep drawing them for the rest of
    // the visit.
    await act(async () => {})
    expect(screen.getByRole('status').textContent).toBe('Loading…')
    expect(sent).toEqual([])

    await act(async () => {
      answer(PERSON)
    })

    const customers = within(await screen.findByRole('region', { name: 'Customers' }))
    expect(customers.getByText('Acme Ltd')).toBeDefined()
    expect(sent.map((request) => request.url)).toEqual([
      '/api/resellers/customers',
      '/api/statements',
      '/api/resellers/customers/org_acme/keys',
      '/api/resellers/customers/org_borealis/keys',
    ])
    for (const request of sent) {
      expect(request.headers.Authorization).toBe(`Bearer ${TOKEN}`)
    }
  })

  it('takes a signed-out person’s figures off the page and sends them to sign in', async () => {
    const sent: Sent[] = []
    transport(sent)
    const { auth, answer } = session()

    render(shell(auth))
    await act(async () => {
      answer(PERSON)
    })
    expect((await screen.findByRole('region', { name: 'Customers' })).textContent).toContain('Acme Ltd')

    await act(async () => {
      answer(null)
    })

    expect(screen.queryByText('Acme Ltd')).toBeNull()
    expect(screen.queryByText('$30.00')).toBeNull()
    expect(screen.queryByRole('region', { name: 'Customers' })).toBeNull()
    expect(screen.getByText('the sign-in screen answers this path')).toBeDefined()
  })

})
