// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The launch confirmation window's document and its view (design section 5.1
// rule 11; console V2.5.1a part 2b-2a).
//
// The view is what the operator actually reads before a `run` child is spawned,
// and this is the only test that runs it. Like the picker's, it runs under
// **plain Node with no DOM library**: `mountConfirm` declares the methods and
// properties it uses, so the document below is a fake that implements exactly
// those, and the console gains no jsdom dependency for one window of one
// question.
//
// Each test names the mutation it catches:
//
//   - a view that drew a field as **markup** fails the headline case, whose
//     fake element has no markup slot at all: setting one is recorded and
//     throws, so a task text of `<img src=x onerror=…>` is a value the window
//     would have executed rather than read;
//   - a view that made **Confirm keyboard-reachable** fails "the confirming
//     button is not in the tab order" (`tabIndex` is -1) and "a keyboard
//     click does not confirm" (a click with `detail: 0` calls `answer` zero
//     times), which is what "Cancel is the default button" has to mean once
//     Enter and Escape are already declined in the main process; and a view that
//     **drew Cancel first** fails the same test's draw-order assertion, which is
//     what the stylesheet's comment above the two buttons describes;
//   - a view that **cut or summarised** the task fails "draws the whole task
//     text", which compares a 16 KiB text's length rather than its presence;
//   - a view that **hid the argv** behind one line fails "draws the argv one
//     word per row";
//   - a view that drew a **null title or plan task as the empty string** fails
//     "draws `none` for a title and a plan task the launch does not carry";
//   - a view that drew the byte size as the **displayed** text's size fails
//     "labels the size as the task as submitted", because `taskText` has its
//     format characters removed and its length is not the size that was sent;
//   - a view that showed the confirming button **over a refusal** fails "draws
//     the refusal's own sentence and no confirming button".

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import {
  CANCEL_LABEL,
  CONFIRM_LABEL,
  CONFIRM_UNDRAWABLE,
  ENTRY_OK_LABEL,
  ENTRY_RESET_LABEL,
  ENTRY_RUNNING,
  NONE,
  SAVE_LABEL,
  TASK_HEADING,
  TICK_ID,
  TITLE_ENTRY,
  TITLE_LAUNCH,
  TITLE_SETTINGS,
  drawn,
  mountConfirm,
  taskSizeText,
} from '../../src/renderer/confirm'
import type {
  ConfirmDocument,
  ConfirmElement,
  ConfirmEvent,
  ConfirmFields,
  SettingsEntryFields,
  SettingsFields,
} from '../../src/renderer/confirm'
import {
  SETTINGS_ENTRY_ABOVE_MAX,
  SETTINGS_ENTRY_CAVEAT,
  SETTINGS_ENTRY_READONLY,
} from '../../src/shared/settingsentry'
import { SETTINGS_SHADOWED_SENTENCE, SETTINGS_TICK_LABEL } from '../../src/main/settingsview'
import type { ConfirmAnswer, ConfirmBridge, ConfirmReply } from '../../src/preload/confirm'

// ---------------------------------------------------------------------------
// A document, faked from the real one
// ---------------------------------------------------------------------------

/** FakeElement is one node: text, the four properties the view touches, a child
 * list, and the listeners a test can fire. It has **no markup slot**: a view
 * that reached for one is recorded here and throws, which is the whole point of
 * the headline case below. */
class FakeElement implements ConfirmElement {
  textContent: string | null = null
  tabIndex = 0
  /** checked and disabled are the checkbox's and the button's own properties:
   * the tick's box and the button it gates are the two elements the view reads
   * and writes them on. */
  checked = false
  disabled = false
  /** value is what a control holds -- a text or number field's text, a select's
   * selected value, a toggle's `true`/`false` read through `checked` -- and the
   * entry form reads it back off the control rather than keeping a copy. */
  value = ''
  readonly attributes = new Map<string, string>()
  readonly children: FakeElement[] = []
  /** focused is whether `focus()` was called on this element. */
  focused = false
  /** failures records every attempt to write this element as markup. */
  readonly failures: string[] = []
  private readonly listeners = new Map<string, ((event: ConfirmEvent) => void | Promise<void>)[]>()

  constructor(readonly tag: string) {}

  set innerHTML(_markup: string) {
    this.failures.push('innerHTML')
    throw new Error('a value of the confirmation window is a text node: this element has no markup slot')
  }

  addEventListener(type: string, listener: (event: ConfirmEvent) => void | Promise<void>): void {
    const held = this.listeners.get(type) ?? []
    held.push(listener)
    this.listeners.set(type, held)
  }

  setAttribute(name: string, value: string): void {
    this.attributes.set(name, value)
  }

  appendChild(child: ConfirmElement): unknown {
    this.children.push(child as FakeElement)
    return child
  }

  replaceChildren(...nodes: ConfirmElement[]): void {
    this.children.length = 0
    for (const node of nodes) this.children.push(node as FakeElement)
  }

  focus(): void {
    this.focused = true
  }

  /** fire runs one event with the given `detail`, awaited: every handler in the
   * view is async, and a test that did not await would assert on a window
   * mid-flight. `detail` is the argument a click arrives with -- 1 for a
   * pointer, 0 for one a keyboard synthesized -- and it is the whole of what
   * separates the two paths. */
  async fire(type: string, detail = 0): Promise<ConfirmEvent> {
    const event: ConfirmEvent = { detail }
    for (const listener of this.listeners.get(type) ?? []) await listener(event)
    return event
  }

  /** button finds a child button by its label. */
  button(label: string): FakeElement {
    const found = this.children.find((child) => child.tag === 'button' && child.textContent === label)
    if (found === undefined) {
      throw new Error(
        `no button labelled ${JSON.stringify(label)} under <${this.tag}>; has ${JSON.stringify(this.children.map((child) => child.textContent))}`,
      )
    }
    return found
  }
}

class FakeDocument implements ConfirmDocument {
  readonly byId = new Map<string, FakeElement>()
  /** created records every tag `createElement` was asked for, so a test can
   * assert the view built no element it must not. */
  readonly created: string[] = []
  createElement(tag: string): ConfirmElement {
    this.created.push(tag)
    return new FakeElement(tag)
  }
  getElementById(id: string): ConfirmElement | null {
    return this.byId.get(id) ?? null
  }
}

const documentPath = fileURLToPath(new URL('../../src/renderer/confirm.html', import.meta.url))
const html = readFileSync(documentPath, 'utf8')

/** markup is the document with its comments removed. Every structural claim
 * below is made over this: a comment is not an element, and this document's own
 * comments name the tags they promise are absent. */
function markup(document: string): string {
  return document.replace(/<!--[\s\S]*?-->/g, '')
}

/** fakeDocument builds the document's elements from the ids the file declares,
 * so the seam between the document and the view is held: an id the view looked
 * up and the file did not carry would be `null` here too. */
function fakeDocument(): FakeDocument {
  const document = new FakeDocument()
  for (const match of html.matchAll(/id="([^"]+)"/g)) {
    document.byId.set(match[1] ?? '', new FakeElement('div'))
  }
  return document
}

/** IDS are the containers the view looks up by name. */
const IDS = ['confirm-status', 'confirm-fields', 'confirm-task', 'confirm-actions'] as const

// ---------------------------------------------------------------------------
// A bridge, faked on the wire
// ---------------------------------------------------------------------------

/** HOSTILE is a task text and a set of field values that are all markup: a
 * window that drew any of them as HTML would build an element and run a
 * handler, and every one of them is a value an operator or a repository can
 * put in front of this window. */
const HOSTILE_TASK = '<img src=x onerror=alert(1)> javascript:alert(1)'
const HOSTILE = {
  installation: 'owner/repo-a',
  recordedRepository: '<b>https://example.invalid/owner/repo-a</b>',
  repository: 'owner/repo-a',
  ref: '<b>main</b>',
  title: '<b>the title</b>',
}

/** aView is one display-only view shaped like `ConfirmView` in the main
 * process, with a test's own values over it. */
function aView(overrides: Partial<ConfirmFields> = {}): ConfirmFields {
  return {
    kind: 'launch',
    runId: 'run-0000000000000001',
    installation: HOSTILE.installation,
    recordedRepository: HOSTILE.recordedRepository,
    repository: HOSTILE.repository,
    ref: HOSTILE.ref,
    title: HOSTILE.title,
    planTask: null,
    argv: ['/usr/local/bin/fugaro', 'run', '--task-file', '-', '--json'],
    taskByteLength: 4_096,
    taskText: HOSTILE_TASK,
    ...overrides,
  }
}

/** SETTINGS_ARGV is the exact command line the confirmed write spawns, in the
 * order `argvFor` builds it (`settings set KEY --layer L [--repo DIRECTORY]
 * <phase> --project P -- VALUE`): the window shows it rather than a summary of
 * it. */
const SETTINGS_ARGV: readonly string[] = Object.freeze([
  '/usr/local/bin/fugaro',
  'settings',
  'set',
  'budget.cap_usd',
  '--layer',
  'project',
  '--project',
  'sandbox',
  '--',
  '30',
])

/** aSettingsView is the window's **second** subject (D1115): the settings half
 * of the union, shaped like `SettingsConfirmView` in the main process, with a
 * test's own values over it. */
function aSettingsView(overrides: Partial<SettingsFields> = {}): SettingsFields {
  return {
    kind: 'settings',
    project: 'sandbox',
    key: 'budget.cap_usd',
    oldValue: '25',
    newValue: '30',
    layer: 'project',
    target: '',
    wins: 'project',
    sentence: '',
    widening: false,
    why: '',
    argv: SETTINGS_ARGV,
    defaultButton: 'confirm',
    tick: '',
    confirmNeedsTick: false,
    ...overrides,
  }
}

/** fakeBridge is the confirmation window's whole surface, recording what it was
 * asked. The answer it records is the **object** the window sends (SU1): the
 * three answers of the one channel, so a test reads back exactly what the main
 * process would have received rather than a boolean this file interpreted. */
function fakeBridge(
  options: {
    readonly fields?: ConfirmReply
    readonly fieldsReplies?: readonly ConfirmReply[]
    readonly fieldsError?: Error
    readonly answerReply?: ConfirmReply
    /** onAnswer runs while `answer` is being asked, which is the only moment a
     * test can read the window as the operator saw it during the call: the entry
     * form's status line is overwritten by the answer it is waiting for. */
    readonly onAnswer?: () => void
  } = {},
): {
  readonly bridge: ConfirmBridge
  readonly answers: readonly ConfirmAnswer[]
  readonly calls: string[]
} {
  const answers: ConfirmAnswer[] = []
  const calls: string[] = []
  let reads = 0
  const bridge: ConfirmBridge = {
    fields: async () => {
      calls.push('fields')
      if (options.fieldsError !== undefined) throw options.fieldsError
      const queued = options.fieldsReplies?.[reads]
      reads += 1
      return queued ?? options.fields ?? { ok: true, data: aView() }
    },
    answer: async (answer: ConfirmAnswer) => {
      calls.push('answer')
      answers.push(answer)
      options.onAnswer?.()
      return options.answerReply ?? { ok: true, data: { answered: true } }
    },
  }
  return { bridge, answers, calls }
}

/** mount builds the view over a fresh fake document, and returns the pieces a
 * test needs. Every test starts here. */
async function mount(options: Parameters<typeof fakeBridge>[0] = {}) {
  const document = fakeDocument()
  const { bridge, answers, calls } = fakeBridge(options)
  await mountConfirm(document, bridge)
  return {
    document,
    answers,
    calls,
    element: (id: string): FakeElement => document.byId.get(id) as FakeElement,
  }
}

/** fieldValue is the `<dd>` a `<dt>` labels, or `null` when there is none. A row
 * whose value is not a bare text node -- the key and the command are a `<code>`
 * inside the cell, and the form's control row holds the control and the inline
 * error -- answers with its children's text, so an assertion reads what the
 * operator reads rather than the cell's own text node. */
function fieldValue(document: FakeDocument, label: string): string | null {
  const fields = document.byId.get('confirm-fields') as FakeElement
  const index = fields.children.findIndex((child) => child.tag === 'dt' && child.textContent === label)
  if (index === -1) return null
  const cell = fields.children[index + 1]
  if (cell === undefined) return null
  return cell.textContent ?? cell.children.map((child) => child.textContent ?? '').join('')
}

/** commandWords are the `<dd>`s under the command's `<dt>`, one word each. */
function commandWords(document: FakeDocument): readonly (string | null)[] {
  const fields = document.byId.get('confirm-fields') as FakeElement
  const start = fields.children.findIndex((child) => child.tag === 'dt' && child.textContent === 'Command')
  const words: (string | null)[] = []
  for (let index = start + 1; index < fields.children.length; index += 1) {
    const child = fields.children[index]
    if (child === undefined || child.tag !== 'dd') break
    words.push(child.textContent)
  }
  return words
}

// ---------------------------------------------------------------------------

describe('the confirmation document and its view', () => {
  it('declares every container the view looks up, and the view finds them', () => {
    // Non-vacuity, and the seam: the fake document is built from the file, so
    // this asserts the file carries the ids `mountConfirm` needs.
    const document = fakeDocument()
    for (const id of IDS) expect(document.getElementById(id), `confirm.html must declare #${id}`).not.toBeNull()
    expect(html).toContain('<script src="./confirm.js" defer>')
  })

  it('begins with the licence notice, and holds no markup, no link and no image', () => {
    // The two comment lines come before the doctype, exactly as the picker's
    // document's do: the notice is the first thing in the file.
    const lines = html.split('\n')
    expect(lines[0]).toBe('<!-- SPDX-License-Identifier: LicenseRef-Fugaro-Subscription -->')
    expect(lines[1]).toBe('<!-- See console/LICENSE.md for the terms. -->')
    expect(lines[2]).toBe('<!doctype html>')
    // The document draws values this process does not own, and rule 11 says it
    // draws no HTML, no link and no image from any of them: the file itself
    // carries none of the elements that could.
    const text = markup(html)
    for (const forbidden of ['<a', '<img', '<iframe', '<form', '<input', '<style']) {
      expect(text, `confirm.html must not carry ${forbidden}`).not.toContain(forbidden)
    }
    // No inline script and no inline handler: the CSP has no `'unsafe-inline'`,
    // so an inline script would not run even if one were written.
    for (const script of text.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/gi)) {
      expect((script[1] ?? '').trim(), 'confirm.html holds inline script text').toBe('')
    }
    expect(text, 'confirm.html holds an on* handler attribute').not.toMatch(/\son[a-z]+\s*=/i)
  })

  it('draws every value as a literal text node, through an element with no markup slot', async () => {
    // The headline: the values of this window are untrusted-by-default (a
    // repository's title, a task text), and rule 11's answer is that each is a
    // **text node**. A view that wrote any of them as markup would have the
    // window parse a handler out of the task it is showing.
    const { document, element } = await mount()
    expect(element('confirm-task').textContent).toBe(HOSTILE_TASK)
    expect(fieldValue(document, 'Title')).toBe(HOSTILE.title)
    expect(fieldValue(document, 'Ref')).toBe(HOSTILE.ref)
    expect(fieldValue(document, 'Recorded repository')).toBe(HOSTILE.recordedRepository)
    // ...and no element of a markup kind was ever built: the view created the
    // two buttons and the field rows and nothing else.
    expect(document.created.filter((tag) => tag === 'img' || tag === 'a' || tag === 'b')).toEqual([])
    expect([...new Set(document.created)].sort()).toEqual(['button', 'dd', 'dt'])

    // The fake's contract, made exact: the element the view writes through has
    // no markup slot at all, so a write to one is recorded and refused -- a view
    // that reached for `innerHTML` would fail here rather than draw markup.
    const victim = element('confirm-task')
    expect(() => {
      ;(victim as unknown as { innerHTML: string }).innerHTML = HOSTILE_TASK
    }).toThrow(/no markup slot/)
    expect(victim.failures).toEqual(['innerHTML'])
    // Restored the way the view writes it, so the value the window shows is the
    // value the launch carries.
    victim.textContent = HOSTILE_TASK
    expect(victim.textContent).toBe(HOSTILE_TASK)
  })

  it('focuses Cancel on load, and the confirming button is not in the tab order', async () => {
    // "Cancel is the default button" (rule 11): the main process already
    // declines Enter and Escape, and this is the other half -- a keyboard
    // cannot reach the button that spawns a child at all.
    const { element } = await mount()
    const actions = element('confirm-actions')
    expect(actions.button(CANCEL_LABEL).focused).toBe(true)
    expect(actions.button(CANCEL_LABEL).attributes.get('autofocus')).toBe('')
    expect(actions.button(CONFIRM_LABEL).tabIndex).toBe(-1)
    expect(actions.button(CONFIRM_LABEL).focused).toBe(false)
    // The draw order, pinned because the stylesheet's comment above the two
    // buttons describes it: the confirming button is drawn first and Cancel
    // second, and Cancel is the default by being the one focused rather than by
    // being drawn first. A view that swapped them would leave that comment
    // describing a document this test does not run.
    expect(actions.children.map((child) => child.textContent)).toEqual([CONFIRM_LABEL, CANCEL_LABEL])
  })

  it('confirms only on a pointer click, and cancels on any click', async () => {
    // The decision this part makes (2b-1's open question): a keyboard-synthesized
    // click arrives with `detail: 0`, so Space or Enter on a focused button --
    // were one reachable -- does nothing. A pointer click is `detail >= 1`.
    const keyboard = await mount()
    const keyboardActions = keyboard.element('confirm-actions')
    await keyboardActions.button(CONFIRM_LABEL).fire('click', 0)
    expect(keyboard.answers).toEqual([])
    expect(keyboard.calls).toEqual(['fields'])

    const pointer = await mount()
    const pointerActions = pointer.element('confirm-actions')
    await pointerActions.button(CONFIRM_LABEL).fire('click', 1)
    expect(pointer.answers).toEqual([{ answer: 'yes' }])
    expect(pointer.calls).toEqual(['fields', 'answer'])

    // Cancel answers on any click at all: it is the default, and the button a
    // keyboard reaches.
    const cancelled = await mount()
    await cancelled.element('confirm-actions').button(CANCEL_LABEL).fire('click', 0)
    expect(cancelled.answers).toEqual([{ answer: 'no' }])
  })

  it('draws the whole task text, never cut and never summarised', async () => {
    // Rule 11: "the whole task text, displayed in full and scrollable -- the
    // operator reads it rather than the app summarising it". A view that
    // truncated it (or passed it through the display sanitizer, which folds a
    // long string) would fail the length comparison, not merely a `toContain`.
    const task = 'x'.repeat(16 * 1024)
    const { element } = await mount({ fields: { ok: true, data: aView({ taskText: task }) } })
    const box = element('confirm-task')
    expect(box.textContent?.length).toBe(task.length)
    expect(box.textContent).toBe(task)
  })

  it('draws the argv one word per row, and the exact command line', async () => {
    const argv = ['/usr/local/bin/fugaro', 'run', '--task-file', '-', '--json', '--repo', 'owner/repo-a']
    const { document } = await mount({ fields: { ok: true, data: aView({ argv }) } })
    expect(commandWords(document)).toEqual(argv)
  })

  it('draws `none` for a title and a plan task the launch does not carry', async () => {
    const { document } = await mount({
      fields: { ok: true, data: aView({ title: null, planTask: null }) },
    })
    expect(fieldValue(document, 'Title')).toBe(NONE)
    expect(fieldValue(document, 'Plan task')).toBe(NONE)
    expect(NONE).toBe('none')

    const named = await mount({
      fields: { ok: true, data: aView({ title: 'the title', planTask: 'Task 3.4b' }) },
    })
    expect(fieldValue(named.document, 'Plan task')).toBe('Task 3.4b')
  })

  it('labels the size as the task as submitted', async () => {
    // `taskText` has its format characters removed (`displayTaskText`), so its
    // length is not the size that was sent: the label says which size the
    // number is, because a window that showed the wrong one would be a window
    // an operator budgets from.
    const { document } = await mount({ fields: { ok: true, data: aView({ taskByteLength: 4_096 }) } })
    const size = fieldValue(document, 'Task size')
    expect(size).toBe(taskSizeText(4_096))
    expect(size).toContain('4096 bytes as submitted')
    expect(size).toContain('format characters are not shown')
  })

  it('draws the refusal’s own sentence and no confirming button', async () => {
    // A `fields()` that refused is not a window that may spawn: it shows why,
    // and the only control left is the one that declines.
    const { element, answers, calls } = await mount({
      fields: { ok: false, reason: 'the confirmation window was opened for a launch this process no longer holds' },
    })
    expect(element('confirm-status').textContent).toBe(
      'the confirmation window was opened for a launch this process no longer holds',
    )
    const actions = element('confirm-actions')
    expect(actions.children.map((child) => child.textContent)).toEqual([CANCEL_LABEL])
    expect(actions.children.some((child) => child.textContent === CONFIRM_LABEL)).toBe(false)
    await actions.button(CANCEL_LABEL).fire('click', 0)
    expect(answers).toEqual([{ answer: 'no' }])
    expect(calls).toEqual(['fields', 'answer'])
  })

  it('draws a refused answer’s own sentence, and leaves the window open on it', async () => {
    // The main process refuses a yes that arrives before the window has settled
    // (`CONFIRM_READY_DELAY_MS`): that click is not an answer and spawns nothing,
    // so the window shows the sentence rather than a button that appears to do
    // nothing. The confirming button is still there -- a refused answer is not a
    // decision, and the click that comes after the delay is the one that counts.
    const refused = await mount({
      fields: { ok: true, data: aView() },
      answerReply: { ok: false, reason: 'the confirmation window is not ready for an answer yet' },
    })
    const actions = refused.element('confirm-actions')
    await actions.button(CONFIRM_LABEL).fire('click', 1)
    expect(refused.answers).toEqual([{ answer: 'yes' }])
    expect(refused.element('confirm-status').textContent).toBe(
      'the confirmation window is not ready for an answer yet',
    )
    expect(actions.button(CONFIRM_LABEL).tabIndex).toBe(-1)

    // An accepted answer draws nothing: the window's life is over and its own
    // close is the main process's.
    const accepted = await mount()
    await accepted.element('confirm-actions').button(CONFIRM_LABEL).fire('click', 1)
    expect(accepted.element('confirm-status').textContent).toBe('')
  })

  it('holds no clock of its own: the delay is the main process’s', () => {
    // The delay is measured where the events are, and the events are the main
    // process's: a timer in this document would be started by the document whose
    // late arrival is the whole hazard, and the operator's click decides nothing
    // until the main process says so.
    const source = readFileSync(fileURLToPath(new URL('../../src/renderer/confirm.ts', import.meta.url)), 'utf8')
    for (const clock of ['setTimeout', 'setInterval', 'requestAnimationFrame', 'performance.now', 'Date.now']) {
      expect(source, `the view reached for ${clock}`).not.toContain(clock)
    }
  })

  it('leaves a focused Cancel in place when reading the launch throws', async () => {
    // Cancel is drawn and focused *before* the launch is read, so a `fields()`
    // that rejects -- not the refusal above, which answers -- leaves the window
    // with the one control that declines rather than a window with nothing on it.
    // Escape, Enter and the close box already decline in the main process; this
    // is the document's own half of "there is always a way out".
    const document = fakeDocument()
    const { bridge } = fakeBridge({ fieldsError: new Error('the read threw') })
    await expect(mountConfirm(document, bridge)).rejects.toThrow('the read threw')

    const actions = document.byId.get('confirm-actions') as FakeElement
    expect(actions.children.map((child) => child.textContent)).toEqual([CANCEL_LABEL])
    expect(actions.button(CANCEL_LABEL).focused).toBe(true)
    expect(actions.children.some((child) => child.textContent === CONFIRM_LABEL)).toBe(false)
  })

  it('wraps a long field value rather than widening the window', async () => {
    // A repository title, a ref or a path can be thousands of characters with no
    // space to break at, and the dashboard's grid puts `main` in the `auto`
    // column: an unbroken value widens it and scrolls the page sideways. The
    // `dl` carries the hook the stylesheet's `overflow-wrap: anywhere` rule
    // selects.
    const screens = readFileSync(
      fileURLToPath(new URL('../../src/renderer/theme/screens.css', import.meta.url)),
      'utf8',
    )
    const rule = /\.console-confirm-fields\s+dd\s*\{([^}]*)\}/.exec(screens)
    expect(rule, 'screens.css carries no rule wrapping the field values').not.toBeNull()
    expect(rule?.[1]).toMatch(/overflow-wrap:\s*anywhere/)
    expect(markup(html)).toMatch(/<dl id="confirm-fields" class="console-confirm-fields">/)

    // The value reaches its `dd` whole -- the rule wraps it, the view does not
    // cut it. (The fake DOM cannot lay out, so the wrap itself is the
    // stylesheet's; the visual result is V2.5.1c's e2e.)
    const ref = 'x'.repeat(4_096)
    const { document } = await mount({ fields: { ok: true, data: aView({ ref }) } })
    expect(fieldValue(document, 'Ref')).toBe(ref)
  })

  it('gives the task box a rule that scrolls it, and a bound', () => {
    // "displayed in full and scrollable": the text is never cut in the DOM, so
    // the scrolling is the stylesheet's, and the box is bounded so a 16 KiB
    // task does not push the buttons off the window.
    const screens = readFileSync(
      fileURLToPath(new URL('../../src/renderer/theme/screens.css', import.meta.url)),
      'utf8',
    )
    const rule = /\.console-confirm-task\s*\{([^}]*)\}/.exec(screens)
    expect(rule, 'screens.css carries no rule for the task box').not.toBeNull()
    expect(rule?.[1]).toMatch(/overflow:\s*auto/)
    expect(rule?.[1]).toMatch(/max-height:\s*\S+/)
    // `pre-wrap` with `anywhere` is the other half of "bounded": this document's
    // `main` sits in the body grid's `auto` column, whose base size is the item's
    // min-content contribution, and a `<pre>` at its default `white-space: pre`
    // makes that contribution the longest line -- so one unbroken line would widen
    // the column and scroll the page sideways. `pre-wrap` allows a wrap and
    // `anywhere` lets an unbroken value break.
    expect(rule?.[1]).toMatch(/white-space:\s*pre-wrap/)
    expect(rule?.[1]).toMatch(/overflow-wrap:\s*anywhere/)
    // The class is on the box the document declares, so the rule is not styling
    // nothing.
    expect(html).toContain('class="console-confirm-task"')
  })
})

// ---------------------------------------------------------------------------
// The same document's second question: the settings edit (D1108, D1109, D1115;
// rule 12). One window, two questions, and the tag is what decides which set of
// rows is drawn -- so the launch cases above and these are the two halves of the
// same view, and a view that drew one question's rows for the other's subject
// fails here.
// ---------------------------------------------------------------------------

describe('the settings question the same document draws', () => {
  it('draws the edit’s own rows, its own heading, and no task at all', async () => {
    const { document, element } = await mount({
      fields: { ok: true, data: aSettingsView({ sentence: SETTINGS_SHADOWED_SENTENCE, why: 'this raises the cap' }) },
    })

    expect(element('confirm-title').textContent).toBe(TITLE_SETTINGS)
    expect(TITLE_SETTINGS).not.toBe(TITLE_LAUNCH)
    // The launch's task heading names a box this question does not fill, so it
    // loses its words rather than standing over an empty box.
    expect(element('confirm-task-heading').textContent).toBe('')
    expect(TASK_HEADING).not.toBe('')
    expect(element('confirm-task').textContent).toBeNull()

    expect(fieldValue(document, 'Project')).toBe('sandbox')
    expect(fieldValue(document, 'Setting')).toBe('budget.cap_usd')
    expect(fieldValue(document, 'Current value')).toBe('25')
    expect(fieldValue(document, 'New value')).toBe('30')
    // The two layers D1113 turns on: the one written and the one that decides
    // today. The comparison is the view's sentence below, never this document's
    // reading of the two fields.
    expect(fieldValue(document, 'Written at')).toBe('project')
    expect(fieldValue(document, 'Decides today')).toBe('project')
    expect(fieldValue(document, 'What this changes')).toBe(SETTINGS_SHADOWED_SENTENCE)
    expect(fieldValue(document, 'Why the CLI asks')).toBe('this raises the cap')
    // None of the launch's own rows: no run id, no title, no plan task, and no
    // task size, because this question has none of them.
    for (const absent of ['Run id', 'Title', 'Plan task', 'Task size', 'Ref', 'Installation']) {
      expect(fieldValue(document, absent), `the settings window drew ${absent}`).toBeNull()
    }
    expect(commandWords(document)).toEqual(SETTINGS_ARGV)

    // The launch's own mount is unchanged: the union added a branch and took
    // nothing from the question it already had.
    const launch = await mount()
    expect(launch.element('confirm-title').textContent).toBe(TITLE_LAUNCH)
    expect(launch.element('confirm-task-heading').textContent).toBe(TASK_HEADING)
    expect(fieldValue(launch.document, 'Run id')).toBe(aView().runId)
  })

  it('draws the target row of a `repo`-layer edit, and nothing of it for another layer (RC80, L12)', async () => {
    // The layer names the document's *kind*; the target names the file. It is
    // drawn from the view's `target`, which `settingsview.ts` reads out of the
    // very argv this window shows and the source is handed, so the line and the
    // spawn cannot name two files.
    const repo = await mount({
      fields: {
        ok: true,
        data: aSettingsView({
          layer: 'repo',
          target: 'writes fugaro.yaml in /Users/owner/src/checkout',
          argv: [
            '/usr/local/bin/fugaro',
            'settings',
            'set',
            'budget.cap_usd',
            '--layer',
            'repo',
            '--repo',
            '/Users/owner/src/checkout',
            '--project',
            'sandbox',
            '--',
            '30',
          ],
        }),
      },
    })
    expect(fieldValue(repo.document, 'Written at')).toBe('repo')
    expect(fieldValue(repo.document, 'Target')).toBe('writes fugaro.yaml in /Users/owner/src/checkout')

    const bare = await mount({ fields: { ok: true, data: aSettingsView() } })
    expect(fieldValue(bare.document, 'Target')).toBeNull()
  })

  it('draws the shadowed sentence and the CLI’s why only where the view carries them', async () => {
    const bare = await mount({ fields: { ok: true, data: aSettingsView() } })
    expect(fieldValue(bare.document, 'What this changes')).toBeNull()
    expect(fieldValue(bare.document, 'Why the CLI asks')).toBeNull()

    const shadowed = await mount({
      fields: { ok: true, data: aSettingsView({ sentence: SETTINGS_SHADOWED_SENTENCE }) },
    })
    expect(fieldValue(shadowed.document, 'What this changes')).toBe(SETTINGS_SHADOWED_SENTENCE)
    expect(fieldValue(shadowed.document, 'Why the CLI asks')).toBeNull()
  })

  it('draws every settings value as a literal text node, through its own class', async () => {
    // A key, an old value and the CLI's own sentence are text the CLI printed,
    // and an old value is whatever the operator wrote last time: each is drawn as
    // a text node through the class the Settings section draws the same value
    // with (`projects.ts`'s `settingsRowLine`: a key is `branch`, a value is
    // `label`, a sentence is `sentence`), so no element is ever built out of one.
    const { document } = await mount({
      fields: {
        ok: true,
        data: aSettingsView({
          key: 'budget.cap_usd',
          oldValue: '<b>25</b>',
          newValue: 'javascript:alert(1)',
          sentence: '<script>alert(1)</script>',
        }),
      },
    })
    expect(fieldValue(document, 'Setting')).toBe('budget.cap_usd')
    expect(fieldValue(document, 'Current value')).toBe('<b>25</b>')
    expect(fieldValue(document, 'What this changes')).toBe('<script>alert(1)</script>')
    expect(document.created.filter((tag) => ['img', 'b', 'script', 'a'].includes(tag))).toEqual([])
    // This edit is a narrowing, so it draws no tick: the elements are the two
    // buttons and the field rows, exactly as the launch's are.
    expect([...new Set(document.created)].sort()).toEqual(['button', 'dd', 'dt'])
  })

  it('draws the console’s own sentence for a value its class refuses, never the value', async () => {
    // The other half of the rule above, and the mutation it catches: a key that
    // is not what `branch` admits is **refused**, and a refusal is drawn as this
    // view's own sentence rather than as the value (`drawn`, the same choice
    // `installations.ts`'s `UNDRAWABLE` makes). A window that fell back to the raw
    // string on a refusal would draw a value the sanitizer declined to vouch for.
    const { document } = await mount({
      fields: {
        ok: true,
        data: aSettingsView({ project: 'not a project', key: '<img src=x onerror=alert(1)>' }),
      },
    })
    expect(fieldValue(document, 'Project')).toBe(CONFIRM_UNDRAWABLE)
    expect(fieldValue(document, 'Setting')).toBe(CONFIRM_UNDRAWABLE)
    // Neither value is anywhere in the window's text: what a refusal draws is the
    // console's own sentence about the class and nothing else.
    const fields = document.byId.get('confirm-fields') as FakeElement
    const drawnText = fields.children.map((child) => child.textContent ?? '').join('\n')
    expect(drawnText).not.toContain('onerror')
    expect(drawnText).not.toContain('not a project')
  })

  it('shapes a narrowing as one click: no tick, Confirm is the default and it answers', async () => {
    const { element, answers } = await mount({ fields: { ok: true, data: aSettingsView() } })
    const actions = element('confirm-actions')
    expect(actions.children.map((child) => child.textContent)).toEqual([SAVE_LABEL, CANCEL_LABEL])
    const confirm = actions.button(SAVE_LABEL)
    expect(confirm.disabled).toBe(false)
    // The default button is the view's own decision (D1109) and it is a focus,
    // never a keystroke: the confirming button stays out of the tab order, and a
    // synthesized click on it still decides nothing.
    expect(confirm.focused).toBe(true)
    expect(confirm.tabIndex).toBe(-1)
    await confirm.fire('click', 0)
    expect(answers).toEqual([])
    await confirm.fire('click', 1)
    // No tick was drawn, so the answer carries none: the member means "the
    // widening's tick", and a narrowing has no box for one. It is **not** sent as
    // `ticked: false` -- "no tick was drawn" and "the box is clear" are two
    // different facts, and the main process refuses a widening's yes on the
    // second (D1109).
    expect(answers).toEqual([{ answer: 'yes' }])
  })

  it('shapes a widening as two answers: the tick, Confirm disabled until it is set, and Cancel focused', async () => {
    const { element, answers } = await mount({
      fields: {
        ok: true,
        data: aSettingsView({
          widening: true,
          why: 'this raises the cap',
          tick: SETTINGS_TICK_LABEL,
          confirmNeedsTick: true,
          defaultButton: 'cancel',
        }),
      },
    })
    const actions = element('confirm-actions')
    const box = actions.children[0] as FakeElement
    const label = actions.children[1] as FakeElement
    const confirm = actions.button(SAVE_LABEL)

    // The tick is a real checkbox beside its own words: the label is a `for=`
    // pointing at the box rather than a text node a screen reader would read on
    // its own.
    expect(box.tag).toBe('input')
    expect(box.attributes.get('type')).toBe('checkbox')
    expect(box.attributes.get('id')).toBe(TICK_ID)
    expect(label.tag).toBe('label')
    expect(label.attributes.get('for')).toBe(TICK_ID)
    expect(label.textContent).toBe(SETTINGS_TICK_LABEL)
    expect(actions.children.map((child) => child.textContent)).toEqual([
      null,
      SETTINGS_TICK_LABEL,
      SAVE_LABEL,
      CANCEL_LABEL,
    ])

    // A widening starts as a window nothing can confirm: Confirm is disabled and
    // the focused control is the one that declines.
    expect(box.checked).toBe(false)
    expect(confirm.disabled).toBe(true)
    expect(actions.button(CANCEL_LABEL).focused).toBe(true)
    expect(confirm.focused).toBe(false)
    expect(confirm.tabIndex).toBe(-1)

    box.checked = true
    await box.fire('change', 1)
    expect(confirm.disabled).toBe(false)
    await confirm.fire('click', 1)
    expect(answers).toEqual([{ answer: 'yes', ticked: true }])
  })

  it('carries the tick’s own state to the main process, which is where it is enforced', async () => {
    // The document disables Confirm, and the main process refuses an unticked yes
    // as well (`CONFIRM_TICK_NEEDED`, C3): this is the second half's input -- the
    // answer with the box still clear -- and the sentence a refusal draws is the
    // same path the launch's will-not-yet click takes.
    const { element, answers } = await mount({
      fields: {
        ok: true,
        data: aSettingsView({ widening: true, tick: SETTINGS_TICK_LABEL, confirmNeedsTick: true }),
      },
      answerReply: { ok: false, reason: 'this confirmation raises a limit: tick the box that says so before confirming' },
    })
    const actions = element('confirm-actions')
    await actions.button(SAVE_LABEL).fire('click', 1)
    expect(answers).toEqual([{ answer: 'yes', ticked: false }])
    expect(answers).toEqual([{ answer: 'yes', ticked: false }])
    expect(element('confirm-status').textContent).toBe(
      'this confirmation raises a limit: tick the box that says so before confirming',
    )
  })
})

// ---------------------------------------------------------------------------
// The same document's third question: the entry form, and the dry run it runs
// (SU1, D2902; R5, R6). **One window and two phases**: the form is what the
// window is shown first, OK submits the typed value, and the main process then
// settles the window with the dry run's own view -- so the diff is drawn by the
// second describe's rows, in this same window, and the second button writes.
//
// The cases here are the acceptance text, one case each: the rows the modal
// shows, the control the key's type calls for, OK disabled until the client
// validation passes with the error drawn inline, the reset with R6's built-in
// default, the read-only row with the command to copy, and the two phases.
// ---------------------------------------------------------------------------

/** anEntryView is the window's **third** subject: the form the main process
 * builds from the read's own row (`settingsEntryView`). It is the shared
 * module's type rather than a mirror, because the rule that decides whether OK
 * is enabled travels with it. */
function anEntryView(overrides: Partial<SettingsEntryFields> = {}): SettingsEntryFields {
  return {
    kind: 'settings-entry',
    project: 'sandbox',
    key: 'budget.cap_usd',
    label: 'Budget cap',
    description: 'What one run may spend.',
    current: '25',
    defaultValue: '25',
    defaultUnset: '',
    caveat: SETTINGS_ENTRY_CAVEAT,
    control: 'number',
    options: [],
    min: '1',
    max: '100',
    format: '',
    type: 'int',
    command: '',
    ...overrides,
  }
}

/** entryValue is the text of one row's `<dd>`, and codeValue is the text of the
 * `<code>` inside it -- the raw key and the command are drawn in a `<code>` of
 * their own, which is what makes them legible as a value to copy. */
function codeValue(document: FakeDocument, label: string): string | null {
  const fields = document.byId.get('confirm-fields') as FakeElement
  const index = fields.children.findIndex((child) => child.tag === 'dt' && child.textContent === label)
  const box = index === -1 ? null : fields.children[index + 1]?.children.find((child) => child.tag === 'code')
  return box?.textContent ?? null
}

/** controlOf is the one control the form drew, whatever the key's type called
 * for, and it is found by the tags rather than by an id so that a view that drew
 * the wrong control fails the type assertions rather than a lookup. */
function controlOf(document: FakeDocument): FakeElement {
  const fields = document.byId.get('confirm-fields') as FakeElement
  const found = fields.children
    .flatMap((child) => child.children)
    .find((child) => child.tag === 'input' || child.tag === 'select')
  if (found === undefined) throw new Error('the form drew no control')
  return found
}

/** okButton is the entry form's OK, whatever the form drew beside it. */
function okButton(document: FakeDocument): FakeElement {
  return (document.byId.get('confirm-actions') as FakeElement).button(ENTRY_OK_LABEL)
}

describe('the entry form the same document draws', () => {
  it('draws the label, the description, the raw key, the value, the default and R6’s caveat', async () => {
    const { document, element } = await mount({ fields: { ok: true, data: anEntryView() } })

    expect(element('confirm-title').textContent).toBe(TITLE_ENTRY)
    expect(TITLE_ENTRY).not.toBe(TITLE_LAUNCH)
    expect(element('confirm-task-heading').textContent).toBe('')
    expect(element('confirm-task').textContent).toBeNull()

    expect(fieldValue(document, 'Project')).toBe('sandbox')
    expect(fieldValue(document, 'Name')).toBe('Budget cap')
    expect(fieldValue(document, 'Description')).toBe('What one run may spend.')
    expect(codeValue(document, 'Setting')).toBe('budget.cap_usd')
    // The raw key is drawn as code **and** as its own row: the label is what a
    // person calls the setting, and the key is what the command line takes, and
    // the window draws both (D2908).
    expect(fieldValue(document, 'Current value')).toBe('25')
    expect(fieldValue(document, 'Built-in default')).toBe('25')
    // R6's second half: the built-in default is named as such, and the window
    // says that a lower layer may take over after a reset.
    expect(fieldValue(document, 'After a reset')).toBe(SETTINGS_ENTRY_CAVEAT)
    // None of the diff's rows and none of the launch's: this phase is the form.
    for (const absent of ['Written at', 'Decides today', 'Run id', 'Title', 'Task size']) {
      expect(fieldValue(document, absent), `the form drew ${absent}`).toBeNull()
    }
    expect(commandWords(document)).toEqual([])
  })

  it('draws the control the key’s type calls for, and R6’s sentence for a key with no default', async () => {
    // A small enum is a select, and the value a run reads now is the selected
    // one.
    const select = await mount({
      fields: {
        ok: true,
        data: anEntryView({
          type: 'enum',
          control: 'select',
          options: ['off', 'warn', 'enforce'],
          current: 'warn',
          min: '',
          max: '',
          defaultValue: '',
          defaultUnset: 'no opinion is recorded',
        }),
      },
    })
    const control = controlOf(select.document)
    expect(control.tag).toBe('select')
    expect(control.value).toBe('warn')
    expect(control.children.map((child) => child.textContent)).toEqual(['off', 'warn', 'enforce'])
    // R6's other spelling: a key with no built-in value draws the CLI's own
    // sentence about what unset means.
    expect(fieldValue(select.document, 'Built-in default')).toBe('no opinion is recorded')

    // A bool is a real checkbox, checked from the value a run reads now.
    const toggle = await mount({
      fields: { ok: true, data: anEntryView({ type: 'bool', control: 'toggle', current: 'true', defaultValue: 'false' }) },
    })
    const box = controlOf(toggle.document)
    expect(box.tag).toBe('input')
    expect(box.attributes.get('type')).toBe('checkbox')
    expect(box.checked).toBe(true)

    // A number carries the metadata's own bounds as attributes, which is the
    // browser's own stepping -- `settingsEntryError` is what gates the button.
    const number = await mount({ fields: { ok: true, data: anEntryView() } })
    const input = controlOf(number.document)
    expect(input.attributes.get('type')).toBe('number')
    expect(input.attributes.get('min')).toBe('1')
    expect(input.attributes.get('max')).toBe('100')

    // A duration is text and not a stepped number, and so is a key with no
    // metadata at all: the fallback is the raw key and a text control (D2908).
    for (const view of [
      anEntryView({ type: 'duration', control: 'text', min: '', max: '', current: '14d', defaultValue: '14d' }),
      anEntryView({ type: '', control: 'text', min: '', max: '' }),
    ]) {
      const bare = await mount({ fields: { ok: true, data: view } })
      expect(controlOf(bare.document).attributes.get('type')).toBe('text')
    }
  })

  it('leaves OK enabled while the value passes, and disables it with the sentence beside it when it does not', async () => {
    // SU1's red step: "a test that a number outside `min..max` leaves OK disabled
    // fails before the control exists". The rule is R5's client validation, and
    // the sentence is drawn **inline**, beside the control it is about.
    const { document } = await mount({ fields: { ok: true, data: anEntryView() } })
    const control = controlOf(document)
    const ok = okButton(document)
    expect(ok.disabled).toBe(false)
    expect(control.value).toBe('25')

    control.value = '101'
    await control.fire('input', 0)
    expect(ok.disabled).toBe(true)
    expect(fieldValue(document, 'New value')).toBe(`${SETTINGS_ENTRY_ABOVE_MAX}100)`)

    // Back inside the bounds, the sentence goes and OK comes back: the form
    // never needs a second window to correct a typo.
    control.value = '100'
    await control.fire('change', 0)
    expect(ok.disabled).toBe(false)
    expect(fieldValue(document, 'New value')).toBe('')

    // An empty field is refused for the types that need a value.
    control.value = ''
    await control.fire('input', 0)
    expect(ok.disabled).toBe(true)
    expect(fieldValue(document, 'New value')).not.toBe('')
  })

  it('submits the control’s own value on OK, and the empty answer on Reset', async () => {
    // The two buttons answer the **same** channel with the **same** shape
    // (`{answer: 'submit', value}`), and the difference between them is the
    // value: the typed one, and `null` for R6's reset (F8: a reset writes no
    // value at all).
    const typed = await mount({ fields: { ok: true, data: anEntryView() } })
    const control = controlOf(typed.document)
    control.value = '30'
    await control.fire('input', 0)
    // The entry form's buttons are the deliberate exception to the pointer-only
    // rule: what they reach is the dry run, which writes nothing.
    await okButton(typed.document).fire('click', 0)
    expect(typed.answers).toEqual([{ answer: 'submit', value: '30' }])

    const reset = await mount({ fields: { ok: true, data: anEntryView() } })
    await (reset.document.byId.get('confirm-actions') as FakeElement).button(ENTRY_RESET_LABEL).fire('click', 0)
    expect(reset.answers).toEqual([{ answer: 'submit', value: null }])
  })

  it('shows the dry run’s diff in the same window, and writes only on the second button', async () => {
    // D2902's one modal, and rule 12's two steps: OK runs `settings set …
    // --dry-run --json`, the main process settles this window with its result,
    // and the same window then shows old/new/the two layers/the exact argv with
    // the button that writes. A view that opened a second window, or that wrote
    // on the first button, fails here.
    const document = fakeDocument()
    const seen: (string | null)[] = []
    const { bridge, answers, calls } = fakeBridge({
      fieldsReplies: [
        { ok: true, data: anEntryView() },
        { ok: true, data: aSettingsView({ sentence: SETTINGS_SHADOWED_SENTENCE, why: 'this raises the cap' }) },
      ],
      onAnswer: () => seen.push((document.byId.get('confirm-status') as FakeElement).textContent),
    })
    await mountConfirm(document, bridge)
    const control = controlOf(document)
    control.value = '30'
    await control.fire('input', 0)
    await okButton(document).fire('click', 1)

    expect(calls).toEqual(['fields', 'answer', 'fields'])
    expect(answers).toEqual([{ answer: 'submit', value: '30' }])
    // The status says the dry run is out while it is: a button that went quiet
    // with nothing said is a window that looks like it ignored the press.
    expect(seen).toEqual([ENTRY_RUNNING])

    // The form is gone and the diff is drawn in its place: the same `dl`, the
    // same two classes of rows, and the argv word by word.
    expect(fieldValue(document, 'Name')).toBeNull()
    expect(fieldValue(document, 'Built-in default')).toBeNull()
    expect(fieldValue(document, 'Current value')).toBe('25')
    expect(fieldValue(document, 'New value')).toBe('30')
    expect(fieldValue(document, 'Written at')).toBe('project')
    expect(fieldValue(document, 'Decides today')).toBe('project')
    expect(fieldValue(document, 'What this changes')).toBe(SETTINGS_SHADOWED_SENTENCE)
    expect(fieldValue(document, 'Why the CLI asks')).toBe('this raises the cap')
    expect(commandWords(document)).toEqual(SETTINGS_ARGV)
    expect((document.byId.get('confirm-title') as FakeElement).textContent).toBe(TITLE_SETTINGS)

    // The second button writes, it is the pointer-only one, and it answers the
    // third shape of the answer: a plain yes, because this diff drew no tick.
    const write = (document.byId.get('confirm-actions') as FakeElement).button(SAVE_LABEL)
    expect(write.tabIndex).toBe(-1)
    expect(write.textContent).toBe(SAVE_LABEL)
    await write.fire('click', 0)
    expect(answers).toEqual([{ answer: 'submit', value: '30' }])
    await write.fire('click', 1)
    expect(answers).toEqual([{ answer: 'submit', value: '30' }, { answer: 'yes' }])
  })

  it('leaves the form standing, with the sentence, when the dry run refuses', async () => {
    // A submit the main process refused is not a decision: the operator reads
    // why (`settingsEntryError` is a pre-check and the CLI's reader the
    // authority), the form is exactly as they left it, and both buttons are live
    // again -- a form left disabled is a form an operator has to reopen.
    const { document, answers } = await mount({
      fields: { ok: true, data: anEntryView() },
      answerReply: { ok: false, reason: 'budget.cap_usd: this value takes --add' },
    })
    await okButton(document).fire('click', 0)
    expect(answers).toEqual([{ answer: 'submit', value: '25' }])
    expect((document.byId.get('confirm-status') as FakeElement).textContent).toBe(
      'budget.cap_usd: this value takes --add',
    )
    expect(fieldValue(document, 'Name')).toBe('Budget cap')
    expect(okButton(document).disabled).toBe(false)
    expect((document.byId.get('confirm-actions') as FakeElement).button(ENTRY_RESET_LABEL).disabled).toBe(false)
  })

  it('draws a key this window does not write read-only, with the command to copy', async () => {
    // R5's last sentence: "keys that take `--add`, `--remove` or `--add-entry`
    // show read-only with the command to copy". The view carries no control for
    // them, so this window draws none, offers no OK and no reset, and says why.
    const { document } = await mount({
      fields: {
        ok: true,
        data: anEntryView({
          control: null,
          command: 'fugaro config publish, which carries the layer file the key is stated in',
        }),
      },
    })
    expect(fieldValue(document, 'Project')).toBe('sandbox')
    expect(codeValue(document, 'Command')).toBe('fugaro config publish, which carries the layer file the key is stated in')
    expect((document.byId.get('confirm-status') as FakeElement).textContent).toBe(SETTINGS_ENTRY_READONLY)
    const actions = document.byId.get('confirm-actions') as FakeElement
    expect(actions.children.map((child) => child.textContent)).toEqual([CANCEL_LABEL])
    // No control was built at all: the row is read-only, and a disabled input
    // would still be a value the window looks like it could send.
    expect(document.created.filter((tag) => tag === 'input' || tag === 'select')).toEqual([])
  })

  it('draws every entry value through its own class, and neither as markup nor raw when refused', async () => {
    // The same rule the diff's rows are held to, asserted for the form: each
    // string a document wrote goes through `sanitizeText` with the class the
    // Settings section draws it with, and a refusal is the console's own
    // sentence rather than the value.
    const { document } = await mount({
      fields: {
        ok: true,
        data: anEntryView({
          label: '<b>the label</b>',
          description: '<script>alert(1)</script>',
          key: '<img src=x onerror=alert(1)>',
          defaultValue: '<i>9</i>',
        }),
      },
    })
    expect(fieldValue(document, 'Name')).toBe('<b>the label</b>')
    expect(fieldValue(document, 'Description')).toBe('<script>alert(1)</script>')
    expect(fieldValue(document, 'Built-in default')).toBe('<i>9</i>')
    expect(codeValue(document, 'Setting')).toBe(CONFIRM_UNDRAWABLE)
    expect(document.created.filter((tag) => ['img', 'b', 'script', 'i', 'a'].includes(tag))).toEqual([])
    // The non-vacuity: the sentence above is the refusal path, not every value's
    // fate -- a key its class admits is drawn as itself.
    expect(drawn('budget.cap_usd', 'branch', 'settings[].key')).toBe('budget.cap_usd')
  })

  it('draws a refused answer’s sentence, and nothing at all when the second read refuses', async () => {
    // Two refusals the form can meet after a submit: an accepted submit whose
    // re-read is refused (the main process's own sentence), and a settled answer
    // that is not the diff this window drew the form for. Neither is guessed at.
    const reread = await mount({
      fieldsReplies: [
        { ok: true, data: anEntryView() },
        { ok: false, reason: 'the settings read answered no document' },
      ],
    })
    await okButton(reread.document).fire('click', 1)
    expect((reread.document.byId.get('confirm-status') as FakeElement).textContent).toBe(
      'the settings read answered no document',
    )
    expect(okButton(reread.document).disabled).toBe(false)
    expect(fieldValue(reread.document, 'Name')).toBe('Budget cap')

    const unexpected = await mount({
      fieldsReplies: [{ ok: true, data: anEntryView() }, { ok: true, data: aView() }],
    })
    await okButton(unexpected.document).fire('click', 1)
    expect((unexpected.document.byId.get('confirm-status') as FakeElement).textContent).toBe(
      'the dry run answered, but this window was not shown its result: nothing has been written',
    )
    expect(fieldValue(unexpected.document, 'Name')).toBe('Budget cap')
  })
})
