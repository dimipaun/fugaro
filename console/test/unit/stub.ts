// A fake `fugaro`, for the tests that must observe what the console hands a
// child process (desktop-console plan Task 1.3).
//
// **Never the real CLI and never the network.** `installStub` writes two files
// into a temporary directory: an executable `fugaro` that `exec`s the Node
// running the test suite (by absolute path, so the child's fixed `PATH` is
// irrelevant) with a generated script, and a `scenario.json` that says what each
// command should answer. Every spawn appends one JSON line to a report file
// beside them.
//
// The report file is the point of the whole arrangement: it is how a test sees
// **the child's own view** -- its `argv`, its `cwd`, the keys of its environment,
// its `PATH`, its `FUGARO_PROJECT` -- rather than the console's account of what
// it passed. A test that asserted on the plan object would be asserting that the
// console is self-consistent; the report is what the child actually got, which
// is the only thing an operator's machine can be wrong about.
//
// The report path is baked into the script as a string literal rather than
// passed in the environment or in argv, because the child's environment is
// exactly the fixed set under test and its argv is exactly what is under test.
// An out-of-band channel is the only kind that cannot change either.

import { chmodSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { existsSync } from 'node:fs'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

/** StubAnswer is what one command does when the stub is asked for it. */
export interface StubAnswer {
  /** `stdout` is written as-is and the process exits. */
  readonly stdout?: string
  /** `stderr` is written to stderr before exiting. */
  readonly stderr?: string
  /** `code` is the exit status (default 0). */
  readonly code?: number
  /** `lines` are written to stdout one at a time, each with a newline, with
   * `delayMs` between them -- the streaming case. */
  readonly lines?: readonly string[]
  /** `delayMs` is the gap between `lines`, and before `stdout`. */
  readonly delayMs?: number
  /** `holdMs` keeps the process alive after its output, without exiting. */
  readonly holdMs?: number
  /** `hold` keeps it alive until it is killed: the long-lived `watch` case. */
  readonly hold?: boolean
  /** `signal` kills the child with a signal instead of exiting -- Task 0.2's
   * closed-stdout case, which reports a signal and not an exit code. */
  readonly signal?: NodeJS.Signals
  /** `split` writes `stdout` in two chunks with a `delayMs` gap, so a test can
   * assert that a document split across two reads is still one document. */
  readonly split?: boolean
}

/** StubScenario keys answers by the command the stub was asked for. The keys are
 * `version`, `watch`, `budget show`, `report`, `runs ls`, `diagnose`, `logs`,
 * `logs --url`, `secrets ls`, `accounts ls` and `doctor`. */
export interface StubScenario {
  readonly commands?: Readonly<Record<string, StubAnswer>>
  readonly default?: StubAnswer
}

/** StubReport is one line of the report file: what the child saw. */
export interface StubReport {
  readonly argv: readonly string[]
  readonly cwd: string
  readonly env: Readonly<Record<string, string>>
  readonly envKeys: readonly string[]
  readonly path: string
  readonly project: string | null
  readonly uid: number | null
}

/** StubKill is one line of the kills file: a child the console stopped. It is
 * the child's own account of being terminated, and it is in a *separate* file
 * from the report because it is not a spawn: `calls()` counts report lines. */
export interface StubKill {
  readonly signal: string
  readonly pid: number
  readonly argv: readonly string[]
}

/** Stub is an installed fake CLI. */
export interface Stub {
  /** `binary` is the absolute path of the executable to configure. */
  readonly binary: string
  /** `directory` is the temporary directory it lives in. */
  readonly directory: string
  /** `reportPath` is the file every spawn appends to. */
  readonly reportPath: string
  /** reports reads the report file back, one entry per spawn so far. */
  reports(): readonly StubReport[]
  /** calls is how many spawns have been recorded. */
  calls(): number
  /** kills reads the kills file back: every child that received a `SIGTERM`.
   * A child killed with `SIGKILL` cannot write one, which is the point -- the
   * grace period is what stops that from happening. */
  kills(): readonly StubKill[]
  /** write replaces the scenario, for a test that needs a second behaviour. */
  write(scenario: StubScenario): void
}

/**
 * installStub writes a fake `fugaro` into `directory` and returns it. The
 * directory is created if it does not exist, which is what lets a test place one
 * under a *prefix* it invented (`<tmp>/prefix/bin/fugaro`) and drive the
 * resolver's order without touching `/opt/homebrew`.
 */
export function installStub(directory: string, scenario: StubScenario): Stub {
  mkdirSync(directory, { recursive: true })
  const binary = join(directory, 'fugaro')
  const scenarioPath = join(directory, 'scenario.json')
  const reportPath = join(directory, 'report.jsonl')
  const killsPath = join(directory, 'kills.jsonl')
  const scriptPath = join(directory, 'fugaro-stub.mjs')

  writeFileSync(scenarioPath, JSON.stringify(scenario, null, 2))
  writeFileSync(scriptPath, stubScript(reportPath, scenarioPath, killsPath))
  // The shim names Node by absolute path, because `#!/usr/bin/env node` would
  // resolve `node` through the child's `PATH` -- which is one of the things
  // under test here. It is the generated script itself with Node as its
  // interpreter, not a `/bin/sh` wrapper: a shell adds `PWD` and `SHLVL` to the
  // environment it execs into, and the environment is also under test. A Node
  // path with whitespace cannot be a shebang, so that alone falls back to the
  // shell.
  if (/\s/.test(process.execPath)) {
    writeFileSync(binary, `#!/bin/sh\nexec ${shellQuote(process.execPath)} ${shellQuote(scriptPath)} "$@"\n`)
  } else {
    writeFileSync(binary, `#!${process.execPath}\nimport(${JSON.stringify(pathToFileURL(scriptPath).href)})\n`)
  }
  chmodSync(binary, 0o755)

  return {
    binary,
    directory,
    reportPath,
    reports(): readonly StubReport[] {
      if (!existsSync(reportPath)) return []
      return readFileSync(reportPath, 'utf8')
        .split('\n')
        .filter((line) => line.trim() !== '')
        .map((line) => JSON.parse(line) as StubReport)
    },
    calls(): number {
      if (!existsSync(reportPath)) return 0
      return readFileSync(reportPath, 'utf8').split('\n').filter((line) => line.trim() !== '').length
    },
    kills(): readonly StubKill[] {
      if (!existsSync(killsPath)) return []
      return readFileSync(killsPath, 'utf8')
        .split('\n')
        .filter((line) => line.trim() !== '')
        .map((line) => JSON.parse(line) as StubKill)
    },
    write(next: StubScenario): void {
      writeFileSync(scenarioPath, JSON.stringify(next, null, 2))
    },
  }
}

function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

/**
 * stubScript is the generated child. It is written as a string rather than kept
 * as a `.mjs` file in the repository because the two paths it needs must be
 * absolute and per-test, and a template is the only way to give it both without
 * an out-of-band channel.
 */
function stubScript(reportPath: string, scenarioPath: string, killsPath: string): string {
  return `// Generated by console/test/unit/stub.ts. Do not edit.
import { appendFileSync, readFileSync } from 'node:fs'

const REPORT = ${JSON.stringify(reportPath)}
const KILLS = ${JSON.stringify(killsPath)}
const SCENARIO = ${JSON.stringify(scenarioPath)}
const argv = process.argv.slice(2)

// The console stops a live child with SIGTERM and only escalates to SIGKILL
// after its grace period, so a child that gets to write this line is a child
// the console stopped *politely*. Nothing is written when it is killed with
// SIGKILL: a process cannot report its own SIGKILL, and a test that finds no
// line here is a test that found a console that did not wait.
process.on('SIGTERM', () => {
  appendFileSync(KILLS, JSON.stringify({ signal: 'SIGTERM', pid: process.pid, argv }) + '\\n')
  process.exit(0)
})

appendFileSync(
  REPORT,
  JSON.stringify({
    argv,
    cwd: process.cwd(),
    env: { ...process.env },
    envKeys: Object.keys(process.env).sort(),
    path: process.env.PATH ?? '',
    project: process.env.FUGARO_PROJECT ?? null,
    uid: typeof process.getuid === 'function' ? process.getuid() : null,
  }) + '\\n',
)

const scenario = JSON.parse(readFileSync(SCENARIO, 'utf8'))

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

// The command path this invocation is for: the leading words that do not start
// with a dash, with the two log operations told apart by --url.
const words = []
for (const element of argv) {
  if (element.startsWith('-')) break
  words.push(element)
}
const key = words[0] === 'logs' ? (argv.includes('--url') ? 'logs --url' : 'logs') : words.join(' ')
const answer = (scenario.commands && scenario.commands[key]) || scenario.default || {}

async function main() {
  if (answer.delayMs) await sleep(answer.delayMs)
  if (answer.stderr) process.stderr.write(answer.stderr)
  if (answer.lines) {
    for (const line of answer.lines) {
      process.stdout.write(line + '\\n')
      if (answer.delayMs) await sleep(answer.delayMs)
    }
  }
  if (typeof answer.stdout === 'string' && answer.stdout !== '') {
    if (answer.split) {
      const half = Math.floor(answer.stdout.length / 2)
      process.stdout.write(answer.stdout.slice(0, half))
      await sleep(answer.delayMs || 20)
      process.stdout.write(answer.stdout.slice(half))
    } else {
      process.stdout.write(answer.stdout)
    }
  }
  if (answer.signal) {
    process.kill(process.pid, answer.signal)
    return
  }
  if (answer.hold) {
    setInterval(() => {}, 1000)
    return
  }
  if (answer.holdMs) {
    await sleep(answer.holdMs)
  }
  process.exit(answer.code || 0)
}

main()
`
}

/** commandKey is the stub's own reading of its argv: the leading words that do
 * not start with a dash, with `logs --url` told apart from `logs` because the
 * console's two log operations differ by exactly that flag. It is a copy of the
 * CLI's command *path*, and the test asserts it agrees with the spawn table's
 * `words` rather than trusting this comment. */
export function commandKey(argv: readonly string[]): string {
  const words: string[] = []
  for (const element of argv) {
    if (element.startsWith('-')) break
    words.push(element)
  }
  const key = words.join(' ')
  if (words[0] === 'logs') return argv.includes('--url') ? 'logs --url' : 'logs'
  return key
}
