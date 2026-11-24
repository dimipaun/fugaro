// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Budget kind: one row per repository -- the caps that bind it, the money
// counted against them and its kill state -- with the link that opens the Spend
// tab (the console-improvements plan's task MO-B; spec section 5's Budget group
// as D2909 amended it; the descriptor it is written against is `../types.ts`,
// whose prose is `docs/console-improvements/manage-kinds.md`).
//
// **One document, and the console's own reader of it.** The kind's read is
// `budget.show` (`fugaro budget show --json`), which this console already reads
// for the rail's gauge, the Spend screen's caps section and the Project tab, so
// nothing here adds an operation, a spawn row or a read. Its rows come from
// `capsOf` (`screens/spend.ts`), which is *the* reading of that document -- the
// entry draws the caps panel and the Coordinator's queue from the same call --
// so a figure this table shows and a figure the caps panel shows cannot
// disagree, and there is no second reader of these keys to drift from it.
//
// **Caps and kill state, per repository, and nothing else.** A row is one
// `repos[]` entry: the repository, its two effective caps each with the scope it
// binds at, the money counted against them, and the kill state. The scope is
// drawn because a cap of $20 with no scope is a figure nobody can act on -- the
// fix differs by scope, which is `projects.ts`'s own note about
// `daily_cap_source`. The document's **global** block, its `defaults` and its
// project-wide kill switch are not rows here: they are not repositories, and a
// table whose rows are repositories would have to invent a row that is not one
// to hold them (the Project tab's caps panel draws them for the project).
//
// **No alerts** (D2909). The spec's Budget group named "spending caps, alerts,
// with a link to the Spend report", and alerts have no CLI object: nothing in
// `budget show --json` is an alert and no command lists one. The owner declined
// them rather than have the console invent a row, so this kind names no alerts
// column, reads no alerts member and draws no empty alerts state -- a document
// that grew an `alerts` array is not read here at all.
//
// **No cap control.** The console writes no cap: setting one is the budget
// admins' (`fugaro budget set`, which asks for the scope and the figure), and a
// Copy-command naming *this row's* repository is not something the descriptor
// can carry -- `actions` is a kind's own list, fixed when the kind is made and
// before any row exists, so a copy naming a row's repository, scope and figure
// has no place to be built. So the kind's one action
// is the Spend link: an operator who wants a cap changed reads the figure here
// and runs the CLI. A control the console draws and does not perform stays
// `disabledWrite` with its own reason (`writes.ts`'s `WRITE_UNAVAILABLE`), which
// is the console's one answer for a write it does not make; this kind adds no
// second one and no write of its own.
//
// **Reduced mode.** A figure a CLI the console cannot vouch for might mean
// something else by is withheld rather than misread, and the questions are the
// three of `REDUCED_VIEWS`: the caps and their scopes under `caps`, the money
// under `spend`, the kill state under `kill` (`.claude/rules/console.md`).
//
// The two money columns are the `spend` view's and not the `caps` view's, which
// is how the Live screen reads the same split: its `capsWithheld` takes out the
// bars and says in its own note that the money of the totals block -- the
// counted dollars and a repository's cap figure among them -- is "the `spend`
// view's question" (design section 5.1 rule 4, C10). This table's caps columns
// are the limits themselves, which is what `HIDDEN_VIEW_WORDS.caps` names ("the
// caps: every limit and its bars"), so they go with `caps` while the dollars
// counted against them go with `spend`; the Live screen's `killWithheld` clears
// the kill state and has no sentence for it, so the word for a withheld kill
// state is this file's own, in the shape of the caps word above.
//
// The repository's own name is not a figure and is never withheld; the Spend
// link goes with the `spend` view, because the rail does not offer a tab this
// mode withholds (`entry.ts`'s `VIEWS`, where the Spend tab is the `spend`
// view's own) and a link to a tab that is not there is a door to nothing. The
// kind is built **for** the compatibility it will be drawn under
// (`budgetKindFor`), so the gate is asked once, where the kind is made, and no
// caller can forget it -- the answer is baked into `decode` rather than passed
// to it, which is `screens/live.ts`'s `capsWithheld` in the descriptor's own
// terms.

import { formatReading } from '../../../shared/availability'
import type { Reading } from '../../../shared/availability'
import type { KillView } from '../../../shared/schema'
import { hidesView } from '../../../shared/version'
import type { CliCompatibility, ReducedView } from '../../../shared/version'
import type { JSONDoc } from '../../../source/source'
import { capsOf } from '../../screens/spend'
import type { SpendCapRow, SpendScope } from '../../screens/spend'
import { defineManageKind } from '../types'
import type { ManageAction, ManageColumn, ManageKind, ManageRow } from '../types'

// ---------------------------------------------------------------------------
// The kind's own words.
// ---------------------------------------------------------------------------

/**
 * BUDGET_CAPS_HIDDEN_WORD is what a withheld cap figure says. It is the
 * family's sentence -- the one `LIVE_CAPS_HIDDEN_WORD` ends with -- and it is a
 * second copy rather than an import for the reason that one is: the screens and
 * the kinds are siblings and none owns the others. One console with one reduced
 * mode withholding the caps in two places under two sentences would read as two
 * modes, so `manage-budget.test.ts` holds this constant equal to the Live
 * screen's.
 */
export const BUDGET_CAPS_HIDDEN_WORD =
  'the caps are withheld for the installation this console is driving'

/** BUDGET_SPEND_HIDDEN_WORD is what a withheld money figure says: the same
 * sentence for the `spend` view's own figures (the money counted against the
 * caps), which is the half of a caps row the `caps` question does not cover. */
export const BUDGET_SPEND_HIDDEN_WORD =
  'the spend is withheld for the installation this console is driving'

/** BUDGET_KILL_HIDDEN_WORD is what a withheld kill state says: the `kill`
 * view's own question, asked only for the state column -- whether a repository
 * is halted is not a figure, but which CLI wrote the halt is a shape this
 * console cannot vouch for when the view is withheld. */
export const BUDGET_KILL_HIDDEN_WORD =
  'the kill state is withheld for the installation this console is driving'

/** LIVE_WORD is the state of a repository the document carries no kill for:
 * `projects.ts`'s kill panel draws the same two words for the same fact, so a
 * repository reads the same on every screen. */
const LIVE_WORD = 'live'

/** KILLED_WORD is the state of one the document's own `kill` node names, and
 * the first word of `live.ts`'s kill banner ("KILLED by ..."). */
const KILLED_WORD = 'KILLED'

/**
 * UNKNOWN_SCOPE_WORD is what a cap scope this console does not know is drawn
 * as, and it is `projects.ts`'s `capSourceAt` word for the same case, beside a
 * figure in a table of its own.
 *
 * A scope in the CLI's own four -- `repo`, `default`, `global`, `none` -- is
 * drawn as the CLI worded it, kept only when it is one of them; a fifth is a
 * newer CLI, and its word is **not** drawn, because a value this console cannot
 * place beside a figure is a value it does not vouch for. Which of the two a
 * scope is has already been decided by `capsOf` (`spend.ts`'s closed `SCOPES`
 * map, read through `scope.known`), so this file reads the answer rather than
 * keeping a second list of the four.
 */
const UNKNOWN_SCOPE_WORD = 'unknown'

/** COLUMNS is the table: the repository, its two caps, the money against them
 * and the kill state. The two figures the caps are counted against
 * (`counted_usd` and `headroom_usd`, both on the same `repos[]` entry) are
 * columns of their own rather than one figure, because the money counted
 * against a cap and the room left under it are the two questions an operator
 * asks one, and the document answers both.
 *
 * **`counted_usd` is the money column, under the CLI's own word for it**, and
 * never `spent_usd`: the two are different figures (the CLI's own `show` help:
 * "Counted is spent plus what runs have reserved and not released", carried as
 * `leased_usd`), `counted_usd` is the one the caps and the headroom beside it
 * are measured against, and `spent_usd` is the billed figure
 * (`console/docs/json-contract.md`, and the caps panel draws the same two with
 * `CAPS_COUNTED_WORD` and `CAPS_BILLED_WORD`). A column headed "spent" drawing
 * the counted figure would report a lease as money charged.
 *
 * A cell's class is a fact about the column (the descriptor's own rule): a
 * repository name is a `repository`, a cap and a money figure are `label`
 * (short rendered fields), and the kill cell is a `sentence` -- it carries the
 * document's own reason text beside its attestation, and free text is what the
 * `sentence` class is for. */
const COLUMNS: readonly ManageColumn[] = Object.freeze([
  { key: 'repository', title: 'Repository', class: 'repository' },
  { key: 'daily', title: 'Daily cap', class: 'label' },
  { key: 'perRun', title: 'Per-run cap', class: 'label' },
  { key: 'counted', title: 'Counted today', class: 'label' },
  { key: 'left', title: 'Left today', class: 'label' },
  { key: 'kill', title: 'Kill switch', class: 'sentence' },
])

/** SPEND_LINK is the kind's one action: the Spend tab, which is where the
 * report behind these figures lives (`entry.ts`'s `VIEWS` id `spend`, the
 * design's "with a link to the Spend report"). */
const SPEND_LINK: readonly ManageAction[] = Object.freeze([
  { kind: 'open-view', view: 'spend' },
])

// ---------------------------------------------------------------------------
// The kind.
// ---------------------------------------------------------------------------

/** Hidden is the answer to each of the three questions, keyed by the view so
 * that a fourth member of `REDUCED_VIEWS` is a compile error here rather than a
 * figure nobody asked about. */
type Hidden = Readonly<Record<ReducedView, boolean>>

/**
 * budgetKindFor builds the Budget kind for the installation this console is
 * driving. The compatibility is an argument rather than something `decode`
 * reads, so the gate is asked once -- where the kind is made, which is
 * `manage/kinds/index.ts`'s `KINDS` (task I-MG) -- and every row a caller draws
 * is already the withholding answer (`Hidden` above).
 */
export function budgetKindFor(compatibility: CliCompatibility): ManageKind {
  const hidden: Hidden = {
    caps: hidesView(compatibility, 'caps'),
    spend: hidesView(compatibility, 'spend'),
    kill: hidesView(compatibility, 'kill'),
  }
  return defineManageKind({
    id: 'budget',
    group: 'Budget',
    title: 'Caps and kill state',
    source: 'budget.show',
    columns: COLUMNS,
    decode: (doc) => rowsOf(doc, hidden),
    // A withheld tab is not offered at all (`entry.ts`), so the kind offers no
    // door to it either: the link is the `spend` view's own, and a reduced
    // console draws neither.
    actions: hidden.spend ? [] : SPEND_LINK,
  })
}

/** rowsOf is the whole of `decode`: the document's `repos[]`, one row each. */
function rowsOf(doc: unknown, hidden: Hidden): readonly ManageRow[] {
  return capsOf(objectAt(doc)).repos.map((row) => rowOf(row, hidden))
}

/** rowOf is one repository's row: its identity, then one cell per column. */
function rowOf(row: SpendCapRow, hidden: Hidden): ManageRow {
  return {
    // The slug is the bucket key and is on every row; `repo` is `omitempty`,
    // which is why a row can name a repository by its key alone
    // (`projects.ts`'s `repoEntryOf` reads the same two members the same way).
    key: row.slug === '' ? row.repo : row.slug,
    cells: {
      repository: row.repo === '' ? row.slug : row.repo,
      daily: hidden.caps ? BUDGET_CAPS_HIDDEN_WORD : capCell(row.dailyCap, row.dailyScope),
      perRun: hidden.caps ? BUDGET_CAPS_HIDDEN_WORD : capCell(row.perRunCap, row.perRunScope),
      counted: hidden.spend ? BUDGET_SPEND_HIDDEN_WORD : formatReading(row.counted, 'usd'),
      left: hidden.spend ? BUDGET_SPEND_HIDDEN_WORD : formatReading(row.headroom, 'usd'),
      kill: hidden.kill ? BUDGET_KILL_HIDDEN_WORD : killCell(row.kill),
    },
  }
}

/** capCell is one cap: the figure through the console's one money formatting
 * (`formatReading`, so a cap the document does not carry is `n/a` and never
 * `$0.00`) and the scope it binds at, in the CLI's own four words. */
function capCell(cap: Reading, scope: SpendScope): string {
  const word = scope.known ? scope.source : UNKNOWN_SCOPE_WORD
  return `${formatReading(cap, 'usd')} (${word})`
}

/** killCell is a repository's kill state: the word the screens use, then the
 * three facts the document has. A kill's keys are `omitempty`, so a kill with
 * no operator, moment or reason draws the ones it has and invents none of the
 * others -- the sentence the Live screen's banner and the Spend screen's cell
 * draw, in one cell because this table has one column for it. */
function killCell(kill: KillView | null): string {
  if (kill === null) return LIVE_WORD
  const facts = [
    kill.by === '' ? '' : `by ${kill.by}`,
    kill.at,
    kill.reason,
  ].filter((fact) => fact !== '')
  return [KILLED_WORD, ...facts].join(' ')
}

/** objectAt is the decoded document, or null when the read answered a shape
 * that is not a document: `capsOf` reads a missing document as an empty one, so
 * a row-less table is what both cases draw. It is the local reader every screen
 * keeps (`projects.ts`, `spend.ts`), named the same way so a reader looking for
 * it finds it. */
function objectAt(raw: unknown): JSONDoc | null {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null
  return raw as JSONDoc
}
