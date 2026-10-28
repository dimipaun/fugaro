// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The confirmation window's preload: the *only* thing that window's document can
// do (design section 5.1 rule 11; console V2.5.1a part 2b-1).
//
// **Two methods, and that is the whole surface**: `fields()`, which reads the
// display-only view this window was opened for, and `answer(true|false)`, which
// is the operator's answer. There is no read of the CLI, no spawn, no
// `openExternal`, no `installation.*` row and no channel of the dashboard's: the
// window shows the task and asks one question, and everything else it might want
// is something it must not have.
//
// It is a third preload rather than a second document on one of the others for
// the reason `src/preload/installations.ts` gives: the dashboard's preload
// exposes twenty-four rows and the picker's six, and this document has no
// business with any of them. Every document of the bundle is served from the
// same origin (`app://console`), so the only thing that separates their surfaces
// is that each window is built with its own preload and each channel prefix is
// gated on the
// sender -- and here the gate is stronger than a document check: `src/main/
// confirm.ts` answers only the `webContents` of an open confirmation window.
//
// What this file deliberately does not do:
//
//   - **no `invoke`, no `send`, no `on`, no channel name.** There is one method
//     per channel and nothing else, for the reason `src/preload/index.ts` gives
//     at length: a general invoker turns an allow-list back into a bridge.
//   - **no import from `src/main/`.** The sandbox has no `node:fs`, and
//     `src/main/confirm.ts` imports it through `./window`; the channel names are
//     therefore mirrored here, and `test/unit/confirm.test.ts` and
//     `test/unit/preload-surface.test.ts` are what make the copies fail when they
//     drift.

/** CONFIRM_BRIDGE_KEY is the global the confirmation document reads. It is a
 * different key from the dashboard's `fugaro` and the picker's
 * `fugaroInstallations`, on purpose: a document that got the wrong preload would
 * find no bridge at all rather than another document's. */
export const CONFIRM_BRIDGE_KEY = 'fugaroConfirm'

/** CONFIRM_BRIDGE_PREFIX is the channel prefix, and it must match
 * `CONFIRM_CHANNEL_PREFIX` in `src/main/confirm.ts`. It is exported so the tests
 * can hold the two to each other. */
export const CONFIRM_BRIDGE_PREFIX = 'confirm:'

/** RendererLike is the slice of Electron's `ipcRenderer` this module uses: one
 * `invoke`, and nothing else exists to use. */
export interface RendererLike {
  invoke(channel: string, ...args: unknown[]): Promise<unknown>
}

/** ContextBridgeLike is the slice of Electron's `contextBridge` this module
 * uses. */
export interface ContextBridgeLike {
  exposeInMainWorld(key: string, value: unknown): void
}

/** ConfirmReply is what both methods answer: the data, or the sentence that says
 * why the main process refused. It is the same envelope the dashboard's and the
 * picker's channels use, because a screen should have one shape to draw. */
export type ConfirmReply =
  | { readonly ok: true; readonly data: unknown }
  | { readonly ok: false; readonly reason: string }

/** ConfirmBridge is what the confirmation document sees. */
export interface ConfirmBridge {
  readonly fields: () => Promise<ConfirmReply>
  readonly answer: (yes: boolean) => Promise<ConfirmReply>
}

/** isConfirmReply narrows what came back off the channel. A main process that
 * answered something else -- a bare value, a rejected promise -- is answered as a
 * refusal rather than handed to a screen as if it were a view. */
export function isConfirmReply(value: unknown): value is ConfirmReply {
  if (typeof value !== 'object' || value === null) return false
  const reply = value as { ok?: unknown; data?: unknown; reason?: unknown }
  if (reply.ok === true) return Object.prototype.hasOwnProperty.call(reply, 'data')
  return reply.ok === false && typeof reply.reason === 'string'
}

/** invokeConfirm is the one place a confirm channel is built, and the one place a
 * reply is narrowed. It never rejects: a screen draws a refusal, it does not
 * catch one. */
async function invokeConfirm(
  renderer: RendererLike,
  channel: 'fields' | 'answer',
  args: readonly unknown[],
): Promise<ConfirmReply> {
  try {
    const raw = await renderer.invoke(CONFIRM_BRIDGE_PREFIX + channel, ...args)
    return isConfirmReply(raw) ? raw : { ok: false, reason: 'the confirmation window answered something that is not a reply' }
  } catch (error) {
    return {
      ok: false,
      reason: `the confirmation window could not be reached (${error instanceof Error ? error.message : 'unknown'})`,
    }
  }
}

/**
 * createConfirmBridge builds the frozen bridge over one renderer. It is a
 * function rather than a literal so the unit test drives it with a recording
 * `invoke` under plain Node -- the same reason `createConsoleBridge` is.
 */
export function createConfirmBridge(renderer: RendererLike): ConfirmBridge {
  return Object.freeze({
    fields: () => invokeConfirm(renderer, 'fields', []),
    answer: (yes: boolean) => invokeConfirm(renderer, 'answer', [yes]),
  })
}

/** exposeConfirmBridge puts the bridge on the document's global. */
export function exposeConfirmBridge(
  contextBridge: ContextBridgeLike,
  renderer: RendererLike,
): ConfirmBridge {
  const bridge = createConfirmBridge(renderer)
  contextBridge.exposeInMainWorld(CONFIRM_BRIDGE_KEY, bridge)
  return bridge
}

/** hasRendererContext is the guard that keeps this module inert everywhere except
 * a renderer, and it is checked before Electron is touched at all -- Electron's
 * `index.js` fetches its runtime on the first `require('electron')`, so a unit
 * test that reached that line would try to download a browser. */
export function hasRendererContext(scope: {
  readonly window?: unknown
  readonly document?: unknown
}): boolean {
  return scope.window !== undefined && scope.document !== undefined
}

// The guard comes first, before the literal `require`, so importing this module
// under Node does nothing at all.
const rendererScope = globalThis as { window?: unknown; document?: unknown }
if (hasRendererContext(rendererScope)) {
  const electron = require('electron') as {
    contextBridge: ContextBridgeLike
    ipcRenderer: RendererLike
  }
  exposeConfirmBridge(electron.contextBridge, electron.ipcRenderer)
}
