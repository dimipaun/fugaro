// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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
// document is exposed to twenty-four operations -- the CLI reads, the handshake,
// the link handover, `installation.current` and the rail's six -- and the
// picker's job is to add an installation by checkout path. Four documents,
// three preloads, three channel prefixes, and a sender gate narrowed to one
// document each (`isSenderOnDocument` in `src/main/ipc.ts`
// for the dashboard's and the picker's, and the open confirmation window's own
// `webContents` in `src/main/confirm.ts`) --
// so a frame on the dashboard cannot reach `fugaro:installations/remove`, and a
// frame on the picker cannot reach `runs.list`.
//
// What the picker may write, and when: **only `installations.json`, and only on
// an add, a choose or a remove.** It never writes on a load, on a pick, or on a
// failed validation, and it never deletes or rewrites a file it refused to read.

import { realpathSync } from 'node:fs'
import { tmpdir } from 'node:os'
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
  StoredInstallation,
  InstallationArguments,
  InstallationFileStore,
  InstallationFs,
  InstallationOperationName,
  InstallationRegistry,
  InstallationRow,
  ValidationDeps,
} from './installations'
import { parseInstallationsDocument } from './installlist'
import type { DiscoveredInstallation, DiscoveredRepository } from './installlist'
import { TEMP_ROOTS, classifyInstallation, openProject, suggestedCheckouts } from './installationstate'
import type { ConsoleFacts } from './installationstate'
import {
  FUGARO_INSTALL_COMMAND,
  GETTING_STARTED_URL,
  NO_ONBOARDING,
  firstInstallationState,
  installCliState,
} from './onboarding'
import type {
  InstallationsCheckoutChoice,
  InstallationsListReply,
  InstallationsListRow,
} from './onboarding'
import { SENDER_REFUSAL, isSenderOnDocument } from './ipc'
import { SPAWN_COMMANDS } from './spawn'
import type { ConsoleEventLike, IpcMainLike } from './ipc'
import { PICKER_ENTRY_PATH, PICKER_ENTRY_URL } from './protocol'
import { verifyCandidate } from '../source/cli'
import {
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

/** UPDATE_AUTO_LABEL and UPDATE_NOW_LABEL are the two items the owner's ruling
 * D768 (a) of 2026-10-30 puts in the application menu: the visible off switch
 * the design's D5 asks for, and a check the operator can ask for now. Both are
 * macOS only, and only while an update controller is running -- the entry point
 * is where that is decided, and it hands them over only then. */
export const UPDATE_AUTO_LABEL = 'Check for Updates Automatically'
export const UPDATE_NOW_LABEL = 'Check for Updates Now'

/** CRASH_SEND_ITEM_LABEL is the crash reporter's own switch, and it is one item
 * with the note in it rather than two: the checkbox says what it does ("Send
 * Crash Reports"), and the parenthetical says when the change takes effect. The
 * reporter is started once, in the entry point's own startup, so a click here is
 * a decision about the **next launch** -- turning reporting off does not
 * interrupt the client this run started, and nothing an operator can click does.
 * Saying so on the item is the honest description of that; a second, greyed-out
 * menu line stating it would be an item nobody can focus or read out. Unlike the
 * two items above it, this one is not macOS-only: it is the console's own setting
 * rather than the updater's, so the entry point hands it over on every platform
 * the console ships a menu on. */
export const CRASH_SEND_ITEM_LABEL = 'Send Crash Reports (applies at the next launch)'

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

/**
 * REVEAL_FAILED is what the reveal affordance answers when nothing was opened,
 * for the reason OPEN_EXTERNAL_FAILED is a sentence: the renderer draws the
 * main process's `reason` and nothing else. The ways to get here -- a project
 * this console does not have, a record with no checkout, a handover the platform
 * declined, and a caller that handed in no reveal at all -- are deliberately one
 * sentence, because from the operator's seat they are one fact. It names no
 * directory: for a WSL row the path the file manager was handed is a Windows
 * spelling, and this console keeps that spelling out of the renderer's reach
 * (D3(a)).
 */
export const REVEAL_FAILED = 'the console could not open the installation’s directory'

/** UpdateMenuOptions is the off switch as the menu sees it: the value the
 * checkbox draws, and the two things the items do with it. Plain values and
 * callbacks rather than the store itself, so that the template stays a plain
 * value a unit test can read and this module keeps knowing nothing about where
 * the switch is kept. */
export interface UpdateMenuOptions {
  readonly auto: boolean
  /** setAuto moves the switch and answers the value it holds afterwards, read
   * from wherever the switch is kept: Electron has already drawn the state this
   * click moved its checkbox to, and a write that did not land is only visible
   * as this answer disagreeing with it. */
  readonly setAuto: (on: boolean) => boolean
  readonly checkNow: () => void
}

/** CrashMenuOptions is the crash reporter's switch as the menu sees it, in the
 * same shape and for the same reason as `UpdateMenuOptions`: the value the
 * checkbox draws, and the one thing the item does with it. What the switch *is*
 * -- `crash.json`, its `O_NOFOLLOW` read and its atomic write -- stays in
 * `./crashstore`, which this module never imports. */
export interface CrashMenuOptions {
  readonly send: boolean
  /** setSend moves the switch and answers the value it holds afterwards, read
   * from the file, for the reason `setAuto` does: the write can fail, and the
   * checkbox Electron already moved is then not this console's state. */
  readonly setSend: (on: boolean) => boolean
}

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
  /** reveal opens one directory in the platform's file manager -- Electron's
   * `shell.showItemInFolder`, handed in for the same reason `openExternal` is:
   * the unit test drives it under plain Node. Like `openExternal` and unlike
   * every other operation here, **the operation takes no path**. The renderer
   * names a project; the path handed to the file manager is computed in this
   * process from its own record (`explorerPathFor`, T-D3), so a frame on the
   * picker's document cannot ask Windows to open a directory of its choosing.
   * It answers whether anything was opened, for the same reason
   * `openExternalValidated` does: a screen told "opened" for a handover the
   * platform declined would draw nothing while the operator waits. Optional
   * because a caller that never shows a reveal affordance (a headless test of
   * the rest of the picker) need not hand one in. */
  readonly reveal?: ((path: string) => Promise<boolean> | boolean) | undefined
  /** The app's own directories. `userData` is where the store lives, and `home`
   * is the operator's home as this app's own environment names it: it is what
   * rule 4's `~` expands to (`binDirectories`' own `home` argument, which
   * defaults to `homedir()`) and what the path refusal is asked about. Nothing in
   * this module calls `homedir()`: `installInstallationPicker`, at the foot of
   * this file, is the one place that decides this value
   * (`options.appEnv['HOME']`, falling back to `app.getPath('home')` when the key
   * is missing or blank), and its own comment says there why the environment's
   * answer and not Electron's. */
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
  /** tempDirectory is the machine's temporary directory, and it is a seam for
   * the same reason `home` and `prefixes` are: the rule that keeps a checkout
   * suggestion off a temporary or tool-owned path (`isToolOwnedPath`) is about
   * *this* machine's temporary root, and a unit test must be able to name it.
   * It defaults to `os.tmpdir()`, which is what the shipped app uses -- the
   * literal roots the rule also refuses (`/tmp`, `/private/tmp`, `/var/folders`)
   * are checked beside it, because a checkout recorded by a tool may spell the
   * one `os.tmpdir()` does not answer. */
  readonly tempDirectory?: (() => string) | undefined
  /** tempRoots replaces `TEMP_ROOTS`, the literal temporary roots refused beside
   * `tempDirectory`, for the same reason: a unit test that sets a real checkout
   * has to create it under the OS's temporary directory. **The shipped app never
   * passes it.** */
  readonly tempRoots?: readonly string[] | undefined
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
  /**
   * onRecordRemoved is the removal's other half, and it fires after **every**
   * successful removal -- the active row's and any other's -- with the
   * **project** those rows are keyed by.
   *
   * It exists beside `onRemoved` rather than inside it because the two answer
   * different questions. `onRemoved` is about the dashboard: it fires only when
   * the removal took the active installation away, and it hands over the record
   * id the dashboard keys its own state by. A launch in flight is not about the
   * active installation at all -- it names its own -- so a window open for a
   * project whose record has just gone must close whether or not that row was
   * the active one, and the launcher maps a **project** to the run id to close
   * (`LaunchRunner.cancelFor`). Folding it into `onRemoved` would make the
   * cancel fire for the wrong removals and hand it an id it cannot use.
   */
  readonly onRecordRemoved?: (project: string) => void
  /** updateMenu is the two update items of D768 (a), when this console has a
   * controller to offer them: absent on everything that is not a packaged macOS
   * app, where the entry point passes nothing and the menu carries neither
   * item. */
  readonly updateMenu?: UpdateMenuOptions | undefined
  /** crashMenu is the crash reporter's checkbox, which is the console's own
   * setting rather than the updater's: the entry point hands it over on the
   * platforms the console ships a menu on, controller or no controller. */
  readonly crashMenu?: CrashMenuOptions | undefined
}

/** InstallationPicker is what the entry point holds. */
export interface InstallationPicker {
  /** installation is the read `handlersFor` answers `installation.current`
   * with. It is a function so the answer is the store's *now*, not the one it
   * had when the handlers were registered.
   *
   * It answers the **session installation** when this run has one -- the row the
   * console is open on, which is not the store's `active` when the operator's
   * choice cannot be read (`openInstallation` below) -- and the store's own
   * answer otherwise. */
  installation(): ActiveInstallation
  /**
   * openInstallation is the owner's open-first-readable rule (ruling of
   * 2026-10-25), run **once at startup, before the handshake**: it reads the list
   * the rail draws from, decides which installation this console is open on
   * (`openProject`: the remembered choice when the CLI's document says it can be
   * read, the first readable row otherwise, the first row when nothing is), and
   * holds that decision as this run's session installation.
   *
   * **It writes nothing.** The store keeps the operator's choice -- "never drop
   * the remembered choice from the saved store" -- so the session's record is the
   * store's own when there is one and an in-memory one otherwise
   * (`registry.sessionRecordFor`).
   *
   * The read it makes is the rail's own, and the first `list` answers it rather
   * than spawning `config list --json` a second time in the same instant
   * (`bootReply`). It never throws: a console whose list read failed has nothing
   * worse to do than open what the store says, which is what a session of `null`
   * leaves it doing.
   */
  openInstallation(): Promise<void>
  /** open shows the picker, focusing the window that is already there rather
   * than opening a second one. Since the owner's design ruling of 2026-10-25 it
   * is the rail's `+` door (`installation.open` on the dashboard's surface) as
   * well as the menu item: **the console no longer opens it by itself**, which
   * is what "the console never shows a picker screen" means here. */
  open(): Promise<void>
  /** answer is the picker's own dispatcher, exposed so the dashboard's surface
   * can reach the same six answers from the console's document
   * (`handlersFor`'s `installations` dep, `src/source/source.ts`). It answers in
   * this module's reply shape, and the join to the dashboard's is one place:
   * `rail` in `src/source/source.ts`.
   *
   * The validated arguments are the picker's own (`InstallationArguments`), so
   * a caller has already been through one closed schema before it gets here --
   * the dashboard's schema for the dashboard's document, the picker's for the
   * picker's. Both admit the same values, which is what
   * `test/unit/ipc.test.ts` holds them to. */
  answer(name: InstallationOperationName, args: InstallationArguments): Promise<unknown>
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
  readonly updateMenu?: UpdateMenuOptions | undefined
  readonly crashMenu?: CrashMenuOptions | undefined
}): readonly unknown[] {
  const updateMenu = options.updateMenu
  const crashMenu = options.crashMenu
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
        // The update items, when there is a controller to move (D768 (a)). The
        // checkbox is Electron's own: its click hands over the item, whose
        // `checked` is the state the click has just moved it to, and that is
        // what moves the switch -- a value this file toggled itself could
        // disagree with what the user sees after the menu redraws it.
        ...(updateMenu === undefined && crashMenu === undefined
          ? []
          : [
              { type: 'separator' },
              ...(updateMenu === undefined
                ? []
                : [
                    {
                      label: UPDATE_AUTO_LABEL,
                      type: 'checkbox',
                      checked: updateMenu.auto,
                      click: (item: { readonly checked: boolean }) => {
                        updateMenu.setAuto(item.checked)
                      },
                    },
                    {
                      label: UPDATE_NOW_LABEL,
                      click: () => {
                        updateMenu.checkNow()
                      },
                    },
                  ]),
              // The crash reporter's switch is Electron's own checkbox too, and
              // for the same reason: the click hands over an item whose `checked`
              // the click itself has just moved, and that is the state the store
              // is asked to keep.
              ...(crashMenu === undefined
                ? []
                : [
                    {
                      label: CRASH_SEND_ITEM_LABEL,
                      type: 'checkbox',
                      checked: crashMenu.send,
                      click: (item: { readonly checked: boolean }) => {
                        crashMenu.setSend(item.checked)
                      },
                    },
                  ]),
            ]),
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
  // The spawn surface is asserted here as well as in the dashboard: there is
  // **one** table since the owner's design ruling of 2026-10-25 (the rail's
  // `installation.list` is the dashboard's own operation now, so the picker's
  // second table went with it), and `assertSpawnSurface` holds every row to the
  // operation it serves -- `config list --json` with no `--project`, no `--dir`
  // and no declared argument among them. A row that grew an argument is refused
  // before a window exists.
  assertSpawnSurface(SPAWN_COMMANDS)
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
    | { readonly ok: true; readonly installations: readonly DiscoveredInstallation[]; readonly warnings: readonly string[] }
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
    | { readonly ok: true; readonly installations: readonly DiscoveredInstallation[]; readonly warnings: readonly string[] }
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
   * openable by name).
   *
   * **Every row carries its own state** (owner's design ruling of 2026-10-25):
   * `classifyInstallation` turns the facts below into the mark the rail draws and
   * the sentence its issue view shows, and `suggestedCheckouts` is what the row
   * offers as a directory to use this installation from. Neither is a guess made
   * here: the facts are the CLI's own document, the store's own refusal, the
   * resolution's own sentence and the CLI's own warning lines.
   */
  /**
   * session is the record of the installation this console is **open on**
   * without having chosen one: `null` when the open row is the store's own active
   * installation, and an in-memory record otherwise (`registry.sessionRecordFor`,
   * which is where the "never written down" rule lives). `markOpen` assigns it on
   * every list read.
   */
  let session: StoredInstallation | null = null

  const tempDirectory = (): string => deps.tempDirectory?.() ?? tmpdir()
  /** realPathOf is a directory as the file system resolves it, so a symlink
   * into a temporary or tool-owned directory is judged by where it leads. A
   * path that does not resolve (gone, unreadable) is judged as spelled. */
  const realPathOf = (path: string): string => {
    try {
      return realpathSync(path)
    } catch {
      return path
    }
  }
  /** offeredCheckouts is the one list a checkout is offered from and accepted
   * from: the screen draws it, and a checkout write is held to it. */
  const offeredCheckouts = (recorded: string | undefined, listed: readonly string[]): readonly string[] =>
    suggestedCheckouts(recorded, listed, tempDirectory(), deps.tempRoots ?? TEMP_ROOTS, realPathOf)

  /**
   * repositoryIdentities is one installation's recorded repository identities,
   * in the order the CLI listed them and each one once: the CLI prints a row per
   * *checkout*, so two checkouts of one repository are one repository to launch
   * in.
   *
   * Nothing here is resolved, joined or looked up. The identity is the CLI's own
   * `<provider>/<owner>/<name>` verbatim, which is the only form the launch
   * accepts (`installlist.ts`, §6.8): the conversion to a clone URL is the
   * launch's own, and a console that rewrote one here would be deciding what an
   * operator is launching in.
   */
  const repositoryIdentities = (
    repositories: readonly DiscoveredRepository[],
  ): readonly string[] => {
    const seen = new Set<string>()
    const identities: string[] = []
    for (const each of repositories) {
      if (seen.has(each.repository)) continue
      seen.add(each.repository)
      identities.push(each.repository)
    }
    return Object.freeze(identities)
  }

  /**
   * checkoutChoicesOf is one installation's menu for the dashboard's and the
   * Graph's chooser: **one entry per repository**, in the order the CLI listed
   * them, over the rows it re-validated and enrolled.
   *
   * Two rules, and both are the reason this is built here rather than in the
   * renderer. The directory must be one the console may offer at all
   * (`offeredCheckouts`, which drops the tool-owned and temporary paths -- the
   * CLI remembers an agent's own worktrees, and a menu that offered one would
   * offer a directory the write below then refuses), and a repository with
   * several of them is **one** entry: the operator picks a repository, so the
   * first surviving path stands for it and `more` says how many others the CLI
   * remembers, rather than five rows of one repository pushing the rest off the
   * menu. The count is read off the surviving list, so it never claims a
   * directory that was dropped.
   */
  const checkoutChoicesOf = (installation: DiscoveredInstallation): readonly InstallationsCheckoutChoice[] => {
    const usable = new Set(
      offeredCheckouts(
        undefined,
        installation.checkoutChoices.map((one) => one.path),
      ),
    )
    const order: string[] = []
    const byRepository = new Map<string, string[]>()
    for (const one of installation.checkoutChoices) {
      if (!usable.has(one.path)) continue
      const held = byRepository.get(one.repository)
      if (held === undefined) {
        byRepository.set(one.repository, [one.path])
        order.push(one.repository)
        continue
      }
      held.push(one.path)
    }
    return Object.freeze(
      order.map((repository) => {
        const paths = byRepository.get(repository) ?? []
        return Object.freeze({
          repository,
          path: paths[0] ?? '',
          more: Math.max(0, paths.length - 1),
        })
      }),
    )
  }

  const rowsOf = (
    discovered: readonly DiscoveredInstallation[],
    warnings: readonly string[],
    cliRefusal: string,
  ): readonly InstallationsListRow[] => {
    // The session is re-decided on every read, because it is a function of what
    // the read found and of the store's own answer -- which a choose, a checkout
    // or a removal may have just changed. It is assigned at the foot of this
    // function, once the rows are the rows.
    const stored = registry.rows()
    const facts: ConsoleFacts = {
      storeRefusal: registry.refusal ?? '',
      storePath,
      cliRefusal,
      installCommand: FUGARO_INSTALL_COMMAND,
      warnings,
    }
    const rows: InstallationsListRow[] = discovered.map((installation) => {
      const held = stored.find((row) => row.project === installation.name)
      const rowFacts = {
        project: installation.name,
        listed: true,
        // The CLI's own signal: `gcp_project` is "" exactly when that config
        // could not be read, and the document's warning says why.
        configUnreadable: installation.gcpProject === '',
      }
      return {
        project: installation.name,
        gcpProject: installation.gcpProject,
        region: installation.region,
        configUnreadable: rowFacts.configUnreadable,
        listed: true,
        saved: held !== undefined,
        active: held?.active ?? false,
        mode: held?.mode ?? 'unknown',
        ...(held?.checkout === undefined ? {} : { checkout: held.checkout }),
        checkouts: offeredCheckouts(held?.checkout, installation.checkouts),
        checkoutChoices: checkoutChoicesOf(installation),
        repositories: repositoryIdentities(installation.repositories),
        state: classifyInstallation(rowFacts, facts),
        open: false,
      }
    })
    for (const row of stored) {
      if (discovered.some((installation) => installation.name === row.project)) continue
      const rowFacts = { project: row.project, listed: false, configUnreadable: false }
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
        checkouts: offeredCheckouts(row.checkout, []),
        // A record the CLI did not list has no document behind it, so there is
        // nothing this console may offer for it: `[]` is the honest list, and
        // the chooser draws the sentence that stands in for the control.
        checkoutChoices: Object.freeze([]),
        repositories: repositoryIdentities([]),
        state: classifyInstallation(rowFacts, facts),
        open: false,
      })
    }
    return markOpen(rows)
  }

  /**
   * markOpen marks the one row the console is open on and settles the session
   * installation from it (`openProject`, `installationstate.ts`). It is the only
   * place either is written, so the rail's selected row and the installation this
   * process drives cannot disagree.
   *
   * The session record is `null` when the open row is the store's own active
   * installation: `installation()` then answers the store, which is the answer it
   * would have given anyway, and the file's record keeps its id.
   */
  const markOpen = (rows: readonly InstallationsListRow[]): readonly InstallationsListRow[] => {
    const decided = openProject(rows)
    const chosen = registry.active()
    session =
      decided === null || decided === chosen?.project ? null : registry.sessionRecordFor(decided)
    return Object.freeze(rows.map((row) => (row.project === decided ? { ...row, open: true } : row)))
  }

  /**
   * lastListed is the document the last successful `list` read, keyed by project
   * name, and it is what a checkout is checked against: the console accepts only
   * the directories the CLI itself listed (installlist.ts rule 2), so a renderer
   * that sends a path this map does not hold has named a directory this process
   * never obtained. It is empty until a list has been read, and an empty map
   * offers nothing -- which is the safe direction.
   *
   * A checkout write is held to `suggestedCheckouts` over this list -- the same
   * list the screen draws, tool-owned and temporary paths dropped -- and not to
   * this map alone: a directory the CLI listed under a temporary root is still
   * one the console never offers, so it is not one a renderer may set either.
   */
  let lastListed: ReadonlyMap<string, readonly string[]> = new Map()

  /**
   * bootReply is the answer `openInstallation` read, kept for the first `list`
   * the renderer asks. It is one-shot and nothing else reads it: the startup's
   * read and the rail's first read are the same question asked in the same
   * instant, and a console that spawned `config list --json` twice at every
   * launch would be paying for a document it already had. Every later `list`
   * reads (a removal, a checkout or the picker window's own refresh), so nothing
   * here can go stale behind a screen.
   */
  let bootReply: InstallationsListReply | null = null
  let bootRead = false

  /**
   * openInstallation is the owner's open-first-readable rule, run once at
   * startup: one list read, and the session installation it settles (see the
   * interface's own comment for the rule and for why nothing is written).
   *
   * A read that throws leaves the session as it was -- `null` on a boot, so the
   * console opens what the store says -- because a console whose `config list`
   * failed is a console that should still open, with its own issue view, rather
   * than one that never draws a window.
   */
  const openInstallation = async (): Promise<void> => {
    // Once per process: the handshake that calls this runs again on a retry or
    // after a choose, and re-reading there would spawn `config list` again and
    // re-decide the session behind the operator's back. A later decision is an
    // explicit one -- a rail `list`, a choose, a checkout or a removal.
    if (bootRead) return
    bootRead = true
    try {
      bootReply = await listReply()
    } catch {
      bootReply = null
    }
  }

  /** listReply is D13's list: the onboarding state, the rows, the CLI's own
   * warning lines and the console's view of the active installation. */
  const listReply = async (): Promise<InstallationsListReply> => {
    const found = await discover()
    // A function, and spread **last** in each answer below: `active` is the
    // session installation, and the session is settled by `rowsOf` (through
    // `markOpen`) from the rows of this very read. A value read here would be the
    // answer of the *previous* read -- at a boot, the store's, before the open
    // rule had run at all.
    const base = (): Pick<InstallationsListReply, 'path' | 'active'> & { readonly refusal?: string } => ({
      ...(registry.refusal === null ? {} : { refusal: registry.refusal }),
      path: storePath,
      active: current(),
    })
    if (!found.ok) {
      // A refusal still draws the rows the console itself has: a store entry is
      // the console's own record, and hiding it because the CLI would not list
      // would take away the one installation the operator can still open.
      //
      // The refusal is handed to the classification as `cliRefusal` only when it
      // *is* one -- a resolution that found no binary. A CLI that exited non-zero
      // or printed a document this console could not parse is a failure of this
      // read and not a fact about the machine's CLI, so no row is marked
      // "install the CLI" for it; the sentence is drawn as the list's own warning
      // line instead, which is where it was drawn before.
      clearCheckouts()
      return Object.freeze({
        onboard: found.onboarding,
        rows: rowsOf([], [], found.onboarding.kind === 'install-cli' ? found.reason : ''),
        warnings: Object.freeze(found.onboarding === NO_ONBOARDING && found.reason !== '' ? [found.reason] : []),
        ...base(),
      })
    }
    // What this read listed is what a checkout may be set from, and it replaces
    // the previous read wholesale: a checkout the CLI has stopped listing is one
    // this console no longer holds.
    lastListed = new Map(found.installations.map((installation) => [installation.name, installation.checkouts]))
    const rows = rowsOf(found.installations, found.warnings, '')
    if (rows.length === 0) {
      // "The CLI listed nothing and the console has nothing" is the one state
      // the first-installation screen is for. A document this console could not
      // read never reaches here.
      return Object.freeze({
        onboard: firstInstallationState(),
        rows,
        warnings: Object.freeze([...found.warnings]),
        ...base(),
      })
    }
    return Object.freeze({
      onboard: NO_ONBOARDING,
      rows,
      warnings: Object.freeze([...found.warnings]),
      ...base(),
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
  /** current is `installation()`: the session installation's answer when this run
   * has one, and the store's own otherwise. */
  const current = (): ActiveInstallation =>
    session === null ? registry.current() : registry.currentFor(session)

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
    const probed = await probeFor(project)
    if (!probed.ok) return { ok: false, reason: probed.reason }
    // The compatibility first, so a row drawn by the list this choose returns
    // already carries the mode the handshake just read.
    registry.remember(project, probed.value.compatibility)
    registry.choose(project)
    // The store now names the chosen installation, so the session decided for
    // an earlier row must not answer any more: the window `onChosen` reopens runs
    // its handshake and binds its source through `installation()` before any
    // list re-reads and `markOpen` settles the session again (review M1).
    session = null
    // Awaited inside the chain: the next mutation waits for the window this
    // choose asked for.
    await deps.onChosen?.({
      project,
      compatibility: probed.value.compatibility,
      resolvedBinaryPath: probed.value.path,
    })
    return { ok: true, data: await listReply() }
  }

  /** probeFor runs one installation's handshake with its record's own override,
   * config and checkout -- what a choose and a rebind after a removal both ask. */
  const probeFor = (project: string): ReturnType<typeof probeInstallation> => {
    const held = registry.entry(project)
    return probeInstallation(
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
  }

  /**
   * setInstallationCheckout is the one place a repository is asked for, and the
   * screen asks at the moment it needs one (requirement 3): the directories the
   * rail offers for that installation (`offeredCheckouts`: what the CLI listed,
   * plus the console's own recorded checkout, minus temporary and tool-owned
   * paths) are the only ones this accepts, and an absent `checkout` clears the
   * one that was there.
   *
   * Setting one runs the pairing check (`validateInstallation`, which is rule
   * 7's `doctor --json` from that directory with that project). Clearing one
   * runs nothing: there is no pairing to check without a checkout, and an
   * installation without one is a state the console supports (rule 5).
   *
   * The check runs against the installation's **own** CLI: the record's override
   * is candidate one of rule 4's walk, and the `--config` rule 7's `doctor` takes
   * is the record's own config path -- the same two facts a choose is probed with.
   */
  const setInstallationCheckout = async (project: string, checkout: string | undefined): Promise<unknown> => {
    const held = registry.entry(project)
    if (held === null && !lastListed.has(project)) {
      return { ok: false, reason: `no installation is named ${JSON.stringify(project)}` }
    }
    if (checkout === undefined) {
      // **A clear is a write only when there is something to clear.** A project
      // the CLI listed and this console holds no record for has no checkout, and
      // `registry.setCheckout` creates a record for a project it holds none
      // (`recordOf`), so reaching it here would answer a no-op by writing
      // `installations.json` for an installation the operator never chose -- and
      // the row would come back `saved`, with the console's own mode for it and a
      // removal offer. The reply is the ordinary list either way: the request was
      // satisfiable, it just had nothing to do. `held` is the same read the guard
      // above used, so this is one lookup and not two.
      if (held !== null) registry.setCheckout(project, undefined)
      return { ok: true, data: await listReply() }
    }
    // Membership of the list the screen offers, and not a shape check: a path
    // that is absolute and well-formed but was never listed is a directory this
    // console did not obtain, and one the CLI listed under a temporary or
    // tool-owned root is one the console never offers (`suggestedCheckouts`
    // drops it). The write is held to the same list the screen draws, so a
    // renderer cannot set a directory the screen would not have shown.
    const offered = offeredCheckouts(held?.checkout, lastListed.get(project) ?? [])
    if (!offered.includes(checkout)) {
      return {
        ok: false,
        reason: `${checkout} is not one of the checkouts the CLI listed for ${JSON.stringify(project)}`,
      }
    }
    // The record's own override and config path, exactly as `chooseInstallation`
    // passes them: the check that runs here is the same rule 4 resolution and the
    // same version handshake, and rule 7's `doctor` reads the config the record
    // names. Validating `{ project, checkout }` alone let rule 4's walk decide
    // which CLI was asked -- so an installation whose only `fugaro` is its
    // override was refused, and one whose override is a different CLI than the
    // walk finds was checked by a binary this console does not drive, with no
    // `--config`. A record with no override passes nothing, and the walk decides,
    // which is rule 4's ordinary case.
    const validated = await validateInstallation(
      {
        project,
        checkout,
        ...(held?.binaryPath === undefined ? {} : { binaryPath: held.binaryPath }),
        ...(held?.configPath === undefined ? {} : { configPath: held.configPath }),
      },
      validationDeps(),
    )
    if (!validated.ok) return { ok: false, reason: validated.reason }
    registry.setCheckout(project, checkout)
    return { ok: true, data: await listReply() }
  }

  /** removeInstallation drops the console's own record. `onRemoved` is the seam
   * that tells the dashboard it has **no** installation, so it fires only when
   * the removal took the active one away -- firing it for a non-active row would
   * tell the dashboard it has nothing to drive while `active` still names an
   * installation the operator can use. `onRecordRemoved` is the removal's other
   * reader and fires for every one that happened, with the project: a launch in
   * flight names its own installation, so its window must close whichever row was
   * active. */
  const removeInstallation = async (project: string): Promise<unknown> => {
    if (registry.entry(project) === null) return { ok: false, reason: 'no such installation' }
    const before = current()
    registry.remove(project)
    deps.onRecordRemoved?.(project)
    const wasOpen = before.chosen && before.project === project
    if (wasOpen) deps.onRemoved?.(before.id)
    // A removal re-reads, so the answer the screen draws from and the session it
    // leaves behind are one decision: the record that went is gone from the rows
    // (`saved`), and the console is open on whichever row the open rule names now.
    const reply = await listReply()
    if (wasOpen) await rebindAfterRemoval(reply)
    return { ok: true, data: { rows: reply.rows, active: reply.active, onboard: reply.onboard } }
  }

  /**
   * rebindAfterRemoval is what keeps a removal of the open installation from
   * leaving the dashboard on a source and a handshake for a record that is gone:
   * when the open rule now names a readable installation, that installation's
   * handshake runs and `onChosen` reopens the window on it -- a choose in all but
   * the store, which keeps no new `active` (the operator chose nothing). An
   * unreadable or absent next row needs no handshake: the dashboard draws its
   * issue, or the onboarding, and reads nothing.
   */
  const rebindAfterRemoval = async (reply: InstallationsListReply): Promise<void> => {
    const open = reply.rows.find((row) => row.open)
    if (open === undefined || !open.state.readable) return
    const probed = await probeFor(open.project)
    if (!probed.ok) return
    // As a choose does: the handshake's answer is what `installation.current`
    // reports the mode and the version from. It is held in memory only.
    registry.remember(open.project, probed.value.compatibility)
    await deps.onChosen?.({
      project: open.project,
      compatibility: probed.value.compatibility,
      resolvedBinaryPath: probed.value.path,
    })
  }

  const answerFor = async (name: InstallationOperationName, args: InstallationArguments): Promise<unknown> => {
    switch (name) {
      case 'list': {
        // The startup's own read, when it is still unclaimed: the rail's first
        // ask and the session's decision are one read (`bootReply`). It is taken
        // here rather than in `listReply` so a mutation's own re-read never
        // consumes it.
        const kept = bootReply
        bootReply = null
        return { ok: true, data: kept ?? (await listReply()) }
      }
      case 'choose':
        return serially(() => chooseInstallation(args['project'] ?? ''))
      case 'checkout':
        return serially(() => setInstallationCheckout(args['project'] ?? '', args['checkout']))
      case 'remove':
        return serially(() => removeInstallation(args['project'] ?? ''))
      case 'reveal':
        // The renderer names a project and nothing else. The path the file
        // manager is handed is this process's own `explorerPath` for that record
        // -- the UNC spelling for a WSL installation, the checkout itself for a
        // host row (D3(a), T-D3) -- so the row is looked up here and its field
        // is the whole input. A project this console does not have and a record
        // with no checkout are the same refusal, because they are the same fact:
        // there is nothing to open.
        const row = rows().find((candidate) => candidate.project === (args['project'] ?? ''))
        const explorerPath = row?.explorerPath
        if (explorerPath === undefined) return { ok: false, reason: REVEAL_FAILED }
        const revealed = await deps.reveal?.(explorerPath)
        if (revealed !== true) return { ok: false, reason: REVEAL_FAILED }
        // The answer names no path: the renderer drew the button from the
        // row's own checkout, and a reply that echoed the spelling the file
        // manager got back across the boundary would put the one spelling this
        // window keeps to itself on the IPC surface for no reader.
        return { ok: true, data: { revealed: true } }
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

  /** drawnAuto is the value the update checkbox is drawn with. Electron's own
   * checkbox moves itself on the click, before anything is written, so a click
   * whose write did not land leaves the menu showing a switch this console is
   * not using -- and the value that *is* in force only comes back from
   * `setAuto`, which reads it from wherever the switch is kept and answers
   * rather than throwing (a throw here would be Electron's uncaught-error
   * dialog over the running app). Every answer moves this local, agreeing or
   * not, so the menu is built again from the value in force whenever the two
   * disagree and every later rebuild draws that same value. */
  let drawnAuto = deps.updateMenu?.auto ?? false

  /** drawnSend is the same value for the crash reporter's checkbox, and the same
   * redraw: the store answers what is in force after a write, and the menu is
   * built again when that disagrees with the state the click left the item in. */
  let drawnSend = deps.crashMenu?.send ?? false

  const installMenu = (): void => {
    const updateMenu = deps.updateMenu
    const crashMenu = deps.crashMenu
    deps.menu.setApplicationMenu(
      deps.menu.buildFromTemplate(
        installationsMenuTemplate({
          appName: 'Fugaro console',
          ...(updateMenu === undefined
            ? {}
            : {
                updateMenu: {
                  auto: drawnAuto,
                  setAuto: (on) => {
                    const actual = updateMenu.setAuto(on)
                    // The value in force is kept whether or not it disagreed
                    // with the click: a write that landed leaves the two equal,
                    // and any later rebuild -- the crash item's redraw, another
                    // click's -- draws from this local rather than from the
                    // click it was built beside.
                    drawnAuto = actual
                    if (actual !== on) installMenu()
                    return actual
                  },
                  checkNow: () => {
                    updateMenu.checkNow()
                  },
                },
              }),
          ...(crashMenu === undefined
            ? {}
            : {
                crashMenu: {
                  send: drawnSend,
                  setSend: (on) => {
                    const actual = crashMenu.setSend(on)
                    drawnSend = actual
                    if (actual !== on) installMenu()
                    return actual
                  },
                },
              }),
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
  }

  return {
    installation: current,
    open,
    openInstallation,
    answer: answerFor,
    installMenu,
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
 *      `picker.answer`/`picker.open` as its `installations` dep, which is what
 *      answers the rail's six operations (owner's design ruling of
 *      2026-10-25), and
 *   2. calls `picker.installMenu()` once the app is ready.
 *
 * Both of those are the entry point's because both need the lifecycle it owns;
 * neither is a second copy of anything here. **It no longer opens the window**:
 * the rail's `+` door and the menu item are the two ways in, and neither is
 * something the console does by itself.
 */
export function installInstallationPicker(options: {
  readonly electron: {
    readonly BrowserWindow: new (options: BrowserWindowConstructorOptions) => BrowserWindow
    readonly session: Session
    readonly ipcMain: IpcMainLike
    readonly Menu: MenuLike
    /** shell is Electron's `shell`. `openExternalValidated` -- the console's
     * one external-link path -- is given it rather than this module calling
     * `shell.openExternal` itself; `showItemInFolder` is the one method used
     * here, and it is used through the `reveal` dep below so a test can hold
     * it. */
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
  readonly onRecordRemoved?: InstallationPickerDeps['onRecordRemoved']
  readonly updateMenu?: UpdateMenuOptions | undefined
  readonly crashMenu?: CrashMenuOptions | undefined
}): InstallationPicker {
  const { electron } = options
  // **The home is this app's own environment's, and not `app.getPath('home')`.**
  // On Linux the two agree -- both read `HOME`. On macOS they do not: Electron
  // answers `home` with Chromium's `base::GetHomeDir()`, whose Apple
  // implementation is Foundation's `NSHomeDirectory()`, which reads the user
  // *record* and ignores `HOME` -- while `homedir()` (libuv) answers with
  // `HOME` first. So a launch whose `HOME` is not the account's (`test/e2e`'s
  // stub does exactly that: `test/support/cliStub`) had this picker resolve
  // `/Users/<owner>/.local/bin` while the same process's own `homedir()` -- and
  // the `HOME` every child is handed (`childEnvironment`) -- was the redirected
  // one.
  //
  // The picker is the wrong place to be the only exception: the startup
  // handshake resolves the *same* CLI with `homedir()` (`src/main/index.ts`
  // passes `resolveHandshakeBinary` no home, so `binDirectories` falls back to
  // it), so a picker that disagreed with the handshake about where the CLI is
  // would draw "install the CLI first" over a console that is already running
  // one, and would ask the operator to install it where it already is. The store
  // path refusal (`installationStorePath`) reads this same home, and it is the
  // right one there too: the two other config directories it checks
  // (`$XDG_CONFIG_HOME`, `$FUGARO_CONFIG`) come from this environment, so the
  // `~/.config/fugaro` beside them must be the one this environment's CLI would
  // use.
  //
  // **This is not a change to rule 4.** The order -- the store's override first,
  // then the platform's well-known prefixes -- and the list itself are
  // untouched, and no search list is read out of the environment: the app's
  // `PATH` is still never walked. This only says which directory `~` is, and it
  // says the same thing the rest of the process does. An empty `HOME` is not a
  // home, so Electron's answer stands in when the key is missing or blank.
  const envHome = options.appEnv['HOME']
  const home = envHome === undefined || envHome === '' ? electron.app.getPath('home') : envHome
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
    // The reveal affordance's one handover (T-D3). **It answers `true` for the
    // call, not for the platform**: `showItemInFolder` returns nothing and
    // reports no failure, so the console cannot know whether a file manager
    // appeared -- what it *can* refuse, and does, is being asked to reveal a
    // row with no directory. A `false` here would be a claim this process
    // cannot make.
    reveal: (path: string) => {
      electron.shell.showItemInFolder(path)
      return true
    },
    userData: electron.app.getPath('userData'),
    home,
    appPath: electron.app.getAppPath(),
    appEnv: options.appEnv,
    ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
    ...(options.spawnLog === undefined ? {} : { spawnLog: options.spawnLog }),
    ...(options.fs === undefined ? {} : { fs: options.fs }),
    ...(options.onChosen === undefined ? {} : { onChosen: options.onChosen }),
    ...(options.onRemoved === undefined ? {} : { onRemoved: options.onRemoved }),
    ...(options.onRecordRemoved === undefined ? {} : { onRecordRemoved: options.onRecordRemoved }),
    ...(options.updateMenu === undefined ? {} : { updateMenu: options.updateMenu }),
    ...(options.crashMenu === undefined ? {} : { crashMenu: options.crashMenu }),
  })
}

/** NO_ACTIVE_INSTALLATION is re-exported so the store's own empty answer is
 * readable from the module that owns the picker's window, and so a test that
 * asserts it can import it from either. */
export { NO_ACTIVE_INSTALLATION }
