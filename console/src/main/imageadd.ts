// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The image write's main-process half (the console-improvements plan, task MW2:
// D2903, O4; the design is `docs/desktop-console/spec.md` section 5.1 rules 11
// and 12, and the CLI's own document is
// `schemas/image-add-plan.schema.json`, MD2).
//
// **What this module is.** The whole of one `image add` that is the main
// process's, and it is the models write's own shape (`modelsadd.ts`) one command
// over: it runs `fugaro image add … --no-apply --dry-run --json` with the values
// the renderer sent **after main validated every one of them against a grammar**
// (`ipc.ts`'s `imageName`, `imageDockerfile`, `imageTags` and
// `imageDescription`), decodes the versioned document the child printed, holds
// that document, its digest and the **exact argv** in the window instance that
// showed them, shows SU1's one window the document as it parsed it, and, once
// that window's own top frame has said yes, runs the same argv without
// `--dry-run --json` -- the argv main built, never one the renderer sent again.
//
// **`--no-apply` is main's word and is a word of the row, not of a payload**, and
// here it is the word that keeps the expensive step off this machine: the add's
// own follow-up steps -- the catalog's build (which bills by itself), the
// repository's redeploy and the publish of the node every run is admitted by
// (D1561) -- are saved for `fugaro apply` (D1565). The document the window shows
// already says `saved` for them, which is what apply does.
//
// **The window carries no tick**, unlike the models write's: this write raises no
// limit, so it is a narrowing's window and its yes is one deliberate click
// (`imagesview.ts` says so where the view is built). `confirm.ts` holds the tick
// to the subjects that really do raise one.
//
// **Nothing is registered here.** The two names are not in `NewOperationName`
// (the contract deliberately stops at AL's nine, `src/shared/new-ops.ts`), so
// there is no `handlersFor` row to build: the handlers below are built over an
// `ImageAddPlanner` seam and the wiring task spreads them into the `gated` table
// with its own planner and floor, exactly as `modelsadd.ts` waits for I-OPS.
// Everything here is tested against a stub child and a stub confirmer.

import { createHash } from 'node:crypto'

import { cliSentence, runToCompletion, READ_TIMEOUT_MS } from './spawn'
import type { ChildSpawner, RunResult, SpawnPlan, Timers } from './spawn'
import type { SpawnLogWriter } from './spawnlog'
import type { LaunchConfirmer } from './launch'
import type { ValidatedArguments } from './ipc'
import { imageAddView } from './imagesview'
import type { ImageAddView } from './imagesview'
import { LAPSE_REFUSAL } from './licence-state'
import { SourceRefusal } from '../source/source'
import { IMAGE_ADD_CLI_FLOOR, UNREADABLE_VERSION, meetsCliFloor } from '../shared/version'

/** ImageAddSpawnName is the two operations this module drives, spelled as
 * `CONSOLE_OPERATIONS` spells them. */
export type ImageAddSpawnName = 'image.add.plan' | 'image.add'

/** IMAGE_ADD_WORD is how the write names itself in the one sentence the console
 * writes about it. */
export const IMAGE_ADD_WORD = 'the image write'

/** IMAGE_ADD_COMMAND is the line the console hands an operator whose CLI is
 * below the floor: a write with no window is still a write the operator can run
 * by hand, and the sentence names the flags the console would have passed. */
export const IMAGE_ADD_COMMAND = 'fugaro image add <name> --dockerfile <owner/name:path>'

/** IMAGE_ADD_FLOOR_UNSET is what a write whose floor is still `null` answers:
 * the floor is unset until a CLI release carries `--no-apply` and the plan
 * document (`IMAGE_ADD_CLI_FLOOR`), and a console that spawned the command
 * anyway would run an argv the installed CLI refuses -- or, worse, one it
 * accepts and runs the follow-up steps of, which for this command is a Cloud
 * Build. */
export const IMAGE_ADD_FLOOR_UNSET = 'needs a fugaro release no CLI has yet'

/** imageAddFloorSentence is the console's own sentence for a write it will not
 * spawn, and it carries the one command that does the same thing by hand. */
export function imageAddFloorSentence(floor: string | null, cliVersion: string): string {
  const instead = `; run ${IMAGE_ADD_COMMAND} in your own terminal instead`
  if (floor === null) return `${IMAGE_ADD_WORD} ${IMAGE_ADD_FLOOR_UNSET}${instead}`
  return `${IMAGE_ADD_WORD} needs fugaro ${floor} and this CLI is ${
    cliVersion === '' ? UNREADABLE_VERSION : cliVersion
  }${instead}`
}

/** IMAGE_ADD_DEADLINE_MS is how long one image child may take, and it is the
 * ordinary read's bound rather than the wizard's hour: the command validates one
 * definition and prints it, and a `--no-apply` write appends one catalog entry
 * and saves its follow-up steps instead of running them, so there is no cloud
 * work behind either phase -- the build this write owes is exactly the step
 * `--no-apply` keeps off this machine. */
export const IMAGE_ADD_DEADLINE_MS = READ_TIMEOUT_MS

/** IMAGE_ADD_PLAN_UNKNOWN_ARGUMENT is what a plan payload carrying anything but
 * the four values is refused with. */
export const IMAGE_ADD_PLAN_UNKNOWN_ARGUMENT =
  'image.add.plan takes the entry name, its definition, its tags and its description, and nothing else'

/** IMAGE_ADD_APPLY_UNKNOWN_ARGUMENT is what an apply payload carrying anything
 * but those four is refused with: the values are the ones the window showed, and
 * a member main did not put in the plan's own payload is a member the window
 * never drew. */
export const IMAGE_ADD_APPLY_UNKNOWN_ARGUMENT = IMAGE_ADD_PLAN_UNKNOWN_ARGUMENT

/** IMAGE_ADD_DISAGREES is what an apply is refused with when its values are not
 * the ones the held plan was about. It is the models write's
 * `MODELS_ADD_DISAGREES` for the same reason: the argv main re-runs is the argv
 * the window showed, and a payload that names another entry or another
 * definition is a request to write something nobody read. */
export const IMAGE_ADD_DISAGREES =
  'the image write does not match the plan this window showed: nothing is written'

/** IMAGE_ADD_PLAN_PENDING_REFUSAL is what a second plan is refused with while
 * one window is still waiting for its answer: one write at a time, so the argv
 * one window showed cannot be replaced by another's. */
export const IMAGE_ADD_PLAN_PENDING_REFUSAL =
  'an image plan is already waiting for your answer in its window'

/** IMAGE_ADD_NO_PLAN_REFUSAL is what an apply with no plan behind it is refused
 * with. */
export const IMAGE_ADD_NO_PLAN_REFUSAL = 'no image plan is waiting: nothing is written'

/** IMAGE_ADD_NO_YES_REFUSAL is what an apply is refused with while the window
 * that showed the plan has not said yes. It is the whole of the gate: a renderer
 * cannot start a write, and neither can a frame that is not the window's own. */
export const IMAGE_ADD_NO_YES_REFUSAL =
  'the image plan has not been confirmed in its window: nothing is written'

/** IMAGE_ADD_DOCUMENT_CHANGED_REFUSAL is what an apply is refused with when the
 * held document no longer hashes to the digest the window showed. Nothing in
 * this console can change it; a document that changed is one something else
 * wrote to, and the write it would stand for is not the one the operator agreed
 * to. */
export const IMAGE_ADD_DOCUMENT_CHANGED_REFUSAL =
  'the image plan changed after the window showed it: nothing is written'

/** IMAGE_ADD_UNREADABLE_DOCUMENT is the fallback for either child when it has no
 * sentence of its own: a plan that printed no document this console can read and
 * said nothing on stderr, or a write that exited non-zero without one. A refusal
 * the CLI prints is a sentence on stderr, so this is the other shape: a child
 * that died before it could say anything -- a bad flag, a config refusal, a
 * missing binary. */
export const IMAGE_ADD_UNREADABLE_DOCUMENT = 'image add printed no plan this console can read'

/** ImageAddValues is the four values one write is about, as main read them out of
 * the payload: the entry's name, the definition it is built from, its tags and
 * its description. They are held with the plan so that an apply can be checked
 * against them instead of believed, and the two optional values are held as the
 * empty string -- which is how the argv spells *this write sets none*, an absent
 * flag rather than an empty one. */
export interface ImageAddValues {
  readonly name: string
  readonly dockerfile: string
  readonly tags: string
  readonly description: string
}

/** ImageAddOutcome is what both operations answer with, and it is a sentence
 * rather than a document -- the models write's own `ModelsAddOutcome`: what the
 * run did, or the sentence it was refused with, with `ok` saying which of the two
 * a screen is drawing. The window is where the plan itself is read, so nothing
 * here repeats it. */
export interface ImageAddOutcome {
  readonly ok: boolean
  readonly sentence: string
}

/** ImageAddHandler is one of the two handlers, `ConsoleHandler` for the names
 * `ipc.ts` carries but the contract does not: it answers asynchronously and
 * throws a `SourceRefusal` for anything it refused. */
export type ImageAddHandler = (args: ValidatedArguments) => Promise<ImageAddOutcome>

/** ImageAddHandlers is the two of them by name, the shape the wiring task
 * spreads into the `gated` table. */
export interface ImageAddHandlers {
  readonly 'image.add.plan': ImageAddHandler
  readonly 'image.add': ImageAddHandler
}

/**
 * ImageAddPlanner is one image run as a child process: the wiring task's argv row
 * for the operation, over the values this module validated and the installation
 * this module is aimed at. It is a seam rather than a call so that the whole flow
 * is testable without a child, and it is handed the values rather than reading
 * them because it is this module that decides which values the two phases carry
 * -- the plan's own, both times.
 */
export type ImageAddPlanner = (
  name: ImageAddSpawnName,
  values: ImageAddValues,
  project: string,
) => SpawnPlan

/** ImageAddDeps is what the write is built from. Everything but `confirmer`,
 * `plan`, `project` and `lapsed` is a seam a test drives. */
export interface ImageAddDeps {
  /** the window, which is SU1's one window and no second implementation. */
  readonly confirmer: LaunchConfirmer
  readonly plan: ImageAddPlanner
  /** the active installation: the project the write lands in, which main names
   * itself rather than letting a renderer choose it (design section 3.4 rule 3).
   * It is also the window's identity (`imageAddConfirmID`) and the project the
   * plan document's own `project` is compared against (`imagesview.ts`), so an
   * installation that is re-chosen while a window is open closes that window as a
   * cancel and a plan for another one is refused rather than shown. */
  readonly project: string
  /** `cliVersion` is the resolved binary's own version line, or `''` when the
   * console could not read one. */
  readonly cliVersion: string
  /** `floor` is `IMAGE_ADD_CLI_FLOOR`, `null` while no release carries the
   * write: every plan refuses its own sentence rather than spawning an argv
   * nothing answers. */
  readonly floor?: string | null
  /** `lapsed` answers whether the licence has lapsed for the installation this
   * write lands in, and it is asked **where the write really starts** and not
   * only at the door: the window's own yes reaches `runApply` from `decide`,
   * with no IPC handler anywhere on that path, so `lapseHandlers`' gate on the
   * row -- the design's `refusedAfterLapse` column
   * (`docs/lapse-read-only/spec.md`) -- is not what stops a plan made after the
   * lapse, which is kept and opens its window. The plan is a dry run and stays
   * reachable; the write it stands for is the row the lapse takes away, and
   * this is the column made true on the window's path.
   *
   * It is required rather than optional for that reason: a write built without
   * it would be a write the lapse does not stop, and no test of a payload
   * could see the difference. */
  readonly lapsed: () => boolean
  /** `digest` is the re-check apply runs, `imageAddDigest` unless a test says
   * otherwise. */
  readonly digest?: (document: string) => string
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timers?: Timers
  readonly deadlineMs?: number
}

/** ImageAddWriter is the two handlers. */
export type ImageAddWriter = ImageAddHandlers

/** ImageAddOutcomeHeld is what became of one apply: the sentence the child
 * printed, or the sentence the console refused it with. It is held rather than
 * thrown on the window's own path -- nobody is awaiting that one -- so a renderer
 * that asks afterwards is told the same thing the run did. */
type ImageAddOutcomeHeld =
  | { readonly kind: 'ran'; readonly sentence: string }
  | { readonly kind: 'refused'; readonly sentence: string }

/** HeldImageAdd is one plan: what the window showed, the document it showed it
 * from, and what has become of the write since. */
interface HeldImageAdd {
  readonly view: ImageAddView
  readonly values: ImageAddValues
  readonly document: string
  readonly digest: string
  readonly applyPlan: SpawnPlan
  armed: boolean
  running: Promise<void> | null
  outcome: ImageAddOutcomeHeld | null
}

/** imageAddDigest is the SHA-256 of the held document, in lowercase hex -- what
 * the window shows and what apply re-checks. */
export function imageAddDigest(document: string): string {
  return createHash('sha256').update(document, 'utf8').digest('hex')
}

/** IMAGE_ADD_VALUE_KEYS is the four members a payload may carry, in the order the
 * argv emits them. */
const IMAGE_ADD_VALUE_KEYS: readonly string[] = Object.freeze([
  'name',
  'dockerfile',
  'tags',
  'description',
])

/**
 * readImageAddValues reads the four values out of a payload, and it is strict in
 * both directions: a member this console does not send is refused, and a member
 * that is not a string is refused. The **grammars** are not re-checked here --
 * `ipc.ts` held every one of them before this handler was called, and a second
 * spelling of a grammar is a second thing to keep in step -- so what this
 * function establishes is the shape, and the two optional values are held as the
 * empty string so that the payload and the plan's held values are one type.
 */
function readImageAddValues(raw: Record<string, unknown>): ImageAddValues {
  for (const key of Object.keys(raw)) {
    if (!IMAGE_ADD_VALUE_KEYS.includes(key)) {
      throw new SourceRefusal(`${IMAGE_ADD_PLAN_UNKNOWN_ARGUMENT}: ${key}`)
    }
  }
  const name = raw['name']
  const dockerfile = raw['dockerfile']
  if (typeof name !== 'string' || name === '') throw new SourceRefusal(IMAGE_ADD_PLAN_UNKNOWN_ARGUMENT)
  if (typeof dockerfile !== 'string' || dockerfile === '') {
    throw new SourceRefusal(IMAGE_ADD_PLAN_UNKNOWN_ARGUMENT)
  }
  const tags = raw['tags']
  const description = raw['description']
  if (tags !== undefined && typeof tags !== 'string') {
    throw new SourceRefusal(IMAGE_ADD_PLAN_UNKNOWN_ARGUMENT)
  }
  if (description !== undefined && typeof description !== 'string') {
    throw new SourceRefusal(IMAGE_ADD_PLAN_UNKNOWN_ARGUMENT)
  }
  return Object.freeze({
    name,
    dockerfile,
    tags: typeof tags === 'string' ? tags : '',
    description: typeof description === 'string' ? description : '',
  })
}

/** sameImageAddValues is whether two sets of values are the same four, with an
 * absent tag list and an absent description read as one fact: neither reaches an
 * argv word, so an apply that omits a value the plan also omitted agrees with it.
 */
function sameImageAddValues(left: ImageAddValues, right: ImageAddValues): boolean {
  return (
    left.name === right.name &&
    left.dockerfile === right.dockerfile &&
    left.tags === right.tags &&
    left.description === right.description
  )
}

/**
 * createImageAdd builds the write's two handlers.
 *
 * The flow is one plan at a time and one gate, and it is the models write's:
 *
 *   1. `image.add.plan` refuses below the floor, reads the four values, builds
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
 *   4. `image.add` is the renderer's door to that run: it refuses while no plan is
 *      held, while the values it carries are not the plan's own, or while the
 *      window has not said yes, joins the write the yes started rather than
 *      spawning a second child, and answers the sentence the child printed, or the
 *      sentence it was refused with.
 */
export function createImageAdd(deps: ImageAddDeps): ImageAddWriter {
  const digest = deps.digest ?? imageAddDigest
  const deadlineMs = deps.deadlineMs ?? IMAGE_ADD_DEADLINE_MS

  let held: HeldImageAdd | null = null
  /** planning is true from the moment a plan is accepted until its dry run has
   * answered and its window is open (or the plan refused). It is the guard's own
   * fact and not `held`'s: `held` is written only after the child answers, so two
   * plans that overlap inside `await runChild` would both read an empty `held`
   * and both spawn, and the second's window would replace the first's argv. */
  let planning = false

  /** runChild runs one child under the deadline and answers its whole result: its
   * status beside both streams. The status is not decoration -- a write prints the
   * entry and the steps to stdout before it can still refuse, so stdout alone
   * cannot say whether the write happened. */
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
  const answerOf = (writer: HeldImageAdd): ImageAddOutcome => {
    const outcome = writer.outcome
    if (outcome === null) throw new SourceRefusal(IMAGE_ADD_NO_PLAN_REFUSAL)
    return { ok: outcome.kind === 'ran', sentence: outcome.sentence }
  }

  /** runApply runs the write once for a record: the lapse, the digest, then the
   * same argv. */
  const runApply = async (writer: HeldImageAdd): Promise<void> => {
    // **The lapse's sentence, and the first fact.** The window's yes reaches
    // this function from `decide`, and no IPC handler is on that path at all,
    // so the gate at the door is not what stops this child -- see `lapsed`
    // above. Nothing is spawned: the sentence is the console's own one for the
    // lapse (`LAPSE_REFUSAL`), which is the same sentence a lapsed console
    // answers `image.add` with, so the two paths say one thing.
    if (deps.lapsed()) {
      writer.outcome = { kind: 'refused', sentence: LAPSE_REFUSAL }
      return
    }
    if (writer.digest !== digest(writer.document)) {
      writer.outcome = { kind: 'refused', sentence: IMAGE_ADD_DOCUMENT_CHANGED_REFUSAL }
      return
    }
    try {
      const result = await runChild(writer.applyPlan)
      // **A write that exited non-zero wrote nothing.** `image add` prints what
      // it did before it can still refuse -- the config changed since it was read,
      // a coding-agent session that may not write, a backup that failed -- so a
      // non-empty stdout is not the report of a write that happened, and a screen
      // told `ran` would say the entry was added when nothing was written. The
      // status is the first fact and the sentence is the child's stderr, as
      // `src/source/cli.ts` reads every other child.
      if (result.code !== 0 || result.signal !== null) {
        writer.outcome = {
          kind: 'refused',
          sentence: sentenceOf(result.stderr, IMAGE_ADD_UNREADABLE_DOCUMENT),
        }
        return
      }
      // The write's own report is what it printed; a child that printed nothing at
      // all is a run the console cannot claim succeeded, and its stderr is the
      // sentence, or the console's own when it said nothing either.
      const sentence = result.stdout.trim()
      writer.outcome =
        sentence === ''
          ? { kind: 'refused', sentence: sentenceOf(result.stderr, IMAGE_ADD_UNREADABLE_DOCUMENT) }
          : { kind: 'ran', sentence }
    } catch (error) {
      writer.outcome = {
        kind: 'refused',
        sentence: error instanceof SourceRefusal ? error.sentence : IMAGE_ADD_UNREADABLE_DOCUMENT,
      }
    }
  }

  /** ensureApply starts the write at most once, and is what the window's yes and
   * the renderer's `image.add` share: the two may arrive in either order, and
   * neither may spawn a second child. */
  const ensureApply = (writer: HeldImageAdd): Promise<void> => {
    if (writer.running === null) writer.running = runApply(writer)
    return writer.running
  }

  /** decide is the window's answer, reached from the window's own main frame
   * (`confirm.ts`'s sender and frame gate). A yes starts the write; a no gives the
   * record up and writes nothing. */
  const decide = (writer: HeldImageAdd, yes: boolean): void => {
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
    throw new SourceRefusal(imageAddFloorSentence(floor, deps.cliVersion))
  }

  return Object.freeze({
    /**
     * plan is `image add --no-apply --dry-run --json …`: validate, build both
     * command lines, hold, run, show the window, answer the sentence.
     */
    'image.add.plan': async (args: ValidatedArguments): Promise<ImageAddOutcome> => {
      refuseBelowFloor()
      const values = readImageAddValues({ ...args })
      // One write at a time: a plan that is still spawning its dry run, or one
      // still waiting for its window's answer, may not be replaced, because the
      // argv the window showed is the argv the operator agreed to.
      if (planning || (held !== null && held.outcome === null)) {
        throw new SourceRefusal(IMAGE_ADD_PLAN_PENDING_REFUSAL)
      }
      planning = true
      try {
        const planPlan = deps.plan('image.add.plan', values, deps.project)
        const applyPlan = deps.plan('image.add', values, deps.project)
        const result = await runChild(planPlan)
        // The dry run's status is the first fact here too: a plan that exited
        // non-zero printed no plan to agree to, whatever its stdout holds.
        if (result.code !== 0 || result.signal !== null) {
          throw new SourceRefusal(sentenceOf(result.stderr, IMAGE_ADD_UNREADABLE_DOCUMENT))
        }
        const document = result.stdout.trim()
        const shownDigest = digest(document)
        // The argv the window shows is the **write's**, not the dry run's: what the
        // operator is agreeing to is the command line the yes spawns, and a line
        // marked `--dry-run` would say nothing is written.
        const read = imageAddView({
          dryRun: parseDocument(document),
          project: deps.project,
          digest: shownDigest,
          argv: applyPlan.argv,
        })
        // **A dry run that refused is not confirmed.** The CLI's own refusal is a
        // sentence with a non-zero exit and no document at all, so there is nothing
        // for a window to draw and no write to agree to: the sentence is the CLI's,
        // and a screen draws it with the field that was refused.
        if (!read.ok) throw new SourceRefusal(sentenceOf(result.stderr, read.because))
        const writer: HeldImageAdd = {
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
        // installation the write lands in (`imageAddConfirmID`).
        deps.confirmer.open(writer.view, (yes: boolean) => {
          decide(writer, yes)
        })
        return { ok: true, sentence: 'the plan is waiting for your answer in its window' }
      } finally {
        planning = false
      }
    },

    /**
     * apply is `image add --no-apply …` over the argv the window showed. The
     * values it carries are checked against the held plan's own, and the argv it
     * spawns is the held one: a renderer that sent different values is refused
     * rather than believed, and one that sent none at all cannot reach the write.
     */
    'image.add': async (args: ValidatedArguments): Promise<ImageAddOutcome> => {
      const writer = held
      if (writer === null) throw new SourceRefusal(IMAGE_ADD_NO_PLAN_REFUSAL)
      const values = readImageAddValues({ ...args })
      if (!sameImageAddValues(values, writer.values)) {
        throw new SourceRefusal(IMAGE_ADD_DISAGREES)
      }
      if (!writer.armed) throw new SourceRefusal(IMAGE_ADD_NO_YES_REFUSAL)
      await ensureApply(writer)
      // A spent record is not offered twice: the outcome has been answered, and
      // the next `image.add.plan` is a new plan rather than this one's write.
      if (writer.outcome !== null) held = null
      return answerOf(writer)
    },
  })
}

/**
 * parseDocument reads the child's stdout as the one JSON document `--json` prints
 * (`json.NewEncoder(...).Encode`, MD2): a single object on a line. It answers
 * `undefined` for anything else, which `imageAddView` refuses as a document it
 * cannot vouch for -- the shape check is the view's, so a document that is JSON
 * but is not a plan is refused with the view's own sentence rather than with a
 * parse error here.
 */
function parseDocument(stdout: string): unknown {
  if (stdout === '') return undefined
  try {
    return JSON.parse(stdout)
  } catch {
    return undefined
  }
}
