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
// **What the frame is: the mount, not the page.** The golden is taken of
// `#live-mount`, the host this spec draws its screen into, and never of the
// whole window. The window's own entry (Task 3.4c) draws a *second* dashboard
// beside this mount, out of the live CLI, and two of that drawing's strings can
// never be identical in two runs:
//
//   - the installation header draws the active installation's **absolute binary
//     path** (`drawInstallation` in `src/renderer/entry.ts`), which is this
//     spec's own scratch directory -- a `mkdtemp` name no two runs share, on any
//     machine, so a committed baseline could not match the run that compares
//     against it (and would not match a second machine's either);
//   - the Live screen the entry draws carries the console's own reading of when
//     the last frame **arrived** (`live-since-at` and `live-since-age`, from
//     `feedHealthOf(envelope['receivedAt'], nowStamp())`), which is a reading of
//     the console's wall clock and not of any document's timestamps.
//
// Both are the product behaving correctly -- the console stamping a frame's
// arrival with its own clock is what makes the 45 s stale condition mean
// anything -- and neither is this spec's subject: the reading tests above take
// every reading *inside* the mount for the same reason. The screen under test is
// drawn from the fixture and from nothing else, and every time-derived label on
// it is measured against the fixture's own clock (the document's `now`): the
// burn history is observed at that stamp (`history.observe` is given
// `at: document.now`) and the burn window is drawn at the stamp
// `buildLiveModel` names for it (`let at = document.now`), which is the
// document's own `now` so long as the feed is not stale -- and this spec's mount
// passes no feed at all, so `at` is always the document's own. A run's age is
// the wire's own `age_seconds`, not a subtraction from `now`. Its frame is
// therefore the same pixels on every run and on every machine that draws the
// same fonts.
//
// Nothing here reaches the network or the CLI: the document is the committed
// synthetic fixture, read from disk, and every string it holds was already
// redacted when the fixture was written (Task 1.4).

import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { loadFixture } from '../../src/source/fixture'
import type { JSONDoc } from '../../src/source/source'
import { LIVE_THEMES } from '../../src/renderer/screens/live'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

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
 * the model from these documents **in order, through one `BurnHistory`** -- which
 * is what a live screen is, a window kept across frames rather than a reading of
 * one (design section 6.3) -- and mount what the last of them draws. One document
 * is the usual case; a sequence is how a test can see the burn window itself,
 * whose bars depend on the clock the model was built at. */
function draw(frames: readonly JSONDoc[], theme: string): string {
  return `(function () {
${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}
var live = load('/renderer/screens/live.js');
var schema = load('/shared/schema.js');
var history = new live.BurnHistory();
var factory = domFactory();
var tree = null;
var frames = ${JSON.stringify(frames)};
for (var i = 0; i < frames.length; i++) {
  var decoded = schema.decodeSnapshot(frames[i], 'the fixture frame');
  if (!decoded.ok) return { error: decoded.skew.because };
  var model = live.buildLiveModel(decoded.value, history);
  if (i === frames.length - 1) tree = live.renderLive(factory, model, ${JSON.stringify(theme)});
}
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
  /** each sparkline bar's own height, as drawn (`data-height`): the reading a
   * clock that moved the burn window's cut would change */
  readonly sparkHeights: readonly (string | null)[]
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
      sparkHeights: attributes('.live-burn-bar', 'data-height'),
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

/** THE_MOUNT is where this spec draws the screen, and therefore what the goldens
 * frame: the mount and never the page (see this file's header). */
const THE_MOUNT = '#live-mount'

/** atAClock is one frame with its own `now` -- the document's clock, the stamp
 * this screen measures its burn window at -- moved by `seconds`. It is how a
 * test changes "what time the document says it is" without touching the wall
 * clock the spec runs on: a fixture is replayed, and a replay is read at the
 * timestamps it carries, so moving the document's own clock is the only way a
 * drawing derived from it can be made to move. */
function atAClock(frame: JSONDoc, seconds: number): JSONDoc {
  const now = frame['now']
  if (typeof now !== 'string') throw new Error('the frame carries no clock reading')
  const moved = Date.parse(now) + seconds * 1000
  if (Number.isNaN(moved)) throw new Error(`the frame's clock reading is not a stamp: ${now}`)
  return { ...frame, now: new Date(moved).toISOString().replace(/\.\d{3}Z$/, 'Z') }
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
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
  })

  test.afterAll(async () => {
    await app?.close()
    cli.remove()
  })

  /** mount draws these documents -- one, or a sequence observed through one
   * `BurnHistory` -- and fails with the window's own words if the console's
   * decoder refused any of them. */
  async function mount(document: JSONDoc | readonly JSONDoc[], theme: string): Promise<void> {
    const frames = Array.isArray(document) ? document : [document]
    const result = await app.evaluate(async ({ BrowserWindow }, source) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(source, true)) as { error?: string }
    }, draw(frames, theme))
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
    await expect(page.locator(THE_MOUNT)).toHaveScreenshot('live-light.png')
  })

  test('draws the live fixture in the dark theme', async () => {
    await mount(theLastFrame(), 'dark')
    const drawn = await screen(page)
    expect(drawn.theme).toBe('dark')
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await expect(page.locator(THE_MOUNT)).toHaveScreenshot('live-dark.png')
  })

  test('windows the burn sparkline at the document\'s own clock', async () => {
    // The clock seam, proved in the window rather than read for. What this
    // screen draws from a clock is the burn window: `sparkline` is a rate over
    // the last `BurnWindow` **ending at the stamp its caller names**, and the
    // stamp this screen names is `at` (`screens/live.ts`:
    // `sparkline(history, repo.slug, at)`), which is the document's own `now`
    // (`let at = document.now`) except for a stale feed, where it moves to the
    // gap's stamp -- and this spec's mount passes no feed, so `at` is the
    // document's `now` throughout. The history is observed at that same stamp,
    // so two frames observed through one history and drawn at their own clock
    // are the fixture's picture of that window; the same two frames with the
    // last document's clock moved an hour on are drawn at a window that has lost
    // the earlier of the two samples and still holds that last document's own.
    //
    // Mutation: a window cut at the renderer's wall clock instead (a `Date.now()`
    // in `BurnHistory.window` or at its call site) fails the FIRST assertion
    // below. The fixture's stamps are three days old, so both mounts window to
    // nothing, and a window with no samples is not an empty chart: `spark`
    // draws the one mark it draws when no document has been seen at all
    // (`screens/live.ts`, the `line.bars.length === 0` branch), which carries no
    // `data-height`. So `asRecorded.sparkBars` is 1, not 2, and the
    // `sparkHeights.length` comparison further down is `1 < 1` rather than
    // `1 < 2`. The re-mount assertions at the end still pass under that mutation
    // -- the two mounts there are both the same empty mark -- which is exactly
    // why the count and the height the window keeps are asserted first.
    const observed = theFrames().slice(-2)
    expect(observed).toHaveLength(2)

    await mount(observed, 'light')
    const asRecorded = await screen(page)
    // The fixture's last two documents are one second apart (12:04:20Z and
    // 12:04:21Z), so both samples are inside the window ending at the frame's own
    // clock: two bars, each with the height it was measured at.
    expect(asRecorded.sparkBars).toBe(2)

    // The same documents with the last one's own clock an hour later: the window
    // it is drawn at ends at `13:04:21Z`, so its cut is `12:59:21Z`, the sample
    // the earlier document stamped at `12:04:20Z` is outside it, and the sample
    // the LAST document stamped at `13:04:21Z` -- the drawing frame's own -- is
    // still inside it. One bar, and it is that sample's measured height: what a
    // window cut anywhere else loses is a MEASURED bar, never one of the marks
    // for a sample that never arrived.
    await mount([observed[0] as JSONDoc, atAClock(observed[1] as JSONDoc, 60 * 60)], 'light')
    const anHourLater = await screen(page)
    // The surviving bar is pinned, not just counted: a bar drawn for a sample
    // that is not there is a gap mark (`—`, `formatReading`'s `GAP_MARK`), and a
    // window with no samples at all is one bar too, so a count alone would read
    // the same for the reading this test is about and for both of those.
    expect(anHourLater.sparkBars).toBe(1)
    expect(anHourLater.sparkHeights).toEqual([asRecorded.sparkHeights[1]])
    expect(anHourLater.sparkHeights.length).toBeLessThan(asRecorded.sparkHeights.length)

    // And the same two frames, at the same clock reading, draw the same screen
    // twice: the frame a golden is taken from is a function of the documents and
    // of nothing else -- which is what makes a committed baseline comparable at
    // all.
    await mount(observed, 'light')
    const again = await screen(page)
    expect(again.sparkHeights).toEqual(asRecorded.sparkHeights)
    expect(again.text).toBe(asRecorded.text)
    expect(again.runs).toEqual(asRecorded.runs)
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
