// The console's Electron main process entry point (desktop-console plan Task
// 1.1).
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
// Note where the document is read from. `tsc` emits JavaScript only, so the
// bundle's files stay in the source tree and the handler's root is
// `src/renderer` under the app's own path. Task 4.1's builder is the one that
// gets to decide where a packaged console keeps its renderer files, and it will
// have to pack them; until then there is no packaging step to disagree with.

import { join } from 'node:path'

import { app, BrowserWindow, Menu, dialog, ipcMain, protocol, session } from 'electron'

import { handleAppRequests, registerAppScheme } from './protocol'
import { installInstallationPicker } from './picker'
import type { InstallationPicker } from './picker'
import { createConsoleLifecycle, installWebContentsSecurity, versionReadFrom } from './window'
import type { ConsoleWindowDeps, VersionRead } from './window'
import { privateWorkingDirectory, resolveBinary } from './spawn'
import type { SpawnTarget } from './spawn'

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
 */
function rendererRoot(): string {
  return join(app.getAppPath(), 'src', 'renderer')
}

function reportAndQuit(error: unknown): void {
  console.error('[console] the window could not be created:', error)
  app.exit(1)
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
 * HANDSHAKE_TARGET is the target the startup version read runs with: no project
 * and no config path, because `fugaro version` takes neither -- the spawn
 * table's row for `cli.version` is the one with `takesProject: false`, and the
 * CLI's own `newVersionCmd` has no flags at all (see `COMMANDS_WITHOUT_PROJECT`
 * in `src/main/spawn.ts`). The installation the operator chooses is D13's, and
 * it does not exist yet: inventing a project name here, or falling back to the
 * ambient `FUGARO_PROJECT`, would be a target nobody chose (design section 3.4
 * rule 1). The probe's child therefore sees `FUGARO_PROJECT` set to the empty
 * string, which is what `childEnvironment` makes of a target with no project,
 * and `version` is the one command that does not read it.
 */
const HANDSHAKE_TARGET: SpawnTarget = Object.freeze({ project: '' })

/**
 * readCliVersion is the handshake's reader: rule 4's resolution (Task 1.3) and
 * the version line of the `fugaro` it finds.
 *
 * It reads the machine and nothing else -- no state from a previous run, no
 * cached answer -- because the question the handshake asks is "which CLI is the
 * console about to drive, right now". A resolution that fails crosses into the
 * compatibility as a **category** (`versionReadFrom` in `window.ts`), never as
 * `resolution.reason`, which embeds what the binary printed; and a resolution
 * or a probe that *throws* is left to `runVersionHandshake`, which turns it into
 * the refusal with the console's own sentence rather than a crash.
 *
 * The child runs in `privateWorkingDirectory()` -- the same owner-only 0700
 * directory every spawn without a checkout uses (rule 5, `src/source/cli.ts`'s
 * `verifyCandidate`), so the probe is not run from wherever the app happened to
 * be launched.
 */
async function readCliVersion(): Promise<VersionRead> {
  const resolution = await resolveBinary({
    appEnv: process.env,
    directory: privateWorkingDirectory(),
    target: HANDSHAKE_TARGET,
  })
  return versionReadFrom(resolution)
}

/** windowDeps is the Electron surface the windows are built with.
 * `session.defaultSession` is read here rather than at module scope because it
 * does not exist until the app is ready. */
function windowDeps(): ConsoleWindowDeps {
  return { BrowserWindow, session: session.defaultSession }
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
})

/**
 * main is what `whenReady` resolves into: serve the bundle once, then run the
 * startup. Everything a second call would repeat is in `lifecycle.activate`.
 */
/**
 * picker is the installation picker (Task 3.4b): the store in
 * `<userData>/installations.json`, the `Installations…` menu item, and the
 * picker's own window and channels. It is built **after** `whenReady`, because
 * `session.defaultSession` and `app.getPath` do not exist before it.
 *
 * It is a module-level binding rather than a local because the entry point's
 * remaining join -- passing `picker.installation` to `handlersFor` as the
 * `installation` dep that answers `installation.current` -- is the
 * renderer-entry task's (3.4a), and that task needs the picker to still be
 * reachable from wherever it builds the handlers.
 */
let picker: InstallationPicker | null = null

async function main(): Promise<void> {
  serveBundle()
  picker = installInstallationPicker({
    electron: { BrowserWindow, session: session.defaultSession, ipcMain, dialog, Menu, app },
    appEnv: process.env,
  })
  // The menu is installed in **every** mode, `refused` included: choosing an
  // installation is how an operator gets out of a refusal, and a console that
  // hid the door would be a console with no way back.
  picker.installMenu()
  await lifecycle.start()
  // D13's first run: with no installation chosen the console makes no guess,
  // says so, and opens the picker once.
  await picker.openOnFirstRun()
}

app.whenReady().then(main).catch(reportAndQuit)

// The console is a single window: closing it quits, except on macOS, where the
// convention is that the app stays running and `activate` brings the window
// back.
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})

// Note which of the two halves this calls. `serveBundle` here would re-register
// the scheme on every dock-icon click -- the window is gone, the process is not
// -- and `protocol.handle` throws on the second registration, which
// `reportAndQuit` would turn into `app.exit(1)`: the console would quit the
// moment the user asked it to come back.
app.on('activate', () => {
  lifecycle.activate().catch(reportAndQuit)
})
