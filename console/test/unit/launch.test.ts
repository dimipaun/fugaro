// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The launch (console v2 plan V2.5.1a part 2a, SECURITY-SENSITIVE): the frozen
// spawn, the confirmation seam, the limits, and the one argv rule 11 writes.
//
// Every property here is one a mutation can break, and each names what catches
// it:
//
//   - a `--title=`/`--plan-task=` prefix accepted for any row but `run.launch`
//     fails "accepts the `=` form for the launch's row and refuses it for every
//     other" (the one mutation proof this task takes, `spawn.ts`'s
//     `assertArgvFlags`);
//   - an argv with one word more than rule 11's fails "builds rule 11's exact
//     argv from the five named fields and nothing else";
//   - the task text reaching an argv word or a spawn-log line fails "keeps the
//     task text out of every argv word and out of the spawn log";
//   - a confirmer asked before the records were read fails every test in "the
//     refusals come before the window";
//   - a second child for an answered run id fails "answers a second yes from the
//     record it already has, and spawns one child in all";
//   - a launch that rebuilt its argv after the window opened fails "spawns the
//     frozen object's own words and nothing else";
//   - a launch that took a second child per installation, or a third across the
//     app, fails the ceiling tests;
//   - a hold or a rate stamp written *after* `build` is awaited fails "opens one
//     window for a same-tick burst on one installation, and two app-wide", which
//     is the burst those limits exist for;
//   - a limit read off the wall clock fails "reads the limits from a clock that
//     only moves forward, never from the wall clock";
//   - a run id drawn without checking it against the ids this process already
//     holds -- one id for two launches in one second, whose `pending` entries
//     then overwrite each other -- fails "redraws the four hex
//     digits when the id it drew is one this process already holds", and an id
//     drawn (rather than claimed) where the launch is built fails "claims the id
//     with the reservation, so a same-tick pair cannot draw one id", and a
//     redraw that skips the ids this process has already issued fails "redraws an
//     id this process has already answered, so a replay cannot resolve a new
//     window";
//   - an id claimed for a launch whose build refused or threw, and never
//     released, fails "frees the id a refused or thrown build claimed, so the
//     next launch can take it";
//   - an answer for an open or settling run id that resolves to nothing instead
//     of attaching to the launch in flight fails "attaches a second answer to the
//     launch in flight, and starts one child in all";
//   - a frozen object kept once its answer has settled -- one task text per
//     launch ever confirmed -- fails "holds a launch’s frozen object only while
//     its answer is unsettled";
//   - an uncapped outcome record, or one evicting anything but the oldest first,
//     fails "caps the outcomes it replays, and answers an evicted id with
//     nothing", and an evicted id drawn again rather than refused by the issued
//     set fails "never draws an id it has already issued, even once its answer is
//     evicted";
//   - a per-installation rate entry left behind once its stamps have expired,
//     which is memory a renderer's stream of names chooses, fails "drops an
//     installation once its stamps have expired, so the names cannot pile up";
//   - a confirmer that throws, or a still-recorded read that rejects, leaving
//     the hold taken fails the two tests beside it in "a launch that was
//     stopped, and one that failed outright";
//   - a killed launch reported as "not launched", or as an exit status of null,
//     fails "names the run id it cannot speak for, and never says the launch did
//     not happen" and "reports a child killed by a signal as unknown".

import { describe, expect, it, vi } from 'vitest'

import {
  CONFIRM_RATE_APP,
  CONFIRM_RATE_PER_INSTALLATION,
  CONFIRM_RATE_WINDOW_MS,
  DECLINED_CANCELLED,
  DECLINED_UNBUILT,
  DECLINING_CONFIRMER,
  LAUNCH_COOL_DOWN_MS,
  LAUNCH_TIMEOUT_MS,
  MAX_RECORDED_OUTCOMES,
  createLaunchRunner,
  launchOutcomeUnknown,
} from '../../src/main/launch'
import type { ConfirmView, FrozenLaunch, LaunchConfirmer, LaunchTarget, TargetLookup } from '../../src/main/launch'
import { RUN_LAUNCH_EQUALS_FLAGS, assertArgvFlags, privateWorkingDirectory } from '../../src/main/spawn'
import type { ChildSpawner, SpawnOptionsLike, SpawnedChildLike, Timers } from '../../src/main/spawn'
import type { RecordedSpawn, SpawnLogWriter } from '../../src/main/spawnlog'

const PROJECT = 'sandbox'
const OTHER_PROJECT = 'staging'
const RECORDED_REPOSITORY = 'github/acme/app'
const REPOSITORY = 'acme/app'
const BRANCH = 'main'
const BINARY = '/usr/local/bin/fugaro'
const CLI_VERSION = '0.11.2'
const TASK = 'summarise the alerting backlog'
/** the stamp and the tail `newRunID` builds for `NOW` and `RANDOM` */
const NOW = Date.UTC(2026, 9, 28, 15, 26, 18)
const RUN_ID = '20261028-152618-a2fc'
const RANDOM = (): number => 0xa2fc / 0x10000

/** countingRandom is the clock's counterpart for the id's hex tail: the first
 * value is `RANDOM`'s, so the first launch of every test is `RUN_ID`, and every
 * later launch of the same runner gets an id of its own -- which is what makes
 * "a run id is never reused" observable. */
function countingRandom(): () => number {
  let tail = 0xa2fc
  return () => {
    const value = tail
    tail += 1
    return value / 0x10000
  }
}

/** tails is a random source that answers one queued draw per call, so a test can
 * force two draws to land on the same tail and then on a different one. A draw
 * past the queue is an error rather than a default: the collision tests are
 * about exactly how many times the id was drawn. */
function tails(...queued: readonly number[]): () => number {
  const values = [...queued]
  return () => {
    const next = values.shift()
    if (next === undefined) throw new Error('the launcher drew more run ids than this test queued')
    return next / 0x10000
  }
}

/** rowArgv is rule 11's argv with this test's own values substituted: the row as
 * a string, which is how the design compares it. */
function rowArgv(options: { readonly title?: string; readonly planTask?: string; readonly project?: string } = {}): string {
  const project = options.project ?? PROJECT
  const title = options.title === undefined ? '' : ` --title=${options.title}`
  const planTask = options.planTask === undefined ? '' : ` --plan-task=${options.planTask}`
  return `run --task-file - --json --run-id ${RUN_ID} --repo ${REPOSITORY}${title}${planTask} --project ${project}`
}

interface SpawnCall {
  readonly command: string
  readonly args: readonly string[]
  readonly options: SpawnOptionsLike
}

interface TypedChild extends SpawnedChildLike {
  readonly written: readonly string[]
  readonly ended: readonly boolean[]
  emitStdout(text: string): void
  emitStderr(text: string): void
  failStdin(error: Error): void
  exit(code: number | null, signal: string | null): void
}

/** typedChild is a child whose stdin, stdout, exit and close the test drives. */
function typedChild(): TypedChild {
  const stdout: ((chunk: Buffer | string) => void)[] = []
  const stderr: ((chunk: Buffer | string) => void)[] = []
  const exits: ((code: number | null, signal: string | null) => void)[] = []
  const closes: ((code: number | null, signal: string | null) => void)[] = []
  const written: string[] = []
  const ended: boolean[] = []
  const stdinErrors: ((error: Error) => void)[] = []
  const child = {
    stdout: { on: (_event: string, listener: (chunk: Buffer | string) => void) => stdout.push(listener) },
    stderr: { on: (_event: string, listener: (chunk: Buffer | string) => void) => stderr.push(listener) },
    stdin: {
      write: (chunk: Uint8Array) => written.push(new TextDecoder().decode(chunk)),
      end: () => ended.push(true),
      // The one stream this console writes to. Its failure is an `error` event
      // on this object, so the fake records the listener that has to be there.
      on: (_event: 'error', listener: (error: Error) => void) => stdinErrors.push(listener),
    },
    on(event: 'spawn' | 'error' | 'exit' | 'close', listener: (() => void) | ((...args: unknown[]) => void)) {
      // Node fires `spawn` on the tick after the process exists; this fake fires
      // it at registration, which is the ordering the assertions here need (the
      // task is written the moment the process exists) without a tick to wait.
      if (event === 'spawn') (listener as () => void)()
      else if (event === 'exit') exits.push(listener as (code: number | null, signal: string | null) => void)
      else if (event === 'close') closes.push(listener as (code: number | null, signal: string | null) => void)
      return undefined
    },
    kill: () => true,
    emitStdout: (text: string) => {
      for (const listener of stdout) listener(text)
    },
    emitStderr: (text: string) => {
      for (const listener of stderr) listener(text)
    },
    failStdin: (error: Error) => {
      for (const listener of stdinErrors) listener(error)
    },
    exit: (code: number | null, signal: string | null) => {
      for (const listener of exits) listener(code, signal)
      for (const listener of closes) listener(code, signal)
    },
    written,
    ended,
  }
  return child as unknown as TypedChild
}

/** launchResult is `fugaro run --json`'s reply for one call's own argv: the run
 * id and the repository are read back off the words the console emitted, so a
 * reply is about this launch by construction -- and a test that wants a mismatch
 * overrides one of them. */
function launchResult(args: readonly string[], overrides: Record<string, string> = {}): string {
  const value = (flag: string): string => {
    const at = args.indexOf(flag)
    return at === -1 ? '' : (args[at + 1] ?? '')
  }
  return JSON.stringify({
    project: PROJECT,
    run: 'run-1',
    repo: value('--repo'),
    run_id: value('--run-id'),
    branch: BRANCH,
    ref: 'refs/heads/main',
    execution: 'execution-1',
    log_url: 'https://example.invalid/run-1',
    status: 'launched',
    ...overrides,
  })
}

/** completingSpawner answers every launch with a well-formed reply, one turn
 * later, so `runToCompletion` sees the child's bytes and then its close. */
function completingSpawner(options: { readonly overrides?: Record<string, string>; readonly code?: number } = {}): ChildSpawner & {
  readonly calls: readonly SpawnCall[]
  readonly children: readonly TypedChild[]
} {
  const calls: SpawnCall[] = []
  const children: TypedChild[] = []
  const spawner = ((command: string, args: readonly string[], settings: SpawnOptionsLike) => {
    calls.push({ command, args, options: settings })
    const child = typedChild()
    children.push(child)
    const stdout = launchResult(args, options.overrides ?? {})
    queueMicrotask(() => {
      child.emitStdout(stdout)
      child.exit(options.code ?? 0, null)
    })
    return child
  }) as unknown as ChildSpawner & { calls: SpawnCall[]; children: TypedChild[] }
  Object.defineProperty(spawner, 'calls', { get: () => calls })
  Object.defineProperty(spawner, 'children', { get: () => children })
  return spawner
}

/** quietSpawner is a spawner whose child never answers: the launch stays in
 * flight, which is what the ceiling tests need. */
function quietSpawner(): ChildSpawner & { readonly calls: readonly SpawnCall[] } {
  const calls: SpawnCall[] = []
  const spawner = ((command: string, args: readonly string[], settings: SpawnOptionsLike) => {
    calls.push({ command, args, options: settings })
    return typedChild()
  }) as unknown as ChildSpawner & { calls: SpawnCall[] }
  Object.defineProperty(spawner, 'calls', { get: () => calls })
  return spawner
}

/** heldSpawner is a spawner whose child does not answer until the test lets it:
 * the launch stays in flight across several answers, which is the state the
 * in-flight attach case is about, and `children` is how the test completes it. */
function heldSpawner(): ChildSpawner & {
  readonly calls: readonly SpawnCall[]
  readonly children: readonly TypedChild[]
} {
  const calls: SpawnCall[] = []
  const children: TypedChild[] = []
  const spawner = ((command: string, args: readonly string[], settings: SpawnOptionsLike) => {
    calls.push({ command, args, options: settings })
    const child = typedChild()
    children.push(child)
    return child
  }) as unknown as ChildSpawner & { calls: SpawnCall[]; children: TypedChild[] }
  Object.defineProperty(spawner, 'calls', { get: () => calls })
  Object.defineProperty(spawner, 'children', { get: () => children })
  return spawner
}

function failingSpawner(why: string): ChildSpawner {
  return () => {
    throw new Error(why)
  }
}

/** neverFires is the timers a test injects when nothing should reach a
 * deadline: no handle is created, so no real timer outlives the test. */
function neverFires(): Timers {
  return { setTimeout: () => 0 as unknown as ReturnType<typeof setTimeout>, clearTimeout: () => undefined }
}

/** recordingTimers remembers the deadline the launcher asked for. */
function recordingTimers(): Timers & { readonly delays: readonly number[] } {
  const delays: number[] = []
  return {
    delays,
    setTimeout: (_handler: () => void, ms: number) => {
      delays.push(ms)
      return 0 as unknown as ReturnType<typeof setTimeout>
    },
    clearTimeout: () => undefined,
  }
}

/** heldConfirmer is a confirmer whose answer the test gives, so a launch can be
 * inspected while its window is open. It holds **one answer per window**, keyed
 * by run id: several windows can be open at once, which is exactly the state
 * the in-flight caps are about. `decide` answers the oldest window still open,
 * which is what a test with a single window reads. */
function heldConfirmer(): LaunchConfirmer & {
  readonly opened: readonly ConfirmView[]
  readonly openRunIds: readonly string[]
  decide(yes: boolean): void
  decideWindow(runId: string, yes: boolean): void
} {
  const opened: ConfirmView[] = []
  const waiting = new Map<string, (yes: boolean, reason?: string) => void>()
  const decideWindow = (runId: string, yes: boolean): void => {
    const held = waiting.get(runId)
    waiting.delete(runId)
    held?.(yes)
  }
  return {
    opened,
    open(view, answer) {
      opened.push(view)
      waiting.set(view.runId, answer)
    },
    decide(yes) {
      const oldest = [...waiting.keys()][0]
      if (oldest !== undefined) decideWindow(oldest, yes)
    },
    decideWindow,
    // The window going away on its own: rule 11's installation moved while the
    // window is open. A run with no window open is not a cancel, exactly as the
    // real confirmer's `close` is.
    close(runId) {
      decideWindow(runId, false)
    },
    get openRunIds(): readonly string[] {
      return [...waiting.keys()]
    },
  }
}

/** answeringConfirmer is the other half: it answers every window as it opens. */
function answeringConfirmer(yes: boolean): LaunchConfirmer & { readonly opened: readonly ConfirmView[] } {
  const opened: ConfirmView[] = []
  return {
    opened,
    open(view, decide) {
      opened.push(view)
      decide(yes)
    },
    // Nothing stays open to be closed.
    close() {
      return undefined
    },
  }
}

/** closingConfirmer is `heldConfirmer` with the run ids it was asked to close
 * kept: `cancelFor` names a **project**, so which run id -- if any -- reached
 * the confirmer's `close` is the whole of what it can be observed to have
 * done. */
function closingConfirmer(): {
  readonly confirmer: LaunchConfirmer
  readonly opened: readonly ConfirmView[]
  readonly closed: readonly string[]
  readonly openRunIds: () => readonly string[]
  decide(yes: boolean): void
  decideWindow(runId: string, yes: boolean): void
} {
  const opened: ConfirmView[] = []
  const closed: string[] = []
  const waiting = new Map<string, (yes: boolean, reason?: string) => void>()
  const decideWindow = (runId: string, yes: boolean): void => {
    const held = waiting.get(runId)
    waiting.delete(runId)
    held?.(yes)
  }
  return {
    opened,
    closed,
    openRunIds: () => [...waiting.keys()],
    decide(yes: boolean) {
      const oldest = [...waiting.keys()][0]
      if (oldest !== undefined) decideWindow(oldest, yes)
    },
    decideWindow,
    confirmer: {
      open(view, decide) {
        opened.push(view)
        waiting.set(view.runId, decide)
      },
      close(runId) {
        closed.push(runId)
        decideWindow(runId, false)
      },
    },
  }
}

/** untilSettled waits out every microtask a launch makes on its way to a spawn:
 * the records read, the answer, and the still-recorded check. A fixed number of
 * turns rather than a poll, so the assertions are about the order the launcher
 * does its work in. */
async function untilSettled(): Promise<void> {
  for (let turn = 0; turn < 12; turn += 1) await Promise.resolve()
}

const FINGERPRINT = '["record-1","sandbox","/usr/local/bin/fugaro","","",""]'

function target(overrides: Partial<LaunchTarget> = {}): LaunchTarget {
  return {
    project: PROJECT,
    binaryPath: BINARY,
    cliVersion: CLI_VERSION,
    records: { baseBranch: BRANCH, repositories: [{ repository: RECORDED_REPOSITORY, baseBranch: '' }] },
    fingerprint: FINGERPRINT,
    ...overrides,
  }
}

/** runner builds a launcher over one target, one clock and one confirmer. A
 * `project: ''` target is the records read refusing every name. */
function runner(options: {
  readonly confirmer: LaunchConfirmer
  readonly target?: LaunchTarget
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timers?: Timers
  /** `now` is the **limits'** clock (monotonic in production, `performance.now`)
   * and `wallClock` the run id's (`Date.now`); both default to `NOW` so that a
   * test's first launch is `RUN_ID`. The one test that asserts the limits do not
   * read the wall clock builds its launcher without either, so that it exercises
   * the production defaults rather than this helper's stand-ins for them. */
  readonly now?: () => number
  readonly wallClock?: () => number
  /** `random` is the id's four hex digits, one draw per attempt: the collision
   * tests are the only callers that need to force two draws to land on the same
   * id, and they are the reason the tail is injectable at all. */
  readonly random?: () => number
  readonly stillRecorded?: (launch: FrozenLaunch) => boolean | Promise<boolean>
  /** `lookUp` replaces the records entirely: the one test that needs a lookup
   * answering for an installation other than the one asked about cannot use the
   * default, which is the console's own shape (the target is built from the
   * name it was given). */
  readonly lookUp?: (project: string) => TargetLookup | Promise<TargetLookup>
}): ReturnType<typeof createLaunchRunner> {
  const now = options.now ?? ((): number => NOW)
  const wallClock = options.wallClock ?? now
  const held = options.target ?? target()
  const lookUp = options.lookUp
  const shared = {
    confirmer: options.confirmer,
    stillRecorded: options.stillRecorded ?? (() => true),
    appEnv: { HOME: '/home/person-1' },
    ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
    ...(options.log === undefined ? {} : { log: options.log }),
    timers: options.timers ?? neverFires(),
    now,
    wallClock,
    random: options.random ?? countingRandom(),
  }
  if (lookUp !== undefined) {
    return createLaunchRunner({ ...shared, lookUp: async (project) => lookUp(project) })
  }
  return createLaunchRunner({ ...shared, lookUp: (project: string) => Promise.resolve(recordsFor(held, project)) })
}

/** recordsFor is the console's own lookup shape: the target under the name it
 * was asked about, or the records refusing a name they do not carry. A
 * `project: ''` target is the records refusing every name. */
function recordsFor(held: LaunchTarget, project: string): TargetLookup {
  if (held.project === '' || project === '') {
    return {
      ok: false,
      reason: `no installation named ${JSON.stringify(project)} is one this console’s records carry`,
    }
  }
  // The launcher refuses a target whose project is not the name it was asked
  // about, so a test that launches a *second* installation gets the same records
  // under that installation's own name.
  return { ok: true, target: project === held.project ? held : { ...held, project } }
}

/** catchRateMap catches the launcher's per-installation rate map, which is the
 * one structure in it a test cannot reach any other way: it lives in a closure,
 * and `reserve` is the only place in the launcher that writes a `Map` entry
 * whose value is a **stamp list** under an installation's name -- the in-flight
 * entry for the same name is a run id string, the cool-down a number, the
 * pending answers functions and the frozen launches objects. The filter is on
 * that shape and not on the map, so what `keys` reads is the rate map and
 * nothing else this test happens to build. */
function catchRateMap(): {
  readonly keys: () => readonly string[]
  readonly restore: () => void
} {
  let caught: Map<string, number[]> | undefined
  const set = Map.prototype.set
  const spy = vi.spyOn(Map.prototype, 'set').mockImplementation(function (
    this: Map<string, number[]>,
    key: string,
    value: number[],
  ) {
    if (Array.isArray(value) && value.every((stamp) => typeof stamp === 'number')) caught = this
    return set.call(this, key, value)
  } as unknown as typeof Map.prototype.set)
  return {
    keys: () => [...(caught?.keys() ?? [])],
    restore: () => spy.mockRestore(),
  }
}

/** catchLaunchMaps catches the launcher's two other closure maps, whose **sizes**
 * are the property: the outcome record (`outcomes`) and the open windows'
 * frozen launches (`frozen`). Neither is reachable from a test any other way,
 * and the filter is on each value's shape -- a `LaunchOutcome` carries
 * `outcomeUnknown` and `replayed`, a `FrozenLaunch` a `plan` and `taskBytes` --
 * so what the two readings see are those maps and nothing else this test builds.
 * A map that was never written reads as absent rather than as empty, which is
 * why the callers assert a size they have made non-zero before reading zero. */
function catchLaunchMaps(): {
  readonly outcomes: () => number | undefined
  readonly frozen: () => number | undefined
  readonly restore: () => void
} {
  let outcomeMap: Map<string, unknown> | undefined
  let frozenMap: Map<string, unknown> | undefined
  const set = Map.prototype.set
  const spy = vi.spyOn(Map.prototype, 'set').mockImplementation(function (
    this: Map<string, unknown>,
    key: string,
    value: unknown,
  ) {
    if (typeof value === 'object' && value !== null) {
      if ('outcomeUnknown' in value && 'replayed' in value) outcomeMap = this
      if ('plan' in value && 'taskBytes' in value) frozenMap = this
    }
    return set.call(this, key, value)
  } as unknown as typeof Map.prototype.set)
  return {
    outcomes: () => outcomeMap?.size,
    frozen: () => frozenMap?.size,
    restore: () => spy.mockRestore(),
  }
}

interface LaunchRequestLike {
  installation: string
  repository: string
  task: string
  title?: string
  planTask?: string
}

function request(overrides: Partial<LaunchRequestLike> = {}): LaunchRequestLike {
  return { installation: PROJECT, repository: RECORDED_REPOSITORY, task: TASK, ...overrides }
}

function logRecorder(): SpawnLogWriter & { readonly lines: readonly RecordedSpawn[] } {
  const lines: RecordedSpawn[] = []
  const writer = {
    enabled: true,
    path: '/tmp/spawn.log',
    records: 0,
    error: null,
    record: (spawn: RecordedSpawn) => lines.push(spawn),
    close: () => undefined,
  }
  Object.defineProperty(writer, 'lines', { get: () => lines })
  return writer as unknown as SpawnLogWriter & { readonly lines: readonly RecordedSpawn[] }
}

describe('the launch, and the argv it writes', () => {
  it('builds rule 11’s exact argv from the five named fields and nothing else', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })
    const launched = held.launch(request())
    await untilSettled()

    const view = confirmer.opened[0] as ConfirmView
    // The vector starts at the program word, so the design's row string -- which
    // spells the CLI's own argv -- is everything after it.
    expect(view.argv.slice(1).join(' ')).toBe(rowArgv())
    expect(view.argv[0]).toBe(BINARY)
    expect(view.argv.slice(1)).toStrictEqual([
      'run',
      '--task-file',
      '-',
      '--json',
      '--run-id',
      RUN_ID,
      '--repo',
      REPOSITORY,
      '--project',
      PROJECT,
    ])

    confirmer.decide(false)
    await launched
  })

  it('adds the two `=` words, in the row’s order, only when they are filled in', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })
    const launched = held.launch(request({ title: 'the backlog', planTask: 'backlog/triage' }))
    await untilSettled()

    const view = confirmer.opened[0] as ConfirmView
    expect(view.argv.slice(1).join(' ')).toBe(rowArgv({ title: 'the backlog', planTask: 'backlog/triage' }))
    expect(view.argv.filter((word) => word.startsWith('--title'))).toStrictEqual(['--title=the backlog'])
    expect(view.argv.filter((word) => word.startsWith('--plan-task'))).toStrictEqual(['--plan-task=backlog/triage'])

    confirmer.decide(false)
    await launched
  })

  it('keeps a title that begins with `-` inside its own word, where it cannot be read as a flag', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })
    const launched = held.launch(request({ title: '-draft' }))
    await untilSettled()

    const view = confirmer.opened[0] as ConfirmView
    expect(view.argv).toContain('--title=-draft')
    expect(view.argv).not.toContain('-draft')

    confirmer.decide(false)
    await launched
  })

  it('accepts the `=` form for the launch’s row and refuses it for every other', () => {
    expect(RUN_LAUNCH_EQUALS_FLAGS).toStrictEqual(['--title=', '--plan-task='])
    expect(() => assertArgvFlags('run.launch', ['run', '--title=a title'])).not.toThrow()
    expect(() => assertArgvFlags('run.launch', ['run', '--plan-task=a task'])).not.toThrow()
    // The two spellings the design refuses: a bare flag, and an `=` with nothing
    // after it.
    expect(() => assertArgvFlags('run.launch', ['run', '--title'])).toThrow()
    expect(() => assertArgvFlags('run.launch', ['run', '--title='])).toThrow()
    for (const row of ['live.watch', 'runs.ls', 'diagnose', 'plan.status']) {
      expect(() => assertArgvFlags(row, [row, '--title=x'])).toThrow()
      expect(() => assertArgvFlags(row, [row, '--plan-task=x'])).toThrow()
    }
    // The closed exclusion list, word by word: none of these is a flag this
    // console may spawn on the launch's row either.
    const excluded = [
      '--pr',
      '--retry',
      '--attempts',
      '--recipe',
      '--attach',
      '--no-budget-check',
      '--token',
      '--mode',
      '--path',
      '--ref',
      '--workflow',
      '--total-timeout',
      '--no-compression',
      '--gcp-project',
      '--region',
      '--batch',
    ]
    for (const flag of excluded) {
      expect(() => assertArgvFlags('run.launch', ['run', flag])).toThrow()
    }
  })

  it('keeps the task text out of every argv word and out of the spawn log', async () => {
    const marker = 'marker-in-the-task-xyz'
    const spawner = completingSpawner()
    const log = logRecorder()
    const held = runner({ confirmer: answeringConfirmer(true), spawner, log })

    const outcome = await held.launch(request({ task: `${marker} and more` }))
    expect(outcome.ok).toBe(true)

    const call = spawner.calls[0] as SpawnCall
    for (const word of [call.command, ...call.args]) expect(word).not.toContain(marker)
    expect(log.lines).toHaveLength(1)
    for (const line of log.lines) {
      for (const word of line.argv) expect(word).not.toContain(marker)
    }
  })

  it('hands the task to the child on its stdin, ends the stream, and gives it no other descriptor', async () => {
    const spawner = completingSpawner()
    const held = runner({ confirmer: answeringConfirmer(true), spawner })

    await held.launch(request({ task: `  ${TASK}  ` }))

    const call = spawner.calls[0] as SpawnCall
    expect(call.options.stdio).toStrictEqual(['pipe', 'pipe', 'pipe'])
    const child = spawner.children[0] as TypedChild
    expect(child.written).toStrictEqual([TASK])
    expect(child.ended).toStrictEqual([true])
  })
})

describe('the frozen spawn', () => {
  it('spawns the frozen object’s own words and nothing else, and freezes the view', async () => {
    const confirmer = heldConfirmer()
    const spawner = completingSpawner()
    const held = runner({ confirmer, spawner })
    const launched = held.launch(request())
    await untilSettled()

    // The window is handed the display-only view, and the view is what the
    // spawn's own words are read against: the same fields, deep-frozen, with
    // nothing of the plan, the environment or the task's bytes in it.
    const view = confirmer.opened[0] as ConfirmView
    expect(Object.isFrozen(view)).toBe(true)
    expect(Object.isFrozen(view.argv)).toBe(true)

    confirmer.decide(true)
    const outcome = await launched
    expect(outcome.ok).toBe(true)

    const call = spawner.calls[0] as SpawnCall
    expect(call.command).toBe(view.argv[0])
    expect(call.args).toStrictEqual(view.argv.slice(1))
    expect(call.command).toBe(BINARY)
    // The child runs in the console's own private, empty directory, and not in
    // the operator's checkout: the frozen launch's cwd is what it always was.
    expect(call.options.cwd).toBe(privateWorkingDirectory({}))
  })

  it('carries the run id in `task.NewRunID`’s shape and the task’s own size', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })
    const launched = held.launch(request({ task: 'a task with an em dash — in it' }))
    await untilSettled()

    const view = confirmer.opened[0] as ConfirmView
    expect(view.runId).toBe(RUN_ID)
    expect(view.taskText).toBe('a task with an em dash — in it')
    expect(view.taskByteLength).toBe(new TextEncoder().encode('a task with an em dash — in it').byteLength)

    confirmer.decide(false)
    await launched
  })
})

describe('the run id, and the second it is cut in', () => {
  it('redraws the four hex digits when the id it drew is one this process already holds', async () => {
    // The id is the UTC second plus 16 random bits, and it **keys** the two maps
    // the answer path reads: `frozen` (what a yes spawns) and `pending` (whose
    // `decide` an answer resolves). Two launches in one second whose tails
    // collide are therefore one id, and `frozen.set` and `pending.set` overwrite
    // silently -- the first window's yes would resolve the second window's
    // promise and spawn the second launch's frozen object, under a run id both
    // launches share, so nothing downstream could tell. The draw is repeated
    // until it is an id this process does not hold; this test forces the
    // collision by giving the source the same tail twice.
    const confirmer = heldConfirmer()
    const spawner = completingSpawner()
    const held = runner({ confirmer, spawner, random: tails(0xa2fc, 0xa2fc, 0x0001) })

    const first = held.launch(request({ installation: PROJECT }))
    await untilSettled()
    const second = held.launch(request({ installation: OTHER_PROJECT }))
    await untilSettled()

    expect(confirmer.opened.map((launch) => launch.runId)).toStrictEqual([
      RUN_ID,
      '20261028-152618-0001',
    ])

    // And the two windows spawn what each of them froze: the yes on the first
    // installation's window runs its own argv, which is the launch the
    // overwriting `frozen.set` used to replace.
    confirmer.decideWindow(RUN_ID, true)
    await untilSettled()
    expect(spawner.calls).toHaveLength(1)
    expect(spawner.calls[0]?.args).toContain(PROJECT)
    expect(spawner.calls[0]?.args).not.toContain(OTHER_PROJECT)

    confirmer.decideWindow('20261028-152618-0001', false)
    void first
    void second
  })

  it('claims the id with the reservation, so a same-tick pair cannot draw one id', async () => {
    // The draw alone is not the property; the **claim** is. Two messages that
    // arrive in one tick both pass the limits and both start reading their
    // records, and if the id were drawn where the launch is built, the second
    // would draw the first's id while the first was still reading -- nothing
    // frozen, nothing committed, nothing for it to see. Claimed synchronously
    // with the reservation, the second message's first draw is already an id
    // this process holds, so it is the redraw that answers.
    const confirmer = heldConfirmer()
    const held = runner({ confirmer, random: tails(0xa2fc, 0xa2fc, 0x0001) })

    const first = held.launch(request({ installation: PROJECT }))
    const second = held.launch(request({ installation: OTHER_PROJECT }))
    await untilSettled()

    expect(confirmer.opened.map((launch) => launch.runId)).toStrictEqual([RUN_ID, '20261028-152618-0001'])

    for (const open of confirmer.openRunIds) confirmer.decideWindow(open, false)
    void first
    void second
  })

  it('redraws an id this process has already answered, so a replay cannot resolve a new window', async () => {
    // The issued ids are the third place an id can be, and the one that outlives
    // the window: an answered launch is released from `claimedRunIds` and from
    // `inFlight`, and its id stays in `issuedRunIds` because a replayed answer
    // still resolves it (`outcomes` holds that answer; the frozen object itself
    // is dropped as the launch settles). Drawn again, the id would be committed a
    // second time, and `pending.set` would hand the *new* window's promise to the
    // old run id, so the next `answer(RUN_ID, …)` (a replay the renderer is
    // entitled to make) would resolve a window the operator has not answered.
    const confirmer = heldConfirmer()
    const spawner = completingSpawner()
    const held = runner({ confirmer, spawner, random: tails(0xa2fc, 0xa2fc, 0x0001) })

    const first = held.launch(request({ installation: PROJECT }))
    await untilSettled()
    confirmer.decideWindow(RUN_ID, false)
    await first

    // The answered id replays, and the launch that follows it -- in the same
    // second, drawing the same tail -- gets an id of its own.
    expect((await held.answer(RUN_ID, true))?.replayed).toBe(true)

    const second = held.launch(request({ installation: OTHER_PROJECT }))
    await untilSettled()
    expect(confirmer.opened.map((launch) => launch.runId)).toStrictEqual([RUN_ID, '20261028-152618-0001'])

    // And the replay still answers the first launch rather than the window that
    // is open now: the new window keeps its own promise, and nothing spawns.
    expect((await held.answer(RUN_ID, true))?.replayed).toBe(true)
    expect(confirmer.openRunIds).toStrictEqual(['20261028-152618-0001'])
    expect(spawner.calls).toHaveLength(0)

    confirmer.decideWindow('20261028-152618-0001', false)
    void second
  })

  it('frees the id a refused or thrown build claimed, so the next launch can take it', async () => {
    // The claim's release for a launch that never reaches `commit`: a request
    // whose `build` refuses, and one whose records read throws outright, both
    // claimed an id and opened no window, and the id goes back with the
    // reservation. Held, it would burn that id for the rest of the second --
    // and the first launch below proves the release by drawing the same tail
    // again and being given it.
    const confirmer = heldConfirmer()
    const held = runner({
      confirmer,
      random: tails(0xa2fc, 0xa2fc, 0xa2fc),
      lookUp: (project) =>
        project === 'boom'
          ? Promise.reject(new Error('the records read blew up'))
          : Promise.resolve(recordsFor(target(), project)),
    })

    const refused = await held.launch(request({ repository: 'github/acme/other' }))
    expect(refused.ok).toBe(false)
    expect(refused.reason).toContain('carries no checkout')

    await expect(held.launch(request({ installation: 'boom' }))).rejects.toThrow('the records read blew up')

    const second = held.launch(request({ installation: OTHER_PROJECT }))
    await untilSettled()
    expect(confirmer.opened.map((launch) => launch.runId)).toStrictEqual([RUN_ID])

    confirmer.decideWindow(RUN_ID, false)
    void second
  })

  it('refuses the launch when every draw is an id this process already holds', async () => {
    // The other side of the redraw, and the reason it is bounded: a source that
    // keeps answering the same tail cannot be drawn out of the collision, and a
    // shared slot is worse than a refusal -- the operator's yes would be
    // attached to another launch. The window does not open, and the refusal
    // carries no run id, because there is no launch for one to name.
    const confirmer = heldConfirmer()
    const held = runner({ confirmer, random: () => RANDOM() })

    const first = held.launch(request({ installation: PROJECT }))
    await untilSettled()
    const refused = await held.launch(request({ installation: OTHER_PROJECT }))

    expect(refused.ok).toBe(false)
    expect(refused.runId).toBe(null)
    expect(refused.reason).toContain('run ids that another launch of this process already holds')
    expect(confirmer.opened).toHaveLength(1)

    confirmer.decideWindow(RUN_ID, false)
    void first
  })
})

describe('the confirmation seam', () => {
  it('refuses every launch by name as the entry point’s fail-closed default, and spawns nothing', async () => {
    const spawner = quietSpawner()
    const held = runner({ confirmer: DECLINING_CONFIRMER, spawner })

    const outcome = await held.launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toBe(DECLINED_UNBUILT)
    expect(outcome.runId).toBe(RUN_ID)
    expect(outcome.outcomeUnknown).toBe(false)
    expect(spawner.calls).toHaveLength(0)
  })

  it('answers a window the operator declined with the cancel sentence, and spawns nothing', async () => {
    const confirmer = heldConfirmer()
    const spawner = quietSpawner()
    const held = runner({ confirmer, spawner })
    const launched = held.launch(request())
    await untilSettled()

    confirmer.decide(false)
    const outcome = await launched
    expect(outcome.reason).toBe(DECLINED_CANCELLED)
    expect(outcome.ok).toBe(false)
    expect(spawner.calls).toHaveLength(0)
  })

  it('turns a launch whose installation left the records while the window was open into a cancel', async () => {
    const confirmer = heldConfirmer()
    const spawner = quietSpawner()
    const held = runner({ confirmer, spawner, stillRecorded: () => false })
    const launched = held.launch(request())
    await untilSettled()

    confirmer.decide(true)
    const outcome = await launched
    expect(outcome.reason).toBe(DECLINED_CANCELLED)
    expect(spawner.calls).toHaveLength(0)
  })

  it('answers a second yes from the record it already has, and spawns one child in all', async () => {
    const confirmer = heldConfirmer()
    const spawner = completingSpawner()
    const held = runner({ confirmer, spawner })
    const launched = held.launch(request())
    await untilSettled()

    const runId = (confirmer.opened[0] as ConfirmView).runId
    // The window's own answer and the launch's are one outcome: the answer
    // attaches to the launch in flight rather than resolving to nothing.
    const answered = await held.answer(runId, true)
    const first = await launched
    expect(first.ok).toBe(true)
    expect(answered).toStrictEqual(first)

    const replayed = await held.answer(runId, true)
    expect(replayed?.ok).toBe(true)
    expect(replayed?.replayed).toBe(true)
    expect(spawner.calls).toHaveLength(1)
  })

  it('answers a run id this process never opened with nothing at all', async () => {
    const held = runner({ confirmer: DECLINING_CONFIRMER })
    expect(await held.answer('20261028-152618-0000', true)).toBeUndefined()
  })

  it('attaches a second answer to the launch in flight, and starts one child in all', async () => {
    // The in-flight case of rule 11's idempotency paragraph: a second answer for
    // the same window's run id -- the operator's second click, a re-sent IPC
    // call -- arrives after the window's own `decide` has been resolved and while
    // the child is still running. It is attached to the launch in flight: it
    // waits on it and gets its answer, and no second child starts.
    const confirmer = heldConfirmer()
    const spawner = heldSpawner()
    const held = runner({ confirmer, spawner })
    const launched = held.launch(request())
    await untilSettled()

    const runId = (confirmer.opened[0] as ConfirmView).runId
    confirmer.decideWindow(runId, true)
    await untilSettled()
    expect(spawner.calls).toHaveLength(1)

    const first = held.answer(runId, true)
    const second = held.answer(runId, true)

    const child = spawner.children[0] as TypedChild
    child.emitStdout(launchResult((spawner.calls[0] as SpawnCall).args))
    child.exit(0, null)

    const [one, two, outcome] = await Promise.all([first, second, launched])
    expect(one).toBeDefined()
    expect(two).toBeDefined()
    for (const answer of [one, two, outcome]) {
      expect(answer?.ok).toBe(true)
      expect(answer?.runId).toBe(runId)
      expect(answer?.result?.run).toBe('run-1')
    }
    expect(spawner.calls).toHaveLength(1)
  })

  it('answers a no for a window that is still open with the cancel refusal, and spawns nothing', async () => {
    const confirmer = heldConfirmer()
    const spawner = completingSpawner()
    const held = runner({ confirmer, spawner })
    const launched = held.launch(request())
    await untilSettled()

    const runId = (confirmer.opened[0] as ConfirmView).runId
    const declined = await held.answer(runId, false)
    expect(declined?.ok).toBe(false)
    expect(declined?.runId).toBe(runId)
    expect(declined?.reason).toBe(DECLINED_CANCELLED)
    expect((await launched).reason).toBe(DECLINED_CANCELLED)
    expect(spawner.calls).toHaveLength(0)
  })
})

describe('the cancel a removal or a re-choose asks for', () => {
  it('decides nothing for a project with no launch in flight', async () => {
    const closing = closingConfirmer()
    const spawner = quietSpawner()
    const held = runner({ confirmer: closing.confirmer, spawner })

    // The first run of the console, an installation never launched, and a
    // project whose launch has already settled: there is no window to close and
    // nothing here decides.
    held.cancelFor('no-such-project')
    held.cancelFor(PROJECT)
    await untilSettled()
    expect(closing.closed).toStrictEqual([])
    expect(closing.opened).toStrictEqual([])
    expect(spawner.calls).toStrictEqual([])
  })

  it('closes the window the project has open, and spawns nothing', async () => {
    const closing = closingConfirmer()
    const spawner = quietSpawner()
    const held = runner({ confirmer: closing.confirmer, spawner })
    const launched = held.launch(request())
    await untilSettled()
    expect(closing.opened).toHaveLength(1)

    held.cancelFor(PROJECT)
    expect(closing.closed).toStrictEqual([RUN_ID])
    const outcome = await launched
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toBe(DECLINED_CANCELLED)
    expect(spawner.calls).toStrictEqual([])

    // The window is spent with its answer: a second cancel names nothing more.
    held.cancelFor(PROJECT)
    expect(closing.closed).toStrictEqual([RUN_ID])
  })

  it('declines a launch whose build is still running, without opening a window', async () => {
    const closing = closingConfirmer()
    const spawner = quietSpawner()
    const held = runner({ confirmer: closing.confirmer, spawner })
    // Called before the launch's first `await`, so the project's entry is the
    // reservation **token** and there is no run id to close yet: closing the run
    // id alone would miss the window that opens a moment later.
    const launched = held.launch(request())
    held.cancelFor(PROJECT)

    const outcome = await launched
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toBe(DECLINED_CANCELLED)
    expect(closing.opened).toStrictEqual([])
    expect(closing.closed).toStrictEqual([])
    expect(spawner.calls).toStrictEqual([])

    // And the installation is not locked out by it: the slot went back with the
    // decline, so the next request opens its window as usual.
    const again = held.launch(request())
    await untilSettled()
    expect(closing.opened).toHaveLength(1)
    closing.decide(false)
    expect((await again).reason).toBe(DECLINED_CANCELLED)
  })

  it('leaves a child that is already running alone', async () => {
    const closing = closingConfirmer()
    const spawner = quietSpawner()
    const held = runner({ confirmer: closing.confirmer, spawner })
    const caught = catchLaunchMaps()
    try {
      const launched = held.launch(request())
      await untilSettled()
      closing.decide(true)
      await untilSettled()
      expect(spawner.calls).toHaveLength(1)
      expect(closing.openRunIds()).toStrictEqual([])

      // A launch this process has spawned is not cancellable from here: no
      // window is open, so `close` decides nothing, and no second child starts.
      held.cancelFor(PROJECT)
      await untilSettled()
      expect(closing.closed).toStrictEqual([RUN_ID])
      expect(spawner.calls).toHaveLength(1)
      expect(caught.outcomes()).toBeUndefined()
      void launched
    } finally {
      caught.restore()
    }
  })
})

describe('the refusals come before the window', () => {
  it('refuses an installation its records do not carry, before any window', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer, target: target({ project: '' }) })

    const outcome = await held.launch(request({ installation: 'gone' }))
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toContain('gone')
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses records that answer for an installation other than the one asked about', async () => {
    // The in-flight caps are read against the name a renderer sent and the hold
    // is taken against the frozen object's own project. A lookup answering with
    // another installation's records would make those two different keys, so the
    // launcher refuses the disagreement rather than launching the wrong one.
    const confirmer = heldConfirmer()
    const held = runner({
      confirmer,
      lookUp: () => ({ ok: true, target: target({ project: OTHER_PROJECT }) }),
    })

    const outcome = await held.launch(request({ installation: PROJECT }))
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toContain(OTHER_PROJECT)
    expect(outcome.reason).toContain(PROJECT)
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses an installation whose records set a config path of their own', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer, target: target({ configPath: '/home/person-1/elsewhere.yaml' }) })

    const outcome = await held.launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toContain('config path')
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses a CLI below the launch’s floor', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer, target: target({ cliVersion: '0.11.1' }) })

    const outcome = await held.launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toContain('0.11.1')
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses an installation that runs its CLI inside a distribution, by name', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer, target: target({ distro: 'Ubuntu-22.04' }) })

    const outcome = await held.launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toContain('Ubuntu-22.04')
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses a repository the conversion does not admit', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })

    const outcome = await held.launch(request({ repository: 'gitlab/acme/app' }))
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toContain('gitlab')
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses a repository its records do not carry', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })

    const outcome = await held.launch(request({ repository: 'github/acme/other' }))
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toContain('acme/other')
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses an installation that records no base branch, naming the two settings that move', async () => {
    const confirmer = heldConfirmer()
    const held = runner({
      confirmer,
      target: target({
        records: { baseBranch: '', repositories: [{ repository: RECORDED_REPOSITORY, baseBranch: '' }] },
      }),
    })

    const outcome = await held.launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toContain('repos.acme/app.base_branch')
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses a CLI too old to report branches, in a sentence about the read rather than the setting', async () => {
    const confirmer = heldConfirmer()
    const held = runner({
      confirmer,
      target: target({
        records: { baseBranch: null, repositories: [{ repository: RECORDED_REPOSITORY, baseBranch: null }] },
      }),
    })

    const outcome = await held.launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toContain('does not report base branches')
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses a recorded branch that is not a branch', async () => {
    const confirmer = heldConfirmer()
    const held = runner({
      confirmer,
      target: target({
        records: { baseBranch: '-main', repositories: [{ repository: RECORDED_REPOSITORY, baseBranch: null }] },
      }),
    })

    const outcome = await held.launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toContain('branch')
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses an over-long task and an over-long title, each in its own sentence', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })

    const longTask = await held.launch(request({ task: 'x'.repeat(16385) }))
    expect(longTask.ok).toBe(false)
    expect(longTask.reason).toContain('16384')

    const longTitle = await held.launch(request({ title: 'x'.repeat(101) }))
    expect(longTitle.ok).toBe(false)
    expect(longTitle.reason).toContain('100')
    expect(confirmer.opened).toHaveLength(0)
  })
})

describe('the launch limits', () => {
  it('asks one installation to confirm at most three launches a minute', async () => {
    let at = NOW
    const confirmer = answeringConfirmer(false)
    const held = runner({ confirmer, now: () => at })

    for (let attempt = 0; attempt < CONFIRM_RATE_PER_INSTALLATION; attempt += 1) {
      expect((await held.launch(request())).reason).toBe(DECLINED_CANCELLED)
    }
    const refused = await held.launch(request())
    expect(refused.ok).toBe(false)
    expect(refused.reason).toContain('3 launches')
    expect(confirmer.opened).toHaveLength(CONFIRM_RATE_PER_INSTALLATION)

    at += 60_000
    expect((await held.launch(request())).reason).toBe(DECLINED_CANCELLED)
  })

  it('asks the whole app to confirm at most six launches a minute', async () => {
    const confirmer = answeringConfirmer(false)
    const held = runner({ confirmer })

    for (let attempt = 0; attempt < CONFIRM_RATE_APP; attempt += 1) {
      expect((await held.launch(request({ installation: `installation-${String(attempt)}` }))).reason).toBe(
        DECLINED_CANCELLED,
      )
    }
    const refused = await held.launch(request({ installation: 'one-too-many' }))
    expect(refused.ok).toBe(false)
    expect(refused.reason).toContain('6 launches')
    expect(confirmer.opened).toHaveLength(CONFIRM_RATE_APP)
  })

  it('drops an installation once its stamps have expired, so the names cannot pile up', async () => {
    // The rate map is keyed by whatever name the renderer sent, so an entry that
    // outlives its window is memory the renderer chooses: at the app-wide cap of
    // six a minute, a stream of names is 8,600 small entries a day, for the life
    // of the process. The entry is dropped with its last stamp, and this reads
    // the map's own keys -- it is in a closure, and which names it still holds is
    // the whole property.
    let at = NOW
    const held = runner({ confirmer: DECLINING_CONFIRMER, now: () => at })
    const caught = catchRateMap()
    try {
      await held.launch(request({ installation: PROJECT }))
      expect(caught.keys()).toStrictEqual([PROJECT])

      at += CONFIRM_RATE_WINDOW_MS
      await held.launch(request({ installation: OTHER_PROJECT }))
      expect(caught.keys()).toStrictEqual([OTHER_PROJECT])
    } finally {
      caught.restore()
    }
  })

  it('holds a launch’s frozen object only while its answer is unsettled', async () => {
    // The frozen object carries the task text and the plan, and both are needed
    // only until the answer settles: the yes spawns from it, and a replay is
    // served from `outcomes` instead. Held for the life of the process it would
    // be one task text per launch ever confirmed.
    const confirmer = heldConfirmer()
    const spawner = quietSpawner()
    const held = runner({ confirmer, spawner })
    const caught = catchLaunchMaps()
    try {
      const launched = held.launch(request())
      await untilSettled()
      expect(caught.frozen()).toBe(1)
      expect(caught.outcomes()).toBeUndefined()

      confirmer.decideWindow(RUN_ID, false)
      await launched
      expect(caught.frozen()).toBe(0)
      expect(caught.outcomes()).toBe(1)
    } finally {
      caught.restore()
    }
  })

  it('caps the outcomes it replays, and answers an evicted id with nothing', async () => {
    // Four past the cap, so the cap is what bounds the record rather than the
    // number of launches this test happens to make: the oldest answer is gone
    // and the newest is still there.
    let at = NOW
    const confirmer = answeringConfirmer(true)
    const spawner = completingSpawner()
    const held = runner({ confirmer, spawner, now: () => at, wallClock: () => NOW })
    const caught = catchLaunchMaps()
    const launches = MAX_RECORDED_OUTCOMES + 4
    try {
      for (let attempt = 0; attempt < launches; attempt += 1) {
        const outcome = await held.launch(request({ installation: `installation-${String(attempt)}` }))
        expect(outcome.ok).toBe(true)
        // Past both rate windows and past the cool-down, so every launch below
        // is a first one for its own installation rather than a refusal.
        at += CONFIRM_RATE_WINDOW_MS
      }
      expect(caught.outcomes()).toBe(MAX_RECORDED_OUTCOMES)
      expect(caught.frozen()).toBe(0)

      const ids = confirmer.opened.map((view) => view.runId)
      expect(ids).toHaveLength(launches)
      // The evicted id is not a launch this process can speak for any more, and
      // answering it spawns nothing.
      expect(await held.answer(ids[0] as string, true)).toBeUndefined()
      expect((await held.answer(ids[ids.length - 1] as string, true))?.replayed).toBe(true)
      expect(spawner.calls).toHaveLength(launches)
    } finally {
      caught.restore()
    }
  })

  it('never draws an id it has already issued, even once its answer is evicted', async () => {
    let at = NOW
    const confirmer = answeringConfirmer(true)
    const spawner = completingSpawner()
    let tail = 0xa2fc
    const queued: number[] = []
    const random = (): number => {
      const next = queued.length > 0 ? (queued.shift() as number) : tail++
      return next / 0x10000
    }
    const held = runner({ confirmer, spawner, now: () => at, wallClock: () => NOW, random })
    const launches = MAX_RECORDED_OUTCOMES + 1
    for (let attempt = 0; attempt < launches; attempt += 1) {
      expect((await held.launch(request({ installation: `installation-${String(attempt)}` }))).ok).toBe(true)
      at += CONFIRM_RATE_WINDOW_MS
    }
    const evicted = (confirmer.opened[0] as ConfirmView).runId
    expect(evicted).toBe(RUN_ID)
    expect(await held.answer(evicted, true)).toBeUndefined()

    // A fresh launch whose first draw lands on that evicted id is refused it and
    // redraws: the issued ids are kept whether or not the answer is, so a drawn
    // id is never one this process has already committed.
    queued.push(0xa2fc, 0x1234)
    const fresh = held.launch(request({ installation: 'fresh' }))
    await untilSettled()
    expect((confirmer.opened[confirmer.opened.length - 1] as ConfirmView).runId).toBe('20261028-152618-1234')
    expect((await fresh).ok).toBe(true)
    expect(spawner.calls).toHaveLength(launches + 1)
  })

  it('pauses an installation for ten seconds after an accepted launch', async () => {
    let at = NOW
    const spawner = completingSpawner()
    const held = runner({ confirmer: answeringConfirmer(true), spawner, now: () => at })

    expect((await held.launch(request())).ok).toBe(true)
    const paused = await held.launch(request())
    expect(paused.ok).toBe(false)
    expect(paused.reason).toContain('10 seconds')

    at += LAUNCH_COOL_DOWN_MS
    expect((await held.launch(request())).ok).toBe(true)
    expect(spawner.calls).toHaveLength(2)
  })

  it('refuses a second launch for an installation already in flight rather than queueing it', async () => {
    const confirmer = answeringConfirmer(true)
    const spawner = quietSpawner()
    const held = runner({ confirmer, spawner })

    const first = held.launch(request())
    await untilSettled()

    const refused = await held.launch(request())
    expect(refused.ok).toBe(false)
    expect(refused.reason).toContain('already in flight')
    expect(spawner.calls).toHaveLength(1)

    void first
  })

  it('counts a window that is still open, for the installation and across the app', async () => {
    // The caps cover the whole unsettled interval and not only the child: a
    // renderer leaning on this operation sends its messages in one tick, and
    // every one of them arrives before any child exists -- and before any
    // cool-down, which starts at an *accepted* launch. Held only from the
    // spawn, both refusals below would be untrue of exactly the burst they
    // exist for.
    const confirmer = heldConfirmer()
    const spawner = quietSpawner()
    const held = runner({ confirmer, spawner })

    const first = held.launch(request({ installation: PROJECT }))
    await untilSettled()
    expect(confirmer.opened).toHaveLength(1)

    const sameInstallation = await held.launch(request({ installation: PROJECT }))
    expect(sameInstallation.ok).toBe(false)
    expect(sameInstallation.reason).toContain('already in flight')
    expect(confirmer.opened).toHaveLength(1)

    const second = held.launch(request({ installation: OTHER_PROJECT }))
    await untilSettled()
    expect(confirmer.opened).toHaveLength(2)

    const third = await held.launch(request({ installation: 'third' }))
    expect(third.ok).toBe(false)
    expect(third.reason).toContain('two launches are already in flight')
    expect(confirmer.opened).toHaveLength(2)
    expect(spawner.calls).toHaveLength(0)

    // Answering a window releases its hold, and only its own: the other
    // installation's window is still open, and the answered installation's
    // entry is gone -- so it can be asked for again, and the app-wide cap is
    // back at two and refuses a third installation once more.
    confirmer.decideWindow(RUN_ID, false)
    await untilSettled()
    expect(confirmer.openRunIds).toHaveLength(1)

    const again = held.launch(request({ installation: PROJECT }))
    await untilSettled()
    expect(confirmer.opened).toHaveLength(3)
    expect(confirmer.openRunIds).toHaveLength(2)

    const fourth = await held.launch(request({ installation: 'third' }))
    expect(fourth.ok).toBe(false)
    expect(fourth.reason).toContain('two launches are already in flight')

    for (const leftover of confirmer.openRunIds) confirmer.decideWindow(leftover, false)
    void first
    void second
    void again
  })

  it('opens one window for a same-tick burst on one installation, and two app-wide', async () => {
    // The state the reservation exists for. Every message of a burst arrives in
    // one tick, so every one of them reads the limits before any of them has
    // written: with the hold and the rate stamp taken after `build` was awaited,
    // one installation's five messages opened **five** windows against caps of
    // one in flight and three a minute, and the second window's hold overwrote
    // the first's entry for that installation -- which is how the caps stopped
    // counting it at all. The reservation is synchronous and it is the last
    // thing before the first `await`, so the second message already sees the
    // first.
    const confirmer = heldConfirmer()
    const spawner = quietSpawner()
    const held = runner({ confirmer, spawner })

    const oneInstallation = [0, 1, 2, 3, 4].map(() => held.launch(request({ installation: PROJECT })))
    await untilSettled()
    expect(confirmer.opened).toHaveLength(1)
    expect(spawner.calls).toHaveLength(0)
    for (const launch of confirmer.opened) confirmer.decideWindow(launch.runId, false)
    const refused = (await Promise.all(oneInstallation)).filter((outcome) =>
      outcome.reason.includes('already in flight'),
    )
    expect(refused).toHaveLength(4)

    // ...and across the app: eight installations, two windows, six refusals --
    // not eight windows, and not the third and fourth installations' messages
    // slipping past the app-wide cap on their way to the rate check.
    const appConfirmer = heldConfirmer()
    const appHeld = runner({ confirmer: appConfirmer, spawner: quietSpawner() })
    const app = ['one', 'two', 'three', 'four', 'five', 'six', 'seven', 'eight'].map((installation) =>
      appHeld.launch(request({ installation })),
    )
    await untilSettled()
    expect(appConfirmer.opened).toHaveLength(2)
    for (const launch of appConfirmer.opened) appConfirmer.decideWindow(launch.runId, false)
    const appRefused = (await Promise.all(app)).filter((outcome) =>
      outcome.reason.includes('two launches are already in flight'),
    )
    expect(appRefused).toHaveLength(6)
  })

  it('charges a build that refused its rate stamp, and frees the slot it had reserved', async () => {
    // Two halves of the reservation. A request that cannot be built still made
    // this process read the records, so it costs a rate stamp like any other --
    // otherwise a renderer could ask a launch that never builds without limit.
    // And nothing is in flight for it, so the installation's slot and the
    // app-wide count are freed: a reservation that leaked here would refuse a
    // real launch two leaks later with a sentence about launches in flight that
    // are not in flight at all.
    const spawner = quietSpawner()
    const unbuildable = runner({ confirmer: heldConfirmer(), spawner, target: { ...target(), project: '' } })
    for (let attempt = 0; attempt < CONFIRM_RATE_PER_INSTALLATION; attempt += 1) {
      const outcome = await unbuildable.launch(request())
      expect(outcome.ok).toBe(false)
      expect(outcome.reason).toContain('records carry')
    }
    expect((await unbuildable.launch(request())).reason).toContain(
      `${String(CONFIRM_RATE_PER_INSTALLATION)} launches`,
    )
    expect(spawner.calls).toHaveLength(0)

    const mixed = runner({
      confirmer: answeringConfirmer(true),
      spawner: completingSpawner(),
      lookUp: (project) =>
        project === PROJECT
          ? { ok: true, target: target() }
          : { ok: false, reason: `no installation named ${JSON.stringify(project)} is one this console’s records carry` },
    })
    for (const installation of ['gone-one', 'gone-two', 'gone-three']) {
      expect((await mixed.launch(request({ installation }))).ok).toBe(false)
    }
    expect((await mixed.launch(request({ installation: PROJECT }))).ok).toBe(true)
  })

  it('reads the limits from a clock that only moves forward, never from the wall clock', async () => {
    // The limits are elapsed-time bounds, and the wall clock is a value the
    // operator, NTP or a machine waking from sleep can move. A cool-down bound
    // to `Date.now` is an hour in the past the moment the clock is set forward,
    // which frees a launch the operator has just been told to wait ten seconds
    // for; set back, it locks the installation out until real time catches up.
    // The limits therefore read a monotonic clock (`performance.now`), and the
    // wall clock is kept for the one thing that needs a date: the run id.
    //
    // The launcher is built here **without** `now` or `wallClock`, so what this
    // case exercises is the production default itself and not the helper's
    // injected stand-in for it: a default reverted to `Date.now` would pass a
    // test whose clocks the helper supplies.
    const spawner = completingSpawner()
    const held = createLaunchRunner({
      confirmer: answeringConfirmer(true),
      stillRecorded: () => true,
      appEnv: { HOME: '/home/person-1' },
      spawner,
      timers: neverFires(),
      random: countingRandom(),
      lookUp: (project) => Promise.resolve(recordsFor(target(), project)),
    })
    expect((await held.launch(request())).ok).toBe(true)

    const real = Date.now
    const moved = vi.spyOn(Date, 'now').mockImplementation(() => real() + 3_600_000)
    try {
      const paused = await held.launch(request())
      expect(paused.ok).toBe(false)
      expect(paused.reason).toContain('10 seconds')
      expect(spawner.calls).toHaveLength(1)
    } finally {
      moved.mockRestore()
    }
  })

  it('runs at most two launches at once across the app, and refuses the third rather than queueing it', async () => {
    const confirmer = answeringConfirmer(true)
    const spawner = quietSpawner()
    const held = runner({ confirmer, spawner })

    void held.launch(request({ installation: PROJECT }))
    await untilSettled()
    void held.launch(request({ installation: OTHER_PROJECT }))
    await untilSettled()

    const refused = await held.launch(request({ installation: 'third' }))
    expect(refused.ok).toBe(false)
    expect(refused.reason).toContain('two launches are already in flight')
    expect(spawner.calls).toHaveLength(2)
  })

  it('asks the child for a launch’s own deadline, which is not a read’s', async () => {
    const timers = recordingTimers()
    const held = runner({ confirmer: answeringConfirmer(true), spawner: completingSpawner(), timers })

    await held.launch(request())
    expect(timers.delays).toStrictEqual([LAUNCH_TIMEOUT_MS])
    expect(LAUNCH_TIMEOUT_MS).toBe(120_000)
  })
})

describe('the reply', () => {
  it('refuses a reply about another run, and another repository, each by name', async () => {
    const runOutcome = await runner({
      confirmer: answeringConfirmer(true),
      spawner: completingSpawner({ overrides: { run_id: '20261028-152618-0001' } }),
    }).launch(request())
    expect(runOutcome.ok).toBe(false)
    expect(runOutcome.reason).toContain('20261028-152618-0001')

    const repoOutcome = await runner({
      confirmer: answeringConfirmer(true),
      spawner: completingSpawner({ overrides: { repo: 'acme/elsewhere' } }),
    }).launch(request())
    expect(repoOutcome.ok).toBe(false)
    expect(repoOutcome.reason).toContain('acme/elsewhere')
  })

  it('reads a reply carrying a key this console does not know, and ignores the key', async () => {
    // The reply arrives after the spend: a field a newer CLI added must not
    // turn a launch that happened into a refusal (the owner's ruling of
    // 2026-10-30).
    const outcome = await runner({
      confirmer: answeringConfirmer(true),
      spawner: completingSpawner({ overrides: { surprise: 'yes' } }),
    }).launch(request())
    expect(outcome.ok).toBe(true)
    expect(outcome.reason).not.toContain('surprise')
  })

  it('reports a child killed by a signal as unknown, and never as an exit status', async () => {
    // A signal is not an exit status: a CLI killed from outside answers
    // `code: null`, and reading that as "exited with status null" reports a
    // launch the console knows nothing about as one that did not happen. It may
    // have claimed the run id and started a container before it was killed, so
    // the answer keeps the run id and says the outcome is unknown.
    const child = typedChild()
    const spawner: ChildSpawner = () => {
      queueMicrotask(() => child.exit(null, 'SIGKILL'))
      return child
    }
    const outcome = await runner({ confirmer: answeringConfirmer(true), spawner }).launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.outcomeUnknown).toBe(true)
    expect(outcome.runId).toBe(RUN_ID)
    expect(outcome.reason).toContain('SIGKILL')
    expect(outcome.reason).toContain(RUN_ID)
    expect(outcome.reason).toContain('fugaro runs ls --json')
    expect(outcome.reason).not.toContain('status null')
  })

  it('refuses a reply from a CLI that exited non-zero, in the CLI’s own words', async () => {
    const child = typedChild()
    const spawner: ChildSpawner = () => {
      queueMicrotask(() => {
        child.emitStderr('the budget is exhausted\n')
        child.exit(1, null)
      })
      return child
    }
    const outcome = await runner({ confirmer: answeringConfirmer(true), spawner }).launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toBe('the budget is exhausted')
  })
})

describe('a launch that was stopped, and one that failed outright', () => {
  it('names the run id it cannot speak for, and never says the launch did not happen', () => {
    const sentence = launchOutcomeUnknown(RUN_ID, 'the launch stopped answering')
    expect(sentence).toContain(RUN_ID)
    expect(sentence).toContain('unknown')
    expect(sentence).toContain('fugaro runs ls --json')
    expect(sentence).toContain(`fugaro diagnose ${RUN_ID} --json`)
    expect(sentence).not.toContain('not launched')
  })

  it('declines, and frees the hold, when the confirmer itself throws', async () => {
    // A confirmer is a seam the next part fills, and a window that cannot be
    // opened is a launch nobody can answer. Thrown out of the operation, the
    // hold taken for this launch would never be released -- no answer is coming
    // -- so the installation could not be launched again for the life of the
    // process. It settles as a decline, which spawns nothing.
    const held = heldConfirmer()
    let calls = 0
    const confirmer: LaunchConfirmer = {
      open(launch, decide) {
        calls += 1
        if (calls === 1) throw new Error('the confirmation window is gone')
        held.open(launch, decide)
      },
      close(runId) {
        held.close(runId)
      },
    }
    const spawner = quietSpawner()
    const launcher = runner({ confirmer, spawner })

    const outcome = await launcher.launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toBe(DECLINED_CANCELLED)
    expect(outcome.outcomeUnknown).toBe(false)
    expect(spawner.calls).toHaveLength(0)

    // The hold is gone, and only this launch's: the very next launch for the
    // same installation opens a window rather than being refused as in flight.
    const second = launcher.launch(request())
    await untilSettled()
    expect(held.opened).toHaveLength(1)
    for (const launch of held.opened) held.decideWindow(launch.runId, false)
    expect((await second).reason).toBe(DECLINED_CANCELLED)
  })

  it('settles as a cancel, spawning nothing, when an open window is closed by name', async () => {
    // Rule 11: an installation removed or re-chosen while the window is open
    // closes the window as a cancel. The caller -- `index.ts`, which is watching
    // the records -- names the run; the seam's `close` is how it says so, and
    // what comes back is the ordinary decline: no child, and the console's own
    // cancel sentence rather than the CLI's.
    const confirmer = heldConfirmer()
    const spawner = quietSpawner()
    const launcher = runner({ confirmer, spawner })

    const launched = launcher.launch(request())
    await untilSettled()
    expect(confirmer.opened).toHaveLength(1)
    const window = confirmer.opened[0] as ConfirmView
    expect(confirmer.openRunIds).toStrictEqual([window.runId])

    confirmer.close(window.runId)
    const outcome = await launched
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toBe(DECLINED_CANCELLED)
    expect(spawner.calls).toHaveLength(0)
    // A close for a run id nothing has open decides nothing, so a second one
    // after the answer changes no outcome.
    confirmer.close(window.runId)
    expect(confirmer.openRunIds).toStrictEqual([])
  })

  it('declines, and frees the hold, when the still-recorded read rejects', async () => {
    // The other way a window is answered leaves a hold behind: `stillRecorded`
    // reads the console's own records and the CLI's list, either of which can
    // fail. A rejection let out of the launcher would leave the hold taken, and
    // an installation the operator can never launch again is a denial of
    // service this console performs on itself. A read that cannot answer
    // answers no, which spawns nothing.
    const spawner = quietSpawner()
    const launcher = runner({
      confirmer: answeringConfirmer(true),
      spawner,
      stillRecorded: () => Promise.reject(new Error('the store is unreadable')),
    })

    const outcome = await launcher.launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toBe(DECLINED_CANCELLED)
    expect(outcome.outcomeUnknown).toBe(false)
    expect(spawner.calls).toHaveLength(0)

    const second = await launcher.launch(request())
    expect(second.reason).toBe(DECLINED_CANCELLED)
    expect(second.reason).not.toContain('already in flight')
  })

  it('reports a spawn that threw as unknown, keeps the run id, and releases the in-flight entry', async () => {
    let at = NOW
    const held = runner({
      confirmer: answeringConfirmer(true),
      spawner: failingSpawner('the binary went away'),
      now: () => at,
    })

    const outcome = await held.launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.outcomeUnknown).toBe(true)
    expect(outcome.runId).toBe(RUN_ID)
    expect(outcome.reason).toContain(RUN_ID)
    expect(outcome.reason).toContain('the binary went away')

    // The entry is released rather than retried -- the second launch is neither
    // refused as in flight nor the first one again, and it is a launch of its
    // own with an id of its own. The clock moves past the accepted launch's
    // cool-down first, so what the second launch is refused or accepted by is
    // the in-flight entry alone.
    at += LAUNCH_COOL_DOWN_MS
    const second = await held.launch(request())
    expect(second.reason).not.toContain('already in flight')
    expect(second.runId).not.toBe(RUN_ID)
    expect(second.outcomeUnknown).toBe(true)
  })
})

// The flow's own answer about itself, and the one thing the update policy is
// allowed to know about this file (desktop-console plan Task 4.4, design
// section 7.3): a signed update replaces the process that owns the launch write
// path, so nothing prompts or installs while a launch is in flight.
// ---------------------------------------------------------------------------

describe('the flow’s answer about itself', () => {
  it('is busy from the reservation until the launch is released, and idle either side', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })
    expect(held.isIdle()).toBe(true)

    const launched = held.launch(request())
    // The reservation is taken **before** `build`'s first `await`, so the flow
    // is busy from the moment the call is made: a check that ran in the window
    // between the call and the window opening would be reading a flow that is
    // about to have a launch in it.
    expect(held.isIdle()).toBe(false)
    await untilSettled()
    expect(held.isIdle()).toBe(false)

    confirmer.decide(false)
    await launched
    expect(held.isIdle()).toBe(true)
  })

  it('tells its idle listeners once per transition, and never for a release that leaves it busy', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })
    let told = 0
    held.onIdle(() => {
      told += 1
    })

    const first = held.launch(request())
    await untilSettled()
    expect(told).toBe(0)
    confirmer.decide(false)
    await first
    expect(told).toBe(1)

    // A second launch, and a second transition: the listener is called for each
    // one, which is what makes "a download that finished mid-launch is offered
    // when that launch ends" true for the launch after it too.
    const second = held.launch(request())
    await untilSettled()
    confirmer.decide(false)
    await second
    expect(told).toBe(2)
  })

  it('tells nobody while another launch is still in flight', async () => {
    // The release of one launch is not the flow going idle when a second is
    // still held: a listener that fired there would offer an update while
    // another launch's record is still being written, which is the whole thing
    // this answer exists to prevent.
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })
    let told = 0
    held.onIdle(() => {
      told += 1
    })

    const first = held.launch(request())
    await untilSettled()
    const second = held.launch(request({ installation: OTHER_PROJECT }))
    await untilSettled()
    const ids = [...confirmer.openRunIds]
    expect(ids).toHaveLength(2)

    confirmer.decideWindow(ids[0] as string, false)
    await first
    expect(held.isIdle()).toBe(false)
    expect(told).toBe(0)

    confirmer.decideWindow(ids[1] as string, false)
    await second
    expect(held.isIdle()).toBe(true)
    expect(told).toBe(1)
  })
})
