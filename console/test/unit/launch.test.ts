// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
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
//   - a killed launch reported as "not launched" fails "names the run id it
//     cannot speak for, and never says the launch did not happen".

import { describe, expect, it } from 'vitest'

import {
  CONFIRM_RATE_APP,
  CONFIRM_RATE_PER_INSTALLATION,
  DECLINED_CANCELLED,
  DECLINED_UNBUILT,
  DECLINING_CONFIRMER,
  LAUNCH_COOL_DOWN_MS,
  LAUNCH_TIMEOUT_MS,
  createLaunchRunner,
  launchOutcomeUnknown,
} from '../../src/main/launch'
import type { FrozenLaunch, LaunchConfirmer, LaunchTarget, TargetLookup } from '../../src/main/launch'
import { RUN_LAUNCH_EQUALS_FLAGS, assertArgvFlags, spawnedArgv } from '../../src/main/spawn'
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
  readonly opened: readonly FrozenLaunch[]
  readonly openRunIds: readonly string[]
  decide(yes: boolean): void
  decideWindow(runId: string, yes: boolean): void
} {
  const opened: FrozenLaunch[] = []
  const waiting = new Map<string, (yes: boolean, reason?: string) => void>()
  const decideWindow = (runId: string, yes: boolean): void => {
    const held = waiting.get(runId)
    waiting.delete(runId)
    held?.(yes)
  }
  return {
    opened,
    open(launch, answer) {
      opened.push(launch)
      waiting.set(launch.runId, answer)
    },
    decide(yes) {
      const oldest = [...waiting.keys()][0]
      if (oldest !== undefined) decideWindow(oldest, yes)
    },
    decideWindow,
    get openRunIds(): readonly string[] {
      return [...waiting.keys()]
    },
  }
}

/** answeringConfirmer is the other half: it answers every window as it opens. */
function answeringConfirmer(yes: boolean): LaunchConfirmer & { readonly opened: readonly FrozenLaunch[] } {
  const opened: FrozenLaunch[] = []
  return {
    opened,
    open(launch, decide) {
      opened.push(launch)
      decide(yes)
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

function target(overrides: Partial<LaunchTarget> = {}): LaunchTarget {
  return {
    project: PROJECT,
    binaryPath: BINARY,
    cliVersion: CLI_VERSION,
    records: { baseBranch: BRANCH, repositories: [{ repository: RECORDED_REPOSITORY, baseBranch: '' }] },
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
  readonly now?: () => number
  readonly stillRecorded?: () => boolean
  /** `lookUp` replaces the records entirely: the one test that needs a lookup
   * answering for an installation other than the one asked about cannot use the
   * default, which is the console's own shape (the target is built from the
   * name it was given). */
  readonly lookUp?: (project: string) => TargetLookup | Promise<TargetLookup>
}): ReturnType<typeof createLaunchRunner> {
  const held = options.target ?? target()
  const lookUp = options.lookUp
  if (lookUp !== undefined) {
    return createLaunchRunner({
      lookUp: async (project) => lookUp(project),
      confirmer: options.confirmer,
      stillRecorded: options.stillRecorded ?? (() => true),
      appEnv: { HOME: '/home/person-1' },
      ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
      ...(options.log === undefined ? {} : { log: options.log }),
      timers: options.timers ?? neverFires(),
      now: options.now ?? (() => NOW),
      random: countingRandom(),
    })
  }
  return createLaunchRunner({
    lookUp: (project: string) =>
      Promise.resolve(
        held.project === '' || project === ''
          ? { ok: false, reason: `no installation named ${JSON.stringify(project)} is one this console’s records carry` }
          : // The launcher refuses a target whose project is not the name it was
            // asked about, so a test that launches a *second* installation gets
            // the same records under that installation's own name.
            { ok: true, target: project === held.project ? held : { ...held, project } },
      ),
    confirmer: options.confirmer,
    stillRecorded: options.stillRecorded ?? (() => true),
    appEnv: { HOME: '/home/person-1' },
    ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
    ...(options.log === undefined ? {} : { log: options.log }),
    timers: options.timers ?? neverFires(),
    now: options.now ?? (() => NOW),
    random: countingRandom(),
  })
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

    const frozen = confirmer.opened[0] as FrozenLaunch
    // The vector starts at the program word, so the design's row string -- which
    // spells the CLI's own argv -- is everything after it.
    expect(frozen.argv.slice(1).join(' ')).toBe(rowArgv())
    expect(frozen.argv[0]).toBe(BINARY)
    expect(frozen.argv.slice(1)).toStrictEqual([
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

    const frozen = confirmer.opened[0] as FrozenLaunch
    expect(frozen.argv.slice(1).join(' ')).toBe(rowArgv({ title: 'the backlog', planTask: 'backlog/triage' }))
    expect(frozen.argv.filter((word) => word.startsWith('--title'))).toStrictEqual(['--title=the backlog'])
    expect(frozen.argv.filter((word) => word.startsWith('--plan-task'))).toStrictEqual(['--plan-task=backlog/triage'])

    confirmer.decide(false)
    await launched
  })

  it('keeps a title that begins with `-` inside its own word, where it cannot be read as a flag', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })
    const launched = held.launch(request({ title: '-draft' }))
    await untilSettled()

    const frozen = confirmer.opened[0] as FrozenLaunch
    expect(frozen.argv).toContain('--title=-draft')
    expect(frozen.argv).not.toContain('-draft')

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
  it('spawns the frozen object’s own words and nothing else, and freezes it', async () => {
    const confirmer = heldConfirmer()
    const spawner = completingSpawner()
    const held = runner({ confirmer, spawner })
    const launched = held.launch(request())
    await untilSettled()

    const frozen = confirmer.opened[0] as FrozenLaunch
    expect(Object.isFrozen(frozen)).toBe(true)
    expect(Object.isFrozen(frozen.argv)).toBe(true)

    confirmer.decide(true)
    const outcome = await launched
    expect(outcome.ok).toBe(true)

    const call = spawner.calls[0] as SpawnCall
    expect(call.args).toStrictEqual(frozen.argv.slice(1))
    expect(call.command).toBe(BINARY)
    expect(call.options.cwd).toBe(frozen.cwd)
    expect(spawnedArgv(frozen.plan)).toStrictEqual([call.command, ...call.args])
  })

  it('carries the run id in `task.NewRunID`’s shape and the task’s own bytes', async () => {
    const confirmer = heldConfirmer()
    const held = runner({ confirmer })
    const launched = held.launch(request({ task: 'a task with an em dash — in it' }))
    await untilSettled()

    const frozen = confirmer.opened[0] as FrozenLaunch
    expect(frozen.runId).toBe(RUN_ID)
    expect(frozen.taskText).toBe('a task with an em dash — in it')
    expect(new TextDecoder().decode(frozen.taskBytes)).toBe(frozen.taskText)

    confirmer.decide(false)
    await launched
  })
})

describe('the confirmation seam', () => {
  it('declines every launch in the shipped app, by name, and spawns nothing', async () => {
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

    const runId = (confirmer.opened[0] as FrozenLaunch).runId
    expect(await held.answer(runId, true)).toBeUndefined()
    const first = await launched
    expect(first.ok).toBe(true)

    const replayed = await held.answer(runId, true)
    expect(replayed?.ok).toBe(true)
    expect(replayed?.replayed).toBe(true)
    expect(spawner.calls).toHaveLength(1)
  })

  it('answers a run id this process never opened with nothing at all', async () => {
    const held = runner({ confirmer: DECLINING_CONFIRMER })
    expect(await held.answer('20261028-152618-0000', true)).toBeUndefined()
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

  it('refuses a reply carrying a key this console does not know', async () => {
    const outcome = await runner({
      confirmer: answeringConfirmer(true),
      spawner: completingSpawner({ overrides: { surprise: 'yes' } }),
    }).launch(request())
    expect(outcome.ok).toBe(false)
    expect(outcome.reason).toContain('surprise')
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
