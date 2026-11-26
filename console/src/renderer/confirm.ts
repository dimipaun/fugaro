// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The confirmation window's document (design section 5.1 rules 11 and 12;
// console V2.5.1a part 2b-2a, the settings-edit plan's C3, SU1/D2902).
//
// This is the document half of the window `src/main/confirm.ts` opens: the main
// process builds the whole spawn once, freezes it under an identity, and shows
// the operator **that frozen object** before a child exists. The view draws it
// and sends back one answer -- nothing here decides anything, and nothing here
// can reach a record, a path or the CLI.
//
// Rule 11 is the whole of the design of the launch half of this file, and three
// of its sentences are the structure rather than a promise:
//
//   - **"the window shows these fields and no others, all of them the main
//     process's own values"** -- `fields()` is read once per phase, and every
//     value drawn below is a string, a number or an array of strings out of that
//     answer. The view names no path, no URL and no command of its own;
//   - **"it renders every value as a text node"** -- the only write to an
//     element is `textContent`, and the element slice below has no markup slot,
//     no `href`, no `src` and no `style` for one to be written through. A task
//     text is a value an operator (or a repository's title) chooses, and this is
//     what keeps `<img onerror=…>` a sentence the operator reads rather than
//     something the window runs;
//   - **"the whole task text, displayed in full and scrollable"** -- the text is
//     written whole and never summarised, and the box that holds it scrolls in
//     the stylesheet. The display sanitizer is deliberately **not** applied to
//     it: `sanitizeRefusal` folds and truncates a long string, and a window that
//     showed a shortened task would be the app summarising what the design says
//     the operator reads.
//
// **The setup half is the same window with a subject of its own** (SW3a,
// D2904): `'setup'` is a first `fugaro setup`, and what it draws is the
// answers **the main process parsed** (never the renderer's own object), the
// SHA-256 of the bytes the child is given on its stdin, the two command lines,
// and the console's four sentences of what a yes does -- that `--json` confirms
// no step, that everything needing a person comes back as a line for the
// operator's terminal, that this yes is their go to run the CLI and not a
// consent the CLI accepts, and that the Terraform plan is taken again at apply.
// The window says all four **before** it asks, which is the whole reason they
// are here rather than on a screen behind it.
//
// **The settings halves are the same window with two more subjects** (D1115's
// union, D2902's one modal): `'settings'` is C3's confirmation of an edit whose
// argv main has already frozen, and `'settings-entry'` is the entry form SU1
// added -- the label, the description, the raw key, the value a run reads now,
// the control the key's type calls for, R6's built-in default, and the OK that
// runs the dry run. **OK does not write**: it submits the typed value to the
// main process, which runs `settings set|unset … --dry-run --json` and then
// *replaces this window's subject* with the dry run's diff (D1106, rule 12's
// step 2). This document learns that by re-reading `fields()` -- the same
// one-way read it was opened with -- and draws the diff in the same window:
// old, new, the layer written, the layer that decides today, the CLI's own
// `why`, the exact argv, and the second button, which is the one that writes.
//
// **The two Manage writes are the same window with two more subjects** (MW1's
// `models add` and MW2's `image add`; the plan's MWR draws them here). Each draws
// the CLI's own `--dry-run --json` document as **main parsed it**
// (`modelsview.ts`, `imagesview.ts`) -- the claim or the entry the write would
// make, the steps `--no-apply` saves, the plan's digest and the exact argv the
// yes spawns -- and nothing this document invents: the values the operator typed
// reached main as a payload, and what the window shows is the document main
// asked the CLI for and then re-checked against that payload. The models write is
// a **widening** and wears the tick the settings edit wears; the image write is a
// narrowing and has none.
//
// **R5's validation is the shared module's, not a second copy here.**
// `settingsEntryError` (SU1's red line: "a number outside `min..max` leaves OK
// disabled") is imported from `src/shared/settingsentry.ts`, which is where the
// rule lives because this document cannot import `src/main/`. It is a **pre-check
// for the button** and never the decision: a value it passes still reaches the
// CLI, whose own reader refuses what it refuses, and the dry run is the
// authority at OK.
//
// **The two halves of a string's provenance are drawn differently.** Every
// string a *document* wrote -- the read's row members and the dry run's -- goes
// through `sanitizeText` with the `TEXT_CLASSES` class the Settings section
// draws the same value with (`projects.ts`'s `settingsRowLine`: a key is
// `branch`, a value and a layer are `label`, a sentence is `sentence`), and a
// refusal is drawn as this view's own `CONFIRM_UNDRAWABLE` rather than as the
// value. The window's **argv is not one of those strings**: it is the console's
// own command line, whose first word is the CLI's own path, and rule 12's
// promise is that the window shows what is spawned rather than a summary of it
// -- a class that draws a path as `<path>` would break exactly that promise, the
// way `sanitizeRefusal`'s own doc says of the CLI's refusal sentences. So the
// argv reaches the screen as it does today, for both halves.
//
// The keyboard is the one place this view is stricter than it looks. The main
// process already declines Enter and Escape (`before-input-event` in
// `src/main/confirm.ts`), so those two cancel whatever this document does; and
// this document makes each **writing** button -- the launch's, and the settings
// confirmation's -- **unreachable and unactivatable** by a keyboard as well: it
// is out of the tab order (`tabIndex = -1`), Cancel is focused on load, and its
// handler answers only a click that arrived from a pointer (a click a keyboard
// synthesized carries `detail === 0` and does nothing). That is "Cancel is the
// default button" carried through to the last input device, and it is why a task
// box an operator is scrolling with Space cannot confirm the run underneath
// them. **The entry form's OK and Reset are the deliberate exception**: what
// they reach is the dry run, which writes nothing at all (D1106), and a form
// only a pointer can submit is a form a keyboard operator cannot use at all.
// The write stays behind the pointer-only button it has always been behind.
//
// The delay a yes waits out is deliberately **not** here. It belongs to the main
// process (`CONFIRM_READY_DELAY_MS` in `src/main/confirm.ts`), which measures it
// from the window's own `show` and `focus`, because the hazard is a document
// that appears late: a timer in this document would be started by the very thing
// that arrived late. This view reads no clock at all -- it answers the moment a
// button is clicked, and an answer the main process refused comes back as a
// refusal and is drawn as the operator's sentence.

import type { ConfirmAnswer, ConfirmBridge } from '../preload/confirm'
import { sanitizeText } from '../shared/sanitize'
import type { TextClass } from '../shared/sanitize'
import { SETTINGS_ENTRY_READONLY, settingsEntryError } from '../shared/settingsentry'
import type { SettingsEntryView } from '../shared/settingsentry'

/** ConfirmFields is the display-only view this window is shown, mirrored from
 * `ConfirmView` in `src/main/launch.ts` the way the picker's answer types are
 * mirrored: the sandboxed renderer cannot import the main process's module, so
 * this is the shape it is written against, and `test/unit/confirm.test.ts` holds
 * the main process's side of the same fields. */
export interface ConfirmFields {
  /** which of the window's questions this is (D1115, D2902, SW3a). It is the
   * field the view branches on -- and it is a **tag**, not a hint: the launch's
   * members, the settings view's, the entry form's and the setup's are drawn by
   * four different sets of rows, and a subject that carried the wrong one would
   * draw the wrong window. */
  readonly kind: 'launch'
  readonly runId: string
  readonly installation: string
  readonly recordedRepository: string
  readonly repository: string
  readonly ref: string
  readonly title: string | null
  readonly planTask: string | null
  readonly argv: readonly string[]
  readonly taskByteLength: number
  readonly taskText: string
}

/** SettingsFields is the settings window's half of the same union, mirrored from
 * `SettingsConfirmView` in `src/main/launch.ts` -- which is C2's `SettingsView`
 * plus the subject's own members. Every value in it is a string, a boolean or an
 * array of strings the main process printed or decided; the view names nothing
 * of its own, and it decides nothing: `defaultButton`, `tick` and
 * `confirmNeedsTick` are the shape the dry run's classification produced
 * (D1109), not this document's reading of a value. */
export interface SettingsFields {
  readonly kind: 'settings'
  readonly project: string
  readonly key: string
  readonly oldValue: string
  readonly newValue: string
  readonly layer: string
  readonly target: string
  readonly wins: string
  readonly sentence: string
  readonly widening: boolean
  readonly why: string
  readonly argv: readonly string[]
  readonly defaultButton: 'confirm' | 'cancel'
  readonly tick: string
  readonly confirmNeedsTick: boolean
}

/** SettingsEntryFields is the entry form's half of the union, and it is **not a
 * mirror**: the shape is `src/shared/settingsentry.ts`'s `SettingsEntryView`,
 * the type main builds and this document draws, because the rule that decides
 * whether OK is enabled travels with it. A second copy here would be a second
 * copy of R5's rule. */
export type SettingsEntryFields = SettingsEntryView

/**
 * SetupFields is what the window is shown for a **first setup** (SW3a, D2904),
 * mirrored from `SetupView` in `src/main/setupview.ts` the way the other two are
 * mirrored from the main process's own modules.
 *
 * Two things about it are the point rather than the drawing:
 *
 *   - **the answers are main's**, parsed and held by `wizardrun.ts` before
 *     this window existed. They are drawn the way the launch's rows are -- as the
 *     main process's own values -- because that is what they are: every one of
 *     them has been through the CLI's own grammar, so a project row can hold
 *     nothing but `[a-z0-9-]`. The renderer's object reaches no row, which is the
 *     whole reason the operator is shown the parsed answers;
 *   - **`notes` are the console's own sentences**, four constants of
 *     `setupview.ts`: what a yes runs (`--json` confirms no step), what comes
 *     back as a line for the operator's terminal, that this yes is their go to
 *     run the CLI and not a consent the CLI accepts, and that the Terraform plan
 *     is taken again at apply. The window says all four before it asks, which is
 *     why they are drawn here and not on a screen behind the window.
 */
export interface SetupFields {
  readonly kind: 'setup'
  readonly project: string
  readonly region: string
  readonly name: string
  readonly rigor: string
  readonly bake: string
  readonly budget: string
  readonly digest: string
  readonly planArgv: readonly string[]
  readonly applyArgv: readonly string[]
  readonly notes: readonly string[]
}

/** ConfirmSubjectFields is what the window is shown: one of the six subjects
 * (D1115, D2902, SW3a; MW1's and MW2's writes are the last two). One window, one
 * question at a time, and one document that draws all of them. */
export type ConfirmSubjectFields =
  | ConfirmFields
  | SettingsFields
  | SettingsEntryFields
  | SetupFields
  | ModelsAddFields
  | ImageAddFields

/**
 * ModelsAddFields is what the window is shown for a **models write** (MW1,
 * D2903), mirrored from `ModelsAddView` in `src/main/modelsview.ts` the way the
 * other subjects are mirrored from their own main-process modules.
 *
 * Three things about it are the point rather than the drawing:
 *
 *   - **the document is the CLI's own dry run**, read member by member by
 *     `modelsview.ts` before this window existed. Nothing here decides what the
 *     write changes: the claim, the entry `budget.allowed_models` would gain,
 *     the price entry and the steps are the document's own members, and a
 *     document the reader could not vouch for was a refused window rather than a
 *     coerced one;
 *   - **`argv` is the write's, not the dry run's** -- what the operator agrees to
 *     is the command line the yes spawns -- and `digest` is the plan document's
 *     own, which is what main re-checks before it runs that argv;
 *   - **it is a widening, and the tick is how the window says so**: the write
 *     appends to the project's allowed models and writes a price, so it changes
 *     what every run of the project may name and spend on. The tick's words are
 *     the console's own constant (`modelsview.ts`), drawn from the view rather
 *     than from this file, and a yes without it is refused by main.
 */
export interface ModelsAddFields {
  readonly kind: 'models-add'
  readonly model: string
  readonly route: string
  readonly path: string
  readonly layer: string
  readonly changed: boolean
  readonly claimEntry: string
  readonly claimedBy: string
  readonly allowEntry: string
  readonly allowPresent: boolean
  readonly priceKey: string
  readonly priceIn: number
  readonly priceOut: number
  readonly priceSource: string
  readonly steps: readonly ModelsAddStepFields[]
  readonly project: string
  readonly digest: string
  readonly argv: readonly string[]
  readonly widening: true
  readonly defaultButton: 'cancel' | 'confirm'
  readonly tick: string
  readonly confirmNeedsTick: boolean
}

/** ModelsAddStepFields is one follow-up step of the document: the CLI's own name
 * for it and the state it would be left in -- `saved`, under `--no-apply`, which
 * is the whole of what this console's write does with it. */
export interface ModelsAddStepFields {
  readonly name: string
  readonly state: string
}

/**
 * ImageAddFields is what the window is shown for an **image write** (MW2,
 * D2903), mirrored from `ImageAddView` in `src/main/imagesview.ts` for the
 * reasons `ModelsAddFields` is mirrored from its own: the entry, its tags, its
 * description and the line `image ls` would print are the dry run document's
 * members, and the digest and the argv are main's.
 *
 * **It carries no tick and that is a decision, not an omission**: this write
 * raises no limit -- the document has no `widening` member for a window to
 * classify it by -- so its yes is one deliberate click. `confirmNeedsTick` is
 * spelled `false` rather than left out so that this document reads the same
 * member on every view it draws.
 *
 * `command` is carried because this shape mirrors main's view member for member
 * and is not drawn: it is the constant the reader already held the plan's own
 * `command` to, and `drawImageAdd`'s header says why it is no row.
 */
export interface ImageAddFields {
  readonly kind: 'image-add'
  /** the CLI's own name for the operation the plan is for (`image add`). */
  readonly command: string
  readonly project: string
  readonly name: string
  readonly tags: readonly string[]
  readonly description: string
  readonly entryDefault: boolean
  readonly source: string
  readonly steps: readonly ImageAddStepFields[]
  readonly digest: string
  readonly argv: readonly string[]
  readonly defaultButton: 'cancel' | 'confirm'
  readonly confirmNeedsTick: boolean
}

/** ImageAddStepFields is one follow-up step of the image document: the CLI's own
 * name for it, and whether `--no-apply` saves it -- which is what every one of
 * them is on this surface, the build included. */
export interface ImageAddStepFields {
  readonly name: string
  readonly saved: boolean
}

/** CONFIRM_LABEL and CANCEL_LABEL are the two buttons' words. Cancel is the
 * default and the one a keyboard reaches; the confirming button takes a
 * deliberate click. */
export const CONFIRM_LABEL = 'Run it'
export const CANCEL_LABEL = 'Cancel'

/** SAVE_LABEL is the settings window's confirming button, and it is a different
 * word from the launch's on purpose: this window writes one key of one file, and
 * a button that said "Run it" in both windows would be a window an operator could
 * answer without reading which question they were answering. It is also the
 * entry form's **second** button -- the one the same window grows after the dry
 * run has answered -- so the two words an operator presses in one edit are `OK`
 * and `Write it`, and the difference between them is the difference between
 * running a dry run and writing. */
export const SAVE_LABEL = 'Write it'

/** ENTRY_OK_LABEL is the entry form's own button: it runs the dry run, and
 * nothing is written by it (D1106, rule 12's step 2). */
export const ENTRY_OK_LABEL = 'OK'

/** SETUP_LABEL is the setup window's confirming button (SW3a). It is a third
 * word rather than the launch's for the reason `SAVE_LABEL` is a second one: this
 * yes runs `fugaro setup` for answers the operator has just read, and a button
 * saying "Run it" in both windows would be a window an operator could answer
 * without reading which question they were answering.
 */
export const SETUP_LABEL = 'Run setup'

/** ADD_MODEL_LABEL and ADD_ENTRY_LABEL are the two Manage writes' confirming
 * buttons (MW1's `models add`, MW2's `image add`). Each names what it adds,
 * because the two windows are one window and an operator who arrived at it from
 * a Manage table is owed the word that says which row of that table they are
 * about to write. */
export const ADD_MODEL_LABEL = 'Add the model'
export const ADD_ENTRY_LABEL = 'Add the entry'

/** ENTRY_RESET_LABEL is R6's reset: it submits the empty answer, which main
 * sends to the dry run as `settings unset` (F8: a reset writes no value at
 * all). */
export const ENTRY_RESET_LABEL = 'Reset to default'

/** ENTRY_RUNNING is what the window says while the dry run is out, so that a
 * window whose button has just gone quiet is a window that is working rather
 * than one that ignored the press. */
export const ENTRY_RUNNING = 'Running the dry run…'

/** ENTRY_SETTLED_UNEXPECTED is drawn when the answer to a submit is a yes but
 * the window's subject is still the form: the main process settles a submitted
 * form with the dry run's own view (rule 12's step 2), and a window that
 * re-read itself into anything else is a window this document will not guess
 * at. It is a sentence rather than a silent no because the operator pressed a
 * button and is owed the account. */
export const ENTRY_SETTLED_UNEXPECTED =
  'the dry run answered, but this window was not shown its result: nothing has been written'

/** NONE is what the window draws for a title or a plan task the launch does not
 * carry. An empty line would be a field an operator cannot tell from one the
 * main process drew nothing for. */
export const NONE = 'none'

/** CONFIRM_UNDRAWABLE is what this view draws where the sanitizer refuses a
 * string a document wrote: the console's own sentence about the *class*, and
 * never the string that was refused -- the same choice `installations.ts`'s
 * `UNDRAWABLE` makes for its own rows. */
export const CONFIRM_UNDRAWABLE = 'a value the console will not draw'

/** FIELDS are the labels of the rows, in the order the window draws them. */
const FIELDS = Object.freeze({
  installation: 'Installation',
  recordedRepository: 'Recorded repository',
  repository: 'Repository',
  ref: 'Ref',
  title: 'Title',
  planTask: 'Plan task',
  runId: 'Run id',
  command: 'Command',
  taskSize: 'Task size',
  project: 'Project',
  key: 'Setting',
  label: 'Name',
  description: 'Description',
  current: 'Current value',
  builtinDefault: 'Built-in default',
  afterReset: 'After a reset',
  newValue: 'New value',
  oldValue: 'Current value',
  layer: 'Written at',
  target: 'Target',
  wins: 'Decides today',
  sentence: 'What this changes',
  why: 'Why the CLI asks',
  region: 'Region',
  name: 'Name',
  rigor: 'Rigor',
  bake: 'Bake repositories',
  budget: 'Budget alert',
  digest: 'SHA-256 of the answers',
  planCommand: 'Plan command',
  applyCommand: 'Apply command',
  setupNotes: 'What this does',
  // The two Manage writes' own rows (MW1, MW2).
  model: 'Model',
  route: 'Route',
  configFile: 'Config file',
  changed: 'Would change the file',
  claimEntry: 'Route entry',
  coveredBy: 'Already covered by',
  allowedModels: 'Allowed models',
  price: 'Price $/M (in / out)',
  priceKey: 'Price entry',
  priceSource: 'Price source',
  steps: 'Steps this saves',
  entry: 'Entry',
  builtFrom: 'Built from',
  tags: 'Tags',
  defaultEntry: 'Default entry',
})

/** YES_WORD, NO_WORD, SAVED_WORD and UNSAVED_WORD are the console's own words
 * for the four booleans the two Manage documents carry: whether the write would
 * change the file, whether the project's allowed-models key is set at all,
 * whether an entry is the one a job naming nothing runs, and whether a step is
 * saved rather than run. They are drawn through `row` because they are the
 * console's vocabulary and not a document's string -- a document sent `true`, and
 * a window that drew `true` would be one an operator reads a value out of. */
export const YES_WORD = 'yes'
export const NO_WORD = 'no'
export const SAVED_WORD = 'saved'
export const UNSAVED_WORD = 'not saved'

/** ALLOWED_ADDED_WORD, ALLOWED_NAMES_WORD and ALLOWED_ABSENT_WORD are the three
 * answers `budget.allowed_models` gives a models plan, in the console's own
 * words: the list gains the entry the write would append, the list is set and
 * already names the model, or the key is not set at all. The third is D786's
 * rule, and it is **not** an empty list: a project whose key is absent allows
 * every model a route opens, so the window says the key is not set rather than
 * drawing `NONE` over it (`internal/cli/models_add.go`'s
 * `modelsAddPlanAllowText` spells the same three states). */
export const ALLOWED_ADDED_WORD = '+='
export const ALLOWED_NAMES_WORD = 'already names it'
export const ALLOWED_ABSENT_WORD = 'is not set, so it stays absent'

/** MODELS_PRICE_GIVEN is the one of a price entry's three sources (`given`,
 * `catalog` and `local`, contract section 6.33) whose entry the write lands: a
 * model the built-in catalog or the project's own file already prices needs no
 * `model_prices.<id>` entry, so the window draws the key as an entry for this
 * source alone and the source row says where the rates came from for the other
 * two. */
export const MODELS_PRICE_GIVEN = 'given'

/** TITLE_LAUNCH, TITLE_SETTINGS, TITLE_ENTRY, TITLE_SETUP, TITLE_MODELS_ADD and
 * TITLE_IMAGE_ADD are the six questions' headings, and TASK_HEADING is the
 * launch's own subtitle. The settings, setup and Manage windows have none: the
 * `<pre>` below holds the command line for a launch and nothing for an edit, a
 * setup or a write, and a heading over an empty box is a field an operator would
 * look for a value in. */
export const TITLE_LAUNCH = 'Confirm this run'
export const TITLE_SETTINGS = 'Confirm this settings change'
export const TITLE_ENTRY = 'Edit this setting'
export const TITLE_SETUP = 'Confirm this setup'
export const TITLE_MODELS_ADD = 'Confirm this models write'
export const TITLE_IMAGE_ADD = 'Confirm this image write'
export const TASK_HEADING = 'The task, as submitted'

/** TICK_ID is the checkbox's id. It exists so that its `<label for=…>` names it
 * the way any other control is named: the tick's words are a real label pointing
 * at the box, and not a text node beside it that a screen reader would read on
 * its own. */
export const TICK_ID = 'confirm-tick'

/** taskSizeText is the size row's whole value, and the label is the point of
 * it: `taskText` is `displayTaskText`'s output, which has the format characters
 * removed, so the text the window shows is **not** the byte count that was
 * submitted. The number is the submitted size, and the sentence says so. */
export function taskSizeText(byteLength: number): string {
  return `${String(byteLength)} bytes as submitted (format characters are not shown)`
}

// ---------------------------------------------------------------------------
// The slice of the DOM this view uses
// ---------------------------------------------------------------------------
//
// A handful of methods and three properties, declared here rather than taken
// from `lib.dom`, for the same reason the picker's view declares its own: the
// view is then drivable under plain Node, where there is no DOM at all, and the
// interface says exactly what the view may touch. There is no markup slot, no
// `href`, no `src` and no `style` in it -- a value of this window is a text node
// or it is not drawn.

/** ConfirmEvent is the slice of an event this view uses. `detail` is the one
 * field it reads, and it is the whole of the difference between a pointer click
 * (1 or more) and one a keyboard synthesized (0). */
export interface ConfirmEvent {
  readonly detail: number
}

/** ConfirmElement is one node. `checked`, `disabled` and `value` are the three
 * properties the entry form's controls and the two buttons carry: the tick is a
 * real checkbox and the buttons are real buttons (D1109), and a value the
 * operator typed is read back off the control it was typed into rather than kept
 * in this document -- the window holds no copy of a value that could drift from
 * the one on screen. */
export interface ConfirmElement {
  textContent: string | null
  tabIndex: number
  checked: boolean
  disabled: boolean
  value: string
  addEventListener(type: string, listener: (event: ConfirmEvent) => void | Promise<void>): void
  setAttribute(name: string, value: string): void
  appendChild(child: ConfirmElement): unknown
  replaceChildren(...nodes: ConfirmElement[]): void
  focus(): void
}

/** ConfirmDocument is the document, by id, plus `createElement` for the rows
 * and the buttons. */
export interface ConfirmDocument {
  getElementById(id: string): ConfirmElement | null
  createElement(tag: string): ConfirmElement
}

/** drawn is the one door every string a document wrote goes through (design
 * section 5.1 rule 7: the console re-applies the display layer's sanitization
 * rather than trusting the transport). `textClass` is the class the Settings
 * section draws the same value with, and `field` is the wire path, so a refusal
 * names the field it came from. The two outcomes are the whole design: safe text
 * is drawn as itself, and a refusal is drawn as `CONFIRM_UNDRAWABLE` -- the
 * refused value reaches no element. */
export function drawn(raw: string, textClass: TextClass, field: string): string {
  const result = sanitizeText(raw, textClass, field)
  return result.ok ? result.text : CONFIRM_UNDRAWABLE
}

/**
 * mountConfirm draws one subject and wires its answers. It reads `fields()`
 * exactly once per phase -- the view is not re-read except after a submit the
 * main process settled, because the frozen launch, or the frozen edit, is what
 * the window shows and re-reading could show a different one -- and it returns
 * when the window is drawn.
 *
 * The order is the point. **Cancel goes in first, before the subject is read**:
 * a `fields()` that never resolves, or that rejects rather than answering, must
 * still leave a focused way out, so the window is never one with nothing to
 * click. Once the fields arrive the other controls join it, a writing button
 * first and Cancel second, Cancel still the focused one for every subject that
 * can write; a refusal draws its sentence and no button at all.
 *
 * **One window, one question at a time** (D1115, D2902, SW3a): the subject's
 * `kind` is what decides which set of rows is drawn, and the branches share
 * every part that is not a field -- the cancel, the refusal, the pointer-only
 * writing button and the main process's readiness gate. Nothing here decides
 * anything any subject means; it draws what it was handed.
 */
export async function mountConfirm(document: ConfirmDocument, bridge: ConfirmBridge): Promise<void> {
  const status = document.getElementById('confirm-status')
  const fields = document.getElementById('confirm-fields')
  const task = document.getElementById('confirm-task')
  const actions = document.getElementById('confirm-actions')

  /** term appends one row's label, and detail appends one row's value: a
   * `dt`/`dd` pair per row, which is what the `dl` and the stylesheet's one rule
   * for it expect. */
  const term = (label: string): void => {
    if (fields === null) return
    const dt = document.createElement('dt')
    dt.textContent = label
    fields.appendChild(dt)
  }
  const detail = (value: string): ConfirmElement | null => {
    if (fields === null) return null
    const dd = document.createElement('dd')
    dd.textContent = value
    fields.appendChild(dd)
    return dd
  }

  /** row appends one labelled value the main process decided or the CLI printed
   * verbatim -- the launch's own rows, which are drawn as they arrive (rule 11:
   * the window shows the main process's own values). */
  const row = (label: string, value: string): void => {
    term(label)
    detail(value)
  }

  /** safeRow is the same row for a string a document wrote, and it is the only
   * way such a string reaches this document. */
  const safeRow = (label: string, raw: string, textClass: TextClass, field: string): void => {
    row(label, drawn(raw, textClass, field))
  }

  /** code appends one row whose value is a word the operator reads as code --
   * the raw key, and the command that writes a key this window does not. Its own
   * element is what makes it legible and selectable: the theme's `code` rule
   * gives it the mono face, and the operator selects it out of the window. */
  const code = (label: string, value: string): void => {
    term(label)
    const dd = detail('')
    if (dd === null) return
    dd.textContent = null
    const box = document.createElement('code')
    box.textContent = value
    dd.appendChild(box)
  }

  /** command appends the argv field by field, one word per row: every subject
   * shows what is spawned and not a summary of it. The words are the console's
   * own command line -- see this file's header for why they are not this view's
   * `sanitizeText` case.
   *
   * `label` is the launch's and the settings confirmation's single `Command` row,
   * and the setup window's two (`Plan command`, `Apply command`): one window
   * spawns two children in turn, and a window that drew both under one label
   * would leave the operator to guess which line is which. */
  const command = (argv: readonly string[], label: string = FIELDS.command): void => {
    term(label)
    for (const word of argv) detail(word)
  }

  const cancel = document.createElement('button')
  cancel.setAttribute('type', 'button')
  // The attribute is set as well as the `focus()` below: the attribute is what
  // a browser focuses on insertion, and the call is what makes it true of a
  // document that was already live when this ran.
  cancel.setAttribute('autofocus', '')
  cancel.textContent = CANCEL_LABEL
  cancel.addEventListener('click', async () => {
    await bridge.answer({ answer: 'no' })
  })

  // Cancel before the subject is read: a `fields()` that rejects, or that never
  // answers, must leave the operator a focused way out rather than a window with
  // no control on it. Escape, Enter and the close box already decline in the
  // main process; this is the document's own half of the same rule.
  if (actions !== null) actions.replaceChildren(cancel)
  cancel.focus()

  const reply = await bridge.fields()
  if (!reply.ok) {
    // A refusal is a window that may not spawn: the sentence is the main
    // process's, drawn as text, and Cancel is the only control left.
    if (status !== null) status.textContent = reply.reason
    return
  }

  const subject = reply.data as ConfirmSubjectFields
  if (status !== null) status.textContent = ''
  // The three static words are the window's, and each question is a different
  // question about a different thing: the heading says which, and the task box
  // -- which holds a task for a launch and nothing at all for a settings
  // subject -- loses the heading that would name a value the window does not
  // draw.
  const title = document.getElementById('confirm-title')
  const heading = document.getElementById('confirm-task-heading')

  /** drawLaunch is rule 11's set of rows, unchanged: the launch's own fields,
   * both repository spellings, and the task whole. */
  const drawLaunch = (launch: ConfirmFields): void => {
    row(FIELDS.installation, launch.installation)
    // Both repositories, because the conversion between them is a decision the
    // operator should see rather than infer (rule 11).
    row(FIELDS.recordedRepository, launch.recordedRepository)
    row(FIELDS.repository, launch.repository)
    row(FIELDS.ref, launch.ref)
    row(FIELDS.title, launch.title ?? NONE)
    row(FIELDS.planTask, launch.planTask ?? NONE)
    row(FIELDS.runId, launch.runId)
    command(launch.argv)
    row(FIELDS.taskSize, taskSizeText(launch.taskByteLength))
    if (task !== null) task.textContent = launch.taskText
  }

  /** drawSettings is the edit's set of rows: the two values, the two layers --
   * the one written and the one that decides today, which is D1113's comparison
   * drawn as two fields rather than as a sentence the window would have to
   * compute -- and, where the view carries them, the shadowed sentence and the
   * CLI's own `why`. Every one of them is a string a document wrote, drawn
   * through the class the Settings section draws it with. */
  const drawSettings = (edit: SettingsFields): void => {
    safeRow(FIELDS.project, edit.project, 'project', 'settings[].project')
    safeRow(FIELDS.key, edit.key, 'branch', 'settings[].key')
    safeRow(FIELDS.oldValue, edit.oldValue, 'label', 'settings[].old')
    safeRow(FIELDS.newValue, edit.newValue, 'label', 'settings[].new')
    safeRow(FIELDS.layer, edit.layer, 'label', 'settings[].layer')
    if (edit.target !== '') safeRow(FIELDS.target, edit.target, 'path', 'settings[].target')
    safeRow(FIELDS.wins, edit.wins, 'label', 'settings[].wins')
    if (edit.sentence !== '') safeRow(FIELDS.sentence, edit.sentence, 'sentence', 'settings[].sentence')
    if (edit.why !== '') safeRow(FIELDS.why, edit.why, 'sentence', 'settings[].why')
    command(edit.argv)
  }

  /** drawWrite is the button that writes, and it is the same button for the
   * launch, the settings confirmation, the setup and the two Manage writes: it is
   * out of the tab order, it answers only a pointer click, and the tick is the
   * only thing that ever starts it disabled. It carries no answer of its own
   * beyond `yes` and the tick: which argv that yes spawns is the main process's,
   * frozen before this window existed. */
  const drawWrite = (
    subject: SettingsFields | ConfirmFields | SetupFields | ModelsAddFields | ImageAddFields,
  ): void => {
    const confirm = document.createElement('button')
    confirm.setAttribute('type', 'button')
    // Out of the tab order, so no amount of tabbing reaches the button that
    // starts a child or writes a key -- for the settings subject too, where the
    // pointer-only rule is the same one: a click is deliberate and a keystroke
    // is not.
    confirm.tabIndex = -1
    confirm.textContent =
      subject.kind === 'settings'
        ? SAVE_LABEL
        : subject.kind === 'setup'
          ? SETUP_LABEL
          : subject.kind === 'models-add'
            ? ADD_MODEL_LABEL
            : subject.kind === 'image-add'
              ? ADD_ENTRY_LABEL
              : CONFIRM_LABEL
    confirm.disabled = false

    let tick: ConfirmElement | null = null
    let tickLabel: ConfirmElement | null = null
    // The tick is the widening's, and the two subjects that carry one are the
    // settings edit and the models write: both change what every run of the
    // project may name and spend on, and both views say so with their own words
    // (`settingsview.ts`, `modelsview.ts`). The image write is a narrowing and
    // carries no tick at all -- its view has no words for one.
    if ((subject.kind === 'settings' || subject.kind === 'models-add') && subject.confirmNeedsTick) {
      // The tick is a real checkbox beside its own words (D1109), and Confirm is
      // disabled until it is set. It is the console's extra confirmation and not
      // an argv word -- the write's one word is `--no-apply`, and the main
      // process is what decides whether the child's CLI registers it -- and the
      // main process enforces the tick again on the way through
      // (`CONFIRM_TICK_NEEDED`), so a document that failed to disable anything
      // still cannot answer a widening's yes unticked.
      const box = document.createElement('input')
      box.setAttribute('type', 'checkbox')
      box.setAttribute('id', TICK_ID)
      box.checked = false
      box.addEventListener('change', () => {
        confirm.disabled = !box.checked
      })
      const words = document.createElement('label')
      words.setAttribute('for', TICK_ID)
      words.textContent = subject.tick
      tick = box
      tickLabel = words
      confirm.disabled = true
    }

    confirm.addEventListener('click', async (event) => {
      // A keyboard-synthesized click carries `detail === 0` and is not an
      // answer: confirming is a deliberate click, and this is where that is
      // enforced in the document.
      if (event.detail < 1) return
      // A window that drew no tick sends none: the member means "the widening's
      // tick", and a launch -- and a narrowing, whose view carries no tick words
      // either -- has no box for one. The main process reads a missing member as
      // an unticked yes (`settingsConfirmAnswerable`), so a widening whose
      // document somehow failed to draw the box is still refused the write.
      // A missing member is a different message from `ticked: false`, and that
      // is why the member is optional rather than a boolean: "no tick was drawn"
      // and "the box is clear" are two facts.
      const answer: ConfirmAnswer =
        tick === null ? { answer: 'yes' } : { answer: 'yes', ticked: tick.checked === true }
      const reply = await bridge.answer(answer)
      // A yes the main process refused decides nothing and leaves this window
      // open -- the readiness delay is the common one, and it is the main
      // process's because it is measured from the window's own `show` and
      // `focus`. The operator reads the sentence rather than pressing a button
      // that silently did nothing, and the confirming button stays where it is:
      // the click after the delay is the one that counts.
      if (!reply.ok && status !== null) status.textContent = reply.reason
    })

    if (actions !== null) {
      if (tick === null || tickLabel === null) actions.replaceChildren(confirm, cancel)
      else actions.replaceChildren(tick, tickLabel, confirm, cancel)
    }

    // The default button is the view's own decision (D1109: Cancel for a
    // widening, Confirm otherwise), and taking it is a focus rather than a
    // keystroke: the confirming button stays pointer-only either way, and Enter
    // and Escape decline in the main process whatever this document does. The
    // three views that carry the member are the ones that decide it -- the
    // settings edit, the models write and the image write; a launch, a first
    // setup and the entry form carry none, and their own rule is Cancel focused.
    const defaultButton: 'confirm' | 'cancel' = 'defaultButton' in subject ? subject.defaultButton : 'cancel'
    if (defaultButton === 'confirm' && !confirm.disabled) confirm.focus()
    else cancel.focus()
  }

  /** drawSettingsPhase is the settings confirmation: the diff's rows and the
   * button that writes them. It is reached twice and that is the point -- from
   * `fields()` for an edit whose argv main froze before the window opened
   * (C3), and from the entry form's own submit, once the main process has run
   * the dry run and settled this window with its result (SU1, D2902). One
   * drawing, so the diff an operator reads is the same diff either way. */
  const drawSettingsPhase = (subject: SettingsFields): void => {
    if (title !== null) title.textContent = TITLE_SETTINGS
    drawSettings(subject)
    drawWrite(subject)
  }

  /** controlFor builds the one control R5's metadata calls for: a select for a
   * small enum, a checkbox for a bool, a number input with the metadata's own
   * bounds, and a text field for everything else -- including a duration, whose
   * bound is a duration and whose value is therefore typed rather than stepped,
   * and including a key with no metadata at all (the fallback: the raw key and a
   * text control). */
  const controlFor = (entry: SettingsEntryFields): ConfirmElement => {
    if (entry.control === 'toggle') {
      const box = document.createElement('input')
      box.setAttribute('type', 'checkbox')
      box.checked = entry.current === 'true'
      return box
    }
    if (entry.control === 'select') {
      const select = document.createElement('select')
      for (const option of entry.options) {
        const item = document.createElement('option')
        // The value stays the document's own spelling, because it is what the
        // answer carries; only what the operator reads is drawn through the
        // class.
        item.setAttribute('value', option)
        item.textContent = drawn(option, 'label', 'settings[].enum')
        select.appendChild(item)
      }
      // The value a run reads now is the selected one where the enum still
      // carries it; a row whose value left the enum opens on its first member,
      // so the control always holds a value the CLI accepts.
      const first = entry.options[0]
      select.value = entry.options.includes(entry.current) ? entry.current : first ?? ''
      return select
    }
    const input = document.createElement('input')
    input.setAttribute('type', entry.control === 'number' ? 'number' : 'text')
    // The bounds are the metadata's, in the value's own spelling, and they are
    // attributes rather than a second check: the browser's own stepping is a
    // convenience and `settingsEntryError` is what gates the button.
    if (entry.control === 'number') {
      if (entry.min !== '') input.setAttribute('min', entry.min)
      if (entry.max !== '') input.setAttribute('max', entry.max)
    }
    input.value = entry.current
    return input
  }

  /** drawEntryPhase is the form: the label, the key, the description, the value
   * a run reads now, R6's built-in default with the caveat a reset owes, and the
   * control. OK and Reset are the two buttons that **submit** -- they reach the
   * dry run, which writes nothing -- and the window's answer to a submit is the
   * diff, drawn by `drawSettingsPhase` in this same window. */
  const drawEntryPhase = (entry: SettingsEntryFields): void => {
    if (title !== null) title.textContent = TITLE_ENTRY
    // The project first, because it is the identity of the write and the diff
    // draws the same row in the same place: one project, two phases.
    safeRow(FIELDS.project, entry.project, 'project', 'settings[].project')
    safeRow(FIELDS.label, entry.label, 'label', 'settings[].label')
    // The raw key is drawn subtly and in its own face: it is a key, not a label,
    // and the window draws both (D2908).
    code(FIELDS.key, drawn(entry.key, 'branch', 'settings[].key'))
    if (entry.description !== '') {
      safeRow(FIELDS.description, entry.description, 'sentence', 'settings[].description')
    }
    safeRow(FIELDS.current, entry.current, 'label', 'settings[].value')
    // R6: the built-in default is named as such, and it is the value the loaders
    // leave in place or the CLI's own sentence saying what unset means.
    if (entry.defaultValue !== '') safeRow(FIELDS.builtinDefault, entry.defaultValue, 'label', 'settings[].default')
    else if (entry.defaultUnset !== '') safeRow(FIELDS.builtinDefault, entry.defaultUnset, 'sentence', 'settings[].default.unset')
    else row(FIELDS.builtinDefault, NONE)

    if (entry.control === null) {
      // R5: a key this window does not write -- a map entry, a block, a
      // family-owned key, one no layer holds -- is drawn read-only, with the
      // command the read names for it, and the operator copies it out of the
      // window. There is no OK and no reset: this window has nothing to submit.
      if (status !== null) status.textContent = SETTINGS_ENTRY_READONLY
      if (entry.command !== '') code(FIELDS.command, drawn(entry.command, 'label', 'settings[].owner'))
      return
    }

    // R6's second half: a reset is not the end of the story, because the layer
    // this window writes is one of several and a layer it loses to may still
    // decide the key (F8: a reset writes no value at all).
    safeRow(FIELDS.afterReset, entry.caveat, 'sentence', 'settings[].caveat')

    const control = controlFor(entry)
    const problem = document.createElement('strong')
    problem.textContent = ''
    term(FIELDS.newValue)
    const slot = detail('')
    if (slot !== null) {
      slot.textContent = null
      slot.appendChild(control)
      slot.appendChild(problem)
    }

    /** read is what the operator's control holds now, in the spelling the
     * answer carries: a toggle is `true` or `false`, and everything else is the
     * control's own value. */
    const read = (): string => (entry.control === 'toggle' ? (control.checked ? 'true' : 'false') : control.value)

    /** check re-runs R5's validation: the sentence, where there is one, is drawn
     * inline beside the control, and OK is disabled while there is one. */
    const check = (): void => {
      const sentence = settingsEntryError(entry, read())
      problem.textContent = sentence === null ? '' : drawn(sentence, 'sentence', 'settings[].validation')
      ok.disabled = sentence !== null
    }

    const ok = document.createElement('button')
    ok.setAttribute('type', 'button')
    ok.textContent = ENTRY_OK_LABEL

    const reset = document.createElement('button')
    reset.setAttribute('type', 'button')
    reset.textContent = ENTRY_RESET_LABEL

    /** submit sends one answer -- the typed value, or the reset's empty one --
     * and draws what comes back. A refusal (a value the main process's own
     * validation refused, a window too young to answer, a licence that lapsed
     * under it) leaves this form exactly as it is, with the sentence drawn and
     * both buttons live again; an accepted submit is the dry run's, and the
     * window's next subject is the diff. */
    const submit = async (value: string | null): Promise<void> => {
      ok.disabled = true
      reset.disabled = true
      if (status !== null) status.textContent = ENTRY_RUNNING
      const answer = await bridge.answer({ answer: 'submit', value })
      if (!answer.ok) {
        if (status !== null) status.textContent = answer.reason
        reset.disabled = false
        check()
        return
      }
      const settled = await bridge.fields()
      if (!settled.ok) {
        if (status !== null) status.textContent = settled.reason
        reset.disabled = false
        check()
        return
      }
      const next = settled.data as ConfirmSubjectFields
      if (next.kind !== 'settings') {
        if (status !== null) status.textContent = ENTRY_SETTLED_UNEXPECTED
        reset.disabled = false
        check()
        return
      }
      // The diff replaces the form in this same window: the rows are cleared and
      // the same `dl` is drawn again, because the two phases are two shapes of
      // one subject and a second window would be D2902's two dialogs.
      if (status !== null) status.textContent = ''
      if (fields !== null) fields.replaceChildren()
      if (task !== null) task.textContent = ''
      drawSettingsPhase(next)
    }

    ok.addEventListener('click', async () => {
      if (ok.disabled) return
      await submit(read())
    })
    reset.addEventListener('click', async () => {
      if (reset.disabled) return
      await submit(null)
    })
    control.addEventListener('input', () => {
      check()
    })
    control.addEventListener('change', () => {
      check()
    })

    check()
    if (actions !== null) actions.replaceChildren(reset, ok, cancel)
    cancel.focus()
  }

  /** drawSetup is the first setup's set of rows: the answers as main
   * parsed them, the digest of the very bytes the child is given, the two command
   * lines, and the four sentences of what a yes does. Every one of them is the
   * main process's own value or its own sentence, which is why they are drawn as
   * the launch's rows are -- and why the digest is drawn as `code`: it is the one
   * string in the window an operator may want to compare against something else,
   * and the mono face is what makes it legible and selectable. */
  const drawSetup = (setup: SetupFields): void => {
    if (title !== null) title.textContent = TITLE_SETUP
    row(FIELDS.project, setup.project)
    row(FIELDS.region, setup.region)
    // An unanswered name is the CLI's own suggestion rather than an empty line:
    // `NONE` is what this window draws for a field the main process did not fill
    // (`wizardrun.ts` leaves it out of the answers file, and the run names the
    // installation after the working directory's own).
    row(FIELDS.name, setup.name === '' ? NONE : setup.name)
    row(FIELDS.rigor, setup.rigor)
    row(FIELDS.bake, setup.bake)
    row(FIELDS.budget, setup.budget)
    code(FIELDS.digest, setup.digest)
    command(setup.planArgv, FIELDS.planCommand)
    command(setup.applyArgv, FIELDS.applyCommand)
    // The notes are the console's own sentences and they are drawn one to a line
    // under one label, in the order main built them: what the yes runs, what it
    // leaves for the operator, what the yes is, and what is taken again at apply.
    term(FIELDS.setupNotes)
    for (const note of setup.notes) detail(note)
  }

  /** drawModelsAdd is the models write's set of rows (MW1): what the write
   * claims, the entry the route's own list would gain or the pattern that already
   * covers it, the three states the project's allowed-models key can be in, the
   * price entry the document would write when the rates are the operator's own
   * and where its two rates came from, the steps
   * `--no-apply` saves, the plan's digest and the exact argv the yes spawns.
   *
   * Every document string goes through `drawn` under the class the Manage table
   * draws the same kind of value with -- the model and the route are `label`, the
   * config file is a `path`, a price entry's key is a `branch` -- and the two
   * booleans are the console's own words, because a document sent `true` and a
   * window that drew it would be one an operator reads a value out of. The digest
   * and the argv are main's own, drawn as the setup window draws its own two. */
  const drawModelsAdd = (add: ModelsAddFields): void => {
    if (title !== null) title.textContent = TITLE_MODELS_ADD
    safeRow(FIELDS.project, add.project, 'project', 'models_add.project')
    safeRow(FIELDS.model, add.model, 'label', 'models_add.model')
    safeRow(FIELDS.route, add.route, 'label', 'models_add.route')
    safeRow(FIELDS.configFile, add.path, 'path', 'models_add.path')
    safeRow(FIELDS.layer, add.layer, 'label', 'models_add.layer')
    row(FIELDS.changed, add.changed ? YES_WORD : NO_WORD)
    // The document carries one of the two and never both: the entry the route's
    // list would gain, or the pattern that already admits the model.
    if (add.claimedBy !== '') safeRow(FIELDS.coveredBy, add.claimedBy, 'label', 'models_add.claim.covered_by')
    else safeRow(FIELDS.claimEntry, add.claimEntry, 'label', 'models_add.claim.entry')
    // The three states the key can be in, read off the document's own two
    // members: the list gains this entry, the list already names the model, or
    // the key is not set at all and stays absent (D786) -- which is the one the
    // old `NONE` here misread, because a project with no key allows every model
    // a route opens.
    if (add.allowPresent && add.allowEntry !== '') {
      row(
        FIELDS.allowedModels,
        `${ALLOWED_ADDED_WORD} ${drawn(add.allowEntry, 'label', 'models_add.allowed_models.entry')}`,
      )
    } else if (add.allowPresent) row(FIELDS.allowedModels, ALLOWED_NAMES_WORD)
    else row(FIELDS.allowedModels, ALLOWED_ABSENT_WORD)
    // The key is an entry the write lands only for a source of `given`: the
    // catalog and the file's own prices need none, and for those two the
    // document's key says where the rate came from rather than a row to write
    // (contract section 6.33).
    if (add.priceSource === MODELS_PRICE_GIVEN) {
      code(FIELDS.priceKey, drawn(add.priceKey, 'branch', 'models_add.prices.key'))
    }
    row(FIELDS.price, `${priceText(add.priceIn)} / ${priceText(add.priceOut)}`)
    safeRow(FIELDS.priceSource, add.priceSource, 'label', 'models_add.prices.source')
    if (add.steps.length > 0) {
      term(FIELDS.steps)
      for (const step of add.steps) {
        detail(
          `${drawn(step.name, 'label', 'models_add.steps[].name')}: ${drawn(step.state, 'sentence', 'models_add.steps[].state')}`,
        )
      }
    }
    code(FIELDS.digest, add.digest)
    command(add.argv)
    drawWrite(add)
  }

  /** priceText is one rate of the price entry, as the window draws it: the
   * document's own number, spelled by this console because a `dd` holds a string.
   * The number reached here only through the document's reader, which refuses a
   * member that is not a finite number (`modelsview.ts`). */
  const priceText = (rate: number): string => `${String(rate)} USD per million tokens`

  /** drawImageAdd is the image write's set of rows (MW2): the entry the catalog
   * would gain -- its name, its tags, what it is for and the one-line definition
   * `image ls` prints -- whether it is the entry a job naming nothing runs, the
   * steps `--no-apply` saves, the plan's digest and the exact argv the yes
   * spawns. Every document string goes through `drawn` under the class the
   * catalog table draws the same column with.
   *
   * The document's `command` member -- the CLI's own name for the operation --
   * is not a row: main's reader refuses a plan for another command before this
   * window exists (`imagesview.ts`), so the row could only ever draw one
   * constant, and the title above already says which write this is in the
   * console's own words. */
  const drawImageAdd = (add: ImageAddFields): void => {
    if (title !== null) title.textContent = TITLE_IMAGE_ADD
    safeRow(FIELDS.project, add.project, 'project', 'image_add.project')
    safeRow(FIELDS.entry, add.name, 'label', 'image_add.name')
    safeRow(FIELDS.builtFrom, add.source, 'label', 'image_add.source')
    // The tags are a list of document strings, so each is drawn on its own before
    // the console joins them: the join is this window's punctuation and never a
    // value that slipped through unsanitized inside another.
    if (add.tags.length > 0) {
      row(
        FIELDS.tags,
        add.tags.map((tag) => drawn(tag, 'label', 'image_add.tags[]')).join(', '),
      )
    } else row(FIELDS.tags, NONE)
    if (add.description !== '') safeRow(FIELDS.description, add.description, 'sentence', 'image_add.description')
    else row(FIELDS.description, NONE)
    row(FIELDS.defaultEntry, add.entryDefault ? YES_WORD : NO_WORD)
    if (add.steps.length > 0) {
      term(FIELDS.steps)
      for (const step of add.steps) {
        detail(
          `${drawn(step.name, 'label', 'image_add.steps[].name')}: ${step.saved ? SAVED_WORD : UNSAVED_WORD}`,
        )
      }
    }
    code(FIELDS.digest, add.digest)
    command(add.argv)
    drawWrite(add)
  }

  if (subject.kind === 'models-add') {
    if (heading !== null) heading.textContent = ''
    drawModelsAdd(subject)
    return
  }
  if (subject.kind === 'image-add') {
    if (heading !== null) heading.textContent = ''
    drawImageAdd(subject)
    return
  }
  if (subject.kind === 'setup') {
    if (heading !== null) heading.textContent = ''
    drawSetup(subject)
    drawWrite(subject)
    return
  }
  if (subject.kind === 'settings-entry') {
    if (heading !== null) heading.textContent = ''
    drawEntryPhase(subject)
    return
  }
  if (subject.kind === 'settings') {
    if (heading !== null) heading.textContent = ''
    drawSettingsPhase(subject)
    return
  }
  if (title !== null) title.textContent = TITLE_LAUNCH
  if (heading !== null) heading.textContent = TASK_HEADING
  drawLaunch(subject)
  drawWrite(subject)
}

/**
 * The bootstrap is guarded on the document and the bridge being there, for the
 * reason `src/renderer/installations.ts` gives about its own guard: this file is
 * imported by a unit test under plain Node, where there is no `document` and no
 * bridge, and importing it must do nothing at all.
 *
 * The cast is the same one the picker makes: this module declares the slice of
 * the DOM it uses, so it reads `globalThis` through its own interface rather
 * than through `lib.dom`'s.
 */
const scope = globalThis as { readonly document?: unknown; readonly fugaroConfirm?: unknown }

if (scope.document !== undefined && scope.fugaroConfirm !== undefined) {
  void mountConfirm(scope.document as ConfirmDocument, scope.fugaroConfirm as ConfirmBridge)
}
