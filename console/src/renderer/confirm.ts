// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The launch confirmation window's view (design section 5.1 rule 11; console
// V2.5.1a part 2b-2a).
//
// This is the document half of the window `src/main/confirm.ts` opens: the main
// process builds the whole spawn once, freezes it under a run id, and shows the
// operator **that frozen object** before a `run` child exists. The view draws
// it and sends back one answer -- nothing here decides anything, and nothing
// here can reach a record, a path or the CLI.
//
// Rule 11 is the whole of the design of this file, and three of its sentences
// are the structure rather than a promise:
//
//   - **"the window shows these fields and no others, all of them the main
//     process's own values"** -- `fields()` is read once, and every value drawn
//     below is a string, a number or an array of strings out of that answer. The
//     view names no path, no URL and no command of its own;
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
// The keyboard is the one place this view is stricter than it looks. The main
// process already declines Enter and Escape (`before-input-event` in
// `src/main/confirm.ts`), so those two cancel whatever this document does; and
// this document makes the confirming button **unreachable and unactivatable**
// by a keyboard as well: it is out of the tab order (`tabIndex = -1`), Cancel is
// focused on load, and the confirming button's handler answers only a click that
// arrived from a pointer -- a click a keyboard synthesized carries `detail === 0`
// and does nothing. That is "Cancel is the default button" carried through to
// the last input device, and it is why a task box an operator is scrolling with
// Space cannot confirm the run underneath them.
//
// The delay a yes waits out is deliberately **not** here. It belongs to the main
// process (`CONFIRM_READY_DELAY_MS` in `src/main/confirm.ts`), which measures it
// from the window's own `show` and `focus`, because the hazard is a document
// that appears late: a timer in this document would be started by the very thing
// that arrived late. This view reads no clock at all -- it answers the moment the
// button is clicked, and a yes the main process refused comes back as a refusal
// and is drawn as the operator's sentence.

import type { ConfirmBridge } from '../preload/confirm'

/** ConfirmFields is the display-only view this window is shown, mirrored from
 * `ConfirmView` in `src/main/launch.ts` the way the picker's answer types are
 * mirrored: the sandboxed renderer cannot import the main process's module, so
 * this is the shape it is written against, and `test/unit/confirm.test.ts` holds
 * the main process's side of the same fields. */
export interface ConfirmFields {
  /** which of the window's two questions this is (D1115). It is the field the
   * view branches on -- and it is a **tag**, not a hint: the launch's members
   * and the settings view's are drawn by two different sets of rows, and a
   * subject that carried the wrong one would draw the wrong window. */
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
  readonly wins: string
  readonly sentence: string
  readonly widening: boolean
  readonly why: string
  readonly argv: readonly string[]
  readonly defaultButton: 'confirm' | 'cancel'
  readonly tick: string
  readonly confirmNeedsTick: boolean
}

/** ConfirmSubjectFields is what the window is shown: one subject or the other
 * (D1115). One window, two questions, and one document that draws both. */
export type ConfirmSubjectFields = ConfirmFields | SettingsFields

/** CONFIRM_LABEL and CANCEL_LABEL are the two buttons' words. Cancel is the
 * default and the one a keyboard reaches; the confirming button takes a
 * deliberate click. */
export const CONFIRM_LABEL = 'Run it'
export const CANCEL_LABEL = 'Cancel'

/** SAVE_LABEL is the settings window's confirming button, and it is a different
 * word from the launch's on purpose: this window writes one key of one file, and
 * a button that said "Run it" in both windows would be a window an operator could
 * answer without reading which question they were answering. */
export const SAVE_LABEL = 'Write it'

/** NONE is what the window draws for a title or a plan task the launch does not
 * carry. An empty line would be a field an operator cannot tell from one the
 * main process drew nothing for. */
export const NONE = 'none'

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
  oldValue: 'Current value',
  newValue: 'New value',
  layer: 'Written at',
  wins: 'Decides today',
  sentence: 'What this changes',
  why: 'Why the CLI asks',
})

/** TITLE_LAUNCH and TITLE_SETTINGS are the two windows' headings, and
 * TASK_HEADING is the launch's own subtitle. The settings window has none: the
 * `<pre>` below holds the command line for a launch and nothing for an edit, and
 * a heading over an empty box is a field an operator would look for a value in. */
/** TICK_ID is the checkbox's id. It exists so that its `<label for=…>` names it
 * the way any other control is named: the tick's words are a real label pointing
 * at the box, and not a text node beside it that a screen reader would read on
 * its own. */
export const TICK_ID = 'confirm-tick'

export const TITLE_LAUNCH = 'Confirm this run'
export const TITLE_SETTINGS = 'Confirm this settings change'
export const TASK_HEADING = 'The task, as submitted'

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
// A handful of methods and two properties, declared here rather than taken from
// `lib.dom`, for the same reason the picker's view declares its own: the view is
// then drivable under plain Node, where there is no DOM at all, and the
// interface says exactly what the view may touch. There is no markup slot, no
// `href`, no `src` and no `style` in it -- a value of this window is a text node
// or it is not drawn.

/** ConfirmEvent is the slice of an event this view uses. `detail` is the one
 * field it reads, and it is the whole of the difference between a pointer click
 * (1 or more) and one a keyboard synthesized (0). */
export interface ConfirmEvent {
  readonly detail: number
}

/** ConfirmElement is one node. `checked` and `disabled` are the two properties a
 * checkbox and a button carry, and they are the whole of what the tick needs:
 * the tick is a real checkbox the operator can reach, and Confirm is disabled
 * until it is set (D1109). */
export interface ConfirmElement {
  textContent: string | null
  tabIndex: number
  checked: boolean
  disabled: boolean
  addEventListener(type: string, listener: (event: ConfirmEvent) => void | Promise<void>): void
  setAttribute(name: string, value: string): void
  appendChild(child: ConfirmElement): unknown
  replaceChildren(...nodes: ConfirmElement[]): void
  focus(): void
}

/** ConfirmDocument is the document, by id, plus `createElement` for the rows and
 * the two buttons. */
export interface ConfirmDocument {
  getElementById(id: string): ConfirmElement | null
  createElement(tag: string): ConfirmElement
}

/**
 * mountConfirm draws one subject and wires the two answers. It reads `fields()`
 * exactly once -- the view is not re-read, because the frozen launch, or the
 * frozen edit, is what the window shows and re-reading could show a different
 * one -- and it returns when the window is drawn.
 *
 * The order is the point. **Cancel goes in first, before the subject is read**:
 * a `fields()` that never resolves, or that rejects rather than answering, must
 * still leave a focused way out, so the window is never one with nothing to
 * click. Once the fields arrive the confirming button joins it, the confirming
 * button first and Cancel second, Cancel still the focused one; a refusal draws
 * its sentence and no confirming button at all.
 *
 * **One window, two questions** (D1115): the subject's `kind` is what decides
 * which set of rows is drawn, and the two branches share every part that is not
 * a field -- the cancel, the refusal, the pointer-only confirming button and the
 * main process's readiness gate. Nothing here decides anything either window
 * means; it draws what it was handed.
 */
export async function mountConfirm(document: ConfirmDocument, bridge: ConfirmBridge): Promise<void> {
  const status = document.getElementById('confirm-status')
  const fields = document.getElementById('confirm-fields')
  const task = document.getElementById('confirm-task')
  const actions = document.getElementById('confirm-actions')

  /** row appends one labelled value. */
  const row = (label: string, value: string): void => {
    if (fields === null) return
    const term = document.createElement('dt')
    term.textContent = label
    const detail = document.createElement('dd')
    detail.textContent = value
    fields.appendChild(term)
    fields.appendChild(detail)
  }

  /** command appends the argv field by field, one word per row: both windows
   * show what is spawned and not a summary of it. */
  const command = (argv: readonly string[]): void => {
    if (fields === null) return
    const term = document.createElement('dt')
    term.textContent = FIELDS.command
    fields.appendChild(term)
    for (const word of argv) {
      const detail = document.createElement('dd')
      detail.textContent = word
      fields.appendChild(detail)
    }
  }

  const cancel = document.createElement('button')
  cancel.setAttribute('type', 'button')
  // The attribute is set as well as the `focus()` below: the attribute is what
  // a browser focuses on insertion, and the call is what makes it true of a
  // document that was already live when this ran.
  cancel.setAttribute('autofocus', '')
  cancel.textContent = CANCEL_LABEL
  cancel.addEventListener('click', async () => {
    await bridge.answer(false)
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
  // The document's three static words are the launch's, and the settings window
  // is a different question about a different thing: its heading says so, and
  // the task box -- which holds a task for a launch and nothing at all for an
  // edit -- loses the heading that would name a value the window does not draw.
  const title = document.getElementById('confirm-title')
  if (title !== null) title.textContent = subject.kind === 'settings' ? TITLE_SETTINGS : TITLE_LAUNCH
  const heading = document.getElementById('confirm-task-heading')
  if (heading !== null) heading.textContent = subject.kind === 'settings' ? '' : TASK_HEADING

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
  }

  /** drawSettings is the edit's set of rows: the two values, the two layers --
   * the one written and the one that decides today, which is D1113's comparison
   * drawn as two fields rather than as a sentence the window would have to
   * compute -- and, where the view carries them, the shadowed sentence and the
   * CLI's own `why`. */
  const drawSettings = (edit: SettingsFields): void => {
    row(FIELDS.project, edit.project)
    row(FIELDS.key, edit.key)
    row(FIELDS.oldValue, edit.oldValue)
    row(FIELDS.newValue, edit.newValue)
    row(FIELDS.layer, edit.layer)
    row(FIELDS.wins, edit.wins)
    if (edit.sentence !== '') row(FIELDS.sentence, edit.sentence)
    if (edit.why !== '') row(FIELDS.why, edit.why)
    command(edit.argv)
  }

  // The confirming button is built the same way for both questions, and the tick
  // is the only thing that ever starts it disabled. It carries no answer of its
  // own beyond `yes` and the tick: which argv that yes spawns is the main
  // process's, frozen before this window existed.
  const confirm = document.createElement('button')
  confirm.setAttribute('type', 'button')
  // Out of the tab order, so no amount of tabbing reaches the button that starts
  // a child -- for the settings window too, where the pointer-only rule is the
  // same one: a click is deliberate and a keystroke is not.
  confirm.tabIndex = -1
  confirm.textContent = subject.kind === 'settings' ? SAVE_LABEL : CONFIRM_LABEL
  confirm.disabled = false

  let tick: ConfirmElement | null = null
  let tickLabel: ConfirmElement | null = null
  if (subject.kind === 'settings') {
    drawSettings(subject)
    if (subject.confirmNeedsTick) {
      // The tick is a real checkbox beside its own words (D1109), and Confirm is
      // disabled until it is set. It is the console's extra confirmation and not
      // an argv word: `--yes` is the CLI's own answer for a widening, and the
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
  } else {
    drawLaunch(subject)
    if (task !== null) task.textContent = subject.taskText
  }

  confirm.addEventListener('click', async (event) => {
    // A keyboard-synthesized click carries `detail === 0` and is not an
    // answer: confirming is a deliberate click, and this is where that is
    // enforced in the document.
    if (event.detail < 1) return
    // A window that drew no tick sends none: the answer channel's second member
    // is optional and means "the widening's tick", which a launch has no box
    // for.
    const reply = await bridge.answer(true, tick === null ? undefined : tick.checked === true)
    // A yes the main process refused decides nothing and leaves this window open
    // -- the readiness delay is the common one, and it is the main process's
    // because it is measured from the window's own `show` and `focus`. The
    // operator reads the sentence rather than pressing a button that silently
    // did nothing, and the confirming button stays where it is: the click after
    // the delay is the one that counts.
    if (!reply.ok && status !== null) status.textContent = reply.reason
  })

  if (actions !== null) {
    if (tick === null || tickLabel === null) actions.replaceChildren(confirm, cancel)
    else actions.replaceChildren(tick, tickLabel, confirm, cancel)
  }

  // The default button is the view's own decision (D1109: Cancel for a widening,
  // Confirm otherwise), and taking it is a focus rather than a keystroke: the
  // confirming button stays pointer-only either way, and Enter and Escape
  // decline in the main process whatever this document does. A launch keeps its
  // own rule -- Cancel focused -- because its default button is not a member of
  // its view.
  if (subject.kind === 'settings' && subject.defaultButton === 'confirm' && !confirm.disabled) confirm.focus()
  else cancel.focus()

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
