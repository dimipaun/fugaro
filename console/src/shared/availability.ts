// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Every number the console draws, and the state it carries (desktop-console
// plan Task 2.0; design section 6.1 rule 1; console/docs/json-contract.md
// section 1).
//
// The design's first UI rule is **every number carries an availability state,
// and a missing value is never an implicit zero**. This module is that rule as
// a type: there is no way to hand a screen a `number`, and no way for a screen
// to format one -- `formatReading` is the only function here that turns a
// reading into the text a person reads, and the screens must pass through it.
// `sanitize.test.ts` scans `src/renderer/` for `toFixed`, `toLocaleString` and
// `Intl.NumberFormat` so a screen cannot go around it.
//
// The five states are the contract's, and so are the spellings:
//
//   measured     the number               a counter the CLI read
//   notional     ~ + the number           a subscription's list price: never
//                                         billed, never capped
//   estimated    est. + the number        compute, an estimate by design
//   unavailable  n/a                      the document says it is not known
//   gap          an em dash in text       the stream stopped; on a chart, a
//                                         broken line or a hollow bar
//
// Why a type rather than a convention. The wire distinguishes "unknown" from
// "zero" with a pointer: `*float64` with no `omitempty` is `null` when the
// document does not know, and `JSONBurn.Known: false` makes `usd_per_min`
// unknown rather than measured-zero (contract section 1). One `JSON.parse`
// turns both into `0` -- the value `undefined` becomes when it is added, the
// value a chart draws when it is scaled -- and `0` on this screen means "this
// repository has spent nothing today", which is a statement about money. So
// `readNumber` is the only way a document's value enters the code, it returns a
// reading rather than a number, and a reading that is not measured carries the
// sentence explaining why instead of a value.
//
// Nothing here imports anything: this module is loaded by the renderer, which
// has no Node builtins, no network and no Electron (design section 5.1). It is
// `shared/` rather than `renderer/` because the main process wants the same
// readings when it answers an IPC call, and because the screens are not the
// only consumer -- `schema.ts` decodes through it.

/**
 * AvailabilityState is the contract's table, one member per row.
 */
export type AvailabilityState = 'measured' | 'notional' | 'estimated' | 'unavailable' | 'gap'

/**
 * AVAILABILITY_STATES lists the states in the design's order. It is the whole
 * list: a sixth state would need a sixth presentation in `formatReading` below,
 * and `availability.test.ts` holds the set to the design's table.
 */
export const AVAILABILITY_STATES: readonly AvailabilityState[] = Object.freeze([
  'measured',
  'notional',
  'estimated',
  'unavailable',
  'gap',
])

/**
 * NOTIONAL_MARK prefixes a subscription's list price on screen. The owner ruled
 * the mark is `~` (2026-10-28), and it is part of the value, so the meaning
 * survives without colour. A tilde also reads as prose's "approximately" -- the
 * state this most resembles -- so a notional figure is told apart from an
 * estimated one by ESTIMATED_MARK's `est.`, never by the tilde: `~$0.40` is a
 * subscription's list price, `est. $0.40` is compute. Drawing a notional dollar
 * as a billed one is a wrong number that looks right.
 */
export const NOTIONAL_MARK = '~'

/**
 * ESTIMATED_MARK prefixes compute, which is an estimate by design
 * (`runstore.Cost.ComputeEstimated`, `JSONLine`'s `compute_usd`).
 */
export const ESTIMATED_MARK = 'est.'

/**
 * UNAVAILABLE_MARK is what a value the document does not carry renders as. It
 * is a word rather than an empty cell on purpose: an empty cell is read as a
 * zero, which is the rule this module exists to enforce.
 */
export const UNAVAILABLE_MARK = 'n/a'

/**
 * GAP_MARK is the text form of a gap. On a chart a gap is a broken line or a
 * hollow bar (design section 6.1 rule 1); where a chart has no room for that,
 * this is what is drawn, and it is deliberately not `n/a`: "we stopped
 * observing" and "the document does not know" are different facts.
 */
export const GAP_MARK = '—'

/**
 * LOWER_BOUND_MARK prefixes a sum that is a lower bound: rows whose compute is
 * not estimated contribute an unknown, not a zero (`runview.Totals`'
 * `ComputeNotEstimated`, contract section 5).
 */
export const LOWER_BOUND_MARK = '≥'

/** Unit is how a measured number is drawn: dollars, a share, a duration. */
export type Unit = 'usd' | 'percent' | 'seconds' | 'minutes' | 'hours' | 'count'

/**
 * Reading is one number and its state.
 *
 * `value` is `number | null` rather than an optional `number` so that a reader
 * cannot confuse "not carried" with "not asked": every reading that is not
 * measured has `value === null`, and `noUncheckedIndexedAccess` plus `strict`
 * make reaching into it without checking the state a type error.
 *
 * `because` is the sentence a screen shows when there is nothing to show --
 * "the document has no total.counted_usd", "no document arrived for 160s". It
 * is required and non-empty for the two states that have no value, because an
 * `n/a` nobody can explain is a value the console cannot defend.
 */
export interface Reading {
  readonly state: AvailabilityState
  readonly value: number | null
  readonly because: string
  readonly lowerBound: boolean
}

/** Options every constructor takes: whether the value is a lower bound. */
export interface ReadingOptions {
  readonly lowerBound?: boolean
}

function requireFinite(what: string, value: unknown): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    // Not a refusal of a document -- `readNumber` is the door documents come
    // through and it never calls this with a bad value. This is the console's
    // own code getting it wrong, and a NaN drawn on a bar chart is a bar of no
    // width, which reads as zero.
    throw new Error(`${what} must be a finite number, got ${String(value)}`)
  }
  return value
}

function requireReason(what: string, because: string): string {
  if (because.trim() === '') {
    throw new Error(`${what} must say why the value is not there`)
  }
  return because
}

/** measured is a counter the CLI read. */
export function measured(value: number, options: ReadingOptions = {}): Reading {
  return {
    state: 'measured',
    value: requireFinite('a measured value', value),
    because: '',
    lowerBound: options.lowerBound === true,
  }
}

/** notional is a subscription's list price: never billed, never capped. */
export function notional(value: number, options: ReadingOptions = {}): Reading {
  return {
    state: 'notional',
    value: requireFinite('a notional value', value),
    because: '',
    lowerBound: options.lowerBound === true,
  }
}

/** estimated is a value the CLI computed rather than measured. */
export function estimated(value: number, options: ReadingOptions = {}): Reading {
  return {
    state: 'estimated',
    value: requireFinite('an estimated value', value),
    because: '',
    lowerBound: options.lowerBound === true,
  }
}

/**
 * unavailable is a value the document says it does not know: an absent key, a
 * `null` on the wire, a `bool` flag that says "I don't know". The reason is
 * mandatory -- it is shown to the person reading the screen.
 */
export function unavailable(because: string, options: ReadingOptions = {}): Reading {
  return {
    state: 'unavailable',
    value: null,
    because: requireReason('an unavailable reading', because),
    lowerBound: options.lowerBound === true,
  }
}

/**
 * gap is the stream having stopped. It is not `unavailable`: the console is
 * saying what it observed, not what the document said.
 */
export function gap(because: string, options: ReadingOptions = {}): Reading {
  return {
    state: 'gap',
    value: null,
    because: requireReason('a gap', because),
    lowerBound: options.lowerBound === true,
  }
}

/**
 * readNumber is the one door a document's value comes through.
 *
 * `field` is the wire path as the contract spells it -- `total.counted_usd` --
 * and it ends up in the sentence an `n/a` is explained by, so the field name in
 * a refusal is the same string the document is read with and the same string
 * `schema.ts` puts in a `SchemaRefusal`.
 *
 * Everything that is not a finite number is unavailable: an absent key, `null`
 * (the `*float64` convention), a string that looks like a number -- parsing it
 * would be trusting the document -- an array, a bool, and the two non-finite
 * numbers, because `NaN` and `Infinity` are what a JSON document cannot carry
 * and what a lenient parse produces when it should not.
 */
export function readNumber(raw: unknown, field: string): Reading {
  if (typeof raw === 'number' && Number.isFinite(raw)) return measured(raw)
  if (raw === null || raw === undefined) {
    return unavailable(`the document has no ${field}`)
  }
  if (typeof raw === 'number') {
    return unavailable(`${field} is not a finite number`)
  }
  return unavailable(`${field} is a ${typeof raw}, not a number`)
}

/**
 * plottable says whether a reading is a point on a series. A point that is not
 * plottable BREAKS the series: it is never skipped, never treated as zero, and
 * never interpolated across.
 */
export function plottable(reading: Reading): boolean {
  return reading.state === 'measured' || reading.state === 'notional' || reading.state === 'estimated'
}

/** Sample is one point of a series: when it arrived, and what it said. */
export interface Sample {
  readonly at: string
  readonly reading: Reading
}

/**
 * seriesSegments splits samples into the runs of consecutive points a line is
 * drawn through. A break is where a reading is not plottable, and the samples
 * on either side are in different segments -- which is the whole point: a line
 * chart that draws one segment through a gap asserts, in ink, a measurement
 * nobody made. The design's rule is a broken line (section 6.1 rule 1), and a
 * broken line is exactly this function's output.
 *
 * The samples themselves are returned, not copies: a segment is the points that
 * arrived, so a point invented to fill a gap cannot survive this function, and
 * `availability.test.ts` holds it by identity.
 */
export function seriesSegments(samples: readonly Sample[]): readonly (readonly Sample[])[] {
  const segments: Sample[][] = []
  let current: Sample[] = []
  for (const sample of samples) {
    if (plottable(sample.reading)) {
      current.push(sample)
      continue
    }
    if (current.length > 0) segments.push(current)
    current = []
  }
  if (current.length > 0) segments.push(current)
  return segments
}

/**
 * fixed formats a number with grouping and a fixed number of decimals, without
 * `toLocaleString`: the grouping separator must not depend on the machine's
 * locale, and a screen that renders `1.234,50` because the operator's laptop is
 * German is a screenshot golden that fails on someone else's machine.
 */
function fixed(value: number, digits: number): string {
  const magnitude = Math.abs(value)
  const rounded = magnitude.toFixed(digits)
  const [whole = '0', fraction = ''] = rounded.split('.')
  const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  // The sign is decided AFTER rounding, because the rounded text is the number
  // a person reads. `-0.001` rounds to `0.00`, and `-$0.00` -- or `-0.0%` --
  // says a repository spent below nothing when what it spent is nothing. A
  // negative that survives rounding keeps its sign. (`-0` itself compares equal
  // to `0`, so the test is `value < 0` and not `Object.is`.)
  const negative = value < 0 && Number(rounded) !== 0
  return `${negative ? '-' : ''}${grouped}${fraction === '' ? '' : `.${fraction}`}`
}

/** formatValue draws one number in its unit. */
function formatValue(value: number, unit: Unit): string {
  switch (unit) {
    case 'usd': {
      // The sign goes before the currency, not before the digits: `$-12.50` is
      // what a naive interpolation draws, and it reads as a price.
      const text = fixed(value, 2)
      return text.startsWith('-') ? `-$${text.slice(1)}` : `$${text}`
    }
    case 'percent':
      return `${fixed(value, 1)}%`
    case 'seconds':
      return `${fixed(value, 0)} s`
    case 'minutes':
      return `${fixed(value, 1)} min`
    case 'hours':
      return `${fixed(value, 2)} h`
    case 'count':
      return fixed(value, 0)
  }
}

/**
 * formatReading is the one function that turns a reading into the text a screen
 * shows. Every state has a presentation and no state has two:
 *
 *   measured     $12.50
 *   notional     ~$3.25
 *   estimated    est. $0.40
 *   unavailable  n/a
 *   gap          an em dash
 *
 * A lower bound on a reading that has no value is dropped rather than drawn:
 * "at least unknown" is unknown, and the marker would suggest a number exists.
 */
export function formatReading(reading: Reading, unit: Unit = 'count'): string {
  if (reading.value === null) {
    return reading.state === 'gap' ? GAP_MARK : UNAVAILABLE_MARK
  }
  const body = formatValue(reading.value, unit)
  let marked = body
  if (reading.state === 'notional') marked = `${NOTIONAL_MARK}${body}`
  if (reading.state === 'estimated') marked = `${ESTIMATED_MARK} ${body}`
  return reading.lowerBound ? `${LOWER_BOUND_MARK} ${marked}` : marked
}
