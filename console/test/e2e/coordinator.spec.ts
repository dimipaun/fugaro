// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Coordinator tab as a graph of the project's active orchestrators (CG7b,
// D3210; the read is C-ORCH's map document,
// `docs/console-fixes/contract-orchestrators.md`).
//
// `coordinator.test.ts` holds the decoder and the screen against
// `test/support/fakeDom.ts`, and `entry-wiring.test.ts` the wiring. This file is
// the one place either of them meets a **browser**: the real CSP, the real
// stylesheet applied to the elements the screen draws, and -- in the second
// describe -- the real entry opening the tab.
//
// Three claims live here and nowhere else:
//
//   1. **the overlap is dashed in a browser's own answer.** An overlap edge and
//      a dependency edge differ by a tone *and* by a dash, and the dash is the
//      channel that survives forced colors (design sections 3.5 and 4.4 rule 2):
//      `getComputedStyle(path).strokeDasharray` is the browser's reading of what
//      the stylesheet asked for, and a unit case has no stylesheet at all.
//   2. **the console's own words are in the DOM beside every tone**, and they
//      stay there when the author's colours are gone. The overlap's count and
//      its sentence are `literal`s the screen writes, so the tone is redundant
//      by construction -- which is the whole of the reason this screen adds no
//      arm to the forced-colors block.
//   3. **the floor is a sentence and not a refusal** (F40). `orchestrator map`
//      is registered from `ee/`, so a CLI below the release that carries it is
//      answered with the console's own sentence naming that release -- and with
//      **no read spawned at all**, which only a window running the real entry
//      can show.
//
// **The goldens** are the first describe:
// `test/e2e/__screenshots__/coordinator/{light,dark}-<platform>.png`, taken the
// way `graph.spec.ts` takes its own -- the compiled screen loaded out of `dist/`
// into the window's own document, laid out by the **real** elk worker, and
// mounted into a host of this spec's making, so what a golden frames is the
// screen and not the window's entry beside it. The name is passed to
// `toHaveScreenshot` as an ARRAY (`['coordinator', 'light.png']`), because
// Playwright sanitizes a STRING name before `snapshotPathTemplate` applies and
// would flatten `'coordinator/light.png'` into
// `__screenshots__/coordinator-light.png` beside the Live screen's two files.
//
// **No PNG is committed**, as for the other screens: macOS and Linux pixels
// differ, and the baselines are owner-run. The document the goldens are drawn
// from is written in this file rather than committed as a fixture: it is a
// shape the contract names (`docs/console-fixes/contract-orchestrators.md`),
// and the console's own decoder admits it in the window before the screen sees
// it, so a frame of it is a frame of a document the console read.

import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import {
  COORDINATOR_EDGE_DEPENDS_CLASS,
  COORDINATOR_EDGE_OVERLAP_CLASS,
  COORDINATOR_FLOOR_AND_WORD,
  COORDINATOR_FLOOR_CLASS,
  COORDINATOR_FLOOR_VERSION,
  COORDINATOR_FLOOR_VERSION_CLASS,
  COORDINATOR_FLOOR_WORD,
  COORDINATOR_FLOOR_WORD_CLASS,
  COORDINATOR_HEALTH_CLASS,
  COORDINATOR_SOURCE_CLASS,
  COORDINATOR_SOURCE_LINE,
  COORDINATOR_THEMES,
  COORDINATOR_TITLE,
  COORDINATOR_TITLE_CLASS,
  COORDINATOR_WARNING_CLASS,
  COORDINATOR_WARNING_COUNT_CLASS,
} from '../../src/renderer/screens/coordinator'
import { CLI_VERSION_BUILT_AGAINST } from '../../src/shared/version'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { expectGolden, expectGrounded } from '../support/goldens'
import { launchConsole } from '../support/launch'

// `test/e2e/x.spec.ts` is two levels below the console's own directory.
const consoleDir = join(__dirname, '..', '..')

/** THE_MOUNT is where the golden describe draws the screen, and therefore what
 * its goldens frame: the mount and never the page (see the header). */
const THE_MOUNT = '#coordinator-mount'

/** GOLDEN_DIR is the directory this spec's recordings live in, and GOLDEN_NAMES
 * are the names `toHaveScreenshot` is given: arrays, because Playwright flattens
 * a string's `/` (see the header). */
const GOLDEN_DIR = join(consoleDir, 'test', 'e2e', '__screenshots__', 'coordinator')
const GOLDEN_NAMES = {
  light: ['coordinator', 'light.png'],
  dark: ['coordinator', 'dark.png'],
} as const

/** SKIP_GOLDENS is the one documented way to run this file with no goldens
 * recorded: `FUGARO_CONSOLE_GOLDENS=skip`. Anything else, including unset, means
 * the golden tests run and a missing baseline fails them. */
const SKIP_GOLDENS = process.env['FUGARO_CONSOLE_GOLDENS'] === 'skip'

/** SPEC is this file's own name: what a re-record command has to name, and what
 * every golden failure here quotes back to the reader. */
const SPEC = 'coordinator.spec.ts'

/**
 * MAP is the document the goldens are drawn from: the project's own
 * orchestrator and a named one, each driven, joined by a dependency one way and
 * an overlap the other, with the sweep on and a heartbeat. It is written from
 * the contract's own table rather than recorded, so the two readings a screen
 * turns on are both in it: the default orchestrator's empty `name` (drawn as the
 * **project's** own name) and a lease with no readable stamp (`null`, which is
 * not a zero).
 */
const MAP = {
  version: 1,
  project: 'repo-a',
  generated_at: '2026-11-22T10:00:00Z',
  nodes: [
    {
      id: 'repo-a',
      name: '',
      driver: 'person-1',
      active_tasks: 2,
      running_jobs: 1,
      lease_age_seconds: 90,
    },
    {
      id: 'repo-a~auth',
      name: 'auth',
      driver: 'person-2',
      active_tasks: 0,
      running_jobs: 3,
      lease_age_seconds: null,
    },
  ],
  edges: [
    { from: 'repo-a~auth', to: 'repo-a', kind: 'dependency', reason: '' },
    { from: 'repo-a', to: 'repo-a~auth', kind: 'overlap', reason: 'both touch src/auth' },
  ],
  health: {
    sweep: true,
    started_at: '2026-11-22T09:59:00Z',
    ok_at: '2026-11-22T09:59:30Z',
    at: '2026-11-22T10:00:00Z',
    note: '',
  },
}

/** The compiled modules the screen is made of, in dependency order: the screen,
 * the writers in `renderer/app.js`, the layout client whose worker places the
 * boxes, the section helper the screen's mark goes up through (with the reading
 * words and the skeleton shapes it requires), and the shared modules those
 * reach. `shared/schema.js` is here because the document is decoded in the
 * window rather than in this file: the screen is handed a document the console's
 * own decoder admitted, which is the shape the entry hands it. Nothing else may
 * be reachable -- the registry below refuses a specifier that is not a relative
 * path, so a bundle that grew an import of `node:` or of a package fails here
 * rather than in a window with no Node in it. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/shortid.js',
  'shared/notice.js',
  'shared/version.js',
  'shared/schema.js',
  'renderer/app.js',
  'renderer/skeleton.js',
  'renderer/screens/reading.js',
  'renderer/section.js',
  'renderer/graph/layout-client.js',
  'renderer/screens/coordinator.js',
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

/** FACTORY is a DOM-backed `CoordinatorFactory`: one wrapper per element, so the
 * screen's own narrow interface is served by a real node, in the namespace the
 * tag needs -- the picture is SVG geometry, and `createNS` is the second method
 * this screen's factory has. `node` is the harness's handle on the element and
 * is never visible to the screen. */
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

/** draw is the expression the window evaluates: decode the document through the
 * console's own door, lay it out with the real worker, and mount what the screen
 * draws. It answers a promise, because the layout is the worker's:
 * `executeJavaScript` waits for it. */
function draw(theme: string): string {
  return `(function () {
${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}
var schema = load('/shared/schema.js');
var coordinator = load('/renderer/screens/coordinator.js');
var decoded = schema.decodeOrchestratorMap(${JSON.stringify(MAP)});
if (!decoded.ok) return Promise.resolve({ error: 'the decoder refused the document: ' + decoded.skew.because });
var factory = domFactory();
var screen = coordinator.coordinatorScreen(factory, ${JSON.stringify(theme)});
return screen.load(decoded.value).then(function (drawn) {
  if (drawn === null) return { error: 'the screen answered no drawing for the document' };
  var previous = document.getElementById('coordinator-mount');
  if (previous) previous.remove();
  var host = document.createElement('div');
  host.id = 'coordinator-mount';
  host.appendChild(drawn.node);
  document.body.appendChild(host);
  return {
    theme: drawn.node.getAttribute('data-theme'),
    nodes: host.querySelectorAll('g.coordinator-node').length,
    edges: host.querySelectorAll('path.coordinator-edge').length,
    canvas: host.querySelectorAll('svg.coordinator-graph').length,
  };
});
})()`
}

/** strokeDash is one element's computed `stroke-dasharray`: the browser's own
 * answer to what the stylesheet asked for. */
async function strokeDash(page: Page, selector: string): Promise<string> {
  return page.locator(selector).first().evaluate((element) => getComputedStyle(element).strokeDasharray)
}

test.describe('the Coordinator screen', () => {
  // One window, and the tests in order: each draws the screen it is about into
  // the same document, and the goldens are taken from what the test before them
  // mounted.
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // A scratch profile with the stub as the active installation, as the other
    // dashboard specs do: without one the window opens on the picker and the
    // document this spec mounts into would be a document with no stylesheet to
    // paint it.
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

  /** mount draws the document in one theme and fails with the window's own words
   * if the bundle could not be loaded or the screen refused to draw. */
  async function mount(theme: string): Promise<void> {
    const result = (await app.evaluate(async ({ BrowserWindow }, source) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(source, true)) as { error?: string }
    }, draw(theme))) as { readonly error?: string }
    expect(result.error).toBeUndefined()
  }

  /** drawn reads what the mount holds back out of the live document. */
  async function drawn(): Promise<{
    readonly theme: string | null
    readonly nodes: number
    readonly edges: number
    readonly canvas: number
  }> {
    return page.evaluate(() => {
      const scope: ParentNode = document.getElementById('coordinator-mount') ?? document.createDocumentFragment()
      return {
        theme: scope.querySelector('section.coordinator')?.getAttribute('data-theme') ?? null,
        nodes: scope.querySelectorAll('g.coordinator-node').length,
        edges: scope.querySelectorAll('path.coordinator-edge').length,
        canvas: scope.querySelectorAll('svg.coordinator-graph').length,
      }
    })
  }

  test('is painted from the design tokens, not from the browser defaults', async () => {
    // The same computed-ground assertion the other screens' specs make, and the
    // one case here that is red when the stylesheet stops being linked: the
    // theme travels as a `data-theme` attribute the renderer stamps, so every
    // read above passes on a tree nothing paints.
    //
    // NOT a golden test, deliberately: `FUGARO_CONSOLE_GOLDENS=skip` does not
    // skip it. Nothing here reads a PNG, so an unstyled tree is red on its own
    // account rather than recorded as a fresh baseline nobody compared.
    await expectGrounded(page, (theme) => mount(theme), {
      themes: COORDINATOR_THEMES,
      mount: THE_MOUNT,
      text: `h1.${COORDINATOR_TITLE_CLASS}`,
      screen: 'the Coordinator screen',
    })
  })

  test('draws the two orchestrators as boxes the worker placed, with both edges', async () => {
    // The worker's answer first: the document has two nodes and two edges, and a
    // frame that drew the fallback list instead would be a golden of the
    // console's own sentence about a layout that did not arrive.
    await mount('light')
    const light = await drawn()
    expect(light.theme).toBe('light')
    expect(light.nodes, 'the worker placed no box').toBe(2)
    expect(light.edges, 'the worker routed no edge').toBe(2)
    expect(light.canvas, 'the canvas is not the drawing').toBe(1)

    // The dependency is solid and the overlap is dashed: the design's two
    // channels, read out of the browser rather than out of the source.
    expect(await strokeDash(page, `.${COORDINATOR_EDGE_DEPENDS_CLASS}`)).toBe('none')
    expect(await strokeDash(page, `.${COORDINATOR_EDGE_OVERLAP_CLASS}`)).not.toBe('none')

    // And the overlap is drawn **twice**: the dashed edge, and the console's own
    // count and sentence above the canvas -- because a dash is a tone, and no
    // reading on this screen is a tone alone (design section 3.5).
    await expect(page.locator(`.${COORDINATOR_WARNING_CLASS}`)).toHaveCount(1)
    await expect(page.locator(`.${COORDINATOR_WARNING_COUNT_CLASS}`)).toHaveText('1')
    // The health line is above the canvas and is drawn from the document: the
    // sweep's own state and the beat it last woke at.
    await expect(page.locator(`.${COORDINATOR_HEALTH_CLASS}`)).toHaveCount(1)
  })

  test('keeps the overlap’s dash, and its words, when forced colors is active', async () => {
    // Section 4.4 rule 2. This screen writes no arm for the overlap: what is
    // asserted is the system's own pass over a rule of this file's, and the dash
    // is the channel it leaves alone -- a colour it takes away, a
    // `stroke-dasharray` it does not.
    const authored = await strokeDash(page, `.${COORDINATOR_EDGE_OVERLAP_CLASS}`)
    expect(authored).not.toBe('none')

    await page.emulateMedia({ forcedColors: 'active' })
    try {
      expect(await strokeDash(page, `.${COORDINATOR_EDGE_OVERLAP_CLASS}`)).toBe(authored)
      // And the words are still there beside it: the two halves of the smoke are
      // the same claim -- the structure survives, and what the structure says is
      // still readable.
      await expect(page.locator(`.${COORDINATOR_WARNING_CLASS}`)).toHaveCount(1)
      await expect(page.locator(`.${COORDINATOR_WARNING_COUNT_CLASS}`)).toHaveText('1')
    } finally {
      await page.emulateMedia({ forcedColors: 'none' })
    }
  })

  test('draws the coordinator screen', async ({}, testInfo) => {
    await mount('light')
    // The frame is worth recording only if the document reached it: the screen
    // draws the console's own sentence about a quiet project when the map holds
    // no node, which would be a golden of a sentence.
    const light = await drawn()
    expect(light.theme).toBe('light')
    expect(light.nodes, 'the worker placed no box').toBeGreaterThan(0)
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await expectGolden(page.locator(THE_MOUNT), GOLDEN_NAMES.light, { spec: SPEC, consoleDir, testInfo })
  })

  test('draws the coordinator screen in the dark theme', async ({}, testInfo) => {
    await mount('dark')
    const dark = await drawn()
    expect(dark.theme).toBe('dark')
    expect(dark.nodes, 'the worker placed no box').toBeGreaterThan(0)
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await expectGolden(page.locator(THE_MOUNT), GOLDEN_NAMES.dark, { spec: SPEC, consoleDir, testInfo })
  })

  test('resolves the golden names under the coordinator directory', ({}, testInfo) => {
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
      COORDINATOR_THEMES.map((theme) => `${theme}-${process.platform}.png`).sort(),
    )
    for (const file of present) {
      expect(file).toMatch(new RegExp(`^(${COORDINATOR_THEMES.join('|')})-(darwin|linux)\\.png$`))
    }
  })
})

// ---------------------------------------------------------------------------
// The Coordinator tab in the dashboard, opened by the real entry.
// ---------------------------------------------------------------------------

/** THE_PANEL is the dashboard's own panel, which is where this view is drawn:
 * the describe above mounts a screen of its own into `#coordinator-mount`, so a
 * bare `section.coordinator` would be ambiguous between the two. */
const THE_PANEL = 'div.console-panel'

/** THE_VIEW is the dashboard's Coordinator tab, and the scope every reading
 * below is taken in. */
const THE_VIEW = `${THE_PANEL} section.coordinator`

/** tab is one button of the dashboard's navigation. */
function tab(view: string): string {
  return `nav.console-views button.console-view[data-view="${view}"]`
}

/**
 * THE_V2_HOOKS are the hooks D3210 removed with the tab's four reads: the action
 * queue, the fleet's totals, the job list and the transcript, plus the stored
 * view's own rows. They are asserted absent in a real window, which is where a
 * leftover drawing would be visible -- a unit case reads the tree the screen
 * built, and a rule or a markup kept in `entry.ts` beside it would not be in it.
 */
const THE_V2_HOOKS: readonly string[] = Object.freeze([
  'section.coordinator-queue',
  'section.coordinator-totals',
  'section.coordinator-jobs',
  'section.coordinator-transcript',
  'div.coordinator-body',
  'div.coordinator-detail',
  'li.coordinator-record',
  'p.coordinator-age',
  'li.coordinator-job',
  'li.coordinator-queue-item',
])

test.describe('the Coordinator tab in the dashboard', () => {
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
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

  test('draws the floor sentence in the click’s own frame, and spawns no read', async () => {
    // **The click's own frame.** What is asserted here is read from the frame the
    // click happened in, before any answer could have arrived: the header the
    // screen can state without a document, and the console's own sentence naming
    // the release `orchestrator map` arrives in.
    //
    // This window's CLI is in range but below that floor -- `ORCHESTRATOR_MAP_
    // CLI_FLOOR` is unset until a release carries the command -- so the tab is
    // the header and the sentence and **nothing else**: no busy mark (there is no
    // read), no skeleton (no shape is coming), no canvas and no refusal, because
    // a read that was never made has no sentence of its own (F40).
    const inTheFrame = await page.evaluate(
      ({ button, view }) => {
        document.querySelector<HTMLElement>(button)?.click()
        const drawn = document.querySelector(view)
        return {
          mounted: drawn !== null,
          title: drawn?.querySelector('h1.coordinator-title')?.textContent ?? null,
          source: drawn?.querySelector('p.coordinator-source')?.textContent ?? null,
          floor: drawn?.querySelector('p.coordinator-floor')?.textContent ?? null,
          floorWord: drawn?.querySelector('span.coordinator-floor-word')?.textContent ?? null,
          floorVersion: drawn?.querySelector('span.coordinator-floor-version')?.textContent ?? null,
          busyLabels: drawn?.querySelectorAll('.console-busy-label').length ?? -1,
          slots: drawn?.querySelectorAll('div.console-busy-slot').length ?? -1,
          skeletons: drawn?.querySelectorAll('.console-skeleton').length ?? -1,
          canvas: drawn?.querySelectorAll('svg.coordinator-graph').length ?? -1,
          boxes: drawn?.querySelectorAll('g.coordinator-node').length ?? -1,
          refusals: drawn?.querySelectorAll('.unavailable-operation').length ?? -1,
          destinations: drawn?.querySelectorAll('a').length ?? -1,
        }
      },
      { button: tab('coordinator'), view: THE_VIEW },
    )
    expect(inTheFrame).toEqual({
      mounted: true,
      title: COORDINATOR_TITLE,
      source: COORDINATOR_SOURCE_LINE,
      floor: `${COORDINATOR_FLOOR_WORD}${COORDINATOR_FLOOR_VERSION}${COORDINATOR_FLOOR_AND_WORD}${CLI_VERSION_BUILT_AGAINST}`,
      floorWord: COORDINATOR_FLOOR_WORD,
      floorVersion: COORDINATOR_FLOOR_VERSION,
      busyLabels: 0,
      slots: 0,
      skeletons: 0,
      canvas: 0,
      boxes: 0,
      refusals: 0,
      destinations: 0,
    })
    await expect(page.locator(`.${COORDINATOR_TITLE_CLASS}`)).toHaveText(COORDINATOR_TITLE)
    await expect(page.locator(`.${COORDINATOR_SOURCE_CLASS}`)).toHaveText(COORDINATOR_SOURCE_LINE)
    // The sentence is four runs and two of its classes repeat: the console's own
    // word, the release the read needs, the word joining the two, and the CLI in
    // hand -- which is the version the handshake answered.
    await expect(page.locator(`.${COORDINATOR_FLOOR_WORD_CLASS}`)).toHaveText([
      COORDINATOR_FLOOR_WORD,
      COORDINATOR_FLOOR_AND_WORD,
    ])
    await expect(page.locator(`.${COORDINATOR_FLOOR_VERSION_CLASS}`)).toHaveText([
      COORDINATOR_FLOOR_VERSION,
      CLI_VERSION_BUILT_AGAINST,
    ])
    // And it is a sentence rather than a refusal: no read was made, so there is
    // no operation cell on the tab at all.
    await expect(page.locator(`.${COORDINATOR_FLOOR_CLASS}`)).toHaveCount(1)
  })

  test('draws none of the v2 tab, in a real window', async () => {
    // D3210: the action queue, the fleet's totals, the job list and the
    // transcript -- with their four reads -- are gone. What a real window adds to
    // `coordinator.test.ts` is the entry beside the screen: a leftover section
    // the entry drew, or a rule that styled one, would be invisible to a unit
    // case about `renderCoordinator`.
    for (const hook of THE_V2_HOOKS) {
      await expect(page.locator(`${THE_VIEW} ${hook}`)).toHaveCount(0)
    }
    // The tab is still the tab: the v2 hooks are gone and its own header is
    // there, which is what makes the absences above an answer rather than a
    // panel that drew nothing at all.
    await expect(page.locator(`.${COORDINATOR_TITLE_CLASS}`)).toHaveText(COORDINATOR_TITLE)
  })
})
