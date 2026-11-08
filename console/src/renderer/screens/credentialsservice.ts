// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The credentials service's state, as a section: the
// `credentials-service-deployed` line of `doctor --json`, drawn as the CLI wrote
// it (the managed-screens plan's CM9; reconciliation F22, decision D1486).
//
// **One section, one read.** The read is `environment.report` -- `fugaro doctor
// --json`, the row CM4 put on the console's surface for the Project tab's
// panels -- and this module adds no operation, no argv and no spawn of its own.
// It is the read `policynotes.ts` (CM7) already names for its checks block, and
// the tab that mounts this section gives it its own host, its own busy state and
// its own place in the parallel first paint (`section.ts`, the owner's rule of
// 2026-10-31). `projects.ts` wires it in (CM10); this module draws it.
//
// **The CLI's words, re-derived nowhere** (D1486). The line the check produces
// is one `doctorCheck`: an `id`, an `ok`, a `severity`, a `problem` and a `fix`.
// The severity is the CLI's classification of the state -- whether a run can
// work around it -- and this section draws it verbatim rather than deciding
// anything about whether the service is deployed, converged or serving. That is
// the whole of the decision: a console that read the installation's own config,
// or the bundle, to say which state an installation is in would be a second
// answer to a question the CLI has already answered, and the first time the two
// disagreed the screen would be the one lying.
//
// **The problem and the fix are the document's own sentences, through the
// refusal door.** They are what a person acts on, and one of the CLI's fixes
// names `roles/run.invoker` -- which the free-text `sentence` class draws as
// `[redacted]`, taking the remedy away. So they go through `sanitizeRefusal`,
// the door the design keeps for a CLI sentence (`docs/design/desktop-console.md`
// section 6.3), exactly as `degraded.ts`, `managed.ts`, `projects.ts` and
// `accountkeys.ts` draw theirs: a role name, a project and a `fugaro creds set
// NAME` are left as the CLI printed them, and a credential a child echoed is
// still redacted. The severity is a word rather than a sentence, so it takes the
// `label` class through `putText` the way the policy section's rows do.
//
// **An installation whose CLI predates DON7 carries no such check.** The line
// arrived with DON7 (`internal/cli/doctor_brokering.go`); a console newer than
// the binary beside it reads a document that has no such check in it, and that
// is a state of its own (`no-check`) rather than an empty list of rows: "this
// CLI does not report the service's state" and "the check says the service is
// off" are different facts, and a screen that drew the first as the second would
// tell an operator to fix something nobody reported.
//
// **Every line the document carries under the one ID is drawn, in its own
// order.** DON7's check is one line for the service; its per-repository half
// (one line per repository, under the same ID, saying why that repository's runs
// do not go through the service) is DON8's and arrives later
// (`docs/credentials-service/default-on.md` section 3). This section reads the
// ID and not a position, so it draws the first line and the tenth alike, and
// neither task has to touch this module when the other lands.

import { unavailable } from '../../shared/availability'
import type { Reading } from '../../shared/availability'
import { sanitizeRefusal, sanitizeText } from '../../shared/sanitize'
import { putReading, putText } from '../app'
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

/** CREDENTIALS_SERVICE_OPERATION is the read this section makes, spelled as the
 * console's operation surface spells it (`src/main/ipc.ts`, CM4). It is drawn on
 * the panel's `data-operation`, so a reader of the markup -- or a test -- can see
 * which operation the panel is about. */
export const CREDENTIALS_SERVICE_OPERATION = 'environment.report'

/** CREDENTIALS_SERVICE_CHECK_ID is the one check this section draws. It is the
 * ID DON7 gave the line (`internal/cli/doctor_brokering.go`), and it is read
 * from the document rather than assumed: a CLI that has not grown the check yet
 * answers with a document that carries none, which is `no-check` below. */
export const CREDENTIALS_SERVICE_CHECK_ID = 'credentials-service-deployed'

/** CREDENTIALS_SERVICE_PANEL_KEY is the panel's own key, the value `data-panel`
 * carries. */
export const CREDENTIALS_SERVICE_PANEL_KEY = 'credentials-service'

/** CREDENTIALS_SERVICE_SOURCE is the command the document comes from, in the
 * console's own words. It is drawn on every state of the panel -- read, refused,
 * and the refusal that names it -- because the design's review criterion for
 * these screens is that **every field traces to a command line in section 5.1's
 * allow-list**, and a refusal that did not name its command would leave an
 * operator with a missing section and no way to ask for it by hand. */
export const CREDENTIALS_SERVICE_SOURCE = 'fugaro doctor --json'

/** CREDENTIALS_SERVICE_TITLE is the panel's heading, the console's own words. */
export const CREDENTIALS_SERVICE_TITLE = 'Credentials service'

/** CREDENTIALS_SERVICE_BUSY is the label the section's busy mark carries. It
 * names the read and not the section: the mark says what is being read, which is
 * the fact an operator waiting on it needs. */
export const CREDENTIALS_SERVICE_BUSY = 'Reading the credentials service’s state…'

/**
 * CREDENTIALS_SERVICE_ABSENT is the refusal for a read that produced nothing and
 * whose refusal carried no CLI sentence of its own: the console's own words,
 * naming the command, so the section's refusal names its own read.
 */
export const CREDENTIALS_SERVICE_ABSENT = `no document arrived for \`${CREDENTIALS_SERVICE_SOURCE}\``

/** CREDENTIALS_SERVICE_SHAPE is the refusal for a document that arrived and is
 * not the one the section reads: a decoder that guessed would be a decoder that
 * draws a member out of a document that does not mean it (reconciliation §5). */
export const CREDENTIALS_SERVICE_SHAPE = `the document \`${CREDENTIALS_SERVICE_SOURCE}\` answered with carries no checks array`

/**
 * CREDENTIALS_SERVICE_NO_CHECK is what the section says when the document
 * answered and carries no `credentials-service-deployed` line at all: the state
 * a CLI older than DON7 leaves, and a fact about the **document** rather than
 * about the service. It is deliberately not the word "off": nothing was read
 * that says the service is not deployed, and a screen that drew the absence as
 * that state would be reporting a measurement it never made.
 */
export const CREDENTIALS_SERVICE_NO_CHECK = `the document \`${CREDENTIALS_SERVICE_SOURCE}\` answered with carries no \`${CREDENTIALS_SERVICE_CHECK_ID}\` check: this CLI does not report the credentials service’s state, so the section says that rather than reporting a state nobody measured`

/** The terms one line's facts are drawn under, one per row. They are the
 * console's words for what each value is, so a figure is never drawn without a
 * word saying what it is a figure of. */
export const CREDENTIALS_SERVICE_SEVERITY_TERM = 'severity'
export const CREDENTIALS_SERVICE_PROBLEM_TERM = 'problem'
export const CREDENTIALS_SERVICE_FIX_TERM = 'fix'

/** The three words the section writes for the check's own `ok` member: the
 * CLI's boolean, in the console's own words. They are read from that member and
 * from nothing else -- the severity is drawn beside them, so the console makes
 * no judgement about whether a line fails `doctor` (the CLI's own `severity` and
 * `class` are the answer to that, and the severity is drawn verbatim). */
export const CREDENTIALS_SERVICE_OK_WORD = 'ok'
export const CREDENTIALS_SERVICE_REPORTED_WORD = 'reported'
export const CREDENTIALS_SERVICE_UNSTATED_WORD = 'not stated'

/** CREDENTIALS_SERVICE_MEMBER_ABSENT_BECAUSE is why a member the document does
 * not carry draws no value: it is the sentence the `n/a` mark goes with, and it
 * is a fact about the *document*, never about the service. A check the CLI
 * writes as `{id, ok}` alone -- its healthy line -- carries no severity, no
 * problem and no fix, and none of the three is a pass. */
export const CREDENTIALS_SERVICE_MEMBER_ABSENT_BECAUSE =
  'the document this section read carries this check without the member: a member the document does not carry is not one the console can draw'

/** The class hooks this section draws with. CM11 writes the rules that select
 * them and the document that licenses them; a class the stylesheet lacks renders
 * as plain markup, which is the state the golden frames are recorded in, so this
 * module lands correct and unstyled rather than waiting. */
export const CREDENTIALS_SERVICE_CLASS = 'projects-credentials-service'
export const CREDENTIALS_SERVICE_TITLE_CLASS = 'projects-credentials-service-title'
export const CREDENTIALS_SERVICE_STATE_CLASS = 'projects-credentials-service-state'
export const CREDENTIALS_SERVICE_SOURCE_CLASS = 'projects-credentials-service-source'
export const CREDENTIALS_SERVICE_BECAUSE_CLASS = 'projects-credentials-service-because'
export const CREDENTIALS_SERVICE_NO_CHECK_CLASS = 'projects-credentials-service-no-check'
export const CREDENTIALS_SERVICE_SHAPE_CLASS = 'projects-credentials-service-shape'
export const CREDENTIALS_SERVICE_LIST_CLASS = 'projects-credentials-service-list'
export const CREDENTIALS_SERVICE_ROW_CLASS = 'projects-credentials-service-row'
export const CREDENTIALS_SERVICE_CHECK_STATE_CLASS = 'projects-credentials-service-check-state'
export const CREDENTIALS_SERVICE_CHECK_ID_CLASS = 'projects-credentials-service-check-id'
export const CREDENTIALS_SERVICE_FACTS_CLASS = 'projects-credentials-service-facts'
export const CREDENTIALS_SERVICE_TERM_CLASS = 'projects-credentials-service-term'
export const CREDENTIALS_SERVICE_VALUE_CLASS = 'projects-credentials-service-value'

// ---------------------------------------------------------------------------
// The model.
// ---------------------------------------------------------------------------

/**
 * CredentialsServiceMember is one of a check's members: the CLI's own string
 * through its door (`text`), or a reading with no value carrying the reason
 * there is none (`reading`). The two are exclusive by construction -- a member
 * the document carries and the door draws is the first, and a member the
 * document does not carry, or one the door refuses, is the second -- so no cell
 * can be an empty string that reads like a value the document sent.
 */
export interface CredentialsServiceMember {
  readonly text: string | null
  readonly reading: Reading | null
}

/** CredentialsServiceCheck is one line of the check, as `doctor --json` writes
 * it. `ok` is `null` where the document carries the check without the member,
 * which is not the same fact as a check the document says passed. */
export interface CredentialsServiceCheck {
  readonly id: string
  readonly ok: boolean | null
  readonly severity: CredentialsServiceMember
  readonly problem: CredentialsServiceMember
  readonly fix: CredentialsServiceMember
}

/**
 * CredentialsServiceState is where the read stands: it answered and carried the
 * check (`read`), it answered and carried no such check at all (`no-check` --
 * the CLI older than DON7), it did not answer (`absent`), or it answered with a
 * document this section does not read (`shape`). The four are states of their
 * own because they are four different facts, and `no-check` in particular may
 * not be folded into `read`: an empty list of rows would read as a check that
 * reported nothing rather than as a document that never carried one.
 */
export type CredentialsServiceState = 'read' | 'no-check' | 'absent' | 'shape'

/** CredentialsServiceModel is the section, built and testable with no element. */
export interface CredentialsServiceModel {
  readonly state: CredentialsServiceState
  readonly because: string | null
  readonly checks: readonly CredentialsServiceCheck[]
}

/** CredentialsServiceInputs is what the entry hands the model: the read's
 * document, and the CLI's own sentence when the read was refused. */
export interface CredentialsServiceInputs {
  readonly raw: unknown
  readonly because?: string | null
}

/** objectAt is a value that is a JSON object, or null. */
function objectAt(raw: unknown): Record<string, unknown> | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

/** arrayAt is a value that is a JSON array, or null. */
function arrayAt(raw: unknown): readonly unknown[] | null {
  return Array.isArray(raw) ? (raw as readonly unknown[]) : null
}

/** textAt is a value that is a JSON string, or null. */
function textAt(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
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
 * command, so the section's refusal names its own read. A `because` that is not
 * a string at all is no sentence either. */
function absent(because: unknown): string {
  if (typeof because === 'string' && because.trim() !== '') return because
  return CREDENTIALS_SERVICE_ABSENT
}

/** MemberDoor is what draws one member's string: the door its kind takes, and
 * the member it produced. The drawn text is the door's answer and never the
 * document's string, which is what makes a redaction the door made reach the
 * cell -- a door that only reported refusal would leave the raw string drawn. */
type MemberDoor = (value: string, field: string) => CredentialsServiceMember

/** member is one of a check's members, read through the door its kind takes: a
 * word through `sanitizeText`'s class, a sentence through `sanitizeRefusal`. A
 * member the document does not carry -- or spells empty, which the contract uses
 * for "there is none" -- is a reading with no value, so the cell is the mark
 * with the reason rather than a blank that reads like a value. */
function member(raw: unknown, field: string, door: MemberDoor): CredentialsServiceMember {
  const text = textAt(raw)
  if (text === null || text === '') {
    return { text: null, reading: unavailable(CREDENTIALS_SERVICE_MEMBER_ABSENT_BECAUSE) }
  }
  return door(text, field)
}

/** wordMember is a member the console draws as a word: the class whose grammar
 * admits a dotted identifier whole, and the console's mark for one it refuses. */
function wordMember(raw: unknown, field: string): CredentialsServiceMember {
  return member(raw, field, (value, name) => {
    const drawn = sanitizeText(value, 'label', name)
    if (drawn.ok) return { text: drawn.text, reading: null }
    return { text: null, reading: unavailable(drawn.reason) }
  })
}

/** sentenceMember is a member the console draws as a sentence: the refusal door,
 * which keeps the role, the project and the command a remedy is made of and
 * still redacts a credential a child echoed. */
function sentenceMember(raw: unknown, field: string): CredentialsServiceMember {
  return member(raw, field, (value, name) => {
    const drawn = sanitizeRefusal(value, name)
    if (drawn.ok) return { text: drawn.text, reading: null }
    return { text: null, reading: unavailable(drawn.reason) }
  })
}

/** checkOf reads one line of the document. */
function checkOf(raw: Record<string, unknown>): CredentialsServiceCheck {
  return {
    id: textAt(raw['id']) ?? CREDENTIALS_SERVICE_CHECK_ID,
    ok: typeof raw['ok'] === 'boolean' ? raw['ok'] : null,
    severity: wordMember(raw['severity'], 'checks[].severity'),
    problem: sentenceMember(raw['problem'], 'checks[].problem'),
    fix: sentenceMember(raw['fix'], 'checks[].fix'),
  }
}

/**
 * buildCredentialsService turns the document into the section. It touches no
 * element, so every rule below is testable in plain Node.
 *
 * The checks are taken **by ID**, in the order the document carries them, and a
 * line whose `id` is not this check's is dropped: `doctor --json` returns around
 * twenty checks, and a section whose subject is the credentials service draws
 * the one family of lines about it and none of the rest.
 */
export function buildCredentialsService(inputs: CredentialsServiceInputs): CredentialsServiceModel {
  const document = objectAt(inputs.raw)
  if (document === null) {
    return { state: 'absent', because: drawnRefusal(absent(inputs.because)), checks: [] }
  }
  const list = arrayAt(document['checks'])
  if (list === null) return { state: 'shape', because: null, checks: [] }
  const checks: CredentialsServiceCheck[] = []
  for (const entry of list) {
    const object = objectAt(entry)
    if (object === null) continue
    if (textAt(object['id']) !== CREDENTIALS_SERVICE_CHECK_ID) continue
    checks.push(checkOf(object))
  }
  if (checks.length === 0) return { state: 'no-check', because: null, checks: [] }
  return { state: 'read', because: null, checks }
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
 * which comes through `putText` (`../app.ts`) or a door this file names, or not
 * at all. */
function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

/** cell is a cell of the console's own making, appended to a parent. */
function cell(parent: ScreenElement, factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = make(factory, tag, ...classes)
  parent.append(element)
  return element
}

/** term is one row's `dt`: the console's own word for what the value beside it
 * is. */
function term(parent: ScreenElement, factory: ScreenFactory, text: string): ScreenElement {
  return literal(cell(parent, factory, 'dt', CREDENTIALS_SERVICE_TERM_CLASS), text)
}

/** memberCell is `dd` with one member drawn: the door's own string, or the mark
 * with the reason there is none. */
function memberCell(parent: ScreenElement, factory: ScreenFactory, value: CredentialsServiceMember): ScreenElement {
  const element = cell(parent, factory, 'dd', CREDENTIALS_SERVICE_VALUE_CLASS)
  if (value.text !== null) return literal(element, value.text)
  putReading(element, value.reading ?? unavailable(CREDENTIALS_SERVICE_MEMBER_ABSENT_BECAUSE), 'count')
  return element
}

/**
 * checkRow draws one line of the check: the CLI's own `ok` as the console's
 * word for it, the check's id, and the three members the plan names -- the
 * severity, the problem and the fix -- each as the CLI wrote it.
 *
 * The `ok` is drawn twice on purpose and in two shapes: as the `data-ok`
 * attribute, which is the document's own boolean and what CM11's stylesheet
 * colours, and as the word beside it, so a reader who has no stylesheet still
 * knows whether the line is one the CLI reports as a problem. Neither is a
 * judgement about the *service*: the severity is drawn verbatim beside them, and
 * `doctor`'s own answer to "does this fail" is the severity and the class it
 * carries in the document.
 */
function checkRow(factory: ScreenFactory, check: CredentialsServiceCheck): ScreenElement {
  const element = make(factory, 'li', CREDENTIALS_SERVICE_ROW_CLASS)
  if (check.ok === null) element.setAttribute('data-ok', 'unstated')
  else if (check.ok) element.setAttribute('data-ok', 'true')
  else element.setAttribute('data-ok', 'false')
  const word = cell(element, factory, 'span', CREDENTIALS_SERVICE_CHECK_STATE_CLASS)
  if (check.ok === null) literal(word, CREDENTIALS_SERVICE_UNSTATED_WORD)
  else if (check.ok) literal(word, CREDENTIALS_SERVICE_OK_WORD)
  else literal(word, CREDENTIALS_SERVICE_REPORTED_WORD)
  putText(cell(element, factory, 'span', CREDENTIALS_SERVICE_CHECK_ID_CLASS), check.id, 'label', 'checks[].id')
  const facts = cell(element, factory, 'dl', CREDENTIALS_SERVICE_FACTS_CLASS)
  term(facts, factory, CREDENTIALS_SERVICE_SEVERITY_TERM)
  memberCell(facts, factory, check.severity)
  term(facts, factory, CREDENTIALS_SERVICE_PROBLEM_TERM)
  memberCell(facts, factory, check.problem)
  term(facts, factory, CREDENTIALS_SERVICE_FIX_TERM)
  memberCell(facts, factory, check.fix)
  return element
}

/** credentialsServicePanel is the section: the panel's own name, its state as a
 * word and not as a tone alone (design section 3.5), the command line it names
 * as its source, the read's refusal when it did not answer, and the lines when
 * it did.
 *
 * A document that answered and carried no such check is drawn as the sentence
 * saying so and **no** rows: the state is a fact about the document, and an
 * empty list would be a list that failed to load. */
function credentialsServicePanel(factory: ScreenFactory, model: CredentialsServiceModel): ScreenElement {
  const element = make(factory, 'section', CREDENTIALS_SERVICE_CLASS)
  element.setAttribute('data-panel', CREDENTIALS_SERVICE_PANEL_KEY)
  element.setAttribute('data-state', model.state)
  element.setAttribute('data-operation', CREDENTIALS_SERVICE_OPERATION)
  literal(cell(element, factory, 'h2', CREDENTIALS_SERVICE_TITLE_CLASS), CREDENTIALS_SERVICE_TITLE)
  const state = cell(element, factory, 'span', CREDENTIALS_SERVICE_STATE_CLASS)
  // A document that answered and carried no such check **was read**: what it
  // says is that this CLI reports no service state, which is not the same fact
  // as a read that did not answer.
  if (model.state === 'read' || model.state === 'no-check') literal(state, 'read')
  else literal(state, 'not read')
  literal(cell(element, factory, 'span', CREDENTIALS_SERVICE_SOURCE_CLASS), CREDENTIALS_SERVICE_SOURCE)
  if (model.because !== null) {
    // `buildCredentialsService` put it through `sanitizeRefusal` (`drawnRefusal`),
    // so this is a sentence this console produced -- the CLI's refusal verbatim,
    // which is what the design requires and what the tab's other panels show for
    // the same read.
    literal(cell(element, factory, 'p', CREDENTIALS_SERVICE_BECAUSE_CLASS), model.because)
  }
  if (model.state === 'no-check') {
    literal(cell(element, factory, 'p', CREDENTIALS_SERVICE_NO_CHECK_CLASS), CREDENTIALS_SERVICE_NO_CHECK)
    return element
  }
  if (model.state === 'shape') {
    literal(cell(element, factory, 'p', CREDENTIALS_SERVICE_SHAPE_CLASS), CREDENTIALS_SERVICE_SHAPE)
    return element
  }
  if (model.state !== 'read') return element
  const list = cell(element, factory, 'ul', CREDENTIALS_SERVICE_LIST_CLASS)
  for (const check of model.checks) list.append(checkRow(factory, check))
  return element
}

/**
 * fillCredentialsService draws the section into the host the tab gave it, and it
 * is one read's filler: `environment.report` lands apart from the tab's other
 * reads, so this panel is drawn the moment its document arrives and nothing
 * waits for another (D1488).
 *
 * It appends to the host and not to a slot inside itself: the mark of a read
 * that is still out is the entry's, in a slot of its own beside this section
 * (`renderBusy`, `../section.ts`), which is what keeps one read's mark from
 * taking another read's claim down.
 */
export function fillCredentialsService(factory: ScreenFactory, model: CredentialsServiceModel, host: ScreenElement): void {
  host.append(credentialsServicePanel(factory, model))
}

/** renderCredentialsService builds the model from the read's own document and
 * draws it: the one call for an entry that has the document in hand and no model
 * to keep. */
export function renderCredentialsService(
  factory: ScreenFactory,
  inputs: CredentialsServiceInputs,
  host: ScreenElement,
): void {
  fillCredentialsService(factory, buildCredentialsService(inputs), host)
}
