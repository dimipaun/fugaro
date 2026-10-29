// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The hardened window in a real Electron process (desktop-console plan Task
// 1.1; design section 5.1 rules 1, 2 and 3).
//
// `test/unit/window.test.ts` asserts what the console decides; this file asserts
// what the browser does with those decisions, because the two are not the same
// claim. A `webPreferences` object can be constructed correctly and still be
// ignored, a CSP can be in the response and still be bypassed by a flag that
// nobody looked at, and `will-navigate` can be wired to a handler that runs and
// still leaves the window somewhere it should not be. So the app is launched
// here, for real, and every assertion below is made against the running
// renderer -- through Playwright where it is a property of the page, and through
// `electronApp.evaluate` where it is a property of the main process.
//
// The network is the interesting case. Two gates stand between the renderer and
// anything remote, and they fail in different ways: the CSP of rule 3
// (`connect-src 'none'`) refuses the request inside Blink, before the network
// stack hears about it, and the Playwright route handler below aborts every
// `http(s)` request that gets that far. The probe asserts the first gate by its
// own evidence -- a `securitypolicyviolation` event naming `connect-src`, which
// a request refused by the *network* never produces -- and asserts the second by
// the absence of the request.
//
// One probe cannot see every relaxation, and it is worth being exact about
// which one sees what, because the plan's red step for this task is the CSP
// with `connect-src 'self'` instead of `'none'`. `'self'` refuses the *remote*
// probe exactly as `'none'` does -- it is not this origin, so the request still
// dies in Blink with the same violation and still never reaches the route
// handler -- so the remote probe does not catch that mutation at all. The
// probe that does is the second one below, which asks for the console's own
// document and reads the **violation event**: under `'none'` the CSP refuses it
// and says so, under `'self'` the CSP permits it and stays silent. (The fetch
// fails either way -- `fetch()` does not support a custom scheme -- so the
// event, not the outcome, is what tells the two directives apart; the test says
// so where it is made.) Between them, the two probes are what hold the directive
// at `'none'`: the remote one catches a CSP that stops refusing remote
// requests (its record of aborted requests must stay empty), and the same-origin
// one catches `'self'`.
//
// Nothing here reaches the network even in principle: the probe's target is
// `example.invalid`, a name the IANA reserves precisely so that it never
// resolves, and every remote request the page makes is aborted by the route
// handler before it leaves the process.

import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { APP_ENTRY_URL, APP_ORIGIN } from '../../src/main/protocol'
import { PROJECT_MARKER } from '../../src/source/redact'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

// PROBE_ORIGIN is a name that cannot resolve (RFC 2606's `.invalid`), so a probe
// that somehow got past both gates still talks to nobody.
const PROBE_ORIGIN = 'https://example.invalid'

// The console's own directory, from this file's location: `test/e2e/x.spec.ts`
// is two levels below it. Playwright loads this spec as CommonJS (the package
// declares no `"type"`), so `__dirname` is the spec's directory on disk.
const consoleDir = join(__dirname, '..', '..')

/** ConsoleDocument is the console's own document, as the frame reports it.
 *
 * `heading` is the window's first `<h1>`, and since Task 3.4c that is **not** a
 * heading the document carries: the dashboard document's body is empty and the
 * entry fills it, so the window's first `<h1>` is the Live screen's project
 * cell, drawn from the feed it reads over IPC. It is still the witness this file
 * wants -- a navigation that committed replaces the document, and a refused
 * same-origin navigation to `other.html` commits a 404 with no `<h1>` at all --
 * and what it holds for this spec's stub is the project the replayed fixture
 * names, which is the recorder's `PROJECT_MARKER` (`test/fixtures/
 * synthetic.ndjson`'s own header says so). Hence the expected value below is
 * that constant rather than a project name. */
interface ConsoleDocument {
  href: string
  title: string
  heading: string | null
}

/**
 * consoleDocument reads the document the frame is showing, from inside the
 * frame.
 *
 * It is not `expect(page).toHaveTitle(...)`, and that is deliberate. A
 * navigation the renderer started and the main process then denied leaves
 * Playwright's view of the frame's title empty -- `page.title()` returns `''`
 * and `toHaveTitle` retries against that same string until it times out, on
 * Electron 44.7.2 with Playwright 1.64.1 -- while `page.evaluate` reads the
 * same frame's live document and finds the title and the `<h1>` intact. The
 * claim these assertions make is about the console's document, so they read it
 * where it is: in the page. `toHaveTitle` is still used for the initial load,
 * where no navigation has been denied.
 */
function consoleDocument(page: Page): Promise<ConsoleDocument> {
  return page.evaluate(() => ({
    href: window.location.href,
    title: document.title,
    heading: document.querySelector('h1')?.textContent ?? null,
  }))
}

test.describe('the hardened window', () => {
  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  // Every remote request the page makes, recorded by the second gate before it
  // aborts it. It must stay empty: a request that reached the route handler is a
  // request the CSP did not refuse.
  const remoteRequests: string[] = []

  test.beforeAll(async () => {
    // The app now reads a `fugaro` before it opens a window (plan Task 3.3), so
    // this spec has to hand it one, or the document it launches on is the
    // refusal one and every assertion below would be about the wrong page.
    cli = inRangeCliStub()
    // `args: [consoleDir]` is the app directory: Electron reads its package.json
    // and runs `main` (`dist/main/index.js`, built by `npm run test:e2e`).
    // A scratch profile with the stub as the active installation (Task 3.4b):
    // the dashboard is this spec's subject, so the first run's picker -- which
    // opens when no installation is chosen -- is not, and no spec touches the
    // machine's own console profile.
    cli.seedActiveInstallation()
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()

    // The second gate, installed before the page is used: no `http(s)` request
    // from this window can succeed, whatever the CSP does.
    await page.route(/^https?:\/\//, (route) => {
      remoteRequests.push(route.request().url())
      return route.abort()
    })
    await page.waitForLoadState('domcontentloaded')
  })

  test.afterAll(async () => {
    await app?.close()
    cli.remove()
  })

  test('serves the document over app://, and never over file://', async () => {
    expect(page.url()).toBe(APP_ENTRY_URL)
    expect(page.url().startsWith('file://')).toBe(false)
    expect(await page.evaluate(() => window.location.origin)).toBe(APP_ORIGIN)
    await expect(page).toHaveTitle('Fugaro console')
  })

  test('has no Node in the renderer (rule 1)', async () => {
    const globals = await page.evaluate(() => ({
      require: typeof (globalThis as { require?: unknown }).require,
      process: typeof (globalThis as { process?: unknown }).process,
      module: typeof (globalThis as { module?: unknown }).module,
      Buffer: typeof (globalThis as { Buffer?: unknown }).Buffer,
      electron: typeof (globalThis as { electron?: unknown }).electron,
    }))
    // Not "undefined because the page has not looked": this is the page's own
    // global object, read from the world the document's scripts run in.
    expect(globals).toEqual({
      require: 'undefined',
      process: 'undefined',
      module: 'undefined',
      Buffer: 'undefined',
      electron: 'undefined',
    })
  })

  test('refuses a renderer-initiated request to a remote origin, and the CSP is what refused it', async () => {
    const probe = await page.evaluate(async (target) => {
      const violations: string[] = []
      const onViolation = (event: Event) => {
        violations.push((event as SecurityPolicyViolationEvent).violatedDirective)
      }
      document.addEventListener('securitypolicyviolation', onViolation)

      let outcome = 'resolved'
      try {
        await fetch(target)
      } catch {
        outcome = 'rejected'
      }
      // The violation event is dispatched asynchronously; give it the tick it
      // needs before the listener comes off.
      await new Promise((resolve) => setTimeout(resolve, 100))
      document.removeEventListener('securitypolicyviolation', onViolation)
      return { outcome, violations }
    }, `${PROBE_ORIGIN}/console-security-probe`)

    expect(probe.outcome).toBe('rejected')
    // The violation is named, so the refusal is the CSP's and not the route
    // handler's: a request that reached the network and was aborted there
    // produces no `securitypolicyviolation` event at all. This is the assertion
    // that fails when the CSP stops refusing remote requests -- `connect-src *`,
    // or the directive gone -- and the next assertion fails with it.
    expect(probe.violations).toContain('connect-src')
    expect(
      remoteRequests,
      'a CSP-refused request must never reach the network stack. If this fails on a Chromium that reports blocked requests to Playwright, this assertion is what to revisit, not the CSP.',
    ).toEqual([])
  })

  test("refuses a request for its own document too: the directive is 'none', not 'self'", async () => {
    // This is the plan's red step for Task 1.1, and it is why this test stands
    // beside the one above rather than inside it: `connect-src 'self'` refuses
    // the *remote* probe exactly as `'none'` does, so that probe cannot see this
    // mutation. The console's own origin is where the two directives part
    // company, and the assertion that tells them apart is the **violation
    // event** -- the CSP's own evidence that it, and not something downstream,
    // refused the request:
    //
    //   - under `connect-src 'none'`, Blink refuses the fetch for
    //     `app://console/index.html` and dispatches a violation naming
    //     `connect-src`;
    //   - under `connect-src 'self'`, the CSP permits the request (it is this
    //     origin) and **no violation is dispatched**; the fetch still fails,
    //     because Chromium's `fetch()` does not support a custom scheme --
    //     observed as `TypeError: Failed to fetch` -- so the `outcome`
    //     assertion alone would pass under the mutation and this test would be
    //     worthless. Both halves were run: `'none'` passes here, `'self'` fails
    //     on the violation assertion.
    const probe = await page.evaluate(async (target) => {
      const violations: string[] = []
      const onViolation = (event: Event) => {
        violations.push((event as SecurityPolicyViolationEvent).violatedDirective)
      }
      document.addEventListener('securitypolicyviolation', onViolation)

      let outcome = 'resolved'
      try {
        await fetch(target)
      } catch {
        outcome = 'rejected'
      }
      await new Promise((resolve) => setTimeout(resolve, 100))
      document.removeEventListener('securitypolicyviolation', onViolation)
      return { outcome, violations }
    }, APP_ENTRY_URL)

    // No bytes of the console's own document arrive over the network layer,
    // whatever the directive says; and the refusal is the CSP's.
    expect(probe.outcome).toBe('rejected')
    expect(probe.violations).toContain('connect-src')
  })

  test('cannot read a file:// URL either', async () => {
    // Chromium's `fetch()` never supports `file:`, so "the fetch rejected" holds
    // under any CSP and proves nothing (review round 2 of PR 514). The probe is
    // the one its siblings use: the CSP's own violation event, which is
    // dispatched only when the policy -- not the scheme -- refused the request.
    // Both halves were run: under `connect-src 'none'` this passes; under
    // `connect-src * file:` the fetch still rejects but no violation is
    // dispatched, and the violation assertion fails. (`connect-src *` alone does
    // not admit `file:`, so it is not the mutation that shows this.)
    const probe = await page.evaluate(async (target) => {
      const violations: string[] = []
      const onViolation = (event: Event) => {
        violations.push((event as SecurityPolicyViolationEvent).violatedDirective)
      }
      document.addEventListener('securitypolicyviolation', onViolation)
      let outcome = 'resolved'
      try {
        await fetch(target)
      } catch {
        outcome = 'rejected'
      }
      await new Promise((resolve) => setTimeout(resolve, 100))
      document.removeEventListener('securitypolicyviolation', onViolation)
      return { outcome, violations }
    }, 'file:///etc/passwd')
    expect(probe.outcome).toBe('rejected')
    expect(probe.violations).toContain('connect-src')
    expect(remoteRequests).toEqual([])
  })

  test('refuses window.open, and no second window appears', async () => {
    const opened = await page.evaluate((target) => window.open(target, '_blank'), `${PROBE_ORIGIN}/console-window-probe`)
    expect(opened).toBeNull()
    expect(app.windows()).toHaveLength(1)
  })

  test('denies a will-navigate out of its own document', async () => {
    // Two attempts, and the first is the one that carries the assertion.
    //
    // `app://console/other.html` is same-origin and would commit if the handler
    // let it through -- a 404 from the protocol handler is still a document --
    // so a `will-navigate` that stopped denying moves the window there, where
    // the URL and the title below both fail. That makes the mutation visible
    // without depending on what a failed remote navigation leaves behind.
    await page.evaluate(() => {
      window.location.href = 'app://console/other.html'
    })
    await page.waitForTimeout(250)
    expect(page.url()).toBe(APP_ENTRY_URL)
    expect(await consoleDocument(page)).toEqual({
      href: APP_ENTRY_URL,
      title: 'Fugaro console',
      // The Live screen's own heading, which is the first `<h1>` in the window
      // since Task 3.4c emptied the dashboard document's body: the entry draws
      // it from the watch document, and the fixture this spec's stub replays
      // names its project `PROJECT_MARKER`. A navigation that committed would
      // have replaced the document and taken this heading with it.
      heading: PROJECT_MARKER,
    })

    // The remote attempt, which is what rule 2 is actually about. Whether an
    // aborted remote navigation commits an error page is Chromium's business;
    // what is asserted is that the console is still on its own document, and
    // that the route handler never saw a request to abort.
    await page.evaluate((target) => {
      window.location.href = target
    }, `${PROBE_ORIGIN}/console-navigate-probe`)
    await page.waitForTimeout(250)
    expect(page.url()).toBe(APP_ENTRY_URL)
    expect(await consoleDocument(page)).toEqual({
      href: APP_ENTRY_URL,
      title: 'Fugaro console',
      heading: PROJECT_MARKER,
    })
    expect(remoteRequests).toEqual([])
  })

  // The last test in the file, because it is the one that takes the shared
  // instance down to zero windows: the `page` the tests above use is gone after
  // it, and Playwright runs a file's tests in order inside a worker, so nothing
  // after it is left without one. That ordering -- and the one shared app and
  // page the whole file is written against, this `beforeAll` included -- holds
  // only because `fullyParallel` is false in playwright.config.ts; nothing in
  // this file can see the flag, so `test/unit/playwright.test.ts` is what holds
  // it at false.
  test('survives macOS activate: the window comes back, the scheme is not registered twice', async () => {
    // The premise, checked against the Electron this runs on rather than
    // assumed: registering the scheme a second time is an error, not a no-op.
    const secondRegistration = await app.evaluate(({ protocol }) => {
      try {
        protocol.handle('app', () => new Response('x'))
        return 'accepted'
      } catch (error) {
        return `refused: ${(error as Error).message}`
      }
    })
    expect(secondRegistration).toContain('Failed to register protocol')

    // Armed before `activate` is emitted, so the window it builds is the one
    // awaited rather than whichever `app.windows()` happens to list first.
    const rebuiltWindow = app.waitForEvent('window')

    // The sequence `activate` is: no windows, dock icon clicked. On macOS the
    // app is still running at that point; here `window-all-closed` would have
    // quit it, so the quit is held back for the length of the check.
    const windows = await app.evaluate(async ({ app: electronApp, BrowserWindow }) => {
      const quit = electronApp.quit
      electronApp.quit = () => {}
      for (const window of BrowserWindow.getAllWindows()) window.destroy()
      await new Promise((resolve) => setTimeout(resolve, 300))
      electronApp.emit('activate')
      await new Promise((resolve) => setTimeout(resolve, 1000))
      const open = BrowserWindow.getAllWindows().length
      electronApp.quit = quit
      return open
    })
    // One window, built by the `activate` handler. This is what the mutation
    // looks like here: with `main()` called from `activate` instead of the
    // window half alone, the second `protocol.handle` throws, `reportAndQuit`
    // runs, and the app exits with `app.exit(1)` -- so the evaluate above never
    // returns and Playwright reports it as "Execution context was destroyed,
    // most likely because of a navigation". Run both ways: `openConsoleWindow`
    // passes, `main` fails here.
    expect(windows).toBe(1)

    const rebuilt = await rebuiltWindow
    expect(await rebuilt.evaluate(() => window.location.href)).toBe(APP_ENTRY_URL)
  })
})
