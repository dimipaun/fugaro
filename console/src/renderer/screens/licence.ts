// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The licence key, as a section of the Project tab: where the operator pastes
// the key the installation runs under, and where the console says what the
// verifier makes of the key it already holds (the subscriptions plan's S2.7;
// the design is `docs/licensing/licence-key.md`, whose "Storage and hand-off
// (F41)" table is the row this screen exists for).
//
// **The console's row of F41's table is a store of its own on the machine, one
// per installation, and the operator's route into it is a paste.** This module
// is the paste and the reading back: `licence.read` answers what the *verifier*
// sees of the key the active installation holds -- the state and the claim set,
// or the sentence that refuses it -- and `licence.save` takes the text the
// operator pasted, verifies it before anything is kept, and refuses the whole
// paste when it does not verify (`src/main/licence-store.ts`, S2.4's verifier).
//
// **The key itself is never drawn, and never comes back.** Neither answer
// carries the key text: the read answers a state and a claim set, and the save
// answers the same reply the read does. So this screen has nothing to redact and
// nothing to copy -- what an operator reads here is a tier, an organisation, a
// seat count and two days, which is exactly what the design says a key *is*
// (`docs/licensing/licence-key.md`, "the claim set").
//
// **No link, and no host of this project's own.** The screen carries no anchor,
// no URL and no "where to buy one" sentence: console code names no host of the
// project's own (the plan's own rule for this task), and the two operations
// behind this section read and write the console's own store rather than
// reaching anything. An installation with no key is a state and not an error
// (`docs/design/lapse-read-only.md` section 2), so the section offers the box
// and says nothing about an address.
//
// **The screen is never gated, and this module knows nothing about a lapse.**
// "The key screen in particular is never gated" is that design's rule for it --
// a state a user cannot walk out of is a state that traps them -- so the section
// draws whatever state the verifier answers (`valid`, `grace`, `expired` or none
// at all) and keeps the box in every one of them: a key is what walks an
// installation out of a lapse.
//
// Nothing here touches an element outside the host it is handed, reads a
// document's field without checking its type, or draws a string that did not
// come from `putText` or from one of this file's own constants.

import { sanitizeRefusal } from '../../shared/sanitize'
import type { TextClass } from '../../shared/sanitize'
import { putText } from '../app'
import type { ElementLike } from '../app'
import { REFUSAL_UNREADABLE } from './degraded'

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

/** LICENCE_CLASS is the panel itself, and it carries the `projects-` prefix
 * because the tab it lands in is the Project tab: the theme's hook selector is
 * keyed by the screen a panel belongs to. */
export const LICENCE_CLASS = 'projects-licence'

export const LICENCE_TITLE_CLASS = 'projects-licence-title'
export const LICENCE_STATE_CLASS = 'projects-licence-state'
export const LICENCE_SOURCE_CLASS = 'projects-licence-source'
export const LICENCE_WORD_CLASS = 'projects-licence-word'
export const LICENCE_BECAUSE_CLASS = 'projects-licence-because'
export const LICENCE_CLAIMS_CLASS = 'projects-licence-claims'
export const LICENCE_TERM_CLASS = 'projects-licence-term'
export const LICENCE_VALUE_CLASS = 'projects-licence-value'
export const LICENCE_FORM_CLASS = 'projects-licence-form'
export const LICENCE_BOX_CLASS = 'projects-licence-box'
export const LICENCE_HINT_CLASS = 'projects-licence-hint'
export const LICENCE_SUBMIT_CLASS = 'projects-licence-submit'
export const LICENCE_OUTCOME_CLASS = 'projects-licence-outcome'

/** LICENCE_OPERATION and LICENCE_SAVE_OPERATION are the two operations this
 * section draws, in the surface's own spellings (`src/main/ipc.ts`, S2.7). They
 * are the first two rows of that table whose command is `null`: the console
 * keeps the key itself, so there is no argv for a key to be in -- which is
 * S2.1's hand-off read from this side, and the "never in argv" case the store's
 * own test asserts one layer down. */
export const LICENCE_OPERATION = 'licence.read'
export const LICENCE_SAVE_OPERATION = 'licence.save'

/** LICENCE_SOURCE is what the panel names as its source, the way every panel of
 * this console names its own (design section 6.2). There is no command line to
 * name -- the two operations above spawn nothing -- so it names the store
 * instead, in this console's own words. */
export const LICENCE_SOURCE = 'the console’s own licence store'

/** LICENCE_TITLE is the panel's heading, the console's own words. */
export const LICENCE_TITLE = 'Licence key'

/** LICENCE_BUSY is the section's mark while its read is out. */
export const LICENCE_BUSY = 'Reading the licence key…'

/** LICENCE_LABEL, LICENCE_PLACEHOLDER and LICENCE_HINT are the box's own words:
 * what it is, where the text comes from, and what happens to it. The hint is the
 * one sentence this screen says about the hand-off, and it says the design's own
 * fact rather than a mechanism: the key is kept on this machine for this
 * installation and handed to the CLI this console spawns, never on a command
 * line. */
export const LICENCE_LABEL = 'Licence key'
export const LICENCE_PLACEHOLDER = 'Paste the key'
export const LICENCE_HINT =
  'Kept on this machine for this installation, and handed to the fugaro this console spawns — never on a command line.'

/** LICENCE_SUBMIT_WORD is the control that keeps what was pasted. */
export const LICENCE_SUBMIT_WORD = 'Keep this key'

/** LICENCE_NO_KEY_WORD is what an installation with no key is drawn as. It is a
 * real state and not an error: a console newly installed as a subscription build
 * has none, and an installation that has never been given a key is not broken. */
export const LICENCE_NO_KEY_WORD = 'no key'

/** LICENCE_REFUSED_WORD is the state of an installation that **holds a key
 * nothing verifies**: a file written by something other than this console, or a
 * key whose id this build does not hold. It is drawn as a state rather than as
 * an absent key, because the two ask different things of the operator -- this
 * one wants a working key pasted over it. */
export const LICENCE_REFUSED_WORD = 'refused'

/** LICENCE_STATE_WORDS are the verifier's three states in this console's own
 * words (S2.1's states). A state word outside this table is not drawn as itself:
 * it is a document this build does not read, which is the shape refusal below,
 * so a fourth state a later CLI grew is a sentence rather than a word an
 * operator would have to interpret. */
export const LICENCE_STATE_WORDS: Readonly<Record<string, string>> = Object.freeze({
  valid: 'valid',
  grace: 'in grace',
  expired: 'expired',
})

/** LICENCE_TERMS are the claim set's fields in this console's words, in the
 * order they are drawn. They are the five a reader of a key needs, plus the
 * feature list when the document carries one; a field the document leaves out
 * draws no row at all rather than an empty cell. */
export const LICENCE_ORGANISATION_TERM = 'Organisation'
export const LICENCE_TIER_TERM = 'Tier'
export const LICENCE_SEATS_TERM = 'Seats'
export const LICENCE_ISSUED_TERM = 'Issued'
export const LICENCE_EXPIRES_TERM = 'Expires'
export const LICENCE_FEATURES_TERM = 'Features'

/** LICENCE_SHAPE_BECAUSE is the refusal for an answer that arrived and is not
 * the shape this section reads: a decoder that guessed would be a decoder that
 * draws a member out of a reply that does not mean it (reconciliation section
 * 5). */
export const LICENCE_SHAPE_BECAUSE =
  'the console answered something that is not a licence state: no state word and no claim set, so none of it is drawn here'

/** LICENCE_ABSENT_BECAUSE is the refusal for a read that produced nothing at
 * all: the bridge itself answered `null`, which is what a member that is missing
 * or that failed to answer looks like from here. */
export const LICENCE_ABSENT_BECAUSE =
  'the console’s own store did not answer, so what this installation holds is not known here'

/** LICENCE_SAVED_WORD is what a save that worked says. It is drawn only until
 * the read behind it lands, because a save re-reads the section: the state and
 * the claim set drawn afterwards are the verifier's answer to the key that was
 * just kept, a stronger thing to show than a sentence claiming a write. */
export const LICENCE_SAVED_WORD = 'kept'

/** LICENCE_UNREADABLE_WORD is what an answer nobody can read says, and it is
 * the console's own: a reply that is not an envelope, or an envelope that is not
 * a reply, is a sentence rather than a broken screen. */
export const LICENCE_UNREADABLE_WORD =
  'the console answered something this screen cannot read, so nothing can be said about the key'

/**
 * LicenceClaim is one drawn line of the claim set: the term in this console's
 * words, the value as the answer carried it, and the class the value is drawn
 * through. The class travels on the row because the values are not one kind of
 * string -- an organisation is free text (`sentence`: a domain, a name, or an
 * order's id, and a value that reads as a host is drawn `<host>` by that class's
 * own rules), while a tier, a seat count, a day and the feature list are the
 * CLI's short rendered fields (`label`) -- and `putText` is the only door a
 * document's string has onto this screen.
 */
export interface LicenceClaim {
  readonly term: string
  readonly value: string
  readonly textClass: TextClass
}

/**
 * LicenceModel is the section's model: the word for the state the verifier
 * answered, the sentence beside it when there is one, the claim rows, and
 * whether the box is offered.
 *
 * `because` is the reason **as it will be drawn**: `buildLicence` puts it
 * through `sanitizeRefusal` (`drawnRefusal`) before it is stored, so the drawing
 * writes a sentence this console produced and never an answer's own string.
 *
 * `accepts` is false in exactly one case -- a read that could not be made at
 * all. An installation with no key takes a paste, and so does one whose key does
 * not verify: both are states an operator walks out of by pasting, which is what
 * "the key screen is never gated" means for this section.
 */
export interface LicenceModel {
  readonly drawn: 'answer' | 'unavailable'
  readonly word: string
  readonly because: string | null
  readonly claims: readonly LicenceClaim[]
  readonly accepts: boolean
}

/** LicenceInputs is what the section is built from: the answer **as it
 * arrived** and the console's own sentence for a read that was refused. The
 * answer is read here rather than decoded elsewhere, because the section reads
 * its own reply -- it checks the state word and the members it draws, and
 * answers a refusal rather than claims it cannot vouch for. */
export interface LicenceInputs {
  readonly raw: unknown
  readonly because: string | null
}

/**
 * LicenceDrawn is what the drawing answers: the panel and the three places the
 * entry needs a handle on -- the box, the control that sends it and the region an
 * answer to a save is drawn in. The three are null where the read did not answer
 * at all, because a console that cannot keep a key must not offer a box that
 * would take one.
 */
export interface LicenceDrawn {
  readonly root: ScreenElement
  readonly box: ScreenElement | null
  readonly submit: ScreenElement | null
  readonly outcome: ScreenElement | null
}

/** LicenceSaveRead is one `licence.save` reply after both envelopes have been
 * read: whether the key was kept, and the sentence the section draws either way.
 * There is no third state -- a reply nobody can read is a sentence saying so. */
export interface LicenceSaveRead {
  readonly saved: boolean
  readonly sentence: string
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
 * compose (design section 6.3, `src/shared/sanitize.ts`). The verifier's
 * refusals come from the same CLI the other sections read refusals from, so they
 * take the same door: the free-text rules would draw `roles/datastore.viewer` as
 * `[redacted]` and eat the remedy, while the credential rules still redact a key
 * a child echoed. A sentence too long for the console to read is
 * `REFUSAL_UNREADABLE` (`./degraded`, the console's one word for it) rather than
 * a cell the operator reads as the refusal.
 */
function drawnRefusal(because: string): string {
  const drawn = sanitizeRefusal(because, 'the reason this section has to draw')
  return drawn.ok ? drawn.text : REFUSAL_UNREADABLE
}

/** dayOf is a Unix second as the day it falls on, in UTC, or null when the
 * member is not a whole second. A claim set's two days are seconds since the
 * epoch and a screen that drew the number would draw a figure nobody reads; a
 * member that is not a number is no day at all rather than `Invalid Date` drawn
 * as a word. */
function dayOf(raw: unknown): string | null {
  if (typeof raw !== 'number' || !Number.isFinite(raw)) return null
  const at = new Date(raw * 1000)
  if (Number.isNaN(at.getTime())) return null
  return at.toISOString().slice(0, 10)
}

/** claimRows is the claim set as rows, in the order the terms are declared.
 * **Only members the answer actually carries are drawn**: a claim set with no
 * `features` draws no features row, and a row with no value would say the key
 * grants nothing. */
function claimRows(claims: Record<string, unknown>): readonly LicenceClaim[] {
  const rows: LicenceClaim[] = []
  const organisation = textAt(claims['organisation'])
  if (organisation !== null) {
    rows.push({ term: LICENCE_ORGANISATION_TERM, value: organisation, textClass: 'sentence' })
  }
  const tier = textAt(claims['tier'])
  if (tier !== null) rows.push({ term: LICENCE_TIER_TERM, value: tier, textClass: 'label' })
  const seats = claims['seats']
  if (typeof seats === 'number' && Number.isFinite(seats)) {
    rows.push({ term: LICENCE_SEATS_TERM, value: String(seats), textClass: 'label' })
  }
  const issued = dayOf(claims['issued'])
  if (issued !== null) rows.push({ term: LICENCE_ISSUED_TERM, value: issued, textClass: 'label' })
  const expires = dayOf(claims['expires'])
  if (expires !== null) rows.push({ term: LICENCE_EXPIRES_TERM, value: expires, textClass: 'label' })
  const features = claims['features']
  if (Array.isArray(features)) {
    const named = features.filter((one): one is string => typeof one === 'string')
    if (named.length > 0) {
      rows.push({ term: LICENCE_FEATURES_TERM, value: named.join(', '), textClass: 'label' })
    }
  }
  return rows
}

/**
 * buildLicence turns the read's own answer into this section's model.
 *
 * **Three rules are decided here and nowhere else.**
 *
 *   - **a state word this build does not know withholds everything**, rather
 *     than drawing the word itself: the verifier's states are a closed set
 *     (`LICENCE_STATE_WORDS`), and a fourth one a later CLI grew is a reply this
 *     build does not read -- drawn as itself it would be a word an operator has
 *     to interpret, and drawn with claims it would be this console vouching for
 *     a state it does not understand.
 *   - **a key that does not verify is a state and not an absent key**
 *     (`LICENCE_REFUSED_WORD`): the installation holds something and nothing
 *     verifies it, which is a different fact from holding nothing, and the
 *     answer's own sentence is drawn beside it. The box stays, because the way
 *     out of this state is a paste.
 *   - **a read that could not be made offers no box** (`accepts`): the console
 *     refused to answer -- the one case being a machine where it may not keep its
 *     records at all (`refusedLicenceStore`, `src/main/licence-store.ts`) -- and
 *     a control that could only ever refuse would be a control that lied.
 *
 * It touches no element, so every one of those is testable in plain Node.
 */
export function buildLicence(inputs: LicenceInputs): LicenceModel {
  if (inputs.because !== null) {
    return {
      drawn: 'unavailable',
      word: '',
      because: drawnRefusal(inputs.because),
      claims: [],
      accepts: false,
    }
  }
  const answer = objectAt(inputs.raw)
  if (answer === null) {
    return {
      drawn: 'unavailable',
      word: '',
      because: drawnRefusal(LICENCE_ABSENT_BECAUSE),
      claims: [],
      accepts: false,
    }
  }
  const state = textAt(answer['state'])
  if (state === 'none') {
    return { drawn: 'answer', word: LICENCE_NO_KEY_WORD, because: null, claims: [], accepts: true }
  }
  if (state === 'refused') {
    const reason = textAt(answer['reason'])
    return {
      drawn: 'answer',
      word: LICENCE_REFUSED_WORD,
      because: drawnRefusal(reason ?? LICENCE_SHAPE_BECAUSE),
      claims: [],
      accepts: true,
    }
  }
  const word = state === null ? undefined : LICENCE_STATE_WORDS[state]
  const claims = objectAt(answer['claims'])
  if (word === undefined || claims === null) {
    return {
      drawn: 'unavailable',
      word: '',
      because: drawnRefusal(LICENCE_SHAPE_BECAUSE),
      claims: [],
      accepts: false,
    }
  }
  return { drawn: 'answer', word, because: null, claims: claimRows(claims), accepts: true }
}

/**
 * licenceSaveOutcomeOf reads one `licence.save` reply: the envelope the main
 * process builds (`ConsoleReply`), and nothing else -- a save has no payload of
 * its own, so a reply that arrived is either an acceptance or the sentence that
 * refused it, and the store's refusal is the verifier's own words
 * (`drawnRefusal`).
 *
 * **The key cannot be in here**: the answer to a save is the same shape the read
 * answers, which carries no key text. A reply this file cannot read is a
 * sentence rather than a state -- there is no `null` and no throw, because a
 * reply nobody can read is something to tell the operator rather than a broken
 * section.
 */
export function licenceSaveOutcomeOf(reply: unknown): LicenceSaveRead {
  const envelope = objectAt(reply)
  if (envelope === null) return { saved: false, sentence: LICENCE_UNREADABLE_WORD }
  const keys = Object.keys(envelope)
  if (envelope['ok'] === false) {
    const reason = envelope['reason']
    if (keys.length !== 2 || keys.includes('data')) {
      return { saved: false, sentence: LICENCE_UNREADABLE_WORD }
    }
    return typeof reason === 'string' && reason !== ''
      ? { saved: false, sentence: drawnRefusal(reason) }
      : { saved: false, sentence: LICENCE_UNREADABLE_WORD }
  }
  if (envelope['ok'] !== true || keys.length !== 2 || !keys.includes('data')) {
    return { saved: false, sentence: LICENCE_UNREADABLE_WORD }
  }
  const answer = objectAt(envelope['data'])
  if (answer === null || textAt(answer['state']) === null) {
    return { saved: false, sentence: LICENCE_UNREADABLE_WORD }
  }
  return { saved: true, sentence: LICENCE_SAVED_WORD }
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

/** claimLine is one row of the claim set: the term in this file's own words and
 * the value through `putText` with the class the row declares. */
function claimLine(factory: ScreenFactory, claim: LicenceClaim, list: ScreenElement): void {
  const term = make(factory, 'dt', LICENCE_TERM_CLASS)
  literal(term, claim.term)
  list.append(term)
  const value = make(factory, 'dd', LICENCE_VALUE_CLASS)
  putText(value, claim.value, claim.textClass, `the licence key’s ${claim.term.toLowerCase()}`)
  list.append(value)
}

/** claimList is the claim set, or no element at all when the answer carried no
 * claim this screen can draw: an empty list would draw a heading over nothing. */
function claimList(factory: ScreenFactory, model: LicenceModel): ScreenElement | null {
  if (model.claims.length === 0) return null
  const list = make(factory, 'dl', LICENCE_CLAIMS_CLASS)
  for (const claim of model.claims) claimLine(factory, claim, list)
  return list
}

/**
 * licenceForm is the box, the control that sends it and the region an answer to
 * a save is drawn in. It is drawn as a whole and handed back, so the wiring does
 * not have to walk the tree it just drew to find the three parts (`renderLaunch`
 * keeps the same shape one screen over). It carries no confirmation, no preview
 * of the key and no link: what the operator pasted goes to one operation, which
 * verifies it before anything is kept.
 */
function licenceForm(factory: ScreenFactory): {
  readonly root: ScreenElement
  readonly box: ScreenElement
  readonly submit: ScreenElement
  readonly outcome: ScreenElement
} {
  const form = make(factory, 'div', LICENCE_FORM_CLASS)
  const label = factory.create('label')
  literal(label, LICENCE_LABEL)
  const box = factory.create('input')
  box.setAttribute('type', 'text')
  box.setAttribute('class', LICENCE_BOX_CLASS)
  box.setAttribute('placeholder', LICENCE_PLACEHOLDER)
  // A key is a credential, so the box does not offer to have the browser
  // remember it, and the spelling and capitalisation helps have nothing to offer
  // a base64url string either.
  box.setAttribute('autocomplete', 'off')
  box.setAttribute('autocapitalize', 'off')
  box.setAttribute('spellcheck', 'false')
  label.append(box)
  form.append(label)
  const hint = make(factory, 'p', LICENCE_HINT_CLASS)
  literal(hint, LICENCE_HINT)
  form.append(hint)
  const submit = factory.create('button')
  submit.setAttribute('type', 'button')
  submit.setAttribute('class', LICENCE_SUBMIT_CLASS)
  literal(submit, LICENCE_SUBMIT_WORD)
  form.append(submit)
  const outcome = make(factory, 'p', LICENCE_OUTCOME_CLASS)
  outcome.setAttribute('aria-live', 'polite')
  form.append(outcome)
  return { root: form, box, submit, outcome }
}

/**
 * renderLicence draws the whole section -- the panel's own name, whether the
 * read landed, what it names as its source, the state the verifier answered, the
 * sentence beside it when there is one, the claim set when there is one, and the
 * box -- and answers the three controls the wiring needs.
 *
 * **The box is drawn only when the model accepts a paste** (`buildLicence`): a
 * console whose store refused to answer offers no control that could only
 * refuse.
 */
export function renderLicence(factory: ScreenFactory, model: LicenceModel): LicenceDrawn {
  const root = make(factory, 'section', LICENCE_CLASS)
  root.setAttribute('data-operation', LICENCE_OPERATION)
  const title = make(factory, 'h2', LICENCE_TITLE_CLASS)
  literal(title, LICENCE_TITLE)
  root.append(title)
  const state = make(factory, 'span', LICENCE_STATE_CLASS)
  literal(state, model.drawn === 'answer' ? 'read' : 'not read')
  root.append(state)
  const source = make(factory, 'span', LICENCE_SOURCE_CLASS)
  literal(source, LICENCE_SOURCE)
  root.append(source)
  if (model.drawn === 'answer') {
    const word = make(factory, 'p', LICENCE_WORD_CLASS)
    literal(word, model.word)
    root.append(word)
  }
  if (model.because !== null) {
    // The reason is drawn, not paraphrased: `buildLicence` or
    // `licenceSaveOutcomeOf` already put it through `sanitizeRefusal`
    // (`drawnRefusal`), so this is a sentence this console produced.
    const because = make(factory, 'p', LICENCE_BECAUSE_CLASS)
    literal(because, model.because)
    root.append(because)
  }
  const claims = claimList(factory, model)
  if (claims !== null) root.append(claims)
  if (!model.accepts) return { root, box: null, submit: null, outcome: null }
  const form = licenceForm(factory)
  root.append(form.root)
  return { root, box: form.box, submit: form.submit, outcome: form.outcome }
}

/**
 * fillLicence draws the section into the host the tab gave it, and it is one
 * read's filler: `licence.read` lands apart from the tab's other reads, so this
 * panel is drawn the moment its answer arrives and nothing waits for another.
 *
 * It appends to the host and not to a slot inside itself: the mark of a read
 * that is still out belongs to the entry, in a slot of its own beside this
 * section (`renderBusy`, `../section.ts`), which is what keeps one read's mark
 * from taking another read's content down.
 */
export function fillLicence(
  factory: ScreenFactory,
  model: LicenceModel,
  host: ScreenElement,
): LicenceDrawn {
  const drawn = renderLicence(factory, model)
  host.append(drawn.root)
  return drawn
}

/** renderLicenceOutcome is what a save answered, as the one element the entry
 * puts in the form's outcome region: the sentence `licenceSaveOutcomeOf` read,
 * which is this file's own word or `drawnRefusal`'s -- so nothing else is
 * applied to it here. A successful save is answered by the read that follows it
 * (the section is re-read), so this is drawn only until that lands. */
export function renderLicenceOutcome(
  factory: ScreenFactory,
  read: LicenceSaveRead,
): ScreenElement {
  const line = make(factory, 'span', LICENCE_OUTCOME_CLASS)
  literal(line, read.sentence)
  return line
}
