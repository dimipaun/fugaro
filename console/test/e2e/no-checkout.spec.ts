// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Graph with an installation that records no checkout, in the real window
// (console 0.116.1).
//
// `plan status --json` is the console's only read with a precondition: it
// resolves the installation's `docs/` from the CLI's working directory, which is
// the **checkout**, so an installation that records none is refused before any
// child is spawned (`NO_PLAN_CHECKOUT`, `src/source/source.ts`). `test/support/
// cliStub.ts`'s `seedActiveCheckout` is what gives an installation one, and this
// spec is the one that does **not** call it: the state under test is the
// installation with none.
//
// What the window is here for, and what the unit suite cannot say:
//
//   1. **the raw refusal is off the screen.** The CLI's sentence is drawn behind
//      a `details` disclosure, and "one click away and never in the way" is a
//      claim about a rendered element: whether a closed disclosure's content is
//      painted is the browser's own answer, not the tree's.
//   2. **the card's one action opens the picker the console already ships.**
//      The button goes through `installation.open` -- the same door the rail's
//      `+` opens -- and the chooser it lands on is the picker's own row of
//      `Use <directory>` buttons, one per checkout the CLI listed. A window and
//      a real IPC round trip are the only way to see that: no new channel, no
//      new bridge method, and a spec that only read the tree could not tell the
//      difference between a wired button and a drawn one.
//   3. **switching tabs never leaves the Graph's answer on the panel.** The
//      defect this fix is for: `show` marked the tab and started its read, and
//      the panel kept the Graph's refusal under the newly marked tab until the
//      new read answered. Spend and Projects refuse here (the stub answers
//      neither `budget show` nor the report), Coordinator draws its own screen
//      -- and none of the three may hold `plan.status`'s refusal or its
//      sentence, in any state, at any point of the switch.
//
// The stub is `inRangeCliStub()`: the dashboard document, the committed
// `plan-status.json` the Graph would have drawn, and the committed
// `coordinator-status.json` the project view draws. Nothing here reaches the
// network or a real CLI.

import { realpathSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { isToolOwnedPath } from '../../src/main/installationstate'
import { PICKER_ENTRY_URL } from '../../src/main/picker'
import {
  NO_CHECKOUT_CHOICE_CLASS,
  NO_CHECKOUT_CHOICE_LABEL,
  NO_CHECKOUT_DETAILS_CLASS,
  NO_CHECKOUT_HEADLINE,
  NO_CHECKOUT_KIND,
} from '../../src/renderer/screens/nocheckout'
import { NO_PLAN_CHECKOUT } from '../../src/source/source'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

const consoleDir = join(__dirname, '..', '..')

/** PANEL is the element every view is drawn into, and the one the switch claims
 * are about. */
const PANEL = 'div.console-panel'

/** CARD is the calm card: the Graph's own state for an installation with no
 * checkout. `refused` is the kind the theme paints, and this is not it. */
const CARD = `${PANEL} section.console-unavailable[data-kind="${NO_CHECKOUT_KIND}"]`

/** REFUSED is the refusal screen -- the drawing the card replaced for this one
 * sentence, and the shape no other tab may keep. */
const REFUSED = `${PANEL} section.console-unavailable[data-kind="refused"]`

/** OPERATION is the read a refusal names, which is the cell that made the old
 * drawing wrong under a tab that asked for another read. */
const OPERATION = 'span.unavailable-operation'

/** NAV is one tab of the navigation. */
function nav(view: string): string {
  return `nav.console-views button.console-view[data-view="${view}"]`
}

/** CHECKOUT is the directory the CLI lists as this project's one checkout, and
 * the one the picker's chooser offers. It is **this repository's own directory**
 * and not a scratch one, and that is the point: the picker never offers a
 * temporary path (`isToolOwnedPath`, `src/main/installationstate.ts`, refuses
 * `/tmp` and the tool-owned roots), so a spec that seeded one would assert the
 * absence of a button rather than the button. It exists, because a checkout is a
 * directory. */
const CHECKOUT = consoleDir

/** CHECKOUT_TOOL_OWNED is whether the picker would refuse this repository's own
 * directory as a checkout. It is a fact about where the repository was cloned to
 * and not about the console: CI clones into a workspace directory, and a scratch
 * clone under the OS's temporary root is a directory the picker never offers
 * (`isToolOwnedPath`, asked about both spellings the picker asks about --
 * `src/main/picker.ts`). */
const CHECKOUT_TOOL_OWNED =
  isToolOwnedPath(CHECKOUT, tmpdir()) || isToolOwnedPath(realpathSync(CHECKOUT), tmpdir())

let cli: CliStub
let app: ElectronApplication
let page: Page

test.describe('the Graph with no installation checkout', () => {
  test.describe.configure({ mode: 'serial' })

  test.beforeAll(async () => {
    cli = inRangeCliStub()
    // The state under test: an installation with **no** checkout. Every other
    // dashboard spec calls `seedActiveCheckout`; this one is the absence.
    cli.seedActiveInstallation('e2e')
    cli.seedInstallationsList(['e2e'], [], {
      checkouts: [{ repository: 'github/acme/aurora', baseBranch: 'main', path: CHECKOUT, valid: true }],
    })
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
    await page.locator(nav('graph')).click()
    await expect(page.locator(CARD)).toHaveCount(1)
  })

  test.afterAll(async () => {
    await app?.close()
    cli?.remove()
  })

  test('draws the calm card where the raw refusal used to be', async () => {
    const card = page.locator(CARD)
    await expect(card.locator('h1.unavailable-headline')).toHaveText(NO_CHECKOUT_HEADLINE)
    await expect(card.locator(`button.${NO_CHECKOUT_CHOICE_CLASS}`)).toHaveText(NO_CHECKOUT_CHOICE_LABEL)
    // Not a refusal screen: no operation cell is drawn, and the frame carries
    // the read as an attribute instead -- which is what makes the card a state
    // the console can name rather than a read that failed.
    await expect(card.locator(OPERATION)).toHaveCount(0)
    await expect(card).toHaveAttribute('data-operation', 'plan.status')

    // The CLI's own sentence is one click away and never in the way. That a
    // *closed* disclosure paints nothing is the browser's answer, and the reason
    // this case is in a window at all.
    const details = card.locator(`details.${NO_CHECKOUT_DETAILS_CLASS}`)
    const raw = details.locator('p')
    await expect(raw).toHaveText(NO_PLAN_CHECKOUT)
    await expect(details.locator('summary')).toHaveText('details')
    expect(await details.evaluate((element) => element.hasAttribute('open'))).toBe(false)
    await expect(raw).toBeHidden()

    await details.locator('summary').click()
    await expect(raw).toBeVisible()
  })

  test('opens the installation picker and its chooser with the card’s one action', async () => {
    // The chooser is one `Use <dir>` button per checkout the CLI listed, and the
    // picker offers none for a path it refuses. A repository cloned under the
    // OS's temporary root therefore has no chooser button to find, and this case
    // -- which is about the chooser -- is skipped with that reason rather than
    // failing on the machine's clone path. The card's action itself is asserted
    // in the case above, which runs everywhere.
    test.skip(
      CHECKOUT_TOOL_OWNED,
      `this repository is checked out under a temporary root (${CHECKOUT}), and the picker never offers one as a checkout`,
    )
    const opened = app.waitForEvent('window')
    await page.locator(`${CARD} button.${NO_CHECKOUT_CHOICE_CLASS}`).click()
    const picker = await opened
    await picker.waitForLoadState('load')
    // The existing door, and the existing window: `installation.open`, the one
    // the rail's `+` opens.
    expect(picker.url()).toBe(PICKER_ENTRY_URL)
    // And the chooser itself: the picker draws one button per checkout the CLI
    // listed for the row, which is the control this card's button exists to
    // reach.
    const row = picker.locator('#picker-list li')
    await expect(row).toHaveCount(1)
    await expect(row.locator('button', { hasText: `Use ${CHECKOUT}` })).toHaveCount(1)
  })

  test('never leaves the Graph’s answer on the panel under another tab', async () => {
    // Each tab is waited for on its **own** drawing before anything is claimed
    // about what is not there: a negative assertion made while the Graph's card
    // is still up would be about the switch's timing rather than about what the
    // tab drew.
    await page.locator(nav('spend')).click()
    await expect(page.locator(REFUSED)).toHaveCount(1)
    await expect(page.locator(`${PANEL} ${OPERATION}`)).toHaveText('spend.report')
    await expect(page.locator(PANEL)).not.toContainText(NO_PLAN_CHECKOUT)

    await page.locator(nav('projects')).click()
    // Projects draws its own screen and says in it which reads refused (the stub
    // answers neither `budget show` nor the run list), rather than replacing the
    // panel with one read's refusal.
    await expect(page.locator(`${PANEL} section.projects`)).toHaveCount(1)
    await expect(page.locator(PANEL)).not.toContainText(NO_PLAN_CHECKOUT)
    await expect(page.locator(`${PANEL} ${OPERATION}`)).toHaveCount(0)

    await page.locator(nav('coordinator')).click()
    await expect(page.locator(`${PANEL} section.coordinator`)).toHaveCount(1)
    await expect(page.locator(`${PANEL} li.coordinator-record`).first()).toBeVisible()
    await expect(page.locator(PANEL)).not.toContainText(NO_PLAN_CHECKOUT)

    // Nothing in any of the three named the Graph's read: the operation cell is
    // the whole of what a refusal screen says about which read it came from, and
    // `plan.status` was never drawn under a tab that did not ask for it.
    await expect(page.locator(`${PANEL} ${OPERATION}`, { hasText: 'plan.status' })).toHaveCount(0)

    // Coming back draws the card again rather than the picture the stub holds:
    // the state is the installation's, and it has not changed.
    await page.locator(nav('graph')).click()
    await expect(page.locator(CARD)).toHaveCount(1)
    await expect(page.locator(`${PANEL} svg.graph-canvas`)).toHaveCount(0)
  })
})
