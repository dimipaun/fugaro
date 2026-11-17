// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The checkout screen (portal plan P4.1; spec §Customer experience, F15, D2192).
//
// **The API names the transaction, and the browser names nothing.** What is
// bought -- the price, the organisation, and the workspace Paddle carries in
// its `custom_data` -- is built server-side (`POST /api/checkout`), because a
// page that could name a workspace could buy a licence against a victim's
// account and then have it refunded (F15). This screen sends the workspace it
// was opened for and the version of the terms it accepted, and hands Paddle's
// overlay the transaction id the API answered: no price, no amount, no custom
// data. The card is entered in Paddle's own frame and never in this page.
//
// **The acceptance recorded here is the buyer's, and it is part of the order.**
// Counsel's terms bind a Customer when it accepts them online as it buys
// (subscription terms §1.2(b)), so the box below is not a receipt drawn up
// afterwards: the version it names travels with the request and is stored with
// the transaction (P4.1's record, which P4.2 reads an event back through). A
// request that names no version is refused by the API, which is why the
// disabled button is not the only guard -- a disabled button does not refuse a
// dispatched event.
//
// **The version is a date, and it is written out here.** The terms are amended
// as a new dated version (§29.1), so what a person accepted is the version they
// were shown and not "the current terms": a re-worded page under one version
// would make yesterday's acceptance an acceptance of something nobody read.
//
// **The overlay is reached through the library and never through a tag.**
// `index.html` admits no third-party script and the CSP is P9.2's, so Paddle's
// Checkout is loaded by `@paddle/paddle-js` when the payment is asked for
// rather than at page load. What it is configured with is the deployment's: the
// client-side token is the build's (`VITE_PADDLE_CLIENT_TOKEN`, public by
// design and not a secret) and the environment follows the portal's own, so a
// staging portal sells in Paddle's sandbox and cannot reach the live account
// (P0.2).
//
// **Nothing about a failure is invented.** A refusal the API wrote is shown as
// the API wrote it -- its one error shape is a sentence for a person (P1.0) --
// and everything else is one sentence this screen owns.

import { initializePaddle, CheckoutEventNames } from '@paddle/paddle-js'
import type { Environments } from '@paddle/paddle-js'
import { useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ApiError } from '../../api/client'
import type { ApiClient } from '../../api/client'
import type { CheckoutCreate, CheckoutSession } from '../../api/types/checkout'

/** The API's route for this area (`api_checkout.go`). */
const CHECKOUT = '/checkout'

/** Where a paid checkout lands: the workspace screen's own marker (P3.4), the
 * one that tells a person to run `fugaro licence refresh`. A purchase reaches
 * the installation through that command and never through the browser. */
const AFTER_PAYMENT = '/workspaces?licence=changed'

/** The version of the licence terms a buyer accepts here, and where they are
 * published. The date is counsel's document of 2026-10-28, which is the text
 * live at that URL. */
export const TERMS_VERSION = '2026-10-28'
export const TERMS_URL = 'https://fugaro.dev/legal/subscription-terms'

const GENERIC_FAILURE = 'The portal could not start the checkout. Try again.'
const OVERLAY_FAILURE = 'The portal could not open Paddle’s payment window. Try again.'
const NO_TRANSACTION = 'The portal did not say which transaction to pay. Try again.'
const NO_WORKSPACE = 'The portal opened checkout without a workspace.'

/** The sentence a person is shown for a failure. */
function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

/**
 * The workspace the path names, or undefined for a path this screen will not
 * ask the API about. `.` and `..` are not ids but dot segments: joined into a
 * route they are normalised away before the request goes out, so a workspace of
 * `..` would ask the API about a different route entirely, with this person's
 * token on it. They are refused exactly as a missing workspace is.
 *
 * The same three lines the billing screen carries: the two screens are two
 * tasks' files and neither may edit the other's, so the guard is written twice
 * rather than one screen reaching into the other's module.
 */
function workspaceIdIn(path: string | undefined): string | undefined {
  return path === '.' || path === '..' ? undefined : path
}

/**
 * Paddle's environment as this deployment sells in.
 *
 * It follows the portal's own environment rather than a second setting that
 * could disagree with it: a staging portal whose overlay pointed at the live
 * account would take a real card for a test purchase, and the two values would
 * be one deploy's facts kept in two places.
 */
function paddleEnvironment(portal: unknown): Environments {
  return portal === 'production' ? 'production' : 'sandbox'
}

/**
 * Paddle's overlay, opened for one transaction, answering when Paddle says the
 * payment is done.
 *
 * The token and the environment are read here rather than held in a constant,
 * so a build's values are the ones the request is made with and the library is
 * only asked to load when a person is actually paying: a page that loaded it at
 * mount would put a third-party script in front of everybody who opens the
 * checkout.
 */
async function openOverlay(transactionId: string, onPaid: () => void): Promise<void> {
  const token: string = import.meta.env.VITE_PADDLE_CLIENT_TOKEN ?? ''
  if (token.trim() === '') {
    throw new Error('this build carries no VITE_PADDLE_CLIENT_TOKEN, so no overlay can be opened')
  }
  const paddle = await initializePaddle({
    environment: paddleEnvironment(import.meta.env.VITE_PORTAL_ENVIRONMENT),
    token,
    eventCallback: (event) => {
      if (event.name === CheckoutEventNames.CHECKOUT_COMPLETED) {
        onPaid()
      }
    },
  })
  if (paddle === undefined) {
    throw new Error('Paddle’s Checkout did not load')
  }
  paddle.Checkout.open({ transactionId })
}

export interface CheckoutScreenProps {
  /** Built by the route from the session; a test hands in a fake. */
  client: ApiClient
}

export function CheckoutScreen({ client }: CheckoutScreenProps) {
  const workspaceId = workspaceIdIn(useParams<{ workspaceId: string }>().workspaceId)
  const navigate = useNavigate()
  const [accepted, setAccepted] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const [paying, setPaying] = useState(false)

  // Paddle's window outlives this screen: a person can leave the checkout with
  // it still open, finish paying in it, and the completion must not pull them
  // out of wherever they are now. The flag is set on mount as well, because a
  // strict-mode remount runs the cleanup of the first pass.
  const live = useRef(true)
  useEffect(() => {
    live.current = true
    return () => {
      live.current = false
    }
  }, [])

  function buy(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault()
    // The acceptance is checked here and not only in the field below: a
    // disabled button does not refuse a dispatched event.
    if (workspaceId === undefined || !accepted || paying) {
      return
    }
    setFailure(null)
    setPaying(true)
    const asked: CheckoutCreate = { workspace_id: workspaceId, terms_version: TERMS_VERSION }
    void client
      .request<CheckoutSession>(CHECKOUT, { method: 'POST', body: asked })
      .then(async (session) => {
        if (
          session === undefined ||
          typeof session.transaction_id !== 'string' ||
          session.transaction_id === ''
        ) {
          if (live.current) {
            setFailure(NO_TRANSACTION)
          }
          return
        }
        try {
          await openOverlay(session.transaction_id, () => {
            if (live.current) {
              // Replaced rather than pushed: the checkout is not a step a
              // person goes Back to, and its transaction is paid.
              void navigate(AFTER_PAYMENT, { replace: true })
            }
          })
        } catch {
          if (live.current) {
            setFailure(OVERLAY_FAILURE)
          }
        }
      })
      .catch((error: unknown) => {
        if (live.current) {
          setFailure(sentence(error))
        }
      })
      .finally(() => {
        if (live.current) {
          setPaying(false)
        }
      })
  }

  return (
    <main>
      <h1>Buy a licence</h1>
      {workspaceId === undefined ? (
        <p role="alert">{NO_WORKSPACE}</p>
      ) : (
        <>
          <p>
            The licence is for the one workspace this page was opened for. Paddle’s payment window
            opens next and names the licence and its price; the card is entered there and never in
            this page.
          </p>
          <p>
            <a href={TERMS_URL} target="_blank" rel="noopener noreferrer">
              The Fugaro Subscription Terms
            </a>
            , version {TERMS_VERSION}, are the terms of this purchase.
          </p>
          <form onSubmit={buy}>
            <input
              id="checkout-terms"
              name="terms"
              type="checkbox"
              checked={accepted}
              onChange={(event) => {
                setAccepted(event.target.checked)
              }}
            />
            <label htmlFor="checkout-terms">
              I accept them on behalf of the business I am buying for, and I am authorised to bind
              it.
            </label>
            <button type="submit" disabled={!accepted || paying}>
              Continue to payment
            </button>
          </form>
        </>
      )}
      {failure === null ? null : <p role="alert">{failure}</p>}
    </main>
  )
}
