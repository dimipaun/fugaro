// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The rows of a streamed run list, in a real window (console-improvements plan
// RS2; D2906, F18).
//
// `test/unit/stream.test.ts` holds the read's three rules against a fake bridge
// and no DOM: the CLI's order, a missing `end` as an interrupted read, and the
// whole-document read where the operation cannot be asked. What a unit test
// cannot say is what the second half of the owner's rule of 2026-10-31 is about
// -- that the rows are **drawn as they arrive** rather than in one burst at the
// end -- because that is a claim about the turns of a real event loop and the
// screen a person sees between them.
//
// So every case here drives the compiled `renderer/stream.js` out of `dist/`,
// with the compiled `renderer/new-bridge.js` in front of it, in the console's own
// window under its own CSP, and takes a reading of the document **at each chunk
// the read draws** (and once after it lands). The observation carries whether the
// read had ended at that moment, so "the rows appeared before the `end`" is
// measured rather than inferred -- and the last observation is the same reading
// with `ended` true, so the field is not a constant that would make the others
// vacuous.
//
// **What this file deliberately is not.** It does not drive the Coordinator
// screen: the operation behind `runs.stream` reaches the bridge in SRC's handler
// (`src/source/new-reads.ts`), wired by I-OPS, and the window's own bridge
// refuses it at the unset floor (`RUNS_STREAM_CLI_FLOOR` is `null` while no CLI
// release carries `--stream`) rather than streaming anything -- so this file
// hands the read a bridge of its own, and one of the cases below hands it one
// with no such member at all. The read is the subject, and the drawing around it
// is the probe's own small one (a list, a count, and the sentence an interrupted
// read draws in the list's place): the console's own caller is `entry.ts`, whose
// queue is drawn by `screens/coordinator.ts`, and its wiring is held by
// `entry-wiring.test.ts`.
//
// **The scheduler is the production one.** No `paint` is passed, so `readStream`
// uses `stream.ts`'s own `NODE_PAINT` (`setTimeout(step, 0)`): each row's drawing
// is a macrotask of its own, which is what lets the window between them hold
// anything at all. A spec that passed a synchronous paint would assert the same
// order and prove nothing about arriving.
//
// No goldens: the plan's file list names no frame, and `playwright.test.ts` holds
// `test/e2e/__screenshots__` to the Live screen's two by name.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { STREAM_INTERRUPTED_WORD } from '../../src/renderer/stream'
import { inRangeCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { launchConsole } from '../support/launch'

// The console's own directory, from this file's location: `test/e2e/x.spec.ts`
// is two levels below it.
const consoleDir = join(__dirname, '..', '..')

/** MODULES are the compiled modules this spec needs, in dependency order: the
 * contract and its decoder (`new-ops.js`, which reads the `row`/`end`/`error`
 * lines), the door in front of it (`new-bridge.js`), the read itself
 * (`stream.js`), and `shortid.js` -- which draws each row by the run's short id,
 * as every list view of this console names one (SR10). The registry guard
 * (`modules-registry.spec.ts`) holds the list to what these modules require. */
const MODULES: readonly string[] = Object.freeze([
  'shared/availability.js',
  'shared/sanitize.js',
  'shared/schema.js',
  'shared/new-ops.js',
  'shared/shortid.js',
  'renderer/new-bridge.js',
  'renderer/stream.js',
])

/** moduleSource is one compiled module as a registry entry. */
function moduleSource(relative: string): string {
  const code = readFileSync(join(consoleDir, 'dist', relative), 'utf8')
  return `registry[${JSON.stringify(`/${relative}`)}] = function (exports, require, module) {\n${code}\n};`
}

/** LOADER is the module registry the injected bundle runs on: a CommonJS
 * `require` that resolves `./` and `../` specifiers and nothing else, so a
 * bundle that grew an import of `node:` or of a package fails here rather than
 * in a window with no Node in it. Unchanged from `live.spec.ts`,
 * `degraded.spec.ts` and `skew.spec.ts`, deliberately: this is the same window
 * and the same constraint. */
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

/** PROBE is the drawing and the record every case works through: a host of the
 * probe's own (so the window's own dashboard, drawn into the same document, is
 * neither read nor disturbed), a list and a count drawn from the rows a chunk
 * carries, and `observe` -- the reading a case asserts on.
 *
 * `observe` is taken **inside `onRows`**, which `readStream` calls before it
 * awaits the paint that follows the chunk: so each observation is the screen at
 * the moment a chunk was drawn, and `ended` (set when the read has landed) says
 * whether the read was still going then. That pair is the whole claim of this
 * file, and the `ended: true` reading after the landing is what keeps it from
 * being true by construction. */
const PROBE = `
var host = document.createElement('section');
host.id = 'runs-stream-probe';
var count = document.createElement('p');
count.id = 'probe-count';
var list = document.createElement('ul');
list.id = 'probe-rows';
var sentence = document.createElement('p');
sentence.id = 'probe-sentence';
host.appendChild(count);
host.appendChild(list);
host.appendChild(sentence);
var previous = document.getElementById('runs-stream-probe');
if (previous) previous.remove();
document.body.appendChild(host);

var frames = [];
var ended = false;
var streamAsked = 0;
var wholeReads = 0;

function observe() {
  var rows = [];
  for (var i = 0; i < list.children.length; i++) rows.push(list.children[i].textContent);
  return { rows: rows, count: count.textContent, ended: ended };
}

function draw(rows) {
  list.replaceChildren();
  for (var i = 0; i < rows.length; i++) {
    var item = document.createElement('li');
    item.textContent = shortid.shortRun(rows[i].run);
    list.appendChild(item);
  }
  count.textContent = String(rows.length);
}

function onRows(rows) {
  draw(rows);
  frames.push(observe());
}

function landed(result) {
  ended = true;
  return new Promise(function (resolve) {
    // One macrotask, so the reading after the landing is taken in a turn of its
    // own rather than in the promise's microtask -- the same shape the chunk
    // readings have, and the one the operator's screen has.
    window.setTimeout(function () {
      frames.push(observe());
      resolve({
        frames: frames,
        status: result.status,
        source: result.source,
        rows: result.rows.map(function (row) { return row.run; }),
        sentence: result.sentence,
        end: result.end === null ? null : { count: result.end.count, version: result.end.version },
        streamAsked: streamAsked,
        wholeReads: wholeReads
      });
    }, 0);
  });
}
`

/** bundle wraps one case's body in the registry and the probe. */
function bundle(body: string): string {
  return `(function () {\n${LOADER}${MODULES.map(moduleSource).join('\n')}${PROBE}${body}\n})()`
}

/** aLine is one `row` line of a `runs.stream` feed: the same row `runs ls
 * --json`'s array holds, with the line's own `type` beside it. The four members
 * a row must carry are the ones `decodeRunsList` reads. */
function aLine(run: string): Record<string, unknown> {
  return { type: 'row', row: { run, repo: 'acme/widgets', status: 'failed', terminal: true } }
}

/** RUNS is the three runs every case streams, in the order the CLI writes them:
 * a real `runs ls` order, newest first, so the assertion on the drawn rows is an
 * assertion about an order rather than about a set. */
const RUNS: readonly string[] = Object.freeze([
  '20261118-101500-aa01',
  '20261118-094000-bb02',
  '20261117-210000-cc03',
])

/** END_LINE is the feed's own statement that it finished, carrying the count the
 * CLI counted (`count` is the document's own figure, and RS2 does not read it as
 * the drawn rows: the rows say what was drawn). */
const END_LINE: Record<string, unknown> = { type: 'end', version: 1, count: RUNS.length }

/** readSource drives `readStream` the way `entry.ts` does: the stream first,
 * each chunk drawn as it arrives, and -- when the read lands -- the whole list
 * drawn again from the answer, or the sentence drawn in the list's place where
 * the read did not finish.
 *
 * `bridge` is the window's own, as this spec can make it: `'stream'` hands
 * `newOpsFrom` a member answering `runs.stream` with `lines`, and `'absent'`
 * hands it `{}` -- no member at all, which is what a console whose
 * `runs.stream` is not wired yet (or a CLI below the floor's release) has, and
 * for which `newOpsFrom` answers `not available`. The whole-document read is the
 * fallback the console ran before the stream existed: `rows`, in one answer.
 *
 * No `paint` is passed anywhere: the scheduler is `stream.ts`'s own production
 * one, so the chunks land in macrotasks of their own and the window between them
 * is real. */
function readSource(options: {
  readonly bridge: 'stream' | 'absent'
  readonly lines?: readonly Record<string, unknown>[]
  readonly rows: readonly string[]
}): string {
  const streamRuns =
    options.bridge === 'stream'
      ? `{
  streamRuns: function () {
    streamAsked += 1;
    return Promise.resolve({ ok: true, data: { document: { lines: ${JSON.stringify(options.lines ?? [])} } } });
  }
}`
      : `{}`
  return bundle(`
var bridge = load('/renderer/new-bridge.js');
var stream = load('/renderer/stream.js');
var shortid = load('/shared/shortid.js');
var ops = bridge.newOpsFrom(${streamRuns});
return stream.readStream({
  stream: function () { return ops.streamRuns({ since: '24h' }); },
  document: function () {
    wholeReads += 1;
    return Promise.resolve({ ok: true, rows: ${JSON.stringify(options.rows.map((run) => ({ run })))} });
  },
  onRows: onRows
}).then(function (result) {
  if (result.status === 'complete') draw(result.rows);
  else {
    list.replaceChildren();
    count.textContent = '';
    sentence.textContent = result.sentence;
  }
  return landed(result);
});
`)
}

/** Observation is the screen at one chunk: the rows drawn so far, the count
 * beside them, and whether the read had ended at that moment. */
interface Observation {
  readonly rows: readonly string[]
  readonly count: string
  readonly ended: boolean
}

/** Probe is what one case's read returned, plus the record of the screen. */
interface Probe {
  readonly frames: readonly Observation[]
  readonly status: string
  readonly source: string
  readonly rows: readonly string[]
  readonly sentence: string | null
  readonly end: { readonly count: number | null; readonly version: number | null } | null
  readonly streamAsked: number
  readonly wholeReads: number
}

/** Drawn is the probe's host, read out of the live document after a case. */
interface Drawn {
  readonly rows: readonly string[]
  readonly count: string | null
  readonly sentence: string | null
}

test.describe('the run list a stream draws as it arrives', () => {
  // One window, one page, and the tests in order: each case mounts the probe's
  // host into the same document, exactly as the other drawing specs do.
  test.describe.configure({ mode: 'serial' })

  let app: ElectronApplication
  let page: Page
  let cli: CliStub

  test.beforeAll(async () => {
    // A scratch profile with the stub as the active installation: without one
    // the window opens on the picker, and there would be no dashboard document
    // to inject the probe into.
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

  /** mount evaluates one case's bundle in the window and answers what the read
   * returned. `executeJavaScript` and not `page.evaluate`, for the reason
   * `skew.spec.ts` gives: an inline `<script>` is refused by the console's CSP,
   * and the registry the bundle builds only exists in the world this call runs
   * in. */
  async function mount(source: string): Promise<Probe> {
    const result = await app.evaluate(async ({ BrowserWindow }, body) => {
      const window = BrowserWindow.getAllWindows()[0]
      if (window === undefined) return { error: 'the console has no window' }
      return (await window.webContents.executeJavaScript(body, true)) as Record<string, unknown>
    }, source)
    expect(result['error']).toBeUndefined()
    return result as unknown as Probe
  }

  /** drawn reads the probe's host out of the live document. Everything is read
   * inside the host, so the window's own dashboard can neither satisfy nor fail
   * an assertion. */
  function drawn(): Promise<Drawn> {
    return page.evaluate(() => {
      const host = document.getElementById('runs-stream-probe')
      if (host === null) return { rows: [], count: null, sentence: null }
      const rows = Array.from(host.querySelectorAll('#probe-rows li')).map(
        (item) => item.textContent ?? '',
      )
      return {
        rows,
        count: document.getElementById('probe-count')?.textContent ?? null,
        sentence: document.getElementById('probe-sentence')?.textContent ?? null,
      }
    })
  }

  test('draws each row while the read is still going, and never the list before the end', async () => {
    const probe = await mount(
      readSource({ bridge: 'stream', lines: [...RUNS.map((run) => aLine(run)), END_LINE], rows: [] }),
    )

    // **The claim, as one table.** One row per chunk, each chunk drawn in a turn
    // of its own while the read had not ended (the first three readings), and
    // then the same reading after the landing (`ended: true`, the whole list):
    // rows appear *before* the end line is read, and the last reading is what
    // makes `ended` a measurement rather than a constant.
    expect(probe.frames.map((frame) => frame.ended)).toEqual([false, false, false, true])
    expect(probe.frames.map((frame) => frame.rows.length)).toEqual([1, 2, 3, 3])
    // The count the queue's head draws updates with the rows, chunk by chunk.
    expect(probe.frames.map((frame) => frame.count)).toEqual(['1', '2', '3', '3'])
    // The rows are the CLI's own order, named by the short id of each run.
    expect(probe.frames[2]?.rows).toEqual(['aa01', 'bb02', 'cc03'])

    expect(probe.status).toBe('complete')
    expect(probe.source).toBe('stream')
    expect(probe.rows).toEqual([...RUNS])
    expect(probe.sentence).toBeNull()
    // The `end` line is read as the feed's own statement: it says the read
    // finished, and it carries the CLI's count (three runs, which is also what
    // the drawing holds -- the reading of the two is the console's own).
    expect(probe.end?.count).toBe(3)
    // The stream was asked once, and the whole document was never read: a read
    // that answers is not also made the other way round.
    expect(probe.streamAsked).toBe(1)
    expect(probe.wholeReads).toBe(0)

    const screen = await drawn()
    expect(screen.rows).toEqual(['aa01', 'bb02', 'cc03'])
    expect(screen.count).toBe('3')
    expect(screen.sentence).toBe('')
  })

  test('draws a feed that stops without an end as an interrupted read, not as a shorter list', async () => {
    const probe = await mount(
      readSource({ bridge: 'stream', lines: RUNS.map((run) => aLine(run)), rows: [] }),
    )


    // The rows did arrive -- the point of the stream is that they show while the
    // read goes on -- and the read's own answer is that it never finished. The
    // last reading is the one the rule is about: the rows that had arrived are
    // **taken back** when the read turns out not to have ended, because a list
    // nothing finished is not a shorter list.
    expect(probe.frames.map((frame) => frame.ended)).toEqual([false, false, false, true])
    expect(probe.frames.map((frame) => frame.rows.length)).toEqual([1, 2, 3, 0])
    expect(probe.status).toBe('interrupted')
    expect(probe.source).toBe('stream')
    expect(probe.sentence).toBe(STREAM_INTERRUPTED_WORD)
    expect(probe.end).toBeNull()

    // What the console draws for it (R1): the sentence, in the list's place, and
    // never the rows that had arrived -- the queue is not the list minus its tail
    // (`entry.ts` draws this part with a Retry beside it, held by
    // `entry-wiring.test.ts`). The caller here draws the sentence where the rows
    // were, which is that rule with the probe's own two elements.
    const screen = await drawn()
    expect(screen.sentence).toBe(STREAM_INTERRUPTED_WORD)
    expect(screen.rows).toEqual([])
  })

  test('falls back to the whole-document read, and draws it in one piece', async () => {
    const probe = await mount(readSource({ bridge: 'absent', rows: RUNS }))

    // One reading, after the landing, holding every row: the fallback read draws
    // once, from the document, and there is no chunk in between -- which is what
    // "the console draws what it drew before the stream existed" means.
    expect(probe.frames.map((frame) => frame.ended)).toEqual([true])
    expect(probe.frames.map((frame) => frame.rows.length)).toEqual([RUNS.length])
    expect(probe.status).toBe('complete')
    expect(probe.source).toBe('whole')
    expect(probe.rows).toEqual([...RUNS])
    expect(probe.sentence).toBeNull()
    // A whole document carries no `end` line of its own: its answer *is* the
    // statement that the read finished.
    expect(probe.end).toBeNull()
    expect(probe.streamAsked).toBe(0)
    expect(probe.wholeReads).toBe(1)

    const screen = await drawn()
    expect(screen.rows).toEqual(['aa01', 'bb02', 'cc03'])
    expect(screen.count).toBe('3')
    expect(screen.sentence).toBe('')
  })
})
