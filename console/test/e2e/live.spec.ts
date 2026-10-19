// The Live screen in a real window (desktop-console plan Task 2.1; design
// sections 6.1 rules 2 and 3, 6.2 screen 1).
//
// `live.test.ts` asserts the model and the tree the screen builds; this file
// asserts that the same screen draws in the console's own hardened window, with
// the real CSP in force, and takes a golden per theme.
//
// **Why the screen is drawn through `executeJavaScript` rather than by a script
// tag or a page.** Task 2.1's file list is the screen and its two tests: the
// entry point (`src/renderer/index.html`, `src/renderer/app.js`) and the place
// the compiled script is *served from* (`src/main/index.ts`'s `rendererRoot`)
// belong to another task, and neither is this one's to edit. So the spec does
// what the app will do, from outside the app: it loads the compiled renderer
// out of `dist/` (the output `npm run build` already produces) and calls
// `renderLive` with a DOM-backed element factory of its own.
//
// Two mechanisms are worth naming, because both are deliberate:
//
//   - The modules are loaded through `webContents.executeJavaScript`, which
//     Electron runs in the page's main world **outside the page's CSP**. An
//     inline `<script>` or `addScriptTag({content})` would be refused by
//     `script-src 'self'`, and Playwright's `page.evaluate` would be refused
//     too the moment the injected code used `new Function` -- so the bundle is
//     assembled in Node and handed over as one expression.
//   - The bundle is CommonJS, because that is what `tsc -p tsconfig.build.json`
//     emits, and it is loaded by a twenty-line registry below rather than by
//     `require`: the renderer has no `require`, and giving the page one would
//     be giving a window with `connect-src 'none'` a Node builtin.
//
// **The goldens.** `test/e2e/__screenshots__/live-{light,dark}-<platform>.png`
// (one per theme per platform: `darwin` and `linux`, because macOS and Linux
// pixels differ and CI runs both) are compared, never written by a run: `playwright.config.ts` sets
// `updateSnapshots: 'none'`, so a missing baseline FAILS (Playwright's
// `'missing'` would write one and pass, comparing nothing). They are recorded by
// an owner, once on each platform the comparison runs on, with
// `npx playwright test live.spec.ts --update-snapshots`, and the
// three PNG-writing and PNG-reading tests below are the only ones that need
// them. Until they are committed those tests fail, on purpose. The one way to
// run the rest of the file without them is the explicit
// `FUGARO_CONSOLE_GOLDENS=skip`, which skips exactly the golden tests and says
// so in the report; nothing skips them silently.
//
// Nothing here reaches the network or the CLI: the document is the committed
// synthetic fixture, read from disk, and every string it holds was already
// redacted when the fixture was written (Task 1.4).

import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { _electron as electron, expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { loadFixture } from '../../src/source/fixture'
import type { JSONDoc } from '../../src/source/source'
import { LIVE_THEMES } from '../../src/renderer/screens/live'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'

// The console's own directory, from this file's location: `test/e2e/x.spec.ts`
// is two levels below it.
const consoleDir = join(__dirname, '..', '..')
const fixturePath = join(consoleDir, 'test', 'fixtures', 'synthetic.ndjson')
const goldenDir = join(consoleDir, 'test', 'e2e', '__screenshots__')

/** SKIP_GOLDENS is the one documented way to run this file with no goldens
 * recorded: `FUGARO_CONSOLE_GOLDENS=skip`. Anything else, including unset, means
 * the golden tests run and a missing baseline fails them. */
const SKIP_GOLDENS = process.env['FUGARO_CONSOLE_GOLDENS'] === 'skip'

/** The compiled modules the screen is made of, in dependency order. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/version.js',
  'shared/schema.js',
  'renderer/app.js',
  'renderer/screens/live.js',
])

/** moduleSource is one compiled module as a registry entry. */
function moduleSource(relative: string): string {
  const code = readFileSync(join(consoleDir, 'dist', relative), 'utf8')
  return `registry[${JSON.stringify(`/${relative}`)}] = function (exports, require, module) {\n${code}\n};`
}

/**
 * LOADER is the module registry the injected bundle runs on: a CommonJS
 * `require` that can resolve `./` and `../` specifiers and nothing else. A
 * specifier that is not a relative path throws, so a bundle that grew an import
 * of `node:` or of a package fails here rather than in a window with no Node in
 * it.
 */
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
 * FACTORY is a DOM-backed `ScreenFactory`: one wrapper per element, so the
 * screen's `ScreenElement` (a text slot, attributes and children) is served by
 * a real node while staying exactly as narrow as the interface the unit tests
 * hand it. `node` is the harness's own handle on the element and is never
 * visible to the screen.
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
    wrap: wrap,
  };
}
`

/** draw is the expression the window evaluates: load the compiled screen, build
 * the model from one document, and mount what it draws. */
function draw(document: JSONDoc, theme: string): string {
  return `(function () {
${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}
var live = load('/renderer/screens/live.js');
var schema = load('/shared/schema.js');
var decoded = schema.decodeSnapshot(${JSON.stringify(document)}, 'the fixture frame');
if (!decoded.ok) return { error: decoded.skew.because };
var factory = domFactory();
var tree = live.renderLive(factory, live.buildLiveModel(decoded.value, new live.BurnHistory()), ${JSON.stringify(theme)});
var previous = document.getElementById('live-mount');
if (previous) previous.remove();
var host = document.createElement('div');
host.id = 'live-mount';
host.appendChild(tree.node);
document.body.appendChild(host);
return { mounted: true };
})()`
}

/** Screen is the part of the drawn screen a test asserts on, read back out of
 * the live document. */
interface Screen {
  readonly theme: string | null
  readonly project: string | null
  readonly mode: string | null
  readonly day: string | null
  readonly connection: string | null
  readonly repos: readonly string[]
  readonly counts: readonly string[]
  readonly bars: readonly (string | null)[]
  readonly runs: readonly string[]
  readonly stages: readonly string[]
  readonly queued: readonly (string | null)[]
  readonly killed: readonly (string | null)[]
  readonly killText: string | null
  readonly flags: readonly string[]
  readonly sparkBars: number
  readonly anchors: number
  readonly text: string
}

/** screen reads the mounted screen out of the page. */
function screen(page: Page): Promise<Screen> {
  return page.evaluate(() => {
    // The window's own entry draws the real dashboard into the same document
    // (Task 3.4c), beside this spec's mount: every reading is taken inside the
    // mount, so the entry's drawing can neither satisfy nor fail an assertion.
    const scope: ParentNode = document.getElementById('live-mount') ?? document.createDocumentFragment()
    const all = (selector: string): HTMLElement[] =>
      Array.from(scope.querySelectorAll<HTMLElement>(selector))
    const texts = (selector: string): string[] => all(selector).map((one) => one.textContent ?? '')
    const attributes = (selector: string, name: string): (string | null)[] =>
      all(selector).map((one) => one.getAttribute(name))
    return {
      theme: scope.querySelector('.live')?.getAttribute('data-theme') ?? null,
      project: scope.querySelector('.live-project')?.textContent ?? null,
      mode: scope.querySelector('.live-mode')?.textContent ?? null,
      day: scope.querySelector('.live-day')?.textContent ?? null,
      connection: scope.querySelector('.live-connection')?.textContent ?? null,
      repos: texts('.live-repo-name'),
      counts: texts('.live-repo-counted'),
      bars: attributes('.live-bar', 'data-fill'),
      runs: texts('.live-run-id'),
      stages: texts('.live-run-stage'),
      queued: attributes('.live-run', 'data-queued'),
      killed: attributes('.live-repo', 'data-killed'),
      killText: scope.querySelector('.live-kill')?.textContent ?? null,
      flags: texts('.live-run-flag'),
      sparkBars: all('.live-burn-bar').length,
      anchors: all('a').length,
      text: document.getElementById('live-mount')?.textContent ?? '',
    }
  })
}

/** theFrames is the committed fixture's documents, in order, exactly as the
 * recorder wrote them. */
function theFrames(): readonly JSONDoc[] {
  const loaded = loadFixture(readFileSync(fixturePath, 'utf8'), 'test/fixtures/synthetic.ndjson')
  const out: JSONDoc[] = []
  for (const record of loaded.records) {
    if ('document' in record) out.push(record.document)
  }
  return out
}

/** theLastFrame is the fixture's absent-field frame: the repository at its cap,
 * a lost run beside a queued one. */
function theLastFrame(): JSONDoc {
  const frames = theFrames()
  const last = frames[frames.length - 1]
  if (last === undefined) throw new Error('the fixture has no documents')
  return last
}

/** withAKill is the last frame with its repository's switch thrown, which the
 * synthetic fixture deliberately does not carry: the design's "a killed
 * repository shows KILLED by <who> <time> \"<reason>\"" is asserted here rather
 * than hoped for in a recording. */
function withAKill(): JSONDoc {
  const frame = theLastFrame()
  // `JSONDoc` is the CLI's document as it arrived -- one JSON object, undecoded
  // (`src/source/source.ts`) -- so the two keys this spec edits are read out of
  // it by name and checked for shape rather than assumed.
  const repos = frame['repos']
  if (!Array.isArray(repos)) throw new Error('the frame has no repositories')
  return {
    ...frame,
    repos: repos.map((repo: unknown) => {
      if (repo === null || typeof repo !== 'object') throw new Error('a repository is not an object')
      return { ...(repo as Record<string, unknown>), kill: { by: 'operator', at: '2026-10-15T12:04:00Z', reason: 'runaway spend' } }
    }),
  }
}

test.describe('the Live screen', () => {
  // One window, one page, and the tests in order: each draws the screen it is
  // about into the same document, and the goldens are taken from what the test
  // before them mounted.
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // The app reads a `fugaro` before it opens a window (plan Task 3.3): without
    // an in-range one it opens the refusal document and this spec would be
    // mounting screens into a page that is not the dashboard's.
    cli = inRangeCliStub()
    // `args: [consoleDir]` is the app directory: Electron reads its package.json
    // and runs `main` (`dist/main/index.js`, built by `npm run test:e2e`).
    // A scratch profile with the stub as the active installation (Task 3.4b):
    // the dashboard is this spec's subject, so the first run's picker -- which
    // opens when no installation is chosen -- is not, and no spec touches the
    // machine's own console profile.
    cli.seedActiveInstallation()
    app = await electron.launch({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
  })

  test.afterAll(async () => {
    await app.close()
    cli.remove()
  })

  /** mount draws one document and fails with the window's own words if the
   * console's decoder refused it. */
  async function mount(document: JSONDoc, theme: string): Promise<void> {
    const result = await app.evaluate(async ({ BrowserWindow }, source) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(source, true)) as { error?: string }
    }, draw(document, theme))
    expect(result.error).toBeUndefined()
  }

  test('renders the fixture in the window, under the real CSP', async () => {
    await mount(theLastFrame(), 'light')
    const drawn = await screen(page)

    expect(drawn.theme).toBe('light')
    // The project name is the fixture's own `<project>` marker: the recorder
    // replaced the installation's name with it before the file was committed.
    expect(drawn.project).toBe('<project>')
    expect(drawn.mode).toBe('observe')
    expect(drawn.day).toBe('2026-10-15')
    expect(drawn.connection).toBe('live')
    expect(drawn.repos).toHaveLength(1)
    expect(drawn.counts).toEqual(['$50.00'])
    expect(drawn.sparkBars).toBeGreaterThan(0)
    // No link is built on this screen (design section 4.3's gap 7), so the
    // window has nothing to click.
    expect(drawn.anchors).toBe(0)
  })

  test('shows a killed repository\'s banner', async () => {
    await mount(withAKill(), 'light')
    const drawn = await screen(page)

    expect(drawn.killed).toEqual(['true'])
    expect(drawn.killText).toContain('KILLED by')
    expect(drawn.killText).toContain('runaway spend')
  })

  test('shows the bucket side\'s queued row', async () => {
    await mount(theLastFrame(), 'light')
    const drawn = await screen(page)

    expect(drawn.runs).toContain('20261015-120400-aa03')
    expect(drawn.queued).toContain('true')
    expect(drawn.stages).toContain('queued')
    // A queued run's spend is `null` on the wire, so the screen draws the
    // console's mark for "not known" rather than a zero.
    expect(drawn.text).toContain('n/a')
  })

  test('draws the live fixture', async () => {
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await mount(theLastFrame(), 'light')
    await expect(page).toHaveScreenshot('live-light.png')
  })

  test('draws the live fixture in the dark theme', async () => {
    await mount(theLastFrame(), 'dark')
    const drawn = await screen(page)
    expect(drawn.theme).toBe('dark')
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await expect(page).toHaveScreenshot('live-dark.png')
  })

  test('has a golden for each theme the design names', () => {
    // A theme with no golden is a theme nobody has looked at. The themes are
    // the screen's own, and the directory is read, so a third palette -- or a
    // PNG that was never committed -- fails here.
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    // One per theme for THIS platform, and nothing that is not a theme's
    // recording for one of the two platforms CI runs.
    const present = existsSync(goldenDir) ? readdirSync(goldenDir).filter((f) => f.endsWith('.png')).sort() : []
    expect(present.filter((f) => f.endsWith(`-${process.platform}.png`))).toEqual(
      LIVE_THEMES.map((theme) => `live-${theme}-${process.platform}.png`).sort(),
    )
    for (const file of present) expect(file).toMatch(new RegExp(`^live-(${LIVE_THEMES.join('|')})-(darwin|linux)\\.png$`))
  })
})
