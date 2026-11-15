// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The app's screens, collected rather than listed (portal plan P1.4).
//
// **A screen is a directory that exports a route, and adding one edits no
// shared file.** `import.meta.glob` collects `src/screens/*/route.tsx` at build
// time, so the ten screen tasks after this one (P2.3, P3.4, P3.5, P4.1, P4.4,
// P5.3, P6.4 and the rest) each add their own directory and nothing else: the
// plan's rule is that no level holds two writers of one file, and a router that
// listed its screens by hand would be that file.
//
// **A screen that does not declare itself is a loud failure, not a missing
// page.** The checks below run when the module is first imported -- in the
// build, and in any test that mounts the app -- so a route file with a typo in
// it fails where somebody is looking, rather than producing an app that quietly
// has no screen for a path. The sort is here for the same kind of reason: the
// glob's order is the file system's, and a router whose order came from a
// directory listing is an app that behaves differently on two machines.

import type { ReactElement } from 'react'

/**
 * Where a visitor with no screen of their own lands, and where the emailed
 * sign-in link returns to (the app hands it to the auth adapter as that link's
 * URL). It is the one place the path is spelled outside the screen's own route
 * file, and the check in `collect` below is what keeps the two the same fact.
 */
export const SIGN_IN_PATH = '/signin'

export interface ScreenRoute {
  /** The path the screen answers, in React Router's own spelling. */
  path: string
  element: ReactElement
}

interface ScreenModule {
  route?: ScreenRoute
}

// The pattern is relative to this file, so `./screens/...` is `src/screens/...`:
// written one level up it collects nothing, and the check below turns that into
// a module that will not load rather than an app with no screens.
const modules = import.meta.glob<ScreenModule>('./screens/*/route.tsx', { eager: true })

function collect(): ScreenRoute[] {
  const collected: ScreenRoute[] = []
  for (const [file, module] of Object.entries(modules)) {
    const route = module.route
    if (route === undefined || typeof route.path !== 'string' || !route.path.startsWith('/')) {
      throw new Error(`${file} must export a route whose path starts with "/"`)
    }
    collected.push(route)
  }
  if (collected.length === 0) {
    throw new Error('src/screens/*/route.tsx collected no screens')
  }
  // A screen writes its own path (see src/screens/signin/route.tsx for why),
  // so this is what keeps SIGN_IN_PATH and the screen that answers it one fact:
  // a rename on either side fails here rather than sending every visitor to a
  // path nothing answers.
  if (!collected.some((route) => route.path === SIGN_IN_PATH)) {
    throw new Error(`no screen answers ${SIGN_IN_PATH}`)
  }
  return collected.sort((left, right) => left.path.localeCompare(right.path))
}

export const routes: readonly ScreenRoute[] = collect()
