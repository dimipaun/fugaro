// The version skew handshake in a real window (desktop-console plan Task 3.2;
// design section 7.1 and D7).
//
// The console is a client of an interface it does not own, and this is the spec
// that holds the consequence in the console's own document, under its own CSP:
// the console reads `fugaro version`, and either it proceeds, or it proceeds in
// a reduced mode with a banner naming both versions and the views it cannot
// vouch for withheld, or -- below the hard floor -- it draws the refusal screen
// and **no dashboard at all**, because a dashboard that misreports spend is
// worse than one that says it cannot read this fugaro.
//
// Three things are worth knowing before reading the assertions:
//
//   - **The banner is `src/shared/version.ts`'s, not a screen's.** The skew
//     state is the console's own, so the module that compares the versions is
//     the module that draws them; `renderer/screens/spend.ts` says the same
//     thing from the other side ("Task 3.2 owns the skew state and a screen that
//     invented its own would be two"). The drawing goes through the sanitizer --
//     the version line is a string a binary chose -- and this spec is where that
//     is checked under the real policy rather than in a fake element.
//   - **`executeJavaScript` and not a page**, exactly as in `live.spec.ts` and
//     `degraded.spec.ts`: the entry point that mounts a screen on load belongs
//     to another task, so this spec loads the compiled renderer out of `dist/`
//     and calls the console's own functions with a DOM-backed factory. The
//     drawing has to go through `executeJavaScript`: an inline `<script>` is
//     refused by the console's CSP, and the module registry the bundle builds
//     only exists in the world `executeJavaScript` runs in. `page.evaluate` is
//     used for the *reading* only, exactly as in the other specs.
//   - **No goldens.** This file takes none: the plan's file list for this task
//     names no frame, and `playwright.test.ts` holds `test/e2e/__screenshots__`
//     to the Live screen's two by name, so a PNG appearing here would fail that
//     test rather than be reviewed. The screenshots for the skew states are the
//     maintainer's to record with `npx playwright test --update-snapshots` on a
//     machine with a display; this file asserts the drawn tree instead, which is
//     what can be asserted on every machine.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { _electron as electron, expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import type { JSONDoc } from '../../src/source/source'
import { loadFixture } from '../../src/source/fixture'
import {
  CLI_HARD_FLOOR_VERSION,
  CLI_VERSION_BUILT_AGAINST,
  HIDDEN_VIEW_WORDS,
  REDUCED_VIEWS,
  SKEW_HEADLINES,
  SUPPORTED_CLI_MINORS,
  UNREADABLE_VERSION,
  classifyCliVersion,
  hidesView,
  skewSentence,
} from '../../src/shared/version'
import { githubToken } from '../support/fakeSecrets'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'

// The console's own directory, from this file's location: `test/e2e/x.spec.ts`
// is two levels below it.
const consoleDir = join(__dirname, '..', '..')

/** MODULES are the compiled modules this spec needs, in dependency order:
 * `version.js` for the handshake itself, `live.js` for the dashboard the
 * below-floor case must not draw -- and whose presence here is what makes the
 * "no dashboard" assertion a statement about a screen that *could* have been
 * drawn rather than about a selector nothing ever fills in. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/schema.js',
  'shared/version.js',
  'renderer/app.js',
  'renderer/screens/live.js',
])

/** moduleSource is one compiled module as a registry entry. */
function moduleSource(relative: string): string {
  const code = readFileSync(join(consoleDir, 'dist', relative), 'utf8')
  return `registry[${JSON.stringify(`/${relative}`)}] = function (exports, require, module) {\n${code}\n};`
}

/** LOADER is the module registry the injected bundle runs on: a CommonJS
 * `require` that resolves `./` and `../` specifiers and nothing else, so a
 * bundle that grew an import of `node:` or of a package fails here rather than
 * in a window with no Node in it. Unchanged from `live.spec.ts` and
 * `degraded.spec.ts`, deliberately: this is the same window and the same
 * constraint -- and it is why `shared/version.js` carries the handshake without
 * reaching for anything outside the bundle. */
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
function mount(tree) {
  var previous = document.getElementById('console-mount');
  if (previous) previous.remove();
  var host = document.createElement('div');
  host.id = 'console-mount';
  host.appendChild(tree.node);
  document.body.appendChild(host);
}
`

/** FACTORY is a DOM-backed factory: one wrapper per element, so what the
 * console draws is served by a real node while staying exactly as narrow as the
 * interface the unit tests hand it. Unchanged from `live.spec.ts`. */
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

/** bundle wraps one drawing body in the harness the window evaluates. */
function bundle(body: string): string {
  return `(function () {\n${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}${body}\n})()`
}

/**
 * drawSkew places a version line **with the console's own handshake** and draws
 * what it produces: the real `classifyCliVersion` and the real
 * `renderVersionSkew`, out of the compiled bundle, in the window. Nothing here
 * re-implements the comparison, so an assertion about the boundary in this file
 * is an assertion about the shipped code.
 */
function drawSkew(line: string, theme: string): string {
  return bundle(`
var version = load('/shared/version.js');
var compatibility = version.classifyCliVersion(${JSON.stringify(line)});
mount(version.renderVersionSkew(domFactory(), compatibility, ${JSON.stringify(theme)}));
return {
  mode: compatibility.mode,
  skew: compatibility.skew,
  cliVersion: compatibility.cliVersion,
  consoleVersion: compatibility.consoleVersion,
  hidden: compatibility.hidden,
  hidesSpend: version.hidesView(compatibility, 'spend'),
  hidesCaps: version.hidesView(compatibility, 'caps'),
  hidesKill: version.hidesView(compatibility, 'kill'),
  sentence: version.skewSentence(compatibility)
};
`)
}

/**
 * drawDashboard draws the Live screen from the committed synthetic fixture:
 * the dashboard a console that ignored the handshake would draw. It is here for
 * one reason -- the below-floor test asserts that *no* dashboard is drawn, and
 * an assertion that nothing matches is only worth something if the same
 * selector finds a dashboard when there is one. This is what makes the `0`
 * below a measurement rather than a tautology.
 */
function drawDashboard(frames: readonly JSONDoc[], theme: string): string {
  return bundle(`
var schema = load('/shared/schema.js');
var live = load('/renderer/screens/live.js');
var frames = ${JSON.stringify(frames)};
var history = new live.BurnHistory();
var last = null;
for (var i = 0; i < frames.length; i++) {
  var decoded = schema.decodeSnapshot(frames[i], 'fixture frame ' + i);
  if (!decoded.ok) return { error: decoded.skew.because };
  last = decoded.value;
  live.buildLiveModel(last, history, null);
}
mount(live.renderLive(domFactory(), live.buildLiveModel(last, history, null), ${JSON.stringify(theme)}));
return { mounted: true };
`)
}

/** Drawn is the part of a mounted screen a test asserts on, read back out of
 * the live document. Every selector that is not there answers `null` or `[]`,
 * which is how "no dashboard" is asserted: nothing on the screen draws one. */
interface Drawn {
  readonly mode: string | null
  readonly skew: string | null
  readonly theme: string | null
  readonly headline: string | null
  readonly sentence: string | null
  readonly consoleVersion: string | null
  readonly cliVersion: string | null
  readonly consoleLabel: string | null
  readonly cliLabel: string | null
  readonly hidden: readonly (string | null)[]
  readonly hiddenWords: readonly string[]
  readonly sections: number
  readonly liveRoots: number
  readonly burnBars: number
  readonly caps: number
  readonly anchors: number
  readonly buttons: number
  readonly text: string
}

/** drawn reads the mounted screen out of the page. */
function drawn(page: Page): Promise<Drawn> {
  return page.evaluate(() => {
    // The window's own entry draws the real dashboard into the same document
    // (Task 3.4c), beside this spec's mount: every reading is taken inside the
    // mount, so the entry's drawing can neither satisfy nor fail an assertion.
    const scope: ParentNode = document.getElementById('console-mount') ?? document.createDocumentFragment()
    const all = (selector: string): HTMLElement[] =>
      Array.from(scope.querySelectorAll<HTMLElement>(selector))
    const root = scope.querySelector('#console-mount > section')
    return {
      mode: root?.getAttribute('data-mode') ?? null,
      skew: root?.getAttribute('data-skew') ?? null,
      theme: root?.getAttribute('data-theme') ?? null,
      headline: scope.querySelector('.skew-headline')?.textContent ?? null,
      sentence: scope.querySelector('.skew-sentence')?.textContent ?? null,
      consoleVersion: scope.querySelector('.skew-console-version')?.textContent ?? null,
      cliVersion: scope.querySelector('.skew-cli-version')?.textContent ?? null,
      consoleLabel: scope.querySelector('.skew-console-label')?.textContent ?? null,
      cliLabel: scope.querySelector('.skew-cli-label')?.textContent ?? null,
      hidden: all('.skew-hidden').map((one) => one.getAttribute('data-hidden')),
      hiddenWords: all('.skew-hidden').map((one) => one.textContent ?? ''),
      sections: all('#console-mount > section').length,
      liveRoots: all('.live').length,
      burnBars: all('.live-burn-bar').length,
      caps: all('.live-cap').length,
      anchors: all('a').length,
      buttons: all('button').length,
      text: document.getElementById('console-mount')?.textContent ?? '',
    }
  })
}

/** theFrames is the synthetic fixture's documents: the dashboard the below-floor
 * case must not draw. */
function theFrames(): readonly JSONDoc[] {
  const path = join(consoleDir, 'test', 'fixtures', 'synthetic.ndjson')
  const out: JSONDoc[] = []
  for (const record of loadFixture(readFileSync(path, 'utf8'), 'test/fixtures/synthetic.ndjson').records) {
    if ('document' in record) out.push(record.document)
  }
  if (out.length === 0) throw new Error('the synthetic fixture has no documents')
  return out
}

/**
 * THE_LINE is the captured output of a real release build, written out here the
 * same way `version.test.ts` writes it: the line `fugaro version` prints, so
 * what this spec places is a version a binary really produces and not one this
 * file invented. `version.test.ts` is where the provenance is documented
 * (`.goreleaser.yaml`'s `{{ .Version }}` into `internal/cli.Version`); this file
 * only has to place it.
 */
const THE_LINE = `${CLI_VERSION_BUILT_AGAINST}\n`

test.describe('the version skew handshake', () => {
  // One window, one page, and the tests in order: each draws the state it is
  // about into the same document, exactly as `live.spec.ts` does.
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // The app reads a `fugaro` before it opens a window (plan Task 3.3): without
    // an in-range one it opens the refusal document, and this spec -- which
    // draws every skew state into the dashboard document by hand, with the
    // compatibility it is testing -- would have no dashboard to draw into.
    cli = inRangeCliStub()
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

  /** mount draws one state and fails with the window's own words if the console
   * refused the frames it was given. */
  async function mount(source: string): Promise<Record<string, unknown>> {
    const result = await app.evaluate(async ({ BrowserWindow }, body) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(body, true)) as Record<string, unknown>
    }, source)
    expect(result['error']).toBeUndefined()
    return result
  }

  /**
   * The window's header, in a real window. Design section 3.4 rule 8 says the
   * handshake "says so in its header", and `main/window.ts` puts the mode in the
   * window's title -- but Electron applies a loaded document's `<title>` over
   * the `title` the window was constructed with, and `src/renderer/index.html`
   * defines one. So the mechanism, not just the option, is what has to hold in
   * Electron: the console refuses the page title, and the title stays its own.
   * The mode's own title (reduced, refused) is asserted in `window.test.ts`
   * against a window that models Electron's rule for exactly this reason; a
   * second window cannot be constructed from here without reaching into the
   * main process's own modules, which this spec deliberately does not do.
   */
  test('keeps the window title its own: the document cannot take the header over', async () => {
    const documentTitle = /<title>([^<]*)<\/title>/.exec(
      readFileSync(join(consoleDir, 'src', 'renderer', 'index.html'), 'utf8'),
    )?.[1]
    expect(documentTitle).toBe('Fugaro console')

    const before = await app.evaluate(({ BrowserWindow }) => {
      const window = BrowserWindow.getAllWindows()[0]
      return window === undefined
        ? null
        : { title: window.getTitle(), listeners: window.listenerCount('page-title-updated') }
    })
    expect(before?.title).toBe(documentTitle)
    // The console's denial is on the window. `>= 1` rather than `=== 1`: this
    // test is about the console's handler being there, and an Electron or
    // Playwright listener beside it is not a defect.
    expect(before?.listeners ?? 0).toBeGreaterThanOrEqual(1)

    // The document asks for a title of its own, which Electron offers to the
    // window. Without the denial the window's title becomes this string, and a
    // reduced or refused console would lose the mode it was built with.
    await page.evaluate(() => {
      document.title = 'a title the document chose'
    })
    const after = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0]?.getTitle() ?? null)
    expect(after).toBe(documentTitle)
    expect(after).not.toBe('a title the document chose')
  })

  test('draws no banner inside the supported range, and says which versions those are', async () => {
    const result = await mount(drawSkew(THE_LINE, 'light'))
    const screen = await drawn(page)

    expect(result['mode']).toBe('ok')
    expect(screen.mode).toBe('ok')
    expect(screen.skew).toBe('supported')
    expect(screen.theme).toBe('light')
    // The console's own version, and the range it is a claim about: the banner
    // names both versions, and the range is the design's "the minor it was built
    // against, plus the previous one".
    expect(screen.consoleVersion).toBe(CLI_VERSION_BUILT_AGAINST)
    expect(result['consoleVersion']).toBe(CLI_VERSION_BUILT_AGAINST)
    expect(screen.sentence).toContain(CLI_VERSION_BUILT_AGAINST)
    expect(screen.sentence).toContain(SUPPORTED_CLI_MINORS.join(', '))
    // The CLI's version reaches the screen as itself -- this is the one case
    // where the two are the same string -- and nothing is withheld.
    expect(screen.cliVersion).toBe(CLI_VERSION_BUILT_AGAINST)
    expect(screen.hidden).toEqual([])
    expect(screen.headline).toBe(SKEW_HEADLINES.ok)
  })

  test('draws the banner, naming both versions, for a CLI below the range', async () => {
    const result = await mount(drawSkew(`${CLI_HARD_FLOOR_VERSION}\n`, 'light'))
    const screen = await drawn(page)

    expect(result['mode']).toBe('reduced')
    expect(screen.mode).toBe('reduced')
    expect(screen.skew).toBe('older')
    expect(screen.headline).toBe(SKEW_HEADLINES.reduced)
    // Both versions, each under the console's own label: what this console was
    // built against, and what the binary reported.
    expect(screen.consoleVersion).toBe(CLI_VERSION_BUILT_AGAINST)
    expect(screen.cliVersion).toBe(CLI_HARD_FLOOR_VERSION)
    expect(screen.consoleLabel).toContain('console')
    expect(screen.cliLabel).toContain('driving')
    expect(screen.sentence).toContain(SUPPORTED_CLI_MINORS.join(', '))
  })

  test('draws the banner for a CLI above the range too, and offers the console the update', async () => {
    // The other direction of the same rule: a newer CLI is not "supported" just
    // because it is newer, and the fix is the console's, not the CLI's.
    const result = await mount(drawSkew('0.9.0\n', 'dark'))
    const screen = await drawn(page)

    expect(screen.mode).toBe('reduced')
    expect(screen.skew).toBe('newer')
    expect(screen.theme).toBe('dark')
    expect(screen.cliVersion).toBe('0.9.0')
    expect(screen.sentence).toContain('update the console')
    // Not the CLI's upgrade command: telling the operator to upgrade a fugaro
    // that is *newer* than the console would be advice that breaks the console.
    expect(screen.sentence).not.toContain('brew upgrade')
  })

  test('the reduced mode hides the views it cannot support, and draws no figure', async () => {
    await mount(drawSkew('0.9.0\n', 'light'))
    const screen = await drawn(page)

    // Named, one by one, so a mutation to the withheld list fails a test that
    // says which view leaked.
    expect(screen.hidden).toEqual([...REDUCED_VIEWS])
    expect(screen.hiddenWords).toEqual(REDUCED_VIEWS.map((view) => HIDDEN_VIEW_WORDS[view]))
    for (const view of REDUCED_VIEWS) {
      expect(screen.text, view).toContain(HIDDEN_VIEW_WORDS[view])
    }
    expect(classifyCliVersion('0.9.0').hidden).toEqual([...REDUCED_VIEWS])
    expect(hidesView(classifyCliVersion('0.9.0'), 'spend')).toBe(true)
    expect(hidesView(classifyCliVersion('0.9.0'), 'caps')).toBe(true)
    expect(hidesView(classifyCliVersion('0.9.0'), 'kill')).toBe(true)

    // And the screen really is not a dashboard: no money, no cap bars, no
    // kill affordance, no link to click. The figures a reduced console refuses
    // to draw are exactly the ones this asserts are absent.
    expect(screen.liveRoots).toBe(0)
    expect(screen.burnBars).toBe(0)
    expect(screen.caps).toBe(0)
    expect(screen.buttons).toBe(0)
    expect(screen.anchors).toBe(0)
    expect(screen.text).not.toContain('$')
  })

  test('the below-floor case shows the refusal screen and no dashboard', async () => {
    // The plan's red step, in the window: below the hard floor the console
    // refuses rather than drawing spend it cannot vouch for.
    const result = await mount(drawSkew('0.5.0\n', 'light'))
    const screen = await drawn(page)

    expect(result['mode']).toBe('refused')
    expect(screen.mode).toBe('refused')
    expect(screen.skew).toBe('below-floor')
    expect(screen.headline).toBe(SKEW_HEADLINES.refused)
    expect(screen.cliVersion).toBe('0.5.0')
    // The refusal names the floor and the command that fixes it, in the CLI's
    // own words for how it is installed (`internal/updatecheck`'s `BrewCommand`).
    expect(screen.sentence).toContain(CLI_HARD_FLOOR_VERSION)
    expect(screen.sentence).toContain('brew upgrade dimipaun/tap/fugaro')

    // No dashboard: not one root, not one bar, not one cap, and nothing to
    // click. `main/window.ts` is the half that makes this structural -- below the
    // floor `startupWindow` builds no window at all -- and this is the half that
    // says the screen a refused console does show is only the refusal.
    expect(screen.liveRoots).toBe(0)
    expect(screen.burnBars).toBe(0)
    expect(screen.caps).toBe(0)
    expect(screen.sections).toBe(1)
    expect(screen.buttons).toBe(0)
    expect(screen.anchors).toBe(0)
    expect(screen.text).not.toContain('$')
    // The withheld views are named on the refusal too, so the operator can see
    // what the console is not showing rather than wondering where it went.
    expect(screen.hidden).toEqual([...REDUCED_VIEWS])
  })

  test('that assertion has teeth: the same selectors find a real dashboard', async () => {
    // The check on the check. `liveRoots === 0` above is only evidence if this
    // window can draw a dashboard at all and the readers see it -- so the same
    // fixture, through the same factory, into the same document, and the same
    // readers answer non-zero.
    const result = await mount(drawDashboard(theFrames(), 'light'))
    const screen = await drawn(page)

    expect(result['mounted']).toBe(true)
    expect(screen.liveRoots).toBe(1)
    expect(screen.burnBars).toBeGreaterThan(2)
    expect(screen.text).toContain('$')
    // And it is a *different* screen: the refused console's root is not drawn
    // when a dashboard is, which is what makes the two states distinguishable
    // by a reader rather than by a flag.
    expect(screen.mode).toBeNull()
    expect(screen.headline).toBeNull()
  })

  test('refuses a version line it cannot read, rather than assuming it is supported', async () => {
    // The line a binary prints can be anything. A console that fell through to
    // "assume it is in range" would drive a CLI it cannot identify, so an
    // unreadable line is the refusal -- and the line itself is dropped where it
    // was read, so it is not drawn either.
    const result = await mount(drawSkew('usage: fugaro version\n', 'light'))
    const screen = await drawn(page)

    expect(result['mode']).toBe('refused')
    expect(result['cliVersion']).toBeNull()
    expect(screen.mode).toBe('refused')
    expect(screen.skew).toBe('unreadable')
    expect(screen.cliVersion).toBe(UNREADABLE_VERSION)
    expect(screen.text).not.toContain('usage')
    expect(screen.liveRoots).toBe(0)
  })

  test('a version line that carried a credential does not put it on the screen', async () => {
    // The version line is a string from outside, so it goes through the
    // sanitizer like every other one. The interesting input is not a plausible
    // line -- the grammar refuses those -- but a line the grammar *accepts* that
    // the token rules still have an opinion about: a build tail of hex. Either
    // it is drawn as it was read or it is refused with a reason, and the one
    // thing that must never happen is a credential-shaped run on screen.
    const token = githubToken()
    const result = await mount(drawSkew(`${token}\n`, 'light'))
    const screen = await drawn(page)

    expect(result['mode']).toBe('refused')
    expect(screen.text).not.toContain(token)

    // The same, but through a line the grammar accepts: a long hex-looking build
    // tail. Whatever the sanitizer decides, the screen agrees with it.
    const shaped = `0.9.0-${'a1b2c3d4'.repeat(6)}`
    await mount(drawSkew(`${shaped}\n`, 'light'))
    const shapedScreen = await drawn(page)
    const placed = classifyCliVersion(shaped)
    expect(placed.mode).toBe('reduced')
    if (shapedScreen.cliVersion === shaped) {
      expect(shapedScreen.text).toContain(shaped)
    } else {
      expect(shapedScreen.cliVersion).toBe(UNREADABLE_VERSION)
    }
  })

  test('the sentence is the console\'s own, whatever the binary said', async () => {
    // Two different unplaceable-ish lines, one sentence: the versions the CLI
    // reported are drawn in their own cells and never composed into prose this
    // console wrote, which is what keeps a string from outside out of a
    // sentence that a screenshot, a log or a title attribute may carry.
    await mount(drawSkew('dev\n', 'light'))
    const dev = await drawn(page)
    await mount(drawSkew('v0.9.0-3-gabc1234\n', 'light'))
    const described = await drawn(page)

    expect(dev.mode).toBe('reduced')
    expect(dev.skew).toBe('unplaceable')
    expect(dev.cliVersion).toBe('dev')
    expect(dev.sentence).toBe(skewSentence(classifyCliVersion('dev')))
    expect(described.cliVersion).toBe('v0.9.0-3-gabc1234')
    expect(described.sentence).not.toContain('gabc1234')
    expect(described.sentence).not.toContain('0.9.0-3')
    // The reported version is in its own cell, drawn through the sanitizer: as
    // it was read, or the console's own sentence for a line it will not draw --
    // never a half-drawn string. (`n/a` was the spelling before the
    // UNREADABLE_VERSION refactor of the last commit; nothing in this file may
    // go back to it, so the assertion names the constant itself.)
    if (described.cliVersion === 'v0.9.0-3-gabc1234') {
      expect(described.text).toContain('gabc1234')
    } else {
      expect(described.cliVersion).toBe(UNREADABLE_VERSION)
    }
  })
})
