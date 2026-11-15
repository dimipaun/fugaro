// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The reseller screen (portal plan P6.4; spec §Resellers, D2189, D2190).
//
// **One load, and it is the screen's own.** The reads are taken together when
// the screen mounts: a reseller reads what it sold and what it owes at the same
// moment, and a screen that drew one section while another was still failing
// would be showing a figure from a state nobody asked about. A customer's keys
// are read after the customers, because they are asked for by the customer the
// list just named, and every one of them is part of the same answer -- so a
// failure of any read is the screen's answer, and half a page is never drawn.
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
import type { KeyMetadata } from '../../api/types/keys'
import type { ResellerCustomer } from '../../api/types/resellers'
import type { Statement } from '../../api/types/statements'
import type { ResellerApi } from './api'
import { CustomersSection } from './customers'
import { StatementsSection } from './statements'
import './reseller.css'

const GENERIC_FAILURE = 'The portal could not read your reseller account. Try again.'

/** What the screen drew: the customers, their keys by organisation, the statements. */
interface Overview {
  customers: ResellerCustomer[]
  keys: Map<string, KeyMetadata[]>
  statements: Statement[]
}

type ScreenState = { status: 'loading' } | ({ status: 'ready' } & Overview) | { status: 'failed'; message: string }

export interface ResellerScreenProps {
  /** The port the screen reads through: the customers, their keys, the months owed. */
  api: ResellerApi
}

/** Everything the screen draws: the customers sold to, their keys, the months owed. */
async function read(api: ResellerApi): Promise<Overview> {
  const [customers, statements] = await Promise.all([api.customers(), api.statements()])
  const held = await Promise.all(
    customers.map(async (customer): Promise<[string, KeyMetadata[]]> => [
      customer.organisation_id,
      await api.keys(customer.organisation_id),
    ]),
  )
  return { customers, keys: new Map(held), statements }
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
      <CustomersSection customers={state.customers} keys={state.keys} />
      <StatementsSection statements={state.statements} customers={state.customers} />
    </main>
  )
}
