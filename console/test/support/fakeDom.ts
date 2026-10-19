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

import { CLI_HARD_FLOOR_VERSION, CLI_VERSION_BUILT_AGAINST, SUPPORTED_CLI_MINORS } from '../../src/shared/version'

/** FakeNode is one element of the fake document. */
export class FakeNode {
  readonly tagName: string
  readonly children: FakeNode[] = []
  readonly attributes = new Map<string, string>()
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

  appendChild(child: FakeNode): FakeNode {
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
    this.children.length = 0
    this.children.push(...nodes)
  }

  /** listeners is what the entry attached, by event type. The wiring is a click
   * on a drawn control, and a test drives it by calling `click()`. */
  readonly listeners = new Map<string, (() => void)[]>()

  addEventListener(type: string, listener: () => void): void {
    const found = this.listeners.get(type) ?? []
    found.push(listener)
    this.listeners.set(type, found)
  }

  /** click fires this node's click listeners, in the order they were added. */
  click(): void {
    for (const listener of this.listeners.get('click') ?? []) listener()
  }
}

/** FakeDocument is the whole document the entry is given: a body to mount into,
 * a factory, and the visibility the Live poll reads. */
export class FakeDocument {
  readonly body = new FakeNode('body')
  visibilityState = 'visible'
  readonly listeners = new Map<string, (() => void)[]>()

  createElement(tag: string): FakeNode {
    return new FakeNode(tag)
  }

  addEventListener(type: string, listener: () => void): void {
    const found = this.listeners.get(type) ?? []
    found.push(listener)
    this.listeners.set(type, found)
  }

  /** setVisibility moves the document's visibility and fires the listeners, which
   * is what a real window does when it is minimized or occluded. */
  setVisibility(state: string): void {
    this.visibilityState = state
    for (const listener of this.listeners.get('visibilitychange') ?? []) listener()
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
