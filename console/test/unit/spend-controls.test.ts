// The Spend screen's controls: the range picker, the breakdown switch and the
// operator's person-view knob (desktop-console plan Task 2.4; design sections
// 2.3, 5.1 rule 6 and 6.2 screen 2, D15).
//
// Task 2.2 drew all three and wired none of them. This file asserts the wiring:
// one `spend.report` read per change, whose `--since`/`--until` come from the
// picker and whose `--by` comes from the switch, through the **existing**
// `runReport` operation and the spawn table's own argv -- no new write, no new
// spawn-table row and no new channel.
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
  personView,
  readSpend,
  spendArguments,
} from '../../src/renderer/screens/spend'
import type { SpendControls, SpendDimension, SpendReader } from '../../src/renderer/screens/spend'
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
      key: '2026-10-09',
      start: '2026-10-09',
      end: '2026-10-09',
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
      key: '2026-10-10',
      start: '2026-10-10',
      end: '2026-10-10',
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
    commands: { version: { stdout: '1.4.2\n' }, report: { stdout: json(document) } },
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

// ---------------------------------------------------------------------------
// The range picker and the breakdown switch.
// ---------------------------------------------------------------------------

describe('the spend controls', () => {
  it('starts on `report`’s own defaults, with the person view off', () => {
    // The screen opens on the read `report` itself would have made (`--since
    // 30d`, `--by day`) rather than on a window of the console's invention, and
    // the person view is off (D15).
    expect(SPEND_DEFAULT_CONTROLS).toEqual({ since: '30d', until: null, by: 'day', persons: false })
    // `until` is left to the CLI on purpose: it defaults to today in UTC, and
    // this console does not know what today is -- a date it invented would be a
    // day nobody measured.
    expect(spendArguments(SPEND_DEFAULT_CONTROLS)).toEqual({
      ok: true,
      args: { by: 'day', since: '30d' },
    })
    // Every window the picker offers is one `report --since` itself accepts.
    for (const window of SPEND_PICKER_WINDOWS) {
      expect(validateArguments('spend.report', { since: window }).ok).toBe(true)
    }
    expect(SPEND_PICKER_WINDOWS).toContain('30d')
  })

  it('sends the picker’s range as --since and --until, and changes nothing else', async () => {
    const console = await wired()
    await read(console, SPEND_DEFAULT_CONTROLS)
    await read(console, { ...SPEND_DEFAULT_CONTROLS, since: '7d', until: '2026-10-11' })

    const [first, second] = reportSpawns(console.logPath)
    expect(first).toBeDefined()
    expect(second).toBeDefined()
    if (first === undefined || second === undefined) return

    expect(flagsOf(first.argv)).toEqual({ '--json': null, '--by': 'day', '--since': '30d', '--project': PROJECT })
    expect(flagsOf(second.argv)).toEqual({
      '--json': null,
      '--by': 'day',
      '--since': '7d',
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
    // The six other words of the switch are the same read with a different
    // `--by`, and every one of them is a word the allow-list admits.
    for (const dimension of SPEND_DIMENSIONS) {
      const decision = spendArguments({ ...SPEND_DEFAULT_CONTROLS, by: dimension, persons: true })
      expect(decision.ok).toBe(true)
    }
  })

  it('re-reads from the new document, and a day the history does not hold is a gap', async () => {
    const console = await wired()
    const outcome = await read(console, { ...SPEND_DEFAULT_CONTROLS, since: '7d' })
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
      '2026-10-05',
      '2026-10-06',
      '2026-10-07',
      '2026-10-08',
    ])
    // The four missing days break the line, and no point of them is a figure.
    expect(billed?.segments).toBe(2)
    for (const point of billed?.points ?? []) {
      if (point.reading.state !== 'gap') continue
      expect(point.reading.value).toBeNull()
    }
  })

  it('never adds NOTIONAL~ to a real total, on the first read or after a re-read', async () => {
    const console = await wired()
    const first = await read(console, SPEND_DEFAULT_CONTROLS)
    const second = await read(console, { ...SPEND_DEFAULT_CONTROLS, since: '7d' })

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
    expect(second.args).toEqual({ by: 'day', since: '7d' })
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
    expect(off.model.rows.map((row) => row.key)).not.toContain('ada@example.com')
    expect(reportSpawns(console.logPath).map((line) => flagsOf(line.argv)['--by'])).toEqual(['day'])
    expect(JSON.stringify(reportSpawns(console.logPath))).not.toContain('person')

    // The knob on: the same read with `by person` and nothing else, and the
    // document that answers it is the one whose rows are drawn.
    console.stub.write({ commands: { report: { stdout: json(PERSON_DOCUMENT) } } })
    const on = await read(console, personView(SPEND_DEFAULT_CONTROLS, true))
    expect(on.args).toEqual({ by: 'person', since: '30d' })
    expect(on.model.persons).toEqual({ enabled: true, withheld: false })
    expect(on.model.rows.map((row) => row.key)).toEqual(['ada@example.com', 'unknown'])

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
    expect(spendArguments(back)).toEqual({ ok: true, args: { by: 'day', since: '30d' } })
  })

  it('refuses a person read the knob did not ask for, before any spawn', async () => {
    // The other half of the same rule: the switch offering `person` while the
    // knob is off is a state the screen must not be able to turn into a read,
    // however the state was reached.
    const decision = spendArguments({ ...SPEND_DEFAULT_CONTROLS, by: 'person' })
    expect(decision.ok).toBe(false)
    expect(decision.ok ? '' : decision.reason).toContain('off for this installation')

    const console = await wired(PERSON_DOCUMENT)
    const outcome = await readSpend(console.read, { ...SPEND_DEFAULT_CONTROLS, by: 'person' }, null)
    expect(outcome.ok).toBe(false)
    expect(outcome.ok ? null : outcome.args).toBeNull()
    expect(reportSpawns(console.logPath)).toEqual([])
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
