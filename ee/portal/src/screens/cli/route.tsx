// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The `licence login` pages' route (portal plan P3.5; spec §11.3, §11.4).
//
// **Two pages, one directory, because a screen is a directory.** P1.4's
// collector takes `src/screens/*/route.tsx` -- one route file per task -- and
// §11.3's authorize page and §11.4's device-approve page are one task, so this
// file declares the one path both live under and hands the rest of it to the
// two screens (`authorize`, `device`). The path carries a `*` for the same
// reason the app's catch-all does: `/cli/authorize` and `/cli/device` are
// matched below it rather than named here, and a page that is neither is a
// sentence rather than a blank screen.
//
// **The session is waited for before either page is drawn.** A full page load
// -- and this is always one: the terminal opens the browser on this address --
// starts before Firebase has restored anything, so a screen mounted then would
// ask the API with no token and be refused for being slow (`src/api/client.ts`,
// P3.4's route). Loading first costs a moment of the same line and cannot turn
// a slow session into a refusal.
//
// **A visitor with no session is signed in on this page and not sent away.**
// The whole of what these pages work with -- the port, the state, the PKCE
// challenge, the project id -- is in this address's query, and a redirect to
// the sign-in screen would drop every one of them: the person would sign in and
// arrive at a page with nothing to hand over. So the sign-in screen (§Auth) is
// mounted here, on the screen it belongs to, which is what it is built for.
//
// The client is built here rather than in the screens, so each screen is a view
// over one seam and its suite hands in a fake (P3.4's route says the same).

import { useMemo } from 'react'
import { Route, Routes } from 'react-router-dom'
import { createApiClient } from '../../api/client'
import { useSession } from '../../auth/session'
import type { ScreenRoute } from '../../routes'
import { SignInScreen } from '../signin/signin'
import { AuthorizeScreen } from './authorize'
import { DeviceScreen } from './device'
import './cli.css'

export function CliRoute() {
  const { state, auth } = useSession()
  const client = useMemo(() => createApiClient({ token: () => auth.idToken() }), [auth])

  if (state.status === 'loading') {
    return (
      <main className="cli">
        <h1>Hand a licence to a terminal</h1>
        <p role="status">Loading…</p>
      </main>
    )
  }

  if (state.status === 'signed-out') {
    return (
      <div className="cli cli--gate">
        <p className="cli__reason">
          The terminal is waiting for a licence. Sign in to choose which one it gets.
        </p>
        <SignInScreen />
      </div>
    )
  }

  return (
    <Routes>
      <Route path="authorize" element={<AuthorizeScreen client={client} auth={auth} />} />
      <Route path="device" element={<DeviceScreen client={client} />} />
      <Route path="*" element={<UnknownStep />} />
    </Routes>
  )
}

/** A path under this directory that is no step of the login: the person is told
 * so rather than left on a page that renders nothing. */
function UnknownStep() {
  return (
    <main className="cli">
      <h1>Hand a licence to a terminal</h1>
      <p role="alert">
        This is not a step of <code>fugaro licence login</code>. Run it again and use the page it
        opens.
      </p>
    </main>
  )
}

export const route: ScreenRoute = {
  path: '/cli/*',
  element: <CliRoute />,
}
