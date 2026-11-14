// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The organisations screen's route (portal plan P2.3). It is the whole of what
// a screen has to declare to be mounted: `src/routes.ts` collects
// `src/screens/*/route.tsx`, and the app renders what it finds.
//
// The path is written here rather than imported from `src/routes.ts` for the
// reason `src/screens/signin/route.tsx` gives: an eager `import.meta.glob`
// becomes a static import, so a route file that read the collector's constant
// would be a cycle the bundler resolves in the wrong order.

import { OrgsScreen } from './orgs'
import type { ScreenRoute } from '../../routes'

export const route: ScreenRoute = {
  path: '/orgs',
  element: <OrgsScreen />,
}
