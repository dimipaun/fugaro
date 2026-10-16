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
  SPAWN_COMMANDS,
  assertSpawnSurface,
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
  readonly #clock: () => Date
  readonly #binaryPath: string
  readonly #feeds = new Set<LiveStream>()

  private constructor(options: CliSourceOptions, bucket: ResolvedSource) {
    this.#options = options
    this.#env = options.appEnv ?? {}
    this.#directory = bucket.directory
    this.#log = bucket.log
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
    const resolution = await resolveBinary({
      ...(options.configuredBinaryPath === undefined ? {} : { configuredPath: options.configuredBinaryPath }),
      appEnv: env,
      directory,
      target: options.target,
      ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
      ...(options.home === undefined ? {} : { home: options.home }),
      log,
    })
    if (!resolution.ok) throw new SourceRefusal(resolution.reason)

    const installation: InstallationRef = {
      binaryPath: resolution.path,
      version: resolution.version,
      project: options.target.project,
      source: resolution.source === 'configured' ? 'configured' : 'well-known',
      ...(options.checkout === undefined ? {} : { checkout: options.checkout }),
    }
    return new CliSource(options, {
      directory,
      log,
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

  subscribeLive(args: ValidatedArguments): LiveFeed {
    const stream = new LiveStream(this.#plan('live.watch', args), {
      ...(this.#options.spawner === undefined ? {} : { spawner: this.#options.spawner }),
      log: this.#log,
      clock: this.#clock,
      firstFrameBudgetMs: this.#options.firstFrameBudgetMs ?? FIRST_FRAME_BUDGET_MS,
    })
    this.#feeds.add(stream)
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

  readRunLogs(args: ValidatedArguments): Promise<Snapshot> {
    return this.#readDocument('run.logs', args)
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
    const feeds = [...this.#feeds]
    this.#feeds.clear()
    await Promise.all(feeds.map((feed) => feed.stop()))
    this.#log.close()
  }
}

/** ResolvedSource is what `CliSource.create` learned before it had an object. */
interface ResolvedSource {
  readonly directory: string
  readonly log: SpawnLogWriter
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
  #firstFrame: { readonly resolve: (snapshot: Snapshot) => void; readonly reject: (error: Error) => void } | null =
    null

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
        const waiting = this.#firstFrame
        this.#firstFrame = null
        waiting?.resolve(snapshot)
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
        const waiting = this.#firstFrame
        this.#firstFrame = null
        if (waiting !== null) {
          waiting.reject(refusalFrom('watch', end.sentence ?? 'the live feed ended before its first document'))
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
   * can describe. */
  current(): Promise<Snapshot> {
    if (this.#latest !== null) return Promise.resolve(this.#latest)
    if (this.#ended !== null) {
      return Promise.reject(
        refusalFrom('watch', this.#ended.sentence ?? 'the live feed ended before its first document'),
      )
    }
    return new Promise<Snapshot>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.#firstFrame = null
        reject(refusalFrom('watch', `no document arrived within ${Math.round(this.#budget / 1000)}s`))
      }, this.#budget)
      this.#firstFrame = {
        resolve: (snapshot) => {
          clearTimeout(timer)
          resolve(snapshot)
        },
        reject: (error) => {
          clearTimeout(timer)
          reject(error)
        },
      }
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
  const log = deps.spawnLog ?? createSpawnLog(env)
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
