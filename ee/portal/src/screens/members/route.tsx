// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The members screen's route (portal plan P2.3), declared the way
// `src/screens/signin/route.tsx` explains: one directory exports a route,
// `src/routes.ts` collects it, and nothing shared lists it.
//
// The path carries no organisation: which one the screen is about is the
// query's (`?org=`, put there by the switcher), so the route is one path for
// every organisation a person belongs to rather than a pattern the collector
// would have to match.

import { MembersScreen } from './members'
import type { ScreenRoute } from '../../routes'

export const route: ScreenRoute = {
  path: '/members',
  element: <MembersScreen />,
}
