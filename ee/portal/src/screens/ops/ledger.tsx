// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The sales ledger (portal plan P5.3; spec §Operations "Sales", F16).
//
// **An entry is the portal's reading of an event, and never the event.** The
// stored deliveries are what a figure is walked back to (P4.2), and they are
// read in the queue above; this is the month's settled lines, which is what a
// report sums and what an operator checks a statement against.
//
// **A month is UTC.** The API keys a report's months on UTC (P5.2), so the
// screen opens on this month in UTC and sends the same spelling back: a browser
// in another zone that named its own month would ask for a month the API does
// not have.

import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import type { LedgerEntry } from '../../api/types/ledger'
import type { OpsApi } from './api'
import { money, thisMonth } from './format'
import { sentence } from './state'

export function LedgerSection({ api }: { api: OpsApi }) {
  const [month, setMonth] = useState(() => thisMonth(new Date()))
  const [asked, setAsked] = useState(month)
  const [entries, setEntries] = useState<LedgerEntry[] | null>(null)
  const [failure, setFailure] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    setEntries(null)
    api.ledger(asked).then(
      (lines) => {
        if (live) {
          setEntries(lines)
        }
      },
      (error: unknown) => {
        if (live) {
          setFailure(sentence(error))
        }
      },
    )
    return () => {
      live = false
    }
  }, [api, asked])

  function show(event: FormEvent): void {
    event.preventDefault()
    setFailure(null)
    setAsked(month)
  }

  return (
    <section aria-labelledby="ops-ledger">
      <h2 id="ops-ledger">The ledger</h2>
      <form onSubmit={show}>
        <label htmlFor="ops-ledger-month">Ledger month</label>
        <input
          id="ops-ledger-month"
          type="month"
          value={month}
          onChange={(event) => {
            setMonth(event.target.value)
          }}
          required
        />
        <button type="submit">Show the ledger</button>
      </form>

      {failure === null ? null : <p role="alert">{failure}</p>}
      {entries === null ? (
        <p role="status">Loading…</p>
      ) : entries.length === 0 ? (
        <p>Nothing settled in that month.</p>
      ) : (
        <ul>
          {entries.map((entry) => (
            <li key={entry.id}>
              {entry.at} · {entry.kind} · {entry.channel} · {entry.organisation_id} ·{' '}
              {money(entry.amount_micros, entry.currency)} · {entry.seats} seats
              {entry.entitlement_id === null ? '' : ` · ${entry.entitlement_id}`}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
