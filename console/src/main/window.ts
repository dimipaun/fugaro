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

import { APP_ENTRY_URL } from './protocol'

/** CONSOLE_WINDOW_TITLE is the window's title and the document's `<title>`. */
export const CONSOLE_WINDOW_TITLE = 'Fugaro console'

/** CONSOLE_WINDOW_SIZE is the console's opening size in CSS pixels. */
export const CONSOLE_WINDOW_SIZE = { width: 1180, height: 780 } as const

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
 * consoleWindowOptions is the window's construction options, webPreferences
 * included. `show: false` with a `ready-to-show` listener (see
 * `createConsoleWindow`) means the window is never visible half-drawn, which
 * matters more here than usual: the console's first paint is the only paint
 * that comes from the bundle, and everything after it comes over IPC.
 */
export function consoleWindowOptions(): BrowserWindowConstructorOptions {
  return {
    width: CONSOLE_WINDOW_SIZE.width,
    height: CONSOLE_WINDOW_SIZE.height,
    show: false,
    title: CONSOLE_WINDOW_TITLE,
    webPreferences: consoleWebPreferences(),
  }
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
 * ALLOWED_EXTERNAL_HOSTS is the whole of the allow-list for opening a URL in
 * the user's own browser, and it is exactly the three hosts rule 2 names: the
 * two providers' web hosts (`github.com`, `bitbucket.org` -- "both providers'
 * web hosts on the `openExternal` allow-list") and the Cloud console the
 * `logs RUN --url` link points into. Rule 2 also names "the docs", and no
 * document on this tree names a docs host, so none is on this list: a fourth
 * entry is a deliberate edit here and in the unit test that holds this list
 * exactly. Hosts are compared whole and lower-cased, so `github.com.evil.test`
 * and `evil-github.com` are not on it.
 *
 * Task 2.3 is the first caller that will display such a link; until then the
 * path exists and is tested, and rule 2's "no link is built" stands: there is
 * no caller that builds one.
 */
export const ALLOWED_EXTERNAL_HOSTS: readonly string[] = [
  'bitbucket.org',
  'console.cloud.google.com',
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
 * openExternalValidated is the one call to `shell.openExternal` in the console.
 * It returns whether it opened anything, so a caller can tell the user what
 * happened rather than pretending. Note that the URL that reaches the OS is the
 * one `validateExternalUrl` returned, not the argument.
 */
export async function openExternalValidated(shell: Shell, raw: string): Promise<boolean> {
  const decision = validateExternalUrl(raw)
  if (!decision.ok) return false
  await shell.openExternal(decision.url)
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
}

/**
 * createConsoleWindow builds the console's one window: the hardened options,
 * the three handlers on its `webContents`, every permission denied for the
 * session, and then the single load of the bundled document over `app://`. It
 * resolves once the document has loaded, so the caller knows the window is up.
 */
export async function createConsoleWindow(deps: ConsoleWindowDeps): Promise<BrowserWindow> {
  const window = new deps.BrowserWindow(consoleWindowOptions())

  installWebContentsSecurity(window.webContents)
  denyEveryPermission(deps.session)

  window.once('ready-to-show', () => {
    window.show()
  })
  await window.loadURL(APP_ENTRY_URL)

  return window
}
