// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The organisation settings screen's route (portal plan P2.3), declared the way
// `src/screens/signin/route.tsx` explains. The organisation it draws is the
// query's `?org=`, as on the members screen.

import { SettingsScreen } from './settings'
import type { ScreenRoute } from '../../routes'

export const route: ScreenRoute = {
  path: '/settings',
  element: <SettingsScreen />,
}
