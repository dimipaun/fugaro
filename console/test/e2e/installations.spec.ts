// The installation picker, in a real Electron window (desktop-console plan Task
// 3.4b, steps 5 and 6; owner ruling C9; review S3).
//
// The unit suite drives the picker's handlers against fakes
// (`test/unit/installations-ipc.test.ts`); what only a running app can show is
// that the real menu opens the real document, that each document's world holds
// its own bridge and not the other's, and that the refusals hold over real IPC
// through the real preload.
//
// Every launch runs against a scratch `--user-data-dir` seeded with two
// installations, each naming a stub of its own: the active one in range, and a
// second one at 0.5.0, below the floor. The startup handshake reads the active
// installation's binary and nothing else, so nothing here depends on which
// `fugaro` the machine has installed.

import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { INSTALLATIONS_MENU_LABEL, PICKER_ENTRY_URL } from '../../src/main/picker'
import { APP_ENTRY_URL } from '../../src/main/protocol'
import { CLI_VERSION_BUILT_AGAINST } from '../../src/shared/version'
import { installCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

const consoleDir = join(__dirname, '..', '..')

const ACTIVE_ID = '0123456789abcdef'
const REFUSED_ID = 'fedcba9876543210'

/** openPickerFromMenu clicks `Installations…` in the application menu -- the
 * one door to the picker (C9) -- and answers the picker's page. */
async function openPickerFromMenu(app: ElectronApplication): Promise<Page> {
  const opened = app.waitForEvent('window')
  const clicked = await app.evaluate(({ Menu }, label) => {
    for (const top of Menu.getApplicationMenu()?.items ?? []) {
      for (const item of top.submenu?.items ?? []) {
        if (item.label === label) {
          item.click()
          return true
        }
      }
    }
    return false
  }, INSTALLATIONS_MENU_LABEL)
  expect(clicked, `the application menu has no ${INSTALLATIONS_MENU_LABEL} item`).toBe(true)
  const picker = await opened
  await picker.waitForLoadState('load')
  return picker
}

/** call invokes one method of the picker's bridge from inside its page. */
function call(page: Page, method: string, args?: Record<string, string>): Promise<{ ok: boolean; reason?: string; data?: unknown }> {
  return page.evaluate(
    async ({ method, args }) => {
      const api = (window as unknown as { fugaroInstallations: Record<string, (args?: unknown) => Promise<unknown>> })
        .fugaroInstallations
      const target = api[method]
      if (target === undefined) throw new Error(`no ${method} on the picker's bridge`)
      return (await target(args)) as { ok: boolean; reason?: string; data?: unknown }
    },
    { method, args },
  )
}

/** windowUrls is every open window's document, in the main process's view. */
function windowUrls(app: ElectronApplication): Promise<string[]> {
  return app.evaluate(({ BrowserWindow }) =>
    BrowserWindow.getAllWindows()
      .map((window) => window.webContents.getURL())
      .sort(),
  )
}

let stub: CliStub
let refusedBinary: string

test.describe('the installation picker, in the real window', () => {
  test.describe.configure({ mode: 'serial' })

  test.beforeAll(() => {
    stub = installCliStub(CLI_VERSION_BUILT_AGAINST)
    refusedBinary = stub.extraStub('old', '0.5.0')
    stub.seedInstallations(
      [
        { id: ACTIVE_ID, project: 'e2e', binaryPath: join(stub.dir, 'fugaro') },
        { id: REFUSED_ID, project: 'old', binaryPath: refusedBinary },
      ],
      ACTIVE_ID,
    )
  })

  test.afterAll(() => {
    stub?.remove()
  })

  test('opens from the menu, lists the store, refuses a literal path and a refused CLI, and keeps each bridge to its own document', async () => {
    const app = await launchConsole({ args: stub.launchArgs(consoleDir), env: stub.env })
    try {
      const dashboard = await app.firstWindow()
      await dashboard.waitForLoadState('domcontentloaded')
      expect(dashboard.url()).toBe(APP_ENTRY_URL)
      // An active installation: no first-run picker, one window.
      expect(app.windows()).toHaveLength(1)

      // The dashboard's world has its own bridge and not the picker's.
      const dashboardBridges = await dashboard.evaluate(() => ({
        fugaro: typeof (window as unknown as Record<string, unknown>)['fugaro'],
        fugaroInstallations: typeof (window as unknown as Record<string, unknown>)['fugaroInstallations'],
      }))
      expect(dashboardBridges).toEqual({ fugaro: 'object', fugaroInstallations: 'undefined' })
      const before = await dashboard.evaluate(async () => {
        const api = (window as unknown as { fugaro: { getInstallation: () => Promise<unknown> } }).fugaro
        return await api.getInstallation()
      })
      expect(before).toMatchObject({ ok: true, data: { chosen: true, id: ACTIVE_ID, project: 'e2e', mode: 'ok' } })

      const picker = await openPickerFromMenu(app)
      expect(picker.url()).toBe(PICKER_ENTRY_URL)
      // ...and the picker's world has its own and not the dashboard's.
      const pickerBridges = await picker.evaluate(() => ({
        fugaro: typeof (window as unknown as Record<string, unknown>)['fugaro'],
        fugaroInstallations: typeof (window as unknown as Record<string, unknown>)['fugaroInstallations'],
      }))
      expect(pickerBridges).toEqual({ fugaro: 'undefined', fugaroInstallations: 'object' })

      // `list`: both installations, the active one marked, the other unprobed.
      const listed = await call(picker, 'list')
      expect(listed.ok).toBe(true)
      const rows = (listed.data as { rows: { id: string; project: string; binary: string; active: boolean; mode: string }[] }).rows
      expect(rows.map((row) => [row.id, row.project, row.binary, row.active, row.mode])).toEqual([
        [ACTIVE_ID, 'e2e', join(stub.dir, 'fugaro'), true, 'ok'],
        [REFUSED_ID, 'old', refusedBinary, false, 'unknown'],
      ])
      // The page drew them.
      await expect(picker.locator('#picker-list li')).toHaveCount(2)

      // A literal path, however well-formed, is not a token this console issued.
      const literal = await call(picker, 'add', { project: 'sneaky', binary: refusedBinary })
      expect(literal).toEqual({ ok: false, reason: 'binary is not a path this console offered' })

      // Choosing the 0.5.0 installation is refused, and changes nothing: the
      // active installation, the dashboard and the set of windows stay as they
      // were.
      const windowsBefore = await windowUrls(app)
      const chosen = await call(picker, 'choose', { id: REFUSED_ID })
      expect(chosen.ok).toBe(false)
      expect(chosen.reason).toContain('0.5.0')
      expect(await windowUrls(app)).toEqual(windowsBefore)
      expect(dashboard.isClosed()).toBe(false)
      expect(dashboard.url()).toBe(APP_ENTRY_URL)
      const after = await dashboard.evaluate(async () => {
        const api = (window as unknown as { fugaro: { getInstallation: () => Promise<unknown> } }).fugaro
        return await api.getInstallation()
      })
      expect(after).toMatchObject({ ok: true, data: { chosen: true, id: ACTIVE_ID } })
      const relisted = await call(picker, 'list')
      const relistedRows = (relisted.data as { rows: { id: string; active: boolean; mode: string }[] }).rows
      expect(relistedRows.map((row) => [row.id, row.active])).toEqual([
        [ACTIVE_ID, true],
        [REFUSED_ID, false],
      ])
    } finally {
      await app.close()
    }
  })
})
