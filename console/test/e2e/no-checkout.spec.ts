// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Graph with an installation that records no checkout, in the real window
// (console 0.116.1; the chooser console 0.118.0 "Plum" added to it).
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
//   2. **the card's chooser is the control that stores the directory, and the
//      store is a file.** The menu is a real `<select>` -- the option the
//      operator picks is the string the button sends -- and the write goes
//      through `installation.checkout`, the picker's own operation, over real
//      IPC, through the main process's membership check and the pairing probe,
//      into `installations.json` in this launch's profile. A spec that only read
//      the tree could not tell a wired button from a drawn one, and could not
//      tell a directory that was *sent* from one that was merely displayed.
//   3. **the plan is read again, and it answers.** The card exists because the
//      plan read refused; the whole point of the chooser is that the next read
//      does not, so the canvas the stub's document draws is the end of the
//      round trip and the only proof that the stored checkout reached the read.
//   4. **switching tabs never leaves the Graph's answer on the panel.** The
//      defect the card was drawn for: `show` marked the tab and started its
//      read, and the panel kept the Graph's refusal under the newly marked tab
//      until the new read answered. Spend and Projects refuse here (the stub
//      answers neither `budget show` nor the report), Coordinator draws its own
//      screen -- and none of the three may hold `plan.status`'s refusal or its
//      sentence, in any state, at any point of the switch.
//
// The stub is `inRangeCliStub()`: the dashboard document, the committed
// `plan-status.json` the Graph draws, and the committed
// `coordinator-status.json` the project view draws. Nothing here reaches the
// network or a real CLI.

import { mkdtempSync, readFileSync, realpathSync, rmSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { INSTALLATIONS_FILE_NAME, type InstallationFile } from '../../src/main/installations'
import { isToolOwnedPath } from '../../src/main/installationstate'
import { CHECKOUT_SELECT_CLASS, checkoutLabel } from '../../src/renderer/checkout'
import {
  NO_CHECKOUT_AUTO_SENTENCE,
  NO_CHECKOUT_CHOICE_CLASS,
  NO_CHECKOUT_CHOICE_LABEL,
  NO_CHECKOUT_DETAILS_CLASS,
  NO_CHECKOUT_EMPTY_SENTENCE,
  NO_CHECKOUT_HEADLINE,
  NO_CHECKOUT_KIND,
  NO_CHECKOUT_LEAD_CLASS,
  NO_CHECKOUT_SENTENCE_CLASS,
  NO_CHECKOUT_USE_LABEL,
} from '../../src/renderer/screens/nocheckout'
import { NO_PLAN_CHECKOUT } from '../../src/source/source'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub, SeededCheckout } from '../support/cliStub'
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

/** CANVAS is the graph the stub's `plan status --json` document draws: the
 * answer the chooser exists to make possible. */
const CANVAS = 'svg.graph-canvas'

/** SELECT is the card's menu, and USE its button. */
const SELECT = `select.${CHECKOUT_SELECT_CLASS}`
const USE = `button.${NO_CHECKOUT_CHOICE_CLASS}`

/** NAV is one tab of the navigation. */
function nav(view: string): string {
  return `nav.console-views button.console-view[data-view="${view}"]`
}

/**
 * checkoutFixture makes a directory the CLI can list as a checkout: a real
 * directory -- the console `stat`s it before it stores it, and the CLI is asked
 * to prove the pairing from inside it -- made **outside** every temporary root,
 * because the console offers no path under one (`suggestedCheckouts`,
 * `src/main/installationstate.ts`) and this spec is about the paths it does
 * offer.
 *
 * It is beside the operator's home directory and removed again, rather than the
 * repository's own directory: a checkout a tool made -- a run's own, an agent
 * harness's worktree -- is one the console refuses, and there the card draws its
 * empty state, so a spec that used one would be asserting about the machine
 * rather than about the console.
 */
function checkoutFixture(): string {
  return mkdtempSync(join(homedir(), 'fugaro-console-checkout-'))
}

/** toolOwned is whether this run's fixtures are directories the console refuses,
 * which is a fact about the machine and not about the console: it is true only
 * where the home directory itself is under the OS's temporary root, and this
 * spec can make its fixtures nowhere else. */
function toolOwned(paths: readonly string[]): boolean {
  return paths.some((path) => isToolOwnedPath(path, tmpdir()) || isToolOwnedPath(realpathSync(path), tmpdir()))
}

/** readInstallations is this launch's own `installations.json`, as the main
 * process wrote it: the file the console's store keeps, and the only place a
 * spec can see which directory a chooser actually sent. */
async function readInstallations(app: ElectronApplication): Promise<InstallationFile> {
  const path = join(await app.evaluate(({ app: electronApp }) => electronApp.getPath('userData')), INSTALLATIONS_FILE_NAME)
  return JSON.parse(readFileSync(path, 'utf8')) as InstallationFile
}

/** textWhenItAppears answers the text of the first element matching `selector`,
 * read in the page the moment it is there.
 *
 * The auto-chosen card is replaced as soon as the plan read answers, and that is
 * the point of the case that reads it: a locator re-resolved after the round trip
 * would find the canvas instead, so the claim about what the card said is made in
 * the page, on the first turn of the event loop the card exists for. */
function textWhenItAppears(page: Page, selector: string): Promise<string> {
  return page.evaluate(
    (target) =>
      new Promise<string>((resolve) => {
        const look = (): void => {
          const node = document.querySelector(target)
          if (node !== null) {
            resolve(node.textContent ?? '')
            return
          }
          setTimeout(look, 0)
        }
        look()
      }),
    selector,
  )
}

/** storedCheckout is the directory this launch's profile records for the active
 * installation, or `undefined` when it records none. */
async function storedCheckout(app: ElectronApplication): Promise<string | undefined> {
  const file = await readInstallations(app)
  return file.installations[0]?.checkout
}

/** openGraph starts the console on the stub's profile, opens the Graph and
 * answers the window. */
async function openGraph(cli: CliStub): Promise<{ app: ElectronApplication; page: Page }> {
  const app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
  const page = await app.firstWindow()
  await page.waitForLoadState('domcontentloaded')
  await page.locator(nav('graph')).click()
  return { app, page }
}

let first = ''
let second = ''

test.beforeAll(() => {
  first = checkoutFixture()
  second = checkoutFixture()
})

test.afterAll(() => {
  rmSync(first, { recursive: true, force: true })
  rmSync(second, { recursive: true, force: true })
})

/** twoCheckouts is two repositories of one installation, both of them
 * re-validated and enrolled by the CLI, in the order the CLI prints them. The
 * menu draws one entry per repository in that order, so the second entry is the
 * one a spec can pick to prove it is not being handed the first.
 *
 * It is a call and not a constant because the fixtures are made in the file's
 * own `beforeAll`, and a constant would be built before they are. */
function twoCheckouts(): readonly SeededCheckout[] {
  return [
    { repository: 'github/owner/repo-a', baseBranch: 'main', path: first, valid: true },
    { repository: 'github/owner/repo-b', baseBranch: 'main', path: second, valid: true },
  ]
}

test.describe('the Graph with no installation checkout', () => {
  test.describe.configure({ mode: 'serial' })

  let cli: CliStub
  let app: ElectronApplication
  let page: Page

  test.beforeAll(async () => {
    cli = inRangeCliStub()
    // The state under test: an installation with **no** checkout. Every other
    // dashboard spec calls `seedActiveCheckout`; this one is the absence. The
    // fixtures above are a different thing: they are what the CLI *lists* for
    // the row, which is the chooser's menu and not the directory a read runs in.
    cli.seedActiveInstallation('e2e')
    cli.seedInstallationsList(['e2e'], [], { checkouts: twoCheckouts() })
    const opened = await openGraph(cli)
    app = opened.app
    page = opened.page
    await expect(page.locator(CARD)).toHaveCount(1)
  })

  test.afterAll(async () => {
    await app?.close()
    cli?.remove()
  })

  test('draws the calm card where the raw refusal used to be', async () => {
    const card = page.locator(CARD)
    await expect(card.locator('h1.unavailable-headline')).toHaveText(NO_CHECKOUT_HEADLINE)
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

  test('offers the installation’s repositories as a menu, and nothing when the console refuses them', async () => {
    // The action is drawn for an installation the CLI listed a choosable
    // checkout for, and the case below is the one that drives it. On the one
    // machine whose home directory is itself a temporary root there is nothing
    // this spec can list that the console would offer, and the card is the
    // empty one -- a branch and not a skip, because everything else here holds
    // there too.
    const card = page.locator(CARD)
    if (toolOwned([first, second])) {
      await expect(card.locator(SELECT)).toHaveCount(0)
      await expect(card.locator(USE)).toHaveCount(0)
      await expect(card).toContainText(NO_CHECKOUT_EMPTY_SENTENCE)
      return
    }
    await expect(card.locator(`p.${NO_CHECKOUT_LEAD_CLASS}`)).toHaveText(NO_CHECKOUT_CHOICE_LABEL)
    const select = card.locator(SELECT)
    await expect(select.locator('option')).toHaveCount(2)
    // One entry per repository, in the CLI's order, labelled with the repository
    // and the short directory -- and the option's **value** is the path itself,
    // which is the string the button sends and the main process matches.
    await expect(select.locator('option').nth(0)).toHaveText(
      checkoutLabel({ repository: 'github/owner/repo-a', path: first, more: 0 }),
    )
    await expect(select.locator('option').nth(1)).toHaveText(
      checkoutLabel({ repository: 'github/owner/repo-b', path: second, more: 0 }),
    )
    await expect(select.locator('option').nth(1)).toHaveAttribute('value', second)
    await expect(card.locator(USE)).toHaveText(NO_CHECKOUT_USE_LABEL)
  })

  test('never leaves the Graph’s answer on the panel under another tab', async () => {
    // Each tab is waited for on its **own** drawing before anything is claimed
    // about what is not there: a negative assertion made while the Graph's card
    // is still up would be about the switch's timing rather than about what the
    // tab drew.
    await page.locator(nav('spend')).click()
    // **Two** refusals, one per section: the stub answers neither `budget show`
    // nor the report, and each of the tab's two reads draws its own refusal into
    // its own section -- the caps' beside the report's, not one refusal standing
    // for the whole panel.
    await expect(page.locator(REFUSED)).toHaveCount(2)
    await expect(page.locator(`${PANEL} ${OPERATION}`, { hasText: 'budget.show' })).toHaveCount(1)
    await expect(page.locator(`${PANEL} ${OPERATION}`, { hasText: 'spend.report' })).toHaveCount(1)
    await expect(page.locator(PANEL)).not.toContainText(NO_PLAN_CHECKOUT)

    await page.locator(nav('projects')).click()
    // Projects makes one read now, `budget show` (the jobs list is gone with its
    // `runs ls`), and the stub does not answer it: the screen draws that one
    // refusal inside its own section, rather than replacing the panel with it.
    await expect(page.locator(`${PANEL} section.projects`)).toHaveCount(1)
    await expect(page.locator(PANEL)).not.toContainText(NO_PLAN_CHECKOUT)
    await expect(page.locator(`${PANEL} ${OPERATION}`)).toHaveCount(1)
    await expect(page.locator(`${PANEL} ${OPERATION}`, { hasText: 'budget.show' })).toHaveCount(1)

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
    await expect(page.locator(`${PANEL} ${CANVAS}`)).toHaveCount(0)
  })
})

test.describe('the Graph’s checkout chooser, in the real window', () => {
  test.describe.configure({ mode: 'serial' })

  let cli: CliStub
  let app: ElectronApplication
  let page: Page

  test.beforeAll(async () => {
    cli = inRangeCliStub()
    cli.seedActiveInstallation('e2e')
    cli.seedInstallationsList(['e2e'], [], { checkouts: twoCheckouts() })
    const opened = await openGraph(cli)
    app = opened.app
    page = opened.page
    await expect(page.locator(CARD)).toHaveCount(1)
  })

  test.afterAll(async () => {
    await app?.close()
    cli?.remove()
  })

  test('stores the directory the operator picked, and the plan renders from it', async () => {
    // The state is the chooser's own: an installation with several usable
    // checkouts, so the card draws the menu rather than choosing for the
    // operator. On the one machine whose home directory is itself a temporary
    // root there is no such fixture for this spec to make, and the card is the
    // empty one -- the case above asserts that branch either way.
    test.skip(
      toolOwned([first, second]),
      `this machine's home directory is under a temporary root (${first}), and the console never offers one as a checkout`,
    )
    // Nothing is recorded before the operator picks: this is the state the card
    // is drawn for, read off the profile rather than off the screen.
    expect(await storedCheckout(app)).toBeUndefined()

    const select = page.locator(`${CARD} ${SELECT}`)
    await expect(select.locator('option')).toHaveCount(2)
    // The **second** repository, so what is stored below cannot be the menu's
    // first entry arriving on its own.
    await select.selectOption(second)
    await page.locator(`${CARD} ${USE}`).click()

    // The write went through the real operation, over real IPC, past the main
    // process's membership check and the CLI's own pairing probe, and landed in
    // the profile: the directory the operator selected, and not another.
    await expect.poll(() => storedCheckout(app)).toBe(second)

    // And the read the card was standing in for now answers: the canvas is the
    // stub's `plan status --json` document laid out by the real worker.
    await page.locator(CANVAS).waitFor()
    await expect(page.locator(CARD)).toHaveCount(0)
  })
})

test.describe('the Graph’s chooser when the CLI lists no usable checkout', () => {
  test.describe.configure({ mode: 'serial' })

  let cli: CliStub
  let app: ElectronApplication
  let page: Page

  test.beforeAll(async () => {
    cli = inRangeCliStub()
    cli.seedActiveInstallation('e2e')
    // A row the CLI's own re-validation refused (`reason: 'missing'`: the
    // directory is not there any more). It is a checkout this installation
    // remembers and is **not** one the console may offer, so the card has
    // nothing to draw a menu from and says what records one instead.
    cli.seedInstallationsList(['e2e'], [], {
      checkouts: [
        { repository: 'github/owner/repo-a', baseBranch: 'main', path: join(homedir(), 'fugaro-console-gone'), valid: false },
      ],
    })
    const opened = await openGraph(cli)
    app = opened.app
    page = opened.page
    await expect(page.locator(CARD)).toHaveCount(1)
  })

  test.afterAll(async () => {
    await app?.close()
    cli?.remove()
  })

  test('says what records one, and offers no control that would lead nowhere', async () => {
    const card = page.locator(CARD)
    await expect(card.locator(`p.${NO_CHECKOUT_SENTENCE_CLASS}`).first()).toHaveText(NO_CHECKOUT_EMPTY_SENTENCE)
    await expect(card.locator(SELECT)).toHaveCount(0)
    await expect(card.locator(USE)).toHaveCount(0)
    // The CLI's sentence is still there, behind the same disclosure: the card is
    // the console's account of the state, and it does not replace the CLI's.
    await expect(card.locator(`details.${NO_CHECKOUT_DETAILS_CLASS} p`)).toHaveText(NO_PLAN_CHECKOUT)
    // Nothing was stored: the card's sentence is the whole of what happened.
    expect(await storedCheckout(app)).toBeUndefined()
  })
})

test.describe('the Graph’s chooser with exactly one checkout', () => {
  test.describe.configure({ mode: 'serial' })

  let cli: CliStub
  let app: ElectronApplication
  let page: Page

  test.beforeAll(async () => {
    cli = inRangeCliStub()
    cli.seedActiveInstallation('e2e')
    cli.seedInstallationsList(['e2e'], [], {
      checkouts: [{ repository: 'github/owner/repo-a', baseBranch: 'main', path: first, valid: true }],
    })
    // The Graph is **not** opened here: this case reads the card as it is drawn,
    // so the reading has to already be watching when the tab is clicked.
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
  })

  test.afterAll(async () => {
    await app?.close()
    cli?.remove()
  })

  test('chooses the one checkout by itself, says so, and reads the plan', async () => {
    test.skip(
      toolOwned([first]),
      `this machine's home directory is under a temporary root (${first}), and the console never offers one as a checkout`,
    )
    // A menu with one entry is a control with nothing to choose, so the card
    // does not draw one: it names the directory the Graph is reading, and there
    // is no button to press. It is read the moment it is there, because the
    // console's own write takes it off the screen again within the round trip.
    const said = textWhenItAppears(page, CARD)
    await page.locator(nav('graph')).click()
    expect(await said).toContain(
      `${NO_CHECKOUT_AUTO_SENTENCE} ${checkoutLabel({ repository: 'github/owner/repo-a', path: first, more: 0 })}`,
    )
    expect(await said).not.toContain(NO_CHECKOUT_USE_LABEL)

    // The console sent it without being asked to, and the read answered.
    await expect.poll(() => storedCheckout(app)).toBe(first)
    await page.locator(CANVAS).waitFor()
    await expect(page.locator(CARD)).toHaveCount(0)
  })
})
