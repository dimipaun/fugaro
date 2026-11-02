// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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
import { randomBytes } from 'node:crypto'
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
import { WSL_UTF8_VAR, WSL_UTF8_VALUE, wslDistributions, wslExecutable } from './wsl'

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
 * the console reaches it by running `wsl.exe -d <distro> --cd <dir> -e sh -c
 * '… exec fugaro …'` rather than by executing a binary the host can see. There is no
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
 * **It is the whole list on both routes, and the WSL route adds `WSLENV_KEY` to
 * the environment `wsl.exe` is handed always and `WSL_UTF8_VAR` only when the
 * spawn asked for UTF-8 (`wslUtf8`, see `childEnvironment`).** Neither is a
 * variable the distribution receives:
 * `WSLENV_KEY` is `wsl.exe`'s own share list, and the console states it empty
 * rather than leaving it out, so "no Windows variable is shared with the
 * distribution" is a sentence this console wrote rather than a default it
 * inherited (F20, D5(a)); `WSL_UTF8_VAR` is the console's own switch asking
 * `wsl.exe` for UTF-8 rather than the machine's console encoding, and the empty
 * share list is what keeps it out of the distribution.
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
  // `budget show`'s own opt-in, and the Spend screen's caps read is its only
  // caller: it costs a listing of the runs bucket, so no other row carries it.
  '--compute',
  '--by',
  '--since',
  '--until',
  '--mine',
  '--pr',
  '--project-wide',
  '--from-store',
  // V2.5.1a's two, and the launch's argv is the only place either can appear:
  // `--task-file -` hands the task over on stdin, and `--run-id` launches under
  // the id the main process generated rather than one the CLI would invent.
  '--task-file',
  '--run-id',
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

/** STDERR_HEAD_LIMIT bounds the **prefix** of an stderr stream the console keeps
 * for the wrapper's own line (T-F1, T-F4). It is a few times `MAX_STDERR_BYTES`
 * because the line is not necessarily the first thing on the pipe: `wsl.exe`
 * prints its own relay warnings before the distribution's command runs, and the
 * head has to hold those and then the line. Past that, a child that prints
 * without end keeps the head as it is -- the wrapper's line, if a child ever
 * started, arrived before anything the child itself printed. */
export const STDERR_HEAD_LIMIT = 16 * 1024

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
 *
 * **`title`, `planTask` and `stdin` are the launch's three** (design §5.1 rule
 * 11). The first two are the only kinds emitted as `--flag=value` in one word:
 * a value inside the flag's own word cannot be read as a flag, so a title that
 * begins with `-` is a title. They cannot ride `value`, whose alphabet
 * (`ARGUMENT_RE`) refuses the space a title is allowed to have and whose
 * leading-`-` rule would refuse `-draft` -- the design's point is that widening
 * that alphabet for a title would widen it for `--repo` and every other value
 * too. `stdin` is an argument that reaches **no argv word at all**: the task
 * text goes to the child on its standard input (below).
 */
export type SpawnArgumentKind = 'value' | 'boolean' | 'secretScope' | 'pullRequest' | 'title' | 'planTask' | 'stdin'

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
  // `gitProvider` is the one kind whose operation spawns nothing
  // (`link.openPullRequest`, K2a): the mapping is total over Task 1.2's kinds,
  // and `value` is what a bare word would reach an argv as if one ever did.
  gitProvider: 'value',
  installationName: 'value',
  installationCheckout: 'value',
  launchTask: 'stdin',
  launchTitle: 'title',
  launchPlanTask: 'planTask',
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
 * INSTALLATION_LIST_COMMAND is the command the rail's one spawn drives, spelled
 * as the two words its `words` start with. It is here rather than inline so the
 * check below and the row cannot disagree about which read lists installations.
 */
export const INSTALLATION_LIST_COMMAND = 'config list'

/** SpawnOperationName is every operation this module will build an argv for. */
export type SpawnOperationName = OperationName

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
export type WslQueryName = 'wsl.distributions' | 'wsl.path'

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
  /**
   * `runIdFlag` is the flag the row's **run id** is emitted under, and it is
   * present on exactly one row: `run.launch`, whose `--run-id` is the id the
   * main process generated for the confirmed window rather than an argument a
   * renderer sent. It is a column of the row rather than an argument because
   * no field of the launch's schema names a run id (design §5.1 rule 11), and
   * it is emitted from `argvFor`'s own `runId`, never from `args`.
   */
  readonly runIdFlag?: string
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
      { key: 'compute', flag: '--compute', kind: 'boolean' },
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
    // The Spend screen's by-person view (console L5b). One declared argument,
    // `--since`, in `runs stats`'s own grammar: the row cannot emit
    // `--all-users` (so no spawn can widen the view the CLI resolved to the
    // caller), and `--by`, `--max-runs`, `--repo` and `--all` are no part of the
    // operation's schema, so `argvFor` refuses them by name before this row is
    // reached with them.
    operation: 'runs.stats',
    words: ['runs', 'stats', '--json'],
    takesProject: true,
    args: [{ key: 'since', flag: '--since', kind: 'value' }],
    dirFlag: false,
  },
  {
    // The Plan screen's read (plan GV2.1), and the one row whose subject is the
    // **checkout**: `plan status` walks the artifacts under the child's own
    // working directory, which is the installation's recorded checkout when it
    // has one. The directory is therefore the spawn's cwd and not a word here --
    // `plan.status` has no `--dir` and no positional, and `dirFlag` is false for
    // every command but `doctor`. An installation with no checkout is refused
    // before this row is reached (`createBoundSource`), because the private
    // working directory holds no `docs/` and a read there would be a document
    // about nothing.
    //
    // It is §5.1 rule 6's `plan status --json` row (the console-v2 plan's GV2.1;
    // its document is `console/docs/json-contract.md` §6.12). The words here are
    // the command's own (`newPlanStatusCmd`), the way every other row's are.
    //
    // `--all` is the screen's own control and the row's one declared argument;
    // `--json` is fixed. `--gcp-project` and `--region` are flags of the CLI's
    // cloud group that this console never emits: neither is on `SPAWN_FLAGS`, so
    // `assertArgvFlags` would refuse an argv carrying one.
    operation: 'plan.status',
    words: ['plan', 'status', '--json'],
    takesProject: true,
    args: [{ key: 'all', flag: '--all', kind: 'boolean' }],
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
  {
    // The installation rail's read (owner's design ruling of 2026-10-25):
    // `fugaro config list` is cobra's `NoArgs` with one flag of its own
    // (`--json`); it takes no `--project`, no `--config` and no `--dir`, and it
    // opens no bucket, so the row carries the three false columns the check
    // below holds it to. It is the one row that lists installations rather than
    // reading a project, and it was the picker window's own read before the
    // rail took it over.
    operation: 'installation.list',
    words: ['config', 'list', '--json'],
    takesProject: false,
    args: [],
    dirFlag: false,
  },
  {
    // The project view (Bitbucket credentials T1.2, D364): every repository of
    // the installation, from the view the coordinator's sweep last stored. The
    // two flags are fixed words rather than declared arguments -- the screen has
    // no control that turns either off -- so `args` is empty and `argvFor` emits
    // exactly the three words below plus `--project`/`--config` (rule 3; the
    // command carries the cloud group's two, and it is a read of a project, so
    // it is not on `COMMANDS_WITHOUT_PROJECT`).
    //
    // `--repo` and `--since` are deliberately absent: the read is about the
    // project's whole repository set, and the stored view carries no time for a
    // window to filter (`src/main/ipc.ts`'s comment on the operation says both
    // at length). `--from-store` is on `SPAWN_FLAGS` and on no never-row: it is
    // the read that touches no credential, and it is the whole point of this
    // row that the console may make it.
    operation: 'coordinator.status',
    words: ['coordinator', 'status', '--from-store', '--all', '--json'],
    takesProject: true,
    args: [],
    dirFlag: false,
  },
  {
    // **The launch, and the console's only write** (design §5.1 rule 11, D22).
    // The row is rule 11's argv word for word: `run --task-file - --json
    // --run-id <id> --repo <owner/name> [--title=<T>] [--plan-task=<P>]
    // --project <name> [--config <path>]`. `--task-file -` is the literal word
    // `-`, and it means the task arrives on the child's stdin -- which is why
    // the `task` argument's kind is `stdin` and why it emits no word at all.
    // `--run-id` is the main process's own (`runIdFlag` above); `--title=` and
    // `--plan-task=` are one word each and appear only when filled in, and the
    // order of the columns here is the order the row is compared as a string in.
    //
    // The exclusion list is closed by construction rather than by a check: the
    // five named fields are the whole schema, the four declared arguments are
    // the whole of what this row can emit, and `--pr`, `--retry`, `--attempts`,
    // `--recipe`, `--attach`, `--no-budget-check`, `--token`, `--mode`,
    // `--path`, `--ref`, `--workflow`, `--total-timeout`, `--no-compression`,
    // `--gcp-project`, `--region` and `--batch` are each on no `SPAWN_FLAGS`
    // entry, so `assertArgvFlags` refuses an argv carrying one, and each is on
    // its own `NEVER_REACHABLE` row for the `run` command. No positional TEXT:
    // `run` refuses one together with `--task-file`, so this row has none.
    operation: 'run.launch',
    words: ['run', '--task-file', '-', '--json'],
    takesProject: true,
    runIdFlag: '--run-id',
    args: [
      { key: 'task', flag: null, kind: 'stdin' },
      { key: 'repository', flag: '--repo', kind: 'value' },
      { key: 'title', flag: '--title=', kind: 'title' },
      { key: 'planTask', flag: '--plan-task=', kind: 'planTask' },
    ],
    dirFlag: false,
  },
] as const satisfies readonly SpawnCommand[])

/**
 * ARGUMENT_RE is the alphabet a *renderer* argument may use. It is deliberately
 * a whitelist rather than a blacklist: a repository is `owner/name`, a run id is
 * `YYYYMMDD-HHMMSS-xxxx`, a day is `YYYY-MM-DD`, `--since` is `Nd`/`3h`/`0` or
 * `report`'s `12w`, `--by` is one of `report`'s eight words -- the schema is
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

/**
 * DISTRO_SHELL_METACHARACTERS_RE matches a character the **distribution's
 * shell** would read as syntax rather than as part of a word.
 *
 * **Why this rule exists on the WSL route alone.** The host route spawns the CLI
 * with `shell: false` and an array argv, so a word is a word and there is no
 * parser between the console and the program -- `isSafeInstallationValue` is the
 * whole of that rule. On the WSL route the CLI is started *inside* a
 * distribution, and the CLI's words are handed to a shell there (`wsl.exe -e sh
 * -c …`, which is also what makes the Linux pid capturable, T-F4).
 * A word carrying `$(…)`, a backtick, a `;` or a newline is therefore not a
 * value there: it is a program the operator never asked the console to run.
 *
 * The console refuses such a word where the plan is built rather than escaping
 * it, because the escaping belongs to the shell's own layer: `shellQuoteWord`
 * below is that layer's function, and Part F's wrapper (`wslWrapperFor`) applies
 * it to every word as a second layer. A word that only *could* be quoted safely
 * is still refused outright --
 * `$(touch x)` as a project name must never reach a shell as syntax (Part D's
 * carry-over, F7's "one word, one value"). */
// eslint-disable-next-line no-control-regex
const DISTRO_SHELL_METACHARACTERS_RE = /[\s$`;()|&<>!*?[\]{}~#'"\\\u0000-\u001f\u007f]/

/** distroShellUnsafe reports whether `word` carries a character the
 * distribution's shell would read as syntax. See the regex above. */
export function distroShellUnsafe(word: string): boolean {
  return DISTRO_SHELL_METACHARACTERS_RE.test(word)
}

/** MAX_DISTRO_NAME_LENGTH bounds a WSL distribution's name. It is the same bound
 * a project name gets: both are short single-token names. */
export const MAX_DISTRO_NAME_LENGTH = 200

/** DISTRO_NAME_PATTERN is the alphabet a WSL distribution's name is drawn from:
 * letters, digits, `.`, `_` and `-`, with a first character that is not `-` (a
 * name that began with one would read as `wsl.exe`'s own flag).
 *
 * **It is the narrowest alphabet in this file, and it is on purpose.** The name
 * arrives from a document -- `wsl.exe --list --quiet`'s lines are data -- and it
 * becomes the value of `-d`, the distribution the console then runs a shell
 * inside (T-D3, T-E3). `installations.ts` reads the same rule for the field it
 * stores, so the store and the plan builder cannot disagree about what a
 * distribution is called. */
export const DISTRO_NAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]*$/

/** isDistroName is the distribution-name rule: a bounded, non-empty name in the
 * alphabet above. */
export function isDistroName(value: string): boolean {
  return value.length <= MAX_DISTRO_NAME_LENGTH && DISTRO_NAME_PATTERN.test(value)
}

/**
 * shellQuoteWord renders one word so that the distribution's shell hands it to
 * the CLI **unchanged**: a POSIX single-quoted string, where the only character
 * that needs anything at all is the single quote itself (`'\''`, the standard
 * four-character splice). Nothing is interpolated inside single quotes -- no
 * `$`, no backtick, no `*` -- so `$(touch x)` in a project name is the fourteen
 * characters of that name and not a command substitution.
 *
 * A NUL cannot be quoted and cannot be in an argv at all: it is refused rather
 * than silently dropped. This function is the shell layer's half of the rule
 * above: Part F's wrapper (`wslWrapperFor`) quotes every word with it, after
 * `planFor` has already refused any word `distroShellUnsafe` flags.
 */
export function shellQuoteWord(word: string): string {
  if (word.includes('\u0000')) refused('a spawn word carries a NUL byte, which no shell can quote')
  return `'${word.split("'").join("'\\''")}'`
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
 *      1.2's schema is what makes exactly one of the two present), and the
 *      launch's title and plan task as one `--flag=value` word each;
 *   3. `--project <name>` and, when the installation declares one,
 *      `--config <path>` -- and then, for `doctor` alone, `--dir <directory>`.
 *
 * The launch's own `--run-id` is emitted between the row's words and its
 * declared arguments, from `context.runId` rather than from `args`: no field of
 * the launch's schema names a run id (design §5.1 rule 11), so a caller cannot
 * propose one, and the order is the order rule 11 writes.
 *
 * An argument key the row does not declare is refused by name rather than
 * ignored: a payload that reached here from a document could otherwise carry a
 * flag of its own. Every emitted flag is then asserted against `SPAWN_FLAGS`, so
 * no row, argument or later edit can emit a flag the console must never reach.
 */
export function argvFor(
  operation: SpawnOperationName,
  args: ValidatedArguments,
  context: {
    readonly target: SpawnTarget
    readonly directory: string | undefined
    /** `runId` is the id the main process generated for this launch. It is
     * required by the one row that carries a `runIdFlag` and refused by every
     * other row, so an id cannot be smuggled into a read's argv. */
    readonly runId?: string | undefined
  },
): readonly string[] {
  const row = spawnCommandFor(operation)
  const declared = new Set(row.args.map((spec) => spec.key))
  for (const key of Object.keys(args)) {
    if (!declared.has(key)) {
      refused(`${operation} received ${key}, which it does not declare: only this table's arguments reach argv`)
    }
  }

  const argv: string[] = [...row.words]
  if (row.runIdFlag !== undefined) {
    const runId = context.runId
    if (runId === undefined || !RUN_ID_ARGUMENT_RE.test(runId)) {
      refused(`${operation} needs the run id the main process generated, in \`task.NewRunID\`'s shape`)
    }
    argv.push(row.runIdFlag, runId)
  } else if (context.runId !== undefined) {
    refused(`${operation} takes no run id: only the launch's own row carries one`)
  }
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
      case 'stdin': {
        // The task text is the one argument that reaches no argv word: it is
        // written to the child's standard input by the launch (`launch.ts`), so
        // it is in neither the process table nor the spawn log. Nothing is
        // emitted here, and the caller's own value is checked where it is read.
        if (typeof value !== 'string') refused(`${operation}'s ${spec.key} is not a string`)
        break
      }
      case 'title':
      case 'planTask': {
        // One word, `--flag=value`, and never two: a value inside the flag's
        // own word cannot be read as a flag, which is what makes `--title=-x`
        // a title. The value itself is the validator's (`ipc.ts` runs
        // `launchrules.ts`'s checker for this kind), so the only thing left to
        // refuse here is the one word an argv could not carry at all.
        if (typeof value !== 'string') refused(`${operation}'s ${spec.key} is not a string`)
        const word = `${spec.flag ?? ''}${value}`
        if (value === '' || word.includes('\u0000')) {
          refused(`${operation}'s ${spec.key} is not a value this console will pass to the CLI`)
        }
        argv.push(word)
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

/** RUN_ID_ARGUMENT_RE is `task.NewRunID`'s shape as this module requires it of
 * the id the main process generated: a UTC stamp and four hex digits. It is
 * narrower than `ipc.ts`'s `RUN_ID_RE` -- which has to accept any run id a CLI
 * ever printed -- because this one is about the id the *console* makes. */
const RUN_ID_ARGUMENT_RE = /^\d{8}-\d{6}-[0-9a-f]{4}$/

/** RUN_LAUNCH_OPERATION is the one row whose argv may carry a word that is not
 * on `SPAWN_FLAGS` verbatim: the launch's two `=`-form flags (below). */
const RUN_LAUNCH_OPERATION = 'run.launch'

/** RUN_LAUNCH_EQUALS_FLAGS are the two prefixes `assertArgvFlags` admits for
 * that row and no other. They are `spawn.ts`'s own spelling of the two words
 * rule 11 fixes, and the design names this exact list: "the `--title=` and
 * `--plan-task=` prefixes, for the `run.launch` row alone". */
export const RUN_LAUNCH_EQUALS_FLAGS: readonly string[] = Object.freeze(['--title=', '--plan-task='])

/** assertArgvFlags refuses an argv carrying a flag outside `SPAWN_FLAGS`, and one
 * carrying a flag the design's never-table names. Both checks are here rather
 * than in the callers because this function is the only producer of an argv.
 *
 * **The launch's two `=`-form flags are the one exception, and it is spelled
 * out rather than implied.** `--title=<T>` and `--plan-task=<P>` are one word
 * each -- the design fixes that form because a value inside the flag's own word
 * cannot be read as a flag -- so they can never match a `SPAWN_FLAGS` entry
 * that is a flag *name*. Accepting them here is what makes that form
 * expressible at all, and the exception is bounded in three ways: it is the
 * `run.launch` row alone, it is those two prefixes alone, and the value after
 * the `=` has to be non-empty. A bare `--title` (no `=`) is not on this list,
 * and neither prefix is admitted for any other row -- so a read cannot grow an
 * `=`-form flag by copying it. The value's own rules (its cap, one line, no
 * control character, no format character) are the validator's: `ipc.ts` runs
 * `launchrules.ts`'s checker for the `title` and `planTask` kinds before any of
 * this is reached. */
export function assertArgvFlags(operation: string, argv: readonly string[]): void {
  const command = commandPathIn(argv)
  for (const element of argv) {
    if (!element.startsWith('--')) continue
    if (operation === RUN_LAUNCH_OPERATION) {
      const equals = RUN_LAUNCH_EQUALS_FLAGS.find((prefix) => element.startsWith(prefix))
      if (equals !== undefined && element.length > equals.length) continue
    }
    if (!SPAWN_FLAGS.includes(element)) {
      refused(`${operation} would emit ${element}, which is not a flag this console may spawn`)
    }
    const forbidden = isNeverReachable({ command, flags: argv })
    if (forbidden !== null) {
      refused(`${operation} would emit ${forbidden.value}: ${forbidden.why} (${forbidden.where})`)
    }
  }
}

/** commandPathIn is an argv's own command path: the leading words up to the
 * first flag. An argv-shaped never row names a command, not a flag, so the check
 * below has to be run against the argv's command rather than against `null` --
 * otherwise a row like `run --pr`, which is argv-shaped precisely because a bare
 * `--pr` is a legitimate flag of other commands, could never match and the
 * exclusion list would go unenforced. */
function commandPathIn(argv: readonly string[]): string {
  const words: string[] = []
  for (const word of argv) {
    if (word.startsWith('-')) break
    words.push(word)
  }
  return words.join(' ')
}

/**
 * WSL_EXEC_FLAG is the word that ends `wsl.exe`'s own words and starts the
 * distribution's program: `-e` (`--exec`), which runs the program **directly**,
 * with no Linux shell between `wsl.exe` and it. `--` is not used: it hands the
 * rest of the line to the distribution's *login* shell, which would re-read the
 * `sh -c` script and every word around it under whatever shell the operator's
 * account has (T-F4). With `-e` the console's own `sh -c` is the one shell, and
 * its quoting (`shellQuoteWord`) is the one quoting that applies.
 */
export const WSL_EXEC_FLAG = '-e'

/**
 * WSL_PREFIX_FLAGS is every word of the wrapper that is a flag or a separator:
 * the distribution to run in, the directory to start in, and the `-e` after
 * which the distribution's program begins. It is `wsl.exe`'s set and **not**
 * `SPAWN_FLAGS`'s, which is the whole reason `SpawnPlan` keeps `prefix` apart
 * from `argv` (T-C1, F8): neither `--cd` nor `-e` is a flag this console may
 * emit to the CLI.
 *
 * The two sets must stay disjoint, and `test/unit/spawn.test.ts` holds them
 * apart: a word in both would be a wrapper word that also looks like a CLI flag,
 * which is the one shape that makes the two halves of the command line
 * indistinguishable.
 */
export const WSL_PREFIX_FLAGS: readonly string[] = Object.freeze(['-d', '--cd', WSL_EXEC_FLAG])

/**
 * isSafeDistroName answers whether a distribution's name may become a wrapper
 * word (T-E3, F22). Part C widened the argv this console builds with words that
 * do not come from the CLI's own table: a distribution's name arrives from
 * `wsl.exe --list --quiet` (a document's string) or from the store, and it lands
 * on `wsl.exe`'s command line. This is the check it crosses.
 *
 * **What it refuses, and why each one is a hazard rather than a taste:**
 *
 *   - a **leading `-`**. This is the whole of the argument confusion here:
 *     `-d --help`, `-d -d` and `-d --` hand `wsl.exe` a word it may read as one
 *     of its own instead of as the distribution it names. `-` is admitted
 *     everywhere else (`Ubuntu-22.04` is a real distribution), so this rule and
 *     not the alphabet is what keeps a flag out of the value position.
 *   - a **control character or a NUL**, which is not a name any `wsl.exe` printed
 *     and not a name any screen can draw.
 *   - the **empty name**, because `-d ''` names nothing.
 *   - an **absurd length** -- the bound the store's own values carry.
 *
 * **What it admits, and why that is not the route's rule.** This is the
 * *argv-level* guard -- argument confusion on `wsl.exe`'s own command line --
 * and whitespace, quotes, `;` and `~` are not argument confusion with an array
 * argv. It is not the rule a distribution must meet to be *driven*: Part D's
 * `isDistroName` (`DISTRO_NAME_PATTERN`, T-D3) is, because the WSL route runs
 * the CLI through the distribution's own shell. `planFor` and the resolver hold
 * the name to that narrower rule first and refuse with a sentence, so a
 * distribution called `My Linux` is refused on the WSL route (fail closed, a
 * known limit in `docs/console-windows/implementation-plan.md`); this guard is
 * the last line under it, and both are asserted in `test/unit/spawn.test.ts`.
 */
export function isSafeDistroName(value: string): boolean {
  if (value === '' || value.length > 256) return false
  if (value.startsWith('-')) return false
  // eslint-disable-next-line no-control-regex
  return !/[\u0000-\u001f\u007f]/.test(value)
}

/**
 * assertWslPrefix holds the wrapper to its shape and its two values to their
 * rule, and it is the prefix's half of what `assertArgvFlags` is to the CLI's
 * argv (T-E3, F22). It is called by `wslWrapper`, the only producer of a prefix,
 * for the same reason `assertArgvFlags` is called by `argvFor`: a check in the
 * callers is a check a later caller can forget.
 *
 * The shape is `-d <distribution>` and then either `-e` or `--cd <directory> -e`
 * -- exactly what `wslWrapper` builds, and nothing else. Its word that is a flag
 * is checked by absence from `SPAWN_FLAGS`, and its two **values** are checked
 * as follows:
 *
 *   - **the distribution's name**, the one word here that reaches `wsl.exe` as a
 *     selector, is held to `isSafeDistroName`: not a flag, not a control
 *     character, not empty, not absurd. That is the argv's rule only: the
 *     route's rule is Part D's narrower `isDistroName`, which `planFor` applies
 *     before this guard runs (see that function's own note).
 *   - **the directory** is a Linux path, and it is held to
 *     `isSafeInstallationValue` -- the rule the project name, the config path
 *     and the checkout already satisfy. `ARGUMENT_RE`, which the renderer's own
 *     values satisfy, would be the wrong rule here: it admits neither the space
 *     of `/home/Ann Lee/src` nor the `~` a shell would expand, and it is a
 *     renderer's alphabet rather than a path's.
 *
 * So the two values are held to the same rule -- no control character or NUL, not
 * empty, not absurd, no leading `-` -- and the name differs only in its length
 * bound (256, the store's own). The narrower alphabet a distribution must meet
 * to be driven at all (`DISTRO_NAME_PATTERN`, Part D) is enforced upstream, in
 * `planFor` and the resolver; this guard is the argv's last line under it.
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
        `it must be a non-empty name that does not begin with "-" and carries no control character`,
    )
  }
  const rest = words.slice(2)
  if (rest.length === 1 && rest[0] === WSL_EXEC_FLAG) return
  if (rest.length === 3 && rest[0] === '--cd' && rest[2] === WSL_EXEC_FLAG) {
    // The directory: the same rule as the name, without the name's length bound.
    const directory = rest[1] ?? ''
    if (!isSafeInstallationValue(directory)) {
      refused(`the wrapper would start the CLI in ${JSON.stringify(directory)}, which is not a directory this console will pass`)
    }
    return
  }
  refused(
    `the wrapper's words are \`-d <distribution>\`, an optional \`--cd <directory>\`, and \`-e\`: ` +
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
 *
 * **There is one table and one caller.** Until the owner's design ruling of
 * 2026-10-25 there were two, because `installation.list` was the picker
 * window's own read and was deliberately not a `CONSOLE_OPERATIONS` entry; the
 * rail made it the dashboard's, so the second table is gone and every spawn the
 * console can make is a row here held to the same one-for-one rule.
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
    const wantsProject = !COMMANDS_WITHOUT_PROJECT.includes(operation.command)
    if (row.takesProject !== wantsProject) {
      refused(
        `${row.operation} ${wantsProject ? 'must carry' : 'must not carry'} ${PROJECT_FLAG}: every command but ${COMMANDS_WITHOUT_PROJECT.join(' and ')} takes it (rule 3)`,
      )
    }
    const wantsDir = COMMANDS_TAKING_DIR.includes(operation.command)
    if (row.dirFlag !== wantsDir) {
      refused(`${row.operation} ${wantsDir ? 'must carry' : 'must not carry'} ${DIR_FLAG}: only doctor takes it (rule 5)`)
    }
    // The run id column, held the same way as the two above: the one row that
    // carries it is the launch, and its flag is one this module may spawn.
    if (row.runIdFlag !== undefined) {
      if (row.operation !== RUN_LAUNCH_OPERATION) {
        refused(`${row.operation} carries a run id flag: only the launch's own row has one`)
      }
      if (!SPAWN_FLAGS.includes(row.runIdFlag)) {
        refused(`${row.operation} would emit ${row.runIdFlag}, which is not a flag this console may spawn`)
      }
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
 * **`wslUtf8` adds the other** (T-F2): `WSL_UTF8=1`, which asks `wsl.exe` for
 * UTF-8. It is set on every call that starts `wsl.exe` -- the queries
 * (`src/main/wsl.ts`) and the CLI's own plan alike (review of `#981`, which
 * found the CLI plan the one call whose stderr carries both `wsl.exe`'s own
 * warnings and the wrapper's line) -- and it is this console's constant rather
 * than anything read from the app's environment, so the allow-list above still
 * decides everything that came from outside.
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
  /** `wslUtf8` states `WSL_UTF8=1`, asking `wsl.exe` to answer in UTF-8 rather
   * than in the machine's console encoding (T-F2; `WSL_UTF8_VAR`). It is true
   * for **every** call that starts `wsl.exe` -- the machine's queries and the
   * CLI's own plan alike (review of `#981`) -- and false for a child that has
   * no `wsl.exe` in front of it. It is not a boundary key: it is this console's
   * own constant, and `wsl`'s empty share list is what keeps it out of the
   * distribution. */
  readonly wslUtf8?: boolean | undefined
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
  if (options.wslUtf8 === true) env[WSL_UTF8_VAR] = WSL_UTF8_VALUE
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
 *
 * **On Windows it makes no mode claim at all** (F12, F45, T-D4). NTFS reports
 * two file modes and neither is a POSIX one: `chmodSync` is a no-op there,
 * `process.getuid` is undefined, and a directory's `0700` is therefore not a
 * fact this console can state -- repeating the POSIX claim on Windows would be
 * a promise the filesystem does not keep. What the Windows branch does is what
 * it can: create the directory under `tmpdir()`, refuse a link or a
 * non-directory planted at that name, and return it. The protection that does
 * hold there is the user profile's own ACL, and it is the operator's, not ours.
 *
 * **On the WSL route this directory is a host directory** and its *Linux*
 * spelling is what goes into the wrapper's `--cd` (T-D2, D4(a)): the directory
 * belongs to the host -- it is where `wsl.exe` itself is started from -- while
 * the CLI inside the distribution is started in the translation of it. */
export function privateWorkingDirectory(options: {
  readonly base?: string
  readonly uid?: number | null
  /** `platform` is the platform whose filesystem this asks, so a test on one
   * machine can drive the other branch (T-D4). It is the host's platform: on the
   * WSL route the directory is still created on the host. */
  readonly platform?: string | undefined
} = {}): string {
  const platform = options.platform ?? process.platform
  // Windows has no uid, so the branch below makes the same answer the
  // `uid === null` path already makes, and it never asks the process for one:
  // `process.getuid` does not exist there.
  const ownUid = platform === 'win32' || typeof process.getuid !== 'function' ? null : process.getuid()
  const uid = platform === 'win32' ? null : options.uid === undefined ? ownUid : options.uid
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
  // **The mode question is POSIX's and it is not asked on Windows** (F12, F45).
  // `chmodSync` there is a no-op and NTFS reports two modes rather than a POSIX
  // one, so a `0700` claim would be a sentence about a state the filesystem does
  // not have -- and a refusal built on it would refuse a perfectly good
  // directory. A directory someone else can write to is then the profile ACL's
  // business, which is the operator's.
  if (platform !== 'win32') {
    if (uid !== null && stat.uid !== uid) {
      refused(`${path} is owned by uid ${stat.uid}, not ${uid}: the console will not spawn from it`)
    }
    // A directory of our own with a wider mode (an older build, a umask) is
    // taken back to owner-only; one that still is not 0700 afterwards is refused.
    if ((stat.mode & 0o777) !== PRIVATE_DIR_MODE) chmodSync(path, PRIVATE_DIR_MODE)
    if ((lstatSync(path).mode & 0o777) !== PRIVATE_DIR_MODE) {
      refused(`${path} is not mode 0700: the console will not spawn from it`)
    }
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
  /** `stdio` is the three descriptors the child gets. stdin is `'ignore'` for
   * every spawn but one: the console never writes to a child, so a child that
   * reads its stdin sees it closed rather than hanging on a terminal nobody is
   * at (Part E). **`run.launch` is the one row that asks for `'pipe'` on
   * stdin** -- the task text is handed over there rather than in an argv word
   * (design §5.1 rule 11)-- so the type admits `'pipe'` in that one slot and
   * `runToCompletion`'s `stdinBytes` is the one caller that uses it.
   * stdout and stderr are `'pipe'` for every read the console makes; the one
   * child whose output is nobody's is the stop sent **inside** a distribution
   * (T-F4), which is fired and never read -- and a pipe nobody drains is a
   * child that blocks on write. `nodeSpawner` passes this through as it stands,
   * so the `'ignore'`s are what the stop's child really gets. */
  readonly stdio: readonly ['ignore' | 'pipe', 'ignore' | 'pipe', 'ignore' | 'pipe']
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
 * arrives as bytes regardless, carry the partial character to the next chunk.
 *
 * **`encoding` is `'latin1'` for one caller and one reason** (T-F2): `wsl.exe`'s
 * own answers are UTF-16LE on some Windows builds and UTF-8 on others (F27, U9),
 * and a UTF-8 decode would turn the first into replacement characters -- the
 * BOM's two bytes are not valid UTF-8 at all -- so the bytes cannot be recovered
 * from its output. Read as `latin1`, one code unit is one byte and nothing is
 * lost or replaced; `wsl.ts` decodes the bytes itself. Every other reader wants
 * text, which is `'utf8'`. */
function readText(
  stream: ReadableLike | null,
  onText: (text: string) => void,
  encoding: 'utf8' | 'latin1' = 'utf8',
): void {
  if (stream === null) return
  stream.setEncoding?.(encoding === 'latin1' ? 'latin1' : 'utf8')
  const decoder = new StringDecoder(encoding)
  stream.on('data', (chunk) => {
    const text = typeof chunk === 'string' ? chunk : decoder.write(chunk)
    if (text !== '') onText(text)
  })
}

/** WritableLike is the slice of a child's standard input this module writes to:
 * the launch hands the task over there and ends the stream, and it never waits
 * for the child to ask for it. */
export interface WritableLike {
  write(chunk: Uint8Array): unknown
  end(): unknown
  /** `on` is here for the write's own failure: a child that exits before it
   * drains its stdin closes the read end, and the resulting `EPIPE` arrives as
   * an `error` event -- which, unheard, is an uncaught exception in the main
   * process. See the write in `runToCompletion`. */
  on(event: 'error', listener: (error: Error) => void): unknown
}

/** SpawnedChildLike is the slice of `ChildProcess` this module uses, as an
 * interface so the test can pass a recording fake and assert on `shell` and on
 * the argv it was handed. */
export interface SpawnedChildLike {
  readonly stdout: ReadableLike | null
  readonly stderr: ReadableLike | null
  /** `stdin` is the child's standard input, present exactly when the caller
   * asked for `'pipe'` on that descriptor -- which is the launch's own request
   * and no other read's. It is optional because a fake need not have it and
   * because most children are spawned with `'ignore'` there, where Node hands
   * back `null`. */
  readonly stdin?: WritableLike | null
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
 *
 * `stdio` is the caller's, copied word for word: a read pipes stdout and
 * stderr, and the stop sent **inside** a distribution asks for `'ignore'` on
 * all three so nothing of its own is left in a pipe the console will never
 * drain. Honouring the caller's request is what makes those `'ignore'`s a
 * behaviour rather than a comment.
 */
export const nodeSpawner: ChildSpawner = (command, args, options) =>
  spawn(command, [...args], {
    cwd: options.cwd,
    env: { ...options.env },
    shell: false,
    stdio: [...options.stdio],
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
 * `wsl.exe -d <distro> --cd <dir> -e sh -c '<script>'`, where the script prints
 * the wrapper's line and `exec`s `'fugaro' …` (T-F4), and four different things
 * are in that line: the program (`wsl.exe`), `wsl.exe`'s own flags (`prefix`,
 * `-d … -e`), the shell that carries the CLI (`wrapper`), and the CLI's argv
 * (`argv`, `fugaro …`, which reaches the OS only inside the script). Keeping
 * them in one array would mean the flag assertion could not tell a `wsl.exe`
 * flag from a `fugaro` flag -- neither `--cd` nor `-e` is a flag this console
 * may emit to the CLI -- and `runToCompletion` would read the binary out of a
 * slot that, on this route, holds the word `fugaro`.
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
   * `['-d', <distro>, '--cd', <dir>, '-e']` -- the distribution to run in, the
   * directory to start in (in the **distribution's** spelling: the console
   * translates before it builds the plan), and the `-e` after which the
   * distribution's program (the wrapper's `sh`) begins. It is deliberately not part of `argv`: these words
   * belong to `wsl.exe` and are asserted as `wsl.exe`'s, not as the CLI's. */
  readonly prefix: readonly string[]
  readonly argv: readonly string[]
  /** `wrapper` is the shell that carries the CLI inside a distribution, or
   * `null` on the host route (T-F4, decision D6(a)).
   *
   * On the WSL route the CLI is not the program the OS starts and not the argv
   * the OS receives: a `sh -c` script carries it, which is what makes the pid
   * inside the distribution capturable and cancellable (F24). When it is not
   * `null`, `prefix` still names the distribution, the directory and the `-e`,
   * and `wrapper.words` replaces `argv` as what follows them -- the CLI's own
   * argv is inside the script, shell-quoted, and is *not* appended a second
   * time. See `wrapperArgs`. */
  readonly wrapper: WslWrapper | null
  readonly cwd: string
  readonly env: Readonly<Record<string, string>>
  readonly project: string
  readonly backoff: Backoff
}

/**
 * WslWrapper is the `sh -c` script that runs the CLI inside a distribution, and
 * the one fact the console needs to stop it (T-F4, D6(a)).
 *
 * **Why a shell at all.** `wsl.exe -d <d> -e fugaro …` starts the CLI directly,
 * and killing the Windows-side `wsl.exe` is not documented to kill the Linux
 * process it started (F24, U5). So the CLI is started under a shell that reports
 * its own pid first and `exec`s the CLI **in place** -- after `exec` the pid the
 * console read *is* the CLI's pid, which is the difference between killing the
 * CLI and killing the wrapper that started it.
 *
 * **The pid is reported on stderr, not in a file, and that is deliberate.** The
 * plan's D6(a) sketched a file the shell writes and the console reads "through
 * the same `wsl.exe`", but the file has no spelling that works: the shell's
 * working directory inside the distribution is the installation's checkout
 * (`--cd`), while the console's own working directory is a **host** path, so a
 * file named from one is not a file found at the other; reading it back would
 * cost a second `wsl.exe` on every cancellation, and a file written into the
 * operator's checkout is litter in their repository. The shell's own stderr is
 * the channel the console already reads, it needs no path translated in either
 * direction, and the pid arrives on the same line as the marker T-F1 needs
 * (`WslWrapper.marker`). The stop itself is unchanged: one `kill` inside the
 * distribution, naming the pid the wrapper reported (D6(a), `wslStopArgs`).
 */
export interface WslWrapper {
  /** `words` is what follows `prefix`: `['sh', '-c', <script>]`. */
  readonly words: readonly string[]
  /** `distro` is the distribution to stop in, named again in the stop
   * invocation: the stop is a second `wsl.exe -d <distro> -e …`, and the
   * distribution it names must be the one the CLI is running in. */
  readonly distro: string
  /** `marker` is the sentence **this** spawn accepts as the wrapper's own line:
   * the fixed prefix and this spawn's random nonce (`wslWrapperMarker`). It is
   * the string the script printed and the string every reader matches against,
   * which is why it is a value of the plan rather than a module constant: a
   * reader that matched the prefix alone would accept a line the CLI wrote
   * (see `WSL_WRAPPER_MARKER_PREFIX`). */
  readonly marker: string
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
 * directory to start in (in the distribution's spelling), and the `-e` after
 * which the distribution's program begins (`WSL_EXEC_FLAG`). `--cd` is left out when there is no
 * directory to name -- see `WslRoute`.
 *
 * It is the only producer of a prefix, so `assertWslPrefix` is called here
 * rather than in the callers (T-E3): the distribution's name is a string that
 * came out of `wsl.exe` or out of the store, and it is checked by
 * `isSafeDistroName` on the way to `wsl.exe`'s command line. */
function wslWrapper(wsl: WslRoute): readonly string[] {
  const head = ['-d', wsl.distro]
  const prefix =
    wsl.directory === undefined ? [...head, WSL_EXEC_FLAG] : [...head, '--cd', wsl.directory, WSL_EXEC_FLAG]
  assertWslPrefix(prefix)
  return Object.freeze(prefix)
}

/**
 * isWindowsPathSpelling reports whether a path is written the way **Windows**
 * spells it: a drive-rooted `C:\…`, or anything carrying a backslash (a Linux
 * path never does -- `/` is the only separator there, and a backslash is an
 * ordinary character in a Linux file name).
 *
 * It exists so that the one spelling rule of this route is enforced where the
 * plan is built rather than discovered where it fails: a `C:\…` handed to a
 * Linux child is a path inside the distribution that does not exist, and the
 * distribution's `wsl.exe` would report it as a missing directory -- a sentence
 * that says nothing about which side spelled the path wrong (T-D2, T-D3).
 */
export function isWindowsPathSpelling(path: string): boolean {
  return path.includes('\\') || /^[A-Za-z]:/.test(path)
}

/**
 * wslUncPath is the spelling Windows opens a distribution's path by:
 * `\\wsl.localhost\<distro>\home\dimi\src` for `/home/dimi/src`.
 *
 * **It is not the spelling the console states and not the one it passes** (D3(a),
 * F16). What the console shows and hands the CLI is the **Linux** spelling --
 * the one the operator can paste back into a terminal inside the distribution --
 * and this one exists for exactly one consumer: the field an affordance that
 * hands a path to Windows itself reads (an Explorer reveal, a file dialog). The
 * two are separate functions rather than one function with a flag because the
 * distinction is the rule, and a caller that reached for the wrong one would be
 * reaching for a spelling whose only justification is "Windows opens this".
 */
export function wslUncPath(distro: string, linuxPath: string): string {
  const tail = linuxPath.replace(/^\/+/, '').split('/').join('\\')
  return `\\\\wsl.localhost\\${distro}\\${tail}`
}

/**
 * assertDistroWordsAreSafe refuses the plan whose words the distribution's shell
 * could read as syntax, and the plan whose paths are spelled for the wrong side.
 * It runs on the WSL route alone: the host route's argv never meets a shell and
 * its paths are the host's.
 *
 * Two rules, one reason -- neither word may mean something other than itself
 * once it is inside the distribution:
 *
 *   - **every word that is not a fixed constant** (the CLI's own program word
 *     and the flags this console emits) must be free of shell metacharacters
 *     (`distroShellUnsafe`), because Part F puts the CLI inside `sh -c` and a
 *     word like `$(touch x)` would be a command rather than a project name;
 *   - **the distribution's own spelling** for the wrapper's distro, its `--cd`
 *     and the installation's `--config`: a Windows spelling there is the
 *     wrong-spelling bug the fake `wsl.exe` refuses by name, and it is refused
 *     here -- before any process exists -- so the failure names the rule rather
 *     than arriving as an `ENOENT` from inside a distribution.
 */
function assertDistroWordsAreSafe(wsl: WslRoute, configPath: string | undefined, cli: readonly string[]): void {
  // **The name first, against the narrowest alphabet in this file.** A
  // distribution is the one word in this line that names *where the shell runs*,
  // and it arrives from a document. `planFor` is the last place it is still a
  // string: a name outside the alphabet is refused here rather than handed to
  // `wsl.exe`, which is what "the plan checks the distro itself" means for a
  // function that starts no process.
  if (!isDistroName(wsl.distro)) {
    refused(
      `${JSON.stringify(wsl.distro)} is not a WSL distribution name: a distribution is ` +
        `${String(MAX_DISTRO_NAME_LENGTH)} characters or fewer of letters, digits, dot, dash and underscore, ` +
        'and the console runs a shell in whatever it names (T-D3)',
    )
  }
  if (distroShellUnsafe(wsl.distro)) {
    refused(
      `the distribution’s name (${JSON.stringify(wsl.distro)}) carries a character the distribution’s shell would read as syntax: ` +
        'the console will not hand a shell a word that is not a name',
    )
  }
  // **The spellings before the shell check**, so a Windows-spelled `--config`
  // is refused as the wrong spelling rather than as a word full of backslashes:
  // both are refusals, and only one of them tells the operator what to do.
  const spellings: readonly (readonly [string, string | undefined])[] = [
    ['the directory the CLI starts in', wsl.directory],
    ['the installation’s config path', configPath],
  ]
  for (const [what, path] of spellings) {
    if (path === undefined || path === '') continue
    if (isWindowsPathSpelling(path)) {
      refused(
        `${what} is a Windows spelling (${path}): on the WSL route every path the console passes is the ` +
          'distribution’s own, translated by `wslpath` (D3(a), T-D2)',
      )
    }
  }
  for (const word of cli) {
    if (word === FUGARO_BINARY_NAME || word.startsWith('--')) continue
    if (distroShellUnsafe(word)) {
      refused(
        `this console will not pass ${JSON.stringify(word)} to the CLI inside a distribution: it carries a character ` +
          'the distribution’s shell would read as syntax rather than as part of the word (T-D3)',
      )
    }
  }
}

/** WSL_SHELL_PROGRAM and WSL_SHELL_FLAG are the shell the CLI is carried in
 * (T-F4): `sh -c <script>`. `sh` and not the operator's `$SHELL`, because the
 * console needs a script it wrote to behave the same way in every distribution,
 * and POSIX `sh` is the one shell every distribution has (D6(a)). */
export const WSL_SHELL_PROGRAM = 'sh'
export const WSL_SHELL_FLAG = '-c'

/** WSL_WRAPPER_MARKER_PREFIX is the fixed head of the sentence the wrapper's
 * shell prints on stderr before it `exec`s the CLI (T-F1), with a **per-spawn
 * nonce** after it and the pid it is about to become after that (T-F4): the
 * marker is `${WSL_WRAPPER_MARKER_PREFIX} <nonce>` (see `wslWrapperMarker`), and
 * the line is `<marker> <pid>`.
 *
 * **Why a marker rather than a guess.** The console has to tell "the CLI exited
 * non-zero" from "`wsl.exe` never got as far as starting the CLI": the first is
 * the CLI's status and the second is `wsl.exe`'s, and reading the second as the
 * first is the substitution F26 refuses (U7, U8 leave the exact statuses
 * unknown, so no status can be read as the answer). The shell's own line is the
 * answer: it can only arrive if a child inside the distribution started at all,
 * whatever `wsl.exe` does with statuses. It goes to stderr -- the channel the
 * CLI's own sentence already uses, which the screens read through
 * `cliSentence` -- and the console removes it there, so nothing an operator
 * reads carries it.
 *
 * **Why a nonce, and what it is not.** The line is read off the *same pipe* as
 * everything the CLI and `wsl.exe` print, and the pid on it becomes a
 * `kill -TERM <pid>` run inside the distribution **as the operator's user**
 * (`wslStopArgs`). A marker that is a fixed string is therefore forgeable by the
 * CLI's own stderr: a child that echoes the sentence and a pid makes the console
 * kill a process of its choosing -- the reviewer's probe on `#981` was
 * `<utf16 warning><marker> 4242\n<marker> 1\n`, which named pid 1. So the marker
 * carries `WSL_WRAPPER_NONCE_BYTES` fresh random bytes, drawn once per plan and
 * written into the `sh -c` script as a quoted constant: only the console's own
 * script can print it, and only the **first** matching line is read (a second
 * line that repeats the marker is not consulted, see `wrapperLine`).
 *
 * The nonce is **not a secret**: it is in the wrapper's script, which is in the
 * child's own command line and in the spawn log's `argv` line (D1). It is
 * required to be unguessable **by the CLI's output**, which is what closes the
 * forgery: nothing the CLI prints can carry bytes it never saw. It is held to
 * that and no more.
 *
 * **The pid travels on the same line**, which is the whole of D6(a)'s capture:
 * the line is printed before `exec`, so after `exec` the number *is* the CLI's
 * pid, and the console kills it inside the distribution. It is not the first
 * thing on stderr: `wsl.exe` prints its own relay warnings before the
 * distribution's command runs, and those warnings are UTF-16LE on the builds
 * that ignore `WSL_UTF8` -- see `wrapperLine`, which is where the NUL bytes and
 * the missing line ending they leave are handled. */
export const WSL_WRAPPER_MARKER_PREFIX = 'fugaro-console: the WSL wrapper started'

/** WSL_WRAPPER_NONCE_BYTES is how many random bytes a spawn's marker carries:
 * 32, which is 256 bits of entropy and twice the 128 the fix's brief asks for.
 * It is drawn from `randomBytes` in `wslWrapperFor`, and the cost of a larger
 * nonce is 64 hex characters in one quoted word of a script. */
export const WSL_WRAPPER_NONCE_BYTES = 32

/** wslWrapperMarker is the marker one spawn accepts: the fixed prefix (T-F1)
 * and the spawn's own nonce, space-separated so the line reads
 * `<prefix> <nonce> <pid>`. The nonce is hex, so it carries no character the
 * shell, the marker's own parsing or a `startsWith` could confuse. */
export function wslWrapperMarker(nonce: string): string {
  return `${WSL_WRAPPER_MARKER_PREFIX} ${nonce}`
}

/**
 * wslWrapperFor builds the `sh -c` script for one WSL plan: it marks that a
 * child started **and names the pid it is about to become**, then `exec`s the
 * CLI with the CLI's own argv (T-F1, T-F4, decision D6(a)).
 *
 * The script is two statements and each one is load-bearing:
 *
 *   1. `printf '%s %s\n' <marker> "$$" >&2` -- the classification T-F1 needs
 *      and the pid T-F4 needs, on the one channel the console already reads
 *      (`WSL_WRAPPER_MARKER_PREFIX`). `$$` and not `$!`, and `exec` rather than a
 *      second process, because the pid that must be killed is the CLI's: after
 *      `exec` the shell *is* the CLI. `printf` rather than `echo` so the
 *      separator and the newline are the script's own and not the shell's
 *      echo-related quirk.
 *   2. `exec <the CLI's argv, each word quoted>` -- the CLI itself.
 *
 * Nothing here writes a file. The plan's D6(a) sketched a pid file, but no
 * spelling of one works across the two sides -- the shell's directory inside the
 * distribution is the checkout and the console's is a host path -- and the
 * arithmetic of reading it back is a second `wsl.exe` per cancellation. See
 * `WslWrapper`.
 *
 * **The marker carries a fresh nonce, drawn here** (see
 * `WSL_WRAPPER_MARKER_PREFIX`): `randomBytes` per plan, so the sentence the
 * console accepts is one only this script could have printed, and a CLI echoing
 * the fixed text -- or any text of its own -- cannot make the console kill a pid
 * of its choosing. The nonce is written into the script as a quoted constant,
 * which is why it is a value of the plan: the script the plan spawns and the
 * marker the readers accept are the same spawn's.
 *
 * A plan is built per spawn the console decides to make, so this is one nonce
 * per spawn. A supervisor that **restarts** a child on its backoff schedule
 * (`startSidecar`) re-runs the same plan and therefore prints the same marker:
 * the nonce is unguessable either way, and a restarted child's head starts
 * empty (`stderrHead` is cleared with it), so a line from the child before it is
 * not read into the one after.
 *
 * The script carries **no absolute path and no secret**: the words are the
 * CLI's own argv, each one through `shellQuoteWord` -- the one quoting function
 * in this module -- and every one of them has already passed Part D's
 * `distroShellUnsafe` refusal in `planFor`, so the quoting is the second layer
 * and not the only one. The nonce is one more word that goes through the same
 * quoting, and it is hex: nothing about it needs the shell's help.
 */
function wslWrapperFor(options: {
  readonly distro: string
  readonly cliWords: readonly string[]
}): WslWrapper {
  const marker = wslWrapperMarker(randomBytes(WSL_WRAPPER_NONCE_BYTES).toString('hex'))
  const statements = [`printf '%s %s\\n' ${shellQuoteWord(marker)} "$$" >&2`]
  statements.push(`exec ${options.cliWords.map(shellQuoteWord).join(' ')}`)
  return Object.freeze({
    words: Object.freeze([WSL_SHELL_PROGRAM, WSL_SHELL_FLAG, statements.join('; ')]),
    distro: options.distro,
    marker,
  })
}

/** WSL_STOP_SIGNAL is the signal the console sends inside the distribution.
 * `TERM` first, exactly as the Windows-side `SIGTERM` is sent first (section
 * 10.12); there is no second, stronger one, because `wsl.exe --terminate` is
 * refused (D6(b)) and a `KILL` inside the distribution would be a second
 * mechanism the plan does not have. */
export const WSL_STOP_SIGNAL = 'TERM'

/** wslStopArgs is the one extra `wsl.exe` a cancellation of a WSL plan sends
 * (T-F4, D6(a)): the distribution is named, `-e` ends `wsl.exe`'s own words and
 * runs the next one directly (no login shell, `WSL_EXEC_FLAG`), and that is a
 * `sh` running one `kill`.
 *
 * **The distribution is held to the same two rules as the plan's.** It is the
 * one string here that did not come from this module, so it passes Part D's
 * `isDistroName` and then `assertWslPrefix` -- the checks every other `-d` this
 * console writes passes -- and a name that fails either is a refusal rather
 * than a stop.
 *
 * **`pid` is a number, and that is the safety of this function.** The value
 * comes from the wrapper's own stderr line, and the console re-reads it as
 * digits before it becomes a `number` (`wrapperPidIn`) -- a line that says
 * anything else yields no stop at all rather than a word that would be shell
 * syntax (`sh -c` is given a command line, so an unvalidated value there would
 * be an injection surface, exactly the one `shellQuoteWord` closes for the CLI's
 * argv). Because it is a `number` here, no caller can pass a string that needs
 * quoting, and a number that is not a positive integer is refused too. */
export function wslStopArgs(options: { readonly distro: string; readonly pid: number }): readonly string[] {
  if (!isDistroName(options.distro)) {
    refused(`${JSON.stringify(options.distro)} is not a WSL distribution name: no stop is sent into it`)
  }
  if (!Number.isSafeInteger(options.pid) || options.pid <= 0) {
    refused(`${String(options.pid)} is not a process id: no stop is sent`)
  }
  const prefix = ['-d', options.distro, WSL_EXEC_FLAG]
  assertWslPrefix(prefix)
  return Object.freeze([
    ...prefix,
    WSL_SHELL_PROGRAM,
    WSL_SHELL_FLAG,
    `kill -${WSL_STOP_SIGNAL} ${String(options.pid)}`,
  ])
}

/**
 * stopInsideDistribution sends the one extra `wsl.exe` that a cancellation of a
 * WSL plan needs (T-F4, D6(a)): it is called once the Windows-side `wsl.exe`
 * has had `SIGTERM` and the shutdown grace and is still there, which means the
 * signal is not going to reach the Linux process by itself (F24, U5).
 *
 * **Nothing waits for it, and that is deliberate.** The caller has already
 * refused the read and is telling the operator; a stop that hangs must not
 * delay that sentence, and a stop that fails is not the read's failure. The
 * child is still started through the same spawner, so a test sees it as a
 * child like any other, and it is recorded in the spawn log (D1: one line per
 * child the app starts), which is what makes "the console stopped it inside the
 * distribution" a thing the operator's log says.
 */
function stopInsideDistribution(options: {
  readonly plan: SpawnPlan
  readonly spawner: ChildSpawner
  readonly log?: SpawnLogWriter | undefined
  readonly pid: number
}): void {
  const wrapper = options.plan.wrapper
  if (wrapper === null) return
  try {
    // Inside the `try`: a distribution or pid `wslStopArgs` refuses is a stop
    // that is not sent, never an exception out of a cancellation.
    const args = wslStopArgs({ distro: wrapper.distro, pid: options.pid })
    const stopper = options.spawner(options.plan.command, args, {
      cwd: options.plan.cwd,
      env: options.plan.env,
      shell: false,
      // Nothing is read: the stop's own answer is not part of any screen, and a
      // pipe nobody drains is a child that blocks on write.
      stdio: ['ignore', 'ignore', 'ignore'],
    })
    stopper.on('spawn', () => {
      options.log?.record({
        argv: [options.plan.command, ...args],
        cwd: options.plan.cwd,
        project: options.plan.project,
      })
    })
    // A missing or unstartable `wsl.exe` emits `error` rather than throwing;
    // listening keeps it from being an unhandled event, and there is nothing
    // else the console can do about it here.
    stopper.on('error', () => {
      /* the stop could not be sent */
    })
  } catch {
    /* the spawner threw: the stop could not be sent */
  }
}

/** WSL_TEXT_NUL and WSL_TEXT_BOM are the two code points a UTF-16LE `wsl.exe`
 * leaves in stderr that is read as UTF-8, and the only two this module removes
 * (review of `#981`).
 *
 * **Why they are there at all.** `WSL_UTF8=1` is set on every `wsl.exe` call
 * (`WSL_UTF8_VAR`), but U9 records that the switch is not honoured on every
 * build, and this machine cannot settle it. On a build that prints its own
 * warnings in UTF-16LE, every ASCII character of a warning arrives as
 * `<char> NUL`, and its line ending arrives as `\n NUL`: decoded as UTF-8 the
 * head looks like `…\0\r\0\n\0<the wrapper's UTF-8 line>`. A reader that split
 * on `\n` and asked `startsWith(marker)` then looked at a line *starting with a
 * NUL* -- and missed the wrapper's line entirely, which is worse than cosmetic:
 * a started CLI is reported as `wrapperStarted=false`, its exit status is
 * nulled, the installation is shown as `no-distro`, and a cancellation misses
 * the pid and can leave the CLI running inside the distribution.
 *
 * `NUL` is the boundary the missing newline becomes, and a leading `BOM` is the
 * other shape a `wsl.exe` answer starts with; both are removed from text this
 * module returns, and `NUL` counts as a line boundary while matching (see
 * `wrapperMarkerOffset`). */
const WSL_TEXT_NUL = '\u0000'
const WSL_TEXT_BOM = '\uFEFF'

/** withoutWslTextNoise removes those two code points from text the console will
 * read or show. What it leaves is the warning `wsl.exe` meant to print: removing
 * the NULs from UTF-16LE ASCII is exactly the ASCII. */
function withoutWslTextNoise(text: string): string {
  const stripped = text.split(WSL_TEXT_NUL).join('')
  return stripped.startsWith(WSL_TEXT_BOM) ? stripped.slice(1) : stripped
}

/** wrapperMarkerOffset finds **the wrapper's own line** in a bounded head of a
 * child's stderr and answers where it starts, or `-1` when the head holds none.
 * It is the one matcher: `wrapperLine`, `wrapperPidIn` and
 * `withoutWrapperMarker` all read the same offset, so the line that classifies a
 * spawn (T-F1), the line whose pid is killed (T-F4) and the line removed from
 * the operator's screen can never be three different lines.
 *
 * **Anywhere in the head, not at offset zero.** `wsl.exe` prints its own
 * warnings -- a relay notice, a proxy warning -- on stderr before the
 * distribution's command runs, so the wrapper's line is regularly not the first
 * thing on the pipe. A reader that insisted on the first bytes would call a
 * shell that did start a shell that did not (T-F1), and would leave the
 * console's own sentence on an operator's screen.
 *
 * **A line start, and the two spellings of one.** The marker is accepted where
 * it is at the start of the head, or immediately after `\n` -- the ordinary
 * case -- or immediately after a `NUL`, which is what a UTF-16LE warning with
 * **no trailing newline** leaves in front of it (the warning's own characters
 * are NUL-separated, and its last one is followed by the NUL that would have
 * been the BOM of a character): `W\0a\0r\0n\0<marker> 4321` is one glued line
 * under a `split('\n')`, and the console's line inside it is still the
 * console's. A `BOM` is accepted as a boundary for the same reason. So a CLI
 * that printed the marker's text in the middle of a line of its own is the CLI's
 * own text and not the wrapper's -- and it is not the wrapper's for the stronger
 * reason too: it does not carry this spawn's nonce.
 *
 * **The first matching line only, exactly once** (review of `#981`). A second
 * line that carries the marker is **not** consulted: it is not the shell's --
 * the script prints one line, before `exec` -- so it is the CLI's own text, and
 * reading a pid off it would be reading a pid the CLI chose. That is the
 * "ignored" half of the rule; the "noted" half is that it stays on the
 * operator's screen, because `withoutWrapperMarker` removes only the line this
 * function found. */
function wrapperMarkerOffset(head: string, marker: string): number {
  let from = 0
  for (;;) {
    const at = head.indexOf(marker, from)
    if (at < 0) return -1
    if (at === 0) return 0
    const before = head[at - 1]
    if (before === '\n' || before === WSL_TEXT_NUL || before === WSL_TEXT_BOM) return at
    from = at + 1
  }
}

/** wrapperLine is the wrapper's own line, without the NUL bytes a UTF-16LE
 * preamble may have put in or in front of it, or `null` when the head holds
 * none. */
function wrapperLine(head: string, marker: string): string | null {
  const at = wrapperMarkerOffset(head, marker)
  if (at < 0) return null
  const end = head.indexOf('\n', at)
  return withoutWslTextNoise(head.slice(at, end < 0 ? head.length : end))
}

/** wrapperMarkerSeen answers whether a child's stderr head holds the wrapper's
 * own line: `true` means a child inside the distribution started, and `false`
 * that `wsl.exe` failed before starting one (T-F1). `marker` is the plan's own
 * (`WslWrapper`), so the question is about **this** spawn's wrapper. */
function wrapperMarkerSeen(head: string, marker: string): boolean {
  return wrapperLine(head, marker) !== null
}

/** wrapperPidIn reads the pid the wrapper reported, out of the same bounded head
 * (`<marker> <pid>`, with `marker` the plan's own -- T-F4), or `null` when the
 * head holds no such line or the number on it is not a plain positive integer.
 * A head that holds no line for **this** marker has no pid to report: a line the
 * CLI wrote with the prefix alone is the CLI's text (see
 * `WSL_WRAPPER_MARKER_PREFIX`), and the answer to it is `null`, which is a
 * residual stated to the operator rather than a stop.
 *
 * The digits-only test is also what makes the value safe to hand to a shell:
 * `wslStopArgs` puts it in a `sh -c` command line, and a line that said
 * anything else would be shell syntax. Refusing here is the one guard, in the
 * one place, rather than a quoting rule every caller must remember. */
export function wrapperPidIn(head: string, marker: string): number | null {
  const line = wrapperLine(head, marker)
  if (line === null) return null
  const digits = line.slice(marker.length).trim()
  if (!/^[0-9]{1,10}$/.test(digits)) return null
  const pid = Number(digits)
  return pid > 0 ? pid : null
}

/** withoutWrapperMarker removes the wrapper's own line from the stderr the rest
 * of the console reads (T-F1): the CLI's sentence is what a screen shows, and
 * the marker -- with the pid on it -- is the console's own word about its own
 * wrapper.
 *
 * The line is removed **wherever it is**, because `wsl.exe`'s own preamble comes
 * before it (see `wrapperMarkerOffset`), and a line that ends in `\r` (a CRLF
 * from a relay that translated them) is the same line. Only a line that *starts*
 * with the marker is removed: a CLI that happened to print the same sentence's
 * text inside a line of its own keeps its own text. stderr with no such line has
 * no line removed -- and **that is the case the cleaning below is for**: a
 * `wsl.exe` that failed before the shell ever ran (`wrapperStarted === false`,
 * the no-distro failure above all) prints nothing but its own warnings, and on a
 * build that ignores `WSL_UTF8` every byte of them is UTF-16LE. There is no line
 * to remove there and there is still text an operator must be able to read, so
 * the cleaning is applied whenever the plan carries a wrapper and not only when
 * a line matched. Only a plan with **no wrapper at all** (`marker === null`, the
 * host route) is handed back untouched, because there the question does not
 * arise: this console wrote nothing into that stream to begin with.
 *
 * **One line goes, not every line that matches**: the line removed is the one
 * `wrapperLine` read (`wrapperMarkerOffset` answers it), which is the only line
 * the console may call its own. A second line carrying the marker is the CLI's
 * own text and stays on the screen, which is the visible half of "a second line
 * is ignored" (see `wrapperMarkerOffset`).
 *
 * What is left is **cleaned of the two code points a UTF-16LE `wsl.exe` leaves
 * in a UTF-8 read** (`WSL_TEXT_NUL`, a leading `WSL_TEXT_BOM`): removing the
 * NULs from UTF-16LE ASCII is the warning `wsl.exe` meant to print, so the
 * operator reads `wsl: a relay warning` rather than the same text with a NUL
 * between every pair of characters. That matters most where this text is the
 * whole answer: `probeVersion` turns a no-distro failure into its reason
 * (`wrapperStarted === false`) and the picker shows the same sentence, both
 * through `cliSentence`, which strips no control characters of its own. */
export function withoutWrapperMarker(stderr: string, marker: string | null): string {
  if (marker === null) return stderr
  const at = wrapperMarkerOffset(stderr, marker)
  if (at < 0) return withoutWslTextNoise(stderr)
  const end = stderr.indexOf('\n', at)
  const kept = stderr.slice(0, at) + stderr.slice(end < 0 ? stderr.length : end + 1)
  return withoutWslTextNoise(kept)
}

/** wrapperArgs is the words handed to `command` beyond the program itself: the
 * wrapper's prefix, then the CLI's argv.
 *
 * The CLI's program word is dropped **exactly when the command already is that
 * program** -- the host route, where `argv[0]` is the resolved binary's path and
 * the OS would otherwise be handed it twice. On the WSL route the command is
 * `wsl.exe` and the program word is `fugaro`, a word the distribution resolves,
 * so it is kept. The rule is stated as the equality rather than as a route flag
 * because the equality is the thing that matters: the program word is not
 * repeated when the command *is* it.
 *
 * A plan that carries a wrapper hands the CLI's argv to the shell instead, and
 * that argv is already inside the script's `exec` line (T-F4): appending it
 * again would start the CLI twice, once through the shell and once as
 * `sh`'s own positional words.
 */
function wrapperArgs(plan: SpawnPlan): readonly string[] {
  if (plan.wrapper !== null) return [...plan.prefix, ...plan.wrapper.words]
  const cli = plan.argv[0] === plan.command ? plan.argv.slice(1) : plan.argv
  return [...plan.prefix, ...cli]
}

/** planFor assembles a plan from an operation, its validated arguments and
 * main-process state. It is the only producer of a `SpawnPlan`, and it builds
 * both routes: the **host** route, where the command is the resolved binary and
 * the wrapper is empty, and the **WSL** route (`context.wsl`), where the command
 * is `wsl.exe`, the prefix is `['-d', <distro>, '--cd', <dir>, '-e']`, and the
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
    /** `runId` is the launch's own (`argvFor`'s `context.runId`): it is present
     * on `run.launch`'s plan and absent from every read's, so a read cannot
     * carry a run id and the one row that must cannot leave it out. */
    readonly runId?: string | undefined
  },
): SpawnPlan {
  const wsl = context.wsl
  if (wsl !== undefined) {
    const cliArgs = argvFor(operation, args, {
      target: context.target,
      directory: wsl.directory,
      runId: context.runId,
    })
    // Both rules of this route, and both before a process exists: the words the
    // distribution's shell would read as syntax, and the paths spelled for the
    // wrong side (T-D3, and Part D's carry-over for `$(…)` in a project name).
    // The quoting in `wslWrapperFor` is the second layer under this one, not a
    // replacement for it.
    assertDistroWordsAreSafe(wsl, context.target.configPath, cliArgs)
    // **The program word is the name, and never a path.** The distribution's
    // own `PATH` resolves it, which is the point of running the CLI inside
    // (T-C5). A record's `binaryPath` is the *Linux spelling* of the same file
    // (F15) and it is not what runs: it is what the console states (the
    // resolution's `path`, D3(a)) and its directory leads the child's `PATH`
    // below, so the `fugaro` the name resolves to is the one the record names.
    const cli: readonly string[] = Object.freeze([FUGARO_BINARY_NAME, ...cliArgs])
    return Object.freeze({
      operation,
      command: wsl.executable ?? wslExecutable(context.appEnv),
      prefix: wslWrapper(wsl),
      argv: cli,
      // The CLI is carried by a shell so that the pid inside the distribution
      // can be named and killed (T-F4, D6(a)) -- see `WslWrapper`. The pid is
      // not in the plan: it is the shell's own, and it arrives on the wrapper's
      // stderr line once the distribution has started it.
      wrapper: wslWrapperFor({ distro: wsl.distro, cliWords: cli }),
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
        // **`WSL_UTF8=1` on the CLI's own plan too** (T-F2, U9; review of
        // `#981`). It was on the queries only, and that was the gap the marker
        // defect arrived through: a CLI plan's `wsl.exe` prints its own relay
        // warnings on the same stderr the wrapper's line travels on, and on a
        // build that ignores this switch those warnings are UTF-16LE -- which
        // is what left a NUL in front of the wrapper's line.
        wslUtf8: true,
      }),
      project: context.target.project,
      backoff: context.backoff ?? DEFAULT_BACKOFF,
    })
  }
  const argv = argvFor(operation, args, {
    target: context.target,
    directory: context.directory,
    runId: context.runId,
  })
  return Object.freeze({
    operation,
    command: context.binaryPath,
    prefix: Object.freeze([]) as readonly string[],
    argv: Object.freeze([context.binaryPath, ...argv]),
    wrapper: null,
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
 * spawnedArgv is the **whole command line as it is spawned**: the command, the
 * wrapper's words, then the CLI's argv -- or, when a shell carries the CLI
 * (T-F4), the command, `wsl.exe`'s own words, and the `sh -c` script that
 * carries the CLI's argv. It is what the spawn log records, so the log's `argv`
 * line stays the operator's record of what ran (D1) on both routes: unchanged
 * on the host route, where there is no wrapper at all, and
 * `['C:\\…\\wsl.exe', '-d', 'Ubuntu', '--cd', '/home/dimi', '-e', 'sh', '-c',`
 * `printf '%s %s\n' 'fugaro-console: the WSL wrapper started <hex nonce>' "$$" >&2; exec`
 * `'fugaro' 'version']` on the WSL one -- the CLI's own words are inside the last
 * element, quoted, which is where they are when it runs. It is the first element
 * of this array that is the program, and that is why the log's line is still
 * "the binary's absolute path first".
 *
 * **The WSL route's last element carries that spawn's nonce** (see
 * `WSL_WRAPPER_MARKER_PREFIX`), so the line is the operator's record of what ran
 * *and* of the sentence the console accepted from it. The nonce is not a secret
 * -- it is in this line, which is the point of the log -- it is only unguessable
 * by the CLI's own output.
 */
export function spawnedArgv(plan: SpawnPlan): readonly string[] {
  return [plan.command, ...wrapperArgs(plan)]
}

/** RunResult is a child that was allowed to finish.
 *
 * `stdout` is decoded as UTF-8 unless the caller asked for `stdoutEncoding:
 * 'latin1'` (the WSL queries, T-F2), and then it is the child's bytes with one
 * code unit per byte: the caller decodes them.
 *
 * `wrapperStarted` is the one fact a `wsl.exe` failure cannot be read without
 * (T-F1, F26). On the WSL route the console does not start the CLI at all: it
 * starts `wsl.exe`, which starts a shell **inside a distribution**, and that
 * shell says so on stderr before it `exec`s the CLI (`WslWrapper.marker`). So
 *   - `true` -- the shell ran, and whatever failed next was the CLI's or the
 *     distribution's after it started a child: the CLI's own exit status;
 *   - `false` -- no shell ever ran: `wsl.exe` failed *before* starting the
 *     child, and its status is `wsl.exe`'s and not `fugaro`'s (U7, U8). Reading
 *     that number as a CLI exit code is the substitution F26 refuses;
 *   - `null` -- the plan carries no wrapper, so the question does not arise (the
 *     host route).
 */
export interface RunResult {
  readonly code: number | null
  readonly signal: string | null
  readonly stdout: string
  readonly stderr: string
  readonly wrapperStarted: boolean | null
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
    /** `stdoutEncoding` is how the child's stdout is decoded. `utf8` is the
     * ordinary answer -- every CLI document is UTF-8 -- and `latin1` is the one
     * alternative, for the WSL queries (T-F2): `wsl.exe --list --quiet` answers
     * in UTF-16LE on the machines WSL does not answer in UTF-8, and a UTF-8
     * decode destroys a UTF-16LE BOM before the caller can look at it. `latin1`
     * maps each byte to one code unit, which is a lossless carry for any byte
     * string, so the caller decodes the bytes it actually has. */
    readonly stdoutEncoding?: 'utf8' | 'latin1'
    /** `stdinBytes` is what the child is given on its standard input, and
     * **the launch is the one caller that passes it** (design §5.1 rule 11):
     * the body is written and the stream is ended immediately. It is the only
     * thing that asks for `'pipe'` on stdin -- every other read asks for
     * `'ignore'`, so a child that reads its stdin sees it closed. A child that
     * never drains the pipe is not waited for: the write is not awaited. */
    readonly stdinBytes?: Uint8Array
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
  const wrapper = plan.wrapper
  // The marker **this** spawn's wrapper printed, or `null` on the host route,
  // where there is no wrapper and so no line to look for (`WslWrapper.marker`).
  const marker = wrapper === null ? null : wrapper.marker

  return new Promise<RunResult>((resolve, reject) => {
    let child: SpawnedChildLike
    try {
      child = spawner(binary, wrapperArgs(plan), {
        cwd: plan.cwd,
        env: plan.env,
        shell: false,
        stdio: [deps.stdinBytes === undefined ? 'ignore' : 'pipe', 'pipe', 'pipe'],
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
      // The task is written the moment the process exists, and the stream is
      // ended with it: the CLI's `taskText` reads stdin to EOF, so a stream
      // left open is a launch the CLI waits on for ever. It is written and not
      // waited for -- a child that exits without reading must not hang the
      // console, and the deadline below is what bounds the whole thing.
      if (deps.stdinBytes !== undefined) {
        // An `error` listener, because this is the one child whose stdin the
        // console writes to: a CLI (or a `wsl.exe`) that exits before draining
        // it -- a bad flag, a config refusal, an `sh -c` that dies at once --
        // closes the read end, and the write's `EPIPE` arrives as an `error`
        // event on the socket. With nothing listening, Node raises that as an
        // uncaught exception in the **main process**: a crashed console on the
        // one row the design allows to write to a child. The failure it
        // describes is already handled where every other failure is -- the
        // child's exit code, its stderr and the deadline below -- so there is
        // nothing to do here but not crash.
        child.stdin?.on('error', () => undefined)
        child.stdin?.write(deps.stdinBytes)
        child.stdin?.end()
      }
    })
    let stdout = ''
    let stderr = ''
    /** `stderrHead` is a bounded **prefix** of stderr (`STDERR_HEAD_LIMIT`).
     * It is not `stderr` itself: that one is bounded *from the end*, so a CLI
     * with a long answer of its own would push the wrapper's line -- which
     * arrived first -- out of it, and the console needs the wrapper's line for
     * both of its facts: whether a child started (T-F1) and the pid to kill
     * (T-F4). A prefix rather than a suffix because the wrapper prints before
     * `exec`, and bounded because a child that prints without end must not
     * grow it. */
    let stderrHead = ''
    let settled = false
    let exited = false
    let deadline: ReturnType<typeof setTimeout> | null = null

    const clearDeadline = (): void => {
      if (deadline !== null) {
        timers.clearTimeout(deadline)
        deadline = null
      }
    }
    /** stopInsideDistribution is T-F4's second half (D6(a)): killing the
     * Windows-side `wsl.exe` is not documented to kill the Linux process it
     * started (F24, U5), so after the grace the console sends **one** more
     * `wsl.exe` -- into the same distribution -- that kills the pid the wrapper
     * reported. The pid is read from the wrapper's own stderr line, and a pid
     * that cannot be read is not guessed at: the caller is told, in `residual`,
     * that the process may still be running. */
    // Stopping first, rejecting second: the caller's rejection must mean "the
    // child has been told to go", not "the child is still out there".
    const abandon = (error: Error): void => {
      if (settled) return
      settled = true
      clearDeadline()
      // `endedOnItsOwn` is whether the child was **already gone when the stop
      // began**, and it is the only thing the console may read as "the process
      // inside the distribution is gone too": `wsl.exe` relays the
      // distribution's command, so a `wsl.exe` that exited by itself exited
      // because that command had finished. A `wsl.exe` that exits *after* the
      // signal is not that evidence, and reading `exited` later in this function
      // is the bug this flag exists for: on Windows the `SIGTERM` below is
      // `TerminateProcess`, so the Windows-side process is gone within a tick
      // while the Linux-side one -- the process F24 is about -- is still there.
      const endedOnItsOwn = exited
      // `wrapperStarted` is the fact a `RunResult` would have carried, read off
      // the head for the same reason the pid is: a read that times out returns
      // no result, and `probeVersion` has nothing else to classify a WSL-route
      // timeout by (T-F1, F26).
      const noteWrapper = (): void => {
        if (error instanceof SpawnTimeout) {
          error.wrapperStarted = marker === null ? null : wrapperMarkerSeen(stderrHead, marker)
        }
      }
      if (endedOnItsOwn) {
        noteWrapper()
        reject(error)
        return
      }
      // The pid is read **now**, before the grace runs out, so that the common
      // case -- the wrapper has already reported it -- needs no second read:
      // the shell prints it before it `exec`s the CLI, which is as soon as the
      // child is running.
      const pid = marker === null ? null : wrapperPidIn(stderrHead, marker)
      // Whether a residual is owed is decided by the read **inside** the timer,
      // not here: a cold distribution can print the wrapper's line a moment
      // after this deadline, and a pid that arrives during the grace is a pid
      // the console can name. Reading it here only would leave the stop naming
      // `null` while the process it could have stopped keeps running, and would
      // state a residual that is already untrue.
      const needsPid = marker !== null && pid === null
      child.kill('SIGTERM')
      if (!needsPid) {
        // Nothing is owed to the operator, so the refusal is not held back:
        // the pid is named and the sentence below is the whole answer.
        noteWrapper()
        reject(error)
      }
      timers.setTimeout(() => {
        // The Windows-side child gets the stronger signal only if it is still
        // there to receive it -- but the stop **inside** the distribution is
        // sent whatever `wsl.exe` did, because its own exit is not evidence
        // about the Linux process (see `endedOnItsOwn`). A stop that waited
        // for `wsl.exe` to still be alive would never be sent on Windows at
        // all, which is F24's stated consequence: a cancelled read leaving a
        // `fugaro` holding a lease inside the distribution with nothing on
        // screen to say so.
        if (!exited) child.kill('SIGKILL')
        const late = needsPid ? wrapperPidIn(stderrHead, marker) : pid
        if (late !== null) stopInsideDistribution({ plan, spawner, log: deps.log, pid: late })
        if (needsPid) {
          // The residual is stated where the operator reads it, and here is the
          // last moment it can be stated *truthfully*: the grace has run, so
          // whether the console could name the process is settled. Appended to
          // the sentence the read is refused with (D6(a): "a residual window
          // that must be written down"). A pid that arrived in the meantime
          // means there is no window to write down -- the stop above named it.
          noteWrapper()
          reject(
            late === null
              ? withResidual(
                  error,
                  'the wrapper reported no pid, so the process inside the distribution could not be stopped; it may still be running',
                )
              : error,
          )
        }
      }, grace)
    }

    deadline = timers.setTimeout(() => {
      deadline = null
      abandon(new SpawnTimeout(`fugaro ${what} did not finish within ${Math.round(timeoutMs / 1000)}s`))
    }, timeoutMs)

    readText(
      child.stdout,
      (text) => {
        if (settled) return
        if (stdout.length + text.length > stdoutLimit) {
          abandon(new Error(`fugaro ${what} printed more than ${stdoutLimit} characters`))
          return
        }
        stdout += text
      },
      deps.stdoutEncoding ?? 'utf8',
    )
    readText(child.stderr, (text) => {
      if (stderrHead.length < STDERR_HEAD_LIMIT) {
        stderrHead = (stderrHead + text).slice(0, STDERR_HEAD_LIMIT)
      }
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
      resolve({
        code,
        signal,
        stdout,
        stderr: withoutWrapperMarker(stderr, marker),
        wrapperStarted: marker === null ? null : wrapperMarkerSeen(stderrHead, marker),
      })
    })
  })
}

/** withResidual appends one clause to a refusal's sentence: what the console
 * could **not** do, said out loud (T-F4, D6(a)). It exists so that a stop that
 * could not name the process inside the distribution never reads as a stop that
 * did -- the operator is told the process may still be running, and where the
 * console looked. It keeps the error's own class, so `probeVersion` still tells
 * a `SpawnTimeout` from a spawn that failed outright. */
function withResidual(error: Error, residual: string): Error {
  if (!(error instanceof SpawnTimeout)) return new Error(`${error.message}; ${residual}`)
  const wrapped = new SpawnTimeout(`${error.message}; ${residual}`)
  wrapped.wrapperStarted = error.wrapperStarted
  return wrapped
}

/** SpawnTimeout is the rejection of a read that missed its deadline, as its
 * own class so `probeVersion` can tell it from a spawn that failed outright.
 *
 * `wrapperStarted` is `RunResult`'s own fact, carried on the rejection because
 * a read that times out never produces a `RunResult` and there is no other way
 * for `probeVersion` to say whether a child inside the distribution had started
 * at all: with `null` for both, a WSL-route timeout would be indistinguishable
 * from a host one (T-F1, F26). `runToCompletion` writes it at the moment it
 * refuses -- the marker's own answer, read off the same bounded head the pid
 * comes from -- and a `SpawnTimeout` built anywhere else leaves it `null`. */
export class SpawnTimeout extends Error {
  override readonly name = 'SpawnTimeout'
  wrapperStarted: boolean | null = null
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
 *   - `no-distro` -- WSL answered and there is no distribution this console can
 *     drive by the name the installation carries. It has **two shapes** (Part E,
 *     T-E3, added the second):
 *       1. the name is simply not among the ones `wsl.exe` lists, which is a
 *          rename or a removal, and the reason carries the whole list;
 *       2. the name *is* listed and is one this console will not hand to
 *          `wsl.exe` -- a registered name beginning with `-` (see
 *          `isSafeDistroName`), which is a name that would be read as one of
 *          `wsl.exe`'s own flags.
 *     The instruction to the operator is the same in both -- choose or add a
 *     distribution -- which is why the second shape did not add a fifth kind: a
 *     kind exists to say which of the four *remedies* applies, and the remedy
 *     here is this one. The sentence in `reason` carries what is specific
 *     (F26/F31's remedy list is unaffected);
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
 *   - **`no-distro`** -- WSL answered, and there is no distribution this console
 *     can drive by the name the installation carries. Its two shapes are both
 *     answered here, and they are told apart by their sentences rather than by
 *     their kind (see `BinaryRefusalKind`):
 *       1. the distribution is **not among them**. The list `wsl.exe` gave is in
 *          the sentence, because the distribution was renamed or removed and the
 *          operator is the only one who can say which;
 *       2. the distribution **is among them**, and the name is one this console
 *          will not hand to `wsl.exe` -- it begins with `-`
 *          (`isSafeDistroName`). It is refused here, with a sentence, rather than
 *          left for the wrapper's own guard to throw out of this async read;
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

  // A name this machine *lists* but this console will not hand to `wsl.exe`
  // (T-E3, `isSafeDistroName`): the name is the one word `wsl.exe` reads as a
  // selector, so a listed name that begins with `-` -- a custom registration can
  // be called `-sneaky` -- is answered here rather than thrown from the wrapper's
  // own guard. `assertWslPrefix` is the last line of the boundary and not an
  // error path an operator should meet as a crash. `no-distro` and not a fifth
  // kind: **this is the second shape of `no-distro`**, documented on
  // `BinaryRefusalKind` -- the name is not one this console has a working route
  // to, and the remedy the kinds drive (choose or add a distribution) is the
  // same one; the reason below carries what is specific to this case.
  if (!isSafeDistroName(distro)) {
    return {
      ok: false,
      path: null,
      kind: 'no-distro',
      reason:
        `the distribution this installation names, ${quote(distro)}, is listed by wsl.exe and is not a name ` +
        `this console will hand to wsl.exe: it must not begin with "-"`,
    }
  }
  // The second half of the same answer, and Part D's rule (T-D3): the WSL route
  // runs the CLI through the distribution's own shell, so a listed name outside
  // `DISTRO_NAME_PATTERN` -- `My Linux`, `dimi's distro`, `Ubuntu;x` -- is
  // refused here with a sentence (fail closed) rather than thrown out of
  // `planFor`'s guard as an exception from an async read.
  if (!isDistroName(distro)) {
    return {
      ok: false,
      path: null,
      kind: 'no-distro',
      reason:
        `the distribution this installation names, ${quote(distro)}, is listed by wsl.exe and is not a name ` +
        `this console will run a shell in: a distribution name is ${String(MAX_DISTRO_NAME_LENGTH)} characters ` +
        'or fewer of letters, digits, dot, dash and underscore -- register it under a plain name ' +
        '(wsl --export and wsl --import) to drive it from the console',
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
    // No child ever started inside the distribution (T-F1, F26): `wsl.exe`
    // itself failed, and its status and sentence are `wsl.exe`'s. The
    // distribution is one the machine listed, so what the operator has to fix
    // is the distribution starting at all -- not a CLI to install, which is
    // what `no-binary` would have said, and not a status to read as `fugaro`'s,
    // which `probe.code` no longer offers (U7, U8).
    if (probe.wrapperStarted === false) {
      return {
        ok: false,
        path: reported,
        kind: 'no-distro',
        reason: `the distribution ${quote(distro)} could not be started: ${probe.reason}`,
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
      /** `wrapperStarted` is `RunResult`'s own fact, carried through so a caller
       * can tell "the CLI answered and failed" from "`wsl.exe` never started a
       * child" (T-F1, F26). See `RunResult`. */
      readonly wrapperStarted: boolean | null
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
      // The sentence is `runToCompletion`'s own, residual and all. A deadline
      // on the WSL route is the one case where the console may have left a
      // `fugaro` running inside the distribution -- a cold distribution can
      // still be starting when the five seconds are up -- and the console says
      // so there, where it can still know it (D6(a), T-F4). Rebuilding a
      // sentence here is what threw that away, and the installations list is
      // exactly where such a probe is made: the operator read an ordinary
      // timeout while the process it could not name kept running.
      return {
        ok: false,
        code: null,
        said: null,
        reason: error.message,
        wrapperStarted: error.wrapperStarted,
      }
    }
    throw error
  }
  if (result.signal !== null) {
    return {
      ok: false,
      code: null,
      said: null,
      reason: `the binary was killed by ${result.signal} while reporting its version`,
      wrapperStarted: result.wrapperStarted,
    }
  }
  const said = result.stderr.trim() === '' ? null : result.stderr.trim()
  if (result.code !== 0) {
    // The status is the CLI's only when a child inside the distribution
    // actually started (T-F1, F26): on the WSL route the program that exited
    // may be `wsl.exe`, and its status is not a `fugaro` exit code (U7, U8).
    // The sentence says which of the two it was, because a reason that read
    // `wsl.exe`'s number as the CLI's would send an operator to debug the
    // wrong program.
    // `code` is deliberately `null` in the `wsl.exe` case: the number is in the
    // sentence, where it is read as `wsl.exe`'s, and it is **not** in the field
    // a classifier reads (F26).
    const wasWsl = result.wrapperStarted === false
    return {
      ok: false,
      code: wasWsl ? null : result.code,
      said,
      reason: `${wasWsl ? 'wsl.exe exited' : 'the binary exited'} ${String(result.code)} for \`version\`: ${
        cliSentence(result.stderr) ?? 'no message'
      }`,
      wrapperStarted: result.wrapperStarted,
    }
  }
  // The child answered and the answer was not a version: `code` is 0, because
  // the status is the child's own and it is not the failure here. A caller that
  // classifies by the status must not read this as "the command was not found".
  const parsed = parseVersionLine(result.stdout)
  return parsed.ok ? parsed : { ok: false, code: result.code, said, reason: parsed.reason, wrapperStarted: result.wrapperStarted }
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
  /** `stderrHead` is the bounded prefix of the current child's stderr, the same
   * fact `runToCompletion` keeps and for the same two reasons (T-F1, T-F4): the
   * wrapper's line says whether a child inside the distribution started, and it
   * carries the pid the stop inside the distribution names. It is reset with
   * `stderr` on every spawn, because a restarted feed has a new child and a new
   * pid. */
  let stderrHead = ''
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
    stderrHead = ''
    buffer = ''
    discarding = false
    readText(started.stdout, consume)
    readText(started.stderr, (text) => {
      if (stderrHead.length < STDERR_HEAD_LIMIT) {
        stderrHead = (stderrHead + text).slice(0, STDERR_HEAD_LIMIT)
      }
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
        // The wrapper's own line is removed here as it is in `runToCompletion`
        // (T-F1): it is the console's word about its own wrapper, and the
        // sentence a screen shows is the CLI's.
        finish({
          reason: decision.reason,
          code,
          signal,
          sentence: cliSentence(withoutWrapperMarker(stderr, plan.wrapper?.marker ?? null)),
          spawns,
        })
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
      // The same second half as `runToCompletion`'s stop (T-F4, D6(a)): a
      // `wsl.exe` that is still there after the grace is not going to carry the
      // signal into the distribution, so the console names the pid the wrapper
      // reported and kills it there. A pid it cannot read is a residual stated
      // to the operator -- `onNotice`, the channel a running feed already has
      // for what it could not do -- rather than a stop that silently did not
      // happen.
      const wrapper = plan.wrapper
      // The marker this plan's script printed -- one plan, so one marker across
      // restarts, and the head it is read off is cleared on every one of them
      // (`WslWrapper.marker`).
      const marker = wrapper === null ? null : wrapper.marker
      // `child` is not null here: `stop()` returned above for a child that was
      // already gone -- nulled by `decide`, which `close` and the drain timer
      // after `exit` both call -- so this stop is armed for a child that was
      // still running when it was asked for.
      const pid = marker === null ? null : wrapperPidIn(stderrHead, marker)
      timers.setTimeout(() => {
        // The Windows-side child gets the stronger signal only if it is still
        // there to receive it; the stop **inside** the distribution is sent
        // whatever `wsl.exe` did, exactly as in `runToCompletion`. On Windows
        // the `SIGTERM` above is `TerminateProcess`, so `close` arrives within
        // a tick and `child` is null by the time this timer fires -- which is
        // why `child === null` must not return here: that return is the normal
        // case on Windows, and it is the one that leaves a `fugaro` holding a
        // lease inside the distribution with nothing on screen to say so.
        if (child !== null) child.kill('SIGKILL')
        if (wrapper === null) return
        // The pid is read **again** here rather than used as it was at stop
        // time: a cold distribution can print the wrapper's line a moment after
        // the stop began, and that pid is one the console can name. Deciding
        // from the stale value would skip the one `wsl.exe` that stops the
        // process, and -- the other half of the same mistake -- would tell the
        // operator the process "may still be running" after the console had
        // just killed it.
        const late = pid ?? (marker === null ? null : wrapperPidIn(stderrHead, marker))
        if (late === null) {
          events.onNotice({
            text:
              'the feed was stopped, but the wrapper reported no pid: the process inside the distribution may still be running',
            receivedAt: stamp(),
          })
          return
        }
        stopInsideDistribution({ plan, spawner, log: deps.log, pid: late })
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
