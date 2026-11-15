// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The billing screen's route (portal plan P4.4). It is the whole of what a
// screen has to declare to be mounted: `src/routes.ts` collects
// `src/screens/*/route.tsx`, and the app renders what it finds.
//
// **The workspace is in the path** because billing is one workspace's: the
// subscription, its seats and its renewal date all belong to one, and a screen
// that carried no workspace would have to guess which of a customer's
// workspaces it was showing. The parameter's name is the one the screen reads
// (`useParams`), so the two are one fact with the browser's URL between them.
//
// The path is written here rather than imported from `src/routes.ts`, for the
// reason `src/screens/signin/route.tsx` records: an eager `import.meta.glob`
// becomes a static import, and a route file that read the collector's own
// module would be a cycle the bundler resolves in the wrong order.

import { BillingScreen } from './billing'
import type { ScreenRoute } from '../../routes'

export const route: ScreenRoute = {
  path: '/billing/:workspaceId',
  element: <BillingScreen />,
}
