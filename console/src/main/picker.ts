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
  MAX_OUTSTANDING_PICKS,
  NO_ACTIVE_INSTALLATION,
  createInstallationFileStore,
  createInstallationRegistry,
  createPickTokens,
  installationChannel,
  installationOperationFor,
  installationStorePath,
  INSTALLATIONS_FILE_NAME,
  probeInstallation,
  refusedInstallationStore,
  validateInstallation,
  validateInstallationArguments,
} from './installations'
import type {
  ActiveInstallation,
  InstallationArguments,
  InstallationFileStore,
  InstallationFs,
  InstallationOperationName,
  InstallationRegistry,
  InstallationRow,
  PickKind,
  PickTokens,
} from './installations'
import { SENDER_REFUSAL, isSenderOnDocument } from './ipc'
import type { ConsoleEventLike, IpcMainLike } from './ipc'
import { PICKER_ENTRY_PATH, PICKER_ENTRY_URL } from './protocol'
import { verifyCandidate } from '../source/cli'
import { buildHardenedWindow, consoleWebPreferences } from './window'
import type { BrowserWindow, BrowserWindowConstructorOptions, Session } from 'electron'
import type { CliCompatibility } from '../shared/version'
import type { ChildSpawner } from './spawn'
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
 * on purpose: this is a list of at most sixteen rows and a form. */
export const PICKER_WINDOW_SIZE = Object.freeze({ width: 720, height: 520 })

/**
 * PICKER_FILE_FILTERS are the native dialogs' filters. They are a *hint* the
 * operator can override -- `properties: ['openFile']` is what bounds the answer
 * -- and none of them is a security boundary: whatever comes back is validated
 * as a path before it is stored, by `validateInstallation`.
 */
export const PICKER_FILE_FILTERS = Object.freeze([
  Object.freeze({ name: 'Executables', extensions: [] as string[] }),
])

/** DialogLike is the slice of Electron's `dialog` this file uses. It is an
 * argument rather than an import for the reason the rest of the console gives:
 * the unit test drives every decision here under plain Node. */
export interface DialogLike {
  showOpenDialog(
    window: BrowserWindow | null,
    options: {
      readonly title: string
      readonly properties: readonly string[]
      readonly filters?: readonly { readonly name: string; readonly extensions: readonly string[] }[]
    },
  ): Promise<{ readonly canceled: boolean; readonly filePaths: readonly string[] }>
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
  readonly dialog: DialogLike
  readonly menu: MenuLike
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
    readonly id: string
    readonly project: string
    readonly binaryPath: string
    /** The answer of the handshake the choose just ran: never `refused`. */
    readonly compatibility: CliCompatibility
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
  /** remember records the compatibility a handshake read for one
   * installation -- the startup's, from `onDecided` in `src/main/index.ts` -- so
   * `installation()` and the rows carry its mode and version (review S1). */
  remember(id: string, compatibility: CliCompatibility): void
  /** close drops every outstanding pick token, so a window that is gone cannot
   * be answered for. */
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
  const validate = (candidate: Parameters<InstallationRegistry['add']>[0]) =>
    validateInstallation(candidate, {
      appEnv: deps.appEnv,
      ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
      ...(deps.spawnLog === undefined ? {} : { spawnLog: deps.spawnLog }),
      ...(deps.fs === undefined ? {} : { fs: deps.fs }),
      verify: async (candidate) => {
        await verifyCandidate(candidate, {
          appEnv: deps.appEnv,
          ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
          ...(deps.spawnLog === undefined ? {} : { spawnLog: deps.spawnLog }),
          home: deps.home,
        })
      },
    })
  const registry = createInstallationRegistry({ store, validate })
  registry.load()
  const tokens: PickTokens = createPickTokens()

  let window: BrowserWindow | null = null
  let openedOnFirstRun = false

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
            // The tokens are dropped with the window: a token is a capability to
            // name one path, and a window that is gone has no use for one. This is
            // also what makes "at most eight outstanding" a bound on a *session*
            // rather than on a process.
            tokens.dropAll()
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

  // One chain for every mutation (review S6): an add, a choose or a remove runs
  // only after the previous one -- its probe, its write and, for a choose, the
  // window it asked for -- has finished. Two chooses clicked 50 ms apart would
  // otherwise both probe, both persist and both open a dashboard.
  let mutations: Promise<unknown> = Promise.resolve()
  const serially = <T>(body: () => Promise<T>): Promise<T> => {
    const next = mutations.then(body, body)
    mutations = next.catch(() => undefined)
    return next
  }

  const rows = (): readonly InstallationRow[] => registry.rows()
  const current = (): ActiveInstallation => registry.current()

  const pick = async (kind: PickKind, title: string, properties: readonly string[]) => {
    const answer = await deps.dialog.showOpenDialog(window, { title, properties, filters: PICKER_FILE_FILTERS })
    if (answer.canceled || answer.filePaths.length === 0) return { ok: false, reason: 'nothing was chosen' }
    const first = answer.filePaths[0]
    if (first === undefined || first === '') return { ok: false, reason: 'nothing was chosen' }
    // The renderer is handed a **token**, never the path: the path stays in this
    // process, and `add` sends the token back. A renderer that invented a path
    // has nothing that validates.
    return { ok: true, data: { token: tokens.issue(kind, first) } }
  }

  const answerFor = async (
    name: InstallationOperationName,
    args: InstallationArguments,
  ): Promise<unknown> => {
    switch (name) {
      case 'list':
        return { rows: rows(), active: current(), ...(registry.refusal === null ? {} : { refusal: registry.refusal }), path: storePath }
      case 'current':
        return current()
      case 'pickBinary':
        return pick('binary', 'Choose the fugaro binary', ['openFile'])
      case 'pickConfig':
        return pick('config', 'Choose the config file', ['openFile'])
      case 'pickCheckout':
        return pick('checkout', 'Choose the checkout directory', ['openDirectory'])
      case 'add':
      case 'choose':
      case 'remove':
        return serially(() => mutate(name, args))
    }
  }

  const mutate = async (
    name: 'add' | 'choose' | 'remove',
    args: InstallationArguments,
  ): Promise<unknown> => {
    switch (name) {
      case 'add': {
        const validated = await registry.add({
          project: args['project'] ?? '',
          binaryPath: args['binary'] ?? '',
          ...(args['config'] === undefined ? {} : { configPath: args['config'] }),
          ...(args['checkout'] === undefined ? {} : { checkout: args['checkout'] }),
        })
        if (!validated.ok) return { ok: false, reason: validated.reason }
        return { ok: true, data: { rows: rows() } }
      }
      case 'choose': {
        const id = args['id'] ?? ''
        const held = registry.rows().find((row) => row.id === id)
        if (held === undefined) return { ok: false, reason: 'no such installation' }
        // Choosing runs the handshake again, and a `refused` CLI is **not**
        // chosen: the console shows the sentence it reached rather than binding
        // a source it cannot drive.
        const probed = await probeInstallation(
          { id, project: held.project, binaryPath: held.binary, ...(held.config === undefined ? {} : { configPath: held.config }), ...(held.checkout === undefined ? {} : { checkout: held.checkout }) },
          {
            appEnv: deps.appEnv,
            ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
            ...(deps.spawnLog === undefined ? {} : { spawnLog: deps.spawnLog }),
          },
        )
        if (!probed.ok) return { ok: false, reason: probed.reason }
        registry.remember(id, probed.compatibility)
        registry.choose(id)
        // Awaited inside the chain: the next mutation waits for the window this
        // choose asked for.
        await deps.onChosen?.({ id, project: held.project, binaryPath: held.binary, compatibility: probed.compatibility })
        return { ok: true, data: { rows: rows(), active: current() } }
      }
      case 'remove': {
        const id = args['id'] ?? ''
        if (!registry.rows().some((row) => row.id === id)) return { ok: false, reason: 'no such installation' }
        // `onRemoved` is the seam that tells the dashboard it has **no**
        // installation, so it fires only when the removal took the active one
        // away -- `registry.remove` already decided that, and its answer says
        // which. Firing it for a non-active row would tell 3.4a's consumer it
        // has nothing to drive while `active` still names an installation the
        // operator can use; that is a dashboard showing D13's no-installation
        // state over a console that is bound and working.
        const before = current()
        registry.remove(id)
        if (before.chosen && before.id === id) deps.onRemoved?.(id)
        // The removal is persisted either way -- only the *notification* is
        // conditional -- and the reply carries `current()`, read back from the
        // store after the write, so the window redraws what is on disk rather
        // than what the request asked for.
        return { ok: true, data: { rows: rows(), active: current() } }
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
        const checked = validateInstallationArguments(installationOperationFor(name), raw, (kind, token) =>
          tokens.redeem(kind, token),
        )
        if (!checked.ok) return { ok: false, reason: checked.reason }
        const data = await answerFor(name, checked.args)
        // `pick` answers an envelope of its own, and the envelope is unwrapped
        // here so every operation answers exactly one shape.
        if (typeof data === 'object' && data !== null && 'ok' in data) return data
        return { ok: true, data }
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
    remember(id: string, compatibility: CliCompatibility): void {
      registry.remember(id, compatibility)
    },
    close(): void {
      tokens.dropAll()
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
    readonly dialog: DialogLike
    readonly Menu: MenuLike
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
    dialog: electron.dialog,
    menu: electron.Menu,
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

/** MAX_OUTSTANDING_PICKS is re-exported so the picker's bound is readable from
 * the module that owns the window, and so a test that asserts it can import it
 * from either. */
export { MAX_OUTSTANDING_PICKS, NO_ACTIVE_INSTALLATION }
