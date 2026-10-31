// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The update client, against the REAL electron-updater (desktop-console plan
// Task 4.4, "Auto-update -- SECURITY-SENSITIVE"; design section 7.4 for "no
// update check that carries an identifier", section 5.1 rule 10 for the two
// files the app may write).
//
// U1's review of 2026-10-30 found that electron-updater 6.8.10 sends and stores
// an identifier of its own: `AppUpdater.getUpdateInfoAndProvider` (~380-392)
// calls `client.setRequestHeaders(this.computeFinalHeaders({ "x-user-staging-id":
// stagingUserId }))` on every feed request, and `stagingUserIdPromise` (~189) is
// a `Lazy` over `getOrCreateStagingUserId` (~500-525), which mints a random UUID
// and writes it to `<userData>/.updaterId`. `computeFinalHeaders` (~332-338)
// merges `requestHeaders` *on top*, so a policy that sets `requestHeaders = {}`
// cannot remove the header. A fake updater cannot see any of that, which is why
// the first suite here builds the real class.
//
// The suite that holds the hardening is written both ways round on purpose: one
// test asserts the hardened client sends nothing, and the one beside it asserts
// the *same* client, unhardened, does send the identifier and does write the
// file. Without the second, the first would go on passing if the library ever
// stopped sending the header at all -- it would be a test of nothing. The two
// suites after it hold the other two things the hardening rewrites to the same
// standard, on the same real class: the download cache the bundle's missing
// `app-update.yml` would have named, and the install-on-quit flag the owner's
// ruling keeps off.

import { existsSync, mkdtempSync, readdirSync, rmSync } from 'node:fs'
import { createRequire } from 'node:module'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { MacUpdater } from 'electron-updater'

import { createLaunchRunner } from '../../src/main/launch'
import type { ConfirmView, LaunchConfirmer, TargetLookup } from '../../src/main/launch'
import { MAX_LISTING_BYTES, RELEASES_URL } from '../../src/main/updates'
import type { IntervalTimers, UpdateController, UpdaterPort } from '../../src/main/updates'
import { STAGING_USER_ID, UPDATER_CACHE_DIR_NAME, hardenUpdater, startUpdates } from '../../src/main/updater-electron'
import type { ListingBody, ListingFetcher, ListingReader, NoticeDialog } from '../../src/main/updater-electron'

const STAGING_HEADER = 'x-user-staging-id'
const CHOSEN_TAG = 'console-v0.120.0'
const CHOSEN_VERSION = '0.120.0'
const RUNNING_VERSION = '0.116.0'

/** electronFake is the Electron electron-updater's macOS client reaches: it
 * takes Squirrel's `autoUpdater` in its constructor. The three empty objects are
 * the members the adapter would use on a real Mac; every test that reaches the
 * adapter hands over its own, so nothing here is ever called. */
const electronFake = vi.hoisted(() => {
  const native = { on: () => native }
  return { autoUpdater: native, app: {}, net: {}, dialog: {} }
})

vi.mock('electron', () => electronFake)

/** installElectronForTheLibrary puts that fake in front of electron-updater's
 * own `require('electron')`. The library is a CommonJS package vitest loads as
 * an external dependency, so `vi.mock` -- which serves the console's own inlined
 * sources -- never sees the require inside `MacUpdater`'s constructor, and
 * without this the real `electron` package's Node shim is what answers.
 *
 * It has to run before the first updater is constructed, not before the import:
 * electron-updater reaches Electron from its constructors and from its lazy
 * `autoUpdater` getter, never at module scope. */
function installElectronForTheLibrary(): void {
  const nodeRequire = createRequire(import.meta.url)
  const path = nodeRequire.resolve('electron')
  ;(nodeRequire.cache as unknown as Record<string, unknown>)[path] = {
    id: path,
    filename: path,
    loaded: true,
    exports: electronFake,
    children: [],
    paths: [],
  }
}

installElectronForTheLibrary()

/** userData is the temporary user-data directory the real client would write
 * `.updaterId` into: a real directory, so "the app writes no other file" is a
 * question about a real filesystem rather than about a spy. */
function userData(): string {
  return mkdtempSync(join(tmpdir(), 'fugaro-updater-'))
}

/** appAdapter is the `AppAdapter` shape electron-updater accepts in place of
 * Electron's own app: only `userDataPath` and `version` matter to this path, and
 * the rest is here so the object is the shape the library reads. */
function appAdapter(directory: string): ConstructorParameters<typeof MacUpdater>[1] {
  return {
    version: RUNNING_VERSION,
    name: 'Fugaro Console',
    isPackaged: true,
    appUpdateConfigPath: join(directory, 'app-update.yml'),
    userDataPath: directory,
    baseCachePath: join(directory, 'cache'),
    whenReady: () => Promise.resolve(),
    relaunch: () => undefined,
    quit: () => undefined,
    onQuit: () => undefined,
  }
}

/** downloadHelper is the shortest path to the directory the client caches a
 * download in: `getOrCreateDownloadHelper` is `private` in the package's
 * `.d.ts` and an ordinary method of the class all the same. */
function downloadHelper(updater: MacUpdater): Promise<{ readonly cacheDir: string }> {
  const internals = updater as unknown as { getOrCreateDownloadHelper(): Promise<{ readonly cacheDir: string }> }
  return internals.getOrCreateDownloadHelper()
}

/** recordingClient is the provider the updater is pointed at: it records the
 * headers the library hands it, which is the whole of what this suite reads. */
function recordingClient(): {
  readonly headers: readonly Readonly<Record<string, string>>[]
  readonly client: { setRequestHeaders(headers: Record<string, string>): void; getLatestVersion(): Promise<unknown> }
} {
  const headers: Record<string, string>[] = []
  return {
    headers,
    client: {
      setRequestHeaders(next: Record<string, string>): void {
        headers.push({ ...next })
      },
      getLatestVersion: () => Promise.resolve({ version: '0.120.0', files: [], path: '', sha512: '', releaseDate: '' }),
    },
  }
}

/** reachUpdateInfo is the path `checkForUpdates` itself takes: with a provider
 * already in place, `getUpdateInfoAndProvider` is what asks the client for its
 * latest version -- and what sets the headers on the way. */
async function reachUpdateInfo(updater: MacUpdater, client: unknown): Promise<void> {
  const internals = updater as unknown as { clientPromise: unknown; getUpdateInfoAndProvider(): Promise<unknown> }
  internals.clientPromise = Promise.resolve(client)
  await internals.getUpdateInfoAndProvider()
}

const UUID_RE = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i

/** NO_CONFIG is `MacUpdater`'s first argument with no `app-update.yml` behind
 * it: this suite points the client at a provider by hand, so nothing ever reads
 * the file. The cast is the constructor's own `AllPublishOptions | undefined`,
 * which does not accept `null` even though the library's runtime does. */
const NO_CONFIG = null as unknown as ConstructorParameters<typeof MacUpdater>[0]

let directory = ''

beforeEach(() => {
  directory = userData()
})

afterEach(() => {
  rmSync(directory, { recursive: true, force: true })
  vi.restoreAllMocks()
})

describe('the identifier electron-updater sends and stores', () => {
  it('sends no staging id and writes no .updaterId, on the real client', async () => {
    const updater = new MacUpdater(NO_CONFIG, appAdapter(directory))
    hardenUpdater(updater as unknown as UpdaterPort)
    const { headers, client } = recordingClient()

    await reachUpdateInfo(updater, client)

    expect(headers).toHaveLength(1)
    const sent = headers[0] ?? {}
    expect(Object.keys(sent)).not.toContain(STAGING_HEADER)
    const values = Object.values(sent).join(' ').split(STAGING_USER_ID).join('')
    expect(values, `a UUID reached the feed request: ${values}`).not.toMatch(UUID_RE)

    expect(readdirSync(directory)).not.toContain('.updaterId')
  })

  it('sends the identifier and writes the file without the hardening, on the same client', async () => {
    // The red step, held as a test. This is what the assertion above is worth:
    // if 6.8.10 ever stopped sending `x-user-staging-id` the hardened test would
    // pass for a reason that has nothing to do with this app, and this one would
    // be the failure that says so.
    const updater = new MacUpdater(NO_CONFIG, appAdapter(directory))
    const { headers, client } = recordingClient()

    await reachUpdateInfo(updater, client)

    expect(Object.keys(headers[0] ?? {})).toContain(STAGING_HEADER)
    expect(readdirSync(directory)).toContain('.updaterId')
  })
})

/** LazyLike is the shape `hardenUpdater` requires of `stagingUserIdPromise`,
 * which is `lazy-val`'s `Lazy`: a `value` accessor on the prototype. The fakes
 * below are built with one so that the fail-closed check passes for the reason
 * it passes on the real client, and the counter is how a test sees whether the
 * library's own creator -- the one that mints the UUID and writes the file --
 * was ever reached. */
class LazyLike {
  private held: Promise<unknown> | null = null
  private creator: (() => Promise<unknown>) | null

  constructor(creator: () => Promise<unknown>) {
    this.creator = creator
  }

  get value(): Promise<unknown> {
    if (this.creator === null) return this.held as Promise<unknown>
    const made = this.creator()
    this.value = made
    return made
  }

  set value(next: Promise<unknown>) {
    this.held = next
    this.creator = null
  }
}

/** UpdaterRecord is the fake client and everything a test reads off it. */
interface UpdaterRecord {
  readonly updater: UpdaterPort
  readonly feedUrls: readonly string[]
  readonly checks: number
  readonly quits: number
  readonly events: readonly string[]
  readonly creatorCalls: number
  readonly headers: Readonly<Record<string, string>>
  readonly autoDownload: boolean
  readonly autoInstallOnAppQuit: boolean
  readonly allowDowngrade: boolean
  readonly allowPrerelease: boolean
  emit(event: 'update-downloaded' | 'error', payload?: unknown): void
}

/** updaterFake is electron-updater as the adapter sees it: the members the
 * policy writes, and the two `hardenUpdater` reaches for. The creator behind
 * `stagingUserIdPromise` is counted and never called. */
function updaterFake(): UpdaterRecord {
  const feedUrls: string[] = []
  const listeners = new Map<string, ((...args: unknown[]) => void)[]>()
  const events: string[] = []
  let checks = 0
  let quits = 0
  let creatorCalls = 0
  const updater = {
    autoDownload: false,
    autoInstallOnAppQuit: false,
    allowDowngrade: true,
    allowPrerelease: true,
    requestHeaders: { 'X-Machine-Id': 'machine-1' } as Record<string, string>,
    stagingUserIdPromise: new LazyLike(() => {
      creatorCalls += 1
      return Promise.resolve('11111111-1111-1111-1111-111111111111')
    }),
    // The second `Lazy` `hardenUpdater` rewrites, and a `Lazy` here for the
    // reason the real one is: a fake without it is a client the hardening is
    // supposed to refuse.
    configOnDisk: new LazyLike(() => Promise.resolve({ updaterCacheDirName: 'from-the-bundle' })),
    computeFinalHeaders(headers: Record<string, string>): Record<string, string> {
      return Object.assign(headers, this.requestHeaders)
    },
    setFeedURL(options: { provider: 'generic'; url: string }): void {
      feedUrls.push(options.url)
    },
    checkForUpdates(): Promise<unknown> {
      checks += 1
      return Promise.resolve(null)
    },
    quitAndInstall(): void {
      quits += 1
    },
    on(event: string, listener: (...args: unknown[]) => void): unknown {
      events.push(event)
      const existing = listeners.get(event) ?? []
      existing.push(listener)
      listeners.set(event, existing)
      return updater
    },
    emit(event: 'update-downloaded' | 'error', payload?: unknown): void {
      for (const listener of listeners.get(event) ?? []) listener(payload)
    },
  }
  return {
    updater: updater as unknown as UpdaterPort,
    emit: updater.emit,
    get feedUrls() {
      return feedUrls
    },
    get checks() {
      return checks
    },
    get quits() {
      return quits
    },
    get events() {
      return events
    },
    get creatorCalls() {
      return creatorCalls
    },
    get headers() {
      return updater.requestHeaders
    },
    get autoDownload() {
      return updater.autoDownload
    },
    get autoInstallOnAppQuit() {
      return updater.autoInstallOnAppQuit
    },
    get allowDowngrade() {
      return updater.allowDowngrade
    },
    get allowPrerelease() {
      return updater.allowPrerelease
    },
  } as UpdaterRecord
}

describe('the hardening refuses a client it does not recognise', () => {
  it('refuses a client with no stagingUserIdPromise at all', () => {
    const { updater } = updaterFake()
    delete (updater as unknown as Record<string, unknown>)['stagingUserIdPromise']
    expect(() => hardenUpdater(updater)).toThrow(/6\.8\.10/)
  })

  it('refuses a stagingUserIdPromise that is not a Lazy', () => {
    // A plain promise is the shape a future version would most plausibly leave
    // behind, and it is exactly the shape this hardening cannot disarm: there
    // would be no `value` to set, so the library's own creator would run.
    const { updater } = updaterFake()
    ;(updater as unknown as Record<string, unknown>)['stagingUserIdPromise'] = Promise.resolve('x')
    expect(() => hardenUpdater(updater)).toThrow(/6\.8\.10/)
  })

  it('refuses a client whose computeFinalHeaders is gone', () => {
    const { updater } = updaterFake()
    delete (updater as unknown as Record<string, unknown>)['computeFinalHeaders']
    expect(() => hardenUpdater(updater)).toThrow(/6\.8\.10/)
  })

  it('refuses a client with no configOnDisk at all', () => {
    // The member is where the download cache directory name would come from, and
    // the file it would come from is one this bundle does not ship: a client
    // without it is a client whose every download fails at the last step.
    const { updater } = updaterFake()
    delete (updater as unknown as Record<string, unknown>)['configOnDisk']
    expect(() => hardenUpdater(updater)).toThrow(/6\.8\.10/)
  })

  it('refuses a configOnDisk that is not a Lazy', () => {
    const { updater } = updaterFake()
    ;(updater as unknown as Record<string, unknown>)['configOnDisk'] = Promise.resolve({})
    expect(() => hardenUpdater(updater)).toThrow(/6\.8\.10/)
  })

  it('never reaches the library’s own id creator, and leaves a constant behind', async () => {
    const { updater, creatorCalls } = updaterFake()
    hardenUpdater(updater)
    const internals = updater as unknown as {
      stagingUserIdPromise: { value: Promise<unknown> }
      configOnDisk: { value: Promise<unknown> }
      computeFinalHeaders(h: Record<string, string>): Record<string, string>
    }
    await expect(internals.stagingUserIdPromise.value).resolves.toBe(STAGING_USER_ID)
    expect(creatorCalls).toBe(0)
    // The cache the client would have read out of an `app-update.yml` this
    // bundle does not ship: the name is this app's, and the creator the library
    // would have called never runs.
    await expect(internals.configOnDisk.value).resolves.toEqual({ updaterCacheDirName: UPDATER_CACHE_DIR_NAME })
    // The merge is what the override has to survive: `requestHeaders` is
    // assigned on top of whatever the library was handed, so a header the
    // operator's own table carried would still come back -- and the staging id
    // would too, if the override deleted it before this merge rather than after.
    expect(internals.computeFinalHeaders({ [STAGING_HEADER]: 'x', Accept: 'application/json' })).toEqual({
      Accept: 'application/json',
      'X-Machine-Id': 'machine-1',
    })
  })
})

/** bodyOf is a listing body as a stream of at most `chunk` bytes a read, with
 * the cancel recorded: the bound is only a bound if the read that would pass it
 * stops, and a cancel is what stopping looks like. */
function bodyOf(text: string, chunk?: number): { readonly body: ListingBody; readonly cancelled: () => boolean } {
  const bytes = new TextEncoder().encode(text)
  const size = chunk ?? bytes.byteLength
  let at = 0
  let cancelled = false
  const reader: ListingReader = {
    read: () => {
      if (at >= bytes.byteLength) return Promise.resolve({ done: true })
      const value = bytes.slice(at, Math.min(at + size, bytes.byteLength))
      at += size
      return Promise.resolve({ done: false, value })
    },
    cancel: () => {
      cancelled = true
      return Promise.resolve()
    },
  }
  return { body: { getReader: () => reader }, cancelled: () => cancelled }
}

/** FetchRecord is one `net.fetch` call, and the response it was answered with. */
interface FetchRecord {
  readonly url: string
  readonly init: unknown
}

/** netFake is Electron's `net`: it records the request and answers with the
 * response the test built. */
function netFake(response: { readonly ok: boolean; readonly body: ListingBody | null }): {
  readonly calls: readonly FetchRecord[]
  readonly net: ListingFetcher
} {
  const calls: FetchRecord[] = []
  return {
    calls,
    net: {
      fetch: (url, init) => {
        calls.push({ url, init })
        return Promise.resolve(response)
      },
    },
  }
}

/** listing is GitHub's answer for the release endpoint, one console release. */
function listing(): string {
  return JSON.stringify([{ tag_name: CHOSEN_TAG, draft: false, prerelease: false, name: CHOSEN_TAG, id: 1 }])
}

/** packageFacts is the Electron `app` the adapter reads: a packaged Mac, which
 * is the only combination the policy is admitted on. */
function packageFacts(overrides: { readonly isPackaged?: boolean; readonly version?: string } = {}): {
  readonly isPackaged: boolean
  getVersion(): string
} {
  return { isPackaged: overrides.isPackaged ?? true, getVersion: () => overrides.version ?? RUNNING_VERSION }
}

/** noticeDialog records every notice the adapter shows and answers with one
 * response. */
function noticeDialog(response: number): { readonly asked: readonly { readonly message: string; readonly buttons: readonly string[] }[]; readonly dialog: NoticeDialog } {
  const asked: { message: string; buttons: readonly string[] }[] = []
  return {
    asked,
    dialog: {
      showMessageBox: (options) => {
        asked.push({ message: options.message, buttons: options.buttons })
        return Promise.resolve({ response })
      },
    },
  }
}

function neverFires(): IntervalTimers {
  return { setInterval: () => 0 as unknown as ReturnType<typeof setInterval>, clearInterval: () => undefined }
}

/** untilSettled lets the policy's promises run: its work is asynchronous all
 * the way down, so an assertion made before this reads a check that has not
 * finished. */
async function untilSettled(): Promise<void> {
  for (let turn = 0; turn < 256; turn += 1) await Promise.resolve()
}

/** adapter builds the client the way the entry point does, on a packaged Mac,
 * and hands back everything a test reads. */
function adapter(options: { readonly isLaunchFlowIdle?: () => boolean; readonly response?: number; readonly listing?: string } = {}): {
  readonly controller: UpdateController | null
  readonly updater: UpdaterRecord
  readonly net: ReturnType<typeof netFake>
  readonly notices: ReturnType<typeof noticeDialog>
} {
  const updater = updaterFake()
  const net = netFake({ ok: true, body: bodyOf(options.listing ?? listing()).body })
  const notices = noticeDialog(options.response ?? 1)
  const controller = startUpdates({
    isLaunchFlowIdle: options.isLaunchFlowIdle ?? (() => true),
    platform: 'darwin',
    app: packageFacts(),
    env: {},
    net: net.net,
    dialog: notices.dialog,
    timers: neverFires(),
    updater: updater.updater,
  })
  return { controller, updater, net, notices }
}

describe('the listing request', () => {
  it('asks the one URL with the two constant headers, no credentials and no redirect', async () => {
    const { controller, net } = adapter()
    await untilSettled()
    expect(net.calls).toHaveLength(1)
    expect(net.calls[0]?.url).toBe(RELEASES_URL)
    expect(net.calls[0]?.init).toEqual({
      method: 'GET',
      headers: { Accept: 'application/vnd.github+json', 'User-Agent': 'fugaro-console' },
      credentials: 'omit',
      redirect: 'error',
    })
  })

  it('stops reading a body past the bound, and cancels it', async () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const huge = 'x'.repeat(MAX_LISTING_BYTES + 1)
    const stream = bodyOf(huge, 64 * 1024)
    const updater = updaterFake()
    const net = netFake({ ok: true, body: stream.body })
    const controller = startUpdates({
      isLaunchFlowIdle: () => true,
      platform: 'darwin',
      app: packageFacts(),
      env: {},
      net: net.net,
      dialog: noticeDialog(1).dialog,
      timers: neverFires(),
      updater: updater.updater,
    })
    await untilSettled()
    expect(stream.cancelled()).toBe(true)
    expect(updater.feedUrls).toEqual([])
    expect(updater.checks).toBe(0)
    expect(logged).toHaveBeenCalled()
  })

  it('refuses a response that is not ok, and never reads it', async () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const stream = bodyOf(listing())
    const updater = updaterFake()
    const net = netFake({ ok: false, body: stream.body })
    const controller = startUpdates({
      isLaunchFlowIdle: () => true,
      platform: 'darwin',
      app: packageFacts(),
      env: {},
      net: net.net,
      dialog: noticeDialog(1).dialog,
      timers: neverFires(),
      updater: updater.updater,
    })
    await untilSettled()
    expect(updater.feedUrls).toEqual([])
    expect(logged).toHaveBeenCalled()
  })

  it('refuses a body with no stream at all', async () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const updater = updaterFake()
    const net = netFake({ ok: true, body: null })
    const controller = startUpdates({
      isLaunchFlowIdle: () => true,
      platform: 'darwin',
      app: packageFacts(),
      env: {},
      net: net.net,
      dialog: noticeDialog(1).dialog,
      timers: neverFires(),
      updater: updater.updater,
    })
    await untilSettled()
    expect(updater.feedUrls).toEqual([])
    expect(logged).toHaveBeenCalled()
  })
})

describe('the platform gate comes before the client', () => {
  /** touchedUpdater is a client that records every member read off it, so
   * "nothing in electron-updater is reached" is an assertion rather than a
   * reading of the source. */
  function touchedUpdater(): { readonly touched: readonly string[]; readonly updater: UpdaterPort } {
    const touched: string[] = []
    const target = updaterFake().updater
    const proxy = new Proxy(target as unknown as Record<string, unknown>, {
      get: (held, key) => {
        if (typeof key === 'string') touched.push(key)
        return Reflect.get(held, key)
      },
      has: (held, key) => {
        if (typeof key === 'string') touched.push(key)
        return Reflect.has(held, key)
      },
      set: (held, key, value) => {
        if (typeof key === 'string') touched.push(key)
        return Reflect.set(held, key, value)
      },
    })
    return { touched, updater: proxy as unknown as UpdaterPort }
  }

  it('reaches no member of the client on a platform that is not macOS', () => {
    const { touched, updater } = touchedUpdater()
    const controller = startUpdates({
      isLaunchFlowIdle: () => true,
      platform: 'linux',
      app: packageFacts(),
      env: {},
      net: netFake({ ok: true, body: null }).net,
      dialog: noticeDialog(1).dialog,
      timers: neverFires(),
      updater,
    })
    expect(controller).toBeNull()
    expect(touched).toEqual([])
  })

  it('reaches no member of the client when the app is not packaged', () => {
    const { touched, updater } = touchedUpdater()
    const controller = startUpdates({
      isLaunchFlowIdle: () => true,
      platform: 'darwin',
      app: packageFacts({ isPackaged: false }),
      env: {},
      net: netFake({ ok: true, body: null }).net,
      dialog: noticeDialog(1).dialog,
      timers: neverFires(),
      updater,
    })
    expect(controller).toBeNull()
    expect(touched).toEqual([])
  })

  it('hardens the client and starts it on a packaged macOS app', async () => {
    const { controller, updater } = adapter()
    expect(controller).not.toBeNull()
    await untilSettled()
    expect(updater.creatorCalls).toBe(0)
    expect(updater.feedUrls).toEqual(['https://github.com/dimipaun/fugaro/releases/download/console-v0.120.0/'])
    expect(updater.autoDownload).toBe(true)
    expect(updater.autoInstallOnAppQuit).toBe(false)
    expect(updater.allowDowngrade).toBe(false)
    expect(updater.allowPrerelease).toBe(false)
    expect(updater.headers).toEqual({})
    expect(updater.events.toSorted()).toEqual(['error', 'update-downloaded'])
  })

  it('throws rather than starting a client it cannot harden', () => {
    // The throw is `hardenUpdater`'s, and it is where "this is not the client I
    // was written against" is decided. It does not reach the app: the test below
    // is what the entry point does with it.
    const { updater } = updaterFake()
    delete (updater as unknown as Record<string, unknown>)['stagingUserIdPromise']
    expect(() => hardenUpdater(updater)).toThrow(/6\.8\.10/)
  })

  it('turns updates off in one constant line rather than failing to start', () => {
    // Owner ruling of 2026-10-30: a client that cannot be hardened is a feature
    // that stays off, not an app that does not start. The line is written out
    // here rather than imported, because what this asserts is the line's text:
    // one constant sentence, and neither the library's error nor a path nor a
    // version out of it.
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const { updater } = updaterFake()
    delete (updater as unknown as Record<string, unknown>)['stagingUserIdPromise']
    let controller: UpdateController | null = null
    expect(() => {
      controller = startUpdates({
        isLaunchFlowIdle: () => true,
        platform: 'darwin',
        app: packageFacts(),
        env: {},
        net: netFake({ ok: true, body: null }).net,
        dialog: noticeDialog(1).dialog,
        timers: neverFires(),
        updater,
      })
    }).not.toThrow()
    expect(controller).toBeNull()
    expect(logged.mock.calls).toEqual([
      ['fugaro console: the update client could not be hardened; automatic updates are off'],
    ])
  })
})

describe('the download cache, which the bundle has no config for', () => {
  it('names it where the hardening puts it, on the real client', async () => {
    // The bundle ships no `app-update.yml` (electron-builder's `publish` is
    // null), so `configOnDisk` is the ENOENT the next test shows: without the
    // name written into it, a download that the feed has already offered fails
    // at the last step, and the cache directory is the one place it would have
    // been written to.
    const updater = new MacUpdater(NO_CONFIG, appAdapter(directory))
    hardenUpdater(updater as unknown as UpdaterPort)

    const helper = await downloadHelper(updater)

    expect(helper.cacheDir).toBe(join(directory, 'cache', 'fugaro-console-updater'))
    expect(existsSync(join(directory, 'app-update.yml'))).toBe(false)
  })

  it('cannot build a download helper at all without the hardening, on the same client', async () => {
    // The red step, held as a test: it is what the assertion above is worth.
    const updater = new MacUpdater(NO_CONFIG, appAdapter(directory))

    await expect(downloadHelper(updater)).rejects.toMatchObject({ code: 'ENOENT' })
  })
})

describe('the install the client would do on the way out', () => {
  /** configureThroughThePolicy runs the entry point over a real client and the
   * listing that offers nothing: `configure()` is then the whole of what the
   * policy wrote, and no check ever points the feed anywhere. */
  async function configureThroughThePolicy(): Promise<MacUpdater> {
    const updater = new MacUpdater(NO_CONFIG, appAdapter(directory))
    const controller = startUpdates({
      isLaunchFlowIdle: () => true,
      platform: 'darwin',
      app: packageFacts(),
      env: {},
      net: netFake({ ok: true, body: bodyOf('[]').body }).net,
      dialog: noticeDialog(1).dialog,
      timers: neverFires(),
      updater: updater as unknown as UpdaterPort,
    })
    expect(controller).not.toBeNull()
    await untilSettled()
    return updater
  }

  it('leaves the flag off on the real client the policy hands over', async () => {
    // Owner ruling of 2026-10-30: an update is installed only through the
    // notice's "Restart to update". On macOS the flag is the whole of the
    // difference: `MacUpdater.doDownloadUpdate` (~220-227) tells Squirrel to
    // fetch and install the file only `if (this.autoInstallOnAppQuit)`, and
    // Squirrel's own install happens when the app quits. `MacUpdater` extends
    // `AppUpdater` directly -- `BaseUpdater`'s `addQuitHandler` is the Linux and
    // Windows updaters' -- so there is no second hook to read, and the download
    // path that would show the fetch is not driven here: an injected `AppAdapter`
    // leaves the library without an HTTP executor (AppUpdater.js ~210), so the
    // claim this test makes is the flag, on the real client, and no more.
    const updater = await configureThroughThePolicy()

    expect(updater.autoInstallOnAppQuit).toBe(false)
  })

  it('starts true on the same client, which is the default the policy opposes', () => {
    // The control, without which the assertion above would hold for a client
    // that never had the flag on in the first place (AppUpdater.js ~114).
    const updater = new MacUpdater(NO_CONFIG, appAdapter(directory))

    expect(updater.autoInstallOnAppQuit).toBe(true)
  })

  it('leaves the flag off on the real client even when the policy never configures', async () => {
    // The test above leaves the flag off through the policy's `configure()`, and
    // this one is a different promise: the adapter's own write. A gate that never
    // opens -- CI here, the local opt-out, the visible switch off -- runs no
    // `configure()` at all, and a client left at the library's default is one
    // armed to install on the way out with nobody having decided anything. The
    // null `requestHeaders` is the witness that nothing else touched this client:
    // the library's constructor leaves it null (AppUpdater.js ~178) and only
    // `configure()` writes `{}`.
    const updater = new MacUpdater(NO_CONFIG, appAdapter(directory))
    const controller = startUpdates({
      isLaunchFlowIdle: () => true,
      platform: 'darwin',
      app: packageFacts(),
      env: { CI: '1' },
      net: netFake({ ok: true, body: bodyOf('[]').body }).net,
      dialog: noticeDialog(1).dialog,
      timers: neverFires(),
      updater: updater as unknown as UpdaterPort,
    })
    await untilSettled()

    expect(controller).not.toBeNull()
    expect(updater.requestHeaders).toBeNull()
    expect(updater.autoInstallOnAppQuit).toBe(false)
  })
})

describe('the notice', () => {
  it('offers the restart, and restarts only when the operator asks for it', async () => {
    const { controller, updater, notices } = adapter({ response: 0 })
    await untilSettled()
    updater.emit('update-downloaded', { version: CHOSEN_VERSION })
    await untilSettled()
    expect(notices.asked).toHaveLength(1)
    expect(notices.asked[0]?.buttons).toEqual(['Restart to update', 'Later'])
    expect(notices.asked[0]?.message).toContain(CHOSEN_VERSION)
    expect(updater.quits).toBe(1)
  })

  it('restarts nothing when the operator says later', async () => {
    const { controller, updater, notices } = adapter({ response: 1 })
    await untilSettled()
    updater.emit('update-downloaded', { version: CHOSEN_VERSION })
    await untilSettled()
    expect(notices.asked).toHaveLength(1)
    expect(updater.quits).toBe(0)
  })
})

/** heldConfirmer is a confirmation window that stays open until the test
 * answers it, which is what holds a launch in flight. */
function heldConfirmer(): LaunchConfirmer & { readonly openRunIds: readonly string[]; decideWindow(runId: string, yes: boolean): void } {
  const waiting = new Map<string, (yes: boolean, reason?: string) => void>()
  const open: string[] = []
  const decideWindow = (runId: string, yes: boolean): void => {
    const held = waiting.get(runId)
    waiting.delete(runId)
    held?.(yes)
  }
  return {
    open(view: ConfirmView, decide: (yes: boolean, reason?: string) => void) {
      open.push(view.runId)
      waiting.set(view.runId, decide)
    },
    close(runId: string) {
      decideWindow(runId, false)
    },
    decideWindow,
    get openRunIds() {
      return open
    },
  }
}

/** neverFiringTimers is the launcher's `setTimeout`: the launch in this suite
 * never reaches the point where a timer matters, and a real one would leave a
 * handle behind. */
function neverFiringTimers(): { setTimeout(handler: () => void, ms: number): ReturnType<typeof setTimeout>; clearTimeout(handle: ReturnType<typeof setTimeout>): void } {
  return { setTimeout: () => 0 as unknown as ReturnType<typeof setTimeout>, clearTimeout: () => undefined }
}

/** lookUp is the records the launcher reads: one installation, one repository. */
function lookUp(): Promise<TargetLookup> {
  return Promise.resolve({
    ok: true,
    target: {
      project: 'sandbox',
      binaryPath: '/usr/local/bin/fugaro',
      cliVersion: '0.11.2',
      records: { baseBranch: 'main', repositories: [{ repository: 'github/acme/app', baseBranch: '' }] },
      fingerprint: '["record-1","sandbox","/usr/local/bin/fugaro","","",""]',
    },
  })
}

describe('never while a launch is in flight', () => {
  it('offers a finished download the moment the launch flow settles, not before', async () => {
    // **This is the test the headline mutation is aimed at.** It is the whole
    // path a real download takes: a real `createLaunchRunner` holds a launch in
    // flight, the adapter is wired to it exactly as `index.ts` wires it, and the
    // download lands in the middle. An `isIdle()` that answered `true` while a
    // window was open would offer the operator a restart that replaces the
    // process writing that launch's record -- and the notice would be there
    // before the launch settled, which is what the first assertion reads.
    const confirmer = heldConfirmer()
    const runner = createLaunchRunner({
      lookUp,
      stillRecorded: () => true,
      confirmer,
      appEnv: {},
      timers: neverFiringTimers(),
    })
    const launched = runner.launch({ installation: 'sandbox', repository: 'github/acme/app', task: 'summarise the alerting backlog' })
    await untilSettled()
    expect(runner.isIdle()).toBe(false)

    const { controller, updater, notices } = adapter({ isLaunchFlowIdle: () => runner.isIdle(), response: 1 })
    runner.onIdle(() => {
      controller?.launchFlowIdle()
    })
    await untilSettled()
    updater.emit('update-downloaded', { version: CHOSEN_VERSION })
    await untilSettled()
    expect(notices.asked).toEqual([])
    expect(updater.quits).toBe(0)

    const runId = confirmer.openRunIds[0] ?? ''
    expect(runId).not.toBe('')
    confirmer.decideWindow(runId, false)
    await launched
    await untilSettled()
    expect(runner.isIdle()).toBe(true)
    expect(notices.asked).toHaveLength(1)
  })
})
