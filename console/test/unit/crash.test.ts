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
//      is `off` until something has read the setting. The gate is strict as well
//      as ordered: one value opens it, and a document that spells the switch any
//      other way -- `"true"`, `1`, `"yes"`, an object -- is an answer this
//      console cannot read as permission.
//   2. **A report is a type and frames, and nothing else** (D734's payload
//      rule, F33's finding). F33 is why this is an allow-list rather than a
//      deny-list: a Go panic value, an Electron error message and a JS exception
//      string all carry repository names, task text and credentials, and the
//      repository's own redactor is best-effort by its comment. So the whole
//      message goes, and the frames are rebuilt from the four safe fields, which
//      is also why the headline case below is a **mutation** proof: the one edit
//      that keeps `value` has to turn it red.
//   3. **What cannot be placed is refused** (fix round 2). The scrubber is the
//      last thing between a frame and the wire, and every input it cannot read is
//      a case for the fixed word: a path that is relative rather than rooted, that
//      walks back out of the app, that carries a host where the path belongs, a
//      percent-escape or a separator left over from another platform, and a value
//      that is not the shape it is claimed to be. Where it keeps something it
//      keeps the least it can -- a type and a function name only when they read
//      as names -- and the traversal case below was that round's **mutation**
//      proof: the refusal of a traversing path is one edit away from a directory
//      name on the wire.
//   4. **The rest of the input is refused too** (fix round 3, T2.1c). A URL's
//      query or fragment, a control character, an archive marker that is only part
//      of a segment, an event string that is not the shape it claims, a getter
//      that throws and a position that is not a finite number are each refused
//      rather than trimmed or trusted -- and the archive case below is this
//      round's **mutation** proof: a search that finds `app.asar/` inside a
//      segment, rather than as one, puts `acme-merger/x.js` on the wire.
//   5. **Every field is read once, and each value is held to its own range**
//      (fix round 4, T2.1d). A property of a library's event can be a getter, and
//      a getter is free to answer differently on each read -- so a field read
//      once for the check and again for the copy is checked as one value and sent
//      as another. Every field, at every level, is therefore read once into a
//      local that is both checked and copied; a timestamp is kept only when it is
//      finite, a position only when it is a whole number of at least zero; and
//      the line breaks and invisible marks Unicode spells outside ASCII are
//      refused beside the ASCII control characters. The read-once case below is
//      this round's **mutation** proof: with the copy reading `event_id` from the
//      event again, the token-shaped placeholder is on the wire in place of the
//      id.
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
  type CrashFrame,
  type ScrubbedCrashEvent,
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

/** PROSE_A is a name-shaped string that is really a sentence: an exception's
 * `type` and a frame's `function` are strings a library filled in, and a library
 * can be handed a task title. */
const PROSE_A = 'acme secret task text'

/** poisonType is an exception type that is a path and a credential rather than a
 * name -- what a library that was handed a message puts in `type`. */
function poisonType(): string {
  return `${HOME_PATH}/acme-secret: ${inert('tok', 'en-xyz')}`
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
 * read at that moment -- which is the plan's red step, and why the field is not
 * in the call at all: `undefined` is the absence of an answer, while `null` is an
 * answer that is not `true`. So what the stub gets back is `off`, and nothing is
 * ever sent. */
function startupStub(dsn: string | null): { readonly started: boolean } {
  return { started: crashReportingDecision({ dsn }).enabled }
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

/** positioned is one frame's position through the whole scrubber: `lineno` and
 * `colno` as a library may have written them, which is why they arrive here as
 * `unknown` rather than as the numbers the type claims. */
function positioned(lineno: unknown, colno: unknown): ScrubbedFrame | undefined {
  const frame = { abs_path: `${APP_ROOT}/dist/main/index.js`, lineno, colno }
  return scrubCrashEvent(
    { exception: { values: [{ type: 'Error', stacktrace: { frames: [frame as unknown as CrashFrame] } }] } },
    APP_ROOT,
  ).exception?.values?.[0]?.stacktrace?.frames?.[0]
}

/** proxyThrowingOn is an event whose named property is a getter that throws. The
 * reporter's event is a library's object rather than a plain one, and a property
 * of one can be a `Proxy` whose handler throws. */
function proxyThrowingOn(property: string): CrashEvent {
  const event: Record<string, unknown> = { level: 'error' }
  return new Proxy(event, {
    get(target, key, receiver) {
      if (key === property) throw new Error(`a throwing getter on ${property}`)
      return Reflect.get(target, key, receiver)
    },
  }) as unknown as CrashEvent
}

/** throwingValues is the same case one level down: the exception's own `values`
 * accessor throws, so the read that decides whether there is an exception at all
 * is the one that goes wrong. */
function throwingValues(): CrashEvent {
  const exception: Record<string, unknown> = {}
  Object.defineProperty(exception, 'values', {
    get() {
      throw new Error('a throwing getter on values')
    },
    enumerable: true,
  })
  return { exception } as unknown as CrashEvent
}

/** throwingFrameField is the same case again, inside a frame: the guard around
 * each frame field keeps a value of the wrong type out, and a guard is not what
 * stops a getter. */
function throwingFrameField(): CrashEvent {
  const frame: Record<string, unknown> = { abs_path: `${APP_ROOT}/dist/main/index.js` }
  Object.defineProperty(frame, 'abs_path', {
    get() {
      throw new Error('a throwing getter on abs_path')
    },
    enumerable: true,
  })
  return { exception: { values: [{ type: 'Error', stacktrace: { frames: [frame] } }] } } as unknown as CrashEvent
}

/** changingOnSecondRead is an object whose named property answers `first` when
 * it is read once and `second` on every read after that. A property of a
 * library's event is not necessarily a value: a lazily computed field and a
 * `Proxy` are both free to answer differently each time, and a field read twice
 * -- once to check it and once to copy it -- is then checked as one value and
 * sent as another. `base` is where the getter is defined, for a case that needs
 * the frame's other fields beside the changing one. */
function changingOnSecondRead(
  property: string,
  first: unknown,
  second: unknown,
  base: Record<string, unknown> = {},
): Record<string, unknown> {
  let reads = 0
  Object.defineProperty(base, property, {
    get() {
      reads += 1
      return reads === 1 ? first : second
    },
    enumerable: true,
  })
  return base
}

describe('crash', () => {
  it('nothing is enabled before the switch is read', () => {
    expect(startupStub(dsnPlaceholder())).toEqual({ started: false })
    expect(startupStub(null)).toEqual({ started: false })
    // The unread switch is the field's absence, and it reports itself as unread
    // rather than as an operator who said no: D734's third state is worth a word
    // of its own precisely so that a log line can tell the two apart.
    expect(crashReportingDecision({ dsn: dsnPlaceholder(), optedIn: undefined })).toEqual({
      enabled: false,
      reason: 'opt-in-not-read',
    })
    expect(crashReportingDecision({ dsn: dsnPlaceholder() })).toEqual({
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

  it('only `true` opens the gate, whatever a document spells the switch with', () => {
    // The value comes from a settings node this console did not write, and a
    // truthy value is not consent: `"true"` is a string, `1` is a number, `{}` is
    // an object, and none of them is the operator saying yes. `null` is an answer
    // that is not permission -- an installation that stores the node and leaves it
    // empty -- while `undefined` is no answer at all, which the case above reports
    // as unread.
    const dsn = dsnPlaceholder()
    for (const optedIn of ['true', 'false', 'yes', 1, 0, {}, []]) {
      expect(crashReportingDecision({ dsn, optedIn }), JSON.stringify(optedIn)).toEqual({
        enabled: false,
        reason: 'opt-in-off',
      })
    }
    expect(crashReportingDecision({ dsn, optedIn: null })).toEqual({ enabled: false, reason: 'opt-in-off' })
    expect(crashReportingDecision({ dsn, optedIn: true })).toEqual({ enabled: true, reason: 'enabled' })
    // ...and no DSN in the build is still the last word, so a document cannot
    // smuggle its way past the two lines above either.
    expect(crashReportingDecision({ dsn: undefined, optedIn: 'true' })).toEqual({
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
    // A filename the SDK already made relative goes with the other relative
    // paths -- a relative path names a file next to something this module never
    // learned -- and a frame with no path at all is kept and not placed.
    expect(scrubbedFrame('', 'dist/main/index.js').filename).toBe('<external>')
    expect(scrubbedFrame('', undefined)).toEqual({ filename: '<external>' })
  })

  it.each([
    ['NaN', Number.NaN],
    ['Infinity', Number.POSITIVE_INFINITY],
    ['a fraction', 1.5],
    ['a negative', -1],
    ['a numeric string', '12'],
  ] as [string, unknown][])('a lineno that is %s is dropped', (_name, value) => {
    expect(positioned(value, 4)).toEqual({ filename: 'dist/main/index.js', colno: 4 })
  })

  it.each([
    ['NaN', Number.NaN],
    ['Infinity', Number.POSITIVE_INFINITY],
    ['a fraction', 1.5],
    ['a negative', -1],
    ['a numeric string', '12'],
  ] as [string, unknown][])('a colno that is %s is dropped', (_name, value) => {
    expect(positioned(12, value)).toEqual({ filename: 'dist/main/index.js', lineno: 12 })
  })

  it('a position that is a whole number of at least zero is kept', () => {
    // The other half of the two rules above: a position is a place in a file,
    // and a whole number is one -- so what the reporter wrote is reported, and
    // only what JSON would have written as `null`, and the fractions and
    // negatives a file has no line at, are dropped.
    expect(positioned(12, 4)).toEqual({ filename: 'dist/main/index.js', lineno: 12, colno: 4 })
    // Zero is the first line and the first column, which is a position a build
    // really writes for a frame it could not walk into -- so it is kept, and it
    // is the case a truthiness check would have dropped.
    expect(positioned(0, 0)).toEqual({ filename: 'dist/main/index.js', lineno: 0, colno: 0 })
  })

  it.each([
    ['NaN', Number.NaN],
    ['Infinity', Number.POSITIVE_INFINITY],
    ['-Infinity', Number.NEGATIVE_INFINITY],
    ['a numeric string', '1767225600'],
  ] as [string, unknown][])('a timestamp that is %s is dropped', (_name, value) => {
    // A timestamp is the one number on the event, and JSON writes all three of
    // `NaN` and the infinities as `null` -- so a kept one reaches the processor
    // as a time this module never checked rather than as a time it did.
    expect(scrubCrashEvent({ timestamp: value }, APP_ROOT)).toEqual({})
  })

  it('a timestamp that is a finite number is kept', () => {
    expect(scrubCrashEvent({ timestamp: 1767225600 }, APP_ROOT)).toEqual({ timestamp: 1767225600 })
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

  it('a file URL that names a host is refused, and only the empty-authority form is a path', () => {
    // `file:///opt/x.js` is a path with an empty authority. One slash more or
    // less is the whole difference: `file://host/opt/x.js` puts a machine's name
    // exactly where the path begins, and everything after it is relative to that
    // name rather than to anything this module can cut at -- so it is refused,
    // which is also what makes the host unsendable.
    for (const source of [
      `file://${HOST_A}/opt/${DIRECTORY_A}/x.js`,
      `file://${HOST_A}${APP_ROOT}/dist/main/index.js`,
      `file://${HOST_A}/Users/person-1/notes.txt`,
    ]) {
      const kept = scrubbedFrame(source).filename
      expect(kept, source).toBe('<external>')
      expect(kept, source).not.toContain(HOST_A)
    }
    // The single-slash spelling carries the path and no host, and it is refused
    // too: this module reads the one form a build actually writes, and guessing
    // at the others is how a `file:/` prefix becomes a way to say something the
    // rule above would not have allowed.
    expect(scrubbedFrame(`file:${APP_ROOT}/dist/main/index.js`).filename).toBe('<external>')
    // The form every reporter writes is still a path, host or no host.
    expect(scrubbedFrame(`file://${APP_ROOT}/dist/main/index.js`).filename).toBe('dist/main/index.js')
  })

  it('a path that carries a query or a fragment is refused rather than trimmed', () => {
    // A `?` and a `#` are where a URL's query and its fragment begin, so
    // `x.js?token=<x>` is a file name with a credential after it. Refused whole
    // rather than trimmed at the marker, and that is the safer of the two: the
    // SDK that built the URL may itself have cut the path at the marker, so what
    // is left before it can be a name the frame never had -- and a wrong name on
    // the wire is not safer than the fixed word, which is the choice this module
    // already makes for a percent-escape.
    const token = tokenPlaceholder()
    const sources = [
      `file://${APP_ROOT}/dist/main/x.js?token=${token}`,
      `file://${APP_ROOT}/dist/main/x.js#${DIRECTORY_A}`,
      `file://${APP_ROOT}/dist/main/x.js?token=${token}#frag`,
      `${APP_ROOT}/dist/main/x.js?token=${token}`,
    ]
    const kept = sources.map((source) => scrubbedFrame(source).filename)
    expect(JSON.stringify(kept)).not.toContain(token)
    expect(JSON.stringify(kept)).not.toContain(DIRECTORY_A)
    expect(kept).toEqual(sources.map(() => '<external>'))
  })

  it('a path that carries a control character is refused whole', () => {
    // A control character is not a separator two readers agree on: the home
    // check below reads `x\nhome/<x>/secret.js` as one segment, while a log line,
    // a terminal and a decoder read it as two -- so the newline is a way *past*
    // the check rather than part of a name.
    expect(scrubbedFrame('/opt/console/x\nhome/person-1/secret.js', undefined, '/opt/console').filename).toBe(
      '<external>',
    )
    // The same case spelled outside ASCII: Unicode has three more line breaks --
    // the next-line character and the line and paragraph separators -- and each
    // one is a break to some reader and a character to another, so a path that
    // hides a home directory behind one is the newline case again.
    expect(scrubbedFrame('/opt/console/x\u0085home/person-1/secret.js', undefined, '/opt/console').filename).toBe(
      '<external>',
    )
    expect(scrubbedFrame('/opt/console/x\u2028home/person-1/secret.js', undefined, '/opt/console').filename).toBe(
      '<external>',
    )
    expect(scrubbedFrame('/opt/console/x\u2029home/person-1/secret.js', undefined, '/opt/console').filename).toBe(
      '<external>',
    )
    for (const [name, source] of [
      ['a newline inside the app', `${APP_ROOT}/x\nhome/person-1/secret.js`],
      ['a carriage return', `${APP_ROOT}/dist/main/x\r.js`],
      ['a NUL', `${APP_ROOT}/dist/main/x\x00.js`],
      ['a tab', `${APP_ROOT}/dist/main/x\t.js`],
      ['a delete', `${APP_ROOT}/dist/main/x\x7f.js`],
      // The Unicode line breaks, and the invisible marks that go with them: a
      // bidi override turns the rest of a line around, and a zero-width joiner
      // is a character a reader does not see -- so a name carrying either one
      // says something to the reader that the bytes do not.
      ['a next-line character', `${APP_ROOT}/dist/main/x\u0085.js`],
      ['a line separator', `${APP_ROOT}/dist/main/x\u2028.js`],
      ['a paragraph separator', `${APP_ROOT}/dist/main/x\u2029.js`],
      ['a bidi override', `${APP_ROOT}/dist/main/x\u202e.js`],
      ['a zero-width joiner', `${APP_ROOT}/dist/main/x\u200d.js`],
    ] as const) {
      const kept = scrubbedFrame(source).filename
      expect(kept, name).toBe('<external>')
      expect(kept, name).not.toContain('person-1')
    }
  })

  it.each([
    // A relative path is a file next to something this module never learned:
    // the part that would say *where* is exactly the part that is missing, so
    // there is nothing to cut it at and nothing to keep of it.
    '~/x',
    `../../${DIRECTORY_A}/x.js`,
    'Users/person-1/x',
    `person-1@${HOST_A}:/srv/x.js`,
    '..\\..\\x.js',
    // A drive letter with no separator behind it is a path relative to whatever
    // directory that drive was last left in, and a one-letter scheme is the same
    // shape: neither is rooted anywhere.
    'C:acme\\x',
    'x:acme/x',
    // A percent-encoded scheme is a scheme in another spelling, and a decoder
    // somewhere down the line reads it as one.
    `file%3A///opt/${DIRECTORY_A}/x.js`,
    `file%3A%2F%2F%2Fopt%2F${DIRECTORY_A}%2Fx.js`,
  ])('the scrubber refuses the path that is not rooted anywhere: %s', (source) => {
    const kept = scrubbedFrame(source).filename
    expect(kept).toBe('<external>')
    expect(kept).not.toContain(DIRECTORY_A)
  })

  it.each([
    // The four shapes a traversal arrives in. Each one is a path that reaches
    // *under* the app root and then walks back out of it, so the root cut -- which
    // is a prefix removal rather than a check -- leaves a relative path that
    // points at a directory outside the app. The first one is this round's
    // mutation proof: with the refusal dropped, it is `../../<directory>/x.js`.
    ['/opt/console/../../acme/x.js', '/opt/console'],
    [`/x/app.asar/../../${DIRECTORY_A}/x.js`, APP_ROOT],
    [`C:\\work\\app\\..\\..\\users\\person-1\\x.js`, APP_ROOT],
    ['..\\..\\Documents and Settings\\person-1\\x.js', APP_ROOT],
    // A separator left over from another platform's syntax inside a name, and an
    // escape that stands for one: both are a segment the module did not read.
    [`${APP_ROOT}/dist/%2e%2e/${DIRECTORY_A}/x.js`, APP_ROOT],
    [`${APP_ROOT}/dist/a:b/x.js`, APP_ROOT],
    [`${APP_ROOT}/~/x.js`, APP_ROOT],
  ])('the scrubber refuses the path that walks out of the app: %s', (source, appRoot) => {
    const kept = scrubbedFrame(source, undefined, appRoot).filename
    expect(kept).toBe('<external>')
    expect(kept).not.toContain('..')
    expect(kept).not.toContain(DIRECTORY_A)
  })

  it('the home-directory check knows the other spellings of a home directory', () => {
    // The check is on what is *left* after the root comes off, so a home
    // directory that survives the cut is the case it exists for -- and it has to
    // know the spellings a real disk uses: a case-insensitive volume writes
    // `USERS` or `HOME`, Windows' older home directory has no user-name segment
    // to recognise, and a path that starts *inside* the archive with `Users/` is
    // a home directory too.
    expect(scrubbedFrame(`${APP_ROOT}/vendor/USERS/person-1/x.js`).filename).toBe('<external>')
    expect(scrubbedFrame(`${APP_ROOT}/vendor/HOME/person-1/x.js`).filename).toBe('<external>')
    expect(scrubbedFrame(`${APP_ROOT}/vendor/root/x.js`).filename).toBe('<external>')
    expect(scrubbedFrame(`${APP_ROOT}/vendor/Documents and Settings/person-1/x.js`).filename).toBe('<external>')
    expect(
      scrubbedFrame(`${PACKAGED_ROOT}/app.asar/Users/person-1/notes.txt`, undefined, PACKAGED_ROOT).filename,
    ).toBe('<external>')
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

  it('the archive marker counts only as a whole path segment', () => {
    // `app.asar` is a directory *name*, so a directory whose name merely ends in
    // it is a directory on somebody's disk: a search that finds the marker
    // anywhere inside a segment cuts at the wrong place and puts the next
    // directory on the wire. This is this round's **mutation** proof -- with a
    // substring search in place of the segment-anchored one, the first case below
    // is `acme-merger/x.js`.
    const packaged = (abs_path: string): string | undefined =>
      scrubCrashEvent(
        { exception: { values: [{ type: 'Error', stacktrace: { frames: [{ abs_path }] } }] } },
        PACKAGED_ROOT,
      ).exception?.values?.[0]?.stacktrace?.frames?.[0]?.filename

    expect(packaged('/tmp/person-1-secret-app.asar/acme-merger/x.js')).toBe('<external>')
    // The review's surviving mutant: a relative path that ends in the archive,
    // which the rooted-path check refuses before the marker is read at all.
    expect(scrubbedFrame('foo/app.asar/x.js').filename).toBe('<external>')
    // A real archive is still a path, and it is cut at its own segment whether or
    // not the app root above it matched.
    expect(packaged(`${PACKAGED_ROOT}/app.asar/dist/main/index.js`)).toBe('dist/main/index.js')
    expect(packaged('/opt/elsewhere/vendor/app.asar/dist/main/index.js')).toBe('dist/main/index.js')
  })

  it('the scrubber keeps a type and a name only when they read as one', () => {
    // `type` and `function` are strings a library filled in, and a library can be
    // handed a sentence: an exception whose type is its message, a frame whose
    // function is a task title. That is F33's finding under two more field names,
    // so both are held to a name's shape, and a string that fails is a fixed word
    // rather than a trim -- what to cut out of a sentence is not something this
    // module can know.
    const scrubbed = scrubCrashEvent(
      {
        exception: {
          values: [
            {
              type: poisonType(),
              stacktrace: { frames: [{ abs_path: `${APP_ROOT}/dist/main/index.js`, function: PROSE_A }] },
            },
          ],
        },
      },
      APP_ROOT,
    )
    const serialised = JSON.stringify(scrubbed)
    expect(serialised).not.toContain(HOME_PATH)
    expect(serialised).not.toContain(PROSE_A)
    expect(scrubbed.exception?.values?.[0]?.type).toBe('<unknown>')
    expect(scrubbed.exception?.values?.[0]?.stacktrace?.frames?.[0]?.function).toBe('<anonymous>')

    // ...and a name that is one is kept, punctuation included: a stack really
    // carries dots for a method and angle brackets for a synthetic frame.
    const named = scrubCrashEvent(
      {
        exception: {
          values: [
            {
              type: 'TypeError',
              stacktrace: { frames: [{ abs_path: `${APP_ROOT}/dist/main/index.js`, function: 'Object.<anonymous>' }] },
            },
          ],
        },
      },
      APP_ROOT,
    )
    expect(named.exception?.values?.[0]?.type).toBe('TypeError')
    expect(named.exception?.values?.[0]?.stacktrace?.frames?.[0]?.function).toBe('Object.<anonymous>')
  })

  it('an event that is not an event comes back as an empty one, rather than throwing', () => {
    // `beforeSend` is called with whatever the SDK could build, and a scrubber
    // that throws in there is a crash inside the crash reporter -- so every shape
    // below is a value that reaches this module from outside and none of them may
    // be read as one.
    expect(scrubCrashEvent(null, APP_ROOT)).toEqual({})
    expect(scrubCrashEvent(undefined, APP_ROOT)).toEqual({})
  })

  it.each([
    ['a path', `${APP_ROOT}/dist/main/x.js`],
    ['a newline', `node\n${DIRECTORY_A}`],
    ['a long string', 'a'.repeat(65)],
    ['a token-shaped placeholder', tokenPlaceholder()],
  ])('an event_id that is %s is dropped', (_name, value) => {
    expect(scrubCrashEvent({ event_id: value }, APP_ROOT)).toEqual({})
  })

  it.each([
    ['a path', `${APP_ROOT}/dist/main/x.js`],
    ['a newline', `node\n${DIRECTORY_A}`],
    ['a long string', 'a'.repeat(17)],
    ['a token-shaped placeholder', tokenPlaceholder()],
  ])('a platform that is %s is dropped', (_name, value) => {
    expect(scrubCrashEvent({ platform: value }, APP_ROOT)).toEqual({})
  })

  it.each([
    ['a path', `${APP_ROOT}/dist/main/x.js`],
    ['a newline', `console-0.116.0\n${DIRECTORY_A}`],
    ['a long string', 'a'.repeat(65)],
    ['a token-shaped placeholder', tokenPlaceholder()],
  ])('a release that is %s is dropped', (_name, value) => {
    expect(scrubCrashEvent({ release: value }, APP_ROOT)).toEqual({})
  })

  it('the event strings are kept when they are the shapes the reporter writes', () => {
    // The other half of the rule above: the three patterns are not a refusal of
    // the fields, they are what the fields look like when the reporter wrote
    // them. The id is the generator's 32 lower-case hex characters -- an
    // upper-case hex string is a hex string and not that id -- and the release is
    // bounded at 64 characters.
    expect(
      scrubCrashEvent({ event_id: '0'.repeat(32), platform: 'node', release: 'console-0.116.0' }, APP_ROOT),
    ).toEqual({ event_id: '0'.repeat(32), platform: 'node', release: 'console-0.116.0' })
    expect(scrubCrashEvent({ event_id: 'A'.repeat(32) }, APP_ROOT)).toEqual({})
    expect(scrubCrashEvent({ release: 'a'.repeat(64) }, APP_ROOT)).toEqual({ release: 'a'.repeat(64) })
  })

  it.each([
    ['the exception', proxyThrowingOn('exception')],
    ['the values', throwingValues()],
    ['a frame field', throwingFrameField()],
    ['an event field', proxyThrowingOn('event_id')],
  ] as [string, CrashEvent][])(
    'an event whose getter throws on %s comes back empty rather than throwing',
    (_position, event) => {
      // `beforeSend` runs inside the reporter, so a throw here is a crash in the
      // crash reporter -- and a getter is not a value the guards above can check
      // before it runs. What comes back is the empty event rather than the fields
      // that were readable: a field read past a throwing sibling is one this
      // module cannot claim it checked.
      expect(scrubCrashEvent(event, APP_ROOT)).toEqual({})
    },
  )

  it('every event field is read once, so a getter that answers again cannot pass the allow-list', () => {
    // A field read twice is checked on one read and copied on another, and a
    // getter is free to answer differently each time -- so a value that reads as
    // an event id when the allow-list checks it and as a credential when the
    // output copies it is a credential on the wire with every check passed. Each
    // field is therefore read exactly once into a local, and the local is what is
    // checked and what is copied.
    //
    // This is this round's **mutation** proof: putting the property read back
    // into the copy -- `{ event_id: event.event_id }` beside the local the check
    // reads -- is one edit away from the line the fix writes, and the first case
    // below then holds the token-shaped placeholder in place of the id.
    const token = tokenPlaceholder()
    const scrubbedEvent = (property: string, first: unknown): ScrubbedCrashEvent =>
      scrubCrashEvent(changingOnSecondRead(property, first, token) as unknown as CrashEvent, APP_ROOT)
    const cases: readonly (readonly [string, unknown, ScrubbedCrashEvent])[] = [
      ['event_id', '0'.repeat(32), { event_id: '0'.repeat(32) }],
      ['timestamp', 1767225600, { timestamp: 1767225600 }],
      ['platform', 'node', { platform: 'node' }],
      ['level', 'error', { level: 'error' }],
      ['release', 'console-0.116.0', { release: 'console-0.116.0' }],
    ]
    for (const [property, first, expected] of cases) {
      const scrubbed = scrubbedEvent(property, first)
      expect(scrubbed, property).toEqual(expected)
      expect(JSON.stringify(scrubbed), property).not.toContain(token)
    }
  })

  it('every exception field is read once, so a getter that answers again cannot pass the allow-list', () => {
    // The same rule one level down, at every position an exception has: the
    // exception itself, its `values`, a value's `type` and `stacktrace`, and the
    // `frames` inside it.
    const token = tokenPlaceholder()
    const frames = [{ abs_path: `${APP_ROOT}/dist/main/index.js` }]
    const first = { type: 'Error', stacktrace: { frames } }
    const expected = {
      exception: { values: [{ type: 'Error', stacktrace: { frames: [{ filename: 'dist/main/index.js' }] } }] },
    }
    const changingValues = changingOnSecondRead('values', [first], token)
    const scrubbed = [
      // The exception itself, and its `values`.
      scrubCrashEvent(
        changingOnSecondRead('exception', { values: [first] }, token) as unknown as CrashEvent,
        APP_ROOT,
      ),
      scrubCrashEvent({ exception: changingValues } as unknown as CrashEvent, APP_ROOT),
      // A value's `type`, and its `stacktrace`.
      scrubCrashEvent(
        {
          exception: {
            values: [changingOnSecondRead('type', 'Error', token, { stacktrace: first.stacktrace })],
          },
        } as unknown as CrashEvent,
        APP_ROOT,
      ),
      scrubCrashEvent(
        {
          exception: {
            values: [changingOnSecondRead('stacktrace', first.stacktrace, token, { type: 'Error' })],
          },
        } as unknown as CrashEvent,
        APP_ROOT,
      ),
      // ...and the `frames` inside it.
      scrubCrashEvent(
        {
          exception: {
            values: [{ type: 'Error', stacktrace: changingOnSecondRead('frames', frames, token) }],
          },
        } as unknown as CrashEvent,
        APP_ROOT,
      ),
    ]
    for (const one of scrubbed) {
      expect(one).toEqual(expected)
      expect(JSON.stringify(one)).not.toContain(token)
    }
  })

  it('every frame field is read once, so a getter that answers again cannot pass the allow-list', () => {
    // And once more inside a frame: the name, the path the cut reads (`abs_path`
    // when it is there and `filename` otherwise), and the three fields beside
    // them.
    const token = tokenPlaceholder()
    const path = `${APP_ROOT}/dist/main/index.js`
    const scrubbedFrameOf = (frame: Record<string, unknown>): ScrubbedFrame | undefined =>
      scrubCrashEvent(
        { exception: { values: [{ type: 'Error', stacktrace: { frames: [frame as unknown as CrashFrame] } }] } },
        APP_ROOT,
      ).exception?.values?.[0]?.stacktrace?.frames?.[0]
    const cases: readonly (readonly [string, Record<string, unknown>, ScrubbedFrame])[] = [
      ['function', changingOnSecondRead('function', 'run', token), { filename: '<external>', function: 'run' }],
      ['abs_path', changingOnSecondRead('abs_path', path, token), { filename: 'dist/main/index.js' }],
      ['filename', changingOnSecondRead('filename', path, token), { filename: 'dist/main/index.js' }],
      [
        'lineno',
        changingOnSecondRead('lineno', 12, token, { abs_path: path }),
        { filename: 'dist/main/index.js', lineno: 12 },
      ],
      [
        'colno',
        changingOnSecondRead('colno', 4, token, { abs_path: path }),
        { filename: 'dist/main/index.js', colno: 4 },
      ],
      [
        'in_app',
        changingOnSecondRead('in_app', true, token, { abs_path: path }),
        { filename: 'dist/main/index.js', in_app: true },
      ],
    ]
    for (const [property, frame, expected] of cases) {
      const scrubbed = scrubbedFrameOf(frame)
      expect(scrubbed, property).toEqual(expected)
      expect(JSON.stringify(scrubbed), property).not.toContain(token)
    }
  })

  it('an exception that is not one is dropped, and an empty values list is not read', () => {
    expect(scrubCrashEvent({ exception: null }, APP_ROOT)).toEqual({})
    expect(scrubCrashEvent({ exception: undefined }, APP_ROOT)).toEqual({})
    expect(scrubCrashEvent({ exception: { values: null } }, APP_ROOT)).toEqual({})
    // The rest of the event is still reported: a bad exception does not take the
    // type, the level and the release down with it.
    expect(scrubCrashEvent({ level: 'error', exception: null }, APP_ROOT)).toEqual({ level: 'error' })
  })

  it('a value entry that is not a value is dropped rather than read', () => {
    expect(
      scrubCrashEvent({ exception: { values: [null, undefined, { type: 'Error' }] } }, APP_ROOT),
    ).toEqual({ exception: { values: [{ type: 'Error' }] } })
  })

  it('a stacktrace that is not one leaves the type and no frames', () => {
    expect(
      scrubCrashEvent({ exception: { values: [{ type: 'Error', stacktrace: null }] } }, APP_ROOT),
    ).toEqual({ exception: { values: [{ type: 'Error' }] } })
  })

  it('a frame that is not a frame is dropped from the stack rather than read', () => {
    // A frame that is not an object is not a frame this module declined to place
    // -- it is not a frame at all -- and inventing one for it would be a stack the
    // reporter never built. What is kept of the value is the type, and the stack
    // it does have is the frames it really had.
    expect(
      scrubCrashEvent(
        { exception: { values: [{ type: 'Error', stacktrace: { frames: [null, undefined] } }] } },
        APP_ROOT,
      ),
    ).toEqual({ exception: { values: [{ type: 'Error', stacktrace: { frames: [] } }] } })
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
