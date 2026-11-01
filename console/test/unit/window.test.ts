// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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
//     the test that counts the calls; letting a rejection from
//     `shell.openExternal` through -- the platform declining the handover --
//     fails the case that asserts it answers `false` instead;
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
  REFUSAL_ENTRY_URL,
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
  HANDSHAKE_NOT_FOUND,
  HANDSHAKE_UNREADABLE,
  consoleWebPreferences,
  consoleWindowOptions,
  consoleWindowTitle,
  createConsoleWindow,
  createRefusalWindow,
  dashboardWebPreferences,
  denyEveryPermission,
  denyNavigation,
  denyPageTitleUpdate,
  denyWebviewAttach,
  denyWindowOpen,
  installWebContentsSecurity,
  openExternalValidated,
  openStartupWindow,
  openWindowFor,
  PULL_REQUEST_PROVIDERS,
  pullRequestUrl,
  runVersionHandshake,
  startupWindow,
  validateExternalUrl,
} from '../../src/main/window'
import type { NavigationEvent, VersionRead } from '../../src/main/window'
import type { CliCompatibility } from '../../src/shared/version'
import {
  CLI_VERSION_BUILT_AGAINST,
  REDUCED_VIEWS,
  SKEW_HEADLINES,
  classifyCliVersion,
  hidesView,
  unreadableReason,
} from '../../src/shared/version'
import { githubToken } from '../support/fakeSecrets'

// The two documents of the bundle, read from this test's own location rather
// than from the working directory: since Task 3.4a the served root is the
// *build's* `dist/bundle/` (`rendererRoot` in src/main/index.ts), which is these
// two files copied verbatim beside the bundled entry -- `test/unit/bundle.test.ts`
// is what holds the copy to the sources, and what is read here is the source the
// copy is made from, which is what `window.test.ts` has always pinned.
const bundleRoot = fileURLToPath(new URL('../../src/renderer', import.meta.url))
const indexHTMLPath = fileURLToPath(new URL('../../src/renderer/index.html', import.meta.url))
const indexHTML = readFileSync(indexHTMLPath, 'utf8')

/**
 * refusalHTML is the second document in the bundle (Task 3.3): the one a refused
 * console loads, which has no dashboard root at all. It is read here rather than
 * described, for the same reason `indexHTML` is.
 */
const refusalHTMLPath = fileURLToPath(new URL('../../src/renderer/refusal.html', import.meta.url))
const refusalHTML = readFileSync(refusalHTMLPath, 'utf8')

/**
 * DOCUMENT_TITLE is the title the loaded document defines, read out of the
 * document itself rather than written down again: it is the value Electron
 * offers to the window in place of the `title` option, so a test that wants to
 * assert what a window's title *ends up* as has to know it. The assertion below
 * (`is the document the window loads`) pins it as the console's plain title.
 */
const DOCUMENT_TITLE = /<title>([^<]*)<\/title>/.exec(indexHTML)?.[1] ?? ''

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

  it('builds the window with those preferences when nothing asks for a preload', () => {
    const options = consoleWindowOptions()
    expect(options.webPreferences).toEqual(consoleWebPreferences())
    expect(options.title).toBe(CONSOLE_WINDOW_TITLE)
    // Shown only once it has painted, so the console is never on screen half
    // drawn (`createConsoleWindow`'s `ready-to-show` listener).
    expect(options.show).toBe(false)
    // The default carries no preload, and that is a decision rather than an
    // oversight (Task 3.4a): the dashboard window is the one window with a
    // renderer entry to run, so its caller is the one that names a preload
    // (`dashboardWebPreferences` below), and the refusal window -- which is
    // built from this default -- gets none by construction.
    expect(options.webPreferences?.preload).toBeUndefined()
  })

  it('adds the preload and changes nothing else about the hardened set', () => {
    // Task 3.4a, step 1, the half that matters: the dashboard's preferences are
    // the hardened set *plus* a preload, so there is exactly one key more than
    // `consoleWebPreferences()` has and every value under the shared keys is
    // identical. A `dashboardWebPreferences` that quietly dropped `sandbox` or
    // turned `contextIsolation` off would fail here, and that is the mutation
    // this test exists for.
    const hardened = consoleWebPreferences()
    const dashboard = dashboardWebPreferences(PRELOAD_PATH)
    expect(dashboard.preload).toBe(PRELOAD_PATH)
    expect(Object.keys(dashboard).toSorted()).toEqual([...Object.keys(hardened), 'preload'].toSorted())
    for (const [flag, value] of Object.entries(hardened)) {
      expect(dashboard[flag as keyof WebPreferences], `${flag} was changed by the preload`).toBe(value)
    }
    // And the path is an absolute one: a relative preload path would resolve
    // against Electron's working directory, which is not the app's.
    expect(PRELOAD_PATH.startsWith('/')).toBe(true)
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
    // The console's own link, and the only one this process builds rather than
    // reads out of a document: the two onboarding screens' link button
    // (`GETTING_STARTED_URL` in `src/main/onboarding.ts`).
    'https://fugaro.dev/getting-started/',
  ]

  it.each(accepted)('opens %s', (link) => {
    expect(validateExternalUrl(link)).toEqual({ ok: true, url: link })
  })

  it('is an allow-list of exactly those hosts, compared whole', () => {
    expect(ALLOWED_EXTERNAL_HOSTS).toEqual([
      'bitbucket.org',
      'console.cloud.google.com',
      // The website's own host, on the list because the onboarding screens link
      // to it. A second entry here is a deliberate edit in `src/main/window.ts`
      // and in this test together.
      'fugaro.dev',
      'github.com',
    ])
  })

  it('does not open a host that merely ends with one of them', () => {
    // The comparison is whole and lower-cased: a lookalike registered under a
    // domain the console trusts is not on the list.
    for (const lookalike of ['https://fugaro.dev.evil.test/getting-started/', 'https://evil-fugaro.dev/']) {
      expect(validateExternalUrl(lookalike).ok, lookalike).toBe(false)
    }
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

  it('opens a link through Electron’s shell on every platform, and through nothing else', async () => {
    // The owner's ruling of 2026-10-20 ships the console on Linux, where the
    // macOS habit of running `/usr/bin/open` would be a child process spawned
    // from the renderer's request -- a new privilege for a click, and one that
    // never existed on darwin either. `shell.openExternal` is Electron's own
    // door and it is the same call on both platforms, so this is a guard rather
    // than a change: `src/main/window.ts` may not import `child_process`, and
    // the only symbol it hands to `shell` is `openExternal`.
    const source = readFileSync(fileURLToPath(new URL('../../src/main/window.ts', import.meta.url)), 'utf8')
    expect(source).not.toMatch(/node:child_process|require\(['"]child_process['"]\)/)
    expect(source).not.toMatch(/['"]open['"]\s*,\s*\[/)
    expect(source).not.toMatch(/\/usr\/bin\/open/)
    // and the one call site is the validated handover the tests above watch
    expect(source.match(/\.openExternal\(/g) ?? []).toHaveLength(1)
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

  it('answers false when the platform refuses the handover, rather than rejecting', async () => {
    // The third way a link does not open, and the only one the *console* cannot
    // decide: the URL passed its allow-list, the handover reached the operating
    // system, and the platform has nothing registered for `https:` -- a
    // container, a headless box, a desktop whose browser association is broken.
    // Electron's `shell.openExternal` reports that by **rejecting**, and an
    // uncaught rejection is not this function's `false`: it travels out of
    // whichever `ipcMain.handle` callback awaited it, so the picker's
    // `openExternal` answers with the operating system's own complaint (its
    // catch surfaces `error.message`) instead of the console's one sentence,
    // and the dashboard's `link.openExternal` rejects the same way. The
    // contract the picker's handler is written against is that both a refused
    // URL and a declined handover answer `false`; this is the half of it that
    // was not true.
    const opened: string[] = []
    const shell = {
      async openExternal(url: string) {
        opened.push(url)
        throw new Error('Failed to open the link: no application is registered for the https scheme')
      },
    } as unknown as Shell

    await expect(openExternalValidated(shell, 'https://github.com/fugaro/fugaro')).resolves.toBe(false)
    // The handover did happen -- the OS was asked -- so a test that only
    // asserted `false` would also pass for a shell this function never reached.
    expect(opened).toEqual(['https://github.com/fugaro/fugaro'])
  })
})

describe('a pull request link is built from three fields and never read (D814, D815)', () => {
  it('builds the GitHub form from the provider, the repository and the number', () => {
    expect(pullRequestUrl('github', 'owner/repo-a', 1458)).toBe('https://github.com/owner/repo-a/pull/1458')
  })

  it('knows exactly the two providers, and no third', () => {
    // The set the IPC schema's own `gitProvider` kind is read from, so the two
    // cannot drift: a third provider here would be one the schema admits.
    expect(PULL_REQUEST_PROVIDERS).toEqual(['github', 'bitbucket'])
  })

  it('builds the Bitbucket form from the same three fields', () => {
    expect(pullRequestUrl('bitbucket', 'owner/repo-a', 1458)).toBe(
      'https://bitbucket.org/owner/repo-a/pull-requests/1458',
    )
  })

  // The six hostile inputs the plan names, and the reason each one is a `null`
  // rather than a URL: an unknown provider has no host to build; a `repo` that
  // is not exactly `owner/name` is a path a job wrote; and a number that is not
  // a positive safe integer is not a pull request number. Every one of them is
  // refused **here**, before `validateExternalUrl` is reached, because this is
  // the first gate -- rule 2's "no link is built from a field a job wrote".
  const hostile: ReadonlyArray<readonly [string, string, number, string]> = [
    ['gitlab', 'owner/repo-a', 1458, 'a provider the console does not know'],
    ['github', 'owner/../evil', 1458, 'a repository that walks out of its own path'],
    ['github', 'owner/repo-a/pull/1', 1458, 'a repository that is already a URL path'],
    ['github', 'owner/repo-a', 0, 'zero'],
    ['github', 'owner/repo-a', -1, 'a negative number'],
    ['github', 'owner/repo-a', 1.5, 'a fraction'],
  ]

  it.each(hostile)('answers null for %s / %s / %s (%s)', (provider, repo, number) => {
    expect(pullRequestUrl(provider, repo, number)).toBeNull()
  })

  const otherHostile: ReadonlyArray<readonly [string, string, number, string]> = [
    ['github', 'owner', 1, 'one segment only'],
    ['github', 'owner/repo-a/', 1, 'a trailing separator'],
    ['github', '/owner/repo-a', 1, 'a leading separator'],
    ['github', 'owner/repo-a?x=1', 1, 'a query'],
    ['github', 'owner/repo-a#f', 1, 'a fragment'],
    ['github', 'owner:repo-a/x', 1, 'a colon'],
    ['github', 'owner@evil.test/x', 1, 'an at sign'],
    ['github', 'owner/repo a', 1, 'a space'],
    ['github', 'https://github.com/owner/repo-a', 1, 'a whole URL'],
    ['github', 'owner/repo-a', 2 ** 53, 'past the safe integers'],
    ['github', 'owner/repo-a', Number.NaN, 'not a number at all'],
  ]

  it.each(otherHostile)('answers null for %s / %s / %s (%s)', (provider, repo, number) => {
    expect(pullRequestUrl(provider, repo, number)).toBeNull()
  })

  it('builds nothing that the one path to the browser would refuse', () => {
    // The two functions are one gate in two steps, and this is the join: what
    // the builder returns is a URL `validateExternalUrl` opens, on the
    // allow-list it already carries. A builder that spelled its own host would
    // fail here rather than at the operating system.
    for (const [provider, host] of [
      ['github', 'github.com'],
      ['bitbucket', 'bitbucket.org'],
    ] as const) {
      const url = pullRequestUrl(provider, 'owner/repo-a', 1458)
      expect(url?.startsWith(`https://${host}/`), `${provider} builds on ${host}`).toBe(true)
      expect(validateExternalUrl(url as string)).toEqual({ ok: true, url })
    }
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

  it('is the document the window loads, with the one script of Task 3.4a', () => {
    expect(indexHTML).toContain(`<title>${CONSOLE_WINDOW_TITLE}</title>`)
    expect(APP_ENTRY_URL).toBe(`${APP_ORIGIN}/index.html`)
    // The document was script-free until Task 3.4a, and it is not any more: the
    // dashboard's renderer is `./console.js`, the bundle this task's build
    // writes into the served root. What the *refusal* document is owed is
    // unchanged -- it loads none (asserted below) -- and the element-by-element
    // version of both claims is `test/unit/csp-document.test.ts`'s.
    expect(indexHTML).toContain('<script src="./console.js" defer></script>')
    expect(indexHTML).not.toContain('<style')
  })
})

/**
 * PRELOAD_PATH is the compiled preload the dashboard window loads (Task 3.4a).
 * It is a path under an application directory and not a real file: the tests
 * below are about *which* window gets the key and what the key holds, and a
 * preference object does not read the file.
 */
const PRELOAD_PATH = '/opt/fugaro-console/dist/preload/index.js'

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
      on(event: string, listener: () => void) {
        listeners.set(event, listener)
      },
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

    const built = await createConsoleWindow({ BrowserWindow: WindowClass, session: session.value, preloadPath: PRELOAD_PATH })

    expect(built).toBe(fakeWindow)
    // The one load, over the console's own scheme: no file://, no remote origin.
    expect(loaded).toEqual([APP_ENTRY_URL])
    expect(options).toEqual([
      {
        width: 1180,
        height: 780,
        show: false,
        title: CONSOLE_WINDOW_TITLE,
        // The one ground literal (Task 6.5): the window paints the colour the
        // stylesheet paints, so the first frame is not a flash of white. It is
        // spelled out here as well as held equal to the token layer's `--ground`
        // in `theme-tokens.test.ts`, because this case is about the options the
        // window is built with and a `backgroundColor` that went missing would
        // otherwise be a change this list could not see.
        backgroundColor: '#0A0E16',
        webPreferences: dashboardWebPreferences(PRELOAD_PATH),
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

/**
 * FakeConsoleWindow is one window of `fakeWindowFactory`, and `title` is the
 * point of it: Electron sets a window's title from the `title` option it was
 * constructed with and then **replaces** it with the loaded document's
 * `<title>` unless a `page-title-updated` listener prevents the default. That
 * rule is modelled here, because it is what makes the assertion about the mode
 * an assertion about the *outcome* -- the title the window ends up with -- and
 * not about the option Electron discards when the document defines one (which
 * `src/renderer/index.html` does; the test below pins it).
 */
interface FakeConsoleWindow {
  /** `title` is the window's current title, as `BrowserWindow.getTitle()` would
   * report it. */
  title: string
  readonly webContents: WebContents
  /** `pageTitleOffers` is every title the document offered, and whether the
   * console refused it. */
  pageTitleOffers: { title: string; prevented: boolean }[]
  on(event: string, listener: (event: NavigationEvent, title: string) => void): void
  once(): void
  show(): void
  loadURL(url: string): Promise<void>
}

/** fakeWindowFactory counts every window constructed, records every load, and
 * hands back the fake `webContents` and the fake window of each, so the handler
 * wiring and the title a window ends up with can be checked on the window that
 * was actually built. */
function fakeWindowFactory(): {
  BrowserWindow: new (options: BrowserWindowConstructorOptions) => BrowserWindow
  options: BrowserWindowConstructorOptions[]
  loaded: string[]
  built: FakeConsoleWindow[]
  contents: FakeWebContents[]
} {
  const options: BrowserWindowConstructorOptions[] = []
  const loaded: string[] = []
  const built: FakeConsoleWindow[] = []
  const contents: FakeWebContents[] = []
  const WindowClass = function (given: BrowserWindowConstructorOptions): object {
    options.push(given)
    const webContents = fakeWebContents()
    contents.push(webContents)
    const titleListeners: ((event: NavigationEvent, title: string) => void)[] = []
    const window: FakeConsoleWindow = {
      title: typeof given.title === 'string' ? given.title : '',
      webContents: webContents.value,
      pageTitleOffers: [],
      on(event: string, listener: (event: NavigationEvent, title: string) => void) {
        if (event === 'page-title-updated') titleListeners.push(listener)
      },
      once() {},
      show() {},
      async loadURL(url: string) {
        loaded.push(url)
        // Electron's rule, in the order Electron applies it: the document's
        // title (`DOCUMENT_TITLE`, what `src/renderer/index.html` defines) is
        // offered to the window and becomes its title unless a listener refuses
        // it -- which is why the `title` option alone is not enough.
        let prevented = false
        for (const listener of titleListeners) {
          listener(
            {
              preventDefault: () => {
                prevented = true
              },
            },
            DOCUMENT_TITLE,
          )
        }
        window.pageTitleOffers.push({ title: DOCUMENT_TITLE, prevented })
        if (!prevented) window.title = DOCUMENT_TITLE
      },
    }
    built.push(window)
    return window
  } as unknown as new (options: BrowserWindowConstructorOptions) => BrowserWindow
  return { BrowserWindow: WindowClass, options, loaded, built, contents }
}

/** nothingToRead is a reader that failed before it could read: what
 * `versionReadFrom` makes of a resolution with no executable candidate. Since
 * round 2 a failed read is a category and never a sentence, so the resolver's
 * reason -- which can quote the binary -- cannot reach the compatibility. */
const nothingToRead = async (): Promise<VersionRead> => ({ ok: false, failure: 'not-found' })

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
    expect(compatibility.skew).toBe('older')
    expect(compatibility.cliVersion).toBe('0.9.0')
    expect(hidesView(compatibility, 'spend')).toBe(true)
  })

  it('proceeds in the reduced mode for a dev build, which no comparison can place', async () => {
    // The other line a `fugaro` binary prints: `var Version = "dev"`
    // (`internal/cli/root.go`) for a binary built without the release
    // `-ldflags`, which is the binary the person building the CLI and the console
    // together runs. It is a *recognised* line and it is not a refusal: refusing
    // would block exactly that person, and the reduced mode is what keeps the
    // acceptance from being a silent trust -- every view whose figures nothing
    // vouched for is withheld, and the banner names the line as unplaceable.
    const compatibility = await runVersionHandshake({
      readVersion: async () => ({ ok: true, stdout: 'dev\n' }),
    })
    expect(compatibility.mode).toBe('reduced')
    expect(compatibility.skew).toBe('unplaceable')
    expect(compatibility.cliVersion).toBe('dev')
    expect(compatibility.hidden).toEqual([...REDUCED_VIEWS])
    expect(hidesView(compatibility, 'spend')).toBe(true)
  })

  it('refuses a CLI below the floor', async () => {
    const compatibility = await runVersionHandshake({
      readVersion: async () => ({ ok: true, stdout: '0.5.0\n' }),
    })
    expect(compatibility.mode).toBe('refused')
    expect(compatibility.skew).toBe('below-floor')
  })

  it('refuses, with the console\'s sentence for the category, when there is no version to read', async () => {
    const compatibility = await runVersionHandshake({ readVersion: nothingToRead })
    expect(compatibility.mode).toBe('refused')
    expect(compatibility.skew).toBe('unreadable')
    // The reason a screen shows is the console's fixed sentence for the
    // category, never the resolver's reason and never the child's output.
    expect(unreadableReason(compatibility)).toBe(HANDSHAKE_NOT_FOUND)
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

/**
 * The mode reaches a real window's header. The review of this task found the
 * `title` option inert: `src/renderer/index.html` defines a `<title>`, and
 * Electron's own typings say the option "will be ignored" then, so a window
 * asserted only on the option it was handed proves nothing about what an
 * operator sees. `fakeWindowFactory`'s window therefore models Electron's rule
 * (the document's title replaces the option's unless a `page-title-updated`
 * listener prevents it) and these tests assert the title the window *ends up*
 * with. `test/e2e/skew.spec.ts` asserts the same thing in a real window, where
 * Electron itself applies the rule.
 */
describe('the title the window ends up with', () => {
  it('is the console\'s own, with the mode on it, after the document has loaded', async () => {
    const factory = fakeWindowFactory()
    const startup = await startupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: async () => ({ ok: true, stdout: '0.6.9\n' }),
    })
    const window = factory.built[0]
    expect(window?.title).toBe(consoleWindowTitle(startup.compatibility))
    expect(window?.title).toContain(SKEW_HEADLINES.reduced)
    expect(window?.title).not.toBe(DOCUMENT_TITLE)
    // The document offered its title and the console refused it -- so this is
    // the denial doing the work, and not a document that stayed silent.
    expect(window?.pageTitleOffers).toEqual([{ title: DOCUMENT_TITLE, prevented: true }])
  })

  it('says what it refused on the window the refusal screen is drawn in', async () => {
    const factory = fakeWindowFactory()
    const built = await createRefusalWindow(
      { BrowserWindow: factory.BrowserWindow, session: fakeSession().value, preloadPath: PRELOAD_PATH },
      classifyCliVersion('0.5.0'),
    )
    expect(factory.built[0]?.title).toContain(SKEW_HEADLINES.refused)
    expect(factory.built[0]?.title).toBe(consoleWindowTitle(classifyCliVersion('0.5.0')))
    expect(factory.built[0]?.title).not.toBe(DOCUMENT_TITLE)
    expect(built.title).toContain(SKEW_HEADLINES.refused)
  })

  it('is the document\'s title when the console does not refuse it -- which is why it does', async () => {
    // The mutation, as a test: the same options, the same document, and no
    // `page-title-updated` denial -- exactly what `buildConsoleWindow` did
    // before the review. The window ends up with the document's plain title and
    // the mode is gone, so the assertions above are about the denial and not
    // about the option, the fake, or Electron happening to agree.
    const factory = fakeWindowFactory()
    const window = new factory.BrowserWindow(
      consoleWindowOptions(classifyCliVersion('0.5.0')),
    ) as unknown as FakeConsoleWindow
    await window.loadURL(APP_ENTRY_URL)
    expect(window.title).toBe(DOCUMENT_TITLE)
    expect(window.title).not.toContain(SKEW_HEADLINES.refused)
    expect(window.pageTitleOffers).toEqual([{ title: DOCUMENT_TITLE, prevented: false }])
  })

  it('refuses a page title through the one handler, whatever the page says', () => {
    const event = recordingEvent()
    denyPageTitleUpdate(event)
    expect(event.prevented).toBe(true)
  })
})

describe('startupWindow', () => {
  it('builds the dashboard window for a CLI inside the range', async () => {
    const factory = fakeWindowFactory()
    const startup = await startupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: async () => ({ ok: true, stdout: `${CLI_VERSION_BUILT_AGAINST}\n` }),
    })
    expect(startup.compatibility.mode).toBe('ok')
    expect(factory.options).toHaveLength(1)
    expect(factory.built).toEqual([startup.window])
    expect(factory.options[0]?.title).toBe(CONSOLE_WINDOW_TITLE)
    // ...and that is what the window *ends up* as: the document offered its own
    // `<title>` and the console refused it (see the block below).
    expect(factory.built[0]?.title).toBe(CONSOLE_WINDOW_TITLE)
    expect(factory.options[0]?.webPreferences).toEqual(dashboardWebPreferences(PRELOAD_PATH))
    expect(factory.loaded).toEqual([APP_ENTRY_URL])
  })

  it('builds the window in a reduced mode too, because a reduced console is a console', async () => {
    const factory = fakeWindowFactory()
    const startup = await startupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: async () => ({ ok: true, stdout: '0.6.9\n' }),
    })
    expect(startup.compatibility.mode).toBe('reduced')
    expect(startup.window).not.toBeNull()
    expect(factory.options[0]?.title).toContain(SKEW_HEADLINES.reduced)
    expect(factory.built[0]?.title).toContain(SKEW_HEADLINES.reduced)
    // The window is still the hardened one, and still the dashboard's: a reduced
    // mode is not a reason to relax anything, and it is not a reason to take the
    // bridge away either -- a reduced console draws the views it may draw, which
    // it can only do over IPC.
    expect(factory.options[0]?.webPreferences).toEqual(dashboardWebPreferences(PRELOAD_PATH))
  })

  it('builds the window for a dev build too, and says on it that the line is unplaceable', async () => {
    // The wiring half of the plan's open item 3 (`docs/plans/2026-10-15-desktop-console.md`),
    // answered on 2026-10-22: a dev CLI opens the dashboard in the reduced mode
    // rather than being refused. So this is the `ok`/reduced path and not the
    // refusal one -- the window is built, it loads the dashboard document, and
    // its title carries the reduced banner's headline, which is the header this
    // tree can put a mode in (the document draws the banner itself).
    const factory = fakeWindowFactory()
    const startup = await startupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: async () => ({ ok: true, stdout: 'dev\n' }),
    })
    expect(startup.compatibility.mode).toBe('reduced')
    expect(startup.compatibility.skew).toBe('unplaceable')
    expect(startup.window).not.toBeNull()
    expect(factory.options).toHaveLength(1)
    expect(factory.loaded).toEqual([APP_ENTRY_URL])
    expect(factory.built[0]?.title).toContain(SKEW_HEADLINES.reduced)
    expect(factory.built[0]?.title).not.toContain(SKEW_HEADLINES.refused)
  })

  it('builds no window at all below the floor -- the plan\'s red step', async () => {
    const factory = fakeWindowFactory()
    const startup = await startupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
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
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: nothingToRead,
    })
    expect(startup.compatibility.mode).toBe('refused')
    expect(startup.window).toBeNull()
    expect(factory.options).toHaveLength(0)
  })

  it('tells the caller the answer after the read and before the document loads', async () => {
    // Task 3.4a, step 2, and the whole reason `onDecided` is a dep: the IPC
    // surface can only be registered in the window between the handshake having
    // an answer and a renderer existing that could ask for one. So the callback
    // is recorded *in order* against the two things it has to sit between -- the
    // version read and the load -- rather than merely "called at some point".
    const order: string[] = []
    const factory = fakeWindowFactory()
    const startup = await startupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: async () => {
        order.push('read')
        return { ok: true, stdout: `${CLI_VERSION_BUILT_AGAINST}\n` }
      },
      onDecided: (compatibility) => {
        // The compatibility, not a mode: the caller stores the whole answer.
        order.push(`decided:${compatibility.mode}`)
        // And there is no window yet -- so nothing can have asked for a handler.
        expect(factory.options).toHaveLength(0)
      },
    })
    order.push('loaded')
    expect(order).toEqual(['read', 'decided:ok', 'loaded'])
    expect(startup.window).not.toBeNull()
  })

  it('tells the caller a reduced answer too, before that window loads', async () => {
    // The reduced mode takes the same path as `ok`: the dashboard document is
    // loaded, its renderer runs, and it will ask for the handshake on its first
    // line -- so the handlers have to be there for it just as much.
    const order: string[] = []
    const startup = await startupWindow({
      BrowserWindow: fakeWindowFactory().BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: async () => {
        order.push('read')
        return { ok: true, stdout: '0.6.9\n' }
      },
      onDecided: (compatibility) => order.push(`decided:${compatibility.mode}`),
    })
    order.push('loaded')
    expect(order).toEqual(['read', 'decided:reduced', 'loaded'])
    expect(startup.window).not.toBeNull()
  })

  it('tells the caller a refused answer, and builds nothing for it', async () => {
    // The refusal is the one mode where the callback's *job* is to do nothing
    // (see `onDecided` in `src/main/index.ts`): a refused console registers no
    // handler at all. It is called all the same, because the caller is what
    // decides that -- and it is called before any window exists, so a console
    // that refused has nothing to take back.
    const answered: CliCompatibility[] = []
    const factory = fakeWindowFactory()
    const startup = await startupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: nothingToRead,
      onDecided: (compatibility) => {
        answered.push(compatibility)
        expect(factory.options).toHaveLength(0)
      },
    })
    expect(answered.map((compatibility) => compatibility.mode)).toEqual(['refused'])
    expect(startup.window).toBeNull()
    expect(factory.options).toHaveLength(0)
    expect(factory.loaded).toEqual([])
  })

  it('is optional, so a caller that wants one window needs no callback', async () => {
    // Every test above this one drives `startupWindow` with no `onDecided`, which
    // is the assertion: a required callback would make this file unbuildable
    // rather than failing here, and this case is written out so that the
    // optionality is a decision a reader can see.
    const startup = await startupWindow({
      BrowserWindow: fakeWindowFactory().BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: async () => ({ ok: true, stdout: `${CLI_VERSION_BUILT_AGAINST}\n` }),
    })
    expect(startup.window).not.toBeNull()
  })
})

describe('createRefusalWindow', () => {
  it('builds the hardened window, with a title that says what it refused', async () => {
    const factory = fakeWindowFactory()
    const session = fakeSession()
    const compatibility = classifyCliVersion('0.5.0')
    const built = await createRefusalWindow(
      { BrowserWindow: factory.BrowserWindow, session: session.value, preloadPath: PRELOAD_PATH },
      compatibility,
    )
    expect(factory.built).toEqual([built])
    expect(factory.options).toHaveLength(1)
    expect(factory.options[0]?.title).toContain(SKEW_HEADLINES.refused)
    expect(factory.built[0]?.title).toContain(SKEW_HEADLINES.refused)
    // The hardened set and **no `preload` key at all** (Task 3.4a, step 1): a
    // refused console has no renderer entry to run and nothing to ask for, so a
    // bridge there would be a surface on a window that must draw nothing.
    expect(factory.options[0]?.webPreferences).toEqual(consoleWebPreferences())
    expect(factory.options[0]?.webPreferences).not.toHaveProperty('preload')
    expect(factory.options[0]?.show).toBe(false)
    // The same single load over the console's own scheme, the same handlers and
    // the same denied permissions as the dashboard window: the refusal screen is
    // drawn by the renderer in a window that is hardened exactly as much. What
    // differs is *which document*: the refusal document, and never the
    // dashboard's (Task 3.3, D7 -- "a refused version never shows the
    // dashboard", which is a claim about the document the window loaded as much
    // as about what the renderer drew).
    expect(factory.loaded).toEqual([REFUSAL_ENTRY_URL])
    expect(factory.loaded).not.toContain(APP_ENTRY_URL)
    expect(REFUSAL_ENTRY_URL).not.toBe(APP_ENTRY_URL)
    expect(session.requestHandler()).not.toBeNull()
    expect(session.checkHandler()).not.toBeNull()
    const navigate = recordingEvent()
    factory.contents[0]?.emit('will-navigate', navigate, 'https://example.invalid/fugaro')
    expect(navigate.prevented).toBe(true)
    expect(factory.contents[0]?.windowOpenHandler()?.()).toEqual({ action: 'deny' })
  })
})

describe('the refusal document in the bundle', () => {
  it('carries the CSP of rule 3, byte for byte, exactly as the dashboard document does', () => {
    // The `<meta>` copy is the belt to the header's braces, and the header is
    // the same one: both documents are served by the same `app://` handler. A
    // refusal document with a weaker policy would be the one document a
    // below-the-floor console ever shows being the least constrained.
    expect(CONTENT_SECURITY_POLICY).toBe(DESIGN_CSP)
    expect(refusalHTML).toContain(`content="${CONTENT_SECURITY_POLICY}"`)
    expect(indexHTML).toContain(`content="${CONTENT_SECURITY_POLICY}"`)
  })

  it('is not the dashboard document, and holds nothing a renderer could draw a figure into', () => {
    // D7's whole content: "a refused version never shows the dashboard". The
    // strongest form of it is that the document a refused console loads is a
    // different file with no dashboard in it at all.
    expect(REFUSAL_ENTRY_URL).toBe(`${APP_ORIGIN}/refusal.html`)
    expect(refusalHTML).not.toBe(indexHTML)
    // The markup, and not the whole file: this document's comment says in prose
    // what Task 3.4a did to `index.html`'s script, and a comment is not an
    // element. What a refused console runs stays the question, and the answer
    // stays none.
    const markup = refusalHTML.replace(/<!--[\s\S]*?-->/g, '')
    expect(markup).not.toContain('<script')
    expect(markup).not.toContain('<style')
    // The console's own sentences, and no version, no path and nothing the CLI
    // printed: the document is static, so what it says cannot depend on the
    // binary that was found.
    expect(refusalHTML).toContain(`<title>${CONSOLE_WINDOW_TITLE}</title>`)
    expect(refusalHTML).toContain('will not draw a dashboard')
    expect(markup, 'the markup names no version').not.toMatch(/\d+\.\d+/)
    expect(markup).not.toContain('0.8')
  })

  it('is served from the same root, under the same scheme and with the same header', async () => {
    expect(resolveBundleRequest(bundleRoot, REFUSAL_ENTRY_URL)).toEqual({
      ok: true,
      filePath: join(bundleRoot, 'refusal.html'),
      mediaType: 'text/html; charset=utf-8',
    })
    const response = await serveAppRequest(bundleRoot, REFUSAL_ENTRY_URL)
    expect(response.status).toBe(200)
    expect(response.headers.get(CSP_HEADER)).toBe(CONTENT_SECURITY_POLICY)
    expect(response.headers.get('content-type')).toBe('text/html; charset=utf-8')
    expect(response.headers.get('x-content-type-options')).toBe('nosniff')
    await expect(response.text()).resolves.toBe(refusalHTML)
  })

  it('is a real file in the tree the handler serves, checked against the working tree', () => {
    // `bundleRoot` is this checkout's `src/renderer`, and the read above would
    // have thrown had the document not been added to the bundle at all.
    // The licence notice every tracked file starts with is an HTML comment here,
    // so the doctype follows it.
    const doctypeAfterNotice = /^\s*(<!--[\s\S]*?-->\s*)*<!doctype html>/i
    expect(doctypeAfterNotice.test(refusalHTML)).toBe(true)
    expect(doctypeAfterNotice.test(indexHTML)).toBe(true)
  })
})

describe('openWindowFor', () => {
  it('opens the dashboard document for anything but a refusal', async () => {
    for (const mode of ['ok', 'reduced'] as const) {
      const factory = fakeWindowFactory()
      const version = mode === 'ok' ? CLI_VERSION_BUILT_AGAINST : '0.9.0'
      const window = await openWindowFor(
        { BrowserWindow: factory.BrowserWindow, session: fakeSession().value, preloadPath: PRELOAD_PATH },
        classifyCliVersion(version),
      )
      expect(factory.built, mode).toEqual([window])
      expect(factory.loaded, mode).toEqual([APP_ENTRY_URL])
      // The title carries the compatibility that was **stored**, not the
      // console's plain one: a reduced console that comes back after a
      // dock-icon click still says which console it is. Built through
      // `createConsoleWindow` (whose compatibility is `null`) this line is the
      // one that fails -- and it is the only assertion in the file that would.
      expect(factory.options[0]?.title, mode).toBe(
        mode === 'ok' ? CONSOLE_WINDOW_TITLE : `${CONSOLE_WINDOW_TITLE} — ${SKEW_HEADLINES.reduced}`,
      )
    }
  })

  it('opens the refusal document for a refusal, and never the dashboard', async () => {
    const factory = fakeWindowFactory()
    await openWindowFor(
      { BrowserWindow: factory.BrowserWindow, session: fakeSession().value, preloadPath: PRELOAD_PATH },
      classifyCliVersion('0.5.0'),
    )
    expect(factory.loaded).toEqual([REFUSAL_ENTRY_URL])
    expect(factory.options[0]?.title).toContain(SKEW_HEADLINES.refused)
  })
})

describe('openStartupWindow', () => {
  it('runs the handshake before any window exists, and opens the right one for its answer', async () => {
    const factory = fakeWindowFactory()
    const order: string[] = []
    const opened = await openStartupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: async () => {
        // The claim this task is about: the version is read *before* anything
        // is constructed, so a refused console decides on the document before a
        // dashboard window could exist.
        order.push(`read (${factory.built.length} windows built)`)
        return { ok: true, stdout: '0.5.0\n' }
      },
    })
    expect(order).toEqual(['read (0 windows built)'])
    expect(opened.compatibility.mode).toBe('refused')
    expect(factory.loaded).toEqual([REFUSAL_ENTRY_URL])
    expect(factory.loaded).not.toContain(APP_ENTRY_URL)
  })

  it('opens the dashboard window for a supported CLI, and does not open a second one', async () => {
    const factory = fakeWindowFactory()
    const opened = await openStartupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: async () => ({ ok: true, stdout: `${CLI_VERSION_BUILT_AGAINST}\n` }),
    })
    expect(opened.compatibility.mode).toBe('ok')
    expect(factory.options).toHaveLength(1)
    expect(factory.loaded).toEqual([APP_ENTRY_URL])
    expect(opened.window).toBe(factory.built[0])
  })

  it('refuses a console it could not identify, which is the answer that draws no figures', async () => {
    const factory = fakeWindowFactory()
    const opened = await openStartupWindow({
      BrowserWindow: factory.BrowserWindow,
      session: fakeSession().value, preloadPath: PRELOAD_PATH,
      readVersion: nothingToRead,
    })
    expect(opened.compatibility.mode).toBe('refused')
    expect(opened.compatibility.skew).toBe('unreadable')
    // `because` is the console's fixed sentence for the reader's category --
    // never the resolver's reason, which can quote what a child printed.
    expect(opened.compatibility.because).toBe(HANDSHAKE_NOT_FOUND)
    expect(factory.loaded).toEqual([REFUSAL_ENTRY_URL])
  })
})
