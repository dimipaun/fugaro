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
// `updateSnapshots: 'none'`, so a missing baseline FAILS. They are recorded by
// an owner, on the platform the comparison runs on (macOS and Linux pixels
// differ), with
//
//   npx playwright test run.spec.ts --update-snapshots
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

/** The compiled modules the screen is made of, in dependency order: the screen,
 * the writers in `renderer/app.js`, and the shared modules they use -- including
 * `shared/replay.js`, which is Task 2.3's other half and the one the replay
 * panel is drawn from. The registry below refuses a specifier that is not a
 * relative path, so a bundle that grew an import of `node:` or of a package
 * fails here rather than in a window with no Node in it. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/version.js',
  'shared/stringify.js',
  'shared/replay.js',
  'renderer/app.js',
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
  readonly stageNames: readonly string[]
  readonly stagePassed: readonly string[]
  readonly stageTests: readonly string[]
  readonly stageSpend: readonly string[]
  readonly stageSpendReasons: readonly (string | null)[]
  readonly stageDeadlines: readonly string[]
  readonly stageDeadlineReasons: readonly (string | null)[]
  readonly prRepo: string
  readonly prNumber: string
  readonly prUrl: string
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
      stageNames: texts('.run-stages .run-stage-name'),
      stagePassed: texts('.run-stages .run-stage-passed'),
      stageTests: texts('.run-stages .run-stage-tests'),
      stageSpend: texts('.run-stages .run-stage-spend'),
      // The reason a stage's spend is `n/a`, read off the cell's title: the
      // cell says `n/a` and the title says what the document gives instead.
      stageSpendReasons: all('.run-stages .run-stage-spend').map((one) => one.getAttribute('title')),
      stageDeadlines: texts('.run-stages .run-stage-deadline'),
      stageDeadlineReasons: all('.run-stages .run-stage-deadline').map((one) => one.getAttribute('title')),
      prRepo: text('.run-pr-repo'),
      prNumber: text('.run-pr-number'),
      prUrl: text('.run-pr-url'),
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
    cost: { model_usd: 1.25, compute_usd: 0.5, compute_estimated: true, total_usd: 1.75, model_basis: 'api-list' },
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
    const result = await app.evaluate(async ({ BrowserWindow }, source) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(source, true)) as { error?: string }
    }, draw(diagnosis, logs, logLink, replay, scrubAt, theme))
    expect(result.error).toBeUndefined()
  }

  test('draws the ledger, the outcome and the halt', async () => {
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, null, 'light')
    const drawn = await screen(page)

    expect(drawn.theme).toBe('light')
    expect(drawn.runId).toBe('20261015-110030-4a5b')
    expect(drawn.status).toBe('finished')
    // The three accounts, apart: the billed model figure, the estimate by
    // design, and the total the table charged.
    expect(drawn.ledgerModel).toBe('$1.25')
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

  test('draws the stage timeline, with the spend and deadline no document carries', async () => {
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, null, 'light')
    const drawn = await screen(page)

    expect(drawn.stageNames).toEqual(['build', 'test'])
    expect(drawn.stagePassed).toEqual(['passed', 'failed'])
    expect(drawn.stageTests).toEqual(['0', '1,182'])
    // Per-stage spend is on no document this tree produces: `diagnose` reports
    // it by route and by model, so the cell says `n/a` and its title says what
    // the document gives instead rather than inventing a split.
    expect(drawn.stageSpend).toEqual(['n/a', 'n/a'])
    for (const reason of drawn.stageSpendReasons) {
      expect(reason).toContain('by route and by model')
    }
    // A `runview.Row` carries no deadline, so each stage draws the reason in
    // the cell's title -- the same two outcomes `putText` has: the safe thing
    // as itself, a refusal as the console's sentence.
    expect(drawn.stageDeadlines).toEqual(['n/a', 'n/a'])
    for (const reason of drawn.stageDeadlineReasons) {
      expect(reason).toContain('row.deadline')
    }
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
    expect(drawn.prRepo).toBe('example-owner/example-repo')
    expect(drawn.prNumber).toBe('#42')
    expect(drawn.prUrl).toBe('https://bitbucket.org/example-owner/example-repo/pull-requests/42')
    expect(drawn.prReason).toContain('no document names the provider')
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
    // they came.
    expect(drawn.routes).toEqual(['anthropic-api'])
    expect(drawn.text).toContain('claude-sonnet-5')
    expect(drawn.text).toContain('the providers reported:')
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

  test('draws the run screen', async () => {
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, '2026-10-15T12:05:00Z', 'light')
    await expect(page.locator(THE_MOUNT)).toHaveScreenshot(GOLDEN_NAMES.light)
  })

  test('draws the run screen in the dark theme', async () => {
    await mount(DIAGNOSIS, logLines(3), LOG_LINK, ARRIVALS, null, 'dark')
    const drawn = await screen(page)
    expect(drawn.theme).toBe('dark')
    test.skip(SKIP_GOLDENS, 'FUGARO_CONSOLE_GOLDENS=skip: goldens not recorded yet')
    await expect(page.locator(THE_MOUNT)).toHaveScreenshot(GOLDEN_NAMES.dark)
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
})
