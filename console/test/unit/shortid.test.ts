// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The short run ID and its collision rule (`console/src/shared/shortid.ts`;
// docs/short-run-ids/spec.md parts 1 and 3, reconciliation F2, D1701, D1706).
//
// This file holds the console's half of the one rule. Its cases are the CLI's
// own (the plan's SR1: `internal/task/shortid_test.go`, `TestShortRun`,
// `TestDisplayRunsCollision`, `TestDisplayRunsCrossesTheDash`,
// `TestDisplayRunsSameIDTwoRepos`): a run named four characters on one side and
// five on the other is two answers to "which run is this", so the two tables
// have to stay value for value.
//
// **The shared table is executed here, not only described.** The written-out
// cases below say what each part of the rule *is*; the last block reads
// `test/fixtures/shortid_cases.json` -- SR1's case table, which the Go test
// `internal/watch/console_parity_test.go` holds byte-equal to
// `internal/task/testdata/shortid_cases.json` -- and runs every case of it
// through this module. Held as data and never run, the table would only prove
// the two files are equal; run, it proves the two *helpers* answer every case
// the same way, which is the point of sharing it (D1706).
//
// Four things are held, one per block of written-out cases:
//
//   1. `shortRun` is the last four characters, the whole string when shorter,
//      and it counts **code points** the way Go's `task.ShortRun` does, so the
//      two never answer a different string for the same input;
//   2. a collision extends the ending one character at a time until it tells
//      the runs apart, and stops there -- never a character more;
//   3. the extension is a raw suffix that **crosses the dash**: the "-" is
//      part of the ending, so a pair of canonical IDs is told apart by the
//      digit before it and not by the four hex digits, which they share;
//   4. two repositories that hold the same run ID cannot be told apart by any
//      suffix, so both are shown as `<slug>/<ending>`.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { MIN_ENDING, displayRuns, shortRun } from '../../src/shared/shortid'

// ---------------------------------------------------------------------------
// The case table the CLI writes and this console reads.
// ---------------------------------------------------------------------------

/** ShortCase is one `short[]` entry: an id and the short form of it. */
interface ShortCase {
  readonly id: string
  readonly short: string
}

/** DisplayRefCase is one ref of a `display[]` entry: the repository, the id,
 * and the ending the rule shows that ref with. */
interface DisplayRefCase {
  readonly slug: string
  readonly run_id: string
  readonly shown: string
}

/** DisplayCase is one `display[]` entry: the case's name, and the refs printed
 * together. The name is not read -- the endings are the whole claim -- and it
 * is here so that this file's shape follows the table's own. */
interface DisplayCase {
  readonly kind: string
  readonly refs: readonly DisplayRefCase[]
}

const CASES = JSON.parse(
  readFileSync(join(__dirname, '..', 'fixtures', 'shortid_cases.json'), 'utf8'),
) as { readonly min_ending: number; readonly short: readonly ShortCase[]; readonly display: readonly DisplayCase[] }

describe('shortRun', () => {
  it('answers the last four characters of a run ID', () => {
    // The red step of SR1, on this side: the ID the whole plan is written
    // around, cut to the four hex digits that are its short form.
    expect(shortRun('20261108-115248-3bb2')).toBe('3bb2')
  })

  it('answers the whole string when it is not longer than four characters', () => {
    const cases: readonly (readonly [string, string])[] = [
      ['3bb2', '3bb2'], // exactly four: nothing to cut
      ['3bb', '3bb'],
      ['a', 'a'],
      ['', ''],
    ]
    for (const [id, want] of cases) expect(shortRun(id), id).toBe(want)
  })

  it('counts code points, not UTF-16 units, as `task.ShortRun` does', () => {
    // A run ID is ASCII, so this cannot happen on the wire -- but the two
    // helpers may not disagree about the same input, and `slice(-4)` would
    // split the astral character here where Go keeps it whole (F2, D1706).
    expect(shortRun('abcde\u{1F600}')).toBe('cde\u{1F600}')
    expect(shortRun('abc\u{1F600}')).toBe('abc\u{1F600}')
  })
})

describe('displayRuns: a collision extends the ending', () => {
  it('extends a pair that shares its last four characters', () => {
    // SR1's red step, value for value: the two runs end `3bb2`, share the dash
    // that follows, and are told apart only by the second-field digit before
    // it -- so the shortest distinguishing ending is six characters.
    expect(
      displayRuns([
        { slug: 'acme/widgets', runId: '20261108-115248-3bb2' },
        { slug: 'acme/widgets', runId: '20261107-010101-3bb2' },
      ]),
    ).toEqual(['8-3bb2', '1-3bb2'])
  })

  it('leaves a lone run at four characters', () => {
    expect(displayRuns([{ slug: 'acme/widgets', runId: '20261108-115248-3bb2' }])).toEqual(['3bb2'])
  })

  it('extends only the runs a collision touches, and stops as soon as it can', () => {
    // The third run's ending is already unique at four characters, so it must
    // not grow; the pair grows to the shortest length that separates it. An
    // implementation that padded every row to the longest ending would fail
    // here, and so would one that grew past the first distinguishing length.
    expect(
      displayRuns([
        { slug: 'acme/widgets', runId: '20261108-115248-3bb2' },
        { slug: 'acme/widgets', runId: '20261107-010101-3bb2' },
        { slug: 'acme/widgets', runId: '20261108-115249-9abc' },
      ]),
    ).toEqual(['8-3bb2', '1-3bb2', '9abc'])
  })

  it('keeps the endings distinct for a three-way collision', () => {
    // Three runs share `3bb2`; the pair that agrees through the second field's
    // digit grows one more character than the third, which parts from them one
    // digit earlier -- each ending is the shortest that tells its run from the
    // two others.
    expect(
      displayRuns([
        { slug: 'acme/widgets', runId: '20261108-115248-3bb2' },
        { slug: 'acme/widgets', runId: '20261107-010101-3bb2' },
        { slug: 'acme/widgets', runId: '20261108-115249-3bb2' },
      ]),
    ).toEqual(['8-3bb2', '1-3bb2', '9-3bb2'])
  })
})

describe('displayRuns: the extension crosses the dash', () => {
  it('carries the dash inside the ending rather than cutting after it', () => {
    // The rule is a raw suffix, not "the text after the last dash": cutting
    // after the dash would leave both runs at `3bb2` and never tell them
    // apart. The five-character ending `-3bb2` is the first that reaches the
    // dash, and it is still shared, so the answer is the six-character suffix.
    const shown = displayRuns([
      { slug: 'acme/widgets', runId: '20261108-115248-3bb2' },
      { slug: 'acme/widgets', runId: '20261107-010101-3bb2' },
    ])
    expect(shown).toHaveLength(2)
    for (const ending of shown) expect(ending).toContain('-')
  })

  it('stops on the dash itself when that is what distinguishes the runs', () => {
    // A run whose whole ID is the four characters, beside a canonical ID that
    // ends with them: at four characters both are `3bb2`, and one character
    // more -- the dash -- is already unique, so the extension stops there.
    expect(
      displayRuns([
        { slug: 'acme/widgets', runId: '20261108-115248-3bb2' },
        { slug: 'acme/widgets', runId: '3bb2' },
      ]),
    ).toEqual(['-3bb2', '3bb2'])
  })
})

describe('displayRuns: the same run ID in two repositories', () => {
  it('shows the repository in front of the ending, since no suffix separates them', () => {
    // No suffix tells two identical run IDs apart, so the repository is the
    // only thing that does -- and both rows get it, or the pair would read as
    // one run drawn twice.
    expect(
      displayRuns([
        { slug: 'acme/widgets', runId: '20261108-115248-3bb2' },
        { slug: 'acme/gadgets', runId: '20261108-115248-3bb2' },
      ]),
    ).toEqual(['acme/widgets/3bb2', 'acme/gadgets/3bb2'])
  })

  it('does not prefix a run repeated within one repository', () => {
    // The same row twice is one run drawn twice, not two repositories holding
    // one ID: the rule words the case as "different repositories", and the
    // slug would be noise here.
    expect(
      displayRuns([
        { slug: 'acme/widgets', runId: '20261108-115248-3bb2' },
        { slug: 'acme/widgets', runId: '20261108-115248-3bb2' },
      ]),
    ).toEqual(['3bb2', '3bb2'])
  })
})

describe('MIN_ENDING', () => {
  it('is four, the length the CLI refuses an ending below', () => {
    // D1701: an ending shorter than four is `ErrTooShort` on the CLI, so the
    // console may not draw one the CLI would refuse to take back.
    expect(MIN_ENDING).toBe(4)
  })
})

describe('the case table the CLI and this console share', () => {
  it('runs every case of SR1’s table through this module, and every answer matches', () => {
    // The table is SR1's own, read here rather than restated: the Go test
    // `internal/watch/console_parity_test.go` holds it byte-equal to
    // `internal/task/testdata/shortid_cases.json`, and this case is the other
    // half of that -- the console *answers* every entry, so the two languages
    // cannot disagree about a case either file carries (D1706, F2). A table
    // only held equal would leave the console's helper free to drift.
    expect(CASES.min_ending).toBe(MIN_ENDING)
    expect(CASES.short).not.toHaveLength(0)
    expect(CASES.display).not.toHaveLength(0)
    for (const one of CASES.short) expect(shortRun(one.id), one.id).toBe(one.short)
    for (const table of CASES.display) {
      const refs = table.refs.map((ref) => ({ slug: ref.slug, runId: ref.run_id }))
      expect(displayRuns(refs), table.kind).toEqual(table.refs.map((ref) => ref.shown))
    }
  })
})
