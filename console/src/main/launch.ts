// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The launch, which is the console's **one write** (design section 5.1 rule 11,
// D22, 2026-10-16). Everything in this file exists because of one fact: a
// `fugaro run` spends money, and `fugaro run` prompts for nothing -- there is no
// `--yes` and no question (`internal/cli/run.go`), so the confirmation is the
// console's own and there is nothing behind it.
//
// **What this file is, and what it deliberately is not.** V2.5.1a is split in
// three: part 1 (the pure rules: `launchrules.ts`, `installlist.ts`'s
// `base_branch` read, `launchSupported` in `src/shared/version.ts`), this part
// (the fence move: the operation, its spawn row, the frozen spawn and the
// limits) and part 2b (the confirmation window). So the confirmation here is a
// **seam** -- `LaunchConfirmer` -- and the production implementation declines
// every launch with `DECLINED_UNBUILT`. That is not a placeholder to be tidied
// away later: it is the property that makes this part shippable at all, because
// it means **no `fugaro run` child can be spawned by the shipped app until the
// window exists**, while the operation, the argv, the reachability table and the
// limits are all in place and tested with a fake confirmer.
//
// Nothing here opens a window. `index.ts` wires the handler with
// `DECLINING_CONFIRMER`; nothing else may.

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
 * minute across the whole app. The cap is on **opening the confirmation**, not
 * on accepting one: a refused window is still a window that took the operator's
 * attention, and a stream of them is a denial of service against the person at
 * the keyboard rather than against the CLI. */
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

/** DECLINED_UNBUILT is the production confirmer's answer, and the sentence is
 * deliberately about this tree rather than about the operator's click: nothing
 * asked them anything. It is the refusal a shipped console gives every launch
 * until V2.5.1b's window lands. */
export const DECLINED_UNBUILT = 'the launch confirmation window is not built yet'

/** DECLINED_CANCELLED is what a declined window answers with once 2b builds it:
 * declining spawns nothing, and the sentence says so rather than looking like a
 * failure. */
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

/**
 * cryptoRandom is the default source of the id's four hex digits, and it is
 * `node:crypto`'s two bytes rather than `Math.random`'s because of what a
 * collision costs: two launches in the same UTC second whose tails collide
 * produce one id, the CLI's claim then holds it, and the second launch comes
 * back `already-launched` -- which D23 has this console draw as a **fact rather
 * than an error**, so a confirmed launch that spends nothing would say nothing
 * about why. `task.NewRunID` draws its own tail from `crypto/rand`
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
 * LaunchConfirmer is the seam V2.5.1b fills with the window. `open` is handed
 * the frozen launch and a `decide` it calls when the operator answers -- the
 * main process's own path, which is what `answer(runId, yes)` reaches from the
 * window's preload; the renderer asks for the window and **cannot answer for the
 * operator**.
 *
 * `decide`'s second parameter is **the confirmer's own sentence for a decline**,
 * and it exists for exactly one reason: the sentence the operator is shown for a
 * decline is the console's, not the CLI's, and V2.5.1a's confirmer declines for
 * a reason the operator must be able to read -- the window does not exist yet
 * (`DECLINED_UNBUILT`) -- which is not the same sentence as a window the
 * operator closed (`DECLINED_CANCELLED`). It is optional, and the window's own
 * main-side path (`answer(runId, yes)`) never passes one: a decline that came
 * from the operator is the cancel sentence and nothing else.
 */
export interface LaunchConfirmer {
  open(launch: FrozenLaunch, decide: (yes: boolean, reason?: string) => void): void
}

/** DECLINING_CONFIRMER is V2.5.1a's production implementation: it declines every
 * launch, by name, so the shipped app has no way to a `run` child at all. */
export const DECLINING_CONFIRMER: LaunchConfirmer = Object.freeze({
  open: (_launch: FrozenLaunch, decide: (yes: boolean, reason?: string) => void): void => {
    decide(false, DECLINED_UNBUILT)
  },
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
  /** stillRecorded answers whether an installation is in the records *now*: a
   * window whose installation was removed or re-chosen while it was open closes
   * as a cancel, and this is the check that makes that true rather than
   * promised. It may be async because the records are read from the CLI's own
   * list, and a stale answer here is a launch for an installation the operator
   * has just removed. */
  readonly stillRecorded: (project: string) => Promise<boolean> | boolean
  readonly appEnv: Readonly<Record<string, string | undefined>>
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timers?: Timers
  /** now and random are the clock and the id's hex tail, both injectable so the
   * rate limits, the cool-down and the run id are testable without waiting. */
  readonly now?: () => number
  readonly random?: () => number
}

/** LaunchRunner is the launcher the entry point wires the handler to. */
export interface LaunchRunner {
  launch(request: LaunchRequest): Promise<LaunchOutcome>
  /** answer is the confirmation window's own main-side path: the yes/no the
   * operator gave, for the run id that window was opened under. */
  answer(runId: string, yes: boolean): Promise<LaunchOutcome | undefined>
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

/**
 * createLaunchRunner builds the launcher. It holds four pieces of state and all
 * four live only in memory, for the life of the process: the confirmation
 * timestamps the rate limits read, the cool-down each installation is under, the
 * launches in flight, and the outcomes an answered run id replays from. Nothing
 * here is written to disk (rule 10: the app writes the spawn log, when asked, and
 * the installations file, and nothing else).
 */
export function createLaunchRunner(deps: LaunchDeps): LaunchRunner {
  const now = deps.now ?? Date.now
  const random = deps.random ?? cryptoRandom

  /** every confirmation opened in the last minute, app-wide */
  const opened: number[] = []
  /** per installation: the confirmations it opened, and its cool-down */
  const perInstallation = new Map<string, number[]>()
  const coolDownUntil = new Map<string, number>()
  /** the id of the launch in flight for each installation, and every id in
   * flight (the app-wide cap is the size of this set) */
  const inFlightByInstallation = new Map<string, string>()
  const inFlight = new Set<string>()
  /** a window that is open: the run id to the `decide` its answer resolves */
  const pending = new Map<string, (answer: LaunchAnswer) => void>()
  /** the frozen object of every window this process opened, so a replayed answer
   * spawns the same launch and not a rebuilt one */
  const frozen = new Map<string, FrozenLaunch>()
  /** what an already-answered run id answers with, for the life of the process */
  const outcomes = new Map<string, LaunchOutcome>()

  const withinWindow = (stamps: readonly number[], at: number): number =>
    stamps.filter((stamp) => at - stamp < CONFIRM_RATE_WINDOW_MS).length

  /**
   * hold registers a launch as in flight **when its window opens**, and not when
   * its child starts, because an open window is exactly what the two in-flight
   * caps are about: "a launch that is unsettled" is a click the operator has not
   * answered yet as much as it is a child that is running, and a renderer that
   * leans on this operation can have several windows open in one tick -- before
   * any cool-down exists, since a cool-down starts at an accepted launch. Held
   * from the window rather than from the spawn, the caps and their two refusal
   * sentences are true of the whole unsettled interval.
   *
   * The entry is released on every path out of that interval: an answer of no, a
   * launch declined because the installation moved, a child that finished, and a
   * child that could not start. A confirmer therefore owes this launcher exactly
   * one answer per window it opens (`LaunchConfirmer`), and both of the ways a
   * window is answered here -- `answer()`, and the `decide` the confirmer was
   * handed -- go through `settle`, so no window can leave an entry held.
   */
  function hold(launch: FrozenLaunch): void {
    inFlight.add(launch.runId)
    inFlightByInstallation.set(launch.project, launch.runId)
  }

  /** release frees a run id, and the installation's entry only when that entry
   * is still **this** run's: an earlier launch finishing must never clear a later
   * one's hold, which would admit a third launch beside a second that is still
   * in flight. */
  function release(runId: string, project: string): void {
    inFlight.delete(runId)
    if (inFlightByInstallation.get(project) === runId) inFlightByInstallation.delete(project)
  }

  /**
   * build freezes the whole spawn, and it is the only place a launch's argv is
   * built. Every refusal it can make is made **before** the confirmer is asked,
   * which is what "the window does not open for a launch that cannot happen"
   * means in code.
   */
  async function build(request: LaunchRequest): Promise<
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

    const runId = newRunID(new Date(now()), random)
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
      outcome = decodeReply(launch, result.code, result.stdout, result.stderr)
    } catch (error) {
      const why = error instanceof Error ? error.message : String(error)
      outcome = refusedOutcome(launchOutcomeUnknown(launch.runId, why), launch.runId)
      outcome = Object.freeze({ ...outcome, outcomeUnknown: true })
    }
    release(launch.runId, launch.project)
    outcomes.set(launch.runId, outcome)
    return outcome
  }

  /** decodeReply is the one reader of the child's answer: the CLI's own sentence
   * for a non-zero exit, and otherwise the closed decoder plus the two identity
   * checks that make the reply this launch's. */
  function decodeReply(
    launch: FrozenLaunch,
    code: number | null,
    stdout: string,
    stderr: string,
  ): LaunchOutcome {
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

  /** settle is the answer path: the one place a window's yes reaches a spawn. */
  async function settle(runId: string, yes: boolean, reason?: string): Promise<LaunchOutcome> {
    const launch = frozen.get(runId)
    if (launch === undefined) return refusedOutcome(DECLINED_CANCELLED, runId)
    if (!yes) {
      const declined = refusedOutcome(reason ?? DECLINED_CANCELLED, runId)
      release(runId, launch.project)
      outcomes.set(runId, declined)
      return declined
    }
    // An installation removed or re-chosen while the window was open closes it
    // as a cancel: the frozen object is the launch, and the launch is for an
    // installation this process no longer has.
    if (!(await deps.stillRecorded(launch.project))) {
      const declined = refusedOutcome(DECLINED_CANCELLED, runId)
      release(runId, launch.project)
      outcomes.set(runId, declined)
      return declined
    }
    coolDownUntil.set(launch.project, now() + LAUNCH_COOL_DOWN_MS)
    return spawn(launch)
  }

  return {
    async launch(request: LaunchRequest): Promise<LaunchOutcome> {
      // The ceilings come first, and deliberately before the records are read:
      // a renderer leaning on this operation must not be able to make the
      // console work -- a spawn of `config list`, a store read -- on every
      // message. The name it gave is the key, and a name that is not an
      // installation is refused by rate as readily as by identity.
      //
      // The two in-flight caps are read against `hold`, which is taken when a
      // window opens: an open window is one of the launches counted, so a burst
      // of `run.launch` messages in a single tick -- before any cool-down
      // exists -- cannot open a second window for an installation, nor a third
      // app-wide.
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
          `${String(CONFIRM_RATE_APP)} launches have been confirmed in the last minute: wait a moment ` +
            'before asking again',
        )
      }

      const built = await build(request)
      if (!built.ok) return refusedOutcome(built.reason)

      // The rate is recorded when the window opens, not when it is answered:
      // the operator's attention is what this bounds. The stored list is pruned
      // to the window as it is written, so it cannot grow without bound.
      const recent = [...mine, at].filter((stamp) => at - stamp < CONFIRM_RATE_WINDOW_MS)
      opened.push(at)
      if (opened.length > CONFIRM_RATE_APP * 4) opened.splice(0, opened.length - CONFIRM_RATE_APP * 4)
      perInstallation.set(request.installation, recent)
      frozen.set(built.launch.runId, built.launch)
      // The hold is taken here, with the window: from this line until the
      // window is answered and its child is done, this run id counts against
      // both in-flight caps.
      hold(built.launch)

      const decided = new Promise<LaunchAnswer>((resolve) => {
        pending.set(built.launch.runId, resolve)
      })
      deps.confirmer.open(built.launch, (yes, reason) => {
        const resolve = pending.get(built.launch.runId)
        if (resolve === undefined) return
        pending.delete(built.launch.runId)
        resolve(reason === undefined ? { yes } : { yes, reason })
      })
      const answered = await decided
      return settle(built.launch.runId, answered.yes, answered.reason)
    },

    async answer(runId: string, yes: boolean): Promise<LaunchOutcome | undefined> {
      // The in-flight case: a second answer for a window that is still open is
      // attached to that launch, and no second child starts.
      const resolve = pending.get(runId)
      if (resolve !== undefined) {
        pending.delete(runId)
        resolve({ yes })
        return undefined
      }
      // The replay case: a second answer for a run id that has been answered is
      // answered from this process's own record, and never spawns again.
      const recorded = outcomes.get(runId)
      if (recorded !== undefined) return Object.freeze({ ...recorded, replayed: true })
      // A run id this process knows nothing about: it spawns nothing, ever.
      return undefined
    },
  }
}

/** launchSpawnedArgv is the whole command line a frozen launch would spawn,
 * which is what the spawn log records and what a test reads instead of a
 * process. */
export function launchSpawnedArgv(launch: FrozenLaunch): readonly string[] {
  return spawnedArgv(launch.plan)
}
