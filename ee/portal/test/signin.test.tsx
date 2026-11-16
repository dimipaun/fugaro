// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The sign-in screen (portal plan P1.4; spec §Auth, D2207).
//
// The screen's job is to offer what will work and to say nothing it invented,
// so both readings are here: an operations organisation sees Google and no
// emailed link, a reseller sees Microsoft only once operations has pinned a
// tenant, and a failure is shown as the API wrote it -- or, when the SDK raised
// it, as one sentence of this screen's own rather than the SDK's console text.
//
// The port is a fake: nothing here reaches Firebase, and no test needs a
// network or an account.

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../src/api/client'
import type { AuthPort, SignedInUser } from '../src/auth/session'
import { SessionProvider } from '../src/auth/session'
import { SignInScreen } from '../src/screens/signin/signin'

// The suite runs without vitest's globals, so React Testing Library cannot
// register its own cleanup: without this, every screen a test rendered is still
// in the document when the next one asks for a heading.
afterEach(cleanup)

const PERSON: SignedInUser = {
  uid: 'uid-1',
  email: 'person@example.com',
  provider: 'google.com',
  authTime: 1_700_000_000_000,
}

const GENERIC = 'The portal could not sign you in. Try again.'

/**
 * A port for a visitor nobody is signed in as, whose first answer arrives at
 * once -- the ordinary state of this screen. A test that wants a signed-in
 * person or a silent SDK passes its own `watch`.
 */
function port(overrides: Partial<AuthPort> = {}): AuthPort {
  return {
    watch: (onUser) => {
      onUser(null)
      return () => {}
    },
    idToken: () => Promise.resolve(null),
    signIn: () => Promise.resolve(),
    sendSignInLink: () => Promise.resolve(),
    isSignInLink: () => false,
    completeSignInLink: () => Promise.resolve(),
    signOut: () => Promise.resolve(),
    ...overrides,
  }
}

function show(element: ReactElement, auth: AuthPort): void {
  render(<SessionProvider port={auth}>{element}</SessionProvider>)
}

describe('the sign-in screen', () => {
  it('offers Google alone to an operations organisation, and says why', async () => {
    show(<SignInScreen organisation={{ type: 'operations' }} />, port())
    await screen.findByRole('heading', { name: 'Sign in to Fugaro' })

    expect(screen.getByRole('button', { name: 'Continue with Google' })).toBeDefined()
    expect(screen.queryByRole('button', { name: 'Continue with Apple' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Continue with Microsoft' })).toBeNull()
    expect(screen.queryByLabelText('Email address')).toBeNull()
    expect(screen.getByText(/The emailed link is not available for it/)).toBeDefined()
  })

  it('offers Microsoft to a reseller only once a tenant is pinned', async () => {
    show(<SignInScreen organisation={{ type: 'reseller' }} />, port())
    await screen.findByRole('heading', { name: 'Sign in to Fugaro' })
    expect(screen.queryByRole('button', { name: 'Continue with Microsoft' })).toBeNull()

    show(
      <SignInScreen organisation={{ type: 'reseller', microsoftTenants: ['tenant-1'] }} />,
      port(),
    )
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Continue with Microsoft' })).toBeDefined()
    })
    expect(screen.queryByLabelText('Email address')).toBeNull()
  })

  it('offers every method, the emailed link included, to a customer', async () => {
    show(<SignInScreen />, port())
    await screen.findByRole('heading', { name: 'Sign in to Fugaro' })

    expect(screen.getByRole('button', { name: 'Continue with Google' })).toBeDefined()
    expect(screen.getByRole('button', { name: 'Continue with Apple' })).toBeDefined()
    expect(screen.getByRole('button', { name: 'Continue with Microsoft' })).toBeDefined()
    expect(screen.getByLabelText('Email address')).toBeDefined()
    expect(screen.queryByText(/not available for it/)).toBeNull()
  })

  it('signs in through the provider a person picks', async () => {
    const auth = port({ signIn: vi.fn(() => Promise.resolve()) })
    show(<SignInScreen />, auth)
    await screen.findByRole('heading', { name: 'Sign in to Fugaro' })

    fireEvent.click(screen.getByRole('button', { name: 'Continue with Apple' }))
    await waitFor(() => {
      expect(auth.signIn).toHaveBeenCalledWith('apple')
    })
  })

  it('mails a link and says where it went', async () => {
    const auth = port({ sendSignInLink: vi.fn(() => Promise.resolve()) })
    show(<SignInScreen />, auth)
    const input = await screen.findByLabelText('Email address')

    fireEvent.change(input, { target: { value: 'other@example.com' } })
    fireEvent.submit(input.closest('form')!)

    await waitFor(() => {
      expect(auth.sendSignInLink).toHaveBeenCalledWith('other@example.com')
    })
    expect(await screen.findByRole('status')).toBeDefined()
    expect(screen.getByText(/other@example\.com/)).toBeDefined()
    expect(screen.queryByLabelText('Email address')).toBeNull()
  })

  it('shows the sentence the API wrote for a refusal', async () => {
    const refusal = new ApiError(403, {
      code: 'sso_required',
      message: 'This organisation signs in with a work account.',
    })
    const auth = port({ signIn: vi.fn(() => Promise.reject(refusal)) })
    show(<SignInScreen />, auth)
    await screen.findByRole('heading', { name: 'Sign in to Fugaro' })

    fireEvent.click(screen.getByRole('button', { name: 'Continue with Google' }))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe('This organisation signs in with a work account.')
  })

  it('answers anything else with one sentence of its own', async () => {
    const auth = port({
      signIn: vi.fn(() => Promise.reject(new Error('Firebase: Error (auth/popup-blocked).'))),
    })
    show(<SignInScreen />, auth)
    await screen.findByRole('heading', { name: 'Sign in to Fugaro' })

    fireEvent.click(screen.getByRole('button', { name: 'Continue with Google' }))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe(GENERIC)
    expect(alert.textContent).not.toContain('popup-blocked')
  })

  it('shows who is signed in, and signs them out', async () => {
    const auth = port({
      watch: (onUser) => {
        onUser(PERSON)
        return () => {}
      },
      signOut: vi.fn(() => Promise.resolve()),
    })
    const view = render(
      <SessionProvider port={auth}>
        <SignInScreen />
      </SessionProvider>,
    )
    await screen.findByRole('heading', { name: 'Signed in' })
    expect(view.getByText('person@example.com')).toBeDefined()

    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }))
    await waitFor(() => {
      expect(auth.signOut).toHaveBeenCalled()
    })
  })

  it('waits for the first answer rather than showing the form', async () => {
    show(<SignInScreen />, port({ watch: () => () => {} }))
    expect(screen.getByRole('status').textContent).toBe('Loading…')
    expect(screen.queryByRole('heading', { name: 'Sign in to Fugaro' })).toBeNull()
  })

  it('completes a link that came back from the mail', async () => {
    const failure = new ApiError(410, { code: 'expired', message: 'That link has expired.' })
    const auth = port({
      isSignInLink: () => true,
      completeSignInLink: vi.fn(() => Promise.reject(failure)),
    })
    show(<SignInScreen />, auth)
    await screen.findByRole('heading', { name: 'Sign in to Fugaro' })

    await waitFor(() => {
      expect(auth.completeSignInLink).toHaveBeenCalledWith(window.location.href)
    })
    expect((await screen.findByRole('alert')).textContent).toBe('That link has expired.')
  })
})
