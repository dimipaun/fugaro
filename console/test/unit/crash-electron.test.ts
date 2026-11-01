// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The crash reporter's wiring, held to its four promises (desktop-console plan
// Task 2.1, the SDK slice; the owner's rulings D733 and D734 of 2026-10-30,
// which design section 7.4 does not describe yet and Task 2.2 amends).
//
// The module under test is the console's only importer of `@sentry/electron`,
// and the SDK it imports is replaced here by a fake that models the one part of
// 8.2.0 this wiring depends on: `init` builds the client's transport from the
// factory it was handed, and `beforeSend` runs before an envelope reaches that
// transport. Everything else the fake does not model is a thing this file
// asserts the adapter never asked for.
//
//   1. **Nothing starts before the switch is read.** The gate is `./crash`'s,
//      and this file is about the half the wiring owns: that a closed answer
//      calls no `init` at all rather than starting a reporter that decides later
//      to send nothing. The DSN is no longer part of that question -- since the
//      owner's ruling of 2026-10-31 it is `./crash-dsn`'s constant, carried by
//      every build rather than supplied to one -- so a yes is the whole input.
//   2. **What reaches the transport is the scrubbed event, and only it.** The
//      headline case below is this slice's mutation proof: with `beforeSend`
//      changed to hand the event back untouched, the token, the home path and
//      the message are what the transport receives.
//   3. **The entry loads this module only when the gate can open, and the wiring
//      asks the gate with the switch and nothing else.** The gate is an
//      allow test on both sides -- `optedIn === true` -- so a state nobody has
//      defined yet is off without either file knowing its name. Both are claims
//      about files this suite cannot import (one of them imports Electron's
//      runtime), so the two cases below read them as text -- with the comments
//      blanked, because both explain the order in prose.
//   4. **The parts only a running client can answer are in
//      `test/e2e/crash-init.spec.ts`**: that the transport it asked for keeps
//      nothing on disk, that no `ipcMain` handler, protocol scheme or session
//      preload is registered, and that the profile is untouched by a report that
//      was sent. Round B moved them out of this file, where each of them could
//      only assert what the fake had been told to do, and into a real Electron
//      main process running the pinned SDK.

import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { inert } from '../support/fakeSecrets'
import { CRASH_START_FAILED, scrubCrashEvent, startCrashReportingSafely, type CrashEvent } from '../../src/main/crash'
import { CONSOLE_CRASH_DSN } from '../../src/main/crash-dsn'
import { CLOSE_TIMEOUT_MS, startCrashReporting } from '../../src/main/crash-electron'

/** harness is the shared state the mocked SDK writes into. It is `vi.hoisted`
 * because the factory below is hoisted above this file's imports, so it cannot
 * close over a variable declared here. */
const harness = vi.hoisted(() => ({
  /** Every options object `init` was handed, in order. */
  initOptions: [] as Record<string, unknown>[],
  /** Every capture-integration call the wiring made, in order, with the options
   * it was handed: the two integrations are the only things that turn an error
   * into a report, so what they are asked for is part of the payload rule. */
  integrations: [] as Record<string, unknown>[],
  /** What the client's transport received, in order. */
  sent: [] as unknown[],
  /** The transport the client built, as `init` left it. */
  transport: { send: async (_envelope: unknown): Promise<unknown> => ({ statusCode: 200 }) },
  /** How many times the client was closed. */
  closes: 0,
  /** Every timeout `close` was handed, in order. `undefined` is the unbounded
   * wait, and it is the thing this wiring must never pass. */
  closeTimeouts: [] as (number | undefined)[],
  /** Whether the client's transport has a send that never settles. It is the
   * endpoint a quit must not wait on. */
  stalled: false,
  /** How many times the mocked SDK's `init` patched `utilityProcess.fork` the
   * way the pinned `configureUtilityProcessIPC` does. */
  forksPatched: 0,
}))

/** utility is the `electron.utilityProcess` the mocked SDK patches: `real` is the
 * fork the app started with, and `patched` is the one `init` assigns over it. It
 * is deliberately **not** `undefined` -- the case the wiring has to survive is
 * the real one, where the SDK's Proxy stands in for a fork that exists, and an
 * undefined `utilityProcess` is a module that has nothing to restore. */
const utility = vi.hoisted(() => {
  const fork = (..._args: unknown[]): { postMessage: () => void } => ({ postMessage: () => undefined })
  return {
    real: fork,
    patched: (..._args: unknown[]): { postMessage: () => void } => ({ postMessage: () => undefined }),
    process: { fork } as { fork: (...args: unknown[]) => unknown },
  }
})

vi.mock('electron', () => ({ utilityProcess: utility.process }))

vi.mock('@sentry/electron/main', () => ({
  init: (options: Record<string, unknown>) => {
    harness.initOptions.push(options)
    // The client builds its transport here, from the factory it was given --
    // the order @sentry/core's `NodeClient` has it in, and the reason the
    // adapter can hand `init` a factory rather than an instance.
    const factory = options.transport as (transportOptions: unknown) => { send: (e: unknown) => Promise<unknown> }
    const transport = factory({ url: 'the endpoint the DSN resolves to', recordDroppedEvent: () => undefined })
    harness.transport = transport
    // ...and `configureUtilityProcessIPC()` runs here too: `@sentry/electron`'s
    // main `sdk.js` calls it on every start, unconditionally, and it is what
    // replaces `electron.utilityProcess.fork` with a Proxy. Nothing in the
    // console turns it off, so the wiring has to put the original back.
    harness.forksPatched += 1
    utility.process.fork = utility.patched
  },
  close: async (timeout?: number) => {
    harness.closes += 1
    harness.closeTimeouts.push(timeout)
    // The real client flushes what the transport has in flight and gives up when
    // the timeout it was handed expires (`@sentry/core`'s `Client.flush` races
    // the client's own processing and `transport.flush` against it). Handed no
    // timeout it waits for the send to settle, however long that takes -- and
    // `harness.stalled` is a send that never settles. `transport.flush` in the
    // electron-net transport has no timeout of its own either, which is why the
    // bound has to come from here.
    if (!harness.stalled) return true
    if (timeout === undefined) return await new Promise<boolean>(() => undefined)
    await new Promise((resolve) => setTimeout(resolve, timeout))
    return false
  },
  makeElectronTransport: (options: unknown) => ({
    send: async (envelope: unknown) => {
      harness.sent.push(envelope)
      return { statusCode: 200 }
    },
    flush: async () => true,
    options,
  }),
  // The two capture integrations the wiring names by hand. They are stubs for
  // the reason the rest of this mock is -- the real ones register process
  // listeners and reach Electron -- so the names and the options here are the
  // wiring's, and `the capture integrations are the pinned SDK's own` below reads
  // the installed package to hold both, and the behaviour they stand for, to the
  // pinned version.
  onUncaughtExceptionIntegration: () => {
    harness.integrations.push({ name: 'OnUncaughtException' })
    return { name: 'OnUncaughtException' }
  },
  onUnhandledRejectionIntegration: (options: Record<string, unknown> = {}) => {
    harness.integrations.push({ name: 'OnUnhandledRejection', ...options })
    return { name: 'OnUnhandledRejection' }
  },
}))

/** dsnPlaceholder is a DSN's shape -- scheme, public key, host, project id --
 * assembled from fragments at run time, for the reason `fakeSecrets` exists and
 * for the same reason the scrubber's own test does it: a DSN-shaped literal in
 * this repository is a defect even when it is invented, and `example.invalid` is
 * the reserved name, so nothing here resolves anywhere. It stands in for the
 * `dsn` a caller supplies where the embedded constant would otherwise go. */
function dsnPlaceholder(): string {
  return inert('https', '://', 'a'.repeat(32), '@', 'example', '.invalid', '/', '1')
}

/** tokenPlaceholder is a credential-shaped value, assembled here. */
function tokenPlaceholder(): string {
  return inert('gh', 'p_', 'a'.repeat(36))
}

/** tempAppRoot is a fresh temp directory per test: what `app.getAppPath()`
 * answers with, and the prefix a kept frame's path is cut at. */
function tempAppRoot(): string {
  const directory = mkdtempSync(join(tmpdir(), 'fugaro-crash-app-'))
  made.push(directory)
  return directory
}

/** made is every app root this file has handed out: they are directories in the
 * machine's temp directory, one per test, and the rule below takes each of them
 * away rather than leaving the run's leavings behind. */
const made: string[] = []

/** consoleFrame is a frame path that is genuinely inside this app, so the
 * scrubber has something to place; `hostileEvent` takes the prefix because the
 * app root is a temp directory here. */
function appFrame(appRoot: string, ...rest: string[]): string {
  return join(appRoot, ...rest)
}

/** hostileEvent is an event as a reporter builds one for a real crash: the
 * fields the scrubber keeps, and the ones it must not -- a message somebody
 * typed, a home path in a frame, and a credential that reached the event. The
 * second frame is inside this app's root, which is the one a payload is allowed
 * to carry a path for. */
function hostileEvent(appRoot: string): CrashEvent & Record<string, unknown> {
  return {
    event_id: 'a'.repeat(32),
    timestamp: 1700000000.5,
    platform: 'node',
    level: 'error',
    release: '0.116.0',
    message: `launching ${inert('gh', 'p_', 'a'.repeat(36))} failed`,
    server_name: 'build-host-a',
    user: { id: 'person-1', email: `${inert('per', 'son-1')}@example.invalid` },
    request: { url: `https://example.invalid/x?token=${inert('gh', 'p_', 'a'.repeat(36))}` },
    contexts: { device: { name: 'a machine' } },
    tags: { 'acme-secret-repo': 'owner/repo-a' },
    extra: { task: 'acme secret task text' },
    breadcrumbs: [{ message: 'a breadcrumb a person typed' }],
    exception: {
      values: [
        {
          type: 'Error',
          value: `ENOENT: /Users/person-1/acme-secret-repo/x.js`,
          mechanism: { type: 'onuncaughtexception' },
          stacktrace: {
            frames: [
              { abs_path: '/Users/person-1/acme-secret-repo/dist/main/index.js', function: 'main', lineno: 12 },
              { abs_path: appFrame(appRoot, 'dist', 'main', 'x.js'), function: 'run', lineno: 3 },
            ],
          },
        },
      ],
    },
  }
}

beforeEach(() => {
  harness.initOptions = []
  harness.integrations = []
  harness.sent = []
  harness.closes = 0
  harness.closeTimeouts = []
  harness.stalled = false
  harness.forksPatched = 0
  utility.process.fork = utility.real
})

afterEach(() => {
  vi.restoreAllMocks()
  for (const directory of made.splice(0)) rmSync(directory, { recursive: true, force: true })
})

describe('the crash reporter starts only when the gate opens', () => {
  it('starts nothing while the switch is unread, off, or not a yes', () => {
    const appRoot = tempAppRoot()
    for (const [name, optedIn] of [
      ['unread', null],
      ['off', false],
      ['the string "true"', 'true'],
      ['the number 1', 1],
      ['an object', { consent: true }],
    ] as const) {
      const handle = startCrashReporting({ optedIn, appRoot })
      expect(harness.initOptions, name).toEqual([])
      void handle.close()
    }
  })

  it('takes the DSN from no environment variable and no file', () => {
    // The ruling of 2026-10-31 in one assertion: the DSN is compiled in, so the
    // running console reads nothing for it -- not the environment of whatever
    // launcher started it, and not a file beside the bundle. Spelling the
    // cancelled variable here pins the absence: a wiring that read it would have
    // to name it, and this file would be the second place it appeared.
    const reader = readFileSync(
      fileURLToPath(new URL('../../src/main/crash-electron.ts', import.meta.url)),
      'utf8',
    )
    expect(reader).not.toContain('FUGARO_CONSOLE_CRASH_DSN')
    expect(reader).not.toContain('process.env')
    expect(reader).not.toContain('node:fs')
    expect(reader).toContain("import { CONSOLE_CRASH_DSN } from './crash-dsn'")
  })

  it('puts electron.utilityProcess.fork back the way the SDK found it', () => {
    // The pinned SDK patches `electron.utilityProcess.fork` on every start and
    // offers no option against it, so the wiring takes the patch off again. The
    // cost of leaving it on is the reason: the patched fork opens a port to the
    // child and forwards what the child sends with `client.sendEnvelope`, which
    // is the one path in this SDK that skips `beforeSend` -- so an unpatched
    // global is the difference between a child's envelope passing the scrubber
    // and it not passing at all. What the mock cannot say is whether the Proxy
    // is a *problem*; `test/e2e/crash-init.spec.ts` holds the real one to the
    // same answer, and this is the half that fails when the restore is dropped.
    const appRoot = tempAppRoot()
    const handle = startCrashReporting({ optedIn: true, appRoot })
    expect(harness.forksPatched, 'the mocked SDK patches fork during init').toBe(1)
    expect(
      utility.process.fork,
      'the fork the app started with is the one it still has: the SDK\'s Proxy is gone',
    ).toBe(utility.real)
    void handle.close()
  })

  it('starts the client with the embedded constant when the switch says yes', () => {
    const appRoot = tempAppRoot()
    const handle = startCrashReporting({ optedIn: true, appRoot })
    expect(harness.initOptions).toHaveLength(1)
    expect(harness.initOptions[0]?.dsn).toBe(CONSOLE_CRASH_DSN)
    void handle.close()
  })

  it('starts the client with a DSN the caller supplied instead of the constant', () => {
    // The tests and the e2e fixture point the wiring at a sink of their own, so
    // the `dsn` input stays. It is the only thing that can displace the
    // constant, and it is not a decision about reporting: the switch is still
    // the whole gate.
    const appRoot = tempAppRoot()
    const dsn = dsnPlaceholder()
    const handle = startCrashReporting({ optedIn: true, appRoot, dsn })
    expect(harness.initOptions[0]?.dsn).toBe(dsn)
    void handle.close()
  })

  it('the entry point loads the module only when the gate can open, and closes it on the way out', () => {
    // `src/main/index.ts` cannot be imported under `vitest` -- it imports the
    // Electron runtime -- so this is the text of it, which is what
    // `test/unit/startup.test.ts` and `test/unit/entry-wiring.test.ts` read too,
    // and it is read with its comments blanked, because the wiring explains this
    // decision in prose that names both modules and a scan that read the prose
    // would be answered by the explanation rather than by the call.
    //
    // Five shapes, because each one fails differently: a second start would be a
    // second client (two transports, two event ids); a start with anything but
    // the store's value would be a slice deciding the switch itself; a
    // **static** import of the module would load `@sentry/electron/main` and the
    // fourteen packages it reaches at every start, whatever the gate answers; a
    // start that is not wrapped would let a throw out of the SDK cost the app its
    // startup; and no call on the quit path is a report cut off mid-send.
    const entry = withoutComments(
      readFileSync(fileURLToPath(new URL('../../src/main/index.ts', import.meta.url)), 'utf8'),
    )
    expect(entry).not.toContain('startCrashReporting({')
    expect([...entry.matchAll(/crashReporting = await startCrashReportingSafely\(\{/g)]).toHaveLength(1)
    expect([...entry.matchAll(/input: \{ optedIn, appRoot: app\.getAppPath\(\) \}/g)]).toHaveLength(1)
    expect([...entry.matchAll(/crashReporting\?\.close\(\)/g)]).toHaveLength(1)

    // The module is reached dynamically, once, and only from behind the gate: a
    // static reach would load it before the decision exists to make, so the only
    // line allowed to carry `from './crash-electron'` is the erased type import,
    // and the loader handed to the guard is the one dynamic import in the file.
    const staticLoads = entry
      .split('\n')
      .filter((line) => line.includes("from './crash-electron'") && !line.trimStart().startsWith('import type'))
    expect(staticLoads).toEqual([])
    expect([...entry.matchAll(/import\('\.\/crash-electron\.js'\)/g)]).toHaveLength(1)

    // ...and the gate is an **allow test**: `optedIn === true`, and nothing else
    // -- not `crashReportingDecision`, not a reason, not a list of the answers
    // that shut it. `./crash` is where the *guard* comes from, and it is the
    // only thing this file takes from it: the module it takes it from is
    // importless, so naming it here loads no SDK, and the two names the gate
    // itself uses stay out of this file.
    expect(entry).not.toContain('crashReportingDecision')
    expect(entry.split('\n').filter((line) => line.includes("from './crash'"))).toEqual([
      "import { startCrashReportingSafely } from './crash'",
    ])
    const allow = entry.indexOf('if (optedIn === true)')
    const load = entry.indexOf("import('./crash-electron.js')")
    expect(allow, 'the entry asks the gate with an allow test').toBeGreaterThan(-1)
    expect(load).toBeGreaterThan(allow)
    const branch = entry.slice(allow, load)
    expect(branch, 'no line closes the block before the load, so the load is inside it').not.toMatch(/^\s*\}/m)
    expect(entry.slice(allow, entry.indexOf('}', load))).toContain('startCrashReportingSafely(')
  })

  it('reads the per-user switch itself, before the gate, and starts nothing on a value it computed', () => {
    // The gate above is only as good as the value it is handed, and the value is
    // the one thing a scan of the gate cannot see: with `optedIn` declared `true`
    // every check in this test file still passes -- the allow test is still an
    // allow test, the import is still lazy, the close is still on the quit path
    // -- and the console reports on every start of every build. So the
    // declaration is pinned as the *whole* line, and it is pinned as a **read of
    // the store**: the switch is the console's own per-user file (`crash.json`,
    // `./crashstore`), not a constant of the build and not a field of anything
    // else, and the read is the one thing that stands between a `true` in that
    // file and a client.
    const entry = withoutComments(
      readFileSync(fileURLToPath(new URL('../../src/main/index.ts', import.meta.url)), 'utf8'),
    )
    const declarations = entry
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line.startsWith('const optedIn'))
    expect(
      declarations,
      'index.ts must read the switch from the store: `= true` here starts the reporter in every build, and nothing else in this file would notice',
    ).toEqual(["const optedIn: unknown = readCrashSwitch(app.getPath('userData'))"])

    // The read comes before the gate and before the load, and it is the only
    // thing between them: a switch read after the client started would decide
    // nothing, and one read after an `await` would be a value from a later tick
    // than the startup it is meant to describe.
    const read = entry.indexOf('readCrashSwitch(')
    const start = entry.indexOf('async function main(')
    expect(read).toBeGreaterThan(start)
    expect(entry.indexOf('if (optedIn === true)')).toBeGreaterThan(read)
    expect(entry.indexOf("import('./crash-electron.js')")).toBeGreaterThan(read)
    const beforeTheGate = entry.slice(start, entry.indexOf('if (optedIn === true)'))
    expect(beforeTheGate, 'nothing is awaited before the switch is read').not.toContain('await ')
  })

  it('guards the load and the init, so a start that fails after the yes still reaches the window', async () => {
    // `./crash`'s guard is where this is testable: `src/main/index.ts` cannot be
    // imported under `vitest`, so the shapes that have to survive are handed to
    // the guard itself -- an import that rejects (a damaged archive, a build that
    // did not carry the file) and an `init` that throws after the module loaded.
    // Both answer `null`, say one constant line, and resolve: what they must never
    // do is reject, because the entry point awaits this inside `main`, whose
    // rejection ends at `reportAndQuit` -- a console that quits instead of opening
    // its window because the crash reporter could not start.
    const logs: string[] = []
    const load = (): Promise<never> => Promise.reject(new Error('Cannot find module ./crash-electron.js'))
    const rejected = await startCrashReportingSafely({
      load,
      input: { optedIn: true, appRoot: '/app' },
      log: (line) => logs.push(line),
    })
    expect(rejected).toBeNull()

    const started = await startCrashReportingSafely<{ readonly close: () => Promise<void> }>({
      load: () => Promise.resolve({ startCrashReporting: () => {
        throw new Error('the client could not be initialised')
      } }),
      input: { optedIn: true, appRoot: '/app' },
      log: (line) => logs.push(line),
    })
    expect(started).toBeNull()

    // One line each, and the line is the constant: it repeats no error text and
    // names no path, both of which arrive from a library.
    expect(logs).toEqual([CRASH_START_FAILED, CRASH_START_FAILED])

    // ...and the path that works is handed the input unchanged, so the guard is
    // not a place the switch or the app root can be rewritten.
    const handles: unknown[] = []
    const ok = await startCrashReportingSafely({
      load: () => Promise.resolve({ startCrashReporting: (input: { readonly optedIn: unknown; readonly appRoot: string }) => {
        handles.push(input)
        return { close: () => Promise.resolve() }
      } }),
      input: { optedIn: true, appRoot: '/app' },
      log: (line) => logs.push(line),
    })
    expect(handles).toEqual([{ optedIn: true, appRoot: '/app' }])
    expect(ok).not.toBeNull()
    expect(logs).toEqual([CRASH_START_FAILED, CRASH_START_FAILED])

    // One assignment in the entry point, and it is the guard's answer -- so
    // nothing else in the file can put a client in the handle the guard did not
    // make.
    const entry = withoutComments(
      readFileSync(fileURLToPath(new URL('../../src/main/index.ts', import.meta.url)), 'utf8'),
    )
    expect([...entry.matchAll(/crashReporting = (?!await startCrashReportingSafely\(\{)/g)]).toEqual([])
  })

  it('the wiring asks the gate with the switch alone, and the constant is the fallback', () => {
    // The one shape that matters inside the wiring, read as text because the
    // module imports the pinned SDK. The gate's input is `{ optedIn }` and
    // nothing else -- no DSN, so no `'no-dsn'` branch and no reason to test for
    // one -- and the DSN that reaches `init` is the caller's if there is one and
    // `./crash-dsn`'s constant otherwise.
    const wiring = withoutComments(
      readFileSync(fileURLToPath(new URL('../../src/main/crash-electron.ts', import.meta.url)), 'utf8'),
    )
    const start =
      /export function startCrashReporting\(input: CrashReportingInput\): CrashReportingHandle \{[\s\S]*?\n\}\n/.exec(
        wiring,
      )
    if (start === null) throw new Error('the scan found no startCrashReporting: it would pass by finding nothing')
    expect(start[0]).toContain('crashReportingDecision({ optedIn: input.optedIn })')
    expect(start[0]).toContain('dsn: input.dsn ?? CONSOLE_CRASH_DSN')
    expect(start[0]).not.toContain('no-dsn')
    // The decision is asked before anything is started, which is the whole
    // ordering claim left in a wiring that no longer reads a disk.
    const decideAt = start[0].indexOf('crashReportingDecision({ optedIn: input.optedIn })')
    const initAt = start[0].indexOf('initClient(')
    expect(decideAt, 'the gate is asked at all').toBeGreaterThan(-1)
    expect(initAt, 'the client starts only after the gate answers').toBeGreaterThan(decideAt)
  })
})

/** withoutComments blanks a source's comments, so a scan over it reads the code
 * rather than the prose -- the wiring discusses both modules and both calls in
 * the comments above them. `test/unit/entry-wiring.test.ts` blanks the same way
 * for the same reason; the `//` is only a comment where it is not part of a URL,
 * which is what the character before it is for. */
function withoutComments(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/(^|[^:])\/\/[^\n]*/gm, '$1')
}

describe('what the client is told', () => {
  it('carries no default integrations and no IPC of either kind', () => {
    const appRoot = tempAppRoot()
    const handle = startCrashReporting({ optedIn: true, appRoot })
    const options = harness.initOptions[0] ?? {}
    // `defaultIntegrations: false` is what keeps out the three the header of
    // the module names: the preload injection, the main-process session and
    // the minidump integration -- the last two of which are the session file
    // and the native dumps.
    expect(options.defaultIntegrations).toBe(false)
    // Neither `Classic` (1) nor `Protocol` (2): the SDK's `IPCMode` has no
    // member for "off", so the adapter spells it 0 and this pins the number.
    expect(options.ipcMode).toBe(0)
    // And the one the options object does not cover, because it is not an
    // integration: `false` here is what keeps the client's constructor from
    // calling `registerDiagnosticsChannelInjection()`, which on this Node is
    // `Module.registerHooks` -- a loader hook over every module the process
    // loads, with no unregister in Node to take it back off. The option is read
    // as `!== false`, so `true` or a missing key installs it.
    expect(options.enableRuntimeChannelInjection).toBe(false)
    // **The captures are named, and they are exactly two.** With
    // `defaultIntegrations: false` a client handed no list captures nothing on
    // its own, so the console names the two that turn a crash into a report and
    // adds no third: no minidump, no session, no preload and no breadcrumb
    // integration, which is the list above restated as the list this option
    // could put back.
    const integrations = options.integrations as readonly { readonly name?: unknown }[]
    expect(integrations.map((integration) => integration.name)).toEqual([
      'OnUncaughtException',
      'OnUnhandledRejection',
    ])
    // ...and what they were asked for: the uncaught-exception integration takes
    // no options at all, and the rejection one is told `'none'`, so it captures
    // and does nothing else -- not `'warn'` (Node's warning written to the
    // console) and not `'strict'` (which calls `logAndExitProcess`). The
    // default is `'warn'`, so this is a decision rather than a silence.
    expect(harness.integrations).toEqual([
      { name: 'OnUncaughtException' },
      { name: 'OnUnhandledRejection', mode: 'none' },
    ])
    // **Neither of them decides this process's exit**, and these two are the
    // options that would: `onFatalError` is what the Electron SDK calls after a
    // fatal flush, and `exitEvenIfOtherHandlersAreRegistered` is `@sentry/node`'s
    // -- an option the integration this wiring imports does not even accept. The
    // console's own paths decide what happens to the process, which is what
    // `reportAndQuit` and the quit handler are for.
    expect(options.onFatalError).toBeUndefined()
    expect(options.exitEvenIfOtherHandlersAreRegistered).toBeUndefined()
    void handle.close()
  })

  it('the capture integrations are the pinned SDK’s own, and neither ends the process', () => {
    // The two names above are stubs' names until this test reads them out of the
    // installed package, which is the same thing the option-name pin below does
    // for the options object: the wiring may name an integration only if the
    // pinned version exports it, and only if what it does is what this file says
    // it does. Three readings, each of which fails on a version that moved:
    //
    //   - `@sentry/electron/main` exports both factories, and
    //     `onUncaughtExceptionIntegration` is the Electron SDK's own -- it is
    //     assigned from its own module *after* the `@sentry/node` re-exports, so
    //     the import in the wiring resolves to the Electron one and not to the
    //     Node one whose options include `exitEvenIfOtherHandlersAreRegistered`;
    //   - the Electron one's handler registers a `global.process.on(
    //     'uncaughtException')` listener, calls `captureException`, and reaches
    //     for `options.onFatalError` **only when it is set** -- there is no
    //     `process.exit` in it at all, which is why an uncaught error leaves the
    //     process where Electron's own handler left it;
    //   - the rejection one decides what to do after capturing from `mode`
    //     alone: `'warn'` writes the warning, `'strict'` calls
    //     `logAndExitProcess`, and anything else -- `'none'`, which is what this
    //     wiring passes -- does nothing.
    const consoleDir = fileURLToPath(new URL('../..', import.meta.url))
    const installed = (relative: string): string => readFileSync(join(consoleDir, relative), 'utf8')
    const exports = installed('node_modules/@sentry/electron/main/index.js')
    expect(exports).toContain('exports.onUncaughtExceptionIntegration = onuncaughtexception.onUncaughtExceptionIntegration;')
    expect(exports).toContain('exports.onUnhandledRejectionIntegration = node.onUnhandledRejectionIntegration;')

    const uncaught = installed('node_modules/@sentry/electron/main/integrations/onuncaughtexception.js')
    expect(uncaught).toContain("global.process.on('uncaughtException'")
    expect(uncaught).toContain('options.onFatalError')
    expect(uncaught).not.toContain('process.exit')
    expect(uncaught).not.toContain('exitEvenIfOtherHandlersAreRegistered')

    const rejection = installed('node_modules/@sentry/node/build/cjs/integrations/onunhandledrejection.js')
    expect(rejection).toContain('mode === "warn"')
    expect(rejection).toContain('mode === "strict"')
    expect(rejection).toContain('errorhandling.logAndExitProcess(reason)')
    // The `'none'` the wiring passes is the mode `handleRejection` answers with
    // nothing at all: the function is the two branches above and no third.
    expect(rejection.slice(rejection.indexOf('function handleRejection'))).not.toMatch(/mode === "none"/)
  })

  it('sends errors only: no transaction and no log leaves, whatever the client is handed', () => {
    const appRoot = tempAppRoot()
    const handle = startCrashReporting({ optedIn: true, appRoot })
    const options = harness.initOptions[0] ?? {}
    // Two payloads the client can be handed beside an error, each refused by
    // name rather than by a default that a later SDK could change. A
    // transaction is a record of the session rather than a stack frame; a log
    // is free text about what the console was doing. Neither is D733's "stack
    // frames only", so neither may leave.
    const beforeSendTransaction = options.beforeSendTransaction as () => unknown
    const beforeSendLog = options.beforeSendLog as () => unknown
    expect(typeof beforeSendTransaction).toBe('function')
    expect(typeof beforeSendLog).toBe('function')
    expect(beforeSendTransaction()).toBeNull()
    expect(beforeSendLog()).toBeNull()
    void handle.close()
  })

  it('names every option it sets in the pinned SDK’s own types, and not one that is not there', () => {
    // The names above are the SDK's, and this is the check the module's header
    // promises: they are read back out of the types of the version
    // `package.json` pins (`save-exact`, so the files below are the ones the
    // build compiles against), rather than trusted because they type-check.
    // `enableLogs` is the one this round wanted and could not have:
    // `@sentry/electron`'s renderer JSDoc names it, the pinned version declares
    // it nowhere, and a key the type does not have would have been an untyped
    // line that did nothing. If a later SDK adds it, this test fails on purpose
    // -- the console's answer to "are logs collected" is then a real decision
    // again instead of `beforeSendLog` alone.
    const consoleDir = fileURLToPath(new URL('../..', import.meta.url))
    const types = (relative: string): string => readFileSync(join(consoleDir, relative), 'utf8')
    const options = types('node_modules/@sentry/core/build/types/types/options.d.ts')
    expect(options).toMatch(/beforeSendTransaction\?:/)
    expect(options).toMatch(/beforeSendLog\?:/)
    const optionFiles = ['node_modules/@sentry/core/build/types/types/options.d.ts'] as const
    for (const file of optionFiles) expect(types(file)).not.toMatch(/\benableLogs\??:/)
    // `enableRuntimeChannelInjection` is the console's own newer one, and it is
    // `@sentry/node`'s rather than `@sentry/core`'s -- the option that decides
    // whether the client's constructor installs a process-wide module-loader
    // hook. Both halves are pinned: the name in the types the version declares,
    // and the `!== false` the client reads it with, because an option read as
    // `=== true` would be off for the missing key and this wiring would be
    // pinned against nothing.
    const nodeTypes = types('node_modules/@sentry/node/build/types/types.d.ts')
    expect(nodeTypes).toMatch(/enableRuntimeChannelInjection\?: boolean/)
    expect(types('node_modules/@sentry/electron/main/sdk.d.ts')).toMatch(/NodeOptions/)
    expect(types('node_modules/@sentry/node/build/cjs/sdk/client.js')).toContain(
      'options.enableRuntimeChannelInjection !== false',
    )
    // A name existing is not a name working, and the round that asked for
    // `beforeSendTransaction` asked for it as "the line that holds": the pinned
    // version declares the callback and also declares that it runs only under
    // `traceLifecycle: 'static'`, while spans stream by default -- and the
    // replacement it points at cannot drop a span at all. Both mechanisms are
    // pinned, so an SDK bump that changed the answer shows up here rather than
    // as a transaction leaving with nobody the wiser.
    expect(options).toMatch(/traceLifecycle\?: 'static' \| 'stream'/)
    expect(options).toMatch(/BeforeSendStreamedSpanCallback = \(span: StreamedSpanJSON\) => StreamedSpanJSON/)
    // And the Electron SDK's own options are the object the wiring hands
    // `init`: it is `Options` above that the two callbacks come from.
    expect(types('node_modules/@sentry/electron/main/sdk.d.ts')).toMatch(/ElectronMainOptions/)
  })

  it('closes the client when the caller closes the handle', async () => {
    const appRoot = tempAppRoot()
    const handle = startCrashReporting({ optedIn: true, appRoot })
    await handle.close()
    expect(harness.closes).toBe(1)
  })

  it('closing a reporter that never started closes nothing', async () => {
    const appRoot = tempAppRoot()
    await startCrashReporting({ optedIn: null, appRoot }).close()
    expect(harness.closes).toBe(0)
  })

  it('gives the close a bound, so a stalled transport cannot hold the quit', async () => {
    // `index.ts` waits for this on `before-quit`, so what it waits on has to end
    // on its own. The client's own answer is the flush timeout plus a transport
    // whose `flush` has none, which on a stalled endpoint is a wait that never
    // ends -- the console would never quit. The bound is the wiring's.
    vi.useFakeTimers()
    try {
      const appRoot = tempAppRoot()
      const handle = startCrashReporting({ optedIn: true, appRoot })
      harness.stalled = true
      const closing = handle.close()
      // The number is pinned as well as the name: it is the wiring's own
      // decision, and a later round that changed it would be changing how long a
      // quit may wait rather than a detail.
      expect(CLOSE_TIMEOUT_MS).toBe(2000)
      expect(harness.closeTimeouts, 'the client was handed a bound rather than left to wait').toEqual([CLOSE_TIMEOUT_MS])
      await vi.advanceTimersByTimeAsync(CLOSE_TIMEOUT_MS)
      await closing
      expect(harness.closes).toBe(1)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('what reaches the transport', () => {
  it('a thrown error arrives scrubbed, and nothing else does', async () => {
    const appRoot = tempAppRoot()
    const handle = startCrashReporting({ optedIn: true, appRoot })
    const options = harness.initOptions[0] ?? {}
    const beforeSend = options.beforeSend as (event: CrashEvent) => unknown
    const raw = hostileEvent(appRoot)

    // The SDK's own order: it builds the event, runs `beforeSend`, and sends
    // what comes back. The mutation proof for this slice is one edit to the
    // adapter -- `beforeSend: (event) => event` -- after which the token, the
    // home path and the message below are what the transport holds.
    const kept = beforeSend(raw)
    await harness.transport.send(kept)

    const onTheWire = JSON.stringify(harness.sent)
    expect(onTheWire).not.toContain(tokenPlaceholder())
    expect(onTheWire).not.toContain('person-1')
    expect(onTheWire).not.toContain('acme-secret-repo')
    expect(onTheWire).not.toContain('acme secret task text')
    expect(onTheWire).not.toContain('build-host-a')
    expect(onTheWire).not.toContain('failed')
    expect(onTheWire).not.toContain('a breadcrumb a person typed')
    // What is left is the type and the frames, which is the whole payload rule.
    expect(harness.sent).toEqual([scrubCrashEvent(raw, appRoot)])
    expect(kept).toEqual({
      event_id: 'a'.repeat(32),
      timestamp: 1700000000.5,
      platform: 'node',
      level: 'error',
      release: '0.116.0',
      exception: {
        values: [
          {
            type: 'Error',
            stacktrace: {
              frames: [
                { filename: '<external>', function: 'main', lineno: 12 },
                { filename: 'dist/main/x.js', function: 'run', lineno: 3 },
              ],
            },
          },
        ],
      },
    })
    void handle.close()
  })

})
