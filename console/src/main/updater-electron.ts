// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The update client: electron-updater 6.8.10 behind the `UpdaterPort` the
// policy in `./updates.ts` is written against (desktop-console plan Task 4.4,
// "Auto-update -- SECURITY-SENSITIVE"; design section 7.4 for "no update check
// that carries an identifier", section 5.2 for the pinned supply chain).
//
// **The identifier this module exists to remove.** U1's review of 2026-10-30
// found that 6.8.10 sends and stores an identifier of its own:
// `AppUpdater.getUpdateInfoAndProvider` (out/AppUpdater.js ~380-392) calls
// `client.setRequestHeaders(this.computeFinalHeaders({ "x-user-staging-id":
// stagingUserId }))` on every feed request, and `stagingUserIdPromise`
// (~189) is a `Lazy` over `getOrCreateStagingUserId` (~500-525), which mints a
// random UUID and writes it to `<userData>/.updaterId`. `computeFinalHeaders`
// (~332-338) merges `requestHeaders` *on top*, so the policy setting
// `requestHeaders = {}` cannot take the header back off: the library adds it
// after. `hardenUpdater` is the whole answer, and it is deliberately
// fail-closed -- a client whose members are not where 6.8.10 has them is a
// client this module refuses to start at all, because the failure it guards
// against is silent and the thing it would leak is the operator's.
//
// **The config the bundle does not ship, and the install it must never do.**
// The same constructor reads `configOnDisk` -- a `Lazy` over the
// `app-update.yml` that electron-builder writes into a packaged app -- for the
// name of the directory a download is cached in
// (`getOrCreateDownloadHelper`, ~542-556, `path.join(this.app.baseCachePath,
// dirName)`), and this bundle sets `publish: null`, so there is no such file:
// unhardened, every download fails with ENOENT at the last step, after the feed
// has been read and the release offered. That name is written here instead.
// `autoInstallOnAppQuit` is 6.8.10's own install-on-quit, and on macOS it is
// read in one place -- `MacUpdater.doDownloadUpdate` (~220-227) tells Squirrel
// to fetch the file only `if (this.autoInstallOnAppQuit)`, and Squirrel installs
// it when the app quits. The owner's ruling of 2026-10-30 is that an update is
// installed only through the notice's "Restart to update", so this module turns
// the flag off on the client it hands over and `./updates.ts` never turns it on.
//
// A client this module cannot harden does not stop the console: `startUpdates`
// logs one constant line and answers null, which is the same answer it gives on
// every other platform, so the app starts with updates off rather than not at
// all.

import { app as electronApp, dialog as electronDialog, net as electronNet } from 'electron'
import { autoUpdater } from 'electron-updater'

import { createUpdateController, MAX_LISTING_BYTES } from './updates'
import type { FetchReleases, IntervalTimers, UpdateController, UpdatePolicyDeps, UpdaterPort } from './updates'

/** STAGING_USER_ID is what replaces the identifier the library would mint, and
 * it is the all-zero UUID rather than an empty string because the library feeds
 * it to `UUID.parse` when it reads a rollout percentage: a value that parses is
 * a value that cannot throw inside a check. Every installation sends the same
 * constant, so the header, if the library ever adds it back, carries nothing
 * about this machine. */
export const STAGING_USER_ID = '00000000-0000-0000-0000-000000000000'

/** UPDATER_CACHE_DIR_NAME is the directory a downloaded update is cached in,
 * under `app.baseCachePath` (`~/Library/Caches` on macOS). The name is this
 * app's own because the file the library would read it from -- the
 * `app-update.yml` electron-builder writes for a published app -- is one this
 * bundle does not have, and a name is all `getOrCreateDownloadHelper` needs. */
export const UPDATER_CACHE_DIR_NAME = 'fugaro-console-updater'

/** HARDENING_FAILED is the refusal `hardenUpdater` throws. It names the version
 * it was written against, because the only reason to see it is that this
 * dependency moved. */
const HARDENING_FAILED =
  'fugaro console: electron-updater 6.8.10 no longer has the members this app rewrites; the update check is not started'

/** UPDATES_OFF is the one line the console logs when it cannot harden the
 * client, and it is the whole of the message: the failure names a member of a
 * package, and neither an error's text nor a path nor a version out of the
 * library belongs in a log the user may paste. */
const UPDATES_OFF = 'fugaro console: the update client could not be hardened; automatic updates are off'

const LISTING_UNREADABLE = 'the release listing could not be read'

/** The members `hardenUpdater` reaches for, as the shape it expects rather than
 * as a package's types: none is public in electron-updater's own `.d.ts`
 * (`stagingUserIdPromise`, `configOnDisk` and `computeFinalHeaders` are
 * `protected` and `private`), and the point of the hardening is to check they
 * are where this app believes they are instead of trusting a type that would
 * let a rename through. */
interface UpdaterInternals {
  stagingUserIdPromise?: { value: unknown }
  configOnDisk?: { value: unknown }
  computeFinalHeaders?: (headers: Record<string, string>) => Record<string, string>
}

/** writableLazy is the check that a member `hardenUpdater` rewrites is a `Lazy`
 * at all: `lazy-val` keeps the `value` setter on the prototype, so a member
 * that has one is a `Lazy` and a member that does not is something this app has
 * no way to disarm. */
function writableLazy(holder: { value: unknown } | undefined): { value: unknown } {
  if (holder === undefined || holder === null) throw new Error(HARDENING_FAILED)
  const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(holder) as object, 'value')?.set
  if (typeof setter !== 'function') throw new Error(HARDENING_FAILED)
  return holder
}

/** setLazy writes a `Lazy`'s value and reads it back: a `Lazy` that answers
 * something else is one this app did not write to. */
function setLazy(holder: { value: unknown }, value: unknown): void {
  holder.value = value
  if (holder.value !== value) throw new Error(HARDENING_FAILED)
}

/** hardenUpdater rewrites an electron-updater client where 6.8.10 would
 * otherwise leak, write or install something this app does not, and throws
 * rather than returning when it cannot.
 *
 * Every part is needed and none is sufficient. The `Lazy`s' own `value` setters
 * are used rather than their creators replaced, so `getOrCreateStagingUserId`
 * is never called at all: no UUID is minted, nothing is written to
 * `.updaterId`, and `.value` answers the constant from then on -- and
 * `getOrCreateDownloadHelper` reads a cache directory name out of a file this
 * bundle does not ship, unless `configOnDisk` answers one first. The header is
 * then removed by an override on the **instance**, because the library's copy
 * merges `requestHeaders` over whatever it is handed -- an override that
 * deleted the header before that merge would be undone by it.
 *
 * Everything is checked before anything is written: a client with one member
 * missing must not be left half-rewritten, because the half that is left is the
 * half the console would run on. */
export function hardenUpdater(updater: UpdaterPort): void {
  const internals = updater as unknown as UpdaterInternals
  const staging = writableLazy(internals.stagingUserIdPromise)
  const config = writableLazy(internals.configOnDisk)
  if (typeof internals.computeFinalHeaders !== 'function') throw new Error(HARDENING_FAILED)

  const merged = internals.computeFinalHeaders.bind(updater)
  internals.computeFinalHeaders = (headers: Record<string, string>): Record<string, string> => {
    const final = merged(headers)
    delete final['x-user-staging-id']
    return final
  }
  setLazy(staging, Promise.resolve(STAGING_USER_ID))
  setLazy(config, Promise.resolve({ updaterCacheDirName: UPDATER_CACHE_DIR_NAME }))

  // The check that the override took: `setLazy` has already read each `Lazy`
  // back, and this is the identifier's own post-condition -- it is the last
  // thing that runs before a check could start, so it is where "the identifier
  // is gone" is decided rather than assumed.
  const probe = internals.computeFinalHeaders({ 'x-user-staging-id': STAGING_USER_ID })
  if (probe['x-user-staging-id'] !== undefined) throw new Error(HARDENING_FAILED)
}

/** ListingRequest is the whole of what a check sends, and it is fixed: design
 * section 7.4 allows the update check as the app's only outbound request and
 * forbids it carrying an identifier, so the request is a method, a constant
 * header table and no credentials. `redirect: 'error'` is part of the same
 * rule -- the URL is a constant, so a response that points somewhere else is a
 * request to somewhere this app never decided to ask. */
export interface ListingRequest {
  readonly method: string
  readonly headers: Readonly<Record<string, string>>
  readonly credentials: 'omit'
  readonly redirect: 'error'
}

/** ListingBody and ListingReader are the part of a `Response` the bounded read
 * uses, narrowed to it: the adapter stops at `MAX_LISTING_BYTES` rather than
 * buffering a body GitHub (or anything in the path) can make arbitrarily long. */
export interface ListingReader {
  read(): Promise<{ readonly done: boolean; readonly value?: Uint8Array }>
  cancel(): Promise<void>
}

export interface ListingBody {
  getReader(): ListingReader
}

/** ListingFetcher is the one request the adapter makes, as Electron's `net`
 * exposes it: `net.fetch` and not the renderer's `fetch`, because this runs in
 * the main process and the request must not be attributable to a session. */
export interface ListingFetcher {
  fetch(input: string, init: ListingRequest): Promise<{ readonly ok: boolean; readonly body: ListingBody | null }>
}

/** NoticeDialog is the one native notice the console shows, narrowed to the
 * call it makes so that a test reads the question rather than a window. */
export interface NoticeDialog {
  showMessageBox(options: {
    readonly type: 'info'
    readonly message: string
    readonly detail: string
    readonly buttons: readonly string[]
    readonly defaultId: number
    readonly cancelId: number
  }): Promise<{ readonly response: number }>
}

export interface UpdateAdapterOptions {
  /** isLaunchFlowIdle is the launch flow's answer about itself, asked afresh
   * every time it matters: a signed update replaces the process that owns the
   * launch write path, so nothing prompts or installs while a launch is in
   * flight. */
  readonly isLaunchFlowIdle: () => boolean
  /** The four Electron and process facts the policy reads. They are here as
   * arguments so that a test can hold the app on a Mac that is packaged without
   * being one. */
  readonly platform?: string
  readonly app?: { readonly isPackaged: boolean; getVersion(): string }
  readonly env?: Readonly<Record<string, string | undefined>>
  /** switchOn is the app's own visible off switch (design D5). The console has
   * no setting for it yet -- the toggle is the surface work that follows this
   * task -- so the default is the feature on, which is what the design's
   * "on by default, visible, and it stays off" asks for at the policy. */
  readonly switchOn?: () => boolean
  readonly timers?: IntervalTimers
  readonly net?: ListingFetcher
  readonly dialog?: NoticeDialog
  /** updater is electron-updater's own singleton unless a test hands over
   * something else; it is read only after the platform and the packaging are
   * known, so an unpackaged console or a non-Mac never constructs a client. */
  readonly updater?: UpdaterPort
}

/** readBounded is the listing read, stopped at `MAX_LISTING_BYTES` rather than
 * measured against it afterwards: the policy can only refuse an oversized body
 * once it holds one, so the read that would hold it is the read that has to
 * stop. A body with no stream is refused for the same reason -- there would be
 * nothing to stop. */
async function readBounded(body: ListingBody): Promise<string> {
  const reader = body.getReader()
  const parts: Uint8Array[] = []
  let total = 0
  for (;;) {
    const chunk = await reader.read()
    if (chunk.done) break
    const bytes = chunk.value ?? new Uint8Array(0)
    total += bytes.byteLength
    if (total > MAX_LISTING_BYTES) {
      await reader.cancel()
      throw new Error(LISTING_UNREADABLE)
    }
    parts.push(bytes)
  }
  const joined = new Uint8Array(total)
  let at = 0
  for (const part of parts) {
    joined.set(part, at)
    at += part.byteLength
  }
  return new TextDecoder('utf-8').decode(joined)
}

/** readyNotice asks the operator whether to restart, and does nothing else with
 * the answer: D762's notice is non-blocking, so the console keeps running
 * whether or not anybody answers, and the answer is only ever the answer to
 * this question. The detail says which version is on disk because the two
 * buttons are the whole of the message. */
function readyNotice(dialog: NoticeDialog, version: string, onRestart: () => void): void {
  void dialog
    .showMessageBox({
      type: 'info',
      message: `Fugaro Console ${version} is ready to install`,
      detail: 'Restart the console to finish the update. Until then this version keeps running.',
      buttons: ['Restart to update', 'Later'],
      defaultId: 1,
      cancelId: 1,
    })
    .then((answer) => {
      if (answer.response === 0) onRestart()
    })
    .catch(() => undefined)
}

export function createUpdateDeps(options: UpdateAdapterOptions & { readonly onReadyToRestart: (version: string) => void }): UpdatePolicyDeps {
  const fetcher = options.net ?? electronNet
  const fetchReleases: FetchReleases = async (url, headers) => {
    const response = await fetcher.fetch(url, { method: 'GET', headers, credentials: 'omit', redirect: 'error' })
    if (!response.ok) throw new Error(LISTING_UNREADABLE)
    if (response.body === null) throw new Error(LISTING_UNREADABLE)
    return await readBounded(response.body)
  }
  // The cast is this app's whole contract with the library, and it is on
  // purpose narrower than the class: `UpdaterPort` is what the policy may
  // reach, and electron-updater's `requestHeaders` is `OutgoingHttpHeaders |
  // null` where this port says a table, because `configure()` in the policy is
  // the only thing that ever writes it and it writes `{}`.
  const updater = options.updater ?? (autoUpdater as unknown as UpdaterPort)
  hardenUpdater(updater)
  // 6.8.10 ships this on (AppUpdater.js ~114), and on macOS it is what lets a
  // finished download reach Squirrel to be installed when the app quits
  // (MacUpdater.doDownloadUpdate, ~220-227). The console installs only through
  // the notice's "Restart to update", so the client is handed over with the flag
  // already off -- before any check, and whether or not one ever runs -- and the
  // policy keeps it off. Nothing else in this app writes it.
  updater.autoInstallOnAppQuit = false
  return {
    platform: options.platform ?? process.platform,
    isPackaged: (options.app ?? electronApp).isPackaged,
    env: options.env ?? process.env,
    currentVersion: (options.app ?? electronApp).getVersion(),
    switchOn: options.switchOn ?? ((): boolean => true),
    timer: options.timers ?? { setInterval, clearInterval },
    fetchReleases,
    isLaunchFlowIdle: options.isLaunchFlowIdle,
    onReadyToRestart: options.onReadyToRestart,
    updater,
  }
}

/** startUpdates is the entry point's whole side of the update policy: it
 * answers the controller to stop on the way out, or null when this console may
 * not update at all.
 *
 * **The admission test comes first and is the only thing standing between a
 * Linux build and an electron-updater client.** `autoUpdater` is a lazy getter
 * that constructs a platform's own updater class the moment it is read, and the
 * policy's own gate is read later and repeatedly -- so the platform and the
 * packaging are decided here, once, before anything in the package is touched.
 * A non-Mac or an unpackaged console therefore reaches no member of it.
 *
 * A client that cannot be hardened is the same answer, and it is an answer
 * rather than a failure: an update check the app cannot make safely is a
 * feature that stays off, and the console that does not start at all is a worse
 * outcome than either. `hardenUpdater` still throws -- that is how it says
 * "this is not the client I was written against" -- and the throw stops here,
 * with one constant line to say so. */
export function startUpdates(options: UpdateAdapterOptions): UpdateController | null {
  const platform = options.platform ?? process.platform
  const app = options.app ?? electronApp
  if (platform !== 'darwin' || !app.isPackaged) return null
  // A lambda rather than Electron's `dialog` itself: its `showMessageBox` has a
  // two-argument overload first, so the object is not the one-method shape this
  // adapter asks for, and the wrapper is also where the buttons array stops
  // being readonly.
  const dialog: NoticeDialog = options.dialog ?? {
    showMessageBox: (ask) => electronDialog.showMessageBox({ ...ask, buttons: [...ask.buttons] }),
  }
  const holder: { controller: UpdateController | null } = { controller: null }
  let deps: UpdatePolicyDeps
  try {
    deps = createUpdateDeps({
      ...options,
      platform,
      app,
      onReadyToRestart: (version) => {
        readyNotice(dialog, version, () => {
          // The version the notice was opened with, and not the one this app
          // holds now: the operator answered about the download it named.
          holder.controller?.restartRequested(version)
        })
      },
    })
  } catch {
    console.error(UPDATES_OFF)
    return null
  }
  holder.controller = createUpdateController(deps)
  void holder.controller.start()
  return holder.controller
}
