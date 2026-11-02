// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The jobs-free totals read's decoder (console 0.119.0 "Pomelo", contract
// §6.17; `decodeRunsSummary`, `src/shared/schema.ts`).
//
// **Why this file is its own file.** The document is the one read the console
// makes that touches no run object and no bucket listing: its figures come from
// the budget database's day counters and the spend history's daily documents,
// so every value here is money or a count and the decoder's job is to keep the
// two apart. The properties are:
//
//   - a fixture document decodes, field for field;
//   - `compute_micros: null` stays `null` and is never read as `0` -- "not
//     estimated" is not "free", which is the one wrong figure this read exists
//     to avoid;
//   - `spent_micros` and `notional_micros` are two facts and are never summed,
//     and the decoded value carries no third field that could be;
//   - `days_unknown` and `notes` are kept: a partly-unknown window is drawn as
//     partly unknown, and the CLI's own lines reach the screen;
//   - a wrong type refuses, by field, before anything is drawn;
//   - a hostile repository name decodes as a **string** and is never a link, a
//     path or a command;
//   - an unknown key is ignored (the owner's ruling of 2026-10-30), so a newer
//     CLI's added field is neither a refusal nor a value a screen can draw.
//
// Every document here is written by hand and hermetic: no file, no network and
// no real name (`acme/widgets` is a placeholder).

import { describe, expect, it } from 'vitest'

import {
  NON_TEXT_STRING_KEYS,
  RUNS_SUMMARY_VERSION,
  WIRE_TEXT_CLASSES,
  decodeRunsSummary,
} from '../../src/shared/schema.js'
import type { RunsSummaryView } from '../../src/shared/schema.js'

/**
 * SUMMARY is one document as `runs summary --json` prints one: two
 * repositories, a window of days, and one day neither source answered for. The
 * second repository is the interesting one -- it has runs, no run's compute is
 * priced, and so its `compute_micros` is `null` rather than `0`.
 */
function summaryDocument(): Record<string, unknown> {
  return {
    v: RUNS_SUMMARY_VERSION,
    project: '<project>',
    since: '2026-10-27',
    until: '2026-11-02',
    repositories: [
      {
        slug: 'a1b2c3d4',
        repo: 'acme/widgets',
        runs: 4,
        states: { succeeded: 3, failed: 1, halted: 0, cancelled: 0, infra_error: 0, other: 0 },
        spent_micros: 1_500_000,
        notional_micros: 250_000,
        compute_micros: 400_000,
        compute_runs: 4,
      },
      {
        slug: 'e5f6a7b8',
        repo: 'acme/gadgets',
        runs: 2,
        states: { succeeded: 0, failed: 1, halted: 0, cancelled: 0, infra_error: 0, other: 1 },
        spent_micros: 0,
        notional_micros: 900_000,
        compute_micros: null,
        compute_runs: 0,
      },
    ],
    totals: {
      runs: 6,
      states: { succeeded: 3, failed: 2, halted: 0, cancelled: 0, infra_error: 0, other: 1 },
      spent_micros: 1_500_000,
      notional_micros: 1_150_000,
      compute_micros: 400_000,
      compute_runs: 4,
    },
    days_unknown: ['2026-10-29'],
    notes: [
      'counted from the budget database’s day counters and the spend history’s daily documents',
      'a run is counted on the UTC day its spend was recorded',
      '2026-10-29 is unknown: the spend history could not answer',
    ],
  }
}

/** decoded is a document that must decode, or the test fails with the
 * decoder's own sentence rather than on an undefined field. */
function decoded(raw: unknown): RunsSummaryView {
  const result = decodeRunsSummary(raw, 'the runs summary document')
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
}

/** refusalOf is the refusal of a document that must be refused. */
function refusalOf(raw: unknown): { field: string; name: string; because: string } {
  const result = decodeRunsSummary(raw, 'the runs summary document')
  if (result.ok) throw new Error('expected a refusal')
  return { ...result.skew.refusal, because: result.skew.because }
}

/** without returns the fixture with one field path removed, so "this key is
 * required" is asserted by a document that is the fixture and not a stub. */
function without(path: readonly string[]): Record<string, unknown> {
  const copy = structuredClone(summaryDocument())
  let at: Record<string, unknown> = copy
  for (const step of path.slice(0, -1)) {
    at = at[step] as Record<string, unknown>
  }
  const last = path[path.length - 1]
  if (last === undefined) throw new Error('path is empty')
  delete at[last]
  return copy
}

/** replacing returns the fixture with one field path set to another value. */
function replacing(path: readonly string[], value: unknown): Record<string, unknown> {
  const copy = structuredClone(summaryDocument())
  let at: Record<string, unknown> = copy
  for (const step of path.slice(0, -1)) {
    at = at[step] as Record<string, unknown>
  }
  const last = path[path.length - 1]
  if (last === undefined) throw new Error('path is empty')
  at[last] = value
  return copy
}

describe('the runs summary document (§6.17)', () => {
  it('decodes the fixture field for field', () => {
    const view = decoded(summaryDocument())
    expect(view.v).toBe(1)
    expect(view.project).toBe('<project>')
    expect(view.since).toBe('2026-10-27')
    expect(view.until).toBe('2026-11-02')
    expect(view.repositories.map((row) => row.repo)).toEqual(['acme/widgets', 'acme/gadgets'])
    expect(view.repositories.map((row) => row.slug)).toEqual(['a1b2c3d4', 'e5f6a7b8'])
    expect(view.repositories[0]?.states).toEqual({
      succeeded: 3,
      failed: 1,
      halted: 0,
      cancelled: 0,
      infraError: 0,
      other: 0,
    })
    expect(view.totals.runs).toBe(6)
    expect(view.totals.states.other).toBe(1)
    expect(view.daysUnknown).toEqual(['2026-10-29'])
    expect(view.notes).toHaveLength(3)
    expect(view.notes[0]).toContain('budget database')
  })

  it('refuses a version it does not read, and says which', () => {
    // The contract promises a field is only ever added within a version (D784),
    // so a `v` this build does not know is a document whose figures may have
    // been re-meant -- refused whole rather than drawn.
    const refusal = refusalOf(replacing(['v'], RUNS_SUMMARY_VERSION + 1))
    expect(refusal.field).toBe('v')
    expect(refusal.name).toBe('unknown-value')
  })

  it('refuses a document that is not an object at all', () => {
    for (const raw of [null, 'a summary', 7, []]) {
      expect(decodeRunsSummary(raw, 'the runs summary document').ok, JSON.stringify(raw)).toBe(false)
    }
  })

  it('requires every field of the shape, by name', () => {
    const required: readonly (readonly string[])[] = [
      ['v'],
      ['project'],
      ['since'],
      ['until'],
      ['repositories'],
      ['totals'],
      ['days_unknown'],
      ['notes'],
      ['totals', 'runs'],
      ['totals', 'states'],
      ['totals', 'states', 'succeeded'],
      ['totals', 'states', 'failed'],
      ['totals', 'states', 'halted'],
      ['totals', 'states', 'cancelled'],
      ['totals', 'states', 'infra_error'],
      ['totals', 'states', 'other'],
      ['totals', 'spent_micros'],
      ['totals', 'notional_micros'],
      ['totals', 'compute_micros'],
      ['totals', 'compute_runs'],
    ]
    for (const path of required) {
      const refusal = refusalOf(without(path))
      expect(refusal.because, path.join('.')).toContain(path[path.length - 1] ?? '')
    }
  })
})

describe('the money the document carries', () => {
  it('keeps `compute_micros: null` null, and never reads it as zero', () => {
    // The one figure this read exists to get right: a row whose runs are not
    // priced is "not estimated", which is not "free". A decoder that defaulted
    // to `0` would draw a repository that spent money as one that spent none.
    const view = decoded(summaryDocument())
    expect(view.repositories[1]?.computeMicros).toBeNull()
    expect(view.repositories[1]?.runs).toBe(2)
    expect(view.repositories[1]?.computeRuns).toBe(0)
    expect(view.repositories[0]?.computeMicros).toBe(400_000)
    // ...and `0` is still `0` where the CLI means it: a row with no run at all.
    const none = decoded(replacing(['repositories'], []))
    expect(none.totals.computeMicros).toBe(400_000)
    const zero = decoded(replacing(['totals', 'compute_micros'], 0))
    expect(zero.totals.computeMicros).toBe(0)
  })

  it('keeps `spent_micros` and `notional_micros` apart, and sums neither', () => {
    // Two facts, never one: the model money the caps count, and the part a
    // subscription paid, which is nobody's bill. The decoded value has both and
    // no third field that could be their sum, so no screen can draw the sum by
    // accident.
    const view = decoded(summaryDocument())
    expect(view.totals.spentMicros).toBe(1_500_000)
    expect(view.totals.notionalMicros).toBe(1_150_000)
    expect(Object.keys(view.totals).sort()).toEqual([
      'computeMicros',
      'computeRuns',
      'notionalMicros',
      'runs',
      'spentMicros',
      'states',
    ])
    // A row where the two differ in which one is zero is carried as it is.
    expect(view.repositories[1]?.spentMicros).toBe(0)
    expect(view.repositories[1]?.notionalMicros).toBe(900_000)
  })

  it('refuses a negative figure, which is not a document this console knows', () => {
    for (const path of [
      ['totals', 'runs'],
      ['totals', 'spent_micros'],
      ['totals', 'notional_micros'],
      ['totals', 'compute_micros'],
      ['totals', 'compute_runs'],
      ['totals', 'states', 'succeeded'],
    ]) {
      expect(refusalOf(replacing(path, -1)).name, path.join('.')).toBe('not-a-number')
    }
  })
})

describe('the days and the notes the document carries', () => {
  it('keeps `days_unknown`, so a partly-unknown window is drawn as partly unknown', () => {
    const view = decoded(summaryDocument())
    expect(view.daysUnknown).toEqual(['2026-10-29'])
    expect(decoded(replacing(['days_unknown'], [])).daysUnknown).toEqual([])
  })

  it('refuses a day that is not a UTC day, in the window or in the list', () => {
    for (const path of [['since'], ['until'], ['days_unknown']]) {
      const value = path[0] === 'days_unknown' ? ['2026-10-29T00:00:00Z'] : '2026-10-29T00:00:00Z'
      const refusal = refusalOf(replacing(path, value))
      expect(refusal.name, path.join('.')).toBe('not-a-day')
    }
    expect(refusalOf(replacing(['days_unknown'], ['20261029'])).name).toBe('not-a-day')
  })

  it('keeps the CLI’s notes as strings, and refuses one that is not', () => {
    const view = decoded(summaryDocument())
    expect(view.notes).toEqual(summaryDocument()['notes'])
    expect(refusalOf(replacing(['notes'], [{ text: 'a note' }])).name).toBe('not-a-string')
    expect(refusalOf(replacing(['notes'], 'a note')).name).toBe('not-an-array')
  })
})

describe('the text of the document', () => {
  it('decodes a hostile repository name as a string and never as a link', () => {
    // `repo` is a day record's own string: text somebody else's project is
    // named, carried as data. It is not a URL, a path or a command, and nothing
    // in this decoder turns it into one -- a screen draws it through the
    // sanitizer, which is what `WIRE_TEXT_CLASSES`'s row for it is for.
    for (const repo of [
      'acme/../../etc/passwd',
      'https://example.invalid/x',
      'acme/widgets; rm -rf /',
      '$(id)',
      'acme/wid\u0000gets',
      '<script>alert(1)</script>',
    ]) {
      const view = decoded(replacing(['repositories'], [
        {
          slug: 'a1b2c3d4',
          repo,
          runs: 1,
          states: { succeeded: 1, failed: 0, halted: 0, cancelled: 0, infra_error: 0, other: 0 },
          spent_micros: 1,
          notional_micros: 0,
          compute_micros: null,
          compute_runs: 0,
        },
      ]))
      expect(typeof view.repositories[0]?.repo).toBe('string')
      expect(view.repositories[0]?.repo).toBe(repo)
    }
  })

  it('refuses a name that is not a string', () => {
    for (const path of [
      ['project'],
      ['repositories'],
      ['totals'],
      ['repositories', '0', 'repo'],
    ]) {
      const value = path[path.length - 1] === 'repo' ? 7 : null
      expect(decodeRunsSummary(replacing(path, value), 'the runs summary document').ok, path.join('.')).toBe(false)
    }
  })

  it('classifies every string key it draws, and leaves the days unclassified', () => {
    // The document's drawn strings are the module's `WIRE_TEXT_CLASSES` rows and
    // its days are `NON_TEXT_STRING_KEYS`: a key in neither would be a value a
    // screen had no class for.
    expect(WIRE_TEXT_CLASSES['project']).toBeDefined()
    expect(WIRE_TEXT_CLASSES['repo']).toBeDefined()
    expect(WIRE_TEXT_CLASSES['slug']).toBeDefined()
    expect(WIRE_TEXT_CLASSES['notes']).toBe('sentence')
    for (const key of ['since', 'until', 'days_unknown']) {
      expect(NON_TEXT_STRING_KEYS, key).toContain(key)
      expect(WIRE_TEXT_CLASSES[key], key).toBeUndefined()
    }
  })
})

describe('a key this build does not know', () => {
  it('is ignored rather than refused, so a newer CLI’s field is not a skew', () => {
    // The owner's ruling of 2026-10-30: a member the schema does not know is a
    // field a newer CLI added within the same version (D784), and refusing it
    // would make this console unable to read the release after it.
    const before = decoded(summaryDocument())
    const withKey = decoded({
      ...summaryDocument(),
      fleet_size: 3,
      totals: { ...(summaryDocument()['totals'] as Record<string, unknown>), estimate_micros: 12 },
    })
    expect(withKey).toEqual(before)
    expect('fleetSize' in withKey).toBe(false)
    expect('estimateMicros' in withKey.totals).toBe(false)
  })
})
