// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The spawn contract, held to the design's section 3.4 rules and section 5.1's
// allow-list (desktop-console plan Task 1.3, SECURITY-SENSITIVE).
//
// Every property here is one a mutation can break, and each names the test that
// catches it:
//
//   - an argv built as a shell string, or a spawn with `shell: true`, fails
//     "builds argv as an array and hands the spawner `shell: false`";
//   - `--project` dropped from `watch` fails the table test named for `watch`;
//     `--project` added to the `version` handshake fails the test named for
//     `version`, which asserts the handshake's argv is exactly `["version"]`
//     (both directions of the plan's red step, because one fails on launch and
//     the other shows another installation's numbers);
//   - a value from a document interpolated into argv fails the hostile-value
//     table: `; rm -rf /`, `$(id)`, a backtick, a quote, a space and a leading
//     dash are refused, for every declared argument of every row;
//   - a child environment that inherits `process.env` wholesale, or that adds
//     `GOOGLE_APPLICATION_CREDENTIALS`, fails "gives the child exactly
//     `CHILD_ENV_KEYS`" and "does not pass the app's own credentials";
//   - the child's `PATH` reduced to one directory fails "is the declared list
//     and not one directory";
//   - a spawn that leaves `cwd` unset, or that uses `/`, fails "spawns from the
//     installation's checkout" and "spawns from the private directory";
//   - `doctor --strict` fails "passes `--dir` and never `--strict`";
//   - a candidate whose version cannot be read being skipped rather than
//     refused fails "refuses a binary whose version cannot be read";
//   - a non-JSON stdout line parsed rather than skipped fails the reader tests;
//   - a dead child not restarted, or restarted after a refusal, fails the
//     supervisor tests;
//   - a second file in `src/` naming `node:child_process` fails "is the only
//     file in src/ that spawns";
//   - a `subscribeLive` that builds a stream per call fails "answers a second
//     live.watch from the feed it already has, and spawns no second child";
//     one that keeps a feed which has ended fails "drops a feed that ended" and
//     "drops a feed the console stopped" (the two halves of the idempotency:
//     same arguments, same live feed -- and no feed at all once it is over).
//
// The fake CLI of `stub.ts` is the only thing spawned: never the real `fugaro`,
// never the network. The report file it appends to is the child's *own* account
// of its argv, cwd and environment, which is what makes these tests about what a
// real machine would see rather than about the console's self-consistency.

import { EventEmitter } from 'node:events'
import { chmodSync, existsSync, lstatSync, mkdirSync, mkdtempSync, realpathSync, rmSync, statSync, symlinkSync, writeFileSync } from 'node:fs'
import { readFileSync, readdirSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { basename, dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  CHILD_ENV_KEYS,
  COMMANDS_WITHOUT_PROJECT,
  DARWIN_LOCATIONS,
  FUGARO_BINARY_NAME,
  INSTALLATION_LIST_COMMAND,
  LINUX_LOCATIONS,
  PLATFORM_LOCATIONS,
  SPAWN_COMMANDS,
  SPAWN_FLAGS,
  WIN32_LOCATIONS,
  WSLENV_KEY,
  WSL_PREFIX_FLAGS,
  argvFor,
  assertArgvFlags,
  assertSpawnSurface,
  assertWslPrefix,
  binDirectories,
  childEnvironment,
  childPath,
  isDistroName,
  isSafeDistroName,
  cliSentence,
  isExecutableFile,
  decodeFeedLine,
  isSafeArgumentValue,
  locationsFor,
  nodeSpawner,
  parseVersionLine,
  planFor,
  PRIVATE_DIR_LEAF,
  PRIVATE_DIR_NAME,
  privateWorkingDirectory,
  RUNS_LIST_EQUALS_FLAGS,
  shellQuoteWord,
  probeVersion,
  resolveBinary,
  resolveHandshakeBinary,
  resolveInstalledBinary,
  wslUncPath,
  runToCompletion,
  restartDecision,
  spawnCommandFor,
  spawnedArgv,
  startSidecar,
  DEFAULT_BACKOFF,
  WSL_SHELL_FLAG,
  WSL_SHELL_PROGRAM,
  WSL_WRAPPER_MARKER_PREFIX,
  WSL_WRAPPER_NONCE_BYTES,
  VERSION_TIMEOUT_MS,
  withoutWrapperMarker,
  wrapperPidIn,
  wslStopArgs,
  wslWrapperMarker,
  SpawnTimeout,
} from '../../src/main/spawn'
import { WSL_UTF8_VAR, WSL_UTF8_VALUE } from '../../src/main/wsl'
import type {
  ChildSpawner,
  SpawnCommand,
  SpawnPlan,
  SpawnedChildLike,
  SpawnOptionsLike,
  Timers,
  SidecarEnd,
  WslRoute,
} from '../../src/main/spawn'
import { createSpawnLog, disabledSpawnLog } from '../../src/main/spawnlog'
import type { SpawnLogWriter } from '../../src/main/spawnlog'
import { RUNS_LIST_MAX_LIMIT, validateArguments } from '../../src/main/ipc'
import type { OperationName, ValidatedArguments } from '../../src/main/ipc'
import { CliSource, LOG_WINDOW_MAX_ENTRIES, MAX_LIVE_FEEDS, createInstallationStore, parseLogEntries } from '../../src/source/cli'
import type { InstallationCandidate } from '../../src/source/cli'
import { RUN_LOG_CAP, buildRunModel } from '../../src/renderer/screens/run'
import { SourceRefusal, handlersFor } from '../../src/source/source'
import { installStub } from './stub'
import type { Stub } from './stub'
import { DISTRO_PATH, installWslStub } from './wslstub'
import type { WslFixture, WslReport, WslStub } from './wslstub'

const SOURCE_ROOT = fileURLToPath(new URL('../../src', import.meta.url))
const PROJECT = 'fugaro'

/** WINDOWS_PATH is a `PATH` spelled the way a Windows host spells one: two
 * entries separated by `;`, drive-rooted, with backslashes. It is the string the
 * child's `PATH` must never be, and the two separators of one WSL spawn are
 * visible side by side because of it (F5, T-A2). */
const WINDOWS_PATH = 'C:\\Windows\\System32;C:\\Windows'

/**
 * COMMANDS_TAKING_PROJECT is the plan's own list, hard-coded: the thirteen
 * operations `addCloudFlags` gives `--project` to (`internal/cli/cloud.go:51`,
 * called from each of those constructors), including the two log operations
 * that share the `logs` command -- console L5b's `runs.stats` is the eleventh,
 * plan GV2.1's `plan.status` the twelfth, Bitbucket credentials T1.2's
 * `coordinator.status` the thirteenth, and the console's one write, V2.5.1a's
 * `run.launch`, the fourteenth. The complement -- the commands
 * that take no
 * flag at all -- is the *module's* `COMMANDS_WITHOUT_PROJECT`, keyed by the
 * command words the CLI itself uses (`version` and the rail's `config list`),
 * which is the same list `assertSpawnSurface` holds the table to; this file
 * imports it rather than spelling it twice.
 */
const COMMANDS_TAKING_PROJECT: readonly OperationName[] = [
  'live.watch',
  'budget.show',
  'spend.report',
  'runs.list',
  'runs.stats',
  'plan.status',
  'run.diagnose',
  'run.logs',
  'run.logLink',
  'secrets.inventory',
  'accounts.inventory',
  'environment.report',
  'coordinator.status',
  'run.launch',
]
/** freshDirectory is a temporary directory this test owns, one per call. */
function freshDirectory(prefix = 'fugaro-console-test-'): string {
  // The canonical spelling: on macOS the temporary directory is reached through
  // the `/var -> /private/var` link, and a child reports its cwd as the
  // realpath, so a test comparing the two needs the realpath on both sides.
  return realpathSync(mkdtempSync(join(tmpdir(), prefix)))
}

/** only is the one entry a list is expected to hold, so "nothing was recorded"
 * fails here rather than making every later assertion vacuous. */
function only<T>(entries: readonly T[]): T {
  expect(entries).toHaveLength(1)
  const [entry] = entries
  if (entry === undefined) throw new Error('nothing was recorded')
  return entry
}

const ownedTemporaryDirectories: string[] = []

/** ownTemporaryDirectory points TMPDIR at a fresh directory this file owns, so a
 * test that reaches the private working directory's default base (a child of the
 * system temporary directory) neither needs nor creates anything on the machine
 * it runs on; the afterEach below removes it and restores TMPDIR. */
function ownTemporaryDirectory(): string {
  const owned = freshDirectory('fugaro-console-tmp-')
  ownedTemporaryDirectories.push(owned)
  vi.stubEnv('TMPDIR', owned)
  return owned
}

afterEach(() => {
  vi.unstubAllEnvs()
  for (const directory of ownedTemporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

/** wrapperMarkerOf is the marker **this plan's** script printed: the readers
 * match that one and no other (a plan carries it because the nonce is drawn per
 * spawn -- `WslWrapper.marker`), so a case that puts a wrapper's line on a
 * scripted child's stderr has to ask the plan for it. */
function wrapperMarkerOf(plan: SpawnPlan): string {
  const wrapper = plan.wrapper
  if (wrapper === null) throw new Error('this plan carries no wrapper: it is the host route')
  return wrapper.marker
}

/** safeContext is the main-process state a plan is built from in a test.
 * `wsl` is the WSL route (T-C5) and is absent for the host route, which is the
 * ordinary case. */
function safeContext(overrides: {
  project?: string
  configPath?: string
  binaryPath?: string
  directory?: string
  appEnv?: Readonly<Record<string, string | undefined>>
  wsl?: WslRoute
} = {}): Parameters<typeof planFor>[2] {
  return {
    target: { project: overrides.project ?? PROJECT, ...(overrides.configPath === undefined ? {} : { configPath: overrides.configPath }) },
    binaryPath: overrides.binaryPath ?? '/opt/homebrew/bin/fugaro',
    directory: overrides.directory ?? '/Users/owner/src/fugaro',
    appEnv: overrides.appEnv ?? {},
    ...(overrides.wsl === undefined ? {} : { wsl: overrides.wsl }),
  }
}

/** LAUNCH_RUN_ID is a run id in `task.NewRunID`'s shape, for the one row whose
 * argv carries one. It is the launch's own id -- the main process generates it
 * (`src/main/launch.ts`) -- so every other row's context leaves it out, and
 * `argvFor` refuses an id on a row that has no `runIdFlag`. */
const LAUNCH_RUN_ID = '20261028-152618-a2fc'

/** contextFor is the plan context one row is built with: `safeContext` for
 * every read, and the same plus the run id for the launch, which cannot be
 * built without one. A loop over the whole table goes through here rather than
 * asking each row what it needs, so a row that starts requiring a new piece of
 * main-process state fails in the loop that builds it. */
function contextFor(
  operation: OperationName,
  overrides: Parameters<typeof safeContext>[0] = {},
): Parameters<typeof planFor>[2] {
  const context = safeContext(overrides)
  return operation === 'run.launch' ? { ...context, runId: LAUNCH_RUN_ID } : context
}

/**
 * recordingSpawner is a `spawn` that never starts anything: it answers with a
 * child whose streams say nothing and whose exit is never emitted. It is for the
 * assertions about *what was handed to the spawner* -- the argv, `shell`, the
 * environment -- where a real process would only add noise.
 */
function recordingSpawner(): ChildSpawner & { readonly calls: readonly SpawnCall[] } {
  const calls: SpawnCall[] = []
  const spawner = ((command: string, args: readonly string[], options: SpawnOptionsLike) => {
    calls.push({ command, args, options })
    return fakeChild()
  }) as ChildSpawner & { calls: SpawnCall[] }
  Object.defineProperty(spawner, 'calls', { get: () => calls })
  return spawner
}

interface SpawnCall {
  readonly command: string
  readonly args: readonly string[]
  readonly options: SpawnOptionsLike
}

function fakeChild(): SpawnedChildLike {
  return {
    stdout: { on: () => undefined },
    stderr: { on: () => undefined },
    // Node emits `spawn` on the tick after the process exists, before any I/O.
    // This fake fires it at registration instead: the ordering the assertions
    // here depend on is the same (a spawn-log line exists before the child's
    // first byte), without a tick for every test to wait out. `scriptedChild`
    // below does the same, and `childThatNeverSpawns` is the other half.
    on: ((
      event: 'spawn' | 'error' | 'exit' | 'close',
      listener: (...args: unknown[]) => void,
    ) => {
      if (event === 'spawn') listener()
      return undefined
    }) as SpawnedChildLike['on'],
    kill: () => true,
  }
}

/** manualTimers is the supervisor's clock, as a test drives it: nothing fires
 * until the test says so, so a backoff is asserted rather than waited out. */
function manualTimers(): Timers & { fired(): readonly number[]; fire(): void; pending(): number } {
  const handles = new Map<number, { fn: () => void; ms: number }>()
  const firedDelays: number[] = []
  let next = 0
  return {
    setTimeout(fn: () => void, ms: number) {
      next += 1
      handles.set(next, { fn, ms })
      return next as unknown as ReturnType<typeof setTimeout>
    },
    clearTimeout(handle: ReturnType<typeof setTimeout>) {
      handles.delete(handle as unknown as number)
    },
    fired: () => firedDelays,
    pending: () => handles.size,
    fire() {
      const entries = [...handles.entries()]
      for (const [key, entry] of entries) {
        handles.delete(key)
        firedDelays.push(entry.ms)
        entry.fn()
      }
    },
  }
}

/**
 * scriptedChild is a child process whose streams, exit and close the test
 * drives, for the supervisor tests that must not depend on a real signal's
 * timing.
 *
 * The three events are separate on purpose, because Node separates them and the
 * order is the whole point of settling on `close`:
 *
 *   - `exit(code, signal)` is what Node does for an ordinary child -- `exit`
 *     and then `close`, with nothing in between -- and it is what most tests
 *     want;
 *   - `exitWithoutClose` fires `exit` alone, which is the real hazard: it is the
 *     window in which a last chunk can still arrive, and a reader that settled
 *     on `exit` has already handed back a torn value;
 *   - `close` fires `close` alone, to end that window.
 */
function scriptedChild(): SpawnedChildLike & {
  emitStdout(text: string | Buffer): void
  emitStderr(text: string | Buffer): void
  exit(code: number | null, signal: string | null): void
  exitWithoutClose(code: number | null, signal: string | null): void
  close(code: number | null, signal: string | null): void
  readonly killed: readonly (NodeJS.Signals | undefined)[]
  readonly destroyed: readonly string[]
} {
  const stdoutListeners: ((chunk: Buffer | string) => void)[] = []
  const stderrListeners: ((chunk: Buffer | string) => void)[] = []
  const exitListeners: ((code: number | null, signal: string | null) => void)[] = []
  const closeListeners: ((code: number | null, signal: string | null) => void)[] = []
  const errorListeners: ((error: Error) => void)[] = []
  const killed: (NodeJS.Signals | undefined)[] = []
  const destroyed: string[] = []
  const child = {
    stdout: {
      on: (_event: string, listener: (chunk: Buffer | string) => void) => stdoutListeners.push(listener),
      destroy: () => destroyed.push('stdout'),
    },
    stderr: {
      on: (_event: string, listener: (chunk: Buffer | string) => void) => stderrListeners.push(listener),
      destroy: () => destroyed.push('stderr'),
    },
    on(
      event: 'spawn' | 'error' | 'exit' | 'close',
      listener:
        | (() => void)
        | ((error: Error) => void)
        | ((code: number | null, signal: string | null) => void),
    ) {
      if (event === 'spawn') (listener as () => void)()
      else if (event === 'error') errorListeners.push(listener as (error: Error) => void)
      else if (event === 'exit') exitListeners.push(listener as (code: number | null, signal: string | null) => void)
      else closeListeners.push(listener as (code: number | null, signal: string | null) => void)
      return undefined
    },
    kill(signal?: NodeJS.Signals) {
      killed.push(signal)
      return true
    },
    emitStdout: (text: string | Buffer) => {
      for (const listener of stdoutListeners) listener(text)
    },
    emitStderr: (text: string | Buffer) => {
      for (const listener of stderrListeners) listener(text)
    },
    exitWithoutClose: (code: number | null, signal: string | null) => {
      for (const listener of exitListeners) listener(code, signal)
    },
    close: (code: number | null, signal: string | null) => {
      for (const listener of closeListeners) listener(code, signal)
    },
    exit: (code: number | null, signal: string | null) => {
      for (const listener of exitListeners) listener(code, signal)
      for (const listener of closeListeners) listener(code, signal)
    },
    killed,
    destroyed,
  }
  return child
}

// 15s rather than a few: the WSL cases wait on a second real Node process (the
// fake `wsl.exe` the stop starts), and a shared CI or developer machine under
// load can take seconds to start one. The bound only lengthens a failure.
async function waitFor(predicate: () => boolean, ms = 15_000): Promise<void> {
  const deadline = Date.now() + ms
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error('waitFor timed out')
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
}

describe('the spawn table and the argv it builds', () => {
  it('has one row per command-bearing operation, and none for the link', () => {
    expect(() => {
      assertSpawnSurface(SPAWN_COMMANDS)
    }).not.toThrow()
    const names = SPAWN_COMMANDS.map((row) => row.operation)
    expect(new Set(names).size).toBe(names.length)
    expect(names).not.toContain('link.openExternal')
  })

  it('carries the installation rail’s read as an ordinary row of the one table', () => {
    // The rail's read is the local `fugaro config list --json`
    // (`console/docs/json-contract.md` §6.8), which is how the console
    // discovers the installations on this machine without asking the operator
    // anything. Until the owner's design ruling of 2026-10-25 it was the picker
    // window's own row in a second table; the rail made it the dashboard's
    // operation, so it is a row of `SPAWN_COMMANDS` like any other and
    // `assertSpawnSurface` holds it to the same one-for-one rule.
    const row = SPAWN_COMMANDS.find((candidate) => candidate.operation === 'installation.list')
    expect(row?.words.join(' ')).toBe('config list --json')
    expect(row?.words.slice(0, 2).join(' ')).toBe(INSTALLATION_LIST_COMMAND)
    // No `--project`, no `--dir`, no arguments: it is a local read that needs
    // no project selected and opens no bucket (`internal/cli/config_list.go`).
    expect(COMMANDS_WITHOUT_PROJECT).toContain(INSTALLATION_LIST_COMMAND)
    expect(row?.takesProject).toBe(false)
    expect(row?.dirFlag).toBe(false)
    expect(row?.args).toEqual([])
    expect([...argvFor('installation.list', {}, { target: { project: 'acme' }, directory: '/tmp' })]).toEqual([
      'config',
      'list',
      '--json',
    ])
    // ...and the read that names another command is refused, so the table
    // cannot grow a second one without this failing.
    expect(() => {
      assertSpawnSurface([
        ...SPAWN_COMMANDS.filter((candidate) => candidate.operation !== 'installation.list'),
        { operation: 'installation.list', words: ['config', 'show', '--json'], takesProject: false, args: [], dirFlag: false },
      ])
    }).toThrow(/does not start with config list/)
  })

  it('fixes the design allow-list exactly: sixteen argvs, fifteen commands, fourteen of them with --project', () => {
    // The design's §5.1 rule 6 table is the whole allow-list, argv for argv:
    // the rail's read (`config list --json`, the case above) used to be the
    // picker's own row in a second table until the owner's ruling of 2026-10-25,
    // plan GV2.1's `plan status --json` is the fourteenth, and Bitbucket
    // credentials T1.2's `coordinator status --from-store --all --json` the
    // fifteenth, and V2.5.1a's launch the sixteenth -- the one row here that is
    // a write, and the one whose words carry `--task-file -` (the literal word
    // `-` puts the task on the child's stdin). The Go side pins
    // the document's list (`internal/cli/docs_desktop_console_test.go`,
    // TestDocsDesktopConsoleDesignAllowListIsExactArgv) and this case pins the
    // console's; the two are held in step by hand on purpose.
    expect([...SPAWN_COMMANDS].map((row) => row.words.join(' '))).toEqual([
      'watch --json',
      'budget show --json',
      'report --json',
      'runs ls --json',
      'runs stats --json',
      'plan status --json',
      'diagnose --json',
      'logs --json',
      'logs --url',
      'secrets ls --json',
      'accounts ls --json',
      'version',
      'doctor --json',
      'config list --json',
      'coordinator status --from-store --all --json',
      'run --task-file - --json',
    ])
    // The twelve *commands* that take the flag (addCloudFlags, internal/cli/
    // cloud.go:51) are thirteen rows plus one: `logs` has two rows, `--json`
    // and `--url`, and neither of them may be the one that forgets --project.
    // A word is a flag or a separator if it begins with `-`, which is what
    // keeps the launch's literal `-` -- the argv word that means "read the task
    // from stdin" -- out of the command path it is compared as.
    const commandsWithProject = SPAWN_COMMANDS.filter((row) => row.takesProject).map((row) =>
      row.words.filter((word) => !word.startsWith('-')).join(' '),
    )
    expect(new Set(commandsWithProject)).toEqual(
      new Set([
        'watch',
        'budget show',
        'report',
        'runs ls',
        'runs stats',
        'plan status',
        'diagnose',
        'logs',
        'secrets ls',
        'accounts ls',
        'doctor',
        'coordinator status',
        'run',
      ]),
    )
    expect(SPAWN_COMMANDS.filter((row) => row.takesProject)).toHaveLength(14)
    // ...and the table's own flags agree with the list the tests above use.
    for (const row of SPAWN_COMMANDS) {
      expect(row.takesProject, row.operation).toBe(COMMANDS_TAKING_PROJECT.includes(row.operation))
    }
  })

  it.each(SPAWN_COMMANDS.map((row) => [row.operation, row] as const))(
    '%s puts --project on its argv, from the chosen installation',
    (_name, row: SpawnCommand) => {
      const argv = argvFor(row.operation, {}, contextFor(row.operation, { project: 'chosen' }))
      // The expectation is written out here rather than read from the row: a
      // test that asks the table what it contains cannot catch the table
      // changing, and "does `watch` carry --project" is the whole question.
      if (COMMANDS_TAKING_PROJECT.includes(row.operation)) {
        expect(argv).toContain('--project')
        expect(argv[argv.indexOf('--project') + 1]).toBe('chosen')
      } else {
        // `COMMANDS_WITHOUT_PROJECT` is keyed by the *command*, not by the
        // operation (`assertSpawnSurface` reads it that way), so the row's own
        // words are what it is asked about.
        expect(COMMANDS_WITHOUT_PROJECT).toContain(row.words.filter((word) => !word.startsWith('-')).join(' '))
        expect(argv).not.toContain('--project')
      }
    },
  )

  it('runs the version handshake bare: `fugaro version` and nothing else', () => {
    // Both halves of the plan's red step are this test and the one above: a
    // console that appended --project here fails on launch with
    // `unknown flag: --project` (newVersionCmd is cobra.NoArgs with no Flags),
    // and a console that left it off watch shows another installation's numbers.
    expect([...argvFor('cli.version', {}, safeContext())]).toEqual(['version'])
    expect(spawnCommandFor('cli.version').takesProject).toBe(false)
  })

  it('carries --config when the installation is not the ambient one', () => {
    const argv = argvFor('budget.show', {}, safeContext({ configPath: '/Users/owner/.config/fugaro/config.toml' }))
    expect(argv).toContain('--config')
    expect(argv[argv.indexOf('--config') + 1]).toBe('/Users/owner/.config/fugaro/config.toml')
    // ...and the ambient one, when the installation does not declare a path.
    expect(argvFor('budget.show', {}, safeContext())).not.toContain('--config')
  })

  it('adds --compute to `budget show` alone, and only when the caller asks', () => {
    // `budget show --compute` is the one flag-conditioned read on this surface
    // (`console/docs/json-contract.md` §3): it costs a listing of the runs
    // bucket, so it is an argument a caller opts into rather than a word of the
    // row, and the argv without it is byte for byte the one this console has
    // always spawned. The row's arguments are emitted in the order the table
    // declares them -- `--all`'s own place -- and the installation's `--project`
    // and `--config` follow every row's flags, this one included.
    expect([...argvFor('budget.show', { compute: true }, safeContext())]).toEqual([
      'budget',
      'show',
      '--json',
      '--compute',
      '--project',
      PROJECT,
    ])
    expect([...argvFor('budget.show', {}, safeContext())]).toEqual([
      'budget',
      'show',
      '--json',
      '--project',
      PROJECT,
    ])
    // `false` is a flag argv does not carry, exactly as `--all` reads.
    expect([...argvFor('budget.show', { compute: false }, safeContext())]).not.toContain('--compute')
    // The flag combines with the two words the CLI admits beside it
    // (`internal/cli/budget.go`), so the surface admits both pairings too.
    expect([...argvFor('budget.show', { repo: 'acme/widgets', compute: true }, safeContext())]).toEqual([
      'budget',
      'show',
      '--json',
      '--repo',
      'acme/widgets',
      '--compute',
      '--project',
      PROJECT,
    ])
    expect([...argvFor('budget.show', { all: true, compute: true }, safeContext())]).toContain('--compute')
    // And no other row declares the key: a caller that reaches for it on a read
    // whose figures nobody draws is refused by the row table, before an argv
    // exists.
    for (const operation of ['spend.report', 'runs.list', 'runs.stats', 'live.watch'] as const) {
      expect(() => argvFor(operation, { compute: true }, contextFor(operation, {})), operation).toThrow(
        `${operation} received compute, which it does not declare`,
      )
    }
    // `--compute` is on `SPAWN_FLAGS` for the row that declares it, and it is
    // not on the never-reachable table: the CLI genuinely admits the flag on
    // this command, so what keeps it off every other row is the row table above
    // -- which is where `--all` and `--mine` are kept too, not here.
    expect(() =>
      assertArgvFlags('budget.show', [FUGARO_BINARY_NAME, 'budget', 'show', '--json', '--compute']),
    ).not.toThrow()
    expect(SPAWN_FLAGS).toContain('--compute')
  })

  it('spawns `plan status` with exactly these words, and no directory anywhere in them', () => {
    // The plan read's argv, word for word (plan GV2.1; the design allow-list's
    // `plan status --json` and its `--all`). It is written out rather than read
    // from the row because the claim is *this* argv: `--json` first, then the
    // cloud flags in the order `runs ls` carries them, then the one argument the
    // screen may add.
    expect(argvFor('plan.status', {}, safeContext({ project: 'chosen' }))).toEqual([
      'plan',
      'status',
      '--json',
      '--project',
      'chosen',
    ])
    expect(argvFor('plan.status', { all: true }, safeContext({ project: 'chosen' }))).toEqual([
      'plan',
      'status',
      '--json',
      '--all',
      '--project',
      'chosen',
    ])
    // `all: false` is the screen's toggle off, and it must be the absence of the
    // word rather than the word with a value beside it: `--all false` is an argv
    // cobra reads as the flag being on.
    expect(argvFor('plan.status', { all: false }, safeContext({ project: 'chosen' }))).not.toContain('--all')
    // A flag payload cannot spell a word of its own, and it cannot get as far as
    // the argv at all: the row declares a boolean and the builder refuses
    // anything else rather than stringifying it into a word.
    expect(() =>
      argvFor(
        'plan.status',
        { all: '--dir' } as unknown as Parameters<typeof argvFor>[1],
        safeContext({ project: 'chosen' }),
      ),
    ).toThrow(/all is not a boolean/)
    // `--config` is the installation's, as on every cloud row: the read runs in
    // the checkout's directory, and the config that names the project is a
    // separate fact about the installation.
    expect(
      argvFor(
        'plan.status',
        {},
        safeContext({ project: 'chosen', configPath: '/Users/owner/.config/fugaro/config.toml' }),
      ),
    ).toEqual([
      'plan',
      'status',
      '--json',
      '--project',
      'chosen',
      '--config',
      '/Users/owner/.config/fugaro/config.toml',
    ])
    // **And no path, in any position.** The directory is the spawn's own: it is
    // `safeContext.directory`, which reaches the child as its cwd and never as a
    // word of the argv. This is the assertion a mutated row (`--dir`, a
    // positional word) fails on, and it is checked against the directory the
    // context actually carries rather than against a path the row would have to
    // spell.
    const directory = '/Users/owner/src/fugaro'
    const argumentCases: ValidatedArguments[] = [{}, { all: true }]
    for (const args of argumentCases) {
      const argv = argvFor('plan.status', args, safeContext({ project: 'chosen', directory }))
      expect(argv, JSON.stringify(args)).not.toContain(directory)
      expect(argv, JSON.stringify(args)).not.toContain('--dir')
      expect(argv).not.toContain('--gcp-project')
      expect(argv).not.toContain('--region')
      // Every word is either one of the command's own, the project's name, or
      // the flag the schema declares -- there is nothing else it could be.
      expect(argv.filter((word) => word.startsWith('--'))).toEqual(
        args.all === true ? ['--json', '--all', '--project'] : ['--json', '--project'],
      )
    }
  })

  it('emits one array element per value, in the operation’s own order', () => {
    const argv = argvFor(
      'runs.list',
      { repo: 'acme/widgets', since: '3d', mine: true, pr: 412 },
      safeContext({ project: 'chosen' }),
    )
    expect(argv).toEqual([
      'runs',
      'ls',
      '--json',
      '--repo',
      'acme/widgets',
      '--since',
      '3d',
      '--mine',
      '--pr',
      '412',
      '--project',
      'chosen',
    ])
  })

  it('emits the secret scope as --project-wide and the repository as a value', () => {
    expect(argvFor('secrets.inventory', { scope: 'project-wide' }, safeContext())).toContain('--project-wide')
    const scoped = argvFor('secrets.inventory', { scope: 'repo', repo: 'acme/widgets' }, safeContext())
    expect(scoped).toContain('--repo')
    expect(scoped).not.toContain('--project-wide')
  })

  it('refuses an argument the operation does not declare, by name', () => {
    expect(() => argvFor('live.watch', { nope: 'x' }, safeContext())).toThrow(/does not declare/)
    expect(() => argvFor('runs.list', { until: '2026-10-16' }, safeContext())).toThrow(/does not declare/)
  })

  // `runs.list`'s `pr` is the one argument whose *type* changes on the way to
  // argv: the schema hands over a number and the CLI's `--pr` is an `IntVar`
  // that reads digits. This pair is that boundary from both sides, and the
  // first version of this test asserted `toHaveLength(0)` on an arrow function
  // -- `Function#length` is arity, so it passed whether `argvFor` threw or not,
  // and it fed `'412'` as a string, which the validator never emits.
  it('spells the validated pull request number as the digits --pr reads', () => {
    const argv = argvFor('runs.list', { repo: 'acme/widgets', pr: 412 }, safeContext())
    expect(argv).toContain('--pr')
    expect(argv).toContain('412')
    expect(argv[argv.indexOf('--pr') + 1]).toBe('412')
  })

  it('pages the runs list with two `=`-form words, in the row’s own order', () => {
    // The page and its cursor (D818, coordinator v2 C2). Both are **one word
    // each** -- `--limit=<n>` and `--after=<run id>` -- and the form is the
    // point: a value inside the flag's own word cannot be read as a flag, so
    // neither argument can spell a word of the command line of its own. They
    // come after the row's other arguments, which is where the table declares
    // them, and before the installation's own `--project`.
    const cursor = '20261030-184402-31de'
    expect([...argvFor('runs.list', { since: '24h', limit: 50 }, safeContext({ project: 'chosen' }))]).toEqual([
      'runs',
      'ls',
      '--json',
      '--since',
      '24h',
      '--limit=50',
      '--project',
      'chosen',
    ])
    const argv = argvFor('runs.list', { since: '24h', limit: 50, after: cursor }, safeContext({ project: 'chosen' }))
    expect([...argv]).toEqual([
      'runs',
      'ls',
      '--json',
      '--since',
      '24h',
      '--limit=50',
      `--after=${cursor}`,
      '--project',
      'chosen',
    ])
    // The value lives inside the flag's word and nowhere else: neither flag is
    // a word of its own, and neither value is either.
    for (const word of ['--limit', '--after', '50', cursor]) {
      expect(argv, word).not.toContain(word)
    }
  })

  it('refuses a page size the CLI’s IntVar would not read', () => {
    // The validator's cap is `ipc.ts`'s `RUNS_LIST_MAX_LIMIT`, and this is the
    // second of the two checks: `String` would happily spell `1e+21` or
    // `1.5`, which cobra's `IntVar` refuses a stage later -- after a child
    // exists, which is the one outcome the schema and this guard both exist to
    // prevent.
    for (const limit of [0, -1, 1.5, RUNS_LIST_MAX_LIMIT + 1, 1e21, Number.NaN, Number.POSITIVE_INFINITY, '50']) {
      expect(() => argvFor('runs.list', { limit }, safeContext()), String(limit)).toThrow(/page size/)
    }
    expect(() => argvFor('runs.list', { limit: RUNS_LIST_MAX_LIMIT }, safeContext())).not.toThrow()
  })

  it('refuses a cursor that is not a run id this console will pass', () => {
    for (const after of ['--watch', '-x', '', 'a b', '$(id)', '20261030-184402-31de; rm -rf /', 'a\nb']) {
      expect(() => argvFor('runs.list', { after }, safeContext()), JSON.stringify(after)).toThrow()
    }
  })

  it('takes the `=` form on the runs list row, and never a bare or empty flag', () => {
    const cursor = '20261030-184402-31de'
    expect(() => assertArgvFlags('runs.list', ['runs', 'ls', '--json', '--limit=25'])).not.toThrow()
    expect(() => assertArgvFlags('runs.list', ['runs', 'ls', '--json', `--after=${cursor}`])).not.toThrow()
    // A bare word and an `=` with nothing after it are both refused: the
    // exception is a *prefix with a value*, not a flag name.
    expect(() => assertArgvFlags('runs.list', ['runs', 'ls', '--json', '--limit'])).toThrow()
    expect(() => assertArgvFlags('runs.list', ['runs', 'ls', '--json', '--limit='])).toThrow()
    expect(() => assertArgvFlags('runs.list', ['runs', 'ls', '--json', '--after'])).toThrow()
    expect(() => assertArgvFlags('runs.list', ['runs', 'ls', '--json', '--after='])).toThrow()
    // The two prefixes are this row's own: no other row may borrow them, and
    // the launch's two are still the launch's alone.
    for (const row of ['live.watch', 'run.diagnose', 'plan.status', 'spend.report']) {
      expect(() => assertArgvFlags(row, [row, '--limit=25']), row).toThrow()
      expect(() => assertArgvFlags(row, [row, `--after=${cursor}`]), row).toThrow()
    }
    expect(() => assertArgvFlags('runs.list', ['runs', 'ls', '--json', '--title=x'])).toThrow()
    expect(() => assertArgvFlags('run.launch', ['run', '--limit=25'])).toThrow()
    // ...and the row declares exactly these two `=`-form flags, so the table
    // and the argv check cannot drift apart.
    expect(RUNS_LIST_EQUALS_FLAGS).toStrictEqual(['--limit=', '--after='])
  })

  it('keeps `--watch` and every other runs ls flag off the runs list row', () => {
    // `--watch` is `watch` under another name and is on the never-reachable
    // table as a *flag*, so it stays refused however a payload spells it. The
    // page and the cursor are the only two words this task adds: every other
    // `runs ls` flag (`--state`, `--batch`, `--interval`, `--watch`) is not a
    // key of the operation's schema and has no row table entry either.
    for (const value of ['--watch', '--interval=10s', '--state=failed']) {
      expect(validateArguments('runs.list', { after: value }).ok, value).toBe(false)
      expect(validateArguments('runs.list', { limit: value }).ok, value).toBe(false)
    }
    for (const key of ['watch', 'state', 'batch', 'interval']) {
      expect(() => argvFor('runs.list', { [key]: true }, safeContext()), key).toThrow(/does not declare/)
    }
    expect(() => assertArgvFlags('runs.list', ['runs', 'ls', '--json', '--watch'])).toThrow(/--watch/)
  })

  it('refuses a pull request that is not a number the validator would hand over', () => {
    for (const value of ['412', ' 412', '-1', '412; rm -rf /'] as const) {
      expect(() => argvFor('runs.list', { pr: value }, safeContext()), value).toThrow(/not a pull request number/)
    }
    for (const value of [-1, 0, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() => argvFor('runs.list', { pr: value }, safeContext()), String(value)).toThrow(
        /not a pull request number/,
      )
    }
  })

  it('accepts the report window `report` reads, and only that one', () => {
    // `spend.report`'s `--since` is not `runs ls`'s: `reportRange` reads a date
    // or `Nd`/`Nw`, so `12w` and `2026-10-01` must survive argvFor while `3h`
    // is the runs grammar's shape and never reaches this row from the schema.
    for (const value of ['2026-10-01', '30d', '12w'] as const) {
      const argv = argvFor('spend.report', { since: value }, safeContext())
      expect(argv[argv.indexOf('--since') + 1], value).toBe(value)
    }
  })

  // The two guards are two alphabets -- the validator's grammar per kind and the
  // spawn side's ARGUMENT_RE -- and a value the first admits and the second
  // refuses is a refusal the operator sees only after the click. Task 1.2's
  // `runSince` admitted `1µs` (U+00B5) and `1μs` (U+03BC), which the spawn
  // alphabet refuses, so every `--since` and every other string a validator can
  // hand over is driven through both here.
  it('turns every value the validator accepts into argv, so the two guards agree', () => {
    const samples: readonly (readonly [OperationName, Record<string, unknown>])[] = [
      ['runs.list', { since: '0' }],
      ['runs.list', { since: '7d' }],
      ['runs.list', { since: '+7d' }],
      ['runs.list', { since: '36h' }],
      ['runs.list', { since: '1h30m' }],
      ['runs.list', { since: '1.5h' }],
      ['runs.list', { since: '+5h' }],
      ['runs.list', { since: '1us' }],
      ['runs.list', { since: '1µs' }],
      ['runs.list', { since: '1μs' }],
      ['runs.list', { since: '300ms' }],
      ['spend.report', { since: '12w', until: '2026-10-16', by: 'person' }],
      ['run.diagnose', { run: '20260101-120000-abcd' }],
    ]
    for (const [operation, payload] of samples) {
      const decision = validateArguments(operation, payload)
      if (!decision.ok) continue
      expect(() => argvFor(operation, decision.args, safeContext()), `${operation} ${JSON.stringify(payload)}`).not.toThrow()
    }
    // ...and the agreement is not vacuous: the ASCII forms do get through.
    expect(validateArguments('runs.list', { since: '1us' }).ok).toBe(true)
    expect(validateArguments('runs.list', { since: '1h30m' }).ok).toBe(true)
  })

  it('refuses a value that is not a boolean or a string where the schema says so', () => {
    expect(() => argvFor('runs.list', { mine: 'yes' }, safeContext())).toThrow(/not a boolean/)
    expect(() => argvFor('runs.list', { repo: 7 }, safeContext())).toThrow(/not a string/)
    expect(() => argvFor('secrets.inventory', { scope: 'everything' }, safeContext())).toThrow(/two scopes/)
  })

  it.each([
    'a; rm -rf /',
    '$(id)',
    '`id`',
    'a b',
    'a"b',
    "a'b",
    'a|b',
    'a&b',
    'a>b',
    'a$(b)',
    'a\ttab',
    'a\nnewline',
    '-e',
    '--json',
    '',
  ])('refuses %j as any declared value of any operation', (value) => {
    for (const row of SPAWN_COMMANDS) {
      for (const spec of row.args) {
        if (spec.kind !== 'value') continue
        expect(
          () => argvFor(row.operation, { [spec.key]: value }, safeContext()),
          `${row.operation} ${spec.key}`,
        ).toThrow()
      }
    }
    expect(isSafeArgumentValue(value)).toBe(false)
  })

  it('accepts the values the surface admits, so the refusal above is not vacuous', () => {
    for (const value of ['acme/widgets', '20260101-120000-abcd', '2026-01-01', '3d', 'day', '412', 'project-wide']) {
      expect(isSafeArgumentValue(value), value).toBe(true)
    }
  })

  it('leaves no shell metacharacter in any argv it builds', () => {
    // The mutation this catches is the plan's own: a command built as a template
    // string (`fugaro watch --json --project ${project}` handed to a shell)
    // would carry a space and a whole command in one element.
    for (const row of SPAWN_COMMANDS) {
      const argv = argvFor(row.operation, {}, contextFor(row.operation))
      for (const element of argv) {
        expect(element, `${row.operation}: ${element}`).not.toMatch(/[\s;&|`$(){}<>"'\\]/)
      }
      expect(['sh', '/bin/sh', 'bash', 'zsh']).not.toContain(argv[0])
    }
  })

  it('refuses a flag the console must never emit, through the never table', () => {
    expect(() => argvFor('budget.show', {}, safeContext())).not.toThrow()
    // `--strict` and `--follow` are rows of the never table, and the guard is
    // inside argvFor so no row, argument or later edit can emit one.
    const strict = planFor('environment.report', {}, safeContext())
    expect(strict.argv).not.toContain('--strict')
    expect(strict.argv).not.toContain('--follow')
  })

  it('refuses a subcommand outside the table before any process exists', () => {
    const spawner = recordingSpawner()
    expect(() => planFor('budget kill' as OperationName, {}, safeContext())).toThrow(/not a console operation/)
    expect(() => spawnCommandFor('exec')).toThrow(/not a console operation/)
    expect(spawner.calls).toHaveLength(0)
  })

  it('fails a table whose argv does not start with the operation’s command', () => {
    const mutated = SPAWN_COMMANDS.map((row) =>
      row.operation === 'run.diagnose' ? { ...row, words: ['budget', 'kill', '--json'] } : row,
    )
    expect(() => assertSpawnSurface(mutated)).toThrow(/does not start with diagnose/)
  })

  it('fails a table that drops a flag the surface fixes, or adds an argument it does not accept', () => {
    expect(() =>
      assertSpawnSurface(
        SPAWN_COMMANDS.map((row) => (row.operation === 'budget.show' ? { ...row, words: ['budget', 'show'] } : row)),
      ),
    ).toThrow(/fixes --json/)
    expect(() =>
      assertSpawnSurface(
        SPAWN_COMMANDS.map((row) =>
          row.operation === 'live.watch'
            ? { ...row, args: [...row.args, { key: 'json', flag: '--json', kind: 'value' as const }] }
            : row,
        ),
      ),
    ).toThrow(/the operation does not accept/)
  })

  it('fails a table that drops or adds --project, or moves --dir (rules 3 and 5)', () => {
    // Review round 2 of PR 478: the doc-comment named "a row that drops
    // `takesProject` from `watch`" as a mutation this check fails, and the body
    // never read the column -- a table with both columns false on every row
    // was accepted, and `argvFor` would then spawn watch and doctor with no
    // --project and no --dir.
    const flip = (operation: string, change: Partial<SpawnCommand>): SpawnCommand[] =>
      SPAWN_COMMANDS.map((row) => (row.operation === operation ? { ...row, ...change } : row))
    expect(() => assertSpawnSurface(flip('live.watch', { takesProject: false }))).toThrow(
      /live\.watch .*--project/,
    )
    expect(() => assertSpawnSurface(flip('cli.version', { takesProject: true }))).toThrow(/cli\.version .*--project/)
    expect(() => assertSpawnSurface(flip('environment.report', { dirFlag: false }))).toThrow(
      /environment\.report .*--dir/,
    )
    expect(() => assertSpawnSurface(flip('runs.list', { dirFlag: true }))).toThrow(/runs\.list .*--dir/)
    expect(() =>
      assertSpawnSurface(SPAWN_COMMANDS.map((row) => ({ ...row, takesProject: false, dirFlag: false }))),
    ).toThrow(/--project/)
  })

  it('fails a table with a command-bearing operation missing from it', () => {
    expect(() => assertSpawnSurface(SPAWN_COMMANDS.filter((row) => row.operation !== 'runs.list'))).toThrow(
      /runs.list has no row/,
    )
  })

  it('builds argv as an array and hands the spawner `shell: false`', async () => {
    const spawner = recordingSpawner()
    const plan = planFor('budget.show', { repo: 'acme/widgets' }, safeContext({ binaryPath: '/opt/homebrew/bin/fugaro' }))
    void runToCompletion(plan, { spawner }).catch(() => undefined) // a fake child never closes: no late rejection
    expect(spawner.calls).toHaveLength(1)
    const call = spawner.calls[0]
    expect(call?.command).toBe('/opt/homebrew/bin/fugaro')
    expect(Array.isArray(call?.args)).toBe(true)
    expect(call?.args).toEqual(['budget', 'show', '--json', '--repo', 'acme/widgets', '--project', PROJECT])
    expect(call?.options.shell).toBe(false)
    expect(call?.options.cwd).toBe('/Users/owner/src/fugaro')
  })
})

describe('the wrapper on the spawn plan', () => {
  // T-C1 of docs/console-windows/implementation-plan.md, decision D1(a): the
  // plan names the program it starts (`command`) and the wrapper's own words
  // (`prefix`) apart from the CLI's argv, because on Windows the console starts
  // `wsl.exe` and `wsl.exe` starts the CLI inside a distribution.
  const WSL = 'C:\\Windows\\System32\\wsl.exe'
  // The two spellings of one directory, as the plan itself has them: `cwd` is
  // the host directory the console starts `wsl.exe` in, and `--cd` is the
  // directory *inside* the distribution the CLI starts in (T-D4's translation).
  const HOST_DIR = 'C:\\Users\\dimi\\src\\fugaro'
  const LINUX_DIR = '/home/dimi/src/fugaro'

  /** wslPlanFor is a plan `planFor` really builds for the WSL route -- the same
   * call `resolveBinary` and `startSidecar`'s caller make -- rather than a plan
   * assembled here. A hand-built one drifts: the wrapper's `sh -c` script, the
   * `--cd` that belongs to `wsl.exe` and the `fugaro` the distribution resolves
   * are all decided by `planFor`, so a fixture that spells them itself asserts
   * against a spawn line the console cannot produce. */
  const wslPlanFor = (operation: OperationName, args: ValidatedArguments = {}): SpawnPlan =>
    planFor(operation, args, safeContext({
      binaryPath: '',
      directory: HOST_DIR,
      wsl: { distro: 'Ubuntu', directory: LINUX_DIR, executable: WSL },
    }))

  /** The wrapper's words, which are `wsl.exe`'s own flags and then the `sh -c`
   * script: what follows the program on every spawned WSL line. */
  const WSL_WORDS = ['-d', 'Ubuntu', '--cd', LINUX_DIR, '-e', 'sh', '-c']

  it('keeps the program, the wrapper’s words and the CLI’s argv apart', () => {
    // The host route: the command *is* the CLI's program word, so `argv[0]` and
    // `command` are the same string and the spawned line is unchanged.
    const host = planFor('budget.show', {}, safeContext({ binaryPath: '/opt/homebrew/bin/fugaro' }))
    expect(host.command).toBe('/opt/homebrew/bin/fugaro')
    expect([...host.prefix]).toEqual([])
    expect(host.argv[0]).toBe('/opt/homebrew/bin/fugaro')
    expect([...spawnedArgv(host)]).toEqual(['/opt/homebrew/bin/fugaro', 'budget', 'show', '--json', '--project', PROJECT])
    expect([...spawnedArgv(host)]).toEqual([...host.argv].slice(0, 1).concat(host.argv.slice(1)))

    // The WSL route: the command is `wsl.exe`, the wrapper's words are its own
    // flags and the script, and `argv[0]` is `fugaro` -- the bare name the
    // *distribution* resolves, which is the whole point of running the CLI
    // inside it.
    const wsl = wslPlanFor('cli.version')
    expect(wsl.command).toBe(WSL)
    expect(wsl.argv[0]).toBe(FUGARO_BINARY_NAME)
    expect([...wsl.prefix]).toEqual(WSL_WORDS.slice(0, 5))
    const spawned = [...spawnedArgv(wsl)]
    expect(spawned.slice(0, 8)).toEqual([WSL, ...WSL_WORDS])
    // The CLI's argv is *inside* the script, quoted, and not a word of its own:
    // that is where it is when the CLI runs (T-F4).
    expect(spawned).toHaveLength(9)
    expect(spawned[8]).toContain(`exec 'fugaro' 'version'`)
    expect(spawned).not.toContain('version')
    // ...and the two directories are the two spellings, not one directory named
    // twice: the console starts `wsl.exe` in the host one and the CLI runs in
    // the distribution's.
    expect(wsl.cwd).toBe(HOST_DIR)
    expect(spawned[4]).toBe(LINUX_DIR)
  })

  it('holds the wrapper’s flags to `wsl.exe` and the CLI’s argv to `SPAWN_FLAGS`', () => {
    // The two halves are asserted against two different rules, and that is why
    // they are two fields. `-d` and `--cd` are `wsl.exe`'s; `--cd` is not a flag
    // this console may emit to the CLI, and neither is `--distribution`, so a
    // wrapper flag that leaked into the CLI's argv is refused by name.
    const wsl = wslPlanFor('cli.version')
    expect(() => assertArgvFlags('cli.version', wsl.argv)).not.toThrow()
    expect(() => assertArgvFlags('cli.version', [FUGARO_BINARY_NAME, 'version', '--cd', '/home/dimi'])).toThrow(
      /--cd, which is not a flag this console may spawn/,
    )
    expect(() =>
      assertArgvFlags('cli.version', [FUGARO_BINARY_NAME, 'version', '--distribution', 'Ubuntu']),
    ).toThrow(/--distribution/)
    // ...and the prefix is *not* an argv the CLI's assertion is ever asked
    // about, which is the whole reason it is a separate field: handed to this
    // same assertion it would be refused, because `--cd` is not a flag this
    // console may emit to the CLI. A design that put the wrapper in `argv` would
    // therefore have to widen `SPAWN_FLAGS` with `wsl.exe`'s own flags.
    expect(() => assertArgvFlags('cli.version', [...wsl.prefix])).toThrow(/--cd/)
  })

  it('starts `command` with the wrapper’s words, and reports `command` and not `argv[0]`', async () => {
    const spawner = recordingSpawner()
    const plan = wslPlanFor('cli.version')
    void runToCompletion(plan, { spawner }).catch(() => undefined) // a fake child never closes: no late rejection
    expect(spawner.calls).toHaveLength(1)
    const call = spawner.calls[0]
    // The reported program is the plan's `command`. Reading `argv[0]` again --
    // the mutation this case exists for -- would report `fugaro`, a file that
    // does not exist on the machine the console is running on.
    expect(call?.command).toBe(WSL)
    expect(call?.command).not.toBe(plan.argv[0])
    // The words handed over are the wrapper's: `wsl.exe`'s own flags, then the
    // script -- with the CLI's argv inside it, quoted, and not one word of it in
    // a slot of its own, which would start the CLI a second time as `sh`'s own
    // positional words.
    expect(call?.args.slice(0, 7)).toEqual([...WSL_WORDS])
    expect(call?.args).toHaveLength(8)
    expect(call?.args[7]).toContain(`exec 'fugaro' 'version'`)
    expect(call?.options.shell).toBe(false)
    expect(call?.options.cwd).toBe(HOST_DIR)
  })

  it('records the whole command line in the spawn log, wrapper and all', async () => {
    const spawner = recordingSpawner()
    const seen: { argv: readonly string[] }[] = []
    const log: SpawnLogWriter = {
      enabled: true,
      path: '/tmp/unused.jsonl',
      records: 0,
      error: null,
      record: (spawn) => {
        seen.push({ argv: spawn.argv })
      },
      close: () => undefined,
    }
    const plan = wslPlanFor('cli.version')
    void runToCompletion(plan, { spawner, log }).catch(() => undefined) // a fake child never closes: no late rejection
    expect(seen).toHaveLength(1)
    // The line the operator reads back is the whole command line: the program
    // that ran, the wrapper's words, and the script carrying the CLI's argv.
    // Recording `plan.argv` instead -- the mutation this case exists for --
    // would log `fugaro`, a name that resolves inside a distribution and
    // nowhere on the machine the log was written on.
    expect(seen[0]?.argv[0]).toBe(WSL)
    expect(seen[0]?.argv).not.toEqual([...plan.argv])
    expect(seen[0]?.argv.slice(0, 8)).toEqual([WSL, ...WSL_WORDS])
    expect(seen[0]?.argv[8]).toContain(`exec 'fugaro' 'version'`)
  })

  it('supervises a stream through the same wrapper', () => {
    const spawner = recordingSpawner()
    const sidecar = startSidecar(wslPlanFor('live.watch'), {
      onFrame: () => undefined,
      onNotice: () => undefined,
      onRestart: () => undefined,
      onEnded: () => undefined,
    }, { spawner })
    expect(spawner.calls).toHaveLength(1)
    expect(spawner.calls[0]?.command).toBe(WSL)
    expect(spawner.calls[0]?.args.slice(0, 7)).toEqual([...WSL_WORDS])
    expect(spawner.calls[0]?.args[7]).toContain(`exec 'fugaro' 'watch' '--json'`)
    void sidecar.stop()
  })
})

describe('the real spawner', () => {
  const spawned = (
    stdio: readonly ['ignore' | 'pipe', 'ignore' | 'pipe', 'ignore' | 'pipe'],
  ) =>
    nodeSpawner('/bin/sh', ['-c', 'exit 0'], {
      cwd: process.cwd(),
      env: {},
      shell: false,
      stdio,
    })

  const closed = (child: SpawnedChildLike): Promise<void> =>
    new Promise((resolve) => {
      child.on('close', () => resolve())
    })

  it('gives the child the descriptors its caller asked for', async () => {
    // T-F4: the one child whose output is nobody's is the stop sent **inside**
    // a distribution, and it asks for `'ignore'` on all three, because a pipe
    // nobody drains is a child that blocks on write. `nodeSpawner` used to
    // hardcode `['ignore', 'pipe', 'pipe']`, so that request was a comment: the
    // stop's own stdout and stderr were pipes the console never read.
    const quiet = spawned(['ignore', 'ignore', 'ignore'])
    expect(quiet.stdout).toBeNull()
    expect(quiet.stderr).toBeNull()
    await closed(quiet)

    // ...and the other direction, so that "honour `stdio`" is not satisfied by
    // ignoring it: every read in this module hands `'pipe'` and reads it.
    const read = spawned(['ignore', 'pipe', 'pipe'])
    expect(read.stdout).not.toBeNull()
    expect(read.stderr).not.toBeNull()
    await closed(read)

    // ...and the one request that asks for stdin, which is the launch's: V2.5.1a
    // widened that single slot so the task text can be written to the child
    // rather than spelled onto the command line (rule 11), and the spawner has
    // to hand back a writable stream when it is asked for one -- a `'pipe'`
    // that came back `null` would be a launch with nowhere to put its task.
    const fed = spawned(['pipe', 'pipe', 'pipe'])
    expect(fed.stdin).not.toBeNull()
    await closed(fed)
  })
})

describe('the wrapper’s own words', () => {
  // T-E3 of docs/console-windows/implementation-plan.md, finding F22: the guard
  // the CLI's argv is held to is extended to the prefix. Part C put words on a
  // command line that do not come from the CLI's own table -- a distribution's
  // name arrives from `wsl.exe --list --quiet` or from the store -- so the
  // prefix has a guard of its own rather than the CLI's, and the two flag sets
  // are disjoint by design (T-C1: that is why `prefix` and `argv` are two
  // fields).
  //
  // **What the two values are held to is the *same* rule** -- no control
  // character, not empty, not absurd, no leading `-` -- and not a narrower
  // alphabet *at this layer*: with an array argv and `shell: false` a space in
  // one element is a character and not a second word. This is the argv's last
  // line only. The rule a distribution must meet to be driven is Part D's
  // narrower `isDistroName` (`DISTRO_NAME_PATTERN`), which `planFor` and the
  // resolver apply first, because the WSL route runs the CLI through the
  // distribution's own shell: a distribution called `My Linux` is refused on
  // the route (fail closed), and the cases below and in the resolution block
  // hold both layers.
  const words = (list: readonly string[]): readonly string[] => Object.freeze([...list])
  const inside = (directory: string): readonly string[] => words(['-d', 'Ubuntu', '--cd', directory, '-e'])

  it('admits exactly the two shapes `wslWrapper` builds, and nothing else', () => {
    expect(() => assertWslPrefix(words(['-d', 'Ubuntu', '-e']))).not.toThrow()
    expect(() => assertWslPrefix(words(['-d', 'Ubuntu-22.04', '--cd', '/home/dimi/src', '-e']))).not.toThrow()
    // Whitespace in either value, admitted *by this guard*: neither becomes two
    // words with an array argv. (The route refuses such a distribution name
    // before the prefix is built -- Part D's rule, asserted below.)
    expect(() => assertWslPrefix(inside('/home/Ann Lee/src/fugaro'))).not.toThrow()
    expect(() => assertWslPrefix(words(['-d', 'My Linux', '--cd', '/home/Ann Lee/src', '-e']))).not.toThrow()
    for (const list of [
      [], // no `-d` at all
      ['-d', 'Ubuntu'], // no `-e`
      ['-d', 'Ubuntu', '--cd', '/home/dimi'], // `--cd` with no `-e`
      ['-d', 'Ubuntu', '--'], // `--`: the distribution's login shell would re-read the words (T-F4)
      ['-d', 'Ubuntu', '--cd', '/home/dimi', '--'],
      ['--cd', '/home/dimi', '-d', 'Ubuntu', '-e'], // the words reordered
      ['-d', 'Ubuntu', '--cd', '/home/dimi', '-e', 'extra'], // one word too many
      ['-d', 'Ubuntu', '--distribution', 'Debian', '-e'], // a flag `wsl.exe` has and this console never emits
    ]) {
      expect(() => assertWslPrefix(words(list))).toThrow()
    }
  })

  it('refuses a distribution name that could be read as a flag, and admits one that merely has a space', () => {
    // What `isSafeDistroName` is for, in both directions. `-` has to be admitted
    // -- a real distribution is called `Ubuntu-22.04` -- so `-d --help`, `-d -d`
    // and `-d --` are words whose *content* is legal and whose *position* makes
    // them a flag; the leading-hyphen rule is what refuses them, and that is the
    // whole of the argument confusion with `shell: false` (no shell re-parses a
    // word, and an array element is never split).
    for (const name of ['-d', '--', '--help', '-sneaky', '', 'Ubuntu\t22.04', 'ubuntu\n', `a${'\u0007'}b`]) {
      expect(isSafeDistroName(name), name).toBe(false)
      expect(() => assertWslPrefix(words(['-d', name, '-e']))).toThrow(/distribution name/)
    }
    // Whitespace, quotes and `;` are not argument confusion with an array argv,
    // so the argv guard admits them. The *route* does not: Part D's
    // `isDistroName` is the rule a distribution must meet to be driven, because
    // the WSL route runs the CLI through the distribution's shell, and it
    // refuses every one of the last four (fail closed, a known limit).
    for (const name of ['Ubuntu', 'Ubuntu-22.04', 'kali-linux', 'My Linux', 'Ubuntu 22.04', "dimi's distro", 'Ubuntu;x']) {
      expect(isSafeDistroName(name), name).toBe(true)
      expect(() => assertWslPrefix(words(['-d', name, '-e'])), name).not.toThrow()
    }
    for (const name of ['Ubuntu', 'Ubuntu-22.04', 'kali-linux', 'kali.rolling', 'Ubuntu_2']) {
      expect(isDistroName(name), name).toBe(true)
    }
    for (const name of ['My Linux', 'Ubuntu 22.04', "dimi's distro", 'Ubuntu;x', '-sneaky', '', 'a'.repeat(201)]) {
      expect(isDistroName(name), name).toBe(false)
    }
    // The length bound is the one the store's own values carry.
    expect(isSafeDistroName('a'.repeat(257))).toBe(false)
    expect(isSafeDistroName(`a${'b'.repeat(255)}`)).toBe(true)
  })

  it('keeps the wrapper’s flags and the CLI’s flags apart', () => {
    // Disjointness is what makes the two fields mean anything: a word in both
    // would be a wrapper word that also looks like a CLI flag, and the two
    // halves of the command line would be indistinguishable.
    expect(WSL_PREFIX_FLAGS.filter((flag) => SPAWN_FLAGS.includes(flag))).toEqual([])
    expect([...WSL_PREFIX_FLAGS]).toEqual(['-d', '--cd', '-e'])
    // ...and a wrapper word that *is* one of the CLI's own flags is refused
    // rather than passed through. This is the mutation the case exists for:
    // widening the guard with `wsl.exe`'s flags -- the alternative a design that
    // kept the wrapper in `argv` would have had to choose.
    expect(() => assertWslPrefix(words(['-d', 'Ubuntu', '--cd', '/home/dimi', '-e', '--json']))).toThrow(
      /flag this console emits to the CLI/,
    )
  })

  it('refuses a directory only for the reasons a path is refused', () => {
    // The exemption is from the *name* alphabet, not from every rule: what is
    // still refused is a control character, an empty string, an absurd length
    // and a leading `-` -- `isSafeInstallationValue`, the rule the project name,
    // the config path and the checkout already satisfy.
    expect(() => assertWslPrefix(inside('/home/dimi/src\n-rf'))).toThrow(/start the CLI in/)
    expect(() => assertWslPrefix(inside('-rf /home/dimi'))).toThrow(/start the CLI in/)
    expect(() => assertWslPrefix(inside(''))).toThrow(/start the CLI in/)
    expect(() => assertWslPrefix(inside(`/home/${'a'.repeat(4100)}/src`))).toThrow(/start the CLI in/)
    expect(() => assertWslPrefix(inside(`/home/${'a'.repeat(1000)}/src`))).not.toThrow()
  })

  it('spells its own flags and the two separators exactly, and never an approximation', () => {
    // What is checked here is that the words *this module* chooses are exactly
    // its own table -- so a prefix that grew a flag, or spelled `--cd` as
    // `--cd=`, fails here. The directory is a `wsl.exe` argument, not a shell
    // word, so `/home/Ann Lee/src` is admitted and stays one word (asserted
    // through the fake in `hands wsl.exe a directory with spaces as one word`).
    // The distribution's name is held to Part D's rule (`isDistroName`) because
    // the route runs a shell in whatever it names: `My Linux` is refused with
    // the rule's sentence before any prefix exists (fail closed).
    const wsl = (distro: string) =>
      safeContext({
        binaryPath: '',
        wsl: { distro, directory: '/home/Ann Lee/src', executable: 'C:\\Windows\\System32\\wsl.exe' },
      })
    const plan = planFor('budget.show', {}, wsl('Ubuntu-22.04'))
    const prefix = [...plan.prefix]
    for (const word of prefix.filter((word) => word.startsWith('-'))) {
      expect([...WSL_PREFIX_FLAGS], word).toContain(word)
    }
    expect(prefix).toEqual(['-d', 'Ubuntu-22.04', '--cd', '/home/Ann Lee/src', '-e'])
    for (const distro of ['My Linux', "dimi's distro", 'Ubuntu;x']) {
      expect(() => planFor('budget.show', {}, wsl(distro)), distro).toThrow(
        `${JSON.stringify(distro)} is not a WSL distribution name: a distribution is 200 characters or fewer of letters, digits, dot, dash and underscore`,
      )
    }
  })

  it('checks the prefix on the way to `wsl.exe`, and not only when asked', () => {
    // The enforcement is inside `wslWrapper`, the only producer of a prefix, for
    // the same reason `assertArgvFlags` is inside `argvFor`: a check in the
    // callers is a check a later caller can forget. The distribution's name is a
    // string that came out of `wsl.exe` or out of the store, so this says the
    // line it has to cross is the one the wrapper builds.
    const wsl = { distro: '-sneaky', directory: '/home/dimi/src', executable: 'C:\\Windows\\System32\\wsl.exe' }
    expect(() => planFor('budget.show', {}, safeContext({ binaryPath: '', wsl }))).toThrow(/distribution name/)
    expect(() => planFor('budget.show', {}, safeContext({ binaryPath: '', wsl: { ...wsl, distro: 'Ubuntu' } }))).not.toThrow()
  })
})

describe('the child environment', () => {
  const fullAppEnv = {
    PATH: '/usr/local/bin:/usr/bin:/bin',
    HOME: '/Users/owner',
    TMPDIR: '/var/folders/xx',
    LANG: 'en_US.UTF-8',
    LC_ALL: 'en_US.UTF-8',
    XDG_CONFIG_HOME: '/Users/owner/.config',
    XDG_CACHE_HOME: '/Users/owner/.cache',
    XDG_DATA_HOME: '/Users/owner/.local/share',
    XDG_STATE_HOME: '/Users/owner/.local/state',
    XDG_RUNTIME_DIR: '/run/user/501',
    FUGARO_PROJECT: 'ambient-project',
    FUGARO_CONFIG: '/Users/owner/.config/fugaro/config.toml',
    GOOGLE_APPLICATION_CREDENTIALS: '/Users/owner/.config/gcloud/application_default_credentials.json',
    ANTHROPIC_API_KEY: 'sk-ant-not-a-real-key',
    AWS_SECRET_ACCESS_KEY: 'not-a-real-key',
    AWS_PROFILE: 'prod',
    GITHUB_TOKEN: 'not-a-real-token',
    SSH_AUTH_SOCK: '/tmp/agent.sock',
  }

  it('gives the child exactly CHILD_ENV_KEYS and nothing else', () => {
    const env = childEnvironment({
      target: { project: PROJECT },
      binaryPath: '/opt/homebrew/bin/fugaro',
      appEnv: fullAppEnv,
    })
    expect(Object.keys(env).sort()).toEqual([...CHILD_ENV_KEYS].sort())
  })

  it('does not pass the app’s own credentials, profile or agent socket', () => {
    const env = childEnvironment({
      target: { project: PROJECT },
      binaryPath: '/opt/homebrew/bin/fugaro',
      appEnv: fullAppEnv,
    })
    for (const forbidden of [
      'GOOGLE_APPLICATION_CREDENTIALS',
      'ANTHROPIC_API_KEY',
      'AWS_SECRET_ACCESS_KEY',
      'AWS_PROFILE',
      'GITHUB_TOKEN',
      'SSH_AUTH_SOCK',
      'FUGARO_CONFIG',
    ]) {
      expect(env[forbidden], forbidden).toBeUndefined()
    }
    // ...and FUGARO_PROJECT is the chosen installation's, not the ambient one:
    // the ambient value is the string the app's own environment carried.
    expect(env['FUGARO_PROJECT']).toBe(PROJECT)
    expect(env['FUGARO_PROJECT']).not.toBe('ambient-project')
  })

  it('is frozen and prototypeless, so nothing can add a variable later', () => {
    const env = childEnvironment({ target: { project: PROJECT }, binaryPath: '/usr/bin/fugaro', appEnv: {} })
    expect(Object.isFrozen(env)).toBe(true)
    expect(Object.getPrototypeOf(env)).toBeNull()
  })

  it('builds the child’s PATH from the binary’s directory and every well-known prefix', () => {
    const env = childEnvironment({
      target: { project: PROJECT },
      binaryPath: '/opt/fugaro/bin/fugaro',
      appEnv: fullAppEnv,
      platform: 'darwin',
    })
    const entries = (env['PATH'] ?? '').split(':')
    expect(entries).toEqual(['/opt/fugaro/bin', ...locationsFor('darwin').childPathPrefixes])
    expect(entries.length).toBeGreaterThan(1)
    expect(entries[0]).toBe('/opt/fugaro/bin')
    for (const prefix of ['/usr/local/bin', '/usr/bin', '/bin', '/usr/sbin', '/sbin']) {
      expect(entries, prefix).toContain(prefix)
    }
  })

  it('builds the child’s PATH from the platform’s own prefixes, and only those', () => {
    // The other half of rule 5's fixed environment: the child's `PATH` is the
    // resolved binary's own directory followed by this platform's tool
    // directories. On Linux the macOS Homebrew prefix is not in it -- a
    // directory that does not exist on the machine the app is running on is not
    // a place to send a child looking for `terraform`.
    const env = childEnvironment({
      target: { project: PROJECT },
      binaryPath: '/usr/bin/fugaro',
      appEnv: { PATH: '/opt/hostile/bin:/tmp/whatever' },
      platform: 'linux',
    })
    const entries = (env['PATH'] ?? '').split(':')
    expect(entries).toEqual([
      '/usr/bin',
      '/usr/local/bin',
      '/bin',
      '/usr/sbin',
      '/sbin',
      '/home/linuxbrew/.linuxbrew/bin',
    ])
    expect(entries).not.toContain('/opt/homebrew/bin')
    expect(env['PATH']).not.toContain('/opt/hostile')
    expect(env['PATH']).not.toContain('/tmp/whatever')
    // and on macOS the same call gives the design's list, so the two are one
    // choice made in one place rather than two lists that must agree
    const mac = childEnvironment({
      target: { project: PROJECT },
      binaryPath: '/usr/bin/fugaro',
      appEnv: {},
      platform: 'darwin',
    })
    expect((mac['PATH'] ?? '').split(':')).toContain('/opt/homebrew/bin')
  })

  it('never lets the app’s own PATH reach the child', () => {
    const env = childEnvironment({
      target: { project: PROJECT },
      binaryPath: '/usr/bin/fugaro',
      appEnv: { PATH: '/opt/hostile/bin:/tmp/whatever' },
    })
    expect(env['PATH']).not.toContain('/opt/hostile')
    expect(env['PATH']).not.toContain('/tmp/whatever')
    // `/usr/bin` is both the binary's directory and a well-known prefix, and it
    // appears once: a duplicated entry is a `PATH` that says nothing extra.
    expect(childPath('/usr/bin/fugaro', 'darwin')).toBe(
      ['/usr/bin', '/opt/homebrew/bin', '/usr/local/bin', '/bin', '/usr/sbin', '/sbin'].join(':'),
    )
    // The same deduplication on Linux, where the macOS tool prefix is not in the
    // list at all.
    expect(childPath('/usr/bin/fugaro', 'linux')).toBe(
      ['/usr/bin', '/usr/local/bin', '/bin', '/usr/sbin', '/sbin', '/home/linuxbrew/.linuxbrew/bin'].join(':'),
    )
  })

  it('puts no `.` on the child’s PATH when the binary is named bare', () => {
    // The WSL route's case (T-C5): there the CLI is the bare name `fugaro`,
    // resolved by the distribution's own `PATH`, so the console has no directory
    // of its own to put in front of the tool list. `dirname('fugaro')` is `.`,
    // and a `.` entry in a `PATH` means *the child's own working directory* --
    // which for a child started in the operator's checkout is the operator's
    // checkout. Dropping it is the whole of the fix, and the tool list is
    // otherwise intact.
    expect(childPath(FUGARO_BINARY_NAME, 'linux')).toBe(
      ['/usr/local/bin', '/usr/bin', '/bin', '/usr/sbin', '/sbin', '/home/linuxbrew/.linuxbrew/bin'].join(':'),
    )
    expect(childPath(FUGARO_BINARY_NAME, 'win32')).toBe(childPath(FUGARO_BINARY_NAME, 'linux'))
    expect(childPath(FUGARO_BINARY_NAME, 'linux').split(':')).not.toContain('.')
    expect(childPath(FUGARO_BINARY_NAME, 'linux').split(':')).not.toContain('')
  })

  it('keeps HOME, TMPDIR, LANG and LC_ALL: the operator’s own tools, as their shell has them', () => {
    const env = childEnvironment({ target: { project: PROJECT }, binaryPath: '/usr/bin/fugaro', appEnv: fullAppEnv })
    expect(env['HOME']).toBe('/Users/owner')
    expect(env['TMPDIR']).toBe('/var/folders/xx')
    expect(env['LANG']).toBe('en_US.UTF-8')
    expect(env['LC_ALL']).toBe('en_US.UTF-8')
  })

  it('omits a variable the app’s environment does not have, rather than inventing one', () => {
    const env = childEnvironment({ target: { project: PROJECT }, binaryPath: '/usr/bin/fugaro', appEnv: { HOME: '/Users/owner' } })
    expect(Object.keys(env).sort()).toEqual(['FUGARO_PROJECT', 'HOME', 'PATH'])
  })

  it('states `WSLENV` empty on the WSL leg, and never carries the key on the host', () => {
    // T-E2 of docs/console-windows/implementation-plan.md, F20 and decision
    // D5(a). `wsl.exe` shares a Windows variable with the distribution only when
    // its name is listed in `WSLENV`, so the console states the list **empty**
    // rather than leaving the variable out and trusting a default (U4): "no
    // Windows variable is shared with the distribution" is then a sentence this
    // console wrote.
    //
    // The mutations this case catches: drop the key, and both assertions here
    // fail; write `'PATH'` (or anything else) into it, and the value assertion
    // fails -- which is the mutation that matters, because a shared `PATH` is a
    // Windows path in a Linux child.
    const wsl = childEnvironment({
      target: { project: PROJECT },
      binaryPath: FUGARO_BINARY_NAME,
      appEnv: fullAppEnv,
      platform: 'win32',
      wsl: true,
    })
    expect(wsl[WSLENV_KEY]).toBe('')
    expect(Object.keys(wsl).sort()).toEqual([...CHILD_ENV_KEYS, WSLENV_KEY].sort())

    // The host route does not carry the key at all: there is no `wsl.exe` between
    // the console and the CLI, so there is no share list to state.
    const host = childEnvironment({
      target: { project: PROJECT },
      binaryPath: '/opt/homebrew/bin/fugaro',
      appEnv: fullAppEnv,
    })
    expect(Object.keys(host)).not.toContain(WSLENV_KEY)
    expect(host[WSLENV_KEY]).toBeUndefined()

    // **The host's own `WSLENV` is not read.** A value the operator set for their
    // own `wsl` sessions -- `PATH` shared, or a variable of their own -- is not
    // this console's share list, and forwarding it would hand the distribution
    // whatever the operator had arranged elsewhere. The key is written, never
    // copied.
    const hostile = childEnvironment({
      target: { project: PROJECT },
      binaryPath: FUGARO_BINARY_NAME,
      appEnv: { ...fullAppEnv, WSLENV: 'PATH/l:FOO/u' },
      platform: 'win32',
      wsl: true,
    })
    expect(hostile[WSLENV_KEY]).toBe('')

    // And it is the *route* that states it, not the platform: the win32 row is
    // what a `wsl.exe` **query** uses too (`src/main/wsl.ts`), and a query starts
    // no distribution process to state a share list to.
    const query = childEnvironment({
      target: { project: PROJECT },
      binaryPath: FUGARO_BINARY_NAME,
      appEnv: fullAppEnv,
      platform: 'win32',
    })
    expect(Object.keys(query)).not.toContain(WSLENV_KEY)
  })

  it('reaches the child as its real environment, not just as a plan', async () => {
    // The end-to-end half: the stub reports the keys of its own process.env, so
    // a merge with process.env anywhere in the console would show up here.
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const source = await CliSource.create({
      target: { project: PROJECT },
      configuredBinaryPath: stub.binary,
      appEnv: { ...fullAppEnv, PATH: '/opt/hostile/bin' },
    })
    await source.environmentReport({})
    const report = stub.reports().at(-1)
    // The stub is exec'd directly (no shell in between, see `stub.ts`), so the
    // child sees exactly the fixed set -- plus, on macOS alone, the one key the
    // operating system itself adds to every process it starts. It is named
    // rather than waved through: any other addition still fails this test.
    const platformKeys = process.platform === 'darwin' ? ['__CF_USER_TEXT_ENCODING'] : []
    expect(report?.envKeys.filter((key) => !platformKeys.includes(key))).toEqual([...CHILD_ENV_KEYS].sort())
    expect(report?.env['FUGARO_CONFIG']).toBeUndefined()
    expect(report?.env['GOOGLE_APPLICATION_CREDENTIALS']).toBeUndefined()
    expect(report?.env['ANTHROPIC_API_KEY']).toBeUndefined()
    expect(report?.project).toBe(PROJECT)
    expect(report?.path).toBe(childPath(stub.binary, process.platform))
    expect(report?.path.startsWith(dirname(stub.binary))).toBe(true)
  })
})

describe('the child’s working directory', () => {
  it('spawns from the installation’s checkout when it has one', async () => {
    const directory = freshDirectory()
    const checkout = freshDirectory('fugaro-checkout-')
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const source = await CliSource.create({
      target: { project: PROJECT },
      configuredBinaryPath: stub.binary,
      checkout,
      appEnv: {},
    })
    await source.environmentReport({})
    expect(stub.reports().at(-1)?.cwd).toBe(checkout)
  })

  it('spawns from a private directory the app owns when there is no checkout, and never /', async () => {
    ownTemporaryDirectory()
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const source = await CliSource.create({
      target: { project: PROJECT },
      configuredBinaryPath: stub.binary,
      appEnv: {},
    })
    await source.environmentReport({})
    const cwd = stub.reports().at(-1)?.cwd ?? ''
    expect(cwd).not.toBe('/')
    expect(cwd.startsWith(realpathSync(tmpdir()))).toBe(true)
    expect(statSync(cwd).isDirectory()).toBe(true)
    expect(statSync(cwd).mode & 0o777).toBe(0o700)
  })

  it('passes `doctor --dir <that directory>` and never `--strict`', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    await source.environmentReport({})
    const argv = stub.reports().at(-1)?.argv ?? []
    expect(argv.slice(0, 2)).toEqual(['doctor', '--json'])
    expect(argv).toContain('--dir')
    expect(argv[argv.indexOf('--dir') + 1]).toBe(stub.reports().at(-1)?.cwd)
    expect(argv).not.toContain('--strict')
  })

  it('gives every plan a cwd, so no spawn can inherit the app’s own', () => {
    for (const row of SPAWN_COMMANDS) {
      const plan = planFor(row.operation, {}, contextFor(row.operation))
      expect(plan.cwd, row.operation).not.toBe('')
      expect(plan.cwd, row.operation).not.toBe('/')
    }
  })

  it('creates the private directory with owner-only mode and reuses it', () => {
    ownTemporaryDirectory()
    expect(privateWorkingDirectory()).toBe(privateWorkingDirectory())
    expect(statSync(privateWorkingDirectory()).mode & 0o777).toBe(0o700)
  })
})

describe('the private working directory', () => {
  const uid = typeof process.getuid === 'function' ? process.getuid() : null
  const name = PRIVATE_DIR_LEAF
  it('puts the uid in the base directory, so a second user never shares it', () => {
    // The defect this test exists for: the base was `join(tmpdir(),
    // 'fugaro-console')` for every user on the machine, created 0700 by whoever
    // ran first. Every other user then found a directory owned by someone else
    // at that name and `assertSafeParent` refused -- correctly, and unfixably
    // from their side -- so the console could not spawn at all for them, and the
    // uid-named leaf below it was never reached. A shared `/tmp` is the case.
    //
    // The temporary directory is one this test owns (TMPDIR points at a mkdtemp
    // directory, removed afterwards), so nothing depends on /tmp/fugaro-console
    // existing on the machine -- it does not on a fresh CI runner -- and the
    // test never creates it there either.
    const owned = ownTemporaryDirectory()
    const path = privateWorkingDirectory()
    const expected = join(owned, uid === null ? PRIVATE_DIR_NAME : `${PRIVATE_DIR_NAME}-${uid}`)
    expect(dirname(path)).toBe(expected)
    expect(basename(path)).toBe(PRIVATE_DIR_LEAF)
    // And the shared name is not what this user runs from: the first line of the
    // defect was that everybody's base was the same path. A string comparison,
    // so it needs no path to exist.
    if (uid !== null) expect(dirname(path)).not.toBe(join(owned, PRIVATE_DIR_NAME))
  })

  it('refuses a symbolic link where the directory should be, even to a directory it would accept', () => {
    // mkdirSync({ recursive: true }) succeeds silently on an existing path and
    // statSync follows the link, so a link planted at this name used to be
    // where every child ran.
    const base = freshDirectory()
    const elsewhere = freshDirectory()
    chmodSync(elsewhere, 0o700)
    symlinkSync(elsewhere, join(base, name))
    expect(() => privateWorkingDirectory({ base })).toThrow(/symbolic link/)
  })

  it('refuses a file where the directory should be', () => {
    const base = freshDirectory()
    writeFileSync(join(base, name), '')
    expect(() => privateWorkingDirectory({ base })).toThrow(/not a directory/)
  })

  it.skipIf(uid === null)('refuses a directory another user owns', () => {
    // The directory is created by this process, so asking for it as another uid
    // is the one way a test can present a directory whose owner is not the
    // expected one without root.
    const base = freshDirectory()
    expect(() => privateWorkingDirectory({ base, uid: (uid ?? 0) + 1 })).toThrow(/owned by uid/)
  })

  it('takes an existing directory of its own back to 0700, and refuses a base that is a link', () => {
    const base = freshDirectory()
    mkdirSync(join(base, name), { mode: 0o755 })
    chmodSync(join(base, name), 0o755)
    const path = privateWorkingDirectory({ base })
    expect(lstatSync(path).mode & 0o777).toBe(0o700)
    const linked = join(freshDirectory(), 'linked-base')
    symlinkSync(base, linked)
    expect(() => privateWorkingDirectory({ base: linked })).toThrow(/symbolic link/)
  })

  it('returns the canonical path, so --dir and the child’s own cwd agree', () => {
    const base = freshDirectory()
    const path = privateWorkingDirectory({ base })
    expect(realpathSync(path)).toBe(path)
  })

  it('creates a directory on Windows that makes no mode claim at all (F12, T-D4)', () => {
    // F12: the POSIX guarantees are POSIX-only, and the defect is stating them
    // anyway. NTFS reports two modes and neither is a POSIX one; `chmodSync` is
    // a no-op there and `process.getuid` does not exist, so a `0700` description
    // -- or an ACL -- would be a promise the filesystem does not keep. What the
    // Windows branch promises is what it can: a directory, empty, at the same
    // name every time, refusing a link or a file someone else planted there.
    //
    // The name is the red step: the Windows branch asks the process for **no
    // uid**, so the base is the shared `fugaro-console` and not
    // `fugaro-console-<uid>`. A branch that kept the POSIX name would fail here
    // on this machine, where `process.getuid` exists and would answer.
    const owned = ownTemporaryDirectory()
    const path = privateWorkingDirectory({ platform: 'win32' })
    expect(dirname(path)).toBe(join(owned, PRIVATE_DIR_NAME))
    expect(basename(path)).toBe(PRIVATE_DIR_LEAF)
    expect(statSync(path).isDirectory()).toBe(true)
    expect(readdirSync(path)).toEqual([])
    expect(privateWorkingDirectory({ platform: 'win32' })).toBe(path)
    // ...and a caller that passes a uid anyway does not get one: there is no
    // such thing to carry here, so `platform` decides and the uid is ignored.
    expect(privateWorkingDirectory({ platform: 'win32', uid: 4242 })).toBe(path)
  })

  it('still refuses a file or a link planted at the Windows name (F12, T-D4)', () => {
    const base = freshDirectory()
    writeFileSync(join(base, PRIVATE_DIR_LEAF), '')
    // A file at the name is not a directory this console will spawn from, on
    // Windows exactly as on POSIX: this is the check the Windows branch keeps.
    expect(() => privateWorkingDirectory({ base, platform: 'win32' })).toThrow(/not a directory/)
  })
})

describe('binary resolution', () => {
  it('is the design’s order on macOS, and the order is the rule', () => {
    // Design section 3.4 rule 4, verbatim, home directory last because it is the
    // one entry that is not a constant. The design is a macOS document (it
    // records the cask and the Developer ID as D4 and Check 59a); the Linux list
    // below is the owner's ruling of 2026-10-20.
    expect([...locationsFor('darwin').binaryPrefixes]).toEqual([
      '/opt/homebrew/bin',
      '/usr/local/bin',
      '~/.local/bin',
      '/usr/bin',
    ])
  })

  it('is the owner’s list on Linux, front to back, with no entry from the app’s PATH', () => {
    // The brief's order for Linux, verbatim: `/usr/local/bin`, `/usr/bin`,
    // `~/.local/bin`, `/home/linuxbrew/.linuxbrew/bin`, `~/.linuxbrew/bin`.
    // `/opt/homebrew/bin` is the macOS Homebrew prefix and is not on Linux --
    // it is not that Linuxbrew lives somewhere else, it is that a directory
    // that does not exist must not be a place this app looks for a binary.
    expect([...locationsFor('linux').binaryPrefixes]).toEqual([
      '/usr/local/bin',
      '/usr/bin',
      '~/.local/bin',
      '/home/linuxbrew/.linuxbrew/bin',
      '~/.linuxbrew/bin',
    ])
    // "Never the inherited PATH": every entry of the well-known list is an
    // absolute path or the one `~` form, and none of them is read out of the
    // app's environment, and there is no `PATH` step after this list -- see the
    // tests below, which are the same rule from the other side.
    for (const platform of ['darwin', 'linux']) {
      for (const prefix of locationsFor(platform).binaryPrefixes) {
        expect(prefix, `${platform}: ${prefix}`).toMatch(/^(?:~\/|\/)/)
      }
    }
    // and the two lists differ, so "chosen per platform" is a fact rather than a
    // shared constant with two names
    expect(locationsFor('linux').binaryPrefixes).not.toEqual(locationsFor('darwin').binaryPrefixes)
  })

  it('is chosen in one place, by process.platform, and only there', () => {
    // The brief's rule: the per-platform choice lives in ONE place. This is that
    // place -- the table below is the whole of the per-platform data, and every
    // reader goes through `locationsFor`.
    expect(Object.keys(PLATFORM_LOCATIONS).toSorted()).toEqual(['darwin', 'linux', 'win32'])
    expect(locationsFor('darwin')).toBe(DARWIN_LOCATIONS)
    expect(locationsFor('linux')).toBe(LINUX_LOCATIONS)
    expect(locationsFor('win32')).toBe(WIN32_LOCATIONS)
    expect(locationsFor()).toBe(PLATFORM_LOCATIONS[process.platform])
    // Every row is frozen and total: every platform has both a resolution
    // order and a child `PATH`, and neither can be extended afterwards. The
    // win32 row's `binaryPrefixes` is empty on purpose -- see the next test --
    // so the "more than nothing" half is asked of the two host rows.
    for (const platform of ['darwin', 'linux']) {
      const locations = locationsFor(platform)
      expect(Object.isFrozen(locations)).toBe(true)
      expect(Object.isFrozen(locations.binaryPrefixes)).toBe(true)
      expect(Object.isFrozen(locations.childPathPrefixes)).toBe(true)
      expect(locations.binaryPrefixes.length).toBeGreaterThan(0)
      expect(locations.childPathPrefixes.length).toBeGreaterThan(0)
      expect(locations.childPathPrefixes).toContain('/usr/bin')
      // A `PATH` entry is not a place a `~` can appear: the child receives a
      // literal string, and nothing expands a tilde in it.
      for (const prefix of locations.childPathPrefixes) {
        expect(prefix, `${platform} child PATH: ${prefix}`).toMatch(/^\//)
      }
    }
  })

  it('refuses a platform nobody decided, rather than answering with Linux’s directories', () => {
    // F5's other half, and the half that used to be a fallback. A platform with
    // no row answered with the POSIX list. That was survivable while the app
    // shipped only for darwin and Linux: the branch was reached by a developer
    // running the sources somewhere the app has never been packaged, and the
    // POSIX layout was the nearest thing to a right answer.
    //
    // It is not survivable now that `win32` is a route the app actually takes.
    // A Windows child handed a Linux `PATH` cannot resolve anything on it, and
    // `/usr/local/bin` on Windows is a *relative* path under the current drive
    // -- so the same silent answer would have the console looking for a binary
    // in a directory that means something different on the machine it is
    // running on. A platform with no row is a platform nobody has decided, and
    // the honest answer is to say so rather than to guess at where to execute a
    // program.
    expect(() => locationsFor('freebsd')).toThrow(/freebsd/)
    expect(() => locationsFor('sunos')).toThrow(/sunos/)
    expect(() => locationsFor('aix')).toThrow(/aix/)
    expect(() => locationsFor('')).toThrow(/platform/)
    // And with no argument at all. `locationsFor()` means "this machine's
    // platform", so a machine running a platform nobody decided is refused by
    // the same lookup rather than answered with Linux's directories -- which is
    // the case the old fallback was written for, and the case it got wrong.
    // `process.platform` is the only place that value comes from and it is
    // configurable, so the test states it and puts it back.
    const machine = process.platform
    Object.defineProperty(process, 'platform', { value: 'freebsd', configurable: true })
    try {
      expect(() => locationsFor()).toThrow(/freebsd/)
      expect(() => locationsFor(undefined)).toThrow(/freebsd/)
    } finally {
      Object.defineProperty(process, 'platform', { value: machine, configurable: true })
    }
    // and the three rows that were decided still answer, `win32` included: the
    // refusal is about the platforms *nobody* decided, not about the new one.
    expect(locationsFor('darwin')).toBe(DARWIN_LOCATIONS)
    expect(locationsFor('linux')).toBe(LINUX_LOCATIONS)
    expect(locationsFor('win32')).toBe(WIN32_LOCATIONS)
  })

  it('is the WSL route on win32, and it is not a directory list', () => {
    // T-C2. The Windows console does not walk the host's directories looking
    // for a `fugaro`: there is no `fugaro.exe`, the CLI is a Linux program
    // inside a distribution, and the way to reach it is `wsl.exe`. So the win32
    // row carries no resolution order at all -- `binaryPrefixes` is empty, and
    // `binDirectories` on this platform finds nothing, which is the truth about
    // the host -- and it says so with `route` rather than leaving a reader to
    // infer it from an empty list.
    expect(WIN32_LOCATIONS.route).toBe('wsl')
    expect([...WIN32_LOCATIONS.binaryPrefixes]).toEqual([])
    // The child's `PATH` is **Linux's**, and that is the whole of F5: the
    // process on the other end of `wsl.exe` is a Linux process, so the
    // directories it may find `terraform` in are Linux's. macOS's list here
    // would put `/opt/homebrew/bin` on a Linux child's `PATH`; a list of
    // Windows directories would put nothing on it that a Linux lookup can use.
    expect([...WIN32_LOCATIONS.childPathPrefixes]).toEqual([...LINUX_LOCATIONS.childPathPrefixes])
    expect(WIN32_LOCATIONS.childPathPrefixes).toContain('/usr/bin')
    expect([...WIN32_LOCATIONS.childPathPrefixes]).not.toContain('/opt/homebrew/bin')
    for (const prefix of WIN32_LOCATIONS.childPathPrefixes) {
      expect(prefix, `win32 child PATH: ${prefix}`).toMatch(/^\//)
    }
    expect(Object.isFrozen(WIN32_LOCATIONS)).toBe(true)
    expect(Object.isFrozen(WIN32_LOCATIONS.binaryPrefixes)).toBe(true)
    expect(Object.isFrozen(WIN32_LOCATIONS.childPathPrefixes)).toBe(true)
    // The host route is what the other two rows are, and it is spelled rather
    // than defaulted: a row that forgot to say would otherwise read as `host`.
    expect(DARWIN_LOCATIONS.route).toBe('host')
    expect(LINUX_LOCATIONS.route).toBe('host')
  })

  it('resolves against the platform’s list, and the list is an argument rather than a global', () => {
    // `binDirectories` walks the same table `locationsFor` returns, so the two
    // cannot drift; `platform` is how a test drives it, because the runner this
    // suite executes on is not the machine the app ships to.
    const linux = binDirectories({ home: '/home/owner', platform: 'linux' })
    expect([...linux]).toEqual([
      '/usr/local/bin/fugaro',
      '/usr/bin/fugaro',
      '/home/owner/.local/bin/fugaro',
      '/home/linuxbrew/.linuxbrew/bin/fugaro',
      '/home/owner/.linuxbrew/bin/fugaro',
    ])
    const darwin = binDirectories({ home: '/Users/owner', platform: 'darwin' })
    expect([...darwin]).toEqual([
      '/opt/homebrew/bin/fugaro',
      '/usr/local/bin/fugaro',
      '/Users/owner/.local/bin/fugaro',
      '/usr/bin/fugaro',
    ])
    // and `~` is the *given* home directory, never the process's own: the
    // resolution is a pure function of its arguments.
    expect([...binDirectories({ home: '/nowhere', platform: 'linux' })]).toContain(
      '/nowhere/.local/bin/fugaro',
    )
    // and the win32 row is not a directory list at all: nothing is walked on
    // the host, because the CLI is not on the host (T-C2). The empty list is
    // the row's `binaryPrefixes`, not a special case in `binDirectories` --
    // which is why the walk is the same code on all three platforms.
    expect([...binDirectories({ home: 'C:\\Users\\owner', platform: 'win32' })]).toEqual([])
  })

  it('asks the mode question of the platform whose filesystem it is asking (F4, T-C7)', () => {
    // The runner this suite executes on is POSIX, so the Windows answer is
    // driven rather than observed -- and the POSIX answer is asserted next to
    // it, because a platform argument that changed both branches would be a
    // different rule, not this one.
    const directory = freshDirectory()
    const plain = join(directory, 'plain')
    writeFileSync(plain, '#!/bin/sh\n', { mode: 0o644 })
    const runnable = join(directory, 'runnable')
    writeFileSync(runnable, '#!/bin/sh\n', { mode: 0o755 })
    const folder = join(directory, 'folder')
    mkdirSync(folder)

    // POSIX, unchanged: a regular file with an execute bit, and one without is
    // not a candidate.
    expect(isExecutableFile(runnable, 'linux')).toBe(true)
    expect(isExecutableFile(plain, 'linux')).toBe(false)

    // Windows reports two file modes and neither of them is an execute bit, so
    // the POSIX question cannot be asked of a Windows path: there, a regular
    // file is runnable -- the same answer `CreateProcess` gives.
    expect(isExecutableFile(plain, 'win32')).toBe(true)
    expect(isExecutableFile(runnable, 'win32')).toBe(true)
    // A directory is still not a file, and neither is a path that is not there.
    expect(isExecutableFile(folder, 'win32')).toBe(false)
    expect(isExecutableFile(join(directory, 'gone'), 'win32')).toBe(false)
  })

  it('reads a Windows installation’s binary that has no execute bit, and refuses it on POSIX', async () => {
    // The same file, two hosts. A CLI installed on Windows by copying a file is
    // runnable -- the mode the copy happens to carry says nothing about it --
    // while on POSIX that file is not a candidate at all. The platform reaches
    // the *call site*, not only the function: this is `resolveInstalledBinary`,
    // the route a stored installation takes.
    //
    // The child is scripted rather than real, because the runner this suite
    // executes on is POSIX and would refuse to execute a 0644 file itself
    // (EACCES) -- which is the host's rule, not the console's, and exactly the
    // rule this test is about.
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' } } })
    chmodSync(stub.binary, 0o644)
    const target = { project: PROJECT }
    const ran: string[] = []
    const spawner: ChildSpawner = (command) => {
      ran.push(command)
      const child = scriptedChild()
      setImmediate(() => {
        child.emitStdout('1.4.2\n')
        child.exit(0, null)
      })
      return child
    }

    const windows = await resolveInstalledBinary({
      binaryPath: stub.binary,
      appEnv: {},
      directory,
      target,
      platform: 'win32',
      spawner,
    })
    expect(windows).toMatchObject({ ok: true, path: stub.binary, version: '1.4.2' })
    expect(ran).toEqual([stub.binary])

    const posix = await resolveInstalledBinary({
      binaryPath: stub.binary,
      appEnv: {},
      directory,
      target,
      platform: 'linux',
      spawner,
    })
    expect(posix.ok).toBe(false)
    if (!posix.ok) {
      expect(posix.kind).toBe('no-binary')
      expect(posix.reason).toContain('not an executable file')
    }
    // The POSIX call added nothing to the list: the mode question answered
    // before the runner was reached at all.
    expect(ran).toEqual([stub.binary])
  })

  it('never takes the app’s own PATH as a place to find the binary', async () => {
    // The owner's ruling of 2026-10-20 (design section 3.4 rule 4): the console
    // resolves the `fugaro` that is *installed*, and the environment the app
    // happened to be launched with is not a place anything is installed. The
    // candidate list is the configured path and the well-known prefixes, and a
    // `fugaro` that exists only on the app's `PATH` is not found at all.
    const base = freshDirectory()
    const onPath = join(base, 'path-bin')
    const stub = installStub(onPath, { commands: { version: { stdout: '9.9.9\n' } } })
    const resolution = await resolveBinary({
      appEnv: { PATH: onPath },
      directory: base,
      target: { project: PROJECT },
      prefixes: [join(base, 'prefix-a', 'bin')],
    })
    expect(resolution.ok).toBe(false)
    // The refusal names the one place it looked, and the app's PATH is not in
    // it: this is what the absent-binary screen shows as "searched".
    expect(resolution.ok === false && resolution.reason).toContain(join(base, 'prefix-a', 'bin', 'fugaro'))
    expect(resolution.ok === false && resolution.reason).not.toContain(onPath)
    // The stub never ran: it was never a candidate.
    expect(stub.calls()).toBe(0)
  })

  it('has no appEnv parameter, so the app’s PATH cannot re-enter the order', () => {
    // The type is part of the guard: this file's resolution order had a third
    // step (every absolute entry of the app's own `PATH`) and removing it from
    // the signature is what keeps it out. A test that only asserted the
    // candidate list could be satisfied by a `PATH` walk added under another
    // name; this one fails the day the parameter comes back.
    const source = readFileSync(new URL('../../src/main/spawn.ts', import.meta.url), 'utf8')
    const signature = source.slice(source.indexOf('export function binDirectories('))
    const body = signature.slice(0, signature.indexOf('\n}'))
    expect(body).not.toContain('appEnv')
    expect(body).not.toContain('PATH')
  })

  it('never lists a relative configured path as a place to find the binary', () => {
    // The same rule as the `PATH` entries above, one step earlier in the order:
    // this list is what `isExecutableFile` is asked about, and that question is
    // put to the app's working directory, while the child runs in the
    // installation's. A relative configured path is therefore not a place to
    // look -- and `resolveBinary` refuses it rather than falling through.
    expect([
      ...binDirectories({ configuredPath: 'bin/fugaro', home: '/Users/owner', prefixes: [] }),
    ]).toEqual([])
    expect([
      ...binDirectories({ configuredPath: './tools/fugaro', home: '/Users/owner', prefixes: [] }),
    ]).toEqual([])
    expect([
      ...binDirectories({ configuredPath: '~/tools/fugaro', home: '/Users/owner', prefixes: [] }),
    ]).toEqual([])
  })

  it('puts an absolute configured path first, with the well-known prefixes after it', () => {
    expect([
      ...binDirectories({ configuredPath: '/opt/fugaro/bin/fugaro', home: '/Users/owner', prefixes: [] }),
    ]).toEqual(['/opt/fugaro/bin/fugaro'])
  })

  it('refuses a configured path that is not absolute, rather than substituting another installation', async () => {
    // The review of Task 4.1's finding: `bin/fugaro` was validated against the
    // app's cwd (`isExecutableFile`) and then handed to the child, whose cwd is
    // the installation's -- so the file that answered the version check need not
    // be the file that runs. Here the relative path does not exist at all, and a
    // perfectly good stub sits in a well-known prefix: before this guard the walk
    // fell through and reported *that* installation's numbers under the
    // operator's chosen path, which is the silent substitution rule 3 refuses.
    const base = freshDirectory()
    const prefix = join(base, 'prefix', 'bin')
    const stub = installStub(prefix, { commands: { version: { stdout: '9.9.9\n' } } })
    const resolution = await resolveBinary({
      configuredPath: 'bin/fugaro',
      appEnv: {},
      directory: base,
      target: { project: PROJECT },
      prefixes: [prefix],
    })
    expect(resolution.ok).toBe(false)
    expect(resolution.ok === false && resolution.path).toBe('bin/fugaro')
    expect(resolution.ok === false && resolution.reason).toMatch(/not absolute/)
    expect(resolution.ok === false && resolution.reason).toContain('bin/fugaro')
    // The sentence names the store's file and the field it read (review of this
    // branch): the operator who sees this line has to be able to find the value
    // that caused it, and after the ruling of 2026-10-20 no screen ever asks for
    // a binary path -- so the message must not ask for one either.
    // Spelled out, so a rename of the constant cannot move the message away from
    // the file the operator actually has on disk.
    expect(resolution.ok === false && resolution.reason).toContain('installations.json')
    expect(resolution.ok === false && resolution.reason).toContain('binaryPath')
    expect(resolution.ok === false && resolution.reason).not.toMatch(/give an absolute path/)
    // Nothing was spawned: the refusal is before the walk, so the stub in the
    // prefix never ran.
    expect(stub.calls()).toBe(0)
  })

  it('resolves a configured path first, and reports where it came from', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' } } })
    const resolution = await resolveBinary({
      configuredPath: stub.binary,
      appEnv: { PATH: '/usr/bin' },
      directory: freshDirectory(),
      target: { project: PROJECT },
    })
    expect(resolution.ok && resolution.path).toBe(stub.binary)
    expect(resolution.ok && resolution.version).toBe('1.4.2')
    expect(resolution.ok && resolution.source).toBe('configured')
  })

  it('walks the well-known prefixes in order, and stops there', async () => {
    const base = freshDirectory()
    const first = join(base, 'prefix-a', 'bin')
    const second = join(base, 'prefix-b', 'bin')
    const onPath = join(base, 'path-bin')
    const options = {
      appEnv: { PATH: onPath },
      directory: base,
      target: { project: PROJECT },
      prefixes: [first, second],
    }
    installStub(join(onPath, '..'), {}) // an empty directory: no binary on PATH
    installStub(second, { commands: { version: { stdout: '2.0.0\n' } } })
    const secondOnly = await resolveBinary(options)
    expect(secondOnly.ok && secondOnly.path).toBe(join(second, 'fugaro'))

    installStub(first, { commands: { version: { stdout: '3.0.0\n' } } })
    const both = await resolveBinary(options)
    expect(both.ok && both.path).toBe(join(first, 'fugaro'))
    expect(both.ok && both.version).toBe('3.0.0')
  })

  it('refuses when the only `fugaro` is the one the app’s PATH points at', async () => {
    // The other half of the test above, at the end of the walk rather than in
    // the middle: every well-known prefix is empty, the app's `PATH` holds a
    // working stub, and the resolution still refuses. This is the mutation that
    // matters -- reintroducing the `PATH` step makes this test find the stub.
    const base = freshDirectory()
    const onPath = join(base, 'path-bin')
    const stub = installStub(onPath, { commands: { version: { stdout: '9.9.9\n' } } })
    const prefix = join(base, 'prefix-a', 'bin')
    const resolution = await resolveBinary({
      appEnv: { PATH: `${join(base, 'nowhere')}:${onPath}`, HOME: base },
      home: base,
      directory: base,
      target: { project: PROJECT },
      prefixes: [prefix],
    })
    expect(resolution.ok).toBe(false)
    expect(resolution.ok === false && resolution.path).toBeNull()
    expect(resolution.ok === false && resolution.reason).toContain(join(prefix, 'fugaro'))
    expect(stub.calls()).toBe(0)
  })

  it('resolves ~/.local/bin for the home directory it was given', async () => {
    const home = freshDirectory('fugaro-home-')
    const stub = installStub(join(home, '.local', 'bin'), { commands: { version: { stdout: '1.0.0\n' } } })
    const resolution = await resolveBinary({
      appEnv: {},
      home,
      directory: home,
      target: { project: PROJECT },
      // The well-known list is injected because this machine may have a `fugaro`
      // in one of the earlier prefixes (`/usr/local/bin` here does); the prefix
      // under test is the one with the tilde in it.
      prefixes: ['~/.local/bin'],
    })
    expect(resolution.ok && resolution.path).toBe(stub.binary)
  })

  it('takes the home directory from the process environment when the caller passes none', async () => {
    // **The e2e suite's hermetic override, in the fast suite.** `test/support/
    // cliStub.ts` chooses the CLI a launch reads the only way rule 4 allows: it
    // writes the stub into a scratch home's `~/.local/bin` and points the
    // launch's `HOME` at that home. That only works because the app's handshake
    // (`resolveHandshakeBinary`) passes **no** `home` -- so `binDirectories`
    // falls back to `homedir()`, which is the process's `HOME`, which for the
    // app is the environment `electron.launch` was handed. A resolver that read
    // the *runner's* home instead would search the operator's real `~/.local/bin`
    // and the specs would be asserting a fact about the machine.
    const home = freshDirectory('fugaro-hermetic-home-')
    const stub = installStub(join(home, '.local', 'bin'), { commands: { version: { stdout: '1.0.0\n' } } })
    // A decoy where the app's `PATH` points: rule 4 does not search it, so the
    // helper's `PATH` is a decoy on purpose and a resolver that read it would
    // find this one instead.
    const onPath = join(freshDirectory('fugaro-hermetic-path-'), 'bin')
    const decoy = installStub(onPath, { commands: { version: { stdout: '9.9.9\n' } } })
    vi.stubEnv('HOME', home)
    const resolution = await resolveBinary({
      appEnv: { HOME: home, PATH: onPath },
      directory: home,
      target: { project: PROJECT },
      // The well-known list is injected for the reason the test above gives --
      // this machine may have a `fugaro` in an earlier prefix -- because what is
      // under test is which home the tilde is joined to, not the list's order.
      prefixes: ['~/.local/bin'],
    })
    expect(resolution.ok && resolution.path).toBe(stub.binary)
    expect(decoy.calls(), 'the resolution searched the app’s PATH').toBe(0)
  })

  it('refuses a candidate whose version cannot be read, rather than skipping it', async () => {
    const base = freshDirectory()
    const first = join(base, 'prefix-a', 'bin')
    const second = join(base, 'prefix-b', 'bin')
    installStub(first, { commands: { version: { stdout: 'not a version at all\n' } } })
    installStub(second, { commands: { version: { stdout: '2.0.0\n' } } })
    const resolution = await resolveBinary({
      appEnv: {},
      directory: base,
      target: { project: PROJECT },
      prefixes: [first, second],
    })
    // The second candidate is a perfectly good binary, and falling through to it
    // is exactly the failure this refuses: the console would silently read a
    // different installation than the one at the configured path.
    expect(resolution.ok).toBe(false)
    expect(resolution.ok === false && resolution.path).toBe(join(first, 'fugaro'))
    expect(resolution.ok === false && resolution.reason).toMatch(/not a version/)
  })

  it('refuses a binary that exits non-zero for `version`, with the CLI’s sentence', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: { version: { stderr: 'fugaro: this build cannot report a version\n', code: 1 } },
    })
    const resolution = await resolveBinary({
      configuredPath: stub.binary,
      appEnv: {},
      directory,
      target: { project: PROJECT },
    })
    expect(resolution.ok).toBe(false)
    expect(resolution.ok === false && resolution.reason).toMatch(/this build cannot report a version/)
  })

  it('refuses when nothing is installed at all, and says where it looked', async () => {
    const base = freshDirectory()
    const resolution = await resolveBinary({
      appEnv: { PATH: join(base, 'empty') },
      directory: base,
      target: { project: PROJECT },
      prefixes: [join(base, 'prefix-a', 'bin')],
    })
    expect(resolution.ok).toBe(false)
    expect(resolution.ok === false && resolution.path).toBeNull()
    expect(resolution.ok === false && resolution.reason).toContain(join(base, 'prefix-a', 'bin', 'fugaro'))
  })

  it('never runs a directory named fugaro, or a file without an execute bit', async () => {
    const base = freshDirectory()
    const prefix = join(base, 'prefix', 'bin')
    installStub(join(prefix, 'fugaro'), { commands: { version: { stdout: '1.0.0\n' } } })
    const asDirectory = await resolveBinary({
      appEnv: {},
      directory: base,
      target: { project: PROJECT },
      prefixes: [prefix],
    })
    expect(asDirectory.ok).toBe(false)
  })

  it('parses the two version lines the CLI can print, and nothing else', () => {
    expect(parseVersionLine('1.4.2\n')).toEqual({ ok: true, version: '1.4.2' })
    expect(parseVersionLine('v0.8.0-rc.1\n')).toEqual({ ok: true, version: 'v0.8.0-rc.1' })
    expect(parseVersionLine('dev\n')).toEqual({ ok: true, version: 'dev' })
    expect(parseVersionLine('').ok).toBe(false)
    expect(parseVersionLine('Usage:\n  fugaro [command]\n').ok).toBe(false)
    expect(parseVersionLine('1.0.0\nextra\n').ok).toBe(false)
  })

  it('drops the CLI’s `fugaro: ` prefix and keeps its sentence verbatim', () => {
    expect(cliSentence('fugaro: no such run\n')).toBe('no such run')
    expect(cliSentence('')).toBeNull()
    expect(cliSentence('\n')).toBeNull()
    expect(cliSentence('panic: nil map\n')).toBe('panic: nil map')
  })

  it('refuses to build a source around a binary whose version cannot be read', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: 'nope\n' } } })
    await expect(
      CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} }),
    ).rejects.toBeInstanceOf(SourceRefusal)
  })
})

describe('the WSL resolution path', () => {
  // T-C5 of docs/console-windows/implementation-plan.md, decisions D1(a) and
  // D2(a): on Windows the CLI is a Linux program inside a distribution, so the
  // resolver's answer is a plan whose command is `wsl.exe`, whose wrapper names
  // the distribution and the directory, and whose program word is `fugaro` --
  // the name the *distribution* resolves, which is the point of running it
  // inside. The four refusals are told apart because each one is a different
  // instruction to the operator.
  //
  // Every case here is driven through the fake `wsl.exe` (T-A1), so the claim is
  // the fake's own report -- what the distribution received -- and never the
  // console's account of itself.
  const TWO_DISTROS: WslFixture = {
    list: 'Ubuntu\r\nDebian\r\n',
    status: 'Default Distribution: Debian\r\nDefault Version: 2\r\n\r\nWSL version: 2.6.1.0\r\n',
  }

  /** NO_WSL is a `wsl.exe` that cannot answer at all: WSL is not installed, or
   * the feature is off. */
  const NO_WSL: WslFixture = {
    list: '',
    status: '',
    queryFailure: {
      status: 1,
      stderr: 'Windows Subsystem for Linux has no installed distributions.\r\n',
    },
  }

  /** wslRoot is the arrangement the cases start from: a fake `wsl.exe`, a host
   * directory to run it in, and a directory inside the distribution to start the
   * CLI in. The two are deliberately different directories, because they are the
   * two sides of the boundary: `cwd` is where `wsl.exe` itself is started and
   * `distroDirectory` is what `--cd` names. */
  function wslRoot(fixture: WslFixture = TWO_DISTROS, options: { readonly version?: string } = {}): {
    wsl: WslStub
    host: string
    distroDirectory: string
  } {
    const root = freshDirectory()
    const host = join(root, 'windows')
    mkdirSync(host, { recursive: true })
    const distroDirectory = join(root, 'home', 'dimi', 'src')
    mkdirSync(distroDirectory, { recursive: true })
    const wsl = installWslStub(host, {
      distros: fixture,
      scenario: { commands: { version: { stdout: `${options.version ?? '1.4.2'}\n` } } },
    })
    return { wsl, host, distroDirectory }
  }

  /** resolveInside is `resolveInstalledBinary` on the WSL route, with the two
   * directories this test owns and the fake's `wsl.exe` as the program. */
  function resolveInside(
    arrangement: { wsl: WslStub; host: string; distroDirectory: string },
    overrides: {
      readonly binaryPath?: string
      readonly distro?: string
      readonly wslDirectory?: string | undefined
    } = {},
  ): Promise<Awaited<ReturnType<typeof resolveInstalledBinary>>> {
    return resolveInstalledBinary({
      binaryPath: overrides.binaryPath ?? '',
      distro: overrides.distro ?? 'Ubuntu',
      ...('wslDirectory' in overrides
        ? overrides.wslDirectory === undefined
          ? {}
          : { wslDirectory: overrides.wslDirectory }
        : { wslDirectory: arrangement.distroDirectory }),
      wslPath: arrangement.wsl.binary,
      appEnv: {},
      directory: arrangement.host,
      target: { project: PROJECT },
    })
  }

  it('builds the wrapper on the plan: `wsl.exe`, the distribution, the directory, the CLI’s own argv', () => {
    const wsl = installWslStub(freshDirectory(), { distros: TWO_DISTROS })
    const context = safeContext({
      binaryPath: '',
      directory: '/Users/owner/AppData/Local/Temp/fugaro-console/cwd',
      wsl: { distro: 'Ubuntu', directory: '/home/dimi/src/fugaro', executable: wsl.binary },
    })
    const plan = planFor('budget.show', {}, context)
    // The program the console starts is `wsl.exe` -- here, the fake -- and not
    // the CLI: on this route there is no host path to the CLI at all.
    expect(plan.command).toBe(wsl.binary)
    expect([...plan.prefix]).toEqual(['-d', 'Ubuntu', '--cd', '/home/dimi/src/fugaro', '-e'])
    // `argv[0]` is the bare name, not a path: the distribution's own `PATH`
    // resolves it, which is the point of running the CLI inside (F5).
    expect(plan.argv[0]).toBe(FUGARO_BINARY_NAME)
    expect([...plan.argv]).toEqual([
      FUGARO_BINARY_NAME,
      'budget',
      'show',
      '--json',
      '--project',
      PROJECT,
    ])
    // Two directories, and they are on two sides of the boundary: `cwd` is the
    // **host** directory `wsl.exe` itself is started in, and the CLI's own
    // directory is the wrapper's `--cd`.
    expect(plan.cwd).toBe('/Users/owner/AppData/Local/Temp/fugaro-console/cwd')
    // The whole line, as it will be spawned and as the log records it (O4).
    // The CLI's argv is **inside** the wrapper's script rather than beside it
    // (T-F4, D6(a)): the shell is what records the pid inside the distribution,
    // and `exec` makes that pid the CLI's own.
    const line = [...spawnedArgv(plan)]
    expect(line.slice(0, 6)).toEqual([wsl.binary, '-d', 'Ubuntu', '--cd', '/home/dimi/src/fugaro', '-e'])
    expect(line.slice(6, 8)).toEqual([WSL_SHELL_PROGRAM, WSL_SHELL_FLAG])
    expect(line).toHaveLength(9)
    const script = line[8] ?? ''
    // The marker T-F1 classifies by, the pid T-F4's stop names -- on the same
    // line, because that is the one channel the console reads -- and the CLI's
    // argv, each word quoted, so a project named `$(id)` is a word.
    //
    // The marker carries **this plan's nonce** (review of `#981`): the fixed
    // prefix a CLI could echo, then `WSL_WRAPPER_NONCE_BYTES` bytes of hex drawn
    // per spawn. The mutation this catches: print the fixed sentence alone, and
    // the assertion on the marker's shape fails here -- and the forged-line cases
    // in `the wrapper's own line` below fail with it.
    const marker = wrapperMarkerOf(plan)
    expect(marker).toMatch(
      new RegExp(`^${WSL_WRAPPER_MARKER_PREFIX} [0-9a-f]{${WSL_WRAPPER_NONCE_BYTES * 2}}$`),
    )
    expect(script).toContain(`printf '%s %s\\n' '${marker}' "$$" >&2`)
    expect(script).toContain(`exec 'fugaro' 'budget' 'show' '--json' '--project' '${PROJECT}'`)
    // Nothing is written anywhere: the pid is a number on stderr, so there is no
    // file for the shell to name in a directory the console would then have to
    // spell the same way (the shell's `--cd` is the checkout; `plan.cwd` is a
    // host path, and the two are not the same directory). The script's one
    // redirection is `>&2`.
    expect(script).not.toMatch(/>\s*'/)
    expect(plan.wrapper?.distro).toBe('Ubuntu')
  })

  it('draws a fresh nonce for every plan, so the marker is one spawn’s own', () => {
    // SECURITY-SENSITIVE, and the property the whole fix rests on (review of
    // `#981`): the marker is unforgeable because nothing the CLI prints can carry
    // bytes it never saw. A nonce fixed in this module would be as printable by
    // the CLI as the fixed prefix was, and **nothing else in this file would
    // notice**: `[0-9a-f]{64}` is a shape a constant satisfies, every reader case
    // feeds the plan's own marker back in, and the forged-line cases forge the
    // fixed prefix alone. Hence this case, which is the one that fails when the
    // draw is hoisted out of `wslWrapperFor`.
    const wsl = installWslStub(freshDirectory(), { distros: TWO_DISTROS })
    const context = safeContext({
      binaryPath: '',
      directory: '/Users/owner/AppData/Local/Temp/fugaro-console/cwd',
      wsl: { distro: 'Ubuntu', directory: '/home/dimi/src/fugaro', executable: wsl.binary },
    })
    const first = planFor('budget.show', {}, context)
    const second = planFor('budget.show', {}, context)
    // One context, two plans, two markers -- and the same script text apart from
    // the nonce on it, which is what "the same spawn's" means: the marker the
    // readers accept is the one the script the plan spawns printed.
    expect(wrapperMarkerOf(second)).not.toBe(wrapperMarkerOf(first))
    expect(second.wrapper?.words[2]).not.toBe(first.wrapper?.words[2])
    // A second plan's marker is not one the first plan's readers accept: the two
    // do not overlap, so a line the first spawn's script printed is not a line
    // the second spawn reads as its own.
    expect(wrapperPidIn(`${wrapperMarkerOf(first)} 4321\n`, wrapperMarkerOf(second))).toBeNull()
  })

  it('gives the CLI inside the distribution Linux’s PATH, CHILD_ENV_KEYS and an empty WSLENV', () => {
    // The credential boundary's near side, on the WSL route: the environment the
    // CLI receives is built by `childEnvironment` and nothing else, and its PATH
    // is a Linux one even though the host is Windows (F5, T-C2). `WSLENV` is
    // one of the two words this route writes itself, and it is the *statement*
    // that the Windows environment is not shared with the distribution (T-E2,
    // D5(a)).
    const wsl = installWslStub(freshDirectory(), { distros: TWO_DISTROS })
    const plan = planFor('budget.show', {}, safeContext({
      binaryPath: FUGARO_BINARY_NAME,
      appEnv: {
        PATH: 'C:\\Windows\\System32;C:\\Windows',
        HOME: '/home/dimi',
        AWS_SECRET_ACCESS_KEY: 'planted',
        GITHUB_TOKEN: 'planted',
      },
      wsl: { distro: 'Ubuntu', directory: '/home/dimi/src', executable: wsl.binary },
    }))
    // Every key is a `CHILD_ENV_KEY` or one of the two words this route writes
    // itself -- the allow-list is the enforcement, and the app's own environment
    // is read for nothing else.
    for (const key of Object.keys(plan.env)) expect([...CHILD_ENV_KEYS, WSLENV_KEY, WSL_UTF8_VAR]).toContain(key)
    // The keys are exactly the ones this `appEnv` supplied plus the two this
    // module always writes (`PATH`, `FUGARO_PROJECT`), the share list and
    // `WSL_UTF8` -- a key that is not on the allow-list would be a key that came
    // from the app's environment, which is the leak this case exists to refuse.
    expect(Object.keys(plan.env).sort()).toEqual(['FUGARO_PROJECT', 'HOME', 'PATH', WSLENV_KEY, WSL_UTF8_VAR].sort())
    expect(plan.env[WSLENV_KEY]).toBe('')
    // **`WSL_UTF8=1` on the CLI's plan too** (review of `#981`): it was on the
    // queries only, and a CLI plan's `wsl.exe` is the one whose stderr carries
    // both its own warnings and the wrapper's line -- so this is the call the
    // switch matters most for. It is the console's own constant, and `WSLENV`
    // empty keeps it on the near side of the distribution.
    expect(plan.env[WSL_UTF8_VAR]).toBe(WSL_UTF8_VALUE)
    expect(plan.env['PATH']).toBe(childPath(FUGARO_BINARY_NAME, 'linux'))
    expect(plan.env['PATH']).not.toContain('C:\\Windows')
    expect(Object.keys(plan.env)).not.toContain('AWS_SECRET_ACCESS_KEY')
    expect(Object.keys(plan.env)).not.toContain('GITHUB_TOKEN')
  })

  it('names the distribution’s directory for `doctor --dir`, and no directory at all when it has none', () => {
    const wsl = installWslStub(freshDirectory(), { distros: TWO_DISTROS })
    const inside = planFor('environment.report', {}, safeContext({
      binaryPath: FUGARO_BINARY_NAME,
      directory: '/Users/owner/AppData/Local/Temp/fugaro-console/cwd',
      wsl: { distro: 'Ubuntu', directory: '/home/dimi/src/fugaro', executable: wsl.binary },
    }))
    // `--dir` is the CLI's own working directory, so it is the **distribution's**
    // spelling and not the host's: a `C:\...` path here is the wrong-spelling
    // bug T-A2's fake refuses by name (T-D3).
    expect([...inside.argv]).toContain('--dir')
    expect(inside.argv[inside.argv.indexOf('--dir') + 1]).toBe('/home/dimi/src/fugaro')
    expect([...inside.argv]).not.toContain('/Users/owner/AppData/Local/Temp/fugaro-console/cwd')

    // With no distribution-side directory -- the state before Part D translates
    // the host's -- the console names none, in either place: no `--cd` and no
    // `--dir`, so the distribution starts the CLI where it starts it. Passing
    // the host's directory instead would be a path the CLI inside cannot resolve.
    const outside = planFor('environment.report', {}, safeContext({
      binaryPath: FUGARO_BINARY_NAME,
      directory: '/Users/owner/AppData/Local/Temp/fugaro-console/cwd',
      wsl: { distro: 'Ubuntu', executable: wsl.binary },
    }))
    expect([...outside.prefix]).toEqual(['-d', 'Ubuntu', '-e'])
    expect([...outside.argv]).not.toContain('--dir')
    expect([...outside.argv]).not.toContain('/Users/owner/AppData/Local/Temp/fugaro-console/cwd')
  })

  it('keeps the launch’s task out of the wrapper’s script, and shell-quotes its title as one word', () => {
    // The launch is the one row whose task reaches no argv word at all (§5.1
    // rule 11), and on the WSL route the row's words become a `sh -c` script,
    // where every word is a shell word. So the task is nowhere in the script --
    // it is written to the distribution's stdin, which is the whole reason the
    // form was chosen -- and the `=`-form title is one word, quoted by the one
    // quoting function there is. A title is the row's one hostile-shaped value:
    // `isSafeInstallationValue` does not hold it, because the IPC validator
    // does, and the shell layer is `shellQuoteWord`.
    const task = 'summarise the backlog; rm -rf /'
    const title = "the owner's backlog"
    const plan = planFor(
      'run.launch',
      { task, repository: 'acme/app', title },
      contextFor('run.launch', {
        binaryPath: FUGARO_BINARY_NAME,
        wsl: { distro: 'Ubuntu', directory: '/home/dimi/src', executable: 'C:\\Windows\\System32\\wsl.exe' },
      }),
    )
    expect(plan.command).toBe('C:\\Windows\\System32\\wsl.exe')
    expect([...plan.prefix]).toEqual(['-d', 'Ubuntu', '--cd', '/home/dimi/src', '-e'])
    expect([...plan.argv]).toStrictEqual([
      'fugaro',
      'run',
      '--task-file',
      '-',
      '--json',
      '--run-id',
      LAUNCH_RUN_ID,
      '--repo',
      'acme/app',
      `--title=${title}`,
      '--project',
      PROJECT,
    ])
    const script = plan.wrapper?.words[2] ?? ''
    expect(script).toContain(`'--title=the owner'\\''s backlog'`)
    expect(script).not.toContain(task)
    expect(script).not.toContain('rm -rf')
  })

  it('hands the CLI a hostile title as exactly one word, for all five shell shapes', async () => {
    // The single quote above is one of five shapes a shell reads as syntax. The
    // title is the launch's one hostile-shaped value -- it is the row's one
    // `=`-form word, and `assertDistroWordsAreSafe` screens only the words a
    // caller supplies as bare values, so `--title=…` reaches the wrapper
    // unscreened -- and on the WSL route every word of the row becomes a word of
    // a `sh -c` script. A double quote ends the quoting, a backslash escapes the
    // closing quote, and a `$( )` or a backtick is a command the operator never
    // asked this console to run.
    //
    // So all five are driven all the way through the wrapper to the CLI, and the
    // claim is the **CLI's own report of its argv**: the title arrived once, as
    // one word, spelled exactly as it was typed, with no word beside it that the
    // shell invented. An assertion on the script's text would only say the
    // console quoted what it wrote.
    const titles = [
      "the owner's backlog",
      'a "quoted" title',
      'a\\backslash',
      'a $(touch pwned) title',
      'a `touch pwned` title',
    ]
    const host = freshDirectory()
    const wsl = installWslStub(host, { distros: TWO_DISTROS })
    // The fake `wsl.exe` resolves the bare name `fugaro` through a relative
    // `distro` entry, so the distribution's directory here is the one the stub
    // was installed into -- and the paths name the test's own operator, never a
    // real one.
    for (const [index, title] of titles.entries()) {
      const plan = planFor(
        'run.launch',
        { task: 'summarise the backlog', repository: 'acme/app', title },
        contextFor('run.launch', {
          binaryPath: FUGARO_BINARY_NAME,
          directory: host,
          wsl: { distro: 'Ubuntu', directory: host, executable: wsl.binary },
        }),
      )
      const result = await runToCompletion(plan, { timeoutMs: 10_000 })
      expect(result.code, title).toBe(0)
      const child = wsl.children()[index]
      expect(child, `${JSON.stringify(title)} started no CLI`).toBeDefined()
      // The stub's own argv, which starts at `run`: the program word is the
      // script's `exec` target and not an argument of the child it becomes.
      expect(child?.argv, title).toStrictEqual([
        'run',
        '--task-file',
        '-',
        '--json',
        '--run-id',
        LAUNCH_RUN_ID,
        '--repo',
        'acme/app',
        `--title=${title}`,
        '--project',
        PROJECT,
      ])
    }
  })

  it('resolves a stored installation inside its distribution, and starts the CLI there', async () => {
    const arrangement = wslRoot()
    const resolution = await resolveInside(arrangement)
    expect(resolution).toEqual({
      ok: true,
      path: FUGARO_BINARY_NAME,
      version: '1.4.2',
      // There is no walk of well-known locations on this route, so the word is
      // the installation's own: it named the distribution (and, when it names
      // one, the path inside it).
      source: 'configured',
    })
    // The fake's own account: one `--list --quiet`, one `--status`, and one
    // `wsl.exe` that started the CLI. The probe is the only thing asked whether
    // the CLI is there -- there is no second call (T-C5).
    const spawn = arrangement.wsl.reports().filter((report) => report.kind === 'spawn')
    expect(spawn).toHaveLength(1)
    expect(spawn[0]?.distro).toBe('Ubuntu')
    expect(spawn[0]?.cd).toBe(arrangement.distroDirectory)
    // `wsl.exe`'s own words, then the shell that carries the CLI (T-F4): the
    // CLI's argv is inside the script, which is what lets the console record the
    // pid it must kill. What the CLI received is the next assertion, from the
    // child's own account rather than from the console's intent.
    expect(spawn[0]?.argv.slice(0, 5)).toEqual(['-d', 'Ubuntu', '--cd', arrangement.distroDirectory, '-e'])
    expect(spawn[0]?.argv.slice(5, 7)).toEqual([WSL_SHELL_PROGRAM, WSL_SHELL_FLAG])
    expect(spawn[0]?.argv).toHaveLength(8)
    expect(spawn[0]?.wrapper).toContain(`exec '${FUGARO_BINARY_NAME}' 'version'`)
    // The host side: `wsl.exe` was started in the directory the console named.
    expect(spawn[0]?.cwd).toBe(arrangement.host)
    // ...and the distribution's side: the CLI's own cwd is the `--cd`.
    const child = only(arrangement.wsl.children())
    expect(child.argv).toEqual(['version'])
    expect(child.cwd).toBe(arrangement.distroDirectory)
  })

  it('states the path the installation stored, and runs the name the distribution resolves', async () => {
    // `binaryPath` under a `distro` is a Linux spelling **inside** the
    // distribution (F15): it is what the console states -- the resolution's
    // `path`, which is the header's line (D3(a)) -- and it is *not* the program
    // word. The distribution resolves the name; the record's directory leads the
    // child's `PATH`, so the name resolves to the file the record names.
    const arrangement = wslRoot()
    const stored = '/home/dimi/.local/bin/fugaro'
    const resolution = await resolveInside(arrangement, { binaryPath: stored })
    expect(resolution.ok && resolution.path).toBe(stored)
    const spawn = only(arrangement.wsl.reports().filter((report) => report.kind === 'spawn'))
    // The program word is the bare name and it is **inside the script** (T-F4):
    // the distribution resolves it, and the record's path is what the console
    // states rather than what it runs.
    expect(spawn.wrapper).toContain(`exec '${FUGARO_BINARY_NAME}' 'version'`)
    expect(spawn.wrapper).not.toContain(stored)
    expect(only(arrangement.wsl.children()).argv).toEqual(['version'])

    // And the plan says the same thing: the name runs, the record's directory is
    // the head of the child's `PATH`, and the distribution's tool list follows.
    const plan = planFor('cli.version', {}, safeContext({
      binaryPath: stored,
      wsl: { distro: 'Ubuntu', directory: '/home/dimi/src', executable: arrangement.wsl.binary },
    }))
    expect(plan.argv[0]).toBe(FUGARO_BINARY_NAME)
    expect(plan.env['PATH']).toBe(childPath(stored, 'linux'))
    expect(plan.env['PATH']?.startsWith('/home/dimi/.local/bin:')).toBe(true)
  })

  it('refuses with `no-wsl` when wsl.exe cannot answer at all', async () => {
    const arrangement = wslRoot(NO_WSL)
    const resolution = await resolveInside(arrangement)
    expect(resolution.ok).toBe(false)
    if (resolution.ok) throw new Error('unreachable')
    expect(resolution.kind).toBe('no-wsl')
    expect(resolution.path).toBeNull()
    // The sentence `wsl.exe` printed is carried: "install WSL" and "install a
    // distribution" are different instructions (T-C4).
    expect(resolution.reason).toContain('Windows Subsystem for Linux has no installed distributions')
    // And no CLI was started anywhere: there is no distribution to start it in.
    expect(arrangement.wsl.childCalls()).toBe(0)
  })

  it('refuses with `no-distro` when the stored distribution has gone, and never falls back to the default', async () => {
    // The mutation this case exists for: a fallback that picks the **default**
    // distribution (here `Debian`) when the stored one is missing. The stored
    // name is a distribution this machine does not have -- a rename or a removal
    // -- and the operator must be told which one, not handed another
    // installation's numbers under the name they chose (F7).
    const arrangement = wslRoot()
    const resolution = await resolveInside(arrangement, { distro: 'Ubuntu-22.04' })
    expect(resolution.ok).toBe(false)
    if (resolution.ok) throw new Error('unreachable')
    expect(resolution.kind).toBe('no-distro')
    expect(resolution.path).toBeNull()
    expect(resolution.reason).toContain('"Ubuntu-22.04"')
    // The list `wsl.exe` gave is in the sentence: the operator is the only one
    // who can say which name is the right one.
    expect(resolution.reason).toContain('"Ubuntu"')
    expect(resolution.reason).toContain('"Debian"')
    // Nothing was started in any distribution.
    expect(arrangement.wsl.childCalls()).toBe(0)
    expect(arrangement.wsl.reports().filter((report) => report.kind === 'spawn')).toHaveLength(0)
  })

  it('hands `wsl.exe` a directory with spaces as one word', async () => {
    // **The case a review asked for**: whitespace in the directory is admitted
    // (`isSafeInstallationValue`; it is a `wsl.exe` argument, not a shell word),
    // so the claim that it is *safe* has to be made where it can be observed --
    // at the fake's own argv. A Linux directory can be called `Ann Lee`, and with
    // an array argv it is one element: nothing splits on the space. The mutation
    // this catches is building the command line as a string, or a guard that
    // splits a value to check it. (A *distribution* name with a space is refused
    // -- Part D's rule; see the next case.)
    const arrangement = wslRoot({
      list: 'Ubuntu\r\nMy Linux\r\n',
      status: 'Default Distribution: Ubuntu\r\nDefault Version: 2\r\n\r\nWSL version: 2.6.1.0\r\n',
    })
    const spaced = join(arrangement.distroDirectory, 'Ann Lee')
    mkdirSync(spaced, { recursive: true })
    const resolution = await resolveInside(arrangement, { distro: 'Ubuntu', wslDirectory: spaced })
    expect(resolution.ok).toBe(true)

    const hop = only(arrangement.wsl.reports().filter((report) => report.kind === 'spawn'))
    expect(hop.argv.filter((word) => word === spaced)).toHaveLength(1)
    expect(hop.distro).toBe('Ubuntu')
    expect(hop.cd).toBe(spaced)
    // ...and the Linux side really was started in it, so this is not only what
    // the console asked for.
    const child = only(arrangement.wsl.children())
    expect(child.argv).toEqual(['version'])
    expect(realpathSync(child.cwd)).toBe(realpathSync(spaced))
  })

  it('refuses, with a sentence, a listed distribution whose name has a space, a quote or a `;`', async () => {
    // Part D's rule (T-D3, `isDistroName`): the WSL route runs the CLI through
    // the distribution's own shell, so a name outside `DISTRO_NAME_PATTERN` is
    // refused (fail closed) even though `wsl.exe --list --quiet` prints it and
    // `wsl --import "My Linux"` registers it. The resolver answers with a
    // `no-distro` sentence rather than letting `planFor`'s guard throw out of
    // an async read -- the mutation this case exists for.
    for (const distro of ['My Linux', "dimi's distro", 'Ubuntu;x']) {
      const arrangement = wslRoot({
        list: `Ubuntu\r\n${distro}\r\n`,
        status: 'Default Distribution: Ubuntu\r\nDefault Version: 2\r\n\r\nWSL version: 2.6.1.0\r\n',
      })
      const resolution = await resolveInside(arrangement, { distro })
      expect(resolution.ok, distro).toBe(false)
      if (resolution.ok) throw new Error('unreachable')
      expect(resolution.kind).toBe('no-distro')
      expect(resolution.path).toBeNull()
      expect(resolution.reason).toContain(JSON.stringify(distro))
      expect(resolution.reason).toContain('is listed by wsl.exe and is not a name this console will run a shell in')
      expect(resolution.reason).toContain('200 characters or fewer of letters, digits, dot, dash and underscore')
      expect(arrangement.wsl.childCalls()).toBe(0)
      expect(arrangement.wsl.reports().filter((report) => report.kind === 'spawn')).toHaveLength(0)
    }
  })

  it('refuses, with a sentence, a listed distribution whose name it will not hand to `wsl.exe`', async () => {
    // T-E3's edge, and the reason the guard is a name check and not a crash: a
    // registered name can begin with `-` -- which is the one shape that is
    // argument confusion, because `wsl.exe` may read it as its own flag -- and
    // `wsl.exe --list --quiet` lists it regardless. The console states that it
    // will not drive it rather than letting the wrapper's own guard throw an
    // exception out of an async read (which is the mutation this case exists
    // for: answer it here, and the case fails with an unhandled error instead of
    // a refusal).
    const arrangement = wslRoot({
      list: 'Ubuntu\r\n-sneaky\r\n',
      status: 'Default Distribution: Ubuntu\r\nDefault Version: 2\r\n\r\nWSL version: 2.6.1.0\r\n',
    })
    const resolution = await resolveInside(arrangement, { distro: '-sneaky' })
    expect(resolution.ok).toBe(false)
    if (resolution.ok) throw new Error('unreachable')
    // `no-distro` and not a fifth kind: this is its second shape, documented on
    // `BinaryRefusalKind` -- there is no distribution this console can drive by
    // that name, and the remedy is the same one the first shape drives.
    expect(resolution.kind).toBe('no-distro')
    expect(resolution.path).toBeNull()
    // The sentence names the name and the rule, so the operator can see what the
    // console will not do rather than only that something failed.
    expect(resolution.reason).toContain('"-sneaky"')
    expect(resolution.reason).toContain('listed by wsl.exe')
    expect(resolution.reason).toContain('begin with "-"')
    expect(arrangement.wsl.childCalls()).toBe(0)
    expect(arrangement.wsl.reports().filter((report) => report.kind === 'spawn')).toHaveLength(0)
  })

  it('refuses with `no-fugaro-in-distro` when the distribution has no CLI in it', async () => {
    // What a shell says when the command is not there: status 127 and a
    // sentence. The classification is by the probe (T-C5), so this is the
    // vector the resolver has to read -- and the instruction it produces is
    // "install the CLI inside the distribution", which is not what the
    // no-binary page says.
    const arrangement = wslRoot(TWO_DISTROS)
    arrangement.wsl.stub.write({
      commands: { version: { code: 127, stderr: 'fugaro: command not found\n' } },
    })
    const resolution = await resolveInside(arrangement)
    expect(resolution.ok).toBe(false)
    if (resolution.ok) throw new Error('unreachable')
    expect(resolution.kind).toBe('no-fugaro-in-distro')
    expect(resolution.reason).toContain('command not found')
    expect(resolution.reason).toContain('"Ubuntu"')
  })

  it('reports a CLI that is there and broken as the probe’s refusal, not as a missing one', async () => {
    // The distinction the four kinds exist for: a `fugaro` that is installed and
    // fails is **not** a `fugaro` that is missing, and a screen that said
    // "install it" would send the operator to install what they already have.
    const broken = wslRoot(TWO_DISTROS)
    broken.wsl.stub.write({ commands: { version: { code: 2, stderr: 'fugaro: the config is unreadable\n' } } })
    const failed = await resolveInside(broken)
    expect(failed.ok).toBe(false)
    if (failed.ok) throw new Error('unreachable')
    expect(failed.kind).toBe('no-binary')
    expect(failed.reason).toContain('the config is unreadable')

    // And the status alone is not the rule: 127 with the CLI's own sentence is
    // still the probe's refusal, because "not found" is a thing a CLI says about
    // a file it was asked to read.
    const ambiguous = wslRoot(TWO_DISTROS)
    ambiguous.wsl.stub.write({ commands: { version: { code: 127, stderr: 'fugaro: no config found\n' } } })
    const other = await resolveInside(ambiguous)
    expect(other.ok).toBe(false)
    if (other.ok) throw new Error('unreachable')
    expect(other.kind).toBe('no-binary')
  })

  it('reads the handshake inside the distribution an installation names', async () => {
    // The startup read is the same route: on Windows there is no host `fugaro`
    // to find, so an installation that names a distribution is read inside it.
    const arrangement = wslRoot()
    const resolution = await resolveHandshakeBinary({
      installation: {
        distro: 'Ubuntu',
        binaryPath: '',
        checkout: arrangement.distroDirectory,
        target: { project: PROJECT },
      },
      wslPath: arrangement.wsl.binary,
      appEnv: {},
      directory: arrangement.host,
    })
    expect(resolution).toEqual({
      ok: true,
      path: FUGARO_BINARY_NAME,
      version: '1.4.2',
      source: 'configured',
    })
    const spawn = only(arrangement.wsl.reports().filter((report) => report.kind === 'spawn'))
    expect(spawn.argv.slice(0, 5)).toEqual(['-d', 'Ubuntu', '--cd', arrangement.distroDirectory, '-e'])
    expect(spawn.argv.slice(5, 7)).toEqual([WSL_SHELL_PROGRAM, WSL_SHELL_FLAG])
    // The handshake's argv is exactly `version` on this route too: no
    // `--project`, because `fugaro version` takes no flags at all -- and it is
    // inside the wrapper's script, which is what the child's own account below
    // confirms (T-F4).
    expect(spawn.wrapper).toContain(`exec '${FUGARO_BINARY_NAME}' 'version'`)
    expect(only(arrangement.wsl.children()).argv).toEqual(['version'])
  })

  it('does not walk the host for an installation whose distribution has gone', async () => {
    // The handshake's ordinary rule is "an installation's binary is an override,
    // not a pin" -- it falls through to rule 4's walk when a stored path has
    // gone. On the WSL route there is nothing to fall through *to*: the walk is
    // a walk of the host, the host has no `fugaro` (T-C2), and answering from
    // another distribution would be another installation's numbers.
    const arrangement = wslRoot()
    const hostStub = installStub(join(arrangement.host, 'bin'), {
      commands: { version: { stdout: '9.9.9\n' } },
    })
    const resolution = await resolveHandshakeBinary({
      installation: {
        distro: 'Ubuntu-22.04',
        binaryPath: '',
        target: { project: PROJECT },
      },
      wslPath: arrangement.wsl.binary,
      appEnv: {},
      directory: arrangement.host,
      prefixes: [dirname(hostStub.binary)],
    })
    expect(resolution.ok).toBe(false)
    if (resolution.ok) throw new Error('unreachable')
    expect(resolution.kind).toBe('no-distro')
    // The host's `fugaro` was never asked: `resolveBinary` was not reached.
    expect(hostStub.calls()).toBe(0)
  })

  // SECURITY-SENSITIVE: the credential boundary, proved through `wsl.exe` rather
  // than around it (T-E1; F18, F19, F21, F22; decision D5(a)).
  //
  // **The claim, in one sentence:** nothing the console's own environment holds
  // reaches the process inside the distribution except the eleven
  // `CHILD_ENV_KEYS`, and the only keys beside them on the way to `wsl.exe` are
  // the two words this console writes itself -- the empty `WSLENV` the route
  // states (T-E2) and `WSL_UTF8=1` (T-F2). Neither is read from anywhere: they
  // are constants, and `WSLENV` empty is what keeps them on the near side.
  //
  // **Why it is asserted here and not read off the plan.** A test that inspected
  // `plan.env` would be asserting that the console is self-consistent. The fake
  // reports what each hop *received*: the `wsl.exe` hop's own environment (what
  // the console handed it) and the `fugaro` hop's (what the Linux side got), and
  // those are the two accounts an operator's machine could be wrong about.
  //
  // **The mutations this case catches**, each one a plausible edit:
  //   1. `...process.env` merged into `childEnvironment` -- the sentinels are in
  //      this *test process's* environment, so they appear in the `wsl.exe`
  //      hop's own report and the case fails by naming them;
  //   2. the same merge done from `appEnv` -- the sentinels are in the `appEnv`
  //      handed over too, so that half is covered by the same assertions;
  //   3. the allow-list widened (a key added to `CHILD_ENV_KEYS`) -- the `wsl.exe`
  //      hop's key set is pinned exactly;
  //   4. `WSLENV` forwarded from the host rather than written empty -- the
  //      near-side assertion on its value fails -- or a word of the operator's
  //      added beside it, which the pinned key set catches;
  //   5. the double forwarding its own environment to the child -- the child
  //      hop's key set is pinned exactly, and `test/unit/wslstub.test.ts` holds
  //      that half of the double's contract on its own.
  it('hands the distribution exactly CHILD_ENV_KEYS, with this process’s credentials planted', async () => {
    // The planted credentials are *names*, and the values are inert strings that
    // match no credential shape (the rule `test/unit/secret-hygiene.test.ts`
    // enforces over this tree). They stand in for everything the console's
    // environment holds and the distribution must not see.
    const PLANTED: Readonly<Record<string, string>> = {
      AWS_SECRET_ACCESS_KEY: 'planted-not-a-real-key',
      GITHUB_TOKEN: 'planted-not-a-real-token',
      GOOGLE_APPLICATION_CREDENTIALS: 'C:\\Users\\dimi\\.config\\gcloud\\planted.json',
    }
    for (const [name, value] of Object.entries(PLANTED)) vi.stubEnv(name, value)
    vi.stubEnv('PATH', WINDOWS_PATH)

    // The environment the app hands the console: this process's own (the
    // sentinels included) plus the eleven keys spelled as a Windows host would
    // spell them. All eleven are present, so "exactly the eleven" is a literal
    // claim here and not a claim about whichever keys this machine happens to
    // have.
    const appEnv: Readonly<Record<string, string | undefined>> = {
      ...process.env,
      ...PLANTED,
      PATH: WINDOWS_PATH,
      HOME: 'C:\\Users\\dimi',
      TMPDIR: 'C:\\Users\\dimi\\AppData\\Local\\Temp',
      LANG: 'en_US.UTF-8',
      LC_ALL: 'en_US.UTF-8',
      XDG_CONFIG_HOME: 'C:\\Users\\dimi\\.config',
      XDG_CACHE_HOME: 'C:\\Users\\dimi\\.cache',
      XDG_DATA_HOME: 'C:\\Users\\dimi\\.local\\share',
      XDG_STATE_HOME: 'C:\\Users\\dimi\\.local\\state',
      XDG_RUNTIME_DIR: 'C:\\Users\\dimi\\run',
    }

    const arrangement = wslRoot()
    const resolution = await resolveInstalledBinary({
      binaryPath: '',
      distro: 'Ubuntu',
      wslDirectory: arrangement.distroDirectory,
      wslPath: arrangement.wsl.binary,
      appEnv,
      directory: arrangement.host,
      target: { project: PROJECT },
    })
    expect(resolution.ok).toBe(true)

    // macOS adds __CF_USER_TEXT_ENCODING to every process it starts (as
    // elsewhere in this file): the platform's own key, named rather than waved
    // through so any other addition still fails.
    const platformKeys = process.platform === 'darwin' ? ['__CF_USER_TEXT_ENCODING'] : []

    // The near side: what `wsl.exe` was handed. The eleven, plus the two keys
    // this route writes itself -- `WSLENV` empty (T-E2) and `WSL_UTF8=1` (T-F2,
    // and on the CLI's own call since the review of `#981`) -- and nothing else,
    // sentinels included.
    const hop = only(arrangement.wsl.reports().filter((report) => report.kind === 'spawn'))
    expect(hop.envKeys.filter((key) => !platformKeys.includes(key)).sort()).toEqual(
      [...CHILD_ENV_KEYS, WSLENV_KEY, WSL_UTF8_VAR].sort(),
    )
    expect(hop.env[WSLENV_KEY]).toBe('')
    expect(hop.env[WSL_UTF8_VAR]).toBe(WSL_UTF8_VALUE)
    // The `PATH` the console hands `wsl.exe` is the **child's** -- Linux's --
    // even though the process that receives it is a Windows one, because it is
    // the environment the distribution's process is built from (F5, T-A2). The
    // Windows-shaped `PATH` this test put in the app's own environment is
    // nowhere in it.
    expect(hop.path).toBe(childPath(FUGARO_BINARY_NAME, 'win32'))
    expect(hop.path).not.toContain('C:\\')
    for (const [name, value] of Object.entries(PLANTED)) {
      expect(hop.envKeys, name).not.toContain(name)
      expect(hop.env[name], name).toBeUndefined()
      // ...and the value is nowhere in the environment either, which is what a
      // key-by-key check would miss if the name were respelled.
      expect(Object.values(hop.env), name).not.toContain(value)
    }
    // The share list is `wsl.exe`'s: it is not one of the variables the child
    // inside the distribution receives, so the double does not hand it over.
    expect(Object.keys(hop.childEnv)).not.toContain(WSLENV_KEY)

    // The far side: what the Linux process got. Exactly the eleven -- the
    // distribution's own `PATH`, the allow-list's values, and nothing else.
    //
    // Part F carries the CLI in `sh -c` (T-F4), and a POSIX shell sets `PWD`
    // and `SHLVL` in the environment of what it `exec`s. Those two are the
    // shell's own bookkeeping, not anything the console or the host handed
    // over, so they are named here -- exactly two, and with their values held to
    // what a shell writes -- rather than waved through.
    const child = only(arrangement.wsl.children())
    const shellKeys = ['PWD', 'SHLVL']
    const childKeys = child.envKeys.filter((key) => !platformKeys.includes(key) && !shellKeys.includes(key))
    expect(childKeys.sort()).toEqual([...CHILD_ENV_KEYS].sort())
    expect(child.env['PWD'] ?? '/').toMatch(/^\//)
    expect(child.env['SHLVL'] ?? '1').toMatch(/^[0-9]+$/)
    // The distribution's own `PATH`, with the double's stand-in for the
    // directory the CLI is installed in at its head (the wrapper's `exec fugaro`
    // resolves through it, `WRAPPER_PATH` in the double) -- and nothing of the
    // host's.
    expect(child.env['PATH']).toBe(`${arrangement.wsl.stub.directory}:${DISTRO_PATH}`)
    expect(child.env['PATH']).not.toContain('C:\\')
    expect(child.env[WSLENV_KEY]).toBeUndefined()
    // The filter is a filter and not an empty environment: the allow-listed
    // values did arrive, spelled as the Windows host spells them.
    expect(child.env['HOME']).toBe('C:\\Users\\dimi')
    expect(child.env['FUGARO_PROJECT']).toBe(PROJECT)
    for (const [name, value] of Object.entries(PLANTED)) {
      expect(child.envKeys, name).not.toContain(name)
      expect(child.env[name], name).toBeUndefined()
      expect(Object.values(child.env), name).not.toContain(value)
    }
  })
})

describe('the spelling each side gets (Part D, T-D3, D3(a))', () => {
  // D3(a): **one thing, one spelling, said out loud.** A path the console
  // *states* or *passes into the distribution* is the distribution's Linux
  // spelling; the UNC spelling (`wslUncPath`) exists only where Windows opens a
  // path itself. T-A2's fake refuses a Windows spelling in `--cd` by name, and
  // the refusals below happen before any process exists -- so the failure names
  // the rule rather than arriving as an `ENOENT` from inside a distribution.
  const UBUNTU: WslRoute = { distro: 'Ubuntu', directory: '/home/dimi/src' }

  it('refuses a project name the distribution’s shell would read as syntax', () => {
    // The carry-over from Part C's review, and the reason it binds this part:
    // the CLI runs through the distribution's own shell, so every word after
    // `--` that is not a fixed constant is shell syntax until it is proved
    // otherwise. A project named `$(touch x)` must never reach a shell.
    const hostile = ['$(touch x)', '`touch x`', 'a;rm -rf /', 'a|b', 'a&b', 'a>b', "a'b"]
    for (const project of hostile) {
      expect(() => planFor('budget.show', {}, safeContext({ project, wsl: UBUNTU })), project).toThrow(
        /shell/,
      )
    }
    // A newline never gets as far as the shell check: it is refused as a name
    // before the argv is built. The rule is "refused", and this is the other
    // reader of it -- what matters is that it is not passed on.
    expect(() => planFor('budget.show', {}, safeContext({ project: 'a\nb', wsl: UBUNTU }))).toThrow(
      /not a value this console will pass/,
    )
    // ...and the same word is refused in every other value too, by whichever
    // reader sees it first: an argument's own value check, or this one. What
    // matters is that it is refused, and that both sentences exist.
    expect(() =>
      planFor('budget.show', { repo: 'acme/$(touch x)' }, safeContext({ wsl: UBUNTU })),
    ).toThrow(/not a value this console will pass|shell/)
  })

  it('builds the plan for a safe word, so the refusal is a filter and not a wall', () => {
    // The other direction of the red step: a project name inside the alphabet
    // still plans, and its Linux directory is what the wrapper names.
    const plan = planFor('budget.show', {}, safeContext({ project: 'acme-widgets', wsl: UBUNTU }))
    expect([...plan.argv]).toEqual([FUGARO_BINARY_NAME, 'budget', 'show', '--json', '--project', 'acme-widgets'])
    expect([...plan.prefix]).toEqual(['-d', 'Ubuntu', '--cd', '/home/dimi/src', '-e'])
  })

  it('refuses a Windows spelling rather than the directory the distribution has', () => {
    // D3(a) from the other side: a `C:\...` spelling in the wrapper's `--cd` is
    // a directory that does not exist inside the distribution. The console
    // refuses it rather than translating it here -- the translation is
    // `wslpath`'s (T-D2), and a path this function composed would be right for
    // `C:` and wrong for every custom mount (D4(b)).
    expect(() =>
      planFor('budget.show', {}, safeContext({ wsl: { distro: 'Ubuntu', directory: 'C:\\Users\\dimi\\src' } })),
    ).toThrow(/Windows spelling/)
    // ...and the config path is the same rule: it is passed *into* the
    // distribution, so it is the distribution's spelling.
    expect(() =>
      planFor(
        'budget.show',
        {},
        safeContext({
          configPath: 'C:\\Users\\dimi\\.config\\fugaro\\config.yaml',
          wsl: UBUNTU,
        }),
      ),
    ).toThrow(/Windows spelling/)
    // With the distribution's spelling both are passed through verbatim.
    const plan = planFor(
      'budget.show',
      {},
      safeContext({ configPath: '/home/dimi/.config/fugaro/config.yaml', wsl: UBUNTU }),
    )
    expect([...plan.argv]).toContain('/home/dimi/.config/fugaro/config.yaml')
  })

  it('refuses a distribution name that is not a name, before anything runs', () => {
    // The name decides **where the shell runs**, and it arrives from a document
    // (D2(a)); `planFor` is the last place it is still a string, and this is
    // what "planFor checks the distro itself" means for a function that starts
    // no process.
    for (const distro of ['Ubuntu; touch /tmp/x', 'Ubuntu\n', 'a b', '-Ubuntu', '', 'Ubuntu$(id)']) {
      expect(() => planFor('budget.show', {}, safeContext({ wsl: { distro, directory: '/home/dimi/src' } })), distro).toThrow(
        /WSL distribution name|shell/,
      )
    }
    // A name inside the alphabet plans: `Ubuntu-22.04` and `Ubuntu_2` are real
    // distribution names, and a check that refused them would refuse the machine.
    for (const distro of ['Ubuntu', 'Ubuntu-22.04', 'Ubuntu_2', 'kali.rolling']) {
      expect(() =>
        planFor('budget.show', {}, safeContext({ wsl: { distro, directory: '/home/dimi/src' } })),
      ).not.toThrow()
    }
  })

  it('is the only spelling Windows needs, and it is only for Windows opening a path', () => {
    // `wslUncPath` is the one place a Windows spelling is *produced*, and it
    // exists for the paths Windows itself opens -- never for a word the console
    // states or passes into the distribution.
    expect(wslUncPath('Ubuntu', '/home/dimi/src')).toBe('\\\\wsl.localhost\\Ubuntu\\home\\dimi\\src')
    expect(wslUncPath('Ubuntu-22.04', '/home/dimi/.local/share/fugaro')).toBe(
      '\\\\wsl.localhost\\Ubuntu-22.04\\home\\dimi\\.local\\share\\fugaro',
    )
    expect(wslUncPath('Ubuntu', '/')).toBe('\\\\wsl.localhost\\Ubuntu\\')
  })

  it('quotes a word for a POSIX shell, and refuses the one it cannot quote', () => {
    // `shellQuoteWord` is Part F's, exported now so that the `sh -c` wrapper
    // quotes rather than concatenates. Until then the WSL route **refuses** a
    // dangerous word (above), which is the safe half of the same rule.
    expect(shellQuoteWord('fugaro')).toBe("'fugaro'")
    expect(shellQuoteWord('a b')).toBe("'a b'")
    expect(shellQuoteWord("a'b")).toBe("'a'\\''b'")
    expect(shellQuoteWord('$(touch x)')).toBe("'$(touch x)'")
    // A NUL cannot be passed to a process at all, so there is nothing to quote:
    // a quote that dropped it would hand the child a different word.
    expect(() => shellQuoteWord('a\u0000b')).toThrow()
  })
})

describe('the reader', () => {
  it.each([
    ['{"kind":"live","day":"2026-01-01"}', 'document'],
    ['  {"kind":"live"}  ', 'document'],
    ['{"kind":"live"}\r', 'document'],
    ['no budget backend: showing run records only', 'notice'],
    ['[]', 'notice'],
    ['"a string"', 'notice'],
    ['42', 'notice'],
    ['null', 'notice'],
    ['{"kind":"live"', 'notice'],
    ['', 'empty'],
    ['   ', 'empty'],
  ])('decodes %j as %s', (line, kind) => {
    expect(decodeFeedLine(line).kind).toBe(kind)
  })

  it('skips a degraded-mode sentence rather than failing the read', async () => {
    const plan = planFor('live.watch', {}, safeContext())
    const child = scriptedChild()
    const frames: string[] = []
    const notices: string[] = []
    startSidecar(plan, {
      onFrame: (frame) => frames.push(JSON.stringify(frame.document)),
      onNotice: (notice) => notices.push(notice.text),
      onRestart: () => undefined,
      onEnded: () => undefined,
    }, { spawner: () => child, timers: manualTimers() })

    child.emitStdout('no budget backend: showing run records only\n')
    child.emitStdout('{"schema":1,"day":"2026-01-01"}\n')
    expect(notices).toEqual(['no budget backend: showing run records only'])
    expect(frames).toEqual(['{"schema":1,"day":"2026-01-01"}'])
  })

  it('joins a document split across two reads into one', () => {
    const plan = planFor('live.watch', {}, safeContext())
    const child = scriptedChild()
    const frames: unknown[] = []
    startSidecar(plan, {
      onFrame: (frame) => frames.push(frame.document),
      onNotice: () => undefined,
      onRestart: () => undefined,
      onEnded: () => undefined,
    }, { spawner: () => child, timers: manualTimers() })
    child.emitStdout('{"schema":1,')
    expect(frames).toHaveLength(0)
    child.emitStdout('"day":"2026-01-01"}\n')
    expect(frames).toHaveLength(1)
  })

  it('stamps each document with the console’s clock and counts them', () => {
    const plan = planFor('live.watch', {}, safeContext())
    const child = scriptedChild()
    const frames: { sequence: number; receivedAt: string; day: string | null; rolled: boolean }[] = []
    startSidecar(plan, {
      onFrame: (frame) => frames.push(frame),
      onNotice: () => undefined,
      onRestart: () => undefined,
      onEnded: () => undefined,
    }, {
      spawner: () => child,
      timers: manualTimers(),
      clock: () => new Date('2026-01-02T00:00:01Z'),
    })
    child.emitStdout('{"day":"2026-01-02"}\n{"day":"2026-01-02"}\n')
    expect(frames.map((frame) => frame.sequence)).toEqual([1, 2])
    expect(frames[0]?.receivedAt).toBe('2026-01-02T00:00:01Z')
  })

  it('marks the UTC-midnight rollover as a gap, not as a zero', () => {
    const plan = planFor('live.watch', {}, safeContext())
    const child = scriptedChild()
    const frames: { day: string | null; rolled: boolean }[] = []
    startSidecar(plan, {
      onFrame: (frame) => frames.push(frame),
      onNotice: () => undefined,
      onRestart: () => undefined,
      onEnded: () => undefined,
    }, { spawner: () => child, timers: manualTimers() })
    child.emitStdout('{"day":"2026-01-01"}\n')
    child.emitStdout('{"day":"2026-01-02"}\n')
    expect(frames.map((frame) => ({ day: frame.day, rolled: frame.rolled }))).toEqual([
      { day: '2026-01-01', rolled: false },
      { day: '2026-01-02', rolled: true },
    ])
  })
})

describe('the supervisor', () => {
  it('restarts a child that dies, with the CLI’s own backoff, and tells the UI', () => {
    const plan = planFor('live.watch', {}, safeContext())
    const timers = manualTimers()
    const children = [scriptedChild(), scriptedChild()]
    let index = 0
    const frames: unknown[] = []
    const restarts: { attempt: number; delayMs: number }[] = []
    startSidecar(plan, {
      onFrame: (frame) => frames.push(frame.document),
      onNotice: () => undefined,
      onRestart: (restart) => restarts.push({ attempt: restart.attempt, delayMs: restart.delayMs }),
      onEnded: () => undefined,
    }, {
      spawner: () => {
        const child = children[index]
        index += 1
        if (child === undefined) throw new Error('the supervisor spawned more children than the test scripted')
        return child
      },
      timers,
    })

    children[0]?.emitStdout('{"day":"2026-01-01"}\n')
    children[0]?.exit(0, null)
    expect(restarts).toEqual([{ attempt: 1, delayMs: DEFAULT_BACKOFF.minMs }])
    expect(timers.pending()).toBe(1)

    timers.fire()
    children[1]?.emitStdout('{"day":"2026-01-01"}\n')
    expect(frames).toHaveLength(2)
  })

  it('doubles the backoff per attempt and stops at the maximum', () => {
    const backoff = { minMs: 1000, maxMs: 30_000 }
    const delay = (attempt: number): number => {
      const decision = restartDecision({ code: 0, signal: null, stopped: false, attempt, backoff })
      return decision.restart ? decision.delayMs : -1
    }
    expect([0, 1, 2, 3, 4, 5, 6, 7, 8].map(delay)).toEqual([
      1000, 2000, 4000, 8000, 16_000, 30_000, 30_000, 30_000, 30_000,
    ])
  })

  it('reads a child to its close, so a last frame is not dropped and a last sentence is not missed', () => {
    // The streaming half of the same defect: `watch`'s final frame can arrive
    // after the child is reaped, and a supervisor that decided on `exit` would
    // both drop that frame and read an empty stderr into the sentence it gives
    // the screen. With the supervisor settling on `exit`, `ends` is non-empty
    // before the `close` below and `frames` has one entry rather than two.
    const plan = planFor('live.watch', {}, safeContext())
    const child = scriptedChild()
    const frames: string[] = []
    const ends: { reason: string; sentence: string | null }[] = []
    startSidecar(plan, {
      onFrame: (frame) => frames.push(JSON.stringify(frame.document)),
      onNotice: () => undefined,
      onRestart: () => undefined,
      onEnded: (end) => ends.push({ reason: end.reason, sentence: end.sentence }),
    }, { spawner: () => child, timers: manualTimers() })

    child.emitStdout('{"day":"2026-01-01"}\n')
    child.exitWithoutClose(1, null)
    expect(ends).toHaveLength(0)
    child.emitStdout('{"day":"2026-01-02"}\n')
    child.emitStderr('fugaro: this checkout belongs to project acme; --project says fugaro\n')
    child.close(1, null)

    expect(frames).toEqual(['{"day":"2026-01-01"}', '{"day":"2026-01-02"}'])
    expect(ends).toEqual([
      { reason: 'refused', sentence: 'this checkout belongs to project acme; --project says fugaro' },
    ])
  })

  it('does not restart after a refusal, and keeps the CLI’s sentence', () => {
    const plan = planFor('live.watch', {}, safeContext())
    const child = scriptedChild()
    const restarts: unknown[] = []
    const ends: { reason: string; sentence: string | null; spawns: number }[] = []
    startSidecar(plan, {
      onFrame: () => undefined,
      onNotice: () => undefined,
      onRestart: (restart) => restarts.push(restart),
      onEnded: (end) => ends.push({ reason: end.reason, sentence: end.sentence, spawns: end.spawns }),
    }, { spawner: () => child, timers: manualTimers() })

    child.emitStderr('fugaro: this checkout belongs to project acme; --project says fugaro\n')
    child.exit(1, null)
    expect(restarts).toHaveLength(0)
    expect(ends).toEqual([
      { reason: 'refused', sentence: 'this checkout belongs to project acme; --project says fugaro', spawns: 1 },
    ])
  })

  it('treats a signal as the ordinary end of a feed: it restarts', () => {
    // Task 0.2 section 10.4: a closed stdout is a SIGPIPE on the next write, and
    // os.ProcessState reports a signal and not an exit code. Reading that as a
    // CLI error is the defect this test exists for.
    const decision = restartDecision({ code: null, signal: 'SIGPIPE', stopped: false, attempt: 0, backoff: DEFAULT_BACKOFF })
    expect(decision).toEqual({ restart: true, attempt: 1, delayMs: 1000 })
    expect(restartDecision({ code: null, signal: 'SIGTERM', stopped: false, attempt: 2, backoff: DEFAULT_BACKOFF })).toEqual({
      restart: true,
      attempt: 3,
      delayMs: 4000,
    })
  })

  it('stops restarting once the console has stopped it', () => {
    expect(restartDecision({ code: 0, signal: null, stopped: true, attempt: 0, backoff: DEFAULT_BACKOFF })).toEqual({
      restart: false,
      reason: 'stopped',
    })
  })

  it('answers a second stop with the same promise, and settles both', async () => {
    // `LiveFeed.stop()` and `CliSource.close()` both reach this, and a caller
    // that stops a feed and then closes the source without awaiting the first
    // call must not be left holding a promise nothing will ever settle. The
    // child is still alive when the second call arrives -- the *pending* case,
    // which is the one a cached resolver loses.
    const plan = planFor('live.watch', {}, safeContext())
    const child = scriptedChild()
    const timers = manualTimers()
    const sidecar = startSidecar(
      plan,
      { onFrame: () => undefined, onNotice: () => undefined, onRestart: () => undefined, onEnded: () => undefined },
      { spawner: () => child, timers },
    )

    const first = sidecar.stop()
    const second = sidecar.stop()
    expect(second).toBe(first)
    // One SIGTERM and one grace timer, not two: the second call joins the stop
    // that is already happening rather than starting a second one.
    expect(child.killed).toEqual(['SIGTERM'])
    expect(timers.pending()).toBe(1)

    child.exit(null, 'SIGTERM')
    const end = await first
    expect(end).toEqual({ reason: 'stopped', code: null, signal: 'SIGTERM', sentence: null, spawns: 1 })
    await expect(second).resolves.toBe(end)
  })

  it('settles `stop()` when the child exits but its stdout is never closed', async () => {
    // A grandchild (or a shim) that inherited the pipe keeps stdout open after
    // the child is reaped, so `close` never comes. Without a backstop after
    // `exit`, `stop()` -- and with it `CliSource.close()` and app quit -- hung.
    const plan = planFor('live.watch', {}, safeContext())
    const child = scriptedChild()
    const timers = manualTimers()
    const ended: string[] = []
    const sidecar = startSidecar(
      plan,
      { onFrame: () => undefined, onNotice: () => undefined, onRestart: () => undefined, onEnded: (end) => ended.push(end.reason) },
      { spawner: () => child, timers },
    )
    const stopping = sidecar.stop()
    child.exitWithoutClose(null, 'SIGTERM')
    timers.fire()
    const end = await Promise.race([stopping, new Promise<'hung'>((resolve) => setTimeout(() => resolve('hung'), 200))])
    expect(end).toEqual({ reason: 'stopped', code: null, signal: 'SIGTERM', sentence: null, spawns: 1 })
    expect(ended).toEqual(['stopped'])
    expect(child.destroyed).toEqual(['stdout', 'stderr'])
    // A late `close` once the grandchild lets go changes nothing.
    child.close(null, 'SIGTERM')
    expect(ended).toEqual(['stopped'])
  })

  it('decides on `exit` after the grace when `close` never comes, without stopping', () => {
    const plan = planFor('live.watch', {}, safeContext())
    const child = scriptedChild()
    const timers = manualTimers()
    const ended: string[] = []
    startSidecar(
      plan,
      { onFrame: () => undefined, onNotice: () => undefined, onRestart: () => undefined, onEnded: (end) => ended.push(end.reason) },
      { spawner: () => child, timers },
    )
    child.emitStderr('Error: refused\n')
    child.exitWithoutClose(1, null)
    expect(ended).toEqual([])
    timers.fire()
    expect(ended).toEqual(['refused'])
  })

  it('stops a binary that never answers `version` instead of abandoning it', async () => {
    // The one stop path in this module that used to walk away: `withTimeout`
    // resolved its race and left the child running, reparented to init when the
    // app quits, with its stdout still draining into a promise nobody awaits.
    const stub = installStub(freshDirectory(), { commands: { version: { hold: true } } })
    // A directory that exists: `safeContext`'s default cwd is a checkout path
    // that is only real on the machine the console ships to.
    const plan = planFor('cli.version', {}, safeContext({ binaryPath: stub.binary, directory: freshDirectory() }))
    // The deadline is a manual timer fired once the stub has reported -- after
    // its SIGTERM handler is installed -- so the test does not depend on how
    // long Node takes to start on this machine (a 50 ms deadline is shorter
    // than that on a laptop, and the child was killed before it could report).
    const timers = manualTimers()
    const pending = probeVersion(plan, { timers, shutdownGraceMs: 50 })
    await waitFor(() => stub.calls() === 1)
    timers.fire()
    const probe = await pending
    // The probe's whole answer: the status and the child's own words are `null`
    // here, because a deadline is the console's own reason and there was no
    // exit to read (T-C5's `said` is what a shell's miss is classified by).
    expect(probe).toEqual({
      ok: false,
      code: null,
      said: null,
      // The sentence is the read's own, spelled where every other timeout in
      // this module is spelled (`runToCompletion`'s deadline), so a probe and a
      // read say the same thing about the same deadline.
      reason: `fugaro version did not finish within ${String(VERSION_TIMEOUT_MS / 1000)}s`,
      // The host route: there is no wrapper, so there is no
      // "did a child inside the distribution start" question to answer (T-F1).
      wrapperStarted: null,
    })
    await waitFor(() => stub.kills().length === 1)
    expect(stub.kills()[0]?.signal).toBe('SIGTERM')
    expect(stub.kills()[0]?.argv).toContain('version')
  })

  it('carries the WSL residual and the wrapper fact through a probe timeout', async () => {
    // The installations list is exactly where a cold-starting distribution is
    // probed: `VERSION_TIMEOUT_MS` is five seconds and a first `wsl.exe` call
    // can take longer than that (U15). When it does, the probe must not read as
    // an ordinary host timeout -- a `fugaro` may have been left running inside
    // the distribution, and the sentence that says so is the one the read
    // computed when it refused. A probe that rebuilt its own sentence here
    // ("the binary did not answer `version` in time", with `wrapperStarted:
    // null`) threw both facts away, and this is the one caller-facing path
    // where they exist.
    const directory = freshDirectory()
    const plan = planFor('cli.version', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', executable: '/usr/bin/wsl.exe' },
    })
    const child = scriptedChild()
    const timers = manualTimers()
    const pending = probeVersion(plan, { spawner: () => child, timers, shutdownGraceMs: 50 })
    timers.fire()
    // Nothing was ever printed: the distribution was still starting, so the
    // pid never came. The refusal waits for the grace and then states the
    // window.
    timers.fire()
    const probe = await pending
    expect(probe.ok).toBe(false)
    if (probe.ok) return
    expect(probe.reason).toContain('did not finish within')
    expect(probe.reason).toContain('may still be running')
    // ...and the WSL-route timeout is not a host one: no marker was seen, so no
    // child inside the distribution ever started (T-F1, F26).
    expect(probe.wrapperStarted).toBe(false)
    expect(probe.code).toBeNull()
    expect(probe.said).toBeNull()

    // With the wrapper's line on stderr the same probe is the same refusal
    // without the window: the console named the process, and the stop went out.
    const reported = scriptedChild()
    const calls: string[][] = []
    const secondTimers = manualTimers()
    const second = probeVersion(plan, {
      spawner: ((command: string, args: readonly string[]) => {
        calls.push([command, ...args])
        return reported
      }) as ChildSpawner,
      timers: secondTimers,
      shutdownGraceMs: 50,
    })
    reported.emitStderr(`${wrapperMarkerOf(plan)} 4321\n`)
    secondTimers.fire()
    secondTimers.fire()
    const secondProbe = await second
    expect(secondProbe.ok).toBe(false)
    if (secondProbe.ok) return
    expect(secondProbe.reason).not.toContain('may still be running')
    expect(secondProbe.wrapperStarted).toBe(true)
    expect(calls[1]).toEqual(['/usr/bin/wsl.exe', '-d', 'Ubuntu', '-e', 'sh', '-c', 'kill -TERM 4321'])
  })

  it('stops a long-lived child with SIGTERM and reports the stop', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, watch: { lines: ['{"day":"2026-01-01"}'], hold: true } },
    })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    const feed = source.subscribeLive({})
    const first = await feed.current()
    expect(first.day).toBe('2026-01-01')
    let ended: { reason: string } | null = null
    feed.onEnded((end) => {
      ended = { reason: end.reason }
    })
    await feed.stop()
    await waitFor(() => ended !== null)
    expect(ended).toEqual({ reason: 'stopped' })
    await source.close()
  })

  it('tells a screen why the feed ended when the CLI refuses', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: {
        version: { stdout: '1.4.2\n' },
        watch: { stderr: 'fugaro: no project selected\n', code: 1 },
      },
    })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    const feed = source.subscribeLive({})
    await expect(feed.current()).rejects.toThrow(/no project selected/)
    await source.close()
  })

  it('answers current() with the frame it already has, without spawning again', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, watch: { lines: ['{"day":"2026-01-01"}'], hold: true } },
    })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    const feed = source.subscribeLive({})
    await feed.current()
    const spawnsBefore = stub.reports().filter((report) => report.argv[0] === 'watch').length
    await feed.current()
    expect(stub.reports().filter((report) => report.argv[0] === 'watch')).toHaveLength(spawnsBefore)
    await source.close()
  })

  it('surfaces a degraded-mode sentence next to the document it accompanies', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: {
        version: { stdout: '1.4.2\n' },
        watch: {
          lines: ['no budget backend: showing run records only', '{"day":"2026-01-01"}'],
          hold: true,
        },
      },
    })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    const feed = source.subscribeLive({})
    const snapshot = await feed.current()
    expect(snapshot.notices.map((notice) => notice.text)).toEqual(['no budget backend: showing run records only'])
    await source.close()
  })
})

/**
 * The live subscription's idempotency. It is load-bearing rather than an
 * optimisation: Task 1.2's surface is `invoke`-only, so a screen reaches
 * `live.watch` once per poll, and an implementation that built a stream per
 * call would leave one `fugaro watch` child -- against the operator's own
 * project -- per poll, none of them stopped until `close()`.
 *
 * Every count of children here is read from the stub's *own* report file, and
 * every one is read through `waitFor`: the child writes its line from its own
 * process, so a count read synchronously after a `subscribeLive` is a count of
 * the spawns that had landed, not of the spawns that happened.
 */
describe('the one-shot read', () => {
  const plan = (): SpawnPlan => planFor('budget.show', {}, safeContext())
  const scriptedSpawner = (child: ReturnType<typeof scriptedChild>): ChildSpawner => () => child

  it('stops a read that never finishes, and says so, instead of waiting forever', async () => {
    // Only the version handshake had a deadline; a `budget show` whose child
    // hung would leave a screen waiting and a process running until quit.
    const child = scriptedChild()
    const timers = manualTimers()
    const read = runToCompletion(plan(), { spawner: scriptedSpawner(child), timers, timeoutMs: 30_000, shutdownGraceMs: 2000 })
    expect(timers.pending()).toBe(1)
    timers.fire()
    await expect(read).rejects.toThrow(/did not finish within 30s/)
    expect(child.killed).toEqual(['SIGTERM'])
    // ...and a child that ignores SIGTERM gets SIGKILL after the grace period.
    timers.fire()
    expect(child.killed).toEqual(['SIGTERM', 'SIGKILL'])
  })

  it('gives every one-shot read a deadline by default', async () => {
    const child = scriptedChild()
    const timers = manualTimers()
    const read = runToCompletion(plan(), { spawner: scriptedSpawner(child), timers })
    expect(timers.pending()).toBe(1)
    child.exit(0, null)
    await expect(read).resolves.toMatchObject({ code: 0 })
    // The deadline is cleared once the child has exited: nothing fires later.
    expect(timers.pending()).toBe(0)
  })

  it('stops a read whose stdout outgrows the cap, rather than buffering it all', async () => {
    const child = scriptedChild()
    const timers = manualTimers()
    const read = runToCompletion(plan(), { spawner: scriptedSpawner(child), timers, maxStdoutLength: 16 })
    child.emitStdout('{"a":"0123456789')
    child.emitStdout('0123456789"}\n')
    await expect(read).rejects.toThrow(/more than 16 characters/)
    expect(child.killed).toEqual(['SIGTERM'])
  })

  it('bounds stderr as well', async () => {
    const child = scriptedChild()
    const read = runToCompletion(plan(), { spawner: scriptedSpawner(child), timers: manualTimers(), maxStderrBytes: 8 })
    child.emitStderr('x'.repeat(100))
    child.exit(1, null)
    expect((await read).stderr.length).toBeLessThanOrEqual(8)
  })

  it('settles on close, so a chunk that arrives after exit is in the value', async () => {
    // Node fires `exit` when the child is reaped, and its stdio may still have
    // bytes on the way at that moment: a reader that settled on `exit` would
    // hand back `{"a":` -- a torn document that the screen then refuses, or a
    // version line with its last character missing, which the handshake would
    // read as a skew it is not. This is the mutation the review found untested:
    // with `runToCompletion` settling on `exit`, the value below is `{"a":`.
    const child = scriptedChild()
    const read = runToCompletion(plan(), { spawner: scriptedSpawner(child), timers: manualTimers() })
    child.emitStdout('{"a":')
    child.exitWithoutClose(0, null)
    child.emitStdout('1}')
    child.close(0, null)
    await expect(read).resolves.toMatchObject({ code: 0, stdout: '{"a":1}' })
  })

  it('refuses a read whose streams never close, rather than returning a torn value', async () => {
    // The other side of settling on `close`: a pipe something else still holds
    // open past the child's exit is not a value to hand a screen. The deadline
    // is what answers it, and it answers by refusing -- never by resolving with
    // the bytes that had arrived.
    const child = scriptedChild()
    const timers = manualTimers()
    const read = runToCompletion(plan(), { spawner: scriptedSpawner(child), timers, timeoutMs: 30_000 })
    child.emitStdout('{"a":')
    child.exitWithoutClose(0, null)
    timers.fire()
    await expect(read).rejects.toThrow(/did not finish within 30s/)
  })

  it('keeps a whole character when the stderr bound cuts beside a surrogate pair', async () => {
    // `boundAppend` keeps the tail, so its cut can land between the two halves
    // of a pair: half of `🚀` is a lone surrogate, and `cliSentence` copies
    // that text into a screen's refusal.
    const child = scriptedChild()
    const read = runToCompletion(plan(), { spawner: scriptedSpawner(child), timers: manualTimers(), maxStderrBytes: 3 })
    child.emitStderr('x🚀y')
    child.exit(1, null)
    // The cut falls on the pair's first half, which is kept whole: '🚀y'.
    expect((await read).stderr).toBe('🚀y')
  })

  it('never leaves a lone surrogate in bounded stderr', async () => {
    const child = scriptedChild()
    const read = runToCompletion(plan(), { spawner: scriptedSpawner(child), timers: manualTimers(), maxStderrBytes: 2 })
    child.emitStderr('x🚀y')
    child.exit(1, null)
    const { stderr } = await read
    // The cut would land on the pair's *second* half, which is dropped: 'y'.
    expect(stderr).toBe('y')
    expect(stderr).not.toMatch(/^[\uDC00-\uDFFF]/)
  })

  it('decodes a UTF-8 character split across two chunks as one character', async () => {
    // `chunk.toString()` on each half of a split `€` (E2 82 AC) is two U+FFFD.
    const child = scriptedChild()
    const read = runToCompletion(plan(), { spawner: scriptedSpawner(child), timers: manualTimers() })
    const euro = Buffer.from('{"v":"€"}\n', 'utf8')
    child.emitStdout(euro.subarray(0, 7))
    child.emitStdout(euro.subarray(7))
    child.emitStderr(Buffer.from('é', 'utf8').subarray(0, 1))
    child.emitStderr(Buffer.from('é', 'utf8').subarray(1))
    child.exit(0, null)
    const result = await read
    expect(result.stdout).toBe('{"v":"€"}\n')
    expect(result.stderr).toBe('é')
    expect(result.stdout).not.toContain('\uFFFD')
  })

  it('does not let a launch’s stdin failure escape as an uncaught exception', async () => {
    // The task text is the console's one write to a child, and a child that
    // exits before draining the pipe -- a CLI that refuses a flag, a `wsl.exe`
    // that dies at once -- closes the read end: the write's `EPIPE` then
    // arrives as an `error` event on that socket. An `EventEmitter` with no
    // `error` listener **throws** on that emit, and an uncaught exception in
    // the main process is a crashed console on the one row the design lets
    // write. This stdin is a real `EventEmitter`, so the emit below throws
    // exactly as Node's socket would if the module's listener were missing.
    const stdin = new EventEmitter() as EventEmitter & {
      write(chunk: Uint8Array): boolean
      end(): boolean
    }
    const written: Uint8Array[] = []
    let ended = false
    stdin.write = (chunk: Uint8Array) => {
      written.push(chunk)
      return true
    }
    stdin.end = () => {
      ended = true
      return true
    }
    const child = { ...scriptedChild(), stdin }
    const task = new TextEncoder().encode('a task\n')
    const read = runToCompletion(plan(), { spawner: () => child, timers: manualTimers(), stdinBytes: task })
    // The task is written and the stream ended without waiting, both at
    // `spawn`, which is where the child process exists.
    expect(written).toEqual([task])
    expect(ended).toBe(true)
    // ...and the failure of that write is not the launch's outcome: the
    // console hears it, and still settles on the child's own exit code.
    expect(() => stdin.emit('error', new Error('EPIPE'))).not.toThrow()
    child.exit(7, null)
    const result = await read
    expect(result.code).toBe(7)
  })
})

describe('the live reader’s bounds', () => {
  const plan = (): SpawnPlan => planFor('live.watch', {}, safeContext())

  function collect(): SidecarCollector {
    const frames: unknown[] = []
    const notices: string[] = []
    return {
      frames,
      notices,
      events: {
        onFrame: (frame) => frames.push(frame.document),
        onNotice: (notice) => notices.push(notice.text),
        onRestart: () => undefined,
        onEnded: () => undefined,
      },
    }
  }

  it('drops a line that outgrows the cap instead of buffering it forever', () => {
    const child = scriptedChild()
    const seen = collect()
    startSidecar(plan(), seen.events, { spawner: () => child, timers: manualTimers(), maxLineLength: 32 })
    // A stream with no newline at all used to grow the buffer without bound.
    for (let index = 0; index < 100; index++) child.emitStdout('x'.repeat(10))
    expect(seen.notices).toHaveLength(1)
    expect(seen.notices[0]).toMatch(/longer than 32 characters/)
    // The rest of that line is dropped too, and the next line is read normally.
    child.emitStdout('tail of the long line\n{"day":"2026-01-01"}\n')
    expect(seen.frames).toEqual([{ day: '2026-01-01' }])
    expect(seen.notices).toHaveLength(1)
  })

  it('decodes a UTF-8 character split across two chunks in the feed', () => {
    const child = scriptedChild()
    const seen = collect()
    startSidecar(plan(), seen.events, { spawner: () => child, timers: manualTimers() })
    const line = Buffer.from('{"who":"José"}\n', 'utf8')
    const split = line.indexOf(0xc3) + 1
    child.emitStdout(line.subarray(0, split))
    child.emitStdout(line.subarray(split))
    expect(seen.frames).toEqual([{ who: 'José' }])
  })
})

interface SidecarCollector {
  readonly frames: unknown[]
  readonly notices: string[]
  readonly events: Parameters<typeof startSidecar>[1]
}

describe('the live subscription', () => {
  const WATCH_ONE = { lines: ['{"day":"2026-01-01"}'], hold: true }
  const WATCH_TWO = { lines: ['{"day":"2026-01-02"}'], hold: true }

  /** watchSpawns counts the `watch` children the stub has seen. */
  const watchSpawns = (stub: Stub): number =>
    stub.reports().filter((report) => report.argv[0] === 'watch').length

  const liveSource = (stub: Stub): Promise<CliSource> =>
    CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })

  it('answers a second live.watch from the feed it already has, and spawns no second child', async () => {
    const stub = installStub(freshDirectory(), {
      commands: { version: { stdout: '1.4.2\n' }, watch: WATCH_ONE },
    })
    const source = await liveSource(stub)
    const handlers = handlersFor(source, { openExternal: async () => {} })
    const first = await handlers['live.watch']({})
    await waitFor(() => watchSpawns(stub) === 1)
    const second = await handlers['live.watch']({})
    expect(second).toEqual(first)
    expect(watchSpawns(stub)).toBe(1)
    // The contract is on the source, so it holds for a caller that reaches it
    // directly: the handler is not what makes it idempotent.
    expect(source.subscribeLive({})).toBe(source.subscribeLive({}))
    await source.close()
  })

  it('keeps two subscriptions apart, and stops both when the source closes', async () => {
    const stub = installStub(freshDirectory(), {
      commands: { version: { stdout: '1.4.2\n' }, watch: WATCH_ONE },
    })
    const source = await liveSource(stub)
    const handlers = handlersFor(source, { openExternal: async () => {} })
    await handlers['live.watch']({})
    await handlers['live.watch']({ repo: 'acme/other' })
    await waitFor(() => watchSpawns(stub) === 2)
    expect(watchSpawns(stub)).toBe(2)
    const argvs = stub
      .reports()
      .filter((report) => report.argv[0] === 'watch')
      .map((report) => report.argv)
    expect(argvs.some((argv) => argv.includes('acme/other'))).toBe(true)
    expect(argvs.some((argv) => !argv.includes('acme/other'))).toBe(true)
    await source.close()
    await waitFor(() => stub.kills().length === 2)
    expect(stub.kills().map((kill) => kill.signal)).toEqual(['SIGTERM', 'SIGTERM'])
  })

  it('keeps at most MAX_LIVE_FEEDS children, stopping the least recently used', async () => {
    // Every distinct valid repository is a distinct argv, so a renderer that
    // walked through repositories used to start one `fugaro watch` per name and
    // keep every one of them until quit.
    expect(MAX_LIVE_FEEDS).toBe(4)
    const stub = installStub(freshDirectory(), {
      commands: { version: { stdout: '1.4.2\n' }, watch: WATCH_ONE },
    })
    const source = await liveSource(stub)
    const repos = ['acme/a', 'acme/b', 'acme/c', 'acme/d']
    const feeds = repos.map((repo) => source.subscribeLive({ repo }))
    await Promise.all(feeds.map((feed) => feed.current()))
    // `acme/a` is used again, so `acme/b` is now the least recently used.
    expect(source.subscribeLive({ repo: 'acme/a' })).toBe(feeds[0])
    let evicted: string | null = null
    feeds[1]?.onEnded((end) => {
      evicted = end.reason
    })
    const fifth = source.subscribeLive({ repo: 'acme/e' })
    await fifth.current()
    await waitFor(() => evicted !== null)
    expect(evicted).toBe('stopped')
    await waitFor(() => stub.kills().length === 1)
    expect(stub.kills()[0]?.argv).toContain('acme/b')
    expect(watchSpawns(stub)).toBe(5)
    // The evicted subscription is a fresh child when it is asked for again.
    expect(source.subscribeLive({ repo: 'acme/b' })).not.toBe(feeds[1])
    await waitFor(() => watchSpawns(stub) === 6)
    await source.close()
    await waitFor(() => stub.kills().length === 6)
  })

  it('answers every overlapping current() with the same first frame', async () => {
    // `handlersFor`'s `live.watch` calls `current()` on every invocation and a
    // screen reaches that once per poll, so two polls can overlap. One waiter
    // slot would drop the first call, whose own budget timer would then refuse
    // it -- 'no document arrived within Ns' -- on a feed that is delivering
    // documents the whole time, which is a refusal the operator sees on screen.
    const stub = installStub(freshDirectory(), {
      commands: { version: { stdout: '1.4.2\n' }, watch: WATCH_ONE },
    })
    const source = await CliSource.create({
      target: { project: PROJECT },
      configuredBinaryPath: stub.binary,
      appEnv: {},
      firstFrameBudgetMs: 200,
    })
    const feed = source.subscribeLive({})
    const [first, second] = await Promise.all([feed.current(), feed.current()])
    expect(second).toEqual(first)
    expect(first.day).toBe('2026-01-01')
    await source.close()
  })

  it('drops a feed that ended, so the retry after a refusal starts a fresh child', async () => {
    const stub = installStub(freshDirectory(), {
      commands: { version: { stdout: '1.4.2\n' }, watch: { stderr: 'fugaro: no project selected\n', code: 1 } },
    })
    const source = await liveSource(stub)
    const refused = source.subscribeLive({})
    await expect(refused.current()).rejects.toThrow(/no project selected/)
    await waitFor(() => watchSpawns(stub) === 1)
    // The operator fixed what the sentence asked for; the next poll must ask the
    // CLI again rather than hand back the feed that already ended.
    stub.write({ commands: { version: { stdout: '1.4.2\n' }, watch: WATCH_TWO } })
    const retried = source.subscribeLive({})
    expect(retried).not.toBe(refused)
    const snapshot = await retried.current()
    expect(snapshot.day).toBe('2026-01-02')
    await waitFor(() => watchSpawns(stub) === 2)
    expect(watchSpawns(stub)).toBe(2)
    await source.close()
    await waitFor(() => stub.kills().length === 1)
    expect(stub.kills()).toHaveLength(1)
  })

  it('drops a feed the console stopped, so the next watch starts a child again', async () => {
    const stub = installStub(freshDirectory(), {
      commands: { version: { stdout: '1.4.2\n' }, watch: WATCH_ONE },
    })
    const source = await liveSource(stub)
    const feed = source.subscribeLive({})
    await feed.current()
    await feed.stop()
    await waitFor(() => stub.kills().length === 1)
    expect(stub.kills()[0]?.argv).toContain('watch')
    const next = source.subscribeLive({})
    expect(next).not.toBe(feed)
    await waitFor(() => watchSpawns(stub) === 2)
    expect(watchSpawns(stub)).toBe(2)
    await source.close()
    await waitFor(() => stub.kills().length === 2)
    expect(stub.kills()).toHaveLength(2)
  })
})

describe('the installation store', () => {
  it('stores an installation whose checkout matches, after one doctor read', async () => {
    const directory = freshDirectory()
    const checkout = freshDirectory('fugaro-checkout-')
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{"checks":[]}\n' } } })
    const store = createInstallationStore({ appEnv: {} })
    await store.add({ project: PROJECT, binaryPath: stub.binary, checkout })
    expect(store.list()).toEqual([{ project: PROJECT, binaryPath: stub.binary, checkout }])
    const doctor = stub.reports().find((report) => report.argv[0] === 'doctor')
    expect(doctor?.cwd).toBe(checkout)
    expect(doctor?.argv).toEqual(['doctor', '--json', '--project', PROJECT, '--dir', checkout])
  })

  it('refuses to store a checkout that belongs to another project, showing the CLI’s sentence', async () => {
    const directory = freshDirectory()
    const checkout = freshDirectory('fugaro-checkout-')
    const stub = installStub(directory, {
      commands: {
        version: { stdout: '1.4.2\n' },
        doctor: {
          stderr: 'fugaro: this checkout belongs to project acme; --project says fugaro\n',
          code: 1,
        },
      },
    })
    const store = createInstallationStore({ appEnv: {} })
    await expect(store.add({ project: PROJECT, binaryPath: stub.binary, checkout })).rejects.toThrow(
      'this checkout belongs to project acme; --project says fugaro',
    )
    expect(store.list()).toEqual([])
  })

  it('refuses an installation with no project name before spawning anything', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' } } })
    const store = createInstallationStore({ appEnv: {} })
    await expect(store.add({ project: '', binaryPath: stub.binary })).rejects.toBeInstanceOf(SourceRefusal)
    expect(stub.calls()).toBe(0)
  })

  it('never reads the checkout itself: the CLI is the reader', async () => {
    // Rule 7, as an observable: **a `fugaro.yaml` that is a directory**. The
    // console hands the checkout to the child as its `cwd` and reads nothing in
    // it, so `add` succeeds on the CLI's own answer; a console that opened
    // `join(checkout, 'fugaro.yaml')` would throw `EISDIR` here, which is the
    // one way to make the read observable without instrumenting `fs` (a
    // mode-`0000` file proves nothing when the suite runs as root).
    const directory = freshDirectory()
    const checkout = freshDirectory('fugaro-checkout-')
    mkdirSync(join(checkout, 'fugaro.yaml'), { recursive: true })
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const store = createInstallationStore({ appEnv: {} })
    await store.add({ project: PROJECT, binaryPath: stub.binary, checkout })
    expect(store.list()).toHaveLength(1)
    // ...and the console's whole use of the checkout is the child's `cwd`: the
    // child is the thing that read it, and it reported where it stood. The
    // source-level half of rule 7 -- that no file under `src/` opens a
    // *config-shaped* path anywhere -- is `test/unit/configread.test.ts`'s scan,
    // which walks the same tree and holds the mutation as a fixture.
    expect(stub.reports().at(-1)?.cwd).toBe(checkout)
  })

  // The `persistence` seam (plan Task 3.4b, step 3: "`createInstallationStore`
  // gains a persistence seam (`load`/`save` dependencies) so its verification
  // logic stays the one in `cli.ts`"). It is what `src/main/installations.ts`
  // hands in, so the store's **ordering** -- load first, check, push, then save
  // -- is the part worth pinning: each of the three tests below fails for a
  // store that got one of those four moves in the wrong place.
  it('starts from what it loaded, and saves the whole list after a verified add', async () => {
    const directory = freshDirectory()
    const checkout = freshDirectory('fugaro-checkout-')
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const held: InstallationCandidate = { project: 'held', binaryPath: stub.binary }
    const saved: (readonly InstallationCandidate[])[] = []
    const store = createInstallationStore({
      appEnv: {},
      persistence: { load: () => [held], save: (list) => void saved.push([...list]) },
    })
    // The load is what the store starts from, and it is a copy: a `save` that
    // mutated the list it was handed could not reach back into the store.
    expect(store.list()).toEqual([held])
    await store.add({ project: PROJECT, binaryPath: stub.binary, checkout })
    expect(store.list()).toEqual([held, { project: PROJECT, binaryPath: stub.binary, checkout }])
    expect(saved).toEqual([[held, { project: PROJECT, binaryPath: stub.binary, checkout }]])
  })

  it('writes nothing when the check refuses, and nothing before the check', async () => {
    const directory = freshDirectory()
    const checkout = freshDirectory('fugaro-checkout-')
    const stub = installStub(directory, {
      commands: {
        version: { stdout: '1.4.2\n' },
        doctor: { stderr: 'fugaro: this checkout belongs to project acme; --project says fugaro\n', code: 1 },
      },
    })
    let saves = 0
    const store = createInstallationStore({
      appEnv: {},
      persistence: { load: () => [], save: () => void (saves += 1) },
    })
    await expect(store.add({ project: PROJECT, binaryPath: stub.binary, checkout })).rejects.toThrow(
      'this checkout belongs to project acme',
    )
    // A store that saved before it checked -- or saved on the refusing path --
    // would have written an installation the CLI just refused.
    expect(saves).toBe(0)
    expect(store.list()).toEqual([])
  })

  it('has the installation in memory when the save itself fails', async () => {
    // The order in the store is: check, push, save. A `save` that throws
    // therefore leaves the two halves disagreeing in the one direction that is
    // safe -- the list has the installation, the file does not, and the next
    // add re-verifies it. A store that pushed *after* saving would lose it, and
    // the assertion below is what says so.
    const directory = freshDirectory()
    const checkout = freshDirectory('fugaro-checkout-')
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const store = createInstallationStore({
      appEnv: {},
      persistence: {
        load: () => [],
        save: () => {
          throw new Error('ENOSPC: no space left on device')
        },
      },
    })
    await expect(store.add({ project: PROJECT, binaryPath: stub.binary, checkout })).rejects.toThrow('ENOSPC')
    expect(store.list()).toEqual([{ project: PROJECT, binaryPath: stub.binary, checkout }])
  })
})

describe('the source as a whole', () => {
  it('answers a refused read with the CLI’s own sentence', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: {
        version: { stdout: '1.4.2\n' },
        'runs ls': { stderr: 'fugaro: no runs matched --since 3d\n', code: 1 },
      },
    })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    await expect(source.listRuns({ since: '3d' })).rejects.toThrow('no runs matched --since 3d')
  })

  it('refuses a read that printed no document rather than drawing an empty screen', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, 'runs ls': { stdout: 'no runs\n' } },
    })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    await expect(source.listRuns({})).rejects.toBeInstanceOf(SourceRefusal)
  })

  it('drops the `fugaro: ` prefix from the sentence a screen shows', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: {
        version: { stdout: '1.4.2\n' },
        'runs ls': { stderr: 'fugaro: no runs matched --since 3d\n', code: 1 },
      },
    })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    const error = await source.listRuns({ since: '3d' }).catch((thrown: unknown) => thrown)
    expect(error).toBeInstanceOf(SourceRefusal)
    expect((error as SourceRefusal).sentence).toBe('no runs matched --since 3d')
  })

  it('returns the version line and the one link as text, with no document in them', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: {
        version: { stdout: '1.4.2\n' },
        'logs --url': { stdout: 'https://console.cloud.google.com/logs/query;query=run\n' },
      },
    })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    expect((await source.version()).text).toBe('1.4.2')
    const link = await source.runLogLink({ run: '20260101-120000-abcd' })
    expect(link.text).toBe('https://console.cloud.google.com/logs/query;query=run')
  })

  it('never passes --url together with --json, which the CLI refuses', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, 'logs --url': { stdout: 'https://example.invalid\n' }, logs: { stdout: '{}\n' } },
    })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    await source.runLogLink({ run: '20260101-120000-abcd' })
    await source.readRunLogs({ run: '20260101-120000-abcd' })
    for (const report of stub.reports().filter((entry) => entry.argv[0] === 'logs')) {
      expect(report.argv.includes('--json') && report.argv.includes('--url')).toBe(false)
    }
  })

  it('reads every line `logs RUN --json` prints into the window the Run screen draws', async () => {
    // `logs RUN --json` prints one compact object per entry (contract section
    // 6.2), not one document: a reader that kept the last object would hand
    // the screen a single line, and `parseLogWindow` none at all. 250 entries,
    // one torn line in the middle, and the window is the newest cap of them.
    const entry = (index: number): string =>
      JSON.stringify({ time: '2026-10-15T11:00:00Z', severity: 'INFO', stage: 'verify', message: `line ${String(index)}` })
    const lines = Array.from({ length: 250 }, (_, index) => entry(index))
    lines.splice(100, 0, '{"time":"2026-10-15T11:00:00Z","severity":"IN')
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, logs: { stdout: `${lines.join('\n')}\n` } },
    })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    const snapshot = await source.readRunLogs({ run: '20260101-120000-abcd' })
    expect(snapshot.document['total']).toBe(250)
    expect(snapshot.document['refused']).toBe(1)
    const entries = snapshot.document['entries'] as readonly Record<string, unknown>[]
    expect(entries).toHaveLength(LOG_WINDOW_MAX_ENTRIES)
    expect(entries.at(-1)?.['message']).toBe('line 249')

    // And the join: the snapshot's document is what the Run screen reads.
    const model = buildRunModel({ diagnosis: {}, logs: snapshot.document, logLink: null })
    expect(model.logs.lines).toHaveLength(RUN_LOG_CAP)
    expect(model.logs.lines.at(-1)?.message).toBe('line 249')
    expect(model.logs.total.value).toBe(250)
    expect(model.logs.dropped.value).toBe(250 - RUN_LOG_CAP)
    expect(model.logs.refused.value).toBe(1)
  })

  it('answers a run with no log entries with an empty window, not a refusal', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, logs: { stdout: '' } } })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    const snapshot = await source.readRunLogs({ run: '20260101-120000-abcd' })
    expect(snapshot.document).toEqual({ entries: [], total: 0, refused: 0 })
    // An empty window is a measured nothing: no lines, and zero refused.
    const model = buildRunModel({ diagnosis: {}, logs: snapshot.document, logLink: null })
    expect(model.logs.lines).toEqual([])
    expect(model.logs.total.value).toBe(0)
    expect(model.logs.refused.value).toBe(0)
  })

  it('records every spawn it makes, including the version handshake', async () => {
    const directory = freshDirectory()
    const logPath = join(directory, 'spawn.jsonl')
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, 'budget show': { stdout: '{"budget":{}}\n' } },
    })
    const source = await CliSource.create({
      target: { project: 'chosen' },
      configuredBinaryPath: stub.binary,
      appEnv: { FUGARO_CONSOLE_SPAWN_LOG: logPath },
    })
    await source.showBudget({})
    const lines = readFileSync(logPath, 'utf8').split('\n').filter((line) => line !== '')
    expect(lines).toHaveLength(2)
    const first = JSON.parse(lines[0] ?? '{}') as { argv: string[]; project: string }
    const second = JSON.parse(lines[1] ?? '{}') as { argv: string[] }
    expect(first.argv).toEqual([stub.binary, 'version'])
    expect(first.project).toBe('chosen')
    expect(second.argv).toEqual([stub.binary, 'budget', 'show', '--json', '--project', 'chosen'])
  })

  it('hands the same spawn log to every spawn, and no log at all when the operator did not opt in', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const source = await CliSource.create({
      target: { project: PROJECT },
      configuredBinaryPath: stub.binary,
      appEnv: {},
      spawnLog: disabledSpawnLog(),
    })
    await source.environmentReport({})
    expect(disabledSpawnLog().enabled).toBe(false)
    expect(createSpawnLog({}).enabled).toBe(false)
  })
})

describe('the source scan', () => {
  const codeOnly = (relative: string): string =>
    readFileSync(join(SOURCE_ROOT, relative), 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/^\s*\/\/.*$/gm, '')

  /** srcFiles is every `.ts` file under `console/src/` -- the walk these scans
   * need before a name that says *src/* is true of them.
   * `test/unit/ipc.test.ts` and `test/unit/configread.test.ts` walk the same
   * tree for their own rules. */
  const srcFiles = (): readonly { readonly path: string; readonly source: string }[] => {
    const walk = (directory: string, prefix: string): { path: string; source: string }[] => {
      const found: { path: string; source: string }[] = []
      for (const entry of readdirSync(directory, { withFileTypes: true })) {
        const relative = prefix === '' ? entry.name : `${prefix}/${entry.name}`
        if (entry.isDirectory()) found.push(...walk(join(directory, entry.name), relative))
        else if (entry.name.endsWith('.ts')) found.push({ path: relative, source: codeOnly(relative) })
      }
      return found
    }
    return walk(SOURCE_ROOT, '')
  }

  it('is the only file in src/ that spawns', () => {
    // Every `.ts` file under `console/src/`, walked rather than listed. The name
    // claims whole-tree reach and the old eight-name list did not have it: a new
    // module under `source/` or a new directory would have been outside the
    // scan, which is the one thing this test exists to catch.
    const spawners = srcFiles()
      .filter((file) => file.source.includes('child_process'))
      .map((file) => file.path)
    expect(spawners).toEqual(['main/spawn.ts'])
    expect(codeOnly('main/spawn.ts')).toContain("from 'node:child_process'")
    // ...and there is exactly one `spawn(` call site, with `shell: false` as a
    // literal rather than a value a caller could set.
    expect(codeOnly('main/spawn.ts').match(/spawn\(/g) ?? []).toHaveLength(1)
    expect(codeOnly('main/spawn.ts')).toContain('shell: false')
    // Every `shell:` in the file is `shell: false`: counted rather than
    // regexed, because a negative lookahead here backtracks into passing.
    const source = codeOnly('main/spawn.ts')
    expect((source.match(/shell:/g) ?? []).length).toBe((source.match(/shell: false/g) ?? []).length)
  })

  it('takes stdin bytes from the launch, and from nowhere else in src/', () => {
    // `stdinBytes` is the one way a caller hands a child bytes on its standard
    // input, and the launch is its one caller: the task text must reach `fugaro
    // run` without ever being an argv word or a spawn-log line (rule 11), and a
    // second caller would be a second child this console writes to. The scan is
    // over every `.ts` file under `src/`, so the next module that reaches for it
    // fails here rather than in review -- the mechanism in `main/spawn.ts` and
    // the one caller in `main/launch.ts`, and nothing else.
    const callers = srcFiles()
      .filter((file) => file.source.includes('stdinBytes'))
      .map((file) => file.path)
    expect([...callers].sort()).toEqual(['main/launch.ts', 'main/spawn.ts'])
    expect(codeOnly('main/launch.ts')).toContain('stdinBytes: launch.taskBytes')
  })

  it('hands the real spawner `shell: false`, as a literal, and never `true`', () => {
    // The mutation this catches is `shell: true` on `nodeSpawner` itself: the
    // recording-spawner tests above assert what the *caller* passes, and this
    // asserts what the real spawner does with it. Counted rather than regexed --
    // a negative lookahead after `\s*` backtracks into passing.
    const source = codeOnly('main/spawn.ts')
    expect((source.match(/shell:/g) ?? []).length).toBeGreaterThan(1)
    expect((source.match(/shell: false/g) ?? []).length).toBe((source.match(/shell:/g) ?? []).length)
    expect(source).toContain('export const nodeSpawner: ChildSpawner')
  })

  it('has no shell string anywhere: no exec, no execSync, no shell', () => {
    const source = codeOnly('main/spawn.ts')
    expect(source).not.toMatch(/\bexecSync?\(/)
    expect(source).not.toMatch(/shell:\s*true/)
    expect(source).not.toMatch(/\/bin\/sh/)
  })

  it('closes stdin at every spawn site, and pipes the other two', () => {
    // T-E3, finding F19: `stdio: ['ignore', 'pipe', 'pipe']` is written at each
    // site that starts a child -- the real spawner, the supervised run and the
    // live subscription -- so a CLI that decides to read a prompt reads EOF
    // rather than the console's terminal, and nothing a window does with the
    // keyboard reaches a child. The mutation this catches is `'inherit'` (or a
    // dropped `stdio`, which hands the child the parent's streams by default) at
    // any one of the three: the count is asserted as well as the literal, so
    // deleting the option fails here rather than passing quietly.
    //
    // Part F adds a fourth site and changes the real spawner's: the stop sent
    // **inside** a distribution (T-F4) reads nothing, so it asks for `'ignore'`
    // on all three, and the real spawner now copies its caller's request
    // (`[...options.stdio]`) instead of hardcoding the reads' answer.
    //
    // **V2.5.1a's launch is the one site whose stdin is not a literal
    // `'ignore'`**: `runToCompletion` asks for `'pipe'` on that one slot when
    // its caller passed `stdinBytes`, and only the launch's own call ever does
    // -- the task text goes down the child's stdin, never onto the command
    // line. Every other site, and every other row, still says `'ignore'`, and
    // the two literals below are what keep that a fact rather than a habit.
    const source = codeOnly('main/spawn.ts')
    const optionLines = source.match(/stdio: \[[^\]]*\]/g) ?? []
    expect([...optionLines].sort()).toEqual(
      [
        'stdio: [...options.stdio]',
        "stdio: ['ignore', 'ignore', 'ignore']",
        "stdio: ['ignore', 'pipe', 'pipe']",
        "stdio: [deps.stdinBytes === undefined ? 'ignore' : 'pipe', 'pipe', 'pipe']",
      ].sort(),
    )
    // The type the spawner is handed says the same thing: one slot admits
    // `'pipe'`, and it is the caller's to ask for -- so a read cannot widen it
    // by accident, and a row that asks for a pipe without saying what to write
    // is a child that hangs on a stream nobody ends.
    expect(source).toContain(
      "stdio: readonly ['ignore' | 'pipe', 'ignore' | 'pipe', 'ignore' | 'pipe']",
    )
    expect(source).not.toMatch(/stdio:\s*'inherit'/)
    expect(source).not.toMatch(/stdio:\s*\[[^\]]*'inherit'/)
  })
})

describe('parseLogEntries', () => {
  const line = (fields: Record<string, unknown>): string => JSON.stringify(fields)
  const base = { time: '2026-10-15T11:00:00Z', severity: 'INFO', message: 'ok' }

  it('keeps every well-shaped entry, and only the fields logLine has', () => {
    const read = parseLogEntries(
      [line(base), line({ ...base, stage: 'verify', stream: 'stdout', event: 'e', extra: 'x' }), ''].join('\n'),
    )
    expect(read.total).toBe(2)
    expect(read.refused).toBe(0)
    expect(read.entries[1]).toEqual({ ...base, stage: 'verify', stream: 'stdout', event: 'e' })
  })

  it('refuses a torn line, a non-object, a wrong-typed field and a missing required one', () => {
    const read = parseLogEntries(
      [
        line(base),
        '{"time":"2026',
        '[1,2]',
        'a sentence',
        line({ ...base, severity: 3 }),
        line({ ...base, stage: {} }),
        line({ time: base.time, severity: base.severity }),
        line(base),
      ].join('\r\n'),
    )
    expect(read.total).toBe(2)
    expect(read.refused).toBe(6)
  })

  it('refuses a line past the line cap without parsing it, and a field past the text cap', () => {
    const long = line({ ...base, message: 'm'.repeat(200) })
    const read = parseLogEntries([long, line(base)].join('\n'), { maxLineLength: 100 })
    expect(read.total).toBe(1)
    expect(read.refused).toBe(1)
    const field = parseLogEntries(line({ ...base, message: 'm'.repeat(70 * 1024) }))
    expect(field.total).toBe(0)
    expect(field.refused).toBe(1)
  })

  it('reads a CRLF line\'s carriage return as its terminator, not as a character past the line cap', () => {
    const exact = line(base)
    const read = parseLogEntries([exact, exact].join('\r\n') + '\r\n', { maxLineLength: exact.length })
    expect(read.refused).toBe(0)
    expect(read.total).toBe(2)
  })

  it('keeps the newest entries up to the cap, oldest first', () => {
    const lines = Array.from({ length: 12 }, (_, index) => line({ ...base, message: String(index) }))
    const read = parseLogEntries(lines.join('\n'), { maxEntries: 5 })
    expect(read.total).toBe(12)
    expect(read.entries.map((entry) => entry['message'])).toEqual(['7', '8', '9', '10', '11'])
  })

  it('caps the window at the Run screen\'s own cap', () => {
    expect(LOG_WINDOW_MAX_ENTRIES).toBe(RUN_LOG_CAP)
  })
})

/** countingLog is a spawn-log writer that keeps its lines in memory, so a test
 * can count what a spawn path wrote without a file. */
function countingLog(): SpawnLogWriter & { readonly lines: readonly string[] } {
  const lines: string[] = []
  return {
    enabled: true,
    path: null,
    error: null,
    get records() {
      return lines.length
    },
    record: (spawn) => {
      lines.push(spawn.argv.join(' '))
    },
    close: () => undefined,
    lines,
  }
}

/** throwingSpawner is a spawn that never produced a child: the binary was
 * removed between resolution and spawn. */
const throwingSpawner: ChildSpawner = () => {
  const error = new Error('spawn /opt/homebrew/bin/fugaro ENOENT') as Error & { code?: string }
  error.code = 'ENOENT'
  throw error
}

/**
 * childThatNeverSpawns is the other way a spawn produces no process, and the one
 * that does not throw: `child_process.spawn` returns a `ChildProcess` object
 * whatever happens, then emits `error` (ENOENT for a binary that is not there,
 * EACCES for one that is not executable) and never emits `spawn`. The streams
 * are never readable, and nothing is ever written to them.
 */
function childThatNeverSpawns(): SpawnedChildLike {
  return {
    stdout: { on: () => undefined },
    stderr: { on: () => undefined },
    on: ((
      event: 'spawn' | 'error' | 'exit' | 'close',
      listener: (...args: unknown[]) => void,
    ) => {
      if (event !== 'error') return undefined
      queueMicrotask(() => {
        const error = new Error('spawn /opt/homebrew/bin/fugaro ENOENT') as Error & { code?: string }
        error.code = 'ENOENT'
        listener(error)
      })
      return undefined
    }) as SpawnedChildLike['on'],
    kill: () => true,
  }
}

describe('the spawn log records children, not attempts', () => {
  it('writes no line when a one-shot read never produced a child', async () => {
    const log = countingLog()
    const plan = planFor('budget.show', {}, safeContext())
    await expect(runToCompletion(plan, { spawner: throwingSpawner, log })).rejects.toThrow(/ENOENT/)
    expect(log.records).toBe(0)
  })

  it('writes no line when the spawner handed back a child that never spawned', async () => {
    // The case a throwing spawner hides: `spawn` returns a `ChildProcess` for a
    // binary that is not there and fails afterwards, so a line recorded on the
    // spawner's return is a line for a process that never existed (review).
    const log = countingLog()
    const plan = planFor('budget.show', {}, safeContext())
    await expect(runToCompletion(plan, { spawner: () => childThatNeverSpawns(), log })).rejects.toThrow(
      /ENOENT/,
    )
    expect(log.records).toBe(0)
  })

  it('writes one line for a one-shot read that did spawn', () => {
    const log = countingLog()
    const plan = planFor('budget.show', {}, safeContext())
    void runToCompletion(plan, { spawner: recordingSpawner(), log }).catch(() => undefined) // a fake child never closes: no late rejection
    expect(log.records).toBe(1)
  })

  it('writes no line for a feed whose child never spawned, and ends as unstartable', () => {
    const log = countingLog()
    const plan = planFor('live.watch', {}, safeContext())
    const ended: string[] = []
    startSidecar(plan, {
      onFrame: () => undefined,
      onNotice: () => undefined,
      onRestart: () => undefined,
      onEnded: (end) => ended.push(end.reason),
    }, { spawner: () => childThatNeverSpawns(), timers: manualTimers(), log })
    return waitFor(() => ended.length > 0).then(() => {
      expect(ended).toEqual(['unstartable'])
      expect(log.records).toBe(0)
    })
  })

  it('writes no line when a feed never produced a child, and one per child it did', () => {
    const log = countingLog()
    const plan = planFor('live.watch', {}, safeContext())
    const ended: string[] = []
    startSidecar(plan, {
      onFrame: () => undefined,
      onNotice: () => undefined,
      onRestart: () => undefined,
      onEnded: (end) => ended.push(end.reason),
    }, { spawner: throwingSpawner, timers: manualTimers(), log })
    expect(ended).toEqual(['unstartable'])
    expect(log.records).toBe(0)

    const second = countingLog()
    startSidecar(plan, {
      onFrame: () => undefined,
      onNotice: () => undefined,
      onRestart: () => undefined,
      onEnded: () => undefined,
    }, { spawner: () => scriptedChild(), timers: manualTimers(), log: second })
    expect(second.records).toBe(1)
  })
})

describe('a feed whose last line has no newline', () => {
  it('delivers the last document when the child closes without a final newline', () => {
    const plan = planFor('live.watch', {}, safeContext())
    const child = scriptedChild()
    const frames: unknown[] = []
    const notices: string[] = []
    startSidecar(plan, {
      onFrame: (frame) => frames.push(frame.document),
      onNotice: (notice) => notices.push(notice.text),
      onRestart: () => undefined,
      onEnded: () => undefined,
    }, { spawner: () => child, timers: manualTimers() })
    child.emitStdout('{"day":"2026-01-01"}\n{"day":"2026-01-02"}')
    expect(frames).toHaveLength(1)
    child.close(0, null)
    expect(frames).toEqual([{ day: '2026-01-01' }, { day: '2026-01-02' }])
    expect(notices).toEqual([])
  })

  it('says a torn last line was dropped, without echoing it', () => {
    const plan = planFor('live.watch', {}, safeContext())
    const child = scriptedChild()
    const frames: unknown[] = []
    const notices: string[] = []
    startSidecar(plan, {
      onFrame: (frame) => frames.push(frame.document),
      onNotice: (notice) => notices.push(notice.text),
      onRestart: () => undefined,
      onEnded: () => undefined,
    }, { spawner: () => child, timers: manualTimers() })
    child.emitStdout('{"day":"2026-01-01"}\n{"day":"2026-')
    child.close(0, null)
    expect(frames).toHaveLength(1)
    expect(notices).toHaveLength(1)
    expect(notices[0]).toMatch(/last line .*without a newline/)
    expect(notices[0]).not.toContain('2026-')
  })
})

/**
 * Part F of docs/console-windows/implementation-plan.md: spawning, streaming,
 * cancellation and exit codes on the WSL route (T-F1, T-F3, T-F4, T-F5).
 *
 * Every case here runs the **fake** `wsl.exe` (T-A1): Windows cannot be reached
 * from this machine, so the doubles are the whole of the evidence, and the
 * checks that need a real Windows host are marked not-run in the PR rather than
 * guessed at here.
 *
 * The one arrangement the cancellation cases need: the directory the
 * distribution starts in (`--cd`) must be the **distribution's** spelling of
 * the directory the console starts `wsl.exe` in, because a Windows spelling is
 * what T-D3/T-D4 exist to translate and the double refuses one (T-C5). The pid
 * the stop names is not a path at all: it rides the wrapper's own stderr line
 * (T-F4, D6(a)), so nothing here has to agree about a file.
 */
// 30 s, because most cases in this suite drive real processes rather than a
// scripted child: `installWslStub` installs a real `wsl.exe` that execs the real
// stub `fugaro`, and what they read back -- the pid a stop named, a status, an
// orphan's report -- comes out of the report file those processes write. The
// observed time is all process startup, and `waitFor` already allows itself 15 s
// for exactly that ("a shared CI or developer machine under load can take
// seconds to start one"); the implicit 5 s default overrode the file's own
// budget, and 'cancels inside the distribution' timed out on a macOS runner
// while passing on rerun. The deadline and the grace the cases assert about are
// still the supervisor's injected clock, fired by the test itself rather than
// waited out; nothing asserted here became weaker, only the suite's own budget.
describe('the wrapper into a distribution', () => {
  const TWO: WslFixture = {
    list: 'Ubuntu\r\nDebian\r\n',
    status: 'Default Distribution: Ubuntu\r\nDefault Version: 2\r\n',
  }

  /** SOME_NONCE and SOME_MARKER are a marker of the shape a plan builds, for the
   * cases that read a head rather than a plan: `wslWrapperMarker` is the one
   * function that composes one, so a case here cannot drift from what
   * `wslWrapperFor` writes into the script. The nonce is a constant because
   * these cases are about the *shape*, and the per-plan randomness is what the
   * end-to-end cases below read off `plan.wrapper.marker`. */
  const SOME_NONCE = 'a'.repeat(WSL_WRAPPER_NONCE_BYTES * 2)
  const SOME_OTHER_NONCE = 'b'.repeat(WSL_WRAPPER_NONCE_BYTES * 2)
  const SOME_MARKER = wslWrapperMarker(SOME_NONCE)


  /** wslPlanIn is a plan whose `--cd` and whose `cwd` are one directory: what
   * the fake does with a child's directory is not this suite's subject, and one
   * directory keeps the plans here readable. */
  function wslPlanIn(wsl: WslStub, operation: string, args: ValidatedArguments = {}): SpawnPlan {
    return planFor(operation as OperationName, args, {
      target: { project: PROJECT },
      binaryPath: '',
      directory: wsl.stub.directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', directory: wsl.stub.directory, executable: wsl.binary },
    })
  }

  /** spawnReports is the `wsl.exe` invocations that started something, in
   * order: the first is the CLI's own, and a second is a stop. */
  const spawnReports = (wsl: WslStub): readonly WslReport[] =>
    wsl.reports().filter((report) => report.kind === 'spawn')

  it('carries the CLI’s exit status through the wrapper, and never shows the wrapper', async () => {
    // T-F1, F26. The wrapper is transparent to a status: what the operator
    // reads is `fugaro`'s, and the marker the shell prints before it `exec`s
    // never reaches a screen (`cliSentence` is what a screen shows).
    //
    // The `wsl.exe` here prints a relay warning of its own **before** the
    // distribution's command runs, which is what the real one does: the
    // wrapper's line is then not the first thing on the pipe, and both facts
    // read off it -- that a child started, and its pid -- have to survive that.
    const preamble = 'wsl: a relay warning about localhost\n'
    const seven = freshDirectory()
    const failing = installWslStub(join(seven, 'windows'), {
      distros: TWO,
      scenario: { commands: { version: { stdout: '1.4.2\n', code: 7 } } },
      preamble,
    })
    const result = await runToCompletion(wslPlanIn(failing, 'cli.version'), { timeoutMs: 10_000 })
    expect(result.code).toBe(7)
    // A child inside the distribution did start, so the status is the CLI's.
    expect(result.wrapperStarted).toBe(true)
    expect(result.stdout).toBe('1.4.2\n')
    // `wsl.exe`'s own warning is `wsl.exe`'s and stays; the console's own line
    // -- marker and pid together -- does not.
    expect(result.stderr).toBe(preamble)
    expect(result.stderr).not.toContain(WSL_WRAPPER_MARKER_PREFIX)

    const zero = freshDirectory()
    const answering = installWslStub(join(zero, 'windows'), {
      distros: TWO,
      scenario: { commands: { version: { stdout: '1.4.2\n' } } },
    })
    const ok = await runToCompletion(wslPlanIn(answering, 'cli.version'), { timeoutMs: 10_000 })
    expect(ok.code).toBe(0)
    expect(ok.wrapperStarted).toBe(true)
    // The child exited on its own, so there is nothing to stop and no second
    // `wsl.exe` was sent (T-F4).
    expect(spawnReports(answering)).toHaveLength(1)
    expect(answering.stub.kills()).toEqual([])
  })

  it('never reads a wsl.exe failure as a CLI exit code', async () => {
    // T-F1's other half (F26, U7/U8). A `--cd` that does not exist inside the
    // distribution is a `wsl.exe` that fails **before** it starts anything
    // there: the status and the sentence are `wsl.exe`'s, and a console that
    // read them as `fugaro`'s would send an operator to debug a CLI that never
    // ran. (A Windows spelling would be the other way to get here, and Part D's
    // `planFor` now refuses that one before any process exists.)
    const directory = freshDirectory()
    const wsl = installWslStub(join(directory, 'windows'), { distros: TWO })
    const plan = planFor('cli.version', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', directory: join(directory, 'not-there'), executable: wsl.binary },
    })
    const result = await runToCompletion(plan, { timeoutMs: 10_000 })
    expect(result.code).not.toBe(0)
    expect(result.wrapperStarted).toBe(false)
    expect(result.stderr).toContain('wsl.exe (the fake)')
    // Nothing was started inside the distribution, which is what "before" means.
    expect(wsl.children()).toEqual([])

    // ...and the probe says whose status it was, and keeps the number out of
    // the field a classifier reads.
    const probe = await probeVersion(plan, { timeoutMs: 10_000 })
    expect(probe.ok).toBe(false)
    if (probe.ok) return
    expect(probe.code).toBeNull()
    expect(probe.wrapperStarted).toBe(false)
    expect(probe.reason).toContain('wsl.exe exited')
    expect(probe.reason).not.toContain('the binary exited')
  })

  it('cancels inside the distribution, naming the pid the wrapper recorded', async () => {
    // T-F4, F24 and decision D6(a). Killing the Windows-side `wsl.exe` is not
    // documented to kill the Linux process it started, so the console sends one
    // more `wsl.exe` that kills the pid the wrapper reported -- and the fake
    // here **ignores SIGTERM**, which is what makes this the case a console that
    // only killed `wsl.exe` fails.
    //
    // The fake's `wsl.exe` prints a warning of its own first, as the real one
    // does, so the wrapper's line -- and the pid on it -- is not the first thing
    // on the pipe.
    const directory = freshDirectory()
    const wsl = installWslStub(join(directory, 'windows'), {
      distros: TWO,
      scenario: { commands: { version: { stdout: '1.4.2\n', hold: true } } },
      holdsOnTerm: true,
      preamble: 'wsl: a relay warning about localhost\n',
    })
    const timers = manualTimers()
    const pending = runToCompletion(wslPlanIn(wsl, 'cli.version'), { timers, timeoutMs: 100, shutdownGraceMs: 50 })
    await waitFor(() => wsl.children().length === 1)
    const refusal: Promise<SpawnTimeout> = pending.then(
      () => {
        throw new Error('the read was expected to be refused')
      },
      (error: SpawnTimeout) => error,
    )

    timers.fire()
    const timeout = await refusal
    expect(timeout).toBeInstanceOf(SpawnTimeout)
    // The pid was read before the grace, so the sentence carries no residual:
    // the console could name the process it stopped.
    expect(timeout.message).not.toContain('may still be running')

    timers.fire()
    await waitFor(() => spawnReports(wsl).length === 2)
    const stop = spawnReports(wsl)[1]
    // The stop is one `wsl.exe`, into the same distribution, with the shell the
    // wrapper used: `wsl.exe -d Ubuntu -e sh -c 'kill -TERM <pid>'`.
    expect(stop?.distro).toBe('Ubuntu')
    expect(stop?.argv.slice(0, 5)).toEqual(['-d', 'Ubuntu', '-e', WSL_SHELL_PROGRAM, WSL_SHELL_FLAG])
    expect(stop?.argv).toHaveLength(6)
    const killed = Number(/^kill -TERM ([0-9]+)$/.exec(stop?.argv[5] ?? '')?.[1])
    expect(Number.isInteger(killed)).toBe(true)

    // The proof that the pid is the **child's** and not the wrapper's: the stub
    // `fugaro` reports its own pid when it is stopped, and it is the pid the
    // stop named. A pid that were the shell's would leave this empty -- the
    // shell is gone, `exec` replaced it with the CLI.
    await waitFor(() => wsl.stub.kills().length === 1)
    const killedChild = only(wsl.stub.kills())
    expect(killedChild.signal).toBe('SIGTERM')
    expect(killedChild.pid).toBe(killed)
    expect(killedChild.argv).toEqual(['version'])
  })

  it('sends the stop when `wsl.exe` is gone before the grace runs out', async () => {
    // F24's stated consequence, and the ordering that produces it: the child
    // exits **after** the console's `SIGTERM` and **before** the grace, which
    // is what a Windows `TerminateProcess` does every time -- the Windows-side
    // process is gone within a tick, while the process it started, the CLI,
    // keeps running inside the distribution with nothing on screen to say so.
    //
    // The moment of the death is the test's rather than a race, so this is the
    // case that fails first: at the grace, `exited` is true, and a stop that
    // asked `if (!exited)` sends nothing at all.
    const directory = freshDirectory()
    const plan = planFor('cli.version', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', executable: '/usr/bin/wsl.exe' },
    })
    const calls: string[][] = []
    const child = scriptedChild()
    const spawner = ((command: string, args: readonly string[]) => {
      calls.push([command, ...args])
      return child
    }) as ChildSpawner
    const timers = manualTimers()
    const pending = runToCompletion(plan, { spawner, timers, timeoutMs: 100, shutdownGraceMs: 50 })
    child.emitStderr(`${wrapperMarkerOf(plan)} 4321\n`)
    const refusal = pending.then(
      () => {
        throw new Error('the read was expected to be refused')
      },
      (error: SpawnTimeout) => error,
    )
    timers.fire()
    // The pid was read before the grace, so there is no residual to state.
    expect((await refusal).message).not.toContain('may still be running')
    expect(child.killed).toEqual(['SIGTERM'])

    child.exit(143, 'SIGTERM')
    timers.fire()
    // Two independent facts, and the two signals must not be conflated: the
    // stronger signal goes only to a Windows-side child that is still there,
    // and the stop inside the distribution goes whatever `wsl.exe` did.
    expect(child.killed).toEqual(['SIGTERM'])
    expect(calls[1]).toEqual(['/usr/bin/wsl.exe', '-d', 'Ubuntu', '-e', 'sh', '-c', 'kill -TERM 4321'])
  })

  it('stops the Linux process when the fake `wsl.exe` dies at once on `SIGTERM`', async () => {
    // The same case end to end, with the real double: this `wsl.exe` does not
    // hold on `SIGTERM` (the case above does), so it dies the moment the
    // console kills it and the CLI it started is left as an orphan inside the
    // distribution -- the state F24 describes. The console must still send the
    // stop, and the evidence that it arrived is the stub `fugaro`'s own report
    // of the signal.
    //
    // Real timers, because the ordering that matters is between a real
    // process's death and the grace, and the fake is a real process. The
    // deadline is generous because the pid line has to have been read before
    // it fires -- the double is a Node process and this machine may be busy --
    // while what the case itself depends on is narrower: the grace is long
    // enough that the fake's death has certainly been seen, which on Windows is
    // the normal case rather than a race.
    const directory = freshDirectory()
    const wsl = installWslStub(join(directory, 'windows'), {
      distros: TWO,
      scenario: { commands: { version: { stdout: '1.4.2\n', hold: true } } },
    })
    const pending = runToCompletion(wslPlanIn(wsl, 'cli.version'), { timeoutMs: 2_000, shutdownGraceMs: 250 })
    const refusal: Promise<SpawnTimeout> = pending.then(
      () => {
        throw new Error('the read was expected to be refused')
      },
      (error: SpawnTimeout) => error,
    )
    const timeout = await refusal
    expect(timeout.message).not.toContain('may still be running')

    await waitFor(() => spawnReports(wsl).length === 2)
    const stop = spawnReports(wsl)[1]
    expect(stop?.distro).toBe('Ubuntu')
    expect(stop?.argv.slice(0, 5)).toEqual(['-d', 'Ubuntu', '-e', WSL_SHELL_PROGRAM, WSL_SHELL_FLAG])

    await waitFor(() => wsl.stub.kills().length === 1)
    const killedChild = only(wsl.stub.kills())
    expect(killedChild.signal).toBe('SIGTERM')
    expect(killedChild.argv).toEqual(['version'])
  })

  it('states the residual when the wrapper has not reported a pid', async () => {
    // D6(a)'s residual window, said on the screen rather than left implicit.
    // The one case it covers is "the console cannot name the process": the
    // wrapper prints its line before it `exec`s, so a child that has started has
    // reported -- but a deadline that fires while the distribution is still
    // starting (a cold distribution takes seconds, U15) arrives before the line
    // does. The console then says so rather than inventing a pid, and sends no
    // stop.
    //
    // The sentence is refused **at the grace**, not at the deadline: a pid can
    // still arrive during those two seconds, and the console must not tell the
    // operator the process may be running after it has just stopped it. The
    // case where it does arrive in time is its own test below.
    const directory = freshDirectory()
    const plan = planFor('cli.version', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', executable: '/usr/bin/wsl.exe' },
    })
    const calls: string[][] = []
    const child = scriptedChild()
    const spawner = ((command: string, args: readonly string[]) => {
      calls.push([command, ...args])
      return child
    }) as ChildSpawner

    const timers = manualTimers()
    const pending = runToCompletion(plan, { spawner, timers, timeoutMs: 100, shutdownGraceMs: 50 })
    const refusal: Promise<SpawnTimeout> = pending.then(
      () => {
        throw new Error('the read was expected to be refused')
      },
      (error: SpawnTimeout) => error,
    )
    timers.fire()
    // The Windows-side child is still stopped first: the residual is about the
    // process *inside* the distribution, not about the one the console owns.
    expect(child.killed).toEqual(['SIGTERM'])
    timers.fire()
    expect(child.killed).toEqual(['SIGTERM', 'SIGKILL'])
    // No second call: a pid the console does not have is not a pid it guesses.
    expect(calls).toHaveLength(1)
    // ...and the sentence the read is refused with carries the window, because
    // the grace is over and the pid never came.
    expect((await refusal).message).toContain('may still be running')
  })

  it('names a pid that arrives during the grace, and states no residual', async () => {
    // The same deadline with the wrapper's line landing a moment late -- what a
    // cold distribution does: `wsl.exe` is still booting when the read's
    // deadline fires, and the shell prints its pid while the grace is running.
    // Reading the pid once, at stop time, would leave the stop naming nothing
    // while the process it could have named kept running inside the
    // distribution, and would refuse the read with a residual that is untrue by
    // the time the operator reads it.
    const directory = freshDirectory()
    const plan = planFor('cli.version', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', executable: '/usr/bin/wsl.exe' },
    })
    const calls: string[][] = []
    const child = scriptedChild()
    const spawner = ((command: string, args: readonly string[]) => {
      calls.push([command, ...args])
      return child
    }) as ChildSpawner
    const timers = manualTimers()
    const pending = runToCompletion(plan, { spawner, timers, timeoutMs: 100, shutdownGraceMs: 50 })
    const refusal = pending.then(
      () => {
        throw new Error('the read was expected to be refused')
      },
      (error: SpawnTimeout) => error,
    )
    // The deadline fires with nothing on stderr: no pid, so no stop yet.
    timers.fire()
    expect(child.killed).toEqual(['SIGTERM'])
    // The wrapper's line arrives inside the grace window.
    child.emitStderr(`${wrapperMarkerOf(plan)} 4321\n`)
    timers.fire()
    // The stop names the pid that arrived, and exactly once: the stale read is
    // not used as well.
    expect(calls[1]).toEqual(['/usr/bin/wsl.exe', '-d', 'Ubuntu', '-e', 'sh', '-c', 'kill -TERM 4321'])
    expect(calls).toHaveLength(2)
    // The process was named, so there is no window to write down.
    expect((await refusal).message).not.toContain('may still be running')
  })

  it('feeds a watch stream through the wrapper exactly as on the host route', async () => {
    // T-F3 (F28, F29): the wrapper is not allowed to change the feed. The same
    // scenario runs on both routes and the two must produce the same frames, the
    // same notices and the same end.
    //
    // The scenario is the two things the plan names. A line ends `\r\n` -- which
    // `decodeFeedLine` strips -- and the child's stdout is written as **bytes**
    // with the cut **inside a multi-byte character**, so the two chunks on the
    // pipe are not text on their own and only a `StringDecoder` that remembers
    // the partial character reassembles the document. `split` cannot do that
    // (it cuts the string, and a string has no half a character); `splitBytes`
    // can, and the cut is computed here from the bytes rather than guessed.
    const document = '{"kind":"live","day":"2026-01-01","note":"héllo ✓"}'
    const stdout = `${document}\r\nno budget backend: showing run records only\n`
    const check = Buffer.from('✓', 'utf8')
    const splitBytes = Buffer.from(stdout, 'utf8').indexOf(check) + 1
    // The cut is inside the character, which is what makes this the case.
    expect(Buffer.from(stdout, 'utf8').subarray(splitBytes - 1, splitBytes + 1)).toEqual(check.subarray(0, 2))

    // The child writes its lines and then **refuses** (status 1): the feed then
    // settles on the child's own end on both routes, which is a fact about the
    // child rather than a moment a test picked -- and the sentence it ends with
    // is compared too, so the wrapper's own marker line is asserted absent
    // (T-F1) as well as asserted equal.
    const scenario = { commands: { watch: { stdout, splitBytes, delayMs: 5, code: 1 } } }

    const hostDirectory = freshDirectory()
    const host = installStub(hostDirectory, scenario)
    const hostPlan = planFor('live.watch', {}, {
      target: { project: PROJECT },
      binaryPath: host.binary,
      directory: hostDirectory,
      appEnv: {},
    })
    const directory = freshDirectory()
    const wsl = installWslStub(join(directory, 'windows'), { distros: TWO, scenario })
    const wslPlan = wslPlanIn(wsl, 'live.watch')

    const seen = async (plan: SpawnPlan): Promise<{ frames: unknown[]; notices: string[]; end: SidecarEnd | null }> => {
      const frames: unknown[] = []
      const notices: string[] = []
      let end: SidecarEnd | null = null
      startSidecar(plan, {
        onFrame: (frame) => frames.push(frame),
        onNotice: (notice) => notices.push(notice.text),
        onRestart: () => undefined,
        onEnded: (ended) => {
          end = ended
        },
        // A fixed clock: every frame carries a `receivedAt` stamp, and the two
        // routes run one after the other, so a wall clock would make the frames
        // differ whenever the two runs straddle a second.
      }, { clock: () => new Date('2026-01-01T00:00:00Z') })
      await waitFor(() => end !== null)
      return { frames, notices, end }
    }

    const fromHost = await seen(hostPlan)
    const fromWsl = await seen(wslPlan)
    expect(fromWsl.frames).toEqual(fromHost.frames)
    expect(fromWsl.notices).toEqual(fromHost.notices)
    expect(fromWsl.end).toEqual(fromHost.end)
    // The document arrived **whole** on both routes: the character cut in half
    // by the pipe was put back together, and the line was not dropped as a
    // fragment (which is what a lost `StringDecoder` would do with it).
    expect(fromHost.notices).toHaveLength(1)
    expect(fromWsl.frames).toHaveLength(1)
    const frame = fromWsl.frames[0] as { document?: Record<string, unknown> } | undefined
    expect(frame?.document?.['note']).toBe('héllo ✓')
  })

  it('stops a read whose pipe outlives the child at the deadline, through the wrapper', async () => {
    // T-F5 (F25): the read deadline is the backstop, and on this route it is
    // load-bearing -- the wrapper adds a relay and the distribution a session,
    // and either can hold the pipe open after the process that wrote to it has
    // gone. So the fake here **outlives its child with stdout still open**: the
    // CLI exits normally, and the reader sees no `close`.
    //
    // The assertions are both halves of the rule: nothing settles the read
    // before the deadline (a reader that settled on `exit` would hand back a
    // torn document here, which is the failure `close`-not-`exit` prevents),
    // and it settles **at** the deadline rather than never.
    const directory = freshDirectory()
    const wsl = installWslStub(join(directory, 'windows'), {
      distros: TWO,
      scenario: { commands: { version: { stdout: '1.4.2\n' } } },
      lingersAfterChild: true,
    })
    const timers = manualTimers()
    const pending = runToCompletion(wslPlanIn(wsl, 'cli.version'), { timers, timeoutMs: 90_000, shutdownGraceMs: 50 })
    await waitFor(() => wsl.children().length === 1)
    let settled = false
    void pending.then(
      () => {
        settled = true
      },
      () => {
        settled = true
      },
    )
    // The CLI has run and exited; the pipe is still open, and nothing has
    // settled the read.
    await new Promise((resolve) => setTimeout(resolve, 100))
    expect(settled).toBe(false)
    expect(timers.fired()).toEqual([])

    timers.fire()
    const error: SpawnTimeout = await pending.then(
      () => {
        throw new Error('the read was expected to be refused')
      },
      (thrown: SpawnTimeout) => thrown,
    )
    expect(error).toBeInstanceOf(SpawnTimeout)
    expect(error.message).toContain('did not finish within 90s')
    expect(timers.fired()).toEqual([90_000])
    // The child was stopped rather than left behind, exactly as on a timeout
    // anywhere else in this module: the shutdown grace is armed, and its
    // `SIGKILL` is the escalation for a process that ignores the `SIGTERM`.
    expect(timers.pending()).toBe(1)
  })

  it('refuses a hostile word before the shell, and quotes every word that reaches it', async () => {
    // The binding carry-over from Part C's review, in the two layers Parts D and
    // F give it. The CLI's argv is built from a project name, a checkout and a
    // config path, and `isSafeInstallationValue` deliberately admits `$`, a
    // backtick, `;`, `|`, `&`, `(`, `)`, quotes and a space in them. With
    // `shell: false` and an array argv those are harmless; inside a `sh -c`
    // script they are a subshell, a word break and a command separator.
    //
    // Layer 1 (Part D): such a word never reaches the script at all -- `planFor`
    // refuses it before any process exists, and that refusal wins.
    const hostile = '/tmp/a b/$(id)/`id`;rm -rf/x|y&z(q)w\'e"r'
    const directory = freshDirectory()
    const wsl = installWslStub(join(directory, 'windows'), {
      distros: TWO,
      scenario: { commands: { 'budget show': { stdout: '{"ok":true}\n' } } },
    })
    const planWith = (configPath: string): SpawnPlan =>
      planFor('budget.show', {}, {
        target: { project: PROJECT, configPath },
        binaryPath: '',
        directory: wsl.stub.directory,
        appEnv: {},
        wsl: { distro: 'Ubuntu', directory: wsl.stub.directory, executable: wsl.binary },
      })
    for (const word of [hostile, '/tmp/a\\b', '/tmp/a\nb', 'a\u0000b']) {
      expect(() => planWith(word), JSON.stringify(word)).toThrow()
    }
    expect(wsl.calls()).toBe(0)

    // Layer 2 (Part F): the words that do pass are still each single-quoted in
    // the script, with the one quoting function there is -- so a later widening
    // of layer 1 does not by itself open a shell injection.
    expect(shellQuoteWord(hostile)).toBe(`'${hostile.replaceAll("'", `'\\''`)}'`)
    expect(() => shellQuoteWord('a\u0000b')).toThrow(/NUL/)
    const plan = planWith('/home/dimi/.config/fugaro/config.toml')
    expect(plan.wrapper?.words[2]).toContain(`'--config' '/home/dimi/.config/fugaro/config.toml'`)
    const result = await runToCompletion(plan, { timeoutMs: 10_000 })
    expect(result.code).toBe(0)
    // The child received the word as one argument: the report is the child's
    // own account of the argv it was handed.
    const child = only(wsl.children())
    expect(child.argv[0]).toBe('budget')
    expect(child.argv.filter((word) => word === '/home/dimi/.config/fugaro/config.toml')).toHaveLength(1)
  })

  it('sends the stop as an argv array with shell false, and refuses a pid line it cannot trust', async () => {
    // The stop is spawned like every other child (rule 1): the distribution is
    // its own argv word, `-e` separates it from the distribution's, and
    // `shell: false` is what keeps a word from being re-parsed. The pid comes
    // off the wrapper's stderr line, and a line that does not hold digits yields
    // **no** stop -- `sh -c` is handed a command line, so an unvalidated value
    // there is the injection surface `shellQuoteWord` closes for the CLI's argv.
    const directory = freshDirectory()
    const plan = planFor('cli.version', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', directory, executable: '/usr/bin/wsl.exe' },
    })

    const calls: { command: string; args: readonly string[]; options: SpawnOptionsLike }[] = []
    const child = scriptedChild()
    const spawner = ((command: string, args: readonly string[], options: SpawnOptionsLike) => {
      calls.push({ command, args, options })
      return child
    }) as ChildSpawner

    const timers = manualTimers()
    const pending = runToCompletion(plan, { spawner, timers, timeoutMs: 100, shutdownGraceMs: 50 })
    // The wrapper's own line, as the shell prints it: the marker and the pid it
    // is about to become, on stderr.
    child.emitStderr(`${wrapperMarkerOf(plan)} 4321\n`)
    const refusal = pending.then(
      () => {
        throw new Error('the read was expected to be refused')
      },
      (error: SpawnTimeout) => error,
    )
    timers.fire()
    // The pid was read, so there is no residual to state: the stop below names
    // it.
    expect((await refusal).message).not.toContain('may still be running')
    timers.fire()

    expect(calls).toHaveLength(2)
    const stop = calls[1]
    expect(stop?.command).toBe('/usr/bin/wsl.exe')
    expect(stop?.args).toEqual(['-d', 'Ubuntu', '-e', 'sh', '-c', 'kill -TERM 4321'])
    expect(stop?.options.shell).toBe(false)
    expect(stop?.options.cwd).toBe(directory)
    // Nothing was read from a file and nothing is left behind: the pid is a
    // number the console parsed, and there is no path in the stop at all.
    expect(stop?.args.join(' ')).not.toContain('/')

    // A line that says something else is not a pid: no stop at all, and the
    // residual is stated instead of a word becoming shell syntax.
    const hostile = scriptedChild()
    const hostileCalls: string[][] = []
    const hostileSpawner = ((command: string, args: readonly string[]) => {
      hostileCalls.push([command, ...args])
      return hostile
    }) as ChildSpawner
    const secondTimers = manualTimers()
    const secondPending = runToCompletion(plan, {
      spawner: hostileSpawner,
      timers: secondTimers,
      timeoutMs: 100,
      shutdownGraceMs: 50,
    })
    hostile.emitStderr(`${wrapperMarkerOf(plan)} 4321; rm -rf /\n`)
    const secondRefusal = secondPending.then(
      () => {
        throw new Error('the read was expected to be refused')
      },
      (error: SpawnTimeout) => error,
    )
    secondTimers.fire()
    secondTimers.fire()
    expect(hostileCalls).toHaveLength(1)
    // The line is not a pid, so the window is the whole of the cancellation and
    // the sentence says so -- refused at the grace, after the second read also
    // found no number to name.
    expect((await secondRefusal).message).toContain('may still be running')
  })

  it('reads the wrapper’s line through a UTF-16LE warning, and stops the pid it names', async () => {
    // SECURITY-SENSITIVE, and the first defect the review of `#981` found. A
    // `wsl.exe` that prints its own relay warning in UTF-16LE -- U9's build,
    // where the `WSL_UTF8` switch is ignored -- puts `\r\0\n\0` at the end of
    // the warning, so a reader that split on `\n` looked at a line *beginning
    // with a NUL* and missed the wrapper's line entirely. What that cost, in the
    // console's own facts: a CLI that started was reported `wrapperStarted` false
    // (so `probeVersion` reads `wsl.exe`'s state as the installation's), the
    // exit status was nulled, and a cancellation named no pid -- which is the
    // orphan inside the distribution F24 is about.
    const directory = freshDirectory()
    const plan = planFor('cli.version', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', executable: '/usr/bin/wsl.exe' },
    })
    const calls: string[][] = []
    const child = scriptedChild()
    const spawner = ((command: string, args: readonly string[]) => {
      calls.push([command, ...args])
      return child
    }) as ChildSpawner
    const timers = manualTimers()
    const pending = runToCompletion(plan, { spawner, timers, timeoutMs: 100, shutdownGraceMs: 50 })
    // The bytes the warning puts on the pipe, as this console's UTF-8 read sees
    // them, and then the wrapper's own line -- which the distribution's `printf`
    // writes as UTF-8.
    const utf16 = Buffer.from('wsl: a relay warning\r\n', 'utf16le').toString('utf8')
    child.emitStderr(`${utf16}${wrapperMarkerOf(plan)} 4321\n`)
    const refusal = pending.then(
      () => {
        throw new Error('the read was expected to be refused')
      },
      (error: SpawnTimeout) => error,
    )
    timers.fire()
    timers.fire()
    const refused = await refusal
    // A child inside the distribution started, so the timeout is the CLI's state
    // and not "WSL is not installed" (T-F1, F26).
    expect(refused.wrapperStarted).toBe(true)
    // ...and the pid was read off the line the NULs were in front of, so the stop
    // reaches inside the distribution instead of being a residual.
    expect(calls[1]).toEqual(['/usr/bin/wsl.exe', '-d', 'Ubuntu', '-e', 'sh', '-c', 'kill -TERM 4321'])
    expect(refused.message).not.toContain('may still be running')
  })

  it('sends no stop for a pid a line the CLI wrote names', async () => {
    // SECURITY-SENSITIVE, and the second defect the review of `#981` found: the
    // reviewer's probe. The line read off stderr becomes a `kill -TERM <pid>` run
    // inside the distribution **as the operator's user** (`wslStopArgs`), so a
    // marker a CLI can print is a kill a CLI can aim: the probe was
    // `<utf16 warning><marker> 4242\n<marker> 1\n` against the fixed sentence,
    // and it named pid 1 inside the distribution.
    //
    // The fix is the nonce, and this is the case that proves it: the CLI echoes
    // the exact text the console used to accept, twice, with two pids -- and
    // neither is named. The mutation this catches: match the prefix rather than
    // the plan's own marker (drop the nonce), and `calls` grows a second entry.
    const directory = freshDirectory()
    const plan = planFor('cli.version', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', executable: '/usr/bin/wsl.exe' },
    })
    const calls: string[][] = []
    const child = scriptedChild()
    const spawner = ((command: string, args: readonly string[]) => {
      calls.push([command, ...args])
      return child
    }) as ChildSpawner
    const timers = manualTimers()
    const pending = runToCompletion(plan, { spawner, timers, timeoutMs: 100, shutdownGraceMs: 50 })
    const utf16 = Buffer.from('Windows Subsystem for Linux has no installed distributions.\r\n', 'utf16le').toString(
      'utf8',
    )
    child.emitStderr(`${utf16}${WSL_WRAPPER_MARKER_PREFIX} 4242\n${WSL_WRAPPER_MARKER_PREFIX} 1\n`)
    const refusal = pending.then(
      () => {
        throw new Error('the read was expected to be refused')
      },
      (error: SpawnTimeout) => error,
    )
    timers.fire()
    timers.fire()
    const refused = await refusal
    // One child, and it is the CLI's: no stop went into the distribution at all.
    expect(calls).toHaveLength(1)
    // A forged line is not the wrapper's, so nothing started as far as this
    // console can tell, and the refusal says what it could not do rather than
    // naming a process the CLI chose.
    expect(refused.wrapperStarted).toBe(false)
    expect(refused.message).toContain('may still be running')
  })

  it('stops a feed inside the distribution too, on the pid the wrapper reported', async () => {
    // T-F4 for the supervisor's own stop, which is the path `LiveFeed.stop()`
    // and app quit take: the same second half as the one-shot read's, and the
    // pid comes off the same line -- this child's line, because a restarted
    // feed has a new child and the head is cleared with the stderr it came on.
    const directory = freshDirectory()
    const plan = planFor('live.watch', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', executable: '/usr/bin/wsl.exe' },
    })
    const calls: string[][] = []
    const child = scriptedChild()
    const timers = manualTimers()
    const notices: string[] = []
    const sidecar = startSidecar(
      plan,
      {
        onFrame: () => undefined,
        onNotice: (notice) => notices.push(notice.text),
        onRestart: () => undefined,
        onEnded: () => undefined,
      },
      {
        spawner: ((command: string, args: readonly string[]) => {
          calls.push([command, ...args])
          return child
        }) as ChildSpawner,
        timers,
      },
    )
    child.emitStderr(`${wrapperMarkerOf(plan)} 4321\n`)
    const stopping = sidecar.stop()
    expect(child.killed).toEqual(['SIGTERM'])
    timers.fire()
    expect(child.killed).toEqual(['SIGTERM', 'SIGKILL'])
    expect(calls[1]).toEqual(['/usr/bin/wsl.exe', '-d', 'Ubuntu', '-e', 'sh', '-c', 'kill -TERM 4321'])
    // The pid was read, so there is no residual to state.
    expect(notices).toEqual([])
    child.close(null, 'SIGTERM')
    expect((await stopping).reason).toBe('stopped')

    // A feed whose wrapper reported no pid: the stop still happens on the
    // Windows side, and the operator is told what could not be reached.
    const other = scriptedChild()
    const otherCalls: string[][] = []
    const otherNotices: string[] = []
    const secondTimers = manualTimers()
    const second = startSidecar(
      plan,
      {
        onFrame: () => undefined,
        onNotice: (notice) => otherNotices.push(notice.text),
        onRestart: () => undefined,
        onEnded: () => undefined,
      },
      {
        spawner: ((command: string, args: readonly string[]) => {
          otherCalls.push([command, ...args])
          return other
        }) as ChildSpawner,
        timers: secondTimers,
      },
    )
    const secondStopping = second.stop()
    secondTimers.fire()
    expect(other.killed).toEqual(['SIGTERM', 'SIGKILL'])
    expect(otherCalls).toHaveLength(1)
    expect(otherNotices.join(' ')).toContain('may still be running')
    other.close(null, 'SIGTERM')
    await secondStopping
  })

  it('stops the feed’s process inside the distribution when `wsl.exe` is already gone', async () => {
    // The supervisor's stop and the one-shot read's share one condition, and
    // this is the case that holds them to it. `wsl.exe` dies from the
    // `SIGTERM` -- on Windows, `TerminateProcess`, within a tick -- so the
    // child is null by the time the grace fires, and a stop that returned there
    // would leave the CLI running inside the distribution while the screen said
    // the feed had stopped.
    const directory = freshDirectory()
    const plan = planFor('live.watch', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', executable: '/usr/bin/wsl.exe' },
    })
    const calls: string[][] = []
    const child = scriptedChild()
    const timers = manualTimers()
    const sidecar = startSidecar(
      plan,
      {
        onFrame: () => undefined,
        onNotice: () => undefined,
        onRestart: () => undefined,
        onEnded: () => undefined,
      },
      {
        spawner: ((command: string, args: readonly string[]) => {
          calls.push([command, ...args])
          return child
        }) as ChildSpawner,
        timers,
      },
    )
    child.emitStderr(`${wrapperMarkerOf(plan)} 4321\n`)
    const stopping = sidecar.stop()
    expect(child.killed).toEqual(['SIGTERM'])
    child.close(143, 'SIGTERM')
    timers.fire()
    // The Windows-side child is gone, so there is nothing to escalate to --
    // and the stop inside the distribution is sent all the same.
    expect(child.killed).toEqual(['SIGTERM'])
    expect(calls[1]).toEqual(['/usr/bin/wsl.exe', '-d', 'Ubuntu', '-e', 'sh', '-c', 'kill -TERM 4321'])
    expect((await stopping).reason).toBe('stopped')
  })

  it('names a pid that arrives during the feed’s grace, and notices no window', async () => {
    // The supervisor's half of the cold-distribution case: the feed is stopped
    // while the distribution is still starting, and the wrapper's line lands
    // inside `SHUTDOWN_GRACE_MS`. The pid is read at the grace, not at the
    // stop: reading it once would skip the one `wsl.exe` that stops the
    // process, and would tell the operator the process "may still be running"
    // after the console had in fact killed it.
    const directory = freshDirectory()
    const plan = planFor('live.watch', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory,
      appEnv: {},
      wsl: { distro: 'Ubuntu', executable: '/usr/bin/wsl.exe' },
    })
    const calls: string[][] = []
    const child = scriptedChild()
    const timers = manualTimers()
    const notices: string[] = []
    const sidecar = startSidecar(
      plan,
      {
        onFrame: () => undefined,
        onNotice: (notice) => notices.push(notice.text),
        onRestart: () => undefined,
        onEnded: () => undefined,
      },
      {
        spawner: ((command: string, args: readonly string[]) => {
          calls.push([command, ...args])
          return child
        }) as ChildSpawner,
        timers,
      },
    )
    const stopping = sidecar.stop()
    expect(child.killed).toEqual(['SIGTERM'])
    child.emitStderr(`${wrapperMarkerOf(plan)} 4321\n`)
    timers.fire()
    expect(calls[1]).toEqual(['/usr/bin/wsl.exe', '-d', 'Ubuntu', '-e', 'sh', '-c', 'kill -TERM 4321'])
    expect(calls).toHaveLength(2)
    expect(notices).toEqual([])
    child.close(null, 'SIGTERM')
    expect((await stopping).reason).toBe('stopped')
  })

  it('never terminates a distribution: `--terminate` is refused', () => {
    // D6(b) of docs/console-windows/implementation-plan.md. `wsl.exe
    // --terminate <distro>` would end every process in the distribution,
    // including the operator's own shells and anything else they have running
    // there: a console that can do that can destroy work it does not own, for a
    // cancellation it was not even asked to make. The one `wsl.exe` this route
    // sends beyond the spawn is the stop, and it names a single pid.
    // Comments are stripped first, and only then is the scan a scan of code:
    // this test's own subject is *named* in the comments beside the stop, which
    // is the opposite of a refusal being violated -- the rule is that no
    // `wsl.exe` is ever handed the word.
    const codeOnly = (source: string): string =>
      source.replace(/\/\*[\s\S]*?\*\//g, '').split('\n').map((line) => line.replace(/\/\/.*$/, '')).join('\n')
    for (const file of ['spawn.ts', 'wsl.ts']) {
      const code = codeOnly(readFileSync(join(SOURCE_ROOT, 'main', file), 'utf8'))
      expect(code, `${file} terminates a distribution`).not.toContain('--terminate')
      expect(code, `${file} shuts WSL down`).not.toContain('--shutdown')
    }
  })

  it('names the stop’s own words for wsl.exe, never for the CLI', () => {
    // `wslStopArgs` is the second invocation's whole argv, and the pid is a
    // **number**: the value comes off the wrapper's own stderr line, and a line
    // that said anything else would be shell syntax in a `sh -c` command line.
    expect(wslStopArgs({ distro: 'Ubuntu-22.04', pid: 4321 })).toEqual([
      '-d',
      'Ubuntu-22.04',
      '-e',
      'sh',
      '-c',
      'kill -TERM 4321',
    ])
    // ...and the CLI's own surface assertion refuses these words by name: they
    // are `wsl.exe`'s, and a design that put them in the CLI's argv would have
    // to widen `SPAWN_FLAGS` to admit them.
    for (const word of wslStopArgs({ distro: 'Ubuntu', pid: 1 }).filter((word) => word.startsWith('-'))) {
      expect([...WSL_PREFIX_FLAGS, WSL_SHELL_FLAG], word).toContain(word)
      expect(SPAWN_FLAGS, word).not.toContain(word)
    }
    // The distribution is held to the plan's own rules (Part D's alphabet, then
    // `assertWslPrefix`), and the pid to a positive integer: a stop that would
    // name anything else is refused rather than sent.
    for (const distro of ['-sneaky', 'My Linux', 'Ubuntu;x', '$(id)', '']) {
      expect(() => wslStopArgs({ distro, pid: 1 }), distro).toThrow(/not a WSL distribution name/)
    }
    for (const pid of [0, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() => wslStopArgs({ distro: 'Ubuntu', pid }), String(pid)).toThrow(/not a process id/)
    }
  })

  it('reads the wrapper’s line wherever wsl.exe puts it on the pipe', () => {
    // T-F1/T-F4 and the reason this is not a `startsWith`: `wsl.exe` prints its
    // own relay warnings on stderr before the distribution's command runs, so
    // the wrapper's line is regularly not the first thing on the pipe. A reader
    // that insisted on offset zero would call a shell that did start a shell
    // that did not, and would leave the console's own sentence -- pid and all --
    // on the operator's screen.
    const marker = SOME_MARKER
    const line = `${marker} 4321`
    expect(wrapperPidIn(`${line}\n`, marker)).toBe(4321)
    expect(wrapperPidIn(`wsl: a relay warning\nthe CLI said something\n${line}\n`, marker)).toBe(4321)
    // A CRLF from a relay that translated line endings is the same line.
    expect(wrapperPidIn(`${line}\r\n`, marker)).toBe(4321)
    // A line the CLI printed that merely *contains* the text is the CLI's own:
    // only a line that starts with the marker is the wrapper's.
    expect(wrapperPidIn(`the CLI said: ${line}\n`, marker)).toBeNull()
    // No line at all is no pid -- and that is a residual, not a guess.
    expect(wrapperPidIn('the CLI said something\n', marker)).toBeNull()
    // A pid that is not a plain positive integer is not a pid: `sh -c` is handed
    // a command line, so this is the injection surface.
    expect(wrapperPidIn(`${marker} 4321; rm -rf /\n`, marker)).toBeNull()
    expect(wrapperPidIn(`${marker} 0\n`, marker)).toBeNull()
    expect(wrapperPidIn(`${marker} -4321\n`, marker)).toBeNull()
    expect(wrapperPidIn(`${marker} 99999999999\n`, marker)).toBeNull()
    expect(wrapperPidIn(`${marker}\n`, marker)).toBeNull()
    // **A line for another marker is another spawn's line, and this is the
    // guard the whole fix turns on** (review of `#981`): a line the CLI wrote --
    // the fixed prefix, this console's own words, or a nonce that is not this
    // spawn's -- is not the shell's line, so it names no pid. The mutation this
    // catches: match the prefix instead of the marker (or drop the nonce from
    // the marker), and the forged pid below becomes a pid the console kills.
    expect(wrapperPidIn(`${WSL_WRAPPER_MARKER_PREFIX} 4242\n`, marker)).toBeNull()
    expect(wrapperPidIn(`${WSL_WRAPPER_MARKER_PREFIX} ${SOME_OTHER_NONCE} 4242\n`, marker)).toBeNull()
    // The first matching line is the one read, and the second is not consulted:
    // the wrapper prints one line, before `exec`, so a second is the CLI's own.
    expect(wrapperPidIn(`${line}\n${marker} 9999\n`, marker)).toBe(4321)
  })

  it('reads the wrapper’s line through the UTF-16LE warnings a wsl.exe leaves on the pipe', () => {
    // SECURITY-SENSITIVE, and the defect the review of `#981` found (review
    // finding 1). `WSL_UTF8=1` is set on every `wsl.exe` call, but U9 records
    // that the switch is not honoured on every build: on one that is not, this
    // console reads a warning that is UTF-16LE as UTF-8, which puts a NUL
    // between every pair of characters and turns its `\r\n` into `\r\0\n\0`.
    // Before the fix the wrapper's line was then missed entirely -- a started
    // CLI was reported `wrapperStarted=false`, its status was nulled, and a
    // cancellation named no pid -- which is what this case holds shut.
    const marker = SOME_MARKER
    const line = `${marker} 4321`
    // The bytes a UTF-16LE warning puts on the pipe, as this console reads them.
    const warning = (text: string): string => Buffer.from(text, 'utf16le').toString('utf8')
    // A warning with its own line ending: the NUL after the `\n` is what used to
    // land in front of the wrapper's line.
    const withEnding = `${warning('wsl: a relay warning\r\n')}${line}\n`
    expect(withEnding).toContain('\u0000')
    expect(wrapperPidIn(withEnding, marker)).toBe(4321)
    // A warning with **no** trailing newline: its last character's NUL is the
    // only boundary in front of the marker, and the two are one line.
    const glued = `${warning('wsl: a relay warning')}${line}\n`
    expect(wrapperPidIn(glued, marker)).toBe(4321)
    // A leading BOM is a boundary too: a `wsl.exe` answer that starts with one
    // must not hide the line behind it.
    expect(wrapperPidIn(`${'\uFEFF'}${line}\n`, marker)).toBe(4321)
    // And what the operator reads is the warning `wsl.exe` meant to print, with
    // the console's own line gone and the NULs with it.
    expect(withoutWrapperMarker(withEnding, marker)).toBe('wsl: a relay warning\r\n')
    expect(withoutWrapperMarker(glued, marker)).toBe('wsl: a relay warning')
  })

  it('removes the wrapper’s line from the sentence an operator reads', () => {
    const marker = SOME_MARKER
    const line = `${marker} 4321`
    // The wrapper's line goes, wherever it is, and wsl.exe's own warning -- which
    // is wsl.exe's and not the console's -- stays.
    expect(withoutWrapperMarker(`${line}\nthe CLI said no\n`, marker)).toBe('the CLI said no\n')
    expect(withoutWrapperMarker(`wsl: a relay warning\n${line}\nthe CLI said no\n`, marker)).toBe(
      'wsl: a relay warning\nthe CLI said no\n',
    )
    expect(withoutWrapperMarker(`wsl: a relay warning\r\n${line}\r\nthe CLI said no\r\n`, marker)).toBe(
      'wsl: a relay warning\r\nthe CLI said no\r\n',
    )
    // A CLI line that merely contains the text keeps its own text.
    expect(withoutWrapperMarker(`the CLI said: ${line}\n`, marker)).toBe(`the CLI said: ${line}\n`)
    // A line for another marker is not the console's and stays on the screen --
    // which is the visible half of "a second line is ignored", and the reason a
    // CLI cannot use the console's own words to hide its output.
    const forged = `${WSL_WRAPPER_MARKER_PREFIX} 4242\n`
    expect(withoutWrapperMarker(forged, marker)).toBe(forged)
    expect(withoutWrapperMarker(`${line}\n${line}\n`, marker)).toBe(`${line}\n`)
    // Nothing to remove: what arrived is what comes back, byte for byte.
    expect(withoutWrapperMarker('the CLI said no\n', marker)).toBe('the CLI said no\n')
    expect(withoutWrapperMarker('', marker)).toBe('')
    // ...and a plan with no wrapper at all (the host route) has no marker to look
    // for, so its stderr is handed back untouched rather than rewritten: this
    // console wrote nothing into that stream.
    expect(withoutWrapperMarker('the CLI said \u0000 no\n', null)).toBe('the CLI said \u0000 no\n')
  })

  it('cleans the UTF-16LE of a wsl.exe that never started a shell', () => {
    // The case where there is **no line to remove and still text to read** (review
    // of `#981`): `wsl.exe` failed before the shell ran -- the no-distro failure,
    // `wrapperStarted === false` -- so its whole stderr is its own warnings, and
    // on a build that ignores `WSL_UTF8` every byte of them is UTF-16LE. This
    // text is the whole answer an operator gets: `probeVersion` turns it into its
    // reason and the picker shows the same sentence, both through `cliSentence`,
    // which strips no control characters of its own, so the NULs are what the
    // operator would read. The mutation this catches: hand `stderr` back untouched
    // when no line matched.
    const failed = Buffer.from('Windows Subsystem for Linux has no installed distributions.\r\n', 'utf16le').toString(
      'utf8',
    )
    expect(failed).toContain('\u0000')
    expect(withoutWrapperMarker(failed, SOME_MARKER)).toBe(
      'Windows Subsystem for Linux has no installed distributions.\r\n',
    )
    // A leading BOM goes the same way, with nothing to remove behind it.
    expect(withoutWrapperMarker(`${'\uFEFF'}wsl: a warning\n`, SOME_MARKER)).toBe('wsl: a warning\n')
  })
}, 30_000)
