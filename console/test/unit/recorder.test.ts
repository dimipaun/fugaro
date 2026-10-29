// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The recorder, held to its own contract (desktop-console plan Task 1.4).
//
// **The tool is agent-written and the recordings are owner-run**, and this file
// is why the agent half is reviewable: the recorder is the one piece of console
// code whose whole job is to *not write something*, so what it refuses is as
// much a part of it as what it captures. Three things are asserted:
//
//   - **its refusals.** A recording nobody asked for is worse than no recording:
//     no `--project` is refused rather than defaulted (design section 3.4 rule
//     3), the `belong` project and anything under `~/.config/fugaro` are refused
//     by name, an `--out` outside `test/fixtures/` is refused, and an existing
//     fixture is not overwritten without `--force`. A recorder whose refusals
//     are untested is a recorder that can be talked into recording the wrong
//     installation.
//   - **the spawn it builds.** It is `planFor`'s, not a second copy of it: the
//     argv is `watch --json` with `--project`, the child environment is
//     `CHILD_ENV_KEYS` and nothing else, and the app's own environment (a token
//     in it, say) does not reach the child.
//   - **what a recording in progress does with stdout.** Every line is stamped
//     with the console's clock and redacted, a sentence becomes a *notice*, a
//     blank line nothing, a torn last line nothing, and a frame the redactor
//     will not vouch for ends the recording with **nothing written**. The child
//     stub and the fake clock below are what let that be observed without
//     spawning anything: the recorder takes its spawner, its clock and its
//     timers as arguments.

import { lstatSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { afterEach, describe, expect, it } from 'vitest'

import { CHILD_ENV_KEYS, SHUTDOWN_GRACE_MS } from '../../src/main/spawn'
import type { ChildSpawner, ReadableLike, SpawnedChildLike, Timers } from '../../src/main/spawn'
import { FIXTURE_CASES, FIXTURE_DIRECTORY, FixtureRefusal, loadFixture } from '../../src/source/fixture'
import type { FixtureRecord } from '../../src/source/fixture'
import { MAX_REDACTABLE_LINE_LENGTH, PROJECT_MARKER, RedactionRefusal, createRedactor } from '../../src/source/redact'
import type { Redactor } from '../../src/source/redact'
import {
  BELONG_PROJECT,
  DEFAULT_DURATION_SECONDS,
  RecorderRefusal,
  USAGE,
  main,
  planForRecording,
  parseRecorderArgs,
  recordLine,
  recorderLiterals,
  recorderMeta,
  renderRecording,
  startRecording,
  writeFixtureFile,
} from '../../tools/record-fixture/index'
import type { RecorderOptions } from '../../tools/record-fixture/index'

const CWD = '/home/operator/src/fugaro/console'
const OUT = `${FIXTURE_DIRECTORY}/normal.ndjson`

/** opts parses one invocation against a fixed checkout, with nothing existing
 * yet -- the shape most of the checks below start from. */
function opts(...argv: readonly string[]): RecorderOptions {
  return parseRecorderArgs([...argv], CWD, { exists: () => false, consoleRoot: CWD })
}

/** full is an invocation that passes every check. */
function full(...extra: readonly string[]): RecorderOptions {
  return opts('--out', OUT, '--project', 'sandbox', '--gcp-project', 'fixture-gcp-1', ...extra)
}

/** refusal runs `run` and returns the sentence it refused with. */
function refusal(run: () => unknown): string {
  try {
    run()
  } catch (error) {
    if (error instanceof RecorderRefusal) return error.message
    throw error
  }
  throw new Error('expected a RecorderRefusal, and the call returned')
}

/** redactor is the redactor a recording of this invented installation would get:
 * the project name as a literal, which has no shape for a pattern class. */
function redactor(): Redactor {
  return createRedactor(['fixture-sandbox', 'fixture-operator'])
}

/** StubStream is a child's stdout as a test drives it. */
class StubStream implements ReadableLike {
  readonly #handlers: ((chunk: Buffer | string) => void)[] = []

  /** listeners is how many readers the recorder attached: an undrained pipe is
   * the bug this count is here to catch. */
  get listeners(): number {
    return this.#handlers.length
  }

  on(_event: 'data', listener: (chunk: Buffer | string) => void): unknown {
    this.#handlers.push(listener)
    return this
  }

  write(text: string): void {
    for (const handler of this.#handlers) handler(text)
  }
}

/** StubChild is a spawned CLI: it records the signals it was sent, and a test
 * decides whether it exits. */
class StubChild implements SpawnedChildLike {
  readonly stdout: ReadableLike = new StubStream()
  readonly stderr: ReadableLike = new StubStream()
  readonly signals: NodeJS.Signals[] = []
  readonly #exits: ((code: number | null, signal: string | null) => void)[] = []
  readonly #closes: ((code: number | null, signal: string | null) => void)[] = []
  readonly #errors: ((error: Error) => void)[] = []
  #alive = true

  kill(signal: NodeJS.Signals = 'SIGTERM'): boolean {
    if (!this.#alive) return false
    this.signals.push(signal)
    // A child that is SIGKILLed is gone; one that is SIGTERMed may yet exit on
    // its own, and that difference is what the grace timer is for.
    // Node then reports the exit, and the close once the pipes have drained.
    if (signal === 'SIGKILL') {
      this.#alive = false
      for (const listener of this.#exits) listener(null, signal)
      for (const listener of this.#closes) listener(null, signal)
    }
    return true
  }

  on(event: 'spawn', listener: () => void): unknown
  on(event: 'error', listener: (error: Error) => void): unknown
  on(event: 'exit' | 'close', listener: (code: number | null, signal: string | null) => void): unknown
  on(
    event: 'spawn' | 'error' | 'exit' | 'close',
    listener: (() => void) | ((error: Error) => void) | ((code: number | null, signal: string | null) => void),
  ): unknown {
    // `spawn` is fired at registration, as in `spawn.test.ts`'s fakes: Node
    // emits it on the tick after the process exists, which for these tests is
    // the same ordering -- a spawn-log line before the child's first byte --
    // without a tick to wait out.
    if (event === 'spawn') (listener as () => void)()
    else if (event === 'error') this.#errors.push(listener as (error: Error) => void)
    else if (event === 'exit') this.#exits.push(listener as (code: number | null, signal: string | null) => void)
    else this.#closes.push(listener as (code: number | null, signal: string | null) => void)
    return this
  }

  /** exit is the child going away by itself, its pipes drained: `exit`, then
   * `close`, as Node reports them. */
  exit(code: number | null): void {
    this.exitOnly(code)
    this.close(code)
  }

  /** exitOnly is the process gone with its pipes still holding output: Node may
   * emit `exit` before the last stdout chunk, and `close` only after it. */
  exitOnly(code: number | null): void {
    this.#alive = false
    for (const listener of this.#exits) listener(code, null)
  }

  /** close is the pipes drained after an `exitOnly`. */
  close(code: number | null): void {
    for (const listener of this.#closes) listener(code, null)
  }

  /** fail is a spawn that never started (`ENOENT`). */
  fail(error: Error): void {
    this.#alive = false
    for (const listener of this.#errors) listener(error)
  }

  write(text: string): void {
    ;(this.stdout as StubStream).write(text)
  }

  /** writeErr is the child talking on stderr, which the recorder drains rather
   * than records. */
  writeErr(text: string): void {
    ;(this.stderr as StubStream).write(text)
  }
}

/** FakeTimers is a `Timers` a test drives: it records what was scheduled and
 * when, and `advanceTo` runs everything due, in order. */
class FakeTimers implements Timers {
  #now = 0
  #next = 1
  readonly #due: { id: number; at: number; handler: () => void }[] = []

  get scheduled(): readonly number[] {
    return this.#due.map((timer) => timer.at)
  }

  setTimeout(handler: () => void, ms: number): ReturnType<typeof setTimeout> {
    const id = this.#next
    this.#next += 1
    this.#due.push({ id, at: this.#now + ms, handler })
    return id as unknown as ReturnType<typeof setTimeout>
  }

  clearTimeout(handle: ReturnType<typeof setTimeout>): void {
    const id = handle as unknown as number
    const index = this.#due.findIndex((timer) => timer.id === id)
    if (index >= 0) this.#due.splice(index, 1)
  }

  advanceTo(at: number): void {
    for (;;) {
      const index = this.#due.findIndex((timer) => timer.at <= at)
      if (index < 0) break
      const [timer] = this.#due.splice(index, 1)
      if (timer === undefined) break
      this.#now = timer.at
      timer.handler()
    }
    this.#now = Math.max(this.#now, at)
  }
}

/** started is a recording in progress over a stub child, a fake clock and a
 * fake timer queue. */
function started(
  options: RecorderOptions = full('--duration', '30'),
  timers = new FakeTimers(),
  stub = new StubChild(),
  useRedactor: Redactor = redactor(),
): { stub: StubChild; timers: FakeTimers; recording: ReturnType<typeof startRecording> } {
  const recording = startRecording({
    options,
    plan: planForRecording(options, {}),
    redactor: useRedactor,
    spawner: (() => stub) as ChildSpawner,
    timers,
    clock: () => new Date('2026-10-15T12:00:00Z'),
  })
  return { stub, timers, recording }
}

describe("the recorder's arguments", () => {
  it('refuses an invocation that would record the wrong thing', () => {
    // Each of these is a recording nobody asked for. They are one test because
    // they are one property -- the recorder refuses rather than guesses -- and
    // the sentence is asserted, because the sentence is what the owner reads.
    const cases: [readonly string[], RegExp][] = [
      [[], /--out is required/],
      [['--out', OUT], /--project is required/],
      [['--out', OUT, '--project', BELONG_PROJECT], /names belong, which this recorder never touches/],
      [['--out', OUT, '--project', 'sandbox', '--dir', '/home/operator/src/belong'], /--dir names belong/],
      [['--out', OUT, '--project', 'Belong'], /--project names belong/],
      [
        ['--out', OUT, '--project', 'sandbox', '--config', '/Users/op/.config/fugaro/config.json'],
        /--config names .config\/fugaro/,
      ],
      [['--out', '/tmp/normal.ndjson', '--project', 'sandbox'], /must be written under test\/fixtures\//],
      [['--out', `${FIXTURE_DIRECTORY}/normal.json`, '--project', 'sandbox'], /must name a .ndjson fixture/],
      [['--out', OUT, '--project', 'sandbox', '--wibble', 'x'], /--wibble is not a recorder flag/],
      [['--out', OUT, '--project'], /--project needs a value/],
      [['--out', OUT, '--project', 'sandbox', '--duration', '0'], /positive whole number of seconds/],
      [['--out', OUT, '--project', 'sandbox', '--duration', '2.5'], /positive whole number of seconds/],
      // A case the design does not name would be written as
      // `test/fixtures/wibble.ndjson` -- a file `fixture.test.ts` then refuses,
      // so the refusal would land on whoever replays it instead of on the
      // invocation that made it. `synthetic` is named and still refused: a
      // recording is never synthetic, and this tool's header says so.
      [['--out', OUT, '--project', 'sandbox', '--case', 'wibble'], /--case must name one of the design's recorded cases/],
      [['--out', OUT, '--project', 'sandbox', '--case', 'synthetic'], /--case must name one of the design's recorded cases/],
      [['--out', OUT, '--project', 'sandbox', '--case', 'Normal'], /--case must name one of the design's recorded cases/],
      [[`${FIXTURE_DIRECTORY}/normal.ndjson`], /unexpected argument/],
    ]
    for (const [argv, expected] of cases) {
      expect(refusal(() => opts(...argv)), JSON.stringify(argv)).toMatch(expected)
    }
    // The `--case` refusal is the one an owner meets while following the plan's
    // own list, so it prints the list rather than only the value it rejected.
    expect(refusal(() => full('--case', 'wibble'))).toContain(FIXTURE_CASES.join(', '))
  })

  it('prints the one documented command when it is asked for help', () => {
    // The README documents this command, and the recorder's own usage line is
    // where a reader lands when they get it wrong.
    const message = refusal(() => opts('--help'))
    expect(message).toBe(USAGE)
    expect(message).toContain(`npm run record-fixture -- --out ${FIXTURE_DIRECTORY}/<name>.ndjson --project <project>`)
    expect(message).toContain(`default: ${String(DEFAULT_DURATION_SECONDS)}`)
  })

  it('never overwrites a fixture without --force', () => {
    const existing = (path: string): boolean => path === `${CWD}/${OUT}`
    expect(
      refusal(() =>
        parseRecorderArgs(['--out', OUT, '--project', 'sandbox', '--gcp-project', 'fixture-gcp-1'], CWD, {
          exists: existing,
          consoleRoot: CWD,
        }),
      ),
    ).toMatch(/exists; pass --force/)
    const forced = parseRecorderArgs(
      ['--out', OUT, '--project', 'sandbox', '--gcp-project', 'fixture-gcp-1', '--force'],
      CWD,
      { exists: existing, consoleRoot: CWD },
    )
    expect(forced.force).toBe(true)
    expect(full().force).toBe(false)
    // ...and the inline form every other flag accepts means the same thing.
    // The review of Task 3.2 found `--force=true` refused with "`--force` is not
    // a recorder flag": fail-closed, but a sentence that names the wrong reason
    // -- it contradicted this file's own usage text, which documents `--force`
    // -- and left the operator to guess. `--force=false` is the default, so the
    // inline form reads as what it says.
    const inline = (token: string): { force: boolean } =>
      parseRecorderArgs(['--out', OUT, '--project', 'sandbox', '--gcp-project', 'fixture-gcp-1', token], CWD, {
        consoleRoot: CWD,
      })
    expect(inline('--force=true').force).toBe(true)
    expect(inline('--force=false').force).toBe(false)
    // A value that is neither is refused for what it is: a switch with a value
    // it does not take, and not a flag that does not exist.
    expect(refusal(() => inline('--force=maybe'))).toMatch(/--force takes true or false, not "maybe"/)
    expect(refusal(() => inline('--force=maybe'))).not.toMatch(/is not a recorder flag/)
    // The check is on the resolved path rather than the spelling: `--out` given
    // from another directory still cannot land on a fixture that is there.
    expect(
      refusal(() =>
        parseRecorderArgs(
          ['--out', './test/fixtures/x.ndjson', '--project', 'sandbox', '--gcp-project', 'fixture-gcp-1'],
          '/a/b/console',
          { exists: () => true, consoleRoot: '/a/b/console' },
        ),
      ),
    ).toMatch(/\/a\/b\/console\/test\/fixtures\/x\.ndjson exists/)
  })

  it('accepts an invocation, resolving the paths it was given', () => {
    const options = full(
      '--duration',
      '45',
      '--case',
      'repo-at-cap',
      '--repo',
      'fixture-owner/fixture-repo',
      '--binary',
      '/opt/homebrew/bin/fugaro',
      '--config',
      '/tmp/fugaro-config.json',
    )
    expect(options).toEqual({
      out: `${CWD}/${OUT}`,
      project: 'sandbox',
      configPath: '/tmp/fugaro-config.json',
      binaryPath: '/opt/homebrew/bin/fugaro',
      directory: CWD,
      caseName: 'repo-at-cap',
      durationSeconds: 45,
      repo: 'fixture-owner/fixture-repo',
      force: false,
      gcpProject: 'fixture-gcp-1',
      projectNumber: undefined,
      literals: [],
    })
    // `--dir` is resolved against the current directory too, so a relative
    // checkout is a plan the spawner can use.
    expect(full('--dir', '../sandbox').directory).toBe('/home/operator/src/fugaro/sandbox')
    // `--name=value` is the same flag, and the defaults are the documented ones.
    const inline = opts(`--out=${OUT}`, '--project=sandbox', '--gcp-project=fixture-gcp-1', '--duration=30')
    expect(inline.project).toBe('sandbox')
    expect(inline.durationSeconds).toBe(30)
    expect(full()).toMatchObject({
      caseName: 'normal',
      durationSeconds: DEFAULT_DURATION_SECONDS,
      binaryPath: 'fugaro',
    })
  })
})

describe("the recorder's own spawn", () => {
  it("is the console's live.watch plan, --project and all", () => {
    const plan = planForRecording(full(), {})
    expect(plan.argv).toEqual(['fugaro', 'watch', '--json', '--project', 'sandbox'])
    expect(plan.cwd).toBe(CWD)
    expect(plan.project).toBe('sandbox')
    // `--repo`, when the owner narrows the recording, is the declared argument
    // and keeps its place before the installation flags.
    expect(planForRecording(full('--repo', 'fixture-owner/fixture-repo'), {}).argv).toEqual([
      'fugaro',
      'watch',
      '--json',
      '--repo',
      'fixture-owner/fixture-repo',
      '--project',
      'sandbox',
    ])
    expect(planForRecording(full('--config', '/tmp/fugaro-config.json'), {}).argv).toEqual([
      'fugaro',
      'watch',
      '--json',
      '--project',
      'sandbox',
      '--config',
      '/tmp/fugaro-config.json',
    ])
  })

  it('gives the child CHILD_ENV_KEYS and nothing else', () => {
    // The recorder adds no variable of its own and inherits none: the app's own
    // environment can hold a token, and the child must not be able to see it.
    const plan = planForRecording(full(), {
      PATH: '/usr/bin:/bin',
      HOME: '/home/operator',
      ANTHROPIC_API_KEY: 'sk-ant-not-a-real-key',
      AWS_SECRET_ACCESS_KEY: 'not-a-real-secret',
      FUGARO_PROJECT: 'somewhere-else',
    })
    for (const key of Object.keys(plan.env)) expect(CHILD_ENV_KEYS).toContain(key)
    const serialised = JSON.stringify(plan.env)
    expect(serialised).not.toContain('ANTHROPIC_API_KEY')
    expect(serialised).not.toContain('not-a-real-key')
    expect(serialised).not.toContain('AWS_SECRET_ACCESS_KEY')
    expect(plan.env['FUGARO_PROJECT']).toBe('sandbox')
    expect(plan.env['HOME']).toBe('/home/operator')
    expect(plan.env['PATH']).toContain('/usr/bin')
  })

  it('tells the redactor every name the installation is known by', () => {
    const literals = recorderLiterals(
      full('--repo', 'fixture-owner/fixture-repo', '--config', '/tmp/fugaro.json'),
      { HOSTNAME: 'operators-macbook.local', USER: 'operator', HOME: '/home/operator' },
    )
    expect(literals).toContain('sandbox')
    expect(literals).toContain('/tmp/fugaro.json')
    expect(literals).toContain('operators-macbook.local')
    expect(literals).toContain('operator')
    expect(literals).toContain('/home/operator')
    expect(literals).toContain('fixture-owner/fixture-repo')
    // The default binary is the literal string `fugaro`, which is a command name
    // and not a name of this installation: redacting it would take the CLI's own
    // name out of every sentence the degraded mode writes.
    expect(recorderLiterals(full(), {})).not.toContain('fugaro')
    expect(recorderLiterals(full('--binary', '/opt/homebrew/bin/fugaro'), {})).toContain(
      '/opt/homebrew/bin/fugaro',
    )
    // No empty string: `''` is a literal that would match everywhere.
    expect(recorderLiterals(full(), {})).not.toContain('')
  })
})

describe('one line of a recording', () => {
  const at = '2026-10-15T12:00:00Z'

  it('redacts a document, stamps it, and keeps a sentence as a notice', () => {
    const document = recordLine(
      '{"project":"fixture-sandbox","runs":[{"repo":"fixture-owner/fixture-repo","requested_by":"operator@example.com"}]}',
      at,
      redactor(),
    )
    expect(document).not.toBeNull()
    if (document === null || !('document' in document)) throw new Error('expected a document record')
    const body = document.document as { project: string; runs: Record<string, string>[] }
    expect(document.at).toBe(at)
    expect(body.project).toBe(PROJECT_MARKER)
    expect(body.runs[0]?.['repo']).toMatch(/^r-[0-9a-f]{8}\/r-[0-9a-f]{8}$/)
    expect(body.runs[0]?.['repo']).not.toContain('fixture-owner')
    expect(body.runs[0]?.['requested_by']).toBe('redacted@example.invalid')
    // The degraded mode writes a person-readable sentence on the same stream,
    // and it is the header warning a replay has to be able to show.
    expect(recordLine('no budget backend: showing run records only', at, redactor())).toEqual({
      at,
      notice: 'no budget backend: showing run records only',
    })
    expect(recordLine('retrying for operator@example.com', at, redactor())).toEqual({
      at,
      notice: 'retrying for redacted@example.invalid',
    })
    // A blank line is nothing at all; an array and a bare scalar are notices by
    // `decodeFeedLine`'s rule, and never a parse error.
    expect(recordLine('', at, redactor())).toBeNull()
    expect(recordLine('   ', at, redactor())).toBeNull()
    expect(recordLine('[{"run":"x"}]', at, redactor())).toEqual({ at, notice: '[{"run":"x"}]' })
    expect(recordLine('{"run": "unterminated', at, redactor())).toEqual({
      at,
      notice: '{"run": "unterminated',
    })
  })

  it('refuses rather than records when the redactor will not vouch for a frame', () => {
    // A redactor with a bug in one rule: the recorder's half of the fail-closed
    // promise is that a frame it cannot vouch for never becomes a record.
    const broken: Redactor = {
      ...redactor(),
      redactText: () => {
        throw new RedactionRefusal('the audit would not vouch for this sentence')
      },
    }
    expect(() => recordLine('a sentence', at, broken)).toThrow(RedactionRefusal)
  })
})

describe('the header of a recording', () => {
  it('says it was recorded, names the case, and carries the project as a marker', () => {
    const meta = recorderMeta(full('--case', 'normal'), new Date('2026-10-15T12:00:00.500Z'))
    expect(meta.case).toBe('normal')
    expect(meta.project).toBe(PROJECT_MARKER)
    expect(meta.synthetic).toBe(false)
    expect(meta.recorded).toBe('2026-10-15T12:00:00Z')
    expect(meta.note).toContain('recorded on 2026-10-15T12:00:00Z')
    expect(meta.note).not.toContain('synthetic:')
    // `recorded` is the one field a synthetic fixture can never have, and a
    // recording carries it; `cli` is present only when the caller knows it.
    expect(recorderMeta(full(), new Date('2026-10-15T12:00:00Z'), '0.6.0').cli).toBe('0.6.0')
    expect(recorderMeta(full(), new Date('2026-10-15T12:00:00Z')).cli).toBeUndefined()
  })

  it('verifies the whole fixture before a byte is written', () => {
    const records: FixtureRecord[] = [
      { at: '2026-10-15T12:00:00Z', document: { project: PROJECT_MARKER } },
      { at: '2026-10-15T12:00:01Z', notice: 'a quiet minute' },
    ]
    const text = renderRecording(records, recorderMeta(full(), new Date('2026-10-15T12:00:01Z')), 'out.ndjson')
    const loaded = loadFixture(text, 'out.ndjson')
    expect(loaded.records).toHaveLength(2)
    expect(loaded.header.synthetic).toBe(false)
    expect(loaded.header.case).toBe('normal')
    // And a record the redactor should have altered is refused *here*, which is
    // the point of rendering through the loader: the file is never written.
    const planted: FixtureRecord[] = [
      { at: '2026-10-15T12:00:00Z', document: { runs: [{ title: 'ask operator@example.com' }] } },
    ]
    expect(() => renderRecording(planted, recorderMeta(full(), new Date()), 'out.ndjson')).toThrow(FixtureRefusal)
  })
})

describe('a recording in progress', () => {
  it('turns stdout into redacted records, and drops a torn last line', async () => {
    const { stub, timers, recording } = started()
    stub.write('{"project":"fixture-sandbox","day":"2026-10-15"}\n')
    stub.write('\n')
    stub.write('no budget backend: showing run records only\n')
    // A write cut in half by the signal is not a frame, and recording it as a
    // notice would put garbage in the fixture.
    stub.write('{"project":"fixture-sandbox"')
    timers.advanceTo(30_000)
    timers.advanceTo(30_000 + SHUTDOWN_GRACE_MS)
    const result = await recording.finished
    expect(result.refusal).toBeNull()
    expect(result.records).toEqual([
      { at: '2026-10-15T12:00:00Z', document: { project: PROJECT_MARKER, day: '2026-10-15' } },
      { at: '2026-10-15T12:00:00Z', notice: 'no budget backend: showing run records only' },
    ])
    expect(JSON.stringify(result.records)).not.toContain('fixture-sandbox')
  })

  it("drains the child's stderr, and never records a byte of it", async () => {
    const { stub, timers, recording } = started()
    stub.write('{"project":"fixture-sandbox","day":"2026-10-15"}\n')
    // Drained: `planFor` gives the child a stderr pipe, and a pipe nobody reads
    // fills up and blocks the child's write, so a CLI that logs would stall the
    // recording. This is the assertion that fails if the drain is removed.
    expect((stub.stderr as StubStream).listeners).toBe(1)
    // And discarded: a stderr line is not a feed frame, so it is neither echoed
    // nor turned into a *notice* record -- the fixture is stdout's alone, and
    // whatever a CLI writes to stderr cannot reach the committed file.
    stub.writeErr('warn: fixture-token at /home/fixture-operator/secrets.yaml\n')
    timers.advanceTo(30_000)
    timers.advanceTo(30_000 + SHUTDOWN_GRACE_MS)
    const result = await recording.finished
    expect(result.refusal).toBeNull()
    expect(result.records).toEqual([
      { at: '2026-10-15T12:00:00Z', document: { project: PROJECT_MARKER, day: '2026-10-15' } },
    ])
    const written = JSON.stringify(result.records)
    expect(written).not.toContain('fixture-token')
    expect(written).not.toContain('fixture-operator')
  })

  it('stops the child at the end of the duration: SIGTERM, then SIGKILL', async () => {
    const { stub, timers, recording } = started(full('--duration', '30'))
    stub.write('{"day":"2026-10-15"}\n')
    expect(stub.signals).toEqual([])
    // The duration is the duration: nothing is killed early...
    timers.advanceTo(29_999)
    expect(stub.signals).toEqual([])
    // ...and at the deadline the child is asked to stop, with the grace the
    // console gives its own children before the SIGKILL.
    timers.advanceTo(30_000)
    expect(stub.signals).toEqual(['SIGTERM'])
    expect(timers.scheduled).toContain(30_000 + SHUTDOWN_GRACE_MS)
    timers.advanceTo(30_000 + SHUTDOWN_GRACE_MS)
    expect(stub.signals).toEqual(['SIGTERM', 'SIGKILL'])
    const result = await recording.finished
    expect(result.exitCode).toBeNull()
    expect(result.records).toHaveLength(1)
    // The timers are cleaned up, so a recording that is over holds nothing open.
    expect(timers.scheduled).toEqual([])
  })

  it('ends early when it is asked to, the way Ctrl-C does', async () => {
    const { stub, timers, recording } = started()
    stub.write('{"day":"2026-10-15"}\n')
    recording.stop()
    expect(stub.signals).toEqual(['SIGTERM'])
    // A second stop is not a second signal: the recording is already ending.
    recording.stop()
    expect(stub.signals).toEqual(['SIGTERM'])
    // The SIGKILL still follows if the child does not go on its own, and the
    // records read up to the stop are kept.
    timers.advanceTo(SHUTDOWN_GRACE_MS)
    expect(stub.signals).toEqual(['SIGTERM', 'SIGKILL'])
    expect((await recording.finished).records).toHaveLength(1)
  })

  it('reports a refusal, and the child is stopped rather than left running', async () => {
    const broken: Redactor = {
      ...redactor(),
      redactText: () => {
        throw new RedactionRefusal('the audit would not vouch for this sentence')
      },
    }
    const { stub, timers, recording } = started(undefined, undefined, undefined, broken)
    stub.write('{"day":"2026-10-15"}\n')
    stub.write('a sentence the redactor refuses\n')
    expect(stub.signals).toEqual(['SIGTERM'])
    timers.advanceTo(SHUTDOWN_GRACE_MS)
    const result = await recording.finished
    expect(result.refusal).toBeInstanceOf(RedactionRefusal)
    // The records read before the refusal are kept in the result, and `main`
    // refuses to write any of them -- see the test below.
    expect(result.records).toHaveLength(1)
  })

  it("reports the child's own exit, so a CLI that refused to start is visible", async () => {
    const { stub, recording } = started()
    stub.write('{"day":"2026-10-15"}\n')
    stub.exit(1)
    const result = await recording.finished
    expect(result.exitCode).toBe(1)
    expect(result.records).toHaveLength(1)
  })

  it('reads until the pipes close, not until the process exits', async () => {
    // Review round 3 of PR 478: the recording settled on `exit`, which Node may
    // emit before the last stdout chunk, and a chunk after it was dropped.
    const { stub, recording } = started()
    stub.write('{"day":"2026-10-15"}\n')
    stub.exitOnly(0)
    stub.write('{"day":"2026-10-16"}\n')
    stub.write('{"day":"2026-10-17"}')
    stub.close(0)
    const result = await recording.finished
    expect(result.exitCode).toBe(0)
    expect(result.records).toHaveLength(3)
  })

  it('still ends a recording whose pipes never close after the SIGKILL', async () => {
    // A grandchild holding the pipe open would keep `close` from ever coming;
    // the recording ends one grace period after the SIGKILL anyway.
    const stub = new StubChild()
    stub.kill = (signal: NodeJS.Signals = 'SIGTERM'): boolean => {
      stub.signals.push(signal)
      return true
    }
    const { timers, recording } = started(full('--duration', '30'), new FakeTimers(), stub)
    stub.write('{"day":"2026-10-15"}\n')
    timers.advanceTo(30_000 + SHUTDOWN_GRACE_MS)
    expect(stub.signals).toEqual(['SIGTERM', 'SIGKILL'])
    timers.advanceTo(30_000 + 2 * SHUTDOWN_GRACE_MS)
    const result = await recording.finished
    expect(result.records).toHaveLength(1)
    expect(timers.scheduled).toEqual([])
  })

  it('keeps reading after the SIGKILL until the pipes close', async () => {
    // Review round 4 of PR 478: the SIGKILL is not the end of the output. Node
    // reports the exit, and the chunks still in the pipe arrive before `close`;
    // a recording that settled at the signal would drop them.
    const stub = new StubChild()
    stub.kill = (signal: NodeJS.Signals = 'SIGTERM'): boolean => {
      stub.signals.push(signal)
      if (signal === 'SIGKILL') stub.exitOnly(null)
      return true
    }
    const { timers, recording } = started(full('--duration', '30'), new FakeTimers(), stub)
    let settled = false
    void recording.finished.then(() => {
      settled = true
    })
    stub.write('{"day":"2026-10-15"}\n')
    timers.advanceTo(30_000 + SHUTDOWN_GRACE_MS)
    expect(stub.signals).toEqual(['SIGTERM', 'SIGKILL'])
    await Promise.resolve()
    expect(settled).toBe(false)
    stub.write('{"day":"2026-10-16"}\n')
    stub.write('{"day":"2026-10-17"}')
    stub.close(null)
    const result = await recording.finished
    expect(result.refusal).toBeNull()
    expect(result.records).toHaveLength(3)
    expect(timers.scheduled).toEqual([])
  })

  it('waits for the pipes of a child that can no longer be signalled', async () => {
    // A child that cannot be signalled has already exited, but its last chunks
    // can still be in the pipe: the recording waits for `close` (or one grace
    // period), it does not settle at once.
    const stub = new StubChild()
    stub.kill = (signal: NodeJS.Signals = 'SIGTERM'): boolean => {
      stub.signals.push(signal)
      return false
    }
    const { timers, recording } = started(full('--duration', '30'), new FakeTimers(), stub)
    let settled = false
    void recording.finished.then(() => {
      settled = true
    })
    stub.write('{"day":"2026-10-15"}\n')
    recording.stop()
    expect(stub.signals).toEqual(['SIGTERM'])
    await Promise.resolve()
    expect(settled).toBe(false)
    stub.write('{"day":"2026-10-16"}\n')
    stub.close(0)
    const result = await recording.finished
    expect(result.records).toHaveLength(2)
    expect(timers.scheduled).toEqual([])
  })

  it('records nothing that arrives after a refusal', async () => {
    // The refusal guard on stdout: once a frame is refused nothing is written,
    // so nothing more is read either -- not even a document that would redact.
    let documents = 0
    const base = redactor()
    const broken: Redactor = {
      ...base,
      redactDocument: (document) => {
        documents += 1
        return base.redactDocument(document)
      },
      redactText: () => {
        throw new RedactionRefusal('the audit would not vouch for this sentence')
      },
    }
    const { stub, timers, recording } = started(undefined, undefined, undefined, broken)
    stub.write('{"day":"2026-10-15"}\n')
    stub.write('a sentence the redactor refuses\n')
    expect(stub.signals).toEqual(['SIGTERM'])
    // The child ignores the SIGTERM and keeps writing.
    stub.write('{"day":"2026-10-16"}\n')
    stub.write('{"day":"2026-10-17"}\n')
    expect(documents).toBe(1)
    timers.advanceTo(SHUTDOWN_GRACE_MS)
    const result = await recording.finished
    expect(result.refusal).toBeInstanceOf(RedactionRefusal)
    expect(result.records).toHaveLength(1)
  })

  it('keeps a final frame the child wrote without a newline, and still drops a torn one', async () => {
    // Review round 2 of PR 478: a CLI whose last document had no terminating
    // newline lost it, and when it was the only one the whole recording was
    // refused. A frame that decodes as a whole document cannot be a torn
    // write, so it is recorded; anything else at the end is still dropped.
    const whole = started()
    whole.stub.write('{"day":"2026-10-15"}')
    whole.stub.exit(0)
    const kept = await whole.recording.finished
    expect(kept.refusal).toBeNull()
    expect(kept.records).toEqual([{ at: '2026-10-15T12:00:00Z', document: { day: '2026-10-15' } }])

    const torn = started()
    torn.stub.write('{"day":"2026-10-15"}\n')
    torn.stub.write('{"day":"2026-10-')
    torn.stub.exit(0)
    expect((await torn.recording.finished).records).toEqual([
      { at: '2026-10-15T12:00:00Z', document: { day: '2026-10-15' } },
    ])
  })

  it('redacts the unterminated final frame like any other', async () => {
    const { stub, recording } = started()
    stub.write('{"project":"fixture-sandbox"}')
    stub.exit(0)
    const result = await recording.finished
    expect(result.records).toEqual([{ at: '2026-10-15T12:00:00Z', document: { project: PROJECT_MARKER } }])
  })

  it('takes a person out of a notice, even one written before the document that named them', async () => {
    // Review round 2 of PR 478: a notice is free text, so the only people the
    // redactor can find in it are the ones the recording's documents named --
    // and a notice can come first. The recorder re-runs every notice through
    // the redactor once the recording is over, with every person in hand.
    const { stub, recording } = started()
    stub.write('queued run 1234 by Dana Whitfield for review\n')
    stub.write('{"runs":[{"requested_by":"Dana Whitfield"}]}\n')
    stub.write('retrying for Dana Whitfield\n')
    stub.exit(0)
    const result = await recording.finished
    expect(result.refusal).toBeNull()
    const written = JSON.stringify(result.records)
    expect(written).not.toContain('Whitfield')
    expect(written).not.toContain('Dana')
    const [first, document, last] = result.records
    const pseudonym = (document as unknown as { document: { runs: { requested_by: string }[] } }).document.runs[0]?.requested_by
    expect(pseudonym).toMatch(/^person-[0-9a-f]{8}$/)
    expect(first).toEqual({ at: '2026-10-15T12:00:00Z', notice: `queued run 1234 by ${String(pseudonym)} for review` })
    expect(last).toEqual({ at: '2026-10-15T12:00:00Z', notice: `retrying for ${String(pseudonym)}` })
  })

  it('reports a spawn that never started', async () => {
    const { stub, recording } = started()
    stub.fail(new Error('spawn fugaro ENOENT'))
    const result = await recording.finished
    expect(result.refusal?.message).toContain('ENOENT')
    expect(result.records).toEqual([])
  })
})

describe('the whole tool', () => {
  /** deps is `main`'s arguments with the two side effects captured: what it
   * logged, and the fixture text it would have written. `script` is what the
   * child does once it has been spawned. */
  function tool(script: (stub: StubChild) => void): {
    readonly run: (argv: readonly string[]) => Promise<number>
    readonly lines: string[]
    readonly written: { path: string; text: string }[]
  } {
    const lines: string[] = []
    const written: { path: string; text: string }[] = []
    const stub = new StubChild()
    return {
      lines,
      written,
      run: (argv) =>
        main(argv, {
          cwd: CWD,
          consoleRoot: CWD,
          exists: () => false,
          appEnv: {},
          log: (line) => lines.push(line),
          clock: () => new Date('2026-10-15T12:00:00Z'),
          timers: new FakeTimers(),
          write: (path, text) => written.push({ path, text }),
          spawner: ((): StubChild => {
            queueMicrotask(() => {
              script(stub)
            })
            return stub
          }) as ChildSpawner,
        }),
    }
  }

  it('writes the fixture, and says what it wrote', async () => {
    // The write is the tool's one side effect, and it is stubbed here so this
    // test never touches test/fixtures: what is asserted is the text it would
    // have written, through the real loader.
    const { run, lines, written } = tool((stub) => {
      stub.write('{"project":"fixture-sandbox","day":"2026-10-15"}\n')
      stub.exit(0)
    })
    const code = await run(['--out', OUT, '--project', 'sandbox', '--gcp-project', 'fixture-gcp-1', '--duration', '30'])
    expect(code).toBe(0)
    expect(lines.join('\n')).toContain('recording normal for 30s')
    expect(lines.join('\n')).toContain('argv: <binary> watch --json --project (')
    expect(lines.join('\n')).toContain(`wrote ${CWD}/${OUT}: 1 records`)
    const [entry] = written
    expect(entry?.path).toBe(`${CWD}/${OUT}`)
    // Nothing is printed about the documents themselves: the owner's scrollback
    // is a leak this tool does not create.
    expect(lines.join('\n')).not.toContain('fixture-sandbox')
    const loaded = loadFixture(entry?.text ?? '', OUT)
    expect(loaded.header.case).toBe('normal')
    expect(loaded.header.synthetic).toBe(false)
    expect(loaded.header.recorded).toBe('2026-10-15T12:00:00Z')
    expect(loaded.records).toHaveLength(1)
    expect(entry?.text).not.toContain('fixture-sandbox')
  })

  it('prints neither the project, the config path nor the checkout, on success or failure', async () => {
    // `recorderLiterals` promises these strings are never printed: they have
    // no shape for a pattern to catch, and the terminal transcript is what an
    // owner pastes when reporting a failure. The child's argv carries the first
    // two (`--project`, `--config`) and its cwd is the third.
    const project = ['zorba', 'prod'].join('-')
    const config = ['/home/operator/private-', 'cfg/fugaro.yaml'].join('')
    const checkout = ['/home/operator/src/', 'secret-checkout'].join('')
    const argv = ['--out', OUT, '--project', project, '--config', config, '--dir', checkout, '--gcp-project', 'fixture-gcp-1', '--duration', '30']
    const leaks = (text: string): readonly string[] => [project, config, checkout].filter((value) => text.includes(value))

    const ok = tool((stub) => {
      stub.write('{"day":"2026-10-15"}\n')
      stub.exit(0)
    })
    expect(await ok.run(argv)).toBe(0)
    expect(leaks(ok.lines.join('\n'))).toEqual([])

    const failed = tool((stub) => {
      stub.write('{"day":"2026-10-15"}\n')
      stub.fail(new Error('spawn fugaro ENOENT'))
    })
    const refusal = await failed.run(argv).then(
      () => '',
      (error: unknown) => (error instanceof Error ? error.message : String(error)),
    )
    expect(refusal).toContain('nothing written')
    expect(leaks(failed.lines.join('\n'))).toEqual([])
    expect(leaks(refusal)).toEqual([])
  })

  it('refuses an empty recording rather than writing a quiet-looking fixture', async () => {
    // The child exits at once with a nonzero code: an installation that is not
    // there, or a project the CLI does not know. A fixture with no records would
    // look like a quiet installation, and the owner would commit it.
    const { run, written } = tool((stub) => {
      stub.exit(2)
    })
    await expect(run(['--out', OUT, '--project', 'sandbox', '--gcp-project', 'fixture-gcp-1', '--duration', '30'])).rejects.toThrow(
      /the CLI exited with code 2 and wrote no document; nothing written/,
    )
    expect(written).toEqual([])
  })

  it('writes nothing when the recording fails part-way', async () => {
    const { run, written, lines } = tool((stub) => {
      stub.write('{"project":"fixture-sandbox"}\n')
      stub.write('a sentence\n')
      stub.fail(new Error('spawn fugaro ENOENT'))
    })
    // The spawn fault ends the recording before its duration is up. The records
    // it read are not the point: a recording that ended in a failure is not a
    // recording, and the file is not written.
    await expect(run(['--out', OUT, '--project', 'sandbox', '--gcp-project', 'fixture-gcp-1', '--duration', '30'])).rejects.toThrow(
      /nothing written/,
    )
    expect(written).toEqual([])
    expect(lines.join('\n')).not.toContain('wrote')
  })

  it('never defaults the project, not even for a whole run', async () => {
    await expect(main(['--out', OUT])).rejects.toThrow(RecorderRefusal)
    // A refusal that happens before anything is spawned: the message is the one
    // the owner reads, and no child is reached.
    await expect(main(['--out', OUT])).rejects.toThrow(/--project is required/)
  })

  it('returns 0 or throws, and never hands back a refusal’s exit code', async () => {
    // The contract `main`'s docstring got wrong (review of Task 4.1): a run that
    // wrote a fixture resolves with 0, and a run that wrote nothing *throws* its
    // `RecorderRefusal` -- the sentence is what the caller prints, and resolving
    // with the code instead would let a caller take a refusal for a finished
    // recording. The 2 is the entry point's, at the foot of the tool.
    const wrote = tool((stub) => {
      stub.write('{"day":"2026-10-15"}\n')
      stub.exit(0)
    })
    expect(await wrote.run(['--out', OUT, '--project', 'sandbox', '--gcp-project', 'fixture-gcp-1'])).toBe(0)

    // Every refusal shape, and each one a throw rather than a number.
    const beforeSpawn = tool(() => {})
    await expect(beforeSpawn.run(['--out', OUT])).rejects.toThrow(RecorderRefusal)
    const noDocument = tool((stub) => {
      stub.exit(2)
    })
    await expect(
      noDocument.run(['--out', OUT, '--project', 'sandbox', '--gcp-project', 'fixture-gcp-1']),
    ).rejects.toThrow(RecorderRefusal)
    // And neither refusal wrote anything, so neither produced a code at all.
    expect(beforeSpawn.written).toEqual([])
    expect(noDocument.written).toEqual([])

    // The claim above is only worth anything if the tool's own docstring states
    // it: that is the sentence an owner reads before running it, and the review
    // of Task 4.1 found the old one describing a return contract `main` does not
    // have (`2 for a refusal`). Read off the source, the way `spawn.test.ts`
    // reads the spawn table's callers.
    const toolSource = readFileSync(fileURLToPath(new URL('../../tools/record-fixture/index.ts', import.meta.url)), 'utf8')
    const docstring = /\/\*\*\n((?: \*.*\n)+) \*\/\nexport async function main\(/.exec(toolSource)?.[1] ?? ''
    expect(docstring).toContain('returns 0')
    expect(docstring).toContain('throws a `RecorderRefusal`')
    expect(docstring).toContain('exits 2')
    expect(docstring).not.toMatch(/2 for a refusal/)
  })
})

describe("the recorder's hardening (security review of b65fff5)", () => {
  it('requires the GCP project, and tells the redactor its ID, bucket and number', () => {
    expect(refusal(() => opts('--out', OUT, '--project', 'sandbox'))).toMatch(/--gcp-project is required/)
    expect(refusal(() => full('--gcp-project', 'Not A Project'))).toMatch(/--gcp-project must be a GCP project ID/)
    const options = full('--project-number', '123456789012', '--literal', 'acme-codename', '--literal', 'second-one')
    const literals = recorderLiterals(options, {})
    expect(literals).toContain('fixture-gcp-1')
    expect(literals).toContain('fugaro-runs-fixture-gcp-1')
    expect(literals).toContain('123456789012')
    expect(literals).toContain('acme-codename')
    expect(literals).toContain('second-one')
    expect(refusal(() => full('--project-number', '12ab'))).toMatch(/--project-number must be/)
  })

  it('normalises a value before the out-of-bounds check', () => {
    for (const project of [' belong', 'belong ', '\tbelong', 'ｂｅｌｏｎｇ', 'BELONG\n']) {
      expect(refusal(() => opts('--out', OUT, '--project', project)), JSON.stringify(project)).toMatch(/belong/)
    }
    expect(refusal(() => full('--gcp-project', 'belong-gcp-1'))).toMatch(/--gcp-project names belong/)
  })

  it('folds look-alike letters and trailing dots before the out-of-bounds check', () => {
    // Cyrillic е and о, a trailing dot (which a resolver drops), and both at once.
    for (const project of ['bеlong', 'belоng', 'belong.', 'BELONG..', 'bеlong.']) {
      expect(refusal(() => opts('--out', OUT, '--project', project)), JSON.stringify(project)).toMatch(/names belong/)
    }
    expect(refusal(() => opts('--out', OUT, '--project', 'sandbox', '--dir', '/home/operator/src/bеlong./x'))).toMatch(
      /--dir names belong/,
    )
  })

  it("bounds a line by the redactor's cap by default, before the redactor sees it", async () => {
    const options = full('--duration', '30')
    const stub = new StubChild()
    const recording = startRecording({
      options,
      plan: planForRecording(options, {}),
      redactor: redactor(),
      spawner: (() => stub) as ChildSpawner,
      timers: new FakeTimers(),
      clock: () => new Date('2026-10-15T12:00:00Z'),
    })
    stub.write(`{"t":"${'a'.repeat(MAX_REDACTABLE_LINE_LENGTH)}"}\n`)
    stub.exit(0)
    const result = await recording.finished
    expect(result.refusal?.message).toMatch(/longer than a feed line/)
    expect(result.records).toEqual([])
  })

  it("refuses an --out outside this repository's console/test/fixtures", () => {
    for (const out of ['/etc/fixtures/x.ndjson', '/home/operator/.ssh/fixtures/x.ndjson', '../other/test/fixtures/x.ndjson']) {
      expect(refusal(() => opts('--out', out, '--project', 'sandbox')), out).toMatch(/must be written under test\/fixtures\//)
    }
  })

  it('refuses a line longer than a feed line can be, before it reaches the redactor', async () => {
    const options = full('--duration', '30')
    const stub = new StubChild()
    const timers = new FakeTimers()
    const recording = startRecording({
      options,
      plan: planForRecording(options, {}),
      redactor: redactor(),
      spawner: (() => stub) as ChildSpawner,
      timers,
      clock: () => new Date('2026-10-15T12:00:00Z'),
      maxLineLength: 64,
    })
    stub.write('{"day":"2026-10-15"}\n')
    stub.write('x'.repeat(65))
    expect(stub.signals).toEqual(['SIGTERM'])
    timers.advanceTo(SHUTDOWN_GRACE_MS)
    const result = await recording.finished
    expect(result.refusal?.message).toMatch(/longer than a feed line/)
    // A complete line over the bound is refused too, not only a partial one.
    const second = new StubChild()
    const again = startRecording({
      options,
      plan: planForRecording(options, {}),
      redactor: redactor(),
      spawner: (() => second) as ChildSpawner,
      timers: new FakeTimers(),
      clock: () => new Date('2026-10-15T12:00:00Z'),
      maxLineLength: 64,
    })
    second.write(`"${'y'.repeat(70)}"\n`)
    second.exit(0)
    expect((await again.finished).refusal?.message).toMatch(/longer than a feed line/)
  })
})

describe('the write', () => {
  const made: string[] = []
  afterEach(() => {
    for (const dir of made.splice(0)) rmSync(dir, { recursive: true, force: true })
  })

  /** consoleRoot is a fresh directory with test/fixtures/ in it. */
  function consoleRoot(): { root: string; fixtures: string; outside: string } {
    const base = realpathSync(mkdtempSync(join(tmpdir(), 'record-fixture-')))
    made.push(base)
    const root = join(base, 'console')
    const fixtures = join(root, 'test', 'fixtures')
    mkdirSync(fixtures, { recursive: true })
    const outside = join(base, 'outside')
    mkdirSync(outside)
    return { root, fixtures, outside }
  }

  it('creates a new fixture, and refuses to overwrite one without --force', () => {
    const { root, fixtures } = consoleRoot()
    const target = join(fixtures, 'normal.ndjson')
    writeFixtureFile(target, 'one\n', { force: false, consoleRoot: root })
    expect(readFileSync(target, 'utf8')).toBe('one\n')
    expect(refusal(() => { writeFixtureFile(target, 'two\n', { force: false, consoleRoot: root }) })).toMatch(/exists/)
    writeFixtureFile(target, 'two\n', { force: true, consoleRoot: root })
    expect(readFileSync(target, 'utf8')).toBe('two\n')
  })

  it('refuses a symbolic link at the target, dangling or not, even with --force', () => {
    const { root, fixtures, outside } = consoleRoot()
    const target = join(fixtures, 'normal.ndjson')
    const victim = join(outside, 'victim')
    symlinkSync(victim, target)
    for (const force of [false, true]) {
      expect(refusal(() => { writeFixtureFile(target, 'x', { force, consoleRoot: root }) })).toMatch(/symbolic link/)
    }
    expect(() => lstatSync(victim)).toThrow()
    writeFileSync(victim, 'keep')
    expect(refusal(() => { writeFixtureFile(target, 'x', { force: true, consoleRoot: root }) })).toMatch(/symbolic link/)
    expect(readFileSync(victim, 'utf8')).toBe('keep')
    // The default existence check is lstat, so the parser sees the dangling
    // link too, and does not call the slot free.
    rmSync(victim)
    expect(
      refusal(() =>
        parseRecorderArgs(['--out', target, '--project', 'sandbox', '--gcp-project', 'fixture-gcp-1'], root, {
          consoleRoot: root,
        }),
      ),
    ).toMatch(/exists/)
  })

  it('refuses a target that is not a regular file, even with --force', () => {
    const { root, fixtures } = consoleRoot()
    mkdirSync(join(fixtures, 'normal.ndjson'))
    expect(
      refusal(() => { writeFixtureFile(join(fixtures, 'normal.ndjson'), 'x', { force: true, consoleRoot: root }) }),
    ).toMatch(/not a regular file/)
  })

  it('refuses a fixtures directory that is a symbolic link, or a target outside it', () => {
    const { root, fixtures, outside } = consoleRoot()
    rmSync(fixtures, { recursive: true })
    symlinkSync(outside, fixtures)
    expect(
      refusal(() => { writeFixtureFile(join(fixtures, 'normal.ndjson'), 'x', { force: false, consoleRoot: root }) }),
    ).toMatch(/symbolic link/)
    expect(() => lstatSync(join(outside, 'normal.ndjson'))).toThrow()
    const other = consoleRoot()
    expect(
      refusal(() => { writeFixtureFile(join(other.outside, 'normal.ndjson'), 'x', { force: false, consoleRoot: other.root }) }),
    ).toMatch(/test\/fixtures/)
  })
})
