// The one child process (desktop-console plan Task 1.3, SECURITY-SENSITIVE;
// design section 3.4 rules 1 to 5 and section 5.1 rules 6 and 8).
//
// The console spawns exactly one binary -- the installed `fugaro` -- and never
// reads a database, a config file or a credential itself. This module is the
// whole of that statement in code: the argv table, the environment the child
// gets, the working directory it gets, the binary resolution order, and the
// reader that turns its stdout into documents. Everything here is decided in the
// main process and every decision is reachable under plain Node, because the
// module imports no Electron: the window (Task 1.1) and the IPC surface
// (Task 1.2) hand it arguments, and `test/unit/spawn.test.ts` drives it against
// a fake `fugaro` executable in a temporary directory -- never the real CLI and
// never the network.
//
// Five rules, and each is a test rather than a comment:
//
//   1. **argv is an array, `shell: false`, and never built from a job-written
//      field** (rule 1). `argvFor` is the only place an argv is built. It reads
//      the operation's *declared* arguments and nothing else -- an undeclared
//      key is refused by name, so an object that arrived from a document cannot
//      smuggle a flag -- and every value is passed through
//      `isSafeArgumentValue`'s conservative character whitelist, so a string a
//      job wrote can never become a command word. Every element is its own array
//      entry, and the spawner is always called with `shell: false`.
//   2. **`--project` goes on every argv that takes it, which is every command
//      except `version`** (rule 3). `SPAWN_COMMANDS` carries `takesProject` per
//      row, and the handshake's argv is exactly `["version"]`: `newVersionCmd`
//      (`internal/cli/root.go`) is `cobra.NoArgs` with no `Flags()` at all, so a
//      console that appended `--project` to it would fail its own version check
//      on the first launch, on every machine -- while a console that left it off
//      `watch` would show another installation's numbers. `--config <path>`
//      follows `--project` whenever the installation declares one, and the value
//      is the installation's, never the ambient environment's.
//   3. **The binary is resolved explicitly** (rule 4): the operator's configured
//      path first, then `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`,
//      `/usr/bin`, and only then `PATH`. The path and the version it reported
//      are the header's ("the resolved absolute path and the version it reports
//      are shown in the header"), and **a candidate that exists but whose version
//      cannot be read is refused rather than skipped**: falling through to the
//      next `fugaro` on the machine would silently change which installation the
//      numbers came from, which is the failure the header exists to prevent.
//   4. **The child's environment is built, not inherited** (rule 5, §5.1 rule
//      8): exactly `PATH`, `HOME`, `TMPDIR`, `LANG`, `LC_ALL`, the five `XDG_*`
//      variables and the chosen installation's `FUGARO_PROJECT` -- and nothing
//      else from the app's own environment. A hostile `AWS_*`,
//      `GOOGLE_APPLICATION_CREDENTIALS`, `ANTHROPIC_API_KEY` or ambient
//      `FUGARO_PROJECT` in the app's environment cannot reach the child, which
//      is asserted by diffing the child's own view of its environment against
//      `CHILD_ENV_KEYS`. **The `PATH` is the resolved binary's own directory
//      first, then every well-known prefix the console resolves against** --
//      because the CLI looks up external tools itself (`doctor` resolves the SDK
//      tools with `exec.LookPath`, `internal/cli/doctor.go:147`), and
//      `terraform`, `cosign` and the rest do not sit beside the `fugaro` binary
//      even under Homebrew. A child given one directory would make `doctor`
//      report those tools missing on a machine that has them. The console's own
//      inherited `PATH` still never reaches the child.
//   5. **The working directory is set explicitly** (rule 5): the installation's
//      checkout directory when it has one, otherwise a private directory the app
//      owns -- never `/`, which is nobody's checkout, and never the operator's
//      home. `doctor` is called with `--dir <that directory>` and **never with
//      `--strict`**, because outside a checkout doctor's `plugin-pin` check is a
//      warning that `--strict` turns into a failure, and that failure is about
//      the checkout rather than about the environment the screen draws.
//
// And the last section of the file is the supervisor Task 0.2 says Option A
// needs (`console/docs/json-contract.md` section 10.11): one child per selected
// installation, restarted with the CLI's own backoff (1 s doubling to 30 s)
// **except after a refusal**, terminated with `SIGTERM` and a `SIGKILL` for a
// reader that has stalled, and read line by line with a rule that matters more
// than any of it: **a stdout line that is not a JSON object is skipped and
// surfaced as a notice**, never parsed and never treated as a crash. The
// degraded mode writes a plain sentence to stdout on the same stream as the
// documents (`runWatchDegraded`, `internal/cli/watch.go`), so that rule is a
// requirement rather than a precaution.
//
// What this module deliberately does not do: it never writes a document
// anywhere, never reads the spawn log back (`src/main/spawnlog.ts` is the only
// writer and there is no reader), and never opens the operator's config. The
// config's *path* is a string on the installation which this module passes to
// the child as `--config`; the CLI is the reader (rule 7), which is also why the
// add-an-installation check in `src/source/cli.ts` is a spawn and not a file
// read.

import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, statSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { dirname, join } from 'node:path'

import { CONSOLE_OPERATIONS, isNeverReachable } from './ipc'
import type { ArgumentKind, OperationName, ValidatedArgument, ValidatedArguments } from './ipc'
import type { SpawnLogWriter } from './spawnlog'

/** JSONObject is a JSON object as it arrived: the CLI's document, undecoded.
 * Decoding it into the console's own shape is Task 2.0's `shared/schema.ts`;
 * nothing in this task reads a field of it except `day`, which the feed tracker
 * needs to spot the UTC-midnight rollover. */
export type JSONObject = Readonly<Record<string, unknown>>

/** FUGARO_BINARY_NAME is the one executable this task spawns. */
export const FUGARO_BINARY_NAME = 'fugaro'

/** PROJECT_FLAG, CONFIG_FLAG and DIR_FLAG are the three argv values this module
 * adds on top of the operation's own flags. */
export const PROJECT_FLAG = '--project'
export const CONFIG_FLAG = '--config'
export const DIR_FLAG = '--dir'

/**
 * WELL_KNOWN_BINARY_PREFIXES is rule 4's order, minus the last step: a
 * configured path first (`SpawnTarget.binaryPath`), then these directories in
 * this order, and only then every entry of the app's own `PATH`. `~/.local/bin`
 * is spelled as a prefix here and joined with the home directory at resolution
 * time, because the home directory is an argument rather than a constant.
 */
export const WELL_KNOWN_BINARY_PREFIXES: readonly string[] = Object.freeze([
  '/opt/homebrew/bin',
  '/usr/local/bin',
  '~/.local/bin',
  '/usr/bin',
])

/**
 * CHILD_PATH_PREFIXES is the well-known *tool* prefixes the child's `PATH` is
 * built from: the resolved binary's own directory first, then these. They are
 * not the same list as `WELL_KNOWN_BINARY_PREFIXES` and the difference is
 * deliberate -- this one answers "where may the CLI find `terraform`?", so it
 * includes `/bin`, `/usr/sbin` and `/sbin`, and it does not include
 * `~/.local/bin` unless the binary itself lives there.
 */
export const CHILD_PATH_PREFIXES: readonly string[] = Object.freeze([
  '/opt/homebrew/bin',
  '/usr/local/bin',
  '/usr/bin',
  '/bin',
  '/usr/sbin',
  '/sbin',
])

/**
 * CHILD_ENV_KEYS is the whole environment the child may receive, and the order
 * is the schema's: the one key this module always writes (`PATH`, rebuilt),
 * the four the operator's own environment supplies when it has them, the five
 * `XDG_*` variables, and `FUGARO_PROJECT`, which is always the chosen
 * installation's. Anything not on this list is not passed -- there is no merge
 * with `process.env` anywhere in this file, and that is the property
 * `test/unit/spawn.test.ts` diffs the child's own report against.
 */
export const CHILD_ENV_KEYS: readonly string[] = Object.freeze([
  'PATH',
  'HOME',
  'TMPDIR',
  'LANG',
  'LC_ALL',
  'XDG_CONFIG_HOME',
  'XDG_CACHE_HOME',
  'XDG_DATA_HOME',
  'XDG_STATE_HOME',
  'XDG_RUNTIME_DIR',
  'FUGARO_PROJECT',
])

/**
 * SPAWN_FLAGS is every flag this module may emit. It is the argv half of the
 * allow-list and it is narrower than the CLI's own set: `--project`, `--config`
 * and `--dir` are the three the console adds from main-process state, `--json`
 * and `--url` are the two the surface fixes (Task 1.2's
 * `ALLOWED_OPERATION_FLAGS`), and the rest are the renderer arguments the design
 * allow-list admits. `argvFor` asserts every emitted flag is on this list, so
 * `--strict`, `--follow`, `--check-github-app`, `--plain` and the other
 * never-reachable flags cannot be emitted by any caller, argument or future
 * edit to a row.
 */
export const SPAWN_FLAGS: readonly string[] = Object.freeze([
  '--json',
  '--url',
  '--project',
  '--config',
  '--dir',
  '--repo',
  '--all',
  '--by',
  '--since',
  '--until',
  '--mine',
  '--pr',
  '--project-wide',
])

/** DEFAULT_BACKOFF is the CLI's own stream backoff (`backMin`/`backMax`,
 * `internal/watch/source.go`), used here for the same reason it is used there:
 * a stream that died should come back quickly once and slowly if it keeps
 * dying. */
export const DEFAULT_BACKOFF: Backoff = Object.freeze({ minMs: 1000, maxMs: 30_000 })

/** SHUTDOWN_GRACE_MS is how long `SIGTERM` gets before `SIGKILL`. Task 0.2
 * section 10.12: the child returns from `watchStream`'s `<-ctx.Done()` case and
 * exits 0 promptly, "not on a tick"; the `SIGKILL` is for the one child that
 * cannot -- a stalled reader blocked inside `Encode` with a full pipe. */
export const SHUTDOWN_GRACE_MS = 2000

/** MAX_STDERR_BYTES bounds what the supervisor keeps of the child's stderr. It
 * is kept only to show the CLI's own sentence verbatim (design section 6.3), so
 * a bound here is a bound on a message, never on a document. */
export const MAX_STDERR_BYTES = 4096

/** PRIVATE_DIR_MODE is the mode of the directory the app owns for a child that
 * has no checkout. See `privateWorkingDirectory`. */
export const PRIVATE_DIR_MODE = 0o700

/** SpawnTarget is what `--project` and `--config` are read from: the
 * installation tuple's two argv-bearing halves (D13). The binary path and the
 * checkout directory are separate arguments because they are not argv values
 * (`binaryPath` is the executable, `directory` is the cwd). */
export interface SpawnTarget {
  readonly project: string
  readonly configPath?: string | undefined
}

/** Backoff is the restart delay schedule, doubling from `minMs` to `maxMs`. */
export interface Backoff {
  readonly minMs: number
  readonly maxMs: number
}

/**
 * SpawnArgumentKind says how one declared argument becomes argv. It is narrower
 * than Task 1.2's `ArgumentKind`, which says what the renderer may *send*: two
 * of those kinds are one string spelling, so a `since` and a `reportSince` are
 * both `value` here, and the only kind whose argv spelling differs is
 * `pullRequest`, where the validator's number has to become the digits the
 * CLI's `IntVar --pr` reads. `SPAWN_KIND_FOR` below is the mapping, and
 * `assertSpawnSurface` refuses a table that disagrees with it -- which is what
 * makes "the validator emits a number here" and "argv wants a string there" a
 * build-time failure rather than a refusal a user sees.
 */
export type SpawnArgumentKind = 'value' | 'boolean' | 'secretScope' | 'pullRequest'

/**
 * SPAWN_KIND_FOR is the one place the two kind vocabularies are related: the
 * operation's declared kind (ipc.ts) to the way that argument reaches argv. It
 * is a `Record<ArgumentKind, …>`, so a new kind in ipc.ts with no entry here is
 * a compile error rather than an argument with no argv spelling.
 */
export const SPAWN_KIND_FOR: Record<ArgumentKind, SpawnArgumentKind> = Object.freeze({
  repository: 'value',
  runId: 'value',
  day: 'value',
  since: 'value',
  reportSince: 'value',
  reportDimension: 'value',
  secretScope: 'secretScope',
  pullRequest: 'pullRequest',
  flag: 'boolean',
  link: 'value',
})

/** SpawnArgument is one declared argument of one command. `flag` is null for a
 * positional (a run id, which always comes last before the console's own
 * flags). */
export interface SpawnArgument {
  readonly key: string
  readonly flag: string | null
  readonly kind: SpawnArgumentKind
}

/**
 * SpawnCommand is one row of the table: the operation it serves, the argv words
 * the CLI's own command path needs, whether the command accepts `--project`,
 * and its declared arguments in the order they are emitted.
 */
export interface SpawnCommand {
  readonly operation: OperationName
  readonly words: readonly string[]
  readonly takesProject: boolean
  readonly args: readonly SpawnArgument[]
  /** `dirFlag` is true for the one command that takes `--dir`: `doctor`. */
  readonly dirFlag: boolean
}

/**
 * SPAWN_COMMANDS is the table, read off the operations Task 1.2 declared and the
 * design's allow-list (§5.1 rule 6). Every operation whose `command` is not null
 * has exactly one row here, and `assertSpawnSurface` fails a table where either
 * side is missing an entry -- so a new read is a new row *and* a new operation,
 * never one without the other.
 *
 * The `takesProject` column is rule 3 and the reason it is per-row: `version`
 * takes no flags at all. `dirFlag` is rule 5 and is per-row for the same kind of
 * reason -- only `doctor` has a `--dir`.
 */
export const SPAWN_COMMANDS: readonly SpawnCommand[] = Object.freeze([
  {
    operation: 'live.watch',
    words: ['watch', '--json'],
    takesProject: true,
    args: [{ key: 'repo', flag: '--repo', kind: 'value' }],
    dirFlag: false,
  },
  {
    operation: 'budget.show',
    words: ['budget', 'show', '--json'],
    takesProject: true,
    args: [
      { key: 'repo', flag: '--repo', kind: 'value' },
      { key: 'all', flag: '--all', kind: 'boolean' },
    ],
    dirFlag: false,
  },
  {
    operation: 'spend.report',
    words: ['report', '--json'],
    takesProject: true,
    args: [
      { key: 'by', flag: '--by', kind: 'value' },
      { key: 'since', flag: '--since', kind: 'value' },
      { key: 'until', flag: '--until', kind: 'value' },
      { key: 'repo', flag: '--repo', kind: 'value' },
    ],
    dirFlag: false,
  },
  {
    operation: 'runs.list',
    words: ['runs', 'ls', '--json'],
    takesProject: true,
    args: [
      { key: 'repo', flag: '--repo', kind: 'value' },
      { key: 'since', flag: '--since', kind: 'value' },
      { key: 'mine', flag: '--mine', kind: 'boolean' },
      { key: 'pr', flag: '--pr', kind: 'pullRequest' },
      { key: 'all', flag: '--all', kind: 'boolean' },
    ],
    dirFlag: false,
  },
  {
    operation: 'run.diagnose',
    words: ['diagnose', '--json'],
    takesProject: true,
    args: [{ key: 'run', flag: null, kind: 'value' }],
    dirFlag: false,
  },
  {
    operation: 'run.logs',
    words: ['logs', '--json'],
    takesProject: true,
    args: [{ key: 'run', flag: null, kind: 'value' }],
    dirFlag: false,
  },
  {
    // Its own row and its own argv, never combined with `--json`: `runLogs`
    // refuses `--url` together with `--json` ("--url prints only the link: it
    // doesn't go with --follow or --json"), so an argv that carried both would
    // be an argv the CLI always rejects.
    operation: 'run.logLink',
    words: ['logs', '--url'],
    takesProject: true,
    args: [{ key: 'run', flag: null, kind: 'value' }],
    dirFlag: false,
  },
  {
    operation: 'secrets.inventory',
    words: ['secrets', 'ls', '--json'],
    takesProject: true,
    args: [
      { key: 'scope', flag: null, kind: 'secretScope' },
      { key: 'repo', flag: '--repo', kind: 'value' },
    ],
    dirFlag: false,
  },
  {
    operation: 'accounts.inventory',
    words: ['accounts', 'ls', '--json'],
    takesProject: true,
    args: [{ key: 'repo', flag: '--repo', kind: 'value' }],
    dirFlag: false,
  },
  {
    // The handshake, and the one row that carries no `--project` (rule 3).
    operation: 'cli.version',
    words: ['version'],
    takesProject: false,
    args: [],
    dirFlag: false,
  },
  {
    operation: 'environment.report',
    words: ['doctor', '--json'],
    takesProject: true,
    args: [],
    dirFlag: true,
  },
] as const satisfies readonly SpawnCommand[])

/**
 * ARGUMENT_RE is the alphabet a *renderer* argument may use. It is deliberately
 * a whitelist rather than a blacklist: a repository is `owner/name`, a run id is
 * `YYYYMMDD-HHMMSS-xxxx`, a day is `YYYY-MM-DD`, `--since` is `Nd`/`3h`/`0` or
 * `report`'s `12w`, `--by` is one of `report`'s seven words -- the schema is
 * what closes that set, and this alphabet only has to be wide enough for it --
 * and a pull request reaches this function as the digits its `IntVar` reads, so
 * every legitimate value is covered -- and whitespace, quotes, `$`, `;`, `|`, `&`, `<`, `>`,
 * `(`, `)`, backticks, backslashes, `*`, `?` and every control character are
 * refused. Nothing a job wrote can be spelled in that alphabet into a shell
 * word, and with `shell: false` there is no shell to spell it for.
 */
const ARGUMENT_RE = /^[A-Za-z0-9._:@/+-]+$/

/**
 * isSafeArgumentValue answers whether one renderer argument may become an argv
 * element. A leading `-` is refused as well as the alphabet: a value that looks
 * like a flag is a value that changes the command.
 */
export function isSafeArgumentValue(value: string): boolean {
  if (value === '' || value.length > 200) return false
  if (value.startsWith('-')) return false
  return ARGUMENT_RE.test(value)
}

/**
 * isSafeInstallationValue answers whether a string the *operator* typed -- a
 * project name, a config path, a checkout directory, a binary path -- may become
 * an argv element or a working directory. It is looser than the argument guard
 * on purpose: a path may contain a space (`/Users/Ann Lee/src/fugaro`), and a
 * space is harmless with `shell: false` and an array argv. What it still refuses
 * is a control character or a NUL, an empty string, something absurdly long, and
 * a leading `-`.
 */
export function isSafeInstallationValue(value: string): boolean {
  if (value === '' || value.length > 4096) return false
  if (value.startsWith('-')) return false
  // eslint-disable-next-line no-control-regex
  return !/[\u0000-\u001f\u007f]/.test(value)
}

function refused(message: string): never {
  throw new Error(message)
}

/**
 * spawnCommandFor returns the row for an operation, and throws for anything
 * else. It is the "refused before spawn" half of the allow-list: a caller that
 * named a subcommand outside the table -- or a name that is not an operation at
 * all -- gets an exception here, before any process exists.
 */
export function spawnCommandFor(operation: string): SpawnCommand {
  const row = SPAWN_COMMANDS.find((candidate) => candidate.operation === operation)
  if (row === undefined) {
    return refused(
      `${operation} is not a console operation this process may spawn: the surface is src/main/ipc.ts's table`,
    )
  }
  return row
}

/**
 * argvFor builds the argument vector for one spawn, and it is the only place one
 * is built. Three things happen here, in this order:
 *
 *   1. the row's own words (`watch --json`);
 *   2. the row's declared arguments, each emitted as its own element -- a
 *      positional as itself, a boolean as its flag, a secret scope as
 *      `--project-wide` (its `--repo R` half is the `repo` argument, and Task
 *      1.2's schema is what makes exactly one of the two present);
 *   3. `--project <name>` and, when the installation declares one,
 *      `--config <path>` -- and then, for `doctor` alone, `--dir <directory>`.
 *
 * An argument key the row does not declare is refused by name rather than
 * ignored: a payload that reached here from a document could otherwise carry a
 * flag of its own. Every emitted flag is then asserted against `SPAWN_FLAGS`, so
 * no row, argument or later edit can emit a flag the console must never reach.
 */
export function argvFor(
  operation: OperationName,
  args: ValidatedArguments,
  context: { readonly target: SpawnTarget; readonly directory: string },
): readonly string[] {
  const row = spawnCommandFor(operation)
  const declared = new Set(row.args.map((spec) => spec.key))
  for (const key of Object.keys(args)) {
    if (!declared.has(key)) {
      refused(`${operation} received ${key}, which it does not declare: only this table's arguments reach argv`)
    }
  }

  const argv: string[] = [...row.words]
  for (const spec of row.args) {
    const value: ValidatedArgument | undefined = args[spec.key]
    if (value === undefined) continue
    switch (spec.kind) {
      case 'boolean': {
        if (typeof value !== 'boolean') refused(`${operation}'s ${spec.key} is not a boolean`)
        if (value && spec.flag !== null) argv.push(spec.flag)
        break
      }
      case 'secretScope': {
        if (value !== 'project-wide' && value !== 'repo') {
          refused(`${operation}'s ${spec.key} is not one of the two scopes`)
        }
        if (value === 'project-wide') argv.push('--project-wide')
        break
      }
      case 'value': {
        if (typeof value !== 'string') refused(`${operation}'s ${spec.key} is not a string`)
        if (!isSafeArgumentValue(value)) {
          refused(`${operation}'s ${spec.key} is not a value this console will pass to the CLI`)
        }
        if (spec.flag !== null) argv.push(spec.flag)
        argv.push(value)
        break
      }
      case 'pullRequest': {
        // The validator hands this over as the *number* it checked (ipc.ts's
        // `pullRequest` kind returns it unchanged), and the CLI's `--pr` is an
        // `IntVar`: the digit spelling is what it reads. The spelling is then
        // put through the same whitelist as every other value, so a number that
        // is not a number -- `NaN`, `Infinity`, `1e21`, `-1`, `1.5` -- is
        // refused here rather than turned into argv.
        if (typeof value !== 'number' || !Number.isInteger(value) || value < 1) {
          refused(`${operation}'s ${spec.key} is not a pull request number`)
        }
        const spelled = String(value)
        if (!isSafeArgumentValue(spelled)) {
          refused(`${operation}'s ${spec.key} is not a value this console will pass to the CLI`)
        }
        if (spec.flag !== null) argv.push(spec.flag)
        argv.push(spelled)
        break
      }
    }
  }

  if (row.takesProject) {
    const project = context.target.project
    if (!isSafeInstallationValue(project)) refused('the installation’s project name is not a value this console will pass')
    argv.push(PROJECT_FLAG, project)
    const configPath = context.target.configPath
    if (configPath !== undefined && configPath !== '') {
      if (!isSafeInstallationValue(configPath)) refused('the installation’s config path is not a value this console will pass')
      argv.push(CONFIG_FLAG, configPath)
    }
  }

  if (row.dirFlag) {
    if (!isSafeInstallationValue(context.directory)) refused('the working directory is not a value this console will pass')
    argv.push(DIR_FLAG, context.directory)
  }

  assertArgvFlags(operation, argv)
  return Object.freeze(argv)
}

/** assertArgvFlags refuses an argv carrying a flag outside `SPAWN_FLAGS`, and one
 * carrying a flag the design's never-table names. Both checks are here rather
 * than in the callers because this function is the only producer of an argv. */
export function assertArgvFlags(operation: string, argv: readonly string[]): void {
  for (const element of argv) {
    if (!element.startsWith('--')) continue
    if (!SPAWN_FLAGS.includes(element)) {
      refused(`${operation} would emit ${element}, which is not a flag this console may spawn`)
    }
    const forbidden = isNeverReachable({ command: null, flags: [element] })
    if (forbidden !== null) {
      refused(`${operation} would emit ${forbidden.value}: ${forbidden.why} (${forbidden.where})`)
    }
  }
}

/**
 * assertSpawnSurface fails a table that does not correspond one-to-one with the
 * operations Task 1.2 declared, drives something on the never-reachable table,
 * declares an argument the operation does not have, omits a flag it does fix, or
 * emits a flag outside `SPAWN_FLAGS`. It takes the table as an argument so the
 * test can hand it the mutation directly -- a row for `budget kill`, a row that
 * drops `takesProject` from `watch` -- instead of the check being reachable only
 * by editing this file. `src/source/cli.ts` calls it with the real table before
 * it resolves a binary, so a console that cannot be built with a widened table
 * cannot be talked into spawning one later.
 */
export function assertSpawnSurface(rows: readonly SpawnCommand[]): void {
  const seen = new Set<string>()
  for (const row of rows) {
    if (seen.has(row.operation)) refused(`${row.operation} has two rows in the spawn table`)
    seen.add(row.operation)

    const operation = CONSOLE_OPERATIONS.find((candidate) => candidate.name === row.operation)
    if (operation === undefined) refused(`${row.operation} is not a console operation`)
    const forbidden = isNeverReachable(operation)
    if (forbidden !== null) {
      refused(`${row.operation} drives ${forbidden.value}, which the console must never reach`)
    }
    if (operation.command === null) {
      refused(`${row.operation} drives no command, so it has no row in the spawn table`)
    }
    const commandWords = operation.command.split(' ')
    for (const [index, word] of commandWords.entries()) {
      if (row.words[index] !== word) {
        refused(`${row.operation}'s argv does not start with ${operation.command}`)
      }
    }
    for (const flag of operation.flags) {
      if (!row.words.includes(flag)) refused(`${row.operation} fixes ${flag} and its argv does not carry it`)
    }
    const declared = new Set<string>(operation.args.map((spec) => spec.key))
    for (const spec of row.args) {
      if (!declared.has(spec.key)) {
        refused(`${row.operation} declares ${spec.key}, which the operation does not accept`)
      }
      // The kind agrees with Task 1.2's schema, through the one mapping that
      // relates the two vocabularies. A row that emitted a `pr` as a string
      // would otherwise pass every check here and refuse a real payload at run
      // time -- in `argvFor`, on the way to a spawn that never happened.
      const argument = operation.args.find((candidate) => candidate.key === spec.key)
      const expected = argument === undefined ? undefined : SPAWN_KIND_FOR[argument.kind]
      if (expected !== spec.kind) {
        refused(
          `${row.operation}'s ${spec.key} is declared ${String(expected)} in the spawn table, but the operation's schema says ${argument?.kind ?? 'nothing'}`,
        )
      }
    }
    assertArgvFlags(row.operation, row.words)
  }
  for (const operation of CONSOLE_OPERATIONS) {
    if (operation.command === null) continue
    if (!seen.has(operation.name)) {
      refused(`${operation.name} has no row in the spawn table: the console would spawn nothing for it`)
    }
  }
}

/**
 * binDirectories is the resolution order of rule 4: the configured path, then
 * the well-known prefixes, then every entry of the app's own `PATH`. It is
 * exported because the order is the rule, and a test that could not read the
 * order could not assert it.
 */
export function binDirectories(options: {
  readonly configuredPath?: string | undefined
  readonly appEnv: Readonly<Record<string, string | undefined>>
  readonly home?: string
  /** `prefixes` replaces `WELL_KNOWN_BINARY_PREFIXES` for a test that cannot
   * create `/opt/homebrew/bin`. It is the *list* that is injectable, never the
   * order: the loop below walks whatever list it is given front to back, and
   * `test/unit/spawn.test.ts` asserts the real list separately. */
  readonly prefixes?: readonly string[] | undefined
}): readonly string[] {
  const home = options.home ?? homedir()
  const candidates: string[] = []
  if (options.configuredPath !== undefined && options.configuredPath !== '') {
    candidates.push(options.configuredPath)
  }
  for (const prefix of options.prefixes ?? WELL_KNOWN_BINARY_PREFIXES) {
    const base = prefix.startsWith('~') ? join(home, prefix.slice(2)) : prefix
    candidates.push(join(base, FUGARO_BINARY_NAME))
  }
  const path = options.appEnv['PATH'] ?? ''
  for (const entry of path.split(':')) {
    if (entry !== '') candidates.push(join(entry, FUGARO_BINARY_NAME))
  }
  return Object.freeze(dedupe(candidates))
}

function dedupe(values: readonly string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const value of values) {
    if (seen.has(value)) continue
    seen.add(value)
    out.push(value)
  }
  return out
}

/** isExecutableFile answers whether a path is a regular file with an execute
 * bit. A directory named `fugaro` is not a candidate, and neither is a path the
 * process may not stat. */
export function isExecutableFile(path: string): boolean {
  try {
    if (!existsSync(path)) return false
    const stat = statSync(path)
    if (!stat.isFile()) return false
    return (stat.mode & 0o111) !== 0
  } catch {
    return false
  }
}

/**
 * parseVersionLine reads `fugaro version`'s one human line (design section 4.3
 * gap 2: there is no `--json` on this tree). The line is `Version`
 * (`internal/cli/root.go`) -- a release tag of a built binary, or the literal
 * `dev` when it was not built with the release `-ldflags` -- so what is accepted
 * is exactly that: `dev`, or an optional `v` followed by a numeric version with
 * the usual optional prerelease and build parts. Anything else -- empty output,
 * a usage message, a stack trace -- is a binary this console refuses to run.
 */
export function parseVersionLine(text: string):
  | { readonly ok: true; readonly version: string }
  | { readonly ok: false; readonly reason: string } {
  const lines = text.split('\n').filter((line) => line.trim() !== '')
  if (lines.length === 0) return { ok: false, reason: 'the binary printed no version line' }
  const line = (lines[0] ?? '').trim()
  if (lines.length !== 1) {
    return { ok: false, reason: 'the binary printed more than one line for its version' }
  }
  if (line.length > 200) return { ok: false, reason: 'the version line is implausibly long' }
  if (line === 'dev') return { ok: true, version: line }
  if (!/^v?\d+(?:\.\d+)*(?:[-+][0-9A-Za-z.-]+)?$/.test(line)) {
    return { ok: false, reason: `the binary printed ${JSON.stringify(line)}, which is not a version` }
  }
  return { ok: true, version: line }
}

/**
 * childEnvironment builds the child's environment: exactly `CHILD_ENV_KEYS`,
 * with `PATH` rebuilt from the resolved binary's directory and the well-known
 * tool prefixes, and `FUGARO_PROJECT` set to the installation's project. The
 * app's own environment is read for `HOME`, `TMPDIR`, `LANG`, `LC_ALL` and the
 * five `XDG_*` variables and for nothing else -- the cloud SDK finds the
 * operator's identity in their home directory exactly as it does in a terminal,
 * and the app never hands it a credential variable (design section 3.1).
 *
 * The returned object has a null prototype and is frozen, so a caller cannot
 * add a key to it after the fact.
 */
export function childEnvironment(options: {
  readonly target: SpawnTarget
  readonly binaryPath: string
  readonly appEnv: Readonly<Record<string, string | undefined>>
}): Readonly<Record<string, string>> {
  const env: Record<string, string> = Object.create(null) as Record<string, string>
  for (const key of CHILD_ENV_KEYS) {
    if (key === 'PATH' || key === 'FUGARO_PROJECT') continue
    const value = options.appEnv[key]
    if (typeof value === 'string' && value !== '') env[key] = value
  }
  env['PATH'] = childPath(options.binaryPath)
  env['FUGARO_PROJECT'] = options.target.project
  return Object.freeze(env)
}

/** childPath is the child's `PATH`, and it is a list rather than one directory
 * -- see rule 4 in this file's header and design section 3.4 rule 5. */
export function childPath(binaryPath: string): string {
  return dedupe([dirname(binaryPath), ...CHILD_PATH_PREFIXES]).join(':')
}

/**
 * privateWorkingDirectory returns the private, empty directory the app owns and
 * spawns from when the installation has no checkout (design section 3.4 rule 5).
 * It is created with mode `0700` under the system temporary directory, with the
 * uid in the name so two users on one machine cannot collide, and it is never
 * `/`, never the app bundle and never the operator's home.
 *
 * **On a fine point of section 5.1 rule 10, said out loud rather than left to be
 * noticed:** that rule calls the spawn log "the only thing the app ever writes
 * to disk", and this creates a directory. The reconciliation is that rule 10 is
 * about *records* -- no history, no cached document, no crash dump, nothing that
 * outlives the process -- and this holds nothing at all: it exists so that
 * `doctor`'s `--dir` is a directory rather than `/`, and an empty directory is
 * the smallest thing that can satisfy "never the app's own default of `/`". A
 * later task that persists the installation list would be a second thing the
 * app writes, and would have to be reconciled with rule 10 explicitly.
 */
export function privateWorkingDirectory(options: {
  readonly base?: string
  readonly uid?: number | null
} = {}): string {
  const base = options.base ?? join(tmpdir(), 'fugaro-console')
  const uid = options.uid ?? (typeof process.getuid === 'function' ? process.getuid() : null)
  const path = join(base, uid === null ? 'cwd' : `cwd-${uid}`)
  mkdirSync(path, { recursive: true, mode: PRIVATE_DIR_MODE })
  const stat = statSync(path)
  if (!stat.isDirectory()) refused(`${path} is not a directory: the console has nowhere to spawn from`)
  return path
}

/** SpawnOptionsLike is the slice of `node:child_process`'s options this module
 * sets, with `shell: false` in the type so no caller can pass `true`. */
export interface SpawnOptionsLike {
  readonly cwd: string
  readonly env: Readonly<Record<string, string>>
  readonly shell: false
  readonly stdio: readonly ['ignore', 'pipe', 'pipe']
}

/** ReadableLike is the slice of a child's stream this module reads. */
export interface ReadableLike {
  on(event: 'data', listener: (chunk: Buffer | string) => void): unknown
}

/** SpawnedChildLike is the slice of `ChildProcess` this module uses, as an
 * interface so the test can pass a recording fake and assert on `shell` and on
 * the argv it was handed. */
export interface SpawnedChildLike {
  readonly stdout: ReadableLike | null
  readonly stderr: ReadableLike | null
  on(event: 'error', listener: (error: Error) => void): unknown
  on(event: 'exit', listener: (code: number | null, signal: string | null) => void): unknown
  kill(signal?: NodeJS.Signals): boolean
}

/** ChildSpawner is `spawn`, narrowed. */
export interface ChildSpawner {
  (command: string, args: readonly string[], options: SpawnOptionsLike): SpawnedChildLike
}

/**
 * nodeSpawner is the real spawner. `shell` is passed as the literal `false` and
 * the arguments as an array, which with the argv table above is rule 1: nothing
 * the console runs goes through a shell, so nothing it runs can be re-parsed.
 */
export const nodeSpawner: ChildSpawner = (command, args, options) =>
  spawn(command, [...args], {
    cwd: options.cwd,
    env: { ...options.env },
    shell: false,
    stdio: ['ignore', 'pipe', 'pipe'],
  }) as unknown as SpawnedChildLike

/**
 * SpawnPlan is one spawn, fully decided: the argv (binary first), the working
 * directory, the built environment, the installation's project for the spawn
 * log, and the restart schedule. Nothing in it is read from a document.
 */
export interface SpawnPlan {
  readonly operation: OperationName
  readonly argv: readonly string[]
  readonly cwd: string
  readonly env: Readonly<Record<string, string>>
  readonly project: string
  readonly backoff: Backoff
}

/** planFor assembles a plan from an operation, its validated arguments and
 * main-process state. It is the only producer of a `SpawnPlan`. */
export function planFor(
  operation: OperationName,
  args: ValidatedArguments,
  context: {
    readonly target: SpawnTarget
    readonly binaryPath: string
    readonly directory: string
    readonly appEnv: Readonly<Record<string, string | undefined>>
    readonly backoff?: Backoff
  },
): SpawnPlan {
  const argv = argvFor(operation, args, { target: context.target, directory: context.directory })
  return Object.freeze({
    operation,
    argv: Object.freeze([context.binaryPath, ...argv]),
    cwd: context.directory,
    env: childEnvironment({
      target: context.target,
      binaryPath: context.binaryPath,
      appEnv: context.appEnv,
    }),
    project: context.target.project,
    backoff: context.backoff ?? DEFAULT_BACKOFF,
  })
}

/** RunResult is a child that was allowed to finish. */
export interface RunResult {
  readonly code: number | null
  readonly signal: string | null
  readonly stdout: string
  readonly stderr: string
}

/** cliSentence turns the CLI's stderr into the sentence a screen shows. The CLI
 * prints `fugaro: <sentence>` (`cmd/fugaro/main.go`), and design section 6.3
 * says a refused read shows the CLI's own text verbatim -- so the prefix is
 * removed and the rest is kept exactly as it arrived, trimmed only of the
 * newline. `null` when the child said nothing. */
export function cliSentence(stderr: string): string | null {
  const text = stderr.trim()
  if (text === '') return null
  const withoutPrefix = text.startsWith('fugaro: ') ? text.slice('fugaro: '.length) : text
  return withoutPrefix.trim() === '' ? null : withoutPrefix.trim()
}

/**
 * runToCompletion spawns a plan and waits for it, returning its status and both
 * streams. It is the one-shot read path (`budget show`, `report`, `runs ls`,
 * `diagnose`, `logs`, `secrets ls`, `accounts ls`, `doctor` and the version
 * handshake); the streaming path is `startSidecar` below.
 */
export function runToCompletion(
  plan: SpawnPlan,
  deps: { readonly spawner?: ChildSpawner; readonly log?: SpawnLogWriter; readonly maxStderrBytes?: number },
): Promise<RunResult> {
  const spawner = deps.spawner ?? nodeSpawner
  const limit = deps.maxStderrBytes ?? MAX_STDERR_BYTES
  const binary = plan.argv[0]
  if (binary === undefined) refused('a spawn plan with no binary is not a plan')
  deps.log?.record({ argv: plan.argv, cwd: plan.cwd, project: plan.project })

  return new Promise<RunResult>((resolve, reject) => {
    let child: SpawnedChildLike
    try {
      child = spawner(binary, plan.argv.slice(1), {
        cwd: plan.cwd,
        env: plan.env,
        shell: false,
        stdio: ['ignore', 'pipe', 'pipe'],
      })
    } catch (error) {
      reject(error instanceof Error ? error : new Error(String(error)))
      return
    }
    let stdout = ''
    let stderr = ''
    child.stdout?.on('data', (chunk) => {
      stdout += chunk.toString()
    })
    child.stderr?.on('data', (chunk) => {
      stderr = boundAppend(stderr, chunk.toString(), limit)
    })
    child.on('error', (error) => {
      reject(error)
    })
    child.on('exit', (code, signal) => {
      resolve({ code, signal, stdout, stderr })
    })
  })
}

function boundAppend(current: string, chunk: string, limit: number): string {
  const next = current + chunk
  return next.length > limit ? next.slice(next.length - limit) : next
}

/** VersionProbe is what `resolveBinary` learned about a candidate. */
export type BinaryResolution =
  | {
      readonly ok: true
      readonly path: string
      readonly version: string
      /** `configured` when the candidate is the path the installation named,
       * `well-known` for every other candidate rule 4's list produced -- a
       * prefix or a `PATH` entry, which is one set of "where else it can be"
       * and stays one word so that no caller can read a distinction the
       * resolution does not make. */
      readonly source: 'configured' | 'well-known'
    }
  | { readonly ok: false; readonly path: string | null; readonly reason: string }

/**
 * resolveBinary walks rule 4's order and returns the first candidate that is an
 * executable file *and* reports a version. A candidate that exists but whose
 * version cannot be read is refused rather than skipped -- see rule 3 in this
 * file's header -- and an order with no candidate at all is refused with the
 * whole list in the message, because the screen that follows says what to
 * install and where.
 */
export async function resolveBinary(options: {
  readonly configuredPath?: string | undefined
  readonly appEnv: Readonly<Record<string, string | undefined>>
  readonly home?: string
  readonly prefixes?: readonly string[] | undefined
  readonly directory: string
  readonly target: SpawnTarget
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timeoutMs?: number
}): Promise<BinaryResolution> {
  const candidates = binDirectories({
    configuredPath: options.configuredPath,
    appEnv: options.appEnv,
    ...(options.home === undefined ? {} : { home: options.home }),
    ...(options.prefixes === undefined ? {} : { prefixes: options.prefixes }),
  })
  for (const candidate of candidates) {
    if (!isExecutableFile(candidate)) continue
    const plan = planFor('cli.version', {}, {
      target: options.target,
      binaryPath: candidate,
      directory: options.directory,
      appEnv: options.appEnv,
    })
    const probe = await probeVersion(plan, options)
    if (!probe.ok) return { ok: false, path: candidate, reason: probe.reason }
    const source = configuredSource(options.configuredPath, candidate)
    return { ok: true, path: candidate, version: probe.version, source }
  }
  return {
    ok: false,
    path: null,
    reason: `no fugaro binary was found: looked at ${candidates.join(', ')}`,
  }
}

function configuredSource(configuredPath: string | undefined, candidate: string): 'configured' | 'well-known' {
  if (configuredPath !== undefined && configuredPath === candidate) return 'configured'
  return 'well-known'
}

/** probeVersion runs `<binary> version` and reads the one human line it prints.
 * It is a spawn like any other: the spawn log records it, with the
 * installation's project, which is what Check 59a's `sort -u` over `.project`
 * depends on. */
export async function probeVersion(
  plan: SpawnPlan,
  deps: { readonly spawner?: ChildSpawner; readonly log?: SpawnLogWriter; readonly timeoutMs?: number },
): Promise<{ readonly ok: true; readonly version: string } | { readonly ok: false; readonly reason: string }> {
  const timeoutMs = deps.timeoutMs ?? 5000
  const result = await withTimeout(runToCompletion(plan, deps), timeoutMs)
  if (result === null) return { ok: false, reason: 'the binary did not answer `version` in time' }
  if (result.signal !== null) {
    return { ok: false, reason: `the binary was killed by ${result.signal} while reporting its version` }
  }
  if (result.code !== 0) {
    return {
      ok: false,
      reason: `the binary exited ${String(result.code)} for \`version\`: ${cliSentence(result.stderr) ?? 'no message'}`,
    }
  }
  return parseVersionLine(result.stdout)
}

async function withTimeout<T>(promise: Promise<T>, ms: number): Promise<T | null> {
  let timer: ReturnType<typeof setTimeout> | null = null
  const timeout = new Promise<null>((resolve) => {
    timer = setTimeout(() => {
      resolve(null)
    }, ms)
  })
  try {
    return await Promise.race([promise, timeout])
  } finally {
    if (timer !== null) clearTimeout(timer)
  }
}

/** DecodedLine is the reader's rule, as a value: a document, a notice (a line
 * that is not a JSON object), or nothing at all (a blank line). */
export type DecodedLine =
  | { readonly kind: 'document'; readonly document: JSONObject }
  | { readonly kind: 'notice'; readonly text: string }
  | { readonly kind: 'empty' }

/**
 * decodeFeedLine is Task 0.2's reader rule 1
 * (`console/docs/json-contract.md` section 10.13): split stdout on newlines and
 * decode each line as one JSON object; **skip a line that is not**. The degraded
 * mode writes `no budget backend: showing run records only` and a retry sentence
 * to stdout on the same stream as the documents
 * (`runWatchDegraded`, `internal/cli/watch.go`), so a JSON array, a bare
 * scalar, a truncated write and a human sentence are all notices -- surfaced as
 * a header warning -- and never a parse error.
 */
export function decodeFeedLine(line: string): DecodedLine {
  const text = line.replace(/\r$/, '')
  if (text.trim() === '') return { kind: 'empty' }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    return { kind: 'notice', text }
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { kind: 'notice', text }
  }
  return { kind: 'document', document: parsed as JSONObject }
}

/** FeedFrame is one document from the feed, with the console's own stamp. */
export interface FeedFrame {
  readonly document: JSONObject
  /** `receivedAt` is the console's clock, RFC 3339 UTC -- never the child's. */
  readonly receivedAt: string
  /** `sequence` counts the documents this feed has delivered, from 1. */
  readonly sequence: number
  /** `day` is the document's `day` field when it carries a string one. */
  readonly day: string | null
  /** `rolled` is true when this document's day differs from the previous one's:
   * the UTC-midnight rollover of Task 0.2 section 10.7, where the document is a
   * gap in the series rather than a measurement. */
  readonly rolled: boolean
}

/** Notice is a stdout line that was not a JSON object, kept as text. */
export interface Notice {
  readonly text: string
  readonly receivedAt: string
}

/**
 * FeedTracker stamps documents and spots the day rollover. It is a separate
 * object rather than a field of the supervisor because the rule it implements is
 * about the *documents*, not about the process: a consumer can replay a
 * recording through it (Task 1.4) and get the same frames and the same gaps.
 */
export class FeedTracker {
  #sequence = 0
  #day: string | null = null

  /** push stamps one document and reports whether the day changed. */
  push(document: JSONObject, receivedAt: string): FeedFrame {
    this.#sequence += 1
    const rawDay = document['day']
    const day = typeof rawDay === 'string' && rawDay !== '' ? rawDay : null
    const rolled = day !== null && this.#day !== null && day !== this.#day
    this.#day = day ?? this.#day
    return { document, receivedAt, sequence: this.#sequence, day, rolled }
  }
}

/** SidecarEndReason says why the child is not coming back. */
export type SidecarEndReason =
  /** the console asked it to stop */
  | 'stopped'
  /** the CLI refused: exit 1 with the operator's sentence (section 10.5) */
  | 'refused'
  /** the child could not be started at all */
  | 'unstartable'

/** SidecarEnd is the last event a feed emits. */
export interface SidecarEnd {
  readonly reason: SidecarEndReason
  readonly code: number | null
  readonly signal: string | null
  /** `sentence` is the CLI's own message, when it printed one. */
  readonly sentence: string | null
  readonly spawns: number
}

/** SidecarRestart is emitted when the child is coming back after a backoff. */
export interface SidecarRestart {
  readonly attempt: number
  readonly delayMs: number
  readonly code: number | null
  readonly signal: string | null
}

/** SidecarEvents is what the console is told as the feed runs. */
export interface SidecarEvents {
  onFrame(frame: FeedFrame): void
  onNotice(notice: Notice): void
  onRestart(restart: SidecarRestart): void
  onEnded(end: SidecarEnd): void
}

/** Timers is the two timer calls this module uses, as an argument so a test can
 * drive a backoff without waiting for it. */
export interface Timers {
  setTimeout(handler: () => void, ms: number): ReturnType<typeof setTimeout>
  clearTimeout(handle: ReturnType<typeof setTimeout>): void
}

const NODE_TIMERS: Timers = {
  setTimeout: (handler, ms) => setTimeout(handler, ms),
  clearTimeout: (handle) => {
    clearTimeout(handle)
  },
}

/**
 * restartDecision is Task 0.2's restart behaviour as a pure function, and it is
 * the one place the four facts below are decided:
 *
 *   - **exit 1 is a refusal** (section 10.5). The CLI exits 1 for a refused read
 *     (usually having written no document at all), for an unknown flag and for
 *     every other user error, and restarting it "before the identity or the role
 *     changes refuses again in the same way" -- so the console shows the CLI's
 *     own sentence and stops rather than looping.
 *   - **a signal is the ordinary end of a feed** (section 10.4): whether it is
 *     `SIGPIPE` because the reader went away or a `SIGTERM` from anywhere else,
 *     `os.ProcessState` reports a signal and not an exit code, and the console
 *     must not read it as a CLI error. It restarts.
 *   - **exit 0 restarts too**: `watchStream` returns nil on a cancelled context,
 *     and a stream that ended cleanly ended.
 *   - **anything above 1 is not reachable from this path** (section 10.4: "a
 *     console reading the status sees 0, 1 or a signal"), so it is treated as a
 *     failure of the child rather than as a refusal: the feed stops and says so.
 */
export function restartDecision(status: {
  readonly code: number | null
  readonly signal: string | null
  readonly stopped: boolean
  readonly attempt: number
  readonly backoff: Backoff
}): { readonly restart: true; readonly attempt: number; readonly delayMs: number } | { readonly restart: false; readonly reason: SidecarEndReason } {
  if (status.stopped) return { restart: false, reason: 'stopped' }
  if (status.code === 1) return { restart: false, reason: 'refused' }
  if (status.code !== null && status.code !== 0) return { restart: false, reason: 'unstartable' }
  const attempt = status.attempt + 1
  const delayMs = Math.min(status.backoff.minMs * 2 ** (attempt - 1), status.backoff.maxMs)
  return { restart: true, attempt, delayMs }
}

/** Sidecar is the supervised child: one process, restarted until it refuses or
 * the console stops it. */
export interface Sidecar {
  readonly spawns: number
  stop(): Promise<SidecarEnd>
}

/**
 * startSidecar runs one plan as a supervised stream. It is section 10.11's
 * answer -- "one child, supervised" -- in the small: it drains stdout
 * continuously (a stalled reader is a real failure mode, section 10.3), feeds
 * every line through `decodeFeedLine` and every document through a
 * `FeedTracker`, restarts with backoff except after a refusal, and stops by
 * closing the pipe and sending `SIGTERM` (section 10.12), with a `SIGKILL` for
 * the child that a stalled reader has wedged.
 */
export function startSidecar(
  plan: SpawnPlan,
  events: SidecarEvents,
  deps: {
    readonly spawner?: ChildSpawner
    readonly timers?: Timers
    readonly log?: SpawnLogWriter
    readonly clock?: () => Date
    readonly shutdownGraceMs?: number
  } = {},
): Sidecar {
  const spawner = deps.spawner ?? nodeSpawner
  const timers = deps.timers ?? NODE_TIMERS
  const clock = deps.clock ?? ((): Date => new Date())
  const grace = deps.shutdownGraceMs ?? SHUTDOWN_GRACE_MS
  const tracker = new FeedTracker()

  let stopped = false
  let spawns = 0
  let attempt = 0
  let child: SpawnedChildLike | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let buffer = ''
  let stderr = ''
  let ended = false
  let settle: ((end: SidecarEnd) => void) | null = null

  const stamp = (): string => clock().toISOString().replace(/\.\d{3}Z$/, 'Z')

  const finish = (end: SidecarEnd): void => {
    if (ended) return
    ended = true
    if (timer !== null) {
      timers.clearTimeout(timer)
      timer = null
    }
    deps.log?.close()
    if (settle !== null) settle(end)
    events.onEnded(end)
  }

  const consume = (chunk: string): void => {
    buffer += chunk
    for (;;) {
      const index = buffer.indexOf('\n')
      if (index < 0) break
      const line = buffer.slice(0, index)
      buffer = buffer.slice(index + 1)
      const decoded = decodeFeedLine(line)
      if (decoded.kind === 'document') {
        events.onFrame(tracker.push(decoded.document, stamp()))
      } else if (decoded.kind === 'notice') {
        events.onNotice({ text: decoded.text, receivedAt: stamp() })
      }
    }
  }

  const spawnOnce = (): void => {
    const binary = plan.argv[0]
    if (binary === undefined) {
      finish({ reason: 'unstartable', code: null, signal: null, sentence: 'the spawn plan has no binary', spawns })
      return
    }
    deps.log?.record({ argv: plan.argv, cwd: plan.cwd, project: plan.project })
    spawns += 1
    let started: SpawnedChildLike
    try {
      started = spawner(binary, plan.argv.slice(1), {
        cwd: plan.cwd,
        env: plan.env,
        shell: false,
        stdio: ['ignore', 'pipe', 'pipe'],
      })
    } catch (error) {
      finish({
        reason: 'unstartable',
        code: null,
        signal: null,
        sentence: error instanceof Error ? error.message : String(error),
        spawns,
      })
      return
    }
    child = started
    stderr = ''
    started.stdout?.on('data', (chunk) => {
      consume(chunk.toString())
    })
    started.stderr?.on('data', (chunk) => {
      stderr = boundAppend(stderr, chunk.toString(), MAX_STDERR_BYTES)
    })
    started.on('error', (error) => {
      finish({ reason: 'unstartable', code: null, signal: null, sentence: error.message, spawns })
    })
    started.on('exit', (code, signal) => {
      child = null
      const decision = restartDecision({ code, signal, stopped, attempt, backoff: plan.backoff })
      if (!decision.restart) {
        finish({ reason: decision.reason, code, signal, sentence: cliSentence(stderr), spawns })
        return
      }
      attempt = decision.attempt
      events.onRestart({ attempt, delayMs: decision.delayMs, code, signal })
      timer = timers.setTimeout(() => {
        timer = null
        if (!stopped) spawnOnce()
      }, decision.delayMs)
    })
  }

  const stop = (): Promise<SidecarEnd> =>
    new Promise<SidecarEnd>((resolve) => {
      if (ended) {
        resolve({ reason: 'stopped', code: null, signal: null, sentence: null, spawns })
        return
      }
      settle = resolve
      stopped = true
      if (timer !== null) {
        timers.clearTimeout(timer)
        timer = null
      }
      const running = child
      if (running === null) {
        finish({ reason: 'stopped', code: null, signal: null, sentence: null, spawns })
        return
      }
      running.kill('SIGTERM')
      timers.setTimeout(() => {
        if (child !== null) child.kill('SIGKILL')
      }, grace)
    })

  spawnOnce()
  return {
    get spawns(): number {
      return spawns
    },
    stop,
  }
}
