// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The account screen's route (portal plan P2.4), declared the way
// `src/screens/signin/route.tsx` explains: one directory exports a route,
// `src/routes.ts` collects it, and nothing shared lists it.
//
// The path carries no organisation, as the members and settings screens' do
// not: `?org=` is what the switcher put there and names the organisation this
// page may delete, and the account's own half needs no organisation at all.

import { AccountScreen } from './account'
import type { ScreenRoute } from '../../routes'

export const route: ScreenRoute = {
  path: '/account',
  element: <AccountScreen />,
}
