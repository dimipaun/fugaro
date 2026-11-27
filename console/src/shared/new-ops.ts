// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The nine new operations' names, payloads and answer shapes (desktop-console
// improvements plan, task C-OPS; the document that goes with this module is
// `docs/console-improvements/contract-ops.md`).
//
// **Why a contract and not a consumer.** The allow-list task (AL) adds the rows
// to `src/main/ipc.ts`, the source task (SRC) writes the handlers, and five
// screens read the answers. Written in that order, each of them would invent its
// own spelling of a name or a field and the console would discover the
// disagreement at runtime, in a window, in front of an operator. So the names,
// the payloads and the answer shapes are frozen here first, the fixture bridge
// the consumers build against (`console/test/support/newOpAnswers.ts`) answers
// each of them with a value that decodes against them, and I-OPS holds those
// fixtures to the goldens the CLI tasks record. The allow-list task implements the operations **beside** this
// module and imports nothing from it: what it must agree with is the wire, and
// the wire is the CLI's.
//
// **The three things each side may rely on** (the plan's own list, each one a
// reading of a document that could otherwise be drawn as a figure nobody
// measured):
//
//   - **an unknown role reads as `none`.** A CLI newer than this build may name
//     a role this build has never heard of (WH1's `role`), and `none` is the one
//     answer that disables every write rather than enabling one: a console that
//     refused the document instead would leave every control enabled, which is
//     the state the role read exists to prevent (RB4).
//   - **a stream without an `end` is interrupted.** The four feeds are NDJSON
//     (`runs ls --stream`, `report --stream`, `budget show --stream`,
//     `setup --events`), so the end of the stream is a line and not a length:
//     `end` (or, for setup, a `done` event) is what says the CLI finished. A
//     feed that stops without one is a read the console ended -- a cap, a killed
//     child, a lost channel -- and it is drawn as an interrupted read with
//     Retry, never as a complete list that happens to be short (RS2, R1).
//   - **an answers object never names a file.** `setup.plan` carries the six
//     answers and `setup.apply` carries nothing at all; main serialises them
//     into bytes it holds in memory and hands those bytes to the child on stdin
//     (`--answers-file -`). A path in either payload is a file a hostile process
//     of the same user could swap between main's check and the CLI's read.
//
// **What a stream's document is.** NDJSON has no single document, so a feed's
// `document` is `{lines: [...]}`: the parsed lines, in the order the CLI wrote
// them. Everything a screen reads -- the rows, the `end`, the failure -- is
// derived from those lines here, in one place, rather than by each reader
// slicing an array of its own. A one-shot read's `document` is the CLI's own
// document, unchanged, which is what makes I-OPS's job possible: a fixture for a
// feed is the golden's own lines, and a fixture for a read is the golden's own
// document.
//
// **The answer envelope.** Every operation's reply is the console's ordinary one
// (`ConsoleReply`, `src/main/ipc.ts`): `{ok: true, data}` or
// `{ok: false, reason}`. `decodeNewOperationAnswer` reads the `data` half -- the
// value the CLIs' handler answered -- and inside it the `document` key for the
// eight operations that spawn a child, exactly as `handlersFor` already wraps a
// CLI read (`src/main/index.ts`). `settings.editor.open` is the exception and
// follows `settings.edit` instead: it spawns nothing itself and answers the
// outcome object (`{ok, sentence}`) directly.
//
// **Nothing here reads a byte of the wire itself.** This module has no `node:`,
// no `electron` and no I/O of its own: `src/shared/` is loaded by the renderer,
// and `sanitize.test.ts` scans the directory to keep it that way.

import { decodeRunsList } from './schema'
import type { Decoded, RunsListRow, SchemaRefusal, SchemaRefusalName, Skew } from './schema'
import type { TextClass } from './sanitize'

// ---------------------------------------------------------------------------
// The names, the methods and the payloads.
// ---------------------------------------------------------------------------

/**
 * NewOperationName is the closed set of operations this contract is about, each
 * spelled exactly as the CLI's operation is (`console/src/main/ipc.ts`,
 * `CONSOLE_OPERATIONS[].name`). A name here and no row there is a consumer the
 * console cannot reach; a row there and no name here is an operation outside the
 * contract, which I-OPS's `ops-contract.test.ts` checks in one direction only
 * (operations added later by MW1 and MW2 are deliberately outside it).
 */
export type NewOperationName =
  | 'runs.stream'
  | 'spend.stream'
  | 'budget.stream'
  | 'whoami'
  | 'image.catalog.ls'
  | 'recipes.ls'
  | 'setup.plan'
  | 'setup.apply'
  | 'settings.editor.open'

/** NEW_OPERATION_NAMES is the set above as a value, in the plan's own order. */
export const NEW_OPERATION_NAMES: readonly NewOperationName[] = Object.freeze([
  'runs.stream',
  'spend.stream',
  'budget.stream',
  'whoami',
  'image.catalog.ls',
  'recipes.ls',
  'setup.plan',
  'setup.apply',
  'settings.editor.open',
])

/**
 * NewOpsMethod is the bridge method each operation is reached by. The names
 * follow the console's own read names -- `listRuns`, `showBudget`, `runReport`,
 * `editSetting` -- and not the CLI's operation names, because the bridge is the
 * renderer's surface rather than a copy of `ipc.ts`.
 */
export type NewOpsMethod =
  | 'streamRuns'
  | 'streamSpend'
  | 'streamBudget'
  | 'whoAmI'
  | 'listImageCatalog'
  | 'listRecipes'
  | 'planSetup'
  | 'applySetup'
  | 'openSettingsEditor'

/**
 * NEW_OPERATION_METHODS is the method each name is reached by. AL's
 * `CONSOLE_OPERATIONS` row must carry the method this table gives its name --
 * that pairing, and not the spelling, is what I-OPS holds the two sides to.
 */
export const NEW_OPERATION_METHODS: Readonly<Record<NewOperationName, NewOpsMethod>> = Object.freeze({
  'runs.stream': 'streamRuns',
  'spend.stream': 'streamSpend',
  'budget.stream': 'streamBudget',
  whoami: 'whoAmI',
  'image.catalog.ls': 'listImageCatalog',
  'recipes.ls': 'listRecipes',
  'setup.plan': 'planSetup',
  'setup.apply': 'applySetup',
  'settings.editor.open': 'openSettingsEditor',
})

/**
 * NEW_OPERATION_PAYLOAD_KEYS is the closed key set of every operation's payload,
 * and it is the whole of what a renderer may send. Each operation's row in
 * `ipc.ts` validates its arguments against exactly these keys, so a key that is
 * not here is refused before a child is spawned (`unknown-key`, `ipc.ts`) rather
 * than reaching an argv: no flag and no path is the renderer's to choose, and
 * the three the plan names are the ones with something at stake --
 * `settings.editor.open` takes `{key}` and nothing else (the layer and the phase
 * stay main's), `setup.apply` takes nothing at all (it reuses the answers main
 * held at plan time), and `setup.plan` takes an answers object and never a path.
 *
 * The table says which keys an operation may carry; the request types below say
 * which of them it must carry.
 */
export const NEW_OPERATION_PAYLOAD_KEYS: Readonly<Record<NewOperationName, readonly string[]>> =
  Object.freeze({
    'runs.stream': Object.freeze(['since']),
    'spend.stream': Object.freeze(['by', 'since', 'until', 'repo']),
    'budget.stream': Object.freeze(['repo', 'all', 'compute']),
    whoami: Object.freeze([]),
    'image.catalog.ls': Object.freeze([]),
    'recipes.ls': Object.freeze([]),
    'setup.plan': Object.freeze(['answers']),
    'setup.apply': Object.freeze([]),
    'settings.editor.open': Object.freeze(['key']),
  })

// ---------------------------------------------------------------------------
// The requests.
// ---------------------------------------------------------------------------

/** NoArgumentRequest is an operation that takes no argument at all: the row's
 * own argv is the whole of it. `{}` is the only value of this type. */
export type NoArgumentRequest = Readonly<Record<string, never>>

/**
 * RunsStreamRequest is `runs ls --stream --json --since W`: the window, and
 * nothing else. The row's other columns (`--repo`, `--mine`, `--pr`, `--all`,
 * `--limit`, `--after`) are not the renderer's to choose, so the runs stream is
 * the window's own list and never a filtered one. The window is `runs ls`'s
 * grammars (`7d`, `36h`, `90m`, `1h30m`, `0`), never a date.
 */
export interface RunsStreamRequest {
  readonly since: string
}

/** SpendStreamRequest is `report --stream` with the flags `spend.report` already
 * takes: the breakdown, the range and the one repository. */
export interface SpendStreamRequest {
  readonly by?: string
  readonly since?: string
  readonly until?: string
  readonly repo?: string
}

/** BudgetStreamRequest is `budget show --stream` with the flags `budget.show`
 * already takes. `compute` costs a listing of the runs bucket, which is why it
 * is a flag at all and why its scope is the caps read alone. */
export interface BudgetStreamRequest {
  readonly repo?: string
  readonly all?: boolean
  readonly compute?: boolean
}

/** SetupPlanRequest is `setup --plan-only --answers-file - --events --json`:
 * the answers, validated in main, shown in the confirmation window, and handed
 * to the child as bytes on stdin. There is no path in this payload and there is
 * no flag beside it. */
export interface SetupPlanRequest {
  readonly answers: SetupAnswers
}

/** SettingsEditorOpenRequest is `settings.editor.open`: one key of the document
 * the settings read answered (never a key the renderer invented), validated with
 * `SETTINGS_KEY_RE`. The layer and the phase are main's -- the key alone does
 * not name where or when a write lands. */
export interface SettingsEditorOpenRequest {
  readonly key: string
}

/** NewOperationRequest is each operation's payload type by name. */
export type NewOperationRequest = {
  readonly 'runs.stream': RunsStreamRequest
  readonly 'spend.stream': SpendStreamRequest
  readonly 'budget.stream': BudgetStreamRequest
  readonly whoami: NoArgumentRequest
  readonly 'image.catalog.ls': NoArgumentRequest
  readonly 'recipes.ls': NoArgumentRequest
  readonly 'setup.plan': SetupPlanRequest
  readonly 'setup.apply': NoArgumentRequest
  readonly 'settings.editor.open': SettingsEditorOpenRequest
}

// ---------------------------------------------------------------------------
// The setup answers.
// ---------------------------------------------------------------------------

/** SETUP_ANSWER_KEYS is the six questions `fugaro setup` asks
 * (`internal/cli/setup.go`, `setupQuestions`; `budget` is added by
 * `setupAnswerQuestions` for D2210), in the order it asks them. These are what
 * the wizard collects and what the renderer sends; **`version` is not one of
 * them** -- it is the answers file's own schema version, which main adds when it
 * serialises the file -- and neither is `budget_currency`, which the CLI
 * defaults to USD (D2211) and this console sends nothing for. */
export const SETUP_ANSWER_KEYS: readonly string[] = Object.freeze([
  'gcp_project',
  'region',
  'name',
  'rigor',
  'budget',
  'bake_repos',
])

/** SETUP_ANSWERS_FILE_KEYS is every key of the file the CLI decodes
 * (`schemas/setup-answers.json`, ST1): the six answers, and the schema's
 * `version` in front of them. Named here so the difference between what the
 * renderer sends and what the child reads is one place and not two. */
export const SETUP_ANSWERS_FILE_KEYS: readonly string[] = Object.freeze([
  'version',
  ...SETUP_ANSWER_KEYS,
])

/** SETUP_BUDGET_MIN and SETUP_BUDGET_MAX are the bounds of the `budget` answer,
 * and they are the CLI's own rule rather than this console's: the amount is the
 * installation's monthly budget **alert** threshold, and `managed.Budget.Check`
 * (`internal/infra/managed/marker.go`) refuses a marker outside
 * `MinBudgetAmount` to `MaxBudgetAmount` -- one to ten million whole units of
 * the currency -- so a document answering outside them would be refused by the
 * installation later rather than by whoever typed it. The answers file's schema
 * states the same two numbers (`schemas/setup-answers.schema.json`), which is
 * where `new-ops.test.ts` reads them. */
export const SETUP_BUDGET_MIN = 1
export const SETUP_BUDGET_MAX = 10_000_000

/** SetupRigor is the rigor mode as the wire spells it: the five modes of
 * `docs/rigor-modes`. It has no default and the setup file requires it, which is
 * why the wizard marks it as the one question a person cannot leave empty. */
export type SetupRigor = 'poc' | 'startup' | 'pro' | 'critical' | 'hotfix'

/** SETUP_RIGOR_MODES is that set as a value, held equal to the console's own
 * vocabulary for the same five (`src/shared/rigor.ts`, `RIGOR_MODE_WORDS`) by
 * `new-ops.test.ts`: a mode the wizard offered and the screens could not draw
 * would be an answer nobody could read back. */
export const SETUP_RIGOR_MODES: readonly SetupRigor[] = Object.freeze([
  'poc',
  'startup',
  'pro',
  'critical',
  'hotfix',
])

/** SetupAnswers is the answers object, and the whole of it. */
export interface SetupAnswers {
  readonly gcp_project: string
  readonly region: string
  readonly name: string
  readonly rigor: SetupRigor
  /** `budget` is the monthly budget alert the CLI's own question suggests
   * (D2210) and is **absent** for a field left empty: the answers file's member
   * is optional, so sending nothing is how the operator takes the CLI's default
   * of 100 USD (D2212) rather than how they ask for no budget at all. */
  readonly budget?: number
  readonly bake_repos: boolean
}

// ---------------------------------------------------------------------------
// The answers.
// ---------------------------------------------------------------------------

/** NewOpsDocument is a document of the CLI's this console has not opened: a
 * record it draws through a reader of its own (`src/shared/schema.ts`'s, or a
 * screen's). The two streams whose rows have no reader yet carry rows of this
 * type, so a screen reads the members it knows and nothing here pretends to have
 * checked the rest. */
export type NewOpsDocument = Readonly<Record<string, unknown>>

/**
 * StreamEnd is a feed's `end` line: the CLI saying it finished. What the line
 * **is** is that statement; its three figures are annotations of it, and each is
 * `null` where the line does not carry it -- `version` is the number's own
 * document version (RS1's runs list, RS3's report), `count` is how many `row`
 * lines the stream carried, and `totals` is the report's totals block.
 *
 * The annotations are tolerant on purpose, and the reason is the line's own
 * meaning: the caps document `budget show --json` writes carries no `version` at
 * all, so a reader that refused an `end` line without one would turn a stream
 * that finished into a stream this console called interrupted -- the exact
 * misreading the line exists to prevent.
 *
 * A feed that carries an `error` line is failed whatever else it carries, `end`
 * included: `feed.end` is `null` for it. `streamAt` says why.
 */
export interface StreamEnd {
  readonly version: number | null
  readonly count: number | null
  readonly totals: NewOpsDocument | null
}

/**
 * StreamFeed is a feed as a screen reads it: the rows in arrival order, the
 * `end` line (`null` -- interrupted), and the CLI's own sentence when an `error`
 * line ended the stream (`null` when none did). `end` and `failure` are two
 * facts and not one: a stream can stop with no word from the CLI, which is an
 * interrupted read, and it can stop with a sentence, which is a refusal the
 * screen draws.
 */
export interface StreamFeed<Row> {
  readonly rows: readonly Row[]
  readonly end: StreamEnd | null
  readonly failure: string | null
}

/** RunsStreamFeed is `runs.stream`: rows of the same shape `runs ls --json`'s
 * array holds (`runview.Row`, read by `schema.ts`'s `decodeRunsList`, so the
 * stream and the whole-document read cannot draw two different lists). */
export type RunsStreamFeed = StreamFeed<RunsListRow>

/** SpendStreamFeed is `spend.stream`: the report's own rows, unopened here --
 * the Spend screen's reader is the one that knows them. */
export type SpendStreamFeed = StreamFeed<NewOpsDocument>

/** BudgetStreamFeed is `budget.stream`: the caps document's own rows, unopened
 * here for the reason the report's are not. */
export type BudgetStreamFeed = StreamFeed<NewOpsDocument>

/** WhoAmIRole is the caller's own role as the console understands it (WH1,
 * D2907). `none` is both a role the CLI writes and the reading of a role this
 * build does not know. */
export type WhoAmIRole = 'viewer' | 'operator' | 'admin' | 'none'

/** WHOAMI_ROLES is that set as a value. The order is the CLI's own (the least
 * privileged first), so a reader that needed a rank would not have to invent
 * one -- nothing here ranks them. */
export const WHOAMI_ROLES: readonly WhoAmIRole[] = Object.freeze([
  'viewer',
  'operator',
  'admin',
  'none',
])

/** WhoAmIScope is what the caller's role reaches: one project's settings, every
 * project of the installation, or a named set of repositories. */
export type WhoAmIScope = 'project' | 'all' | 'repos'

/** WHOAMI_SCOPES is that set as a value. */
export const WHOAMI_SCOPES: readonly WhoAmIScope[] = Object.freeze(['project', 'all', 'repos'])

/**
 * WhoAmIView is `whoami` (`user ls --me --json`) as this console reads it: the
 * caller's role, what it reaches, and the repositories a `repos` scope names.
 *
 * `scope` is `null` for a scope word this build does not know, which is not the
 * same fact as `none` for a role: the role decides whether a control is drawn at
 * all, and a build that has not learned a new scope still knows what the caller
 * may not do. Nothing in the console enables an action from `scope`; it is read
 * so a tooltip can name where the role reaches (RB4).
 */
export interface WhoAmIView {
  readonly version: number
  readonly role: WhoAmIRole
  readonly scope: WhoAmIScope | null
  readonly repos: readonly string[]
}

/** ImageCatalogEntryView is one entry of `image ls --json`: the name a
 * repository's `needs:` matches, the tags it may name it by, whether it is what
 * a job naming nothing runs, what it is built from, and the digest the last
 * build recorded (empty when nothing built it). */
export interface ImageCatalogEntryView {
  readonly name: string
  readonly tags: readonly string[]
  readonly isDefault: boolean
  readonly source: string
  readonly description: string
  readonly digest: string
}

/** ImageCatalogView is `image ls --json`: the installation's project and its
 * catalog, in the CLI's own order. */
export interface ImageCatalogView {
  readonly project: string
  readonly entries: readonly ImageCatalogEntryView[]
}

/** RecipeStepView is one step of a recipe: its kind, the command it runs when it
 * is a check, and the three knobs a review or adversarial step carries. */
export interface RecipeStepView {
  readonly kind: string
  readonly command: string
  readonly maxRounds: number | null
  readonly bounce: boolean
  readonly autofix: boolean
}

/**
 * RecipeView is one recipe of `recipes ls --json`, whichever layer it came from.
 * `launchable` is the CLI's own word for a recipe a routing skill may pick: it
 * is false for a recipe that failed to parse, and a console that drew the row as
 * launchable anyway would offer a launch the run then refuses. `shadowedBy`
 * names the layer that wins the name, empty for the winner and for every recipe
 * no layer shadows.
 */
export interface RecipeView {
  readonly name: string
  readonly source: string
  readonly description: string
  readonly useWhen: string
  readonly steps: readonly RecipeStepView[]
  readonly launchable: boolean
  readonly shadowedBy: string
  readonly problem: string
}

/** RecipesView is `recipes ls --json`: the rows, and the notes the CLI put on
 * stderr for a recipe it skipped or a layer that has no `use_when`. */
export interface RecipesView {
  readonly recipes: readonly RecipeView[]
  readonly notes: readonly string[]
}

/** SetupEventType is the closed set of setup event lines (`setup --events`,
 * ST2): every kind a line of the feed may carry. All but `heartbeat` are events
 * a view is handed (`SetupEvent`); a `heartbeat` is the feed's own liveness --
 * one a minute for a step still running -- which the reader accepts and drops
 * (D3436, D3453). */
export type SetupEventType =
  | 'stage'
  | 'step'
  | 'left_for_you'
  | 'notice'
  | 'done'
  | 'error'
  | 'heartbeat'

/** SETUP_EVENT_TYPES is that set as a value, in the plan's own order, with the
 * liveness line last. */
export const SETUP_EVENT_TYPES: readonly SetupEventType[] = Object.freeze([
  'stage',
  'step',
  'left_for_you',
  'notice',
  'done',
  'error',
  'heartbeat',
])

/** SetupRepoLine is one repository a stage has something to say about: how the
 * stage left it (`done`, `needs you`, `unread` or `skipped`, the CLI's own four
 * words) and one line of detail, absent when the state says it all. Both the
 * name and the detail are a repository's own text -- a path, a branch name, a
 * provider's sentence -- so a view draws them through the text class it draws
 * untrusted text with. */
export interface SetupRepoLine {
  readonly repo: string
  readonly state: string
  readonly detail: string
}

/**
 * SetupStageEvent is a stage's line: the stage's name, the state the converge
 * settled it with, its own one-line detail, the stage it waited for, whether it
 * skipped without doing its work (the CLI's `undone`, which tells a skipped
 * stage from one that found nothing to do), and the lines it reported about the
 * repositories it read (U13; `[]` for a stage that reports none). A stage that
 * reports lines says their summary as its detail -- "3 repositories: 1 done,
 * 1 needs you, 1 unread" -- which is the same sentence from every directory.
 */
export interface SetupStageEvent {
  readonly type: 'stage'
  readonly name: string
  readonly state: string
  readonly detail: string
  readonly after: string
  readonly undone: boolean
  readonly repos: readonly SetupRepoLine[]
}

/**
 * SetupStepEvent is a step's line inside a stage: the progress a person watching
 * the run would see. It is the one event whose members this contract fixes
 * without a CLI task having written one yet -- ST2's schema is the authority --
 * so a member it does not write reads as the empty string rather than refusing
 * the feed, and the line's stage is what it must carry.
 */
export interface SetupStepEvent {
  readonly type: 'step'
  readonly stage: string
  readonly name: string
  readonly detail: string
}

/** SetupLeftForYouEvent is a step only a person can do: the CLI names the stage,
 * what kind of thing is owed, the one line to read, and the commands to run in
 * the owner's terminal. The wizard lists every one of these and offers a Copy
 * for a command whose first word is one it may copy (SW3b). */
export interface SetupLeftForYouEvent {
  readonly type: 'left_for_you'
  readonly stage: string
  readonly kind: string
  readonly text: string
  readonly commands: readonly string[]
}

/**
 * SetupNoticeEvent is one line the run reported outside its stages: a step after
 * the stage loop, or an admit pattern the reader could not expand into
 * repository names (R12, R19). `step` is the step's own name -- a step after the
 * loop has no stage to report under -- and `repo` and `state` are the empty
 * string for a notice about no repository at all.
 */
export interface SetupNoticeEvent {
  readonly type: 'notice'
  readonly step: string
  readonly repo: string
  readonly state: string
  readonly detail: string
}

/** SetupDoneEvent is the line that says the run finished: it is what makes a
 * setup feed complete rather than interrupted. */
export interface SetupDoneEvent {
  readonly type: 'done'
}

/** SetupErrorEvent is the stage the run stopped at, with the CLI's own sentence
 * and the fix it printed. */
export interface SetupErrorEvent {
  readonly type: 'error'
  readonly stage: string
  readonly error: string
  readonly fix: string
}

/** SetupEvent is one line of the setup feed. */
export type SetupEvent =
  | SetupStageEvent
  | SetupStepEvent
  | SetupLeftForYouEvent
  | SetupNoticeEvent
  | SetupDoneEvent
  | SetupErrorEvent

/** SetupFailure is the failed stage, as the feed answers it: `error` is the
 * CLI's sentence and `fix` the line it tells the owner to run. */
export interface SetupFailure {
  readonly stage: string
  readonly error: string
  readonly fix: string
}

/**
 * SetupFeed is `setup --events` as the wizard reads it: every event in arrival
 * order, whether the run reached its `done` line, and the failure it stopped at
 * (`null` when it did not fail). `done` false and `failure` null is an
 * interrupted read -- a cap, a killed child, a lost channel -- and the wizard
 * draws it as one, with Retry, rather than as a plan that finished early.
 */
export interface SetupFeed {
  readonly events: readonly SetupEvent[]
  readonly done: boolean
  readonly failure: SetupFailure | null
}

/** SettingsEditorOutcome is `settings.editor.open`'s answer, the shape
 * `settings.edit` already answers (`src/main/settingsedit.ts`): whether a write
 * happened, and the sentence the row draws either way. */
export interface SettingsEditorOutcome {
  readonly ok: boolean
  readonly sentence: string
}

/** NewOperationAnswers is each operation's answer type by name. */
export interface NewOperationAnswers {
  readonly 'runs.stream': RunsStreamFeed
  readonly 'spend.stream': SpendStreamFeed
  readonly 'budget.stream': BudgetStreamFeed
  readonly whoami: WhoAmIView
  readonly 'image.catalog.ls': ImageCatalogView
  readonly 'recipes.ls': RecipesView
  readonly 'setup.plan': SetupFeed
  readonly 'setup.apply': SetupFeed
  readonly 'settings.editor.open': SettingsEditorOutcome
}

/** NewOperationAnswer is one operation's answer type. */
export type NewOperationAnswer<Name extends NewOperationName> = NewOperationAnswers[Name]

/**
 * NEW_OPERATION_TEXT_CLASSES is the class every text field these answers carry
 * is drawn through, keyed by the field's own wire name (`sanitize.ts`'s
 * `TEXT_CLASSES`, the console's closed set). The classes are chosen here rather
 * than by each consumer for `WIRE_TEXT_CLASSES`'s reason: a value that reaches a
 * screen through a class nobody chose is a value the display layer guesses
 * about, and this is the last place both the field and its reader are visible at
 * once. SRC's readers put each of these fields through its class before the
 * answer is answered, and a screen re-sanitizes at the display layer as it does
 * for every other document.
 */
export const NEW_OPERATION_TEXT_CLASSES: Readonly<Record<string, TextClass>> = Object.freeze({
  // A row of `runs ls --json`, read by the schema's own reader.
  run: 'runId',
  repo: 'repository',
  status: 'label',
  title: 'sentence',
  plan_task: 'label',
  // The feeds' own members.
  failure: 'sentence',
  // `whoami`.
  scope: 'label',
  // `image ls --json`.
  project: 'project',
  name: 'label',
  tags: 'label',
  source: 'label',
  description: 'sentence',
  digest: 'label',
  // `recipes ls --json`.
  use_when: 'sentence',
  kind: 'label',
  command: 'label',
  shadowed_by: 'label',
  problem: 'sentence',
  notes: 'sentence',
  // `setup --events`.
  stage: 'label',
  state: 'label',
  detail: 'sentence',
  after: 'label',
  text: 'sentence',
  commands: 'sentence',
  error: 'sentence',
  fix: 'sentence',
})

/** NEW_OPS_NOT_AVAILABLE is what a read that no handler answers says: a method
 * the bridge does not have, or one whose channel threw. It is AL's own sentence
 * for a stub handler, so a console whose operation is not wired yet reads the
 * same way wherever it is drawn. */
export const NEW_OPS_NOT_AVAILABLE = 'not available in this build'

/** NEW_OPS_UNREADABLE is what an answer nobody can read says: a reply that is
 * not the console's envelope, or a document the decoders refused. It is a
 * refusal and never an empty document, for `replyData`'s reason -- a screen
 * handed `{}` would draw zeros nobody measured. */
export const NEW_OPS_UNREADABLE = 'the console could not read this answer'

/** NEW_OPS_STREAM_FAILED is the sentence a feed carries when its `error` line
 * named no reason this console could find: the read failed, and saying so is
 * better than drawing a list that stopped without one. */
export const NEW_OPS_STREAM_FAILED = 'the read failed and the CLI wrote no sentence for it'

/** NewOpsGapName is why a read answered nothing: no handler behind it, a refusal
 * the channel answered with, or an answer this build could not read. */
export type NewOpsGapName = 'not-available' | 'refused' | 'unreadable'

/** NewOpsGap is one of those, with the sentence a screen draws. */
export interface NewOpsGap {
  readonly gap: NewOpsGapName
  readonly sentence: string
}

/** NewOpsReply is what one of the nine answers: a value, or the reason there is
 * none. There is deliberately no third state -- a read that failed is a gap and
 * not an empty value. */
export type NewOpsReply<T> =
  | { readonly ok: true; readonly value: T }
  | { readonly ok: false; readonly gap: NewOpsGap }

/**
 * NewOps is the renderer's typed read of the nine operations, as
 * `src/renderer/entry.ts`'s `ReadBridge` is for the reads that came before it.
 * `newOpsFrom` (`src/renderer/new-bridge.ts`) builds one over the structural
 * bridge, so no consumer has to edit `entry.ts` to reach a new operation.
 *
 * Its methods are named exactly as `NEW_OPERATION_METHODS` names the bridge
 * member they call -- one spelling for one operation, so a reader that knows
 * which operation it wants knows both names. The spelling is `ipc.ts`'s own
 * (`listRuns`, `showBudget`, `runReport`, `editSetting`, `summaryRuns`): the
 * verb first, then what it reads.
 */
export interface NewOps {
  streamRuns(request: RunsStreamRequest): Promise<NewOpsReply<RunsStreamFeed>>
  streamSpend(request: SpendStreamRequest): Promise<NewOpsReply<SpendStreamFeed>>
  streamBudget(request: BudgetStreamRequest): Promise<NewOpsReply<BudgetStreamFeed>>
  whoAmI(request: NoArgumentRequest): Promise<NewOpsReply<WhoAmIView>>
  listImageCatalog(request: NoArgumentRequest): Promise<NewOpsReply<ImageCatalogView>>
  listRecipes(request: NoArgumentRequest): Promise<NewOpsReply<RecipesView>>
  planSetup(request: SetupPlanRequest): Promise<NewOpsReply<SetupFeed>>
  applySetup(request: NoArgumentRequest): Promise<NewOpsReply<SetupFeed>>
  openSettingsEditor(request: SettingsEditorOpenRequest): Promise<NewOpsReply<SettingsEditorOutcome>>
}

// ---------------------------------------------------------------------------
// The decode.
//
// The readers below are this module's own, and they are here rather than in
// `schema.ts` for one reason: every reader of that module is module-private, and
// these are new documents rather than the feed's. They follow its rules exactly
// -- a missing key and a wrong-typed key are refused **by name**, an unknown key
// is ignored, a `null` at an optional key is the CLI saying it has nothing --
// and they answer the same `Decoded`/`Skew` pair, so a screen draws one skew
// sentence whether the document it could not read was the feed's or this one's.
// ---------------------------------------------------------------------------

class Refusal extends Error {
  constructor(readonly refusal: SchemaRefusal) {
    super(`${refusal.where}: ${refusal.field} is ${refusal.name}`)
  }
}

function refuse(name: SchemaRefusalName, field: string, where: string): never {
  throw new Refusal({ name, field, where })
}

function skewFor(refusal: SchemaRefusal): Skew {
  return {
    name: 'skew',
    because: `${refusal.where} is not one the console reads: ${refusal.field} is ${refusal.name}`,
    refusal,
  }
}

function at<T>(where: string, decode: () => T): Decoded<T> {
  try {
    return { ok: true, value: decode() }
  } catch (error) {
    if (error instanceof Refusal) return { ok: false, skew: skewFor(error.refusal) }
    throw error
  }
}

/** objectAt is a value that must be a JSON object. */
function objectAt(raw: unknown, field: string, where: string): Record<string, unknown> {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) {
    return refuse('not-an-object', field, where)
  }
  return raw as Record<string, unknown>
}

/** memberAt is a key the shape requires: absent, and the document is refused by
 * the field's own name. */
function memberAt(raw: Record<string, unknown>, key: string, field: string, where: string): unknown {
  if (!(key in raw)) return refuse('missing', field === '' ? key : `${field}.${key}`, where)
  return raw[key]
}

function stringAt(raw: unknown, field: string, where: string): string {
  if (typeof raw !== 'string') return refuse('not-a-string', field, where)
  return raw
}

/** optionalStringAt is a string key the shape may leave out, or write `null` at:
 * both read as the empty string, the CLI's own `omitempty`. */
function optionalStringAt(
  raw: Record<string, unknown>,
  key: string,
  field: string,
  where: string,
): string {
  const value = raw[key]
  if (value === undefined || value === null) return ''
  return stringAt(value, field === '' ? key : `${field}.${key}`, where)
}

function boolAt(raw: unknown, field: string, where: string): boolean {
  if (typeof raw !== 'boolean') return refuse('not-a-boolean', field, where)
  return raw
}

function optionalBoolAt(
  raw: Record<string, unknown>,
  key: string,
  field: string,
  where: string,
): boolean {
  const value = raw[key]
  if (value === undefined || value === null) return false
  return boolAt(value, field === '' ? key : `${field}.${key}`, where)
}

function intAt(raw: unknown, field: string, where: string): number {
  if (typeof raw !== 'number' || !Number.isInteger(raw)) return refuse('not-a-number', field, where)
  return raw
}

/** optionalIntAt is a whole-number key the shape may leave out (`count`, a step's
 * `max_rounds`): absent and `null` are both "the CLI did not say", which is not
 * the same fact as zero. A value that is there and is not a whole number is
 * refused by name, as `intAt` refuses one. */
function optionalIntAt(
  raw: Record<string, unknown>,
  key: string,
  field: string,
  where: string,
): number | null {
  const value = raw[key]
  if (value === undefined || value === null) return null
  return intAt(value, field === '' ? key : `${field}.${key}`, where)
}

function arrayAt(raw: unknown, field: string, where: string): readonly unknown[] {
  if (!Array.isArray(raw)) return refuse('not-an-array', field, where)
  return raw
}

/** optionalArrayAt is an array key the shape may leave out or write `null` at
 * (`tags`, `steps`): the CLI's `omitempty`, and an empty list is what it means. */
function optionalArrayAt(
  raw: Record<string, unknown>,
  key: string,
  field: string,
  where: string,
): readonly unknown[] {
  const value = raw[key]
  if (value === undefined || value === null) return []
  return arrayAt(value, field === '' ? key : `${field}.${key}`, where)
}

/** stringsAt reads an array of strings -- a repository list, a tag list, a set of
 * commands -- refusing the element that is not one, by its index. */
function stringsAt(raw: unknown, field: string, where: string): readonly string[] {
  return arrayAt(raw, field, where).map((item, index) =>
    stringAt(item, `${field}[${String(index)}]`, where),
  )
}

/** optionalDocumentAt is a block key the shape may leave out or write `null` at
 * (`totals`): the block, or `null` for "this document has none of it". */
function optionalDocumentAt(
  raw: Record<string, unknown>,
  key: string,
  field: string,
  where: string,
): NewOpsDocument | null {
  const value = raw[key]
  if (value === undefined || value === null) return null
  return objectAt(value, field === '' ? key : `${field}.${key}`, where)
}

/** atLine is the field spelling of one parsed line. */
function lineField(index: number): string {
  return `lines[${String(index)}]`
}

/** linesOf is the parsed lines of a feed's document, in the order the CLI wrote
 * them: the document is `{lines: [...]}` because NDJSON has no document of its
 * own (see this module's header). */
function linesOf(document: Record<string, unknown>, where: string): readonly Record<string, unknown>[] {
  return arrayAt(memberAt(document, 'lines', '', where), 'lines', where).map((line, index) =>
    objectAt(line, lineField(index), where),
  )
}

/** typeOf is a line's `type`, the discriminator every NDJSON line of this
 * console's set carries. */
function typeOf(line: Record<string, unknown>, field: string, where: string): string {
  return stringAt(memberAt(line, 'type', field, where), `${field}.type`, where)
}

/** sentenceOf is a failure line's sentence, whatever the line calls it: RS1's
 * `error` line is the CLI's own words for the failure, and a stream whose
 * producer names the member something else says the same thing. A line that
 * carries none of the three is a failure with no readable reason, and the
 * console's own sentence is drawn in its place rather than an empty one. */
function sentenceOf(line: Record<string, unknown>, field: string, where: string): string {
  for (const key of ['error', 'message', 'sentence']) {
    const value = line[key]
    if (typeof value === 'string' && value !== '') return value
    if (value !== undefined && value !== null && typeof value !== 'string') {
      refuse('not-a-string', `${field}.${key}`, where)
    }
  }
  return NEW_OPS_STREAM_FAILED
}

/** streamEndAt is a feed's `end` line: the figures it carries, each read where
 * it is there and left `null` where it is not (the block above says why a figure
 * the line omits is not a refusal). A figure that **is** there and is not its
 * kind is still refused by name: an `end` line whose `count` is a string is a
 * line no CLI wrote. */
function streamEndAt(line: Record<string, unknown>, field: string, where: string): StreamEnd {
  return {
    version: optionalIntAt(line, 'version', field, where),
    count: optionalIntAt(line, 'count', field, where),
    totals: optionalDocumentAt(line, 'totals', field, where),
  }
}

/** rowOf is the row one `row` line carries. A feed has no document of its own,
 * so the CLI writes an element of a document's own array **under the feed's own
 * key** rather than as the line itself (`streamRowLine`,
 * `internal/cli/runs_stream.go`: `{"type":"row","row":{...}}`, the shape
 * `internal/cli/testdata/runs_stream.golden.ndjson` pins). The line is therefore
 * a wrapper, and what a screen draws is the member inside it: a reader that
 * pushed the line would hand `decodeRunsList` a "row" whose members are `type`
 * and `row`, which is a document no CLI wrote and one that reader refuses.
 *
 * A `row` line whose `row` is missing, or is not an object, is refused by name
 * for the CLI's own reason (it refuses `{"type":"row"}` and `{"type":"row","row":1}`
 * alike): a line that carries no row is not a row with nothing in it, it is a
 * line this build cannot read. */
function rowOf(line: Record<string, unknown>, field: string, where: string): Record<string, unknown> {
  return objectAt(memberAt(line, 'row', field, where), `${field}.row`, where)
}

/** streamAt reads a feed's lines into rows, its `end` line and its failure. The
 * last `end` line wins, which is what a stream that wrote two means; an `end`
 * line no `row` followed is an empty feed and not a refusal.
 *
 * **A failure wins over an `end`.** A stream that wrote both is one no CLI
 * writes (the writer ends with one terminator, and a failure exits non-zero),
 * and the console has to pick a reading for it: the `error` line says the read
 * did not finish, so the `end` before it is dropped and the feed is drawn as
 * failed. The other way round -- a completed read with a failure beside it --
 * would show a list the CLI did not stand behind. */
function streamAt(
  document: Record<string, unknown>,
  where: string,
): { readonly rows: readonly Record<string, unknown>[]; readonly end: StreamEnd | null; readonly failure: string | null } {
  const rows: Record<string, unknown>[] = []
  let end: StreamEnd | null = null
  let failure: string | null = null
  for (const [index, line] of linesOf(document, where).entries()) {
    const field = lineField(index)
    const type = typeOf(line, field, where)
    if (type === 'row') rows.push(rowOf(line, field, where))
    else if (type === 'end') end = streamEndAt(line, field, where)
    else if (type === 'error') failure = sentenceOf(line, field, where)
    else refuse('unknown-value', `${field}.type`, where)
  }
  return { rows, end: failure === null ? end : null, failure }
}

/** runsFeedAt is `runs.stream`'s document: the same rows `runs ls --json`'s
 * array holds, read by the same reader. */
function runsFeedAt(document: Record<string, unknown>, where: string): RunsStreamFeed {
  const stream = streamAt(document, where)
  const rows = decodeRunsList({ runs: [...stream.rows] }, where)
  if (!rows.ok) throw new Refusal(rows.skew.refusal)
  return { rows: rows.value.runs, end: stream.end, failure: stream.failure }
}

/** rowsFeedAt is a feed whose rows this console has no reader for: every row
 * line must be a JSON object, and what its members are is the screen's own
 * reader's business (the Spend and Project tabs'). */
function rowsFeedAt(
  document: Record<string, unknown>,
  where: string,
): { readonly rows: readonly NewOpsDocument[]; readonly end: StreamEnd | null; readonly failure: string | null } {
  const stream = streamAt(document, where)
  return { rows: stream.rows, end: stream.end, failure: stream.failure }
}

/** whoAmIAt is `user ls --me --json`. An unknown role is `none` (this module's
 * header says why) and an unknown scope is `null`; everything else is refused by
 * name, because a document that cannot say who is asking is a document no
 * control may be enabled from. */
function whoAmIAt(document: Record<string, unknown>, where: string): WhoAmIView {
  const rawRole = stringAt(memberAt(document, 'role', '', where), 'role', where)
  const rawScope = optionalStringAt(document, 'scope', '', where)
  return {
    version: intAt(memberAt(document, 'version', '', where), 'version', where),
    role: (WHOAMI_ROLES as readonly string[]).includes(rawRole) ? (rawRole as WhoAmIRole) : 'none',
    scope: rawScope !== '' && (WHOAMI_SCOPES as readonly string[]).includes(rawScope)
      ? (rawScope as WhoAmIScope)
      : null,
    repos: stringsAt(memberAt(document, 'repos', '', where), 'repos', where),
  }
}

function imageEntryAt(raw: unknown, field: string, where: string): ImageCatalogEntryView {
  const entry = objectAt(raw, field, where)
  return {
    name: stringAt(memberAt(entry, 'name', field, where), `${field}.name`, where),
    tags: stringsAt(optionalArrayAt(entry, 'tags', field, where), `${field}.tags`, where),
    isDefault: optionalBoolAt(entry, 'default', field, where),
    source: stringAt(memberAt(entry, 'source', field, where), `${field}.source`, where),
    description: optionalStringAt(entry, 'description', field, where),
    digest: optionalStringAt(entry, 'digest', field, where),
  }
}

function imageCatalogAt(document: Record<string, unknown>, where: string): ImageCatalogView {
  return {
    project: stringAt(memberAt(document, 'project', '', where), 'project', where),
    entries: arrayAt(memberAt(document, 'entries', '', where), 'entries', where).map((entry, index) =>
      imageEntryAt(entry, `entries[${String(index)}]`, where),
    ),
  }
}

function recipeStepAt(raw: unknown, field: string, where: string): RecipeStepView {
  const step = objectAt(raw, field, where)
  return {
    kind: stringAt(memberAt(step, 'kind', field, where), `${field}.kind`, where),
    command: optionalStringAt(step, 'command', field, where),
    maxRounds: optionalIntAt(step, 'max_rounds', field, where),
    bounce: optionalBoolAt(step, 'bounce', field, where),
    autofix: optionalBoolAt(step, 'autofix', field, where),
  }
}

function recipeAt(raw: unknown, field: string, where: string): RecipeView {
  const recipe = objectAt(raw, field, where)
  return {
    name: stringAt(memberAt(recipe, 'name', field, where), `${field}.name`, where),
    source: stringAt(memberAt(recipe, 'source', field, where), `${field}.source`, where),
    description: optionalStringAt(recipe, 'description', field, where),
    useWhen: optionalStringAt(recipe, 'use_when', field, where),
    steps: optionalArrayAt(recipe, 'steps', field, where).map((step, index) =>
      recipeStepAt(step, `${field}.steps[${String(index)}]`, where),
    ),
    // The CLI writes `launchable` on every row, and a row that lost it is read
    // as unlaunchable: a routing skill must not pick a recipe this console could
    // not read, and false is the refusal's own answer.
    launchable: optionalBoolAt(recipe, 'launchable', field, where),
    shadowedBy: optionalStringAt(recipe, 'shadowed_by', field, where),
    problem: optionalStringAt(recipe, 'problem', field, where),
  }
}

function recipesAt(document: Record<string, unknown>, where: string): RecipesView {
  return {
    recipes: arrayAt(memberAt(document, 'recipes', '', where), 'recipes', where).map(
      (recipe, index) => recipeAt(recipe, `recipes[${String(index)}]`, where),
    ),
    notes: stringsAt(optionalArrayAt(document, 'notes', '', where), 'notes', where),
  }
}

function stageEventAt(line: Record<string, unknown>, field: string, where: string): SetupStageEvent {
  return {
    type: 'stage',
    name: stringAt(memberAt(line, 'name', field, where), `${field}.name`, where),
    state: stringAt(memberAt(line, 'state', field, where), `${field}.state`, where),
    detail: optionalStringAt(line, 'detail', field, where),
    after: optionalStringAt(line, 'after', field, where),
    undone: optionalBoolAt(line, 'undone', field, where),
    repos: optionalArrayAt(line, 'repos', field, where).map((raw, index) =>
      repoLineAt(raw, `${field}.repos[${String(index)}]`, where),
    ),
  }
}

/** repoLineAt is one repository a stage reported on: the name and the state are
 * the line's own, and the detail is absent when the state says it all. */
function repoLineAt(raw: unknown, field: string, where: string): SetupRepoLine {
  const line = objectAt(raw, field, where)
  return {
    repo: stringAt(memberAt(line, 'repo', field, where), `${field}.repo`, where),
    state: stringAt(memberAt(line, 'state', field, where), `${field}.state`, where),
    detail: optionalStringAt(line, 'detail', field, where),
  }
}

function stepEventAt(line: Record<string, unknown>, field: string, where: string): SetupStepEvent {
  return {
    type: 'step',
    stage: stringAt(memberAt(line, 'stage', field, where), `${field}.stage`, where),
    name: optionalStringAt(line, 'name', field, where),
    detail: optionalStringAt(line, 'detail', field, where),
  }
}

/** noticeEventAt is one line the run reported outside its stages: the step's own
 * name, and the repository or admit pattern it is about -- both empty for a
 * notice about no repository. */
function noticeEventAt(line: Record<string, unknown>, field: string, where: string): SetupNoticeEvent {
  return {
    type: 'notice',
    step: stringAt(memberAt(line, 'step', field, where), `${field}.step`, where),
    repo: optionalStringAt(line, 'repo', field, where),
    state: optionalStringAt(line, 'state', field, where),
    detail: optionalStringAt(line, 'detail', field, where),
  }
}

function leftEventAt(
  line: Record<string, unknown>,
  field: string,
  where: string,
): SetupLeftForYouEvent {
  return {
    type: 'left_for_you',
    stage: stringAt(memberAt(line, 'stage', field, where), `${field}.stage`, where),
    kind: optionalStringAt(line, 'kind', field, where),
    text: stringAt(memberAt(line, 'text', field, where), `${field}.text`, where),
    commands: stringsAt(optionalArrayAt(line, 'commands', field, where), `${field}.commands`, where),
  }
}

function failureAt(line: Record<string, unknown>, field: string, where: string): SetupFailure {
  return {
    stage: stringAt(memberAt(line, 'stage', field, where), `${field}.stage`, where),
    error: stringAt(memberAt(line, 'error', field, where), `${field}.error`, where),
    fix: optionalStringAt(line, 'fix', field, where),
  }
}

/** setupFeedAt is `setup --events`: the events in arrival order, whether the run
 * reached its `done` line, and the failure it stopped at. A feed whose last line
 * is neither is interrupted, which is `done: false` and no failure. */
function setupFeedAt(document: Record<string, unknown>, where: string): SetupFeed {
  const events: SetupEvent[] = []
  let done = false
  let failure: SetupFailure | null = null
  for (const [index, line] of linesOf(document, where).entries()) {
    const field = lineField(index)
    const type = typeOf(line, field, where)
    if (type === 'stage') events.push(stageEventAt(line, field, where))
    else if (type === 'step') events.push(stepEventAt(line, field, where))
    else if (type === 'left_for_you') events.push(leftEventAt(line, field, where))
    else if (type === 'notice') events.push(noticeEventAt(line, field, where))
    else if (type === 'done') {
      done = true
      events.push({ type: 'done' })
    } else if (type === 'error') {
      failure = failureAt(line, field, where)
      events.push({ type: 'error', ...failure })
    } else if (type === 'heartbeat') {
      // Liveness and not a stage of the run: the feed carries one a minute for
      // a step still running (D3436, D3453), and no view draws one -- so the
      // line is accepted and dropped, and nothing of it is read. It is what
      // keeps a console that knows the kind from refusing a newer CLI's feed;
      // an older console, which does not, still refuses it.
    } else refuse('unknown-value', `${field}.type`, where)
  }
  return { events, done, failure }
}

/** settingsEditorAt is `settings.editor.open`'s answer: the outcome object
 * itself, as `settings.edit` answers it, with `sentence` required and a
 * non-empty one -- a write whose answer says neither what happened nor why is
 * refused rather than drawn as a blank row. */
function settingsEditorAt(raw: unknown, where: string): SettingsEditorOutcome {
  const outcome = objectAt(raw, 'the outcome', where)
  return {
    ok: boolAt(memberAt(outcome, 'ok', '', where), 'ok', where),
    sentence: stringAt(memberAt(outcome, 'sentence', '', where), 'sentence', where),
  }
}

/** WHERE is how each document is named in a refusal's sentence. */
const WHERE: Readonly<Record<NewOperationName, string>> = Object.freeze({
  'runs.stream': 'the runs stream',
  'spend.stream': 'the spend stream',
  'budget.stream': 'the caps stream',
  whoami: 'the whoami document',
  'image.catalog.ls': 'the image catalog document',
  'recipes.ls': 'the recipes document',
  'setup.plan': 'the setup plan feed',
  'setup.apply': 'the setup apply feed',
  'settings.editor.open': 'the settings editor outcome',
})

/**
 * decodeNewOperationAnswer admits one operation's answer: the `data` half of the
 * console's reply (`{ok: true, data}`), which is the value the operation's
 * handler answered. For the eight operations that spawn a child that value
 * carries the document under `document` -- a feed's `{lines: [...]}` or a
 * one-shot read's own document -- and `settings.editor.open` answers its outcome
 * object directly, as `settings.edit` does.
 *
 * A document this build cannot read is refused by name, with the same `Skew`
 * every other door in the console answers, so a screen draws one sentence
 * whether the shape it could not read was the feed's or this one's.
 */
export function decodeNewOperationAnswer<Name extends NewOperationName>(
  name: Name,
  answer: unknown,
  where: string = WHERE[name],
): Decoded<NewOperationAnswer<Name>> {
  return at(where, () => decodeAnswer(name, answer, where)) as Decoded<NewOperationAnswer<Name>>
}

function decodeAnswer(name: NewOperationName, answer: unknown, where: string): unknown {
  if (name === 'settings.editor.open') return settingsEditorAt(answer, where)
  const data = objectAt(answer, 'the answer', where)
  const document = objectAt(memberAt(data, 'document', '', where), 'the document', where)
  if (name === 'runs.stream') return runsFeedAt(document, where)
  if (name === 'spend.stream') return rowsFeedAt(document, where)
  if (name === 'budget.stream') return rowsFeedAt(document, where)
  if (name === 'whoami') return whoAmIAt(document, where)
  if (name === 'image.catalog.ls') return imageCatalogAt(document, where)
  if (name === 'recipes.ls') return recipesAt(document, where)
  return setupFeedAt(document, where)
}
