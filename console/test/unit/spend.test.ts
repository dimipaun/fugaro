// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The Spend screen's row model, its three series and its caps panel
// (desktop-console plan Task 2.2; design section 6.2 screen 2, D15;
// `console/docs/json-contract.md` sections 3 and 4).
//
// Four things are being held here, and each is a sentence from the plan or the
// design rather than a preference:
//
//   1. **no screen ever adds a notional to a real total.** The plan says it, the
//      design's first rule says it, and `internal/report/report.go`'s package
//      comment says it in the CLI's own voice ("Model dollars, notional dollars
//      ... and compute dollars are three separate columns and are never added
//      together"). The test below pins it to the *series model* -- the number
//      the chart is drawn from -- and compares it against the sum, so a model
//      that started adding them fails here with both figures named;
//   2. **a partial day is hollow** -- `(partial)` is the CLI's own word
//      (`internal/report/render.go`'s table appends it to the row key) and the
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
  buildSpendModel,
  dayRange,
  keyClass,
  peopleArguments,
  renderSpend,
  scopeOf,
} from '../../src/renderer/screens/spend'
import type {
  SpendAccount,
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
 * day that is not final (`internal/cli/report.go`: today and yesterday keep
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
 * a row's key is a rigor mode or the CLI's own `none` (`internal/report/
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

/** rowFor is one row by its key. */
function rowFor(model: SpendModel, key: string): SpendRow {
  const found = model.rows.find((row) => row.key === key)
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
    // `internal/report/report.go`'s `Dim` values, one by one: the switch draws
    // the CLI's vocabulary, and a word the CLI renamed would leave the switch
    // offering a breakdown `report` refuses. The whole list against the CLI's
    // own `--by` help is `report-dimensions.test.ts`; this is the same fact
    // held name by name.
    const go = 'internal/report/report.go'
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
    // (`internal/cli/report.go:40`), and `src/main/ipc.ts` restates the bound
    // for the argument side. The console's series honours the same number: a
    // document claiming a longer range is one `report` itself refuses.
    expect(sourceConstant('internal/cli/report.go', 'reportMaxDays')).toBe('3660')
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
    // And the notional series' axis top is notional too: three accounts, three
    // axes, never one shared scale.
    expect(notional.peak.state).toBe('notional')
    expect(seriesOf(model, 'model').peak.state).toBe('measured')
    expect(seriesOf(model, 'compute').peak.state).toBe('estimated')
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
    // The height of a point with no figure is a gap, never a zero-height bar:
    // a bar of no width reads as "this day spent nothing".
    expect(second.height.state).toBe('gap')

    const compute = pointFor(seriesOf(model, 'compute'), '2026-10-13')
    expect(compute.reading.state).toBe('unavailable')
    expect(compute.reading.value).toBeNull()
  })

  it('keeps compute in the estimated state wherever the document carries it', () => {
    // `compute_estimated` is true whenever `compute_micros` is set
    // (`internal/report/render.go`'s `jsonLine`), and an estimate drawn as a
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
    expect(missing.height.state).toBe('gap')
    expect(missing.reading.because).toContain('no document')
    // A line drawn through the missing day would assert, in ink, a measurement
    // nobody made: 12-13 and 15 are two segments.
    expect(billed.segments).toBe(2)
  })

  it('breaks every account\'s series, and says so in the console\'s own words', () => {
    const model = modelOf()
    // Notional: the 13th is `n/a` and the 14th is a gap, so the 12th and the
    // 15th are two segments.
    expect(seriesOf(model, 'notional').segments).toBe(2)
    expect(seriesOf(model, 'compute').segments).toBe(1)
    expect(model.notes.join(' ')).toContain('a gap, not a zero')
  })

  it('breaks every chart on the day the history does not hold, in the drawn tree', () => {
    // The three charts share a key space and each of them has a point for the
    // missing day: `spend.spec.ts` reads exactly this list back out of the
    // window (three gaps, one per account), and the container this task was
    // written in cannot launch Electron, so the fact is held here as well.
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(
      all(tree, 'spend-point')
        .filter((point) => point.getAttribute('data-state') === 'gap')
        .map((point) => point.getAttribute('data-key')),
    ).toEqual(['2026-10-14', '2026-10-14', '2026-10-14'])
  })

  it('says nothing about gaps when every day of the range has a row', () => {
    // The other side of the boundary: a range whose days are all in the history
    // is one unbroken line and no legend. A console that drew the legend
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
    expect(billed.segments).toBe(1)
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
      expect(model.rows.map((row) => row.key)).toContain('2026-10-01')
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
    expect(billed.segments).toBe(1)
  })
})

// ---------------------------------------------------------------------------
// The person breakdown, off by default.
// ---------------------------------------------------------------------------

describe('the person breakdown', () => {
  it('is off by default: the rows are in the document and the screen draws none of them', () => {
    const model = buildSpendModel(PERSON_REPORT, BUDGET)
    expect(model.persons).toEqual({ enabled: false, withheld: true })
    expect(model.rows).toEqual([])
    for (const series of model.series) {
      expect(series.points).toEqual([])
      expect(series.peak.value).toBeNull()
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
    expect(model.rows.map((row) => row.key)).toEqual(['ada@example.com', 'unknown'])
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
    expect(model.rows.map((row) => row.key)).toEqual(['pro', 'none'])
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
    expect(texts(tree, 'spend-key')).toEqual(['pro', 'none'])
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
    // "none"` (`internal/cli/budget.go`'s `capSource`), and its headroom is
    // `null` too because there is no cap to measure against. Both are `n/a`.
    expect(texts(row, 'spend-cap-daily')).toEqual([UNAVAILABLE_MARK])
    expect(texts(row, 'spend-cap-headroom')).toEqual([UNAVAILABLE_MARK])
    // The zeros that *are* there are the document's own measured zeros --
    // `counted_usd` and `notional_usd` are plain numbers on the wire, and a
    // repository that counted nothing today counted zero.
    expect(texts(row, 'spend-cap-counted')).toEqual(['$0.00'])
    expect(texts(row, 'spend-cap-notional')).toEqual([`${NOTIONAL_MARK}$0.00`])
  })

  it('reads `compute_usd` off a cap row as an estimate, and n/a where there is none', () => {
    // The owner approved `compute_usd` on each `showRepo` on 2026-10-28, ahead
    // of the CLI release that writes it. The row reader already ignores keys it
    // does not know, so the console reads the new one into the model -- as an
    // **estimate**, compute's own account -- and draws nothing with it yet; the
    // Spend redesign is the screen that will.
    const model = buildSpendModel(DAY_REPORT, {
      ...BUDGET,
      repos: [{ ...THE_REPOS[0], compute_usd: 0.75 }],
    })
    const row = model.caps.repos[0]
    expect(row?.compute.state).toBe('estimated')
    expect(row?.compute.value).toBe(0.75)
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

  constructor(readonly tag: string) {}

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
const factory: ScreenFactory = { create: (tag) => new FakeScreen(tag) }

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

/** pointDrawn is one drawn point of one account's chart, by its key: the three
 * charts share a key space, so a point is only ever a point *of* an account. */
function pointDrawn(node: ScreenElement, account: SpendAccount, key: string): FakeScreen {
  const block = all(node, 'spend-account').find((one) => one.getAttribute('data-account') === account)
  if (block === undefined) throw new Error(`no ${account} chart`)
  const found = all(block, 'spend-point').filter((point) => point.getAttribute('data-key') === key)
  expect(found, `expected one ${account} point for ${key}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no ${account} point ${key}`)
  return first
}

describe('renderSpend', () => {
  it('puts the theme on the root, so a stylesheet has two palettes to draw', () => {
    for (const theme of SPEND_THEMES) {
      const tree = renderSpend(factory, modelOf(), theme)
      expect(tree.getAttribute('data-theme')).toBe(theme)
      expect((tree as FakeScreen).classes).toContain('spend')
    }
  })

  it('draws the three accounts as three charts, each with its own axis top', () => {
    // Three accounts, three charts: a shared axis would put a notional bar and
    // a billed bar at heights that invite a comparison the design forbids.
    const tree = renderSpend(factory, modelOf(), 'light')
    const accounts = all(tree, 'spend-account')
    expect(accounts.map((account) => account.getAttribute('data-account'))).toEqual([
      'model',
      'notional',
      'compute',
    ])
    expect(texts(tree, 'spend-peak')).toEqual(['$1.25', `${NOTIONAL_MARK}$0.40`, 'est. $0.12'])
    expect(texts(tree, 'spend-series').length).toBe(3)
    expect(
      texts(tree, 'spend-account-title').map((title) => (title ?? '').includes('NOTIONAL~')),
    ).toEqual([false, true, false])
  })

  it('draws a gap as the mark, and never as a zero-height measured bar', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    const point = pointDrawn(tree, 'model', '2026-10-14')
    expect(point.getAttribute('data-state')).toBe('gap')
    expect(point.getAttribute('data-height')).toBe(GAP_MARK)
    expect(drawn(point)).toContain(GAP_MARK)
    expect(drawn(point)).not.toContain('$0.00')
  })

  it('marks a partial point hollow and writes the marker beside it', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    const point = pointDrawn(tree, 'model', '2026-10-15')
    expect(point.getAttribute('data-partial')).toBe('true')
    expect(point.getAttribute('data-hollow')).toBe('true')
    // The height is a real one, and it is a share of this account's own peak:
    // 0.30 of 1.25.
    expect(point.getAttribute('data-height')).toBe('24.0%')
    expect(texts(point, 'spend-partial')).toEqual([PARTIAL_MARKER])
    expect(pointDrawn(tree, 'model', '2026-10-12').getAttribute('data-hollow')).toBe('false')
  })

  it('carries the broken line in the markup: one segment count per chart', () => {
    const tree = renderSpend(factory, modelOf(), 'light')
    expect(texts(tree, 'spend-series').length).toBe(3)
    expect(all(tree, 'spend-series').map((series) => series.getAttribute('data-segments'))).toEqual([
      '2',
      '2',
      '1',
    ])
  })

  it('writes every cell as text and sets no attribute that could act', () => {
    // The fake throws on the navigating attribute, `src`, `srcdoc`, `style`,
    // `formaction`, `action` and every `on*` handler, so what is left is
    // exactly the set below: the class hooks a stylesheet keys off, the title
    // an explanation goes in, and `data-` readings.
    const tree = renderSpend(factory, modelOf(), 'light')
    for (const node of walk(tree)) {
      for (const name of node.attributes.keys()) {
        expect(name === 'class' || name === 'title' || name.startsWith('data-'), `${node.tag}[${name}]`).toBe(true)
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
    const key = one(tree, 'spend-key')
    expect(key.textContent).toBe(UNAVAILABLE_MARK)
    expect(one(tree, 'spend-row').getAttribute('data-key')).toBe(UNAVAILABLE_MARK)
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
