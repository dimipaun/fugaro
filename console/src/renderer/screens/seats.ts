// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The seats section of the Project tab: the advisory estimate of how many
// people a subscription's seat rule would count, drawn beside the seats the
// licence key carries (the subscriptions plan's S4.2; the read behind it is
// `fugaro seats --json`, S4.1, and the rulings are D439, D433 and T14).
//
// **Seats are compared, never enforced (D439).** This module draws two figures
// next to each other and one sentence when the first is above the second. It
// offers no control, refuses nothing, holds no other section back and carries no
// state the tab is gated on: a console that could refuse over a seat count would
// be enforcing a contract pricing rule, and the ruling says nobody does.
//
// **The estimate is the CLI's, and the key's seats are the licence section's.**
// The people are the document's own rows -- one identity each, however many
// lists named them (S4.1) -- and the figure this section draws beside them is
// read out of the *licence* section's model: `buildSeats` finds the seats row
// `buildLicence` made (`LICENCE_SEATS_TERM`) rather than decoding the key's
// reply a second time. One reader of a key, one answer about it, and the number
// an operator reads here is the number they read one panel over.
//
// **Every figure is a reading.** The count of people is `estimated`, and a
// lower bound whenever the document listed a member it cannot count or a run it
// could not read -- the estimate is a floor by construction, never a ceiling
// (S4.1) -- while the key's seats are `measured` or `n/a` with one of this
// module's own sentences naming the fact that is missing. No path here draws a
// bare number, and no path draws a `0` for a key that is not there.
//
// **An identity reaches the screen through its own door.** A person is the
// `person` class, which draws an address as the console's marker and a login
// whole; a member the estimate lists and does not count is a group, a domain or
// an account and not a person at all, so it is drawn as the `label` it is --
// refusing it would hide the very row the ruling exists to show -- and the
// reason beside it is one of this module's own sentences, matched from the
// document's code against a closed table.
//
// The document's `notes` are **not** drawn: they are the CLI's prose for a human
// reading a terminal, and every sentence a reader needs here is this file's own
// (the advisory, the warning, the floor). What the console says about the
// estimate is the console's to say.

import { estimated, measured, unavailable } from '../../shared/availability'
import type { Reading } from '../../shared/availability'
import { sanitizeRefusal } from '../../shared/sanitize'
import { SEATS_CLI_FLOOR } from '../../shared/version'
import { putReading, putText } from '../app'
import type { ElementLike } from '../app'
import { REFUSAL_UNREADABLE } from './degraded'
import { LICENCE_NO_KEY_WORD, LICENCE_SEATS_TERM } from './licence'
import type { LicenceModel } from './licence'

/** ScreenElement and ScreenFactory are the drawing surface, declared here as
 * every screen declares its own: the entry's `DomScreenFactory` is assignable to
 * it, and so is the test document's. */
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

/** SEATS_CLASS is the panel itself, and it carries the `projects-` prefix
 * because the tab it lands in is the Project tab: the theme's hook selector is
 * keyed by the screen a panel belongs to. */
export const SEATS_CLASS = 'projects-seats'

export const SEATS_TITLE_CLASS = 'projects-seats-title'
export const SEATS_STATE_CLASS = 'projects-seats-state'
export const SEATS_SOURCE_CLASS = 'projects-seats-source'
export const SEATS_WINDOW_CLASS = 'projects-seats-window'
export const SEATS_FIGURES_CLASS = 'projects-seats-figures'
export const SEATS_TERM_CLASS = 'projects-seats-term'
export const SEATS_VALUE_CLASS = 'projects-seats-value'
export const SEATS_WARNING_CLASS = 'projects-seats-warning'
export const SEATS_ADVISORY_CLASS = 'projects-seats-advisory'
export const SEATS_FLOOR_CLASS = 'projects-seats-floor'
export const SEATS_EMPTY_CLASS = 'projects-seats-empty'
export const SEATS_BECAUSE_CLASS = 'projects-seats-because'
export const SEATS_PEOPLE_CLASS = 'projects-seats-people'
export const SEATS_PERSON_CLASS = 'projects-seats-person'
export const SEATS_PERSON_NAME_CLASS = 'projects-seats-person-name'
export const SEATS_SOURCES_CLASS = 'projects-seats-sources'
export const SEATS_NOTCOUNTED_LIST_CLASS = 'projects-seats-not-counted-list'
export const SEATS_NOTCOUNTED_CLASS = 'projects-seats-not-counted'
export const SEATS_MEMBER_CLASS = 'projects-seats-member'
export const SEATS_WHY_CLASS = 'projects-seats-why'

/** SEATS_OPERATION is the read behind this section, in the surface's own
 * spelling (`src/main/ipc.ts`, S4.2). It is a read: a lapse never refuses it
 * (`docs/design/lapse-read-only.md` wraps the writes only), and this section
 * draws no control that a lapse would have to gate. */
export const SEATS_OPERATION = 'seats.read'

/** SEATS_SOURCE is what the panel names as its source, the way every panel of
 * this console names its own (design section 6.2): the command whose estimate
 * this is. The key's seats come from the licence section's own read, which is
 * named one panel over rather than here. */
export const SEATS_SOURCE = 'fugaro seats --json'

/** SEATS_TITLE is the panel's heading, the console's own words. */
export const SEATS_TITLE = 'Seats'

/** SEATS_BUSY is the section's mark while its read is out. */
export const SEATS_BUSY = 'Reading the seat estimate…'

/** SEATS_USED_TERM and SEATS_LICENSED_TERM are the two figures' own names. They
 * are drawn as a pair and in this order, because the section exists to compare
 * them: the estimate first, the figure it is compared against beside it. */
export const SEATS_USED_TERM = 'Seats used'
export const SEATS_LICENSED_TERM = 'Seats on the key'

/** SEATS_WINDOW_TERM is the label the window the estimate was taken over is
 * drawn under. The window itself is the document's (`days`, `since`, `until`),
 * so it goes through `putText` and not through this file's words. */
export const SEATS_WINDOW_TERM = 'Window'

/**
 * SEATS_OVER_WARNING, SEATS_ADVISORY and SEATS_FLOOR are what the section says
 * about the figures, in this console's words, and each is drawn only where it is
 * true:
 *
 *   - the warning is drawn when the estimate is above the key's seats. It says
 *     the overage **and** that nothing is blocked, because the number alone
 *     reads as a failure a reader would look for a way to fix;
 *   - the advisory is drawn in every state. What this panel is -- an estimate of
 *     who launches jobs, from this installation's own data, and never a count of
 *     licence seats -- is the same fact whether or not the read answered;
 *   - the floor is drawn whenever the estimate is a lower bound, which is
 *     whenever the document listed a member it cannot count or a run it could
 *     not read. Without it the `≥` beside the figure is a mark nobody can
 *     explain.
 */
export const SEATS_OVER_WARNING =
  'Above the seats this key carries: the figure is an estimate, it is advisory, and nothing here is blocked by it.'
export const SEATS_ADVISORY =
  'Advisory: an estimate of who launches jobs on this installation, from the installation’s own data, and never a count of licence seats.'
export const SEATS_FLOOR =
  'A lower bound: a member the estimate cannot size, or a run it could not read, is counted nowhere — so the figure is a floor and not a ceiling.'

/**
 * SEATS_CLI_FLOOR_WORD introduces the console's own sentence for a CLI below the
 * seats command's floor, and **its trailing space is the sentence's**: the
 * version line follows it and the two are one string. The `CLI` in its name is
 * this file's one hedge against a collision of its own: `SEATS_FLOOR` above is
 * the estimate's lower bound, and the two floors are different facts.
 *
 * `fugaro seats` landed in v0.21.0 (`SEATS_CLI_FLOOR`), so a CLI older than that
 * has no such command and answers `unknown command "seats"` -- a failure the
 * operator can do nothing about. The floor is asked before the read goes out
 * (`src/renderer/entry.ts`), and below it the panel spawns nothing and draws
 * this.
 */
export const SEATS_CLI_FLOOR_WORD = `the seat estimate needs fugaro ${SEATS_CLI_FLOOR} and this CLI is `

/** SEATS_EMPTY is what an estimate with nobody in it says. An empty table would
 * read as a failure; this is the document's own fact, said plainly (S4.1 prints
 * the same sentence for a terminal reader). */
export const SEATS_EMPTY =
  'No person is counted: neither list names one and no run of the window recorded a requester.'

/**
 * SEATS_WHY_WORDS is the document's two `not_counted` reasons in this console's
 * words, and SEATS_WHY_UNREADABLE is what a third one is drawn as. The reasons
 * are values of the document's own (`unknown_size`, `account`, S4.1), so the row
 * is drawn from this table and never from the string the document carried: a
 * reason a later CLI grew is a word nobody here can interpret, and drawing it as
 * itself would be the section inventing a meaning for it -- so `whyWord` asks
 * this table whether it **owns** the code, for `rigor.ts`'s reason: a bare index
 * answers for `constructor`, `toString` and `__proto__` too, and the row would
 * carry a native function's source or `[object Object]` where this module's own
 * sentence belongs.
 */
export const SEATS_WHY_WORDS: Readonly<Record<string, string>> = Object.freeze({
  unknown_size: 'an unknown number of people',
  account: 'an account, not a person',
})
export const SEATS_WHY_UNREADABLE = 'a reason this build of the console does not read'

/** SEATS_NO_KEY_BECAUSE, SEATS_LICENCE_UNREAD_BECAUSE and
 * SEATS_LICENCE_NO_SEATS_BECAUSE are the three facts that leave the key's seats
 * unusable, one sentence each. They are this module's own words and they are
 * drawn as the `n/a`'s title, so a reader learns *which* fact is missing rather
 * than being shown a comparison the console cannot make. */
export const SEATS_NO_KEY_BECAUSE =
  'no licence key is installed, so this console holds no seat count to compare the estimate against'
export const SEATS_LICENCE_UNREAD_BECAUSE =
  'the licence key could not be read, so this console holds no seat count to compare the estimate against'
export const SEATS_LICENCE_NO_SEATS_BECAUSE =
  'the licence key carries no seat count, so there is nothing to compare the estimate against'

/** SEATS_SHAPE_BECAUSE is the refusal for an answer that arrived and is not the
 * shape this section reads: a decoder that guessed would be a decoder that draws
 * an estimate out of a reply that does not mean one (reconciliation section 5).
 */
export const SEATS_SHAPE_BECAUSE =
  'the console answered something that is not a seat estimate: no table of people, no window and no count of the runs it could not read, so none of it is drawn here'

/** SEATS_ABSENT_BECAUSE is the refusal for a read that produced nothing at all:
 * the bridge itself answered `null`, which is what a member that is missing or
 * that failed to answer looks like from here. */
export const SEATS_ABSENT_BECAUSE =
  'the console did not answer, so what this installation’s seat estimate is, is not known here'

/** SeatsPerson is one identity the estimate counts: the text the document
 * keyed it by, and the sources that named it, already joined. Both go through
 * `putText` -- the person through the `person` class and the sources through
 * `label` -- so neither is drawn as it arrived. */
export interface SeatsPerson {
  readonly person: string
  readonly sources: string
}

/** SeatsFlagged is one identity the estimate lists and does not count, with the
 * reason already in this console's words (`SEATS_WHY_WORDS`). */
export interface SeatsFlagged {
  readonly member: string
  readonly sources: string
  readonly why: string
}

/**
 * SeatsInputs is what the section is built from: the answer **as it arrived**,
 * the licence section's own model, and the console's sentence for a read that
 * was refused.
 *
 * The answer is read here rather than decoded elsewhere, because the section
 * reads its own reply: it checks the members it draws and answers a refusal
 * rather than figures it cannot vouch for. The licence model is passed in rather
 * than built here, so the key is decoded once and one panel's answer about it is
 * the other's.
 */
export interface SeatsInputs {
  readonly raw: unknown
  readonly licence: LicenceModel
  readonly because: string | null
  /** tooOld is whether the CLI in use predates the command this section reads
   * (`seatsSupported`). It is a fourth input rather than something the read
   * answers, because below the floor no read is made at all: the entry asks the
   * floor before the child goes out, and hands the answer in. */
  readonly tooOld: boolean
  /** version is the CLI line the floor was asked about, drawn **only** in the
   * floor sentence above (`SEATS_CLI_FLOOR_WORD`). */
  readonly version: string
}

/**
 * SeatsModel is the section's model: whether the read answered, the sentence
 * beside it when it did not, the two figures, whether one is above the other,
 * the window, and the document's two tables as rows.
 *
 * `because` is the reason **as it will be drawn**: `buildSeats` puts it through
 * `sanitizeRefusal` (`drawnRefusal`) before it is stored, so the drawing writes
 * a sentence this console produced and never an answer's own string.
 *
 * `used` and `licensed` are readings and not numbers, so there is no shape of
 * this model in which a figure the console does not have is drawn as a zero. In
 * the `unavailable` case both carry the reason and neither is drawn.
 */
export interface SeatsModel {
  readonly drawn: 'answer' | 'unavailable'
  readonly because: string | null
  readonly used: Reading
  readonly licensed: Reading
  readonly over: boolean
  readonly window: string | null
  readonly people: readonly SeatsPerson[]
  readonly flagged: readonly SeatsFlagged[]
}

/** SeatsDrawn is what the drawing answers: the panel, and nothing else. There is
 * no control in this section, so there is no handle for the wiring to keep. */
export interface SeatsDrawn {
  readonly root: ScreenElement
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

/**
 * drawnRefusal is a reason as a screen may draw it: `sanitizeRefusal` and
 * nothing else, which is the one treatment of a sentence this console did not
 * compose (design section 6.3, `src/shared/sanitize.ts`). The CLI's refusals
 * name buckets, projects and roles, so they take this door and not the free-text
 * rules -- which would draw `roles/datastore.viewer` as `[redacted]` and eat the
 * remedy -- while a credential a child echoed is still redacted. A sentence too
 * long for the console to read is `REFUSAL_UNREADABLE` (`./degraded`, the
 * console's one word for it) rather than a cell the operator reads as the
 * refusal.
 */
function drawnRefusal(because: string): string {
  const drawn = sanitizeRefusal(because, 'the reason this section has to draw')
  return drawn.ok ? drawn.text : REFUSAL_UNREADABLE
}

/** sourcesOf is a row's `sources` member as the cell it is drawn in: the texts
 * the document carried, joined, or null when the member is not the list of
 * strings the document's own shape says it is. */
function sourcesOf(raw: unknown): string | null {
  if (!Array.isArray(raw)) return null
  const named: string[] = []
  for (const one of raw) {
    const text = textAt(one)
    if (text === null) return null
    named.push(text)
  }
  return named.join(', ')
}

/** personRows is the document's `people` table as rows, or null when it is not
 * that table. A row this read cannot vouch for is the whole answer refused, and
 * not a row skipped: an estimate silently short of a person is a figure nobody
 * can check. */
function personRows(raw: unknown): readonly SeatsPerson[] | null {
  if (!Array.isArray(raw)) return null
  const rows: SeatsPerson[] = []
  for (const one of raw) {
    const row = objectAt(one)
    if (row === null) return null
    const person = textAt(row['person'])
    const sources = sourcesOf(row['sources'])
    if (person === null || sources === null) return null
    rows.push({ person, sources })
  }
  return rows
}

/** whyWord is one `not_counted` reason in this file's own words. The lookup is
 * an ownership check and not an index: `code` is a string the document chose,
 * and an ordinary object literal answers `obj['constructor']`, `obj['toString']`
 * and `obj['__proto__']` from `Object.prototype`, so the fallback this table
 * exists for would be defeated by exactly the input it is there for. */
function whyWord(code: string): string {
  const word = Object.hasOwn(SEATS_WHY_WORDS, code) ? SEATS_WHY_WORDS[code] : undefined
  return word ?? SEATS_WHY_UNREADABLE
}

/** flaggedRows is the document's `not_counted` table as rows, with each reason
 * through this file's own table. A reason the table does not hold is drawn as
 * `SEATS_WHY_UNREADABLE` -- never as the code the document carried. */
function flaggedRows(raw: unknown): readonly SeatsFlagged[] | null {
  if (!Array.isArray(raw)) return null
  const rows: SeatsFlagged[] = []
  for (const one of raw) {
    const row = objectAt(one)
    if (row === null) return null
    const member = textAt(row['member'])
    const code = textAt(row['why'])
    const sources = sourcesOf(row['sources'])
    if (member === null || code === null || sources === null) return null
    rows.push({ member, sources, why: whyWord(code) })
  }
  return rows
}

/**
 * licensedReading is the seats the licence key carries, out of the licence
 * section's own model: the `measured` count the verifier read, or `n/a` with the
 * sentence naming which of the three facts is missing.
 *
 * The row is found by the term `buildLicence` wrote it under and its value is
 * parsed back, so a row drawn as a number is the only row this can find: the
 * builder emits a seats row only for a finite number (S2.7), and the guard is
 * here anyway -- `Number('')` is `0`, and a key with no seat count must be an
 * `n/a` and never a zero the estimate would be compared against.
 */
function licensedReading(licence: LicenceModel): Reading {
  if (licence.drawn !== 'answer') return unavailable(SEATS_LICENCE_UNREAD_BECAUSE)
  if (licence.word === LICENCE_NO_KEY_WORD) return unavailable(SEATS_NO_KEY_BECAUSE)
  const row = licence.claims.find((claim) => claim.term === LICENCE_SEATS_TERM)
  if (row === undefined) return unavailable(SEATS_LICENCE_NO_SEATS_BECAUSE)
  const count = Number(row.value)
  if (row.value.trim() === '' || !Number.isFinite(count)) {
    return unavailable(SEATS_LICENCE_NO_SEATS_BECAUSE)
  }
  return measured(count)
}

/** unavailableModel is the section with no figures: the reason, and the two
 * readings that carry it so no caller has to invent an empty pair. Nothing in
 * the drawing reads them in this case. */
function unavailableModel(licence: LicenceModel, because: string): SeatsModel {
  return {
    drawn: 'unavailable',
    because: drawnRefusal(because),
    used: unavailable(because),
    licensed: licensedReading(licence),
    over: false,
    window: null,
    people: [],
    flagged: [],
  }
}

/**
 * buildSeats turns the read's own answer into this section's model.
 *
 * **Below the command's floor there is nothing to read** (`SEATS_CLI_FLOOR_WORD`):
 * the model is the unavailable one, carrying the sentence naming the release the
 * panel needs, and neither figure is drawn -- not as a zero and not as an `n/a`,
 * because the console is not saying "no seat count", it is saying it cannot ask.
 *
 * **Three rules are decided here and nowhere else.**
 *
 *   - **a reply this build cannot read is refused whole** (`SEATS_SHAPE_BECAUSE`,
 *     `SEATS_ABSENT_BECAUSE`): an answer that is not the document's shape is a
 *     sentence rather than a table with a row missing, because a figure drawn
 *     from a reply the console does not understand is a figure that looks right.
 *   - **the estimate is a lower bound exactly when the document says so**: it is
 *     `estimated` -- the CLI's own word for a value it computed rather than
 *     measured -- and `lowerBound` when a member was listed and not counted or a
 *     run could not be read (S4.1's `not_counted` and `unreadable`).
 *   - **the overage is a comparison and nothing else** (`over`): it is true only
 *     when there is a seat count to compare against and the estimate is above
 *     it. Equality is not over it, and an `n/a` beside a figure is never an
 *     overage -- the section has nothing to warn about and says nothing.
 *
 * It touches no element, so every one of those is testable in plain Node.
 */
export function buildSeats(inputs: SeatsInputs): SeatsModel {
  // The floor is asked first, and it is not a refusal: the read never went out,
  // and the sentence names the release this panel needs rather than a failure
  // the operator cannot act on (`SEATS_CLI_FLOOR_WORD`; `modelsListSupported`'s
  // panel is drawn the same way one panel over).
  if (inputs.tooOld) return unavailableModel(inputs.licence, `${SEATS_CLI_FLOOR_WORD}${inputs.version}`)
  if (inputs.because !== null) return unavailableModel(inputs.licence, inputs.because)
  const doc = objectAt(inputs.raw)
  if (doc === null) return unavailableModel(inputs.licence, SEATS_ABSENT_BECAUSE)
  const people = personRows(doc['people'])
  const flagged = flaggedRows(doc['not_counted'])
  const days = doc['days']
  const since = textAt(doc['since'])
  const until = textAt(doc['until'])
  const unreadable = doc['unreadable']
  if (
    people === null ||
    flagged === null ||
    since === null ||
    until === null ||
    typeof days !== 'number' ||
    !Number.isFinite(days) ||
    typeof unreadable !== 'number' ||
    !Number.isFinite(unreadable)
  ) {
    return unavailableModel(inputs.licence, SEATS_SHAPE_BECAUSE)
  }
  const licensed = licensedReading(inputs.licence)
  const used = estimated(people.length, { lowerBound: flagged.length > 0 || unreadable > 0 })
  return {
    drawn: 'answer',
    because: null,
    used,
    licensed,
    over: licensed.value !== null && people.length > licensed.value,
    window: `the last ${String(days)} days: ${since} to ${until}`,
    people,
    flagged,
  }
}

/** make is one element of a class, in the shape every screen of this console
 * declares it: variadic, so a call site passes this file's own class constants
 * and nothing else. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  if (classes.length > 0) element.setAttribute('class', classes.join(' '))
  return element
}

/** literal writes one of this file's own words. An answer's string never reaches
 * it: those go through `putText` (`../app.ts`) or through `drawnRefusal`. */
function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

/** figure is one row of the pair: the term in this file's own words and the
 * reading through `putReading`, which is the only door a number has onto a
 * screen (design section 6.1 rule 1). */
function figure(
  factory: ScreenFactory,
  term: string,
  reading: Reading,
  list: ScreenElement,
): void {
  const name = make(factory, 'dt', SEATS_TERM_CLASS)
  literal(name, term)
  list.append(name)
  const value = make(factory, 'dd', SEATS_VALUE_CLASS)
  putReading(value, reading, 'count')
  list.append(value)
}

/** personLine is one identity the estimate counts: the person through the
 * `person` class -- an address is the console's marker, a login is drawn whole,
 * and anything else is refused with the reason in the title -- and the sources
 * that named them through `label`, in the words the document's own enumeration
 * uses (`launcher`, `operator`, `requester`). */
function personLine(factory: ScreenFactory, row: SeatsPerson, list: ScreenElement): void {
  const line = make(factory, 'li', SEATS_PERSON_CLASS)
  const person = make(factory, 'span', SEATS_PERSON_NAME_CLASS)
  putText(person, row.person, 'person', 'the person a seat estimate counted')
  line.append(person)
  const sources = make(factory, 'span', SEATS_SOURCES_CLASS)
  putText(sources, row.sources, 'label', 'the sources naming one identity')
  line.append(sources)
  list.append(line)
}

/** flaggedLine is one identity the estimate lists and does not count. The member
 * is not a person -- a group, a domain or an account -- so it takes the `label`
 * class: the `person` class would refuse it and the row would be drawn as `n/a`,
 * which hides the very thing the ruling asks to be shown. Its reason is this
 * module's own sentence (see `SEATS_WHY_WORDS`). */
function flaggedLine(factory: ScreenFactory, row: SeatsFlagged, list: ScreenElement): void {
  const line = make(factory, 'li', SEATS_NOTCOUNTED_CLASS)
  const member = make(factory, 'span', SEATS_MEMBER_CLASS)
  putText(member, row.member, 'label', 'a member a seat estimate could not count')
  line.append(member)
  const sources = make(factory, 'span', SEATS_SOURCES_CLASS)
  putText(sources, row.sources, 'label', 'the sources naming one identity')
  line.append(sources)
  const why = make(factory, 'span', SEATS_WHY_CLASS)
  literal(why, row.why)
  line.append(why)
  list.append(line)
}

/**
 * renderSeats draws the whole section: the panel's own name, whether the read
 * landed, what it names as its source, the two figures beside each other, the
 * warning and the notes that are true of them, and the document's two tables.
 *
 * **It draws no control, in any state.** There is nothing to accept, nothing to
 * refuse and nothing to send: the section is a reading (D439).
 */
export function renderSeats(factory: ScreenFactory, model: SeatsModel): SeatsDrawn {
  const root = make(factory, 'section', SEATS_CLASS)
  root.setAttribute('data-operation', SEATS_OPERATION)
  const title = make(factory, 'h2', SEATS_TITLE_CLASS)
  literal(title, SEATS_TITLE)
  root.append(title)
  const state = make(factory, 'span', SEATS_STATE_CLASS)
  literal(state, model.drawn === 'answer' ? 'read' : 'not read')
  root.append(state)
  const source = make(factory, 'span', SEATS_SOURCE_CLASS)
  literal(source, SEATS_SOURCE)
  root.append(source)
  if (model.because !== null) {
    // The reason is drawn, not paraphrased: `buildSeats` already put it through
    // `sanitizeRefusal` (`drawnRefusal`), so this is a sentence this console
    // produced.
    const because = make(factory, 'p', SEATS_BECAUSE_CLASS)
    literal(because, model.because)
    root.append(because)
  }
  if (model.drawn === 'answer') {
    if (model.window !== null) {
      const window = make(factory, 'p', SEATS_WINDOW_CLASS)
      putText(window, model.window, 'label', 'the window this estimate was taken over')
      root.append(window)
    }
    const figures = make(factory, 'dl', SEATS_FIGURES_CLASS)
    figure(factory, SEATS_USED_TERM, model.used, figures)
    figure(factory, SEATS_LICENSED_TERM, model.licensed, figures)
    root.append(figures)
    if (model.over) {
      const warning = make(factory, 'p', SEATS_WARNING_CLASS)
      literal(warning, SEATS_OVER_WARNING)
      root.append(warning)
    }
    if (model.used.lowerBound) {
      const floor = make(factory, 'p', SEATS_FLOOR_CLASS)
      literal(floor, SEATS_FLOOR)
      root.append(floor)
    }
    if (model.people.length === 0) {
      // An estimate with nobody in it is said plainly, not shown as an empty
      // table a reader could take for a failure.
      const empty = make(factory, 'p', SEATS_EMPTY_CLASS)
      literal(empty, SEATS_EMPTY)
      root.append(empty)
    } else {
      const people = make(factory, 'ul', SEATS_PEOPLE_CLASS)
      for (const row of model.people) personLine(factory, row, people)
      root.append(people)
    }
    if (model.flagged.length > 0) {
      // The second table is what the estimate cannot count, and why: a group's
      // size is unknowable from here, and an account is not a person. Both are
      // listed, so nothing a reader might have expected to count is silently
      // missing.
      const flagged = make(factory, 'ul', SEATS_NOTCOUNTED_LIST_CLASS)
      for (const row of model.flagged) flaggedLine(factory, row, flagged)
      root.append(flagged)
    }
  }
  const advisory = make(factory, 'p', SEATS_ADVISORY_CLASS)
  literal(advisory, SEATS_ADVISORY)
  root.append(advisory)
  return { root }
}

/**
 * fillSeats draws the section into the host the tab gave it, and it is one
 * read's filler: `seats.read` lands apart from the tab's other reads, so this
 * panel is drawn the moment its answer arrives and nothing waits for another.
 *
 * It appends to the host and not to a slot inside itself: the mark of a read
 * that is still out belongs to the entry, in a slot of its own beside this
 * section (`renderBusy`, `../section.ts`), which is what keeps one read's mark
 * from taking another read's content down.
 */
export function fillSeats(
  factory: ScreenFactory,
  model: SeatsModel,
  host: ScreenElement,
): SeatsDrawn {
  const drawn = renderSeats(factory, model)
  host.append(drawn.root)
  return drawn
}
