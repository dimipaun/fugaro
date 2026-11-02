// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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
  resolveInstalledBinary,
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
import { SourceRefusal, refusalFrom, NO_PLAN_CHECKOUT } from './source'
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
 * PLAN_STATUS_MIN_INTERVAL_MS is how long `plan.status` serves the document it
 * already read before it runs the child again: the dependency-graph plan's
 * GV2.1 fixes the refresh at "on demand, at most once a minute, never per watch
 * frame". The read is the most expensive one this surface has -- it walks a
 * repository's `docs/` and joins the runs -- and a screen that redraws it on
 * every live frame would run it several times a minute for a graph that changes
 * when a plan file changes.
 *
 * The cached answer is the snapshot the read actually produced, stamped with the
 * console clock of that read, and not a re-stamp: a screen that showed a fresh
 * time over a minute-old document would be inventing a reading. A call inside
 * the interval answers that document; a call outside it runs the child.
 */
export const PLAN_STATUS_MIN_INTERVAL_MS = 60_000

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
 * machine would actually be searched in, not a list written down twice. The
 * configured path is an *input* and not a field: it is one of those candidates,
 * and the screen draws the count of them and never a path.
 */
export function noInstallationState(options: {
  readonly reason: string
  readonly configuredPath?: string | undefined
  readonly home?: string
}): NoInstallationState {
  return {
    kind: 'no-binary',
    reason: options.reason,
    installCommand: FUGARO_INSTALL_COMMAND,
    searched: binDirectories({
      ...(options.configuredPath === undefined ? {} : { configuredPath: options.configuredPath }),
      ...(options.home === undefined ? {} : { home: options.home }),
    }),
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
 * header, and `close` stops the feeds. The thirteen reads are one-liners over
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

  /** statsRuns reads `runs stats --json`: one indented document, like every
   * other one-shot read (`console/docs/json-contract.md` section 9.2 -- the two
   * line-delimited ones are `watch --json` and `logs RUN --json`). */
  statsRuns(args: ValidatedArguments): Promise<Snapshot> {
    return this.#readDocument('runs.stats', args)
  }

  /** planStatus reads `plan status --json`, which is the one read whose answer
   * depends on **where** the child runs: the CLI walks `docs/` from its working
   * directory (`internal/cli/plan_status.go`), and that directory is this
   * source's checkout (`options.checkout`, rule 5). No argv this method builds
   * names a path -- the row in `src/main/spawn.ts` has no `--dir` and no
   * positional word -- so the document is about the installation's own
   * repository or it is a refusal. */
  planStatus(args: ValidatedArguments): Promise<Snapshot> {
    return this.#readDocument('plan.status', args)
  }

  /** coordinatorStatus reads `coordinator status --from-store --all --json`,
   * which is the one read here that touches nothing on this machine and no
   * credential: the child reads the caller's own database node through
   * `roles/firebasedatabase.viewer` and prints it. It is therefore the only read
   * with **no precondition of its own** -- no checkout, no App key, no
   * Bitbucket credential -- and the two flags that make it that are words of the
   * spawn row, so no caller can turn either off. */
  coordinatorStatus(args: ValidatedArguments): Promise<Snapshot> {
    return this.#readDocument('coordinator.status', args)
  }

  /** coordinatorShow reads `coordinator show --json --repo <owner/name> --pr
   * <n>`: one pull request as the host has it, the description included (C3,
   * D816). It is the second read here whose child mints a credential of the
   * host's -- the coordinator's own read-only installation token, which the CLI
   * resolves from the repository's provider, exactly as it does for the stored
   * view above -- and no credential of the host's passes through this process:
   * the argv carries the repository and the number, and the reply carries the
   * host's own document. */
  coordinatorShow(args: ValidatedArguments): Promise<Snapshot> {
    return this.#readDocument('coordinator.show', args)
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

// ---------------------------------------------------------------------------
// The bound source (desktop-console plan Task 3.4c)
// ---------------------------------------------------------------------------

/**
 * BoundInstallation is one installation as the console's own state holds it:
 * D13's tuple, plus the id that says *which* installation it is and the version
 * the handshake read for it. It is structurally the store's `ActiveInstallation`
 * with `chosen: true`, declared here rather than imported because this module is
 * the source layer: it knows an installation's four fields and nothing about the
 * file they are kept in.
 *
 * `binaryPath` is the installation's **optional** binary override, and it is
 * passed to the resolver as the configured path (rule 4's first place) only when
 * a record carries one. **Absent is the ordinary case** (owner's ruling of
 * 2026-10-20): the console never asks the operator for a binary and never stores
 * the path it resolved, so a bound source resolves the installed `fugaro` itself
 * -- `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, `/usr/bin`, and
 * never the app's inherited `PATH` -- exactly as the startup handshake does
 * (`resolveHandshakeBinary`). An override that is there is tried first and
 * falls through to those same locations when it has gone, so an old store entry
 * keeps working.
 */
export interface BoundInstallation {
  readonly id: string
  readonly project: string
  readonly binaryPath?: string | undefined
  /** the config path, when this installation is not the ambient one (rule 3). */
  readonly configPath?: string | undefined
  /** the checkout, which is also the child's working directory (rule 5). */
  readonly checkout?: string | undefined
  /** the version the handshake read for it, when one has read it. */
  readonly version?: string | undefined
}

/** SourceForOptions is `CliSourceOptions` minus the three fields the
 * installation itself supplies: the target, the binary and the directory. */
export type SourceForOptions = Omit<CliSourceOptions, 'target' | 'configuredBinaryPath' | 'checkout'>

/**
 * sourceFor binds a real `CliSource` to one installation, and it is **the only
 * source the shipped app can construct** (plan step 1).
 *
 * The alternative -- a source built from the ambient environment -- is what
 * design section 3.4 rule 3 forbids: a Finder-launched app has cwd `/` and
 * launchd's environment, so an unbound source would read whatever installation
 * the login shell happened to select. There is no environment variable, argv or
 * setting that chooses a source or a binary: `src/source/fixture.ts` is reachable
 * only through the unit tests' injected `create`, and a grep-style test asserts
 * that `src/main/index.ts` imports nothing from it (plan step 1).
 */
export function sourceFor(
  installation: BoundInstallation,
  options: SourceForOptions = {},
): Promise<CliSource> {
  return CliSource.create({
    ...options,
    target: Object.freeze({
      project: installation.project,
      ...(installation.configPath === undefined ? {} : { configPath: installation.configPath }),
    }),
    // The store's override, when the record carries one. Absent means rule 4
    // resolves the installed `fugaro`, which is what the console does by
    // default -- and passing `undefined` as a configured path would be a second
    // spelling of "no override", so it is left out.
    ...(installation.binaryPath === undefined || installation.binaryPath === ''
      ? {}
      : { configuredBinaryPath: installation.binaryPath }),
    ...(installation.checkout === undefined ? {} : { checkout: installation.checkout }),
  })
}

/** BoundSourceDeps is the bound source's two seams: where the active
 * installation is read from, and how a source is built for one. */
export interface BoundSourceDeps extends SourceForOptions {
  /**
   * installation is the console's own state, read **at call time** -- so a
   * removal or a switch is answered by the next read rather than by a value
   * captured when the surface was registered.
   */
  readonly installation: () => BoundInstallation | null
  /**
   * create builds the source for one installation. It is a seam and not a
   * parameter of the shipped app: the e2e specs and the unit tests hand in a
   * source they control, and `src/main/index.ts` leaves it out so that the only
   * thing it can construct is `sourceFor`.
   */
  readonly create?: ((installation: BoundInstallation) => Promise<CliSource>) | undefined
}

/** BoundSource is the source the IPC surface is built over, plus the one
 * operation the surface has no name for: dropping what is bound to an
 * installation that is gone. */
export interface BoundSource extends FugaroSource {
  /**
   * forget closes the source bound to the installation the console no longer
   * has and clears the binding, so the next read answers D13's no-installation
   * refusal rather than a document from an installation nobody chose. It is
   * `onRemoved`'s whole body (plan 3.4b's deferred N1) and it is idempotent.
   */
  forget(): Promise<void>
}

/**
 * bindingKey is what "the same source" means: the fields of one installation
 * that decide **what the child is and where it runs**. It is not the id alone,
 * and that is the whole of a defect this function exists to prevent: the id is
 * *which* installation this is -- `setCheckout` deliberately preserves it
 * (`console/src/main/installations.ts`), and so does a record that gains or
 * loses its override -- while the checkout is the child's working directory
 * (`options.checkout ?? privateWorkingDirectory()`) and the override is rule 4's
 * first candidate. A source cached by the id alone kept running in the directory
 * the installation used to be in: the picker ran rule 7's `doctor` from the new
 * one, the row and the header reported it, and every read still spawned with the
 * old cwd, so the dashboard read the wrong repository (or was refused by the
 * CLI's own pairing sentence) under a screen saying the new checkout was in use.
 *
 * It is serialised rather than joined with a separator: these are paths and
 * project names, and a delimiter one of them may contain is a collision waiting
 * to be a wrong directory.
 */
function bindingKey(installation: BoundInstallation): string {
  return JSON.stringify([
    installation.id,
    installation.project,
    installation.binaryPath ?? '',
    installation.configPath ?? '',
    installation.checkout ?? '',
  ])
}

/**
 * createBoundSource is the join between the console's own state and the data
 * plane: every read resolves the installation that is active **now**, builds the
 * one source for it if there is not one already, and delegates.
 *
 * Four decisions are worth reading off the code rather than out of a comment:
 *
 *   - **no installation, no spawn.** `resolve` throws `NoInstallation` before
 *     anything is constructed, so a console with nothing chosen drives nothing.
 *   - **one source per binding.** The source is cached by `bindingKey` -- the
 *     installation's id and the three fields the child is built from -- and the
 *     previous one is closed when that key changes, so neither a switch nor a
 *     checkout change can leave a `fugaro watch` child running for a directory
 *     the console has stopped showing.
 *   - **`subscribeLive` stays synchronous and idempotent.** Building a
 *     `CliSource` is asynchronous (it resolves and probes the binary) and
 *     `FugaroSource.subscribeLive` is not, so the feed this returns is a
 *     **lazy** one: it attaches on the first `current()`, and the same argument
 *     object answers the same feed object while it is live -- the contract the
 *     interface states, kept here rather than only under it.
 *   - **`planStatus` is the one read that decides something here** instead of
 *     delegating a one-liner: it refuses an installation with no checkout
 *     **before** `resolve` is reached, so nothing is spawned for a question with
 *     no directory to be asked about, and it answers the document it read within
 *     `PLAN_STATUS_MIN_INTERVAL_MS` rather than running the child again. Both
 *     belong on this side of the seam because both are about the *binding*: the
 *     checkout is the binding's, and the document is a function of the binding
 *     and the arguments.
 */
export function createBoundSource(deps: BoundSourceDeps): BoundSource {
  // The two seams are this function's own and are not `CliSource` options, so
  // they are not handed on to it.
  const { installation: _installation, create: injected, ...options } = deps
  const create =
    injected ?? ((installation: BoundInstallation): Promise<CliSource> => sourceFor(installation, options))
  let bound: { readonly key: string; readonly source: CliSource } | null = null
  let pending: { readonly key: string; readonly source: Promise<CliSource> } | null = null
  const feeds = new Map<string, LiveFeed>()

  /**
   * planReads holds the last `plan status` read per (binding, argv) -- the
   * promise itself, so a read still in flight is shared -- with the console
   * clock of the moment it finished, `null` while it runs. It is cleared wherever
   * `feeds` is: both are answers about the binding that is being dropped, and a
   * cached plan graph for a directory the console has stopped showing is the
   * same defect as a live feed for one.
   */
  const planReads = new Map<string, { readonly reading: Promise<Snapshot>; at: number | null }>()

  /** nowMs is the console's own clock in milliseconds, the one the sources are
   * built with (`options.clock`), so a test that moves the clock moves this
   * interval with it rather than reaching for the wall clock here. */
  const nowMs = (): number => (deps.clock ?? ((): Date => new Date()))().getTime()

  /**
   * epoch is what "the build in flight is no longer the one that matters" is
   * written as. Building a source is asynchronous -- it resolves the binary and
   * probes its version -- and `drop` can run in the middle of one: the operator
   * removes the installation while the console is reading from it. A build that
   * finishes after that must not bind, because `bound` is what every later read
   * is handed, and a source bound for an installation the store no longer names
   * is a `fugaro watch` child nobody holds and nobody will ever close -- which
   * is the one thing `forget` exists to prevent.
   */
  let epoch = 0

  const noInstallation = (): NoInstallation =>
    new NoInstallation(
      noInstallationState({
        reason: 'no installation is chosen, so there is no binary for the console to read',
        ...(deps.home === undefined ? {} : { home: deps.home }),
      }),
    )

  /**
   * superseded is the refusal for a read whose build landed after the console
   * stopped showing the installation it was for: the operator moved to another
   * installation, or removed this one.
   *
   * It is **not** always D13's no-installation state. When the console has moved
   * to another installation, one is chosen, its binary is there and its source
   * is being built -- and a screen saying "no installation is chosen, so there
   * is no binary for the console to read" would be describing a console that is
   * not this one, with a `searched` list and an install command to match. The
   * store is read here, at the moment of the refusal, so the sentence is about
   * what actually happened: a removal is D13's state, and a switch is a read for
   * an installation the console has stopped showing. A build superseded by a
   * **checkout change** lands here too, and the sentence is right for it as well:
   * the read was for the binding that named the previous directory, which is a
   * binding the console is no longer showing.
   */
  const superseded = (): SourceRefusal =>
    deps.installation() === null
      ? noInstallation()
      : new SourceRefusal('the console is no longer showing the installation this read was for')

  const resolve = async (): Promise<CliSource> => {
    const installation = deps.installation()
    if (installation === null) throw noInstallation()
    const key = bindingKey(installation)
    if (bound !== null && bound.key === key) return bound.source
    if (pending !== null && pending.key === key) return pending.source
    // A different binding, or none: the previous source's children are stopped
    // before a new one is built. `bound` is cleared first so a read that arrives
    // while the close is in flight cannot be handed the source that is on its way
    // out -- and the build is registered as `pending` **before** that close is
    // awaited, so the read that arrives in the same window joins this build
    // instead of starting a second one for the same binding (which would leave the
    // first refused as a superseded build, and cost a second probe of a binary
    // that was already being probed).
    const previous = bound
    bound = null
    feeds.clear()
    planReads.clear()
    epoch += 1
    const mine = epoch
    const source = (async (): Promise<CliSource> => {
      if (previous !== null) await previous.source.close()
      const created = await create(installation)
      if (mine !== epoch) {
        // Dropped, or superseded by a build for another binding, while this
        // one was in flight. `forget` could not close what did not exist yet, so
        // this source is closed here -- nothing else holds it -- and the read
        // that asked for it is refused for what actually happened.
        void created.close().catch(() => undefined)
        throw superseded()
      }
      bound = { key, source: created }
      pending = null
      return created
    })()
    pending = { key, source }
    // A build that failed is not kept as this installation's pending build: the
    // probe can fail for a reason that passes (a `version` that did not answer
    // in time on a loaded machine, a binary mid-upgrade), and a rejection kept
    // here would answer every later read with it until the operator switched
    // installations. The next read builds again.
    source.catch(() => {
      if (pending !== null && pending.source === source) pending = null
    })
    return source
  }

  const installationRef = (installation: BoundInstallation): InstallationRef => ({
    binaryPath: installation.binaryPath ?? '',
    version: installation.version ?? '',
    project: installation.project,
    // Which rule 4 place this installation's binary comes from, **as the
    // binding knows it**: the store's own override when the record carries one
    // (rule 4's first candidate), and the well-known install locations
    // otherwise -- which is the ordinary case, because the console never stores
    // the path it resolved (owner's ruling of 2026-10-20). The binding carries
    // the override and nothing else, so an empty `binaryPath` here means rule 4
    // will be walked, and this field is what says so while there is no source to
    // ask. The *built* source answers the finer question from its own
    // resolution (`resolution.source`, this file), where an override that has
    // gone falls through and the word is the location that answered.
    source: installation.binaryPath === undefined || installation.binaryPath === '' ? 'well-known' : 'configured',
    ...(installation.checkout === undefined ? {} : { checkout: installation.checkout }),
  })

  /**
   * lazyFeed wraps one subscription. It is deliberately small: the three
   * listeners attach once the source exists (nothing in this console calls them
   * yet -- the surface is `invoke`-only and `live.watch` is a poll), and
   * `current`/`stop` are the two the handler uses.
   *
   * Every `current()` asks the bound source for the feed again rather than
   * keeping the first stream it was handed. `CliSource.subscribeLive` answers
   * the same stream while it is live and drops one that has *ended*, so the
   * poll after a `watch` child exited (or was evicted past `MAX_LIVE_FEEDS`)
   * starts a fresh child; a stream held here would be polled dead forever.
   */
  const lazyFeed = (args: ValidatedArguments): LiveFeed => {
    let real: LiveFeed | null = null
    let attaching: Promise<LiveFeed> | null = null
    const attach = (): Promise<LiveFeed> => {
      attaching = resolve().then((source) => {
        real = source.subscribeLive(args)
        return real
      })
      return attaching
    }
    const defer = (wire: (feed: LiveFeed) => Unsubscribe): Unsubscribe => {
      let off: Unsubscribe | null = null
      let stopped = false
      void attach()
        .then((feed) => {
          if (!stopped) off = wire(feed)
        })
        .catch(() => undefined)
      return () => {
        stopped = true
        if (off !== null) off()
      }
    }
    return {
      get latest(): Snapshot | null {
        return real === null ? null : real.latest
      },
      onChange: (listener) => defer((feed) => feed.onChange(listener)),
      onNotice: (listener) => defer((feed) => feed.onNotice(listener)),
      onEnded: (listener) => defer((feed) => feed.onEnded(listener)),
      current: async (): Promise<Snapshot> => (await attach()).current(),
      stop: async (): Promise<void> => {
        if (real !== null) {
          await real.stop()
          return
        }
        if (attaching !== null) {
          const feed = await attaching.catch(() => null)
          if (feed !== null) await feed.stop()
        }
      },
    }
  }

  /** drop clears the binding and stops everything the bound source started.
   * `forget` and `close` are the same act under two names: the interface's
   * `close` stops every child this source started, and `forget` is what
   * `onRemoved` calls -- an installation that is gone must leave nothing running
   * for it. */
  const drop = async (): Promise<void> => {
    const previous = bound
    bound = null
    pending = null
    feeds.clear()
    planReads.clear()
    // A build in flight is invalidated here rather than awaited: it is for an
    // installation that is gone, and it closes its own source when it lands.
    epoch += 1
    if (previous !== null) await previous.source.close()
  }

  const read = async <T>(operation: string, go: (source: CliSource) => Promise<T>): Promise<T> => {
    let source: CliSource
    try {
      source = await resolve()
    } catch (error) {
      // A source that could not be built is the read's refusal, named: the CLI's
      // own sentence when it printed one (a binary whose version cannot be read),
      // and the console's own no-installation sentence otherwise.
      if (error instanceof SourceRefusal) throw error
      throw new SourceRefusal(`${operation} failed: ${error instanceof Error ? error.message : String(error)}`)
    }
    return go(source)
  }

  return {
    get installation(): InstallationRef {
      const installation = deps.installation()
      if (installation === null) throw noInstallation()
      return installationRef(installation)
    },
    version: () => read('cli.version', (source) => source.version()),
    subscribeLive(args: ValidatedArguments): LiveFeed {
      // One feed object per (installation, arguments) while it is live, which is
      // the idempotency `FugaroSource.subscribeLive` states. The key is the
      // active installation's id and the argument object: the same arguments
      // under a different installation are legitimately a different feed.
      const installation = deps.installation()
      if (installation === null) throw noInstallation()
      const key = `${installation.id}\u0000${JSON.stringify(sortedArguments(args))}`
      const existing = feeds.get(key)
      if (existing !== undefined) return existing
      const feed = lazyFeed(args)
      feeds.set(key, feed)
      return feed
    },
    showBudget: (args) => read('budget.show', (source) => source.showBudget(args)),
    runReport: (args) => read('spend.report', (source) => source.runReport(args)),
    listRuns: (args) => read('runs.list', (source) => source.listRuns(args)),
    statsRuns: (args) => read('runs.stats', (source) => source.statsRuns(args)),
    /**
     * planStatus is the surface's one read with a precondition and a cadence.
     *
     * The precondition is the checkout. The CLI resolves the plan's `docs/`
     * directory from the child's working directory, and an installation that
     * records none has no directory for the read to be about: this refuses with
     * `NO_PLAN_CHECKOUT` **before** `resolve` -- so no binary is probed, no
     * child is spawned, and the console does not quietly read whatever
     * repository it happens to be sitting in. That the renderer cannot supply a
     * directory is `src/main/ipc.ts`'s guarantee, stated there; this is the
     * other half, where the console refuses to guess one.
     *
     * The cadence is `PLAN_STATUS_MIN_INTERVAL_MS`, keyed by the binding *and*
     * the argv the arguments become (`--all` is a different document, not a
     * wider one; `all: false` is the same argv as no `all`). The entry is the
     * read itself, **in flight** as well as answered, so calls that arrive while
     * the child runs share it rather than each spawning one, and a refused read
     * holds the floor like an answered one. The `at` recorded is the clock
     * reading **after** the child finished, so the interval is a floor on the
     * time between two spawns rather than on the time between a spawn and the
     * next request.
     */
    planStatus: async (args) => {
      const installation = deps.installation()
      if (installation === null) throw noInstallation()
      if (installation.checkout === undefined) throw new SourceRefusal(NO_PLAN_CHECKOUT)
      const key = `${bindingKey(installation)}\u0000${args['all'] === true ? '--all' : ''}`
      const cached = planReads.get(key)
      if (cached !== undefined && (cached.at === null || nowMs() - cached.at < PLAN_STATUS_MIN_INTERVAL_MS)) {
        return cached.reading
      }
      const reading = read('plan.status', (source) => source.planStatus(args))
      const entry: { readonly reading: Promise<Snapshot>; at: number | null } = { reading, at: null }
      planReads.set(key, entry)
      const settled = (): void => {
        entry.at = nowMs()
      }
      reading.then(settled, settled)
      return reading
    },
    /** coordinatorStatus has no precondition and no cadence: the read touches
     * nothing on this machine (it is the caller's own database node) and the
     * stored view is a document the sweep replaces in place, so there is no
     * directory to refuse for and nothing to be gained by holding a floor
     * between two calls. */
    coordinatorStatus: (args) => read('coordinator.status', (source) => source.coordinatorStatus(args)),
    /** coordinatorShow has no precondition and no cadence of its own: the
     * repository and the number in the argv are the whole question, the CLI
     * resolves the repository's provider and mints the coordinator's own
     * read-only installation token for it, and there is nothing on this machine
     * for this process to refuse a read for. Two calls with the same arguments
     * are two host reads, which is what the caller asked for. */
    coordinatorShow: (args) => read('coordinator.show', (source) => source.coordinatorShow(args)),
    diagnoseRun: (args) => read('run.diagnose', (source) => source.diagnoseRun(args)),
    readRunLogs: (args) => read('run.logs', (source) => source.readRunLogs(args)),
    runLogLink: (args) => read('run.logLink', (source) => source.runLogLink(args)),
    listSecrets: (args) => read('secrets.inventory', (source) => source.listSecrets(args)),
    listAccounts: (args) => read('accounts.inventory', (source) => source.listAccounts(args)),
    environmentReport: (args) => read('environment.report', (source) => source.environmentReport(args)),
    forget: drop,
    close: drop,
  }
}

/** sortedArguments is one argument object with its keys in a fixed order, so two
 * calls with the same arguments answer the same key however the object was
 * built. Values are already validated (strings and booleans only). */
function sortedArguments(args: ValidatedArguments): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const key of Object.keys(args).sort()) out[key] = args[key]
  return out
}

/**
 * parseStdout reads a one-shot read's stdout. Every one-shot command in the
 * console's set prints ONE document indented over several lines
 * (`console/docs/json-contract.md` section 9.2: a line parser is right only for
 * `watch` and `logs`), so the document is read whole: it opens with a `{` at
 * the start of a line and closes with a `}` alone at the start of a line --
 * the shape `json.MarshalIndent` and `SetIndent` write, where every nested line
 * is indented -- or it is one compact line. A nested `{}` is indented and is
 * never taken for the document. A line outside the document that is not blank
 * is kept as a notice (the CLI's degraded-mode sentences share the stream).
 *
 * A read whose stdout holds no document, a torn one, or more than one answers
 * `document: null`, which the caller turns into a refusal rather than an empty
 * screen or a guess at which document was meant.
 */
export function parseStdout(
  stdout: string,
  receivedAt = '',
): { readonly document: JSONObject | null; readonly notices: readonly Notice[] } {
  const notices: Notice[] = []
  const documents: JSONObject[] = []
  let torn = false
  const lines = stdout.split('\n').map((line) => line.replace(/\r$/, ''))
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i] as string
    if (line.trim() === '') continue
    if (!line.startsWith('{')) {
      notices.push({ text: line, receivedAt })
      continue
    }
    const compact = decodeFeedLine(line)
    if (compact.kind === 'document') {
      documents.push(compact.document)
      continue
    }
    // An indented document: read to the `}` that closes it at column 0.
    let end = i + 1
    while (end < lines.length && lines[end] !== '}') end += 1
    if (end >= lines.length) {
      torn = true
      break
    }
    const decoded = decodeDocument(lines.slice(i, end + 1).join('\n'))
    if (decoded === null) torn = true
    else documents.push(decoded)
    i = end
  }
  const document = !torn && documents.length === 1 ? (documents[0] as JSONObject) : null
  return { document, notices }
}

/** decodeDocument is one JSON object, or null for anything else. */
function decodeDocument(text: string): JSONObject | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    return null
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return null
  return parsed as JSONObject
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
 * one document read whole, which is the rule for the indented documents
 * (`parseStdout`).
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
 *
 * **`distro` is the WSL one, and it is what puts the candidate on that route**
 * (D2(a)): with it, `binaryPath` and `checkout` are Linux spellings *inside* the
 * distribution and the CLI is reached through `wsl.exe`; without it -- the
 * ordinary case, and every record this console wrote before Windows -- the
 * installation is a host installation and the fields are host paths.
 */
export interface InstallationCandidate {
  readonly project: string
  readonly configPath?: string | undefined
  readonly binaryPath?: string | undefined
  readonly checkout?: string | undefined
  readonly distro?: string | undefined
}

/** InstallationStore is the in-memory list the app holds for one run. */
export interface InstallationStore {
  list(): readonly InstallationCandidate[]
  /** add verifies the candidate with one allow-listed read and stores it, or
   * throws the CLI's own refusal and stores nothing. */
  add(candidate: InstallationCandidate): Promise<InstallationCandidate>
}

/**
 * InstallationPersistence is the store's durability seam (desktop-console plan
 * Task 3.4b; owner ruling C8, 2026-10-18).
 *
 * The store itself stays what it was -- a list, and the one check that matters,
 * that an installation is verified *before* it is stored -- and the file is
 * somebody else's shape: `src/main/installations.ts` owns `installations.json`,
 * its closed schema, its ids and its `active` field, and it is the module that
 * decides **when** a write happens (an add, a choose, a remove, and nothing
 * else). What this seam exists for is the plan's requirement that the
 * *verification* logic stay here, in one place: a caller that persists gets it
 * by handing in `load`/`save` over the same candidate tuple rather than by
 * writing a second store with its own copy of the check.
 *
 * Both halves are optional and both are total: a store with no `load` starts
 * empty (which is what every unit test wants), and a store with no `save` is the
 * in-memory list this function used to be. Neither is a path: this module never
 * learns where the file is, and rule 7's "the app never reads the operator's
 * config" is untouched by either.
 */
export interface InstallationPersistence {
  load(): readonly InstallationCandidate[]
  save(list: readonly InstallationCandidate[]): void
}

/**
 * createInstallationStore returns the store. It is in memory unless a caller
 * hands it a `persistence`, and the check it runs is the one that matters: an
 * installation is verified with one allow-listed read *before* it is stored, so
 * a refused candidate is never written.
 *
 * **This comment used to say the console writes nothing but the spawn log.**
 * That was true when it was written and it is not true now: rule 10 and section
 * 7.4 name `installations.json` as the second file the app writes, and the store
 * that writes it is `src/main/installations.ts` -- this function is the seam it
 * uses for the check, not a second implementation of it.
 */
export function createInstallationStore(deps: {
  readonly appEnv?: Readonly<Record<string, string | undefined>>
  readonly spawner?: ChildSpawner
  readonly spawnLog?: SpawnLogWriter
  readonly persistence?: InstallationPersistence
}): InstallationStore {
  const stored: InstallationCandidate[] = deps.persistence === undefined ? [] : [...deps.persistence.load()]
  const env = deps.appEnv ?? {}

  return {
    list: () => [...stored],
    async add(candidate: InstallationCandidate): Promise<InstallationCandidate> {
      if (candidate.project === '') throw new SourceRefusal('an installation needs a project name')
      assertSpawnSurface(SPAWN_COMMANDS)
      await verifyCandidate(candidate, deps)
      stored.push(candidate)
      // The write happens *after* the check and after the push, so a save that
      // throws leaves the in-memory list and the file disagreeing in the one
      // direction that is safe: the list has the installation, the file does
      // not, and the next add re-verifies.
      deps.persistence?.save([...stored])
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
    /** `wslPath` replaces the `wsl.exe` a WSL candidate's check runs, so a test
     * can point the route at the fake (T-A1, T-D1). */
    readonly wslPath?: string | undefined
  },
): Promise<void> {
  const env = deps.appEnv ?? {}
  // **A WSL installation is never verified from the host's checkout path.** When
  // the candidate names a distribution its `checkout` is a Linux directory
  // inside it (F15), which is not this process's to start a child in: the host
  // directory is the private one, and the CLI's own directory travels as the
  // wrapper's `--cd` (D2(a), D3(a)).
  const distro = candidate.distro ?? ''
  const directory =
    distro === '' ? candidate.checkout ?? privateWorkingDirectory() : privateWorkingDirectory()
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

/** verifyWith is `verifyCandidate`'s body, with the log already chosen.
 *
 * **It runs on the installation's own route** (T-D1, D2(a)): a candidate with a
 * `distro` is checked *inside* that distribution -- the resolution and the
 * `doctor --json` read both go through `wsl.exe`, exactly as the app's own reads
 * do (T-C5) -- and a candidate without one is checked on the host, unchanged.
 * The property the pairing check exists for is the CLI's own statement about the
 * checkout it was started in, so the read has to happen where the checkout is. */
async function verifyWith(
  candidate: InstallationCandidate,
  deps: { readonly spawner?: ChildSpawner; readonly home?: string; readonly wslPath?: string | undefined },
  env: Readonly<Record<string, string | undefined>>,
  directory: string,
  log: SpawnLogWriter,
): Promise<void> {
  const target: SpawnTarget = {
    project: candidate.project,
    ...(candidate.configPath === undefined ? {} : { configPath: candidate.configPath }),
  }
  const distro = candidate.distro ?? ''
  if (distro !== '') {
    const resolution = await resolveInstalledBinary({
      binaryPath: candidate.binaryPath ?? '',
      distro,
      appEnv: env,
      directory,
      target,
      ...(candidate.checkout === undefined ? {} : { wslDirectory: candidate.checkout }),
      ...(deps.wslPath === undefined ? {} : { wslPath: deps.wslPath }),
      ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
      log,
    })
    if (!resolution.ok) throw new SourceRefusal(resolution.reason)
    const plan = planFor('environment.report', {}, {
      target,
      binaryPath: resolution.path,
      directory,
      appEnv: env,
      wsl: {
        distro,
        ...(candidate.checkout === undefined ? {} : { directory: candidate.checkout }),
        ...(deps.wslPath === undefined ? {} : { executable: deps.wslPath }),
      },
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
    return
  }
  const resolution = await resolveBinary({
    ...(candidate.binaryPath === undefined ? {} : { configuredPath: candidate.binaryPath }),
    appEnv: env,
    directory,
    target,
    ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
    ...(deps.home === undefined ? {} : { home: deps.home }),
    log,
  })
  if (!resolution.ok) throw new SourceRefusal(resolution.reason)

  const plan = planFor('environment.report', {}, {
    target,
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
