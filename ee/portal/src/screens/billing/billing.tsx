// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The billing screen (portal plan P4.4; spec §Customer experience, D2192).
//
// **It mirrors and never decides.** Paddle is where the subscription lives:
// invoices, payment methods and cancellation live in Paddle's own customer
// portal, and what is here is the state the API last read from Paddle's events.
// So every number on this page is a fact this screen was told -- the seat count
// is Paddle's quantity, the renewal date is Paddle's instant formatted and not
// a date worked out here, and the state is Paddle's own word, which is why it
// is shown as it arrives rather than translated.
//
// **The seat field is the whole quantity, not an increment.** That is the wire's
// shape (`SubscriptionSeats`), and it is what makes a request whose reply was
// lost safe to send again: a customer who types the number they believe they are
// buying cannot buy it twice by reloading. It is also why nothing here disables
// the button while a request is out.
//
// **Paddle's customer portal is asked for when it is wanted, and only followed
// when it is https.** A portal session is minted per request and goes stale, so
// one fetched at page load is a link that may already be dead when it is
// clicked; and a URL that arrived over the API is followed only if it is
// `https:`, because an answer that named `javascript:` would be script this
// screen would otherwise put in an `href` and run.
//
// **Nothing is asked for until the session has been answered.** The provider
// starts at "not answered yet" and the SDK answers late -- the stored session is
// read before it does -- so a request made at mount would carry no token at all
// (`src/auth/firebase.ts`'s `idToken` reads the current user, which is nobody
// until then) and the API's refusal would be what a signed-in customer saw, for
// good, since nothing here asks again. So the load waits for an answer, and the
// token is asked for per request as the client intends.
//
// **Who may change the seats is not this screen's check.** Roles live in the
// memberships the API reads (P2.1), so a member who is refused is refused by the
// API, and the refusal is what a person is shown. Nothing about a failure is
// invented here: a sentence the API wrote is shown as it wrote it, and anything
// else -- a dropped connection, a Paddle outage -- becomes one sentence this
// screen owns rather than a transport error a reader cannot act on.

import { useEffect, useMemo, useState } from 'react'
import type { FormEvent, ReactNode } from 'react'
import { useParams } from 'react-router-dom'
import { ApiError, createApiClient } from '../../api/client'
import type { Subscription, SubscriptionSeats } from '../../api/types/billing'
import { useSession } from '../../auth/session'

/**
 * The API's routes for this area (P4.3 serves them, `ee/keyservice/api_billing.go`).
 * A workspace id is encoded rather than pasted: an id carrying a slash would
 * otherwise widen the path, and the client refuses a path that leaves the
 * origin only after this has joined it.
 */
const ROUTES = {
  subscription: (workspaceId: string) => `/workspaces/${encodeURIComponent(workspaceId)}/subscription`,
  seats: (workspaceId: string) => `/workspaces/${encodeURIComponent(workspaceId)}/subscription/seats`,
  portal: (workspaceId: string) => `/workspaces/${encodeURIComponent(workspaceId)}/subscription/portal`,
}

/** What the API answers when the screen asks for Paddle's customer portal. */
interface CustomerPortal {
  url: string
}

const SEATS_LABEL = 'New seat count'
const GENERIC_FAILURE = 'The portal could not read your billing. Try again.'
const PORTAL_FAILURE = 'The portal could not open Paddle’s customer portal. Try again.'
const BAD_QUANTITY = 'Enter a whole number of seats, one or more.'
const NO_WORKSPACE = 'The portal opened billing without a workspace.'
const SIGN_IN_NEEDED = 'Sign in to see this workspace’s billing.'

// The renewal day is UTC because Paddle's instant is: a customer in Auckland
// reading a day this browser had shifted would be shown a renewal on a date
// Paddle's own portal does not agree with. The locale is pinned for the same
// reason -- a date whose wording followed the machine would be a different
// sentence in a test than in a bug report.
const RENEWAL_DAY = new Intl.DateTimeFormat('en-GB', {
  day: 'numeric',
  month: 'long',
  year: 'numeric',
  timeZone: 'UTC',
})

function day(stamp: string): string {
  return RENEWAL_DAY.format(new Date(stamp))
}

/** The sentence a person is shown for a failure. */
function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

/** Paddle's URL, or null for anything this screen will not follow. */
function httpsUrl(url: string): string | null {
  try {
    const parsed = new URL(url)
    return parsed.protocol === 'https:' ? parsed.href : null
  } catch {
    return null
  }
}

export function BillingScreen() {
  const { state, auth } = useSession()
  const workspaceId = useParams<{ workspaceId: string }>().workspaceId
  const api = useMemo(() => createApiClient({ token: () => auth.idToken() }), [auth])
  const signedIn = state.status === 'signed-in'

  const [subscription, setSubscription] = useState<Subscription | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [seats, setSeats] = useState('')
  const [portal, setPortal] = useState<string | null>(null)
  const [waiting, setWaiting] = useState(workspaceId !== undefined)

  useEffect(() => {
    if (workspaceId === undefined || !signedIn) {
      return
    }
    let live = true
    void api
      .request<Subscription>(ROUTES.subscription(workspaceId))
      .then((answer) => {
        if (!live) {
          return
        }
        setSubscription(answer)
        setSeats(String(answer.seats))
      })
      .catch((error: unknown) => {
        if (live) {
          setFailure(sentence(error))
        }
      })
      .finally(() => {
        if (live) {
          setWaiting(false)
        }
      })
    return () => {
      live = false
    }
  }, [api, workspaceId, signedIn])

  function buy(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault()
    if (workspaceId === undefined) {
      return
    }
    const quantity = Number(seats.trim())
    if (!Number.isInteger(quantity) || quantity < 1) {
      setFailure(BAD_QUANTITY)
      return
    }
    setFailure(null)
    setNotice(null)
    const body: SubscriptionSeats = { seats: quantity }
    void api
      .request<Subscription>(ROUTES.seats(workspaceId), { method: 'POST', body })
      .then((answer) => {
        setSubscription(answer)
        setSeats(String(answer.seats))
        setNotice(`The subscription now has ${answer.seats} seats.`)
      })
      .catch((error: unknown) => {
        setFailure(sentence(error))
      })
  }

  function askForPortal(): void {
    if (workspaceId === undefined) {
      return
    }
    setFailure(null)
    void api
      .request<CustomerPortal>(ROUTES.portal(workspaceId))
      .then((answer) => {
        const url = httpsUrl(answer.url)
        if (url === null) {
          setFailure(PORTAL_FAILURE)
          return
        }
        setPortal(url)
      })
      .catch((error: unknown) => {
        setFailure(sentence(error))
      })
  }

  function renewal(): ReactNode {
    if (subscription === null || subscription.renews_at === null) {
      return 'Does not renew'
    }
    return <time dateTime={subscription.renews_at}>{day(subscription.renews_at)}</time>
  }

  return (
    <main>
      <h1>Billing</h1>
      {workspaceId === undefined ? (
        <p role="alert">{NO_WORKSPACE}</p>
      ) : state.status === 'signed-out' ? (
        <p>{SIGN_IN_NEEDED}</p>
      ) : waiting ? (
        <p role="status">Loading…</p>
      ) : null}
      {failure === null ? null : <p role="alert">{failure}</p>}

      {subscription === null ? null : (
        <>
          <dl>
            <dt>Status</dt>
            <dd>{subscription.state}</dd>
            <dt>Seats</dt>
            <dd>{subscription.seats}</dd>
            <dt>Renews on</dt>
            <dd>{renewal()}</dd>
            {subscription.ends_at === null ? null : (
              <>
                <dt>Ends on</dt>
                <dd>
                  <time dateTime={subscription.ends_at}>{day(subscription.ends_at)}</time>
                </dd>
              </>
            )}
          </dl>

          <form onSubmit={buy}>
            <label htmlFor="billing-seats">{SEATS_LABEL}</label>
            <input
              id="billing-seats"
              name="seats"
              type="number"
              min="1"
              step="1"
              value={seats}
              onChange={(event) => {
                setSeats(event.target.value)
              }}
            />
            <button type="submit">Update seats</button>
          </form>

          <div>
            <button type="button" className="secondary" onClick={askForPortal}>
              Invoices and payment methods
            </button>
            {portal === null ? null : (
              <p>
                <a href={portal} target="_blank" rel="noopener noreferrer">
                  Open Paddle’s customer portal
                </a>
              </p>
            )}
          </div>
        </>
      )}

      {notice === null ? null : <p role="status">{notice}</p>}
    </main>
  )
}
