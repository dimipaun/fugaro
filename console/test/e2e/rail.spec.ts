// The installation rail and its issue view, in the real window (owner's design
// ruling of 2026-10-25): the console never shows a picker screen; every
// installation the CLI lists is a row in the rail, readable or not; a row the
// console cannot read carries a warning mark and opens its issue in the main
// area -- one plain sentence first, the CLI's own text behind a disclosure.
//
// The projects are invented (`aurora`, `orbit`), and the CLI is the stub
// (`test/fixtures/bin/fugaro`): a seeded override names it, so rule 4 resolves
// it first whatever the machine has installed.

import { join } from 'node:path'

import { expect, test } from '@playwright/test'

import { PICKER_ENTRY_URL } from '../../src/main/picker'
import { APP_ENTRY_URL } from '../../src/main/protocol'
import { CLI_VERSION_BUILT_AGAINST } from '../../src/shared/version'
import { installCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

const consoleDir = join(__dirname, '..', '..')

const AURORA_ID = '0123456789abcdef'
const ORBIT_ID = 'fedcba9876543210'

/** ORBIT_LINE is the stub's own warning line for a config it could not read
 * (`seedInstallationsList`'s `unreadable:` prefix). */
const ORBIT_LINE = '/Users/owner/.config/fugaro/projects/orbit.yaml could not be read: permission denied'

test.describe('the installation rail, in the real window', () => {
  let stub: CliStub

  test.afterEach(() => {
    stub?.remove()
  })

  test('lists every installation, marks the unreadable one, and opens its issue in the main area', async () => {
    stub = installCliStub(CLI_VERSION_BUILT_AGAINST)
    stub.seedInstallationsList(['aurora', 'unreadable:orbit'])
    stub.seedInstallations([{ id: AURORA_ID, project: 'aurora', binaryPath: join(stub.dir, 'fugaro') }], AURORA_ID)
    const app = await launchConsole({ args: stub.launchArgs(consoleDir), env: stub.env })
    try {
      const page = await app.firstWindow()
      await page.waitForLoadState('domcontentloaded')
      expect(page.url()).toBe(APP_ENTRY_URL)

      const rail = page.locator('aside.console-rail')
      const rows = rail.locator('button.console-rail-row')
      await expect(rows).toHaveCount(2)
      await expect(rail.locator('span.console-rail-name')).toHaveText(['aurora', 'orbit'])
      // The readable one is open and selected; the other is idle.
      await expect(rows.nth(0)).toHaveAttribute('aria-selected', 'true')
      await expect(rows.nth(1).locator('span.console-rail-state')).toHaveAttribute('data-state', 'idle')

      // The mark: a glyph with an accessible name, on the unreadable row alone.
      await expect(rows.nth(0).locator('span.console-rail-warning')).toHaveCount(0)
      const mark = rows.nth(1).locator('span.console-rail-warning')
      await expect(mark).toHaveText('⚠')
      await expect(mark).toHaveAttribute('role', 'img')
      await expect(page.getByRole('img', { name: /^warning: edit .*orbit\.yaml: permission denied/ })).toHaveCount(1)

      // No picker window opened by itself: the dashboard is the only window.
      expect(app.windows()).toHaveLength(1)

      await rows.nth(1).click()
      const issue = page.locator('div.console-panel section.installation-issue')
      await expect(issue).toHaveCount(1)
      await expect(issue.locator('h1.installation-issue-headline')).toHaveText('orbit')
      await expect(issue.locator('p.installation-issue-sentence')).toHaveText(
        'edit /Users/owner/.config/fugaro/projects/orbit.yaml: permission denied. Then run fugaro doctor --project orbit.',
      )
      await expect(issue.locator('code.installation-issue-command')).toHaveText('fugaro doctor --project orbit')
      // The CLI's own text is behind the disclosure, closed until asked for.
      const details = issue.locator('details.installation-issue-details')
      await expect(details.locator('pre.installation-issue-lines')).toBeHidden()
      await details.locator('summary.installation-issue-summary').click()
      await expect(details.locator('pre.installation-issue-lines')).toHaveText(ORBIT_LINE)
      // No banner: the issue is the main area, and nothing is drawn above it.
      await expect(page.locator('section.console-skew')).toHaveCount(0)
      await expect(rows.nth(1)).toHaveAttribute('aria-selected', 'true')

      // The nav leads back to the dashboard.
      await page.locator('nav.console-views button[data-view="live"]').click()
      await expect(issue).toHaveCount(0)
      await expect(rows.nth(0)).toHaveAttribute('aria-selected', 'true')

      // The + row opens the picker window, which is the one way to add one.
      const opened = app.waitForEvent('window')
      await rail.locator('button.console-rail-add').click()
      const picker = await opened
      await picker.waitForLoadState('domcontentloaded')
      expect(picker.url()).toBe(PICKER_ENTRY_URL)
    } finally {
      await app.close()
    }
  })

  test('opens on the issue when no installation can be read, keeps the remembered choice, and reads nothing', async () => {
    stub = installCliStub(CLI_VERSION_BUILT_AGAINST)
    stub.seedInstallationsList(['unreadable:orbit'])
    stub.seedInstallations([{ id: ORBIT_ID, project: 'orbit', binaryPath: join(stub.dir, 'fugaro') }], ORBIT_ID)
    const app = await launchConsole({ args: stub.launchArgs(consoleDir), env: stub.env })
    try {
      const page = await app.firstWindow()
      await page.waitForLoadState('domcontentloaded')
      expect(page.url()).toBe(APP_ENTRY_URL)
      const issue = page.locator('div.console-panel section.installation-issue')
      await expect(issue).toHaveCount(1)
      await expect(issue.locator('h1.installation-issue-headline')).toHaveText('orbit')
      // The remembered row is saved, so the issue view can remove it.
      await expect(issue.locator('button.installation-issue-remove')).toHaveText('Remove from this console')
      await expect(page.locator('aside.console-rail span.console-rail-warning')).toHaveCount(1)
      // Nothing was read for a console that cannot read its installation: the
      // Live screen was never drawn.
      await expect(page.locator('div.console-panel section.live')).toHaveCount(0)
    } finally {
      await app.close()
    }
  })
})
