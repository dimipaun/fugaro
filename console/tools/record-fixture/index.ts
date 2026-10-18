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
//      rule 3), no `--gcp-project` is a refusal (its ID, bucket and number are
//      literals the redactor must be told, and this tool reads no config to
//      find them), `belong` is a refusal however it is spaced or spelled, an
//      `--out` anywhere but this repository's `console/test/fixtures/` or not
//      ending `.ndjson` is a refusal, overwriting an existing fixture needs
//      `--force`, and a `--case` outside the design's list is a
//      refusal too -- otherwise the tool writes `test/fixtures/wibble.ndjson`
//      and the *suite* refuses it later, on whoever replays it rather than on
//      the invocation that made it.
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
//   4. **verifies before it writes, and writes safely**: the fixture text goes
//      back through the real `loadFixture` before a byte lands on disk, and the
//      write itself (`writeFixtureFile`) refuses a symbolic link anywhere from
//      the console package down to the target, refuses a target that is not a
//      regular file even with `--force`, writes a temporary file exclusively in
//      the same directory and moves it into place.
//   5. **records for a bounded time** (`--duration`, 120 s by default) and then
//      stops the child the way the console does -- `SIGTERM`, then `SIGKILL`
//      for a child that will not go. A torn last line (a write cut in half by
//      the signal) is dropped rather than recorded as garbage -- a last line
//      that decodes as a whole document is not torn, and is kept -- and a recording
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

import { randomBytes } from 'node:crypto'
import { linkSync, lstatSync, readFileSync, realpathSync, renameSync, unlinkSync, writeFileSync } from 'node:fs'
import type { Stats } from 'node:fs'
import { homedir, hostname, userInfo } from 'node:os'
import { basename, dirname, join, resolve } from 'node:path'

import { SHUTDOWN_GRACE_MS, decodeFeedLine, nodeSpawner, planFor } from '../../src/main/spawn'
import type { ChildSpawner, SpawnPlan, SpawnedChildLike, Timers } from '../../src/main/spawn'
import type { ValidatedArguments } from '../../src/main/ipc'
import { FIXTURE_CASES, FIXTURE_DIRECTORY, loadFixture, stampOf, writeFixture } from '../../src/source/fixture'
import type { FixtureCaseName, FixtureMeta, FixtureRecord } from '../../src/source/fixture'
import {
  MAX_REDACTABLE_LINE_LENGTH,
  PROJECT_MARKER,
  RedactionRefusal,
  createRedactor,
  foldConfusables,
} from '../../src/source/redact'
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
  readonly caseName: FixtureCaseName
  readonly durationSeconds: number
  readonly repo?: string | undefined
  readonly force: boolean
  /** `gcpProject` is the installation's GCP project ID: a literal, with its
   * runs bucket (`fugaro-runs-<id>`) derived from it. */
  readonly gcpProject: string
  /** `projectNumber` is the GCP project number, when the owner gives it. */
  readonly projectNumber?: string | undefined
  /** `literals` are any further names (`--literal`, repeatable) the owner
   * knows the recording can carry: a bucket with another name, a codename. */
  readonly literals: readonly string[]
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
  --gcp-project <id>  the installation's GCP project ID (required; redacted with
                      its runs bucket wherever it appears)
  --project-number <n> the GCP project number, redacted wherever it appears
  --literal <text>    any further name to redact (repeatable)
  --config <path>     the installation's config, passed through to the CLI
  --binary <path>     the fugaro binary (default: "fugaro", resolved by PATH)
  --dir <path>        the checkout to run from (default: the current directory)
  --case <name>       which recorded case this is (default: "normal")
  --duration <secs>   how long to record (default: ${DEFAULT_DURATION_SECONDS})
  --repo <owner/name> narrow the recording to one repository
  --force             overwrite an existing regular file (never a link)
`

const KNOWN_FLAGS = new Set([
  'out',
  'project',
  'gcp-project',
  'project-number',
  'literal',
  'config',
  'binary',
  'dir',
  'case',
  'duration',
  'repo',
  // `force` takes no value of the audit's kind -- it never reaches `flags` --
  // but it is in the list so that a use of it that the loop above did not handle
  // is refused as *this* flag rather than as "not a recorder flag" (see the loop).
  'force',
])

/** GCP_PROJECT_ID_RE is a GCP project ID's own grammar. */
const GCP_PROJECT_ID_RE = /^[a-z][a-z0-9-]{4,28}[a-z0-9]$/

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
  deps: { readonly exists?: (path: string) => boolean; readonly consoleRoot?: string } = {},
): RecorderOptions {
  const flags = new Map<string, string>()
  const literals: string[] = []
  const setFlag = (name: string, value: string): void => {
    if (name === 'literal') literals.push(value)
    else flags.set(name, value)
  }
  const switches = new Set<string>()
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i] ?? ''
    if (token === '--help' || token === '-h') throw new RecorderRefusal(USAGE)
    if (!token.startsWith('--')) {
      throw new RecorderRefusal(`unexpected argument ${JSON.stringify(token)}\n\n${USAGE}`)
    }
    const [name, inline] = splitFlag(token)
    if (name === 'force') {
      // A switch, and the one flag whose inline form is not a value: every other
      // flag takes `--x=v`, so `--force=true` is what a reader who has just used
      // them writes, and the review of Task 3.2 found it refused with "`--force`
      // is not a recorder flag" -- a sentence that names the wrong reason and
      // leaves the operator guessing about a flag the usage text documents.
      // `force` is therefore a known flag, `--force=true` means force, and
      // `--force=false` means the default, so the inline form says what it reads
      // as. Anything else is refused with a sentence about the switch.
      if (inline === undefined) {
        switches.add(name)
        continue
      }
      if (inline === 'true') {
        switches.add(name)
        continue
      }
      if (inline === 'false') continue
      throw new RecorderRefusal(`--force takes true or false, not ${JSON.stringify(inline)}\n\n${USAGE}`)
    }
    if (inline !== undefined) {
      setFlag(name, inline)
      continue
    }
    const value = argv[i + 1]
    if (value === undefined || value.startsWith('--')) {
      throw new RecorderRefusal(`--${name} needs a value\n\n${USAGE}`)
    }
    setFlag(name, value)
    i += 1
  }
  for (const name of [...flags.keys(), ...(literals.length > 0 ? ['literal'] : [])]) {
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
  // **The directory is this repository's, not any directory named
  // `fixtures`**: `/etc/fixtures/x.ndjson` and `~/.ssh/fixtures/x.ndjson` were
  // both accepted when only the name was checked. This is the lexical half;
  // `writeFixtureFile` checks the real path, with every link refused.
  const fixturesDir = resolve(deps.consoleRoot ?? defaultConsoleRoot(), FIXTURE_DIRECTORY)
  if (dirname(resolvedOut) !== fixturesDir) {
    throw new RecorderRefusal(
      `--out must be written under ${FIXTURE_DIRECTORY}/ of this repository's console (${fixturesDir}), not ${dirname(resolvedOut)}`,
    )
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
  const caseName = flags.get('case') ?? 'normal'
  if (!isFixtureCase(caseName)) {
    // A recorder that writes `test/fixtures/wibble.ndjson` has produced a file
    // its own suite refuses, and the refusal lands on whoever replays it rather
    // than on the invocation that made it. `synthetic` is deliberately not
    // accepted either: a recording is never synthetic, and the header this tool
    // writes says `synthetic: false`.
    throw new RecorderRefusal(
      `--case must name one of the design's recorded cases (${FIXTURE_CASES.join(', ')}), not ${JSON.stringify(caseName)}`,
    )
  }
  const gcpProject = flags.get('gcp-project')
  if (gcpProject === undefined) {
    throw new RecorderRefusal(
      `--gcp-project is required: its ID and bucket are names the redactor must be told, and this recorder reads no config to find them\n\n${USAGE}`,
    )
  }
  if (!GCP_PROJECT_ID_RE.test(gcpProject)) {
    throw new RecorderRefusal(`--gcp-project must be a GCP project ID (6-30 lower-case letters, digits and hyphens)`)
  }
  outOfBounds('--gcp-project', gcpProject)
  const projectNumber = flags.get('project-number')
  if (projectNumber !== undefined && !/^\d{6,20}$/.test(projectNumber)) {
    throw new RecorderRefusal('--project-number must be the GCP project number: digits only')
  }
  for (const literal of literals) outOfBounds('--literal', literal)

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
    caseName,
    durationSeconds: duration,
    repo: flags.get('repo'),
    force,
    gcpProject,
    projectNumber,
    literals,
  }
}

/** isFixtureCase narrows a `--case` value to the design's list, so the options
 * the rest of this file gets are a case `FixtureMeta` accepts rather than an
 * arbitrary string that happens to have been typed on a command line. */
function isFixtureCase(value: string): value is FixtureCaseName {
  return (FIXTURE_CASES as readonly string[]).includes(value)
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
  // Normalised first: NFKC, every whitespace and control character gone,
  // Cyrillic and Greek look-alikes folded to the Latin letter they are drawn
  // as, lower case, and a trailing dot dropped from every segment (a resolver
  // drops it too). `--project " belong"` was a project the CLI would trim and
  // the check would not; `bеlong` (Cyrillic е) and `belong.` were two more.
  const lowered = foldConfusables(value.normalize('NFKC').replace(/[\s\p{C}]+/gu, ''))
    .toLowerCase()
    .replace(/\\/g, '/')
    .replace(/\.+(?=\/|$)/g, '')
  for (const fragment of OUT_OF_BOUNDS) {
    if (
      lowered === fragment ||
      lowered.includes(`/${fragment}`) ||
      lowered.includes(`${fragment}/`) ||
      lowered.startsWith(`${fragment}-`)
    ) {
      throw new RecorderRefusal(`${flag} names ${fragment}, which this recorder never touches`)
    }
  }
}

/** existsOnDisk is the default `exists`: **`lstatSync`**, so a dangling
 * symbolic link counts as something that is there. `statSync` follows the
 * link, called a dangling one absent, and the write then went where it
 * pointed. */
function existsOnDisk(path: string): boolean {
  try {
    lstatSync(path)
    return true
  } catch {
    return false
  }
}

/** defaultConsoleRoot is the console package this tool belongs to: the nearest
 * directory above this module whose `package.json` is `fugaro-console`. The
 * module runs from `tools/record-fixture/` (source) or
 * `.record-fixture/tools/record-fixture/` (compiled), and both are inside it. */
function defaultConsoleRoot(): string {
  let dir = __dirname
  for (;;) {
    try {
      const pkg = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8')) as { name?: unknown }
      if (pkg.name === 'fugaro-console') return dir
    } catch {
      // Not this directory.
    }
    const up = dirname(dir)
    if (up === dir) throw new RecorderRefusal('cannot find the console package this recorder belongs to')
    dir = up
  }
}

/**
 * writeFixtureFile is the one write this tool performs, done so that nothing
 * but a regular file in this repository's `console/test/fixtures/` can be
 * written:
 *
 *   - the console package is resolved to its real path, and **every component
 *     from there down to the fixtures directory is `lstat`ed and refused if it
 *     is a symbolic link** or not a directory;
 *   - the target's parent must resolve to exactly that directory;
 *   - the target is `lstat`ed: a symbolic link (dangling or not) is refused,
 *     anything that is not a regular file is refused **even with `--force`**,
 *     and an existing regular file is refused without it;
 *   - the text is written to a temporary file in the same directory, created
 *     exclusively (`wx`), and moved into place: `link` for a new fixture
 *     (which fails if something appeared meanwhile), `rename` over an existing
 *     regular one (which replaces the directory entry and never follows it).
 */
export function writeFixtureFile(
  path: string,
  text: string,
  options: { readonly force: boolean; readonly consoleRoot?: string },
): void {
  const root = realpathSync(options.consoleRoot ?? defaultConsoleRoot())
  let dir = root
  for (const part of FIXTURE_DIRECTORY.split('/')) {
    dir = join(dir, part)
    const stats = lstatOrNull(dir)
    if (stats === null) throw new RecorderRefusal(`${dir} does not exist; nothing written`)
    if (stats.isSymbolicLink()) throw new RecorderRefusal(`${dir} is a symbolic link; nothing written`)
    if (!stats.isDirectory()) throw new RecorderRefusal(`${dir} is not a directory; nothing written`)
  }
  const parent = lstatOrNull(dirname(path)) === null ? null : realpathSync(dirname(path))
  if (parent !== dir) {
    throw new RecorderRefusal(`${path} is not in ${FIXTURE_DIRECTORY}/ of this repository's console (${dir})`)
  }
  const target = join(dir, basename(path))
  const existing = lstatOrNull(target)
  if (existing !== null) {
    if (existing.isSymbolicLink()) throw new RecorderRefusal(`${target} is a symbolic link; nothing written`)
    if (!existing.isFile()) throw new RecorderRefusal(`${target} is not a regular file; nothing written`)
    if (!options.force) throw new RecorderRefusal(`${target} exists; pass --force to overwrite a fixture`)
  }
  const temporary = join(dir, `.${basename(target)}.${randomBytes(6).toString('hex')}.tmp`)
  writeFileSync(temporary, text, { flag: 'wx', mode: 0o644 })
  try {
    if (existing === null) {
      linkSync(temporary, target)
      unlinkSync(temporary)
    } else {
      renameSync(temporary, target)
    }
  } catch (error) {
    try {
      unlinkSync(temporary)
    } catch {
      // Already gone.
    }
    throw new RecorderRefusal(
      `${target} could not be written: ${error instanceof Error ? error.message : String(error)}; nothing written`,
    )
  }
}

/** lstatOrNull is `lstatSync`, with "not there" as `null`. */
function lstatOrNull(path: string): Stats | null {
  try {
    return lstatSync(path)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return null
    throw error
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
    options.gcpProject,
    `fugaro-runs-${options.gcpProject}`,
    options.projectNumber,
    ...options.literals,
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
    // The note is free text in the header, and the loader audits the header
    // with the same rules as a document, so it may not spell a path-shaped
    // name: `tools/record-fixture` is an `owner/name` to the repository rule,
    // and a note that tripped a class would refuse the recording this tool had
    // just made.
    note: `recorded on ${stampOf(now)} by the console's own recorder, redacted with the console's own rules; read it before you commit it`,
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
  /** `maxLineLength` bounds one line before it reaches the redactor; the
   * redactor's bound (`MAX_REDACTABLE_LINE_LENGTH`, far below the live feed's)
   * by default: a line the redactor would refuse is refused here, before a
   * single rule has run on it. */
  readonly maxLineLength?: number
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
  const maxLine = deps.maxLineLength ?? MAX_REDACTABLE_LINE_LENGTH
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
  let closeTimer: ReturnType<typeof setTimeout> | null = null
  let settle: () => void = () => {}

  /** finish is what the end of a recording owes the records, before anyone
   * sees them. First the **unterminated last frame**: a child may write its
   * last document with no `\n`, and the recorder cannot tell that from a write
   * cut in half by the signal -- except that a cut-off frame never decodes as a
   * whole JSON object. So a final frame that is a document is recorded (through
   * the redactor, like any other), and anything else is dropped, as a torn
   * write always was. Then **every notice again**: a notice is free text, so
   * the only people the redactor can find in it are the ones the recording's
   * documents named, and a notice may come before the document that names its
   * person. Re-running each one now, with every person in hand, is what keeps a
   * requester's name out of a notice whatever order the CLI wrote them in. A
   * refusal from either is the recording's refusal: nothing is written. */
  const finish = (): void => {
    const tail = partial
    partial = ''
    if (refusal !== null) return
    try {
      if (decodeFeedLine(tail).kind === 'document') {
        const record = recordLine(tail, stampOf(clock()), redactor)
        if (record !== null) records.push(record)
      }
      for (const [index, record] of records.entries()) {
        if ('notice' in record) records[index] = { at: record.at, notice: redactor.redactText(record.notice) }
      }
    } catch (error) {
      refusal = error instanceof Error ? error : new Error(String(error))
    }
  }

  const finished = new Promise<RecordingResult>((resolve) => {
    settle = (): void => {
      if (settled) return
      settled = true
      finish()
      if (durationTimer !== null) timers.clearTimeout(durationTimer)
      if (graceTimer !== null) timers.clearTimeout(graceTimer)
      if (closeTimer !== null) timers.clearTimeout(closeTimer)
      resolve({ records, refusal, exitCode })
    }
  })

  /** awaitClose gives the child's pipes one more grace period to close. The
   * recording settles on `close`, which comes once stdout has drained; this
   * timer is only for a pipe that never closes (a grandchild holding it open),
   * so a recording cannot hang on one. */
  const awaitClose = (): void => {
    if (settled) return
    closeTimer = timers.setTimeout(settle, SHUTDOWN_GRACE_MS)
  }

  /** end stops the child the way the console stops one: SIGTERM, then SIGKILL
   * for a child that will not go. The recording is finished when the child's
   * pipes have closed, not when the signal was sent, and a second `stop()` -- a
   * Ctrl-C on a recording that is already ending -- is not a second signal. A
   * child that cannot be signalled has already exited: its `close` is still to
   * come, unless the recording is a refusal, which writes nothing and so has
   * nothing left to read. */
  const end = (): void => {
    if (settled || ending) return
    ending = true
    if (!child.kill('SIGTERM')) {
      if (refusal !== null) settle()
      else awaitClose()
      return
    }
    graceTimer = timers.setTimeout(() => {
      child.kill('SIGKILL')
      awaitClose()
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
    // Every chunk up to `close` is read; after a refusal there is nothing more
    // to record, because nothing will be written.
    if (settled || refusal !== null) return
    partial += typeof chunk === 'string' ? chunk : chunk.toString('utf8')
    const lines = partial.split('\n')
    partial = lines.pop() ?? ''
    // The buffer is bounded before anything is redacted: a child that writes
    // without a newline would otherwise grow it without limit, and a line the
    // loader would refuse is not one to record.
    if (partial.length > maxLine || lines.some((line) => line.length > maxLine)) {
      partial = ''
      refusal = new RecorderRefusal(`a line longer than a feed line can be (${String(maxLine)} characters); nothing written`)
      end()
      return
    }
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
      if (settled || refusal !== null) return
    }
  })
  child.on('error', (error: Error) => {
    refusal = refusal ?? error
    end()
  })
  // `close`, not `exit`: Node may emit `exit` while the last stdout chunks are
  // still in the pipe, and a recording settled then would drop them. `close`
  // comes once every stdio stream has ended.
  child.on('close', (code: number | null) => {
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
  /** `consoleRoot` is the console package `--out` must be inside; this tool's
   * own package by default. */
  readonly consoleRoot?: string
  readonly appEnv?: Readonly<Record<string, string | undefined>>
}

const NODE_TIMERS: Timers = {
  setTimeout: (handler, ms) => setTimeout(handler, ms),
  clearTimeout: (handle) => {
    clearTimeout(handle)
  },
}

/** argvShape is the child's command with every value taken out: the words
 * before the first flag (`watch`), and each flag's name, never what follows
 * it. The binary is not named at all -- `--binary` is one of the literals. */
function argvShape(argv: readonly string[]): string {
  const words: string[] = ['<binary>']
  const rest = argv.slice(1)
  let flagsBegun = false
  for (const word of rest) {
    if (word.startsWith('--')) {
      flagsBegun = true
      words.push(word.split('=')[0] ?? word)
    } else if (!flagsBegun) {
      words.push(word)
    }
  }
  return words.join(' ')
}

/**
 * main records one fixture and returns 0 -- the exit code of a run that wrote
 * one. A run that wrote nothing throws a `RecorderRefusal` carrying the sentence
 * to print, rather than resolving with a code a caller could take for a finished
 * recording. It is the whole tool, and the one function an owner's run reaches;
 * the entry point at the foot of this file turns those two outcomes into the
 * process's own: the 0 is assigned to `process.exitCode`, and a thrown refusal
 * is printed and exits 2. (The review of Task 4.1 found this docstring claiming
 * `main` itself returned that 2.)
 */
export async function main(argv: readonly string[], deps: RecorderDeps = {}): Promise<number> {
  const cwd = deps.cwd ?? process.cwd()
  const log = deps.log ?? ((line: string): void => void process.stdout.write(`${line}\n`))
  const clock = deps.clock ?? ((): Date => new Date())
  const options = parseRecorderArgs(argv, cwd, {
    ...(deps.exists === undefined ? {} : { exists: deps.exists }),
    ...(deps.consoleRoot === undefined ? {} : { consoleRoot: deps.consoleRoot }),
  })
  const plan = planForRecording(options, deps.appEnv ?? process.env)
  const redactor = createRedactor(machineLiterals(options, deps.appEnv ?? process.env))

  // The child's argv carries `--project <name>` and `--config <path>`, and its
  // cwd is the checkout: the strings `recorderLiterals` promises are never
  // printed. What is said is the shape -- the command and the flags, no value
  // -- so an owner's pasted transcript says what ran without saying where.
  log(`recording ${options.caseName} for ${String(options.durationSeconds)}s`)
  log(`  argv: ${argvShape(plan.argv)} (${String(plan.argv.length)} words; values not printed)`)

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
  const write =
    deps.write ??
    ((path: string, body: string): void => {
      writeFixtureFile(path, body, {
        force: options.force,
        ...(deps.consoleRoot === undefined ? {} : { consoleRoot: deps.consoleRoot }),
      })
    })
  write(options.out, text)
  log(`wrote ${options.out}: ${String(result.records.length)} records`)
  return 0
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
