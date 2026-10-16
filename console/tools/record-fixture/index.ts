// The recorder: the tool that turns one `fugaro watch --json` into a fixture
// (desktop-console plan Task 1.4; design section 9.1).
//
// **The tool is agent-written. The recordings are owner-run.** This file is
// written and tested by an agent, and it is deliberately built so that an agent
// *cannot* use it to produce a fixture by accident: it holds no credential, it
// reads no config (the CLI is the reader), it never defaults to the ambient
// project, and it refuses the two things the plan puts out of bounds -- the
// `belong` project and anything under a checkout named after it, and a path
// inside `~/.config/fugaro`. An agent's run in this task stopped at the tool;
// the fixture it commits is synthetic (`test/unit/synthetic.ts`), and the README
// says so in the same words.
//
// What it does, in the order it does it:
//
//   1. **reads its own arguments and refuses rather than guesses**
//      (`parseRecorderArgs`): no `--project` is a refusal rather than a fall
//      back to whatever the ambient selection happens to be (design section 3.4
//      rule 3), `belong` is a refusal, an `--out` outside a `fixtures/`
//      directory or not ending `.ndjson` is a refusal, and overwriting an
//      existing fixture needs `--force`.
//   2. **builds the spawn through the console's own rules** (`planFor`): the
//      exact argv of `live.watch` from `SPAWN_COMMANDS`, the fixed child
//      environment of `CHILD_ENV_KEYS`, an explicit working directory and
//      `shell: false`. The recorder spawns the same plan the console spawns, so
//      a recording cannot be of a command the console would refuse to run.
//   3. **reads stdout line by line, stamps each line's arrival with the
//      console's clock, and puts every line through the redactor** -- a
//      document through `redactDocument`, a sentence through `redactText`, a
//      blank line nowhere. The CLI's degraded mode writes a human sentence on
//      the same stream as the documents, and it becomes a *notice* record, which
//      is what a replay needs to show the same header warning.
//   4. **verifies before it writes**: the fixture text goes back through the
//      real `loadFixture` before a byte lands on disk, so a file the console
//      would refuse to replay is never written at all.
//   5. **records for a bounded time** (`--duration`, 120 s by default) and then
//      stops the child the way the console does -- `SIGTERM`, then `SIGKILL`
//      for a child that will not go. A torn last line (a write cut in half by
//      the signal) is dropped rather than recorded as garbage, and a recording
//      that read nothing at all is a refusal: an empty fixture would look like a
//      quiet installation rather than a failed run.
//
// What it deliberately does not do: it does not read `~/.config/fugaro`
// (`--config` is passed *through* to the CLI), it does not touch `belong`, it
// holds no token, **it prints neither the child's documents nor its stderr**
// (the operator's terminal is theirs, but a scrollback is a leak this tool will
// not create: the child's stderr is drained so a chatty CLI cannot block on a
// full pipe, and then discarded without a byte of it being echoed), and it
// writes exactly one file: the fixture named by `--out`.

import { statSync, writeFileSync } from 'node:fs'
import { homedir, hostname, userInfo } from 'node:os'
import { basename, dirname, resolve } from 'node:path'

import { SHUTDOWN_GRACE_MS, decodeFeedLine, nodeSpawner, planFor } from '../../src/main/spawn'
import type { ChildSpawner, SpawnPlan, SpawnedChildLike, Timers } from '../../src/main/spawn'
import type { ValidatedArguments } from '../../src/main/ipc'
import { FIXTURE_DIRECTORY, loadFixture, stampOf, writeFixture } from '../../src/source/fixture'
import type { FixtureMeta, FixtureRecord } from '../../src/source/fixture'
import { PROJECT_MARKER, RedactionRefusal, createRedactor } from '../../src/source/redact'
import type { Redactor } from '../../src/source/redact'
import type { JSONDoc } from '../../src/source/source'

/** DEFAULT_DURATION_SECONDS is how long a recording runs when `--duration` is
 * not given. It is long enough for a day rollover, a burn rate to warm up and a
 * run to go silent -- the design's own cases are *about* time -- and short
 * enough to sit through. */
export const DEFAULT_DURATION_SECONDS = 120

/** BELONG_PROJECT is the project the plan puts out of bounds: **no agent and no
 * automated run touches it**, and the recorder refuses it by name so that a
 * script which tried would stop before it spawned anything. */
export const BELONG_PROJECT = 'belong'

/** OUT_OF_BOUNDS is the fragment list `outOfBounds` refuses: the project above,
 * and the config directory an agent must never read. */
const OUT_OF_BOUNDS = [BELONG_PROJECT, '.config/fugaro']

/** RecorderOptions is a recorder invocation that passed every check. */
export interface RecorderOptions {
  readonly out: string
  readonly project: string
  readonly configPath?: string | undefined
  readonly binaryPath: string
  readonly directory: string
  readonly caseName: string
  readonly durationSeconds: number
  readonly repo?: string | undefined
  readonly force: boolean
}

/** RecorderRefusal is a refusal with the sentence to print. It is what a bad
 * invocation, an out-of-bounds path and an empty recording all end as. */
export class RecorderRefusal extends Error {
  override readonly name = 'RecorderRefusal'
}

/** USAGE is what `--help` prints: the one command the README documents, and the
 * flags around it. */
export const USAGE = `usage: npm run record-fixture -- --out ${FIXTURE_DIRECTORY}/<name>.ndjson --project <project> [options]

  --out <path>        where the fixture is written (must be under ${FIXTURE_DIRECTORY}/)
  --project <name>    the installation's project (required; never defaulted)
  --config <path>     the installation's config, passed through to the CLI
  --binary <path>     the fugaro binary (default: "fugaro", resolved by PATH)
  --dir <path>        the checkout to run from (default: the current directory)
  --case <name>       which recorded case this is (default: "normal")
  --duration <secs>   how long to record (default: ${DEFAULT_DURATION_SECONDS})
  --repo <owner/name> narrow the recording to one repository
  --force             overwrite an existing fixture
`

const KNOWN_FLAGS = new Set(['out', 'project', 'config', 'binary', 'dir', 'case', 'duration', 'repo'])

/**
 * parseRecorderArgs turns argv into options or throws `RecorderRefusal` with the
 * sentence to print. It is a pure function of the argument list, the current
 * directory and one injected `exists`, which is what lets `recorder.test.ts`
 * hold every refusal without spawning anything: a recorder whose *refusals* are
 * untested is a recorder that can be talked into recording the wrong project.
 */
export function parseRecorderArgs(
  argv: readonly string[],
  cwd: string,
  deps: { readonly exists?: (path: string) => boolean } = {},
): RecorderOptions {
  const flags = new Map<string, string>()
  const switches = new Set<string>()
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i] ?? ''
    if (token === '--help' || token === '-h') throw new RecorderRefusal(USAGE)
    if (!token.startsWith('--')) {
      throw new RecorderRefusal(`unexpected argument ${JSON.stringify(token)}\n\n${USAGE}`)
    }
    const [name, inline] = splitFlag(token)
    if (inline !== undefined) {
      flags.set(name, inline)
      continue
    }
    if (name === 'force') {
      switches.add(name)
      continue
    }
    const value = argv[i + 1]
    if (value === undefined || value.startsWith('--')) {
      throw new RecorderRefusal(`--${name} needs a value\n\n${USAGE}`)
    }
    flags.set(name, value)
    i += 1
  }
  for (const name of flags.keys()) {
    if (!KNOWN_FLAGS.has(name)) throw new RecorderRefusal(`--${name} is not a recorder flag\n\n${USAGE}`)
  }

  const out = flags.get('out')
  if (out === undefined) throw new RecorderRefusal(`--out is required: a recording is one file\n\n${USAGE}`)
  const project = flags.get('project')
  if (project === undefined) {
    throw new RecorderRefusal(
      `--project is required: the recorder never defaults to the ambient installation\n\n${USAGE}`,
    )
  }
  const directory = resolve(cwd, flags.get('dir') ?? '.')

  const resolvedOut = resolve(cwd, out)
  if (!resolvedOut.endsWith('.ndjson')) throw new RecorderRefusal('--out must name a .ndjson fixture')
  if (basename(dirname(resolvedOut)) !== basename(FIXTURE_DIRECTORY)) {
    throw new RecorderRefusal(`--out must be written under ${FIXTURE_DIRECTORY}/, not ${dirname(resolvedOut)}`)
  }
  outOfBounds('--project', project)
  outOfBounds('--dir', directory)
  const configPath = flags.get('config')
  if (configPath !== undefined) outOfBounds('--config', configPath)
  const binaryPath = flags.get('binary') ?? 'fugaro'
  outOfBounds('--binary', binaryPath)

  const duration = Number(flags.get('duration') ?? DEFAULT_DURATION_SECONDS)
  if (!Number.isInteger(duration) || duration <= 0) {
    throw new RecorderRefusal('--duration must be a positive whole number of seconds')
  }
  const force = switches.has('force')
  const exists = deps.exists ?? existsOnDisk
  if (!force && exists(resolvedOut)) {
    throw new RecorderRefusal(`${resolvedOut} exists; pass --force to overwrite a fixture`)
  }

  return {
    out: resolvedOut,
    project,
    configPath,
    binaryPath,
    directory,
    caseName: flags.get('case') ?? 'normal',
    durationSeconds: duration,
    repo: flags.get('repo'),
    force,
  }
}

/** splitFlag splits `--name=value` into its two halves. */
function splitFlag(token: string): [string, string | undefined] {
  const body = token.slice(2)
  const eq = body.indexOf('=')
  if (eq < 0) return [body, undefined]
  return [body.slice(0, eq), body.slice(eq + 1)]
}

/** outOfBounds refuses a value that names the project the plan puts out of
 * bounds, or the config directory an agent must never read. It is a check over
 * the *value as spelled*, case-insensitively, so `--dir ../Belong` is refused
 * too -- the point is to stop a scripted run, not to be a filesystem check. */
function outOfBounds(flag: string, value: string): void {
  const lowered = value.toLowerCase().replace(/\\/g, '/')
  for (const fragment of OUT_OF_BOUNDS) {
    if (lowered === fragment || lowered.includes(`/${fragment}`) || lowered.includes(`${fragment}/`)) {
      throw new RecorderRefusal(`${flag} names ${fragment}, which this recorder never touches`)
    }
  }
}

/** existsOnDisk is the default `exists`: `statSync` rather than `existsSync`, so
 * a directory named like a fixture is caught too -- writing over one would fail
 * anyway, and refusing first says why. */
function existsOnDisk(path: string): boolean {
  try {
    statSync(path)
    return true
  } catch {
    return false
  }
}

/** planForRecording builds the one spawn this tool performs, through the
 * console's own `planFor` -- so the argv, the child environment and the working
 * directory are the console's rules and not a second copy of them. */
export function planForRecording(
  options: RecorderOptions,
  appEnv: Readonly<Record<string, string | undefined>>,
): SpawnPlan {
  const args: Record<string, string> = {}
  if (options.repo !== undefined) args['repo'] = options.repo
  return planFor('live.watch', args as ValidatedArguments, {
    target: {
      project: options.project,
      ...(options.configPath === undefined ? {} : { configPath: options.configPath }),
    },
    binaryPath: options.binaryPath,
    directory: options.directory,
    appEnv,
  })
}

/**
 * recorderLiterals is what the recorder tells the redactor about the
 * installation it is recording: the project name, the config path, the binary,
 * the checkout and (when given) the repository -- every one of them a string
 * with no shape for a pattern class to catch, and every one of them something
 * that must not survive into a fixture.
 *
 * They are used as *needles*: the redactor replaces them with `[redacted]`, and
 * the fixture keeps the marker. They are never printed and never written.
 */
export function recorderLiterals(
  options: RecorderOptions,
  env: Readonly<Record<string, string | undefined>> = {},
): readonly string[] {
  const values: (string | undefined)[] = [
    options.project,
    options.configPath,
    options.binaryPath === 'fugaro' ? undefined : options.binaryPath,
    options.directory,
    options.repo,
    env['HOSTNAME'],
    env['USER'],
    env['HOME'],
  ]
  return uniqueValues(values)
}

/** machineLiterals is `recorderLiterals` plus what only this process knows: the
 * machine's host name, the operator's user name and their home directory. It is
 * separate so a test can build literals without touching the machine it runs
 * on. */
export function machineLiterals(
  options: RecorderOptions,
  env: Readonly<Record<string, string | undefined>> = process.env,
): readonly string[] {
  const extra: string[] = []
  try {
    extra.push(hostname(), userInfo().username, homedir())
  } catch {
    // A process with no passwd entry and no host name still records: the
    // pattern classes carry the rest.
  }
  return uniqueValues([...recorderLiterals(options, env), ...extra])
}

/** uniqueValues drops the `undefined`s and the empty strings -- `''` is a
 * literal that would match everywhere, and it is never a name. */
function uniqueValues(values: readonly (string | undefined)[]): readonly string[] {
  const out: string[] = []
  for (const value of values) {
    if (value === undefined || value === '') continue
    if (!out.includes(value)) out.push(value)
  }
  return out
}

/** recordLine is one line of the child's stdout as a fixture record, or `null`
 * for a line that is nothing at all. A line that is not a JSON object is a
 * **notice** (`decodeFeedLine`'s rule, json-contract section 10.13) and never a
 * parse error; a line that *is* one goes through the redactor, which refuses
 * rather than passing anything it cannot vouch for. */
export function recordLine(line: string, at: string, redactor: Redactor): FixtureRecord | null {
  const decoded = decodeFeedLine(line)
  if (decoded.kind === 'empty') return null
  if (decoded.kind === 'notice') return { at, notice: redactor.redactText(decoded.text) }
  return { at, document: redactor.redactDocument(decoded.document, 'a recorded frame') as JSONDoc }
}

/** recorderMeta is the header a recording carries. The project is written as the
 * **marker**, not as the project: a fixture names a case, not an installation,
 * and a header is no more allowed to carry the operator's project than a
 * document is. `synthetic` is false, and `recorded` is the one field a synthetic
 * fixture can never have. */
export function recorderMeta(options: RecorderOptions, now: Date, cliVersion?: string | undefined): FixtureMeta {
  return {
    case: options.caseName,
    project: PROJECT_MARKER,
    synthetic: false,
    note: `recorded on ${stampOf(now)} by tools/record-fixture and redacted with the console's own rules; read it before you commit it`,
    ...(cliVersion === undefined ? {} : { cli: cliVersion }),
    recorded: stampOf(now),
  }
}

/** renderRecording is **verify-then-write** as a function: the fixture text is
 * built, loaded again with the real loader -- marker, digest, record count,
 * order, and the redaction audit -- and only then returned. A recording the
 * console itself would refuse is a `FixtureRefusal` here, before a byte reaches
 * the disk. */
export function renderRecording(records: readonly FixtureRecord[], meta: FixtureMeta, where: string): string {
  const text = writeFixture(records, meta)
  loadFixture(text, where)
  return text
}

/** RecordingResult is what a finished recording read: the records, and the
 * refusal that ended it early, if there was one. A result with a refusal is
 * never written. */
export interface RecordingResult {
  readonly records: readonly FixtureRecord[]
  readonly refusal: Error | null
  /** `exitCode` is the child's own exit code when it exited before the
   * recording's duration was up -- a CLI that refused to start. `null` when the
   * recorder stopped it, which is the happy path. */
  readonly exitCode: number | null
}

/** Recording is a recording in progress. */
export interface Recording {
  /** `plan` is what was spawned: the argv, the cwd and the child environment. */
  readonly plan: SpawnPlan
  readonly records: readonly FixtureRecord[]
  /** `finished` resolves when the child is gone -- by itself, by `stop()`, at
   * the end of the duration, or on a refusal. */
  readonly finished: Promise<RecordingResult>
  /** `stop` ends the recording early, the way Ctrl-C does. */
  stop(): void
}

/** RecordingDeps is everything `startRecording` reaches for, as arguments: the
 * spawner, the clock and the timers, so `recorder.test.ts` drives a whole
 * recording with a stub child and a fake clock -- and no timers, no process and
 * no child of its own. */
export interface RecordingDeps {
  readonly options: RecorderOptions
  readonly plan: SpawnPlan
  readonly redactor: Redactor
  readonly spawner: ChildSpawner
  readonly timers: Timers
  readonly clock: () => Date
}

/**
 * startRecording spawns the plan and reads it. It is the whole recorder minus
 * the argument checks and the write, which is what makes it testable: what it
 * must get right -- every document redacted, every sentence a notice, a blank
 * line nowhere, a refusal fatal -- is a property a test can hold with a stub
 * child.
 */
export function startRecording(deps: RecordingDeps): Recording {
  const { options, plan, redactor, spawner, timers, clock } = deps
  const [binary, ...argv] = plan.argv
  if (binary === undefined) throw new RecorderRefusal('the plan names no binary')
  const child: SpawnedChildLike = spawner(binary, argv, {
    cwd: plan.cwd,
    env: plan.env,
    shell: false,
    stdio: ['ignore', 'pipe', 'pipe'],
  })

  const records: FixtureRecord[] = []
  let refusal: Error | null = null
  let exitCode: number | null = null
  let partial = ''
  let settled = false
  let ending = false
  let durationTimer: ReturnType<typeof setTimeout> | null = null
  let graceTimer: ReturnType<typeof setTimeout> | null = null
  let settle: () => void = () => {}

  const finished = new Promise<RecordingResult>((resolve) => {
    settle = (): void => {
      if (settled) return
      settled = true
      if (durationTimer !== null) timers.clearTimeout(durationTimer)
      if (graceTimer !== null) timers.clearTimeout(graceTimer)
      resolve({ records, refusal, exitCode })
    }
  })

  /** end stops the child the way the console stops one: SIGTERM, then SIGKILL
   * for a child that will not go. The recording is finished when the child is
   * gone, not when the signal was sent, and a second `stop()` -- a Ctrl-C on a
   * recording that is already ending -- is not a second signal. */
  const end = (): void => {
    if (settled || ending) return
    ending = true
    if (!child.kill('SIGTERM')) {
      settle()
      return
    }
    graceTimer = timers.setTimeout(() => {
      child.kill('SIGKILL')
      settle()
    }, SHUTDOWN_GRACE_MS)
  }

  child.stdout?.setEncoding?.('utf8')
  // The child's stderr is *drained and discarded*, and both halves are on
  // purpose. Drained, because `planFor` gives the child a pipe and a pipe
  // nobody reads fills up (about 64 KB) and then blocks the child's write --
  // a CLI that logs to stderr would stall the recording, and the recording
  // would end with nothing written rather than with the fixture the operator
  // asked for. Discarded, because the recorder's one output is the fixture: the
  // operator's terminal is theirs, but a stderr line is raw CLI text and a
  // scrollback is a leak this tool will not create. That is also why this is not
  // routed through `decodeFeedLine`: a stderr line is not a feed frame, and
  // turning it into a *notice* record would put the CLI's diagnostics in the
  // fixture as though `watch --json` had written them.
  child.stderr?.on('data', () => undefined)
  child.stdout?.on('data', (chunk: Buffer | string) => {
    if (settled) return
    partial += typeof chunk === 'string' ? chunk : chunk.toString('utf8')
    const lines = partial.split('\n')
    partial = lines.pop() ?? ''
    for (const line of lines) {
      try {
        const record = recordLine(line, stampOf(clock()), redactor)
        if (record !== null) records.push(record)
      } catch (error) {
        // A frame the redactor will not vouch for ends the recording and
        // nothing is written: a fixture is redacted or it does not exist.
        refusal = error instanceof Error ? error : new Error(String(error))
        end()
      }
      if (settled) return
    }
  })
  child.on('error', (error: Error) => {
    refusal = refusal ?? error
    end()
  })
  child.on('exit', (code: number | null) => {
    if (!settled && graceTimer === null) exitCode = code
    settle()
  })

  durationTimer = timers.setTimeout(end, options.durationSeconds * 1000)

  return { plan, records, finished, stop: end }
}

/** RecorderDeps is everything the tool's `main` reaches for: the spawner, the
 * timers, the clock, one `exists`, one `write` and one `log`. A test can drive
 * the whole tool through it; the defaults are the real ones. Nothing here is a
 * credential and nothing here reads a config file. */
export interface RecorderDeps {
  readonly spawner?: ChildSpawner
  readonly timers?: Timers
  readonly clock?: () => Date
  readonly exists?: (path: string) => boolean
  /** `write` is the tool's one side effect. It is an argument so a test can
   * assert the fixture text without writing one into `test/fixtures/`. */
  readonly write?: (path: string, text: string) => void
  readonly log?: (line: string) => void
  readonly cwd?: string
  readonly appEnv?: Readonly<Record<string, string | undefined>>
}

const NODE_TIMERS: Timers = {
  setTimeout: (handler, ms) => setTimeout(handler, ms),
  clearTimeout: (handle) => {
    clearTimeout(handle)
  },
}

/**
 * main records one fixture and returns the process exit code: 0 for a fixture
 * written, 2 for a refusal (whose sentence it prints). It is the whole tool, and
 * the one function an owner's run reaches.
 */
export async function main(argv: readonly string[], deps: RecorderDeps = {}): Promise<number> {
  const cwd = deps.cwd ?? process.cwd()
  const log = deps.log ?? ((line: string): void => void process.stdout.write(`${line}\n`))
  const clock = deps.clock ?? ((): Date => new Date())
  const options = parseRecorderArgs(argv, cwd, deps.exists === undefined ? {} : { exists: deps.exists })
  const plan = planForRecording(options, deps.appEnv ?? process.env)
  const redactor = createRedactor(machineLiterals(options, deps.appEnv ?? process.env))

  log(`recording ${options.caseName} for ${String(options.durationSeconds)}s`)
  log(`  argv: ${plan.argv[0] ?? ''} ${plan.argv.slice(1).join(' ')}`)
  log(`  cwd:  ${plan.cwd}`)

  const recording = startRecording({
    options,
    plan,
    redactor,
    spawner: deps.spawner ?? nodeSpawner,
    timers: deps.timers ?? NODE_TIMERS,
    clock,
  })
  const result = await recording.finished
  if (result.refusal !== null) {
    throw new RecorderRefusal(
      result.refusal instanceof RedactionRefusal
        ? `redaction refused a frame, nothing written: ${result.refusal.message}`
        : `the recording failed, nothing written: ${result.refusal.message}`,
    )
  }
  if (result.records.length === 0) {
    throw new RecorderRefusal(
      result.exitCode === null
        ? 'no document arrived before the recording ended; nothing written'
        : `the CLI exited with code ${String(result.exitCode)} and wrote no document; nothing written`,
    )
  }

  const meta = recorderMeta(options, clock())
  const text = renderRecording(result.records, meta, options.out)
  ;(deps.write ?? defaultWrite)(options.out, text)
  log(`wrote ${options.out}: ${String(result.records.length)} records`)
  return 0
}

/** defaultWrite is the real write: one file, the fixture named by `--out`, and
 * nothing else. */
function defaultWrite(path: string, text: string): void {
  writeFileSync(path, text)
}

/** isEntryPoint is true when this file was run rather than imported: `argv[1]`
 * is this module, compiled to `index.js` (or run from source as `index.ts` by a
 * loader). A test importing it -- vitest's entry is `argv[1]` there -- does not
 * start a recording. */
function isEntryPoint(argv1: string | undefined): boolean {
  return argv1 !== undefined && /record-fixture[\\/]index\.(?:js|cjs|ts)$/.test(argv1)
}

if (isEntryPoint(process.argv[1])) {
  main(process.argv.slice(2)).then(
    (code) => {
      process.exitCode = code
    },
    (error: unknown) => {
      const message = error instanceof Error ? error.message : String(error)
      process.stderr.write(`${message}\n`)
      process.exitCode = 2
    },
  )
}
