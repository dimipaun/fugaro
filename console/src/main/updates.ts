// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The update policy (desktop-console plan Task 4.4, "Auto-update --
// SECURITY-SENSITIVE"; design section 7.3 for the cadence, the refused
// downgrade and the visible off switch, section 7.4 for "no update check that
// carries an identifier").
//
// A signed update replaces the process that owns the console's launch write
// path, so this module is a trust boundary rather than a convenience. The rules
// it holds are the ones Task 4.4's own test list names -- the opt-out honoured
// all three ways, a request that carries no identifier, a refused downgrade, a
// check that never runs in CI -- and one this work adds: nothing prompts or
// installs while a launch is in flight. The owner's ruling of 2026-10-30 is
// that nothing installs on quit either: an update is installed only through the
// notice's "Restart to update", so the client's own install-on-quit is off from
// the first configuration and no path here turns it on. That notice names the
// download it was opened for, and a press installs only that one: a check that
// lands between the prompt and the answer leaves the app holding other bytes,
// and the bytes that were not offered are not the ones installed. It reaches
// none of that on its own: the listing arrives through an injected
// `fetchReleases`, six hours pass on an injected timer and the client is an
// injected `UpdaterPort` that the adapter fills with electron-updater. What that
// buys is a policy testable with no network, no clock and no shipped update.

/** UPDATE_INTERVAL_MS is the interval between checks: design section 7.3 asks
 * for "check on launch and then on a long interval", and six hours is what this
 * work fixes that interval at -- long enough that an idle console is invisible
 * on the wire, short enough that a running one learns of a release the day it
 * ships. */
export const UPDATE_INTERVAL_MS = 6 * 60 * 60 * 1000

/** RELEASES_URL is the release listing of this repository. It is the console's
 * own repository because the console releases on its own tag (Task 4.4's release
 * note: the feed "point[s] at the console's own `console-vX.Y.Z` release ...,
 * never at the CLI's `vX.Y.Z`"); the listing is read rather than
 * `/releases/latest` because the newest release is often a CLI one, which the
 * filter below has to be able to skip past.
 *
 * `per_page=100` is the whole of the query, and it is there because the check
 * reads **one page**: GitHub's default page is thirty releases, the CLI releases
 * faster than the console, and a burst of thirty between two console releases
 * would push the newest console tag off the page the console ever asks for --
 * a check that answers "nothing new" for months. It stays on the allow-list
 * below by being part of this constant and nothing else. */
export const RELEASES_URL = 'https://api.github.com/repos/dimipaun/fugaro/releases?per_page=100'

/** FEED_PREFIX is the whole of the feed URL but its tag, and the only host an
 * update may ever be downloaded from: a feed URL the policy did not build is a
 * feed URL nobody checked. */
const FEED_PREFIX = 'https://github.com/dimipaun/fugaro/releases/download/'

/** CONSOLE_TAG_RE is a console release tag, and nothing else: the tag alone
 * decides what may be appended to `FEED_PREFIX`, so it is a shape that cannot
 * carry a second path segment, a percent-escape, a query or a fragment. Its
 * version rule is `internal/updatecheck`'s -- no leading zero, at most nine
 * digits per component -- narrowed to the console's own prefix. */
const CONSOLE_TAG_RE = /^console-v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$/

/** RUNNING_VERSION_RE is the running app's version. It tolerates the CLI's
 * `v` prefix because the version string may come from a tag, and tolerates
 * nothing else: a pre-release or a dev version is not something to compare a
 * release against. */
const RUNNING_VERSION_RE = /^v?(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$/

const CONSOLE_TAG_PREFIX = 'console-v'

/** MAX_LISTING_BYTES bounds the listing, and it is sized against the page the
 * URL above asks for rather than against a page in general: `per_page=100` makes
 * a page up to a hundred release objects, each carrying its whole release-notes
 * body and its asset list, where the thirty-object page the bound was first
 * written for was about three and a half times smaller. Four MiB is the same
 * allowance the original one MiB made -- roughly forty KB a release, against a
 * real page of tens of KB -- and the coupling is asserted in `updates.test.ts`,
 * because a bound below a page GitHub will really answer is a check that fails
 * on every tick and offers no release ever again, which is exactly the failure
 * `per_page=100` exists to avoid.
 *
 * It is exported because the bound has to be the caller's too: this module can
 * only refuse an oversized body once it holds it, and the read that reads it is
 * the injected `fetchReleases`, so the adapter must stop reading at this length
 * rather than buffer a body GitHub (or anything in the path) can make
 * arbitrarily long. The CLI's own 64 KiB is for one release object from
 * `/releases/latest`, not a page. */
export const MAX_LISTING_BYTES = 4 << 20

/** LISTING_HEADERS is the whole of what a check sends, and it is fixed: design
 * section 7.4 allows the update check as the app's only outbound request and
 * forbids it carrying an identifier, and GitHub asks for a `User-Agent` -- which
 * is therefore a constant, not a version, a machine, a user or an installation
 * name. */
const LISTING_HEADERS: Readonly<Record<string, string>> = Object.freeze({
  Accept: 'application/vnd.github+json',
  'User-Agent': 'fugaro-console',
})

/** The three failure lines are constants rather than built from the failure
 * they report: a fetch error carries the URL it asked for, an updater error can
 * carry a response body, and neither belongs in a log the user may paste. What
 * is useful is that a check failed and that the next one will try again. */
const LISTING_FAILED = 'fugaro console: the release listing could not be read; the next check will try again'
const CLIENT_FAILED = 'fugaro console: the update client refused the check; the next check will try again'
const TAG_REFUSED = 'the update feed URL is refused: that tag is not a console release tag'

/** RESTART_REFUSED is the line a press for a version this policy does not hold
 * gets, and it is a constant for the reason the three above are: the press names
 * a version, and a version is exactly what does not belong in a log the user may
 * paste. What is useful is that the restart was refused. */
const RESTART_REFUSED = 'fugaro console: the restart is refused; the update on disk is not the version the notice named'

/** IntervalTimers is `setInterval`/`clearInterval`, injected so the cadence is
 * a test's to move rather than a wall clock's. */
export interface IntervalTimers {
  setInterval(handler: () => void, ms: number): ReturnType<typeof setInterval>
  clearInterval(handle: ReturnType<typeof setInterval>): void
}

/** FetchReleases is the one network call this module makes, as an argument.
 * The body arrives as text so that what is parsed is this module's decision; the
 * length it may reach is the caller's to enforce, against `MAX_LISTING_BYTES`. */
export type FetchReleases = (url: string, headers: Readonly<Record<string, string>>) => Promise<string>

/** UpdaterPort is the subset of electron-updater's `AppUpdater` the policy
 * uses, under electron-updater's own member names, so that the adapter is a
 * pass-through and this module never imports the package. */
export interface UpdaterPort {
  setFeedURL(options: { provider: 'generic'; url: string }): void
  checkForUpdates(): Promise<unknown>
  quitAndInstall(): void
  autoDownload: boolean
  autoInstallOnAppQuit: boolean
  allowDowngrade: boolean
  allowPrerelease: boolean
  requestHeaders: Record<string, string>
  on(event: 'update-downloaded' | 'error', listener: (...args: unknown[]) => void): unknown
}

/** UpdatePolicyDeps is everything the policy is allowed to reach. The two
 * opt-outs are here as questions rather than as facts, and each is asked afresh
 * when it matters: `switchOn` is the app's own visible toggle (design D5), and
 * `FUGARO_NO_UPDATE_CHECK` is read off the environment. The CLI's
 * `update_check: false` is deliberately not one of them: design section 5.1
 * rule 7 keeps the console out of the CLI's config, and the auto-update plan's
 * D769 leaves that setting to the CLI. `isLaunchFlowIdle` is the launch flow's
 * answer about itself. */
export interface UpdatePolicyDeps {
  readonly platform: string
  readonly isPackaged: boolean
  readonly env: Readonly<Record<string, string | undefined>>
  readonly currentVersion: string
  readonly switchOn: () => boolean
  readonly timer: IntervalTimers
  readonly fetchReleases: FetchReleases
  readonly isLaunchFlowIdle: () => boolean
  readonly onReadyToRestart: (version: string) => void
  readonly updater: UpdaterPort
}

export interface UpdateController {
  start(): Promise<void>
  stop(): void
  checkNow(): Promise<void>
  /** restartRequested is the notice's "Restart to update", answered with the
   * version that notice was opened with: the notice is answered later than it
   * was shown, so the caller's version is what ties the press to a download. */
  restartRequested(version: string): void
  launchFlowIdle(): void
}

/** Version is a parsed release version, compared as three numbers and never as
 * text: `0.120.0` is above `0.99.0`, which a string comparison gets wrong. */
type Version = readonly [number, number, number]

function parseVersion(pattern: RegExp, text: string): Version | null {
  const parts = pattern.exec(text)
  if (parts === null) return null
  return [Number(parts[1]), Number(parts[2]), Number(parts[3])]
}

function above(candidate: Version, running: Version): boolean {
  if (candidate[0] !== running[0]) return candidate[0] > running[0]
  if (candidate[1] !== running[1]) return candidate[1] > running[1]
  return candidate[2] > running[2]
}

/** stableConsoleTag is the tag of a release that may be offered, or null: a
 * draft or a pre-release is out on its flag, and a tag of any other shape is out
 * on its text. Everything is read off an untrusted body, so each field is
 * checked by name rather than assumed to be there. */
function stableConsoleTag(entry: unknown): string | null {
  if (entry === null || typeof entry !== 'object') return null
  const release = entry as Record<string, unknown>
  if (release['draft'] !== false || release['prerelease'] !== false) return null
  const tag = release['tag_name']
  return typeof tag === 'string' ? tag : null
}

/** newestStableConsoleRelease is the newest release the console may move to, as
 * a tag, or null when there is none: drafts, pre-releases, CLI tags and
 * malformed tags are not releases of this app, and nothing at or below `current`
 * is a release to move to at all -- design section 7.3's "never downgrade"
 * covers the lower one, and reinstalling what is already running is the same
 * answer. */
export function newestStableConsoleRelease(releases: unknown, current: string): string | null {
  const running = parseVersion(RUNNING_VERSION_RE, current)
  if (running === null || !Array.isArray(releases)) return null
  let newest: { readonly tag: string; readonly version: Version } | null = null
  for (const entry of releases) {
    const tag = stableConsoleTag(entry)
    if (tag === null) continue
    const version = parseVersion(CONSOLE_TAG_RE, tag)
    if (version === null || !above(version, running)) continue
    if (newest === null || above(version, newest.version)) newest = { tag, version }
  }
  return newest === null ? null : newest.tag
}

/** feedUrlFor is the second and last URL this console may build: the tag has to
 * be a console release tag, so another host, another scheme, another repository,
 * a path that steps up a level and an encoded separator cannot even be written
 * down, whatever the listing said. A tag that fails is a throw rather than a
 * repair, because there is nothing safe to guess at. */
export function feedUrlFor(tag: string): string {
  if (typeof tag !== 'string' || !CONSOLE_TAG_RE.test(tag)) throw new Error(TAG_REFUSED)
  return `${FEED_PREFIX}${tag}/`
}

export function createUpdateController(deps: UpdatePolicyDeps): UpdateController {
  let handle: ReturnType<typeof setInterval> | null = null
  let configured = false
  /** generation is what makes an abandoned check abandon its work: every check
   * takes the number it is running under, and `stop()` moves it on. */
  let generation = 0
  /** expected holds the releases this policy has pointed the feed at and not yet
   * seen finish, so that a completion can be tied to a release the console
   * actually asked for. */
  const expected = new Set<string>()
  let readyDeferred: string | null = null
  /** installable is the version of the download this policy last accepted, and
   * the only version a restart may install: a press arrives with the version its
   * notice was opened with, and a check that landed in between can leave the app
   * holding a different download than the one the operator answered about. */
  let installable: string | null = null
  /** restartDeferred is the version an accepted press asked for while a launch
   * was in flight, re-checked against `installable` where it is delivered. */
  let restartDeferred: string | null = null
  /** live is whether a check has been asked for and not yet stopped. It is here
   * because the download event carries no generation: `stop()` abandons a check
   * in flight by moving `generation` on, but a download that began before the
   * app started quitting still arrives afterwards, and acting on it would open
   * the restart notice -- or install the update -- while the process is on its
   * way out. `checkNow()` arms it as well as `start()`, because the controller
   * can be driven by `checkNow` alone. */
  let live = false

  /** allowed is the gate, read fresh every time it matters: the switch or the
   * local opt-out can change between two ticks, and a check that started under
   * an open gate must not finish its work under a closed one. */
  const allowed = (): boolean =>
    deps.platform === 'darwin' &&
    deps.isPackaged &&
    (deps.env['CI'] ?? '') === '' &&
    deps.env['FUGARO_NO_UPDATE_CHECK'] !== '1' &&
    deps.switchOn()

  const clearTimer = (): void => {
    if (handle !== null) deps.timer.clearInterval(handle)
    handle = null
  }

  const superseded = (at: number): boolean => at !== generation || !allowed()

  /** downloadedVersion is the release a finished download is for, or null when
   * this policy cannot tie the event to one. The client's own `version` is
   * believed only when a feed was pointed at exactly that release: a check that
   * repointed the feed while an earlier download was still in flight leaves two
   * candidates, and naming the wrong one would offer the user a version whose
   * bytes are not on disk. */
  const downloadedVersion = (payload: unknown): string | null => {
    const said = payload !== null && typeof payload === 'object' ? (payload as Record<string, unknown>)['version'] : undefined
    if (typeof said === 'string') return expected.has(said) ? said : null
    if (expected.size !== 1) return null
    for (const only of expected) return only
    return null
  }

  /** onDownloaded hands the ready version to the app, unless a launch is in
   * flight: an update is not installed while a launch writes the run's record,
   * so the prompt waits and the download does not. A download that finishes
   * after the gate closed prompts nobody: `restartRequested` would refuse the
   * prompt's own button. */
  const onDownloaded = (payload: unknown): void => {
    // The app said stop: the bytes are on disk and stay there, but nothing about
    // a process on its way out prompts a user or replaces the app.
    if (!live) return
    // A download that finishes under a closed gate is refused where it lands:
    // the gate can close between the check that pointed the feed and the
    // download it started, and the tick that would notice is up to six hours
    // away. Nothing is installed on quit in this app either way, so the refusal
    // is the prompt's alone.
    if (!allowed()) return
    const version = downloadedVersion(payload)
    if (version === null) {
      // A download the policy cannot tie to the tag it pointed the feed at: the
      // zip's SHA-512 comes from `latest-mac.yml` alone, so the version that
      // manifest names has to be the tag this controller resolved, and one that
      // is not may be anything at all. It is not offered to the user.
      return
    }
    expected.delete(version)
    installable = version
    if (!deps.isLaunchFlowIdle()) {
      readyDeferred = version
      return
    }
    deps.onReadyToRestart(version)
  }

  /** configure writes the state the rules require onto the client, and
   * registers the listeners, before any check can start one: the adapter is
   * handed over as it comes, inheriting electron-updater's own defaults --
   * downgrades, pre-releases and a header of its own -- so a check that ran
   * without this would run with all three standing. */
  const configure = (): void => {
    if (configured) return
    configured = true
    deps.updater.autoDownload = true
    // Off, and it is the policy's statement of the owner's ruling of 2026-10-30:
    // an update is installed only through the notice's "Restart to update", so
    // the client's own install-on-quit -- `true` by default in 6.8.10 -- is
    // turned off before any check can run. Nothing here turns it on again, and
    // the adapter turns it off on the client it hands over as well, because a
    // policy that never configures (no check ever allowed) must still not leave
    // a downloading client armed to install on the way out.
    deps.updater.autoInstallOnAppQuit = false
    deps.updater.allowDowngrade = false
    deps.updater.allowPrerelease = false
    // Empty, and a plain object rather than a frozen one. Nothing in 6.8.10
    // writes into this table -- `addAuthHeader` reassigns the field and
    // `computeFinalHeaders` builds the object it hands out -- so a frozen `{}`
    // would not throw today, and freezing buys nothing an empty table does not
    // already say. It is left writable so that a version that does write in
    // place changes a header instead of throwing inside the library on every
    // check, which would read as an update client that never completes.
    deps.updater.requestHeaders = {}
    deps.updater.on('update-downloaded', onDownloaded)
    deps.updater.on('error', () => {
      console.error(CLIENT_FAILED)
    })
  }

  const checkNow = async (): Promise<void> => {
    if (!allowed()) return
    live = true
    configure()
    generation += 1
    const at = generation
    let body: string
    try {
      body = await deps.fetchReleases(RELEASES_URL, LISTING_HEADERS)
    } catch {
      console.error(LISTING_FAILED)
      return
    }
    if (superseded(at)) return
    if (Buffer.byteLength(body, 'utf8') > MAX_LISTING_BYTES) {
      console.error(LISTING_FAILED)
      return
    }
    let releases: unknown
    try {
      releases = JSON.parse(body) as unknown
    } catch {
      console.error(LISTING_FAILED)
      return
    }
    const tag = newestStableConsoleRelease(releases, deps.currentVersion)
    if (tag === null) return
    try {
      deps.updater.setFeedURL({ provider: 'generic', url: feedUrlFor(tag) })
      expected.add(tag.slice(CONSOLE_TAG_PREFIX.length))
      await deps.updater.checkForUpdates()
    } catch {
      console.error(CLIENT_FAILED)
    }
  }

  const tick = (): void => {
    if (!allowed()) {
      clearTimer()
      return
    }
    void checkNow()
  }

  return {
    start: async () => {
      if (!allowed()) return
      clearTimer()
      configure()
      handle = deps.timer.setInterval(tick, UPDATE_INTERVAL_MS)
      await checkNow()
    },
    stop: () => {
      live = false
      generation += 1
      clearTimer()
      // **The download is forgotten with the rest.** `stop()` is not only the
      // quitting process's: the switch going off stops this controller, and the
      // switch coming back on starts it again -- with the bytes of a download
      // the console did not choose in this run still on disk, and a notice that
      // promises them. A version kept across a stop is a version a later press
      // installs, so all three go: what is installable, and both deferrals that
      // exist to deliver it.
      installable = null
      readyDeferred = null
      restartDeferred = null
    },
    checkNow,
    restartRequested: (version: string) => {
      // Two refusals with no line between them: the app said stop and this is a
      // quitting process, or the gate closed and nothing here may still write to
      // the client. Neither records a deferral, so there is nothing left for a
      // later `launchFlowIdle()` to deliver -- the same two refusals
      // `launchFlowIdle` itself makes.
      if (!live || !allowed()) return
      if (version !== installable) {
        // The press is about the download its notice named, and this policy is
        // holding a different one -- or none: the bytes that would be installed
        // are not the bytes that were offered, and an update is installed only
        // through the notice the operator saw.
        console.error(RESTART_REFUSED)
        return
      }
      if (!deps.isLaunchFlowIdle()) {
        restartDeferred = version
        return
      }
      deps.updater.quitAndInstall()
    },
    launchFlowIdle: () => {
      // A flow that goes idle while the app is quitting delivers nothing: the
      // deferrals this holds are a prompt and an install, and both are for a
      // console that is still running.
      if (!live || !allowed() || !deps.isLaunchFlowIdle()) return
      const version = readyDeferred
      readyDeferred = null
      if (version !== null) deps.onReadyToRestart(version)
      const deferred = restartDeferred
      restartDeferred = null
      // The deferral is the version that was pressed, and the download is read
      // again here rather than trusted from where the press was recorded: a check
      // that landed while the flow was busy may have replaced it.
      if (deferred === null || deferred !== installable) return
      deps.updater.quitAndInstall()
    },
  }
}
