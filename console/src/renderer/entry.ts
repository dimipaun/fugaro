// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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
//     **The owner's Q1 ruling of 2026-10-19 settled that question the other
//     way**: the console ships one value set, dark, so `CONSOLE_THEME` keeps the
//     literal it has and nothing here reads a preference at all -- the case that
//     holds this is in `test/unit/entry-wiring.test.ts`, and it scans every
//     renderer source for a media query, a `nativeTheme` query or a
//     `themeChoice()` arriving without a ruling that ships a second set with
//     them. (`data-theme` is *not* in that scan: the screens have stamped the
//     theme they were drawn with on their own roots since they were written, and
//     that attribute is the drawing's record rather than a read of the machine.)
//   - **The keyboard is this file's, and it is the console's first.** Two
//     tablists get the WAI-ARIA behaviour -- the view navigation and the
//     installation rail's list (design section 4.3) -- and the rail's rows are
//     addressable by position with the shortcut keys (design section 4.1:
//     Command-1 to Command-3 on macOS, Control-1 to Control-3 elsewhere, read
//     from the **physical** key so that the operator's layout cannot spell them
//     away). Every write is an
//     attribute (`role`, `aria-selected`, `tabindex`) and no style is written,
//     which is why the CSP and `ElementLike` are untouched; the two events are
//     a `keydown` on a drawn control and one on the document, the second being
//     what "without walking the tab order at all" means.
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
import { ONBOARDING_COPY } from './installations'
import { disabledWrite } from './writes'
import type { CompatibilityBridge, MountLike, ScreenGate } from './app'
import { formatReading, measured, notional, readNumber, unavailable } from '../shared/availability'
import type { Reading } from '../shared/availability'
import { rigorIndex } from '../shared/rigor'
import type { RigorView } from '../shared/rigor'
import { decodeCoordinatorStatus, decodePlanStatus, decodeSnapshot } from '../shared/schema'
import type { PlanStatusView } from '../shared/schema'
import { sanitizeRefusal } from '../shared/sanitize'
import { LIVE_STALE_AFTER_SECONDS, BurnHistory, buildLiveModel, renderLive } from './screens/live'
import type { FeedHealth, LiveModel } from './screens/live'
import {
  SPEND_DEFAULT_CONTROLS,
  capsOf,
  fillSpendCaps,
  fillSpendReport,
  personView,
  readSpend,
  renderSpendFrame,
} from './screens/spend'
import type {
  DrawnSpendFrame,
  PeopleReader,
  SpendControls,
  SpendModel,
  SpendReader,
} from './screens/spend'
import { BUSY_SLOT_CLASS, CACHE_FRESH_MS, CACHE_LIMIT, cacheKey, renderStamp, resultCache, sectionRead } from './section'
import { COORDINATOR_SHAPE, PROJECTS_SHAPE, SPEND_CAPS_SHAPE, SPEND_REPORT_SHAPE } from './skeleton'
import {
  COORDINATOR_BUSY,
  buildCoordinatorModel,
  fillCoordinatorSummary,
  renderCoordinatorFrame,
  repoDetail,
} from './screens/coordinator'
import type { CoordinatorModel, DrawnCoordinatorFrame } from './screens/coordinator'
import { PROJECTS_BUSY, buildProjectsModel, fillProjects, renderProjectsFrame } from './screens/projects'
import type { DrawnProjectsFrame, ProjectsModel } from './screens/projects'
import { buildRunModel, renderRun } from './screens/run'
import { refusedState, renderUnavailable } from './screens/degraded'
import { READING_STILL_MS, renderReading } from './screens/reading'
import type { ReadingTab } from './screens/reading'
import { NO_CHECKOUT_SENTENCE, renderNoCheckout } from './screens/nocheckout'
import {
  launchBytes,
  launchCheck,
  launchCounter,
  launchOutcomeOf,
  renderLaunch,
  renderLaunchOutcome,
} from './screens/launch'
import type { LaunchControl, LaunchFields, LaunchTyped, LaunchView } from './screens/launch'
import {
  GRAPH_PINCH_STEP,
  GRAPH_REFRESH_MS,
  GRAPH_ZOOM_STEP,
  SVG_NAMESPACE_URI,
  graphScreen,
  runIdsIn,
  tabKey,
  tabOf,
} from './screens/graph'
import type { GraphScreen } from './screens/graph'
import type { GraphTab } from './graph/model'
import { SKEW_HEADLINES, hidesView, launchSupported } from '../shared/version'
import type { ReducedView, SkewElement, SkewFactory, SkewTheme } from '../shared/version'
import { NOTICE_HIDE_DETAILS, NOTICE_SHOW_DETAILS } from '../shared/notice'
import type { DrawnNotice } from '../shared/notice'

/**
 * CONSOLE_THEME is the theme every drawing is made for. It is one word rather
 * than a preference read, for the reason in this file's header: the code that
 * would read the operating system's preference is in no task's Files, and a
 * drawing that guessed would be a drawing nobody decided. `'dark'` is the
 * console's own first screen in design section 6.3's mockups.
 *
 * **The owner's Q1 ruling of 2026-10-19 makes that literal the shipped answer
 * rather than a placeholder** (design section 3.6): one value set ships, dark,
 * so there is nothing for this word to be chosen between and no `matchMedia`
 * read, no `change` subscription and no `nativeTheme` query comes with it. A
 * later task that ships a light set changes this *value* under a new ruling, and
 * the case in `test/unit/entry-wiring.test.ts` is what makes that a decision
 * rather than a drift.
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
  /** focus moves the keyboard to this element. It is here rather than on
   * `SkewElement` because a *screen* never moves the focus: the two callers are
   * the tablists' own key handling (design sections 4.1 and 4.3), where a
   * roving `tabindex` that moved the attribute and left the keyboard behind
   * would be a tablist whose ring never follows the arrows. `ElementLike` -- the
   * interface the screens are drawn against -- is untouched by it. */
  focus(): void
}

/** DomScreenFactory is a `SkewFactory` whose elements are wrapped nodes. It is
 * assignable to every screen's own `ScreenFactory`, which is the point: one
 * factory draws all five screens.
 *
 * `createNS` is the second namespace and not a flag on `create`: the graph screen
 * draws its picture as SVG geometry (GV2.3b), and an element made with
 * `createElement` is in the HTML namespace and is not drawn inside an `<svg>` at
 * all. It is the graph's own `GraphFactory` this answers. */
export interface DomScreenFactory {
  create(tag: string): DomElement
  createNS(tag: string): DomElement
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
      focus: (): void => {
        // Both elements this factory makes -- `createElement`'s and
        // `createElementNS`'s -- implement `focus` (`HTMLElement` and
        // `SVGElement` each do), and `focus` is the one member this file needs
        // that `Element`, which is what `nodeOf` answers with, does not declare.
        const focusable = node as Element & { focus(): void }
        focusable.focus()
      },
    }
    nodes.set(element, node)
    return element
  }

  return {
    factory: {
      create: (tag: string) => wrap(document.createElement(tag)),
      createNS: (tag: string) => wrap(document.createElementNS(SVG_NAMESPACE_URI, tag)),
    },
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
 * ReadBridge is the ten reads the screens make (Task 3.4c; console L5b added
 * `statsRuns`, Bitbucket credentials T1.2 added `coordinatorStatus`, GV2.3b
 * added `planStatus`), named one by one
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
  statsRuns(args: Readonly<Record<string, unknown>>): Promise<unknown>
  coordinatorStatus(args: Readonly<Record<string, unknown>>): Promise<unknown>
  diagnoseRun(args: Readonly<Record<string, unknown>>): Promise<unknown>
  readRunLogs(args: Readonly<Record<string, unknown>>): Promise<unknown>
  runLogLink(args: Readonly<Record<string, unknown>>): Promise<unknown>
  planStatus(args: Readonly<Record<string, unknown>>): Promise<unknown>
}

/** readBridgeFrom is the ten reads over the structural bridge. A missing
 * member answers `null` rather than rejecting, and so does a member that fails
 * to answer -- by throwing before it returns, or by returning a promise that
 * rejects, which is what `invoke` does on a channel with nothing behind it.
 * Both are "the console could not read this", which `replyData` turns into the
 * sentence every screen draws in place of a figure. */
function askMember(fugaro: unknown, name: string, args: Readonly<Record<string, unknown>>): Promise<unknown> {
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

export function readBridgeFrom(fugaro: unknown): ReadBridge {
  const ask = (name: string, args: Readonly<Record<string, unknown>>): Promise<unknown> => askMember(fugaro, name, args)
  return {
    subscribeLive: (args) => ask('subscribeLive', args),
    showBudget: (args) => ask('showBudget', args),
    runReport: (args) => ask('runReport', args),
    listRuns: (args) => ask('listRuns', args),
    statsRuns: (args) => ask('statsRuns', args),
    coordinatorStatus: (args) => ask('coordinatorStatus', args),
    diagnoseRun: (args) => ask('diagnoseRun', args),
    readRunLogs: (args) => ask('readRunLogs', args),
    runLogLink: (args) => ask('runLogLink', args),
    planStatus: (args) => ask('planStatus', args),
  }
}

/** RailBridge is the rail's six operations (`installation.*`) over the
 * structural bridge, answered the way `readBridgeFrom` answers: a member that is
 * missing or fails to answer is `null`, which `replyData` draws as a refusal. */
export interface RailBridge {
  listInstallations(args: Readonly<Record<string, unknown>>): Promise<unknown>
  chooseInstallation(args: Readonly<Record<string, unknown>>): Promise<unknown>
  setInstallationCheckout(args: Readonly<Record<string, unknown>>): Promise<unknown>
  removeInstallation(args: Readonly<Record<string, unknown>>): Promise<unknown>
  revealInstallation(args: Readonly<Record<string, unknown>>): Promise<unknown>
  openInstallations(args: Readonly<Record<string, unknown>>): Promise<unknown>
}

export function railBridgeFrom(fugaro: unknown): RailBridge {
  const ask = (name: string, args: Readonly<Record<string, unknown>>): Promise<unknown> => askMember(fugaro, name, args)
  return {
    listInstallations: (args) => ask('listInstallations', args),
    chooseInstallation: (args) => ask('chooseInstallation', args),
    setInstallationCheckout: (args) => ask('setInstallationCheckout', args),
    removeInstallation: (args) => ask('removeInstallation', args),
    revealInstallation: (args) => ask('revealInstallation', args),
    openInstallations: (args) => ask('openInstallations', args),
  }
}

/**
 * LaunchBridge is the one **write** the dashboard makes: `run.launch`, the Live
 * view's launch control (V2.5.1b). It is a bridge of its own rather than a tenth
 * member of `ReadBridge`, because that interface is the reads a screen is drawn
 * from and this one spends money -- a reader looking for what this console can
 * *do* should not have to find it among the things it can look at.
 *
 * It is answered the way `readBridgeFrom` answers: a member that is missing, or
 * that fails to answer at all, is `null` rather than a rejection -- and the
 * launch form draws that as a reply it could not read, which is the state that
 * leaves the operator with the fields they typed and no launch claimed.
 */
export interface LaunchBridge {
  launchRun(args: Readonly<Record<string, unknown>>): Promise<unknown>
}

export function launchBridgeFrom(fugaro: unknown): LaunchBridge {
  const ask = (name: string, args: Readonly<Record<string, unknown>>): Promise<unknown> => askMember(fugaro, name, args)
  return { launchRun: (args) => ask('launchRun', args) }
}

/**
 * valueOf is one form control's value, read through the node behind a drawn
 * element: a screen's interfaces carry text and attributes and never an input's
 * `value`, so this is the one place this file reads a value *as* a value rather
 * than as text. A node that carries none -- a fake, or something that is not an
 * input at all -- answers the empty string, which every check and the CLI both
 * refuse.
 */
function valueOf(surface: DomSurface, element: SkewElement): string {
  const node = surface.nodeOf(element) as unknown as { readonly value?: unknown }
  return typeof node.value === 'string' ? node.value : ''
}

/** setValueOf is the other half: the launch form clears the task it launched, and
 * a value is written the same way it is read -- on the node behind the element,
 * because a drawing cannot carry one. */
function setValueOf(surface: DomSurface, element: SkewElement, value: string): void {
  ;(surface.nodeOf(element) as unknown as { value: string }).value = value
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
// The keyboard (desktop-console plan Task 6.5; design sections 4.1 and 4.3).
// ---------------------------------------------------------------------------

/**
 * KeyEventLike is the keyboard event this file reads: **the two fields a key
 * has** -- `code`, the physical key, and `key`, the character the operator's
 * layout produced from it -- the two modifiers the shortcut keys are spelled
 * with, and `preventDefault`, which is how a key the console has taken stops
 * being the window's.
 *
 * Both of the key's fields are here because **the two handlers read different
 * ones**: a tablist's arrow, Home, End and Enter are characters the layout does
 * not change (`event.key`), while the shortcut keys are *positions on the
 * keyboard* and are read as `event.code`, since the unshifted `1` key of an
 * AZERTY keyboard produces `&` (see `shortcutRow`).
 *
 * It is declared structurally rather than as `KeyboardEvent` for the reason
 * every seam in this file is: the wiring is driven under `vitest` with a fake
 * document, and a case dispatches an object it built rather than a real event.
 * A real `KeyboardEvent` satisfies this shape, which is why `keydownOn` below
 * can hand it to a listener typed as this.
 */
export interface KeyEventLike {
  readonly code: string
  readonly key: string
  readonly metaKey: boolean
  readonly ctrlKey: boolean
  preventDefault(): void
}

/**
 * keydownOn attaches a key listener to a real node -- the two tablists'
 * listeners, and the document's for the shortcut keys.
 *
 * The cast is the seam between the DOM's signature and this file's: `Element`'s
 * own `addEventListener` types a key listener's argument as `Event`, which
 * declares none of the three fields a key handler reads, so a listener written
 * in `KeyEventLike` is not assignable to it -- and not even comparable, which is
 * why the conversion goes through `unknown`. Writing it once, here, is what
 * keeps the two tablists' handlers the same shape as the mappings they call.
 */
function keydownOn(node: Element, listener: (event: KeyEventLike) => void): void {
  node.addEventListener('keydown', listener as unknown as EventListener)
}

/**
 * WheelEventLike is the three fields the graph's zoom reads off a wheel event:
 * the direction of the scroll (`deltaY`, whose sign is what says in or out) and
 * `ctrlKey`, which a browser sets when the gesture that produced the event was a
 * **pinch** rather than a wheel -- one listener covers both for that reason, and
 * the two differ only in the step they take. `deltaY`'s magnitude is deliberately
 * not read: a trackpad reports a stream of small deltas and a wheel reports one
 * big one, and a zoom that scaled with either would be a different zoom on every
 * machine.
 *
 * Structural, for the reason `KeyEventLike` is: a case drives this under
 * `vitest` with an object it built.
 */
export interface WheelEventLike {
  readonly deltaY: number
  readonly ctrlKey: boolean
  preventDefault(): void
}

/** wheelOn attaches a wheel listener to a real node, with the same cast and for
 * the same reason `keydownOn` documents: `Element`'s own signature types the
 * argument as `Event`, which declares none of the fields this handler reads. */
function wheelOn(node: Element, listener: (event: WheelEventLike) => void): void {
  node.addEventListener('wheel', listener as unknown as EventListener, { passive: false })
}

/**
 * TabAxis is which arrow pair drives a tablist. **The two tablists have
 * different axes and the design says so**: the navigation is a row of buttons
 * and is driven with Left and Right (design section 4.3), and the rail's list is
 * a column of rows driven with "Arrow Up and Arrow Down" (design section 4.1).
 * Home and End are on both, because they are the same question of either -- the
 * first tab and the last.
 */
export type TabAxis = 'x' | 'y'

/**
 * tabTarget is where an arrow, Home or End moves within a tablist of `count`
 * tabs from the tab at `at`, and `null` for a key the tablist does not handle --
 * which is how a key the console does not own stays the window's.
 *
 * **The arrows wrap**, and that is the one thing here the design does not spell
 * out: "Arrow/Home/End handling" (section 4.3) and "Arrow Up and Arrow Down move
 * between the rows" (section 4.1) say which keys move but not what the last tab
 * does. Wrapping is the WAI-ARIA pattern's own optional behaviour and the one
 * that keeps every arrow key doing something -- without it, Right on the last
 * tab either does nothing or re-selects the tab already on screen, and Home and
 * End would be the only keys that reach the far end. It is a behaviour a case
 * asserts (`entry-wiring.test.ts`), not a licence to invent: a task that wants
 * clamping changes this function and the case with it.
 */
export function tabTarget(key: string, at: number, count: number, axis: TabAxis): number | null {
  if (count <= 0 || at < 0 || at >= count) return null
  const next = axis === 'x' ? 'ArrowRight' : 'ArrowDown'
  const previous = axis === 'x' ? 'ArrowLeft' : 'ArrowUp'
  if (key === next) return (at + 1) % count
  if (key === previous) return (at - 1 + count) % count
  if (key === 'Home') return 0
  if (key === 'End') return count - 1
  return null
}

/**
 * tabStop is the roving `tabindex` one tab of a tablist carries: `'0'` for the
 * tab that holds the list's single stop and `'-1'` for every tab beside it
 * (design sections 4.1 and 4.3 -- "the selected row is `0`, the rest `-1`").
 *
 * It is a function rather than the same ternary written twice because **the two
 * tablists must answer it the same way**, and the rail's list holds one row
 * today: a rail that stopped roving would still be one stop while it is one row,
 * so the rule is held here, where a case drives both answers, and each tablist
 * is left with the write.
 */
export function tabStop(selected: boolean): '0' | '-1' {
  return selected ? '0' : '-1'
}

/**
 * SHORTCUT_ROWS is how many rows the shortcut keys address: **the first three
 * the rail draws**, which is the whole of what design section 4.1 grants
 * ("**Command-1** to **Command-3** on macOS and **Control-1** to **Control-3**
 * elsewhere address the first three rows **the rail draws**") and the hint's own
 * sentence ("Switch with the shortcut keys 1 2 3"). A fourth key is **not the
 * console's key**: it is neither selected nor prevented, so the window keeps it
 * rather than the console swallowing a keystroke no ruling gave it.
 */
export const SHORTCUT_ROWS = 3

/**
 * shortcutRow is the row a shortcut key addresses, or `null` for a key that is
 * not one. The design's rule (section 4.1) is **Command-1 to Command-3 on macOS
 * and Control-1 to Control-3 elsewhere**, and every half of it is load-bearing:
 * the modifier the platform does not use answers `null` rather than being
 * accepted as a second spelling, a digit held with both modifiers answers `null`
 * too, because a key that two rules claim is a key whose rule is not this one,
 * and a position above `SHORTCUT_ROWS` answers `null` because the design grants
 * three rows and no fourth.
 *
 * **`event.code` is read and `event.key` deliberately is not.** `code` is the
 * *physical* key -- the number row's `Digit1` -- while `key` is the character
 * the operator's layout produces from it, and on AZERTY the unshifted `1` key
 * produces `&`: a mapping that read `key` would do nothing at all to the
 * keystroke the rail's hint tells that operator to press. The field is also what
 * makes `Numpad1`, which produces the character `1`, **not** a spelling of
 * `Digit1` -- the design grants the number row's digits and names no keypad key,
 * so admitting one would be a binding nobody ruled on.
 *
 * The digit is the row's **position, one-based** -- `1` is the first row the
 * rail draws -- which is the hint's own sentence. An index no row is drawn at
 * addresses nothing (the rail addresses only the rows it draws), so a rail of
 * one row answers the first key and no other.
 *
 * `mac` is a parameter rather than a read: `isMacPlatform` is the one place the
 * window's platform is asked about, and this function stays a mapping a case can
 * drive both ways.
 */
export function shortcutRow(event: KeyEventLike, mac: boolean): number | null {
  const modifier = mac ? event.metaKey : event.ctrlKey
  const other = mac ? event.ctrlKey : event.metaKey
  if (!modifier || other) return null
  const digit = /^Digit([1-9])$/.exec(event.code)
  if (digit === null) return null
  const position = Number(digit[1])
  if (position > SHORTCUT_ROWS) return null
  return position - 1
}

/**
 * isMacPlatform answers whether this window is on macOS, which is the whole of
 * what the shortcut keys need to know about the machine.
 *
 * **This is the file's one read of the window's platform, and it is a read of
 * the navigator the scope carries** -- not a global, so a case can drive both
 * platforms. The origin is not the reason for the two spellings: the console's
 * `app://` scheme is registered `{ standard: true, secure: true }` precisely so
 * the document is a secure context (`src/main/protocol.ts`), and
 * `navigator.userAgentData` is not withheld from it. The fallback is for the
 * value rather than the origin -- the modern spelling is a Chromium
 * user-agent-client-hints member, and **its `platform` can be an empty string**
 * where the hints are unavailable or reduced, which is a spelling that is
 * present and says nothing. So `navigator.platform` -- deprecated, but answering
 * `MacIntel` on macOS -- is read when the modern one is missing *or blank*, and
 * a name that is still blank answers `false`. A scope with no navigator at all
 * (a `vm` realm, a unit test) answers `false` too, which is the "elsewhere" half
 * of the rule and never an exception.
 */
export function isMacPlatform(navigator: unknown): boolean {
  const scope = navigator as
    | {
        readonly userAgentData?: { readonly platform?: unknown }
        readonly platform?: unknown
      }
    | null
    | undefined
  const modern = scope?.userAgentData?.platform
  const legacy = scope?.platform
  const spelled = (value: unknown): string => (typeof value === 'string' ? value : '')
  const name = spelled(modern) || spelled(legacy)
  return /^mac/i.test(name)
}

// ---------------------------------------------------------------------------
// The installation rail (desktop-console plan Task 6.3; design section 4.1).
// ---------------------------------------------------------------------------

/** RAIL_CLASS and the thirteen below it are the rail's **fourteen hooks** (design
 * sections 2.1 and 4.1): `RAIL_ADD_CLASS` names the foot row and the button
 * inside it both, because a row and the control in it are one region and the
 * class names the region. The rules that select them are `theme/base.css`'s, and
 * they land in this task's commit with these elements -- a hook and the rule that
 * selects it move together, because a stylesheet written against a tree nothing
 * draws is a rule nothing can see.
 *
 * `RAIL_NAMELINE_CLASS` and `RAIL_WARNING_CLASS` are the owner's design ruling of
 * 2026-10-25: a row's name line holds the installation's name **and the warning
 * mark beside it**, which is why the name and the mark share a line element
 * rather than sitting in the row's column one above the other. */
export const RAIL_CLASS = 'console-rail'
export const RAIL_HEAD_CLASS = 'console-rail-head'
export const RAIL_TITLE_CLASS = 'console-rail-title'
export const RAIL_LIST_CLASS = 'console-rail-list'
export const RAIL_ROW_CLASS = 'console-rail-row'
export const RAIL_NAMELINE_CLASS = 'console-rail-nameline'
export const RAIL_NAME_CLASS = 'console-rail-name'
export const RAIL_WARNING_CLASS = 'console-rail-warning'
export const RAIL_PROJECT_CLASS = 'console-rail-project'
export const RAIL_STATE_CLASS = 'console-rail-state'
export const RAIL_SPEND_CLASS = 'console-rail-spend'
export const RAIL_ADD_CLASS = 'console-rail-add'
export const RAIL_HINT_CLASS = 'console-rail-hint'
/** RAIL_EMPTY_CLASS is the line the rail draws in place of rows when it has
 * none; the `+` below it stays the way to add one. */
export const RAIL_EMPTY_CLASS = 'console-rail-empty'

/** RAIL_TITLE_ID is the rail title's id, which names the rows' tablist
 * (`aria-labelledby`): a tablist with no accessible name is announced as a bare
 * list of tabs (review m5). */
export const RAIL_TITLE_ID = 'console-rail-title'

/** RAIL_TITLE, RAIL_HINT and RAIL_ADD_LABEL are the rail's own words, drawn
 * from here rather than from a document: the title names the region, the hint is
 * the design's own sentence about the shortcut keys (Task 6.5's), and the add
 * row's label is the design's. */
export const RAIL_TITLE = 'Installations'
export const RAIL_HINT = 'Switch with the shortcut keys 1 2 3'
export const RAIL_EMPTY_TEXT = 'No installations yet'
export const RAIL_ADD_LABEL = '+ Add installation'

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

/** RAIL_WARNING_GLYPH is the character the warning mark draws, and
 * RAIL_WARNING_LABEL is the word it is announced with. The two are the mark's
 * two halves and neither is the only one (design section 3.5): the glyph is what
 * an operator sees beside the name, and the accessible name is the row's own
 * sentence -- `railWarningName` below -- so the fact survives a screenshot that
 * loses the tone, and a reader that never draws the glyph. */
export const RAIL_WARNING_GLYPH = '⚠'
export const RAIL_WARNING_LABEL = 'warning'

/** railWarningName is the warning mark's accessible name: the word, and the
 * issue view's own sentence for the row. The sentence is the main process's
 * (`src/main/installationstate.ts` composes it) and goes through the sanitizer
 * like every other string a document or the CLI wrote. */
export function railWarningName(row: { readonly sentence: string }): string {
  return `${RAIL_WARNING_LABEL}: ${textOf(row.sentence, 'the installation’s issue')}`
}

/** RAIL_ISSUE_UNREAD is this file's own sentence for a row whose state it could
 * not read. It is the renderer's word and not the main process's, because the
 * main process classifies every row it sends: a row whose `state` is missing or
 * is not a state at all is a row this build does not understand, and the honest
 * answer is the console's own "could not read" -- never `readable`, which would
 * open an installation nothing has classified. */
export const RAIL_ISSUE_UNREAD = 'the console could not read this installation’s state'

/**
 * RailIssue is the four issues the main process classifies an installation with
 * (`InstallationIssue`, `src/main/installationstate.ts`), plus `'unknown'`: this
 * file's own word for a state it could not read. `'unknown'` is deliberately not
 * one of the main process's four -- a fifth issue would be a claim about the
 * machine rather than about this document -- and it is the one value that is
 * never `readable`.
 */
export type RailIssue = 'none' | 'store' | 'config' | 'binary' | 'stale' | 'unknown'

/** RAIL_ISSUES is the vocabulary `railRows` accepts from the wire, and it is the
 * main process's four: a fifth word from a newer console is a state this build
 * does not know, and it is drawn as `'unknown'` -- a warning the operator can
 * read rather than a row this build would open on a guess. */
const RAIL_ISSUES: readonly RailIssue[] = Object.freeze(['none', 'store', 'config', 'binary', 'stale'])

/**
 * RailRow is one installation as this document draws it: what the CLI listed and
 * the console recorded, reduced to the fields the rail and the issue view use.
 *
 * It is **not** `InstallationsListRow`. That type is the main process's
 * (`src/main/onboarding.ts`), it carries facts this document draws nowhere (the
 * GCP project, the region, the mode), and a sandboxed renderer cannot import it:
 * what crosses is JSON, and `railRows` is the one place it is read -- so a row
 * this file cannot read is a row it refuses to open rather than one it opens
 * with fields defaulted.
 */
export interface RailRow {
  readonly project: string
  readonly issue: RailIssue
  /** `readable` is the main process's own answer to "does a click open this
   * installation": true for every issue but the three refusals, and true for
   * `stale` (the owner's ruling of 2026-10-25: an installation whose shared
   * config is overdue is still opened, because the reader returned its fields).
   * It is **false** for `'unknown'`, which is this file's refusal to open a row
   * nothing has classified. */
  readonly readable: boolean
  /** `sentence` is the issue view's one plain sentence, and the warning mark's
   * accessible name. Empty when the row has no issue. */
  readonly sentence: string
  /** `command` is the exact command the sentence names, for the issue view's
   * own line. Empty when there is none to run. */
  readonly command: string
  /** `details` is the CLI's own text about this installation, verbatim, for the
   * issue view's disclosure. */
  readonly details: readonly string[]
  /** `open` is the one row the console is open on, and the rail's selected row
   * (the main process decides it: `openProject`). */
  readonly open: boolean
  /** `active` is the operator's remembered choice in the store, which this
   * document never rewrites and which differs from `open` in exactly the case
   * the owner's ruling names. */
  readonly active: boolean
  /** `saved` is whether this console holds a record of the installation at all,
   * which is what decides whether there is anything to remove: the main
   * process's removal refuses a project it has no entry for. */
  readonly saved: boolean
  readonly checkout: string | null
  readonly checkouts: readonly string[]
  /** `repositories` is what the installation records as its own, in the CLI's
   * order and each one once: the `<provider>/<owner>/<name>` identity verbatim.
   * It is what the launch control offers (V2.5.1b) and it is **not** `checkouts`
   * -- a launch names a repository, not a directory -- so a row that carries no
   * parseable list carries none rather than the paths, and the form then draws
   * its own sentence. */
  readonly repositories: readonly string[]
}

/**
 * railRows reads one `installation.list` answer: every row, in the order the
 * main process sent them, or `null` when the answer is not a list at all.
 *
 * **It is a door, not a cast.** The renderer's other reads hand a document to
 * `schema.ts`; this one is small enough to read here, and every field is checked
 * because the answer is the *console's own* main process only in the shipped
 * app: a bridge is a seam, a test drives it with a fake, and a row read as
 * `undefined` and drawn as `undefined` is a rail that says an installation is
 * called nothing. A row with no name is dropped -- it names no installation and
 * nothing could be asked about it -- and a row whose `state` cannot be read is
 * kept with `'unknown'`, because a row that exists and cannot be opened is
 * exactly what the rail exists to show.
 *
 * `null` is the answer for a reply that is not a reply, or one whose `rows` is
 * not an array: the caller draws its refusal and keeps the rows it had, which is
 * the same direction every other read in this file fails in.
 */
export function railRows(reply: unknown): readonly RailRow[] | null {
  const answer = replyData(reply)
  if (!answer.ok) return null
  const data = answer.data
  const document =
    typeof data === 'object' && data !== null && !Array.isArray(data)
      ? (data as Record<string, unknown>)
      : null
  const listed = document === null ? null : document['rows']
  if (!Array.isArray(listed)) return null
  const rows: RailRow[] = []
  for (const raw of listed) {
    const row =
      typeof raw === 'object' && raw !== null && !Array.isArray(raw)
        ? (raw as Record<string, unknown>)
        : null
    if (row === null) continue
    const project = row['project']
    if (typeof project !== 'string' || project === '') continue
    const state = row['state']
    const held =
      typeof state === 'object' && state !== null && !Array.isArray(state)
        ? (state as Record<string, unknown>)
        : null
    const issue = held === null ? 'unknown' : railIssueOf(held['issue'])
    rows.push(
      Object.freeze({
        project,
        issue,
        readable: issue !== 'unknown' && held?.['readable'] === true,
        sentence: held === null ? RAIL_ISSUE_UNREAD : textOf(held['sentence'], 'the installation’s issue'),
        command: held === null ? '' : textOf(held['command'], 'the installation’s command'),
        details: held === null ? Object.freeze([]) : stringsOf(held['details']),
        open: row['open'] === true,
        active: row['active'] === true,
        saved: row['saved'] === true,
        checkout: typeof row['checkout'] === 'string' && row['checkout'] !== '' ? row['checkout'] : null,
        checkouts: stringsOf(row['checkouts']),
        // Read like `checkouts` except for the one thing this field is not: an
        // identity main matches against its own records, so it is kept **raw**.
        // A list this field cannot be read from is an installation whose launch
        // control has nothing to offer (the panel draws its own sentence), which
        // is not a reason to drop a row the rail can draw in every other
        // respect. The launch form draws each identity through `sanitizeRefusal`
        // and keeps the raw string beside the drawn label -- a repository whose
        // name is shaped like a credential must still launch.
        repositories: rawStringsOf(row['repositories']),
      }),
    )
  }
  return Object.freeze(rows)
}

/** railIssueOf is one wire word as this file's vocabulary: the main process's
 * four, or `'unknown'` for anything else -- a word from a newer console, a
 * number, a missing field. */
function railIssueOf(raw: unknown): RailIssue {
  return RAIL_ISSUES.includes(raw as RailIssue) ? (raw as RailIssue) : 'unknown'
}

/** stringsOf is a list of strings as this file draws it: each one through
 * `textOf`, and anything that is not a string dropped rather than drawn as
 * `undefined`. */
function stringsOf(raw: unknown): readonly string[] {
  if (!Array.isArray(raw)) return Object.freeze([])
  return Object.freeze(
    raw.filter((one): one is string => typeof one === 'string').map((one) => textOf(one, 'the CLI’s own line')),
  )
}

/** rawStringsOf is a list of strings with nothing done to them: the same filter
 * `stringsOf` applies, and no sanitiser. It is for a list of *identities* --
 * today the repository list a launch names, which main matches against the
 * records it read from the CLI -- where a folded or redacted copy would be a
 * value main refuses. The screen that draws one redacts the label it draws and
 * keeps the raw string beside it (`screens/launch.ts`'s `options`). */
function rawStringsOf(raw: unknown): readonly string[] {
  if (!Array.isArray(raw)) return Object.freeze([])
  return Object.freeze(raw.filter((one): one is string => typeof one === 'string'))
}

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

/** RailView is the drawn rail and the writes this file makes to it. The rail's
 * own regions are drawn once and updated in place, rather than redrawn on every
 * frame: it is chrome, and a rail rebuilt under the operator's pointer is a rail
 * that loses its focus. Its *rows* are the one part that is rebuilt, because a
 * row is a fact about the machine rather than about this document -- a choose, a
 * removal and a checkout all end in a fresh list read (`setRows`). */
export interface RailView {
  readonly root: DomElement
  /** setState writes the state word and its glyph, on the row the console is
   * open on. Every other row is `idle`, which is the vocabulary's own word for
   * "it is stored, and nothing is being read from it". */
  setState(state: RailState): void
  /** setSpend writes the open row's spend, from the console's own budget read. */
  setSpend(reading: Reading): void
  /** setRows draws a fresh list: every installation the main process answered
   * with, in its own order, and the row it marks `open` selected. */
  setRows(rows: readonly RailRow[]): void
  /** view marks the row whose issue the main area is showing as the selected
   * row, or -- with `null` -- hands the mark back to the row the console is open
   * on. A row the list no longer holds marks nothing and falls back the same
   * way. */
  view(project: string | null): void
  /** selectAt activates the row at a position -- the write the shortcut keys make
   * (design section 4.1: "the shortcut keys select by position", and the hint's
   * own sentence is "Switch with the shortcut keys 1 2 3"). It moves the keyboard
   * to that row as well, because a switch the operator's next arrow key would
   * move away from is a switch that does not agree with the focus; an index no
   * row is drawn at addresses nothing. */
  selectAt(index: number): void
}

/**
 * drawRail draws the installation rail: the canvas's one structural addition
 * (design section 4.1, approved by the owner on 2026-10-19), and the grid's
 * first column -- the body's first content child when there is no banner, and
 * its second when there is one, because `startConsole` appends the skew banner
 * to the same mount first. It is appended before the installation line so the
 * tab order is the order a reader sees.
 *
 * **It draws one row per installation the main process listed, readable or
 * not** (owner's design ruling of 2026-10-25). Every row is a tab of the list:
 * its name and its project, its state word and its spend -- and, when the row
 * has an issue, the warning mark beside its name. A row with an issue carries
 * the mark **and** the issue's sentence as its accessible name (`railWarningName`),
 * so the fact is never the tone alone (design section 3.5).
 *
 * **A click activates the row**, and what that means is the ruling's own rule:
 * a readable row switches the console to it, and a row with an issue opens that
 * issue in the main area. `activate` is the caller's -- `mountDashboard` owns
 * both halves, because both are reads and this function makes none.
 *
 * **The open row's state is the rail's own mark, not a field of any read**: the
 * rail knows whether the console is reading that installation now, so it opens
 * at `running` and goes `silent` when that read was refused or the feed has gone
 * quiet (`setState`). Every other row is `idle` -- stored, and nothing being read
 * from it -- and a row the console cannot open is `idle` too, which is the
 * honest word for an installation this run is not reading.
 *
 * **The foot row opens the picker window**, which is the one thing the owner's
 * ruling leaves the picker for: adding an installation by checkout path. It is
 * `installation.open`, and the window it opens is the existing picker document
 * (`src/main/picker.ts`), not a second flow.
 */
export function drawRail(
  surface: DomSurface,
  rows: readonly RailRow[],
  activate: (row: RailRow, byKeyboard: boolean) => void,
  openPicker: () => void,
  onState: (shown: RailState) => void = () => undefined,
): RailView {
  const factory = surface.factory
  const rail = factory.create('aside')
  rail.setAttribute('class', RAIL_CLASS)

  const head = factory.create('div')
  head.setAttribute('class', RAIL_HEAD_CLASS)
  const title = factory.create('p')
  title.setAttribute('class', RAIL_TITLE_CLASS)
  title.textContent = RAIL_TITLE
  title.setAttribute('id', RAIL_TITLE_ID)
  head.append(title)

  // The list is a tablist in its own right (design sections 2.1 and 4.1): the
  // role is Task 6.5's, and it is the renderer's first `role=` -- the rows below
  // are its tabs.
  const list = factory.create('div')
  list.setAttribute('class', RAIL_LIST_CLASS)
  list.setAttribute('role', 'tablist')
  list.setAttribute('aria-labelledby', RAIL_TITLE_ID)

  const foot = factory.create('div')
  foot.setAttribute('class', RAIL_ADD_CLASS)
  const add = factory.create('button')
  add.setAttribute('class', RAIL_ADD_CLASS)
  add.setAttribute('type', 'button')
  add.textContent = RAIL_ADD_LABEL
  foot.append(add)
  surface.nodeOf(add).addEventListener('click', openPicker)

  const hint = factory.create('p')
  hint.setAttribute('class', RAIL_HINT_CLASS)
  hint.textContent = RAIL_HINT

  const empty = factory.create('p')
  empty.setAttribute('class', RAIL_EMPTY_CLASS)
  empty.textContent = RAIL_EMPTY_TEXT

  // The drawn rows, in the order they are drawn: the shortcut keys address them
  // by position and the arrows move between them. Each entry keeps the row's
  // four writable spans beside the row it was drawn for, so a setter below
  // writes to the row it is about rather than to "the row".
  const drawn: { readonly row: RailRow; readonly element: DomElement; readonly state: DomElement; readonly spend: DomElement }[] = []
  // The same rows' buttons and state spans, position for position: the two
  // writers below walk these, which is the shape the theme's selector scan reads
  // an attribute's element from (`test/unit/theme-selectors.test.ts`).
  const drawnButtons: DomElement[] = []
  const drawnStates: DomElement[] = []

  /** word is the state of the installation the console is reading. It is
   * `running` from the first paint, because a console that has just mounted is
   * about to make its first read of it. */
  let word: RailState = 'running'

  /**
   * mark writes the two attributes a tablist needs on every row: `aria-selected`
   * -- which the design's rule selects for the 3 px `--accent` border
   * (`base.css`, design section 4.1) -- and the roving `tabindex`.
   *
   * **The selected row is the one the main process marked `open`**, and the tab
   * stop falls back to the first row when no row is open -- the same fallback
   * `markButtons` makes for the navigation, and for the same reason: a tablist
   * whose every tab is at `-1` is a tablist the keyboard cannot enter at all.
   * The two are one row in every answer this console sends (the main process
   * marks exactly one row open whenever it lists one), so the fallback is a
   * shape the reply cannot produce today rather than a state the rail is in.
   *
   * While the main area shows another row's issue (`view`), that row is the
   * selected one: the mark says which installation the main area is about.
   */
  let viewing: string | null = null
  const mark = (): void => {
    const viewIndex = viewing === null ? -1 : drawn.findIndex((entry) => entry.row.project === viewing)
    const selected = viewIndex >= 0 ? viewIndex : drawn.findIndex((entry) => entry.row.open)
    const stop = selected >= 0 ? selected : 0
    for (const rowButton of drawnButtons) {
      const index = drawnButtons.indexOf(rowButton)
      rowButton.setAttribute('aria-selected', index === selected ? 'true' : 'false')
      rowButton.setAttribute('tabindex', tabStop(index === stop))
    }
  }

  /** writeState writes the state word and its glyph on every row: the open row
   * carries `word` when it can be read, and `idle` is what everything else is. */
  const writeState = (): void => {
    const openIndex = drawn.findIndex((entry) => entry.row.open)
    for (const stateSpan of drawnStates) {
      const index = drawnStates.indexOf(stateSpan)
      const shown: RailState = index === openIndex && drawn[index]?.row.readable === true ? word : 'idle'
      stateSpan.textContent = `${RAIL_STATE_GLYPHS[shown]} ${shown}`
      stateSpan.setAttribute('data-state', shown)
    }
    const open = openIndex >= 0 ? drawn[openIndex] : undefined
    onState(open !== undefined && open.row.readable ? word : 'idle')
  }

  /** rowElement draws one installation's row, with the two spans a setter
   * writes to kept beside the element that holds them. */
  const rowElement = (
    row: RailRow,
  ): { readonly element: DomElement; readonly state: DomElement; readonly spend: DomElement } => {
    const rowButton = factory.create('button')
    rowButton.setAttribute('class', RAIL_ROW_CLASS)
    rowButton.setAttribute('type', 'button')
    rowButton.setAttribute('role', 'tab')
    const nameline = factory.create('div')
    nameline.setAttribute('class', RAIL_NAMELINE_CLASS)
    const name = factory.create('span')
    name.setAttribute('class', RAIL_NAME_CLASS)
    // One label, two spans: `ActiveInstallation` carries no display name, so the
    // row's identity is the project it names, drawn as the name and again as the
    // small-text line under it.
    putText(name, row.project, 'label', 'the installation’s project')
    nameline.append(name)
    if (row.issue !== 'none') {
      const warning = factory.create('span')
      warning.setAttribute('class', RAIL_WARNING_CLASS)
      // `role="img"` with an `aria-label` is what makes the mark a thing with a
      // name rather than a stray character: a bare span's `aria-label` is not
      // announced by every reader, and the mark has to carry the sentence
      // somewhere the tone cannot (design section 3.5).
      warning.setAttribute('role', 'img')
      warning.setAttribute('aria-label', railWarningName(row))
      warning.setAttribute('title', railWarningName(row))
      warning.textContent = RAIL_WARNING_GLYPH
      nameline.append(warning)
    }
    const project = factory.create('span')
    project.setAttribute('class', RAIL_PROJECT_CLASS)
    putText(project, row.project, 'label', 'the installation’s project')
    const stateSpan = factory.create('span')
    stateSpan.setAttribute('class', RAIL_STATE_CLASS)
    const spend = factory.create('span')
    spend.setAttribute('class', RAIL_SPEND_CLASS)
    putReading(spend, unavailable(RAIL_SPEND_UNREAD), 'usd')
    rowButton.append(nameline)
    rowButton.append(project)
    rowButton.append(stateSpan)
    rowButton.append(spend)
    drawnButtons.push(rowButton)
    drawnStates.push(stateSpan)
    return { element: rowButton, state: stateSpan, spend }
  }

  /**
   * setRows draws a fresh list and wires each row. The rows are **replaced
   * wholesale** rather than patched: a row that is gone from the answer is gone
   * from the rail, and the row the reply marks `open` is the selected one.
   *
   * Each row's keys are the rail's half of Task 6.5 (design section 4.1): the
   * arrows move the focus between the rows and change no mark, and **Enter
   * activates** the row the operator is standing on -- the pattern's
   * manual-activation form, whose "select" is the click the row already answers.
   */
  const setRows = (next: readonly RailRow[]): void => {
    drawn.length = 0
    drawnButtons.length = 0
    drawnStates.length = 0
    const elements: DomElement[] = []
    for (const row of next) {
      const { element, state, spend } = rowElement(row)
      elements.push(element)
      drawn.push({ row, element, state, spend })
      surface.nodeOf(element).addEventListener('click', () => {
        activate(row, false)
      })
      keydownOn(surface.nodeOf(element), (event: KeyEventLike) => {
        const target = tabTarget(event.key, elements.indexOf(element), elements.length, 'y')
        if (target !== null) {
          event.preventDefault()
          elements[target]?.focus()
          return
        }
        if (event.key === 'Enter') {
          event.preventDefault()
          activate(row, true)
        }
      })
    }
    surface.nodeOf(list).replaceChildren(...elements.map((element) => surface.nodeOf(element)))
    if (next.length > 0) empty.setAttribute('hidden', '')
    else empty.removeAttribute('hidden')
    mark()
    writeState()
  }

  rail.append(head)
  rail.append(list)
  rail.append(empty)
  rail.append(foot)
  rail.append(hint)
  setRows(rows)

  return {
    root: rail,
    setState: (next: RailState): void => {
      word = next
      writeState()
    },
    setSpend: (reading: Reading): void => {
      const open = drawn.find((entry) => entry.row.open)
      if (open === undefined || !open.row.readable) return
      // The title is cleared first: `putReading` writes one only for a reading
      // with no value, and the rail is the one drawing this file *updates* in
      // place rather than redrawing -- so a reason left over from the `n/a` this
      // span drew a moment ago would sit under a figure that is now measured.
      open.spend.removeAttribute('title')
      putReading(open.spend, reading, 'usd')
    },
    setRows,
    view: (project: string | null): void => {
      viewing = project
      mark()
    },
    selectAt: (index: number): void => {
      const wanted = drawn[index]
      if (wanted === undefined) return
      // The focus follows, and it is the same move the row's own Enter makes: a
      // shortcut that switched the console while the keyboard stayed where it
      // was would leave the operator's next arrow key acting somewhere else.
      wanted.element.focus()
      activate(wanted.row, true)
    },
  }
}

// ---------------------------------------------------------------------------
// The onboarding view: a machine with no installation at all.
// ---------------------------------------------------------------------------

/** RailOnboarding is the list answer's `onboard` state, as this file draws it:
 * the two screens `src/main/onboarding.ts` defines, never `none`. */
export interface RailOnboarding {
  readonly kind: 'install-cli' | 'first-installation'
  readonly command: string
  /** reason is the resolution's own sentence for `install-cli`, `''` otherwise. */
  readonly reason: string
}

/** railOnboarding reads `onboard` out of one `installation.list` answer, or
 * `null` when it names no screen this build draws. */
export function railOnboarding(reply: unknown): RailOnboarding | null {
  const answer = replyData(reply)
  if (!answer.ok || typeof answer.data !== 'object' || answer.data === null) return null
  const onboard = (answer.data as Record<string, unknown>)['onboard']
  if (typeof onboard !== 'object' || onboard === null) return null
  const state = onboard as Record<string, unknown>
  const kind = state['kind']
  if (kind !== 'install-cli' && kind !== 'first-installation') return null
  return Object.freeze({
    kind,
    command: textOf(state['command'], 'the onboarding command'),
    reason: typeof state['reason'] === 'string' ? textOf(state['reason'], 'the resolution’s sentence') : '',
  })
}

export const ONBOARDING_CLASS = 'installation-onboarding'
export const ONBOARDING_TITLE_CLASS = 'installation-onboarding-title'
export const ONBOARDING_BODY_CLASS = 'installation-onboarding-body'
export const ONBOARDING_REASON_CLASS = 'installation-onboarding-reason'
export const ONBOARDING_COMMAND_CLASS = 'installation-onboarding-command'

/** drawOnboarding is the main area of a console with no installation: the
 * picker's own words for the screen (`ONBOARDING_COPY`), the resolution's
 * sentence when the CLI is missing, and the one command from the main process. */
export function drawOnboarding(surface: DomSurface, onboarding: RailOnboarding): SkewElement {
  const factory = surface.factory
  const copy = ONBOARDING_COPY[onboarding.kind]
  const section = factory.create('section')
  section.setAttribute('class', ONBOARDING_CLASS)
  const title = factory.create('h1')
  title.setAttribute('class', ONBOARDING_TITLE_CLASS)
  title.textContent = copy.title
  section.append(title)
  const body = factory.create('p')
  body.setAttribute('class', ONBOARDING_BODY_CLASS)
  body.textContent = copy.body
  section.append(body)
  if (onboarding.reason !== '') {
    const reason = factory.create('p')
    reason.setAttribute('class', ONBOARDING_REASON_CLASS)
    reason.textContent = onboarding.reason
    section.append(reason)
  }
  const command = factory.create('code')
  command.setAttribute('class', ONBOARDING_COMMAND_CLASS)
  command.textContent = onboarding.command
  section.append(command)
  return section
}

// ---------------------------------------------------------------------------
// The issue view (owner's design ruling of 2026-10-25).
// ---------------------------------------------------------------------------

/** The issue view's hooks. It is chrome -- `entry.ts` draws it, not a screen --
 * because it is about the installation rather than about anything the CLI
 * measured, and because it is the one drawing in the main area that carries
 * controls of its own (design section 2.1's rail row is the same shape). */
export const ISSUE_CLASS = 'installation-issue'
export const ISSUE_HEADLINE_CLASS = 'installation-issue-headline'
export const ISSUE_SENTENCE_CLASS = 'installation-issue-sentence'
export const ISSUE_COMMAND_CLASS = 'installation-issue-command'
export const ISSUE_DETAILS_CLASS = 'installation-issue-details'
export const ISSUE_SUMMARY_CLASS = 'installation-issue-summary'
export const ISSUE_LINES_CLASS = 'installation-issue-lines'
export const ISSUE_ACTIONS_CLASS = 'installation-issue-actions'
export const ISSUE_CHOOSE_CLASS = 'installation-issue-choose'
export const ISSUE_REMOVE_CLASS = 'installation-issue-remove'
export const ISSUE_CHECKOUT_LEAD_CLASS = 'installation-issue-checkout-lead'
export const ISSUE_CHECKOUT_CLASS = 'installation-issue-checkout'
export const ISSUE_REVEAL_CLASS = 'installation-issue-reveal'

/** ISSUE_DETAILS_LABEL is the disclosure's summary: the CLI's own lines are one
 * click away and never in the way. The ruling asks for exactly this -- "the CLI's
 * full text behind a 'details' disclosure" -- and the word is the disclosure's,
 * not a paraphrase of what is behind it. */
export const ISSUE_DETAILS_LABEL = 'details'

/** ISSUE_CHOOSE_LABEL is the switch a **readable** row's issue offers. Only a
 * warning that is not a refusal can be openable (`stale`), and the operator
 * still has to be able to act on the row they clicked -- so the issue view
 * carries the old Installations screen's "Choose", under a label that says what
 * it does to the console rather than what it does to the row. */
export const ISSUE_CHOOSE_LABEL = 'Use this installation'

/** ISSUE_REMOVE_LABEL, ISSUE_CHECKOUT_LEAD, ISSUE_CHECKOUT_LABEL and
 * ISSUE_REVEAL_LABEL are the three actions the owner's ruling names -- "the
 * 'Remove from this console' / 'Choose a checkout' actions that the old
 * Installations screen offered, now inside that issue view" -- with the old
 * screen's own words wherever it had them. */
export const ISSUE_REMOVE_LABEL = 'Remove from this console'
export const ISSUE_CHECKOUT_LEAD = 'Choose a checkout'
export const ISSUE_CHECKOUT_LABEL = 'Use this directory'
export const ISSUE_CHECKOUT_STOP_LABEL = 'Stop using this checkout'
export const ISSUE_REVEAL_LABEL = 'Open the checkout folder'

/** IssueActions is what the issue view's controls send. Every one of them is the
 * caller's (`mountDashboard`), which is where the read and the redrawing are:
 * this function draws one row's issue and decides nothing. */
export interface IssueActions {
  readonly choose: (project: string) => void
  readonly remove: (project: string) => void
  readonly checkout: (project: string, checkout: string | null) => void
  readonly reveal: (project: string) => void
}

/**
 * drawIssue draws one installation's issue, which is what the main area holds
 * when the console cannot read the installation it is open on -- and what a
 * click on a warned row opens (owner's design ruling of 2026-10-25).
 *
 * The order is the ruling's own: **one plain sentence first**, the exact command
 * it names drawn beneath it as a `code` line, the CLI's own text behind a
 * `details` disclosure, and the actions the old Installations screen offered --
 * now here, and nowhere else. There is no banner and no dismissible notice: the
 * panel *is* the issue while it is up, and the rail beside it is how the operator
 * leaves.
 *
 * Every string is the main process's, already through `textOf` in `railRows`
 * (which is why the writes below are `textContent` and not a second sanitizer
 * pass), and no path is invented here: the checkouts offered are the row's own
 * list, and the main process checks membership against the document it read.
 */
export function drawIssue(
  surface: DomSurface,
  row: RailRow,
  actions: IssueActions,
): { readonly root: SkewElement; readonly headline: DomElement } {
  const factory = surface.factory
  const section = factory.create('section')
  section.setAttribute('class', ISSUE_CLASS)
  // `data-issue` is the classification the main process made, repeated as a hook
  // no rule selects today: a stylesheet that wants to tell a stale warning from a
  // refusal adds it to §2.1 first (design §2's licence rule).
  section.setAttribute('data-issue', row.issue)

  const headline = factory.create('h1')
  headline.setAttribute('class', ISSUE_HEADLINE_CLASS)
  // Focusable by script and not in the tab order: a keyboard activation moves
  // the reader to the issue it opened (review m5).
  headline.setAttribute('tabindex', '-1')
  putText(headline, row.project, 'label', 'the installation’s project')
  section.append(headline)

  const sentence = factory.create('p')
  sentence.setAttribute('class', ISSUE_SENTENCE_CLASS)
  sentence.textContent = row.sentence
  section.append(sentence)

  if (row.command !== '') {
    const command = factory.create('code')
    command.setAttribute('class', ISSUE_COMMAND_CLASS)
    command.textContent = row.command
    section.append(command)
  }

  if (row.details.length > 0) {
    const details = factory.create('details')
    details.setAttribute('class', ISSUE_DETAILS_CLASS)
    const summary = factory.create('summary')
    summary.setAttribute('class', ISSUE_SUMMARY_CLASS)
    summary.textContent = ISSUE_DETAILS_LABEL
    details.append(summary)
    const lines = factory.create('pre')
    lines.setAttribute('class', ISSUE_LINES_CLASS)
    lines.textContent = row.details.join('\n')
    details.append(lines)
    section.append(details)
  }

  const controls = factory.create('div')
  controls.setAttribute('class', ISSUE_ACTIONS_CLASS)
  // The class is set at each `create`, not through a helper's parameter: the
  // theme's selector scan reads a hook's tag from that pair.
  const wire = (element: DomElement, label: string, onClick: () => void): void => {
    element.setAttribute('type', 'button')
    element.textContent = label
    surface.nodeOf(element).addEventListener('click', onClick)
    controls.append(element)
  }

  // The switch comes first when there is one: it is the action that leaves the
  // issue, and every other action below keeps the operator in it.
  if (row.readable) {
    const choose = factory.create('button')
    choose.setAttribute('class', ISSUE_CHOOSE_CLASS)
    wire(choose, ISSUE_CHOOSE_LABEL, () => actions.choose(row.project))
  }
  if (row.saved) {
    const remove = factory.create('button')
    remove.setAttribute('class', ISSUE_REMOVE_CLASS)
    wire(remove, ISSUE_REMOVE_LABEL, () => actions.remove(row.project))
  }
  if (row.checkout !== null) {
    const reveal = factory.create('button')
    reveal.setAttribute('class', ISSUE_REVEAL_CLASS)
    wire(reveal, ISSUE_REVEAL_LABEL, () => actions.reveal(row.project))
  }

  const offered = row.checkouts.filter((candidate) => candidate !== row.checkout)
  if (offered.length > 0 || row.checkout !== null) {
    const lead = factory.create('p')
    lead.setAttribute('class', ISSUE_CHECKOUT_LEAD_CLASS)
    lead.textContent = ISSUE_CHECKOUT_LEAD
    controls.append(lead)
    for (const checkout of offered) {
      const element = factory.create('button')
      element.setAttribute('class', ISSUE_CHECKOUT_CLASS)
      element.setAttribute('type', 'button')
      element.textContent = `${ISSUE_CHECKOUT_LABEL}: ${checkout}`
      surface.nodeOf(element).addEventListener('click', () => {
        actions.checkout(row.project, checkout)
      })
      controls.append(element)
    }
    if (row.checkout !== null) {
      const element = factory.create('button')
      element.setAttribute('class', ISSUE_CHECKOUT_CLASS)
      element.setAttribute('type', 'button')
      element.textContent = ISSUE_CHECKOUT_STOP_LABEL
      surface.nodeOf(element).addEventListener('click', () => {
        actions.checkout(row.project, null)
      })
      controls.append(element)
    }
  }

  section.append(controls)
  return { root: section, headline }
}

// ---------------------------------------------------------------------------
// The shell: one header row and the page container (console redesign, step 1).
// ---------------------------------------------------------------------------

/** The shell's hooks. They are constants because a spec reads them, and each is
 * a row of the styling design's §2.1 table, which is the licence a rule selects
 * it under. */
export const SHELL_HEADER_CLASS = 'console-header'
export const SHELL_WORDMARK_CLASS = 'console-wordmark'
export const SHELL_WORDMARK_NAME_CLASS = 'console-wordmark-name'
export const SHELL_PAGE_CLASS = 'console-page'
export const SWITCHER_CLASS = 'console-switcher'
export const SWITCHER_BUTTON_CLASS = 'console-switcher-button'
export const SWITCHER_DOT_CLASS = 'console-switcher-dot'
export const SWITCHER_NAME_CLASS = 'console-switcher-name'
export const SWITCHER_VERSION_CLASS = 'console-switcher-version'
export const SWITCHER_MENU_CLASS = 'console-switcher-menu'
export const SWITCHER_MENU_ID = 'console-switcher-menu'
export const GAUGE_CLASS = 'console-gauge'
export const GAUGE_LABEL_CLASS = 'console-gauge-label'
export const GAUGE_NOTIONAL_CLASS = 'console-gauge-notional'
export const GAUGE_BAR_CLASS = 'console-gauge-bar'
export const GAUGE_TRACK_CLASS = 'console-gauge-track'
export const GAUGE_FILL_CLASS = 'console-gauge-fill'
export const GAUGE_CAP_CLASS = 'console-gauge-cap'
export const HALT_CLASS = 'console-halt'

export const WORDMARK_TEXT = 'fugaro'
export const SWITCHER_TITLE = 'Switch installation'
export const GAUGE_LABEL = 'GLOBAL'
export const GAUGE_NAME = 'Global budget today'
export const HALT_LABEL = 'Halt fleet'

/** GAUGE_WIDTH and GAUGE_HEIGHT are the gauge's drawn size in CSS pixels, and
 * its SVG's user units: the fill's width is an attribute in those units, because
 * the console writes no inline style and a stylesheet cannot read a number out
 * of an attribute. */
export const GAUGE_WIDTH = 96
export const GAUGE_HEIGHT = 6

/** GAUGE_WITHHELD is the gauge's reason when the mode withholds caps: the same
 * gate the Spend and Projects views ask, so a reduced console shows `n/a` here
 * rather than figures from a CLI shape it does not know. */
export const GAUGE_WITHHELD = 'this CLI version’s caps are not shown'

/** The reduced-mode chip's hooks and its word. The chip is the header's
 * statement that the console is driving a fugaro it reads only in part -- it
 * stands in the header for as long as the mode does, so a dismissed notice is a
 * card put away and never a fact hidden (§2.14). */
export const REDUCED_CHIP_CLASS = 'console-reduced-chip'
export const REDUCED_CHIP_REASON_CLASS = 'console-reduced-chip-reason'
export const REDUCED_CHIP_REASON_ID = 'console-reduced-chip-reason'
export const REDUCED_CHIP_LABEL = 'Reduced mode'
export const GAUGE_UNREAD = 'no budget has been read yet'

/** SwitcherLine is what the switcher's button draws: the installation's name
 * and its CLI version and mode, from `installation.current`. */
export interface SwitcherLine {
  readonly name: string
  readonly version: string
}

/** switcherLine reads the button's two texts out of `installation.current`,
 * through the same sanitizer `drawInstallation` uses. */
export function switcherLine(reply: unknown): SwitcherLine {
  const answer = replyData(reply)
  const value =
    answer.ok && typeof answer.data === 'object' && answer.data !== null && !Array.isArray(answer.data)
      ? (answer.data as Record<string, unknown>)
      : null
  if (value === null || value['chosen'] !== true) {
    return { name: 'No installation', version: value === null ? 'not read' : 'none chosen' }
  }
  const version = value['cliVersion']
  return {
    name: textOf(value['project'], 'the installation’s project'),
    version: `CLI ${typeof version === 'string' ? textOf(version, 'the installation’s version') : 'version not read'} · ${textOf(value['mode'], 'the installation’s mode')}`,
  }
}

/** SwitcherView is the drawn switcher and the writes this file makes to it. */
export interface SwitcherView {
  readonly root: DomElement
  /** setState writes the status dot: the open installation's rail word. */
  setState(state: RailState): void
  /** close folds the menu and, when asked, hands the focus back to the button. */
  close(focusButton: boolean): void
}

/**
 * drawSwitcher draws the installation switcher, which replaced the canvas's
 * left rail (owner's ruling of 2026-10-28): a button naming the installation
 * the console reads, and a menu holding what the rail held -- the installation
 * line with the CLI's full path, then the rail's own tree. The menu is a
 * disclosure: `aria-expanded` on the button, `hidden` on the menu, Escape and a
 * click outside fold it. The shortcut keys still reach the rows when it is
 * folded, because they are the document's.
 */
export function drawSwitcher(
  surface: DomSurface,
  document: Document,
  line: SwitcherLine,
  installation: SkewElement,
  rail: DomElement,
): SwitcherView {
  const factory = surface.factory
  const root = factory.create('div')
  root.setAttribute('class', SWITCHER_CLASS)

  const trigger = factory.create('button')
  trigger.setAttribute('class', SWITCHER_BUTTON_CLASS)
  trigger.setAttribute('type', 'button')
  trigger.setAttribute('title', SWITCHER_TITLE)
  trigger.setAttribute('aria-haspopup', 'true')
  trigger.setAttribute('aria-expanded', 'false')
  trigger.setAttribute('aria-controls', SWITCHER_MENU_ID)
  const dot = factory.create('span')
  dot.setAttribute('class', SWITCHER_DOT_CLASS)
  dot.setAttribute('data-state', 'running')
  dot.setAttribute('role', 'img')
  dot.setAttribute('aria-label', 'running')
  const name = factory.create('span')
  name.setAttribute('class', SWITCHER_NAME_CLASS)
  name.textContent = line.name
  const version = factory.create('span')
  version.setAttribute('class', SWITCHER_VERSION_CLASS)
  version.textContent = line.version
  trigger.append(dot)
  trigger.append(name)
  trigger.append(version)

  const menu = factory.create('div')
  menu.setAttribute('class', SWITCHER_MENU_CLASS)
  menu.setAttribute('id', SWITCHER_MENU_ID)
  menu.setAttribute('hidden', '')
  menu.append(installation)
  menu.append(rail)

  root.append(trigger)
  root.append(menu)

  const close = (focusButton: boolean): void => {
    menu.setAttribute('hidden', '')
    trigger.setAttribute('aria-expanded', 'false')
    if (focusButton) trigger.focus()
  }
  surface.nodeOf(trigger).addEventListener('click', () => {
    if (trigger.getAttribute('aria-expanded') === 'true') {
      close(false)
      return
    }
    menu.removeAttribute('hidden')
    trigger.setAttribute('aria-expanded', 'true')
  })
  keydownOn(surface.nodeOf(root), (event: KeyEventLike) => {
    if (event.key !== 'Escape' || trigger.getAttribute('aria-expanded') !== 'true') return
    event.preventDefault()
    close(true)
  })
  const node = surface.nodeOf(root)
  document.addEventListener('click', (event: Event) => {
    if (trigger.getAttribute('aria-expanded') !== 'true') return
    const target = event.target as Node | null
    if (target !== null && typeof node.contains === 'function' && node.contains(target)) return
    close(false)
  })

  return {
    root,
    setState: (state: RailState): void => {
      dot.setAttribute('data-state', state)
      dot.setAttribute('aria-label', state)
    },
    close,
  }
}

/** GaugeFigures are the three numbers the fleet gauge draws: today's notional
 * spend, the counted dollars held against the global cap, and the cap. */
export interface GaugeFigures {
  readonly notionalUsd: Reading
  readonly countedUsd: Reading
  readonly capUsd: Reading
}

/** gaugeFromBudget reads the gauge's figures out of a `budget show` document's
 * `global` block: the read the Spend and Projects views make. */
export function gaugeFromBudget(raw: unknown): GaugeFigures {
  const document = typeof raw === 'object' && raw !== null && !Array.isArray(raw) ? (raw as Record<string, unknown>) : null
  const global = document === null ? null : document['global']
  const block = typeof global === 'object' && global !== null && !Array.isArray(global) ? (global as Record<string, unknown>) : null
  const field = (name: string): Reading => readNumber(block === null ? undefined : block[name], `global.${name}`)
  const notionalUsd = field('notional_usd')
  return {
    notionalUsd: notionalUsd.value === null ? notionalUsd : notional(notionalUsd.value),
    countedUsd: field('counted_usd'),
    capUsd: field('daily_cap_usd'),
  }
}

/** GaugeView is the drawn gauge and its one writer. */
export interface GaugeView {
  readonly root: DomElement
  set(figures: GaugeFigures): void
}

/**
 * drawGauge draws the fleet gauge: the word, today's notional spend, a 96 x 6
 * bar and the global cap.
 *
 * **The bar is the counted dollars against the cap and nothing else.** The
 * redesign draws a hatched notional segment beside the leased fill; notional
 * dollars count toward no cap, so putting them on the cap's axis would add
 * notional to counted, which the redesign's own money grammar forbids. The
 * notional figure is the text beside the bar. A cap or a count that is not a
 * measurement draws an empty track, never a bar at zero.
 */
export function drawGauge(surface: DomSurface, withheld: boolean): GaugeView {
  const factory = surface.factory
  const root = factory.create('div')
  root.setAttribute('class', GAUGE_CLASS)
  root.setAttribute('role', 'group')
  root.setAttribute('aria-label', GAUGE_NAME)
  const label = factory.create('span')
  label.setAttribute('class', GAUGE_LABEL_CLASS)
  label.textContent = GAUGE_LABEL
  const notionalSpan = factory.create('span')
  notionalSpan.setAttribute('class', GAUGE_NOTIONAL_CLASS)
  const bar = factory.createNS('svg')
  bar.setAttribute('class', GAUGE_BAR_CLASS)
  bar.setAttribute('width', String(GAUGE_WIDTH))
  bar.setAttribute('height', String(GAUGE_HEIGHT))
  bar.setAttribute('viewBox', `0 0 ${String(GAUGE_WIDTH)} ${String(GAUGE_HEIGHT)}`)
  bar.setAttribute('aria-hidden', 'true')
  const track = factory.createNS('rect')
  track.setAttribute('class', GAUGE_TRACK_CLASS)
  track.setAttribute('width', String(GAUGE_WIDTH))
  track.setAttribute('height', String(GAUGE_HEIGHT))
  track.setAttribute('rx', '3')
  const fill = factory.createNS('rect')
  fill.setAttribute('class', GAUGE_FILL_CLASS)
  fill.setAttribute('width', '0')
  fill.setAttribute('height', String(GAUGE_HEIGHT))
  fill.setAttribute('rx', '3')
  bar.append(track)
  bar.append(fill)
  const cap = factory.create('span')
  cap.setAttribute('class', GAUGE_CAP_CLASS)
  root.append(label)
  root.append(notionalSpan)
  root.append(bar)
  root.append(cap)

  const set = (figures: GaugeFigures): void => {
    const shown: GaugeFigures = withheld
      ? { notionalUsd: unavailable(GAUGE_WITHHELD), countedUsd: unavailable(GAUGE_WITHHELD), capUsd: unavailable(GAUGE_WITHHELD) }
      : figures
    notionalSpan.removeAttribute('title')
    cap.removeAttribute('title')
    putReading(notionalSpan, shown.notionalUsd, 'usd')
    putReading(cap, shown.capUsd, 'usd')
    const counted = shown.countedUsd.value
    const limit = shown.capUsd.value
    const share = counted !== null && limit !== null && limit > 0 ? Math.min(1, Math.max(0, counted / limit)) : 0
    fill.setAttribute('width', String(Math.round(share * GAUGE_WIDTH * 100) / 100))
    root.setAttribute(
      'title',
      counted !== null && limit !== null && limit > 0
        ? `counted ${formatReading(shown.countedUsd, 'usd')} of the ${formatReading(shown.capUsd, 'usd')} daily cap (${formatReading(measured(share * 100), 'percent')})`
        : GAUGE_UNREAD,
    )
  }
  set({ notionalUsd: unavailable(GAUGE_UNREAD), countedUsd: unavailable(GAUGE_UNREAD), capUsd: unavailable(GAUGE_UNREAD) })
  return { root, set }
}

/** drawWordmark draws the wordmark: the three staggered strokes and the name,
 * as the control that goes to Live. It is a button for the reason the nav's
 * tabs are: the renderer writes no `href`. `home` is the click, bound by the
 * dashboard. */
export function drawWordmark(surface: DomSurface, home: () => void): DomElement {
  const factory = surface.factory
  const link = factory.create('button')
  link.setAttribute('class', SHELL_WORDMARK_CLASS)
  link.setAttribute('type', 'button')
  link.setAttribute('aria-label', 'fugaro: Live')
  const mark = factory.createNS('svg')
  mark.setAttribute('width', '24')
  mark.setAttribute('height', '24')
  mark.setAttribute('viewBox', '0 0 24 24')
  mark.setAttribute('aria-hidden', 'true')
  for (const d of ['M3 6 H15', 'M6 12 H18', 'M9 18 H21']) {
    const stroke = factory.createNS('path')
    stroke.setAttribute('d', d)
    mark.append(stroke)
  }
  const name = factory.create('span')
  name.setAttribute('class', SHELL_WORDMARK_NAME_CLASS)
  name.textContent = WORDMARK_TEXT
  link.append(mark)
  link.append(name)
  surface.nodeOf(link).addEventListener('click', () => {
    home()
  })
  return link
}

/** drawHalt draws Halt fleet: designed, and not performed (`writes.ts`). */
export function drawHalt(surface: DomSurface): DomElement {
  const factory = surface.factory
  const icon = factory.createNS('svg')
  icon.setAttribute('width', '14')
  icon.setAttribute('height', '14')
  icon.setAttribute('viewBox', '0 0 14 14')
  for (const d of ['M7 1.5 V6.5', 'M3.6 3.6 A4.8 4.8 0 1 0 10.4 3.6']) {
    const stroke = factory.createNS('path')
    stroke.setAttribute('d', d)
    icon.append(stroke)
  }
  return disabledWrite(factory, HALT_CLASS, HALT_LABEL, icon)
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

/**
 * LIVE_RIGOR_TTL_MS is how long the Live screen's rigor join is kept: 15,000 ms.
 *
 * The mode on a Live row is a **join**, not a frame field: `watch --json`
 * answers `JSONRun`, which carries no rigor block, so the mode comes from
 * `runs ls --json` and is matched to the row by run id (`rigorIndex`,
 * `shared/rigor.ts`). That read is a CLI invocation of its own, and it is
 * deliberately **not** the poll's: the poll runs at `LIVE_POLL_MS`, and a
 * `runs ls` every second would be sixty invocations a minute for a fact that is
 * written once, at the run's bootstrap.
 *
 * A block never changes after it is written, so the only thing a fresh read can
 * add is a **run that started since** -- and this TTL is how long that can go
 * unnoticed. The join is re-read when the Live view is entered and, while it is
 * on screen, when the map is older than this. A read that refuses leaves the map
 * it had (the first read leaves it empty): a run the join does not know about is
 * drawn with **no chip at all**, which is the cell a run with no block draws,
 * and never a mode taken from another run.
 */
export const LIVE_RIGOR_TTL_MS = 15_000

/**
 * RUNS_SINCE is the window every `runs ls` this console makes is read for: a day.
 *
 * The owner's rule of 2026-10-31 puts it on the Jobs section by name -- "always
 * with `--since 24h`" -- and the two other readers of the same command take it
 * for their own reasons: a run's rigor block and the run a graph node's door
 * opens are both facts about **this day's** work, and a `runs ls` that answered
 * with the whole history would be a document nobody on these screens can use.
 * The cut is the console's own: this tree has no `runs ls --limit`.
 */
export const RUNS_SINCE = '24h'

/** GRAPH_BUSY is what the Graph section's mark says it is reading: the plan.
 * The mark belongs to the operator's own controls -- a stream tab, the Refresh
 * -- and neither to the poll's background cadence nor to the run list's
 * decoration: a mark for a read nobody asked for would flicker over a picture
 * that is already true. */
export const GRAPH_BUSY = 'Reading the plan…'

/** VIEW_BUTTON_CLASS, VIEW_PANEL_CLASS and VIEW_NAV_CLASS are the console's own
 * names for the three elements the navigation is made of. They are constants
 * because a spec reads them. */
export const VIEW_NAV_CLASS = 'console-views'
export const VIEW_BUTTON_CLASS = 'console-view'
export const VIEW_PANEL_CLASS = 'console-panel'

/** PANEL_BUSY_ATTRIBUTE is on the panel while it holds a tab's placeholder: the
 * region is being replaced, and a reader that cannot see the sentence in it is
 * owed that much. `replace` takes it off again, so it is up exactly as long as
 * the placeholder is. */
export const PANEL_BUSY_ATTRIBUTE = 'aria-busy'

/**
 * VIEWS is the navigation, and it lists **only views `drawScreen` would draw**:
 * Live is always there (design C10: `live.watch` is never withheld, so a reduced
 * console still has a live screen), and each of the other two names the
 * `ReducedView` the gate knows it by. A mode that withholds a view does not
 * merely fail to mount it -- the entry does not offer it, and the read behind it
 * is never made.
 *
 * Coordinator is the project view (Bitbucket credentials T1.2, D364). Its
 * `view` is **null and not a `ReducedView`**, which is the same classification
 * `plan.status` has: the read is repository state, not money, so no mode
 * withholds it and a reduced console still offers it. It is the one view whose
 * read touches no credential of the host's, which is why it is the one a
 * console driving a CLI that cannot reach Bitbucket still draws.
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
  // The graph is never withheld either, and for the same reason the Live screen
  // is not: GV2.1 decided `plan.status` shows no spend and no cap, so a reduced
  // console has nothing to hide in it. It is `null` here rather than a fourth
  // `ReducedView`, which would be a mode that hides a view no mode hides.
  { id: 'graph', label: 'Graph', view: null },
  { id: 'spend', label: 'Spend', view: 'spend' },
  { id: 'projects', label: 'Project', view: 'caps' },
  { id: 'coordinator', label: 'Coordinator', view: null },
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

/** SPEND_CAPS_BUSY is what the caps section's mark says it is reading. The caps
 * are one read (`budget show`) and the mark names it, so a section that is busy
 * never says only "reading". */
const SPEND_CAPS_BUSY = 'Reading the budget caps…'

/**
 * spendBusyLabel is what the report section's mark says: the window and the
 * breakdown the operator just chose, so the seconds a report takes are spent
 * telling them which read is running rather than only that one is.
 *
 * Both words are the screen's own -- the picker's window and the switch's
 * breakdown, which the drawing already put on screen -- and never a value out of
 * a document.
 */
function spendBusyLabel(controls: SpendControls): string {
  return `Reading spend for ${controls.since}, by ${controls.by}…`
}

/**
 * spendViewKey is one cached view: the installation and the three controls that
 * name it. `until` is in the key as its own word rather than left out: no
 * control sets it yet, and a cached view drawn for a range the console later
 * learns to ask for differently would be a figure under the wrong window.
 */
function spendViewKey(project: string, controls: SpendControls): string {
  return cacheKey([
    project,
    controls.since,
    controls.until ?? 'today',
    controls.by,
    controls.persons ? 'persons' : 'alone',
  ])
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
  /** navigator is the window's navigator, and it is here for one question: the
   * rail's shortcut keys are Command-1 to Command-3 on macOS and Control-1 to
   * Control-3 elsewhere (design section 4.1), so `isMacPlatform` has to be able
   * to ask.
   * It is optional because a caller that drives this file under `vitest` has no
   * window -- and a scope with none answers "not macOS", which is the rule's
   * other half rather than a special case. */
  readonly navigator?: unknown
}

/**
 * drawReducedChip draws the header's Reduced-mode chip and wires the notice it
 * stands for. It is what is left on the document after the notice is put away,
 * and it is what brings the notice back.
 *
 * The notice can be put away because a whole card at the top of the window is a
 * lot of room to give a fact the operator has already read. What putting it away
 * must not do is pretend the fact gone: a reduced console withholds spend, caps
 * and the kill controls (`REDUCED_VIEWS`), and a window whose nav has no such
 * button and no word about why is the state the design's §4.4 rule 1 refuses. So
 * the chip carries the headline as its description and as its title, stays in
 * the header for as long as the mode does, and hands the notice back -- and the
 * keyboard with it, onto the card's own title.
 *
 * The state is the renderer's memory and nothing else: the dismissal is the
 * notice root's `hidden` attribute, so nothing is written to storage and nothing
 * is asked of the main process, and a reload is a fresh window that draws the
 * notice again. Escape is deliberately not a way out -- the card is not a
 * dialog, and the switcher's own Escape handling is its own.
 */
function drawReducedChip(surface: DomSurface, header: SkewElement, notice: DrawnNotice): void {
  const chip = surface.factory.create('button')
  chip.setAttribute('class', REDUCED_CHIP_CLASS)
  chip.setAttribute('type', 'button')
  chip.setAttribute('data-severity', 'warning')
  chip.setAttribute('title', SKEW_HEADLINES.reduced)
  chip.setAttribute('aria-describedby', REDUCED_CHIP_REASON_ID)
  chip.textContent = REDUCED_CHIP_LABEL
  header.append(chip)

  // Visually hidden and not `aria-hidden`: the chip's visible word is two words
  // long, and what the mode *is* -- the headline the dismissed card carried --
  // is what a reader who only reaches the chip is owed.
  const reason = surface.factory.create('span')
  reason.setAttribute('class', REDUCED_CHIP_REASON_CLASS)
  reason.setAttribute('id', REDUCED_CHIP_REASON_ID)
  reason.textContent = SKEW_HEADLINES.reduced
  header.append(reason)

  const showNotice = (): void => {
    notice.root.removeAttribute('hidden')
    // A heading is not focusable without `tabindex="-1"`, which the card draws
    // on its title for exactly this move.
    ;(surface.nodeOf(notice.title) as unknown as { focus(): void }).focus()
  }
  surface.nodeOf(chip).addEventListener('click', showNotice)

  const dismiss = notice.dismiss
  if (dismiss !== null) {
    surface.nodeOf(dismiss).addEventListener('click', () => {
      // The card stays where it is and keeps its place in the document -- the
      // body's first child -- and only its `hidden` attribute changes.
      notice.root.setAttribute('hidden', '')
      chip.focus()
    })
  }

  const toggle = notice.toggle
  if (toggle === null) return
  surface.nodeOf(toggle).addEventListener('click', () => {
    const open = toggle.getAttribute('aria-expanded') === 'true'
    toggle.setAttribute('aria-expanded', open ? 'false' : 'true')
    toggle.textContent = open ? NOTICE_SHOW_DETAILS : NOTICE_HIDE_DETAILS
    if (open) notice.details.setAttribute('hidden', '')
    else notice.details.removeAttribute('hidden')
  })
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
 * and the panel with the Live screen in it, and the reduced mode's chip in the
 * header between the switcher and the navigation (`drawReducedChip`). A refused
 * console draws the refusal and **stops**: no rail, no installation line, no
 * navigation, no panel, and not one read -- which is the whole of D7 from this
 * side.
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
  const railBridge = railBridgeFrom(scope.fugaro)
  const listed = await railBridge.listInstallations({})
  const listedRows = railRows(listed)
  const rows = listedRows ?? currentRailRows(installation)
  const onboarding = railOnboarding(listed)
  // The rail is drawn before the dashboard for the DOM order, and a click on it
  // is answered by the dashboard's own views: `mountDashboard` binds `activate`.
  const wiring: RailWiring = { activate: () => undefined, home: () => undefined }
  // The switcher is drawn after the rail it holds, so the rail's first state
  // word reaches it through this holder rather than through a view not yet made.
  const status: { switcher: SwitcherView | null; word: RailState } = { switcher: null, word: 'running' }
  const rail = drawRail(
    surface,
    rows,
    (row, byKeyboard) => {
      status.switcher?.close(false)
      wiring.activate(row, byKeyboard)
    },
    () => {
      status.switcher?.close(false)
      void railBridge.openInstallations({})
    },
    (shown) => {
      status.word = shown
      status.switcher?.setState(shown)
    },
  )
  const header = surface.factory.create('header')
  header.setAttribute('class', SHELL_HEADER_CLASS)
  header.append(
    drawWordmark(surface, () => {
      wiring.home()
    }),
  )
  const switcher = drawSwitcher(
    surface,
    scope.document,
    switcherLine(installation),
    drawInstallation(surface.factory, installation),
    rail.root,
  )
  status.switcher = switcher
  switcher.setState(status.word)
  header.append(switcher.root)
  // After the switcher and before the nav the dashboard draws into the same
  // header: the chip belongs to the installation the window is about, and it is
  // in the flow of the header rather than inside the switcher's own control.
  if (gate.notice !== null && gate.compatibility.mode === 'reduced') {
    drawReducedChip(surface, header, gate.notice)
  }
  surface.mount.append(header)
  await mountDashboard({
    header,
    document: scope.document,
    surface,
    gate,
    bridge,
    rail,
    railBridge,
    launchBridge: launchBridgeFrom(scope.fugaro),
    rows,
    railListed: listedRows !== null,
    onboarding,
    wiring,
    navigator: scope.navigator,
  })
}

/** RailWiring is the rail's click, bound by `mountDashboard` once the views a
 * click can show exist. */
interface RailWiring {
  activate: (row: RailRow, byKeyboard: boolean) => void
  /** home is the wordmark's click: the Live view. */
  home: () => void
}

/**
 * currentRailRows is the rail a console draws when its list read failed: the one
 * installation `installation.current` named, readable and open, or no row at all
 * -- never an invented one. A failed `config list` is not a reason to hide the
 * installation the console is reading.
 */
export function currentRailRows(installation: unknown): readonly RailRow[] {
  const answer = replyData(installation)
  const value =
    answer.ok && typeof answer.data === 'object' && answer.data !== null && !Array.isArray(answer.data)
      ? (answer.data as Record<string, unknown>)
      : null
  const project = value === null ? undefined : value['project']
  if (value === null || value['chosen'] !== true || typeof project !== 'string' || project === '') {
    return Object.freeze([])
  }
  return Object.freeze([
    Object.freeze({
      project,
      issue: 'none' as const,
      readable: true,
      sentence: '',
      command: '',
      details: Object.freeze([]),
      open: true,
      active: true,
      saved: true,
      checkout: null,
      checkouts: Object.freeze([]),
      // `installation.current` carries no repository list -- the CLI's list is
      // the only read that does -- so a console whose list read failed offers no
      // repository to launch in, and its launch control says so rather than
      // guessing at one.
      repositories: Object.freeze([]),
    }),
  ])
}

/**
 * LaunchNow is the drawn launch control, the installation it was built for and
 * whether a call is out.
 *
 * The control is **built once per offer of repositories and kept**, which is
 * what makes a form survive the one-second Live poll: the poll replaces the
 * panel, and a control rebuilt on every frame would be a form that emptied
 * itself while the operator was typing. "The same offer" is the installation
 * **and** the repository identities it records: a row's list can change while
 * its project does not -- a checkout added or removed under the open
 * installation, whose answer replaces the rail rows without dropping the Live
 * screen -- and a kept control would then offer a list the installation no
 * longer has, refusing the repository the operator picked from the CLI's own
 * list. Rebuilding it is also the whole of "switching installation clears the
 * form and any drawn outcome" (`sameOffer` and `launchNowOf` below).
 */
interface LaunchNow {
  readonly project: string
  readonly view: LaunchView
  readonly control: LaunchControl
  busy: boolean
}

/** DashboardDeps is what the dashboard half of `bootDashboard` needs, named so
 * that the wiring can be read -- and driven -- apart from the startup. */
interface DashboardDeps {
  /** header is the shell's header row: the wordmark and the switcher are in
   * it, and the dashboard appends the navigation, the gauge and Halt fleet. */
  readonly header: DomElement
  readonly document: Document
  readonly surface: DomSurface
  readonly gate: ScreenGate
  readonly bridge: ReadBridge
  /** rail is the installation rail, drawn before the dashboard was mounted and
   * updated from here: the Live half owns its state word, the two views that
   * read the budget fill its spend, and the shortcut keys select its rows. */
  readonly rail: RailView
  /** railBridge, rows and wiring are the rail's own operations, the rows it was
   * first drawn with, and the click this function binds. */
  readonly railBridge: RailBridge
  /** launchBridge is the Live view's one write (`run.launch`, V2.5.1b), the
   * only operation of this console that spends money. */
  readonly launchBridge: LaunchBridge
  readonly rows: readonly RailRow[]
  /** railListed is whether `rows` is the CLI's own list answer rather than the
   * fallback for a list this console could not read (`currentRailRows`). The
   * two are not the same fact: a fallback row carries an empty `checkouts`
   * because nothing was read, and the Graph's no-checkout card decides whether
   * it has an action from whether the CLI listed one. */
  readonly railListed: boolean
  /** onboarding is the screen the list answer names for a machine with no
   * installation, or `null`. */
  readonly onboarding: RailOnboarding | null
  readonly wiring: RailWiring
  /** navigator is the window's, for the one question the shortcut keys ask of
   * the machine -- which modifier they are spelled with (design section 4.1:
   * Command on macOS, Control elsewhere). It is the scope's rather than a global
   * so a case can drive both platforms (`isMacPlatform`). */
  readonly navigator: unknown
}

/**
 * mountDashboard builds the navigation and the panel, draws the default view,
 * and starts the Live poll. It answers when the **first** Live read has been
 * drawn, so a caller that awaits `bootDashboard` is looking at a dashboard with
 * figures in it rather than at an empty panel that fills in later.
 */
async function mountDashboard(deps: DashboardDeps): Promise<void> {
  const { document, surface, gate, bridge, rail, railBridge, launchBridge, wiring, navigator, header } = deps
  const factory = surface.factory
  const theme = CONSOLE_THEME

  // The Live screen's own state, kept across polls: the burn window accumulates
  // samples, so it must outlive a drawing. A fresh one per poll would be a
  // one-bar sparkline forever.
  let history = new BurnHistory()
  const live: {
    drawn: Drawn | null
    inFlight: boolean
    timer: ReturnType<typeof setInterval> | null
    /** the rigor join: `runs ls --json`'s blocks by run id (`rigorIndex`), the
     * stamp the read that built it started at, and whether one is in flight. */
    rigor: ReadonlyMap<string, RigorView>
    rigorAt: number
    rigorInFlight: boolean
    /** reply is the last `live.watch` answer this screen drew, held so the rigor
     * join's own read can redraw the frame it belongs to: the two are separate
     * reads (the frame's and the join's) and the join's answer is what the column
     * waits for. */
    reply: unknown
  } = {
    drawn: null,
    inFlight: false,
    timer: null,
    rigor: new Map(),
    rigorAt: 0,
    rigorInFlight: false,
    reply: null,
  }

  /** panel is where every screen is drawn. It is created here and appended
   * after the navigation below, so the page reads nav-then-screens. */
  const panel = factory.create('div')
  panel.setAttribute('class', VIEW_PANEL_CLASS)

  /** replace clears the panel and draws one screen into it. The panel is this
   * file's own element, so clearing it is `replaceChildren` on the node behind
   * it -- a write no screen makes, because a screen draws one tree and owns none
   * of the page.
   *
   * **It is also what ends a read's placeholder.** Every drawing in this file
   * comes through here -- a screen, a refusal, the issue view, the console's own
   * onboarding -- so this is the one place the still-reading timer can be
   * disarmed: the read has landed, or the operator has moved on, and a line
   * armed for a panel that is no longer there must not arrive into it. */
  const replace = (drawn: SkewElement): void => {
    endReading()
    surface.nodeOf(panel).removeAttribute(PANEL_BUSY_ATTRIBUTE)
    surface.nodeOf(panel).replaceChildren(surface.nodeOf(drawn))
  }

  /** readingTimer is the one timer behind the placeholder's still-reading line.
   * There is never more than one: a second `placeReading` replaces the first, and
   * `replace` clears it, so a line can only arrive into the placeholder its own
   * read put up. */
  let readingTimer: ReturnType<typeof setTimeout> | null = null

  const endReading = (): void => {
    if (readingTimer === null) return
    clearTimeout(readingTimer)
    readingTimer = null
  }

  /**
   * placeReading puts the placeholder up for a read that has not landed yet, and
   * arms the one line that says so when the read is slow.
   *
   * **It is called where a read starts and nowhere else**, and that is the whole
   * of this fix: the panel used to hold the last view's drawing -- the Graph's
   * refusal, most visibly -- until the new read answered, which is a screen
   * naming one tab and holding another's answer. A view that redraws a model
   * this file already holds is not one of these (Live's frame, the Graph's
   * picture), and asking for one there would take a screen down to put the
   * placeholder up.
   *
   * `mine` is the generation of the read that is starting. The placeholder itself
   * is drawn unconditionally -- it is what the operator is looking at while the
   * read runs -- and the timer is the half that is guarded: a line whose read was
   * left behind is not added to anything, which matters because the timer's
   * callback is the one thing here that is not a read's own answer.
   */
  const placeReading = (tab: ReadingTab, mine: number): void => {
    const drawn = renderReading(factory, tab, theme)
    replace(drawn.root)
    // The panel itself is busy, not only the sentence in it: a reader who cannot
    // see the drawing is told that the region is being replaced, which is what
    // `aria-busy` on a live region is for.
    surface.nodeOf(panel).setAttribute(PANEL_BUSY_ATTRIBUTE, 'true')
    readingTimer = setTimeout(() => {
      readingTimer = null
      // `replace` already disarmed this timer for every read that landed, so a
      // drawing that arrived is never read over. What is left is the operator:
      // `show` claims, and a placeholder that is still up while the generation
      // moved is the panel of a view nobody is on.
      //
      // That claim draws through `replace` in the same turn it makes (Live's
      // frame, the Graph's picture, the next tab's placeholder), so this check
      // is layered behind `endReading` above and no case can tell it from its
      // absence: removing it alone fails nothing. It stays because the rule it
      // keeps is this file's own -- a line armed for a read is only ever added
      // to the drawing that read put up.
      if (stale(mine)) return
      drawn.body.append(drawn.still)
    }, READING_STILL_MS)
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
  // The gauge is drawn with the header below; a read that lands before it is
  // drawn has nowhere to go, and the first poll is after it.
  let setGauge: (figures: GaugeFigures) => void = () => undefined

  /**
   * syncPolling is the one place the interval is started and stopped, so the two
   * conditions -- a view that polls is the one on screen, and the window is
   * visible -- cannot be applied in one place and forgotten in the other. A
   * hidden view or a hidden window is not a reason to keep spawning reads nobody
   * is looking at.
   *
   * **Two views poll**: Live, because a live screen is a poll, and Graph, because
   * a graph draws the same feed's stages (GV2.3b). The graph's *plan* is a
   * different read at a different cadence, and `maybeRefreshGraph` is where that
   * one is decided.
   */
  const syncPolling = (): void => {
    const wanted = (currentView === 'live' || currentView === 'graph') && scopeVisible()
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
   * markButtons marks the view on screen: `aria-current="page"` on its button
   * and on no other. The buttons are ordinary tab stops (the redesign's tabs,
   * 2026-10-28), so there is no roving `tabindex` to keep. Run detail and the
   * issue view have no button, and while one is up no button is marked, which
   * is the truth about the navigation.
   */
  const markButtons = (id: string): void => {
    for (const [key, button] of buttons) {
      if (key === id) button.setAttribute('aria-current', 'page')
      else button.removeAttribute('aria-current')
    }
  }

  /**
   * markCurrent is the navigation's one writer: it records which view is on
   * screen, marks its buttons, and invalidates whatever was in flight for the
   * view before it. Run detail is a view without a button -- it is reached from
   * a run row -- so every button is unmarked while it is up, which is the truth
   * about the navigation: none of the offered views is the one on screen.
   *
   * It answers the generation it claimed, because the switch itself is a door
   * that holds a drawing: `show` hands it to `placeReading` for the views whose
   * read has not started yet.
   */
  const markCurrent = (id: string): number => {
    const mine = claim()
    markButtons(id)
    currentView = id
    syncPolling()
    return mine
  }

  /** leaveSpend is what `show` calls when the operator moves to another tab. It
   * is the Spend section's own (`showSpend` sets it), and it is a variable here
   * for the reason `setGauge` is one: the switch is written above the screen it
   * switches away from, and a screen's own state is not the navigation's to
   * know. */
  let leaveSpend: () => void = () => undefined

  /** leaveCoordinator is the same door for the Coordinator screen, and it also
   * stops the chunks the screen's detail is drawn in: a repository that has not
   * been drawn yet belongs to a tab nobody is looking at. */
  let leaveCoordinator: () => void = () => undefined

  /** leaveProjects is the same door for the Projects screen's one section:
   * `showProjects` sets it. */
  let leaveProjects: () => void = () => undefined

  /** leaveGraph is the same door for the Graph: its section's mark belongs to
   * the tab the operator is on, and the Graph's own controls put one up. */
  let leaveGraph: () => void = () => undefined

  /** show switches the panel to one view. The Live screen is not re-read here --
   * its model is the one the last poll drew, which is the point of a live
   * screen -- and the other three read on entry. The Live screen's **rigor join**
   * is not its frame and is not re-read by the poll either, so it is asked for
   * here: entering the view is the moment a reader is looking at run rows, and
   * `maybeRefreshRigor` decides whether the map is old enough to be worth a
   * read.
   *
   * **The panel never keeps the last tab's answer.** A view that has something to
   * redraw draws it (Live's frame, the Graph's picture); a view whose read is
   * about to start says what it is waiting for (`placeReading`), so the refusal
   * the Graph left on screen cannot outlive the tab that chose it. The claim is
   * taken here, before any of that, and it is the generation the placeholder and
   * the read are held to. */
  const show = (id: string): void => {
    const mine = markCurrent(id)
    rail.view(null)
    // A section's busy mark and its fifteen-second line belong to the tab the
    // operator is on: leaving Spend ends both, so nothing that was armed for a
    // screen nobody is looking at is left armed.
    if (id !== 'spend') leaveSpend()
    if (id !== 'projects') leaveProjects()
    if (id !== 'coordinator') leaveCoordinator()
    if (id !== 'graph') leaveGraph()
    if (id === 'live') {
      maybeRefreshRigor()
      // A console that opened on an issue has made no Live read yet.
      if (live.drawn !== null) {
        replace(live.drawn)
        placeLaunch()
      } else {
        placeReading('live', mine)
        void pollLive()
      }
      return
    }
    if (id === 'graph') void showGraph()
    if (id === 'spend') void showSpend()
    if (id === 'projects') void showProjects()
    if (id === 'coordinator') void showCoordinator()
  }

  // The navigation is the redesign's five tabs, in the header after the
  // switcher. The redesign draws them as links; they stay buttons because the
  // renderer never writes an `href` (design section 2: a URL goes to
  // `data-href`, and the fake document refuses the attribute outright), and a
  // button is the native control for an in-page switch. A click is `show`, and
  // the button's own Enter and Space are the same click.
  const nav = factory.create('nav')
  nav.setAttribute('class', VIEW_NAV_CLASS)
  nav.setAttribute('aria-label', 'Console')
  header.append(nav)

  for (const entry of VIEWS) {
    // The gate is asked before the link exists: a view this mode withholds is
    // not offered at all, and the read behind it is never made.
    if (entry.view !== null && hidesView(gate.compatibility, entry.view)) continue
    const button = factory.create('button')
    button.setAttribute('class', VIEW_BUTTON_CLASS)
    button.setAttribute('type', 'button')
    button.setAttribute('data-view', entry.id)
    button.textContent = entry.label
    nav.append(button)
    buttons.set(entry.id, button)
    surface.nodeOf(button).addEventListener('click', () => {
      show(entry.id)
    })
  }
  wiring.home = (): void => {
    show('live')
    buttons.get('live')?.focus()
  }

  // The gauge and Halt fleet close the header row. The gauge's figures are the
  // global budget the console already reads: the Live frame's total on every
  // poll, and `budget show`'s global block when Spend or Projects reads it.
  const gauge = drawGauge(surface, hidesView(gate.compatibility, 'caps'))
  setGauge = gauge.set
  header.append(gauge.root)
  header.append(drawHalt(surface))

  // The view on screen is marked from the first paint, not only after the first
  // click: `markButtons` writes these attributes on every switch, and a
  // navigation that carried them only once something had been clicked would draw
  // a console whose current view is unmarked and whose tab stop is nowhere.
  markButtons(currentView)

  const page = factory.create('main')
  page.setAttribute('class', SHELL_PAGE_CLASS)
  page.append(panel)
  surface.mount.append(page)

  // ---- Live -------------------------------------------------------------

  /**
   * refreshRigor makes one `runs ls --json` read and keeps the join it answers
   * with (`LIVE_RIGOR_TTL_MS` says why the Live screen needs one at all).
   *
   * **At most one call is in flight**, the rule `pollLive` keeps and for the
   * same reason: the flag is set before the await and cleared in a `finally`, so
   * a slow read cannot be overtaken by the next tick. `rigorAt` is stamped
   * **before** the read, so a read that refuses waits a whole TTL rather than
   * being retried on the next tick of a one-second poll.
   *
   * A refusal is **not** a refusal screen. The mode is one cell of a row and the
   * rows are the frame's: replacing a live screen with "this read failed"
   * because the run list could not be read would hide the run that is spending
   * money now, which is the one thing this screen exists to show. So the map
   * stays as it was -- empty, if this was the first read -- and the rows draw no
   * chips.
   */
  const refreshRigor = async (): Promise<void> => {
    if (live.rigorInFlight) return
    live.rigorInFlight = true
    live.rigorAt = Date.now()
    try {
      const answer = documentOf(await bridge.listRuns({ since: RUNS_SINCE }))
      if (answer.ok) live.rigor = rigorIndex(answer.data)
      // **The column fills in the moment the join lands** and not on the next
      // tick of the poll: the rows are drawn without it (a run the join does not
      // know about draws no chip at all, which is the cell a run with no block
      // draws), and a read that took most of the second would otherwise leave the
      // column empty for another one. The frame is the one the poll last drew, so
      // what is redrawn is the same frame with its rigor cells filled.
      if (answer.ok && currentView === 'live' && live.reply !== null) drawLive(live.reply)
    } catch {
      // `readBridgeFrom` answers `null` rather than rejecting, so reaching here
      // is a bridge that threw where it should not have: the map stays as it
      // was, which is the same cell a refused read draws.
    } finally {
      live.rigorInFlight = false
    }
  }

  /** maybeRefreshRigor is the one place the TTL is applied, so entering the view
   * and the poll cannot disagree about when the join is due. */
  const maybeRefreshRigor = (): void => {
    if (Date.now() - live.rigorAt < LIVE_RIGOR_TTL_MS) return
    void refreshRigor()
  }

  /**
   * pollLive makes one `live.watch` read and draws it. **At most one call is in
   * flight**: the flag is set before the await and cleared in a `finally`, so a
   * slow read cannot be overtaken by the next tick of the interval. The flag is
   * this file's rather than the bridge's because the bridge is `invoke`-only and
   * an unanswered call is invisible to it.
   *
   * The tick also asks whether the rigor join is due. It is asked here and not
   * from a second interval because this is the only thing that runs while the
   * view is on screen, and `syncPolling` is already the one place that decides
   * whether anything should be running at all.
   */
  const pollLive = async (): Promise<void> => {
    if (live.inFlight) return
    live.inFlight = true
    try {
      const frame = await bridge.subscribeLive({})
      // One read feeds two views. The frame is the Live screen's, and it is also
      // the graph's: a node's stage is the frame's stage for the run the plan
      // joined it to (GV2.3b), so the graph takes the same rows rather than a
      // second `live.watch` of its own.
      if (currentView === 'graph') await frameGraph(frame)
      else drawLive(frame)
      // The rigor join is asked for **after** the frame is drawn, so a read that
      // refuses cannot keep a frame off the screen: what the poll is for is the
      // frame, and the join is one cell of a row that the next frame draws with.
      maybeRefreshRigor()
      // The plan is the graph's other input, and it is read on its own cadence.
      if (currentView === 'graph') maybeRefreshGraph()
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
    live.reply = reply
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
    const model = buildLiveModel(decoded.value, history, feed, live.rigor)
    setGauge({ notionalUsd: model.total.notionalUsd, countedUsd: model.total.countedUsd, capUsd: model.total.dailyCapUsd })
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
    placeLaunch()
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
   *
   * **The review inbox's rows are the same door** (v2 plan Task V2.7.1):
   * `live-review-run-id` is a run id drawn the same way, and `model.review` is
   * where it is checked. A finished run is a run -- `fugaro diagnose RUN` reads
   * it from the runs bucket precisely because its registry entry is gone -- so
   * a ready row that could not be opened would be the one run row on this
   * screen with nothing behind it.
   */
  const wireRuns = (drawn: Drawn, model: LiveModel): void => {
    const known = new Set<string>()
    for (const repo of model.repos) for (const row of repo.runs) known.add(row.run)
    for (const row of model.orphans) known.add(row.run)
    for (const row of model.review) known.add(row.run)
    const door = (rowClass: string, idClass: string): void => {
      for (const row of classed(drawn, rowClass)) {
        const id = classed(row, idClass)[0]?.textContent ?? ''
        if (!known.has(id)) continue
        surface.nodeOf(row).addEventListener('click', (event: Event) => {
          // **A write the row draws is not the door.** A lost run's Reap is
          // drawn `aria-disabled` rather than `disabled` so a keyboard reader
          // reaches it and hears why it does nothing (`writes.ts`) -- which is
          // what lets its click fire and bubble to this listener. The row is a
          // door to the run, and a click that landed on a button inside it is
          // the button's, not the run's.
          const target = event.target as Element | null
          if (target !== null && typeof target.closest === 'function' && target.closest('button') !== null) {
            return
          }
          void showRun(id)
        })
      }
    }
    door('live-run', 'live-run-id')
    door('live-review-row', 'live-review-run-id')
  }

  // ---- The graph (GV2.3b) -----------------------------------------------

  /**
   * graph is the Graph view's own state: the screen (and with it the layout
   * cache and the worker, built when the view is first opened and dropped when
   * the installation changes), when the plan was last read, and whether a plan
   * read is in flight.
   *
   * The **read is the entry's**, not the screen's: a screen is a function of the
   * documents it is handed, and which document to read and how often is this
   * file's business -- the same division `showSpend` and `showRun` keep.
   */
  const graph: {
    screen: GraphScreen | null
    readAt: number
    inFlight: boolean
    /** queued is the claim of the operator's own Refresh when it arrived while a
     * read was outstanding, and `null` when no read is waiting: at most **one**
     * read is re-queued however many times the control is pressed. */
    queued: number | null
    /** queuedToken is the section token that queued claim was taken with, so the
     * re-queued read keeps the mark its own press put up. */
    queuedToken: number | null
    /** runs is the run list the last decoration answered, held for the section
     * the next visit draws while its own runs read is out: a node's door is worth
     * the list this console already has rather than one it is waiting for. */
    runs: readonly string[]
    /** decorating is whether a run-list read is outstanding. It is not the plan's
     * in-flight flag: the two are different reads of the same tab, and the
     * decoration must not hold up the plan's cadence. */
    decorating: boolean
  } = {
    screen: null,
    readAt: 0,
    inFlight: false,
    queued: null,
    queuedToken: null,
    runs: [],
    decorating: false,
  }

  /** GraphHeld is what the Graph's session cache keeps per installation: the
   * plan document and the run list the last read answered. It is memory and
   * nothing else, and `forgetLive` empties it. */
  interface GraphHeld {
    readonly document: PlanStatusView
    readonly runs: readonly string[]
  }

  const graphViews = resultCache<GraphHeld>(CACHE_LIMIT)

  /** graphRead is the Graph's section read lifecycle: the mark the operator's
   * own controls put up in the frame they were pressed in, the sequence token
   * that keeps the latest press's answer, and the fifteen-second line. */
  const graphRead = sectionRead({ factory, theme })

  /** leaveGraph ends the section's busy state when the operator moves to another
   * tab: nothing is in flight for a screen nobody is looking at. */
  leaveGraph = () => {
    graphRead.end()
  }

  /** graphSlot is the slot the current drawing's mark and stamp go into, or
   * `null` when the panel holds no drawing of this Graph (the placeholder, a
   * refusal, or another screen) -- in which case there is nothing to mark and
   * nothing to stamp, and the placeholder is already saying a read is running. */
  const graphSlot = (): SkewElement | null => {
    const tree = graphTree
    if (tree === null) return null
    return classed(tree, BUSY_SLOT_CLASS)[0] ?? null
  }

  /** graphScreenNow is the screen, built on first use. The worker is started
   * here and nowhere else, and `forgetLive` disposes of it. */
  const graphScreenNow = (): GraphScreen => {
    if (graph.screen === null) graph.screen = graphScreen(factory, theme)
    return graph.screen
  }

  /**
   * readGraph makes one `plan.status` read and draws it.
   *
   * **At most one call in flight**, the rule every other read here keeps: the
   * flag is set before the await and cleared in a `finally`. `readAt` is stamped
   * **before** the read, so a read that refuses waits a whole interval rather
   * than being retried on the next tick of the one-second poll.
   *
   * A refusal is a refusal screen, and here that is the right answer: the graph
   * *is* the plan, and a plan the console cannot read is a screen with nothing on
   * it. That is not the Live screen's rule -- a frame that fails leaves the last
   * frame up -- because a frame is a moment and a plan is the whole picture.
   */
  const readGraph = async (mine: number, token: number | null): Promise<void> => {
    if (graph.inFlight) {
      // A read that is already outstanding is not a reason to drop the
      // operator's: the one in flight may have started before the change they
      // want to see, so their press is held as **one** read to make when it
      // settles. The claim is kept rather than the read: a console that moved on
      // in the meantime has bumped the generation, and the queued read is then
      // stale and draws nothing.
      graph.queued = mine
      graph.queuedToken = token
      return
    }
    graph.inFlight = true
    graph.readAt = Date.now()
    // The mark goes up **before the await**, in the frame the operator pressed
    // the control in, over the picture already drawn -- which is the other half
    // of the owner's rule of 2026-10-31. `token` is `null` for a read nobody
    // asked for (the poll's own cadence): a mark for that would flicker over a
    // picture that is already true.
    if (token !== null) markGraph(token)
    try {
      const plan = documentOf(await bridge.planStatus({ all: true }))
      // The section's token is settled before the refusal is drawn, for the
      // reason every other screen gives: the mark must come down for the
      // operator's own read, and a read an earlier press started must not take
      // down the mark a later one put up.
      if (token !== null && !graphRead.settle(token)) return
      // Before the refusal, not after it: a refusal is a screen of its own, and
      // one drawn for a generation that has passed is the operation cell
      // `plan.status` under a tab that asked for another read.
      if (stale(mine)) return
      if (!plan.ok) {
        refuseGraph(plan.sentence)
        return
      }
      const decoded = decodePlanStatus(plan.data, 'plan.status')
      if (!decoded.ok) {
        refuseGraph(decoded.skew.because)
        return
      }
      // **The canvas is drawn from the plan alone.** The plan is the graph, and
      // `runs ls` is the decoration a node's door comes from: a screen that
      // waited for both before drawing anything took the sum of the two reads
      // before it answered at all (the owner's report of 2026-10-31). What this
      // session already read decorates it at once, and the fresh list follows in
      // `decorateRuns` below.
      const drawn = await graphScreenNow().load(decoded.value, graph.runs)
      if (stale(mine) || drawn === null) return
      // A read names no gesture: the keyboard stays where it was, and a reader
      // who is not in the graph is not pulled into it.
      redraw(drawn, stopNow())
      graphViews.put(graphKey(), { document: decoded.value, runs: graph.runs }, Date.now())
      void decorateRuns(mine, decoded.value)
    } catch {
      // `readBridgeFrom` answers `null` rather than rejecting, so reaching here
      // is a throw this file did not expect -- `graphScreenNow` building its
      // worker, which is the same turn as the run-list guard above. The
      // generation check is therefore layered behind that guard and cannot be
      // told from its absence on its own; it stays because the guard above is
      // about the run list and this one is about the read as a whole.
      if (!stale(mine)) refuseGraph(READ_UNREADABLE)
    } finally {
      graph.inFlight = false
      const queued = graph.queued
      const queuedToken = graph.queuedToken
      graph.queued = null
      graph.queuedToken = null
      if (queued !== null) void readGraph(queued, queuedToken)
    }
  }

  /** graphKey is one installation's Graph entry in the session cache. The cache
   * is per installation like every other one here, and `forgetLive` empties it
   * with the rest: a plan of the old installation is never drawn under the new
   * one's positions. */
  const graphKey = (): string =>
    cacheKey([railNow.find((row) => row.open)?.project ?? '', 'graph'])

  /** markGraph puts the section's busy mark up over the picture on screen, for
   * one token. It is a no-op when the panel holds no drawing of this Graph: the
   * placeholder is already saying a read is running, and a mark over a refusal
   * would be a mark over a sentence that is an answer. */
  const markGraph = (token: number): void => {
    const slot = graphSlot()
    const tree = graphTree
    if (slot === null || tree === null) return
    graphRead.mark(token, tree, slot, GRAPH_BUSY)
  }

  /**
   * stampGraph draws the cached view's line and its Refresh into the drawing's
   * own slot: when this view was read, and the one control that reads it again.
   * A figure that arrived without a read is never silently old (the owner's rule
   * of 2026-10-31), and the Refresh is the door the operator's own doubt goes
   * through.
   */
  const stampGraph = (at: number): void => {
    const slot = graphSlot()
    if (slot === null) return
    const stamp = renderStamp(factory, at)
    slot.append(stamp.root)
    surface.nodeOf(stamp.refresh).addEventListener('click', () => {
      void readGraph(claim(), graphRead.start())
    })
  }

  /**
   * decorateRuns reads the console's run list -- `runs ls --since 24h`, the same
   * read the Live screen's rigor join makes and **not** through that join, which
   * drops every row it cannot show a rigor block for -- and attaches it to the
   * picture already on screen.
   *
   * **It is the read that decorates and never the read that decides.** A graph
   * whose canvas is up is a graph the operator can read; the list is what a
   * node's door resolves four characters of a run id against, so it arrives
   * after the plan and nothing waits for it. A refusal is not a refusal screen
   * and not a reason to take the doors away: the list this session already holds
   * stays, and a console that never read one draws nodes with no door and the
   * sentence saying so (`GRAPH_NO_RUN_LISTED`).
   */
  const decorateRuns = async (mine: number, document: PlanStatusView): Promise<void> => {
    if (graph.decorating) return
    graph.decorating = true
    try {
      const listed = documentOf(await bridge.listRuns({ since: RUNS_SINCE }))
      // The generation is checked **after** the read and never before the mark,
      // which is not this read's: a decoration nobody is waiting for must not
      // replace a view the operator has moved to.
      if (stale(mine) || !listed.ok) return
      const runs = runIdsIn(listed.data)
      const drawn = graphScreenNow().decorate(runs)
      if (stale(mine) || drawn === null) return
      graph.runs = runs
      redraw(drawn, stopNow())
      graphViews.put(graphKey(), { document, runs }, Date.now())
    } catch {
      // As above: a bridge that threw where it should not have leaves the
      // doors as they were, which is the same drawing a refused read makes.
    } finally {
      graph.decorating = false
    }
  }

  /** refuseGraph is the refusal screen and the forgetting together: a plan the
   * console could not read must not be redrawn from the plan it read before it,
   * so the screen drops the document it holds as well as the panel drawing it
   * (GV2.3b, `GraphScreen.forget`). The drawing it held goes too, so a redraw
   * that comes later asks the keyboard about the tree the panel really has.
   *
   * **A read refused for want of a checkout is not a refusal screen.** The
   * sentence is `src/source/cli.ts`'s precondition for this one read -- no plan
   * document can be read because the installation names no directory to read it
   * from -- and it is a state of the installation with one action, not a failure
   * of the console or of the CLI; `drawNoCheckout` draws it as such and keeps the
   * CLI's sentence in a disclosure. **That is the only sentence routed here**:
   * every other refusal, including one the console could not read at all, is the
   * refusal screen it always was. */
  const refuseGraph = (sentence: string): void => {
    graph.screen?.forget()
    graphTree = null
    if (sentence === NO_CHECKOUT_SENTENCE) {
      drawNoCheckout(sentence)
      return
    }
    refuse('plan.status', sentence)
  }

  /**
   * drawNoCheckout draws the Graph's no-checkout card and wires its one action.
   *
   * The action is the installation picker's own door, and it is opened the way
   * the rail's `+` opens it: `installation.open`, the picker's window, where the
   * checkout chooser already is. There is no new channel, no new bridge method
   * and no new argument -- a card in the panel is not a reason to widen the IPC
   * allow-list, and the chooser this button needs is one the console already
   * ships.
   */
  const drawNoCheckout = (sentence: string): void => {
    // The card's action opens the picker, and the picker's only word about a
    // checkout is one `Use <dir>` per checkout the CLI listed for the row. So
    // the action is offered for an installation the CLI listed a checkout for,
    // and the card says what to do instead when the CLI listed none -- while a
    // console that never read the list at all keeps the action, because "no
    // checkout is recorded" is not a claim it can make.
    const open = railNow.find((row) => row.open)
    const choosable = !deps.railListed || open === undefined || open.checkouts.length > 0
    const drawn = renderNoCheckout(factory, sentence, theme, { choosable })
    replace(drawn.root)
    if (drawn.choice === null) return
    surface.nodeOf(drawn.choice).addEventListener('click', () => {
      void railBridge.openInstallations({})
    })
  }

  /** maybeRefreshGraph is the one place the plan's cadence is applied, so
   * entering the view, the poll and the operator's refresh cannot disagree about
   * when a read is due. */
  const maybeRefreshGraph = (): void => {
    if (graph.inFlight) return
    if (Date.now() - graph.readAt < GRAPH_REFRESH_MS) return
    // **No token**: the cadence is nobody's press, and a mark put up for it would
    // flicker over a picture the operator is reading.
    void readGraph(claim(), null)
  }

  /**
   * showGraph opens the Graph view: the screen is built if it is not up yet, the
   * picture the last read drew goes back up (the Live screen's own rule -- a
   * screen whose model this file already holds is not blank while a read is on
   * its way), and the plan is read on entry. That read is the first of the three
   * moments the read is made at; the others are the cadence and the operator's
   * own refresh.
   *
   * The placeholder goes up for the console that has **nothing** to redraw: a
   * Graph a refusal took down, or one this console has never read. A Graph that
   * holds a picture keeps it -- the picture is the plan the operator was looking
   * at, and a placeholder in its place would take a drawing away to say that
   * another one is coming.
   */
  const showGraph = async (): Promise<void> => {
    const mine = claim()
    const held = graphViews.get(graphKey())
    if (held !== null) {
      // **The cached view is drawn before anything is awaited**, which is the
      // whole point of the cache: the plan this session already read, with the
      // run list it was decorated with, laid out again by the engine's own cache
      // without a message to the worker. It is stamped `as of HH:MM` with the
      // door to read it again, and a view older than `CACHE_FRESH_MS` is read
      // again at once **as well** -- never served silently.
      const drawn = await graphScreenNow().load(held.value.document, held.value.runs)
      if (stale(mine) || drawn === null) return
      graph.runs = held.value.runs
      redraw(drawn, stopNow())
      stampGraph(held.at)
      if (Date.now() - held.at >= CACHE_FRESH_MS) void readGraph(mine, graphRead.start())
      return
    }
    const last = graph.screen?.redraw() ?? null
    if (last !== null) redraw(last, stopNow())
    else placeReading('graph', mine)
    // The placeholder is what says a read is running when this console has
    // nothing to redraw, so the read carries no token: a mark over the sentence
    // that already names the tab would be a second answer to the same click.
    await readGraph(mine, null)
  }

  /** graphTree is the graph drawing the panel holds, so a redraw no gesture
   * caused can ask where the keyboard is **before** it takes that tree down. It
   * is `null` while the panel holds something else -- a refusal, or another
   * screen -- and a stale tree is harmless either way: the walk below matches the
   * keyboard against the tree's own elements, and a detached one holds none. */
  let graphTree: Drawn | null = null

  /**
   * FocusTarget is where a redraw puts the keyboard: the classes the element may
   * be drawn as, and -- when a class alone does not name it -- the attribute and
   * value that do.
   *
   * A node is a card on the canvas or a row in the list -- two drawings of one
   * door, and the drawing the operator is looking at is the one that holds it --
   * and a tab is named by its `aria-selected`, because the tab the screen drew as
   * selected is the tab that is showing even when the one asked for was not in
   * this document. The three controls that carry no identity attribute (Fit,
   * Refresh, the view switch) are named by their class alone, and the screen
   * draws one of each.
   */
  interface FocusTarget {
    readonly classes: readonly string[]
    readonly attribute?: string
    readonly value?: string
  }

  /** STOP_ATTRIBUTES is the attributes that name one drawn thing apart from its
   * siblings, in the order a target reads them: a node's and a row's key, a
   * chip's release, a tab's stream. */
  const STOP_ATTRIBUTES = ['data-key', 'data-chip', 'data-tab'] as const

  /** nodeStop is the card or row a task's key names. */
  const nodeStop = (key: string): FocusTarget => ({
    classes: ['graph-node', 'graph-row'],
    attribute: 'data-key',
    value: key,
  })

  /** SELECTED_TAB is the stream filter's own single stop: the segment of the
   * stream the graph is showing. It is `aria-pressed` and not `aria-selected`
   * from step 6b, which is where the tabs became a segmented control. */
  const SELECTED_TAB: FocusTarget = { classes: ['graph-tab'], attribute: 'aria-pressed', value: 'true' }

  /** INSPECTOR_PANEL is the graph's inspector itself -- the `aside` beside the
   * canvas. It takes the keyboard when a press selects a task this drawing does
   * not draw (a task of a shut finished release has no card on the canvas), and
   * it is `tabindex="-1"` for exactly that. */
  const INSPECTOR_PANEL: FocusTarget = { classes: ['graph-inspector'] }

  /** DRAWING_STOP is the drawing's own roving stop: the card, chip or segment
   * the screen put in the tab order. It is where the keyboard goes when the
   * element a gesture named -- a card of a task the last read finished, say -- is
   * not in the tree the gesture drew, because a reader left on `<body>` is a
   * reader who cannot walk on (design section 4.3). */
  const DRAWING_STOP: FocusTarget = {
    classes: ['graph-node', 'graph-row', 'graph-chip', 'graph-tab'],
    attribute: 'tabindex',
    value: '0',
  }

  /** chipStop is the chip of the release whose key it was drawn for: the button
   * the operator pressed, and the one the redraw draws back. */
  const chipStop = (key: string): FocusTarget => ({ classes: ['graph-chip'], attribute: 'data-chip', value: key })

  /** FIT_BUTTON and VIEW_BUTTON are the two controls a class names on its own:
   * neither carries an attribute that tells it from a sibling, and the screen
   * draws one of each. */
  const FIT_BUTTON: FocusTarget = { classes: ['graph-fit'] }
  const VIEW_BUTTON: FocusTarget = { classes: ['graph-view'] }

  /** stopOf is where the keyboard is, read off a tree the panel holds: the target
   * naming `document.activeElement` when that element is one of the tree's own,
   * and `null` when the keyboard is anywhere else -- another screen, the rail, or
   * nowhere at all. */
  const stopOf = (drawn: Drawn): FocusTarget | null => {
    const active = document.activeElement
    if (active === null) return null
    for (const element of elementsUnder(drawn)) {
      if (surface.nodeOf(element) !== active) continue
      const classes = (element.getAttribute('class') ?? '').split(/\s+/).filter((each) => each !== '')
      for (const attribute of STOP_ATTRIBUTES) {
        const value = element.getAttribute(attribute)
        if (value !== null) return { classes, attribute, value }
      }
      return { classes }
    }
    return null
  }

  /** stopNow is where the keyboard is in the graph as it stands: the target a
   * redraw that no gesture caused leaves it on. It is `null` when the keyboard is
   * not on one of the drawing's own elements, and a redraw with `null` focuses
   * nothing -- which is how a frame that lands while the reader is in the rail
   * leaves them there. */
  const stopNow = (): FocusTarget | null => (graphTree === null ? null : stopOf(graphTree))

  /**
   * redraw puts a freshly drawn graph on the panel, wires it, and moves the
   * keyboard into the **new** tree -- or leaves it where it was, when the target
   * it is handed is `null`.
   *
   * **Every call site names a target, and that is the rule.** A gesture names the
   * element it was made on -- the chip that was pressed, the node a click
   * selected, the tab an arrow key chose -- because that element is the one the
   * reader is working with. A redraw that no gesture caused (a live frame, the
   * poll's own read, a wheel) names `stopNow()`, the element the keyboard is
   * already on.
   *
   * **The focus is asked for here and never on the tree being taken down.** Every
   * one of these redraws replaces the panel's tree, so an element found in the
   * old one is a node a real window has already detached: focusing it leaves the
   * keyboard on `<body>`, and the reader who pressed an arrow key is no longer
   * anywhere (design section 4.3).
   */
  const redraw = (element: SkewElement | null, focus: FocusTarget | null, instead: FocusTarget | null = null): void => {
    if (element === null) return
    replace(element)
    const next = element as Drawn
    graphTree = next
    wireGraph(next)
    if (focus === null) return
    if (!focusMatching(next, focus) && instead !== null) focusMatching(next, instead)
  }

  /** focusMatching moves the keyboard to the element of a freshly drawn tree
   * that a gesture chose: the first element of one of `classes` whose `attribute`
   * carries `value` -- or, for a target that names no attribute, the first
   * element of one of `classes`. The roving `tabindex` is what makes the stop
   * reachable and the focus is what makes the ring follow it: a selection that
   * moved the attribute and left the keyboard where it was is a picture a
   * keyboard reader cannot find their way around (design section 4.3).
   *
   * It answers whether it found one, so a caller with a second target for a tree
   * that does not draw the first can take it (`redraw`'s `instead`). */
  const focusMatching = (drawn: Drawn, target: FocusTarget): boolean => {
    for (const name of target.classes) {
      const found = classed(drawn, name).find(
        (element) => target.attribute === undefined || element.getAttribute(target.attribute) === target.value,
      )
      if (found === undefined) continue
      const focusable = found as Drawn & { focus?(): void }
      focusable.focus?.()
      return true
    }
    return false
  }

  /**
   * frameGraph draws one live frame into the graph. The stages move and the
   * structure does not, so the screen folds them into the document it already
   * has and the layout cache answers the same graph -- which is the whole reason
   * the graph can be polled at all (GV2.3a's `structureHash` excludes state).
   *
   * A frame the console cannot read is **ignored**: the graph is a picture of the
   * plan, and a `live.watch` read that failed says nothing about the plan. The
   * plan's own read is where a refusal belongs, and it has one.
   *
   * **The rail's state word is the read's, and it is written here too.** The
   * frame is the same `live.watch` answer `drawLive` reads (`pollLive` routes one
   * read to the view on screen), so while the graph is up this is the only place
   * the word can be kept: a feed that has gone quiet, or a frame the console
   * cannot read, leaves the rail on `silent` here exactly as it does on Live
   * (design section 4.1). It is written **before** the drawing is asked for and
   * whether or not one is drawn -- the word is about the console's read of this
   * installation, not about whether the picture moved.
   */
  const frameGraph = async (reply: unknown): Promise<void> => {
    if (graph.screen === null) return
    const answer = replyData(reply)
    if (!answer.ok) {
      rail.setState('silent')
      return
    }
    const envelope = answer.data as Record<string, unknown>
    const decoded = decodeSnapshot(envelope['document'], 'live.watch')
    if (!decoded.ok) {
      rail.setState('silent')
      return
    }
    rail.setState(feedHealthOf(envelope['receivedAt'], nowStamp()).stale ? 'silent' : 'running')
    const drawn = await graph.screen.frame(decoded.value.runs)
    // The frame that changed nothing answers nothing, and a frame that landed
    // after the operator moved on draws nothing.
    if (drawn === null || currentView !== 'graph') return
    // A frame is not a gesture: the keyboard stays on the element it was on, and
    // a reader who is elsewhere is left there.
    redraw(drawn, stopNow())
  }

  /**
   * wireGraph makes the drawn graph a screen a person can drive: the tabs narrow
   * the stream, a chip opens a finished release, a node selects (and, once
   * selected, opens its run), the two controls zoom and switch the drawing, and
   * the arrow keys walk the picture.
   *
   * **A node opens a run only through the entry's own runs list.** The screen
   * resolves the node's four-character suffix against the console's current runs
   * and writes the run it found into `data-run-id`; a node whose suffix no run
   * ends with, or one two runs end with, carries none -- and this function opens
   * nothing for it, because there is no run to open. The sentence saying why is
   * on the element's own tooltip, drawn by the screen.
   *
   * A **pull request** is a number and no destination (design section 5.1 rule 2
   * and its gap 7: no document names a provider), so nothing here reads one. The
   * console's one door to the outside is `link.openExternal`, and this screen
   * offers it nothing to open.
   */
  const wireGraph = (drawn: Drawn): void => {
    const screen = graph.screen
    if (screen === null) return

    /**
     * chooseTab is the stream filter's one door, from the mouse and from the
     * keyboard alike.
     *
     * **It answers in the frame it was pressed in.** A tab is a different graph
     * and not a different drawing of one -- the screen asks the engine to lay it
     * out again -- so the press takes the section's token and puts the mark up
     * over the picture on screen before anything is awaited; the drawing that
     * answers replaces it, and the mark comes down with the token. Two quick
     * presses leave the second one's graph: an answer whose token is no longer
     * the newest is dropped, whatever order the two arrive in.
     */
    const chooseTab = (tab: GraphTab): void => {
      const token = graphRead.start()
      markGraph(token)
      void screen.narrow(tab).then((element) => {
        if (!graphRead.settle(token)) return
        redraw(element, SELECTED_TAB)
      })
    }

    const tabButtons = classed(drawn, 'graph-tab')
    for (const button of tabButtons) {
      const tab = tabOf(button.getAttribute('data-tab') ?? tabKey(null))
      const node = surface.nodeOf(button)
      if (tab !== screen.current.tab) {
        node.addEventListener('click', () => {
          chooseTab(tab)
        })
      }
      // The tablist's keys (design section 4.3, the navigation's own rule): Left
      // and Right, Home and End, and each of them is the tab it moves to -- a tab's
      // selection *is* the graph it draws, so the arrows activate as they move and
      // no second key is needed. The selected tab is in the loop like any other,
      // because it is the one the arrows move **from**.
      keydownOn(node, (event: KeyEventLike) => {
        const target = tabTarget(event.key, tabButtons.indexOf(button), tabButtons.length, 'x')
        const wanted = target === null ? undefined : tabButtons[target]
        if (wanted === undefined) return
        event.preventDefault()
        // The tab the screen draws is the one to focus, not the tab that was
        // asked for: a stream this document no longer carries falls back to All,
        // and the ring belongs on the tab that is showing.
        chooseTab(tabOf(wanted.getAttribute('data-tab') ?? tabKey(null)))
      })
    }

    for (const button of classed(drawn, 'graph-chip')) {
      const key = button.getAttribute('data-chip') ?? ''
      surface.nodeOf(button).addEventListener('click', () => {
        const open = screen.current.expanded
        redraw(
          screen.change({ expanded: open.includes(key) ? open.filter((each) => each !== key) : [...open, key] }),
          chipStop(key),
        )
      })
    }

    /**
     * chooseTask selects one task, from the picture and from the inspector alike.
     *
     * **The card it names may not be in the tree this draws.** A task of a shut
     * finished release is off the canvas (`visibleNodes`), so a press on the
     * panel's own list of what a task waits on can name a task with no card: the
     * panel -- which is still drawn, and is now about the task just chosen -- is
     * where the keyboard goes instead of nowhere (design section 4.3).
     */
    const chooseTask = (key: string): void => {
      redraw(screen.change({ selected: key }), nodeStop(key), INSPECTOR_PANEL)
    }

    /** closeInspector is Escape's own gesture: the panel is closed and the
     * keyboard goes back to the card it was opened from. */
    const closeInspector = (): void => {
      const selected = screen.current.selected
      if (selected === null) return
      redraw(screen.change({ selected: null }), nodeStop(selected), DRAWING_STOP)
    }

    // A node and a list row are the same door: the card's `g` and the row's `li`
    // both carry `data-key` and both carry `data-run-id` when there is a run.
    const doors = [...classed(drawn, 'graph-node'), ...classed(drawn, 'graph-row')]
    for (const door of doors) {
      const key = door.getAttribute('data-key') ?? ''
      const run = door.getAttribute('data-run-id')
      const node = surface.nodeOf(door)
      node.addEventListener('click', () => {
        // The first click selects -- which is what highlights the edges that
        // touch the task, and what opens the inspector beside the canvas -- and a
        // second click on the task already selected is the gesture that opens it.
        // A card with no run is selected like any other and opens nothing, and
        // its tooltip says why.
        if (screen.current.selected === key) {
          if (run !== null) void showRun(run)
          return
        }
        chooseTask(key)
      })
      keydownOn(node, (event: KeyEventLike) => {
        if (event.key === 'Escape') {
          event.preventDefault()
          closeInspector()
          return
        }
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault()
          // Enter opens the card's run where it has one and **selects** it where
          // it has none: the keyboard's own way into the inspector, which a card
          // with no run would otherwise not have at all.
          if (run !== null) void showRun(run)
          else chooseTask(key)
          return
        }
        const target = tabTarget(event.key, doors.indexOf(door), doors.length, 'y')
        const wanted = target === null ? undefined : doors[target]
        if (wanted === undefined) return
        event.preventDefault()
        const moved = wanted.getAttribute('data-key') ?? ''
        redraw(screen.change({ selected: moved }), nodeStop(moved))
      })
    }

    // The inspector is read-only apart from its two doors: the tasks the selected
    // one waits on and the tasks that wait on it are buttons that select that
    // card, and Open run is the picture's own door spelled out. Escape closes the
    // panel wherever the keyboard is inside it -- the panel is focusable for the
    // press that names a card the canvas does not draw, and a key inside it
    // reaches this listener in a browser the same way it reaches a card's.
    const panel = classed(drawn, 'graph-inspector')[0]
    if (panel !== undefined) {
      keydownOn(surface.nodeOf(panel), (event: KeyEventLike) => {
        if (event.key !== 'Escape') return
        event.preventDefault()
        closeInspector()
      })
    }
    for (const button of [...classed(drawn, 'graph-inspector-need'), ...classed(drawn, 'graph-inspector-unblock')]) {
      const key = button.getAttribute('data-key') ?? ''
      const node = surface.nodeOf(button)
      node.addEventListener('click', () => {
        chooseTask(key)
      })
      keydownOn(node, (event: KeyEventLike) => {
        if (event.key !== 'Enter' && event.key !== ' ') return
        event.preventDefault()
        chooseTask(key)
      })
    }
    const open = classed(drawn, 'graph-inspector-open')[0]
    if (open !== undefined) {
      const run = open.getAttribute('data-run-id')
      if (run !== null) surface.nodeOf(open).addEventListener('click', () => void showRun(run))
    }

    const refresh = classed(drawn, 'graph-refresh')[0]
    if (refresh !== undefined) {
      surface.nodeOf(refresh).addEventListener('click', () => {
        // The token is taken **in the click**, before the read is started: it is
        // what puts the mark up in this frame, and what keeps two quick presses
        // from leaving the earlier one's answer on screen.
        void readGraph(claim(), graphRead.start())
      })
    }

    const fit = classed(drawn, 'graph-fit')[0]
    if (fit !== undefined) {
      surface.nodeOf(fit).addEventListener('click', () => {
        redraw(screen.change({ zoom: 1 }), FIT_BUTTON)
      })
    }

    const view = classed(drawn, 'graph-view')[0]
    if (view !== undefined) {
      surface.nodeOf(view).addEventListener('click', () => {
        redraw(screen.change({ view: screen.current.view === 'graph' ? 'list' : 'graph' }), VIEW_BUTTON)
      })
    }

    // Zoom and pinch are one gesture in a browser: a wheel event, with
    // `ctrlKey` set when what produced it was a pinch. The two differ only in
    // the step, which is why they are one listener.
    const canvas = classed(drawn, 'graph-canvas')[0]
    if (canvas !== undefined) {
      wheelOn(surface.nodeOf(canvas), (event: WheelEventLike) => {
        if (event.deltaY === 0) return
        event.preventDefault()
        const step = event.ctrlKey ? GRAPH_PINCH_STEP : GRAPH_ZOOM_STEP
        const zoom = screen.current.zoom * (event.deltaY < 0 ? step : 1 / step)
        // A wheel is a pointer gesture and names no element of its own: the
        // keyboard goes back to whatever it was on.
        redraw(screen.change({ zoom }), stopNow())
      })
    }
  }

  // ---- Section fillers --------------------------------------------------

  /** clearSection empties one section down to its own slot -- the mark of the
   * read that has just landed, and the view it replaces. The slot stays, because
   * it is where the next read's mark goes, and it is put back as the section's
   * only child so the fillers below can append the content beside it.
   *
   * It is shared by every screen whose sections fill as their own reads land
   * (Spend's two and Projects'), because the rule it keeps is the same one: a
   * section is emptied to its slot and never to nothing. */
  const clearSection = (host: Drawn, slot: Drawn): void => {
    const node = surface.nodeOf(slot)
    node.replaceChildren()
    surface.nodeOf(host).replaceChildren(node)
  }

  /** appendTo puts one of the entry's own drawings into a section a screen made.
   * The screens' `append` takes a child of their own declared type and a refusal
   * screen or a cache stamp is not one of those: they are this file's, drawn
   * into a host the screen drew. */
  const appendTo = (host: Drawn, child: SkewElement): void => {
    host.append(child)
  }

  // ---- Spend ------------------------------------------------------------

  /** spendBudget and spendCapsSentence are what `budget show` last answered for
   * this installation: the document the caps section reads its figures out of,
   * or the CLI's own words when it refused. They are held for the installation
   * and dropped by `forgetLive` -- the caps are a fact about the installation
   * and not about the window the operator chose, so a control change re-reads
   * the report and never the budget (`readSpend`'s own division, kept here) --
   * and they are what a visit draws while its own caps read is in flight. */
  let spendBudget: Readonly<Record<string, unknown>> | null = null
  let spendCapsSentence: string | null = null

  /** spendViews is what this session already read, one entry per view the three
   * controls can name: the installation, the window, the breakdown and the
   * person switch. It is memory and nothing else, and `forgetLive` empties it,
   * so a view read for one installation is never drawn under another. */
  const spendViews = resultCache<SpendModel>(CACHE_LIMIT)

  /** spendCapsRead and spendReportRead are the two sections' read lifecycles:
   * each has its own busy mark, its own sequence token and its own refusal, so a
   * slow report cannot take the caps' mark down and a control change cannot make
   * the caps section look busy. Both are read on every visit and neither waits
   * for the other. */
  const spendCapsRead = sectionRead({ factory, theme })
  const spendReportRead = sectionRead({ factory, theme })

  /** leaveSpend ends both sections' busy state when the operator moves to
   * another tab: nothing is in flight for a screen nobody is looking at. */
  leaveSpend = () => {
    spendCapsRead.end()
    spendReportRead.end()
  }

  /**
   * showSpend draws the Spend screen as **two sections that fill as their own
   * reads land** (the owner's rule of 2026-10-31): the caps section from
   * `budget show` and the report section from `report --json`. The two reads
   * start together and neither waits for the other -- the caps answer in about a
   * second and the report in about twenty-three on the owner's installation, so
   * a screen that waited for both would hold a spinner for the whole of the
   * second one.
   *
   * **A control change is answered in the frame it happened in.** The click puts
   * a busy mark and `aria-busy` on the report section, keeps the figures already
   * drawn under it (dimmed, never blanked) and starts the read; the mark names
   * the window and the breakdown being read. A read that lands with an older
   * token than the latest change is dropped, so two quick changes leave the
   * second one's view on screen whatever order the answers arrive in.
   *
   * **What the session already read is drawn instantly.** The cache is keyed by
   * the installation and the three controls, lives in memory for this session
   * only, and a view older than `CACHE_FRESH_MS` is stamped and re-read at once
   * rather than served silently. Entering the tab **and** a control change both
   * answer from it, so the round trip an operator makes between two windows
   * costs no read; the stamp's own Refresh is the one control that always reads
   * again.
   *
   * **The caps are read on every visit.** They are the installation's own budget
   * rather than the view the operator chose, so a control change never re-reads
   * them -- but they move while the console is open and a refusal must not stick
   * for the session, so entering the tab starts their read again, in parallel
   * with the report's, and draws what the session holds under its mark.
   *
   * The by-person panel is the screen's second read (`statsRuns`, console L5b)
   * and `readSpend` makes it only while the operator's knob is on, so the wiring
   * here hands over the reader and decides nothing about when it runs.
   */
  const showSpend = async (): Promise<void> => {
    const mine = claim()
    const project = railNow.find((row) => row.open)?.project ?? ''
    let controls: SpendControls = SPEND_DEFAULT_CONTROLS
    /** frame is the shell on screen, or `null` while the panel holds the tab's
     * placeholder: the shell goes up the moment the first of the two reads
     * lands, because a Spend screen with no figure under a tab that is still
     * being read is worse than the placeholder that names the tab. */
    let frame: DrawnSpendFrame | null = null
    let reportPending = true
    let capsPending = spendBudget === null && spendCapsSentence === null
    /** reportToken and capsToken are the sequence tokens of the reads that are
     * out. They are held here and not only inside the readers because `mount`
     * marks a section that is still pending, and the mark has to belong to the
     * read that is really in flight. */
    let reportToken = 0
    let capsToken = 0

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

    // The by-person read is the same shape over `statsRuns`, and its refusal is
    // the panel's alone: `readSpend` turns a throw here into the panel's own
    // sentence and draws the rest of the screen from the report.
    const peopleReader: PeopleReader = async (args) => {
      const answer = documentOf(await bridge.statsRuns(args))
      if (!answer.ok) throw { sentence: answer.sentence }
      return answer.data
    }

    /**
     * mount draws the shell if it is not up yet, and answers it.
     *
     * It puts the busy mark on every section whose read is still out, which is
     * the only moment those marks can be drawn: the panel held the tab's
     * placeholder until now, and the sections did not exist.
     *
     * **The mark of a pending section carries that section's skeleton**, and it
     * is what keeps the placeholder's promise across the swap: the first read to
     * land replaces the panel with this shell, and the section still out shows
     * the shape it will take rather than a mark over nothing. That is also why
     * the section that just landed gets no mark here -- its own fill replaces
     * the slot's contents in this same turn, and it is `paintCaps` and
     * `paintReport` that decide that, not this.
     */
    const mount = (): DrawnSpendFrame => {
      if (frame !== null) return frame
      const drawn = renderSpendFrame(factory, theme)
      frame = drawn
      replace(drawn.root)
      if (capsPending) {
        spendCapsRead.mark(capsToken, drawn.caps, drawn.capsSlot, SPEND_CAPS_BUSY, SPEND_CAPS_SHAPE)
      }
      if (reportPending) {
        spendReportRead.mark(
          reportToken,
          drawn.report,
          drawn.reportSlot,
          spendBusyLabel(controls),
          SPEND_REPORT_SHAPE,
        )
      }
      return drawn
    }

    /** paintCaps draws the caps section from what `budget show` answered. It is
     * its own read, its own refusal (`budget.show`) and its own token: a report
     * that refuses is not a reason to take this section's answer down, and the
     * other way round.
     *
     * `still` is the case of a visit that already holds caps and has just asked
     * for them again: the figures this session has are drawn **and then** the
     * mark goes up over them, because a mark drawn before the drawing is taken
     * out of the section with the slot it was in. */
    const paintCaps = (still = false): void => {
      capsPending = false
      const drawn = mount()
      clearSection(drawn.caps, drawn.capsSlot)
      if (spendCapsSentence !== null) {
        appendTo(drawn.capsSlot, refusedScreen(factory, 'budget.show', spendCapsSentence, theme))
      } else {
        fillSpendCaps(factory, capsOf(spendBudget), drawn.caps)
      }
      if (still) spendCapsRead.mark(capsToken, drawn.caps, drawn.capsSlot, SPEND_CAPS_BUSY)
    }

    /**
     * paintReport fills the report section from what the report answered, and
     * wires the controls it drew.
     *
     * `cached` is whether this view came out of the session cache rather than
     * out of a read made now: a cached view is drawn with the console clock it
     * was read at and a control to read it again, so a figure the operator is
     * looking at is never silently old.
     */
    const paintReport = (
      model: SpendModel,
      readWith: SpendControls,
      at: number,
      cached: boolean,
    ): void => {
      reportPending = false
      const drawn = mount()
      clearSection(drawn.report, drawn.reportSlot)
      fillSpendReport(factory, model, theme, readWith, drawn.report)
      if (cached) {
        const stamp = renderStamp(factory, at)
        appendTo(drawn.reportSlot, stamp.root)
        surface.nodeOf(stamp.refresh).addEventListener('click', () => {
          // `refreshFrom` and not `change`: the visit's own `controls` is what
          // the read is started from, whatever drew the control the operator
          // pressed -- and a Refresh reads the view again even though the cache
          // holds it, which is the whole of what the control is for.
          refreshFrom(readWith)
        })
      }
      wireSpendControls(drawn.root, readWith, change)
    }

    /**
     * refresh starts the report read for one set of controls.
     *
     * The token is taken **before the await**, so a slow earlier read cannot
     * overwrite a later one, and the mark goes up in the frame the change
     * happened in. When the shell is not up yet -- the first read of a visit --
     * there is nothing to mark, and `mount` puts the mark up when it arrives.
     */
    const refresh = (readWith: SpendControls): void => {
      reportToken = spendReportRead.start()
      const token = reportToken
      if (frame !== null) {
        spendReportRead.mark(token, frame.report, frame.reportSlot, spendBusyLabel(readWith))
      }
      void loadReport(readWith, token)
    }

    /** loadReport makes one report read and draws it when it is still the
     * latest. */
    const loadReport = async (readWith: SpendControls, token: number): Promise<void> => {
      // The caps are handed over for the model's own `caps` field, which the
      // report section does not draw: the caps are their own section, and this
      // read starts before that one may have answered.
      const outcome = await readSpend(reader, readWith, spendBudget, peopleReader)
      // The token is checked **before** anything is taken down. The busy state
      // belongs to the helper rather than to this read, so a read from an earlier
      // visit that landed after the operator came back would end the *current*
      // visit's mark -- taking its `aria-busy` off and cancelling its
      // fifteen-second line -- for a read nobody is waiting for any more.
      if (!spendReportRead.settle(token)) return
      if (stale(mine)) return
      if (!outcome.ok) {
        // The report's refusal is **this section's own**, exactly as the caps'
        // is: the caps read may still be out, and a refusal drawn over the whole
        // panel would be replaced the moment it landed -- leaving the report
        // section marked as reading with no read in flight. The frame stays for
        // the same reason.
        reportPending = false
        const drawn = mount()
        clearSection(drawn.report, drawn.reportSlot)
        appendTo(drawn.reportSlot, refusedScreen(factory, 'spend.report', outcome.reason, theme))
        return
      }
      const at = Date.now()
      spendViews.put(spendViewKey(project, readWith), outcome.model, at)
      paintReport(outcome.model, readWith, at, false)
    }

    /** loadCaps makes one caps read and paints the section when it lands. It is
     * started together with the report's read and never waits for it, and it is
     * told which token it is: the section's read is made on every visit, so two
     * of them can be out at once and the one that answers is not necessarily the
     * one this call started. */
    const loadCaps = async (token: number): Promise<void> => {
      const reply = documentOf(await bridge.showBudget({}))
      // The rail's spend is the console's own budget read -- this one, the read
      // the caps section makes -- and the rail makes none of its own (design
      // section 4.1). It is filled before the staleness check below: the spend
      // is a fact about the installation rather than about the view the
      // operator chose, so a read that landed after they moved on still fills
      // the rail.
      if (reply.ok) {
        rail.setSpend(railSpend(reply.data))
        setGauge(gaugeFromBudget(reply.data))
        spendBudget = reply.data
        // The sentence and the document are one answer between them, and
        // `paintCaps` reads the sentence first: a refusal left standing here is
        // the section refusing for the rest of the session, whatever the reads
        // that follow say (only `forgetLive` used to clear it).
        spendCapsSentence = null
      } else {
        spendCapsSentence = reply.sentence
        spendBudget = null
      }
      // The token goes first, for the report's own reason: the mark this section
      // wears may belong to a later read, and `end` would take it down with it.
      if (!spendCapsRead.settle(token)) return
      if (stale(mine)) return
      paintCaps()
    }

    /**
     * startCaps starts one caps read: its token, the mark when the section is
     * already drawn, and the read itself. `mount` puts the mark up when the
     * shell is not up yet, and the read is out either way -- a section with a
     * read in flight must not look as though it has answered.
     */
    const startCaps = (): void => {
      capsToken = spendCapsRead.start()
      if (frame === null) capsPending = true
      else spendCapsRead.mark(capsToken, frame.caps, frame.capsSlot, SPEND_CAPS_BUSY)
      void loadCaps(capsToken)
    }

    /**
     * change answers one control change: the mark goes up in the same frame as
     * the click, the figures already drawn stay under it, and the read for the
     * new controls starts at once -- unless this session has already read the
     * view the change names, which the three controls make a small and often
     * repeated set: 30d → 7d → 30d is a round trip an operator makes without
     * meaning to ask the CLI anything. A hit is drawn stamped in the click's own
     * frame, and one older than `CACHE_FRESH_MS` is read again at once rather
     * than served silently.
     */
    const change = (next: SpendControls): void => {
      controls = next
      const hit = spendViews.get(spendViewKey(project, next))
      if (hit === null) {
        refresh(next)
        return
      }
      // A token is taken even though no read is made. The cached view is the
      // newest answer for the controls on screen, and a read still in flight for
      // a range the operator has just left -- a whole report, so a read that can
      // take twenty seconds -- must not be drawn over it when it lands. `end`
      // then takes that read's mark off the section it would keep dimmed.
      reportToken = spendReportRead.start()
      spendReportRead.end()
      paintReport(hit.value, next, hit.at, true)
      if (Date.now() - hit.at >= CACHE_FRESH_MS) refresh(next)
    }

    /**
     * refreshFrom is the stamp's own control, and the one door out of the cache
     * for the view on screen: a Refresh is the operator saying the figures they
     * are looking at are old, so the view is read again whatever this session
     * holds. The caps are read again with it, because they are as old as the view
     * the stamp is about.
     */
    const refreshFrom = (readWith: SpendControls): void => {
      controls = readWith
      refresh(readWith)
      startCaps()
    }

    // The view the screen opens on, if this session has already read it. A hit
    // is drawn before anything is awaited -- which is the whole point of the
    // cache -- and a view older than `CACHE_FRESH_MS` is stamped and re-read at
    // once rather than served silently.
    const cached = spendViews.get(spendViewKey(project, controls))
    const heldCaps = spendBudget !== null || spendCapsSentence !== null
    capsPending = !heldCaps
    if (cached === null) placeReading('spend', mine)
    else paintReport(cached.value, controls, cached.at, true)

    // The caps are read on **every** visit, not once for the installation: they
    // are the installation's budget, which moves while the console is open, and
    // a `budget show` the CLI refused would otherwise stay refused for the rest
    // of the session. What this session already holds is drawn first, under the
    // mark of the read that is starting -- never a blank section for the second
    // the CLI takes. Both reads start here, together: the caps section is not a
    // consequence of the report, and waiting for the report before asking for
    // the caps is the serialisation this fix exists to remove.
    if (heldCaps) paintCaps(true)
    startCaps()
    if (cached === null || Date.now() - cached.at >= CACHE_FRESH_MS) refresh(controls)
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

  /** projectsViews is what this session already read for the Projects screen:
   * one entry per installation, keyed by the open installation's project. It is
   * memory and nothing else, and `forgetLive` empties it with the rest of the
   * installation's state, so a view read for one installation is never drawn
   * under another. */
  const projectsViews = resultCache<ProjectsModel>(CACHE_LIMIT)

  /** projectsRead is the screen's own read lifecycle. The read is one
   * (`budget show`), so the section is the screen and there is one of each: one
   * busy mark, one sequence token, one refusal. */
  const projectsRead = sectionRead({ factory, theme })

  /** leaveProjects ends that section's busy state when the operator moves to
   * another tab: nothing is in flight for a screen nobody is looking at. */
  leaveProjects = () => {
    projectsRead.end()
  }

  /**
   * showProjects draws the Projects screen from its **one** read.
   *
   * **The jobs list is gone** (owner's report of 2026-10-31: it "is slow and
   * serves no purpose there"). `runs ls --json` is about 248 KB and six seconds
   * for a day of runs on the owner's installation, and this screen started it
   * before the caps -- the panel's whole content -- could be drawn: the tab took
   * about twenty seconds with nothing on screen. The read is `budget show` now,
   * about a second, and the panels derived from the run list went with it
   * (`buildProjectsModel`'s own note).
   *
   * **The shell is mounted the moment the tab is opened**, and the section's
   * busy mark and skeleton go up in that same frame: the header, the caps and the
   * rest of the panels are all that read's, so a screen that waited for it would
   * be the placeholder under another name. The mark names the read, the previous
   * visit's figures stay under it (dimmed, never blanked), and after fifteen
   * seconds it says so.
   *
   * **What this session already read is drawn instantly**, stamped `as of
   * HH:MM` with a Refresh control, and a view older than `CACHE_FRESH_MS` is
   * read again at once rather than served silently.
   */
  const showProjects = async (): Promise<void> => {
    const mine = claim()
    const project = railNow.find((row) => row.open)?.project ?? ''
    const key = cacheKey([project, 'projects'])
    const held = projectsViews.get(key)
    let frame: DrawnProjectsFrame | null = null
    let token = 0

    /** mount draws the shell if it is not up yet, and answers it. The mark of a
     * read that is still out goes up here, which is the only moment it can be
     * drawn: the panel held another view's content until this call. */
    const mount = (busy: boolean): DrawnProjectsFrame => {
      if (frame !== null) return frame
      const drawn = renderProjectsFrame(factory, theme)
      frame = drawn
      replace(drawn.root)
      if (busy) projectsRead.mark(token, drawn.body, drawn.slot, PROJECTS_BUSY, PROJECTS_SHAPE)
      return drawn
    }

    /** paint fills the section from what `budget show` answered. `cached` is
     * whether this view came out of the session cache rather than out of a read
     * made now: a cached view carries the console clock it was read at and the
     * one control that reads it again. */
    const paint = (model: ProjectsModel, at: number, cached: boolean): void => {
      const drawn = mount(false)
      clearSection(drawn.body, drawn.slot)
      fillProjects(factory, model, drawn.body)
      if (cached) {
        const stamp = renderStamp(factory, at)
        appendTo(drawn.body, stamp.root)
        surface.nodeOf(stamp.refresh).addEventListener('click', () => {
          start()
        })
      }
    }

    /** load makes the one read and draws it when it is still the latest. */
    const load = async (readToken: number): Promise<void> => {
      const answer = documentOf(await bridge.showBudget({}))
      // The rail's spend and the fleet gauge are this read's too (design section
      // 4.1): they are filled before the staleness check, because the spend is a
      // fact about the installation rather than about the panel the operator is
      // on, and a read that landed after they moved on still fills them.
      if (answer.ok) {
        rail.setSpend(railSpend(answer.data))
        setGauge(gaugeFromBudget(answer.data))
      }
      // The token is checked before anything is taken down, so a read from an
      // earlier visit cannot end the *current* visit's mark.
      if (!projectsRead.settle(readToken)) return
      if (stale(mine)) return
      if (!answer.ok) {
        const drawn = mount(false)
        clearSection(drawn.body, drawn.slot)
        appendTo(drawn.body, refusedScreen(factory, 'budget.show', answer.sentence, theme))
        return
      }
      const model = buildProjectsModel({
        budget: answer.data,
        accounts: null,
        secrets: null,
        sentences: {},
      })
      const at = Date.now()
      projectsViews.put(key, model, at)
      paint(model, at, false)
    }

    /** start takes the sequence token for one read, puts the mark up in the
     * frame's own section when the shell is already drawn, and makes the read. */
    const start = (): void => {
      token = projectsRead.start()
      if (frame === null) mount(true)
      else projectsRead.mark(token, frame.body, frame.slot, PROJECTS_BUSY)
      void load(token)
    }

    // The view this screen opens on, if this session has already read it, is
    // drawn before anything is awaited -- which is the whole point of the cache.
    // A view older than `CACHE_FRESH_MS` is stamped **and** read again at once
    // rather than served silently.
    if (held === null) start()
    else {
      paint(held.value, held.at, true)
      if (Date.now() - held.at >= CACHE_FRESH_MS) start()
    }
  }

  // ---- Coordinator ------------------------------------------------------

  /** coordinatorViews is what this session already read for the Coordinator
   * screen: the stored view of every repository of one installation. It is
   * memory and nothing else, and `forgetLive` empties it, so a view read for one
   * installation is never drawn under another. */
  const coordinatorViews = resultCache<CoordinatorModel>(CACHE_LIMIT)

  /** coordinatorRead is the screen's own read lifecycle: one busy mark, one
   * sequence token, one refusal -- the read is one document, the stored view of
   * every repository at once. */
  const coordinatorRead = sectionRead({ factory, theme })

  /** coordinatorChunk is the timer the detail is drawn on. It is held here
   * rather than inside a visit because leaving the tab has to stop it: the
   * chunks draw into a tree the operator has left. */
  let coordinatorChunk: ReturnType<typeof setTimeout> | null = null

  /** stopCoordinatorChunks cancels the detail still to be drawn. */
  const stopCoordinatorChunks = (): void => {
    if (coordinatorChunk === null) return
    clearTimeout(coordinatorChunk)
    coordinatorChunk = null
  }

  /** leaveCoordinator ends the section's busy state and stops its chunks when the
   * operator moves to another tab: nothing is in flight for a screen nobody is
   * looking at. */
  leaveCoordinator = () => {
    stopCoordinatorChunks()
    coordinatorRead.end()
  }

  /**
   * showCoordinator reads the project view: one document, the coordinator's
   * stored view for every repository of the installation
   * (`coordinator status --from-store --all --json`).
   *
   * **The read is one, and its refusal is the screen.** There is no second
   * document to fall back on and no figure this file could draw without it, so a
   * refused read is drawn as the CLI's own sentence rather than as an empty
   * project: a panel of zeroes would read as "this project has no work", which
   * is a different fact from "the console could not ask" (Global Constraint 5).
   *
   * **A document the decoder refuses is a refusal too.** `decodeCoordinatorStatus`
   * is the console's strict reader for this shape (unknown fields refused), so a
   * document this build cannot read is drawn with the skew's own sentence --
   * never as a view with nothing in it, which is the one reading a strict reader
   * exists to prevent.
   *
   * **The tab answers in the frame it was opened in** (the owner's rule of
   * 2026-10-31). This is the longest read the console makes -- about 724 KB and
   * thirteen seconds on the owner's installation -- so the frame goes up at once
   * and the count of what the read is made of is on it: the source line and the
   * console's sentence about the view's age are the console's own words and need
   * no document. Under the frame's mark the body holds the shape of what is
   * coming, and what the last visit drew stays dimmed rather than blanked.
   *
   * **The summary is painted the moment the document lands, and each
   * repository's detail after it, one repository per chunk.** A document that
   * lists hundreds of pull requests with their runs is the larger half of the
   * drawing, so making all of it in the frame the decode returned in would
   * freeze the window for as long as the drawing took; the chunks let the
   * operator scroll and read the summary while the rest arrives. The sequence
   * token is checked in every chunk as well as at the end of the read, so a
   * repository drawn late never lands under another installation or another
   * visit.
   *
   * **What this session already read is drawn instantly**, stamped `as of
   * HH:MM` with a Refresh control, and a view older than `CACHE_FRESH_MS` is
   * read again at once rather than served silently.
   */
  const showCoordinator = async (): Promise<void> => {
    const mine = claim()
    const project = railNow.find((row) => row.open)?.project ?? ''
    const key = cacheKey([project, 'coordinator'])
    const held = coordinatorViews.get(key)
    let frame: DrawnCoordinatorFrame | null = null
    let token = 0

    /** mount draws the shell if it is not up yet, and answers it. The mark of a
     * read that is still out goes up here, which is the only moment it can be
     * drawn: the panel held another view's content until this call. */
    const mount = (busy: boolean): DrawnCoordinatorFrame => {
      if (frame !== null) return frame
      const drawn = renderCoordinatorFrame(factory, theme)
      frame = drawn
      replace(drawn.root)
      if (busy) coordinatorRead.mark(token, drawn.body, drawn.slot, COORDINATOR_BUSY, COORDINATOR_SHAPE)
      return drawn
    }

    /**
     * paint draws the whole screen from a document: the summary in this frame,
     * and one repository's detail per chunk after it.
     *
     * `cached` is whether this view came out of the session cache rather than
     * out of a read made now: a cached view carries the console clock it was
     * read at and the one control that reads it again.
     */
    const paint = (model: CoordinatorModel, at: number, cached: boolean): void => {
      stopCoordinatorChunks()
      const drawn = mount(false)
      clearSection(drawn.body, drawn.slot)
      if (cached) {
        const stamp = renderStamp(factory, at)
        appendTo(drawn.body, stamp.root)
        surface.nodeOf(stamp.refresh).addEventListener('click', () => {
          start()
        })
      }
      const details = fillCoordinatorSummary(factory, model, drawn.body)
      let next = 0
      const step = (): void => {
        coordinatorChunk = null
        // The chunk is a drawing of a view this visit may no longer own: the
        // operator can have left the tab, or opened another installation, while
        // it was queued. Laid out per repository anyway, because `details` is
        // the order the summary drew them in and a detail must land under its
        // own summary.
        if (stale(mine)) return
        const detail = details[next]
        next += 1
        if (detail === undefined) return
        repoDetail(detail.host, factory, detail.panel)
        if (next < details.length) coordinatorChunk = setTimeout(step, 0)
      }
      if (details.length > 0) coordinatorChunk = setTimeout(step, 0)
    }

    /** load makes the one read and paints it when it is still the latest. */
    const load = async (readToken: number): Promise<void> => {
      const answer = documentOf(await bridge.coordinatorStatus({}))
      // The token is checked before anything is taken down, so a read from an
      // earlier visit cannot end the *current* visit's mark.
      if (!coordinatorRead.settle(readToken)) return
      if (stale(mine)) return
      if (!answer.ok) {
        stopCoordinatorChunks()
        const drawn = mount(false)
        clearSection(drawn.body, drawn.slot)
        appendTo(drawn.body, refusedScreen(factory, 'coordinator.status', answer.sentence, theme))
        return
      }
      const decoded = decodeCoordinatorStatus(answer.data, 'coordinator.status')
      if (!decoded.ok) {
        stopCoordinatorChunks()
        const drawn = mount(false)
        clearSection(drawn.body, drawn.slot)
        appendTo(drawn.body, refusedScreen(factory, 'coordinator.status', decoded.skew.because, theme))
        return
      }
      const model = buildCoordinatorModel({ view: decoded.value })
      const at = Date.now()
      coordinatorViews.put(key, model, at)
      paint(model, at, false)
    }

    /** start takes the sequence token for one read, puts the mark up in the
     * frame's own section when the shell is already drawn, and makes the read. */
    const start = (): void => {
      token = coordinatorRead.start()
      if (frame === null) mount(true)
      else coordinatorRead.mark(token, frame.body, frame.slot, COORDINATOR_BUSY)
      void load(token)
    }

    // The view this screen opens on, if this session has already read it, is
    // drawn before anything is awaited -- which is the whole point of the cache.
    // A view older than `CACHE_FRESH_MS` is stamped **and** read again at once
    // rather than served silently. The frame is drawn either way, so the tab is
    // never a blank panel while that read runs.
    if (held === null) start()
    else {
      paint(held.value, held.at, true)
      // The mark goes up **after** the drawing, and only for a view old enough to
      // be read again: `paint` empties the section down to its slot, so a mark
      // drawn before it would go out with the slot's own contents. A view still
      // inside `CACHE_FRESH_MS` is served as it is, with its stamp and its
      // Refresh, and no read at all.
      if (Date.now() - held.at >= CACHE_FRESH_MS) start()
    }
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

    const drawn = renderRun(factory, buildRunModel({ diagnosis, logs, logLink, sentences }), theme)
    replace(drawn)
    // The breadcrumb's `Live` crumb is the way back from a run the operator
    // opened, and it is the whole of what this screen wires: the same
    // `show('live')` the nav's own Live button calls, and the run id beside it
    // is selectable text rather than a control. Nothing else on Run binds
    // anything -- the retry is `writes.ts`'s refused button.
    const crumb = classed(drawn, 'run-crumb-live')[0]
    if (crumb !== undefined) {
      surface.nodeOf(crumb).addEventListener('click', () => {
        show('live')
      })
    }
  }

  // ---- The installation rail's issue view --------------------------------

  /**
   * The rail's rows as the last list answer drew them. A click is answered from
   * the row it was drawn with; this is what an action's own answer replaces.
   */
  let railNow: readonly RailRow[] = deps.rows
  let onboardNow: RailOnboarding | null = deps.onboarding

  // ---- The launch control (V2.5.1b) --------------------------------------

  let launchNow: LaunchNow | null = null

  /** sameOffer is whether the control that is held was built for the repositories
   * the open row offers now: the same installation, and the same identities in the
   * same order. */
  const sameOffer = (held: LaunchNow, open: RailRow): boolean =>
    held.project === open.project &&
    held.view.repositories.length === open.repositories.length &&
    held.view.repositories.every((one, index) => one === open.repositories[index])

  /** launchNowOf is the control for the installation the panel is about, built
   * on first use and rebuilt when its repository list changes. `supported` is the
   * handshake's answer about the CLI this console is driving (design section 5.1
   * rule 11: below the floor there is no control at all, and no call is reachable
   * from this file). */
  const launchNowOf = (): LaunchNow | null => {
    const open = railNow.find((row) => row.open)
    if (open === undefined) return null
    if (launchNow !== null && sameOffer(launchNow, open)) return launchNow
    const view: LaunchView = {
      installation: open.project,
      repositories: open.repositories,
      supported: launchSupported(gate.compatibility.cliVersion ?? ''),
    }
    const control = renderLaunch(factory, view)
    launchNow = { project: open.project, view, control, busy: false }
    wireLaunch(launchNow)
    return launchNow
  }

  /** countNow writes the task's own byte count, which is the number main counts. */
  const countNow = (fields: LaunchFields, text: string): void => {
    fields.count.textContent = launchCounter(launchBytes(text))
  }

  /** busyNow is the in-flight state, written in one place: the submit is
   * `disabled` and the form carries `aria-busy`, so a control that was pressed
   * cannot be pressed again while the confirmation window is open in front of
   * it. */
  const busyNow = (held: LaunchNow, busy: boolean): void => {
    const fields = held.control.fields
    if (fields === null || held.control.form === null) return
    ;(surface.nodeOf(fields.submit) as unknown as { disabled: boolean }).disabled = busy
    held.control.form.setAttribute('aria-busy', busy ? 'true' : 'false')
  }

  /**
   * sendLaunch asks the main process for the launch. It is the **only** call
   * this file makes with a launch's fields, and the object it passes carries
   * exactly the schema's five keys: the installation, the repository the select
   * holds -- the identity the installation records, which is the option's own
   * `value` and not the label drawn beside it -- the task, and the title and plan
   * task when the operator typed one. Nothing here is a path, a flag, a ref or a
   * run id -- main and its confirmation window own all four -- and `launchCheck`
   * is a convenience in front of it rather than the boundary: main re-validates
   * every field.
   *
   * The outcome is drawn in the region `renderLaunch` marked `aria-live`, and
   * the task is cleared only when a launch happened: a refusal keeps the fields,
   * because the operator's next move is to fix one of them.
   */
  const sendLaunch = async (held: LaunchNow): Promise<void> => {
    const fields = held.control.fields
    if (fields === null || held.busy) return
    const typed: LaunchTyped = {
      repository: valueOf(surface, fields.repository),
      task: valueOf(surface, fields.task),
      title: valueOf(surface, fields.title),
      planTask: valueOf(surface, fields.planTask),
    }
    const complaint = launchCheck(typed, held.view.repositories)
    fields.hint.textContent = complaint ?? ''
    if (complaint !== null) return
    const title = typed.title.trim()
    const planTask = typed.planTask.trim()
    held.busy = true
    busyNow(held, true)
    let reply: unknown = null
    try {
      reply = await launchBridge.launchRun({
        installation: held.project,
        repository: typed.repository,
        task: typed.task,
        ...(title === '' ? {} : { title }),
        ...(planTask === '' ? {} : { planTask }),
      })
    } finally {
      held.busy = false
      busyNow(held, false)
    }
    // A launch that settled after the operator moved to another installation is
    // an answer about a form that is gone: drawing it would be a sentence about
    // one installation's launch under another's.
    if (launchNow !== held) return
    const read = launchOutcomeOf(reply)
    surface.nodeOf(held.control.outcome).replaceChildren(surface.nodeOf(renderLaunchOutcome(factory, read)))
    if (read.kind === 'launched') {
      setValueOf(surface, fields.task, '')
      countNow(fields, '')
      fields.hint.textContent = ''
    }
  }

  /** wireLaunch binds the form's own events: the button that reveals it, the
   * counter that follows the task, and the submit. */
  const wireLaunch = (held: LaunchNow): void => {
    const { open, form, fields } = held.control
    if (open !== null && form !== null) {
      surface.nodeOf(open).addEventListener('click', () => {
        surface.nodeOf(form).removeAttribute('hidden')
        // The button says what it did: `aria-expanded` follows the form it
        // controls, so the state is announced and not only drawn.
        open.setAttribute('aria-expanded', 'true')
      })
    }
    if (fields === null) return
    surface.nodeOf(fields.task).addEventListener('input', () => {
      countNow(fields, valueOf(surface, fields.task))
    })
    surface.nodeOf(fields.submit).addEventListener('click', () => {
      void sendLaunch(held)
    })
  }

  /**
   * placeLaunch puts the control under the Live screen. It is called from the
   * two places the Live view is drawn into the panel and from nowhere else, so
   * "the launch control is on the Runs screen and on no other view" is the call
   * sites rather than a condition every other screen would have to remember.
   */
  const placeLaunch = (): void => {
    if (currentView !== 'live') return
    const held = launchNowOf()
    if (held === null) return
    surface.nodeOf(panel).appendChild(surface.nodeOf(held.control.root))
  }

  /** showOnboarding puts the first-run screen in the main area: no
   * installation, so nothing to read and no Live refusal in its place. */
  const showOnboarding = (onboarding: RailOnboarding): void => {
    markCurrent('onboarding')
    rail.view(null)
    replace(drawOnboarding(surface, onboarding))
  }

  /** forgetLive drops what the Live screen held for the installation the
   * console was open on, so the next one is read afresh rather than drawn from
   * the frame and the burn history of an installation that is gone. */
  const forgetLive = (): void => {
    live.drawn = null
    live.rigor = new Map()
    live.rigorAt = 0
    history = new BurnHistory()
    // Spend's two documents and the views read from them are this
    // installation's: the caps are its budget, and a cached view carries its
    // figures. The next installation is read afresh rather than drawn from
    // another one's spend.
    spendBudget = null
    spendCapsSentence = null
    spendViews.clear()
    // The Projects screen's view is the same kind of fact: it is this
    // installation's caps, and a cached one carries its figures.
    projectsViews.clear()
    // And the Coordinator's: the stored view is one installation's own
    // repositories, so a view read for another one is never drawn under it.
    coordinatorViews.clear()
    // The graph is a picture of **one installation's** plan, laid out by a worker
    // this console started for it: both are dropped here, so the next
    // installation is drawn from its own document by its own engine rather than
    // from positions computed for a checkout that is gone.
    graph.screen?.dispose()
    graph.screen = null
    graph.readAt = 0
    // The plan and the run list the Graph cache holds are this installation's
    // own: a plan of the old one is never drawn under the new one's positions.
    graphViews.clear()
    graph.runs = []
  }

  /** showIssue puts one installation's issue in the main area (owner's design
   * ruling of 2026-10-25): a view like Run detail, with no navigation button
   * and no Live poll under it. */
  const showIssue = (row: RailRow, focus = false): void => {
    markCurrent('issue')
    rail.view(row.project)
    const drawn = drawIssue(surface, row, issueActions)
    replace(drawn.root)
    if (focus) drawn.headline.focus()
  }

  /** leaveIssue is where the main area goes when the issue it showed is gone:
   * the open installation's own issue when it cannot be read, Live otherwise. */
  const leaveIssue = (): void => {
    const open = railNow.find((row) => row.open)
    if (open !== undefined && !open.readable) {
      showIssue(open)
      return
    }
    if (open === undefined && railNow.length === 0 && onboardNow !== null) {
      showOnboarding(onboardNow)
      return
    }
    show('live')
  }

  /** answered draws one rail action's answer: the refusal in the CLI's or the
   * store's own words, or the fresh list -- and the issue of the row the action
   * was about while it still has one. */
  const answered = (operation: string, reply: unknown, project: string): void => {
    const answer = replyData(reply)
    if (!answer.ok) {
      refuse(operation, answer.sentence)
      return
    }
    const next = railRows(reply)
    if (next === null) return
    const openBefore = railNow.find((row) => row.open)?.project
    railNow = next
    onboardNow = railOnboarding(reply) ?? onboardNow
    rail.setRows(next)
    if (next.find((row) => row.open)?.project !== openBefore) forgetLive()
    const still = next.find((row) => row.project === project)
    if (still !== undefined && still.issue !== 'none') {
      showIssue(still)
      return
    }
    leaveIssue()
  }

  /** choose switches the console to one installation. On success the main
   * process replaces this window with one for that installation, so the answer
   * drawn here is the refusal, or a list from a host that kept the window. */
  const choose = async (project: string): Promise<void> => {
    answered('installation.choose', await railBridge.chooseInstallation({ project }), project)
  }

  const issueActions: IssueActions = {
    choose: (project) => {
      const row = railNow.find((each) => each.project === project)
      if (row?.open === true) {
        show('live')
        return
      }
      void choose(project)
    },
    remove: (project) => {
      void railBridge
        .removeInstallation({ project })
        .then((reply) => answered('installation.remove', reply, project))
    },
    checkout: (project, checkout) => {
      void railBridge
        .setInstallationCheckout(checkout === null ? { project } : { project, checkout })
        .then((reply) => answered('installation.checkout', reply, project))
    },
    reveal: (project) => {
      void railBridge.revealInstallation({ project }).then((reply) => {
        const answer = replyData(reply)
        if (!answer.ok) refuse('installation.reveal', answer.sentence)
      })
    },
  }

  /**
   * activate is a click on a rail row (or its shortcut key). A row with an issue
   * opens that issue in the main area -- the warning mark is the door to it --
   * and its issue view carries the switch when the row is readable. A row with
   * none switches the console to it, or, when it is already the open one, puts
   * the dashboard back.
   */
  wiring.activate = (row: RailRow, byKeyboard: boolean): void => {
    if (row.issue !== 'none' || !row.readable) {
      showIssue(row, byKeyboard)
      return
    }
    if (row.open) {
      if (currentView === 'issue') show('live')
      return
    }
    void choose(row.project)
  }

  // ---- The first read ---------------------------------------------------

  // The first read is not a poll: the screen is drawn once whatever the window's
  // visibility is, so a console that opened behind another window still has the
  // figures it read. The interval is what pauses. A console open on an
  // installation it cannot read draws that installation's issue instead and
  // reads nothing (the owner's ruling: "If NO installation is readable, show the
  // issue view of the first one in the main area").
  const opened = railNow.find((row) => row.open)
  if (opened !== undefined && !opened.readable) showIssue(opened)
  else if (opened === undefined && railNow.length === 0 && onboardNow !== null) showOnboarding(onboardNow)
  else await pollLive()
  syncPolling()

  document.addEventListener('visibilitychange', () => {
    syncPolling()
  })

  // ---- The rail's shortcut keys -----------------------------------------

  // The shortcut keys are the *document's* and not the rail's, which is what
  // design section 4.3 means by "without walking the tab order at all": the key
  // reaches a row from wherever the operator is standing, and the rail is the
  // region it addresses rather than the place the keyboard has to be. The
  // modifier is the platform's (design section 4.1), so the key is read against
  // the navigator this window was handed, and a key the console has taken is
  // prevented -- a shortcut that also did whatever the platform does with it
  // would be two actions for one keystroke. Which keys those are is
  // `shortcutRow`'s whole answer: the first three rows and no fourth, read from
  // the physical key rather than from the character the layout produced, and
  // everything it answers `null` for is the window's and is not prevented here.
  const mac = isMacPlatform(navigator)
  document.addEventListener('keydown', (event: KeyboardEvent) => {
    const index = shortcutRow(event, mac)
    if (index === null) return
    event.preventDefault()
    rail.selectAt(index)
  })
}

// The one moment this file gets in a renderer. `startConsole` answers every
// failure as a compatibility and so does not reject; the `catch` is for a
// document this file cannot draw into at all, and then the right drawing is
// none.
const scope = globalThis as {
  readonly fugaro?: unknown
  readonly document?: unknown
  readonly navigator?: unknown
}
if (hasDashboardContext(scope)) {
  void bootDashboard({
    fugaro: scope.fugaro,
    document: scope.document as Document,
    navigator: scope.navigator,
  }).catch(() => undefined)
}
