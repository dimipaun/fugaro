// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The reseller screen's route (portal plan P6.4). It is the whole of what a
// screen has to declare to be mounted: `src/routes.ts` collects
// `src/screens/*/route.tsx` with an eager `import.meta.glob`, and the app renders
// what it finds, so this task adds a directory and edits no shared file.
//
// **The wiring belongs here and not in the screen.** `ResellerScreen` is handed
// its port, and this file is what builds the one a signed-in person reads
// through: `resellerApi` over the API client, whose token function asks the
// session for the current token at every request (`src/api/client.ts` holds a
// function for exactly that reason). A screen that read the session itself
// could not be rendered without one, which is the opposite of what its suite
// needs.
//
// The path is spelled here rather than read from `src/routes.ts` for the reason
// the sign-in screen's route gives: an eager glob is a static import, so a route
// file that read the collector's own constant would be a cycle the bundler
// resolves with the constant not yet initialised.

import { useMemo } from 'react'
import { createApiClient } from '../../api/client'
import { useSession } from '../../auth/session'
import { resellerApi } from './api'
import { ResellerScreen } from './reseller'
import type { ScreenRoute } from '../../routes'

function SignedInResellerScreen() {
  const { auth } = useSession()
  const api = useMemo(() => resellerApi(createApiClient({ token: () => auth.idToken() })), [auth])
  return <ResellerScreen api={api} />
}

export const route: ScreenRoute = {
  path: '/reseller',
  element: <SignedInResellerScreen />,
}
