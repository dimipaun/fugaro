// The layout worker in a real Electron process, under the console's unchanged
// CSP (GV2.2 in `docs/console-dependency-graph/implementation-plan.md`).
//
// Two claims live here and nowhere else. `test/unit/bundle.test.ts` holds the
// served root to `SERVED_FILES` and the copied worker to the pinned package's
// bytes; `test/unit/layout-client.test.ts` holds the protocol. Neither can say
// that a **browser** starts the worker from `app://console/elk-worker.min.js`
// -- that is `script-src 'self'` doing its job with no `worker-src` directive
// -- or that the CSP still refuses `new Function` *inside the worker's own
// realm*. That second one is the point of the task: the served file is
// minified third-party code, and the single relaxation that would make it
// dangerous is eval, so the directive is asserted where the code would use it.

import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page, Worker as PageWorker } from '@playwright/test'

import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

// `test/e2e/x.spec.ts` is two levels below the console's own directory.
const consoleDir = join(__dirname, '..', '..')

/** GRAPH is a two-node fixture: ids and sizes, never text. */
const GRAPH = {
  id: 'root',
  layoutOptions: { 'elk.algorithm': 'layered' },
  children: [
    { id: 'a', width: 100, height: 40 },
    { id: 'b', width: 100, height: 40 },
  ],
  edges: [{ id: 'a->b', sources: ['a'], targets: ['b'] }],
}

interface Placed {
  readonly id: string
  readonly x: number
  readonly y: number
  readonly width: number
  readonly height: number
}

test.describe('the layout worker', () => {
  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // A scratch profile with the stub as the active installation, exactly as
    // security.spec.ts does: without one the window opens on the picker, and
    // the served root is then a different document's root.
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
  })

  test.afterAll(async () => {
    await app?.close()
    cli.remove()
  })

  /** started makes a worker in the page and returns Playwright's handle to it,
   * so a later case can run code inside the worker's own realm. */
  async function started(): Promise<PageWorker> {
    const seen = page.waitForEvent('worker')
    await page.evaluate(() => {
      ;(globalThis as { probe?: Worker }).probe = new Worker('elk-worker.min.js')
    })
    return seen
  }

  test('runs the pinned worker from the served root, and places a fixture', async () => {
    const worker = await started()
    expect(worker.url()).toContain('elk-worker.min.js')
    const placed = await page.evaluate(
      (graph) =>
        new Promise<Placed>((resolve, reject) => {
          const probe = (globalThis as { probe?: Worker }).probe
          if (probe === undefined) {
            reject(new Error('the page made no worker to probe'))
            return
          }
          probe.onerror = (event) => reject(new Error(`the worker failed: ${String(event.message)}`))
          probe.onmessage = (event: MessageEvent) => {
            const reply = event.data as {
              id: number
              error?: string
              data?: { children?: Placed[] }
            }
            if (reply.id === 1) {
              probe.postMessage({ id: 2, cmd: 'layout', graph, layoutOptions: {}, options: {} })
              return
            }
            if (reply.error !== undefined) {
              reject(new Error(`the worker refused the graph: ${reply.error}`))
              return
            }
            const child = reply.data?.children?.[0]
            if (child === undefined) {
              reject(new Error('the worker placed no node'))
              return
            }
            resolve(child)
          }
          probe.postMessage({ id: 1, cmd: 'register', algorithms: ['layered'] })
        }),
      GRAPH,
    )
    // Two nodes, one under the other, each its own size: the algorithm ran. A
    // worker that failed to start leaves this promise pending and the case
    // times out, which is the failure a missing `SERVED_FILES` entry produces.
    expect(placed.id).toBe('a')
    expect(Number.isFinite(placed.x) && Number.isFinite(placed.y)).toBe(true)
    expect([placed.width, placed.height]).toEqual([100, 40])
  })

  test('refuses new Function inside the worker (no unsafe-eval)', async () => {
    const worker = await started()
    const refused = await worker.evaluate(() => {
      try {
        new Function('return 1')()
        return false
      } catch {
        return true
      }
    })
    expect(refused, 'the CSP permits eval inside the worker, which is the one relaxation C5 forbids').toBe(true)
  })
})
