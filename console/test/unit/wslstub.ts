// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// A fake `wsl.exe`, for the tests that must observe what the Linux side
// received (docs/console-windows/implementation-plan.md Part A, decision D11).
//
// **The claim that matters is the second hop's, and only an out-of-band channel
// can state it.** On Windows the console runs `wsl.exe -d <distro> --cd <dir> -e
// sh -c '… exec fugaro …'`, so every question about the WSL route -- which distribution, which
// directory, which environment, which exit status -- is a question about what
// the *distribution* got rather than about what the console thinks it passed.
// `installWslStub` therefore installs a real executable named `wsl.exe` (the
// name is data, not a platform fact: on the POSIX test machine it is a file with
// a shebang, exactly as `stub.ts`'s `fugaro` is) that parses the console's argv,
// records its own view to the same out-of-band report file `installStub` uses,
// and then **execs the stub `fugaro`** with the remaining argv, in the directory
// the console named.
//
// So one report file carries two hops, in order:
//
//   1. the `wsl.exe` hop, marked `hop: 'wsl'` -- its own argv, its own `cwd`,
//      its own environment, the distribution it was given, the `--cd` it was
//      handed, and, for the two shapes that start no CLI, what it answered;
//   2. the `fugaro` hop, written by `stub.ts`'s generated child itself, with no
//      `hop` key -- what the Linux side received.
//
// `WslStub.reports()` reads the first, `WslStub.children()` the second, and a
// test that asserted on the console's own plan object instead would be asserting
// that the console is self-consistent, which is the failure `stub.ts`'s own
// comment says the report file exists to avoid. `WslStub.stub` is the stub
// `fugaro` **as the child hop**, so `stub.reports()` is `children()` and not the
// shared file: the two hops are read apart everywhere, including there.
//
// **What this double deliberately refuses to be, and what it must be.**
//
//   - **Not a shell.** The children it starts are the stub `fugaro`, the
//     `wslpath` query, and the `sh -c` wrapper the console puts around the CLI
//     (T-F4) -- and nothing else. A fake that runs anything makes every
//     assertion about *what was run* meaningless (T-A3).
//   - **It does not guess a path.** `--cd` is the *distribution's* own spelling,
//     because the console translates before it builds the plan and asks
//     `wslpath` for the translation (T-C5, T-D3, T-D4): a Windows spelling in
//     `--cd` is refused with a sentence, which is how a Part C or D bug becomes
//     loud here. `wslpath` itself is answered from **the test's own table**, in
//     both directions, and a path the table does not carry is a refusal rather
//     than a guess -- a mapping computed here would be right for a `C:` drive
//     path and wrong for every custom mount (T-D2, D4(b)).
//   - **It does not pass the host's `PATH` to a Linux child**: the child gets the
//     distribution's own, because `wsl.exe` builds a Linux child's environment
//     and a Windows `PATH` in it is the one thing that cannot work (T-A2, F5).
//   - **It can fail the way `wsl.exe` fails**, because that is a state the
//     console has to classify rather than a corner of the fixture: a query can
//     exit non-zero with a sentence, which is T-C4's "WSL is not installed".

import { chmodSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

import { CHILD_ENV_KEYS, FUGARO_BINARY_NAME } from '../../src/main/spawn'
import { installStub } from './stub'
import type { Stub, StubReport, StubScenario } from './stub'

/** WSL_BINARY_NAME is the name of the fake, spelled as the console spells it.
 * The name is data rather than a platform fact: the console spawns this string
 * on Windows and a test spawns it on POSIX, and both are the same double. */
export const WSL_BINARY_NAME = 'wsl.exe'

/** DISTRO_DIRECTORY is the subdirectory the stub `fugaro` lives in. The two
 * directories are the two sides of the boundary this work is about -- the host's
 * `wsl.exe` at the top, the distribution's `fugaro` under here -- and keeping
 * them apart is what makes a test's paths say which side they are on. */
export const DISTRO_DIRECTORY = 'distro'

/** DISTRO_PATH is the distribution's own `PATH`: what the fake gives the child
 * *instead of* its own. It is deliberately a plain Linux list and never the
 * test's `PATH`, because on the WSL leg the child is a Linux process and a
 * Windows `PATH` is not a thing it could use (F5, T-A2). The fake resolves the
 * bare name `fugaro` to the installed stub itself, which is its stand-in for the
 * distribution's own lookup. */
export const DISTRO_PATH = '/usr/local/bin:/usr/bin:/bin'

/** WSLPATH_PROGRAM is the one query that runs *inside* the distribution: the
 * console asks it to translate a path rather than computing the mapping itself
 * (T-D2, D4(b)). */
export const WSLPATH_PROGRAM = 'wslpath'

/** WRAPPER_PROGRAM is the shell the console wraps the CLI in, so that the pid
 * inside the distribution can be captured and killed (T-F4, D6(a)). The double
 * models the wrapper the plan writes and not a general shell: `sh -c <script>`
 * and nothing else. */
export const WRAPPER_PROGRAM = 'sh'

/** WslInvocationKind is what one invocation of the fake turned out to be.
 * `spawn` started the stub `fugaro` -- directly or through the wrapper;
 * `query` answered `--list --quiet` or `--status` from the fixture, or failed
 * the way the fixture says; `translate` answered a `wslpath` query from the
 * table; `refused` is an invocation the double will not carry out, and it is a
 * recorded outcome rather than a silent one because a test has to be able to see
 * what the console asked for. */
export type WslInvocationKind = 'spawn' | 'query' | 'translate' | 'refused'

/** WslPathDirection is which way a `wslpath` call translates: `-u` reads a
 * Windows spelling and answers the distribution's, `-w` the other way. */
export type WslPathDirection = 'toLinux' | 'toWindows'

/** WslPathPair is one path in both of its spellings, and it is **the test's own
 * record**: the double never derives one from the other, because a mapping
 * computed here would be right for a `C:` drive path and wrong for every custom
 * mount, every `--cd` outside `/mnt`, and every distribution with its own
 * `/etc/wsl.conf` (D4(b)). A path the table does not carry is refused. */
export interface WslPathPair {
  /** `windows` is the spelling Windows uses for the path -- `C:\Users\dimi\src`
   * or the UNC form `\\wsl.localhost\Ubuntu\home\dimi\src`. */
  readonly windows: string
  /** `linux` is the spelling the distribution uses for the same path. */
  readonly linux: string
}

/** WslPathQuery is a `wslpath` invocation's own record: what was asked, which
 * way, and what the table answered (`null` when it carried no such path, which
 * is the refusal the console must state rather than guess around). */
export interface WslPathQuery {
  readonly direction: WslPathDirection
  readonly path: string
  readonly answer: string | null
}

/** WslReport is one line the fake wrote: `StubReport`'s own view (its argv, its
 * cwd, its environment, its `PATH`) plus what only this double knows. */
export interface WslReport extends StubReport {
  /** `hop` is the marker that tells the two hops of one report file apart; the
   * stub `fugaro`'s own lines do not carry it. */
  readonly hop: 'wsl'
  readonly kind: WslInvocationKind
  /** `distro` is the distribution the console named with `-d`. */
  readonly distro: string | null
  /** `cd` is the `--cd` exactly as the console sent it, which is the
   * **distribution's** own spelling of the directory (T-C5, T-D4): the console
   * translates before it builds the plan, and a Windows spelling here is refused
   * rather than recorded. */
  readonly cd: string | null
  /** `wrapper` is the `sh -c` script the console wrapped the CLI in, when it did
   * (T-F4); `null` when the child was the CLI itself. */
  readonly wrapper: string | null
  /** `wslpath` is the translation this invocation carried, when it was one. */
  readonly wslpath: WslPathQuery | null
  /** `childEnv` is the environment the child *inside the distribution* will
   * receive: the eleven `CHILD_ENV_KEYS` and no other key, with `PATH` the
   * distribution's own. */
  readonly childEnv: Readonly<Record<string, string>>
}

/** WslFixture is what the two queries answer. Both strings are the fixture's
 * own, verbatim: the bytes `wsl.exe` prints are what the console's decoder has
 * to survive (F27, U9), so the test writes the vector it wants decoded. */
export interface WslFixture {
  /** `list` is exactly what `wsl.exe --list --quiet` prints. */
  readonly list: string
  /** `status` is exactly what `wsl.exe --status` prints. */
  readonly status: string
  /** `encoding` is how the two are written -- `utf8` by default, `utf16le` for
   * the vector that models the Windows builds answering in UTF-16 (U9). The
   * text itself carries any BOM and any `\r\n`, so the vector is the test's. */
  readonly encoding?: 'utf8' | 'utf16le'
  /** `queryFailure` is a `wsl.exe` that cannot answer at all -- WSL not
   * installed, or the feature off -- which is a state the console classifies
   * rather than an empty answer (T-C4's `absent` outcome). Both queries then
   * exit with this status and print this sentence on stderr, and the report
   * still records the argv: what the console asked for is a fact either way. */
  readonly queryFailure?: { readonly status: number; readonly stderr: string }
}

/** WslStubOptions is what a test decides before the double exists. */
export interface WslStubOptions {
  /** `scenario` is the stub `fugaro`'s own scenario: what the Linux side
   * answers when the fake starts it. */
  readonly scenario?: StubScenario
  /** `distros` is what `wsl.exe --list --quiet` and `--status` answer. */
  readonly distros: WslFixture
  /** `paths` is the `wslpath` table: the paths the fixture can translate, each
   * in both of its spellings. It is **the test's own record** and never a
   * computed mapping (D4(b)); a path it does not carry is refused. */
  readonly paths?: readonly WslPathPair[]
  /** `distroPath` replaces the distribution's `PATH`, for a test that wants its
   * own. */
  readonly distroPath?: string
  /** `lingersAfterChild` keeps the fake `wsl.exe` **alive after the child
   * inside the distribution has exited**, holding its own stdout open. It is
   * T-F5's case (F25): a pipe that outlives the process that wrote to it, which
   * is why `runToCompletion` settles on `close` and keeps the read deadline as
   * the backstop. Two more ways for that to happen arrive with the wrapper --
   * the WSL relay and the distribution's session -- so the backstop is
   * load-bearing on this route rather than theoretical. */
  readonly lingersAfterChild?: boolean
  /** `holdsOnTerm` makes the fake `wsl.exe` **ignore `SIGTERM`**, which is the
   * state T-F4 is built for: killing the Windows-side `wsl.exe` is not
   * documented to kill the Linux process it started (F24, U5), and the console
   * may not even get as far as `wsl.exe` dying. A fake that always died on
   * `SIGTERM` would let a console that only ever killed `wsl.exe` pass every
   * test, which is exactly the bug. */
  readonly holdsOnTerm?: boolean
  /** `preamble` is what the fake `wsl.exe` prints on **stderr before it runs
   * the distribution's command**, which is what the real one does: its relay
   * notices and warnings arrive on the same pipe, before the wrapper's own
   * line. A reader that looked for the wrapper's line only at the start of
   * stderr would fail here, so this option is how T-F1's "a shell did start"
   * and T-F4's pid are asserted against the real ordering rather than the
   * convenient one. */
  readonly preamble?: string
}

/** WslStub is an installed fake `wsl.exe` and the stub `fugaro` behind it. */
export interface WslStub {
  /** `binary` is the absolute path of the executable the console will spawn. */
  readonly binary: string
  /** `directory` is the temporary directory it lives in: the host's side. */
  readonly directory: string
  /** `reportPath` is the file every hop appends to -- the stub `fugaro`'s own,
   * so both hops of one invocation land in one place, in order. */
  readonly reportPath: string
  /** `stub` is the fake CLI inside the distribution, **read as the child hop**:
   * its `reports()` and `calls()` are narrowed to the lines the stub `fugaro`
   * itself wrote, so the `wsl.exe` hop is not mixed into them. It is the handle
   * a test needs for a scenario, a kill or the stub's own binary. */
  readonly stub: Stub
  /** reports reads the `wsl.exe` hop back: one entry per invocation, in order,
   * whether it started a child, answered a query, translated a path or refused. */
  reports(): readonly WslReport[]
  /** children reads the `fugaro` hop back: what the Linux side received. It is
   * the account the whole arrangement exists for. */
  children(): readonly StubReport[]
  /** calls is how many times `wsl.exe` itself was run. */
  calls(): number
  /** childCalls is how many times the Linux side was run: one fewer than `calls`
   * for every query, translation and refusal. */
  childCalls(): number
}

/**
 * installWslStub writes the fake `wsl.exe` into `directory` and the stub
 * `fugaro` into `directory/distro`, and returns both.
 */
export function installWslStub(directory: string, options: WslStubOptions): WslStub {
  mkdirSync(directory, { recursive: true })
  const binary = join(directory, WSL_BINARY_NAME)
  const scriptPath = join(directory, 'wsl-stub.mjs')
  const stub = installStub(join(directory, DISTRO_DIRECTORY), options.scenario ?? {})
  writeFileSync(
    scriptPath,
    wslScript({
      reportPath: stub.reportPath,
      childBinary: stub.binary,
      childEnvKeys: CHILD_ENV_KEYS,
      distroPath: options.distroPath ?? DISTRO_PATH,
      distroDirectory: stub.directory,
      translations: options.paths ?? [],
      list: options.distros.list,
      status: options.distros.status,
      encoding: options.distros.encoding ?? 'utf8',
      queryFailure: options.distros.queryFailure ?? null,
      holdsOnTerm: options.holdsOnTerm ?? false,
      lingersAfterChild: options.lingersAfterChild ?? false,
      preamble: options.preamble ?? null,
    }),
  )
  // The shim is `stub.ts`'s own arrangement, for its reasons: Node is named by
  // absolute path, because `#!/usr/bin/env node` would resolve `node` through
  // the child's `PATH` -- which is one of the things under test -- and the file
  // is the generated script itself with Node as its interpreter rather than a
  // `/bin/sh` wrapper, because a shell adds `PWD` and `SHLVL` to the environment
  // it execs into, and the environment is also under test. A Node path with
  // whitespace cannot be a shebang, so that alone falls back to the shell.
  if (/\s/.test(process.execPath)) {
    writeFileSync(binary, `#!/bin/sh\nexec ${shellQuote(process.execPath)} ${shellQuote(scriptPath)} "$@"\n`)
  } else {
    writeFileSync(binary, `#!${process.execPath}\nimport(${JSON.stringify(pathToFileURL(scriptPath).href)})\n`)
  }
  chmodSync(binary, 0o755)

  const read = (): readonly StubReport[] => {
    if (!existsSync(stub.reportPath)) return []
    return readFileSync(stub.reportPath, 'utf8')
      .split('\n')
      .filter((line) => line.trim() !== '')
      .map((line) => JSON.parse(line) as StubReport)
  }
  const childHop = (): readonly StubReport[] => read().filter((line) => !isWslReport(line))

  return {
    binary,
    directory,
    reportPath: stub.reportPath,
    // The stub `fugaro` is handed back with its report narrowed to its own hop.
    // Its `reportPath` is still the shared file -- that is where its child
    // writes -- but `reports()` and `calls()` must not answer with the
    // `wsl.exe` hop's lines, or the two-hop separation this file is built
    // around would hold everywhere except at the one handle named after the
    // child.
    stub: {
      ...stub,
      reports: childHop,
      calls: (): number => childHop().length,
    },
    reports(): readonly WslReport[] {
      return read().filter(isWslReport)
    },
    children: childHop,
    calls(): number {
      return read().filter(isWslReport).length
    },
    childCalls(): number {
      return childHop().length
    },
  }
}

/** isWslReport tells the two hops of one report file apart. */
function isWslReport(line: StubReport): line is WslReport {
  return (line as { hop?: unknown }).hop === 'wsl'
}

function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

/**
 * parseWslArgv reads the fake's own argv: the distribution, the `--cd`, and the
 * child's argv after the `-e` (`--exec`). It answers a refusal sentence instead of a
 * partial parse, so an invocation the double does not model is loud rather than
 * quietly reinterpreted -- `-d` is required because the console always names the
 * distribution it resolved (D2(a)), and a shape it does not know is refused
 * rather than skipped. `--` is one of those: on a real `wsl.exe` it hands the
 * rest of the line to the distribution's **login shell**, which would re-read
 * every word, so a console that sent it is refused here by name (T-F4).
 *
 * It is exported, and its own source is what the generated child runs (see
 * `wslScript`), so the parser a test can hold and the parser the double runs are
 * one implementation rather than two that can drift.
 */
export function parseWslArgv(argv: readonly string[]):
  | {
      readonly ok: true
      readonly distro: string
      readonly cd: string | null
      readonly child: readonly string[]
    }
  | { readonly ok: false; readonly reason: string } {
  let distro: string | null = null
  let cd: string | null = null
  let index = 0
  while (index < argv.length && argv[index] !== '-e' && argv[index] !== '--exec') {
    const flag = argv[index]
    if (flag === '--') {
      return {
        ok: false,
        reason: '`--` hands the command to the distribution’s login shell: the console runs its program with `-e`',
      }
    }
    if (flag === '-d' || flag === '--distribution') {
      const value = argv[index + 1]
      if (value === undefined || value === '') {
        return { ok: false, reason: `\`${flag}\` names no distribution` }
      }
      distro = value
      index += 2
      continue
    }
    if (flag === '--cd') {
      const value = argv[index + 1]
      if (value === undefined || value === '') {
        return { ok: false, reason: '`--cd` names no directory' }
      }
      cd = value
      index += 2
      continue
    }
    return {
      ok: false,
      reason: `this double knows \`-d\`, \`--cd\` and \`-e\`, and was asked for ${JSON.stringify(flag)}`,
    }
  }
  if (index >= argv.length) {
    return { ok: false, reason: 'there is no `-e`: the double starts a child only after it' }
  }
  if (distro === null) {
    return { ok: false, reason: 'no `-d`: the console always names the distribution it resolved' }
  }
  const child = argv.slice(index + 1)
  if (child.length === 0) {
    return { ok: false, reason: 'nothing follows the `-e`: there is no child to start' }
  }
  return { ok: true, distro, cd, child }
}

/**
 * isWindowsSpelling says whether a path is spelled the way Windows spells one --
 * a drive-qualified `C:\…` or the UNC form `\\wsl.localhost\…` -- and it exists
 * for `--cd`, which the console sends in the **distribution's** spelling (T-C5,
 * T-D4). A Linux path never contains a backslash, so the test is exact rather
 * than heuristic.
 *
 * It is exported and injected into the generated child, like the other parsers,
 * so the rule a test can hold and the rule the double applies are one.
 */
export function isWindowsSpelling(path: string): boolean {
  return path.includes('\\') || /^[A-Za-z]:/.test(path)
}

/**
 * parseWslpathArgv reads a `wslpath` invocation's own argv: which direction, and
 * which path. `-a` is accepted and ignored -- it asks for an absolute answer,
 * and the table's answer is the test's verbatim string -- while a second
 * direction flag, an unknown flag, no flag and no path are each refused with a
 * sentence, because a translation the double cannot tell the direction of is a
 * translation it must not answer.
 */
export function parseWslpathArgv(argv: readonly string[]):
  | { readonly ok: true; readonly direction: WslPathDirection; readonly path: string }
  | { readonly ok: false; readonly reason: string } {
  let direction: WslPathDirection | null = null
  let path: string | null = null
  for (const word of argv) {
    if (word === '-a') continue
    if (word === '-u' || word === '-w') {
      if (direction !== null) {
        return { ok: false, reason: 'more than one of `-u` and `-w`: the double cannot tell which direction was asked for' }
      }
      direction = word === '-u' ? 'toLinux' : 'toWindows'
      continue
    }
    if (word.startsWith('-')) {
      return { ok: false, reason: `this double knows \`wslpath -a -u|-w <path>\`, and was asked for ${JSON.stringify(word)}` }
    }
    if (path !== null) {
      return { ok: false, reason: `\`wslpath\` takes one path, and was given ${JSON.stringify(path)} and ${JSON.stringify(word)}` }
    }
    path = word
  }
  if (direction === null) {
    return { ok: false, reason: 'no `-u` or `-w`: the double cannot tell which direction was asked for' }
  }
  if (path === null) {
    return { ok: false, reason: '`wslpath` was given no path to translate' }
  }
  return { ok: true, direction, path }
}

/**
 * translatePath answers a `wslpath` query from the test's own table, in both
 * directions, and answers `null` -- a refusal, not a guess -- for a path the
 * table does not carry. The `null` is what lets a test prove the console stated
 * the miss instead of inventing a mapping (T-D2).
 */
export function translatePath(
  direction: WslPathDirection,
  path: string,
  pairs: readonly WslPathPair[],
): string | null {
  for (const pair of pairs) {
    if (direction === 'toLinux' && pair.windows === path) return pair.linux
    if (direction === 'toWindows' && pair.linux === path) return pair.windows
  }
  return null
}

/**
 * childEnvironmentFor builds the environment the child inside the distribution
 * receives: the eleven `CHILD_ENV_KEYS` and **no other key**, with `PATH` the
 * distribution's own. The filter is the double's half of the boundary
 * `spawn.ts`'s `childEnvironment` enforces on the console's side: whatever else
 * the host's environment holds -- a Windows `PATH`, a sentinel a test planted --
 * does not cross into the distribution.
 *
 * It mirrors `childEnvironment`'s own rule that an empty value is not a value:
 * only keys the console actually set are passed on, plus `PATH`, which is always
 * the distribution's.
 */
export function childEnvironmentFor(
  env: Readonly<Record<string, string | undefined>>,
  keys: readonly string[],
  distroPath: string,
): Record<string, string> {
  const child: Record<string, string> = {}
  for (const key of keys) {
    if (key === 'PATH') continue
    const value = env[key]
    if (typeof value === 'string' && value !== '') child[key] = value
  }
  child['PATH'] = distroPath
  return child
}

/**
 * wslScript is the generated double. It is written as a string rather than kept
 * as a `.mjs` file in the repository because the paths, the fixture and the
 * translation table it needs are absolute and per-test, and a template is the
 * only way to give it all of them without an out-of-band channel. The five
 * functions above are injected by their own source, as `stub.ts` injects
 * `commandKey`, so the double cannot carry a second copy of the rules.
 */
function wslScript(options: {
  readonly reportPath: string
  readonly childBinary: string
  readonly childEnvKeys: readonly string[]
  readonly distroPath: string
  readonly distroDirectory: string
  readonly translations: readonly WslPathPair[]
  readonly list: string
  readonly status: string
  readonly encoding: 'utf8' | 'utf16le'
  readonly queryFailure: { readonly status: number; readonly stderr: string } | null
  readonly holdsOnTerm: boolean
  readonly lingersAfterChild: boolean
  readonly preamble: string | null
}): string {
  return `// Generated by console/test/unit/wslstub.ts. Do not edit.
import { appendFileSync } from 'node:fs'
import { spawn } from 'node:child_process'

const REPORT = ${JSON.stringify(options.reportPath)}
const CHILD = ${JSON.stringify(options.childBinary)}
const CHILD_ENV_KEYS = ${JSON.stringify(options.childEnvKeys)}
const DISTRO_PATH = ${JSON.stringify(options.distroPath)}
const DISTRO_DIRECTORY = ${JSON.stringify(options.distroDirectory)}
const TRANSLATIONS = ${JSON.stringify(options.translations)}
const LIST = ${JSON.stringify(options.list)}
const STATUS = ${JSON.stringify(options.status)}
const ENCODING = ${JSON.stringify(options.encoding)}
const HOLDS_ON_TERM = ${JSON.stringify(options.holdsOnTerm)}
const LINGERS_AFTER_CHILD = ${JSON.stringify(options.lingersAfterChild)}
const PREAMBLE = ${JSON.stringify(options.preamble)}
// A Windows-side \`wsl.exe\` that will not go: the console's \`SIGTERM\` does not
// end it, so the Linux process behind it outlives every Windows-side signal
// (F24, U5). Installing a listener is what replaces Node's default behaviour of
// terminating on \`SIGTERM\`.
if (HOLDS_ON_TERM) process.on('SIGTERM', () => {})
const QUERY_FAILURE = ${JSON.stringify(options.queryFailure)}

// The wrapper's \`PATH\`: the distribution's own, with the directory the double
// installed the stub \`fugaro\` in front of it. A real distribution has the CLI on
// its \`PATH\` -- \`/usr/local/bin\`, which is the first entry of \`DISTRO_PATH\` --
// and the double's stand-in for that directory is the one it wrote the stub
// into. Without it, \`exec fugaro\` inside the wrapper (T-F4) would not resolve,
// and the wrapper is the shape whose whole point is that the CLI is reached
// through it. The direct spawn needs none of this: it names the stub itself.
const WRAPPER_PATH = DISTRO_DIRECTORY + ':' + DISTRO_PATH

const argv = process.argv.slice(2)

${parseWslArgv.toString()}
${isWindowsSpelling.toString()}
${parseWslpathArgv.toString()}
${translatePath.toString()}
${childEnvironmentFor.toString()}

// Every invocation is recorded, including the ones that are refused: the report
// is the only channel a test has for "what did the console actually ask for",
// and a refusal that left no trace would be a test that cannot say what was
// refused. \`childEnv\` is the environment the child *will* receive, so a test can
// read the boundary's own state before the child runs.
function record(kind, fields) {
  appendFileSync(
    REPORT,
    JSON.stringify({
      hop: 'wsl',
      kind,
      argv,
      cwd: process.cwd(),
      env: { ...process.env },
      envKeys: Object.keys(process.env).sort(),
      path: process.env.PATH ?? '',
      project: process.env.FUGARO_PROJECT ?? null,
      uid: typeof process.getuid === 'function' ? process.getuid() : null,
      distro: fields.distro ?? null,
      cd: fields.cd ?? null,
      wrapper: fields.wrapper ?? null,
      wslpath: fields.wslpath ?? null,
      childEnv: fields.childEnv ?? {},
    }) + '\\n',
  )
}

// A refusal is an exit status and a sentence on stderr, never a silent no-op:
// the console reads the status (T-F1), and a test reads the sentence.
function refuse(reason) {
  record('refused', {})
  fail(reason)
}

// \`fail\` is the exit without the record, for the two cases whose record is
// written first and carries the answer: a query the fixture fails, and a
// translation the table does not carry.
function fail(reason) {
  process.stderr.write('wsl.exe (the fake): ' + reason + '\\n')
  process.exit(1)
}

// The two queries the resolver and the picker ask, answered from the fixture in
// the encoding the test chose -- or failed, when the fixture models a \`wsl.exe\`
// that cannot answer (T-C4's absent outcome). They start no child, so the report
// line they leave behind is the only record of the arguments the console sent.
const first = argv[0]
if (first === '--list' || first === '--status') {
  const wanted = first === '--list' ? ['--list', '--quiet'] : ['--status']
  if (argv.length !== wanted.length || wanted.some((word, i) => argv[i] !== word)) {
    refuse('this double answers \`' + wanted.join(' ') + '\` and nothing else')
  }
  record('query', {})
  if (QUERY_FAILURE !== null) {
    process.stderr.write(QUERY_FAILURE.stderr)
    process.exit(QUERY_FAILURE.status)
  }
  process.stdout.write(Buffer.from(first === '--list' ? LIST : STATUS, ENCODING))
  process.exit(0)
}

const parsed = parseWslArgv(argv)
if (!parsed.ok) refuse(parsed.reason)

// \`--cd\` is the distribution's own spelling of the directory, because the
// console translates before it builds the plan (T-D2, T-D3, T-D4). The Windows
// spelling is refused rather than accepted: a double that took it would hide the
// very bug T-D3 is about, and the child would be started in a directory that
// does not exist.
if (parsed.cd !== null && isWindowsSpelling(parsed.cd)) {
  refuse('\`--cd\` is \`' + parsed.cd + '\`, a Windows spelling: the console sends the distribution\\'s own spelling (T-C5, T-D4) and asks \`wslpath\` for it')
}

// The double is not a shell (T-A3). The children it starts are the stub
// \`fugaro\` -- named the way the console names it (the bare name, resolved by the
// distribution's own PATH, which the double stands in for) or by the absolute
// path the double installed -- the \`wslpath\` query, and the \`sh -c\` wrapper the
// console puts around the CLI (T-F4). Anything else is refused rather than run:
// a fake that runs anything makes every assertion about what was run
// meaningless.
const program = parsed.child[0]
const isWrapper = program === ${JSON.stringify(WRAPPER_PROGRAM)}
if (program !== ${JSON.stringify(FUGARO_BINARY_NAME)} && program !== CHILD && program !== ${JSON.stringify(WSLPATH_PROGRAM)} && !isWrapper) {
  refuse('this double runs the stub \`${FUGARO_BINARY_NAME}\`, \`${WSLPATH_PROGRAM}\` and a \`${WRAPPER_PROGRAM} -c\` wrapper, and was asked for ' + JSON.stringify(program))
}
if (isWrapper && (parsed.child.length !== 3 || parsed.child[1] !== '-c')) {
  refuse('this double runs a \`${WRAPPER_PROGRAM} -c <script>\` wrapper and nothing else, and was asked for ' + JSON.stringify(parsed.child.join(' ')))
}

// \`wslpath\`, answered from the test's own table in both directions. A path the
// table does not carry is a refusal with the path in it, never a guess: the
// console is required to state the miss (T-D2).
if (program === ${JSON.stringify(WSLPATH_PROGRAM)}) {
  const wanted = parseWslpathArgv(parsed.child.slice(1))
  if (!wanted.ok) refuse(wanted.reason)
  const answer = translatePath(wanted.direction, wanted.path, TRANSLATIONS)
  record('translate', {
    distro: parsed.distro,
    cd: parsed.cd,
    wslpath: { direction: wanted.direction, path: wanted.path, answer },
  })
  if (answer === null) {
    fail('the table this double was given carries no ' + (wanted.direction === 'toLinux' ? 'Windows' : 'distribution') + ' spelling of \`' + wanted.path + '\`: it is the test\\'s own record, and a mapping computed here would be wrong for every custom mount')
  }
  process.stdout.write(answer + '\\n')
  process.exit(0)
}

const env = childEnvironmentFor(process.env, CHILD_ENV_KEYS, isWrapper ? WRAPPER_PATH : DISTRO_PATH)
// The child's working directory is the one the console named -- and it is the
// distribution's spelling, so it is the answer as it stands. \`process.cwd()\` is
// the fake's own directory, and it is the wrong answer for a child inside a
// distribution, so it is only the fallback for an invocation that named none.
const cwd = parsed.cd ?? process.cwd()
record('spawn', {
  distro: parsed.distro,
  cd: parsed.cd,
  wrapper: isWrapper ? parsed.child[2] : null,
  childEnv: env,
})

// \`wsl.exe\`'s own stderr, before the distribution's command runs: its relay
// notices and warnings arrive on the same pipe as the wrapper's line, and
// earlier (T-F1). A reader that looked only at the first bytes of stderr would
// fail on this and only on this.
if (PREAMBLE !== null) process.stderr.write(PREAMBLE)

// The wrapper is run by the distribution's own shell, at \`/bin/sh\` -- the path
// \`sh\` resolves to on the \`PATH\` above -- so the shell prints its own pid in
// the line it writes before it \`exec\`s the CLI, which is the pid the console
// has to kill (T-F4, D6(a)).
const child = isWrapper
  ? spawn('/bin/sh', ['-c', parsed.child[2]], { cwd, env, stdio: 'inherit' })
  : spawn(CHILD, parsed.child.slice(1), { cwd, env, stdio: 'inherit' })
child.on('error', (error) => {
  process.stderr.write('wsl.exe (the fake): could not start the child in ' + cwd + ': ' + error.message + '\\n')
  process.exit(1)
})
// The child's status is the fake's status: the wrapper is transparent to an exit
// code, which is what T-F1 asserts, and a signal is mirrored so a killed child
// is a killed \`wsl.exe\` rather than a successful one.
child.on('exit', (code, signal) => {
  // A \`wsl.exe\` that outlives the process it started, with its stdout still
  // open: the console's reader sees no \`close\`, and only its deadline can
  // settle the read (T-F5, F25).
  // A held handle as well as the early return: Node exits when the event loop
  // is empty, and a fake with no child left and no handle would leave the loop
  // and close its stdout -- which is the opposite of what this case models.
  if (LINGERS_AFTER_CHILD) {
    setInterval(() => {}, 60_000)
    return
  }
  if (signal !== null) process.kill(process.pid, signal)
  process.exit(code === null ? 1 : code)
})
`
}
