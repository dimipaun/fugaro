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

import { describe, expect, it } from 'vitest'

import { CHILD_ENV_KEYS, SHUTDOWN_GRACE_MS } from '../../src/main/spawn'
import type { ChildSpawner, ReadableLike, SpawnedChildLike, Timers } from '../../src/main/spawn'
import { FIXTURE_CASES, FIXTURE_DIRECTORY, FixtureRefusal, loadFixture } from '../../src/source/fixture'
import type { FixtureRecord } from '../../src/source/fixture'
import { PROJECT_MARKER, RedactionRefusal, createRedactor } from '../../src/source/redact'
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
} from '../../tools/record-fixture/index'
import type { RecorderOptions } from '../../tools/record-fixture/index'

const CWD = '/home/operator/src/fugaro/console'
const OUT = `${FIXTURE_DIRECTORY}/normal.ndjson`

/** opts parses one invocation against a fixed checkout, with nothing existing
 * yet -- the shape most of the checks below start from. */
function opts(...argv: readonly string[]): RecorderOptions {
  return parseRecorderArgs([...argv], CWD, { exists: () => false })
}

/** full is an invocation that passes every check. */
function full(...extra: readonly string[]): RecorderOptions {
  return opts('--out', OUT, '--project', 'sandbox', ...extra)
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
  readonly #errors: ((error: Error) => void)[] = []
  #alive = true

  kill(signal: NodeJS.Signals = 'SIGTERM'): boolean {
    if (!this.#alive) return false
    this.signals.push(signal)
    // A child that is SIGKILLed is gone; one that is SIGTERMed may yet exit on
    // its own, and that difference is what the grace timer is for.
    if (signal === 'SIGKILL') this.#alive = false
    return true
  }

  on(event: 'error', listener: (error: Error) => void): unknown
  on(event: 'exit', listener: (code: number | null, signal: string | null) => void): unknown
  on(
    event: 'error' | 'exit',
    listener: ((error: Error) => void) | ((code: number | null, signal: string | null) => void),
  ): unknown {
    if (event === 'error') this.#errors.push(listener as (error: Error) => void)
    else this.#exits.push(listener as (code: number | null, signal: string | null) => void)
    return this
  }

  /** exit is the child going away by itself. */
  exit(code: number | null): void {
    this.#alive = false
    for (const listener of this.#exits) listener(code, null)
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
      refusal(() => parseRecorderArgs(['--out', OUT, '--project', 'sandbox'], CWD, { exists: existing })),
    ).toMatch(/exists; pass --force/)
    const forced = parseRecorderArgs(['--out', OUT, '--project', 'sandbox', '--force'], CWD, { exists: existing })
    expect(forced.force).toBe(true)
    expect(opts('--out', OUT, '--project', 'sandbox').force).toBe(false)
    // The check is on the resolved path rather than the spelling: `--out` given
    // from another directory still cannot land on a fixture that is there.
    expect(
      refusal(() =>
        parseRecorderArgs(['--out', './test/fixtures/x.ndjson', '--project', 'sandbox'], '/a/b/console', {
          exists: () => true,
        }),
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
    })
    // `--dir` is resolved against the current directory too, so a relative
    // checkout is a plan the spawner can use.
    expect(full('--dir', '../sandbox').directory).toBe('/home/operator/src/fugaro/sandbox')
    // `--name=value` is the same flag, and the defaults are the documented ones.
    const inline = opts(`--out=${OUT}`, '--project=sandbox', '--duration=30')
    expect(inline.project).toBe('sandbox')
    expect(inline.durationSeconds).toBe(30)
    expect(opts('--out', OUT, '--project', 'sandbox')).toMatchObject({
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
    const code = await run(['--out', OUT, '--project', 'sandbox', '--duration', '30'])
    expect(code).toBe(0)
    expect(lines.join('\n')).toContain('watch --json --project sandbox')
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

  it('refuses an empty recording rather than writing a quiet-looking fixture', async () => {
    // The child exits at once with a nonzero code: an installation that is not
    // there, or a project the CLI does not know. A fixture with no records would
    // look like a quiet installation, and the owner would commit it.
    const { run, written } = tool((stub) => {
      stub.exit(2)
    })
    await expect(run(['--out', OUT, '--project', 'sandbox', '--duration', '30'])).rejects.toThrow(
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
    await expect(run(['--out', OUT, '--project', 'sandbox', '--duration', '30'])).rejects.toThrow(
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
})
