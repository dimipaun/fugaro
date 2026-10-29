// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The fake document the entry is driven against (desktop-console plan Tasks
// 3.4a and 3.4c).
//
// Two unit suites need the same thing -- a document the renderer entry can be
// run against without Electron -- and they need it to be hostile in the same
// places, so there is one of it:
//
//   * `test/unit/bundle.test.ts` runs the **built bundle** in a `node:vm` realm
//     against it, which is the only way the shipped artifact is exercised
//     without a window;
//   * `test/unit/entry-wiring.test.ts` imports `src/renderer/entry.ts` and calls
//     `bootDashboard` with it, which is how the wiring (the gate, the poll, the
//     controls) is driven without a build.
//
// FakeNode is hostile in the two places a drawing must not reach:
//
//   - **the markup-parsing slots** (`innerHTML`, `outerHTML`,
//     `insertAdjacentHTML`) throw, so a bundle that built an element from a
//     string fails here rather than in a window (design section 5.1 rule 7);
//   - **the navigating, styling and event attributes** throw in
//     `setAttribute`, so a drawing cannot set `href`, an inline style or an
//     `on*` handler even by accident. `style` is in that set because rule 3's
//     `style-src 'self'` allows no inline style, and the theme is drawn as
//     `data-theme` rather than as one.
//
// Nothing else of the DOM is offered, so a drawing that reaches for something
// else -- a `querySelector`, a `classList`, a `dataset` -- fails here rather than
// passing on a surface a real window would not give it either.
//
// **The keyboard is the one thing Task 6.5 adds to this fake, and it is offered
// for the same reason the click is**: a tablist's behaviour is a listener on a
// node, and a suite that could not dispatch a key could not tell a roving
// `tabindex` that works from one that is only written down. So `press` fires a
// node's `keydown` listeners with **both fields of a key**, because the console
// reads both: `code`, the physical key the shortcut keys are mapped on, and
// `key`, the character the layout produced from it -- plus the two modifiers.
// And `focus` records the node as the document's active element, which
// is the observable the arrow keys are held to, since a roving `tabindex` that
// moved the attribute and left the keyboard where it was would be a tablist that
// looks right and is not.

import { CLI_HARD_FLOOR_VERSION, CLI_VERSION_BUILT_AGAINST, SUPPORTED_CLI_MINORS } from '../../src/shared/version'

/** FakeEvent is what this fake dispatches: the fields the console's listeners
 * read, and the `preventDefault` that says a key the console handled is no
 * longer the browser's. One shape covers a click and a keydown, because the two
 * differ only in what the case cares about. */
export interface FakeEvent {
  readonly key: string
  readonly code: string
  readonly metaKey: boolean
  readonly ctrlKey: boolean
  readonly defaultPrevented: boolean
  /** target is where the event happened, which is the node a listener reads to
   * tell a click on a control *inside* it from a click on itself. */
  readonly target: FakeNode | null
  preventDefault(): void
}

/** fakeEvent builds one, with `defaultPrevented` flipped by its own
 * `preventDefault` -- which is what lets a case assert that the console took the
 * key rather than letting the window scroll under it. */
function fakeEvent(
  key: string,
  code: string,
  metaKey: boolean,
  ctrlKey: boolean,
  target: FakeNode | null = null,
): FakeEvent {
  const event = {
    key,
    code,
    metaKey,
    ctrlKey,
    defaultPrevented: false,
    target,
    preventDefault(): void {
      event.defaultPrevented = true
    },
  }
  return event
}

/** FakeModifiers is the two modifiers a case may hold down. */
export interface FakeModifiers {
  readonly meta?: boolean
  readonly ctrl?: boolean
}

/** codeOf is the physical key a case gets when it spells only a character: the
 * number row's digit for a digit, and the character itself otherwise. A real
 * keyboard event carries both, and they differ wherever the layout does -- the
 * unshifted `1` key of an AZERTY keyboard produces `&`, and a keypad key is
 * `Numpad1` while producing `1` -- so **a case that cares which physical key was
 * pressed passes the code itself** rather than relying on this default. */
function codeOf(key: string): string {
  return /^[0-9]$/.test(key) ? `Digit${key}` : key
}

/** keyEvent builds the keyboard event the console's mappings are driven with, for
 * a case that has no node to dispatch one on. */
export function keyEvent(key: string, modifiers: FakeModifiers = {}, code = codeOf(key)): FakeEvent {
  return fakeEvent(key, code, modifiers.meta === true, modifiers.ctrl === true)
}

/** FakeNode is one element of the fake document. */
export class FakeNode {
  readonly tagName: string
  readonly children: FakeNode[] = []
  readonly attributes = new Map<string, string>()
  /** namespace is what `createElementNS` was called with, and `null` for a node
   * `createElement` made. It is the observable that says the graph's geometry was
   * made in SVG's namespace rather than the HTML one. */
  namespace: string | null = null
  private text: string | null = null

  constructor(tagName: string) {
    this.tagName = tagName.toUpperCase()
  }

  get textContent(): string | null {
    return this.text
  }

  set textContent(value: string | null) {
    this.text = value
  }

  get innerHTML(): never {
    throw new Error('the console read innerHTML')
  }

  set innerHTML(_value: string) {
    throw new Error('the console assigned innerHTML')
  }

  get outerHTML(): never {
    throw new Error('the console read outerHTML')
  }

  insertAdjacentHTML(): never {
    throw new Error('the console called insertAdjacentHTML')
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  setAttribute(name: string, value: string): void {
    if (name === 'href' || name === 'src' || name === 'srcdoc' || name === 'style' || name.startsWith('on')) {
      throw new Error(`the console set the ${name} attribute`)
    }
    this.attributes.set(name, value)
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }

  /** parent is the node this one was appended to, and `null` for a node nothing
   * holds. A real event's target answers `closest` by walking exactly this, so
   * the fake keeps it rather than leaving a nested control with no way back up
   * to the row whose listener it bubbles to. */
  parent: FakeNode | null = null

  appendChild(child: FakeNode): FakeNode {
    child.parent = this
    this.children.push(child)
    return child
  }

  /**
   * replaceChildren is how the entry swaps one screen for another inside its
   * own panel (Task 3.4c). It is here rather than a text write because a screen
   * is replaced as a *tree*: assigning `textContent` would be the one write this
   * fake exists to keep out of the entry.
   */
  replaceChildren(...nodes: FakeNode[]): void {
    for (const node of this.children) node.parent = null
    this.children.length = 0
    for (const node of nodes) node.parent = this
    this.children.push(...nodes)
  }

  /** owner is the document that made this node, so `focus()` has somewhere to
   * record itself. It is `null` for the body, which no factory made. */
  owner: FakeDocument | null = null

  /** listeners is what the entry attached, by event type. The wiring is a click
   * on a drawn control and a key on one of the two tablists, and a test drives
   * them by calling `click()` and `press()`. */
  readonly listeners = new Map<string, ((event: FakeEvent) => void)[]>()

  addEventListener(type: string, listener: (event: FakeEvent) => void): void {
    const found = this.listeners.get(type) ?? []
    found.push(listener)
    this.listeners.set(type, found)
  }

  /** closest answers the nearest ancestor-or-self carrying a tag name, which is
   * the one selector the console's listeners ask for: they tell a click on a
   * control nested in a row from a click on the row. */
  closest(selector: string): FakeNode | null {
    const wanted = selector.toLowerCase()
    for (let node: FakeNode | null = this; node !== null; node = node.parent) {
      if (node.tagName.toLowerCase() === wanted) return node
    }
    return null
  }

  /** click fires this node's click listeners, in the order they were added, with
   * `target` where the click happened -- this node when a case does not say
   * otherwise.
   *
   * The fake fires no ancestor's listeners: a real click on a control drawn
   * inside a row bubbles to the row, and a case that means that click says so
   * by firing the row's own with the control as the target. */
  click(target: FakeNode | null = this): void {
    for (const listener of this.listeners.get('click') ?? []) listener(fakeEvent('', '', false, false, target))
  }

  /** press fires this node's keydown listeners with one key and the modifiers a
   * case holds down, and answers the event, so a case can assert that the
   * console took the key (`defaultPrevented`) rather than leaving it to the
   * window. `code` is the physical key, which a real event carries beside the
   * character the layout produced from it (`codeOf` is the default). */
  press(key: string, modifiers: FakeModifiers = {}, code = codeOf(key)): FakeEvent {
    const event = keyEvent(key, modifiers, code)
    for (const listener of this.listeners.get('keydown') ?? []) listener(event)
    return event
  }

  /** focus is where the keyboard went: a real node moves the document's focus,
   * and this one records itself as the document's active element. */
  focus(): void {
    if (this.owner !== null) this.owner.activeElement = this
  }
}

/** FakeDocument is the whole document the entry is given: a body to mount into,
 * a factory, and the visibility the Live poll reads. */
export class FakeDocument {
  readonly body = new FakeNode('body')
  visibilityState = 'visible'
  /** activeElement is the node `focus()` last moved the keyboard to, which is
   * what a case asserts after an arrow key: the roving tabindex and the focus
   * are two halves of the same move and a case that read only the attribute
   * would pass on a tablist whose ring never moved. */
  activeElement: FakeNode | null = null
  readonly listeners = new Map<string, ((event: FakeEvent) => void)[]>()

  constructor() {
    this.body.owner = this
  }

  createElement(tag: string): FakeNode {
    const node = new FakeNode(tag)
    node.owner = this
    return node
  }

  /**
   * createElementNS is the SVG half of the factory (GV2.3b): the graph screen
   * draws geometry as SVG attributes, and an element made in the HTML namespace
   * is not drawn at all in a real window. The fake records the namespace so a
   * case can hold the screen to making its geometry in SVG's, and answers the
   * same node `createElement` does -- nothing else of a namespaced element is
   * different for a screen that only writes text and attributes.
   */
  createElementNS(namespace: string, tag: string): FakeNode {
    const node = new FakeNode(tag)
    node.owner = this
    node.namespace = namespace
    return node
  }

  addEventListener(type: string, listener: (event: FakeEvent) => void): void {
    const found = this.listeners.get(type) ?? []
    found.push(listener)
    this.listeners.set(type, found)
  }

  /** setVisibility moves the document's visibility and fires the listeners, which
   * is what a real window does when it is minimized or occluded. */
  setVisibility(state: string): void {
    this.visibilityState = state
    for (const listener of this.listeners.get('visibilitychange') ?? []) {
      listener(fakeEvent('', '', false, false))
    }
  }

  /** press fires the document's own keydown listeners: the rail's shortcut keys
   * are the document's and not a node's -- they reach a row from wherever the
   * operator is (design section 4.3) -- so this is where a case drives them.
   * `code` is the physical key (`codeOf` is the default), and it is the field the
   * shortcut keys read. */
  press(key: string, modifiers: FakeModifiers = {}, code = codeOf(key)): FakeEvent {
    const event = keyEvent(key, modifiers, code)
    for (const listener of this.listeners.get('keydown') ?? []) listener(event)
    return event
  }
}

/**
 * recordingTimers is the clock a suite lends the entry. `setInterval` is a
 * **host** object, not a language one, so a `vm` realm has none -- and the Live
 * screen's poll is one. The timers record the callbacks and never fire on their
 * own, so nothing keeps the test process alive and no poll happens unless a case
 * asks for one.
 */
export function recordingTimers(): {
  readonly globals: Record<string, unknown>
  tick(): void
  readonly live: () => number
} {
  const callbacks = new Map<number, () => void>()
  let next = 1
  return {
    globals: {
      setInterval: (callback: () => void): number => {
        const handle = next
        next += 1
        callbacks.set(handle, callback)
        return handle
      },
      clearInterval: (handle: number): void => {
        callbacks.delete(handle)
      },
    },
    tick(): void {
      for (const callback of [...callbacks.values()]) callback()
    },
    live: () => callbacks.size,
  }
}

/** everyNode is the tree under a node, depth first. */
export function everyNode(node: FakeNode): readonly FakeNode[] {
  return [node, ...node.children.flatMap((child) => everyNode(child))]
}

/** textOf is all the text drawn under a node. */
export function textOf(node: FakeNode): string {
  return everyNode(node)
    .map((each) => each.textContent ?? '')
    .join('\n')
}

/** anOkCompatibility is a handshake answer in range, built from the console's
 * own constants rather than from literals a drift could leave behind. */
export function anOkCompatibility(): Record<string, unknown> {
  return {
    mode: 'ok',
    skew: 'supported',
    cliVersion: '1.2.3',
    consoleVersion: CLI_VERSION_BUILT_AGAINST,
    supported: [...SUPPORTED_CLI_MINORS],
    floor: CLI_HARD_FLOOR_VERSION,
    hidden: [],
  }
}

/** aReducedCompatibility is the answer for a CLI newer than this console reads:
 * reduced, with all three views withheld (`hidesView` is `hidden.includes`, and
 * `compatibilityFrom` refuses a mode whose hidden list disagrees with it). */
export function aReducedCompatibility(): Record<string, unknown> {
  return {
    mode: 'reduced',
    skew: 'newer',
    cliVersion: '9.9.9',
    consoleVersion: CLI_VERSION_BUILT_AGAINST,
    supported: [...SUPPORTED_CLI_MINORS],
    floor: CLI_HARD_FLOOR_VERSION,
    hidden: ['spend', 'caps', 'kill'],
  }
}

/** aRefusedCompatibility is the answer for a CLI this console will not drive at
 * all: every view withheld, and the document loads no script of its own. */
export function aRefusedCompatibility(): Record<string, unknown> {
  return {
    mode: 'refused',
    skew: 'older',
    cliVersion: '0.1.0',
    consoleVersion: CLI_VERSION_BUILT_AGAINST,
    supported: [...SUPPORTED_CLI_MINORS],
    floor: CLI_HARD_FLOOR_VERSION,
    hidden: ['spend', 'caps', 'kill'],
  }
}
