// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Project tab's retirements, as one section: what the last plain
// `fugaro update` retired by itself, and the runs it walked past while it did
// (the managed-screens plan's CM8; decisions F21, D1484, D1485).
//
// **One section, one read, and here is the settlement the plan left to this
// task.** The interface says "the record CM1 writes, through the operation CM4
// added for the marks (`managed.status` carries it) or a row of its own if
// CM1's record is its own document; the task settles which with the owner and
// says so in its commit". This section reads it **through `managed.status`** --
// the operation `managed.ts` already makes (CM4, `src/main/ipc.ts`) -- for
// three reasons, in the order they bind:
//
//   1. The plan's own parents: `docs/console-managed-screens/reconciliation.md`
//      F21 files the record as one of the managed screen's subjects, and CM12's
//      fixture arms are the two CM4 reads. A fourth read ("a row of its own")
//      would be a new row in the spawn and IPC allow-lists -- a
//      security-sensitive change of its own, with its own pinning tests and its
//      own row in `docs/design/desktop-console.md` section 5.1 -- and the
//      plan's shared-files table gives this task no writer of `spawn.ts`,
//      `ipc.ts`, `preload/index.ts` or that design document.
//   2. One read is one opinion. The retirement and the marks are two facts
//      about one project's update, and a second spawn would be a second walk of
//      the same state at a second moment (the reason D1488 keeps the trail in
//      the marks' read).
//   3. The record is CM1's own document on disk (`localcfg.UpdateProject`:
//      `at`, `retire[]`, `runs_in_flight`), and the two reads are already the
//      same command's: `managed status --json` is where the console asks about
//      the last update.
//
// **What that settlement costs today, reported rather than worked around.** The
// `managed status --json` document this build's CLI prints (CM2,
// `internal/cli/managed_status.go`) carries the project, the marks, the version
// and the audit trail, and **no retirement member**: contract section 6.21 does
// not list one. So an installation whose CLI is this build's answers the
// section's read with a document that has no `retirements` key, and the section
// draws its refusal -- the honest answer, and never "no update recorded yet",
// which is a claim about a machine that has run an update and would be false.
// The member is a Go change (the document struct, its contract section and the
// docs tests that pin both), it is outside this task's one file group, and this
// module is written to read it the moment it lands: `retirements` holding
// `localcfg.UpdateProject` plus the record's `version`, or `null` for a project
// this machine has recorded no update for.
//
// **The two empty states are two sentences** (CM1, D1484). A record whose
// `retire` is `[]` is "the last update retired nothing": a run happened, and it
// is drawn with the time it settled and the runs it walked past. A record with
// no entry for this project is "no update recorded yet": a fact about a machine
// that has never run the update at all. Merging them would tell an operator who
// has just updated that nothing was retired -- which is a different, and
// possibly false, claim.
//
// **Names, never values, and no plan** (D1485). A retirement carries an
// address, a kind, a cause, the `kept` promise, a secret container's logical
// name and its Secret Manager **ID** -- which is a name, not a value -- and the
// `protected` flag. This section draws exactly those: the secret is drawn as
// its name, under a term saying so and with a note saying why, and a value that
// is not a name has no cell here at all. The Terraform plan is not drawn,
// because a plan is a document about the operator's whole project and carries
// values a retirement set does not need, and the record's `job` and `region` --
// which the state-removal steps need and a screen does not -- are read by
// nothing in this file. `rowOf` reads the keys it names and **only** those, so
// a document wider than the record contributes nothing to any cell.
//
// **Every string is a document's.** The addresses, the causes, the promises,
// the names and the time arrive from a CLI child, so each goes through
// `putText` with the class its wire key declares; the numbers go through
// `putReading`, so an unknown count is drawn as `n/a` and never as a zero it
// did not read (D1483). The only words this file writes itself are its titles,
// its terms and its two flag words, which is what `literal` is for.
//
// The tab mounts this (CM10) and the fixtures arm it (CM12); this module draws
// it, and `retirements.test.ts` is what holds it to the four rules above.

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

/** RETIREMENTS_OPERATION is the read this section makes, spelled as the
 * console's operation surface spells it (`src/main/ipc.ts`, CM4). It is drawn on
 * the panel's `data-operation`, so a reader of the markup -- or a test -- can
 * see which operation the panel is about. */
export const RETIREMENTS_OPERATION = 'managed.status'

/** RETIREMENTS_PANEL_KEY is the panel's own key, the value `data-panel` carries.
 * It is **not** `managed`: the two sections read the same document and draw
 * different subjects, and a panel key is what CM11's rules and CM12's fixtures
 * select one section by. */
export const RETIREMENTS_PANEL_KEY = 'retirements'

/** RETIREMENTS_SOURCE_LINE is the command the record comes from, in the
 * console's own words. It is drawn on every state of the panel -- read, refused,
 * and on each of the section's own refusals -- because every field traces to a
 * command line in section 5.1's allow-list, and a refusal that did not name its
 * command would leave an operator with a missing section and no way to ask for
 * it by hand. */
export const RETIREMENTS_SOURCE_LINE = 'fugaro managed status --json'

/** The headings: the panel's, the record block's, and the set's. The block's
 * head names the subject -- the *last* update, because the record holds one
 * update and this is which one. */
export const RETIREMENTS_TITLE = 'The last update’s retirements'
export const RETIREMENTS_RECORD_TITLE = 'The last update'
export const RETIREMENTS_SET_TITLE = 'Retired'

/** RETIREMENTS_BUSY is the label the section's busy mark carries. It names the
 * read and not the section: the mark says what is being read, which is the fact
 * an operator waiting on it needs. */
export const RETIREMENTS_BUSY = 'Reading the last update’s retirements…'

/** RETIREMENTS_RECORD_VERSION is the only version of CM1's record this build
 * reads (`localcfg.UpdateRecordVersion`). A record of another version is
 * refused rather than read as this one's: the file is state a newer fugaro may
 * have written in a shape this build would misread. */
export const RETIREMENTS_RECORD_VERSION = 1

/**
 * RETIREMENTS_NONE_SENTENCE and RETIREMENTS_NO_RECORD_SENTENCE are the two
 * empty states, and they are deliberately two sentences (CM1, D1484): the first
 * is a run's ("the last update retired nothing"), the second a machine's ("no
 * update recorded yet"). They are drawn in different places for the same
 * reason -- the first inside the record block, beside the time and the runs the
 * run carried, and the second where the record would be, with no block at all.
 */
export const RETIREMENTS_NONE_SENTENCE = 'nothing retired by the last update'
export const RETIREMENTS_NO_RECORD_SENTENCE = 'no update recorded yet'

/** The phrases the section's own refusals are composed around, so a test can
 * pin a sentence by the word that makes it the sentence it is. */
export const RETIREMENTS_ABSENT_WORD = 'carries no last-update record'
export const RETIREMENTS_SET_WORD = 'carries no retirement set'
export const RETIREMENTS_VERSION_WORD = 'this console reads version'

/** The terms the block and its rows draw, one per row: the console's words for
 * what each value is, so a figure is never drawn without a word saying what it
 * is a figure of. */
export const RETIREMENTS_AT_TERM = 'at'
export const RETIREMENTS_INFLIGHT_TERM = 'runs in flight'
export const RETIREMENTS_ADDRESS_TERM = 'address'
export const RETIREMENTS_KIND_TERM = 'kind'
export const RETIREMENTS_CAUSE_TERM = 'why'
export const RETIREMENTS_KEPT_TERM = 'kept'
export const RETIREMENTS_LOGICAL_TERM = 'logical name'
export const RETIREMENTS_SECRET_TERM = 'secret name'
export const RETIREMENTS_PROTECTED_TERM = 'protected'

/**
 * The two flag words of a row's `protected`, and they are the console's own:
 * the wire member is a boolean, so its `false` is "this address may be deleted
 * without a second decision" and never a deletion that was refused. The words
 * say which of the two the record reported, and neither of them is a promise
 * about what deleting it would do.
 */
export const RETIREMENTS_PROTECTED_WORD = 'protected'
export const RETIREMENTS_UNPROTECTED_WORD = 'not protected'

/**
 * RETIREMENTS_SECRET_NOTE is what the secret cell says in its `title`, and it
 * is the whole reason this row exists in the shape it does (D1485): the ID the
 * record carries is the secret **container's name**, and the last update's
 * record holds no secret value at all. A cell that drew something else -- a
 * redaction, or a value -- would be claiming either that this console had a
 * value to withhold or that it had read one, and it has neither.
 */
export const RETIREMENTS_SECRET_NOTE =
  'a Secret Manager ID is the secret container’s name, and the last update’s record carries names and no values'

/** The class hooks this section draws with. CM11 writes the rules that select
 * them; CM12's fixtures select the section by `RETIREMENTS_CLASS`. */
export const RETIREMENTS_CLASS = 'projects-retirements'
export const RETIREMENTS_RECORD_CLASS = 'projects-retirements-record'
export const RETIREMENTS_RECORD_HEAD_CLASS = 'projects-retirements-record-head'
export const RETIREMENTS_SET_HEAD_CLASS = 'projects-retirements-set-head'
export const RETIREMENTS_RECORD_LIST_CLASS = 'projects-retirements-record-list'
export const RETIREMENTS_SET_LIST_CLASS = 'projects-retirements-set-list'
export const RETIREMENTS_ROW_CLASS = 'projects-retirements-row'
export const RETIREMENTS_ROW_FACTS_CLASS = 'projects-retirements-row-facts'
export const RETIREMENTS_STATE_CLASS = 'projects-retirements-state'
export const RETIREMENTS_TERM_CLASS = 'projects-retirements-term'
export const RETIREMENTS_VALUE_CLASS = 'projects-retirements-value'
export const RETIREMENTS_WORD_CLASS = 'projects-retirements-word'
export const RETIREMENTS_NUMBER_CLASS = 'projects-retirements-number'
export const RETIREMENTS_SECRET_CLASS = 'projects-retirements-secret'

// ---------------------------------------------------------------------------
// The model.
// ---------------------------------------------------------------------------

/** RetirementsState is the section's own state. There is no third: the read
 * answers with a document or it does not, and a document of a shape this build
 * cannot read -- or of a record version it does not write -- is a read that did
 * not answer. */
export type RetirementsState = 'read' | 'unavailable'

/**
 * RetiredResource is one retired address, and it is D1485's closed list:
 * the address, the kind, the cause, the `kept` promise, a secret container's
 * logical name, the Secret Manager ID -- **as a name** -- and the flag. There is
 * no `job`, no `region` and no plan member here on purpose: a member is a place
 * a value can arrive, and the whole of the bound is that these cannot.
 *
 * Each string is null when the record's spelling of it is empty, which the wire
 * uses for "this row is not about a secret" and "nothing was promised". A null
 * draws no row at all: an empty cell is not the same claim as an absent one.
 */
export interface RetiredResource {
  readonly address: string | null
  readonly kind: string | null
  readonly cause: string | null
  readonly kept: string | null
  readonly logical: string | null
  readonly secretName: string | null
  readonly protected: boolean | null
}

/** RetirementsRecord is the last update of one project: when it settled its
 * retirement, what it retired, and the runs the registry held while it did. */
export interface RetirementsRecord {
  readonly at: string | null
  readonly rows: readonly RetiredResource[]
  readonly runsInFlight: Reading
}

/** RetirementsModel is the section, built and testable with no element. */
export interface RetirementsModel {
  readonly state: RetirementsState
  readonly because: string | null
  readonly record: RetirementsRecord | null
}

/** RetirementsInputs is what the entry hands the model: the read's document --
 * the `managed status --json` one, of which this section reads the
 * `retirements` member -- and the CLI's own sentence when the read was refused
 * whole. */
export interface RetirementsInputs {
  readonly raw: unknown
  readonly because?: string | null
}

/** objectAt is a value that is a JSON object, or null. */
function objectAt(raw: unknown): Record<string, unknown> | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

/** named is a document string that is a *fact* only when it says something: the
 * wire spells "there is none" as `""`, and an empty cell is not the same claim
 * as an absent row. The empty spelling becomes null, and the drawing then draws
 * no row at all. */
function named(raw: unknown): string | null {
  return typeof raw === 'string' && raw !== '' ? raw : null
}

/** drawnRefusal draws a sentence through `sanitizeRefusal`, the one door per
 * screen: the free-text rules would draw `roles/datastore.viewer` as
 * `[redacted]`, and the remedy is the whole value of a refusal, while the
 * credential rules still take a token a child echoed. A sentence the sanitizer
 * refuses whole is `REFUSAL_UNREADABLE`, so a refusal is never blank. */
function drawnRefusal(because: string): string {
  const drawn = sanitizeRefusal(because, 'the reason this section has nothing to draw')
  return drawn.ok ? drawn.text : REFUSAL_UNREADABLE
}

/** refusal answers the console's own sentence when the read carried none, and
 * the CLI's own when it did: a read that was refused at the process (no
 * document, a refusal sentence) is drawn as that sentence, because it is the
 * only account of what happened. */
function refusal(because: string | null | undefined, own: string): string {
  const said = because
  if (said !== undefined && said !== null && said.trim() !== '') return drawnRefusal(said)
  return drawnRefusal(own)
}

/** recordAbsentSentence is the section's own refusal for a document that
 * answered the marks' questions and carries no record at all -- an installation
 * whose CLI does not print one. It is never the "no update recorded yet"
 * sentence: that is a claim about the machine, and this is a claim about the
 * read. */
function recordAbsentSentence(): string {
  return `the read \`${RETIREMENTS_SOURCE_LINE}\` ${RETIREMENTS_ABSENT_WORD}, so this console cannot say what the last update retired`
}

/** setAbsentSentence is the same refusal one level in: a record whose version
 * this build reads and whose `retire` member is not a list. It is refused
 * rather than drawn as an empty set, because `[]` is a set that was written and
 * this is a shape this build does not know. */
function setAbsentSentence(): string {
  return `the last-update record \`${RETIREMENTS_SOURCE_LINE}\` ${RETIREMENTS_SET_WORD}, so this console cannot say what the last update retired`
}

/** versionSentence names both versions, which is the whole of the remedy: the
 * record is state a newer fugaro may have written, and an operator who reads
 * the two numbers knows whether to update the console or read the file by
 * hand. */
function versionSentence(version: unknown): string {
  return `the last-update record this read printed is version ${JSON.stringify(version)}, and ${RETIREMENTS_VERSION_WORD} ${String(RETIREMENTS_RECORD_VERSION)}`
}

/** rowOf reads the keys of one retirement and **only** those: the address, the
 * kind, the cause, the promise, the two names and the flag. The record the CLI
 * holds carries more -- the job and the region the state-removal steps need --
 * and none of it is this screen's to draw (D1485), so the keys are read one by
 * one rather than the object being carried or spread. */
function rowOf(raw: unknown): RetiredResource | null {
  const row = objectAt(raw)
  if (row === null) return null
  const flag = row['protected']
  return {
    address: named(row['address']),
    kind: named(row['kind']),
    cause: named(row['cause']),
    kept: named(row['kept']),
    logical: named(row['logical']),
    secretName: named(row['secret_id']),
    protected: typeof flag === 'boolean' ? flag : null,
  }
}

/** rowsOf reads the set, and answers null when any member of it is not a
 * retirement this build can read: a set half drawn is a set that reads as
 * smaller than it is, which is the one thing a retirement list must not do. */
function rowsOf(raw: unknown): readonly RetiredResource[] | null {
  if (!Array.isArray(raw)) return null
  const rows: RetiredResource[] = []
  for (const entry of raw) {
    const row = rowOf(entry)
    if (row === null) return null
    rows.push(row)
  }
  return rows
}

/**
 * buildRetirementsModel turns the read's document into the section. It touches
 * no element, so every rule below is testable in plain Node.
 *
 * The three answers the read can give are kept apart, because they are three
 * different facts: a record (drawn), `null` (this machine has recorded no
 * update for the project -- "no update recorded yet"), and a document that
 * does not carry the record at all (refused, because that is a read that did
 * not answer and not a machine that has never updated).
 */
export function buildRetirementsModel(inputs: RetirementsInputs): RetirementsModel {
  const document = objectAt(inputs.raw)
  if (document === null) {
    return {
      state: 'unavailable',
      because: refusal(inputs.because, `no document arrived for \`${RETIREMENTS_SOURCE_LINE}\``),
      record: null,
    }
  }
  const raw = document['retirements']
  if (raw === undefined) {
    return { state: 'unavailable', because: refusal(inputs.because, recordAbsentSentence()), record: null }
  }
  if (raw === null) return { state: 'read', because: null, record: null }
  const entry = objectAt(raw)
  if (entry === null) {
    return { state: 'unavailable', because: refusal(inputs.because, recordAbsentSentence()), record: null }
  }
  const version: unknown = entry['version']
  if (version !== RETIREMENTS_RECORD_VERSION) {
    return { state: 'unavailable', because: refusal(inputs.because, versionSentence(version)), record: null }
  }
  const rows = rowsOf(entry['retire'])
  if (rows === null) {
    return { state: 'unavailable', because: refusal(inputs.because, setAbsentSentence()), record: null }
  }
  return {
    state: 'read',
    because: null,
    record: {
      at: named(entry['at']),
      rows,
      runsInFlight: readNumber(entry['runs_in_flight'], 'runs_in_flight'),
    },
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
  return literal(cell(parent, factory, 'dt', RETIREMENTS_TERM_CLASS), text)
}

/** textCell is `cell` with a document's string written into it through
 * `putText`, with the class the wire key declares. */
function textCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  raw: unknown,
  textClass: TextClass,
  field: string,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, 'dd', RETIREMENTS_VALUE_CLASS, ...classes)
  putText(element, raw, textClass, field)
  return element
}

/** numberCell is `cell` with a reading written through `putReading`, so no
 * number can be drawn without its state. */
function numberCell(parent: ScreenElement, factory: ScreenFactory, reading: Reading): ScreenElement {
  const element = cell(parent, factory, 'dd', RETIREMENTS_VALUE_CLASS, RETIREMENTS_NUMBER_CLASS)
  putReading(element, reading, 'count')
  return element
}

/**
 * panelShell is the panel the tab's other sections are drawn with: the same
 * `section.projects-panel`, the same `data-panel`/`data-state`/`data-operation`
 * hooks, the same `h2.projects-title`, the same `span.projects-state` word and
 * the same `span.projects-source` line, so this section reads as a sibling of
 * the panels already on the tab rather than as a card of its own.
 *
 * The "no update recorded yet" sentence is drawn here rather than in a block,
 * because there is no record to put a block around: the section says what it
 * found in the place the record would have been, and it is a `read` -- the read
 * answered -- and not a refusal.
 *
 * The `because` sentence is drawn **only** when the read did not answer, and it
 * is a string this module already put through `sanitizeRefusal` (or one of its
 * own sentences): `drawnRefusal` is where the CLI's wording stops being a
 * document's string.
 */
function panelShell(factory: ScreenFactory, model: RetirementsModel): ScreenElement {
  const element = make(factory, 'section', 'projects-panel')
  element.setAttribute('data-panel', RETIREMENTS_PANEL_KEY)
  element.setAttribute('data-state', model.state)
  element.setAttribute('data-operation', RETIREMENTS_OPERATION)
  literal(cell(element, factory, 'h2', 'projects-title'), RETIREMENTS_TITLE)
  const state = cell(element, factory, 'span', 'projects-state')
  if (model.state === 'read') literal(state, 'read')
  else literal(state, 'not read')
  literal(cell(element, factory, 'span', 'projects-source'), RETIREMENTS_SOURCE_LINE)
  if (model.because !== null) literal(cell(element, factory, 'p', 'projects-because'), model.because)
  else if (model.record === null) {
    literal(cell(element, factory, 'p', RETIREMENTS_STATE_CLASS), RETIREMENTS_NO_RECORD_SENTENCE)
  }
  return element
}

/** factRow is one `dt`/`dd` pair of a definition list, and it is drawn only
 * when the value is there: a row saying "kept: —" would be a promise the record
 * did not make. */
function factRow(
  parent: ScreenElement,
  factory: ScreenFactory,
  termText: string,
  raw: unknown,
  textClass: TextClass,
  field: string,
  ...classes: string[]
): void {
  if (raw === null || raw === undefined) return
  term(parent, factory, termText)
  textCell(parent, factory, raw, textClass, field, ...classes)
}

/**
 * rowBlock is one retired address: its address, its kind, the cause, the
 * promise the update kept where there was one, and the two names and the flag.
 *
 * **The address is a `label`**, not a `path` or a `host`: it is a Terraform
 * address -- dotted module and resource names, no filesystem and no machine --
 * and this console's class list has no member for one, which is the decision
 * `managed.ts` reports for the plan digest rather than makes here. The
 * free-text rules then apply to a low-entropy dotted name, which is what the
 * secrets inventory already draws a resource's own name with (`projects.ts`).
 * The secret is the same answer: a **name**, drawn as itself, with the note
 * saying so (D1485).
 *
 * The `protected` flag is drawn only when the record carried a boolean for it:
 * a row whose flag this build did not read draws no flag rather than an untrue
 * one, and both booleans are facts an operator needs -- not protected is the
 * address the update deleted by itself.
 */
function rowBlock(factory: ScreenFactory, row: RetiredResource, ...classes: string[]): ScreenElement {
  const element = make(factory, 'li', RETIREMENTS_ROW_CLASS, ...classes)
  const facts = cell(element, factory, 'dl', RETIREMENTS_ROW_FACTS_CLASS)
  factRow(facts, factory, RETIREMENTS_ADDRESS_TERM, row.address, 'label', 'address')
  factRow(facts, factory, RETIREMENTS_KIND_TERM, row.kind, 'label', 'kind')
  factRow(facts, factory, RETIREMENTS_CAUSE_TERM, row.cause, 'sentence', 'cause')
  factRow(facts, factory, RETIREMENTS_KEPT_TERM, row.kept, 'sentence', 'kept')
  factRow(facts, factory, RETIREMENTS_LOGICAL_TERM, row.logical, 'label', 'logical')
  if (row.secretName !== null) {
    term(facts, factory, RETIREMENTS_SECRET_TERM)
    const name = textCell(facts, factory, row.secretName, 'label', 'secret_id', RETIREMENTS_SECRET_CLASS)
    name.setAttribute('title', RETIREMENTS_SECRET_NOTE)
  }
  if (row.protected !== null) {
    term(facts, factory, RETIREMENTS_PROTECTED_TERM)
    const word = cell(facts, factory, 'dd', RETIREMENTS_VALUE_CLASS, RETIREMENTS_WORD_CLASS)
    if (row.protected) literal(word, RETIREMENTS_PROTECTED_WORD)
    else literal(word, RETIREMENTS_UNPROTECTED_WORD)
  }
  return element
}

/**
 * recordBlock is the record: when the update settled its retirement and how
 * many runs the registry held while it did, and then either the set -- one row
 * per address -- or the sentence for an update that retired nothing.
 *
 * **The time and the runs are drawn beside the empty sentence and never
 * instead of it.** They are facts about the same run, and an operator who reads
 * "nothing retired" wants to know which update said so and what it walked past;
 * a block that dropped them would leave the operator unable to tell this
 * update's answer from an older one's.
 *
 * `runs_in_flight` goes through `putReading`, so an absent count is `n/a` with
 * its reason and never a zero (D1483): "no run was in flight" is a claim about
 * the registry, and an absent member is a claim about the read.
 */
function recordBlock(factory: ScreenFactory, record: RetirementsRecord): ScreenElement {
  const element = make(factory, 'section', RETIREMENTS_RECORD_CLASS)
  literal(cell(element, factory, 'h3', RETIREMENTS_RECORD_HEAD_CLASS), RETIREMENTS_RECORD_TITLE)
  const facts = cell(element, factory, 'dl', RETIREMENTS_RECORD_LIST_CLASS)
  factRow(facts, factory, RETIREMENTS_AT_TERM, record.at, 'label', 'at')
  term(facts, factory, RETIREMENTS_INFLIGHT_TERM)
  numberCell(facts, factory, record.runsInFlight)
  if (record.rows.length === 0) {
    literal(cell(element, factory, 'p', RETIREMENTS_STATE_CLASS), RETIREMENTS_NONE_SENTENCE)
    return element
  }
  literal(cell(element, factory, 'h4', RETIREMENTS_SET_HEAD_CLASS), RETIREMENTS_SET_TITLE)
  const list = cell(element, factory, 'ul', RETIREMENTS_SET_LIST_CLASS)
  for (const row of record.rows) list.append(rowBlock(factory, row))
  return element
}

/** RetirementsFrame is the section's shell: the host the entry mounts, and the
 * slot its busy mark goes in. */
export interface RetirementsFrame {
  readonly root: ScreenElement
  readonly slot: ScreenElement
}

/**
 * renderRetirementsFrame is the section's host and its slot.
 *
 * **The slot is the host's first child**, so the mark a control change puts up
 * in the same frame sits at the top of this section rather than under a block
 * nobody has scrolled to, and `entry.ts`'s `clearSection(host, slot)` empties
 * the section back to it. It is the tab's own `console-busy-slot`, so one
 * section's mark cannot take another's down: the two hosts are separate
 * elements and each wears its own `data-busy`.
 */
export function renderRetirementsFrame(factory: ScreenFactory): RetirementsFrame {
  const root = make(factory, 'div', RETIREMENTS_CLASS)
  const slot = cell(root, factory, 'div', BUSY_SLOT_CLASS)
  return { root, slot }
}

/** fillRetirements draws the section into its host: the panel, and then the
 * record block -- and only when the read answered with a record. A refusal
 * draws the panel alone, because a block under it would read as an update that
 * retired nothing rather than as a read that did not answer. */
export function fillRetirements(
  factory: ScreenFactory,
  model: RetirementsModel,
  host: ScreenElement,
): void {
  host.append(panelShell(factory, model))
  const record = model.record
  if (model.state !== 'read' || record === null) return
  host.append(recordBlock(factory, record))
}

/** renderRetirements is the whole section for one model: the frame and the fill
 * in one call. The entry mounts the two separately -- it needs the slot before
 * any read lands -- so this is for the callers that do not. */
export function renderRetirements(factory: ScreenFactory, model: RetirementsModel): ScreenElement {
  const frame = renderRetirementsFrame(factory)
  fillRetirements(factory, model, frame.root)
  return frame.root
}
