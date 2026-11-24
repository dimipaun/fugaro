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
//     parsing, no navigating attribute and no style. That is
//     `src/renderer/app.ts`'s `ElementLike`, widened only by the `tag` and
//     `append` that `SkewElement` declares, and it is design section 5.1 rule
//     7 reaching all the way to the one place a real document exists. Two
//     exceptions exist, and both are named here rather than left for a reader to
//     find in the wrapper:
//     - `domSurface`'s `nodeOf`, which this file uses to attach a click listener
//       to a drawn row: a listener is not a *write*, a screen never sees a node,
//       and the alternative -- a screen that handled its own events -- is the
//       interface widening the plan's rule exists to prevent.
//     - **the settings panel's search, and nothing else** (console-styling
//       section 1.1, the console-improvements plan's SU2a): the wrapped element
//       also carries `addEventListener`, whose listener takes no argument, and a
//       `value` getter, and `screens/projects.ts`'s `SettingsElement` is the one
//       interface in the tree that asks for either. That section filters the
//       document this build has already read: a keystroke opens no read, names
//       no command and writes nothing, it reads no event object, and the class
//       and the `hidden` attribute it moves are inside the drawing surface a
//       screen already has -- so it is not the event-handling screen the
//       exception above is about. A screen that wants a *command* run still goes
//       through the bridge, as every one of them does.
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
import { newOpsFrom } from './new-bridge'
import { readStream } from './stream'
import type { StreamPaint, StreamWhole } from './stream'
import { drawWizard } from './wizard/wizard'
import type { NewOps, NewOpsReply, SettingsEditorOutcome } from '../shared/new-ops'
import { readGate } from './gating'
import { disabledWrite } from './writes'
import type { CompatibilityBridge, MountLike, ScreenGate } from './app'
import type { Gate } from './gating'
import { formatReading, measured, notional, readNumber, unavailable } from '../shared/availability'
import type { Reading } from '../shared/availability'
import { rigorIndex } from '../shared/rigor'
import type { RigorView } from '../shared/rigor'
import {
  decodeGraphDocument,
  decodeModelsList,
  decodeOrchestratorMap,
  decodePlanStatus,
  decodeRunsSummary,
  decodeSnapshot,
} from '../shared/schema'
import type {
  GraphView,
  ModelsListView,
  OrchestratorMapView,
  PlanStatusView,
  RunsSummaryView,
} from '../shared/schema'
import { sanitizeRefusal } from '../shared/sanitize'
import { LIVE_STALE_AFTER_SECONDS, BurnHistory, HourHistory, buildLiveModel, renderLive } from './screens/live'
import type { FeedHealth, LiveModel } from './screens/live'
import {
  SPEND_DEFAULT_CONTROLS,
  capsOf,
  fillSpendCaps,
  fillSpendReport,
  personView,
  readCapsStream,
  readReportStream,
  renderSpendFrame,
} from './screens/spend'
import type {
  DrawnSpendFrame,
  PeopleReader,
  SpendCaps,
  SpendControls,
  SpendModel,
  SpendReader,
} from './screens/spend'
import { cacheInvalidator } from './cache-invalidate'
import type { CacheScope } from './cache-invalidate'
import { prewarm } from './prewarm'
import type { PrewarmTab } from './prewarm'
import {
  BUSY_SLOT_CLASS,
  CACHE_FRESH_MS,
  CACHE_LIMIT,
  cacheKey,
  renderRetry,
  renderStamp,
  resultCache,
  sectionRead,
} from './section'
import {
  COORDINATOR_SHAPE,
  PROJECTS_SHAPE,
  PROJECTS_MODELS_SHAPE,
  SPEND_CAPS_SHAPE,
  SPEND_REPORT_SHAPE,
} from './skeleton'
import {
  COORDINATOR_ATTRIBUTE,
  COORDINATOR_BUSY,
  COORDINATOR_NODE_CLASS,
  COORDINATOR_TASKS_CLOSE_CLASS,
  coordinatorScreen,
  coordinatorTasks,
  renderCoordinatorFloor,
} from './screens/coordinator'
import type { CoordinatorScreen } from './screens/coordinator'
import type { SectionRead } from './section'
import {
  PROJECTS_BUSY,
  PROJECTS_MODELS_BUSY,
  PROJECTS_SETTINGS_BUSY,
  SETTINGS_PENCIL_CLASS,
  SETTINGS_PENCIL_KEY,
  buildProjectsModel,
  buildProjectsModels,
  buildProjectsSettings,
  fillProjects,
  fillProjectsModels,
  fillProjectsSettings,
  readBudgetStream,
  renderProjectsFrame,
} from './screens/projects'
import type { DrawnProjectsFrame, ProjectsModel } from './screens/projects'
import { ACCOUNT_KEYS_BUSY, buildAccountKeys, fillAccountKeys } from './screens/accountkeys'
import {
  CREDENTIALS_SERVICE_BUSY,
  buildCredentialsService,
  fillCredentialsService,
} from './screens/credentialsservice'
import {
  LICENCE_BUSY,
  buildLicence,
  fillLicence,
  licenceSaveOutcomeOf,
  renderLicenceOutcome,
} from './screens/licence'
import type { LicenceDrawn } from './screens/licence'
import { SEATS_BUSY, buildSeats, fillSeats } from './screens/seats'
import { MANAGED_BUSY, buildManagedModel, fillManaged } from './screens/managed'
import { RETIREMENTS_BUSY, buildRetirementsModel, fillRetirements } from './screens/retirements'
import type { OperationName } from '../main/ipc'
import {
  RUN_LOGS_BUSY,
  RUN_LOGS_SHAPE,
  RUN_RECORD_BUSY,
  RUN_RECORD_SHAPE,
  buildRunModel,
  fillRunLogs,
  fillRunRecord,
  renderRunFrame,
  runBusyLabel,
  runMainChildren,
  runRecordChildren,
} from './screens/run'
import { refusedState, renderUnavailable } from './screens/degraded'
import { renderCredentialsSide } from './screens/environment'
import { READING_STILL_MS, renderReading } from './screens/reading'
import type { ReadingTab } from './screens/reading'
import { noCheckoutActionKind, NO_CHECKOUT_SENTENCE, renderNoCheckout } from './screens/nocheckout'
import type { NoCheckoutOptions } from './screens/nocheckout'
import { renderCheckoutSelect } from './checkout'
import type { CheckoutChoice } from './checkout'
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
  GRAPH_SCOPE_ALL,
  GRAPH_SCOPE_CONTROL_CLASS,
  GRAPH_SCOPE_PLAN,
  GRAPH_ZOOM_STEP,
  SVG_NAMESPACE_URI,
  graphScreen,
  runIdsIn,
  scopeOf,
  tabKey,
  tabOf,
} from './screens/graph'
import type { GraphScreen, GraphScope } from './screens/graph'
import type { GraphTab } from './graph/model'
import {
  MOVE_CANCEL_CLASS,
  MOVE_CONFIRM_CLASS,
  MOVE_OPEN_CLASS,
  MOVE_TARGET_CLASS,
  MOVE_TARGET_UNCHOSEN,
  MOVE_UNDO_CLASS,
  MOVE_UNREADABLE_WORD,
  MoveFlow,
  moveAnswerOf,
} from './graph/move'
import type { MoveRequest, MoveSheet } from './graph/move'
import {
  SKEW_HEADLINES,
  hidesView,
  launchSupported,
  modelsListSupported,
  orchestratorMapSupported,
  planStatusNoMergesSupported,
  runsListPageSupported,
  runsSummarySupported,
  seatsSupported,
} from '../shared/version'
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
  /** addEventListener and `value` are what a **section** needs to own its own
   * interaction, and the settings panel is the one that does (the
   * console-improvements plan's SU2a, `projects.ts`'s `SettingsElement`): its
   * search filters a document this build already read, so the section binds it
   * itself rather than handing a read or a command to this file's seam. `value`
   * is the box's own text, which is not its `textContent`, and it is a getter
   * for that reason -- a keystroke changes the node, not the wrapper.
   *
   * A screen never reached either of them through `SkewElement`, which is
   * untouched by both. */
  addEventListener(type: string, listener: () => void): void
  readonly value: string
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
      addEventListener: (type: string, listener: () => void) => {
        node.addEventListener(type, listener)
      },
      get value(): string {
        // Only a form control has one, and the one caller is the settings
        // panel's search box: `Element` does not declare the member, and a
        // wrapper for anything else throws only where a reader asked for it.
        return (node as Element & { value: string }).value
      },
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
 * ReadBridge is the twenty-three reads the screens make (Task 3.4c; console L5b
 * added `statsRuns`, Bitbucket credentials T1.2 added `coordinatorStatus`,
 * GV2.3b added `planStatus`, coordinator v2 K3 added `coordinatorShow`, K5 added
 * `coordinatorIntents`, console 0.119.0 added `summaryRuns` -- the
 * Coordinator's jobs-free totals read -- and `listModels`, the Project tab's
 * models panel, and the settings-edit plan's C1 added `readSettings`, the same
 * tab's settings panel, and the graph plan's CG9a added `graphRead`, the Graph
 * tab's read of the versioned graph document, and the managed-project screens
 * plan's CM4 added `managedStatus`, `policyShow` and `environmentReport` -- the
 * same tab's four later sections and its policy panel's own document, and the
 * subscriptions plan's S2.7 added `readLicence`, the licence key's own section of
 * the same tab -- **the one read here that spawns no child at all**, because the
 * console keeps that key itself -- and the graph plan's CG9b added `graphWatch`,
 * the same tab's live feed of the graph document, and the subscriptions plan's
 * S4.2 added `readSeats`, the same tab's advisory seat estimate of who launches
 * jobs on the installation, and the console-fixes plan's CG7b added
 * `orchestratorMap`, the Coordinator tab's map of the project's active
 * orchestrators), named one
 * by one for the reason `CompatibilityBridge` names one: this file should reach
 * for exactly the operations it draws, and a reader should be able to see which
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
  summaryRuns(args: Readonly<Record<string, unknown>>): Promise<unknown>
  listModels(args: Readonly<Record<string, unknown>>): Promise<unknown>
  readSettings(args: Readonly<Record<string, unknown>>): Promise<unknown>
  coordinatorStatus(args: Readonly<Record<string, unknown>>): Promise<unknown>
  coordinatorShow(args: Readonly<Record<string, unknown>>): Promise<unknown>
  coordinatorIntents(args: Readonly<Record<string, unknown>>): Promise<unknown>
  diagnoseRun(args: Readonly<Record<string, unknown>>): Promise<unknown>
  readRunLogs(args: Readonly<Record<string, unknown>>): Promise<unknown>
  runLogLink(args: Readonly<Record<string, unknown>>): Promise<unknown>
  planStatus(args: Readonly<Record<string, unknown>>): Promise<unknown>
  graphRead(args: Readonly<Record<string, unknown>>): Promise<unknown>
  graphWatch(args: Readonly<Record<string, unknown>>): Promise<unknown>
  managedStatus(args: Readonly<Record<string, unknown>>): Promise<unknown>
  policyShow(args: Readonly<Record<string, unknown>>): Promise<unknown>
  environmentReport(args: Readonly<Record<string, unknown>>): Promise<unknown>
  readLicence(args: Readonly<Record<string, unknown>>): Promise<unknown>
  readSeats(args: Readonly<Record<string, unknown>>): Promise<unknown>
  orchestratorMap(args: Readonly<Record<string, unknown>>): Promise<unknown>
}

/** readBridgeFrom is the twenty-three reads over the structural bridge. A missing
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
    summaryRuns: (args) => ask('summaryRuns', args),
    listModels: (args) => ask('listModels', args),
    readSettings: (args) => ask('readSettings', args),
    coordinatorStatus: (args) => ask('coordinatorStatus', args),
    coordinatorShow: (args) => ask('coordinatorShow', args),
    coordinatorIntents: (args) => ask('coordinatorIntents', args),
    diagnoseRun: (args) => ask('diagnoseRun', args),
    readRunLogs: (args) => ask('readRunLogs', args),
    runLogLink: (args) => ask('runLogLink', args),
    planStatus: (args) => ask('planStatus', args),
    graphRead: (args) => ask('graphRead', args),
    graphWatch: (args) => ask('graphWatch', args),
    managedStatus: (args) => ask('managedStatus', args),
    policyShow: (args) => ask('policyShow', args),
    environmentReport: (args) => ask('environmentReport', args),
    readLicence: (args) => ask('readLicence', args),
    readSeats: (args) => ask('readSeats', args),
    orchestratorMap: (args) => ask('orchestratorMap', args),
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
 * LicenceBridge is the Project tab's second write: `licence.save`, the paste
 * that keeps the licence key this installation runs under (the subscriptions
 * plan's S2.7). It is a bridge of its own rather than a member of `ReadBridge`
 * for `LaunchBridge`'s reason: that interface is the reads a screen is drawn
 * from, and this one writes a credential to disk.
 *
 * **It carries the key and nothing else.** The object it is handed is the one
 * argument the operation declares -- the pasted text, in this console's own
 * `licenceKey` kind (`ipc.ts`), checked for length and for invisible characters
 * before it is dispatched -- and never a path, a project or a flag: which
 * installation's store the key lands in is the main process's own active
 * installation (`main/index.ts`), so a renderer cannot name one the rail did not
 * choose.
 *
 * It is answered the way the other bridges are: a member that is missing or
 * fails to answer is `null`, and the press draws a sentence it could not read
 * rather than claiming a write. A reply that arrived is the same licence state
 * the read answers -- which carries no key text -- so nothing this bridge returns
 * is the key that was pasted.
 */
export interface LicenceBridge {
  saveLicence(args: Readonly<Record<string, unknown>>): Promise<unknown>
}

export function licenceBridgeFrom(fugaro: unknown): LicenceBridge {
  const ask = (name: string, args: Readonly<Record<string, unknown>>): Promise<unknown> => askMember(fugaro, name, args)
  return { saveLicence: (args) => ask('saveLicence', args) }
}

/**
 * SETTINGS_EDIT_OUTCOME_CLASS is the settings outcome's hook: the sentence a
 * pencil's press draws under the row it was pressed on, marked `aria-live` when
 * it is drawn -- an answer that lands while the operator is looking elsewhere (a
 * refusal from the flow's own window, the CLI's account of a write) is announced
 * the way the launch form's outcome region is.
 *
 * **The row is where it belongs.** The entry appends it to the `li` the pencil
 * sits in, so the key, the scope it belongs to and the value a run gets stay on
 * the screen around the sentence, and the section's own repaint takes it down
 * with the row it was about.
 */
export const SETTINGS_EDIT_OUTCOME_CLASS = 'projects-settings-editor-outcome'

/** SettingsEditorRead is one `settings.editor.open` answer read into the two
 * facts the row draws: whether a write happened, and the sentence to draw either
 * way. There is no third state -- an operation no handler answers, a refusal the
 * channel answered with and an answer this build cannot read are all a sentence
 * saying so. */
export interface SettingsEditorRead {
  readonly written: boolean
  readonly sentence: string
}

/**
 * settingsEditorRead reads one reply into those two facts. The operation's own
 * decoder has already turned the wire into the contract's shape
 * (`SettingsEditorOutcome`, `src/shared/new-ops.ts`), so what is left is the two
 * halves of an answer: the outcome itself, or the gap that stands in for one.
 * Both carry the sentence the row draws, and both go through `textOf`, because a
 * sentence from the CLI is text like any other.
 *
 * **A write that happened and a write that did not are told apart by the
 * answer's own `ok` field and never by its sentence**, the way `launchOutcomeOf`
 * deliberately does not do: a refusal here is the CLI's own words (the
 * compare-and-swap's sentence, a permissions refusal, the flow's own
 * already-open sentence), no two of which share a shape, so the field the main
 * process set is the only thing that knows.
 */
export function settingsEditorRead(reply: NewOpsReply<SettingsEditorOutcome>): SettingsEditorRead {
  const sentence = (raw: string): string => textOf(raw, 'the settings editor’s sentence')
  if (!reply.ok) return { written: false, sentence: sentence(reply.gap.sentence) }
  return { written: reply.value.ok, sentence: sentence(reply.value.sentence) }
}

/**
 * clearSettingsOutcome takes down the outcome line a previous press of this
 * row's pencil drew, if one is still there. A second press asks the question
 * again, and two sentences under one row would be two answers to one question
 * with the older one first.
 *
 * It walks the row's own children rather than holding a reference to the node it
 * appended: the section repaints its rows, so a press can outlive the row it
 * started in, and a node this file remembered would be one a repaint had already
 * detached.
 */
function clearSettingsOutcome(row: Element): void {
  const kept = [...row.children].filter(
    (child) => child.getAttribute('class') !== SETTINGS_EDIT_OUTCOME_CLASS,
  )
  row.replaceChildren(...kept)
}

/**
 * LinkBridge is the one operation the Coordinator's pull request numbers use:
 * `link.openPullRequest` (K2a), which takes the triple -- provider, repository,
 * number -- and opens it in the operator's browser. It is a bridge of its own
 * rather than a member of `ReadBridge` for `LaunchBridge`'s reason: a reader
 * looking for what this console can *do* should not find it among the things it
 * can look at.
 *
 * **It is handed a triple and never a URL.** The renderer builds no destination
 * at all: main builds it from these three fields with its own alphabet and hands
 * the result to the one validated shell call, so a document's `url` field has no
 * path to a browser even when the field is a `javascript:` one.
 *
 * It is answered the way the other bridges are: a member that is missing or
 * fails to answer is `null`, and a number pressed then opens nothing -- which is
 * the same answer main gives for a triple it cannot build a URL from.
 */
export interface LinkBridge {
  openPullRequest(args: Readonly<Record<string, unknown>>): Promise<unknown>
}

export function linkBridgeFrom(fugaro: unknown): LinkBridge {
  const ask = (name: string, args: Readonly<Record<string, unknown>>): Promise<unknown> => askMember(fugaro, name, args)
  return { openPullRequest: (args) => ask('openPullRequest', args) }
}

/**
 * MoveBridge is the Graph screen's one write: `roadmap.move`, a task of the
 * project's roadmap record dragged into another release or out of every one
 * (GV3.2, design section 5.1 rule 14). It is a bridge of its own rather than a
 * member of `ReadBridge` for `LaunchBridge`'s reason: that interface is the reads
 * a screen is drawn from, and this one **writes the record every console of this
 * project reads**.
 *
 * **It is handed two ids and one boolean.** `node` is the graph document key of
 * the row that was dragged and `toRelease` the record's key for the box it was
 * dropped on (the empty string for the unassign) -- both ids that document
 * printed, checked against it in the main process before any word is built -- and
 * `confirmed` decides which half of the operation runs: absent or false answers
 * the move's own command line and starts no child, and true runs it, and only if
 * the argv this console drew is the argv main builds. **No renderer here states a
 * task id, a release name or a flag**: what the CLI reads is spelled out of the
 * document the console last read, so a title can never reach a command line.
 *
 * It is answered the way the other writes are: a member that is missing or fails
 * to answer is `null`, and the sheet draws a sentence it could not read rather
 * than claiming a move. A reply that arrived is one `RoadmapMoveAnswer` (a
 * preview, or §6.24's outcome for the confirmed half).
 */
export interface MoveBridge {
  moveRoadmapTask(args: Readonly<Record<string, unknown>>): Promise<unknown>
}

export function moveBridgeFrom(fugaro: unknown): MoveBridge {
  const ask = (name: string, args: Readonly<Record<string, unknown>>): Promise<unknown> => askMember(fugaro, name, args)
  return { moveRoadmapTask: (args) => ask('moveRoadmapTask', args) }
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
 *
 * **A WSL installation gains the credentials line** (T-G5): `renderCredentialsSide`
 * appends the console's own statement of which side of the boundary the CLI's
 * credentials are on, and it is called here rather than by a screen because this
 * line is the only surface that receives the installation's `distro` -- the
 * field `ActiveInstallation` carries exactly when the installation is a WSL one
 * (`src/main/installations.ts`, T-D1). Nothing about the three lines above
 * changes: the statement is added, and the host case draws exactly what it drew
 * before.
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
  renderCredentialsSide(section, factory, reply)
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

/**
 * DragEventLike is the one field of a drag this file reads: the transfer a
 * browser writes a drag's payload into, which is what makes a drag start at all
 * (`dragstart`), and which the move reads nothing back out of -- the task a drag
 * carries is held in the entry's own closure, so nothing a page writes into a
 * transfer can name a task (`wireGraph`).
 *
 * Structural, for the reason `KeyEventLike` is: a case drives this under
 * `vitest` with an object it built, and a real `DragEvent` satisfies it.
 */
export interface DragEventLike {
  readonly dataTransfer?: { setData(type: string, value: string): void } | null
  preventDefault(): void
}

/** dragOn attaches a drag listener to a real node, with the same cast and for the
 * same reason `keydownOn` documents: `Element`'s own signature types the argument
 * as `Event`, which declares none of the fields these handlers read. */
function dragOn(node: Element, name: string, listener: (event: DragEventLike) => void): void {
  node.addEventListener(name, listener as unknown as EventListener)
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
/** RAIL_ADD_PROJECT_CLASS is the foot's **second** door (SW3c, R4): the add row
 * above it opens the picker window, which is how an installation is added by
 * checkout path, and this one opens the setup wizard in the main area. It is a
 * class of its own rather than a second `console-rail-add` because the two are
 * different affordances -- and because the rail's own e2e locator holds the add
 * row to being exactly one element. */
export const RAIL_ADD_PROJECT_CLASS = 'console-rail-add-project'
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
/** RAIL_ADD_PROJECT_LABEL is the second door's own word, and it is the same word
 * the picker window's button carries (`ADD_PROJECT_LABEL`,
 * `src/renderer/installations.ts`): one affordance, two windows. */
export const RAIL_ADD_PROJECT_LABEL = 'Add project'

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
  /**
   * `checkoutChoices` is the menu the dashboard's and the Graph's checkout
   * chooser offers: one entry per repository, from the rows the CLI re-validated
   * **and** enrolled (`InstallationsListRow.checkoutChoices`,
   * `src/main/onboarding.ts`).
   *
   * Both strings are kept **raw**, for the reason `repositories` below is: the
   * `path` is what the main process matches against the list it built, and a
   * folded or redacted copy would be a directory it refuses. The screen that
   * draws an entry redacts the label it draws and keeps the raw string beside it
   * (`src/renderer/checkout.ts`'s `checkoutLabel`).
   */
  readonly checkoutChoices: readonly CheckoutChoice[]
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
        checkoutChoices: checkoutChoicesOf(row['checkoutChoices']),
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
 * checkoutChoicesOf reads one row's `checkoutChoices` as this file draws it: the
 * menu entries, each with its repository, its path and its count, or the empty
 * list for anything this file cannot read a whole entry from.
 *
 * **Nothing is sanitized here and nothing is dropped for what it says.** The
 * `path` is the value the main process matches against its own list, so it is
 * kept exactly as it arrived, and an entry is dropped only when it is not an
 * entry at all: a row that is not an object, or one whose `path` is not a
 * non-empty string, names no directory to send back. The repository is the
 * entry's label and a value the console only draws (`checkoutLabel`), so a
 * missing one is drawn as this console's marker rather than dropping a
 * directory the operator could otherwise pick. `more` is the main process's
 * count; anything that is not a non-negative finite number is read as none,
 * because a menu entry that said "+NaN more" would be a sentence about nothing.
 */
function checkoutChoicesOf(raw: unknown): readonly CheckoutChoice[] {
  if (!Array.isArray(raw)) return Object.freeze([])
  const choices: CheckoutChoice[] = []
  for (const one of raw) {
    if (typeof one !== 'object' || one === null || Array.isArray(one)) continue
    const entry = one as Record<string, unknown>
    const path = entry['path']
    if (typeof path !== 'string' || path === '') continue
    const repository = entry['repository']
    const more = entry['more']
    choices.push(
      Object.freeze({
        repository: typeof repository === 'string' ? repository : '',
        path,
        more: typeof more === 'number' && Number.isFinite(more) && more > 0 ? Math.floor(more) : 0,
      }),
    )
  }
  return Object.freeze(choices)
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
  openAddProject: () => void = () => undefined,
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

  // **The second door in the same foot** (SW3c, R4): with installations present,
  // a console can set up another one from here, and the wizard opens in the main
  // area over the installation it is reading. It is drawn **only with a row on
  // the rail**: a machine with no installation is already showing the wizard in
  // the main area (the onboarding screen), and a door beside it would be a second
  // control for one state.
  if (rows.length > 0) {
    const addProject = factory.create('button')
    addProject.setAttribute('class', RAIL_ADD_PROJECT_CLASS)
    addProject.setAttribute('type', 'button')
    addProject.textContent = RAIL_ADD_PROJECT_LABEL
    foot.append(addProject)
    surface.nodeOf(addProject).addEventListener('click', openAddProject)
  }

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

/**
 * clipboardCopy is the clipboard the wizard's Copy buttons hand their text to,
 * built from the navigator this window was given and never from a global -- a
 * case drives it, and a window that withheld it is not a crash. A navigator
 * with no `clipboard.writeText` (a document under plain Node, an Electron window
 * with the permission off) copies nothing at all: the command is drawn beside
 * the button either way, so a clipboard nobody has costs the operator a
 * selection and never the text.
 *
 * A write that rejects is swallowed **on purpose**: the console has no place to
 * report a clipboard failure that would not be noise under a line the operator
 * can already read, and an unhandled rejection would be a renderer error for a
 * fact with no consequence.
 */
export function clipboardCopy(navigator: unknown): (text: string) => void {
  const scope = navigator as { clipboard?: { writeText?: unknown } } | null | undefined
  const clipboard = scope?.clipboard
  const write = clipboard?.writeText
  if (clipboard === undefined || typeof write !== 'function') return () => {}
  const text = write as (value: string) => unknown
  return (value: string): void => {
    void Promise.resolve(text.call(clipboard, value)).catch(() => {})
  }
}

/** OnboardingDeps is what the first-installation screen needs to draw the
 * wizard instead of the command line: the console's door to the operations
 * (`setup.plan` and `setup.apply`, SW3a), the clipboard a `left_for_you` line's
 * Copy hands its text to, and the clock the feed is drawn on. All three are the
 * caller's -- this file spawns nothing, copies nothing and owns no timer -- and
 * a caller with none (a case, a window that never built its bridge) gets the
 * command line this view has always drawn. */
export interface OnboardingDeps {
  readonly ops: NewOps
  readonly copy: (text: string) => void
  readonly paint?: StreamPaint
}

/** drawOnboarding is the main area of a console with no installation: the
 * picker's own words for the screen (`ONBOARDING_COPY`), the resolution's
 * sentence when the CLI is missing, and -- for a machine whose CLI is there and
 * has no installation -- either the one command from the main process or, when
 * the caller can drive the operations, the wizard (SW3b, D2904).
 *
 * **The wizard takes the place of the body and the command.** The body's own
 * sentence introduces a command for the operator to run ("Create one with:"),
 * which is not what happens when the console asks `setup.plan` itself, so the
 * two go together and the wizard is drawn in their place. The title stays: it is
 * the heading this screen shares with the picker, and the wizard draws none of
 * its own. */
export function drawOnboarding(
  surface: DomSurface,
  onboarding: RailOnboarding,
  deps?: OnboardingDeps,
): SkewElement {
  const factory = surface.factory
  const copy = ONBOARDING_COPY[onboarding.kind]
  const section = factory.create('section')
  section.setAttribute('class', ONBOARDING_CLASS)
  const title = factory.create('h1')
  title.setAttribute('class', ONBOARDING_TITLE_CLASS)
  title.textContent = copy.title
  section.append(title)
  if (onboarding.reason !== '') {
    const reason = factory.create('p')
    reason.setAttribute('class', ONBOARDING_REASON_CLASS)
    reason.textContent = onboarding.reason
    section.append(reason)
  }
  if (onboarding.kind === 'first-installation' && deps !== undefined) {
    const wizard = drawWizard({
      factory,
      plan: (request) => deps.ops.planSetup(request),
      apply: (request) => deps.ops.applySetup(request),
      copy: deps.copy,
      ...(deps.paint === undefined ? {} : { paint: deps.paint }),
    })
    // The wizard's own factory is this document's, so the element it answers is
    // one of this section's children -- the two surfaces' `append` are the same
    // call on the same node, and `WizardElement` says only that this module
    // touches no more of it than a wizard needs.
    section.append(wizard as unknown as SkewElement)
    return section
  }
  const body = factory.create('p')
  body.setAttribute('class', ONBOARDING_BODY_CLASS)
  body.textContent = copy.body
  section.append(body)
  const command = factory.create('code')
  command.setAttribute('class', ONBOARDING_COMMAND_CLASS)
  command.textContent = onboarding.command
  section.append(command)
  return section
}

// ---------------------------------------------------------------------------
// Add project: the same wizard, for a console that already has an installation
// ---------------------------------------------------------------------------

export const ADD_PROJECT_CLASS = 'installation-add-project'
export const ADD_PROJECT_TITLE_CLASS = 'installation-add-project-title'
export const ADD_PROJECT_LEAD_CLASS = 'installation-add-project-lead'
export const ADD_PROJECT_CANCEL_CLASS = 'installation-add-project-cancel'

/** ADD_PROJECT_TITLE is the heading of the screen the rail's and the picker's
 * Add project door opens (SW3c, R4). The first-run screen's heading is the
 * picker's own (`ONBOARDING_COPY`); this one is the rail's word for the door,
 * because the screen behind it is the same wizard with a different subject: an
 * installation that already exists beside the one being set up. */
export const ADD_PROJECT_TITLE = 'Add project'
export const ADD_PROJECT_CANCEL_LABEL = 'Cancel'
/** ADD_PROJECT_LEAD is the one sentence that says what this screen is. It says
 * *second* because that is the whole difference from the first run, and it
 * repeats the rule the run itself keeps: `setup` is asked in an empty directory,
 * so nothing here wires a repository -- repositories are added afterwards from
 * Manage. */
export const ADD_PROJECT_LEAD = 'Set up a second installation beside this one.'

/**
 * drawAddProject is the main area for a console that already has an installation
 * and was asked for another (SW3c, R4): the heading, the one sentence, **the
 * same wizard as the first run** (`drawWizard`, SW3b -- one screen, two doors),
 * and Cancel, which is what makes this a view rather than a flow the operator
 * cannot leave.
 *
 * It draws its own `h1` where the first-run screen uses the picker's, and that
 * is deliberate: this screen is not the onboarding screen and must not borrow
 * its title. Everything below the heading is SW3b's module unchanged --
 * including that the wizard reads no answers from anywhere but the operator, and
 * that the setup run itself is main's (SW3a), started from the bytes main holds.
 */
export function drawAddProject(
  surface: DomSurface,
  deps: OnboardingDeps & {
    readonly cancel: () => void
    /** applied is called when a setup run has ended, whatever its outcome: the
     * run is the moment the machine can hold an installation it did not hold
     * when this screen opened, so it is the moment the rail's list is worth
     * reading again. The wizard draws the outcome either way -- this hook is
     * about the list behind the screen, not about the screen. */
    readonly applied?: () => void
  },
): SkewElement {
  const factory = surface.factory
  const section = factory.create('section')
  section.setAttribute('class', ADD_PROJECT_CLASS)
  const title = factory.create('h1')
  title.setAttribute('class', ADD_PROJECT_TITLE_CLASS)
  title.textContent = ADD_PROJECT_TITLE
  section.append(title)
  const lead = factory.create('p')
  lead.setAttribute('class', ADD_PROJECT_LEAD_CLASS)
  lead.textContent = ADD_PROJECT_LEAD
  section.append(lead)
  const wizard = drawWizard({
    factory,
    plan: (request) => deps.ops.planSetup(request),
    apply: (request) =>
      deps.ops.applySetup(request).then((answer) => {
        deps.applied?.()
        return answer
      }),
    copy: deps.copy,
    ...(deps.paint === undefined ? {} : { paint: deps.paint }),
  })
  section.append(wizard as unknown as SkewElement)
  const cancel = factory.create('button')
  cancel.setAttribute('class', ADD_PROJECT_CANCEL_CLASS)
  cancel.setAttribute('type', 'button')
  cancel.textContent = ADD_PROJECT_CANCEL_LABEL
  surface.nodeOf(cancel).addEventListener('click', deps.cancel)
  section.append(cancel)
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
/** The directory the menu has selected, sent by the button under it. It names
 * the **checkout** and not the directory alone: the operator is choosing which
 * of the installation's repositories this console is to read from, and the menu
 * above it is what says which directory that is. */
export const ISSUE_CHECKOUT_LABEL = 'Use this checkout'
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

  // The menu offers one entry per repository this installation records a usable
  // checkout of, and never the directory it is already used from: that one has
  // its own control below ("Stop using this checkout"), and a menu whose first
  // entry was the state the operator is already in would be a control that does
  // nothing.
  const offered = row.checkoutChoices.filter((choice) => choice.path !== row.checkout)
  if (offered.length > 0 || row.checkout !== null) {
    const lead = factory.create('p')
    lead.setAttribute('class', ISSUE_CHECKOUT_LEAD_CLASS)
    lead.textContent = ISSUE_CHECKOUT_LEAD
    controls.append(lead)
    if (offered.length > 0) {
      const select = renderCheckoutSelect(factory, offered)
      controls.append(select)
      const element = factory.create('button')
      element.setAttribute('class', ISSUE_CHECKOUT_CLASS)
      element.setAttribute('type', 'button')
      element.textContent = ISSUE_CHECKOUT_LABEL
      surface.nodeOf(element).addEventListener('click', () => {
        actions.checkout(row.project, valueOf(surface, select))
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

/** drawHalt draws Halt fleet: designed, and not performed (`writes.ts`).
 *
 * Halt fleet is every run of the project (`budget kill` with no repository),
 * which the CLI gates as a project-level change (`accessGateProject`,
 * `ActionManageProject`), so the role decision is `manage-project`: an admin at
 * project scope may use it and everyone else reads why they may not (RB4). */
export function drawHalt(surface: DomSurface, roles: Gate): DomElement {
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
  return disabledWrite(factory, HALT_CLASS, HALT_LABEL, icon, roles.can('manage-project'))
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
 * The mode on a Live row is a **join**: `watch --json` answers `JSONRun`, whose
 * own `mode`/`from` are the run's mode and layer and nothing else of its rigor
 * block, so the block comes from `runs ls --json` and is matched to the row by
 * run id (`rigorIndex`, `shared/rigor.ts`). That read is a CLI invocation of
 * its own, and it is deliberately **not** the poll's: the poll runs at
 * `LIVE_POLL_MS`, and a
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

/**
 * RUNS_LIST_OPERATION and RUNS_STREAM_OPERATION are the two reads the
 * Coordinator's jobs part can be making, named as the console's operations are
 * named (`src/shared/new-ops.ts`, `src/main/ipc.ts`): the whole document
 * (`runs ls --json`), and the opt-in line-delimited read of the same command
 * (`runs ls --stream --json`) that main registers as `runs.stream`.
 *
 * **A refusal names its own command**, and these two are different commands with
 * different flags: a part that drew `runs.list` over a read that was
 * `runs.stream` would be pointing the operator at a command the console did not
 * run. Which one the part wears is the last read's own `source`
 * (`readStream`) and not a property of the screen.
 */
export const RUNS_LIST_OPERATION = 'runs.list'
export const RUNS_STREAM_OPERATION = 'runs.stream'

/**
 * COORDINATOR_SUMMARY_SINCE is the window the Coordinator's fleet strip reads
 * its runs for: seven days.
 *
 * It is the command's own default and the window `budget show`'s live counters
 * cover, so the two figures the strip draws about a period -- the window's runs
 * and the money the caps count -- are about the same one. It is one of the two
 * words the surface admits (`SUMMARY_SINCE_WINDOWS`, `src/main/ipc.ts`): a
 * window is the one thing about this read a renderer chooses, and the console's
 * own closed set is the day a screen opens on and the week.
 */
export const COORDINATOR_SUMMARY_SINCE = '7d'

/** GRAPH_BUSY is what the Graph section's mark says it is reading: the plan.
 * The mark belongs to the operator's own controls -- a stream tab, the Refresh
 * -- and neither to the poll's background cadence nor to the run list's
 * decoration: a mark for a read nobody asked for would flicker over a picture
 * that is already true. */
export const GRAPH_BUSY = 'Reading the plan…'

/** GRAPH_SCOPE_BUSY is the same mark's other label: a scope change is a read of
 * its own (`graph.read`), and the mark says which one is running. The two are
 * separate sentences because the mark is the only thing on the panel that says
 * what the console is doing, and "Reading the plan…" over a coordinator read
 * would name the wrong document -- the owner's rule of 2026-10-31 asks for a
 * label naming what is being read, and the two scopes read two different
 * documents. */
export const GRAPH_SCOPE_BUSY = 'Reading the coordinator…'

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
  // The graph is never withheld either, and for the reason the Live screen's tab
  // is not: what is behind it is repository state (`plan.status`), and the read
  // is not a spend read (`REDUCED_VIEW_OPERATIONS` names none of the graph's
  // commands), so a reduced console still draws the plan, its tasks and its
  // stages. Since S4 the plan's estimate carries one money figure, and that
  // figure is withheld **inside the drawing** rather than with the tab: the
  // entry asks `hidesView` for `spend` and hands the answer to the screen, which
  // draws the ready-by range and `n/a` where the cost was -- the same shape as
  // the Live screen's caps. It is `null` here rather than a fourth
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
  // The caller's own role (RB4, D2907; the read is WH1's `user ls --me --json`):
  // read once, here, off the version line the handshake already has, so the
  // header's write control is drawn with the role the CLI published rather than
  // with a guess. With no answer -- a CLI below `WHOAMI_CLI_FLOOR`, which is
  // unset until a release carries `--me`, a refusal, a bridge that cannot ask --
  // the gate allows every action and every control is drawn as it always was.
  // One door is built and both callers share it: the gate's own read here, and
  // the screens that ask a new operation when a control is pressed (`entry
  // .ts`'s `DashboardDeps.ops`). Building it twice would be two objects with one
  // behaviour, and the door holds nothing.
  const ops = newOpsFrom(scope.fugaro)
  const roles = await readGate(ops, gate.compatibility.cliVersion)
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
  const wiring: RailWiring = { activate: () => undefined, home: () => undefined, addProject: () => undefined }
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
    () => {
      status.switcher?.close(false)
      wiring.addProject()
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
    roles,
    bridge,
    ops,
    rail,
    railBridge,
    launchBridge: launchBridgeFrom(scope.fugaro),
    moveBridge: moveBridgeFrom(scope.fugaro),
    licenceBridge: licenceBridgeFrom(scope.fugaro),
    linkBridge: linkBridgeFrom(scope.fugaro),
    rows,
    railListed: listedRows !== null,
    onboarding,
    wiring,
    // The picker's Add project door, if it was the door this window was opened
    // for (SW3c): main consumed the request on this read, and this window is the
    // one opened for it.
    addProject: addProjectRequested(installation),
    navigator: scope.navigator,
  })
}

/** addProjectRequested reads the one member main adds to `installation.current`
 * for the window the picker's Add project door asked for (SW3c, R4). It is
 * `true` only when the answer says so: any other answer -- no installation, a
 * read that failed, an older main process -- leaves the console opening where it
 * always opens. */
export function addProjectRequested(installation: unknown): boolean {
  const answer = replyData(installation)
  if (!answer.ok || typeof answer.data !== 'object' || answer.data === null) return false
  return (answer.data as Record<string, unknown>)['addProject'] === true
}

/** RailWiring is the rail's click, bound by `mountDashboard` once the views a
 * click can show exist. */
interface RailWiring {
  activate: (row: RailRow, byKeyboard: boolean) => void
  /** home is the wordmark's click: the Live view. */
  home: () => void
  /** addProject is the rail's second foot door (SW3c, R4): the wizard, in the
   * main area, in place of the installation the console is reading. */
  addProject: () => void
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
      // `installation.current` carries neither the CLI's checkout list nor its
      // repository list -- `config list` is the only read that carries either --
      // so a console whose list read failed offers no checkout to choose and no
      // repository to launch in, and says so rather than guessing at one.
      checkoutChoices: Object.freeze([]),
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
  /** roles is the caller's own role, read at startup (RB4, D2907): the reason a
   * write control this console draws is disabled for is `roles.can(action)`. */
  readonly roles: Gate
  readonly bridge: ReadBridge
  /** ops is the C-OPS door to the nine new operations (`newOpsFrom`), built once
   * at startup and handed down rather than rebuilt by each screen: the read is
   * made when a control is pressed and not when the door is made, so a screen
   * that has one has everything a press needs. The Coordinator's jobs read is
   * the first caller (`runs.stream`, RS2). */
  readonly ops: NewOps
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
  /** moveBridge is the Graph screen's own write (`roadmap.move`, GV3.2): the
   * console's third write, and the one that changes what every other console of
   * the project is drawn from. */
  readonly moveBridge: MoveBridge
  /** licenceBridge is the Project tab's licence key (`licence.save`, S2.7): the
   * console's fourth write, and the one that writes and spawns nothing -- it
   * keeps a credential the console hands to every child it spawns. */
  readonly licenceBridge: LicenceBridge
  /** linkBridge is the Coordinator's numbers (`link.openPullRequest`, K2a): the
   * one operation that leaves this window, and the only one handed a triple
   * rather than a document's own field. */
  readonly linkBridge: LinkBridge
  readonly rows: readonly RailRow[]
  /** railListed is whether `rows` is the CLI's own list answer rather than the
   * fallback for a list this console could not read (`currentRailRows`). The
   * two are not the same fact: a fallback row carries an empty `checkouts`
   * because nothing was read, and the Graph's no-checkout card decides whether
   * it has an action from whether the CLI listed one. */
  readonly railListed: boolean
  /** addProject is whether this window was opened by the picker's Add project
   * door (SW3c, R4): the console opens on the wizard, in place of the
   * installation it is reading, and Cancel returns to that installation. */
  readonly addProject?: boolean
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
  const { document, surface, gate, roles, bridge, ops, rail, railBridge, launchBridge, moveBridge, licenceBridge, linkBridge, wiring, navigator, header } =
    deps
  const factory = surface.factory
  const theme = CONSOLE_THEME

  // The Live screen's own state, kept across polls: the two windows accumulate
  // samples, so they must outlive a drawing. A fresh pair per poll would be a
  // one-bar sparkline forever, and the hour line would never be longer than the
  // five-minute one.
  let history = new BurnHistory()
  let hourHistory = new HourHistory()
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

  /**
   * withRetry adds R1's Retry control to a refused page and wires it to the read
   * that page is about (`docs/console-improvements/reconciliation.md`: a refused
   * or timed-out section gets a control that re-spawns that section's own read,
   * beside the CLI's sentence and command).
   *
   * **The read is a caller's closure and never a word.** Which read a section is
   * waiting for is the one fact only the code that started it has -- `startCaps`
   * for the Spend caps, `reReadJobs` for the coordinator's job list -- so the
   * control is wired here and the read comes from the call site. Nothing is
   * derived from the operation name: this console has no table from an operation
   * back to a read, and inventing one would make the control's behaviour a
   * string's.
   *
   * **A refused page with no read to name draws no control.** A Retry with
   * nothing behind it is a control that answers a click with silence, and one
   * kind of refusal in this file has no read: a refusal that is the answer to a
   * *write* (the rail's own actions, `answered`) is not a section waiting on a
   * read -- the operator's own button is that door, and a second one beside the
   * sentence would be this console offering to repeat a write.
   */
  const withRetry = (page: SkewElement, retry: (() => void) | null): SkewElement => {
    if (retry === null) return page
    const control = renderRetry(factory)
    page.append(control)
    surface.nodeOf(control).addEventListener('click', retry)
    return page
  }

  /** refusedInto draws one refused **section** into the host it belongs to: the
   * section stays on the screen with its siblings (the owner's rule of
   * 2026-10-31), and the refusal is what is drawn in its place. */
  const refusedInto = (host: Drawn, page: SkewElement, retry: (() => void) | null): void => {
    appendTo(host, withRetry(page, retry))
  }

  /** refuse replaces the whole panel with a refusal -- one read whose failure is
   * the whole view's (the Live frame, the Graph's plan, a rail write) rather than
   * one section among others. */
  const refuse = (operation: string, sentence: string, retry: (() => void) | null = null): void => {
    replace(withRetry(refusedScreen(factory, operation, sentence, theme), retry))
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
   *
   * **The graph's own feed is started and stopped here too** (`syncGraphWatch`),
   * and not from `showGraph`: the two conditions are the same pair, and a tab
   * switch, a hidden window and a re-entry are all the same question. It is
   * called before the interval is settled so that a tab that has just come into
   * view is not left holding no feed until the next tick.
   */
  const syncPolling = (): void => {
    const wanted = (currentView === 'live' || currentView === 'graph') && scopeVisible()
    syncGraphWatch()
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
   *
   * **Leaving the Run screen ends both of its reads here**, rather than in
   * `show`'s row of doors: a run is left for the switch `show` makes and for the
   * ones it does not -- the rail's issue view and the onboarding screen -- and
   * both of that screen's sections have a read whose answer is written into
   * nodes the next view has already detached.
   */
  const markCurrent = (id: string): number => {
    const mine = claim()
    markButtons(id)
    if (id !== 'run') leaveRun()
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

  /** leaveRun is the same door for the Run screen's two sections: `showRun` sets
   * it. It is called from `markCurrent` and not from `show`, because a run is
   * left for anything at all -- a tab, the rail's issue view, the onboarding
   * screen -- and only `show` passes through the four doors above. Those two
   * reads write a fifteen-second line as well as a mark, and the line is armed
   * for a section nobody is looking at the moment the run is left. */
  let leaveRun: () => void = () => undefined

  /** startPrewarm is the queue that reads the other tabs into their caches once
   * the tab on screen has been painted (PW1, D2906). It is a door of the same
   * kind as the four above: the reads it starts belong to the sections below it
   * -- they are `bridge` calls and the tabs' own caches -- and the navigation
   * that owns the first paint is written above every screen. `drawLive` calls
   * it, and it is a variable here for the reason the doors are. */
  let startPrewarm: () => void = () => undefined

  /** prewarmed is whether that queue has already run for this installation. It
   * is started from the Live screen's first paint, and `drawLive` runs on every
   * poll tick -- so the flag is what keeps the console's reads for the *other*
   * tabs to once. `forgetLive` clears it with the rest of the installation's
   * state, because the caches it filled are that installation's. */
  let prewarmed = false

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
    backToView = () => show(id)
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

  /**
   * invalidator is this console's one cache invalidator (R2; F17): the five tabs
   * register what they hold and how to read it again, and a **write** names the
   * scopes it touched.
   *
   * It is built here rather than at the module's top because two consoles in one
   * process -- a window and a reload, a unit test's two surfaces -- are two
   * installations, and a shared registry would let one window's write clear the
   * other's caches.
   *
   * **A scope's read is the tab's own door, not a second one.** `show(scope)` is
   * exactly what the operator's click on that tab does -- the marks, the
   * placeholder, the reads -- so the invalidation cannot drift from what a visit
   * means; the one scope that is not a `show` is Live, whose held frame *is* the
   * screen (a `show` would draw it back rather than read again).
   *
   * **A scope that is not the tab in front is cleared and not read.** Nothing is
   * on screen to draw a mark into, the next visit reads the tab on entry anyway,
   * and a read for a tab nobody is on would spawn a child for a screen no one is
   * looking at -- the rule `.claude/rules/console.md` states for every section of
   * this console.
   */
  const invalidator = cacheInvalidator()
  invalidator.remember('live', {
    // What a launch makes stale is the **join**: the frame is the poll's, one
    // second wide, and the mode chip on a row is `runs ls`'s answer held for
    // `LIVE_RIGOR_TTL_MS`. Dropping the clock is what makes the launch's own run
    // read now rather than at the end of the TTL.
    clear: () => {
      live.rigorAt = 0
    },
    reread: () => {
      if (currentView !== 'live') return
      void pollLive()
      maybeRefreshRigor()
    },
  })
  // The other four scopes are registered by the tabs themselves, when they open:
  // what a tab holds and how to read it again are the tab's own facts -- the
  // visit's read, its cache and its sections -- and a table here would have to
  // reach into four screens for them.

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
  wiring.addProject = (): void => {
    showAddProject()
  }

  // The gauge and Halt fleet close the header row. The gauge's figures are the
  // global budget the console already reads: the Live frame's total on every
  // poll, and `budget show`'s global block when Spend or Projects reads it.
  const gauge = drawGauge(surface, hidesView(gate.compatibility, 'caps'))
  setGauge = gauge.set
  header.append(gauge.root)
  header.append(drawHalt(surface, roles))

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
      if (currentView === 'graph') {
        await frameGraph(frame)
        // **And the graph's own feed is polled here, beside it, and not awaited.**
        // The two are independent reads of the same tab -- the frame moves the
        // stages, the feed replaces the document -- so they are started together
        // (the owner's rule of 2026-10-31); the feed's first call waits for the
        // child's first document (the source's first-frame budget), and a tick
        // held for that long would stop drawing the frames the stages move with.
        // `graph.feedInFlight` is what keeps the next tick from starting a second
        // poll of the feed.
        void pollGraph()
      } else drawLive(frame)
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
      // The poll failed with nothing on screen that says why: the Retry is the
      // same read, made again by the operator's own press.
      if (currentView === 'live') refuse('live.watch', READ_UNREADABLE, () => void pollLive())
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
    /** paintLive puts this answer on the panel: the frame, or the refusal for a
     * read the console could not make. It is a closure of its own so that the
     * prewarm below follows **every** first paint -- a console whose feed refused
     * has painted its active tab just as one whose frame landed has, and the tabs
     * it is not showing are read for it the same way. */
    const paintLive = (): void => {
      const answer = replyData(reply)
      if (!answer.ok) {
        rail.setState('silent')
        refuse('live.watch', answer.sentence, () => void pollLive())
        return
      }
      const envelope = answer.data as Record<string, unknown>
      live.reply = reply
      const decoded = decodeSnapshot(envelope['document'], 'live.watch')
      if (!decoded.ok) {
        rail.setState('silent')
        refuse('live.watch', decoded.skew.because, () => void pollLive())
        return
      }
      const feed = feedHealthOf(envelope['receivedAt'], nowStamp())
      // The rail's state word is this read's own: the console is reading this
      // installation now (`running`), unless the frame it holds has gone quiet --
      // which is the second of the two conditions the design gives `silent`
      // (section 4.1). A read that was refused sets it above.
      rail.setState(feed.stale ? 'silent' : 'running')
      const model = buildLiveModel(decoded.value, history, feed, live.rigor, hourHistory)
      setGauge({ notionalUsd: model.total.notionalUsd, countedUsd: model.total.countedUsd, capUsd: model.total.dailyCapUsd })
      // The kill banner is the `kill` view. It is asked for through the gate --
      // the one door -- with an empty drawing, because what a reduced console
      // withholds here is not a screen but one panel of the Live screen: the
      // answer decides whether the model the screen is handed carries the kill
      // state at all.
      const mayDrawKill = gate.drawScreen('kill', () => undefined) === 'drawn'
      // The caps are the second view this screen withholds (`caps` beside `kill`
      // above), and the answer travels the same way the kill banner's does: as a
      // model the screen is handed, so a figure the gate withheld is one the
      // gauge cannot draw back later.
      const drawn = renderLive(factory, mayDrawKill ? model : killWithheld(model), theme, hidesView(gate.compatibility, 'caps'))
      live.drawn = drawn
      replace(drawn)
      wireRuns(drawn, model)
      placeLaunch()
    }
    paintLive()
    // The tab on screen has been painted -- with the frame this read read, or
    // with the refusal for the frame it could not -- and that is the moment the
    // console reads for the tabs it is *not* showing (PW1, D2906). It is here
    // rather than in `pollLive` because this is the paint: a poll that found
    // another view on screen painted nothing, and starts nothing. The queue runs
    // once per installation (`prewarmed`), which is what keeps the cadence's own
    // repeats from re-reading the other tabs.
    startPrewarm()
  }

  /**
   * wireRuns makes each drawn run row a door to the Run screen (plan 3.4c step
   * 2: "Run detail: reached from a run row").
   *
   * **The cell's `data-run-id` is the only thing read** (D1707). The drawn text
   * is the run's short ID -- four characters, extended over the rows of its
   * table when two of them collide (`live.ts`'s `putRunId`) -- and that is a
   * display: `fugaro` takes four or more characters as an *ending to resolve*,
   * so a door that handed one over would be asking about a different question
   * than the row shows, and one that could match two runs. The whole ID travels
   * in the attribute, written through the same `runId` class the cell's text
   * used to go through, so a run ID the sanitizer refused leaves the attribute
   * off and matches no run in the model -- a row whose ID cannot be drawn is a
   * row with nothing to ask about, rather than one that asks about whatever the
   * mark happens to be.
   *
   * **The review inbox's rows are the same door** (v2 plan Task V2.7.1):
   * `live-review-run-id` carries the same attribute, over the same short ID,
   * and `model.review` is where it is checked. A finished run is a run --
   * `fugaro diagnose RUN` reads it from the runs bucket precisely because its
   * registry entry is gone -- so a ready row that could not be opened would be
   * the one run row on this screen with nothing behind it.
   */
  const wireRuns = (drawn: Drawn, model: LiveModel): void => {
    const known = new Set<string>()
    for (const repo of model.repos) for (const row of repo.runs) known.add(row.run)
    for (const row of model.orphans) known.add(row.run)
    for (const row of model.review) known.add(row.run)
    const door = (rowClass: string, idClass: string): void => {
      for (const row of classed(drawn, rowClass)) {
        const id = classed(row, idClass)[0]?.getAttribute('data-run-id') ?? ''
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
          showRun(id)
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
    /** merging is the merge section's own busy state (IR4): whether the plain
     * `plan status --json --all` -- the same canvas, read with its merge state
     * -- is outstanding. It is `decorating`'s rule for the third read of this
     * tab: a flag of its own rather than the plan's, so the canvas is never held
     * up by the merge lookups and a second merge read never starts over the
     * first. **Nothing is marked on the panel for it**: the canvas is already
     * painted and true, and its state changes colour when the answer lands. */
    merging: boolean
    /** mergeQueued is the generation a canvas read asked the merge section for
     * while one was already outstanding, or `null`. It is `queued`'s rule
     * one read over: a canvas painted while the previous merge read was still
     * out is a canvas with no merge state coming, and the refresh that painted
     * it is a gesture the operator made -- so the read is kept rather than
     * dropped, and made the moment the one in flight settles. The generation is
     * kept rather than a flag for `queued`'s own reason: a console that has
     * moved on in the meantime has bumped it, and the kept read is then stale
     * and draws nothing. */
    mergeQueued: number | null
    /** mergeNotice is the sentence the merge read was refused with, kept apart
     * from `notice` because the two are different states of this tab: `notice`
     * is a **scope**'s refusal, drawn with the figures of the scope that
     * answered, and this one is about the merge state of the plan scope's own
     * canvas. A successful canvas read clears it (the merge read follows and
     * says its own sentence, or none), and a successful merge read clears it
     * too -- while the scope's sentence is not this read's to take down. */
    mergeNotice: string | null
    /** watching is whether the console has a graph feed it must stop: it is set
     * at the subscribe and cleared when the stop is sent, so a tab that is left
     * does not send one stop per tick -- and, more to the point, so a feed that
     * is wanted is never stopped by a second look at the same conditions
     * (`startGraphWatch` / `stopGraphWatch`). */
    watching: boolean
    /** feedInFlight is whether a poll of the graph feed is outstanding. It is
     * `decorating`'s rule for the feed: the tick must not start a second poll
     * while the first is waiting for its document. */
    feedInFlight: boolean
    /** scope is the scope the operator is looking at (CG10). It is the entry's
     * own memory of a **choice** and not the screen's drawing state: the screen
     * is put on a scope by the document it is handed (`load` and `loadScope`),
     * and this is what a cadence, a re-entry or a refusal consults to know which
     * read to make. */
    scope: GraphScope
    /** chosen is whether the operator has picked a scope themselves. The console
     * opens on the plan scope (the read it has always made) and **falls back to
     * the coordinator's when the plan scope refuses for want of a checkout**
     * (H9) -- and this flag is what keeps that fallback from firing again once a
     * person has chosen: an operator who asks for the plan scope of an
     * installation that records no checkout is asking for the card, not for the
     * scope the console would have opened on. */
    chosen: boolean
    /** notice is the sentence a chosen scope was refused with, drawn inside the
     * drawing under the control that chose it (T3), or `null` when the scope in
     * hand was read. */
    notice: string | null
    /** fellBack is whether the console opened on the coordinator scope because
     * the plan scope refused for want of a checkout (H9). It is what tells the
     * refusal of that fallback apart from a refusal of a scope a person chose:
     * the plan scope's card is the drawing for the first (the installation has
     * one thing to do about it, and the read that was refused was the console's
     * own idea), and the sentence naming `graph.read` is the answer to the
     * second, where the operator asked for that scope. A scope the operator
     * chooses clears it (`chooseScope`), because from then on the read is
     * theirs. */
    fellBack: boolean
  } = {
    screen: null,
    readAt: 0,
    inFlight: false,
    queued: null,
    queuedToken: null,
    runs: [],
    decorating: false,
    merging: false,
    mergeQueued: null,
    mergeNotice: null,
    watching: false,
    feedInFlight: false,
    scope: GRAPH_SCOPE_PLAN,
    chosen: false,
    notice: null,
    fellBack: false,
  }

  /** GraphHeld is what the Graph's session cache keeps per installation: the
   * plan document, the run list the last read answered, and whether that
   * document carries its merge state. It is memory and nothing else, and
   * `forgetLive` empties it.
   *
   * **`merged` is what makes a later read not a first paint** (IR4 of the
   * console-fixes plan): a plan already read with its merges is a canvas whose
   * merged nodes are drawn merged, and reading that canvas again without them
   * would put every one of them back to `done` with the `merge unknown` mark --
   * for the merge section to draw them merged again a moment later. The cache is
   * the one place that knows which document a session has for an installation,
   * so the fact is kept beside the document it is about. */
  interface GraphHeld {
    readonly document: PlanStatusView
    readonly runs: readonly string[]
    readonly merged: boolean
  }

  const graphViews = resultCache<GraphHeld>(CACHE_LIMIT)

  /** autoChosen is the installations the Graph's card has already chosen the one
   * checkout of. It is per installation and not per view, because the write it
   * guards is: a card drawn again for the same installation -- a redraw, a
   * cadence read, a visit after another tab -- must not send the same directory a
   * second time, and an installation the operator comes back to is one whose
   * list may have changed under it. */
  const autoChosen = new Set<string>()

  /** graphRead is the Graph's section read lifecycle: the mark the operator's
   * own controls put up in the frame they were pressed in, the sequence token
   * that keeps the latest press's answer, and the fifteen-second line. */
  const graphRead = sectionRead({ factory, theme })

  /** leaveGraph ends the section's busy state when the operator moves to another
   * tab: nothing is in flight for a screen nobody is looking at. The graph's
   * **feed** is not ended here, because this door is for the busy mark alone: the
   * child's lifetime is `syncGraphWatch`'s, which the switch already ran. */
  leaveGraph = () => {
    graphRead.end()
  }

  /**
   * startGraphWatch and stopGraphWatch are the two acts the feed's conditions
   * decide between, and `watching` is what makes each of them once: a feed that
   * is wanted is not subscribed twice, and the stop is sent for a child the
   * console holds and never once per tick.
   *
   * **The poll is what starts the child**: the main process's `graph.watch`
   * subscribes the feed and answers the document it holds, so the door that wants
   * it makes one call now rather than waiting for the next tick of the interval
   * -- and the first document is drawn rather than thrown away.
   *
   * **The stop names the scope the subscription was made with**, because that
   * argv is the feed's key in the main process: a stop with a different `source`
   * would name a feed that is not running and leave this one's child alive until
   * quit. The reply is not awaited and not drawn -- the state the caller asked for
   * is "no child", and `stopGraphWatch` never refuses.
   */
  const startGraphWatch = (): void => {
    if (graph.watching) return
    graph.watching = true
    void pollGraph()
  }

  const stopGraphWatch = (): void => {
    if (!graph.watching) return
    graph.watching = false
    void bridge.graphWatch({ source: GRAPH_SCOPE_ALL, visible: false })
  }

  /**
   * syncGraphWatch decides whether the graph feed is wanted, and it is the one
   * place that decides it: the Graph tab is the view on screen, the window is
   * visible, and the scope in hand is the one the feed's document is about. **The
   * tab is H6's condition**, and the cost it exists for is the CLI's own
   * subscription on the project's scope subtree: a console whose operator is on
   * another tab, or whose window is behind another window, must not be holding
   * one. `close()` on the source would stop it anyway; what this buys is that it
   * is never held in the first place.
   *
   * **The scope is the second one, and it is the document's own.** `graph --watch
   * --json --source all` prints the union scope's document and there is no
   * streaming form of the plan scope's -- a plan is `plan.status`, and the plan
   * scope is the one the Graph opens on whenever a checkout is recorded -- so a
   * console showing that scope would hold a child whose every document it throws
   * away. That is the subscription H6's cost is about, held on the common path:
   * it is not started at all, and what starts it is the scope becoming `all`
   * (`chooseScope`, `readScope`).
   *
   * It is called from `syncPolling`, so the tab switch (`markCurrent`), the
   * visibility change and the first read all go through it and cannot disagree --
   * and from the doors that settle the scope in hand, because a scope is not a
   * tab: a choice and the fallback of H9 both settle one while the tab stays
   * where it is.
   */
  const syncGraphWatch = (): void => {
    if (currentView === 'graph' && scopeVisible() && graph.scope === GRAPH_SCOPE_ALL) {
      startGraphWatch()
      return
    }
    stopGraphWatch()
  }

  /**
   * pollGraph draws what the graph feed is holding.
   *
   * **It is a poll of a child that is already running, and never a read of its
   * own**: `graph.watch` answers the feed's `current()`, so the call that starts a
   * child waits for its first document and every later one answers at once. The
   * screen's `loadScope` is what lays the document out, and a document that
   * changed nothing lays out to the same picture.
   *
   * **At most one call in flight**, the rule every other read here keeps: the flag
   * is set before the await and cleared in a `finally`, so a slow first document
   * cannot be overtaken by the next tick. And a document that lands after the
   * operator has moved on is dropped, twice over: this view is not the one on
   * screen, or the scope in hand is not this feed's (`all`), which is the plan
   * scope's drawing and not this document's to replace (CG10).
   *
   * **The scope is the feed's own condition and this is the door it is kept at**,
   * because the poll is what starts the child: the tick asks this every second
   * while the Graph is on screen, so a scope that moved without one of its own
   * doors -- a scope read that refused, whose fallback is the plan scope -- is
   * answered here too, and a child the console no longer has a use for is given up
   * rather than kept for the rest of the visit. Nothing is subscribed for a scope
   * the document does not belong to: it would be a child printing a document that
   * is thrown away, which is the subscription H6's cost is about.
   *
   * **A refusal is not a drawing.** A child that died, or one the ceiling
   * evicted, must leave the figures the operator is reading exactly where they
   * are: `graph.read` is the read that answers a screen with a sentence about this
   * scope (`readScope`, `refuseScope`), and it is still made on its own cadence
   * (`maybeRefreshGraph`), so a feed that is not there costs the console the
   * stream and not the picture. This is `frameGraph`'s own rule for the live
   * frame, for the same reason.
   */
  const pollGraph = async (): Promise<void> => {
    if (graph.feedInFlight) return
    if (graph.scope !== GRAPH_SCOPE_ALL) {
      stopGraphWatch()
      return
    }
    // The poll subscribes, so it is the poll that records the child as held: a
    // feed this call started is one a tab that is left still gives up.
    graph.watching = true
    graph.feedInFlight = true
    try {
      const reply = await bridge.graphWatch({ source: GRAPH_SCOPE_ALL })
      if (currentView !== 'graph' || graph.scope !== GRAPH_SCOPE_ALL) return
      const answer = replyData(reply)
      if (!answer.ok) return
      const envelope = answer.data as Record<string, unknown>
      const decoded = decodeGraphDocument(envelope['document'], 'graph.watch')
      if (!decoded.ok) return
      const drawn = graphScreenNow().loadScope(decoded.value, null)
      if (drawn === null) return
      // The refusal a scope read left on screen goes with the document that
      // answers it: this document **is** the coordinator scope's, so a sentence
      // about a read of it is no longer true. The plan scope's notice is not
      // this call's to clear, and the guard above has already turned it away.
      graph.notice = null
      redraw(drawn, stopNow())
    } catch {
      // `readBridgeFrom` answers `null` rather than rejecting, so this is a throw
      // this file did not expect; the picture already on screen is the answer.
    } finally {
      graph.feedInFlight = false
    }
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

  /**
   * moveFlowNow is the move's flow, built on first use.
   *
   * **It is the port that makes the fence a fence.** `roadmap.move` is the one
   * door the flow knocks on, and what this file hands it is the preload surface
   * and `replyData` -- so a missing bridge, a thrown refusal and an answer this
   * build cannot read are one shape on the sheet (`MoveRead`), and the flow
   * itself never reaches a source, a read or a child.
   *
   * **One flow lives as long as one installation's screen.** It holds the request
   * a confirmation is about and the last move the record applied -- which is what
   * an undo inverts -- and both are facts about the checkout the console is
   * driving; `forgetLive` drops it with the screen, so a way back is never
   * offered across two installations.
   */
  let moveFlow: MoveFlow | null = null
  /**
   * **The two gestures in flight live out here, with the flow, and not inside
   * `wireGraph`.** Every redraw wires a freshly drawn tree, so a variable
   * declared in there is a new one per drawing: the key the row's button opened
   * the sheet for would be gone by the time the select's own change listener --
   * a listener of the drawing that very redraw made -- read it, and the sheet
   * would answer nothing whatever a person chose.
   */
  let dragging: string | null = null
  let choosing: string | null = null
  /**
   * **moveTurn is which gesture the sheet on screen is the answer to**, and it
   * lives out here with the two above for the same reason.
   *
   * A preview is a child, so its answer lands after whatever the operator did
   * next: a second drop, another choice from the list, Escape, or the
   * confirmation of the sheet already up. A drawing made from an answer to a
   * gesture that is over is the defect this number exists for -- a sheet showing
   * one release while the flow holds the request for another is a confirmation
   * that runs what is not drawn -- so every gesture takes a new turn, and a
   * continuation draws only while its own turn is still the one in hand.
   */
  let moveTurn = 0
  const takeMoveTurn = (): number => {
    moveTurn += 1
    return moveTurn
  }
  /**
   * **readByMove is the move whose own flow is reading the plan again already**
   * (R2). A confirmed move is a write, and the scope it names is this tab's; but
   * the flow that made it goes on to read the scope itself, because the sheet
   * over the drawing has to be answered in the light of the document the move
   * left. So the invalidation clears the cache and leaves the read alone -- one
   * write, one read -- and this number is how the scope's own `reread` knows the
   * read it would make is already out.
   */
  let readByMove: number | null = null
  const moveFlowNow = (): MoveFlow => {
    moveFlow ??= new MoveFlow({
      move: async (args) => {
        const { node, toRelease, confirmed } = args
        const answer = replyData(await moveBridge.moveRoadmapTask({ node, toRelease, confirmed }))
        if (!answer.ok) return { ok: false, sentence: answer.sentence }
        const read = moveAnswerOf(answer.data)
        return read === null ? { ok: false, sentence: MOVE_UNREADABLE_WORD } : { ok: true, answer: read }
      },
    })
    return moveFlow
  }

  /** graphScreenNow is the screen, built on first use. The worker is started
   * here and nowhere else, and `forgetLive` disposes of it.
   *
   * The gate is asked here, as it is for the Live screen's caps: the plan's
   * estimate carries its cost (S4), and a reduced console draws the ready-by
   * range with the cost withheld rather than a dollar figure it cannot vouch
   * for. The answer is handed in at construction because the screen holds no
   * compatibility of its own -- `gate.compatibility` is fixed for the session,
   * so a screen built once is asked once. */
  const graphScreenNow = (): GraphScreen => {
    if (graph.screen === null) graph.screen = graphScreen(factory, theme, hidesView(gate.compatibility, 'spend'))
    return graph.screen
  }

  /**
   * canvasRead is the argv one read of the canvas is made with, and whether the
   * merge section follows it (IR4 of the console-fixes plan, D3217 as amended).
   *
   * **The canvas is read without its merge state only when this session has
   * none**, which is the first paint and nothing else: `--all --no-merges` above
   * `PLAN_STATUS_NO_MERGES_CLI_FLOOR` while `mergesInHand` is false, and the
   * plain `--all` read otherwise.
   *
   * **A later read is not a first paint, and reading it as one is churn.** The
   * picture on screen already carries merge state, so a cadence read of
   * `--no-merges` would put every merged node back to `done` with the `merge
   * unknown` mark -- a colour and a size change, a structure hash that has
   * moved, a layout rebuilt for it -- and the merge section would then draw them
   * merged again a moment later, twice a minute. The plain read keeps the
   * drawing as it is and costs the forge lookups on a read the operator is not
   * waiting inside: the previous canvas stays up until the answer lands.
   *
   * **`mergesFollow` is what makes the two reads one.** Below the floor there is
   * no flag to pass and the single read is the merge read, so nothing follows
   * it; and above the floor a read that already carries merge state needs no
   * section to decorate it. The merge section exists for one read and one only:
   * the canvas read that left the merge state out.
   *
   * **The floor is asked before the read goes out**, the way `modelsTooOld` and
   * the Coordinator's map floor are: `plan status --no-merges` is newer than the
   * oldest CLI this console supports, and an older binary answers the word with
   * `unknown flag: --no-merges`, which fails the whole read -- a failure the
   * operator can do nothing about. Below the floor the console makes the one
   * read it has always made, and the canvas is the same graph: `--all` lists the
   * same tasks either way, and what `--no-merges` changes is the merge state of
   * a task whose run recorded a ready pull request.
   */
  const canvasRead = (): { readonly args: Readonly<Record<string, unknown>>; readonly mergesFollow: boolean } => {
    const alone = mergesWanted() && !mergesInHand()
    return { args: alone ? { all: true, noMerges: true } : { all: true }, mergesFollow: alone }
  }

  /** mergesWanted is whether this installation's CLI may be asked for the plan
   * **without** merge state -- the one question the canvas's argv and the merge
   * section's existence are both decided by, asked through one function so the
   * two can never disagree. */
  const mergesWanted = (): boolean =>
    planStatusNoMergesSupported(gate.compatibility.cliVersion ?? '')

  /** mergesInHand is whether the canvas this session holds for this installation
   * already carries its merge state: the document the session cache has, and a
   * drawing made from it. **Both halves are asked**, because the read they
   * decide is about a picture: a document cached for an installation whose
   * canvas a refusal has since taken down is a read that would spawn the
   * lookups on a first paint, and a document is not in hand until the screen
   * holds it. */
  const mergesInHand = (): boolean =>
    graphViews.get(graphKey())?.value.merged === true && graph.screen?.planInHand != null

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
    if (token !== null) markGraph(token, GRAPH_BUSY)
    // The argv and whether the merge section follows it are decided in one
    // breath (`canvasRead`), and the decision is made **before** the await: a
    // merge read that lands while this one is out puts merge state in hand, and
    // what this read asked for is what it answers with.
    const read = canvasRead()
    try {
      const plan = documentOf(await bridge.planStatus(read.args))
      // The section's token is settled before the refusal is drawn, for the
      // reason every other screen gives: the mark must come down for the
      // operator's own read, and a read an earlier press started must not take
      // down the mark a later one put up. **It is settled before the fallback
      // below as well**: the fallback is a different read, with a mark of its
      // own, and the plan's mark is not the coordinator's to wear.
      if (token !== null && !graphRead.settle(token)) return
      // Before the refusal, not after it: a refusal is a screen of its own, and
      // one drawn for a generation that has passed is the operation cell
      // `plan.status` under a tab that asked for another read.
      if (stale(mine)) return
      if (!plan.ok) {
        // **H9: the plan scope is the one scope that needs a checkout, and the
        // console does not open on a card for an installation that has one
        // scope it can read.** The sentence is the CLI's own precondition for
        // this one read, and the coordinator scope needs no checkout at all, so
        // the console opens there instead -- once, and only for the scope it
        // chose on its own: an operator who has picked a scope is asking for
        // that scope's answer (the card, with its own door back), and a second
        // fallback would be the console arguing with the person using it.
        //
        // **And not for a card that records the checkout by itself** (H9 too,
        // and the plan scope's own requirement): see `cardChoosesByItself`. The
        // fallback replaces a card the operator had nothing to do about; it must
        // not take the console's own choice of an installation's single checkout
        // away, which is the write that makes this read succeed at all.
        if (plan.sentence === NO_CHECKOUT_SENTENCE && !graph.chosen && !cardChoosesByItself()) {
          graph.fellBack = true
          await readScope(mine, null)
          return
        }
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
      // The notice is the sentence a **chosen** scope was refused with, and it
      // is drawn into every reading of the scope it was refused for: it says
      // which read the console cannot make (`graph.read`), and the plan scope's
      // own figures are what it is drawn over.
      //
      // **A merge refusal goes with the canvas that replaces it**: the sentence
      // was about the merge state of the document this read has just replaced,
      // and the merge read below says its own.
      graph.mergeNotice = null
      const drawn = await graphScreenNow().load(decoded.value, graph.runs, graph.notice)
      if (stale(mine) || drawn === null) return
      // A read names no gesture: the keyboard stays where it was, and a reader
      // who is not in the graph is not pulled into it.
      graph.scope = GRAPH_SCOPE_PLAN
      graph.fellBack = false
      redraw(drawn, stopNow())
      graphViews.put(graphKey(), { document: decoded.value, runs: graph.runs, merged: !read.mergesFollow }, Date.now())
      void decorateRuns(mine, decoded.value)
      // **And the merge state follows the canvas** (IR4, D3217 as amended) --
      // for the one read that left it out. It is a separate read of the same
      // command without `--no-merges`, started **after** the canvas has painted
      // rather than in parallel with it: it decorates a canvas, so it has
      // nothing to draw from until the canvas exists, and its whole point is
      // that the canvas did not wait for it. Below the floor, and on a read that
      // already carried merge state, there is nothing to follow: the document on
      // screen is the merge read's own.
      if (read.mergesFollow) void decorateMerges(mine)
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

  /**
   * readScope makes one `graph.read` -- the coordinator's view of the
   * installation, which needs no checkout at all (H9) -- and draws it.
   *
   * **It is a read of the same section and not a second screen.** The Graph is
   * one tab with two scopes, so a scope read takes the section's own token and
   * puts the section's own mark up: a scope change is answered in the frame it
   * happened in, over the figures the last read drew (the owner's rule of
   * 2026-10-31), and an answer whose token is no longer the newest is dropped --
   * which is what keeps a slow coordinator read from landing over the plan scope
   * an operator changed their mind into.
   *
   * **It does not take the plan's in-flight flag.** The two reads are two
   * documents and neither waits for the other: a scope change must be able to
   * start while a plan read is running (and the other way round), or the
   * operator's own choice would queue behind a read they did not ask for. What
   * orders them is the token, and what the flag keeps ordering is the plan's own
   * reads.
   *
   * There is **no cache** for this scope. The plan's cache exists because a plan
   * is a document of a checkout that changes on the scale of a plan; the
   * coordinator's view is the installation's whole state -- which is exactly the
   * thing worth re-reading rather than serving from memory.
   */
  const readScope = async (mine: number, token: number | null): Promise<void> => {
    if (token !== null) markGraph(token, GRAPH_SCOPE_BUSY)
    // The cadence is kept off this read the same way it is kept off the plan's:
    // the stamp is taken before the await, so a read that is refused is not
    // retried on the next tick of the one-second poll.
    graph.readAt = Date.now()
    try {
      const read = documentOf(await bridge.graphRead({ source: GRAPH_SCOPE_ALL }))
      if (token !== null && !graphRead.settle(token)) return
      if (stale(mine)) return
      if (!read.ok) {
        refuseScope(read.sentence)
        return
      }
      const decoded = decodeGraphDocument(read.data, 'graph.read')
      if (!decoded.ok) {
        refuseScope(decoded.skew.because)
        return
      }
      const drawn = graphScreenNow().loadScope(decoded.value, null)
      if (stale(mine) || drawn === null) return
      graph.scope = GRAPH_SCOPE_ALL
      graph.notice = null
      graph.fellBack = false
      redraw(drawn, stopNow())
      // **The scope is settled, so the feed is asked for in this turn.** This is
      // the read the console's own fallback makes for an installation that
      // records no checkout (H9), and it is the one door into this scope that is
      // nobody's press: without this call the child would wait for the next tick
      // of the interval, and the first document -- the drawing this scope opens
      // with -- would be the one thrown away (`syncGraphWatch`).
      syncGraphWatch()
    } catch {
      // As `readGraph`: `readBridgeFrom` answers `null` rather than rejecting, so
      // this is a throw this file did not expect, and the generation is checked
      // so a refusal is never drawn for a console that has moved on.
      if (!stale(mine)) refuseScope(READ_UNREADABLE)
    }
  }

  /**
   * refuseScope is what a refused scope read is answered with, and it is T3's
   * own ruling: **the plan scope is kept and the sentence is drawn in it**.
   *
   * A refusal of this read is not a failure of the console or of the CLI -- the
   * core stub simply has no `graph` command -- and the scope it was refused for
   * is one of two. A refusal screen would take the scope control away with it,
   * leaving an operator who cannot choose anything else; so the drawing the
   * console already has stays, under the sentence naming the read it could not
   * make, and the control stays with it. Only a console with **no drawing at
   * all** is answered with the refusal screen, because then there is nothing to
   * keep and a reader needs to be told what happened.
   *
   * The scope the entry remembers is the one the screen is still drawn in, and
   * never the one that was refused: the two must agree, or the next cadence
   * would read a document the panel is not showing.
   *
   * **A refusal of the console's own fallback is the card, and not a sentence.**
   * When the plan scope refused for want of a checkout and the scope the console
   * opened instead refuses as well, there is no drawing to keep and the state the
   * operator has to do something about is the one the card names: a checkpoint
   * the Graph needs and the installation has not chosen. The read that was
   * refused was the console's own idea, nobody asked for its sentence, and the
   * card's own door is what makes that scope reachable -- pressing it is a
   * person's choice, and *that* refusal is drawn in the frame it was made in.
   */
  const refuseScope = (sentence: string): void => {
    graph.notice = sentence
    const screen = graph.screen
    const kept = screen === null ? null : screen.noticed(sentence)
    if (kept !== null) {
      graph.scope = screen?.current.scope ?? GRAPH_SCOPE_PLAN
      redraw(kept, stopNow())
      return
    }
    graph.scope = GRAPH_SCOPE_PLAN
    if (graph.fellBack) {
      // The sentence goes with the read it was about: the card draws the CLI's
      // own account of the **plan** refusal behind its disclosure, and a sentence
      // about a read this console made on its own would be drawn over every
      // later plan read that succeeded.
      graph.notice = null
      refuseGraph(NO_CHECKOUT_SENTENCE)
      return
    }
    graphTree = null
    // The Graph's whole view is the read that refused, so its Retry is the tab's
    // own door rather than a section's: `showGraph` reads the scope on screen
    // again, which is what the operator's press means here.
    refuse('graph.read', sentence, () => void showGraph())
  }

  /**
   * chooseScope is the operator's own choice of a scope, from the control in the
   * toolbar and from the no-checkout card's door back to the coordinator alike.
   *
   * **A choice is a read, in the frame the control was used in.** The token is
   * taken here, before the read is started, because that is what puts the mark
   * up in this frame and what keeps two quick choices from drawing the earlier
   * one's answer; `chosen` is what tells the console the fallback of H9 is no
   * longer its business -- from here on, the scope a reader asks for is the
   * scope they get, refusals included.
   *
   * **The fallback's own flag goes with the choice.** `fellBack` means "this
   * console opened on the coordinator scope by itself, so a coordinator read that
   * refuses is the plan scope's card"; once a reader has chosen, the read is
   * theirs and a refusal is the answer to it. Left set, the card's own door back
   * to the coordinator -- the one control an installation with no checkout has --
   * would take the sentence out of the refusal and draw the same card again: a
   * door that answers with the screen it is on.
   */
  const chooseScope = (scope: GraphScope): void => {
    graph.chosen = true
    graph.fellBack = false
    graph.scope = scope
    graph.notice = null
    // **The feed goes with the scope, in this frame.** The choice of `all` is
    // what starts the child and any other choice is what gives it up, and the
    // operator's own door is not a second late: the tick would answer the same
    // question on its next turn, which is a whole second of a subscription to a
    // scope nobody is looking at (`syncGraphWatch`).
    syncGraphWatch()
    const token = graphRead.start()
    if (scope === GRAPH_SCOPE_ALL) void readScope(claim(), token)
    else void readGraph(claim(), token)
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
  const markGraph = (token: number, label: string): void => {
    const slot = graphSlot()
    const tree = graphTree
    if (slot === null || tree === null) return
    graphRead.mark(token, tree, slot, label)
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
      const screen = graphScreenNow()
      const drawn = screen.decorate(runs)
      if (stale(mine) || drawn === null) return
      graph.runs = runs
      redraw(drawn, stopNow())
      // The document cached is the one the drawing really holds, which is not
      // necessarily the one this read was handed: the merge section may have
      // replaced it while this read was out, and caching the canvas document
      // back over the merge-inclusive one would make the next visit's first
      // paint the older of the two. **The merge fact is carried forward with it**
      // for the same reason: it is about the document in hand, and the entry is
      // the only place that knows which read put that document there.
      graphViews.put(
        graphKey(),
        { document: screen.planInHand ?? document, runs, merged: graphViews.get(graphKey())?.value.merged === true },
        Date.now(),
      )
    } catch {
      // As above: a bridge that threw where it should not have leaves the
      // doors as they were, which is the same drawing a refused read makes.
    } finally {
      graph.decorating = false
    }
  }

  /**
   * decorateMerges reads the plan **again, with its merge state** -- the plain
   * `plan status --json --all`, which is the same canvas and up to thirty-two
   * forge lookups more -- and attaches what it says to the picture on screen
   * (IR4 of the console-fixes plan, D3217 as amended).
   *
   * **It is the canvas's own section read the same way `decorateRuns` is the
   * canvas's own runs read**: a read of its own, with its own busy state
   * (`graph.merging`) and its own refusal, started after the canvas has painted
   * and awaited by nothing. The canvas is drawn from the read that spawns no
   * `gh` (`graphCanvasArgs`), so the first paint costs no forge lookup at all;
   * this read is what turns a merged task's `done`/`merge unknown` into the
   * merged state the plain read knows, and a node changes colour in the drawing
   * the operator is already reading rather than arriving late as a second
   * picture.
   *
   * **Its refusal is a sentence in the drawing and never a refusal screen.** The
   * canvas on screen is true -- it is the plan, read without merge state -- and
   * taking it down because the merge lookups failed would throw away a graph the
   * operator can read for a fact about a colour. `noticed` keeps the figures and
   * writes the sentence into them, which is the same door a refused scope uses,
   * and `graph.mergeNotice` is what keeps the sentence across a redraw (a tab, a
   * selection, a zoom) until a read answers. The document on screen is
   * unchanged: the read that did not answer is not a read that said the canvas
   * is wrong.
   *
   * The source holds its own cadence per argv (`PLAN_STATUS_MIN_INTERVAL_MS`),
   * so a canvas read and this one within the same minute are two documents from
   * two children and a repeat of either is served rather than respawned.
   */
  const decorateMerges = async (mine: number): Promise<void> => {
    if (graph.merging) {
      // A canvas painted under a merge read that is still out is a canvas whose
      // merge state is unknown with nothing coming for it, and it is a state a
      // gesture reaches: Refresh paints the canvas again while the lookups are
      // still running. The claim is kept rather than the read, as
      // `readGraph` keeps one -- see `mergeQueued`.
      graph.mergeQueued = mine
      return
    }
    graph.merging = true
    try {
      const plan = documentOf(await bridge.planStatus({ all: true }))
      // The generation is checked after the read, on `decorateRuns`'s terms: a
      // decoration nobody is waiting for must not replace a view the operator
      // has moved to.
      if (stale(mine)) return
      if (!plan.ok) {
        refuseMerges(plan.sentence)
        return
      }
      const decoded = decodePlanStatus(plan.data, 'plan.status')
      if (!decoded.ok) {
        refuseMerges(decoded.skew.because)
        return
      }
      // **The scope's own sentence goes back in with the document**: a canvas
      // served from memory drew the sentence the last merge read was refused
      // with, and this read is the one that answers it -- leaving it up over a
      // document that carries the merge state would be a sentence about a read
      // that has since been made.
      const drawn = await graphScreenNow().decorateMerges(decoded.value, graph.notice)
      if (stale(mine) || drawn === null) return
      graph.mergeNotice = null
      redraw(drawn, stopNow())
      // The cache holds the **merge-inclusive** document from here on, which is
      // a strict gain for the next visit's first paint: the same canvas with more
      // of it known -- and `merged`, which is what tells that visit the merge
      // state is already in hand and no first paint without it is due.
      graphViews.put(graphKey(), { document: decoded.value, runs: graph.runs, merged: true }, Date.now())
    } catch {
      // As `decorateRuns`: a bridge that threw where it should not have leaves
      // the canvas as it was.
    } finally {
      graph.merging = false
      // The claim a canvas read made while this one was out: it is made now, in
      // the drawing the operator's own gesture left, and a generation that has
      // passed since is dropped by the read's own check as every other read's is.
      const queued = graph.mergeQueued
      graph.mergeQueued = null
      if (queued !== null) void decorateMerges(queued)
    }
  }

  /** refuseMerges is the merge section's refusal: the sentence, in the drawing,
   * over the canvas that answered. It is drawn only where there is a drawing to
   * put it in -- a console with none is showing a refusal or a card, and those
   * already say what is wrong. */
  const refuseMerges = (sentence: string): void => {
    graph.mergeNotice = sentence
    const drawn = graph.screen?.noticed(sentence) ?? null
    if (drawn !== null) redraw(drawn, stopNow())
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
    refuse('plan.status', sentence, () => void showGraph())
  }

  /** openRow is the rail's open installation, which is the one the Graph reads
   * for: the card's state is that row's. */
  const openRow = (): RailRow | undefined => railNow.find((row) => row.open)

  /**
   * cardOptions is what the no-checkout card is drawn from, and it is built in
   * one place because the fallback above asks the same question the drawing
   * answers: an installation's open row, the list this console has read for it,
   * and whether the card may choose its one checkout by itself.
   */
  const cardOptions = (): NoCheckoutOptions => {
    const open = openRow()
    return {
      choices: open?.checkoutChoices ?? [],
      listed: deps.railListed && open !== undefined,
      // A project this card has already chosen for is not chosen for again: the
      // write is the answer to the card, and a plan that still refuses after it
      // draws the menu rather than a sentence about a read that is not happening.
      auto: open !== undefined && !autoChosen.has(open.project),
    }
  }

  /**
   * cardChoosesByItself is whether that card would record the installation's one
   * checkout on its own, and it is what keeps the H9 fallback from undoing the
   * plan scope's own way out.
   *
   * **The fallback answers a card the operator could only be sent somewhere
   * else from** -- the CLI listed no checkout, the list was never read, or there
   * are several to choose between. **It does not answer the console's own
   * choice**: an installation with exactly one usable checkout gets the card
   * that records it and reads the plan from it, in the frame it always did, and
   * the coordinator scope is reached by the operator's choice of scope like any
   * other. Opening on the coordinator scope there would leave the installation's
   * checkout unchosen and the plan scope unreadable until somebody chose it
   * back: the state this card exists to end.
   *
   * The question is the card's own (`noCheckoutActionKind`, over the options the
   * drawing is given), so the two cannot disagree about which state this is.
   */
  const cardChoosesByItself = (): boolean => noCheckoutActionKind(cardOptions()) === 'auto'

  /**
   * drawNoCheckout draws the Graph's no-checkout card and wires its action.
   *
   * The action is the chooser this console already ships -- the issue view's own
   * menu, one entry per repository the CLI re-validated and enrolled -- and it
   * sends the directory it has selected through `installation.checkout`, the
   * operation the picker window's own control uses. There is no new channel, no
   * new bridge method and no new argument: a card in the panel is not a reason
   * to widen the IPC allow-list.
   *
   * The card is drawn for an installation the CLI listed a checkout for, says
   * what records one when the CLI listed none, and keeps the picker's door for a
   * console that never read the list at all -- because "no checkout is recorded"
   * is not a claim it can make.
   */
  const drawNoCheckout = (sentence: string): void => {
    const open = openRow()
    const drawn = renderNoCheckout(factory, sentence, theme, cardOptions())
    replace(drawn.root)
    // **The card's own door back to the coordinator scope** (CG10). The card is
    // drawn *instead of* the Graph, so the scope control is gone with it, and the
    // plan scope is a choice somebody made: this is the one way out for an
    // installation that records no checkout, and it is wired for every state of
    // the card -- including the ones whose action is a sentence rather than a
    // control.
    surface.nodeOf(drawn.coordinator).addEventListener('click', () => {
      chooseScope(GRAPH_SCOPE_ALL)
    })
    const action = drawn.action
    if (action.kind === 'picker') {
      surface.nodeOf(action.button).addEventListener('click', () => {
        void railBridge.openInstallations({})
      })
      return
    }
    if (open === undefined) return
    if (action.kind === 'auto') {
      autoChosen.add(open.project)
      useCheckout(open.project, action.choice.path)
      return
    }
    if (action.kind === 'choose') {
      surface.nodeOf(action.button).addEventListener('click', () => {
        useCheckout(open.project, valueOf(surface, action.select))
      })
    }
  }

  /** maybeRefreshGraph is the one place the plan's cadence is applied, so
   * entering the view, the poll and the operator's refresh cannot disagree about
   * when a read is due. */
  const maybeRefreshGraph = (): void => {
    if (graph.inFlight) return
    if (Date.now() - graph.readAt < GRAPH_REFRESH_MS) return
    // **No token**: the cadence is nobody's press, and a mark put up for it would
    // flicker over a picture the operator is reading.
    // **The scope in hand is the scope the cadence keeps fresh** (CG10): a
    // background read of the plan under a coordinator drawing would be a read
    // nobody can see, made at the cost of the one nobody asked for.
    if (graph.scope === GRAPH_SCOPE_ALL) void readScope(claim(), null)
    else void readGraph(claim(), null)
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
    // R2: the scope a move names, registered by the visit that owns it. What it
    // holds is the plan this session read, with the run list it was decorated
    // with -- the document a move changes -- and the read is this tab's own, for
    // the scope the operator is in.
    invalidator.remember('graph', {
      clear: () => graphViews.clear(),
      reread: () => {
        // The marker is read and cleared in the same breath: it stands for one
        // read the flow has already started, and for that one only.
        const byMove = readByMove
        readByMove = null
        if (stale(mine) || byMove === moveTurn) return
        if (graph.scope === GRAPH_SCOPE_ALL) void readScope(claim(), graphRead.start())
        else void readGraph(claim(), graphRead.start())
      },
    })
    // The cache holds the **plan** scope's document, so it is drawn when the plan
    // scope is the one in hand: a console that comes back to the Graph on the
    // coordinator scope draws the coordinator reading it already has (below) and
    // reads that scope again, rather than replacing the operator's choice with
    // the plan it happened to cache.
    if (held !== null && graph.scope === GRAPH_SCOPE_PLAN) {
      // **The cached view is drawn before anything is awaited**, which is the
      // whole point of the cache: the plan this session already read, with the
      // run list it was decorated with, laid out again by the engine's own cache
      // without a message to the worker. It is stamped `as of HH:MM` with the
      // door to read it again, and a view older than `CACHE_FRESH_MS` is read
      // again at once **as well** -- never served silently.
      const drawn = await graphScreenNow().load(held.value.document, held.value.runs, graph.mergeNotice ?? graph.notice)
      if (stale(mine) || drawn === null) return
      graph.runs = held.value.runs
      redraw(drawn, stopNow())
      stampGraph(held.at)
      if (Date.now() - held.at >= CACHE_FRESH_MS) void readGraph(mine, graphRead.start())
      // **A canvas served from memory is still a canvas the merge section
      // decorates.** The cached document is the one a read put there, and a
      // merge read that was refused -- or one that had not landed when the
      // operator left -- leaves it without merge state; a fresh view is not
      // re-read, so without this the colour a merged node wears would be the
      // last read's for as long as the cache is fresh. The source's own cadence
      // makes a repeat within the minute a document rather than a second child.
      else if (mergesWanted() && !held.value.merged) void decorateMerges(mine)
      return
    }
    const last = graph.screen?.redraw() ?? null
    if (last !== null) redraw(last, stopNow())
    else placeReading('graph', mine)
    // The placeholder is what says a read is running when this console has
    // nothing to redraw, so the read carries no token: a mark over the sentence
    // that already names the tab would be a second answer to the same click.
    if (graph.scope === GRAPH_SCOPE_ALL) {
      await readScope(mine, null)
      return
    }
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

  /** CONFIRM_BUTTON, CANCEL_BUTTON, UNDO_BUTTON and TARGET_SELECT are the move
   * sheet's own four stops (GV3.2). Each is the control the phase it was drawn in
   * is answered with -- the confirming button while a command line is in hand,
   * Cancel for every sheet that has no move to run, the way back after the record
   * applied one, and the select of the boxes while the sheet is choosing -- so
   * the keyboard lands on the button a person would press, and never on one the
   * drawing left out. */
  const CONFIRM_BUTTON: FocusTarget = { classes: [MOVE_CONFIRM_CLASS] }
  const CANCEL_BUTTON: FocusTarget = { classes: [MOVE_CANCEL_CLASS] }
  const UNDO_BUTTON: FocusTarget = { classes: [MOVE_UNDO_CLASS] }
  const TARGET_SELECT: FocusTarget = { classes: [MOVE_TARGET_CLASS] }

  /** moveStop is a record task's own row: the element the sheet was opened from,
   * where the keyboard goes back when the move is closed or declined. It may not
   * be in the tree the close draws -- a move that was applied put the row in
   * another box -- and the row survives that: the key is the document's and the
   * box it is drawn inside is not part of it. */
  const moveStop = (key: string): FocusTarget => ({ classes: ['graph-scope-node'], attribute: 'data-key', value: key })

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
      markGraph(token, GRAPH_BUSY)
      void screen.narrow(tab).then((element) => {
        if (!graphRead.settle(token)) return
        redraw(element, SELECTED_TAB)
      })
    }

    /**
     * chooseScopeControl is the toolbar's own choice of a scope (CG10), and it
     * is a `change` on the menu rather than a click on a button: what the control
     * answers with is its **value**, which is why the entry reads it back through
     * `valueOf` -- the same door the checkout chooser's own control uses.
     *
     * The choice goes to `chooseScope` and not to a read here: the two scopes
     * read two documents (so the entry is what can decide), and the read it
     * starts carries the section's token, which is what makes the answer land in
     * the frame the control was used in.
     */
    const control = classed(drawn, GRAPH_SCOPE_CONTROL_CLASS)[0]
    if (control !== undefined) {
      surface.nodeOf(control).addEventListener('change', () => {
        chooseScope(scopeOf(valueOf(surface, control)))
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
          if (run !== null) showRun(run)
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
          if (run !== null) showRun(run)
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
      if (run !== null) surface.nodeOf(open).addEventListener('click', () => showRun(run))
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

    /**
     * The move (GV3.2, and design section 5.1 rule 14).
     *
     * **The renderer's whole payload is two ids its own drawing put on two
     * elements**: the row's `data-key` and the target box's `data-release`. This
     * function never builds a word of a command line and never reads a title --
     * it hands the pair to the flow, and the sheet it draws is the answer the
     * main process composed out of the document it checked the pair against.
     *
     * **Every gesture ends in the same sheet.** A drop, the row's own button and
     * the select of the boxes all call `openMove`, which goes to a **preview**
     * (`confirmed: false`) -- a call that starts no child -- and the sheet that
     * comes back is what a person confirms or declines. Undo is the flow's
     * inverse move, previewed the same way, so there is no path from this screen
     * to a move nobody was shown.
     */
    /** drawMove puts one sheet on the drawing -- `null` closes it -- and leaves
     * the keyboard on the control that sheet is answered with. */
    const drawMove = (sheet: MoveSheet | null, focus: FocusTarget | null): void => {
      redraw(screen.change({ move: sheet }), focus)
    }

    /** openMove previews one move and draws the answer: the reading sheet while
     * the record is being asked, then the command line it answered with, or the
     * sentence a refusal came with. **The answer is drawn only while this gesture
     * is still the current one**: a drop on another box, the list's own choice or
     * an Escape replaces the sheet, and the reading this call started is then an
     * answer to a question nobody is asking -- drawn anyway, it is a sheet
     * showing one move over the request of another, and the confirming click
     * would run the move the flow holds rather than the one on screen. */
    const openMove = (node: string, toRelease: string): void => {
      const mine = takeMoveTurn()
      choosing = null
      drawMove({ request: { node, toRelease }, answer: null, sentence: null }, CANCEL_BUTTON)
      void moveFlowNow()
        .preview({ node, toRelease })
        .then((sheet) => {
          if (mine !== moveTurn || sheet === null) return
          drawMove(sheet, sheet.answer === null ? CANCEL_BUTTON : CONFIRM_BUTTON)
        })
    }

    /** closeMove is Cancel, and the Escape that stands for it: the request is
     * dropped by the flow, so nothing is sent, and the keyboard goes back to the
     * row the move was about. A preview still reading is dropped with the request
     * it was made for -- the answer would otherwise put the sheet back up over
     * the row a person has just gone back to. */
    const closeMove = (): void => {
      takeMoveTurn()
      const flow = moveFlowNow()
      const key = choosing ?? flow.pending?.node ?? null
      flow.decline()
      choosing = null
      drawMove(null, key === null ? DRAWING_STOP : moveStop(key))
    }

    /** confirmMove runs the move the sheet drew -- the flow answers `null` for a
     * second press, a stale key or a declined sheet, and sends nothing -- and is
     * followed by a re-read of the drawing, whatever the record answered. A move
     * it applied leaves the document in hand the one from before it, so the way
     * back would be refused as stale; a **refused** one is the record saying the
     * release it holds is not the one this console drew, which leaves the sheet's
     * own `in` field, the drawing behind it and the `--expect` of every next
     * move about a record that is no longer there. */
    const confirmMove = (): void => {
      const mine = takeMoveTurn()
      void moveFlowNow()
        .confirm()
        .then(async (sheet) => {
          if (sheet === null || mine !== moveTurn) return
          const applied = sheet.answer?.outcome?.status === 'applied'
          drawMove(sheet, applied ? UNDO_BUTTON : CANCEL_BUTTON)
          if (applied) {
            // R2: a move that was applied is a write, and the plan it moved is
            // the scope this tab caches. The clear comes first, so nothing draws
            // the pre-move plan out of the cache while the read below is out --
            // and the read below **is** the scope's own (`readByMove`), which is
            // why the invalidation does not start a second one.
            readByMove = mine
            invalidator.invalidate(['graph'])
          }
          await readScope(claim(), graphRead.start())
          // The sheet is drawn again over the fresh document, and the keyboard
          // goes to the button that answers it in the light of what the record
          // said: a reader who confirmed with the keyboard is left on the way
          // back rather than nowhere.
          if (mine !== moveTurn) return
          drawMove(sheet, applied ? UNDO_BUTTON : CANCEL_BUTTON)
        })
    }

    /** undoMove is the inverse move, previewed like any other: it does not run
     * anything, it puts a sheet up. Its answer is dropped while a newer gesture
     * is in hand, exactly as `openMove`'s is. */
    const undoMove = (): void => {
      const mine = takeMoveTurn()
      void moveFlowNow()
        .undo()
        .then((sheet) => {
          if (mine !== moveTurn || sheet === null) return
          drawMove(sheet, sheet.answer === null ? CANCEL_BUTTON : CONFIRM_BUTTON)
        })
    }

    for (const row of classed(drawn, 'graph-scope-node')) {
      const key = row.getAttribute('data-key') ?? ''
      if (row.getAttribute('draggable') !== 'true') continue
      dragOn(surface.nodeOf(row), 'dragstart', (event) => {
        // The key is held in this closure and written to the transfer as well:
        // the transfer is what a browser needs to start a drag at all, and what
        // the drop below reads is the closure, so the payload of a drag is never
        // the thing that decides which task moves.
        dragging = key
        event.dataTransfer?.setData('text/plain', key)
      })
      dragOn(surface.nodeOf(row), 'dragend', () => {
        dragging = null
      })
    }

    /** dropOn makes one element a place a task can be dropped on: a release box,
     * or the lane's unassigned group -- which is the empty key the operation
     * declares for taking a task out of every release (D2114). */
    const dropOn = (element: Drawn, toRelease: string): void => {
      const node = surface.nodeOf(element)
      dragOn(node, 'dragover', (event) => {
        // A drop happens only over a target that said the drag was welcome, and
        // that answer is also the cursor a person reads as "this box".
        if (dragging === null) return
        event.preventDefault()
      })
      dragOn(node, 'drop', (event) => {
        const moved = dragging
        dragging = null
        if (moved === null) return
        event.preventDefault()
        openMove(moved, toRelease)
      })
    }
    for (const box of classed(drawn, 'graph-scope-box')) {
      dropOn(box, box.getAttribute('data-release') ?? '')
    }
    for (const group of classed(drawn, 'graph-scope-unassigned')) dropOn(group, '')

    for (const button of classed(drawn, MOVE_OPEN_CLASS)) {
      const key = button.getAttribute('data-key') ?? ''
      surface.nodeOf(button).addEventListener('click', () => {
        // The keyboard's way into a move: the sheet opens on the list of boxes
        // and nothing is asked of the record until one is chosen. It is a gesture
        // like any other, so it takes the turn: a preview still reading for the
        // sheet that was up must not draw itself over this one.
        takeMoveTurn()
        choosing = key
        drawMove({ request: null, answer: null, sentence: null }, TARGET_SELECT)
      })
    }

    const target = classed(drawn, MOVE_TARGET_CLASS)[0]
    if (target !== undefined) {
      surface.nodeOf(target).addEventListener('change', () => {
        const key = choosing
        if (key === null) return
        const chosen = valueOf(surface, target)
        // The control opens on its own not-yet-chosen option
        // (`MOVE_TARGET_UNCHOSEN`), which is a value no box key can be and is
        // not a choice: the sheet stays up with nothing asked of the record,
        // exactly as it does before anyone has touched the list.
        if (chosen === MOVE_TARGET_UNCHOSEN) return
        openMove(key, chosen)
      })
    }
    const confirmButton = classed(drawn, MOVE_CONFIRM_CLASS)[0]
    if (confirmButton !== undefined) {
      const node = surface.nodeOf(confirmButton)
      node.addEventListener('click', () => {
        confirmMove()
      })
      keydownOn(node, (event: KeyEventLike) => {
        if (event.key === 'Escape') {
          event.preventDefault()
          closeMove()
        }
      })
    }
    const cancelButton = classed(drawn, MOVE_CANCEL_CLASS)[0]
    if (cancelButton !== undefined) {
      surface.nodeOf(cancelButton).addEventListener('click', () => {
        closeMove()
      })
    }
    const undoButton = classed(drawn, MOVE_UNDO_CLASS)[0]
    if (undoButton !== undefined) {
      surface.nodeOf(undoButton).addEventListener('click', () => {
        undoMove()
      })
    }
    const sheet = classed(drawn, 'graph-move')[0]
    if (sheet !== undefined) {
      // Escape closes the sheet from wherever the keyboard is inside it, exactly
      // as it closes the inspector: a confirmation is answered with two buttons
      // and a key, and Cancel is the one that writes nothing.
      keydownOn(surface.nodeOf(sheet), (event: KeyEventLike) => {
        if (event.key !== 'Escape') return
        event.preventDefault()
        closeMove()
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

  /** railBudget makes the console's own budget read where the caps of a screen
   * came from the feed, and answers the document it read.
   *
   * **The rail's spend is this read and never the feed's.** The design says so
   * in as many words (console-styling spec, "What the rail draws": "the spend is
   * the console's own budget read -- `budget.show` on the dashboard's channel --
   * ... and no field is added to it for the rail"), and the feed could not fill
   * it anyway: its rows are the repositories and the project's kill switch, and
   * neither the project's counted dollars the rail draws nor the counters the
   * gauge does are among them. So both figures are filled here or not at all.
   *
   * **It is one read for both screens** -- the Spend caps section and the Project
   * tab, the two RS5 gave the feed to -- because the two figures are one
   * document's, and it is exactly the read those screens made before the feed
   * existed. It draws no panel: the table a screen is looking at is the feed's,
   * and a document landing behind it does not undo it.
   *
   * **It is a read of the installation and not of a visit**, which is the same
   * division the rail's spend has always had: it is not tied to a section's
   * token, so a read that lands after the operator moved on still fills the rail,
   * and it draws no sentence of its own -- the sentence a section draws is the
   * read that section made. A read the console cannot make answers `null` and
   * leaves both figures as they are. */
  const railBudget = async (): Promise<Readonly<Record<string, unknown>> | null> => {
    const answer = documentOf(await bridge.showBudget({}))
    if (!answer.ok) return null
    rail.setSpend(railSpend(answer.data))
    setGauge(gaugeFromBudget(answer.data))
    return answer.data
  }

  /** spendCaps, spendCapsSentence and spendCapsWithheld are what the caps read
   * of this installation last answered, in the three states the section draws: a
   * panel of figures, the CLI's own words for a read it refused (drawn with
   * R1's Retry), or the reduced mode's word for a view it withholds (drawn
   * without one -- there is nothing to read again). They are held for the
   * installation and dropped by `forgetLive` -- the caps are a fact about the
   * installation and not about the window the operator chose, so a control
   * change re-reads the report and never the budget (`readSpend`'s own division,
   * kept here) -- and they are what a visit draws while its own caps read is in
   * flight.
   *
   * **spendBudget is the document and only a document read has one** (RS5): the
   * `budget show --stream` feed carries the repositories' rows and the project's
   * kill switch and neither the project's own figures nor the counters the rail
   * and the gauge read, so where the feed answered the section holds no document
   * of its own and the report model's own `caps` field is built from what the
   * session already holds -- until the console's own budget read, made for the
   * rail and the gauge beside the feed, lands with one (`railBudget`). */
  let spendBudget: Readonly<Record<string, unknown>> | null = null
  let spendCaps: SpendCaps | null = null
  let spendCapsSentence: string | null = null
  let spendCapsWithheld: string | null = null

  /** spendViews is what this session already read, one entry per view the three
   * controls can name: the installation, the window, the breakdown and the
   * person switch. It is memory and nothing else, and `forgetLive` empties it,
   * so a view read for one installation is never drawn under another. */
  const spendViews = resultCache<SpendModel>(CACHE_LIMIT)

  /** reportReader is the report's whole-document read -- the fallback RS5's
   * stream hands over to when the feed cannot be asked (`readReportStream`'s
   * `report`). It is at the entry's scope and not inside `showSpend` because the
   * prewarm queue reads the same command, and one closure over `bridge` is the
   * read both callers want: a refusal travels on a thrown value, which is how
   * `refusalSentence` reads it (`cause.sentence`, the shape `CliSource` throws),
   * so a read the console could not make at all is drawn with *that* sentence
   * rather than as a CLI refusal that printed nothing.
   *
   * **The visit's tokens are not its business.** Which answer stays and which
   * mark comes down belong to the section that asked (`spendReportRead`), so a
   * reader shared by two callers holds no state at all. */
  const reportReader: SpendReader = async (args) => {
    const answer = documentOf(await bridge.runReport(args))
    if (!answer.ok) throw { sentence: answer.sentence }
    return answer.data
  }

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
    /** holdingCaps is whether this session has an answer to draw while its own
     * caps read is out, in any of the three states `paintCaps` draws. */
    const holdingCaps = (): boolean =>
      spendCaps !== null || spendCapsSentence !== null || spendCapsWithheld !== null
    let capsPending = !holdingCaps()
    /** capsPartial is the caps feed's rows read **so far** by the read that is
     * out, and `paintCaps` draws it ahead of the session's held answer (RS5: the
     * table one line longer each time the feed writes one).
     *
     * **It is this visit's and never the session's.** Only a read that finished
     * is a view this session has read, so the rows of a feed still arriving --
     * or of one the operator left mid-feed, which `readStream` answers `dropped`
     * -- are never written to `spendCaps`: a partial list held as the caps would
     * be drawn as the caps on the next visit, which is the reading R1 forbids
     * (never a partial list as a complete one). It is cleared when this read
     * settles, whatever it settled as, and when a new read starts. */
    let capsPartial: SpendCaps | null = null
    /** capsWithheld is the reduced mode's answer for the caps view, asked once
     * per visit through `hidesView` and handed to the read itself: a mode that
     * withholds the caps must not have the console spawn the read whose answer it
     * may not draw (`readStream`'s `withheld`). The same gate withholds the
     * report below. */
    const capsWithheld = hidesView(gate.compatibility, 'caps')
    const reportWithheld = hidesView(gate.compatibility, 'spend')
    /** reportToken and capsToken are the sequence tokens of the reads that are
     * out. They are held here and not only inside the readers because `mount`
     * marks a section that is still pending, and the mark has to belong to the
     * read that is really in flight. */
    let reportToken = 0
    let capsToken = 0

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
      if (capsPartial !== null) {
        // The feed's rows read so far, and they are drawn ahead of the
        // session's held answer **and** of a sentence an earlier read left: this
        // section is being read again now, and the rows arriving are this read's
        // -- which is what the report section's own partial paint does with its
        // refusal, one section over.
        fillSpendCaps(factory, capsPartial, drawn.caps)
      } else if (spendCapsWithheld !== null) {
        // A withheld view is not a refused read: there is nothing to ask again,
        // so the section carries the console's own word and no Retry.
        refusedInto(
          drawn.capsSlot,
          refusedScreen(factory, 'budget.show', spendCapsWithheld, theme),
          null,
        )
      } else if (spendCapsSentence !== null) {
        // The Retry is this section's own read and not the report's: the two
        // reads of this screen are independent, and a control that re-read a
        // report the operator was not refused would answer the caps' sentence
        // with a figure from another read.
        refusedInto(
          drawn.capsSlot,
          refusedScreen(factory, 'budget.show', spendCapsSentence, theme),
          () => startCaps(),
        )
      } else {
        fillSpendCaps(factory, spendCaps ?? capsOf(spendBudget), drawn.caps)
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

    /** paintPartial draws the report section from the rows read **so far**
     * (RS5): the table one line longer each time the feed writes one, with the
     * controls drawn from the first row rather than from the last.
     *
     * It is `paintReport` without the stamp and without a cache write: a list
     * that is still arriving is not a view this session has read, and nothing
     * else may be drawn from it (`readStream`'s rule that only a `complete`
     * read's rows are the list -- the totals line and the section's own landing
     * are the `end` line's). */
    const paintPartial = (model: SpendModel, readWith: SpendControls): void => {
      reportPending = false
      const drawn = mount()
      clearSection(drawn.report, drawn.reportSlot)
      fillSpendReport(factory, model, theme, readWith, drawn.report)
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
     * latest.
     *
     * **The read is RS5's streamed one** (D2906/O3): `report --stream --json`
     * where the operation can be asked, which hands the rows over one paint at a
     * time, and the whole `report --json` document where it cannot -- no
     * handler, a CLI below the feed's floor, a refusal. What each chunk draws is
     * the table it has (`paintPartial`), the `end` line completes the section,
     * and a feed that stopped without one is R1's interrupted read: the sentence
     * and a Retry, never the rows that happened to arrive drawn as the list. */
    const loadReport = async (readWith: SpendControls, token: number): Promise<void> => {
      const outcome = await readReportStream({
        stream: (args) => ops.streamSpend(args),
        report: reportReader,
        controls: readWith,
        // The caps are handed over for the model's own `caps` field, which the
        // report section does not draw: the caps are their own section, and this
        // read starts before that one may have answered.
        caps: spendBudget,
        project,
        withheld: reportWithheld,
        // The by-person read is `runs stats` and is made only while the knob is
        // on, exactly as it was before the report's read grew a stream.
        people: peopleReader,
        onRows: (model) => {
          if (stale(mine)) return
          if (!spendReportRead.current(token)) return
          paintPartial(model, readWith)
        },
        // The section's own token decides which answer stays, and it is asked
        // between the chunks as well as after them: a read the operator has
        // replaced stops drawing rather than drawing under the newer answer.
        current: () => !stale(mine) && spendReportRead.current(token),
      })
      // The token is checked **before** anything is taken down. The busy state
      // belongs to the helper rather than to this read, so a read from an earlier
      // visit that landed after the operator came back would end the *current*
      // visit's mark -- taking its `aria-busy` off and cancelling its
      // fifteen-second line -- for a read nobody is waiting for any more.
      if (!spendReportRead.settle(token)) return
      if (stale(mine)) return
      if (outcome.status !== 'complete') {
        // A read the section moved on from answers no sentence at all and is
        // already accounted for above (`settle` is the same token `current`
        // asks), so only a read this visit still owns draws here.
        if (outcome.sentence === null) return
        // The report's refusal is **this section's own**, exactly as the caps'
        // is: the caps read may still be out, and a refusal drawn over the whole
        // panel would be replaced the moment it landed -- leaving the report
        // section marked as reading with no read in flight. The frame stays for
        // the same reason.
        reportPending = false
        const drawn = mount()
        clearSection(drawn.report, drawn.reportSlot)
        // The view is read again as the operator asked for it, which is the
        // controls on screen: a Retry that re-read a range the operator had
        // moved off would answer one refusal with another view's figures. A
        // withheld view is the one state drawn without it: there is nothing to
        // ask again.
        refusedInto(
          drawn.reportSlot,
          refusedScreen(factory, 'spend.report', outcome.sentence, theme),
          outcome.status === 'withheld' ? null : () => refresh(readWith),
        )
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
     * one this call started.
     *
     * **It is RS5's streamed read too** (`budget show --stream --json`, with the
     * whole `budget show --json` document as the fallback): the repositories'
     * rows fill the table one line at a time, the `end` line completes the
     * section, and a feed that stopped without one is R1's interrupted read.
     * What the feed does not carry -- the project's own figures, the counters
     * the rail and the gauge read -- is left to the whole-document read rather
     * than invented from the rows: where that is the read that answered, it fills
     * them itself, and where the feed answered they are read for on their own
     * (`railBudget`). */
    const loadCaps = async (token: number): Promise<void> => {
      const outcome = await readCapsStream({
        stream: () => ops.streamBudget({}),
        budget: async () => {
          const answer = documentOf(await bridge.showBudget({}))
          // The sentence travels on the thrown value, `reader`'s own shape: a
          // read the console could not make at all must be drawn with that
          // sentence and not as a CLI refusal that printed nothing.
          if (!answer.ok) throw { sentence: answer.sentence }
          return answer.data
        },
        withheld: capsWithheld,
        onRows: (caps) => {
          if (stale(mine)) return
          if (!spendCapsRead.current(token)) return
          // The rows are drawn from `capsPartial` and nowhere else: this read
          // has not answered yet, and a feed still arriving is not a view the
          // session holds (`capsPartial`'s own note).
          capsPartial = caps
          paintCaps()
        },
        current: () => !stale(mine) && spendCapsRead.current(token),
      })
      // The feed's rows are the section's while its read is out and not after
      // it: whatever this read answered is the whole of what the section holds.
      capsPartial = null
      // The rail's spend and the gauge are the console's own budget read -- this
      // one, the read the caps section makes -- and the rail makes none of its
      // own (design section 4.1). They are filled before the staleness check
      // below: the spend is a fact about the installation rather than about the
      // view the operator chose, so a read that landed after they moved on still
      // fills the rail.
      if (outcome.status === 'complete') {
        if (outcome.document !== null) {
          rail.setSpend(railSpend(outcome.document))
          setGauge(gaugeFromBudget(outcome.document))
          spendBudget = outcome.document
        } else {
          // The feed answered, and it carries neither figure: they are read for
          // on their own, and the document that read answers becomes the one the
          // report model's own `caps` field draws on.
          void railBudget().then((document) => {
            if (document !== null) spendBudget = document
          })
        }
      }
      // The three states are one answer between them, and `paintCaps` reads the
      // withheld word first and the sentence second: a refusal left standing
      // here is the section refusing for the rest of the session, whatever the
      // reads that follow say (only `forgetLive` used to clear it).
      if (outcome.status === 'complete') {
        spendCaps = outcome.caps
        spendCapsSentence = null
        spendCapsWithheld = null
      } else if (outcome.status === 'withheld') {
        spendCaps = null
        spendBudget = null
        spendCapsSentence = null
        spendCapsWithheld = outcome.sentence
      } else if (outcome.sentence !== null) {
        spendCaps = null
        spendBudget = null
        spendCapsSentence = outcome.sentence
        spendCapsWithheld = null
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
      // A new read starts from what the session holds: rows an earlier feed was
      // still writing when the operator left it are that read's and not this
      // one's (`capsPartial`'s own note).
      capsPartial = null
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
    const heldCaps = holdingCaps()
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

    // R2: the scope this screen registers when it opens. What it holds is the
    // reports this session read for the installation; the read is the whole
    // visit's own pair, which is what the stamp's Refresh draws (`refreshFrom`).
    // The caps are read again with the report because they are the same
    // installation's budget: a write that moved one moved the other.
    invalidator.remember('spend', {
      clear: () => spendViews.clear(),
      reread: () => {
        if (stale(mine)) return
        refreshFrom(controls)
      },
    })
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
   * under another.
   *
   * **It holds the budget document and not the built model** (CM10): the model
   * is now a function of three reads -- `budget show`, `policy.show` and
   * doctor's -- and a cache of the model would freeze the two later ones at
   * whatever they said when the budget was read. The budget is the read this
   * cache is for; the other two are made on every visit, so a cached view drawn
   * before they land is filled in as they do. */
  const projectsViews = resultCache<Readonly<Record<string, unknown>>>(CACHE_LIMIT)

  /** projectsRead is the screen's own read lifecycle. The read is one
   * (`budget show`), so the section is the screen and there is one of each: one
   * busy mark, one sequence token, one refusal. */
  const projectsRead = sectionRead({ factory, theme })

  /** projectsModelsRead is the tab's **second** section (console 0.119.0):
   * `models ls --json`, the routes, the models a run can name and the price
   * catalog's age. It has a lifecycle of its own for the reason the Coordinator's
   * window does -- its own sequence token, its own busy mark, its own refusal --
   * because the two reads of this tab are independent: the models read is offline
   * and cheap and does not wait on `budget show`, and a tab holding one mark for
   * both would hold the models behind the budget or the budget behind the
   * models. */
  const projectsModelsRead = sectionRead({ factory, theme })

  /** projectsSettingsRead is the tab's **third** section (the settings-edit
   * plan's C1): `settings --json`, the registry's rows with what decides each
   * one and which of them the generic path may write. It has a lifecycle of its
   * own for the models section's reason one read over -- its own token, its own
   * mark, its own refusal -- because the third read is as independent as the
   * other two: it is a cloud read of its own (the project layer is cached, but
   * the read is the read) and neither the budget nor the models may be held
   * behind it. */
  const projectsSettingsRead = sectionRead({ factory, theme })

  /** projectsManagedRead, projectsRetirementsRead, projectsAccountKeysRead and
   * projectsCredentialsServiceRead are the tab's **four later sections** (the
   * managed-project screens plan's CM10): the managed state, the last update's
   * retirements, the review account keys and the credentials service. Each has a
   * lifecycle of its own -- its own sequence token, its own busy mark and its own
   * refusal -- for the reason every section here has one: the four reads are as
   * independent as the three above, and a tab holding one mark for them would
   * hold three sections behind the slowest one.
   *
   * **Four lifecycles and three reads**, because one read can feed two sections
   * (D1488): `managed status --json` draws the managed state and the retirements,
   * and `settings --json` -- the read the settings panel already makes -- draws
   * the account keys. The lifecycles stay separate whatever the read is, which is
   * the whole reason the count of them is not the count of the reads: the two
   * sections of one read settle on their own hosts and neither's mark can take
   * the other's down. */
  const projectsManagedRead = sectionRead({ factory, theme })
  const projectsRetirementsRead = sectionRead({ factory, theme })
  const projectsAccountKeysRead = sectionRead({ factory, theme })
  const projectsCredentialsServiceRead = sectionRead({ factory, theme })

  /** projectsLicenceRead is the tab's **eighth** section (the subscriptions
   * plan's S2.7): the licence key this installation runs under, which the
   * console keeps itself. It has a lifecycle of its own for the reason every
   * section here has one, and it is the only one of the eight whose read spawns
   * no child at all -- `licence.read` answers out of the console's own store
   * (`src/main/licence-store.ts`), so it neither holds nor is held by a command
   * line. */
  const projectsLicenceRead = sectionRead({ factory, theme })

  /** projectsSeatsRead is the tab's **ninth** section (the subscriptions plan's
   * S4.2): the advisory estimate of who launches jobs on this installation,
   * drawn beside the seats the licence key carries. It reads a command of its
   * own -- `fugaro seats --json` -- and it is the one section of this tab whose
   * subject is a **contract pricing rule** rather than a running thing: a count
   * of people, never a figure of what this installation spent. */
  const projectsSeatsRead = sectionRead({ factory, theme })

  /** leaveProjects ends those sections' busy state when the operator moves to
   * another tab: nothing is in flight for a screen nobody is looking at. */
  leaveProjects = () => {
    projectsRead.end()
    projectsModelsRead.end()
    projectsSettingsRead.end()
    projectsManagedRead.end()
    projectsRetirementsRead.end()
    projectsAccountKeysRead.end()
    projectsCredentialsServiceRead.end()
    projectsLicenceRead.end()
    projectsSeatsRead.end()
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
   *
   * **The models panel is a second section with a second read** (console
   * 0.119.0): `models ls --json` goes out in the same turn as `budget show` and
   * paints the moment it lands. It is gated by its own floor
   * (`modelsListSupported`): below it this visit spawns nothing and the panel
   * draws the console's own sentence naming the version it needs, which is the
   * shape the Coordinator's fleet window has one tab over.
   *
   * **The settings panel is a third section with a third read** (the
   * settings-edit plan's C1): `settings --json` goes out in the same turn as the
   * other two and paints the moment it lands. It has no floor of its own and
   * takes no cache: the read is the project's settings as the CLI reads them
   * now, and a CLI that cannot take `--project` answers `unknown flag`, which
   * this panel draws as its own reason.
   *
   * **Four later sections join them, and the policy panel stops being a
   * not-built one** (the managed-project screens plan's CM10). The managed
   * state, the retirements, the review account keys and the credentials service
   * are four siblings of the three panels above, each with its own read, its own
   * busy mark and its own refusal -- drawn the way the models and settings
   * panels are, and not as members of `PROJECT_PANEL_KEYS`, which stays the
   * first read's seven. Their reads go out in the same turn as the other three,
   * in one turn and not one after the other, and each section paints the moment
   * its document arrives. **Jobs are not in the first paint** and are not on
   * this tab at all (`runs ls --json` is gone from it): the owner's rule of
   * 2026-10-31, and the reason the tab's first read is the caps and not the run
   * list.
   *
   * **The policy panel is a member of the body's seven and reads a document of
   * its own** (`policy.show`, D1481). Its read is a later one, so the body is
   * **repainted** when it lands -- the panel's state, its source line, its count
   * and the section under its frame are all functions of the body's model, so
   * the only drawing that can turn it from "no document arrived" into a read one
   * is the body's own. Until then the panel says which read has not answered,
   * which is the sentence the node block beside it draws (`absent`) -- and the
   * rest of the body is drawn, and stays drawn, whatever that read does.
   */

  const showProjects = async (): Promise<void> => {
    const mine = claim()
    const project = railNow.find((row) => row.open)?.project ?? ''
    const key = cacheKey([project, 'projects'])
    const held = projectsViews.get(key)
    let frame: DrawnProjectsFrame | null = null
    let token = 0

    /** modelsTooOld is whether the CLI in use predates `models ls`
     * (`modelsListSupported`). It landed after v0.15.0, so a 0.15.x CLI has no
     * such verb and answers `unknown command "ls"` -- a failure the operator can
     * do nothing about. The floor is asked **before** the read goes out. */
    const modelsTooOld = !modelsListSupported(gate.compatibility.cliVersion ?? '')
    /** seatsTooOld is whether the CLI in use predates the seat estimate
     * (`seatsSupported`). `fugaro seats` landed in v0.21.0, and every CLI above
     * this console's supported set answers `ok`, so a 0.20.x installation is a
     * CLI the tab reads and one that answers `unknown command "seats"` for this
     * read -- a failure the operator can do nothing about. The floor is asked
     * **before** the read goes out, like the models panel's. */
    const seatsTooOld = !seatsSupported(gate.compatibility.cliVersion ?? '')
    /** capsWithheld is the reduced mode's answer for this tab's own view
     * (`hidesView`), asked once per visit and handed to the body's read: a mode
     * that withholds the caps must not have the console spawn the read whose
     * answer it may not draw (`readStream`'s `withheld`), and the body then
     * draws the console's own word for the view instead of its figures. */
    const capsWithheld = hidesView(gate.compatibility, 'caps')
    /** model and modelsSentence are what `models ls` last answered on this
     * visit, and `modelsOut` says a read of it is in flight. */
    let models: ModelsListView | null = null
    let modelsSentence: string | null = null
    let modelsOut = false
    let modelsAttempt = 0
    let modelsToken = 0
    /** modelsPainted says this visit has already drawn a models panel: the mark
     * of a re-read carries no skeleton over content that is already there. */
    let modelsPainted = false
    /** settings, settingsSentence and settingsOut are the third section's, on
     * the models section's terms: what `settings --json` last answered, the CLI's
     * own sentence for a read that was refused, and whether a read of it is in
     * flight. There is no floor for this read and no cache of its own: unlike
     * `models ls`, `settings` is a verb of the CLI the console has always
     * supported, and unlike the budget a settings row is about the project's
     * configuration now -- the two flags the read needs (`--project`,
     * `--config`) are the ones every cloud command of this tab takes. A CLI too
     * old for them refuses the spawn with its own sentence (`unknown flag`),
     * which is drawn as this panel's reason rather than as an empty table. */
    let settings: unknown = null
    let settingsSentence: string | null = null
    let settingsOut = false
    let settingsAttempt = 0
    let settingsToken = 0
    /** settingsPainted says this visit has already drawn a settings panel, for
     * `modelsPainted`'s reason. */
    let settingsPainted = false
    /** managed, managedSentence and managedOut are the managed state's and the
     * retirements' read: one document, two sections (D1488). `managed` is what
     * `managed status --json` last answered, `managedSentence` the CLI's own
     * sentence for a read that was refused, and `managedOut` whether a read of it
     * is in flight. */
    let managed: unknown = null
    let managedSentence: string | null = null
    let managedOut = false
    let managedAttempt = 0
    let managedToken = 0
    let retirementsToken = 0
    /** policy, policySentence and policyAttempt are the policy panel's own read
     * (`policy.show`): the `{project, policy}` document, the CLI's own sentence
     * for a refused one, and the attempt that keeps a slow answer from landing
     * under a newer visit. There is no `policyOut` flag and no busy mark of its
     * own: the panel is a member of the body's model, so its "still reading"
     * state **is** the body's drawing of a document that has not arrived. */
    let policy: unknown = null
    let policySentence: string | null = null
    let policyAttempt = 0
    /** doctor, doctorSentence and doctorAttempt are the credentials service's
     * read (`environment.report`). The same document is the policy panel's checks
     * block (`policynotes.ts`), which is why it is held here rather than inside
     * that section: two sections read it, and one read is made. */
    let doctor: unknown = null
    let doctorSentence: string | null = null
    let doctorOut = false
    let doctorAttempt = 0
    let credentialsServiceToken = 0
    /** accountKeysOut and accountKeysToken are the account keys section's own
     * lifecycle state over the **settings** read: the section has its own mark
     * and its own token, and the read is the one the settings panel above it
     * makes (`settingsOut`/`settingsToken` are that read's). */
    let accountKeysOut = false
    let accountKeysToken = 0
    /** licence, licenceSentence, licenceOut and licenceAttempt are the licence
     * key's own read (`licence.read`, the subscriptions plan's S2.7): the state
     * and claim set the verifier answered for this installation's key, the
     * console's own sentence for a read it could not make, whether one is in
     * flight and which attempt it was. The document is held **whole and
     * unread**, for `buildAccountKeys`'s reason: the section reads its own reply.
     */
    let licence: unknown = null
    let licenceSentence: string | null = null
    let licenceOut = false
    let licenceToken = 0
    let licenceAttempt = 0
    /** licenceDrawn is the panel the box is in, or null when the section holds
     * no form -- a read that did not answer offers none, and an answer to a save
     * is drawn only while the panel that sent it is still the one on screen: a
     * box whose panel was redrawn while the save was out is a box that is gone,
     * and its answer belongs to a section nobody is looking at. */
    let licenceDrawn: LicenceDrawn | null = null
    /** seats, seatsSentence, seatsOut and seatsAttempt are the seat estimate's
     * own read (`seats.read`, the subscriptions plan's S4.2): the document
     * `fugaro seats --json` printed, the CLI's own sentence for a read that was
     * refused, whether one is in flight and which attempt it was. The document
     * is held **whole and unread**, for `licence`'s reason: the section reads
     * its own reply.
     *
     * **seatsPainted is what the licence's own landing is checked against**: the
     * seats panel draws the key's seat count out of the licence section's model,
     * so a licence that lands after the estimate has been drawn has to be able
     * to repaint this panel with it -- and only when the panel is on screen and
     * no read of its own is in flight, which is what keeps a landing from
     * clearing another read's mark. */
    let seats: unknown = null
    let seatsSentence: string | null = null
    let seatsOut = false
    let seatsToken = 0
    let seatsAttempt = 0
    let seatsPainted = false
    /** bodyModel is the model the body is drawn from, or null when the body holds
     * something that is not one -- a refusal, or nothing yet. The policy and
     * doctor reads repaint the body **only** when it holds a model: a landing
     * that drew a model built from no budget document would replace the caps with
     * a sentence about a read that is still in flight. */
    let bodyModel: ProjectsModel | null = null
    /** budgetDoc is the last `budget show` document this visit read, and budgetOut
     * says a read of it is in flight. Both are `repaintBody`'s: the model is drawn
     * around the document, and a repaint while the budget's own read is out would
     * take that read's mark down. `budgetAt` is the console clock that document
     * was read at, which the cache stamp is drawn from and which a repaint may
     * not move forward. */
    let budgetDoc: Readonly<Record<string, unknown>> | null = null
    let budgetAt = 0
    let budgetOut = false
    /** budgetStreamed says the document the body is drawn from came out of a
     * `budget show --stream` feed rather than from the whole document (RS5):
     * the feed carries the repositories' rows and the project's kill switch and
     * no other member, so the panels that read one -- the project's own caps,
     * the defaults, the mode, the reserve -- must say the read did not carry
     * them instead of drawing the `n/a` a whole document's own absence means
     * (`ProjectsInputs.streamed`). */
    let budgetStreamed = false
    /** cachedAt is the console clock the body's budget figures were read at, or
     * null when they came out of a read made now. It is held rather than passed,
     * because the body is repainted by three different landings and only the
     * budget's own knows whether its figures are the cache's. */
    let cachedAt: number | null = null

    /** modelNow builds the body's model from the documents this visit holds. The
     * policy panel is a member of that model, so the two later reads
     * (`policy.show` and `doctor --json`) are handed here, along with the CLI's
     * own sentence for whichever of them was refused. */
    const modelNow = (budget: unknown, streamed: boolean): ProjectsModel => {
      const sentences: Partial<Record<OperationName, string>> = {}
      if (policySentence !== null) sentences['policy.show'] = policySentence
      return buildProjectsModel({
        budget,
        accounts: null,
        secrets: null,
        policy,
        doctor,
        sentences,
        streamed,
      })
    }

    /** mount draws the shell if it is not up yet, and answers it. The mark of a
     * read that is still out goes up here, which is the only moment it can be
     * drawn: the panel held another view's content until this call. */
    const mount = (busy: boolean): DrawnProjectsFrame => {
      if (frame !== null) return frame
      const drawn = renderProjectsFrame(factory, theme)
      frame = drawn
      replace(drawn.root)
      if (busy) {
        projectsRead.mark(token, drawn.body, drawn.slot, PROJECTS_BUSY, PROJECTS_SHAPE)
        // The models section's mark goes on **its own** host and its own slot:
        // the two reads are in flight at once, and one element wearing both
        // claims would have whichever read landed first take the other's down.
        if (modelsOut) {
          projectsModelsRead.mark(
            modelsToken,
            drawn.models,
            drawn.modelsSlot,
            PROJECTS_MODELS_BUSY,
            PROJECTS_MODELS_SHAPE,
          )
        }
        if (settingsOut) {
          projectsSettingsRead.mark(
            settingsToken,
            drawn.settings,
            drawn.settingsSlot,
            PROJECTS_SETTINGS_BUSY,
          )
        }
        // CM10's four, in the order the frame mounts their hosts. Two of them
        // wear a mark for a read the section above them makes -- the account
        // keys for the settings read, the retirements for the managed state's --
        // and that is the point of two lifecycles for one read: each mark goes on
        // its own host, and whichever lands first takes down neither.
        if (managedOut) {
          projectsManagedRead.mark(managedToken, drawn.managed, drawn.managedSlot, MANAGED_BUSY)
          projectsRetirementsRead.mark(
            retirementsToken,
            drawn.retirements,
            drawn.retirementsSlot,
            RETIREMENTS_BUSY,
          )
        }
        if (accountKeysOut) {
          projectsAccountKeysRead.mark(
            accountKeysToken,
            drawn.accountKeys,
            drawn.accountKeysSlot,
            ACCOUNT_KEYS_BUSY,
          )
        }
        if (doctorOut) {
          projectsCredentialsServiceRead.mark(
            credentialsServiceToken,
            drawn.credentialsService,
            drawn.credentialsServiceSlot,
            CREDENTIALS_SERVICE_BUSY,
          )
        }
        // The licence key (S2.7), the last host the frame mounts: its mark goes
        // up on its own section like the seven above, and it is the one mark in
        // this tab whose read is not a child process.
        if (licenceOut) {
          projectsLicenceRead.mark(licenceToken, drawn.licence, drawn.licenceSlot, LICENCE_BUSY)
        }
        // The seat estimate (S4.2), the last host the frame mounts: its mark goes
        // up on its own section like the eight above, and its read is the one
        // section of this tab whose child reads the installation's bucket --
        // every run of the last 90 days, one `task.json` apiece.
        if (seatsOut) {
          projectsSeatsRead.mark(seatsToken, drawn.seats, drawn.seatsSlot, SEATS_BUSY)
        }
      }
      return drawn
    }

    /** paint fills the section from what `budget show` answered. `cached` is
     * whether this view came out of the session cache rather than out of a read
     * made now: a cached view carries the console clock it was read at and the
     * one control that reads it again.
     *
     * The model is built here rather than handed in, because the body is drawn
     * three times over one visit -- once for the budget, and once more for each
     * of the two reads that land in a panel of it (CM10's policy read and
     * doctor's checks). A caller would have to hold every document the model is
     * a function of; the body holds them itself.
     */
    const paint = (
      budget: Readonly<Record<string, unknown>>,
      at: number,
      cached: boolean,
      streamed = false,
    ): void => {
      const model = modelNow(budget, streamed)
      bodyModel = model
      budgetDoc = budget
      budgetAt = at
      cachedAt = cached ? at : null
      budgetStreamed = streamed
      const drawn = mount(false)
      clearSection(drawn.body, drawn.slot)
      fillProjects(factory, model, drawn.body)
      // The cache is rolled forward with what the repaint drew, so a visit that
      // comes back to this tab draws the fuller view rather than the one the
      // budget's own read built. A cached view is never written back: its stamp
      // would then say the figures are newer than they are. **A streamed view is
      // never cached either** (RS5): what the feed assembled is not the document
      // the next visit would draw, and a cache hit is drawn as a whole document
      // -- the members the feed does not carry would come back as absences the
      // console could not account for.
      if (!cached && !streamed) projectsViews.put(key, budget, at)
      if (cachedAt !== null) {
        const stamp = renderStamp(factory, cachedAt)
        appendTo(drawn.body, stamp.root)
        surface.nodeOf(stamp.refresh).addEventListener('click', () => {
          start()
        })
      }
    }

    /** paintPartial draws the body from the rows read **so far** (RS5): the caps
     * table one line longer each time the feed writes one, drawn before the `end`
     * line's own landing.
     *
     * It is `paint` without the state a landing owns: no cache write, no clock
     * and no `budgetDoc` (`repaintBody` is guarded by `budgetOut` while this
     * read is out, and the landing below replaces all of it). What it draws is
     * the feed's own read, so the model is built `streamed`, and the members the
     * feed does not carry are drawn as this read not having them rather than as
     * a figure. */
    const paintPartial = (document: Readonly<Record<string, unknown>>): void => {
      const drawn = mount(false)
      clearSection(drawn.body, drawn.slot)
      fillProjects(factory, modelNow(document, true), drawn.body)
    }

    /** repaintBody draws the body again from the documents this visit holds now,
     * and it is what the two later reads of a panel of it do when they land
     * (CM10). It does nothing while the budget's own read is out or while the
     * body holds something that is not a model: a landing that cleared a live
     * mark, or replaced the caps with a sentence about a read still in flight,
     * would be this console taking a drawing away to say a drawing is coming.
     *
     * Nothing is lost by waiting -- `load` builds the body from the documents
     * held at that moment, so a policy or a doctor answer that landed first is
     * drawn by the budget's own paint. */
    const repaintBody = (): void => {
      if (budgetDoc === null || bodyModel === null || budgetOut) return
      // The stamp's clock is the one the budget's own read was made at, and it
      // is passed back unchanged: a repaint is not a fresh reading of the caps,
      // and moving the stamp forward would say the figures are newer than they
      // are. `budgetStreamed` travels with the document, so a body drawn from a
      // feed is repainted as the feed's own read (`ProjectsInputs.streamed`).
      paint(budgetDoc, budgetAt, cachedAt !== null, budgetStreamed)
    }

    /**
     * paintModels fills the tab's second section from what `models ls`
     * answered. Below the floor and on a refused read it draws the panel's own
     * sentence and no list: a document that did not arrive is never drawn as an
     * empty set of routes and models, which would read as "this project runs
     * nothing" (`buildProjectsModels`'s three inputs are the whole of the
     * distinction).
     */
    const paintModels = (): void => {
      const drawn = mount(false)
      modelsPainted = true
      clearSection(drawn.models, drawn.modelsSlot)
      fillProjectsModels(
        factory,
        buildProjectsModels({
          document: models,
          because: modelsSentence,
          tooOld: modelsTooOld,
          version: gate.compatibility.cliVersion ?? '',
        }),
        drawn.models,
      )
    }

    /**
     * openSettingsRow is a pencil's press: the row's own key is handed to
     * `settings.editor.open` (SU1's one modal, D2902), and that flow does the
     * entry, the dry run's diff and the confirmation, and the write on a yes
     * from its own window's top frame (rules 11 and 12).
     *
     * **What leaves this file is the key and nothing else**
     * (`SettingsEditorOpenRequest`): no value, no subcommand, no layer and no
     * argv. The value is the operator's and it is typed into the window rather
     * than into a box this console draws, so the layer, the dry run, the window
     * and the write are the main process's -- and an empty box there is the
     * `unset` subcommand rather than a value of `""` (D1103).
     *
     * **The row draws the outcome.** The answer is the CLI's own sentence
     * (`SettingsEditorOutcome`, the shape `settings.edit` answered with) and it
     * is appended to the `li` the pencil sits in, so the key, the scope it
     * belongs to and the value a run gets stay on the screen around it. A reply
     * this build cannot read -- no handler behind the operation, a channel that
     * threw, a document the contract's decoder refused -- draws the operation's
     * own gap sentence rather than a claim about a write
     * (`settingsEditorRead`).
     *
     * **Nothing is retried, and nothing is held.** A refusal draws the CLI's own
     * words and no second write follows it (D1112): the compare-and-swap refuses
     * an edit built on bytes that moved, and the operator's way to ask again is
     * the pencil itself, which is a press and not this console's decision. The
     * one window is main's, so an edit already open on the project is refused by
     * the flow with its own sentence, which this row draws like any other
     * answer: a second press is not this file's to refuse or to remember.
     *
     * **A write re-reads the tab at once** (R2): the row is one figure of this
     * tab's documents and the console's cached copy of them is the one from
     * before the write, so the scope the visit registered is dropped and read
     * again -- the body's reads and this section's own, which is what puts the
     * row the outcome was about back with the value the write left (`beginSettings`,
     * in the scope's `reread`). The sentence above it is the CLI's account of
     * the write, and it stays on the screen until that read lands.
     */
    const openSettingsRow = async (key: string, pencil: Element): Promise<void> => {
      const row = pencil.closest('li')
      if (row === null) return
      clearSettingsOutcome(row)
      const outcome = factory.create('span')
      outcome.setAttribute('class', SETTINGS_EDIT_OUTCOME_CLASS)
      outcome.setAttribute('aria-live', 'polite')
      row.appendChild(surface.nodeOf(outcome))
      const read = settingsEditorRead(await ops.openSettingsEditor({ key }))
      outcome.textContent = read.sentence
      if (read.written) invalidator.invalidate(['projects'])
    }

    /**
     * bindSettingsPencils attaches each pencil the settings panel just drew to
     * the press above, and puts the focus back on the pencil it was pressed on
     * once the flow has ended, so the keyboard returns to the control that
     * opened the window rather than staying on the window that closed (D1116).
     * The focus is taken on the node the drawing answered with and not on a
     * re-drawn one, for `redraw`'s reason: a node this session has taken down is
     * one a real window has detached, and focusing it would leave the keyboard
     * behind.
     *
     * **The focus moves after the answer and not on the press**, which is the
     * one thing this half of D1116 changes with the modal: the press now waits
     * for the window (`openSettingsRow` resolves when the flow has ended), and a
     * focus taken while the window is still open would pull the keyboard back
     * out of it. A write's own re-read replaces the row this node is in, and a
     * replaced node cannot hold the keyboard -- the limit the inline editor had,
     * one press later.
     */
    const bindSettingsPencils = (host: Drawn): void => {
      for (const pencil of classed(host, SETTINGS_PENCIL_CLASS)) {
        const key = pencil.getAttribute(SETTINGS_PENCIL_KEY) ?? ''
        const node = surface.nodeOf(pencil)
        node.addEventListener('click', () => {
          void openSettingsRow(key, node).then(() => {
            const focusable = node as Element & { focus(): void }
            focusable.focus()
          })
        })
      }
    }

    /**
     * paintSettings fills the tab's third section from what `settings --json`
     * answered.
     *
     * `raw` is the document as it arrived rather than a decoded view, because
     * this screen reads its own documents (`buildProjectsSettings`'s note): a
     * document of another version, or a read that was refused, draws the panel's
     * own sentence and no table -- an empty table would read as "this project
     * sets nothing", which is a claim no answer here made.
     *
     * The rows are replaced whole, so an outcome line an earlier press drew
     * goes with them: it is a child of one of these rows, and the row it was
     * appended to no longer exists. That is also what takes a write's own
     * sentence off the screen -- the re-read the write asked for is this paint,
     * and the fresh row is what the operator is left reading.
     */
    const paintSettings = (): void => {
      const drawn = mount(false)
      settingsPainted = true
      clearSection(drawn.settings, drawn.settingsSlot)
      fillProjectsSettings(
        factory,
        buildProjectsSettings({ raw: settings, because: settingsSentence }),
        drawn.settings,
      )
      bindSettingsPencils(drawn.settings)
    }

    /**
     * paintManaged and paintRetirements fill the tab's fourth and fifth sections
     * from what `managed status --json` answered.
     *
     * **One read, two sections** (D1488): the managed state draws the marks and
     * the trail, and the retirements draw the last update's set, out of the same
     * document. Each is filled on its own host and out of its own model, so a
     * section that had nothing to say -- a refused read, a document this build
     * does not read -- says it in its own place without the other's content.
     */
    const paintManaged = (): void => {
      const drawn = mount(false)
      clearSection(drawn.managed, drawn.managedSlot)
      fillManaged(factory, buildManagedModel({ raw: managed, because: managedSentence }), drawn.managed)
    }

    /** paintRetirements fills the fifth section, out of the same document
     * `paintManaged` reads (`buildRetirementsModel`'s note: it reads the
     * `retirements` member of `managed status --json`). */
    const paintRetirements = (): void => {
      const drawn = mount(false)
      clearSection(drawn.retirements, drawn.retirementsSlot)
      fillRetirements(
        factory,
        buildRetirementsModel({ raw: managed, because: managedSentence }),
        drawn.retirements,
      )
    }

    /**
     * paintAccountKeys fills the sixth section from the document the settings
     * panel beside it reads: the review account keys are `settings --json`'s own
     * rows (`default_claude_account` and the stage keys), so the tab makes **one**
     * read of that document and fills two sections with it (D1488).
     *
     * The read's state is the settings section's (`settings`/`settingsSentence`),
     * and the section's own drawing is this module's: it is handed the document
     * as it arrived, because it reads the version and the members it draws itself
     * (`buildAccountKeys`'s note).
     */
    const paintAccountKeys = (): void => {
      const drawn = mount(false)
      clearSection(drawn.accountKeys, drawn.accountKeysSlot)
      fillAccountKeys(
        factory,
        buildAccountKeys({ raw: settings, because: settingsSentence }),
        drawn.accountKeys,
      )
    }

    /** paintCredentialsService fills the seventh section from what
     * `doctor --json` answered. The same document is the policy panel's checks
     * block, which is drawn by the body's own repaint and not here: the two
     * sections read one document and draw different things out of it. */
    const paintCredentialsService = (): void => {
      const drawn = mount(false)
      clearSection(drawn.credentialsService, drawn.credentialsServiceSlot)
      fillCredentialsService(
        factory,
        buildCredentialsService({ raw: doctor, because: doctorSentence }),
        drawn.credentialsService,
      )
    }

    /** paintLicence fills the eighth section from what `licence.read` answered.
     * It is handed the document as it arrived, because the section reads its own
     * reply (`buildLicence`'s note), and it keeps the handles the drawing
     * answered so the paste's own event has box, control and outcome without
     * walking the tree. */
    const paintLicence = (): void => {
      const drawn = mount(false)
      clearSection(drawn.licence, drawn.licenceSlot)
      const painted = fillLicence(
        factory,
        buildLicence({ raw: licence, because: licenceSentence }),
        drawn.licence,
      )
      licenceDrawn = painted
      wireLicence(painted)
    }

    /** paintSeats fills the ninth section from what `seats.read` answered, and
     * from the licence section's own model: the estimate is one document and the
     * figure it is compared against is the key's, which this section does not
     * read for itself (`buildSeats` finds the seats row `buildLicence` made, so
     * the number an operator reads here is the number they read one panel over).
     *
     * It is handed both documents as they arrived, because the section reads its
     * own reply. **It is also what the licence's own landing calls**, so a key
     * that arrives after the estimate repaints this panel with its seat count --
     * guarded by `seatsPainted` and `!seatsOut` at that call site, because a
     * repaint blanks the section and the mark of a read that is still out lives
     * in a slot inside it. */
    const paintSeats = (): void => {
      const drawn = mount(false)
      clearSection(drawn.seats, drawn.seatsSlot)
      seatsPainted = true
      fillSeats(
        factory,
        buildSeats({
          raw: seats,
          licence: buildLicence({ raw: licence, because: licenceSentence }),
          because: seatsSentence,
          tooOld: seatsTooOld,
          version: gate.compatibility.cliVersion ?? '',
        }),
        drawn.seats,
      )
    }

    /**
     * wireLicence binds the paste: the one control of this section that writes
     * anything.
     *
     * **The whole save is one press and one sentence.** The box's text goes to
     * `licence.save` as it was pasted -- trimming and the shape checks belong to
     * the surface's own argument check (`ipc.ts`'s `licenceKey` kind) and to the
     * verifier, not to an event handler -- and what comes back is drawn in the
     * form's outcome region. The key is never echoed into that region, into the
     * log or into the panel: the answer carries a state, and a sentence when the
     * save was refused.
     *
     * **A save that worked re-reads the section**, rather than drawing a second
     * answer of its own: the state and the claim set the next read draws are the
     * verifier's word on the key that was just kept, and the box is emptied --
     * what it held is kept now, and a credential left in a text field is a
     * credential on screen for as long as the tab is open.
     */
    const wireLicence = (painted: LicenceDrawn): void => {
      const { box, submit, outcome } = painted
      if (box === null || submit === null) return
      let busy = false
      /** busyNow says a save is in flight: the box and the control that sends it
       * are disabled and the section says it is busy, so a double press cannot
       * be two writes of one paste. */
      const busyNow = (now: boolean): void => {
        busy = now
        ;(surface.nodeOf(box) as unknown as { disabled: boolean }).disabled = now
        ;(surface.nodeOf(submit) as unknown as { disabled: boolean }).disabled = now
        painted.root.setAttribute('aria-busy', now ? 'true' : 'false')
      }
      const send = async (): Promise<void> => {
        if (busy) return
        const key = valueOf(surface, box)
        // An empty box is not a paste: the surface checks the same fact one
        // layer down (`licenceKey` requires a string of at least one character),
        // and a press that sent nothing would be answered with a sentence about
        // an empty string.
        if (key.trim() === '') return
        busyNow(true)
        let reply: unknown = null
        try {
          reply = await licenceBridge.saveLicence({ key })
        } finally {
          if (licenceDrawn === painted) busyNow(false)
        }
        // An answer that landed after the section was drawn again is an answer
        // about a box that is gone, for `licenceDrawn`'s reason.
        if (licenceDrawn !== painted) return
        const read = licenceSaveOutcomeOf(reply)
        if (outcome !== null) {
          surface
            .nodeOf(outcome)
            .replaceChildren(surface.nodeOf(renderLicenceOutcome(factory, read)))
        }
        if (read.saved) {
          setValueOf(surface, box, '')
          beginLicence()
        }
      }
      surface.nodeOf(submit).addEventListener('click', () => {
        void send()
      })
    }

    /** load makes the one read and draws it when it is still the latest.
     *
     * **It is RS5's streamed read** (`budget show --stream --json`, D2906/O3):
     * the repositories' rows are drawn as the feed writes them (`paintPartial`),
     * the `end` line completes the body, and the whole `budget show --json`
     * document is the fallback where the operation cannot be asked -- no
     * handler, a CLI below the feed's floor, a refusal. A feed that stopped
     * without its `end` line is R1's interrupted read: the sentence and a Retry,
     * never the rows that arrived drawn as the table. */
    const load = async (readToken: number): Promise<void> => {
      budgetOut = true
      const outcome = await readBudgetStream({
        stream: () => ops.streamBudget({}),
        budget: async () => {
          const answer = documentOf(await bridge.showBudget({}))
          // The sentence travels on the thrown value, which is how
          // `readBudgetStream` reads a refusal (`refusalSentence` takes
          // `cause.sentence`, the shape `CliSource` throws).
          if (!answer.ok) throw { sentence: answer.sentence }
          return answer.data
        },
        withheld: capsWithheld,
        onRows: (document) => {
          if (stale(mine)) return
          if (!projectsRead.current(readToken)) return
          paintPartial(document)
        },
        current: () => !stale(mine) && projectsRead.current(readToken),
      })
      // The rail's spend and the fleet gauge are the console's own budget read
      // (design section 4.1) and never the feed's: a whole-document answer fills
      // them itself, and where the feed answered -- the feed carries neither the
      // project's counted dollars nor the counters these two draw -- they are
      // read for on their own (`railBudget`). Both are filled before the
      // staleness check, because the spend is a fact about the installation
      // rather than about the panel the operator is on, and a read that landed
      // after they moved on still fills them. The document that read answers is
      // **not** held as `budgetDoc`: that one travels with `budgetStreamed`, and
      // a whole document drawn as the feed's would put back the members the feed
      // does not carry as figures (`paint`'s own note).
      if (outcome.status === 'complete') {
        if (outcome.source === 'whole') {
          rail.setSpend(railSpend(outcome.document))
          setGauge(gaugeFromBudget(outcome.document))
        } else {
          void railBudget()
        }
      }
      // The token is checked before anything is taken down, so a read from an
      // earlier visit cannot end the *current* visit's mark -- **and it is
      // checked before the in-flight flag is cleared**, because `budgetOut` is
      // about *a* budget read and not about *this* answer: a superseded answer
      // says nothing about the read that superseded it, and clearing the flag
      // here would open `repaintBody`'s guard under a mark that is still the
      // current visit's. The clearing sits below the return for that reason, and
      // with the other reads' (`managedOut`, `settingsOut`).
      const mineNow = projectsRead.settle(readToken)
      if (!mineNow) return
      budgetOut = false
      if (stale(mine)) return
      if (outcome.status !== 'complete') {
        // A read the section moved on from answers no sentence and is accounted
        // for above, for the report section's reason (`settle` asks the token
        // `current` asks).
        if (outcome.sentence === null) return
        budgetDoc = null
        bodyModel = null
        const drawn = mount(false)
        clearSection(drawn.body, drawn.slot)
        // The Retry is the budget read this section is drawn from and not the
        // whole visit: `start` would take every other section of the tab down to
        // read it again, and the six sections either side of the body answered
        // for themselves. A withheld view is drawn without one: there is no read
        // to ask again.
        refusedInto(
          drawn.body,
          refusedScreen(factory, 'budget.show', outcome.sentence, theme),
          outcome.status === 'withheld'
            ? null
            : () => {
                token = projectsRead.start()
                const retry = mount(false)
                projectsRead.mark(token, retry.body, retry.slot, PROJECTS_BUSY)
                void load(token)
              },
        )
        return
      }
      budgetDoc = outcome.document
      const at = Date.now()
      paint(budgetDoc, at, false, outcome.source === 'stream')
    }

    /** loadModels makes the second section's read, `models ls --json`. The
     * attempt is checked before anything is drawn and `stale(mine)` after it, so
     * an answer to a read the operator has already asked again -- or to one from
     * a tab they have left -- never lands under another visit. A refusal or a
     * document this build's decoder will not read leaves the section with its own
     * sentence and nothing else. */
    const loadModels = async (attempt: number): Promise<void> => {
      const answer = documentOf(await bridge.listModels({}))
      if (modelsAttempt !== attempt) return
      if (stale(mine)) return
      modelsOut = false
      models = null
      modelsSentence = null
      if (!answer.ok) {
        modelsSentence = answer.sentence
      } else {
        const decoded = decodeModelsList(answer.data, 'models.list')
        if (!decoded.ok) modelsSentence = decoded.skew.because
        else models = decoded.value
      }
      // The mark is taken down through the section helper rather than by the
      // filler: `fillProjectsModels` writes **beside** the slot, so nothing else
      // would remove the claim from the host it was made on.
      projectsModelsRead.settle(modelsToken)
      paintModels()
    }

    /** loadSettings makes the third section's read, `settings --json`. The
     * attempt check and `stale(mine)` are the models read's, one section over:
     * an answer to a read the operator has already asked again -- or to one from
     * a tab they have left -- never lands under another visit. Every answer,
     * landed or refused, is kept as it arrived and handed to the builder, which
     * is where a document this build does not read becomes the panel's own
     * sentence. */
    const loadSettings = async (attempt: number): Promise<void> => {
      const answer = documentOf(await bridge.readSettings({}))
      if (settingsAttempt !== attempt) return
      if (stale(mine)) return
      settingsOut = false
      accountKeysOut = false
      settings = null
      settingsSentence = null
      if (!answer.ok) settingsSentence = answer.sentence
      else settings = answer.data
      // The marks are taken down through the section helpers rather than by the
      // fillers, for the models read's reason: `fillProjectsSettings` writes
      // beside the slot, so nothing else would remove the claim from the host it
      // was made on. **Two** sections settle here, because two are drawn from
      // this one document (D1488): the settings panel's and the review account
      // keys'.
      projectsSettingsRead.settle(settingsToken)
      projectsAccountKeysRead.settle(accountKeysToken)
      paintSettings()
      paintAccountKeys()
    }

    /**
     * loadManaged makes the fourth and fifth sections' one read,
     * `managed status --json` (CM8 and CM9 read the same document). The attempt
     * check and `stale(mine)` are the models read's, two sections over: an answer
     * to a read the operator has already asked again -- or to one from a tab they
     * have left -- never lands under another visit.
     */
    const loadManaged = async (attempt: number): Promise<void> => {
      const answer = documentOf(await bridge.managedStatus({}))
      if (managedAttempt !== attempt) return
      if (stale(mine)) return
      managedOut = false
      managed = null
      managedSentence = null
      if (!answer.ok) managedSentence = answer.sentence
      else managed = answer.data
      projectsManagedRead.settle(managedToken)
      projectsRetirementsRead.settle(retirementsToken)
      paintManaged()
      paintRetirements()
    }

    /**
     * loadPolicy makes the policy panel's own read, `project policy show --json`
     * (CM10's flip). It has no mark of its own: the panel is a member of the
     * body's model, so what the operator sees while this read is out is the
     * body's own drawing of a document that has not arrived -- and the body is
     * repainted when it lands, which is the only drawing that can turn that panel
     * into a read one.
     *
     * A refused read is kept as a sentence and handed to the model, so the panel
     * draws the CLI's own words rather than the console's "no document arrived"
     * (design section 6.3).
     */
    const loadPolicy = async (attempt: number): Promise<void> => {
      const answer = documentOf(await bridge.policyShow({}))
      if (policyAttempt !== attempt) return
      if (stale(mine)) return
      policy = null
      policySentence = null
      if (!answer.ok) policySentence = answer.sentence
      else policy = answer.data
      repaintBody()
    }

    /**
     * loadDoctor makes the credentials service's read, `doctor --json`. The
     * document is the policy panel's checks block as well -- CM7's section reads
     * the six `policy-*` checks out of it (D1482) -- so its landing draws the
     * seventh section **and** repaints the body: one read, two sections, and
     * neither waits for the other.
     */
    const loadDoctor = async (attempt: number): Promise<void> => {
      const answer = documentOf(await bridge.environmentReport({}))
      if (doctorAttempt !== attempt) return
      if (stale(mine)) return
      doctorOut = false
      doctor = null
      doctorSentence = null
      if (!answer.ok) doctorSentence = answer.sentence
      else doctor = answer.data
      // The mark is taken down through the section helper rather than by the
      // filler, for the models read's reason: `fillCredentialsService` writes
      // beside the slot, so nothing else would remove the claim from the host it
      // was made on.
      projectsCredentialsServiceRead.settle(credentialsServiceToken)
      paintCredentialsService()
      repaintBody()
    }

    /**
     * loadLicence makes the licence key's read, `licence.read`, and draws the
     * eighth section from it. The read spawns nothing -- the console's own store
     * answers it (`src/main/licence-store.ts`) -- so it is the one read of this
     * tab that cannot fail for want of a child, and the sentence it can be
     * refused with is the console's own (a machine where the console may not
     * keep records at all).
     *
     * A save re-reads through here as well (`wireLicence`), which is what makes
     * the section's state after a paste the verifier's answer rather than the
     * form's claim.
     */
    const loadLicence = async (attempt: number): Promise<void> => {
      // `replyData` and not `documentOf`: these two operations spawn no child,
      // so their `data` is the answer itself rather than a `Snapshot` wrapping a
      // CLI document (`src/main/index.ts`'s handler map). What arrives is one
      // `LicenceReply` -- a state word and a claim set, or a sentence -- and it
      // is handed to the section unread, because the section reads its own
      // reply.
      const answer = replyData(await bridge.readLicence({}))
      if (licenceAttempt !== attempt) return
      if (stale(mine)) return
      licenceOut = false
      licence = null
      licenceSentence = null
      if (!answer.ok) licenceSentence = answer.sentence
      else licence = answer.data
      // The mark is taken down through the section helper rather than by the
      // filler, for the models read's reason: `fillLicence` writes beside the
      // slot, so nothing else would remove the claim from the host it was made
      // on.
      projectsLicenceRead.settle(licenceToken)
      paintLicence()
      // The seat estimate is drawn beside this key whatever this read answered
      // (`seatsPainted` and `!seatsOut`: the panel is on screen and no read of
      // its own is in flight), because its comparison is the key's seat count --
      // a licence that just landed is the one fact this section was missing, and
      // a licence that was **replaced** by a paste is a new figure for it.
      if (seatsPainted && !seatsOut) paintSeats()
    }

    /**
     * loadSeats makes the seat estimate's read, `seats.read`, and draws the
     * ninth section from it.
     *
     * The reply is a `Snapshot` wrapping the CLI's own document (`documentOf`),
     * and the document is handed to the builder **unread**: the section reads its
     * own reply (`buildSeats`'s three rules), so a document this build does not
     * understand is refused by the panel rather than decoded into three empty
     * tables here. That is the same door the licence section takes, and it is
     * this change's own choice: no decoder for this document exists, and a
     * section that draws two figures and two tables checks every member of them
     * itself.
     */
    const loadSeats = async (attempt: number): Promise<void> => {
      const answer = documentOf(await bridge.readSeats({}))
      if (seatsAttempt !== attempt) return
      if (stale(mine)) return
      seatsOut = false
      seats = null
      seatsSentence = null
      if (!answer.ok) seatsSentence = answer.sentence
      else seats = answer.data
      // The mark is taken down through the section helper rather than by the
      // filler, for the models read's reason: `fillSeats` writes beside the
      // slot, so nothing else would remove the claim from the host it was made
      // on.
      projectsSeatsRead.settle(seatsToken)
      paintSeats()
    }

    /** beginModels starts the second section's read and puts its mark up -- or,
     * below the floor, makes no read at all and draws the console's own sentence
     * naming the version this panel needs. It is called on every visit, the
     * budget's own read or not: a cached budget view drawn before anything is
     * awaited is not what keeps the models panel waiting. */
    const beginModels = (): void => {
      if (!modelsTooOld) {
        modelsAttempt += 1
        modelsOut = true
        modelsToken = projectsModelsRead.start()
      }
      if (frame === null) mount(true)
      else markModels()
      if (modelsTooOld) paintModels()
      else void loadModels(modelsAttempt)
    }

    /** markModels puts the second section's mark up where the frame already is.
     * The skeleton goes with the **first** read of the visit and not with a
     * re-read: content already drawn is dimmed under the mark, and a skeleton
     * over it would be the console taking a drawing away to say a drawing is
     * coming (`section.ts`'s rule for Spend's two sections). */
    const markModels = (): void => {
      if (!modelsOut || frame === null) return
      projectsModelsRead.mark(
        modelsToken,
        frame.models,
        frame.modelsSlot,
        PROJECTS_MODELS_BUSY,
        modelsPainted ? undefined : PROJECTS_MODELS_SHAPE,
      )
    }

    /** markSettings puts the third section's mark up where the frame already is.
     * As with the models section, the mark carries no skeleton: this section
     * draws none in this release (`markModels`'s rule, and C1 adds no shape of
     * its own to `skeleton.ts`). */
    const markSettings = (): void => {
      if (!settingsOut || frame === null) return
      projectsSettingsRead.mark(
        settingsToken,
        frame.settings,
        frame.settingsSlot,
        PROJECTS_SETTINGS_BUSY,
      )
    }

    /** markAccountKeys puts the sixth section's mark up, for the document the
     * settings panel beside it reads: **one read, two marks**, on two hosts, so
     * that neither section's claim can take the other's down. */
    const markAccountKeys = (): void => {
      if (!accountKeysOut || frame === null) return
      projectsAccountKeysRead.mark(
        accountKeysToken,
        frame.accountKeys,
        frame.accountKeysSlot,
        ACCOUNT_KEYS_BUSY,
      )
    }

    /** markManaged puts the fourth and fifth sections' marks up, for the one
     * document the two of them are drawn from (D1488). Neither carries a
     * skeleton: no section here draws one in this release (`markModels`'s rule).
     */
    const markManaged = (): void => {
      if (!managedOut || frame === null) return
      projectsManagedRead.mark(managedToken, frame.managed, frame.managedSlot, MANAGED_BUSY)
      projectsRetirementsRead.mark(
        retirementsToken,
        frame.retirements,
        frame.retirementsSlot,
        RETIREMENTS_BUSY,
      )
    }

    /** markCredentialsService puts the seventh section's mark up. */
    const markCredentialsService = (): void => {
      if (!doctorOut || frame === null) return
      projectsCredentialsServiceRead.mark(
        credentialsServiceToken,
        frame.credentialsService,
        frame.credentialsServiceSlot,
        CREDENTIALS_SERVICE_BUSY,
      )
    }

    /** markLicence puts the eighth section's mark up. */
    const markLicence = (): void => {
      if (!licenceOut || frame === null) return
      projectsLicenceRead.mark(licenceToken, frame.licence, frame.licenceSlot, LICENCE_BUSY)
    }

    /**
     * beginLicence starts the licence key's read and puts its mark up. It is
     * called on every visit, the budget's own read or not, for `beginModels`'s
     * reason: a cached budget view drawn before anything is awaited is not what
     * keeps this section waiting.
     *
     * **It is also what a save re-reads through** (`wireLicence`): the section is
     * the verifier's answer about the key this installation holds, so the one
     * thing a paste has to do afterwards is ask the verifier again -- there is no
     * second drawing of a save's answer, and the panel an operator is looking at
     * becomes the state their paste produced.
     */
    const beginLicence = (): void => {
      licenceAttempt += 1
      licenceOut = true
      licenceToken = projectsLicenceRead.start()
      if (frame === null) mount(true)
      else markLicence()
      void loadLicence(licenceAttempt)
    }

    /** markSeats puts the ninth section's mark up. */
    const markSeats = (): void => {
      if (!seatsOut || frame === null) return
      projectsSeatsRead.mark(seatsToken, frame.seats, frame.seatsSlot, SEATS_BUSY)
    }

    /**
     * beginSeats starts the seat estimate's read and puts its mark up -- or,
     * below the command's floor, makes no read at all and draws the console's own
     * sentence naming the version this panel needs (`beginModels`' shape). It is
     * called on every visit, the budget's own read or not, for `beginModels`'s
     * reason: a cached budget view drawn before anything is awaited is not what
     * keeps this section waiting.
     */
    const beginSeats = (): void => {
      if (!seatsTooOld) {
        seatsAttempt += 1
        seatsOut = true
        seatsToken = projectsSeatsRead.start()
      }
      if (frame === null) mount(true)
      else markSeats()
      if (seatsTooOld) paintSeats()
      else void loadSeats(seatsAttempt)
    }

    /** beginSettings starts the third section's read and puts its mark up. It
     * is called on every visit, the budget's own read or not: a cached budget
     * view drawn before anything is awaited is not what keeps the settings panel
     * waiting. The sixth section -- the review account keys, drawn from this same
     * document -- starts with it: one read, two sections, and neither may be held
     * behind the other (D1488). */
    const beginSettings = (): void => {
      settingsAttempt += 1
      settingsOut = true
      settingsToken = projectsSettingsRead.start()
      accountKeysOut = true
      accountKeysToken = projectsAccountKeysRead.start()
      if (frame === null) mount(true)
      else {
        markSettings()
        markAccountKeys()
      }
      void loadSettings(settingsAttempt)
    }

    /**
     * beginLaterSections starts the three reads the four later sections are drawn
     * from (CM10) and puts their marks up: the managed state and the retirements
     * off `managed status --json`, the credentials service off `doctor --json`,
     * and the policy panel's `policy.show` off a read that wears no mark -- it is
     * a member of the body's own model.
     *
     * It is called on every visit, the budget's own read or not, for
     * `beginModels`'s reason: a cached budget view drawn before anything is
     * awaited is not what keeps these sections waiting.
     */
    const beginLaterSections = (): void => {
      managedAttempt += 1
      managedOut = true
      managedToken = projectsManagedRead.start()
      retirementsToken = projectsRetirementsRead.start()
      policyAttempt += 1
      doctorAttempt += 1
      doctorOut = true
      credentialsServiceToken = projectsCredentialsServiceRead.start()
      if (frame === null) mount(true)
      else {
        markManaged()
        markCredentialsService()
      }
      // The licence key's read is the fifth of them (S2.7): it is started after
      // the frame's own marks for the reason every section's is -- it puts its
      // own mark up, and the visit that begins the other reads begins this one
      // too.
      beginLicence()
      // The seat estimate's read is the sixth of them (S4.2), started after the
      // frame's own marks for `beginLicence`'s reason exactly.
      beginSeats()
      void loadManaged(managedAttempt)
      void loadPolicy(policyAttempt)
      void loadDoctor(doctorAttempt)
    }

    /** start takes the sequence token for one read, puts the mark up in the
     * frame's own section when the shell is already drawn, and makes the read. */
    const start = (): void => {
      token = projectsRead.start()
      // The models section's read is taken with the budget's and in the same
      // turn, and the settings section's with both, and the four later sections'
      // three with all of them: the reads are independent documents, and asking
      // for one after the other answered would be the serialisation the owner's
      // rule of 2026-10-31 forbids. Below the floor there is no models read at
      // all; no other read here has a floor.
      if (!modelsTooOld) {
        modelsAttempt += 1
        modelsOut = true
        modelsToken = projectsModelsRead.start()
      }
      settingsAttempt += 1
      settingsOut = true
      settingsToken = projectsSettingsRead.start()
      accountKeysOut = true
      accountKeysToken = projectsAccountKeysRead.start()
      managedAttempt += 1
      managedOut = true
      managedToken = projectsManagedRead.start()
      retirementsToken = projectsRetirementsRead.start()
      policyAttempt += 1
      doctorAttempt += 1
      doctorOut = true
      credentialsServiceToken = projectsCredentialsServiceRead.start()
      // The licence key's read is taken with the rest (S2.7). It is started
      // **before** the frame is mounted, so the shell's own mount puts its mark
      // up with the others' -- the one difference being that it is the only read
      // here that answers out of the console rather than out of a child.
      licenceAttempt += 1
      licenceOut = true
      licenceToken = projectsLicenceRead.start()
      // The seat estimate's read is taken with the rest (S4.2). It is the tab's
      // one read whose child lists the installation's bucket, so it is the one
      // that answers last -- and taking its token here is what lets its panel
      // draw the moment it does, rather than behind the budget's read. Below the
      // command's floor there is no seats read at all (`seatsTooOld`): the panel
      // is then drawn by the same branch the models panel uses below its own.
      if (!seatsTooOld) {
        seatsAttempt += 1
        seatsOut = true
        seatsToken = projectsSeatsRead.start()
      }
      const fresh = frame === null
      const drawn = mount(true)
      if (!fresh) {
        projectsRead.mark(token, drawn.body, drawn.slot, PROJECTS_BUSY)
        markModels()
        markSettings()
        markAccountKeys()
        markManaged()
        markCredentialsService()
        markLicence()
        markSeats()
      }
      void load(token)
      if (modelsTooOld) paintModels()
      else void loadModels(modelsAttempt)
      void loadSettings(settingsAttempt)
      void loadManaged(managedAttempt)
      void loadPolicy(policyAttempt)
      void loadDoctor(doctorAttempt)
      void loadLicence(licenceAttempt)
      if (seatsTooOld) paintSeats()
      else void loadSeats(seatsAttempt)
    }

    // The view this screen opens on, if this session has already read it, is
    // drawn before anything is awaited -- which is the whole point of the cache.
    // A view older than `CACHE_FRESH_MS` is stamped **and** read again at once
    // rather than served silently.
    if (held === null) start()
    else {
      paint(held.value, held.at, true)
      if (Date.now() - held.at >= CACHE_FRESH_MS) start()
      else {
        beginModels()
        beginSettings()
        beginLaterSections()
      }
    }

    // R2: the scope a settings write names, registered by the visit that owns it.
    // The clear is this session's cached document for the tab; the reads are the
    // body's own -- the budget read, which is the document the cache holds -- and
    // **the settings section's**, whose every row is a figure a write can change.
    //
    // It is deliberately not `start()`: that would re-read every section of the
    // tab, and the six others (`models ls`, the managed state, `policy.show`,
    // `doctor`, the licence key and the seat estimate) read documents a settings
    // write cannot move. The settings section's own read is what makes the row
    // the operator wrote show the value the CLI now reports rather than the one
    // from before the write (SU2b).
    invalidator.remember('projects', {
      clear: () => projectsViews.clear(),
      reread: () => {
        // A visit the operator has left has nothing to read: the registration is
        // the visit's own, and the next visit reads the tab on entry.
        if (stale(mine)) return
        token = projectsRead.start()
        const drawn = mount(false)
        projectsRead.mark(token, drawn.body, drawn.slot, PROJECTS_BUSY)
        void load(token)
        beginSettings()
      },
    })
  }

  // ---- Coordinator ------------------------------------------------------

  /** coordinatorRead is the Coordinator tab's **one** section read: the map of
   * the project's active orchestrators (`orchestrator map --json`, CG7b/D3210).
   *
   * **One read and one section.** The v2 tab's four reads -- the stored view,
   * the fleet window, the job list and the transcript -- are gone with their
   * sections (D3210): the map document is the whole of what this tab draws, and
   * a section of its own per figure would be four marks over one read. The
   * drill-down below is this section's own second read, and it is the one place
   * a mark is put up without the section being re-read. */
  const coordinatorRead = sectionRead({ factory, theme })

  /** coordinator is the screen and the layout worker it owns, built on first use
   * and kept for the session: a worker is a process, so the console builds one
   * when the tab is first opened and disposes of it when it moves to another
   * installation (`forgetLive`). */
  let coordinator: CoordinatorScreen | null = null
  const coordinatorNow = (): CoordinatorScreen => {
    if (coordinator === null) coordinator = coordinatorScreen(factory, theme)
    return coordinator
  }

  /** leaveCoordinator ends the read's busy state when the operator leaves the
   * tab: a mark and a fifteen-second line are for the map on screen, and a read
   * that landed after it was left would draw into a panel nothing is showing. */
  leaveCoordinator = () => {
    coordinatorRead.end()
  }

  /** drawCoordinator puts one drawing on the panel and wires its own controls.
   * It answers nothing for a drawing that was superseded -- a read whose visit
   * the operator has left, or an earlier answer to a question already asked
   * again -- which is what keeps a slow map from landing over a later one. */
  const drawCoordinator = (screen: CoordinatorScreen, mine: number, drawn: Drawn | null): void => {
    if (drawn === null || stale(mine)) return
    replace(drawn)
    bindCoordinator(screen, mine, drawn)
  }

  /** drawCoordinatorRefused draws the section with a refused map read: the CLI's
   * or the decoder's own sentence under the header, and the section's Retry --
   * the one control that reads the map again. It is a refusal of **this**
   * section and not of the tab, so the header and the source line stay up. */
  const drawCoordinatorRefused = (
    screen: CoordinatorScreen,
    mine: number,
    sentence: string,
  ): void => {
    const drawn = screen.noticed(sentence)
    if (drawn === null || stale(mine)) return
    const slot = classed(drawn, BUSY_SLOT_CLASS)[0]
    if (slot !== undefined) {
      const retry = renderRetry(factory)
      appendTo(slot, retry)
      surface.nodeOf(retry).addEventListener('click', () => {
        if (!stale(mine)) showCoordinator()
      })
    }
    replace(drawn)
    bindCoordinator(screen, mine, drawn)
  }

  /** loadCoordinatorTasks reads one orchestrator's own items for the drill-down:
   * `graph.read --source orchestrator:<id>`, a scope CG7a's grammar already
   * admits, so this press adds no word to either allow-list.
   *
   * The selection is checked again after the await, because the operator can
   * open a second box while the first read is out: an answer for a panel that is
   * no longer open is dropped rather than drawn over the one that is.
   *
   * The scope is handed to `coordinatorTasks` as well as to the read, because
   * the document's own `sources[]` row for it is what tells an orchestrator
   * holding nothing from one whose stream could not be read (H3). */
  const loadCoordinatorTasks = async (
    screen: CoordinatorScreen,
    mine: number,
    id: string,
  ): Promise<void> => {
    const scope = `orchestrator:${id}`
    const answer = documentOf(await bridge.graphRead({ source: scope }))
    if (stale(mine) || screen.current.selected !== id) return
    if (!answer.ok) {
      drawCoordinator(screen, mine, screen.tasksRefused(answer.sentence))
      return
    }
    const decoded = decodeGraphDocument(answer.data)
    if (!decoded.ok) {
      drawCoordinator(screen, mine, screen.tasksRefused(decoded.skew.because))
      return
    }
    const read = coordinatorTasks(decoded.value, scope)
    drawCoordinator(
      screen,
      mine,
      read.kind === 'refused' ? screen.tasksRefused(read.sentence) : screen.tasksDrawn(read.tasks),
    )
  }

  /** openCoordinatorTasks opens one orchestrator's panel: the press is answered
   * in the frame it happened in -- the panel with its own mark -- and the read
   * follows. */
  const openCoordinatorTasks = (screen: CoordinatorScreen, mine: number, id: string): void => {
    drawCoordinator(screen, mine, screen.change(id))
    void loadCoordinatorTasks(screen, mine, id)
  }

  /** bindCoordinator wires one drawing's own controls: every box's press and
   * Enter open that orchestrator's live tasks, and the panel's one control closes
   * it.
   *
   * It is called for **every** drawing rather than once, because a drawing is a
   * new tree each time: the boxes and the close control belong to the tree that
   * was drawn, and a listener bound to the last one is a control that stopped
   * answering. */
  const bindCoordinator = (screen: CoordinatorScreen, mine: number, drawn: Drawn): void => {
    for (const box of classed(drawn, COORDINATOR_NODE_CLASS)) {
      const id = box.getAttribute(COORDINATOR_ATTRIBUTE)
      if (id === null) continue
      const node = surface.nodeOf(box)
      node.addEventListener('click', () => {
        openCoordinatorTasks(screen, mine, id)
      })
      keydownOn(node, (event: KeyEventLike) => {
        if (event.key !== 'Enter' && event.key !== ' ') return
        event.preventDefault()
        openCoordinatorTasks(screen, mine, id)
      })
    }
    const close = classed(drawn, COORDINATOR_TASKS_CLOSE_CLASS)[0]
    if (close !== undefined) {
      surface.nodeOf(close).addEventListener('click', () => {
        drawCoordinator(screen, mine, screen.change(null))
      })
    }
  }

  /** loadCoordinatorMap reads the map and draws it: the document is the
   * installation's live state, so a visit reads it rather than serving one from
   * the session (`CoordinatorScreen.forget` drops the last drawing as the visit
   * opens). */
  const loadCoordinatorMap = async (
    screen: CoordinatorScreen,
    mine: number,
    token: number,
  ): Promise<void> => {
    const answer = documentOf(await bridge.orchestratorMap({}))
    // The token goes first: the mark this panel wears may belong to a later
    // visit, and `settle` would take that one down with it.
    if (!coordinatorRead.settle(token)) return
    if (stale(mine)) return
    if (!answer.ok) {
      drawCoordinatorRefused(screen, mine, answer.sentence)
      return
    }
    const decoded = decodeOrchestratorMap(answer.data)
    if (!decoded.ok) {
      drawCoordinatorRefused(screen, mine, decoded.skew.because)
      return
    }
    drawCoordinator(screen, mine, await screen.load(decoded.value))
  }

  /**
   * showCoordinator opens the tab: the frame is drawn in the click's own turn
   * with the section's mark over it, and the map is read behind it.
   *
   * **Below the version floor the tab draws a sentence and spawns nothing**
   * (F40). `orchestrator map` is registered from `ee/`, so an older binary
   * answers `unknown command`: the tab names the CLI the read needs rather than
   * handing a child a command that does not exist and drawing a refusal about the
   * wrong thing. The same floor covers the drill-down's named scope, because the
   * release that carries the map is the one that first reads a named
   * orchestrator.
   *
   * **It registers the tab's own scope with the invalidator** (R2): a write that
   * moves what this screen shows re-reads the map, so a drawing kept under a
   * write the operator made elsewhere is never left standing as if it were
   * fresh.
   */
  const showCoordinator = (): void => {
    markCurrent('coordinator')
    const mine = claim()
    const version = gate.compatibility.cliVersion ?? ''
    if (!orchestratorMapSupported(version)) {
      replace(renderCoordinatorFloor(factory, theme, version))
      return
    }
    const screen = coordinatorNow()
    // No history (D3210): the map is what is being driven now, so the visit
    // reads it. `forget` drops the last drawing rather than leaving it under the
    // mark, where an old box would be a figure the operator cannot tell from a
    // fresh one.
    screen.forget()
    const frame = screen.frame()
    const slot = classed(frame, BUSY_SLOT_CLASS)[0]
    replace(frame)
    const token = coordinatorRead.start()
    if (slot !== undefined) {
      coordinatorRead.mark(token, frame, slot, COORDINATOR_BUSY, COORDINATOR_SHAPE)
    }
    invalidator.remember('coordinator', {
      clear: () => undefined,
      reread: () => {
        if (stale(mine)) return
        void loadCoordinatorMap(screen, mine, coordinatorRead.start())
      },
    })
    void loadCoordinatorMap(screen, mine, token)
  }
  // ---- Run detail -------------------------------------------------------

  /** runRecordRead and runLogsRead are the Run screen's two section reads: each
   * has its own busy mark, its own sequence token and its own refusal, so a slow
   * log window cannot take the record's mark down and one refused read cannot
   * hide the other section's answer. */
  const runRecordRead = sectionRead({ factory, theme })
  const runLogsRead = sectionRead({ factory, theme })

  /** leaveRun ends both reads' busy state when the operator leaves the run: a
   * mark and a fifteen-second line are for the run on screen, and a read that
   * landed after it was left would draw into a column nothing is showing. */
  leaveRun = () => {
    runRecordRead.end()
    runLogsRead.end()
  }

  /**
   * showRun opens one run: its own record (`diagnose RUN --json`, which draws
   * the header's figures, the timeline, the ledger and the diagnosis) and the
   * capped log window (`logs RUN --json`, with `logs RUN --url` beside it).
   *
   * **The frame is drawn in the click's own turn** (the owner's complaint of
   * 2026-11-01: "when I click on a job id, I see no feedback, it's as if the
   * click didn't register, only seconds later do I see the job details"). Every
   * one of the three reads used to be awaited before anything was drawn, so the
   * click answered with the previous screen until the last of them landed; now
   * the id, the way back to Live and the two sections' skeletons are drawn
   * before the first `await`, and each section is filled when *its* read
   * answers.
   *
   * **The two reads are independent and are made in parallel.** Neither section
   * waits for the other: the record's answer fills the header, the timeline, the
   * ledger and the diagnosis, the log window's fills the log section and the
   * arrivals under it, and a refusal names the operation it came from and is
   * drawn in its own section -- `run.diagnose` over the record's own region,
   * `run.logs` in the log section -- rather than taking the whole screen down.
   * Handing the screen `null` instead would draw an empty log window and a
   * ledger of `n/a`, which reads as "this run logged nothing and spent nothing"
   * when what happened is that the console could not ask (Global Constraint 5).
   *
   * The **link is the log section's own second read**: `logs RUN --url` refuses
   * for a run whose transcript is not linked, which is a fact about the link and
   * not a reason to hide a run that read perfectly well -- `buildRunModel` draws
   * its own sentence for a link that did not arrive. It is made beside the other
   * two and never before them, and a link that lands after the window does
   * repaints that one section, because the sentence for a link that did not
   * arrive must not stand once it has.
   */
  const showRun = (run: string): void => {
    // A run is a view like any other: the navigation marks none of its buttons
    // while it is up, and the Live poll stops -- a poll that kept running under
    // it would replace the run the operator opened a second later.
    markCurrent('run')
    backToView = () => showRun(run)
    const mine = claim()
    const drawn = renderRunFrame(factory, theme, run)
    replace(drawn.root)
    // The breadcrumb's `Live` crumb is the way back from a run the operator
    // opened, and it is the whole of what this screen wires: the same
    // `show('live')` the nav's own Live button calls, and the run id beside it
    // is selectable text rather than a control. Nothing else on Run binds
    // anything -- the retry is `writes.ts`'s refused button. It is bound here
    // and not when a read lands, because the frame the click drew carries it.
    const crumb = classed(drawn.root, 'run-crumb-live')[0]
    if (crumb !== undefined) {
      surface.nodeOf(crumb).addEventListener('click', () => {
        show('live')
      })
    }

    /** what the two reads have drawn so far, held here so `placeMain` can write
     * the column from whatever has landed, in whatever order it landed in. */
    let before: readonly Drawn[] = []
    let diagnosisDrawn = false
    let arrivals: readonly Drawn[] = []
    let window_: Readonly<Record<string, unknown>> | null = null
    let logLink: string | null = null

    /**
     * placeMain writes the two hosts' children in the screen's own order.
     *
     * **It replaces them rather than appending to them**, because the two reads
     * answer in an order nobody chooses: the log window's read can land first,
     * and the record's panels belong *above* the diagnosis. A column built by
     * appends would have put the timeline under the log window for every
     * operator whose log read answered first, and the two answers arrive from
     * the same CLI within a second of each other.
     *
     * The frame draws both hosts empty and `showRun` calls this once as soon as
     * the two marks are up: the sections and the slots the marks go into are the
     * frame's, and this is what writes them into the page in their order. The
     * record's children go into the record's own host, which is what its busy
     * mark is on -- the log window is the column's other child and is not inside
     * the region that mark dims and announces.
     */
    const placeMain = (): void => {
      const parts = { before, diagnosis: diagnosisDrawn, arrivals }
      const record = runRecordChildren(drawn, parts).map((each) => surface.nodeOf(each))
      const column = runMainChildren(drawn, parts).map((each) => surface.nodeOf(each))
      surface.nodeOf(drawn.recordHost).replaceChildren(...record)
      surface.nodeOf(drawn.main).replaceChildren(...column)
    }

    // Both tokens are the section's **current** one rather than the visit's, so
    // the section's own Retry can take a new one and read again (R1): the two
    // reads capture the token they were started under before they await, which
    // is what keeps a slow first answer from settling the token of the read that
    // replaced it.
    let recordToken = runRecordRead.start()
    let logsToken = runLogsRead.start()
    runRecordRead.mark(recordToken, drawn.recordHost, drawn.mainSlot, runBusyLabel(run, RUN_RECORD_BUSY), RUN_RECORD_SHAPE)
    runLogsRead.mark(logsToken, drawn.logs, drawn.logsSlot, runBusyLabel(run, RUN_LOGS_BUSY), RUN_LOGS_SHAPE)
    placeMain()

    /** paintLogs fills the log section from what has landed. It is called by
     * both of that section's reads: the window's own, and the link's when it
     * arrives after it. */
    const paintLogs = (): void => {
      if (window_ === null) return
      clearSection(drawn.logs, drawn.logsSlot)
      arrivals = fillRunLogs(factory, buildRunModel({ diagnosis: null, logs: window_, logLink }), drawn)
      placeMain()
    }

    const loadRecord = async (): Promise<void> => {
      const readToken = recordToken
      const answer = documentOf(await bridge.diagnoseRun({ run }))
      // The token goes first: the mark this host wears may belong to a later
      // visit, and `settle` would take that one down with it.
      if (!runRecordRead.settle(readToken)) return
      if (stale(mine)) return
      // The mark the read wore comes out of the host's slot, and the refusal,
      // when there is one, is drawn in its place: the host has no section of
      // its own until the record answers, and a refusal is not a section.
      surface.nodeOf(drawn.mainSlot).replaceChildren()
      if (!answer.ok) {
        // Only the record's own parts come down here. `arrivals` is the log
        // read's -- the replay panel drawn with the window it replays -- and a
        // refused diagnosis has no say over what that read already put on
        // screen: the two reads answer in an order nobody chooses, and this one
        // refusing is not a reason to take the other's panel away.
        before = []
        diagnosisDrawn = false
        // The section's own read, and the section's own token with it: this
        // screen had no press for a refusal at all before it (F21), and the
        // breadcrumb back to Live is not one.
        refusedInto(
          drawn.mainSlot,
          refusedScreen(factory, 'run.diagnose', answer.sentence, theme),
          () => {
            recordToken = runRecordRead.start()
            runRecordRead.mark(
              recordToken,
              drawn.recordHost,
              drawn.mainSlot,
              runBusyLabel(run, RUN_RECORD_BUSY),
            )
            void loadRecord()
          },
        )
        placeMain()
        return
      }
      before = fillRunRecord(factory, buildRunModel({ diagnosis: answer.data, logs: null, logLink: null }), drawn).before
      diagnosisDrawn = true
      placeMain()
    }

    const loadLogs = async (): Promise<void> => {
      const readToken = logsToken
      const answer = documentOf(await bridge.readRunLogs({ run }))
      if (!runLogsRead.settle(readToken)) return
      if (stale(mine)) return
      if (!answer.ok) {
        window_ = null
        arrivals = []
        clearSection(drawn.logs, drawn.logsSlot)
        // This section's own read again, on the record's terms above.
        refusedInto(
          drawn.logsSlot,
          refusedScreen(factory, 'run.logs', answer.sentence, theme),
          () => {
            logsToken = runLogsRead.start()
            runLogsRead.mark(logsToken, drawn.logs, drawn.logsSlot, runBusyLabel(run, RUN_LOGS_BUSY))
            void loadLogs()
          },
        )
        placeMain()
        return
      }
      window_ = answer.data
      paintLogs()
    }

    const loadLink = async (): Promise<void> => {
      const answer = replyData(await bridge.runLogLink({ run }))
      if (stale(mine)) return
      if (answer.ok && typeof answer.data === 'string') logLink = answer.data
      // A link that arrived after the window did redraws that section: the note
      // saying no link arrived is the *window's* sentence, and it is not true
      // any more.
      paintLogs()
    }

    // All three in the click's own turn, so they are in flight together and the
    // sections fill as their own answers land.
    void loadRecord()
    void loadLogs()
    void loadLink()
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
      // R2: a launch is a write, and the tab it was made in is the one that
      // shows the run it started. The Live scope is cleared and read again at
      // once -- the poll's own read, made now rather than at the next tick --
      // because the frame on screen is the one from **before** the run existed.
      invalidator.invalidate(['live'])
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
   * installation, so nothing to read and no Live refusal in its place. The
   * wizard's own two operations and its clipboard are handed in here, where the
   * console's door to them (`ops`) and the window's navigator are. */
  const showOnboarding = (onboarding: RailOnboarding): void => {
    markCurrent('onboarding')
    rail.view(null)
    replace(
      drawOnboarding(surface, onboarding, {
        ops,
        copy: clipboardCopy(navigator),
      }),
    )
  }

  /** backToView is the call that draws the screen the main area is showing
   * again. The five tabs are `show`'s, and the two screens that are not tabs --
   * an installation's issue and a run's detail -- record their own, because
   * neither is reachable by a view id.
   *
   * **The Add project screen is its one reader** (SW3c, R4): it is drawn in
   * place of the current view and not beside it, so the way back is remembered
   * here rather than recomputed, and "back to the current installation" means
   * the screen the operator was standing on. */
  let backToView: () => void = () => show('live')

  /** addProjectReturn is that call as the Add project door found it: the door
   * remembers the screen it replaced rather than reading `backToView` when
   * Cancel is clicked, because anything that drew in between would have moved
   * it. */
  let addProjectReturn: () => void = () => show('live')

  /** refreshRail is the rail's list read again, after a setup run ended: the
   * installation the wizard created is a row now, and the rail is the list that
   * has to carry it (SW3c, R4). The **rows** are re-read and nothing else -- the
   * switcher's line and the header's figures are about the installation the
   * console is reading, which a second installation does not change. A read that
   * fails leaves the rows the console has: it is the same rule `currentRailRows`
   * keeps for the first paint, and a console that blanked its rail for a failed
   * list read would hide the installation it is working in. */
  const refreshRail = async (): Promise<void> => {
    const listed = railRows(await deps.railBridge.listInstallations({}))
    if (listed === null) return
    railNow = listed
    deps.rail.setRows(listed)
  }

  /** showAddProject puts the wizard in the main area, in place of the
   * installation the console is reading (SW3c, R4). It is the rail's second foot
   * door's whole behaviour and the destination of the picker's Add project
   * operation, and both are the same screen: `drawAddProject` draws one heading
   * of its own over SW3b's wizard, and Cancel hands the main area back to the
   * view this was opened from.
   *
   * **The setup run is main's** (SW3a) and this file starts nothing: the wizard's
   * two operations go through the console's own door (`ops`), which is the
   * dashboard document's -- the picker window cannot ask them, which is why the
   * door hands over a request rather than drawing this screen itself. */
  const showAddProject = (): void => {
    if (currentView !== 'add-project') addProjectReturn = backToView
    markCurrent('add-project')
    rail.view(null)
    replace(
      drawAddProject(surface, {
        ops,
        copy: clipboardCopy(navigator),
        cancel: () => {
          addProjectReturn()
        },
        applied: () => {
          void refreshRail()
        },
      }),
    )
  }

  /** forgetLive drops what the Live screen held for the installation the
   * console was open on, so the next one is read afresh rather than drawn from
   * the frame and the burn history of an installation that is gone. */
  const forgetLive = (): void => {
    live.drawn = null
    // The prewarm queue belonged to the installation being left, and the caches
    // it filled are dropped below with the rest of that installation's state:
    // the next one is read afresh, which is a queue of its own.
    prewarmed = false
    live.rigor = new Map()
    live.rigorAt = 0
    history = new BurnHistory()
    hourHistory = new HourHistory()
    // Spend's two documents and the views read from them are this
    // installation's: the caps are its budget, and a cached view carries its
    // figures. The next installation is read afresh rather than drawn from
    // another one's spend.
    spendBudget = null
    spendCaps = null
    spendCapsSentence = null
    spendCapsWithheld = null
    // **Every cache goes through the invalidator** (R2's other half): the four
    // session caches and the Live join are the scopes the tabs registered, and
    // `forget` is the installation switch -- nothing is read here, because the
    // console is about to close every read it holds and the first visit to a tab
    // reads it.
    invalidator.forget()
    // The graph is a picture of **one installation's** plan, laid out by a worker
    // this console started for it: both are dropped here, so the next
    // installation is drawn from its own document by its own engine rather than
    // from positions computed for a checkout that is gone.
    graph.screen?.dispose()
    graph.screen = null
    // The Coordinator's screen goes the same way and for the same reason: its
    // map is one installation's active orchestrators, laid out by a worker this
    // console started for it, so the next installation is drawn from its own
    // document by its own engine.
    coordinator?.dispose()
    coordinator = null
    // And the move in hand is that installation's too: the request a confirm
    // would send, the answer an undo inverts and the two gestures in flight are
    // all about the record the console just left -- a drag whose `dragend` never
    // came would otherwise make the next drop move this installation's key. The
    // turn is taken as well, so a preview still reading for the old record draws
    // nothing over the drawing that replaces it.
    moveFlow = null
    takeMoveTurn()
    dragging = null
    choosing = null
    graph.readAt = 0
    // The scope is the same kind of fact: the plan scope's document belongs to
    // the installation that is gone, so the next one opens on the scope the
    // console has always opened on -- and `chosen` goes with it, because the
    // fallback of H9 is this console's own decision about a scope nobody has
    // picked yet. A sentence about a read the old installation was refused is
    // not a sentence about this one.
    graph.scope = GRAPH_SCOPE_PLAN
    graph.chosen = false
    graph.notice = null
    graph.fellBack = false
    // The feed is the old installation's project, and the main process has
    // already stopped it as part of the switch (`sourceFor`'s own `close`): what
    // this clears is the entry's memory of holding one, so the next tab visit
    // starts a child for the installation that is now open.
    graph.watching = false
    graph.feedInFlight = false
    graph.runs = []
  }

  /** showIssue puts one installation's issue in the main area (owner's design
   * ruling of 2026-10-25): a view like Run detail, with no navigation button
   * and no Live poll under it. */
  const showIssue = (row: RailRow, focus = false): void => {
    markCurrent('issue')
    backToView = () => showIssue(row)
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
   * was about while it still has one.
   *
   * `stay` is the Graph card's own write: the chooser is drawn in the Graph, so
   * its answer is the Graph and not the issue view the rail's own actions leave
   * for. The rail is refreshed either way -- the row's checkout is what the card
   * was drawn from -- and only the walk away from the view is skipped. */
  const answered = (operation: string, reply: unknown, project: string, stay = false): void => {
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
    if (stay) return
    const still = next.find((row) => row.project === project)
    if (still !== undefined && still.issue !== 'none') {
      showIssue(still)
      return
    }
    leaveIssue()
  }

  /** useCheckout records a directory for an installation and reads the plan
   * again. It is the Graph card's own write -- the one place a checkout is set
   * from inside a view -- and the read after it is what turns the card back into
   * the picture: the write is only recorded here, and the plan is what says
   * whether the directory the CLI re-validated is one the Graph can read.
   *
   * **The generation is taken before the write, and both answers are dropped
   * when it is stale.** The write is answered after the pairing probe, which
   * takes seconds, and the operator can move to another tab in that time: the
   * read and the refusal both belong to the Graph, so neither is drawn once the
   * Graph is not the view on screen -- exactly as `readGraph` guards its own
   * answer. The read that follows the write is held to `mine` rather than to a
   * claim taken after it, so what it draws is the generation this press took:
   * a plan read that was already in flight when the checkout changed cannot land
   * under it, and one the operator has left since is dropped like any other. */
  const useCheckout = (project: string, checkout: string): void => {
    const mine = claim()
    void railBridge.setInstallationCheckout({ project, checkout }).then((reply) => {
      if (stale(mine) || currentView !== 'graph') return
      const accepted = replyData(reply).ok
      answered('installation.checkout', reply, project, true)
      if (accepted) void readGraph(mine, null)
    })
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

  // ---- The prewarm queue (PW1, D2906) -----------------------------------

  /**
   * prewarmGraph reads the Graph tab's primary section -- the plan the canvas is
   * drawn from, and the run list a node's door comes from -- and caches the pair
   * `readGraph` and `decorateRuns` cache, under the same key.
   *
   * **The plan scope is the one scope it reads, and H9's fallback is not made
   * from here.** A console that opens on the coordinator scope because the plan
   * of its installation cannot be read is answering a drawing the operator is
   * looking at; a tab nobody is on has no drawing to save, so a refused plan
   * leaves the Graph cold and its own visit makes the read and, if it is that
   * refusal, the trip.
   *
   * **A read that fails caches nothing at all.** A view served from this cache
   * is stamped and, while it is younger than `CACHE_FRESH_MS`, is not re-read
   * (`showGraph`), so half a Graph -- a canvas with no doors, cached out of a run
   * list that was refused -- would be a picture the operator's own visit draws
   * and does not correct.
   *
   * **It reads the canvas's own argv** (`canvasRead`, IR4): what the queue warms
   * must be what a visit draws, or the warm paint would be a document the visit's
   * own read never makes -- a merges-inclusive plan cached out of a read the
   * first paint exists to avoid, and a merge section with nothing to do. The
   * queue runs while the operator is on another tab, with no drawing in hand, so
   * its read is the first paint's own (`mergesFollow`) and carries it into the
   * cache: the visit that draws it is then the one that starts the merge section.
   */
  const prewarmGraph = async (still: () => boolean): Promise<void> => {
    const read = canvasRead()
    const plan = documentOf(await bridge.planStatus(read.args))
    if (!plan.ok) return
    const decoded = decodePlanStatus(plan.data, 'plan.status')
    if (!decoded.ok) return
    const listed = documentOf(await bridge.listRuns({ since: RUNS_SINCE }))
    if (!listed.ok) return
    if (!still()) return
    graphViews.put(
      graphKey(),
      { document: decoded.value, runs: runIdsIn(listed.data), merged: !read.mergesFollow },
      Date.now(),
    )
    // **The cadence is stamped too**, and it is not the cache's own age: the age
    // says what the *view* is worth, and `graph.readAt` is what
    // `maybeRefreshGraph` measures the next read from. A cached Graph whose
    // entry was not read in this tab would leave the tab reading the plan again
    // on the first tick it is entered -- the warm view served and then
    // immediately replaced, which is what the queue exists to avoid.
    graph.readAt = Date.now()
  }

  /**
   * prewarmSpend reads the Spend tab's primary section: the report, with the
   * three controls a visit opens on (`SPEND_DEFAULT_CONTROLS` -- `showSpend`
   * starts there on every visit, so this key is the one the operator's first
   * visit looks up).
   *
   * **It is the tab's own streamed read** (`readReportStream`, RS5's path), so
   * what the queue warms is what a visit warms: where the feed cannot be asked
   * -- no handler, a CLI below the feed's floor, a refusal -- that same read is
   * the whole `report --json` document, and the model is built from it either
   * way. The caps are handed over exactly as the visit hands them over, for the
   * model's own `caps` field; the caps themselves are their own section and
   * their own read, and no cache holds them.
   */
  const prewarmSpend = async (still: () => boolean, project: string): Promise<void> => {
    const outcome = await readReportStream({
      stream: (args) => ops.streamSpend(args),
      report: reportReader,
      controls: SPEND_DEFAULT_CONTROLS,
      caps: spendBudget,
      project,
      withheld: hidesView(gate.compatibility, 'spend'),
    })
    if (outcome.status !== 'complete') return
    if (still()) spendViews.put(spendViewKey(project, SPEND_DEFAULT_CONTROLS), outcome.model, Date.now())
  }

  /**
   * prewarmProjects reads the Projects tab's primary section: the whole
   * `budget show --json` document, which is the one thing that tab's cache
   * holds. **It is the whole document and not `budget show --stream`**, because
   * a streamed view is deliberately never cached (RS5): a cache hit is drawn as
   * a whole document, and the members the feed does not carry would come back as
   * absences the console could not account for. So the read that fills this
   * cache is the document's own, exactly as the visit's fallback makes it -- and
   * where that read is refused this tab stays cold and its own visit draws the
   * refusal.
   */
  const prewarmProjects = async (still: () => boolean, project: string): Promise<void> => {
    const answer = documentOf(await bridge.showBudget({}))
    if (!answer.ok) return
    if (still()) projectsViews.put(cacheKey([project, 'projects']), answer.data, Date.now())
  }

  /**
   * startPrewarm is the queue the Live screen's first paint leaves behind: after
   * the tab on screen has been drawn, the console reads each **other** tab's
   * primary section into its cache, one read at a time, in the order the
   * navigation offers them (`prewarm`, `src/renderer/prewarm.ts`; PW1, D2906).
   * The operator's first switch to one of those tabs then draws a view this
   * session already read -- stamped `as of`, like any other cached view -- and
   * re-reads it in the background on the cache's own rule.
   *
   * **The generation is read here and not claimed.** A door claims when the
   * view changes; this one does not change it, and claiming would cancel the
   * read the operator's own press started a moment before -- a switch landing
   * between that press and this tick's paint would draw nothing at all. What the
   * queue holds is the generation it saw, and `still` is how it asks whether
   * that is still the view on screen: every tab switch and every installation
   * switch claims, so one check answers for both.
   *
   * **Live is not in the list**, and there is nothing for it to be: the tab on
   * screen is the one this queue follows, its model is the poll's own read, and
   * the feed the other views read is not something a second reader warms. The
   * Coordinator is not in it either -- it keeps no session cache at all, so
   * there is no view to warm (`showCoordinator`).
   *
   * **Every first paint starts it, a refusal included**: a console whose own
   * feed could not be read has still drawn its active tab, and the tabs it is
   * not showing are sections of their own -- so `drawLive` starts the queue on
   * that door too, and what a reduced console or one whose CLI version is
   * refused does instead is read by `prewarm` itself, which asks the mode.
   */
  startPrewarm = (): void => {
    if (prewarmed) return
    prewarmed = true
    const watching = generation
    const still = (): boolean => generation === watching
    // The project is the open row's, read once: an installation switch moves the
    // generation, which is what stops the queue -- so this name is either the
    // installation the queue was started for or a read that caches nothing.
    const project = railNow.find((row) => row.open)?.project ?? ''
    const tabs: readonly PrewarmTab[] = [
      { id: 'graph', read: () => prewarmGraph(still) },
      { id: 'spend', read: () => prewarmSpend(still, project) },
      { id: 'projects', read: () => prewarmProjects(still, project) },
    ]
    void prewarm({ mode: gate.compatibility.mode, active: currentView, still, tabs })
  }

  // ---- The first read ---------------------------------------------------

  // The first read is not a poll: the screen is drawn once whatever the window's
  // visibility is, so a console that opened behind another window still has the
  // figures it read. The interval is what pauses. A console open on an
  // installation it cannot read draws that installation's issue instead and
  // reads nothing (the owner's ruling: "If NO installation is readable, show the
  // issue view of the first one in the main area").
  const opened = railNow.find((row) => row.open)
  // The picker's Add project door comes first when it was this window's door
  // (SW3c): the operator asked for a new project, so the wizard is what they are
  // shown -- over the installation's issue and over the Live read alike, both of
  // which Cancel brings back.
  if (deps.addProject === true) showAddProject()
  else if (opened !== undefined && !opened.readable) showIssue(opened)
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
