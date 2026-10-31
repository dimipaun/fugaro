// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The app `test/e2e/crash-init.spec.ts` launches: the console's real crash
// wiring, started in a real Electron main process and pointed at the spec's
// loopback sink (desktop-console plan Task 2.1b, fix round B).
//
// **Why an Electron app and not a module.** The claims this fixture exists to
// make are claims about the pinned SDK, and the SDK's own bookkeeping is what
// decides them: which transport `init` builds when it is handed one, whether a
// utility process's `fork` is wrapped, what a client that is never started does
// to a profile. A fake SDK -- the unit suite's -- models the parts the wiring
// reads and is silent about the rest, so the third of those claims it cannot make
// at all. Here `@sentry/electron/main` is the pinned 8.2.0, `electron` is the
// pinned Electron 45, and the wiring is the console's own built
// `dist/main/crash-electron.js` -- the code `npm run build` writes into the app,
// reached through the same relative module graph the app has.
//
// **What it reports.** It cannot be asked from outside: the assertions are about
// what a *running* main process observed, and by the time Playwright could
// evaluate in it the observations are over. So it writes them to the report file
// the spec names and quits: whether `init` wrapped `utilityProcess.fork`, what
// the userData directory held before and after a report was sent, and whether
// anything registered an `ipcMain` channel, a protocol scheme, a session preload.
// The four registries are wrapped *before* the wiring starts, because a
// registration that already happened leaves no trace to read afterwards.
//
// **Its two inputs are env**: the report's path, and the DSN the spec built at
// run time from its sink's own loopback address. Nothing here holds a DSN, a
// host or an endpoint; the token and the home path below are assembled from
// fragments at run time, which is the rule `test/support/fakeSecrets.ts` keeps
// and the reason no line of this file carries a shape a scanner looks for.
'use strict'

const { app, ipcMain, protocol, session, utilityProcess } = require('electron')
const { readdirSync, writeFileSync } = require('node:fs')
const Module = require('node:module')
const { join } = require('node:path')
const { homedir, hostname } = require('node:os')
const { pathToFileURL } = require('node:url')
const { types } = require('node:util')

/** consoleRoot is the console directory this fixture lives in: `test/fixtures/
 * crash-init/` is three levels down from it. The built wiring is required from
 * there by path rather than by specifier, because a specifier would resolve
 * against this fixture's own package.json. */
const consoleRoot = join(__dirname, '..', '..', '..')

const reportPath = process.env.FUGARO_CRASH_FIXTURE_REPORT ?? ''
const dsn = process.env.FUGARO_CRASH_FIXTURE_DSN ?? ''

/** A credential's shape and the machine's own identifiers, assembled here and
 * used only as values the *report must not carry*: the scrubber may not pass on
 * an exception's message, and the SDK may not pass on the host name it knows. */
const token = ['gh', 'p_', 'a'.repeat(36)].join('')
const home = homedir()
const host = hostname()

/** recorded is every registration the wiring must not make, in the four
 * registries Electron offers for them. Each is a wrapper installed before the
 * crash reporter starts, so the recording is of what the *SDK* did rather than
 * of what some later read could still see. */
const recorded = { ipcs: [], protocols: [], preloads: [] }

/** watch wraps one method of one of those registries, keeping its first argument
 * -- the channel, the scheme -- and calling through, so the app behaves exactly
 * as it would have. */
function watch(target, method, into) {
  const original = target[method]
  target[method] = function (...args) {
    into.push(String(args[0]))
    return original.apply(this, args)
  }
}

/** listing is a directory's contents as paths, recursively, or an empty list for
 * one that does not exist yet. The spec reads it for a `sentry` path: that is
 * where the SDK's *offline* transport persists what it could not deliver, so its
 * absence is the claim that the wiring asked for the other factory. */
function listing(root) {
  try {
    return readdirSync(root, { recursive: true }).map(String)
  } catch {
    return []
  }
}

/** loaderHooks is every module-loader hook the SDK installed, in order, named by
 * the hooks it registered. Node has no `unregisterHooks`, so a process-wide
 * loader hook is observable only *before* it is installed -- which is why this
 * is a wrapper rather than a read of some state afterwards. */
const loaderHooks = []

/** watchLoaderHooks wraps `Module.registerHooks` before anything is required: on
 * the Node the pinned Electron embeds the SDK reaches for this API first, and it
 * rewrites matching modules for the life of the process. */
function watchLoaderHooks() {
  const real = Module.registerHooks
  if (typeof real !== 'function') return
  Module.registerHooks = function (hooks, ...rest) {
    loaderHooks.push(Object.keys(hooks ?? {}).join(','))
    return real.call(this, hooks, ...rest)
  }
}

app
  .whenReady()
  .then(async () => {
    watch(ipcMain, 'on', recorded.ipcs)
    watch(ipcMain, 'handle', recorded.ipcs)
    watch(protocol, 'handle', recorded.protocols)
    watch(protocol, 'registerSchemesAsPrivileged', recorded.protocols)
    // The default session is a getter that throws before the app is ready, which
    // is why every one of these wrappers is inside this callback.
    watch(session.defaultSession, 'registerPreloadScript', recorded.preloads)
    watchLoaderHooks()

    const { startCrashReporting } = require(join(consoleRoot, 'dist', 'main', 'crash-electron.js'))
    const sdk = require('@sentry/electron/main')

    const userData = app.getPath('userData')
    const appRoot = app.getAppPath()
    const before = listing(userData)

    // Opted in, and handed the spec's DSN: the gate the wiring would otherwise
    // leave shut is deliberately open here, which is the whole point -- what is
    // being tested is what the SDK does once it is running, not whether it runs.
    const handle = startCrashReporting({ optedIn: true, appRoot, dsn })
    // The wiring puts `utilityProcess.fork` back the moment `init` returns:
    // the SDK's `configureUtilityProcessIPC` patches it on every start, and the
    // spec's header carries what that patch costs and why the console takes it
    // off. What is read here is the global the *app* is left with.
    const forkWrapped = typeof utilityProcess.fork === 'function' && types.isProxy(utilityProcess.fork)

    // **No process-wide loader hook.** The SDK's `NodeClient` constructor calls
    // `registerDiagnosticsChannelInjection()`, which on this Node rewrites
    // matching modules through `Module.registerHooks` -- for the life of the
    // process, and with no way to remove it, `close()` included. The wiring
    // refuses it with an option rather than a restore, which is the only shape
    // that works for a hook Node offers no unregister for; what is read here is
    // that the API was never reached and that the global the SDK's bookkeeping
    // hangs off was never created.
    const orchestrion = globalThis.__SENTRY_ORCHESTRION__ ?? null

    // **The dynamic import the build emits.** The spec reads the specifier out
    // of `dist/main/index.js`, lays a copy of `dist/main/` out inside a directory
    // named `app.asar` and names the file the specifier resolved to here -- this
    // process only loads it. That is the half worth a real runtime: the file the
    // entry's `import('./crash-electron.js')` names, imported the way the app
    // imports it (a `file:` URL, through the asar-aware loader), so a specifier
    // that names nothing, a dropped extension or a path the packed layout breaks
    // all fail rather than being resolved on paper.
    const importPath = process.env.FUGARO_CRASH_FIXTURE_IMPORT ?? ''
    const entryImport = { loaded: false }
    if (importPath !== '') {
      const loaded = await import(pathToFileURL(importPath).href)
      entryImport.loaded = typeof loaded.startCrashReporting === 'function'
    }

    // A thrown error: the shape a crash report is for, with a message that
    // carries the credential and the home path the report may not.
    let thrown
    try {
      throw new Error(`the run under ${home} failed with ${token}`)
    } catch (error) {
      thrown = error
    }
    sdk.captureException(thrown)

    // ...and an event built by hand, because the fields a *real* crash carries
    // are not the fields this client would fill in: `includeServerName: false`
    // means the SDK adds no host name, so the only way to test that one is
    // scrubbed is to hand the pipeline an event that has it. Every value here is
    // invented, and each is one the payload rule refuses by name -- a message, a
    // person, a URL with a credential in it, a tag, free text, a breadcrumb.
    sdk.captureEvent({
      message: `launching ${token} failed`,
      level: 'error',
      platform: 'node',
      release: '0.0.0-fixture',
      server_name: host,
      user: { id: 'person-1', email: 'person-1@example.invalid' },
      request: { url: `https://example.invalid/x?token=${token}` },
      tags: { 'acme-secret-repo': 'owner/repo-a' },
      extra: { task: 'acme secret task text' },
      breadcrumbs: [{ message: 'a breadcrumb a person typed' }],
      contexts: { device: { name: 'a machine' } },
    })

    // Long enough for both envelopes to be flushed to the sink and for anything
    // the SDK wanted to write to have been written: a profile write is
    // asynchronous, so a listing taken at once would answer about the moment
    // rather than about the run.
    await new Promise((resolve) => setTimeout(resolve, 2500))

    writeFileSync(
      reportPath,
      JSON.stringify(
        { forkWrapped, loaderHooks, orchestrion, entryImport, before, after: listing(userData), recorded },
        null,
        2,
      ),
    )
    await handle.close()
    app.quit()
  })
  .catch((error) => {
    // A start that failed is a fact the spec must see rather than a launch that
    // hangs: it is written to the same report, and the exit code is non-zero.
    writeFileSync(reportPath, JSON.stringify({ fatal: String(error) }))
    app.exit(1)
  })
