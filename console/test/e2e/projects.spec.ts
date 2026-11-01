// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Projects screen in a real window (desktop-console plan Task 2.3; design
// section 6.2 screen 3).
//
// `projects.test.ts` asserts the model and the tree the screen builds; this file
// asserts that the same screen draws in the console's own hardened window, with
// the real CSP in force, and takes a golden per theme. The three things the plan
// names for this spec are here: the panels the CLI has no command for render as
// not-built, the source lines and the binding scopes are drawn, and there is a
// golden for each theme.
//
// **The screen reads one document and no run list** (console 0.117.0's fix,
// owner's report of 2026-10-31: this tab "also takes about 20 s with no
// feedback"). The jobs table is gone from the screen and with it the
// `runs ls --json` read that fed it, so the corpus here is three documents --
// `budget show`, `accounts ls` and `secrets ls` -- and the dashboard case at the
// foot of this file asserts the other half of that: the real window's Projects
// view drives `budget show` and no `runs` read at all.
//
// **Why the screen is drawn through `executeJavaScript` rather than by a script
// tag or a page.** This task's file list is the screen and its two tests: the
// entry point (`src/renderer/index.html`, `src/renderer/app.js`) and the place
// the compiled script is *served from* (`src/main/index.ts`'s `rendererRoot`)
// belong to another task, and neither is this one's to edit. So the spec does
// what the app will do, from outside the app: it loads the compiled renderer out
// of `dist/` (the output `npm run build` already produces) and calls
// `renderProjects` with a DOM-backed element factory of its own. The two
// mechanisms -- `webContents.executeJavaScript` for the CSP, and a twenty-line
// CommonJS registry instead of `require` -- are the same ones `live.spec.ts` and
// `spend.spec.ts` use, for the same reasons.
//
// **There is no recorded corpus for these documents.** `test/fixtures/` holds
// the watch feed and nothing else, and neither `budget show --json`,
// `accounts ls --json` nor `secrets ls --json` is recorded anywhere, so
// the documents below are written here in the shapes the contract describes
// (`console/docs/json-contract.md`), the same corpus `projects.test.ts` builds.
// `secrets ls --json` is a **bare array** and not an object, which is why it is
// handed to the model as one: see the note the panel draws about it. Nothing
// here reaches the network or the CLI: every document is an object in this file.
//
// **The goldens** are `test/e2e/__screenshots__/projects/{light,dark}-<platform>.png`, in a
// subdirectory of the golden directory rather than beside the Live screen's two
// files for the reason `spend.spec.ts` records: `live.spec.ts` holds the
// directory to exactly its own two PNGs, and that file is not this task's to
// edit. The name is passed to `toHaveScreenshot` as an ARRAY (`['projects',
// 'light.png']`): Playwright sanitizes a STRING name before
// `snapshotPathTemplate` applies, turning the `/` of `'projects/light.png'` into
// `-` and writing `__screenshots__/projects-light.png` beside the Live files.
// The test 'resolves the golden names under the projects directory' asks
// Playwright itself where each name lands.
//
// **No PNG is committed**, as for the Live and Spend screens: macOS and Linux
// pixels differ, so a golden recorded on the wrong platform would compare
// nothing.
//
// **The frame is the mount, not the page** (`#projects-mount`, the host this
// spec draws its screen into), for the reason `live.spec.ts` records at length:
// the window's own entry draws a second dashboard beside this mount, and two of
// its strings can never be the same in two runs -- the installation header
// carries the active installation's **absolute binary path** (this spec's own
// `mkdtemp` scratch directory), and the Live screen it draws carries the
// console's wall-clock reading of when the last frame *arrived*. The screen
// under test is drawn from the documents in this file and from nothing else, so
// its frame is the same pixels on every run.
//
// They are compared, never written by a run: `playwright.config.ts` sets
// `updateSnapshots: 'none'`, so a missing baseline FAILS a comparison run. They
// are recorded by an owner, on the platform the comparison runs on (macOS and
// Linux pixels differ), with
//
//   cd console && npm run build && npx playwright test projects.spec.ts --update-snapshots
//
// — the build is in the line because the specs read `dist/` and
// `npx playwright test` does not build, and on Linux it takes
// `xvfb-run --auto-servernum` (Electron needs a display);
// `test/support/goldens.ts`'s `recordCommand` is that line, per spec and per
// platform, and it is what a failed golden's message prints
//
// (owner-run, on the comparison's platform; commit `__screenshots__/projects/`
// afterwards) and until they are committed the golden tests fail on purpose. The
// one way to run the rest of the file without them is the explicit
// `FUGARO_CONSOLE_GOLDENS=skip`, which skips exactly the golden tests and says
// so in the report; nothing skips them silently.

import { existsSync, mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { SPAWN_LOG_ENV } from '../../src/main/spawnlog'
import type { SpawnLogEntry } from '../../src/main/spawnlog'
import type { JSONDoc } from '../../src/source/source'
import { PROJECT_PANEL_KEYS, PROJECTS_BUSY, PROJECTS_THEMES } from '../../src/renderer/screens/projects'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { expectGolden, expectGrounded } from '../support/goldens'
import { launchConsole } from '../support/launch'

// The console's own directory, from this file's location: `test/e2e/x.spec.ts`
// is two levels below it.
const consoleDir = join(__dirname, '..', '..')
const goldenDir = join(consoleDir, 'test', 'e2e', '__screenshots__', 'projects')

/** GOLDEN_NAMES are the names `toHaveScreenshot` is given: arrays, because
 * Playwright flattens a string's `/` (see the header). */
const GOLDEN_NAMES = {
  light: ['projects', 'light.png'],
  dark: ['projects', 'dark.png'],
} as const

/** THE_MOUNT is where this spec draws the screen, and therefore what the goldens
 * frame: the mount and never the page (see the header). */
const THE_MOUNT = '#projects-mount'

/** SKIP_GOLDENS is the one documented way to run this file with no goldens
 * recorded: `FUGARO_CONSOLE_GOLDENS=skip`. Anything else, including unset, means
 * the golden tests run and a missing baseline fails them. */
const SKIP_GOLDENS = process.env['FUGARO_CONSOLE_GOLDENS'] === 'skip'

/** SPEC is this file's own name: what a re-record command has to name, and what
 * every golden failure here quotes back to the reader. */
const SPEC = 'projects.spec.ts'

/** The compiled modules the screen is made of, in dependency order: the screen
 * and the writers in `renderer/app.js`, and the shared modules those writers
 * use. The list follows the compiled `require` calls and nothing else -- the
 * section helper the screen draws its busy mark through (`renderer/section.js`)
 * and the reading words and skeleton beside it are here for that reason, while
 * `shared/launcher.js` and `shared/rigor.js` are gone with the run list that
 * used them -- `shared/notice.js` stays, because `shared/version.js` reads the
 * reduced-mode notice it carries. `src/main/ipc.ts` is imported by the screen
 * for a **type** only, which the compiler erases, so nothing of the main
 * process is reachable from here -- and the registry below refuses a specifier
 * that is not a relative path, so a bundle that grew an import of `node:` or of
 * a package fails here rather than in a window with no Node in it. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/notice.js',
  'shared/version.js',
  'renderer/app.js',
  'renderer/skeleton.js',
  'renderer/screens/reading.js',
  'renderer/section.js',
  'renderer/writes.js',
  'renderer/screens/projects.js',
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

/** draw is the expression the window evaluates: load the compiled screen, build
 * the model from the three documents, and mount what it draws. */
function draw(budget: JSONDoc, accounts: JSONDoc, secrets: readonly JSONDoc[], theme: string): string {
  return `(function () {
${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}
var projects = load('/renderer/screens/projects.js');
var factory = domFactory();
var model = projects.buildProjectsModel({
  budget: ${JSON.stringify(budget)},
  accounts: ${JSON.stringify(accounts)},
  secrets: ${JSON.stringify(secrets)},
  sentences: {}
});
var tree = projects.renderProjects(factory, model, ${JSON.stringify(theme)});
var previous = document.getElementById('projects-mount');
if (previous) previous.remove();
var host = document.createElement('div');
host.id = 'projects-mount';
host.appendChild(tree.node);
document.body.appendChild(host);
return { mounted: true, panels: model.panels.map(function (panel) { return panel.key + ':' + panel.state; }) };
})()`
}

/** Screen is the part of the drawn screen a test asserts on, read back out of
 * the live document. */
interface Screen {
  readonly theme: string | null
  readonly panels: readonly (string | null)[]
  readonly states: readonly (string | null)[]
  readonly titles: readonly string[]
  readonly sources: readonly string[]
  readonly counts: readonly string[]
  // The panel's state **as a word**: `read`, `not read` or `locked`, drawn
  // beside the title so no state is carried by a frame's tone alone.
  readonly stateWords: readonly string[]
  readonly scopes: readonly (string | null)[]
  // The word each cap comes from, on the repository rows that carry one.
  readonly capSources: readonly string[]
  readonly kills: readonly string[]
  readonly killStates: readonly string[]
  readonly killActions: readonly string[]
  readonly killedRows: number
  readonly repoKinds: readonly (string | null)[]
  readonly repoNotes: readonly string[]
  // The repository entries the cap document reports: one card, and one entry per
  // repository it has a cap row for.
  readonly enrolled: readonly string[]
  readonly repoWrites: readonly string[]
  readonly secretNames: readonly string[]
  readonly anchors: number
  readonly hrefs: number
  readonly text: string
}

/** screen reads the mounted screen out of the page. */
function screen(page: Page): Promise<Screen> {
  return page.evaluate(() => {
    // The window's own entry draws the real dashboard into the same document
    // (Task 3.4c), beside this spec's mount: every reading is taken inside the
    // mount, so the entry's drawing can neither satisfy nor fail an assertion.
    const scope: ParentNode = document.getElementById('projects-mount') ?? document.createDocumentFragment()
    const all = (selector: string, within?: Element): HTMLElement[] =>
      Array.from((within ?? scope).querySelectorAll<HTMLElement>(selector))
    const texts = (selector: string): string[] => all(selector).map((one) => one.textContent ?? '')
    const attributes = (selector: string, name: string): (string | null)[] =>
      all(selector).map((one) => one.getAttribute(name))
    return {
      theme: scope.querySelector('.projects')?.getAttribute('data-theme') ?? null,
      panels: attributes('.projects-panel', 'data-panel'),
      states: attributes('.projects-panel', 'data-state'),
      titles: texts('.projects-title'),
      sources: texts('.projects-source'),
      // The count of a panel that read a document: absent entirely on the
      // panels that did not, which is what makes a not-built panel not a zero.
      counts: texts('.projects-count'),
      stateWords: texts('.projects-state'),
      scopes: attributes('.projects-cap', 'data-scope'),
      capSources: texts('.projects-cap-source'),
      kills: texts('.projects-kill'),
      // The switch's state is a word on the row (`live` / `KILLED`), and so is
      // the action the design puts on it -- drawn refused (`writes.ts`), which
      // is why these are read as text rather than clicked.
      killStates: texts('.projects-kill-state'),
      killActions: texts('.projects-kill-action'),
      killedRows: all('li.projects-kill-row[data-killed="true"]').length,
      repoKinds: attributes('.projects-repo-card', 'data-kind'),
      repoNotes: texts('.projects-repo-card-note'),
      enrolled: texts('.projects-repo-enrolled'),
      repoWrites: texts('button.projects-repo-add'),
      secretNames: texts('.projects-secret-name'),
      // A screen may offer no navigation at all, and no destination attribute
      // either: this console draws text, and the main process owns opening.
      anchors: all('a').length,
      hrefs: all('[data-href]').length,
      text: document.getElementById('projects-mount')?.textContent ?? '',
    }
  })
}

// ---------------------------------------------------------------------------
// The four documents.
// ---------------------------------------------------------------------------

/**
 * BUDGET is `budget show --json`: two cap rows -- one with its own daily cap and
 * a per-run cap from the global fallback, one with no cap at all -- and the
 * project's own switch in the global block. The second row is also killed, so
 * the kill panel has a banner to draw.
 */
const BUDGET: JSONDoc = {
  project: 'example-project',
  gcp_project: 'example-cloud',
  mode: 'enforce',
  day: '2026-10-15',
  max_reserve_usd: 5,
  global: {
    daily_cap_usd: 40,
    per_run_cap_usd: 8,
    counted_usd: 12.5,
    spent_usd: 4,
    notional_usd: 3.25,
    headroom_usd: 27.5,
    kill: null,
  },
  defaults: { repo_daily_cap_usd: 10, repo_per_run_cap_usd: 2 },
  repos: [
    {
      slug: 'r-1a2b3c4d/r-5e6f7a8b',
      repo: 'example-owner/example-repo',
      daily_cap_usd: 20,
      daily_cap_source: 'repo',
      per_run_cap_usd: 8,
      per_run_cap_source: 'global',
      counted_usd: 12.5,
      spent_usd: 4,
      notional_usd: 3.25,
      headroom_usd: 7.5,
      kill: null,
      running: [],
    },
    {
      slug: 'r-1a2b3c4d/r-9c8d7e6f',
      repo: 'example-owner/other-repo',
      daily_cap_usd: null,
      daily_cap_source: 'none',
      per_run_cap_usd: null,
      per_run_cap_source: 'none',
      counted_usd: 0,
      spent_usd: 0,
      notional_usd: 0,
      headroom_usd: null,
      kill: { by: 'operator@example.test', at: '2026-10-15T11:30:00Z', reason: 'spend rate' },
      running: [],
    },
  ],
  running: [],
  warnings: [],
}

/** ACCOUNTS is `accounts ls --json`, complete: the names are drawn, and the
 * address in one is drawn as the `person` class's marker rather than as itself. */
const ACCOUNTS: JSONDoc = {
  repo: 'example-owner/example-repo',
  accounts: ['claude-ops@example.test'],
  complete: true,
}

/** ACCOUNTS_INCOMPLETE is the same document with `complete: false`: an
 * inventory the CLI could not finish is `unavailable`, never an empty list. */
const ACCOUNTS_INCOMPLETE: JSONDoc = {
  project: 'example-project',
  accounts: [],
  complete: false,
  note: 'the project could not be selected',
}

/**
 * SECRETS is `secrets ls --json`'s body: a **bare array**, as the contract says
 * (section 6.6). The names, scopes, labels and version counts are drawn, and no
 * value is -- there is no value in the document to draw.
 */
const SECRETS: readonly JSONDoc[] = [
  {
    name: 'fugaro-github-token',
    scope: 'repo',
    allowed: ['example-owner/example-repo'],
    id: 'projects/example-cloud/secrets/fugaro-github-token',
    created: '2026-09-01T09:00:00Z',
    labels: { fugaro_secret: 'fugaro-github-token', managed_by: 'fugaro' },
    versions: 3,
    latest: '3',
  },
  {
    name: 'fugaro-slack-webhook',
    scope: 'project',
    allowed: [],
    id: 'projects/example-cloud/secrets/fugaro-slack-webhook',
    created: '2026-09-02T09:00:00Z',
    labels: { managed_by: 'fugaro' },
    versions: 1,
    latest: '1',
  },
]

test.describe('the Projects screen', () => {
  // One window, one page, and the tests in order: each draws the screen it is
  // about into the same document, and the goldens are taken from what the test
  // before them mounted.
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // The app reads a `fugaro` before it opens a window (plan Task 3.3): without
    // an in-range one it opens the refusal document, and the dashboard document
    // this spec mounts into would never be loaded.
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

  /** mount draws the three documents and fails with the window's own words if
   * the bundle could not be loaded or the screen refused to draw. */
  async function mount(
    budget: JSONDoc,
    accounts: JSONDoc,
    secrets: readonly JSONDoc[],
    theme: string,
  ): Promise<void> {
    const result = await app.evaluate(async ({ BrowserWindow }, source) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(source, true)) as { error?: string }
    }, draw(budget, accounts, secrets, theme))
    expect(result.error).toBeUndefined()
  }

  test('draws one panel per section, each naming its source and its state', async () => {
    await mount(BUDGET, ACCOUNTS, SECRETS, 'light')
    const drawn = await screen(page)

    expect(drawn.theme).toBe('light')
    expect(drawn.panels).toEqual([...PROJECT_PANEL_KEYS])
    expect(drawn.states).toEqual(['read', 'read', 'read', 'read', 'read', 'not-built', 'not-built'])
    // Every panel names the command it read, drawn on the panel itself: a panel
    // whose source is nowhere is a panel a reader cannot check. There is one
    // command behind the three panels that read the cap document -- `budget show`
    // -- and `runs ls` is not among the sources at all, which is the screen's
    // half of the read that was taken off this tab.
    for (const source of drawn.sources) expect(source.length).toBeGreaterThan(0)
    expect(drawn.sources).toContain('fugaro budget show --json')
    expect(drawn.sources).not.toContain('fugaro runs ls --json')
    // And every panel says its state in words, so the frame's tone is never the
    // only channel: `read` on the five that read a document, the design's
    // `locked` on the two cards this CLI cannot read at all.
    expect(drawn.stateWords).toEqual(['read', 'read', 'read', 'read', 'read', 'locked', 'locked'])
  })

  test('renders the two sections with no command as not-built, and never as a zero', async () => {
    await mount(BUDGET, ACCOUNTS, SECRETS, 'light')
    const drawn = await screen(page)
    const text = drawn.text

    // `config/policy` (project-centric phase 1) and the enrollment allow-list
    // (phase 1 and 4) have no command behind them on this tree. A `0` would be a
    // measurement nobody made and an empty list would be "there are none", so
    // those two panels carry no count element at all.
    expect(drawn.titles).toContain('policy')
    expect(drawn.titles).toContain('enrollment allow-list')
    // The two panels say *why* they have nothing, with the design's own phrase
    // for the state: "not readable with this CLI".
    expect(text).toContain('not readable with this CLI')
    // The five panels that read a document have a count; the two that did not
    // have none. This is the mechanism behind "never as a zero": there is no
    // reading to format, so nothing can render as `0`.
    expect(drawn.counts.length).toBe(5)
  })

  test('names the binding scope of every cap', async () => {
    await mount(BUDGET, ACCOUNTS, SECRETS, 'light')
    const drawn = await screen(page)

    // A cap with no scope is a number a reader cannot act on: whether it is the
    // repository's own cap, the repository default or the global fallback is
    // what says where to change it.
    expect(drawn.scopes).toEqual(['global', 'defaults', 'repo', 'repo'])
    expect(drawn.text).toContain('global')
    // A figure with no word for where it comes from is a number a reader cannot
    // check: the repository rows carry the cap's source (`repo`, `global` or
    // `none`), which is the vocabulary `budget show --json` binds them with.
    expect(drawn.capSources).toEqual(['repo', 'global', 'none', 'none'])
    // A cap the document does not carry is `n/a`, never `$0.00`.
    expect(drawn.text).toContain('n/a')
    expect(drawn.text).toContain('largest single lease')
  })

  test('draws the kill state as a fact, including the repositories that are not killed', async () => {
    await mount(BUDGET, ACCOUNTS, SECRETS, 'light')
    const drawn = await screen(page)

    // The state is a word on the row and the attribute beside it, so a tone is
    // never the only thing that carries it.
    expect(drawn.killStates).toEqual(['live', 'live', 'KILLED'])
    expect(drawn.killedRows).toBe(1)
    expect(drawn.kills).toHaveLength(1)
    expect(drawn.kills[0]).toContain('KILLED by')
    expect(drawn.kills[0]).toContain('spend rate')
    // The switches that are off are drawn with their own word, `live` -- a
    // measured fact -- and not as a missing value, which `n/a` would say.
    // Each row carries the action the design puts on it, and the console does
    // not perform it: `Kill` on a live row, `Revive` on a killed one, drawn
    // through `disabledWrite` (`writes.ts`) with nothing behind it.
    expect(drawn.killActions).toEqual(['Kill', 'Kill', 'Revive'])
  })

  test('draws the repository set the cap document reports, and claims nothing beyond it', async () => {
    await mount(BUDGET, ACCOUNTS, SECRETS, 'light')
    const drawn = await screen(page)

    // **One card, and one entry per cap row.** The panel used to be the crossing
    // of two sets -- the cap document's repositories and the run list's -- and
    // each of its three cards needed `runs ls` to say anything. The run list is
    // not read on this tab at all now, so the panel draws the one set the
    // document itself reports and makes no claim about which of these
    // repositories have run: the sentences about the crossing are gone with the
    // read that could support them.
    expect(drawn.repoKinds).toEqual(['enrolled'])
    expect(drawn.enrolled).toEqual(['example-owner/example-repo', 'example-owner/other-repo'])
    expect(drawn.text).toContain('enrolled')
    expect(drawn.text).not.toContain('seen, not enrolled')
    expect(drawn.text).not.toContain('Running with no cap row.')
    // Nothing on the card is a write either: the "Add cap" action belonged to
    // the seen-not-enrolled case, which no longer exists here.
    expect(drawn.repoNotes).toEqual([])
    expect(drawn.repoWrites).toEqual([])
  })

  test('draws the secrets inventory as names, labels and version counts, and no value', async () => {
    await mount(BUDGET, ACCOUNTS, SECRETS, 'light')
    const drawn = await screen(page)

    expect(drawn.secretNames).toEqual(['fugaro-github-token', 'fugaro-slack-webhook'])
    expect(drawn.text).toContain('managed_by')
    // The panel says the one thing it can vouch for -- every entry in this
    // document is fugaro's -- in a sentence of its own.
    expect(drawn.text).toContain('All managed by fugaro.')
    // The document's own shape is drawn on the panel: an operator who sees an
    // empty inventory should know why the console reads it.
    expect(drawn.text).toContain('bare JSON array')
  })

  test('draws an incomplete accounts document as unavailable rather than as an empty list', async () => {
    await mount(BUDGET, ACCOUNTS_INCOMPLETE, SECRETS, 'light')
    const drawn = await screen(page)

    expect(drawn.states).toEqual(['read', 'read', 'read', 'unavailable', 'read', 'not-built', 'not-built'])
    expect(drawn.text).toContain('the project could not be selected')
    // An incomplete inventory has no count: `0 accounts` would be a false
    // statement about the project, not a missing one.
    expect(drawn.counts.length).toBe(4)
  })

  test('offers no link, and nothing to click', async () => {
    await mount(BUDGET, ACCOUNTS, SECRETS, 'light')
    const drawn = await screen(page)

    expect(drawn.anchors).toBe(0)
    expect(drawn.hrefs).toBe(0)
  })

  test('is painted from the design tokens, not from the browser defaults', async () => {
    // Design section 6.2's computed-ground assertion, and the one case in this
    // file that is red when the stylesheet stops being linked: the theme travels
    // as a `data-theme` attribute the renderer stamps, so every read above passes
    // on a tree nothing paints. `test/support/goldens.ts` holds the app's ground
    // and the screen's text colour to the shipped palette, once per theme.
    //
    // NOT a golden test, deliberately: `FUGARO_CONSOLE_GOLDENS=skip` does not skip
    // it. Nothing here reads a PNG, so an unstyled tree is red on its own account
    // rather than recorded as a fresh baseline nobody compared.
    await expectGrounded(page, (theme) => mount(BUDGET, ACCOUNTS, SECRETS, theme), {
      themes: PROJECTS_THEMES,
      mount: THE_MOUNT,
      text: 'h1.projects-project',
      screen: 'the Projects screen',
    })
  })

  test('draws the projects screen', async ({}, testInfo) => {
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await mount(BUDGET, ACCOUNTS, SECRETS, 'light')
    await expectGolden(page.locator(THE_MOUNT), GOLDEN_NAMES.light, { spec: SPEC, consoleDir, testInfo })
  })

  test('draws the projects screen in the dark theme', async ({}, testInfo) => {
    await mount(BUDGET, ACCOUNTS, SECRETS, 'dark')
    const drawn = await screen(page)
    expect(drawn.theme).toBe('dark')
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await expectGolden(page.locator(THE_MOUNT), GOLDEN_NAMES.dark, { spec: SPEC, consoleDir, testInfo })
  })

  test('resolves the golden names under the projects directory', ({}, testInfo) => {
    // Not a golden test: it asks Playwright where a recording would land, so a
    // name the template flattens is red before anyone records anything.
    expect(testInfo.snapshotPath(...GOLDEN_NAMES.light)).toBe(join(goldenDir, `light-${process.platform}.png`))
    expect(testInfo.snapshotPath(...GOLDEN_NAMES.dark)).toBe(join(goldenDir, `dark-${process.platform}.png`))
  })

  test('has a golden for each theme the design names', () => {
    // A theme with no golden is a theme nobody has looked at. The themes are
    // the screen's own, and the directory is this screen's own, so a third
    // palette -- or a PNG that was never committed -- fails here.
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    const present = existsSync(goldenDir)
      ? readdirSync(goldenDir)
          .filter((file) => file.endsWith('.png'))
          .sort()
      : []
    // One per theme for THIS platform (the template adds `-<platform>`: macOS
    // and Linux pixels differ and CI runs both), and nothing that is not a
    // theme's recording for one of those two platforms.
    expect(present.filter((file) => file.endsWith(`-${process.platform}.png`))).toEqual(
      PROJECTS_THEMES.map((theme) => `${theme}-${process.platform}.png`).sort(),
    )
    for (const file of present) expect(file).toMatch(new RegExp(`^(${PROJECTS_THEMES.join('|')})-(darwin|linux)\\.png$`))
  })

  test('keeps its state words and its row boundary under forced colors (design section 6.4)', async () => {
    // The Projects screen's half of the forced-colors smoke (section 4.4, in the half
    // section 6.4 gives this task). This is the screen with the most states and the
    // fewest colours: every panel carries `data-state`, the two with no command say
    // "not readable with this CLI" in a sentence, and neither may become a colour the
    // system takes away. The chrome's half is `entry.spec.ts`'s; a smoke, not a golden.
    await mount(BUDGET, ACCOUNTS, SECRETS, 'light')
    await page.emulateMedia({ forcedColors: 'active', colorScheme: 'light' })
    expect(await page.evaluate(() => matchMedia('(forced-colors: active)').matches)).toBe(true)

    const forced = await page.evaluate(() => {
      const scope: ParentNode = document.getElementById('projects-mount') ?? document.createDocumentFragment()
      const all = (selector: string): HTMLElement[] =>
        Array.from(scope.querySelectorAll<HTMLElement>(selector))
      // The caps table is the only table this screen draws now: the jobs table
      // and its `tr.projects-job` rows went with the run list.
      const row = scope.querySelector('tr.projects-cap')
      const computed = row === null ? null : getComputedStyle(row)
      const number = scope.querySelector('.projects-number')
      return {
        states: all('.projects-panel').map((panel) => panel.getAttribute('data-state')),
        // The state words the panels draw beside their titles: the tone a panel
        // is painted in is not what says which state it is.
        words: all('.projects-state').map((one) => one.textContent ?? ''),
        killWords: all('.projects-kill-state').map((one) => one.textContent ?? ''),
        because: all('.projects-because').map((one) => one.textContent ?? ''),
        rowVisibility: computed?.visibility ?? null,
        rowWidth: computed?.borderBottomWidth ?? null,
        rowColour: computed?.borderBottomColor ?? null,
        tabular: number === null ? null : getComputedStyle(number).fontVariantNumeric,
      }
    })

    // The state is in the DOM twice over: as the attribute a reader's tools read, and
    // as the sentence a reader reads.
    expect(forced.states).toContain('not-built')
    expect(forced.words).toEqual(['read', 'read', 'read', 'read', 'read', 'locked', 'locked'])
    // The switches' state words survive too, `KILLED` among them.
    expect(forced.killWords).toEqual(['live', 'live', 'KILLED'])
    expect(forced.because.join(' '), 'a panel with no document lost its reason').toContain(
      'not readable with this CLI',
    )
    expect(forced.rowVisibility).toBe('visible')
    expect(forced.rowWidth, 'the panel’s row boundary collapsed under forced colors').not.toBe('0px')
    expect(forced.rowColour).not.toBe('rgba(0, 0, 0, 0)')
    expect(forced.tabular, 'the numbers lost their tabular figures').toContain('tabular-nums')

    await page.emulateMedia({ forcedColors: 'none' })
  })
})

// ---------------------------------------------------------------------------
// The Projects view in the dashboard, reading through the stub CLI.
// ---------------------------------------------------------------------------

/** THE_PANEL is the dashboard's own panel, which is where the view is drawn:
 * this file mounts a screen of its own into `#projects-mount` above, so a bare
 * `section.projects` would be ambiguous between the two. */
const THE_PANEL = 'div.console-panel'

/** THE_PROJECTS is the dashboard's Projects view, and the scope every reading
 * below is taken in. */
const THE_PROJECTS = `${THE_PANEL} section.projects`

/** THE_BODY is the section the read belongs to: the element the busy mark is
 * put on, and the one the screen is filled into when the answer lands. */
const THE_BODY = `${THE_PROJECTS} div.projects-body`

/** tab is one button of the dashboard's navigation. */
function tab(view: string): string {
  return `nav.console-views button.console-view[data-view="${view}"]`
}

test.describe('the Projects view in the dashboard', () => {
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub
  let scratch: string
  let spawnLog: string

  test.beforeAll(async () => {
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    // The one document this view reads, handed to the stub the way the Spend
    // specs hand it their own: the stub answers `budget show --json` with
    // `spend-budget.json`, and refuses the read when no document is seeded.
    cli.seedSpend({ budget: { document: BUDGET } })
    // The spawn log is the operator's own opt-in (`SPAWN_LOG_ENV`) and the only
    // place a read that should not have happened is visible from outside: the
    // case below reads it to say which commands this view drove.
    scratch = mkdtempSync(join(tmpdir(), 'fugaro-console-projects-'))
    spawnLog = join(scratch, 'spawn.jsonl')
    app = await launchConsole({
      args: cli.launchArgs(consoleDir),
      env: { ...cli.env, [SPAWN_LOG_ENV]: spawnLog },
    })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
  })

  test.afterAll(async () => {
    await app?.close()
    cli.remove()
    rmSync(scratch, { recursive: true, force: true })
  })

  /** reads is the spawn log as records: one line per child the console ran,
   * written by `src/main/spawn.ts` before the child prints anything. */
  function reads(): readonly SpawnLogEntry[] {
    let text: string
    try {
      text = readFileSync(spawnLog, 'utf8')
    } catch {
      return []
    }
    return text
      .split('\n')
      .filter((line) => line.trim() !== '')
      .map((line) => JSON.parse(line) as SpawnLogEntry)
  }

  test('answers the tab in the frame it was opened in, and reads one command', async () => {
    // Live, the view this console opens on, makes one `runs ls --json` of its own
    // -- the rigor join -- and that read is not this case's subject. Waiting for
    // it to land is what makes the count below a count of *this view's* reads: a
    // console that asked for a run list when Projects was opened would add a
    // second record here, and one that asked as the tab was opened would be
    // seen whatever the boot had done.
    await expect
      .poll(() => reads().filter((record) => record.argv[1] === 'runs').length, {
        message: 'the Live view never made its rigor join',
      })
      .toBeGreaterThan(0)
    const before = reads().length

    // **The click's own frame.** The tab is pressed and the section drawn in the
    // same turn of the event loop, with nothing awaited in between: the mark,
    // the shape it holds and the absence of the header are all read from the
    // frame the click happened in, before any answer can have arrived.
    const inTheFrame = await page.evaluate(
      ({ button: selector, body: bodySelector }) => {
        const pressed = document.querySelector<HTMLElement>(selector)
        pressed?.click()
        const body = document.querySelector(bodySelector)
        const section = document.querySelector('div.console-panel section.projects')
        return {
          mounted: section !== null,
          busy: body?.getAttribute('aria-busy') ?? null,
          marked: body?.hasAttribute('data-busy') ?? null,
          label: body?.querySelector('.console-busy-label')?.textContent ?? null,
          // Both halves of the mark are drawn and the stylesheet picks one, so
          // the ring being here is the drawing rather than a claim about the
          // machine's motion preference.
          spinner: body?.querySelector('.console-busy-spinner') !== null,
          // The shape of what is coming, because this section has never been
          // filled: the read is the whole screen here.
          skeleton: body?.querySelector('.console-skeleton') !== null,
          // And nothing of the document: the header names the project, which is
          // the read's own, so a header before the answer would be a screen
          // naming something the console has not read.
          header: section?.querySelector('h1.projects-project') !== null,
          jobs: section?.querySelectorAll('.projects-job').length ?? -1,
        }
      },
      { button: tab('projects'), body: THE_BODY },
    )
    expect(inTheFrame).toEqual({
      mounted: true,
      busy: 'true',
      marked: true,
      label: PROJECTS_BUSY,
      spinner: true,
      skeleton: true,
      header: false,
      jobs: 0,
    })

    // Then the content, drawn from the document the stub answered with.
    await expect(page.locator(`${THE_PROJECTS} h1.projects-project`)).toHaveText('example-project')
    await expect(page.locator(`${THE_PROJECTS} tr.projects-cap`).first()).toBeVisible()
    await expect(page.locator(THE_BODY)).not.toHaveAttribute('aria-busy', 'true')
    // The jobs table is not there to be drawn: this screen has no run list and
    // no panel built from one, which is the owner's report of 2026-10-31
    // answered on the tab itself.
    await expect(page.locator(`${THE_PROJECTS} .projects-job`)).toHaveCount(0)

    // And the reads behind that drawing: `budget show`, the one command the
    // panels name, and **no `runs` read at all** since this view was opened.
    const asked = reads()
      .slice(before)
      .map((record) => record.argv.slice(1))
    expect(asked).toContainEqual(['budget', 'show', '--json', '--project', 'e2e'])
    expect(asked.filter((argv) => argv[0] === 'runs')).toEqual([])
  })
})
