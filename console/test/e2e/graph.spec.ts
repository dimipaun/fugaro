// The Graph view in a real window (GV2.3b; the owner's dependency graph spec §3
// and its addendum §3, `docs/console-dependency-graph/`).
//
// `graph.test.ts` holds the screen and `entry-wiring.test.ts` the wiring, both
// against `test/support/fakeDom.ts`. This file is the one place either of them
// meets a **browser**: the real CSP, the real layout worker
// (`app://console/elk-worker.min.js`, GV2.2), the real entry reading
// `plan status --json` from the stub CLI, and the keyboard a person presses.
// Four claims live here and nowhere else:
//
//   1. **the state words are drawn, and a blocked card's second channel is a
//      dash.** The unit suite reads the tree the screen builds; whether the
//      stylesheet's dash reaches a real `stroke-dasharray` is a claim about a
//      rendered element, and only a browser computes one.
//   2. **a chip expands on a click and on nothing else** (the addendum:
//      "expand only when clicked"): the panel is drawn for the click, and not
//      for the focus or the hover that a menu-ish control would open on.
//   3. **`forced-colors: active` keeps the dash and the 1/2/3 px edge scale** --
//      design section 4.4's rule 1 for this screen, whose information is a
//      *stroke* rather than a colour. The emulation is Chromium's own, so this
//      is the system's forced-colors pass and not a stylesheet pretending.
//   4. **the keyboard walks the graph.** The tablist's roving `tabindex` is a
//      claim about the *tab order*, which a fake DOM cannot have: Tab has to
//      reach the tablist, the arrows have to move the tab, and a redraw has to
//      put the focus back in the tree it drew rather than on `<body>`.
//
// **The graph's document is the stub's `plan status --json` answer.** The stub
// beside this suite answers that read (`test/fixtures/bin/fugaro`,
// `test/support/cliStub.ts`) with the committed `test/fixtures/plan-status.json`
// -- the same document `graph.test.ts` draws its screen from -- so the graph in
// this window is the graph the unit cases are written against, and nothing here
// is a shape invented in a spec.
//
// **One flag is seeded differently, and this is why.** A **chip** exists only for
// a *finished* release (the addendum: the chips are the finished releases), and
// the committed document has none -- the unit suite patches the same flag
// (`FINISHED` in `graph.test.ts`). The one case about a chip **launches its own
// window** over a copy with a release marked finished, rather than changing the
// document every other case reads: a finished release's tasks are the ones the
// canvas leaves out until its chip is open, and the committed document's
// critical path and its one blocked card are both inside the two releases it
// has, so finishing either of them would take an edge or a state away from a
// case that is about them.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

// `test/e2e/x.spec.ts` is two levels below the console's own directory.
const consoleDir = join(__dirname, '..', '..')

/** PLAN_FIXTURE is the committed `plan status --json` document: the stub's own
 * answer, and what the unit suite draws its screen from. */
const PLAN_FIXTURE = join(consoleDir, 'test', 'fixtures', 'plan-status.json')

/** NAV_GRAPH is the navigation's own door to this view. */
const NAV_GRAPH = 'nav.console-views button.console-view[data-view="graph"]'

/** CANVAS is the picture: the `svg` the layout worker's answer is drawn into,
 * and the element whose absence means the screen fell back to the list (which is
 * what a worker that answered nothing leaves). */
const CANVAS = 'svg.graph-canvas'

/**
 * withFinishedRelease is the committed document with its first release marked
 * finished -- the one state a chip is drawn for, and the one the committed
 * document does not carry (see the header).
 */
function withFinishedRelease(): unknown {
  const document = JSON.parse(readFileSync(PLAN_FIXTURE, 'utf8')) as {
    plans: { releases: { finished: boolean }[] }[]
  }
  const first = document.plans[0]?.releases[0]
  if (first === undefined) throw new Error('the committed plan document has no release to finish')
  first.finished = true
  return document
}

/** strokeDash is one element's computed `stroke-dasharray`: the browser's own
 * answer to what the stylesheet asked for. */
async function strokeDash(page: Page, selector: string): Promise<string> {
  return page.locator(selector).first().evaluate((element) => getComputedStyle(element).strokeDasharray)
}

/** active is the focused element's attributes, read inside the page -- the
 * keyboard's own observable, which no locator can report. */
async function active(
  page: Page,
): Promise<{ readonly classes: readonly string[]; readonly attributes: Readonly<Record<string, string>> }> {
  return page.evaluate(() => {
    const element = document.activeElement
    const attributes: Record<string, string> = {}
    for (const name of ['data-tab', 'data-key', 'data-selected', 'aria-selected']) {
      const value = element?.getAttribute(name)
      if (value !== null && value !== undefined) attributes[name] = value
    }
    return {
      classes: [...(element?.classList ?? [])],
      attributes,
    }
  })
}

/**
 * focusedOn is the attributes of the focused element **when it is one of
 * `className`**, and `null` otherwise -- `<body>` included.
 *
 * Every poll in this file goes through it, and that is the whole of why they are
 * not flaky: a redraw that drops the keyboard leaves `document.activeElement` on
 * `<body>`, whose attributes are all absent, so a poll written as "the attribute
 * changed" (`'' !== 'stream:example'`) passes on exactly the failure it is meant
 * to catch. A poll that can only pass on an element of the right class cannot.
 */
async function focusedOn(
  page: Page,
  className: string,
): Promise<{ readonly classes: readonly string[]; readonly attributes: Readonly<Record<string, string>> } | null> {
  const now = await active(page)
  return now.classes.includes(className) ? now : null
}

/** pressTabUntil walks the tab order from wherever the keyboard is until the
 * focused element carries `className`, and answers whether it got there. It is
 * how a claim about the *tab order* is made: a roving `tabindex` that put every
 * tab in the order, or none of them, fails here rather than passing on a
 * `focus()` the spec called itself. */
async function pressTabUntil(page: Page, className: string, presses = 80): Promise<boolean> {
  for (let taken = 0; taken < presses; taken += 1) {
    await page.keyboard.press('Tab')
    const reached = await page.evaluate(
      (name) => document.activeElement?.classList.contains(name) === true,
      className,
    )
    if (reached) return true
  }
  return false
}

test.describe('the graph view in a window', () => {
  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // A scratch profile with the stub as the active installation, as the other
    // dashboard specs do: without one the window opens on the picker and there
    // is no view to open. The plan document needs no seeding of its own -- the
    // stub carries the committed one, which is the document every case here but
    // the chip's reads.
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    // And a checkout, which is the other half of that read: `plan.status` is
    // about a directory -- the CLI resolves `docs/` from its working directory
    // -- so an installation that records none is refused before any child runs
    // (`NO_PLAN_CHECKOUT` in `src/source/source.ts`), and the screen would draw
    // that refusal where this spec is waiting for a canvas.
    cli.seedActiveCheckout()
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
    await page.locator(NAV_GRAPH).click()
    // The canvas is the worker's answer drawn: the screen falls back to the list
    // when there is none, so this is the whole chain -- the stub's document, the
    // entry's read, the screen's model and ELK's layout -- arriving.
    await page.locator(CANVAS).waitFor()
  })

  test.afterAll(async () => {
    await app?.close()
    cli.remove()
  })

  test('draws the state words, and marks a blocked card with a dash as well as a tone', async () => {
    const words = await page.locator(`${CANVAS} [data-word]`).allTextContents()
    // The committed document carries one task of each of these, and a word is
    // what the design makes the state (§3.5): a reader who cannot tell the tones
    // apart still reads the state.
    expect(words).toContain('done')
    expect(words).toContain('blocked')
    expect(words).toContain('ready')

    // The blocked card's boundary is dashed, which is the second channel that
    // survives `forced-colors` -- and it is a *computed* `stroke-dasharray`, so
    // this fails if the rule never reaches a rendered element.
    expect(await strokeDash(page, 'g.graph-node[data-state="blocked"] .graph-node-box')).not.toBe('none')
    // A card in a state with no second channel is not dashed, so the assertion
    // above is about the blocked rule and not about every box on the canvas.
    expect(await strokeDash(page, 'g.graph-node[data-state="ready"] .graph-node-box')).toBe('none')
  })

  test('keeps the dash and the edge scale when forced colors is active', async () => {
    // A task is selected first: the selection is a width as well as a tone
    // (`svg .graph-node[data-selected='true'] .graph-node-box`), and a window with
    // nothing selected has no such element at all -- a `null` read there would
    // make the assertion below pass whatever the stylesheet says.
    await page.locator('g.graph-node').first().click()
    await page.locator('g.graph-node[data-selected="true"]').first().waitFor()
    await page.emulateMedia({ forcedColors: 'active' })
    try {
      // The tones are the system's here and the shapes are not: the blocked
      // card is still dashed, and the critical path is still wider than a plain
      // edge. That is design section 4.4's rule 1 for this screen, whose whole
      // distinction between one edge and another is a stroke.
      expect(await strokeDash(page, 'g.graph-node[data-state="blocked"] .graph-node-box')).not.toBe('none')
      expect(await strokeDash(page, 'g.graph-node[data-state="ready"] .graph-node-box')).toBe('none')

      const widths = await page.evaluate(() => {
        const width = (selector: string): string | null => {
          const element = document.querySelector(selector)
          return element === null ? null : getComputedStyle(element).strokeWidth
        }
        return {
          critical: width('g.graph-edge[data-critical="true"] .graph-edge-path'),
          // An edge that is neither critical nor one of the selected task's, so
          // the width read here is the plain one rather than the selection's.
          plain: width('g.graph-edge[data-critical="false"][data-selected="false"] .graph-edge-path'),
          selected: width('g.graph-node[data-selected="true"] .graph-node-box'),
        }
      })
      expect(widths.critical).toBe('3px')
      expect(widths.plain).toBe('2px')
      // The selection is a width too, and the tone it is drawn in is the
      // reading's -- which forced colors takes away, leaving the thickness.
      expect(widths.selected).toBe('2px')
    } finally {
      await page.emulateMedia({ forcedColors: 'none' })
    }
  })

  test('walks the graph from the keyboard, and the focus comes back after a redraw', async () => {
    // The tablist's roving `tabindex` is a claim about the tab order, so this
    // walks it: one tab of the graph's tablist is in the order, and Tab reaches
    // it from wherever the keyboard is.
    expect(await pressTabUntil(page, 'graph-tab'), 'the tab order never reached the graph’s tablist').toBe(true)
    const first = await active(page)
    expect(first.classes).toContain('graph-tab')
    expect(first.attributes['aria-selected']).toBe('true')
    const firstTab = first.attributes['data-tab'] ?? ''
    expect(firstTab, 'the first tab carries no data-tab: the polls below would be vacuous').not.toBe('')

    // Right moves to the next tab and **takes the focus with it**, and the tab
    // it lands on is the one the screen drew as selected: a tab is a different
    // graph, so the redraw lands a worker's answer later. The poll answers a
    // value only while a **focused** tab other than the first is showing, so the
    // old tab's `aria-selected` (which is still `true` until the redraw lands)
    // and `<body>` both keep it waiting rather than passing it.
    await page.keyboard.press('ArrowRight')
    await expect
      .poll(async () => {
        const now = await focusedOn(page, 'graph-tab')
        const tab = now?.attributes['data-tab'] ?? ''
        return tab !== '' && tab !== firstTab ? tab : ''
      })
      .not.toBe('')
    const moved = await active(page)
    expect(moved.classes).toContain('graph-tab')
    expect(moved.attributes['aria-selected']).toBe('true')
    expect(moved.attributes['data-tab'] ?? '').not.toBe(firstTab)

    // And a redraw puts the focus back in the tree it drew. A click selects the
    // task -- which replaces the picture -- and the arrow key then moves the
    // selection, which replaces it again: without the focus restore the reader
    // is on `<body>` after the first of the two and never gets to the second.
    const node = page.locator('g.graph-node').first()
    const key = (await node.getAttribute('data-key')) ?? ''
    expect(key, 'the node carries no data-key: the polls below would be vacuous').not.toBe('')
    await node.click()
    await expect
      .poll(async () => {
        const now = await focusedOn(page, 'graph-node')
        const at = now?.attributes['data-key'] ?? ''
        return at === key ? at : ''
      })
      .toBe(key)
    const selected = await active(page)
    expect(selected.classes).toContain('graph-node')
    expect(selected.attributes['data-selected']).toBe('true')

    await page.keyboard.press('ArrowDown')
    await expect
      .poll(async () => {
        const now = await focusedOn(page, 'graph-node')
        const at = now?.attributes['data-key'] ?? ''
        return at !== '' && at !== key ? at : ''
      })
      .not.toBe('')
    const next = await active(page)
    expect(next.classes).toContain('graph-node')
    expect(next.attributes['data-selected']).toBe('true')
    expect(next.attributes['data-key'] ?? '').not.toBe(key)
  })
})

test.describe('the graph view’s chips', () => {
  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // Its own window, over its own copy of the document: a chip is drawn for a
    // *finished* release and the committed document has none (the header says
    // why this is a second launch rather than a flag flipped under the cases
    // above).
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    cli.seedActiveCheckout()
    cli.seedPlan(withFinishedRelease())
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
    await page.locator(NAV_GRAPH).click()
    await page.locator(CANVAS).waitFor()
  })

  test.afterAll(async () => {
    await app?.close()
    cli.remove()
  })

  test('expands a finished release on a click, and on nothing else', async () => {
    const chip = page.locator('button.graph-chip').first()
    await expect(chip).toHaveAttribute('aria-expanded', 'false')
    expect(await page.locator('div.graph-chip-body').count()).toBe(0)

    // Not on the focus, and not on the hover: the addendum's gesture is the
    // click, and a chip that opened on either would be opening on something no
    // reader asked for.
    await chip.focus()
    await expect(chip).toHaveAttribute('aria-expanded', 'false')
    expect(await page.locator('div.graph-chip-body').count()).toBe(0)
    await chip.hover()
    await expect(chip).toHaveAttribute('aria-expanded', 'false')
    expect(await page.locator('div.graph-chip-body').count()).toBe(0)

    await chip.click()
    await expect(chip).toHaveAttribute('aria-expanded', 'true')
    // The body is the release's tasks, and it is the one place a finished
    // release's tasks are drawn: the canvas leaves them out until the chip is
    // open.
    expect(await page.locator('div.graph-chip-body').count()).toBe(1)
    expect(await page.locator('div.graph-chip-body li').count()).toBeGreaterThan(0)

    await chip.click()
    await expect(chip).toHaveAttribute('aria-expanded', 'false')
    expect(await page.locator('div.graph-chip-body').count()).toBe(0)
  })
})
