// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The sign-in screen (portal plan P1.4; spec §Auth, §Tenancy).
//
// **It offers what will work and nothing that will not.** The methods come
// from `src/auth/sso.ts`, which is D2207's rule as the app applies it: an
// operations organisation is offered Google, a reseller Google and Microsoft
// once operations has pinned a tenant. The API refuses the rest anyway (P1.3);
// this screen is what keeps the refusal from being the way a person finds out.
// A customer organisation -- the ordinary sign-up, and this screen's default --
// is offered every method the project enables.
//
// **The emailed link is completed here, on the screen it returns to.** A link
// that came back from the mail arrives with its code in the query string, and
// the effect below asks the port whether that URL is one and completes it; the
// answer is a signed-in session like any other. That is why the link's URL is
// this screen's path (P1.4's `src/auth/firebase.ts`) and not the app's root,
// where a router redirect would leave the code behind.
//
// **Nothing about a failure is invented here.** A refusal the API wrote is
// shown as the API wrote it -- its one error shape is a sentence for a person
// (P1.0). Anything else, including the provider's own errors, is answered with
// one sentence this screen owns: an SDK's message is written for a developer
// reading a console, and a screen that printed it would be showing a reader
// something nobody wrote for them.

import { useEffect, useState } from 'react'
import { ApiError } from '../../api/client'
import type { OrganisationType } from '../../api/types/orgs'
import { useSession } from '../../auth/session'
import { isFederated, requiresSSO, signInMethods } from '../../auth/sso'
import type { FederatedMethod, SignInMethod } from '../../auth/sso'

const LABELS: Record<SignInMethod, string> = {
  google: 'Continue with Google',
  apple: 'Continue with Apple',
  microsoft: 'Continue with Microsoft',
  emailLink: 'Email me a sign-in link',
}

const GENERIC_FAILURE = 'The portal could not sign you in. Try again.'

/** The sentence a person is shown for a failure. */
function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

export interface SignInScreenProps {
  /**
   * The organisation the visitor is signing in for. The ordinary sign-up --
   * a customer organisation -- is the default, and the invitation flow hands
   * this screen the invited organisation's own type (P2.2, P2.3).
   */
  organisation?: { type: OrganisationType; microsoftTenants?: readonly string[] }
}

export function SignInScreen({ organisation = { type: 'customer' } }: SignInScreenProps) {
  const { state, auth } = useSession()
  const [email, setEmail] = useState('')
  const [sentTo, setSentTo] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const methods = signInMethods(organisation.type, organisation.microsoftTenants)

  useEffect(() => {
    const href = window.location.href
    if (!auth.isSignInLink(href)) {
      return
    }
    void auth.completeSignInLink(href).catch((error: unknown) => {
      setFailure(sentence(error))
    })
  }, [auth])

  async function run(action: () => Promise<void>): Promise<void> {
    setBusy(true)
    setFailure(null)
    try {
      await action()
    } catch (error: unknown) {
      setFailure(sentence(error))
    } finally {
      setBusy(false)
    }
  }

  if (state.status === 'loading') {
    return (
      <main className="signin">
        <p role="status">Loading…</p>
      </main>
    )
  }

  if (state.status === 'signed-in') {
    return (
      <main className="signin">
        <h1>Signed in</h1>
        <p>
          You are signed in as <strong>{state.user.email}</strong>.
        </p>
        <button type="button" disabled={busy} onClick={() => void run(() => auth.signOut())}>
          Sign out
        </button>
        {failure === null ? null : <p role="alert">{failure}</p>}
      </main>
    )
  }

  return (
    <main className="signin">
      <h1>Sign in to Fugaro</h1>
      {requiresSSO(organisation.type) ? (
        <p className="signin__sso">
          This organisation signs in with a work account. The emailed link is not available for it.
        </p>
      ) : null}

      <div className="signin__methods">
        {methods.filter(isFederated).map((method) => (
          <button
            key={method}
            type="button"
            disabled={busy}
            onClick={() => void run(() => auth.signIn(method as FederatedMethod))}
          >
            {LABELS[method]}
          </button>
        ))}
      </div>

      {methods.includes('emailLink') ? (
        sentTo === null ? (
          <form
            className="signin__email"
            onSubmit={(event) => {
              event.preventDefault()
              const address = email.trim()
              void run(async () => {
                await auth.sendSignInLink(address)
                setSentTo(address)
              })
            }}
          >
            <label htmlFor="signin-email">Email address</label>
            <input
              id="signin-email"
              name="email"
              type="email"
              autoComplete="email"
              required
              value={email}
              onChange={(event) => {
                setEmail(event.target.value)
              }}
            />
            <button type="submit" disabled={busy}>
              {LABELS.emailLink}
            </button>
          </form>
        ) : (
          <p role="status">
            A sign-in link is on its way to <strong>{sentTo}</strong>. Open it in this browser.
          </p>
        )
      ) : null}

      {failure === null ? null : <p role="alert">{failure}</p>}
    </main>
  )
}
