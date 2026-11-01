// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Run detail screen in a real window (desktop-console plan Task 2.3;
// design section 6.2 screen 4).
//
// `projects.test.ts` asserts the model and the tree the screen builds (the
// plan's file list for this task names no `run.test.ts`); this file asserts that
// the same screen draws in the console's own hardened window, with the real CSP
// in force, and takes a golden per theme. The four things the plan names for
// this spec are here: the ledger, the stage timeline with its deadlines, **the
// pull request as text with no link**, the capped log window with its link, and
// the replay buffer -- whose bound, span and scrubbed moment the panel draws.
//
// **Why the screen is drawn through `executeJavaScript` rather than by a script
// tag or a page.** This task's file list is the screen and its two tests: the
// entry point and the place the compiled script is served from belong to another
// task. So the spec does what the app will do, from outside the app: it loads
// the compiled renderer out of `dist/` and calls `renderRun` with a DOM-backed
// element factory of its own -- the same two mechanisms (`executeJavaScript` for
// the CSP, a twenty-line CommonJS registry instead of `require`) that
// `live.spec.ts`, `spend.spec.ts` and `projects.spec.ts` use.
//
// **There is no recorded corpus for these documents.** `test/fixtures/` holds
// the watch feed and nothing else, and neither `diagnose RUN --json` nor
// `logs RUN --json` is recorded anywhere, so the document below is written here
// in the shape `Diagnosis` writes it, with a real `verify.Record`
// (`internal/verify/verify.go:110`) and a Bitbucket-shaped `pr_url`. The replay
// frames are arrivals in the shape the live feed hands a screen (`Snapshot`), so
// the panel is drawn from the buffer this task's other half is. Nothing here
// reaches the network or the CLI.
//
// **The goldens** are `test/e2e/__screenshots__/run/{light,dark}-<platform>.png`, in a
// subdirectory of the golden directory rather than beside the Live screen's two
// files for the reason `spend.spec.ts` records: `live.spec.ts` holds the
// directory to exactly its own two PNGs, and that file is not this task's to
// edit. The name is passed to `toHaveScreenshot` as an ARRAY (`['run',
// 'light.png']`): Playwright sanitizes a STRING name before
// `snapshotPathTemplate` applies, turning the `/` of `'run/light.png'` into `-`
// and writing `__screenshots__/run-light.png` beside the Live files. The test
// 'resolves the golden names under the run directory' asks Playwright itself
// where each name lands.
//
// **No PNG is committed**, as for the other three screens: macOS and Linux
// pixels differ, so a golden recorded on the wrong platform would compare
// nothing.
//
// **The frame is the mount, not the page** (`#run-mount`, the host this spec
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
//   cd console && npm run build && npx playwright test run.spec.ts --update-snapshots
//
// — the build is in the line because the specs read `dist/` and
// `npx playwright test` does not build, and on Linux it takes
// `xvfb-run --auto-servernum` (Electron needs a display);
// `test/support/goldens.ts`'s `recordCommand` is that line, per spec and per
// platform, and it is what a failed golden's message prints
//
// (owner-run, on the comparison's platform; commit `__screenshots__/run/`
// afterwards) and until they are committed the golden tests fail on purpose. The
// one way to run the rest of the file without them is the explicit
// `FUGARO_CONSOLE_GOLDENS=skip`, which skips exactly the golden tests and says
// so in the report; nothing skips them silently.

import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import type { JSONDoc } from '../../src/source/source'
import { RUN_LOG_CAP, RUN_THEMES } from '../../src/renderer/screens/run'
import { githubToken } from '../support/fakeSecrets'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { expectGolden, expectGrounded } from '../support/goldens'
import { launchConsole } from '../support/launch'

// The console's own directory, from this file's location: `test/e2e/x.spec.ts`
// is two levels below it.
const consoleDir = join(__dirname, '..', '..')
const goldenDir = join(consoleDir, 'test', 'e2e', '__screenshots__', 'run')

/** GOLDEN_NAMES are the names `toHaveScreenshot` is given: arrays, because
 * Playwright flattens a string's `/` (see the header). */
const GOLDEN_NAMES = {
  light: ['run', 'light.png'],
  dark: ['run', 'dark.png'],
} as const

/** THE_MOUNT is where this spec draws the screen, and therefore what the goldens
 * frame: the mount and never the page (see the header). */
const THE_MOUNT = '#run-mount'

/** SKIP_GOLDENS is the one documented way to run this file with no goldens
 * recorded: `FUGARO_CONSOLE_GOLDENS=skip`. Anything else, including unset, means
 * the golden tests run and a missing baseline fails them. */
const SKIP_GOLDENS = process.env['FUGARO_CONSOLE_GOLDENS'] === 'skip'

/** SPEC is this file's own name: what a re-record command has to name, and what
 * every golden failure here quotes back to the reader. */
const SPEC = 'run.spec.ts'

/** The compiled modules the screen is made of, in dependency order: the screen,
 * the writers in `renderer/app.js`, and the shared modules they use -- including
 * `shared/replay.js`, which is Task 2.3's other half and the one the replay
 * panel is drawn from. The registry below refuses a specifier that is not a
 * relative path, so a bundle that grew an import of `node:` or of a package
 * fails here rather than in a window with no Node in it. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/launcher.js',
  'shared/rigor.js',
  'shared/notice.js',
  'shared/version.js',
  'shared/stringify.js',
  'shared/replay.js',
  'renderer/app.js',
  'renderer/skeleton.js',
  'renderer/screens/reading.js',
  'renderer/section.js',
  'renderer/writes.js',
  'renderer/screens/run.js',
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
    createNS: function (tag) { return wrap(document.createElementNS('http://www.w3.org/2000/svg', tag)); },
    wrap: wrap,
  };
}
`

/** draw is the expression the window evaluates: load the compiled screen, build
 * the model from the diagnosis, the log window, the link and the ring, and mount
 * what it draws. */
function draw(
  diagnosis: JSONDoc,
  logs: readonly JSONDoc[],
  logLink: string | null,
  replay: readonly JSONDoc[],
  scrubAt: string | null,
  theme: string,
): string {
  return `(function () {
${LOADER}${MODULES.map(moduleSource).join('\n')}${FACTORY}
var run = load('/renderer/screens/run.js');
var factory = domFactory();
var model = run.buildRunModel({
  diagnosis: ${JSON.stringify(diagnosis)},
  logs: ${JSON.stringify(logs)},
  logLink: ${JSON.stringify(logLink)},
  replay: ${JSON.stringify(replay)},
  scrubAt: ${JSON.stringify(scrubAt)},
  sentences: {}
});
var tree = run.renderRun(factory, model, ${JSON.stringify(theme)});
var previous = document.getElementById('run-mount');
if (previous) previous.remove();
var host = document.createElement('div');
host.id = 'run-mount';
host.appendChild(tree.node);
document.body.appendChild(host);
return { mounted: true, stages: model.stages.length, logs: model.logs.lines.length };
})()`
}

/** Screen is the part of the drawn screen a test asserts on, read back out of
 * the live document. */
interface Screen {
  readonly theme: string | null
  readonly runId: string
  readonly status: string
  readonly flags: string
  readonly halt: string
  readonly ledgerModel: string
  readonly ledgerCompute: string
  readonly ledgerTotal: string
  /** the run's own spend by stage, from the document's top-level `stages` */
  readonly spendStageNames: readonly string[]
  readonly spendStageCalls: readonly string[]
  readonly spendStageAgents: readonly string[]
  readonly spendStageAgentReasons: readonly (string | null)[]
  readonly spendStageUsd: readonly string[]
  readonly spendStageNotional: readonly string[]
  /** the findings the run's mode tolerated, from the document's `tolerated`
   * block: the total the block carries and the entries it lists */
  readonly toleratedTotal: string
  readonly toleratedTitles: readonly string[]
  readonly toleratedModes: readonly string[]
  readonly stageNames: readonly string[]
  readonly stagePassed: readonly string[]
  readonly stageTests: readonly string[]
  readonly stageSpend: readonly string[]
  readonly stageSpendReasons: readonly (string | null)[]
  readonly stageDeadlines: readonly string[]
  readonly stageDeadlineReasons: readonly (string | null)[]
  /** the Rigor row's cells: empty strings and no areas for a record whose row
   * carries no block, which is the case the row is conditional on */
  readonly rigorMode: string
  readonly rigorFrom: string
  readonly rigorAreaNames: readonly string[]
  readonly rigorAreaModes: readonly string[]
  readonly prRepo: string
  readonly prNumber: string
  readonly prUrl: string
  /** prLine is the pull-request line's cells in drawing order: the three facts
   * and the separator between them. */
  readonly prLine: readonly string[]
  readonly prReason: string
  readonly logLines: number
  readonly logMessages: readonly string[]
  readonly logDropped: string
  readonly linkText: string
  readonly linkDestination: string | null
  readonly failedTests: readonly string[]
  readonly routes: readonly string[]
  readonly replayCount: string
  readonly replayWindow: string
  readonly replayFrom: string
  readonly replayTo: string
  readonly replayAt: string
  readonly replayNote: string
  readonly anchors: number
  readonly navigating: number
  readonly text: string
}

/** screen reads the mounted screen out of the page. */
function screen(page: Page): Promise<Screen> {
  return page.evaluate(() => {
    // The window's own entry draws the real dashboard into the same document
    // (Task 3.4c), beside this spec's mount: every reading is taken inside the
    // mount, so the entry's drawing can neither satisfy nor fail an assertion.
    const scope: ParentNode = document.getElementById('run-mount') ?? document.createDocumentFragment()
    const all = (selector: string, within?: Element): HTMLElement[] =>
      Array.from((within ?? scope).querySelectorAll<HTMLElement>(selector))
    const text = (selector: string): string => all(selector)[0]?.textContent ?? ''
    const texts = (selector: string): string[] => all(selector).map((one) => one.textContent ?? '')
    return {
      theme: scope.querySelector('.run')?.getAttribute('data-theme') ?? null,
      runId: text('.run-id'),
      status: text('.run-status'),
      flags: text('.run-flags'),
      halt: text('.run-halt'),
      ledgerModel: text('.run-ledger-model-value'),
      ledgerCompute: text('.run-ledger-compute-value'),
      ledgerTotal: text('.run-ledger-total-value'),
      spendStageNames: texts('.run-spend-stages .run-spend-stage-name'),
      spendStageCalls: texts('.run-spend-stages .run-spend-stage-calls'),
      spendStageAgents: texts('.run-spend-stages .run-spend-stage-agents'),
      // A stage whose `calls`/`agents` pointer the document left absent is not a
      // stage that counted zero: the cell says `n/a` and its title says which.
      spendStageAgentReasons: all('.run-spend-stages .run-spend-stage-agents').map((one) =>
        one.getAttribute('title'),
      ),
      spendStageUsd: texts('.run-spend-stages .run-spend-stage-usd'),
      // The part of the stage's model figure the plan covered, drawn with the
      // console's notional mark so the list price is never read as money.
      spendStageNotional: texts('.run-spend-stages .run-spend-stage-notional'),
      toleratedTotal: text('.run-tolerated-total'),
      toleratedTitles: texts('.run-tolerated-title'),
      toleratedModes: texts('.run-tolerated-mode'),
      stageNames: texts('.run-stages .run-stage-name'),
      stagePassed: texts('.run-stages .run-stage-passed'),
      stageTests: texts('.run-stages .run-stage-tests'),
      stageSpend: texts('.run-stages .run-stage-spend'),
      // The reason a stage's spend is `n/a`, read off the cell's title: the
      // cell says `n/a` and the title says what the document gives instead.
      stageSpendReasons: all('.run-stages .run-stage-spend').map((one) => one.getAttribute('title')),
      stageDeadlines: texts('.run-stages .run-stage-deadline'),
      stageDeadlineReasons: all('.run-stages .run-stage-deadline').map((one) => one.getAttribute('title')),
      rigorMode: text('.run-rigor-mode'),
      rigorFrom: text('.run-rigor-from'),
      rigorAreaNames: texts('.run-rigor-area-name'),
      rigorAreaModes: texts('.run-rigor-area-mode'),
      prRepo: text('.run-pr-repo'),
      prNumber: text('.run-pr-number'),
      prUrl: text('.run-pr-url'),
      prLine: texts('.run-pr > .run-pr-repo, .run-pr > .run-pr-number, .run-pr > .run-pr-sep, .run-pr > .run-pr-url'),
      prReason: text('.run-pr-reason'),
      logLines: all('.run-log-line').length,
      logMessages: texts('.run-log-message'),
      logDropped: text('.run-log-dropped'),
      linkText: text('.run-log-link'),
      // The destination is an attribute the main process owns -- `data-href` --
      // and never the navigating one: an element the renderer cannot navigate
      // with is an element that cannot navigate anywhere.
      linkDestination: all('.run-log-link')[0]?.getAttribute('data-href') ?? null,
      failedTests: texts('.run-failed-test'),
      routes: texts('.run-route-name'),
      replayCount: text('.run-replay-count'),
      replayWindow: text('.run-replay-window'),
      replayFrom: text('.run-replay-from'),
      replayTo: text('.run-replay-to'),
      replayAt: text('.run-replay-at'),
      replayNote: text('.run-replay-note'),
      anchors: all('a').length,
      // Counted inside the screen's own mount, which must exist (-1 otherwise,
      // so a missing mount cannot read as zero): since Task 3.4a the document
      // itself carries one `<script src="./console.js">`, the shell's and not
      // the screen's, and test/unit/csp-document.test.ts pins that list.
      navigating: ((mount) =>
        mount === null ? -1 : all('[href]', mount).length + all('[src]', mount).length)(
        document.getElementById('run-mount'),
      ),
      text: document.getElementById('run-mount')?.textContent ?? '',
    }
  })
}

// ---------------------------------------------------------------------------
// The documents.
// ---------------------------------------------------------------------------

/**
 * DIAGNOSIS is `diagnose RUN --json`: a finished run on a **Bitbucket**
 * repository, with the `pr` number and the `pr_url` -- and no provider, which is
 * design section 4.3's gap 7 and the reason the screen draws text and no link.
 * `verify` carries the two real `verify.Record`s the panel draws one row per.
 */
const DIAGNOSIS: JSONDoc = {
  project: 'example-project',
  row: {
    run: '20261015-110030-4a5b',
    run_id: '4a5b',
    repo: 'example-owner/example-repo',
    workflow: 'default',
    oauth_account: 'claude-ops@example.test',
    recipe: 'default',
    status: 'finished',
    stage: 'done',
    reason: 'the run finished',
    branch: 'fugaro/20261015-110030-4a5b',
    outcome: 'success',
    requested_by: 'operator@example.test',
    pr: 42,
    pr_url: 'https://bitbucket.org/example-owner/example-repo/pull-requests/42',
    log_url: 'https://console.cloud.google.com/batch/jobs/example-job',
    created: '2026-10-15T11:00:00Z',
    started_at: '2026-10-15T11:00:30Z',
    image_age_s: 3600,
    terminal: true,
    settled: true,
    follow_up: false,
    stale_draft: false,
    cost: {
      model_usd: 1.25,
      model_notional_usd: 0.42,
      compute_usd: 0.5,
      compute_estimated: true,
      total_usd: 1.75,
      model_basis: 'api-list',
    },
  },
  halt: { reason: 'the daily cap was reached', scope: 'repo', at: '2026-10-15T11:30:00Z' },
  verify: [
    {
      n: 1,
      kind: 'build',
      head_sha: '0f1e2d3c4b5a69788796a5b4c3d2e1f0a1b2c3d4',
      clean_tree: true,
      exit_code: 0,
      passed: true,
      tests: 0,
      failures: 0,
      skipped: 0,
      started_at: '2026-10-15T11:05:00Z',
      duration_s: 18,
    },
    {
      n: 2,
      kind: 'test',
      head_sha: '0f1e2d3c4b5a69788796a5b4c3d2e1f0a1b2c3d4',
      clean_tree: true,
      exit_code: 1,
      passed: false,
      tests: 1182,
      failures: 1,
      skipped: 2,
      started_at: '2026-10-15T11:10:00Z',
      duration_s: 42,
    },
  ],
  failed: ['TestSpend'],
  flaky: ['TestFlaky'],
  findings: [],
  log_tail: ['verify: TestSpend failed'],
  agent_message: 'the spend test needs the fixture regenerated',
  report_path: 'runs/example/20261015-110030-4a5b/report.json',
  route_by: { 'anthropic-api': 0.9 },
  model_by: { 'claude-sonnet-5': 1.25 },
  // The run's own per-stage record, whose `model_usd` is the ledger's model
  // figure split the way the CLI counted it (the three stages sum to the row's
  // 1.25, and their notional parts to the row's 0.42). `verify` carries no
  // `agents` pointer at all: the CLI's own `StageTiming` writes those as
  // pointers, so an absent one means the stage was not counted, not that it
  // counted zero. The two dollars are `omitempty` floats instead, and
  // `model_notional_usd` is the part of the stage's `model_usd` the plan covered
  // -- never more than it -- so `verify` with no notional key is a measured zero.
  stages: [
    { name: 'implement', duration_s: 612, calls: 412, agents: 3, model_usd: 0.8, model_notional_usd: 0.3 },
    { name: 'review', duration_s: 344, calls: 268, agents: 2, model_usd: 0.3, model_notional_usd: 0.12 },
    { name: 'verify', duration_s: 60, calls: 41, model_usd: 0.15 },
  ],
  // The mode let these through, and the block's `total` is above the two it
  // lists: the CLI bounds the list, so a shorter list is not the whole of it.
  // Each `label` is the score word the axes give (2x3 is Low, 2x1 is a Nit) and
  // each `class` is one of the classes the record uses.
  tolerated: {
    total: 3,
    findings: [
      {
        title: 'the retry ceiling differs between the two paths',
        label: 'low',
        mode: 'startup',
        likelihood: 2,
        impact: 3,
        fix_cost: 1,
        class: 'correctness',
      },
      {
        title: 'the table test misses the drain branch',
        label: 'nit',
        mode: 'startup',
        likelihood: 2,
        impact: 1,
        fix_cost: 1,
        class: 'maintainability',
      },
    ],
  },
  reported_usd: 1.2,
  charge_usd: 1.25,
}

/** LOG_LINES is `logs RUN --json`: one compact `logLine` object per line, as the
 * contract describes it (section 6.2). */
function logLines(count: number): readonly JSONDoc[] {
  return Array.from({ length: count }, (_, index) => ({
    time: `2026-10-15T11:${String(10 + (index % 40)).padStart(2, '0')}:00Z`,
    severity: 'info',
    stage: 'verify',
    message: `line ${String(index)}`,
  }))
}

/** LOG_LINK is `logs RUN --url`'s one text line: the full transcript. */
const LOG_LINK = 'https://console.cloud.google.com/batch/jobs/example-job/logs'

/** arrivals is a replay of three documents a few minutes apart, in the shape the
 * live feed hands a screen. */
const ARRIVALS: readonly JSONDoc[] = [
  { document: { project: 'example-project', day: '2026-10-15', counted_usd: 0.5, sources: [] }, receivedAt: '2026-10-15T12:00:00Z', notices: [] },
  { document: { project: 'example-project', day: '2026-10-15', counted_usd: 1.0, sources: [] }, receivedAt: '2026-10-15T12:04:00Z', notices: [] },
  { document: { project: 'example-project', day: '2026-10-15', counted_usd: 1.5, sources: [] }, receivedAt: '2026-10-15T12:10:00Z', notices: [] },
]

test.describe('the Run detail screen', () => {
  // One window, one page, and the tests in order: each draws the screen it is
  // about into the same document, and the goldens are taken from what the test
  // before them mounted. The page is the console's *current* window (`pageNow`)
  // rather than the one `beforeAll` saw, because the console replaces its window
  // -- closing that one -- when a lifecycle decision lands.
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // The app reads a `fugaro` before it opens a window (plan Task 3.3): without
    // an in-range one it opens the refusal document, and the dashboard document
    // this spec mounts into would never be loaded.
    cli = inRangeCliStub()
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

  /** pageNow is the page of the window the console is showing now.
   *
   * It is asked for per mount instead of kept from `beforeAll`: the console
   * answers a lifecycle decision by opening the window it switches to and
   * closing the one it replaces (`src/main/window.ts`), so a handle taken once
   * names a window that can be gone by the time a later case in this serial
   * describe reads through it -- which is `Target page, context or browser has
   * been closed` on whichever case is reading when it goes.
   *
   * No window at all is a failure at once and in the console's own words --
   * the sentence every neighbouring spec's mount answers with -- rather than a
   * wait on a window that is not coming. */
  async function pageNow(): Promise<Page> {
    if (!page.isClosed()) return page
    const open = app.windows().find((candidate) => !candidate.isClosed())
    if (open === undefined) throw new Error('the console has no window')
    page = open
    await page.waitForLoadState('domcontentloaded')
    return page
  }

  /** mount draws the run and fails with the window's own words if the bundle
   * could not be loaded or the screen refused to draw. */
  async function mount(
    diagnosis: JSONDoc,
    logs: readonly JSONDoc[],
    logLink: string | null,
    replay: readonly JSONDoc[],
    scrubAt: string | null,
    theme: string,
  ): Promise<void> {
    const shown = await pageNow()
    // The window the draw goes into is the window this case reads back through:
    // it is taken from the page rather than as `getAllWindows()[0]`, so a mount
    // cannot be drawn into one window and read out of another.
    const window = await app.browserWindow(shown)
    const result = (await window.evaluate(
      async (
        held: { webContents: { executeJavaScript(source: string, userGesture: boolean): Promise<unknown> } },
        source: string,
      ) => (await held.webContents.executeJavaScript(source, true)) as { error?: string },
      draw(diagnosis, logs, logLink, replay, scrubAt, theme),
    )) as { error?: string }
    expect(result.error).toBeUndefined()
  }

  test('draws the ledger, the outcome and the halt', async () => {
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, null, 'light')
    const drawn = await screen(page)

    expect(drawn.theme).toBe('light')
    expect(drawn.runId).toBe('20261015-110030-4a5b')
    expect(drawn.status).toBe('finished')
    // The three accounts, apart: the billed model figure (the row's `model_usd`
    // less the part its plan paid), the estimate by design, and the total the
    // table charged.
    expect(drawn.ledgerModel).toBe('$0.83')
    expect(drawn.ledgerCompute).toBe('est. $0.50')
    expect(drawn.ledgerTotal).toBe('$1.75')
    // A halting is the money decision this console exists to explain, so it is
    // on the header rather than buried in the diagnosis view.
    expect(drawn.halt).toContain('HALTED')
    expect(drawn.halt).toContain('the daily cap was reached')
    // The terminal/settled pair is what says whether these numbers can move.
    expect(drawn.flags).toContain('terminal')
    expect(drawn.flags).toContain('settled')
  })

  test('draws the run\'s rigor row, and no row at all for a record with no block', async () => {
    // Owner request 2026-10-25: the run detail is the one screen with room for
    // the whole answer -- the mode, the layer that decided it and every rule
    // that matched -- where the Projects cell and the Live chip are one word.
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, null, 'light')
    const plain = await screen(page)
    expect(plain.rigorMode).toBe('')
    expect(plain.rigorAreaNames).toEqual([])
    expect(plain.text).not.toContain('RIGOR')

    // `DIAGNOSIS`'s row is a run from before the field, so the block is added
    // to it the way a 0.11.0 runner writes it -- the record's own two stamps
    // included, because a real block carries them and the row draws neither.
    const row = DIAGNOSIS['row'] as JSONDoc
    const withRigor: JSONDoc = {
      ...DIAGNOSIS,
      row: {
        ...row,
        rigor: {
          mode: 'critical',
          from: 'area',
          areas: [
            { name: 'payments', mode: 'critical', at: '2026-10-15T11:00:05Z' },
            { name: 'billing', mode: 'pro', at: '2026-10-15T11:00:05Z' },
          ],
          at: '2026-10-15T11:00:05Z',
          revision: 'origin/main',
        },
      },
    }
    await mount(withRigor, logLines(3), LOG_LINK, ARRIVALS, null, 'light')
    const drawn = await screen(page)
    expect(drawn.rigorMode).toBe('Critical')
    // The layer in the console's words, not the record's enum, and the areas as
    // the repository named them.
    expect(drawn.rigorFrom).toBe('an area rule')
    expect(drawn.rigorAreaNames).toEqual(['payments', 'billing'])
    expect(drawn.rigorAreaModes).toEqual(['Critical', 'Pro'])
    expect(drawn.text).toContain('RIGOR')
    // The two stamps are in the record and in no cell of the screen.
    expect(drawn.text).not.toContain('origin/main')
    expect(drawn.text).not.toContain('2026-10-15T11:00:05Z')
  })

  test('draws the stage timeline, whose spend and deadline no verify record carries', async () => {
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, null, 'light')
    const drawn = await screen(page)

    expect(drawn.stageNames).toEqual(['build', 'test'])
    expect(drawn.stagePassed).toEqual(['passed', 'failed'])
    expect(drawn.stageTests).toEqual(['0', '1,182'])
    // Per-stage spend is on no verify record: the run's spend by stage is the
    // document's top-level `stages`, which the panel above the timeline draws,
    // so the cell says `n/a` and its title names that panel rather than
    // inventing a split.
    expect(drawn.stageSpend).toEqual(['n/a', 'n/a'])
    for (const reason of drawn.stageSpendReasons) {
      expect(reason).toContain('spend by stage')
    }
    // A `runview.Row` carries no deadline, so each stage draws the reason in
    // the cell's title -- the same two outcomes `putText` has: the safe thing
    // as itself, a refusal as the console's sentence.
    expect(drawn.stageDeadlines).toEqual(['n/a', 'n/a'])
    for (const reason of drawn.stageDeadlineReasons) {
      expect(reason).toContain('row.deadline')
    }
  })

  test('draws the run’s spend by stage and the findings the mode tolerated', async () => {
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, null, 'light')
    const drawn = await screen(page)

    expect(drawn.spendStageNames).toEqual(['implement', 'review', 'verify'])
    // The calls and the sub-agents a stage was counted at, and the dollars its
    // model spend came to: every figure is one the document wrote.
    expect(drawn.spendStageCalls).toEqual(['412', '268', '41'])
    expect(drawn.spendStageAgents).toEqual(['3', '2', 'n/a'])
    // The usd cell is the **billed** part of the stage's figure, which is the
    // stage's `model_usd` less the part the plan paid -- never the whole figure,
    // which would add billed and notional money on one row.
    expect(drawn.spendStageUsd).toEqual(['$0.50', '$0.18', '$0.15'])
    // The notional part is the plan's list price inside the stage's figure, so
    // it carries the console's notional mark -- and `verify`, with no notional
    // key at all, is a measured zero rather than a marked one.
    expect(drawn.spendStageNotional).toEqual(['~$0.30', '~$0.12', '$0.00'])
    // `verify` carries no `agents` pointer, which is not a stage that counted
    // zero: the cell says `n/a` and its title says what the absence means.
    expect(drawn.spendStageAgentReasons[2]).toContain('not counted for this stage')
    expect(drawn.spendStageAgentReasons[0]).toBeNull()

    // The mode's own word for each finding, and the block's total drawn beside
    // the entries rather than replaced by their count.
    expect(drawn.toleratedTotal).toContain('3')
    expect(drawn.toleratedTitles).toEqual([
      'the retry ceiling differs between the two paths',
      'the table test misses the drain branch',
    ])
    expect(drawn.toleratedModes).toEqual(['Startup', 'Startup'])
  })

  test('draws a deadline the row does carry, and only then', async () => {
    const withDeadline: JSONDoc = {
      ...DIAGNOSIS,
      row: { ...(DIAGNOSIS['row'] as JSONDoc), deadline: 'near' },
    }
    await mount(withDeadline, logLines(3), LOG_LINK, ARRIVALS, null, 'light')
    const drawn = await screen(page)

    // `JSONRun.deadline` is the CLI's own word (`ok`/`near`/`over`), drawn as
    // the CLI worded it and never parsed.
    expect(drawn.stageDeadlines).toEqual(['near', 'near'])
  })

  test('shows the pull request as text, and builds no link from it', async () => {
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, null, 'light')
    const drawn = await screen(page)

    // Design section 4.3's gap 7: no document names the provider, so a link
    // rebuilt from the repository and the number would name a repository this
    // run does not use -- `example-owner/example-repo` is Bitbucket, and a
    // `github.com` URL for it would pass this console's own host allow-list.
    // The reason is design section 5.1 rule 2's own sentence, drawn whole.
    expect(drawn.prRepo).toBe('example-owner/example-repo')
    expect(drawn.prNumber).toBe('#42')
    expect(drawn.prUrl).toBe('https://bitbucket.org/example-owner/example-repo/pull-requests/42')
    expect(drawn.prReason).toBe(
      'this CLI does not say which provider this run’s repository is on, so the console will not build a link from it',
    )
    // The three facts are one line and the cells between them are theirs, so
    // they are drawn apart (`acme/platform-core#412https://...` was the defect
    // the owner reported on 2026-10-21).
    expect(drawn.prLine).toEqual([
      'example-owner/example-repo',
      ' · ',
      '#42',
      ' · ',
      'https://bitbucket.org/example-owner/example-repo/pull-requests/42',
    ])
    // Nothing in the pull request panel is a link or a destination.
    expect(drawn.text).not.toContain('github.com')
  })

  test('bounds the log window and links to the transcript by destination attribute', async () => {
    await mount(DIAGNOSIS, logLines(RUN_LOG_CAP + 25), LOG_LINK, ARRIVALS, null, 'light')
    const drawn = await screen(page)

    // Exactly the cap is drawn, and what the cap left out is a reading: a panel
    // that silently ended would look like the whole transcript.
    expect(drawn.logLines).toBe(RUN_LOG_CAP)
    expect(drawn.logDropped).toBe('25')
    expect(drawn.logMessages[0]).toBe('line 25')
    // The link is a text and a `data-href`, never a navigating attribute: the
    // main process owns the allow-list and opening, the renderer does not.
    expect(drawn.linkText.length).toBeGreaterThan(0)
    expect(drawn.linkDestination).toBe(LOG_LINK)
    expect(drawn.navigating).toBe(0)
  })

  test('draws a document\'s log line through the sanitizer', async () => {
    const lines = [
      ...logLines(2),
      {
        time: '2026-10-15T11:12:00Z',
        severity: 'error',
        stage: 'verify',
        // A job wrote this line, and a job can write a credential into it: the
        // `sentence` class is what redacts one, in place, the same way the
        // recorder does.
        message: `using ${githubToken()}`,
      },
    ]
    await mount(DIAGNOSIS, lines, LOG_LINK, ARRIVALS, null, 'light')
    const drawn = await screen(page)

    expect(drawn.logMessages[2]).toBe('using [redacted-token]')
    // The token itself is in no cell of the tree -- not as text, and not as a
    // title either.
    expect(drawn.text).not.toContain(githubToken())
  })

  test('draws the diagnosis: the failed tests and the spend splits', async () => {
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, null, 'light')
    const drawn = await screen(page)

    expect(drawn.failedTests).toEqual(['TestSpend'])
    // The two splits `diagnose` produces, which are what a per-stage spend
    // would have been read from: they are the document's own account, drawn as
    // they came. Since step 3b they are drawn in the ledger, under the
    // reconciliation, rather than in the diagnosis -- so the same two splits are
    // read off the same hooks from their new place in the screen.
    expect(drawn.routes).toEqual(['anthropic-api'])
    expect(drawn.text).toContain('claude-sonnet-5')
    expect(drawn.text).toContain('Providers reported')
  })

  test('draws the replay buffer: its bound, its span and the moment scrubbed to', async () => {
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, '2026-10-15T12:05:00Z', 'light')
    const drawn = await screen(page)

    expect(drawn.replayCount).toBe('3')
    expect(drawn.replayWindow).toBe('900 s')
    expect(drawn.replayFrom).toBe('2026-10-15T12:00:00Z')
    expect(drawn.replayTo).toBe('2026-10-15T12:10:00Z')
    // The moment scrubbed to is the newest frame at or before it, which is the
    // view the console was showing then -- the debugging aid for "the run was
    // slow at 14:20".
    expect(drawn.replayAt).toBe('2026-10-15T12:04:00Z')
    expect(drawn.replayNote).toContain('15 minutes')
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
    await expectGrounded(await pageNow(), (theme) => mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, null, theme), {
      themes: RUN_THEMES,
      mount: THE_MOUNT,
      text: 'h1.run-id',
      screen: 'the Run detail screen',
    })
  })

  test('draws the run screen', async ({}, testInfo) => {
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, '2026-10-15T12:05:00Z', 'light')
    await expectGolden(page.locator(THE_MOUNT), GOLDEN_NAMES.light, { spec: SPEC, consoleDir, testInfo })
  })

  test('draws the run screen in the dark theme', async ({}, testInfo) => {
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, null, 'dark')
    const drawn = await screen(page)
    expect(drawn.theme).toBe('dark')
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await expectGolden(page.locator(THE_MOUNT), GOLDEN_NAMES.dark, { spec: SPEC, consoleDir, testInfo })
  })

  test('resolves the golden names under the run directory', ({}, testInfo) => {
    // Not a golden test: it asks Playwright where a recording would land, so a
    // name the template flattens is red before anyone records anything.
    expect(testInfo.snapshotPath(...GOLDEN_NAMES.light)).toBe(join(goldenDir, `light-${process.platform}.png`))
    expect(testInfo.snapshotPath(...GOLDEN_NAMES.dark)).toBe(join(goldenDir, `dark-${process.platform}.png`))
  })

  test('has a golden for each theme the design names', () => {
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
      RUN_THEMES.map((theme) => `${theme}-${process.platform}.png`).sort(),
    )
    for (const file of present) expect(file).toMatch(new RegExp(`^(${RUN_THEMES.join('|')})-(darwin|linux)\\.png$`))
  })

  test('keeps its state words and its row boundary under forced colors (design section 6.4)', async () => {
    // The Run detail screen's half of the forced-colors smoke (section 4.4, in the half
    // section 6.4 gives this task). This is the densest table in the console: a stage
    // cell that is `n/a` because no verify record carries spend, a deadline that is `n/a`,
    // and the halt's own `HALTED` -- each a word rather than a tone, so each survives.
    // The chrome's half is `entry.spec.ts`'s; a smoke, not a golden.
    await mount(DIAGNOSIS, logLines(4), LOG_LINK, ARRIVALS, null, 'light')
    await page.emulateMedia({ forcedColors: 'active', colorScheme: 'light' })
    expect(await page.evaluate(() => matchMedia('(forced-colors: active)').matches)).toBe(true)

    const forced = await page.evaluate(() => {
      const scope: ParentNode = document.getElementById('run-mount') ?? document.createDocumentFragment()
      const row = scope.querySelector('li.run-log-line') ?? scope.querySelector('li.run-stage')
      const computed = row === null ? null : getComputedStyle(row)
      const number = scope.querySelector('.run-number')
      return {
        words: document.getElementById('run-mount')?.textContent ?? '',
        rowVisibility: computed?.visibility ?? null,
        rowWidth: computed?.borderBottomWidth ?? null,
        rowColour: computed?.borderBottomColor ?? null,
        linkColour: (() => {
          const link = scope.querySelector('a.run-log-link')
          return link === null ? null : getComputedStyle(link).color
        })(),
        tabular: number === null ? null : getComputedStyle(number).fontVariantNumeric,
      }
    })

    expect(forced.words, 'the halt lost its word').toContain('HALTED')
    expect(forced.words, 'a stage with no spend lost its mark').toContain('n/a')
    expect(forced.rowVisibility).toBe('visible')
    expect(forced.rowWidth, 'the log row’s boundary collapsed under forced colors').not.toBe('0px')
    expect(forced.rowColour).not.toBe('rgba(0, 0, 0, 0)')
    // The link the console is allowed to draw keeps a colour the system names, which is
    // the one thing the palette hands over rather than paints (section 4.4's second rule).
    expect(forced.linkColour).not.toBe('rgba(0, 0, 0, 0)')
    expect(forced.tabular, 'the numbers lost their tabular figures').toContain('tabular-nums')

    await page.emulateMedia({ forcedColors: 'none' })
  })
})
