// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The hardened window (desktop-console plan Task 1.1; design section 5.1 rules
// 1, 2 and 9).
//
// Everything the window is allowed to do is decided here, and the answers are
// all the same shape: no. The renderer has no Node, no remote module, no
// `<webview>`, no insecure content, no experimental features, no permissions of
// any kind, no navigation away from its own document, no `window.open`, and no
// way to open a URL outside the console unless that URL is `https:`, carries no
// credentials and no port, and names a host on the allow-list below. The
// console's own origin is `app://console` (see `protocol.ts`), which is the only
// origin the renderer ever runs on.
//
// As in `protocol.ts`, this module imports *types* only from Electron, so
// `test/unit/window.test.ts` can drive every one of those answers under plain
// Node, with fakes for the two objects that would otherwise need a running
// Electron: the window's `webContents` and the `session` and `shell` modules.
// The window itself is created in `createConsoleWindow`, which takes the
// Electron classes as arguments -- `src/main/index.ts` is the only caller.
//
// Task 3.2's version skew handshake lives at the bottom of this file, because
// "the console refuses to draw a live dashboard" is a statement about *this*
// window: below the hard floor `startupWindow` constructs no window at all, and
// the only window a refused console can open is the refusal screen's. The
// startup path is `startupWindow` -- read the CLI's version, place it, build the
// dashboard window only if the console will draw one -- and, since Task 3.3,
// `src/main/index.ts` calls `openStartupWindow`, which is that handshake
// **before any window exists** and then the one window the compatibility allows:
// the dashboard document, or `REFUSAL_ENTRY_URL`. (What the renderer *draws* in
// either document is still the screen-mounting work of a later task: this tree
// has no bundling step, so no document loads a script yet.)
//
// Two notes on what is deliberately NOT here, because both are later tasks and
// a reviewer should not have to guess:
//
//   - There is no preload and no IPC. The renderer of Task 1.1 draws a static
//     document; the `contextBridge` surface with the one narrow data path
//     arrives in Task 1.2, and it lands in a window that already has all of the
//     above switched off.
//   - The Electron fuses (Task 4.1) are not flipped yet, so this is the
//     JavaScript-level hardening plus the OS-level plan still to come. What is
//     here does not depend on the fuses being flipped, and the fuses do not
//     replace anything here.

import type {
  BrowserWindow,
  BrowserWindowConstructorOptions,
  Session,
  Shell,
  WebContents,
  WebPreferences,
} from 'electron'

import { SKEW_HEADLINES, classifyCliVersion, unreadableCompatibility } from '../shared/version'
import type { CliCompatibility } from '../shared/version'
import { APP_ENTRY_URL, REFUSAL_ENTRY_URL } from './protocol'
import type { BinaryResolution } from './spawn'

/** CONSOLE_WINDOW_TITLE is the window's title and the document's `<title>`. */
export const CONSOLE_WINDOW_TITLE = 'Fugaro console'

/** CONSOLE_WINDOW_SIZE is the console's opening size in CSS pixels. */
export const CONSOLE_WINDOW_SIZE = { width: 1180, height: 780 } as const

/**
 * CONSOLE_WINDOW_BACKGROUND is the colour the window paints **before the
 * document does**: `backgroundColor` in the options below, which is what the
 * platform shows in the frame between the window being created and the
 * stylesheet's first paint. Without it that frame is the platform's default --
 * white -- and the console's own ground arrives a moment later as a flash
 * (design section 4.1).
 *
 * **It is `--ground`, and it is a duplicate of it.** The main process cannot
 * read the stylesheet: `--ground` is declared in
 * `src/renderer/theme/palette.css` and this module has no way to resolve a
 * custom property, so the value is written out here and the equality is held by
 * a test rather than by the compiler -- `test/unit/theme-tokens.test.ts` reads
 * this literal out of `consoleWindowOptions()` and fails, by name, when the two
 * drift apart (design sections 4.1 and 6.3).
 *
 * **One literal, because one value set ships.** The owner's Q1 ruling of
 * 2026-10-19 settled that the console is dark-only (design section 3.6), so
 * there is exactly one ground for this to equal and no `nativeTheme` read and no
 * `updated` listener comes with it. The two-set design would have needed one
 * literal per set and the OS to choose between them -- and a single pinned
 * literal would then have flashed **dark** on a light machine, which is the very
 * flash this colour exists to remove; with one set that objection is gone, and
 * a later task that ships a light set has to come back to this constant.
 */
export const CONSOLE_WINDOW_BACKGROUND = '#0A0E16'

/**
 * consoleWebPreferences is design section 5.1 rule 1, flag by flag:
 *
 *   - `contextIsolation: true` -- the renderer's world and the preload's world
 *     (Task 1.2) are separate JavaScript contexts, so a renderer-side mistake
 *     cannot reach a preload-side function and `contextBridge` stays the only
 *     door between them;
 *   - `nodeIntegration: false` (and `nodeIntegrationInWorker: false`) -- no
 *     `require`, no `process`, no `Buffer` in the renderer, so a string that a
 *     job wrote and a template rendered is a string and not a capability;
 *   - `sandbox: true` -- the renderer runs in Chromium's sandbox, with the
 *     process-wide OS-level restrictions that gives it, independently of
 *     whether the fuses have been flipped;
 *   - `webviewTag: false` -- `<webview>` is not available, so the console
 *     cannot end up embedding a second, less hardened browser;
 *   - `allowRunningInsecureContent: false` -- an `https://` document of ours
 *     (there is none) could not pull in `http://` script;
 *   - `experimentalFeatures: false` -- no Chromium features that are not on by
 *     default, because the console has no use for one and every one of them is
 *     another thing to keep track of.
 *
 * The unit test asserts each of these by name and then asserts the returned
 * object has exactly these keys, so a weakening flag added in a later edit
 * fails the test rather than slipping in beside them.
 */
export function consoleWebPreferences(): WebPreferences {
  return {
    contextIsolation: true,
    nodeIntegration: false,
    nodeIntegrationInWorker: false,
    sandbox: true,
    webviewTag: false,
    allowRunningInsecureContent: false,
    experimentalFeatures: false,
  }
}

/**
 * dashboardWebPreferences is the dashboard window's preferences: the hardened
 * set above, and **exactly one key more** -- `preload` (desktop-console plan Task
 * 3.4a, step 1; design section 5.1 rules 1 and 5).
 *
 * Why the dashboard and not the refusal window: the refusal document has no
 * renderer entry to load and nothing to ask for, so a preload there would be a
 * bridge on a window that must draw nothing -- and D7's claim is about what a
 * refused console *has*. `buildConsoleWindow` below is where that difference is
 * decided, by the one thing that distinguishes the two windows, which is the
 * document they load.
 *
 * Why `preload` is a path and not a module: a sandboxed preload is loaded by
 * Electron from the file system, and the file is the compiled
 * `dist/preload/index.js` under the app's own path. The path is an argument
 * rather than a constant of this module for the same reason every other Electron
 * value in this file is: this module imports Electron *types* only, so
 * `test/unit/window.test.ts` can assert -- under plain Node -- that the key is
 * there, that it is absolute, and that it is inside the app's own directory, and
 * `src/main/index.ts` is the one place that knows where the app is.
 *
 * It is a **separate function** and not a parameter of `consoleWebPreferences`
 * for the reason the hardened set's comment gives: `consoleWebPreferences` is the
 * refusal window's set and the baseline this one adds to, and a caller that could
 * pass a preload into it could also pass one into a window that must not have
 * one. Spreading the one into the other keeps the hardened flags in one place:
 * this function cannot weaken a flag without editing the set it spreads.
 */
export function dashboardWebPreferences(preloadPath: string): WebPreferences {
  return { ...consoleWebPreferences(), preload: preloadPath }
}

/**
 * consoleWindowOptions is the window's construction options, webPreferences
 * included. `show: false` with a `ready-to-show` listener (see
 * `createConsoleWindow`) means the window is never visible half-drawn, which
 * matters more here than usual: the console's first paint is the only paint
 * that comes from the bundle, and everything after it comes over IPC.
 *
 * The preferences are a parameter with the hardened set as its default (Task
 * 3.4a): every caller that does not ask for the dashboard's preload gets exactly
 * what this function always built, and the dashboard's caller asks for it through
 * `dashboardWebPreferences`, which is the one place the extra key is added.
 *
 * `backgroundColor` is Task 6.5's (design section 4.1) and it is
 * `CONSOLE_WINDOW_BACKGROUND`, whose comment says why it is a literal this
 * module cannot resolve from the stylesheet and which test holds it equal to
 * `--ground`.
 */
export function consoleWindowOptions(
  compatibility: CliCompatibility | null = null,
  webPreferences: WebPreferences = consoleWebPreferences(),
): BrowserWindowConstructorOptions {
  return {
    width: CONSOLE_WINDOW_SIZE.width,
    height: CONSOLE_WINDOW_SIZE.height,
    show: false,
    backgroundColor: CONSOLE_WINDOW_BACKGROUND,
    title: consoleWindowTitle(compatibility),
    webPreferences,
  }
}

/**
 * consoleWindowTitle is the window's title, and it says which console this is
 * **and what it made of the CLI it is driving** (design section 3.4 rule 8: the
 * handshake "says so in its header"). Inside the range, and before a handshake
 * has run (`null`, which is what `src/main/index.ts` passes today), it is the
 * plain title.
 *
 * The title carries the *mode* and never the version the binary reported, and
 * that is deliberate rather than an omission: a window title is drawn by the
 * operating system's chrome and not by this console's writers, so it is not a
 * place `sanitize.ts` can reach, and a string a binary chose does not go into a
 * place the console's own rules cannot be applied. The version the CLI reported
 * is drawn in the document, by `renderVersionSkew`, through the sanitizer.
 */
export function consoleWindowTitle(compatibility: CliCompatibility | null): string {
  if (compatibility === null || compatibility.mode === 'ok') return CONSOLE_WINDOW_TITLE
  return `${CONSOLE_WINDOW_TITLE} — ${SKEW_HEADLINES[compatibility.mode]}`
}

/** NavigationEvent is the shape of Electron's cancellable event objects. */
export interface NavigationEvent {
  preventDefault(): void
}

/**
 * denyNavigation is the whole of the console's `will-navigate` policy: every
 * navigation is refused, including a navigation to `app://console/index.html`
 * itself. Rule 2 makes the window's single load the main process's business --
 * `loadURL(APP_ENTRY_URL)` in `createConsoleWindow` -- and there is nothing the
 * renderer can navigate to that it should be able to reach by asking, so the
 * handler does not have to distinguish the harmless case from the hostile one.
 * In-page fragment changes do not fire `will-navigate` and are unaffected.
 *
 * `_target` is the URL the renderer is trying to reach, and it is deliberately
 * not consulted: the answer is the same for a `javascript:` URL, a `file:` URL,
 * a remote origin and the console's own document, and a handler that never
 * reads the URL is a handler that cannot be talked into a special case. It is a
 * parameter rather than an omission so that the unit test can say so over a
 * table of URLs, one of them the console's own.
 */
export function denyNavigation(event: NavigationEvent, _target: string): void {
  event.preventDefault()
}

/**
 * denyWindowOpen is the whole of the `setWindowOpenHandler` policy: no popup,
 * no `_blank`, no window the renderer asked for, whatever the URL. A future
 * task that wants a link to open in the user's browser calls
 * `openExternalValidated` from the main process instead, which is the one path
 * that validates where it is going.
 */
export function denyWindowOpen(): { action: 'deny' } {
  return { action: 'deny' }
}

/**
 * denyWebviewAttach refuses a `<webview>` even though `webviewTag: false`
 * already means the renderer cannot create one: the flag and the handler guard
 * the same door from two sides, and the handler costs one line. A `<webview>`
 * that attached would be a browser inside the console with its own, weaker
 * preferences.
 */
export function denyWebviewAttach(event: NavigationEvent): void {
  event.preventDefault()
}

/**
 * installWebContentsSecurity puts those three answers on one `webContents`. It
 * runs from `web-contents-created` in the entry point as well as for the
 * console's own window, so it covers any `webContents` Electron creates,
 * including ones created before the window exists.
 */
export function installWebContentsSecurity(contents: WebContents): void {
  contents.on('will-navigate', (event, url) => denyNavigation(event, url))
  contents.on('will-attach-webview', (event) => denyWebviewAttach(event))
  contents.setWindowOpenHandler(() => denyWindowOpen())
}

/**
 * denyEveryPermission turns off every permission for a session, in both of the
 * ways Electron asks: the request handler (a page asking for the camera) and
 * the check handler (a page asking whether it may have it). The console has no
 * use for any of them -- rule 3's `connect-src 'none'` means it does not even
 * talk to the network -- and denying by default means a future feature has to
 * come here and name the one permission it needs.
 */
export function denyEveryPermission(session: Session): void {
  session.setPermissionRequestHandler((_contents, _permission, callback) => {
    callback(false)
  })
  session.setPermissionCheckHandler(() => false)
}

/**
 * denyPageTitleUpdate stops a loaded document from becoming the window's title.
 * It is the difference between `consoleWindowTitle` being a decision and being a
 * suggestion: the document defines a `<title>` (`src/renderer/index.html`, which
 * the unit test pins), and Electron applies a page's title over the `title` the
 * window was constructed with, so the mode -- reduced, refused -- would be
 * dropped on load in every real window. A window title is chrome the console
 * owns: the page it draws is the console's *content*, and the title that names
 * what the console made of the CLI is not the page's to overwrite.
 */
export function denyPageTitleUpdate(event: NavigationEvent): void {
  event.preventDefault()
}

/**
 * ALLOWED_EXTERNAL_HOSTS is the whole of the allow-list for opening a URL in
 * the user's own browser, and it is exactly four hosts: the two providers' web
 * hosts (`github.com`, `bitbucket.org` -- "both providers' web hosts on the
 * `openExternal` allow-list"), the Cloud console the `logs RUN --url` link
 * points into, and the website the console's own onboarding screens link to.
 * Rule 2 also names "the docs", and no document on this tree names a docs host,
 * so none is on this list: an entry is a deliberate edit here and in the unit
 * test that holds this list exactly. Hosts are compared whole and lower-cased,
 * so `github.com.evil.test` and `evil-github.com` are not on it.
 *
 * `fugaro.dev` is the fourth, and it is the console's own link rather than a
 * document's: the two onboarding screens (owner's ruling of 2026-10-20) offer a
 * link button to the website's getting-started page, whose URL is
 * `GETTING_STARTED_URL` in `src/main/onboarding.ts`. That constant lives in the
 * main process and never crosses the IPC boundary -- the picker's `openExternal`
 * operation takes **no arguments** -- so the check below is the second gate for
 * a link this process built, and no renderer can name one.
 *
 * Task 2.3 is the first caller that will display a document's link; until then
 * the path exists and is tested, and rule 2's "no link is built" stands for the
 * dashboard's documents: there is no caller that builds one.
 */
export const ALLOWED_EXTERNAL_HOSTS: readonly string[] = [
  'bitbucket.org',
  'console.cloud.google.com',
  'fugaro.dev',
  'github.com',
]

/** ExternalUrlDecision is `validateExternalUrl`'s answer. */
export type ExternalUrlDecision = { ok: true; url: string } | { ok: false; reason: string }

/**
 * validateExternalUrl is the only place a URL from outside the console is
 * looked at before it is handed to the operating system, and it is an
 * allow-list: `https:` only (so `javascript:`, `file:` and `data:` are refused
 * by the scheme test rather than by a list of forbidden ones), no credentials
 * in the URL, no explicit port, and the host must be on
 * `ALLOWED_EXTERNAL_HOSTS`. The URL is returned in its parsed, normalised form
 * (`url.href`), so what is opened is what was validated and not the string it
 * was spelled as.
 *
 * Rule 2 is explicit that this is **the second gate, never the first**: the
 * first is that no link is built from a field a job wrote -- the run's `pr_url`
 * is displayed as text and never opened, because no document on this tree says
 * which provider the run's repository is on. A host check that stands alone
 * would happily open a plausible-looking GitHub URL for a repository that is on
 * Bitbucket. This function is what a link passes through *after* it has been
 * constructed from the document's own fields (Task 2.3, once gap 7 lands), and
 * it is the only place anything reaches `shell.openExternal`.
 */
export function validateExternalUrl(raw: string): ExternalUrlDecision {
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    return { ok: false, reason: 'the link is not a URL' }
  }
  if (url.protocol !== 'https:') {
    return { ok: false, reason: 'only https: links are opened in the browser' }
  }
  if (url.username !== '' || url.password !== '') {
    return { ok: false, reason: 'the link carries credentials' }
  }
  if (url.port !== '') {
    return { ok: false, reason: 'the link names a port' }
  }
  if (!ALLOWED_EXTERNAL_HOSTS.includes(url.hostname)) {
    return { ok: false, reason: 'the host is not one the console opens in the browser' }
  }
  return { ok: true, url: url.href }
}

/**
 * PULL_REQUEST_ROUTES is the whole of what a provider may be, and the host and
 * the path segment each one's pull requests live under. It is two entries and
 * not a pattern: a provider the console does not know has no host to build on,
 * and a table is the only shape in which "which providers does this console
 * draw a link for" is one reviewable answer. A `Map` rather than an object
 * literal, so that a `provider` spelled `constructor` is a miss rather than a
 * prototype member. Both hosts are already on `ALLOWED_EXTERNAL_HOSTS`, and
 * neither is added to it here.
 */
const PULL_REQUEST_ROUTES: ReadonlyMap<string, { readonly host: string; readonly segment: string }> =
  new Map([
    ['github', { host: 'github.com', segment: 'pull' }],
    ['bitbucket', { host: 'bitbucket.org', segment: 'pull-requests' }],
  ])

/**
 * PULL_REQUEST_REPO_RE is the repository half of the builder: **exactly** the
 * two segments a repository has, each of them beginning with a letter or a
 * digit and continuing in the host's own name alphabet. It is stricter than the
 * `repository` kind the IPC schema accepts on purpose, and the strictness is
 * the whole point: a value that is already a path (`owner/name/pull/1`) or one
 * that walks (`owner/../evil`) is refused here, before a URL exists, rather
 * than being pasted into one.
 *
 * A segment cannot begin with `.`, so `.` and `..` are refused by the alphabet
 * and not by a list of forbidden names; there is exactly one `/` in the pattern,
 * so a third segment cannot appear; and every character that could end the
 * authority or start a query, a fragment or a userinfo (`:`, `@`, `?`, `#`, `%`,
 * `\`, a space) is outside the alphabet.
 */
const PULL_REQUEST_REPO_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*\/[A-Za-z0-9][A-Za-z0-9._-]*$/

/**
 * pullRequestUrl builds the one URL this console opens that comes from a
 * document's own fields (D814, D815), and it builds it **from three fields
 * rather than reading it**: a pull request document carries a `url`, and that
 * field is displayed as text and never handed to the browser, because a URL a
 * job wrote is not a fact about the repository (rule 2, reconciliation F2).
 *
 * `null` is the answer for everything else -- an unknown provider, a repository
 * that is not exactly `owner/name` in the host's alphabet, a number that is not
 * a positive safe integer -- and a `null` is a refusal that opens nothing.
 *
 * **This is the first gate and `validateExternalUrl` is the second**, which is
 * the order rule 2 states: the URL is built here and then handed to
 * `openExternalValidated`, so a host check never stands alone in front of a
 * string that came from somewhere else. The builder asks `validateExternalUrl`
 * for its own answer as well, so what it returns is provably a URL the console
 * would open, and a table entry above that drifted from the allow-list would
 * answer `null` here rather than reach the operating system.
 */
export function pullRequestUrl(provider: string, repo: string, number: number): string | null {
  const route = PULL_REQUEST_ROUTES.get(provider)
  if (route === undefined) return null
  if (!PULL_REQUEST_REPO_RE.test(repo)) return null
  if (!Number.isSafeInteger(number) || number < 1) return null
  const [owner, name] = repo.split('/')
  const decision = validateExternalUrl(
    `https://${route.host}/${owner}/${name}/${route.segment}/${String(number)}`,
  )
  return decision.ok ? decision.url : null
}

/**
 * openExternalValidated is the one call to `shell.openExternal` in the console.
 * It returns whether it opened anything, so a caller can tell the user what
 * happened rather than pretending. Note that the URL that reaches the OS is the
 * one `validateExternalUrl` returned, not the argument.
 *
 * **Three ways a link does not open, and all three answer `false`.** The URL is
 * refused by the allow-list above; the handover reaches the platform and the
 * platform declines it -- no application registered for `https:`, a prompt the
 * operator dismissed -- which Electron reports by **rejecting**; or it opens.
 * The middle one is caught here rather than allowed to travel, because a
 * rejection is not an answer this function's callers can use: both of them
 * await it inside an `ipcMain.handle` callback (`src/main/index.ts`'s
 * `openExternalFromShell` for the dashboard's `link.openExternal`, and the
 * picker's `openExternal`), and each of those catches an error by putting its
 * `message` in front of the operator -- so an uncaught rejection here would
 * draw the *operating system's* complaint ("Failed to open the link: ...")
 * where the console has a sentence of its own (`OPEN_EXTERNAL_FAILED`). The
 * platform's text is not a sentence this console writes and is not what its
 * screens are written to draw, so it stops here.
 */
export async function openExternalValidated(shell: Shell, raw: string): Promise<boolean> {
  const decision = validateExternalUrl(raw)
  if (!decision.ok) return false
  try {
    await shell.openExternal(decision.url)
  } catch {
    return false
  }
  return true
}

/**
 * ConsoleWindowDeps is the Electron surface `createConsoleWindow` needs: the
 * BrowserWindow class to construct with, and the session whose permissions are
 * denied. It is an argument rather than an import for the same reason as
 * everything else in this file -- the entry point is the only module that has
 * a running Electron to hand it one.
 */
export interface ConsoleWindowDeps {
  BrowserWindow: new (options: BrowserWindowConstructorOptions) => BrowserWindow
  session: Session
  /**
   * `preloadPath` is the compiled preload the **dashboard** window loads (Task
   * 3.4a), and it is required rather than optional: it is the one window member
   * that carries the console's whole IPC surface, and a default would make
   * forgetting it a console with no bridge at all -- which is a state the
   * renderer handles (a missing bridge is a refusal) but which nobody would have
   * chosen. The refusal window ignores it, by `buildConsoleWindow`'s decision
   * below and not by a caller's.
   */
  readonly preloadPath: string
}

/**
 * createConsoleWindow builds the console's one window: the hardened options,
 * the three handlers on its `webContents`, every permission denied for the
 * session, and then the single load of the bundled document over `app://`. It
 * resolves once the document has loaded, so the caller knows the window is up.
 */
export async function createConsoleWindow(deps: ConsoleWindowDeps): Promise<BrowserWindow> {
  return buildConsoleWindow(deps, null, APP_ENTRY_URL)
}

/** buildConsoleWindow is the one place a console window is constructed, so
 * every window this app ever opens -- the dashboard's and the refusal screen's
 * -- has the same preferences, the same handlers, the same denied permissions
 * and the same single load.
 *
 * `entryUrl` is the document that single load is for, and it is a parameter
 * rather than a constant (Task 3.3): the dashboard document is the dashboard's,
 * and the refusal's is `REFUSAL_ENTRY_URL`. Which one a window got is the D7
 * claim itself and the thing an e2e assertion can read off a running window
 * (`page.url()`), so it is decided by the caller's door and not by anything the
 * renderer could influence.
 *
 * **The document also decides the preload** (Task 3.4a): the dashboard document
 * gets `dashboardWebPreferences(deps.preloadPath)` and the refusal document gets
 * the plain hardened set, with no `preload` key at all. It is the same `entryUrl`
 * that decides both, and it is checked here rather than passed in as a second
 * flag, so "the refusal window has no bridge" is a property of the refusal
 * window's *document* and cannot be got wrong by a caller that meant the other
 * window: the two windows differ in exactly one thing, and this is where that
 * one thing is read. */
async function buildConsoleWindow(
  deps: ConsoleWindowDeps,
  compatibility: CliCompatibility | null,
  entryUrl: string,
): Promise<BrowserWindow> {
  const webPreferences =
    entryUrl === APP_ENTRY_URL ? dashboardWebPreferences(deps.preloadPath) : consoleWebPreferences()
  return buildHardenedWindow(deps, consoleWindowOptions(compatibility, webPreferences), entryUrl)
}

/**
 * buildHardenedWindow is the one construction path every window of this app
 * goes through -- the dashboard's, the refusal screen's and, since Task 3.4b,
 * the installation picker's (`src/main/picker.ts`): the options it is given, the
 * three denials on its `webContents`, every permission denied for the session,
 * the page refused the title, shown only when ready, and then the single load.
 *
 * The options are the caller's, and that is the only thing that differs between
 * windows: each caller builds them from `consoleWebPreferences()` (plus at most
 * a `preload`), so the hardened flags stay in one place. `beforeLoad` runs after
 * construction and before the load, which is where a caller that must hold the
 * window while its document loads (the picker, whose `closed` listener drops its
 * pick tokens) attaches its own listeners.
 */
export async function buildHardenedWindow(
  deps: { readonly BrowserWindow: new (options: BrowserWindowConstructorOptions) => BrowserWindow; readonly session: Session },
  options: BrowserWindowConstructorOptions,
  entryUrl: string,
  beforeLoad?: (window: BrowserWindow) => void,
): Promise<BrowserWindow> {
  const window = new deps.BrowserWindow(options)

  installWebContentsSecurity(window.webContents)
  denyEveryPermission(deps.session)

  // The document may not take the window's title over. Electron applies the
  // page's `<title>` to the window once the document loads, and Electron's own
  // typings say that when the HTML defines one the `title` option "will be
  // ignored" -- `src/renderer/index.html` defines one, so without this handler
  // the mode `consoleWindowTitle` puts in the title would never reach a real
  // window, and a reduced or refused console would show the plain title. The
  // header saying which console this is and what it made of the CLI is design
  // section 3.4 rule 8's, and a page cannot be the thing that erases it.
  window.on('page-title-updated', (event) => {
    denyPageTitleUpdate(event)
  })

  window.once('ready-to-show', () => {
    window.show()
  })
  beforeLoad?.(window)
  await window.loadURL(entryUrl)

  return window
}

// ---------------------------------------------------------------------------
// The version skew handshake (desktop-console plan Task 3.2; design section 7.1
// and D7).
// ---------------------------------------------------------------------------

/**
 * HandshakeFailure is the closed set of reasons a version read can fail for,
 * and it is a *category*, never a sentence: round 2 of the Task 3.3 review found
 * the first version carrying `resolution.reason` into the refusal, and that
 * reason embeds what the binary printed (`parseVersionLine`'s "the binary
 * printed "<line>", which is not a version") and the CLI's stderr -- so a
 * hostile stub's line reached `compatibility.because` verbatim. Plan step 3:
 * "the raw unparsable line is never carried". A category cannot carry a line.
 *
 *   - `not-found`: no candidate on rule 4's list is an executable `fugaro`;
 *   - `unreadable`: a binary was found and did not answer `version` with a line
 *     the console could read (an unparsable line, a non-zero exit, a signal, a
 *     timeout -- the resolver's reason says which, and that reason stays in the
 *     main process: it is the CLI's words as much as the console's).
 */
export type HandshakeFailure = 'not-found' | 'unreadable'

/** HANDSHAKE_NOT_FOUND is the console's sentence for `not-found`. */
export const HANDSHAKE_NOT_FOUND = 'the console found no fugaro binary to read'

/** HANDSHAKE_UNREADABLE is the console's own reason when a binary was found and
 * its version could not be read, or the read itself threw -- a spawn that
 * failed, a timeout. It is a sentence of this console's, never the child's
 * output, and it is the `because` of the compatibility `runVersionHandshake`
 * builds, so the refusal screen draws it in the reason cell (`p.skew-because`)
 * under the console's own skew sentence. */
export const HANDSHAKE_UNREADABLE = 'the console could not read which fugaro it is driving'

/** HANDSHAKE_SENTENCES is the whole of what a failed read can say: one fixed
 * sentence per category, and nothing else is ever a `because`. */
export const HANDSHAKE_SENTENCES: Readonly<Record<HandshakeFailure, string>> = Object.freeze({
  'not-found': HANDSHAKE_NOT_FOUND,
  unreadable: HANDSHAKE_UNREADABLE,
})

/**
 * VersionRead is the reader's answer: the line `fugaro version` wrote, or the
 * category of failure. It is required to be a *reader* and not an import
 * because this module holds no spawner -- `src/main/spawn.ts` owns
 * `resolveBinary`/`probeVersion` (Task 1.3) and the console's rule that a binary
 * whose version cannot be read is a binary it does not run.
 */
export type VersionRead =
  | { readonly ok: true; readonly stdout: string }
  | { readonly ok: false; readonly failure: HandshakeFailure }

/**
 * versionReadFrom turns rule 4's resolution into the handshake's read. The
 * resolver's `reason` is deliberately **dropped**: it is a sentence built in
 * part from the child's own output, and only the category crosses into the
 * compatibility. `path === null` is the resolver's "no candidate was
 * executable"; a non-null path is a binary that was found and failed its probe.
 */
export function versionReadFrom(resolution: BinaryResolution): VersionRead {
  if (resolution.ok) return { ok: true, stdout: resolution.version }
  return { ok: false, failure: resolution.path === null ? 'not-found' : 'unreadable' }
}

/** handshakeSentence is the fixed sentence for a category, and the unreadable
 * one for anything that is not on the closed list -- a reader that answered
 * outside its type still produces no text of its own. */
function handshakeSentence(failure: unknown): string {
  if (failure === 'not-found' || failure === 'unreadable') return HANDSHAKE_SENTENCES[failure]
  return HANDSHAKE_UNREADABLE
}

/** VersionHandshakeDeps is the Electron-free surface the handshake needs: one
 * function that reads `fugaro version`'s stdout. */
export interface VersionHandshakeDeps {
  readonly readVersion: () => Promise<VersionRead>
}

/**
 * runVersionHandshake reads the CLI's version and places it in the supported
 * range. It is the startup path of design section 6.3: the console either
 * proceeds, proceeds in a reduced mode, or refuses -- and the refusal is only
 * this value here; `startupWindow` below is where it becomes real.
 *
 * A reader that *throws* is a refusal and not a crash: a console that cannot
 * start because a subprocess misbehaved would be worse than one that says so,
 * and a CLI whose line cannot be read is one the console will not draw a
 * dashboard for (D7). **An unplaceable line is not that case**: a `dev` build is
 * *recognised*, so it is reduced rather than refused -- the dashboard is built,
 * and the views whose figures nothing vouched for are withheld (`classifyCliVersion`
 * in `src/shared/version.ts`, and the plan's open item 3 as the owner answered it
 * on 2026-10-22).
 */
export async function runVersionHandshake(deps: VersionHandshakeDeps): Promise<CliCompatibility> {
  try {
    const read = await deps.readVersion()
    if (read.ok) return classifyCliVersion(read.stdout)
    return unreadableCompatibility(handshakeSentence(read.failure))
  } catch {
    return unreadableCompatibility(HANDSHAKE_UNREADABLE)
  }
}

/** ConsoleStartup is what startup produces: the window (or `null`, which is the
 * refusal), and what the console made of the CLI it read. */
export interface ConsoleStartup {
  /**
   * `window` is `null` when the console refused: **no window is constructed**,
   * so there is no dashboard to draw and nothing to misdraw spend into. That is
   * stronger than a flag a renderer could ignore, and it is the plan's red step
   * -- "the below-floor test against a console that draws the dashboard anyway"
   * -- held where drawing is actually decided.
   */
  readonly window: BrowserWindow | null
  readonly compatibility: CliCompatibility
}

/**
 * StartupWindowDeps is the window surface plus the version reader, and the one
 * callback that runs when the handshake has an answer (Task 3.4a).
 *
 * `onDecided` is a *dep* and not a return value because of *when* it has to run:
 * after the version read and **before the dashboard document is loaded**, which
 * is the only moment at which the IPC handlers can be registered without a window
 * existing that could ask for something nobody answered. `startupWindow` below is
 * where that ordering lives, and it is the reason this callback is threaded
 * through the window-building path rather than called by the entry point after
 * `lifecycle.start()` resolved -- by then the dashboard is already up and its
 * renderer has already asked for the handshake.
 *
 * It is optional so that the unit tests that drive a single window (and the
 * callers that only want one) need no callback, and it is called for **every**
 * mode: a refused console's callback is what records the answer and decides, in
 * `src/main/index.ts`, to register nothing.
 */
export interface StartupWindowDeps extends ConsoleWindowDeps, VersionHandshakeDeps {
  readonly onDecided?: (compatibility: CliCompatibility) => void
}

/**
 * startupWindow is the console's startup in one call: read the CLI's version,
 * place it, and build the window only if the console will draw a dashboard in
 * it.
 *
 * On the refusal path there is deliberately **no window yet**, and the caller is
 * the one that decides between two honest options: open nothing, or open a
 * window whose only content is the refusal screen -- which is
 * `createRefusalWindow`, the door below. What the caller may not do is
 * `createConsoleWindow` after a refusal: that path is not on this function's
 * return value, so a wiring mistake shows up as a refusal screen or as no window
 * at all rather than as a spend figure nothing vouched for.
 */
export async function startupWindow(deps: StartupWindowDeps): Promise<ConsoleStartup> {
  const compatibility = await runVersionHandshake(deps)
  // The one moment the answer exists and no window does (Task 3.4a). For the
  // dashboard it is the last moment the handlers can be registered *before* a
  // renderer can ask for one, and for a refusal it is the moment the entry point
  // learns there is nothing to register.
  deps.onDecided?.(compatibility)
  if (compatibility.mode === 'refused') return { window: null, compatibility }
  return {
    window: await buildConsoleWindow(deps, compatibility, APP_ENTRY_URL),
    compatibility,
  }
}

/**
 * createRefusalWindow builds the window a refused console is allowed: the same
 * hardened window as the dashboard's -- same preferences, same handlers, same
 * denied permissions, same single load -- with a title that says which console
 * this is and that it refused. The renderer draws `renderVersionSkew` in it
 * (`src/shared/version.ts`) instead of a dashboard, and draws no `live` root at
 * all.
 *
 * It is a named door rather than a `createConsoleWindow` with an argument so
 * that the refusal is a decision a reviewer can find, and so that the only
 * caller that can draw a *dashboard* is the one that got a window from
 * `startupWindow`.
 */
export async function createRefusalWindow(
  deps: ConsoleWindowDeps,
  compatibility: CliCompatibility,
): Promise<BrowserWindow> {
  return buildConsoleWindow(deps, compatibility, REFUSAL_ENTRY_URL)
}

// ---------------------------------------------------------------------------
// The startup path, in one place (desktop-console plan Task 3.3).
// ---------------------------------------------------------------------------

/**
 * openWindowFor opens the one window a compatibility allows: the refusal
 * screen's when the console refused, the dashboard's when it did not (in the
 * reduced mode it is still the dashboard document, with the banner the renderer
 * draws and the three views withheld).
 *
 * It is the *only* function that chooses between the two doors, so "a refused
 * console never opens the dashboard" is one branch a reader can check, and it is
 * the function `activate` re-enters: a window closed on macOS comes back as the
 * window the compatibility that was already read calls for, and the handshake is
 * not read again.
 *
 * The dashboard door is built through `buildConsoleWindow` with **that same
 * compatibility**, and not through `createConsoleWindow`, which is the door for
 * "no handshake has run" (`null`) and carries the console's plain title. The
 * difference is the reduced mode: its window says which console it is, in the
 * title, and a window that came back after a dock-icon click must not be the one
 * place where that sentence goes missing -- the mode did not change just because
 * the window was closed and reopened.
 */
export async function openWindowFor(
  deps: ConsoleWindowDeps,
  compatibility: CliCompatibility,
): Promise<BrowserWindow> {
  if (compatibility.mode === 'refused') return createRefusalWindow(deps, compatibility)
  return buildConsoleWindow(deps, compatibility, APP_ENTRY_URL)
}

/**
 * openStartupWindow is the console's startup: the handshake first, and only
 * then a window -- the dashboard's if the console may draw one, the refusal
 * screen's if it may not.
 *
 * **The order is the point** (Task 3.3's first step): the version read happens
 * before any window exists, so there is no window at all during the read, and a
 * window is built only for a compatibility that has already been decided. A
 * caller that wants the two halves apart -- to open no window at all on a
 * refusal, say -- still has `startupWindow` and `createRefusalWindow`; this
 * function is what `src/main/index.ts` calls, and it is here rather than there
 * because `index.ts` imports the Electron *runtime* (it cannot be driven under
 * plain Node, which is how `test/unit/startup.test.ts` holds this order), while
 * this module imports Electron *types* only.
 */
export async function openStartupWindow(
  deps: StartupWindowDeps,
): Promise<ConsoleStartup> {
  const startup = await startupWindow(deps)
  if (startup.window !== null) return startup
  return {
    window: await openWindowFor(deps, startup.compatibility),
    compatibility: startup.compatibility,
  }
}

/**
 * ConsoleLifecycleDeps is what the console's lifecycle needs from the entry
 * point: the window surface (a function, because Electron's `session` does not
 * exist until the app is ready), the version reader, and how many windows are
 * open right now.
 */
export interface ConsoleLifecycleDeps {
  readonly windowDeps: () => ConsoleWindowDeps
  readonly readVersion: () => Promise<VersionRead>
  readonly openWindowCount: () => number
  /**
   * `onDecided` is handed to the startup and runs the moment the handshake has an
   * answer, before any window is built (Task 3.4a). `activate` does **not** call
   * it again: the answer it stored is the one it already reported, and a second
   * registration is what this callback's caller would do twice if it were called
   * per window. `choose` (Task 3.4b) does call it, once per new answer, because a
   * new installation's answer is a new decision; the caller's registration is
   * once-only on its own side.
   */
  readonly onDecided?: (compatibility: CliCompatibility) => void
}

/** ConsoleLifecycle is the startup and the `activate` handler, as two calls. */
export interface ConsoleLifecycle {
  /** start runs the handshake and opens the window it allows -- **once**:
   * every later call answers the same promise. */
  start(): Promise<CliCompatibility>
  /** activate is macOS's dock-icon click: a window for the stored answer when
   * none is open, and nothing otherwise. */
  activate(): Promise<void>
  /**
   * choose is a new answer for a newly chosen installation (Task 3.4b, step 5):
   * the picker has already run that installation's handshake and refused to
   * choose a `refused` one. It waits for the startup, replaces the stored answer,
   * hands it to `onDecided` -- the same callback the startup ran, so the surface
   * is registered if it was not yet and never twice -- and reopens the console's
   * window through `openWindowFor`, closing the one it replaces. A
   * refused-to-ok switch therefore replaces the refusal window with the
   * dashboard; the reverse is not a door this offers, because the picker does
   * not choose a refused installation.
   */
  choose(compatibility: CliCompatibility): Promise<void>
}

/**
 * createConsoleLifecycle is the entry point's state, moved here so it can be
 * driven under plain Node (`test/unit/startup.test.ts`), which `index.ts`
 * cannot be: it imports the Electron runtime.
 *
 * Two rules, both from round 2 of the Task 3.3 review:
 *
 *   - **the startup is single-flight.** The handshake's promise is stored the
 *     moment it starts, so an `activate` that fires while it is still running
 *     waits for *that* startup -- whose own window is the one that opens --
 *     rather than starting a second handshake and a second window. The first
 *     version kept only the *answer*, so `activate` before the answer saw
 *     "nothing decided" and ran `startConsole` again;
 *   - **`activate` opens the window the stored answer allows**, through
 *     `openWindowFor` and never through a dashboard door of its own, so a
 *     refused console comes back as the refusal screen, and only when no
 *     window is open.
 */
export function createConsoleLifecycle(deps: ConsoleLifecycleDeps): ConsoleLifecycle {
  let startup: Promise<CliCompatibility> | null = null
  let decided: CliCompatibility | null = null
  // shown is the console window this lifecycle opened last (the dashboard or the
  // refusal screen, never the picker's), so `choose` closes the one it replaces.
  let shown: BrowserWindow | null = null

  const start = (): Promise<CliCompatibility> => {
    if (startup === null) {
      startup = openStartupWindow({
        ...deps.windowDeps(),
        readVersion: deps.readVersion,
        ...(deps.onDecided === undefined ? {} : { onDecided: deps.onDecided }),
      }).then(
        (opened) => {
          decided = opened.compatibility
          shown = opened.window
          return opened.compatibility
        },
      )
    }
    return startup
  }

  const activate = async (): Promise<void> => {
    // Not started: the entry point's `main` starts it once the app is ready and
    // the bundle is served, and the window that startup opens is the one this
    // click asked for. Starting it from here could run before the protocol
    // handler exists.
    if (startup === null) return
    if (decided === null) {
      // The handshake is running: wait for *that* startup, whose window is the
      // one this click asked for. Never a second handshake, never a second
      // window.
      await startup
      return
    }
    // A window is constructed synchronously inside `openWindowFor` (before its
    // first `await`), so a second click in the same turn already sees it here.
    if (deps.openWindowCount() > 0) return
    shown = await openWindowFor(deps.windowDeps(), decided)
  }

  // choosing is the chain every `choose` runs on (review S6). Each one reads
  // `previous = shown` and then awaits the new window; two of them interleaved
  // would both capture the same old window and leave **two** dashboards open.
  // Run one after the other, each closes the window the one before it opened.
  let choosing: Promise<void> = Promise.resolve()

  const chooseNow = async (compatibility: CliCompatibility): Promise<void> => {
    if (startup !== null) await startup
    decided = compatibility
    deps.onDecided?.(compatibility)
    const previous = shown
    shown = await openWindowFor(deps.windowDeps(), compatibility)
    if (previous !== null && !previous.isDestroyed()) previous.close()
  }

  const choose = (compatibility: CliCompatibility): Promise<void> => {
    const next = choosing.then(
      () => chooseNow(compatibility),
      () => chooseNow(compatibility),
    )
    choosing = next.catch(() => undefined)
    return next
  }

  return { start, activate, choose }
}
