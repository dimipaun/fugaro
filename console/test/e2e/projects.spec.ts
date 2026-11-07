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

import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { CONFIRM_ENTRY_URL } from '../../src/main/confirm'
import { SPAWN_LOG_ENV } from '../../src/main/spawnlog'
import type { SpawnLogEntry } from '../../src/main/spawnlog'
import { SETTINGS_TICK_LABEL } from '../../src/main/settingsview'
import type { JSONDoc } from '../../src/source/source'
import { CANCEL_LABEL, SAVE_LABEL, TICK_ID, TITLE_SETTINGS } from '../../src/renderer/confirm'
import { PROJECT_PANEL_KEYS, PROJECTS_BUSY, PROJECTS_THEMES } from '../../src/renderer/screens/projects'
import { CLI_VERSION_BUILT_AGAINST, MODELS_LIST_CLI_FLOOR } from '../../src/shared/version'
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
 * reduced-mode notice it carries, and `shared/schema.js` is here as the one
 * **deliberate** addition to the compiled requires: the models panel's document
 * arrives as a raw `models ls --json` object and the entry decodes it before the
 * model builder sees it (`decodeModelsList`), so the spec decodes it in the page
 * the same way rather than hand-writing a view the decoder is the judge of.
 * `src/main/ipc.ts` is imported by the screen
 * for a **type** only, which the compiler erases, so nothing of the main
 * process is reachable from here -- and the registry below refuses a specifier
 * that is not a relative path, so a bundle that grew an import of `node:` or of
 * a package fails here rather than in a window with no Node in it. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/notice.js',
  'shared/version.js',
  'shared/schema.js',
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
 * the model from the documents, and mount what it draws. */
function draw(
  budget: JSONDoc,
  accounts: JSONDoc,
  secrets: readonly JSONDoc[],
  theme: string,
  models: JSONDoc | null = MODELS,
  settings: JSONDoc | null = SETTINGS,
): string {
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
var raw = ${JSON.stringify(models)};
var decoded = raw === null ? null : load('/shared/schema.js').decodeModelsList(raw, 'models ls --json');
var modelsModel = projects.buildProjectsModels({
  document: decoded !== null && decoded.ok ? decoded.value : null,
  because: decoded !== null && !decoded.ok ? decoded.skew.because : null,
  tooOld: false,
  version: '1.2.3'
});
var settingsRaw = ${JSON.stringify(settings)};
var settingsModel = projects.buildProjectsSettings({ raw: settingsRaw, because: null });
var tree = projects.renderProjects(factory, model, ${JSON.stringify(theme)}, modelsModel, settingsModel);
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
  // The models panel (console 0.119.0): the routes' names and hosts, the
  // repositories an allow-list names, the models a run can name and their
  // prices as drawn, and every control of any kind under the panel -- which is
  // an empty list, because the panel is read-only.
  readonly modelsRoutes: readonly string[]
  readonly modelsHosts: readonly string[]
  readonly modelsAllowed: readonly string[]
  readonly modelsIds: readonly string[]
  readonly modelsPrices: readonly string[]
  readonly modelsControls: readonly string[]
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
      modelsRoutes: texts('.projects-models-route-name'),
      modelsHosts: texts('.projects-models-route-host'),
      modelsAllowed: texts('.projects-models-allow'),
      modelsIds: texts('.projects-models-id'),
      modelsPrices: texts('.projects-models-input, .projects-models-output'),
      // The whole panel, for anything a reader could operate: the models read
      // is read-only, so an `input`, a `select`, a `button`, a `form` or a
      // contenteditable under it would be a control no operation backs.
      modelsControls: all(
        '.projects-models input, .projects-models textarea, .projects-models select, ' +
          '.projects-models button, .projects-models form, .projects-models [contenteditable]',
      ).map((one) => one.tagName.toLowerCase()),
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

/**
 * MODELS is `models ls --json` (contract section 6.18), the read the tab's
 * **second** section draws from (console 0.119.0): the routes a launch may send
 * data through, the models a run can name today, and the price catalog's age.
 * It is one document per project and it is offline -- no bucket, no host -- so
 * nothing here is a run's spend.
 *
 * The two routes are the screen's two states in one document: `anthropic` is
 * open to every repository (`open_to_every_repo`, drawn in words) and `gateway`
 * names the one repository it may send data to and carries a fee. One model is
 * priced and one is not: `claude-sonnet-5` has no `input_per_m` at all, which is
 * unknown and never zero, and no route -- the two cases the panel must not draw
 * as `$0.00` and as a blank.
 *
 * The catalog is stale, so the panel's age marker has something to say: a
 * catalog's age is what makes its prices a claim about a date.
 */
const MODELS: JSONDoc = {
  version: 1,
  project: 'example-project',
  catalog: {
    source: 'https://catalog.example.test/models.json',
    checked_at: '2026-10-01',
    age_days: 33,
    stale: true,
  },
  routes: [
    {
      name: 'anthropic',
      kind: 'direct',
      base_url: 'https://api.anthropic.com',
      host: 'api.anthropic.com',
      models: ['claude-*'],
      route_fee_pct: 0,
      allow_data_to: ['*'],
      open_to_every_repo: true,
      note: '',
    },
    {
      name: 'gateway',
      kind: 'openai',
      base_url: 'https://gateway.example.test/v1',
      host: 'gateway.example.test',
      models: ['deepseek-v4.1-flash'],
      route_fee_pct: 12.5,
      allow_data_to: ['example-owner/example-repo'],
      open_to_every_repo: false,
      note: 'the provider margin is added on this route',
    },
  ],
  models: [
    { id: 'deepseek-v4.1-flash', route: 'gateway', source: 'catalog', input_per_m: 0.14, output_per_m: 0.28, catalog: null, allowed_to: ['*'], refused: '', note: '' },
    { id: 'claude-sonnet-5', route: '', source: 'unpriced', input_per_m: null, output_per_m: null, catalog: null, allowed_to: [], refused: 'no route serves it', note: '' },
  ],
  repositories: [],
}

/**
 * SETTINGS is `settings --json` (the settings-edit plan's C4), and it is the
 * committed fixture read from disk rather than restated here: the e2e stub
 * answers the same read with the same file (`test/fixtures/bin/fugaro`), and a
 * second copy in this spec would be a second document to keep in step. Its four
 * rows are the four the screen's rules are about -- a narrowing the generic path
 * writes, a widening it writes, a key another writer owns, and a sensitive one
 * that is drawn nowhere at all (D1111).
 *
 * **This document is what the golden tests photograph, so the committed darwin
 * pair is a picture of the fixture as it was before C4.** The settings table
 * draws the rows' own words (`settingsRowLine`: the scope, the value, the layer
 * that won, the state, the owner), so the pair last recorded in `660baae4`
 * draws `repo`/`set (project)`/`set (local)`/`default` and the sentence "Fugaro
 * Mailbox" where these rows draw `project`, `stale 3h`, `ok`, `local` and the
 * local config's compact JSON. Re-recording is the owner's, on macOS, with the
 * command in this file's header; the frame is the same pixels on every run, so
 * that recording is the only thing that clears the comparison. The freshness
 * clock reads the theme directory and not the documents a spec mounts, so this
 * staleness reaches the comparison rather than `assertGoldenFresh`.
 */
const SETTINGS: JSONDoc = JSON.parse(
  readFileSync(join(consoleDir, 'test', 'fixtures', 'settings.json'), 'utf8'),
) as JSONDoc

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
    // The eighth panel is the models panel (console 0.119.0): the tab's second
    // read, drawn by `fillProjectsModels` rather than by the seven-panel model,
    // and last in the tree because it is a sibling of the body those seven are
    // filled into. The ninth is the settings panel (the settings-edit plan's C1
    // and this task's C4), a third sibling drawn the same way -- which is what
    // these two frames gain: the settings section (D1118).
    expect(drawn.panels).toEqual([...PROJECT_PANEL_KEYS, 'models', 'settings'])
    expect(drawn.states).toEqual(['read', 'read', 'read', 'read', 'read', 'not-built', 'not-built', 'read', 'read'])
    // Every panel names the command it read, drawn on the panel itself: a panel
    // whose source is nowhere is a panel a reader cannot check. There is one
    // command behind the three panels that read the cap document -- `budget show`
    // -- and `runs ls` is not among the sources at all, which is the screen's
    // half of the read that was taken off this tab.
    for (const source of drawn.sources) expect(source.length).toBeGreaterThan(0)
    expect(drawn.sources).toContain('fugaro budget show --json')
    expect(drawn.sources).not.toContain('fugaro runs ls --json')
    // And every panel says its state in words, so the frame's tone is never the
    // only channel: `read` on the six that read a document (the models panel
    // among them), the design's `locked` on the two cards this CLI cannot read
    // at all.
    expect(drawn.stateWords).toEqual(['read', 'read', 'read', 'read', 'read', 'locked', 'locked', 'read', 'read'])
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
    // The seven panels that read a document have a count (the models panel
    // counts its routes and models, the settings panel its rows); the two that
    // did not have none. This is the mechanism behind "never as a zero": there
    // is no reading to format, so nothing can render as `0`.
    expect(drawn.counts.length).toBe(7)
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

  test('draws the models read-only: the routes, the open-to-every words and no price as a zero', async () => {
    await mount(BUDGET, ACCOUNTS, SECRETS, 'light')
    const drawn = await screen(page)

    // **The panel is read-only, and that is asserted on the drawn tree rather
    // than on the model**: there is no control under it of any kind -- nothing to
    // add a model, remove one or point a role at one -- and no navigation
    // either, so the `base_url` below is text and not a destination.
    expect(drawn.modelsControls).toEqual([])
    expect(drawn.anchors).toBe(0)
    expect(drawn.hrefs).toBe(0)
    // The route's own words: which repositories may send data to it is a
    // sentence (`may send data to`) and never a bare list, and the route open to
    // every repository says so in words rather than by an empty allow-list.
    expect(drawn.modelsRoutes).toEqual(['anthropic', 'gateway'])
    // **The host here is the console's marker for a machine name and not the
    // name the document wrote.** `routes[].host` is the `host` class in the
    // decoder's own table (`WIRE_TEXT_CLASSES`, `src/shared/schema.ts`), and that
    // class draws `HOST_PLACEHOLDER` for every machine name, valid or not --
    // which is the recorder's rule as well (`src/source/redact.ts`). No other
    // class draws the name either: `label` and `sentence` run the free-text
    // rules, whose hostname rule replaces one before any class branch reads the
    // value. The hostname the route points at is not hidden by this: it is drawn
    // whole inside the `base_url` below, which is a `url`.
    expect(drawn.modelsHosts).toEqual(['<host>', '<host>'])
    expect(drawn.text).toContain('open to every repository')
    expect(drawn.modelsAllowed).toEqual(['example-owner/example-repo'])
    // The models: one priced, one not. A price the document does not carry is
    // `n/a` -- unknown -- and never `$0.00`, which would say the model is free.
    expect(drawn.modelsIds).toEqual(['deepseek-v4.1-flash', 'claude-sonnet-5'])
    expect(drawn.modelsPrices).toContain('n/a')
    expect(drawn.modelsPrices).not.toContain('$0.00')
    // The catalog's age and its stale marker, as words on the panel.
    expect(drawn.text).toContain('2026-10-01')
    expect(drawn.text).toContain('stale')
  })

  test('draws an incomplete accounts document as unavailable rather than as an empty list', async () => {
    await mount(BUDGET, ACCOUNTS_INCOMPLETE, SECRETS, 'light')
    const drawn = await screen(page)

    expect(drawn.states).toEqual([
      'read',
      'read',
      'read',
      'unavailable',
      'read',
      'not-built',
      'not-built',
      'read',
      'read',
    ])
    expect(drawn.text).toContain('the project could not be selected')
    // An incomplete inventory has no count: `0 accounts` would be a false
    // statement about the project, not a missing one. The models panel and the
    // settings panel are reads of their own and are unaffected by this document
    // either way.
    expect(drawn.counts.length).toBe(6)
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
    expect(forced.words).toEqual(['read', 'read', 'read', 'read', 'read', 'locked', 'locked', 'read', 'read'])
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

/** readsOf is a spawn log as records: one line per child the console ran,
 * written by `src/main/spawn.ts` before the child prints anything. It is the
 * only place a read that should not have happened is visible from outside. */
function readsOf(where: string): readonly SpawnLogEntry[] {
  let text: string
  try {
    text = readFileSync(where, 'utf8')
  } catch {
    return []
  }
  return text
    .split('\n')
    .filter((line) => line.trim() !== '')
    .map((line) => JSON.parse(line) as SpawnLogEntry)
}

/** SETTINGS_KEY is the row the narrowing below edits, and SETTINGS_WIDENING_KEY
 * the one the widening edits: both are rows of `test/fixtures/settings.json`,
 * whose `target_layer` the console takes the `--layer` word from (D1102). The
 * first writes the `repo` layer the read names as that row's target, and that is
 * **not** the `project` layer that wins it: the repository's own `fugaro.yaml`
 * overlays the project layer's defaults (`internal/config/resolve.go`,
 * `TestResolveTable`'s "a repository scalar beats the project"), so this write
 * does change what a run reads, while D1113's comparison -- two printed layer
 * names, equal or not -- draws the sentence that says it will not. That is the
 * limit `settingsview.ts` records where the sentence is built, and not something
 * this fixture earns: comparing two names cannot tell a write that loses to the
 * winner from one that outranks it, the layers' order is the CLI's (D1102), and
 * the fix is the plan's own escape hatch -- a `shadowed` field in the dry run's
 * document (D1113) -- rather than a second copy of that order here. The spec
 * drives this row for the argv it writes, which is the property this spec exists
 * for, never for the sentence. The second writes a layer the CLI's dry run
 * classifies as a widening (`DRY_RUNS` below, which is the stub's answer and not
 * a comparison this spec makes), which is the whole of the difference between
 * the two windows. */
const SETTINGS_KEY = 'agent.review_rounds'
const SETTINGS_WIDENING_KEY = 'claude_account_auto'

/** DRY_RUNS are the documents the stub answers the dry run with, one per key, as
 * `settings set --dry-run --json` prints them (`settingsWriteDoc`,
 * `internal/cli/settings_set.go`): the key, the layer written, the file written,
 * whether the edit changes anything, whether it widens, the CLI's own `why`, the
 * two values, and the `refusal` member a real document always carries empty. */
const DRY_RUNS: Readonly<Record<string, JSONDoc>> = {
  [SETTINGS_KEY]: {
    version: 1,
    key: SETTINGS_KEY,
    layer: 'repo',
    path: '/home/owner/src/example-project/fugaro.yaml',
    changed: true,
    widening: false,
    why: '',
    old: '2',
    new: '3',
    refusal: '',
  },
  [SETTINGS_WIDENING_KEY]: {
    version: 1,
    key: SETTINGS_WIDENING_KEY,
    layer: 'local',
    path: '/home/owner/.config/fugaro/local.yaml',
    changed: true,
    widening: true,
    why: 'this raises the per-run ceiling the project layer sets',
    old: 'false',
    new: 'true',
    refusal: '',
  },
}

/** seedSettings writes the settings documents beside **every** copy of the stub,
 * the way `cliStub`'s own seeders do: either copy can be the one the console's
 * resolution runs, and the stub answers a read from the files beside itself. The
 * read is the committed fixture; the dry runs are keyed the way the stub looks
 * them up (`settings-dryrun-<KEY>.json`). Hermetic: three files in a scratch
 * directory, and no CLI but the stub. */
function seedSettings(cli: CliStub): void {
  const read = readFileSync(join(consoleDir, 'test', 'fixtures', 'settings.json'), 'utf8')
  for (const where of [join(cli.home, '.local', 'bin'), cli.dir]) {
    mkdirSync(where, { recursive: true })
    writeFileSync(join(where, 'settings.json'), read)
    for (const [key, document] of Object.entries(DRY_RUNS)) {
      writeFileSync(join(where, `settings-dryrun-${key}.json`), `${JSON.stringify(document)}\n`)
    }
  }
}

/** confirmWindow is the settings window the edit flow opens, found the way the
 * operator finds it: the window whose URL is the confirmation document. It is
 * the second window this app has, so the first one is never it. */
async function confirmWindow(app: ElectronApplication): Promise<Page> {
  let found: Page | null = null
  await expect
    .poll(
      () => {
        found = app.windows().find((page) => page.url() === CONFIRM_ENTRY_URL) ?? null
        return found === null ? 'none' : 'there'
      },
      { timeout: 15_000, message: 'the confirmation window never opened' },
    )
    .toBe('there')
  if (found === null) throw new Error('the confirmation window went away between the poll and this line')
  return found
}

/** shownArgv is the command line the window draws: the `Command` row's own
 * values, field by field and in order -- which is the argv and not a summary of
 * it, and therefore the thing this spec compares the stub's record against. */
function shownArgv(confirm: Page): Promise<readonly string[]> {
  return confirm.evaluate(() => {
    const fields = document.getElementById('confirm-fields')
    const nodes = Array.from(fields?.children ?? [])
    const at = nodes.findIndex((node) => node.tagName.toLowerCase() === 'dt' && node.textContent === 'Command')
    if (at < 0) return []
    const words: string[] = []
    for (const node of nodes.slice(at + 1)) {
      if (node.tagName.toLowerCase() !== 'dd') break
      words.push(node.textContent ?? '')
    }
    return words
  })
}

/** pressWhenReady presses one of the window's two buttons until the window is
 * gone. It is `launch.spec.ts`'s rule for the other question, one window over: a
 * press that lands inside the main process's readiness delay is refused with
 * `CONFIRM_NOT_YET`, so the spec waits on the state -- the window closed --
 * rather than on a sleep, and a press the window took is the one that closed it.
 */
async function pressWhenReady(confirm: Page, label: string): Promise<void> {
  await expect
    .poll(
      async () => {
        if (confirm.isClosed()) return 'answered'
        await confirm
          .getByRole('button', { name: label })
          .click({ timeout: 3_000 })
          .catch((error: unknown) => {
            // A press that arrived before the button existed is the poll's to
            // retry; a window that went away is the answer itself.
            if (error instanceof Error && (error.name === 'TimeoutError' || /has been closed|Target closed/.test(error.message))) {
              return undefined
            }
            throw error
          })
        return confirm.isClosed() ? 'answered' : 'waiting'
      },
      { timeout: 20_000, intervals: [100], message: `the ${label} press was never taken by the window` },
    )
    .toBe('answered')
}

/** settingsWrites is the argv of every settings write the console has run so far,
 * read from the spawn log: the record of what was spawned, and the second
 * reading of "the console ran the command line it showed". */
function settingsWrites(where: string): readonly (readonly string[])[] {
  return readsOf(where)
    .map((record) => record.argv)
    .filter((argv) => argv[1] === 'settings' && argv.includes('--yes'))
}

/** stubWrites is the stub's own record of every settings line it was handed,
 * phase by phase: the dry run's line and the write's, in the order it answered
 * them. It is what a case waits on for a write that must have happened, and for
 * one that must not. */
function stubWrites(cli: CliStub): readonly string[] {
  return cli.answeredReads().filter((line) => line.includes(' settings set '))
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
    // The tab's **second** read, seeded the same way: the models panel is drawn
    // from `models ls --json`, and the stub refuses that read when no document
    // is seeded -- which is what makes the panel's own refusal path reachable
    // here without a second stub.
    cli.seedModels({ document: MODELS })
    // The tab's **third** read and its two write phases (the settings-edit
    // plan's C4): `settings --json` is answered from the committed fixture, and
    // the dry run from the document seeded for the key the console names --
    // which is why the cases below can drive a narrowing and a widening without
    // a second stub. Unseeded, both are refused with exit 2 rather than answered
    // with a document claiming something no installation said.
    seedSettings(cli)
    // **The stub's own line is below the models read's floor**
    // (`CLI_VERSION_BUILT_AGAINST` is 0.13.0 and `MODELS_LIST_CLI_FLOOR` is
    // 0.16.0), so a view on it draws the console's floor sentence for the panel
    // and spawns nothing at all: the `models ls --json` argv asserted below would
    // then be a read this view never makes. The version the console reads is the
    // stub's own `version.txt`, so this case raises it rather than installing a
    // second stub -- and the case that asserts the floor sentence, below, is the
    // one that leaves it where it is.
    cli.setVersion(MODELS_LIST_CLI_FLOOR)
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

  test('answers the tab in the frame it was opened in, and reads one command', async () => {
    // Live, the view this console opens on, makes one `runs ls --json` of its own
    // -- the rigor join -- and that read is not this case's subject. Waiting for
    // it to land is what makes the count below a count of *this view's* reads: a
    // console that asked for a run list when Projects was opened would add a
    // second record here, and one that asked as the tab was opened would be
    // seen whatever the boot had done.
    await expect
      .poll(() => readsOf(spawnLog).filter((record) => record.argv[1] === 'runs').length, {
        message: 'the Live view never made its rigor join',
      })
      .toBeGreaterThan(0)
    const before = readsOf(spawnLog).length

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
    // The models panel is the tab's own second section, and it holds the
    // document the stub answered with: a route of it is drawn, which is what
    // says this read landed and was painted rather than merely asked for.
    await expect(page.locator(`${THE_PROJECTS} .projects-models-route-name`).first()).toHaveText('anthropic')
    // The jobs table is not there to be drawn: this screen has no run list and
    // no panel built from one, which is the owner's report of 2026-10-31
    // answered on the tab itself.
    await expect(page.locator(`${THE_PROJECTS} .projects-job`)).toHaveCount(0)

    // And the reads behind that drawing: `budget show` and `models ls`, the two
    // commands this tab's two sections name, and **no `runs` read at all**
    // since this view was opened.
    const asked = readsOf(spawnLog)
      .slice(before)
      .map((record) => record.argv.slice(1))
    expect(asked).toContainEqual(['budget', 'show', '--json', '--project', 'e2e'])
    // The models read is the group's one reachable word and takes no argument
    // of its own: the argv is the command, the row's `--json` and the
    // installation's `--project`, and nothing a renderer chose.
    expect(asked).toContainEqual(['models', 'ls', '--json', '--project', 'e2e'])
    expect(asked.filter((argv) => argv[0] === 'runs')).toEqual([])
  })

  test('draws the settings section from the read, and records that read', async () => {
    // The tab's third section, drawn from the document the stub answered
    // `settings --json` with: three rows, because the fixture's fourth is
    // sensitive and a sensitive row is drawn nowhere (D1111).
    const rows = page.locator(`${THE_PROJECTS} li.projects-settings-row`)
    await expect(rows).toHaveCount(3)
    await expect(rows.first()).toContainText(SETTINGS_KEY)

    const asked = readsOf(spawnLog).map((record) => record.argv.slice(1))
    expect(asked).toContainEqual(['settings', '--json', '--project', 'e2e'])
  })

  test('writes a narrowing with the argv it showed, and records that same argv', async () => {
    // The first edit this spec drives, so the stub's record holds this flow's two
    // phases and nothing else: the dry run's line and the write's.
    expect(stubWrites(cli)).toEqual([])
    const row = page.locator(`${THE_PROJECTS} li.projects-settings-row`).filter({ hasText: SETTINGS_KEY })
    await row.locator('button.projects-settings-pencil').click()
    const editor = page.locator(`${THE_PROJECTS} .projects-settings-editor`)
    await expect(editor).toBeVisible()
    // The value is typed into the row's own box; an empty box is the unset, and
    // this case is the write of one value (the terminator is C3's unit test).
    await editor.locator('input').fill('3')
    await editor.getByRole('button', { name: 'Review and write' }).click()

    const confirm = await confirmWindow(app)
    await expect(confirm.locator('#confirm-title')).toHaveText(TITLE_SETTINGS)
    // The window is the settings question and not the launch's: its confirming
    // button is the settings window's word, and a narrowing has no tick at all
    // (D1109) -- the tick is the widening's.
    await expect(confirm.getByRole('button', { name: SAVE_LABEL })).toBeVisible()
    await expect(confirm.locator(`#${TICK_ID}`)).toHaveCount(0)

    const shown = await shownArgv(confirm)
    expect(shown.slice(1)).toEqual([
      'settings', 'set', SETTINGS_KEY, '--layer', 'repo', '--yes', '--project', 'e2e', '--', '3',
    ])
    await pressWhenReady(confirm, SAVE_LABEL)

    // The write's own outcome, in the CLI's words, and the row it leaves behind:
    // an edit does not change a run until `fugaro update` publishes it (D1119).
    await expect(editor.locator('.projects-settings-editor-outcome')).toContainText('next: fugaro update')
    // **The property this spec exists for**, from outside the process: the stub's
    // own record of the command line it was handed -- written by the stub, from
    // `$0` and every argument -- is the command line the window drew, word for
    // word, for both phases of the flow. The dry run's is the spelling that
    // carries **no** `--yes`: the CLI's confirmation is its own question and
    // `--yes` is the console's answer to it (D1107).
    const dryLine = [
      shown[0], 'settings', 'set', SETTINGS_KEY, '--layer', 'repo', '--dry-run', '--json', '--project', 'e2e', '--', '3',
    ].join(' ')
    await expect
      .poll(() => stubWrites(cli).length, { message: 'the write never reached the stub' })
      .toBe(2)
    expect(stubWrites(cli)).toEqual([dryLine, shown.join(' ')])
    // And the spawn log agrees from the other side: the write the console ran is
    // that argv, and not a second spelling built for the spawn.
    await expect.poll(() => settingsWrites(spawnLog).length).toBeGreaterThan(0)
    expect(settingsWrites(spawnLog).at(-1)).toEqual(shown)
  })

  test('asks for the tick on a widening, and writes the argv the ticked window showed', async () => {
    const row = page.locator(`${THE_PROJECTS} li.projects-settings-row`).filter({ hasText: SETTINGS_WIDENING_KEY })
    await row.locator('button.projects-settings-pencil').click()
    const editor = page.locator(`${THE_PROJECTS} .projects-settings-editor`)
    await expect(editor).toBeVisible()
    await editor.locator('input').fill('true')
    await editor.getByRole('button', { name: 'Review and write' }).click()

    const confirm = await confirmWindow(app)
    await expect(confirm.locator('#confirm-title')).toHaveText(TITLE_SETTINGS)
    // A widening draws the console's own extra confirmation (D1101): the tick is
    // a real checkbox with the owner's own words, and Confirm is disabled until
    // it is set. The tick is not an argv word -- the window below carries the
    // same argv ticked or not.
    const tick = confirm.locator(`#${TICK_ID}`)
    await expect(tick).toBeVisible()
    await expect(confirm.locator(`label[for="${TICK_ID}"]`)).toHaveText(SETTINGS_TICK_LABEL)
    await expect(confirm.getByRole('button', { name: SAVE_LABEL })).toBeDisabled()

    const shown = await shownArgv(confirm)
    expect(shown.slice(1)).toEqual([
      'settings', 'set', SETTINGS_WIDENING_KEY, '--layer', 'local', '--yes', '--project', 'e2e', '--', 'true',
    ])
    await tick.check()
    await expect(confirm.getByRole('button', { name: SAVE_LABEL })).toBeEnabled()
    const before = stubWrites(cli).length
    await pressWhenReady(confirm, SAVE_LABEL)

    await expect.poll(() => stubWrites(cli).length, { message: 'the widened write never reached the stub' }).toBe(before + 1)
    expect(stubWrites(cli).at(-1)).toBe(shown.join(' '))
    await expect.poll(() => settingsWrites(spawnLog).length).toBeGreaterThan(1)
    expect(settingsWrites(spawnLog).at(-1)).toEqual(shown)
  })

  test('spawns no write when the window is cancelled', async () => {
    const writes = settingsWrites(spawnLog).length
    const before = stubWrites(cli).length
    const row = page.locator(`${THE_PROJECTS} li.projects-settings-row`).filter({ hasText: SETTINGS_KEY })
    await row.locator('button.projects-settings-pencil').click()
    const editor = page.locator(`${THE_PROJECTS} .projects-settings-editor`)
    await expect(editor).toBeVisible()
    await editor.locator('input').fill('5')
    await editor.getByRole('button', { name: 'Review and write' }).click()

    const confirm = await confirmWindow(app)
    await pressWhenReady(confirm, CANCEL_LABEL)

    // The dry run went out -- the window was drawn from its answer -- and the
    // write did not: a no is the operator's, and the only argv this case may
    // add to either record is the dry run's.
    await expect.poll(() => stubWrites(cli).length, { message: 'the cancelled edit’s dry run never reached the stub' }).toBe(
      before + 1,
    )
    expect(stubWrites(cli).at(-1)).toContain('--dry-run')
    expect(stubWrites(cli).at(-1)).not.toContain('--yes')
    expect(settingsWrites(spawnLog)).toHaveLength(writes)
  })

  test('draws an owned row without a pencil, and never draws a sensitive row', async () => {
    const rows = page.locator(`${THE_PROJECTS} li.projects-settings-row`)
    // A row the generic path does not write is drawn -- with the family the
    // read's `owner` names -- and offers no pencil (D1111).
    const owned = rows.filter({ hasText: 'mailbox.keychain_item' })
    await expect(owned).toHaveCount(1)
    await expect(owned.locator('button.projects-settings-pencil')).toHaveCount(0)
    // A sensitive row is not drawn at all: the fixture carries
    // `budget.firebase_api_key`, and neither its key nor a pencil for it is
    // anywhere on the tab. The console keeps no list of its own -- the field is
    // the CLI's (D1102).
    const text = (await page.locator(THE_PROJECTS).textContent()) ?? ''
    expect(text).not.toContain('budget.firebase_api_key')
    await expect(rows.locator('button.projects-settings-pencil')).toHaveCount(2)
  })
})

// ---------------------------------------------------------------------------
// The models panel below the models read's floor.
// ---------------------------------------------------------------------------

/** The case below is the other half of the version the case above seeds: a view
 * whose CLI has no `models ls` at all. `inRangeCliStub` prints
 * `CLI_VERSION_BUILT_AGAINST` (0.13.0), which is inside the console's supported
 * range and below `MODELS_LIST_CLI_FLOOR` (0.16.0), so this is the state the
 * panel's floor sentence was written for -- a 0.15.x CLI, which would answer
 * `unknown command "ls"`, is the same state one minor up. */
test.describe('the models panel below the models read floor', () => {
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub
  let scratch: string
  let spawnLog: string

  test.beforeAll(async () => {
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    cli.seedSpend({ budget: { document: BUDGET } })
    // **No models document is seeded, and none is needed**: the floor is asked
    // before the read goes out, so the panel must never reach the stub.
    scratch = mkdtempSync(join(tmpdir(), 'fugaro-console-models-floor-'))
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

  test('draws the console’s own floor sentence, and spawns no models read', async () => {
    await page.click(tab('projects'))
    // The tab is up -- the caps panel's own read landed -- and the models panel
    // is the one part of it that is not read.
    await expect(page.locator(`${THE_PROJECTS} h1.projects-project`)).toHaveText('example-project')
    const panel = page.locator(`${THE_PROJECTS} section.projects-panel[data-panel="models"]`)
    await expect(panel).toHaveAttribute('data-state', 'unavailable')
    // The sentence is the console's own and names both versions: what the read
    // needs and what this CLI is.
    await expect(panel.locator('p.projects-because')).toHaveText(
      `the models read needs fugaro ${MODELS_LIST_CLI_FLOOR} and this CLI is ${CLI_VERSION_BUILT_AGAINST}`,
    )
    // Nothing of a document, because there was no read to draw one from.
    await expect(page.locator(`${THE_PROJECTS} .projects-models-route`)).toHaveCount(0)
    // And the spawn log says the same from outside: this view drove no `models`
    // child at all, so a panel that drew the sentence *after* asking would be a
    // read this case sees.
    const asked = readsOf(spawnLog).map((record) => record.argv.slice(1))
    expect(asked).toContainEqual(['budget', 'show', '--json', '--project', 'e2e'])
    expect(asked.filter((argv) => argv[0] === 'models')).toEqual([])
  })
})
