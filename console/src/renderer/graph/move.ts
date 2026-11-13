// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The move: one task of the record dragged into another release, behind a
// preview and a confirmation (the dependency-graph plan's GV3.2; design section
// 5.1 rule 14, contract sections 6.24 and 6.25).
//
// **This module is the sheet and the flow, and it builds no command line.** What
// it sends is two ids the drawing put on two elements -- the node's document key
// and a release box key -- and what it draws is the answer the console's own
// `roadmapMove` composed from the document it checked those ids against: the
// exact argv, the task, the box it is in and the box it goes to. A renderer
// cannot spell a word of the command, which is the whole of rule 14's fence.
//
// **A title cannot forge the from/to line, and that is a property of the
// shape.** `moveFields` answers one field per fact -- the task, the box it is
// in, the box it goes to -- each sanitized with the class its own wire key
// declares (`title`, `name`) and each cut to `MOVE_MAX_FIELD_CODE_POINTS`. A
// title that carries an arrow, a release's name and the console's own word for
// "no release" therefore draws inside its own cell and nowhere else: there is no
// string in this module that is assembled out of two facts, so there is nothing
// for a title to be assembled into.
//
// **The three gestures are the flow's, and two of them are safe by default.** A
// preview sends `confirmed: false` and runs no child; a confirm sends `true`
// **for the request the preview drew** and for nothing else, so a second click, a
// keypress after the sheet closed and a preview that answered a different request
// all spawn nothing; and a decline drops the request outright. Undo is not a
// fourth gesture: it is the inverse move (`inverseOf`), previewed and confirmed
// the same way, which is why it cannot skip the confirmation a move must pass.
//
// The flow owns no timer, no child and no path: `MovePort` is the one door it
// knocks on, and the entry (`entry.ts`) is what wires that door to the preload
// surface and the drawing to this module's classes.

import type { Sanitized } from '../../shared/sanitize'
import { sanitizeText } from '../../shared/sanitize'
import { UNAVAILABLE_MARK } from '../../shared/availability'
import { wireTextClass } from '../../shared/schema'
import type { RoadmapMoveAnswer, RoadmapMoveOutcome } from '../../source/source'
import type { ScreenElement, ScreenFactory } from '../screens/graph'

/** MoveRequest is everything a renderer is allowed to say about a move: the
 * document key of the task (`nodes[].key`) and the record's key for the release
 * box it was dropped on (`releases[].key`), or the empty string for the
 * unassign. Neither is a word of a command line, and neither is a name: the two
 * are what the last document read listed, and the main process is what turns
 * them into the positional and the `--to` word. */
export interface MoveRequest {
  readonly node: string
  readonly toRelease: string
}

/** MoveRead is one `roadmap.move` as this side of the seam read it: the answer,
 * or the sentence a call that never reached the record is reported by. The
 * console's own source throws its refusals (`NO_ROADMAP_NODE` and its siblings),
 * so the entry is what turns a throw into this shape -- the same place, and the
 * same reason, `replyData` turns a reply envelope into one sentence. */
export type MoveRead =
  | { readonly ok: true; readonly answer: RoadmapMoveAnswer }
  | { readonly ok: false; readonly sentence: string }

/**
 * moveAnswerOf reads one `roadmap.move` answer off the bridge, **closed**: the
 * contract's members and every one of their types, with a move's two sides either
 * a `{key, name}` pair or the `null` §6.25 writes for a task in no release.
 *
 * **An answer this build cannot read is `null`.** A confirmation is a promise
 * about what the confirming click spawns, and an answer read in part is a
 * command line drawn from a document the record did not quite say: the sheet
 * then says it could not read the answer instead of drawing the fields it did
 * find. `status` is the one member judged as well, and for the strongest reason
 * of the set -- it is what a screen decides from whether the move applied, and a
 * word this build does not hold is a newer CLI's status whose meaning it cannot
 * read (`MOVE_OUTCOME_WORDS` is the vocabulary, and it is the whole of it).
 *
 * The preview and the write are one operation precisely so that what is drawn
 * and what runs are the same argv (`ipc.ts`), and this reader is the renderer's
 * half of that agreement: nothing here rebuilds a word of the command line, it
 * is the answer's own array or nothing.
 */
export function moveAnswerOf(raw: unknown): RoadmapMoveAnswer | null {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null
  const answer = raw as Record<string, unknown>
  const refusal = nullableText(answer['refusal'])
  const sentence = nullableText(answer['sentence'])
  if (refusal === undefined || sentence === undefined) return null
  const argv = answer['argv']
  if (!Array.isArray(argv) || argv.some((word) => typeof word !== 'string')) return null
  const task = answer['task']
  if (typeof task !== 'object' || task === null) return null
  const held = task as Record<string, unknown>
  const key = text(held['key'])
  const id = text(held['id'])
  const title = text(held['title'])
  if (key === null || id === null || title === null) return null
  const from = moveSideOf(answer['from'])
  const to = moveSideOf(answer['to'])
  if (from === undefined || to === undefined) return null
  const outcome = moveOutcomeOf(answer['outcome'])
  if (outcome === undefined) return null
  return {
    refusal,
    argv: argv as readonly string[],
    task: { key, id, title },
    from,
    to,
    outcome,
    sentence,
  }
}

/** text is one member that must be a string, and `null` where it is anything
 * else -- the answer is refused by the caller, which is the one place that
 * decides. */
function text(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

/** nullableText is one of the answer's optional members: a string, or the
 * `null` the contract writes where there is none. It answers `undefined` for a
 * member that is neither, which is what tells "nothing set" from "not this
 * shape". */
function nullableText(raw: unknown): string | null | undefined {
  return raw === null ? null : (text(raw) ?? undefined)
}

/** moveSideOf is one side of a move: the record's key and its name for a release
 * box, the `null` of a task in no box, or `undefined` for a shape this build
 * does not read. */
function moveSideOf(raw: unknown): { readonly key: string; readonly name: string } | null | undefined {
  if (raw === null) return null
  if (typeof raw !== 'object' || Array.isArray(raw)) return undefined
  const side = raw as Record<string, unknown>
  const key = text(side['key'])
  const name = text(side['name'])
  if (key === null || name === null) return undefined
  return { key, name }
}

/** moveOutcomeOf is §6.24's document, or `undefined` for one this build cannot
 * read -- its status included. */
function moveOutcomeOf(raw: unknown): RoadmapMoveOutcome | null | undefined {
  if (raw === null) return null
  if (typeof raw !== 'object' || Array.isArray(raw)) return undefined
  const outcome = raw as Record<string, unknown>
  const status = MOVE_STATUSES.find((each) => each === outcome['status'])
  const request = text(outcome['request'])
  const reason = nullableText(outcome['reason'])
  const current = nullableText(outcome['current'])
  if (status === undefined || request === null || reason === undefined || current === undefined) return undefined
  return { status, request, reason, current }
}

/** MovePort is the one door the flow knocks on: `FugaroSource.roadmapMove`, and
 * nothing else. It is a port rather than the source itself because the flow must
 * not be able to reach a read, a feed or a run, and because a test's port can
 * then be a recorder. */
export interface MovePort {
  move(args: MoveRequest & { readonly confirmed: boolean }): Promise<MoveRead>
}

/** MoveTarget is one release box a task can be moved to, as the drawing already
 * knows it: the record's key for the box, and the name its heading is drawn
 * with. It is what the keyboard's own way into a move is built from -- a select
 * of the boxes -- because a drag is a pointer's gesture and nothing else
 * (design section 4.3): the mouse drops a task on a box, and the keyboard
 * chooses one from this list. */
export interface MoveTarget {
  readonly key: string
  readonly name: Sanitized
}

/** MoveSheet is what the confirmation draws: the request in hand and what the
 * record answered it with.
 *
 * `answer` and `sentence` are never both set while a request is in hand: an
 * answer is the record's own reply, and the sentence is the console's for a call
 * that was refused before it queued anything (a task no read listed, a box no
 * read listed, a reply this build cannot read). A sheet that held both would
 * draw a refusal beside a command line that was never run. */
export interface MoveSheet {
  /** `request` is the move in hand, or `null` for the sheet a keyboard opened
   * with no target chosen yet (`movePhaseOf`'s `choosing`). */
  readonly request: MoveRequest | null
  readonly answer: RoadmapMoveAnswer | null
  readonly sentence: string | null
}

/** MOVE_MAX_FIELD_CODE_POINTS is how much of one value a field of the sheet
 * carries. It is this module's own cut and it is **tighter than the sanitizer's**
 * (`MAX_TEXT_LENGTH`): a field is one row of a sheet, and a title of five hundred
 * characters is a row that pushes the two boxes out of the window a person is
 * reading. The cut is marked (`MOVE_CUT_MARK`) rather than silent -- a value
 * shortened without saying so reads as the whole value. */
export const MOVE_MAX_FIELD_CODE_POINTS = 120

/** MOVE_CUT_MARK ends a value this module cut. It is U+2026, the same mark
 * `graph/model.ts` cuts a title with, so one console marks a shortened value one
 * way. */
export const MOVE_CUT_MARK = '…'

/** MOVE_TASK_LABEL, MOVE_FROM_LABEL and MOVE_TO_LABEL are the three named fields
 * of a confirmation, and they are the console's own words: the record names a
 * task and a release, and it has no word for "the box it is in" or for "the box
 * it goes to", which is what a confirmation is about. */
export const MOVE_TASK_LABEL = 'task'
export const MOVE_FROM_LABEL = 'in'
export const MOVE_TO_LABEL = 'to'

/** MOVE_UNASSIGNED_WORD is what a field draws for the side of a move that names
 * no box at all (the record's `none`). It is a word rather than an empty cell,
 * because an empty cell is a field a person cannot tell from one the console
 * failed to draw. */
export const MOVE_UNASSIGNED_WORD = 'no release'

/** MOVE_TITLE is the sheet's heading. */
export const MOVE_TITLE = 'Move this task'

/** MOVE_COMMAND_LABEL names the argv cell, which is the exact command line the
 * confirming button will spawn. */
export const MOVE_COMMAND_LABEL = 'this runs'

/** MOVE_CONFIRM_LABEL, MOVE_CANCEL_LABEL and MOVE_UNDO_LABEL are the three
 * buttons' words. Cancel is never the same word as either of the other two: a
 * sheet whose buttons read alike is a sheet a person answers without reading
 * which one they pressed. */
export const MOVE_CONFIRM_LABEL = 'Move it'
export const MOVE_CANCEL_LABEL = 'Cancel'
export const MOVE_UNDO_LABEL = 'Undo'

/** MOVE_SHEET_LABEL is the sheet's accessible name. */
export const MOVE_SHEET_LABEL = 'move this task to another release'

/** MOVE_READING_WORD is what the sheet says while the record's answer is on its
 * way. It is a word and not a blank: a sheet with a command line and no status
 * is a sheet that looks confirmed. */
export const MOVE_READING_WORD = 'asking the record'

/** MOVE_UNREADABLE_WORD is what an answer this build cannot read is reported by.
 * It is this module's own sentence -- the record's words are nowhere in it. */
export const MOVE_UNREADABLE_WORD = 'the console could not read this move’s answer'

/** MOVE_OPEN_LABEL is the row button's word, and MOVE_TARGET_LABEL names the
 * select it opens. */
export const MOVE_OPEN_LABEL = 'Move…'
export const MOVE_TARGET_LABEL = 'move this task to'

/** MOVE_TARGET_UNCHOSEN is the value of the select's not-yet-chosen option, and
 * it is a value no release key can be: a box's key is 64 lowercase hex
 * characters and the unassign is the empty string, so a lone dash is neither.
 *
 * **It is what the control is drawn on rather than the first box of its list.**
 * A browser pre-selects the first option of a `<select>` nobody has touched and
 * reports a `change` only when the value differs from the one it holds, so a
 * control drawn on the first box would swallow the choice of that box -- and of
 * the unassign, on a drawing whose boxes are all finished and whose list
 * therefore holds that option alone. The entry refuses this value, so a person
 * choosing it makes no request at all. */
export const MOVE_TARGET_UNCHOSEN = '-'

/** MOVE_TARGET_UNCHOSEN_LABEL is what that option reads as. */
export const MOVE_TARGET_UNCHOSEN_LABEL = 'choose a release'

/** MOVE_STATUSES is §6.24's four statuses, and it is the **whole** vocabulary
 * this build reads: `moveAnswerOf` refuses an answer carrying any other word,
 * because a status is what a screen decides from -- whether the move applied,
 * and so whether a way back is offered -- and a word this build does not hold is
 * a meaning it cannot read. A newer CLI's status is read as "this answer is not
 * this build's", which is the one answer that draws no button. */
export const MOVE_STATUSES = Object.freeze(['applied', 'refused', 'failed', 'pending'] as const)
export type MoveStatus = (typeof MOVE_STATUSES)[number]

/** MOVE_OUTCOME_WORDS is those four statuses as the sheet's own words, so a
 * screen draws a word rather than parsing the contract's spelling. */
export const MOVE_OUTCOME_WORDS: Readonly<Record<MoveStatus, string>> = Object.freeze({
  applied: 'the record applied the move',
  refused: 'the record refused the move',
  failed: 'the move failed',
  pending: 'the request is queued and not yet applied',
})

/** MOVE_CLASSES are the sheet's own classes, named here because the entry wires
 * the drawing by them and a rule styles them. */
export const MOVE_SHEET_CLASS = 'graph-move'
export const MOVE_FIELD_CLASS = 'graph-move-field'
export const MOVE_VALUE_CLASS = 'graph-move-value'
export const MOVE_WORD_CLASS = 'graph-move-word'
export const MOVE_CONFIRM_CLASS = 'graph-move-confirm'
export const MOVE_CANCEL_CLASS = 'graph-move-cancel'
export const MOVE_UNDO_CLASS = 'graph-move-undo'
export const MOVE_STATUS_CLASS = 'graph-move-status'
/** MOVE_OPEN_CLASS is the button a record task's row carries: the keyboard's and
 * the screen reader's way into a move, which a drag cannot be. */
export const MOVE_OPEN_CLASS = 'graph-move-open'
/** MOVE_TARGET_CLASS is the select of the boxes a move may go to, drawn while
 * the sheet is choosing one. */
export const MOVE_TARGET_CLASS = 'graph-move-target'

/** MOVE_PHASES is the sheet's own state as one attribute, so a rule and a test
 * read which of the three sheets is drawn rather than inferring it. */
export const MOVE_PHASES = Object.freeze(['choosing', 'preview', 'reading', 'refused', 'answered'] as const)
export type MovePhase = (typeof MOVE_PHASES)[number]

/** MoveField is one named field of the confirmation: the console's own label,
 * and the record's value as the sanitizer drew it. */
export interface MoveField {
  readonly label: string
  readonly value: Sanitized
}

/** cut is this module's own bound on one cell: `[...text]` iterates code points,
 * so a surrogate pair is never split into the two lone halves a font draws as a
 * box, and a value inside the bound is returned untouched. The mark is inside
 * the bound rather than after it, so "a cell is at most
 * `MOVE_MAX_FIELD_CODE_POINTS` characters" is a claim about what a person sees. */
function cut(text: string): string {
  const points = [...text]
  if (points.length <= MOVE_MAX_FIELD_CODE_POINTS) return text
  return `${points.slice(0, MOVE_MAX_FIELD_CODE_POINTS - 1).join('')}${MOVE_CUT_MARK}`
}

/** moveValue is one value of the record through the sanitizer and this module's
 * own cut. The class is asked of the wire key the value came by
 * (`wireTextClass`), so the title of a task and the name of a release draw
 * exactly as the same two values draw on the drawing behind the sheet -- one
 * reader, one class, two places. */
function moveValue(raw: string, key: string, field: string): Sanitized {
  const value = sanitizeText(raw, wireTextClass(key) ?? 'sentence', field)
  if (!value.ok) return value
  return { ok: true, text: cut(value.text), flags: value.flags }
}

/** moveSide is one end of a move as a field: the box's name, or the console's
 * own word for a task that is in no box at all. */
function moveSide(side: RoadmapMoveAnswer['from'], field: string): Sanitized {
  if (side === null) return { ok: true, text: MOVE_UNASSIGNED_WORD, flags: [] }
  return moveValue(side.name, 'name', field)
}

/** moveFields is the confirmation's three named fields, in the order the sheet
 * draws them: the task, the box it is in, and the box it goes to. **No string
 * here is composed of another**: each value is one value of the record, which is
 * what keeps a title from spelling the from/to line. */
export function moveFields(answer: RoadmapMoveAnswer): readonly MoveField[] {
  return [
    { label: MOVE_TASK_LABEL, value: moveValue(answer.task.title, 'title', 'the task’s title') },
    { label: MOVE_FROM_LABEL, value: moveSide(answer.from, 'the release the task is in') },
    { label: MOVE_TO_LABEL, value: moveSide(answer.to, 'the release the task goes to') },
  ]
}

/**
 * moveArgv is the exact command line as the sheet draws it: one value per word,
 * and one element per value.
 *
 * **A word is drawn as itself and not through a class**, which is what rule 11's
 * confirmation window does with the launch's argv and rule 12's with the edit's
 * (`src/renderer/confirm.ts`'s `command`: "both windows show what is spawned and
 * not a summary of it"). The two document words in this line are the record's
 * own **identifiers** -- the `id` the positional reads and the two release keys --
 * and never a title: the titles are the three named fields, which are sanitized.
 * A class here would draw `--to [redacted-token]` over a 64-hex release key
 * (the entropy rule reads a long hex run as a credential), and a confirmation
 * whose command line is redacted is a confirmation of nothing.
 *
 * The bound is still this module's: a word longer than a field is cut and marked,
 * so a document with an outsized `id` cannot turn the sheet into a page.
 */
export function moveArgv(answer: RoadmapMoveAnswer): readonly Sanitized[] {
  return answer.argv.map((word) => ({ ok: true, text: cut(word), flags: [] }))
}

/** inverseOf is the move that undoes an answer: the box the task came from, or
 * no box at all when it was in none. **It is a request like any other** and is
 * previewed and confirmed the same way, so an undo cannot be a write that skips
 * the confirmation. */
export function inverseOf(answer: RoadmapMoveAnswer): MoveRequest {
  return { node: answer.task.key, toRelease: answer.from === null ? '' : answer.from.key }
}

/**
 * movePhaseOf is which of the five sheets one is.
 *
 * `choosing` is the sheet a **keyboard** opened: a record task's row carries a
 * button, and pressing it puts up the list of boxes with nothing sent yet -- so
 * the phase is the state with no request, no answer and no sentence, and its
 * select is what turns a keyboard's choice into the request a preview is made
 * from.
 *
 * The other four are the reading state while the record is being asked, the
 * refusal when a call never reached it (or came back with the record's own
 * refusal), the preview when a command line is in hand and nothing has run, and
 * the answer when the record has spoken.
 */
export function movePhaseOf(sheet: MoveSheet, reading: boolean): MovePhase {
  if (reading) return 'reading'
  if (sheet.request === null && sheet.answer === null && sheet.sentence === null) return 'choosing'
  if (sheet.sentence !== null) return 'refused'
  if (sheet.answer !== null && sheet.answer.outcome !== null) return 'answered'
  if ((sheet.answer?.refusal ?? null) !== null) return 'refused'
  return 'preview'
}

/**
 * MoveFlow is the three gestures over one port, and the **request in hand** is
 * the whole of its state.
 *
 * `preview` replaces it, `decline` drops it, and `confirm` consumes it --
 * clearing it before the answer arrives, so the second call of a double click
 * finds nothing to confirm and sends nothing. `undo` is `preview` of
 * `inverseOf` the last applied answer, which is why nothing else about undo is
 * here: it is the same request, the same confirmation and the same fence.
 */
export class MoveFlow {
  private request: MoveRequest | null = null
  private applied: RoadmapMoveAnswer | null = null

  constructor(private readonly port: MovePort) {}

  /** preview asks the record for this move's own command line. Nothing is
   * spawned: the call carries `confirmed: false`, which is the one argument of
   * `roadmap.move` that reaches no word of any argv. */
  async preview(request: MoveRequest): Promise<MoveSheet> {
    this.request = { ...request }
    const read = await this.port.move({ ...request, confirmed: false })
    const sheet = this.sheetOf(request, read)
    // **A preview with no command line in hand leaves nothing to confirm.** A
    // call the console refused (a task no read listed) and an answer that is
    // already an outcome both mean the sheet offers no confirming button, and a
    // request kept past that is a confirm that would send a move a person never
    // saw drawn.
    if (movePhaseOf(sheet, false) !== 'preview') this.request = null
    return sheet
  }

  /** confirm runs the move the **preview drew**, and nothing else: a confirm
   * with no request in hand (a declined sheet, a second click, a stale key)
   * answers `null` and sends nothing at all. */
  async confirm(): Promise<MoveSheet | null> {
    const request = this.request
    if (request === null) return null
    this.request = null
    const read = await this.port.move({ ...request, confirmed: true })
    return this.sheetOf(request, read)
  }

  /** decline is the sheet's other button: the request is dropped and nothing is
   * sent. It answers `null`, which is the closed sheet. */
  decline(): null {
    this.request = null
    return null
  }

  /** undo is the inverse of the last move the record applied, previewed the way
   * any move is. It answers `null` when there is nothing to undo, which is also
   * what a sheet whose write has not been read back yet gets. */
  async undo(): Promise<MoveSheet | null> {
    const answer = this.applied
    if (answer === null) return null
    return this.preview(inverseOf(answer))
  }

  /** requestInHand is the request a confirm would send, or null when the sheet
   * has been declined, confirmed or never opened. The entry draws its buttons
   * from the drawing rather than from this, and it is read here for the one
   * thing a drawing cannot say: whether a confirm is still the preview's. */
  get pending(): MoveRequest | null {
    return this.request === null ? null : { ...this.request }
  }

  private sheetOf(request: MoveRequest, read: MoveRead): MoveSheet {
    if (!read.ok) return { request: { ...request }, answer: null, sentence: read.sentence }
    // The last move the record applied is what an undo inverts, and it is kept
    // only for that: a refused, failed or queued request is not a state the way
    // back goes through.
    this.applied = read.answer.outcome?.status === 'applied' ? read.answer : null
    return { request: { ...request }, answer: read.answer, sentence: null }
  }
}

// ---------------------------------------------------------------------------
// The sheet
// ---------------------------------------------------------------------------

/** make is a fresh element of the console's own making. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  if (classes.length > 0) element.setAttribute('class', classes.join(' '))
  return element
}

/** literal writes one of this module's own words. A value off the wire never
 * reaches it: those go through `drawn`. */
function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

/** drawn writes one `Sanitized` value into a fresh cell, with the console's own
 * `n/a` and the sanitizer's reason in the `title` where it refused -- the same
 * pair of outcomes `graph.ts`'s own `drawInto` has, and refused is the right
 * answer for a value this console will not draw. */
function drawn(parent: ScreenElement, factory: ScreenFactory, tag: string, value: Sanitized, ...classes: string[]): ScreenElement {
  const element = make(factory, tag, ...classes)
  parent.append(element)
  if (value.ok) return literal(element, value.text)
  element.setAttribute('title', value.reason)
  return literal(element, UNAVAILABLE_MARK)
}

/** sheetPhrase is the sheet's sentence-cell value: the refusal as this module
 * draws one, or the CLI's own words for a call the door refused before it
 * queued. It is a `Sanitized` value like every other cell, so a sentence a
 * server wrote cannot be anything but text. */
function sheetSentence(sheet: MoveSheet): Sanitized | null {
  if (sheet.sentence !== null) return sanitizeText(sheet.sentence, 'sentence', 'the refusal')
  const refusal = sheet.answer?.refusal ?? null
  if (refusal !== null && refusal !== '') return sanitizeText(refusal, 'sentence', 'the refusal')
  return null
}

/** statusOf is the record's own answer as the sheet's words: §6.24's status
 * through `MOVE_OUTCOME_WORDS`, with what the record said alongside it. */
function statusOf(answer: RoadmapMoveAnswer): string | null {
  const outcome = answer.outcome
  if (outcome === null) return null
  const word = MOVE_OUTCOME_WORDS[outcome.status]
  const because = outcome.reason
  return because === null || because === '' ? word : `${word}: ${because}`
}

/**
 * moveSheet draws the confirmation: its heading, the three named fields, the
 * exact argv **word by word**, the record's own answer where it has one, and the
 * buttons the phase allows.
 *
 * **Every value is its own element and every word of the command line is its own
 * element.** That is not a styling choice: it is what the fence looks like in the
 * DOM -- a title that carries an arrow and a release's name is the text of one
 * cell, and there is no cell whose text is assembled out of two facts.
 *
 * **The confirming button is drawn only where a command line is in hand and
 * nothing has run.** A reading sheet has no button, a refusal has only Cancel,
 * and an answered sheet has Undo where the move applied and Cancel otherwise --
 * so a person cannot confirm a move, undo one or re-run one by pressing a button
 * that is not there.
 */
export function moveSheet(factory: ScreenFactory, sheet: MoveSheet, targets: readonly MoveTarget[] = []): ScreenElement {
  const reading = sheet.answer === null && sheet.sentence === null && sheet.request !== null
  const phase = movePhaseOf(sheet, reading)
  const root = make(factory, 'section', MOVE_SHEET_CLASS)
  root.setAttribute('data-phase', phase)
  root.setAttribute('role', 'region')
  root.setAttribute('aria-label', MOVE_SHEET_LABEL)
  const heading = make(factory, 'h2', 'graph-move-title')
  root.append(literal(heading, MOVE_TITLE))

  const sentence = sheetSentence(sheet)
  if (phase === 'reading') drawn(root, factory, 'p', sanitizeText(MOVE_READING_WORD, 'label', 'the reading word'), 'graph-move-sentence')
  else if (sentence !== null) drawn(root, factory, 'p', sentence, 'graph-move-sentence')

  if (phase === 'choosing') {
    // **The keyboard's own way in.** A drag is a pointer's gesture and this
    // console has no pointer-only control (design section 4.3): the row's own
    // button opens this list, and the option a person chooses is the request the
    // preview is made from. The value of an option is the record's key and never
    // a word of a command: even here, what leaves the renderer is an id.
    const options = make(factory, 'select', MOVE_TARGET_CLASS)
    options.setAttribute('aria-label', MOVE_TARGET_LABEL)
    // The control opens on this and not on a box: `MOVE_TARGET_UNCHOSEN` states
    // why, and `selected` says it in the markup as well, the way the scope
    // control does -- a drawing is the state it was made with, and a control
    // whose selection the browser inferred from first-in-the-list would make the
    // first box's own choice unreportable.
    const unchosen = literal(make(factory, 'option'), MOVE_TARGET_UNCHOSEN_LABEL)
    unchosen.setAttribute('value', MOVE_TARGET_UNCHOSEN)
    unchosen.setAttribute('selected', 'selected')
    options.append(unchosen)
    for (const target of targets) {
      const option = literal(make(factory, 'option'), target.name.ok ? target.name.text : UNAVAILABLE_MARK)
      option.setAttribute('value', target.key)
      options.append(option)
    }
    // The unassign is a target like any other: a task in a box is taken out of
    // it by choosing this, and the request then carries the empty string the
    // operation declares for it.
    const none = literal(make(factory, 'option'), MOVE_UNASSIGNED_WORD)
    none.setAttribute('value', '')
    options.append(none)
    root.append(options)
  }

  const answer = sheet.answer
  if (answer !== null) {
    const fields = make(factory, 'div', 'graph-move-fields')
    for (const field of moveFields(answer)) {
      const cell = make(factory, 'div', MOVE_FIELD_CLASS)
      cell.setAttribute('data-field', field.label)
      cell.append(literal(make(factory, 'span', 'graph-move-label'), field.label))
      drawn(cell, factory, 'span', field.value, MOVE_VALUE_CLASS)
      fields.append(cell)
    }
    root.append(fields)

    const command = make(factory, 'div', 'graph-move-command')
    command.setAttribute('role', 'group')
    command.setAttribute('aria-label', MOVE_COMMAND_LABEL)
    command.append(literal(make(factory, 'span', 'graph-move-command-label'), MOVE_COMMAND_LABEL))
    const argv = make(factory, 'code', 'graph-move-argv')
    for (const word of moveArgv(answer)) drawn(argv, factory, 'span', word, MOVE_WORD_CLASS)
    command.append(argv)
    root.append(command)

    const status = statusOf(answer)
    if (status !== null) {
      const statusCell = make(factory, 'p', MOVE_STATUS_CLASS)
      root.append(literal(statusCell, status))
    }
  }

  const actions = make(factory, 'div', 'graph-move-actions')
  if (phase === 'preview') actions.append(literal(make(factory, 'button', MOVE_CONFIRM_CLASS), MOVE_CONFIRM_LABEL))
  if (phase === 'answered' && answer?.outcome?.status === 'applied') {
    actions.append(literal(make(factory, 'button', MOVE_UNDO_CLASS), MOVE_UNDO_LABEL))
  }
  actions.append(literal(make(factory, 'button', MOVE_CANCEL_CLASS), MOVE_CANCEL_LABEL))
  root.append(actions)
  return root
}
