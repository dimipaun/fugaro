// A fake `wsl.exe`, for the tests that must observe what the Linux side
// received (docs/console-windows/implementation-plan.md Part A, decision D11).
//
// **The claim that matters is the second hop's, and only an out-of-band channel
// can state it.** On Windows the console runs `wsl.exe -d <distro> --cd <dir> --
// fugaro …`, so every question about the WSL route -- which distribution, which
// directory, which environment, which exit status -- is a question about what
// the *distribution* got rather than about what the console thinks it passed.
// `installWslStub` therefore installs a real executable named `wsl.exe` (the
// name is data, not a platform fact: on the POSIX test machine it is a file with
// a shebang, exactly as `stub.ts`'s `fugaro` is) that parses the console's argv,
// records its own view to the same out-of-band report file `installStub` uses,
// and then **execs the stub `fugaro`** with the remaining argv and the
// translated working directory.
//
// So one report file carries two hops, in order:
//
//   1. the `wsl.exe` hop, marked `hop: 'wsl'` -- its own argv, its own `cwd`,
//      its own environment, the distribution it was given, and what it did with
//      `--cd`;
//   2. the `fugaro` hop, written by `stub.ts`'s generated child itself, with no
//      `hop` key -- what the Linux side received.
//
// `WslStub.reports()` reads the first, `WslStub.children()` the second, and a
// test that asserted on the console's own plan object instead would be asserting
// that the console is self-consistent, which is the failure `stub.ts`'s own
// comment says the report file exists to avoid.
//
// **Three things this double deliberately refuses to be.** It is not a shell:
// the child it starts is the stub `fugaro` and nothing else (T-A3), because a
// fake that runs anything makes every assertion about *what was run* meaningless.
// It does not guess a path: `--cd` is translated through the table the test
// recorded, and a path the table does not carry is reported as untranslated
// rather than passed off as translated (T-A1 step 3). And it does not pass the
// host's `PATH` to a Linux child: the child gets the distribution's own, because
// `wsl.exe` builds a Linux child's environment and a Windows `PATH` in it is the
// one thing that cannot work (T-A2, F5).

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

/** WslInvocationKind is what one invocation of the fake turned out to be.
 * `spawn` started the stub `fugaro`; `query` answered `--list --quiet` or
 * `--status` from the fixture; `refused` is an invocation the double will not
 * carry out, and it is a recorded outcome rather than a silent one because a
 * test has to be able to see what the console asked for. */
export type WslInvocationKind = 'spawn' | 'query' | 'refused'

/** WslReport is one line the fake wrote: `StubReport`'s own view (its argv, its
 * cwd, its environment, its `PATH`) plus what only this double knows. */
export interface WslReport extends StubReport {
  /** `hop` is the marker that tells the two hops of one report file apart; the
   * stub `fugaro`'s own lines do not carry it. */
  readonly hop: 'wsl'
  readonly kind: WslInvocationKind
  /** `distro` is the distribution the console named with `-d`. */
  readonly distro: string | null
  /** `cd` is the `--cd` exactly as the console sent it. */
  readonly cd: string | null
  /** `linuxCwd` is the Linux spelling the test's table carries for `cd`, or
   * `null` when the table does not carry it. */
  readonly linuxCwd: string | null
  /** `untranslated` is the `cd` the table did not carry, so a test can tell "the
   * console asked for a translation and got one" from "the console happened to
   * pass a path that needed none". */
  readonly untranslated: string | null
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
}

/** WslStubOptions is what a test decides before the double exists. */
export interface WslStubOptions {
  /** `scenario` is the stub `fugaro`'s own scenario: what the Linux side
   * answers when the fake starts it. */
  readonly scenario?: StubScenario
  /** `distros` is what `wsl.exe --list --quiet` and `--status` answer. */
  readonly distros: WslFixture
  /** `paths` is the translation table, keyed by the Windows spelling the console
   * sends. It is **the test's own record** and never a computed mapping: a
   * mapping computed here would be right for `C:\Users\X` and wrong for every
   * custom mount, and the console is required to ask `wslpath` instead
   * (D4(b)). A `--cd` the table does not carry is reported as untranslated. */
  readonly paths?: Readonly<Record<string, string>>
  /** `distroPath` replaces the distribution's `PATH`, for a test that wants its
   * own. */
  readonly distroPath?: string
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
  /** `stub` is the fake CLI inside the distribution, for a test that needs its
   * own scenario, its report or its kills file. */
  readonly stub: Stub
  /** reports reads the `wsl.exe` hop back: one entry per invocation, in order,
   * whether it started a child, answered a query or refused. */
  reports(): readonly WslReport[]
  /** children reads the `fugaro` hop back: what the Linux side received. It is
   * the account the whole arrangement exists for. */
  children(): readonly StubReport[]
  /** calls is how many times `wsl.exe` itself was run. */
  calls(): number
  /** childCalls is how many times the Linux side was run: one fewer than `calls`
   * for every query, and one fewer than `calls` for every refusal. */
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
      translations: options.paths ?? {},
      list: options.distros.list,
      status: options.distros.status,
      encoding: options.distros.encoding ?? 'utf8',
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

  return {
    binary,
    directory,
    reportPath: stub.reportPath,
    stub,
    reports(): readonly WslReport[] {
      return read().filter(isWslReport)
    },
    children(): readonly StubReport[] {
      return read().filter((line) => !isWslReport(line))
    },
    calls(): number {
      return read().filter(isWslReport).length
    },
    childCalls(): number {
      return read().filter((line) => !isWslReport(line)).length
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
 * child's argv after the `--`. It answers a refusal sentence instead of a
 * partial parse, so an invocation the double does not model is loud rather than
 * quietly reinterpreted -- `-d` is required because the console always names the
 * distribution it resolved (D2(a)), and a shape it does not know is refused
 * rather than skipped.
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
  while (index < argv.length && argv[index] !== '--') {
    const flag = argv[index]
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
      reason: `this double knows \`-d\`, \`--cd\` and \`--\`, and was asked for ${JSON.stringify(flag)}`,
    }
  }
  if (index >= argv.length) {
    return { ok: false, reason: 'there is no `--`: the double starts a child only after the separator' }
  }
  if (distro === null) {
    return { ok: false, reason: 'no `-d`: the console always names the distribution it resolved' }
  }
  const child = argv.slice(index + 1)
  if (child.length === 0) {
    return { ok: false, reason: 'nothing follows the `--`: there is no child to start' }
  }
  return { ok: true, distro, cd, child }
}

/**
 * translateCd looks the `--cd` up in the test's own table. A path the table does
 * not carry is **reported as untranslated rather than guessed at**: that is what
 * lets a test prove the console asked for a translation, instead of the console
 * happening to pass a path that needed none. The child is still started in
 * whatever it was given, so an untranslated path is a fact in the report and not
 * a silent substitution.
 */
export function translateCd(
  cd: string | null,
  table: Readonly<Record<string, string>>,
): { readonly linuxCwd: string | null; readonly untranslated: string | null } {
  if (cd === null) return { linuxCwd: null, untranslated: null }
  const mapped = table[cd]
  if (typeof mapped === 'string' && mapped !== '') return { linuxCwd: mapped, untranslated: null }
  return { linuxCwd: null, untranslated: cd }
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
 * only way to give it all of them without an out-of-band channel. The three
 * functions above are injected by their own source, as `stub.ts` injects
 * `commandKey`, so the double cannot carry a second copy of the rules.
 */
function wslScript(options: {
  readonly reportPath: string
  readonly childBinary: string
  readonly childEnvKeys: readonly string[]
  readonly distroPath: string
  readonly translations: Readonly<Record<string, string>>
  readonly list: string
  readonly status: string
  readonly encoding: 'utf8' | 'utf16le'
}): string {
  return `// Generated by console/test/unit/wslstub.ts. Do not edit.
import { appendFileSync } from 'node:fs'
import { spawn } from 'node:child_process'

const REPORT = ${JSON.stringify(options.reportPath)}
const CHILD = ${JSON.stringify(options.childBinary)}
const CHILD_ENV_KEYS = ${JSON.stringify(options.childEnvKeys)}
const DISTRO_PATH = ${JSON.stringify(options.distroPath)}
const TRANSLATIONS = ${JSON.stringify(options.translations)}
const LIST = ${JSON.stringify(options.list)}
const STATUS = ${JSON.stringify(options.status)}
const ENCODING = ${JSON.stringify(options.encoding)}

const argv = process.argv.slice(2)

${parseWslArgv.toString()}
${translateCd.toString()}
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
      linuxCwd: fields.linuxCwd ?? null,
      untranslated: fields.untranslated ?? null,
      childEnv: fields.childEnv ?? {},
    }) + '\\n',
  )
}

// A refusal is an exit status and a sentence on stderr, never a silent no-op:
// the console reads the status (T-F1), and a test reads the sentence.
function refuse(reason) {
  record('refused', {})
  process.stderr.write('wsl.exe (the fake): ' + reason + '\\n')
  process.exit(1)
}

// The two queries the resolver and the picker ask, answered from the fixture in
// the encoding the test chose. They start no child, so the report line they
// leave behind is the only record of the arguments the console sent.
const first = argv[0]
if (first === '--list' || first === '--status') {
  const wanted = first === '--list' ? ['--list', '--quiet'] : ['--status']
  if (argv.length !== wanted.length || wanted.some((word, i) => argv[i] !== word)) {
    refuse('this double answers \`' + wanted.join(' ') + '\` and nothing else')
  }
  record('query', {})
  process.stdout.write(Buffer.from(first === '--list' ? LIST : STATUS, ENCODING))
  process.exit(0)
}

const parsed = parseWslArgv(argv)
if (!parsed.ok) refuse(parsed.reason)

// The double is not a shell (T-A3). The child is the stub \`fugaro\` and nothing
// else -- named the way the console names it (the bare name, resolved by the
// distribution's own PATH, which the double stands in for) or by the absolute
// path the double installed. Anything else is refused rather than run: a fake
// that runs anything makes every assertion about what was run meaningless.
const program = parsed.child[0]
if (program !== ${JSON.stringify(FUGARO_BINARY_NAME)} && program !== CHILD) {
  refuse('this double runs the stub \`${FUGARO_BINARY_NAME}\` and nothing else, and was asked for ' + JSON.stringify(program))
}

const translated = translateCd(parsed.cd, TRANSLATIONS)
const env = childEnvironmentFor(process.env, CHILD_ENV_KEYS, DISTRO_PATH)
record('spawn', {
  distro: parsed.distro,
  cd: parsed.cd,
  linuxCwd: translated.linuxCwd,
  untranslated: translated.untranslated,
  childEnv: env,
})

// The child's working directory is the translated one -- and, when the table did
// not carry the path, the path as it was given, which is a fact the report above
// already states. \`process.cwd()\` is the fake's own directory, and it is the
// wrong answer for a child inside a distribution.
const cwd = translated.linuxCwd ?? parsed.cd ?? process.cwd()
const child = spawn(CHILD, parsed.child.slice(1), { cwd, env, stdio: 'inherit' })
child.on('error', (error) => {
  process.stderr.write('wsl.exe (the fake): could not start the stub fugaro in ' + cwd + ': ' + error.message + '\\n')
  process.exit(1)
})
// The child's status is the fake's status: the wrapper is transparent to an exit
// code, which is what T-F1 asserts, and a signal is mirrored so a killed child
// is a killed \`wsl.exe\` rather than a successful one.
child.on('exit', (code, signal) => {
  if (signal !== null) process.kill(process.pid, signal)
  process.exit(code === null ? 1 : code)
})
`
}
