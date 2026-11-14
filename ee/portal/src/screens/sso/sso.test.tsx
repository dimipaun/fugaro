// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The SSO dialog and the moment the switcher opens it (portal plan P2.3;
// spec §Auth, D2207).
//
// The plan names this suite's first case -- an operations organisation
// selected while the person is signed in by email opens the dialog -- and the
// two halves are here together because they are one behaviour: the switcher
// decides, the dialog answers. The rest reads the dialog's own rule, which is
// `src/auth/sso.ts`'s: an operations organisation is offered Google alone, a
// reseller Microsoft only once operations has pinned a directory, and nobody
// is offered the emailed link the API refuses.
//
// The first case is also what pins the other half of a choice: **the refusal
// writes nothing.** Choosing an organisation is a write (`POST /orgs/{id}/select`,
// pinned in `switcher.test.tsx`), and the one refusal the switcher makes on its
// own must not send it — a person who is turned away has not started working in
// the organisation, and a `last_used_at` that moved anyway would land them there
// the next time. The `fetch` below fails the test if anything reaches it.
//
// Hermetic: the port is a fake, so nothing reaches Firebase, and this suite
// fetches nothing at all -- the switcher is handed its organisations.

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Organisation } from '../../api/types/orgs'
import type { AuthPort, SignedInUser } from '../../auth/session'
import { SessionProvider } from '../../auth/session'
import { OrganisationSwitcher } from '../orgs/switcher'
import { SsoDialog } from './dialog'

// The suite runs without the runner's globals, so React Testing Library cannot
// register its own cleanup.
afterEach(cleanup)

const PERSON: SignedInUser = {
  uid: 'uid_dana',
  email: 'dana@example.com',
  provider: 'emailLink',
  authTime: 1_700_000_000_000,
}

const OPERATIONS: Organisation = {
  id: 'org_ops',
  name: 'Fugaro operations',
  type: 'operations',
  terms: null,
  reseller_id: null,
  created_at: '2026-01-01T00:00:00Z',
}

function port(user: SignedInUser | null = PERSON, overrides: Partial<AuthPort> = {}): AuthPort {
  return {
    watch: (onUser) => {
      onUser(user)
      return () => {}
    },
    idToken: vi.fn(() => Promise.resolve('token')),
    signIn: vi.fn(() => Promise.resolve()),
    sendSignInLink: vi.fn(() => Promise.resolve()),
    isSignInLink: () => false,
    completeSignInLink: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
    ...overrides,
  }
}

/** A `fetch` that fails the test if anything reaches it, because nothing should. */
function transport(): ReturnType<typeof vi.fn> {
  const fetch = vi.fn(() => {
    throw new Error('a choice this suite refuses wrote something')
  })
  vi.stubGlobal('fetch', fetch)
  return fetch
}

afterEach(() => {
  vi.unstubAllGlobals()
})

/** Where the router ended up, so a refusal reads as "it did not go there". */
function Landing() {
  const location = useLocation()
  return <p>{location.pathname + location.search}</p>
}

function show(element: ReactElement, auth: AuthPort = port()): void {
  render(<SessionProvider port={auth}>{element}</SessionProvider>)
}

describe('an organisation that needs a work account', () => {
  it('opens the dialog for an operations organisation picked by an email identity', async () => {
    const fetch = transport()
    show(
      <MemoryRouter initialEntries={['/orgs']}>
        <Routes>
          <Route
            path="/orgs"
            element={<OrganisationSwitcher organisations={[OPERATIONS]} selectedId={null} />}
          />
          <Route path="*" element={<Landing />} />
        </Routes>
      </MemoryRouter>,
    )

    fireEvent.click(screen.getByRole('link', { name: OPERATIONS.name }))

    expect(await screen.findByRole('dialog')).toBeDefined()
    expect(screen.queryByText(`/members?org=${OPERATIONS.id}`)).toBeNull()
    expect(fetch).not.toHaveBeenCalled()
  })
})

describe('the SSO dialog', () => {
  it('says the emailed link is refused, and offers Google alone to operations', () => {
    show(<SsoDialog organisation={{ type: 'operations' }} onDismiss={() => {}} />)

    expect(screen.getByRole('dialog')).toBeDefined()
    expect(screen.getByText(/The emailed sign-in link is refused for it/)).toBeDefined()
    expect(screen.getByRole('button', { name: 'Sign in with Google' })).toBeDefined()
    expect(screen.queryByRole('button', { name: 'Sign in with Microsoft' })).toBeNull()
    expect(screen.queryByLabelText('Email address')).toBeNull()
  })

  it('offers Microsoft to a reseller only once a directory is pinned', () => {
    show(<SsoDialog organisation={{ type: 'reseller' }} onDismiss={() => {}} />)
    expect(screen.getByRole('button', { name: 'Sign in with Google' })).toBeDefined()
    expect(screen.queryByRole('button', { name: 'Sign in with Microsoft' })).toBeNull()

    cleanup()
    show(
      <SsoDialog
        organisation={{ type: 'reseller', microsoftTenants: ['directory-a'] }}
        onDismiss={() => {}}
      />,
    )
    expect(screen.getByRole('button', { name: 'Sign in with Microsoft' })).toBeDefined()
  })

  it('signs the person in with the method they choose', async () => {
    const auth = port()
    show(<SsoDialog organisation={{ type: 'operations' }} onDismiss={() => {}} />, auth)

    fireEvent.click(screen.getByRole('button', { name: 'Sign in with Google' }))

    await waitFor(() => {
      expect(auth.signIn).toHaveBeenCalledWith('google')
    })
  })

  it('closes without signing anybody in', () => {
    const onDismiss = vi.fn()
    show(<SsoDialog organisation={{ type: 'operations' }} onDismiss={onDismiss} />)

    fireEvent.click(screen.getByRole('button', { name: 'Not now' }))

    expect(onDismiss).toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeDefined()
  })
})
