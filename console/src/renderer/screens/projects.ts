// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Projects screen: the design's screen 3, drawn from `budget show --json`,
// `runs ls --json`, `accounts ls --json` and `secrets ls --json`
// (desktop-console plan Task 2.3; design section 6.2 screen 3).
//
// The design's rule for this screen, and the one this file is shaped around:
//
//   **"Every panel names its source, and where there is no source the panel
//   says so rather than drawing an empty box."**
//
// So the model is *eight panels*, each with a source line, a state
// (`read`, `unavailable`, `not-built`) and -- when the state is not `read` --
// the sentence that says why. A panel has a count only when a document was read
// (`count === null` means no measurement exists), which is what makes "not built"
// impossible to draw as a zero: there is no number for a screen to draw. Two of
// the eight have no command behind them on this tree at all:
//
//   - **the policy object** -- `fugaro project show --json` ships with
//     project-centric phase 1 (PR #390), which is not in this tree, so the panel
//     draws the *not readable with this CLI* state the design asks for **from
//     its first commit** rather than waiting for the command to exist;
//   - **the enrollment allow-list** -- project-centric phase 1 and 4. The
//     repositories drawn instead are the ones the CLI reports: enrolled (the
//     cap document's own `repos`, which is the closest thing to the project's
//     repository set the CLI has today) and seen (the repositories the run list
//     has rows for), with the difference between the two named. A repository
//     with runs and no cap row is a cap nobody set, and that is worth a line.
//
// **The secrets inventory, and a fact about this build's reader.** The document
// `secrets ls --json` prints is a **bare JSON array** (`console/docs/
// json-contract.md` section 6.6: `enc.Encode(entries)`), and this console's
// reader rule (section 10.13 rule 1, `decodeFeedLine` in `src/main/spawn.ts`)
// reads a line that is not a JSON object as a **notice** -- so through the live
// source today no document reaches this panel at all, and `parseStdout` answers
// `document: null`, which the one-shot read turns into a refusal. The panel is
// therefore written to read the array (a test hands it one, and the entries are
// drawn as the design asks: **name, scope, labels and version counts only, never
// a value**) and to draw the unavailable state with the reason when nothing
// arrived. Both halves matter: the inventory is implemented, and the state an
// operator sees today is honest rather than an empty list. The fix is a reader
// rule or a CLI wrapper, and neither is this task's file list.
//
// **The dollars are not this screen's.** The Spend screen owns spend: this one
// draws the *caps*, their binding scopes and the kill switches (the plan's
// "caps, kill state"), and per-job cost only as the row's own ledger -- so two
// screens cannot disagree about a dollar figure. The three accounts stay apart
// here too: a job draws the **billed** part of its model figure as its model
// dollars -- `model_usd` minus `model_notional_usd`, C1.16/P12 -- and
// `~$` only when that part is the plan's whole figure, exactly as the
// Live and Spend screens do.
//
// Everything else is Task 2.0's two rules, unchanged: every number enters
// through `readNumber` and is drawn through `putReading`, and every string from
// a document is drawn through `putText` with the class its wire key declares --
// no markup-parsing slot, no navigating attribute, no event handler. It reads
// its own documents rather than going through `schema.ts` for the same reason
// the Spend screen does: that decoder admits the *feed*'s two shapes (it is
// `watch --json`'s contract), and four more commands are four more documents.

import { UNAVAILABLE_MARK, measured, readNumber, unavailable } from '../../shared/availability'
import type { Reading } from '../../shared/availability'
import type { OperationName } from '../../main/ipc'
import { launcherCell } from '../../shared/launcher'
import { RIGOR_ABSENT, readRigor, rigorModeWord } from '../../shared/rigor'
import type { RigorView } from '../../shared/rigor'
import type { KillView } from '../../shared/schema'
import { sanitizeRefusal, sanitizeText } from '../../shared/sanitize'
import type { TextClass } from '../../shared/sanitize'
import type { JSONDoc } from '../../source/source'
import { putReading, putText } from '../app'
import type { ElementLike } from '../app'
import { disabledWrite } from '../writes'

// ---------------------------------------------------------------------------
// The source lines, and the panels.
// ---------------------------------------------------------------------------

/**
 * PROJECTS_SOURCE_LINES is the command line each operation's document comes
 * from, in the console's own words. It is a table rather than a string in each
 * panel because the design's review criterion for this screen is that **every
 * field traces to a command line in section 5.1's allow-list**, and a table is
 * what a test can hold to the allow-list itself (`CONSOLE_OPERATIONS`).
 */
export const PROJECTS_SOURCE_LINES: Readonly<Record<string, string>> = Object.freeze({
  'budget.show': 'fugaro budget show --json',
  'runs.list': 'fugaro runs ls --json',
  'accounts.inventory': 'fugaro accounts ls --json',
  'secrets.inventory': 'fugaro secrets ls --json',
})

/**
 * POLICY_SOURCE_LINE is the command the policy panel *would* read, quoted from
 * the design's own sentence about it (section 6.2 screen 3). No operation in
 * `CONSOLE_OPERATIONS` carries it, and the panel's `operation` is null: the
 * screen says what it cannot read rather than claiming a read it never made.
 */
export const POLICY_SOURCE_LINE = 'fugaro project show --json'

/** ENROLLMENT_SOURCE_LINE is the same honesty for the allow-list: there is no
 * command on this tree, so the panel names the phase it ships with. */
export const ENROLLMENT_SOURCE_LINE = 'no command on this tree (project-centric phase 1 and 4)'

/**
 * REPOSITORIES_SOURCE_LINE names **both** documents the repositories panel
 * reads: the enrolled set is the cap document's and the seen set is the run
 * list's. It is the one panel with two sources, and naming only one of them
 * would be a claim about where the other list came from.
 */
export const REPOSITORIES_SOURCE_LINE = `${PROJECTS_SOURCE_LINES['budget.show'] as string} + ${PROJECTS_SOURCE_LINES['runs.list'] as string}`

/**
 * SECRETS_ARRAY_NOTE is the console's own sentence about the one document whose
 * *shape* this build's reader cannot carry. It is drawn on the secrets panel
 * always, because it is a fact about the panel and not about the read: an
 * operator looking at an empty inventory is owed the reason.
 */
export const SECRETS_ARRAY_NOTE =
  'the CLI prints `secrets ls --json` as a bare JSON array; this build reads a line that is not a JSON object as a notice (json-contract section 10.13 rule 1), so the panel draws entries only when a source hands it the parsed array'

/** PROJECT_JOB_CAP bounds the job list, like the log panel's cap: a project
 * with ten thousand runs is a project whose screen is not a list of ten
 * thousand rows, and the count of what was not drawn is drawn instead. */
export const PROJECT_JOB_CAP = 50

/** ProjectsPanelKey is the eight panels, in the order they are drawn. */
export type ProjectsPanelKey =
  | 'caps'
  | 'kill'
  | 'repositories'
  | 'jobs'
  | 'accounts'
  | 'secrets'
  | 'policy'
  | 'enrollment'

/** PROJECT_PANEL_KEYS is that list as a value: the model carries one panel per
 * member and a test walks it. */
export const PROJECT_PANEL_KEYS: readonly ProjectsPanelKey[] = Object.freeze([
  'caps',
  'kill',
  'repositories',
  'jobs',
  'accounts',
  'secrets',
  'policy',
  'enrollment',
])

/**
 * ProjectPanelState is the design's three answers, and there are no others:
 * `read` (a document arrived and the panel is drawn from it), `unavailable`
 * (the read was refused or nothing arrived -- the reason is the CLI's own
 * sentence when it printed one), and `not-built` (there is no source at all on
 * this tree).
 */
export type ProjectPanelState = 'read' | 'unavailable' | 'not-built'

/**
 * ProjectsPanel is what every panel says about itself, whatever it draws.
 *
 * `count` is `null` whenever no measurement exists -- for `not-built` and for
 * `unavailable` alike -- and that is the mechanism behind the plan's rule that
 * an unbuilt section is *never* drawn as a zero: a screen has no reading to
 * format, so there is nothing that could render as `0`.
 */
/** ProjectsBecauseKind names the two doors a panel's reason may go through: see
 * `ProjectsPanel.becauseKind`. It is a named type rather than an inline union
 * because the panel constructors take it as a defaulted argument. */
type ProjectsBecauseKind = 'refusal' | 'document'

export interface ProjectsPanel {
  readonly key: ProjectsPanelKey
  readonly title: string
  readonly source: string
  /** `operation` is the allow-listed read behind the panel, or null when there
   * is none (the two not-built panels). */
  readonly operation: OperationName | null
  readonly state: ProjectPanelState
  /** `because` is why there is nothing, in the CLI's words when it printed a
   * sentence and in the console's own otherwise. Non-null unless `read`. */
  readonly because: string | null
  /** `becauseKind` says which door `because` goes through, and it is not
   * decoration: review round 5 of PR 478 found the same channel drawn two ways
   * (this panel through `putText`, the refused-read screen through a literal).
   *
   *   - `refusal` -- the console's account of a read that produced nothing,
   *     carrying the CLI's own sentence when the child printed one (`absent`).
   *     It goes through `sanitizeRefusal`: the credential rules and this
   *     layer's own, so a token echoed into stderr is redacted and the CLI's
   *     remedy -- `roles/datastore.viewer` -- is *not* drawn as `[redacted]`,
   *     which is what design section 6.3 requires ("the console shows that text
   *     unedited").
   *   - `document` -- a string a **document** wrote (`accounts`' `note`). Task
   *     2.0's rule applies to it unchanged: it goes through `putText` with its
   *     wire class, or it does not reach the screen. */
  readonly becauseKind: ProjectsBecauseKind
  readonly count: Reading | null
}

// ---------------------------------------------------------------------------
// The model.
// ---------------------------------------------------------------------------

/** RepoEntry is a repository as one of the two lists names it: the bucket key
 * (always there) and `owner/name` (the CLI omits it on some documents). */
export interface RepoEntry {
  readonly slug: string
  readonly repo: string | null
}

/** ProjectCapScope is which row of the caps panel a row is: the project's own
 * limits, the defaults every repository inherits, or one repository. */
export type ProjectCapScope = 'global' | 'defaults' | 'repo'

/**
 * ProjectCapRow is one line of the caps panel.
 *
 * `perRunCapSource` is the design's **binding scope named**: the CLI writes
 * `repo`, `default`, `global` or `none` on a repository row
 * (`perRunCapSource`, `internal/cli/budget.go:293`), which is the answer to
 * "which cap is actually binding here" that a person cannot get from the
 * numbers alone -- a repository cap and a project cap can be the same figure
 * with different consequences. It is null on the two project-wide rows because
 * there is nothing above them.
 */
export interface ProjectCapRow {
  readonly scope: ProjectCapScope
  readonly slug: string | null
  readonly repo: string | null
  readonly dailyCap: Reading
  readonly dailyCapSource: string | null
  readonly perRunCap: Reading
  readonly perRunCapSource: string | null
}

/** ProjectKillRow is a repository's kill switch: the slug, and the kill when it
 * is on. `null` is not-killed, which is a measured fact about the document
 * (`*showKill` with no `omitempty`), not an unknown. */
export interface ProjectKillRow {
  readonly slug: string
  readonly repo: string | null
  readonly kill: KillView | null
}

/** ProjectJobCost is one job row's ledger: the three accounts, apart. */
export interface ProjectJobCost {
  readonly model: Reading
  readonly compute: Reading
  readonly total: Reading
}

/** ProjectJob is one `runview.Row` of the run list, as this screen draws it. */
export interface ProjectJob {
  readonly run: string
  readonly repo: string | null
  readonly workflow: string | null
  readonly status: string
  readonly stage: string | null
  readonly requestedBy: string | null
  /** the row's whole recorded launcher (`launched_by`), `null` when the record
   * carries none; the cell draws `launcherShort` of it (`shared/launcher.ts`) */
  readonly launchedBy: string | null
  readonly branch: string | null
  readonly created: string | null
  readonly cost: ProjectJobCost
  /** the row's rigor block, or `null` for a record that carries none this
   * reader can show (`shared/rigor.ts`). The MODE column draws it, and it is
   * drawn **only when some listed run carries one** -- the conditional column
   * `runs ls` itself writes (`internal/cli/runs.go`'s `showMode`) */
  readonly rigor: RigorView | null
}

/** ProjectJobs is the job list, its bound, and what the bound left out. */
export interface ProjectJobs {
  readonly rows: readonly ProjectJob[]
  readonly total: Reading
  readonly dropped: Reading
}

/** ProjectRepositories is the two lists and the difference between them. */
export interface ProjectRepositories {
  readonly enrolled: readonly RepoEntry[]
  readonly seen: readonly RepoEntry[]
  /** a repository the cap document knows and the run list has no row for */
  readonly onlyEnrolled: readonly string[]
  /** a repository the run list has rows for and the cap document does not: the
   * case where a repository is running with no cap the project set for it */
  readonly onlySeen: readonly string[]
  readonly enrolledCount: Reading
  readonly seenCount: Reading
}

/** ProjectAccounts is `accounts ls --json`: the names, and whether the project
 * config could be selected at all. */
export interface ProjectAccounts {
  readonly repo: string | null
  readonly complete: boolean
  readonly names: readonly string[]
  readonly note: string | null
}

/** ProjectSecretLabel is one label of a secret: the key and its value. Both are
 * metadata (`fugaro_secret`, `managed_by`), and neither is ever a secret's
 * payload -- the CLI "has no access call at all" (contract section 6.6). */
export interface ProjectSecretLabel {
  readonly key: string
  readonly value: string
}

/** ProjectSecret is one `secretEntry`: names, labels and version counts. */
export interface ProjectSecret {
  readonly name: string
  readonly scope: string | null
  readonly allowed: readonly string[]
  readonly labels: readonly ProjectSecretLabel[]
  readonly versions: Reading
  readonly latest: string | null
}

/** ProjectSecrets is the inventory, plus the console's sentence about the
 * document's shape. */
export interface ProjectSecrets {
  readonly entries: readonly ProjectSecret[]
  readonly note: string
}

/**
 * ProjectsInputs is everything the screen is drawn from. Each document is
 * `unknown` rather than `JSONDoc` for one reason: `secrets ls --json` is a bare
 * array, and a bare array is not a `JSONDoc` -- the sources cannot carry one
 * (see the header), and this screen's reader accepts either.
 *
 * `null` means "no document arrived". `sentences` carries the CLI's own
 * sentence for a read that was refused, keyed by operation, which is design
 * section 6.3's rule that a refused read shows the CLI's text rather than an
 * apology this console invented.
 */
export interface ProjectsInputs {
  readonly budget: unknown
  readonly runs: unknown
  readonly accounts: unknown
  readonly secrets: unknown
  readonly sentences?: Partial<Record<OperationName, string>> | undefined
}

/** ProjectsModel is the whole screen, built and testable with no element. */
export interface ProjectsModel {
  readonly project: string | null
  readonly mode: string | null
  readonly day: string | null
  readonly panels: readonly ProjectsPanel[]
  readonly caps: readonly ProjectCapRow[]
  readonly maxReserve: Reading
  readonly kill: { readonly global: KillView | null; readonly rows: readonly ProjectKillRow[] }
  readonly repositories: ProjectRepositories
  readonly jobs: ProjectJobs
  readonly accounts: ProjectAccounts
  readonly secrets: ProjectSecrets
  readonly warnings: readonly string[]
}

// ---------------------------------------------------------------------------
// Reading the four documents.
// ---------------------------------------------------------------------------

/** objectAt is a value that is a JSON object, or null. Tolerant on the axis the
 * contract names -- a document the console cannot read is an unavailable panel
 * -- and closed on the axis it does not: nothing here guesses a shape. */
function objectAt(raw: unknown): Record<string, unknown> | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

/** arrayAt is a value that is a JSON array, or null. */
function arrayAt(raw: unknown): readonly unknown[] | null {
  return Array.isArray(raw) ? raw : null
}

/** textAt is a value that is a JSON string, or null. */
function textAt(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

/** textsAt is an array of strings, with the values that are not strings left
 * out: `allowed`, `warnings` and a repository list are all `[]string` on the
 * wire, and a document that carries something else in the array is a document
 * this screen does not draw a guess from. */
function textsAt(raw: unknown): readonly string[] {
  const array = arrayAt(raw)
  if (array === null) return []
  return array.filter((item): item is string => typeof item === 'string')
}

/** capSourceAt is a cap's binding scope: the CLI's own three words plus the
 * global one, or null when the key is absent. It is an enum the CLI writes from
 * its own constant table, and it is drawn as the CLI worded it. */
function capSourceAt(raw: unknown): string | null {
  const text = textAt(raw)
  if (text === null) return null
  return CAP_SOURCES.includes(text) ? text : 'unknown'
}

/** CAP_SOURCES is `perRunCapSource`'s own vocabulary (`budget.go:293`), read
 * off the CLI: a fourth word is a newer CLI, and the screen says `unknown`
 * rather than drawing a word it does not know. */
const CAP_SOURCES: readonly string[] = Object.freeze(['repo', 'default', 'global', 'none'])

/** killAt reads a `*showKill`: an object with the three strings, or null when
 * the wire has none. A kill with no `by`/`at`/`reason` is a kill all the same --
 * the keys are `omitempty` -- so the three are read as empty strings and the
 * screen draws what it has. */
function killAt(raw: unknown): KillView | null {
  const kill = objectAt(raw)
  if (kill === null) return null
  return { by: textAt(kill['by']) ?? '', at: textAt(kill['at']) ?? '', reason: textAt(kill['reason']) ?? '' }
}

/** repoEntryOf reads `slug` and `repo` off a repository block or a run row. The
 * slug is the bucket key and is on both shapes; `repo` is `omitempty`, which is
 * why a row can name a repository by its key alone. */
function repoEntryOf(raw: unknown): RepoEntry | null {
  const object = objectAt(raw)
  if (object === null) return null
  const slug = textAt(object['slug'])
  const repo = textAt(object['repo'])
  if (slug === null && repo === null) return null
  return { slug: slug ?? repo ?? '', repo }
}

/** modelReading is this screen's model figure: the row's model dollars as
 * **money** (C1.16, P12/D140), because `model_usd` is the whole model figure
 * -- the money and the subscription's list price together -- and
 * `model_notional_usd` is the part of it the plan paid. The billed dollars are
 * the difference; the basis alone cannot make that split, since a mixed cascade
 * run's basis is `api-list` for the whole run while part of its figure is the
 * plan's. A row written before C1.16 carries no `model_notional_usd`, and then
 * the subscription basis is the fallback: such a row's whole figure is the
 * plan's, and that is the notional account.
 *
 * The state follows the figure: the money part when the row has one, and
 * otherwise the notional figure marked notional -- the same figure and the same
 * mark a subscription row drew before the key existed. The Run detail screen
 * declares its own copy of this rule for its own document (`modelReading` in
 * `run.ts`): the four screens are siblings and neither imports the other, so
 * the two are held equal by their tests rather than by a shared module. */
function modelReading(model: Reading, notional: Reading, basis: string | null): Reading {
  if (model.value === null) return model
  const plan = notional.value ?? (basis === 'subscription' ? model.value : 0)
  const money = Math.max(0, model.value - plan)
  if (money > 0) return { ...model, value: money }
  if (plan > 0 || basis === 'subscription') return { ...model, value: plan, state: 'notional' }
  return model
}

/** costOf reads `runstore.Cost`'s fields the console draws, with the model
 * figure split into what was billed and what the plan paid (`modelReading`). */
function costOf(raw: unknown, where: string): ProjectJobCost {
  const cost = objectAt(raw) ?? {}
  const basis = textAt(cost['model_basis'])
  const model = readNumber(cost['model_usd'], `${where}.model_usd`)
  const notional = readNumber(cost['model_notional_usd'], `${where}.model_notional_usd`)
  return {
    model: modelReading(model, notional, basis),
    compute: computeReading(cost, `${where}.compute_usd`),
    total: readNumber(cost['total_usd'], `${where}.total_usd`),
  }
}

/**
 * computeReading is compute, which the flag decides and not the number
 * (`runstore.Cost`): `ComputeEstimated` "is true only when prices and resources
 * were known; **when false, ComputeUSD is 0 and means 'not estimated', not
 * free**" (`internal/runstore/cost.go:15`). So the flag off is **not** a
 * measured `$0.00` -- that reads as free, which is the one thing the design's
 * section 10.1 forbids -- and it is not a zero-hiding `n/a` either: it is
 * unavailable, with the CLI's own sentence, which is what `runs ls` prints
 * ("compute not estimated") and what the report's `compute_not_estimated` count
 * is about.
 *
 * A figure that is present with the flag off is not a shape the CLI writes; the
 * number is still the number, and hiding it would be the worse error of the
 * two, so it is drawn as the measurement it is.
 */
function computeReading(cost: Record<string, unknown>, field: string): Reading {
  const reading = readNumber(cost['compute_usd'], field)
  if (cost['compute_estimated'] === true) {
    return reading.value === null ? reading : { ...reading, state: 'estimated' }
  }
  if (reading.value === null || reading.value === 0) return unavailable(COMPUTE_NOT_ESTIMATED)
  return reading
}

/** COMPUTE_NOT_ESTIMATED is that sentence. The Run detail screen declares its
 * own copy rather than importing this one: the four screens are siblings and
 * none owns another, and a constant shared across two of them would be the first
 * thread of a dependency the file layouts deliberately do not have. */
const COMPUTE_NOT_ESTIMATED =
  'compute is not estimated for this run: the price table or the run\'s resources were unknown, and a `compute_usd` of 0 with `compute_estimated` false means "not estimated", not free'

/** capRow is one row of the caps panel, from either of the three shapes the
 * document uses (the project's own block, the defaults block, a repository
 * block). */
function capRow(
  scope: ProjectCapScope,
  block: Record<string, unknown>,
  dailyKey: string,
  perRunKey: string,
  entry: RepoEntry | null,
  dailySource: string | null,
  perRunSource: string | null,
): ProjectCapRow {
  return {
    scope,
    slug: entry?.slug ?? null,
    repo: entry?.repo ?? null,
    dailyCap: readNumber(block[dailyKey], `${scope}.${dailyKey}`),
    dailyCapSource: dailySource,
    perRunCap: readNumber(block[perRunKey], `${scope}.${perRunKey}`),
    perRunCapSource: perRunSource,
  }
}

/** panelOf builds one panel: a read, an unavailable one, or a not-built one.
 * The three are separate doors so that a caller cannot build a `read` panel
 * with no count, or a not-built one with a count -- the shape the plan's
 * "never as a zero" rule depends on. */
function readPanel(
  key: ProjectsPanelKey,
  title: string,
  operation: OperationName,
  count: number,
  source?: string,
): ProjectsPanel {
  return {
    key,
    title,
    source: source ?? PROJECTS_SOURCE_LINES[operation] ?? `fugaro ${operation}`,
    operation,
    state: 'read',
    because: null,
    becauseKind: 'refusal',
    count: measured(count),
  }
}

function unavailablePanel(
  key: ProjectsPanelKey,
  title: string,
  operation: OperationName,
  because: string,
  source?: string,
  becauseKind: ProjectsBecauseKind = 'refusal',
): ProjectsPanel {
  return {
    key,
    title,
    source: source ?? PROJECTS_SOURCE_LINES[operation] ?? `fugaro ${operation}`,
    operation,
    state: 'unavailable',
    because: becauseKind === 'refusal' ? drawnRefusal(because) : because,
    becauseKind,
    count: null,
  }
}

function notBuiltPanel(key: ProjectsPanelKey, title: string, source: string, because: string): ProjectsPanel {
  return {
    key,
    title,
    source,
    operation: null,
    state: 'not-built',
    because: drawnRefusal(because),
    becauseKind: 'refusal',
    count: null,
  }
}

/** POLICY_BECAUSE and ENROLLMENT_BECAUSE are the two not-built reasons, in the
 * console's own words. They both begin with the design's phrase for this state
 * -- *not readable with this CLI* -- so that a reader (and a test) can find it. */
const POLICY_BECAUSE =
  'not readable with this CLI: the policy object ships with project-centric phase 1 (`fugaro project show --json`, PR #390), so there is no document to draw'

const ENROLLMENT_BECAUSE =
  'not readable with this CLI: the enrollment allow-list ships with project-centric phase 1 and phase 4; the repositories below are the ones the CLI reports, not the ones the project admits'

/** REFUSAL_UNREADABLE is what a panel says when its reason cannot be drawn at
 * all (`sanitizeRefusal` refuses it -- a string longer than the console reads,
 * or one that is not text). The console still knows the read refused, which is
 * what the panel's own state and source line say. */
const REFUSAL_UNREADABLE =
  'the CLI refused this read, and its own sentence is longer than the console will draw'

/**
 * drawnRefusal is `sanitizeRefusal`'s decision for a panel's reason, applied
 * where the reason is **built** rather than where it is drawn.
 *
 * Review round 5 of PR 478: this screen drew the CLI's refusal sentence through
 * `putText`, so a refusal naming `roles/datastore.viewer` was drawn
 * `[redacted]` here while the refused-read screen drew it verbatim -- one
 * channel, two treatments, and this one lost the remedy design section 6.3
 * requires ("the console shows that text unedited"). Now both screens run the
 * same pass: the credential rules and this layer's own, never the pattern
 * classes that eat a role name. A document's own string never reaches this
 * function; the accounts panel's `note` is drawn through `putText` like every
 * other document string (`becauseKind`).
 *
 * Sanitizing in the panel constructors is also what keeps `panelShell`'s
 * `literal` call honest: every `because` it draws is either a string this pass
 * produced or one of the console's own constants.
 */
function drawnRefusal(because: string): string {
  const drawn = sanitizeRefusal(because, 'the reason this panel has nothing to draw')
  return drawn.ok ? drawn.text : REFUSAL_UNREADABLE
}

/** absent is the sentence for a panel whose read produced nothing and whose
 * refusal carried no CLI sentence of its own. */
function absent(operation: OperationName, sentences: ProjectsInputs['sentences']): string {
  const sentence = sentences?.[operation]
  if (sentence !== undefined && sentence.trim() !== '') return sentence
  return `no document arrived for \`${PROJECTS_SOURCE_LINES[operation] ?? operation}\``
}

/**
 * repositoriesBecause is why the repositories panel has nothing to draw. **Its
 * two lists come from two documents** -- the enrolled set from the cap document
 * and the seen set from the run list -- so either one missing leaves the panel
 * unable to make either claim, and it says which is missing rather than drawing
 * the half it has:
 *
 *   - without the cap document, every repository the run list knows would be
 *     drawn as **running with no cap row**, which is a false statement about a
 *     project whose caps nobody read;
 *   - without the run list, the enrolled set would be drawn as having **no runs
 *     at all**, `seen 0`, which is a fabricated zero of the same kind.
 *
 * The reason carries the CLI's own sentence for the refused read when it printed
 * one (`absent`), and the console's account of what the absence means otherwise.
 */
function repositoriesBecause(
  budgetMissing: boolean,
  runsMissing: boolean,
  sentences: ProjectsInputs['sentences'],
): string {
  const parts: string[] = []
  if (budgetMissing) {
    parts.push(`the enrolled repositories are unknown: ${absent('budget.show', sentences)}`)
  }
  if (runsMissing) {
    parts.push(`the repositories with a recorded run are unknown: ${absent('runs.list', sentences)}`)
  }
  return parts.join('; ')
}

/**
 * buildProjectsModel turns the four documents into the eight panels. It touches
 * no element, so every rule below -- the not-built states, the enrolled/seen
 * split, the cap the document does not carry -- is testable in plain Node.
 */
export function buildProjectsModel(inputs: ProjectsInputs): ProjectsModel {
  const budget = objectAt(inputs.budget)
  const runs = objectAt(inputs.runs)
  const accounts = objectAt(inputs.accounts)
  const secretList = arrayAt(inputs.secrets)

  const warnings = [...textsAt(budget?.['warnings']), ...textsAt(runs?.['warnings'])]

  // The caps: the project's own block, the defaults every repository inherits,
  // and one row per repository. The global block is `showGlobal`, whose two caps
  // are `daily_cap_usd` and `per_run_cap_usd`.
  const caps: ProjectCapRow[] = []
  const global = objectAt(budget?.['global'])
  if (global !== null) caps.push(capRow('global', global, 'daily_cap_usd', 'per_run_cap_usd', null, null, null))
  const defaults = objectAt(budget?.['defaults'])
  if (defaults !== null) {
    caps.push(
      capRow('defaults', defaults, 'repo_daily_cap_usd', 'repo_per_run_cap_usd', null, null, null),
    )
  }
  const repoBlocks = arrayAt(budget?.['repos']) ?? []
  for (const block of repoBlocks) {
    const object = objectAt(block)
    const entry = repoEntryOf(block)
    if (object === null || entry === null) continue
    caps.push(
      capRow(
        'repo',
        object,
        'daily_cap_usd',
        'per_run_cap_usd',
        entry,
        capSourceAt(object['daily_cap_source']),
        capSourceAt(object['per_run_cap_source']),
      ),
    )
  }

  const killRows: ProjectKillRow[] = []
  for (const block of repoBlocks) {
    const entry = repoEntryOf(block)
    if (entry === null) continue
    killRows.push({ slug: entry.slug, repo: entry.repo, kill: killAt(objectAt(block)?.['kill']) })
  }

  // The two repository lists. Enrolled is the cap document's own set -- the
  // repositories the project has cap rows for, which is the closest thing to an
  // enrollment this CLI has until phase 1 ships one -- and seen is the run
  // list's, by `repo` (the row carries no slug).
  const enrolled = repoBlocks
    .map((block) => repoEntryOf(block))
    .filter((entry): entry is RepoEntry => entry !== null)
  const seenRuns = (arrayAt(runs?.['runs']) ?? []).map((row) => objectAt(row)).filter((row): row is Record<string, unknown> => row !== null)
  const seen: RepoEntry[] = []
  for (const row of seenRuns) {
    const repo = textAt(row['repo'])
    if (repo === null) continue
    if (!seen.some((entry) => entry.repo === repo || entry.slug === repo)) seen.push({ slug: repo, repo })
  }
  const enrolledKeys = new Set(enrolled.flatMap((entry) => [entry.slug, entry.repo ?? entry.slug]))
  const seenKeys = new Set(seen.flatMap((entry) => [entry.slug, entry.repo ?? entry.slug]))
  // A difference needs **both** sides. With one document missing the list that
  // would have come from it is empty, and every entry of the other list would
  // read as the difference -- "running with no cap row" for a project whose caps
  // nobody read. So neither difference is computed until both documents are
  // here, and neither count is a zero: it is the reading the missing document
  // owes (`Reading.because` says which).
  const bothKnown = budget !== null && runs !== null
  const repositories: ProjectRepositories = {
    enrolled,
    seen,
    onlyEnrolled: bothKnown
      ? enrolled.filter((entry) => !seenKeys.has(entry.slug) && !seenKeys.has(entry.repo ?? '')).map(labelOf)
      : [],
    onlySeen: bothKnown
      ? seen.filter((entry) => !enrolledKeys.has(entry.slug) && !enrolledKeys.has(entry.repo ?? '')).map(labelOf)
      : [],
    enrolledCount:
      budget === null
        ? unavailable('the cap document did not arrive: how many repositories are enrolled is unknown')
        : measured(enrolled.length),
    seenCount:
      runs === null
        ? unavailable('the run list did not arrive: how many repositories have a run is unknown')
        : measured(seen.length),
  }

  // The jobs: `runs ls --json`'s rows, capped. The newest are the ones a person
  // is looking for, so the cap keeps the head of the list the CLI wrote (which
  // is newest-first, `runs.go`) and counts the rest.
  const jobs: ProjectJob[] = seenRuns.slice(0, PROJECT_JOB_CAP).map((row) => ({
    run: textAt(row['run']) ?? textAt(row['run_id']) ?? '',
    repo: textAt(row['repo']),
    workflow: textAt(row['workflow']),
    status: textAt(row['status']) ?? '',
    stage: textAt(row['stage']),
    requestedBy: textAt(row['requested_by']),
    launchedBy: textAt(row['launched_by']),
    branch: textAt(row['branch']),
    created: textAt(row['created']),
    cost: costOf(row['cost'], 'runs[].cost'),
    rigor: readRigor(row['rigor']),
  }))

  // The accounts: `accountsListDoc`. `complete: false` means the project config
  // could not be selected, which the contract says is `unavailable` **and never
  // an empty list** -- the type's own doc comment is the authority.
  const complete = accounts?.['complete'] === true
  const accountNote = textAt(accounts?.['note'])
  const projectAccounts: ProjectAccounts = {
    repo: textAt(accounts?.['repo']),
    complete,
    names: textsAt(accounts?.['accounts']),
    note: accountNote,
  }

  // The secrets: the bare array. `versions` is the count of versions not
  // destroyed, and `latest` is the newest enabled one; `id` and `created` are
  // deliberately not read at all -- the design's "names, labels and version
  // counts only", and the surest way to keep a value off a screen is to not
  // have it in the model.
  const secretEntries: ProjectSecret[] = (secretList ?? []).flatMap((item) => {
    const entry = objectAt(item)
    if (entry === null) return []
    const labels = objectAt(entry['labels'])
    return [
      {
        name: textAt(entry['name']) ?? '',
        scope: textAt(entry['scope']),
        allowed: textsAt(entry['allowed']),
        labels: Object.entries(labels ?? {}).flatMap(([key, value]) => {
          const text = textAt(value)
          return text === null ? [] : [{ key, value: text }]
        }),
        versions: readNumber(entry['versions'], 'secrets[].versions'),
        latest: textAt(entry['latest']),
      },
    ]
  })

  const panels: ProjectsPanel[] = [
    budget === null
      ? unavailablePanel('caps', 'caps', 'budget.show', absent('budget.show', inputs.sentences))
      : readPanel('caps', 'caps', 'budget.show', caps.length),
    budget === null
      ? unavailablePanel('kill', 'kill switches', 'budget.show', absent('budget.show', inputs.sentences))
      : readPanel('kill', 'kill switches', 'budget.show', killRows.filter((row) => row.kill !== null).length),
    // Read only when **both** documents arrived: the panel's two lists are one
    // from each, and a panel drawn from half a pair makes claims about the other
    // half out of nothing (`repositoriesBecause`).
    budget === null || runs === null
      ? unavailablePanel(
          'repositories',
          'repositories',
          budget === null ? 'budget.show' : 'runs.list',
          repositoriesBecause(budget === null, runs === null, inputs.sentences),
          REPOSITORIES_SOURCE_LINE,
        )
      : readPanel('repositories', 'repositories', 'budget.show', enrolled.length, REPOSITORIES_SOURCE_LINE),
    runs === null
      ? unavailablePanel('jobs', 'jobs', 'runs.list', absent('runs.list', inputs.sentences))
      : readPanel('jobs', 'jobs', 'runs.list', jobs.length),
    accounts === null
      ? unavailablePanel('accounts', 'Claude accounts', 'accounts.inventory', absent('accounts.inventory', inputs.sentences))
      : complete
        ? readPanel('accounts', 'Claude accounts', 'accounts.inventory', projectAccounts.names.length)
        : unavailablePanel(
            'accounts',
            'Claude accounts',
            'accounts.inventory',
            accountNote ?? 'the document says complete: false: the project config could not be selected',
            undefined,
            // A document's own `note` is a document's string: it goes through
            // `putText`, not through the refusal door.
            accountNote === null ? 'refusal' : 'document',
          ),
    secretList === null
      ? unavailablePanel('secrets', 'secrets inventory', 'secrets.inventory', absent('secrets.inventory', inputs.sentences))
      : readPanel('secrets', 'secrets inventory', 'secrets.inventory', secretEntries.length),
    notBuiltPanel('policy', 'policy', POLICY_SOURCE_LINE, POLICY_BECAUSE),
    notBuiltPanel('enrollment', 'enrollment allow-list', ENROLLMENT_SOURCE_LINE, ENROLLMENT_BECAUSE),
  ]

  return {
    project: textAt(budget?.['project']) ?? textAt(runs?.['project']),
    mode: textAt(budget?.['mode']),
    day: textAt(budget?.['day']),
    panels,
    caps,
    maxReserve: readNumber(budget?.['max_reserve_usd'], 'max_reserve_usd'),
    kill: { global: killAt(objectAt(budget?.['global'])?.['kill']), rows: killRows },
    repositories,
    jobs: {
      rows: jobs,
      total: measured(seenRuns.length),
      dropped: measured(Math.max(0, seenRuns.length - jobs.length)),
    },
    accounts: projectAccounts,
    secrets: { entries: secretEntries, note: SECRETS_ARRAY_NOTE },
    warnings,
  }
}

/** labelOf is a repository's own name for a sentence: `owner/name` when the
 * document carries it, the bucket key when it does not. */
function labelOf(entry: RepoEntry): string {
  return entry.repo ?? entry.slug
}

/** labelClass is the class a `labelOf` value is drawn with: `owner/name` is a
 * repository and the slash-free bucket key is a slug -- the two shapes the CLI
 * writes, decided the way `spend.ts`'s `keyClass` decides them. Drawing the slug
 * fallback with the `repository` class could only ever draw `n/a` (review
 * round 2 of PR 494). */
function labelClass(label: string): TextClass {
  return label.includes('/') ? 'repository' : 'slug'
}

/** EVERY_REPOSITORY are the two values `secrets ls --project-wide` writes in
 * `allowed` for a secret open to every repository
 * (`projectSecretAllowed`, `internal/cli/secrets.go`): a provider with no
 * allow-list answers the words, and an explicit `["*"]` is passed through. They
 * are console vocabulary, matched exactly and drawn through the `label`
 * class; anything else in that list is held to the `repository` class. */
const EVERY_REPOSITORY: readonly string[] = Object.freeze(['any repository', '*'])

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

/**
 * ScreenElement is what a screen may draw into: Task 2.0's `ElementLike` (a text
 * slot and attributes), a tag, children and `append`. No markup-parsing slot, no
 * navigating attribute, no event handler and no style -- a screen that wanted one
 * would have to widen this interface first, in a diff a reviewer can see.
 *
 * It is declared here as well as in `live.ts` and `spend.ts` rather than
 * imported from either, because the four screens are siblings and none owns
 * another, and because the file the interface belongs in (`renderer/app.ts`) is
 * not this task's to edit. The declarations are structurally identical, and
 * `projects.test.ts` draws **one screen through another's factory** in both
 * directions -- so a drift is a failing test rather than a screen that compiles
 * and draws nothing. The day `app.ts` holds them, all four import them.
 */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

/** ScreenFactory is where elements come from. It is an argument rather than a
 * `document` reference so the screen can be drawn, and asserted on, in a test
 * with no window at all. */
export interface ScreenFactory {
  create(tag: string): ScreenElement
}

/** make is a fresh element with its classes. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  return element
}

/** literal writes the console's own vocabulary -- never a document's string,
 * which comes through `putText` (`../app.ts`) or not at all. */
function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

/** cell is a text cell of the console's own making, appended to a parent. */
function cell(parent: ScreenElement, factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = make(factory, tag, ...classes)
  parent.append(element)
  return element
}

/** textCell is `cell` with a document's string written into it through
 * `putText`, with the class the wire key declares. */
function textCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  raw: unknown,
  textClass: TextClass,
  field: string,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, ...classes)
  putText(element, raw, textClass, field)
  return element
}

/** numberCell is `cell` with a reading written through `putReading`, so no
 * number can be drawn without its state. */
function numberCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  reading: Reading,
  unit: Parameters<typeof putReading>[2],
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, 'projects-number', ...classes)
  putReading(element, reading, unit)
  return element
}

/**
 * rigorCell is one row's MODE cell: the mode's own word for the five modes this
 * build knows, the **raw value through `putText`** for one it does not, and
 * `RIGOR_ABSENT` for a run whose record carries no block.
 *
 * The third case is the reason this is a function rather than a lookup: a mode
 * string this build has never heard of is job-written text, so it takes the
 * text path -- bounded, escaped, and `n/a` with the reason in the `title` when
 * the sanitizer refuses it -- and never the console's own `literal` writer. A
 * cell that wrote it as vocabulary would be putting a value no reader can check
 * into the document under the console's own name.
 */
function rigorCell(parent: ScreenElement, factory: ScreenFactory, view: RigorView | null): ScreenElement {
  const drawn = cell(parent, factory, 'span', 'projects-job-mode')
  if (view === null) return literal(drawn, RIGOR_ABSENT)
  const word = rigorModeWord(view.mode)
  if (word !== null) return literal(drawn, word)
  putText(drawn, view.mode, 'label', 'runs[].rigor.mode')
  return drawn
}

/** sanitizedKey is a document string bound for an **attribute value** rather
 * than a text cell -- the same door `putText` uses (`live.ts` has the twin), or
 * null when the sanitizer refuses the string. */
function sanitizedKey(raw: string): string | null {
  const result = sanitizeText(raw, 'slug', 'repos[].slug')
  return result.ok && result.text !== '' ? result.text : null
}

/** note is one of the console's own sentences, in a cell of its own. */
function note(parent: ScreenElement, factory: ScreenFactory, text: string, ...classes: string[]): ScreenElement {
  return literal(cell(parent, factory, 'p', 'projects-note', ...classes), text)
}

/** killBanner is the badge a killed row carries: the word, and the three facts
 * the document has. A kill's keys are `omitempty`, so a kill with no operator,
 * no moment or no reason draws the ones it has and invents none of the others --
 * an empty cell would say the document carried an empty string, which is a
 * different fact from "it carried none". */
function killBanner(factory: ScreenFactory, kill: KillView): ScreenElement {
  const element = make(factory, 'div', 'projects-kill')
  element.setAttribute('data-killed', 'true')
  const word = cell(element, factory, 'span', 'projects-kill-word')
  if (kill.by === '') literal(word, 'KILLED')
  else literal(word, 'KILLED by')
  if (kill.by !== '') textCell(element, factory, 'span', kill.by, 'person', 'kill.by', 'projects-kill-by')
  if (kill.at !== '') textCell(element, factory, 'span', kill.at, 'label', 'kill.at', 'projects-kill-at')
  if (kill.reason !== '') textCell(element, factory, 'span', kill.reason, 'sentence', 'kill.reason', 'projects-kill-reason')
  return element
}

/** killRow is one kill switch: the scope it belongs to, its state as a word, the
 * badge when it is on, and the one write the design draws for it.
 *
 * The write is drawn through `disabledWrite` and binds nothing: this release
 * kills no repository and revives none, and the button says so rather than
 * looking like a control that quietly does nothing. */
function killRow(factory: ScreenFactory, scope: ScreenElement, kill: KillView | null): ScreenElement {
  const element = make(factory, 'li', 'projects-kill-row')
  const killed = kill !== null
  element.setAttribute('data-killed', killed ? 'true' : 'false')
  element.append(scope)
  const state = cell(element, factory, 'span', 'projects-kill-state')
  if (killed) literal(state, 'KILLED')
  else literal(state, 'live')
  if (kill !== null) element.append(killBanner(factory, kill))
  element.append(disabledWrite(factory, 'projects-kill-action', killed ? 'Revive' : 'Kill'))
  return element
}

/** header draws the project line: the project, the mode and the day. */
function header(factory: ScreenFactory, model: ProjectsModel): ScreenElement {
  const element = make(factory, 'header', 'projects-header')
  const heading = cell(element, factory, 'h1', 'projects-project')
  if (model.project === null) literal(heading, 'the project is unknown')
  else putText(heading, model.project, 'project', 'project')
  if (model.mode !== null) textCell(element, factory, 'span', model.mode, 'label', 'mode', 'projects-mode')
  if (model.day !== null) textCell(element, factory, 'span', model.day, 'label', 'day', 'projects-day')
  return element
}

/** panelShell draws one panel's frame: its title, its state as a word, its
 * source, and either its count or the sentence explaining why there is none. */
function panelShell(factory: ScreenFactory, panel: ProjectsPanel): ScreenElement {
  const element = make(factory, 'section', 'projects-panel')
  element.setAttribute('data-panel', panel.key)
  element.setAttribute('data-state', panel.state)
  if (panel.operation !== null) element.setAttribute('data-operation', panel.operation)
  literal(cell(element, factory, 'h2', 'projects-title'), panel.title)
  // The state is a word on the panel and never the frame's tone alone (styling
  // design section 3.5). `locked` is the design's word for a card this CLI
  // cannot read at all -- a panel with no command behind it -- and it is the
  // state whose frame is dashed.
  const state = cell(element, factory, 'span', 'projects-state')
  if (panel.state === 'read') literal(state, 'read')
  else if (panel.state === 'not-built') literal(state, 'locked')
  else literal(state, 'not read')
  literal(cell(element, factory, 'span', 'projects-source'), panel.source)
  // A count exists only where a document was read, which is what keeps a
  // not-built panel from rendering as a zero: there is no reading to format.
  if (panel.count !== null) numberCell(element, factory, 'span', panel.count, 'count', 'projects-count')
  // `because` is the console's sentence *or* the CLI's own refusal sentence
  // (`absent`), and the two doors it may go through are named by
  // `becauseKind`. A `refusal` reason was already drawn through
  // `sanitizeRefusal` where it was built (`drawnRefusal`), which is why a
  // `literal` call is honest here; a `document` reason -- the accounts panel's
  // own `note` -- goes through `putText` with its wire class, like every other
  // document string. Review round 5 of PR 478: this cell used to send *both*
  // through `putText`, which drew the CLI's remedy as `[redacted]` while the
  // refused-read screen drew it verbatim.
  if (panel.because !== null) {
    if (panel.becauseKind === 'document') {
      textCell(element, factory, 'p', panel.because, 'sentence', 'because', 'projects-because')
    } else {
      literal(cell(element, factory, 'p', 'projects-because'), panel.because)
    }
  }
  return element
}

/** capHead is one column heading of the caps table, marked as a column header so
 * a reader's tools read the columns a sighted reader reads. The word is written
 * by the caller, because it is the console's own vocabulary and a parameter
 * would be a string this file cannot vouch for. */
function capHead(parent: ScreenElement, factory: ScreenFactory): ScreenElement {
  const element = cell(parent, factory, 'th', 'projects-cap-head')
  element.setAttribute('scope', 'col')
  return element
}

/** capRowLine is one caps row: the scope it belongs to, and its two caps, each
 * in a cell of its own with the word that says where it binds.
 *
 * A cap's figure and its source word are drawn here rather than by a helper
 * taking the two as parameters, because the word's own guard is written at the
 * one place it is read: `daily_cap_source` is `repo`, `default`, `global` or
 * `none` -- the CLI's own words for where the effective cap came from, kept only
 * when it is one of them (`capSourceAt`) -- and what the sink scan reads is the
 * model's value, not a parameter's.
 *
 * **The two cells make the row a labelled row**: the figure sits under the cap's
 * column heading and the source word under it, so no row is a bare number and no
 * column is a bare list of them. */
function capRowLine(factory: ScreenFactory, row: ProjectCapRow): ScreenElement {
  const element = make(factory, 'tr', 'projects-cap')
  element.setAttribute('data-scope', row.scope)
  // The slug is the document's string, and an attribute is as much the
  // document's DOM as a text cell: it is written only as the sanitizer drew it,
  // and a slug the sanitizer refuses leaves no attribute at all.
  const slug = row.slug === null ? null : sanitizedKey(row.slug)
  if (slug !== null) element.setAttribute('data-slug', slug)
  const scope = cell(element, factory, 'th', 'projects-cap-scope')
  scope.setAttribute('scope', 'row')
  const label = row.repo ?? row.slug
  if (label !== null) putText(scope, label, labelClass(label), 'repo')
  else literal(scope, row.scope)
  const daily = cell(element, factory, 'td', 'projects-cap-cell')
  numberCell(daily, factory, 'span', row.dailyCap, 'usd', 'projects-cap-value')
  if (row.dailyCapSource !== null) literal(cell(daily, factory, 'span', 'projects-cap-source'), row.dailyCapSource)
  const perRun = cell(element, factory, 'td', 'projects-cap-cell')
  numberCell(perRun, factory, 'span', row.perRunCap, 'usd', 'projects-cap-value')
  if (row.perRunCapSource !== null) literal(cell(perRun, factory, 'span', 'projects-cap-source'), row.perRunCapSource)
  return element
}

/** capsPanel is the caps: the largest single lease, then one table whose every
 * figure is a labelled cell -- a column heading for the cap and the CLI's own
 * word for the scope that binds it beside the number. */
function capsPanel(factory: ScreenFactory, model: ProjectsModel): ScreenElement {
  const element = make(factory, 'div', 'projects-caps')
  const reserve = cell(element, factory, 'p', 'projects-reserve')
  literal(cell(reserve, factory, 'span', 'projects-reserve-word'), 'largest single lease')
  numberCell(reserve, factory, 'span', model.maxReserve, 'usd', 'projects-reserve-value')
  const table = cell(element, factory, 'table', 'projects-cap-table')
  const head = cell(table, factory, 'thead')
  const headRow = cell(head, factory, 'tr')
  literal(capHead(headRow, factory), 'scope')
  literal(capHead(headRow, factory), 'daily cap')
  literal(capHead(headRow, factory), 'per-run cap')
  const body = cell(table, factory, 'tbody')
  for (const row of model.caps) body.append(capRowLine(factory, row))
  return element
}

/** killPanel is the project's own switch and every repository's. A switch that
 * is off is drawn as the measured fact it is -- `live` -- and not as a missing
 * value, which `n/a` would say. The project's switch names itself with the
 * console's own word; a repository's is a string the document wrote, so it goes
 * through `putText` like every other one. */
function killPanel(factory: ScreenFactory, model: ProjectsModel): ScreenElement {
  const element = make(factory, 'div', 'projects-kills')
  const list = cell(element, factory, 'ul', 'projects-kill-list')
  const project = make(factory, 'span', 'projects-kill-scope')
  literal(project, 'project')
  list.append(killRow(factory, project, model.kill.global))
  for (const row of model.kill.rows) {
    const label = row.repo ?? row.slug
    const scope = make(factory, 'span', 'projects-kill-repo')
    putText(scope, label, labelClass(label), 'repo')
    list.append(killRow(factory, scope, row.kill))
  }
  return element
}

/** repositoriesPanel is the repositories as three cards: the ones the cap
 * document knows **and** the run list has seen, the ones with a cap row and no
 * run yet, and the ones running with no cap row at all. The third is the case
 * the screen exists for -- a repository spending under a cap nobody set -- so it
 * is drawn amber and carries the write that would fix it, drawn disabled like
 * every other write this release does not perform.
 *
 * **A card with no repository draws the console's own `none` and never an empty
 * list.** The difference between "the document names no repository" and "no
 * list arrived" is the whole reason this panel has a state: the second draws no
 * card at all, and the panel's own sentence says which of its two documents is
 * missing (`repositoriesBecause`). Each card is built in place rather than by a
 * helper taking a class name, so every class and every `data-kind` is a literal
 * at the call site. */
function repositoriesPanel(factory: ScreenFactory, model: ProjectRepositories): ScreenElement {
  const element = make(factory, 'div', 'projects-repos')
  // `onlyEnrolled` names the enrolled entries with no run; everything else in
  // `enrolled` has been seen. With both documents here -- and the panel draws no
  // body otherwise -- the two differences are complete, so the first card is the
  // enrolled set minus the first difference, by the label each list uses.
  const onlyEnrolled = new Set(model.onlyEnrolled)
  const both = model.enrolled.filter((entry) => !onlyEnrolled.has(labelOf(entry))).map(labelOf)

  const seen = make(factory, 'article', 'projects-repo-card')
  seen.setAttribute('data-kind', 'both')
  literal(cell(seen, factory, 'h3', 'projects-repo-card-title'), 'enrolled and seen')
  if (both.length === 0) {
    literal(cell(seen, factory, 'p', 'projects-repo-card-none'), 'none')
  } else {
    const list = cell(seen, factory, 'ul', 'projects-repo-list')
    for (const entry of both) textCell(list, factory, 'li', entry, labelClass(entry), 'repo', 'projects-repo-both')
  }

  const waiting = make(factory, 'article', 'projects-repo-card')
  waiting.setAttribute('data-kind', 'enrolled')
  literal(cell(waiting, factory, 'h3', 'projects-repo-card-title'), 'enrolled, no run yet')
  if (model.onlyEnrolled.length === 0) {
    literal(cell(waiting, factory, 'p', 'projects-repo-card-none'), 'none')
  } else {
    literal(cell(waiting, factory, 'p', 'projects-repo-card-note'), 'Cap row, no run yet.')
    const list = cell(waiting, factory, 'ul', 'projects-repo-list')
    for (const entry of model.onlyEnrolled) {
      textCell(list, factory, 'li', entry, labelClass(entry), 'repo', 'projects-only-enrolled-repo')
    }
  }

  const uncapped = make(factory, 'article', 'projects-repo-card')
  uncapped.setAttribute('data-kind', 'seen')
  literal(cell(uncapped, factory, 'h3', 'projects-repo-card-title'), 'seen, not enrolled')
  if (model.onlySeen.length === 0) {
    literal(cell(uncapped, factory, 'p', 'projects-repo-card-none'), 'none')
  } else {
    literal(cell(uncapped, factory, 'p', 'projects-repo-card-note'), 'Running with no cap row.')
    const list = cell(uncapped, factory, 'ul', 'projects-repo-list')
    for (const entry of model.onlySeen) {
      textCell(list, factory, 'li', entry, labelClass(entry), 'repo', 'projects-only-seen-repo')
    }
    uncapped.append(disabledWrite(factory, 'projects-repo-add', 'Add cap'))
  }

  element.append(seen)
  element.append(waiting)
  element.append(uncapped)
  return element
}

/** ABSENT_MARK is the console's own `n/a` (`UNAVAILABLE_MARK`), named as a
 * module constant so the sink scan reads a string this file declares rather
 * than a value it imported. */
const ABSENT_MARK = UNAVAILABLE_MARK

/** putMaybe writes one column a record may leave out -- the repository, the
 * workflow, the stage, the two actors, the timestamp -- into the cell the caller
 * has already drawn: the record's own value through `putText` when it carries
 * one, and **the console's `n/a`** when it does not.
 *
 * The cell is drawn by the caller rather than here, because the selector scan
 * reads a column's tag at the emitter call site and a tag handed to a helper as
 * a parameter is one no stylesheet rule may qualify (`run.ts`'s `putMaybe` and
 * `live.ts`'s `putActor` take the element for the same reason).
 *
 * A table cell is never omitted: one row drawn with a cell fewer shifts every
 * column after it, so the absent case draws the mark rather than nothing.
 * Nothing is invented either way, and the raw value never reaches the mark's
 * path: the document's string goes through `putText`, and a refused one is
 * `putText`'s own `n/a` with the refusal in the `title`. */
function putMaybe(element: ScreenElement, raw: string | null, textClass: TextClass, field: string): void {
  if (raw === null || raw === '') {
    literal(element, ABSENT_MARK)
    return
  }
  putText(element, raw, textClass, field)
}

/** jobHead is one column header of the jobs table. */
function jobHead(parent: ScreenElement, factory: ScreenFactory): ScreenElement {
  const element = cell(parent, factory, 'th', 'projects-job-head')
  element.setAttribute('scope', 'col')
  return element
}

/**
 * jobsPanel is the project's runs as a table: one row per run, the columns the
 * run list carries, and **the three cost accounts in three cells** -- a
 * subscription's model dollars are notional, its compute dollars are billed and
 * the two are never added into one figure (`docs/design/console-styling.md`
 * section 2.10). The row is a `tr` so the figures line up down the column: a
 * project's runs are read by comparing them, which a wrapped row of spans does
 * not let anyone do.
 *
 * **The MODE column appears only when some listed run carries a block.** It is
 * the conditional column `runs ls` writes (`internal/cli/runs.go`'s `showMode`):
 * a run from before the field, or one that died before bootstrap, has no block
 * and does not widen every other run's row for it -- and a project whose runs
 * are all older than the field draws the table it always drew. Once one row
 * carries a block the column is on every row, and the rows without one say so
 * with `RIGOR_ABSENT` rather than with a blank.
 */
function jobsPanel(factory: ScreenFactory, model: ProjectJobs): ScreenElement {
  const element = make(factory, 'div', 'projects-jobs')
  const showMode = model.rows.some((job) => job.rigor !== null)
  const table = cell(element, factory, 'table', 'projects-job-table')
  const head = cell(table, factory, 'thead')
  const headRow = cell(head, factory, 'tr')
  literal(jobHead(headRow, factory), 'run')
  literal(jobHead(headRow, factory), 'repository')
  literal(jobHead(headRow, factory), 'workflow')
  literal(jobHead(headRow, factory), 'status')
  literal(jobHead(headRow, factory), 'stage')
  literal(jobHead(headRow, factory), 'requested by')
  literal(jobHead(headRow, factory), 'started by')
  literal(jobHead(headRow, factory), 'created')
  if (showMode) literal(jobHead(headRow, factory), 'mode')
  literal(jobHead(headRow, factory), 'model')
  literal(jobHead(headRow, factory), 'compute')
  literal(jobHead(headRow, factory), 'total')
  const body = cell(table, factory, 'tbody')
  for (const job of model.rows) {
    const line = cell(body, factory, 'tr', 'projects-job')
    textCell(line, factory, 'td', job.run, 'runId', 'runs[].run', 'projects-job-run')
    putMaybe(cell(line, factory, 'td', 'projects-job-repo'), job.repo, 'repository', 'runs[].repo')
    putMaybe(cell(line, factory, 'td', 'projects-job-workflow'), job.workflow, 'label', 'runs[].workflow')
    // `status` is a string from the run record, so it goes through `putText`
    // like every other one: a value a document chose is not vocabulary the
    // console may write into the document unwatched.
    textCell(line, factory, 'td', job.status, 'label', 'runs[].status', 'projects-job-status')
    putMaybe(cell(line, factory, 'td', 'projects-job-stage'), job.stage, 'label', 'runs[].stage')
    putMaybe(cell(line, factory, 'td', 'projects-job-by'), job.requestedBy, 'person', 'runs[].requested_by')
    // Who started the job, beside who asked for it: the short form of
    // `launched_by`, drawn as a `label` (`shared/launcher.ts` says why not
    // `person`), and the mark for a row the record does not name.
    putMaybe(cell(line, factory, 'td', 'projects-job-started-by'), launcherCell(job.launchedBy), 'label', 'runs[].launched_by')
    putMaybe(cell(line, factory, 'td', 'projects-job-created'), job.created, 'label', 'runs[].created')
    if (showMode) rigorCell(cell(line, factory, 'td', 'projects-job-mode-cell'), factory, job.rigor)
    numberCell(line, factory, 'td', job.cost.model, 'usd', 'projects-job-model')
    numberCell(line, factory, 'td', job.cost.compute, 'usd', 'projects-job-compute')
    numberCell(line, factory, 'td', job.cost.total, 'usd', 'projects-job-total')
  }
  if (model.dropped.value !== null && model.dropped.value > 0) {
    const line = cell(element, factory, 'p', 'projects-jobs-note')
    literal(line, 'runs not drawn:')
    numberCell(line, factory, 'span', model.dropped, 'count', 'projects-jobs-dropped')
    literal(cell(line, factory, 'span', 'projects-jobs-total-of'), 'of')
    numberCell(line, factory, 'span', model.total, 'count', 'projects-jobs-total')
  }
  return element
}

/** accountsPanel is the Claude accounts: the repository the document was read
 * for, one row per account, and the document's own sentence when it could not
 * select a project at all.
 *
 * **An empty list the document did carry draws `none`**, and an empty list from
 * a document that could not select a project does not: the second is the case
 * `complete: false` names, and its own sentence (drawn from the document) says
 * why. A blank space would say the two are the same fact. */
function accountsPanel(factory: ScreenFactory, model: ProjectAccounts): ScreenElement {
  const element = make(factory, 'div', 'projects-accounts')
  if (model.repo !== null) textCell(element, factory, 'span', model.repo, 'repository', 'repo', 'projects-accounts-repo')
  if (model.names.length === 0) {
    if (model.complete) literal(cell(element, factory, 'p', 'projects-account-none'), 'none')
  } else {
    const list = cell(element, factory, 'ul', 'projects-account-list')
    for (const name of model.names) {
      textCell(list, factory, 'li', name, 'person', 'accounts[]', 'projects-account')
    }
  }
  if (model.note !== null) textCell(element, factory, 'p', model.note, 'sentence', 'note', 'projects-accounts-note')
  return element
}

/** SECRETS_MANAGED_WORD is the console's sentence for an inventory the console
 * itself keeps, drawn only where every entry is one (`managed_by: fugaro`). */
const SECRETS_MANAGED_WORD = 'All managed by fugaro.'

/** secretsPanel is the inventory: names, scopes, labels, version counts, and
 * where each secret is bound.
 *
 * **The "all managed by fugaro" sentence is drawn only when the panel has a
 * document and every entry in it says so.** The sentence is a claim about all of
 * them, so it takes the fact about all of them: an inventory with one secret
 * someone else manages would make it false, and a panel with no document at all
 * (this build's state -- `entry.ts` hands the panel `null`) draws no body, so
 * the sentence is not drawn there either. */
function secretsPanel(factory: ScreenFactory, model: ProjectSecrets): ScreenElement {
  const element = make(factory, 'div', 'projects-secrets')
  const list = cell(element, factory, 'ul', 'projects-secret-list')
  for (const entry of model.entries) {
    const line = cell(list, factory, 'li', 'projects-secret')
    textCell(line, factory, 'span', entry.name, 'label', 'secrets[].name', 'projects-secret-name')
    if (entry.scope !== null) textCell(line, factory, 'span', entry.scope, 'label', 'secrets[].scope', 'projects-secret-scope')
    numberCell(line, factory, 'span', entry.versions, 'count', 'projects-secret-versions')
    if (entry.latest !== null) textCell(line, factory, 'span', entry.latest, 'label', 'secrets[].latest', 'projects-secret-latest')
    for (const label of entry.labels) {
      const pair = cell(line, factory, 'span', 'projects-secret-label')
      // A label's *key* and its *value* are both strings from the document --
      // the key is `Object.entries(labels)`, not a console table -- and both go
      // through the same door: a label that held a credential, or a control or
      // invisible character, is refused or redacted here rather than drawn.
      textCell(pair, factory, 'span', label.key, 'label', 'secrets[].labels', 'projects-secret-label-key')
      textCell(pair, factory, 'span', label.value, 'label', 'secrets[].labels', 'projects-secret-label-value')
    }
    // Where the secret is bound: one block naming the fact, then the values, so
    // a row cannot read as a list of repositories with no word for what they
    // are. A secret bound nowhere draws no block rather than an empty one.
    if (entry.allowed.length > 0) {
      const bound = cell(line, factory, 'span', 'projects-secret-bound')
      literal(cell(bound, factory, 'span', 'projects-secret-bound-word'), 'bound to')
      for (const allowed of entry.allowed) {
        // The two "every repository" values still go through the door, as a
        // label; only the exact console vocabulary takes that class.
        const textClass: TextClass = typeof allowed === 'string' && EVERY_REPOSITORY.includes(allowed) ? 'label' : 'repository'
        textCell(bound, factory, 'span', allowed, textClass, 'secrets[].allowed', 'projects-secret-allowed')
      }
    }
  }
  const managed =
    model.entries.length > 0 &&
    model.entries.every((entry) => entry.labels.some((label) => label.key === 'managed_by' && label.value === 'fugaro'))
  if (managed) literal(cell(element, factory, 'p', 'projects-secrets-managed'), SECRETS_MANAGED_WORD)
  return element
}

const PANEL_BODIES: Readonly<Record<ProjectsPanelKey, (factory: ScreenFactory, model: ProjectsModel) => ScreenElement | null>> =
  Object.freeze({
    caps: (factory, model) => capsPanel(factory, model),
    kill: (factory, model) => killPanel(factory, model),
    repositories: (factory, model) => repositoriesPanel(factory, model.repositories),
    jobs: (factory, model) => jobsPanel(factory, model.jobs),
    accounts: (factory, model) => accountsPanel(factory, model.accounts),
    secrets: (factory, model) => secretsPanel(factory, model.secrets),
    // The two not-built panels draw nothing but their frame: no body, no list,
    // no count. Their reason is the frame's own `because`.
    policy: () => null,
    enrollment: () => null,
  })

/** PROJECTS_THEMES is the set of themes a screenshot golden is taken for, and
 * it is the same two the Live and Spend screens name: design section 6.1 rule 5
 * has a light and a dark palette and a small phosphor set besides, the tokens
 * are not in this task, and `data-theme` is how a theme reaches the document.
 * The three lists are held equal by a test until `theme/tokens.css` lands. */
export const PROJECTS_THEMES = Object.freeze(['light', 'dark'] as const)

/** ProjectsTheme is one of them. */
export type ProjectsTheme = (typeof PROJECTS_THEMES)[number]

/** renderProjects walks the model through Task 2.0's writers. */
export function renderProjects(factory: ScreenFactory, model: ProjectsModel, theme: ProjectsTheme): ScreenElement {
  const root = make(factory, 'section', 'projects')
  root.setAttribute('data-theme', theme)
  root.append(header(factory, model))
  for (const panel of model.panels) {
    const element = panelShell(factory, panel)
    // A body is drawn only for a panel that **read a document**: the body is the
    // read's own content, and a panel with none draws its frame and its reason
    // instead. That is what keeps a document that did not arrive from being
    // drawn as an empty list, as a `0`, or as a claim about a set no document
    // reported (`repositoriesBecause`).
    const body = panel.state === 'read' ? PANEL_BODIES[panel.key](factory, model) : null
    if (body !== null) element.append(body)
    // The one thing drawn on a panel whatever its state is the console's own
    // sentence about the panel's *shape* rather than about the read: an operator
    // looking at an empty secrets inventory is owed the reason it is empty, and
    // that reason is as true of a refused read as of an arrived one.
    if (panel.key === 'secrets') note(element, factory, model.secrets.note)
    root.append(element)
  }
  for (const warning of model.warnings) {
    textCell(root, factory, 'p', warning, 'sentence', 'warnings[]', 'projects-warning')
  }
  return root
}
