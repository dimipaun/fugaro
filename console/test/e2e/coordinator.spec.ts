// The project view in a real window (Bitbucket credentials T1.2, D364; the
// styling work's per-screen forced-colors smoke, design section 6.4).
//
// `coordinator.test.ts` holds the decoder and the screen against
// `test/support/fakeDom.ts`, and `entry-wiring.test.ts` the wiring. This file is
// the one place either of them meets a **browser**: the real CSP, the real entry
// reading `coordinator status --from-store --all --json` from the stub CLI, and
// the real stylesheet applied to the elements the screen draws. Two claims live
// here and nowhere else:
//
//   1. **the console's own words are in the DOM**, and they are what the
//      stylesheet's tones are redundant beside. `state unread`, `draft`,
//      `in flight` and `no run behind it` are `literal`s the screen writes, so a
//      unit case can read them off a fake tree -- but section 4.4 rule 1 is a
//      claim about a reader who cannot see the tones, and the rule that paints
//      those tones is in `screens.css`, which no unit case applies.
//   2. **the rows keep their boundary when the author's colours are gone.** Every
//      record, collision and unread row is separated by a `--rule-width` hairline
//      in `--border` (design section 3.8), and a table drawn in hairlines alone
//      becomes structureless the moment the author's colours go away -- which is
//      what section 4.4 rule 2 is for. This screen adds **no arm** to the
//      forced-colors block: its rows' boundary is a border of its own, and the
//      claim is that the system's own pass leaves it there. The emulation is
//      Chromium's, so this is the system's forced-colors pass and not a
//      stylesheet pretending.
//
// **The project view's document is the stub's `coordinator status` answer.** The
// stub beside this suite answers that read (`test/fixtures/bin/fugaro`,
// `test/support/cliStub.ts`) with the committed
// `test/fixtures/coordinator-status.json`, and `test/unit/cli-stub.test.ts`
// holds that document to the console's own decoder -- so the view in this window
// is a document the console reads rather than a shape invented in a spec. It
// carries every word the first case reads: a draft pull request, a record in
// flight with a run that has not finished, a record with no run behind it, and a
// pull request whose host state no sweep could read.

import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

// `test/e2e/x.spec.ts` is two levels below the console's own directory.
const consoleDir = join(__dirname, '..', '..')

/** NAV_COORDINATOR is the navigation's own door to this view. */
const NAV_COORDINATOR = 'nav.console-views button.console-view[data-view="coordinator"]'

/** SCREEN is the project view's root: drawn once the read's document has been
 * decoded, so its arrival is the whole chain -- the stub's document, the entry's
 * read and the screen -- rather than a screen with nothing in it. */
const SCREEN = 'section.coordinator'

/** RECORD is one record's row: the element whose hairline is the boundary the
 * forced-colors case is about. */
const RECORD = 'li.coordinator-record'

/**
 * rowBoundary is one element's computed bottom border: the browser's own answer
 * to what the stylesheet asked for, and the thing section 4.4 rule 2 promises
 * survives forced colors.
 */
async function rowBoundary(page: Page, selector: string): Promise<{ readonly width: string; readonly style: string }> {
  return page.locator(selector).first().evaluate((element) => {
    const computed = getComputedStyle(element)
    return { width: computed.borderBottomWidth, style: computed.borderBottomStyle }
  })
}

test.describe('the project view in a window', () => {
  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // A scratch profile with the stub as the active installation, as the other
    // dashboard specs do: without one the window opens on the picker and there
    // is no view to open. The document needs no seeding of its own -- the stub
    // carries the committed one -- and no checkout either: this read names no
    // directory (unlike `plan status --json`), because the stored view is a
    // database read for every repository the installation's config lists.
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
    await page.locator(NAV_COORDINATOR).click()
    await page.locator(SCREEN).waitFor()
    await page.locator(RECORD).first().waitFor()
  })

  test.afterAll(async () => {
    await app?.close()
    cli.remove()
  })

  test('draws the console’s own words beside every tone the stylesheet paints', async () => {
    // A word and not a tone: section 3.5's never-colour-alone rule, and the
    // reason this screen adds no arm to the forced-colors block at all. Each
    // word is the console's own `literal`, so what this case reads is what the
    // screen wrote and not a class the stylesheet invented.
    const words = await page.locator(`${SCREEN} span`).allTextContents()
    expect(words).toContain('draft')
    expect(words).toContain('in flight')
    expect(words).toContain('no run behind it')
    expect(words).toContain('state unread')

    // And each one is in the element the stylesheet's tone is written on, so the
    // tone is redundant beside it rather than the other way round.
    for (const [hook, word] of [
      ['coordinator-record-draft', 'draft'],
      ['coordinator-record-noruns', 'no run behind it'],
      ['coordinator-unread-word', 'state unread'],
    ] as const) {
      await expect(page.locator(`span.${hook}`).first()).toHaveText(word)
    }
    // `in flight` is two readings and one word: the record's own state, and the
    // run that has not finished. The document carries one of each.
    await expect(page.locator('span.coordinator-record-flight').first()).toHaveText('in flight')
    await expect(page.locator('span.coordinator-run-open').first()).toHaveText('in flight')
  })

  test('keeps the rows’ boundary when forced colors is active', async () => {
    // The authored boundary first, so the case below is about what forced colors
    // does to it rather than about a row that never had one.
    const authored = await rowBoundary(page, RECORD)
    expect(authored.style).not.toBe('none')
    expect(authored.width).not.toBe('0px')

    await page.emulateMedia({ forcedColors: 'active' })
    try {
      // Section 4.4 rule 2: a panel keeps a boundary. This screen writes no arm
      // for its rows, so what is asserted is the system's own pass -- the
      // hairline is still a hairline of a non-zero width once every colour this
      // file names is gone.
      const forced = await rowBoundary(page, RECORD)
      expect(forced.style).not.toBe('none')
      expect(forced.width).not.toBe('0px')
      // And the words are still there beside it: the two halves of the smoke are
      // the same claim -- the structure survives, and what the structure says is
      // still readable.
      await expect(page.locator('span.coordinator-record-draft').first()).toHaveText('draft')
      await expect(page.locator('span.coordinator-unread-word').first()).toHaveText('state unread')
    } finally {
      await page.emulateMedia({ forcedColors: 'none' })
    }
  })
})
