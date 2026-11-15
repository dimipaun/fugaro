// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The route the two pages hang off (portal plan P3.5).
//
// A screen is a directory and a route file is what mounts it (P1.4), and this
// task is the one that has two pages under one directory, so the file is worth
// its own cases: that the session is waited for before either page is drawn --
// a full page load starts before Firebase has restored anything, and a read
// made then is refused rather than late -- that a visitor with no session is
// signed in on this page and not sent away from it, which is the difference
// between keeping §11.3's parameters and dropping them, and that each step
// answers its own path.
//
// The route builds its own client from the session, so these cases watch
// `fetch` itself rather than handing in a fake `ApiClient`: what they check is
// the transport (the bearer token) as much as the call.

import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { SessionProvider } from '../../auth/session'
import { route } from './route'
import { INSTALLATION, pending, session, wire } from './testkit'

afterEach(cleanup)

function address(): string {
  const params = new URLSearchParams({
    port: '53123',
    state: 'state-fixture',
    code_challenge: 'challenge-fixture',
    code_challenge_method: 'S256',
    project_id: INSTALLATION,
  })
  return `/cli/authorize?${params.toString()}`
}

/** The route, mounted the way `src/app.tsx` mounts it, with a probe that says
 * where the router thinks it is. */
function showRoute(visit: ReturnType<typeof session>, at: string): void {
  render(
    <SessionProvider port={visit.port}>
      <MemoryRouter initialEntries={[at]}>
        <Where />
        <Routes>
          <Route path={route.path} element={route.element} />
        </Routes>
      </MemoryRouter>
    </SessionProvider>,
  )
}

function Where() {
  const location = useLocation()
  return <p>{`the router is at ${location.pathname}${location.search}`}</p>
}

describe('the licence login route', () => {
  it('waits for the session before it asks the API anything', async () => {
    const calls = wire([pending()])
    const visit = session()
    showRoute(visit, address())

    // A read made before the session was restored carries no token, and the
    // API answers it with a refusal the page would have to show a person for
    // something that was only slow.
    expect(screen.getByText('Loading…')).not.toBeNull()
    expect(calls).toEqual([])
    expect(screen.queryByRole('button', { name: 'Hand over the licence' })).toBeNull()

    act(() => {
      visit.signedIn()
    })

    expect(await screen.findByRole('button', { name: 'Hand over the licence' })).not.toBeNull()
    expect(calls.map((call) => call.method)).toEqual(['GET'])
    expect(calls[0]?.headers.Authorization).toBeDefined()
  })

  it('signs a visitor in on this page rather than sending them away from it', () => {
    const calls = wire([pending()])
    const visit = session()
    showRoute(visit, '/cli/device?user_code=TESTCODE')

    act(() => {
      visit.signedOut()
    })

    // The sign-in screen is mounted here, and the address the terminal opened
    // -- the code the person is about to type in is not this page's, but the
    // port and the parameters above are -- is still under the router.
    expect(screen.getByRole('button', { name: 'Continue with Google' })).not.toBeNull()
    expect(screen.getByText('the router is at /cli/device?user_code=TESTCODE')).not.toBeNull()
    expect(calls).toEqual([])
  })

  it('answers the device step with the device page', () => {
    // One route per directory (P1.4) and this directory has two pages, so the
    // path is the one both live under and the steps below it are matched here:
    // a file per step would be two writers of one path, and a path that named
    // only `/cli/authorize` would leave §11.4's page reachable by nothing.
    const calls = wire([pending()])
    const visit = session()
    showRoute(visit, '/cli/device')

    act(() => {
      visit.signedIn()
    })

    expect(screen.getByLabelText('Code')).not.toBeNull()
    expect(calls).toEqual([])
  })

  it('tells a person that a path under it is no step of the login', () => {
    const calls = wire([pending()])
    const visit = session()
    showRoute(visit, '/cli/nonsense')

    act(() => {
      visit.signedIn()
    })

    expect(screen.getByRole('alert').textContent).toBe(
      'This is not a step of fugaro licence login. Run it again and use the page it opens.',
    )
    expect(calls).toEqual([])
  })
})
