// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The skeleton's own three files (portal plan P1.4): `src/routes.ts`,
// `src/app.tsx` and `src/main.tsx`.
//
// **Why they need a suite of their own.** The four suites the plan names reach
// screens, the gate, the API client and the reporter; none of them imports
// these three, so a portal that mounts nothing at all can still be green
// everywhere: the glob's pattern is relative to the file that writes it, and
// written one level up it collects no screen while `vitest`, `tsc` and the
// build all pass. What catches that is a suite that imports the module and
// looks at what it collected -- which is exactly what `collect` refuses to do
// quietly -- and a boot is the same kind of hole from the other side, because
// its work happens when the module is evaluated rather than when a function is
// called.
//
// Every case here is hermetic: the auth port and the reporter are fakes, so
// nothing reaches Firebase or Sentry, and the two chunks those two load are
// only ever named.

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../src/app'
import { firebaseConfig } from '../src/auth/firebase'
import type { AuthPort } from '../src/auth/session'
import { SessionProvider } from '../src/auth/session'
import { CONSENT_KEY } from '../src/consent'
import { SIGN_IN_PATH, routes } from '../src/routes'

// The suite runs without the runner's globals, so React Testing Library cannot
// register its own cleanup.
afterEach(cleanup)

const STARTED = 'The portal could not start. Reload the page.'

/** A port for a visitor nobody is signed in as, which answers at once. */
function port(): AuthPort {
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
  }
}

describe('the collected screens', () => {
  it('collects the screen the glob names, from the directory it names it in', () => {
    // The import above is half the assertion: `collect` runs when this module
    // is evaluated and throws when the pattern matched nothing, so a pattern
    // one level up fails here rather than mounting an app with no screens.
    expect(routes.map((route) => route.path)).toContain(SIGN_IN_PATH)
    for (const route of routes) {
      expect(route.path.startsWith('/')).toBe(true)
    }
  })

  it('orders the screens by path rather than by the directory listing', () => {
    const paths = routes.map((route) => route.path)
    expect([...paths].sort((left, right) => left.localeCompare(right))).toEqual(paths)
  })
})

describe('the app', () => {
  it('mounts the collected screen for anybody with no screen of their own', async () => {
    render(
      <SessionProvider port={port()}>
        <App />
      </SessionProvider>,
    )
    expect(await screen.findByRole('heading', { name: 'Sign in to Fugaro' })).toBeDefined()
  })

  it('draws the consent gate above the screens, and records the decision', async () => {
    render(
      <SessionProvider port={port()}>
        <App />
      </SessionProvider>,
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Accept analytics' }))

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: 'Accept analytics' })).toBeNull()
    })
    expect(window.localStorage.getItem(CONSENT_KEY)).toBe('granted')
  })
})

describe('the boot', () => {
  beforeEach(() => {
    // `src/main.tsx` does its work at module evaluation, so each case gets a
    // fresh copy of it and a page with the element it mounts into.
    vi.resetModules()
    window.localStorage.clear()
    document.body.innerHTML = '<div id="root"></div>'
  })

  afterEach(() => {
    vi.doUnmock('../src/auth/firebase')
    vi.doUnmock('../src/telemetry')
    vi.unstubAllEnvs()
    document.body.innerHTML = ''
  })

  function root(): HTMLElement {
    const element = document.getElementById('root')
    if (element === null) {
      throw new Error('the case did not leave a #root element')
    }
    return element
  }

  it('mounts the app when the build was given a Firebase configuration', async () => {
    const created = vi.fn(() => Promise.resolve(port()))
    vi.doMock('../src/auth/firebase', async (importOriginal) => ({
      ...(await importOriginal<typeof import('../src/auth/firebase')>()),
      firebaseConfig: () => ({
        apiKey: 'portal-web-api-key',
        authDomain: 'portal.example.test',
        projectId: 'portal-test',
        appId: 'portal-test-app',
      }),
      createFirebaseAuthPort: created,
    }))

    await import('../src/main')

    expect(await screen.findByRole('heading', { name: 'Sign in to Fugaro' })).toBeDefined()
    expect(created).toHaveBeenCalledWith(
      expect.objectContaining({ projectId: 'portal-test' }),
      expect.stringContaining(SIGN_IN_PATH),
    )
  })

  it('says so, rather than mounting, when the build has no Firebase configuration', async () => {
    // The premise: this environment carries no portal configuration, so the
    // real `firebaseConfig` answers null and the branch below is the one taken.
    expect(firebaseConfig(import.meta.env)).toBeNull()

    await import('../src/main')

    await waitFor(() => {
      expect(root().textContent).toContain('no Firebase configuration')
    })
  })

  it('reports a boot that failed, and leaves a sentence rather than an empty page', async () => {
    const report = vi.fn()
    vi.doMock('../src/telemetry', () => ({
      createTelemetry: () => Promise.resolve({ report }),
    }))
    vi.doMock('../src/auth/firebase', async (importOriginal) => ({
      ...(await importOriginal<typeof import('../src/auth/firebase')>()),
      firebaseConfig: () => ({
        apiKey: 'portal-web-api-key',
        authDomain: 'portal.example.test',
        projectId: 'portal-test',
        appId: 'portal-test-app',
      }),
      // The auth chunk failing to load is the boot this catches.
      createFirebaseAuthPort: () => Promise.reject(new Error('the auth chunk did not load')),
    }))

    await import('../src/main')

    await waitFor(() => {
      expect(root().textContent).toBe(STARTED)
    })
    expect(report).toHaveBeenCalledWith(expect.any(Error))
  })

  it('still says so when the reporter is the thing that failed to load', async () => {
    vi.doMock('../src/telemetry', () => ({
      createTelemetry: () => Promise.reject(new Error('the reporter chunk did not load')),
    }))

    await import('../src/main')

    await waitFor(() => {
      expect(root().textContent).toBe(STARTED)
    })
  })
})
