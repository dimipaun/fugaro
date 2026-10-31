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
//   1. **Nothing starts before the switch is read, and nothing starts without a
//      DSN.** The gate is `./crash`'s, and this file is about the half the
//      wiring owns: that a closed answer calls no `init` at all rather than
//      starting a reporter that decides later to send nothing.
//   2. **What reaches the transport is the scrubbed event, and only it.** The
//      headline case below is this slice's mutation proof: with `beforeSend`
//      changed to hand the event back untouched, the token, the home path and
//      the message are what the transport receives.
//   3. **The entry loads this module only when the gate can open, and the wiring
//      reads the artifact only for a switch that says yes.** The gate is an
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

import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { inert } from '../support/fakeSecrets'
import { scrubCrashEvent, type CrashEvent } from '../../src/main/crash'
import { CLOSE_TIMEOUT_MS, crashDsnFromBuild, startCrashReporting } from '../../src/main/crash-electron'
import { CRASH_DSN_VARIABLE, writeCrashDsnArtifact } from '../../tools/crash-dsn'

/** harness is the shared state the mocked SDK writes into. It is `vi.hoisted`
 * because the factory below is hoisted above this file's imports, so it cannot
 * close over a variable declared here. */
const harness = vi.hoisted(() => ({
  /** Every options object `init` was handed, in order. */
  initOptions: [] as Record<string, unknown>[],
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

/** artifactReads is every path this module graph asked `node:fs` to read, in
 * order. It is a probe rather than a fake -- the real `readFileSync` still runs,
 * and the value a reader gets is the file's -- so that "the artifact was not
 * read" is an observation about the wiring's own call and not an inference from
 * the fact that a read of a missing file answers null anyway. */
const artifactReads = vi.hoisted(() => ({ paths: [] as string[] }))

vi.mock('node:fs', async (importOriginal) => {
  const actual = await importOriginal<typeof import('node:fs')>()
  const reading = actual.readFileSync as (...args: unknown[]) => unknown
  return {
    ...actual,
    readFileSync: (path: unknown, ...rest: unknown[]) => {
      artifactReads.paths.push(String(path))
      return reading(path, ...rest)
    },
  }
})

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
}))

/** dsnPlaceholder is a DSN's shape -- scheme, public key, host, project id --
 * assembled from fragments at run time, for the reason `fakeSecrets` exists and
 * for the same reason the scrubber's own test does it: a DSN-shaped literal in
 * this repository is a defect even when it is invented, and `example.invalid` is
 * the reserved name, so nothing here resolves anywhere. */
function dsnPlaceholder(): string {
  return inert('https', '://', 'a'.repeat(32), '@', 'example', '.invalid', '/', '1')
}

/** tokenPlaceholder is a credential-shaped value, assembled here. */
function tokenPlaceholder(): string {
  return inert('gh', 'p_', 'a'.repeat(36))
}

/** tempAppRoot is a fresh temp directory per test: the DSN artifact's root and
 * the prefix a kept frame's path is cut at. It is what `app.getAppPath()`
 * answers with, and the DSN the build wrote is the only thing in it. */
function tempAppRoot(): string {
  return mkdtempSync(join(tmpdir(), 'fugaro-crash-app-'))
}

/** consoleFrame is a frame path that is genuinely inside this app, so the
 * scrubber has something to place; `hostileEvent` takes the prefix because the
 * app root is a temp directory here. */
function appFrame(appRoot: string, ...rest: string[]): string {
  return join(appRoot, ...rest)
}

/** writeRawDsn puts arbitrary bytes where the artifact goes. The happy path goes
 * through the build step itself (`writeCrashDsnArtifact`); this is for the two
 * files the build step never writes -- one that is not JSON, and one that is
 * JSON of another shape -- which the reader still has to refuse. */
function writeRawDsn(appRoot: string, contents: string): void {
  mkdirSync(join(appRoot, 'dist', 'main'), { recursive: true })
  writeFileSync(join(appRoot, 'dist', 'main', 'crash-dsn.json'), contents)
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
  harness.sent = []
  harness.closes = 0
  harness.closeTimeouts = []
  harness.stalled = false
  harness.forksPatched = 0
  utility.process.fork = utility.real
  artifactReads.paths = []
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('the crash reporter starts only when the gate opens', () => {
  it('starts nothing while the switch is unread, off, or not a yes', () => {
    const appRoot = tempAppRoot()
    // The DSN the build was given is present in every case below: the point is
    // that a DSN alone does not open the gate, which is D734 and the reason the
    // switch is read first.
    writeCrashDsnArtifact(appRoot, dsnPlaceholder())
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

  it('does not read the build\'s DSN artifact for a switch that is not a yes', () => {
    // The order the wiring asks the gate in is a claim about the disk, and this
    // is the disk: the artifact is present in every case below, so a wiring that
    // read it first would be visible here as a path the probe recorded, and the
    // null a read of a missing file answers with could not stand in for that.
    const appRoot = tempAppRoot()
    writeCrashDsnArtifact(appRoot, dsnPlaceholder())
    for (const optedIn of [null, false, 'true', 1, { consent: true }]) {
      void startCrashReporting({ optedIn, appRoot }).close()
    }
    expect(artifactReads.paths.filter((path) => path.includes('crash-dsn.json'))).toEqual([])

    // ...and the same probe answers for the other direction, so a wiring that
    // had stopped reading the artifact altogether could not pass the assertion
    // above by reading nothing anywhere.
    artifactReads.paths = []
    void startCrashReporting({ optedIn: true, appRoot }).close()
    expect(artifactReads.paths.filter((path) => path.includes('crash-dsn.json'))).toHaveLength(1)
  })

  it('starts nothing when the build carried no DSN, or a blank one', () => {
    const appRoot = tempAppRoot()
    // No artifact at all: the dormant build the owner's ruling describes.
    void startCrashReporting({ optedIn: true, appRoot })
    expect(harness.initOptions).toEqual([])
    // A build that was handed a blank variable writes nothing, so the next call
    // sees the same absent artifact: the blank value and the unset one are one
    // case here, which is what they are on the build side too.
    writeCrashDsnArtifact(appRoot, '   ')
    void startCrashReporting({ optedIn: true, appRoot })
    expect(harness.initOptions).toEqual([])
    // And a file that is not the shape the build step writes is refused rather
    // than read for whatever it holds.
    writeRawDsn(appRoot, 'not json at all')
    expect(crashDsnFromBuild(appRoot)).toBeNull()
    const wrongShape = tempAppRoot()
    writeRawDsn(wrongShape, JSON.stringify({ endpoint: 'somewhere' }))
    expect(crashDsnFromBuild(wrongShape)).toBeNull()
    void startCrashReporting({ optedIn: true, appRoot: wrongShape })
    expect(harness.initOptions).toEqual([])
  })

  it('reads the artifact the build step writes, and nothing from the environment', () => {
    // The two halves of one mechanism, held together: `npm run build`'s last
    // step writes the artifact and this module reads it, so what the build puts
    // down has to be what the app picks up. The env is set to a different value
    // on purpose -- the running console is launched by whatever launcher a
    // person used, so the environment is not the build's, and reading it would
    // be reading a setting of the machine.
    const appRoot = tempAppRoot()
    const dsn = dsnPlaceholder()
    expect(writeCrashDsnArtifact(appRoot, dsn)).toBe('written')
    vi.stubEnv(CRASH_DSN_VARIABLE, 'a value the running app must not read')
    expect(crashDsnFromBuild(appRoot)).toBe(dsn)
    vi.unstubAllEnvs()
  })

  it('the build step removes the artifact when it was given no DSN', () => {
    // This is the dormant release: the owner's build without the variable. The
    // removal is the assertion that matters -- an artifact an earlier build left
    // in the same directory would otherwise ship one build's DSN inside a bundle
    // whose builder believes reporting is off.
    const appRoot = tempAppRoot()
    expect(writeCrashDsnArtifact(appRoot, dsnPlaceholder())).toBe('written')
    expect(crashDsnFromBuild(appRoot)).not.toBeNull()
    expect(writeCrashDsnArtifact(appRoot, '   ')).toBe('removed')
    expect(crashDsnFromBuild(appRoot)).toBeNull()
    expect(writeCrashDsnArtifact(appRoot, undefined)).toBe('removed')
    expect(crashDsnFromBuild(appRoot)).toBeNull()
  })

  it('the build step names the release variable once, and the app never reads it', () => {
    // One spelling in the tree, and this is it: a second one could only drift
    // from this one, and a drifted name means a release build the owner believes
    // reports and a console that stays dormant.
    expect(CRASH_DSN_VARIABLE).toBe('FUGARO_CONSOLE_CRASH_DSN')
    const reader = readFileSync(
      fileURLToPath(new URL('../../src/main/crash-electron.ts', import.meta.url)),
      'utf8',
    )
    expect(reader).not.toContain(CRASH_DSN_VARIABLE)
    expect(reader).not.toContain('process.env')
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
    writeCrashDsnArtifact(appRoot, dsnPlaceholder())
    const handle = startCrashReporting({ optedIn: true, appRoot })
    expect(harness.forksPatched, 'the mocked SDK patches fork during init').toBe(1)
    expect(
      utility.process.fork,
      'the fork the app started with is the one it still has: the SDK\'s Proxy is gone',
    ).toBe(utility.real)
    void handle.close()
  })

  it('starts the client with the artifact DSN when the switch says yes', () => {
    const appRoot = tempAppRoot()
    const dsn = dsnPlaceholder()
    writeCrashDsnArtifact(appRoot, dsn)
    const handle = startCrashReporting({ optedIn: true, appRoot })
    expect(harness.initOptions).toHaveLength(1)
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
    // Four shapes, because each one fails differently: a second call would start
    // a second client (two transports, two event ids); a call with anything but
    // `null` would be a slice deciding the switch before the switch exists; a
    // **static** import of the module would load `@sentry/electron/main` and the
    // fourteen packages it reaches at every start, whatever the gate answers;
    // and no call on the quit path is a report cut off mid-send.
    const entry = withoutComments(
      readFileSync(fileURLToPath(new URL('../../src/main/index.ts', import.meta.url)), 'utf8'),
    )
    const starts = [...entry.matchAll(/startCrashReporting\(\{([^}]*)\}\)/g)].map((match) => match[1]?.trim())
    expect(starts).toEqual(['optedIn, appRoot: app.getAppPath()'])
    expect([...entry.matchAll(/crashReporting\?\.close\(\)/g)]).toHaveLength(1)

    // The module is reached dynamically, once, and only from behind the gate: a
    // static reach would load it before the decision exists to make, so the only
    // line allowed to carry `from './crash-electron'` is the erased type import.
    const staticLoads = entry
      .split('\n')
      .filter((line) => line.includes("from './crash-electron'") && !line.trimStart().startsWith('import type'))
    expect(staticLoads).toEqual([])
    expect([...entry.matchAll(/import\('\.\/crash-electron\.js'\)/g)]).toHaveLength(1)

    // ...and the gate is an **allow test**: `optedIn === true`, and nothing else
    // -- no `./crash`, no reason, no list of the answers that shut it. A state
    // nobody has defined yet is off because it is not a yes, so the switch slice
    // can add to what a yes is without this file learning a name.
    expect(entry).not.toContain('crashReportingDecision')
    expect(entry).not.toContain("from './crash'")
    const allow = entry.indexOf('if (optedIn === true)')
    const load = entry.indexOf("import('./crash-electron.js')")
    expect(allow, 'the entry asks the gate with an allow test').toBeGreaterThan(-1)
    expect(load).toBeGreaterThan(allow)
    const branch = entry.slice(allow, load)
    expect(branch, 'no line closes the block before the load, so the load is inside it').not.toMatch(/^\s*\}/m)
    expect(entry.slice(allow, entry.indexOf('}', load))).toContain('startCrashReporting(')
  })

  it('the wiring reads the build\'s DSN artifact only for a switch that says yes', () => {
    // The one order that matters inside the wiring, and it is an allow test
    // too: `crashDsnFromBuild` is on the yes side of `input.optedIn === true`,
    // which is the half of the gate about the disk -- a start whose switch is
    // off must not have opened the artifact the build wrote -- and
    // `crashReportingDecision` is asked once, after it, about the switch and the
    // DSN together. There is no separate switch-first call any more: the reason
    // it existed (`'no-dsn'`) was a spelling of "a yes whose DSN has not been
    // read yet", which is what the expression below says outright.
    const wiring = withoutComments(
      readFileSync(fileURLToPath(new URL('../../src/main/crash-electron.ts', import.meta.url)), 'utf8'),
    )
    const start =
      /export function startCrashReporting\(input: CrashReportingInput\): CrashReportingHandle \{[\s\S]*?\n\}\n/.exec(
        wiring,
      )
    if (start === null) throw new Error('the scan found no startCrashReporting: it would pass by finding nothing')
    expect(start[0]).toContain(
      'const dsn = input.dsn ?? (input.optedIn === true ? crashDsnFromBuild(input.appRoot) : null)',
    )
    const artifactAt = start[0].indexOf('crashDsnFromBuild(input.appRoot)')
    const decideAt = start[0].indexOf('crashReportingDecision({ dsn, optedIn: input.optedIn })')
    expect(artifactAt, 'the artifact is read at all').toBeGreaterThan(-1)
    expect(decideAt, 'the gate decides after the artifact is read').toBeGreaterThan(artifactAt)
    expect(start[0]).not.toContain("reason !== 'no-dsn'")
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
    writeCrashDsnArtifact(appRoot, dsnPlaceholder())
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
    // Nothing else is added: an integrations list here would be a way to turn
    // one of those back on without touching CRASH_CLIENT_OPTIONS.
    expect(options.integrations).toBeUndefined()
    void handle.close()
  })

  it('sends errors only: no transaction and no log leaves, whatever the client is handed', () => {
    const appRoot = tempAppRoot()
    writeCrashDsnArtifact(appRoot, dsnPlaceholder())
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
    writeCrashDsnArtifact(appRoot, dsnPlaceholder())
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
      writeCrashDsnArtifact(appRoot, dsnPlaceholder())
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

    writeCrashDsnArtifact(appRoot, dsnPlaceholder())
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
