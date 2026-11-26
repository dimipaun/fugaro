// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Manage section's shell: the four accordion groups, one section per kind,
// and only what a kind decodes (the console-improvements plan's task MO-SH;
// reconciliation R3; decisions D2903 and D2909; the prose is
// `docs/console-improvements/manage-kinds.md`).
//
// **What the shell knows, and what it does not.** It knows the four group names
// and their order (`MANAGE_GROUPS`, MG0), the shape of a kind's descriptor, and
// the four things it draws from one: a heading, a table of the kind's own
// columns, the rows `decode` answered with, and the actions the kind offers. It
// knows **no document**: it reads no key of the read's answer by name, holds no
// column of its own and no sentence about a row, and draws a string only where a
// kind put one. That is what makes a fifth kind, or a column moved from one kind
// to another, a change in that kind's own file -- and what makes the failure this
// shell must not have (a kind's document misread into a figure nobody checked)
// impossible here rather than merely unlikely.
//
// **Every kind is a section of its own** (`.claude/rules/console.md`, the
// owner's rule of 2026-10-31). One read, one busy mark, one refusal: the groups
// are opened independently, each kind's read starts on its own, and a kind that
// refuses is drawn as a refusal **under its own heading** while its neighbours
// keep the rows they already have. Nothing here waits for the last read, and no
// answer is redrawn all at once: the busy mark of the kind being read is drawn at
// the top of that kind's section, and the reads start in parallel within
// whatever limit `src/main` puts on children.
//
// **"Only what is on screen" is the same rule read from the other side.** A
// group is collapsed by default, and a collapsed group's kinds are read when the
// operator opens it and not before: seven list reads (the plan's own count for
// the four groups) are seven children of the CLI, and spending them on lists
// nobody asked to see would be the tab paying for the whole section on every
// visit. The disclosure is a real `details`/`summary`, so the state is the
// element's own `open` attribute -- no class carries it, no preference is read
// and no motion is the shell's. `read()` and `readKind()` are the two doors a
// caller with its own reason to read (a write landing, a refresh of the whole
// section) opens.
//
// **The stamp and the Retry are `section.ts`'s, not a second drawing of them.**
// A kind that has landed wears `renderStamp`'s "as of HH:MM" and its Refresh
// control -- the same line every cached view of this console wears, because the
// fact is the same one: when this list was read. A kind that was refused wears
// the CLI's own sentence and `renderRetry`, which is R1's control and the
// section's own door back. Both controls re-spawn **that kind's** read and
// nothing else.
//
// **The refusal names its own read.** The console's operation behind a kind is
// the kind's `source` (`members.list`) and not a command line: the shell has no
// table of op names to argv, and inventing `fugaro members.list` would name a
// command nobody can run. So the operation is drawn on the kind's own element
// (`data-operation`, as `managed.ts` does) and on the Retry control, and a
// refusal the CLI did not word itself -- a read that threw nothing readable --
// says so in the console's own sentence, which names the operation. An operator
// holding a refusal therefore has the whole of what this console knows.
//
// **The empty state is one sentence, and it is the shell's.** The descriptor
// carries no wording per kind, and a sentence the shell assembled out of a
// kind's title ("No members yet") would be a sentence the shell wrote about a
// document it did not read. One plain line says the fact that is true of every
// kind: the read answered and the list is empty.
//
// **Two things this shell deliberately does not decide**, both recorded in the
// pull request that landed it. A kind's `actions` are drawn **once per kind**,
// under the table, and not once per row: the descriptor's action carries the
// exact argv and no per-row value, so the same control beside every row would be
// the same command drawn N times. A kind that needs a copy command naming a row
// (a member's address, a key's name) needs a descriptor member for it, which
// MG0 does not carry. And **reduced mode is not asked here**: no member of the
// descriptor says which figures a kind's rows hold, so the shell has nothing to
// ask `hidesView` about -- a kind whose list carries spend, caps or kill figures
// withholds them in its own drawing (MO-B), and a shell-level answer to that
// question is a descriptor change with its own task.

import { sanitizeRefusal, sanitizeText } from '../../shared/sanitize'
import type { SkewTheme } from '../../shared/version'
import { putText } from '../app'
import type { ElementLike } from '../app'
import type { GateAction, GateDecision } from '../gating'
import { BUSY_SLOT_CLASS, renderRetry, renderStamp, sectionRead } from '../section'
import type { SectionElement, SectionRead, SectionTimers } from '../section'
import type { SkeletonBlock, SkeletonShape } from '../skeleton'
import { disabledWrite } from '../writes'
import { copyCommandText } from './command'
import { MANAGE_GROUPS } from './types'
import type { ManageAction, ManageKind, ManageRow, WriteAction } from './types'

// ---------------------------------------------------------------------------
// The element surface, and the caller's four doors.
// ---------------------------------------------------------------------------

/**
 * ManageElement is what the shell draws into. It is `SectionElement` plus
 * `replaceChildren` and `addEventListener`, and both of those are here for one
 * reason each.
 *
 * **`replaceChildren` is how a section is repainted.** The reads of this section
 * land at different moments -- that is the whole of the rule above -- so the
 * drawing of a kind has to replace that kind's own body and the busy mark in its
 * slot without touching a neighbour, and `replaceChildren` is the entry's own
 * door for exactly that (`surface.nodeOf(slot).replaceChildren()`).
 *
 * **`addEventListener` is how the shell owns its own interaction.** The
 * convention in this tree is that a *screen* draws and the entry wires, because
 * the entry is the only place with a document; this module is not a screen but
 * the Manage section itself, and its three interactions -- opening a group,
 * re-reading one kind, copying a command -- are the section's own, exactly as
 * the picker's and the confirmation window's are theirs (`installations.ts`,
 * `confirm.ts`). Nothing else about the DOM is touched: no attribute a screen
 * may not set (`test/unit/screen-literals.test.ts`), no navigation, no inline
 * style.
 */
export interface ManageElement extends ElementLike {
  readonly tag: string
  append(child: ManageElement): void
  replaceChildren(...children: ManageElement[]): void
  addEventListener(type: string, listener: () => void): void
}

/** ManageFactory is where a section's elements come from. */
export interface ManageFactory {
  create(tag: string): ManageElement
}

/**
 * ManageControlElement is the slice of an entry form's control this section
 * touches: the value the operator typed, and the one property that gates the
 * submit. It is declared here rather than added to `ManageElement` on purpose --
 * `ManageElement` is what every node of this section is, and a text box is not
 * one of those: a shell that could read a value off any element it draws would
 * be a shell that could read one off a heading.
 */
export interface ManageControlElement extends ManageElement {
  value: string
  disabled: boolean
}

/**
 * ManageDeps is what the caller hands the section: where elements come from,
 * the theme the busy mark is drawn with, the read, the role the writes are
 * asked about, and the two things the console does on the operator's behalf.
 *
 * **The read is handed in rather than imported.** The shell spawns nothing: the
 * caller's `read` is the console's own operation surface (`entry.ts`'s bridge),
 * which is the one place that knows the allow-lists, and the argument is an
 * operation name from a kind's `source` -- a name the console's own type already
 * holds. `copy` is the clipboard and `openView` is the rail; both are the
 * caller's, so this module touches neither `navigator` nor the navigation.
 */
export interface ManageDeps {
  readonly factory: ManageFactory
  readonly theme: SkewTheme
  readonly read: (operation: string, args: Readonly<Record<string, unknown>>) => Promise<unknown>
  readonly copy: (text: string) => void
  readonly openView: (view: string) => void
  /**
   * can is RB4's read of the caller's own role, asked once per write control as
   * it is drawn (`gating.ts`'s `Gate.can`) -- the section asks about the action
   * it is about to offer and never about a role, which is F19's rule.
   *
   * **It is required, and no caller may hand nothing.** A section that drew a
   * write control without a role read would offer the write to a role the CLI
   * refuses, and the models write is the one the CLI cannot correct: `models add`
   * has no role check of its own to fall back on (U5 of the reconciliation), so
   * an unwired gate there is a write offered to everybody. A caller that read no
   * role says so with a value -- `gateFrom(null).can`, which allows every action
   * and is exactly `gating.ts`'s "no answer" case -- rather than by leaving the
   * member out, so the decision is the caller's and read at the one place it is
   * made. The tab that draws this section is the caller
   * (`screens/projects.ts`), and the role read it hands is the entry's
   * (`entry.ts`'s `roles`).
   */
  readonly can: (action: GateAction) => GateDecision
  readonly now?: () => number
  readonly timers?: SectionTimers
}

/** ManageSection is the drawn section and the three things a caller may ask of
 * it afterwards: read everything, re-read one kind, and stop (a tab the
 * operator has left). */
export interface ManageSection {
  readonly root: ManageElement
  /** read starts every kind's own read, in parallel. */
  read(): void
  /** readKind re-reads one kind, which is what its Retry and Refresh controls
   * do and what a caller with a reason of its own may do. A kind id no kind of
   * this section has is ignored rather than thrown at: the caller is a button's
   * handler, and a section that threw there would take the tab down. */
  readKind(id: string): void
  /** end clears every kind's busy state without settling it, for a screen the
   * operator has left. */
  end(): void
}

/** ManageMount is what `mountManage` answers: the kinds are fixed when the
 * section is built, and `draw` puts them into a caller's tree once, with that
 * caller's factory, theme and reads. */
export interface ManageMount {
  draw(deps: ManageDeps): ManageSection
}

// ---------------------------------------------------------------------------
// The section's own vocabulary.
// ---------------------------------------------------------------------------

/** MANAGE_CLASS is the section's root: the four groups and nothing else. */
export const MANAGE_CLASS = 'manage-section'
/** MANAGE_GROUP_CLASS, MANAGE_GROUP_TITLE_CLASS and MANAGE_GROUP_BODY_CLASS
 * are one group: the `details` disclosure, its `summary` heading and the column
 * of kinds under it. The `open` attribute the browser puts on the disclosure is
 * the whole of the group's state. */
export const MANAGE_GROUP_CLASS = 'manage-group'
export const MANAGE_GROUP_TITLE_CLASS = 'manage-group-title'
export const MANAGE_GROUP_BODY_CLASS = 'manage-group-body'
/** MANAGE_KIND_CLASS is one kind's own section: the unit every rule above is
 * about -- one read, one busy mark, one refusal. */
export const MANAGE_KIND_CLASS = 'manage-kind'
export const MANAGE_KIND_TITLE_CLASS = 'manage-kind-title'
/** MANAGE_BODY_CLASS is the kind's content, replaced whole each time its read
 * lands. The busy mark is **not** in here: it goes in the slot above, so it
 * stays at the top of the section it belongs to. */
export const MANAGE_BODY_CLASS = 'manage-body'
export const MANAGE_TABLE_CLASS = 'manage-table'
export const MANAGE_COLUMN_CLASS = 'manage-column'
export const MANAGE_ROW_CLASS = 'manage-row'
export const MANAGE_CELL_CLASS = 'manage-cell'
/** MANAGE_ACTIONS_CLASS is the line of controls the kind offers, and
 * MANAGE_COPY_CLASS and MANAGE_VIEW_CLASS are its two kinds of control. */
export const MANAGE_ACTIONS_CLASS = 'manage-actions'
export const MANAGE_COPY_CLASS = 'manage-copy'
export const MANAGE_VIEW_CLASS = 'manage-view'
/** MANAGE_COPY_BOX_CLASS and MANAGE_COPY_CONSEQUENCE_CLASS are the Copy-command
 * that states a consequence: the pair the command is drawn in -- the button, and
 * the sentence under it (`CopyCommandAction`'s `consequence`). A command with no
 * consequence is the bare button, with neither of these drawn. */
export const MANAGE_COPY_BOX_CLASS = 'manage-copy-box'
export const MANAGE_COPY_CONSEQUENCE_CLASS = 'manage-copy-consequence'
/** MANAGE_WRITE_CLASS is the control a write action is drawn as -- the button
 * the operator presses to open the form, and the button that submits it -- and
 * MANAGE_WRITE_BOX_CLASS, MANAGE_WRITE_FORM_CLASS, MANAGE_WRITE_FIELD_CLASS,
 * MANAGE_WRITE_INPUT_CLASS and MANAGE_WRITE_CANCEL_CLASS are the rest of that
 * drawing: the write's own box in the actions line, the form inside it, one row
 * per field, the box a value is typed into, and the way out. */
export const MANAGE_WRITE_CLASS = 'manage-write'
export const MANAGE_WRITE_BOX_CLASS = 'manage-write-box'
export const MANAGE_WRITE_FORM_CLASS = 'manage-write-form'
export const MANAGE_WRITE_FIELD_CLASS = 'manage-write-field'
export const MANAGE_WRITE_INPUT_CLASS = 'manage-write-input'
export const MANAGE_WRITE_CANCEL_CLASS = 'manage-write-cancel'
/** MANAGE_REFUSAL_CLASS is the CLI's own sentence for a read that did not
 * answer, and MANAGE_EMPTY_CLASS the console's sentence for a read that
 * answered with nothing. They are two elements because they are two facts. A
 * refused **plan** is drawn as the first of them, inside the write's own box. */
export const MANAGE_REFUSAL_CLASS = 'manage-refusal'
export const MANAGE_EMPTY_CLASS = 'manage-empty'

/** MANAGE_KIND_ATTRIBUTE is the kind's own id on its section, and
 * MANAGE_OPERATION_ATTRIBUTE the console operation its read names. The second is
 * what makes a refusal's section self-describing: a reader of the markup -- or a
 * test, or an operator filing a report -- can see which read this list is. */
export const MANAGE_KIND_ATTRIBUTE = 'data-kind'
export const MANAGE_OPERATION_ATTRIBUTE = 'data-operation'
/** MANAGE_GROUP_ATTRIBUTE is the group's own name on its disclosure, in the
 * console's four words (`MANAGE_GROUPS`). It is drawn rather than slugged: a
 * second spelling of the four names is a second place they can disagree. */
export const MANAGE_GROUP_ATTRIBUTE = 'data-group'
/** MANAGE_ROW_ATTRIBUTE and MANAGE_COLUMN_ATTRIBUTE are the row's identity
 * among the rows of one read and the column a cell belongs to. The column's is
 * the kind's own key and cannot be anything else; the row's is a **document**
 * string, so it passes the sanitizer before it is an attribute (see
 * `rowKey`). */
export const MANAGE_ROW_ATTRIBUTE = 'data-row'
export const MANAGE_COLUMN_ATTRIBUTE = 'data-column'

/** MANAGE_EMPTY_SENTENCE is the one plain line an empty kind says. It is the
 * console's own vocabulary, drawn through `literal`, and it names no kind and no
 * column: see the header on why it is not built out of a kind's title. */
export const MANAGE_EMPTY_SENTENCE = 'Nothing listed yet'

/** MANAGE_BUSY_LEAD opens the busy mark's label, which names the kind being
 * read ("Reading Members…"): the mark says *what* is being read, which is the
 * fact an operator waiting on it needs. */
export const MANAGE_BUSY_LEAD = 'Reading'

/** MANAGE_VIEW_LEAD and MANAGE_VIEW_TAIL bracket a view's id in the label of
 * the control that opens it: "Open the spend view". The id is the console's own
 * (`VIEWS` in `entry.ts`), a word the entry answers or refuses, and this is the
 * only spelling of the label. */
export const MANAGE_VIEW_LEAD = 'Open the'
export const MANAGE_VIEW_TAIL = 'view'

/** MANAGE_WRITE_SUBMIT is the form's own button: it runs the write's dry run,
 * and the window main opens from that document is where anything is confirmed.
 * A second word rather than the write control's own label, because the two
 * presses are two steps -- one opens a form, the other asks the CLI what the
 * values would change -- and the operator reads which one they are on. */
export const MANAGE_WRITE_SUBMIT = 'Continue'

/** MANAGE_WRITE_CANCEL is the form's way out, and it is the same word the
 * confirmation window's own cancel carries (`confirm.ts`'s `CANCEL_LABEL`)
 * because it is the same fact: a form the operator has decided not to submit.
 * It is spelled here rather than imported so that this module keeps drawing the
 * section's own vocabulary and nothing of a window's. */
export const MANAGE_WRITE_CANCEL = 'Cancel'

/** MANAGE_SKELETON_BLOCKS is the shape a kind's list is drawn as while its
 * first read is in flight: the heading line the kind draws, then the rows of the
 * table. The count is approximate on purpose -- the rows come from a document
 * that has not answered -- and it is here so the page does not jump when the
 * read lands. It is drawn **only** for a kind that has never been filled, which
 * is what tells a first read apart from a re-read (`sectionRead.mark`). */
export const MANAGE_SKELETON_BLOCKS: readonly SkeletonBlock[] = Object.freeze<SkeletonBlock[]>([
  'line',
  'row',
  'row',
  'row',
])

// ---------------------------------------------------------------------------
// Drawing.
// ---------------------------------------------------------------------------

/** literal is this module's one writer of a text slot, in `section.ts`'s
 * sense: every argument it is handed is the console's own vocabulary -- a kind's
 * heading, a column's title, a group's name, or a constant above. A document's
 * string never reaches it; those go through `putText`. */
function literal(element: ManageElement, text: string): ManageElement {
  element.textContent = text
  return element
}

/** make draws one element of the console's own tag and classes. */
function make(factory: ManageFactory, tag: string, ...classes: string[]): ManageElement {
  const element = factory.create(tag)
  if (classes.length > 0) element.setAttribute('class', classes.join(' '))
  return element
}

/** asManage re-widens an element `section.ts` handed back. Its helpers are typed
 * against `SectionElement` -- the three attributes and `append` a mark, a stamp
 * and a Retry need -- which is deliberately a smaller surface than a caller's
 * factory draws; the element is the caller's own, made by its own `create`. The
 * entry takes the same step in the other direction, through `surface.nodeOf`,
 * for the same reason: a shared primitive's drawing and the surface it was
 * drawn on are two facts, and this is where they meet. */
function asManage(element: SectionElement): ManageElement {
  return element as ManageElement
}

/** KindFrame is one kind's drawing: the section that wears the busy state, the
 * slot the mark goes in, the body its content is replaced in, and that kind's
 * own read lifecycle. */
interface KindFrame {
  readonly kind: ManageKind
  readonly root: ManageElement
  readonly slot: ManageElement
  readonly body: ManageElement
  readonly read: SectionRead
  /** landed is whether this kind's read has ever answered. It is what tells a
   * first read from a re-read, which is the one thing the skeleton is drawn on. */
  landed: boolean
}

/** orderedKinds is the kinds in the order the section draws them: the four
 * groups in `MANAGE_GROUPS`' order, and within a group the order the caller
 * listed them in -- which is `manage/kinds/index.ts`'s, the plan's own order.
 * The sort is explicit rather than assumed: a kind list that arrived grouped
 * differently would otherwise draw the groups in whatever order it was written. */
function orderedKinds(kinds: readonly ManageKind[]): readonly ManageKind[] {
  return [...kinds].sort(
    (left, right) => MANAGE_GROUPS.indexOf(left.group) - MANAGE_GROUPS.indexOf(right.group),
  )
}

/** busyLabel is the mark's own label for one kind: what is being read. */
function busyLabel(kind: ManageKind): string {
  return `${MANAGE_BUSY_LEAD} ${kind.title}…`
}

/** skeletonFor is the shape one kind's section holds while it is first read.
 * `section` names the kind, so a reader of the tree -- or a stylesheet -- can
 * tell which section a skeleton belongs to. */
function skeletonFor(kind: ManageKind): SkeletonShape {
  return { section: `manage-${kind.id}`, blocks: MANAGE_SKELETON_BLOCKS }
}

/** becauseOf is the CLI's own sentence out of a failed read, or null when the
 * refusal carried nothing this console can read.
 *
 * The sentence is read **structurally** (`sentence`) because that is the shape a
 * refused read really arrives in: the caller's read is the console's operation
 * surface, and the entry throws the CLI's reply as a plain `{ sentence }` rather
 * than as an `Error` (`entry.ts`'s readers, and `readSpend`'s `refusalSentence`,
 * which reads the same member for the same reason). An `Error` is read too, for
 * a failure that really is one. A thrown value with neither is not a sentence,
 * and `String(it)` would draw whatever a hostile object's `toString` returned. */
function becauseOf(error: unknown): string | null {
  const raw = (error as { readonly sentence?: unknown } | null | undefined)?.sentence
  if (typeof raw === 'string' && raw.trim() !== '') return raw
  return error instanceof Error && error.message.trim() !== '' ? error.message : null
}

/**
 * refusalSentence is what a refused kind says. The CLI's own sentence is the
 * remedy the operator acts on, so it is drawn when there is one, sanitized
 * through `sanitizeRefusal` -- the one door for a refusal, which redacts a
 * credential a child may have echoed and leaves the remedy as the CLI wrote it
 * (the placeholder rules that used to redact an address or a repository name out
 * of one went with D3213, 2026-11-21). When there is none, the console says the
 * one thing it knows, naming the read.
 *
 * The refusal **never carries a value the read refused**: a sentence with
 * nothing readable in it is replaced wholesale rather than patched.
 */
function refusalSentence(kind: ManageKind, because: string | null): string {
  if (because !== null) {
    const drawn = sanitizeRefusal(because, 'the reason this list has nothing to draw')
    if (drawn.ok && drawn.text.trim() !== '') return drawn.text
  }
  return `no document arrived for the read \`${kind.source}\``
}

/**
 * rowKey is a row's identity as an attribute value, or null when the sanitizer
 * refuses it. The key is a document's string -- the CLI's own id, address or
 * name -- and it is drawn as an attribute, so it goes through the same door
 * every other document string does (`projects.ts`'s `sanitizedKey`). A key this
 * console will not draw is a row drawn without the attribute: the row itself is
 * still drawn, because the cells are what the operator reads and the key is
 * bookkeeping.
 */
function rowKey(raw: unknown): string | null {
  const drawn = sanitizeText(raw, 'label', 'a row key')
  return drawn.ok && drawn.text !== '' ? drawn.text : null
}

/** tableOf draws one kind's rows: the heading row the kind's own `columns`
 * name, and one row per row the kind decoded. Every cell is a document's string
 * and goes through `putText` under the class its **column** declared -- the
 * class is a fact about the column and never about the value (MG0) -- and the
 * field names the wire path the value came from, so a refused cell says which
 * one it was. */
function tableOf(factory: ManageFactory, kind: ManageKind, rows: readonly ManageRow[]): ManageElement {
  const table = make(factory, 'table', MANAGE_TABLE_CLASS)
  const head = make(factory, 'thead')
  const headRow = make(factory, 'tr')
  for (const column of kind.columns) {
    const heading = make(factory, 'th', MANAGE_COLUMN_CLASS)
    heading.setAttribute('scope', 'col')
    heading.setAttribute(MANAGE_COLUMN_ATTRIBUTE, column.key)
    literal(heading, column.title)
    headRow.append(heading)
  }
  head.append(headRow)
  table.append(head)
  const body = make(factory, 'tbody')
  for (const row of rows) {
    const line = make(factory, 'tr', MANAGE_ROW_CLASS)
    const key = rowKey(row.key)
    if (key !== null) line.setAttribute(MANAGE_ROW_ATTRIBUTE, key)
    for (const column of kind.columns) {
      const cell = make(factory, 'td', MANAGE_CELL_CLASS)
      cell.setAttribute(MANAGE_COLUMN_ATTRIBUTE, column.key)
      putText(cell, row.cells[column.key], column.class, `${kind.source}[].${column.key}`)
      line.append(cell)
    }
    body.append(line)
  }
  table.append(body)
  return table
}

/** actionControl draws one of a kind's actions. A **copy-command** is a button
 * whose label *is* the command and whose whole behaviour is to put that exact
 * argv on the clipboard -- `copyCommandText`, the one place the join is
 * spelled, so nothing here quotes, escapes or reassembles it. An **open-view**
 * is a button that asks the caller to show a view of the console's own rail;
 * which views exist is the entry's business, and a view the rail does not have
 * is a view the entry does not draw. A **write** is the third arm below.
 */
function actionControl(
  factory: ManageFactory,
  action: ManageAction,
  deps: ManageDeps,
): ManageElement {
  // The three kinds of control are drawn one per arm rather than through a class
  // chosen by a ternary: each is a different control -- one copies, one asks the
  // caller to show a view, one collects values and runs a dry run -- and the arm
  // is also what lets `manage.css` qualify each hook with the tag the shell
  // really draws, which is a fact the selector scan reads out of a call whose tag
  // and class are both literals.
  if (action.kind === 'copy-command') {
    const text = copyCommandText(action)
    const copy = literal(make(factory, 'button', MANAGE_COPY_CLASS), text)
    copy.setAttribute('type', 'button')
    copy.addEventListener('click', () => deps.copy(text))
    // **The sentence travels with the command** (`CopyCommandAction`'s
    // `consequence`): what the command does that a person has to read before
    // running it is drawn beside the button rather than left in the descriptor.
    // It is the case for the below-floor copy-commands above all, where the
    // sentence is the whole of why there is no write button and what the CLI
    // still needs. A command that states nothing draws nothing but itself, so a
    // kind whose command destroys nothing keeps the bare button it had.
    if (action.consequence === '') return copy
    const box = make(factory, 'div', MANAGE_COPY_BOX_CLASS)
    box.append(copy)
    box.append(literal(make(factory, 'p', MANAGE_COPY_CONSEQUENCE_CLASS), action.consequence))
    return box
  }
  if (action.kind === 'write') return writeControl(factory, action, deps)
  const view = literal(
    make(factory, 'button', MANAGE_VIEW_CLASS),
    `${MANAGE_VIEW_LEAD} ${action.view} ${MANAGE_VIEW_TAIL}`,
  )
  view.setAttribute('type', 'button')
  view.addEventListener('click', () => deps.openView(action.view))
  return view
}

/**
 * writeControl draws one write action: the control that opens its form, and --
 * once pressed -- the form itself. **The role is asked here and the floor is
 * not**: whether this CLI can be shown the write at all is the kind's own
 * decision when it is built (`types.ts`'s `WriteAction`), and a kind below the
 * floor hands this shell a Copy-command instead, so this function is only ever
 * reached for a write this console can really perform.
 *
 * A role that may not use it draws the control through `disabledWrite` -- the
 * one function every write this console refuses goes through, with the role
 * sentence as its tooltip -- and binds nothing at all, which is the whole of
 * "disabled with the reason": the operator reads why, and there is no listener,
 * no channel and no form behind the button.
 *
 * The form submits the values to the kind's **plan** operation. What comes back
 * is main's, and main's answer to a plan is the window it opened: the document
 * is drawn there (`renderer/confirm.ts`), and this section draws none of it. A
 * refusal -- a CLI below its own floor, a value its grammar refused, a window
 * already waiting for an answer -- is drawn as the CLI's own sentence inside the
 * box, which is the same door a refused read's sentence comes through.
 */
function writeControl(
  factory: ManageFactory,
  action: WriteAction,
  deps: ManageDeps,
): ManageElement {
  const box = make(factory, 'div', MANAGE_WRITE_BOX_CLASS)
  const decision = deps.can(action.gate)
  const button = decision.allowed
    ? literal(make(factory, 'button', MANAGE_WRITE_CLASS), action.label)
    : disabledWrite(factory, MANAGE_WRITE_CLASS, action.label, null, decision)
  button.setAttribute('type', 'button')
  box.append(button)
  if (!decision.allowed) return box

  /** open replaces the control with the form, in the write's own box: one press
   * reveals the values, and Cancel puts the control back exactly as it was. */
  const open = (): void => {
    const form = make(factory, 'div', MANAGE_WRITE_FORM_CLASS)
    const controls: { readonly field: WriteAction['fields'][number]; readonly box: ManageControlElement }[] =
      []
    for (const field of action.fields) {
      const line = literal(make(factory, 'label', MANAGE_WRITE_FIELD_CLASS), field.label)
      const box_ = factory.create('input') as ManageControlElement
      box_.setAttribute('class', MANAGE_WRITE_INPUT_CLASS)
      box_.setAttribute('type', 'text')
      box_.value = ''
      line.append(box_)
      form.append(line)
      controls.push({ field, box: box_ })
    }
    const status = make(factory, 'p', MANAGE_REFUSAL_CLASS)
    status.textContent = ''

    const submit = literal(
      make(factory, 'button', MANAGE_WRITE_CLASS),
      MANAGE_WRITE_SUBMIT,
    ) as ManageControlElement
    submit.setAttribute('type', 'button')
    const back = literal(
      make(factory, 'button', MANAGE_WRITE_CANCEL_CLASS),
      MANAGE_WRITE_CANCEL,
    ) as ManageControlElement
    back.setAttribute('type', 'button')

    /** values is what the form holds now: every box with something in it, and no
     * member at all for an optional box the operator left alone. */
    const values = (): Readonly<Record<string, string>> => {
      const out: Record<string, string> = {}
      for (const control of controls) {
        if (control.box.value === '') continue
        out[control.field.key] = control.box.value
      }
      return out
    }

    /** check is the button's pre-check and never the decision: a required box
     * that is empty leaves the submit disabled, and everything else is the
     * CLI's own reader to refuse. */
    const check = (): void => {
      submit.disabled = controls.some(
        (control) => control.field.required && control.box.value.trim() === '',
      )
    }

    submit.addEventListener('click', () => {
      if (submit.disabled) return
      submit.disabled = true
      back.disabled = true
      void deps.read(action.plan, values()).then(
        () => {
          // The window main opened from the dry run's document is what the
          // operator reads next, and it is main's: this box has nothing to add
          // and nothing to redraw. **A sentence an earlier press drew is cleared
          // here**, because a refusal that no longer holds must not stand beside
          // a window main has just opened: the second press of a form whose
          // first was refused (`a window is already waiting for an answer`) ends
          // with main waiting on an answer and the box saying nothing. The form
          // stays where it is, so a refusal below leaves the values typed and
          // one press away from another try.
          status.textContent = ''
          submit.disabled = false
          back.disabled = false
        },
        (error: unknown) => {
          status.textContent = refusalSentenceOf(action, becauseOf(error))
          submit.disabled = false
          back.disabled = false
          check()
        },
      )
    })
    back.addEventListener('click', () => {
      box.replaceChildren(button)
    })
    for (const control of controls) {
      control.box.addEventListener('input', () => {
        check()
      })
    }

    check()
    form.append(submit)
    form.append(back)
    form.append(status)
    box.replaceChildren(form)
  }

  button.addEventListener('click', open)
  return box
}

/** refusalSentenceOf is the sentence a refused plan is drawn with. It is the
 * read's own door (`refusalSentence`) with the operation named rather than the
 * kind: a plan that refused names no list at all. */
function refusalSentenceOf(action: WriteAction, because: string | null): string {
  if (because !== null) {
    const drawn = sanitizeRefusal(because, 'the reason this write has nothing to show')
    if (drawn.ok && drawn.text.trim() !== '') return drawn.text
  }
  return `no plan arrived for the write \`${action.plan}\``
}

/** actionsOf draws the controls a kind offers, or nothing when it offers none.
 * They are drawn **whenever the read landed**, an empty list included: the
 * command a kind offers is about the kind and not about the rows a read happened
 * to answer with, and an operator looking at an empty list is exactly the one
 * who wants the command that fills it. A refused read draws none: nothing about
 * the list is known, and a control that acts on it would be an offer made on
 * nothing. */
function actionsOf(factory: ManageFactory, kind: ManageKind, deps: ManageDeps): ManageElement | null {
  if (kind.actions.length === 0) return null
  const line = make(factory, 'div', MANAGE_ACTIONS_CLASS)
  for (const action of kind.actions) line.append(actionControl(factory, action, deps))
  return line
}

// ---------------------------------------------------------------------------
// The section's lifecycle.
// ---------------------------------------------------------------------------

/**
 * mountManage builds the Manage section over one list of kinds.
 *
 * The kinds are fixed here and the drawing is per caller: `draw` is handed the
 * factory, the theme, the read and the clipboard, so the e2e suite against the
 * stub CLI and the real entry draw the same section over the same descriptors.
 */
export function mountManage(kinds: readonly ManageKind[]): ManageMount {
  const ordered = orderedKinds(kinds)
  return {
    draw(deps: ManageDeps): ManageSection {
      const factory = deps.factory
      const root = make(factory, 'section', MANAGE_CLASS)
      const frames: KindFrame[] = []
      const byId = new Map<string, KindFrame>()
      /** opened is the groups whose kinds have been started. It is what makes
       * the disclosure a gate rather than a repaint: an operator who folds a
       * group and opens it again meets the rows already read, not a second
       * round of children. */
      const opened = new Set<string>()

      /** begin starts one kind's own read: the frame's own token, its own busy
       * mark, and the paint of whatever comes back. */
      const begin = (frame: KindFrame): void => {
        const token = frame.read.start()
        frame.read.mark(
          token,
          frame.root,
          frame.slot,
          busyLabel(frame.kind),
          frame.landed ? undefined : skeletonFor(frame.kind),
        )
        void deps.read(frame.kind.source, {}).then(
          (doc) => {
            // A slow earlier answer must never overwrite a later one: the token
            // is the newest change's, and an older read that lands after it is
            // dropped here rather than drawn.
            if (!frame.read.settle(token)) return
            landed(frame, doc)
          },
          (error: unknown) => {
            if (!frame.read.settle(token)) return
            refused(frame, becauseOf(error))
          },
        )
      }

      /** landed paints one kind that answered. The slot is emptied of the busy
       * mark, and the body is replaced whole: the stamp, then either the table
       * or the empty line, then the kind's own actions. */
      const landed = (frame: KindFrame, doc: unknown): void => {
        let rows: readonly ManageRow[]
        try {
          rows = frame.kind.decode(doc)
        } catch {
          // A `decode` that throws is a kind refusing its own document, which is
          // the same fact to an operator as a read that failed: this list has
          // nothing to draw. The console's own sentence is the honest one -- the
          // kind's exception is not the CLI's refusal and must not be drawn as
          // one.
          refused(frame, null)
          return
        }
        frame.slot.replaceChildren()
        const stamp = renderStamp(factory, deps.now?.() ?? Date.now())
        asManage(stamp.refresh).addEventListener('click', () => begin(frame))
        const drawn: ManageElement[] = [asManage(stamp.root)]
        drawn.push(rows.length === 0 ? emptyLine() : tableOf(factory, frame.kind, rows))
        const actions = actionsOf(factory, frame.kind, deps)
        if (actions !== null) drawn.push(actions)
        frame.body.replaceChildren(...drawn)
        frame.landed = true
      }

      /** refused paints one kind that did not answer: the CLI's sentence, and
       * R1's Retry beside it. Only **this** kind's body is touched, which is the
       * whole of "one kind's refusal leaves the others drawn". */
      const refused = (frame: KindFrame, because: string | null): void => {
        frame.slot.replaceChildren()
        const line = make(factory, 'p', MANAGE_REFUSAL_CLASS)
        literal(line, refusalSentence(frame.kind, because))
        const retry = renderRetry(factory)
        retry.setAttribute(MANAGE_OPERATION_ATTRIBUTE, frame.kind.source)
        retry.addEventListener('click', () => begin(frame))
        frame.body.replaceChildren(line, retry)
      }

      const emptyLine = (): ManageElement =>
        literal(make(factory, 'p', MANAGE_EMPTY_CLASS), MANAGE_EMPTY_SENTENCE)

      const buildKind = (kind: ManageKind): KindFrame => {
        const section = make(factory, 'section', MANAGE_KIND_CLASS)
        section.setAttribute(MANAGE_KIND_ATTRIBUTE, kind.id)
        section.setAttribute(MANAGE_OPERATION_ATTRIBUTE, kind.source)
        section.append(literal(make(factory, 'h3', MANAGE_KIND_TITLE_CLASS), kind.title))
        const slot = make(factory, 'div', BUSY_SLOT_CLASS)
        section.append(slot)
        const body = make(factory, 'div', MANAGE_BODY_CLASS)
        section.append(body)
        return {
          kind,
          root: section,
          slot,
          body,
          read: sectionRead({ factory, theme: deps.theme, timers: deps.timers }),
          landed: false,
        }
      }

      for (const group of MANAGE_GROUPS) {
        const mine = ordered.filter((kind) => kind.group === group)
        // A group the console has no kind in is not drawn: a heading with
        // nothing under it would claim this console manages something it does
        // not, and where the four kinds of a slice land is the plan's business
        // rather than this shell's.
        if (mine.length === 0) continue
        const disclosure = make(factory, 'details', MANAGE_GROUP_CLASS)
        disclosure.setAttribute(MANAGE_GROUP_ATTRIBUTE, group)
        disclosure.append(literal(make(factory, 'summary', MANAGE_GROUP_TITLE_CLASS), group))
        const body = make(factory, 'div', MANAGE_GROUP_BODY_CLASS)
        disclosure.append(body)
        const inGroup: KindFrame[] = []
        for (const kind of mine) {
          const frame = buildKind(kind)
          inGroup.push(frame)
          frames.push(frame)
          byId.set(kind.id, frame)
          body.append(frame.root)
        }
        disclosure.addEventListener('toggle', () => {
          // A `toggle` fires for the closing direction too, and for a group that
          // has already been read: only the first opening of a group starts its
          // kinds.
          if (disclosure.getAttribute('open') === null) return
          if (opened.has(group)) return
          opened.add(group)
          for (const frame of inGroup) begin(frame)
        })
        root.append(disclosure)
      }

      return {
        root,
        read(): void {
          for (const frame of frames) begin(frame)
        },
        readKind(id: string): void {
          const frame = byId.get(id)
          if (frame !== undefined) begin(frame)
        },
        end(): void {
          for (const frame of frames) {
            frame.read.end()
            // `end` forgets the mark it was wearing, so the shell takes the
            // element out with it: a mark left in the slot would be a second mark
            // under the first one on this kind's next read, and `landed` and
            // `refused` are the only other places that empty the slot.
            frame.slot.replaceChildren()
          }
        },
      }
    },
  }
}
