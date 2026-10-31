// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Spend screen's row model, its three series and its caps panel
// (desktop-console plan Task 2.2; design section 6.2 screen 2, D15;
// `console/docs/json-contract.md` sections 3 and 4).
//
// Four things are being held here, and each is a sentence from the plan or the
// design rather than a preference:
//
//   1. **no screen ever adds a notional to a real total.** The plan says it, the
//      design's first rule says it, and `ee/report/report.go`'s package
//      comment says it in the CLI's own voice ("Model dollars, notional dollars
//      ... and compute dollars are three separate columns and are never added
//      together"). The test below pins it to the *series model* -- the number
//      the chart is drawn from -- and compares it against the sum, so a model
//      that started adding them fails here with both figures named;
//   2. **a partial day is hollow** -- `(partial)` is the CLI's own word
//      (`ee/report/render.go`'s table appends it to the row key) and the
//      design draws a day that is not final yet as a hollow bar;
//   3. **the person breakdown is off by default** (design D15). The rows are in
//      the document; the screen draws none of them until it is asked to, and it
//      says why. When it is asked, a person is drawn through `sanitize.ts`'s
//      `person` class, so an address is the console's marker and never the
//      address;
//   4. **a day the history does not hold is a gap, not a zero.** `report` reads
//      a window and the days before it are pruned, so the console enumerates
//      the range itself and a missing day becomes a point that breaks the line.
//
// There is no recorded corpus for these two documents -- `test/fixtures/`
// holds the watch feed's documents and nothing else -- so the corpus is here,
// in the shape `JSONLine` writes: `*_micros` integers with their `*_usd`
// strings, `null` for a figure the document does not carry, `partial` on a day
// that is not final. A number this file asserts on is a number the real
// `report --json` writes for the same facts; the shapes are held to the Go
// structs by name where a constant could drift (`SPEND_DIMENSIONS` against
// `report.go`'s `Dim` values, `SPEND_MAX_RANGE_DAYS` against `reportMaxDays`
// and `ipc.ts`'s `REPORT_MAX_DAYS`).

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import {
  ESTIMATED_MARK,
  GAP_MARK,
  NOTIONAL_MARK,
  UNAVAILABLE_MARK,
  estimated,
  formatReading,
  gap,
  measured,
  notional,
  unavailable,
} from '../../src/shared/availability'
import type { JSONDoc } from '../../src/source/source'
import { LIVE_THEMES } from '../../src/renderer/screens/live'
import type { ScreenFactory as LiveScreenFactory } from '../../src/renderer/screens/live'
import {
  PARTIAL_MARKER,
  RESERVE_LABEL,
  SPEND_ACCOUNTS,
  SPEND_DIMENSIONS,
  SPEND_MAX_RANGE_DAYS,
  SPEND_THEMES,
  UNREADABLE_MARKER,
  buildSpendModel,
  dayRange,
  keyClass,
  peopleArguments,
  renderSpend,
  renderSpendFrame,
  scopeOf,
} from '../../src/renderer/screens/spend'
import { BUSY_SLOT_CLASS } from '../../src/renderer/section'
import { drawnRows } from '../support/spend'
import type {
  SpendAccount,
  SpendBar,
  SpendFactory,
  SpendModel,
  SpendPeople,
  SpendRow,
  ScreenElement,
  ScreenFactory,
} from '../../src/renderer/screens/spend'

// ---------------------------------------------------------------------------
// The two documents.
// ---------------------------------------------------------------------------

/**
 * DOLLARS is the scale the document's `*_micros` integers are in
 * (`budget.Micros`, and `USD6` is the same integer with six decimals). The
 * figures below are written as micros so that every one of them is an integer
 * a reader can check against the string beside it.
 */
const DOLLARS = 1_000_000

/**
 * DAY_ROWS is a day-grouped report of `since` 2026-10-12 to `until`
 * 2026-10-15 with **three** rows: the 14th has no document at all, which is the
 * gap the series has to break on. The 15th carries `partial: true` and the
 * totals row does too, which is what `report` writes when the range includes a
 * day that is not final (`ee/report/cli/report.go`: today and yesterday keep
 * changing).
 */
const DAY_ROWS = [
  {
    key: '2026-10-12',
    start: '2026-10-12',
    end: '2026-10-12',
    partial: false,
    model_micros: 1_250_000,
    model_usd: '1.250000',
    notional_micros: 400_000,
    notional_usd: '0.400000',
    compute_micros: 120_000,
    compute_usd: '0.120000',
    compute_estimated: true,
    run_hours: 1.5,
    runs: 2,
    calls: 17,
  },
  {
    key: '2026-10-13',
    start: '2026-10-13',
    end: '2026-10-13',
    partial: false,
    model_micros: 900_000,
    model_usd: '0.900000',
    // A day with no subscription spend at all: `notional_micros` is null
    // (not tracked), which is `n/a` and never `$0.00`.
    notional_micros: null,
    notional_usd: null,
    // No run of the day had its compute estimated: `n/a`, never 0.
    compute_micros: null,
    compute_usd: null,
    compute_estimated: false,
    run_hours: null,
    runs: 1,
    calls: 9,
  },
  {
    key: '2026-10-15',
    start: '2026-10-15',
    end: '2026-10-15',
    partial: true,
    model_micros: 300_000,
    model_usd: '0.300000',
    notional_micros: 100_000,
    notional_usd: '0.100000',
    compute_micros: null,
    compute_usd: null,
    compute_estimated: false,
    run_hours: null,
    runs: 1,
    calls: 4,
  },
] as const

const DAY_TOTALS = {
  key: 'TOTAL',
  end: '2026-10-15',
  partial: true,
  model_micros: 2_450_000,
  model_usd: '2.450000',
  notional_micros: 500_000,
  notional_usd: '0.500000',
  compute_micros: 120_000,
  compute_usd: '0.120000',
  compute_estimated: true,
  run_hours: 1.5,
  runs: 4,
  calls: 30,
} as const

/** DAY_REPORT is the document those rows came out of. */
const DAY_REPORT: JSONDoc = {
  project: 'fugaro-demo',
  source: 'firestore',
  degraded: false,
  by: 'day',
  since: '2026-10-12',
  until: '2026-10-15',
  weeks: 'iso-utc-monday',
  rows: DAY_ROWS,
  totals: DAY_TOTALS,
  warnings: [],
}

/**
 * MODEL_ROWS is a by-model report: no `start` (the key is not a period), a
 * `tokens` object on a model whose tokens were tracked and none on one whose
 * were not, and `compute_micros` null on both -- `report` writes compute per
 * model as not-recorded ("-" in the table, null in JSON), which is a third
 * thing again from "no run was estimated".
 */
const MODEL_ROWS = [
  {
    key: 'claude-sonnet-5',
    end: '2026-10-15',
    partial: false,
    model_micros: 2_000_000,
    model_usd: '2.000000',
    notional_micros: 500_000,
    notional_usd: '0.500000',
    compute_micros: null,
    compute_usd: null,
    compute_estimated: false,
    run_hours: null,
    runs: 3,
    calls: 21,
    tokens: { in: 120_000, out: 8_000, cache_read: 400_000, cache_write: 12_000 },
  },
  {
    key: 'claude-opus-5',
    end: '2026-10-12',
    partial: false,
    model_micros: 450_000,
    model_usd: '0.450000',
    notional_micros: null,
    notional_usd: null,
    compute_micros: null,
    compute_usd: null,
    compute_estimated: false,
    run_hours: null,
    runs: 1,
    calls: 6,
  },
] as const

const MODEL_REPORT: JSONDoc = {
  project: 'fugaro-demo',
  source: 'firestore',
  degraded: false,
  by: 'model',
  since: '2026-10-12',
  until: '2026-10-15',
  weeks: 'iso-utc-monday',
  rows: MODEL_ROWS,
  totals: {
    key: 'TOTAL',
    end: '2026-10-15',
    partial: false,
    model_micros: 2_450_000,
    model_usd: '2.450000',
    notional_micros: 500_000,
    notional_usd: '0.500000',
    compute_micros: null,
    compute_usd: null,
    compute_estimated: false,
    run_hours: null,
    runs: 4,
    calls: 27,
  },
  warnings: [],
}

/**
 * STATS_ROWS is `runs stats --json`'s own row shape (console/docs/json-contract.md
 * section 6.10, `internal/userstats.Row`): the counts that add up to `runs`, the
 * two money accounts, and the average with the sample it is over. The first row
 * is a launcher the recorder knew (an address, because that is what a
 * `launch.json` records), the second another one, and the third the CLI's own
 * unattributed key (`userstats.UnknownUser`) -- a group of runs and not a person.
 */
const STATS_ROWS = [
  {
    user: 'ada@example.com',
    currency: 'USD',
    runs: 3,
    succeeded: 2,
    failed: 1,
    halted: 0,
    infra_error: 0,
    other: 0,
    no_record: 0,
    counted_usd: 1.5,
    notional_usd: 0.4,
    counted_runs: 3,
    avg_counted_usd: 0.5,
  },
  {
    user: 'bob@example.com',
    currency: 'USD',
    runs: 1,
    succeeded: 1,
    failed: 0,
    halted: 0,
    infra_error: 0,
    other: 0,
    no_record: 0,
    counted_usd: 0.25,
    notional_usd: 0,
    counted_runs: 1,
    avg_counted_usd: 0.25,
  },
  {
    user: 'unknown',
    currency: 'USD',
    runs: 1,
    succeeded: 0,
    failed: 0,
    halted: 0,
    infra_error: 0,
    other: 0,
    no_record: 1,
    counted_usd: 0,
    notional_usd: 0,
    // No run of this group settled a real-dollar cost: the CLI writes 0 with a
    // counted-runs of 0, and the average is **unavailable** rather than $0.00.
    counted_runs: 0,
    avg_counted_usd: 0,
  },
] as const

/** STATS_TOTALS is the same document's total line: one per currency, with the
 * CLI's own empty `user` (`internal/userstats.Aggregate`). */
const STATS_TOTALS = [
  {
    user: '',
    currency: 'USD',
    runs: 5,
    succeeded: 3,
    failed: 1,
    halted: 0,
    infra_error: 0,
    other: 0,
    no_record: 1,
    counted_usd: 1.75,
    notional_usd: 0.4,
    counted_runs: 4,
    // The CLI rounds every published figure to the cent (`userstats.cents`):
    // the total's average is $0.44, not 0.4375.
    avg_counted_usd: 0.44,
  },
] as const

/** STATS_DOCUMENT is the by-person aggregate those rows came out of, in the
 * shape `internal/cli/runs_stats.go`'s `userStatsDoc` writes: the view the CLI
 * resolved, the identity it resolved it for, whether the installation's own
 * lists place that identity as an operator, the window, the read's own bound and
 * the runs of the window that never started. */
function statsDocument(over: JSONDoc = {}): JSONDoc {
  return {
    project: 'fugaro-demo',
    by: 'user',
    view: 'all',
    you: 'ada@example.com',
    operator: true,
    since: '2026-10-12',
    until: '2026-10-15',
    max_runs: 5000,
    truncated: false,
    rows: STATS_ROWS,
    totals: STATS_TOTALS,
    unlaunched: 2,
    notes: ['these are every launcher of this project in the window'],
    ...over,
  }
}

/**
 * statsOwnDocument is the **same read** as `statsDocument` in the shape the CLI
 * really publishes for a private view, and the two are not interchangeable.
 *
 * `internal/cli/runs_stats.go` filters the private view *before* it aggregates:
 * it deletes every run whose recorded launcher is not the caller, and only then
 * does `userstats.Aggregate` build the rows -- and the totals **out of those
 * rows** (`userstats.go:229`). So an `own` document carries the caller's runs
 * alone in `rows` *and* in `totals` (whose single line is that person's figures,
 * not the window's), and the two states the CLI cannot key the view in -- `you`
 * empty, or the unattributed key, which it maps to the empty identity before the
 * filter (`userstats.CallerKey`) -- delete *every* run, so both come back `[]`.
 * A fixture that pairs an `own` view with a whole-window total is a document no
 * CLI writes, and a screen written against it would tell a launcher that the
 * number under their own row was everyone's.
 */
function statsOwnDocument(you: string): JSONDoc {
  const identified = you !== '' && you !== 'unknown'
  const rows = identified ? STATS_ROWS.filter((row) => row.user === you) : []
  // The CLI writes one total per currency with the empty `user`, summing the
  // rows it kept -- for one row, that row's own figures.
  const totals = rows.map((row) => ({ ...row, user: '' }))
  return statsDocument({
    view: 'own',
    you,
    operator: false,
    rows: [...rows],
    totals,
    notes: ['this read is your own runs in the window'],
  })
}

/** statsDefensiveDocument is a document **the CLI does not write**: a private
 * view that carries other people's rows, a `by` or a `view` word this build does
 * not know. It exists to pin the screen's defence rather than the wire, which is
 * why it is built from the whole-window rows on purpose. */
function statsDefensiveDocument(over: JSONDoc = {}): JSONDoc {
  return {
    project: 'fugaro-demo',
    by: 'user',
    view: 'all',
    you: 'ada@example.com',
    operator: true,
    since: '2026-10-12',
    until: '2026-10-15',
    max_runs: 5000,
    truncated: false,
    rows: STATS_ROWS,
    totals: STATS_TOTALS,
    unlaunched: 2,
    notes: ['these are every launcher of this project in the window'],
    ...over,
  }
}

/**
 * PERSON_ROWS is a by-person report: a requester the recorder knew (an address,
 * because that is what the runner records), and the CLI's own `unknown` bucket
 * for the runs with no requester (`budget.UnknownPerson`, `internal/budget/
 * rollup.go:18`).
 */
const PERSON_ROWS = [
  {
    key: 'ada@example.com',
    end: '2026-10-15',
    partial: false,
    model_micros: 1_800_000,
    model_usd: '1.800000',
    notional_micros: 200_000,
    notional_usd: '0.200000',
    compute_micros: null,
    compute_usd: null,
    compute_estimated: false,
    run_hours: null,
    runs: 3,
    calls: 19,
  },
  {
    key: 'unknown',
    end: '2026-10-13',
    partial: false,
    model_micros: 650_000,
    model_usd: '0.650000',
    notional_micros: null,
    notional_usd: null,
    compute_micros: null,
    compute_usd: null,
    compute_estimated: false,
    run_hours: null,
    runs: 1,
    calls: 8,
  },
] as const

const PERSON_REPORT: JSONDoc = {
  project: 'fugaro-demo',
  source: 'firestore',
  degraded: false,
  by: 'person',
  since: '2026-10-12',
  until: '2026-10-15',
  weeks: 'iso-utc-monday',
  rows: PERSON_ROWS,
  totals: {
    key: 'TOTAL',
    end: '2026-10-15',
    partial: false,
    model_micros: 2_450_000,
    model_usd: '2.450000',
    notional_micros: 200_000,
    notional_usd: '0.200000',
    compute_micros: null,
    compute_usd: null,
    compute_estimated: false,
    run_hours: null,
    runs: 4,
    calls: 27,
  },
  warnings: [],
}

/**
 * MODE_ROWS is a by-mode report: `report --by mode` reads the run records
 * rather than the spend history (`source: "run-records"`, and not degraded), so
 * a row's key is a rigor mode or the CLI's own `none` (`ee/report/
 * report.go`'s `ModeNone`) for a run whose record carries no mode -- or one
 * this build does not know, which reads as `none` rather than as the raw text.
 * There is no `start` (a mode is not a period), `calls` is 0 and `run_hours` is
 * null (a run record carries neither), and a row with no estimated run reads
 * compute `n/a` rather than 0.
 */
const MODE_ROWS = [
  {
    key: 'pro',
    end: '2026-10-15',
    partial: false,
    model_micros: 1_500_000,
    model_usd: '1.500000',
    notional_micros: null,
    notional_usd: null,
    compute_micros: 250_000,
    compute_usd: '0.250000',
    compute_estimated: true,
    run_hours: null,
    runs: 1,
    calls: 0,
  },
  {
    key: 'none',
    end: '2026-10-17',
    partial: false,
    model_micros: 500_000,
    model_usd: '0.500000',
    notional_micros: 3_000_000,
    notional_usd: '3.000000',
    compute_micros: null,
    compute_usd: null,
    compute_estimated: false,
    run_hours: null,
    runs: 2,
    calls: 0,
  },
] as const

const MODE_REPORT: JSONDoc = {
  project: 'fugaro-demo',
  source: 'run-records',
  degraded: false,
  by: 'mode',
  since: '2026-10-12',
  until: '2026-10-15',
  weeks: 'iso-utc-monday',
  rows: MODE_ROWS,
  totals: {
    key: 'TOTAL',
    end: '2026-10-17',
    partial: false,
    model_micros: 2_000_000,
    model_usd: '2.000000',
    notional_micros: 3_000_000,
    notional_usd: '3.000000',
    compute_micros: 250_000,
    compute_usd: '0.250000',
    compute_estimated: true,
    run_hours: null,
    runs: 3,
    calls: 0,
  },
  warnings: [],
}

/**
 * MODE_UNREADABLE_REPORT is the document `report --by mode` writes for a window
 * that holds one readable run record and one it cannot read at all: the JSON is
 * copied **verbatim** from the merged CLI's own output for exactly that window
 * (`ee/report/cli/report_test.go`'s `TestReportByModeUnreadableRecord`, whose
 * fixture seeds `pro` run `20261016-120000-aaaa` and corrupt run
 * `20261017-120000-bbbb`). Two things here are the CLI's and not this file's
 * invention: the unreadable row `none` carries `unreadable: true` with **every
 * money key null** -- a run whose record the build cannot read did launch, so
 * it counts once in `runs`, and its money was never measured, so a `0` there
 * would be a measurement nobody made (`n/a`, never `$0.00`) -- and the readable
 * row keeps its own sums. The `totals` row sums the readable row only, and is
 * **not** marked: one readable row makes the total a measurement.
 */
const MODE_UNREADABLE_ROWS = [
  {
    key: 'pro',
    end: '2026-10-16',
    partial: false,
    model_micros: 1_500_000,
    model_usd: '1.500000',
    notional_micros: 0,
    notional_usd: '0.000000',
    compute_micros: 250_000,
    compute_usd: '0.250000',
    compute_estimated: true,
    run_hours: null,
    runs: 1,
    calls: 0,
  },
  {
    key: 'none',
    end: '2026-10-17',
    partial: false,
    unreadable: true,
    model_micros: null,
    model_usd: null,
    notional_micros: null,
    notional_usd: null,
    compute_micros: null,
    compute_usd: null,
    compute_estimated: false,
    run_hours: null,
    runs: 1,
    calls: 0,
  },
] as const

const MODE_UNREADABLE_TOTALS = {
  key: 'TOTAL',
  end: '2026-10-17',
  partial: false,
  model_micros: 1_500_000,
  model_usd: '1.500000',
  notional_micros: 0,
  notional_usd: '0.000000',
  compute_micros: 250_000,
  compute_usd: '0.250000',
  compute_estimated: true,
  run_hours: null,
  runs: 2,
  calls: 0,
} as const

const MODE_UNREADABLE_REPORT: JSONDoc = {
  project: 'aurora',
  source: 'run-records',
  degraded: false,
  by: 'mode',
  since: '2026-10-01',
  until: '2026-10-20',
  weeks: 'iso-utc-monday',
  rows: MODE_UNREADABLE_ROWS,
  totals: MODE_UNREADABLE_TOTALS,
  warnings: [
    'these rows come from run records (the runs bucket), not the Firestore spend history: a run\'s rigor mode is written in its own record, and every run counts once, on its launch day',
    '1 of the window\'s runs have no record this report can read (1 hold a result.json this build cannot read, 0 have none in the bucket): they count in RUNS, and the money is short by whatever they spent',
  ],
}

/**
 * MODE_ALL_UNREADABLE_REPORT is the same read for a window in which **no** run
 * record could be read, copied verbatim from the CLI's output for
 * `TestReportByModeEveryRecordUnreadable`'s two corrupt runs. One row and the
 * totals line are both marked: the document's rule for the totals is that it
 * follows the rows (`Row.Unreadable`, `report.Build`; contract §4), so a total
 * that sums nothing but unreadable rows reads `n/a` rather than the `$0.00`
 * that sums nothing.
 */
const MODE_ALL_UNREADABLE_REPORT: JSONDoc = {
  project: 'aurora',
  source: 'run-records',
  degraded: false,
  by: 'mode',
  since: '2026-10-01',
  until: '2026-10-20',
  weeks: 'iso-utc-monday',
  rows: [
    {
      key: 'none',
      end: '2026-10-17',
      partial: false,
      unreadable: true,
      model_micros: null,
      model_usd: null,
      notional_micros: null,
      notional_usd: null,
      compute_micros: null,
      compute_usd: null,
      compute_estimated: false,
      run_hours: null,
      runs: 2,
      calls: 0,
    },
  ],
  totals: {
    key: 'TOTAL',
    end: '2026-10-17',
    partial: false,
    unreadable: true,
    model_micros: null,
    model_usd: null,
    notional_micros: null,
    notional_usd: null,
    compute_micros: null,
    compute_usd: null,
    compute_estimated: false,
    run_hours: null,
    runs: 2,
    calls: 0,
  },
  warnings: [
    'these rows come from run records (the runs bucket), not the Firestore spend history: a run\'s rigor mode is written in its own record, and every run counts once, on its launch day',
    '2 of the window\'s runs have no record this report can read (2 hold a result.json this build cannot read, 0 have none in the bucket): they count in RUNS, and the money is short by whatever they spent',
  ],
}

/**
 * THE_REPOS is `budget show --json`'s `repos` array with one repository at each
 * binding scope the CLI can name: a repository with its own two caps, one whose
 * per-run cap comes from the repository default, and one whose per-run cap
 * comes from the global fallback. The second carries `daily_cap_usd: null` with
 * `daily_cap_source: "none"`, which is the case the design's panel exists for:
 * a cap of $0 and a cap nobody set are different facts.
 */
const THE_REPOS = [
  {
    slug: 'widget',
    repo: 'acme/widget',
    daily_cap_usd: 5,
    daily_cap_source: 'repo',
    per_run_cap_usd: 1.5,
    per_run_cap_source: 'repo',
    counted_usd: 3,
    spent_usd: 2,
    notional_usd: 1,
    headroom_usd: 2,
    kill: null,
    running: [],
  },
  {
    slug: 'docs',
    repo: 'acme/docs',
    daily_cap_usd: null,
    daily_cap_source: 'none',
    per_run_cap_usd: 2,
    per_run_cap_source: 'default',
    counted_usd: 0,
    spent_usd: 0,
    notional_usd: 0,
    headroom_usd: null,
    kill: null,
    running: [],
  },
  {
    slug: 'infra',
    // No `repo`: `showRepo.Repo` is `omitempty`, so a record with no path leaves
    // the slug as the only name there is.
    daily_cap_usd: 7,
    daily_cap_source: 'repo',
    per_run_cap_usd: 25,
    per_run_cap_source: 'global',
    counted_usd: 4,
    spent_usd: 4,
    notional_usd: 0,
    headroom_usd: 3,
    kill: null,
    running: [],
  },
] as const

const THE_GLOBAL = {
  daily_cap_usd: 25,
  per_run_cap_usd: 25,
  counted_usd: 12.5,
  spent_usd: 10,
  notional_usd: 4.2,
  headroom_usd: 12.5,
  kill: null,
}

const BUDGET: JSONDoc = {
  project: 'fugaro-demo',
  gcp_project: 'demo-1234',
  mode: 'enforce',
  day: '2026-10-15',
  max_reserve_usd: 50,
  global: THE_GLOBAL,
  defaults: { repo_daily_cap_usd: 10, repo_per_run_cap_usd: 2 },
  repos: THE_REPOS,
  running: [],
  warnings: ['the day record for 2026-10-14 is missing, so counted_usd for that day is a lower bound'],
}

/** modelOf is the screen's model for the day report and the caps document. */
function modelOf(report: JSONDoc = DAY_REPORT, budget: JSONDoc | null = BUDGET): SpendModel {
  return buildSpendModel(report, budget)
}

/** seriesOf is one account's series, asserted to be there. */
function seriesOf(model: SpendModel, account: SpendAccount) {
  const found = model.series.find((series) => series.account === account)
  if (found === undefined) throw new Error(`no ${account} series`)
  return found
}

/** pointFor is one point by its key, asserted to be there. */
function pointFor(series: ReturnType<typeof seriesOf>, key: string) {
  const found = series.points.find((point) => point.key === key)
  if (found === undefined) throw new Error(`no point ${key} in the ${series.account} series`)
  return found
}

/** barFor is one account's bar for one key of the chart, asserted to be there.
 * The key's group holds a bar per account even where none is drawn -- a value
 * the document does not carry is a bar whose share is a gap -- so this is also
 * how a test says what the drawing had to leave out. */
function barFor(model: SpendModel, key: string, account: SpendAccount): SpendBar {
  const group = [...model.chart.groups, ...model.chart.outside].find((one) => one.key === key)
  const found = group?.bars.find((bar) => bar.account === account)
  if (found === undefined) throw new Error(`no ${account} bar for ${key}`)
  return found
}

/** rowFor is one row by its key. */
function rowFor(model: SpendModel, key: string): SpendRow {
  const found = drawnRows(model).find((row) => row.key === key)
  if (found === undefined) throw new Error(`no row ${key}`)
  return found
}

/**
 * summed is **the mutation this screen must never be**: the billed figure with
 * the notional one added to it. It exists so that the test below can name the
 * wrong number and assert the screen does not draw it -- the plan's "mutation-
 * proven by summing them" -- rather than only asserting the right one. The
 * design's rule is that a person must never be shown a total that adds a
 * subscription's list price to a bill.
 */
function summed(micros: number | null, notionalMicros: number | null): number | null {
  if (micros === null || notionalMicros === null) return null
  return (micros + notionalMicros) / DOLLARS
}

// ---------------------------------------------------------------------------
// The Go and IPC sources the constants answer to.
// ---------------------------------------------------------------------------

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = path.join(HERE, '..', '..', '..')

/**
 * sourceConstant reads a constant's right-hand side out of a source file, Go or
 * TypeScript. It throws rather than returning '' so that a constant that was
 * renamed fails here, by name, instead of comparing an empty string to a
 * number -- the same holding `live.test.ts` puts on the watch thresholds.
 */
function sourceConstant(relative: string, name: string): string {
  const where = path.join(REPO_ROOT, relative)
  const source = readFileSync(where, 'utf8')
  const pattern = String.raw`^\s*(?:export\s+)?(?:const\s+)?${name}(?:\s+Dim)?\s*=\s*([^\n]+)$`
  const found = new RegExp(pattern, 'm').exec(source)
  if (found === null || found[1] === undefined) throw new Error(`${where} has no ${name}`)
  return found[1].trim()
}

describe('the Spend screen constants', () => {
  it('are the CLI\'s eight breakdowns, by name, and cannot drift from them', () => {
    // `ee/report/report.go`'s `Dim` values, one by one: the switch draws
    // the CLI's vocabulary, and a word the CLI renamed would leave the switch
    // offering a breakdown `report` refuses. The whole list against the CLI's
    // own `--by` help is `report-dimensions.test.ts`; this is the same fact
    // held name by name.
    const go = 'ee/report/report.go'
    expect(sourceConstant(go, 'ByDay')).toBe('"day"')
    expect(sourceConstant(go, 'ByWeek')).toBe('"week"')
    expect(sourceConstant(go, 'ByMonth')).toBe('"month"')
    expect(sourceConstant(go, 'ByYear')).toBe('"year"')
    expect(sourceConstant(go, 'ByRepo')).toBe('"repo"')
    expect(sourceConstant(go, 'ByModel')).toBe('"model"')
    expect(sourceConstant(go, 'ByPerson')).toBe('"person"')
    expect(sourceConstant(go, 'ByMode')).toBe('"mode"')

    expect(SPEND_DIMENSIONS).toEqual(['day', 'week', 'month', 'year', 'repo', 'model', 'person', 'mode'])
  })

  it('bounds a day series at the CLI\'s own reportMaxDays, on both sides of it', () => {
    // The CLI bounds `--since` and the resolved range with `reportMaxDays`
    // (`ee/report/cli/report.go`'s `reportMaxDays`), and `src/main/ipc.ts` restates the bound
    // for the argument side. The console's series honours the same number: a
    // document claiming a longer range is one `report` itself refuses.
    expect(sourceConstant('ee/report/cli/report.go', 'reportMaxDays')).toBe('3660')
    expect(sourceConstant('console/src/main/ipc.ts', 'REPORT_MAX_DAYS')).toBe('3660')
    expect(SPEND_MAX_RANGE_DAYS).toBe(3660)
  })

  it('draws the three accounts the design keeps apart, and only those', () => {
    expect(SPEND_ACCOUNTS).toEqual(['model', 'notional', 'compute'])
  })

  it('names the same two themes the Live screen\'s goldens are taken for', () => {
    // Held to the Live screen's own list, which is the claim `spend.ts` makes
    // in its header: two lists of the same two words are exactly the kind of
    // pair that drifts when nothing compares them, and a golden per theme is
    // taken for both screens. `live.test.ts` holds `LIVE_THEMES` to the same
    // literal, so the chain is literal -> `LIVE_THEMES` -> `SPEND_THEMES`.
    expect(SPEND_THEMES).toEqual(LIVE_THEMES)
    expect(SPEND_THEMES).toEqual(['light', 'dark'])
  })

  it('takes the reserve\'s words from the CLI, which prints the same line', () => {
    // `internal/cli/budget.go`'s `printShow` writes "largest single lease (max
    // reserve): %s" for `max_reserve_usd`, above the `project` block. The panel
    // says the CLI's words about the CLI's fact rather than inventing a second
    // phrase, the way `PARTIAL_MARKER` reuses `report`'s own marker.
    const source = readFileSync(path.join(REPO_ROOT, 'internal/cli/budget.go'), 'utf8')
    expect(RESERVE_LABEL).toBe('largest single lease (max reserve)')
    expect(source).toContain(RESERVE_LABEL)
  })
})

// ---------------------------------------------------------------------------
// The day range: the console's own arithmetic.
// ---------------------------------------------------------------------------

/** plusDays moves a UTC day by a number of days, in the test, so that a
 * boundary can be written down without a second day enumeration. */
function plusDays(day: string, days: number): string {
  return new Date(Date.parse(`${day}T00:00:00Z`) + days * 86_400_000).toISOString().slice(0, 10)
}

describe('dayRange', () => {
  it('is every UTC day of the range, inclusive at both ends', () => {
    expect(dayRange('2026-10-12', '2026-10-15')).toEqual([
      '2026-10-12',
      '2026-10-13',
      '2026-10-14',
      '2026-10-15',
    ])
    expect(dayRange('2026-10-12', '2026-10-12')).toEqual(['2026-10-12'])
  })

  it('crosses a month end and a leap day', () => {
    // The arithmetic is the console's own, so it is held at the two places a
    // hand-rolled one goes wrong.
    expect(dayRange('2026-02-27', '2026-03-02')).toEqual([
      '2026-02-27',
      '2026-02-28',
      '2026-03-01',
      '2026-03-02',
    ])
    expect(dayRange('2028-02-28', '2028-03-01')).toEqual(['2028-02-28', '2028-02-29', '2028-03-01'])
  })

  it('refuses a range it cannot enumerate, rather than guessing one', () => {
    // A missing date: `report` always writes both, so an absent one is not a
    // range the console will invent.
    expect(dayRange(null, '2026-10-15')).toBeNull()
    expect(dayRange('2026-10-12', null)).toBeNull()
    // A range that runs backwards.
    expect(dayRange('2026-10-15', '2026-10-12')).toBeNull()
    // A day that does not exist, and a date that is not `YYYY-MM-DD`: both are
    // refused rather than rolled into the next month by `Date`.
    expect(dayRange('2026-02-31', '2026-03-02')).toBeNull()
    expect(dayRange('2026-1-2', '2026-01-03')).toBeNull()
    expect(dayRange('2026-10-12T00:00:00Z', '2026-10-15')).toBeNull()
  })

  it('stops at reportMaxDays, exactly', () => {
    // Without the bound, a corrupt `since` of `0001-01-01` would have the
    // console build two million strings before drawing anything. The bound is
    // the CLI's own, and it is inclusive: a range of exactly `reportMaxDays`
    // days is one `report` answers.
    const first = '2026-01-01'
    const atTheBound = plusDays(first, SPEND_MAX_RANGE_DAYS - 1)
    const onePast = plusDays(first, SPEND_MAX_RANGE_DAYS)
    expect(dayRange(first, atTheBound)).toHaveLength(SPEND_MAX_RANGE_DAYS)
    expect(dayRange(first, onePast)).toBeNull()
    expect(dayRange('0001-01-01', '2026-10-15')).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// The three accounts.
// ---------------------------------------------------------------------------

describe('the three accounts', () => {
  it('draws the billed series from the document\'s model dollars, never from model + notional', () => {
    // The plan's first named test for this screen, asserted on the series
    // model -- the figures the chart is drawn from. `summed` is the mutation: a
    // model that added the two accounts would produce that number and fail
    // here, with both figures in the diff.
    const model = modelOf()
    const billed = seriesOf(model, 'model')
    for (const row of DAY_ROWS) {
      const point = pointFor(billed, row.key)
      expect(point.reading.state).toBe('measured')
      expect(point.reading.value).toBe(row.model_micros / DOLLARS)
      expect(point.reading.value).not.toBe(summed(row.model_micros, row.notional_micros))
    }
  })

  it('keeps the notional account in its own series, marked, and never in the billed one', () => {
    const model = modelOf()
    const notional = seriesOf(model, 'notional')
    const first = pointFor(notional, '2026-10-12')
    expect(first.reading.state).toBe('notional')
    expect(first.reading.value).toBe(400_000 / DOLLARS)
    // The mutation again, from the other side: the billed series at that day is
    // the billed figure, which is not this.
    expect(pointFor(seriesOf(model, 'model'), '2026-10-12').reading.value).not.toBe(first.reading.value)
    // And the chart's one axis top is the largest value any account draws, in
    // that account's own state: the fixture's largest is the 12th's billed
    // $1.25, which is a figure the document carries and never the same day's
    // notional $0.40 added to it.
    expect(model.chart.axis.state).toBe('measured')
    expect(model.chart.axis.value).toBe(1_250_000 / DOLLARS)
    expect(model.chart.axis.value).not.toBe(summed(1_250_000, 400_000))
  })

  it('draws the document\'s own totals line, it does not add the rows up', () => {
    // A printout of three accounts is exactly where a screen is tempted to
    // recompute. It must not: the CLI's totals are the sum *by its own rules*
    // (a day with both billed and notional splits a model's dollars), and a
    // second opinion here would disagree with the terminal about the same day.
    // The fixture makes the difference visible by disagreeing on purpose.
    const model = modelOf({ ...DAY_REPORT, totals: { ...DAY_TOTALS, model_micros: 99_000_000 } })
    expect(model.totals.model.value).toBe(99)
    expect(model.totals.model.value).not.toBe(DAY_TOTALS.model_micros / DOLLARS)
    // The three totals are three readings in three states, and the billed one
    // is never the notional one added in.
    expect(model.totals.notional.state).toBe('notional')
    expect(model.totals.compute.state).toBe('estimated')
    expect(model.totals.model.value).not.toBe(summed(DAY_TOTALS.model_micros, DAY_TOTALS.notional_micros))
  })

  it('is n/a, not zero, for an account the document does not carry', () => {
    const model = modelOf()
    const second = pointFor(seriesOf(model, 'notional'), '2026-10-13')
    expect(second.reading.state).toBe('unavailable')
    expect(second.reading.value).toBeNull()
    // The share of a bar with no figure is a gap, never a zero-height bar: a
    // bar of no height reads as "this day spent nothing".
    expect(barFor(model, '2026-10-13', 'notional').share.state).toBe('gap')

    const compute = pointFor(seriesOf(model, 'compute'), '2026-10-13')
    expect(compute.reading.state).toBe('unavailable')
    expect(compute.reading.value).toBeNull()
  })

  it('keeps compute in the estimated state wherever the document carries it', () => {
    // `compute_estimated` is true whenever `compute_micros` is set
    // (`ee/report/render.go`'s `jsonLine`), and an estimate drawn as a
    // measurement is the exact error the `est.` mark exists to prevent.
    const compute = pointFor(seriesOf(modelOf(), 'compute'), '2026-10-12')
    expect(compute.reading.state).toBe('estimated')
    expect(compute.reading.value).toBe(120_000 / DOLLARS)
  })
})

// ---------------------------------------------------------------------------
// Partial days and gaps.
// ---------------------------------------------------------------------------

describe('a partial day', () => {
  it('is hollow, in the series and in the row', () => {
    const model = modelOf()
    const point = pointFor(seriesOf(model, 'model'), '2026-10-15')
    expect(point.partial).toBe(true)
    expect(point.hollow).toBe(true)
    // Hollow is not empty: the figure is real, it just is not final.
    expect(point.reading.value).toBe(300_000 / DOLLARS)
    expect(rowFor(model, '2026-10-15').hollow).toBe(true)
  })

  it('is not a property of a day that is final', () => {
    const model = modelOf()
    const point = pointFor(seriesOf(model, 'model'), '2026-10-12')
    expect(point.partial).toBe(false)
    expect(point.hollow).toBe(false)
    expect(rowFor(model, '2026-10-12').hollow).toBe(false)
  })

  it('carries the CLI\'s own marker, and the document\'s own partial flag on the totals', () => {
    // `report`'s table appends `(partial)` to the row key, and the totals row
    // is partial when any row is: the console says the same word about the same
    // fact rather than inventing a second one.
    const model = modelOf()
    expect(model.totals.partial).toBe(true)
    expect(PARTIAL_MARKER).toBe('(partial)')
  })
})

describe('a day the history does not hold', () => {
  it('is a gap that breaks the series, not a zero', () => {
    const model = modelOf()
    const billed = seriesOf(model, 'model')
    expect(billed.points.map((point) => point.key)).toEqual([
      '2026-10-12',
      '2026-10-13',
      '2026-10-14',
      '2026-10-15',
    ])
    const missing = pointFor(billed, '2026-10-14')
    expect(missing.reading.state).toBe('gap')
    expect(missing.reading.value).toBeNull()
    expect(missing.reading.because).toContain('no document')
    // A bar drawn through the missing day would assert, in ink, a measurement
    // nobody made: every account's bar there is a gap, and the drawing leaves
    // all three out in favour of the day's one hatched column.
    for (const account of SPEND_ACCOUNTS) {
      expect(barFor(model, '2026-10-14', account).share.state).toBe('gap')
    }
  })

  it('breaks every account\'s series, and says so in the console\'s own words', () => {
    const model = modelOf()
    // The 13th's notional and compute values are `n/a` and the 14th is a gap,
    // so neither account has a bar on either day.
    expect(barFor(model, '2026-10-13', 'notional').share.value).toBeNull()
    expect(barFor(model, '2026-10-13', 'compute').share.value).toBeNull()
    expect(barFor(model, '2026-10-14', 'compute').share.state).toBe('gap')
    expect(model.notes.join(' ')).toContain('a gap, not a zero')
  })

  it('breaks the chart on the day the history does not hold, in the drawn tree', () => {
    // The chart holds one group per day of the range and the missing day is one
    // hatched column of its own: `spend.spec.ts` reads the same list back out of
    // the window, and the fact is held here as well.
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(all(tree, 'spend-gap-column').map((column) => column.getAttribute('data-key'))).toEqual([
      '2026-10-14',
    ])
    expect(all(tree, 'spend-chart-key').map((key) => key.getAttribute('data-gap'))).toEqual([
      'false',
      'false',
      'true',
      'false',
    ])
  })

  it('says nothing about gaps when every day of the range has a row', () => {
    // The other side of the boundary: a range whose days are all in the history
    // is no gap and no legend. A console that drew the legend
    // whenever the rows were fewer than the days -- rather than whenever a day
    // was actually missing -- would explain a gap it did not draw.
    const fourth = { ...DAY_ROWS[0], key: '2026-10-14', start: '2026-10-14', end: '2026-10-14' }
    const model = buildSpendModel({ ...DAY_REPORT, rows: [...DAY_ROWS, fourth] }, BUDGET)
    expect(model.notes).toEqual([])
    const billed = seriesOf(model, 'model')
    expect(billed.points.map((point) => point.key)).toEqual([
      '2026-10-12',
      '2026-10-13',
      '2026-10-14',
      '2026-10-15',
    ])
    expect(billed.points.every((point) => point.reading.state === 'measured')).toBe(true)
  })

  describe('a row dated outside the range', () => {
    const OUTSIDE_NOTE = 'row(s) outside the range'
    const before = { ...DAY_ROWS[0], key: '2026-10-01', start: '2026-10-01', end: '2026-10-01' }
    const after = { ...DAY_ROWS[0], key: '2026-10-20', start: '2026-10-20', end: '2026-10-20' }

    it('is not plotted on the day axis, and the axis stays the range', () => {
      // Position on a time series is the date: a row before `since` appended
      // after the last day would sit to the right of `until`.
      for (const stray of [before, after]) {
        const model = modelOf({ ...DAY_REPORT, rows: [...DAY_ROWS, stray] })
        for (const account of ['model', 'notional', 'compute'] as const) {
          expect(seriesOf(model, account).points.map((point) => point.key)).toEqual([
            '2026-10-12',
            '2026-10-13',
            '2026-10-14',
            '2026-10-15',
          ])
        }
      }
    })

    it('is said out loud, with a count and no wire text, and the row stays in the list', () => {
      const model = modelOf({ ...DAY_REPORT, rows: [before, ...DAY_ROWS, after] })
      expect(model.notes.some((note) => note.startsWith('2 ') && note.includes(OUTSIDE_NOTE))).toBe(true)
      expect(model.notes.join(' ')).not.toContain('2026-10-01')
      expect(drawnRows(model).map((row) => row.key)).toContain('2026-10-01')
      const quiet = modelOf()
      expect(quiet.notes.join(' ')).not.toContain(OUTSIDE_NOTE)
    })

    it('does not pass for a gap: the missing day stays a gap', () => {
      const model = modelOf({ ...DAY_REPORT, rows: [...DAY_ROWS, before] })
      expect(pointFor(seriesOf(model, 'model'), '2026-10-14').reading.state).toBe('gap')
    })

    it('is drawn once for a repeated date: the later row wins, and no extra point appears', () => {
      const later = { ...DAY_ROWS[0], key: '2026-10-12', start: '2026-10-12', end: '2026-10-12' }
      const model = modelOf({ ...DAY_REPORT, rows: [...DAY_ROWS, later] })
      const points = seriesOf(model, 'model').points
      expect(points.filter((point) => point.key === '2026-10-12')).toHaveLength(1)
      expect(points).toHaveLength(4)
      expect(model.notes.join(' ')).not.toContain(OUTSIDE_NOTE)
    })
  })

  it('is only a gap inside the range the document covers', () => {
    // A range the console cannot enumerate -- `by model`, and any `by` whose
    // `since`/`until` it could not read -- has the document's own rows as its
    // points and no invented ones. The console does not know which days such a
    // report covers, so it does not pretend a day is missing.
    const model = buildSpendModel(MODEL_REPORT, BUDGET)
    const billed = seriesOf(model, 'model')
    expect(billed.points.map((point) => point.key)).toEqual(['claude-sonnet-5', 'claude-opus-5'])
    expect(billed.points.every((point) => point.reading.state === 'measured')).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// The person breakdown, off by default.
// ---------------------------------------------------------------------------

describe('the person breakdown', () => {
  it('is off by default: the rows are in the document and the screen draws none of them', () => {
    const model = buildSpendModel(PERSON_REPORT, BUDGET)
    expect(model.persons).toEqual({ enabled: false, withheld: true })
    expect(drawnRows(model)).toEqual([])
    // No figure at all, on the one axis the chart would have drawn: a scale of
    // nothing is `n/a` with the console's reason, never a `$0.00`.
    expect(model.chart.axis.value).toBeNull()
    for (const series of model.series) {
      expect(series.points).toEqual([])
      expect(series.empty).toContain('person breakdown is off')
    }
    // One sentence, and it is the reason: a view that is off without a note is
    // a view the operator will look for and not find.
    expect(model.notes).toHaveLength(1)
    expect(model.notes[0]).toContain('person breakdown is off')
    // The switch says so too, in the shape the screen draws: off, and current.
    const entry = model.switches.find((one) => one.dimension === 'person')
    expect(entry).toMatchObject({ enabled: false, current: true })
    expect(entry?.note).toContain('person breakdown is off')
  })

  it('draws no person and no address while it is off', () => {
    const tree = renderSpend(factory, buildSpendModel(PERSON_REPORT, BUDGET), 'light')
    const text = drawn(tree)
    expect(text).toContain('person breakdown is off')
    for (const row of PERSON_ROWS) expect(text).not.toContain(row.key)
    expect(all(tree, 'spend-row')).toEqual([])
    // The switch is drawn, and it is the one word that is not enabled.
    expect(
      all(tree, 'spend-dim')
        .filter((dim) => dim.getAttribute('data-enabled') === 'false')
        .map((dim) => dim.textContent),
    ).toEqual(['person'])
  })

  it('shows the rows when it is asked to, and draws an address as the console\'s marker', () => {
    const model = buildSpendModel(PERSON_REPORT, BUDGET, { persons: true })
    expect(model.persons).toEqual({ enabled: true, withheld: false })
    expect(drawnRows(model).map((row) => row.key)).toEqual(['ada@example.com', 'unknown'])
    expect(model.notes).toEqual([])

    const tree = renderSpend(factory, model, 'light')
    const text = drawn(tree)
    // The person class: a whole address is the console's marker, and the login
    // `unknown` -- the CLI's own bucket for runs with no requester -- is drawn
    // as itself.
    expect(text).toContain('redacted@example.invalid')
    expect(text).toContain('unknown')
    expect(text).not.toContain('ada@example.com')
    expect(text).not.toContain('ada')
    expect(keyClass('person', 'ada@example.com')).toBe('person')
  })
})

// ---------------------------------------------------------------------------
// The by-mode breakdown (`report --by mode`).
// ---------------------------------------------------------------------------

describe('the by-mode breakdown', () => {
  it('is a word the console knows, so a by-mode document is drawn and not read as unknown', () => {
    // Red before `mode` was in `SPEND_DIMENSIONS`: `dimensionOf('mode')` was
    // null, so the screen took the unknown-breakdown path -- the switch drew
    // "an unknown breakdown" and the note said it could not name the grouping.
    const model = buildSpendModel(MODE_REPORT, BUDGET)
    expect(model.dimensionKnown).toBe(true)
    expect(model.dimension).toBe('mode')
    expect(model.notes).toEqual([])
    expect(drawnRows(model).map((row) => row.key)).toEqual(['pro', 'none'])
    // The switch offers every word, and `mode` is the one the document asked for.
    expect(model.switches.map((one) => one.dimension)).toEqual([...SPEND_DIMENSIONS])
    expect(model.switches.find((one) => one.dimension === 'mode')).toMatchObject({
      enabled: true,
      current: true,
    })
  })

  it('draws a mode and the CLI\'s own `none` as labels, never as an error', () => {
    const tree = renderSpend(factory, buildSpendModel(MODE_REPORT, BUDGET), 'light')
    // A mode word and `none` take the free-text class, exactly as a model name
    // does -- so `none` is its own row and not the switch's unknown word.
    expect(keyClass('mode', 'pro')).toBe('label')
    expect(keyClass('mode', 'none')).toBe('label')
    // Each key is drawn twice: once under the chart's group and once in the
    // table's row.
    expect(texts(tree, 'spend-key')).toEqual(['pro', 'none', 'pro', 'none'])
    expect(all(tree, 'spend-dim-unknown')).toEqual([])
    expect(drawn(tree)).not.toContain('an unknown breakdown')
  })

  it('keeps the money grammar: the three accounts stay apart and are never summed', () => {
    const model = buildSpendModel(MODE_REPORT, BUDGET)
    const tree = renderSpend(factory, model, 'light')
    // The notional row is marked and the row with no estimated run reads `n/a`;
    // no cell is model + notional or model + compute.
    expect(texts(tree, 'spend-notional')).toEqual([UNAVAILABLE_MARK, `${NOTIONAL_MARK}$3.00`])
    expect(texts(tree, 'spend-compute')).toEqual(['est. $0.25', UNAVAILABLE_MARK])
    expect(drawn(tree)).not.toContain('$3.50')
    expect(drawn(tree)).not.toContain('$2.25')
    // A mode row carries no run-hours, so the column is `n/a` and never `0.00 h`.
    expect(texts(tree, 'spend-run-hours')).toEqual([UNAVAILABLE_MARK, UNAVAILABLE_MARK])
  })

  it('says `no readable record`, the fact a corrupt record and a run in flight share', () => {
    // The words are the console's own -- `report`'s table and CSV draw `n/a`
    // on such a row and name no mark -- and they name the fact a corrupt
    // record and a run that has not written one share, rather than
    // "unreadable", which misnames the in-flight run: the usual `none` row of
    // a window that reaches into today.
    expect(UNREADABLE_MARKER).toBe('no readable record')
  })

  it('reads an unreadable row as `n/a`, not as a measured zero', () => {
    // Red before `rowOf` read `unreadable`: the row was kept, but nothing said
    // why its money was `n/a` rather than `$0.00`, and a reader could not tell
    // a run whose record the build cannot read from a run that spent nothing.
    const model = buildSpendModel(MODE_UNREADABLE_REPORT, BUDGET)
    expect(model.dimensionKnown).toBe(true)
    expect(model.notes).toEqual([])
    // Both rows survive the read: the unreadable one is a row of the document,
    // and dropping it would hide the run the CLI counted.
    expect(drawnRows(model).map((row) => row.key)).toEqual(['pro', 'none'])

    const none = rowFor(model, 'none')
    expect(none.unreadable).toBe(true)
    // A null money key is *unavailable* -- the reading, not a zero -- and its
    // reason is the row's own fact, not "the document has no model_micros": the
    // key is present and null on purpose, and there was no record to read.
    for (const reading of [none.model, none.notional, none.compute]) {
      expect(reading.state).toBe('unavailable')
      expect(reading.value).toBeNull()
      expect(reading.because).toBe('no readable record for these runs')
    }
    // The run itself is counted: the record could not be read, but the run did
    // launch, and the CLI counts it once.
    expect(none.runs.value).toBe(1)
    expect(none.runs.state).toBe('measured')

    const pro = rowFor(model, 'pro')
    expect(pro.unreadable).toBe(false)
    expect(pro.model).toMatchObject({ state: 'measured', value: 1.5 })
    expect(pro.notional).toMatchObject({ state: 'notional', value: 0 })
    expect(pro.compute).toMatchObject({ state: 'estimated', value: 0.25 })
    // A mode row carries no run-hours, which is its own `n/a` and unrelated to
    // the mark.
    expect(pro.runHours.state).toBe('unavailable')
    expect(pro.runs.value).toBe(1)

    // The totals are the document's own row and are never recomputed from the
    // rows: a sentinel proves the screen reads them.
    expect(model.totals.model.value).toBe(1.5)
    const sentinel = buildSpendModel(
      { ...MODE_UNREADABLE_REPORT, totals: { ...MODE_UNREADABLE_TOTALS, model_micros: 9_000_000 } },
      BUDGET,
    )
    expect(sentinel.totals.model.value).toBe(9)
    expect(sentinel.totals.model.value).not.toBe(1.5)

    // The row line says which fact the `n/a`s are, beside them.
    const tree = renderSpend(factory, model, 'light')
    const rows = all(tree, 'spend-row')
    const unreadableRow = rows.find((row) => row.getAttribute('data-key') === 'none')
    const ordinaryRow = rows.find((row) => row.getAttribute('data-key') === 'pro')
    if (unreadableRow === undefined || ordinaryRow === undefined) throw new Error('a row is missing')
    expect(texts(unreadableRow, 'spend-unreadable')).toEqual([UNREADABLE_MARKER])
    expect(texts(unreadableRow, 'spend-model')).toEqual([UNAVAILABLE_MARK])
    expect(texts(unreadableRow, 'spend-notional')).toEqual([UNAVAILABLE_MARK])
    expect(texts(unreadableRow, 'spend-compute')).toEqual([UNAVAILABLE_MARK])
    // The `n/a`'s own explanation is the row's, in the drawn cell's `title`.
    expect(one(unreadableRow, 'spend-model').getAttribute('title')).toBe('no readable record for these runs')
    expect(drawn(unreadableRow)).not.toContain('$0.00')
    // The hook is a fact a stylesheet keys off (design §2.4), so it is read
    // back rather than left to the class scan, which reads CSS and not the
    // renderer: an attribute silently dropped would otherwise pass every test.
    expect(unreadableRow.getAttribute('data-unreadable')).toBe('true')
    expect(ordinaryRow.getAttribute('data-unreadable')).toBe('false')
    expect(texts(ordinaryRow, 'spend-unreadable')).toEqual([])
    expect(texts(ordinaryRow, 'spend-model')).toEqual(['$1.50'])
    // The mixed document's total is a measurement, and says so.
    expect(one(tree, 'spend-totals').getAttribute('data-unreadable')).toBe('false')
    expect(texts(one(tree, 'spend-totals'), 'spend-unreadable')).toEqual([])
  })

  it('draws an all-unreadable window\'s total as `n/a` too, never as $0.00', () => {
    // Red before `totalsOf` read `unreadable`: a window in which every run was
    // unreadable drew a TOTAL of three `$0.00`-looking `n/a`s with no sentence
    // saying why, under rows that all carried one, and the false title "the
    // document has no model_micros" for a key that is present and null.
    const model = buildSpendModel(MODE_ALL_UNREADABLE_REPORT, BUDGET)
    expect(drawnRows(model).map((row) => row.key)).toEqual(['none'])
    expect(rowFor(model, 'none').unreadable).toBe(true)
    expect(model.totals.unreadable).toBe(true)
    // The total counted both runs and measured nothing.
    expect(model.totals.runs.value).toBe(2)
    for (const reading of [model.totals.model, model.totals.notional, model.totals.compute]) {
      expect(reading.state).toBe('unavailable')
      expect(reading.value).toBeNull()
      expect(reading.because).toBe('no readable record for these runs')
    }

    const tree = renderSpend(factory, model, 'light')
    const totals = one(tree, 'spend-totals')
    expect(totals.getAttribute('data-unreadable')).toBe('true')
    expect(texts(totals, 'spend-unreadable')).toEqual([UNREADABLE_MARKER])
    expect(texts(totals, 'spend-total-model')).toEqual([UNAVAILABLE_MARK])
    expect(texts(totals, 'spend-total-notional')).toEqual([UNAVAILABLE_MARK])
    expect(texts(totals, 'spend-total-compute')).toEqual([UNAVAILABLE_MARK])
    expect(one(totals, 'spend-total-model').getAttribute('title')).toBe('no readable record for these runs')
    expect(texts(totals, 'spend-total-runs')).toEqual(['2'])
    expect(drawn(totals)).not.toContain('$0.00')
  })

  it('ignores a per-row key it does not know, so a newer document is not a refusal', () => {
    // The contract grows a row key from one CLI release to the next, and the
    // console reads the keys it knows -- `rowOf` and `totalsOf` reach for each
    // key by name, so one they never name is simply not read. An unknown extra
    // key is therefore not a version mismatch and not a refusal, on a readable
    // row, on the unreadable one, or on the totals line.
    const report: JSONDoc = {
      ...MODE_UNREADABLE_REPORT,
      rows: MODE_UNREADABLE_ROWS.map((row) => ({ ...row, something_new: 'from a newer CLI' })),
      totals: { ...MODE_UNREADABLE_TOTALS, something_new: 'from a newer CLI' },
    }
    const model = buildSpendModel(report, BUDGET)
    expect(model.dimensionKnown).toBe(true)
    expect(model.notes).toEqual([])
    expect(drawnRows(model).map((row) => row.key)).toEqual(['pro', 'none'])
    expect(rowFor(model, 'pro').model.value).toBe(1.5)
    // The unknown key does not disturb the unreadable read either.
    expect(rowFor(model, 'none').unreadable).toBe(true)
    expect(model.totals.model.value).toBe(1.5)
  })
})

// ---------------------------------------------------------------------------
// The by-person view (console L5b).
// ---------------------------------------------------------------------------

/** peopleOf is one model's by-person view, asserted to be there: the panel
 * exists exactly when the operator's knob is on (design D15). */
function peopleOf(model: SpendModel): SpendPeople {
  const people = model.people
  if (people === null) throw new Error('this model carries no by-person view')
  return people
}

describe('the by-person view', () => {
  it('is absent, with no read behind it, while the person view is off', () => {
    // The mutation this is written against: a panel that is always built, and a
    // by-person read that happens whatever the knob says. With the knob off
    // there is no panel at all -- not an empty one -- because there was no read
    // to draw from and nothing the operator asked to see.
    const off = buildSpendModel(PERSON_REPORT, BUDGET)
    expect(off.people).toBeNull()
    expect(all(renderSpend(factory, off, 'light'), 'spend-people')).toEqual([])
    // The document being handed in does not change that: the knob decides.
    const handed = buildSpendModel(PERSON_REPORT, BUDGET, { people: statsDocument() })
    expect(handed.people).toBeNull()
  })

  it('draws the view the CLI resolved: every row, the identity, the operator line and the window', () => {
    const model = buildSpendModel(PERSON_REPORT, BUDGET, { persons: true, people: statsDocument() })
    const people = peopleOf(model)
    expect(people.available).toBe(true)
    expect(people.view).toBe('all')
    expect(people.viewSource).toBe('all')
    expect(people.viewName).toBe('everyone in this project')
    expect(people.failPrivate).toBe(false)
    expect(people.operator).toBe(true)
    expect(people.you).toBe('ada@example.com')
    expect(people.withheld).toBe(0)
    expect(people.rows.map((row) => row.user)).toEqual([
      'ada@example.com',
      'bob@example.com',
      'unknown',
    ])
    // The document's own totals are read as their own rows, and never summed
    // here: the total is the CLI's sum of the rows this screen also drew.
    expect(people.totals).toHaveLength(1)
    expect(people.totals[0]?.counted.value).toBe(1.75)
    expect(people.totals[0]?.counted.state).toBe('measured')
    expect(people.since).toBe('2026-10-12')
    expect(people.until).toBe('2026-10-15')
    expect(people.maxRuns.value).toBe(5000)

    const tree = renderSpend(factory, model, 'light')
    const panel = one(tree, 'spend-people')
    expect(panel.getAttribute('data-available')).toBe('true')
    expect(panel.getAttribute('data-view')).toBe('all')
    expect(panel.getAttribute('data-operator')).toBe('true')
    expect(texts(tree, 'spend-people-view')).toEqual(['everyone in this project'])
    expect(texts(tree, 'spend-people-you')).toEqual(['redacted@example.invalid'])
    expect(texts(tree, 'spend-people-operator')[0]).toContain('place you as an operator')
    expect(texts(tree, 'spend-people-max-runs')).toEqual([formatReading(people.maxRuns, 'count')])
    // Three rows and the total line. The total is the console's own word,
    // because the document's own total carries an empty user.
    expect(all(tree, 'spend-person')).toHaveLength(4)
    expect(one(tree, 'spend-person-total').getAttribute('data-user')).toBeNull()
    expect(texts(tree, 'spend-person-user')).toEqual([
      'redacted@example.invalid',
      'redacted@example.invalid',
      'unknown',
      'total',
    ])
    expect(all(tree, 'spend-person-total')).toHaveLength(1)
    // The document's own note is drawn, and it is the *document's*: it reaches
    // the screen through `putText` and not as a literal.
    expect(texts(tree, 'spend-person-note')).toEqual([
      'these are every launcher of this project in the window',
    ])
  })

  it('draws a person key through the person class, and the attribute carries what was drawn', () => {
    // An identity is untrusted display text -- it is whatever a `launch.json`
    // recorded -- so an address is the console's own marker, in the cell and in
    // the attribute alike: `data-user` is read back off the drawn cell, so a
    // hostile key cannot reach the screen through the attribute.
    const model = buildSpendModel(PERSON_REPORT, BUDGET, { persons: true, people: statsDocument() })
    const tree = renderSpend(factory, model, 'light')
    const [first] = all(tree, 'spend-person')
    expect(first?.getAttribute('data-user')).toBe('redacted@example.invalid')
    expect(first?.getAttribute('data-currency')).toBe('USD')
    expect(drawn(tree)).toContain('redacted@example.invalid')
    expect(drawn(tree)).not.toContain('ada@example.com')
    expect(drawn(tree)).not.toContain('bob@example.com')
    // The identity the view was resolved for is that same untrusted text, and
    // it is drawn the same way -- through the same class, and nowhere raw.
    expect(drawn(tree)).not.toContain('ada')
  })

  it('draws one row for a private view, and says the total under it is that person’s own', () => {
    const model = buildSpendModel(PERSON_REPORT, BUDGET, {
      persons: true,
      people: statsOwnDocument('bob@example.com'),
    })
    const people = peopleOf(model)
    expect(people.view).toBe('own')
    expect(people.viewName).toBe('you alone')
    expect(people.operator).toBe(false)
    expect(people.rows.map((row) => row.user)).toEqual(['bob@example.com'])
    expect(people.withheld).toBe(0)
    expect(people.sentences.join(' ')).toContain('your own runs alone')
    // The total the CLI published for this view is the caller's own, because the
    // CLI aggregates after it filters -- so the panel says whose money that line
    // is rather than letting it read as the window's.
    expect(people.totals).toHaveLength(1)
    expect(people.totals[0]?.counted.value).toBe(0.25)
    expect(people.sentences.join(' ')).toContain('not the window’s total')

    const tree = renderSpend(factory, model, 'light')
    // One row and the total.
    expect(all(tree, 'spend-person')).toHaveLength(2)
    expect(texts(tree, 'spend-person-user')).toEqual(['redacted@example.invalid', 'total'])
    expect(texts(tree, 'spend-people-operator')[0]).toContain('do not place you as an operator')
    expect(drawn(tree)).not.toContain('ada@example.com')
  })

  it('still counts the rows of an own view a document should not have carried', () => {
    // A document the CLI does not write: `runs stats` never publishes another
    // launcher's row in a private view (it deletes them before aggregating), so
    // this document is a stranger's. The screen is written against the shape and
    // not against the CLI's good behaviour, so it draws the caller's row alone
    // and says how many it did not draw.
    const model = buildSpendModel(PERSON_REPORT, BUDGET, {
      persons: true,
      people: statsDefensiveDocument({ view: 'own', you: 'bob@example.com' }),
    })
    const people = peopleOf(model)
    expect(people.rows.map((row) => row.user)).toEqual(['bob@example.com'])
    expect(people.withheld).toBe(2)
    expect(people.sentences.join(' ')).toContain('2 row(s) of this document are not drawn')
    expect(people.sentences.join(' ')).toContain('other people’s rows are withheld')
    expect(drawn(renderSpend(factory, model, 'light'))).not.toContain('ada@example.com')
  })

  it('fails private: with no identity to key the view by, it draws neither a row nor a total', () => {
    // The two documents here are the ones the CLI really publishes for these
    // states, and neither carries a total: `runs_stats.go` deletes every run
    // before `userstats.Aggregate` can sum the rows, so `rows` and `totals` come
    // back `[]` together (`statsOwnDocument`). That is the whole reason the
    // panel may not promise a total beside its "no rows" sentence: there is no
    // figure to promise, and a panel that drew the identity sentence and then
    // "the document carries no totals line" would contradict itself.
    for (const [you, sentence] of [
      ['', 'did not say who this read was resolved for'],
      ['unknown', 'unattributed key itself'],
    ] as const) {
      const model = buildSpendModel(PERSON_REPORT, BUDGET, {
        persons: true,
        people: statsOwnDocument(you),
      })
      const people = peopleOf(model)
      expect(people.rows, you).toEqual([])
      expect(people.totals, you).toEqual([])
      expect(people.failPrivate, you).toBe(true)
      expect(people.withheld, you).toBe(0)
      expect(people.sentences.join(' '), you).toContain(sentence)
      // The fail-private sentence promises no figure the document does not
      // carry: "only the totals" is not something this state can say.
      expect(people.sentences.join(' '), you).not.toContain('only the totals')

      const tree = renderSpend(factory, model, 'light')
      expect(all(tree, 'spend-person'), you).toEqual([])
      expect(all(tree, 'spend-person-total'), you).toEqual([])
      expect(one(tree, 'spend-people-rows').childNodes(), you).toEqual([])
      // No total was published, so the panel says that and nothing else about
      // one: exactly one sentence about the missing line, its own console words.
      const notes = texts(tree, 'spend-person-note')
      expect(notes.filter((note) => note?.includes('no totals line')), you).toHaveLength(1)
      expect(notes.join(' '), you).not.toContain('only the totals')
      // The identity cell says why it is not an identity, in the console's own
      // words: an empty cell would read as anonymous.
      // `''` is drawn as the console's sentence for an identity the CLI could
      // not resolve; the unattributed key is drawn as itself, because it is a
      // key the CLI published and not a person -- which is exactly why it draws
      // no row.
      expect(texts(tree, 'spend-people-you'), you).toEqual([
        you === '' ? 'the CLI could not say who this read was resolved for' : 'unknown',
      ])
      // No identity of the by-person document is drawn. (`unknown` is not
      // asserted on: it is the *report*'s own key for its unattributed row,
      // which this document legitimately draws when the person breakdown is
      // on -- the by-person panel's rows are the ones asserted empty above.)
      for (const row of STATS_ROWS.filter((one) => one.user.includes('@'))) {
        expect(drawn(tree), row.user).not.toContain(row.user)
      }
    }
  })

  it('fails private under the whole-project view too: with no identity it draws the totals and no row', () => {
    // Design section 2.4 (console-styling.md) and section 6.2 (desktop-console.md):
    // a by-person read with `data-view="all"` that does not say who it was
    // resolved for draws the document's totals and **no row at all**. The CLI
    // does write this document -- `--all-users` is admitted with no identity
    // (`runs_stats.go` refuses only the private view without one) -- so this is
    // the wire, not a defence. Both identity states the CLI cannot key a person
    // by are asserted: `''` (unavailable) and the unattributed key itself.
    for (const you of ['', 'unknown'] as const) {
      const model = buildSpendModel(PERSON_REPORT, BUDGET, {
        persons: true,
        people: statsDocument({ you }),
      })
      const people = peopleOf(model)
      expect(people.view, you).toBe('all')
      expect(people.rows, you).toEqual([])
      expect(people.failPrivate, you).toBe(true)
      expect(people.withheld, you).toBe(STATS_ROWS.length)
      // The totals are the document's own and are still drawn: they name nobody.
      expect(people.totals, you).toHaveLength(1)
      expect(people.sentences.join(' '), you).toContain(
        'did not say who this read was resolved for',
      )
      expect(people.sentences.join(' '), you).not.toContain('every launcher')

      const tree = renderSpend(factory, model, 'light')
      expect(one(tree, 'spend-people').getAttribute('data-view'), you).toBe('all')
      // No row inside the rows list -- read off the list's own children, so the
      // assertion cannot pass on a selector that matches nothing.
      expect(one(tree, 'spend-people-rows').childNodes(), you).toEqual([])
      // The one `spend-person` line left is the total, and it is the total.
      const lines = all(tree, 'spend-person')
      expect(lines, you).toHaveLength(1)
      expect(lines[0]?.classes, you).toContain('spend-person-total')
      expect(texts(tree, 'spend-person-user'), you).toEqual(['total'])
      for (const row of STATS_ROWS.filter((line) => line.user.includes('@'))) {
        expect(drawn(tree), row.user).not.toContain(row.user)
      }
    }
  })

  it('draws everyone for an operator and the caller alone for a plain launcher, by the CLI’s view', () => {
    // The two licensed states side by side, from the documents the CLI writes:
    // an operator (`view: all`, identified) sees every launcher's row; a plain
    // launcher (`view: own`, the default the CLI resolves) sees their own row
    // alone, even though the window holds other people's runs.
    const operator = renderSpend(
      factory,
      buildSpendModel(PERSON_REPORT, BUDGET, { persons: true, people: statsDocument() }),
      'light',
    )
    const operatorRows = one(operator, 'spend-people-rows').childNodes()
    expect(operatorRows).toHaveLength(STATS_ROWS.length)
    expect(one(operator, 'spend-people').getAttribute('data-operator')).toBe('true')

    const launcher = renderSpend(
      factory,
      buildSpendModel(PERSON_REPORT, BUDGET, {
        persons: true,
        people: statsOwnDocument('bob@example.com'),
      }),
      'light',
    )
    const launcherRows = one(launcher, 'spend-people-rows').childNodes() as readonly FakeScreen[]
    expect(launcherRows).toHaveLength(1)
    expect(launcherRows[0]?.classes).toContain('spend-person')
    expect(launcherRows[0]?.classes).not.toContain('spend-person-total')
    expect(one(launcher, 'spend-people').getAttribute('data-view')).toBe('own')
    expect(one(launcher, 'spend-people').getAttribute('data-operator')).toBe('false')
  })

  it('counts the rows of a private view a document should not have carried, and keeps no claim about totals', () => {
    // A document the CLI does not write, on purpose: an own view that still
    // carries other people's rows *and* a whole-window total. The rows are
    // withheld by the same rule and counted; the document's totals are drawn as
    // it published them, and the panel says which read they are the sum of.
    const model = buildSpendModel(PERSON_REPORT, BUDGET, {
      persons: true,
      people: statsDefensiveDocument({ view: 'own', you: '' }),
    })
    const people = peopleOf(model)
    expect(people.rows).toEqual([])
    expect(people.totals).toHaveLength(1)
    expect(people.withheld).toBe(STATS_ROWS.length)
    expect(people.sentences.join(' ')).toContain('3 row(s) of this document are not drawn')
    const tree = renderSpend(factory, model, 'light')
    expect(all(tree, 'spend-person-total')).toHaveLength(1)
    expect(all(tree, 'spend-person')).toHaveLength(1)
  })

  it('draws no row for a view or a breakdown this build does not know, and echoes neither word', () => {
    // The value is never drawn -- not in a cell, not in an attribute: the
    // panel's own closed words stand in for it, the rule the switch's eighth
    // breakdown and the Live screen's unknown flags follow. Both documents here
    // are ones the CLI does not write (its `view` is `own` or `all`, its `by` is
    // `user`), so they pin the defence rather than the wire.
    const unknownView = buildSpendModel(PERSON_REPORT, BUDGET, {
      persons: true,
      people: statsDefensiveDocument({ view: 'the-whole-company' }),
    })
    const view = peopleOf(unknownView)
    expect(view.view).toBe('unknown')
    expect(view.viewName).toBe('a view this console does not know')
    expect(view.rows).toEqual([])
    expect(view.failPrivate).toBe(true)
    expect(view.totals).toHaveLength(1)
    expect(view.sentences.join(' ')).toContain('a view this console does not know')
    const drawnView = renderSpend(factory, unknownView, 'light')
    expect(one(drawnView, 'spend-people').getAttribute('data-view')).toBe('unknown')
    expect(all(drawnView, 'spend-person')).toHaveLength(1)
    expect(drawn(drawnView)).not.toContain('the-whole-company')

    const unknownBy = buildSpendModel(PERSON_REPORT, BUDGET, {
      persons: true,
      people: statsDefensiveDocument({ by: 'team' }),
    })
    const by = peopleOf(unknownBy)
    expect(by.rows).toEqual([])
    expect(by.withheld).toBe(STATS_ROWS.length)
    expect(by.sentences.join(' ')).toContain('a breakdown this console does not know')
    const drawnBy = renderSpend(factory, unknownBy, 'light')
    expect(drawn(drawnBy)).not.toContain('team')
    // The document published totals, so they are drawn -- as *its* totals: in a
    // state where the panel does not know who the rows were for, it claims
    // nothing about what they are the sum of.
    expect(all(drawnBy, 'spend-person-total')).toHaveLength(1)
    expect(texts(drawnBy, 'spend-person-user')).toEqual(['total'])
  })

  it('says the read was bounded, and draws no average where the document has no sample', () => {
    const model = buildSpendModel(PERSON_REPORT, BUDGET, {
      persons: true,
      people: statsDocument({ truncated: true }),
    })
    const people = peopleOf(model)
    expect(people.truncated).toBe(true)
    expect(people.sentences.join(' ')).toContain('more runs than the CLI read')
    // `unlaunched: 2`: runs that never started are the window's count and are in
    // no row, and the panel says so rather than letting them look like a gap in
    // the arithmetic.
    expect(people.unlaunched.value).toBe(2)
    expect(people.sentences.join(' ')).toContain('never started')
    // The sentence says the count is drawn, so it is: in its own cell, with its
    // own hook and the console's word beside it, after the totals.
    const counted = renderSpend(factory, model, 'light')
    expect(texts(counted, 'spend-people-unlaunched')).toEqual([
      formatReading(people.unlaunched, 'count'),
    ])
    expect(texts(counted, 'spend-people-unlaunched-word')).toEqual(['never started'])

    // The third row settled no real-dollar cost: `counted_runs` is 0, so the
    // average is *unavailable* and never `$0.00` -- which would be a figure
    // somebody could act on.
    const noSample = people.rows[2]
    expect(noSample?.countedRuns.value).toBe(0)
    expect(noSample?.average.state).toBe('unavailable')
    expect(noSample?.average.value).toBeNull()
    const tree = renderSpend(factory, model, 'light')
    const averages = texts(tree, 'spend-person-average')
    expect(averages[2]).toBe(UNAVAILABLE_MARK)
    expect(one(tree, 'spend-people').getAttribute('data-truncated')).toBe('true')
  })

  it('says why it has no document at all, rather than drawing an empty panel', () => {
    const none = buildSpendModel(PERSON_REPORT, BUDGET, { persons: true })
    const people = peopleOf(none)
    expect(people.available).toBe(false)
    expect(people.rows).toEqual([])
    expect(people.totals).toEqual([])
    expect(people.reason).toContain('made no by-person read')
    const tree = renderSpend(factory, none, 'light')
    expect(one(tree, 'spend-people').getAttribute('data-available')).toBe('false')
    expect(one(tree, 'spend-people-reason').textContent).toContain('made no by-person read')
    expect(all(tree, 'spend-person')).toEqual([])
    expect(all(tree, 'spend-people-rows')).toEqual([])

    // A refusal's sentence is drawn as the reason, sanitized by the caller
    // (`readSpend`'s `refusalSentence`): the panel draws what it is handed, and
    // the model never invents one.
    const refused = buildSpendModel(PERSON_REPORT, BUDGET, {
      persons: true,
      peopleReason: 'the CLI refused this read',
    })
    expect(one(renderSpend(factory, refused, 'light'), 'spend-people-reason').textContent).toBe(
      'the CLI refused this read',
    )
  })

  it('says a range with an end makes no by-person read, and draws that sentence in the panel it names', () => {
    // `PERSON_UNTIL` is the reason the panel is handed when the picker holds an
    // end of range: the panel *is* drawn -- `data-available="false"`, the
    // sentence as its reason -- so the sentence must not say it is not.
    const decision = peopleArguments({ since: '30d', until: '2026-10-11', by: 'day', persons: true })
    expect(decision.ok).toBe(false)
    const reason = decision.ok ? '' : decision.reason
    expect(reason).toContain('no by-person read is made')
    expect(reason).not.toContain('not drawn')
    const tree = renderSpend(
      factory,
      buildSpendModel(PERSON_REPORT, BUDGET, { persons: true, peopleReason: reason }),
      'light',
    )
    expect(one(tree, 'spend-people').getAttribute('data-available')).toBe('false')
    expect(texts(tree, 'spend-people-reason')).toEqual([reason])
  })

  it('keeps the three accounts apart in a person row, and the sample with its average', () => {
    // The same rule the report's rows follow: `counted_usd` and `notional_usd`
    // are two accounts and a row draws both, marked, and never their sum.
    const model = buildSpendModel(PERSON_REPORT, BUDGET, { persons: true, people: statsDocument() })
    const row = peopleOf(model).rows[0]
    expect(row?.counted.value).toBe(1.5)
    expect(row?.notional.value).toBe(0.4)
    expect(row?.notional.state).toBe('notional')
    expect(row?.counted.value).not.toBe(1.5 + 0.4)
    expect(row?.countedRuns.value).toBe(3)
    expect(row?.average.value).toBe(0.5)
    // The counts add up to the runs, which is what makes the row readable.
    expect((row?.succeeded.value ?? 0) + (row?.failed.value ?? 0)).toBe(row?.runs.value)
    const tree = renderSpend(factory, model, 'light')
    expect(texts(tree, 'spend-person-notional')[0]).toContain(NOTIONAL_MARK)
    expect(texts(tree, 'spend-person-counted')[0]).not.toContain(NOTIONAL_MARK)
  })
})

// ---------------------------------------------------------------------------
// The caps panel.
// ---------------------------------------------------------------------------

describe('the caps panel', () => {
  it('names the scope that binds each cap, which is what makes it actionable', () => {
    // A cap of $20 with no scope is a number a reader cannot act on: the fix is
    // different depending on whether it is the repository's own cap or the
    // global fallback. `per_run_cap_source` is the CLI's own answer and the
    // panel draws it.
    const model = modelOf()
    expect(model.caps.available).toBe(true)
    const widget = model.caps.repos[0]
    const docs = model.caps.repos[1]
    const infra = model.caps.repos[2]
    expect(widget?.perRunScope).toEqual({
      source: 'repo',
      name: "this repository's own cap",
      known: true,
    })
    expect(docs?.perRunScope.name).toBe('the repository default')
    expect(infra?.perRunScope.name).toBe('the global fallback')
    expect(widget?.dailyScope.name).toBe("this repository's own cap")
    expect(docs?.dailyScope.name).toBe('no cap is set at this scope')
  })

  it('draws the scope beside the cap, in markup a stylesheet and a test can find', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(texts(tree, 'spend-scope')).toContain("this repository's own cap")
    expect(texts(tree, 'spend-scope')).toContain('the global fallback')
    // Each repository draws its daily cap first and its per-run cap second, and
    // each cap carries its own scope word beside it.
    const rows = all(tree, 'spend-cap-repo')
    const scopesOf = (row: FakeScreen) => all(row, 'spend-scope')
    expect(rows.map((row) => scopesOf(row)[1]?.getAttribute('data-per-run-scope'))).toEqual([
      'repo',
      'default',
      'global',
    ])
    expect(rows.map((row) => scopesOf(row)[0]?.getAttribute('data-daily-scope'))).toEqual([
      'repo',
      'none',
      'repo',
    ])
    expect(texts(tree, 'spend-scope').filter((one) => one === 'no cap is set at this scope')).toHaveLength(1)
  })

  it('draws a cap the document does not carry as n/a, never as $0.00', () => {
    const docs = modelOf().caps.repos[1]
    expect(docs?.dailyCap.state).toBe('unavailable')
    expect(docs?.dailyCap.value).toBeNull()
    const tree = renderSpend(factory, modelOf(), 'light')
    const row = all(tree, 'spend-cap-repo')[1]
    if (row === undefined) throw new Error('no second repository row')
    // The `acme/docs` row: its daily cap is `null` with `daily_cap_source:
    // "none"` (`internal/cli/budget.go`'s `capSource`), so the cap is `n/a` with
    // the reader's reason and the scope word says no cap is set at that scope.
    expect(texts(row, 'spend-cap-daily')).toEqual([UNAVAILABLE_MARK])
    expect(one(row, 'spend-cap-daily').getAttribute('title')).toBe('the document has no repos[].daily_cap_usd')
    expect(texts(row, 'spend-scope')).toEqual(['no cap is set at this scope', 'the repository default'])
    // The zeros that *are* there are the document's own measured zeros --
    // `counted_usd` and `notional_usd` are plain numbers on the wire, and a
    // repository that counted nothing today counted zero.
    expect(texts(row, 'spend-cap-counted')).toEqual(['$0.00'])
    expect(texts(row, 'spend-cap-notional')).toEqual([`${NOTIONAL_MARK}$0.00`])
  })

  it('heads the panel and draws every figure of the global row under its own word', () => {
    // The panel is a list of labelled figures, not a table: no column header
    // carries `billed` or `headroom`, so each figure carries its own word, and a
    // reader never meets a run of numbers that says nothing about which account
    // each one belongs to.
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(texts(tree, 'spend-caps-title')).toEqual(['Caps today'])
    const global = one(tree, 'spend-cap-global')
    expect(figureWords(global)).toEqual([
      'daily cap',
      'per-run cap',
      'counted',
      'billed',
      'notional',
      'headroom',
    ])
    // Each word is beside its own figure: `budget show`'s six global values, in
    // the CLI's own meaning (`internal/cli/budget.go`'s `showGlobal`).
    expect(texts(capFigureOf(global, 'spend-cap-daily'), 'spend-cap-daily')).toEqual(['$25.00'])
    expect(texts(capFigureOf(global, 'spend-cap-per-run'), 'spend-cap-per-run')).toEqual(['$25.00'])
    expect(texts(capFigureOf(global, 'spend-cap-counted'), 'spend-cap-counted')).toEqual(['$12.50'])
    expect(texts(capFigureOf(global, 'spend-cap-spent'), 'spend-cap-spent')).toEqual(['$10.00'])
    expect(texts(capFigureOf(global, 'spend-cap-notional'), 'spend-cap-notional')).toEqual([
      `${NOTIONAL_MARK}$4.20`,
    ])
    expect(texts(capFigureOf(global, 'spend-cap-headroom'), 'spend-cap-headroom')).toEqual(['$12.50'])
  })

  it('draws the bar at the counted share of the daily cap, and never at a sum (MUTATION)', () => {
    // `counted_usd` is the figure the daily cap is enforced against
    // (`budget.Counters.Counted`, `internal/budget/model.go`: the money reserved
    // by runs and not yet released, a ceiling on spend), so it is the figure the
    // bar draws. The design's "billed + compute" is its assumption #2 and not a
    // figure the document holds: the renderer adds nothing, and billed, notional
    // and compute are three accounts.
    //
    // Mutation: feed the fill `spent + compute` -- the sum the design's sentence
    // invites -- instead of `counted`, and this test fails. The widget counted
    // $3.00 of a $5.00 cap (60 units of the bar's hundred), while its billed
    // $2.00 plus the $0.75 compute the document prices is $2.75 (55 units).
    const priced = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      repos: [{ ...THE_REPOS[0], compute_usd: 0.75 }],
    })
    const row = firstRepoRow(renderSpend(factory, priced, 'light'))
    expect(one(row, 'spend-cap-bar-fill').getAttribute('width')).toBe('60')
    expect(one(row, 'spend-cap-bar-track').getAttribute('width')).toBe('100')
    // Decorative geometry: the two figures it stands for are text beside it.
    expect(one(row, 'spend-cap-bar-svg').getAttribute('aria-hidden')).toBe('true')
    expect(texts(row, 'spend-cap-counted')).toEqual(['$3.00'])
    expect(texts(capFigureOf(row, 'spend-cap-daily'), 'spend-cap-daily')).toEqual(['$5.00'])
    // A repository past its cap draws a full bar rather than a share larger
    // than the cap, and says so in words.
    const over = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      repos: [{ ...THE_REPOS[0], counted_usd: 9, headroom_usd: 0 }, ...THE_REPOS.slice(1)],
    })
    expect(one(firstRepoRow(renderSpend(factory, over, 'light')), 'spend-cap-bar-fill').getAttribute('width')).toBe(
      '100',
    )
  })

  it('says "at cap" in words, amber beside the bar and never colour alone', () => {
    // A cap reached is a fact the panel may colour only because it also says it
    // (design section 3.5): the word is drawn either where the document's own
    // measured headroom has run out -- the CLI measures it under the
    // repository's cap *and* the project's -- or where the counted dollars are
    // at or past the cap the row carries.
    const reached = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      repos: [
        { ...THE_REPOS[0], counted_usd: 3, daily_cap_usd: 3, headroom_usd: 0 },
        { ...THE_REPOS[1], counted_usd: 1, daily_cap_usd: 5, headroom_usd: 0 },
        ...THE_REPOS.slice(2),
      ],
    })
    const tree = renderSpend(factory, reached, 'light')
    const rows = all(tree, 'spend-cap-repo')
    expect(rows.map((row) => row.getAttribute('data-at-cap'))).toEqual(['true', 'true', 'false'])
    expect(texts(rows[0] ?? tree, 'spend-cap-at-cap')).toEqual(['at cap'])
    expect(texts(rows[1] ?? tree, 'spend-cap-at-cap')).toEqual(['at cap'])
    // The repository under its cap draws neither the word nor the tone.
    expect(texts(rows[2] ?? tree, 'spend-cap-at-cap')).toEqual([])
    // An unavailable headroom is not a zero: the `acme/docs` row above carries
    // no cap at all and is not drawn as a repository at its limit.
    expect(all(tree, 'spend-cap-at-cap').length).toBe(2)
  })

  it('draws the headroom the "at cap" word is measured from, bar or no bar', () => {
    // `headroom_usd` is the room the CLI finds left under whichever cap binds --
    // the repository's own and the project-wide one, whichever is tighter
    // (`internal/cli/budget.go`) -- so a repository the global cap is holding
    // back is at its limit while its own counted dollars are still under its own
    // cap, and a repository whose cap nobody set is at its limit with no bar at
    // all. The row draws the headroom beside the word for exactly that reason: a
    // state whose figure is off-screen is one the reader cannot check.
    const held = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      repos: [{ ...THE_REPOS[1], counted_usd: 0, daily_cap_usd: null, headroom_usd: 0 }],
    })
    const row = firstRepoRow(renderSpend(factory, held, 'light'))
    expect(row.getAttribute('data-at-cap')).toBe('true')
    expect(texts(row, 'spend-cap-at-cap')).toEqual(['at cap'])
    // The no-daily-cap case: no geometry to contradict the word, the cap `n/a`
    // with its reason, and the measured zero that is the ground of the state
    // drawn beside it.
    expect(all(row, 'spend-cap-bar-fill')).toEqual([])
    expect(all(row, 'spend-cap-bar-svg')).toEqual([])
    expect(texts(capFigureOf(row, 'spend-cap-daily'), 'spend-cap-daily')).toEqual([UNAVAILABLE_MARK])
    expect(texts(capFigureOf(row, 'spend-cap-headroom'), 'spend-cap-headroom')).toEqual(['$0.00'])
  })

  it('draws no bar for a repository with no daily cap, and no sum in its place', () => {
    const row = all(renderSpend(factory, modelOf(), 'light'), 'spend-cap-repo')[1]
    if (row === undefined) throw new Error('no second repository row')
    // A bar of no length would read as a measurement of zero, so a cap the
    // document does not carry draws no geometry at all -- and the counted
    // dollars beside it are still the document's own measured zero.
    expect(all(row, 'spend-cap-bar-svg')).toEqual([])
    expect(all(row, 'spend-cap-bar-fill')).toEqual([])
    expect(one(row, 'spend-cap-bar')).toBeDefined()
    expect(texts(row, 'spend-cap-counted')).toEqual(['$0.00'])
    expect(row.getAttribute('data-at-cap')).toBe('false')
  })

  it('sets no attribute on the caps panel that could act', () => {
    // The caps panel draws geometry and figures and offers no destination: no
    // node carries a style, a destination, a resource or a handler.
    const tree = one(renderSpend(factory, modelOf(), 'light'), 'spend-caps')
    for (const node of walk(tree)) {
      for (const forbidden of ['style', 'href', 'src', 'onclick', 'oninput']) {
        expect(node.attributes.has(forbidden), `${node.tag}[${forbidden}]`).toBe(false)
      }
    }
  })

  it('reads `compute_usd` off a cap row as an estimate, and n/a where there is none', () => {
    // The owner approved `compute_usd` on each `showRepo` on 2026-10-28, and
    // `budget show --compute` writes it since 2026-10-29. This console's own
    // `budget.show` row declares that flag and the caps panel draws the figure
    // in its own cell, but no caller passes it yet -- the Spend screen's caps
    // read is flipped behind `budgetComputeSupported` in its own reviewed run --
    // so the key is still only in a document a person hands this reader. The
    // row reader reads it into the model as an **estimate**, compute's own
    // account, which is what the drawing test below starts from.
    const model = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      repos: [{ ...THE_REPOS[0], compute_usd: 0.75 }],
    })
    const row = model.caps.repos[0]
    expect(row?.compute.state).toBe('estimated')
    expect(row?.compute.value).toBe(0.75)
    // A measured `0` is not the `n/a` an absent key draws -- compute the
    // document priced at nothing is a figure, and the one value that tells the
    // two apart.
    const zero = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      repos: [{ ...THE_REPOS[0], compute_usd: 0 }],
    })
    expect(zero.caps.repos[0]?.compute.state).toBe('estimated')
    expect(zero.caps.repos[0]?.compute.value).toBe(0)
    // And a row without it -- the recorded document, and a `null` alike -- is
    // `n/a`, never `$0.00`.
    expect(modelOf().caps.repos[0]?.compute.state).toBe('unavailable')
    expect(modelOf().caps.repos[0]?.compute.value).toBeNull()
    const nulled = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      repos: [{ ...THE_REPOS[0], compute_usd: null }],
    })
    expect(nulled.caps.repos[0]?.compute.value).toBeNull()
  })

  it('draws that compute in its own cell: `est. $0.12`, `n/a` where there is none', () => {
    // The cell is the third dollar account of a cap row, beside the counted and
    // the notional dollars and never summed with either: a compute the
    // document priced is drawn through `formatReading` -- `ESTIMATED_MARK`'s
    // `est.`, and never the notional mark -- and a key the document does not
    // carry is `n/a` with its reason, never `$0.00`.
    const priced = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      repos: [{ ...THE_REPOS[0], compute_usd: 0.12 }],
    })
    const reading = priced.caps.repos[0]?.compute
    if (reading === undefined) throw new Error('no first repository row')
    expect(formatReading(reading, 'usd')).toBe(`${ESTIMATED_MARK} $0.12`)
    const tree = renderSpend(factory, priced, 'light')
    const row = firstRepoRow(tree)
    expect(texts(row, 'spend-cap-compute')).toEqual([`${ESTIMATED_MARK} $0.12`])
    expect(texts(row, 'spend-cap-compute')[0]).not.toContain(NOTIONAL_MARK)
    // The two accounts beside it are the document's own figures, untouched by
    // the third: `counted_usd: 3`, `notional_usd: 1` on this fixture.
    expect(texts(row, 'spend-cap-counted')).toEqual(['$3.00'])
    expect(texts(row, 'spend-cap-notional')).toEqual([`${NOTIONAL_MARK}$1.00`])
    // A measured `0` is a figure the document priced at nothing, and it is
    // drawn as one rather than as the `n/a` an absent key draws.
    const zero = renderSpend(
      factory,
      buildSpendModel(DAY_REPORT, { ...BUDGET, repos: [{ ...THE_REPOS[0], compute_usd: 0 }] }),
      'light',
    )
    expect(texts(firstRepoRow(zero), 'spend-cap-compute')).toEqual([`${ESTIMATED_MARK} $0.00`])
    // A `null` -- the key a `budget show --compute` writes for a repository
    // whose bucket read found no estimate -- is `n/a`, and the cell carries the
    // reader's own sentence for why, which is what keeps an `n/a` defensible.
    const nulled = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      repos: [{ ...THE_REPOS[0], compute_usd: null }],
    })
    const absent = firstRepoRow(renderSpend(factory, nulled, 'light'))
    expect(texts(absent, 'spend-cap-compute')).toEqual([UNAVAILABLE_MARK])
    expect(one(absent, 'spend-cap-compute').getAttribute('title')).toBe('the document has no compute_usd')
    // A value that is not a number is `n/a` too, with the reader's sentence for
    // *that* -- the caps panel refuses no document, so a shape it cannot read is
    // the same drawn state as a key it was not sent, and the two are told apart
    // by the reason the cell carries.
    const wrongKind = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      repos: [{ ...THE_REPOS[0], compute_usd: '0.12' }],
    })
    const wrongRow = firstRepoRow(renderSpend(factory, wrongKind, 'light'))
    expect(texts(wrongRow, 'spend-cap-compute')).toEqual([UNAVAILABLE_MARK])
    expect(one(wrongRow, 'spend-cap-compute').getAttribute('title')).toBe('compute_usd is a string, not a number')
    // The column is the read's and not the row's. The key is flag-conditioned,
    // so a document read without `--compute` -- the recorded fixture, and every
    // document this console produces today -- carries it on no row and draws no
    // compute cell at all: an `n/a` on every row would be a permanent column
    // for a flag nobody passed. Once one row carries it, every row draws the
    // cell, and a row that carries none is `n/a` rather than blank.
    expect(all(renderSpend(factory, modelOf(), 'light'), 'spend-cap-compute')).toHaveLength(0)
    const mixed = renderSpend(
      factory,
      buildSpendModel(DAY_REPORT, {
        ...BUDGET,
        repos: [{ ...THE_REPOS[0], compute_usd: 0.12 }, ...THE_REPOS.slice(1)],
      }),
      'light',
    )
    expect(all(mixed, 'spend-cap-compute')).toHaveLength(THE_REPOS.length)
    const [, withoutIt] = all(mixed, 'spend-cap-repo')
    if (withoutIt === undefined) throw new Error('no second repository row')
    expect(texts(withoutIt, 'spend-cap-compute')).toEqual([UNAVAILABLE_MARK])
  })

  it('draws an unrecognised scope word as an unknown, and never echoes it', () => {
    const caps: JSONDoc = {
      ...BUDGET,
      repos: [{ ...THE_REPOS[0], per_run_cap_source: 'wombat' }],
    }
    const model = buildSpendModel(DAY_REPORT, caps)
    const row = model.caps.repos[0]
    expect(row?.perRunScope.known).toBe(false)
    // The word is kept in the model, where a test can see it, and it is not
    // what the screen draws -- the same rule the Live screen's unknown flags
    // follow.
    expect(row?.perRunScope.source).toBe('wombat')
    expect(scopeOf('wombat').name).toBe('a scope this console does not know')
    const tree = renderSpend(factory, model, 'light')
    expect(drawn(tree)).not.toContain('wombat')
    const drawnRow = one(tree, 'spend-cap-repo')
    // The daily cap comes with its own scope span and the per-run cap with the
    // one that matters here: the second span of the row.
    expect(all(drawnRow, 'spend-scope')[1]?.getAttribute('data-per-run-scope')).toBe('unknown')
    expect(all(drawnRow, 'spend-scope')[1]?.getAttribute('data-scope-known')).toBe('false')
  })

  it('says it has no caps at all rather than drawing an empty panel', () => {
    // No `budget show --json` was read. That is not "this project has no caps":
    // the whole point of the design's `n/a` rules is that the two facts are
    // different, and an empty panel reads as the first one.
    const model = buildSpendModel(DAY_REPORT, null)
    expect(model.caps.available).toBe(false)
    expect(model.caps.global).toBeNull()
    const tree = renderSpend(factory, model, 'light')
    expect(one(tree, 'spend-caps').getAttribute('data-available')).toBe('false')
    expect(drawn(tree)).toContain('n/a, not zero')
    expect(drawn(tree)).not.toContain('$0.00')
  })

  it('reads the empty mode as unset, which acts as observe', () => {
    // `mode: ""` is the one empty value only this document carries: the CLI's
    // help says a department with no mode set behaves as observe. Drawing the
    // empty string is an empty cell and drawing `observe` invents a setting, so
    // the console says what it knows in its own words.
    const model = buildSpendModel(DAY_REPORT, { ...BUDGET, mode: '' })
    expect(model.caps.mode).toBeNull()
    expect(drawn(renderSpend(factory, model, 'light'))).toContain('mode unset (acts as observe)')
  })

  it('draws a kill with the scope it belongs to, global or per repository', () => {
    const kill = { by: 'ada', at: '2026-10-15T09:00:00Z', reason: 'daily cap reached' }
    const model = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      global: { ...THE_GLOBAL, kill },
      repos: [{ ...THE_REPOS[0], kill }],
    })
    expect(model.caps.global?.kill?.reason).toBe('daily cap reached')
    expect(model.caps.repos[0]?.kill?.by).toBe('ada')
    const tree = renderSpend(factory, model, 'light')
    const banners = all(tree, 'spend-kill')
    expect(banners).toHaveLength(2)
    expect(banners.every((banner) => banner.getAttribute('data-killed') === 'true')).toBe(true)
    expect(drawn(tree)).toContain('KILLED by')
  })

  it('draws the CLI\'s warnings verbatim and the notional account beside the counted one', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(texts(tree, 'spend-warning')).toEqual([
      'the day record for 2026-10-14 is missing, so counted_usd for that day is a lower bound',
    ])
    // Notional again, in its own state, beside the counted dollars it must
    // never be added to.
    expect(texts(tree, 'spend-cap-notional')[0]).toBe(`${NOTIONAL_MARK}$4.20`)
    expect(texts(tree, 'spend-cap-counted')[0]).toBe('$12.50')
  })

  it('draws the largest single lease with the CLI\'s own words, outside the global block', () => {
    // `max_reserve_usd` is a **top-level** field of `budget show --json`
    // (`budgetShowDoc`, `internal/cli/budget.go`): the project's largest single
    // lease (`budget.Limits.MaxReserveMicros`), a *limit* rather than a cap.
    // `printShow` writes it above the `project` block, and the CLI's own mode
    // line keeps it apart from the caps ("kill switches and the reserve limit
    // apply"). Inside the div whose word is `global` it would read as a global
    // cap, which is the one thing this panel must not say, so the test asserts
    // both halves: the figure, and where it is not.
    const model = modelOf()
    expect(model.caps.reserve).toEqual(measured(50))
    const tree = renderSpend(factory, model, 'light')
    const limit = one(tree, 'spend-cap-limit')
    expect(limit.getAttribute('data-limit')).toBe('max_reserve')
    expect(texts(limit, 'spend-cap-word')).toEqual([RESERVE_LABEL])
    expect(texts(limit, 'spend-cap-reserve')).toEqual(['$50.00'])
    // Not in the `global` block, and not in anything else either: one figure,
    // drawn once, in the block that says what it is.
    const global = one(tree, 'spend-cap-global')
    expect(texts(global, 'spend-cap-reserve')).toEqual([])
    expect(texts(tree, 'spend-cap-reserve')).toHaveLength(1)
    // The model says the same thing structurally: the global block has no
    // reserve field to misread.
    expect(Object.keys(model.caps.global ?? {})).not.toContain('reserve')
  })

  it('draws the project limit even when the document carries no global block', () => {
    // The two are independent fields of `budgetShowDoc`, so this document is
    // not a contradiction (`budget show --repo R` still prints the reserve
    // line), and a limit that vanished with the block it used to sit in would
    // be a figure lost to a placement mistake.
    const model = buildSpendModel(DAY_REPORT, { ...BUDGET, global: undefined })
    expect(model.caps.global).toBeNull()
    expect(model.caps.reserve).toEqual(measured(50))
    const tree = renderSpend(factory, model, 'light')
    expect(texts(one(tree, 'spend-cap-limit'), 'spend-cap-reserve')).toEqual(['$50.00'])
  })

  it('draws a limit the document does not carry as n/a, never as $0.00', () => {
    // `max_reserve_usd` is `*float64` on the wire, like every cap here: `null`
    // is "the document does not say", which is not a project that may take no
    // lease at all.
    const model = buildSpendModel(DAY_REPORT, { ...BUDGET, max_reserve_usd: null })
    expect(model.caps.reserve.state).toBe('unavailable')
    const tree = renderSpend(factory, model, 'light')
    expect(texts(one(tree, 'spend-cap-limit'), 'spend-cap-reserve')).toEqual([UNAVAILABLE_MARK])
    // And with no document at all the panel says so, in one sentence, rather
    // than drawing a limit it did not read -- the `available: false` path above.
    const none = buildSpendModel(DAY_REPORT, null)
    expect(none.caps.reserve.state).toBe('unavailable')
    expect(drawn(renderSpend(factory, none, 'light'))).not.toContain(RESERVE_LABEL)
  })
})

// ---------------------------------------------------------------------------
// The switch and the header.
// ---------------------------------------------------------------------------

describe('the breakdown switch', () => {
  it('draws the eight words and marks the document\'s own', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    const dims = all(tree, 'spend-dim')
    expect(dims.map((dim) => dim.textContent)).toEqual([
      'day',
      'week',
      'month',
      'year',
      'repo',
      'model',
      'person',
      'mode',
    ])
    expect(dims.filter((dim) => dim.getAttribute('data-current') === 'true').map((dim) => dim.textContent)).toEqual([
      'day',
    ])
    expect(one(tree, 'spend-switch').getAttribute('data-known')).toBe('true')
  })

  it('names a ninth breakdown as unknown without echoing its word', () => {
    // An `by` this build does not know is a newer CLI, and the console says so
    // in its own words: the value is not drawn, the same rule the Live screen's
    // unknown flags follow.
    const model = buildSpendModel({ ...DAY_REPORT, by: 'wombat', rows: [] }, BUDGET)
    expect(model.dimensionKnown).toBe(false)
    expect(model.dimension).toBeNull()
    const tree = renderSpend(factory, model, 'light')
    expect(one(tree, 'spend-switch').getAttribute('data-known')).toBe('false')
    expect(texts(tree, 'spend-dim-unknown')).toEqual(['an unknown breakdown'])
    expect(drawn(tree)).not.toContain('wombat')
    expect(
      all(tree, 'spend-dim').filter((dim) => dim.getAttribute('data-current') === 'true').map((dim) => dim.textContent),
    ).toEqual(['an unknown breakdown'])
  })

  it('gives each breakdown the text class the wire shape implies', () => {
    // `by repo`'s key is `CanonicalRepo`'s `owner/name` or, for a record with
    // no path, the slug `report` falls back to; `by person`'s is a person, and
    // a period's or a model's is a label.
    expect(keyClass('repo', 'acme/widget')).toBe('repository')
    expect(keyClass('repo', 'widget')).toBe('slug')
    expect(keyClass('person', 'ada@example.com')).toBe('person')
    expect(keyClass('day', '2026-10-15')).toBe('label')
    expect(keyClass('model', 'claude-sonnet-5')).toBe('label')
    expect(keyClass(null, 'whatever')).toBe('label')
  })
})

describe('the header', () => {
  it('draws the range, the convention that names the weeks and the source', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(texts(tree, 'spend-project')).toEqual(['fugaro-demo'])
    expect(texts(tree, 'spend-since')).toEqual(['2026-10-12'])
    expect(texts(tree, 'spend-until')).toEqual(['2026-10-15'])
    // A week row means different days under a different convention, so the
    // document's own `weeks` is drawn rather than left to be inferred.
    expect(texts(tree, 'spend-weeks')).toEqual(['iso-utc-monday'])
    expect(texts(tree, 'spend-source')).toEqual(['firestore'])
    expect(all(tree, 'spend-degraded')).toEqual([])
  })

  it('draws the degraded path\'s notice when the report came from run records', () => {
    const report: JSONDoc = {
      ...DAY_REPORT,
      source: 'run-records',
      degraded: true,
      notice: 'reading the run records: the budget database was not reachable',
    }
    const tree = renderSpend(factory, buildSpendModel(report, BUDGET), 'light')
    expect(one(tree, 'spend-degraded').getAttribute('data-degraded')).toBe('true')
    expect(texts(tree, 'spend-notice')).toEqual([
      'reading the run records: the budget database was not reachable',
    ])
  })
})

// ---------------------------------------------------------------------------
// The model's rows.
// ---------------------------------------------------------------------------

describe('a report row', () => {
  it('carries the three accounts, the runs and their hours, each in its own state', () => {
    const row = rowFor(modelOf(), '2026-10-12')
    expect(row.model.state).toBe('measured')
    expect(row.notional.state).toBe('notional')
    expect(row.compute.state).toBe('estimated')
    expect(row.runs.value).toBe(2)
    expect(row.runHours.value).toBe(1.5)
    expect(row.calls.value).toBe(17)
    // The run hours of a day whose compute was not estimated are `null` on the
    // wire (`RunHours` is only set under `ComputeKnown`), which is `n/a`.
    expect(rowFor(modelOf(), '2026-10-13').runHours.state).toBe('unavailable')
    // A *readable* row's null key keeps `readNumber`'s own sentence: the
    // row-own reason is the unreadable row's alone, and only a key present and
    // null because there was no record to read gets it.
    expect(rowFor(modelOf(), '2026-10-13').notional.because).toBe('the document has no notional_micros')
    expect(rowFor(modelOf(), '2026-10-13').runHours.because).toBe('the document has no run_hours')
  })

  it('carries a model row\'s tokens, and none for a model whose tokens were not tracked', () => {
    const model = buildSpendModel(MODEL_REPORT, BUDGET)
    const tracked = rowFor(model, 'claude-sonnet-5')
    expect(tracked.tokens?.in.value).toBe(120_000)
    expect(tracked.tokens?.cacheWrite.value).toBe(12_000)
    // `tokens` is `omitempty` on the wire: absent means the breakdown does not
    // track them (per model it does, per person it does not), which is a
    // different fact from a model with no tokens.
    expect(rowFor(model, 'claude-opus-5').tokens).toBeNull()
  })

  it('draws the three accounts in three separate cells', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(texts(tree, 'spend-model')[0]).toBe('$1.25')
    expect(texts(tree, 'spend-notional')[0]).toBe(`${NOTIONAL_MARK}$0.40`)
    expect(texts(tree, 'spend-compute')[0]).toBe('est. $0.12')
    expect(texts(tree, 'spend-run-hours')[0]).toBe('1.50 h')
    // The three money cells are `td`s of the table's row, one account each.
    expect(rowDrawn(tree, '2026-10-12').attributes.get('data-key')).toBe('2026-10-12')
    // Not one cell of the row is the sum of two accounts.
    expect(texts(tree, 'spend-model')[0]).not.toBe('$1.65')
    // The whole notional column, top to bottom: a figure or `n/a`, and never an
    // unmarked dollar.
    expect(texts(tree, 'spend-notional')).toEqual([
      `${NOTIONAL_MARK}$0.40`,
      UNAVAILABLE_MARK,
      `${NOTIONAL_MARK}$0.10`,
    ])
  })
})

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

/**
 * FakeScreen is the smallest thing the screen may draw into, and it is a fake
 * for the same reason `live.test.ts`'s is: the unit suite has no `document` and
 * the point is the *observation*. Every attribute that could make the window do
 * something -- the navigating attribute, a resource, an event handler, a style
 * -- throws, so a screen that reached for one fails here rather than in a
 * window nobody is watching.
 */
class FakeScreen implements ScreenElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  readonly classes: string[] = []
  textContent: string | null = null

  static readonly FORBIDDEN_ATTRIBUTES = new Set([
    ['h', 'ref'].join(''),
    'src',
    'srcdoc',
    'style',
    'formaction',
    'action',
  ])

  constructor(
    readonly tag: string,
    /** the namespace the element was made in: `svg` for the chart's geometry,
     * which the DOM makes with a different call from the rest. */
    readonly namespace: string = 'html',
  ) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeScreen.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`the spend screen may not set the ${name} attribute`)
    }
    if (lowered === 'class') {
      this.classes.length = 0
      this.classes.push(...value.split(/\s+/).filter((one) => one !== ''))
    }
    this.attributes.set(name, value)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }

  append(child: ScreenElement): void {
    this.nodes.push(child as FakeScreen)
  }

  childNodes(): readonly ScreenElement[] {
    return this.nodes
  }
}

/** factory makes fresh fakes, in the shape the renderer is handed. */
const factory: SpendFactory = {
  create: (tag) => new FakeScreen(tag),
  createNS: (tag) => new FakeScreen(tag, 'svg'),
}

/** walk visits every node of a tree, the root first. */
function walk(node: ScreenElement): readonly FakeScreen[] {
  const fake = node as FakeScreen
  const out: FakeScreen[] = [fake]
  for (const child of fake.childNodes()) out.push(...walk(child))
  return out
}

/** all is every node carrying a class, in document order. */
function all(node: ScreenElement, className: string): readonly FakeScreen[] {
  return walk(node).filter((one) => one.classes.includes(className))
}

/** one is the single node carrying a class, asserted to be single. */
function one(node: ScreenElement, className: string): FakeScreen {
  const found = all(node, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** firstRepoRow is the caps panel's first repository row, which is the row
 * every per-row cell is asserted on. */
function firstRepoRow(tree: ScreenElement): FakeScreen {
  const first = all(tree, 'spend-cap-repo')[0]
  if (first === undefined) throw new Error('no repository row')
  return first
}

/** figureWords is the word every labelled figure of a caps block carries, in
 * document order: the panel draws no column headers, so this is how a test says
 * which figure is which. */
function figureWords(node: ScreenElement): readonly string[] {
  return all(node, 'spend-cap-figure').map((figure) => figure.childNodes()[0]?.textContent ?? '')
}

/** capFigureOf is the labelled figure whose number cell carries `className`,
 * which is how a test says a figure is under the word it was drawn with. */
function capFigureOf(node: ScreenElement, className: string): FakeScreen {
  const found = all(node, 'spend-cap-figure').filter((figure) => all(figure, className).length > 0)
  expect(found, `expected one figure for .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no figure for .${className}`)
  return first
}

/** drawn is the whole tree's text, in document order: what a person reading the
 * screen would read if they read every cell. */
function drawn(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

/** texts is the text of every node carrying a class. */
function texts(node: ScreenElement, className: string): readonly (string | null)[] {
  return all(node, className).map((leaf) => leaf.textContent)
}

/** barDrawn is one drawn bar, by the account it draws and the key it belongs
 * to: the three accounts are three bars of one group, so a bar is only ever a
 * bar *of* an account and a key. */
function barDrawn(node: ScreenElement, account: SpendAccount, key: string): FakeScreen {
  const found = all(node, 'spend-bar').filter(
    (bar) => bar.getAttribute('data-account') === account && bar.getAttribute('data-key') === key,
  )
  expect(found, `expected one ${account} bar for ${key}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no ${account} bar for ${key}`)
  return first
}

/** barsOf is every drawn bar of one key, in the order they were drawn. */
function barsOf(node: ScreenElement, key: string): readonly FakeScreen[] {
  return all(node, 'spend-bar').filter((bar) => bar.getAttribute('data-key') === key)
}

/** chartKeyOf is the chart's own label for one key, which is where a gap, an
 * `n/a` account and the `(partial)` mark are written. */
function chartKeyOf(node: ScreenElement, key: string): FakeScreen {
  const found = all(node, 'spend-chart-key').filter((one) => one.getAttribute('data-key') === key)
  expect(found, `expected one chart label for ${key}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no chart label for ${key}`)
  return first
}

/** rowDrawn is the table's row for one key. */
function rowDrawn(node: ScreenElement, key: string): FakeScreen {
  const found = all(node, 'spend-row').filter((row) => row.getAttribute('data-key') === key)
  expect(found, `expected one row for ${key}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no row for ${key}`)
  return first
}

describe('renderSpendFrame', () => {
  it('draws the two sections and no figure, each section holding its busy slot first', () => {
    // The shell is what the entry mounts the moment the first of the two reads
    // lands (console 0.116.1, the owner's rule of 2026-10-31): one element per
    // section and not a word of either document, so a section whose read has
    // not answered yet is an empty element and not a blank screen.
    const frame = renderSpendFrame(factory, 'light')
    expect(frame.root.getAttribute('data-theme')).toBe('light')
    expect((frame.root as FakeScreen).classes).toContain('spend')
    expect(drawn(frame.root)).toBe('')
    expect((frame.report as FakeScreen).classes).toContain('spend-report')
    expect((frame.caps as FakeScreen).classes).toContain('spend-caps')
    // The slot is the section's **first** child and the shell's own element, not
    // something a filler appends: a mark put there in the click's own frame sits
    // at the top of the section, over the figures that section already drew,
    // rather than under a table nobody has scrolled to.
    expect(frame.report.childNodes()[0]).toBe(frame.reportSlot)
    expect(frame.caps.childNodes()[0]).toBe(frame.capsSlot)
    expect((frame.reportSlot as FakeScreen).classes).toContain(BUSY_SLOT_CLASS)
    expect((frame.capsSlot as FakeScreen).classes).toContain(BUSY_SLOT_CLASS)
  })

  it('fills each section without appending a second slot, so the one-shot drawing keeps the shell', () => {
    // `renderSpend` is the shell with both sections filled, and the fillers find
    // the slot the shell drew rather than drawing one of their own: a filler that
    // appended would leave two slots per section, and the entry's mark would land
    // in one of them depending on which drawing path ran.
    const tree = renderSpend(factory, modelOf(), 'light')
    const report = one(tree, 'spend-report')
    const caps = one(tree, 'spend-caps')
    expect(all(report, BUSY_SLOT_CLASS)).toHaveLength(1)
    expect(all(caps, BUSY_SLOT_CLASS)).toHaveLength(1)
    expect((report.childNodes()[0] as FakeScreen).classes).toContain(BUSY_SLOT_CLASS)
    expect((caps.childNodes()[0] as FakeScreen).classes).toContain(BUSY_SLOT_CLASS)
    // Filled, the same shell now reads: the range is the **document's** own, so
    // the section that was empty above carries a figure, and the slot is still
    // the first thing in it.
    expect(texts(tree, 'spend-since')).toEqual(['2026-10-12'])
  })
})

describe('renderSpend', () => {
  it('puts the theme on the root, so a stylesheet has two palettes to draw', () => {
    for (const theme of SPEND_THEMES) {
      const tree = renderSpend(factory, modelOf(), theme)
      expect(tree.getAttribute('data-theme')).toBe(theme)
      expect((tree as FakeScreen).classes).toContain('spend')
    }
  })

  it('draws one grouped chart on one shared axis, and never a chart per account', () => {
    // One chart: the three accounts are three bars of one key, side by side on
    // one axis, and the axis top is the largest value any of them draws -- the
    // billed 1.25 of the 12th. The notional bar is its own share of that same
    // axis (0.40 of 1.25), so the two are read against one scale and never
    // against a total.
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(all(tree, 'spend-chart')).toHaveLength(1)
    expect(all(tree, 'spend-account')).toEqual([])
    expect(texts(tree, 'spend-axis-top')).toEqual(['$1.25'])
    expect(texts(tree, 'spend-legend-word')).toEqual([
      'billed model spend',
      '~$ subscription list price',
      'compute (estimated)',
    ])
    expect(barsOf(tree, '2026-10-12')).toHaveLength(3)
    expect(barDrawn(tree, 'model', '2026-10-12').getAttribute('data-height')).toBe('100.0%')
    expect(barDrawn(tree, 'notional', '2026-10-12').getAttribute('data-height')).toBe('32.0%')
    expect(barDrawn(tree, 'compute', '2026-10-12').getAttribute('data-height')).toBe('9.6%')
  })

  it('never draws billed + notional as a figure, a bar or an axis', () => {
    // The rule the design's money grammar turns on: the two accounts are
    // different kinds of money and are never added. Billed 1.25 and notional
    // 0.40 are on the same day of this document, so a screen that summed them
    // would draw $1.65 -- as the day's figure, as the day's bar or as the axis
    // top -- and this is the test that fails when it does.
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(drawn(tree)).not.toContain('$1.65')
    expect(texts(tree, 'spend-axis-top')).toEqual(['$1.25'])
    expect(barDrawn(tree, 'model', '2026-10-12').getAttribute('data-height')).toBe('100.0%')
    expect(barDrawn(tree, 'notional', '2026-10-12').getAttribute('data-height')).toBe('32.0%')
    const row = rowDrawn(tree, '2026-10-12')
    expect(texts(row, 'spend-model')).toEqual(['$1.25'])
    expect(texts(row, 'spend-notional')).toEqual([`${NOTIONAL_MARK}$0.40`])
  })

  it('draws a key\'s bars as siblings on the axis, never stacked on one another', () => {
    // Grouped, never stacked: each bar's foot is the axis, so its height is its
    // own account's share of the axis top. Stacked, the notional bar would
    // start where the billed one ends.
    const tree = renderSpend(factory, modelOf(), 'light')
    const bars = barsOf(tree, '2026-10-12')
    expect(bars.map((bar) => bar.getAttribute('data-account'))).toEqual(['model', 'notional', 'compute'])
    for (const bar of bars) {
      const height = Number.parseFloat(bar.getAttribute('height') ?? '')
      const foot = Number.parseFloat(bar.getAttribute('y') ?? '') + height
      expect(foot).toBeCloseTo(100, 5)
    }
    const xs = bars.map((bar) => Number.parseFloat(bar.getAttribute('x') ?? ''))
    expect(xs[0]).toBeLessThan(xs[1] ?? 0)
    expect(xs[1]).toBeLessThan(xs[2] ?? 0)
  })

  it('draws the gridlines as shares of the one axis, and labels only its top', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(all(tree, 'spend-gridline')).toHaveLength(4)
    expect(all(tree, 'spend-gridline').map((line) => line.getAttribute('y1'))).toEqual(['75', '50', '25', '0'])
    expect(all(tree, 'spend-axis-line')).toHaveLength(1)
    expect(texts(tree, 'spend-axis-word')).toEqual(['axis'])
    // The axis top is the one figure: a label on every line would be four
    // numbers saying three times what the top already says.
    expect(texts(tree, 'spend-axis-top')).toEqual(['$1.25'])
  })

  it('draws the chart as geometry: one aria-hidden svg in the drawing\'s own units', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    const svg = one(tree, 'spend-chart-svg')
    expect(svg.tag).toBe('svg')
    expect(svg.namespace).toBe('svg')
    expect(svg.getAttribute('viewBox')).toBe('0 0 100 100')
    expect(svg.getAttribute('preserveAspectRatio')).toBe('none')
    expect(svg.getAttribute('aria-hidden')).toBe('true')
    // Every value the chart draws is text beside it -- the key labels, the
    // legend and the table -- so a reader told the svg is decoration loses no
    // figure and no word.
    expect(drawn(one(tree, 'spend-chart'))).toContain('$1.25')
    expect(texts(one(tree, 'spend-chart'), 'spend-chart-key')).toHaveLength(4)
  })

  it('draws an axis of n/a, and no gridlines, when no account has a value to draw', () => {
    const model = buildSpendModel({ ...MODEL_REPORT, rows: [] }, BUDGET)
    const tree = renderSpend(factory, model, 'light')
    expect(texts(tree, 'spend-axis-top')).toEqual([UNAVAILABLE_MARK])
    // No scale, so no gridlines: a scale of nothing is not a measurement.
    expect(all(tree, 'spend-gridline')).toEqual([])
    // And no row and no bar invents a zero for an account the document does
    // not carry: every money cell of the empty range says `n/a`.
    expect(drawn(one(tree, 'spend-table'))).not.toContain('$0.00')
    expect(texts(tree, 'spend-model')).toEqual([])
  })

  it('draws a gap as one hatched column and the table\'s own sentence, never a zero', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    const column = one(tree, 'spend-gap-column')
    expect(column.getAttribute('data-key')).toBe('2026-10-14')
    expect(Number.parseFloat(column.getAttribute('height') ?? '')).toBeCloseTo(100, 5)
    expect(texts(tree, 'spend-chart-gap')).toEqual(['no record'])
    // No account draws a bar for that day: a bar of no height reads as a
    // measurement of zero, and there is no document at all behind the column.
    expect(barsOf(tree, '2026-10-14')).toEqual([])
    const row = rowDrawn(tree, '2026-10-14')
    expect(row.getAttribute('data-gap')).toBe('true')
    expect(texts(row, 'spend-gap-cell')).toEqual(['No record for this day. A gap, not a zero.'])
    expect(texts(row, 'spend-model')).toEqual([])
    expect(drawn(row)).not.toContain('$0.00')
    // The runs the day does not cover are the console's gap mark, and the money
    // columns are not there at all: three `n/a`s would read as three
    // measurements, and there is no document behind the row.
    expect(texts(row, 'spend-runs')).toEqual([GAP_MARK])
    expect(texts(row, 'spend-run-hours')).toEqual([GAP_MARK])
  })

  it('tags a partial day in the chart and in the table, and keeps its figure', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    const bar = barDrawn(tree, 'model', '2026-10-15')
    expect(bar.getAttribute('data-partial')).toBe('true')
    expect(bar.getAttribute('data-hollow')).toBe('true')
    // The height is a real one, and it is a share of the chart's own axis: 0.30
    // of 1.25. Hollow is not empty.
    expect(bar.getAttribute('data-height')).toBe('24.0%')
    expect(barDrawn(tree, 'model', '2026-10-12').getAttribute('data-hollow')).toBe('false')
    const key = chartKeyOf(tree, '2026-10-15')
    expect(key.getAttribute('data-partial')).toBe('true')
    expect(texts(key, 'spend-partial')).toEqual([PARTIAL_MARKER])
    expect(chartKeyOf(tree, '2026-10-12').getAttribute('data-partial')).toBe('false')
    const row = rowDrawn(tree, '2026-10-15')
    expect(row.getAttribute('data-partial')).toBe('true')
    expect(row.getAttribute('data-hollow')).toBe('true')
    expect(texts(row, 'spend-partial')).toEqual([PARTIAL_MARKER])
  })

  it('draws the eight headings and one row per period, the gap day included', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    const head = one(tree, 'spend-table-head')
    expect(texts(head, 'spend-th')).toEqual([
      'key',
      'billed model',
      'notional (list price)',
      'compute (estimated)',
      'runs',
      'runtime',
      'tokens in',
      'tokens out',
    ])
    expect(all(tree, 'spend-row').map((row) => row.getAttribute('data-key'))).toEqual([
      '2026-10-12',
      '2026-10-13',
      '2026-10-14',
      '2026-10-15',
    ])
    expect(texts(rowDrawn(tree, '2026-10-13'), 'spend-runs')).toEqual(['1'])
    expect(texts(rowDrawn(tree, '2026-10-13'), 'spend-run-hours')).toEqual([UNAVAILABLE_MARK])
  })

  it('gives every row the table\'s own width, the gap day\'s one cell included', () => {
    // A row with fewer columns than the headings would put the figures after it
    // under the wrong word, and the gap row is the one that could: its money is
    // **one** cell across the money columns. What is asserted is the width the
    // headings add up to and the one span that makes the gap row add up to it
    // -- a span of two or of four would leave the row a column short or over.
    const tree = renderSpend(factory, modelOf(), 'light')
    const headings = texts(one(tree, 'spend-table-head'), 'spend-th')
    const cells = (row: FakeScreen): readonly FakeScreen[] =>
      (row.childNodes() as readonly FakeScreen[]).filter((child) => child.tag === 'td')
    const width = (row: FakeScreen): number =>
      cells(row).reduce((sum, cell) => sum + Number(cell.getAttribute('colspan') ?? '1'), 0)
    for (const row of all(tree, 'spend-row')) {
      expect(width(row), `the row for ${row.getAttribute('data-key') ?? ''}`).toBe(headings.length)
    }
    // The gap's own span, exactly: the three money columns, and the counts that
    // follow it are cells like any other row's.
    const gap = rowDrawn(tree, '2026-10-14')
    expect(cells(gap).map((cell) => cell.getAttribute('colspan'))).toEqual([null, '3', null, null, null, null])
    // `spend-num` is the shared numeric class every figure cell carries, so
    // what is asserted here is the column hook under it.
    const hooks = (cell: FakeScreen): readonly string[] =>
      cell.classes.filter((name) => name !== 'spend-num')
    expect(cells(gap).map(hooks)).toEqual([
      ['spend-key'],
      ['spend-gap-cell'],
      ['spend-runs'],
      ['spend-run-hours'],
      ['spend-tokens-in'],
      ['spend-tokens-out'],
    ])
  })

  it('draws the two token columns, and n/a with the console\'s reason for a document that tracks none', () => {
    // The token counts are the rest of what a period's record carries, and the
    // table draws the columns for every row: a breakdown the document does not
    // track is `n/a` with the reason behind it, because a blank cell reads as a
    // zero and "this report does not count tokens" is not "this model used
    // none". The 12th's row of the day report tracks none at all.
    const model = buildSpendModel(MODEL_REPORT, BUDGET)
    const tree = renderSpend(factory, model, 'light')
    const tracked = rowDrawn(tree, 'claude-sonnet-5')
    expect(texts(tracked, 'spend-tokens-in')).toEqual(['120,000'])
    expect(texts(tracked, 'spend-tokens-out')).toEqual(['8,000'])
    const untracked = rowDrawn(tree, 'claude-opus-5')
    expect(texts(untracked, 'spend-tokens-in')).toEqual([UNAVAILABLE_MARK])
    expect(texts(untracked, 'spend-tokens-out')).toEqual([UNAVAILABLE_MARK])
    expect(one(untracked, 'spend-tokens-in').getAttribute('title')).toBe('the document does not track tokens for this period')
    // And a day the history does not hold draws the em dash every other count
    // of that row draws, not a second sentence about tokens.
    const gap = rowDrawn(renderSpend(factory, modelOf(), 'light'), '2026-10-14')
    expect(texts(gap, 'spend-tokens-in')).toEqual([GAP_MARK])
    expect(texts(gap, 'spend-tokens-out')).toEqual([GAP_MARK])
  })

  it('draws no bar for an n/a account, and writes its n/a at the bar\'s own foot', () => {
    // The 13th carries no notional and no compute: two of the group's three
    // bars are absent -- not zero-height -- and each missing one says `n/a`.
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(barsOf(tree, '2026-10-13').map((bar) => bar.getAttribute('data-account'))).toEqual(['model'])
    const key = chartKeyOf(tree, '2026-10-13')
    expect(all(key, 'spend-chart-na').map((none) => none.getAttribute('data-account'))).toEqual([
      'notional',
      'compute',
    ])
    expect(texts(key, 'spend-chart-na')).toEqual([UNAVAILABLE_MARK, UNAVAILABLE_MARK])
    expect(drawn(key)).not.toContain('$0.00')
  })

  it('draws the four totals cells and the line that says the money is not added', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    const totals = one(tree, 'spend-totals')
    expect(texts(totals, 'spend-totals-word')).toEqual([
      'billed model',
      'notional (list price)',
      'compute (estimated)',
      'runs · runtime',
    ])
    expect(texts(totals, 'spend-total-model')).toEqual(['$2.45'])
    expect(texts(totals, 'spend-total-notional')).toEqual([`${NOTIONAL_MARK}$0.50`])
    expect(texts(totals, 'spend-total-compute')).toEqual(['est. $0.12'])
    expect(texts(totals, 'spend-total-runs')).toEqual(['4'])
    expect(texts(totals, 'spend-total-run-hours')).toEqual(['1.50 h'])
    expect(totals.getAttribute('data-partial')).toBe('true')
    expect(texts(totals, 'spend-totals-partial')).toEqual(['Today is partial.'])
    expect(texts(totals, 'spend-totals-rule')).toEqual([
      'Billed and notional are different kinds of money, so they are never added together.',
    ])
    // Four cells, and not a fifth that added two of them.
    expect(drawn(totals)).not.toContain('$2.95')
  })

  it('says nothing about a partial range when the document says the range is final', () => {
    // "Today is partial." is a fact about this range, not a permanent label: a
    // final range draws the rule and not the partial sentence.
    const tree = renderSpend(factory, buildSpendModel(MODEL_REPORT, BUDGET), 'light')
    const totals = one(tree, 'spend-totals')
    expect(totals.getAttribute('data-partial')).toBe('false')
    expect(texts(totals, 'spend-totals-partial')).toEqual([])
    expect(texts(totals, 'spend-totals-rule')).toHaveLength(1)
  })

  it('lists the rows the range does not cover in the table, and draws none of them', () => {
    const before = { ...DAY_ROWS[0], key: '2026-10-01', start: '2026-10-01', end: '2026-10-01' }
    const model = modelOf({ ...DAY_REPORT, rows: [before, ...DAY_ROWS] })
    const tree = renderSpend(factory, model, 'light')
    expect(all(tree, 'spend-chart-key').map((key) => key.getAttribute('data-key'))).toEqual([
      '2026-10-12',
      '2026-10-13',
      '2026-10-14',
      '2026-10-15',
    ])
    expect(barsOf(tree, '2026-10-01')).toEqual([])
    const row = rowDrawn(tree, '2026-10-01')
    expect(row.getAttribute('data-gap')).toBe('false')
    expect(texts(row, 'spend-model')).toEqual(['$1.25'])
  })

  it('writes every cell as text and sets no attribute that could act', () => {
    // The fake throws on the navigating attribute, `src`, `srcdoc`, `style`,
    // `formaction`, `action` and every `on*` handler, so what is left is
    // exactly the set below: the class hooks a stylesheet keys off, the title
    // an explanation goes in, `data-` readings, the two ARIA states a segmented
    // control and a chart have to carry, and the geometry of the chart (its own
    // box and the four lengths of a bar) plus the span of the gap row's cell.
    const allowed = new Set([
      'class',
      'title',
      'type',
      'aria-pressed',
      'aria-hidden',
      'viewBox',
      'preserveAspectRatio',
      'height',
      'x',
      'y',
      'width',
      'colspan',
      'x1',
      'x2',
      'y1',
      'y2',
    ])
    const tree = renderSpend(factory, modelOf(), 'light')
    for (const node of walk(tree)) {
      for (const name of node.attributes.keys()) {
        expect(name.startsWith('data-') || allowed.has(name), `${node.tag}[${name}]`).toBe(true)
      }
      // Nothing here can be clicked into a navigation, load a resource, or run
      // a handler: the screen draws figures and offers no destination.
      for (const forbidden of ['style', 'href', 'src', 'onclick', 'oninput']) {
        expect(node.attributes.has(forbidden), `${node.tag}[${forbidden}]`).toBe(false)
      }
    }
  })

  it('leaves the anchor and its destination out of the spend screen entirely', () => {
    // This screen offers no destination, so there is not one element here that
    // could be clicked into a navigation.
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(walk(tree).filter((node) => node.tag === 'a')).toEqual([])
  })

  it('puts the sanitized key, not the document\'s, into an attribute', () => {
    // A key `putText` refuses is drawn as `n/a`, and `data-key` is read back off
    // the drawn cell for exactly that reason: an attribute echoing the raw
    // string would put back on screen what the refusal kept off it, and a
    // reviewer checking attribute *names* would not see it.
    const report: JSONDoc = {
      ...MODEL_REPORT,
      rows: [{ ...MODEL_ROWS[0], key: 'claude\nsonnet' }],
    }
    const tree = renderSpend(factory, buildSpendModel(report, BUDGET), 'light')
    // The chart's label and the table's cell are the same drawn key, twice.
    expect(all(tree, 'spend-key').map((key) => key.textContent)).toEqual([UNAVAILABLE_MARK, UNAVAILABLE_MARK])
    expect(one(tree, 'spend-row').getAttribute('data-key')).toBe(UNAVAILABLE_MARK)
    expect(chartKeyOf(tree, UNAVAILABLE_MARK).getAttribute('data-key')).toBe(UNAVAILABLE_MARK)
    expect(drawn(tree)).not.toContain('claude\nsonnet')
  })

  it('draws into the same factory shape the Live screen draws into', () => {
    // Both screens declare their own element interface (neither may edit
    // `renderer/app.ts`, which is Task 2.0's file), so the two declarations are
    // asserted assignable **in both directions** here: a drift would otherwise
    // be a screen that compiles and draws nothing.
    const asLive = (fact: ScreenFactory): LiveScreenFactory => fact
    const asSpend = (fact: LiveScreenFactory): ScreenFactory => fact
    const live = asLive(factory)
    expect(asSpend(live).create('span').tag).toBe('span')
  })
})

// ---------------------------------------------------------------------------
// The pieces, on their own.
// ---------------------------------------------------------------------------

describe('the readings this file builds for itself', () => {
  it('are the same readers every screen uses, so their sentences stay the console\'s', () => {
    // A screen may not invent a reading: `measured`, `notional`, `estimated`,
    // `gap` and `unavailable` are `availability.ts`'s, and `readNumber` is the
    // one door a document's number comes through. This is the smoke test that
    // the two doors are the ones in use.
    expect(measured(1).value).toBe(1)
    expect(notional(1).state).toBe('notional')
    expect(estimated(1).state).toBe('estimated')
    expect(gap('no document').value).toBeNull()
    expect(unavailable('no figure').because).toBe('no figure')
  })
})
