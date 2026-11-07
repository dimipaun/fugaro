// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Project tab's managed state and its audit trail, as one section: **the
// marks** and **the trail**, drawn from `managed status --json` (the
// managed-screens plan's CM5; contract section 6.21, decisions D1472 and
// D1474 to D1477).
//
// **One read, two blocks.** The managed state and the trail are one read and
// one module (D1488): the trail is a second block of the same document, not a
// read of its own, because the command computes both from the same walk of the
// marker and the record family -- a second spawn would be a second opinion
// about one project. The section therefore refuses as a whole and names its own
// command, and the tab that mounts it gives it its own host, its own busy state
// and its own place in the parallel first paint (`section.ts`, the owner's rule
// of 2026-10-31). `projects.ts` wires it in (CM10); this module draws it.
//
// **Every string is a document's.** The marks, the read's reason, the day, the
// state sentence and the record's five members all arrive from a CLI child, so
// every one of them goes through `putText` with the class its wire key
// declares, and every number through `putReading`. The only words this file
// writes itself are the section's titles, its terms and its two state words --
// which is what `literal` is for, and why `screen-literals.test.ts` reads this
// file like every other screen.
//
// **The record's argv never arrives, and this decoder is the second half of
// that promise** (D1476). Section 6.21 bounds the document to the five members
// below; the record itself carries the argv the write ran as, the rules the
// gate fired and the marks that were in force. A decoder that spread the
// `audit` object into the model -- or that drew it whole -- would put a path, a
// root name or a title an operator did not mean to publish on a surface anybody
// at the machine can read, and it would do it with the CLI's bound intact and
// the console's own leak. So `recordOf` reads the five keys by name and nothing
// else touches the object.
//
// **The section draws no "revoked"** (D1475). A project whose marker or label
// was taken off is *not managed*: "revoked" is a run-time fact of a write gate
// that is persisted nowhere, so a screen that drew it would be showing a state
// the CLI does not have. The marks' own answer, and beside it the newest
// record's verdict, are the two facts there are.

import { readNumber } from '../../shared/availability'
import type { Reading } from '../../shared/availability'
import { sanitizeRefusal } from '../../shared/sanitize'
import type { TextClass } from '../../shared/sanitize'
import { putReading, putText } from '../app'
import type { ElementLike } from '../app'
import { BUSY_SLOT_CLASS } from '../section'
import { REFUSAL_UNREADABLE } from './degraded'

/** ScreenElement and ScreenFactory are the drawing surface, declared here as
 * every screen declares its own. */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

export interface ScreenFactory {
  create(tag: string): ScreenElement
}

// ---------------------------------------------------------------------------
// The section's own vocabulary.
// ---------------------------------------------------------------------------

/** MANAGED_OPERATION is the read this section makes, spelled as the console's
 * operation surface spells it (`src/main/ipc.ts`, CM4). It is drawn on the
 * panel's `data-operation`, so a reader of the markup -- or a test -- can see
 * which operation the panel is about. */
export const MANAGED_OPERATION = 'managed.status'

/** MANAGED_PANEL_KEY is the panel's own key, the value `data-panel` carries. */
export const MANAGED_PANEL_KEY = 'managed'

/**
 * MANAGED_SOURCE_LINE is the command the document comes from, in the console's
 * own words. It is drawn on every state of the panel -- read, refused, and the
 * refusal that names it -- because the design's review criterion for this
 * screen is that **every field traces to a command line in section 5.1's
 * allow-list**, and a refusal that did not name its command would leave an
 * operator with a missing section and no way to ask for it by hand.
 */
export const MANAGED_SOURCE_LINE = 'fugaro managed status --json'

/** MANAGED_TITLE is the panel's heading; the two block headings below are the
 * plan's own names for the two things the ask lists. */
export const MANAGED_TITLE = 'Managed state'
export const MANAGED_MARKS_TITLE = 'The marks'
export const MANAGED_TRAIL_TITLE = 'The trail'

/** MANAGED_BUSY is the label the section's busy mark carries. It names the
 * read and not the section: the mark says what is being read, which is the fact
 * an operator waiting on it needs. */
export const MANAGED_BUSY = 'Reading the project’s managed state…'

/**
 * The two state words of the marks block. They are the console's own, and they
 * are deliberately **not** "revoked" (D1475): the document's `managed` is true
 * only when the creation fact in the runs bucket and the project's
 * `fugaro_managed` label agree, so its `false` is "the two do not agree" --
 * whatever the reason the read names beside it -- and never a mark that was
 * taken away.
 */
export const MANAGED_MANAGED_WORD = 'managed'
export const MANAGED_NOT_MANAGED_WORD = 'not managed'

/** The terms the two blocks draw, one per row. They are the console's words for
 * what each value is, so a figure is never drawn without a word saying what it
 * is a figure of. */
export const MANAGED_MARK_TERM = 'state'
export const MANAGED_REASON_TERM = 'why not'
export const MANAGED_VERSION_TERM = 'version'
export const MANAGED_GENERATION_TERM = 'generation'
export const MANAGED_DAY_TERM = 'day'
export const MANAGED_COUNT_TERM = 'records'
export const MANAGED_AT_TERM = 'at'
export const MANAGED_COMMAND_TERM = 'command'
export const MANAGED_VERDICT_TERM = 'verdict'
export const MANAGED_IDENTITY_TERM = 'identity'
export const MANAGED_DIGEST_TERM = 'plan digest'

/** MANAGED_DIGEST_NOTE is what the digest row says in its `title` about a cell
 * that draws a marker instead of the digest. It is the console's own sentence
 * and it carries no value: it explains a redaction the operator can see, which
 * is the one thing a screen drawing a marker owes the person reading it. */
export const MANAGED_DIGEST_NOTE =
  'the plan digest is a 64-character hex run, and this console reads a run like that as a credential, so it draws its marker here'

/** The class hooks this section draws with. CM11 writes the rules that select
 * them and the document that licenses them; a class the stylesheet lacks
 * renders as plain markup, which is the state the golden frames are recorded
 * in, so this module lands correct and unstyled rather than waiting. */
export const MANAGED_CLASS = 'projects-managed'
export const MANAGED_MARKS_CLASS = 'projects-managed-marks'
export const MANAGED_MARKS_HEAD_CLASS = 'projects-managed-marks-head'
export const MANAGED_MARKS_LIST_CLASS = 'projects-managed-marks-list'
export const MANAGED_TRAIL_CLASS = 'projects-managed-trail'
export const MANAGED_TRAIL_HEAD_CLASS = 'projects-managed-trail-head'
export const MANAGED_TRAIL_LIST_CLASS = 'projects-managed-trail-list'
export const MANAGED_TRAIL_STATE_CLASS = 'projects-managed-trail-state'
export const MANAGED_TERM_CLASS = 'projects-managed-term'
export const MANAGED_VALUE_CLASS = 'projects-managed-value'
export const MANAGED_WORD_CLASS = 'projects-managed-word'
export const MANAGED_NUMBER_CLASS = 'projects-managed-number'

// ---------------------------------------------------------------------------
// The model.
// ---------------------------------------------------------------------------

/** ManagedState is the section's own state. There is no third: the command
 * answers with a document or it does not, and a document of a shape this build
 * cannot read is a read that did not answer. */
export type ManagedState = 'read' | 'unavailable'

/** ManagedMarks is the marks block: the four `MarkFacts` fields the read
 * exposes (D1474). `reason` is the read's own sentence for a project that is
 * **not** managed, and null when the marks agree; `generation` is the creation
 * fact's object generation in decimal, and null when no object was placed. */
export interface ManagedMarks {
  readonly managed: boolean
  readonly reason: string | null
  readonly version: Reading
  readonly generation: string | null
}

/**
 * ManagedRecord is the bounded subset of one audit record (D1476): the time,
 * the command, the verdict, the identity and the plan digest -- **and nothing
 * else**. There is no `argv` member here on purpose: a member is a place a
 * value can arrive, and the whole of the bound is that this one cannot.
 */
export interface ManagedRecord {
  readonly at: string | null
  readonly command: string | null
  readonly verdict: string | null
  readonly identity: string | null
  readonly planDigest: string | null
}

/** ManagedTrail is the trail block: the day the window answered from, that
 * day's record count, the sentence for a read that did not answer, and the
 * newest record of the window. `day` and `state` are null for the empty
 * spellings the contract uses (`""`), and `record` is null when the window
 * holds none. */
export interface ManagedTrail {
  readonly day: string | null
  readonly count: Reading
  readonly state: string | null
  readonly record: ManagedRecord | null
}

/** ManagedModel is the section, built and testable with no element. */
export interface ManagedModel {
  readonly state: ManagedState
  readonly because: string | null
  readonly marks: ManagedMarks | null
  readonly trail: ManagedTrail | null
}

/** ManagedInputs is what the entry hands the model: the read's document, and
 * the CLI's own sentence when the read was refused. */
export interface ManagedInputs {
  readonly raw: unknown
  readonly because?: string | null
}

/** objectAt is a value that is a JSON object, or null. */
function objectAt(raw: unknown): Record<string, unknown> | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

/** textAt is a value that is a JSON string, or null. */
function textAt(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

/** named is a document string that is a *fact* only when it says something: the
 * contract spells "there is none" as `""` for the reason, the generation, the
 * day and the state sentence, and an empty cell is not the same claim as an
 * absent row. The empty spelling becomes null, and the drawing then draws no
 * row at all. */
function named(raw: unknown): string | null {
  const text = textAt(raw)
  return text === null || text === '' ? null : text
}

/** drawnRefusal is the CLI's own refusal sentence, sanitized, or the console's
 * sentence for one it cannot draw. The free-text rules would draw
 * `roles/datastore.viewer` as `[redacted]` and the project as `<project>`, and
 * the remedy is the whole value of a refusal, so this is `sanitizeRefusal` --
 * the one door per screen, as `degraded.ts` and `projects.ts` both say. */
function drawnRefusal(because: string): string {
  const drawn = sanitizeRefusal(because, 'the reason this section has nothing to draw')
  return drawn.ok ? drawn.text : REFUSAL_UNREADABLE
}

/** absent is the sentence for a read that produced nothing and whose refusal
 * carried no CLI sentence of its own: the console's own words, naming the
 * command, so the section's refusal names its own read. */
function absent(because: string | null | undefined): string {
  if (because !== undefined && because !== null && because.trim() !== '') return because
  return `no document arrived for \`${MANAGED_SOURCE_LINE}\``
}

/** recordOf reads the five members of the bounded subset and **only** those
 * five. The record the CLI holds carries more -- the argv, the rules, the marks
 * -- and none of it is this screen's to draw (D1476), so the keys are read one
 * by one rather than the object being carried or spread. */
function recordOf(audit: Record<string, unknown>): ManagedRecord {
  return {
    at: textAt(audit['at']),
    command: textAt(audit['command']),
    verdict: textAt(audit['verdict']),
    identity: textAt(audit['identity']),
    planDigest: textAt(audit['plan_digest']),
  }
}

/** trailOf reads the trail block of a **managed** project's document. A project
 * that is not managed is never handed here: it files no record (D1025), so its
 * four trail members are the empty spellings and none of them is a fact about
 * it. */
function trailOf(document: Record<string, unknown>): ManagedTrail {
  const audit = objectAt(document['audit'])
  return {
    day: named(document['audit_day']),
    count: readNumber(document['audit_count'], 'audit_count'),
    state: named(document['audit_state']),
    record: audit === null ? null : recordOf(audit),
  }
}

/**
 * buildManagedModel turns the document into the section. It touches no element,
 * so every rule below is testable in plain Node.
 *
 * **A document whose `managed` is not a boolean is a read that did not
 * answer**, not a document to read around. The member is the document's whole
 * subject -- it decides whether the trail is a fact about this project at all
 * -- so a `"true"` or a `1` there is a shape this build does not know, and
 * coercing it would decide the section against a reading of the document the
 * CLI never made. The section then says which read did not answer and refuses
 * as a whole, which is what it does for a document that never arrived.
 */
export function buildManagedModel(inputs: ManagedInputs): ManagedModel {
  const document = objectAt(inputs.raw)
  const managed = document === null ? undefined : document['managed']
  if (document === null || typeof managed !== 'boolean') {
    return {
      state: 'unavailable',
      because: drawnRefusal(absent(inputs.because)),
      marks: null,
      trail: null,
    }
  }
  return {
    state: 'read',
    because: null,
    marks: {
      managed,
      reason: named(document['reason']),
      version: readNumber(document['version'], 'version'),
      generation: named(document['generation']),
    },
    trail: managed ? trailOf(document) : null,
  }
}

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

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

/** term is one row's `dt`: the console's own word for what the value beside it
 * is. */
function term(parent: ScreenElement, factory: ScreenFactory, text: string): ScreenElement {
  return literal(cell(parent, factory, 'dt', MANAGED_TERM_CLASS), text)
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
  reading: Reading,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, 'dd', MANAGED_VALUE_CLASS, MANAGED_NUMBER_CLASS, ...classes)
  putReading(element, reading, 'count')
  return element
}

/**
 * panelShell is the panel the tab's other sections are drawn with: the same
 * `section.projects-panel`, the same `data-panel`/`data-state`/`data-operation`
 * hooks, the same `h2.projects-title`, the same `span.projects-state` word and
 * the same `span.projects-source` line, so the managed section reads as a
 * sibling of the panels already on the tab rather than as a card of its own.
 *
 * The `because` sentence is drawn **only** when the read did not answer, and it
 * is a string this module already put through `sanitizeRefusal` (or one of its
 * own sentences naming the command): `drawnRefusal` is where the CLI's wording
 * stops being a document's string.
 */
function panelShell(factory: ScreenFactory, model: ManagedModel): ScreenElement {
  const element = make(factory, 'section', 'projects-panel')
  element.setAttribute('data-panel', MANAGED_PANEL_KEY)
  element.setAttribute('data-state', model.state)
  element.setAttribute('data-operation', MANAGED_OPERATION)
  literal(cell(element, factory, 'h2', 'projects-title'), MANAGED_TITLE)
  const state = cell(element, factory, 'span', 'projects-state')
  if (model.state === 'read') literal(state, 'read')
  else literal(state, 'not read')
  literal(cell(element, factory, 'span', 'projects-source'), MANAGED_SOURCE_LINE)
  if (model.because !== null) literal(cell(element, factory, 'p', 'projects-because'), model.because)
  return element
}

/** marksBlock is the marks: managed or not, why not when it is not, and the two
 * numbers the creation fact carries. The reason is drawn **beside** the state
 * word and never instead of it (D1474): "not managed" is the answer and the
 * sentence is the why. */
function marksBlock(factory: ScreenFactory, marks: ManagedMarks): ScreenElement {
  const element = make(factory, 'section', MANAGED_MARKS_CLASS)
  literal(cell(element, factory, 'h3', MANAGED_MARKS_HEAD_CLASS), MANAGED_MARKS_TITLE)
  const list = cell(element, factory, 'dl', MANAGED_MARKS_LIST_CLASS)
  term(list, factory, MANAGED_MARK_TERM)
  const word = cell(list, factory, 'dd', MANAGED_VALUE_CLASS, MANAGED_WORD_CLASS)
  if (marks.managed) literal(word, MANAGED_MANAGED_WORD)
  else literal(word, MANAGED_NOT_MANAGED_WORD)
  if (marks.reason !== null) {
    term(list, factory, MANAGED_REASON_TERM)
    textCell(list, factory, 'dd', marks.reason, 'sentence', 'reason', MANAGED_VALUE_CLASS)
  }
  term(list, factory, MANAGED_VERSION_TERM)
  numberCell(list, factory, marks.version)
  if (marks.generation !== null) {
    term(list, factory, MANAGED_GENERATION_TERM)
    textCell(list, factory, 'dd', marks.generation, 'label', 'generation', MANAGED_VALUE_CLASS)
  }
  return element
}

/**
 * trailBlock is the trail: the day and its count, or the sentence for a read
 * that did not answer, and then the newest record's five members.
 *
 * **A state and a figure are never drawn together.** Section 6.21 is explicit
 * that an unknown drawn as `audit_count: 0` is the silent pass -- "nothing was
 * recorded" and "the records could not be read" look identical in a count --
 * and that a figure beside a state is read as an answer. So when the document
 * carries an `audit_state`, the section draws that sentence and **no** day and
 * no count, even though the wire spells both as `""` and `0`.
 */
function trailBlock(factory: ScreenFactory, trail: ManagedTrail): ScreenElement {
  const element = make(factory, 'section', MANAGED_TRAIL_CLASS)
  literal(cell(element, factory, 'h3', MANAGED_TRAIL_HEAD_CLASS), MANAGED_TRAIL_TITLE)
  if (trail.state !== null) {
    textCell(element, factory, 'p', trail.state, 'sentence', 'audit_state', MANAGED_TRAIL_STATE_CLASS)
  }
  const list = cell(element, factory, 'dl', MANAGED_TRAIL_LIST_CLASS)
  if (trail.state === null) {
    if (trail.day !== null) {
      term(list, factory, MANAGED_DAY_TERM)
      textCell(list, factory, 'dd', trail.day, 'label', 'audit_day', MANAGED_VALUE_CLASS)
    }
    term(list, factory, MANAGED_COUNT_TERM)
    numberCell(list, factory, trail.count)
  }
  const record = trail.record
  if (record !== null) {
    term(list, factory, MANAGED_AT_TERM)
    textCell(list, factory, 'dd', record.at, 'label', 'at', MANAGED_VALUE_CLASS)
    term(list, factory, MANAGED_COMMAND_TERM)
    textCell(list, factory, 'dd', record.command, 'label', 'command', MANAGED_VALUE_CLASS)
    term(list, factory, MANAGED_VERDICT_TERM)
    textCell(list, factory, 'dd', record.verdict, 'label', 'verdict', MANAGED_VALUE_CLASS)
    // The account the write ran as is a person, and the `person` class is the
    // one the recorder's own identity fields carry (`account`, `launched_by`,
    // `requested_by`): an address is drawn without its local part, and a value
    // that is not a login at all is refused rather than half-drawn.
    term(list, factory, MANAGED_IDENTITY_TERM)
    textCell(list, factory, 'dd', record.identity, 'person', 'identity', MANAGED_VALUE_CLASS)
    // The plan digest is a 64-character hex run, and the free-text rules take a
    // long hex run for a credential, so this cell draws the marker rather than
    // the digest -- the same answer the recorder makes for the same value
    // (`src/source/redact.ts` names the keys whose digests it keeps, and
    // `plan_digest` is not one of them). `putText`'s ok path writes text and no
    // title, so the marker would stand alone and unexplained; the note goes on
    // the cell **before** the write, so a document that carried no digest at
    // all keeps `putText`'s own reason for the `n/a` in its place.
    //
    // Drawing the digest itself would take a class this console does not have:
    // the recorder has a `digest` class that keeps a hex value as written
    // (`redact.ts`'s `HEX_RE`), and this layer's class list has no member for
    // one, so the field contract section 6.21 carries for comparing a record
    // against a plan is drawn by `fugaro managed status` itself and not here.
    // Adding that class is a decision about the sanitizer rather than about this
    // drawing -- the same call `docs/design/console-styling.md` section 2.5
    // makes for a coordinator id -- and it is reported rather than made in this
    // section.
    term(list, factory, MANAGED_DIGEST_TERM)
    const digest = cell(list, factory, 'dd', MANAGED_VALUE_CLASS)
    digest.setAttribute('title', MANAGED_DIGEST_NOTE)
    putText(digest, record.planDigest, 'label', 'plan_digest')
  }
  return element
}

/** ManagedFrame is the section's shell: the host the entry mounts, and the slot
 * its busy mark goes in. */
export interface ManagedFrame {
  readonly root: ScreenElement
  readonly slot: ScreenElement
}

/**
 * renderManagedFrame is the section's host and its slot.
 *
 * **The slot is the host's first child**, so the mark a control change puts up
 * in the same frame sits at the top of this section rather than under a block
 * nobody has scrolled to, and `entry.ts`'s `clearSection(host, slot)` empties
 * the section back to it. It is the tab's own `console-busy-slot`, so one
 * section's mark cannot take another's down: the two hosts are separate
 * elements and each wears its own `data-busy`.
 */
export function renderManagedFrame(factory: ScreenFactory): ManagedFrame {
  const root = make(factory, 'div', MANAGED_CLASS)
  const slot = cell(root, factory, 'div', BUSY_SLOT_CLASS)
  return { root, slot }
}

/** fillManaged draws the section into its host: the panel, and then the two
 * blocks -- the trail only when the document read one. A refusal draws the
 * panel alone, with no empty block under it, because an empty marks list would
 * read as a project with no marks rather than as a read that did not answer. */
export function fillManaged(factory: ScreenFactory, model: ManagedModel, host: ScreenElement): void {
  host.append(panelShell(factory, model))
  if (model.state !== 'read' || model.marks === null) return
  host.append(marksBlock(factory, model.marks))
  if (model.trail !== null) host.append(trailBlock(factory, model.trail))
}

/** renderManaged is the whole section for one model: the frame and the fill, in
 * one call. The entry mounts the two separately -- it needs the slot before any
 * read lands -- so this is for the callers that do not. */
export function renderManaged(factory: ScreenFactory, model: ManagedModel): ScreenElement {
  const frame = renderManagedFrame(factory)
  fillManaged(factory, model, frame.root)
  return frame.root
}
