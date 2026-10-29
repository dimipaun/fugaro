// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
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

/** CONFIRM_LABEL and CANCEL_LABEL are the two buttons' words. Cancel is the
 * default and the one a keyboard reaches; the confirming button takes a
 * deliberate click. */
export const CONFIRM_LABEL = 'Run it'
export const CANCEL_LABEL = 'Cancel'

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
})

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

/** ConfirmElement is one node. */
export interface ConfirmElement {
  textContent: string | null
  tabIndex: number
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
 * mountConfirm draws one launch and wires the two answers. It reads `fields()`
 * exactly once -- the view is not re-read, because the frozen launch is what the
 * window shows and re-reading could show a different one -- and it returns when
 * the window is drawn.
 *
 * The order is the point. **Cancel goes in first, before the launch is read**:
 * a `fields()` that never resolves, or that rejects rather than answering, must
 * still leave a focused way out, so the window is never one with nothing to
 * click. Once the fields arrive the confirming button joins it, the confirming
 * button first and Cancel second, Cancel still the focused one; a refusal draws
 * its sentence and no confirming button at all.
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

  // Cancel before the launch is read: a `fields()` that rejects, or that never
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

  const view = reply.data as ConfirmFields
  if (status !== null) status.textContent = ''
  row(FIELDS.installation, view.installation)
  // Both repositories, because the conversion between them is a decision the
  // operator should see rather than infer (rule 11).
  row(FIELDS.recordedRepository, view.recordedRepository)
  row(FIELDS.repository, view.repository)
  row(FIELDS.ref, view.ref)
  row(FIELDS.title, view.title ?? NONE)
  row(FIELDS.planTask, view.planTask ?? NONE)
  row(FIELDS.runId, view.runId)
  // The command line field by field, one word per row: the window shows what is
  // spawned and not a summary of it.
  if (fields !== null) {
    const term = document.createElement('dt')
    term.textContent = FIELDS.command
    fields.appendChild(term)
    for (const word of view.argv) {
      const detail = document.createElement('dd')
      detail.textContent = word
      fields.appendChild(detail)
    }
  }
  row(FIELDS.taskSize, taskSizeText(view.taskByteLength))
  if (task !== null) task.textContent = view.taskText

  const confirm = document.createElement('button')
  confirm.setAttribute('type', 'button')
  // Out of the tab order, so no amount of tabbing reaches the button that
  // spawns a child.
  confirm.tabIndex = -1
  confirm.textContent = CONFIRM_LABEL
  confirm.addEventListener('click', async (event) => {
    // A keyboard-synthesized click carries `detail === 0` and is not an
    // answer: confirming is a deliberate click, and this is where that is
    // enforced in the document.
    if (event.detail < 1) return
    const reply = await bridge.answer(true)
    // A yes the main process refused decides nothing and leaves this window open
    // -- the readiness delay is the common one, and it is the main process's
    // because it is measured from the window's own `show` and `focus`. The
    // operator reads the sentence rather than pressing a button that silently
    // did nothing, and the confirming button stays where it is: the click after
    // the delay is the one that counts.
    if (!reply.ok && status !== null) status.textContent = reply.reason
  })

  if (actions !== null) actions.replaceChildren(confirm, cancel)
  cancel.focus()
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
