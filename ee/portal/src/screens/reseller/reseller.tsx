// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The reseller screen (portal plan P6.4; spec §Resellers, D2189, D2190).
//
// **One read, and it is the screen's own.** The two sections below are one
// request each, made once when the screen mounts: a reseller reads what it sold
// and what it owes at the same moment, and a screen that drew one section while
// the other was still failing would be showing a figure from a state nobody
// asked about. So the two reads are taken together, and a failure of either is
// the screen's answer.
//
// **The screen is a function of the port it is handed.** Where the port comes
// from when a person is looking at the screen -- `resellerApi` over the API
// client with the session's own token function -- is `route.tsx`, which is the
// file the router mounts. Keeping the session out of this file is what lets the
// screen be drawn by a suite with no provider, no Firebase configuration and no
// network: the whole of what these two sections read is what a test hands them.
//
// **A refusal is shown as the API wrote it, and nothing else is.** The API's one
// error shape is a sentence for a person (P1.0), so a screen that rewrote it
// would be second-guessing the service that knows whether the caller is a
// reseller at all. Anything else -- a chunk that failed to load, a bug in this
// screen -- gets one sentence of the screen's own rather than a stack trace or
// an SDK's console text.

import { useEffect, useState } from 'react'
import { ApiError } from '../../api/client'
import type { ResellerCustomer } from '../../api/types/resellers'
import type { Statement } from '../../api/types/statements'
import type { ResellerApi } from './api'
import { CustomersSection } from './customers'
import { StatementsSection } from './statements'
import './reseller.css'

const GENERIC_FAILURE = 'The portal could not read your reseller account. Try again.'

type ScreenState =
  | { status: 'loading' }
  | { status: 'ready'; customers: ResellerCustomer[]; statements: Statement[] }
  | { status: 'failed'; message: string }

export interface ResellerScreenProps {
  /** What the screen reads: the customers sold to, and the months owed. */
  api: ResellerApi
}

/** The two reads the screen draws: the customers sold to, and the months owed. */
async function read(api: ResellerApi): Promise<{ customers: ResellerCustomer[]; statements: Statement[] }> {
  const [customers, statements] = await Promise.all([api.customers(), api.statements()])
  return { customers, statements }
}

/** The sentence a person is shown for a failure. */
function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

export function ResellerScreen({ api }: ResellerScreenProps) {
  const [state, setState] = useState<ScreenState>({ status: 'loading' })

  useEffect(() => {
    // A read that lands after the screen changed hands -- a new port, or an
    // unmount -- is dropped rather than drawn: the person would otherwise be
    // shown a figure from a session that is no longer theirs.
    let live = true
    void read(api).then(
      (data) => {
        if (live) {
          setState({ status: 'ready', ...data })
        }
      },
      (error: unknown) => {
        if (live) {
          setState({ status: 'failed', message: sentence(error) })
        }
      },
    )
    return () => {
      live = false
    }
  }, [api])

  if (state.status === 'loading') {
    return (
      <main className="reseller">
        <p role="status">Loading…</p>
      </main>
    )
  }

  if (state.status === 'failed') {
    return (
      <main className="reseller">
        <h1>Your reseller account</h1>
        <p role="alert">{state.message}</p>
      </main>
    )
  }

  return (
    <main className="reseller">
      <h1>Your reseller account</h1>
      <CustomersSection customers={state.customers} />
      <StatementsSection statements={state.statements} customers={state.customers} />
    </main>
  )
}
