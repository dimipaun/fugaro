// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The crash reporter's client, running (desktop-console plan Task 2.1b, fix
// round B: the replacement for the three unit tests that asserted nothing).
//
// `test/unit/crash-electron.test.ts` drives the wiring against a **fake** SDK
// that models the two parts the wiring reads -- `init` builds the client's
// transport from the factory it is handed, and `beforeSend` runs before an
// envelope reaches that transport -- and there were three tests in it whose
// subject was everything the fake is silent about: that the transport keeps
// nothing on disk, that no `ipcMain` handler, protocol scheme or session preload
// is registered, and that the profile is untouched by a report that was sent.
// Each of them could only ever assert what the fake had been told to do, so each
// of them passed by construction. They are one test here, and it runs the pinned
// `@sentry/electron/main` 8.2.0 inside the pinned Electron 45 in a real main
// process, over a sink this spec starts on loopback.
//
// What the real SDK is asked, and what each answer is:
//
//   - **the scrubbed event is what leaves, and only it.** The fixture captures a
//     thrown error whose message carries a credential and the home directory,
//     and then hands the client an event carrying a host name, a person, a URL
//     with the credential in it, a tag, free text and a breadcrumb. Every one of
//     those is refused by `scrubCrashEvent`, so every one of them is absent from
//     what the sink receives -- and the assertion is over the bytes the sink
//     got, which is the only place a leak could be observed rather than
//     predicted. Read the other way, the payload still has the exception's type
//     and its frames, which is the whole of the rule: the report is a stack, not
//     a sentence.
//   - **no session, no transaction, no log, no client report.** The three
//     options `CRASH_CLIENT_OPTIONS` and the four `startCrashReporting` names are
//     refusals, and a refusal is only proved by the payloads that do *not*
//     arrive. The sink's items are read back by type, so a version bump that
//     started a session or a client report -- which no option of this wiring
//     would notice -- fails here.
//   - **nothing is written to the profile.** `@sentry/electron/main`'s own
//     default transport is the *offline* one, whose store persists every
//     envelope it could not deliver under `<userData>/sentry/queue`; the wiring
//     asks for the other factory, and the fixture lists the profile before and
//     after. This is also the pin that a future SDK starts tripping: if the
//     wiring stopped overriding the transport, the SDK would write there and the
//     listing would say so.
//   - **no IPC surface.** `ipcMode: 0` is the value that keeps the SDK from
//     installing its own channels, protocol and preload, and the four registries
//     are wrapped *before* the wiring starts -- a handler that was installed
//     leaves no trace to read afterwards.
//   - **the utility-process patch is taken back off.** The pinned `init` wraps
//     `electron.utilityProcess.fork` on every start, whatever `ipcMode` it was
//     given: `configureUtilityProcessIPC()` is called unconditionally
//     (`node_modules/@sentry/electron/main/sdk.js`), no option refuses it, and it
//     is not an integration that `defaultIntegrations: false` could leave out.
//     What it would leave behind opens a port to a child process and forwards
//     what the child sends with `client.sendEnvelope` -- the one path in this SDK
//     that does not pass `beforeSend` -- so `startCrashReporting` puts the
//     original fork back the moment `init` returns, and this asserts the global
//     the app is left with is not the SDK's. (The console forks no utility
//     process today; the patch would be inert, which is not the same as absent.)
//   - **no process-wide loader hook.** The same client constructor calls
//     `registerDiagnosticsChannelInjection()`, which on Electron 45's Node is
//     `Module.registerHooks({ resolve, load })` -- a hook over every module this
//     process loads, for the rest of its life. Unlike the fork patch there is
//     nothing to restore: Node has no `unregisterHooks`, so the only shape that
//     works is an option that never installs it, and this is the assertion that
//     the option did. The API is wrapped *before* the SDK is required, because a
//     hook that was installed leaves no trace to read afterwards, and the
//     orchestrion global the hook's callback writes is checked as well.
//   - **the dynamic import is the one the build emits.** `dist/main/index.js`'s
//     `import('./crash-electron.js')` is read out of the built file, resolved
//     against a copy of `dist/main/` inside a directory named `app.asar` and
//     loaded, so a specifier that names nothing, a dropped extension, or a path
//     the packer's layout breaks all fail here rather than in an installed app.
//   - **a crash is captured, and the process's exit is not the reporter's.**
//     With `defaultIntegrations: false` a client handed no list captures
//     nothing: `captureException` still works, but nothing calls it. So the
//     wiring names the SDK's two integrations by hand -- uncaught exceptions and
//     unhandled rejections -- and neither of them takes an option that would
//     decide what the process does next (`@sentry/core`'s
//     `onUncaughtExceptionIntegration` has no `exitEvenIfOtherHandlersAreRegistered`
//     and no `onFatalError`; `onUnhandledRejectionIntegration` is given
//     `mode: 'none'`, whose alternatives are the process-wide behaviours this
//     console leaves alone). What that leaves behind is Electron's own uncaught
//     handler and the console's `reportAndQuit`, exactly as before this wiring
//     existed; the unit test pins the options and the listener, and this fixture
//     captures through the SDK's API rather than by throwing at the process,
//     because a report from a process that simply died is not a payload this
//     spec could read back from its sink.
//   - **nothing at all is installed when the switch is off.** The same fixture,
//     launched with `FUGARO_CRASH_FIXTURE_OPTED_IN=0`, hands the wiring `false`:
//     no client is constructed, so the fork patch, the loader hook, the four IPC
//     registries and the profile are all untouched and the sink stays empty --
//     which is the assertion that the switch is a gate rather than a filter.
//
// Nothing here reaches the network: the sink is an `http` server on 127.0.0.1
// with a port the kernel picks, its DSN is assembled at run time from that port,
// and the fixture's Electron is the app's own pinned runtime.

import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, symlinkSync } from 'node:fs'
import { createServer } from 'node:http'
import type { Server } from 'node:http'
import { homedir, hostname, tmpdir } from 'node:os'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'

import { inert } from '../support/fakeSecrets'
import { launchConsole } from '../support/launch'

// Playwright loads a spec as CommonJS here -- the console's package.json
// declares no `"type"` -- so `__dirname` is the spec's own directory on disk,
// which is `console/test/e2e`.
const consoleDir = join(__dirname, '..', '..')

/** FIXTURE_DIR is the Electron app this spec launches: the real wiring in a real
 * main process (`test/fixtures/crash-init/main.cjs`). It is an app directory
 * rather than the console itself because the console starts its reporter only
 * when the per-user switch says so, and that switch is off in every build until
 * a person ticks it (`crash.json` absent, which reads as off, and the DSN is one
 * public constant the app carries rather than a build input) -- so the console
 * would send nothing, and a test of what the SDK does with a *running* client
 * has to start one. The fixture asks both questions: it is launched with the
 * switch on, and again with it off. */
const FIXTURE_DIR = join(consoleDir, 'test', 'fixtures', 'crash-init')

/** NO_DISPLAY is what the fixture's Electron needs to start where nothing has a
 * display: Chromium initializes a platform at process start even though this app
 * opens no window, and `--ozone-platform=headless` is the display CI's
 * `xvfb-run` would otherwise provide. It is added on Linux only, which is the
 * one platform whose Chromium needs to be told. */
const NO_DISPLAY = process.platform === 'linux' ? ['--ozone-platform=headless'] : []

/** Items is one envelope read back: the type of each item it carries, and the
 * payload beside it. An envelope is a JSON header line followed by a header and
 * a payload line per item (`@sentry/core`'s format), which is what the sink
 * receives verbatim. */
interface Item {
  readonly type: string
  readonly payload: Record<string, unknown>
}

function itemsOf(envelope: string): readonly Item[] {
  const lines = envelope.split('\n')
  const items: Item[] = []
  for (let index = 1; index + 1 < lines.length; index += 2) {
    const header = JSON.parse(lines[index] as string) as { readonly type?: unknown }
    const payload = JSON.parse(lines[index + 1] as string) as Record<string, unknown>
    items.push({ type: String(header.type), payload })
  }
  return items
}

/** Report is what the fixture observed and wrote out. It cannot be asked from
 * outside: the assertions are about a start that is over by the time Playwright
 * could evaluate in the app. */
interface Report {
  readonly fatal?: string
  readonly forkWrapped?: boolean
  readonly loaderHooks?: readonly string[]
  readonly orchestrion?: unknown
  readonly entryImport?: { readonly loaded?: boolean }
  readonly before?: readonly string[]
  readonly after?: readonly string[]
  readonly recorded?: { readonly ipcs?: readonly string[]; readonly protocols?: readonly string[]; readonly preloads?: readonly string[] }
}

/** Sink is the loopback server the client's DSN points at, and the envelopes it
 * received in order. */
interface Sink {
  readonly dsn: string
  readonly server: Server
  readonly envelopes: string[]
}

async function startSink(): Promise<Sink> {
  const envelopes: string[] = []
  const server = createServer((request, response) => {
    let body = ''
    request.setEncoding('utf8')
    request.on('data', (chunk: string) => {
      body += chunk
    })
    request.on('end', () => {
      envelopes.push(body)
      response.writeHead(200)
      response.end()
    })
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (address === null || typeof address === 'string') throw new Error('the sink took no loopback port')
  return {
    // The public key is a run of one letter: a DSN's *shape* is what the client
    // parses, and `test/support/fakeSecrets.ts` is where a test gets a value
    // that looks like a credential without any line of the tree carrying one.
    dsn: inert('http', '://', 'a'.repeat(32), '@127.0.0.1:', String(address.port), '/', '1'),
    server,
    envelopes,
  }
}

/** closeSink stops the server and answers when the port is released, so a spec
 * that asserted nothing arrived does not leave a listener behind a failed test. */
function closeSink(sink: Sink): Promise<void> {
  return new Promise((resolve) => sink.server.close(() => resolve()))
}

/** waitForReport polls for the file the fixture writes, because the fixture
 * quits itself: the spec learns the app is done from the file rather than from
 * Playwright, which would otherwise have to race an `app.close()` against the
 * envelopes still in flight. */
async function waitForReport(path: string, timeoutMs: number): Promise<Report> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (existsSync(path)) return JSON.parse(readFileSync(path, 'utf8')) as Report
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  throw new Error(`the crash fixture wrote no report to ${path}`)
}

/** appAsarLayout is the packed app's shape, to the one degree this test needs it:
 * a copy of the built `dist/main/` inside a directory named `app.asar`, which is
 * the segment Electron's own loader treats as an archive. It is built here rather
 * than in the fixture because Electron's fs patches intercept a path with that
 * segment -- a directory named `app.asar` cannot be *created* there, only read --
 * and because the resolution this holds to is Node's: the module graph reaches
 * `electron` and `@sentry/electron` through the `node_modules` beside the entry,
 * which is the console's own. It answers the file the entry's dynamic import
 * resolves to, so the specifier is checked against the layout rather than
 * assumed. */
function appAsarLayout(): { readonly resolved: string; readonly specifier: string } {
  const entry = readFileSync(join(consoleDir, 'dist', 'main', 'index.js'), 'utf8')
  const emitted = [...entry.matchAll(/import\(\s*['"]([^'"]+)['"]\s*\)/g)].map((match) => match[1])
  // One, and it is the crash reporter's: a second dynamic import would be a
  // module the entry loads without the gate having been asked.
  expect(emitted, 'the built entry emits exactly one dynamic import').toEqual(['./crash-electron.js'])
  const root = join(mkdtempSync(join(tmpdir(), 'fugaro-crash-app-')), 'app.asar')
  mkdirSync(join(root, 'dist'), { recursive: true })
  cpSync(join(consoleDir, 'dist', 'main'), join(root, 'dist', 'main'), { recursive: true })
  symlinkSync(join(consoleDir, 'node_modules'), join(root, 'node_modules'), 'dir')
  const specifier = emitted[0] as string
  const resolved = join(root, 'dist', 'main', specifier)
  expect(existsSync(resolved), `the emitted specifier resolves under the packed layout: ${specifier}`).toBe(true)
  return { resolved, specifier }
}

/** envFor is the child's whole environment: this process's, so Electron finds
 * its display and its libraries the way CI gives them to it, plus the fixture's
 * inputs. `optedIn` is the switch's answer, and it defaults to the yes the first
 * test needs: the fixture reads it as a string, so `'0'` is the no and anything
 * else -- including this default -- is the yes. */
function envFor(sink: Sink, reportPath: string, importPath: string, optedIn = true): Record<string, string> {
  const env: Record<string, string> = {}
  for (const [key, value] of Object.entries(process.env)) {
    if (typeof value === 'string') env[key] = value
  }
  env['FUGARO_CRASH_FIXTURE_DSN'] = sink.dsn
  env['FUGARO_CRASH_FIXTURE_REPORT'] = reportPath
  env['FUGARO_CRASH_FIXTURE_IMPORT'] = importPath
  env['FUGARO_CRASH_FIXTURE_OPTED_IN'] = optedIn ? '1' : '0'
  return env
}

test('the real client sends the scrubbed event, and nothing else', async () => {
  const sink = await startSink()
  const profile = mkdtempSync(join(tmpdir(), 'fugaro-crash-profile-'))
  const reportPath = join(mkdtempSync(join(tmpdir(), 'fugaro-crash-report-')), 'report.json')
  const layout = appAsarLayout()

  const app = await launchConsole({
    args: [FIXTURE_DIR, `--user-data-dir=${profile}`, ...NO_DISPLAY],
    env: envFor(sink, reportPath, layout.resolved),
  })

  let report: Report
  try {
    report = await waitForReport(reportPath, 60_000)
  } finally {
    await app.close().catch(() => undefined)
    await closeSink(sink)
  }

  // A start that threw is reported rather than waited on: without this the
  // assertions below would read an empty sink and fail about the wrong thing.
  expect(report.fatal, 'the fixture started, reported and quit').toBeUndefined()

  // **What left, by type.** Two error events and nothing else: a session, a
  // transaction, a log or a client report would each be an item here, and each
  // of them is a payload the wiring refuses by name or by not starting what
  // would produce it.
  const items = sink.envelopes.flatMap(itemsOf)
  expect(items.map((item) => item.type)).toEqual(['event', 'event'])

  // **What is in them.** The bytes are read as text, because that is what a
  // leak is: a value that survived the scrubber anywhere in the payload, not
  // only in the field it was written to.
  const token = inert('gh', 'p_', 'a'.repeat(36))
  const onTheWire = sink.envelopes.join('\n')
  for (const refused of [
    token,
    'person-1',
    'acme-secret-repo',
    'acme secret task text',
    'a breadcrumb a person typed',
    'example.invalid',
    'failed',
    'launching',
  ]) {
    expect(onTheWire, `${refused} is not a value a report may carry`).not.toContain(refused)
  }

  // The two identifiers the machine would have supplied: the home directory the
  // fixture's message names, and the host name it put on the event. Neither is
  // in the payload -- `scrubCrashEvent` drops the message whole and keeps five
  // fields of an event, and the SDK's own `includeServerName: false` means the
  // second is only ever a value *this* fixture wrote.
  expect(onTheWire).not.toContain(homedir())
  expect(onTheWire).not.toContain(hostname())

  // **The stack is what is left**: the thrown error is one exception with frames,
  // at least one of which is a file of the fixture's own app, named relative to
  // the app rather than by its path on this machine.
  const thrown = items.find(
    (item) => (item.payload['exception'] as { readonly values?: readonly { readonly type?: unknown }[] } | undefined)?.values?.[0]?.type === 'Error',
  )
  expect(thrown, 'the thrown error is one of the two events').toBeDefined()
  const values = (thrown?.payload['exception'] as { readonly values: readonly { readonly stacktrace?: { readonly frames?: readonly { readonly filename?: unknown }[] } }[] }).values
  const frames = values[0]?.stacktrace?.frames ?? []
  expect(frames.length, 'the exception kept its frames').toBeGreaterThan(0)
  expect(frames.map((frame) => frame.filename)).toContain('main.cjs')
  expect(onTheWire).not.toContain(FIXTURE_DIR)

  // **Nothing was written to the profile.** The offline transport's store is
  // `<userData>/sentry/queue`, so the absence of any `sentry` path is the claim
  // that the wiring asked for the transport that keeps nothing.
  const written = report.after ?? []
  expect(written.filter((path) => /sentry/i.test(path))).toEqual([])

  // **No IPC surface**: the four registries the SDK could have touched, recorded
  // from before the wiring started.
  expect(report.recorded?.ipcs ?? []).toEqual([])
  expect(report.recorded?.protocols ?? []).toEqual([])
  expect(report.recorded?.preloads ?? []).toEqual([])

  // **The utility-process patch is not left behind.** The pinned `init` installs
  // it unconditionally and the wiring takes it off again; the header carries what
  // the patch would forward and why.
  expect(report.forkWrapped, 'the SDK\'s patch on electron.utilityProcess.fork is gone by the time the app runs').toBe(
    false,
  )

  // **No process-wide loader hook was installed.** The pinned `NodeClient`
  // calls `registerDiagnosticsChannelInjection()` in its constructor, and on
  // this Node that is `Module.registerHooks({ resolve, load })`: a loader hook
  // for the whole process that Node offers no way to remove, so nothing -- not
  // `close()` and not this wiring -- could take it back off. It is refused by
  // an option instead, and this is the observation that the option worked: the
  // API was never reached, and the global the SDK's bookkeeping hangs off
  // (`GLOBAL_OBJ.__SENTRY_ORCHESTRION__`, set by the hook's callback) is absent.
  expect(report.loaderHooks ?? [], 'the SDK installed no module-loader hook').toEqual([])
  expect(report.orchestrion ?? null, 'no orchestrion bookkeeping is left on the global').toBeNull()

  // **The emitted dynamic import loads**, from the packed layout: the specifier
  // above is the built file's rather than the source's, and the fixture imported
  // what it resolved to in the runtime the app runs on.
  expect(report.entryImport?.loaded, 'loading the emitted specifier yields the wiring the app starts').toBe(true)
})

test('the switch off installs nothing, and sends nothing', async () => {
  const sink = await startSink()
  const profile = mkdtempSync(join(tmpdir(), 'fugaro-crash-profile-'))
  const reportPath = join(mkdtempSync(join(tmpdir(), 'fugaro-crash-report-')), 'report.json')
  const layout = appAsarLayout()

  const app = await launchConsole({
    args: [FIXTURE_DIR, `--user-data-dir=${profile}`, ...NO_DISPLAY],
    env: envFor(sink, reportPath, layout.resolved, false),
  })

  let report: Report
  try {
    report = await waitForReport(reportPath, 60_000)
  } finally {
    await app.close().catch(() => undefined)
    await closeSink(sink)
  }
  expect(report.fatal, 'the fixture started, reported and quit').toBeUndefined()

  // **The switch is a gate rather than a filter.** The fixture captured an error
  // and an event through the SDK's API exactly as the first test's fixture does,
  // and the sink is where a client that existed would have put them. Nothing
  // arrived, because no client was constructed at all: the wiring's first act is
  // the decision, and a no returns the handle that closes nothing.
  expect(sink.envelopes, 'a switch that is off sends nothing, whatever it is handed').toEqual([])

  // **And nothing was installed on the way to that answer.** Each of these is
  // the SDK's doing that the wiring cannot see after the fact -- the fork patch,
  // the process-wide loader hook, the bookkeeping global, the four registries --
  // or a file the profile would carry, and each is untouched here for the same
  // reason the first test has to assert them: the *absence* is the claim.
  expect(report.forkWrapped, 'no utility-process patch was installed').toBe(false)
  expect(report.loaderHooks ?? [], 'no module-loader hook was installed').toEqual([])
  expect(report.orchestrion ?? null, 'no orchestrion bookkeeping is left on the global').toBeNull()
  expect(report.recorded?.ipcs ?? []).toEqual([])
  expect(report.recorded?.protocols ?? []).toEqual([])
  expect(report.recorded?.preloads ?? []).toEqual([])
  const written = report.after ?? []
  expect(written.filter((path) => /sentry/i.test(path))).toEqual([])

  // The module the entry imports is still the file a yes would load, so the
  // claim above is about what the wiring does with the answer rather than about
  // a build that dropped it.
  expect(report.entryImport?.loaded).toBe(true)
})
