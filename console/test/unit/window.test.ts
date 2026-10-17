// The hardened window, held to its own rules (desktop-console plan Task 1.1;
// design section 5.1 rules 1, 2 and 3, and D14).
//
// Every property this file asserts is one a mutation can break, and each one is
// written as its own `it` so that a mutation names the test that caught it
// rather than failing a crowd:
//
//   - `contextIsolation: false`, `nodeIntegration: true`, `sandbox: false`,
//     `webviewTag: true`, `allowRunningInsecureContent: true` or
//     `experimentalFeatures: true` in `consoleWebPreferences` fails the flag
//     table below, and *any* extra key fails the "exactly those flags" test;
//   - a `will-navigate` handler that allows anything, a window-open handler
//     that returns `{ action: 'allow' }` or an attached `<webview>` fails the
//     three handler tests and the wiring test;
//   - a permission handler that calls back `true`, or a permission *check*
//     handler that returns `true`, fails the two halves of the permission test;
//   - `validateExternalUrl` accepting a `javascript:`, `file:` or `data:` URL
//     fails its table; accepting `https://github.com.evil.test`, a URL with
//     credentials or one with a port fails the three entries that name them;
//     and `openExternalValidated` calling `openExternal` on a refused URL fails
//     the test that counts the calls;
//   - the protocol handler serving a path outside the bundle root, serving a
//     request over another scheme or host, or dropping the CSP from a response
//     fails the request table, which asserts the status *and* the reason for
//     each refusal;
//   - `registerSchemesAsPrivileged` without `standard`, without `secure`, or
//     with `bypassCSP` fails the registration test;
//   - a CSP that loses `connect-src 'none'` fails the directive test, and a
//     `src/renderer/index.html` whose `<meta>` copy drifts from the header by
//     one byte fails the document test.
//
// Nothing here loads Electron. That is the reason `src/main/window.ts` and
// `src/main/protocol.ts` import types only and take the Electron runtime as an
// argument: under `vitest`, `require('electron')` is a path string rather than a
// process, so a module that imported it could not be tested here at all, and the
// fakes below (a fake `webContents`, a fake `session`, a fake `protocol` module
// and a fake BrowserWindow class) are the whole of the substitution. The real
// thing is exercised in `test/e2e/security.spec.ts`.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import type {
  BrowserWindow,
  BrowserWindowConstructorOptions,
  Protocol,
  Session,
  Shell,
  WebContents,
  WebPreferences,
} from 'electron'

import {
  APP_ENTRY_URL,
  APP_ORIGIN,
  APP_SCHEME,
  CONTENT_SECURITY_POLICY,
  CSP_HEADER,
  handleAppRequests,
  hasDotSegment,
  isInsideBundleRoot,
  mediaTypeFor,
  registerAppScheme,
  resolveBundleRequest,
  serveAppRequest,
} from '../../src/main/protocol'
import {
  ALLOWED_EXTERNAL_HOSTS,
  CONSOLE_WINDOW_TITLE,
  HANDSHAKE_UNREADABLE,
  consoleWebPreferences,
  consoleWindowOptions,
  consoleWindowTitle,
  createConsoleWindow,
  createRefusalWindow,
  denyEveryPermission,
  denyNavigation,
  denyWebviewAttach,
  denyWindowOpen,
  installWebContentsSecurity,
  openExternalValidated,
  runVersionHandshake,
  startupWindow,
  validateExternalUrl,
} from '../../src/main/window'
import {
  CLI_VERSION_BUILT_AGAINST,
  SKEW_HEADLINES,
  classifyCliVersion,
  hidesView,
  unreadableReason,
} from '../../src/shared/version'
import { githubToken } from '../support/fakeSecrets'

// The two files of the bundle, read from this test's own location rather than
// from the working directory: the protocol handler's root is the source tree
// (`rendererRoot` in src/main/index.ts), so what is served here is exactly what
// is served by the running app.
const bundleRoot = fileURLToPath(new URL('../../src/renderer', import.meta.url))
const indexHTMLPath = fileURLToPath(new URL('../../src/renderer/index.html', import.meta.url))
const indexHTML = readFileSync(indexHTMLPath, 'utf8')

// The design's own CSP string, section 5.1 rule 3, copied by hand rather than
// built from the constant it is compared against: a test that assembled its
// expectation from the implementation would agree with any implementation.
const DESIGN_CSP =
  "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'"

type RecordedEvent = {
  readonly prevented: boolean
  preventDefault(): void
}

/** recordingEvent is a stand-in for Electron's cancellable event objects. */
function recordingEvent(): RecordedEvent {
  let prevented = false
  return {
    get prevented() {
      return prevented
    },
    preventDefault() {
      prevented = true
    },
  }
}

type FakeWebContents = {
  value: WebContents
  emit(event: string, ...args: unknown[]): void
  windowOpenHandler(): (() => unknown) | null
}

/** fakeWebContents records the listeners `installWebContentsSecurity` adds. */
function fakeWebContents(): FakeWebContents {
  const listeners = new Map<string, (...args: unknown[]) => void>()
  let openHandler: (() => unknown) | null = null
  const contents = {
    on(event: string, listener: (...args: unknown[]) => void) {
      listeners.set(event, listener)
    },
    setWindowOpenHandler(handler: () => unknown) {
      openHandler = handler
    },
  }
  return {
    value: contents as unknown as WebContents,
    emit(event: string, ...args: unknown[]) {
      listeners.get(event)?.(...args)
    },
    windowOpenHandler: () => openHandler,
  }
}

type FakeSession = {
  value: Session
  requestHandler(): ((contents: unknown, permission: string, cb: (granted: boolean) => void) => void) | null
  checkHandler(): (() => boolean) | null
}

/** fakeSession records both of the handlers `denyEveryPermission` installs. */
function fakeSession(): FakeSession {
  type RequestHandler = (
    contents: unknown,
    permission: string,
    callback: (granted: boolean) => void,
  ) => void
  let request: RequestHandler | null = null
  let check: (() => boolean) | null = null
  const session = {
    setPermissionRequestHandler(handler: RequestHandler) {
      request = handler
    },
    setPermissionCheckHandler(handler: () => boolean) {
      check = handler
    },
  }
  return {
    value: session as unknown as Session,
    requestHandler: () => request,
    checkHandler: () => check,
  }
}

describe('the window is hardened, flag by flag (design section 5.1 rule 1)', () => {
  const flags: ReadonlyArray<readonly [keyof WebPreferences, boolean]> = [
    ['contextIsolation', true],
    ['nodeIntegration', false],
    ['nodeIntegrationInWorker', false],
    ['sandbox', true],
    ['webviewTag', false],
    ['allowRunningInsecureContent', false],
    ['experimentalFeatures', false],
  ]

  it.each(flags)('sets %s to %s', (flag, value) => {
    expect(consoleWebPreferences()[flag]).toBe(value)
  })

  it('sets exactly those flags and no others', () => {
    // The stronger half: a weakening flag *added* beside the right ones is the
    // failure mode the truth table alone would not see.
    expect(Object.keys(consoleWebPreferences()).toSorted()).toEqual(
      flags.map(([flag]) => flag).toSorted(),
    )
  })

  it('builds the window with those preferences and no preload yet', () => {
    const options = consoleWindowOptions()
    expect(options.webPreferences).toEqual(consoleWebPreferences())
    expect(options.title).toBe(CONSOLE_WINDOW_TITLE)
    // Shown only once it has painted, so the console is never on screen half
    // drawn (`createConsoleWindow`'s `ready-to-show` listener).
    expect(options.show).toBe(false)
    // The preload and its `contextBridge` surface are Task 1.2's; asserting the
    // absence here is what makes it a decision rather than an oversight.
    expect(options.webPreferences?.preload).toBeUndefined()
  })
})

describe('every way out of the window is closed (design section 5.1 rule 2)', () => {
  const navigations = [
    'https://example.invalid/fugaro',
    'http://example.invalid/fugaro',
    'javascript:alert(document.cookie)',
    'data:text/html,<h1>hello</h1>',
    'file:///etc/passwd',
    APP_ENTRY_URL,
    `${APP_ORIGIN}/other.html`,
  ]

  it.each(navigations)('refuses a will-navigate to %s', (target) => {
    const event = recordingEvent()
    denyNavigation(event, target)
    expect(event.prevented).toBe(true)
  })

  it('refuses every window.open', () => {
    expect(denyWindowOpen()).toEqual({ action: 'deny' })
  })

  it('refuses a webview attach', () => {
    const event = recordingEvent()
    denyWebviewAttach(event)
    expect(event.prevented).toBe(true)
  })

  it('puts all three on a webContents, and they deny when they are reached', () => {
    const contents = fakeWebContents()
    installWebContentsSecurity(contents.value)

    const navigate = recordingEvent()
    contents.emit('will-navigate', navigate, 'https://example.invalid/fugaro')
    expect(navigate.prevented).toBe(true)

    const attach = recordingEvent()
    contents.emit('will-attach-webview', attach, {}, {})
    expect(attach.prevented).toBe(true)

    const handler = contents.windowOpenHandler()
    expect(typeof handler).toBe('function')
    expect(handler?.()).toEqual({ action: 'deny' })
  })

  it('denies every permission, in both of the ways Electron asks', () => {
    const session = fakeSession()
    denyEveryPermission(session.value)

    const request = session.requestHandler()
    const check = session.checkHandler()
    expect(typeof request).toBe('function')
    expect(typeof check).toBe('function')

    for (const permission of ['camera', 'microphone', 'geolocation', 'notifications', 'media']) {
      let granted = true
      request?.(null, permission, (answer) => {
        granted = answer
      })
      expect(granted, permission).toBe(false)
    }
    expect(check?.()).toBe(false)
  })
})

describe('the one path to the user’s browser is an allow-list (rule 2, D14)', () => {
  const refused: ReadonlyArray<readonly [string, string]> = [
    ['javascript:alert(document.cookie)', 'only https: links are opened in the browser'],
    ['file:///etc/passwd', 'only https: links are opened in the browser'],
    ['data:text/html,<h1>hello</h1>', 'only https: links are opened in the browser'],
    ['http://github.com/fugaro/fugaro', 'only https: links are opened in the browser'],
    ['mailto:owner@example.invalid', 'only https: links are opened in the browser'],
    ['chrome://settings', 'only https: links are opened in the browser'],
    ['https://evil.invalid/fugaro/fugaro', 'the host is not one the console opens in the browser'],
    ['https://github.com.evil.invalid/fugaro', 'the host is not one the console opens in the browser'],
    ['https://evil-github.com/fugaro', 'the host is not one the console opens in the browser'],
    ['https://console.cloud.google.com.evil.invalid/run', 'the host is not one the console opens in the browser'],
    ['https://owner:token@github.com/fugaro/fugaro', 'the link carries credentials'],
    ['https://github.com:8443/fugaro/fugaro', 'the link names a port'],
    ['not a url at all', 'the link is not a URL'],
    ['', 'the link is not a URL'],
  ]

  it.each(refused)('refuses %s', (link, reason) => {
    expect(validateExternalUrl(link)).toEqual({ ok: false, reason })
  })

  // The three hosts rule 2 names: GitHub, Bitbucket Cloud, and the Cloud
  // console that `fugaro logs RUN --url` points into (the same host
  // internal/cli's own log-link test builds).
  const accepted = [
    'https://github.com/fugaro/fugaro/pull/434',
    'https://bitbucket.org/edgeappinc/fugarosandbox/pull-requests/1',
    'https://console.cloud.google.com/run/jobs/executions/details/us-east5/web-00001-abcd?project=proj-1234',
  ]

  it.each(accepted)('opens %s', (link) => {
    expect(validateExternalUrl(link)).toEqual({ ok: true, url: link })
  })

  it('is an allow-list of exactly those hosts, compared whole', () => {
    expect(ALLOWED_EXTERNAL_HOSTS).toEqual([
      'bitbucket.org',
      'console.cloud.google.com',
      'github.com',
    ])
  })

  it('hands over the parsed URL, not the string it was spelled as', () => {
    // What is opened is what was validated: the normalised `url.href`, so a
    // spelling that resolves somewhere else cannot diverge from the check.
    expect(validateExternalUrl('https://github.com/fugaro/console/../fugaro')).toEqual({
      ok: true,
      url: 'https://github.com/fugaro/fugaro',
    })
  })

  it('opens an allow-listed link once, and says that it did', async () => {
    const opened: string[] = []
    const shell = {
      async openExternal(url: string) {
        opened.push(url)
      },
    } as unknown as Shell

    // The spelling is deliberately NOT its canonical form. This is the only
    // test that watches the handover -- what actually reaches
    // `shell.openExternal` -- and the two do not pin the same thing: the table
    // above pins `validateExternalUrl`'s decision, and a canonical spelling
    // here would leave `shell.openExternal(raw)` in the implementation green
    // (measured: with that mutation and the canonical spelling, the whole suite
    // still passed, which is how the handover went unpinned). The spelling
    // `github.com/fugaro/console/../fugaro` resolves to
    // `github.com/fugaro/fugaro` under the WHATWG parser, so the two strings
    // differ exactly where a check that validates one and opens the other
    // would let a spelling diverge from what was validated.
    await expect(
      openExternalValidated(shell, 'https://github.com/fugaro/console/../fugaro'),
    ).resolves.toBe(true)
    expect(
      opened,
      'shell.openExternal must be handed the URL validateExternalUrl returned (url.href), not the string the caller spelled: a spelling that resolves somewhere else would otherwise be opened unvalidated',
    ).toEqual(['https://github.com/fugaro/fugaro'])
  })

  it('calls nothing at all for a refused link', async () => {
    const opened: string[] = []
    const shell = {
      async openExternal(url: string) {
        opened.push(url)
      },
    } as unknown as Shell

    for (const link of ['javascript:alert(1)', 'file:///etc/passwd', 'https://evil.invalid/x']) {
      await expect(openExternalValidated(shell, link), link).resolves.toBe(false)
    }
    expect(opened).toEqual([])
  })
})

describe('the bundle root is the only place a file comes from (rule 3)', () => {
  const root = '/opt/fugaro/console/src/renderer'

  const cases: ReadonlyArray<readonly [string, boolean]> = [
    ['/opt/fugaro/console/src/renderer/index.html', true],
    ['/opt/fugaro/console/src/renderer/assets/app.css', true],
    ['/opt/fugaro/console/src/renderer', false],
    ['/opt/fugaro/console/src/renderer/', false],
    ['/opt/fugaro/console/src/renderer-other/index.html', false],
    ['/opt/fugaro/console/src/main/index.js', false],
    ['/opt/fugaro/console/src/renderer/../main/index.js', false],
    ['/etc/passwd', false],
    ['/opt/fugaro/console/src/renderer/index.html/../../../etc/passwd', false],
  ]

  it.each(cases)('%s inside the root: %s', (candidate, inside) => {
    expect(isInsideBundleRoot(root, candidate)).toBe(inside)
  })

  it('reads a dot segment in the raw URL, encoded or not', () => {
    for (const raw of [
      'app://console/../../etc/passwd',
      'app://console/./index.html',
      'app://console/%2e%2e/secret.txt',
      'app://console/a/../b',
    ]) {
      expect(hasDotSegment(raw), raw).toBe(true)
    }
    for (const raw of [
      'app://console/index.html',
      'app://console/assets/app.css',
      'app://console/a..b/c',
      // One segment as far as the URL parser is concerned -- `%2f` is not a
      // separator -- so this check does not see it. The containment check does,
      // and the request table below is where that one is asserted.
      'app://console/%2e%2e%2f%2e%2e%2fetc/passwd',
    ]) {
      expect(hasDotSegment(raw), raw).toBe(false)
    }
  })
})

describe('the app:// handler serves one root and refuses everything else', () => {
  const refused: ReadonlyArray<readonly [string, number, string]> = [
    ['file:///etc/passwd', 403, 'scheme'],
    ['https://example.invalid/index.html', 403, 'scheme'],
    ['data:text/html,<h1>hello</h1>', 403, 'scheme'],
    ['app://evil.invalid/index.html', 403, 'host'],
    ['app://console:8443/index.html', 403, 'port'],
    ['app://user:secret@console/index.html', 403, 'credentials'],
    ['app://console/index.html?path=/etc/passwd', 403, 'query'],
    ['app://console/../../etc/passwd', 403, 'traversal'],
    ['app://console/%2e%2e/secret.txt', 403, 'traversal'],
    ['app://console/%2e%2e%2f%2e%2e%2fetc/passwd', 403, 'bundle root'],
    ['app://console/index.html%00.png', 403, 'NUL'],
    ['app://console/..%5C..%5Cetc%5Cpasswd', 403, 'backslash'],
    ['app://console/a%2', 400, 'percent-encoding'],
    ['not a url at all', 400, 'does not parse'],
    ['', 400, 'does not parse'],
  ]

  it.each(refused)('refuses %s with %i (%s)', async (url, status, reason) => {
    const response = await serveAppRequest(bundleRoot, url)
    expect(response.status, url).toBe(status)
    await expect(response.text(), url).resolves.toContain(reason)
  })

  it('carries the CSP on a refusal, as it does on a document', async () => {
    const response = await serveAppRequest(bundleRoot, 'app://evil.invalid/index.html')
    expect(response.headers.get(CSP_HEADER)).toBe(CONTENT_SECURITY_POLICY)
  })

  it('resolves the document to the file in the bundle root, and serves it with the header', async () => {
    expect(resolveBundleRequest(bundleRoot, APP_ENTRY_URL)).toEqual({
      ok: true,
      filePath: join(bundleRoot, 'index.html'),
      mediaType: 'text/html; charset=utf-8',
    })

    const response = await serveAppRequest(bundleRoot, APP_ENTRY_URL)
    expect(response.status).toBe(200)
    // The header is the whole reason the scheme is `app://` and not `file://`
    // (rule 3): a `file://` document cannot carry one.
    expect(response.headers.get(CSP_HEADER)).toBe(CONTENT_SECURITY_POLICY)
    expect(response.headers.get('content-type')).toBe('text/html; charset=utf-8')
    expect(response.headers.get('x-content-type-options')).toBe('nosniff')
    await expect(response.text()).resolves.toBe(indexHTML)
  })

  it('serves the document for the bare origin as well', async () => {
    for (const url of [APP_ORIGIN, `${APP_ORIGIN}/`]) {
      const response = await serveAppRequest(bundleRoot, url)
      expect(response.status, url).toBe(200)
      await expect(response.text(), url).resolves.toBe(indexHTML)
    }
  })

  it('answers 404 for a path inside the root that names no file', async () => {
    // `/etc/passwd` is a request *inside* the bundle, not the real file: the
    // handler joins it under the root, finds nothing, and says so.
    const response = await serveAppRequest(bundleRoot, 'app://console/etc/passwd')
    expect(response.status).toBe(404)
    await expect(response.text()).resolves.toBe('no such file in the bundle\n')
  })

  it('refuses a spelling that walks up out of the root', async () => {
    const response = await serveAppRequest(bundleRoot, 'app://console/../renderer')
    expect(response.status).toBe(403)
    await expect(response.text()).resolves.toContain('traversal')
  })

  it('treats a directory as a file that is not there, not as a broken console', async () => {
    // The root here is `src/`, because it is the one tree on this checkout with
    // a directory in it; `readFile` answers EISDIR, and that is 404 rather than
    // 500: a directory is not a resource this bundle serves.
    const srcRoot = fileURLToPath(new URL('../../src', import.meta.url))
    const response = await serveAppRequest(srcRoot, 'app://console/main/')
    expect(response.status).toBe(404)
    await expect(response.text()).resolves.toBe('no such file in the bundle\n')
  })

  it('answers 500 rather than a document when the read fails for another reason', async () => {
    // A name longer than the filesystem allows reaches `readFile` and fails
    // with ENAMETOOLONG, which is not one of the "there is no such file" codes:
    // the reader gets the one-line reason and no document. (A mode-000 file
    // would be the other way to provoke this, and it is not portable: a test run
    // as root reads it happily.)
    const response = await serveAppRequest(bundleRoot, `app://console/${'a'.repeat(5000)}.html`)
    expect(response.status).toBe(500)
    await expect(response.text()).resolves.toBe('the bundle file could not be read\n')
  })

  it('serves a media type by extension, and nothing else by guess', () => {
    expect(mediaTypeFor('/x/index.html')).toBe('text/html; charset=utf-8')
    expect(mediaTypeFor('/x/app.css')).toBe('text/css; charset=utf-8')
    expect(mediaTypeFor('/x/app.js')).toBe('text/javascript; charset=utf-8')
    expect(mediaTypeFor('/x/APP.CSS')).toBe('text/css; charset=utf-8')
    expect(mediaTypeFor('/x/thing.bin')).toBe('application/octet-stream')
    expect(mediaTypeFor('/x/no-extension')).toBe('application/octet-stream')
  })
})

describe('the scheme is declared and wired, not assumed', () => {
  it('registers app as standard and secure, and never as bypassing the CSP', () => {
    const registered: Array<{ scheme: string; privileges: Record<string, unknown> }> = []
    const protocolModule = {
      registerSchemesAsPrivileged(schemes: Array<{ scheme: string; privileges: Record<string, unknown> }>) {
        registered.push(...schemes)
      },
    } as unknown as Protocol

    registerAppScheme(protocolModule)

    expect(registered).toHaveLength(1)
    expect(registered[0]?.scheme).toBe(APP_SCHEME)
    expect(registered[0]?.privileges).toEqual({ standard: true, secure: true })
    // Named on its own: `bypassCSP` would be an off switch for the header this
    // whole module exists to deliver.
    expect(registered[0]?.privileges?.['bypassCSP']).toBeUndefined()
  })

  it('handles every app:// request through the bundle, CSP header included', async () => {
    // The recorded scheme and handler live in an object rather than in two
    // `let`s: TypeScript narrows a `let` to its initial value across a callback
    // that assigns it, and the calls below would then be calls on `never`.
    type Handler = (request: { url: string }) => Promise<Response>
    const wiring: { scheme: string | null; handler: Handler | null } = {
      scheme: null,
      handler: null,
    }
    const protocolModule = {
      handle(name: string, given: Handler) {
        wiring.scheme = name
        wiring.handler = given
      },
    } as unknown as Protocol

    handleAppRequests(protocolModule, bundleRoot)

    expect(wiring.scheme).toBe(APP_SCHEME)
    const response = await wiring.handler?.({ url: APP_ENTRY_URL })
    expect(response?.status).toBe(200)
    expect(response?.headers.get(CSP_HEADER)).toBe(CONTENT_SECURITY_POLICY)

    const refused = await wiring.handler?.({ url: 'app://evil.invalid/index.html' })
    expect(refused?.status).toBe(403)
  })
})

describe('the document in the bundle', () => {
  it('carries the CSP of rule 3, byte for byte, as a meta copy of the header', () => {
    expect(CONTENT_SECURITY_POLICY).toBe(DESIGN_CSP)
    expect(indexHTML).toContain(`content="${CONTENT_SECURITY_POLICY}"`)
  })

  it('closes the doors that matter for a document that will render job-written text', () => {
    expect(CONTENT_SECURITY_POLICY).toContain("default-src 'none'")
    expect(CONTENT_SECURITY_POLICY).toContain("connect-src 'none'")
    expect(CONTENT_SECURITY_POLICY).toContain("script-src 'self'")
    expect(CONTENT_SECURITY_POLICY).toContain("object-src 'none'")
    expect(CONTENT_SECURITY_POLICY).toContain("frame-ancestors 'none'")
    expect(CONTENT_SECURITY_POLICY).not.toContain('unsafe-inline')
    expect(CONTENT_SECURITY_POLICY).not.toContain('unsafe-eval')
    expect(CONTENT_SECURITY_POLICY).not.toContain('*')
  })

  it('is the document the window loads, and it is script-free', () => {
    expect(indexHTML).toContain(`<title>${CONSOLE_WINDOW_TITLE}</title>`)
    expect(APP_ENTRY_URL).toBe(`${APP_ORIGIN}/index.html`)
    expect(indexHTML).not.toContain('<script')
    expect(indexHTML).not.toContain('<style')
  })
})

describe('createConsoleWindow', () => {
  it('loads the bundled document and wires every denial to the window it built', async () => {
    const contents = fakeWebContents()
    const session = fakeSession()
    const loaded: string[] = []
    const options: BrowserWindowConstructorOptions[] = []
    const listeners = new Map<string, () => void>()
    let shown = false

    const fakeWindow = {
      webContents: contents.value,
      once(event: string, listener: () => void) {
        listeners.set(event, listener)
      },
      show() {
        shown = true
      },
      async loadURL(url: string) {
        loaded.push(url)
      },
    }
    const WindowClass = function (given: BrowserWindowConstructorOptions): object {
      options.push(given)
      return fakeWindow
    } as unknown as new (options: BrowserWindowConstructorOptions) => BrowserWindow

    const built = await createConsoleWindow({ BrowserWindow: WindowClass, session: session.value })

    expect(built).toBe(fakeWindow)
    // The one load, over the console's own scheme: no file://, no remote origin.
    expect(loaded).toEqual([APP_ENTRY_URL])
    expect(options).toEqual([
      {
        width: 1180,
        height: 780,
        show: false,
        title: CONSOLE_WINDOW_TITLE,
        webPreferences: consoleWebPreferences(),
      },
    ])
    expect(session.requestHandler()).not.toBeNull()
    expect(session.checkHandler()).not.toBeNull()

    const navigate = recordingEvent()
    contents.emit('will-navigate', navigate, 'https://example.invalid/fugaro')
    expect(navigate.prevented).toBe(true)

    const handler = contents.windowOpenHandler()
    expect(handler?.()).toEqual({ action: 'deny' })

    expect(shown).toBe(false)
    listeners.get('ready-to-show')?.()
    expect(shown).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// The version skew handshake (desktop-console plan Task 3.2; design section 7.1
// and D7).
//
// The handshake's *placement* is `src/shared/version.ts`'s and is tested there
// boundary by boundary. What is held here is the half that only this module can
// be wrong about: that the refusal is real -- that a console which will not read
// the CLI it found builds **no window**, so there is no dashboard to draw and
// nothing to misreport spend into. That is the plan's red step, and it is
// asserted by counting the constructors rather than by reading a flag, because a
// flag is exactly what a console that drew the dashboard anyway would have set
// correctly.
// ---------------------------------------------------------------------------

/** fakeWindowFactory counts every window constructed, records every load, and
 * hands back the fake `webContents` of each so the handler wiring can be
 * checked on the window that was actually built. */
function fakeWindowFactory(): {
  BrowserWindow: new (options: BrowserWindowConstructorOptions) => BrowserWindow
  options: BrowserWindowConstructorOptions[]
  loaded: string[]
  built: unknown[]
  contents: FakeWebContents[]
} {
  const options: BrowserWindowConstructorOptions[] = []
  const loaded: string[] = []
  const built: unknown[] = []
  const contents: FakeWebContents[] = []
  const WindowClass = function (given: BrowserWindowConstructorOptions): object {
    options.push(given)
    const webContents = fakeWebContents()
    contents.push(webContents)
    const window = {
      webContents: webContents.value,
      once() {},
      show() {},
      async loadURL(url: string) {
        loaded.push(url)
      },
    }
    built.push(window)
    return window
  } as unknown as new (options: BrowserWindowConstructorOptions) => BrowserWindow
  return { BrowserWindow: WindowClass, options, loaded, built, contents }
}

/** nothingToRead is a reader that failed before it could read: the shape
 * `src/main/spawn.ts`'s `probeVersion` returns when no binary resolved. */
const nothingToRead = async (): Promise<{ ok: false; reason: string }> => ({
  ok: false,
  reason: 'no fugaro binary was found',
})

describe('runVersionHandshake', () => {
  it('proceeds for a CLI inside the supported range', async () => {
    const compatibility = await runVersionHandshake({
      readVersion: async () => ({ ok: true, stdout: `${CLI_VERSION_BUILT_AGAINST}\n` }),
    })
    expect(compatibility.mode).toBe('ok')
    expect(compatibility.skew).toBe('supported')
    expect(compatibility.cliVersion).toBe(CLI_VERSION_BUILT_AGAINST)
  })

  it('proceeds in a reduced mode for a CLI outside it, naming what it read', async () => {
    const compatibility = await runVersionHandshake({
      readVersion: async () => ({ ok: true, stdout: '0.9.0\n' }),
    })
    expect(compatibility.mode).toBe('reduced')
    expect(compatibility.skew).toBe('newer')
    expect(compatibility.cliVersion).toBe('0.9.0')
    expect(hidesView(compatibility, 'spend')).toBe(true)
  })

  it('refuses a CLI below the floor', async () => {
    const compatibility = await runVersionHandshake({
      readVersion: async () => ({ ok: true, stdout: '0.5.0\n' }),
    })
    expect(compatibility.mode).toBe('refused')
    expect(compatibility.skew).toBe('below-floor')
  })

  it('refuses, with the reader\'s own reason, when there is no version to read', async () => {
    const compatibility = await runVersionHandshake({ readVersion: nothingToRead })
    expect(compatibility.mode).toBe('refused')
    expect(compatibility.skew).toBe('unreadable')
    // The reason a screen or a title shows is the console's own sentence: the
    // reader's, which is `probeVersion`'s, and never the child's output.
    expect(unreadableReason(compatibility)).toBe('no fugaro binary was found')
  })

  it('refuses rather than crashing when the read itself throws', async () => {
    // A subprocess that misbehaves must not take the console down with it, and
    // must not be upgraded into a dashboard either: the outcome of a throw is the
    // same refusal a failed read gets.
    const compatibility = await runVersionHandshake({
      readVersion: async () => {
        throw new Error('spawn failed')
      },
    })
    expect(compatibility.mode).toBe('refused')
    expect(compatibility.skew).toBe('unreadable')
    expect(unreadableReason(compatibility)).toBe(HANDSHAKE_UNREADABLE)
  })

  it('carries no part of a line it could not read', async () => {
    // The line a wrapper echoed is a string a binary chose. It is dropped where
    // it was read: the compatibility, its reason and its title all have no place
    // for it, so nothing downstream can draw it.
    const output = `${githubToken()}\n`
    const compatibility = await runVersionHandshake({ readVersion: async () => ({ ok: true, stdout: output }) })
    expect(compatibility.cliVersion).toBeNull()
    expect(JSON.stringify(compatibility)).not.toContain(githubToken())
    expect(consoleWindowTitle(compatibility)).not.toContain(githubToken())
  })
})

describe('consoleWindowTitle', () => {
  it('is the plain title before a handshake has run, and inside the range', () => {
    expect(consoleWindowTitle(null)).toBe(CONSOLE_WINDOW_TITLE)
    expect(consoleWindowTitle(classifyCliVersion(CLI_VERSION_BUILT_AGAINST))).toBe(CONSOLE_WINDOW_TITLE)
  })

  it('says which mode the console is in, in its own words', () => {
    const reduced = consoleWindowTitle(classifyCliVersion('0.9.0'))
    expect(reduced).toContain(CONSOLE_WINDOW_TITLE)
    expect(reduced).toContain(SKEW_HEADLINES.reduced)
    const refused = consoleWindowTitle(classifyCliVersion('0.5.0'))
    expect(refused).toContain(CONSOLE_WINDOW_TITLE)
    expect(refused).toContain(SKEW_HEADLINES.refused)
  })

  it('never carries the version the binary reported, because the OS chrome draws it', () => {
    // A window title is not drawn by this console's writers, so it is not a
    // place `sanitize.ts` can reach and not a place a string from outside goes.
    // The reported version is drawn in the document instead, by
    // `renderVersionSkew`, through the sanitizer.
    const compatibility = classifyCliVersion('v0.9.0-3-gabc1234')
    expect(compatibility.cliVersion).toBe('v0.9.0-3-gabc1234')
    expect(consoleWindowTitle(compatibility)).not.toContain('gabc1234')
    expect(consoleWindowTitle(compatibility)).not.toContain('0.9.0')
  })
})

describe('startupWindow', () => {
  it('builds the dashboard window for a CLI inside the range', async () => {
    const factory = fakeWindowFactory()
    const startup = await startupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value,
      readVersion: async () => ({ ok: true, stdout: `${CLI_VERSION_BUILT_AGAINST}\n` }),
    })
    expect(startup.compatibility.mode).toBe('ok')
    expect(factory.options).toHaveLength(1)
    expect(factory.built).toEqual([startup.window])
    expect(factory.options[0]?.title).toBe(CONSOLE_WINDOW_TITLE)
    expect(factory.options[0]?.webPreferences).toEqual(consoleWebPreferences())
    expect(factory.loaded).toEqual([APP_ENTRY_URL])
  })

  it('builds the window in a reduced mode too, because a reduced console is a console', async () => {
    const factory = fakeWindowFactory()
    const startup = await startupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value,
      readVersion: async () => ({ ok: true, stdout: '0.6.9\n' }),
    })
    expect(startup.compatibility.mode).toBe('reduced')
    expect(startup.window).not.toBeNull()
    expect(factory.options[0]?.title).toContain(SKEW_HEADLINES.reduced)
    // The window is still the hardened one: a reduced mode is not a reason to
    // relax anything.
    expect(factory.options[0]?.webPreferences).toEqual(consoleWebPreferences())
  })

  it('builds no window at all below the floor -- the plan\'s red step', async () => {
    const factory = fakeWindowFactory()
    const startup = await startupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value,
      readVersion: async () => ({ ok: true, stdout: '0.5.0\n' }),
    })
    expect(startup.compatibility.mode).toBe('refused')
    expect(startup.window).toBeNull()
    // The stronger half: nothing was *constructed*. A console that built a
    // window and decided later not to draw a dashboard in it would pass a
    // `window === null` check written the other way round, and would still be
    // one renderer bug away from a spend figure nothing vouched for.
    expect(factory.options).toHaveLength(0)
    expect(factory.built).toHaveLength(0)
    expect(factory.loaded).toEqual([])
  })

  it('builds no window when the version could not be read either', async () => {
    const factory = fakeWindowFactory()
    const startup = await startupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value,
      readVersion: nothingToRead,
    })
    expect(startup.compatibility.mode).toBe('refused')
    expect(startup.window).toBeNull()
    expect(factory.options).toHaveLength(0)
  })
})

describe('createRefusalWindow', () => {
  it('builds the hardened window, with a title that says what it refused', async () => {
    const factory = fakeWindowFactory()
    const session = fakeSession()
    const compatibility = classifyCliVersion('0.5.0')
    const built = await createRefusalWindow(
      { BrowserWindow: factory.BrowserWindow, session: session.value },
      compatibility,
    )
    expect(factory.built).toEqual([built])
    expect(factory.options).toHaveLength(1)
    expect(factory.options[0]?.title).toContain(SKEW_HEADLINES.refused)
    expect(factory.options[0]?.webPreferences).toEqual(consoleWebPreferences())
    expect(factory.options[0]?.show).toBe(false)
    // The same single load over the console's own scheme, the same handlers and
    // the same denied permissions as the dashboard window: the refusal screen is
    // drawn by the renderer in a window that is hardened exactly as much.
    expect(factory.loaded).toEqual([APP_ENTRY_URL])
    expect(session.requestHandler()).not.toBeNull()
    expect(session.checkHandler()).not.toBeNull()
    const navigate = recordingEvent()
    factory.contents[0]?.emit('will-navigate', navigate, 'https://example.invalid/fugaro')
    expect(navigate.prevented).toBe(true)
    expect(factory.contents[0]?.windowOpenHandler()?.()).toEqual({ action: 'deny' })
  })
})
