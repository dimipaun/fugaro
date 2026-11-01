// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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
//
// **The goldens** are the second describe: `test/e2e/__screenshots__/coordinator/{light,dark}-<platform>.png`,
// taken the way `spend.spec.ts` takes its own -- the compiled screen loaded out
// of `dist/` into the window's own document and mounted into a host of this
// spec's making, so what a golden frames is the screen and not the window's
// entry beside it. The name is passed to `toHaveScreenshot` as an ARRAY
// (`['coordinator', 'light.png']`), because Playwright sanitizes a STRING name
// before `snapshotPathTemplate` applies and would flatten
// `'coordinator/light.png'` into `__screenshots__/coordinator-light.png` beside
// the Live screen's two files.
//
// Nothing on this screen moves or is read from a clock -- `COORDINATOR_NO_AGE_WORD`
// is the console's own sentence about the time the stored view does not carry --
// so the frame is a function of the committed document alone and needs no
// emulation to freeze it. **No PNG is committed**, as for the other five
// screens: macOS and Linux pixels differ, and the baselines are owner-run.

import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import {
  COORDINATOR_BUSY,
  COORDINATOR_NO_AGE_WORD,
  COORDINATOR_SOURCE_LINE,
  COORDINATOR_THEMES,
} from '../../src/renderer/screens/coordinator'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { expectGolden, expectGrounded } from '../support/goldens'
import { launchConsole } from '../support/launch'

// `test/e2e/x.spec.ts` is two levels below the console's own directory.
const consoleDir = join(__dirname, '..', '..')

/** COORDINATOR_FIXTURE is the committed `coordinator status --from-store --all
 * --json` document: the stub's own answer, and the document this spec's goldens
 * are drawn from. */
const COORDINATOR_FIXTURE = join(consoleDir, 'test', 'fixtures', 'coordinator-status.json')

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

/** The compiled modules the screen is made of, in dependency order: the screen,
 * the writers in `renderer/app.js`, and the shared modules those reach.
 * `shared/schema.js` is here because the fixture is decoded in the window rather
 * than in this file: the screen is handed a document the console's own decoder
 * admitted, which is the shape the entry hands it. Nothing else may be
 * reachable -- the registry below refuses a specifier that is not a relative
 * path, so a bundle that grew an import of `node:` or of a package fails here
 * rather than in a window with no Node in it. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/notice.js',
  'shared/version.js',
  'shared/schema.js',
  'renderer/app.js',
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

/** FACTORY is a DOM-backed `ScreenFactory`: one wrapper per element, so the
 * screen's own narrow interface is served by a real node. `node` is the
 * harness's handle on the element and is never visible to the screen. */
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
    wrap: wrap,
  };
}
`

/** draw is the expression the window evaluates: decode the committed fixture
 * through the console's own door, build the panels from it, and mount what the
 * screen draws. */
function draw(theme: string): string {
  const document = JSON.parse(readFileSync(COORDINATOR_FIXTURE, 'utf8')) as unknown
  return `(function () {
${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}
var schema = load('/shared/schema.js');
var coordinator = load('/renderer/screens/coordinator.js');
var decoded = schema.decodeCoordinatorStatus(${JSON.stringify(document)}, 'the committed coordinator document');
if (!decoded.ok) return { error: 'the decoder refused the fixture: ' + decoded.skew.because };
var factory = domFactory();
var model = coordinator.buildCoordinatorModel({ view: decoded.value });
var tree = coordinator.renderCoordinator(factory, model, ${JSON.stringify(theme)});
var previous = document.getElementById('coordinator-mount');
if (previous) previous.remove();
var host = document.createElement('div');
host.id = 'coordinator-mount';
host.appendChild(tree.node);
document.body.appendChild(host);
return { mounted: true };
})()`
}

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

test.describe('the Coordinator screen', () => {
  // One window, and the tests in order: each draws the screen it is about into
  // the same document, and the goldens are taken from what the test before them
  // mounted.
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // A scratch profile with the stub as the active installation, as the cases
    // above: without one the window opens on the picker and the document this
    // spec mounts into would be a document with no stylesheet to paint it.
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
  async function drawn(): Promise<{ readonly theme: string | null; readonly repos: number; readonly records: number }> {
    return page.evaluate(() => {
      const scope: ParentNode = document.getElementById('coordinator-mount') ?? document.createDocumentFragment()
      return {
        theme: scope.querySelector('section.coordinator')?.getAttribute('data-theme') ?? null,
        repos: scope.querySelectorAll('section.coordinator-repo').length,
        records: scope.querySelectorAll('li.coordinator-record').length,
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
      themes: COORDINATOR_THEMES,
      mount: THE_MOUNT,
      text: 'h2.coordinator-repo-name',
      screen: 'the Coordinator screen',
    })
  })

  test('draws the coordinator screen', async ({}, testInfo) => {
    await mount('light')
    // The frame is worth recording only if the document reached it: the screen
    // draws an empty-state sentence when the installation lists no repository,
    // which would be a golden of a sentence.
    const light = await drawn()
    expect(light.theme).toBe('light')
    expect(light.repos, 'the committed document draws no repository panel').toBeGreaterThan(0)
    expect(light.records, 'the committed document draws no record row').toBeGreaterThan(0)
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await expectGolden(page.locator(THE_MOUNT), GOLDEN_NAMES.light, { spec: SPEC, consoleDir, testInfo })
  })

  test('draws the coordinator screen in the dark theme', async ({}, testInfo) => {
    await mount('dark')
    const dark = await drawn()
    expect(dark.theme).toBe('dark')
    expect(dark.repos, 'the committed document draws no repository panel').toBeGreaterThan(0)
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
// The Coordinator view in the dashboard, reading through the stub CLI.
// ---------------------------------------------------------------------------

/** THE_PANEL is the dashboard's own panel, which is where this view is drawn:
 * the describe above mounts a screen of its own into `#coordinator-mount`, so a
 * bare `section.coordinator` would be ambiguous between the two. */
const THE_PANEL = 'div.console-panel'

/** THE_VIEW is the dashboard's Coordinator view, and the scope every reading
 * below is taken in. */
const THE_VIEW = `${THE_PANEL} section.coordinator`

/** THE_BODY is the section the read belongs to: the element the busy mark is put
 * on, and the one the summary and each repository's detail are filled into. */
const THE_BODY = `${THE_VIEW} div.coordinator-body`

/** tab is one button of the dashboard's navigation. */
function tab(view: string): string {
  return `nav.console-views button.console-view[data-view="${view}"]`
}

test.describe('the Coordinator view in the dashboard', () => {
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // A window of its own, and **the first visit to this tab in it**: the second
    // visit inside five minutes is served from the session cache and paints at
    // once, which is the state the case below is not about.
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

  test('draws what is true before the answer in the frame the tab was opened in, and fills the body after', async () => {
    // **The click's own frame.** The read behind this view is the longest of the
    // three tabs -- about 724 KB on the owner's installation -- so a screen that
    // waited for it answered a click with nothing at all (the owner's report of
    // 2026-10-31). What is asserted here is read from the frame the click
    // happened in, before any answer can have arrived: the header the screen can
    // state without a document, the mark naming the read, the shape that is
    // coming, and the absence of every record.
    const inTheFrame = await page.evaluate(
      ({ button: selector, view: viewSelector, body: bodySelector }) => {
        document.querySelector<HTMLElement>(selector)?.click()
        const view = document.querySelector(viewSelector)
        const body = document.querySelector(bodySelector)
        return {
          mounted: view !== null,
          // The two sentences drawn before the read answers: which command the
          // panel is reading, and that the console claims no age for the view.
          source: view?.querySelector('p.coordinator-source')?.textContent ?? null,
          age: view?.querySelector('p.coordinator-age')?.textContent ?? null,
          busy: body?.getAttribute('aria-busy') ?? null,
          marked: body?.hasAttribute('data-busy') ?? null,
          label: body?.querySelector('.console-busy-label')?.textContent ?? null,
          spinner: body?.querySelector('.console-busy-spinner') !== null,
          skeleton: body?.querySelector('.console-skeleton') !== null,
          records: view?.querySelectorAll('li.coordinator-record').length ?? -1,
        }
      },
      { button: tab('coordinator'), view: THE_VIEW, body: THE_BODY },
    )
    expect(inTheFrame).toEqual({
      mounted: true,
      source: COORDINATOR_SOURCE_LINE,
      age: COORDINATOR_NO_AGE_WORD,
      busy: 'true',
      marked: true,
      label: COORDINATOR_BUSY,
      spinner: true,
      skeleton: true,
      records: 0,
    })

    // Then the body: the summary and the records the stub's committed document
    // carries, filled into the same section, under the header that was already
    // there.
    await expect(page.locator(`${THE_VIEW} li.coordinator-record`).first()).toBeVisible()
    await expect(page.locator(`${THE_VIEW} div.coordinator-detail`).first()).toBeVisible()
    await expect(page.locator(THE_BODY)).not.toHaveAttribute('aria-busy', 'true')
    await expect(page.locator(`${THE_VIEW} p.coordinator-source`)).toHaveText(COORDINATOR_SOURCE_LINE)
  })
})
