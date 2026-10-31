// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's crash reporter, held to its two rules (desktop-console plan
// Task 2.1, the first slice; D733, D734; design sections 7.3 and 7.4).
//
// The plan's task covers a package, a wiring and a public statement; this slice
// is the module underneath them and nothing else. What is held here is what the
// wiring will not be able to change once it exists:
//
//   1. **Nothing is sent before the switch is read** (D734). The decision has
//      three inputs, not two: the switch may be `true`, `false`, or unread, and
//      the unread case is off rather than assumed on. The red step of the task is
//      the stub below, a console that asks the question at launch with a DSN in
//      hand -- a reporter built on that answer sends nothing, because the answer
//      is `off` until something has read the setting.
//   2. **A report is a type and frames, and nothing else** (D734's payload
//      rule, F33's finding). F33 is why this is an allow-list rather than a
//      deny-list: a Go panic value, an Electron error message and a JS exception
//      string all carry repository names, task text and credentials, and the
//      repository's own redactor is best-effort by its comment. So the whole
//      message goes, and the frames are rebuilt from the four safe fields, which
//      is also why the headline case below is a **mutation** proof: the one edit
//      that keeps `value` has to turn it red.
//
// The module under test is pure: it reads no environment, opens no file, calls
// nothing, and holds no DSN, host or endpoint. The scan in `holds no
// environment` state that as a property of the source rather than of a run,
// because "no reporting host" is a claim about a file that no test which merely
// calls a function can make.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { inert } from '../support/fakeSecrets'

import {
  CRASH_CLIENT_OPTIONS,
  crashReportingDecision,
  scrubCrashEvent,
  type CrashEvent,
  type ScrubbedFrame,
} from '../../src/main/crash'

/** DSN_PLACEHOLDER is a DSN's shape -- scheme, public key, host, project id --
 * assembled from fragments at run time, for the reason `fakeSecrets` exists: a
 * DSN-shaped literal in this repository is a defect even when it is invented. The
 * host is `example.invalid`, the name reserved for exactly this, so nothing here
 * resolves anywhere and no reporting host is named by this file. */
function dsnPlaceholder(): string {
  return inert('https', '://', 'a'.repeat(32), '@', 'example', '.invalid', '/', '1')
}

/** APP_ROOT is the directory the console's own code is loaded from, as a
 * developer's checkout has it. It is a placeholder path rather than a real one:
 * the tests below are about which part of a path survives, and a path under a
 * home directory is the case the scrubber exists to remove. */
const APP_ROOT = '/opt/console-checkout'

/** PACKAGED_ROOT is the same app installed, where the code sits inside an
 * `app.asar` archive under the resources directory. */
const PACKAGED_ROOT = '/opt/console-resources'

/** HOME_PATH is a path under a home directory, with the placeholder user name
 * `person-1`: the shape that must never reach the processor. */
const HOME_PATH = '/Users/person-1'

/** HOST_A and REPO_A are a machine name and a repository name, both
 * placeholders: what an error message carries when it is a real one. */
const HOST_A = 'build-host-a'
const REPO_A = 'owner/repo-a'

/** DIRECTORY_A is a placeholder directory name -- what a checkout is called on
 * the machine that built the app, and the part of a path a URL must not carry
 * out of a frame. */
const DIRECTORY_A = 'acme-secret-repo'

/** TOKEN_A is a credential-shaped placeholder, assembled here. */
function tokenPlaceholder(): string {
  return inert('gh', 'p_', 'a'.repeat(36))
}

/** HostileEvent is an event as a reporter builds one: the fields the scrubber
 * names, and the ones it does not -- which is why this type is wider than
 * `CrashEvent`. `CrashEvent` is deliberately narrow (what the module reads), and
 * a literal with a `user`, a `contexts` and a `breadcrumbs` on it is the case
 * the scrubber exists for rather than a shape the module should be taught. */
type HostileEvent = CrashEvent & Record<string, unknown>

/** startupStub stands in for the console that starts reporting at launch: it
 * asks the gate once, at startup, holding the DSN the build was given, and would
 * initialise the client if the answer were `enabled`. The switch has not been
 * read at that moment, which is the plan's red step -- so what the stub gets back
 * is `off`, and nothing is ever sent. */
function startupStub(dsn: string | null): { readonly started: boolean } {
  return { started: crashReportingDecision({ dsn, optedIn: null }).enabled }
}

/** scrubbedFrame is one frame through the whole scrubber, which is the only
 * route a frame's path has: a case about a path is a case about the event it
 * arrives in. */
function scrubbedFrame(abs_path: string, filename?: string, appRoot = APP_ROOT): ScrubbedFrame {
  const scrubbed = scrubCrashEvent(
    { exception: { values: [{ type: 'Error', stacktrace: { frames: [{ abs_path, filename }] } }] } },
    appRoot,
  )
  const kept = scrubbed.exception?.values?.[0]?.stacktrace?.frames?.[0]
  // Every frame in is a frame out: the stack's shape is what the processor
  // groups by, so a frame this module cannot place is `<external>` rather than a
  // hole in the middle of a report.
  if (kept === undefined) throw new Error('the frame was dropped')
  return kept
}

describe('crash', () => {
  it('nothing is enabled before the switch is read', () => {
    expect(startupStub(dsnPlaceholder())).toEqual({ started: false })
    expect(startupStub(null)).toEqual({ started: false })
    expect(crashReportingDecision({ dsn: dsnPlaceholder(), optedIn: null })).toEqual({
      enabled: false,
      reason: 'opt-in-not-read',
    })
  })

  it('nothing is enabled with the switch off', () => {
    expect(crashReportingDecision({ dsn: dsnPlaceholder(), optedIn: false })).toEqual({
      enabled: false,
      reason: 'opt-in-off',
    })
  })

  it('nothing is enabled without a DSN that is one, whatever the switch says', () => {
    // The dormant build the owner ruled for: the switch may be on and the DSN
    // missing, empty or whitespace, and the answer is still off -- so a release
    // that was built without the value sends nothing rather than failing.
    for (const dsn of [undefined, null, '', '   ', '\t\n']) {
      expect(crashReportingDecision({ dsn, optedIn: true })).toEqual({
        enabled: false,
        reason: 'no-dsn',
      })
    }
    // The reason is a word for a log line and never the value, which is a URL
    // with a key in it.
    const decision = crashReportingDecision({ dsn: dsnPlaceholder(), optedIn: true })
    expect(JSON.stringify(decision)).not.toContain(dsnPlaceholder())
  })

  it('enabled only when the switch is read, is on, and the build carries a DSN', () => {
    expect(crashReportingDecision({ dsn: dsnPlaceholder(), optedIn: true })).toEqual({
      enabled: true,
      reason: 'enabled',
    })
  })

  it('holds no environment read, no file, no network call and no endpoint', () => {
    const source = readFileSync(fileURLToPath(new URL('../../src/main/crash.ts', import.meta.url)), 'utf8')
    const code = source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
    const offences: string[] = []
    for (const [what, re] of [
      ['an environment read', /\bprocess\s*(?:\.\s*env\b|\[\s*['"]env['"]\s*\])/],
      ['a file read', /\b(?:readFile|readFileSync|readdir|createReadStream|openSync)\b/],
      ['a network call', /\b(?:fetch|XMLHttpRequest|net\.connect|https?\.request)\s*\(/],
      ['a URL', /https?:\/\//],
      ['an import', /^\s*import\b/m],
    ] as const) {
      if (re.test(code)) offences.push(`${what} (${String(re)})`)
    }
    // The scan is only about this file if it read this file: a path that moved
    // would otherwise leave it passing on an empty string.
    expect(code).toContain('scrubCrashEvent')
    expect(offences).toEqual([])
  })

  it('the scrubber drops message, home path, hostname, env and token-shaped placeholder', () => {
    const token = tokenPlaceholder()
    const event: HostileEvent = {
      event_id: '0'.repeat(32),
      timestamp: 1767225600,
      platform: 'node',
      level: 'error',
      release: 'console-0.116.0',
      message: `failed to reach ${REPO_A} with ${token}`,
      user: { id: 'person-1', username: 'person-1', ip_address: '203.0.113.7' },
      server_name: HOST_A,
      contexts: {
        device: { name: HOST_A, arch: 'arm64' },
        os: { name: 'macOS', version: '15.0' },
        runtime: { name: 'node' },
      },
      tags: { repository: REPO_A, home: `${HOME_PATH}/dev` },
      extra: { task: `push ${REPO_A}`, home: HOME_PATH, token },
      breadcrumbs: [{ message: `read ${HOME_PATH}/.config/fugaro/credentials.yaml`, data: { token } }],
      request: { url: `https://example.invalid/${REPO_A}` },
      modules: { 'dep-a': '1.2.3' },
      environment: 'production',
      sdk: { name: 'a-sdk', version: '1.2.3' },
      debug_meta: { images: [{ code_file: `${HOME_PATH}/app` }] },
      exception: {
        values: [
          {
            type: 'Error',
            value: `EACCES pushing ${REPO_A} from ${HOME_PATH} with ${token}`,
            mechanism: { type: 'generic', handled: false, data: { token } },
            module: 'console:main',
            stacktrace: {
              frames: [
                {
                  function: 'main',
                  filename: 'index.js',
                  abs_path: `${APP_ROOT}/dist/main/index.js`,
                  lineno: 12,
                  colno: 4,
                  in_app: true,
                  context_line: `const token = '${token}'`,
                  pre_context: [HOME_PATH],
                  post_context: [REPO_A],
                  vars: { token, host: HOST_A },
                  module: 'main',
                },
                {
                  function: `${APP_ROOT}/dist/main/gone.js`,
                  abs_path: `${APP_ROOT}/dist/main/gone.js`,
                  lineno: 1,
                  colno: 1,
                  in_app: true,
                },
                {
                  function: 'run',
                  filename: `${HOME_PATH}/dev/dep-a/index.js`,
                  abs_path: '/opt/vendor/node_modules/dep-a/index.js',
                  lineno: 90,
                  colno: 2,
                  in_app: false,
                },
                {
                  function: 'stray',
                  abs_path: `${HOME_PATH}/dev/elsewhere/dist/main/stray.js`,
                  lineno: 3,
                  colno: 1,
                  in_app: true,
                },
              ],
            },
          },
        ],
      },
    }

    const scrubbed = scrubCrashEvent(event, APP_ROOT)
    const serialised = JSON.stringify(scrubbed)

    // Nothing that names a person, a machine, a repository or a credential
    // survives -- not in a message, not in a path, not in an os context.
    for (const secret of [token, HOME_PATH, 'person-1', HOST_A, REPO_A, '203.0.113.7']) {
      expect(serialised).not.toContain(secret)
    }
    expect(serialised).not.toContain('credentials.yaml')
    expect(serialised).not.toContain('EACCES')

    // And what survives is exactly the allow-listed fields: a report is the
    // type, the level and the frames.
    expect(scrubbed).toEqual({
      event_id: '0'.repeat(32),
      timestamp: 1767225600,
      platform: 'node',
      level: 'error',
      release: 'console-0.116.0',
      exception: {
        values: [
          {
            type: 'Error',
            stacktrace: {
              frames: [
                {
                  function: 'main',
                  filename: 'dist/main/index.js',
                  lineno: 12,
                  colno: 4,
                  in_app: true,
                },
                {
                  function: '<anonymous>',
                  filename: 'dist/main/gone.js',
                  lineno: 1,
                  colno: 1,
                  in_app: true,
                },
                {
                  function: 'run',
                  filename: '<external>',
                  lineno: 90,
                  colno: 2,
                  in_app: false,
                },
                {
                  function: 'stray',
                  filename: '<external>',
                  lineno: 3,
                  colno: 1,
                  in_app: true,
                },
              ],
            },
          },
        ],
      },
    })

    // The input is read and never edited: the caller's event is the SDK's own
    // object, and a scrubber that emptied it would break the reporter it runs
    // inside.
    expect(event.exception?.values?.[0]?.value).toContain('EACCES')
    expect(event.exception?.values?.[0]?.stacktrace?.frames?.[0]?.context_line).toContain(token)
    expect(event.breadcrumbs).toHaveLength(1)
  })

  it('the scrubber keeps a frame of the app and marks every other one external', () => {
    // A frame inside the app, however the path is spelled: a Windows install's
    // separators are the same frame as a macOS one's, because the output is
    // POSIX-separated either way.
    expect(scrubbedFrame(`${APP_ROOT}/dist/main/index.js`).filename).toBe('dist/main/index.js')
    expect(scrubbedFrame('C:\\console\\dist\\main\\index.js', undefined, 'C:\\console').filename).toBe(
      'dist/main/index.js',
    )
    // A path still carrying a home directory after the root came off is out --
    // the root is not trusted to be the only place a user name hides.
    expect(scrubbedFrame(`${APP_ROOT}/vendor/home/person-1/leak.js`).filename).toBe('<external>')
    expect(
      scrubbedFrame(
        'C:\\Users\\person-1\\AppData\\Local\\console\\resources\\app.asar\\..\\..\\..\\Users\\person-1\\notes.txt',
      ).filename,
    ).toBe('<external>')
    // Anything outside the app is the fixed word, and never the path.
    expect(scrubbedFrame('/opt/vendor/node_modules/dep-a/index.js').filename).toBe('<external>')
    expect(scrubbedFrame(`${HOME_PATH}/dev/elsewhere/main.js`).filename).toBe('<external>')
    // A filename the SDK already made relative is kept as it is, and a frame
    // with no path at all is kept and not placed.
    expect(scrubbedFrame('', 'dist/main/index.js').filename).toBe('dist/main/index.js')
    expect(scrubbedFrame('', undefined)).toEqual({ filename: '<external>' })
  })

  it('the scrubber cuts a frame path that arrives as a URL, and refuses a URL that is not a file', () => {
    // A path is not always a path: a build reports frames as `file://` URLs as
    // often as it reports them bare, and an Electron frame comes as an `app://`
    // one. Both name a file, so the scheme comes off and what is left is
    // scrubbed exactly as a bare path is -- the same cut, the same separator
    // handling, the same refusal of a home directory.
    expect(scrubbedFrame(`file://${APP_ROOT}/dist/main/index.js`).filename).toBe('dist/main/index.js')
    expect(scrubbedFrame(`FILE://${APP_ROOT}/dist/main/index.js`).filename).toBe('dist/main/index.js')
    expect(scrubbedFrame(`file://${PACKAGED_ROOT}/app.asar/dist/main/index.js`, undefined, PACKAGED_ROOT).filename).toBe(
      'dist/main/index.js',
    )
    expect(scrubbedFrame(`file://${APP_ROOT}/vendor/home/person-1/leak.js`).filename).toBe('<external>')

    // Every other scheme is refused whole, and that is the point rather than a
    // tidiness rule: what follows the colon is a host, a query or a body, none
    // of which is a frame's to report, and a path smuggled inside one of them
    // would be a directory name this module never looked at.
    const refused = [
      `file:///opt/${DIRECTORY_A}/main.js`,
      `file:///D:/work/${DIRECTORY_A}/main.js`,
      `file:///C:\\work\\${DIRECTORY_A}\\main.js`,
      `FILE:///opt/${DIRECTORY_A}/main.js`,
      `app:///workspace/${DIRECTORY_A}/main.js`,
      `APP:///workspace/${DIRECTORY_A}/main.js`,
      `https://${HOST_A}/${DIRECTORY_A}/x.js`,
      `data:text/javascript,//${DIRECTORY_A}/main.js`,
      `blob:https://${HOST_A}/${DIRECTORY_A}/0f1e`,
      `my-scheme://${DIRECTORY_A}/main.js`,
    ]
    const kept = refused.map((source) => scrubbedFrame(source).filename)
    expect(JSON.stringify(kept)).not.toContain(DIRECTORY_A)
    expect(kept).toEqual(refused.map(() => '<external>'))
    // The archive URL a packaged Electron frame arrives under is a path once its
    // scheme is off, and it is nobody's root here, so it is the fixed word: what
    // places such a frame is the absolute path beside it, which the reporter
    // carries for exactly this reason.
    expect(scrubbedFrame('app:///dist/main/index.js').filename).toBe('<external>')

    // A drive letter is not a one-letter scheme: reading `C:` as one would take
    // the Windows form of the frame the case above keeps, which is a path the
    // console does have and does want.
    expect(scrubbedFrame('C:\\console\\dist\\main\\index.js', undefined, 'C:\\console').filename).toBe(
      'dist/main/index.js',
    )
  })

  it('the scrubber reads the app root and the packaged path', () => {
    const packaged = (abs_path: string): string | undefined =>
      scrubCrashEvent(
        { exception: { values: [{ type: 'Error', stacktrace: { frames: [{ abs_path }] } }] } },
        PACKAGED_ROOT,
      ).exception?.values?.[0]?.stacktrace?.frames?.[0]?.filename

    // Packaged, the code is inside the archive: what is kept is the part after
    // `app.asar/`, because the archive's own directory is a machine's.
    expect(packaged(`${PACKAGED_ROOT}/app.asar/dist/main/index.js`)).toBe('dist/main/index.js')
    expect(packaged(`${PACKAGED_ROOT}/app.asar/dist/main/crash.js`)).toBe('dist/main/crash.js')
    // A file under the root but outside the archive is still the app's own code,
    // and a path under neither is not.
    expect(packaged(`${PACKAGED_ROOT}/other/index.js`)).toBe('other/index.js')
    expect(packaged('/opt/vendor/node_modules/dep-a/index.js')).toBe('<external>')
  })

  it('no minidump, no session and no client report', () => {
    // Each option is pinned by name, because the wiring's whole safety is that
    // the SDK is told these things rather than trusted to default to them.
    expect(CRASH_CLIENT_OPTIONS).toEqual({
      sendDefaultPii: false,
      autoSessionTracking: false,
      sendClientReports: false,
      attachStacktrace: false,
      maxBreadcrumbs: 0,
      includeLocalVariables: false,
      defaultIntegrations: false,
    })
    expect(Object.isFrozen(CRASH_CLIENT_OPTIONS)).toBe(true)
  })
})
