// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Projects screen: the design's screen 3, drawn from `budget show --json`,
// `accounts ls --json` and `secrets ls --json` (desktop-console plan Task 2.3;
// design section 6.2 screen 3).
//
// The design's rule for this screen, and the one this file is shaped around:
//
//   **"Every panel names its source, and where there is no source the panel
//   says so rather than drawing an empty box."**
//
// So the model is *seven panels*, each with a source line, a state
// (`read`, `unavailable`, `not-built`) and -- when the state is not `read` --
// the sentence that says why. A panel has a count only when a document was read
// (`count === null` means no measurement exists), which is what makes "not built"
// impossible to draw as a zero: there is no number for a screen to draw. Two of
// the seven have no command behind them on this tree at all:
//
//   - **the policy object** -- `fugaro project show --json` ships with
//     project-centric phase 1 (PR #390), which is not in this tree, so the panel
//     draws the *not readable with this CLI* state the design asks for **from
//     its first commit** rather than waiting for the command to exist;
//   - **the enrollment allow-list** -- project-centric phase 1 and 4.
//
// **The run list is gone, and the panels derived from it went with it.** The
// jobs table and the two repository claims that needed `runs ls` (the
// enrolled-and-seen split, and the "seen, not enrolled" case) are removed. The
// owner's report of 2026-10-31: the jobs list "is slow and serves no purpose
// there" -- `runs ls --json` is about 248 KB and six seconds for a day of runs,
// and it was this screen's second read, started before the caps it sits beside
// could be drawn. Waiting on it made the whole tab about twenty seconds with no
// feedback. The repositories panel now draws the cap document's own `repos` --
// the closest thing to the project's repository set the CLI has today -- and
// makes no claim about which of them have run: that claim needs a document this
// screen no longer reads, and a repository listed with no run is not the same
// fact as a repository running with no cap row (`repositoriesBecause`).
//
// **`budget show` is the screen's only read**, about a second on the owner's
// installation, and the entry draws the whole panel from it.
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
import type { KillView } from '../../shared/schema'
import { sanitizeRefusal, sanitizeText } from '../../shared/sanitize'
import type { TextClass } from '../../shared/sanitize'
import type { JSONDoc } from '../../source/source'
import { putReading, putText } from '../app'
import type { ElementLike } from '../app'
import { BUSY_SLOT_CLASS } from '../section'
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
 * REPOSITORIES_SOURCE_LINE names the one document the repositories panel reads.
 * It was two -- the cap document's enrolled set and the run list's seen set --
 * and it is one now that the run list is gone: the panel draws the cap
 * document's own `repos` and nothing else.
 */
export const REPOSITORIES_SOURCE_LINE = PROJECTS_SOURCE_LINES['budget.show'] as string

/**
 * SECRETS_ARRAY_NOTE is the console's own sentence about the one document whose
 * *shape* this build's reader cannot carry. It is drawn on the secrets panel
 * always, because it is a fact about the panel and not about the read: an
 * operator looking at an empty inventory is owed the reason.
 */
export const SECRETS_ARRAY_NOTE =
  'the CLI prints `secrets ls --json` as a bare JSON array; this build reads a line that is not a JSON object as a notice (json-contract section 10.13 rule 1), so the panel draws entries only when a source hands it the parsed array'

/** ProjectsPanelKey is the seven panels, in the order they are drawn. */
export type ProjectsPanelKey =
  | 'caps'
  | 'kill'
  | 'repositories'
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

/** ProjectRepositories is the cap document's own repository set: the entries
 * the project has cap rows for, which is the closest thing to an enrollment
 * this CLI has until project-centric phase 1 ships one. **It is one list and
 * not a pair**: the run list is gone from this screen, so the panel makes no
 * claim about which of these repositories have run. */
export interface ProjectRepositories {
  readonly enrolled: readonly RepoEntry[]
  readonly enrolledCount: Reading
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
 * one list comes from the cap document**, so the document missing leaves the
 * panel unable to say anything about the project's repository set, and it says
 * the read is missing rather than drawing an empty list -- which would read as
 * "this project has no repository", a different fact from "the console could not
 * ask" (Global Constraint 5).
 *
 * The reason carries the CLI's own sentence for the refused read when it printed
 * one (`absent`), and the console's account of what the absence means otherwise.
 */
function repositoriesBecause(budgetMissing: boolean, sentences: ProjectsInputs['sentences']): string {
  if (!budgetMissing) return ''
  return `the enrolled repositories are unknown: ${absent('budget.show', sentences)}`
}

/**
 * buildProjectsModel turns the three documents into the seven panels. It touches
 * no element, so every rule below -- the not-built states, the repository set
 * the cap document carries, the cap the document does not carry -- is testable
 * in plain Node.
 */
export function buildProjectsModel(inputs: ProjectsInputs): ProjectsModel {
  const budget = objectAt(inputs.budget)
  const accounts = objectAt(inputs.accounts)
  const secretList = arrayAt(inputs.secrets)

  const warnings = textsAt(budget?.['warnings'])

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

  // The repository set: the cap document's own `repos`. It is one list now that
  // the run list is gone -- there is no second set to difference it against, so
  // the panel draws these and claims nothing about which have run.
  const enrolled = repoBlocks
    .map((block) => repoEntryOf(block))
    .filter((entry): entry is RepoEntry => entry !== null)
  const repositories: ProjectRepositories = {
    enrolled,
    enrolledCount:
      budget === null
        ? unavailable('the cap document did not arrive: how many repositories are enrolled is unknown')
        : measured(enrolled.length),
  }

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
    // Read from the cap document alone, and it draws no list when that document
    // did not arrive: a repository set drawn from nothing would read as "this
    // project has no repository", which is a different fact from "the console
    // could not ask" (`repositoriesBecause`).
    budget === null
      ? unavailablePanel(
          'repositories',
          'repositories',
          'budget.show',
          repositoriesBecause(budget === null, inputs.sentences),
          REPOSITORIES_SOURCE_LINE,
        )
      : readPanel('repositories', 'repositories', 'budget.show', enrolled.length, REPOSITORIES_SOURCE_LINE),
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
    project: textAt(budget?.['project']),
    mode: textAt(budget?.['mode']),
    day: textAt(budget?.['day']),
    panels,
    caps,
    maxReserve: readNumber(budget?.['max_reserve_usd'], 'max_reserve_usd'),
    kill: { global: killAt(objectAt(budget?.['global'])?.['kill']), rows: killRows },
    repositories,
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

/** repositoriesPanel is the repository set the cap document carries: one card,
 * one entry per repository the project has a cap row for.
 *
 * **It is one card and not three.** The three were the enrolled-and-seen split
 * -- the repositories with a cap row and a run, the ones with a cap row and no
 * run yet, and the ones running with no cap row -- and every one of them needed
 * `runs ls` to say anything. The run list is gone from this screen (see the
 * header), so the panel draws the one set the cap document itself reports and
 * makes no claim about which of these repositories have run. A card with no
 * repository draws the console's own `none` and never an empty list; a panel
 * whose document never arrived draws no card at all and its own sentence says
 * so (`repositoriesBecause`). */
function repositoriesPanel(factory: ScreenFactory, model: ProjectRepositories): ScreenElement {
  const element = make(factory, 'div', 'projects-repos')
  const card = make(factory, 'article', 'projects-repo-card')
  card.setAttribute('data-kind', 'enrolled')
  literal(cell(card, factory, 'h3', 'projects-repo-card-title'), 'enrolled')
  if (model.enrolled.length === 0) {
    literal(cell(card, factory, 'p', 'projects-repo-card-none'), 'none')
  } else {
    const list = cell(card, factory, 'ul', 'projects-repo-list')
    for (const entry of model.enrolled) {
      textCell(list, factory, 'li', labelOf(entry), labelClass(labelOf(entry)), 'repo', 'projects-repo-enrolled')
    }
  }
  element.append(card)
  return element
}

/** ABSENT_MARK is the console's own `n/a` (`UNAVAILABLE_MARK`), named as a
 * module constant so the sink scan reads a string this file declares rather
 * than a value it imported. */
const ABSENT_MARK = UNAVAILABLE_MARK

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

/** PROJECTS_BUSY is what the screen's section mark says it is reading. The
 * screen reads one document and the mark names it, so a section that is busy
 * never says only "reading". */
export const PROJECTS_BUSY = 'Reading the project’s budget…'

/** PROJECTS_BODY_CLASS is the section the model is drawn into, and it is the
 * element the entry's busy mark belongs to: the screen's one read is the whole
 * of its content, so the section and the screen are the same box. */
export const PROJECTS_BODY_CLASS = 'projects-body'

/** DrawnProjectsFrame is the screen with nothing in it: the root, the section a
 * read fills, and the slot the entry's busy mark goes in. */
export interface DrawnProjectsFrame {
  readonly root: ScreenElement
  readonly body: ScreenElement
  readonly slot: ScreenElement
}

/**
 * renderProjectsFrame draws the Projects shell: the root and one empty section,
 * and no figure at all.
 *
 * **It is what makes the screen's one read answer in the frame it started in**,
 * on the owner's rule of 2026-10-31: the entry mounts this the moment the tab is
 * opened, puts the busy mark and the skeleton of the screen's shape in the
 * section, and `fillProjects` replaces the slot's contents when `budget show`
 * answers. Nothing here is drawn from a document, and the header is deliberately
 * **not** here -- the project, the mode and the day are the document's, and a
 * header drawn from nothing would name a project the console has not read.
 *
 * The root carries no `data-busy` of its own: the section does, so the
 * stylesheet dims the section's children and leaves the mark and the header
 * around it alone.
 */
export function renderProjectsFrame(factory: ScreenFactory, theme: ProjectsTheme): DrawnProjectsFrame {
  const root = make(factory, 'section', 'projects')
  root.setAttribute('data-theme', theme)
  const body = make(factory, 'div', PROJECTS_BODY_CLASS)
  const slot = cell(body, factory, 'div', BUSY_SLOT_CLASS)
  root.append(body)
  return { root, body, slot }
}

/**
 * fillProjects draws the whole model into the frame's section: the header, the
 * seven panels and the document's warnings.
 *
 * It appends to the **section** rather than to the slot, and the slot is left
 * where it is: the mark lives in the slot, and the dimming rule is written
 * against the section's own children, so a figure drawn into the slot would be
 * a figure exempt from the very rule that says the section is being re-read.
 */
export function fillProjects(
  factory: ScreenFactory,
  model: ProjectsModel,
  host: ScreenElement,
): void {
  host.append(header(factory, model))
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
    host.append(element)
  }
  for (const warning of model.warnings) {
    textCell(host, factory, 'p', warning, 'sentence', 'warnings[]', 'projects-warning')
  }
}

/** renderProjects walks the model through Task 2.0's writers: the frame with its
 * section filled. It is what the tests and the spec draw, because both want the
 * screen an operator sees rather than the shell the entry mounts first. */
export function renderProjects(factory: ScreenFactory, model: ProjectsModel, theme: ProjectsTheme): ScreenElement {
  const frame = renderProjectsFrame(factory, theme)
  fillProjects(factory, model, frame.body)
  return frame.root
}
