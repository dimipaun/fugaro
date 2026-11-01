// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The picker's surface, held to the two rules that make it a surface rather than
// a bridge (desktop-console plan Task 3.4b; design sections 3.4 rules 4, 5 and 7,
// 5.1 rule 10, D13, D14; owner ruling C9; owner's ruling of 2026-10-20).
//
// The two rules, and the mutation each test catches:
//
//   1. **a renderer never names a path the console did not obtain.** The picker's
//      five operations take a project name, a directory out of that
//      installation's own `checkouts`, or nothing at all -- so a `checkout` for a
//      directory the CLI never listed is refused ("offers only what the CLI
//      listed"), and the one URL operation takes **no arguments**, so a frame
//      cannot ask the operating system to open a link of its choosing
//      ("the link operation takes no arguments");
//   2. **a frame reaches one document's surface and no other.** The dashboard and
//      the picker are served from the same origin, so an origin-only gate lets
//      either reach both; the tests below drive a dashboard-sender event into
//      `fugaro:installations/*` and a picker-sender event into
//      `fugaro:console/*` and require the fixed refusal both ways.
//
// The preload's copy of the table is compared with the main process's, because
// the two are duplicated on purpose (the sandboxed preload cannot import a module
// that reads `node:fs`) and a duplicate nobody compares is a duplicate that
// drifts.

import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it, vi } from 'vitest'

import {
  INSTALLATION_CHANNEL_PREFIX,
  INSTALLATION_OPERATIONS,
  INSTALLATIONS_FILE_NAME,
  NO_ACTIVE_INSTALLATION,
  installationChannel,
  serializeInstallationFile,
} from '../../src/main/installations'
import {
  INSTALLATION_BRIDGE_KEY,
  INSTALLATION_BRIDGE_PREFIX,
  INSTALLATION_OPERATIONS as PRELOAD_OPERATIONS,
  createInstallationBridge,
  invokeOperation,
} from '../../src/preload/installations'
import { createInstallationPicker } from '../../src/main/picker'
import type { CrashMenuOptions } from '../../src/main/picker'
import { CRASH_SWITCH_UNWRITTEN, createCrashSettings, nodeCrashFs, readCrashSwitch } from '../../src/main/crashstore'
import { UPDATES_SWITCH_UNWRITTEN, createUpdateSettings, nodeUpdatesFs, readUpdateSwitch } from '../../src/main/updatestore'
import type { UpdateController } from '../../src/main/updates'
import { TEMP_ROOTS } from '../../src/main/installationstate'
import {
  INSTALLATIONS_MENU_LABEL,
  OPEN_EXTERNAL_FAILED,
  PICKER_ENTRY_URL,
  REVEAL_FAILED,
  CRASH_SEND_ITEM_LABEL,
  UPDATE_AUTO_LABEL,
  UPDATE_NOW_LABEL,
  pickerOptions,
  pickerPreloadPath,
} from '../../src/main/picker'
import type { UpdateMenuOptions } from '../../src/main/picker'
import { GETTING_STARTED_URL, FUGARO_INSTALL_COMMAND, FUGARO_SETUP_COMMAND } from '../../src/main/onboarding'
import { ALLOWED_EXTERNAL_HOSTS, consoleWebPreferences, validateExternalUrl } from '../../src/main/window'
import {
  CONSOLE_OPERATION_NAMES,
  SENDER_REFUSAL,
  channelForOperation,
  registerConsoleHandlers,
} from '../../src/main/ipc'
// `APP_ENTRY_URL` is the **protocol** module's constant; `ipc.ts` only imports
// it to pass to `isSenderOnDocument`. Importing it from `ipc.ts` would be an
// undefined binding, and the two tests below that build a dashboard frame would
// then pass for the wrong reason (a garbage frame is refused too).
import { APP_ENTRY_URL, CONTENT_SECURITY_POLICY } from '../../src/main/protocol'
import type { ConsoleEventLike, ConsoleHandlers, ConsoleReply, IpcMainLike } from '../../src/main/ipc'
import { CLI_VERSION_BUILT_AGAINST, classifyCliVersion } from '../../src/shared/version'
import { FUGARO_BINARY_NAME, binDirectories } from '../../src/main/spawn'
import { installStub } from './stub'
import type { Stub } from './stub'

function freshDirectory(): string {
  return mkdtempSync(join(tmpdir(), 'fugaro-picker-'))
}

/** fakeIpcMain records the listeners it was handed, so a test can call one. */
function fakeIpcMain(): IpcMainLike & { readonly listeners: Map<string, (event: ConsoleEventLike, raw: unknown) => Promise<unknown>> } {
  const listeners = new Map<string, (event: ConsoleEventLike, raw: unknown) => Promise<unknown>>()
  return {
    listeners,
    handle(channel, listener) {
      listeners.set(channel, listener as unknown as (event: ConsoleEventLike, raw: unknown) => Promise<unknown>)
    },
  }
}

/** fakeWindow is the slice of Electron's BrowserWindow the picker uses. */
function fakeWindow(options: Record<string, unknown> = {}, loadFails?: 'closed' | 'closing' | 'error') {
  // Every listener is kept, not one per event name: the picker and
  // `buildHardenedWindow` both listen for `closed`, and a fake that kept only the
  // last one would hide the picker's.
  const handlers = new Map<string, ((...args: unknown[]) => void)[]>()
  const listen = (name: string, handler: (...args: unknown[]) => void): void => {
    handlers.set(name, [...(handlers.get(name) ?? []), handler])
  }
  let openHandlerSet = false
  let destroyed = false
  const contents = {
    on: (name: string, handler: (...args: unknown[]) => void) => {
      listen(name, handler)
    },
    setWindowOpenHandler: () => {
      openHandlerSet = true
    },
  }
  const window = {
    options,
    handlerNames: () => handlers.keys(),
    openHandlerSet: () => openHandlerSet,
    webContents: contents,
    loaded: [] as string[],
    on: (name: string, handler: (...args: unknown[]) => void) => {
      listen(name, handler)
    },
    once: (name: string, handler: (...args: unknown[]) => void) => {
      listen(name, handler)
    },
    loadURL: async (url: string) => {
      // `loadURL` in Electron returns a promise that resolves when the document
      // has loaded; the fake records the URL and resolves, which is what the
      // picker's `open()` awaits.
      window.loaded.push(url)
      if (loadFails === 'closed') {
        destroyed = true
        throw new Error('ERR_ABORTED (-3) loading app://console/installations.html')
      }
      // The same, as macOS orders it: the close is asked for (`close` fires) and
      // the load rejects while the window does not yet report itself destroyed.
      if (loadFails === 'closing') {
        for (const handler of handlers.get('close') ?? []) handler()
        throw new Error('ERR_ABORTED (-3) loading app://console/installations.html')
      }
      if (loadFails === 'error') throw new Error('ERR_FAILED (-2) loading app://console/installations.html')
    },
    isDestroyed: () => destroyed,
    focus: () => undefined,
    show: () => undefined,
    fire(name: string, ...args: unknown[]): void {
      for (const handler of handlers.get(name) ?? []) handler(...args)
    },
  }
  return window
}

/** STUB_VERSION is the version every stub here prints: a CLI this console
 * supports, so a choose reaches its handshake and the refusals below are about
 * what the test is actually asking. */
const CLOCKED = CLI_VERSION_BUILT_AGAINST

/** seededStub installs one stub `fugaro`, printing `CLOCKED`, in a directory of
 * its own -- the directory it returns is the one rule 4's prefix list is given,
 * because rule 4 looks in *directories* and joins the binary's name itself. */
function seededStub(): Stub {
  const directory = freshDirectory()
  const stub = installStub(join(directory, 'bin'), {
    commands: {
      version: { stdout: `${CLOCKED}\n` },
      doctor: { stdout: '{"ok":true,"checks":[]}\n' },
      // The picker's one read. A scenario that did not answer it would leave the
      // discovery to the stub's default, which is exit 1 -- and a test that
      // wanted to see a *list* has to seed one.
      'config list': { stdout: '{"installations":[],"warnings":[]}\n' },
    },
  })
  return stub
}

/** pickerOver builds a picker over fakes, with one userData directory of its
 * own and a home directory under the test's control.
 *
 * `searched` is the directory rule 4 looks in, spelled as a one-entry prefix
 * list: a unit test must be able to say "this machine has no `fugaro`" without a
 * real `/usr/local/bin` deciding the answer, and the shipped app never passes
 * this (see `InstallationPickerDeps.prefixes`). */
function pickerOver(
  options: {
    readonly userData?: string
    readonly loadFails?: 'closed' | 'closing' | 'error'
    readonly appEnv?: Readonly<Record<string, string | undefined>>
    readonly home?: string
    readonly prefixes?: readonly string[]
    /** tempRoots is the picker's temporary-root rule. It defaults to none here,
     * because these cases create their checkouts under the OS's temporary
     * directory; the case that pins the rule passes the real one. */
    readonly tempRoots?: readonly string[]
    readonly spawnLog?: unknown
    readonly onChosen?: Parameters<typeof createInstallationPicker>[0]['onChosen']
    readonly onRemoved?: Parameters<typeof createInstallationPicker>[0]['onRemoved']
    readonly onRecordRemoved?: Parameters<typeof createInstallationPicker>[0]['onRecordRemoved']
    readonly openExternal?: (url: string) => Promise<boolean>
    readonly reveal?: (path: string) => Promise<boolean> | boolean
    readonly updateMenu?: UpdateMenuOptions
    readonly crashMenu?: CrashMenuOptions
  } = {},
) {
  const ipcMain = fakeIpcMain()
  const loadFails = options.loadFails
  const windows: ReturnType<typeof fakeWindow>[] = []
  const BrowserWindow = function BrowserWindow(this: unknown, options: Record<string, unknown>) {
    const window = fakeWindow(options, loadFails)
    windows.push(window)
    return window
  } as unknown as new (options: unknown) => never
  let applicationMenu: unknown = null
  let redraws = 0
  const opened: string[] = []
  const revealed: string[] = []
  const picker = createInstallationPicker({
    BrowserWindow: BrowserWindow as never,
    session: {
      setPermissionRequestHandler: () => undefined,
      setPermissionCheckHandler: () => undefined,
    } as never,
    ipcMain,
    menu: {
      buildFromTemplate: (template) => {
        redraws += 1
        return builtMenuItems(template)
      },
      setApplicationMenu: (value) => {
        applicationMenu = value
      },
    },
    // The console's one external-link path, as `installInstallationPicker`
    // hands it in: `openExternalValidated(shell, ...)`. The seam is here so the
    // test can hold the URL the picker *would* open, which is the only thing
    // that makes "the operation takes no arguments" an observation rather than a
    // reading of the code.
    openExternal: async (url: string) => {
      opened.push(url)
      return options.openExternal === undefined ? true : options.openExternal(url)
    },
    // The reveal affordance's handover, held for the same reason `openExternal`
    // is: the path the file manager is handed is the whole of what a test can
    // observe about it, and it must be this process's own.
    reveal: async (path: string) => {
      revealed.push(path)
      return options.reveal === undefined ? true : options.reveal(path)
    },
    userData: options.userData ?? freshDirectory(),
    home: options.home ?? freshDirectory(),
    prefixes: options.prefixes ?? [],
    ...(options.tempRoots === undefined
      ? { tempDirectory: () => '/nonexistent-console-temp', tempRoots: [] }
      : { tempDirectory: () => tmpdir(), tempRoots: options.tempRoots }),
    appPath: '/work/repo/console',
    appEnv: options.appEnv ?? {},
    ...(options.onChosen === undefined ? {} : { onChosen: options.onChosen }),
    ...(options.onRemoved === undefined ? {} : { onRemoved: options.onRemoved }),
    ...(options.onRecordRemoved === undefined ? {} : { onRecordRemoved: options.onRecordRemoved }),
    ...(options.updateMenu === undefined ? {} : { updateMenu: options.updateMenu }),
    ...(options.crashMenu === undefined ? {} : { crashMenu: options.crashMenu }),
  })
  return { picker, ipcMain, windows, opened, revealed, menu: () => applicationMenu, redraws: () => redraws }
}

/** builtMenuItems is `Menu.buildFromTemplate` as these tests need it: a fresh
 * item per template entry, and a checkbox whose own `checked` has already moved
 * by the time its handler runs -- which is what Electron's checkbox does, and
 * the whole reason the picker draws the menu again from the value in force
 * rather than leaving the item the click moved. Everything else about the
 * template is kept, because the assertions are made on what the app would show. */
function builtMenuItems(value: unknown): unknown {
  if (!Array.isArray(value)) return value
  return (value as readonly unknown[]).map(builtMenuItem)
}

function builtMenuItem(value: unknown): unknown {
  if (typeof value !== 'object' || value === null) return value
  const template = value as Record<string, unknown>
  const built: Record<string, unknown> = { ...template }
  if (Array.isArray(template['submenu'])) built['submenu'] = builtMenuItems(template['submenu'])
  const handler = template['click']
  if (typeof handler === 'function') {
    built['click'] = () => {
      if (built['type'] === 'checkbox') built['checked'] = !built['checked']
      ;(handler as (item: unknown) => void)(built)
    }
  }
  return built
}

/** appMenuItems is the application submenu as the tests read it: the one menu
 * this app builds, holding `Installations…`, the update items when there are
 * any, and Quit. */
interface MenuItem {
  readonly label?: string
  readonly role?: string
  readonly type?: string
  readonly checked?: boolean
  readonly submenu?: readonly MenuItem[]
  /** click is the built item's own: these tests call it the way Electron does,
   * with nothing, and the fake moves `checked` first. */
  readonly click?: (item?: { readonly checked: boolean }) => void
}

function appMenuItems(installed: unknown): readonly MenuItem[] {
  const top = installed as readonly MenuItem[]
  expect(top).toHaveLength(3)
  const items = top[0]?.submenu
  if (items === undefined) throw new Error('the application menu has no submenu')
  return items
}

/** frozenPaths is every path in the value that is frozen. Electron's
 * `buildFromTemplate` sorts the template in place and assigns each item's
 * `submenu`, so a frozen one throws at startup: this names whatever would. */
function frozenPaths(value: unknown): readonly string[] {
  const frozen: string[] = []
  const walk = (node: unknown, at: string): void => {
    if (typeof node !== 'object' || node === null) return
    if (Object.isFrozen(node)) frozen.push(at)
    for (const [key, child] of Object.entries(node)) walk(child, `${at}.${key}`)
  }
  walk(value, 'template')
  return frozen
}

/** dashboardEvent and pickerEvent are forged events from each document, with the
 * frame the sender gate reads. */
function eventFor(url: string): ConsoleEventLike {
  const frame = { url }
  return { senderFrame: frame, sender: { mainFrame: frame } }
}

/** answer calls one picker channel with a picker frame and returns the reply. */
async function answer(
  ipcMain: ReturnType<typeof fakeIpcMain>,
  name: Parameters<typeof installationChannel>[0],
  args?: unknown,
): Promise<ConsoleReply> {
  const listener = ipcMain.listeners.get(installationChannel(name))
  expect(listener, name).toBeDefined()
  return (await listener?.(eventFor(PICKER_ENTRY_URL), args ?? {})) as ConsoleReply
}

/** listed is the document the stub prints, as a value: two installations, one of
 * them with a config that could not be read. */
const LISTED = {
  installations: [
    { name: 'sandbox', gcp_project: 'fugaro-sandbox-482301', region: 'europe-west4' },
    { name: 'acme', gcp_project: '', region: '' },
  ],
  warnings: ['/Users/owner/.config/fugaro/projects/acme.yaml could not be read: permission denied'],
}

/** listableStub installs a stub that answers the installations document above. */
function listableStub(): Stub {
  const directory = freshDirectory()
  const stub = installStub(join(directory, 'bin'), {
    commands: {
      version: { stdout: `${CLOCKED}\n` },
      doctor: { stdout: '{"ok":true,"checks":[]}\n' },
      'config list': { stdout: `${JSON.stringify(LISTED)}\n` },
    },
  })
  return stub
}

// ---------------------------------------------------------------------------
// The table, and its copy in the preload
// ---------------------------------------------------------------------------

describe('the picker’s allow-list', () => {
  it('is closed, frozen, and six rows wide', () => {
    // Six, because the form is gone (owner's ruling of 2026-10-20) and the
    // reveal affordance arrived with Part D: the list, one click that opens an
    // installation, the checkout it is used from, the removal of this console's
    // own record, the one directory Explorer opens, and the one link. A row that
    // came back -- `add`, `pickBinary` -- fails here by name.
    expect(INSTALLATION_OPERATIONS.map((row) => row.name)).toEqual([
      'list',
      'choose',
      'checkout',
      'remove',
      'reveal',
      'openExternal',
    ])
    expect(Object.isFrozen(INSTALLATION_OPERATIONS)).toBe(true)
    for (const row of INSTALLATION_OPERATIONS) expect(Object.isFrozen(row)).toBe(true)
    expect(INSTALLATION_CHANNEL_PREFIX).toBe('fugaro:installations/')
    expect(installationChannel('remove')).toBe('fugaro:installations/remove')
    // The one row that takes no arguments at all, and the reason the URL never
    // crosses the boundary: a renderer that sends a `url` key has sent a key no
    // row declares.
    expect(INSTALLATION_OPERATIONS.find((row) => row.name === 'openExternal')?.args).toEqual([])
  })

  it('is mirrored by the preload, exactly', () => {
    // The duplicate is deliberate and this is what makes it safe: the preload
    // may not import the main module (the sandbox has no `node:fs`), so the
    // comparison has to be a test's.
    expect(PRELOAD_OPERATIONS.map(([name]) => name)).toEqual(INSTALLATION_OPERATIONS.map((row) => row.name))
    expect(PRELOAD_OPERATIONS.map(([, method]) => method)).toEqual(INSTALLATION_OPERATIONS.map((row) => row.method))
    expect(INSTALLATION_BRIDGE_PREFIX).toBe(INSTALLATION_CHANNEL_PREFIX)
    expect(INSTALLATION_BRIDGE_KEY).toBe('fugaroInstallations')
    // ...and the dashboard's prefix is a *different* namespace, which is what
    // the sender narrowing below turns into two unreachable-from-each-other
    // surfaces.
    expect(INSTALLATION_CHANNEL_PREFIX).not.toBe('fugaro:console/')
  })
})

// ---------------------------------------------------------------------------
// The bridge: no invoker, one method per row, channels built in one place
// ---------------------------------------------------------------------------

describe('the picker’s bridge', () => {
  it('exposes one method per row and nothing else', () => {
    const calls: string[] = []
    const bridge = createInstallationBridge({
      invoke: async (channel: string) => {
        calls.push(channel)
        return { ok: true, data: null }
      },
    })
    const keys = Object.keys(bridge).sort()
    expect(
      keys,
    ).toEqual(['checkout', 'choose', 'list', 'openExternal', 'operations', 'remove', 'reveal', 'version'].sort())
    // The three names a bridge would have, and their absence is the design's
    // rule 5: there is no general invoker to point at a channel.
    for (const forbidden of ['invoke', 'send', 'on', 'channel']) expect(keys).not.toContain(forbidden)
    expect(Object.isFrozen(bridge)).toBe(true)
    expect(bridge.version).toBe(1)
    expect(bridge.operations).toEqual(INSTALLATION_OPERATIONS.map((row) => row.name))
    return bridge.list().then(() => {
      expect(calls).toEqual(['fugaro:installations/list'])
    })
  })

  it('never rejects: a main process that throws answers a refusal', async () => {
    const reply = await invokeOperation(
      {
        invoke: async () => {
          throw new Error('the channel is gone')
        },
      },
      'list',
      undefined,
    )
    expect(reply.ok).toBe(false)
    if (!reply.ok) expect(reply.reason).toContain('could not be reached')
  })

  it('answers a refusal for a reply that is not one', async () => {
    const reply = await invokeOperation({ invoke: async () => 'nonsense' }, 'list', undefined)
    expect(reply.ok).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// The sender gate, both ways
// ---------------------------------------------------------------------------

describe('the two documents’ surfaces', () => {
  it('refuses a dashboard frame on every picker channel', async () => {
    const { picker, ipcMain } = pickerOver()
    void picker
    for (const row of INSTALLATION_OPERATIONS) {
      const listener = ipcMain.listeners.get(installationChannel(row.name))
      expect(listener, row.name).toBeDefined()
      const reply = (await listener?.(eventFor(APP_ENTRY_URL), {})) as ConsoleReply
      expect(reply, row.name).toEqual({ ok: false, reason: SENDER_REFUSAL })
    }
  })

  it('refuses a picker frame on every dashboard channel', async () => {
    const ipcMain = fakeIpcMain()
    const handlers = Object.fromEntries(
      CONSOLE_OPERATION_NAMES.map((name) => [name, () => null]),
    ) as unknown as ConsoleHandlers
    registerConsoleHandlers(ipcMain, handlers)
    for (const name of CONSOLE_OPERATION_NAMES) {
      const listener = ipcMain.listeners.get(channelForOperation(name))
      const reply = (await listener?.(eventFor(PICKER_ENTRY_URL), {})) as ConsoleReply
      expect(reply, name).toEqual({ ok: false, reason: SENDER_REFUSAL })
    }
  })

  it('answers the dashboard’s own frame on the dashboard channels', async () => {
    // The other half, without which the two tests above would pass on a gate
    // that refused everything.
    const ipcMain = fakeIpcMain()
    const handlers = Object.fromEntries(
      CONSOLE_OPERATION_NAMES.map((name) => [name, () => null]),
    ) as unknown as ConsoleHandlers
    registerConsoleHandlers(ipcMain, handlers)
    const listener = ipcMain.listeners.get(channelForOperation('installation.current'))
    const reply = (await listener?.(eventFor(APP_ENTRY_URL), {})) as ConsoleReply
    expect(reply).toEqual({ ok: true, data: null })
    // ...and a fragment on that document is still that document.
    const fragment = (await listener?.(eventFor(`${APP_ENTRY_URL}#installations`), {})) as ConsoleReply
    expect(fragment).toEqual({ ok: true, data: null })
  })

  it('answers the picker’s own frame on the picker channels, and only that frame', async () => {
    const { picker, ipcMain } = pickerOver({ prefixes: [freshDirectory()] })
    void picker
    const listener = ipcMain.listeners.get(installationChannel('list'))
    const reply = (await listener?.(eventFor(`${PICKER_ENTRY_URL}#top`), {})) as ConsoleReply
    expect(reply.ok).toBe(true)
    if (reply.ok) {
      const data = reply.data as { rows: readonly unknown[]; path: string }
      expect(data.rows).toEqual([])
      expect(data.path).toContain('installations.json')
    }
    // A query string is still the same document; another path on the same
    // origin is not.
    expect(((await listener?.(eventFor(`${PICKER_ENTRY_URL}?x=1`), {})) as ConsoleReply).ok).toBe(true)
    expect((await listener?.(eventFor('app://console/other.html'), {})) as ConsoleReply).toEqual({
      ok: false,
      reason: SENDER_REFUSAL,
    })
  })
})

// ---------------------------------------------------------------------------
// The list: what the CLI printed, and the two onboarding screens
// ---------------------------------------------------------------------------

describe('the picker’s list', () => {
  it('draws the installations the CLI listed, with its warning lines', async () => {
    const { picker, ipcMain } = pickerOver({ prefixes: [listableStub().directory] })
    void picker
    const reply = await answer(ipcMain, 'list')
    expect(reply.ok).toBe(true)
    if (!reply.ok) return
    const data = reply.data as {
      onboard: { kind: string }
      rows: readonly { project: string; gcpProject: string; region: string; configUnreadable: boolean; listed: boolean }[]
      warnings: readonly string[]
    }
    expect(data.onboard.kind).toBe('none')
    expect(data.rows.map((row) => [row.project, row.gcpProject, row.region, row.configUnreadable, row.listed])).toEqual([
      ['sandbox', 'fugaro-sandbox-482301', 'europe-west4', false, true],
      // The row whose config could not be read is a row: the CLI listed it, and
      // the warning beneath says why. A picker that dropped it would be the
      // silent omission `config list` refuses to make.
      ['acme', '', '', true, true],
    ])
    expect([...data.warnings]).toEqual(LISTED.warnings)
  })

  it('answers the absent-binary screen, with the command and the places it looked', async () => {
    // A prefix list with nothing in it, so the resolution fails for the reason
    // this screen is for -- without a real `/usr/local/bin` deciding the answer.
    const empty = freshDirectory()
    const { picker, ipcMain } = pickerOver({ prefixes: [empty] })
    void picker
    const reply = await answer(ipcMain, 'list')
    expect(reply.ok).toBe(true)
    if (!reply.ok) return
    const data = reply.data as {
      onboard: { kind: string; reason: string; searched: readonly string[]; command: string }
      rows: readonly unknown[]
      warnings: readonly string[]
    }
    expect(data.onboard.kind).toBe('install-cli')
    expect(data.onboard.command).toBe(FUGARO_INSTALL_COMMAND)
    expect(data.onboard.command).toBe('brew install dimipaun/tap/fugaro')
    // The places it looked are a fact of the resolution, not a guess: rule 4
    // names *candidates*, so each entry is a prefix with the binary's own name
    // already joined.
    expect([...data.onboard.searched]).toEqual([join(empty, FUGARO_BINARY_NAME)])
    expect(data.onboard.reason).toContain(join(empty, FUGARO_BINARY_NAME))
    expect(data.rows).toEqual([])
    // The screen is the whole of what the window says: the resolution's sentence
    // is not *also* handed over as a warning line, which would draw it twice.
    expect(data.warnings).toEqual([])
  })

  it('answers the no-installation screen when a binary answered and nothing was listed', async () => {
    const { picker, ipcMain } = pickerOver({ prefixes: [seededStub().directory] })
    void picker
    const reply = await answer(ipcMain, 'list')
    expect(reply.ok).toBe(true)
    if (!reply.ok) return
    const data = reply.data as { onboard: { kind: string; command: string }; rows: readonly unknown[] }
    expect(data.onboard.kind).toBe('first-installation')
    expect(data.onboard.command).toBe(FUGARO_SETUP_COMMAND)
    expect(data.onboard.command).toBe('fugaro setup')
    expect(data.rows).toEqual([])
  })

  it('keeps a record the CLI did not list, so an older store still opens', async () => {
    // `installations.json` written by a console whose CLI cannot enumerate, and a
    // CLI that lists nothing: the record is still a row, marked as this console's
    // own, and it can still be chosen -- which is what "no migration" means for
    // the store's optional binary override.
    const root = freshDirectory()
    const userData = join(root, 'user-data')
    mkdirSync(userData, { recursive: true, mode: 0o700 })
    const stub = installStub(join(root, 'bin'), {
      commands: {
        version: { stdout: `${CLOCKED}\n` },
        doctor: { stdout: '{"ok":true,"checks":[]}\n' },
        'config list': { stdout: '{"installations":[],"warnings":[]}\n' },
      },
    })
    writeFileSync(
      join(userData, INSTALLATIONS_FILE_NAME),
      serializeInstallationFile({
        version: 1,
        active: '0123456789abcdef',
        installations: [{ id: '0123456789abcdef', project: 'legacy', binaryPath: stub.binary }],
      }),
      { mode: 0o600 },
    )
    const { picker, ipcMain } = pickerOver({ userData, prefixes: [stub.binary] })
    const reply = await answer(ipcMain, 'list')
    expect(reply.ok).toBe(true)
    if (!reply.ok) return
    const data = reply.data as { onboard: { kind: string }; rows: readonly { project: string; listed: boolean; saved: boolean; active: boolean }[] }
    // Not the first-installation screen: the console has an installation, it is
    // this console's own record, and it is the one the dashboard is showing.
    expect(data.onboard.kind).toBe('none')
    expect(data.rows).toEqual([
      expect.objectContaining({ project: 'legacy', listed: false, saved: true, active: true }),
    ])
    // ...and it chooses, which is the whole point of keeping it.
    const chosen = await answer(ipcMain, 'choose', { project: 'legacy' })
    expect(chosen.ok).toBe(true)
    expect(picker.installation()).toMatchObject({ chosen: true, project: 'legacy' })
  })

  it('carries the startup’s handshake into the dashboard’s read and the picker’s row', async () => {
    // What `src/main/index.ts` does with the startup read: `onDecided` hands the
    // compatibility to `picker.remember(project, …)`, and the two readers a
    // launch draws -- `installation.current` (the dashboard's header) and the
    // picker's rows -- both ask the registry for it **by project**
    // (`compatibility.get(active.project)`, `rowsOf`'s `compatibility.get(
    // installation.project)`). Remembering under the installation's *id*
    // instead put the answer where nothing looks: the header drew "version not
    // read · unknown" and the active row said `unknown` on every launch, with
    // the handshake's own answer thrown away.
    const root = freshDirectory()
    const userData = join(root, 'user-data')
    mkdirSync(userData, { recursive: true, mode: 0o700 })
    writeFileSync(
      join(userData, INSTALLATIONS_FILE_NAME),
      serializeInstallationFile({
        version: 1,
        active: '0123456789abcdef',
        installations: [{ id: '0123456789abcdef', project: 'legacy' }],
      }),
      { mode: 0o600 },
    )
    const { picker, ipcMain } = pickerOver({ userData, prefixes: [freshDirectory()] })
    // Nothing has read it in this run, so nothing may say `ok` (review S1). The
    // non-vacuity half: the assertions below would pass on a console that always
    // answered `ok`.
    const before = picker.installation()
    expect(before).toMatchObject({ chosen: true, project: 'legacy', mode: 'unknown', cliVersion: null })
    const read = classifyCliVersion(CLOCKED)
    picker.remember('legacy', read)
    // The dashboard's header...
    expect(picker.installation()).toMatchObject({
      chosen: true,
      project: 'legacy',
      mode: read.mode,
      cliVersion: read.cliVersion,
    })
    // ...and the row the picker draws, from the same answer.
    const listed = await answer(ipcMain, 'list')
    expect(listed.ok).toBe(true)
    if (!listed.ok) return
    const rows = (listed.data as { rows: readonly { project: string; mode: string }[] }).rows
    expect(rows).toEqual([expect.objectContaining({ project: 'legacy', mode: read.mode })])
    // The id is a different string, and it is not a second key: a reader that
    // asked for it would answer `unknown` while the project's entry sat there.
    expect(read.mode).toBe('ok')
  })

  it('draws the store’s own rows beside the CLI’s refusal, rather than hiding them', async () => {
    // The CLI is gone between one read and the next -- the operator moved it, or
    // the upgrade is mid-flight. The console still has its own record, and the
    // refusal is a warning line over it, not an empty window.
    const root = freshDirectory()
    const userData = join(root, 'user-data')
    mkdirSync(userData, { recursive: true, mode: 0o700 })
    writeFileSync(
      join(userData, INSTALLATIONS_FILE_NAME),
      serializeInstallationFile({
        version: 1,
        active: null,
        installations: [{ id: '0123456789abcdef', project: 'legacy' }],
      }),
      { mode: 0o600 },
    )
    const empty = freshDirectory()
    const { picker, ipcMain } = pickerOver({ userData, prefixes: [empty] })
    void picker
    const reply = await answer(ipcMain, 'list')
    expect(reply.ok).toBe(true)
    if (!reply.ok) return
    const data = reply.data as { rows: readonly { project: string; listed: boolean }[]; warnings: readonly string[] }
    expect(data.rows).toEqual([expect.objectContaining({ project: 'legacy', listed: false })])
    // ...and the resolution's own sentence is the *screen*, not a warning line
    // over it: `warnings` is the CLI's own warning list, and nothing was read.
    expect(data.warnings).toEqual([])
    expect((reply.data as { onboard: { kind: string } }).onboard.kind).toBe('install-cli')
  })
})

// ---------------------------------------------------------------------------
// Choose, checkout, remove -- one click each
// ---------------------------------------------------------------------------

describe('the picker’s choose', () => {
  it('opens the installation the CLI listed, and hands the resolved binary to the window', async () => {
    const handed: { project: string; version: string; resolvedBinaryPath: string }[] = []
    const stub = listableStub()
    const { picker, ipcMain } = pickerOver({
      prefixes: [stub.directory],
      onChosen: (chosen) => {
        handed.push({
          project: chosen.project,
          version: chosen.compatibility.cliVersion ?? '',
          resolvedBinaryPath: chosen.resolvedBinaryPath,
        })
      },
    })
    void picker
    // The list first, which is what the renderer does: the rows are its own, and
    // the choose is a click on one of them.
    await answer(ipcMain, 'list')
    const reply = await answer(ipcMain, 'choose', { project: 'sandbox' })
    expect(reply.ok).toBe(true)
    if (!reply.ok) return
    expect(handed).toEqual([{ project: 'sandbox', version: CLOCKED, resolvedBinaryPath: stub.binary }])
    // The choice is persisted, and the list comes back with it: one shape for
    // every operation.
    expect(picker.installation()).toMatchObject({ chosen: true, project: 'sandbox' })
    const data = reply.data as { rows: readonly { project: string; active: boolean; saved: boolean }[] }
    expect(data.rows.find((row) => row.project === 'sandbox')).toMatchObject({ active: true, saved: true })
  })

  it('does not choose a CLI this console cannot drive, and hands nothing over (review S11)', async () => {
    const refused = installStub(join(freshDirectory(), 'bin'), {
      commands: { version: { stdout: '0.5.0\n' }, 'config list': { stdout: '{"installations":[{"name":"old"}],"warnings":[]}\n' } },
    })
    const handed: string[] = []
    const { picker, ipcMain } = pickerOver({
      prefixes: [refused.directory],
      onChosen: (chosen) => {
        handed.push(chosen.project)
      },
    })
    void picker
    await answer(ipcMain, 'list')
    const reply = await answer(ipcMain, 'choose', { project: 'old' })
    // The list itself is answered -- the installation is a fact of the machine
    // whatever its CLI reports -- and the *choose* is refused, with the CLI's own
    // version in the sentence.
    expect(reply.ok).toBe(false)
    if (!reply.ok) expect(reply.reason).toContain('0.5.0')
    expect(handed).toEqual([])
    // **Nothing was chosen**, and the store says so: the row the console just
    // listed is still `saved: false` with no `active`, which is what "the choose
    // wrote nothing" means from the renderer's side. What changed with the
    // owner's design ruling of 2026-10-25 is that the console is nonetheless
    // **open** on it -- the first readable installation is opened without being
    // chosen (`openProject`, `installationstate.ts`) -- so `installation()`
    // answers that session and not "no installation has been chosen".
    const relisted = await answer(ipcMain, 'list')
    if (!relisted.ok) throw new Error(relisted.reason)
    const data = relisted.data as { rows: readonly { project: string; active: boolean; saved: boolean }[] }
    expect(data.rows.find((row) => row.project === 'old')).toMatchObject({ active: false, saved: false })
    expect(picker.installation()).toMatchObject({ chosen: true, project: 'old' })
  })

  it('refuses a project the console never listed', async () => {
    const { picker, ipcMain } = pickerOver({ prefixes: [seededStub().directory] })
    void picker
    const reply = await answer(ipcMain, 'choose', { project: 'invented' })
    expect(reply.ok).toBe(false)
    // The CLI is asked for it (that is what a choose does) and refuses; what the
    // console does not do is make the project up, or write a record for it.
    if (!reply.ok) expect(reply.reason).not.toBe('')
  })

  it('runs one mutation at a time: a second choose waits for the first one’s window (review S6)', async () => {
    let release: () => void = () => undefined
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    const handed: string[] = []
    const binary = listableStub().directory
    const { ipcMain } = pickerOver({
      prefixes: [binary],
      onChosen: async (chosen) => {
        handed.push(chosen.project)
        // The first choose's window is still opening.
        if (handed.length === 1) await held
      },
    })
    await answer(ipcMain, 'list')
    const first = answer(ipcMain, 'choose', { project: 'sandbox' })
    const second = answer(ipcMain, 'choose', { project: 'acme' })
    // The first choose reaches its window; then, long enough for the second probe
    // to have finished were it allowed to run, the second has not.
    await vi.waitFor(() => {
      expect(handed).toHaveLength(1)
    }, { timeout: 10_000 })
    await new Promise((resolve) => setTimeout(resolve, 1000))
    expect(handed).toEqual(['sandbox'])
    release()
    await Promise.all([first, second])
    expect(handed).toEqual(['sandbox', 'acme'])
  }, 20_000)
})

describe('the picker’s checkout', () => {
  /** withCheckouts is a stub whose document lists one checkout for `sandbox`, and
   * a scratch directory that actually exists -- `validateInstallation` stats it,
   * because it becomes a child's working directory (rule 5). */
  function withCheckouts(): { readonly directory: string; readonly checkout: string } {
    const checkout = freshDirectory()
    const stub = installStub(join(freshDirectory(), 'bin'), {
      commands: {
        version: { stdout: `${CLOCKED}\n` },
        doctor: { stdout: '{"ok":true,"checks":[]}\n' },
        'config list': {
          stdout: `${JSON.stringify({ installations: [{ name: 'sandbox', gcp_project: 'p', region: 'r', checkouts: [{ repository: 'github/o/r', path: checkout, enrolled: true, valid: true }] }], warnings: [] })}\n`,
        },
      },
    })
    return { directory: stub.directory, checkout }
  }

  it('refuses a checkout the CLI listed under a temporary root, as the screen never offers it', async () => {
    // The renderer is not trusted to keep to the drawn list: the write is held to
    // the same list the rail offers, so a temporary or tool-owned directory the
    // CLI listed is refused here as well as left off the screen.
    const { directory, checkout } = withCheckouts()
    const { picker, ipcMain } = pickerOver({ prefixes: [directory], tempRoots: TEMP_ROOTS })
    void picker
    const listed = (await answer(ipcMain, 'list')) as ConsoleReply
    expect(listed.ok).toBe(true)
    if (listed.ok) {
      const data = listed.data as { rows: readonly { project: string; checkouts: readonly string[] }[] }
      expect(data.rows.find((row) => row.project === 'sandbox')?.checkouts).toEqual([])
    }
    const refused = (await answer(ipcMain, 'checkout', { project: 'sandbox', checkout })) as ConsoleReply
    expect(refused.ok).toBe(false)
    if (!refused.ok) expect(refused.reason).toContain('not one of the checkouts the CLI listed')
  })

  it('refuses a listed checkout that is a symlink into a tool-owned directory, offer and write both (review m1)', async () => {
    const target = join(freshDirectory(), 'scratchpad', 'real')
    mkdirSync(target, { recursive: true })
    const link = join(freshDirectory(), 'link')
    symlinkSync(target, link)
    const stub = installStub(join(freshDirectory(), 'bin'), {
      commands: {
        version: { stdout: `${CLOCKED}\n` },
        doctor: { stdout: '{"ok":true,"checks":[]}\n' },
        'config list': {
          stdout: `${JSON.stringify({ installations: [{ name: 'sandbox', gcp_project: 'p', region: 'r', checkouts: [{ repository: 'github/o/r', path: link, enrolled: true, valid: true }] }], warnings: [] })}\n`,
        },
      },
    })
    const { picker, ipcMain } = pickerOver({ prefixes: [stub.directory] })
    void picker
    const listed = (await answer(ipcMain, 'list')) as ConsoleReply
    expect(listed.ok).toBe(true)
    if (listed.ok) {
      const data = listed.data as { rows: readonly { project: string; checkouts: readonly string[] }[] }
      expect(data.rows.find((row) => row.project === 'sandbox')?.checkouts).toEqual([])
    }
    const refused = (await answer(ipcMain, 'checkout', { project: 'sandbox', checkout: link })) as ConsoleReply
    expect(refused.ok).toBe(false)
  })

  it('offers only what the CLI listed: a directory it never named is refused', async () => {
    const { directory, checkout } = withCheckouts()
    const { picker, ipcMain } = pickerOver({ prefixes: [directory] })
    void picker
    // The list first: the console holds the checkouts from the document it last
    // read, and offers nothing before it has read one.
    expect((await answer(ipcMain, 'checkout', { project: 'sandbox', checkout: freshDirectory() })).ok).toBe(false)
    await answer(ipcMain, 'list')
    const invented = (await answer(ipcMain, 'checkout', { project: 'sandbox', checkout: freshDirectory() })) as ConsoleReply
    expect(invented.ok).toBe(false)
    if (!invented.ok) expect(invented.reason).toContain('not one of the checkouts the CLI listed')
    // ...and the one it did list is accepted, which is what makes the refusal a
    // decision rather than a checkout that never works. It runs rule 7's pairing
    // check, which is the stub's `doctor`.
    const accepted = await answer(ipcMain, 'checkout', { project: 'sandbox', checkout })
    expect(accepted.ok).toBe(true)
    if (accepted.ok) {
      const data = accepted.data as { rows: readonly { project: string; checkout?: string }[] }
      expect(data.rows.find((row) => row.project === 'sandbox')?.checkout).toBe(checkout)
    }
    // ...and the pairing refusal is the CLI's own sentence, shown verbatim.
    const refusing = installStub(join(freshDirectory(), 'bin'), {
      commands: {
        version: { stdout: `${CLOCKED}\n` },
        'config list': {
          stdout: `${JSON.stringify({ installations: [{ name: 'sandbox', checkouts: [{ repository: 'github/o/r', path: checkout, enrolled: true, valid: true }] }], warnings: [] })}\n`,
        },
        doctor: { stderr: 'the checkout is not the project named\n', code: 1 },
      },
    })
    const other = pickerOver({ prefixes: [refusing.directory] })
    void other.picker
    await answer(other.ipcMain, 'list')
    const refused = (await answer(other.ipcMain, 'checkout', { project: 'sandbox', checkout })) as ConsoleReply
    expect(refused.ok).toBe(false)
    if (!refused.ok) expect(refused.reason).toContain('not the project named')
  })

  it('probes the record’s own override, and its config, rather than a second walk of rule 4', async () => {
    // `setInstallationCheckout` validated `{ project, checkout }` alone: the
    // record's own `binaryPath` and `configPath` were not part of the candidate,
    // so rule 4's walk decided which CLI the version handshake and rule 7's
    // pairing `doctor` ran. Two installations go wrong at once, and this case
    // holds both halves:
    //
    //   - one whose **only** `fugaro` is the override it carries is refused, by a
    //     console that could drive it perfectly well;
    //   - and one whose override is a *different* CLI than the walk finds is
    //     validated by a binary this console does not drive, with no `--config`.
    const root = freshDirectory()
    const userData = join(root, 'user-data')
    mkdirSync(userData, { recursive: true, mode: 0o700 })
    const checkout = freshDirectory()
    const config = join(root, 'sandbox.yaml')
    const override = installStub(join(root, 'own'), {
      commands: {
        version: { stdout: `${CLOCKED}\n` },
        doctor: { stdout: '{"ok":true,"checks":[]}\n' },
        'config list': {
          stdout: `${JSON.stringify({ installations: [{ name: 'sandbox', checkouts: [{ repository: 'github/o/r', path: checkout, enrolled: true, valid: true }] }], warnings: [] })}\n`,
        },
      },
    })
    // The `fugaro` a walk of rule 4 finds when it does not start from the record:
    // a CLI this console cannot drive. It is on that walk's own list, which is
    // what makes the assertions below about the *choice* of binary rather than
    // about a decoy nothing would ever run.
    const decoy = installStub(join(root, 'elsewhere', 'bin'), {
      commands: { version: { stdout: '0.5.0\n' } },
    })
    expect(binDirectories({ prefixes: [decoy.directory] }), 'the case proves nothing: rule 4 would not find the decoy').toContain(
      decoy.binary,
    )
    writeFileSync(
      join(userData, INSTALLATIONS_FILE_NAME),
      serializeInstallationFile({
        version: 1,
        active: '0123456789abcdef',
        installations: [
          { id: '0123456789abcdef', project: 'sandbox', binaryPath: override.binary, configPath: config },
        ],
      }),
      { mode: 0o600 },
    )
    const { picker, ipcMain } = pickerOver({ userData, prefixes: [decoy.directory] })
    void picker
    await answer(ipcMain, 'list')
    const accepted = await answer(ipcMain, 'checkout', { project: 'sandbox', checkout })
    // The installation works: its override answers the handshake and the pairing
    // check, and it is the *only* `fugaro` this console should have asked.
    expect(accepted, 'a working installation whose only fugaro is its override was refused').toMatchObject({ ok: true })
    expect(
      [...new Set(override.reports().map((report) => report.argv[0]))].sort(),
      'the record’s own binary did not run the read, the handshake and the pairing check',
    ).toEqual(['config', 'doctor', 'version'])
    expect(decoy.calls(), 'a binary this console does not drive was asked to validate the checkout').toBe(0)
    // ...and the pairing check ran with the record's own config path, which is a
    // field of the record for exactly this reason: rule 3's `--config` names the
    // config `doctor` reads, and a checkout validated without it is validated
    // against whatever the CLI would have read otherwise.
    const doctor = override.reports().find((report) => report.argv[0] === 'doctor')
    expect(doctor, 'the pairing check did not run').toBeDefined()
    expect(doctor?.argv, 'the pairing check ran without the record’s config path').toContain(config)
    expect(doctor?.argv).toContain('--config')
    expect(doctor?.project).toBe('sandbox')
  })

  it('clears a checkout when no directory is sent, and checks nothing', async () => {
    const { directory, checkout } = withCheckouts()
    const { picker, ipcMain } = pickerOver({ prefixes: [directory] })
    void picker
    await answer(ipcMain, 'list')
    expect((await answer(ipcMain, 'checkout', { project: 'sandbox', checkout })).ok).toBe(true)
    const cleared = await answer(ipcMain, 'checkout', { project: 'sandbox' })
    expect(cleared.ok).toBe(true)
    if (cleared.ok) {
      const data = cleared.data as { rows: readonly { project: string; checkout?: string }[] }
      // Back to rule 5's ordinary case: an installation with no checkout.
      expect(data.rows.find((row) => row.project === 'sandbox')?.checkout).toBeUndefined()
    }
  })

  it('writes nothing when a listed project this console holds no record for is cleared', async () => {
    // The other half of the case above, and the one the console got wrong: a
    // project the CLI lists and this console never chose has **no record at
    // all**, so there is no checkout to clear. The reply is still the ordinary
    // list -- the request was satisfiable, it had nothing to do -- but a clear
    // that reached `registry.setCheckout` would *create* a record for it
    // (`recordOf(project)`), writing `installations.json` for an installation the
    // operator never chose: from then on the row is `saved`, the console calls
    // its mode `reduced` for it, and the row can be removed -- every one of them
    // a claim about an installation this console does not hold.
    const { directory } = withCheckouts()
    const { picker, ipcMain } = pickerOver({ prefixes: [directory] })
    await answer(ipcMain, 'list')
    // The store the picker names for itself, and the state it is in first: a
    // launch that has chosen nothing has no file at all, which is what makes the
    // assertion below about this clear rather than about some earlier write.
    const file = picker.storePath
    expect(existsSync(file), 'the store wrote a file before anything was chosen').toBe(false)
    const cleared = await answer(ipcMain, 'checkout', { project: 'sandbox' })
    expect(cleared.ok).toBe(true)
    if (cleared.ok) {
      const data = cleared.data as { rows: readonly { project: string; saved: boolean }[] }
      expect(data.rows.map((row) => [row.project, row.saved])).toEqual([['sandbox', false]])
    }
    expect(existsSync(file), 'a clear wrote a record for a project the console holds nothing about').toBe(false)
  })
})

describe('the picker’s remove', () => {
  it('names every removal that happened to the launch seam, active or not, and none that refused', async () => {
    // `onRemoved` is the dashboard's seam and it fires only for the **active**
    // installation, because a removal of another row leaves the dashboard with
    // an installation to drive. A launch in flight is a different fact: it names
    // its own installation, so a window open for a project whose record just
    // went must close whichever row was active. That is what `onRecordRemoved`
    // is for, and it is handed the **project** -- the name the launcher maps to
    // a run id -- rather than the record id `onRemoved` carries.
    const notified: string[] = []
    const removed: string[] = []
    const { picker, ipcMain } = pickerOver({
      prefixes: [listableStub().directory],
      onRemoved: (id) => notified.push(id),
      onRecordRemoved: (project) => removed.push(project),
    })
    void picker
    await answer(ipcMain, 'list')
    // Two records, and the open rule settles which one this run drives: `choose`
    // moves the store's active installation, and the row the console is open on
    // is the rail's own decision (`openProject`). `acme` is chosen second, so it
    // is the active one; the row that stays open -- and so the one `onRemoved`
    // speaks for -- is `sandbox`.
    expect((await answer(ipcMain, 'choose', { project: 'sandbox' })).ok).toBe(true)
    expect((await answer(ipcMain, 'choose', { project: 'acme' })).ok).toBe(true)

    // The row the dashboard is **not** on: `onRemoved` says nothing, because the
    // console still has an installation to drive, but the removal happened and
    // the launch seam is told which project it was.
    expect((await answer(ipcMain, 'remove', { project: 'acme' })).ok).toBe(true)
    expect(removed).toStrictEqual(['acme'])
    expect(notified).toStrictEqual([])

    // The row the console is open on: both seams fire, and the project is named
    // here too.
    expect((await answer(ipcMain, 'remove', { project: 'sandbox' })).ok).toBe(true)
    expect(removed).toStrictEqual(['acme', 'sandbox'])
    expect(notified).toHaveLength(1)

    // A refusal is not a removal: a project this console holds no record for
    // writes nothing and names nothing.
    expect((await answer(ipcMain, 'remove', { project: 'never-chosen' })).ok).toBe(false)
    expect(removed).toStrictEqual(['acme', 'sandbox'])
    expect(notified).toHaveLength(1)
  })

  it('drops this console’s record, and tells the dashboard when the active one went', async () => {
    const removed: string[] = []
    const { picker, ipcMain } = pickerOver({ prefixes: [listableStub().directory], onRemoved: (id) => removed.push(id) })
    void picker
    await answer(ipcMain, 'list')
    await answer(ipcMain, 'choose', { project: 'sandbox' })
    const active = picker.installation()
    expect(active.chosen && active.id).toBeTruthy()
    // A project the CLI listed but this console never chose has no record to
    // drop, and the refusal says so rather than writing one.
    expect((await answer(ipcMain, 'remove', { project: 'acme' })).ok).toBe(false)
    expect(removed).toEqual([])
    const reply = await answer(ipcMain, 'remove', { project: 'sandbox' })
    expect(reply.ok).toBe(true)
    expect(removed).toEqual([active.chosen ? active.id : ''])
    // The console's own record for `sandbox` is gone -- the removal re-reads, and
    // the row comes back with no `saved` and no `active`, which is the fact the
    // screen draws. The installation itself is still on the machine (the CLI lists
    // it), so the open rule of 2026-10-25 opens it again as this run's **session**:
    // the console reads from an installation it keeps no record of, which is what
    // "every discovered installation is listed in the rail, and one of them is
    // open" means once the operator drops the record for the one that was.
    if (!reply.ok) throw new Error(reply.reason)
    const data = reply.data as { rows: readonly { project: string; active: boolean; saved: boolean }[] }
    expect(data.rows.find((row) => row.project === 'sandbox')).toMatchObject({ active: false, saved: false })
    expect(picker.installation()).toMatchObject({ chosen: true, project: 'sandbox' })
  })
})

// ---------------------------------------------------------------------------
// The one link
// ---------------------------------------------------------------------------

describe('the picker’s external link', () => {
  it('opens the getting-started page, and refuses an argument of any kind', async () => {
    const { picker, ipcMain, opened } = pickerOver({ prefixes: [seededStub().directory] })
    void picker
    const reply = await answer(ipcMain, 'openExternal')
    expect(reply.ok).toBe(true)
    // The URL is this process's own constant, and the operation is the only
    // caller: a renderer never holds the address.
    expect(opened).toEqual([GETTING_STARTED_URL])
    expect(GETTING_STARTED_URL).toBe('https://fugaro.dev/getting-started/')
    // A frame that tries to name a URL is refused **by name**, before any
    // handler: the row takes no argument from a renderer.
    const named = (await answer(ipcMain, 'openExternal', { url: 'https://evil.test/' })) as ConsoleReply
    expect(named).toEqual({ ok: false, reason: 'unknown argument: url' })
    expect(opened).toHaveLength(1)
  })

  it('opens only through the console’s allow-list, so the host is checked', () => {
    // The second gate, and the reason `fugaro.dev` had to be added to it: the
    // link is built in the main process, and it still goes through
    // `openExternalValidated`, whose host list is the one `window.ts` owns.
    expect(validateExternalUrl(GETTING_STARTED_URL)).toEqual({ ok: true, url: GETTING_STARTED_URL })
    expect(ALLOWED_EXTERNAL_HOSTS).toContain('fugaro.dev')
    // A refusal from the path is answered as a refusal, not swallowed: the
    // picker says what happened.
    return pickerOverWithFailingOpen().then(async (built) => {
      const reply = await answer(built.ipcMain, 'openExternal')
      expect(reply.ok).toBe(false)
      if (!reply.ok) expect(reply.reason).toContain('no browser here')
    })
  })

  it('reports a handover the platform declined, rather than a success that drew nothing', async () => {
    // `openExternalValidated` answers a **boolean**, and `false` is how it says
    // that nothing was opened: a URL its own allow-list refused, or a handover
    // the operating system did not take (no handler for `https:`, a dismissed
    // prompt). The reply used to be `ok: true` either way -- and the renderer
    // draws the `reason` of a refusal and nothing else, so the operator clicked
    // "Open the getting-started guide" and watched a window do nothing.
    const built = pickerOver({
      prefixes: [seededStub().directory],
      openExternal: () => Promise.resolve(false),
    })
    const reply = (await answer(built.ipcMain, 'openExternal')) as ConsoleReply
    expect(reply).toEqual({ ok: false, reason: OPEN_EXTERNAL_FAILED })
    // Non-vacuity: the sentence is the console's own and it is drawn, so it says
    // what happened without naming a URL, a host or a reason this process cannot
    // know.
    expect(OPEN_EXTERNAL_FAILED).toBe('the console could not open the link in the browser')
    expect(OPEN_EXTERNAL_FAILED).not.toContain(GETTING_STARTED_URL)
  })
})

/** pickerOverWithFailingOpen is a picker whose external-link path refuses, which
 * is what `openExternalValidated` does when the shell cannot open a URL. */
function pickerOverWithFailingOpen() {
  const built = pickerOver({
    prefixes: [seededStub().directory],
    openExternal: () => Promise.reject(new Error('no browser here')),
  })
  return Promise.resolve(built)
}

// ---------------------------------------------------------------------------
// The window
// ---------------------------------------------------------------------------

describe('the picker’s reveal', () => {
  /** record seeds the store with one installation, which is what the reveal
   * affordance exists for: its checkout is a directory only the platform's own
   * file manager can open -- and for a WSL installation, only under a spelling
   * this window never draws. */
  const record = (installation: Record<string, string>): { readonly userData: string } => {
    const root = freshDirectory()
    const userData = join(root, 'user-data')
    mkdirSync(userData, { recursive: true, mode: 0o700 })
    writeFileSync(
      join(userData, INSTALLATIONS_FILE_NAME),
      serializeInstallationFile({
        version: 1,
        active: '0123456789abcdef',
        installations: [{ id: '0123456789abcdef', project: 'acme', ...installation }],
      }),
      { mode: 0o600 },
    )
    return { userData }
  }
  const CHECKOUT = '/home/dimi/src/acme'
  const UNC = '\\\\wsl.localhost\\Ubuntu\\home\\dimi\\src\\acme'

  it('hands the file manager the UNC spelling of a WSL checkout, composed in this process (T-D3)', async () => {
    const { userData } = record({ checkout: CHECKOUT, distro: 'Ubuntu' })
    const { ipcMain, revealed } = pickerOver({ userData })
    const reply = await answer(ipcMain, 'reveal', { project: 'acme' })
    expect(reply).toMatchObject({ ok: true })
    // The UNC spelling, and not the record's checkout: `checkout` is the Linux
    // path the picker *draws* (the row's own line, `rowText`), and this is the
    // spelling the file manager opens. A drive path does not exist for
    // `/home/dimi/src` (D3(a)).
    expect(revealed).toEqual([UNC])
  })

  it('hands a host row’s own checkout, unchanged', async () => {
    // No distribution: the checkout is already the spelling this platform's file
    // manager opens, so there is nothing to translate and `wslUncPath` is not
    // consulted -- a UNC form here would be a path Windows would refuse.
    const { userData } = record({ checkout: '/home/dimi/src/acme' })
    const { ipcMain, revealed } = pickerOver({ userData })
    const reply = await answer(ipcMain, 'reveal', { project: 'acme' })
    expect(reply).toMatchObject({ ok: true })
    expect(revealed).toEqual([CHECKOUT])
  })

  it('takes no path from the renderer: an argument the row does not declare is refused', async () => {
    // The path the file manager is handed is the main process's own, so a frame
    // on the picker's document cannot name a directory for it to open. This is
    // the rule the external link's URL keeps, checked the same way.
    const { userData } = record({ checkout: CHECKOUT, distro: 'Ubuntu' })
    const { ipcMain, revealed } = pickerOver({ userData })
    const smuggled = await answer(ipcMain, 'reveal', { project: 'acme', path: 'C:\\Windows\\System32' })
    expect(smuggled.ok).toBe(false)
    expect(revealed).toEqual([])
  })

  it('refuses a project this console has no record for, and one with nothing to open', async () => {
    const { userData } = record({ checkout: CHECKOUT, distro: 'Ubuntu' })
    const { ipcMain, revealed } = pickerOver({ userData })
    const unknown = await answer(ipcMain, 'reveal', { project: 'never-listed' })
    expect(unknown).toMatchObject({ ok: false, reason: REVEAL_FAILED })
    // A record with no checkout (rule 5's ordinary case) is the same refusal,
    // rather than a success that opened nothing.
    const bare = record({})
    const none = pickerOver({ userData: bare.userData })
    const nothing = await answer(none.ipcMain, 'reveal', { project: 'acme' })
    expect(nothing).toMatchObject({ ok: false, reason: REVEAL_FAILED })
    expect(revealed).toEqual([])
  })

  it('reports a handover the platform declined, rather than a success that drew nothing', async () => {
    const { userData } = record({ checkout: CHECKOUT, distro: 'Ubuntu' })
    const { ipcMain, revealed } = pickerOver({ userData, reveal: () => false })
    const reply = await answer(ipcMain, 'reveal', { project: 'acme' })
    expect(reply).toMatchObject({ ok: false, reason: REVEAL_FAILED })
    expect(revealed).toHaveLength(1)
  })

  it('keeps the Windows spelling out of the answer', async () => {
    // The renderer drew the button from the row's own checkout; the reply names
    // no path, so the one spelling this window keeps to itself is not echoed
    // across the boundary for a reader that does not exist.
    const { userData } = record({ checkout: CHECKOUT, distro: 'Ubuntu' })
    const { ipcMain } = pickerOver({ userData })
    const reply = await answer(ipcMain, 'reveal', { project: 'acme' })
    expect(JSON.stringify(reply)).not.toContain('wsl.localhost')
  })
})

describe('the picker’s window', () => {
  it('opens its own document when it is asked to, and focuses the window it already has', async () => {
    // **Nothing opens this window by itself** (owner's design ruling of
    // 2026-10-25): the console never shows a picker screen, so the only two
    // callers are the rail's `+` door (`installation.open`) and the menu item.
    // What is asserted here is that `open` is the one door and that it is
    // idempotent -- a second call focuses rather than building a second window.
    const { picker, windows } = pickerOver()
    expect(windows).toHaveLength(0)
    await picker.open()
    await picker.open()
    expect(windows).toHaveLength(1)
    // The picker's own document, on the console's own origin, and the preload is
    // the picker's -- a window that got the dashboard's preload would expose
    // twenty-two operations to a document whose whole job is to draw a list.
    expect(windows[0]?.loaded).toEqual([PICKER_ENTRY_URL])
    expect(PICKER_ENTRY_URL).toBe('app://console/installations.html')
  })

  it('is built through the same hardened path as the dashboard (buildHardenedWindow)', async () => {
    const { picker, windows } = pickerOver()
    await picker.open()
    const built = windows[0]
    expect(built, 'no picker window was built').toBeDefined()
    // The three webContents denials, the title refusal and the show-when-ready
    // are what `buildHardenedWindow` installs on every window this app opens; a
    // picker built by hand beside it would be the one window without them.
    expect([...(built?.handlerNames() ?? [])].sort()).toEqual(
      ['close', 'closed', 'page-title-updated', 'ready-to-show', 'will-attach-webview', 'will-navigate'].sort(),
    )
    expect(built?.openHandlerSet()).toBe(true)
    expect(built?.options).toEqual(pickerOptions(pickerPreloadPath('/work/repo/console')))
    expect(built?.options['webPreferences']).toEqual({
      ...consoleWebPreferences(),
      preload: '/work/repo/console/dist/preload/installations.js',
    })
  })

  it('treats a picker closed while it loads as dismissed, not as a failure', async () => {
    // A load aborted because the operator closed the picker must not be
    // reported as a failure -- the door that opened it (the rail's `+`, the menu
    // item) is not a screen anybody is waiting on. Any other load failure still
    // is.
    const dismissed = pickerOver({ loadFails: 'closed' })
    await expect(dismissed.picker.open()).resolves.toBeUndefined()
    expect(dismissed.windows).toHaveLength(1)
    const closing = pickerOver({ loadFails: 'closing' })
    await expect(closing.picker.open()).resolves.toBeUndefined()
    const broken = pickerOver({ loadFails: 'error' })
    await expect(broken.picker.open()).rejects.toThrow(/ERR_FAILED/)
  })

  it('is opened from the menu item and from nowhere else', async () => {
    const { picker, windows, menu } = pickerOver()
    picker.installMenu()
    // Ours, then Electron's own Edit and Window menus (review S10).
    const installed = menu()
    expect((installed as readonly MenuItem[]).slice(1).map((entry) => entry.role)).toEqual(['editMenu', 'windowMenu'])
    const items = appMenuItems(installed)
    const item = items.find((entry) => entry.label === INSTALLATIONS_MENU_LABEL)
    expect(item, INSTALLATIONS_MENU_LABEL).toBeDefined()
    expect(INSTALLATIONS_MENU_LABEL).toBe('Installations…')
    expect(frozenPaths(installed)).toEqual([])
    expect(windows).toHaveLength(0)
    item?.click?.({ checked: false })
    await Promise.resolve()
    expect(windows).toHaveLength(1)
  })

  it('carries the update switch and a check-now item when the entry hands them over (D768 (a))', async () => {
    const calls: string[] = []
    const { picker, menu } = pickerOver({
      updateMenu: {
        auto: false,
        setAuto: (on) => {
          calls.push(`setAuto:${String(on)}`)
          return on
        },
        checkNow: () => {
          calls.push('checkNow')
        },
      },
    })
    picker.installMenu()
    const items = appMenuItems(menu())

    const toggle = items.find((entry) => entry.label === UPDATE_AUTO_LABEL)
    expect(UPDATE_AUTO_LABEL).toBe('Check for Updates Automatically')
    // Electron's own checkbox, drawn from the store's value: the test above
    // proved a `checked` of `true` is not what this value is.
    expect(toggle?.type).toBe('checkbox')
    expect(toggle?.checked).toBe(false)
    // The click is Electron's: the item has already moved its own `checked` when
    // the handler runs, and it is that state -- not one this file computed --
    // that moves the switch.
    toggle?.click?.()
    expect(toggle?.checked).toBe(true)
    expect(calls).toEqual(['setAuto:true'])

    const now = items.find((entry) => entry.label === UPDATE_NOW_LABEL)
    expect(UPDATE_NOW_LABEL).toBe('Check for Updates Now')
    now?.click?.({ checked: false })
    expect(calls).toEqual(['setAuto:true', 'checkNow'])

    // ...and the update items are not frozen either.
    expect(frozenPaths(menu())).toEqual([])
  })

  it('redraws the checkbox from the value in force when the write behind a click fails (U4b, U4c)', () => {
    // The click is Electron's: the checkbox has moved its own `checked` to
    // `false` before the callback runs. A write that did not land leaves the
    // file saying `true`, so the menu is built again from what `setAuto`
    // answers -- and nothing thrown in the store reaches this process as an
    // uncaught error dialog. The redraw is the assertion: without it the menu
    // would still be the one the click left behind, and the operator would see
    // a switch this console is not using.
    const directory = freshDirectory()
    const controller: UpdateController = {
      start: vi.fn(async () => undefined),
      stop: vi.fn(),
      checkNow: vi.fn(async () => undefined),
      restartRequested: vi.fn(),
      launchFlowIdle: vi.fn(),
    }
    const settings = createUpdateSettings({
      directory,
      controller: () => controller,
      fs: { ...nodeUpdatesFs, writeFile: () => {
        throw new Error('ENOSPC: no space left on device')
      } },
    })
    const { picker, menu, redraws } = pickerOver({
      updateMenu: {
        auto: settings.auto,
        setAuto: (on) => settings.setAuto(on),
        checkNow: () => undefined,
      },
    })
    picker.installMenu()
    const drawn = redraws()
    expect(drawn).toBe(1)
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    let clicked: MenuItem | undefined
    try {
      clicked = appMenuItems(menu()).find((entry) => entry.label === UPDATE_AUTO_LABEL)
      expect(clicked?.checked).toBe(true)
      expect(() => clicked?.click?.()).not.toThrow()
      expect(logged.mock.calls).toEqual([[UPDATES_SWITCH_UNWRITTEN]])
    } finally {
      logged.mockRestore()
    }

    // The click moved the item it was given, and the menu the app is showing is
    // not that item any more: it is one built again, from the value in force.
    expect(clicked?.checked).toBe(false)
    expect(redraws()).toBe(drawn + 1)
    expect(readUpdateSwitch(directory)).toBe(true)
    expect(appMenuItems(menu()).find((entry) => entry.label === UPDATE_AUTO_LABEL)?.checked).toBe(true)

    // The switch did not move, so the controller is left exactly as it was: a
    // failed write is not a reason to stop a timer the file still asks for.
    expect(controller.start).not.toHaveBeenCalled()
    expect(controller.stop).not.toHaveBeenCalled()
  })

  it('draws the checkbox checked when the store says the switch is on', () => {
    const { picker, menu } = pickerOver({
      updateMenu: { auto: true, setAuto: () => true, checkNow: () => undefined },
    })
    picker.installMenu()
    expect(appMenuItems(menu()).find((entry) => entry.label === UPDATE_AUTO_LABEL)?.checked).toBe(true)
  })

  it('has neither update item when the entry hands over no controller', () => {
    // What `src/main/index.ts` passes on every platform that is not a packaged
    // macOS app: no `updateMenu` at all, because `startUpdates` answered null,
    // so the menu is exactly the one it was before U4.
    const { picker, menu } = pickerOver()
    picker.installMenu()
    const labels = appMenuItems(menu()).map((entry) => entry.label)
    expect(labels).toEqual([INSTALLATIONS_MENU_LABEL, undefined, undefined])
    expect(labels).not.toContain(UPDATE_AUTO_LABEL)
    expect(labels).not.toContain(UPDATE_NOW_LABEL)
  })

  it('carries the crash reporter’s checkbox, drawn from the store the click moves (run 2)', () => {
    // The switch is `crash.json`, so the item's `checked` is read from it rather
    // than handed in as a constant, and the click is Electron's own: the item has
    // already moved its `checked` when the handler runs, and that state -- not one
    // this file computed -- is what the store is asked to keep. On is the
    // direction that proves the write: it is the one that is not the file's
    // absent default.
    const directory = freshDirectory()
    const settings = createCrashSettings({ directory })
    const { picker, menu } = pickerOver({
      crashMenu: { send: settings.send, setSend: (on) => settings.setSend(on) },
    })
    picker.installMenu()
    const item = appMenuItems(menu()).find((entry) => entry.label === CRASH_SEND_ITEM_LABEL)
    // The label says what the item does **and** when the change takes effect: the
    // reporter is started once, at startup, so this click is a decision about the
    // next launch and the item says so rather than leaving an operator to find
    // out. There is no second, greyed-out line stating it.
    expect(CRASH_SEND_ITEM_LABEL).toBe('Send Crash Reports (applies at the next launch)')
    expect(item?.type).toBe('checkbox')
    expect(item?.checked).toBe(false)
    expect(readdirSync(directory)).toEqual([])

    item?.click?.()
    expect(item?.checked).toBe(true)
    expect(readCrashSwitch(directory)).toBe(true)
    expect(readdirSync(directory)).toEqual(['crash.json'])

    // ...and off again, which is the same code path in the other direction.
    appMenuItems(menu()).find((entry) => entry.label === CRASH_SEND_ITEM_LABEL)?.click?.()
    expect(readCrashSwitch(directory)).toBe(false)
  })

  it('draws the crash checkbox checked when the store says the switch is on', () => {
    const directory = freshDirectory()
    const settings = createCrashSettings({ directory })
    settings.setSend(true)
    const { picker, menu } = pickerOver({
      crashMenu: { send: settings.send, setSend: (on) => settings.setSend(on) },
    })
    picker.installMenu()
    expect(appMenuItems(menu()).find((entry) => entry.label === CRASH_SEND_ITEM_LABEL)?.checked).toBe(true)
  })

  it('reads the crash switch once, and hands the menu a value rather than the getter (run 2)', () => {
    // `crashSettings.send` is a getter over the file, and the template is not the
    // place for one: it is handed to Electron's `buildFromTemplate`, which reads
    // and sorts it in place and may read a property as often as it likes, so a
    // live getter in there would be an unknowable number of reads of a file
    // another process can move between them, and a checkbox drawn from a value
    // the file no longer holds. What is handed over instead is one boolean, read
    // once when the picker is built; every install after that draws the local the
    // store's own answers keep in step (which is what the redraw case above
    // reads).
    let reads = 0
    const { picker } = pickerOver({
      crashMenu: {
        get send(): boolean {
          reads += 1
          return false
        },
        setSend: () => false,
      },
    })
    picker.installMenu()
    picker.installMenu()
    expect(reads).toBe(1)
  })

  it('redraws the crash checkbox from the value in force when the write behind a click fails', () => {
    // The same rule U4b has, and for the same reason: the box has already moved
    // itself, a write that did not land leaves the file saying `true`, and the
    // operator must be shown the switch this console is actually using. Nothing
    // thrown in the store reaches the process either -- a throw out of a menu
    // click is Electron's uncaught-error dialog over a running app.
    const directory = freshDirectory()
    // The file says `true` before the failing writer is built -- unlike the
    // update switch, whose absent file already draws checked, this one's default
    // is off, so the value has to be in the file for the click to be moving it.
    createCrashSettings({ directory }).setSend(true)
    const settings = createCrashSettings({
      directory,
      fs: {
        ...nodeCrashFs,
        writeFile: () => {
          throw new Error('ENOSPC: no space left on device')
        },
      },
    })
    const { picker, menu, redraws } = pickerOver({
      crashMenu: { send: settings.send, setSend: (on) => settings.setSend(on) },
    })
    picker.installMenu()
    const drawn = redraws()
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    let clicked: MenuItem | undefined
    try {
      clicked = appMenuItems(menu()).find((entry) => entry.label === CRASH_SEND_ITEM_LABEL)
      expect(clicked?.checked).toBe(true)
      expect(() => clicked?.click?.()).not.toThrow()
      expect(logged.mock.calls).toEqual([[CRASH_SWITCH_UNWRITTEN]])
    } finally {
      logged.mockRestore()
    }
    expect(clicked?.checked).toBe(false)
    expect(redraws()).toBe(drawn + 1)
    expect(appMenuItems(menu()).find((entry) => entry.label === CRASH_SEND_ITEM_LABEL)?.checked).toBe(true)
    expect(readCrashSwitch(directory)).toBe(true)
  })

  it('keeps the crash checkbox in step across a later rebuild (run 3)', () => {
    // A rebuild redraws the whole menu, not only the item that asked for it. The
    // crash item is drawn from `drawnSend`, so a write that landed has to move
    // `drawnSend` even though it agreed with the click -- otherwise the next
    // rebuild, whoever caused it, shows the operator a crash switch this console
    // is not using.
    const directory = freshDirectory()
    const crash = createCrashSettings({ directory })
    const controller: UpdateController = {
      start: vi.fn(async () => undefined),
      stop: vi.fn(),
      checkNow: vi.fn(async () => undefined),
      restartRequested: vi.fn(),
      launchFlowIdle: vi.fn(),
    }
    // The update switch is the rebuild: its write fails, so its own checked and
    // the value in force disagree and the menu is built again.
    const updates = createUpdateSettings({
      directory,
      controller: () => controller,
      fs: {
        ...nodeUpdatesFs,
        writeFile: () => {
          throw new Error('ENOSPC: no space left on device')
        },
      },
    })
    const { picker, menu } = pickerOver({
      updateMenu: { auto: updates.auto, setAuto: (on) => updates.setAuto(on), checkNow: () => undefined },
      crashMenu: { send: crash.send, setSend: (on) => crash.setSend(on) },
    })
    picker.installMenu()
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      const crashItem = appMenuItems(menu()).find((entry) => entry.label === CRASH_SEND_ITEM_LABEL)
      expect(crashItem?.checked).toBe(false)
      crashItem?.click?.()
      expect(readCrashSwitch(directory)).toBe(true)

      const updateItem = appMenuItems(menu()).find((entry) => entry.label === UPDATE_AUTO_LABEL)
      expect(updateItem?.checked).toBe(true)
      updateItem?.click?.()
    } finally {
      logged.mockRestore()
    }

    // The rebuild the update item's failed write asked for draws the crash item
    // from the file's value, which the click above put there.
    expect(readCrashSwitch(directory)).toBe(true)
    expect(appMenuItems(menu()).find((entry) => entry.label === CRASH_SEND_ITEM_LABEL)?.checked).toBe(true)
  })

  it('has no crash item when the entry hands over none', () => {
    // What `src/main/index.ts` passes on Windows, where the owner's ruling of
    // 2026-10-30 does not put this item.
    const { picker, menu } = pickerOver()
    picker.installMenu()
    const labels = appMenuItems(menu()).map((entry) => entry.label)
    expect(labels).not.toContain(CRASH_SEND_ITEM_LABEL)
    expect(labels.filter((label) => label !== undefined)).toEqual([INSTALLATIONS_MENU_LABEL])
  })
})

describe('a store the path rule refuses', () => {
  it('starts with no store and says why, rather than quitting the console (review S7)', async () => {
    // `$FUGARO_CONFIG` at `~/fugaro.yaml` makes the home directory the CLI's
    // config directory, and every `userData` is under the home directory.
    const home = freshDirectory()
    const userData = join(home, 'Library', 'Application Support', 'fugaro-console')
    let built: ReturnType<typeof pickerOver> | undefined
    expect(() => {
      built = pickerOver({ userData, home, prefixes: [], appEnv: { FUGARO_CONFIG: join(home, 'fugaro.yaml') } })
    }).not.toThrow()
    if (built === undefined) return
    const store = built
    expect(store.picker.refusal).toContain('refuses to keep its installations')
    const current = store.picker.installation()
    expect(current.chosen).toBe(false)
    if (!current.chosen) {
      expect(current.refusal).toContain('refuses to keep its installations')
      expect(current.path).toBe(join(userData, INSTALLATIONS_FILE_NAME))
    }
    const list = await answer(store.ipcMain, 'list')
    expect(list.ok && (list.data as { refusal?: string }).refusal).toContain('refuses to keep its installations')
    expect(existsSync(join(userData, INSTALLATIONS_FILE_NAME))).toBe(false)
  })
})

describe('the picker’s document', () => {
  // Read from this file's own location rather than from the working directory,
  // the way `window.test.ts` reads `index.html`.
  const documentPath = fileURLToPath(new URL('../../src/renderer/installations.html', import.meta.url))
  const html = readFileSync(documentPath, 'utf8')

  // The design's own CSP string, section 5.1 rule 3, copied by hand rather than
  // assembled from the constant it is compared against: a test that built its
  // expectation out of the implementation would agree with any implementation.
  const DESIGN_CSP =
    "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'"

  it('carries the header’s CSP byte for byte, with no delta of its own', () => {
    // The picker is a second page of the same bundle, not a second policy: it is
    // served by the same `app://` handler, so it gets the same header, and the
    // meta copy is what covers the window between parsing and the header.
    expect(CONTENT_SECURITY_POLICY).toBe(DESIGN_CSP)
    expect(html).toContain(`content="${CONTENT_SECURITY_POLICY}"`)
    // The mutation this catches, held as a value: a document that loosened the
    // policy for itself -- an inline script, a remote script -- would fail the
    // assertion above, and this is what that looks like.
    const loosened = html.replace("script-src 'self'", "script-src 'self' 'unsafe-inline' https://cdn.example")
    expect(loosened.includes(`content="${CONTENT_SECURITY_POLICY}"`)).toBe(false)
  })

  it('loads exactly one script, as a file from its own origin, deferred, and nothing inline', () => {
    // The extraction takes the opening tag of every `<script>`, so a second
    // script, a remote one, or an inline one is a second element here.
    const tags = [...html.matchAll(/<script\b[^>]*>/g)].map((match) => match[0])
    expect(tags).toEqual(['<script src="./installations.js" defer>'])
    // Non-vacuity, against the same extraction: an inline script is a tag this
    // assertion does not accept, so the check is not merely "there is a tag".
    expect([...('<script>alert(1)</script>'.matchAll(/<script\b[^>]*>/g))].map((match) => match[0])).toEqual([
      '<script>',
    ])
  })

  it('embeds nothing remote, and the website is never framed or fetched', () => {
    // The two onboarding screens *link* to the website through the main
    // process's external-link path, and the document holds no part of it: no
    // image, no font, no frame, no URL at all. `connect-src 'none'` above is the
    // other half of the same rule.
    for (const remote of ['http://', 'https://', '<img', '<iframe', '<object', '<link']) {
      if (remote === '<link') continue
      expect(html, `installations.html must not carry ${remote}`).not.toContain(remote)
    }
    // The one `<link>` is the stylesheet of this same origin, and it is the only
    // one.
    expect([...html.matchAll(/<link\b[^>]*>/g)].map((match) => match[0])).toEqual([
      '<link rel="stylesheet" href="./console.css" />',
    ])
  })
})
