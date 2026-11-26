// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The setup wizard's main-process half (the console-improvements plan, task
// SW3a: D2904, O6, F15, F27; the contract is `src/shared/new-ops.ts`, task
// C-OPS, and the design is `docs/desktop-console/spec.md` section 5.1 rules 11
// and 12).
//
// **What this module is.** The whole of a first setup that is the main
// process's: it validates the answers a renderer sent, serialises them **once**
// into bytes it holds in memory, runs `fugaro setup --plan-only …` with those
// bytes on the child's **stdin**, shows SU1's one window the answers as it
// parsed them beside their SHA-256, and, once that window's own top frame has
// said yes, runs `fugaro setup …` with **the same bytes**. Nothing here writes a
// file: the CLI's `--answers-file -` reads stdin (ST1), so there is no path for
// a hostile process of the same user to swap between the check and the read, and
// no answer of the operator's ever lands on the disk.
//
// **The window's yes is not a consent the CLI accepts** (O6, F27). `setup
// --json` confirms no step (`unconfirmedWhy`, `internal/cli/init.go`), so the
// apply runs exactly what the CLI applies without a confirmation; every step that
// needs a person -- creating the GCP project, linking its billing account, a
// Terraform review in a managed project, every secret -- comes back from the
// child as a `left_for_you` event with its own line to run in the owner's
// terminal, and the window says so before it asks
// (`setupview.ts`'s four sentences). What the console adds is a **gate**: it
// holds the answers, so a renderer cannot change them between the plan the
// operator read and the run they agreed to.
//
// **What the renderer may send is an object of the wizard's answers and nothing
// else.** No path, no flag, no `version`, no key outside the set: the document
// is refused by name for each of those, and the grammars it holds the two
// identifiers to are the CLI's own (`infra.ValidProjectID`'s 6-to-30 project
// ID, `regionRE`'s `us-east5` shape, `config.ProjectNameRE`'s name, the four
// rigor modes the answers file's schema accepts, and the budget alert's own
// amount bounds). A value that is a path or a flag fails one of them, which is
// the point: the console never passes a word the operator did not mean as a
// value.
//
// **The budget answer is the one member a CLI may be too old to take**, and it
// is dropped for such a CLI before the bytes are serialised and before the
// window is shown (`budgetAnswerSupported`, `shared/version.ts`): the answers
// file's reader refuses a member it does not know by name, so sending it to an
// older fugaro would stop the whole setup rather than being ignored. What the
// window draws and what the child decodes are the same document either way.
//
// **The two operations are one flow, and the digest is what ties them.** A plan
// holds its bytes, their digest, both command lines and a fresh empty working
// directory; a second `setup.plan` is refused while that one is unsettled, and
// `setup.apply` -- whose payload is empty by contract, so a renderer can slip no
// new answers in at apply -- spawns nothing until the window's own top-frame yes
// has arrived, and refuses if the held bytes no longer hash to what the window
// showed. The bytes are the same `Uint8Array` both children are given: the red
// step of this task is the test that says so.
//
// **The working directory is a fresh empty one** (O7, and this task's U9): the
// console-spawned `setup` is not in a checkout of anything, so the CLI's
// repository question is never reached -- `gateEarly` returns at once when there
// is no checkout (`internal/cli/init_repo_gate.go`), `asksOnboard` needs a
// checkout and a person at a terminal, and the console never passes
// `--onboard-repo`. It is mode 0700, it is empty (the answers travel on stdin),
// and it is removed when the run's outcome is held.
//
// **Nothing is registered here.** The two names are not in `OperationName` until
// task AL adds their rows to the allow-lists, so there is no `handlersFor` row
// and no `planFor` row to build a plan from: the handlers below are built over a
// `SetupPlanner` seam and the wiring task (I-OPS) spreads them into the `gated`
// table with AL's own planner and floor, exactly as `src/source/new-reads.ts`
// does for the six reads. Everything here is tested against a stub child.

import { createHash } from 'node:crypto'
import { chmodSync, mkdtempSync, rmSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'

import { cliSentence, runToCompletion } from './spawn'
import type { ChildSpawner, JSONObject, SpawnPlan, Timers } from './spawn'
import type { SpawnLogWriter } from './spawnlog'
import type { LaunchConfirmer } from './launch'
import type { ValidatedArguments } from './ipc'
import { setupConfirmID, setupView } from './setupview'
import type { SetupView } from './setupview'
import { FUGARO_SETUP_COMMAND } from './onboarding'
import { LAPSE_REFUSAL } from './licence-state'
import {
  SETUP_ANSWER_KEYS,
  SETUP_ANSWERS_FILE_KEYS,
  SETUP_BUDGET_MAX,
  SETUP_BUDGET_MIN,
  SETUP_RIGOR_MODES,
} from '../shared/new-ops'
import type { SetupRigor } from '../shared/new-ops'
import { NEW_READ_BOUNDS, throughClasses } from '../source/new-reads'
import type { NewReadBounds } from '../source/new-reads'
import { SourceRefusal } from '../source/source'
import {
  BUDGET_ANSWER_CLI_FLOOR,
  UNREADABLE_VERSION,
  budgetAnswerSupported,
  meetsCliFloor,
} from '../shared/version'

// ---------------------------------------------------------------------------
// The two names, and the floor.
// ---------------------------------------------------------------------------

/** SetupSpawnName is the two operations this module drives, in the contract's
 * own spelling (`src/shared/new-ops.ts`). */
export type SetupSpawnName = 'setup.plan' | 'setup.apply'

/** SETUP_WIZARD_WORD is how the wizard names itself in the one sentence the
 * console writes about it, the way `NEW_READ_WORDS` does for the six reads. */
export const SETUP_WIZARD_WORD = 'the setup wizard'

/** SETUP_FLOOR_UNSET is what a wizard whose floor is still `null` answers: the
 * floor is unset until a CLI release carries `--answers-file` and `--events`
 * (the idiom `NEW_READ_FLOOR_UNSET` follows), and a console that spawned the
 * command anyway would run an argv the installed CLI refuses. */
export const SETUP_FLOOR_UNSET = 'needs a fugaro release no CLI has yet'

/** setupFloorSentence is the console's own sentence for a wizard it will not
 * spawn, and it carries the one command that does the same thing by hand: a
 * setup with no wizard is still a setup the operator can run. */
export function setupFloorSentence(floor: string | null, cliVersion: string): string {
  const instead = `; run ${FUGARO_SETUP_COMMAND} in your own terminal instead`
  if (floor === null) return `${SETUP_WIZARD_WORD} ${SETUP_FLOOR_UNSET}${instead}`
  return `${SETUP_WIZARD_WORD} needs fugaro ${floor} and this CLI is ${
    cliVersion === '' ? UNREADABLE_VERSION : cliVersion
  }${instead}`
}

/** SETUP_DEADLINE_MS is how long one setup child may take. It is **not**
 * `READ_TIMEOUT_MS` (90 s), which would kill a plan or an apply part-way: a
 * `--plan-only` run computes the Terraform plan and an apply runs it, and both
 * are the minutes a cloud project takes. The run ends on the CLI's own exit, on
 * this deadline, or on nothing else -- the console has no cancel path here. */
export const SETUP_DEADLINE_MS = 60 * 60 * 1000

// ---------------------------------------------------------------------------
// What a renderer may send, and what main does with it.
// ---------------------------------------------------------------------------

/** SETUP_ANSWERS_UNKNOWN_KEY is what an answers object carrying a member this
 * console does not send is refused with. The member is named; its value is not
 * echoed. */
export const SETUP_ANSWERS_UNKNOWN_KEY = 'the setup answers carry a member this console does not send'

/** SETUP_ANSWERS_NOT_AN_OBJECT is what a payload whose `answers` is anything but
 * an object is refused with. */
export const SETUP_ANSWERS_NOT_AN_OBJECT = 'the setup answers are not an object'

/** SETUP_ANSWERS_MISSING is what an answers object with a question left out is
 * refused with: everything but `name`, `budget` and `bake_repos` is required,
 * and the CLI would ask for it at a terminal this run does not have. */
export const SETUP_ANSWERS_MISSING = 'the setup answers leave a required question unanswered'

/** SETUP_PROJECT_GRAMMAR is the CLI's own project ID rule, in the CLI's own
 * words (`internal/infra/projectcreate.go`): the project the run creates. */
export const SETUP_PROJECT_GRAMMAR =
  '6 to 30 characters of a-z, 0-9 and -, starting with a letter and not ending with -'

/** SETUP_REGION_GRAMMAR is `regionRE`'s shape (`internal/cli/init_inputs.go`). */
export const SETUP_REGION_GRAMMAR = 'a region like us-east5 or europe-west4'

/** SETUP_NAME_GRAMMAR is `config.ProjectNameRE`'s shape. */
export const SETUP_NAME_GRAMMAR =
  '1 to 40 characters of a-z, 0-9 and -, starting and ending with a letter or digit'

/** SETUP_PROJECT_REFUSAL IS what a project id outside the grammar is refused
 * with. The value is not echoed: a refusal a screen draws is a sentence about
 * the field, and the console does not put a value it has just refused in front
 * of an operator as if it were text. */
export const SETUP_PROJECT_REFUSAL = `the GCP project id is not a project ID (${SETUP_PROJECT_GRAMMAR})`

/** SETUP_REGION_REFUSAL is the region's. */
export const SETUP_REGION_REFUSAL = `the region is not a Google Cloud region (${SETUP_REGION_GRAMMAR})`

/** SETUP_NAME_REFUSAL is the project name's. */
export const SETUP_NAME_REFUSAL = `the project name is not a project name (${SETUP_NAME_GRAMMAR})`

/** SETUP_BAKE_REFUSAL is what a `bake_repos` that is not a boolean is refused
 * with. */
export const SETUP_BAKE_REFUSAL = 'the bake switch is not a yes or a no'

/** SETUP_BUDGET_REFUSAL is what a `budget` that is not a whole number of units
 * inside the alert's bounds is refused with. The value is not echoed: it is a
 * number a field produced, and the sentence is about the question. */
export const SETUP_BUDGET_REFUSAL = `the monthly budget alert is not a whole number of units between ${SETUP_BUDGET_MIN} and ${SETUP_BUDGET_MAX}`

/** SETUP_RIGOR_LAYER_WORD is the four modes the answers file's schema accepts.
 * `hotfix` is deliberately not among them: it is an operator's own per-run mode
 * and no layer sets it (D834, `schemas/setup-answers.schema.json`). The wizard's
 * vocabulary is the wire's five (`SETUP_RIGOR_MODES`); what may be *written*
 * into a project layer is these four, and this is the sentence that says so. */
export const SETUP_RIGOR_LAYER_WORD = 'poc, startup, pro or critical'

/** SETUP_RIGOR_REFUSAL is what a rigor outside that set is refused with, and it
 * is one sentence whether the value was empty, misspelled or `hotfix`. */
export const SETUP_RIGOR_REFUSAL = `the rigor mode is not one of ${SETUP_RIGOR_LAYER_WORD}`

/** SETUP_LAYER_RIGOR_MODES is the four, as a value, held equal to the schema's
 * enum by `wizard-main.test.ts`. */
export const SETUP_LAYER_RIGOR_MODES: readonly SetupRigor[] = Object.freeze(
  SETUP_RIGOR_MODES.filter((mode) => mode !== 'hotfix'),
)

/** SETUP_PROJECT_RE is `infra.ValidProjectID`'s pattern. */
export const SETUP_PROJECT_RE = /^[a-z][a-z0-9-]{4,28}[a-z0-9]$/

/** SETUP_REGION_RE is `regionRE`'s pattern. */
export const SETUP_REGION_RE = /^[a-z]+-[a-z]+[0-9]+$/

/** SETUP_NAME_RE is `config.ProjectNameRE`'s pattern. */
export const SETUP_NAME_RE = /^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/

/** refuseAnswers throws the console's own sentence for one malformed answer. */
function refuseAnswers(sentence: string): never {
  throw new SourceRefusal(sentence)
}

/**
 * SetupAnswersHeld is the answers **as main holds them**: the five questions
 * `SetupAnswers` names (the wire type the renderer sends, `../shared/new-ops`),
 * with `bake_repos` in **three** states rather than the wire's two. `undefined` is a question nobody answered, and the CLI's rule for
 * that field is that an absent key means **yes**: only a written `false` turns
 * the bake off (`internal/imagebake`'s plan, `internal/cli/init.go`'s
 * `carryBakeAnswer`, which exists precisely because a person who answered no
 * would otherwise get baking on). `projectpolicy.Policy.BakesRepos` and
 * `localcfg.BakeReposEffective` read it the same way.
 *
 * The three states therefore have to reach the bytes **and** the window, and a
 * boolean cannot carry them: flattening "nobody answered" into `false` would
 * both omit the key -- so the run bakes -- and draw "no" in the window, which is
 * the operator being told the opposite of what will happen.
 *
 * `budget` is a question with the same two states -- a number the operator
 * chose, or `undefined` for a field left empty, which the CLI answers with its
 * own default (100) -- and it is dropped for a CLI that cannot take the member
 * (`budgetAnswerSupported`), so a held answer of `undefined` is either nobody's
 * answer or a CLI too old for it. The two are the same document either way.
 */
export interface SetupAnswersHeld {
  readonly gcp_project: string
  readonly region: string
  readonly name: string
  readonly rigor: SetupRigor
  readonly budget: number | undefined
  readonly bake_repos: boolean | undefined
}

/**
 * validateSetupAnswers reads the object a renderer sent as the answers, and it
 * is **strict in both directions**: a member this console does not send is
 * refused by name (`SETUP_ANSWER_KEYS`, and no `version`, which main adds when
 * it serialises), and a required question that is missing or outside its
 * grammar is refused with the CLI's own rule.
 *
 * `name`, `budget` and `bake_repos` may still be **unanswered** -- absent, or,
 * for `name`, the blank string a renderer sends for the field it drew empty,
 * which is the same statement to the CLI's reader -- and each means its own
 * thing. A switch nobody answered is the CLI's own default, which for
 * `bake_repos` is **yes** -- only a written `false` turns the bake off -- so it
 * is held as unanswered and never flattened into `false`. A name nobody gave is
 * a refusal the CLI makes itself: on this door (`--json`, and no terminal) it
 * cannot ask (`init_inputs.go`'s `canAsk`), so it stops with "there is no project
 * config yet: pass --name <name> (the project's name)", and main invents none --
 * a suggestion would take the wizard's own `fugaro-setup-XXXXXX` working
 * directory for a name the CLI calls permanent. An amount nobody answered is
 * `undefined` and not a zero: the CLI reads an absent `budget` as its own
 * default, while `0` is an amount the alert cannot be created with and is
 * refused -- so the two must not be one state here either. `rigor` may not be
 * absent at all: it has no default (D900), which is the one answer the wizard
 * requires.
 */
export function validateSetupAnswers(raw: unknown): SetupAnswersHeld {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) {
    refuseAnswers(SETUP_ANSWERS_NOT_AN_OBJECT)
  }
  const record = raw as Record<string, unknown>
  for (const key of Object.keys(record)) {
    if (!SETUP_ANSWER_KEYS.includes(key)) {
      refuseAnswers(`${SETUP_ANSWERS_UNKNOWN_KEY}: ${key}`)
    }
  }
  const project = record['gcp_project']
  if (typeof project !== 'string') refuseAnswers(SETUP_ANSWERS_MISSING)
  if (!SETUP_PROJECT_RE.test(project)) refuseAnswers(SETUP_PROJECT_REFUSAL)
  const region = record['region']
  if (typeof region !== 'string') refuseAnswers(SETUP_ANSWERS_MISSING)
  if (!SETUP_REGION_RE.test(region)) refuseAnswers(SETUP_REGION_REFUSAL)
  const rigor = record['rigor']
  if (rigor === undefined || rigor === null) refuseAnswers(SETUP_ANSWERS_MISSING)
  if (typeof rigor !== 'string' || !SETUP_LAYER_RIGOR_MODES.includes(rigor as SetupRigor)) {
    refuseAnswers(SETUP_RIGOR_REFUSAL)
  }
  const name = record['name']
  // A **blank** name is the wire's own spelling of "unanswered", not a malformed
  // one, so it is not refused: the renderer sends the field it drew, and an
  // operator who left the name empty sends `''`. The CLI reads it as unanswered
  // exactly this way (`setupAnswers.answer`: `name` is answered when it is not
  // the empty string) and the answers file's schema makes `name` optional for
  // the same reason, so refusing `''` here would refuse a state the CLI is
  // designed to take -- and would take away the `left_for_you` line naming
  // `--name` that is how the console hands a first setup's naming back.
  if (name !== undefined && name !== null && name !== '') {
    if (typeof name !== 'string' || !SETUP_NAME_RE.test(name)) refuseAnswers(SETUP_NAME_REFUSAL)
  }
  const bake = record['bake_repos']
  if (bake !== undefined && bake !== null && typeof bake !== 'boolean') {
    refuseAnswers(SETUP_BAKE_REFUSAL)
  }
  const budget = record['budget']
  // A whole number of units, and never a fractional one: `--budget 1.5` is a
  // word cobra's `IntVar` refuses, and the answers file's schema says `integer`.
  // `null` is the wire's own spelling of "nobody answered", exactly as it is for
  // `bake_repos`, so it is unanswered and not a malformed amount.
  if (budget !== undefined && budget !== null) {
    if (
      typeof budget !== 'number' ||
      !Number.isInteger(budget) ||
      budget < SETUP_BUDGET_MIN ||
      budget > SETUP_BUDGET_MAX
    ) {
      refuseAnswers(SETUP_BUDGET_REFUSAL)
    }
  }
  const answers: SetupAnswersHeld = {
    gcp_project: project,
    region,
    name: typeof name === 'string' ? name : '',
    rigor: rigor as SetupRigor,
    budget: typeof budget === 'number' ? budget : undefined,
    bake_repos: typeof bake === 'boolean' ? bake : undefined,
  }
  return Object.freeze(answers)
}

/** SETUP_PLAN_UNKNOWN_ARGUMENT is what a plan payload carrying anything but
 * `answers` is refused with: there is no path and no flag in it, by contract. */
export const SETUP_PLAN_UNKNOWN_ARGUMENT = 'setup.plan takes the answers and nothing else'

/** SETUP_APPLY_UNKNOWN_ARGUMENT is what an apply payload carrying any member at
 * all is refused with: the operation's payload is **empty** by contract, because
 * apply reuses the answers and the digest main held at plan time. A renderer
 * that sent `{yes: true}`, or new answers, or a path, is not answered. */
export const SETUP_APPLY_UNKNOWN_ARGUMENT = 'setup.apply takes nothing'

/** SETUP_PLAN_PENDING_REFUSAL is what a second plan is refused with while one
 * window is still waiting for its answer: one wizard at a time, so the bytes one
 * window showed cannot be replaced by another's. */
export const SETUP_PLAN_PENDING_REFUSAL =
  'a setup plan is already waiting for your answer in its window'

/** SETUP_NO_PLAN_REFUSAL is what an apply with no plan behind it is refused
 * with. */
export const SETUP_NO_PLAN_REFUSAL = 'no setup plan is waiting: nothing is applied'

/** SETUP_NO_YES_REFUSAL is what an apply is refused with while the window that
 * showed the plan has not said yes. It is the whole of the gate: a renderer
 * cannot start a setup, and neither can a frame that is not the window's own. */
export const SETUP_NO_YES_REFUSAL =
  'the setup plan has not been confirmed in its window: nothing is applied'

/** SETUP_BYTES_CHANGED_REFUSAL is what an apply is refused with when the held
 * bytes no longer hash to the digest the window showed. Nothing in this console
 * can change them; a buffer that changed is a buffer something else wrote to,
 * and the run it would spawn is not the run the operator agreed to. */
export const SETUP_BYTES_CHANGED_REFUSAL =
  'the setup answers changed after the window showed them: nothing is applied'

/** SETUP_NO_EVENTS_REFUSAL is what a child that printed no event line at all is
 * refused with, when its stderr said nothing either. */
export const SETUP_NO_EVENTS_REFUSAL = 'setup printed no events'

/** SETUP_FAILED_WITHOUT_A_REASON stands in for an `error` line whose own
 * `error` member is missing or empty. The shape is the CLI's (`SetupFailure`),
 * so such a line is a line no CLI wrote; the console says the run failed rather
 * than answering an empty sentence. */
export const SETUP_FAILED_WITHOUT_A_REASON = 'fugaro setup failed'

/**
 * setupFailureSentence is a plan's own refusal in the operator's words: the
 * CLI's `error` (its `SetupFailure`), with the stage the run stopped at in front
 * of it when the event names one -- a refusal at "project" reads as exactly
 * that -- and the CLI's `fix` after it when the line carries one.
 *
 * It exists because a plan's refusal reaches the renderer as a **sentence**, not
 * as a feed (`setupFeedFailure` below), so the stage has nowhere else to travel:
 * a screen drawing the refusal cannot ask the event it never received.
 */
export function setupFailureSentence(line: JSONObject): string {
  const stage = typeof line['stage'] === 'string' ? line['stage'] : ''
  const said = line['error']
  const reason =
    typeof said === 'string' && said !== '' ? said : SETUP_FAILED_WITHOUT_A_REASON
  const fix = typeof line['fix'] === 'string' ? line['fix'] : ''
  const head = stage === '' ? reason : `${stage}: ${reason}`
  return fix === '' ? head : `${head} (${fix})`
}

/**
 * setupFeedFailure reads a **plan** feed's own ending: the sentence the run
 * stopped at when its last line is an `error` event, and `null` when it reached
 * its `done` line.
 *
 * It is what keeps a refused plan from being confirmed. `setup --json` refuses
 * what it cannot ask about -- a missing `--name` is one line of `error` and exit
 * 1 -- and a window opened over that feed would ask the operator to confirm a run
 * that has already failed, whose yes would spawn an apply that fails the same
 * way. The last line decides, exactly as it does for `done`: a run with steps
 * left for the operator ends `done` at exit 1 and not `error` (ST2), so the exit
 * code is not read here.
 */
export function setupFeedFailure(lines: readonly JSONObject[]): string | null {
  const last = lines[lines.length - 1]
  if (last === undefined || last['type'] !== 'error') return null
  return setupFailureSentence(last)
}

// ---------------------------------------------------------------------------
// The bytes, held once.
// ---------------------------------------------------------------------------

/**
 * serialiseSetupAnswers writes the answers file the CLI decodes
 * (`schemas/setup-answers.schema.json`, ST1): the schema's `version` first, then
 * the answers in the order `SETUP_ANSWERS_FILE_KEYS` names them. A member
 * with no answer is left out rather than written empty -- an empty `name` is
 * read as unanswered by the CLI either way (`setupAnswers.answer`), and the
 * schema's `name` and `budget` are optional, so each key is omitted rather than
 * written blank: one spelling of "nobody answered this" instead of two -- with
 * **one** exception: `bake_repos` is written whenever it was answered, `false`
 * included, because an absent key is not "no" to any reader of that field but
 * the CLI's default, which is **yes**. Only a written `false` turns baking off.
 *
 * An absent `budget` is the CLI's own default amount (100) and never a zero,
 * which is why it is omitted and not written as `0`: an amount the alert cannot
 * be created with is refused by the run, while an absent one is not asked about
 * at all. A held `budget` of `undefined` reaches here for a CLI below
 * `BUDGET_ANSWER_CLI_FLOOR` too (`setup.plan` drops the answer there), and the
 * document is the same one.
 *
 * The order is fixed and the document is small, so the bytes are a function of
 * the answers and nothing else: the digest the window shows is the digest of the
 * run, and a second call with the same answers writes the same bytes (which is
 * exactly what this module does **not** rely on -- it serialises once and keeps
 * the buffer).
 */
export function serialiseSetupAnswers(answers: SetupAnswersHeld): Uint8Array {
  const document: Record<string, unknown> = {
    version: 1,
    gcp_project: answers.gcp_project,
    region: answers.region,
  }
  if (answers.name !== '') document['name'] = answers.name
  document['rigor'] = answers.rigor
  // The budget answer is written where there is one. `SETUP_ANSWERS_FILE_KEYS`
  // puts it between the rigor mode and the bake switch, which is the order
  // `setupAnswerQuestions` lists its members in -- one order in both documents.
  if (typeof answers.budget === 'number') document['budget'] = answers.budget
  // **A written no has to be written.** `bake_repos` omitted means *yes* to
  // every reader of the field (`internal/imagebake`'s plan, `localcfg`'s
  // `BakeReposEffective`, `projectpolicy`'s `BakesRepos`), so omitting it for an
  // operator who declined would bake their repositories into the image their
  // jobs run while the window says they will not be. The key is absent only for
  // a switch nobody answered, which is the CLI's own default.
  if (typeof answers.bake_repos === 'boolean') document['bake_repos'] = answers.bake_repos
  return new TextEncoder().encode(JSON.stringify(document))
}

/** setupAnswersDigest is the SHA-256 of the held bytes, in lowercase hex -- what
 * the window shows and what apply re-checks. */
export function setupAnswersDigest(bytes: Uint8Array): string {
  return createHash('sha256').update(bytes).digest('hex')
}

// ---------------------------------------------------------------------------
// The working directory, and the feed's bounds.
// ---------------------------------------------------------------------------

/** SETUP_DIR_PREFIX is the prefix of the fresh working directory's name. */
export const SETUP_DIR_PREFIX = 'fugaro-setup-'

/** SETUP_DIR_MODE is the directory's mode, and it is the private one the CLI's
 * own working directory has (`privateWorkingDirectory`, `spawn.ts`): owner-only,
 * so nothing another account on the machine can read or write is where a setup
 * runs. The directory holds **no file** -- the answers travel on stdin -- and it
 * exists so that the CLI's working directory is a directory of its own rather
 * than the console's or `/`. */
export const SETUP_DIR_MODE = 0o700

/** makeSetupDirectory makes the fresh empty directory a setup run takes as its
 * working directory. `mkdtemp` is what makes it fresh and unguessable; the
 * `chmod` is what makes the mode a fact rather than a promise about the umask. */
export function makeSetupDirectory(base?: string): string {
  const path = mkdtempSync(join(base ?? tmpdir(), SETUP_DIR_PREFIX))
  chmodSync(path, SETUP_DIR_MODE)
  return path
}

/** removeSetupDirectory takes it away again, contents and all. It is called on
 * every path out of a plan that does not end in a run: a refusal, a decline, and
 * the moment an apply's outcome is held. */
export function removeSetupDirectory(path: string): void {
  if (path === '') return
  rmSync(path, { recursive: true, force: true })
}

/** SetupBounds is what one setup feed is read under, and it is **the reads'
 * own bound rather than a second one** (`NEW_READ_BOUNDS`, `source/new-reads.ts`):
 * the contract gives the four streams and the setup feed the same three
 * (`MAX_FEED_LINE_LENGTH` a line, 10,000 rows, 16 MiB in all), because it is one
 * decision -- how much of a stream the console holds in memory before it ends
 * the read. Task AL's `ipc.ts` constants are what replaces these values, and
 * they replace them once for both. */
export type SetupBounds = NewReadBounds

/**
 * readEventLines reads a setup child's stdout as its `--events` feed: NDJSON, a
 * document a line, and **the last line is the run's own account** (`done`, or
 * `error` naming the stage it stopped at). A line the console cannot read, one
 * past the ceiling, or the row and byte caps **end the read where it is**, which
 * is the interrupted read the contract names (no `done` line, and a screen draws
 * Retry) -- never a refusal, which would throw away the stages that did arrive,
 * and never a silently short list read as a complete one.
 */
export function readEventLines(stdout: string, bounds: SetupBounds = NEW_READ_BOUNDS): JSONObject[] {
  const lines: JSONObject[] = []
  let held = 0
  for (const raw of stdout.split('\n')) {
    if (raw.trim() === '') continue
    if (raw.length > bounds.maxLineLength) break
    let parsed: unknown
    try {
      parsed = JSON.parse(raw)
    } catch {
      break
    }
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) break
    lines.push(parsed as JSONObject)
    held += raw.length
    if (lines.length >= bounds.maxRows || held >= bounds.maxBytes) break
  }
  return lines
}

// ---------------------------------------------------------------------------
// The seams, and the flow.
// ---------------------------------------------------------------------------

/**
 * SetupPlanner is one setup run as a child process: task AL's argv row for the
 * operation, in the working directory the wizard made. The directory is an
 * argument rather than something the planner knows because it is this module
 * that makes it, removes it and holds it for the window's lifetime (O7).
 */
export type SetupPlanner = (name: SetupSpawnName, directory: string) => SpawnPlan

/** SetupFeedAnswer is one setup operation's answer: the feed's own document, the
 * `data` half of the console's reply (`registerConsoleHandlers` wraps it), and
 * the shape `decodeNewOperationAnswer` reads as a `SetupFeed`. */
export interface SetupFeedAnswer {
  readonly document: { readonly lines: readonly JSONObject[] }
}

/** SetupHandler is one of the two handlers, `ConsoleHandler` for the names
 * `ipc.ts` does not carry yet: it answers asynchronously and throws a
 * `SourceRefusal` for anything it refused. */
export type SetupHandler = (args: ValidatedArguments) => Promise<SetupFeedAnswer>

/** SetupHandlers is the two of them by name, the shape I-OPS spreads into the
 * `gated` table. */
export interface SetupHandlers {
  readonly 'setup.plan': SetupHandler
  readonly 'setup.apply': SetupHandler
}

/** SetupWizardDeps is what the wizard is built from. Everything but `confirmer`
 * and `plan` is a seam a test drives. */
export interface SetupWizardDeps {
  /** the window, which is SU1's one window and no second implementation. */
  readonly confirmer: LaunchConfirmer
  readonly plan: SetupPlanner
  /** `cliVersion` is the resolved binary's own version line, or `''` when the
   * console could not read one. */
  readonly cliVersion: string
  /** `floor` is task AL's one constant for the two rows, `null` while no release
   * carries them: every setup refuses its own sentence rather than spawning an
   * argv nothing answers. */
  readonly floor?: string | null
  /** `budgetFloor` is the **other** floor: the release whose answers reader takes
   * the `budget` member (`BUDGET_ANSWER_CLI_FLOOR`, `shared/version.ts`). It is a
   * seam like `floor` because the constant is `null` until a release names
   * itself, so the branch that keeps the amount could not be driven through it;
   * `null` is the state today. */
  readonly budgetFloor?: string | null
  /** `lapsed` answers whether the licence has lapsed for the installation this
   * setup runs in, and it is asked **where the apply really starts** and not
   * only at the door: the window's own yes reaches `runApply` from `decide`,
   * with no IPC handler anywhere on that path, so `lapseHandlers`' gate on
   * `setup.apply` -- the design's `refusedAfterLapse` column
   * (`docs/lapse-read-only/spec.md`) -- is not what stops a plan made after the
   * lapse, which is kept and opens its window. The plan is a dry run and stays
   * reachable; the apply it stands for is the row the lapse takes away, and this
   * is the column made true on the window's path. It is the same fact the two
   * writes ask (`modelsadd.ts`, `imageadd.ts`), and for the same reason.
   *
   * It is required rather than optional: a wizard built without it would be an
   * apply the lapse does not stop, and no test of a payload could see the
   * difference. */
  readonly lapsed: () => boolean
  readonly bounds?: SetupBounds
  /** `serialise` is where the bytes come from. It is called **once per plan**,
   * and the buffer it answers is the one both children are given: a test that
   * wants to change a byte under the window hands one back it keeps a reference
   * to. */
  readonly serialise?: (answers: SetupAnswersHeld) => Uint8Array
  /** `digest` is the re-check apply runs, `setupAnswersDigest` unless a test
   * says otherwise. */
  readonly digest?: (bytes: Uint8Array) => string
  /** `makeDirectory` makes the working directory a run takes, and
   * `removeDirectory` takes it away. */
  readonly makeDirectory?: () => string
  readonly removeDirectory?: (path: string) => void
  readonly spawner?: ChildSpawner
  readonly log?: SpawnLogWriter
  readonly timers?: Timers
  readonly deadlineMs?: number
}

/** SetupWizard is the two handlers. */
export type SetupWizard = SetupHandlers

/** SetupOutcome is what became of one apply: the feed the child printed, or the
 * sentence the console refused it with. It is held rather than thrown on the
 * window's own path -- nobody is awaiting that one -- so a renderer that asks
 * afterwards is told the same thing the run did. */
type SetupOutcome =
  | { readonly kind: 'feed'; readonly lines: readonly JSONObject[] }
  | { readonly kind: 'refused'; readonly sentence: string }

/** HeldWizard is one plan: what the window showed, the bytes it showed them
 * from, and what has become of the run since. */
interface HeldWizard {
  readonly view: SetupView
  readonly bytes: Uint8Array
  readonly digest: string
  readonly applyPlan: SpawnPlan
  readonly directory: string
  armed: boolean
  running: Promise<void> | null
  outcome: SetupOutcome | null
}

/**
 * createSetupWizard builds the wizard's two handlers.
 *
 * The flow is one plan at a time and one gate:
 *
 *   1. `setup.plan` refuses below the floor, validates the answers, serialises
 *      them once, makes the private directory, builds both command lines, holds
 *      the record, runs the plan child with the bytes on its stdin, and only
 *      then shows SU1's window the answers it parsed beside the digest. **A plan
 *      that refused shows no window and answers its own sentence instead**, and
 *      a refusal has exactly two shapes: a child that printed no line at all
 *      (its stderr is the sentence), and a feed whose **last line is `error`**
 *      (`setupFeedFailure` -- the CLI's own refusal, with the stage it stopped
 *      at), which is what `--json` answers when it cannot ask about something,
 *      a missing `--name` among them;
 *   2. the window's plan frame answering no gives up the record and the
 *      directory, and nothing is applied;
 *   3. the window's **own top frame** answering yes arms the record and starts
 *      the apply: the same bytes, after the digest is checked again;
 *   4. `setup.apply` -- payload empty, refused otherwise -- is the renderer's
 *      door to that run: it refuses while no plan is held or the window has not
 *      said yes, joins the apply the yes started rather than spawning a second
 *      child, and answers the feed the child printed, or throws the sentence it
 *      was refused with.
 */
export function createSetupWizard(deps: SetupWizardDeps): SetupWizard {
  const bounds = deps.bounds ?? NEW_READ_BOUNDS
  const serialise = deps.serialise ?? serialiseSetupAnswers
  const digest = deps.digest ?? setupAnswersDigest
  const makeDirectory = deps.makeDirectory ?? ((): string => makeSetupDirectory())
  const removeDirectory = deps.removeDirectory ?? removeSetupDirectory
  const deadlineMs = deps.deadlineMs ?? SETUP_DEADLINE_MS

  let held: HeldWizard | null = null

  /** runFeed runs one child under the deadline with the held bytes on its stdin,
   * and answers the event lines it printed. */
  const runFeed = async (plan: SpawnPlan, bytes: Uint8Array): Promise<JSONObject[]> => {
    const result = await runToCompletion(plan, {
      stdinBytes: bytes,
      timeoutMs: deadlineMs,
      ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
      ...(deps.log === undefined ? {} : { log: deps.log }),
      ...(deps.timers === undefined ? {} : { timers: deps.timers }),
    })
    const lines = readEventLines(result.stdout, bounds)
    // **A child that printed no line at all is the one failure a feed cannot
    // carry.** A run that ends with steps left for the operator ends `done` with
    // exit 1 and not `error` (ST2), so the exit code decides nothing here: the
    // last line does. No line at all is a child that died before it could say
    // anything -- a bad flag, a config refusal, a missing binary -- and the
    // sentence is the CLI's own, which is what a screen draws.
    if (lines.length === 0) {
      throw new SourceRefusal(cliSentence(result.stderr) ?? SETUP_NO_EVENTS_REFUSAL)
    }
    return lines
  }

  /** answerOf is the held outcome as an answer, or the sentence it was refused
   * with. */
  const answerOf = (wizard: HeldWizard): SetupFeedAnswer => {
    const outcome = wizard.outcome
    if (outcome === null) throw new SourceRefusal(SETUP_NO_PLAN_REFUSAL)
    if (outcome.kind === 'refused') throw new SourceRefusal(outcome.sentence)
    return { document: { lines: outcome.lines.map((line) => throughClasses(line)) } }
  }

  /** runApply runs the apply once for a record: the lapse first, then the
   * digest, then the same bytes, and the directory goes as soon as the outcome is
   * held. */
  const runApply = async (wizard: HeldWizard): Promise<void> => {
    // **The lapse's sentence, and the first fact.** The window's yes reaches
    // this function from `decide`, and no IPC handler is on that path at all,
    // so the gate at the door is not what stops this child -- see `lapsed`
    // above. Nothing is spawned: the sentence is the console's own one for the
    // lapse (`LAPSE_REFUSAL`), which is the same sentence a lapsed console
    // answers `setup.apply` with, so the two paths say one thing.
    if (deps.lapsed()) {
      wizard.outcome = { kind: 'refused', sentence: LAPSE_REFUSAL }
      removeDirectory(wizard.directory)
      return
    }
    if (wizard.digest !== digest(wizard.bytes)) {
      wizard.outcome = { kind: 'refused', sentence: SETUP_BYTES_CHANGED_REFUSAL }
      removeDirectory(wizard.directory)
      return
    }
    try {
      const lines = await runFeed(wizard.applyPlan, wizard.bytes)
      wizard.outcome = { kind: 'feed', lines }
    } catch (error) {
      wizard.outcome = {
        kind: 'refused',
        sentence: error instanceof SourceRefusal ? error.sentence : SETUP_NO_EVENTS_REFUSAL,
      }
    } finally {
      removeDirectory(wizard.directory)
    }
  }

  /** ensureApply starts the apply at most once, and is what the window's yes and
   * the renderer's `setup.apply` share: the two may arrive in either order, and
   * neither may spawn a second child. */
  const ensureApply = (wizard: HeldWizard): Promise<void> => {
    if (wizard.running === null) wizard.running = runApply(wizard)
    return wizard.running
  }

  /** decide is the window's answer, reached from the window's own main frame
   * (`confirm.ts`'s sender and frame gate). A yes starts the apply; a no gives
   * the record and its directory up, and applies nothing. */
  const decide = (wizard: HeldWizard, yes: boolean): void => {
    if (held !== wizard || wizard.outcome !== null || wizard.running !== null) return
    if (!yes) {
      held = null
      removeDirectory(wizard.directory)
      return
    }
    wizard.armed = true
    void ensureApply(wizard).catch(() => undefined)
  }

  /** refuseBelowFloor is the whole of the floor check, before anything is
   * spawned or made. */
  const refuseBelowFloor = (): void => {
    const floor = deps.floor ?? null
    if (floor !== null && meetsCliFloor(deps.cliVersion, floor)) return
    throw new SourceRefusal(setupFloorSentence(floor, deps.cliVersion))
  }

  /**
   * holdBudget is the held answers as the installed CLI can take them: the
   * budget answer is dropped for a CLI below `BUDGET_ANSWER_CLI_FLOOR`
   * (`shared/version.ts`) and kept for one at or above it.
   *
   * **It is applied to the held answers and not to the bytes alone**, because
   * the bytes and the window are one document: `setupView` draws what this
   * answers and `serialise` writes what it answers, so a budget drawn in the
   * window is a budget the child is given. The alternative -- serialising the
   * budget away and drawing it anyway -- would be a window confirming an amount
   * the run never uses, which is the one thing this screen exists to prevent.
   *
   * The answer is dropped rather than refused: the question is the CLI's own and
   * the field is the console's, and an operator whose fugaro cannot take an
   * amount is better served by a setup that runs with the CLI's default than by
   * a refusal of a form this console drew.
   */
  const holdBudget = (answers: SetupAnswersHeld): SetupAnswersHeld => {
    if (budgetAnswerSupported(deps.cliVersion, deps.budgetFloor ?? BUDGET_ANSWER_CLI_FLOOR)) {
      return answers
    }
    return Object.freeze({ ...answers, budget: undefined })
  }

  return Object.freeze({
    /**
     * plan is `setup --plan-only --answers-file - --events --json`: validate,
     * serialise once, hold, run, show the window, answer the feed.
     */
    'setup.plan': async (args: ValidatedArguments): Promise<SetupFeedAnswer> => {
      refuseBelowFloor()
      const payload: Record<string, unknown> = { ...args }
      for (const key of Object.keys(payload)) {
        if (key !== 'answers') throw new SourceRefusal(SETUP_PLAN_UNKNOWN_ARGUMENT)
      }
      const answers = holdBudget(validateSetupAnswers(payload['answers']))
      // One wizard at a time: a plan that is still waiting for its window's
      // answer may not be replaced, because the bytes the window showed are the
      // bytes the operator agreed to.
      if (held !== null && held.outcome === null) throw new SourceRefusal(SETUP_PLAN_PENDING_REFUSAL)
      const bytes = serialise(answers)
      const shownDigest = digest(bytes)
      const directory = makeDirectory()
      let planPlan: SpawnPlan
      let applyPlan: SpawnPlan
      try {
        planPlan = deps.plan('setup.plan', directory)
        applyPlan = deps.plan('setup.apply', directory)
      } catch (error) {
        removeDirectory(directory)
        throw error
      }
      const wizard: HeldWizard = {
        // The answers **as main parsed them**, which is the whole point of the
        // view: the renderer's own object reaches no row.
        view: setupView({
          project: answers.gcp_project,
          region: answers.region,
          name: answers.name,
          rigor: answers.rigor,
          budgetUnits: answers.budget,
          bakeRepos: answers.bake_repos,
          digest: shownDigest,
          planArgv: planPlan.argv,
          applyArgv: applyPlan.argv,
        }),
        bytes,
        digest: shownDigest,
        applyPlan,
        directory,
        armed: false,
        running: null,
        outcome: null,
      }
      held = wizard
      let lines: JSONObject[]
      try {
        lines = await runFeed(planPlan, bytes)
      } catch (error) {
        held = null
        removeDirectory(directory)
        throw error
      }
      // **A refused plan is not confirmed.** The CLI's own refusal arrives as
      // the feed's last line (`error`), and a window opened over it would ask
      // the operator to agree to a run that has already failed -- with a yes
      // whose apply fails the same way. Nothing is held and the directory goes
      // with the record; the sentence is the CLI's.
      const failed = setupFeedFailure(lines)
      if (failed !== null) {
        held = null
        removeDirectory(directory)
        throw new SourceRefusal(failed)
      }
      // The window is shown only for a plan that ran, and the feed is answered
      // after it: the operator reads the answers and the digest while the screen
      // behind the window draws the plan's own events. The window's identity is
      // the project the answers name (`setupConfirmID`).
      deps.confirmer.open(wizard.view, (yes: boolean) => {
        decide(wizard, yes)
      })
      return { document: { lines: lines.map((line) => throughClasses(line)) } }
    },

    /**
     * apply is `setup --answers-file - --events --json` over the answers the
     * window showed. Its payload is empty and it must be: nothing a renderer
     * sends can reach the child, and the held bytes are the ones the digest was
     * taken of.
     */
    'setup.apply': async (args: ValidatedArguments): Promise<SetupFeedAnswer> => {
      if (Object.keys(args).length > 0) throw new SourceRefusal(SETUP_APPLY_UNKNOWN_ARGUMENT)
      const wizard = held
      if (wizard === null) throw new SourceRefusal(SETUP_NO_PLAN_REFUSAL)
      if (!wizard.armed) throw new SourceRefusal(SETUP_NO_YES_REFUSAL)
      await ensureApply(wizard)
      // A spent record is not offered twice: the outcome has been answered, and
      // the next `setup.plan` is a new wizard rather than this one's plan.
      if (wizard.outcome !== null) held = null
      return answerOf(wizard)
    },
  })
}
