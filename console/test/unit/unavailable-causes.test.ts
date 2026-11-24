// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Every `n/a` on a screen has a named cause (console-fixes ID5, reconciliation
// F28).
//
// F28's finding is that the console's `n/a` (`shared/availability.ts`'s
// `UNAVAILABLE_MARK`) is mostly not a data-loading bug, and that the marks were
// indistinguishable from one another: a launcher the `person` class refused and
// a figure the record never carried drew the same three characters. D3213
// removed the display masks (ID1, ID2), so the refusal F28 calls the *main*
// cause is gone -- an identity the record holds is drawn whole, which is the
// red step below -- and what is left is a small, enumerable set of sites. This
// file is the table over that set:
//
//   - **every renderer site that can draw the mark** is a row of `SITES`, with
//     the file it lives in, the one cause it can draw, and a `drive` that
//     reaches it through the screen's own public door (a builder and a
//     renderer, or a module the screens are handed) rather than by calling the
//     private helper that writes the text;
//   - **each mark's cause is one of the three F28 names** -- a field the record
//     lacks, a version floor, or a refused read -- and a row whose element
//     carries the cause on screen asserts the exact sentence a reader is given,
//     so a screen that draws the mark with *no* cause fails here;
//   - **a present, valid identity never draws the mark** (`IDENTITIES`): the
//     whole recorded address reaches the Run screen's `started by`, `requested
//     by` and `account` cells and the Live table's BY column, drawn whole and
//     with no reason beside it. Before ID1 and ID2 these read `n/a` -- the
//     `person` class refused a dotted local part and `launcher.ts` cut at the
//     first `@` -- which is the red step the task names;
//   - **the coverage itself is checked against the source** (`the scan`): every
//     file under `src/renderer` that can draw the mark, plus the module that
//     declares it, must be in the table, and the table may name no file that
//     does not. **A file can draw the mark without ever naming it**: the door
//     every mark on a screen comes through is `formatReading`, which reads
//     `UNAVAILABLE_MARK` inside `availability.ts` -- so the scan counts the mark
//     literal *and* a `formatReading(` call, and a screen wired to a reading is
//     covered by it. A new screen that draws the mark therefore fails this file
//     rather than slipping past it, and a file that stops drawing it fails too,
//     so the table cannot rot into a list of places that no longer exist.
//
// **A `formatReading` call that cannot yield the mark is named, with its
// guard**: `CANNOT_DRAW` is the pinned list of them (`spark`'s bars, which the
// model makes a gap; the gauge's title, whose branch is guarded by the two
// figures being numbers; a spend bar's height, which `barOf` makes a gap or a
// measured share). Each line names the symbol it is about and the case asserts
// the symbol is still in that file, so the list cannot rot into invented names.
//
// **Where a cause is not on screen, the table says so and says why.** An
// element that carries the mark and no reason is listed in `NO_REASON_SENTENCE`
// with the sentence naming where its cause is stated instead -- a model cell, a
// label string, an `<option>` with no title slot, a canvas card's SVG text. The
// set is pinned and small: a site that *loses* the reason it used to draw fails
// the third case, so a title cannot quietly go missing.
//
// Hermetic: the fake element below is the whole DOM, every case runs against a
// document built in this file or an invented fixture, and nothing reaches the
// network, the home directory or a cloud.

import { readdirSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { LIVE_CAPS_HIDDEN_WORD, buildLiveModel, renderLive } from '../../src/renderer/screens/live'
import { RUN_RECORD_BUSY, buildRunModel, renderRun, runBusyLabel } from '../../src/renderer/screens/run'
import {
  SETTINGS_PENCIL_CLASS,
  SETTINGS_PENCIL_KEY,
  buildProjectsSettings,
  fillProjectsSettings,
} from '../../src/renderer/screens/projects'
import { GRAPH_INITIAL_STATE, renderGraph } from '../../src/renderer/screens/graph'
import {
  GRAPH_ESTIMATE_MINUTES,
  GRAPH_ESTIMATE_NO_HISTORY,
  GRAPH_ESTIMATE_READY,
  GRAPH_ESTIMATE_TASKS,
} from '../../src/renderer/screens/graph'
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH, buildGraphModel } from '../../src/renderer/graph/model'
import { nodeKey } from '../../src/renderer/graph/layout'
import type { Positions } from '../../src/renderer/graph/layout-client'
import { MOVE_TARGET_CLASS, MOVE_VALUE_CLASS, moveAnswerOf, moveSheet } from '../../src/renderer/graph/move'
import type { MoveTarget } from '../../src/renderer/graph/move'
import { KEYS_KIND } from '../../src/renderer/manage/kinds/keys'
import { MODELS_KIND } from '../../src/renderer/manage/kinds/models'
import { poolsKind } from '../../src/renderer/manage/kinds/pools'
import { budgetKindFor } from '../../src/renderer/manage/kinds/budget'
import { classifyCliVersion } from '../../src/shared/version'
import { putLink, putReading, putText } from '../../src/renderer/app'
import { UNAVAILABLE_MARK, readNumber } from '../../src/shared/availability'
import { sanitizeText } from '../../src/shared/sanitize'
import { decodePlanStatus, decodeSnapshot } from '../../src/shared/schema'
import type { LiveDocument, PlanStatusView } from '../../src/shared/schema'
import { SYNTHETIC_SOURCE } from './synthetic'

const HERE = dirname(fileURLToPath(import.meta.url))
const SOURCE = join(HERE, '..', '..', 'src')

// ---------------------------------------------------------------------------
// The fake element and the factory
// ---------------------------------------------------------------------------

/**
 * FakeElement is the smallest thing a screen may draw into, and it is a fake
 * for the same reason the other screens' unit tests declare one: there is no
 * `document` in this suite and the point is the *observation*. Every attribute
 * that could make the window do something -- the navigating attribute, a
 * resource, a style, a handler -- throws, so a screen that reached for one
 * fails here rather than in a window nobody is watching.
 */
class FakeElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeElement[] = []
  readonly classes: string[] = []
  textContent: string | null = null
  value = ''

  static readonly FORBIDDEN_ATTRIBUTES = new Set([
    ['h', 'ref'].join(''),
    'src',
    'srcdoc',
    'style',
    'formaction',
    'action',
  ])

  constructor(
    readonly tag: string,
    readonly namespace: 'html' | 'svg' = 'html',
  ) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeElement.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`a console screen may not set the ${name} attribute`)
    }
    if (lowered === 'class') {
      this.classes.length = 0
      this.classes.push(...value.split(/\s+/).filter((one) => one !== ''))
    }
    this.attributes.set(name, value)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }

  append(child: DrawnNode): void {
    this.nodes.push(child as FakeElement)
  }

  childNodes(): readonly FakeElement[] {
    return this.nodes
  }

  /** The settings panel's search box is the one control a screen reads either
   * way and the one that binds its own listener, so both live on the fake. */
  addEventListener(): void {
    // Bound and never fired: this file drives the drawings and not the controls.
  }
}

/** factory makes fresh fakes, in the shape every screen is handed. Both
 * namespaces are here because Run's timeline and the Graph's canvas draw in
 * SVG, and the fake remembers which one made a node. */
const factory = {
  create: (tag: string): FakeElement => new FakeElement(tag),
  createNS: (tag: string): FakeElement => new FakeElement(tag, 'svg'),
}

/**
 * DrawnNode is the shape each screen's own `ScreenElement` has -- four screens
 * declare one apiece and they are the same shape -- so the helpers below can
 * take a drawing without naming which screen made it. The tree behind it is
 * always `FakeElement`, which is what the cast inside `walk` is for.
 */
type DrawnNode = FakeElement | { childNodes(): readonly DrawnNode[] }

/** walk visits every node of a tree, the root first. */
function walk(node: DrawnNode): readonly FakeElement[] {
  const fake = node as FakeElement
  const out: FakeElement[] = [fake]
  for (const child of fake.childNodes()) out.push(...walk(child))
  return out
}

/** all is every node carrying a class, in document order. */
function all(node: DrawnNode, className: string): readonly FakeElement[] {
  return walk(node).filter((one) => one.classes.includes(className))
}

/** theMark is the one element of a drawing left with the mark, asserted to be
 * one: a tree that drew the refused value in two places is not a tree this file
 * can read a cause off. */
function theMark(tree: DrawnNode, className: string): FakeElement {
  const found = all(tree, className).filter((each) => each.textContent === UNAVAILABLE_MARK)
  expect(found, `expected exactly one refused .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no refused .${className}`)
  return first
}

/** one is the single node carrying a class, asserted to be single. */
function one(node: DrawnNode, className: string): FakeElement {
  const found = all(node, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

// ---------------------------------------------------------------------------
// The table
// ---------------------------------------------------------------------------

/** CauseKind is one of the three causes F28 allows a mark to have. */
type CauseKind = 'missing-field' | 'version-floor' | 'refused-read'

/** Drawn is what a site put on an element: the text a reader sees, and the
 * reason a reader is given where the site states one. */
interface Drawn {
  readonly text: string | null
  readonly reason: string | null
}

/** Site is one renderer site that can draw the mark: the console-fixes plan
 * task (ID5) drives every one of them. */
interface Site {
  readonly id: string
  /** the file the site lives in, repo-relative, as `the scan` reads it */
  readonly file: string
  readonly cause: CauseKind
  /** the text the element is left with: the mark, or the console's own sentence
   * the mark sits inside (`runBusyLabel`) */
  readonly text: string
  /** the sentence the element carries, or null where the site draws the mark
   * and no reason (every such site is named in `NO_REASON_SENTENCE`) */
  readonly says: string | null
  drive(): Drawn
}

/** refused is a `Sanitized` refusal the real sanitizer made, as the model hands
 * one to a screen: the field path and the class are the ones the wire key
 * carries, so the reason is the console's own sentence and not a test's. */
function refused(raw: string, textClass: 'label' | 'sentence' | 'url', field: string): string | null {
  const value = sanitizeText(raw, textClass, field)
  return value.ok ? null : value.reason
}

/** MULTILINE is a value the `label` class refuses: a cell is one line, and a
 * value that is not one is the refusal every label cell states. */
const MULTILINE = ['ana', 'smith@acme.com'].join('\n')

/** ACCOUNT is a person's address, whole: what the record holds and what every
 * identity cell draws (D3213). */
const ACCOUNT = ['ana', 'smith', 'acme.com'].join('.') + '@' + ['acme', 'com'].join('.')

/** liveFrame is the invented feed's second document -- the one whose runs carry
 * models and an account -- through the real decoder, so a case patches a
 * document and not a hand-written model. */
function liveFrame(): LiveDocument {
  const record = SYNTHETIC_SOURCE[1]
  if (record === undefined || !('document' in record)) throw new Error('the invented feed has no second document')
  const decoded = decodeSnapshot(record.document, 'test/fixtures/synthetic.ndjson')
  if (!decoded.ok) throw new Error(`the invented feed did not decode: ${decoded.skew.name}`)
  return decoded.value
}

/** graphDocument is `plan-status.json` with one path replaced and run through
 * the real decoder -- a patched document is a document and goes through the
 * decoder like any other. */
function graphDocument(path: readonly (string | number)[], value: unknown): PlanStatusView {
  const raw = JSON.parse(readFileSync(join('test', 'fixtures', 'plan-status.json'), 'utf8')) as Record<string, unknown>
  let at: unknown = raw
  for (const step of path.slice(0, -1)) {
    if (at === null || typeof at !== 'object') throw new Error(`no ${String(step)} in the path`)
    at = (at as Record<string, unknown>)[step]
  }
  const last = path[path.length - 1]
  if (last === undefined || at === null || typeof at !== 'object') throw new Error('the path is empty')
  ;(at as Record<string, unknown>)[last] = value
  const decoded = decodePlanStatus(raw)
  if (!decoded.ok) throw new Error(decoded.skew.because)
  return decoded.value
}

/** LONG_TITLE is a title longer than `sanitize.ts`'s 64 KiB bound: a value the
 * `sentence` class refuses that a document can still carry, and therefore the
 * way a graph node's title reaches a screen refused. */
const LONG_TITLE = 'x'.repeat(70_000)

/** TITLE_REASON is what the graph model refused that title with: the wire path
 * the node was read from, and the class's own words. */
const TITLE_REASON = `plans[0].nodes[0].title is longer than the console will read (65536 characters)`

/** graphPicture is the Graph screen's own model and a placement for its first
 * task, which is what the canvas draws from: the layout engine's answer, hand
 * built, so the case is about the drawing and not about ELK. */
function graphPicture(): { readonly model: ReturnType<typeof buildGraphModel>; readonly positions: Positions } {
  const model = buildGraphModel({ document: graphDocument(['plans', 0, 'nodes', 0, 'title'], LONG_TITLE), tab: null })
  const plan = model.columns[0]?.plans[0]
  if (plan === undefined) throw new Error('the fixture has no plan')
  const node = plan.nodes[0]
  if (node === undefined) throw new Error('the fixture plan has no task')
  return {
    model,
    positions: {
      nodes: [{ id: nodeKey(plan.id, node.id), x: 0, y: 0, width: GRAPH_NODE_WIDTH, height: GRAPH_NODE_HEIGHT }],
      edges: [],
    },
  }
}

/** ESTIMATE_RANGE_MARK is `graph.ts`'s own range separator (`GRAPH_ESTIMATE_RANGE`,
 * which the screen keeps to itself), written as the code point so this file does
 * not carry a second copy of the glyph. */
const ESTIMATE_RANGE_MARK = '–'

/** SUPPORTED is the handshake for a CLI inside this console's own range, which
 * is the one the Manage kinds are asked with when nothing is withheld. */
const SUPPORTED = classifyCliVersion('0.12.0')

/** modelsDocument is `models ls --json` with one model the report cannot price:
 * both rates are `null`, which is unknown and never zero. */
function modelsDocument(): unknown {
  return {
    version: 1,
    project: 'example-project',
    catalog: null,
    routes: [],
    models: [
      {
        id: 'claude-sonnet-5',
        route: 'gateway',
        source: 'none',
        input_per_m: null,
        output_per_m: null,
        catalog: null,
        allowed_to: ['*'],
        refused: 'no route serves it',
        note: '',
      },
    ],
    repositories: [],
  }
}

/** poolsDocument is `accounts pools ls --json` with one member whose usage the
 * document holds no counts in and whose headroom it does not carry at all: four
 * figures the console has no number for, and four `n/a`s rather than four
 * zeroes. */
function poolsDocument(): unknown {
  return {
    version: 1,
    project: 'example-project',
    pools: [
      {
        name: 'example-pool',
        rule: 'round-robin',
        members: [
          {
            name: 'ana',
            state: 'active',
            next_reset: '2026-11-03T06:00:00Z',
            usage: {},
            headroom: null,
          },
        ],
      },
    ],
  }
}

/** budgetDocument is `budget show --json` with one repository the document holds
 * no figures for: both caps, the money counted and the headroom are absent, so
 * every figure cell is a mark. The scopes are carried, so the two cap cells draw
 * the console's own word for the scope beside the mark. */
function budgetDocument(): unknown {
  return {
    project: 'example-project',
    mode: 'enforce',
    day: '2026-11-03',
    max_reserve_usd: 12,
    defaults: { repo_daily_cap_usd: 25, repo_per_run_cap_usd: 5 },
    repos: [
      {
        slug: 'example-owner/example-repo',
        repo: 'example-owner/example-repo',
        daily_cap_usd: null,
        daily_cap_source: 'repo',
        per_run_cap_usd: null,
        per_run_cap_source: 'repo',
        counted_usd: null,
        spent_usd: null,
        notional_usd: 0,
        leased_usd: null,
        headroom_usd: null,
        kill: null,
        running: [],
      },
    ],
  }
}

/** SITES is the table, one row per renderer site that can draw the mark. */
const SITES: readonly Site[] = [
  {
    id: 'availability.reading',
    file: 'shared/availability.ts',
    cause: 'missing-field',
    text: UNAVAILABLE_MARK,
    says: 'the document has no total.counted_usd',
    drive: () => {
      const element = new FakeElement('span')
      putReading(element, readNumber(undefined, 'total.counted_usd'), 'usd')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'app.put-text',
    file: 'renderer/app.ts',
    cause: 'refused-read',
    text: UNAVAILABLE_MARK,
    says: refused(MULTILINE, 'label', 'runs[].launched_by'),
    drive: () => {
      const element = new FakeElement('span')
      putText(element, MULTILINE, 'label', 'runs[].launched_by')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'app.put-link',
    file: 'renderer/app.ts',
    cause: 'refused-read',
    text: UNAVAILABLE_MARK,
    says: refused('not a url', 'url', 'row.pr_url'),
    drive: () => {
      const element = new FakeElement('span')
      // The URL class refuses a string that is not a URL, and the console
      // offers no destination: the mark, and the reason and never the URL.
      putLink(element, 'not a url', 'row.pr_url')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'live.caps',
    file: 'renderer/screens/live.ts',
    cause: 'version-floor',
    text: UNAVAILABLE_MARK,
    says: LIVE_CAPS_HIDDEN_WORD,
    drive: () => {
      const element = one(renderLive(factory, buildLiveModel(liveFrame()), 'light', true), 'live-gauge-rate')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'live.actor',
    file: 'renderer/screens/live.ts',
    cause: 'missing-field',
    text: UNAVAILABLE_MARK,
    says: 'the document carries no runs[].models',
    drive: () => {
      const frame = liveFrame()
      const withoutModels = frame.runs.map((run) => ({ ...run, models: '', auth: '' }))
      const element = one(renderLive(factory, buildLiveModel({ ...frame, runs: withoutModels }), 'light'), 'live-run-models')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'run.column',
    file: 'renderer/screens/run.ts',
    cause: 'missing-field',
    text: UNAVAILABLE_MARK,
    says: 'the document carries no row.repo',
    drive: () => {
      const model = buildRunModel({ diagnosis: { row: {} }, logs: null, logLink: null })
      const element = one(renderRun(factory, model, 'light'), 'run-repo')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'run.busy-label',
    file: 'renderer/screens/run.ts',
    cause: 'refused-read',
    text: `Reading ${RUN_RECORD_BUSY} of ${UNAVAILABLE_MARK}`,
    says: null,
    drive: () => ({ text: runBusyLabel('not a run id', RUN_RECORD_BUSY), reason: null }),
  },
  {
    id: 'graph.list-cell',
    file: 'renderer/screens/graph.ts',
    cause: 'refused-read',
    text: UNAVAILABLE_MARK,
    says: TITLE_REASON,
    drive: () => {
      const picture = graphPicture()
      const tree = renderGraph(factory, { ...picture, positions: null, state: GRAPH_INITIAL_STATE, runs: [], notice: null }, 'light')
      const element = theMark(tree, 'graph-row-title')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'graph.canvas-title',
    file: 'renderer/screens/graph.ts',
    cause: 'refused-read',
    text: UNAVAILABLE_MARK,
    says: null,
    drive: () => {
      const picture = graphPicture()
      const tree = renderGraph(factory, { ...picture, state: GRAPH_INITIAL_STATE, runs: [], notice: null }, 'light')
      const element = theMark(tree, 'graph-node-title-line')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'graph.card-label',
    file: 'renderer/screens/graph.ts',
    cause: 'refused-read',
    text: `${UNAVAILABLE_MARK}, done, critical`,
    says: null,
    drive: () => {
      const picture = graphPicture()
      const tree = renderGraph(factory, { ...picture, state: GRAPH_INITIAL_STATE, runs: [], notice: null }, 'light')
      // A card is named by its title and its words, comma-joined, so the mark
      // its refused title draws is the first part of the card's own name.
      const named = all(tree, 'graph-node').filter((each) =>
        (each.getAttribute('aria-label') ?? '').startsWith(UNAVAILABLE_MARK),
      )
      expect(named).toHaveLength(1)
      const element = named[0]
      if (element === undefined) throw new Error('no card was named by the mark')
      return { text: element.getAttribute('aria-label'), reason: element.getAttribute('title') }
    },
  },
  {
    id: 'move.field',
    file: 'renderer/graph/move.ts',
    cause: 'refused-read',
    text: UNAVAILABLE_MARK,
    says: refused(MULTILINE, 'label', 'the release the task goes to'),
    drive: () => {
      const answer = moveAnswerOf({
        refusal: null,
        sentence: null,
        argv: ['fugaro', 'roadmap', 'move', '--json', 'plans/example/task-one', '--to', '0.13.0'],
        task: { key: 'plans/example/task-one', id: 'example/task-one', title: 'The first task' },
        from: { key: '0.12.0', name: '0.12.0' },
        to: { key: '0.13.0', name: MULTILINE },
        outcome: null,
      })
      if (answer === null) throw new Error('the answer did not read')
      const tree = moveSheet(factory, { request: null, answer, sentence: null }, [])
      const cells = all(tree, MOVE_VALUE_CLASS).filter((one) => one.textContent === UNAVAILABLE_MARK)
      const first = cells[0]
      if (first === undefined) throw new Error('no refused field in the sheet')
      return { text: first.textContent, reason: first.getAttribute('title') }
    },
  },
  {
    id: 'move.target-option',
    file: 'renderer/graph/move.ts',
    cause: 'refused-read',
    text: UNAVAILABLE_MARK,
    says: null,
    drive: () => {
      const target: MoveTarget = { key: '0.13.0', name: sanitizeText(MULTILINE, 'label', 'a release name') }
      const tree = moveSheet(factory, { request: null, answer: null, sentence: null }, [target])
      const options = all(tree, MOVE_TARGET_CLASS)[0]
      if (options === undefined) throw new Error('no target control')
      const refusedOption = options.childNodes().find((child) => child.textContent === UNAVAILABLE_MARK)
      if (refusedOption === undefined) throw new Error('the target list drew no mark')
      return { text: refusedOption.textContent, reason: refusedOption.getAttribute('title') }
    },
  },
  {
    id: 'keys.scope',
    file: 'renderer/manage/kinds/keys.ts',
    cause: 'missing-field',
    text: UNAVAILABLE_MARK,
    says: null,
    drive: () => {
      const rows = KEYS_KIND.decode([{ name: 'example-key' }])
      return { text: rows[0]?.cells['scope'] ?? null, reason: null }
    },
  },
  {
    id: 'projects.setting-key',
    file: 'renderer/screens/projects.ts',
    cause: 'refused-read',
    text: UNAVAILABLE_MARK,
    says: null,
    drive: () => {
      const model = buildProjectsSettings({ raw: settingsDocument(), because: null })
      const host = new FakeElement('div')
      fillProjectsSettings(factory, model, host)
      const pencils = all(host, SETTINGS_PENCIL_CLASS).filter(
        (each) => each.getAttribute(SETTINGS_PENCIL_KEY) === UNAVAILABLE_MARK,
      )
      expect(pencils).toHaveLength(1)
      const pencil = pencils[0]
      if (pencil === undefined) throw new Error('no pencil was named by the mark')
      // The button's accessible name is the key drawn (D1116), so a key the
      // class refused is announced as the mark rather than as a blank.
      expect(pencil.getAttribute('aria-label')).toBe(`Edit ${UNAVAILABLE_MARK}`)
      return { text: pencil.getAttribute(SETTINGS_PENCIL_KEY), reason: null }
    },
  },
  {
    id: 'live.bar-fill',
    file: 'renderer/screens/live.ts',
    cause: 'missing-field',
    text: UNAVAILABLE_MARK,
    says: 'the document carries no total.max_parallel, so the console cannot say how full the concurrent-run cap is',
    drive: () => {
      // `bar` is the one place this file calls `formatReading` for a fill, and it
      // draws the same reading twice: the text and its state through
      // `putReading` (which is where the reason lands) and the share it is filled
      // at through `data-fill`, which is the string a test reads. The invented
      // feed carries no `total.max_parallel`, so the concurrent-run cap is the
      // cap's own unavailable reading and the bar is the mark.
      const element = one(renderLive(factory, buildLiveModel(liveFrame()), 'light'), 'live-fleet-runs-cap')
      return { text: element.getAttribute('data-fill'), reason: element.getAttribute('title') }
    },
  },
  {
    id: 'models.input',
    file: 'renderer/manage/kinds/models.ts',
    cause: 'missing-field',
    text: UNAVAILABLE_MARK,
    says: null,
    drive: () => {
      const rows = MODELS_KIND.decode(modelsDocument())
      return { text: rows[0]?.cells['input'] ?? null, reason: null }
    },
  },
  {
    id: 'models.output',
    file: 'renderer/manage/kinds/models.ts',
    cause: 'missing-field',
    text: UNAVAILABLE_MARK,
    says: null,
    drive: () => {
      const rows = MODELS_KIND.decode(modelsDocument())
      return { text: rows[0]?.cells['output'] ?? null, reason: null }
    },
  },
  {
    id: 'pools.usage',
    file: 'renderer/manage/kinds/pools.ts',
    cause: 'missing-field',
    text: `${UNAVAILABLE_MARK} calls, ${UNAVAILABLE_MARK} tokens`,
    says: null,
    drive: () => {
      const rows = poolsKind.decode(poolsDocument())
      return { text: rows[0]?.cells['usage'] ?? null, reason: null }
    },
  },
  {
    id: 'pools.headroom',
    file: 'renderer/manage/kinds/pools.ts',
    cause: 'missing-field',
    text: UNAVAILABLE_MARK,
    says: null,
    drive: () => {
      const rows = poolsKind.decode(poolsDocument())
      return { text: rows[0]?.cells['headroom'] ?? null, reason: null }
    },
  },
  {
    id: 'budget.counted',
    file: 'renderer/manage/kinds/budget.ts',
    cause: 'missing-field',
    text: UNAVAILABLE_MARK,
    says: null,
    drive: () => {
      const rows = budgetKindFor(SUPPORTED).decode(budgetDocument())
      return { text: rows[0]?.cells['counted'] ?? null, reason: null }
    },
  },
  {
    id: 'budget.left',
    file: 'renderer/manage/kinds/budget.ts',
    cause: 'missing-field',
    text: UNAVAILABLE_MARK,
    says: null,
    drive: () => {
      const rows = budgetKindFor(SUPPORTED).decode(budgetDocument())
      return { text: rows[0]?.cells['left'] ?? null, reason: null }
    },
  },
  {
    id: 'budget.cap',
    file: 'renderer/manage/kinds/budget.ts',
    cause: 'missing-field',
    text: `${UNAVAILABLE_MARK} (repo)`,
    says: null,
    drive: () => {
      const rows = budgetKindFor(SUPPORTED).decode(budgetDocument())
      return { text: rows[0]?.cells['daily'] ?? null, reason: null }
    },
  },
  {
    id: 'graph.release-bar',
    file: 'renderer/screens/graph.ts',
    cause: 'missing-field',
    text: `${UNAVAILABLE_MARK} of 3 done`,
    says: null,
    drive: () => {
      // A release's counters are the decoder's readings and the bar is one text
      // run composed from them: a counter the document does not carry is a mark
      // in the middle of the bar and never a `0`.
      const model = buildGraphModel({
        document: graphDocument(['plans', 0, 'releases', 0, 'done'], undefined),
        tab: null,
      })
      const tree = renderGraph(
        factory,
        { model, positions: null, state: GRAPH_INITIAL_STATE, runs: [], notice: null },
        'light',
      )
      // The mark sits inside the bar's own run ("n/a of 3 done"), so the bar is
      // found by the run it carries rather than by being the mark itself.
      const marked = all(tree, 'graph-release-bar').filter((each) =>
        (each.textContent ?? '').includes(UNAVAILABLE_MARK),
      )
      expect(marked).toHaveLength(1)
      const element = marked[0]
      if (element === undefined) throw new Error('no refused release bar')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'graph.estimate-line',
    file: 'renderer/screens/graph.ts',
    cause: 'missing-field',
    text: `${GRAPH_ESTIMATE_READY} ${UNAVAILABLE_MARK}${ESTIMATE_RANGE_MARK}${UNAVAILABLE_MARK} ${GRAPH_ESTIMATE_MINUTES}, ${GRAPH_ESTIMATE_NO_HISTORY} ${UNAVAILABLE_MARK} ${GRAPH_ESTIMATE_TASKS}`,
    says: null,
    drive: () => {
      // The estimate line holds this file's other two `formatReading` sites: the
      // range (`rangeText`) and the count of tasks with no figure
      // (`unknownCostText`, D2723). `ready_in_minutes` is required by the
      // decoder, so a range with no ends in it is how a document reaches the line
      // with nothing to draw.
      const model = buildGraphModel({
        document: graphDocument(['plans', 0, 'estimate'], { ready_in_minutes: {} }),
        tab: null,
      })
      const tree = renderGraph(
        factory,
        { model, positions: null, state: GRAPH_INITIAL_STATE, runs: [], notice: null },
        'light',
      )
      const line = one(tree, 'graph-list-estimate')
      return { text: line.textContent, reason: line.getAttribute('title') }
    },
  },
]

/** settingsDocument is `settings --json` with one row whose key the `branch`
 * class refuses: a key is a dotted identifier, and the cell and the control
 * that opens the editor both draw the mark for one this console will not. */
function settingsDocument(): unknown {
  return {
    version: 1,
    project: 'example-project',
    generated_at: '2026-11-03T06:00:00Z',
    sources: [{ layer: 'default', read_at: '2026-11-03T06:00:00Z', duration_ms: 0, status: 'ok' }],
    settings: [
      {
        key: 'a key with spaces',
        scope: 'project',
        value: '8',
        wins: 'project',
        default: { value: '10', source: 'settings.DefaultPerRunUSD' },
        state: 'ok',
        writable: true,
        owner: '',
        target_layer: 'project',
      },
      {
        key: 'budget.per_run_usd',
        scope: 'project',
        value: '8',
        wins: 'project',
        default: { value: '10', source: 'settings.DefaultPerRunUSD' },
        state: 'ok',
        writable: true,
        owner: '',
        target_layer: 'project',
      },
    ],
  }
}

/**
 * NO_REASON_SENTENCE is every site that draws the mark and no reason, with the
 * sentence naming where its cause is stated instead. The set is pinned so that
 * a site which loses a reason it used to draw fails `states a cause`, and so
 * that a new site which draws a bare mark cannot be added without a line here.
 */
const NO_REASON_SENTENCE: Readonly<Record<string, string>> = Object.freeze({
  'run.busy-label':
    'the mark is inside a control\'s own label ("Reading <what> of n/a"), which is a string rather than an element: the cause is the refusal of row.run, and the label cannot carry a title',
  'graph.canvas-title':
    'the card\'s title lines are SVG text with no title slot; the cause is the refusal the model put on the node\'s title, and the same value is drawn with its reason in the list view (graph.list-cell)',
  'graph.card-label':
    'the card is named by its aria-label, which is a string: the cause is the refusal the model put on the node\'s title',
  'move.target-option':
    'an <option> has no title slot the sheet draws; the cause is the refusal of the release name the screen was handed (MoveTarget.name)',
  'keys.scope':
    'the mark is a model cell (ManageRow.cells.scope) the shell draws as text: the cause is the row the contract leaves no scope on',
  'projects.setting-key':
    'the cell and the pencil\'s data-setting-key both read the mark, and neither has a title slot: the cause is the branch class refusing the key',
  'models.input':
    'the price is a table cell (ManageRow.cells.input) drawn as text, with no title slot: the cause is the document\'s missing models[].input_per_m, and where the CLI has a reason for a model it cannot price, that reason is the neighbouring notes cell (the document\'s `refused`)',
  'models.output':
    'the price is a table cell (ManageRow.cells.output) drawn as text, with no title slot: the cause is the document\'s missing models[].output_per_m, and the CLI\'s own reason, where it has one, is the notes cell',
  'pools.usage':
    'the counts are one table cell (ManageRow.cells.usage) drawn as text: the cause is the fields the document does not carry (pools[].members[].usage.calls and .tokens)',
  'pools.headroom':
    'the share is a table cell (ManageRow.cells.headroom) drawn as text: the cause is the pools[].members[].headroom the document does not carry',
  'budget.counted':
    'the figure is a table cell (ManageRow.cells.counted) drawn as text: the cause is the repos[].counted_usd the budget show document does not carry',
  'budget.left':
    'the figure is a table cell (ManageRow.cells.left) drawn as text: the cause is the repos[].headroom_usd the budget show document does not carry',
  'budget.cap':
    'the cap is a table cell (ManageRow.cells.daily and .perRun) drawn as text, and the scope word beside the mark is the cell\'s own: the cause is the repos[].daily_cap_usd the document does not carry',
  'graph.release-bar':
    'the bar is one text run ("n of m done") with no title slot: the cause is the reading the decoder made for a counter the document does not carry (release.done.because), which the model holds and the run cannot draw',
  'graph.estimate-line':
    'the line is one text run composed of readings with no title slot, except where a withheld figure sets one: the cause is the readings the decoder made for a range or a count the document does not carry',
})

// ---------------------------------------------------------------------------
// The calls that cannot draw the mark
// ---------------------------------------------------------------------------

/**
 * CANNOT_DRAW is every `formatReading` call this tree has that **cannot** yield
 * the mark, keyed by `file:symbol`, with the guard that keeps it from one. The
 * mark's door is `formatReading` (it reads the constant), so a file is caught by
 * `the scan` whether it names the mark or only formats a reading -- and a call
 * that cannot draw it has to be *named* rather than silently left out, which is
 * what this list is for.
 *
 * Each line names the symbol it is about, and the scan asserts that symbol is
 * still in that file, so an entry cannot rot into a name nobody has. An entry is
 * only ever a *call site*; the file it lives in may still be tabled, and one is.
 */
const CANNOT_DRAW: Readonly<Record<string, string>> = Object.freeze({
  'renderer/screens/live.ts:spark':
    'a bar\'s height and the rate its title carries both come from `sparkline`, which makes a sample the document does not carry a gap (the em dash, `gap`) and every other sample a `measured` share: neither state can be the mark',
  'renderer/screens/live.ts:staleBanner':
    '`data-silence` draws `feedReading(feed)`, and the banner is drawn only for a feed `FeedHealth` calls `stale` -- which is a silence it measured, so the reading always has a value',
  'renderer/screens/live.ts:reviewSection':
    'the count is `measured(model.reviewCount)`, and `reviewCount` is this console\'s count of the list the document carries: `schema.ts` refuses a document with no list before a screen sees it, so the count is never a reading without a value',
  'renderer/entry.ts:drawGauge':
    'the title\'s three `formatReading` calls are inside the branch that draws one, and that branch is guarded by `counted` and `limit` both being numbers: the two figures it formats are the ones the guard just checked, and the third is `measured(share * 100)`',
  'renderer/screens/spend.ts:barRect':
    'the height is `bar.share`, which `barOf` makes `gap(reading.because)` for a value the document does not carry and `measured(share)` for every other -- a gap is the em dash, never the mark',
  'renderer/manage/kinds/pools.ts:headroomText':
    'the second `formatReading` in `headroomText` is `measured(reading.value * 100)`, a share of a value the line above just proved is not null',
})

// ---------------------------------------------------------------------------
// The identities: a present, valid value never draws the mark
// ---------------------------------------------------------------------------

/** IDENTITIES is the red step: before ID1 and ID2 each of these drew `n/a`. */
const IDENTITIES: readonly { readonly id: string; readonly drive: () => Drawn }[] = [
  {
    id: 'run.started-by',
    drive: () => {
      const model = buildRunModel({ diagnosis: { row: { launched_by: ACCOUNT } }, logs: null, logLink: null })
      const element = one(renderRun(factory, model, 'light'), 'run-started-by')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'run.requested-by',
    drive: () => {
      const model = buildRunModel({ diagnosis: { row: { requested_by: ACCOUNT } }, logs: null, logLink: null })
      const element = one(renderRun(factory, model, 'light'), 'run-requested-by')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'run.account',
    drive: () => {
      const model = buildRunModel({ diagnosis: { row: { oauth_account: ACCOUNT } }, logs: null, logLink: null })
      const element = one(renderRun(factory, model, 'light'), 'run-account')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'live.started-by',
    drive: () => {
      const frame = liveFrame()
      const runs = frame.runs.map((run) => ({ ...run, launchedBy: ACCOUNT }))
      const element = one(renderLive(factory, buildLiveModel({ ...frame, runs }), 'light'), 'live-run-started-by')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
  {
    id: 'app.put-text',
    drive: () => {
      const element = new FakeElement('span')
      putText(element, ACCOUNT, 'person', 'row.launched_by')
      return { text: element.textContent, reason: element.getAttribute('title') }
    },
  },
]

// ---------------------------------------------------------------------------
// The cases
// ---------------------------------------------------------------------------

describe('every n/a names its cause (ID5, F28)', () => {
  it('draws the mark at every site that can draw it', () => {
    for (const site of SITES) {
      const drawn = site.drive()
      expect(drawn.text, site.id).toBe(site.text)
      expect(drawn.text ?? '', site.id).toContain(UNAVAILABLE_MARK)
    }
  })

  it('states a cause from the three F28 names, and no other', () => {
    const kinds = new Set<CauseKind>()
    for (const site of SITES) {
      kinds.add(site.cause)
      expect(['missing-field', 'version-floor', 'refused-read'], site.id).toContain(site.cause)
    }
    // All three are covered: a table that only ever said "a field is missing"
    // would not hold F28's version floors or its refused reads.
    expect([...kinds].sort()).toEqual(['missing-field', 'refused-read', 'version-floor'])
  })

  it('carries the sentence on the element wherever the site states one', () => {
    for (const site of SITES) {
      const drawn = site.drive()
      if (site.says !== null) {
        expect(drawn.reason, site.id).toBe(site.says)
        // The reason is a sentence a reader is given, never the mark again and
        // never the refused value itself.
        expect(drawn.reason, site.id).not.toBe(UNAVAILABLE_MARK)
        expect(drawn.reason, site.id).not.toContain(MULTILINE)
        continue
      }
      expect(drawn.reason, site.id).toBeNull()
      // A site with no sentence is one the table names, and each line says
      // where its cause is stated instead.
      expect(Object.keys(NO_REASON_SENTENCE), site.id).toContain(site.id)
    }
    // And no line names a site that does state a sentence, or a site that is
    // no longer in the table: the list cannot grow into a blanket permission.
    for (const id of Object.keys(NO_REASON_SENTENCE)) {
      const site = SITES.find((one) => one.id === id)
      expect(site, id).toBeDefined()
      expect(site?.says, id).toBeNull()
    }
  })

  it('names the field a missing-field mark is about, and not the value', () => {
    for (const site of SITES) {
      if (site.cause !== 'missing-field' || site.says === null) continue
      expect(site.says, site.id).toMatch(/^the document (carries no|has no) /)
    }
  })

  it('draws a present, valid identity whole, and never the mark', () => {
    for (const identity of IDENTITIES) {
      const drawn = identity.drive()
      expect(drawn.text, identity.id).toBe(ACCOUNT)
      expect(drawn.text, identity.id).not.toBe(UNAVAILABLE_MARK)
      expect(drawn.reason, identity.id).toBeNull()
    }
  })

  it('covers every renderer file that can draw the mark, and only those', () => {
    // A file can draw the mark two ways: by naming it, or by formatting a
    // reading, which is the one door the mark reaches a screen through
    // (`availability.ts`'s `formatReading` reads the constant). Counting only the
    // first is what would let a whole screen wired to a reading slip past this
    // table, so both are counted.
    const files = readdirSync(SOURCE, { recursive: true })
      .map((each) => String(each))
      .filter((each) => each.endsWith('.ts'))
      .map((each) => ({ path: each, source: readFileSync(join(SOURCE, each), 'utf8') }))
      .filter(
        (each) =>
          each.source.includes('UNAVAILABLE_MARK') ||
          each.source.includes('ABSENT_MARK') ||
          each.source.includes('formatReading('),
      )
      .map((each) => each.path.replace(/\\/g, '/'))
      .sort()

    // A file is covered by a row of the table, or by a `CANNOT_DRAW` line naming
    // the call in it that cannot yield the mark -- never by neither.
    const tabled = new Set(SITES.map((site) => site.file))
    const excused = new Set(Object.keys(CANNOT_DRAW).map((key) => key.slice(0, key.lastIndexOf(':'))))
    expect([...new Set([...tabled, ...excused])].sort()).toEqual(files)

    // Every row names a file that exists, so the table cannot point at a
    // module this tree no longer has.
    for (const site of SITES) expect(() => readFileSync(join(SOURCE, site.file), 'utf8'), site.id).not.toThrow()

    // And every excuse names a symbol that file still has, so an entry cannot
    // outlive the call it is about and quietly excuse whatever replaces it.
    for (const [key, why] of Object.entries(CANNOT_DRAW)) {
      const at = key.lastIndexOf(':')
      const source = readFileSync(join(SOURCE, key.slice(0, at)), 'utf8')
      expect(source, key).toContain(key.slice(at + 1))
      expect(why, key).not.toBe('')
    }
  })
})
