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
// The rendering paths a case reaches are named when the case is written, not
// quoted: a heading by its role, a section by its own label, and the file's one
// sentence of its own by value, so a rewording that loses the promise fails here
// rather than passing with a screen that says something else.

import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { ApiError } from '../../api/client'
import type { KeyMetadata } from '../../api/types/keys'
import type { ResellerCustomer } from '../../api/types/resellers'
import type { Statement } from '../../api/types/statements'
import { routes } from '../../routes'
import type { ResellerApi } from './api'
import { ResellerScreen } from './reseller'
import { route } from './route'

// The suite runs without the runner's globals, so React Testing Library cannot
// register its own cleanup: a screen a case rendered is still in the document
// when the next case asks for a heading.
afterEach(cleanup)

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
