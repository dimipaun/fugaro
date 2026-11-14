// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// How the reseller screens write a figure and a moment (portal plan P6.4).
//
// **The scale of a `*_micros` is the contract's, not this screen's.**
// `budget.Micros` counts millionths of a dollar and the console's spend screen
// reads the same integer the same way (`MICROS_PER_USD` there), so the figure a
// reseller is shown is the integer it will be invoiced for divided by one
// number both sides know -- not a string the API formatted, and not a second
// opinion about what a micro is.
//
// **The day is UTC, and it is the date part of the instant.** A statement's
// month is UTC by D2189 (`YYYY-MM`), so a link or an issue drawn in the
// reader's own zone would put two rows of one month on a different day from the
// month they belong to. A value the API sent that is not a moment at all is
// shown as it arrived: a screen has no better answer, and drawing an empty cell
// would hide that the wire carried something the portal does not understand.

export const MICROS_PER_USD = 1_000_000

/** One `*_micros` figure as dollars and cents. */
export function usd(micros: number): string {
  return `$${(micros / MICROS_PER_USD).toFixed(2)}`
}

/** One instant as `YYYY-MM-DD` in UTC. */
export function day(value: string): string {
  const at = new Date(value)
  return Number.isNaN(at.getTime()) ? value : at.toISOString().slice(0, 10)
}
