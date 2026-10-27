// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The installation picker's preload: the *only* thing the picker document can
// do (desktop-console plan Task 3.4b; design sections 3.4 rules 5 and 7, 5.1
// rule 10, D13, D14; owner ruling C9, 2026-10-18).
//
// It is a second preload rather than a second window on the dashboard's, and
// the reason is the surface: `src/preload/index.ts` exposes **twenty-three** rows
// to the document that draws the dashboard -- the CLI reads, the startup
// handshake's answer, `openExternal`'s handover, `installation.current` and the
// installation rail's six -- and the picker has no business with any of them. The two documents are served from the
// same origin (`app://console`), so the *only* thing that separates their
// surfaces is that each window is built with its own preload and each channel
// prefix is gated on the document that may reach it -- `isSenderOnDocument` in
// `src/main/ipc.ts` for the `fugaro:console/` prefix, and `src/main/picker.ts` for
// the `fugaro:installations/` prefix.
//
// What this file deliberately does not do:
//
//   - **no path the console did not itself obtain crosses this boundary.** The
//     form is gone (owner's ruling of 2026-10-20): there is no `add`, no
//     `pickBinary`, no `pickConfig` and no `pickCheckout`, and no pick token.
//     The rows a screen draws are the installations `fugaro config list --json`
//     printed, a checkout it may set is a directory *that same document* listed
//     (`checkout`'s argument is checked against the last read by
//     `src/main/picker.ts`), and the one URL is not an argument at all: the
//     `openExternal` row takes **no arguments**, so `GETTING_STARTED_URL` never
//     leaves the main process.
//   - **no `invoke`, no `send`, no `on`, no channel name.** There is one method
//     per row of `INSTALLATION_OPERATIONS` and nothing else, for the reason
//     `src/preload/index.ts` gives at length: a general invoker turns an
//     allow-list back into a bridge.
//   - **no import from `src/main/`.** The sandbox has no `node:fs`, and
//     `src/main/installations.ts` imports it; the operation table is therefore
//     mirrored here, exactly as `EXPOSED_OPERATIONS` mirrors
//     `CONSOLE_OPERATIONS`, and `test/unit/installations-ipc.test.ts` is what
//     makes the two copies fail when they drift.

/**
 * INSTALLATION_BRIDGE_KEY is the global the picker document reads. It is a
 * different key from the dashboard's `fugaro`, on purpose: a document that got
 * the wrong preload would find no bridge at all rather than the other
 * document's.
 */
export const INSTALLATION_BRIDGE_KEY = 'fugaroInstallations'

/** INSTALLATION_BRIDGE_VERSION is the picker surface's version. */
export const INSTALLATION_BRIDGE_VERSION = 1

/**
 * INSTALLATION_OPERATIONS is the mirror of `INSTALLATION_OPERATIONS` in
 * `src/main/installations.ts`: the channel name and the method name, and
 * nothing else -- the argument schemas live in the main process, where they are
 * checked, and duplicating them here would be a second copy of a security rule.
 */
export const INSTALLATION_OPERATIONS = Object.freeze([
  ['list', 'list'],
  ['choose', 'choose'],
  ['checkout', 'checkout'],
  ['remove', 'remove'],
  ['reveal', 'reveal'],
  ['openExternal', 'openExternal'],
] as const) satisfies readonly (readonly [string, string])[]

/** InstallationOperationName is the closed union of channel names. */
export type InstallationOperationName = (typeof INSTALLATION_OPERATIONS)[number][0]

/** InstallationMethodName is the closed union of method names. */
export type InstallationMethodName = (typeof INSTALLATION_OPERATIONS)[number][1]

/** InstallationReply is what every method answers: the data, or the sentence
 * that says why it could not be read. It is the same envelope
 * `src/main/ipc.ts` uses -- `{ ok, data }` or `{ ok, reason }` -- because a
 * screen should have one shape to draw across both windows. */
export type InstallationReply =
  | { readonly ok: true; readonly data: unknown }
  | { readonly ok: false; readonly reason: string }

/** InstallationMethod is the shape of every method here: one optional argument
 * object, one reply that is never a rejection. */
export type InstallationMethod = (args?: Readonly<Record<string, unknown>>) => Promise<InstallationReply>

/** InstallationBridge is what the picker document sees. */
export type InstallationBridge = {
  readonly version: number
  readonly operations: readonly InstallationOperationName[]
} & {
  readonly [Method in InstallationMethodName]: InstallationMethod
}

/** INSTALLATION_BRIDGE_PREFIX is the channel prefix, and it must match
 * `INSTALLATION_CHANNEL_PREFIX` in `src/main/installations.ts`. It is exported
 * so `test/unit/installations-ipc.test.ts` can hold the two prefixes to each
 * other: the sandboxed preload cannot import the main process's module, so the
 * constant is duplicated on purpose, and a duplicate nobody compares drifts. */
export const INSTALLATION_BRIDGE_PREFIX = 'fugaro:installations/'

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

/** isInstallationReply narrows what came back off the channel. A main process
 * that answered something else -- a rejected promise, a bare value -- is
 * answered as a refusal rather than handed to a screen as if it were a
 * document. */
export function isInstallationReply(value: unknown): value is InstallationReply {
  if (typeof value !== 'object' || value === null) return false
  const reply = value as { ok?: unknown; data?: unknown; reason?: unknown }
  if (reply.ok === true) return Object.prototype.hasOwnProperty.call(reply, 'data')
  return reply.ok === false && typeof reply.reason === 'string'
}

/** invokeOperation is the one place a channel is built, and the one place a
 * reply is narrowed. It never rejects: a screen draws a refusal, it does not
 * catch one. */
export async function invokeOperation(
  renderer: RendererLike,
  operation: InstallationOperationName,
  args: Readonly<Record<string, unknown>> | undefined,
): Promise<InstallationReply> {
  try {
    const raw = args === undefined
      ? await renderer.invoke(INSTALLATION_BRIDGE_PREFIX + operation)
      : await renderer.invoke(INSTALLATION_BRIDGE_PREFIX + operation, args)
    return isInstallationReply(raw) ? raw : { ok: false, reason: 'the picker answered something that is not a reply' }
  } catch (error) {
    return {
      ok: false,
      reason: `the picker could not be reached (${error instanceof Error ? error.message : 'unknown'})`,
    }
  }
}

/**
 * createInstallationBridge builds the frozen bridge over one renderer. It is a
 * function rather than a literal so the unit test drives it with a recording
 * `invoke` under plain Node -- the same reason `createConsoleBridge` is.
 */
export function createInstallationBridge(renderer: RendererLike): InstallationBridge {
  const bridge: Record<string, unknown> = {
    version: INSTALLATION_BRIDGE_VERSION,
    operations: Object.freeze(INSTALLATION_OPERATIONS.map(([name]) => name)),
  }
  for (const [name, method] of INSTALLATION_OPERATIONS) {
    bridge[method] = (args?: Readonly<Record<string, unknown>>) => invokeOperation(renderer, name, args)
  }
  return Object.freeze(bridge) as unknown as InstallationBridge
}

/** exposeInstallationBridge puts the bridge on the document's global. */
export function exposeInstallationBridge(
  contextBridge: ContextBridgeLike,
  renderer: RendererLike,
): InstallationBridge {
  const bridge = createInstallationBridge(renderer)
  contextBridge.exposeInMainWorld(INSTALLATION_BRIDGE_KEY, bridge)
  return bridge
}

/** hasRendererContext is the guard that keeps this module inert everywhere
 * except a renderer, and it is checked before Electron is touched at all --
 * Electron's `index.js` fetches its runtime on the first `require('electron')`,
 * so a unit test that reached that line would try to download a browser. */
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
  exposeInstallationBridge(electron.contextBridge, electron.ipcRenderer)
}
