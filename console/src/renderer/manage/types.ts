// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Manage section's kind descriptor: the one shape a kind file writes and the
// shell draws (the console-improvements plan's task MG0; spec section 5 as the
// reconciliation amended it -- D2903, D2905 and D2909; the prose is
// `docs/console-improvements/manage-kinds.md`).
//
// **A descriptor, and not a contract.** The console's contract is
// `console/docs/json-contract.md`, which is about what the CLI may send and what
// the console may read back; a Manage kind is neither. It is the console's own
// description of one list it draws from a read the CLI already answers -- which
// document, which columns, and how a row of it becomes cells -- and every part of
// it is inside this process. That is what makes a fifth kind, or a column moved
// from one kind to another, an ordinary change rather than a format change: no
// reader outside this binary consumes a descriptor, so no member of one is
// versioned (unlike the rule `CLAUDE.md` states for a wire shape).
//
// **One kind, one file.** Every kind lives in `manage/kinds/` -- `people.ts`,
// `models.ts`, `keys.ts`, `budget.ts`, `repos.ts`, `images.ts`, `recipes.ts` --
// and `manage/kinds/index.ts` lists them in the group order this file exports.
// The shell (`manage/shell.ts`) draws the groups, one section per kind, a table
// per kind's `columns`, and it draws **only what a kind decodes**: it knows no
// document of its own, reads no key by name, and holds no sentence about a row.
// Everything a kind's read means -- which document, what a row is, what may be
// copied from it -- is in that kind's file, which is what keeps a new kind from
// reaching into the shell.

import type { TextClass } from '../../shared/sanitize'

/**
 * MANAGE_GROUPS is the spec's section 5 grouping, in the order the Project tab
 * draws it: the owner's four names, and the order is part of the descriptor
 * because the shell sorts kinds by it and a kind file must not be able to
 * disagree with its neighbours about where its own group sits.
 */
export const MANAGE_GROUPS = Object.freeze([
  'People & access',
  'Code',
  'Models & keys',
  'Budget',
] as const)

export type ManageGroup = (typeof MANAGE_GROUPS)[number]

/**
 * ManageColumn is one column of a kind's table. `key` is the name a row's
 * `cells` is keyed by and `title` is the heading drawn above it -- the console's
 * own words, both of them.
 *
 * **`class` is not optional.** Every cell is a string a document sent, so every
 * cell has to pass `sanitizeText` before it is drawn (`.claude/rules/console.md`,
 * design section 5.1 rule 7), and the class it passes under is a fact about the
 * column and never about the value: a member's address is a `person` and a
 * repository's name is a `repository` because that is what the column *is*. A
 * column that could not name its class would be a value the shell has to guess
 * about, and a guess is what the classes exist to rule out.
 */
export interface ManageColumn {
  readonly key: string
  readonly title: string
  readonly class: TextClass
}

/**
 * ManageRow is one row a kind decoded: `key` identifies it among the rows of one
 * read (the CLI's own id, address or name where it gives one) and `cells` is one
 * string per column, keyed by `ManageColumn.key`.
 *
 * The row carries no status of its own: "status" in the spec's list view is a
 * column like any other, so a kind that has one names it in `columns` and fills
 * it here, and a kind whose document has none does not draw one.
 */
export interface ManageRow {
  readonly key: string
  readonly cells: Readonly<Record<string, string>>
  /**
   * actions are the commands this row alone is acted on with, and they are where
   * a read that is a LIST of objects says what happens to one of them (spec
   * section 5; MO-P's revoke and removal, MO-C's repository remove).
   *
   * A command that acts on one row cannot live in `ManageKind.actions`: a revoke
   * names one person's role and scope pair and an account's removal names one
   * account, so a kind-level command could only carry placeholders, which is a
   * command the operator has to repair by hand and exactly what a Copy-command
   * exists not to be. The kind-level list keeps the commands that are NOT about
   * a row -- adding a pair, adding an account -- and those are the ones whose
   * values only the operator knows.
   *
   * Absent (not empty) for a row no command is offered on: a member this
   * machine's own lists hold as a group is not one `fugaro user offboard` names,
   * and a row says so by having no action rather than by an action that would
   * fail. The shell draws what is here and never an action of its own.
   */
  readonly actions?: readonly ManageAction[]
}

/**
 * CopyCommandAction is the action on every write D2903 keeps out of the console:
 * the exact argv to put on the clipboard, which the operator runs in a terminal
 * themselves. The words are built by `command.ts` (`buildCopyCommand`), which
 * refuses any value that is not a plain word, so a row's text can never put a
 * second command on the clipboard. The shell copies it with `copyCommandText`,
 * which is the argv joined with single spaces (one word, one shell word), and
 * never quotes it again.
 */
export interface CopyCommandAction {
  readonly kind: 'copy-command'
  readonly argv: readonly string[]
  /**
   * consequence is what the command will do that the operator cannot see from
   * its line, in **the CLI's own words** (spec section 8: a destructive action
   * states its consequences, and the sentence the console shows is the one the
   * command's own help prints -- MO-P's revoke, offboard and `accounts rm`).
   *
   * It is a fixed sentence rather than a sentence a kind composed, and the test
   * that pins a kind's actions holds it to that: each one is a run of the
   * command's own help text, so the console cannot come to say something about a
   * command that the command does not say. A copy-command with nothing to state
   * -- adding a pair, adding an account -- has no `consequence`, and the field's
   * absence is what the shell draws nothing from: an empty string would be a
   * sentence that failed to load.
   */
  readonly consequence?: string
}

/**
 * OpenViewAction is a link from a kind to a view of the console's own rail -- the
 * Budget kind opens the Spend tab (MO-B, D2909). `view` is a `VIEWS` id
 * (`src/renderer/entry.ts`): the shell asks the entry to show it, and no kind
 * admits a view the rail does not have.
 */
export interface OpenViewAction {
  readonly kind: 'open-view'
  readonly view: string
}

/**
 * ManageAction is what a kind offers beside a row. A **write** action -- a
 * console control that runs a command itself rather than copying it -- is added
 * by the task that admits it (MW1's `models add` window, MW2's `image add`),
 * together with its own review; the first pass has these two and nothing else, so
 * this union is the whole of the action vocabulary today.
 */
export type ManageAction = CopyCommandAction | OpenViewAction

/**
 * ManageKind is one list the Manage section draws. `source` is the console
 * operation's own name (`members.list`, `budget.show`), which is the read whose
 * answer `decode` is handed: the shell reads the op and decodes its document, and
 * a kind that named an operation outside the IPC allow-list would be a section
 * that can only ever refuse.
 *
 * `decode` is where the document stops being `unknown`. It reads the keys it
 * knows by name and answers the rows they make -- one cell per column it
 * declared -- and it is the only place a kind's shape is known: the shell draws
 * the answer, and a member the kind did not read is not drawn rather than drawn
 * as whatever was in the document.
 */
export interface ManageKind {
  readonly id: string
  readonly group: ManageGroup
  readonly title: string
  readonly source: string
  readonly columns: readonly ManageColumn[]
  decode(doc: unknown): readonly ManageRow[]
  readonly actions: readonly ManageAction[]
}

/**
 * defineManageKind is how a kind file declares its kind, and it refuses a kind
 * whose group is not one of the four.
 *
 * The refusal is a check rather than only a type because `ManageGroup` is erased
 * at run time: a descriptor assembled where the compiler cannot see it -- a test
 * fixture, a kind built from a table -- would otherwise land in a group the shell
 * has no place for, and the shell's answer to that is to draw nothing, which is a
 * kind missing from the Project tab with no sentence saying why. So the group is
 * checked where the kind is defined, and a mistake is a refusal there instead of
 * a gap on a screen. The message names the group and the four, so the fix is in
 * the sentence.
 */
export function defineManageKind(kind: ManageKind): ManageKind {
  if (!MANAGE_GROUPS.includes(kind.group)) {
    throw new Error(
      `the Manage kind ${kind.id} is in the group ${String(kind.group)}, which is not one of the four this console draws (${MANAGE_GROUPS.join(', ')})`,
    )
  }
  return kind
}
