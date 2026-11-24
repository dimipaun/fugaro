// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The models write's main-process half (the console-improvements plan, task
// MW1: D2903, O4; the design is `docs/desktop-console/spec.md` section 5.1
// rules 11 and 12, and the CLI's own document is
// `schemas/models-add-plan.schema.json`, MD1).
//
// **What this module is.** The whole of one `models add` that is the main
// process's, and it is the wizard's own shape (`wizardrun.ts`) for a write that
// holds no bytes of its own: it runs `fugaro models add … --no-apply --dry-run
// --json` with the values the renderer sent **after main validated every one of
// them against a grammar** (`ipc.ts`'s `modelId`, `modelRoute`, `modelPrice`),
// decodes the versioned document the child printed, holds that document, its
// digest and the **exact argv** in the window instance that showed them, shows
// SU1's one window the document as it parsed it, and, once that window's own top
// frame has said yes, runs the same argv without `--dry-run --json` -- the argv
// main built, never one the renderer sent again.
//
// **The write is a widening, and the window is where that is decided.** `models
// add` appends the model to a route's own list in the project config, writes a
// `model_prices` entry and appends a `budget.allowed_models` entry, so it
// changes what every run of the project may name and spend on -- there is no
// narrowing form of it. The view carries the tick (`modelsview.ts`) and
// `confirm.ts` refuses a yes on this subject without it, which is the gate and
// not a disabled button.
//
// **`--no-apply` is main's word and is a word of the row, not of a payload.** The
// steps that make the change live are saved for `fugaro apply` (D1565), so the
// write changes the project config and runs nothing else -- and the document the
// window shows already says `saved` for them, which is what apply does.
//
// **Nothing is registered here.** The two names are not in `NewOperationName`
// (the contract deliberately stops at AL's nine, `src/shared/new-ops.ts`), so
// there is no `handlersFor` row to build: the handlers below are built over a
// `ModelsAddPlanner` seam and the wiring task spreads them into the `gated` table
// with its own planner and floor, exactly as `wizardrun.ts` waits for I-OPS.
// Everything here is tested against a stub child and a stub confirmer.

import { createHash } from 'node:crypto'

import { cliSentence, runToCompletion, READ_TIMEOUT_MS } from './spawn'
import type { ChildSpawner, RunResult, SpawnPlan, Timers } from './spawn'
import type { SpawnLogWriter } from './spawnlog'
import type { LaunchConfirmer } from './launch'
import type { ValidatedArguments } from './ipc'
import { modelsAddView } from './modelsview'
import type { ModelsAddView } from './modelsview'
import { LAPSE_REFUSAL } from './licence-state'
import { SourceRefusal } from '../source/source'
import { MODELS_ADD_CLI_FLOOR, UNREADABLE_VERSION, meetsCliFloor } from '../shared/version'

/** ModelsAddSpawnName is the two operations this module drives, spelled as
 * `CONSOLE_OPERATIONS` spells them. */
export type ModelsAddSpawnName = 'models.add.plan' | 'models.add'

/** MODELS_ADD_WORD is how the write names itself in the one sentence the console
 * writes about it. */
export const MODELS_ADD_WORD = 'the models write'

/** MODELS_ADD_COMMAND is the line the console hands an operator whose CLI is
 * below the floor: a write with no window is still a write the operator can run
 * by hand, and the sentence names the flags the console would have passed. */
export const MODELS_ADD_COMMAND = 'fugaro models add <id> --route <route>'

/** MODELS_ADD_FLOOR_UNSET is what a write whose floor is still `null` answers:
 * the floor is unset until a CLI release carries `--no-apply` and the plan
 * document (`MODELS_ADD_CLI_FLOOR`), and a console that spawned the command
 * anyway would run an argv the installed CLI refuses -- or, worse, one it
 * accepts and runs the follow-up steps of. */
export const MODELS_ADD_FLOOR_UNSET = 'needs a fugaro release no CLI has yet'

/** modelsAddFloorSentence is the console's own sentence for a write it will not
 * spawn, and it carries the one command that does the same thing by hand. */
export function modelsAddFloorSentence(floor: string | null, cliVersion: string): string {
  const instead = `; run ${MODELS_ADD_COMMAND} in your own terminal instead`
  if (floor === null) return `${MODELS_ADD_WORD} ${MODELS_ADD_FLOOR_UNSET}${instead}`
  return `${MODELS_ADD_WORD} needs fugaro ${floor} and this CLI is ${
    cliVersion === '' ? UNREADABLE_VERSION : cliVersion
  }${instead}`
}

/** MODELS_ADD_DEADLINE_MS is how long one models child may take, and it is the
 * ordinary read's bound rather than the wizard's hour: the command computes a
 * change to one config file and prints it, and a `--no-apply` write saves its
 * follow-up steps instead of running them, so there is no cloud work behind
 * either phase. */
export const MODELS_ADD_DEADLINE_MS = READ_TIMEOUT_MS

/** MODELS_ADD_PLAN_UNKNOWN_ARGUMENT is what a plan payload carrying anything but
 * the four values is refused with. */
export const MODELS_ADD_PLAN_UNKNOWN_ARGUMENT =
  'models.add.plan takes the model, the route and the two rates, and nothing else'

/** MODELS_ADD_APPLY_UNKNOWN_ARGUMENT is what an apply payload carrying anything
 * but those four is refused with: the values are the ones the window showed, and
 * a member main did not put in the plan's own payload is a member the window
 * never drew. */
export const MODELS_ADD_APPLY_UNKNOWN_ARGUMENT = MODELS_ADD_PLAN_UNKNOWN_ARGUMENT

/** MODELS_ADD_DISAGREES is what an apply is refused with when its values are not
 * the ones the held plan was about. It is the settings write's
 * `SETTINGS_EDIT_DISAGREES` for the same reason: the argv main re-runs is the
 * argv the window showed, and a payload that names another model or another
 * price is a request to write something nobody read. */
export const MODELS_ADD_DISAGREES =
  'the models write does not match the plan this window showed: nothing is written'

/** MODELS_ADD_PLAN_PENDING_REFUSAL is what a second plan is refused with while
 * one window is still waiting for its answer: one write at a time, so the argv
 * one window showed cannot be replaced by another's. */
export const MODELS_ADD_PLAN_PENDING_REFUSAL =
  'a models plan is already waiting for your answer in its window'

/** MODELS_ADD_NO_PLAN_REFUSAL is what an apply with no plan behind it is refused
 * with. */
export const MODELS_ADD_NO_PLAN_REFUSAL = 'no models plan is waiting: nothing is written'

/** MODELS_ADD_NO_YES_REFUSAL is what an apply is refused with while the window
 * that showed the plan has not said yes. It is the whole of the gate: a renderer
 * cannot start a write, and neither can a frame that is not the window's own. */
export const MODELS_ADD_NO_YES_REFUSAL =
  'the models plan has not been confirmed in its window: nothing is written'

/** MODELS_ADD_DOCUMENT_CHANGED_REFUSAL is what an apply is refused with when the
 * held document no longer hashes to the digest the window showed. Nothing in
 * this console can change it; a document that changed is one something else
 * wrote to, and the write it would stand for is not the one the operator agreed
 * to. */
export const MODELS_ADD_DOCUMENT_CHANGED_REFUSAL =
  'the models plan changed after the window showed it: nothing is written'

/** MODELS_ADD_UNREADABLE_DOCUMENT is the fallback for either child when it has
 * no sentence of its own: a plan that printed no document this console can read
 * and said nothing on stderr, or a write that exited non-zero without one. A
 * refusal the CLI prints is a sentence on stderr, so this is the other shape: a
 * child that died before it could say anything -- a bad flag, a config refusal,
 * a missing binary. */
export const MODELS_ADD_UNREADABLE_DOCUMENT = 'models add printed no plan this console can read'

/** ModelsAddValues is the four values one write is about, as main read them out
 * of the payload: the model ID, the route it is claimed on and the two rates.
 * They are held with the plan so that an apply can be checked against them
 * instead of believed. */
export interface ModelsAddValues {
  readonly id: string
  readonly route: string
  readonly priceIn: string
  readonly priceOut: string
}

/** ModelsAddOutcome is what both operations answer with, and it is a sentence
 * rather than a document -- the settings write's own `SettingsEditOutcome`:
 * what the run did, or the sentence it was refused with, with `ok` saying which
 * of the two a screen is drawing. The window is where the plan itself is read,
 * so nothing here repeats it. */
export interface ModelsAddOutcome {
  readonly ok: boolean
  readonly sentence: string
}

/** ModelsAddHandler is one of the two handlers, `ConsoleHandler` for the names
 * `ipc.ts` carries but the contract does not: it answers asynchronously and
 * throws a `SourceRefusal` for anything it refused. */
export type ModelsAddHandler = (args: ValidatedArguments) => Promise<ModelsAddOutcome>

/** ModelsAddHandlers is the two of them by name, the shape the wiring task
 * spreads into the `gated` table. */
export interface ModelsAddHandlers {
  readonly 'models.add.plan': ModelsAddHandler
  readonly 'models.add': ModelsAddHandler
}

/**
 * ModelsAddPlanner is one models run as a child process: the wiring task's argv
 * row for the operation, over the values this module validated and the
 * installation this module is aimed at. It is a seam rather than a call so that
 * the whole flow is testable without a child, and it is handed the values rather
 * than reading them because it is this module that decides which values the two
 * phases carry -- the plan's own, both times.
 */
export type ModelsAddPlanner = (
  name: ModelsAddSpawnName,
  values: ModelsAddValues,
  project: string,
) => SpawnPlan

/** ModelsAddDeps is what the write is built from. Everything but `confirmer`,
 * `plan`, `project` and `lapsed` is a seam a test drives. */
export interface ModelsAddDeps {
  /** the window, which is SU1's one window and no second implementation. */
  readonly confirmer: LaunchConfirmer
  readonly plan: ModelsAddPlanner
  /** the active installation: the project the write lands in, which main names
   * itself rather than letting a renderer choose it (design section 3.4 rule 3).
   * It is also the window's identity (`modelsAddConfirmID`), so an installation
   * that is re-chosen while a window is open closes that window as a cancel. */
  readonly project: string
  /** `cliVersion` is the resolved binary's own version line, or `''` when the
   * console could not read one. */
  readonly cliVersion: string
  /** `floor` is `MODELS_ADD_CLI_FLOOR`, `null` while no release carries the
   * write: every plan refuses its own sentence rather than spawning an argv
   * nothing answers. */
  readonly floor?: string | null
  /** `lapsed` answers whether the licence has lapsed for the installation this
   * write lands in, and it is asked **where the write really starts** and not
   * only at the door: the window's own yes reaches `runApply` from `decide`,
   * with no IPC handler anywhere on that path, so `lapseHandlers`' gate on the
   * row -- the design's `refusedAfterLapse` column
   * (`docs/lapse-read-only/spec.md`) -- is not what stops a plan made after the
   * lapse, which is kept and opens its window. The plan is a dry
   * run and stays reachable; the write it stands for is the row the lapse takes
   * away, and this is the column made true on the window's path.
   *
   * It is required rather than optional for that reason: a write built without
   * it would be a write the lapse does not stop, and no test of a payload could
   * see the difference. */
  readonly lapsed: () => boolean
  /** `digest` is the re-check apply runs, `modelsAddDigest` unless a test says
   * otherwise. */
  readonly digest?: (document: string) => string
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timers?: Timers
  readonly deadlineMs?: number
}

/** ModelsAddWriter is the two handlers. */
export type ModelsAddWriter = ModelsAddHandlers

/** ModelsAddOutcomeHeld is what became of one apply: the sentence the child
 * printed, or the sentence the console refused it with. It is held rather than
 * thrown on the window's own path -- nobody is awaiting that one -- so a
 * renderer that asks afterwards is told the same thing the run did. */
type ModelsAddOutcomeHeld =
  | { readonly kind: 'ran'; readonly sentence: string }
  | { readonly kind: 'refused'; readonly sentence: string }

/** HeldModelsAdd is one plan: what the window showed, the document it showed it
 * from, and what has become of the write since. */
interface HeldModelsAdd {
  readonly view: ModelsAddView
  readonly values: ModelsAddValues
  readonly document: string
  readonly digest: string
  readonly applyPlan: SpawnPlan
  armed: boolean
  running: Promise<void> | null
  outcome: ModelsAddOutcomeHeld | null
}

/** modelsAddDigest is the SHA-256 of the held document, in lowercase hex -- what
 * the window shows and what apply re-checks. */
export function modelsAddDigest(document: string): string {
  return createHash('sha256').update(document, 'utf8').digest('hex')
}

/** MODELS_ADD_VALUE_KEYS is the four members a payload may carry, in the order
 * the argv emits them. */
const MODELS_ADD_VALUE_KEYS: readonly string[] = Object.freeze([
  'id',
  'route',
  'priceIn',
  'priceOut',
])

/**
 * readModelsAddValues reads the four values out of a payload, and it is strict
 * in both directions: a member this console does not send is refused, and a
 * member that is not a string is refused. The **grammars** are not re-checked
 * here -- `ipc.ts` held every one of them before this handler was called, and a
 * second spelling of a grammar is a second thing to keep in step -- so what this
 * function establishes is the shape, and the two optional rates are held as the
 * empty string so that the payload and the plan's held values are one type.
 */
function readModelsAddValues(raw: Record<string, unknown>): ModelsAddValues {
  for (const key of Object.keys(raw)) {
    if (!MODELS_ADD_VALUE_KEYS.includes(key)) {
      throw new SourceRefusal(`${MODELS_ADD_PLAN_UNKNOWN_ARGUMENT}: ${key}`)
    }
  }
  const id = raw['id']
  const route = raw['route']
  if (typeof id !== 'string' || id === '') throw new SourceRefusal(MODELS_ADD_PLAN_UNKNOWN_ARGUMENT)
  if (typeof route !== 'string' || route === '') {
    throw new SourceRefusal(MODELS_ADD_PLAN_UNKNOWN_ARGUMENT)
  }
  const priceIn = raw['priceIn']
  const priceOut = raw['priceOut']
  if (priceIn !== undefined && typeof priceIn !== 'string') {
    throw new SourceRefusal(MODELS_ADD_PLAN_UNKNOWN_ARGUMENT)
  }
  if (priceOut !== undefined && typeof priceOut !== 'string') {
    throw new SourceRefusal(MODELS_ADD_PLAN_UNKNOWN_ARGUMENT)
  }
  return Object.freeze({
    id,
    route,
    priceIn: typeof priceIn === 'string' ? priceIn : '',
    priceOut: typeof priceOut === 'string' ? priceOut : '',
  })
}

/** sameModelsAddValues is whether two sets of values are the same four, with an
 * absent rate and a rate the CLI refused read as one fact: neither reaches an
 * argv word, so an apply that omits a rate the plan also omitted agrees with it.
 */
function sameModelsAddValues(left: ModelsAddValues, right: ModelsAddValues): boolean {
  return (
    left.id === right.id &&
    left.route === right.route &&
    left.priceIn === right.priceIn &&
    left.priceOut === right.priceOut
  )
}

/**
 * createModelsAdd builds the write's two handlers.
 *
 * The flow is one plan at a time and one gate:
 *
 *   1. `models.add.plan` refuses below the floor, reads the four values, builds
 *      both command lines, holds the record, runs the dry run, decodes the
 *      document it printed, and only then shows SU1's window the document beside
 *      its digest and the exact argv. **A dry run that refused shows no window
 *      and answers its own sentence instead** -- the CLI's own, which is what a
 *      screen draws;
 *   2. the window's plan frame answering no gives the record up and writes
 *      nothing;
 *   3. the window's **own top frame** answering yes arms the record and starts
 *      the write: the same argv, after the lapse is asked and the held document
 *      is hashed again;
 *   4. `models.add` is the renderer's door to that run: it refuses while no plan
 *      is held, while the values it carries are not the plan's own, or while the
 *      window has not said yes, joins the write the yes started rather than
 *      spawning a second child, and answers the sentence the child printed, or
 *      the sentence it was refused with.
 */
export function createModelsAdd(deps: ModelsAddDeps): ModelsAddWriter {
  const digest = deps.digest ?? modelsAddDigest
  const deadlineMs = deps.deadlineMs ?? MODELS_ADD_DEADLINE_MS

  let held: HeldModelsAdd | null = null

  /** runChild runs one child under the deadline and answers its whole result:
   * its status beside both streams. The status is not decoration -- a write
   * prints the path and the diff to stdout before it can still refuse, so stdout
   * alone cannot say whether the write happened. */
  const runChild = async (plan: SpawnPlan): Promise<RunResult> =>
    runToCompletion(plan, {
      timeoutMs: deadlineMs,
      ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
      ...(deps.log === undefined ? {} : { log: deps.log }),
      ...(deps.timers === undefined ? {} : { timers: deps.timers }),
    })

  /** sentenceOf is a child's own account of itself: its stderr through
   * `cliSentence`, or the console's sentence when it said nothing. */
  const sentenceOf = (stderr: string, fallback: string): string =>
    cliSentence(stderr) ?? fallback

  /** answerOf is the held outcome as an answer, or the sentence it was refused
   * with. */
  const answerOf = (writer: HeldModelsAdd): ModelsAddOutcome => {
    const outcome = writer.outcome
    if (outcome === null) throw new SourceRefusal(MODELS_ADD_NO_PLAN_REFUSAL)
    return { ok: outcome.kind === 'ran', sentence: outcome.sentence }
  }

  /** runApply runs the write once for a record: the lapse, the digest, then the
   * same argv. */
  const runApply = async (writer: HeldModelsAdd): Promise<void> => {
    // **The lapse's sentence, and the first fact.** The window's yes reaches
    // this function from `decide`, and no IPC handler is on that path at all,
    // so the gate at the door is not what stops this child -- see `lapsed`
    // above. Nothing is spawned: the sentence is the console's own one for the
    // lapse (`LAPSE_REFUSAL`), which is the same sentence a lapsed console
    // answers `models.add` with, so the two paths say one thing.
    if (deps.lapsed()) {
      writer.outcome = { kind: 'refused', sentence: LAPSE_REFUSAL }
      return
    }
    if (writer.digest !== digest(writer.document)) {
      writer.outcome = { kind: 'refused', sentence: MODELS_ADD_DOCUMENT_CHANGED_REFUSAL }
      return
    }
    try {
      const result = await runChild(writer.applyPlan)
      // **A write that exited non-zero wrote nothing.** `models add` prints the
      // path and the diff to stdout *before* it can still refuse -- the config
      // changed since it was read, a coding-agent session that may not write, a
      // backup that failed -- so a non-empty stdout is not the report of a write
      // that happened, and a screen told `ran` would say the model was added
      // when nothing was written. The status is the first fact and the sentence
      // is the child's stderr, as `src/source/cli.ts` reads every other child.
      if (result.code !== 0 || result.signal !== null) {
        writer.outcome = {
          kind: 'refused',
          sentence: sentenceOf(result.stderr, MODELS_ADD_UNREADABLE_DOCUMENT),
        }
        return
      }
      // The write's own report is what it printed; a child that printed nothing
      // at all is a run the console cannot claim succeeded, and its stderr is
      // the sentence, or the console's own when it said nothing either.
      const sentence = result.stdout.trim()
      writer.outcome =
        sentence === ''
          ? { kind: 'refused', sentence: sentenceOf(result.stderr, MODELS_ADD_UNREADABLE_DOCUMENT) }
          : { kind: 'ran', sentence }
    } catch (error) {
      writer.outcome = {
        kind: 'refused',
        sentence: error instanceof SourceRefusal ? error.sentence : MODELS_ADD_UNREADABLE_DOCUMENT,
      }
    }
  }

  /** ensureApply starts the write at most once, and is what the window's yes and
   * the renderer's `models.add` share: the two may arrive in either order, and
   * neither may spawn a second child. */
  const ensureApply = (writer: HeldModelsAdd): Promise<void> => {
    if (writer.running === null) writer.running = runApply(writer)
    return writer.running
  }

  /** decide is the window's answer, reached from the window's own main frame
   * (`confirm.ts`'s sender and frame gate). A yes starts the write; a no gives
   * the record up and writes nothing. */
  const decide = (writer: HeldModelsAdd, yes: boolean): void => {
    if (held !== writer || writer.outcome !== null || writer.running !== null) return
    if (!yes) {
      held = null
      return
    }
    writer.armed = true
    void ensureApply(writer).catch(() => undefined)
  }

  /** refuseBelowFloor is the whole of the floor check, before anything is
   * spawned. */
  const refuseBelowFloor = (): void => {
    const floor = deps.floor ?? null
    if (floor !== null && meetsCliFloor(deps.cliVersion, floor)) return
    throw new SourceRefusal(modelsAddFloorSentence(floor, deps.cliVersion))
  }

  return Object.freeze({
    /**
     * plan is `models add --no-apply --dry-run --json …`: validate, build both
     * command lines, hold, run, show the window, answer the sentence.
     */
    'models.add.plan': async (args: ValidatedArguments): Promise<ModelsAddOutcome> => {
      refuseBelowFloor()
      const values = readModelsAddValues({ ...args })
      // One write at a time: a plan that is still waiting for its window's
      // answer may not be replaced, because the argv the window showed is the
      // argv the operator agreed to.
      if (held !== null && held.outcome === null) throw new SourceRefusal(MODELS_ADD_PLAN_PENDING_REFUSAL)
      const planPlan = deps.plan('models.add.plan', values, deps.project)
      const applyPlan = deps.plan('models.add', values, deps.project)
      const result = await runChild(planPlan)
      // The dry run's status is the first fact here too: a plan that exited
      // non-zero printed no plan to agree to, whatever its stdout holds.
      if (result.code !== 0 || result.signal !== null) {
        throw new SourceRefusal(sentenceOf(result.stderr, MODELS_ADD_UNREADABLE_DOCUMENT))
      }
      const document = result.stdout.trim()
      const shownDigest = digest(document)
      // The argv the window shows is the **write's**, not the dry run's: what
      // the operator is agreeing to is the command line the yes spawns, and a
      // line marked `--dry-run` would say nothing is written.
      const read = modelsAddView({
        dryRun: parseDocument(document),
        project: deps.project,
        digest: shownDigest,
        argv: applyPlan.argv,
      })
      // **A dry run that refused is not confirmed.** The CLI's own refusal is a
      // sentence with a non-zero exit and no document at all, so there is
      // nothing for a window to draw and no write to agree to: the sentence is
      // the CLI's, and a screen draws it with the field that was refused.
      if (!read.ok) throw new SourceRefusal(sentenceOf(result.stderr, read.because))
      const writer: HeldModelsAdd = {
        view: read.view,
        values,
        document,
        digest: shownDigest,
        applyPlan,
        armed: false,
        running: null,
        outcome: null,
      }
      held = writer
      // The window is shown only for a plan that ran. Its identity is the
      // installation the write lands in (`modelsAddConfirmID`).
      deps.confirmer.open(writer.view, (yes: boolean) => {
        decide(writer, yes)
      })
      return { ok: true, sentence: 'the plan is waiting for your answer in its window' }
    },

    /**
     * apply is `models add --no-apply …` over the argv the window showed. The
     * values it carries are checked against the held plan's own, and the argv it
     * spawns is the held one: a renderer that sent different values is refused
     * rather than believed, and one that sent none at all cannot reach the
     * write.
     */
    'models.add': async (args: ValidatedArguments): Promise<ModelsAddOutcome> => {
      const writer = held
      if (writer === null) throw new SourceRefusal(MODELS_ADD_NO_PLAN_REFUSAL)
      const values = readModelsAddValues({ ...args })
      if (!sameModelsAddValues(values, writer.values)) {
        throw new SourceRefusal(MODELS_ADD_DISAGREES)
      }
      if (!writer.armed) throw new SourceRefusal(MODELS_ADD_NO_YES_REFUSAL)
      await ensureApply(writer)
      // A spent record is not offered twice: the outcome has been answered, and
      // the next `models.add.plan` is a new plan rather than this one's write.
      if (writer.outcome !== null) held = null
      return answerOf(writer)
    },
  })
}

/**
 * parseDocument reads the child's stdout as the one JSON document `--json`
 * prints (`json.NewEncoder(...).Encode`, MD1): a single object on a line. It
 * answers `undefined` for anything else, which `modelsAddView` refuses as a
 * document it cannot vouch for -- the shape check is the view's, so a document
 * that is JSON but is not a plan is refused with the view's own sentence rather
 * than with a parse error here.
 */
function parseDocument(stdout: string): unknown {
  if (stdout === '') return undefined
  try {
    return JSON.parse(stdout)
  } catch {
    return undefined
  }
}
