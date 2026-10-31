// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Spend screen in a real window (desktop-console plan Task 2.2; design
// section 6.2 screen 2, D15).
//
// `spend.test.ts` asserts the model and the tree the screen builds; this file
// asserts that the same screen draws in the console's own hardened window, with
// the real CSP in force, and takes a golden per theme. The three things the
// plan names for this spec are here: the three series render separately, the
// caps panel names a binding scope, and there is a golden for each theme.
//
// **Why the screen is drawn through `executeJavaScript` rather than by a script
// tag or a page.** Task 2.2's file list is the screen and its two tests: the
// entry point (`src/renderer/index.html`, `src/renderer/app.js`) and the place
// the compiled script is *served from* (`src/main/index.ts`'s `rendererRoot`)
// belong to another task, and neither is this one's to edit. So the spec does
// what the app will do, from outside the app: it loads the compiled renderer
// out of `dist/` (the output `npm run build` already produces) and calls
// `renderSpend` with a DOM-backed element factory of its own. The two
// mechanisms -- `webContents.executeJavaScript` for the CSP, and a twenty-line
// CommonJS registry instead of `require` -- are the same ones Task 2.1's
// `live.spec.ts` uses, for the same reasons.
//
// **There is no recorded corpus for these two documents.** `test/fixtures/`
// holds the watch feed and nothing else, and neither `report --json` nor
// `budget show --json` is recorded anywhere, so the documents below are written
// here in the shape `JSONLine` and `showBudget` write -- the same corpus
// `spend.test.ts` builds, cut down to what a window is needed for. Nothing here
// reaches the network or the CLI: both documents are objects in this file.
//
// **The goldens** are `test/e2e/__screenshots__/spend/{light,dark}-<platform>.png`, and
// they are in a subdirectory of the golden directory rather than beside the
// Live screen's two files for a reason that is not tidiness: `live.spec.ts`
// holds the directory to exactly its own two PNGs (`readdirSync`... `filter(
// endsWith('.png'))`), and that file is not this task's to edit. The name is
// passed to `toHaveScreenshot` as an ARRAY (`['spend', 'light.png']`): Playwright
// sanitizes a STRING name before `snapshotPathTemplate` applies, turning the `/`
// of `'spend/light.png'` into `-` and writing `__screenshots__/spend-light.png`
// beside the Live files. The test 'resolves the golden names under the spend
// directory' asks Playwright itself where each name lands.
//
// **No PNG is committed**, as for the Live screen: macOS and Linux pixels
// differ, so a golden recorded on the wrong platform would compare nothing.
//
// **The frame is the mount, not the page** (`#spend-mount`, the host this spec
// draws its screen into), for the reason `live.spec.ts` records at length: the
// window's own entry draws a second dashboard beside this mount, and two of its
// strings can never be the same in two runs -- the installation header carries
// the active installation's **absolute binary path** (this spec's own `mkdtemp`
// scratch directory), and the Live screen it draws carries the console's
// wall-clock reading of when the last frame *arrived*. The screen under test is
// drawn from the documents in this file and from nothing else, so its frame is
// the same pixels on every run.
//
// They are compared, never written by a run: `playwright.config.ts` sets
// `updateSnapshots: 'none'`, so a missing baseline FAILS a comparison run. They
// are recorded by an owner, on the platform the comparison runs on (macOS and
// Linux pixels differ), with
//
//   cd console && npm run build && npx playwright test spend.spec.ts --update-snapshots
//
// — the build is in the line because the specs read `dist/` and
// `npx playwright test` does not build, and on Linux it takes
// `xvfb-run --auto-servernum` (Electron needs a display);
// `test/support/goldens.ts`'s `recordCommand` is that line, per spec and per
// platform, and it is what a failed golden's message prints
//
// (owner-run, on the comparison's platform; commit `__screenshots__/spend/`
// afterwards) and until they are committed the golden tests fail on purpose. The
// one way to run the rest of the file without them is the explicit
// `FUGARO_CONSOLE_GOLDENS=skip`, which skips exactly the golden tests and says
// so in the report; nothing skips them silently.

import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import type { JSONDoc } from '../../src/source/source'
import { GAP_MARK } from '../../src/shared/availability'
import { SPEND_DEFAULT_CONTROLS, SPEND_THEMES, personView } from '../../src/renderer/screens/spend'
import type { SpendControls } from '../../src/renderer/screens/spend'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { expectGolden, expectGrounded } from '../support/goldens'
import { launchConsole } from '../support/launch'

// The console's own directory, from this file's location: `test/e2e/x.spec.ts`
// is two levels below it.
const consoleDir = join(__dirname, '..', '..')
const goldenDir = join(consoleDir, 'test', 'e2e', '__screenshots__', 'spend')

/** GOLDEN_NAMES are the names `toHaveScreenshot` is given: arrays, because
 * Playwright flattens a string's `/` (see the header). */
const GOLDEN_NAMES = {
  light: ['spend', 'light.png'],
  dark: ['spend', 'dark.png'],
} as const

/** THE_MOUNT is where this spec draws the screen, and therefore what the goldens
 * frame: the mount and never the page (see the header). */
const THE_MOUNT = '#spend-mount'

/** SKIP_GOLDENS is the one documented way to run this file with no goldens
 * recorded: `FUGARO_CONSOLE_GOLDENS=skip`. Anything else, including unset, means
 * the golden tests run and a missing baseline fails them. */
const SKIP_GOLDENS = process.env['FUGARO_CONSOLE_GOLDENS'] === 'skip'

/** SPEC is this file's own name: what a re-record command has to name, and what
 * every golden failure here quotes back to the reader. */
const SPEC = 'spend.spec.ts'

/** The compiled modules the screen is made of, in dependency order: the screen,
 * the writers in `renderer/app.js`, and the two shared modules those writers
 * use. Nothing else may be reachable -- the registry below refuses a specifier
 * that is not a relative path, so a bundle that grew an import of `node:` or of
 * a package fails here rather than in a window with no Node in it. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/notice.js',
  'shared/version.js',
  'renderer/app.js',
  // The placeholder and the busy mark the screen's two sections are drawn with
  // (console 0.116.1): `renderSpendFrame` puts a busy slot in each section, so
  // the screen requires the helper and the words it is drawn with.
  'renderer/screens/reading.js',
  'renderer/section.js',
  'renderer/screens/spend.js',
])

/** moduleSource is one compiled module as a registry entry. */
function moduleSource(relative: string): string {
  const code = readFileSync(join(consoleDir, 'dist', relative), 'utf8')
  return `registry[${JSON.stringify(`/${relative}`)}] = function (exports, require, module) {\n${code}\n};`
}

/**
 * LOADER is the module registry the injected bundle runs on: a CommonJS
 * `require` that can resolve `./` and `../` specifiers and nothing else.
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
 * screen's own narrow interface is served by a real node. `node` is the
 * harness's handle on the element and is never visible to the screen.
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
    // The chart is geometry: its svg, rect, line and g are built in the SVG
    // namespace, which is the second half of the factory's own interface
    // (SpendFactory.createNS), exactly as the window's real one is.
    createNS: function (tag) {
      return wrap(document.createElementNS('http://www.w3.org/2000/svg', tag));
    },
    wrap: wrap,
  };
}
`

/** draw is the expression the window evaluates: load the compiled screen, build
 * the model from the two documents, and mount what it draws. */
function draw(report: JSONDoc, budget: JSONDoc | null, theme: string, persons: boolean): string {
  return `(function () {
${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}
var spend = load('/renderer/screens/spend.js');
var factory = domFactory();
var model = spend.buildSpendModel(${JSON.stringify(report)}, ${JSON.stringify(budget)}, { persons: ${String(persons)} });
var tree = spend.renderSpend(factory, model, ${JSON.stringify(theme)});
var previous = document.getElementById('spend-mount');
if (previous) previous.remove();
var host = document.createElement('div');
host.id = 'spend-mount';
host.appendChild(tree.node);
document.body.appendChild(host);
return { mounted: true, accounts: model.series.map(function (series) { return series.account; }) };
})()`
}

/**
 * drawWired is the expression the window evaluates for the **wired** path (Task
 * 2.4): the screen's own `readSpend`, with a reader that answers each `--since`
 * with the document written for it, and the tree the outcome's model draws. It
 * is what the app does -- one read per control change, through the one
 * allow-listed operation -- with the source replaced by an object in this file,
 * so what the window proves is the screen's half: the arguments it builds, the
 * one read it makes, and what it draws from what came back.
 *
 * `people` is the by-person document (console L5b), and it is optional: a case
 * that passes none gets the **no reader** state `readSpend` is written for --
 * the panel says this console made no by-person read -- rather than a rejection
 * dressed up as a CLI refusal.
 */
function drawWired(
  controls: SpendControls,
  documents: Record<string, JSONDoc>,
  theme: string,
  people: JSONDoc | null = null,
): string {
  return `(function () {
${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}
var spend = load('/renderer/screens/spend.js');
var factory = domFactory();
var documents = ${JSON.stringify(documents)};
var controls = ${JSON.stringify(controls)};
var peopleDocument = ${JSON.stringify(people)};
var asked = [];
var askedPeople = [];
var read = function (args) {
  asked.push(args);
  var document = documents[args.since];
  if (!document) return Promise.reject(new Error('no document for ' + args.since));
  return Promise.resolve(document);
};
// A fixture with a "refusal" key is the CLI turning the read down: the
// reader rejects the way the app's own does, with the child's sentence, which
// is the panel's reason and never the screen's.
var readPeople = peopleDocument === null ? null : function (args) {
  askedPeople.push(args);
  if (peopleDocument.refusal) return Promise.reject({ sentence: peopleDocument.refusal });
  return Promise.resolve(peopleDocument);
};
return spend.readSpend(read, controls, ${JSON.stringify(BUDGET)}, readPeople).then(function (outcome) {
  if (!outcome.ok) return { ok: false, reason: outcome.reason, asked: asked, askedPeople: askedPeople };
  var tree = spend.renderSpend(factory, outcome.model, ${JSON.stringify(theme)}, controls);
  var previous = document.getElementById('spend-mount');
  if (previous) previous.remove();
  var host = document.createElement('div');
  host.id = 'spend-mount';
  host.appendChild(tree.node);
  document.body.appendChild(host);
  return {
    ok: true,
    asked: asked,
    askedPeople: askedPeople,
    args: outcome.args,
  };
});
})()`
}

/** Screen is the part of the drawn screen a test asserts on, read back out of
 * the live document. */
interface Screen {
  readonly theme: string | null
  readonly accounts: readonly (string | null)[]
  /** the breakdown switch's own words, in the order it draws them */
  readonly dimensions: readonly string[]
  /** the legend's three marks, in the order the chart draws its series */
  readonly legendWords: readonly string[]
  /** the one axis's top, the largest value any account draws */
  readonly axisTops: readonly string[]
  /** every bar's own share of that axis, in drawing order */
  readonly barHeights: readonly (string | null)[]
  /** the day each hatched gap column is about */
  readonly gaps: readonly string[]
  /** the table's gap sentences, one per day the history does not hold */
  readonly gapSentences: readonly string[]
  readonly scopes: readonly string[]
  readonly perRunScopes: readonly (string | null)[]
  readonly limitWords: readonly string[]
  readonly limits: readonly string[]
  readonly limitsInGlobal: number
  /** the filled width of every drawn daily-cap bar, on the bar's hundred-unit box */
  readonly capBars: readonly (string | null)[]
  readonly atCaps: number
  readonly atCapWords: readonly string[]
  readonly rows: number
  readonly notionalCells: readonly string[]
  readonly anchors: number
  readonly windows: readonly string[]
  readonly currentWindows: readonly string[]
  readonly personsEnabled: string | null
  readonly personState: string | null
  /** the by-person panel (console L5b): whether it is there, the view the CLI
   * resolved, the rows it drew and the total it always draws */
  readonly peoplePanel: number
  readonly peopleAvailable: string | null
  readonly peopleView: string | null
  readonly peopleRows: number
  readonly peopleTotals: number
  readonly peopleReason: string | null
  readonly peopleYou: string | null
  /** the by-person panel's money column, per drawn line: the count, the two
   * accounts (the notional one marked) and the average with its sample */
  readonly peopleMoney: readonly string[]
  readonly peopleNotional: readonly string[]
  readonly peopleAverages: readonly string[]
  readonly peopleSamples: readonly string[]
  readonly bars: number
  readonly chartNa: readonly string[]
  readonly text: string
}

/** screen reads the mounted screen out of the page. */
function screen(page: Page): Promise<Screen> {
  return page.evaluate(() => {
    // The window's own entry draws the real dashboard into the same document
    // (Task 3.4c), beside this spec's mount: every reading is taken inside the
    // mount, so the entry's drawing can neither satisfy nor fail an assertion.
    const scope: ParentNode = document.getElementById('spend-mount') ?? document.createDocumentFragment()
    const all = (selector: string, within?: Element): HTMLElement[] =>
      Array.from((within ?? scope).querySelectorAll<HTMLElement>(selector))
    const texts = (selector: string): string[] => all(selector).map((one) => one.textContent ?? '')
    const attributes = (selector: string, name: string): (string | null)[] =>
      all(selector).map((one) => one.getAttribute(name))
    return {
      theme: scope.querySelector('.spend')?.getAttribute('data-theme') ?? null,
      // The chart's three series, one `g` each, keyed by the account they draw.
      accounts: attributes('.spend-series', 'data-account'),
      dimensions: texts('.spend-dim'),
      legendWords: texts('.spend-legend-word'),
      axisTops: texts('.spend-axis-top'),
      barHeights: attributes('.spend-bar', 'data-height'),
      // The one column a day the history does not hold draws, by that day: a
      // gap is one hatched column and not three bars of no height.
      gaps: all('.spend-gap-column').map((column) => column.getAttribute('data-key') ?? ''),
      gapSentences: texts('.spend-gap-cell'),
      scopes: texts('.spend-scope'),
      perRunScopes: all('.spend-cap-repo').map(
        (row) => all('.spend-scope', row)[1]?.getAttribute('data-per-run-scope') ?? null,
      ),
      // The largest single lease (`max_reserve_usd`) and where it is drawn: the
      // top-level field is a project limit, not a global cap, and the CLI prints
      // it outside the caps too.
      limitWords: texts('.spend-cap-limit .spend-cap-word'),
      limits: texts('.spend-cap-limit .spend-cap-reserve'),
      limitsInGlobal: all('.spend-cap-global .spend-cap-reserve').length,
      // The daily-cap bars, by the share of the cap they fill: the CLI's own
      // `counted_usd` against `daily_cap_usd`, and no sum. A repository with no
      // daily cap draws none at all.
      capBars: attributes('.spend-cap-bar-fill', 'width'),
      // A repository at its cap says so in words, and carries the attribute a
      // rule may key on.
      atCaps: all('.spend-cap-repo[data-at-cap="true"]').length,
      atCapWords: texts('.spend-cap-at-cap'),
      rows: all('.spend-row').length,
      notionalCells: texts('.spend-notional'),
      anchors: all('a').length,
      // The range picker's windows, which of them is the operator's, and the
      // person view's own knob -- the three controls this task wires.
      windows: texts('.spend-window'),
      currentWindows: all('.spend-window')
        .filter((window) => window.getAttribute('data-current') === 'true')
        .map((window) => window.textContent ?? ''),
      personsEnabled: scope.querySelector('.spend-persons')?.getAttribute('data-enabled') ?? null,
      personState: scope.querySelector('.spend-persons-state')?.textContent ?? null,
      peoplePanel: all('.spend-people').length,
      peopleAvailable: scope.querySelector('.spend-people')?.getAttribute('data-available') ?? null,
      peopleView: scope.querySelector('.spend-people')?.getAttribute('data-view') ?? null,
      peopleRows: all('.spend-people-rows .spend-person').length,
      peopleTotals: all('.spend-people-totals .spend-person').length,
      peopleReason: scope.querySelector('.spend-people-reason')?.textContent ?? null,
      peopleYou: scope.querySelector('.spend-people-you')?.textContent ?? null,
      // The panel's money path, per drawn line: the model figure, the notional
      // one (with its mark), the average and the sample it is over. Read from
      // the panel's own classes so the report's columns cannot satisfy them.
      peopleMoney: texts('.spend-people .spend-person-counted'),
      peopleNotional: texts('.spend-people .spend-person-notional'),
      peopleAverages: texts('.spend-people .spend-person-average'),
      peopleSamples: texts('.spend-people .spend-person-counted-runs'),
      bars: all('.spend-bar').length,
      // The chart's own readings, and only the chart's: an account the document
      // does not carry draws no bar and writes its `n/a` at that bar's foot,
      // while a `$0.00` in the caps panel is a different statement about a
      // different document.
      chartNa: texts('.spend-chart-na'),
      text: document.getElementById('spend-mount')?.textContent ?? '',
    }
  })
}

// ---------------------------------------------------------------------------
// The two documents.
// ---------------------------------------------------------------------------

const DOLLARS = 1_000_000

/**
 * The report: 2026-10-12 to 2026-10-15 by day, with **three** rows -- the 14th
 * has no document at all, which is the gap the chart has to break on -- and a
 * partial day on the 15th. The three accounts are the fixture's own figures,
 * and no two of them are ever added: 1.25 billed, 0.40 notional, 0.12 compute.
 */
const REPORT: JSONDoc = {
  project: '<project>',
  source: 'firestore',
  degraded: false,
  by: 'day',
  since: '2026-10-12',
  until: '2026-10-15',
  weeks: 'iso-utc-monday',
  rows: [
    {
      key: '2026-10-12',
      start: '2026-10-12',
      end: '2026-10-12',
      partial: false,
      model_micros: 1_250_000,
      model_usd: '1.250000',
      notional_micros: 400_000,
      notional_usd: '0.400000',
      compute_micros: 120_000,
      compute_usd: '0.120000',
      compute_estimated: true,
      run_hours: 1.5,
      runs: 2,
      calls: 17,
    },
    {
      key: '2026-10-13',
      start: '2026-10-13',
      end: '2026-10-13',
      partial: false,
      model_micros: 900_000,
      model_usd: '0.900000',
      notional_micros: null,
      notional_usd: null,
      compute_micros: null,
      compute_usd: null,
      compute_estimated: false,
      run_hours: null,
      runs: 1,
      calls: 9,
    },
    {
      key: '2026-10-15',
      start: '2026-10-15',
      end: '2026-10-15',
      partial: true,
      model_micros: 300_000,
      model_usd: '0.300000',
      notional_micros: 100_000,
      notional_usd: '0.100000',
      compute_micros: null,
      compute_usd: null,
      compute_estimated: false,
      run_hours: null,
      runs: 1,
      calls: 4,
    },
  ],
  totals: {
    key: 'TOTAL',
    end: '2026-10-15',
    partial: true,
    model_micros: 2_450_000,
    model_usd: '2.450000',
    notional_micros: 500_000,
    notional_usd: '0.500000',
    compute_micros: 120_000,
    compute_usd: '0.120000',
    compute_estimated: true,
    run_hours: 1.5,
    runs: 4,
    calls: 30,
  },
  warnings: [],
}

/**
 * The budget: one repository with its own two caps, one whose per-run cap comes
 * from the repository default, and one whose per-run cap comes from the global
 * fallback -- the three scopes the panel has to name. `acme/docs` has no daily
 * cap at all (`daily_cap_usd: null` with `daily_cap_source: "none"`), which is
 * `n/a` and never `$0.00`.
 */
const BUDGET: JSONDoc = {
  project: '<project>',
  gcp_project: '<gcp-project>',
  mode: 'enforce',
  day: '2026-10-15',
  max_reserve_usd: 50,
  global: {
    daily_cap_usd: 25,
    per_run_cap_usd: 25,
    counted_usd: 12.5,
    spent_usd: 10,
    notional_usd: 4.2,
    headroom_usd: 12.5,
    kill: null,
  },
  defaults: { repo_daily_cap_usd: 10, repo_per_run_cap_usd: 2 },
  repos: [
    {
      slug: 'widget',
      repo: 'acme/widget',
      daily_cap_usd: 5,
      daily_cap_source: 'repo',
      per_run_cap_usd: 1.5,
      per_run_cap_source: 'repo',
      counted_usd: 3,
      spent_usd: 2,
      notional_usd: 1,
      headroom_usd: 2,
      kill: null,
      running: [],
    },
    {
      slug: 'docs',
      repo: 'acme/docs',
      daily_cap_usd: null,
      daily_cap_source: 'none',
      per_run_cap_usd: 2,
      per_run_cap_source: 'default',
      counted_usd: 0,
      spent_usd: 0,
      notional_usd: 0,
      headroom_usd: null,
      kill: null,
      running: [],
    },
    {
      slug: 'infra',
      daily_cap_usd: 7,
      daily_cap_source: 'repo',
      per_run_cap_usd: 25,
      per_run_cap_source: 'global',
      counted_usd: 4,
      spent_usd: 4,
      notional_usd: 0,
      headroom_usd: 3,
      kill: null,
      running: [],
    },
  ],
  running: [],
  warnings: [],
}

/**
 * The by-person document: what `runs stats --json` answers (contract section
 * 6.10, console L5b). Two launchers and the CLI's own unattributed bucket, plus
 * the read's own account of itself -- the view it resolved, who it resolved it
 * for, and the bound it stopped at. The console adds no figure of its own to
 * this, so a number on the panel that is not here is a bug.
 */
const STATS_DOCUMENT: JSONDoc = {
  project: '<project>',
  by: 'user',
  view: 'all',
  you: 'ada@example.com',
  operator: true,
  since: '2026-10-12',
  until: '2026-10-15',
  max_runs: 5000,
  truncated: false,
  unlaunched: 2,
  rows: [
    {
      user: 'ada@example.com',
      currency: 'USD',
      runs: 3,
      succeeded: 2,
      failed: 1,
      halted: 0,
      infra_error: 0,
      other: 0,
      no_record: 0,
      // The three money fields are JSON **numbers** on the wire, and all three
      // are always present: contract section 6.10 declares `counted_usd`,
      // `notional_usd` and `avg_counted_usd` `float64` with no `omitempty`, and
      // notional `0` means zero rather than absent. A string here would be a
      // document no CLI writes, and `readNumber` refuses a numeric string -- so
      // a fixture that typed them as strings would draw `n/a` in every money
      // cell and still pass.
      counted_usd: 1.5,
      notional_usd: 0.4,
      counted_runs: 3,
      avg_counted_usd: 0.5,
    },
    {
      user: 'bob@example.com',
      currency: 'USD',
      runs: 1,
      succeeded: 1,
      failed: 0,
      halted: 0,
      infra_error: 0,
      other: 0,
      no_record: 0,
      counted_usd: 0.65,
      notional_usd: 0,
      counted_runs: 1,
      avg_counted_usd: 0.65,
    },
    {
      user: 'unknown',
      currency: 'USD',
      runs: 1,
      succeeded: 0,
      failed: 0,
      halted: 0,
      infra_error: 0,
      other: 0,
      no_record: 1,
      counted_usd: 0,
      notional_usd: 0,
      counted_runs: 0,
      // `counted_runs` 0 with an average of 0 is the CLI's "no run to average":
      // the console draws `n/a`, never `$0.00` (contract section 6.10).
      avg_counted_usd: 0,
    },
  ],
  totals: [
    {
      user: '',
      currency: 'USD',
      runs: 5,
      succeeded: 3,
      failed: 1,
      halted: 0,
      infra_error: 0,
      other: 0,
      no_record: 1,
      counted_usd: 2.15,
      notional_usd: 0.4,
      counted_runs: 4,
      // The CLI rounds every published figure to the cent (`userstats.cents`),
      // so the total's average is $0.54 and not 0.5375.
      avg_counted_usd: 0.54,
    },
  ],
  notes: ['these are every launcher of this project in the window'],
}

/**
 * The person report: two requesters, one an address and one the CLI's own
 * `unknown` bucket (`budget.UnknownPerson`) for the runs with no requester.
 * Nothing in this document is drawn until the view is asked for.
 */
const PERSON_REPORT: JSONDoc = {
  ...REPORT,
  by: 'person',
  rows: [
    {
      key: 'ada@example.com',
      end: '2026-10-15',
      partial: false,
      model_micros: 1_800_000,
      model_usd: '1.800000',
      notional_micros: 200_000,
      notional_usd: '0.200000',
      compute_micros: null,
      compute_usd: null,
      compute_estimated: false,
      run_hours: null,
      runs: 3,
      calls: 19,
    },
    {
      key: 'unknown',
      end: '2026-10-13',
      partial: false,
      model_micros: 650_000,
      model_usd: '0.650000',
      notional_micros: null,
      notional_usd: null,
      compute_micros: null,
      compute_usd: null,
      compute_estimated: false,
      run_hours: null,
      runs: 1,
      calls: 8,
    },
  ],
}

test.describe('the Spend screen', () => {
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

  /** mount draws the two documents and fails with the window's own words if
   * the bundle could not be loaded or the screen refused to draw. */
  async function mount(
    report: JSONDoc,
    budget: JSONDoc | null,
    theme: string,
    persons = false,
  ): Promise<void> {
    const result = await app.evaluate(async ({ BrowserWindow }, source) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(source, true)) as { error?: string }
    }, draw(report, budget, theme, persons))
    expect(result.error).toBeUndefined()
  }

  /** mountWired draws the screen through the wired path: the controls, the
   * documents the reader answers each `--since` with, and what the window's own
   * read asked for. */
  async function mountWired(
    controls: SpendControls,
    documents: Record<string, JSONDoc>,
    theme = 'light',
    people: JSONDoc | null = null,
  ): Promise<{
    readonly asked: readonly Record<string, string>[]
    readonly askedPeople: readonly Record<string, string>[]
  }> {
    const result = (await app.evaluate(async ({ BrowserWindow }, source) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) {
        return { ok: false, reason: 'the console has no window', asked: [], askedPeople: [] }
      }
      return (await window.webContents.executeJavaScript(source, true)) as {
        ok: boolean
        reason?: string
        asked: readonly Record<string, string>[]
        askedPeople: readonly Record<string, string>[]
      }
    }, drawWired(controls, documents, theme, people))) as {
      readonly ok: boolean
      readonly reason?: string
      readonly asked: readonly Record<string, string>[]
      readonly askedPeople: readonly Record<string, string>[]
    }
    expect(result.reason).toBeUndefined()
    expect(result.ok).toBe(true)
    return { asked: result.asked, askedPeople: result.askedPeople }
  }

  test('renders the three accounts as one grouped chart on one shared axis', async () => {
    await mount(REPORT, BUDGET, 'light')
    const drawn = await screen(page)

    expect(drawn.theme).toBe('light')
    expect(drawn.accounts).toEqual(['model', 'notional', 'compute'])
    // The breakdown switch offers the CLI's whole `--by` vocabulary, the
    // by-mode word among it: the report this screen read is by day, and a
    // breakdown the console's table did not carry would be refused on the wire
    // before the window ever asked for it.
    expect(drawn.dimensions).toContain('mode')
    // One chart, three marks, and the legend names each of them in words: the
    // marks are a fill, a hatch and a dash, and none of the three is a colour
    // alone.
    expect(drawn.legendWords).toEqual([
      'billed model spend',
      '~$ subscription list price',
      'compute (estimated)',
    ])
    // **One axis, and its top is the largest value any account draws** -- the
    // 12th's billed $1.25 -- so every bar is a share of one scale and no axis
    // and no bar is ever the sum of two accounts.
    expect(drawn.axisTops).toEqual(['$1.25'])
    // One `g` per account, so the bars arrive a whole account at a time: the
    // model's for the 12th, the 13th and the 15th, then the notional's for the
    // 12th and the 15th, then compute's for the 12th -- each at its own value's
    // share of $1.25, and the 14th absent from all three because it is a gap.
    expect(drawn.barHeights).toEqual(['100.0%', '72.0%', '24.0%', '32.0%', '8.0%', '9.6%'])
    // The day with no document draws **one** hatched column, and the table says
    // so in its own words: a gap is a gap and never three zeros.
    expect(drawn.gaps).toEqual(['2026-10-14'])
    expect(drawn.gapSentences).toEqual(['No record for this day. A gap, not a zero.'])
    // An account the document does not carry draws no bar and writes its `n/a`
    // at that bar's own foot: the 13th has neither a notional nor a compute
    // value, and the 15th has no compute.
    expect(drawn.chartNa).toEqual(['n/a', 'n/a', 'n/a'])
    // The notional column of the rows is marked, so the window never shows a
    // subscription's list price as a billed dollar.
    expect(drawn.notionalCells).toEqual(['~$0.40', 'n/a', '~$0.10'])
    // No cell of the window is the sum of two accounts.
    expect(drawn.text).not.toContain('$1.65')
  })

  test('names the binding scope of every cap', async () => {
    await mount(REPORT, BUDGET, 'light')
    const drawn = await screen(page)

    // A cap with no scope is a number a reader cannot act on: the fix is
    // different depending on whether it is the repository's own cap, the
    // repository default or the global fallback.
    expect(drawn.scopes).toContain("this repository's own cap")
    expect(drawn.scopes).toContain('the repository default')
    expect(drawn.scopes).toContain('the global fallback')
    expect(drawn.perRunScopes).toEqual(['repo', 'default', 'global'])
    // A cap the document does not carry is `n/a`, never `$0.00`.
    expect(drawn.text).toContain('no cap is set at this scope')
    expect(drawn.text).toContain('n/a')
    // The largest single lease is a top-level field (`max_reserve_usd`), and it
    // is drawn in its own block with the CLI's own words -- never inside the
    // block whose word is `global`, where a project limit would read as a
    // global cap.
    expect(drawn.limitWords).toEqual(['largest single lease (max reserve)'])
    expect(drawn.limits).toEqual(['$50.00'])
    expect(drawn.limitsInGlobal).toBe(0)
    // The daily-cap bars draw the CLI's own counted dollars against the cap:
    // two repositories with a cap, `$3.00` of `$5.00` (sixty of the bar's
    // hundred units) and `$4.00` of `$7.00`, and none at all for `acme/docs`.
    // The figures the bars stand for are text beside them, so the geometry
    // carries no fact of its own.
    expect(drawn.capBars).toEqual(['60', '57.14'])
    expect(drawn.text).toContain('Caps today')
    expect(drawn.atCaps).toBe(0)
    expect(drawn.atCapWords).toEqual([])
  })

  test('hides the person breakdown by default', async () => {
    await mount(PERSON_REPORT, BUDGET, 'light')
    const drawn = await screen(page)

    expect(drawn.rows).toBe(0)
    expect(drawn.text).toContain('person breakdown is off')
    // The rows are in the document and the address in them is drawn nowhere:
    // not the address, and not the login either.
    expect(drawn.text).not.toContain('ada')
  })

  test('shows the person breakdown when it is asked for, without the address', async () => {
    await mount(PERSON_REPORT, BUDGET, 'light', true)
    const drawn = await screen(page)

    expect(drawn.rows).toBe(2)
    // The `person` class of `sanitize.ts`: an address on the wire is the
    // console's own marker, and the CLI's `unknown` bucket is drawn as itself.
    expect(drawn.text).toContain('redacted@example.invalid')
    expect(drawn.text).toContain('unknown')
    expect(drawn.text).not.toContain('ada')
  })

  test('offers no link, and nothing to click', async () => {
    await mount(REPORT, BUDGET, 'light')
    const drawn = await screen(page)
    expect(drawn.anchors).toBe(0)
  })

  // -------------------------------------------------------------------------
  // The wired controls (Task 2.4).
  // -------------------------------------------------------------------------

  test('draws the picker’s windows, and redraws the series when the range changes', async () => {
    // The picker asks for a window; the header draws the range that came back.
    // The two are different statements, and the series is the document's own:
    // `report` reads a window of the history, so a day inside the range with no
    // row is a gap and never a zero.
    const wide: JSONDoc = { ...REPORT, since: '2026-10-05' }
    const asked = await mountWired(SPEND_DEFAULT_CONTROLS, { '30d': REPORT, '7d': wide })
    const before = await screen(page)

    expect(before.windows).toEqual(['7d', '30d', '90d', '12w'])
    expect(before.currentWindows).toEqual(['30d'])
    expect(asked.asked).toEqual([{ by: 'day', since: '30d' }])
    expect(before.bars).toBe(6)
    expect(before.gaps).toEqual(['2026-10-14'])

    const moved = await mountWired({ ...SPEND_DEFAULT_CONTROLS, since: '7d' }, { '30d': REPORT, '7d': wide })
    const after = await screen(page)

    // One read, with the new window and nothing else changed.
    expect(moved.asked).toEqual([{ by: 'day', since: '7d' }])
    expect(after.currentWindows).toEqual(['7d'])
    // Eleven days of which the history holds three: the eight days it does not
    // hold are one hatched column and one table sentence each, and the widened
    // range is the only thing that changed -- the bars the history does carry
    // are the same six.
    expect(after.bars).toBe(6)
    expect(after.gaps).toHaveLength(8)
    expect(after.gaps).toContain('2026-10-05')
    expect(after.gapSentences).toHaveLength(8)
    // A day the history does not hold is a gap and never a zero, and the
    // assertion is scoped to the chart on purpose: the caps panel is built
    // from `BUDGET` and legitimately draws `$0.00` for a cap nothing has been
    // counted against (`spend.test.ts` asserts those cells), so the whole
    // mount's text would fail this for the wrong reason.
    expect(after.chartNa).toEqual(['n/a', 'n/a', 'n/a'])
    expect(after.chartNa).not.toContain('$0.00')
    expect(after.accounts).toEqual(['model', 'notional', 'compute'])
  })

  test('keeps the person view off the screen, and off the wire, until the knob is turned on', async () => {
    // The default golden is taken from this state: the knob is drawn, and it is
    // off. The document the read answers with carries the person rows -- a
    // window holding a stale document is the case the withholding is for -- and
    // none of them is drawn, and no address is anywhere in the text.
    const off = await mountWired(SPEND_DEFAULT_CONTROLS, { '30d': PERSON_REPORT })
    const hidden = await screen(page)

    expect(off.asked).toEqual([{ by: 'day', since: '30d' }])
    expect(JSON.stringify(off.asked)).not.toContain('person')
    expect(hidden.personsEnabled).toBe('false')
    expect(hidden.personState).toBe('off')
    expect(hidden.rows).toBe(0)
    expect(hidden.text).toContain('person breakdown is off')
    expect(hidden.text).not.toContain('ada')

    // The operator's own switch: the same read with `by person` and nothing
    // else, and the rows are drawn -- as the console's marker, never the
    // address.
    const on = await mountWired(personView(SPEND_DEFAULT_CONTROLS, true), { '30d': PERSON_REPORT })
    const shown = await screen(page)

    expect(on.asked).toEqual([{ by: 'person', since: '30d' }])
    expect(shown.personsEnabled).toBe('true')
    expect(shown.personState).toBe('on')
    expect(shown.rows).toBe(2)
    expect(shown.text).toContain('redacted@example.invalid')
    expect(shown.text).toContain('unknown')
    expect(shown.text).not.toContain('ada')
  })

  // -------------------------------------------------------------------------
  // The by-person view (console L5b).
  // -------------------------------------------------------------------------

  test('draws the by-person panel from its own read, and keeps the addresses out of it', async () => {
    // The knob on is a *second* read, through a second allow-listed operation
    // (`runs stats --json`), with the window translated from the picker's own
    // (`spend.ts`'s `PERSON_WINDOW_DAYS`: `30d` is `30d` in both grammars, and
    // the week form is the one that is not).
    const wired = await mountWired(
      personView(SPEND_DEFAULT_CONTROLS, true),
      { '30d': REPORT },
      'light',
      STATS_DOCUMENT,
    )
    const drawn = await screen(page)

    expect(wired.asked).toEqual([{ by: 'person', since: '30d' }])
    expect(wired.askedPeople).toEqual([{ since: '30d' }])
    expect(drawn.peoplePanel).toBe(1)
    expect(drawn.peopleAvailable).toBe('true')
    expect(drawn.peopleView).toBe('all')
    expect(drawn.peopleRows).toBe(3)
    expect(drawn.peopleTotals).toBe(1)
    expect(drawn.peopleReason).toBeNull()
    // The identity the read was resolved for, through the `person` class: the
    // console's marker, never the address, and the login nowhere on the mount.
    expect(drawn.peopleYou).toBe('redacted@example.invalid')
    expect(drawn.text).toContain('these are every launcher of this project in the window')
    expect(drawn.text).not.toContain('ada')

    // The money path, end to end: the document's three `float64`s reach the
    // cells as dollars, the notional one carries its mark, and the average
    // without a sample is `n/a` rather than `$0.00`. Three rows plus the total
    // line, and the total is the CLI's own sum of the rows above it ($1.50 +
    // $0.65 + $0.00) and never a sum this screen computed.
    expect(drawn.peopleMoney).toEqual(['$1.50', '$0.65', '$0.00', '$2.15'])
    expect(drawn.peopleNotional).toEqual([
      `~$0.40`,
      `~$0.00`,
      `~$0.00`,
      `~$0.40`,
    ])
    expect(drawn.peopleAverages).toEqual(['$0.50', '$0.65', 'n/a', '$0.54'])
    expect(drawn.peopleSamples).toEqual(['3', '1', '0', '4'])

    // The knob off is one read and no panel at all -- not an empty one.
    const off = await mountWired(SPEND_DEFAULT_CONTROLS, { '30d': REPORT }, 'light', STATS_DOCUMENT)
    const hidden = await screen(page)

    expect(off.askedPeople).toEqual([])
    expect(hidden.peoplePanel).toBe(0)
    expect(hidden.text).not.toContain('ada')
  })

  test('draws the by-person read’s refusal beside the report, and not as the screen’s', async () => {
    // A second read that fails is a panel with the CLI's sentence in it, and
    // the report -- a different read, which answered -- is still drawn from
    // its own document. The refusal is the child's text, so it reaches the
    // screen through `sanitizeRefusal` like every other refusal.
    const refused = await mountWired(
      personView(SPEND_DEFAULT_CONTROLS, true),
      { '30d': REPORT },
      'light',
      { refusal: 'runs stats: you are not an operator of this project' },
    )
    const drawn = await screen(page)

    expect(refused.asked).toEqual([{ by: 'person', since: '30d' }])
    expect(drawn.peoplePanel).toBe(1)
    expect(drawn.peopleAvailable).toBe('false')
    expect(drawn.peopleReason).toBe('runs stats: you are not an operator of this project')
    expect(drawn.peopleRows).toBe(0)
    expect(drawn.bars).toBeGreaterThan(0)
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
    await expectGrounded(page, (theme) => mount(REPORT, BUDGET, theme), {
      themes: SPEND_THEMES,
      mount: THE_MOUNT,
      text: 'h1.spend-project',
      screen: 'the Spend screen',
    })
  })

  test('draws the spend screen', async ({}, testInfo) => {
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await mount(REPORT, BUDGET, 'light')
    await expectGolden(page.locator(THE_MOUNT), GOLDEN_NAMES.light, { spec: SPEC, consoleDir, testInfo })
  })

  test('draws the spend screen in the dark theme', async ({}, testInfo) => {
    await mount(REPORT, BUDGET, 'dark')
    const drawn = await screen(page)
    expect(drawn.theme).toBe('dark')
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await expectGolden(page.locator(THE_MOUNT), GOLDEN_NAMES.dark, { spec: SPEC, consoleDir, testInfo })
  })

  test('resolves the golden names under the spend directory', ({}, testInfo) => {
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
      SPEND_THEMES.map((theme) => `${theme}-${process.platform}.png`).sort(),
    )
    for (const file of present) expect(file).toMatch(new RegExp(`^(${SPEND_THEMES.join('|')})-(darwin|linux)\\.png$`))
  })

  test('keeps its state words and its row boundary under forced colors (design section 6.4)', async () => {
    // The Spend screen's half of the forced-colors smoke (section 4.4, in the half
    // section 6.4 gives this task): the words this screen states its honesty with --
    // `(partial)`, the gap's em dash, and the notional marker -- are still in the DOM,
    // the figure row still has a boundary, and the numbers still carry tabular figures.
    // The chrome's half is `entry.spec.ts`'s; a smoke, not a golden.
    await mount(REPORT, BUDGET, 'light')
    await page.emulateMedia({ forcedColors: 'active', colorScheme: 'light' })
    expect(await page.evaluate(() => matchMedia('(forced-colors: active)').matches)).toBe(true)

    const forced = await page.evaluate(() => {
      const scope: ParentNode = document.getElementById('spend-mount') ?? document.createDocumentFragment()
      const row = scope.querySelector('tr.spend-row')
      const computed = row === null ? null : getComputedStyle(row)
      const number = scope.querySelector('.spend-num')
      return {
        words: document.getElementById('spend-mount')?.textContent ?? '',
        rowVisibility: computed?.visibility ?? null,
        rowWidth: computed?.borderBottomWidth ?? null,
        rowColour: computed?.borderBottomColor ?? null,
        tabular: number === null ? null : getComputedStyle(number).fontVariantNumeric,
      }
    })

    expect(forced.words, 'a partial day lost its marker').toContain('(partial)')
    // The table's runs cells of a day the history does not hold carry the mark
    // and never a figure: a gap is a gap in every column.
    expect(forced.words, 'the table lost its gap mark').toContain(GAP_MARK)
    expect(forced.words, 'a cap that cannot be read lost its mark').toContain('n/a')
    expect(forced.rowVisibility).toBe('visible')
    expect(forced.rowWidth, 'the figure row’s boundary collapsed under forced colors').not.toBe('0px')
    expect(forced.rowColour).not.toBe('rgba(0, 0, 0, 0)')
    expect(forced.tabular, 'the numbers lost their tabular figures').toContain('tabular-nums')

    await page.emulateMedia({ forcedColors: 'none' })
  })
})

// ---------------------------------------------------------------------------
// The Spend view in the dashboard, reading through the stub CLI.
// ---------------------------------------------------------------------------

/** THE_PANEL is the dashboard's own panel, which is where the view is drawn:
 * this file mounts a screen of its own into `#spend-mount` above, so a bare
 * `section.spend` would be ambiguous between the two. */
const THE_PANEL = 'div.console-panel'

/** THE_SPEND is the dashboard's Spend view, and the scope every reading below
 * is taken in. */
const THE_SPEND = `${THE_PANEL} section.spend`

/** THE_REPORT is the view's report section: the one a control change marks, and
 * the one whose content stays on screen under the mark. */
const THE_REPORT = `${THE_SPEND} div.spend-report`

/** tab is one button of the dashboard's navigation. */
function tab(view: string): string {
  return `nav.console-views button.console-view[data-view="${view}"]`
}

/** windowButton is one window of the range picker. Its whole text is the
 * window's own word, so the word is what finds it. */
function windowButton(word: string): string {
  return `${THE_SPEND} ol.spend-picker li.spend-item button.spend-window:text-is("${word}")`
}

/** reportWindow is the document the stub answers one window with: the fixture
 * above, with the `since` day that window covers. The header draws the
 * document's own `since` (`span.spend-since`), so the day is what says which
 * window's answer is on screen -- which is the whole of what the case below
 * reads. */
function reportWindow(since: string): JSONDoc {
  return { ...REPORT, since }
}

test.describe('the Spend view in the dashboard', () => {
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    cli = inRangeCliStub()
    cli.seedActiveInstallation()
    // The two sections' reads, and the order their answers arrive in: the window
    // the operator asks for **first** is the slow one (three seconds), so its
    // answer lands after the answer to the window they ask for last. That is the
    // case the fix is about -- the view that must stay is the last change's, and
    // the console's own tests cannot make a real child slow.
    cli.seedSpend({
      budget: { document: BUDGET },
      reports: {
        '30d': { document: reportWindow('2026-09-16') },
        '7d': { document: reportWindow('2026-10-09'), delaySeconds: 3 },
        '90d': { document: reportWindow('2026-07-18'), delaySeconds: 0.5 },
      },
    })
    app = await launchConsole({ args: cli.launchArgs(consoleDir), env: cli.env })
    page = await app.firstWindow()
    await page.waitForLoadState('domcontentloaded')
  })

  test.afterAll(async () => {
    await app?.close()
    cli?.remove()
  })

  test('answers a control change in the frame it happened in, and keeps the last change’s view', async () => {
    await page.locator(tab('spend')).click()
    const since = page.locator(`${THE_SPEND} span.spend-since`)
    // The view opens on `30d`, which is the console's own default, and the
    // header draws the day the stub's document for that window covers.
    await expect(since).toHaveText('2026-09-16')

    // **The click's own frame.** The window is pressed and the section read in
    // the same turn of the event loop, with nothing awaited in between -- so a
    // mark that only appeared when the read landed would be missing here, and
    // the three seconds this window's read takes are not part of the wait.
    const inTheFrame = await page.evaluate((scope) => {
      const button = Array.from(document.querySelectorAll(`${scope} ol.spend-picker button.spend-window`)).find(
        (one) => one.textContent === '7d',
      )
      if (button === undefined) return null
      ;(button as HTMLElement).click()
      const section = document.querySelector(`${scope} div.spend-report`)
      const mark = section?.querySelector('.console-busy') ?? null
      return {
        busy: section?.getAttribute('aria-busy') ?? null,
        marked: section?.hasAttribute('data-busy') ?? null,
        label: mark?.querySelector('.console-busy-label')?.textContent ?? null,
        // Both halves of the mark are drawn and the stylesheet picks one, so the
        // ring being here is the drawing and not a claim about the machine's
        // motion preference (the arm that hides it is `graph-motion.test.ts`'s).
        spinner: mark !== null && mark.querySelector('.console-busy-spinner') !== null,
        // The view already drawn stays under the mark: the operator is never
        // shown a blank panel for a read that is running.
        since: document.querySelector(`${scope} span.spend-since`)?.textContent ?? null,
      }
    }, THE_SPEND)
    expect(inTheFrame).toEqual({
      busy: 'true',
      marked: true,
      label: 'Reading spend for 7d, by day…',
      spinner: true,
      since: '2026-09-16',
    })

    // The second change, made before the first read has answered. Its view is
    // the one that has to stay.
    await page.locator(windowButton('90d')).click()
    await expect(since).toHaveText('2026-07-18')
    await expect(page.locator(THE_REPORT)).not.toHaveAttribute('aria-busy', 'true')

    // The read asked for first answers last, and is dropped. Waiting it out is
    // the assertion: there is no signal to wait on for a thing that must not
    // happen, and three seconds is this stub's own delay for `7d`.
    await page.waitForTimeout(4000)
    await expect(since).toHaveText('2026-07-18')
    await expect(page.locator(THE_REPORT)).not.toHaveAttribute('aria-busy', 'true')
    await expect(page.locator(`${THE_REPORT} .console-busy`)).toHaveCount(0)
  })
})
