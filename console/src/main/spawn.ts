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
//      path first -- an absolute one, since a relative path is checked against
//      the app's working directory and run against the child's, which is a
//      refusal and not a fallback (review of Task 4.1) -- then the platform's
//      well-known directories, and **nothing else: the app's own inherited
//      `PATH` is never searched** (owner's ruling of 2026-10-20: the console
//      asks the operator for nothing, and resolves the `fugaro` that is
//      *installed*). The configured path is an optional override a store may
//      already carry, never a question the first run asks. **Which directories those are is a
//      per-platform list chosen in one place** (`PLATFORM_LOCATIONS`, read
//      through `locationsFor`): the design's macOS order is
//      `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, `/usr/bin`, and
//      Linux's (the owner's ruling of 2026-10-20) is `/usr/local/bin`,
//      `/usr/bin`, `~/.local/bin`, `/home/linuxbrew/.linuxbrew/bin`,
//      `~/.linuxbrew/bin`. Neither list is ever read out of the app's own
//      environment -- "never the inherited `PATH`" is a property of the list,
//      and there is no step after it. **On Windows there is no list at all**:
//      the CLI is a Linux program inside a WSL distribution, the row for
//      `win32` is the WSL route rather than a set of directories (T-C2), and a
//      platform with no row is refused rather than answered with Linux's
//      (T-C3). The path and the version it reported
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
//      first, then the platform's own tool directories** (rule 5's list, from
//      the same `locationsFor` table: macOS gets `/opt/homebrew/bin`, Linux
//      gets `/home/linuxbrew/.linuxbrew/bin`, and neither gets the other's;
//      **on the WSL route the list is Linux's, because the child is a Linux
//      process** -- F5, and `WIN32_LOCATIONS.childPathPrefixes` says so by
//      naming Linux's) --
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
import { chmodSync, existsSync, lstatSync, mkdirSync, realpathSync, statSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { StringDecoder } from 'node:string_decoder'
import { dirname, isAbsolute, join } from 'node:path'

import { CONSOLE_OPERATIONS, isNeverReachable } from './ipc'
import type { ArgumentKind, OperationName, ValidatedArgument, ValidatedArguments } from './ipc'
import type { SpawnLogWriter } from './spawnlog'
// The WSL seam, and the one import that closes a cycle: `wsl.ts` imports this
// module's `childEnvironment`, `runToCompletion` and `FUGARO_BINARY_NAME`, and
// this module imports `wslExecutable` (which program a WSL plan runs) and
// `wslDistributions` (which distributions the machine has, for the resolver's
// two refusals, T-C5). The cycle is deliberate and safe: both modules use the
// other's bindings **inside function bodies only**, so neither reads a
// partially initialized module at load time, and the alternative -- a second
// copy of `wslExecutable` here, or a resolver in `wsl.ts` -- would be two
// answers to one question.
import { wslDistributions, wslExecutable } from './wsl'

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
 * PlatformRoute is which *kind* of answer a platform's row is. It exists so a
 * reader is never left to infer the route from a list's contents: an empty
 * `binaryPrefixes` looks like an unfinished row, and the win32 row's emptiness
 * is the opposite of unfinished -- it is the statement that the host is not
 * where the CLI is.
 *
 *   - `host` -- the CLI is a program on this machine, so rule 4's walk is a
 *     walk of this machine's directories and the child runs where the console
 *     runs;
 *   - `wsl` -- the CLI is a Linux program inside a distribution and is reached
 *     through `wsl.exe`, so the walk is not a walk of the host at all (T-C2)
 *     and the child's `PATH` is a Linux one (F5).
 */
export type PlatformRoute = 'host' | 'wsl'

/**
 * PlatformLocations is the whole of what this file knows per platform: where
 * rule 4 looks for the CLI, and which tool directories rule 5 gives the child.
 * The two are not the same list on any platform and the difference is
 * deliberate (see each field), so they are two fields of one record rather than
 * two constants that have to be kept in step by hand.
 */
export interface PlatformLocations {
  /** route is which kind of answer this row is -- see `PlatformRoute`. */
  readonly route: PlatformRoute
  /** binaryPrefixes is rule 4's order, and it is the whole of it: a configured
   * path first (`SpawnTarget.binaryPath`, the optional override an installation
   * may carry), then these directories in this order. The app's own inherited
   * `PATH` is **not** a step after them (owner's ruling of 2026-10-20). `~/.local/bin` is
   * spelled with `~` and joined with the home directory at resolution time,
   * because the home directory is an argument rather than a constant. **On the
   * `wsl` route it is empty**: the host is not where the CLI is, and an empty
   * list is how the row says so. */
  readonly binaryPrefixes: readonly string[]
  /** childPathPrefixes is the well-known *tool* directories the child's `PATH`
   * is built from: the resolved binary's own directory first, then these. It
   * answers "where may the CLI find `terraform`?", so it includes `/bin`,
   * `/usr/sbin` and `/sbin`, and it does not include `~/.local/bin` unless the
   * binary itself lives there. Every entry is absolute: the child receives a
   * literal string and nothing expands a `~` inside a `PATH`. **It is the list
   * of the platform the child runs on, which is not always the platform the
   * console runs on**: on the `wsl` route it is Linux's (F5). */
  readonly childPathPrefixes: readonly string[]
}

/**
 * DARWIN_LOCATIONS is rule 4's macOS order and rule 5's macOS tool list,
 * verbatim from design section 3.4.
 *
 * `/opt/homebrew/bin` is the Apple-silicon Homebrew prefix; it exists on the
 * Intel Macs too as of Homebrew's own relocation, and it is first because a
 * Homebrew-installed CLI is the operator's own installation rather than the
 * system's.
 */
export const DARWIN_LOCATIONS: PlatformLocations = Object.freeze({
  route: 'host' as const,
  binaryPrefixes: Object.freeze(['/opt/homebrew/bin', '/usr/local/bin', '~/.local/bin', '/usr/bin']),
  childPathPrefixes: Object.freeze([
    '/opt/homebrew/bin',
    '/usr/local/bin',
    '/usr/bin',
    '/bin',
    '/usr/sbin',
    '/sbin',
  ]),
})

/**
 * LINUX_LOCATIONS is the same two lists for Linux, from the owner's ruling of
 * 2026-10-20 ("yes, Linux for 0.10.0, unsigned"), and the order is the brief's:
 * `/usr/local/bin`, `/usr/bin`, `~/.local/bin`,
 * `/home/linuxbrew/.linuxbrew/bin`, `~/.linuxbrew/bin`.
 *
 * Two things are worth stating, because both are choices rather than
 * transcriptions:
 *
 *   - **`/opt/homebrew/bin` is not here.** It is the macOS Homebrew prefix: on
 *     Linux the equivalent installations are Linuxbrew's two directories below,
 *     and a directory that does not exist on the machine is not a place to look
 *     for a binary.
 *   - **`~/.linuxbrew/bin` is a resolution candidate and cannot be a `PATH`
 *     entry**, because nothing expands a tilde inside a `PATH`; the per-user
 *     case is covered by the resolved binary's own directory, which the child's
 *     `PATH` always puts first.
 */
export const LINUX_LOCATIONS: PlatformLocations = Object.freeze({
  route: 'host' as const,
  binaryPrefixes: Object.freeze([
    '/usr/local/bin',
    '/usr/bin',
    '~/.local/bin',
    '/home/linuxbrew/.linuxbrew/bin',
    '~/.linuxbrew/bin',
  ]),
  childPathPrefixes: Object.freeze([
    '/usr/local/bin',
    '/usr/bin',
    '/bin',
    '/usr/sbin',
    '/sbin',
    '/home/linuxbrew/.linuxbrew/bin',
  ]),
})

/**
 * WIN32_LOCATIONS is the row for the Windows console, and **it is not a list of
 * directories** (T-C2, decision D1). It is the row for a route rather than a
 * place: on Windows the CLI is a Linux program inside a WSL distribution, and
 * the console reaches it by running `wsl.exe -d <distro> --cd <dir> -- fugaro
 * …` rather than by executing a binary the host can see. There is no
 * `fugaro.exe` to find, so there is nothing for rule 4 to walk.
 *
 * The two fields say that in the two ways they can:
 *
 *   - **`binaryPrefixes` is empty**, so `binDirectories` on this platform is
 *     empty too -- the same code, told the truth about the host. It is not a
 *     fallback to Linux's list: on Windows `/usr/local/bin` is a path relative
 *     to the current drive, and looking for a binary there would be a search of
 *     a directory that means something else on the machine it runs on. The
 *     distro-side resolution is its own path (`src/main/wsl.ts`, T-C4/T-C5),
 *     not a row in this table.
 *   - **`childPathPrefixes` is Linux's**, by reference rather than by a copy
 *     (F5). The process on the other end of `wsl.exe` is a Linux process: the
 *     directories it may find `terraform`, `gcloud` or `cosign` in are Linux's,
 *     and a `PATH` of Windows directories would give it entries no Linux lookup
 *     can use. Naming `LINUX_LOCATIONS.childPathPrefixes` here rather than
 *     writing the six strings out again is deliberate -- the child's tool list
 *     is one fact about Linux, and two copies of it could drift.
 */
export const WIN32_LOCATIONS: PlatformLocations = Object.freeze({
  route: 'wsl' as const,
  binaryPrefixes: Object.freeze([] as string[]),
  childPathPrefixes: LINUX_LOCATIONS.childPathPrefixes,
})

/**
 * PLATFORM_LOCATIONS is the per-platform table, and `locationsFor` is the only
 * reader of it. **The platform choice lives here and nowhere else**: no other
 * line in this module -- and none in `src/source/` -- names a well-known
 * directory, so a second platform is a row in this table rather than an edit in
 * each place that needs one.
 *
 * The keys are Node's own `process.platform` values, which are the same strings
 * electron-builder uses for `electronPlatformName` -- so the two halves of "does
 * this build support this platform?" (this table and the fuse hook's
 * `SUPPORTED_PLATFORMS`) are keyed identically on purpose.
 *
 * **A row is a decision, and the table is the list of decisions that have been
 * made**: darwin and Linux are host routes, win32 is the WSL route, and a
 * platform that is not here is one nobody has decided -- which `locationsFor`
 * refuses rather than answers (T-C3).
 */
export const PLATFORM_LOCATIONS: Readonly<Record<string, PlatformLocations>> = Object.freeze({
  darwin: DARWIN_LOCATIONS,
  linux: LINUX_LOCATIONS,
  win32: WIN32_LOCATIONS,
})

/**
 * locationsFor is the one place the per-platform choice is made: the platform's
 * row, or a refusal for a platform nobody has decided.
 *
 * **It used to answer with Linux's lists, and that fallback is gone** (T-C3).
 * It was survivable while the app shipped only for darwin and Linux: the branch
 * was reached by a developer running the sources somewhere the app has never
 * been packaged, and the POSIX layout was the nearest thing to a right answer.
 * It stopped being survivable when `win32` became a route this app takes: the
 * same silent answer would hand a Windows child a Linux `PATH` -- entries no
 * Windows lookup can use -- and send rule 4 to `/usr/local/bin`, which on
 * Windows is a path relative to the current drive rather than the absolute
 * directory the list means on POSIX. A wrong answer that is *shaped* like a
 * right one is worse than no answer, and the platform nobody decided is exactly
 * where the console should stop rather than guess at where to execute a
 * program.
 *
 * The throw is a programming error and not an operator-facing refusal: the
 * platforms the app is packaged for each have a row, and `fuses.ts`'s
 * `SUPPORTED_PLATFORMS` refuses to build for any other one. It is reachable in
 * a test, which is the point -- `locationsFor('freebsd')` is how the refusal is
 * held.
 */
export function locationsFor(platform: string = process.platform): PlatformLocations {
  const row = PLATFORM_LOCATIONS[platform]
  if (row === undefined) {
    throw new Error(
      `there is no location row for the platform ${JSON.stringify(platform)}: this console ` +
        `resolves the CLI on darwin, on linux and on win32 (inside a WSL distribution) and ` +
        `nowhere else, and a guess at another platform's directories would be a guess at ` +
        `where to execute a program.`,
    )
  }
  return row
}

/**
 * CHILD_ENV_KEYS is the whole environment the child may receive, and the order
 * is the schema's: the one key this module always writes (`PATH`, rebuilt),
 * the four the operator's own environment supplies when it has them, the five
 * `XDG_*` variables, and `FUGARO_PROJECT`, which is always the chosen
 * installation's. Anything not on this list is not passed -- there is no merge
 * with `process.env` anywhere in this file, and that is the property
 * `test/unit/spawn.test.ts` diffs the child's own report against.
 *
 * **It is the whole list on both routes, and the WSL route adds exactly one key
 * to the environment `wsl.exe` is handed (`WSLENV_KEY`, see `childEnvironment`).**
 * That key is not a variable the distribution receives: it is `wsl.exe`'s own
 * share list, and the console states it empty rather than leaving it out, so
 * "no Windows variable is shared with the distribution" is a sentence this
 * console wrote rather than a default it inherited (F20, D5(a)).
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
 * WSLENV_KEY is `wsl.exe`'s own share list, and **the console writes it, always
 * empty, and never reads it** (Part E, T-E2; F20, D5(a)).
 *
 * The mechanism it states: `wsl.exe` copies a Windows variable into the
 * distribution's environment **only when that variable's name is listed in
 * `WSLENV`**, and an entry may carry a `/u`, `/w` or `/l` suffix saying which
 * side's spelling it keeps. So an absent `WSLENV` and an empty one mean the same
 * thing to a build that reads it as a list -- and only the second is a thing the
 * console *said*. With `WSLENV` in the host's own environment the console does
 * nothing: the key is written as `''` unconditionally and the host's value is
 * not read, because a `WSLENV` that lists `PATH` (or anything else) is exactly
 * the leak this module's allow-list exists to prevent, and forwarding the
 * operator's own share list would hand the distribution whatever the operator
 * had arranged for their own `wsl` sessions -- which is not the console's
 * decision to make. `test/unit/secret-hygiene.test.ts` holds the "never reads
 * it" half as a source scan over `console/src/`.
 */
export const WSLENV_KEY = 'WSLENV'

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

/** READ_TIMEOUT_MS is the deadline of every one-shot read (`runToCompletion`).
 * The CLI's slowest read is `doctor`, which runs a handful of local checks; a
 * child still running after this is a child that is not going to answer, and
 * it is stopped -- `SIGTERM`, then `SIGKILL` after `SHUTDOWN_GRACE_MS` -- rather
 * than left to a screen that waits forever. */
export const READ_TIMEOUT_MS = 90_000

/** VERSION_TIMEOUT_MS is the version handshake's own, shorter deadline: the
 * command prints one constant line. */
export const VERSION_TIMEOUT_MS = 5000

/** MAX_STDOUT_LENGTH bounds what a one-shot read may print, in UTF-16 code
 * units. The largest document is `logs --json`; this is generously above it,
 * and a child that prints more is stopped and refused rather than buffered
 * until the app runs out of memory. */
export const MAX_STDOUT_LENGTH = 32 * 1024 * 1024

/** MAX_FEED_LINE_LENGTH bounds one line of the live feed. A watch document is
 * a few kilobytes; a stream that writes this much without a newline is not
 * writing documents, and its line is dropped and surfaced as a notice. */
export const MAX_FEED_LINE_LENGTH = 4 * 1024 * 1024

/** PRIVATE_DIR_MODE is the mode of the directory the app owns for a child that
 * has no checkout. See `privateWorkingDirectory`. */
export const PRIVATE_DIR_MODE = 0o700

/** PRIVATE_DIR_NAME is the name of that directory under the system temporary
 * directory, and `PRIVATE_DIR_LEAF` the name of the directory inside it the
 * child actually runs in. The base carries the uid (see
 * `privateWorkingDirectory`): the leaf cannot, because the base is the level two
 * users on one machine would otherwise share. */
export const PRIVATE_DIR_NAME = 'fugaro-console'
export const PRIVATE_DIR_LEAF = 'cwd'

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
 * of those kinds are one string spelling, so a `runSince` and a `reportSince`
 * are both `value` here, and the only kind whose argv spelling differs is
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
  reportSince: 'value',
  runSince: 'value',
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
 * PickerOperationName is the one spawn that belongs to the **installation
 * picker** rather than to the dashboard: the local read that lists the
 * installations this machine has (`fugaro config list --json`,
 * `internal/cli/config_list.go`; `console/docs/json-contract.md` §6.8).
 *
 * It is not a `CONSOLE_OPERATIONS` entry and deliberately so: that table is the
 * *dashboard's* IPC surface, mirrored one-for-one in `src/preload/index.ts` and
 * pinned exactly by `test/unit/ipc.test.ts`, and the picker has its own surface
 * on its own document (`src/preload/installations.ts`). Widening the dashboard's
 * surface to carry a read only the picker makes would put a command in a
 * renderer that has no business spawning it.
 */
export type PickerOperationName = 'installation.list'

/** PICKER_OPERATION_NAMES is that vocabulary as a value, so the table check and
 * a test can both read the closed set rather than restating it. */
export const PICKER_OPERATION_NAMES: readonly PickerOperationName[] = Object.freeze(['installation.list'])

/** INSTALLATION_LIST_COMMAND is the command the one picker row drives, spelled
 * as the two words `words` starts with. It is here rather than inline so the
 * check below and the row cannot disagree about which read the picker makes. */
export const INSTALLATION_LIST_COMMAND = 'config list'

/** SpawnOperationName is every operation this module will build an argv for. */
export type SpawnOperationName = OperationName | PickerOperationName

/**
 * WslQueryName is the questions the console asks `wsl.exe` itself rather than
 * the CLI (Part C, T-C4; `src/main/wsl.ts`). They are not CLI operations: they
 * carry no `--project`, they produce no document, and `SPAWN_COMMANDS` must not
 * grow a row for them -- `assertSpawnSurface`'s table is the table of reads the
 * console makes *of the CLI*, and `wsl.exe --list --quiet` is not one.
 *
 * They are named anyway, because a query is a spawn like any other: it carries a
 * plan, it goes through `runToCompletion`, and the spawn log records it. A plan
 * whose `operation` field was a CLI operation would say the console had run that
 * operation when it had not.
 */
export type WslQueryName = 'wsl.distributions'

/**
 * SpawnCommand is one row of the table: the operation it serves, the argv words
 * the CLI's own command path needs, whether the command accepts `--project`,
 * and its declared arguments in the order they are emitted.
 */
export interface SpawnCommand {
  readonly operation: SpawnOperationName
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
 * PICKER_COMMANDS is the installation picker's own table, and it has exactly one
 * row: the local read that enumerates the installations on this machine. It is a
 * separate table rather than a row of `SPAWN_COMMANDS` because that table is
 * held one-for-one to the dashboard's operation table (`assertSpawnSurface`),
 * and this read is not one of the dashboard's operations.
 *
 * `fugaro config list` is cobra's `NoArgs` with one flag of its own (`--json`);
 * it takes no `--project`, no `--config` and no `--dir`, and it opens no bucket,
 * so the row carries the three false columns the check below holds it to.
 */
export const PICKER_COMMANDS: readonly SpawnCommand[] = Object.freeze([
  {
    operation: 'installation.list',
    words: ['config', 'list', '--json'],
    takesProject: false,
    args: [],
    dirFlag: false,
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
  const row =
    SPAWN_COMMANDS.find((candidate) => candidate.operation === operation) ??
    PICKER_COMMANDS.find((candidate) => candidate.operation === operation)
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
  operation: SpawnOperationName,
  args: ValidatedArguments,
  context: { readonly target: SpawnTarget; readonly directory: string | undefined },
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

  // A caller with no directory to name **omits the flag** rather than naming a
  // wrong one. `directory` is `undefined` in exactly one place: the WSL route
  // before Part D translates a host directory into the distribution's spelling
  // (T-D4), where the alternative -- handing the CLI a `C:\...` path inside a
  // distribution -- is the wrong-spelling bug T-A2's fake refuses by name. The
  // host route always has one, so its argv is unchanged.
  if (row.dirFlag && context.directory !== undefined) {
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
 * WSL_PREFIX_FLAGS is every word of the wrapper that is a flag or a separator:
 * the distribution to run in, the directory to start in, and the `--` after
 * which the CLI's own argv begins. It is `wsl.exe`'s set and **not**
 * `SPAWN_FLAGS`'s, which is the whole reason `SpawnPlan` keeps `prefix` apart
 * from `argv` (T-C1, F8): `--cd` is not a flag this console may emit to the CLI,
 * and `--` is not a flag at all.
 *
 * The two sets must stay disjoint, and `test/unit/spawn.test.ts` holds them
 * apart: a word in both would be a wrapper word that also looks like a CLI flag,
 * which is the one shape that makes the two halves of the command line
 * indistinguishable.
 */
export const WSL_PREFIX_FLAGS: readonly string[] = Object.freeze(['-d', '--cd', '--'])

/**
 * DISTRO_NAME_RE is the alphabet a WSL distribution's name may use when this
 * console hands it to `wsl.exe` (T-E3, F22). A distribution's name is a
 * registry key's name on Windows -- letters, digits, `.`, `_` and `-` -- and it
 * is checked as a name rather than as a path, because it is one.
 *
 * **Why a name needs an alphabet at all.** Part C widened the argv this console
 * builds with words that do not come from the CLI's own table: a distribution's
 * name arrives from `wsl.exe --list --quiet` (a document's string) or from the
 * store, and it lands on `wsl.exe`'s command line. With `shell: false` there is
 * no shell for it to be re-parsed by, so the hazard is not injection but
 * *argument confusion*: a name spelled `--help`, `-d` or `--` is a word
 * `wsl.exe` reads as its own, and a name carrying whitespace or a control
 * character is a name no `wsl.exe` ever printed. A name outside this alphabet,
 * or one that begins with a hyphen, is refused by name rather than run (see
 * `isSafeDistroName` and `assertWslPrefix`).
 */
export const DISTRO_NAME_RE = /^[A-Za-z0-9._-]+$/

/** isSafeDistroName answers whether a distribution's name may become a wrapper
 * word. It is the tight alphabet above, with the length bound the store's own
 * values carry, and an empty name is refused because `-d ''` names nothing.
 *
 * **A leading `-` is refused as well as the alphabet**, and that is the one
 * place this rule is narrower than the alphabet it is built on: `-` has to be
 * admitted (a real distribution is called `Ubuntu-22.04`), so `-d --help`,
 * `-d -d` and `-d --` are all *inside* the alphabet while being exactly the
 * argument confusion the alphabet exists to refuse. A distribution whose
 * registered name begins with a hyphen is not a distribution any `wsl.exe`
 * printed, so refusing it by name costs nothing real. */
export function isSafeDistroName(value: string): boolean {
  if (value === '' || value.length > 256) return false
  if (value.startsWith('-')) return false
  return DISTRO_NAME_RE.test(value)
}

/**
 * assertWslPrefix holds the wrapper to its shape and to its two alphabets, and
 * it is the prefix's half of what `assertArgvFlags` is to the CLI's argv
 * (T-E3, F22). It is called by `wslWrapper`, the only producer of a prefix, for
 * the same reason `assertArgvFlags` is called by `argvFor`: a check in the
 * callers is a check a later caller can forget.
 *
 * The shape is `-d <distribution>` and then either `--` or `--cd <directory> --`
 * -- exactly what `wslWrapper` builds, and nothing else. Two of the words are
 * **values**, and they are checked by two different rules:
 *
 *   - **the distribution's name** is a *name*, and it is held to
 *     `DISTRO_NAME_RE`: it is the one word here that reaches `wsl.exe` as a
 *     selector, and a name that could be read as a flag or as two words is the
 *     argument confusion `DISTRO_NAME_RE` exists to refuse.
 *   - **the directory is exempt from that alphabet, by name and deliberately.**
 *     It is a Linux path, and a path is not a name: `/home/Ann Lee/src` is a
 *     directory an operator can have, and `ARGUMENT_RE` (which the renderer's
 *     own values satisfy) admits neither the space nor the `~` a shell would
 *     expand. It is checked by `isSafeInstallationValue` instead -- the rule the
 *     project name, the config path and the checkout already satisfy -- so what
 *     is refused is a control character, a NUL, an empty string, an absurd
 *     length and a leading `-`, and what is admitted is every path an operator
 *     can actually type.
 */
export function assertWslPrefix(prefix: readonly string[]): void {
  const words = [...prefix]
  for (const word of words) {
    if (SPAWN_FLAGS.includes(word)) {
      refused(`the wrapper would carry ${word}, which is a flag this console emits to the CLI: the two halves of the command line are separate fields`)
    }
  }
  const [flag, name] = words
  if (flag !== '-d') {
    refused(`the wrapper must start with -d <distribution>, and starts with ${JSON.stringify(flag ?? '')}`)
  }
  if (name === undefined || !isSafeDistroName(name)) {
    refused(
      `the distribution name ${JSON.stringify(name ?? '')} is not one this console will hand to wsl.exe: ` +
        `a distribution's name is ${String(DISTRO_NAME_RE)}`,
    )
  }
  const rest = words.slice(2)
  if (rest.length === 1 && rest[0] === '--') return
  if (rest.length === 3 && rest[0] === '--cd' && rest[2] === '--') {
    // The directory, and the exemption above: a path is not a name.
    const directory = rest[1] ?? ''
    if (!isSafeInstallationValue(directory)) {
      refused(`the wrapper would start the CLI in ${JSON.stringify(directory)}, which is not a directory this console will pass`)
    }
    return
  }
  refused(
    `the wrapper's words are \`-d <distribution>\`, an optional \`--cd <directory>\`, and \`--\`: ` +
      `${JSON.stringify(words.join(' '))} is none of them`,
  )
}

/**
 * COMMANDS_WITHOUT_PROJECT and COMMANDS_TAKING_DIR are rules 3 and 5 as the CLI
 * states them, keyed by the *operation's* command (Task 1.2's table), not by the
 * spawn row: `version` is the one command with no `--project`
 * (`newVersionCmd` has no flags at all), and `doctor` is the one with a `--dir`.
 * `assertSpawnSurface` holds every row's `takesProject` and `dirFlag` to these,
 * so the columns `argvFor` keys off cannot drift from the rule.
 */
export const COMMANDS_WITHOUT_PROJECT: readonly string[] = Object.freeze(['version', 'config list'])
export const COMMANDS_TAKING_DIR: readonly string[] = Object.freeze(['doctor'])

/**
 * assertSpawnSurface fails a table that does not correspond one-to-one with the
 * operations Task 1.2 declared, drives something on the never-reachable table,
 * declares an argument the operation does not have, omits a flag it does fix,
 * gets `takesProject` or `dirFlag` wrong for its command (rules 3 and 5), or
 * emits a flag outside `SPAWN_FLAGS`. It takes the table as an argument so the
 * test can hand it the mutation directly -- a row for `budget kill`, a row that
 * drops `takesProject` from `watch` -- instead of the check being reachable only
 * by editing this file. `src/source/cli.ts` calls it with the real table before
 * it resolves a binary, so a console that cannot be built with a widened table
 * cannot be talked into spawning one later.
 */
export function assertSpawnSurface(
  rows: readonly SpawnCommand[],
  pickerRows: readonly SpawnCommand[] = PICKER_COMMANDS,
): void {
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
    const wantsProject = !COMMANDS_WITHOUT_PROJECT.includes(operation.command)
    if (row.takesProject !== wantsProject) {
      refused(
        `${row.operation} ${wantsProject ? 'must carry' : 'must not carry'} ${PROJECT_FLAG}: every command but version takes it (rule 3)`,
      )
    }
    const wantsDir = COMMANDS_TAKING_DIR.includes(operation.command)
    if (row.dirFlag !== wantsDir) {
      refused(`${row.operation} ${wantsDir ? 'must carry' : 'must not carry'} ${DIR_FLAG}: only doctor takes it (rule 5)`)
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

  // The picker's table, held to the same two rules that matter and to the
  // vocabulary of the picker surface: a row here serves an operation the
  // picker's own preload exposes (`PICKER_COMMANDS`' names are the whole set
  // this function admits, and one outside it is refused), it carries no
  // `--project` and no `--dir`, and it declares no arguments -- because every
  // value this read takes is one the CLI itself printed.
  for (const row of pickerRows) {
    if (seen.has(row.operation)) refused(`${row.operation} has rows in both spawn tables`)
    seen.add(row.operation)
    if (!PICKER_OPERATION_NAMES.includes(row.operation as PickerOperationName)) {
      refused(`${row.operation} is not an operation the installation picker may spawn`)
    }
    const command = row.words.filter((word) => !word.startsWith('--')).join(' ')
    if (command !== INSTALLATION_LIST_COMMAND) {
      refused(`${row.operation} drives ${command}, which is not the read the picker makes`)
    }
    if (row.takesProject !== !COMMANDS_WITHOUT_PROJECT.includes(command)) {
      refused(
        `${row.operation} ${COMMANDS_WITHOUT_PROJECT.includes(command) ? 'must not carry' : 'must carry'} ${PROJECT_FLAG}: the read takes no project`,
      )
    }
    if (row.dirFlag !== COMMANDS_TAKING_DIR.includes(command)) {
      refused(`${row.operation} must not carry ${DIR_FLAG}: it reads no checkout`)
    }
    if (row.args.length !== 0) {
      refused(`${row.operation} declares an argument, and the read takes none`)
    }
    assertArgvFlags(row.operation, row.words)
  }
}

/**
 * binDirectories is the resolution order of rule 4: the configured path, then
 * the well-known prefixes -- and there is no third step. It is exported because
 * the order is the rule, and a test that could not read the order could not
 * assert it.
 *
 * There is deliberately **no `appEnv` parameter**: the app's own inherited
 * `PATH` used to be the last step here, and taking it out of the signature is
 * what keeps it out of the order -- a future edit cannot put the app's `PATH`
 * back without changing every caller as well as this function.
 */
export function binDirectories(options: {
  readonly configuredPath?: string | undefined
  /** `home` replaces `homedir()` for the `~` entries. It is an argument so the
   * resolution is a pure function of what it is given. */
  readonly home?: string
  /** `platform` replaces `process.platform` for the well-known list. It is how
   * a test on one machine drives the other platform's order -- and the list
   * itself comes from `locationsFor`, so an injected platform cannot choose a
   * list this module does not already know. */
  readonly platform?: string | undefined
  /** `prefixes` replaces the platform's list outright, for a test that cannot
   * create `/opt/homebrew/bin`. It is the *list* that is injectable, never the
   * order: the loop below walks whatever list it is given front to back, and
   * `test/unit/spawn.test.ts` asserts the real lists separately. */
  readonly prefixes?: readonly string[] | undefined
}): readonly string[] {
  const home = options.home ?? homedir()
  const candidates: string[] = []
  // Only an absolute one, for the same reason as the well-known prefixes below.
  // This list is what `isExecutableFile` is asked about, and that question is put to
  // the *app's* working directory; the child then runs with the installation's
  // directory as its cwd, and `argv[0]` is this string. A relative configured
  // path would therefore be checked in one place and run in another -- a
  // different file, or none -- so it is not a candidate at all. `resolveBinary`
  // refuses it outright rather than letting the walk fall through to the
  // well-known prefixes, which would be the silent substitution rule 3 refuses.
  if (options.configuredPath !== undefined && options.configuredPath !== '' && isAbsolute(options.configuredPath)) {
    candidates.push(options.configuredPath)
  }
  const wellKnown = options.prefixes ?? locationsFor(options.platform).binaryPrefixes
  for (const prefix of wellKnown) {
    const base = prefix.startsWith('~') ? join(home, prefix.slice(2)) : prefix
    candidates.push(join(base, FUGARO_BINARY_NAME))
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
 * process may not stat.
 *
 * **It is a question about this host's filesystem, and it is only ever asked
 * about a path on it** (F4, T-C6). The one path in this work that is not: a
 * stored installation's `binaryPath` under a `distro`, which is a Linux spelling
 * *inside* a distribution and which Windows cannot `stat` at all. Asking this
 * function about it would answer "not executable" about a CLI that is there and
 * works -- so that route does not ask it (`resolveInstalledBinary`'s WSL branch,
 * and `installations.ts`'s `validateInstallation`, which skips the same question
 * about the checkout). The distribution answers instead, through the same
 * `version` probe every other route uses.
 *
 * **The mode question is the platform's** (T-C7). On POSIX it is "a regular file
 * with an execute bit"; on Windows there is no execute bit at all -- the
 * filesystem reports two modes and neither of them is one -- so the answer is
 * "a regular file", which is the same answer `CreateProcess` gives. Asking the
 * POSIX question of a Windows path would answer "not executable" about every
 * CLI on the machine. That is a *different* question from the one above about
 * paths the host cannot see at all: both say "the host does not decide here",
 * and neither replaces the other.
 *
 * `platform` is the platform of the filesystem being asked, which is the host's
 * -- `resolveBinary` and `resolveInstalledBinary` pass the same value to
 * `binDirectories`, so one route does not walk one platform's directories and
 * then ask another platform's mode question about them. */
export function isExecutableFile(path: string, platform: string = process.platform): boolean {
  try {
    if (!existsSync(path)) return false
    const stat = statSync(path)
    if (!stat.isFile()) return false
    if (platform === 'win32') return true
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
 *
 * **`wsl` adds one key and it is always the empty string** (T-E2, D5(a)):
 * `WSLENV`, `wsl.exe`'s own share list, which says which Windows variables the
 * distribution receives. The allow-list above is what keeps the Windows
 * environment out; this states *why* the mechanism that could let it in is
 * closed, rather than relying on a default whose exact behaviour is unverified
 * (U4). See `WSLENV_KEY`.
 *
 * **It is an explicit option and not `platform === 'win32'`**, because the two
 * questions are different ones: the row a platform selects is the `PATH` a child
 * on that platform can use (`WIN32_LOCATIONS`' child prefixes are Linux's, F5),
 * and a `wsl.exe` *query* uses that row too while starting no distribution
 * process at all (`src/main/wsl.ts`). Only a plan that hands `wsl.exe` a CLI to
 * run **inside** a distribution has a distribution process to state a share list
 * to, and only that plan sets this.
 */
export function childEnvironment(options: {
  readonly target: SpawnTarget
  readonly binaryPath: string
  readonly appEnv: Readonly<Record<string, string | undefined>>
  /** `platform` replaces `process.platform` when the child's `PATH` is built,
   * for the same reason `binDirectories` takes one: the platform's tool
   * directories are a fact about the platform the child will run on. */
  readonly platform?: string | undefined
  /** `wsl` states the WSL share list (`WSLENV_KEY`) empty. True for the WSL
   * route's own plans and false everywhere else, including the `wsl.exe`
   * queries. */
  readonly wsl?: boolean | undefined
}): Readonly<Record<string, string>> {
  const env: Record<string, string> = Object.create(null) as Record<string, string>
  for (const key of CHILD_ENV_KEYS) {
    if (key === 'PATH' || key === 'FUGARO_PROJECT') continue
    const value = options.appEnv[key]
    if (typeof value === 'string' && value !== '') env[key] = value
  }
  env['PATH'] = childPath(options.binaryPath, options.platform)
  env['FUGARO_PROJECT'] = options.target.project
  // Written unconditionally, and never read from `appEnv`: the host's own
  // `WSLENV` is the operator's business and not this console's, and a value
  // carried over from it would be a share list the console did not choose.
  if (options.wsl === true) env[WSLENV_KEY] = ''
  return Object.freeze(env)
}

/** childPath is the child's `PATH`, and it is a list rather than one directory
 * -- see rule 4 in this file's header and design section 3.4 rule 5. Its tail is
 * the platform's own tool directories (`locationsFor`), so the macOS Homebrew
 * prefix is not on a Linux child's `PATH` and Linuxbrew's system directory is
 * not on a macOS one's.
 *
 * **Its head is the binary's own directory, or nothing at all.** `dirname`
 * answers `.` for a path with no directory in it, and a `.` entry in a `PATH`
 * means *the child's own working directory* -- which, for a child started in the
 * operator's checkout, is the operator's checkout. That is not a directory the
 * console decided to put on the `PATH`, and on the WSL route it is the only
 * answer there could be: the CLI there is the bare name `fugaro`, resolved by
 * the distribution's own `PATH`, so the console has no directory of its own to
 * put in front of the tool list (F5, T-C5). The empty entry is dropped rather
 * than passed. */
export function childPath(binaryPath: string, platform: string = process.platform): string {
  const directory = dirname(binaryPath)
  const head = directory === '.' || directory === '' ? [] : [directory]
  return dedupe([...head, ...locationsFor(platform).childPathPrefixes]).join(':')
}

/**
 * privateWorkingDirectory returns the private, empty directory the app owns and
 * spawns from when the installation has no checkout (design section 3.4 rule 5).
 * It is created with mode `0700` under the system temporary directory, with the
 * uid in the **base** directory's name (`fugaro-console-<uid>`, and
 * `fugaro-console` where the platform has no uid) so two users on one machine
 * cannot collide, and it is never `/`, never the app bundle and never the
 * operator's home.
 *
 * **The uid used to be only in the leaf, and that was a defect.** With one
 * `fugaro-console` for the whole machine, the first user created the base 0700
 * and every other user who ran the console found a directory owned by someone
 * else at that name -- `assertSafeParent` refused it, correctly and with no way
 * for them to fix it, so the console could not spawn at all for the second
 * account and the per-uid leaf was never reached. A review found it; the base
 * carries the uid now. `assertSafeParent` still runs, and still has a job: a
 * directory or a symbolic link *someone else* planted at this user's own name is
 * refused rather than spawned from.
 *
 * **On a fine point of section 5.1 rule 10, said out loud rather than left to be
 * noticed:** that rule is about *records* -- no history, no cached document, no
 * crash dump, nothing that outlives the process -- and this creates a directory.
 * The reconciliation is that a directory is not a record at all: it exists so
 * that `doctor`'s `--dir` is a directory rather than `/`, and an empty directory
 * is the smallest thing that can satisfy "never the app's own default of `/`".
 * **The rule's wording changed on 2026-10-18** (the owner's C8 answer): it read
 * "the spawn log is the only thing the app ever writes to disk" and now reads
 * "the app writes the spawn log, when asked, and the installations file, and
 * nothing else". The installations file is the other write and it is a record;
 * it is reconciled there rather than here, because `src/main/installations.ts`
 * owns it and this module writes neither file. This directory is still neither.
 */
export function privateWorkingDirectory(options: {
  readonly base?: string
  readonly uid?: number | null
} = {}): string {
  const ownUid = typeof process.getuid === 'function' ? process.getuid() : null
  const uid = options.uid === undefined ? ownUid : options.uid
  // The uid is in the **base** name, which is the level two users on one machine
  // would otherwise share. With one `fugaro-console` for everybody, the first
  // user created it 0700 and every other user then found a directory owned by
  // someone else at that name: `assertSafeParent` refused it -- correctly, and
  // unfixably from their side, since it is not theirs to remove -- so the
  // console could not spawn **at all** for the second account, and the per-uid
  // leaf below was never reached. One operator per machine never notices
  // (`tmpdir()` is per-user on macOS); a shared `/tmp` is the case that does.
  const base =
    options.base ?? join(tmpdir(), uid === null ? PRIVATE_DIR_NAME : `${PRIVATE_DIR_NAME}-${uid}`)
  const path = join(base, PRIVATE_DIR_LEAF)
  // Each level is created only if it is missing, and then *checked* rather than
  // trusted: `mkdirSync({ recursive: true })` succeeds silently on a path that
  // already exists, so a directory -- or a symbolic link -- someone else put at
  // this name would otherwise be where every child runs. `lstat` does not follow
  // a link, so a link is seen as a link and refused.
  mkdirSync(base, { recursive: true, mode: PRIVATE_DIR_MODE })
  assertSafeParent(base, uid)
  try {
    mkdirSync(path, { mode: PRIVATE_DIR_MODE })
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error
  }
  const stat = lstatSync(path)
  if (stat.isSymbolicLink()) refused(`${path} is a symbolic link: the console will not spawn from it`)
  if (!stat.isDirectory()) refused(`${path} is not a directory: the console has nowhere to spawn from`)
  if (uid !== null && stat.uid !== uid) {
    refused(`${path} is owned by uid ${stat.uid}, not ${uid}: the console will not spawn from it`)
  }
  // A directory of our own with a wider mode (an older build, a umask) is taken
  // back to owner-only; one that still is not 0700 afterwards is refused.
  if ((stat.mode & 0o777) !== PRIVATE_DIR_MODE) chmodSync(path, PRIVATE_DIR_MODE)
  if ((lstatSync(path).mode & 0o777) !== PRIVATE_DIR_MODE) {
    refused(`${path} is not mode 0700: the console will not spawn from it`)
  }
  // The canonical spelling (`/private/var/...` on macOS), so the `--dir` the
  // child is given and the working directory it reports are one path.
  return realpathSync(path)
}

/** assertSafeParent refuses a base directory another user could rename the
 * private directory out of: a link, a non-directory, one owned by anyone but
 * this user or root, or one others may write to without the sticky bit. */
function assertSafeParent(base: string, uid: number | null): void {
  const stat = lstatSync(base)
  if (stat.isSymbolicLink()) refused(`${base} is a symbolic link: the console will not spawn under it`)
  if (!stat.isDirectory()) refused(`${base} is not a directory: the console has nowhere to spawn from`)
  if (uid === null) return
  if (stat.uid !== uid && stat.uid !== 0) {
    refused(`${base} is owned by uid ${stat.uid}, not ${uid}: the console will not spawn under it`)
  }
  if ((stat.mode & 0o022) !== 0 && (stat.mode & 0o1000) === 0) {
    refused(`${base} is writable by others without the sticky bit: the console will not spawn under it`)
  }
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
  /** Present on every real stream; optional so a test's fake need not have it.
   * `readText` calls it, and decodes any `Buffer` that still arrives with a
   * `StringDecoder`, so a UTF-8 character split across two chunks is one
   * character either way. */
  setEncoding?(encoding: BufferEncoding): unknown
  /** Present on every real stream; the supervisor calls it to let go of a pipe
   * a grandchild still holds once the child itself has exited (`startSidecar`). */
  destroy?(): unknown
}

/** readText subscribes to a child's stream as text. `chunk.toString()` on a
 * `Buffer` that ends in the middle of a UTF-8 character turns both halves into
 * U+FFFD; the stream's own `utf8` encoding, and the decoder for anything that
 * arrives as bytes regardless, carry the partial character to the next chunk. */
function readText(stream: ReadableLike | null, onText: (text: string) => void): void {
  if (stream === null) return
  stream.setEncoding?.('utf8')
  const decoder = new StringDecoder('utf8')
  stream.on('data', (chunk) => {
    const text = typeof chunk === 'string' ? chunk : decoder.write(chunk)
    if (text !== '') onText(text)
  })
}

/** SpawnedChildLike is the slice of `ChildProcess` this module uses, as an
 * interface so the test can pass a recording fake and assert on `shell` and on
 * the argv it was handed. */
export interface SpawnedChildLike {
  readonly stdout: ReadableLike | null
  readonly stderr: ReadableLike | null
  /** `spawn` fires once the process exists: Node emits it before any I/O and
   * never for a spawn that failed, where a missing or non-executable binary
   * hands back a `ChildProcess` and then emits `error` (ENOENT/EACCES) instead.
   * It is what the spawn log's one-line-per-child rule is held to. */
  on(event: 'spawn', listener: () => void): unknown
  on(event: 'error', listener: (error: Error) => void): unknown
  on(event: 'exit', listener: (code: number | null, signal: string | null) => void): unknown
  /** `close` comes after `exit`, once the child's stdio has drained: a reader
   * that must see every byte (the fixture recorder) settles on it. */
  on(event: 'close', listener: (code: number | null, signal: string | null) => void): unknown
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
 * SpawnPlan is one spawn, fully decided: **the program the OS starts
 * (`command`), the wrapper's own words (`prefix`), the CLI's argument vector
 * (`argv`, program word first)**, the working directory, the built environment,
 * the installation's project for the spawn log, and the restart schedule.
 * Nothing in it is read from a document.
 *
 * **Why the program and its argv are two fields rather than one array.** On
 * Windows the console does not start the CLI at all: it starts `wsl.exe`, which
 * starts the CLI *inside a distribution* (docs/console-windows/
 * implementation-plan.md, decision D1(a)). The words that run are then
 * `wsl.exe -d <distro> --cd <dir> -- fugaro …`, and three different things are
 * in that line: the program (`wsl.exe`), the wrapper's own flags (`-d … --`),
 * and the CLI's argv (`fugaro …`). Keeping them in one array would mean the
 * flag assertion could not tell a `wsl.exe` flag from a `fugaro` flag -- `--cd`
 * is not a flag this console may emit to the CLI, and `--` is not one at all --
 * and `runToCompletion` would read the binary out of a slot that, on this route,
 * holds the word `fugaro`.
 *
 * **`argv` is what the CLI receives, program word first**, so the plan says the
 * same thing on both routes: the host route's `argv[0]` is the resolved binary's
 * path, the WSL route's is the bare name `fugaro` (resolved by the
 * distribution's own `PATH`, which is the point of running it inside). The
 * spawn log's `argv` line is the *whole* command line -- see `spawnedArgv` --
 * and is therefore unchanged on the host route, where the wrapper is empty.
 */
export interface SpawnPlan {
  /** `operation` is the CLI operation this plan runs, or the `wsl.exe` question
   * it asks (`WslQueryName`). Nothing in this module reads it: it is what the
   * plan says about itself, and `wsl.ts` is the one place that builds the
   * second kind. */
  readonly operation: SpawnOperationName | WslQueryName
  /** `command` is the program handed to the spawner: the resolved binary on the
   * host route, `wsl.exe` on the WSL route. It is what `runToCompletion` and
   * `startSidecar` start, and what the spawn log's first word is. */
  readonly command: string
  /** `prefix` is the wrapper's own words, between the command and the CLI's
   * argv, and it is empty on the host route. On the WSL route it is
   * `['-d', <distro>, '--cd', <dir>, '--']` -- the distribution to run in, the
   * directory to start in (in the **distribution's** spelling: the console
   * translates before it builds the plan), and the separator after which the
   * CLI's own argv begins. It is deliberately not part of `argv`: these words
   * belong to `wsl.exe` and are asserted as `wsl.exe`'s, not as the CLI's. */
  readonly prefix: readonly string[]
  readonly argv: readonly string[]
  readonly cwd: string
  readonly env: Readonly<Record<string, string>>
  readonly project: string
  readonly backoff: Backoff
}

/**
 * WslRoute is how a plan reaches the CLI inside a distribution (T-C5, decisions
 * D1(a) and D2(a)): the distribution's name, the directory the CLI starts in,
 * and -- for a test only -- which `wsl.exe` to run.
 *
 * **`directory` is the distribution's spelling, not the host's.** The console
 * translates before it builds the plan (T-D3: `wslpath`, D4(b)) because a
 * `C:\...` path handed to a Linux child is the wrong-spelling bug T-A2's fake
 * refuses by name. It may be `undefined`, and then the console names **no**
 * directory at all -- neither `--cd` nor `doctor`'s `--dir` -- and the
 * distribution starts the CLI where it starts it. That is the honest answer
 * while the translation does not exist yet (T-D4 fills this in with the
 * distribution's spelling of the private working directory): inventing a
 * directory, or passing the host's, would be a path the operator never chose.
 *
 * **`executable` is `wsl.exe`, and it is not the CLI.** It is left out in the
 * app, where `wslExecutable(appEnv)` computes the absolute path, and set by a
 * test that must point the route at the fake (T-A1).
 *
 * **The plan's `binaryPath` is the record's Linux spelling of the CLI, not the
 * program word.** The name `fugaro` is what `wsl.exe` hands the distribution, and
 * the record's path is what the console states and what leads the child's `PATH`;
 * `''` is the store's own spelling of "no path of my own" and the ordinary case.
 */
export interface WslRoute {
  readonly distro: string
  readonly directory?: string | undefined
  readonly executable?: string | undefined
}

/** wslWrapper is the wrapper's own words: the distribution to run in, the
 * directory to start in (in the distribution's spelling), and the separator
 * after which the CLI's argv begins. `--cd` is left out when there is no
 * directory to name -- see `WslRoute`.
 *
 * It is the only producer of a prefix, so `assertWslPrefix` is called here
 * rather than in the callers (T-E3): the distribution's name is a string that
 * came out of `wsl.exe` or out of the store, and it is checked against
 * `DISTRO_NAME_RE` on the way to `wsl.exe`'s command line. */
function wslWrapper(wsl: WslRoute): readonly string[] {
  const head = ['-d', wsl.distro]
  const prefix = wsl.directory === undefined ? [...head, '--'] : [...head, '--cd', wsl.directory, '--']
  assertWslPrefix(prefix)
  return Object.freeze(prefix)
}

/** planFor assembles a plan from an operation, its validated arguments and
 * main-process state. It is the only producer of a `SpawnPlan`, and it builds
 * both routes: the **host** route, where the command is the resolved binary and
 * the wrapper is empty, and the **WSL** route (`context.wsl`), where the command
 * is `wsl.exe`, the wrapper is `['-d', <distro>, '--cd', <dir>, '--']`, and the
 * CLI's program word is `fugaro` (T-C5).
 *
 * **What differs between the two routes, and why.** The program word: on the
 * host route it is the resolved binary's absolute path, because that is the file
 * that runs; on the WSL route it is the bare name `fugaro`, because the
 * distribution's own `PATH` is what finds the CLI there -- that is the point of
 * running it inside (T-C5). The child's `PATH`: Linux's, on both the host
 * route's win32 row and here (F5). And the working directory: `cwd` is where
 * `wsl.exe` itself is started, which is a **host** directory, while the
 * directory the CLI starts in is the wrapper's `--cd` and `argvFor`'s `--dir` --
 * two different directories, which is why they are two different fields.
 */
export function planFor(
  operation: SpawnOperationName,
  args: ValidatedArguments,
  context: {
    readonly target: SpawnTarget
    readonly binaryPath: string
    readonly directory: string
    readonly appEnv: Readonly<Record<string, string | undefined>>
    readonly wsl?: WslRoute | undefined
    readonly backoff?: Backoff
  },
): SpawnPlan {
  const wsl = context.wsl
  if (wsl !== undefined) {
    return Object.freeze({
      operation,
      command: wsl.executable ?? wslExecutable(context.appEnv),
      prefix: wslWrapper(wsl),
      // **The program word is the name, and never a path.** The distribution's
      // own `PATH` resolves it, which is the point of running the CLI inside
      // (T-C5). A record's `binaryPath` is the *Linux spelling* of the same file
      // (F15) and it is not what runs: it is what the console states (the
      // resolution's `path`, D3(a)) and its directory leads the child's `PATH`
      // below, so the `fugaro` the name resolves to is the one the record names.
      argv: Object.freeze([
        FUGARO_BINARY_NAME,
        ...argvFor(operation, args, { target: context.target, directory: wsl.directory }),
      ]),
      // `cwd` is the host directory the console starts `wsl.exe` in. It is not
      // the CLI's directory: that one is inside the distribution and is the
      // wrapper's `--cd`.
      cwd: context.directory,
      // The `PATH` head is the record's own directory, exactly as on the host
      // route, so a name and a stored path agree about which file runs; `''`
      // (the ordinary case: no path of its own) puts nothing in front of the
      // distribution's tool list.
      env: childEnvironment({
        target: context.target,
        binaryPath: context.binaryPath,
        appEnv: context.appEnv,
        platform: 'win32',
        // The WSL route is the one plan that starts a distribution process, so
        // it is the one environment that states `WSLENV` empty (T-E2, D5(a)).
        wsl: true,
      }),
      project: context.target.project,
      backoff: context.backoff ?? DEFAULT_BACKOFF,
    })
  }
  const argv = argvFor(operation, args, { target: context.target, directory: context.directory })
  return Object.freeze({
    operation,
    command: context.binaryPath,
    prefix: Object.freeze([]) as readonly string[],
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

/**
 * wrapperArgs is the words handed to `command` beyond the program itself: the
 * wrapper's prefix, then the CLI's argv.
 *
 * The CLI's program word is dropped **exactly when the command already is that
 * program** -- the host route, where `argv[0]` is the resolved binary's path and
 * the OS would otherwise be handed it twice. On the WSL route the command is
 * `wsl.exe` and the program word is `fugaro`, a word the distribution resolves,
 * so it is kept. The rule is stated as the equality rather than as a route flag
 * because the equality is the thing that matters: the program word is not
 * repeated when the command *is* it.
 */
function wrapperArgs(plan: SpawnPlan): readonly string[] {
  const cli = plan.argv[0] === plan.command ? plan.argv.slice(1) : plan.argv
  return [...plan.prefix, ...cli]
}

/**
 * spawnedArgv is the **whole command line as it is spawned**: the command, the
 * wrapper's words, then the CLI's argv. It is what the spawn log records, so the
 * log's `argv` line stays the operator's record of what ran (D1) on both routes
 * -- unchanged on the host route, where there is no wrapper, and
 * `['C:\\…\\wsl.exe', '-d', 'Ubuntu', '--cd', '/home/dimi', '--', 'fugaro', 'version']`
 * on the WSL one. It is the first element of this array that is the program, and
 * that is why the log's line is still "the binary's absolute path first".
 */
export function spawnedArgv(plan: SpawnPlan): readonly string[] {
  return [plan.command, ...wrapperArgs(plan)]
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
 *
 * Every one of those reads has a deadline (`READ_TIMEOUT_MS` unless the caller
 * gives one) and a stdout cap (`MAX_STDOUT_LENGTH`); stderr is bounded as
 * before. A child that misses the deadline or outgrows the cap is stopped --
 * `SIGTERM`, then `SIGKILL` after the grace period -- and the promise rejects
 * with a sentence that says which, so no read can leave a screen waiting or a
 * process running.
 */
export function runToCompletion(
  plan: SpawnPlan,
  deps: {
    readonly spawner?: ChildSpawner
    readonly log?: SpawnLogWriter
    readonly maxStderrBytes?: number
    readonly maxStdoutLength?: number
    readonly timeoutMs?: number
    readonly timers?: Timers
    readonly shutdownGraceMs?: number
  },
): Promise<RunResult> {
  const spawner = deps.spawner ?? nodeSpawner
  const limit = deps.maxStderrBytes ?? MAX_STDERR_BYTES
  const stdoutLimit = deps.maxStdoutLength ?? MAX_STDOUT_LENGTH
  const timeoutMs = deps.timeoutMs ?? READ_TIMEOUT_MS
  const timers = deps.timers ?? NODE_TIMERS
  const grace = deps.shutdownGraceMs ?? SHUTDOWN_GRACE_MS
  const binary = plan.command
  if (binary === '') refused('a spawn plan with no binary is not a plan')
  const what = plan.argv.slice(1).filter((word) => !word.startsWith('-')).slice(0, 2).join(' ')

  return new Promise<RunResult>((resolve, reject) => {
    let child: SpawnedChildLike
    try {
      child = spawner(binary, wrapperArgs(plan), {
        cwd: plan.cwd,
        env: plan.env,
        shell: false,
        stdio: ['ignore', 'pipe', 'pipe'],
      })
    } catch (error) {
      reject(error instanceof Error ? error : new Error(String(error)))
      return
    }
    // Recorded when the child process exists, which is what `spawn` means (`spawnlog.ts`'s
    // one line per child). It is *not* the spawner's return: `spawn` hands back
    // a `ChildProcess` before it knows whether there is a process, and a binary
    // that is missing or not executable produces none -- it emits `error`,
    // never `spawn` -- so recording on return wrote a line for a child that
    // never ran (review). A spawner that throws, above, is the other half of
    // the same rule and records nothing either.
    child.on('spawn', () => {
      deps.log?.record({ argv: spawnedArgv(plan), cwd: plan.cwd, project: plan.project })
    })
    let stdout = ''
    let stderr = ''
    let settled = false
    let exited = false
    let deadline: ReturnType<typeof setTimeout> | null = null

    const clearDeadline = (): void => {
      if (deadline !== null) {
        timers.clearTimeout(deadline)
        deadline = null
      }
    }
    // Stopping first, rejecting second: the caller's rejection must mean "the
    // child has been told to go", not "the child is still out there".
    const abandon = (error: Error): void => {
      if (settled) return
      settled = true
      clearDeadline()
      if (!exited) {
        child.kill('SIGTERM')
        timers.setTimeout(() => {
          if (!exited) child.kill('SIGKILL')
        }, grace)
      }
      reject(error)
    }

    deadline = timers.setTimeout(() => {
      deadline = null
      abandon(new SpawnTimeout(`fugaro ${what} did not finish within ${Math.round(timeoutMs / 1000)}s`))
    }, timeoutMs)

    readText(child.stdout, (text) => {
      if (settled) return
      if (stdout.length + text.length > stdoutLimit) {
        abandon(new Error(`fugaro ${what} printed more than ${stdoutLimit} characters`))
        return
      }
      stdout += text
    })
    readText(child.stderr, (text) => {
      stderr = boundAppend(stderr, text, limit)
    })
    child.on('error', (error) => {
      if (settled) return
      settled = true
      clearDeadline()
      reject(error)
    })
    // `exit` is recorded but does not settle the read. Node fires it when the
    // child is reaped, and its stdio streams may still be open at that moment:
    // a last chunk that arrives afterwards belongs to the value, and a reader
    // that resolved on `exit` would hand back a torn document -- a screen then
    // refuses a read that actually succeeded. `close` is the event that says
    // every byte has been read, which is what this function's name promises and
    // what `SpawnedChildLike`'s own comment above already states (and what
    // `tools/record-fixture/index.ts` settles on for exactly this reason).
    //
    // The deadline stays the backstop for the other side of the trade: a stream
    // a stray grandchild holds open past the child's exit is refused with
    // `SpawnTimeout` rather than resolved with whatever had arrived.
    child.on('exit', () => {
      exited = true
    })
    child.on('close', (code, signal) => {
      exited = true
      if (settled) return
      settled = true
      clearDeadline()
      resolve({ code, signal, stdout, stderr })
    })
  })
}

/** SpawnTimeout is the rejection of a read that missed its deadline, as its
 * own class so `probeVersion` can tell it from a spawn that failed outright. */
export class SpawnTimeout extends Error {
  override readonly name = 'SpawnTimeout'
}

/** boundAppend keeps the last `limit` code units of `current + chunk`: stderr's
 * tail is where a CLI puts its refusal, and a screen shows the sentence that
 * comes out of it (`cliSentence`). The cut can land between the two halves of a
 * surrogate pair, and half a pair is not text -- it is a lone surrogate, which
 * one drawer renders as a replacement character and another as nothing at all.
 * A leading *low* surrogate is the proof of such a cut, so it is dropped, the
 * same step `src/main/ipc.ts`'s `boundReason` takes at the other end of its
 * string. */
function boundAppend(current: string, chunk: string, limit: number): string {
  const next = current + chunk
  if (next.length <= limit) return next
  let from = next.length - limit
  const first = next.charCodeAt(from)
  if (first >= 0xdc00 && first <= 0xdfff) from += 1
  return next.slice(from)
}

/**
 * BinaryRefusalKind is **which** failure a resolution is, and it exists because
 * the four of them are four different instructions to the operator (Part G,
 * T-G1). A screen that showed "no fugaro was found" for all four would send
 * somebody to install a CLI when what is missing is WSL, and would send somebody
 * to install WSL when the CLI is already there and merely not on the
 * distribution's `PATH`.
 *
 *   - `no-binary` -- the existing state: a path that is not absolute, not there,
 *     not an executable file, or a CLI that is there and will not answer. This
 *     is the state the no-binary page has always drawn, and it is the fallback
 *     for anything this vocabulary does not name;
 *   - `no-wsl` -- `wsl.exe` could not answer at all (T-C4's `absent` outcome):
 *     WSL is not installed, or the feature is off;
 *   - `no-distro` -- WSL answered and the distribution this installation names
 *     is not among them;
 *   - `no-fugaro-in-distro` -- the distribution is there and the CLI is not in
 *     it: nothing named `fugaro` is on the distribution's own `PATH`.
 *
 * The kinds are the *console's* vocabulary and not the CLI's: no CLI exit status
 * reaches a screen through this field.
 */
export type BinaryRefusalKind = 'no-binary' | 'no-wsl' | 'no-distro' | 'no-fugaro-in-distro'

export type BinaryResolution =
  | {
      readonly ok: true
      readonly path: string
      readonly version: string
      /** `configured` when the candidate is the path the installation named,
       * `well-known` for every other candidate rule 4's list produced -- the
       * well-known prefixes, which are one set of "where else it can be" and
       * stay one word so that no caller can read a distinction the resolution
       * does not make. */
      readonly source: 'configured' | 'well-known'
    }
  | {
      readonly ok: false
      readonly path: string | null
      readonly kind: BinaryRefusalKind
      readonly reason: string
    }

/**
 * resolveBinary walks rule 4's order and returns the first candidate that is an
 * executable file *and* reports a version. A candidate that exists but whose
 * version cannot be read is refused rather than skipped -- see rule 3 in this
 * file's header -- and an order with no candidate at all is refused with the
 * whole list in the message, because the screen that follows says what to
 * install and where.
 *
 * A configured path that is not absolute is refused before the walk begins, and
 * for the same reason: it would be checked against the app's own working
 * directory and then run against the child's, so the file that answered the
 * version check need not be the file that runs.
 *
 * The configured path is a *candidate* and not a decision (owner's ruling of
 * 2026-10-20, §3.4 rule 4): a store that already carries one is asked first, and
 * if it has gone the walk carries on to the well-known locations rather than
 * refusing. The console never asks the operator for a binary path.
 */
export async function resolveBinary(options: {
  readonly configuredPath?: string | undefined
  readonly appEnv: Readonly<Record<string, string | undefined>>
  readonly home?: string
  readonly prefixes?: readonly string[] | undefined
  /** `platform` is the host whose filesystem is being asked: it picks the
   * well-known list *and* the mode question `isExecutableFile` answers, so the
   * two cannot disagree (T-C7). It replaces `process.platform` for a test, and
   * an injected platform cannot choose a list this module does not know --
   * `binDirectories` reads it through `locationsFor`. */
  readonly platform?: string | undefined
  readonly directory: string
  readonly target: SpawnTarget
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timeoutMs?: number
}): Promise<BinaryResolution> {
  // Before the walk, and refusing rather than skipping: a configured path that
  // is not absolute names a file relative to the app's own working directory --
  // which is nothing the operator chose, and not the directory the child runs in
  // (rule 5). Walking on would resolve some other installation and report its
  // numbers under the operator's chosen path (review of Task 4.1).
  if (options.configuredPath !== undefined && options.configuredPath !== '' && !isAbsolute(options.configuredPath)) {
    return {
      ok: false,
      path: options.configuredPath,
      kind: 'no-binary',
      // The sentence names the file and the field, and asks for nothing: the
      // ruling of 2026-10-20 removed every screen that took a binary path, so
      // "give an absolute path" would describe an affordance the console no
      // longer draws. The one place an override can come from is the field.
      reason: `the binaryPath in installations.json is not absolute: ${options.configuredPath} -- that field names the file the console runs, so it is read as written and never against a working directory`,
    }
  }
  const candidates = binDirectories({
    configuredPath: options.configuredPath,
    ...(options.home === undefined ? {} : { home: options.home }),
    ...(options.prefixes === undefined ? {} : { prefixes: options.prefixes }),
    ...(options.platform === undefined ? {} : { platform: options.platform }),
  })
  for (const candidate of candidates) {
    if (!isExecutableFile(candidate, options.platform)) continue
    const plan = planFor('cli.version', {}, {
      target: options.target,
      binaryPath: candidate,
      directory: options.directory,
      appEnv: options.appEnv,
    })
    const probe = await probeVersion(plan, options)
    if (!probe.ok) return { ok: false, path: candidate, kind: 'no-binary', reason: probe.reason }
    const source = configuredSource(options.configuredPath, candidate)
    return { ok: true, path: candidate, version: probe.version, source }
  }
  return {
    ok: false,
    path: null,
    kind: 'no-binary',
    reason: `no fugaro binary was found: looked at ${candidates.join(', ')}`,
  }
}

/**
 * resolveInstalledBinary reads **exactly one** binary: the one an installation
 * names. It never walks rule 4's list.
 *
 * That is the difference from `resolveBinary` with a `configuredPath`, and it is
 * the whole point (desktop-console plan Task 3.4b, step 4: "its handshake decides
 * the window"). `resolveBinary` skips a configured path that is missing or not
 * executable and carries on to the platform's well-known directories -- so a
 * stored installation whose binary had gone would be answered by some *other*
 * `fugaro`. Here a stored binary that is not there answers `path: null`, one
 * that is there but is not an executable regular file answers its own path, and
 * a probe that fails answers its reason -- and in every case nothing else on the
 * machine is asked.
 *
 * The startup handshake no longer calls this (owner's ruling of 2026-10-20: an
 * installation's binary is an override, not a pin -- see
 * `resolveHandshakeBinary`). Its remaining caller is the screenshot isolation
 * check (`tools/screenshots/isolation.ts`), which must prove that exactly the
 * fixture binary answered and nothing else on the machine did.
 */
export async function resolveInstalledBinary(options: {
  readonly binaryPath: string
  /** `distro` is the WSL distribution this installation's CLI runs in (D2(a)),
   * or absent for an installation on the host. When it is set the binary is a
   * program **inside** the distribution: the host's `statSync` cannot answer for
   * it (F4, T-C6) and the question "is it there, and does it run?" is the
   * distribution's, through the same `version` probe every other route uses. */
  readonly distro?: string | undefined
  /** `wslDirectory` is the directory the CLI starts in, in the **distribution's**
   * spelling (T-D3). Absent means the console names none and the distribution
   * chooses -- see `WslRoute`. */
  readonly wslDirectory?: string | undefined
  /** `wslPath` replaces the `wsl.exe` `wslExecutable` computes: how a test
   * points the route at the fake (T-A1), and the only reason it exists. */
  readonly wslPath?: string | undefined
  readonly appEnv: Readonly<Record<string, string | undefined>>
  /** `platform` is the host's, for the mode question below (T-C7). The WSL
   * branch never asks it: the distribution's CLI is not on this filesystem at
   * all (F4, T-C6). */
  readonly platform?: string | undefined
  readonly directory: string
  readonly target: SpawnTarget
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timeoutMs?: number
}): Promise<BinaryResolution> {
  if (options.distro !== undefined && options.distro !== '') {
    return resolveInDistribution(options, options.distro)
  }
  const path = options.binaryPath
  if (path === '' || !isAbsolute(path)) {
    return {
      ok: false,
      path,
      kind: 'no-binary',
      reason: `the installation's binary path is not absolute: ${path}`,
    }
  }
  if (!existsSync(path)) {
    return {
      ok: false,
      path: null,
      kind: 'no-binary',
      reason: `the installation's binary is not there: ${path}`,
    }
  }
  if (!isExecutableFile(path, options.platform)) {
    return {
      ok: false,
      path,
      kind: 'no-binary',
      reason: `the installation's binary is not an executable file: ${path}`,
    }
  }
  const plan = planFor('cli.version', {}, {
    target: options.target,
    binaryPath: path,
    directory: options.directory,
    appEnv: options.appEnv,
  })
  const probe = await probeVersion(plan, options)
  if (!probe.ok) return { ok: false, path, kind: 'no-binary', reason: probe.reason }
  return { ok: true, path, version: probe.version, source: 'configured' }
}

/**
 * resolveInDistribution is the WSL half of `resolveInstalledBinary` (T-C5), and
 * it is where the four refusals are told apart. Each one is a different
 * instruction, which is the whole reason `BinaryRefusalKind` exists:
 *
 *   - **`no-wsl`** -- `wsl.exe` could not answer at all (T-C4's `absent`
 *     outcome): WSL is not installed, or the feature is off. The operator's
 *     instruction is to install WSL, not to install a CLI;
 *   - **`no-distro`** -- WSL answered, and the distribution this installation
 *     names is not among them. The list `wsl.exe` gave is in the sentence,
 *     because the distribution was renamed or removed and the operator is the
 *     only one who can say which;
 *   - **`no-fugaro-in-distro`** -- the distribution is there and the CLI is not
 *     in it: the probe came back as a shell's miss (see
 *     `SHELL_COMMAND_NOT_FOUND`). The instruction is to install the CLI inside
 *     the distribution;
 *   - **the probe's own refusal, unchanged** -- the distribution is there and
 *     the CLI answered and the answer was not a version. A broken CLI must not
 *     be reported as a missing one, so this is the same refusal the host route
 *     has always produced, kind `no-binary`.
 *
 * **The classification is by the probe, not by a second call.** The `version`
 * probe that decides the happy path is the only thing asked whether the CLI is
 * there; a second question -- `command -v fugaro`, say -- would be another
 * process in the common path, and U15 records that a *cold-starting*
 * distribution can take seconds to answer its first invocation. So a healthy
 * WSL resolution costs one `wsl.exe --list` (the enumeration, which is cached by
 * the caller when it matters), and one `fugaro version`.
 *
 * **Exactly one distribution is tried** (F7): the one the installation names.
 * A missing distribution is a stated failure, never a quiet switch to the
 * default -- the operator would be reading another installation's numbers under
 * the name of the one they chose.
 */
async function resolveInDistribution(
  options: {
    readonly binaryPath: string
    readonly appEnv: Readonly<Record<string, string | undefined>>
    readonly directory: string
    readonly target: SpawnTarget
    readonly wslDirectory?: string | undefined
    readonly wslPath?: string | undefined
    readonly spawner?: ChildSpawner
    readonly log?: SpawnLogWriter
    readonly timeoutMs?: number
  },
  distro: string,
): Promise<BinaryResolution> {
  const listed = await wslDistributions({
    appEnv: options.appEnv,
    // The query is `wsl.exe` itself, so its `cwd` is a **host** directory --
    // the same one the plan will start `wsl.exe` in.
    cwd: options.directory,
    project: options.target.project,
    ...(options.wslPath === undefined ? {} : { wslPath: options.wslPath }),
    ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
    ...(options.log === undefined ? {} : { log: options.log }),
    ...(options.timeoutMs === undefined ? {} : { timeoutMs: options.timeoutMs }),
  })
  if (listed.kind === 'absent') {
    return { ok: false, path: null, kind: 'no-wsl', reason: listed.reason }
  }
  if (!listed.distributions.some((one) => one.name === distro)) {
    const names = listed.distributions.map((one) => one.name)
    return {
      ok: false,
      path: null,
      kind: 'no-distro',
      reason:
        names.length === 0
          ? `this machine has no WSL distributions, and this installation names ${quote(distro)}`
          : `this machine has no distribution named ${quote(distro)}: wsl.exe lists ${names
              .map(quote)
              .join(', ')}`,
    }
  }

  // The word `wsl.exe` hands the distribution: the bare name, which the
  // distribution's own `PATH` resolves. The record's path, when it has one, is
  // what the console *states* -- it is a Linux spelling inside the distribution
  // (F15) and it is the same file the name resolves to, because `planFor` puts
  // its directory at the head of the child's `PATH`.
  const reported = options.binaryPath === '' ? FUGARO_BINARY_NAME : options.binaryPath
  const plan = planFor('cli.version', {}, {
    target: options.target,
    binaryPath: options.binaryPath,
    directory: options.directory,
    appEnv: options.appEnv,
    wsl: {
      distro,
      ...(options.wslDirectory === undefined ? {} : { directory: options.wslDirectory }),
      ...(options.wslPath === undefined ? {} : { executable: options.wslPath }),
    },
  })
  const probe = await probeVersion(plan, options)
  if (!probe.ok) {
    if (isShellMiss(probe)) {
      return {
        ok: false,
        path: reported,
        kind: 'no-fugaro-in-distro',
        reason:
          `the distribution ${quote(distro)} has no ${quote(FUGARO_BINARY_NAME)}: ` +
          `${probe.said ?? 'the distribution said nothing'}`,
      }
    }
    return { ok: false, path: reported, kind: 'no-binary', reason: probe.reason }
  }
  return { ok: true, path: reported, version: probe.version, source: 'configured' }
}

/** SHELL_COMMAND_NOT_FOUND is the status a POSIX shell uses for a command it
 * could not find (127), and `126` for one it found and could not execute. It is
 * a convention rather than a guarantee, which is why it is only ever read
 * together with the sentence -- see `isShellMiss`. */
export const SHELL_COMMAND_NOT_FOUND = 127

/** SHELL_MISS_RE is how a shell says it could not find the command. It is
 * deliberately narrow: "not found" alone is a sentence a CLI prints about its
 * own config file, and a CLI that cannot read its config is not a CLI that is
 * missing. Only a shell's own spellings count, and only beside the status. */
const SHELL_MISS_RE = /command not found|not found, but can be installed|: not found/i

/** isShellMiss answers whether a failed probe is a shell saying the command was
 * not there -- **both** halves of it, because either half alone is ambiguous: a
 * CLI may exit 127 for its own reasons, and a CLI may print "not found" about a
 * file it was asked to read. */
function isShellMiss(probe: VersionProbe): boolean {
  if (probe.ok) return false
  if (probe.code !== SHELL_COMMAND_NOT_FOUND) return false
  return probe.said !== null && SHELL_MISS_RE.test(probe.said)
}

/** quote spells a name for a sentence an operator reads. */
function quote(value: string): string {
  return JSON.stringify(value)
}

/**
 * resolveHandshakeBinary is the startup handshake's resolution, in the one
 * place a unit test can drive it (`src/main/index.ts` imports the Electron
 * runtime and cannot be imported under `vitest`).
 *
 * With an active installation it is rule 4's own walk with that installation's
 * optional override asked first, and that installation's project as the target,
 * so the spawn log records which installation the bare `version` was for. With
 * none, the same walk with the project-less target -- and **no** project from
 * anywhere else, not `FUGARO_PROJECT`, not the working directory (D13: no
 * guess).
 *
 * An installation's binary is an *override* and never a pin (owner's ruling of
 * 2026-10-20, §3.4 rule 4): the console resolves the `fugaro` that is installed
 * on this machine, so a store entry that names a path which has since gone is
 * not a refusal but the next candidate in the order. That replaces the narrower
 * `resolveInstalledBinary` this function used to call for an active
 * installation, which stopped at the stored path (review of 3.4b, B1): correct
 * while the path was the operator's choice and asked for on first run, wrong now
 * that the first run asks for nothing.
 *
 * **The WSL route is the one exception, and it is the opposite rule.** An
 * installation that names a `distro` is read **inside** that distribution, by
 * `resolveInstalledBinary` -- because on Windows there is no walk to fall through
 * to, and the distribution is the installation's identity rather than a
 * candidate. See the comment on the branch below.
 */
export async function resolveHandshakeBinary(options: {
  readonly installation: {
    readonly binaryPath?: string | undefined
    readonly distro?: string | undefined
    readonly checkout?: string | undefined
    readonly target: SpawnTarget
  } | null
  readonly appEnv: Readonly<Record<string, string | undefined>>
  readonly directory: string
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly home?: string
  readonly prefixes?: readonly string[] | undefined
  /** `wslPath` replaces the `wsl.exe` the route computes: how a test points the
   * handshake at the fake (T-A1). */
  readonly wslPath?: string | undefined
}): Promise<BinaryResolution> {
  const shared = {
    appEnv: options.appEnv,
    directory: options.directory,
    ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
    ...(options.log === undefined ? {} : { log: options.log }),
    ...(options.home === undefined ? {} : { home: options.home }),
    ...(options.prefixes === undefined ? {} : { prefixes: options.prefixes }),
  }
  const installation = options.installation
  // **On the WSL route the distro is the installation's identity, not an
  // override.** Everywhere else an installation's binary is a *candidate* that
  // rule 4 asks first and carries on from when it has gone (owner's ruling of
  // 2026-10-20). There is nothing to carry on *to* inside a distribution: the
  // walk is a walk of the host, the host has no `fugaro` to find (T-C2), and a
  // console that answered a renamed distribution by looking for a second one
  // would be reading numbers from an installation the operator did not choose
  // (F7). So a `distro` sends the read inside it, and a distribution that has
  // gone is a stated refusal -- `no-distro` -- rather than a silent switch.
  if (installation !== null && installation.distro !== undefined && installation.distro !== '') {
    return resolveInstalledBinary({
      ...shared,
      binaryPath: installation.binaryPath ?? '',
      distro: installation.distro,
      ...(installation.checkout === undefined ? {} : { wslDirectory: installation.checkout }),
      ...(options.wslPath === undefined ? {} : { wslPath: options.wslPath }),
      target: installation.target,
    })
  }
  if (installation !== null) {
    return resolveBinary({
      ...shared,
      ...(installation.binaryPath === undefined
        ? {}
        : { configuredPath: installation.binaryPath }),
      target: installation.target,
    })
  }
  return resolveBinary({ ...shared, target: NO_INSTALLATION_TARGET })
}

/**
 * NO_INSTALLATION_TARGET is the target the startup read runs with when no
 * installation is active: no project and no config path, because `fugaro
 * version` takes neither (`COMMANDS_WITHOUT_PROJECT`). Inventing a project here,
 * or falling back to the ambient `FUGARO_PROJECT`, would be a target nobody
 * chose (design section 3.4 rule 1).
 */
export const NO_INSTALLATION_TARGET: SpawnTarget = Object.freeze({ project: '' })

function configuredSource(configuredPath: string | undefined, candidate: string): 'configured' | 'well-known' {
  if (configuredPath !== undefined && configuredPath === candidate) return 'configured'
  return 'well-known'
}

/** VersionProbe is what `probeVersion` learned: the version, or why there is
 * none -- with the child's own exit status and its own last word beside it,
 * because one caller classifies by them (T-C5's "the distribution has no
 * `fugaro`" is a shell's 127-and-a-sentence, and the plan's rule is that the
 * classification is by the *probe* rather than by a second call).
 *
 * `code` is `null` when there was no status to read: a child that was killed, or
 * one that missed the deadline. `said` is what the child printed on stderr,
 * trimmed, or `null` when it printed nothing -- and it is the **raw** text, not
 * `cliSentence`'s, because the sentence being read here is a *shell's* about a
 * missing command and not the CLI's about a refused read. */
export type VersionProbe =
  | { readonly ok: true; readonly version: string }
  | {
      readonly ok: false
      readonly code: number | null
      readonly said: string | null
      readonly reason: string
    }

/** probeVersion runs `<binary> version` and reads the one human line it prints.
 * It is a spawn like any other: the spawn log records it, with the
 * installation's project, which is what Check 59a's `sort -u` over `.project`
 * depends on. */
export async function probeVersion(
  plan: SpawnPlan,
  deps: {
    readonly spawner?: ChildSpawner
    readonly log?: SpawnLogWriter
    readonly timeoutMs?: number
    readonly timers?: Timers
    readonly shutdownGraceMs?: number
  },
): Promise<VersionProbe> {
  // A probe that times out stops its child, exactly as every other stop path in
  // this module does: `SIGTERM`, then `SIGKILL` for the child that ignores it
  // (section 10.12). That is `runToCompletion`'s own deadline now, with the
  // handshake's shorter budget; walking away from a hung binary would leave it
  // running, reparented to init when the app quits.
  let result: RunResult
  try {
    result = await runToCompletion(plan, {
      ...deps,
      timeoutMs: deps.timeoutMs ?? VERSION_TIMEOUT_MS,
    })
  } catch (error) {
    if (error instanceof SpawnTimeout) {
      return { ok: false, code: null, said: null, reason: 'the binary did not answer `version` in time' }
    }
    throw error
  }
  if (result.signal !== null) {
    return {
      ok: false,
      code: null,
      said: null,
      reason: `the binary was killed by ${result.signal} while reporting its version`,
    }
  }
  const said = result.stderr.trim() === '' ? null : result.stderr.trim()
  if (result.code !== 0) {
    return {
      ok: false,
      code: result.code,
      said,
      reason: `the binary exited ${String(result.code)} for \`version\`: ${cliSentence(result.stderr) ?? 'no message'}`,
    }
  }
  // The child answered and the answer was not a version: `code` is 0, because
  // the status is the child's own and it is not the failure here. A caller that
  // classifies by the status must not read this as "the command was not found".
  const parsed = parseVersionLine(result.stdout)
  return parsed.ok ? parsed : { ok: false, code: result.code, said, reason: parsed.reason }
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
    readonly maxLineLength?: number
  } = {},
): Sidecar {
  const spawner = deps.spawner ?? nodeSpawner
  const timers = deps.timers ?? NODE_TIMERS
  const clock = deps.clock ?? ((): Date => new Date())
  const grace = deps.shutdownGraceMs ?? SHUTDOWN_GRACE_MS
  const tracker = new FeedTracker()
  const maxLine = deps.maxLineLength ?? MAX_FEED_LINE_LENGTH

  let stopped = false
  let spawns = 0
  let attempt = 0
  let child: SpawnedChildLike | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let buffer = ''
  /** `discarding` is true while the rest of an over-long line is being dropped:
   * everything up to its newline is thrown away, and reading resumes after. */
  let discarding = false
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
    // Review round 5 of PR 478: this used to close `deps.log` here. The sidecar
    // never *opens* a log -- `deps.log` is always a writer it was handed -- and
    // design section 5.1 rule 10 wants one line per child process the app
    // spawns, so a feed that ends must leave the log open for the next child
    // rather than ending the writer the whole session shares. The closer is
    // whoever opened it: `CliSource.close()` when the source opened it, the
    // caller when the caller injected it (the rule `#ownsLog` states, and the
    // one `runToCompletion` in this same file already keeps).
    if (settle !== null) settle(end)
    events.onEnded(end)
  }

  const consume = (chunk: string): void => {
    buffer += chunk
    for (;;) {
      const index = buffer.indexOf('\n')
      if (index < 0) {
        // A stream with no newline would otherwise grow this buffer forever.
        // The line is dropped once it passes the cap, said once as a notice,
        // and the rest of it is dropped as it arrives.
        if (buffer.length > maxLine) {
          if (!discarding) {
            events.onNotice({ text: `a feed line longer than ${maxLine} characters was dropped`, receivedAt: stamp() })
          }
          discarding = true
          buffer = ''
        }
        break
      }
      const line = buffer.slice(0, index)
      buffer = buffer.slice(index + 1)
      if (discarding) {
        discarding = false
        continue
      }
      if (line.length > maxLine) {
        events.onNotice({ text: `a feed line longer than ${maxLine} characters was dropped`, receivedAt: stamp() })
        continue
      }
      const decoded = decodeFeedLine(line)
      if (decoded.kind === 'document') {
        events.onFrame(tracker.push(decoded.document, stamp()))
      } else if (decoded.kind === 'notice') {
        events.onNotice({ text: decoded.text, receivedAt: stamp() })
      }
    }
  }

  // drain is the end of one child's stdout: a last line the child wrote
  // without a newline is still a line, so it is decoded like any other; a
  // fragment that is not a whole document (a child killed mid-write) is said
  // as a notice -- without its text -- rather than lost in silence.
  const drain = (): void => {
    const rest = buffer
    const wasDiscarding = discarding
    buffer = ''
    discarding = false
    if (wasDiscarding || rest.trim() === '') return
    if (rest.length > maxLine) {
      events.onNotice({ text: `a feed line longer than ${maxLine} characters was dropped`, receivedAt: stamp() })
      return
    }
    const decoded = decodeFeedLine(rest)
    if (decoded.kind === 'document') {
      events.onFrame(tracker.push(decoded.document, stamp()))
      return
    }
    if (decoded.kind === 'notice' && !rest.trimStart().startsWith('{')) {
      // A sentence, as `consume` would have said it had the newline come.
      events.onNotice({ text: decoded.text, receivedAt: stamp() })
      return
    }
    events.onNotice({
      text: `the feed's last line ended without a newline and was not a whole document; it was dropped (${String(rest.length)} characters)`,
      receivedAt: stamp(),
    })
  }

  const spawnOnce = (): void => {
    const binary = plan.command
    if (binary === '') {
      finish({ reason: 'unstartable', code: null, signal: null, sentence: 'the spawn plan has no binary', spawns })
      return
    }
    spawns += 1
    let started: SpawnedChildLike
    try {
      started = spawner(binary, wrapperArgs(plan), {
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
    started.on('spawn', () => {
      deps.log?.record({ argv: spawnedArgv(plan), cwd: plan.cwd, project: plan.project })
    })
    child = started
    stderr = ''
    buffer = ''
    discarding = false
    readText(started.stdout, consume)
    readText(started.stderr, (text) => {
      stderr = boundAppend(stderr, text, MAX_STDERR_BYTES)
    })
    started.on('error', (error) => {
      finish({ reason: 'unstartable', code: null, signal: null, sentence: error.message, spawns })
    })
    // `close`, not `exit`, for the same reason as the one-shot reader above:
    // the last frame of a `watch` can arrive after the child is reaped, and a
    // supervisor that decided `lost` on `exit` would both drop that frame and
    // read an empty stderr into the sentence it gives the screen. `close` is
    // the point at which there is nothing left to read from this child, so it
    // is also where `drain` hands on a last line that had no newline.
    //
    // `exit` still arms a backstop, the counterpart of `runToCompletion`'s
    // deadline: a grandchild (or a shim) that inherited the pipe can hold
    // stdout open after the child is reaped, and then `close` never comes --
    // no restart, no `lost`, and a `stop()` that never settles, which hangs
    // `CliSource.close()` and app quit with it. After `exit` the streams get
    // the shutdown grace to drain; then they are destroyed and the child is
    // decided on its exit status as if `close` had come.
    let decided = false
    let drainTimer: ReturnType<typeof setTimeout> | null = null
    const decide = (code: number | null, signal: string | null): void => {
      if (decided) return
      decided = true
      if (drainTimer !== null) {
        timers.clearTimeout(drainTimer)
        drainTimer = null
      }
      drain()
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
    }
    started.on('exit', (code, signal) => {
      if (decided || drainTimer !== null) return
      drainTimer = timers.setTimeout(() => {
        drainTimer = null
        if (decided) return
        started.stdout?.destroy?.()
        started.stderr?.destroy?.()
        decide(code, signal)
      }, grace)
    })
    started.on('close', decide)
  }

  // `stop()` is idempotent, and *the promise is what is cached*, not just the
  // effect: `LiveFeed.stop()` and `CliSource.close()` both reach it, and a
  // caller that stops a feed and then closes the source without awaiting the
  // first call must not be left with a promise nothing will ever settle.
  // Caching the promise also means the second call does not send a second
  // `SIGTERM` or arm a second `SIGKILL` for a child already on its way out.
  let stopping: Promise<SidecarEnd> | null = null

  const stop = (): Promise<SidecarEnd> => {
    if (stopping !== null) return stopping
    stopping = new Promise<SidecarEnd>((resolve) => {
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
    return stopping
  }

  spawnOnce()
  return {
    get spawns(): number {
      return spawns
    },
    stop,
  }
}
