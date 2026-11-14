// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Monthly reports (portal plan P5.3; spec §Operations "Reports", D2190).
//
// **The channel split is the figure, not a detail.** Direct sales and reseller
// sales are counted apart because the two are settled differently (D2190), and
// the customers behind each are drawn so a figure can be walked back to who
// bought: a bare total is a number nobody can check.
//
// **A month is UTC here too.** The API keys a report on UTC (P5.2), and the
// screen sends the month it opens on in that spelling, for the reason the
// ledger's comment gives.
//
// **A refused read is an answer, and the section stops at it.** The load has
// finished, so nothing is still loading under the API's own sentence, and the
// month form above is how the report is asked for again -- pressing Show
// re-runs the load, which is what the attempt counter is for. A section left
// saying "Loading…" beside the refusal would be telling an operator to wait for
// a month that is never coming.

import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import type { ReportMonth } from '../../api/types/reports'
import type { OpsApi } from './api'
import { money, thisMonth } from './format'
import { sentence } from './state'

export function ReportsSection({ api }: { api: OpsApi }) {
  const [month, setMonth] = useState(() => thisMonth(new Date()))
  const [asked, setAsked] = useState(month)
  const [attempt, setAttempt] = useState(0)
  const [report, setReport] = useState<ReportMonth | null>(null)
  const [failure, setFailure] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    setReport(null)
    // A load that starts clears the last refusal, so a month that answers this
    // time is not drawn under the sentence the month before it was refused
    // with.
    setFailure(null)
    api.report(asked).then(
      (answered) => {
        if (live) {
          setReport(answered)
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
  }, [api, asked, attempt])

  function show(event: FormEvent): void {
    event.preventDefault()
    // Pressing Show on the month already asked for changes nothing the effect
    // depends on, and a refused load would then stay on screen forever: the
    // attempt is what makes the same month a new load.
    setAsked(month)
    setAttempt((tried) => tried + 1)
  }

  return (
    <section aria-labelledby="ops-reports">
      <h2 id="ops-reports">Reports</h2>
      <form onSubmit={show}>
        <label htmlFor="ops-report-month">Report month</label>
        <input
          id="ops-report-month"
          type="month"
          value={month}
          onChange={(event) => {
            setMonth(event.target.value)
          }}
          required
        />
        <button type="submit">Show the report</button>
      </form>

      {failure === null ? null : <p role="alert">{failure}</p>}
      {failure === null && report === null ? <p role="status">Loading…</p> : null}
      {report === null ? null : (
        <>
          <p>
            {report.month}: {money(report.direct_micros)} direct,{' '}
            {money(report.reseller_micros)} through resellers.
          </p>
          {report.customers.length === 0 ? (
            <p>No customer bought in that month.</p>
          ) : (
            <ul>
              {report.customers.map((customer) => (
                <li key={customer.organisation_id}>
                  {customer.name} · {customer.channel} · {money(customer.amount_micros)} ·{' '}
                  {customer.organisation_id}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  )
}
