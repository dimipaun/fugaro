// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The installation picker, in a real Electron window (desktop-console plan Task
// 3.4b, steps 5 and 6; owner ruling C9; the owner's ruling of 2026-10-20).
//
// The unit suite drives the picker's handlers against fakes
// (`test/unit/installations-ipc.test.ts`) and the view against a document
// (`test/unit/installations-view.test.ts`); what only a running app can show is
// that the real menu opens the real document, that each document's world holds
// its own bridge and not the other's, and that the refusals hold over real IPC
// through the real preload.
//
// Every launch runs against a scratch `--user-data-dir`. The picker's rows are
// the CLI's own list now (owner's ruling of 2026-10-20): the stub answers
// `config list --json` with the document `seedInstallationsList` wrote, and the
// store's records are merged into it -- so this profile has
//
//   - `e2e`, listed and chosen (the active installation, so the dashboard is
//     what the app opens on);
//   - `acme`, listed with a config the CLI could not read: a row the picker must
//     draw *with* the CLI's warning line rather than hide;
//   - `old`, a record the CLI did not list, whose stored binary reports 0.5.0 --
//     below this console's floor, so choosing it is refused.
//
// Nothing here depends on which `fugaro` the machine has installed: the active
// record's binary is the stub, and rule 4 puts a configured path first.
//
// The **first run** is the one state with no record to point rule 4 at, and it
// has a case of its own at the end of this file: an unseeded profile, the stub's
// own empty `config list --json` document, and the "set up your first
// installation" screen -- the console asking the operator for no binary and no
// config, which is the headline of the change this file belongs to. That case is
// the only one here that can skip, and it skips for exactly one reason: an
// unseeded profile resolves through the well-known prefixes, so a machine with
// its own `fugaro` in `/usr/local/bin` is not a machine this stub can answer for.

import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { NO_ACTIVE_INSTALLATION } from '../../src/main/ipc'
import { INSTALLATIONS_MENU_LABEL, PICKER_ENTRY_URL } from '../../src/main/picker'
import { FUGARO_SETUP_COMMAND } from '../../src/main/onboarding'
import { APP_ENTRY_URL } from '../../src/main/protocol'
import { ONBOARDING_COPY } from '../../src/renderer/installations'
import { CLI_VERSION_BUILT_AGAINST } from '../../src/shared/version'
import { installCliStub, resolveConsoleBinary } from '../support/cliStub'
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
    // The document the CLI prints: one ordinary installation, and one whose
    // config could not be read (the `unreadable:` prefix makes its GCP project
    // empty and adds the CLI's own warning line).
    stub.seedInstallationsList(['e2e', 'unreadable:acme'])
    // The console's own records: the active one -- which is also the binary rule
    // 4 resolves first, so this profile's CLI is the stub and never the
    // machine's -- and a legacy entry naming a refused CLI.
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

  test('opens from the menu, lists what the CLI printed, refuses a refused CLI and a directory the CLI never listed, and keeps each bridge to its own document', async () => {
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

      // The form is gone (owner's ruling of 2026-10-20): there is a list and no
      // way to add an installation by hand, which is the whole point of the
      // screen -- the console asks the operator for no binary and no config.
      await expect(picker.locator('#picker-list')).toHaveCount(1)
      await expect(picker.locator('#picker-project')).toHaveCount(0)
      await expect(picker.locator('#picker-add-submit')).toHaveCount(0)
      await expect(picker.locator('#picker-pick-binary')).toHaveCount(0)
      // The three rows, in the CLI's order and then this console's own leftovers:
      // the two it listed, and the record it did not (`listed: false`).
      const listed = await call(picker, 'list')
      expect(listed.ok).toBe(true)
      const rows = (
        listed.data as {
          rows: { project: string; gcpProject: string; region: string; listed: boolean; saved: boolean; active: boolean; configUnreadable: boolean }[]
        }
      ).rows
      expect(rows.map((row) => [row.project, row.listed, row.active, row.configUnreadable])).toEqual([
        ['e2e', true, true, false],
        ['acme', true, false, true],
        ['old', false, false, false],
      ])
      await expect(picker.locator('#picker-list li')).toHaveCount(3)
      // The row whose config could not be read is drawn **with the CLI's own
      // warning**, never hidden: the picker shows what the CLI refused to read,
      // in the CLI's words.
      await expect(picker.locator('#picker-warnings li')).toHaveCount(1)
      await expect(picker.locator('#picker-warnings')).toContainText('acme')

      // Choosing the 0.5.0 installation is refused, and changes nothing: the
      // active installation, the dashboard and the set of windows stay as they
      // were. The record's stored binary is the *optional override* rule 4 tries
      // first, so this is the legacy-store path as well as the floor check.
      const windowsBefore = await windowUrls(app)
      const chosen = await call(picker, 'choose', { project: 'old' })
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

      // A directory the CLI never listed is refused by name, however well-formed
      // it is: the console offers only what the CLI itself printed for that
      // installation (rule 7's third prohibition, at the surface).
      const invented = await call(picker, 'checkout', { project: 'acme', checkout: stub.dir })
      expect(invented.ok).toBe(false)
      expect(invented.reason).toContain('not one of the checkouts the CLI listed')

      // The link row takes **no arguments at all**: `GETTING_STARTED_URL` is the
      // main process's own constant, so a frame that sends an address has sent a
      // key no row declares -- and it is refused before any handler runs. (The
      // accepted call is not exercised here: it would ask the operating system to
      // open a browser.)
      const named = await call(picker, 'openExternal', { url: 'https://fugaro.dev/getting-started/' })
      expect(named).toEqual({ ok: false, reason: 'unknown argument: url' })

      // And the row for a project the CLI did not list is still openable by name:
      // no migration, and the console's own record is not thrown away because a
      // newer CLI cannot enumerate it. `acme` is the listed one here, so the
      // assertion is about the *list* rather than about a choose -- a choose
      // would open a dashboard and close this window under the test.
      const relisted = await call(picker, 'list')
      const relistedRows = (relisted.data as { rows: { project: string; listed: boolean }[] }).rows
      expect(relistedRows.map((row) => [row.project, row.listed])).toEqual([
        ['e2e', true],
        ['acme', true],
        ['old', false],
      ])
    } finally {
      await app.close()
    }
  })
})

test.describe('the first run, in the real window', () => {
  test('opens no picker by itself; the rail’s + opens it on the empty list, which asks for no binary and no config', async () => {
    // **The headline state of this console** (owner's ruling of 2026-10-20): a
    // machine with no installation at all. Nothing is seeded here -- no
    // `installations.json` in the profile, and no `installations.ndjson` either,
    // so the CLI answers `config list --json` with the empty document a real
    // `config list --json` prints when it found nothing
    // (`{"installations":[],"warnings":[]}`; `test/fixtures/bin/fugaro`). That
    // document is the only thing that draws the "set up your first installation"
    // screen: a CLI that printed *nothing* is text the console cannot parse, and
    // `picker.ts` draws the warning line instead.
    const fresh = installCliStub(CLI_VERSION_BUILT_AGAINST)
    try {
      // An unseeded profile has no stored override, so rule 4 walks the
      // well-known prefixes and this *machine's* `fugaro` can win -- and the
      // screen under test is the one the stub's empty document draws. So the
      // spec runs only where the stub is what the app resolved, asked of the
      // console's own resolver with this launch's own environment (the
      // `resolveConsoleBinary` idiom `startup-skew.spec.ts` uses). It is not an
      // assertion about the machine: it says which `fugaro` answered.
      const winner = await resolveConsoleBinary(fresh.env)
      const resolved = winner.ok ? winner.path : ''
      const ours = resolved === join(fresh.dir, 'fugaro') || resolved === join(fresh.home, '.local', 'bin', 'fugaro')
      test.skip(!ours, `this console resolved ${resolved === '' ? 'no fugaro' : resolved}, not the stub`)

      const app = await launchConsole({ args: fresh.launchArgs(consoleDir), env: fresh.env })
      try {
        // The owner's design ruling of 2026-10-25: the console never shows a
        // picker screen by itself. The dashboard is the one window, its rail has
        // no row, and the rail's + is the door to the picker.
        const dashboard = await app.firstWindow()
        await dashboard.waitForLoadState('load')
        expect(dashboard.url()).toBe(APP_ENTRY_URL)
        expect(app.windows()).toHaveLength(1)
        await expect(dashboard.locator('aside.console-rail button.console-rail-row')).toHaveCount(0)
        const opened = app.waitForEvent('window')
        await dashboard.locator('button.console-switcher-button').click()
        await dashboard.locator('aside.console-rail button.console-rail-add').click()
        const picker = await opened
        await picker.waitForLoadState('load')
        expect(picker.url()).toBe(PICKER_ENTRY_URL)
        // The console's own window, and its line: nothing has been chosen, and
        // the console says so rather than guessing (D13).
        const installation = await dashboard.evaluate(async () => {
          const api = (window as unknown as { fugaro: { getInstallation: () => Promise<unknown> } }).fugaro
          return await api.getInstallation()
        })
        expect(installation).toEqual({ ok: true, data: { chosen: false, sentence: NO_ACTIVE_INSTALLATION } })

        // The screen, as the document has it: the title, the one sentence of
        // what is missing, the one command -- the main process's own constant,
        // never a string retyped here -- and the one link, which is a button.
        const screen = picker.locator('#picker-onboarding')
        await expect(screen.locator('h2')).toHaveText('Set up your first installation')
        await expect(screen.locator('p').first()).toHaveText(
          'The console found the Fugaro CLI, and it has no installation to show. Create one with:',
        )
        await expect(screen.locator('code')).toHaveText(FUGARO_SETUP_COMMAND)
        await expect(screen.locator('button')).toHaveText([ONBOARDING_COPY['first-installation'].link])
        // And nothing else is drawn: no row (the machine has none, and this
        // profile holds no record either), no warning line (a refusal would have
        // put one here instead of this screen), and no status sentence.
        await expect(picker.locator('#picker-list li')).toHaveCount(0)
        await expect(picker.locator('#picker-warnings li')).toHaveCount(0)
        await expect(picker.locator('#picker-status')).toHaveText('')

        // The same answer over real IPC: the main process's own reply, which is
        // what the reading above is a drawing of.
        const listed = await call(picker, 'list')
        expect(listed.ok).toBe(true)
        const data = listed.data as { onboard: { kind: string; command: string }; rows: readonly unknown[] }
        expect(data.onboard).toEqual({ kind: 'first-installation', command: FUGARO_SETUP_COMMAND })
        expect(data.rows).toEqual([])
      } finally {
        await app.close()
      }
    } finally {
      fresh.remove()
    }
  })
})
