// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The launch, which is the console's **one write** (design section 5.1 rule 11,
// D22, 2026-10-16). Everything in this file exists because of one fact: a
// `fugaro run` spends money, and `fugaro run` prompts for nothing -- there is no
// `--yes` and no question (`internal/cli/run.go`), so the confirmation is the
// console's own and there is nothing behind it.
//
// **What this file is, and what it deliberately is not.** V2.5.1a is split in
// three: part 1 (the pure rules: `launchrules.ts`, `installlist.ts`'s
// `base_branch` read, `launchSupported` in `src/shared/version.ts`), part 2a
// (the fence move: the operation, its spawn row, the frozen spawn and the
// limits) and part 2b (the confirmation window, `confirm.ts`). The confirmation
// here is a **seam** -- `LaunchConfirmer` -- because the window is Electron's
// own and cannot be driven under plain Node: everything above the seam is
// unit-tested with a fake confirmer, and the shipped wiring is `index.ts`'s.
// Since part 2b-3b-1 that wiring is the real window, built in `main()` once the
// session exists, and only a yes from that window's own top frame reaches
// `spawn`.
//
// Nothing here opens a window. `index.ts` builds the window
// (`createConfirmWindow`) and the launcher over it, and the module-scope runner
// it starts the process with -- the one that answers before `main` has built
// either -- is over `DECLINING_CONFIRMER`; nothing else may.

import { randomBytes } from 'node:crypto'

import type { ChildSpawner, SpawnPlan, Timers } from './spawn'
import { cliSentence, planFor, privateWorkingDirectory, runToCompletion, spawnedArgv } from './spawn'
import type { SpawnLogWriter } from './spawnlog'
import type { LaunchRecords, LaunchResult } from './launchrules'
import {
  checkLaunchBranch,
  checkLaunchPlanTask,
  checkLaunchTask,
  checkLaunchTitle,
  decodeLaunchResult,
  displayTaskText,
  launchRepoArgument,
  recordedBaseBranch,
} from './launchrules'
import { launchSupported } from '../shared/version'

/**
 * LAUNCH_TIMEOUT_MS is this row's own deadline, and it is **not**
 * `READ_TIMEOUT_MS` (90 s): a launch runs the budget pre-check, writes the task,
 * claims the run id and starts a job, so a read's deadline would kill a launch
 * that is still working. Design section 5.1 rule 11 names the number and this is
 * it.
 */
export const LAUNCH_TIMEOUT_MS = 120_000

/** The confirmation rate, in design section 5.1 rule 11's own numbers: windows
 * are opened at most three times a minute per installation and six times a
 * minute across the whole app. The cap is on the **confirmation request**, not
 * on accepting one: the request is what this process works for -- the name is
 * read against the records before any window exists -- so a refused window and a
 * launch that never builds both count, and a stream of them is a denial of
 * service against the person at the keyboard rather than against the CLI. */
export const CONFIRM_RATE_WINDOW_MS = 60_000
export const CONFIRM_RATE_PER_INSTALLATION = 3
export const CONFIRM_RATE_APP = 6

/** An accepted launch pauses that installation's next window for ten seconds:
 * longer than any double-submit a person can make, short enough that a
 * deliberate second launch is paused rather than blocked. */
export const LAUNCH_COOL_DOWN_MS = 10_000

/** At most one launch is in flight per installation and two across the app, and
 * a further `run.launch` while a launch is unsettled is **refused rather than
 * queued** -- D22's "no command queue" in the small. */
export const MAX_IN_FLIGHT_PER_INSTALLATION = 1
export const MAX_IN_FLIGHT = 2

/** MAX_RECORDED_OUTCOMES is how many answered run ids this process replays an
 * answer from, evicting the oldest first: 256. A replay is an operator's second
 * answer for a window they have just closed, and the limits above allow at most
 * six windows a minute app-wide, so 256 is more than forty minutes of the
 * busiest answering this console permits -- far past any replay an operator can
 * reach -- while each record is one `LaunchOutcome` and the cap keeps the whole
 * record small for the life of the process. Eviction is not forgetting: an
 * evicted id stays in `issuedRunIds`, so it is never drawn for a new window, and
 * an answer for it spawns nothing. */
export const MAX_RECORDED_OUTCOMES = 256

/** DECLINED_UNBUILT is what a launch meets before `main` has built the
 * confirmation window, and the sentence is deliberately about this tree rather
 * than about the operator's click: nothing asked them anything. It is the
 * fail-closed default's answer (`DECLINING_CONFIRMER`), not a sentence an
 * operator is expected to read: the surface is registered only from `onDecided`,
 * which only `lifecycle.start()` and `choose` reach, both from inside `main`
 * and after the window is built. */
export const DECLINED_UNBUILT = 'the launch confirmation window is not built yet'

/** DECLINED_CANCELLED is the console's cancel sentence, and it is the answer to
 * every way a launch is declined: the operator answered no, pressed Escape or
 * Enter, or closed the window; the installation the launch was cut for was
 * removed or re-chosen while it was open (`cancelFor`); or the still-recorded
 * check found the installation had moved. Declining spawns nothing, and the
 * sentence says that rather than looking like a failure. */
export const DECLINED_CANCELLED = 'the launch was not confirmed: nothing was spawned'

/** RUN_ID_DIGITS is the shape `task.NewRunID` builds, `<UTC 20060102-150405>-<4
 * hex>`. It is written here as a formatter and in `spawn.ts` as a grammar, and
 * the two are held to each other by a test: the id this file makes must be one
 * `argvFor` accepts. */
function newRunID(now: Date, random: () => number): string {
  const pad = (value: number, width: number): string => String(value).padStart(width, '0')
  const stamp =
    `${String(now.getUTCFullYear())}${pad(now.getUTCMonth() + 1, 2)}${pad(now.getUTCDate(), 2)}` +
    `-${pad(now.getUTCHours(), 2)}${pad(now.getUTCMinutes(), 2)}${pad(now.getUTCSeconds(), 2)}`
  const tail = Math.floor(random() * 0x10000)
  return `${stamp}-${tail.toString(16).padStart(4, '0')}`
}

/** RUN_ID_DRAWS is how many times `launch` redraws the id's four hex digits
 * before it refuses the launch: the stamp is the UTC second and cannot be
 * redrawn, so a collision that survives eight draws is one this process cannot
 * draw its way out of, and a retry that cannot succeed is a hang rather than a
 * launch. */
const RUN_ID_DRAWS = 8

/**
 * drawRunID is `newRunID` with collision-freedom against the ids **this process
 * already carries**, and it answers null when every draw was one of them.
 *
 * The id has 16 random bits and nothing else: two launches in the same UTC
 * second whose tails collide would be one id -- `frozen` and `pending` are keyed
 * by it, so `frozen.set` and `pending.set` would overwrite silently, and the
 * first window's yes would then spawn the second launch's frozen object under
 * the first window's answer. Redrawing inside the one place the id is cut,
 * *before* the window opens, is what keeps that from being reachable at all,
 * and `taken` is asked about all three places an id can be: **claimed** (drawn
 * by a launch that is still reading its records), **in flight** (a window that
 * is open) and **issued** (an id this process has already committed, which a
 * replayed answer still resolves -- and which stays known after the launch's own
 * objects have been dropped, since `issuedRunIds` is strings alone). The claim
 * is the one that makes the check complete, because it is the only one that
 * exists before the launch is built.
 *
 * What is left after the retries is a refusal rather than a shared slot: an id
 * this process already holds is one an operator's yes could be attached to the
 * wrong launch, which is worse than asking again a second later.
 */
function drawRunID(now: Date, random: () => number, taken: (runId: string) => boolean): string | null {
  for (let draw = 0; draw < RUN_ID_DRAWS; draw += 1) {
    const runId = newRunID(now, random)
    if (!taken(runId)) return runId
  }
  return null
}

/**
 * cryptoRandom is the default source of the id's four hex digits, and it is
 * `node:crypto`'s two bytes rather than `Math.random`'s because of what a
 * collision costs: two launches in the same UTC second whose tails collide
 * produce one id, the CLI's claim then holds it, and the second launch comes
 * back `already-launched` -- which D23 has this console draw as a **fact rather
 * than an error**, so a confirmed launch that spends nothing would say nothing
 * about why. A collision with an id **this** process holds never reaches the
 * CLI at all: `drawRunID` redraws the tail and refuses only if every draw is
 * taken, so what is left here is a second console, or a run cut by hand in the
 * same second. `task.NewRunID` draws its own tail from `crypto/rand`
 * (`internal/task/task.go`), so this is the same source as the id it is shaped
 * like. The digits are not a secret and nothing here is key material: this is
 * collision-freedom, not unpredictability.
 */
function cryptoRandom(): number {
  return randomBytes(2).readUInt16BE(0) / 0x10000
}

/** LaunchRequest is the five validated fields the operation carries, and it is
 * the whole of what a renderer can say about a launch: no flag, no path, no ref,
 * no workflow, no recipe, no account, no cloud project, no region, no timeout,
 * and no run id (that one is this process's own). */
export interface LaunchRequest {
  readonly installation: string
  readonly repository: string
  readonly task: string
  readonly title?: string | undefined
  readonly planTask?: string | undefined
}

/**
 * LaunchTarget is what this process's own records answer about one installation:
 * the things the spawn is built from and the things the refusals are decided on.
 * It is handed in by the entry point rather than read here, so this module is a
 * pure function of its arguments and a unit test drives it under plain Node with
 * no store, no CLI and no window.
 */
export interface LaunchTarget {
  /** the installation's project name -- what `--project` carries */
  readonly project: string
  /** the resolved binary's absolute path: the host route's program word */
  readonly binaryPath: string
  /** the installation's config path, when it records one */
  readonly configPath?: string | undefined
  /** the WSL distribution this installation runs inside, when it has one */
  readonly distro?: string | undefined
  /** the version the handshake read for this installation, or `''` for none */
  readonly cliVersion: string
  /** the base branches and repositories its `config list --json` read carried */
  readonly records: LaunchRecords
  /** fingerprint is the identity of the **record** this target was read from:
   * the entry point builds it from this console's own stored installation
   * (`launchrules.ts`'s `LaunchRecordFacts`, which the rail's row is not part
   * of), this file freezes it into the launch, and the entry point compares a
   * fresh one at the answer. "The name is still saved" is not "the installation
   * is still the one this launch is for" -- a removal and a re-add under the
   * same name, or an edit of the binary, the distribution or the checkout, is a
   * different installation, and a launch cut for the old one closes as a
   * cancel. */
  readonly fingerprint: string
}

/** TargetLookup is the answer of the record read: the target, or the console's
 * own sentence for a name its records do not carry. */
export type TargetLookup =
  | { readonly ok: true; readonly target: LaunchTarget }
  | { readonly ok: false; readonly reason: string }

/**
 * FrozenLaunch is the whole spawn, built **once** and frozen, and it is what the
 * confirmation shows and what a yes-answer spawns **verbatim**. The design's
 * rule is why it is one object: the main process never re-reads the records, the
 * renderer's fields or the current installation between the window and the
 * spawn, so nothing an operator or a renderer does while a window is open can
 * change what confirming it spawns.
 */
export interface FrozenLaunch {
  readonly runId: string
  /** the installation's project name, and the repository as the record spelled
   * it and as the argv will carry it: both are shown, so the operator sees the
   * conversion rather than inferring it. */
  readonly project: string
  readonly recordedRepository: string
  readonly repository: string
  readonly branch: string
  readonly title: string | null
  readonly planTask: string | null
  /** the task as the operator typed it, trimmed, and the bytes the child is
   * given: the same text, encoded once. */
  readonly taskText: string
  readonly taskBytes: Uint8Array
  /** the record's own fingerprint, as `LaunchTarget` answers it: what the
   * answer-time read is compared against (`LaunchDeps.stillRecorded`). */
  readonly fingerprint: string
  readonly argv: readonly string[]
  readonly cwd: string
  readonly env: Readonly<Record<string, string>>
  readonly plan: SpawnPlan
}

/** LaunchOutcome is what the operation answers with. It is one closed object
 * rather than a union of shapes so that a refusal, a replay and a launch all
 * carry the run id: a killed launch's id is the one thing the operator needs,
 * and an outcome that dropped it would leave them with nothing to read. */
export interface LaunchOutcome {
  /** `ok` is true only when a spawn happened (or its reply was replayed). */
  readonly ok: boolean
  /** the run id, or null for a refusal decided before one was generated. */
  readonly runId: string | null
  /** the decoded `launchResult`, or null when nothing decoded. */
  readonly result: LaunchResult | null
  /** the console's own sentence for a refusal, or `''` when `ok`. */
  readonly reason: string
  /** true only for a launch that was stopped before it answered: the one state
   * where the console knows nothing about what the CLI did. */
  readonly outcomeUnknown: boolean
  /** a replay is the same answer served from this process's memory rather than
   * from a second child. */
  readonly replayed: boolean
}

/**
 * ConfirmView is the whole of what the confirmation window is shown, and it is a
 * **copy** rather than the frozen launch: the launch carries the plan, the
 * child's environment, its working directory and `taskBytes`, and a window is
 * the one place in this app that is not the main process. The bytes are the
 * sharpest of the four -- a `Uint8Array` is an object `Object.freeze` cannot
 * freeze, so a renderer handed one could write into the buffer the child is
 * spawned with. Nothing here is anything but a string, a number or an array of
 * strings, and the whole view is deep-frozen, so what the window reads cannot be
 * changed under it between the paint and the answer.
 *
 * The field names are the operator's rather than this file's: `installation` is
 * the project, `ref` the recorded base branch. `recordedRepository` and
 * `repository` are both shown because the conversion between them is a decision
 * the operator should see rather than infer.
 */
export interface ConfirmView {
  readonly runId: string
  readonly installation: string
  readonly recordedRepository: string
  readonly repository: string
  readonly ref: string
  readonly title: string | null
  readonly planTask: string | null
  /** the whole command line the yes-answer spawns, program word included and
   * field by field, so the window shows what is spawned and not a summary of it. */
  readonly argv: readonly string[]
  readonly taskByteLength: number
  /** the task's text, whole and never cut, with the format characters removed
   * (`displayTaskText`). It is the text the operator reads, not a preview: the
   * window is scrollable for exactly this. */
  readonly taskText: string
}

/** confirmView is the one conversion from the frozen launch to what the window
 * is shown, and it is a pure function of the launch for that reason -- a second
 * spelling of these fields would be a second thing to keep in step. */
export function confirmView(launch: FrozenLaunch): ConfirmView {
  return Object.freeze({
    runId: launch.runId,
    installation: launch.project,
    recordedRepository: launch.recordedRepository,
    repository: launch.repository,
    ref: launch.branch,
    title: launch.title,
    planTask: launch.planTask,
    argv: Object.freeze([...launchSpawnedArgv(launch)]),
    taskByteLength: launch.taskBytes.byteLength,
    taskText: displayTaskText(launch.taskText),
  })
}

/**
 * LaunchConfirmer is the seam the confirmation window fills: `confirm.ts`'s
 * `createConfirmWindow` is the shipped implementation, and `index.ts` builds one
 * in `main()` and hands it to `createLaunchRunner`. `open` is handed the
 * **display-only view** of the frozen launch and a `decide` it calls when the
 * operator answers -- the main process's own path, which is what `answer(runId,
 * yes)` reaches from the window's preload; the renderer asks for the window and
 * **cannot answer for the operator**.
 *
 * `decide`'s second parameter is **the confirmer's own sentence for a decline**,
 * and it exists for exactly one reason: the sentence the operator is shown for a
 * decline is the console's, not the CLI's, and a confirmer can decline for a
 * reason of its own -- the fail-closed default declines because there is no
 * window at all yet (`DECLINED_UNBUILT`) -- which is not the same sentence as a
 * window the operator closed (`DECLINED_CANCELLED`). It is optional, and the
 * window's own main-side path (`answer(runId, yes)`) never passes one: a decline
 * that came from the operator is the cancel sentence and nothing else.
 *
 * `close` is the seam's **second half**, and it is here because rule 11's "an
 * installation removed or re-chosen while the window is open closes the window
 * as a cancel" is a thing the caller knows and the window does not: the caller
 * names the run whose window should go away (`LaunchRunner.cancelFor`, which is
 * where a project becomes that run id), the window decides no, and the cancel
 * that follows is the ordinary decline -- nothing is spawned, and the frozen
 * launch the run id holds is not touched. A run whose window is not open is
 * nothing here to close, and nothing here decides.
 */
export interface LaunchConfirmer {
  open(view: ConfirmView, decide: (yes: boolean, reason?: string) => void): void
  close(runId: string): void
}

/** DECLINING_CONFIRMER is the fail-closed default `index.ts` starts the process
 * with, before `main` has built the confirmation window: there is no window to
 * ask, so every launch is declined by name (`DECLINED_UNBUILT`) and nothing
 * reaches `spawn`. `main` replaces it with the window's own confirmer. */
export const DECLINING_CONFIRMER: LaunchConfirmer = Object.freeze({
  open: (_view: ConfirmView, decide: (yes: boolean, reason?: string) => void): void => {
    decide(false, DECLINED_UNBUILT)
  },
  // No window is ever opened, so none is ever open to close: `open` has already
  // decided, and there is nothing here for a close to cancel.
  close: (_runId: string): void => undefined,
})

/** The console's own sentence for a launch that was stopped before it answered.
 * It never says "not launched": the CLI may have started the container before it
 * died, so the honest answer names the run id and the two allow-listed reads
 * that settle it (`fugaro runs ls --json`, `fugaro diagnose <run> --json`). */
export function launchOutcomeUnknown(runId: string, why: string): string {
  return (
    `${why}, so whether run ${runId} started is unknown. Read \`fugaro runs ls --json\`, ` +
    `or \`fugaro diagnose ${runId} --json\`, to settle it.`
  )
}

export interface LaunchDeps {
  /** records answers this process's own records for one installation name. It
   * is async because the answer includes the installation's `config list --json`
   * read; a name the records do not carry is `ok: false`. */
  readonly lookUp: (project: string) => Promise<TargetLookup>
  readonly confirmer: LaunchConfirmer
  /** stillRecorded answers whether the **same** installation is in the records
   * now: a window whose installation was removed, re-added or changed while it
   * was open closes as a cancel, and this is the check that makes that true
   * rather than promised. It is handed the whole frozen launch, because the name
   * alone cannot answer it -- the launch carries the record's fingerprint and
   * the implementation compares a fresh one against it. It may be async because
   * the records are read from the CLI's own list, and a stale answer here is a
   * launch for an installation the operator has just removed. A read that
   * refuses or throws answers no; see `settle`. */
  readonly stillRecorded: (launch: FrozenLaunch) => Promise<boolean> | boolean
  readonly appEnv: Readonly<Record<string, string | undefined>>
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timers?: Timers
  /** now, wallClock and random are the two clocks and the id's hex tail, all
   * injectable so the limits, the cool-down and the run id are testable without
   * waiting.
   *
   * `now` is the **limits'** clock, and it is monotonic (`performance.now`) and
   * deliberately not the wall clock: the in-flight entries, the cool-down and
   * the two rate windows are elapsed-time bounds, and a wall clock somebody
   * moves -- an operator, NTP, a laptop waking from sleep with a wrong time --
   * would either lock an installation out of launching until real time caught up
   * or free it early. `wallClock` is the run id's, and it *is* the wall clock
   * (`Date.now`), because the id's stamp is the UTC second the run was cut in
   * and the CLI reads it as one. */
  readonly now?: () => number
  readonly wallClock?: () => number
  readonly random?: () => number
}

/** LaunchRunner is the launcher the entry point wires the handler to. */
export interface LaunchRunner {
  launch(request: LaunchRequest): Promise<LaunchOutcome>
  /** answer is the confirmation window's own main-side path: the yes/no the
   * operator gave, for the run id that window was opened under.
   *
   * It answers with the outcome `launch` resolves with for that run id. The
   * first answer for an open window resolves that window's `decide`; every
   * answer while the launch is still settling -- the first one included, and a
   * later yes or no, since the child cannot be cancelled from here -- then waits
   * on the launch in flight. A run id this process has already answered replays
   * from its own record, and one it never opened answers `undefined` and spawns
   * nothing. */
  answer(runId: string, yes: boolean): Promise<LaunchOutcome | undefined>
  /** cancelFor is rule 11's "an installation **removed or re-chosen** while the
   * window is open closes the window as a cancel", reached from the two places
   * the entry point learns that an installation moved: a choose, and a removal
   * from the rail.
   *
   * It takes a **project** rather than a run id because that is what those two
   * callers know, and it lives on the runner rather than on the confirmer
   * because the runner is the one structure that maps one to the other
   * (`inFlightByInstallation`): which launch -- if any -- is in flight for a
   * project, and under which run id, is exactly that map's answer.
   *
   * How it cancels depends on how far that launch has got, and both halves are
   * a decline that spawns nothing. Once the launch is built the entry is its
   * run id, and `confirmer.close(runId)` takes the window away. Before that --
   * while `build` is still reading the records, when the entry is the
   * reservation **token** -- there is no run id and no window yet, so the
   * reservation is marked here and the launch declines the moment its build
   * returns, **without opening a window**: closing the run id alone would miss
   * the window that opens a moment later, which is the window rule 11 says must
   * not be there.
   *
   * A project with nothing in flight is a no-op. So is one whose child is
   * already running: no window is open by then, so `close` decides nothing --
   * the same answer it gives a spent run id -- and a launch this process has
   * spawned is not cancellable from here. There is never a second `decide` for
   * one launch. */
  cancelFor(project: string): void
  /** isIdle is the launch flow's answer about itself, and it is the whole of
   * what the update policy is allowed to know about this file (Task 4.4): a
   * signed update replaces the process that owns the launch write path, so
   * nothing prompts or installs while a launch is in flight.
   *
   * `inFlight` is the whole of it. A launch enters that set with its
   * **reservation** -- before its records are read, before its window opens --
   * and leaves it when `release` frees the run id, which is the last thing the
   * launch's own `finally` does. So "idle" means no launch is being built, none
   * is being confirmed and none is running, which is exactly the window in which
   * the process may be replaced. */
  isIdle(): boolean
  /** onIdle registers a listener called when the flow **becomes** idle -- once
   * per transition, not once per settled launch, and never on a launch that
   * leaves the flow idle. It is a listener rather than a poll because the
   * moment is what the policy defers on: a download that finished while a
   * launch was in flight is offered the instant that launch settles, and a
   * caller that had to ask would have to ask on a timer. */
  onIdle(listener: () => void): void
}

/** LaunchAnswer is what an open window's `decide` resolves with: the operator's
 * answer, and the confirmer's own sentence when the answer is no. */
interface LaunchAnswer {
  readonly yes: boolean
  readonly reason?: string | undefined
}

/** refusedOutcome is the one spelling of a refusal that never spawned. */
function refusedOutcome(reason: string, runId: string | null = null): LaunchOutcome {
  return Object.freeze({ ok: false, runId, result: null, reason, outcomeUnknown: false, replayed: false })
}

/** unknownOutcome is the one spelling of a launch this process was cut off
 * from: a spawn that threw, or a child killed by a signal. It is a refusal with
 * `outcomeUnknown` set, so the run id is kept and the sentence
 * (`launchOutcomeUnknown`) names the two reads that settle it. */
function unknownOutcome(why: string, runId: string): LaunchOutcome {
  return Object.freeze({ ...refusedOutcome(launchOutcomeUnknown(runId, why), runId), outcomeUnknown: true })
}

/**
 * createLaunchRunner builds the launcher. Everything it holds lives only in
 * memory, for the life of the process: the confirmation timestamps the rate
 * limits read, the cool-down each installation is under, the launches in flight,
 * the windows that are open, the ids it has issued, and the outcomes an answered
 * run id replays from. Nothing here is written to disk (rule 10: the app writes
 * the spawn log, when asked, and the installations file, and nothing else).
 *
 * The two that grow with the launches this process has run, rather than with
 * what is unsettled, are each bounded where they can be: `issuedRunIds` keeps one
 * short string per launch for the life of the process, which is what makes a
 * drawn id unmistakably this process's, and `outcomes` is capped at
 * `MAX_RECORDED_OUTCOMES` because a replayed answer is an answer and not a
 * promise about every id ever used.
 */
export function createLaunchRunner(deps: LaunchDeps): LaunchRunner {
  const now = deps.now ?? ((): number => performance.now())
  const wallClock = deps.wallClock ?? Date.now
  const random = deps.random ?? cryptoRandom

  /** every confirmation opened in the last minute, app-wide */
  const opened: number[] = []
  /** per installation: the confirmations it opened, and its cool-down */
  const perInstallation = new Map<string, number[]>()
  const coolDownUntil = new Map<string, number>()
  /** the id of the launch in flight for each installation, and every id in
   * flight (the app-wide cap is the size of this set). A launch is in here from
   * its reservation, under a token, until its run id replaces that token. */
  const inFlightByInstallation = new Map<string, string>()
  const inFlight = new Set<string>()
  /** the run ids drawn for launches that are **not yet in flight**: a launch
   * claims its id in the same synchronous block as its reservation, and the
   * claim is what keeps two messages of one tick from drawing the same id -- so
   * it lives here rather than in `inFlight`, whose size is the app-wide cap and
   * would otherwise count one launch twice while its build was reading. An id
   * leaves this set when it enters `inFlight` (`commit`) or when the launch is
   * released without one (`launch`'s `finally`). */
  const claimedRunIds = new Set<string>()
  /** every run id this process has ever committed, as strings and nothing else.
   * An id stays here for the life of the process, so it is never drawn again: a
   * drawn id is one an operator's yes could be attached to the wrong launch, and
   * the record that makes a replay safe is capped (`outcomes`), so the ids
   * themselves cannot be. It carries no task text and no plan, which is why the
   * collision check reads it rather than the frozen objects. */
  const issuedRunIds = new Set<string>()
  /** how many reservations this process has taken, and the prefix that keeps a
   * reservation's token from ever reading as a run id (`newRunID`'s shape has no
   * colon in it) */
  let reservations = 0
  const RESERVATION_PREFIX = 'reservation:'
  /** a window that is open: the run id to the `decide` its answer resolves */
  const pending = new Map<string, (answer: LaunchAnswer) => void>()
  /** the reservations a removal or a re-choose cancelled while their launch was
   * still being built, so the launch declines instead of opening a window. A
   * token enters it (`cancelFor`) only while the token is in
   * `inFlightByInstallation`, and `launch`'s `finally` drops it on every path
   * out, so it holds at most the reservations in flight -- never a token per
   * launch this process has ever seen. */
  const cancelledReservations = new Set<string>()
  /** the outcome promise of every launch this process has opened a window for
   * and not yet finished: the run id to the promise `launch` itself resolves
   * with. It is what an answer attaches to, so a second answer for a window that
   * is open, or for one whose yes is already running, waits on the launch in
   * flight rather than starting a second child. The entry is dropped as the
   * launch settles (`settle`'s `finally`), so it holds nothing settled. */
  const settling = new Map<string, Promise<LaunchOutcome>>()
  /** the frozen object of every window this process has open, so a yes spawns
   * the launch the operator read and not a rebuilt one. It is dropped the moment
   * the answer settles (`settle`'s `finally`, and the confirmer-threw path), so
   * a settled launch holds no task text -- what a replay needs is `outcomes`,
   * which is an answer and not a task. */
  const frozen = new Map<string, FrozenLaunch>()
  /** what an already-answered run id answers with, capped at
   * `MAX_RECORDED_OUTCOMES` and written only through `recordOutcome` */
  const outcomes = new Map<string, LaunchOutcome>()
  /** who to tell when the flow becomes idle (`isIdle`), kept in insertion
   * order and copied before it is walked, so a listener that registers another
   * one is not called in the same pass */
  const idleListeners = new Set<() => void>()

  /** recordOutcome is the one write to `outcomes`, and it is where the cap is
   * kept. A `Map` iterates in insertion order, so the first key is the oldest
   * answer: the one an eviction drops once the record is over the cap. */
  function recordOutcome(runId: string, outcome: LaunchOutcome): void {
    outcomes.set(runId, outcome)
    while (outcomes.size > MAX_RECORDED_OUTCOMES) {
      const oldest = outcomes.keys().next().value
      if (oldest === undefined) break
      outcomes.delete(oldest)
    }
  }

  /** liveStamps is the window's one definition, and it is a function for that
   * reason: the rate checks **count** these and the sweep **keeps** these, and a
   * sweep whose predicate drifted from the count's would drop a stamp the rate
   * still reads (or keep every key forever) without either side looking wrong. */
  const liveStamps = (stamps: readonly number[], at: number): readonly number[] =>
    stamps.filter((stamp) => at - stamp < CONFIRM_RATE_WINDOW_MS)

  const withinWindow = (stamps: readonly number[], at: number): number => liveStamps(stamps, at).length

  /** pruneExpired drops every installation whose stamps have all fallen out of
   * the window, key and all: an installation with nothing live in the window is
   * one whose next launch is its first again, and remembering that it was asked
   * is exactly the unbounded growth `reserve` sweeps away. Deleting the entry
   * being iterated is defined for a `Map`, and so is setting a key that is
   * already there. */
  function pruneExpired(at: number): void {
    for (const [installation, stamps] of perInstallation) {
      const live = liveStamps(stamps, at)
      if (live.length === 0) perInstallation.delete(installation)
      else perInstallation.set(installation, [...live])
    }
  }

  /**
   * reserve takes a launch's **synchronous** claim on the limits -- the
   * installation's in-flight slot, one place in the app-wide count, and the
   * confirmation's rate stamp -- and returns the token that names it until the
   * launch is built and has a run id of its own.
   *
   * **Synchronous is the whole point.** The three checks above it are reads, and
   * the write used to happen after `build` was awaited: every message of a
   * same-tick burst read the limits before any of them had written, so one
   * installation's burst opened as many windows as the renderer could send,
   * against caps of one in flight and three a minute -- and the second window's
   * hold then *overwrote* the first's entry for that installation. Written here,
   * before the first `await`, the second message of a burst already sees the
   * first's reservation.
   *
   * The rate is recorded when the **request** arrives -- window or not, and for
   * a request whose build refuses and opens no window at all -- rather than when
   * it is answered: what it bounds is how often this process is made to work on
   * a launch (the records read is the work, and a request that never builds
   * still made it), which is the operator's own rate of asking. The stored
   * stamps are pruned to the window as they are written, and the installations
   * whose stamps have all expired are **dropped from the map** rather than left
   * behind with an empty list (`pruneExpired`), so neither the list nor the set
   * of names can grow without bound.
   */
  function reserve(installation: string, at: number): string {
    reservations += 1
    const token = `${RESERVATION_PREFIX}${String(reservations)}`
    inFlight.add(token)
    inFlightByInstallation.set(installation, token)
    // The map's **keys** are pruned as well as its stamps, and this is the only
    // thing that keeps it bounded: the installation name is whatever the
    // renderer sent, so a stream of names -- a renderer bending 6 requests a
    // minute into 8,600 entries a day -- would otherwise leave one entry per
    // name for the life of the process. The sweep is over the entries the
    // window permits and not over every name ever asked for: this whole map is
    // written at most `CONFIRM_RATE_APP` times a minute (the app-wide rate is
    // checked before `reserve` is reached), so it holds a handful of entries.
    pruneExpired(at)
    const mine = perInstallation.get(installation) ?? []
    perInstallation.set(installation, [...mine, at])
    opened.push(at)
    if (opened.length > CONFIRM_RATE_APP * 4) opened.splice(0, opened.length - CONFIRM_RATE_APP * 4)
    return token
  }

  /**
   * commit turns a reservation into the **run-id hold**, once the launch is
   * built: the same three structures, now named by the run id the window is
   * opened under, so the hold is the one the caps read and `release` frees.
   *
   * A launch is held **from its reservation until its child is done**, and not
   * from its spawn, because an open window is exactly what the two in-flight
   * caps are about: "a launch that is unsettled" is a click the operator has not
   * answered yet as much as it is a child that is running. Held from the
   * reservation, the caps and their two refusal sentences are true of the whole
   * unsettled interval.
   *
   * It is also where the id's **claim** ends: `launch` claimed the id before its
   * first `await` (`claimedRunIds`), and from this line the id is held in
   * `inFlight`, so the two sets never hold one id at once and `launch`'s
   * `finally` can delete from both without caring which one had it.
   *
   * The entry is released on every path out of that interval: a build that
   * refuses or throws, a confirmer that throws, an answer of no, a launch
   * declined because the installation moved, a child that finished, and a child
   * that could not start. A confirmer therefore owes this launcher exactly one
   * answer per window it opens (`LaunchConfirmer`), and both of the ways a window
   * is answered here -- `answer()`, and the `decide` the confirmer was handed --
   * go through `settle`, so no window can leave an entry held.
   */
  function commit(token: string, launch: FrozenLaunch): void {
    inFlight.delete(token)
    inFlight.add(launch.runId)
    claimedRunIds.delete(launch.runId)
    issuedRunIds.add(launch.runId)
    inFlightByInstallation.set(launch.project, launch.runId)
  }

  /** release frees a run id, and the installation's entry only when that entry
   * is still **this** run's: an earlier launch finishing must never clear a later
   * one's hold, which would admit a third launch beside a second that is still
   * in flight. */
  function release(runId: string, project: string): void {
    const wasBusy = inFlight.size > 0
    inFlight.delete(runId)
    if (inFlightByInstallation.get(project) === runId) inFlightByInstallation.delete(project)
    // The idle listeners fire on the **transition**, which is why they are
    // announced from here and not from `settle`: a release that finds the set
    // already empty is the second half of a launch that has just been freed
    // (the token after the run id) and tells nobody anything.
    if (!wasBusy || inFlight.size > 0) return
    for (const listener of [...idleListeners]) listener()
  }

  /**
   * build freezes the whole spawn, and it is the only place a launch's argv is
   * built. Every refusal it can make is made **before** the confirmer is asked,
   * which is what "the window does not open for a launch that cannot happen"
   * means in code.
   *
   * The run id arrives from `launch` rather than being drawn here, and that is
   * deliberate: it is claimed **synchronously with the reservation**, before this
   * function's first `await`, so that two messages of one tick cannot both draw
   * it (see the claim in `launch`). A request that this function refuses still
   * had an id drawn and claimed for it, and `launch`'s `finally` frees it with
   * the reservation.
   */
  async function build(request: LaunchRequest, runId: string): Promise<
    { readonly ok: true; readonly launch: FrozenLaunch } | { readonly ok: false; readonly reason: string }
  > {
    const looked = await deps.lookUp(request.installation)
    if (!looked.ok) return looked
    const target = looked.target

    // The target's project **is** the name that was asked for, and this is what
    // makes the two in-flight keys one key: the ceilings at the top of `launch`
    // are read against the name a renderer sent, and the hold is taken against
    // the project of the frozen object -- so a lookup that answered with another
    // installation's records would hold a name nobody asked about and leave the
    // asked-for one free. The console's own lookup cannot do that (it builds the
    // target from the argument it was given), which is why this is a refusal by
    // name rather than a silent correction: a launcher whose two halves can
    // disagree about which installation a launch is for is a fence with a gap in
    // it, whatever the one caller does today.
    if (target.project !== request.installation) {
      return {
        ok: false,
        reason:
          `the records answered for ${JSON.stringify(target.project)} when ${JSON.stringify(request.installation)} ` +
          'was asked about: a launch is built for one installation or for none',
      }
    }

    // The floor is a **version comparison** and not membership in the console's
    // supported range: the range is minor-granular and the floor is a patch, so
    // `0.11.0` is inside "0.11" and still below `planTaskSince`'s `0.11.2`.
    if (!launchSupported(target.cliVersion)) {
      return {
        ok: false,
        reason:
          `the CLI this installation resolves is ${target.cliVersion === '' ? 'unknown' : target.cliVersion}: ` +
          'a launch needs a CLI that carries `--plan-task` (0.11.2 or newer)',
      }
    }

    // A `distro` is the WSL route, and a launch cannot take it yet: the task has
    // to reach the CLI on the distribution's own stdin, and the console's
    // wrapper for that route is not built to carry it. The refusal is by name
    // and not a fallback, because the one thing this row must never do is spawn
    // with `'ignore'` and an empty task -- a launch that ran with no task at all
    // is a spend nobody asked for.
    if (target.distro !== undefined && target.distro !== '') {
      return {
        ok: false,
        reason:
          `this installation’s CLI runs inside the ${target.distro} distribution, and this console cannot ` +
          'yet hand a launch’s task to that route: run it from a terminal, or use an installation that ' +
          'runs the CLI directly',
      }
    }

    // A recorded `configPath` is refused **whatever it names**, and this is
    // rule 11's own rule read strictly: the row passes `--config` only for a
    // path it can decide is that project's own projects file, and deciding that
    // means reading the CLI's configuration directory, which section 3.4 rule 7
    // forbids the app to do. So a path this console cannot check is a path it
    // does not pass and an installation it does not launch -- the direction that
    // spends nothing rather than the one that spends from a config nobody
    // verified.
    if (target.configPath !== undefined && target.configPath !== '') {
      return {
        ok: false,
        reason:
          'this installation’s records set a config path of their own, and this console cannot tell whether ' +
          'that path is the project’s own: launch it from a terminal instead',
      }
    }

    const converted = launchRepoArgument(request.repository)
    if (!converted.ok) return { ok: false, reason: converted.reason }

    // The repository must be one the installation's own `config list --json`
    // read carried: the identity a renderer sends is checked against the
    // console's records rather than trusted, and `--repo` is built from the
    // record's spelling and not from the request's -- so a repository this
    // installation does not enroll is a launch that is not built at all. The
    // conversion is checked first, because an identity the conversion refuses is
    // not one this app could have printed from any record.
    const recorded = target.records.repositories.find((row) => row.repository === request.repository)
    if (recorded === undefined) {
      return {
        ok: false,
        reason:
          `the installation’s \`config list --json\` carries no checkout of ${request.repository}: ` +
          'a launch is cut only for a repository the installation records',
      }
    }

    const branch = recordedBaseBranch(target.records, request.repository)
    if (!branch.ok) return { ok: false, reason: branch.reason }
    const checkedBranch = checkLaunchBranch(branch.value)
    if (!checkedBranch.ok) return { ok: false, reason: checkedBranch.reason }

    const task = checkLaunchTask(request.task)
    if (!task.ok) return { ok: false, reason: task.reason }
    const title = checkLaunchTitle(request.title)
    if (!title.ok) return { ok: false, reason: title.reason }
    const planTask = checkLaunchPlanTask(request.planTask)
    if (!planTask.ok) return { ok: false, reason: planTask.reason }

    // The child's working directory is always the app's own private, empty
    // directory -- never the operator's checkout, the installation's recorded
    // one included. `newRunRef` cuts a run from the branch the *working
    // directory's* checkout is on, so a launch spawned in a checkout could take
    // its ref from whatever the operator happens to have checked out, invisibly.
    const cwd = privateWorkingDirectory({})
    const context = {
      target: { project: target.project, configPath: target.configPath },
      binaryPath: target.binaryPath,
      directory: cwd,
      appEnv: deps.appEnv,
      runId,
    }
    const args = {
      task: task.value.text,
      repository: converted.value,
      ...(title.value === null ? {} : { title: title.value }),
      ...(planTask.value === null ? {} : { planTask: planTask.value }),
    }
    const plan = planFor('run.launch', args, context)
    const encoded = new Uint8Array(new TextEncoder().encode(task.value.text))

    return {
      ok: true,
      launch: Object.freeze({
        runId,
        project: target.project,
        recordedRepository: request.repository,
        repository: converted.value,
        branch: checkedBranch.value,
        title: title.value,
        planTask: planTask.value,
        taskText: task.value.text,
        taskBytes: encoded,
        fingerprint: target.fingerprint,
        argv: plan.argv,
        cwd: plan.cwd,
        env: plan.env,
        plan,
      }),
    }
  }

  /** spawn runs the frozen plan and decodes the reply. It is reached only from a
   * yes-answer, once per run id. */
  async function spawn(launch: FrozenLaunch): Promise<LaunchOutcome> {
    let outcome: LaunchOutcome
    try {
      const result = await runToCompletion(launch.plan, {
        spawner: deps.spawner,
        log: deps.log,
        timers: deps.timers,
        timeoutMs: LAUNCH_TIMEOUT_MS,
        stdinBytes: launch.taskBytes,
      })
      outcome = decodeReply(launch, result.code, result.signal, result.stdout, result.stderr)
    } catch (error) {
      const why = error instanceof Error ? error.message : String(error)
      outcome = unknownOutcome(why, launch.runId)
    }
    release(launch.runId, launch.project)
    recordOutcome(launch.runId, outcome)
    return outcome
  }

  /** decodeReply is the one reader of the child's answer: the CLI's own sentence
   * for a non-zero exit, and otherwise the closed decoder plus the two identity
   * checks that make the reply this launch's. */
  function decodeReply(
    launch: FrozenLaunch,
    code: number | null,
    signal: string | null,
    stdout: string,
    stderr: string,
  ): LaunchOutcome {
    // A signal is **not** an exit status, and this check comes first because a
    // killed child answers `code: null`: a CLI killed from outside -- the
    // operator's task manager, a session ending, a machine shutting down --
    // would otherwise be reported as "exited with status null", a refusal that
    // says the launch did not happen, when this console in fact knows nothing
    // about what the CLI did before it was killed. It may have claimed the run
    // id and started a container, so the answer keeps the run id and says the
    // outcome is unknown.
    if (signal !== null) return unknownOutcome(`the launch was killed by ${signal}`, launch.runId)
    if (code !== 0) {
      const sentence = cliSentence(stderr)
      return refusedOutcome(sentence ?? `the launch exited with status ${String(code)}`, launch.runId)
    }
    const decoded = decodeLaunchResult(stdout)
    if (!decoded.ok) return refusedOutcome(decoded.reason, launch.runId)
    // `run_id` and `repo` are the CLI's own values and they are checked against
    // the frozen ones rather than trusted: a reply about another run is a reply
    // this console must not draw as this launch's.
    if (decoded.value.runId !== launch.runId) {
      return refusedOutcome(
        `the launch answered about run ${decoded.value.runId}, not the run id this console generated`,
        launch.runId,
      )
    }
    if (decoded.value.repo !== launch.repository) {
      return refusedOutcome(
        `the launch answered about ${decoded.value.repo}, not the repository this launch was cut for`,
        launch.runId,
      )
    }
    return Object.freeze({
      ok: true,
      runId: launch.runId,
      result: decoded.value,
      reason: '',
      outcomeUnknown: false,
      replayed: false,
    })
  }

  /** settle is the answer path: the one place a window's yes reaches a spawn,
   * and the one place every entry it and `launch` took is released -- the frozen
   * object and the settling promise included, because a launch that has answered
   * holds neither a task text nor a window. */
  async function settle(runId: string, yes: boolean, reason?: string): Promise<LaunchOutcome> {
    const launch = frozen.get(runId)
    if (launch === undefined) return refusedOutcome(DECLINED_CANCELLED, runId)
    try {
      if (!yes) {
        const declined = refusedOutcome(reason ?? DECLINED_CANCELLED, runId)
        recordOutcome(runId, declined)
        return declined
      }
      // An installation removed, re-added or changed while the window was open
      // closes it as a cancel: the frozen object is the launch, and the launch
      // is for a record this process no longer has.
      //
      // A read that **rejects** answers no rather than propagating: the check
      // exists so that a launch cannot outlive the installation it was cut for,
      // and a store this process cannot read is not evidence that it can. A
      // rejection let out of here would leave the hold taken and this
      // installation unable to launch again for the life of the process -- and
      // the decline it turns into spawns nothing, which is the direction that
      // spends nothing.
      let still = false
      try {
        still = await deps.stillRecorded(launch)
      } catch {
        still = false
      }
      if (!still) {
        const declined = refusedOutcome(DECLINED_CANCELLED, runId)
        recordOutcome(runId, declined)
        return declined
      }
      coolDownUntil.set(launch.project, now() + LAUNCH_COOL_DOWN_MS)
      return await spawn(launch)
    } finally {
      release(runId, launch.project)
      frozen.delete(runId)
      settling.delete(runId)
    }
  }

  return {
    async launch(request: LaunchRequest): Promise<LaunchOutcome> {
      // The ceilings come first, and deliberately before the records are read:
      // a renderer leaning on this operation must not be able to make the
      // console work -- a spawn of `config list`, a store read -- on every
      // message. The name it gave is the key, and a name that is **not** an
      // installation is refused by rate as readily as by identity, because the
      // stamp is taken (below, in `reserve`) before the records are read rather
      // than only once a launch has been built: without that, a stream of names
      // the store does not carry would cost this process a full read each and
      // never a stamp.
      //
      // The two in-flight caps and the two rates are read here and **written
      // synchronously below, before the first `await`**: an open window is one
      // of the launches counted, so a burst of `run.launch` messages in a single
      // tick -- before any cool-down exists -- cannot open a second window for
      // an installation, nor a third app-wide. Reading the limits here and
      // writing them after `build` had awaited would let every message of that
      // burst through all four checks.
      const at = now()
      if (inFlightByInstallation.has(request.installation)) {
        return refusedOutcome(
          `a launch for ${request.installation} is already in flight: this console runs one launch at a time ` +
            'per installation and does not queue them',
        )
      }
      if (inFlight.size >= MAX_IN_FLIGHT) {
        return refusedOutcome(
          `two launches are already in flight: this console runs at most ${String(MAX_IN_FLIGHT)} at a time ` +
            'and does not queue them',
        )
      }
      const coolDown = coolDownUntil.get(request.installation) ?? 0
      if (at < coolDown) {
        return refusedOutcome(
          `a launch for ${request.installation} was accepted moments ago: wait ` +
            `${String(Math.ceil((coolDown - at) / 1000))} seconds before confirming another`,
        )
      }
      const mine = perInstallation.get(request.installation) ?? []
      if (withinWindow(mine, at) >= CONFIRM_RATE_PER_INSTALLATION) {
        return refusedOutcome(
          `this installation has been asked to confirm ${String(CONFIRM_RATE_PER_INSTALLATION)} launches in ` +
            'the last minute: wait a moment before asking again',
        )
      }
      if (withinWindow(opened, at) >= CONFIRM_RATE_APP) {
        return refusedOutcome(
          `the console has been asked to confirm ${String(CONFIRM_RATE_APP)} launches in the last minute: ` +
            'wait a moment before asking again',
        )
      }

      // The three writes the checks above were reads of. `reserve` is
      // synchronous and it is the last thing before the first `await`, so the
      // next message of a same-tick burst sees this launch already counted.
      const token = reserve(request.installation, at)
      // The id, drawn and claimed below, is declared out here so the `finally`
      // can free it: the draw itself is inside the `try` and not beside the
      // reservation, because `random` is the source of the id's four hex digits
      // and a source that throws (`cryptoRandom` reads two bytes from
      // `node:crypto`) must not take the reservation with it -- the token is in
      // `inFlight` and in `inFlightByInstallation` from `reserve`, and a throw
      // between the two would block that installation and hold an app-wide slot
      // for the life of the process.
      let runId: string | null = null

      try {
        // The id is drawn **and claimed here**, in the same synchronous block as
        // the reservation and for the same reason: the four hex digits are the
        // whole of the id's entropy, and the id keys both maps the answer path
        // reads -- `frozen` (what a yes spawns) and `pending` (whose `decide` an
        // answer resolves) -- so two launches sharing one id means the second
        // window's entries replace the first's and the first window's yes spawns
        // the second launch. Claimed before this launch's first `await`, the id
        // is one a same-tick message's draw is already refused; drawn where the
        // launch is *built* instead, the second message of a same-tick pair
        // would draw the first's id while the first was still reading its
        // records -- nothing claimed, nothing issued, nothing committed for it
        // to see.
        runId = drawRunID(
          new Date(wallClock()),
          random,
          (id) => claimedRunIds.has(id) || inFlight.has(id) || issuedRunIds.has(id),
        )
        if (runId === null) {
          // A refusal about this console's own bookkeeping rather than about the
          // operator, and before any window: an id this process already holds is
          // one an operator's yes could be attached to the wrong launch, which
          // is worse than asking again a second later. The slot goes back with
          // the refusal (the `finally`); the rate stamp does not, like every
          // other refusal here.
          return refusedOutcome(
            `this console drew ${String(RUN_ID_DRAWS)} run ids that another launch of this process already holds: ` +
              'the id is the current second and four hex digits, so wait a moment and ask for the launch again',
          )
        }
        claimedRunIds.add(runId)

        const built = await build(request, runId)
        // A refusal from `build` **keeps the rate stamp**: a request that cannot
        // be built is still a request the operator's console had to read the
        // records for, and a renderer that asked for a launch that never builds
        // must not be able to ask without limit. The slot and the app-wide count
        // are freed, because nothing is in flight.
        if (!built.ok) return refusedOutcome(built.reason)

        // An installation removed or re-chosen while this launch was still
        // being built: the window it would open now is the one rule 11 says
        // must not be there, so it is declined here instead -- no window, no
        // child, and the reservation released on the way out. The id was drawn
        // and claimed but never issued, so there is no outcome to record: no
        // sender ever saw it.
        if (cancelledReservations.delete(token)) {
          const declined = refusedOutcome(DECLINED_CANCELLED, built.launch.runId)
          return declined
        }

        frozen.set(built.launch.runId, built.launch)
        commit(token, built.launch)

        const decided = new Promise<LaunchAnswer>((resolve) => {
          pending.set(built.launch.runId, resolve)
        })
        // The promise this call resolves with, kept where `answer` can reach it
        // from the moment the window opens: an answer that arrives after the
        // window's own `decide` has been resolved -- the operator clicking
        // twice, a re-sent IPC call -- attaches to the launch already in flight
        // and waits on this, rather than returning nothing or starting a second
        // child. Set here and not after the `await`, so an answer made in the
        // same tick as the window's own finds it.
        const settled = decided.then((answer) => settle(built.launch.runId, answer.yes, answer.reason))
        settling.set(built.launch.runId, settled)
        try {
          deps.confirmer.open(confirmView(built.launch), (yes, reason) => {
            const resolve = pending.get(built.launch.runId)
            if (resolve === undefined) return
            pending.delete(built.launch.runId)
            resolve(reason === undefined ? { yes } : { yes, reason })
          })
        } catch {
          // A confirmer that throws is a window that did not open: there is
          // nobody to ask, so the launch is declined here rather than left
          // pending -- a `decided` nothing will ever resolve would hold this run
          // id, and its installation's slot, for the life of the process. The
          // frozen object and the settling promise go with it: no window exists
          // to answer from either.
          pending.delete(built.launch.runId)
          frozen.delete(built.launch.runId)
          settling.delete(built.launch.runId)
          const declined = refusedOutcome(DECLINED_CANCELLED, built.launch.runId)
          recordOutcome(built.launch.runId, declined)
          return declined
        }
        return await settled
      } finally {
        // The one exit this file's own paths do not cover: if the draw, `build`
        // or the confirmer threw outright, the reservation, the id's claim and
        // the hold (`settle` has already freed the last of them) are released
        // here. All three are released whatever happened, because they are this
        // call's whether or not the launch was built: `release(token, …)` frees
        // the reservation while the installation's entry is still the token,
        // `release(runId, …)` frees the id and the entry the commit moved it to,
        // and `claimedRunIds.delete` frees a claim the commit never took over --
        // the only release a refused or thrown `build` gets, since it never
        // reaches `commit`. `release` is idempotent, so the ordinary paths
        // through `settle` are unaffected by it, and `runId` is null only when
        // the draw itself threw.
        //
        // The reservation's cancel mark goes with them: whether the launch
        // declined on it or the mark was never read, this call is over and the
        // token will never be looked up again.
        cancelledReservations.delete(token)
        release(token, request.installation)
        if (runId !== null) {
          release(runId, request.installation)
          claimedRunIds.delete(runId)
        }
      }
    },

    async answer(runId: string, yes: boolean): Promise<LaunchOutcome | undefined> {
      // The in-flight case: an answer for a window that is still open resolves
      // that window's `decide` -- the first answer only, as it always has -- and
      // then waits on the launch itself. A later answer, while the child runs,
      // finds no `decide` left and waits on the same launch: it is attached to
      // the launch in flight, a yes and a no alike (the child cannot be
      // cancelled from here), and no second child starts.
      const resolve = pending.get(runId)
      if (resolve !== undefined) {
        pending.delete(runId)
        resolve({ yes })
      }
      const attached = settling.get(runId)
      if (attached !== undefined) return await attached
      // The replay case: a second answer for a run id that has been answered is
      // answered from this process's own record, and never spawns again.
      const recorded = outcomes.get(runId)
      if (recorded !== undefined) return Object.freeze({ ...recorded, replayed: true })
      // A run id this process knows nothing about: it spawns nothing, ever.
      return undefined
    },

    cancelFor(project: string): void {
      const inFlight = inFlightByInstallation.get(project)
      if (inFlight === undefined) return
      // The entry is the reservation token while `build` is reading the records,
      // and the run id from `commit` until the launch is released: the token has
      // no window to close, so it is marked and the launch declines when its
      // build returns.
      if (inFlight.startsWith(RESERVATION_PREFIX)) {
        cancelledReservations.add(inFlight)
        return
      }
      deps.confirmer.close(inFlight)
    },

    isIdle(): boolean {
      return inFlight.size === 0
    },

    onIdle(listener: () => void): void {
      idleListeners.add(listener)
    },
  }
}

/** launchSpawnedArgv is the whole command line a frozen launch would spawn,
 * which is what the spawn log records and what a test reads instead of a
 * process. */
export function launchSpawnedArgv(launch: FrozenLaunch): readonly string[] {
  return spawnedArgv(launch.plan)
}
