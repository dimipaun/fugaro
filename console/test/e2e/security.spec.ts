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
// the absence of the request. A `connect-src 'self'` regression therefore fails
// this file twice over: the violation event never fires, and the route handler
// records the request it had to abort.
//
// Nothing here reaches the network even in principle: the probe's target is
// `example.invalid`, a name the IANA reserves precisely so that it never
// resolves, and every remote request the page makes is aborted by the route
// handler before it leaves the process.

import { join } from 'node:path'

import { _electron as electron, expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { APP_ENTRY_URL, APP_ORIGIN } from '../../src/main/protocol'

// PROBE_ORIGIN is a name that cannot resolve (RFC 2606's `.invalid`), so a probe
// that somehow got past both gates still talks to nobody.
const PROBE_ORIGIN = 'https://example.invalid'

// The console's own directory, from this file's location: `test/e2e/x.spec.ts`
// is two levels below it. Playwright loads this spec as CommonJS (the package
// declares no `"type"`), so `__dirname` is the spec's directory on disk.
const consoleDir = join(__dirname, '..', '..')

test.describe('the hardened window', () => {
  let app: ElectronApplication
  let page: Page

  // Every remote request the page makes, recorded by the second gate before it
  // aborts it. It must stay empty: a request that reached the route handler is a
  // request the CSP did not refuse.
  const remoteRequests: string[] = []

  test.beforeAll(async () => {
    // `args: [consoleDir]` is the app directory: Electron reads its package.json
    // and runs `main` (`dist/main/index.js`, built by `npm run test:e2e`).
    app = await electron.launch({ args: [consoleDir] })
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
    await app.close()
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
    // `connect-src 'none'`, by name: this is the assertion that fails when the
    // directive is relaxed to `'self'`, because the request would then reach the
    // route handler instead of being refused in Blink.
    expect(probe.violations).toContain('connect-src')
    expect(
      remoteRequests,
      'a CSP-refused request must never reach the network stack. If this fails on a Chromium that reports blocked requests to Playwright, this assertion is what to revisit, not the CSP.',
    ).toEqual([])
  })

  test('cannot read a file:// URL either', async () => {
    const outcome = await page.evaluate(async () => {
      try {
        await fetch('file:///etc/passwd')
        return 'resolved'
      } catch {
        return 'rejected'
      }
    })
    expect(outcome).toBe('rejected')
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
    await expect(page).toHaveTitle('Fugaro console')

    // The remote attempt, which is what rule 2 is actually about. Whether an
    // aborted remote navigation commits an error page is Chromium's business;
    // what is asserted is that the console is still on its own document, and
    // that the route handler never saw a request to abort.
    await page.evaluate((target) => {
      window.location.href = target
    }, `${PROBE_ORIGIN}/console-navigate-probe`)
    await page.waitForTimeout(250)
    expect(page.url()).toBe(APP_ENTRY_URL)
    await expect(page).toHaveTitle('Fugaro console')
    expect(remoteRequests).toEqual([])
  })
})
