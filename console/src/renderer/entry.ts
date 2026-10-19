// The renderer's entry point (desktop-console plan Tasks 3.4a, 3.4b and 3.4c;
// design section 5.1 rules 1, 4 and 5).
//
// This is the file the dashboard document loads, and **its first call is
// `startConsole`, and it draws nothing before that promise settles** (plan step
// 5). Everything a screen may draw is behind the gate `startConsole` hands back.
// The refusal screen or the reduced-mode banner is drawn, then the installation
// rail (Task 6.3), then one line saying which installation the console is
// showing, or that none is chosen, and then -- Task 3.4c -- the navigation and
// the screens themselves.
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
//     reaching all the way to the one place a real document exists. The one
//     exception is `domSurface`'s `nodeOf`, which this file uses to attach a
//     click listener to a drawn row: a listener is not a *write*, a screen never
//     sees a node, and the alternative -- a screen that handled its own events --
//     is the interface widening the plan's rule exists to prevent.
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

import { putReading, putText, startConsole } from './app'
import type { CompatibilityBridge, MountLike, ScreenGate } from './app'
import { readNumber, unavailable } from '../shared/availability'
import type { Reading } from '../shared/availability'
import { decodeSnapshot } from '../shared/schema'
import { sanitizeRefusal } from '../shared/sanitize'
import { LIVE_STALE_AFTER_SECONDS, BurnHistory, buildLiveModel, renderLive } from './screens/live'
import type { FeedHealth, LiveModel } from './screens/live'
import { SPEND_DEFAULT_CONTROLS, personView, readSpend, renderSpend } from './screens/spend'
import type { SpendControls, SpendReader } from './screens/spend'
import { buildProjectsModel, renderProjects } from './screens/projects'
import { buildRunModel, renderRun } from './screens/run'
import { refusedState, renderUnavailable } from './screens/degraded'
import { hidesView } from '../shared/version'
import type { ReducedView, SkewElement, SkewFactory, SkewTheme } from '../shared/version'

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
 * DomElement is a wrapped node: the `SkewElement` interface, the real node the
 * wrapper is for, and the children the wrapper made.
 *
 * The node is not part of what a drawing sees -- it is how `domSurface`'s mount
 * finds the element to append, and how this file attaches a listener -- and
 * keeping it on the wrapper rather than in a side table means the two cannot
 * come apart. `childNodes` is here rather than on `SkewElement` because the four
 * screens declare it on their own `ScreenElement` and `SkewElement` is the
 * narrower interface the gate is written against.
 */
export interface DomElement extends SkewElement {
  readonly node: Element
  append(child: SkewElement): void
  childNodes(): readonly DomElement[]
}

/** DomScreenFactory is a `SkewFactory` whose elements are wrapped nodes. It is
 * assignable to every screen's own `ScreenFactory`, which is the point: one
 * factory draws all four screens. */
export interface DomScreenFactory {
  create(tag: string): DomElement
}

/** DomSurface is everything this file needs from a real document: the factory
 * the drawings come from, where a drawing is appended, and the node behind a
 * drawn element. */
export interface DomSurface {
  readonly factory: DomScreenFactory
  readonly mount: MountLike
  /** nodeOf is the real node behind an element this factory made. It throws for
   * anything else, like `append` below, because its only caller is this file's
   * own listener wiring. */
  nodeOf(element: SkewElement): Element
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
export function domSurface(document: Document): DomSurface {
  const nodes = new WeakMap<SkewElement, Element>()

  const nodeOf = (element: SkewElement): Element => {
    const node = nodes.get(element)
    if (node === undefined) {
      throw new Error('the console drew an element that is not this document’s')
    }
    return node
  }

  const wrap = (node: Element): DomElement => {
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
      childNodes: () => [...node.children].map((child) => wrap(child)),
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
    nodeOf,
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
 * InstallationBridge is the one other method of the dashboard's bridge this
 * file uses (Task 3.4b, step 6): `installation.current`, the store's answer for
 * which installation the console is showing. Described structurally, for the
 * reason `CompatibilityBridge` is.
 */
export interface InstallationBridge {
  getInstallation(): Promise<unknown>
}

/** installationBridgeFrom is `bridgeFrom`'s twin for `getInstallation`: a
 * missing bridge or member answers `null`, which draws the console's own
 * "could not read" sentence. */
export function installationBridgeFrom(fugaro: unknown): InstallationBridge {
  const bridge = fugaro as { readonly getInstallation?: unknown } | null | undefined
  const ask = bridge === null || bridge === undefined ? undefined : bridge.getInstallation
  if (typeof ask !== 'function') return { getInstallation: async (): Promise<unknown> => null }
  return {
    getInstallation: (): Promise<unknown> => Promise.resolve((ask as () => unknown).call(bridge)),
  }
}

/**
 * methodOf finds one method of the bridge and binds it to the object it was
 * found on, so a method that needs its own `this` still works. It answers `null`
 * when there is no bridge, or when the member is not a function -- the same
 * "there is nothing to ask" answer `bridgeFrom` gives.
 */
function methodOf(fugaro: unknown, name: string): ((args: unknown) => unknown) | null {
  const bridge = fugaro as Record<string, unknown> | null | undefined
  const member = bridge === null || bridge === undefined ? undefined : bridge[name]
  if (typeof member !== 'function') return null
  return (args: unknown) => (member as (value: unknown) => unknown).call(bridge, args)
}

/**
 * ReadBridge is the seven reads the screens make (Task 3.4c), named one by one
 * for the reason `CompatibilityBridge` names one: this file should reach for
 * exactly the operations it draws, and a reader should be able to see which
 * those are without reading the preload.
 *
 * Every method takes one argument object -- `ipc.ts`'s validated arguments, in
 * the shape the operation's own `args` declares -- and answers whatever the
 * channel answered, which is a `ConsoleReply` and is checked by `replyData`
 * before any of it reaches a screen.
 */
export interface ReadBridge {
  subscribeLive(args: Readonly<Record<string, unknown>>): Promise<unknown>
  showBudget(args: Readonly<Record<string, unknown>>): Promise<unknown>
  runReport(args: Readonly<Record<string, unknown>>): Promise<unknown>
  listRuns(args: Readonly<Record<string, unknown>>): Promise<unknown>
  diagnoseRun(args: Readonly<Record<string, unknown>>): Promise<unknown>
  readRunLogs(args: Readonly<Record<string, unknown>>): Promise<unknown>
  runLogLink(args: Readonly<Record<string, unknown>>): Promise<unknown>
}

/** readBridgeFrom is the seven reads over the structural bridge. A missing
 * member answers `null` rather than rejecting, and so does a member that fails
 * to answer -- by throwing before it returns, or by returning a promise that
 * rejects, which is what `invoke` does on a channel with nothing behind it.
 * Both are "the console could not read this", which `replyData` turns into the
 * sentence every screen draws in place of a figure. */
export function readBridgeFrom(fugaro: unknown): ReadBridge {
  const ask = (name: string, args: Readonly<Record<string, unknown>>): Promise<unknown> => {
    const member = methodOf(fugaro, name)
    if (member === null) return Promise.resolve(null)
    try {
      // The rejection is the half a `try`/`catch` around the call cannot see:
      // the call returned before it failed. Left alone it is an unhandled
      // rejection and a screen that silently keeps whatever was drawn before
      // it, so the read that failed to answer answers `null` like the rest.
      return Promise.resolve(member(args)).catch(() => null)
    } catch {
      return Promise.resolve(null)
    }
  }
  return {
    subscribeLive: (args) => ask('subscribeLive', args),
    showBudget: (args) => ask('showBudget', args),
    runReport: (args) => ask('runReport', args),
    listRuns: (args) => ask('listRuns', args),
    diagnoseRun: (args) => ask('diagnoseRun', args),
    readRunLogs: (args) => ask('readRunLogs', args),
    runLogLink: (args) => ask('runLogLink', args),
  }
}

/** INSTALLATION_UNREADABLE is the console's sentence for an answer to
 * `installation.current` it will not draw from. */
export const INSTALLATION_UNREADABLE = 'the console could not read which installation it is showing'

/**
 * READ_UNREADABLE is the console's sentence for an answer to a read that is not
 * a `ConsoleReply` at all: a bridge with no such method, a call that rejected,
 * or a value of some other shape. The console names the read it could not make
 * and draws no figure -- the same rule `RENDERER_UNREADABLE` states for the
 * handshake, and for the same reason.
 */
export const READ_UNREADABLE = 'the console could not read the answer to this read'

/** UNDRAWN is what a field the sanitizer refused is drawn as. */
const UNDRAWN = 'a line the console will not draw'

/** textOf is one server string as the dashboard draws it: through
 * `sanitizeRefusal`, like every sentence the picker draws, and set as
 * `textContent` only. */
function textOf(raw: unknown, field: string): string {
  const result = sanitizeRefusal(raw, field)
  return result.ok ? result.text : UNDRAWN
}

/**
 * ReadAnswer is one bridge reply after `replyData` has looked at it: the data,
 * or the sentence the console draws in place of a screen.
 */
export type ReadAnswer =
  | { readonly ok: true; readonly data: unknown }
  | { readonly ok: false; readonly sentence: string }

/** DocumentAnswer is `documentOf`'s answer: a document, or the sentence. */
export type DocumentAnswer =
  | { readonly ok: true; readonly data: Readonly<Record<string, unknown>> }
  | { readonly ok: false; readonly sentence: string }

/**
 * replyData checks the envelope every operation answers with (`ConsoleReply` in
 * `src/main/ipc.ts`): `ok: true` and a `data` field, or `ok: false` and a
 * non-empty `reason` string. Anything else -- `null`, a number, an object with
 * neither flag, an `ok: false` with no sentence -- is `READ_UNREADABLE`, and it
 * is a refusal rather than an empty document: a screen handed `{}` for an answer
 * nobody could read would draw zeros nobody measured, which is the one outcome
 * design section 6.1 rule 1 forbids.
 *
 * The check is written here rather than imported from the preload or the main
 * process for the reason `compatibilityFrom` is: the renderer's copy is what
 * still holds if the other two are bypassed, and the main process never
 * *constructs* a reply this would refuse -- this is the renderer's own door.
 */
export function replyData(reply: unknown): ReadAnswer {
  const envelope = typeof reply === 'object' && reply !== null ? (reply as Record<string, unknown>) : null
  if (envelope === null) return { ok: false, sentence: READ_UNREADABLE }
  if (envelope['ok'] === true) {
    return Object.prototype.hasOwnProperty.call(envelope, 'data')
      ? { ok: true, data: envelope['data'] }
      : { ok: false, sentence: READ_UNREADABLE }
  }
  const reason = envelope['reason']
  if (envelope['ok'] === false && typeof reason === 'string' && reason.trim() !== '') {
    return { ok: false, sentence: reason }
  }
  return { ok: false, sentence: READ_UNREADABLE }
}

/**
 * documentOf is one one-shot read's document: the `Snapshot` the operation
 * answered with, and the `document` inside it, which must be a JSON object.
 *
 * A read that answered with something that is not a snapshot, or a snapshot
 * whose document is not an object, is a refusal rather than an empty document --
 * the same rule `replyData` states, one layer in. */
export function documentOf(reply: unknown): DocumentAnswer {
  const answer = replyData(reply)
  if (!answer.ok) return answer
  const data = answer.data
  if (typeof data !== 'object' || data === null || Array.isArray(data)) {
    return { ok: false, sentence: READ_UNREADABLE }
  }
  const document = (data as Record<string, unknown>)['document']
  if (typeof document !== 'object' || document === null || Array.isArray(document)) {
    return { ok: false, sentence: READ_UNREADABLE }
  }
  return { ok: true, data: document as Readonly<Record<string, unknown>> }
}

/** refusedScreen is the one drawing for every state with nothing to draw: a
 * refusal the CLI printed, an answer the console could not read, or a document
 * `schema.ts` refused. `refusedState` is where the CLI's sentence goes through
 * `sanitizeRefusal` (design section 6.3: the remedy survives, a token does not),
 * and `renderUnavailable` draws no figure at all -- which is what makes "never a
 * zero" a property of the screen rather than a promise about the caller. */
function refusedScreen(
  factory: DomScreenFactory,
  operation: string,
  sentence: string,
  theme: SkewTheme,
): SkewElement {
  return renderUnavailable(factory, refusedState(operation, { sentence }), theme)
}

/**
 * drawInstallation is the dashboard's statement of which installation it is
 * showing (plan 3.4b, steps 1, 4 and 6; review S5). It draws **no figure**:
 *
 *   - none chosen: D13's no-installation sentence, and -- when the store's file
 *     was refused -- the console's sentence naming the file's path and why;
 *   - one chosen: its project, its binary path, and the version and mode the
 *     handshake read for it (`unknown` when nothing has read it).
 *
 * Every string is the main process's, drawn through `sanitizeRefusal`; an
 * answer of any other shape is the console's own "could not read" sentence.
 */
export function drawInstallation(factory: SkewFactory, reply: unknown): SkewElement {
  const section = factory.create('section')
  section.setAttribute('class', 'console-installation')
  const line = (className: string, text: string): void => {
    const element = factory.create('p')
    element.setAttribute('class', className)
    element.textContent = text
    section.append(element)
  }
  const envelope = typeof reply === 'object' && reply !== null ? (reply as Record<string, unknown>) : null
  const data = envelope !== null && envelope['ok'] === true ? envelope['data'] : null
  const value = typeof data === 'object' && data !== null && !Array.isArray(data) ? (data as Record<string, unknown>) : null
  if (value === null || typeof value['chosen'] !== 'boolean') {
    section.setAttribute('data-installation', 'unreadable')
    line('installation-unreadable', INSTALLATION_UNREADABLE)
    return section
  }
  if (value['chosen'] === false) {
    section.setAttribute('data-installation', 'none')
    line('installation-none', textOf(value['sentence'], 'the no-installation sentence'))
    if (value['refusal'] !== undefined) {
      line(
        'installation-store-refusal',
        `${textOf(value['path'], 'the installations file’s path')} could not be read: ${textOf(value['refusal'], 'the installations file’s refusal')}`,
      )
    }
    return section
  }
  section.setAttribute('data-installation', 'chosen')
  const mode = textOf(value['mode'], 'the installation’s mode')
  section.setAttribute('data-mode', mode)
  line('installation-project', textOf(value['project'], 'the installation’s project'))
  line('installation-binary', textOf(value['binary'], 'the installation’s binary'))
  const version = value['cliVersion']
  line(
    'installation-version',
    `${typeof version === 'string' ? textOf(version, 'the installation’s version') : 'version not read'} · ${mode}`,
  )
  return section
}

// ---------------------------------------------------------------------------
// The installation rail (desktop-console plan Task 6.3; design section 4.1).
// ---------------------------------------------------------------------------

/** RAIL_CLASS and the eleven below it are the rail's **twelve hooks on thirteen
 * elements** (design sections 2.1 and 4.1): `RAIL_ADD_CLASS` names the foot row
 * and the button inside it both, because a row and the control in it are one
 * region and the class names the region. The rules that select them are
 * `theme/base.css`'s, and they land in this task's commit with these elements --
 * a hook and the rule that selects it move together, because a stylesheet
 * written against a tree nothing draws is a rule nothing can see. */
export const RAIL_CLASS = 'console-rail'
export const RAIL_HEAD_CLASS = 'console-rail-head'
export const RAIL_TITLE_CLASS = 'console-rail-title'
export const RAIL_COLLAPSE_CLASS = 'console-rail-collapse'
export const RAIL_LIST_CLASS = 'console-rail-list'
export const RAIL_ROW_CLASS = 'console-rail-row'
export const RAIL_NAME_CLASS = 'console-rail-name'
export const RAIL_PROJECT_CLASS = 'console-rail-project'
export const RAIL_STATE_CLASS = 'console-rail-state'
export const RAIL_SPEND_CLASS = 'console-rail-spend'
export const RAIL_ADD_CLASS = 'console-rail-add'
export const RAIL_HINT_CLASS = 'console-rail-hint'

/** RAIL_LIST_ID is what the collapse control's `aria-controls` names. It is an
 * `id` and not a hook: `aria-controls` takes an element's id, and no rule in the
 * stylesheet selects it -- the class above is what a rule may select. */
export const RAIL_LIST_ID = 'console-rail-list'

/** RAIL_TITLE, RAIL_HINT and RAIL_ADD_LABEL are the rail's own words, drawn
 * from here rather than from a document: the title names the region, the hint is
 * the design's own sentence about the shortcut keys (Task 6.5's), and the add
 * row's label is the design's. */
export const RAIL_TITLE = 'Installations'
export const RAIL_HINT = 'Switch with the shortcut keys 1 2 3'
export const RAIL_ADD_LABEL = '+ Add installation'

/** RAIL_COLLAPSE_LABEL and RAIL_EXPAND_LABEL are the collapse control's
 * accessible name in the two states it has. The name changes with the state, so
 * what the control *does* next is announced rather than drawn only (design
 * section 4.1); the glyph beside it is the drawn half and is never the only
 * one. */
export const RAIL_COLLAPSE_LABEL = 'Collapse the installations rail'
export const RAIL_EXPAND_LABEL = 'Expand the installations rail'
export const RAIL_COLLAPSE_GLYPH = '‹'
export const RAIL_EXPAND_GLYPH = '›'

/** RailState is the rail's three words, and the glyph each is drawn beside. The
 * word is drawn **and** the glyph is drawn, never one instead of the other
 * (design sections 3.5 and 4.1), and `data-state` carries the same word so the
 * tone has a hook -- the tone is the redundant second channel, never the only
 * carrier. `idle` is the stored installation nothing is being read from: it is
 * the vocabulary's third word and **no read in this phase draws it**, because a
 * row for a stored installation needs a list read the design withholds
 * (design section 4.1, section 11). */
export type RailState = 'running' | 'silent' | 'idle'

/** RAIL_STATE_GLYPHS is the glyph of each word, in the design's own table. */
export const RAIL_STATE_GLYPHS: Readonly<Record<RailState, string>> = Object.freeze({
  running: '●',
  silent: '▲',
  idle: '○',
})

/** RAIL_SPEND_UNREAD is what the spend span says before the console has read a
 * budget: the caps panel's own sentence, so the rail and the panel say the same
 * thing about the same absence (`screens/spend.ts`). It is a **reason with no
 * figure** -- `n/a` and never `$0.00`, which would be a spend nobody measured. */
export const RAIL_SPEND_UNREAD = 'no budget show document was read'

/**
 * railSpend is the one figure the rail's row draws: `global.counted_usd` from a
 * `budget.show` document, which is the figure the caps panel counts its caps
 * against (design section 4.1: "the spend is the console's own budget read ...
 * whose counted and spent dollars section 2.4's caps panel already draws").
 *
 * The path is read here rather than through `screens/spend.ts`'s `capsOf`,
 * because that function builds a whole caps model for a screen and the rail
 * draws one number of it. A document that is not an object, or one with no
 * `global` block, is `n/a` with the field's own reason -- `readNumber`'s rule,
 * one layer out -- and never a zero.
 */
export function railSpend(raw: unknown): Reading {
  const document = typeof raw === 'object' && raw !== null && !Array.isArray(raw) ? (raw as Record<string, unknown>) : null
  const global = document === null ? null : document['global']
  const block = typeof global === 'object' && global !== null && !Array.isArray(global) ? (global as Record<string, unknown>) : null
  return readNumber(block === null ? undefined : block['counted_usd'], 'global.counted_usd')
}

/** RailView is the drawn rail and the three writes this file makes to it. The
 * rail is drawn once and updated in place, rather than redrawn on every frame:
 * it is chrome, and a rail rebuilt under the operator's pointer is a rail that
 * loses its focus. */
export interface RailView {
  readonly root: DomElement
  /** setState writes the state word and its glyph. */
  setState(state: RailState): void
  /** setSpend writes the row's spend, from the console's own budget read. */
  setSpend(reading: Reading): void
}

/**
 * drawRail draws the installation rail: the canvas's one structural addition
 * (design section 4.1, approved by the owner on 2026-10-19), and the grid's
 * first column -- the body's first content child when there is no banner, and
 * its second when there is one, because `startConsole` appends the skew banner
 * to the same mount first. It is appended before the installation line so the
 * tab order is the order a reader sees.
 *
 * **It draws one row per installation the dashboard can read, which is one
 * row.** `installation.current` is the only installation read this document
 * has, it takes no argument and names no id, and its answer is the installation
 * the operator chose -- so the row's name and project are that answer's one
 * label, drawn twice (the canvas's two-line identity), and a console with no
 * installation chosen, or one whose store could not be read, draws **no row**
 * rather than an invented one. A row per *stored* installation needs a bounded
 * list read on this bridge, which the design withholds (section 4.1, section
 * 11); this file does not add one, and the rail makes **no read of its own** --
 * which is also what keeps the entry spec's spawn log at the two spawns a
 * healthy console makes.
 *
 * **The row's state is the rail's own mark, not a field of any read**: the rail
 * knows whether the console is reading this installation now, so it opens at
 * `running` and goes `silent` when that read was refused or the feed has gone
 * quiet. `setState` is how the Live half of this file tells it.
 *
 * **The foot row is drawn and not wired.** "+ Add installation" is the design's
 * control, and the flow it opens is the picker window, which this document
 * cannot open: the picker is reached from the application menu's `Installations…`
 * item (ruling C9), the dashboard's fourteen-operation bridge has no operation
 * that opens a window, and both the design (section 11: no Phase-6 task owns the
 * preload or the IPC allow-list) and this task's Files keep it that way. So the
 * control is drawn where the design draws it, with the label the design gives
 * it, and the click that would open the picker is the one thing this task
 * cannot add -- said here rather than papered over with a handler that does
 * nothing. The rows are the same shape for a different reason: a row's
 * `aria-selected` and its keys are Task 6.5's, and a row selects nothing today
 * (the console's one choose operation is the picker's).
 */
export function drawRail(surface: DomSurface, reply: unknown): RailView {
  const factory = surface.factory
  const rail = factory.create('aside')
  rail.setAttribute('class', RAIL_CLASS)

  const head = factory.create('div')
  head.setAttribute('class', RAIL_HEAD_CLASS)
  const title = factory.create('p')
  title.setAttribute('class', RAIL_TITLE_CLASS)
  title.textContent = RAIL_TITLE
  const collapse = factory.create('button')
  collapse.setAttribute('class', RAIL_COLLAPSE_CLASS)
  collapse.setAttribute('type', 'button')
  collapse.setAttribute('aria-controls', RAIL_LIST_ID)
  collapse.setAttribute('aria-expanded', 'true')
  collapse.setAttribute('aria-label', RAIL_COLLAPSE_LABEL)
  collapse.textContent = RAIL_COLLAPSE_GLYPH
  head.append(title)
  head.append(collapse)

  const list = factory.create('div')
  list.setAttribute('class', RAIL_LIST_CLASS)
  list.setAttribute('id', RAIL_LIST_ID)

  const foot = factory.create('div')
  foot.setAttribute('class', RAIL_ADD_CLASS)
  const add = factory.create('button')
  add.setAttribute('class', RAIL_ADD_CLASS)
  add.setAttribute('type', 'button')
  add.textContent = RAIL_ADD_LABEL
  foot.append(add)

  const hint = factory.create('p')
  hint.setAttribute('class', RAIL_HINT_CLASS)
  hint.textContent = RAIL_HINT

  // The one row, when the dashboard can read an installation to draw one for.
  // Its spans exist either way, so the two setters below are the same code in
  // both cases -- a rail with no row has nothing to write to, and writing to a
  // span that is not in the tree is a no-op rather than a branch.
  const row = factory.create('button')
  row.setAttribute('class', RAIL_ROW_CLASS)
  row.setAttribute('type', 'button')
  const name = factory.create('span')
  name.setAttribute('class', RAIL_NAME_CLASS)
  const project = factory.create('span')
  project.setAttribute('class', RAIL_PROJECT_CLASS)
  const state = factory.create('span')
  state.setAttribute('class', RAIL_STATE_CLASS)
  const spend = factory.create('span')
  spend.setAttribute('class', RAIL_SPEND_CLASS)

  const envelope = typeof reply === 'object' && reply !== null ? (reply as Record<string, unknown>) : null
  const data = envelope !== null && envelope['ok'] === true ? envelope['data'] : null
  const value = typeof data === 'object' && data !== null && !Array.isArray(data) ? (data as Record<string, unknown>) : null
  const readable = value !== null && value['chosen'] === true

  let word: RailState = 'running'
  const writeState = (): void => {
    if (!readable) return
    state.textContent = `${RAIL_STATE_GLYPHS[word]} ${word}`
    state.setAttribute('data-state', word)
  }
  writeState()

  if (readable && value !== null) {
    // One label, two spans: `ActiveInstallation` carries no display name, so the
    // row's identity is the project it names, drawn as the name and again as the
    // small-text line under it.
    putText(name, value['project'], 'label', 'the installation’s project')
    putText(project, value['project'], 'label', 'the installation’s project')
    putReading(spend, unavailable(RAIL_SPEND_UNREAD), 'usd')
    row.append(name)
    row.append(project)
    row.append(state)
    row.append(spend)
    list.append(row)
  }

  rail.append(head)
  rail.append(list)
  rail.append(foot)
  rail.append(hint)

  // The collapse control is this task's, so its behaviour is too: it flips the
  // rail's `data-collapsed`, which is the one rule the 216 px and 56 px widths
  // are selected by, and it keeps `aria-expanded` and its own name in step with
  // what it drew. Nothing else about the rail moves -- a collapsed rail is the
  // same tree, drawn narrow.
  let collapsed = false
  surface.nodeOf(collapse).addEventListener('click', () => {
    collapsed = !collapsed
    rail.setAttribute('data-collapsed', collapsed ? 'true' : 'false')
    collapse.setAttribute('aria-expanded', collapsed ? 'false' : 'true')
    collapse.setAttribute('aria-label', collapsed ? RAIL_EXPAND_LABEL : RAIL_COLLAPSE_LABEL)
    collapse.textContent = collapsed ? RAIL_EXPAND_GLYPH : RAIL_COLLAPSE_GLYPH
  })

  return {
    root: rail,
    setState: (next: RailState): void => {
      word = next
      writeState()
    },
    setSpend: (reading: Reading): void => {
      if (!readable) return
      // The title is cleared first: `putReading` writes one only for a reading
      // with no value, and the rail is the one drawing this file *updates* in
      // place rather than redrawing -- so a reason left over from the `n/a` this
      // span drew a moment ago would sit under a figure that is now measured.
      spend.removeAttribute('title')
      putReading(spend, reading, 'usd')
    },
  }
}

// ---------------------------------------------------------------------------
// The dashboard (desktop-console plan Task 3.4c).
// ---------------------------------------------------------------------------

/**
 * LIVE_POLL_MS is the Live screen's poll interval: 1,000 ms, which is `watch`'s
 * own document floor. The surface is `invoke`-only -- there is no pushed channel
 * (design section 5.1 rule 3) -- so a live screen is a poll, and this is the
 * rate the design names.
 */
export const LIVE_POLL_MS = 1000

/** VIEW_BUTTON_CLASS, VIEW_PANEL_CLASS and VIEW_NAV_CLASS are the console's own
 * names for the three elements the navigation is made of. They are constants
 * because a spec reads them. */
export const VIEW_NAV_CLASS = 'console-views'
export const VIEW_BUTTON_CLASS = 'console-view'
export const VIEW_PANEL_CLASS = 'console-panel'

/**
 * VIEWS is the navigation, and it lists **only views `drawScreen` would draw**:
 * Live is always there (design C10: `live.watch` is never withheld, so a reduced
 * console still has a live screen), and each of the other two names the
 * `ReducedView` the gate knows it by. A mode that withholds a view does not
 * merely fail to mount it -- the entry does not offer it, and the read behind it
 * is never made.
 *
 * Run detail is deliberately **not** here: it is reached from a run row (a run
 * is what names it), and until the operator picks one there is no run to draw.
 */
export const VIEWS: readonly {
  readonly id: string
  readonly label: string
  readonly view: ReducedView | null
}[] = Object.freeze([
  { id: 'live', label: 'Live', view: null },
  { id: 'spend', label: 'Spend', view: 'spend' },
  { id: 'projects', label: 'Projects', view: 'caps' },
])

/**
 * Drawn is what a screen's `render*` answers with: the `ScreenElement` the
 * screens declare, structurally. It is written out here because the four screens
 * declare it four times -- `live.ts`, `spend.ts`, `projects.ts` and `run.ts` are
 * siblings and none owns the others -- and this file walks the tree rather than
 * drawing into it.
 */
interface Drawn extends SkewElement {
  childNodes(): readonly Drawn[]
}

/** elementsUnder is every element of a drawn tree, the root included, in the
 * order it was appended. It walks `childNodes()` -- the interface the screens
 * declare -- rather than the document, so the walk is over what was *drawn*
 * rather than over whatever else is on the page. */
function elementsUnder(root: Drawn): readonly Drawn[] {
  const found: Drawn[] = [root]
  for (const child of root.childNodes()) found.push(...elementsUnder(child))
  return found
}

/** classed finds the drawn elements carrying one class. */
function classed(root: Drawn, className: string): readonly Drawn[] {
  return elementsUnder(root).filter((element) =>
    (element.getAttribute('class') ?? '').split(/\s+/).includes(className),
  )
}

/** nowStamp is the console's own clock, RFC 3339 UTC, in the same shape
 * `src/source/cli.ts` stamps a snapshot with -- so the two can be subtracted
 * without either being read as a local time. */
function nowStamp(): string {
  return new Date().toISOString().replace(/\.\d{3}Z$/, 'Z')
}

/**
 * feedHealthOf is the console's own reading of how long ago the frame it holds
 * arrived (design section 6.3's stale condition; `FeedHealth` in `live.ts`).
 *
 * Two clocks meet here and only one of them decides anything: `receivedAt` is
 * the **console's** stamp of the frame's arrival, taken in the main process, so
 * the silence is a difference between two readings of one clock and no offset
 * between the CLI's clock and the console's can move it. An unparseable or
 * absent stamp is no reading at all -- `null`, which draws the unavailable mark
 * rather than a zero.
 */
export function feedHealthOf(receivedAt: unknown, at: string): FeedHealth {
  const since = typeof receivedAt === 'string' ? receivedAt : null
  const when = since === null ? Number.NaN : Date.parse(since)
  const now = Date.parse(at)
  const seconds =
    since === null || Number.isNaN(when) || Number.isNaN(now)
      ? null
      : Math.max(0, Math.round((now - when) / 1000))
  return {
    since,
    at,
    seconds,
    stale: seconds !== null && seconds > LIVE_STALE_AFTER_SECONDS,
  }
}

/**
 * killWithheld is the Live model with the kill state taken out (design section
 * 5.1 rule 4 and C10). It is a value rather than a flag threaded through the
 * drawing: `renderLive` draws a kill banner exactly when the model carries one,
 * so a model with none cannot draw one however the screen is written later.
 *
 * Both places the banner lives are cleared -- the project's own bar
 * (`LiveModel.total.kill`) and each repository's (`LiveRepoRow.kill`) -- because
 * a screen that cleared one of the two would still be showing the kill switch.
 */
export function killWithheld(model: LiveModel): LiveModel {
  return {
    ...model,
    total: { ...model.total, kill: null },
    repos: model.repos.map((repo) => ({ ...repo, kill: null })),
  }
}

/** DashboardScope is what `bootDashboard` is driven with: the bridge, and the
 * document. It is a scope rather than globals so the wiring can be driven under
 * `vitest` with a fake document and a fake bridge. */
export interface DashboardScope {
  readonly fugaro: unknown
  readonly document: Document
}

/**
 * bootDashboard is what the loaded document runs: read the bridge, hand it to
 * `startConsole`, and let the gate draw. It is a function of a *scope* rather
 * than of globals so that it can be driven -- in
 * `test/unit/entry-wiring.test.ts` with a fake document and a fake bridge, and
 * in `test/e2e/entry.spec.ts` in the real window.
 *
 * What it draws, and what it appends to the mount (`document.body`) in that
 * order, so DOM order and tab order agree with the layout: `startConsole`'s
 * refusal or banner first (it appends that itself), then the installation rail
 * (`drawRail`, Task 6.3), then the line saying which installation the console is
 * showing (`drawInstallation`), and then -- `mountDashboard`'s -- the navigation
 * and the panel with the Live screen in it. A refused console draws the refusal
 * and **stops**: no rail, no installation line, no navigation, no panel, and not
 * one read -- which is the whole of D7 from this side.
 */
export async function bootDashboard(scope: DashboardScope): Promise<void> {
  const surface = domSurface(scope.document)
  const gate = await startConsole({
    bridge: bridgeFrom(scope.fugaro),
    mount: surface.mount,
    factory: surface.factory,
    theme: CONSOLE_THEME,
  })
  if (gate.compatibility.mode === 'refused') return
  const bridge = readBridgeFrom(scope.fugaro)
  let installation: unknown
  try {
    installation = await installationBridgeFrom(scope.fugaro).getInstallation()
  } catch {
    installation = null
  }
  const rail = drawRail(surface, installation)
  surface.mount.append(rail.root)
  surface.mount.append(drawInstallation(surface.factory, installation))
  await mountDashboard({ document: scope.document, surface, gate, bridge, rail })
}

/** DashboardDeps is what the dashboard half of `bootDashboard` needs, named so
 * that the wiring can be read -- and driven -- apart from the startup. */
interface DashboardDeps {
  readonly document: Document
  readonly surface: DomSurface
  readonly gate: ScreenGate
  readonly bridge: ReadBridge
  /** rail is the installation rail, drawn before the dashboard was mounted and
   * updated from here: the Live half owns its state word, and the two views that
   * read the budget fill its spend. */
  readonly rail: RailView
}

/**
 * mountDashboard builds the navigation and the panel, draws the default view,
 * and starts the Live poll. It answers when the **first** Live read has been
 * drawn, so a caller that awaits `bootDashboard` is looking at a dashboard with
 * figures in it rather than at an empty panel that fills in later.
 */
async function mountDashboard(deps: DashboardDeps): Promise<void> {
  const { document, surface, gate, bridge, rail } = deps
  const factory = surface.factory
  const theme = CONSOLE_THEME

  // The Live screen's own state, kept across polls: the burn window accumulates
  // samples, so it must outlive a drawing. A fresh one per poll would be a
  // one-bar sparkline forever.
  const history = new BurnHistory()
  const live: {
    drawn: Drawn | null
    inFlight: boolean
    timer: ReturnType<typeof setInterval> | null
  } = { drawn: null, inFlight: false, timer: null }

  /** panel is where every screen is drawn. It is created here and appended
   * after the navigation below, so the page reads nav-then-screens. */
  const panel = factory.create('div')
  panel.setAttribute('class', VIEW_PANEL_CLASS)

  /** replace clears the panel and draws one screen into it. The panel is this
   * file's own element, so clearing it is `replaceChildren` on the node behind
   * it -- a write no screen makes, because a screen draws one tree and owns none
   * of the page. */
  const replace = (drawn: SkewElement): void => {
    surface.nodeOf(panel).replaceChildren(surface.nodeOf(drawn))
  }

  const refuse = (operation: string, sentence: string): void => {
    replace(refusedScreen(factory, operation, sentence, theme))
  }

  // ---- The views --------------------------------------------------------

  /** currentView is the view id on screen; `'run'` is not in `VIEWS`, because
   * Run detail is reached from a run row rather than offered in the navigation,
   * and selecting one of the offered views is how the operator leaves it. */
  let currentView = 'live'

  /** drawnViews is the navigation's buttons by view id, so `show` can mark the
   * current one without walking the tree again. */
  const buttons = new Map<string, DomElement>()

  /**
   * syncPolling is the one place the interval is started and stopped, so the two
   * conditions -- the Live view is the one on screen, and the window is visible
   * -- cannot be applied in one place and forgotten in the other. A hidden view
   * or a hidden window is not a reason to keep spawning reads nobody is looking
   * at.
   */
  const syncPolling = (): void => {
    const wanted = currentView === 'live' && scopeVisible()
    if (wanted && live.timer === null) {
      live.timer = setInterval(() => {
        void pollLive()
      }, LIVE_POLL_MS)
      return
    }
    if (!wanted && live.timer !== null) {
      clearInterval(live.timer)
      live.timer = null
    }
  }

  /**
   * scopeVisible is whether this window is visible at all. `visibilityState` is
   * `'visible'` in a shown window and `'hidden'` when the window is minimized or
   * occluded; a document that does not carry the property at all (a fake one, in
   * a unit test) is treated as visible, which is the state the console is in
   * when it has just opened a window.
   */
  const scopeVisible = (): boolean => {
    const state = (document as { readonly visibilityState?: unknown }).visibilityState
    return state === undefined || state === 'visible'
  }

  /**
   * generation is what "the operator has moved on" is written as. Every screen
   * is drawn from a read, and a read is a message round -- `spend.report` is a
   * whole report -- so an answer can land after the operator has chosen another
   * view. The panel is *their* view: a drawing that landed late would replace
   * what they chose with what they left, which is a screen naming one thing and
   * holding another.
   *
   * `claim` takes the next generation and answers it; a drawer keeps what it was
   * given and asks `stale` before it draws anything. Every door that changes the
   * view claims, so an answer for a view nobody is looking at cannot be drawn.
   */
  let generation = 0
  const claim = (): number => {
    generation += 1
    return generation
  }
  const stale = (mine: number): boolean => mine !== generation

  /**
   * markCurrent is the navigation's one writer: it records which view is on
   * screen, marks its button, and invalidates whatever was in flight for the
   * view before it. Run detail is a view without a button -- it is reached from
   * a run row -- so every button is unmarked while it is up, which is the truth
   * about the navigation: none of the offered views is the one on screen.
   */
  const markCurrent = (id: string): void => {
    claim()
    for (const [key, button] of buttons) {
      button.setAttribute('aria-current', key === id ? 'true' : 'false')
    }
    currentView = id
    syncPolling()
  }

  /** show switches the panel to one view. The Live screen is not re-read here --
   * its model is the one the last poll drew, which is the point of a live
   * screen -- and the other two read on entry. */
  const show = (id: string): void => {
    markCurrent(id)
    if (id === 'live') {
      if (live.drawn !== null) replace(live.drawn)
      return
    }
    if (id === 'spend') void showSpend()
    if (id === 'projects') void showProjects()
  }

  const nav = factory.create('nav')
  nav.setAttribute('class', VIEW_NAV_CLASS)
  surface.mount.append(nav)

  for (const entry of VIEWS) {
    // The gate is asked before the button exists: a view this mode withholds is
    // not offered at all, and the read behind it is never made.
    if (entry.view !== null && hidesView(gate.compatibility, entry.view)) continue
    const button = factory.create('button')
    button.setAttribute('class', VIEW_BUTTON_CLASS)
    button.setAttribute('type', 'button')
    button.setAttribute('data-view', entry.id)
    // The view on screen is marked from the first paint, not only after the
    // first click: `show` writes this attribute on every switch, and a
    // navigation that carried it only once something had been clicked would
    // draw a console whose current view is unmarked.
    button.setAttribute('aria-current', entry.id === currentView ? 'true' : 'false')
    button.textContent = entry.label
    nav.append(button)
    buttons.set(entry.id, button)
    surface.nodeOf(button).addEventListener('click', () => {
      show(entry.id)
    })
  }

  surface.mount.append(panel)

  // ---- Live -------------------------------------------------------------

  /**
   * pollLive makes one `live.watch` read and draws it. **At most one call is in
   * flight**: the flag is set before the await and cleared in a `finally`, so a
   * slow read cannot be overtaken by the next tick of the interval. The flag is
   * this file's rather than the bridge's because the bridge is `invoke`-only and
   * an unanswered call is invisible to it.
   */
  const pollLive = async (): Promise<void> => {
    if (live.inFlight) return
    live.inFlight = true
    try {
      drawLive(await bridge.subscribeLive({}))
    } catch {
      // The same rule `drawLive` keeps: a poll that fails while another view is
      // on screen does not replace the view the operator chose.
      rail.setState('silent')
      if (currentView === 'live') refuse('live.watch', READ_UNREADABLE)
    } finally {
      live.inFlight = false
    }
  }

  /**
   * drawLive decodes one `live.watch` answer and draws it. Every door out of it
   * is a refusal screen and none of them is an empty one: a reply the console
   * cannot read, a document `schema.ts` refuses (the `Skew`'s own sentence,
   * which is written in the console's words), and a feed that answered nothing.
   */
  const drawLive = (reply: unknown): void => {
    // The Live screen is the poll's own view. A frame that lands while another
    // view is on screen -- the operator opened a run, or moved to Spend while
    // this read was outstanding -- is a drawing nobody is looking at, and it
    // would replace the screen they did open. The interval is stopped by
    // `syncPolling`; this is the read that was already on its way.
    if (currentView !== 'live') return
    const answer = replyData(reply)
    if (!answer.ok) {
      rail.setState('silent')
      refuse('live.watch', answer.sentence)
      return
    }
    const envelope = answer.data as Record<string, unknown>
    const decoded = decodeSnapshot(envelope['document'], 'live.watch')
    if (!decoded.ok) {
      rail.setState('silent')
      refuse('live.watch', decoded.skew.because)
      return
    }
    const feed = feedHealthOf(envelope['receivedAt'], nowStamp())
    // The rail's state word is this read's own: the console is reading this
    // installation now (`running`), unless the frame it holds has gone quiet --
    // which is the second of the two conditions the design gives `silent`
    // (section 4.1). A read that was refused sets it above.
    rail.setState(feed.stale ? 'silent' : 'running')
    const model = buildLiveModel(decoded.value, history, feed)
    // The kill banner is the `kill` view. It is asked for through the gate --
    // the one door -- with an empty drawing, because what a reduced console
    // withholds here is not a screen but one panel of the Live screen: the
    // answer decides whether the model the screen is handed carries the kill
    // state at all.
    const mayDrawKill = gate.drawScreen('kill', () => undefined) === 'drawn'
    const drawn = renderLive(factory, mayDrawKill ? model : killWithheld(model), theme)
    live.drawn = drawn
    replace(drawn)
    wireRuns(drawn, model)
  }

  /**
   * wireRuns makes each drawn run row a door to the Run screen (plan 3.4c step
   * 2: "Run detail: reached from a run row").
   *
   * The row's own drawn id is the only thing read. `live-run-id` is written
   * through `putText`, so a run id the sanitizer refused is drawn as the
   * unavailable mark and matches no run in the model -- and a row whose id
   * cannot be drawn is a row with nothing to ask about, rather than one that
   * asks about whatever the mark happens to be.
   */
  const wireRuns = (drawn: Drawn, model: LiveModel): void => {
    const known = new Set<string>()
    for (const repo of model.repos) for (const row of repo.runs) known.add(row.run)
    for (const row of model.orphans) known.add(row.run)
    for (const row of classed(drawn, 'live-run')) {
      const id = classed(row, 'live-run-id')[0]?.textContent ?? ''
      if (!known.has(id)) continue
      surface.nodeOf(row).addEventListener('click', () => {
        void showRun(id)
      })
    }
  }

  // ---- Spend ------------------------------------------------------------

  /**
   * showSpend reads the caps once and the report on every control change, which
   * is `readSpend`'s own division: the budget document is the caller's, because
   * it is a different operation and a control change is not a reason to re-read
   * it.
   */
  const showSpend = async (): Promise<void> => {
    const mine = claim()
    let controls: SpendControls = SPEND_DEFAULT_CONTROLS
    let caps: Readonly<Record<string, unknown>> | null = null
    const capsReply = documentOf(await bridge.showBudget({}))
    // The rail's spend is the console's own budget read -- this one, the read
    // the caps panel makes -- and the rail makes none of its own (design section
    // 4.1). It is filled before the staleness check below: the spend is a fact
    // about the installation rather than about the view the operator chose, so a
    // read that landed after they moved on still fills the rail.
    if (capsReply.ok) rail.setSpend(railSpend(capsReply.data))
    if (stale(mine)) return
    if (capsReply.ok) caps = capsReply.data

    const reader: SpendReader = async (args) => {
      const answer = documentOf(await bridge.runReport(args))
      // The sentence travels on the thrown value, which is how `readSpend`
      // reads a refusal (`refusalSentence` takes `cause.sentence`, the shape
      // `CliSource` throws). A read the console could not make at all -- a
      // rejection, an answer that is not a reply -- must be drawn with *that*
      // sentence, not as a read the CLI refused and printed nothing for.
      if (!answer.ok) throw { sentence: answer.sentence }
      return answer.data
    }

    // asked is which report is the latest one asked for. Two control changes in
    // a row are two reports in flight, and the first can answer last: drawn, it
    // would put the earlier range's figures under a picker that marks the later
    // one. Each draw also renders with the controls *it* read with, never with
    // whatever `controls` has become since.
    let asked = 0
    const draw = async (): Promise<void> => {
      const ticket = (asked += 1)
      const readWith = controls
      const outcome = await readSpend(reader, readWith, caps)
      // The report was read for a view that is no longer the one on screen (the
      // operator left Spend), or for controls the operator has changed again.
      if (stale(mine) || ticket !== asked) return
      if (!outcome.ok) {
        refuse('spend.report', outcome.reason)
        return
      }
      const drawn = renderSpend(factory, outcome.model, theme, readWith)
      replace(drawn)
      wireSpendControls(drawn, readWith, (next) => {
        controls = next
        void draw()
      })
    }
    await draw()
  }

  /**
   * wireSpendControls attaches the three controls `renderSpend` draws: the range
   * picker's windows, the breakdown switch's enabled words, and the person knob.
   * Each answers the *next* controls, which the caller re-reads with --
   * `personView` is the screen's own function for the knob's rule (design D15),
   * so the entry does not restate it.
   */
  const wireSpendControls = (
    drawn: Drawn,
    controls: SpendControls,
    change: (next: SpendControls) => void,
  ): void => {
    for (const window of classed(drawn, 'spend-window')) {
      const word = window.textContent ?? ''
      surface.nodeOf(window).addEventListener('click', () => {
        change(Object.freeze({ ...controls, since: word }))
      })
    }
    for (const tab of classed(drawn, 'spend-dim')) {
      if (tab.getAttribute('data-enabled') !== 'true') continue
      const dimension = tab.getAttribute('data-dimension') ?? ''
      surface.nodeOf(tab).addEventListener('click', () => {
        change(Object.freeze({ ...controls, by: dimension as SpendControls['by'] }))
      })
    }
    const knob = classed(drawn, 'spend-persons')[0]
    if (knob !== undefined) {
      const on = knob.getAttribute('data-enabled') !== 'true'
      surface.nodeOf(knob).addEventListener('click', () => {
        change(personView(controls, on))
      })
    }
  }

  // ---- Projects ---------------------------------------------------------

  /**
   * showProjects reads the two documents this console can read for it and hands
   * each one's refusal -- when there is one -- to the screen's own `sentences`,
   * so a panel that has nothing says *why* in the CLI's words rather than in a
   * default this file invented.
   *
   * The two repository-scoped reads are **not made**: `accounts ls` and
   * `secrets ls` both need a repository, and this console has none to name (the
   * installation's project is not a repository). Their panels say so through the
   * screen's own absent sentence, which is the truth about a read the console
   * did not make -- and is not a zero.
   */
  const showProjects = async (): Promise<void> => {
    const mine = claim()
    const sentences: Record<string, string> = {}
    const read = async (operation: string, go: () => Promise<unknown>): Promise<unknown> => {
      const answer = documentOf(await go())
      if (!answer.ok) sentences[operation] = answer.sentence
      return answer.ok ? answer.data : null
    }
    const budget = await read('budget.show', () => bridge.showBudget({}))
    // The Projects view makes the same budget read, and the rail's spend is that
    // read's too: one read serves the panel and the rail.
    if (budget !== null) rail.setSpend(railSpend(budget))
    const runs = await read('runs.list', () => bridge.listRuns({}))
    // Both reads are in, and the operator may have moved on while they were.
    if (stale(mine)) return
    const model = buildProjectsModel({ budget, runs, accounts: null, secrets: null, sentences })
    replace(renderProjects(factory, model, theme))
  }

  // ---- Run detail -------------------------------------------------------

  /**
   * showRun reads one run's diagnosis, its log window and its Cloud console
   * link.
   *
   * **The two document reads decide whether there is a screen at all.** Both are
   * the run's own record -- `diagnose` is what the run did, `logs` is what it
   * said -- and a refused read of either is a run this console cannot draw:
   * handing `null` to the screen instead would draw an empty log window and a
   * ledger of `n/a`, which reads as "this run logged nothing and spent nothing"
   * when what happened is that the console could not ask (Global Constraint 5).
   * So the refusal is drawn, with the CLI's own sentence and the operation it
   * came from, and the first one that refused is the one named.
   *
   * The **link is not one of them**: `logs RUN --url` refuses for a run whose
   * transcript is not linked, which is a fact about the link and not a reason to
   * hide a run that read perfectly well -- `buildRunModel` draws its own
   * sentence for a link that did not arrive.
   */
  const showRun = async (run: string): Promise<void> => {
    // A run is a view like any other: the navigation marks none of its buttons
    // while it is up, and the Live poll stops -- a poll that kept running under
    // it would replace the run the operator opened a second later.
    markCurrent('run')
    const mine = claim()
    const sentences: Record<string, string> = {}
    const read = async (operation: string, go: () => Promise<unknown>): Promise<unknown> => {
      const answer = documentOf(await go())
      if (!answer.ok) sentences[operation] = answer.sentence
      return answer.ok ? answer.data : null
    }
    const diagnosis = await read('run.diagnose', () => bridge.diagnoseRun({ run }))
    const logs = await read('run.logs', () => bridge.readRunLogs({ run }))
    if (stale(mine)) return
    const refused = (['run.diagnose', 'run.logs'] as const)
      .map((operation) => ({ operation, sentence: sentences[operation] }))
      .find((each) => each.sentence !== undefined)
    if (refused !== undefined) {
      refuse(refused.operation, refused.sentence ?? READ_UNREADABLE)
      return
    }

    let logLink: string | null = null
    const link = replyData(await bridge.runLogLink({ run }))
    if (stale(mine)) return
    if (link.ok && typeof link.data === 'string') logLink = link.data
    else if (!link.ok) sentences['run.logLink'] = link.sentence

    replace(renderRun(factory, buildRunModel({ diagnosis, logs, logLink, sentences }), theme))
  }

  // ---- The first read ---------------------------------------------------

  // The first read is not a poll: the screen is drawn once whatever the window's
  // visibility is, so a console that opened behind another window still has the
  // figures it read. The interval is what pauses.
  await pollLive()
  syncPolling()

  document.addEventListener('visibilitychange', () => {
    syncPolling()
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
