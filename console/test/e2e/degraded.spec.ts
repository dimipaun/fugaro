// The four conditions of design section 6.3 in a real window (desktop-console
// plan Task 3.1).
//
// The design says what the console does when it has something to draw; this is
// what it does when it does not: the CLI's own no-Firebase mode, a feed that
// has gone quiet, no `fugaro` at all, and a read the CLI refused. `degraded.ts`
// draws the last three (the third and fourth are its "no source at all" screens
// and the first is its degraded document screen) and `live.ts` draws the second
// as a banner over the Live screen, because a stale feed is still a dashboard:
// the last values stay, with the timestamps that say when they were read.
//
// **Why `executeJavaScript` and not a page.** Exactly as in `live.spec.ts`,
// and for the same reason: Task 2.1's file list is the screen and its tests,
// and the entry point that would mount a screen on load (`src/renderer/app.js`
// in a page, `src/main/index.ts`'s `rendererRoot`) belongs to another task. So
// this spec does what the app will do, from outside the app: it loads the
// compiled renderer out of `dist/` and calls the screens with a DOM-backed
// element factory of its own. `webContents.executeJavaScript` is what makes
// that possible under the console's real CSP, which refuses both an inline
// `<script>` and Playwright's `page.evaluate`.
//
// **The goldens.** This file takes none, on purpose: Task 3.1's file list names
// no `test/e2e/__screenshots__` frame, and `live.spec.ts`'s own test holds that
// directory to the Live screen's two, so a third PNG appearing here would fail
// that test rather than be reviewed. What is asserted instead is the drawn tree:
// the states, the marks, and the two things design section 6.3 is really about
// -- a cap the console cannot read is `n/a` and never `$0.00`, and a refusal is
// on screen character for character.
//
// Nothing here reaches the network. The no-Firebase screen is drawn from the
// committed synthetic fixture, read from disk and already redacted when it was
// written (Task 1.4); the absent-binary screen is built from a resolution run
// against an empty temporary directory; and the refusal is the sentence
// `internal/cli/report.go` prints, carried the way the console carries it.

import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { resolveBinary } from '../../src/main/spawn'
import {
  FEED_STALE_AFTER_SECONDS,
  FUGARO_INSTALL_COMMAND,
  feedSilence,
  noInstallationState,
} from '../../src/source/cli'
import type { FeedSilence, NoInstallationState } from '../../src/source/cli'
import { loadFixture } from '../../src/source/fixture'
import { refusalFrom } from '../../src/source/source'
import type { JSONDoc } from '../../src/source/source'
import { TOKEN_PLACEHOLDER } from '../../src/shared/sanitize'
import { githubToken } from '../support/fakeSecrets'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'
import { LIVE_STALE_AFTER_SECONDS } from '../../src/renderer/screens/live'
import type { FeedHealth } from '../../src/renderer/screens/live'
import { NO_BINARY_HEADLINE, refusedState } from '../../src/renderer/screens/degraded'
import type { NoBinaryState, RefusedState } from '../../src/renderer/screens/degraded'

// The console's own directory, from this file's location: `test/e2e/x.spec.ts`
// is two levels below it.
const consoleDir = join(__dirname, '..', '..')
const fixturePath = join(consoleDir, 'test', 'fixtures', 'degraded.ndjson')

/** MODULES are the compiled modules these screens are made of, in dependency
 * order. `schema.js` is here for its decoder, `live.js` because the stale
 * condition is drawn by the Live screen and because the degraded screen is a
 * sibling of it, and `degraded.js` for the rest. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/version.js',
  'shared/schema.js',
  'renderer/app.js',
  'renderer/screens/live.js',
  'renderer/screens/degraded.js',
])

/** moduleSource is one compiled module as a registry entry. */
function moduleSource(relative: string): string {
  const code = readFileSync(join(consoleDir, 'dist', relative), 'utf8')
  return `registry[${JSON.stringify(`/${relative}`)}] = function (exports, require, module) {\n${code}\n};`
}

/** LOADER is the module registry the injected bundle runs on: a CommonJS
 * `require` that resolves `./` and `../` specifiers and nothing else, so a
 * bundle that grew an import of `node:` or of a package fails here rather than
 * in a window with no Node in it. Unchanged from `live.spec.ts`, deliberately:
 * this is the same window and the same constraint. */
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

/** FACTORY is a DOM-backed `ScreenFactory`: one wrapper per element, so the
 * screen's `ScreenElement` is served by a real node while staying exactly as
 * narrow as the interface the unit tests hand it. Unchanged from
 * `live.spec.ts`. */
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

/** drawDegraded is the no-Firebase screen: one document of the fixture, the
 * notices that arrived beside it, and the console's own silence reading. */
function drawDegraded(
  document: JSONDoc,
  notices: readonly string[],
  feed: FeedHealth | null,
  theme: string,
): string {
  return bundle(`
var schema = load('/shared/schema.js');
var degraded = load('/renderer/screens/degraded.js');
var decoded = schema.decodeDocument(${JSON.stringify(document)}, 'the fixture frame');
if (!decoded.ok) return { error: decoded.skew.because };
var model = degraded.buildNoFirebaseModel(decoded.value, ${JSON.stringify(notices.map((text) => ({ text })))}, ${JSON.stringify(feed)});
mount(degraded.renderNoFirebase(domFactory(), model, ${JSON.stringify(theme)}));
return { mounted: true };
`)
}

/** drawLive is the Live screen with a feed reading: every frame of the fixture
 * observed into one burn window, in the order they arrived, and the last of them
 * drawn while the console has heard nothing for longer than the threshold (or
 * with `feed` null, which is the caller that keeps no reading at all -- every
 * drawing the earlier specs and the goldens take). This is the condition the
 * design states on the Live screen rather than on a screen of its own. */
function drawLive(frames: readonly JSONDoc[], feed: FeedHealth | null, theme: string): string {
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
var model = live.buildLiveModel(last, history, ${JSON.stringify(feed)});
mount(live.renderLive(domFactory(), model, ${JSON.stringify(theme)}));
return { mounted: true };
`)
}

/** drawUnavailable is the console's screen when there is no source to read: no
 * binary, or a read the CLI refused. It also asks the window what each of the
 * two doors would do with the refusal sentence -- the `sentence` class, which
 * would eat the remedy, and `sanitizeRefusal`, which is the one the screen
 * uses. That is the assertion that explains why the refusal is not drawn
 * through `putText`, read off the running renderer rather than argued here.
 *
 * The state is built the way a caller builds it: `refusedState` is what runs
 * the refusal door, so a test that wants the raw sentence for the comparison
 * passes its own state and reads `drawnRefusal` back. */
function drawUnavailable(state: NoBinaryState | RefusedState, theme: string): string {
  return bundle(`
var sanitize = load('/shared/sanitize.js');
var degraded = load('/renderer/screens/degraded.js');
mount(degraded.renderUnavailable(domFactory(), ${JSON.stringify(state)}, ${JSON.stringify(theme)}));
var sentenced = ${JSON.stringify(state.kind === 'refused' ? state.sentence : '')};
return {
  mounted: true,
  sanitized: sanitize.sanitizeText(sentenced, 'sentence', 'the CLI sentence').text,
  refusalDrawn: sanitize.sanitizeRefusal(sentenced, 'the CLI refusal sentence').text,
  refusedState: degraded.refusedState('budget.show', { sentence: sentenced }).sentence
};
`)
}

/** Drawn is the part of a mounted screen a test asserts on, read back out of
 * the live document. Every selector that is not there answers `null` or `[]`,
 * which is how "no dashboard" is asserted: nothing on the screen draws one. */
interface Drawn {
  readonly kind: string | null
  readonly theme: string | null
  readonly receivedAt: string | null
  readonly sections: number
  readonly liveRoots: number
  readonly degradedRoots: number
  readonly headline: string | null
  readonly project: string | null
  readonly mode: string | null
  readonly modeState: string | null
  readonly notice: string | null
  readonly warnings: readonly string[]
  readonly caps: string | null
  readonly capsState: string | null
  readonly killCapable: string | null
  readonly killable: number
  readonly runIds: readonly string[]
  readonly runComputes: readonly string[]
  readonly notionalRows: number
  readonly refusal: string | null
  readonly operation: string | null
  readonly installCommand: string | null
  readonly searched: string | null
  readonly stale: string | null
  readonly staleSilence: string | null
  readonly staleThreshold: string | null
  readonly staleAt: string | null
  readonly sinceAt: string | null
  readonly counted: string | null
  readonly segments: string | null
  readonly bars: readonly (string | null)[]
  readonly gaps: number
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
    const texts = (selector: string): string[] => all(selector).map((one) => one.textContent ?? '')
    const attributes = (selector: string, name: string): (string | null)[] =>
      all(selector).map((one) => one.getAttribute(name))
    const root = scope.querySelector('#console-mount > section')
    return {
      kind: root?.getAttribute('data-kind') ?? null,
      theme: root?.getAttribute('data-theme') ?? null,
      receivedAt: root?.getAttribute('data-received-at') ?? null,
      sections: all('#console-mount > section').length,
      liveRoots: all('.live').length,
      degradedRoots: all('.degraded').length,
      headline: scope.querySelector('.unavailable-headline')?.textContent ?? null,
      project: scope.querySelector('.degraded-project')?.textContent ?? null,
      mode: scope.querySelector('.degraded-mode')?.textContent ?? null,
      modeState: scope.querySelector('.degraded-mode')?.getAttribute('data-state') ?? null,
      notice: scope.querySelector('.degraded-notice')?.textContent ?? null,
      warnings: texts('.degraded-warning'),
      caps: scope.querySelector('.degraded-cap')?.textContent ?? null,
      capsState: scope.querySelector('.degraded-cap')?.getAttribute('data-state') ?? null,
      killCapable: scope.querySelector('.degraded-caps')?.getAttribute('data-kill-capable') ?? null,
      killable: all('[data-killed], .degraded-kill').length,
      runIds: texts('.degraded-run-id'),
      runComputes: texts('.degraded-run-compute'),
      notionalRows: all('.degraded-run[data-notional-model="true"]').length,
      refusal: scope.querySelector('.unavailable-sentence')?.textContent ?? null,
      operation: scope.querySelector('.unavailable-operation')?.textContent ?? null,
      installCommand: scope.querySelector('.unavailable-install-command')?.textContent ?? null,
      searched: scope.querySelector('.unavailable-searched-count')?.textContent ?? null,
      stale: scope.querySelector('.live-stale')?.getAttribute('data-stale') ?? null,
      staleSilence: scope.querySelector('.live-stale')?.getAttribute('data-silence') ?? null,
      staleThreshold: scope.querySelector('.live-stale-threshold')?.textContent ?? null,
      staleAt: scope.querySelector('.live-stale-at')?.textContent ?? null,
      sinceAt: scope.querySelector('.live-since-at')?.textContent ?? null,
      counted: scope.querySelector('.live-counted')?.textContent ?? null,
      segments: scope.querySelector('.live-burn')?.getAttribute('data-segments') ?? null,
      bars: attributes('.live-burn-bar', 'data-state'),
      gaps: all('.live-burn-bar[data-state="gap"]').length,
      anchors: all('a').length,
      buttons: all('button').length,
      text: document.getElementById('console-mount')?.textContent ?? '',
    }
  })
}

/** records is the committed fixture's lines, in order, exactly as the writer
 * wrote them. */
function records(): ReturnType<typeof loadFixture>['records'] {
  return loadFixture(readFileSync(fixturePath, 'utf8'), 'test/fixtures/degraded.ndjson').records
}

/** theDegradedFrame is the fixture's first document: the degraded shape, two
 * run records, one of them a subscription's and one whose compute was not
 * estimated. */
function theDegradedFrame(): JSONDoc {
  for (const record of records()) {
    if ('document' in record) return record.document
  }
  throw new Error('the degraded fixture has no document')
}

/** theNotice is the non-JSON line the fixture records: the CLI's own sentence
 * for this mode, followed by the retrying line the reader must skip and keep
 * (contract section 10.8). */
function theNotice(): string {
  for (const record of records()) {
    if ('notice' in record) return record.notice
  }
  throw new Error('the degraded fixture has no notice')
}

/** theLiveFrames is the synthetic fixture's documents, which are the frames the
 * stale banner is drawn over: a Live screen with a feed that has stopped. */
function theLiveFrames(): readonly JSONDoc[] {
  const path = join(consoleDir, 'test', 'fixtures', 'synthetic.ndjson')
  const out: JSONDoc[] = []
  for (const record of loadFixture(readFileSync(path, 'utf8'), 'test/fixtures/synthetic.ndjson').records) {
    if ('document' in record) out.push(record.document)
  }
  if (out.length === 0) throw new Error('the synthetic fixture has no documents')
  return out
}

/** THE_REFUSAL is `internal/cli/report.go`'s `firestoreErr` for a permission
 * refusal, which is the sentence the CLI prints and the console shows: the read
 * that failed, the two roles that remedy it, and the error the client
 * returned. It is written out here because it is the *input* to this spec --
 * what is asserted is that the screen draws it unchanged. */
/** THE_TOKEN is a credential-shaped value assembled at run time -- never
 * written down, see `test/support/fakeSecrets.ts` -- used for the one assertion
 * that a refusal which echoed a token does not put it on the screen. */
const THE_TOKEN = githubToken()

const THE_REFUSAL =
  "project <project>'s spend history refused the read: you need the Cloud Datastore Viewer role " +
  '(roles/datastore.viewer) on its Firebase project sandbox-db, plus the Service Usage Consumer role ' +
  '(roles/serviceusage.serviceUsageConsumer); GCP owners and editors have both; gcloud auth ' +
  'application-default login may be needed (rpc error: code = PermissionDenied desc = Missing or ' +
  'insufficient permissions)'

/** asFeedHealth is the drift check between the source's `FeedSilence` and the
 * screen's `FeedHealth`: the screen may not import a module that spawns, so the
 * two shapes are structural, and this assignment is what makes a change to
 * either one a type error in this file. */
function asFeedHealth(feed: FeedSilence): FeedHealth {
  return feed
}

/** asNoBinaryState is the same check for the absent-binary screen's state: the
 * renderer's `NoBinaryState` against `src/source/cli.ts`'s
 * `NoInstallationState`, which is the one the resolution builds. */
function asNoBinaryState(state: NoInstallationState): NoBinaryState {
  return state
}

/** anAbsentState resolves no binary at all: the same directory as the configured
 * home so `~/.local/bin` cannot be the owner's, and one search prefix inside it
 * -- an empty temporary directory, so nothing is executable in it. It is a real
 * resolution -- `resolveBinary` walks what it is given and refuses when nothing
 * is executable -- narrowed so that the answer does not depend on whether the
 * machine running this spec has a `fugaro` installed in `/opt/homebrew/bin`. */
async function anAbsentState(directory: string): Promise<NoBinaryState> {
  const bin = join(directory, 'bin')
  const resolution = await resolveBinary({
    prefixes: [bin],
    home: directory,
    // The app's own environment, which rule 4 never searches: the `PATH` a test
    // runner happens to have must not decide this spec's answer.
    appEnv: { PATH: '/nowhere' },
    directory,
    target: { project: 'fugaro' },
  })
  if (resolution.ok) throw new Error('the spec resolved a binary it did not install')
  return asNoBinaryState(noInstallationState({ reason: resolution.reason, home: directory }))
}

test.describe('the console with nothing to read', () => {
  // One window, one page, and the tests in order: each draws the screen it is
  // about into the same document, exactly as `live.spec.ts` does.
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let scratch: string
  let cli: CliStub

  test.beforeAll(async () => {
    scratch = mkdtempSync(join(tmpdir(), 'fugaro-degraded-'))
    // The app reads a `fugaro` before it opens a window (plan Task 3.3): without
    // an in-range one it opens the refusal document, and the dashboard document
    // this spec mounts into would never be loaded. The spec's own no-binary and
    // refused screens are drawn into that document with their own states and
    // their own `appEnv`, so this stub is here for the window only.
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
    rmSync(scratch, { recursive: true, force: true })
  })

  /** mount draws one screen and fails with the window's own words if the
   * console's decoder refused the document. */
  async function mount(source: string): Promise<{ sanitized?: string; refusalDrawn?: string; refusedState?: string }> {
    const result = await app.evaluate(async ({ BrowserWindow }, body) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(body, true)) as {
        error?: string
        sanitized?: string
        refusalDrawn?: string
        refusedState?: string
      }
    }, source)
    expect(result.error).toBeUndefined()
    return result
  }

  test('the stale threshold is the CLI\'s 45 s on both sides of the window', () => {
    // The source measures the feed's silence with one constant and the banner
    // explains itself with the other. They stand for `watch.StaleAfter`, and a
    // console that bannered at a number its CLI does not call stale would be
    // telling the operator about a silence that is not one.
    expect(FEED_STALE_AFTER_SECONDS).toBe(45)
    expect(LIVE_STALE_AFTER_SECONDS).toBe(FEED_STALE_AFTER_SECONDS)
  })

  test('draws a feed that has gone quiet as a banner, a break and a timestamp', async () => {
    const frames = theLiveFrames()
    const last = frames[frames.length - 1]
    if (last === undefined) throw new Error('the fixture has no frames')
    const arrived = String(last['now'])
    // The console's own clock, an exact silence past the threshold: the same
    // `feedSilence` the source runs, so the reading on screen is the reading the
    // console will really make.
    const feed = asFeedHealth(feedSilence({ receivedAt: arrived }, new Date(Date.parse(arrived) + 260_000)))
    expect(feed.stale).toBe(true)

    await mount(drawLive(frames, feed, 'light'))
    const screen = await drawn(page)

    expect(screen.theme).toBe('light')
    expect(screen.stale).toBe('true')
    // The silence as drawn, and the threshold it is measured against -- the
    // number interpolated from the constant rather than a second copy of it.
    expect(screen.staleSilence).toBe('260 s')
    expect(screen.staleThreshold).toContain('stale after')
    expect(screen.staleThreshold).toContain(`${String(FEED_STALE_AFTER_SECONDS)} s`)
    expect(screen.staleThreshold).toContain('the values above are the last ones read')
    expect(screen.staleAt).toBe(feed.at)
    // The last-known values stay, with the timestamp they were read at.
    expect(screen.sinceAt).toBe(arrived)
    expect(screen.receivedAt).toBe(arrived)
    expect(screen.counted).toBe('$50.00')
    // And the graphs break rather than interpolate: the newest sample is a gap,
    // drawn as the console's own mark for one, with the samples that were
    // measured still in front of it.
    const bars = screen.bars
    expect(bars.length).toBeGreaterThan(2)
    expect(bars[bars.length - 1]).toBe('gap')
    expect(screen.gaps).toBeGreaterThan(0)
    expect(screen.text).toContain('—')
  })

  test('draws no stale banner for a feed that has just spoken', async () => {
    // The fresh side of the threshold, in the real window: a frame one second
    // old is a feed nobody needs a banner about.
    await mount(
      drawLive(
        theLiveFrames(),
        asFeedHealth(feedSilence({ receivedAt: '2026-10-15T13:00:00Z' }, new Date('2026-10-15T13:00:01Z'))),
        'light',
      ),
    )
    const screen = await drawn(page)
    expect(screen.stale).toBeNull()
  })

  test('draws no stale banner when nobody is keeping the console\'s clock', async () => {
    // The caller that keeps no feed reading at all is every drawing the earlier
    // specs and the goldens take, and it must not grow a banner it cannot fill
    // in. Review round 5 of PR 478: the test that carried this name passed a
    // *fresh* reading, which is a different case -- the null path its comment
    // described was never exercised in the window. It is this test now.
    await mount(drawLive(theLiveFrames(), null, 'light'))
    const screen = await drawn(page)
    expect(screen.stale).toBeNull()
    expect(screen.staleSilence).toBeNull()
    expect(screen.sinceAt).toBeNull()
    // The dashboard is still drawn: no reading is not no screen.
    expect(screen.liveRoots).toBe(1)
    expect(screen.bars.length).toBeGreaterThan(2)
  })

  test('draws the CLI\'s no-Firebase mode with the caps unavailable and not zero', async () => {
    const feed = asFeedHealth(feedSilence({ receivedAt: '2026-10-15T13:00:30Z' }, new Date('2026-10-15T13:00:40Z')))
    await mount(drawDegraded(theDegradedFrame(), [theNotice()], feed, 'light'))
    const screen = await drawn(page)

    expect(screen.kind).toBe('no-firebase')
    expect(screen.degradedRoots).toBe(1)
    // Not a partially drawn dashboard: this is the degraded shape's own screen.
    expect(screen.liveRoots).toBe(0)
    expect(screen.project).toBe('<project>')
    expect(screen.mode).toBe('polling')
    expect(screen.modeState).toBe('polling')
    expect(screen.notice).toContain('no budget backend')
    // The CLI's own non-JSON line, kept rather than dropped.
    expect(screen.warnings).toHaveLength(1)
    expect(screen.warnings[0]).toContain('run records unavailable (retrying)')
    // The plan's red step: a cap the console cannot read is `n/a`, and never a
    // zero drawn as one -- a project whose cap is unreadable is not a project
    // that has spent nothing, and `$0.00` of a $50 cap says it has. (A
    // subscription row's model figure is `NOTIONAL~$0.00` and is a different
    // statement, which is why this is asserted on the cap and not on the text:
    // the notional marker is what keeps the two apart.)
    expect(screen.caps).toBe('n/a')
    expect(screen.caps).not.toBe('$0.00')
    expect(screen.capsState).toBe('unavailable')
    // And the kill affordance is absent -- not drawn disabled.
    expect(screen.killCapable).toBe('false')
    expect(screen.killable).toBe(0)
    expect(screen.buttons).toBe(0)
    // Two run records, one of them a subscription's and marked notional.
    expect(screen.runIds).toEqual(['20261015-123000-bb01', '20261015-121000-bb02'])
    expect(screen.notionalRows).toBe(1)
    expect(screen.text).toContain('NOTIONAL~')
    // The row whose compute was not estimated is `n/a`, and the totals built
    // over it are lower bounds rather than sums of a zero.
    expect(screen.runComputes[1]).toBe('n/a')
    expect(screen.text).toContain('≥ $9.40')
    // The last read, kept with the values it belongs to.
    expect(screen.receivedAt).toBe('2026-10-15T13:00:30Z')
    expect(screen.text).toContain('last read')
    // No link is offered on this screen, so the window has nothing to click.
    expect(screen.anchors).toBe(0)
  })

  test('draws the degraded screen in the dark theme', async () => {
    // The degraded screens are drawn once per theme exactly as the four screens
    // are; the palette is `LIVE_THEMES`' and deliberately not a third one.
    await mount(drawDegraded(theDegradedFrame(), [], null, 'dark'))
    const screen = await drawn(page)
    expect(screen.theme).toBe('dark')
    expect(screen.degradedRoots).toBe(1)
    expect(screen.caps).toBe('n/a')
  })

  test('reaches the absent-binary screen with no fugaro anywhere to run', async () => {
    const state = await anAbsentState(scratch)
    await mount(drawUnavailable(state, 'light'))
    const screen = await drawn(page)

    expect(screen.kind).toBe('no-binary')
    expect(screen.headline).toBe(NO_BINARY_HEADLINE)
    expect(screen.installCommand).toBe(FUGARO_INSTALL_COMMAND)
    // And no choose-the-binary affordance: the console asks the operator for no
    // path (the owner's ruling of 2026-10-20, §6.3's row), so the screen that
    // used to draw one draws nothing at all where it stood.
    expect(screen.text).not.toContain('choose a different fugaro binary')
    expect(screen.text).not.toContain('choose the binary')
    // The count of places searched, and never the places: `sanitize.ts` draws
    // every path as `<path>` -- "a path is where the operator's user name and
    // checkout live, and neither belongs in a screenshot" -- so a list of them
    // would be a column of placeholders. The sentence keeps the fact.
    expect(screen.searched).toBe(String(state.searched.length))
    expect(screen.text).not.toContain(scratch)
    expect(screen.text).not.toContain('/opt/homebrew')
    expect(screen.text).toContain('no fugaro binary was found')
    // Not a partially drawn dashboard: no Live screen, no cap line, no runs.
    expect(screen.liveRoots).toBe(0)
    expect(screen.degradedRoots).toBe(0)
    expect(screen.sections).toBe(1)
    expect(screen.buttons).toBe(0)
    expect(screen.anchors).toBe(0)
  })

  test('shows a refused read in the CLI\'s own words, remedy and all', async () => {
    const state: RefusedState = { kind: 'refused', operation: 'budget.show', sentence: THE_REFUSAL }
    const result = await mount(drawUnavailable(state, 'dark'))
    const screen = await drawn(page)

    expect(screen.kind).toBe('refused')
    expect(screen.theme).toBe('dark')
    expect(screen.operation).toBe('budget.show')
    // Character for character, and the remedy survives: this is the sentence
    // the operator needs, not a summary of it.
    expect(screen.refusal).toBe(THE_REFUSAL)
    expect(screen.refusal).toContain('roles/datastore.viewer')
    expect(screen.refusal).toContain('roles/serviceusage.serviceUsageConsumer')
    // The screen is the operator's only copy, and this is why it does not go
    // through the `sentence` class: the same sentence read as prose loses both
    // roles. The window that just drew it is the one that says so -- and it
    // says the other half too, that the door the screen really uses drew it
    // character for character (review round 5 of PR 478: that door is
    // `sanitizeRefusal`, the credential rules, so a token is still redacted).
    expect(result.sanitized).not.toBe(THE_REFUSAL)
    expect(result.sanitized).not.toContain('roles/datastore.viewer')
    expect(result.refusalDrawn).toBe(THE_REFUSAL)
    // And the model is where the sentence is sanitized, so the drawing reads
    // exactly what the door produced.
    expect(result.refusedState).toBe(THE_REFUSAL)
    expect(screen.refusal).toBe(result.refusalDrawn)

    // A refusal that echoed a credential is the case the old raw `literal`
    // missed: the token never reaches the screen.
    const withToken = `${THE_REFUSAL} (token ${THE_TOKEN})`
    const refused = await mount(
      drawUnavailable(
        refusedState('budget.show', refusalFrom('budget.show', withToken)),
        'light',
      ),
    )
    const tokenScreen = await drawn(page)
    expect(tokenScreen.refusal).not.toContain(THE_TOKEN)
    expect(tokenScreen.refusal).toContain(TOKEN_PLACEHOLDER)
    expect(refused.refusalDrawn).not.toContain(THE_TOKEN)
    // And still no dashboard behind it.
    expect(screen.liveRoots).toBe(0)
    expect(screen.degradedRoots).toBe(0)
    expect(screen.sections).toBe(1)
    expect(screen.buttons).toBe(0)
    expect(screen.anchors).toBe(0)
  })

  test('keeps these screens’ marks under forced colors (design section 6.4)', async () => {
    // The degraded and unavailable screens' half of the forced-colors smoke (section
    // 4.4, in the half section 6.4 gives this task), and the screens where it matters
    // most: these two state almost everything in words -- `n/a` for a cap that cannot be
    // read, `polling`, the install command, the searched count -- so a system that takes
    // the palette away must take nothing else. The chrome's half is `entry.spec.ts`'s; a
    // smoke, not a golden.
    await mount(drawDegraded(theDegradedFrame(), [theNotice()], null, 'light'))
    await page.emulateMedia({ forcedColors: 'active', colorScheme: 'light' })
    expect(await page.evaluate(() => matchMedia('(forced-colors: active)').matches)).toBe(true)

    const forcedDegraded = await page.evaluate(() => {
      const scope: ParentNode = document.getElementById('console-mount') ?? document.createDocumentFragment()
      const row = scope.querySelector('li.degraded-run')
      const computed = row === null ? null : getComputedStyle(row)
      const number = scope.querySelector('.degraded-num')
      return {
        rowVisibility: computed?.visibility ?? null,
        rowWidth: computed?.borderBottomWidth ?? null,
        rowColour: computed?.borderBottomColor ?? null,
        tabular: number === null ? null : getComputedStyle(number).fontVariantNumeric,
      }
    })
    const degraded = await drawn(page)
    expect(degraded.caps, 'the unreadable cap lost its mark').toBe('n/a')
    expect(degraded.mode).toBe('polling')
    expect(forcedDegraded.rowVisibility).toBe('visible')
    expect(forcedDegraded.rowWidth, 'the run row’s boundary collapsed under forced colors').not.toBe('0px')
    expect(forcedDegraded.rowColour).not.toBe('rgba(0, 0, 0, 0)')
    // The fifth number hook, under a palette that is not the theme's: `degraded-num` is
    // the class Task 6.4 added to this screen's `numberCell`, and tabular figures are not
    // a colour, so they are still set.
    expect(forcedDegraded.tabular, 'the numbers lost their tabular figures').toContain('tabular-nums')

    // And the absent-binary screen, which is the same rules on a screen with no
    // dashboard at all: its one figure keeps the hook and its install command keeps a
    // boundary the system draws.
    const state = await anAbsentState(scratch)
    await mount(drawUnavailable(state, 'light'))
    const forcedUnavailable = await page.evaluate(() => {
      const scope: ParentNode = document.getElementById('console-mount') ?? document.createDocumentFragment()
      const command = scope.querySelector('code.unavailable-install-command')
      const computed = command === null ? null : getComputedStyle(command)
      const number = scope.querySelector('.degraded-num')
      return {
        commandWidth: computed?.borderBottomWidth ?? null,
        commandColour: computed?.borderBottomColor ?? null,
        tabular: number === null ? null : getComputedStyle(number).fontVariantNumeric,
      }
    })
    expect(forcedUnavailable.commandWidth, 'the install command lost its boundary').not.toBe('0px')
    expect(forcedUnavailable.commandColour).not.toBe('rgba(0, 0, 0, 0)')
    expect(forcedUnavailable.tabular, 'the searched count lost its tabular figures').toContain('tabular-nums')

    await page.emulateMedia({ forcedColors: 'none' })
  })
})
