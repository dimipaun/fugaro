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
// **The port is built from the session, so nothing is read without one.** The
// port exists only while somebody is signed in, and its identity follows the
// session state: an API client built while the session is still loading would
// ask `idToken` before there is a token -- `src/auth/session.tsx` answers what
// the SDK has told it, which is nothing yet -- and the screen's own effect keys
// on the port it is handed, so a port that outlived the session it was built
// for would leave the first refusal on screen for ever, sign-out included. The
// screen is therefore mounted with the session and unmounted with it, which is
// also what takes a signed-out person's figures off the page.
//
// The path is spelled here rather than read from `src/routes.ts` for the reason
// the sign-in screen's route gives: an eager glob is a static import, so a route
// file that read the collector's own constant would be a cycle the bundler
// resolves with the constant not yet initialised.

import { useMemo } from 'react'
import { Navigate } from 'react-router-dom'
import { createApiClient } from '../../api/client'
import { useSession } from '../../auth/session'
import { resellerApi } from './api'
import { ResellerScreen } from './reseller'
import type { ScreenRoute } from '../../routes'

/**
 * Where a visitor with no session of their own is sent: the path
 * `src/routes.ts` holds as `SIGN_IN_PATH` and the sign-in screen answers. It is
 * the second spelling of that path outside the screen that owns it, and
 * `reseller.test.tsx` holds the two against each other -- a route that sent a
 * signed-out visitor anywhere else would be the catch-all's redirect, not this
 * screen's.
 */
const SIGN_IN = '/signin'

function SignedInResellerScreen() {
  const { state, auth } = useSession()
  const api = useMemo(
    () =>
      state.status === 'signed-in'
        ? resellerApi(createApiClient({ token: () => auth.idToken() }))
        : null,
    [state, auth],
  )

  // The state is named as well as the port, because the person's uid is what
  // the screen below is keyed on: nothing here may read a user out of a state
  // that has not got one.
  if (state.status !== 'signed-in' || api === null) {
    // "Not answered yet" is not "signed out": a screen that treated the two
    // alike would send somebody who is already signed in back to sign in
    // (`src/auth/session.tsx` says the same about the state it publishes).
    return state.status === 'loading' ? (
      <main className="reseller">
        <p role="status">Loading…</p>
      </main>
    ) : (
      <Navigate to={SIGN_IN} replace />
    )
  }

  // The key is the person, and it is what makes a switch between two accounts a
  // switch of screens. `src/auth/session.tsx` publishes the second `signed-in`
  // with no `signed-out` between them, so a new port alone would leave the
  // screen the instance it was: its effect would read again, but the `ready`
  // state it drew the first account's figures from would be on screen until
  // those reads answered -- or for ever, if they never did. Keyed this way the
  // old instance goes in the same commit that publishes the new session, so the
  // status is what is drawn, not the person who has already gone.
  return <ResellerScreen key={state.user.uid} api={api} />
}

export const route: ScreenRoute = {
  path: '/reseller',
  element: <SignedInResellerScreen />,
}
