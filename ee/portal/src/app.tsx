// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The app itself: the router, the consent gate, and nothing else (portal plan
// P1.4).
//
// **The screens are mounted and never listed.** Every route in `src/routes.ts`
// is rendered here, and the catch-all sends anybody with no screen of their own
// to the sign-in screen -- which is where the emailed link's code lands, and why
// the link's URL is that path rather than the root. `src/main.tsx` wraps this in
// a `SessionProvider`; nothing below the router reads Firebase.
//
// **The consent gate is here because it is the shell's, not a screen's.** The
// decision applies to the whole app, so the banner is drawn once above the
// routes and the stored decision is applied when the app mounts: a returning
// visitor who granted is measured from that moment (and `applyConsent` starts
// nothing for a visitor who never decided). Where a person changes their mind
// later is the account screen (P2.4), which is the one place a setting that is
// not in this file belongs.
//
// The privacy notice the banner points at is the website's statement, which is
// where D93's promise about the tags lives; the portal does not restate it.

import { useEffect, useMemo, useState } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import {
  applyConsent,
  browserConsentStore,
  firebaseAnalyticsClient,
  grantConsent,
  measurementId,
  refuseConsent,
} from './consent'
import { SIGN_IN_PATH, routes } from './routes'

const PRIVACY_NOTICE = 'https://fugaro.dev/privacy/'

export function App() {
  const store = useMemo(() => browserConsentStore(), [])
  const analytics = useMemo(() => firebaseAnalyticsClient(measurementId(import.meta.env)), [])
  const [decided, setDecided] = useState(() => store.read() !== null)

  useEffect(() => {
    applyConsent(store, analytics)
  }, [store, analytics])

  function decide(decision: 'granted' | 'refused') {
    if (decision === 'granted') {
      grantConsent(store, analytics)
    } else {
      refuseConsent(store, analytics)
    }
    setDecided(true)
  }

  return (
    <BrowserRouter>
      {decided ? null : <ConsentBanner onDecide={decide} />}
      <Routes>
        {routes.map((route) => (
          <Route key={route.path} path={route.path} element={route.element} />
        ))}
        <Route path="*" element={<Navigate to={SIGN_IN_PATH} replace />} />
      </Routes>
    </BrowserRouter>
  )
}

function ConsentBanner({ onDecide }: { onDecide: (decision: 'granted' | 'refused') => void }) {
  return (
    <aside className="consent" aria-label="Analytics consent">
      <p className="consent__text">
        The portal can send Google figures about how it is used, to see what breaks. Nothing is
        sent until you accept, and declining changes nothing about the app. What is kept, and for
        how long, is in the <a href={PRIVACY_NOTICE}>privacy notice</a>.
      </p>
      <div className="consent__choices">
        <button type="button" onClick={() => onDecide('granted')}>
          Accept analytics
        </button>
        <button type="button" className="secondary" onClick={() => onDecide('refused')}>
          No analytics
        </button>
      </div>
    </aside>
  )
}
