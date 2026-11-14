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

import { useMemo } from 'react'
import { createApiClient } from '../../api/client'
import { useSession } from '../../auth/session'
import type { ScreenRoute } from '../../routes'
import { WorkspacesScreen } from './workspaces'

function WorkspacesRoute() {
  const { auth } = useSession()
  const client = useMemo(() => createApiClient({ token: () => auth.idToken() }), [auth])
  return <WorkspacesScreen client={client} />
}

export const route: ScreenRoute = {
  path: '/workspaces',
  element: <WorkspacesRoute />,
}
