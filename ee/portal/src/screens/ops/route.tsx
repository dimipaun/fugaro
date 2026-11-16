// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The operations screens' route (portal plan P5.3). It is the whole of what
// this area declares to be mounted: `src/routes.ts` collects
// `src/screens/*/route.tsx`, so the operations area adds a directory and edits
// no shared file.
//
// The path is written here and not imported from `src/routes.ts`, for the
// reason `src/screens/signin/route.tsx` gives: an eager `import.meta.glob`
// becomes a static import, and a route file that read the collector's own
// constant would be a cycle the bundler resolves in the wrong order.

import type { ScreenRoute } from '../../routes'
import { OpsScreen } from './ops'

export const route: ScreenRoute = {
  path: '/ops',
  element: <OpsScreen />,
}
