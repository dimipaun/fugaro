// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
//
// The launch control on the Live view (console v2 plan V2.5.1b; design section
// 5.1 rule 11).
//
// V2.5.1a built the *operation*: `run.launch`, its closed five-field schema, the
// conversion from a repository identity to a clone URL, and the confirmation
// window the main process owns. This file is the renderer half of rule 11, and
// it is written against two rules that are the whole of why the control is safe
// to have:
//
//   - **the renderer adds no confirmation.** Main re-validates every field and
//     the confirmation window (`src/main/confirm.ts`) is the only confirmation
//     there is, so this form has no "are you sure", no preview of the argv and
//     no run id of its own: it says in one sentence that a window will open, and
//     then asks for it. A second gate here would be a second policy that can
//     disagree with the first.
//   - **the renderer cannot name a path, a flag or a ref.** The form's four
//     controls are a repository chosen from the installation's own recorded
//     identities, a task, a title and a plan task -- and the launch call carries
//     exactly those five fields and no other key, which is what makes `--pr=1`,
//     `/etc/passwd` and `../x` typed into them values rather than arguments.
//     `launchCheck` below is a **convenience** mirror of `launchrules`' caps, so
//     an operator is told about an obviously refused value without a round trip.
//     It is not a boundary, and main refuses what it misses.
//
// Every value is a text node -- never a markup-parsing slot, never a link,
// never an image -- and every refusal is main's own sentence as text: rule 11
// draws the CLI's message verbatim, so a message that carries markup arrives as
// characters rather than as an element. The values that are a document's own go
// through the console's sanitizer like every other one: `launchResult`'s fields
// through `putText` with the field's own class (`fact` below), and the recorded
// repository identities -- which `entry.ts`'s `railRows` keeps **raw**, because
// that string is also the one main matches against its own records -- through
// `refusalText` for the label and nothing at all for the value (`options`).
//
// The three caps, the trim behind the counter and the cancel sentence are
// mirrored rather than imported, because a screen may not read a main-process
// value: `launch-form.test.ts` holds each mirror to its original, so a drift is
// a failing test rather than a form that refuses what main accepts or a decline
// drawn as a refusal.

import { sanitizeRefusal } from '../../shared/sanitize'
import { LAUNCH_CLI_FLOOR } from '../../shared/version'
import { putText } from '../app'
import type { ElementLike } from '../app'
import type { TextClass } from '../../shared/sanitize'

/**
 * LaunchElement is what this screen may draw into: `ElementLike`'s text slot and
 * attributes, a tag, children and `append`. It is deliberately no wider -- no
 * markup-parsing slot, no navigating attribute, no event handler and no style --
 * so a drawing that wanted one of those would have to widen this interface
 * first, in a diff a reviewer can see, rather than reach for it quietly.
 */
export interface LaunchElement extends ElementLike {
  readonly tag: string
  append(child: LaunchElement): void
  childNodes(): readonly LaunchElement[]
}

/** LaunchFactory is where elements come from: an argument rather than a
 * `document` reference, so the drawing can be asserted on with no window. */
export interface LaunchFactory {
  create(tag: string): LaunchElement
}

// ---------------------------------------------------------------------------
// The numbers, mirrored from `src/main/launchrules.ts`
// ---------------------------------------------------------------------------

/** LAUNCH_TASK_MAX_BYTES is `MAX_LAUNCH_TASK_BYTES`: the task's cap in UTF-8
 * bytes after the trim. */
export const LAUNCH_TASK_MAX_BYTES = 16_384
/** LAUNCH_TITLE_MAX_RUNES is `MAX_LAUNCH_TITLE_RUNES`: the title's cap in
 * characters. */
export const LAUNCH_TITLE_MAX_RUNES = 100
/** LAUNCH_PLAN_TASK_MAX_BYTES is `MAX_LAUNCH_PLAN_TASK_BYTES`: the plan task's
 * cap in bytes. */
export const LAUNCH_PLAN_TASK_MAX_BYTES = 128

/** LAUNCH_TASK_LIMIT is the task's cap as the counter spells it, so a counter
 * can never name a number the check does not use. */
export const LAUNCH_TASK_LIMIT = '16 KiB'

/**
 * LAUNCH_TRIM_SPACE is `launchrules`' `GO_TRIM_SPACE`: `strings.TrimSpace`'s set
 * (`unicode.IsSpace`), which is the trim the CLI applies to the task it is
 * given. It is written out rather than reached for through
 * `String.prototype.trim`, because the two are **not** the same set: JavaScript
 * also trims U+FEFF, which Go does not, and JavaScript does *not* trim U+0085,
 * which Go does. A counter that trimmed like JavaScript would show a task as
 * fitting that main then refuses -- the defect this mirror exists for -- and one
 * that trimmed nothing at all would report bytes the run does not keep.
 *
 * Every code point in the set is in the BMP, so a scan by code unit is exact and
 * no member is half of a surrogate pair. The set is read twice per call -- once
 * from each end -- which is what keeps a task a renderer filled with whitespace
 * from costing this screen seconds, the reason `launchrules` writes the set out
 * there rather than using a regular expression.
 *
 * `launch-form.test.ts` holds it to `launchrules`' own, code point by code
 * point, through `checkLaunchTask`.
 */
export const LAUNCH_TRIM_SPACE: ReadonlySet<number> = new Set([
  0x09,
  0x0a,
  0x0b,
  0x0c,
  0x0d,
  0x20,
  0x85,
  0xa0,
  0x1680,
  0x2000,
  0x2001,
  0x2002,
  0x2003,
  0x2004,
  0x2005,
  0x2006,
  0x2007,
  0x2008,
  0x2009,
  0x200a,
  0x2028,
  0x2029,
  0x202f,
  0x205f,
  0x3000,
])

/** launchTrimmed is the CLI's `strings.TrimSpace`, mirrored: the leading and
 * trailing run of `LAUNCH_TRIM_SPACE` characters goes and nothing inside the
 * text does. Two index scans over `charCodeAt`, the way `launchrules`'
 * `trimTaskText` does it. */
export function launchTrimmed(text: string): string {
  let start = 0
  let end = text.length
  while (start < end && LAUNCH_TRIM_SPACE.has(text.charCodeAt(start))) start += 1
  while (end > start && LAUNCH_TRIM_SPACE.has(text.charCodeAt(end - 1))) end -= 1
  if (start === 0 && end === text.length) return text
  return text.slice(start, end)
}

/**
 * LAUNCH_DECLINED_REASON is `DECLINED_CANCELLED`, the main process's sentence
 * for a confirmation window the operator closed. The renderer has to *recognise*
 * it to tell a decline from every other refusal, and the only way it can is to
 * hold the same string: `launch-form.test.ts` pins the two equal, so a reworded
 * cancel sentence is a failing test rather than a decline drawn as a refusal.
 */
export const LAUNCH_DECLINED_REASON = 'the launch was not confirmed: nothing was spawned'

// ---------------------------------------------------------------------------
// The words
// ---------------------------------------------------------------------------

export const LAUNCH_OPEN_WORD = 'Launch a run'
export const LAUNCH_SUBMIT_WORD = 'Open the confirmation window'
export const LAUNCH_OPENS_WINDOW =
  'a confirmation window will open, showing the installation, the repository as this installation records it and the repository the launch will carry, the ref, the title, the plan task, the run id, the exact command and the whole task; nothing launches until that window’s own Launch button is pressed'
export const LAUNCH_NEEDS_CLI = `launching needs CLI ${LAUNCH_CLI_FLOOR} or newer`
export const LAUNCH_NO_REPOSITORY =
  'this installation records no repository, so there is nothing to launch in'
export const LAUNCH_LAUNCHED_WORD = 'the run launched'
export const LAUNCH_ALREADY_WORD = 'the run was already launched'
export const LAUNCH_REPLAYED_WORD = 'this is the answer this console already gave for this launch'
export const LAUNCH_DECLINED_WORD = 'not confirmed: nothing was launched'
export const LAUNCH_OUTCOME_UNKNOWN_WORD = 'the outcome is unknown'
export const LAUNCH_UNREADABLE =
  'the console could not read the launch’s answer, so nothing is drawn about it'
/** LAUNCH_UNDRAWN is what a sentence the sanitizer refused whole is drawn as,
 * the same marker `entry.ts` uses for its own. */
export const LAUNCH_UNDRAWN = 'a line the console will not draw'

// ---------------------------------------------------------------------------
// The selectors
// ---------------------------------------------------------------------------

export const LAUNCH_CLASS = 'launch'
export const LAUNCH_OPEN_CLASS = 'launch-open'
export const LAUNCH_FORM_CLASS = 'launch-form'
export const LAUNCH_SUBMIT_CLASS = 'launch-submit'
export const LAUNCH_REPOSITORY_CLASS = 'launch-repository'
export const LAUNCH_TASK_CLASS = 'launch-task'
export const LAUNCH_TITLE_CLASS = 'launch-title'
export const LAUNCH_PLAN_TASK_CLASS = 'launch-plan-task'
export const LAUNCH_COUNT_CLASS = 'launch-task-count'
export const LAUNCH_HINT_CLASS = 'launch-hint'
export const LAUNCH_HELP_CLASS = 'launch-help'
export const LAUNCH_LABEL_CLASS = 'launch-label'
export const LAUNCH_OUTCOME_CLASS = 'launch-outcome'
export const LAUNCH_OUTCOME_BODY_CLASS = 'launch-outcome-body'
export const LAUNCH_OUTCOME_LINE_CLASS = 'launch-outcome-line'
export const LAUNCH_FACTS_CLASS = 'launch-facts'
export const LAUNCH_FACT_LABEL_CLASS = 'launch-fact-label'
export const LAUNCH_FACT_VALUE_CLASS = 'launch-fact-value'
export const LAUNCH_FLOOR_CLASS = 'launch-floor'
export const LAUNCH_EMPTY_CLASS = 'launch-empty'

/** LAUNCH_IDS are the ids the labels point at and `aria-describedby` names. They
 * are constants rather than literals so a `for` and an `id` cannot be spelled
 * differently in two places. */
export const LAUNCH_IDS = Object.freeze({
  form: 'launch-form',
  repository: 'launch-repository',
  repositoryHelp: 'launch-repository-help',
  task: 'launch-task',
  count: 'launch-task-count',
  title: 'launch-title',
  titleHelp: 'launch-title-help',
  planTask: 'launch-plan-task',
  planTaskHelp: 'launch-plan-task-help',
  hint: 'launch-hint',
})

// The attributes this screen sets are `class`, `id`, `for`, `type`, `hidden`,
// `value` -- on one `<option>`, and inert there (`options`) -- `aria-live`,
// `aria-describedby` and `aria-busy`, and nothing else: there is no navigating
// attribute, no resource attribute, no style and no event handler anywhere in
// this file.

// ---------------------------------------------------------------------------
// The drawing
// ---------------------------------------------------------------------------

function make(factory: LaunchFactory, tag: string, ...classes: string[]): LaunchElement {
  const element = factory.create(tag)
  if (classes.length > 0) element.setAttribute('class', classes.join(' '))
  return element
}

/** literal writes the console's own words -- a label, a heading, a whole
 * sentence -- into an element. It is not `putText`: that is the door a
 * *document's* string comes through, and running this file's own vocabulary
 * through the sanitizer would invite a reader to think a screen may write a
 * value without it. */
function literal(element: LaunchElement, text: string): LaunchElement {
  element.textContent = text
  return element
}

/**
 * options draws what the select offers: one `<option>` per recorded repository,
 * in the installation's own order.
 *
 * Each option carries **the recorded identity as its `value`**, and the drawn
 * text is a sanitizer's own answer for that identity: `entry.ts`'s `railRows`
 * keeps the row's list raw, and the label goes through `refusalText` here, so a
 * repository whose name is shaped like a credential is drawn as the marker while
 * the value the form sends is still the string main matches against its own
 * records (`launchRepoArgument`, `src/main/launch.ts`). A form that sent the
 * label would refuse every launch on an installation whose repository is named
 * like a token, and one that sent a *folded* identity would be refused by main
 * for a repository the operator did pick -- so the value is written as it came
 * and nothing here trims or rewrites it.
 *
 * The `value` attribute is the one attribute of this file that carries a
 * document's string, and it is inert: it is never parsed as markup, never
 * navigated to and never interpreted as a style. It is what a `select` reports,
 * because this screen cannot hold a value anywhere else -- `textContent` is the
 * label, and `entry.ts` reads the select's own `value` back.
 */
function options(
  select: LaunchElement,
  factory: LaunchFactory,
  repositories: readonly string[],
): void {
  for (const recorded of repositories) {
    const option = make(factory, 'option')
    option.setAttribute('value', recorded)
    literal(option, refusalText(recorded))
    select.append(option)
  }
}

function cell(
  parent: LaunchElement,
  factory: LaunchFactory,
  tag: string,
  ...classes: string[]
): LaunchElement {
  const element = make(factory, tag, ...classes)
  parent.append(element)
  return element
}

/** help is one of a control's own lines: an id for `aria-describedby` to name,
 * and the console's own words in it. */
function help(
  parent: LaunchElement,
  factory: LaunchFactory,
  id: string,
  text: string,
  ...classes: string[]
): LaunchElement {
  const element = cell(parent, factory, 'p', LAUNCH_HELP_CLASS, ...classes)
  element.setAttribute('id', id)
  return literal(element, text)
}

/** field is one labelled control: a `<label for>` before it, the id the label
 * names, and the lines that describe it. Every control on this form is made
 * here, so "no control without an accessible name" is a property of one
 * function rather than of four call sites. */
function field(
  parent: LaunchElement,
  factory: LaunchFactory,
  tag: string,
  controlId: string,
  className: string,
  label: string,
  described: readonly string[],
): LaunchElement {
  const labelElement = cell(parent, factory, 'label', LAUNCH_LABEL_CLASS)
  labelElement.setAttribute('for', controlId)
  literal(labelElement, label)
  const control = cell(parent, factory, tag, className)
  control.setAttribute('id', controlId)
  control.setAttribute('aria-describedby', described.join(' '))
  return control
}

/** LaunchView is what the control is drawn for: the installation, the repository
 * identities it records (in the CLI's order, deduplicated by the main process,
 * each one the string main matches against its own records -- kept **raw** by
 * `entry.ts`'s `railRows`, and redacted only for the label `options` draws), and
 * whether this console's CLI may be launched with at all. */
export interface LaunchView {
  readonly installation: string
  readonly repositories: readonly string[]
  readonly supported: boolean
}

/** LaunchFields is the form's own controls, so the wiring does not have to walk
 * the tree it just drew to find them. */
export interface LaunchFields {
  readonly repository: LaunchElement
  readonly task: LaunchElement
  readonly title: LaunchElement
  readonly planTask: LaunchElement
  readonly count: LaunchElement
  readonly hint: LaunchElement
  readonly submit: LaunchElement
}

/** LaunchControl is the whole control: the button, the sentence beside it, the
 * inline form, its fields and the region an outcome is drawn in. `open`, `form`
 * and `fields` are null where the thing itself does not exist -- no launch is
 * offered at all below the CLI floor, and none when the installation records no
 * repository, because there would be nothing to send. */
export interface LaunchControl {
  readonly root: LaunchElement
  readonly open: LaunchElement | null
  readonly form: LaunchElement | null
  readonly fields: LaunchFields | null
  readonly outcome: LaunchElement
}

/**
 * renderLaunch draws the whole control. The form is drawn hidden and the wiring
 * reveals it, because "Launch a run" is a button and a form that was already
 * open would be a button that did nothing. The button carries `aria-expanded`
 * and `aria-controls` for the form it un-hides, so the control it opens is
 * related to it rather than merely next to it, and the sentence that explains
 * the button is drawn **before** the form: read in order, the button, why
 * pressing it opens a window, then the fields.
 *
 * The repository is a `<select>` and never a text field: the identities it
 * offers are the installation's own recorded ones and nothing else (see
 * `options`).
 *
 * With no repository to offer there is no button and no form at all, and the
 * one sentence says so in the place the control would be: an installation that
 * records no repository has nothing to launch on, so a button here would be a
 * button that opens an empty form. Below the CLI floor it is the same shape with
 * the floor's own sentence -- and in both cases nothing on this screen reaches
 * `launchRun`, because there is nothing to send.
 */
export function renderLaunch(factory: LaunchFactory, view: LaunchView): LaunchControl {
  const root = make(factory, 'section', LAUNCH_CLASS)
  const outcome = make(factory, 'div', LAUNCH_OUTCOME_CLASS)
  outcome.setAttribute('aria-live', 'polite')

  if (!view.supported) {
    literal(cell(root, factory, 'p', LAUNCH_FLOOR_CLASS), LAUNCH_NEEDS_CLI)
    root.append(outcome)
    return { root, open: null, form: null, fields: null, outcome }
  }

  if (view.repositories.length === 0) {
    literal(cell(root, factory, 'p', LAUNCH_EMPTY_CLASS), LAUNCH_NO_REPOSITORY)
    root.append(outcome)
    return { root, open: null, form: null, fields: null, outcome }
  }

  const open = cell(root, factory, 'button', LAUNCH_OPEN_CLASS)
  open.setAttribute('type', 'button')
  open.setAttribute('aria-expanded', 'false')
  open.setAttribute('aria-controls', LAUNCH_IDS.form)
  literal(open, LAUNCH_OPEN_WORD)

  literal(cell(root, factory, 'p', LAUNCH_HELP_CLASS), LAUNCH_OPENS_WINDOW)

  const form = cell(root, factory, 'div', LAUNCH_FORM_CLASS)
  form.setAttribute('id', LAUNCH_IDS.form)
  form.setAttribute('hidden', '')

  const repository = field(
    form,
    factory,
    'select',
    LAUNCH_IDS.repository,
    LAUNCH_REPOSITORY_CLASS,
    'Repository',
    [LAUNCH_IDS.repositoryHelp, LAUNCH_IDS.hint],
  )
  options(repository, factory, view.repositories)
  help(form, factory, LAUNCH_IDS.repositoryHelp, 'one of the repositories this installation records')

  field(form, factory, 'textarea', LAUNCH_IDS.task, LAUNCH_TASK_CLASS, 'Task', [
    LAUNCH_IDS.count,
    LAUNCH_IDS.hint,
  ])
  const count = help(form, factory, LAUNCH_IDS.count, launchCounter(0), LAUNCH_COUNT_CLASS)

  field(form, factory, 'input', LAUNCH_IDS.title, LAUNCH_TITLE_CLASS, 'Title', [
    LAUNCH_IDS.titleHelp,
    LAUNCH_IDS.hint,
  ])
  help(
    form,
    factory,
    LAUNCH_IDS.titleHelp,
    `optional, one line, up to ${String(LAUNCH_TITLE_MAX_RUNES)} characters`,
  )

  field(form, factory, 'input', LAUNCH_IDS.planTask, LAUNCH_PLAN_TASK_CLASS, 'Plan task', [
    LAUNCH_IDS.planTaskHelp,
    LAUNCH_IDS.hint,
  ])
  help(
    form,
    factory,
    LAUNCH_IDS.planTaskHelp,
    `optional, <plan>/<task>, up to ${String(LAUNCH_PLAN_TASK_MAX_BYTES)} bytes`,
  )

  const hint = help(form, factory, LAUNCH_IDS.hint, '')
  hint.setAttribute('aria-live', 'polite')

  const submit = cell(form, factory, 'button', LAUNCH_SUBMIT_CLASS)
  submit.setAttribute('type', 'button')
  literal(submit, LAUNCH_SUBMIT_WORD)

  root.append(outcome)
  return {
    root,
    open,
    form,
    fields: {
      repository,
      task: fieldOf(form, LAUNCH_TASK_CLASS),
      title: fieldOf(form, LAUNCH_TITLE_CLASS),
      planTask: fieldOf(form, LAUNCH_PLAN_TASK_CLASS),
      count,
      hint,
      submit,
    },
    outcome,
  }
}

/** fieldOf is one control this function just drew, by its class. It is a lookup
 * rather than a second reference so `field` has one return and the four call
 * sites stay one line each. */
function fieldOf(form: LaunchElement, className: string): LaunchElement {
  const found = form.childNodes().find((element) => element.getAttribute('class') === className)
  if (found === undefined) throw new Error(`the launch form drew no ${className}`)
  return found
}

// ---------------------------------------------------------------------------
// The counter and the convenience check
// ---------------------------------------------------------------------------

/** launchBytes is the task's own byte count, trimmed the way the CLI trims it:
 * bytes and not characters, because a task is sent as UTF-8 and a three-byte
 * character is three of them. The trim is `launchTrimmed`, which is Go's set and
 * **not** `String.prototype.trim` -- a counter that trimmed like JavaScript
 * would show a task at the cap as fitting and main would then refuse it. It is a
 * convenience in any case, because main trims again with `launchrules`' own. */
export function launchBytes(text: string): number {
  return new TextEncoder().encode(launchTrimmed(text)).length
}

/** launchCounter is the counter's line: the bytes as typed, against the cap. */
export function launchCounter(bytes: number): string {
  return `${String(bytes)} of ${LAUNCH_TASK_LIMIT}`
}

/** LAUNCH_CONTROL_RE and LAUNCH_FORMAT_RE are `launchrules`' `CONTROL_RE` and
 * `FORMAT_RE`: the C0 and C1 controls -- the newline and the tab among them --
 * and the format characters, which pass a control check while still reversing or
 * joining the line a person reads. */
const LAUNCH_CONTROL_RE = /\p{Cc}/u
const LAUNCH_FORMAT_RE = /\p{Cf}/u

/** LaunchTyped is the four controls as the operator left them. */
export interface LaunchTyped {
  readonly repository: string
  readonly task: string
  readonly title: string
  readonly planTask: string
}

/**
 * launchCheck answers, in the console's own words, the first reason this form
 * would obviously be refused -- or `null`.
 *
 * **It is a convenience and not a boundary.** Main re-validates every field
 * against `launchrules` and against its own recorded identities *before* a
 * window opens, and this function's whole job is to spare an operator a round
 * trip for a mistake they can see. It therefore mirrors only what this slice
 * fixes -- the three caps, the trim, an empty value after the trim, and the
 * control and format characters the title and the plan task may not carry -- and
 * **invents nothing**: a repository outside the recorded list, a plan task that
 * is not `<plan>/<task>`, a title of nothing but invisible characters are all
 * main's calls, and a check here that disagreed with main's would be a form that
 * refuses a launch main would accept.
 */
export function launchCheck(typed: LaunchTyped, repositories: readonly string[]): string | null {
  if (!repositories.includes(typed.repository)) {
    return 'choose one of the repositories this installation records'
  }
  // The task is sent **as typed** (`entry.ts`'s `sendLaunch` hands main
  // `typed.task`), so it is trimmed here with the CLI's own set -- which is what
  // main trims it with, and what makes the count below the count main makes. The
  // title and the plan task are the other way round: the entry trims those with
  // JavaScript's `trim` *before* it sends them, so each is trimmed the same way
  // here and the byte count is of the value main is handed.
  if (launchTrimmed(typed.task) === '') return 'type the task the run is given'
  const bytes = launchBytes(typed.task)
  if (bytes > LAUNCH_TASK_MAX_BYTES) {
    return `the task is ${String(bytes)} bytes after the trim, over the ${LAUNCH_TASK_LIMIT} this console sends`
  }
  const title = typed.title.trim()
  if (title !== '') {
    const runes = [...title].length
    if (runes > LAUNCH_TITLE_MAX_RUNES) {
      return `the title is ${String(runes)} characters, over the ${String(LAUNCH_TITLE_MAX_RUNES)} this console sends`
    }
    const problem = launchCharacterProblem(title, 'the title')
    if (problem !== null) return problem
  }
  const planTask = typed.planTask.trim()
  if (planTask !== '') {
    const planBytes = launchBytes(planTask)
    if (planBytes > LAUNCH_PLAN_TASK_MAX_BYTES) {
      return `the plan task is ${String(planBytes)} bytes, over the ${String(LAUNCH_PLAN_TASK_MAX_BYTES)} this console sends`
    }
    const problem = launchCharacterProblem(planTask, 'the plan task')
    if (problem !== null) return problem
  }
  return null
}

/** launchCharacterProblem is the rule the title and the plan task share here,
 * spelled as main spells it: no control character and no format character. */
function launchCharacterProblem(value: string, noun: string): string | null {
  if (LAUNCH_CONTROL_RE.test(value)) return `${noun} carries a control character`
  if (LAUNCH_FORMAT_RE.test(value)) return `${noun} carries a format character`
  return null
}

// ---------------------------------------------------------------------------
// The answer
// ---------------------------------------------------------------------------

/** LaunchResultView is the part of a `launchResult` this console draws: the six
 * facts rule 11 names, and the two status words D23 keeps apart. Nothing else of
 * the CLI's reply reaches a screen -- the recipe and the project layer are drawn
 * by no view of this console -- and `resultView` is the door that holds the
 * reply to that. */
export interface LaunchResultView {
  readonly run: string
  readonly repo: string
  readonly branch: string
  readonly execution: string
  readonly logUrl: string
  readonly status: 'launched' | 'already-launched'
}

/** LaunchOutcomeRead is one answer, read closed. `unreadable` is a state of its
 * own rather than an error: a reply this build does not understand draws one
 * sentence and nothing else, which is the whole of "never a partial draw". */
export type LaunchOutcomeRead =
  | { readonly kind: 'launched'; readonly result: LaunchResultView; readonly replayed: boolean }
  | { readonly kind: 'declined' }
  | { readonly kind: 'unknown'; readonly reason: string }
  | { readonly kind: 'refused'; readonly reason: string }
  | { readonly kind: 'unreadable' }

const UNREADABLE: LaunchOutcomeRead = Object.freeze({ kind: 'unreadable' as const })

/** OUTCOME_KEYS are the six keys `LaunchOutcome` answers with, all of them
 * always present, and RESULT_KEYS are the thirteen `launchResult` carries: the
 * seven the CLI always writes and the six `omitempty` ones. A key outside either
 * set is a reply from a console or a CLI this build does not know, and refusing
 * the whole answer is what keeps a field a newer main process added from being
 * drawn by an older renderer's guess. */
const OUTCOME_KEYS: readonly string[] = Object.freeze([
  'ok',
  'runId',
  'result',
  'reason',
  'outcomeUnknown',
  'replayed',
])
const RESULT_KEYS: readonly string[] = Object.freeze([
  'project',
  'run',
  'repo',
  'runId',
  'branch',
  'ref',
  'execution',
  'logUrl',
  'status',
  'pr',
  'previousRun',
  'recipe',
  'projectLayer',
])
const RESULT_STRINGS: readonly string[] = Object.freeze([
  'project',
  'run',
  'repo',
  'runId',
  'branch',
  'ref',
  'execution',
  'logUrl',
])

function record(raw: unknown): Record<string, unknown> | null {
  return typeof raw === 'object' && raw !== null && !Array.isArray(raw)
    ? (raw as Record<string, unknown>)
    : null
}

/** hasExactly is "this object's key set is this set", which is the closed read:
 * an object carrying a key outside `keys` is not one of these. */
function hasExactly(value: Record<string, unknown>, keys: readonly string[]): boolean {
  const found = Object.keys(value)
  return found.length === keys.length && found.every((key) => keys.includes(key))
}

/**
 * launchOutcomeOf reads one `run.launch` reply: the envelope the main process's
 * `dispatch` builds (`ConsoleReply`), and inside it the operation's
 * `LaunchOutcome`. Both are read **closed**, and every door out of this function
 * is a state `renderLaunchOutcome` draws -- there is no `null` and no throw,
 * because a reply nobody can read is a sentence rather than a broken screen.
 *
 * A **decline** is told from every other refusal by its sentence alone: main
 * answers a window the operator closed with `DECLINED_CANCELLED`, and this
 * file's mirror of that string is the only thing that separates "nothing was
 * launched, because you said so" from "nothing was launched, and here is why".
 */
export function launchOutcomeOf(reply: unknown): LaunchOutcomeRead {
  const envelope = record(reply)
  if (envelope === null) return UNREADABLE
  const keys = Object.keys(envelope)
  if (envelope['ok'] === false) {
    const reason = envelope['reason']
    if (keys.length !== 2 || keys.includes('data')) return UNREADABLE
    return typeof reason === 'string' && reason !== '' ? { kind: 'refused', reason } : UNREADABLE
  }
  if (envelope['ok'] !== true || keys.length !== 2 || !keys.includes('data')) return UNREADABLE
  const data = record(envelope['data'])
  if (data === null || !hasExactly(data, OUTCOME_KEYS)) return UNREADABLE
  const reason = data['reason']
  const runId = data['runId']
  const replayed = data['replayed']
  if (typeof data['ok'] !== 'boolean') return UNREADABLE
  if (typeof data['outcomeUnknown'] !== 'boolean') return UNREADABLE
  if (typeof replayed !== 'boolean') return UNREADABLE
  if (typeof reason !== 'string') return UNREADABLE
  if (runId !== null && typeof runId !== 'string') return UNREADABLE
  if (data['outcomeUnknown']) {
    // A launch stopped before it answered is the one state where this console
    // knows nothing about what the child did, and the run id is the only thing
    // that can settle it: an answer naming no run is not one to draw a
    // settlement for, and main's own sentence names both the run and the reads.
    return typeof runId === 'string' && runId !== ''
      ? { kind: 'unknown', reason: refusalText(reason) }
      : UNREADABLE
  }
  const result = data['result']
  if (data['ok']) {
    const drawn = resultView(result)
    if (drawn === null || reason !== '') return UNREADABLE
    return { kind: 'launched', result: drawn, replayed }
  }
  // A refused launch carries no result, and its sentence is what is drawn: the
  // CLI's own words through `refusalText`, or this console's for a cancel.
  if (result !== null || reason === '') return UNREADABLE
  return reason === LAUNCH_DECLINED_REASON ? { kind: 'declined' } : { kind: 'refused', reason }
}

/** resultView reads the six facts this console draws out of a `launchResult`,
 * closed on the key set and on every type. The two structures nothing draws
 * (`recipe` and `projectLayer`) are held to "an object or null" and are not read
 * further: no screen is handed them, so there is nothing for a partial draw to
 * be partial about. */
function resultView(raw: unknown): LaunchResultView | null {
  const value = record(raw)
  if (value === null || !hasExactly(value, RESULT_KEYS)) return null
  for (const key of RESULT_STRINGS) {
    if (typeof value[key] !== 'string') return null
  }
  const status = value['status']
  if (status !== 'launched' && status !== 'already-launched') return null
  const pr = value['pr']
  if (pr !== null && typeof pr !== 'number') return null
  for (const key of ['recipe', 'projectLayer']) {
    if (value[key] !== null && record(value[key]) === null) return null
  }
  return {
    run: value['run'] as string,
    repo: value['repo'] as string,
    branch: value['branch'] as string,
    execution: value['execution'] as string,
    logUrl: value['logUrl'] as string,
    status,
  }
}

/** refusalText is one of main's own sentences as this console draws it: through
 * `sanitizeRefusal`, which keeps a path, a project and a role as they were and
 * redacts a token, an address or a URL. A sentence the pass refuses whole is
 * drawn as the marker rather than as nothing, because a screen that silently
 * dropped the reason would be a refusal with no explanation at all. */
export function refusalText(raw: string): string {
  const result = sanitizeRefusal(raw, 'the launch’s refusal')
  return result.ok ? result.text : LAUNCH_UNDRAWN
}

/**
 * fact is one label-and-value line of a launch's outcome: the label this file's
 * own words, the value through `putText`, in a `dt`/`dd` pair so the two are
 * lines of their own rather than one run of text.
 *
 * The value is a document's string -- `launchResult`'s own fields -- so it goes
 * through a class like every other document string this console draws, and the
 * class is the field's own: `runId` for the run, `repository` for the `owner/name`
 * main checked, `branch`, `slug` for the execution and `url` for the CLI's
 * `log_url` (drawn as text and never as a link: rule 11 carries the URL, and a
 * link would be one more door out of the console for a string that came from a
 * run). Writing it raw would be the one place a run's own text reached a screen
 * unfiltered -- `decodeLaunchResult` bounds a string and type-checks it, and
 * strips no control or format character, so a branch name carrying a bidi
 * override would be drawn as it arrived. A value a class will not admit is drawn
 * as the console's unavailable mark rather than as nothing, which is the
 * treatment `graph.ts` gives a value its class refuses.
 */
function fact(
  parent: LaunchElement,
  factory: LaunchFactory,
  name: string,
  value: string,
  kind: TextClass,
): void {
  literal(cell(parent, factory, 'dt', LAUNCH_FACT_LABEL_CLASS), name)
  putText(cell(parent, factory, 'dd', LAUNCH_FACT_VALUE_CLASS), value, kind, `the launch result's ${name}`)
}

/** line is one sentence of an outcome, appended to the region's body. */
function line(parent: LaunchElement, factory: LaunchFactory, sentence: string): LaunchElement {
  return literal(cell(parent, factory, 'p', LAUNCH_OUTCOME_LINE_CLASS), sentence)
}

/**
 * renderLaunchOutcome draws one answer as sentences and facts, in the region
 * `renderLaunch` marked `aria-live` -- so an operator looking elsewhere is told
 * when the answer arrives.
 *
 * The log URL is drawn as **text**: rule 11 carries the CLI's own `log_url`, and
 * a link here would be one more door out of the console for a string that came
 * from a run. And there is **no control of any kind** in this tree, whatever the
 * outcome: open point 6 (a held run id and a retry) is still open, so nothing
 * here offers to launch again.
 */
export function renderLaunchOutcome(factory: LaunchFactory, read: LaunchOutcomeRead): LaunchElement {
  const root = make(factory, 'div', LAUNCH_OUTCOME_BODY_CLASS)
  if (read.kind === 'unreadable') {
    line(root, factory, LAUNCH_UNREADABLE)
    return root
  }
  if (read.kind === 'declined') {
    line(root, factory, LAUNCH_DECLINED_WORD)
    return root
  }
  if (read.kind === 'refused') {
    line(root, factory, refusalText(read.reason))
    return root
  }
  if (read.kind === 'unknown') {
    line(root, factory, LAUNCH_OUTCOME_UNKNOWN_WORD)
    line(root, factory, read.reason)
    return root
  }
  line(
    root,
    factory,
    read.result.status === 'already-launched' ? LAUNCH_ALREADY_WORD : LAUNCH_LAUNCHED_WORD,
  )
  const facts = cell(root, factory, 'dl', LAUNCH_FACTS_CLASS)
  fact(facts, factory, 'run', read.result.run, 'runId')
  fact(facts, factory, 'repository', read.result.repo, 'repository')
  fact(facts, factory, 'branch', read.result.branch, 'branch')
  fact(facts, factory, 'execution', read.result.execution, 'slug')
  if (read.result.logUrl !== '') fact(facts, factory, 'log', read.result.logUrl, 'url')
  if (read.replayed) line(root, factory, LAUNCH_REPLAYED_WORD)
  return root
}
