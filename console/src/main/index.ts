// The console's Electron main process entry point (desktop-console plan Task
// 1.1; the IPC wiring is Task 3.4a).
//
// The plan's Files column for Task 1.1 names `src/main/window.ts` and
// `src/main/protocol.ts`; this file is here because those two are libraries and
// something has to call them. Without an entry point the `app://` scheme is
// never registered, `protocol.handle` never runs, and the e2e spec has no
// application to launch -- the window flags would be a set of options nothing
// constructs. It keeps to the minimum that makes the two modules live, and it
// is the only place in the console that imports Electron at runtime: every
// decision it could get wrong is in `window.ts` or `protocol.ts`, where the
// unit test can reach it under plain Node.
//
// **What Task 3.4a adds here, and why it is here rather than in a module the
// unit suite can import.** Three things, all of them one-liners over functions
// that *are* unit-tested: the served root is the built bundle (`rendererRoot`),
// the dashboard's preload path is named once (`consolePreloadPath`), and the
// handshake's answer is turned into a registered IPC surface (`onDecided`).
// What is left in this file is the wiring between them, and the wiring is the
// part that cannot be imported under `vitest`: this module imports the Electron
// *runtime*, so `require('electron')` here is a process and not a path string.
// `test/unit/startup.test.ts` reads this file as **text** and asserts the shapes
// that matter (the order, the once-only registration, the refusal a console with
// no installation answers), and `test/e2e/entry.spec.ts` drives the real thing.
//
// Note where the documents are read from. `tsc` emits JavaScript only, so the
// bundle's files are written by `tools/bundle-renderer/index.ts` into
// `dist/bundle/` -- the compiled entry's whole closure as one script, and the two
// documents copied verbatim -- and the handler's root is that directory under the
// app's own path. Task 4.1's builder is the one that gets to decide where a
// packaged console keeps those files, and it will have to pack them; until then
// there is no packaging step to disagree with.

import { join } from 'node:path'

import { app, BrowserWindow, Menu, dialog, ipcMain, protocol, session, shell } from 'electron'
import type { OpenDialogOptions } from 'electron'

import { handleAppRequests, registerAppScheme } from './protocol'
import { consoleIconPath, installDevDockIcon } from './icon'
import { installInstallationPicker } from './picker'
import type { DialogLike, InstallationPicker } from './picker'
import {
  createConsoleLifecycle,
  installWebContentsSecurity,
  openExternalValidated,
  versionReadFrom,
} from './window'
import type { ConsoleWindowDeps, VersionRead } from './window'
import { privateWorkingDirectory, resolveHandshakeBinary } from './spawn'
import type { SpawnTarget } from './spawn'
import { createSpawnLog } from './spawnlog'
import { reducedModeHandlers, registerConsoleHandlers, startupCompatibility } from './ipc'
import type { ConsoleHandlers } from './ipc'
import { handlersFor } from '../source/source'
import { createBoundSource } from '../source/cli'
import type { BoundInstallation } from '../source/cli'
import type { CliCompatibility } from '../shared/version'

// Before `whenReady` resolves, which is the only window Electron gives for it:
// a scheme registered later is not a standard or secure one.
registerAppScheme(protocol)

// Every webContents Electron ever creates gets the three denials, not just the
// console's own window -- so a webContents that appears for a reason this file
// does not know about is still one that cannot navigate, open a window or
// attach a `<webview>`. `createConsoleWindow` installs them on the window it
// builds as well: a window that is correct on its own does not depend on the
// global hook having been registered.
app.on('web-contents-created', (_event, contents) => {
  installWebContentsSecurity(contents)
})

/**
 * rendererRoot is the directory the protocol handler serves, and the only
 * directory it will ever read a file from.
 *
 * It is the **build's** output and not the source tree (Task 3.4a, step 4):
 * `npm run build` compiles the renderer's entry into `dist/renderer/`, bundles it
 * and its whole closure into `dist/bundle/console.js`, and copies the two
 * documents beside it. `SERVED_FILES` in `src/main/protocol.ts` is the list of
 * what that directory holds, and `test/unit/bundle.test.ts` holds the two equal
 * in both directions -- so this line is the one place the served root is named,
 * and the directory it names is the one a test checks.
 */
function rendererRoot(): string {
  return join(app.getAppPath(), 'dist', 'bundle')
}

/**
 * consolePreloadPath is the compiled preload the dashboard window loads: the
 * file `tsc` emits from `src/preload/index.ts`.
 *
 * It is inside the app's own path, like the served root, and it is named here
 * because this is the module that knows where the app is. The rule that the
 * renderer may not name a path is unaffected: the *renderer* never sees this
 * one -- the window is built with it before any document loads, and what crosses
 * into the renderer is the frozen object the preload puts on `window`
 * (`src/preload/index.ts`).
 */
function consolePreloadPath(): string {
  return join(app.getAppPath(), 'dist', 'preload', 'index.js')
}

function reportAndQuit(error: unknown): void {
  console.error('[console] the window could not be created:', error)
  // The other way out of this process, and `app.exit` fires no `before-quit`:
  // the handler below is not reached on this path, so a switch or an `activate`
  // that failed would leave the console's child running exactly as a plain quit
  // would have. The close is bounded (`SIGTERM`, then `SIGKILL`), so a fatal
  // error still exits, and the exit code is the one this path has always used.
  void boundSource
    .close()
    .catch(() => undefined)
    .finally(() => app.exit(1))
}

/**
 * serveBundle puts the protocol handler in place, and there is exactly one of
 * these per process: `protocol.handle` refuses a scheme that is already
 * registered ("Failed to register protocol: app"), so a second call is not a
 * harmless no-op but a thrown error. It is therefore separate from
 * `lifecycle.activate` below, which is the half that may run again.
 */
function serveBundle(): void {
  handleAppRequests(protocol, rendererRoot())
}

/**
 * handshakeInstallation is the stored **active** installation the startup
 * handshake reads (Task 3.4b, steps 3 and 4), or `null` when there is none.
 *
 * With one, its `binaryPath` is the **only** binary the handshake reads
 * (`resolveInstalledBinary`: no fallthrough to any other `fugaro`, review B1)
 * and its project (and config path, when stored) is the spawn target, so the
 * bare `version` spawn records which installation it was for. With none, the
 * console makes **no guess**: not `FUGARO_PROJECT`, not the working directory's
 * checkout, never the CLI's config -- `resolveHandshakeBinary` runs Task 3.3's
 * project-less read against rule 4's resolution, the dashboard answers
 * `NO_ACTIVE_INSTALLATION`, and the picker opens once.
 */
function handshakeInstallation(): {
  readonly id: string
  readonly binaryPath: string
  readonly target: SpawnTarget
} | null {
  const chosen = picker?.installation()
  if (chosen === undefined || !chosen.chosen) return null
  return {
    id: chosen.id,
    binaryPath: chosen.binary,
    target: Object.freeze({
      project: chosen.project,
      ...(chosen.config === undefined ? {} : { configPath: chosen.config }),
    }),
  }
}

/**
 * handshakeFor is the id of the installation whose startup handshake has just
 * been read, until `onDecided` hands the answer to the picker's registry
 * (`picker.remember`), so `installation.current` and the picker's rows carry the
 * startup's mode and version rather than a default (review S1). It is consumed
 * once: a later `onDecided` comes from a choose, whose own probe the picker has
 * already remembered.
 */
let handshakeFor: string | null = null

/**
 * readCliVersion is the handshake's reader: `resolveHandshakeBinary`
 * (`src/main/spawn.ts`, where it is unit-tested) and the version line of the
 * one binary it reads.
 *
 * It reads the machine and nothing else -- no state from a previous run, no
 * cached answer. A resolution that fails crosses into the compatibility as a
 * **category** (`versionReadFrom` in `window.ts`), never as `resolution.reason`,
 * which embeds what the binary printed; and a resolution or a probe that
 * *throws* is left to `runVersionHandshake`, which turns it into the refusal
 * with the console's own sentence rather than a crash.
 *
 * The child runs in `privateWorkingDirectory()` (rule 5), and the probe is
 * logged like every spawn (design section 5.1 rule 10): the log is opened for
 * this one read and closed as soon as it is done.
 */
async function readCliVersion(): Promise<VersionRead> {
  const log = createSpawnLog(process.env)
  const chosen = handshakeInstallation()
  try {
    const resolution = await resolveHandshakeBinary({
      installation: chosen,
      appEnv: process.env,
      directory: privateWorkingDirectory(),
      log,
    })
    return versionReadFrom(resolution)
  } finally {
    handshakeFor = chosen === null ? null : chosen.id
    log.close()
  }
}

/** windowDeps is the Electron surface the windows are built with.
 * `session.defaultSession` is read here rather than at module scope because it
 * does not exist until the app is ready. `preloadPath` is the dashboard window's
 * (Task 3.4a): `buildConsoleWindow` gives it to that window alone. */
function windowDeps(): ConsoleWindowDeps {
  return { BrowserWindow, session: session.defaultSession, preloadPath: consolePreloadPath() }
}

// ---------------------------------------------------------------------------
// The IPC surface (desktop-console plan Task 3.4a, steps 1 to 3)
// ---------------------------------------------------------------------------

/**
 * current is the handshake's answer, and the only thing this file stores.
 *
 * It is written once, by `onDecided` below, before any window exists -- so a
 * handler can only ever read an answer the console has already drawn from -- and
 * it is read through `storedCompatibility` rather than captured, which is what
 * makes `startup.compatibility`'s answer the one the console decided on rather
 * than a copy of it.
 */
let current: CliCompatibility | null = null

/**
 * storedCompatibility is the read the `startup.compatibility` handler answers
 * from. It throws when there is no answer yet, and `dispatch` turns that throw
 * into a reply: an operation called before the handshake finished is a console
 * that does not exist yet, and it is answered as a refusal rather than as
 * `null`, which no screen is written for.
 */
function storedCompatibility(): CliCompatibility {
  if (current === null) throw new Error('the console has not read a fugaro yet')
  return current
}

/**
 * activeBinding is the installation the source is bound to, as the store holds
 * it **now** (Task 3.4c, step 1). `null` is D13's no-installation state, and it
 * is answered with no spawn at all -- the alternative would be a source built
 * from whatever the machine happened to have (the ambient `FUGARO_PROJECT` and
 * the first `fugaro` on `PATH`), which is a console driving something the
 * operator never chose.
 *
 * It is a function rather than a value for the reason `handlersFor`'s
 * `installation` dep is: the store is read at message time, so a choose or a
 * removal is answered by the next read rather than by a value captured when the
 * surface was registered.
 */
function activeBinding(): BoundInstallation | null {
  const chosen = picker?.installation()
  if (chosen === undefined || !chosen.chosen) return null
  return {
    id: chosen.id,
    project: chosen.project,
    binaryPath: chosen.binary,
    ...(chosen.config === undefined ? {} : { configPath: chosen.config }),
    ...(chosen.checkout === undefined ? {} : { checkout: chosen.checkout }),
    ...(chosen.cliVersion === null ? {} : { version: chosen.cliVersion }),
  }
}

/**
 * boundSource is the `FugaroSource` the whole surface is built over: the real
 * `CliSource` for the active installation, built by `sourceFor` and cached by
 * the installation's id (`createBoundSource` in `src/source/cli.ts`, where the
 * whole of the decision is unit-tested).
 *
 * **This is the only source the shipped app can construct.** `sourceFor` passes
 * the installation's own binary as the configured path, so the child is the
 * binary the operator chose or there is no child; `src/source/fixture.ts` -- the
 * replaying source the unit tests use -- is reachable only through the tests'
 * injected `create`, and a grep-style test holds `test/unit/entry-wiring.test.ts`
 * to that: this file imports nothing from `fixture.ts`, and no environment
 * variable, argv or setting selects a source.
 *
 * The store is read through `activeBinding`, and the source it builds spawns on
 * the first read rather than here, so a console with nothing chosen -- and a
 * console in a mode that draws no screen -- drives nothing at all.
 */
const boundSource = createBoundSource({ installation: activeBinding, appEnv: process.env })

/**
 * openExternalFromShell is the surface's one operation that is not a read of the
 * CLI: it hands the URL to `openExternalValidated`, which is the console's one
 * call to `shell.openExternal` and its own allow-list (`src/main/window.ts`). The
 * argument is narrowed here and validated there; a value that is not a string
 * cannot be a URL this console opens, so it is refused by the same function
 * rather than by a second check written here.
 */
async function openExternalFromShell(raw: unknown): Promise<void> {
  await openExternalValidated(shell, typeof raw === 'string' ? raw : '')
}

/**
 * gated is the whole surface, and it is the join the two modules describe:
 * `handlersFor` answers the twelve operations that read the CLI (over
 * `boundSource`, so each of them reaches the active installation's own binary
 * and nothing else), and `startup.compatibility` is joined in here because it is
 * the one operation whose answer is the console's own state rather than a read
 * (`CliBackedHandlers` in `src/source/source.ts` says so from the other side).
 *
 * `reducedModeHandlers` wraps the result in `registerHandlers` below rather than
 * here, so that what this constant holds is the surface and what the wrapper
 * holds is the gate over it.
 */
const gated: ConsoleHandlers = {
  ...handlersFor(boundSource, {
    openExternal: openExternalFromShell,
    // `installation.current` (Task 3.4b): the picker's store, read at message
    // time. Before the picker exists the answer is `handlersFor`'s own
    // no-installation state.
    installation: () => picker?.installation(),
  }),
  'startup.compatibility': startupCompatibility(storedCompatibility),
}

/** registered is whether this process has put the surface on `ipcMain`. */
let registered = false

/**
 * registerHandlers puts the surface on `ipcMain`, **once per process**.
 *
 * The once-only rule is not tidiness: Electron's `ipcMain.handle` throws on a
 * second listener for a channel it already has ("Attempted to register a second
 * handler"), so a second registration would be a startup failure -- and the
 * guard is what makes `onDecided` safe to call from a path that might run twice,
 * which is exactly what `activate` is for the window.
 *
 * The wrapper is `reducedModeHandlers`: the handshake's answer decides which
 * operations refuse before the source is reached, and it is read per call, so a
 * handler registered here can never answer for a mode the console is not in.
 */
function registerHandlers(): void {
  if (registered) return
  registered = true
  registerConsoleHandlers(ipcMain, reducedModeHandlers(gated, storedCompatibility))
}

/**
 * onDecided is what the handshake's answer does to this process: it is stored,
 * and -- unless the console refused -- the IPC surface is registered.
 *
 * **A refused console registers nothing, ever.** That is the plan's step 2 and
 * design D7's strongest form: a refused console loads a document with no
 * renderer entry in it, and there is no handler behind any channel for one to
 * reach even if it did. The refusal is decided here, once, from the mode; a
 * later `activate` reopens the refusal window and does not come back through
 * this function at all.
 *
 * The ordering is the other half of it: `startupWindow` calls this after the
 * version read and **before** the dashboard document is loaded, so a renderer
 * that asks for `startup.compatibility` the moment it runs finds an answer
 * rather than an unregistered channel.
 */
function onDecided(compatibility: CliCompatibility): void {
  current = compatibility
  const read = handshakeFor
  handshakeFor = null
  if (read !== null) picker?.remember(read, compatibility)
  if (compatibility.mode === 'refused') return
  registerHandlers()
}

/**
 * lifecycle is the startup and the `activate` path (`createConsoleLifecycle` in
 * `window.ts`, where it is unit-tested): the handshake runs once -- a stored
 * promise, so an `activate` during the handshake waits for it rather than
 * starting a second one -- and `activate` opens the window the stored answer
 * allows, so a refused console comes back as a refusal screen and never as a
 * dashboard, and a dock-icon click cannot change which console the operator is
 * looking at.
 */
const lifecycle = createConsoleLifecycle({
  windowDeps,
  readVersion: readCliVersion,
  openWindowCount: () => BrowserWindow.getAllWindows().length,
  onDecided,
})

/**
 * picker is the installation picker (Task 3.4b): the store in
 * `<userData>/installations.json`, the `Installations…` menu item, and the
 * picker's own window and channels. It is built **after** `whenReady`, because
 * `session.defaultSession` and `app.getPath` do not exist before it, and
 * **before** the startup handshake, which reads the stored active installation
 * (`handshakeInstallation` above).
 *
 * A choose runs the version handshake for that installation in the picker
 * (`probeInstallation`); a `refused` answer is not chosen. Otherwise
 * `onChosen` hands the answer to `lifecycle.choose`, which goes through the
 * same path the startup does: `onDecided` stores it and registers the surface if
 * it was not yet, and the window is reopened through `openWindowFor` -- so a
 * refused-to-ok switch replaces the refusal window with the dashboard.
 */
let picker: InstallationPicker | null = null

/**
 * pickerDialog is Electron's `dialog` narrowed to the picker's `DialogLike`: a
 * parentless dialog when the picker has no window, and the options copied into
 * Electron's own (mutable) shapes rather than cast.
 */
const pickerDialog: DialogLike = {
  showOpenDialog: (window, options) => {
    const electronOptions: OpenDialogOptions = {
      title: options.title,
      properties: [...options.properties] as OpenDialogOptions['properties'],
      ...(options.filters === undefined
        ? {}
        : { filters: options.filters.map((filter) => ({ name: filter.name, extensions: [...filter.extensions] })) }),
    }
    return window === null ? dialog.showOpenDialog(electronOptions) : dialog.showOpenDialog(window, electronOptions)
  },
}

/**
 * main is what `whenReady` resolves into: serve the bundle once, then run the
 * startup. Everything a second call would repeat is in `lifecycle.activate`.
 */
async function main(): Promise<void> {
  // The icon first, and before any window exists: an unpackaged console has no
  // bundle to carry `build/icon.png`, so until this runs the dock shows
  // Electron's own icon rather than the website's mark (the owner's brief of
  // 2026-10-19). It is a no-op once packaged -- the bundle's icon is the same
  // file, put there by the packager -- and off macOS, where there is no dock to
  // set. `installDevDockIcon` is where all of that is decided and tested.
  installDevDockIcon({
    platform: process.platform,
    packaged: app.isPackaged,
    dock: app.dock,
    iconPath: consoleIconPath(app.getAppPath()),
  })
  serveBundle()
  picker = installInstallationPicker({
    electron: { BrowserWindow, session: session.defaultSession, ipcMain, dialog: pickerDialog, Menu, app },
    appEnv: process.env,
    // The picker awaits this inside its own serial chain (review S6), so a
    // second choose cannot start until the window this one asked for is up.
    onChosen: (chosen) => lifecycle.choose(chosen.compatibility).catch(reportAndQuit),
    // A removal that took the active installation drops the bound source (Task
    // 3.4c; the seam 3.4b left for it). Without this the surface would keep
    // answering from a source built for a binary the store no longer names --
    // and, worse, would keep its child process alive. `forget` is safe with
    // nothing bound (the first run) and safe to call twice, so the wiring is
    // unconditional.
    onRemoved: () => {
      void boundSource.forget()
    },
  })
  // The menu is installed in **every** mode, `refused` included: choosing an
  // installation is how an operator gets out of a refusal, and a console that
  // hid the door would be a console with no way back.
  picker.installMenu()
  await lifecycle.start()
  // D13's first run: with no installation chosen the console makes no guess,
  // says so, and opens the picker once. A picker that fails to open is logged
  // and is not a reason to quit a console whose own window is already up: the
  // menu item is still there.
  await picker.openOnFirstRun().catch((error: unknown) => {
    console.error('[console] the installation picker could not be opened:', error)
  })
}

app.whenReady().then(main).catch(reportAndQuit)

// The console is a single window: closing it quits, except on macOS, where the
// convention is that the app stays running and `activate` brings the window
// back.
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})

/**
 * **The console stops what it started before it exits.** The Live screen's poll
 * runs `fugaro watch` through the source bound to the active installation, and
 * that child lives as long as the source does: nothing else would stop it. The
 * menu's Quit is Electron's own `role: 'quit'` with an empty callback and
 * `window-all-closed` only calls `app.quit()`, so without this the child
 * outlives the app that started it -- the opposite of what `forget` protects (an
 * installation the console no longer shows leaves nothing running), and the
 * reason every spawn is written to the spawn log: a child nobody stopped is a
 * child somebody has to find.
 *
 * `CliSource.close()` is bounded -- `SIGTERM`, then `SIGKILL` after
 * `SHUTDOWN_GRACE_MS`, and a drain backstop for a grandchild holding the pipe --
 * so the quit can wait for it. The first `before-quit` is held back for exactly
 * that; the `app.quit()` below comes back through this handler, and that second
 * one falls through, or the quit would hold itself back forever.
 */
let closing = false
app.on('before-quit', (event) => {
  if (closing) return
  closing = true
  event.preventDefault()
  void boundSource
    .close()
    .catch(() => undefined)
    .finally(() => app.quit())
})

// Note which of the two halves this calls. `serveBundle` here would re-register
// the scheme on every dock-icon click -- the window is gone, the process is not
// -- and `protocol.handle` throws on the second registration, which
// `reportAndQuit` would turn into `app.exit(1)`: the console would quit the
// moment the user asked it to come back.
app.on('activate', () => {
  lifecycle.activate().catch(reportAndQuit)
})
