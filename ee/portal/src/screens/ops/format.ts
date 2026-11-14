// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The units the API's shapes are written in, and how the operations screens
// show them (portal plan P5.3).
//
// **Nothing here formats for a locale.** The portal is read by one team and the
// figures it shows are compared between two records and against a statement, so
// an amount or an instant that a reader's browser reworded would be a different
// string for the same row on two machines. Micros are the shape's own unit
// (ledger.entry, reports.month) and the currency is the shape's own field; a
// month is UTC because a report's months are (P5.2). What is left for a design
// pass is the typography, not the value.

/**
 * Micros of the currency's own unit, as the amount and the currency it is in.
 * A month of reports carries totals and no currency (reports.month), so the
 * currency is left out rather than guessed at: an amount with the wrong
 * currency against it is a wrong figure, and a blank space after one is a
 * worse-looking one.
 */
export function money(micros: number, currency?: string): string {
  const amount = (micros / 1_000_000).toFixed(2)
  return currency === undefined || currency === '' ? amount : `${amount} ${currency}`
}

/** This month in UTC, as a report's own month keys are (`YYYY-MM`). */
export function thisMonth(now: Date): string {
  return now.toISOString().slice(0, 7)
}

/**
 * The instant a term ends when an operator picks a day: midnight UTC of that
 * day, or null when they picked none. A date field answers a day and the shapes
 * carry an instant (F30: an end is capped by the row, and a requested end is a
 * UTC instant), so the day the operator chose is the whole of it, read as UTC
 * rather than as the browser's zone.
 */
export function dayInstant(day: string): string | null {
  if (day === '') {
    return null
  }
  const at = new Date(`${day}T00:00:00Z`)
  return Number.isNaN(at.getTime()) ? null : at.toISOString()
}
