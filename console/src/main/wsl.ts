// The WSL seam's own module (docs/console-windows/implementation-plan.md Part C,
// T-C4; decisions D2(a) and D4(a)).
//
// **What lives here.** Everything the console says to `wsl.exe` *as a program*
// rather than to the CLI inside a distribution: where `wsl.exe` is, and the
// question the console asks before any CLI exists -- which distributions this
// machine has, and which one is the default. `spawn.ts` owns the CLI's argv and
// its environment; this module owns the Windows-side program that argv is handed
// to.
//
// **One source of truth about what a distribution is.** `wslDistributions` is
// the only place a distribution's name and its default-ness are read, so the
// picker (Part G) and the resolver (T-C5) cannot disagree about what the machine
// has -- a picker that offered one list and a resolver that matched another would
// be two answers to one question.
//
// **The three outcomes, and why an empty list is not one of them.** The question
// "which distributions are there?" has three honest answers on Windows, and the
// console has to tell them apart:
//
//   - `listed` with names -- WSL is there and has distributions;
//   - `listed` with no names -- WSL answered, and it has none. This is a state,
//     not a failure, and Part G draws it as its own page: "install a
//     distribution" is a different instruction from "install WSL";
//   - `absent` -- `wsl.exe` could not answer at all: it is not there, the
//     feature is off, or the answer did not arrive in time. The refusal carries
//     the sentence `wsl.exe` printed, because "Windows Subsystem for Linux has
//     no installed distributions" and "the Windows Subsystem for Linux optional
//     component is not enabled" are different instructions too.
//
// Collapsing the third into the second is the failure this module exists to
// prevent: an empty list on a machine with no WSL at all would send the operator
// looking for a distribution to install when the thing that is missing is WSL
// itself.
//
// **A default is a fact about the machine, not a choice the console makes.**
// `wsl.exe --status` names the default distribution, and the console marks it
// rather than picking one: with no default named, the caller must ask the
// operator (Part G), and with a default named it is offered first. Nothing here
// ever falls back to "the first one in the list", which would be a choice the
// operator never made and cannot see.

import { DEFAULT_BACKOFF, FUGARO_BINARY_NAME, childEnvironment, runToCompletion } from './spawn'
import type { ChildSpawner, SpawnPlan, Timers, WslQueryName } from './spawn'
import type { SpawnLogWriter } from './spawnlog'

/** WSL_EXE_NAME is the program's name on Windows. It is data rather than a
 * platform fact, exactly as `test/unit/wslstub.ts`'s double of the same name is:
 * the console spawns this string on Windows and a test spawns a file of this
 * name on POSIX. */
export const WSL_EXE_NAME = 'wsl.exe'

/** WSL_SYSTEM_DIRECTORY is the Windows component directory `wsl.exe` is
 * installed in. */
export const WSL_SYSTEM_DIRECTORY = 'System32'

/** WSL_SYSTEM_ROOT is `%SystemRoot%`'s value on every Windows installation this
 * console has been asked about, and the answer used when the app's own
 * environment does not carry a usable one. */
export const WSL_SYSTEM_ROOT = 'C:\\Windows'

/**
 * WSL_QUERY_TIMEOUT_MS is the deadline for the questions in this module. It is
 * longer than the version handshake's five seconds on purpose: U15 records that
 * a *cold-starting* distribution can take seconds to answer its first
 * invocation, and a query that is stopped early would be classified as `absent`
 * on a machine where WSL is installed and merely slow.
 */
export const WSL_QUERY_TIMEOUT_MS = 15_000

/**
 * NO_DEFAULT_SPELLINGS are the values a `Default Distribution:` line has been
 * seen to carry when there is no default. An empty value is what a build that
 * prints the label with nothing after it produces, and `(none)` is what the
 * builds that spell it out produce; both mean the same thing, and both send the
 * caller to "ask the operator" rather than to "use the first one".
 */
export const NO_DEFAULT_SPELLINGS: readonly string[] = Object.freeze(['', '(none)'])

/** DEFAULT_LINE matches `wsl.exe --status`'s one line about the default. The
 * label is matched case-insensitively and the value is taken verbatim, because
 * the rest of the line is a distribution's name and a name is data. */
const DEFAULT_LINE = /^Default Distribution:[ \t]*(.*)$/i

/** SYSTEM_ROOT_RE is what a `%SystemRoot%` may look like for this module to use
 * it: one drive-rooted directory level, no separator inside it, no `..`, and
 * nothing outside the alphabet Windows allows in a directory name. See
 * `wslExecutable`. */
const SYSTEM_ROOT_RE = /^[A-Za-z]:\\[A-Za-z0-9 ._-]+$/

/**
 * wslExecutable is the `wsl.exe` the console runs, as an **absolute** path.
 *
 * It is absolute rather than the bare name for the reason design section 3.4
 * rule 4 gives about the CLI: the app's inherited `PATH` is not a place anything
 * is installed, and a console that resolved `wsl.exe` through it would be
 * running whatever the operator's environment happened to put first. The
 * Windows component directory is a fact about Windows instead, and the app's
 * environment is asked for `%SystemRoot%` only because a Windows installation
 * is allowed to live somewhere other than `C:\Windows`.
 *
 * **That answer is checked rather than trusted.** It is a path this console
 * executes, so a value that is not one plain drive-rooted directory name -- a
 * UNC path, a relative path, a path with `..` in it, a path with a forward slash
 * or a NUL -- is not a Windows directory and the default is used instead. The
 * check is deliberately narrow: the environment can be set by anything that
 * started the app, and "the app runs what the environment names" is the property
 * this whole module's callers exist to avoid.
 */
export function wslExecutable(appEnv: Readonly<Record<string, string | undefined>>): string {
  const fromEnv = appEnv['SystemRoot'] ?? appEnv['windir']
  const root = fromEnv !== undefined && SYSTEM_ROOT_RE.test(fromEnv) ? fromEnv : WSL_SYSTEM_ROOT
  return `${root}\\${WSL_SYSTEM_DIRECTORY}\\${WSL_EXE_NAME}`
}

/** WslDistribution is one distribution this machine has, and whether
 * `wsl.exe --status` named it the default. `isDefault` is false for every row
 * when no default is named -- see `wslDistributions` -- so a caller that must
 * have a default asks the operator rather than reading the first row. */
export interface WslDistribution {
  readonly name: string
  readonly isDefault: boolean
}

/**
 * WslDistributions is the answer to "which distributions are there?", in the
 * three shapes the module header describes. `listed` is an answer (possibly an
 * empty one); `absent` is the absence of one, with the sentence `wsl.exe` gave
 * or the reason it gave none.
 */
export type WslDistributions =
  | { readonly kind: 'listed'; readonly distributions: readonly WslDistribution[] }
  | { readonly kind: 'absent'; readonly reason: string }

/**
 * wslDistributions asks `wsl.exe` what this machine has: `--list --quiet` for
 * the names, in the order `wsl.exe` gave them, and `--status` for which of them
 * is the default.
 *
 * **Two invocations, and the second is only a nicety.** A `--list` that cannot
 * answer is `absent` and `--status` is not asked: there is nothing to mark. A
 * `--list` that answers and a `--status` that does not is `listed` with **no**
 * default marked, because the list is the answer to the question that was asked
 * and the default is a detail of it -- an operator who has distributions and a
 * broken `--status` must still see their distributions.
 *
 * `cwd` is required rather than defaulted: the query is a child process like any
 * other, and this module will not start one in whatever directory the app
 * happens to be in (rule 5's "never the app's own").
 */
export async function wslDistributions(options: {
  readonly appEnv: Readonly<Record<string, string | undefined>>
  /** `cwd` is the directory the query runs in. It is a host directory: this
   * child is `wsl.exe` itself, not the CLI. */
  readonly cwd: string
  /** `project` is what the spawn log records for these children. A query that
   * belongs to an installation passes that installation's project; the picker's
   * has none and passes nothing. */
  readonly project?: string | undefined
  /** `wslPath` replaces the program `wslExecutable` computes. It is how a test
   * points the console at the fake (T-A1), and the only reason it exists. */
  readonly wslPath?: string | undefined
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timers?: Timers
  readonly timeoutMs?: number
}): Promise<WslDistributions> {
  const project = options.project ?? ''
  const program = options.wslPath ?? wslExecutable(options.appEnv)
  const shared = {
    program,
    appEnv: options.appEnv,
    cwd: options.cwd,
    project,
    ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
    ...(options.log === undefined ? {} : { log: options.log }),
    ...(options.timers === undefined ? {} : { timers: options.timers }),
    ...(options.timeoutMs === undefined ? {} : { timeoutMs: options.timeoutMs }),
  }

  const list = await runWslQuery({ ...shared, operation: 'wsl.distributions', prefix: [], argv: ['--list', '--quiet'] })
  if (!list.ok) return { kind: 'absent', reason: list.reason }
  const names = parseDistributionList(list.stdout)
  if (names === null) {
    // The one thing this module refuses to guess about: an answer that is not
    // text this decoder can read is not a list of names, and half of one would
    // be a distribution the console then tries to run. See `parseDistributionList`
    // for what T-F2 does about the encoding itself.
    return {
      kind: 'absent',
      reason:
        'wsl.exe answered `--list --quiet` in bytes that are not UTF-8 text: the list of ' +
        'distributions cannot be read, and a partial read would name distributions that are not there',
    }
  }

  const status = await runWslQuery({ ...shared, operation: 'wsl.distributions', prefix: [], argv: ['--status'] })
  const preferred = status.ok ? parseDefaultDistribution(status.stdout) : null
  return {
    kind: 'listed',
    distributions: Object.freeze(
      names.map((name) =>
        Object.freeze({ name, isDefault: preferred !== null && preferred === name }),
      ),
    ),
  }
}

/** WslQueryResult is one query's answer: the bytes `wsl.exe` printed, or the
 * reason there are none. */
type WslQueryResult =
  | { readonly ok: true; readonly stdout: string }
  | { readonly ok: false; readonly reason: string }

/**
 * runWslQuery runs one `wsl.exe` question **through the ordinary spawn path**:
 * `runToCompletion`, with its deadline, its stdout cap, its `SIGTERM`-then-
 * `SIGKILL` stop and its spawn-log line. There is no second spawner in this
 * repository, and a query that bypassed this one would be a child process the
 * app started and the spawn log did not record.
 *
 * The plan it builds is a plan like any other -- command, argv, cwd, the
 * environment from `childEnvironment` and nothing else -- with one difference:
 * its `operation` is a `WslQueryName` rather than a CLI operation, because a
 * query drives no command of the CLI's (`assertSpawnSurface`'s table is about
 * those, and it must not grow a row for `--list`).
 *
 * `prefix` is the difference between the two kinds of question this module asks.
 * The questions *about the machine* (`--list`, `--status`) run `wsl.exe` on the
 * host and have no wrapper at all; the questions asked **inside a distribution**
 * (`wslpath`, T-D2) name it with `-d <distro> --`, and the program word after
 * the `--` is the distribution's own program rather than the CLI.
 */
async function runWslQuery(options: {
  readonly operation: WslQueryName
  readonly program: string
  readonly prefix: readonly string[]
  /** `argv` is the far side's argv, program word first: `['--list','--quiet']`
   * for a host question, `['wslpath','-a','-u',<path>]` for a translation. */
  readonly argv: readonly string[]
  readonly appEnv: Readonly<Record<string, string | undefined>>
  readonly cwd: string
  readonly project: string
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timers?: Timers
  readonly timeoutMs?: number
}): Promise<WslQueryResult> {
  const plan: SpawnPlan = Object.freeze({
    operation: options.operation,
    command: options.program,
    prefix: Object.freeze([...options.prefix]) as readonly string[],
    argv: Object.freeze([...options.argv]),
    cwd: options.cwd,
    // The same environment rule as any other spawn, with the **win32** row's
    // child `PATH` -- which is Linux's (`WIN32_LOCATIONS`, T-C2), because the
    // process on the far side of `wsl.exe` is a Linux process. The binary named
    // here is the CLI's bare name rather than the program being run: the
    // environment this builds is the one the *distribution* will see, and the
    // distribution resolves `fugaro` itself.
    env: childEnvironment({
      target: { project: options.project },
      binaryPath: FUGARO_BINARY_NAME,
      appEnv: options.appEnv,
      platform: 'win32',
    }),
    project: options.project,
    backoff: DEFAULT_BACKOFF,
  })

  let result
  try {
    result = await runToCompletion(plan, {
      ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
      ...(options.log === undefined ? {} : { log: options.log }),
      ...(options.timers === undefined ? {} : { timers: options.timers }),
      timeoutMs: options.timeoutMs ?? WSL_QUERY_TIMEOUT_MS,
    })
  } catch (error) {
    return {
      ok: false,
      reason: `wsl.exe did not answer \`${options.argv.join(' ')}\`: ${describe(error)}`,
    }
  }
  if (result.code !== 0) {
    const said = result.stderr.trim()
    return {
      ok: false,
      reason:
        `wsl.exe exited ${String(result.code)} for \`${options.argv.join(' ')}\`` +
        (said === '' ? ' and said nothing' : `: ${said}`),
    }
  }
  return { ok: true, stdout: result.stdout }
}

// ---------------------------------------------------------------------------
// wslpath: the translation, both directions (T-D2, D4(a))
// ---------------------------------------------------------------------------

/** WSLPATH_PROGRAM is the distribution's own path-translation tool. It is asked
 * rather than reimplemented: see `WslPathTranslation`. */
export const WSLPATH_PROGRAM = 'wslpath'

/** WSLPATH_FLAG_ABSOLUTE asks for an absolute answer -- `wslpath -a`. The
 * console passes it so the answer can be handed to a child as a working
 * directory whatever the distribution's own cwd was. */
export const WSLPATH_FLAG_ABSOLUTE = '-a'

/** WSLPATH_FLAG_TO_LINUX (`-u`) reads a Windows spelling and answers the
 * distribution's; WSLPATH_FLAG_TO_WINDOWS (`-w`) is the other way. */
export const WSLPATH_FLAG_TO_LINUX = '-u'
export const WSLPATH_FLAG_TO_WINDOWS = '-w'

/** WslPathDirection is which way a translation goes. */
export type WslPathDirection = 'toLinux' | 'toWindows'

/**
 * WslPathTranslation is one translation's answer: the other side's spelling, or
 * **a stated refusal**.
 *
 * **Why this is a process and not a function.** `C:\Users\X` -> `/mnt/c/Users/X`
 * is the common case, and a mapping computed in the console is right for it and
 * wrong for every mount the operator configured, every `--cd` outside `/mnt`,
 * and every distribution with its own `/etc/wsl.conf` (D4(b)). A console that
 * computed it would hand a child a directory that does not exist while believing
 * it had answered -- which is why the distribution is asked, and why a path it
 * cannot translate is a sentence rather than a guess.
 */
export type WslPathTranslation =
  | { readonly ok: true; readonly path: string }
  | { readonly ok: false; readonly reason: string }

/** WslPathQueryOptions is one translation's inputs. Everything the query needs is
 * handed in, exactly as `wslDistributions` takes it: the app's environment, a
 * host `cwd`, and -- for a test -- the program. */
export interface WslPathQueryOptions {
  readonly direction: WslPathDirection
  readonly path: string
  /** `distro` is the distribution whose `wslpath` answers. It is the
   * installation's own distribution and never a default: a stored installation's
   * distro is the only one tried (F7). */
  readonly distro: string
  readonly appEnv: Readonly<Record<string, string | undefined>>
  /** `cwd` is the **host** directory `wsl.exe` itself runs in. The translation
   * runs inside the distribution, but `wsl.exe` is a host process like any
   * other, and this module will not start one in whatever directory the app
   * happens to be in. */
  readonly cwd: string
  /** `project` is what the spawn log records for this child. */
  readonly project?: string | undefined
  /** `wslPath` replaces the program `wslExecutable` computes: how a test points
   * the console at the fake (T-A1). */
  readonly wslPath?: string | undefined
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timers?: Timers
  readonly timeoutMs?: number
}

/**
 * wslpath asks the distribution to translate one path, in one direction, with
 * **one** `wsl.exe -d <distro> -- wslpath -a -u|-w <path>` call, through the same
 * spawner and the same deadline as every other child this console starts (D4(a)).
 *
 * The answer is trimmed and checked rather than trusted: an empty answer, one
 * carrying a NUL or a newline, or one that is not an absolute path in the
 * direction asked for is a refusal, because every consumer of this value hands
 * it to a child as a directory or compares it with one, and a value that is none
 * of those things is not a path this console may pass on.
 */
export async function wslpath(options: WslPathQueryOptions): Promise<WslPathTranslation> {
  const direction = options.direction
  if (options.distro === '') {
    return { ok: false, reason: 'no distribution was named, so no path can be translated inside one' }
  }
  const program = options.wslPath ?? wslExecutable(options.appEnv)
  const answer = await runWslQuery({
    operation: 'wsl.path',
    program,
    // The one wrapper this module writes: the question is asked *inside* the
    // distribution, so the distribution is named and `--` ends `wsl.exe`'s own
    // words. `--cd` is deliberately absent: the translation is about a path, and
    // naming a directory to run it in would be a translation of a path the
    // console has not translated yet.
    prefix: ['-d', options.distro, '--'],
    argv: [
      WSLPATH_PROGRAM,
      WSLPATH_FLAG_ABSOLUTE,
      direction === 'toLinux' ? WSLPATH_FLAG_TO_LINUX : WSLPATH_FLAG_TO_WINDOWS,
      options.path,
    ],
    appEnv: options.appEnv,
    cwd: options.cwd,
    project: options.project ?? '',
    ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
    ...(options.log === undefined ? {} : { log: options.log }),
    ...(options.timers === undefined ? {} : { timers: options.timers }),
    ...(options.timeoutMs === undefined ? {} : { timeoutMs: options.timeoutMs }),
  })
  if (!answer.ok) return { ok: false, reason: answer.reason }
  const path = answer.stdout.trim()
  if (path === '' || path.includes('\u0000') || path.includes('\n')) {
    return {
      ok: false,
      reason:
        `the distribution answered \`wslpath\` with something that is not a path for ${options.path}: ` +
        'the console will not pass an answer it cannot read on (T-D2)',
    }
  }
  if (direction === 'toLinux' && !path.startsWith('/')) {
    return {
      ok: false,
      reason: `the distribution answered ${JSON.stringify(path)} for ${options.path}, which is not an absolute path inside it`,
    }
  }
  return { ok: true, path }
}

/**
 * WslPathCache is `wslpath` for one window's lifetime: the same path in the same
 * direction is asked **once** and the answer is kept, exactly as D4(a) decides
 * ("cached for the life of the window", one process per distinct path).
 *
 * The cache holds the promise rather than the answer, so two callers that ask
 * for the same path at the same moment share one `wsl.exe` rather than racing
 * to start two. A refusal is **not** kept: a path the distribution could not
 * translate this second is asked again next time, because the answer depends on
 * a running distribution and a mount that may have arrived.
 */
export interface WslPathCache {
  toLinux(path: string): Promise<WslPathTranslation>
  toWindows(path: string): Promise<WslPathTranslation>
  /** size is how many translations are held, so a test can show that the second
   * ask did not start a second process. */
  size(): number
}

/** createWslPathCache returns the per-window cache. Everything but the direction
 * and the path is decided once, here. */
export function createWslPathCache(options: Omit<WslPathQueryOptions, 'direction' | 'path'>): WslPathCache {
  const held = new Map<string, Promise<WslPathTranslation>>()
  const ask = (direction: WslPathDirection, path: string): Promise<WslPathTranslation> => {
    const key = `${direction}\u0000${path}`
    const known = held.get(key)
    if (known !== undefined) return known
    const pending = wslpath({ ...options, direction, path }).then((answer) => {
      // A refusal is forgotten as soon as it is handed out -- see the interface
      // -- so a distribution that comes back up is asked again rather than
      // answered from a sentence that is no longer true.
      if (!answer.ok) held.delete(key)
      return answer
    })
    held.set(key, pending)
    return pending
  }
  return {
    toLinux: (path) => ask('toLinux', path),
    toWindows: (path) => ask('toWindows', path),
    size: () => held.size,
  }
}

/**
 * parseDistributionList reads `wsl.exe --list --quiet`: one distribution name
 * per line, in the order `wsl.exe` printed them, which is the order the picker
 * offers and the order the operator's own `wsl -l` shows.
 *
 * `null` -- not an empty list -- is the answer for text this decoder cannot read
 * as names: a UTF-16LE answer arrives as NUL-interleaved bytes, and half a name
 * is worse than no name, because the console would then try to run a CLI in a
 * distribution whose name it invented. **The decoder proper is T-F2's**: it adds
 * the UTF-16LE vector and the BOM that goes with it, and this function's `null`
 * is the case it narrows.
 *
 * A UTF-8 BOM is stripped, `\r\n` and `\n` both end a line, blank lines are
 * dropped, and a line's surrounding whitespace is not part of the name.
 */
export function parseDistributionList(text: string): readonly string[] | null {
  if (text.includes('\u0000')) return null
  const lines = stripBom(text)
    .split('\n')
    .map((line) => line.replace(/\r$/, '').trim())
    .filter((line) => line !== '')
  return lines
}

/**
 * parseDefaultDistribution reads `wsl.exe --status` for the one line that names
 * the default, and answers `null` when there is none to read: no such line at
 * all, or a value that is one of `NO_DEFAULT_SPELLINGS`.
 *
 * A name that is not one of the listed distributions is **not** an error here:
 * this function answers what the line said, and `wslDistributions` marks a
 * distribution only when the name matches one it has. A `--status` that names a
 * distribution `--list` did not report is a machine whose two answers disagree,
 * and the console's response is "no default marked, ask the operator" rather
 * than a guess at which answer is stale.
 */
export function parseDefaultDistribution(text: string): string | null {
  for (const raw of stripBom(text).split('\n')) {
    const match = DEFAULT_LINE.exec(raw.replace(/\r$/, ''))
    if (match === null) continue
    const value = (match[1] ?? '').trim()
    if (NO_DEFAULT_SPELLINGS.includes(value)) return null
    return value
  }
  return null
}

/** stripBom removes a leading U+FEFF, which is what a UTF-8 BOM decodes to. */
function stripBom(text: string): string {
  return text.startsWith('\uFEFF') ? text.slice(1) : text
}

/** describe renders an unknown thrown value as a bounded one-line string. */
function describe(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}
