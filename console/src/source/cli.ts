// The v1 FugaroSource: the installed `fugaro` as a sidecar (design D1, Option A).
//
// This file is the join between the data plane (`src/source/source.ts`) and the
// one child process (`src/main/spawn.ts`). It holds no credential, reads no
// database, reads no config file and knows no field of any document: it resolves
// a binary, builds a plan for each read, runs it, and turns what comes back into
// a `Snapshot`, a `TextSnapshot` or a `SourceRefusal` whose message is the CLI's
// own sentence.
//
// Four things are decided here that are not decided in `spawn.ts`:
//
//   1. **which installation** -- the `SpawnTarget` (project, and the config path
//      when the installation has one) comes from the operator's chosen
//      installation and from nowhere else. There is no fallback to the ambient
//      environment: a Finder-launched app has no project configured, so a source
//      that "defaulted" would read whatever installation the login shell
//      happened to select, which is the defect design section 3.4 rule 3 names.
//   2. **which directory** -- the installation's checkout when it has one, else
//      the private directory `privateWorkingDirectory` owns. Never `/`.
//   3. **`doctor --dir <directory>`, never `--strict`** -- outside a checkout
//      doctor's `plugin-pin` check is a warning, and `--strict` turns that
//      warning into a failure about the checkout rather than about the
//      environment the screen draws.
//   4. **when a refused read becomes which sentence.** The CLI exits 1 and
//      prints `fugaro: <sentence>` for every read it refuses, and that sentence
//      is what a screen shows (design section 6.3). This module does not add a
//      word to it, and does not translate it.
//
// And the last section is the **installation store**, which is where the
// add-an-installation check lives. It is a spawn and not a file read, for rule
// 7's reason: **the app never reads the operator's config; the CLI is the
// reader.** So an installation with a checkout is verified by running one
// allow-listed `doctor --json` with the checkout as the child's working
// directory, and a checkout that belongs to another project is refused with the
// CLI's own `this checkout belongs to project %s; --project says %s`
// (`internal/localcfg/select.go`) -- and **not stored**, because storing it
// would leave every screen refused later, one refusal per command, with nothing
// on screen explaining why.

import {
  MAX_FEED_LINE_LENGTH,
  SPAWN_COMMANDS,
  assertSpawnSurface,
  binDirectories,
  cliSentence,
  decodeFeedLine,
  planFor,
  privateWorkingDirectory,
  resolveBinary,
  runToCompletion,
  startSidecar,
} from '../main/spawn'
import type {
  ChildSpawner,
  JSONObject,
  Notice,
  Sidecar,
  SpawnPlan,
  SpawnTarget,
} from '../main/spawn'
import { createSpawnLog } from '../main/spawnlog'
import type { SpawnLogWriter } from '../main/spawnlog'
import type { OperationName, ValidatedArguments } from '../main/ipc'
import { MAX_TEXT_INPUT_LENGTH } from '../shared/sanitize'
import { SourceRefusal, refusalFrom } from './source'
import type {
  FeedEnd,
  FugaroSource,
  InstallationRef,
  JSONDoc,
  LiveFeed,
  Snapshot,
  TextSnapshot,
  Unsubscribe,
} from './source'

/**
 * FIRST_FRAME_BUDGET_MS is how long `current()` waits for a feed's first
 * document before it gives up and says so. Task 0.2 section 10.1 measures the
 * CLI's own first document at `watchFirstFrame` plus one tick (about six
 * seconds), so this is generously more than twice that: it exists so a screen
 * that asked for a snapshot is *told* something rather than left waiting, and it
 * is not a claim about how fast the CLI is.
 */
export const FIRST_FRAME_BUDGET_MS = 15_000

/**
 * MAX_LIVE_FEEDS is how many `fugaro watch` children one source keeps at once.
 * A subscription is keyed by its argv, so every distinct valid repository is a
 * distinct child; without a ceiling a renderer that walked through repositories
 * would leave one process per name running until quit. Past this many, the
 * least recently used feed is stopped (its child gets `SIGTERM`) and dropped.
 */
export const MAX_LIVE_FEEDS = 4

/**
 * FEED_STALE_AFTER_SECONDS is `watch.StaleAfter` (`internal/watch/state.go`):
 * the per-stream silence, keep-alives counting as events, after which the CLI's
 * own `connection.state` becomes `stale`. It is the console's number too --
 * design section 6.3's stale banner is the same 45 s, because a console that
 * bannered at its own threshold would be telling the operator about a silence
 * the CLI it is reading does not call one. It is stated here, beside the code
 * that measures the silence off the source's own clock (§10.11 item 2: the
 * child does not exit on a dead stream, so the console needs its own staleness
 * clock), and `live.ts`'s `LIVE_STALE_AFTER_SECONDS` is the screen's copy of
 * the same fact: `degraded.spec.ts` asserts the two cannot drift.
 */
export const FEED_STALE_AFTER_SECONDS = 45

/**
 * FeedSilence is what the console knows about how long the feed has been quiet:
 * the stamp of the newest frame it holds, the console's own clock when it
 * looked, the seconds between them, and whether that silence is a stale one.
 *
 * It is deliberately about **arrival**, not about the document's own `now`: a
 * frame that arrived two seconds ago and says the day rolled is not silence,
 * and a frame that has not arrived at all is silence no matter what the last one
 * claimed. `seconds` is `null` -- not `0` -- when no frame has arrived, which is
 * the availability rule every number on this console follows: a console that has
 * heard nothing has not measured a silence of zero seconds.
 */
export interface FeedSilence {
  /** the arrival stamp of the newest frame, or `null` when there is none */
  readonly since: string | null
  /** the console's own clock at the moment of the reading */
  readonly at: string
  /** whole seconds between `since` and `at`, or `null` with no frame */
  readonly seconds: number | null
  /** whether `seconds` is past `FEED_STALE_AFTER_SECONDS` */
  readonly stale: boolean
}

/**
 * feedSilence measures a feed's quiet. `latest` is the newest thing the console
 * received -- a `Snapshot`, a `TextSnapshot` or a `Notice`, all of which carry
 * `receivedAt` -- and `now` is the console's clock, passed in rather than read
 * here so that a test can hold a feed to an exact silence instead of to a
 * machine's speed.
 *
 * `at` is the clock reading in the same shape `receivedAt` uses (whole seconds,
 * `Z`), so the two stamps can be compared and drawn together without a second
 * formatter.
 */
export function feedSilence(
  latest: { readonly receivedAt: string } | null,
  now: Date,
): FeedSilence {
  const at = now.toISOString().replace(/\.\d{3}Z$/, 'Z')
  if (latest === null) {
    return { since: null, at, seconds: null, stale: false }
  }
  const arrived = Date.parse(latest.receivedAt)
  const measured = Date.parse(at)
  // A stamp the console itself wrote is always parseable; a frame's own stamp is
  // not used here at all. An unreadable one is the same fact as no frame: the
  // console cannot say how long it has been quiet, so it says `null`.
  const seconds = Number.isNaN(arrived) || Number.isNaN(measured) ? null : Math.max(0, Math.round((measured - arrived) / 1000))
  return {
    since: latest.receivedAt,
    at,
    seconds,
    stale: seconds !== null && seconds > FEED_STALE_AFTER_SECONDS,
  }
}

/**
 * FUGARO_INSTALL_COMMAND is the one line the absent-binary screen offers, and
 * it is the README's own command rather than one this file invented:
 * `console/README.md` and `docs/release.md` both hand the cask as the install
 * step (`brew install dimipaun/tap/fugaro`). The console prints it and runs
 * nothing -- installing a binary is the operator's act, not the app's.
 */
export const FUGARO_INSTALL_COMMAND = 'brew install dimipaun/tap/fugaro'

/**
 * NoInstallationState is the console's state when there is no `fugaro` to read:
 * everything design section 6.3's first screen needs, with no document in it.
 * `renderer/screens/degraded.ts` draws it, and `degraded.spec.ts` holds one
 * built here to the renderer's own type, so the two cannot drift.
 */
export interface NoInstallationState {
  readonly kind: 'no-binary'
  /** the resolution's own sentence, kept as the CLI-less reason it is */
  readonly reason: string
  readonly installCommand: string
  /** every candidate the resolution looked at, in the order it looked */
  readonly searched: readonly string[]
  /** the path the installation named, when it named one */
  readonly configuredPath: string | null
}

/**
 * NoInstallation is thrown instead of a bare `SourceRefusal` when **binary
 * resolution** failed -- the operator has no `fugaro`, or the one they have
 * cannot say which version it is. It is still a `SourceRefusal` (so nothing that
 * catches one changes behaviour) carrying the resolution's own sentence, and it
 * adds the state a screen needs: this is not a refused read, it is a missing
 * program, and the screen that follows says what to install rather than which
 * read failed.
 */
export class NoInstallation extends SourceRefusal {
  override readonly name = 'NoInstallation'
  readonly state: NoInstallationState

  constructor(state: NoInstallationState) {
    super(state.reason)
    this.state = state
  }
}

/**
 * noInstallationState is the state, built from the resolution that failed. The
 * candidate list is re-derived from the same `binDirectories` the resolution
 * walked, with the same options -- so the screen names the directories this
 * machine would actually be searched in, not a list written down twice.
 */
export function noInstallationState(options: {
  readonly reason: string
  readonly configuredPath?: string | undefined
  readonly appEnv?: Readonly<Record<string, string | undefined>>
  readonly home?: string
}): NoInstallationState {
  return {
    kind: 'no-binary',
    reason: options.reason,
    installCommand: FUGARO_INSTALL_COMMAND,
    searched: binDirectories({
      ...(options.configuredPath === undefined ? {} : { configuredPath: options.configuredPath }),
      appEnv: options.appEnv ?? {},
      ...(options.home === undefined ? {} : { home: options.home }),
    }),
    configuredPath: options.configuredPath === undefined || options.configuredPath === '' ? null : options.configuredPath,
  }
}

/** CliSourceOptions is everything a v1 source is built from. `appEnv` is the
 * app's own environment, used for `HOME`, `TMPDIR`, `LANG`/`LC_ALL`, the five
 * `XDG_*` variables and binary resolution -- and for nothing else, because
 * `childEnvironment` reads no other key. */
export interface CliSourceOptions {
  readonly target: SpawnTarget
  readonly configuredBinaryPath?: string | undefined
  readonly checkout?: string | undefined
  readonly appEnv?: Readonly<Record<string, string | undefined>>
  readonly spawner?: ChildSpawner
  readonly spawnLog?: SpawnLogWriter
  readonly home?: string
  readonly clock?: () => Date
  readonly firstFrameBudgetMs?: number
}

/**
 * CliSource is a `FugaroSource` and a little more: `installation` is the resolved
 * header, and `close` stops the feeds. The eleven reads are one-liners over
 * `readDocument` and `readText`.
 */
export class CliSource implements FugaroSource {
  readonly installation: InstallationRef

  readonly #options: CliSourceOptions
  readonly #env: Readonly<Record<string, string | undefined>>
  readonly #directory: string
  readonly #log: SpawnLogWriter
  /** `#ownsLog` is whether `create` opened the log. A log the caller injected
   * is the caller's to close, as it is in `verifyCandidate`. */
  readonly #ownsLog: boolean
  readonly #clock: () => Date
  readonly #binaryPath: string
  /** The live feeds, keyed by the argv that produced them: one child per
   * subscription, not one per call. See `subscribeLive`. */
  readonly #feeds = new Map<string, LiveStream>()

  private constructor(options: CliSourceOptions, bucket: ResolvedSource) {
    this.#options = options
    this.#env = options.appEnv ?? {}
    this.#directory = bucket.directory
    this.#log = bucket.log
    this.#ownsLog = bucket.ownsLog
    this.#clock = options.clock ?? ((): Date => new Date())
    this.#binaryPath = bucket.binaryPath
    this.installation = bucket.installation
  }

  /** create resolves the binary once, at startup, and refuses to build a source
   * around a binary whose version cannot be read. */
  static async create(options: CliSourceOptions): Promise<CliSource> {
    // Fail closed before anything is spawned: a spawn table that has drifted from
    // the IPC surface (or that drives something on the never table) is a console
    // that must not start.
    assertSpawnSurface(SPAWN_COMMANDS)

    const env = options.appEnv ?? {}
    const directory = options.checkout ?? privateWorkingDirectory()
    const log = options.spawnLog ?? createSpawnLog(env)
    const ownsLog = log !== options.spawnLog
    let resolution: Awaited<ReturnType<typeof resolveBinary>>
    try {
      resolution = await resolveBinary({
        ...(options.configuredBinaryPath === undefined ? {} : { configuredPath: options.configuredBinaryPath }),
        appEnv: env,
        directory,
        target: options.target,
        ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
        ...(options.home === undefined ? {} : { home: options.home }),
        log,
      })
    } catch (error) {
      if (ownsLog) log.close()
      throw error
    }
    if (!resolution.ok) {
      if (ownsLog) log.close()
      throw new NoInstallation(
        noInstallationState({
          reason: resolution.reason,
          configuredPath: options.configuredBinaryPath,
          appEnv: env,
          ...(options.home === undefined ? {} : { home: options.home }),
        }),
      )
    }

    const installation: InstallationRef = {
      binaryPath: resolution.path,
      version: resolution.version,
      project: options.target.project,
      // Passed through, not re-derived: `resolution.source` is already the
      // installation's own `source` vocabulary, so there is no second spelling
      // of it here for a later edit to disagree with.
      source: resolution.source,
      ...(options.checkout === undefined ? {} : { checkout: options.checkout }),
    }
    return new CliSource(options, {
      directory,
      log,
      ownsLog,
      binaryPath: resolution.path,
      installation,
    })
  }

  #plan(operation: OperationName, args: ValidatedArguments): SpawnPlan {
    return planFor(operation, args, {
      target: this.#options.target,
      binaryPath: this.#binaryPath,
      directory: this.#directory,
      appEnv: this.#env,
    })
  }

  /** readDocument runs one one-shot read and returns the document it printed. */
  async #readDocument(operation: OperationName, args: ValidatedArguments): Promise<Snapshot> {
    const plan = this.#plan(operation, args)
    const result = await runToCompletion(plan, {
      log: this.#log,
      ...(this.#options.spawner === undefined ? {} : { spawner: this.#options.spawner }),
    })
    if (result.code !== 0) {
      throw refusalFrom(operation, cliSentence(result.stderr))
    }
    const read = parseStdout(result.stdout, this.#stamp())
    if (read.document === null) {
      throw refusalFrom(operation, cliSentence(result.stderr) ?? `${operation} printed no JSON document`)
    }
    return {
      document: read.document,
      receivedAt: this.#stamp(),
      sequence: 1,
      day: typeof read.document['day'] === 'string' ? read.document['day'] : null,
      rolled: false,
      notices: read.notices,
    }
  }

  /** readText runs one one-shot read whose answer is a sentence, which is
   * `run.logLink` alone: `logs RUN --url` prints the link and nothing else. */
  async #readText(operation: OperationName, args: ValidatedArguments): Promise<TextSnapshot> {
    const plan = this.#plan(operation, args)
    const result = await runToCompletion(plan, {
      log: this.#log,
      ...(this.#options.spawner === undefined ? {} : { spawner: this.#options.spawner }),
    })
    if (result.code !== 0) throw refusalFrom(operation, cliSentence(result.stderr))
    return { text: result.stdout.trim(), receivedAt: this.#stamp() }
  }

  #stamp(): string {
    return this.#clock().toISOString().replace(/\.\d{3}Z$/, 'Z')
  }

  version(): Promise<TextSnapshot> {
    return this.#readText('cli.version', {})
  }

  /**
   * subscribeLive is **idempotent for the same arguments while a feed is live**
   * (the contract `FugaroSource.subscribeLive` states). The surface is
   * `invoke`-only, so the Live screen reaches `live.watch` once per poll; a
   * stream per call would be a `fugaro watch` child per poll, and nothing would
   * ever stop them until `close()`.
   *
   * The key is the argv, because the argv is what decides which child this is:
   * two calls that plan the same argv are the same subscription, and two that do
   * not are two -- a second repository is legitimately a second `watch`. A feed
   * that has *ended* is removed, so the call after a refusal starts a fresh
   * child rather than handing back a dead stream.
   *
   * At most `MAX_LIVE_FEEDS` feeds are kept: the map is in recency order (a hit
   * moves its entry to the end), and a new subscription past the ceiling stops
   * the oldest one first.
   */
  subscribeLive(args: ValidatedArguments): LiveFeed {
    const plan = this.#plan('live.watch', args)
    const key = plan.argv.join('\u0000')
    const live = this.#feeds.get(key)
    if (live !== undefined) {
      this.#feeds.delete(key)
      this.#feeds.set(key, live)
      return live
    }
    while (this.#feeds.size >= MAX_LIVE_FEEDS) {
      const oldest = this.#feeds.entries().next().value
      if (oldest === undefined) break
      const [oldestKey, oldestStream] = oldest
      this.#feeds.delete(oldestKey)
      void oldestStream.stop()
    }

    const stream = new LiveStream(plan, {
      ...(this.#options.spawner === undefined ? {} : { spawner: this.#options.spawner }),
      log: this.#log,
      clock: this.#clock,
      firstFrameBudgetMs: this.#options.firstFrameBudgetMs ?? FIRST_FRAME_BUDGET_MS,
    })
    this.#feeds.set(key, stream)
    // The listener is what makes the map mean "live": it is also called
    // immediately for a feed that has already ended, which is why the entry is
    // only removed when it is still this stream's.
    stream.onEnded(() => {
      if (this.#feeds.get(key) === stream) this.#feeds.delete(key)
    })
    return stream
  }

  showBudget(args: ValidatedArguments): Promise<Snapshot> {
    return this.#readDocument('budget.show', args)
  }

  runReport(args: ValidatedArguments): Promise<Snapshot> {
    return this.#readDocument('spend.report', args)
  }

  listRuns(args: ValidatedArguments): Promise<Snapshot> {
    return this.#readDocument('runs.list', args)
  }

  diagnoseRun(args: ValidatedArguments): Promise<Snapshot> {
    return this.#readDocument('run.diagnose', args)
  }

  /** readRunLogs reads `logs RUN --json`, which is line-delimited -- one
   * `logLine` per entry, not one document -- into the capped window
   * `parseLogEntries` builds. A run with no entries is an empty window, not a
   * refusal: the CLI exits 0 and prints nothing on stdout for it. */
  async readRunLogs(args: ValidatedArguments): Promise<Snapshot> {
    const operation: OperationName = 'run.logs'
    const plan = this.#plan(operation, args)
    const result = await runToCompletion(plan, {
      log: this.#log,
      ...(this.#options.spawner === undefined ? {} : { spawner: this.#options.spawner }),
    })
    if (result.code !== 0) throw refusalFrom(operation, cliSentence(result.stderr))
    const window = parseLogEntries(result.stdout)
    const receivedAt = this.#stamp()
    const hint = cliSentence(result.stderr)
    return {
      document: { entries: [...window.entries], total: window.total, refused: window.refused },
      receivedAt,
      sequence: 1,
      day: null,
      rolled: false,
      notices: hint === null ? [] : [{ text: hint, receivedAt }],
    }
  }

  runLogLink(args: ValidatedArguments): Promise<TextSnapshot> {
    return this.#readText('run.logLink', args)
  }

  listSecrets(args: ValidatedArguments): Promise<Snapshot> {
    return this.#readDocument('secrets.inventory', args)
  }

  listAccounts(args: ValidatedArguments): Promise<Snapshot> {
    return this.#readDocument('accounts.inventory', args)
  }

  environmentReport(args: ValidatedArguments): Promise<Snapshot> {
    // The one one-shot read that carries a `--dir` (the installation's directory
    // or the private one), because doctor's checks are about the checkout it is
    // pointed at.
    return this.#readDocument('environment.report', args)
  }

  async close(): Promise<void> {
    const feeds = [...this.#feeds.values()]
    this.#feeds.clear()
    await Promise.all(feeds.map((feed) => feed.stop()))
    if (this.#ownsLog) this.#log.close()
  }
}

/** ResolvedSource is what `CliSource.create` learned before it had an object. */
interface ResolvedSource {
  readonly directory: string
  readonly log: SpawnLogWriter
  readonly ownsLog: boolean
  readonly binaryPath: string
  readonly installation: InstallationRef
}

/**
 * parseStdout reads a one-shot read's stdout. A document that a human sentence
 * accompanies is still a document -- the CLI writes its degraded-mode notices to
 * stdout on the same stream -- so the lines are decoded one by one and **the last
 * JSON object wins**, with every line that was not one kept as a notice. A read
 * whose stdout holds no JSON object at all answers `document: null`, which the
 * caller turns into a refusal rather than an empty screen.
 */
export function parseStdout(
  stdout: string,
  receivedAt = '',
): { readonly document: JSONObject | null; readonly notices: readonly Notice[] } {
  const notices: Notice[] = []
  let document: JSONObject | null = null
  for (const line of stdout.split('\n')) {
    const decoded = decodeFeedLine(line)
    if (decoded.kind === 'document') document = decoded.document
    else if (decoded.kind === 'notice') notices.push({ text: decoded.text, receivedAt })
  }
  return { document, notices }
}

/**
 * LOG_WINDOW_MAX_ENTRIES is how many `logLine` entries the log read keeps: the
 * newest ones, which is the Run screen's own cap (`RUN_LOG_CAP`,
 * `src/renderer/screens/run.ts`; `spawn.test.ts` holds the two equal). The main
 * process keeps no more than the screen can draw, and says how many it read.
 */
export const LOG_WINDOW_MAX_ENTRIES = 200

/** LOG_REQUIRED_FIELDS and LOG_OPTIONAL_FIELDS are `logLine`'s keys
 * (`internal/cli/logs.go:33`): `time`, `severity` and `message` carry no
 * `omitempty`, the other three do. No other key is kept. */
const LOG_REQUIRED_FIELDS = ['time', 'severity', 'message'] as const
const LOG_OPTIONAL_FIELDS = ['stage', 'stream', 'event'] as const

/** LogEntries is the capped window: the newest entries, oldest first, how many
 * well-shaped entries the read held in all, and how many lines it refused. */
export interface LogEntries {
  readonly entries: readonly JSONObject[]
  readonly total: number
  readonly refused: number
}

/**
 * parseLogEntries reads `logs RUN --json`'s stdout: one compact `logLine`
 * object per line (contract section 6.2), so **every** line is an entry -- not
 * the last one, which is the rule for the indented documents (`parseStdout`).
 *
 * Each line is held to the entry's shape before it is kept: a line past the
 * feed's line cap is refused without being parsed, a torn or non-object line is
 * refused, a required field that is missing or not a string is refused, and a
 * string past the sanitizer's own input bound (`MAX_TEXT_INPUT_LENGTH`) is
 * refused. A refused line is counted, never dropped silently, and never echoed.
 * What is kept is still text from a job: the Run screen draws every field of it
 * through `putText`. Only the newest `maxEntries` are kept.
 */
export function parseLogEntries(
  stdout: string,
  limits: { readonly maxLineLength?: number; readonly maxEntries?: number } = {},
): LogEntries {
  const maxLine = limits.maxLineLength ?? MAX_FEED_LINE_LENGTH
  const maxEntries = limits.maxEntries ?? LOG_WINDOW_MAX_ENTRIES
  const entries: JSONObject[] = []
  let total = 0
  let refused = 0
  for (const raw of stdout.split('\n')) {
    const line = raw.replace(/\r$/, '')
    if (line.trim() === '') continue
    if (line.length > maxLine) {
      refused += 1
      continue
    }
    const decoded = decodeFeedLine(line)
    const entry = decoded.kind === 'document' ? logEntryOf(decoded.document) : null
    if (entry === null) {
      refused += 1
      continue
    }
    total += 1
    entries.push(entry)
    if (entries.length > maxEntries) entries.shift()
  }
  return { entries, total, refused }
}

/** logEntryOf is one `logLine` held to its shape, with only its own keys, or
 * null when it is not one. */
function logEntryOf(document: JSONObject): JSONObject | null {
  const entry: Record<string, string> = {}
  for (const key of LOG_REQUIRED_FIELDS) {
    const value = document[key]
    if (typeof value !== 'string' || value.length > MAX_TEXT_INPUT_LENGTH) return null
    entry[key] = value
  }
  for (const key of LOG_OPTIONAL_FIELDS) {
    const value = document[key]
    if (value === undefined) continue
    if (typeof value !== 'string' || value.length > MAX_TEXT_INPUT_LENGTH) return null
    entry[key] = value
  }
  return entry as JSONObject
}

/**
 * LiveStream is `LiveFeed` over a supervised child: the sidecar's frames become
 * snapshots, its notices accumulate onto the next snapshot (so a degraded mode's
 * sentence travels with the document a screen draws), and an end that is not a
 * stop is a refusal the screen can show.
 */
/** LiveStreamDeps is what a feed is built from: the supervisor's own four
 * arguments plus the first-frame budget `current()` uses. */
interface LiveStreamDeps {
  readonly spawner?: ChildSpawner | undefined
  readonly log?: SpawnLogWriter | undefined
  readonly clock?: (() => Date) | undefined
  readonly firstFrameBudgetMs?: number | undefined
}

class LiveStream implements LiveFeed {
  #latest: Snapshot | null = null
  #notices: Notice[] = []
  #snapshots = 0
  #ended: FeedEnd | null = null
  readonly #sidecar: Sidecar
  readonly #budget: number
  readonly #change = new Set<(snapshot: Snapshot) => void>()
  readonly #noticeListeners = new Set<(notice: Notice) => void>()
  readonly #endedListeners = new Set<(end: FeedEnd) => void>()
  /** #waiting is *every* `current()` call that is waiting for a first frame,
   * not one slot. `handlersFor`'s `live.watch` calls `current()` on every
   * invocation and a screen reaches that once per poll, so two polls can
   * overlap; a single slot would silently replace the first waiter, whose own
   * budget timer would then reject it -- 'no document arrived within Ns' -- on a
   * feed that is delivering documents the whole time. */
  readonly #waiting = new Set<{
    readonly resolve: (snapshot: Snapshot) => void
    readonly reject: (error: Error) => void
  }>()

  constructor(plan: SpawnPlan, deps: LiveStreamDeps) {
    this.#budget = deps.firstFrameBudgetMs ?? FIRST_FRAME_BUDGET_MS
    this.#sidecar = startSidecar(plan, {
      onFrame: (frame) => {
        this.#snapshots += 1
        const snapshot: Snapshot = {
          document: frame.document as JSONDoc,
          receivedAt: frame.receivedAt,
          sequence: frame.sequence,
          day: frame.day,
          rolled: frame.rolled,
          notices: this.#notices,
        }
        this.#notices = []
        this.#latest = snapshot
        for (const listener of [...this.#change]) listener(snapshot)
        // Every waiter gets this frame: they all asked the same question of the
        // same feed, so they all get the same answer.
        const waiting = [...this.#waiting]
        this.#waiting.clear()
        for (const waiter of waiting) waiter.resolve(snapshot)
      },
      onNotice: (notice) => {
        this.#notices = [...this.#notices, notice]
        for (const listener of [...this.#noticeListeners]) listener(notice)
      },
      onRestart: () => undefined,
      onEnded: (end) => {
        const finished: FeedEnd = {
          reason: end.reason,
          sentence: end.sentence,
          snapshots: this.#snapshots,
        }
        this.#ended = finished
        const waiting = [...this.#waiting]
        this.#waiting.clear()
        for (const waiter of waiting) {
          waiter.reject(refusalFrom('watch', end.sentence ?? 'the live feed ended before its first document'))
        }
        for (const listener of [...this.#endedListeners]) listener(finished)
      },
    }, {
      ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
      ...(deps.log === undefined ? {} : { log: deps.log }),
      ...(deps.clock === undefined ? {} : { clock: deps.clock }),
    })
  }

  get latest(): Snapshot | null {
    return this.#latest
  }

  onChange(listener: (snapshot: Snapshot) => void): Unsubscribe {
    this.#change.add(listener)
    return () => {
      this.#change.delete(listener)
    }
  }

  onNotice(listener: (notice: Notice) => void): Unsubscribe {
    this.#noticeListeners.add(listener)
    return () => {
      this.#noticeListeners.delete(listener)
    }
  }

  onEnded(listener: (end: FeedEnd) => void): Unsubscribe {
    this.#endedListeners.add(listener)
    if (this.#ended !== null) listener(this.#ended)
    return () => {
      this.#endedListeners.delete(listener)
    }
  }

  /** current answers with the frame the feed has -- immediately when there is
   * one, and otherwise as soon as the first arrives. A feed that ends first, or
   * that says nothing within the budget, is a refusal rather than a promise that
   * never settles: a screen waiting forever is the one outcome nothing on screen
   * can describe. Overlapping calls are all answered by the same frame, and each
   * one's own budget timer is cleared by whichever answer it gets, so a call
   * that is answered late is not also refused by its own timer. */
  current(): Promise<Snapshot> {
    if (this.#latest !== null) return Promise.resolve(this.#latest)
    if (this.#ended !== null) {
      return Promise.reject(
        refusalFrom('watch', this.#ended.sentence ?? 'the live feed ended before its first document'),
      )
    }
    return new Promise<Snapshot>((resolve, reject) => {
      let timer: ReturnType<typeof setTimeout> | null = null
      const waiter = {
        resolve: (snapshot: Snapshot) => {
          if (timer !== null) clearTimeout(timer)
          resolve(snapshot)
        },
        reject: (error: Error) => {
          if (timer !== null) clearTimeout(timer)
          reject(error)
        },
      }
      timer = setTimeout(() => {
        this.#waiting.delete(waiter)
        reject(refusalFrom('watch', `no document arrived within ${Math.round(this.#budget / 1000)}s`))
      }, this.#budget)
      this.#waiting.add(waiter)
    })
  }

  stop(): Promise<void> {
    return this.#sidecar.stop().then(() => undefined)
  }
}

// ---------------------------------------------------------------------------
// The installation store.
// ---------------------------------------------------------------------------

/**
 * InstallationCandidate is one installation as the operator entered it: the
 * project name, the config path when it is not the ambient one, the binary when
 * they chose one, and the checkout when they picked a directory. It is the D13
 * tuple minus the binary path and version the source resolves itself.
 */
export interface InstallationCandidate {
  readonly project: string
  readonly configPath?: string | undefined
  readonly binaryPath?: string | undefined
  readonly checkout?: string | undefined
}

/** InstallationStore is the in-memory list the app holds for one run. */
export interface InstallationStore {
  list(): readonly InstallationCandidate[]
  /** add verifies the candidate with one allow-listed read and stores it, or
   * throws the CLI's own refusal and stores nothing. */
  add(candidate: InstallationCandidate): Promise<InstallationCandidate>
}

/**
 * createInstallationStore returns the store. **It is in memory on purpose**: the
 * console writes nothing but the spawn log (design section 5.1 rule 10), so the
 * installation list does not survive the process, and a later task that persists
 * it will have to reconcile that with rule 10 explicitly rather than growing it
 * by accident. What the store does do is the check that matters: an installation
 * is verified *before* it is stored.
 */
export function createInstallationStore(deps: {
  readonly appEnv?: Readonly<Record<string, string | undefined>>
  readonly spawner?: ChildSpawner
  readonly spawnLog?: SpawnLogWriter
}): InstallationStore {
  const stored: InstallationCandidate[] = []
  const env = deps.appEnv ?? {}

  return {
    list: () => [...stored],
    async add(candidate: InstallationCandidate): Promise<InstallationCandidate> {
      if (candidate.project === '') throw new SourceRefusal('an installation needs a project name')
      assertSpawnSurface(SPAWN_COMMANDS)
      await verifyCandidate(candidate, deps)
      stored.push(candidate)
      return candidate
    },
  }
}

/**
 * verifyCandidate runs the one allow-listed read that proves an installation is
 * usable: `doctor --json`, with the installation's `--project`, its `--config`
 * when it has one, and **the checkout as the child's working directory** (which
 * is also the `--dir` it is given). Nothing here opens `fugaro.yaml`: the CLI
 * reads it, and the CLI's refusal -- `this checkout belongs to project %s;
 * --project says %s` -- is the sentence the screen shows.
 */
export async function verifyCandidate(
  candidate: InstallationCandidate,
  deps: {
    readonly appEnv?: Readonly<Record<string, string | undefined>>
    readonly spawner?: ChildSpawner
    readonly spawnLog?: SpawnLogWriter
    readonly home?: string
  },
): Promise<void> {
  const env = deps.appEnv ?? {}
  const directory = candidate.checkout ?? privateWorkingDirectory()
  // A log this function opened is this function's to close; one it was handed
  // is the caller's. Without the `finally`, each installation added held one
  // descriptor until the process exited (review round 2 of PR 478).
  const log = deps.spawnLog ?? createSpawnLog(env)
  try {
    await verifyWith(candidate, deps, env, directory, log)
  } finally {
    if (log !== deps.spawnLog) log.close()
  }
}

/** verifyWith is `verifyCandidate`'s body, with the log already chosen. */
async function verifyWith(
  candidate: InstallationCandidate,
  deps: { readonly spawner?: ChildSpawner; readonly home?: string },
  env: Readonly<Record<string, string | undefined>>,
  directory: string,
  log: SpawnLogWriter,
): Promise<void> {
  const resolution = await resolveBinary({
    ...(candidate.binaryPath === undefined ? {} : { configuredPath: candidate.binaryPath }),
    appEnv: env,
    directory,
    target: { project: candidate.project, ...(candidate.configPath === undefined ? {} : { configPath: candidate.configPath }) },
    ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
    ...(deps.home === undefined ? {} : { home: deps.home }),
    log,
  })
  if (!resolution.ok) throw new SourceRefusal(resolution.reason)

  const plan = planFor('environment.report', {}, {
    target: {
      project: candidate.project,
      ...(candidate.configPath === undefined ? {} : { configPath: candidate.configPath }),
    },
    binaryPath: resolution.path,
    directory,
    appEnv: env,
  })
  const result = await runToCompletion(plan, {
    log,
    ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
  })
  if (result.code !== 0) {
    throw refusalFrom('doctor', cliSentence(result.stderr))
  }
  if (parseStdout(result.stdout).document === null) {
    throw refusalFrom('doctor', cliSentence(result.stderr) ?? 'doctor printed no JSON document')
  }
}
