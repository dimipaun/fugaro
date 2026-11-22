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
import type { GateAction } from '../gating'

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
 *
 * **`actions` is the row's own, and it is a member of the row for a reason.**
 * The command D2903 puts on the clipboard names the row it came from -- `fugaro
 * project repos rm github/acme/app` is one repository's, `fugaro user grant
 * ana@example.invalid` is one person's -- and the value it names is a value of
 * that row's document, which the kind is the only thing holding. A command
 * declared on the kind alone could only be one that names no repository, person
 * or key at all, so a kind whose rows each need their own declares them here
 * (MO-C and MO-P are the first), and a row that leaves the member out draws the
 * kind's own `actions` and nothing else (MG0's descriptor, whose `actions` is
 * what a kind offers beside every row it decodes).
 */
export interface ManageRow {
  readonly key: string
  readonly cells: Readonly<Record<string, string>>
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
   * consequence is the fixed sentence the shell draws beside the Copy, and the
   * empty string for a command that destroys nothing (`secrets set`, an
   * enrolment).
   *
   * It is the console's own words and never a document's sentence:
   * `buildCopyCommand` takes it as an argument, so the composition is text a
   * kind's author wrote. The one part of it a document supplies is a value the
   * sentence names -- the repository a removal is about, the allow-list entry
   * that still admits it -- and a kind reads such a value through its
   * `TEXT_CLASSES` class before it interpolates it (`manage/kinds/repos.ts`),
   * because the copy alphabets are about a word a shell will run and this is a
   * sentence a person reads. What it must state is the CLI's own account of what
   * the command does -- spec section 8
   * asks a destructive action to state its consequences, and D2909 gives a
   * removal no confirmation window to state them in: the operator's next step
   * after the copy is the terminal, so the sentence travels with the command.
   * The argv beside it is what a person runs; this is what they read first.
   */
  readonly consequence: string
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
 * WriteField is one value a write action's form collects, before any child
 * exists: the key the operation's own payload carries it under (`id`, `route`,
 * `priceIn`), and the console's own label for the box. Every one of them is
 * **text the operator types**, so there is no class here and nothing to
 * sanitize: a value a document sent never reaches this shape, and `main` holds
 * each of them to its own grammar before it builds an argv (`.claude/rules/
 * console.md`: a new argument is security-sensitive, and the grammars are
 * `ipc.ts`'s).
 *
 * `required` is what the form's own button is drawn on: a required box that is
 * empty leaves the submit disabled, and an optional one that is empty sends no
 * member at all rather than an empty string. It is a **pre-check for the
 * button** and never the decision -- the CLI refuses what it refuses, and its
 * sentence is what the operator reads (`confirm.ts`'s argument for R5 is the
 * same one).
 */
export interface WriteField {
  readonly key: string
  readonly label: string
  readonly required: boolean
}

/**
 * WriteAction is a write the console performs itself, and it is the one action
 * that opens SU1's window. The task that admits a write adds one of these to its
 * kind (MW1's `models add`, MW2's `image add`; the plan's MWR), together with its
 * own review.
 *
 * **The action carries no command and no consequence**, unlike
 * `CopyCommandAction`: the console runs this one, so the argv is main's, built
 * from the values this form collected and frozen in the window instance that
 * showed it (`src/main/modelsadd.ts`). A descriptor that spelled the line would
 * be a second answer to what is spawned, and rule 11's promise is that the
 * window shows the argv rather than a description of it.
 *
 * **`plan` is the operation the form submits to**, and it is the *plan* one
 * (`models.add.plan`): the window the operator confirms is opened by main from
 * the dry run's own document, and the write that follows is main's on the
 * window's own yes -- a renderer cannot reach it. `gate` is the action RB4 gates
 * it by, asked at drawing time, and `fields` is the form the values are typed
 * into.
 */
export interface WriteAction {
  readonly kind: 'write'
  readonly plan: string
  readonly label: string
  readonly gate: GateAction
  readonly fields: readonly WriteField[]
}

/**
 * ManageAction is what a kind offers beside a row: a command to copy, a view to
 * open, or a write the console performs behind SU1's window. A kind below the
 * floor of a write's own CLI shows the Copy-command instead of the write, and
 * that is the kind's own decision at the moment it is built -- the action it
 * hands the shell is one or the other, never both.
 */
export type ManageAction = CopyCommandAction | OpenViewAction | WriteAction

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
