// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
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
//     Enter and Escape are already declined in the main process;
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
  mountConfirm,
  taskSizeText,
} from '../../src/renderer/confirm'
import type {
  ConfirmDocument,
  ConfirmElement,
  ConfirmEvent,
  ConfirmFields,
} from '../../src/renderer/confirm'
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

/** fakeBridge is the confirmation window's whole surface, recording what it was
 * asked. */
function fakeBridge(options: { readonly fields?: ConfirmReply } = {}): {
  readonly bridge: ConfirmBridge
  readonly answered: readonly boolean[]
  readonly calls: string[]
} {
  const answered: boolean[] = []
  const calls: string[] = []
  const bridge: ConfirmBridge = {
    fields: async () => {
      calls.push('fields')
      return options.fields ?? { ok: true, data: aView() }
    },
    answer: async (yes: boolean) => {
      calls.push('answer')
      answered.push(yes)
      return { ok: true, data: { answered: true } }
    },
  }
  return { bridge, answered, calls }
}

/** mount builds the view over a fresh fake document, and returns the pieces a
 * test needs. Every test starts here. */
async function mount(options: Parameters<typeof fakeBridge>[0] = {}) {
  const document = fakeDocument()
  const { bridge, answered, calls } = fakeBridge(options)
  await mountConfirm(document, bridge)
  return {
    document,
    answered,
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
    expect(lines[0]).toBe('<!-- SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise -->')
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

  it('draws every value as a literal text node, and the fake refuses markup', async () => {
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

    // The mutation proof, and the reason the fake has no markup slot: the same
    // value written through one is recorded as a failure and refused.
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

  it('gives the task box a rule that scrolls it, and a bound', () => {
    // "displayed in full and scrollable": the text is never cut in the DOM, so
    // the scrolling is the stylesheet's, and the box is bounded so a 16 KiB
    // task does not push the buttons off the window.
    const screens = readFileSync(
      fileURLToPath(new URL('../../src/renderer/theme/screens.css', import.meta.url)),
      'utf8',
    )
    const rule = /\.confirm-task\s*\{([^}]*)\}/.exec(screens)
    expect(rule, 'screens.css carries no rule for the task box').not.toBeNull()
    expect(rule?.[1]).toMatch(/overflow:\s*auto/)
    expect(rule?.[1]).toMatch(/max-height:\s*\S+/)
    // The class is on the box the document declares, so the rule is not styling
    // nothing.
    expect(html).toContain('class="confirm-task"')
  })
})
