// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The first-run setup wizard's screen (the console-improvements plan, task SW3b;
// spec section 6 as `reconciliation.md`'s D2904 amended it; the decisions this
// file is written against are D2904, D2905, O6, F15, F27, and the contract is
// `src/shared/new-ops.ts`, task C-OPS).
//
// **What it is.** The screen the onboarding view draws in place of `fugaro
// setup`'s command line when the console has no installation: the questions
// `fugaro setup` asks, answered in a form, sent to main as one answers object,
// and then the run's own event feed drawn as the lines arrive. It is the same
// screen behind the rail's "Add project" (SW3c), which is why it is a module of
// its own rather than a branch inside `entry.ts`.
//
// **What it asks, and what it never asks.** The questions are the contract's
// (`SETUP_ANSWER_KEYS`: `gcp_project`, `region`, `name`, `rigor`, `budget`,
// `bake_repos`) in the order `fugaro setup` asks them, with the defaults the CLI
// has -- the region `us-east5` (the US place's default, `place.DefaultRegion`),
// the monthly budget alert amount `100` (D2212, `managed.DefaultBudgetAmount`)
// and baking on -- and with **none** for the rigor mode, which is the one
// question with no default at all (D900) and the one this screen marks required:
// the CLI refuses a setup that answers no mode, so a form that offered a default
// would be offering an answer the CLI does not accept. Nothing else is asked
// here. There is no field for a secret value and no field for a path or a flag
// (`validateSetupAnswers` refuses a key outside that set, and D2905 keeps every
// secret out of this console), there is no `<form>` -- so Enter submits nothing,
// which is the second half of the red step -- and this module spawns no terminal
// and no child: it hands main an answers object and draws what main answers.
//
// **The feed.** A plan answers the events `setup --events` wrote (ST2), in
// arrival order, and each line is drawn as it is read: the stages the run
// settled, the steps it reached, every `left_for_you` line with its own command,
// and the `error` line of a run that stopped. A feed with no last line is an
// interrupted read and draws the console's own sentence with Retry, exactly as
// every other streamed read in this console does (R1, `stream.ts`) -- it is never
// drawn as a plan that finished early. A gap -- no handler, a refusal, an answer
// this build cannot read -- is the console's sentence for it and nothing else.
//
// **A `left_for_you` command is copied through MG0's builder or not at all.** The
// CLI hands the line to run in the owner's terminal, and the wizard draws it as
// text either way. A line whose first word is `fugaro` or `gcloud` -- the two
// commands a first setup's hand-off lines name -- is assembled as a Copy-command
// by `manage/command.ts`, so the console's own two words are the only words it
// writes and every other word of the line is a value from the CLI's document; a
// value the builder refuses (a `--billing-account=<id>` whose letters the
// alphabets do not admit, a line with a `..` segment) draws **no Copy at all**
// and the reason beside it, because a command the console will not copy is a
// command it will not repair either. A line that opens with anything else is
// displayed only: it is a sentence, not a command of the two this console may
// hand to a clipboard.
//
// **The apply is a second press, and it is honest about what it does.** The plan
// is the dry run main holds while its window asks the owner; the run behind the
// window's yes is the real setup, and `setup.apply` is the renderer's door to
// that run's feed (SW3a). The wizard offers "Follow the run" once a plan has been
// sent: the press asks for that feed, and a plan nobody has confirmed yet is
// refused with main's own sentence, which is drawn where the events would go.
// There is no polling here and no guess about when the window was answered.

import { putText } from '../app'
import { buildCopyCommand, commandValue, commandWord, copyCommandText } from '../manage/command'
import type { CommandPart, CopyCommandAnswer } from '../manage/command'
import { BUSY_BUSY_ATTRIBUTE, renderRetry } from '../section'
import { NODE_PAINT, STREAM_INTERRUPTED_WORD } from '../stream'
import type { StreamPaint } from '../stream'
import { RIGOR_MODE_WORDS } from '../../shared/rigor'
import type { TextClass } from '../../shared/sanitize'
import {
  SETUP_ANSWER_KEYS,
  SETUP_BUDGET_MAX,
  SETUP_BUDGET_MIN,
  SETUP_RIGOR_MODES,
} from '../../shared/new-ops'
import type {
  NewOpsReply,
  NoArgumentRequest,
  SetupAnswers,
  SetupEvent,
  SetupFeed,
  SetupLeftForYouEvent,
  SetupPlanRequest,
  SetupRigor,
} from '../../shared/new-ops'

// ---------------------------------------------------------------------------
// The DOM this file draws into.
// ---------------------------------------------------------------------------

/**
 * WizardElement is the whole of the DOM this screen touches: `SkewElement`'s
 * members, plus the two a section that owns its own interaction needs --
 * `addEventListener` (the submit, the two Copy buttons, the follow control and
 * the Retry) and `value` (what the operator typed, which is not `textContent`).
 *
 * It is declared here rather than imported for `section.ts`'s reason: this
 * module depends on no screen and no screen depends on it. `entry.ts`'s
 * `DomElement` is the shape it was written for, and the entry's own factory is
 * what a caller hands in.
 *
 * **There is no `replaceChildren`.** The entry's wrapper has none, and a screen
 * that cleared its own tree would own a rewrite of what a section may do to a
 * panel. So an attempt is drawn **beside** the attempt before it rather than over
 * it (`send`), and the submit's own rule -- an unanswered required question sends
 * nothing -- is the refusal the operator reads, drawn in the same place wherever
 * it happens. The two controls that **start** a read are the ones `send` holds
 * (`disabled`) while its answer is outstanding, which is what a press whose child
 * main holds for up to an hour needs; the Retry a finished attempt drew is not
 * one of them, because a second attempt beside the first is what this screen
 * draws for a read that stopped.
 */
export interface WizardElement {
  textContent: string | null
  setAttribute(name: string, value: string): void
  getAttribute(name: string): string | null
  removeAttribute(name: string): void
  readonly tag: string
  append(child: WizardElement): void
  addEventListener(type: string, listener: () => void): void
  readonly value: string
}

/** WizardFactory is where elements come from. */
export interface WizardFactory {
  create(tag: string): WizardElement
}

/** literal is this file's one writer of a text slot: every argument it is handed
 * is this file's own constant or a sentence built out of those, so no document
 * string reaches it. Everything a CLI wrote goes through `putText` and its
 * class. */
function literal(element: WizardElement, text: string): WizardElement {
  element.textContent = text
  return element
}

/** make is a fresh element of one tag carrying one class. */
function make(factory: WizardFactory, tag: string, className: string): WizardElement {
  const element = factory.create(tag)
  element.setAttribute('class', className)
  return element
}

/** text is a fresh element carrying one field the CLI wrote, drawn through its
 * own class (`NEW_OPERATION_TEXT_CLASSES`). It is an element of its own and never
 * written into a parent that already has children: `putText` writes
 * `textContent`, which would clear them. */
function text(
  factory: WizardFactory,
  tag: string,
  className: string,
  raw: unknown,
  textClass: TextClass,
  field: string,
): WizardElement {
  const element = make(factory, tag, className)
  putText(element, raw, textClass, field)
  return element
}

// ---------------------------------------------------------------------------
// The screen's own vocabulary.
// ---------------------------------------------------------------------------

/** WIZARD_CLASS is the screen's root, and the four classes under it are its
 * parts: the lead sentence, the questions and the answers area. There is no
 * heading among them: **the heading is the caller's**. The onboarding view
 * already draws one above this screen (`ONBOARDING_COPY`'s title, and the
 * picker draws the same words), and a wizard that drew its own would be two
 * headings saying one thing. */
export const WIZARD_CLASS = 'console-wizard'
export const WIZARD_LEAD_CLASS = 'console-wizard-lead'
export const WIZARD_QUESTIONS_CLASS = 'console-wizard-questions'

/** WIZARD_QUESTION_CLASS and WIZARD_QUESTION_ATTRIBUTE are one question's row --
 * its label, its note and its control -- with the answer key it fills as the
 * attribute, so a reader of the tree (and a case) can name the row it means
 * without counting children. */
export const WIZARD_QUESTION_CLASS = 'console-wizard-question'
export const WIZARD_QUESTION_ATTRIBUTE = 'data-wizard-question'
export const WIZARD_LABEL_CLASS = 'console-wizard-label'

/** WIZARD_CONTROL_ID_PREFIX is the head of the id every question's control is
 * given, and the label beside it is drawn `for` that id: a control with no
 * accessible name is a control a screen reader reads as "edit text", and the id's
 * tail is the row's own answer key, so the two can never drift apart. */
export const WIZARD_CONTROL_ID_PREFIX = 'console-wizard-field-'

export const WIZARD_NOTE_CLASS = 'console-wizard-note'
export const WIZARD_INPUT_CLASS = 'console-wizard-input'
export const WIZARD_OPTION_CLASS = 'console-wizard-option'

/** WIZARD_SUBMIT_CLASS is the one control that sends the answers, and it is a
 * button in a plain container: **there is no `form` here**, so Enter does not
 * submit this screen and the rigor question has no way to be answered by a
 * keystroke that meant something else. */
export const WIZARD_SUBMIT_CLASS = 'console-wizard-submit'

/** WIZARD_ANSWER_CLASS is one sentence about an outcome -- the console's own
 * refusal of an unanswered question above the attempts, and, inside an attempt,
 * that press's own outcome -- and it is never a text slot a document's string is
 * written into raw: a sentence the CLI wrote goes through `putText`'s class
 * (`text`), and the rest are this file's own words. */
export const WIZARD_ANSWER_CLASS = 'console-wizard-answer'

/** WIZARD_ATTEMPTS_CLASS is the container every attempt is drawn into, and
 * WIZARD_ATTEMPT_CLASS one press's own block: its sentence, its events and, where
 * the read stopped, its Retry. */
export const WIZARD_ATTEMPTS_CLASS = 'console-wizard-attempts'
export const WIZARD_ATTEMPT_CLASS = 'console-wizard-attempt'

/** WIZARD_EVENT_CLASS and WIZARD_EVENT_ATTRIBUTE are one event line, with the
 * wire's own `type` as the attribute (`SETUP_EVENT_TYPES`), so the order of the
 * drawings is the order of the feed and a case reads the kind each one was drawn
 * as. */
export const WIZARD_EVENT_CLASS = 'console-wizard-event'
export const WIZARD_EVENT_ATTRIBUTE = 'data-wizard-event'

/** WIZARD_LINES_CLASS is the container one attempt's feed lines are appended to,
 * so the attempt's own sentences (the hand-off, the done line, an interruption)
 * read as being about the lines above them. */
export const WIZARD_LINES_CLASS = 'console-wizard-lines'

/** WIZARD_EVENT_KIND_CLASS is the console's own word for an event's kind, drawn
 * first in the line; WIZARD_EVENT_STAGE_CLASS and WIZARD_EVENT_TEXT_CLASS are the
 * CLI's own fields behind it, each drawn through its own class
 * (`NEW_OPERATION_TEXT_CLASSES`). */
export const WIZARD_EVENT_KIND_CLASS = 'console-wizard-event-kind'
export const WIZARD_EVENT_STAGE_CLASS = 'console-wizard-event-stage'
export const WIZARD_EVENT_TEXT_CLASS = 'console-wizard-event-text'
export const WIZARD_EVENT_STATE_CLASS = 'console-wizard-event-state'
export const WIZARD_EVENT_AFTER_CLASS = 'console-wizard-event-after'

/** WIZARD_COMMAND_CLASS is one command of a `left_for_you` line, and
 * WIZARD_COPY_CLASS the Copy beside it -- drawn only where the builder admitted
 * the line, with WIZARD_COPY_REFUSAL_CLASS carrying its reason where it did
 * not. */
export const WIZARD_COMMAND_CLASS = 'console-wizard-command'
export const WIZARD_COPY_CLASS = 'console-wizard-copy'
export const WIZARD_COPY_REFUSAL_CLASS = 'console-wizard-copy-refusal'

/** WIZARD_FOLLOW_CLASS is the press that asks main for the apply's own feed. */
export const WIZARD_FOLLOW_CLASS = 'console-wizard-follow'

/** WIZARD_BUSY_CLASS is the one line that says a press is being answered. The
 * screen has a single one rather than a line per attempt -- it is the state of
 * the screen and not of an attempt -- and `send` writes it and clears it. */
export const WIZARD_BUSY_CLASS = 'console-wizard-busy'

/** WIZARD_LEAD and WIZARD_FOLLOW_LEAD are the console's own words for this
 * screen. They are not the CLI's sentences and not the onboarding copy's:
 * `installations.ts`'s `ONBOARDING_COPY` is the picker's too, and the sentence
 * that introduces a command line is not the sentence that introduces a form. */
export const WIZARD_LEAD =
  'Answer these five questions and the console runs the setup for you. It asks for no secret and opens no terminal at all: every step that needs a person comes back below as a line for you to run.'
export const WIZARD_FOLLOW_LEAD =
  'The plan above is the dry run. Once the confirmation window has been answered, follow the run to watch what it applies:'
export const WIZARD_FOLLOW_LABEL = 'Follow the run'

/** WIZARD_SUBMIT_LABEL is the button's own word. It names what the press does
 * (send the answers and plan) rather than what the run will do, because the plan
 * applies nothing. */
export const WIZARD_SUBMIT_LABEL = 'Plan the setup'

/** WIZARD_PLAN_BUSY and WIZARD_APPLY_BUSY are the busy line's two labels, one per
 * press, in the register every other label of this console's reads in
 * (`'Reading the project’s budget…'` and its neighbours). They name what the
 * press is waiting for rather than repeating the button, and they are two words
 * because the two presses wait for different things: the plan for a child that
 * answers with the whole feed at once, the apply for a run main holds for up to
 * its own hour. */
export const WIZARD_PLAN_BUSY = 'Planning the setup…'
export const WIZARD_APPLY_BUSY = 'Following the run…'

/** WIZARD_RIGOR_REQUIRED is the rigor row's own note: the one question whose
 * emptiness is the CLI's refusal rather than a default it takes. */
export const WIZARD_RIGOR_REQUIRED = 'the rigor mode has no default: a setup that answers no mode is refused'

/** WIZARD_BUDGET_DEFAULT is the budget row's own default, and it is the CLI's:
 * `managed.DefaultBudgetAmount` (100 whole units of USD, D2212). The field is
 * drawn with it, so a press that leaves it alone sends 100 -- which is what the
 * run would have used anyway -- and an operator who wants another amount types
 * over it. */
export const WIZARD_BUDGET_DEFAULT = '100'

/** WIZARD_BUDGET_NOTE is the budget row's note, and it says the one thing a
 * figure like this is misread as: the amount is the threshold Cloud Billing
 * warns the owner at, and **not** a cap on what a run may spend -- that is the
 * gateway's cost cap, a different figure in a different place (D64, D2210). The
 * unit is named because the field carries no currency: the console asks for the
 * amount and the run takes USD (D2211). */
export const WIZARD_BUDGET_NOTE =
  'the amount Cloud Billing warns you at, in whole US dollars; this is an alert and not a cap on what a run may spend'

/** WIZARD_DONE and WIZARD_HAND_OFF are the console's two sentences for a feed
 * that ended `done`: the run finished, and -- where it left steps for the
 * operator -- those steps are theirs. The CLI's own `done` line carries nothing
 * but its exit code, and a hand-off is exactly what exit 1 behind a `done` feed
 * means (ST2), which is why the second sentence is drawn from the lines above it
 * and not from the code. */
export const WIZARD_DONE = 'the setup run finished'
export const WIZARD_HAND_OFF = 'the lines above are the steps only you can run, each with its command'

/** WIZARD_KIND_WORDS is the console's own word per event kind, in the register
 * every other label this console draws uses. A kind has one word here or the
 * type does not compile, which is the reason it is a typed record rather than a
 * lookup that could miss. */
export const WIZARD_KIND_WORDS: Readonly<Record<SetupEvent['type'], string>> = Object.freeze({
  step: 'working',
  stage: 'stage',
  left_for_you: 'for you',
  done: 'done',
  error: 'failed',
})

/** WIZARD_UNDONE_WORD is what a stage the CLI skipped without doing its work says
 * behind its detail (`undone`, ST2). */
export const WIZARD_UNDONE_WORD = 'nothing was done for it'

/** WIZARD_COMMANDS_LEAD introduces a `left_for_you` line's commands, and
 * WIZARD_COMMAND_NONE is what a line with no command at all says -- the CLI may
 * leave a step that is a sentence rather than a command. */
export const WIZARD_COMMANDS_LEAD = 'run this in your terminal:'
export const WIZARD_COMMAND_NONE = 'this one has no command to run'

// ---------------------------------------------------------------------------
// The questions.
// ---------------------------------------------------------------------------

/** WizardOption is one choice of a select question: the value the answers object
 * carries and the console's own word for it. */
export interface WizardOption {
  readonly value: string
  readonly label: string
}

/**
 * WizardQuestion is one row of the form: the answer key it fills, the console's
 * own label and note, the control it is answered with, the default it is drawn
 * with, whether an empty answer is a refusal, and a select's choices.
 *
 * `default` is the string the control is drawn with -- `''` for a question the
 * CLI has no default for and for one whose answer is optional. It is applied as
 * the element's own attribute (`value` on an input, `selected` on an option),
 * which a document reads back as the field's value until the operator types.
 */
export interface WizardQuestion {
  readonly key: string
  readonly label: string
  readonly note: string
  readonly control: 'text' | 'select'
  readonly default: string
  readonly required: boolean
  readonly options?: readonly WizardOption[]
}

/**
 * WIZARD_RIGOR_MODES is the four modes the form offers, and `hotfix` is
 * deliberately not among them: it is an operator's own per-run mode and no layer
 * sets it (D834), which is why main refuses it for these answers
 * (`SETUP_LAYER_RIGOR_MODES`, held equal to the schema's enum). A select that
 * offered it would be offering an answer the console refuses, and this list is
 * held equal to main's own by `wizard.test.ts`.
 */
export const WIZARD_RIGOR_MODES: readonly SetupRigor[] = Object.freeze(
  SETUP_RIGOR_MODES.filter((mode) => mode !== 'hotfix'),
)

/** WIZARD_YES and WIZARD_NO are the bake switch's two answers, as the select's
 * own values. They are words and not `true`/`false`: the control's value is a
 * string, and the answers object's boolean is assembled from them (`WIZARD_YES`
 * is on). The default is yes, which is the CLI's own (`internal/imagebake`: only
 * a written no turns the bake off). */
export const WIZARD_YES = 'yes'
export const WIZARD_NO = 'no'

/**
 * WIZARD_QUESTIONS is the form's table, in the order `fugaro setup` asks its
 * questions: it is `SETUP_ANSWER_KEYS`' order, and `wizard.test.ts` holds the two
 * together, so a key added to the contract's five without a row here fails rather
 * than being a question nobody is asked.
 *
 * The defaults are the CLI's own: `region` is `us-east5` (the US place's region
 * when only the place is chosen, `place.DefaultRegion(place.US)`), `budget` is
 * `WIZARD_BUDGET_DEFAULT` and `bake_repos` is yes. `gcp_project` and `name` are
 * drawn empty -- the first is required and the second is optional, and the CLI's
 * own answer to a blank name is the `left_for_you` line that names it, which a
 * wizard that invented one would take away. `rigor` is the one row whose
 * `default` is empty *and* whose emptiness is a refusal; `budget` is optional
 * like `name`, and a blank one is an answer nobody gave rather than a refusal.
 */
export const WIZARD_QUESTIONS: readonly WizardQuestion[] = Object.freeze([
  Object.freeze({
    key: SETUP_ANSWER_KEYS[0] ?? '',
    label: 'GCP project ID',
    note: 'the Google Cloud project this installation’s jobs and its data live in',
    control: 'text' as const,
    default: '',
    required: true,
  }),
  Object.freeze({
    key: SETUP_ANSWER_KEYS[1] ?? '',
    label: 'Region',
    note: 'where the installation’s jobs and its data go; us-east5 is the default',
    control: 'text' as const,
    default: 'us-east5',
    required: true,
  }),
  Object.freeze({
    key: SETUP_ANSWER_KEYS[2] ?? '',
    label: 'Fugaro project name',
    note: 'what this installation is called; leave it empty and the setup hands the step that names it back to you',
    control: 'text' as const,
    default: '',
    required: false,
  }),
  Object.freeze({
    key: SETUP_ANSWER_KEYS[3] ?? '',
    label: 'Rigor mode',
    note: WIZARD_RIGOR_REQUIRED,
    control: 'select' as const,
    default: '',
    required: true,
    options: Object.freeze([
      Object.freeze({ value: '', label: 'choose a mode' }),
      ...WIZARD_RIGOR_MODES.map((mode) =>
        Object.freeze({ value: mode, label: RIGOR_MODE_WORDS[mode] ?? mode }),
      ),
    ]),
  }),
  Object.freeze({
    // The budget question (T2b of docs/managed-flag-ergonomics; D2210, D2211,
    // D2212). The field carries no currency: the run takes USD unless a
    // document says otherwise, and the console asks for the amount alone. It is
    // optional, and the drawn default is the CLI's own amount, so a press that
    // does not touch it sends the figure the run would have used.
    key: SETUP_ANSWER_KEYS[4] ?? '',
    label: 'Monthly budget alert',
    note: WIZARD_BUDGET_NOTE,
    control: 'text' as const,
    default: WIZARD_BUDGET_DEFAULT,
    required: false,
  }),
  Object.freeze({
    key: SETUP_ANSWER_KEYS[5] ?? '',
    label: 'Bake your repos into the Docker images?',
    note: 'this makes jobs start faster; the setup bakes unless you say no',
    control: 'select' as const,
    default: WIZARD_YES,
    required: false,
    options: Object.freeze([
      Object.freeze({ value: WIZARD_YES, label: 'yes, bake them' }),
      Object.freeze({ value: WIZARD_NO, label: 'no' }),
    ]),
  }),
])

// ---------------------------------------------------------------------------
// The sentences the press can draw.
// ---------------------------------------------------------------------------

/** wizardUnanswered is the console's own refusal of a required question the
 * operator left empty. It names the question by the console's own label -- never
 * a value, which there is none of -- and it is the whole of what the red step
 * asks for: rigor has no default, so this is the sentence a press that answered
 * no mode draws, and nothing is sent. */
export function wizardUnanswered(label: string): string {
  return `nothing is sent until this question is answered: ${label}`
}

/** wizardNotAChoice is the refusal of a select answered with something outside
 * its own choices, which only a caller that did not draw this form can produce. */
export function wizardNotAChoice(label: string): string {
  return `this question’s answer is not one of the choices it offers: ${label}`
}

/** WIZARD_BUDGET_REFRUSAL is the refusal of a budget field that is not a whole
 * number of units. It names the question and the rule and echoes no value: a
 * field is a value the operator typed, and the sentence is about what the
 * question takes. The bounds are the alert's own (`SETUP_BUDGET_MIN`,
 * `SETUP_BUDGET_MAX`), so a form that accepted an amount outside them would be
 * offering an answer the run refuses after a child exists. */
export function wizardBadBudget(label: string): string {
  return `this question takes a whole number of units between ${SETUP_BUDGET_MIN} and ${SETUP_BUDGET_MAX}: ${label}`
}

// ---------------------------------------------------------------------------
// The screen.
// ---------------------------------------------------------------------------

/** WizardDeps is what a caller hands the screen: the factory, main's two
 * operations, the clipboard, and the clock the feed is drawn on. */
export interface WizardDeps {
  readonly factory: WizardFactory
  /** plan is `setup.plan`, the dry run: it validates the answers, holds them as
   * the bytes it will apply, and opens the confirmation window. */
  readonly plan: (request: SetupPlanRequest) => Promise<NewOpsReply<SetupFeed>>
  /** apply is `setup.apply`, the renderer's door to the run the window's own yes
   * started. It is asked only by the follow control, on a press. */
  readonly apply: (request: NoArgumentRequest) => Promise<NewOpsReply<SetupFeed>>
  /** copy is the clipboard. The caller owns it, as it does for the Manage
   * section (`manage/shell.ts`'s `ManageDeps`), so this module touches no
   * `navigator`. */
  readonly copy: (text: string) => void
  /** paint is the clock one event is drawn on, injected so a case drives the
   * lines without waiting for the host's timer. The default is the next tick
   * (`NODE_PAINT`), one event per tick, the way `stream.ts` draws a feed. */
  readonly paint?: StreamPaint
}

/** DrawnQuestion is one question's row as it was drawn: the descriptor, the
 * control an answer is read from, and the element the row is. */
interface DrawnQuestion {
  readonly question: WizardQuestion
  readonly control: WizardElement
}

/** AssembleAnswer is the form's answer to a press: the answers object, or the
 * console's own sentence for the question that stopped it. */
type AssembleAnswer =
  | { readonly ok: true; readonly answers: SetupAnswers }
  | { readonly ok: false; readonly sentence: string }

/**
 * drawWizard draws the whole screen and answers its root. It draws and binds
 * only: nothing is read, asked or spawned until the submit is pressed.
 *
 * It is the entry point SW3c opens too -- the same screen in place of the
 * current installation -- which is why it takes no onboarding state and draws no
 * command line: what is above it is the caller's (`entry.ts`'s `drawOnboarding`
 * draws the heading it shares with the picker).
 */
export function drawWizard(deps: WizardDeps): WizardElement {
  const factory = deps.factory
  const paint = deps.paint ?? NODE_PAINT

  const root = make(factory, 'section', WIZARD_CLASS)
  root.append(literal(make(factory, 'p', WIZARD_LEAD_CLASS), WIZARD_LEAD))

  const questions = make(factory, 'div', WIZARD_QUESTIONS_CLASS)
  const drawn: DrawnQuestion[] = WIZARD_QUESTIONS.map((question) => {
    const row = make(factory, 'div', WIZARD_QUESTION_CLASS)
    row.setAttribute(WIZARD_QUESTION_ATTRIBUTE, question.key)
    // A question's label names its box the way the launch window's form does
    // (`launch.ts`'s `field`): a `<label for>` pointing at the control's own id,
    // so every control has an accessible name. The row's key is the id's tail, so
    // the two are one string rather than a second convention.
    const controlId = WIZARD_CONTROL_ID_PREFIX + question.key
    const label = literal(make(factory, 'label', WIZARD_LABEL_CLASS), question.label)
    label.setAttribute('for', controlId)
    row.append(label)
    row.append(literal(make(factory, 'p', WIZARD_NOTE_CLASS), question.note))
    const control = make(factory, question.control === 'select' ? 'select' : 'input', WIZARD_INPUT_CLASS)
    control.setAttribute('id', controlId)
    if (question.control === 'text') {
      // A text field's default is its `value` attribute: a document reads it back
      // as the field's value until the operator types, and there is no `value`
      // setter in the surface this screen draws into.
      control.setAttribute('value', question.default)
      control.setAttribute('type', 'text')
    } else {
      for (const option of question.options ?? []) {
        const drawnOption = literal(
          make(factory, 'option', WIZARD_OPTION_CLASS),
          option.label,
        )
        // An option's own value is the wire's word, and it is set as an attribute
        // for the reason the text field's default is: the element's `value` is
        // the chosen option's, which the document answers from this.
        drawnOption.setAttribute('value', option.value)
        if (option.value === question.default) drawnOption.setAttribute('selected', '')
        control.append(drawnOption)
      }
    }
    row.append(control)
    questions.append(row)
    return { question, control }
  })
  root.append(questions)

  // The answer line belongs to **the form** and to nothing else: it carries the
  // console's own refusal of a press that answered no required question, which is
  // the one outcome no run produced. Everything a run said is drawn inside that
  // press's own attempt block (below), so an outcome is never shown twice and an
  // earlier attempt is never rewritten -- which is also why an attempt is appended
  // beside the last one rather than over it: the surface this screen draws into
  // has no `replaceChildren`.
  const answer = make(factory, 'p', WIZARD_ANSWER_CLASS)
  // The busy line is the screen's, not an attempt's: it says what the press that
  // is outstanding is waiting for, and `send` writes and clears it. It is drawn
  // empty from the start rather than added on the press, so the state it carries
  // has one writer and one place in the tree.
  const busy = make(factory, 'p', WIZARD_BUSY_CLASS)
  const attempts = make(factory, 'div', WIZARD_ATTEMPTS_CLASS)

  /** say writes the console's own sentence about the form; the empty string
   * clears it, which is what a press that got past the form does. */
  const say = (sentence: string): void => {
    literal(answer, sentence)
  }

  /** send asks the operation and draws its answer as one attempt: the lines as
   * they are read, and the console's own sentence for the outcome.
   *
   * **The press is answered in the frame it happened in**, which is the console's
   * rule for a control change and matters most here: the child behind
   * `setup.plan` and `setup.apply` is main's, and it runs for as long as a setup
   * takes (up to the hour main holds it for), while this screen's answer is one
   * document that arrives in a single reply. So the label naming what is being
   * read goes up before the await, the attempt's own block is marked `aria-busy`,
   * and the two controls that start a read are held until it lands -- the
   * `finally` takes all four down on every path, including a bridge that throws,
   * because a screen left marked busy is the endless spinner this console's rules
   * refuse. The earlier attempt's own drawing is left exactly as it was: dimming it
   * would be dimming an outcome that has stopped changing (`base.css` dims what a
   * `data-busy` section still holds). */
  const send = async (
    word: string,
    ask: () => Promise<NewOpsReply<SetupFeed>>,
  ): Promise<void> => {
    const block = make(factory, 'div', WIZARD_ATTEMPT_CLASS)
    block.setAttribute(BUSY_BUSY_ATTRIBUTE, 'true')
    attempts.append(block)
    const lines = make(factory, 'div', WIZARD_LINES_CLASS)
    block.append(lines)
    literal(busy, word)
    root.setAttribute(BUSY_BUSY_ATTRIBUTE, 'true')
    submit.setAttribute('disabled', '')
    follow.setAttribute('disabled', '')
    try {
      const reply = await ask()
      if (!reply.ok) {
        // A gap is the console's or the CLI's sentence and nothing else: an
        // attempt that cannot be read is never drawn as an empty run.
        block.append(
          text(factory, 'p', WIZARD_ANSWER_CLASS, reply.gap.sentence, 'sentence', 'the setup’s own refusal'),
        )
        return
      }
      const feed = reply.value
      let left = 0
      for (const event of feed.events) {
        if (event.type === 'left_for_you') left += 1
        lines.append(drawEvent(factory, event, deps.copy))
        await painted(paint)
      }
      if (feed.done) {
        block.append(literal(make(factory, 'p', WIZARD_ANSWER_CLASS), WIZARD_DONE))
        if (left > 0) {
          block.append(literal(make(factory, 'p', WIZARD_ANSWER_CLASS), WIZARD_HAND_OFF))
        }
        return
      }
      if (feed.failure !== null) {
        // The feed's own `error` line is drawn above, with the stage it stopped at
        // and the fix; nothing is added here. A failure that reached this branch
        // with no `error` line in the feed is a shape the contract cannot produce
        // (`setupFeedAt` sets both together), and drawing nothing twice is what
        // keeps one failure from being two sentences.
        return
      }
      // No last line and no failure: an interrupted read, drawn as one.
      block.append(literal(make(factory, 'p', WIZARD_ANSWER_CLASS), STREAM_INTERRUPTED_WORD))
      const retry = renderRetry<WizardElement>(factory)
      block.append(retry)
      retry.addEventListener('click', () => {
        void send(word, ask)
      })
    } finally {
      literal(busy, '')
      root.removeAttribute(BUSY_BUSY_ATTRIBUTE)
      // The attempt is the one whose read this was, so its own mark goes with
      // the line: a block left marked busy would be a screen reader saying
      // "busy" about an answer already drawn.
      block.removeAttribute(BUSY_BUSY_ATTRIBUTE)
      submit.removeAttribute('disabled')
      follow.removeAttribute('disabled')
    }
  }

  const submit = literal(make(factory, 'button', WIZARD_SUBMIT_CLASS), WIZARD_SUBMIT_LABEL)
  submit.setAttribute('type', 'button')
  // The press reads the form, refuses an unanswered required question itself --
  // sending nothing at all -- and asks the plan with the answers object when it
  // can. There is no `form` around it, so Enter is not a second way to press this.
  submit.addEventListener('click', () => {
    const assembled = assemble(drawn)
    if (!assembled.ok) {
      say(assembled.sentence)
      return
    }
    say('')
    void send(WIZARD_PLAN_BUSY, () => deps.plan({ answers: assembled.answers }))
  })
  root.append(submit)

  // The follow control asks for the run the confirmation window's own yes started
  // (`setup.apply`, SW3a's renderer door to that feed). A plan nobody has
  // confirmed is refused with main's own sentence, drawn in the attempt like any
  // other answer: this screen never guesses when the window was answered.
  const followLead = literal(make(factory, 'p', WIZARD_LEAD_CLASS), WIZARD_FOLLOW_LEAD)
  const follow = literal(make(factory, 'button', WIZARD_FOLLOW_CLASS), WIZARD_FOLLOW_LABEL)
  follow.setAttribute('type', 'button')
  follow.addEventListener('click', () => {
    void send(WIZARD_APPLY_BUSY, () => deps.apply({}))
  })
  root.append(followLead)
  root.append(follow)

  root.append(busy)
  root.append(answer)
  root.append(attempts)
  return root
}

/** assemble reads the form and answers the answers object main validates, or the
 * console's own sentence for the question that stopped it. It is the one place
 * the wire object is built, so its members are spelled once and each is read
 * from the row whose key it is -- never copied across from a record whose shape
 * only this function believes in.
 *
 * The rigor member is the one that is not a plain string on the wire, so it is
 * looked back up among the modes this form offers: a value that is not one of
 * them is refused here rather than cast into the contract's own type and left for
 * main to refuse later with a sentence about a file.
 *
 * The budget member is the other one that is not a string, and it is the only
 * optional one: an empty field carries no amount at all, so it is left out of
 * the object rather than sent as a zero the run would read as a real alert. */
export function assemble(drawn: readonly DrawnQuestion[]): AssembleAnswer {
  const read: Record<string, string> = {}
  for (const row of drawn) {
    const value = row.control.value.trim()
    if (row.question.required && value === '') {
      return { ok: false, sentence: wizardUnanswered(row.question.label) }
    }
    if (row.question.control === 'select') {
      const options = row.question.options ?? []
      if (!options.some((option) => option.value === value)) {
        return { ok: false, sentence: wizardNotAChoice(row.question.label) }
      }
    }
    read[row.question.key] = value
  }
  const rigorRow = drawn.find((row) => row.question.key === 'rigor')
  const rigor = WIZARD_RIGOR_MODES.find((mode) => mode === read['rigor'])
  if (rigor === undefined) {
    // A form with no rigor row at all is a caller that did not draw this one, and
    // a rigor row whose value is not a mode is the same refusal as any other
    // select's: the sentence names the question, never the value.
    return {
      ok: false,
      sentence:
        rigorRow === undefined
          ? wizardUnanswered('Rigor mode')
          : wizardNotAChoice(rigorRow.question.label),
    }
  }
  const budgetRow = drawn.find((row) => row.question.key === 'budget')
  const typedBudget = read['budget'] ?? ''
  let budget: number | undefined
  if (typedBudget !== '') {
    // Digits only: `Number('1e3')`, `Number(' 0x10')` and `Number('1_000')` are
    // all amounts the field never offered, and the run takes a whole number of
    // units, so the shape is checked here rather than left to a cast.
    if (!/^[0-9]+$/.test(typedBudget)) {
      return { ok: false, sentence: wizardBadBudget(budgetRow?.question.label ?? 'Budget') }
    }
    budget = Number(typedBudget)
    if (budget < SETUP_BUDGET_MIN || budget > SETUP_BUDGET_MAX) {
      return { ok: false, sentence: wizardBadBudget(budgetRow?.question.label ?? 'Budget') }
    }
  }
  const answers: SetupAnswers = {
    gcp_project: read['gcp_project'] ?? '',
    region: read['region'] ?? '',
    name: read['name'] ?? '',
    rigor,
    bake_repos: read['bake_repos'] === WIZARD_YES,
  }
  return {
    ok: true,
    answers: budget === undefined ? answers : { ...answers, budget },
  }
}

/** drawEvent draws one feed line: the console's word for its kind, the stage or
 * step it belongs to, the CLI's own fields through their classes, and -- for a
 * `left_for_you` line -- every command it carries. */
function drawEvent(
  factory: WizardFactory,
  event: SetupEvent,
  copy: (text: string) => void,
): WizardElement {
  const line = make(factory, 'div', WIZARD_EVENT_CLASS)
  line.setAttribute(WIZARD_EVENT_ATTRIBUTE, event.type)
  line.append(literal(make(factory, 'span', WIZARD_EVENT_KIND_CLASS), WIZARD_KIND_WORDS[event.type]))
  if (event.type === 'step') {
    // A step's `stage` is the stage it is inside and its `name` the step; where
    // the CLI wrote no name the line still says where it is, which is what a
    // person watching a run wants from it. Both take the `stage` field's class: a
    // step's name is the same kind of word.
    line.append(
      text(
        factory,
        'span',
        WIZARD_EVENT_STAGE_CLASS,
        event.name === '' ? event.stage : event.name,
        'label',
        'the setup step',
      ),
    )
    if (event.detail !== '') {
      line.append(
        text(factory, 'span', WIZARD_EVENT_TEXT_CLASS, event.detail, 'sentence', 'the setup step’s detail'),
      )
    }
    return line
  }
  if (event.type === 'stage') {
    line.append(text(factory, 'span', WIZARD_EVENT_STAGE_CLASS, event.name, 'label', 'the setup stage'))
    line.append(
      text(factory, 'span', WIZARD_EVENT_STATE_CLASS, event.state, 'label', 'the setup stage’s state'),
    )
    if (event.after !== '') {
      line.append(
        text(factory, 'span', WIZARD_EVENT_AFTER_CLASS, event.after, 'label', 'the stage this one waited for'),
      )
    }
    if (event.detail !== '') {
      line.append(
        text(factory, 'span', WIZARD_EVENT_TEXT_CLASS, event.detail, 'sentence', 'the setup stage’s detail'),
      )
    }
    // `undone` is the CLI's own word for a stage that skipped without doing its
    // work (ST2), and the console draws its own word for it: the value is a
    // boolean, so there is nothing of a document's to put through a class.
    if (event.undone) {
      line.append(literal(make(factory, 'span', WIZARD_EVENT_STATE_CLASS), WIZARD_UNDONE_WORD))
    }
    return line
  }
  if (event.type === 'left_for_you') {
    line.append(drawLeftForYou(factory, event, copy))
    return line
  }
  if (event.type === 'error') {
    // The stage the run stopped at, then the CLI's sentence and the fix it
    // printed: the whole of the acceptance's "an `error` line's sentence with the
    // failed stage".
    if (event.stage !== '') {
      line.append(
        text(factory, 'span', WIZARD_EVENT_STAGE_CLASS, event.stage, 'label', 'the stage the setup stopped at'),
      )
    }
    line.append(
      text(factory, 'span', WIZARD_EVENT_TEXT_CLASS, event.error, 'sentence', 'the setup’s own sentence'),
    )
    if (event.fix !== '') {
      line.append(text(factory, 'span', WIZARD_EVENT_AFTER_CLASS, event.fix, 'sentence', 'the setup’s own fix'))
    }
    return line
  }
  return line
}

/** drawLeftForYou draws one step only the owner can do: its text, and then each
 * command it carries -- each with a Copy where MG0's builder admits it, and its
 * reason where it does not. */
function drawLeftForYou(
  factory: WizardFactory,
  event: SetupLeftForYouEvent,
  copy: (text: string) => void,
): WizardElement {
  const body = make(factory, 'div', WIZARD_EVENT_TEXT_CLASS)
  body.append(text(factory, 'p', WIZARD_COMMAND_CLASS, event.text, 'sentence', 'the step left for you'))
  if (event.commands.length === 0) {
    // A `command` step with no `commands` is its own one command: the CLI's
    // `PrintedCommands` answers exactly that (`initflow/stage.go`), and it is the
    // shape of `fugaro update`, `fugaro update-skills` and `fugaro orchestrator
    // install` -- a line a first setup leaves behind constantly. The line above is
    // that command, so it is offered as a Copy where the builder admits it and is
    // **not** drawn a second time beneath itself.
    if (event.kind !== WIZARD_LEFT_COMMAND_KIND) {
      body.append(literal(make(factory, 'p', WIZARD_NOTE_CLASS), WIZARD_COMMAND_NONE))
      return body
    }
    appendCopy(factory, body, event.text, copy)
    return body
  }
  body.append(literal(make(factory, 'p', WIZARD_NOTE_CLASS), WIZARD_COMMANDS_LEAD))
  for (const command of event.commands) {
    body.append(
      text(factory, 'code', WIZARD_COMMAND_CLASS, command, 'sentence', 'a command left for you'),
    )
    appendCopy(factory, body, command, copy)
  }
  return body
}

/** appendCopy draws one command's Copy where MG0's builder admits it, the
 * builder's own reason where it refuses the line, and nothing at all for a line
 * this console copies nothing from. */
function appendCopy(
  factory: WizardFactory,
  body: WizardElement,
  command: string,
  copy: (text: string) => void,
): void {
  const admitted = wordCopy(command)
  if (admitted === null) {
    // The two words this console may hand a clipboard are `fugaro` and
    // `gcloud`, and any other opening word is a sentence rather than one of
    // them: the line is displayed and no Copy is drawn beside it, which is the
    // whole of "displayed only".
    return
  }
  if (!admitted.ok) {
    // A word the builder refused: the line is drawn as text and the Copy is
    // not, with the builder's own reason beside it -- never a repaired command.
    body.append(literal(make(factory, 'p', WIZARD_COPY_REFUSAL_CLASS), admitted.reason))
    return
  }
  const label = copyCommandText(admitted.action)
  const control = literal(make(factory, 'button', WIZARD_COPY_CLASS), label)
  control.setAttribute('type', 'button')
  control.addEventListener('click', () => copy(label))
  body.append(control)
}

/** wordCopy is MG0's builder over one line of the CLI's text: the console's own
 * two words are the only words it writes, and every other word of the line is a
 * value from the CLI's document. It answers `null` for a line this console copies
 * nothing from at all -- one that opens with neither word -- and the builder's
 * own refusal for a line whose words it will not admit. */
function wordCopy(command: string): CopyCommandAnswer | null {
  const words = command.split(/\s+/).filter((word) => word !== '')
  const first = words[0] ?? ''
  if (first !== WIZARD_COPY_WORDS[0] && first !== WIZARD_COPY_WORDS[1]) return null
  return buildCopyCommand([commandWord(first), ...words.slice(1).map(partOf)])
}

/** partOf classifies one word after the command's own name. A word that opens
 * with a dash is a **flag**, and a flag is the console's kind of word rather than
 * a document's value: MG0's `commandWord` holds it to `FLAG_PATTERN`, which is the
 * value alphabet plus the leading dashes a flag needs and the same `..` rule, so a
 * word that carries a space or a character a shell would read as syntax is refused
 * either way. Without this a `left_for_you` line carrying a flag -- `fugaro setup
 * --operator me --admin me`, and most `gcloud` lines -- was refused by the
 * builder's value alphabet for opening with a dash, which is the rule that keeps a
 * document from **choosing** an option of a command this console writes; here the
 * line is the CLI's own printed command, flag and all, so the console hands back a
 * line it was given rather than letting a value pick a flag. */
function partOf(word: string): CommandPart {
  return word.startsWith('-') ? commandWord(word) : commandValue(word)
}

/** WIZARD_LEFT_COMMAND_KIND is the CLI's own kind for a step whose text **is** its
 * one command (`initflow`'s `LeftCommand`). Every other kind's text is a sentence
 * -- something to do in a web console, a refusal -- and a sentence is drawn with
 * the console's "no command to run" note instead of a Copy. */
export const WIZARD_LEFT_COMMAND_KIND = 'command'

/** WIZARD_COPY_WORDS are the two first words a `left_for_you` line may open with
 * and still be offered as a Copy: the CLI itself and the Google Cloud CLI, which
 * are the two commands a first setup's hand-off lines name. Every other opening
 * word is a sentence this console draws and does not copy -- a line of prose, a
 * command of some other tool -- so the set is closed here rather than inferred
 * from a pattern. */
export const WIZARD_COPY_WORDS: readonly string[] = Object.freeze(['fugaro', 'gcloud'])

/** painted is one event's tick, as a promise the loop can await. */
function painted(paint: StreamPaint): Promise<void> {
  return new Promise<void>((resolve) => {
    paint(resolve)
  })
}
