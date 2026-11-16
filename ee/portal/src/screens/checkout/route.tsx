// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The checkout screen's route (portal plan P4.1). It is the whole of what a
// screen has to declare to be mounted: `src/routes.ts` collects
// `src/screens/*/route.tsx`, and the app renders what it finds.
//
// **The workspace is in the path** because a checkout is one workspace's: a
// licence is bought for one, and a screen that carried none would have to guess
// which of a person's workspaces the payment was for -- and the guess is the
// thing F15 found, since a workspace a page names is a workspace a page can
// choose. The name of the parameter is the one the screen reads (`useParams`),
// so the address and the screen are one fact with the browser's URL between
// them.
//
// The path is written here rather than imported from `src/routes.ts` for the
// reason `src/screens/signin/route.tsx` records: an eager `import.meta.glob`
// becomes a static import, so a route file that read the collector's constant
// would be a cycle the bundler resolves with the screen first and the constant
// not yet initialised.
//
// **The API client is built here and the session is answered first.** Both are
// the workspace screen's route's reasons, unchanged: a test mounts the screen
// with a fake client and no provider, and a full page load -- a reload, a
// bookmark -- starts with Firebase having restored nothing, so a screen mounted
// before the session is answered would ask with no token and be left showing
// the API's refusal after every refresh.

import { useMemo } from 'react'
import { Navigate } from 'react-router-dom'
import { createApiClient } from '../../api/client'
import { useSession } from '../../auth/session'
import type { ScreenRoute } from '../../routes'
import { CheckoutScreen } from './checkout'

/** Where somebody with no session belongs. Written here rather than imported
 * for the reason the screen's own path is (see above): the collector is a
 * static import of this file, so its constants are not readable from here. */
const SIGN_IN = '/signin'

function CheckoutRoute() {
  const { state, auth } = useSession()
  const client = useMemo(() => createApiClient({ token: () => auth.idToken() }), [auth])

  if (state.status === 'loading') {
    return (
      <main>
        <h1>Buy a licence</h1>
        <p role="status">Loading…</p>
      </main>
    )
  }

  if (state.status === 'signed-out') {
    return <Navigate to={SIGN_IN} replace />
  }

  // Keyed on the account: the overlay's callbacks belong to the person who
  // opened it, and a purchase started by one is not the next one's to finish.
  return <CheckoutScreen key={state.user.uid} client={client} />
}

export const route: ScreenRoute = {
  path: '/checkout/:workspaceId',
  element: <CheckoutRoute />,
}
