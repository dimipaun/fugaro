// The picker's surface, held to the two rules that make it a surface rather
// than a bridge (desktop-console plan Task 3.4b; design sections 3.4 rules 5 and
// 7, 5.1 rule 10, D13, D14; owner ruling C9).
//
// The two rules, and the mutation each test catches:
//
//   1. **a renderer never names a path.** `add` sends pick *tokens*, and the
//      token check is by shape before it is by redemption -- so an `add` that
//      accepted a literal path (the plan's red step) fails here, and a token
//      that is redeemed twice fails too;
//   2. **a frame reaches one document's surface and no other.** The dashboard
//      and the picker are served from the same origin, so an origin-only gate
//      lets either reach both; the tests below drive a dashboard-sender event
//      into `fugaro:installations/*` and a picker-sender event into
//      `fugaro:console/*` and require the fixed refusal both ways.
//
// The preload's copy of the table is compared with the main process's, because
// the two are duplicated on purpose (the sandboxed preload cannot import a
// module that reads `node:fs`) and a duplicate nobody compares is a duplicate
// that drifts.

import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it, vi } from 'vitest'

import {
  INSTALLATION_CHANNEL_PREFIX,
  INSTALLATION_OPERATIONS,
  MAX_OUTSTANDING_PICKS,
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
import { INSTALLATIONS_MENU_LABEL, PICKER_ENTRY_URL, pickerOptions, pickerPreloadPath } from '../../src/main/picker'
import { consoleWebPreferences } from '../../src/main/window'
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
import { CLI_VERSION_BUILT_AGAINST } from '../../src/shared/version'
import { installStub } from './stub'

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
  // last one would hide the picker's (review: the token-drop mutation survived).
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
      // The operator closes the window while the document loads: Electron
      // destroys it and rejects the load with ERR_ABORTED.
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

/** pickerOver builds a picker over fakes, with one userData directory of its
 * own. It returns the picker and the listeners it registered. */
function pickerOver(
  options: {
    readonly userData?: string
    readonly loadFails?: 'closed' | 'closing' | 'error'
    readonly appEnv?: Readonly<Record<string, string | undefined>>
    readonly home?: string
    readonly onChosen?: Parameters<typeof createInstallationPicker>[0]['onChosen']
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
  const picker = createInstallationPicker({
    BrowserWindow: BrowserWindow as never,
    session: {
      setPermissionRequestHandler: () => undefined,
      setPermissionCheckHandler: () => undefined,
    } as never,
    ipcMain,
    dialog: {
      showOpenDialog: async () => ({ canceled: false, filePaths: ['/usr/local/bin/fugaro'] }),
    },
    menu: {
      buildFromTemplate: (template) => template,
      setApplicationMenu: (value) => {
        applicationMenu = value
      },
    },
    userData: options.userData ?? freshDirectory(),
    home: options.home ?? '/Users/owner',
    appPath: '/work/repo/console',
    appEnv: options.appEnv ?? {},
    ...(options.onChosen === undefined ? {} : { onChosen: options.onChosen }),
  })
  return { picker, ipcMain, windows, menu: () => applicationMenu }
}

/** dashboardEvent and pickerEvent are forged events from each document, with
 * the frame the sender gate reads. */
function eventFor(url: string): ConsoleEventLike {
  const frame = { url }
  return { senderFrame: frame, sender: { mainFrame: frame } }
}

// ---------------------------------------------------------------------------
// The table, and its copy in the preload
// ---------------------------------------------------------------------------

describe('the picker’s allow-list', () => {
  it('is closed, frozen, and eight rows wide', () => {
    expect(INSTALLATION_OPERATIONS.map((row) => row.name)).toEqual([
      'list',
      'current',
      'pickBinary',
      'pickConfig',
      'pickCheckout',
      'add',
      'choose',
      'remove',
    ])
    expect(Object.isFrozen(INSTALLATION_OPERATIONS)).toBe(true)
    for (const row of INSTALLATION_OPERATIONS) expect(Object.isFrozen(row)).toBe(true)
    expect(INSTALLATION_CHANNEL_PREFIX).toBe('fugaro:installations/')
    expect(installationChannel('remove')).toBe('fugaro:installations/remove')
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
    expect(keys).toEqual(['add', 'choose', 'current', 'list', 'pickBinary', 'pickConfig', 'pickCheckout', 'remove', 'operations', 'version'].sort())
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
    const { picker, ipcMain } = pickerOver()
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
// The tokens, through the surface
// ---------------------------------------------------------------------------

describe('the picker’s add', () => {
  it('refuses a literal path and accepts an issued token, once', async () => {
    const { picker, ipcMain } = pickerOver()
    void picker
    const add = ipcMain.listeners.get(installationChannel('add'))
    const pick = ipcMain.listeners.get(installationChannel('pickBinary'))
    expect(add).toBeDefined()
    expect(pick).toBeDefined()

    // The plan's red step: a literal path is refused, and the value is a
    // perfectly good absolute path -- the refusal is about *provenance*.
    const literal = (await add?.(eventFor(PICKER_ENTRY_URL), { project: 'fugaro', binary: '/usr/local/bin/fugaro' })) as ConsoleReply
    expect(literal).toEqual({ ok: false, reason: 'binary is not a path this console offered' })

    const picked = (await pick?.(eventFor(PICKER_ENTRY_URL), {})) as ConsoleReply
    expect(picked.ok).toBe(true)
    if (!picked.ok) return
    const token = (picked.data as { token: string }).token
    expect(token).toMatch(/^[0-9a-f]{32}$/)

    // The stub at `/usr/local/bin/fugaro` is not executable in the test
    // container, so the add stops at the binary check -- which is the point
    // being made here: the token *was* redeemed, and what refused is the
    // validation and not the provenance.
    const redeemed = (await add?.(eventFor(PICKER_ENTRY_URL), { project: 'fugaro', binary: token })) as ConsoleReply
    expect(redeemed.ok).toBe(false)
    if (!redeemed.ok) expect(redeemed.reason).not.toContain('not a path this console offered')

    // Single use: the same token again is refused *by provenance*, because the
    // first redemption consumed it.
    const replay = (await add?.(eventFor(PICKER_ENTRY_URL), { project: 'fugaro', binary: token })) as ConsoleReply
    expect(replay).toEqual({ ok: false, reason: 'binary is not an outstanding choice' })
  })

  it('bounds the outstanding tokens at eight', async () => {
    const { picker, ipcMain } = pickerOver()
    void picker
    const pick = ipcMain.listeners.get(installationChannel('pickBinary'))
    for (let index = 0; index < MAX_OUTSTANDING_PICKS + 3; index += 1) {
      const reply = (await pick?.(eventFor(PICKER_ENTRY_URL), {})) as ConsoleReply
      expect(reply.ok).toBe(true)
    }
    // The register is bounded, which is a property of `createPickTokens` and is
    // asserted there; here the point is that the *surface* cannot grow it past
    // the bound by being called in a loop.
    expect(MAX_OUTSTANDING_PICKS).toBe(8)
  })

  it('answers the no-installation state before anything is chosen', async () => {
    const { picker, ipcMain } = pickerOver()
    expect(picker.installation()).toEqual({ chosen: false, sentence: NO_ACTIVE_INSTALLATION })
    expect(picker.installation()).toEqual({ chosen: false, sentence: 'no installation has been chosen' })
    const current = ipcMain.listeners.get(installationChannel('current'))
    const reply = (await current?.(eventFor(PICKER_ENTRY_URL), {})) as ConsoleReply
    expect(reply).toEqual({ ok: true, data: { chosen: false, sentence: NO_ACTIVE_INSTALLATION } })
  })

  it('refuses a choose for an installation that is not there', async () => {
    const { picker, ipcMain } = pickerOver()
    void picker
    const choose = ipcMain.listeners.get(installationChannel('choose'))
    const reply = (await choose?.(eventFor(PICKER_ENTRY_URL), { id: '0123456789abcdef' })) as ConsoleReply
    expect(reply).toEqual({ ok: false, reason: 'no such installation' })
  })

  it('opens its own document, once, on the first run', async () => {
    const { picker, windows } = pickerOver()
    await picker.openOnFirstRun()
    await picker.openOnFirstRun()
    expect(windows).toHaveLength(1)
    // The picker's own document, on the console's own origin, and the preload
    // is the picker's -- a window that got the dashboard's preload would expose
    // fourteen CLI reads to a document whose whole job is to write a file.
    expect(windows[0]?.loaded).toEqual([PICKER_ENTRY_URL])
    expect(PICKER_ENTRY_URL).toBe('app://console/installations.html')
    // A second `open()` focuses rather than building a second window.
    await picker.open()
    expect(windows).toHaveLength(1)
  })

  it('is built through the same hardened path as the dashboard (buildHardenedWindow)', async () => {
    const { picker, windows } = pickerOver()
    await picker.open()
    const built = windows[0]
    expect(built, 'no picker window was built').toBeDefined()
    // The three webContents denials, the title refusal and the show-when-ready
    // are what `buildHardenedWindow` installs on every window this app opens;
    // a picker built by hand beside it would be the one window without them.
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
    // The first run awaits `openOnFirstRun()` inside `main`, whose catch quits
    // the console: a load aborted because the operator closed the picker must
    // not reach it. Any other load failure still does.
    const dismissed = pickerOver({ loadFails: 'closed' })
    await expect(dismissed.picker.openOnFirstRun()).resolves.toBeUndefined()
    expect(dismissed.windows).toHaveLength(1)
    const closing = pickerOver({ loadFails: 'closing' })
    await expect(closing.picker.openOnFirstRun()).resolves.toBeUndefined()
    const broken = pickerOver({ loadFails: 'error' })
    await expect(broken.picker.open()).rejects.toThrow(/ERR_FAILED/)
  })

  it('is opened from the menu item and from nowhere else', async () => {
    const { picker, windows, menu } = pickerOver()
    picker.installMenu()
    const installed = menu() as readonly { readonly label?: string; readonly role?: string; readonly submenu?: readonly unknown[] }[]
    // Ours, then Electron's own Edit and Window menus (review S10): without
    // them macOS loses copy/paste/select-all in the picker's field and
    // close/minimise on every window.
    expect(installed).toHaveLength(3)
    expect(installed.slice(1).map((entry) => entry.role)).toEqual(['editMenu', 'windowMenu'])
    const items = installed[0]?.submenu as readonly { readonly label?: string; readonly click?: () => void }[]
    const item = items.find((entry) => entry.label === INSTALLATIONS_MENU_LABEL)
    expect(item, INSTALLATIONS_MENU_LABEL).toBeDefined()
    expect(INSTALLATIONS_MENU_LABEL).toBe('Installations…')
    // Electron's `buildFromTemplate` writes into the template (it sorts it and
    // assigns `submenu`), so a frozen one throws at startup: nothing in the
    // value handed to it may be frozen, at any depth.
    const frozen: string[] = []
    const walk = (value: unknown, at: string): void => {
      if (typeof value !== 'object' || value === null) return
      if (Object.isFrozen(value)) frozen.push(at)
      for (const [key, child] of Object.entries(value)) walk(child, `${at}.${key}`)
    }
    walk(installed, 'template')
    expect(frozen).toEqual([])
    expect(windows).toHaveLength(0)
    item?.click?.()
    await Promise.resolve()
    expect(windows).toHaveLength(1)
  })

  it('drops every outstanding token when the picker closes', async () => {
    const { picker, ipcMain, windows } = pickerOver()
    await picker.open()
    // A **binary** token, redeemed as a binary: the first version of this test
    // picked a config and redeemed it as a binary, which the kind check refuses
    // on its own -- so removing the drop survived it (review S12).
    const pick = ipcMain.listeners.get(installationChannel('pickBinary'))
    const add = ipcMain.listeners.get(installationChannel('add'))
    const issue = async (): Promise<string> => {
      const picked = (await pick?.(eventFor(PICKER_ENTRY_URL), {})) as ConsoleReply
      expect(picked.ok).toBe(true)
      return picked.ok ? (picked.data as { token: string }).token : ''
    }
    // The control: with the window open, a binary token is redeemed (and what
    // refuses is the validation of the path, not its provenance).
    const open = (await add?.(eventFor(PICKER_ENTRY_URL), { project: 'fugaro', binary: await issue() })) as ConsoleReply
    expect(open.ok).toBe(false)
    if (!open.ok) expect(open.reason).not.toMatch(/outstanding choice|path this console offered/)
    const token = await issue()
    // The window closes -- the operator dismissed it -- and the token it was
    // holding is dropped with it.
    windows[0]?.fire('closed')
    const reply = (await add?.(eventFor(PICKER_ENTRY_URL), { project: 'fugaro', binary: token })) as ConsoleReply
    expect(reply).toEqual({ ok: false, reason: 'binary is not an outstanding choice' })
    picker.close()
  })
})

// ---------------------------------------------------------------------------
// Choose, against stub binaries
// ---------------------------------------------------------------------------

/** seededPicker is a picker over a userData holding `installations.json` with
 * one installation per stub version, the first one active. */
function seededPicker(
  versions: readonly string[],
  onChosen?: Parameters<typeof createInstallationPicker>[0]['onChosen'],
) {
  const root = freshDirectory()
  const userData = join(root, 'user-data')
  mkdirSync(userData, { recursive: true, mode: 0o700 })
  const entries = versions.map((version, index) => {
    const stub = installStub(join(root, `bin-${String(index)}`), {
      commands: { version: { stdout: `${version}\n` }, doctor: { stdout: '{}\n' } },
    })
    return { id: `${String(index).padStart(2, '0')}23456789abcdef`, project: `p${String(index)}`, binaryPath: stub.binary }
  })
  writeFileSync(
    join(userData, INSTALLATIONS_FILE_NAME),
    serializeInstallationFile({ version: 1, active: entries[0]?.id ?? null, installations: entries }),
    { mode: 0o600 },
  )
  return { ...pickerOver({ userData, ...(onChosen === undefined ? {} : { onChosen }) }), entries }
}

describe('the picker’s choose', () => {
  it('does not choose a refused CLI: the active one stays, and nothing is handed to the window (review S11)', async () => {
    const chosen: string[] = []
    const { picker, ipcMain, entries } = seededPicker([CLI_VERSION_BUILT_AGAINST, '0.5.0'], (installation) => {
      chosen.push(installation.id)
    })
    const choose = ipcMain.listeners.get(installationChannel('choose'))
    const refusedId = entries[1]?.id ?? ''
    const reply = (await choose?.(eventFor(PICKER_ENTRY_URL), { id: refusedId })) as ConsoleReply
    expect(reply.ok).toBe(false)
    if (!reply.ok) expect(reply.reason).toContain('0.5.0')
    // Not chosen: the active installation is the one it was, on disk and in the
    // dashboard's read, and the lifecycle was never asked for a window.
    const current = picker.installation()
    expect(current.chosen && current.id).toBe(entries[0]?.id)
    expect(chosen).toEqual([])
    // And an ok one is chosen, which is what makes the refusal above a decision
    // rather than a choose that never works.
    const ok = (await choose?.(eventFor(PICKER_ENTRY_URL), { id: entries[0]?.id ?? '' })) as ConsoleReply
    expect(ok.ok).toBe(true)
    expect(chosen).toEqual([entries[0]?.id])
  })

  it('runs one mutation at a time: a second choose waits for the first one’s window (review S6)', async () => {
    let release: () => void = () => undefined
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    const handed: string[] = []
    const { picker, ipcMain, entries } = seededPicker([CLI_VERSION_BUILT_AGAINST, CLI_VERSION_BUILT_AGAINST], async (installation) => {
      handed.push(installation.id)
      // The first choose's window is still opening.
      if (handed.length === 1) await held
    })
    const choose = ipcMain.listeners.get(installationChannel('choose'))
    const first = choose?.(eventFor(PICKER_ENTRY_URL), { id: entries[0]?.id ?? '' })
    const second = choose?.(eventFor(PICKER_ENTRY_URL), { id: entries[1]?.id ?? '' })
    // The first choose reaches its window; then, long enough for the second
    // probe to have finished were it allowed to run, the second has not.
    await vi.waitFor(() => {
      expect(handed).toHaveLength(1)
    }, { timeout: 10_000 })
    await new Promise((resolve) => setTimeout(resolve, 1500))
    expect(handed).toEqual([entries[0]?.id])
    release()
    await Promise.all([first, second])
    expect(handed).toEqual([entries[0]?.id, entries[1]?.id])
    const current = picker.installation()
    expect(current.chosen && current.id).toBe(entries[1]?.id)
  }, 20_000)
})

describe('a store the path rule refuses', () => {
  it('starts with no store and says why, rather than quitting the console (review S7)', async () => {
    // `$FUGARO_CONFIG` at `~/fugaro.yaml` makes the home directory the CLI's
    // config directory, and every `userData` is under the home directory.
    const home = freshDirectory()
    const userData = join(home, 'Library', 'Application Support', 'fugaro-console')
    let built: ReturnType<typeof pickerOver> | undefined
    expect(() => {
      built = pickerOver({ userData, home, appEnv: { FUGARO_CONFIG: join(home, 'fugaro.yaml') } })
    }).not.toThrow()
    if (built === undefined) return
    expect(built.picker.refusal).toContain('refuses to keep its installations')
    const current = built.picker.installation()
    expect(current.chosen).toBe(false)
    if (!current.chosen) {
      expect(current.refusal).toContain('refuses to keep its installations')
      expect(current.path).toBe(join(userData, INSTALLATIONS_FILE_NAME))
    }
    const list = (await built.ipcMain.listeners.get(installationChannel('list'))?.(eventFor(PICKER_ENTRY_URL), {})) as ConsoleReply
    expect(list.ok && (list.data as { refusal?: string }).refusal).toContain('refuses to keep its installations')
    expect(existsSync(join(userData, INSTALLATIONS_FILE_NAME))).toBe(false)
  })
})

describe('the picker’s document', () => {
  // Read from this file's own location rather than from the working directory,
  // the way `window.test.ts` reads `index.html`: the served root is the source
  // tree today (`rendererRoot` in `src/main/index.ts`) and becomes the
  // bundler's output directory when the renderer entry lands, and either way
  // this is the document as written.
  const documentPath = fileURLToPath(new URL('../../src/renderer/installations.html', import.meta.url))
  const html = readFileSync(documentPath, 'utf8')

  // The design's own CSP string, section 5.1 rule 3, copied by hand rather than
  // assembled from the constant it is compared against: a test that built its
  // expectation out of the implementation would agree with any implementation.
  const DESIGN_CSP =
    "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'"

  it('carries the header’s CSP byte for byte, with no delta of its own', () => {
    // The picker is a second page of the same bundle, not a second policy: it
    // is served by the same `app://` handler, so it gets the same header, and
    // the meta copy is what covers the window between parsing and the header.
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
})
