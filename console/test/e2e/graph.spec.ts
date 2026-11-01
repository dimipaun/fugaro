// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Graph view in a real window (GV2.3b; the owner's dependency graph spec §3
// and its addendum §3, `docs/console-dependency-graph/`).
//
// `graph.test.ts` holds the screen and `entry-wiring.test.ts` the wiring, both
// against `test/support/fakeDom.ts`. This file is the one place either of them
// meets a **browser**: the real CSP, the real layout worker
// (`app://console/elk-worker.min.js`, GV2.2), the real entry reading
// `plan status --json` from the stub CLI, and the keyboard a person presses.
// Seven claims live here and nowhere else:
//
//   1. **the state words are drawn, and a blocked card's second channel is a
//      dash.** The unit suite reads the tree the screen builds; whether the
//      stylesheet's dash reaches a real `stroke-dasharray` is a claim about a
//      rendered element, and only a browser computes one.
//   2. **a chip expands on a click and on nothing else** (the addendum:
//      "expand only when clicked"): the panel is drawn for the click, and not
//      for the focus or the hover that a menu-ish control would open on.
//   3. **`forced-colors: active` keeps the dash and the edge scale** -- design
//      section 4.4's rule 1 for this screen, whose information is a *stroke*
//      rather than a colour. The scale is what is left of the selection in this
//      mode, where the tone that says the same thing is gone: the selected edge
//      is drawn a pixel wider than the critical path, so the two facts stay two
//      strokes. The emulation is Chromium's own, so this is the system's
//      forced-colors pass and not a stylesheet pretending.
//   4. **the keyboard walks the graph.** A roving `tabindex` is a claim about
//      the *tab order*, which a fake DOM cannot have: Tab has to reach the
//      tablist and a task (a group whose every door is out of the tab order is a
//      picture no keyboard can enter), the arrows have to move the tab, and a
//      redraw has to put the focus back in the tree it drew rather than on
//      `<body>`.
//   5. **the three things that move stop moving when the reader asks them to.**
//      GV2.4's motion is a comet on a running card, a pulse on a critical one and
//      a flow along a critical edge; `prefers-reduced-motion: reduce` leaves each
//      as its still drawing and `forced-colors: active` draws them without
//      running them (design section 3.9's amendment and section 4.4). Whether a
//      *computed* `animation-name` is `none` is a claim about a rendered element
//      and a browser's own cascade -- and the reduced-motion half is the one the
//      global freeze in `base.css` cannot make (F36: it only shortens the
//      animation, which parks a comet off the card's far edge at `opacity: 0`).
//   6. **a release heading is hit-tested as itself and not as the card that used
//      to cover it.** The box is grown upward for its heading, and the engine's
//      own room within a layer -- two cards of one layer are stacked one above
//      the other, now that the picture is read left to right -- was less than
//      that growth, so the heading was drawn inside the card above it, and
//      painted over, because the screen appends every box before every card. A
//      screenshot cannot report this (a covered heading is a heading-shaped
//      hole, which looks like a heading drawn faintly) and the unit suite cannot
//      either: whether a point on a glyph belongs to the heading or to a card is
//      the browser's own hit test, over the real worker's answer. The hit test is
//      made **once per glyph**, because one point on a heading is a point a card
//      can miss: a card covering all but the first character of a line leaves
//      that one point over the heading and every other one under the card.
//   7. **the streams are drawn as rows.** One engine run per stream (the owner's
//      ruling of 2026-10-31) is a claim about rectangles: the bands are stacked,
//      and every card is inside its own stream's band. `graph.test.ts` proves it
//      over the real engine with no browser; this is the same claim where the
//      layout arrived through the real worker and the real CSP, and where the
//      rectangles are the browser's own.
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
//
// **The goldens** are the third describe: `test/e2e/__screenshots__/graph/{light,dark}-<platform>.png`,
// taken the way `spend.spec.ts` takes its own -- the compiled screen loaded out
// of `dist/` into the window's own document and mounted into a host of this
// spec's making, so what a golden frames is the screen and not the window's
// entry beside it. The name is passed to `toHaveScreenshot` as an ARRAY
// (`['graph', 'light.png']`), because Playwright sanitizes a STRING name before
// `snapshotPathTemplate` applies and would flatten `'graph/light.png'` into
// `__screenshots__/graph-light.png` beside the Live screen's two files.
//
// The frame is the mount for the reason `spend.spec.ts` records at length, and
// one of its own: the entry draws this same screen into the document beside the
// mount, from the same stub document, and two `section.graph` elements in one
// document are two elements a page-scoped frame cannot tell apart. **No PNG is
// committed**, as for the other five screens: macOS and Linux pixels differ, and
// the baselines are owner-run.

import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { GRAPH_BUSY } from '../../src/renderer/entry'
import { GRAPH_THEMES } from '../../src/renderer/screens/graph'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { expectGolden, expectGrounded } from '../support/goldens'
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

/** HEADING is a release box's heading text: the name line a card covered. */
const HEADING = 'text.graph-release-heading'

/** RUNS is the console's own run list, in the shape `plan status` writes a
 * task's `run` as: the last four characters are what the document carries, and
 * the whole id is what a card's door opens (`runForSuffix`). The fixture's five
 * suffixes are here, so no card in a golden is drawn with the sentence a suffix
 * that matches nothing gets. */
const RUNS: readonly string[] = Object.freeze([
  '20261027-030000-aa01',
  '20261027-030000-aa02',
  '20261027-030000-aa03',
  '20261027-030000-aa04',
  '20261027-030000-aa05',
])

/** THE_MOUNT is where the golden describe draws the screen, and therefore what
 * its goldens frame: the mount and never the page (see the header). */
const THE_MOUNT = '#graph-mount'

/** GOLDEN_DIR is the directory this spec's recordings live in, and
 * GOLDEN_NAMES are the names `toHaveScreenshot` is given: arrays, because
 * Playwright flattens a string's `/` (see the header). */
const GOLDEN_DIR = join(consoleDir, 'test', 'e2e', '__screenshots__', 'graph')
const GOLDEN_NAMES = {
  light: ['graph', 'light.png'],
  dark: ['graph', 'dark.png'],
} as const

/** SKIP_GOLDENS is the one documented way to run this file with no goldens
 * recorded: `FUGARO_CONSOLE_GOLDENS=skip`. Anything else, including unset, means
 * the golden tests run and a missing baseline fails them. */
const SKIP_GOLDENS = process.env['FUGARO_CONSOLE_GOLDENS'] === 'skip'

/** SPEC is this file's own name: what a re-record command has to name, and what
 * every golden failure here quotes back to the reader. */
const SPEC = 'graph.spec.ts'

/** The compiled modules the screen is made of, in dependency order: the screen,
 * the three modules of the picture (the model, the cache and the worker client)
 * and the three shared modules those reach. `shared/schema.js` is here because
 * the fixture is decoded in the window rather than in this file: the screen is
 * handed a document the console's own decoder admitted, which is the shape the
 * entry hands it. Nothing else may be reachable -- the registry below refuses a
 * specifier that is not a relative path, so a bundle that grew an import of
 * `node:` or of a package fails here rather than in a window with no Node in
 * it. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/schema.js',
  'renderer/graph/model.js',
  'renderer/graph/layout-client.js',
  'renderer/graph/layout.js',
  'renderer/screens/graph.js',
])

/** moduleSource is one compiled module as a registry entry. */
function moduleSource(relative: string): string {
  const code = readFileSync(join(consoleDir, 'dist', relative), 'utf8')
  return `registry[${JSON.stringify(`/${relative}`)}] = function (exports, require, module) {\n${code}\n};`
}

/** LOADER is the module registry the injected bundle runs on: a CommonJS
 * `require` that can resolve `./` and `../` specifiers and nothing else. */
const LOADER = `
var registry = {};
var cache = {};
function normalize(spec, from) {
  if (spec.charAt(0) !== '.') throw new Error('the renderer may not require ' + spec);
  var base = from.split('/').slice(0, -1);
  var parts = spec.split('/');
  for (var i = 0; i < parts.length; i++) {
    var part = parts[i];
    if (part === '' || part === '.') continue;
    if (part === '..') { base.pop(); continue; }
    base.push(part);
  }
  var resolved = base.join('/');
  return /\\.js$/.test(resolved) ? resolved : resolved + '.js';
}
function load(path) {
  if (cache[path]) return cache[path].exports;
  var definition = registry[path];
  if (!definition) throw new Error('no module ' + path);
  var module = { exports: {} };
  cache[path] = module;
  definition(module.exports, function (spec) { return load(normalize(spec, path)); }, module);
  return module.exports;
}
`

/**
 * FACTORY is a DOM-backed `GraphFactory`: one wrapper per element, so the
 * screen's own narrow interface is served by a real node, in the namespace the
 * tag needs -- the picture is SVG geometry, and `createNS` is the second method
 * this screen's factory has. `node` is the harness's handle on the element and
 * is never visible to the screen.
 */
const FACTORY = `
function domFactory() {
  var wrappers = new Map();
  function wrap(node) {
    var existing = wrappers.get(node);
    if (existing) return existing;
    var wrapper = {
      tag: node.tagName.toLowerCase(),
      node: node,
      get textContent() { return node.textContent; },
      set textContent(value) { node.textContent = value; },
      setAttribute: function (name, value) { node.setAttribute(name, value); },
      getAttribute: function (name) { return node.getAttribute(name); },
      removeAttribute: function (name) { node.removeAttribute(name); },
      append: function (child) { node.appendChild(child.node); },
      childNodes: function () {
        var out = [];
        for (var i = 0; i < node.children.length; i++) out.push(wrap(node.children[i]));
        return out;
      },
    };
    wrappers.set(node, wrapper);
    return wrapper;
  }
  return {
    create: function (tag) { return wrap(document.createElement(tag)); },
    createNS: function (tag) { return wrap(document.createElementNS('http://www.w3.org/2000/svg', tag)); },
    wrap: wrap,
  };
}
`

/** draw is the expression the window evaluates: decode the committed fixture
 * through the console's own door, lay it out with the real worker, and mount
 * what the screen draws. It answers a promise, because the layout is the
 * worker's: `executeJavaScript` waits for it. */
function draw(theme: string): string {
  const plan = JSON.parse(readFileSync(PLAN_FIXTURE, 'utf8')) as unknown
  return `(function () {
${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}
var schema = load('/shared/schema.js');
var graph = load('/renderer/screens/graph.js');
var decoded = schema.decodePlanStatus(${JSON.stringify(plan)}, 'the committed plan document');
if (!decoded.ok) return Promise.resolve({ error: 'the decoder refused the fixture: ' + decoded.skew.because });
var factory = domFactory();
var screen = graph.graphScreen(factory, ${JSON.stringify(theme)});
return screen.load(decoded.value, ${JSON.stringify(RUNS)}).then(function (drawing) {
  if (drawing === null) return { error: 'the screen answered no drawing for the committed document' };
  var previous = document.getElementById('graph-mount');
  if (previous) previous.remove();
  var host = document.createElement('div');
  host.id = 'graph-mount';
  host.appendChild(drawing.node);
  document.body.appendChild(host);
  return {
    theme: drawing.node.getAttribute('data-theme'),
    releases: host.querySelectorAll('g.graph-release').length,
    cards: host.querySelectorAll('g.graph-node').length,
  };
});
})()`
}

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

/** Rect is a rectangle read out of the window: the four edges of it, in the
 * window's own coordinates, which is what makes two of them comparable. */
interface Rect {
  readonly x: number
  readonly y: number
  readonly right: number
  readonly bottom: number
}

/** planStreams is the committed document's plan id to the stream that plan names
 * (the empty string for a plan that names none): the model's columns are built
 * from exactly that, and the band a card is drawn in is its plan's stream. */
function planStreams(): ReadonlyMap<string, string> {
  const document = JSON.parse(readFileSync(PLAN_FIXTURE, 'utf8')) as {
    plans: { id: string; stream?: string }[]
  }
  const found = new Map<string, string>()
  for (const plan of document.plans) found.set(plan.id, plan.stream ?? '')
  return found
}

/** planOf is the plan a card's own layout key names, or `null` when the key names
 * no plan of `streams`. The key is `nodeKey`'s: the plan's **length**, the plan,
 * then the task. The length is what says where the plan's id ends, so an id
 * holding a colon is read whole. */
function planOf(key: string, streams: ReadonlyMap<string, string>): string | null {
  const at = key.indexOf(':')
  const length = Number(key.slice(0, at))
  if (!Number.isFinite(length)) return null
  const plan = key.slice(at + 1, at + 1 + length)
  return streams.has(plan) ? plan : null
}

/** holds is whether one rectangle holds another whole. */
function holds(outer: Rect, inner: Rect): boolean {
  return inner.x >= outer.x && inner.y >= outer.y && inner.right <= outer.right && inner.bottom <= outer.bottom
}

/** strokeDash is one element's computed `stroke-dasharray`: the browser's own
 * answer to what the stylesheet asked for. */
async function strokeDash(page: Page, selector: string): Promise<string> {
  return page.locator(selector).first().evaluate((element) => getComputedStyle(element).strokeDasharray)
}

/** strokeOffset is one element's computed `stroke-dashoffset`: where the dash a
 * mark is drawn with has come to rest. */
async function strokeOffset(page: Page, selector: string): Promise<string> {
  return page.locator(selector).first().evaluate((element) => getComputedStyle(element).strokeDashoffset)
}

/** COMET, PULSE and FLOW are the three marks GV2.4's motion is drawn on: the
 * streak that travels a running card's border, the glow behind a critical card,
 * and the critical path's own dashes. */
const COMET = 'g.graph-node[data-state="implementing"] .graph-node-comet'
const PULSE = 'g.graph-node[data-critical="true"] .graph-node-pulse'
const FLOW = 'g.graph-edge[data-critical="true"] .graph-edge-path'

/** moving is one mark's computed motion -- the animation's name and its opacity,
 * which is the whole of what "this is still" means here -- or `null` when nothing
 * matches, which is a claim of its own: a selector that matches no element is the
 * one way "it does not animate" is trivially true. */
async function moving(
  page: Page,
  selector: string,
): Promise<{ readonly name: string; readonly opacity: string } | null> {
  return page.evaluate((at) => {
    const element = document.querySelector(at)
    if (element === null) return null
    const style = getComputedStyle(element)
    return { name: style.animationName, opacity: style.opacity }
  }, selector)
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

  test('reaches the picture from the keyboard before any task is selected', async () => {
    // **This case is first because it is about the state the screen opens in.**
    // Nothing is selected when the graph is drawn, and the doors are a roving
    // `tabindex`: a group whose every door is at `-1` is a group the keyboard
    // cannot enter at all, which leaves the picture and the list mouse-only -- no
    // task selectable, no run openable, and the arrow walk with nowhere to start.
    // So the tab order has to reach a door, and the door it reaches is the stop
    // and not the selection.
    expect(await pressTabUntil(page, 'graph-node'), 'the tab order never reached a task').toBe(true)
    const reached = await active(page)
    expect(reached.classes).toContain('graph-node')
    expect(reached.attributes['data-selected']).toBe('false')

    // One door in the tab order, and it is the first the drawing draws: the
    // roving rule is the tablist's own, one stop and not a second walk through
    // every card.
    const stops = await page.evaluate(() =>
      [...document.querySelectorAll('g.graph-node, li.graph-row')].filter(
        (each) => each.getAttribute('tabindex') === '0',
      ),
    )
    expect(stops).toHaveLength(1)
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

  test('draws every release heading out from under the task cards', async () => {
    // The committed document carries two releases, and the case is about a
    // heading a card covered: one heading would leave it vacuous if the layout
    // ever drew a single row.
    const headings = page.locator(HEADING)
    expect(await headings.count(), 'the committed document draws two release headings').toBeGreaterThan(1)

    for (const heading of await headings.all()) {
      // Each heading is scrolled to the middle of the window before it is read:
      // the picture is taller than the viewport, and `elementsFromPoint` answers
      // about the viewport -- a point below the fold hit-tests as nothing at all,
      // which is a pass for the wrong reason.
      const reading = await heading.evaluate((element) => {
        element.scrollIntoView({ block: 'center', inline: 'center' })
        // **Every glyph of the heading is read, one Range per character.** The
        // point hit-tested is the centre of a glyph's own box, because the centre
        // of the text element's box can land between two glyphs, where the hit
        // test answers about whatever is behind the text -- the very answer this
        // case exists to rule out. One glyph would not be enough either: a card
        // can cover the rest of the line and leave the first character standing,
        // which is a heading a reader cannot read and a case that would pass.
        const glyphs: { readonly text: string; readonly rect: DOMRect }[] = []
        const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT)
        for (let text = walker.nextNode(); text !== null; text = walker.nextNode()) {
          const value = text.textContent ?? ''
          for (let at = 0; at < value.length; at += 1) {
            const range = document.createRange()
            range.setStart(text, at)
            range.setEnd(text, at + 1)
            glyphs.push({ text: value[at] ?? '', rect: range.getBoundingClientRect() })
          }
        }
        return {
          release: element.parentElement?.getAttribute('data-release') ?? 'a release',
          glyphs: glyphs.map((glyph) => {
            const at = { x: glyph.rect.left + glyph.rect.width / 2, y: glyph.rect.top + glyph.rect.height / 2 }
            const stack = document.elementsFromPoint(at.x, at.y)
            const mine = stack.findIndex((each) => each === element || element.contains(each))
            return {
              text: glyph.text,
              height: glyph.rect.height,
              inViewport: at.x >= 0 && at.y >= 0 && at.x <= window.innerWidth && at.y <= window.innerHeight,
              // What the point actually hits: the topmost element at a glyph is
              // the `<text>` itself or one of its tspans.
              hit: stack[0]?.getAttribute('class') ?? 'nothing',
              topIsHeading: stack[0] !== undefined && (stack[0] === element || element.contains(stack[0])),
              // And every element *above* the heading that belongs to a task card:
              // the stack is topmost-first, so anything before the heading is paint
              // order that came later, which is what covering it means.
              coveredBy: (mine < 0 ? stack : stack.slice(0, mine))
                .filter((each) => each.closest('g.graph-node') !== null)
                .map((each) => each.getAttribute('class') ?? ''),
            }
          }),
        }
      })

      const glyphs = reading?.glyphs ?? []
      expect(
        glyphs.length,
        `${String(reading?.release)}'s heading was read as ${String(glyphs.length)} glyphs, and a heading is hit-tested glyph by glyph`,
      ).toBeGreaterThan(1)
      for (const glyph of glyphs) {
        const where = `${String(reading?.release)}'s heading, at its “${glyph.text}”`
        expect(glyph.height, `the glyph read at ${where} is empty, so its hit test is vacuous`).toBeGreaterThan(0)
        expect(glyph.inViewport, `the point at ${where} is outside the viewport, where a hit test answers nothing`).toBe(true)
        expect(glyph.topIsHeading, `${where}, is hit as ${String(glyph.hit)} instead`).toBe(true)
        expect(glyph.coveredBy, `${where}, is painted over by a task card`).toEqual([])
      }
    }
  })

  test('stacks the streams as rows, and keeps every card inside its own row', async () => {
    // **One engine run per stream** (the owner's ruling of 2026-10-31). Asked for
    // the whole picture at once, the engine packs components anywhere: a stream
    // whose plans run deeper than its neighbour's has the neighbour's plans
    // placed among its own, and the band drawn as the union of its cards then
    // swallows another stream's cards. Laid out one run per stream and stacked in
    // the model's order, two rows cannot share ground.
    //
    // The committed document draws two of them -- `example`, and the plans that
    // name no stream, which the model gives a column of its own -- so a claim
    // about rows is not vacuous here, and every card has a band of its own to be
    // inside.
    const streams = planStreams()
    const reading = await page.evaluate(() => {
      const rect = (element: Element | null): Rect | null => {
        if (element === null) return null
        const box = element.getBoundingClientRect()
        return { x: box.left, y: box.top, right: box.right, bottom: box.bottom }
      }
      return {
        bands: [...document.querySelectorAll('g.graph-band')].map((each) => ({
          stream: each.getAttribute('data-stream') ?? '',
          box: rect(each.querySelector('.graph-band-box')),
        })),
        cards: [...document.querySelectorAll('g.graph-node')].map((each) => ({
          key: each.getAttribute('data-key') ?? '',
          box: rect(each.querySelector('.graph-node-box')),
        })),
      }
    })

    expect(reading.bands.length, 'the committed document draws one band per stream').toBeGreaterThan(1)
    expect(reading.cards.length).toBeGreaterThan(1)
    for (const band of reading.bands) {
      expect(band.box, `${band.stream}'s band drew no box`).not.toBeNull()
      expect([...streams.values()], `no plan of the committed document is in ${band.stream}`).toContain(band.stream)
    }
    const boxes = reading.bands.map((band) => band.box as Rect)

    // **The rows share no ground.** Read in the order the screen drew them -- the
    // model's own order, stream by stream -- each band's bottom is above the next
    // band's top. Bands that overlapped are exactly the defect: one stream's band
    // drawn across another stream's cards.
    for (let at = 0; at + 1 < boxes.length; at += 1) {
      const above = boxes[at] as Rect
      const below = boxes[at + 1] as Rect
      expect(
        above.bottom,
        `${reading.bands[at]?.stream ?? ''}'s band reaches into ${reading.bands[at + 1]?.stream ?? ''}'s row`,
      ).toBeLessThanOrEqual(below.y)
    }
    // And the drawn order is the stacked order: the bands are drawn where they
    // are stacked, so the picture's own rows are the model's columns.
    const drawn = reading.bands.map((band) => band.stream)
    const byRow = [...reading.bands]
      .sort((one, two) => (one.box?.y ?? 0) - (two.box?.y ?? 0))
      .map((band) => band.stream)
    expect(byRow, 'the rows are stacked in the order the screen drew them').toEqual(drawn)

    // **Every card is inside its own stream's band**, and no card is inside two:
    // a card laid out by one stream's run and drawn in another's row is the
    // picture this change removed.
    for (const card of reading.cards) {
      const plan = planOf(card.key, streams)
      expect(plan, `${card.key} names no plan of the committed document`).not.toBeNull()
      const mine = streams.get(plan ?? '') ?? ''
      const holders = reading.bands.filter((band) => band.box !== null && card.box !== null && holds(band.box, card.box))
      expect(holders.map((band) => band.stream), `${card.key} is inside its plan's own band`).toEqual([mine])
    }
  })

  test('keeps the dash and the edge scale when forced colors is active', async () => {
    // A task is selected first: the selection is a width as well as a tone
    // (`.graph-node[data-selected='true'] .graph-node-box`), and a window with
    // nothing selected has no such element at all -- a `null` read there would
    // make the assertion below pass whatever the stylesheet says. The card
    // selected is one in a **neutral** state, so the running card read below
    // keeps its own state's thickness with the selection out of its way.
    await page.locator('g.graph-node[data-state="ready"]').first().click()
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
          // An edge that is neither critical, nor a cross-release one (the design
          // draws those at 1 px), nor one of the selected task's -- so the width
          // read here is the plain one and not another rule's.
          plain: width(
            'g.graph-edge[data-critical="false"][data-cross="false"][data-selected="false"] .graph-edge-path',
          ),
          selected: width('g.graph-node[data-selected="true"] .graph-node-box'),
          // The selected task's own edges. The task selected above is in a
          // neutral state and off the critical path, so the edge read here is one
          // the selection is the only rule for: an edge of the critical path that
          // were also selected would answer for both rules at once.
          selectedEdge: width('g.graph-edge[data-selected="true"] .graph-edge-path'),
          // A task being worked on, with no selection on it: its state is 2 px,
          // which is the thickness the selected card's boundary has to be told
          // apart from -- in this mode the tone that says the same thing is
          // gone, so the difference has to be the width.
          running: width('g.graph-node[data-state="implementing"]:not([data-selected="true"]) .graph-node-box'),
        }
      })
      expect(widths.critical).toBe('3px')
      expect(widths.plain).toBe('2px')
      // The whole distinction survives, which is the claim design section 2.11
      // makes for this mode: four strokes, four widths. The selected one is
      // **wider** than the critical path and not the same width in another tone,
      // because the tone is the one thing this mode takes away -- 4 px against 3,
      // where the selection's own rule draws the two in different tones and the
      // same thickness.
      expect(widths.selectedEdge).toBe('4px')
      expect(widths.selectedEdge).not.toBe(widths.critical)
      // The selection is a width too, and the tone it is drawn in is the
      // reading's -- which forced colors takes away, leaving the thickness. It is
      // 3 px against a running card's 2, which is what keeps the selected card
      // distinguishable from the state it also carries.
      expect(widths.selected).toBe('3px')
      expect(widths.running).toBe('2px')

      // A band and a card are the **same fill** in this mode (`Canvas`), so the
      // boundary is all that is left to tell the row's ground from a card on it,
      // and the two are read against the reading tone rather than against a
      // literal: the card's boundary is what a title is filled in (`CanvasText`
      // in this mode, and never a name this test has to know), and the band's is
      // not it.
      const inks = await page.evaluate(() => {
        const ink = (selector: string, property: 'stroke' | 'fill'): string | null => {
          const element = document.querySelector(selector)
          return element === null ? null : getComputedStyle(element)[property]
        }
        return {
          band: ink('g.graph-band .graph-band-box', 'stroke'),
          // A card with no selection on it: the selected card's boundary is
          // `Highlight` by this arm's own rule, which is a mark of its own and
          // not the reading tone read beside it here.
          card: ink('g.graph-node:not([data-selected="true"]) .graph-node-box', 'stroke'),
          reading: ink('g.graph-node .graph-node-title', 'fill'),
        }
      })
      expect(inks.reading).not.toBeNull()
      expect(inks.card).toBe(inks.reading)
      expect(inks.band).not.toBe(inks.reading)
    } finally {
      await page.emulateMedia({ forcedColors: 'none' })
    }
  })

  test('draws no motion at all when forced colors is active', async () => {
    // The three marks first, with no emulation on: without this the case below
    // would pass on three selectors that match nothing, which is the one way a
    // claim about motion is trivially true. The names are the stylesheet's own.
    // Both media are set rather than left to the host -- a developer's own
    // "reduce motion" setting would otherwise have the marks already off.
    await page.emulateMedia({ reducedMotion: 'no-preference', forcedColors: 'none' })
    expect((await moving(page, COMET))?.name).toBe('graph-comet')
    expect((await moving(page, PULSE))?.name).toBe('graph-pulse')
    expect((await moving(page, FLOW))?.name).toBe('graph-flow')

    await page.emulateMedia({ forcedColors: 'active' })
    try {
      // Design section 4.4 and the amendment's fourth rule: the mode that takes
      // the tones away keeps the marks and runs nothing. The arms are written on
      // the same selectors the motion block animates, because a media query adds
      // no specificity -- so a browser that computed `none` here is a browser
      // that has the cascade right, not merely a rule in the file.
      expect((await moving(page, COMET))?.name).toBe('none')
      expect((await moving(page, PULSE))?.name).toBe('none')
      expect((await moving(page, FLOW))?.name).toBe('none')
    } finally {
      await page.emulateMedia({ forcedColors: 'none' })
    }
  })

  test('leaves each moving state as its still drawing when reduced motion is asked for', async () => {
    // **F36.** `base.css` freezes animation globally with
    // `animation-duration: 0.01ms !important` and one iteration, and that is a
    // duration and a count and nothing else: it leaves every animation *named*,
    // and it leaves a mark whose dasharray lives in the animated rule -- the flow,
    // whose dashes are not in a keyframe -- drawn as dashes that are not flowing,
    // which is a picture of something that is not happening. So the graph restates
    // its three states under its own arm, and this is the case that says so in a
    // browser.
    await page.emulateMedia({ reducedMotion: 'reduce' })
    try {
      const comet = await moving(page, COMET)
      expect(comet, 'no comet is drawn, so the still mark below would be vacuous').not.toBeNull()
      expect(comet?.name).toBe('none')
      // The still drawing: the streak's dash at rest on the card's own border --
      // a dash that is drawn, and drawn where the animation starts it, rather
      // than a border left with whatever the freeze stopped on.
      expect(await strokeDash(page, COMET)).not.toBe('none')
      expect(await strokeOffset(page, COMET)).toBe('0px')

      const pulse = await moving(page, PULSE)
      expect(pulse, 'no glow is drawn, so the still mark below would be vacuous').not.toBeNull()
      expect(pulse?.name).toBe('none')
      // And the glow is still a glow: its resting opacity, behind the card.
      expect(pulse?.opacity).toBe('0.45')

      const flow = await moving(page, FLOW)
      expect(flow?.name).toBe('none')
      // And the dashes go with it: the static style of a critical edge is the
      // 3 px line, and a dasharray left behind by the animation would be a
      // dashed critical path in the mode that asked for no motion.
      expect(await strokeDash(page, FLOW)).toBe('none')

      // Nothing a reader reads moved or went away: the word the mark was drawn
      // for is still in the DOM beside it (design section 3.5).
      expect(await page.locator(`${CANVAS} [data-word]`).allTextContents()).toContain('implementing')
    } finally {
      await page.emulateMedia({ reducedMotion: 'no-preference' })
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

test.describe('the Graph screen', () => {
  // One window, and the tests in order: each draws the screen it is about into
  // the same document, and the goldens are taken from what the test before them
  // mounted.
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // A scratch profile with the stub as the active installation and a checkout,
    // as the cases above: without either the window opens on the picker or on the
    // entry's refusal, and the document this spec mounts into would be a document
    // with no stylesheet to paint it.
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    cli.seedActiveCheckout()
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
    // GV2.4's motion is the one thing this screen draws that is not a function of
    // its inputs: a comet, a pulse and a flow, all three infinite. The
    // reduced-motion arm leaves each as its still drawing (design section 3.9's
    // amendment), so a frame is the same frame in every run -- and it is the arm
    // a reader who asked for no motion is shown, which is the drawing a golden
    // should be a record of.
    await page.emulateMedia({ reducedMotion: 'reduce' })
  })

  test.afterAll(async () => {
    await app?.close()
    cli.remove()
  })

  /** mount draws the committed fixture in one theme and fails with the window's
   * own words if the bundle could not be loaded or the screen refused to draw. */
  async function mount(theme: string): Promise<void> {
    const result = (await app.evaluate(async ({ BrowserWindow }, source) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(source, true)) as { error?: string }
    }, draw(theme))) as { readonly error?: string }
    expect(result.error).toBeUndefined()
  }

  /** drawn reads what the mount holds back out of the live document: the theme
   * the screen stamped, and the two things a frame of it has to contain. */
  async function drawn(): Promise<{ readonly theme: string | null; readonly cards: number; readonly releases: number }> {
    return page.evaluate(() => {
      const scope: ParentNode = document.getElementById('graph-mount') ?? document.createDocumentFragment()
      return {
        theme: scope.querySelector('section.graph')?.getAttribute('data-theme') ?? null,
        cards: scope.querySelectorAll('g.graph-node').length,
        releases: scope.querySelectorAll('g.graph-release').length,
      }
    })
  }

  test('is painted from the design tokens, not from the browser defaults', async () => {
    // The same computed-ground assertion the other five screens' specs make, and
    // the one case here that is red when the stylesheet stops being linked: the
    // theme travels as a `data-theme` attribute the renderer stamps, so every
    // read above passes on a tree nothing paints.
    //
    // NOT a golden test, deliberately: `FUGARO_CONSOLE_GOLDENS=skip` does not
    // skip it. Nothing here reads a PNG, so an unstyled tree is red on its own
    // account rather than recorded as a fresh baseline nobody compared.
    await expectGrounded(page, (theme) => mount(theme), {
      themes: GRAPH_THEMES,
      mount: THE_MOUNT,
      text: 'h1.graph-title',
      screen: 'the Graph screen',
    })
  })

  test('draws the graph screen', async ({}, testInfo) => {
    await mount('light')
    // The frame is worth recording only if the document reached it: a worker
    // that answered nothing leaves the screen on its list fallback, which would
    // be a golden of a sentence.
    const light = await drawn()
    expect(light.theme).toBe('light')
    expect(light.cards, 'the committed document draws no task card, so the frame is not the graph').toBeGreaterThan(0)
    expect(light.releases, 'the committed document draws no release box').toBeGreaterThan(0)
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await expectGolden(page.locator(THE_MOUNT), GOLDEN_NAMES.light, { spec: SPEC, consoleDir, testInfo })
  })

  test('draws the graph screen in the dark theme', async ({}, testInfo) => {
    await mount('dark')
    const dark = await drawn()
    expect(dark.theme).toBe('dark')
    expect(dark.cards, 'the committed document draws no task card, so the frame is not the graph').toBeGreaterThan(0)
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await expectGolden(page.locator(THE_MOUNT), GOLDEN_NAMES.dark, { spec: SPEC, consoleDir, testInfo })
  })

  test('resolves the golden names under the graph directory', ({}, testInfo) => {
    // Not a golden test: it asks Playwright where a recording would land, so a
    // name the template flattens is red before anyone records anything.
    expect(testInfo.snapshotPath(...GOLDEN_NAMES.light)).toBe(join(GOLDEN_DIR, `light-${process.platform}.png`))
    expect(testInfo.snapshotPath(...GOLDEN_NAMES.dark)).toBe(join(GOLDEN_DIR, `dark-${process.platform}.png`))
  })

  test('has a golden for each theme the design names', () => {
    // A theme with no golden is a theme nobody has looked at. The themes are the
    // screen's own, and the directory is this screen's own, so a third palette --
    // or a PNG that was never committed -- fails here.
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    const present = existsSync(GOLDEN_DIR)
      ? readdirSync(GOLDEN_DIR)
          .filter((file) => file.endsWith('.png'))
          .sort()
      : []
    // One per theme for THIS platform (the template adds `-<platform>`: macOS
    // and Linux pixels differ and CI runs both), and nothing that is not a
    // theme's recording for one of those two platforms.
    expect(present.filter((file) => file.endsWith(`-${process.platform}.png`))).toEqual(
      GRAPH_THEMES.map((theme) => `${theme}-${process.platform}.png`).sort(),
    )
    for (const file of present) expect(file).toMatch(new RegExp(`^(${GRAPH_THEMES.join('|')})-(darwin|linux)\\.png$`))
  })
})

// ---------------------------------------------------------------------------
// The Graph view's stream filter, in the dashboard.
// ---------------------------------------------------------------------------

/** THE_PANEL is the dashboard's own panel, which is where the view is drawn: the
 * describe above mounts a screen of its own into `#graph-mount`, so a bare
 * `section.graph` would be ambiguous between the two. */
const THE_PANEL = 'div.console-panel'

/** THE_GRAPH is the dashboard's Graph view, and the scope every reading below is
 * taken in. */
const THE_GRAPH = `${THE_PANEL} section.graph`

/** SELECTED_TAB is the tab the screen drew as the one showing. The tab that is
 * showing carries no listener (the entry wires the doors it does not draw as
 * current), so what a case about presses can read is which one it is. */
const SELECTED_TAB = `${THE_GRAPH} nav.graph-tabs button.graph-tab[aria-selected="true"]`

test.describe('the Graph view’s stream filter', () => {
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // A window of its own, opened on the Graph for the first time: the tab the
    // console opens on is All, which is the state the two presses below are
    // made from.
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    // The checkout `plan.status` is read against: the CLI resolves `docs/` from
    // its working directory, so an installation that records none is refused
    // before any child runs and the view would draw that refusal instead of a
    // canvas.
    cli.seedActiveCheckout()
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

  test('keeps the stream the operator pressed last when two presses land in one frame', async () => {
    // The committed document carries three tabs: the catalogue's stream, the
    // column its second plan makes by naming no stream, and All. Two of them can
    // be pressed from here, which is what this case needs -- the third is the one
    // showing.
    await expect(page.locator(SELECTED_TAB)).toHaveAttribute('data-tab', 'all')
    await expect(page.locator(`${THE_GRAPH} nav.graph-tabs button.graph-tab`)).toHaveCount(3)

    // **Two presses in one turn of the event loop.** Each is a layout of its own
    // graph rather than a redraw of the same picture, and the answer that must
    // be on screen is the second one's, whatever order the two come back in.
    const inTheFrame = await page.evaluate((scope) => {
      const root = (): Element | null => document.querySelector(scope)
      const press = (key: string): void => {
        const button = root()?.querySelector<HTMLElement>(`nav.graph-tabs button.graph-tab[data-tab="${key}"]`)
        button?.click()
      }
      press('stream:example')
      press('stream:')
      const section = root()
      return {
        // The mark is up in the frame the press happened in, over the picture the
        // operator was already looking at.
        busy: section?.getAttribute('aria-busy') ?? null,
        marked: section?.hasAttribute('data-busy') ?? null,
        label: section?.querySelector('.console-busy-label')?.textContent ?? null,
        canvas: section?.querySelector('svg.graph-canvas') !== null,
      }
    }, THE_GRAPH)
    expect(inTheFrame).toEqual({
      busy: 'true',
      marked: true,
      label: GRAPH_BUSY,
      canvas: true,
    })

    // The second press's graph is the one that stays. The tab is the strongest
    // reading -- it is what the press chose -- and the picture behind it is
    // independent evidence: the first plan is the only one with releases, so a
    // canvas that came back for the first press would still carry its headings,
    // and a dropped answer draws none of them.
    await expect(page.locator(SELECTED_TAB)).toHaveAttribute('data-tab', 'stream:')
    await expect(page.locator(`${THE_GRAPH} ${HEADING}`)).toHaveCount(0)
    await expect(page.locator(`${THE_GRAPH} g.graph-node`)).toHaveCount(1)
  })
})
