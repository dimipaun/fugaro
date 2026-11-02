// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The preload (desktop-console plan Task 1.2, SECURITY-SENSITIVE; design
// section 5.1 rule 5).
//
// What this file does is small and deliberate: it puts a frozen, versioned
// object on `window` whose every property is a *method with a fixed channel
// behind it*, and it gives the renderer no way to name a channel, load a module,
// spawn a process or reach Electron. The channel a method calls is a constant
// concatenated here from the operation's name -- the renderer supplies an
// *argument object* and nothing else -- and the main process validates that
// object against the operation's closed schema (`src/main/ipc.ts`), which is
// where the security decisions live. This side is a phone book, not a switch.
//
// Three prohibitions, and the reader should check each of them against the code
// below rather than against this sentence:
//
//   - **no `invoke(channel, ...)`**: there is exactly one `invoke` call in the
//     file, in `invokeOperation`, and its first argument is built from
//     `EXPOSED_OPERATIONS` -- a list written out here -- never from an argument,
//     a property, or anything else a renderer can influence;
//   - **no channel name from the renderer**: the exposed methods take one
//     argument, the operation's own arguments. A method's channel is fixed by
//     which method was called;
//   - **no dynamic `require`**: the one `require` in the file has the literal
//     specifier `'electron'`, and everything else this module needs at runtime
//     is in the file.
//
// ## Why the surface is written out here instead of imported
//
// The window Task 1.1 builds runs `sandbox: true` (`consoleWebPreferences()`),
// and in a sandboxed preload Electron replaces `require` with a polyfill that
// resolves `electron`, `events`, `timers` and `url` and **nothing else**. A
// relative `require('../main/ipc')` -- which is what `tsc` would emit for a
// value import -- therefore cannot load; the module would fail the moment
// Electron ran it. The alternatives were a bundler (a dependency this task is
// not allowed to gain, and the pinning test is what says so: `package.json`'s
// dependency set is exact and checked) or writing the list twice.
//
// So the list is written twice, and the duplication is closed by a test rather
// than by a comment: `test/unit/ipc.test.ts` builds this bridge against a
// recording `ipcRenderer` and asserts that (a) the methods on the frozen object
// are exactly `CONSOLE_OPERATIONS`' methods and nothing else, (b) each method's
// `invoke` lands on `channelForOperation(name)` for that same operation, and
// (c) `version` is `IPC_VERSION`. Add an operation to the table without adding
// it here, or add one here without adding it to the table, and a named test
// fails -- which is the same mutation-proof shape the never-reachable table uses.
//
// **Types are imported from the table, values are not.** A type-only import is
// erased by the compiler, so nothing above applies to it; this is why the
// comment on `import type` below is worth keeping accurate.

import type { ConsoleReply } from '../main/ipc'

/**
 * CONSOLE_BRIDGE_KEY is the one name this module puts on the renderer's
 * `window`, so the whole surface is `window.fugaro`, and nothing else appears
 * there. An object of methods is a smaller thing to review than a set of
 * globals, and it is one key to delete if a future version has to.
 */
export const CONSOLE_BRIDGE_KEY = 'fugaro'

/**
 * BRIDGE_VERSION is the surface's version, carried *in* the object the renderer
 * receives so a screen can tell which console it is running against.
 *
 * It MUST equal `IPC_VERSION` in `src/main/ipc.ts`, and it is written as a
 * literal for the sandbox reason above -- that import cannot be a value import.
 * `test/unit/ipc.test.ts` compares the two, so the drift this duplication
 * invites is a failing test and not a runtime surprise.
 */
export const BRIDGE_VERSION = 1

/**
 * EXPOSED_OPERATIONS is the phone book: the operation's name (which is the
 * channel's suffix, and the identity the main process dispatches on) and the
 * method the renderer calls. Order is not meaningful -- the test compares sets
 * -- but it is kept in the table's order so a reader can diff the two files
 * line by line.
 *
 * The initializer is `as const` and carries **no type annotation**, and that is
 * load-bearing rather than a style choice. An annotation of
 * `readonly (readonly [string, string])[]` would make
 * `typeof EXPOSED_OPERATIONS` *the annotation* rather than the literals below:
 * `ExposedName` and `ExposedMethod` would then both be `string`, the mapped
 * type under them would collapse into a string index signature, and a renderer
 * could write `bridge.showBudgte(...)` and have `tsc --noEmit` accept it -- the
 * compile-time half of rule 5 inert for exactly the code it exists for, with
 * nothing failing, because the runtime tests in `test/unit/ipc.test.ts` pin the
 * same sets against the table and the object is built from that table anyway.
 * `satisfies` checks each row's shape without widening anything, so a row that
 * is not a name and a method is still a compile error, and the twenty-five names
 * stay literal -- which is what makes `ExposedName`, `ExposedMethod` and
 * `ConsoleBridge` below the closed things their comments say they are.
 *
 * `test/unit/ipc.test.ts` holds that from both sides: a source scan named for
 * this declaration, and `assertClosedSurface`, whose `@ts-expect-error`
 * directives stop being errors -- and so fail `npm run typecheck` -- if the
 * literal types are ever widened back into `string`.
 *
 * **Task 2.4's twelve rows are still those twelve.** The Spend screen's controls
 * wire the range picker and the breakdown switch to `runReport` -- on the
 * screen's side of the seam: `src/renderer/screens/spend.ts` calls them, and the
 * entry point that calls *it* is the screens task's (3.4c). Task 3.4a builds the
 * entry (`src/renderer/entry.ts`, bundled into the one script the dashboard
 * document loads) and stops at the gate, so what reaches these methods today is
 * the handshake and nothing else. They do it with the arguments `spend.report`
 * already declares: no operation was added here, no method was added to one, and
 * no screen gained a way to name a channel. A task that needs a *new read* adds
 * a row to the table in `src/main/ipc.ts` and a row here, and the test above is
 * what makes forgetting the second half fail rather than pass.
 *
 * **Console L5b added the fifteenth row and no method of its own invention**:
 * `runs.stats` / `statsRuns`, the Spend screen's by-person view, which is the
 * twelfth CLI command this surface can reach. The row is here rather than
 * reached through `runReport` because the by-person aggregate is
 * `fugaro runs stats --json` and not `report` (design §5.1 rule 6), and a method
 * that took the operation as an argument is the shape rule 5 forbids.
 *
 * **GV2.1 added the twenty-second row and no method of its own invention**:
 * `plan.status` / `planStatus`, the plan graph joined to the runs working on it,
 * which is the thirteenth CLI command this surface can reach and the second
 * whose subject is an installation's checkout rather than a project alone. The
 * row is here for the same reason L5b's is: the read is `fugaro plan status
 * --json`, and a method that took the operation as an argument is the shape rule
 * 5 forbids. That checkout is the *argument no renderer supplies* -- the row
 * declares one optional flag, `all`, and the directory the child runs in is the
 * installation's, which is why `src/main/ipc.ts`'s comment on the operation says
 * so at length.
 *
 * **Two of the first fifteen rows are not spawns.** Task 3.3 added
 * `startup.compatibility` / `getCompatibility`: the handshake's answer, which the
 * renderer asks for once at load and draws nothing until it has. Task 3.4a makes
 * that sentence literal -- the dashboard document loads the bundled entry, its
 * first call is `startConsole`, and the first thing `startConsole` does is ask
 * this method -- so the main process registers this handler *before* that
 * document is loaded (`src/main/index.ts`'s `onDecided`, called by
 * `startupWindow` between the version read and the `loadURL`). Task 3.4b added
 * `installation.current` / `getInstallation`: the installation the operator
 * chose, as the store holds it, so the header can name the project it is showing
 * without the renderer ever naming a path. Both take no argument at all --
 * `ConsoleMethod`'s optional argument object is simply not sent, so there is
 * nothing for a renderer to name.
 *
 * **The installation rail added six more, for twenty-two** (owner's design
 * ruling of 2026-10-25): `installation.list` spawns `config list --json`, and
 * the other five act on the console's own store and window.
 *
 * **Bitbucket credentials T1.2 added the twenty-third** (`coordinator.status` /
 * `coordinatorStatus`, the project view read from the coordinator's stored
 * view -- `fugaro coordinator status --from-store --all --json`, D364) and left
 * the other twenty-two exactly where they were. It is the one read of the CLI's
 * this surface can reach that needs no credential of the host's, and its two
 * flags are words of `src/main/spawn.ts`'s row rather than arguments, which is
 * why the row declares none.
 *
 * **V2.5.1a added the twenty-fourth** (`run.launch` / `launchRun`, the console's
 * one write, design section 5.1 rule 11) and left the other twenty-three exactly
 * where they were. The twenty-third is above it in this list.
 *
 * **Coordinator v2 K2a added the twenty-fifth** (`link.openPullRequest` /
 * `openPullRequest`, D814 and D815) and left the other twenty-four exactly where
 * they were. The twenty-fourth is above it in this list.
 */
export const EXPOSED_OPERATIONS = Object.freeze([
  ['live.watch', 'subscribeLive'],
  ['budget.show', 'showBudget'],
  ['spend.report', 'runReport'],
  ['runs.list', 'listRuns'],
  ['runs.stats', 'statsRuns'],
  ['plan.status', 'planStatus'],
  ['run.diagnose', 'diagnoseRun'],
  ['run.logs', 'readRunLogs'],
  ['run.logLink', 'runLogLink'],
  ['secrets.inventory', 'listSecrets'],
  ['accounts.inventory', 'listAccounts'],
  ['cli.version', 'cliVersion'],
  ['startup.compatibility', 'getCompatibility'],
  ['installation.current', 'getInstallation'],
  ['environment.report', 'environmentReport'],
  ['link.openExternal', 'openExternal'],
  // The clickable pull request number (Coordinator v2 K2a, D814/D815). One row,
  // and the method takes the operation's own three fields: the provider, the
  // repository and the number. It carries **no URL**, because the main process
  // builds the link from those three fields -- a `url` a job wrote is drawn as
  // text and never reaches the browser (rule 2, F2).
  ['link.openPullRequest', 'openPullRequest'],
  // The installation rail (owner's design ruling of 2026-10-25). The console
  // never shows a picker screen: the rail lists every installation, a click
  // switches the console to one, and a row that cannot be read opens its issue
  // in the main area. Six rows, and five of them spawn nothing: they read and
  // write the console's own store, hand the file manager a directory this
  // process computed, or open the window the `+` door is. The sixth,
  // `installation.list`, is `fugaro config list --json` -- the read that was
  // the picker window's own and is now the rail's.
  ['installation.list', 'listInstallations'],
  ['installation.choose', 'chooseInstallation'],
  ['installation.checkout', 'setInstallationCheckout'],
  ['installation.remove', 'removeInstallation'],
  ['installation.reveal', 'revealInstallation'],
  ['installation.open', 'openInstallations'],
  // The project view (Bitbucket credentials T1.2, D364): `fugaro coordinator
  // status --from-store --all --json`, the one read of the CLI's that needs no
  // credential of the host's. One row here for the reason L5b's and GV2.1's are
  // one row each: a method that took the operation as an argument is the shape
  // rule 5 forbids. The two flags the read fixes are words of `src/main/
  // spawn.ts`'s row, not arguments a renderer sends -- the row here declares
  // none.
  ['coordinator.status', 'coordinatorStatus'],
  // The launch, and the surface's **one write** (V2.5.1a, design section 5.1
  // rule 11, D22). One row, like every other: the five fields are the
  // operation's own closed schema. What this method *does* is ask the main
  // process for a confirmation window: it cannot answer for the operator, and
  // since V2.5.1a's part 2b-3b-1 the main process spawns on a yes from that
  // window's own top frame and on nothing else (`createConfirmWindow`,
  // `src/main/confirm.ts`), so no renderer reaches a `fugaro run` child through
  // this row by itself.
  ['run.launch', 'launchRun'],
] as const) satisfies readonly (readonly [string, string])[]

/**
 * ExposedName is the closed union of the operation names this file exposes --
 * twenty-five string literals, not `string`. It is the type of the `operations` list
 * a screen switches on, so the closure is what lets a screen be exhaustive.
 */
export type ExposedName = (typeof EXPOSED_OPERATIONS)[number][0]

/** ExposedMethod is the closed union of the method names it exposes. */
export type ExposedMethod = (typeof EXPOSED_OPERATIONS)[number][1]

/** ConsoleMethod is the shape of every method on the bridge: one optional
 * argument object, one reply that is never a rejection. */
export type ConsoleMethod = (args?: Readonly<Record<string, unknown>>) => Promise<ConsoleReply>

/**
 * ConsoleBridge is what the renderer sees. Every operation is a method that
 * takes one optional argument object and answers a `ConsoleReply` -- never a
 * rejection, so a screen has one shape to draw, and never a value the renderer
 * has to interpret: the data is the CLI's own document, already rendered.
 *
 * The mapped type is the *closed* surface rule 5 asks for only while
 * `ExposedMethod` is a union of literals (see `EXPOSED_OPERATIONS` above): with
 * the table widened, this becomes a string index signature, and every claim
 * below -- including "no `invoke`" -- becomes an unchecked assertion a renderer
 * can walk through at compile time.
 *
 * There is no `invoke`, no `send`, no `on` and no `channel` here, and their
 * absence is the design's rule 5. When the live feed needs documents to arrive
 * *without* a request (design section 2.2), that arrives as an operation in
 * `EXPOSED_OPERATIONS` with its own method -- not as a listener the renderer can
 * point at an arbitrary channel, which is the shape that turns an allow-list
 * back into a bridge.
 */
export type ConsoleBridge = {
  readonly version: number
  readonly operations: readonly ExposedName[]
} & {
  readonly [Method in ExposedMethod]: ConsoleMethod
}

/**
 * BRIDGE_PREFIX is the channel prefix, and it must match `IPC_CHANNEL_PREFIX` in
 * `src/main/ipc.ts` -- another consequence of the sandbox, and another thing
 * `test/unit/ipc.test.ts` compares by asserting the channel each method actually
 * invokes.
 */
const BRIDGE_PREFIX = 'fugaro:console/'

/**
 * RendererLike is the slice of Electron's `ipcRenderer` this module uses: one
 * `invoke`, because there is nothing else to use. It is an argument to
 * `createConsoleBridge` rather than an import so that the unit test drives every
 * decision here under plain Node, and so that the file has one place -- the
 * guarded block at the bottom -- that touches Electron at all.
 */
export interface RendererLike {
  invoke(channel: string, ...args: unknown[]): Promise<unknown>
}

/** ContextBridgeLike is the slice of Electron's `contextBridge` this module
 * uses, for the same reason. */
export interface ContextBridgeLike {
  exposeInMainWorld(key: string, api: unknown): void
}

/**
 * isConsoleReply checks the envelope the main process promised before it is
 * handed to a screen. It does not check the *data* -- that is the CLI's document
 * and the console draws what it was given -- but a bridge that forwarded
 * `undefined`, or an object shaped like something else, would put a value in the
 * renderer that no screen's code was written for.
 */
function isConsoleReply(value: unknown): value is ConsoleReply {
  if (typeof value !== 'object' || value === null) return false
  const ok: unknown = (value as { ok?: unknown }).ok
  return typeof ok === 'boolean'
}

/**
 * invokeOperation is the file's one call into Electron's IPC, and the only place
 * a channel name is formed on this side. `EXPOSED_OPERATIONS`'s name is
 * concatenated with the bridge prefix and passed straight through; the renderer
 * never sees or supplies either half.
 *
 * The argument object is forwarded *as it arrived*. That is on purpose: there is
 * one validating gate in this application and it is in the main process, where
 * the command it guards also lives. A second check here could only drift from
 * the first, and a preload that "cleans up" an argument is a preload a renderer
 * can surprise.
 */
async function invokeOperation(
  ipcRenderer: RendererLike,
  name: string,
  args: unknown,
): Promise<ConsoleReply> {
  try {
    const reply: unknown = await ipcRenderer.invoke(BRIDGE_PREFIX + name, args)
    if (!isConsoleReply(reply)) {
      return { ok: false, reason: 'the console answered with something that is not a console reply' }
    }
    return reply
  } catch {
    // `registerConsoleHandlers` answers a refusal rather than rejecting, so
    // reaching here means the channel itself is gone: the window is closing, or
    // the main process died. There is nothing else to report.
    return { ok: false, reason: 'the console is not reachable' }
  }
}

/**
 * createConsoleBridge builds the frozen object. One method per operation in
 * `EXPOSED_OPERATIONS`, each closing over its own name, and no method that takes
 * a channel.
 *
 * `Object.freeze` is shallow, which is what is wanted and all that is needed
 * here: the object's own properties are the surface, none of them is an object
 * the renderer could reach into, and `operations` is frozen as well. The plan
 * asks for `Object.isFrozen` explicitly, and the test asserts it on the returned
 * object.
 *
 * The returned object is not cast into `ConsoleBridge`: it is checked against
 * it, like any other value, and that is the point of the accumulator's type. It
 * is `Record<ExposedMethod, ConsoleMethod>` -- twenty-five *named* properties,
 * because `ExposedMethod` is a union of literals -- so spreading it gives the
 * object literal those twenty-five properties as properties rather than as an index
 * signature. (Before that type was literal, this line needed `as ConsoleBridge`
 * to silence the compiler, which is what made the claim untested rather than
 * checked.) The loop is what actually writes them, and a loop is a thing the
 * compiler cannot count, so what closes *that* gap is `test/unit/ipc.test.ts`:
 * it reads the frozen object back and compares its methods with the table's, in
 * both directions.
 */
export function createConsoleBridge(ipcRenderer: RendererLike): ConsoleBridge {
  const methods: Record<ExposedMethod, ConsoleMethod> = Object.create(null)
  for (const [name, method] of EXPOSED_OPERATIONS) {
    methods[method] = (args) => invokeOperation(ipcRenderer, name, args)
  }
  const names: readonly ExposedName[] = Object.freeze(EXPOSED_OPERATIONS.map(([name]) => name))
  return Object.freeze({
    version: BRIDGE_VERSION,
    operations: names,
    ...methods,
  })
}

/**
 * exposeConsoleBridge is `contextBridge.exposeInMainWorld` with the key and the
 * object already decided, so the bottom of this file is the only place the two
 * are combined and there is no call site that could pick a different key.
 *
 * `exposeInMainWorld` is what keeps rule 5's promise concrete: the renderer gets
 * a copy of this object, in its own world, with `contextIsolation` in between.
 * It cannot reach the preload's scope, `ipcRenderer`, or Node -- only these
 * twenty-five methods.
 */
export function exposeConsoleBridge(
  contextBridge: ContextBridgeLike,
  ipcRenderer: RendererLike,
): void {
  contextBridge.exposeInMainWorld(CONSOLE_BRIDGE_KEY, createConsoleBridge(ipcRenderer))
}

/**
 * hasRendererContext is the guard that keeps this module inert everywhere except
 * a renderer. It is checked before Electron is touched at all, which matters for
 * a reason beyond tidiness: Electron's own `index.js` fetches its runtime on the
 * first `require('electron')` (console/.npmrc), so a unit test that imported
 * this file and reached that line would try to download a browser.
 *
 * A preload script always runs in a renderer, so `window` and `document` are
 * both there when Electron loads this file, and neither is under `vitest`'s
 * default Node environment -- which is exactly the distinction the guard needs.
 */
export function hasRendererContext(scope: {
  readonly window?: unknown
  readonly document?: unknown
}): boolean {
  return scope.window !== undefined && scope.document !== undefined
}

// The bridge is put on `window` here rather than from a function the main
// process calls: a preload script is loaded *by* the renderer, and this is the
// only moment it gets. The guard comes first, before the literal `require`, so
// importing this module under Node does nothing at all.
const rendererScope = globalThis as { window?: unknown; document?: unknown }
if (hasRendererContext(rendererScope)) {
  const electron = require('electron') as {
    contextBridge: ContextBridgeLike
    ipcRenderer: RendererLike
  }
  exposeConsoleBridge(electron.contextBridge, electron.ipcRenderer)
}
