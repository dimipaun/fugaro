// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The workspace screen's route (portal plan P3.4). It is the whole of what a
// screen has to declare to be mounted: `src/routes.ts` collects
// `src/screens/*/route.tsx`, and the app renders what it finds.
//
// The path is written here rather than imported from `src/routes.ts` for the
// reason the sign-in screen's route gives: an eager `import.meta.glob` becomes a
// static import, so a route file that read the collector's constant would be a
// cycle the bundler resolves with the screen first and the constant not yet
// initialised.
//
// **The API client is built here and not in the screen.** It needs the current
// ID token, which is the session's, and a screen that read the session itself
// would need a provider in every test that mounts it; built here, the screen is
// a view over one seam and its suite hands in a fake. The client asks for the
// token per request rather than holding one (`src/api/client.ts`), which is why
// the closure is over the port and not over a token.
//
// **And the session is answered here before the screen is mounted at all.** A
// full page load -- a reload, a bookmark, the return from a checkout -- starts
// with Firebase having restored nothing yet, so the first render happens with
// `currentUser` null and `idToken()` answers null (`src/auth/firebase.ts`). A
// screen mounted then asks for its two lists with no token, the API refuses
// both, and because the screen's own read runs once over a client that never
// changes, that refusal is what the person is left looking at after every
// refresh. Waiting for the answer here costs a moment of the same loading line
// and cannot turn a slow session into a refusal.

import { useMemo } from 'react'
import { Navigate } from 'react-router-dom'
import { createApiClient } from '../../api/client'
import { useSession } from '../../auth/session'
import type { ScreenRoute } from '../../routes'
import { WorkspacesScreen } from './workspaces'

/** Where somebody with no session belongs. Written here rather than imported
 * for the reason the screen's own path is (see above): the collector is a
 * static import of this file, so its constants are not readable from here. */
const SIGN_IN = '/signin'

function WorkspacesRoute() {
  const { state, auth } = useSession()
  const client = useMemo(() => createApiClient({ token: () => auth.idToken() }), [auth])

  if (state.status === 'loading') {
    return (
      <main className="workspaces">
        <h1>Workspaces</h1>
        <p role="status">Loading…</p>
      </main>
    )
  }

  if (state.status === 'signed-out') {
    return <Navigate to={SIGN_IN} replace />
  }

  // Keyed on the account, so nothing the screen collected -- the two lists, and
  // the bundles of the keys it has revealed -- outlives the person it was read
  // for.
  return <WorkspacesScreen key={state.user.uid} client={client} />
}

export const route: ScreenRoute = {
  path: '/workspaces',
  element: <WorkspacesRoute />,
}
