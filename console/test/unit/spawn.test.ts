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

import { chmodSync, lstatSync, mkdirSync, mkdtempSync, realpathSync, rmSync, statSync, symlinkSync, writeFileSync } from 'node:fs'
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
  PICKER_COMMANDS,
  PICKER_OPERATION_NAMES,
  PLATFORM_LOCATIONS,
  SPAWN_COMMANDS,
  WIN32_LOCATIONS,
  argvFor,
  assertArgvFlags,
  assertSpawnSurface,
  binDirectories,
  childEnvironment,
  childPath,
  cliSentence,
  decodeFeedLine,
  isSafeArgumentValue,
  locationsFor,
  parseVersionLine,
  planFor,
  PRIVATE_DIR_LEAF,
  PRIVATE_DIR_NAME,
  privateWorkingDirectory,
  probeVersion,
  resolveBinary,
  resolveHandshakeBinary,
  resolveInstalledBinary,
  runToCompletion,
  restartDecision,
  spawnCommandFor,
  spawnedArgv,
  startSidecar,
  DEFAULT_BACKOFF,
} from '../../src/main/spawn'
import type {
  ChildSpawner,
  SpawnCommand,
  SpawnPlan,
  SpawnedChildLike,
  SpawnOptionsLike,
  Timers,
  WslRoute,
} from '../../src/main/spawn'
import { createSpawnLog, disabledSpawnLog } from '../../src/main/spawnlog'
import type { SpawnLogWriter } from '../../src/main/spawnlog'
import { validateArguments } from '../../src/main/ipc'
import type { OperationName, ValidatedArguments } from '../../src/main/ipc'
import { CliSource, LOG_WINDOW_MAX_ENTRIES, MAX_LIVE_FEEDS, createInstallationStore, parseLogEntries } from '../../src/source/cli'
import type { InstallationCandidate } from '../../src/source/cli'
import { RUN_LOG_CAP, buildRunModel } from '../../src/renderer/screens/run'
import { SourceRefusal, handlersFor } from '../../src/source/source'
import { installStub } from './stub'
import type { Stub } from './stub'
import { DISTRO_PATH, installWslStub } from './wslstub'
import type { WslFixture, WslStub } from './wslstub'

const SOURCE_ROOT = fileURLToPath(new URL('../../src', import.meta.url))
const PROJECT = 'fugaro'

/**
 * COMMANDS_TAKING_PROJECT is the plan's own list, hard-coded: the ten
 * operations `addCloudFlags` gives `--project` to (`internal/cli/cloud.go:51`,
 * called from each of those constructors), including the two log operations
 * that share the `logs` command. The complement -- the commands that take no
 * flag at all -- is the *module's* `COMMANDS_WITHOUT_PROJECT`, keyed by the
 * command words the CLI itself uses (`version`, and the picker's `config list`),
 * which is the same list `assertSpawnSurface` holds the table to; this file
 * imports it rather than spelling it twice.
 *
 * `installation.list` is a *spawn* row the console's own operation table does
 * not have -- its `OperationName` is the dashboard's -- so the list below names
 * it as itself plus that one name.
 */
const COMMANDS_TAKING_PROJECT: readonly (OperationName | 'installation.list')[] = [
  'live.watch',
  'budget.show',
  'spend.report',
  'runs.list',
  'run.diagnose',
  'run.logs',
  'run.logLink',
  'secrets.inventory',
  'accounts.inventory',
  'environment.report',
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

async function waitFor(predicate: () => boolean, ms = 4000): Promise<void> {
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

  it('has the picker’s own read as its own row, and the two tables agree', () => {
    // The picker's table is exactly one row -- the local `fugaro config list
    // --json` (`console/docs/json-contract.md` §6.8), which is how the console
    // discovers the installations on this machine without asking the operator
    // anything -- and `assertSpawnSurface` is what holds it to that.
    expect([...PICKER_COMMANDS].map((row) => [row.operation, row.words.join(' ')])).toEqual([
      ['installation.list', 'config list --json'],
    ])
    expect([...PICKER_OPERATION_NAMES]).toEqual(['installation.list'])
    const first = PICKER_COMMANDS[0]
    expect(first?.words.slice(0, 2).join(' ')).toBe(INSTALLATION_LIST_COMMAND)
    // No `--project`, no `--dir`, no arguments: it is a local read that needs
    // no project selected and opens no bucket (`internal/cli/config_list.go`).
    expect(COMMANDS_WITHOUT_PROJECT).toContain(INSTALLATION_LIST_COMMAND)
    expect(() => {
      assertSpawnSurface(SPAWN_COMMANDS, PICKER_COMMANDS)
    }).not.toThrow()
    expect([...argvFor('installation.list', {}, { target: { project: 'acme' }, directory: '/tmp' })]).toEqual([
      'config',
      'list',
      '--json',
    ])
    // ...and the read that names an installation the picker never asked for is
    // refused, so the table cannot grow a second command without this failing.
    expect(() => {
      assertSpawnSurface(SPAWN_COMMANDS, [
        { operation: 'installation.list', words: ['config', 'show', '--json'], takesProject: false, args: [], dirFlag: false },
      ])
    }).toThrow(/config show/)
    expect(() => {
      assertSpawnSurface(SPAWN_COMMANDS, [
        { operation: 'config.list', words: ['config', 'list', '--json'], takesProject: false, args: [], dirFlag: false },
      ] as never)
    }).toThrow(/picker/)
  })

  it('fixes the design allow-list’s dashboard half: eleven argvs, ten commands, nine of them with --project', () => {
    // The design's §5.1 rule 6 table is the whole allow-list -- twelve argvs
    // since round 5, because the picker's own read (`config list --json`, the
    // case above) is on it too -- and this table is the eleven the dashboard
    // spawns. The two are held in step by hand on purpose: the Go side pins the
    // document's twelve (`internal/cli/docs_desktop_console_test.go`,
    // TestDocsDesktopConsoleDesignAllowListIsExactArgv) and these cases pin the
    // console's eleven plus the picker's one.
    expect([...SPAWN_COMMANDS].map((row) => row.words.join(' '))).toEqual([
      'watch --json',
      'budget show --json',
      'report --json',
      'runs ls --json',
      'diagnose --json',
      'logs --json',
      'logs --url',
      'secrets ls --json',
      'accounts ls --json',
      'version',
      'doctor --json',
    ])
    // The nine *commands* that take the flag (addCloudFlags, internal/cli/
    // cloud.go:51) are nine rows plus one: `logs` has two rows, `--json` and
    // `--url`, and neither of them may be the one that forgets --project.
    const commandsWithProject = SPAWN_COMMANDS.filter((row) => row.takesProject).map((row) =>
      row.words.filter((word) => !word.startsWith('--')).join(' '),
    )
    expect(new Set(commandsWithProject)).toEqual(
      new Set(['watch', 'budget show', 'report', 'runs ls', 'diagnose', 'logs', 'secrets ls', 'accounts ls', 'doctor']),
    )
    expect(SPAWN_COMMANDS.filter((row) => row.takesProject)).toHaveLength(10)
    // ...and the table's own flags agree with the list the tests above use.
    for (const row of SPAWN_COMMANDS) {
      expect(row.takesProject, row.operation).toBe(COMMANDS_TAKING_PROJECT.includes(row.operation))
    }
  })

  it.each(SPAWN_COMMANDS.map((row) => [row.operation, row] as const))(
    '%s puts --project on its argv, from the chosen installation',
    (_name, row: SpawnCommand) => {
      const argv = argvFor(row.operation, {}, safeContext({ project: 'chosen' }))
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
        expect(COMMANDS_WITHOUT_PROJECT).toContain(row.words.filter((word) => !word.startsWith('--')).join(' '))
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
      const argv = argvFor(row.operation, {}, safeContext())
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
    void runToCompletion(plan, { spawner })
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
  const wslPlan = (argv: readonly string[]): SpawnPlan =>
    Object.freeze({
      operation: 'cli.version' as const,
      command: WSL,
      prefix: Object.freeze(['-d', 'Ubuntu', '--cd', '/home/dimi', '--']),
      argv: Object.freeze([FUGARO_BINARY_NAME, ...argv]),
      cwd: '/home/dimi',
      env: Object.freeze({}),
      project: PROJECT,
      backoff: DEFAULT_BACKOFF,
    })

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
    // flags, and `argv[0]` is `fugaro` -- the bare name the *distribution*
    // resolves, which is the whole point of running the CLI inside it.
    const wsl = wslPlan(['version'])
    expect(wsl.argv[0]).toBe(FUGARO_BINARY_NAME)
    expect([...spawnedArgv(wsl)]).toEqual([WSL, '-d', 'Ubuntu', '--cd', '/home/dimi', '--', 'fugaro', 'version'])
  })

  it('holds the wrapper’s flags to `wsl.exe` and the CLI’s argv to `SPAWN_FLAGS`', () => {
    // The two halves are asserted against two different rules, and that is why
    // they are two fields. `-d` and `--cd` are `wsl.exe`'s; `--cd` is not a flag
    // this console may emit to the CLI, and neither is `--distribution`, so a
    // wrapper flag that leaked into the CLI's argv is refused by name.
    const wsl = wslPlan(['version'])
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
    const plan = wslPlan(['version'])
    void runToCompletion(plan, { spawner })
    expect(spawner.calls).toHaveLength(1)
    const call = spawner.calls[0]
    // The reported program is the plan's `command`. Reading `argv[0]` again --
    // the mutation this case exists for -- would report `fugaro`, a file that
    // does not exist on the machine the console is running on.
    expect(call?.command).toBe(WSL)
    expect(call?.command).not.toBe(plan.argv[0])
    expect(call?.args).toEqual(['-d', 'Ubuntu', '--cd', '/home/dimi', '--', 'fugaro', 'version'])
    expect(call?.options.shell).toBe(false)
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
    void runToCompletion(wslPlan(['version']), { spawner, log })
    expect(seen).toHaveLength(1)
    expect(seen[0]?.argv).toEqual([WSL, '-d', 'Ubuntu', '--cd', '/home/dimi', '--', 'fugaro', 'version'])
  })

  it('supervises a stream through the same wrapper', () => {
    const spawner = recordingSpawner()
    const sidecar = startSidecar(wslPlan(['watch', '--json']), {
      onFrame: () => undefined,
      onNotice: () => undefined,
      onRestart: () => undefined,
      onEnded: () => undefined,
    }, { spawner })
    expect(spawner.calls).toHaveLength(1)
    expect(spawner.calls[0]?.command).toBe(WSL)
    expect(spawner.calls[0]?.args).toEqual(['-d', 'Ubuntu', '--cd', '/home/dimi', '--', 'fugaro', 'watch', '--json'])
    void sidecar.stop()
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
      const plan = planFor(row.operation, {}, safeContext())
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
    const plan = planFor('budget.show', {}, safeContext({
      binaryPath: FUGARO_BINARY_NAME,
      directory: '/Users/owner/AppData/Local/Temp/fugaro-console/cwd',
      wsl: { distro: 'Ubuntu', directory: '/home/dimi/src/fugaro', executable: wsl.binary },
    }))
    // The program the console starts is `wsl.exe` -- here, the fake -- and not
    // the CLI: on this route there is no host path to the CLI at all.
    expect(plan.command).toBe(wsl.binary)
    expect([...plan.prefix]).toEqual(['-d', 'Ubuntu', '--cd', '/home/dimi/src/fugaro', '--'])
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
    expect([...spawnedArgv(plan)]).toEqual([
      wsl.binary,
      '-d',
      'Ubuntu',
      '--cd',
      '/home/dimi/src/fugaro',
      '--',
      'fugaro',
      'budget',
      'show',
      '--json',
      '--project',
      PROJECT,
    ])
  })

  it('gives the CLI inside the distribution Linux’s PATH and exactly CHILD_ENV_KEYS', () => {
    // The credential boundary's near side, on the WSL route: the environment the
    // CLI receives is built by `childEnvironment` and nothing else, and its PATH
    // is a Linux one even though the host is Windows (F5, T-C2).
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
    // Every key is a `CHILD_ENV_KEY` -- the allow-list is the enforcement, and
    // the app's own environment is read for nothing else.
    for (const key of Object.keys(plan.env)) expect(CHILD_ENV_KEYS).toContain(key)
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
    expect([...outside.prefix]).toEqual(['-d', 'Ubuntu', '--'])
    expect([...outside.argv]).not.toContain('--dir')
    expect([...outside.argv]).not.toContain('/Users/owner/AppData/Local/Temp/fugaro-console/cwd')
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
    expect(spawn[0]?.argv).toEqual([
      '-d',
      'Ubuntu',
      '--cd',
      arrangement.distroDirectory,
      '--',
      FUGARO_BINARY_NAME,
      'version',
    ])
    // The host side: `wsl.exe` was started in the directory the console named.
    expect(spawn[0]?.cwd).toBe(arrangement.host)
    // ...and the distribution's side: the CLI's own cwd is the `--cd`.
    const child = only(arrangement.wsl.children())
    expect(child.argv).toEqual(['version'])
    expect(child.cwd).toBe(arrangement.distroDirectory)
  })

  it('runs the path the installation stored inside the distribution, when it stores one', async () => {
    // `binaryPath` under a `distro` is a Linux spelling **inside** the
    // distribution (F15, T-C6): when the record names one, that is the program
    // word, and the distribution's `PATH` is not consulted. The fake runs the
    // absolute path it installed, which is this route's stand-in for the CLI the
    // operator's own record names.
    const arrangement = wslRoot()
    const stored = arrangement.wsl.stub.binary
    const resolution = await resolveInside(arrangement, { binaryPath: stored })
    expect(resolution.ok && resolution.path).toBe(stored)
    const spawn = only(arrangement.wsl.reports().filter((report) => report.kind === 'spawn'))
    expect(spawn.argv.at(-2)).toBe(stored)
    expect(only(arrangement.wsl.children()).argv).toEqual(['version'])
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
    expect(spawn.argv).toEqual([
      '-d',
      'Ubuntu',
      '--cd',
      arrangement.distroDirectory,
      '--',
      FUGARO_BINARY_NAME,
      'version',
    ])
    // The handshake's argv is exactly `version` on this route too: no
    // `--project`, because `fugaro version` takes no flags at all.
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
      reason: 'the binary did not answer `version` in time',
    })
    await waitFor(() => stub.kills().length === 1)
    expect(stub.kills()[0]?.signal).toBe('SIGTERM')
    expect(stub.kills()[0]?.argv).toContain('version')
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
    void runToCompletion(plan, { spawner: recordingSpawner(), log })
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
