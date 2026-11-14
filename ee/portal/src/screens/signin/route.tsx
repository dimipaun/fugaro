// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The sign-in screen's route (portal plan P1.4). It is the whole of what a
// screen has to declare to be mounted: `src/routes.ts` collects
// `src/screens/*/route.tsx`, and the app renders what it finds.
//
// The path is written here rather than imported from `src/routes.ts`, and that
// is not a preference: an eager `import.meta.glob` becomes a static import, so
// a route file that read the collector's constant would be a cycle the bundler
// resolves in the wrong order (the screen first, with the constant not yet
// initialised). `src/routes.ts` holds the same path as `SIGN_IN_PATH`, and it
// refuses to start when no collected screen answers it -- so the two are one
// fact with a check between them rather than two facts.

import { SignInScreen } from './signin'
import type { ScreenRoute } from '../../routes'

export const route: ScreenRoute = {
  path: '/signin',
  element: <SignInScreen />,
}
