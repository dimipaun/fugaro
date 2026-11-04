// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Spend screen's controls: the range picker, the breakdown switch and the
// operator's person-view knob (desktop-console plan Task 2.4; design sections
// 2.3, 5.1 rule 6 and 6.2 screen 2, D15).
//
// Task 2.2 drew all three and wired none of them. This file asserts the
// screen's half of the wiring: one `spend.report` read per change, whose
// `--since`/`--until` come from the picker and whose `--by` comes from the
// switch, through the **existing** `runReport` operation and the spawn table's
// own argv -- no new write, no new channel, and (for that read) no new
// spawn-table row. **Console L5b added one**: the by-person view is a second
// read, `runs stats --json` through `statsRuns`, and the tests below assert the
// argv it is allowed to build -- `--since` and nothing else, never
// `--all-users`, which is the flag that would make this console the one
// deciding who may see whose rows. (The entry point that calls this library is
// Task 3.4c's `entry.ts`'s `showSpend`.)
//
// **The reads here are real spawns.** The reader below is a `CliSource` over
// Task 1.3's stub, and every argument the screen builds goes through the IPC
// allow-list's own `validateArguments` before it reaches the spawn table. What
// the assertions read is **the child's own account** -- the console's opt-in
// spawn log, one JSON line per spawn -- rather than the console's plan object: a
// test that read the plan back would only be asserting that the console is
// self-consistent, and the plan is the thing that can be wrong.
//
// **The person view is off by default** (design D15), and the two consequences
// are asserted here rather than in `spend.test.ts`, which owns the drawing:
// while the knob is off, no spawn carries `--by person`, and the screen refuses
// to build one even if the switch is asked for `person`. The mutation these two
// tests are written against is a default of `on`.

import { existsSync, mkdtempSync, readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { validateArguments } from '../../src/main/ipc'
import { CliSource } from '../../src/source/cli'
import type { JSONDoc } from '../../src/source/source'
import {
  SPEND_DEFAULT_CONTROLS,
  SPEND_DIMENSIONS,
  SPEND_PICKER_WINDOWS,
  buildSpendModel,
  peopleArguments,
  personView,
  readSpend,
  renderSpend,
  spendArguments,
} from '../../src/renderer/screens/spend'
import { drawnRows } from '../support/spend'
import type {
  PeopleReader,
  ScreenElement,
  SpendControls,
  SpendDimension,
  SpendFactory,
  SpendReader,
} from '../../src/renderer/screens/spend'
import { installStub } from './stub'
import type { Stub } from './stub'

const PROJECT = 'fugaro'
const DOLLARS = 1_000_000

/**
 * The day document: a week whose history the report does not hold all of. The
 * range is 2026-10-05..2026-10-11 and three of its seven days have a row, so the
 * other four are **gaps** -- days before the window the history keeps, which
 * `report` leaves out of the document rather than reporting as zero.
 */
const WEEK_DOCUMENT: JSONDoc = {
  project: PROJECT,
  source: 'firestore',
  degraded: false,
  by: 'day',
  since: '2026-10-05',
  until: '2026-10-11',
  weeks: 'iso-utc-monday',
  rows: [
    {
      key: '2026-10-05',
      start: '2026-10-05',
      end: '2026-10-05',
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
      key: '2026-10-06',
      start: '2026-10-06',
      end: '2026-10-06',
      partial: false,
      model_micros: 900_000,
      model_usd: '0.900000',
      notional_micros: null,
      notional_usd: null,
      compute_micros: null,
      compute_usd: null,
      compute_estimated: false,
      run_hours: null,
      runs: 1,
      calls: 9,
    },
    {
      key: '2026-10-11',
      start: '2026-10-11',
      end: '2026-10-11',
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
  ],
  totals: {
    key: 'TOTAL',
    end: '2026-10-11',
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
  },
  warnings: [],
}

/**
 * The person document: the same project grouped by person, one requester an
 * address and one the CLI's own `unknown` bucket. It is the document a
 * `--by person` read answers with, and the only read that answers with it is
 * the one the operator turned the knob on for.
 */
const PERSON_DOCUMENT: JSONDoc = {
  ...WEEK_DOCUMENT,
  by: 'person',
  rows: [
    {
      key: 'ada@example.com',
      end: '2026-10-11',
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
      end: '2026-10-10',
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
  ],
}

/**
 * STATS_DOCUMENT is a `runs stats --json` document (`internal/cli/runs_stats.go`'s
 * `userStatsDoc`, contract section 6.10), in the same window as the report
 * above: the view the CLI resolved, the identity it resolved it for, the
 * installation's own operator line, the window, the read's bound, the rows and
 * the totals, and the runs of the window that never started.
 */
const STATS_DOCUMENT: JSONDoc = {
  project: PROJECT,
  by: 'user',
  view: 'own',
  you: 'ada@example.com',
  operator: false,
  since: '2026-10-05',
  until: '2026-10-11',
  max_runs: 5000,
  truncated: false,
  rows: [
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
  ],
  totals: [
    {
      user: '',
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
  ],
  unlaunched: 0,
  notes: ['these are the runs you started in the window'],
}

/** json is one document as the child prints it. */
function json(document: JSONDoc): string {
  return `${JSON.stringify(document)}\n`
}

/** Wired is one installed console source, the screen's own reader over it, and
 * the two files a test reads the child's account out of. */
interface Wired {
  /** the read the screen makes, as the app will make it: the allow-list's own
   * validator, then the source's one allow-listed `report --json` read */
  readonly read: SpendReader
  /** the by-person read, wired the same way over `runs.stats` (console L5b) */
  readonly readPeople: PeopleReader
  /** the stub, so a test can change what the next read answers with */
  readonly stub: Stub
  /** the console's own spawn log (design section 5.1 rule 10) */
  readonly logPath: string
}

/** wired installs a stub `fugaro` and returns the reader the screen is handed.
 * `FUGARO_CONSOLE_SPAWN_LOG` is set, so every spawn the console makes is
 * recorded beside the stub. */
async function wired(document: JSONDoc = WEEK_DOCUMENT): Promise<Wired> {
  const directory = mkdtempSync(join(tmpdir(), 'fugaro-spend-controls-'))
  const logPath = join(directory, 'spawn.jsonl')
  const stub = installStub(directory, {
    commands: {
      version: { stdout: '1.4.2\n' },
      report: { stdout: json(document) },
      'runs stats': { stdout: json(STATS_DOCUMENT) },
    },
  })
  const source = await CliSource.create({
    target: { project: PROJECT },
    configuredBinaryPath: stub.binary,
    appEnv: { FUGARO_CONSOLE_SPAWN_LOG: logPath },
  })
  return {
    stub,
    logPath,
    read: async (args) => {
      const decision = validateArguments('spend.report', args)
      if (!decision.ok) {
        throw new Error(`the allow-list refused the screen's own arguments: ${decision.reason}`)
      }
      return (await source.runReport(decision.args)).document
    },
    readPeople: async (args) => {
      const decision = validateArguments('runs.stats', args)
      if (!decision.ok) {
        throw new Error(`the allow-list refused the by-person read's own arguments: ${decision.reason}`)
      }
      return (await source.statsRuns(decision.args)).document
    },
  }
}

/** SpawnLogLine is one line of the console's spawn log. */
interface SpawnLogLine {
  readonly argv: readonly string[]
  readonly cwd: string
  readonly project: string
}

/** spawns is the console's own spawn log, read off disk. The `version`
 * handshake is in it too, so the tests below filter by command word. */
function spawns(logPath: string): readonly SpawnLogLine[] {
  if (!existsSync(logPath)) return []
  return readFileSync(logPath, 'utf8')
    .split('\n')
    .filter((line) => line !== '')
    .map((line) => JSON.parse(line) as SpawnLogLine)
}

/** reportSpawns is every `report` the console spawned, in order. */
function reportSpawns(logPath: string): readonly SpawnLogLine[] {
  return spawns(logPath).filter((line) => line.argv[1] === 'report')
}

/** statsSpawns is every `runs stats` the console spawned, in order: the
 * by-person read (console L5b) and nothing else on that command word. */
function statsSpawns(logPath: string): readonly SpawnLogLine[] {
  return spawns(logPath).filter((line) => line.argv[1] === 'runs')
}

/** flagsOf is a child's argv as the flag map a test can compare: every
 * `--flag value` pair, and a lone flag mapped to `null`. `argv[0]` is the
 * binary and the command words are not flags, so neither is in it. */
function flagsOf(argv: readonly string[]): Readonly<Record<string, string | null>> {
  const out: Record<string, string | null> = {}
  for (let index = 0; index < argv.length; index++) {
    const word = argv[index] ?? ''
    if (!word.startsWith('--')) continue
    const value = argv[index + 1]
    if (value === undefined || value.startsWith('--')) out[word] = null
    else {
      out[word] = value
      index++
    }
  }
  return out
}

/** changedFlags is exactly the flags two spawns disagree about, sorted: the
 * assertion "this control changes this flag and nothing else" needs both halves
 * -- the flag that moved, and the absence of any other. */
function changedFlags(before: readonly string[], after: readonly string[]): readonly string[] {
  const one = flagsOf(before)
  const two = flagsOf(after)
  return [...new Set([...Object.keys(one), ...Object.keys(two)])]
    .filter((flag) => one[flag] !== two[flag])
    .sort()
}

/** read is one wired read, asserted to have produced a model. */
async function read(console: Wired, controls: SpendControls) {
  const outcome = await readSpend(console.read, controls, null)
  if (!outcome.ok) throw new Error(`the read refused: ${outcome.reason}`)
  return outcome
}

/** readWith is the same, with the by-person reader wired too: the shape
 * `entry.ts`'s `showSpend` calls `readSpend` in. */
async function readWith(console: Wired, controls: SpendControls) {
  const outcome = await readSpend(console.read, controls, null, console.readPeople)
  if (!outcome.ok) throw new Error(`the read refused: ${outcome.reason}`)
  return outcome
}

// ---------------------------------------------------------------------------
// The range picker and the breakdown switch.
// ---------------------------------------------------------------------------

describe('the spend controls', () => {
  it('opens on the recent week by day, with the person view off', () => {
    // The owner's choice for console 0.118.0: the screen opens on `--since 7d`
    // rather than on the 30 days `report`'s own `--since` defaults to, because
    // the recent week is what an operator looking at spend today is asking
    // about. The read itself is still `report`'s, built from these controls and
    // carrying no flag `report` does not take, and the person view is off (D15).
    expect(SPEND_DEFAULT_CONTROLS).toEqual({ since: '7d', until: null, by: 'day', persons: false })
    // `until` is left to the CLI on purpose: it defaults to today in UTC, and
    // this console does not know what today is -- a date it invented would be a
    // day nobody measured.
    expect(spendArguments(SPEND_DEFAULT_CONTROLS)).toEqual({
      ok: true,
      args: { by: 'day', since: '7d' },
    })
    // Every window the picker offers is one `report --since` itself accepts --
    // the windows the opening one moved past included: the default moved, the
    // picker did not.
    for (const window of SPEND_PICKER_WINDOWS) {
      expect(validateArguments('spend.report', { since: window }).ok).toBe(true)
    }
    expect(SPEND_PICKER_WINDOWS).toEqual(['7d', '30d', '90d', '12w'])
  })

  it('sends the picker’s range as --since and --until, and changes nothing else', async () => {
    const console = await wired()
    await read(console, SPEND_DEFAULT_CONTROLS)
    await read(console, { ...SPEND_DEFAULT_CONTROLS, since: '30d', until: '2026-10-11' })

    const [first, second] = reportSpawns(console.logPath)
    expect(first).toBeDefined()
    expect(second).toBeDefined()
    if (first === undefined || second === undefined) return

    expect(flagsOf(first.argv)).toEqual({ '--json': null, '--by': 'day', '--since': '7d', '--project': PROJECT })
    expect(flagsOf(second.argv)).toEqual({
      '--json': null,
      '--by': 'day',
      '--since': '30d',
      '--until': '2026-10-11',
      '--project': PROJECT,
    })
    // The two spawns differ in the range and in nothing else: the same command
    // words, the same working directory, the same project, the same breakdown.
    expect(second.argv.slice(0, 2)).toEqual(first.argv.slice(0, 2))
    expect(second.cwd).toBe(first.cwd)
    expect(second.project).toBe(first.project)
    expect(changedFlags(first.argv, second.argv)).toEqual(['--since', '--until'])
    // And no read is ever a write: `report --json` is the only command this
    // screen's controls spawn, twice.
    expect(reportSpawns(console.logPath)).toHaveLength(2)
    expect(spawns(console.logPath).map((line) => line.argv[1])).toEqual(['version', 'report', 'report'])
  })

  it('sends the switch’s word as --by, and changes nothing else', async () => {
    const console = await wired()
    await read(console, SPEND_DEFAULT_CONTROLS)
    await read(console, { ...SPEND_DEFAULT_CONTROLS, by: 'repo' })

    const [first, second] = reportSpawns(console.logPath)
    if (first === undefined || second === undefined) throw new Error('two reads, two spawns')
    expect(flagsOf(second.argv)['--by']).toBe('repo')
    expect(changedFlags(first.argv, second.argv)).toEqual(['--by'])
    // Every word of the switch is the same read with a different `--by`, and
    // every one of them is a word the allow-list admits.
    for (const dimension of SPEND_DIMENSIONS) {
      const decision = spendArguments({ ...SPEND_DEFAULT_CONTROLS, by: dimension, persons: true })
      expect(decision.ok).toBe(true)
    }
  })

  it('re-reads from the new document, and a day the history does not hold is a gap', async () => {
    const console = await wired()
    const outcome = await read(console, { ...SPEND_DEFAULT_CONTROLS, since: '30d' })
    // The series is the *document's* range: seven days, four of them with no
    // row. A day before the window the history keeps is a gap and never a row
    // of zeros -- the console does not know those days were free.
    const billed = outcome.model.series.find((series) => series.account === 'model')
    expect(billed?.points.map((point) => point.key)).toEqual([
      '2026-10-05',
      '2026-10-06',
      '2026-10-07',
      '2026-10-08',
      '2026-10-09',
      '2026-10-10',
      '2026-10-11',
    ])
    expect(billed?.points.filter((point) => point.reading.state === 'gap').map((point) => point.key)).toEqual([
      '2026-10-07',
      '2026-10-08',
      '2026-10-09',
      '2026-10-10',
    ])
    // The four missing days draw no bar at all, and no point of them is a
    // figure: the group the chart draws there is a gap.
    for (const day of ['2026-10-07', '2026-10-08', '2026-10-09', '2026-10-10']) {
      const group = outcome.model.chart.groups.find((one) => one.key === day)
      expect(group?.gap).toBe(true)
      expect(group?.bars.every((bar) => bar.share.value === null)).toBe(true)
    }
    for (const point of billed?.points ?? []) {
      if (point.reading.state !== 'gap') continue
      expect(point.reading.value).toBeNull()
    }
  })

  it('never adds ~$ to a real total, on the first read or after a re-read', async () => {
    const console = await wired()
    const first = await read(console, SPEND_DEFAULT_CONTROLS)
    const second = await read(console, { ...SPEND_DEFAULT_CONTROLS, since: '30d' })

    for (const outcome of [first, second]) {
      // The document's own three totals, drawn as three readings: 2.45 billed,
      // 0.50 notional, 0.12 compute. The sum of the first two is the mutation
      // this screen must never draw, and no reading is it.
      expect(outcome.model.totals.model.value).toBe((2_450_000 / DOLLARS))
      expect(outcome.model.totals.notional.value).toBe(500_000 / DOLLARS)
      expect(outcome.model.totals.compute.value).toBe(120_000 / DOLLARS)
      const readings = [
        outcome.model.totals.model,
        outcome.model.totals.notional,
        outcome.model.totals.compute,
        ...outcome.model.series.flatMap((series) => series.points.map((point) => point.reading)),
      ]
      expect(readings.some((reading) => reading.value === 2_450_000 / DOLLARS + 500_000 / DOLLARS)).toBe(false)
    }
    // The re-read is a second spawn, and the model it produced is the second
    // document's: the controls re-read rather than reusing the first screen.
    expect(reportSpawns(console.logPath)).toHaveLength(2)
    expect(second.args).toEqual({ by: 'day', since: '30d' })
  })

  // -------------------------------------------------------------------------
  // The person view (D15).
  // -------------------------------------------------------------------------

  it('carries no --by person, and draws no person row, until the operator turns the knob on', async () => {
    // The mutation this test is written against, named: a default of `on`.
    expect(SPEND_DEFAULT_CONTROLS.persons).toBe(false)

    const console = await wired(WEEK_DOCUMENT)
    const off = await read(console, SPEND_DEFAULT_CONTROLS)
    expect(off.args['by']).toBe('day')
    expect(off.model.persons).toEqual({ enabled: false, withheld: false })
    // No row of the screen is a person, and no address is anywhere on it.
    expect(drawnRows(off.model).map((row) => row.key)).not.toContain('ada@example.com')
    expect(reportSpawns(console.logPath).map((line) => flagsOf(line.argv)['--by'])).toEqual(['day'])
    expect(JSON.stringify(reportSpawns(console.logPath))).not.toContain('person')

    // The knob on: the same read with `by person` and nothing else, and the
    // document that answers it is the one whose rows are drawn.
    console.stub.write({ commands: { report: { stdout: json(PERSON_DOCUMENT) } } })
    const on = await read(console, personView(SPEND_DEFAULT_CONTROLS, true))
    expect(on.args).toEqual({ by: 'person', since: '7d' })
    expect(on.model.persons).toEqual({ enabled: true, withheld: false })
    expect(drawnRows(on.model).map((row) => row.key)).toEqual(['ada@example.com', 'unknown'])

    const [, second] = reportSpawns(console.logPath)
    const [first] = reportSpawns(console.logPath)
    if (first === undefined || second === undefined) throw new Error('two reads, two spawns')
    expect(flagsOf(second.argv)['--by']).toBe('person')
    expect(changedFlags(first.argv, second.argv)).toEqual(['--by'])

    // And the knob off again leaves no read that asks for a person breakdown:
    // the switch's own word goes back to a breakdown that is on.
    const back = personView(personView(SPEND_DEFAULT_CONTROLS, true), false)
    expect(back.persons).toBe(false)
    expect(back.by).toBe('day')
    expect(spendArguments(back)).toEqual({ ok: true, args: { by: 'day', since: '7d' } })
  })

  it('refuses a person read the knob did not ask for, before any spawn', async () => {
    // The other half of the same rule: the switch offering `person` while the
    // knob is off is a state the screen must not be able to turn into a read,
    // however the state was reached.
    const decision = spendArguments({ ...SPEND_DEFAULT_CONTROLS, by: 'person' })
    expect(decision.ok).toBe(false)
    expect(decision.ok ? '' : decision.reason).toContain('off by default')

    const console = await wired(PERSON_DOCUMENT)
    const outcome = await readSpend(console.read, { ...SPEND_DEFAULT_CONTROLS, by: 'person' }, null)
    expect(outcome.ok).toBe(false)
    expect(outcome.ok ? null : outcome.args).toBeNull()
    expect(reportSpawns(console.logPath)).toEqual([])
  })

  // -------------------------------------------------------------------------
  // The by-person read (console L5b).
  // -------------------------------------------------------------------------
  //
  // The by-person view is the screen's second read, and it is the one this task
  // added to the allow-list. What is asserted here is the *argv*: which command
  // words, which flags, and -- the half that matters -- which flags this console
  // can never build. The drawing the document produces is `spend.test.ts`'s.

  it('spawns no by-person read at all while the knob is off', async () => {
    // The mutation this is written against: a read made whatever the knob says,
    // or a panel built from a read the operator never asked for. With the knob
    // off the screen spawns the report once and nothing else.
    const console = await wired()
    const outcome = await readWith(console, SPEND_DEFAULT_CONTROLS)
    expect(outcome.model.people).toBeNull()
    expect(statsSpawns(console.logPath)).toEqual([])
    expect(spawns(console.logPath).map((line) => line.argv.slice(1, 3))).toEqual([
      ['version'],
      ['report', '--json'],
    ])
  })

  it('reads `runs stats --json --since …` once while the knob is on, and asks for nothing else', async () => {
    const console = await wired()
    const outcome = await readWith(console, personView(SPEND_DEFAULT_CONTROLS, true))

    const [spawn] = statsSpawns(console.logPath)
    expect(spawn).toBeDefined()
    if (spawn === undefined) return
    // The command words, then exactly the two flags this console may send:
    // `--json`, and `--since` from the picker's own window.
    expect(spawn.argv.slice(1, 4)).toEqual(['runs', 'stats', '--json'])
    expect(flagsOf(spawn.argv)).toEqual({ '--json': null, '--since': '7d', '--project': PROJECT })
    // **The flags this console must never be able to build.** `--all-users` is
    // the one that would make "who may see whose rows" this window's decision
    // rather than the CLI's; the rest are the flags design section 5.1 rule 6
    // leaves off the operation's schema on purpose (`src/main/ipc.ts`).
    for (const refused of ['--all-users', '--all', '--by', '--repo', '--max-runs', '--until']) {
      expect(spawn.argv, refused).not.toContain(refused)
      expect(JSON.stringify(spawn.argv), refused).not.toContain(refused)
    }
    expect(JSON.stringify(spawns(console.logPath))).not.toContain('all-users')
    // One by-person read, one report read: the knob is one read and not a poll.
    expect(statsSpawns(console.logPath)).toHaveLength(1)
    expect(reportSpawns(console.logPath)).toHaveLength(1)
    // And the document that came back is the one drawn.
    expect(outcome.model.people?.view).toBe('own')
    expect(outcome.model.people?.you).toBe('ada@example.com')
  })

  it('translates the picker’s four windows into the forms `runs stats --since` parses, and never sends a week form', async () => {
    // `report --since` takes `Nd` and `Nw`; `runs stats --since` (parseSince,
    // `internal/cli/runs.go`) takes a day count, a Go duration or `0` and has
    // **no week form at all**. The two grammars are asserted here against the
    // allow-list's own schema, so the translation cannot rot silently.
    const console = await wired()
    const sent: (string | undefined)[] = []
    for (const window of SPEND_PICKER_WINDOWS) {
      const controls = personView({ ...SPEND_DEFAULT_CONTROLS, since: window }, true)
      const decision = peopleArguments(controls)
      expect(decision.ok, window).toBe(true)
      if (decision.ok) {
        expect(validateArguments('runs.stats', decision.args).ok, window).toBe(true)
      }
      await readWith(console, controls)
      sent.push(flagsOf(statsSpawns(console.logPath).at(-1)?.argv ?? [])['--since'] ?? undefined)
    }
    expect(sent).toEqual(['7d', '30d', '90d', '84d'])
    // Twelve weeks is eighty-four days (`reportRange`'s own unit is 7), and the
    // week spelling itself is a value this console must not be able to send:
    // the allow-list refuses it, which is why the table exists.
    expect(sent).not.toContain('12w')
    expect(validateArguments('runs.stats', { since: '12w' }).ok).toBe(false)
    // Every window the picker offers is a `--since` the *report* read accepts
    // too, so neither read is asked for a window the console invented.
    for (const window of SPEND_PICKER_WINDOWS) {
      expect(validateArguments('spend.report', { since: window }).ok, window).toBe(true)
    }
  })

  it('refuses a window the by-person read has no form for, and draws the report anyway', async () => {
    // A day range and a range with an end are the two states `runs stats` cannot
    // be asked about, and the console refuses rather than approximating: a
    // window it silently altered would be drawn under the picker that holds the
    // one the operator chose.
    const console = await wired()
    const cases: readonly (readonly [SpendControls, string])[] = [
      [personView({ ...SPEND_DEFAULT_CONTROLS, since: '2026-10-05' }, true), '2026-10-05'],
      [personView({ ...SPEND_DEFAULT_CONTROLS, until: '2026-10-11' }, true), '2026-10-11'],
    ]
    for (const [controls, value] of cases) {
      const outcome = await readWith(console, controls)
      // The report is the screen's subject: its figures are drawn, and the
      // by-person panel is the one that has no document.
      expect(drawnRows(outcome.model).length, value).toBeGreaterThan(0)
      expect(outcome.model.people?.available, value).toBe(false)
      const reason = outcome.model.people?.reason ?? ''
      expect(reason.length, value).toBeGreaterThan(0)
      expect(reason, value).not.toContain(value)
    }
    // Nothing reached a child for the by-person read, and the report was read
    // once per case -- the refusal is the panel's and not the screen's.
    expect(statsSpawns(console.logPath)).toEqual([])
    expect(reportSpawns(console.logPath)).toHaveLength(cases.length)
  })

  it('draws its own sentence, with no spawn, when the caller wired no by-person reader', async () => {
    // `readSpend`'s fourth parameter is optional, so the three-argument call
    // sites keep working -- and the panel they produce says the truth about
    // itself: this console made no read.
    const console = await wired()
    const outcome = await read(console, personView(SPEND_DEFAULT_CONTROLS, true))
    expect(statsSpawns(console.logPath)).toEqual([])
    expect(outcome.model.people?.available).toBe(false)
    expect(outcome.model.people?.reason).toContain('made no by-person read')
  })

  it('draws the CLI’s own sentence for a refused by-person read, sanitized, beside the report’s figures', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'fugaro-spend-controls-'))
    const logPath = join(directory, 'spawn.jsonl')
    const stub = installStub(directory, {
      commands: {
        version: { stdout: '1.4.2\n' },
        report: { stdout: json(WEEK_DOCUMENT) },
        'runs stats': {
          stderr: 'fugaro: cannot tell who you are, and this view shows your own row alone\n',
          code: 1,
        },
      },
    })
    const source = await CliSource.create({
      target: { project: PROJECT },
      configuredBinaryPath: stub.binary,
      appEnv: { FUGARO_CONSOLE_SPAWN_LOG: logPath },
    })
    const outcome = await readSpend(
      async (args) => (await source.runReport(validateOk(args))).document,
      personView(SPEND_DEFAULT_CONTROLS, true),
      null,
      async (args) => {
        const decision = validateArguments('runs.stats', args)
        if (!decision.ok) throw new Error(decision.reason)
        return (await source.statsRuns(decision.args)).document
      },
    )
    // The screen is drawn -- the report answered -- and the panel carries the
    // CLI's sentence and no rows.
    expect(outcome.ok).toBe(true)
    if (!outcome.ok) return
    expect(outcome.model.people?.available).toBe(false)
    expect(outcome.model.people?.reason).toBe(
      'cannot tell who you are, and this view shows your own row alone',
    )
    expect(statsSpawns(logPath)).toHaveLength(1)
  })

  // -------------------------------------------------------------------------
  // The drawing the wiring reads.
  // -------------------------------------------------------------------------
  //
  // The two tests below draw the screen in plain Node. The e2e's goldens are
  // the real check of the drawing, but they need an Electron and a display;
  // these assert the two facts the *wiring* depends on -- which window the
  // picker marks as current, and what the person knob says -- so a control
  // drawn in a state nothing can read is a failing test and not a screenshot
  // somebody has to notice.

  it('marks the picker’s current window, and marks a day range as a day range', () => {
    const off = renderSpend(factory, buildSpendModel(WEEK_DOCUMENT, null), 'light', SPEND_DEFAULT_CONTROLS)
    expect(texts(off, 'spend-window')).toEqual(['7d', '30d', '90d', '12w'])
    expect(marked(off, 'spend-window')).toEqual(['7d'])
    // Every window is a real button, and `aria-pressed` is the state a reader
    // who cannot see the pressed style is told.
    expect(all(off, 'spend-window').map((one) => one.tag)).toEqual(['button', 'button', 'button', 'button'])
    expect(all(off, 'spend-window').map((one) => one.getAttribute('aria-pressed'))).toEqual([
      'true',
      'false',
      'false',
      'false',
    ])
    const pickerOff = only(off, 'spend-picker')
    expect(pickerOff.getAttribute('data-day')).toBe('false')
    // The end of the range is the CLI's own today unless the operator set one,
    // and the picker says which of the two it is.
    expect(pickerOff.getAttribute('data-until')).toBe('cli')
    // The header draws the *document's* range, not the window asked for: the
    // two are different statements and the screen keeps them apart.
    expect(texts(off, 'spend-since')).toEqual(['2026-10-05'])
    expect(texts(off, 'spend-until')).toEqual(['2026-10-11'])

    // A day range is not one of the four windows, so none of them is marked:
    // the picker does not round the operator's days to the nearest window.
    const day = renderSpend(
      factory,
      buildSpendModel(WEEK_DOCUMENT, null),
      'light',
      { ...SPEND_DEFAULT_CONTROLS, since: '2026-10-05', until: '2026-10-11' },
    )
    expect(marked(day, 'spend-window')).toEqual([])
    expect(all(day, 'spend-window').map((one) => one.getAttribute('aria-pressed'))).toEqual([
      'false',
      'false',
      'false',
      'false',
    ])
    const pickerDay = only(day, 'spend-picker')
    expect(pickerDay.getAttribute('data-day')).toBe('true')
    expect(pickerDay.getAttribute('data-until')).toBe('set')
  })

  it('draws every control as a real button that carries its own hook and its pressed state', () => {
    // The wiring in `entry.ts` reads the hook off the element that was clicked
    // -- the window's own word, the breakdown's `data-dimension`, the knob's
    // `data-enabled` -- so the hook has to be *on* the button and not on a
    // wrapper around it. `aria-pressed` is the same state said in the one way a
    // reader who cannot see the pressed style can hear it.
    const tree = renderSpend(factory, buildSpendModel(WEEK_DOCUMENT, null), 'light', SPEND_DEFAULT_CONTROLS)
    for (const hook of ['spend-window', 'spend-dim', 'spend-persons']) {
      for (const one of all(tree, hook)) {
        const enabled = one.getAttribute('data-enabled')
        if (enabled === 'false' && hook !== 'spend-persons') continue
        expect(one.tag, `${hook}: ${one.textContent ?? ''}`).toBe('button')
        expect(one.getAttribute('type'), `${hook} type`).toBe('button')
        expect(['true', 'false'], `${hook} aria-pressed`).toContain(one.getAttribute('aria-pressed'))
      }
    }
    // The one word this console may not show is not a button at all: a control
    // that cannot be pressed is not drawn as one.
    const disabled = all(tree, 'spend-dim').filter((one) => one.getAttribute('data-enabled') === 'false')
    expect(disabled.map((one) => one.tag)).toEqual(['span'])
    expect(disabled.map((one) => one.textContent)).toEqual(['person'])
    // The window's own word is the whole of its text, which is what the wiring
    // reads back.
    expect(all(tree, 'spend-window').map((one) => one.textContent)).toEqual(['7d', '30d', '90d', '12w'])
  })

  it('says the same true thing everywhere about the person knob: off by default, this session only, not remembered', async () => {
    // D15 asks for a per-installation setting, but nothing persists the knob
    // (it is renderer session state; persisting it needs a write channel this
    // console does not have). So no sentence may claim an installation.
    const off = renderSpend(factory, buildSpendModel(PERSON_DOCUMENT, null), 'light', SPEND_DEFAULT_CONTROLS)
    const on = renderSpend(
      factory,
      buildSpendModel(PERSON_DOCUMENT, null, { persons: true }),
      'light',
      personView(SPEND_DEFAULT_CONTROLS, true),
    )
    const refusal = spendArguments({ ...SPEND_DEFAULT_CONTROLS, by: 'person' })
    const sentences = [
      texts(off, 'spend-persons-note')[0] ?? '',
      texts(on, 'spend-persons-note')[0] ?? '',
      refusal.ok ? '' : refusal.reason,
    ]
    for (const sentence of sentences) {
      expect(sentence).toContain('off by default')
      expect(sentence).toContain('this session only')
      expect(sentence).toContain('not remembered')
      expect(sentence).not.toContain('installation')
    }
    // And the behaviour is what the sentences say: a fresh state is off.
    expect(SPEND_DEFAULT_CONTROLS.persons).toBe(false)
  })

  it('draws the person knob off, with no person row on the screen, until the knob is on', () => {
    // Off: the knob is on the screen and it is off, and the person document's
    // rows are withheld rather than drawn -- the address is not on the screen
    // at all, not even as a key this console redacted after drawing it.
    const off = renderSpend(factory, buildSpendModel(PERSON_DOCUMENT, null), 'light', SPEND_DEFAULT_CONTROLS)
    expect(only(off, 'spend-persons').getAttribute('data-enabled')).toBe('false')
    expect(only(off, 'spend-persons').tag).toBe('button')
    expect(only(off, 'spend-persons').getAttribute('aria-pressed')).toBe('false')
    expect(texts(off, 'spend-persons-state')).toEqual(['off'])
    expect(texts(off, 'spend-persons-note')[0]).toContain('person breakdown is off')
    expect(all(off, 'spend-row')).toEqual([])
    expect(drawnText(off)).not.toContain('ada')

    // On: the same document, and the rows are the document's own keys. The one
    // address is drawn as the console's own marker for it, and the note beside
    // the knob says what turning it on costs.
    const on = renderSpend(
      factory,
      buildSpendModel(PERSON_DOCUMENT, null, { persons: true }),
      'light',
      personView(SPEND_DEFAULT_CONTROLS, true),
    )
    expect(only(on, 'spend-persons').getAttribute('data-enabled')).toBe('true')
    expect(only(on, 'spend-persons').getAttribute('aria-pressed')).toBe('true')
    expect(texts(on, 'spend-persons-state')).toEqual(['on'])
    expect(texts(on, 'spend-persons-note')[0]).toContain('is on for this session')
    expect(all(on, 'spend-row')).toHaveLength(2)
    expect(drawnText(on)).toContain('redacted@example.invalid')
    expect(drawnText(on)).not.toContain('ada')
    // The switch's own word for the grouping is drawn from the document: the
    // knob and the switch agree about what this screen is grouped by.
    expect(texts(on, 'spend-key')).toContain('unknown')
  })

  // -------------------------------------------------------------------------
  // Values the allow-list does not have.
  // -------------------------------------------------------------------------

  it('refuses a window, a day or a breakdown outside its own closed sets, before any spawn', async () => {
    const cases: readonly (readonly [SpendControls, string, string])[] = [
      [{ ...SPEND_DEFAULT_CONTROLS, since: '36h' }, '36h', 'an hours window the picker does not offer'],
      [{ ...SPEND_DEFAULT_CONTROLS, since: '2026-02-31' }, '2026-02-31', 'a day that does not exist'],
      [{ ...SPEND_DEFAULT_CONTROLS, since: '2026-10-11 --csv' }, '2026-10-11 --csv', 'a window with a flag in it'],
      [{ ...SPEND_DEFAULT_CONTROLS, until: '7d' }, '7d', 'a window where a day belongs'],
      [
        { ...SPEND_DEFAULT_CONTROLS, until: '2026-10-11T00:00:00Z' },
        '2026-10-11T00:00:00Z',
        'an instant where a day belongs',
      ],
      [{ ...SPEND_DEFAULT_CONTROLS, by: 'hour' as SpendDimension }, 'hour', 'a breakdown the CLI has no word for'],
      [
        { ...SPEND_DEFAULT_CONTROLS, by: 'wombat' as SpendDimension },
        'wombat',
        'a breakdown this console does not know',
      ],
    ]
    const console = await wired()
    for (const [controls, value, why] of cases) {
      const decision = spendArguments(controls)
      expect(decision.ok, why).toBe(false)
      const outcome = await readSpend(console.read, controls, null)
      expect(outcome.ok, why).toBe(false)
      // The refusal is the console's own sentence: the value it refused is not
      // echoed back into it (the rule the Live screen's unknown flags follow).
      const reason = outcome.ok ? '' : outcome.reason
      expect(reason.length, why).toBeGreaterThan(0)
      expect(reason, why).not.toContain(value)
    }
    // Nothing above reached a child: the picker and the switch are validated
    // before the spawn table is asked for an argv.
    expect(spawns(console.logPath).map((line) => line.argv[1])).toEqual(['version'])
  })

  it('answers a refused read with the CLI’s own sentence, sanitized', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'fugaro-spend-controls-'))
    const stub = installStub(directory, {
      commands: {
        version: { stdout: '1.4.2\n' },
        report: { stderr: 'fugaro: the range is longer than 3660 days\n', code: 1 },
      },
    })
    const source = await CliSource.create({
      target: { project: PROJECT },
      configuredBinaryPath: stub.binary,
      appEnv: {},
    })
    const outcome = await readSpend(
      async (args) => (await source.runReport(validateOk(args))).document,
      SPEND_DEFAULT_CONTROLS,
      null,
    )
    expect(outcome.ok).toBe(false)
    expect(outcome.ok ? '' : outcome.reason).toBe('the range is longer than 3660 days')
  })
})

/** validateOk is `validateArguments` for the read above, which must not be the
 * thing under test there. */
function validateOk(args: Readonly<Record<string, string>>) {
  const decision = validateArguments('spend.report', args)
  if (!decision.ok) throw new Error(decision.reason)
  return decision.args
}

// ---------------------------------------------------------------------------
// The drawn tree, in plain Node.
// ---------------------------------------------------------------------------

/**
 * FakeScreen is a `ScreenElement` that records what it was told and nothing
 * else. It is deliberately permissive: the attribute names a screen may write
 * are `screen-literals.test.ts`'s business (it reads the source), and the tags
 * and classes a screen may draw are `spend.test.ts`'s. What these two tests ask
 * is only what the tree *says*.
 */
class FakeScreen implements ScreenElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  textContent: string | null = null

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
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

const factory: SpendFactory = {
  create: (tag) => new FakeScreen(tag),
  createNS: (tag) => new FakeScreen(tag),
}

/** walk is the whole tree under `node`, in draw order, the root last. */
function walk(node: ScreenElement): readonly FakeScreen[] {
  const out: FakeScreen[] = []
  const visit = (one: ScreenElement): void => {
    for (const child of one.childNodes()) visit(child)
    out.push(one as FakeScreen)
  }
  visit(node)
  return out
}

/** all is every element of the tree carrying `className`. */
function all(node: ScreenElement, className: string): readonly FakeScreen[] {
  return walk(node).filter((one) => (one.getAttribute('class') ?? '').split(/\s+/).includes(className))
}

/** only is the one element of the tree carrying `className`. */
function only(node: ScreenElement, className: string): FakeScreen {
  const found = all(node, className)
  expect(found).toHaveLength(1)
  const [first] = found
  if (first === undefined) throw new Error(`no element carries ${className}`)
  return first
}

/** texts is the text of every element carrying `className`, in draw order. */
function texts(node: ScreenElement, className: string): readonly (string | null)[] {
  return all(node, className).map((one) => one.textContent)
}

/** marked is the text of the `className` elements marked `data-current`. */
function marked(node: ScreenElement, className: string): readonly (string | null)[] {
  return all(node, className)
    .filter((one) => one.getAttribute('data-current') === 'true')
    .map((one) => one.textContent)
}

/** drawnText is everything the tree says, in one string: the assertion "this
 * value is nowhere on the screen" needs the whole of it, not a slot. */
function drawnText(node: ScreenElement): string {
  return walk(node)
    .map((one) => one.textContent ?? '')
    .join('\n')
}


