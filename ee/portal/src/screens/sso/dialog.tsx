// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The dialog an operations or reseller organisation opens when the identity in
// front of it cannot reach one (portal plan P2.3; spec §Auth, D2207).
//
// **The organisation stays selectable, and the dialog is the courtesy.** The
// API refuses an emailed-link or password identity for a membership of one of
// these organisations on every request that uses it (P1.3, P2.1), so what this
// buys is that the refusal is not the way a person finds out. The methods it
// offers are `src/auth/sso.ts`'s -- the same rule the sign-in screen offers --
// so it cannot offer one the API would turn away.
//
// **It is a dialog and not a screen, which is why it has no route.** The spec
// has it open over the organisation the person just chose, and a path would be
// a second place the rule is read from, describing an organisation the URL does
// not name.
//
// A failure here is one sentence of this file's own, for the reason the sign-in
// screen gives: the SDK's message is written for a developer reading a console,
// and the API is not what answered.

import { useId, useState } from 'react'
import type { OrganisationType } from '../../api/types/orgs'
import { useSession } from '../../auth/session'
import { isFederated, signInMethods } from '../../auth/sso'

const LABELS = {
  google: 'Sign in with Google',
  apple: 'Sign in with Apple',
  microsoft: 'Sign in with Microsoft',
} as const

const GENERIC_FAILURE = 'The portal could not sign you in. Try again.'

export interface SsoOrganisation {
  type: OrganisationType
  /**
   * Operations' pinned Microsoft directories for this organisation (P5.1). None
   * means none is pinned, and Microsoft is then not offered: a sign-in the API
   * cannot check against a directory is one it refuses.
   */
  microsoftTenants?: readonly string[]
}

export function SsoDialog({
  organisation,
  onDismiss,
}: {
  organisation: SsoOrganisation
  onDismiss: () => void
}) {
  const { auth } = useSession()
  const heading = useId()
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const methods = signInMethods(organisation.type, organisation.microsoftTenants).filter(
    isFederated,
  )

  async function signIn(method: (typeof methods)[number]): Promise<void> {
    setBusy(true)
    setFailure(null)
    try {
      await auth.signIn(method)
    } catch {
      setFailure(GENERIC_FAILURE)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div role="dialog" aria-modal="true" aria-labelledby={heading}>
      <h2 id={heading}>This organisation signs in with a work account</h2>
      <p>The emailed sign-in link is refused for it. Sign in again with one of these.</p>
      {methods.map((method, index) => (
        <button
          key={method}
          type="button"
          disabled={busy}
          autoFocus={index === 0}
          onClick={() => void signIn(method)}
        >
          {LABELS[method]}
        </button>
      ))}
      <button type="button" className="secondary" onClick={onDismiss}>
        Not now
      </button>
      {failure === null ? null : <p role="alert">{failure}</p>}
    </div>
  )
}
