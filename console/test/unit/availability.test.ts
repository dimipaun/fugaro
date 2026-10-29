// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The five availability states, held to the three things the design's first UI
// rule (docs/design/desktop-console.md section 6.1 rule 1, and
// console/docs/json-contract.md section 1) can get wrong (desktop-console plan
// Task 2.0).
//
// The rule is one sentence long -- **every number carries an availability
// state, and a missing value is never an implicit zero** -- and it fails in
// three places, so there are three groups of tests here:
//
//   1. a value the document does not carry renders `n/a`, and never `0`. The
//      contract's `*float64` convention is the sharp edge: `null` on the wire
//      means *unavailable* and `0` means *measured zero*, and the two are one
//      `JSON.parse` apart. `JSONBurn.Known: false` is the same edge with a bool.
//   2. a subscription's spend renders with its marker, and compute renders
//      marked as the estimate it is by design. A notional dollar drawn as a
//      billed one is a wrong number that looks right, which is worse than a
//      missing one.
//   3. a gap and an unavailable point BREAK a series rather than being plotted
//      or interpolated across. This is the rule that has no natural failure
//      mode in a type: a line chart that skips a missing point draws a
//      straight line through it and asserts, in ink, a measurement nobody
//      made.
//
// The last test here is the rule that keeps the other three from being
// bypassed: no file under `console/src/renderer/` may format a number itself.
// A screen that calls `value.toFixed(2)` has drawn a bare number, and the
// state, the marker and the unit are all gone. The scan is in
// `sanitize.test.ts` next to the `innerHTML` scan, because both are one
// question -- what may a renderer file contain -- and two walks of the same
// directory would drift.

import { describe, expect, it } from 'vitest'

import {
  AVAILABILITY_STATES,
  ESTIMATED_MARK,
  GAP_MARK,
  LOWER_BOUND_MARK,
  NOTIONAL_MARK,
  UNAVAILABLE_MARK,
  estimated,
  formatReading,
  gap,
  measured,
  notional,
  plottable,
  readNumber,
  seriesSegments,
  unavailable,
} from '../../src/shared/availability'
import type { AvailabilityState, Reading, Sample } from '../../src/shared/availability'

describe('the five states', () => {
  it('names exactly the design’s five states', () => {
    // Named, not counted: a sixth state would need a sixth presentation and
    // this list is where the design's table is written down in code.
    expect([...AVAILABILITY_STATES].toSorted()).toEqual([
      'estimated',
      'gap',
      'measured',
      'notional',
      'unavailable',
    ])
  })

  it('gives every constructor its own state', () => {
    const readings: readonly [Reading, AvailabilityState][] = [
      [measured(12.5), 'measured'],
      [notional(3.25), 'notional'],
      [estimated(0.4), 'estimated'],
      [unavailable('the document does not carry it'), 'unavailable'],
      [gap('no document arrived for 160s'), 'gap'],
    ]
    expect(readings.map(([reading]) => reading.state)).toEqual([
      'measured',
      'notional',
      'estimated',
      'unavailable',
      'gap',
    ])
  })

  it('refuses to invent a reason for a value it does not have', () => {
    // An `n/a` with no reason is a value the console cannot explain to the
    // person reading it, and the screens show that reason. Empty is the
    // mistake this catches, because `''` is what a missing argument gives.
    expect(() => unavailable('')).toThrow(/why/i)
    expect(() => unavailable('   ')).toThrow(/why/i)
    expect(() => gap('')).toThrow(/why/i)
  })

  it('refuses a measured value that is not a finite number', () => {
    // The last door into a bare number. `readNumber` is the door documents
    // come through and it refuses these, so the constructors refuse them too:
    // a NaN drawn on a bar chart is a bar of no width, which reads as zero.
    expect(() => measured(NaN)).toThrow(/finite/i)
    expect(() => measured(Infinity)).toThrow(/finite/i)
    expect(() => notional(NaN)).toThrow(/finite/i)
    expect(() => estimated(Infinity)).toThrow(/finite/i)
  })
})

describe('a missing value', () => {
  it('renders n/a, and never 0', () => {
    // The red step: a formatter that returns the number for a missing field
    // returns `$0.00` here and this assertion fails.
    const absent = readNumber(undefined, 'total.counted_usd')
    expect(formatReading(absent, 'usd')).toBe(UNAVAILABLE_MARK)
    expect(formatReading(absent, 'usd')).not.toContain('0')
  })

  it('reads the contract’s three spellings of "not known" as unavailable', () => {
    // json-contract section 1: an absent key, a `null` (the `*float64`
    // convention), and a value of the wrong type -- a string that looks like a
    // number is not one, and parsing it would be trusting the document.
    for (const raw of [undefined, null, '12.5', '', {}, [], true, NaN, Infinity, -Infinity]) {
      const reading = readNumber(raw, 'total.counted_usd')
      expect(reading.state, `readNumber(${JSON.stringify(raw)})`).toBe('unavailable')
      expect(reading.value, `readNumber(${JSON.stringify(raw)})`).toBeNull()
    }
  })

  it('says which field it is missing, in the words a screen would show', () => {
    expect(readNumber(undefined, 'total.counted_usd').because).toContain('total.counted_usd')
    expect(readNumber(null, 'total.percent').because).toContain('total.percent')
    expect(readNumber('12.5', 'total.spent_usd').because).toContain('string')
  })

  it('keeps a measured zero a measured zero', () => {
    // The other half of the rule, and the reason `n/a` cannot simply be "the
    // number is falsy": a repository that has spent nothing has spent nothing,
    // and that is a measurement.
    const zero = readNumber(0, 'total.spent_usd')
    expect(zero.state).toBe('measured')
    expect(formatReading(zero, 'usd')).toBe('$0.00')
    expect(zero.because).toBe('')
  })
})

describe('a value that is not a plain measurement', () => {
  it('marks a subscription’s spend notional', () => {
    const reading = notional(3.25)
    expect(reading.state).toBe('notional')
    const text = formatReading(reading, 'usd')
    expect(text).toContain(NOTIONAL_MARK)
    expect(text).toContain('$3.25')
  })

  it('never spells the notional state out in the text it writes', () => {
    // The `~` is the whole mark: a screen that also wrote the state's name
    // would put a word the reader has to decode beside a figure that carries
    // its own meaning.
    for (const value of [0, 3.25, -0.001, 1250]) {
      expect(formatReading(notional(value), 'usd')).not.toContain('NOTIONAL')
    }
  })

  it('marks compute as the estimate it is', () => {
    // `JSONLine.ComputeEstimated` / `runstore.Cost.ComputeEstimated`: when
    // compute is not estimated, compute_usd is 0 and means "not estimated",
    // never "free" -- so the state, not the number, is what tells them apart.
    expect(formatReading(estimated(0), 'usd')).toContain(ESTIMATED_MARK)
    expect(formatReading(estimated(0), 'usd')).toContain('$0.00')
    expect(formatReading(readNumber(undefined, 'cost.compute_usd'), 'usd')).toBe(UNAVAILABLE_MARK)
  })

  it('marks a lower bound, and never marks an unknown one', () => {
    // `runview.Totals.ComputeNotEstimated`: those rows' compute is unknown, so
    // the sums are a lower bound. A bound on a value nobody measured is still
    // unknown, so the marker is dropped rather than the value invented.
    const bound = formatReading(estimated(50, { lowerBound: true }), 'usd')
    expect(bound.startsWith(LOWER_BOUND_MARK)).toBe(true)
    expect(formatReading(unavailable('not estimated', { lowerBound: true }), 'usd')).toBe(
      UNAVAILABLE_MARK,
    )
    expect(formatReading(gap('the stream stopped', { lowerBound: true }), 'usd')).toBe(GAP_MARK)
  })

  it('draws a gap as a gap, with no number in it', () => {
    const reading = gap('no document arrived for 160s')
    const text = formatReading(reading, 'usd')
    expect(text).toBe(GAP_MARK)
    expect(text).not.toMatch(/\d/)
    expect(reading.because).toContain('160s')
  })
})

describe('formatting', () => {
  it('renders each unit the way the screens draw it', () => {
    expect(formatReading(measured(1234.5), 'usd')).toBe('$1,234.50')
    expect(formatReading(measured(-12.5), 'usd')).toBe('-$12.50')
    expect(formatReading(measured(25.08), 'percent')).toBe('25.1%')
    expect(formatReading(measured(1500), 'seconds')).toBe('1,500 s')
    expect(formatReading(measured(0.42), 'hours')).toBe('0.42 h')
    expect(formatReading(measured(12), 'count')).toBe('12')
    expect(formatReading(measured(1234567), 'count')).toBe('1,234,567')
  })

  it('gives -0 and 0 the same rendering', () => {
    expect(formatReading(measured(-0), 'usd')).toBe('$0.00')
  })

  it('decides the sign after rounding, so a small negative is not `-$0.00`', () => {
    // A refund, a rounding artefact or a float subtraction that landed at
    // -0.001 is a zero at the precision the screen draws. `-$0.00` and `-0.0%`
    // are wrong numbers: they say a repository spent below nothing when what it
    // spent is nothing. So the sign comes from the ROUNDED text -- the number a
    // person reads -- and a value that rounds away to zero at the unit's own
    // precision is drawn as zero (`-0.4 s` is `0 s`, because the unit draws
    // whole seconds). A negative that survives rounding keeps its sign.
    expect(formatReading(measured(-0.001), 'usd')).toBe('$0.00')
    expect(formatReading(measured(-0.004), 'usd')).toBe('$0.00')
    expect(formatReading(measured(-0.005), 'usd')).toBe('-$0.01')
    expect(formatReading(measured(-0.001), 'hours')).toBe('0.00 h')
    expect(formatReading(measured(-0.004), 'percent')).toBe('0.0%')
    expect(formatReading(measured(-0.04), 'percent')).toBe('0.0%')
    expect(formatReading(measured(-0.06), 'percent')).toBe('-0.1%')
    expect(formatReading(measured(-0.001), 'minutes')).toBe('0.0 min')
    expect(formatReading(measured(-0.4), 'seconds')).toBe('0 s')
    expect(formatReading(measured(-0.6), 'seconds')).toBe('-1 s')
    expect(formatReading(measured(-0.49), 'count')).toBe('0')
    expect(formatReading(measured(-1.5), 'usd')).toBe('-$1.50')
    // And a state's marker is not a sign: the marks go before the number, not
    // before the minus.
    expect(formatReading(notional(-0.001), 'usd')).toBe('~$0.00')
    expect(formatReading(estimated(-0.001), 'usd')).toBe('est. $0.00')
  })
})

describe('a series', () => {
  const sample = (at: string, reading: Reading): Sample => ({ at, reading })

  it('breaks at a gap rather than interpolating across it', () => {
    const before = sample('2026-10-15T12:00:00Z', measured(12.5))
    const beforeToo = sample('2026-10-15T12:00:05Z', measured(12.54))
    const stopped = sample('2026-10-15T12:04:20Z', gap('no document arrived for 160s'))
    const after = sample('2026-10-15T12:04:21Z', measured(50))
    const segments = seriesSegments([before, beforeToo, stopped, after])
    expect(segments).toHaveLength(2)
    // Identity, not equality: a segment is the samples that arrived, and a
    // point interpolated into the break would be a new object here.
    expect(segments[0]).toEqual([before, beforeToo])
    expect(segments[1]).toEqual([after])
    expect(segments[0]?.[0]).toBe(before)
    expect(segments.flat()).not.toContain(stopped)
  })

  it('breaks at an unavailable point too, because a missing point is not a zero', () => {
    const before = sample('2026-10-15T12:00:00Z', measured(12.5))
    const unknown = sample('2026-10-15T12:00:05Z', unavailable('the document has no counted_usd'))
    const after = sample('2026-10-15T12:00:10Z', measured(12.54))
    expect(seriesSegments([before, unknown, after]).map((s) => s.length)).toEqual([1, 1])
  })

  it('plots a notional point and an estimated point', () => {
    // They are drawn differently from a measured point, but they are points:
    // breaking the line at every notional sample would break every series on a
    // subscription installation.
    expect(plottable(measured(1))).toBe(true)
    expect(plottable(notional(1))).toBe(true)
    expect(plottable(estimated(1))).toBe(true)
    expect(plottable(unavailable('no counted_usd'))).toBe(false)
    expect(plottable(gap('the stream stopped'))).toBe(false)
    expect(seriesSegments([])).toEqual([])
  })
})
