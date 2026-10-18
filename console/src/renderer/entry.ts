// The renderer's entry point (desktop-console plan Task 3.4a; design section 5.1
// rules 1, 4 and 5).
//
// This is the file the dashboard document loads, and it is small on purpose:
// **its first call is `startConsole`, and it draws nothing before that promise
// settles** (plan step 5). Everything a screen may draw is behind the gate
// `startConsole` hands back -- and in this task the entry stops there. The
// refusal screen or the reduced-mode banner is drawn, and the dashboard frame
// stays empty: the screens are Task 3.4c's, and a reader of this task should not
// go looking for one here.
//
// Three things this file decides, each for a reason a reviewer can check:
//
//   - **A missing bridge is a refusal.** `window.fugaro` is put there by
//     `src/preload/index.ts` (Task 1.2's surface), and a document that has no
//     bridge is a document that cannot ask the handshake's question -- so the
//     answer it gets is the one that draws no figures. `bridgeFrom` answers a
//     `null` for it, which is the same value `startConsole` gets from a
//     *rejected* call, so both arrive at the gate as the refusal
//     (`compatibilityFrom` in `src/renderer/app.ts`).
//   - **The theme is fixed here.** `SkewTheme` is a parameter of every drawing
//     (`renderVersionSkew`'s third argument) and no task's Files name the code
//     that would read the operating system's preference (`theme/tokens.css` is
//     in the plan's tree and in no task's Files), so the entry names one theme
//     rather than inventing a preference query here. A later task that follows
//     the OS edits `CONSOLE_THEME`'s *value*; nothing else in this file moves.
//   - **The DOM is reached through the same narrow interface the screens get.**
//     `domSurface` hands `startConsole` a `SkewFactory` and a `MountLike`, and
//     the element it wraps exposes a text slot and attributes -- no markup
//     parsing, no navigating attribute, no style and no event handler -- which
//     is `src/renderer/app.ts`'s `ElementLike`, widened only by the `tag` and
//     `append` that `SkewElement` declares. That is design section 5.1 rule 7
//     reaching all the way to the one place a real document exists.
//
// **This file is bundled, not imported.** `tsc` compiles it to
// `dist/renderer/entry.js`, and `tools/bundle-renderer/index.ts` walks that
// file's string-literal relative `require` calls into one script
// (`dist/bundle/console.js`) that `src/renderer/index.html` loads with
// `<script src="./console.js" defer>`. Two consequences are load-bearing:
// every import here must stay a *relative path* (the bundler fails the build on
// anything else -- a bare specifier, a computed one, `electron`, a Node
// built-in), and this file must not import `src/preload/index.ts`, which is a
// value module with a `require('electron')` in it. The bridge is therefore
// described structurally (`CompatibilityBridge`), exactly as `app.ts` does.
//
// The bottom of the file is the one moment a renderer gets: the document has
// loaded, so `document` is there. The guard is the same shape
// `src/preload/index.ts` uses, and for the same reason -- `test/unit` imports
// this module under plain Node, where there is no document and nothing should
// happen at all.

import { startConsole } from './app'
import type { CompatibilityBridge, MountLike } from './app'
import type { SkewElement, SkewFactory, SkewTheme } from '../shared/version'

/**
 * CONSOLE_THEME is the theme every drawing is made for. It is one word rather
 * than a preference read, for the reason in this file's header: the code that
 * would read the operating system's preference is in no task's Files, and a
 * drawing that guessed would be a drawing nobody decided. `'dark'` is the
 * console's own first screen in design section 6.3's mockups.
 */
export const CONSOLE_THEME: SkewTheme = 'dark'

/**
 * hasDashboardContext is the guard that keeps this module inert everywhere
 * except a document. A preload script and this entry are both loaded by a
 * renderer, so `document` is there; under `vitest`'s Node environment it is not,
 * and importing this module then does nothing.
 *
 * The test is `document` alone and **not** `window.fugaro`: a document with no
 * bridge is a real state this file has an answer for (the refusal), and a guard
 * that treated it as "not a renderer" would leave that document blank with no
 * explanation.
 */
export function hasDashboardContext(scope: { readonly document?: unknown }): boolean {
  return scope.document !== undefined
}

/**
 * DomElement is a wrapped node: the `SkewElement` interface and the real node
 * the wrapper is for. The node is not part of what a drawing sees -- it is how
 * `domSurface`'s mount finds the element to append -- and keeping it on the
 * wrapper rather than in a side table means the two cannot come apart.
 */
export interface DomElement extends SkewElement {
  readonly node: Element
}

/**
 * domSurface is the renderer's whole contact with a real document: a
 * `SkewFactory` that makes wrapped elements, and the `MountLike` a drawing is
 * appended through.
 *
 * `append` on a wrapper resolves the child through the same map, so a drawing
 * can only ever build a tree of elements this factory made: a child that did not
 * come from it is refused with a throw rather than appended as whatever object
 * it is. The failure is loud because it can only be a bug in the console's own
 * code -- every `append` call site is a drawing that was handed this factory --
 * and a silent no-op would be a screen that drew less than it thought.
 */
export function domSurface(document: Document): { factory: SkewFactory; mount: MountLike } {
  const nodes = new WeakMap<SkewElement, Element>()

  const nodeOf = (element: SkewElement): Element => {
    const node = nodes.get(element)
    if (node === undefined) {
      throw new Error('the console drew an element that is not this document’s')
    }
    return node
  }

  const wrap = (node: Element): SkewElement => {
    const element: DomElement = {
      node,
      tag: node.tagName.toLowerCase(),
      get textContent(): string | null {
        return node.textContent
      },
      set textContent(value: string | null) {
        node.textContent = value
      },
      setAttribute: (name: string, value: string) => {
        node.setAttribute(name, value)
      },
      getAttribute: (name: string) => node.getAttribute(name),
      removeAttribute: (name: string) => {
        node.removeAttribute(name)
      },
      append: (child: SkewElement) => {
        node.appendChild(nodeOf(child))
      },
    }
    nodes.set(element, node)
    return element
  }

  return {
    factory: { create: (tag: string) => wrap(document.createElement(tag)) },
    mount: {
      append(child: SkewElement): void {
        document.body.appendChild(nodeOf(child))
      },
    },
  }
}

/**
 * bridgeFrom is the renderer's half of the preload seam, and it is the one place
 * a missing or malformed `window.fugaro` is turned into an answer the gate can
 * read: `getCompatibility` is answered with `null` when there is no bridge, or
 * when the member is not a function, and `compatibilityFrom(null)` is the
 * refusal.
 *
 * The method is called **on the object it was found on** (`call(bridge)`), so a
 * bridge whose method needs its own `this` still works -- `contextBridge` copies
 * an object across the two worlds, and the copy's methods are the preload's.
 * What is *not* done here is any checking of the answer: that is the gate's, in
 * `app.ts`, and it is a second implementation of the main process's own check on
 * purpose. This function decides one thing only, and that is whether there is
 * anything to ask.
 */
export function bridgeFrom(fugaro: unknown): CompatibilityBridge {
  const bridge = fugaro as { readonly getCompatibility?: unknown } | null | undefined
  const ask = bridge === null || bridge === undefined ? undefined : bridge.getCompatibility
  if (typeof ask !== 'function') {
    return { getCompatibility: async (): Promise<unknown> => null }
  }
  return {
    getCompatibility: (): Promise<unknown> =>
      Promise.resolve((ask as () => unknown).call(bridge)),
  }
}

/**
 * bootDashboard is what the loaded document runs: read the bridge, hand it to
 * `startConsole`, and let the gate draw. It is a function of a *scope* rather
 * than of globals so that it can be driven -- in `test/unit/startup.test.ts`
 * with a fake document, and in `test/e2e/entry.spec.ts` in the real window.
 *
 * What it does not do is draw anything itself: no frame, no heading, no
 * placeholder. The document already carries the frame (`index.html`'s `<main>`),
 * the refusal and the banner are `startConsole`'s, and a figure is a screen's --
 * which is Task 3.4c's, so the gate's answer is deliberately dropped here.
 */
export async function bootDashboard(scope: {
  readonly fugaro: unknown
  readonly document: Document
}): Promise<void> {
  const surface = domSurface(scope.document)
  await startConsole({
    bridge: bridgeFrom(scope.fugaro),
    mount: surface.mount,
    factory: surface.factory,
    theme: CONSOLE_THEME,
  })
}

// The one moment this file gets in a renderer. `startConsole` answers every
// failure as a compatibility and so does not reject; the `catch` is for a
// document this file cannot draw into at all, and then the right drawing is
// none.
const scope = globalThis as { readonly fugaro?: unknown; readonly document?: unknown }
if (hasDashboardContext(scope)) {
  void bootDashboard({ fugaro: scope.fugaro, document: scope.document as Document }).catch(
    () => undefined,
  )
}
