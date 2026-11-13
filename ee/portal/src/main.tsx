// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Where the app starts (portal plan P1.4). It is the only file that touches the
// document, and the only one that builds the two things the whole app shares:
// the telemetry reporter and the auth port. Everything below it is given what
// it needs.
//
// **A build with no Firebase configuration says so instead of pretending.** The
// portal's configuration reaches the bundle as Vite environment variables
// (P9.2's build supplies them); without them the sign-in screen could only ever
// fail, and a notice in the page is a truer answer than a form that cannot
// work.
//
// **The reporter is wired to the two events nothing else catches**: an uncaught
// error and an unhandled rejection. A failure inside React's own render is an
// error boundary's business and belongs to the screens that have state worth
// keeping on it (P2.3), not here -- and an `unhandledrejection` is exactly where
// an API call that failed comes out, which is the failure the owner most wants
// to hear about.

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './app'
import { createFirebaseAuthPort, firebaseConfig } from './auth/firebase'
import { SessionProvider } from './auth/session'
import { SIGN_IN_PATH } from './routes'
import { createTelemetry } from './telemetry'
import './styles.css'

async function start(): Promise<void> {
  const root = document.getElementById('root')
  if (root === null) {
    throw new Error('index.html has no #root element to mount into')
  }

  const telemetry = await createTelemetry({
    dsn: import.meta.env.VITE_SENTRY_DSN ?? null,
    environment: import.meta.env.VITE_PORTAL_ENVIRONMENT ?? 'development',
    release: import.meta.env.VITE_PORTAL_RELEASE,
  })
  window.addEventListener('error', (event: ErrorEvent) => {
    telemetry.report(event.error ?? event.message)
  })
  window.addEventListener('unhandledrejection', (event: PromiseRejectionEvent) => {
    telemetry.report(event.reason)
  })

  const config = firebaseConfig(import.meta.env)
  if (config === null) {
    root.textContent =
      'This build of the portal has no Firebase configuration, so nobody can sign in.'
    return
  }

  const port = await createFirebaseAuthPort(config, window.location.origin + SIGN_IN_PATH)
  createRoot(root).render(
    <StrictMode>
      <SessionProvider port={port}>
        <App />
      </SessionProvider>
    </StrictMode>,
  )
}

void start()
