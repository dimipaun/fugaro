// The installation picker: its window, its menu item, and its channels
// (desktop-console plan Task 3.4b; design sections 3.4 rules 5 and 7, 5.1 rule
// 10, 7.4, D13, D14; owner rulings C8 and C9, 2026-10-18).
//
// This file is new rather than an edit to `window.ts` and `index.ts`: it was
// written in parallel with Task 3.4a's rebuild of the entry point. Since the two
// were merged, the picker's window is built by `buildHardenedWindow`
// (`src/main/window.ts`), the one construction path the dashboard and the
// refusal screen go through too, with `consoleWebPreferences()` plus the
// picker's own preload; and a choose goes through the lifecycle's handshake
// path (`lifecycle.choose`, via `onChosen` in `src/main/index.ts`).
//
// The picker is a *separate window* (C9) and not a panel in the dashboard's
// header, and the reason is the surface rather than the layout: the dashboard's
// document is exposed to fourteen operations -- the CLI reads, the handshake,
// the link handover and `installation.current` -- and the picker's job is to
// write a file. Two documents, two preloads, two channel prefixes, and a sender
// gate narrowed to one document each (`isSenderOnDocument` in `src/main/ipc.ts`) --
// so a frame on the dashboard cannot reach `fugaro:installations/remove`, and a
// frame on the picker cannot reach `runs.list`.
//
// What the picker may write, and when: **only `installations.json`, and only on
// an add, a choose or a remove.** It never writes on a load, on a pick, or on a
// failed validation, and it never deletes or rewrites a file it refused to read.

import { join } from 'node:path'

import {
  INSTALLATION_OPERATIONS,
  NO_ACTIVE_INSTALLATION,
  createInstallationFileStore,
  createInstallationRegistry,
  installationChannel,
  installationOperationFor,
  installationStorePath,
  INSTALLATIONS_FILE_NAME,
  probeInstallation,
  refusedInstallationStore,
  validateInstallation,
  validateInstallationArguments,
} from './installations'
import type { InstallationCandidate } from '../source/cli'
import type {
  ActiveInstallation,
  InstallationArguments,
  InstallationFileStore,
  InstallationFs,
  InstallationOperationName,
  InstallationRegistry,
  InstallationRow,
  ValidationDeps,
} from './installations'
import { parseInstallationsDocument } from './installlist'
import {
  GETTING_STARTED_URL,
  NO_ONBOARDING,
  firstInstallationState,
  installCliState,
} from './onboarding'
import type { InstallationsListReply, InstallationsListRow } from './onboarding'
import { SENDER_REFUSAL, isSenderOnDocument } from './ipc'
import { SPAWN_COMMANDS } from './spawn'
import type { ConsoleEventLike, IpcMainLike } from './ipc'
import { PICKER_ENTRY_PATH, PICKER_ENTRY_URL } from './protocol'
import { verifyCandidate } from '../source/cli'
import {
  PICKER_COMMANDS,
  assertSpawnSurface,
  binDirectories,
  planFor,
  privateWorkingDirectory,
  resolveBinary,
  runToCompletion,
  cliSentence,
} from './spawn'
import { buildHardenedWindow, consoleWebPreferences, openExternalValidated } from './window'
import type { BrowserWindow, BrowserWindowConstructorOptions, Session, Shell } from 'electron'
import type { CliCompatibility } from '../shared/version'
import type { ChildSpawner, SpawnPlan } from './spawn'
import type { SpawnLogWriter } from './spawnlog'

/** PICKER_ENTRY_PATH is the picker's document, and PICKER_ENTRY_URL the URL the
 * picker's window loads and the one its channels are gated on. They are
 * `protocol.ts`'s, beside the dashboard's and the refusal's, and `SERVED_FILES`
 * names the document and its script; re-exported here for the picker's tests. */
export { PICKER_ENTRY_PATH, PICKER_ENTRY_URL }

/** INSTALLATIONS_MENU_LABEL is the application menu item's label, and it is the
 * only door to the picker (C9). */
export const INSTALLATIONS_MENU_LABEL = 'Installations…'

/** PICKER_WINDOW_TITLE is the picker's window title. It carries no mode and no
 * version: the picker draws a list, and the console's own compatibility is the
 * dashboard's to state. */
export const PICKER_WINDOW_TITLE = 'Fugaro console — installations'

/** PICKER_WINDOW_SIZE is the picker's size. It is smaller than the dashboard's
 * on purpose: this is a list of the installations the CLI found, with one button
 * per row, and -- on a machine with none, or with no `fugaro` at all -- one of
 * the two onboarding screens.
 *
 * **There is no file dialog in this window any more** (owner's ruling of
 * 2026-10-20): the console asks the operator for no binary and no config, so
 * `dialog.showOpenDialog` is not called from anywhere in this file, and there is
 * no `DialogLike` seam for it.
 */
export const PICKER_WINDOW_SIZE = Object.freeze({ width: 720, height: 520 })

/**
 * OPEN_EXTERNAL_FAILED is what the link row answers when nothing was opened,
 * and it is a sentence rather than an empty success: the renderer draws the
 * main process's `reason` and nothing else, so a swallowed `false` would leave
 * an operator who clicked "Open the getting-started guide" with a window that
 * did nothing and said nothing. It names no URL and no host -- the reason a
 * handover failed is the operating system's, and this console does not pretend
 * to know it.
 */
export const OPEN_EXTERNAL_FAILED = 'the console could not open the link in the browser'

/** MenuLike is the slice of Electron's `Menu` this file uses. */
export interface MenuLike {
  buildFromTemplate(template: readonly unknown[]): unknown
  setApplicationMenu(menu: unknown): void
}

/** InstallationPickerDeps is everything the picker is built from. */
export interface InstallationPickerDeps {
  readonly BrowserWindow: new (options: BrowserWindowConstructorOptions) => BrowserWindow
  readonly session: Session
  readonly ipcMain: IpcMainLike
  readonly menu: MenuLike
  /** openExternal is the console's one external-link path,
   * `openExternalValidated(shell, ...)` in `src/main/window.ts`, handed in for
   * the reason everything else here is: the unit test drives it under plain
   * Node. The operation that calls it takes **no arguments**, so the URL is this
   * process's own constant and a renderer cannot name one. */
  readonly openExternal: (url: string) => Promise<boolean>
  /** The app's own directories. `userData` is where the store lives, and
   * `home` is passed to the path refusal -- this file never calls `homedir()`. */
  readonly userData: string
  readonly home: string
  /** The app's root, from which `pickerPreloadPath` derives the preload. */
  readonly appPath: string
  readonly appEnv: Readonly<Record<string, string | undefined>>
  readonly spawner?: ChildSpawner
  readonly spawnLog?: SpawnLogWriter
  readonly fs?: InstallationFs
  /** prefixes, when given, replaces rule 4's well-known directories
   * (`binDirectories`' own parameter). It exists for the same reason `home`
   * does: a unit test must be able to say "this machine has no `fugaro`"
   * without a real `/usr/local/bin` on the machine under test deciding the
   * answer. **The shipped app never passes it** -- `installInstallationPicker`
   * leaves it out, so the rule's own list is the one that runs. */
  readonly prefixes?: readonly string[] | undefined
  /**
   * onChosen is the seam the entry point consumes: it is called after a choose
   * has run the handshake and persisted `active`, and `src/main/index.ts` hands
   * the answer to `lifecycle.choose`, which replaces the stored compatibility,
   * registers the surface if it was not yet and reopens the window through
   * `openWindowFor`. Binding a `CliSource` to the chosen installation is Task
   * 3.4c's. The picker states the fact and hands it over rather than reaching
   * into the lifecycle.
   */
  readonly onChosen?: (installation: {
    readonly project: string
    /** The answer of the handshake the choose just ran: never `refused`. */
    readonly compatibility: CliCompatibility
    /** The binary rule 4 resolved for it -- a fact of this run, which the
     * dashboard's header names and which is never stored (owner's ruling of
     * 2026-10-20). */
    readonly resolvedBinaryPath: string
  }) => Promise<void> | void
  /** onRemoved is the same seam for a removal that took the active installation
   * away: the console has none, and its dashboard must say so. */
  readonly onRemoved?: (id: string) => void
}

/** InstallationPicker is what the entry point holds. */
export interface InstallationPicker {
  /** installation is the read `handlersFor` answers `installation.current`
   * with. It is a function so the answer is the store's *now*, not the one it
   * had when the handlers were registered. */
  installation(): ActiveInstallation
  /** open shows the picker, focusing the window that is already there rather
   * than opening a second one. */
  open(): Promise<void>
  /** openOnFirstRun is D13's "the picker opens once, automatically, on that
   * first run": it opens only when no installation has been chosen, and only
   * once per process. */
  openOnFirstRun(): Promise<void>
  /** installMenu puts `Installations…` in the application menu. It is called in
   * **every** mode, `refused` included: choosing an installation is how an
   * operator gets out of a refusal. */
  installMenu(): void
  /** refusal is the sentence the store's file was refused with, or null. The
   * dashboard says it, naming the path, and an add is refused until it is
   * fixed. */
  readonly refusal: string | null
  readonly storePath: string
  /** remember records the compatibility a handshake read for one project --
   * the startup's, from `onDecided` in `src/main/index.ts` -- so
   * `installation()` and the rows carry its mode and version (review S1). */
  remember(project: string, compatibility: CliCompatibility): void
  /** close is the window's own teardown hook. It is called when the picker is
   * closed, and there is nothing left in this file for it to drop -- the pick
   * tokens went with the form (owner's ruling of 2026-10-20) -- so it is kept as
   * the entry point's seam rather than removed from under its caller. */
  close(): void
}

/** installationsMenuTemplate is the application menu, as a plain value.
 *
 * It is a template rather than a built menu so the unit test can assert the
 * item's label and that activating it calls `open` -- the one door to the
 * picker, and a door a test can hold. It keeps a **Quit** item because replacing
 * the application menu removes the default one, and an app whose menu has no
 * Quit is an app a person has to force-quit. */
export function installationsMenuTemplate(options: {
  readonly onOpen: () => void
  readonly onQuit: () => void
  readonly appName: string
}): readonly unknown[] {
  // **Not frozen**, unlike every other table in this module: Electron's
  // `Menu.buildFromTemplate` sorts the template in place and assigns each
  // item's `submenu`, and a frozen template makes it throw ("Cannot assign to
  // read only property 'submenu'") before the app has a window -- which is what
  // the first real launch of this file did. A fresh value per call is the
  // guard instead: nothing else holds a reference to it.
  //
  // The Edit and Window menus are Electron's own roles, kept beside ours
  // (review S10): replacing the application menu drops the default one, and on
  // macOS the editing shortcuts (copy, paste, select all -- the picker's project
  // field needs them) and the window ones (close, minimise) are menu items, not
  // built-ins.
  return [
    {
      label: options.appName,
      submenu: [
        { label: INSTALLATIONS_MENU_LABEL, click: options.onOpen },
        { type: 'separator' },
        { role: 'quit', click: options.onQuit },
      ],
    },
    { role: 'editMenu' },
    { role: 'windowMenu' },
  ]
}

/** pickerOptions is the picker window's construction options: the dashboard's
 * own `consoleWebPreferences()` with **one** addition -- the picker's preload.
 * There is no relaxation here and there is none to make: the picker is a
 * hardened window like every other one this app opens. */
export function pickerOptions(preloadPath: string): BrowserWindowConstructorOptions {
  return {
    width: PICKER_WINDOW_SIZE.width,
    height: PICKER_WINDOW_SIZE.height,
    show: false,
    title: PICKER_WINDOW_TITLE,
    webPreferences: { ...consoleWebPreferences(), preload: preloadPath },
  }
}

/**
 * pickerPreloadPath is where the build puts the picker's preload, derived from
 * the app's own root (`app.getAppPath()`, which is what `rendererRoot()` in
 * `src/main/index.ts` derives the served documents from too) rather than from
 * `__dirname`.
 *
 * That is not style: the typecheck configuration is `module: ESNext`, where
 * `__dirname` is not a thing this file may name, and a path that is computed
 * from the app root is also the one a test can hold without a build on disk.
 * The preload is compiled by `tsconfig.build.json` into `dist/preload/`, beside
 * the dashboard's (`consolePreloadPath` in `src/main/index.ts`), and the
 * documents are served from the bundler's `dist/bundle/` (Task 3.4a).
 */
export function pickerPreloadPath(appPath: string): string {
  return join(appPath, 'dist', 'preload', 'installations.js')
}

/**
 * createInstallationPicker builds the picker and registers its channels.
 *
 * The store is read **once, here**, before any window exists -- and a refused
 * file is not an exception: `refusal` carries the sentence, the console starts
 * with no installation, and an add is refused until the operator fixes the file.
 */
export function createInstallationPicker(deps: InstallationPickerDeps): InstallationPicker {
  // The spawn surface is asserted here as well as in the dashboard, and the
  // picker's own table is the second argument: `installation.list` is a row the
  // dashboard's fourteen operations do not have, and `assertSpawnSurface` holds
  // it to the picker's one command (`config list --json`, no `--project`, no
  // `--dir`, no declared arguments). A row that grew an argument, or a name
  // outside `INSTALLATION_OPERATION_NAMES`, is refused before a window exists.
  assertSpawnSurface(SPAWN_COMMANDS, PICKER_COMMANDS)
  // A `userData` the path rule refuses -- `$FUGARO_CONFIG` in the home directory
  // makes every `userData` "inside" the CLI's configuration -- is **not** a
  // reason to quit at startup (review S7). The console starts with no store: the
  // refusal is the sentence the picker and the dashboard draw, every add is
  // refused, and nothing is written anywhere.
  let storePath: string
  let store: InstallationFileStore
  try {
    storePath = installationStorePath({ userData: deps.userData, env: deps.appEnv, home: deps.home })
    store = createInstallationFileStore({
      path: storePath,
      ...(deps.fs === undefined ? {} : { fs: deps.fs }),
    })
  } catch (error) {
    storePath = join(deps.userData, INSTALLATIONS_FILE_NAME)
    store = refusedInstallationStore(
      storePath,
      error instanceof Error ? error.message : 'the console has no place to keep its installations',
    )
  }
  /** verify is the pairing check `validateInstallation` runs for a checkout: one
   * allow-listed `doctor --json` from that directory, with that project (rule 7).
   * It is the only thing in this file that reads a config, and it does not read
   * one: the CLI does. */
  const verify = async (candidate: InstallationCandidate): Promise<void> => {
    await verifyCandidate(candidate, {
      appEnv: deps.appEnv,
      ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
      ...(deps.spawnLog === undefined ? {} : { spawnLog: deps.spawnLog }),
      home: deps.home,
    })
  }

  /** validationDeps is what both `probeInstallation` and `validateInstallation`
   * take: the running app's facts and the one verification seam. */
  const validationDeps = (): ValidationDeps => ({
    appEnv: deps.appEnv,
    home: deps.home,
    ...(deps.prefixes === undefined ? {} : { prefixes: deps.prefixes }),
    ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
    ...(deps.spawnLog === undefined ? {} : { spawnLog: deps.spawnLog }),
    ...(deps.fs === undefined ? {} : { fs: deps.fs }),
    verify,
  })
  const registry = createInstallationRegistry({ store })
  // The store is read here, once, before any window: the picker's rows carry the
  // records this console already has (`listReply` merges them, and `rowsOf` adds
  // one per record the CLI did not list), and `refusal` -- the sentence a store
  // file that cannot be read is drawn with -- comes from this read and from
  // nowhere else. A picker that never loaded would draw an empty list over a
  // store full of installations, and would open its first-run screen for an
  // operator who has one.
  registry.load()

  let window: BrowserWindow | null = null
  let openedOnFirstRun = false

  /** workingDirectory is where a probe without a checkout runs (rule 5). */
  const workingDirectory = (): string => privateWorkingDirectory()

  /**
   * discover is the read the whole picker is built on: rule 4's resolution of
   * the installed `fugaro` (the store's own override for the *active*
   * installation first, when it has one), and then `fugaro config list --json`
   * through the picker's own spawn row -- the CLI's enumeration of the
   * installations on this machine, which is the only thing the console draws
   * (design section 3.4 rule 7: it reads no config itself).
   *
   * It never throws: a resolution that failed, a CLI that refused and a document
   * that is not one are all answers, because every one of them is a screen.
   */
  const discover = async (): Promise<
    | { readonly ok: true; readonly installations: readonly { name: string; gcpProject: string; region: string; checkouts: readonly string[] }[]; readonly warnings: readonly string[] }
    | { readonly ok: false; readonly onboarding: InstallationsListReply['onboard']; readonly reason: string; readonly searched: readonly string[] }
  > => {
    const override = registry.active()?.binaryPath
    const configuredPath = override === undefined || override === '' ? {} : { configuredPath: override }
    const searched = binDirectories({
      ...configuredPath,
      home: deps.home,
      ...(deps.prefixes === undefined ? {} : { prefixes: deps.prefixes }),
    })
    const resolution = await resolveBinary({
      ...configuredPath,
      appEnv: deps.appEnv,
      home: deps.home,
      ...(deps.prefixes === undefined ? {} : { prefixes: deps.prefixes }),
      directory: workingDirectory(),
      target: { project: '' },
      ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
      ...(deps.spawnLog === undefined ? {} : { log: deps.spawnLog }),
    })
    if (!resolution.ok) {
      // The screen is drawn from the resolution's own sentence and its own
      // list of directories: this is where the console looked, and it is a fact
      // rather than a guess about the operator's machine.
      return { ok: false, onboarding: installCliState({ reason: resolution.reason, searched }), reason: resolution.reason, searched }
    }
    return readInstallations(resolution.path)
  }

  /** readInstallations spawns the picker's one read with the binary rule 4
   * resolved, and parses what it printed. */
  const readInstallations = async (
    binaryPath: string,
  ): Promise<
    | { readonly ok: true; readonly installations: readonly { name: string; gcpProject: string; region: string; checkouts: readonly string[] }[]; readonly warnings: readonly string[] }
    | { readonly ok: false; readonly onboarding: InstallationsListReply['onboard']; readonly reason: string; readonly searched: readonly string[] }
  > => {
    const plan: SpawnPlan = planFor('installation.list', {}, {
      target: { project: '' },
      binaryPath,
      directory: workingDirectory(),
      appEnv: deps.appEnv,
    })
    let run
    try {
      run = await runToCompletion(plan, {
        ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
        ...(deps.spawnLog === undefined ? {} : { log: deps.spawnLog }),
      })
    } catch (error) {
      // A CLI that timed out or overflowed the cap. It is the console's own
      // sentence, and it is a refusal like any other: nothing is drawn as if the
      // list had been read.
      const sentence = error instanceof Error ? error.message : 'the CLI could not list the installations'
      return { ok: false, onboarding: NO_ONBOARDING, reason: sentence, searched: [] }
    }
    if (run.signal !== null || run.code !== 0) {
      const sentence = cliSentence(run.stderr) ?? `the CLI exited ${String(run.code)} for its installation list`
      return { ok: false, onboarding: NO_ONBOARDING, reason: sentence, searched: [] }
    }
    const parsed = parseInstallationsDocument(run.stdout)
    if (!parsed.ok) return { ok: false, onboarding: NO_ONBOARDING, reason: parsed.reason, searched: [] }
    return { ok: true, installations: parsed.document.installations, warnings: parsed.document.warnings }
  }

  /** rowsOf merges what the CLI listed with what this console has recorded: one
   * row per listed installation, and one more per record the CLI did not list --
   * which is the case that keeps a store written by an older console working
   * (its binary override and its checkout are still there, and it is still
   * openable by name). */
  const rowsOf = (
    discovered: readonly { name: string; gcpProject: string; region: string; checkouts: readonly string[] }[],
  ): readonly InstallationsListRow[] => {
    const stored = registry.rows()
    const rows: InstallationsListRow[] = discovered.map((installation) => {
      const held = stored.find((row) => row.project === installation.name)
      return {
        project: installation.name,
        gcpProject: installation.gcpProject,
        region: installation.region,
        // The CLI's own signal: `gcp_project` is "" exactly when that config
        // could not be read, and the document's warning says why.
        configUnreadable: installation.gcpProject === '',
        listed: true,
        saved: held !== undefined,
        active: held?.active ?? false,
        mode: held?.mode ?? 'unknown',
        ...(held?.checkout === undefined ? {} : { checkout: held.checkout }),
        checkouts: installation.checkouts,
      }
    })
    for (const row of stored) {
      if (discovered.some((installation) => installation.name === row.project)) continue
      rows.push({
        project: row.project,
        gcpProject: '',
        region: '',
        configUnreadable: false,
        listed: false,
        saved: true,
        active: row.active,
        mode: row.mode,
        ...(row.checkout === undefined ? {} : { checkout: row.checkout }),
        checkouts: [],
      })
    }
    return Object.freeze(rows)
  }

  /**
   * lastListed is the document the last successful `list` read, keyed by project
   * name, and it is what a checkout is checked against: the console offers only
   * the directories the CLI itself listed (installlist.ts rule 2), so a renderer
   * that sends a path this map does not hold has named a directory this process
   * never obtained. It is empty until a list has been read, and an empty map
   * offers nothing -- which is the safe direction.
   */
  let lastListed: ReadonlyMap<string, readonly string[]> = new Map()

  /** listReply is D13's list: the onboarding state, the rows, the CLI's own
   * warning lines and the console's view of the active installation. */
  const listReply = async (): Promise<InstallationsListReply> => {
    const found = await discover()
    const base = {
      ...(registry.refusal === null ? {} : { refusal: registry.refusal }),
      path: storePath,
      active: current(),
    }
    if (!found.ok) {
      // A refusal still draws the rows the console itself has: a store entry is
      // the console's own record, and hiding it because the CLI would not list
      // would take away the one installation the operator can still open.
      clearCheckouts()
      return Object.freeze({
        onboard: found.onboarding,
        rows: rowsOf([]),
        warnings: Object.freeze(found.onboarding === NO_ONBOARDING && found.reason !== '' ? [found.reason] : []),
        ...base,
      })
    }
    // What this read listed is what a checkout may be set from, and it replaces
    // the previous read wholesale: a checkout the CLI has stopped listing is one
    // this console no longer holds.
    lastListed = new Map(found.installations.map((installation) => [installation.name, installation.checkouts]))
    const rows = rowsOf(found.installations)
    if (rows.length === 0) {
      // "The CLI listed nothing and the console has nothing" is the one state
      // the first-installation screen is for. A document this console could not
      // read never reaches here.
      return Object.freeze({
        onboard: firstInstallationState(),
        rows,
        warnings: Object.freeze([...found.warnings]),
        ...base,
      })
    }
    return Object.freeze({
      onboard: NO_ONBOARDING,
      rows,
      warnings: Object.freeze([...found.warnings]),
      ...base,
    })
  }

  const open = async (): Promise<void> => {
    if (window !== null && !window.isDestroyed()) {
      window.focus()
      return
    }
    // The same construction path as the dashboard and the refusal screen
    // (`buildHardenedWindow` in `src/main/window.ts`): the three denials, every
    // permission denied, the title kept, shown when ready, one load. Only the
    // options differ, and they are `consoleWebPreferences()` plus this preload.
    let opening: BrowserWindow | null = null
    // closing is set by the window's own `close` event, which Electron emits
    // when the close is asked for -- before the aborted load rejects, and before
    // `isDestroyed()` turns true on every platform (macOS lags it).
    let closing = false
    try {
      await buildHardenedWindow(
        { BrowserWindow: deps.BrowserWindow, session: deps.session },
        pickerOptions(pickerPreloadPath(deps.appPath)),
        PICKER_ENTRY_URL,
        (built) => {
          opening = built
          built.on('close', () => {
            closing = true
          })
          built.on('closed', () => {
            // There is nothing left to drop with the window: the pick tokens
            // went with the form (owner's ruling of 2026-10-20), and this window
            // holds no capability of its own.
            window = null
          })
          // Held before the load, so a second `open()` while this one loads
          // focuses this window rather than building another.
          window = built
        },
      )
    } catch (error) {
      // A picker the operator closed while its document was still loading
      // rejects that load (`ERR_ABORTED`). That is a dismissal, not a failure:
      // rethrowing it would reach `main`'s `reportAndQuit` on the first run and
      // quit the whole console. Any other failure is still thrown.
      const built = opening as BrowserWindow | null
      if (built !== null && (closing || built.isDestroyed())) return
      throw error
    }
  }

  // One chain for every mutation (review S6): a choose, a checkout or a remove
  // runs only after the previous one -- its probe, its write and, for a choose,
  // the window it asked for -- has finished. Two chooses clicked 50 ms apart
  // would otherwise both probe, both persist and both open a dashboard.
  let mutations: Promise<unknown> = Promise.resolve()
  const serially = <T>(body: () => Promise<T>): Promise<T> => {
    const next = mutations.then(body, body)
    mutations = next.catch(() => undefined)
    return next
  }

  const rows = (): readonly InstallationRow[] => registry.rows()
  const current = (): ActiveInstallation => registry.current()

  /** clearCheckouts drops the document a checkout is checked against. It is
   * called when a read refused, so a refusal cannot be followed by a checkout
   * that was never listed. */
  const clearCheckouts = (): void => {
    lastListed = new Map()
  }

  /**
   * chooseInstallation is one click: rule 4's resolution for the project the CLI
   * listed (the store's own override first, when that record has one), the
   * version handshake, and -- only then -- the store and the dashboard.
   *
   * A `refused` CLI is **not** chosen: the console shows the sentence it reached
   * rather than binding a source it cannot drive. Nothing is written on any
   * refusing path, and the pairing check does not run here: choosing is not
   * adding, and a checkout's pairing was proved when it was set.
   */
  const chooseInstallation = async (project: string): Promise<unknown> => {
    const held = registry.entry(project)
    // A project this console never drew is not one it opens: the row is the
    // handle, and the handles are the names the CLI printed (or a record this
    // console already had, which is the no-migration case). Without this, a frame
    // on the picker's document could name any string at all and have rule 4
    // resolve -- and the dashboard be told to drive -- a project that appeared on
    // no screen.
    if (held === null && !lastListed.has(project)) {
      return { ok: false, reason: `no installation is named ${JSON.stringify(project)}` }
    }
    const probed = await probeInstallation(
      {
        project,
        ...(held?.binaryPath === undefined ? {} : { binaryPath: held.binaryPath }),
        ...(held?.configPath === undefined ? {} : { configPath: held.configPath }),
        ...(held?.checkout === undefined ? {} : { checkout: held.checkout }),
      },
      // `probeInstallation` takes everything but the two seams it does not use:
      // the pairing check (a choose runs no `doctor`) and the filesystem (it
      // stats nothing -- a checkout was checked when it was set).
      validationDeps(),
    )
    if (!probed.ok) return { ok: false, reason: probed.reason }
    // The compatibility first, so a row drawn by the list this choose returns
    // already carries the mode the handshake just read.
    registry.remember(project, probed.value.compatibility)
    registry.choose(project)
    // Awaited inside the chain: the next mutation waits for the window this
    // choose asked for.
    await deps.onChosen?.({
      project,
      compatibility: probed.value.compatibility,
      resolvedBinaryPath: probed.value.path,
    })
    return { ok: true, data: await listReply() }
  }

  /**
   * setInstallationCheckout is the one place a repository is asked for, and the
   * screen asks at the moment it needs one (requirement 3): the directories the
   * CLI itself listed for that installation are the only ones this accepts, and
   * an absent `checkout` clears the one that was there.
   *
   * Setting one runs the pairing check (`validateInstallation`, which is rule
   * 7's `doctor --json` from that directory with that project). Clearing one
   * runs nothing: there is no pairing to check without a checkout, and an
   * installation without one is a state the console supports (rule 5).
   */
  const setInstallationCheckout = async (project: string, checkout: string | undefined): Promise<unknown> => {
    if (registry.entry(project) === null && !lastListed.has(project)) {
      return { ok: false, reason: `no installation is named ${JSON.stringify(project)}` }
    }
    if (checkout === undefined) {
      registry.setCheckout(project, undefined)
      return { ok: true, data: await listReply() }
    }
    // Membership of the list the CLI printed, and not a shape check: a path that
    // is absolute and well-formed but was never listed is a directory this
    // console did not obtain, and it is refused by name.
    const offered = lastListed.get(project) ?? []
    if (!offered.includes(checkout)) {
      return {
        ok: false,
        reason: `${checkout} is not one of the checkouts the CLI listed for ${JSON.stringify(project)}`,
      }
    }
    const validated = await validateInstallation({ project, checkout }, validationDeps())
    if (!validated.ok) return { ok: false, reason: validated.reason }
    registry.setCheckout(project, checkout)
    return { ok: true, data: await listReply() }
  }

  /** removeInstallation drops the console's own record. `onRemoved` is the seam
   * that tells the dashboard it has **no** installation, so it fires only when
   * the removal took the active one away -- firing it for a non-active row would
   * tell the dashboard it has nothing to drive while `active` still names an
   * installation the operator can use. */
  const removeInstallation = (project: string): unknown => {
    if (registry.entry(project) === null) return { ok: false, reason: 'no such installation' }
    const before = current()
    registry.remove(project)
    if (before.chosen && before.project === project) deps.onRemoved?.(before.id)
    return { ok: true, data: { rows: rows(), active: current() } }
  }

  const answerFor = async (name: InstallationOperationName, args: InstallationArguments): Promise<unknown> => {
    switch (name) {
      case 'list':
        return { ok: true, data: await listReply() }
      case 'choose':
        return serially(() => chooseInstallation(args['project'] ?? ''))
      case 'checkout':
        return serially(() => setInstallationCheckout(args['project'] ?? '', args['checkout']))
      case 'remove':
        return serially(async () => removeInstallation(args['project'] ?? ''))
      case 'openExternal': {
        // The URL is this process's own constant and the operation takes **no
        // arguments at all** (requirement 4): `GETTING_STARTED_URL` never
        // crosses the IPC boundary, so a frame on the picker's document cannot
        // ask the operating system to open a link of its choosing. The one path
        // is `openExternalValidated(shell, ...)`, handed in as `deps`.
        //
        // **Its boolean decides the reply.** `openExternalValidated` returns
        // whether anything was opened -- a URL its allow-list refused, and a
        // handover the platform declined (no handler for `https:`, a dismissed
        // prompt), both answer `false` -- and a screen that reported success for
        // those would draw nothing at all while the operator waits for a browser
        // that is not coming. The renderer surfaces `reason` and only `reason`,
        // which is why this is a refusal and not a field of a successful reply.
        const opened = await deps.openExternal(GETTING_STARTED_URL)
        if (!opened) return { ok: false, reason: OPEN_EXTERNAL_FAILED }
        return { ok: true, data: { opened: GETTING_STARTED_URL } }
      }
    }
  }

  // The picker's channels, one per row, each gated on the picker's own document
  // and nothing else. Registration is here rather than at a caller's because
  // this is the only module that knows the picker's surface.
  for (const operation of INSTALLATION_OPERATIONS) {
    const name = operation.name
    deps.ipcMain.handle(installationChannel(name), async (event: ConsoleEventLike, raw: unknown) => {
      if (!isSenderOnDocument(event, PICKER_ENTRY_URL)) return { ok: false, reason: SENDER_REFUSAL }
      try {
        const checked = validateInstallationArguments(installationOperationFor(name), raw)
        if (!checked.ok) return { ok: false, reason: checked.reason }
        return await answerFor(name, checked.args)
      } catch (error) {
        return { ok: false, reason: error instanceof Error ? error.message : 'the picker could not answer' }
      }
    })
  }

  return {
    installation: current,
    open,
    async openOnFirstRun(): Promise<void> {
      if (openedOnFirstRun) return
      openedOnFirstRun = true
      if (registry.current().chosen) return
      await open()
    },
    installMenu(): void {
      deps.menu.setApplicationMenu(
        deps.menu.buildFromTemplate(
          installationsMenuTemplate({
            appName: 'Fugaro console',
            onOpen: () => {
              open().catch((error: unknown) => {
                console.error('[console] the installation picker could not be opened:', error)
              })
            },
            onQuit: () => {
              // The real quit is Electron's `role: 'quit'`; the callback exists
              // so a test can hold the item's action without an app to quit.
            },
          }),
        ),
      )
    },
    get refusal(): string | null {
      return registry.refusal
    },
    storePath,
    remember(project: string, compatibility: CliCompatibility): void {
      registry.remember(project, compatibility)
    },
    close(): void {
      // Nothing left to drop: the window's own `closed` handler above is the
      // whole of the teardown. Kept as the entry point's seam.
    },
  }
}

/**
 * installInstallationPicker is the entry point's one call, and the whole of
 * Task 3.4b's edit to `src/main/index.ts`: it builds the picker from the running
 * Electron and returns it. The entry point then
 *
 *   1. passes `picker.installation` to `handlersFor` as its `installation` dep,
 *      which is what answers `installation.current` (Task 3.4a's join), and
 *   2. calls `picker.installMenu()` once the app is ready and
 *      `picker.openOnFirstRun()` when the handshake has settled.
 *
 * Both of those are the entry point's because both need the lifecycle it owns;
 * neither is a second copy of anything here.
 */
export function installInstallationPicker(options: {
  readonly electron: {
    readonly BrowserWindow: new (options: BrowserWindowConstructorOptions) => BrowserWindow
    readonly session: Session
    readonly ipcMain: IpcMainLike
    readonly Menu: MenuLike
    /** shell is Electron's `shell`, and it is handed to
     * `openExternalValidated` -- the console's one external-link path -- rather
     * than used here: this module never calls `shell.openExternal` itself. */
    readonly shell: Shell
    readonly app: {
      getPath(name: string): string
      getAppPath(): string
      quit(): void
    }
  }
  readonly appEnv: Readonly<Record<string, string | undefined>>
  readonly spawner?: ChildSpawner
  readonly spawnLog?: SpawnLogWriter
  readonly fs?: InstallationFs
  readonly onChosen?: InstallationPickerDeps['onChosen']
  readonly onRemoved?: InstallationPickerDeps['onRemoved']
}): InstallationPicker {
  const { electron } = options
  return createInstallationPicker({
    BrowserWindow: electron.BrowserWindow,
    session: electron.session,
    ipcMain: electron.ipcMain,
    menu: electron.Menu,
    // The one external-link path, unchanged and not bypassed: the URL this
    // console may open is checked by `validateExternalUrl` (its scheme, its
    // credentials, its port) and its host against `ALLOWED_EXTERNAL_HOSTS`
    // before Electron is asked to open anything.
    openExternal: (url: string) => openExternalValidated(electron.shell, url),
    userData: electron.app.getPath('userData'),
    home: electron.app.getPath('home'),
    appPath: electron.app.getAppPath(),
    appEnv: options.appEnv,
    ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
    ...(options.spawnLog === undefined ? {} : { spawnLog: options.spawnLog }),
    ...(options.fs === undefined ? {} : { fs: options.fs }),
    ...(options.onChosen === undefined ? {} : { onChosen: options.onChosen }),
    ...(options.onRemoved === undefined ? {} : { onRemoved: options.onRemoved }),
  })
}

/** NO_ACTIVE_INSTALLATION is re-exported so the store's own empty answer is
 * readable from the module that owns the picker's window, and so a test that
 * asserts it can import it from either. */
export { NO_ACTIVE_INSTALLATION }
