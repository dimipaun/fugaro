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
  NONE,
  SAVE_LABEL,
  TASK_HEADING,
  TICK_ID,
  TITLE_LAUNCH,
  TITLE_SETTINGS,
  mountConfirm,
  taskSizeText,
} from '../../src/renderer/confirm'
import type {
  ConfirmDocument,
  ConfirmElement,
  ConfirmEvent,
  ConfirmFields,
  SettingsFields,
} from '../../src/renderer/confirm'
import { SETTINGS_SHADOWED_SENTENCE, SETTINGS_TICK_LABEL } from '../../src/main/settingsview'
import type { ConfirmBridge, ConfirmReply } from '../../src/preload/confirm'

// ---------------------------------------------------------------------------
// A document, faked from the real one
// ---------------------------------------------------------------------------

/** FakeElement is one node: text, the two properties the view touches, a child
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
 * order `argvFor` builds it (`settings set KEY --layer L <phase> --project P --
 * VALUE`): the window shows it rather than a summary of it. */
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
 * asked. */
function fakeBridge(
  options: {
    readonly fields?: ConfirmReply
    readonly fieldsError?: Error
    readonly answerReply?: ConfirmReply
  } = {},
): {
  readonly bridge: ConfirmBridge
  readonly answered: readonly boolean[]
  readonly ticks: readonly (boolean | undefined)[]
  readonly calls: string[]
} {
  const answered: boolean[] = []
  const ticks: (boolean | undefined)[] = []
  const calls: string[] = []
  const bridge: ConfirmBridge = {
    fields: async () => {
      calls.push('fields')
      if (options.fieldsError !== undefined) throw options.fieldsError
      return options.fields ?? { ok: true, data: aView() }
    },
    answer: async (yes: boolean, ticked?: boolean) => {
      calls.push('answer')
      answered.push(yes)
      ticks.push(ticked)
      return options.answerReply ?? { ok: true, data: { answered: true } }
    },
  }
  return { bridge, answered, ticks, calls }
}

/** mount builds the view over a fresh fake document, and returns the pieces a
 * test needs. Every test starts here. */
async function mount(options: Parameters<typeof fakeBridge>[0] = {}) {
  const document = fakeDocument()
  const { bridge, answered, ticks, calls } = fakeBridge(options)
  await mountConfirm(document, bridge)
  return {
    document,
    answered,
    ticks,
    calls,
    element: (id: string): FakeElement => document.byId.get(id) as FakeElement,
  }
}

/** fieldValue is the `<dd>` a `<dt>` labels, or `null` when there is none. */
function fieldValue(document: FakeDocument, label: string): string | null {
  const fields = document.byId.get('confirm-fields') as FakeElement
  const index = fields.children.findIndex((child) => child.tag === 'dt' && child.textContent === label)
  return index === -1 ? null : (fields.children[index + 1]?.textContent ?? null)
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
    expect(keyboard.answered).toEqual([])
    expect(keyboard.calls).toEqual(['fields'])

    const pointer = await mount()
    const pointerActions = pointer.element('confirm-actions')
    await pointerActions.button(CONFIRM_LABEL).fire('click', 1)
    expect(pointer.answered).toEqual([true])
    expect(pointer.calls).toEqual(['fields', 'answer'])

    // Cancel answers on any click at all: it is the default, and the button a
    // keyboard reaches.
    const cancelled = await mount()
    await cancelled.element('confirm-actions').button(CANCEL_LABEL).fire('click', 0)
    expect(cancelled.answered).toEqual([false])
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
    const { element, answered, calls } = await mount({
      fields: { ok: false, reason: 'the confirmation window was opened for a launch this process no longer holds' },
    })
    expect(element('confirm-status').textContent).toBe(
      'the confirmation window was opened for a launch this process no longer holds',
    )
    const actions = element('confirm-actions')
    expect(actions.children.map((child) => child.textContent)).toEqual([CANCEL_LABEL])
    expect(actions.children.some((child) => child.textContent === CONFIRM_LABEL)).toBe(false)
    await actions.button(CANCEL_LABEL).fire('click', 0)
    expect(answered).toEqual([false])
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
    expect(refused.answered).toEqual([true])
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

  it('draws every settings value as a literal text node', async () => {
    // A key, an old value and the CLI's own sentence are text the CLI printed,
    // and an old value is whatever the operator wrote last time: the same rule
    // the launch's values are held to, asserted for this half of the union.
    const { document } = await mount({
      fields: {
        ok: true,
        data: aSettingsView({
          key: '<img src=x onerror=alert(1)>',
          oldValue: '<b>25</b>',
          newValue: 'javascript:alert(1)',
          sentence: '<script>alert(1)</script>',
        }),
      },
    })
    expect(fieldValue(document, 'Setting')).toBe('<img src=x onerror=alert(1)>')
    expect(fieldValue(document, 'Current value')).toBe('<b>25</b>')
    expect(fieldValue(document, 'What this changes')).toBe('<script>alert(1)</script>')
    expect(document.created.filter((tag) => ['img', 'b', 'script', 'a'].includes(tag))).toEqual([])
    // This edit is a narrowing, so it draws no tick: the elements are the two
    // buttons and the field rows, exactly as the launch's are.
    expect([...new Set(document.created)].sort()).toEqual(['button', 'dd', 'dt'])
  })

  it('shapes a narrowing as one click: no tick, Confirm is the default and it answers', async () => {
    const { element, answered, ticks } = await mount({ fields: { ok: true, data: aSettingsView() } })
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
    expect(answered).toEqual([])
    await confirm.fire('click', 1)
    expect(answered).toEqual([true])
    // No tick was drawn, so the answer carries none: the channel's second member
    // means "the widening's tick", and a narrowing has no box for one.
    expect(ticks).toEqual([undefined])
  })

  it('shapes a widening as two answers: the tick, Confirm disabled until it is set, and Cancel focused', async () => {
    const { element, answered, ticks } = await mount({
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
    expect(answered).toEqual([true])
    expect(ticks).toEqual([true])
  })

  it('carries the tick’s own state to the main process, which is where it is enforced', async () => {
    // The document disables Confirm, and the main process refuses an unticked yes
    // as well (`CONFIRM_TICK_NEEDED`, C3): this is the second half's input -- the
    // answer with the box still clear -- and the sentence a refusal draws is the
    // same path the launch's will-not-yet click takes.
    const { element, answered, ticks } = await mount({
      fields: {
        ok: true,
        data: aSettingsView({ widening: true, tick: SETTINGS_TICK_LABEL, confirmNeedsTick: true }),
      },
      answerReply: { ok: false, reason: 'this confirmation raises a limit: tick the box that says so before confirming' },
    })
    const actions = element('confirm-actions')
    await actions.button(SAVE_LABEL).fire('click', 1)
    expect(answered).toEqual([true])
    expect(ticks).toEqual([false])
    expect(element('confirm-status').textContent).toBe(
      'this confirmation raises a limit: tick the box that says so before confirming',
    )
  })
})
