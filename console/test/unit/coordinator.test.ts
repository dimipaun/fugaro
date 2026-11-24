// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Coordinator tab as a graph of the project's active orchestrators (console
// CG7b, D3210; the read is C-ORCH's map document,
// `docs/console-fixes/contract-orchestrators.md`).
//
// The tab is **one section over one read**: `fugaro orchestrator map --json`
// laid out by the engine and drawn as boxes and edges, a health line under it,
// and a drill-down that reads one orchestrator's own items through
// `graph.read --source orchestrator:<id>` -- a scope CG7a's grammar already
// admits, so this screen adds no word to either allow-list (the map's own row
// and operation are pinned in `ipc.test.ts`, `spawn.test.ts` and
// `preload-surface.test.ts`).
//
// What this file pins is what the task's acceptance names:
//
//   - **the graph from a fixture map** -- a box per node with its name, driver
//     and two counts; a dependency solid and an overlap dashed and marked; the
//     overlap count as a warning beside it;
//   - **the drill-down** -- a press opens one orchestrator's live tasks, the
//     read's own mark goes up while it is out, and a refusal or an empty
//     document is drawn as itself;
//   - **the health line** -- the sweep's heartbeat and one lease age per node,
//     with `null` drawn as "no readable lease stamp" and never as a zero, and
//     with the sweep off said in words and a fix named rather than an absence
//     of dashed edges drawn as "nothing overlaps" (D3211);
//   - **no v2 section** -- the action queue, the totals, the job list and the
//     transcript are gone, and a drawing carries none of their hooks.
//
// Everything runs against a fake factory that **throws on the attributes a
// screen may not set** (`href`, `src`, `style`, `on*`), so a screen that wrote a
// document's string into an attribute would fail here rather than in a window.
// The screen is drawn through `renderCoordinator`, which is a pure function of a
// picture, so every case below is a case about the drawing rather than about a
// window.

import { describe, expect, it } from 'vitest'

import {
  COORDINATOR_ATTRIBUTE,
  COORDINATOR_BUSY,
  COORDINATOR_CANVAS_LABEL,
  COORDINATOR_DEPENDS_WORD,
  COORDINATOR_EDGE_CLASS,
  COORDINATOR_EDGE_DEPENDS_CLASS,
  COORDINATOR_EDGE_OVERLAP_CLASS,
  COORDINATOR_EMPTY_CLASS,
  COORDINATOR_EMPTY_WORD,
  COORDINATOR_FLOOR_AND_WORD,
  COORDINATOR_FLOOR_CLASS,
  COORDINATOR_FLOOR_VERSION,
  COORDINATOR_FLOOR_WORD,
  COORDINATOR_GAP,
  COORDINATOR_GRAPH_CLASS,
  COORDINATOR_HEALTH_BEAT_WORD,
  COORDINATOR_HEALTH_CLASS,
  COORDINATOR_HEALTH_NEVER_WORD,
  COORDINATOR_HEALTH_OFF_WORD,
  COORDINATOR_HEALTH_ON_WORD,
  COORDINATOR_HEALTH_WORD,
  COORDINATOR_LEASE_AGE_CLASS,
  COORDINATOR_LEASE_AGE_WORD,
  COORDINATOR_LEASE_CLASS,
  COORDINATOR_LEASE_WORD,
  COORDINATOR_LIST_CLASS,
  COORDINATOR_NO_AGE_WORD,
  COORDINATOR_NO_CANVAS,
  COORDINATOR_NO_CANVAS_CLASS,
  COORDINATOR_NODE_BOX_CLASS,
  COORDINATOR_NODE_CLASS,
  COORDINATOR_NODE_DRIVER_CLASS,
  COORDINATOR_NODE_HEIGHT,
  COORDINATOR_NODE_NAME_CLASS,
  COORDINATOR_NODE_OPTION,
  COORDINATOR_NODE_WIDTH,
  COORDINATOR_OVERLAP_SINGULAR,
  COORDINATOR_OVERLAP_WARNING_WORD,
  COORDINATOR_OVERLAP_WORD,
  COORDINATOR_PAD,
  COORDINATOR_ROW_CLASS,
  COORDINATOR_ROW_EDGE_CLASS,
  COORDINATOR_SOURCE_CLASS,
  COORDINATOR_SOURCE_LINE,
  COORDINATOR_SWEEP_ATTRIBUTE,
  COORDINATOR_SWEEP_OFF_CLASS,
  COORDINATOR_SWEEP_OFF_FIX_WORD,
  COORDINATOR_SWEEP_OFF_NOTE_CLASS,
  COORDINATOR_SWEEP_OFF_WORD,
  COORDINATOR_TASK_CLASS,
  COORDINATOR_TASK_REPO_CLASS,
  COORDINATOR_TASK_STATE_CLASS,
  COORDINATOR_TASK_TITLE_CLASS,
  COORDINATOR_TASKS_BUSY,
  COORDINATOR_TASKS_CLASS,
  COORDINATOR_TASKS_CLOSE_CLASS,
  COORDINATOR_TASKS_CLOSE_WORD,
  COORDINATOR_TASKS_EMPTY_CLASS,
  COORDINATOR_TASKS_EMPTY_WORD,
  COORDINATOR_TASKS_NOT_UP_WORD,
  COORDINATOR_TASKS_WORD,
  COORDINATOR_TASKS_WORD_CLASS,
  COORDINATOR_THEMES,
  COORDINATOR_TITLE,
  COORDINATOR_TITLE_CLASS,
  COORDINATOR_TASKS_COUNT_WORD,
  COORDINATOR_JOBS_COUNT_WORD,
  COORDINATOR_UNKNOWN_DRIVER_WORD,
  COORDINATOR_WARNING_ATTRIBUTE,
  COORDINATOR_WARNING_CLASS,
  COORDINATOR_WARNING_COUNT_CLASS,
  CoordinatorLayoutClient,
  CoordinatorScreen,
  buildCoordinatorModel,
  coordinatorDrawing,
  coordinatorLayout,
  coordinatorTasks,
  renderCoordinator,
  renderCoordinatorFloor,
} from '../../src/renderer/screens/coordinator'
import type {
  CoordinatorModel,
  CoordinatorPicture,
  CoordinatorTask,
  CoordinatorTheme,
  ScreenElement,
  ScreenFactory,
} from '../../src/renderer/screens/coordinator'
import { BUSY_SLOT_CLASS } from '../../src/renderer/section'
import type { WorkerLike } from '../../src/renderer/graph/layout-client'
import type { Positions } from '../../src/renderer/graph/layout-client'
import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import { decodeGraphDocument, decodeOrchestratorMap } from '../../src/shared/schema'
import type { GraphView, OrchestratorMapView } from '../../src/shared/schema'

// ---------------------------------------------------------------------------
// The harness: a fake element that refuses what a screen may not do.
// ---------------------------------------------------------------------------

/** FakeScreen is the element this screen draws into. It records text, classes
 * and attributes, and it **throws on the attributes a screen must never set**:
 * the navigating attribute, a resource, a style and any handler. A harness that
 * merely recorded an `onload` would let the screen build one and leave the
 * assertion three lines below to remember to look; this one fails at the call.
 *
 * `createNS` is the same element: the screen's SVG half differs from its HTML
 * half by the namespace it asks the document for, and this fake records the
 * namespace rather than the element, so a case can tell the two apart where it
 * matters and nowhere else. */
class FakeScreen implements ScreenElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  readonly classes: string[] = []
  textContent: string | null = null

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
    readonly namespace = '',
  ) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeScreen.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
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

  append(child: ScreenElement): void {
    this.nodes.push(child as FakeScreen)
  }

  childNodes(): readonly ScreenElement[] {
    return this.nodes
  }
}

/** factory makes fresh fakes, in the shape the renderer is handed. */
const factory: ScreenFactory = { create: (tag) => new FakeScreen(tag) }

/** coordinatorFactory is the same factory with the SVG door the screen asks
 * for. */
const coordinatorFactory = {
  create: (tag: string) => new FakeScreen(tag),
  createNS: (tag: string) => new FakeScreen(tag, 'svg'),
}

/** walk visits every node of a tree, the root first. */
function walk(node: ScreenElement): readonly FakeScreen[] {
  const fake = node as FakeScreen
  const out: FakeScreen[] = [fake]
  for (const child of fake.childNodes()) out.push(...walk(child))
  return out
}

/** all is every node carrying a class, in document order. */
function all(node: ScreenElement, className: string): readonly FakeScreen[] {
  return walk(node).filter((one) => one.classes.includes(className))
}

/** one is the single node carrying a class, asserted to be single. */
function one(node: ScreenElement, className: string): FakeScreen {
  const found = all(node, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** textOf is the single node carrying a class, its own text. */
function textOf(node: ScreenElement, className: string): string {
  return one(node, className).textContent ?? ''
}

/** drawn is the whole tree's text, in document order, one space between the
 * pieces -- which is how a case about a sentence reads it. */
function drawn(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

/** said is the whole tree's text the way a browser concatenates it: every
 * node's own text, in document order, **with nothing put between them**.
 * `drawn` joins with a space, so a box's own line -- the word `tasks`, a space,
 * then the figure -- is the case only this one reads. */
function said(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .join('')
}

// ---------------------------------------------------------------------------
// The documents.
// ---------------------------------------------------------------------------

/**
 * MAP is one map document: the project's own orchestrator and a named one,
 * each driven, joined by a dependency one way and an overlap the other, with
 * the sweep on and a heartbeat. It is written from the contract's own table
 * (`docs/console-fixes/contract-orchestrators.md`) rather than recorded, so the
 * two readings a screen turns on -- the default orchestrator's empty `name` and
 * a lease with no readable stamp -- are both in it.
 */
const MAP = {
  version: 1,
  project: 'repo-a',
  generated_at: '2026-11-22T10:00:00Z',
  nodes: [
    {
      id: 'repo-a',
      name: '',
      driver: 'person-1',
      active_tasks: 2,
      running_jobs: 1,
      lease_age_seconds: 90,
    },
    {
      id: 'repo-a~auth',
      name: 'auth',
      driver: 'person-2',
      active_tasks: 0,
      running_jobs: 3,
      lease_age_seconds: null,
    },
  ],
  edges: [
    { from: 'repo-a~auth', to: 'repo-a', kind: 'dependency', reason: '' },
    { from: 'repo-a', to: 'repo-a~auth', kind: 'overlap', reason: 'both touch src/auth' },
  ],
  health: {
    sweep: true,
    started_at: '2026-11-22T09:59:00Z',
    ok_at: '2026-11-22T09:59:30Z',
    at: '2026-11-22T10:00:00Z',
    note: '',
  },
}

/** mapDecoded decodes a map document, or the failure is the decoder's own
 * sentence rather than an assertion three lines later. */
function mapDecoded(raw: unknown): OrchestratorMapView {
  const result = decodeOrchestratorMap(raw)
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
}

/** withMap is `MAP` with one node or edge field replaced, so a case is about
 * the one thing it changes. */
function withMap(change: Record<string, unknown>): unknown {
  return { ...MAP, ...change }
}

/** modelOf is one document as the drawing's own model. */
function modelOf(raw: unknown): CoordinatorModel {
  return buildCoordinatorModel(mapDecoded(raw))
}

/** ITEMS is one orchestrator's own document, as `graph.read --source
 * orchestrator:<id>` answers it: the items it holds, each with the four strings
 * the drill-down draws. */
const ITEMS = {
  version: 1,
  stream_id: 'repo-a~auth',
  seq: 4,
  generated_at: '2026-11-22T10:00:00Z',
  sources: [],
  releases: [],
  nodes: [
    {
      key: 'plan-1/task-1',
      source: 'record',
      kind: 'task',
      id: 'plan-1/task-1',
      title: 'a task being worked',
      repo: 'owner/repo-a',
      state: 'running',
      release: '',
      updated: null,
      plan: 'plan-1',
      stream: '1.5',
      version: '1.5',
      dangling: false,
    },
  ],
  edges: [],
  warnings: [],
}

/** itemsDecoded decodes one orchestrator's document. */
function itemsDecoded(raw: unknown): GraphView {
  const result = decodeGraphDocument(raw)
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
}

/** scopeOf is the graph scope the drill-down reads one orchestrator through,
 * spelled as the entry spells it for the read itself: the scope is what the
 * document's own `sources[]` row is matched against, so a case that spelled it
 * another way would be asking about a scope the read never named. */
function scopeOf(id: string): string {
  return `orchestrator:${id}`
}

/** tasksOf is one document's rows through `coordinatorTasks`, for the cases
 * whose scope answered `up` (or listed no source of its own): the rows are the
 * whole answer there, and a refusal is a case of its own below. */
function tasksOf(view: GraphView): readonly CoordinatorTask[] {
  const read = coordinatorTasks(view, scopeOf('repo-a~auth'))
  if (read.kind === 'refused') throw new Error(read.sentence)
  return read.tasks
}

/** withSource is one orchestrator document with its own `sources[]` row set: the
 * scope the drill-down read, in the state and with the reason a case is about. */
function withSource(raw: unknown, state: string, reason: string): unknown {
  return {
    ...(raw as Record<string, unknown>),
    sources: [{ source: scopeOf('repo-a~auth'), state, as_of: null, reason }],
  }
}

/** positionsFor is a layout covering a model: every node placed in a row, every
 * edge routed. The ids are the screen's own (`coordinatorLayout`'s), so a case
 * about the drawing is not also a case about the worker's id format. */
function positionsFor(model: CoordinatorModel): Positions {
  const graph = coordinatorLayout(model)
  return {
    nodes: graph.children.map((child, index) => ({
      id: child.id,
      x: index * (COORDINATOR_NODE_WIDTH + COORDINATOR_GAP),
      y: 0,
      width: child.width,
      height: child.height,
    })),
    edges: graph.edges.map((edge) => ({
      id: edge.id,
      sections: [{ startPoint: { x: 0, y: 0 }, endPoint: { x: 40, y: 40 } }],
    })),
  }
}

/** pictureOf is one drawing's picture: a map that has landed, placed, with no
 * panel open and no notice. */
function pictureOf(raw: unknown, over: Partial<CoordinatorPicture> = {}): CoordinatorPicture {
  const model = modelOf(raw)
  return {
    model,
    positions: positionsFor(model),
    selected: null,
    tasks: { kind: 'closed' },
    notice: null,
    ...over,
  }
}

/** renders draws one picture in the dark theme. */
function renders(picture: CoordinatorPicture, theme: CoordinatorTheme = 'dark'): ScreenElement {
  return renderCoordinator(coordinatorFactory, picture, theme)
}

/** aWorker answers the layout protocol as the engine does, and records every
 * request it was posted: one entry per command, so a case can read the
 * `layoutOptions` the client sent. */
class FakeWorker implements WorkerLike {
  readonly sent: Record<string, unknown>[] = []
  private readonly listeners = new Map<string, ((event: { data?: unknown }) => void)[]>()

  constructor(private readonly refuse = false) {}

  postMessage(message: unknown): void {
    const request = message as Record<string, unknown>
    this.sent.push(request)
    if (request['cmd'] !== 'layout') return
    if (this.refuse) {
      queueMicrotask(() => this.answer({ id: request['id'], error: 'this worker lays nothing out' }))
      return
    }
    const graph = request['graph'] as { children: { id: string }[]; edges: { id: string }[] }
    const data = {
      children: graph.children.map((child, index) => ({
        id: child.id,
        x: index * 300,
        y: 0,
        width: COORDINATOR_NODE_WIDTH,
        height: COORDINATOR_NODE_HEIGHT,
      })),
      edges: graph.edges.map((edge) => ({
        id: edge.id,
        sections: [{ startPoint: { x: 0, y: 0 }, endPoint: { x: 40, y: 40 } }],
      })),
    }
    queueMicrotask(() => this.answer({ id: request['id'], data }))
  }

  addEventListener(type: string, listener: (event: { data?: unknown }) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  terminate(): void {}

  /** layouts is the number of layouts this worker was asked to run. */
  get layouts(): number {
    return this.sent.filter((each) => each['cmd'] === 'layout').length
  }

  /** options is the `layoutOptions` of the last layout posted. */
  get options(): Readonly<Record<string, string>> {
    const last = [...this.sent].reverse().find((each) => each['cmd'] === 'layout')
    return (last?.['layoutOptions'] ?? {}) as Readonly<Record<string, string>>
  }

  private answer(reply: unknown): void {
    for (const listener of this.listeners.get('message') ?? []) listener({ data: reply })
  }
}

/** screenOver is a screen over the fake worker, with the worker beside it. */
function screenOver(): { screen: CoordinatorScreen; worker: FakeWorker } {
  const worker = new FakeWorker()
  const screen = new CoordinatorScreen({
    factory: coordinatorFactory,
    client: new CoordinatorLayoutClient(() => worker),
    theme: 'light',
  })
  return { screen, worker }
}

// ---------------------------------------------------------------------------
// The model and the layout.
// ---------------------------------------------------------------------------

describe('the Coordinator tab as a graph (CG7b, D3210)', () => {
  describe('the map document as the drawing needs it', () => {
    it('carries every field the boxes are drawn with, and counts the overlaps', () => {
      const model = modelOf(MAP)
      expect(model.project).toBe('repo-a')
      expect(model.nodes).toHaveLength(2)
      expect(model.nodes[0]).toEqual({
        id: 'repo-a',
        name: '',
        driver: 'person-1',
        activeTasks: 2,
        runningJobs: 1,
        leaseAgeSeconds: 90,
      })
      // Null is carried as null and never folded into a zero: the box draws the
      // console's sentence for it, which a zero would take away.
      expect(model.nodes[1]?.leaseAgeSeconds).toBeNull()
      expect(model.edges[1]).toEqual({
        from: 'repo-a',
        to: 'repo-a~auth',
        kind: 'overlap',
        reason: 'both touch src/auth',
      })
      expect(model.overlaps).toBe(1)
      expect(model.health).toEqual({ sweep: true, okAt: '2026-11-22T09:59:30Z', note: '' })
    })

    it('counts an edge of a kind this build has not learned as no overlap', () => {
      const model = modelOf(
        withMap({
          edges: [{ from: 'repo-a', to: 'repo-a~auth', kind: 'contends', reason: 'a third relation' }],
        }),
      )
      expect(model.overlaps).toBe(0)
    })

    it('reads one orchestrator document as the drill-down rows, in its own order', () => {
      const tasks = tasksOf(itemsDecoded(ITEMS))
      expect(tasks).toEqual([
        { key: 'plan-1/task-1', title: 'a task being worked', state: 'running', repo: 'owner/repo-a' },
      ])
    })

    it('says the source is not up, and why, instead of calling a stream nobody read a quiet one', () => {
      // H3 and json-contract §6.22: a scope whose own source is not `up` answers
      // with no item for a reason of its own. Drawn as the empty sentence it
      // would report a stream nobody could read as an orchestrator doing
      // nothing, which is the one reading a reader acts on by doing nothing.
      const view = itemsDecoded(withSource(ITEMS, 'down', 'the stream was withdrawn'))
      const read = coordinatorTasks(view, scopeOf('repo-a~auth'))
      expect(read).toEqual({
        kind: 'refused',
        sentence: `${COORDINATOR_TASKS_NOT_UP_WORD}: down — the stream was withdrawn`,
      })
      const tree = renders(pictureOf(MAP, { selected: 'repo-a~auth', tasks: read }))
      expect(textOf(tree, COORDINATOR_TASKS_EMPTY_CLASS)).toBe(
        `${COORDINATOR_TASKS_NOT_UP_WORD}: down — the stream was withdrawn`,
      )
      expect(drawn(tree)).not.toContain(COORDINATOR_TASKS_EMPTY_WORD)

      // The state word is drawn whatever it is -- this build has no vocabulary
      // for the whole set -- and a source that gave no reason draws none.
      const silent = itemsDecoded(withSource(ITEMS, 'unreadable', ''))
      expect(coordinatorTasks(silent, scopeOf('repo-a~auth'))).toEqual({
        kind: 'refused',
        sentence: `${COORDINATOR_TASKS_NOT_UP_WORD}: unreadable`,
      })

      // A scope the document lists as `up`, and one it does not list at all,
      // are read exactly as they were: the rows, and the empty sentence when
      // there are none.
      expect(coordinatorTasks(itemsDecoded(withSource(ITEMS, 'up', '')), scopeOf('repo-a~auth'))).toEqual({
        kind: 'drawn',
        tasks: [{ key: 'plan-1/task-1', title: 'a task being worked', state: 'running', repo: 'owner/repo-a' }],
      })
      expect(coordinatorTasks(itemsDecoded(ITEMS), scopeOf('repo-a~auth')).kind).toBe('drawn')
    })
  })

  describe('the layout', () => {
    it('gives the worker ids and sizes and never a name', () => {
      const graph = coordinatorLayout(modelOf(MAP))
      expect(graph.id).toBe('coordinator')
      expect(graph.children).toEqual([
        { id: 'repo-a', width: COORDINATOR_NODE_WIDTH, height: COORDINATOR_NODE_HEIGHT },
        { id: 'repo-a~auth', width: COORDINATOR_NODE_WIDTH, height: COORDINATOR_NODE_HEIGHT },
      ])
      // No name, no driver and no count: the worker is handed ids and sizes,
      // and every child object is exactly those three keys.
      expect(JSON.stringify(graph)).not.toContain('person-1')
      expect(JSON.stringify(graph)).not.toContain('"name"')
      for (const child of graph.children) expect(Object.keys(child).sort()).toEqual(['height', 'id', 'width'])
    })

    it('gives one pair two edge ids when they both depend and overlap', () => {
      const graph = coordinatorLayout(modelOf(MAP))
      const ids = graph.edges.map((edge) => edge.id)
      expect(new Set(ids).size).toBe(ids.length)
      expect(graph.edges[0]?.sources).toEqual(['repo-a~auth'])
      expect(graph.edges[0]?.targets).toEqual(['repo-a'])
    })

    it('places a model with the positions the worker answered with', () => {
      const model = modelOf(MAP)
      const drawing = coordinatorDrawing(model, positionsFor(model))
      expect(drawing).not.toBeNull()
      expect(drawing?.boxes.map((box) => box.node.id)).toEqual(['repo-a', 'repo-a~auth'])
      expect(drawing?.routes).toHaveLength(2)
      expect(drawing?.width).toBeGreaterThan(0)
      expect(drawing?.height).toBeGreaterThan(0)
    })

    it('answers nothing for a layout that does not cover the model', () => {
      const model = modelOf(MAP)
      const positions = positionsFor(model)
      expect(coordinatorDrawing(model, { nodes: positions.nodes.slice(0, 1), edges: positions.edges })).toBeNull()
      expect(coordinatorDrawing(model, { nodes: positions.nodes, edges: [] })).toBeNull()
    })

    it('asks the engine for the room a box draws in', async () => {
      const worker = new FakeWorker()
      const client = new CoordinatorLayoutClient(() => worker)
      await client.layout(coordinatorLayout(modelOf(MAP)))
      expect(worker.options[COORDINATOR_NODE_OPTION]).toBe(String(COORDINATOR_GAP))
      expect(worker.options['elk.layered.spacing.nodeNodeBetweenLayers']).toBe(String(COORDINATOR_GAP))
      expect(worker.options['elk.spacing.componentComponent']).toBe(String(COORDINATOR_GAP))
      expect(COORDINATOR_GAP).toBe(COORDINATOR_PAD * 2)
    })
  })

  // -------------------------------------------------------------------------
  // The drawing.
  // -------------------------------------------------------------------------

  describe('the drawing', () => {
    it('draws the heading and the command line before any read answers', () => {
      const tree = renders({ model: null, positions: null, selected: null, tasks: { kind: 'closed' }, notice: null })
      expect((tree as FakeScreen).classes).toContain('coordinator')
      expect(tree.getAttribute('data-theme')).toBe('dark')
      expect(textOf(tree, COORDINATOR_TITLE_CLASS)).toBe(COORDINATOR_TITLE)
      expect(textOf(tree, COORDINATOR_SOURCE_CLASS)).toBe(COORDINATOR_SOURCE_LINE)
      // The busy slot is the section's own first child: the mark of a read that
      // is running goes above the figures already drawn.
      expect(tree.childNodes()[0]?.getAttribute('class')).toBe(BUSY_SLOT_CLASS)
      expect(all(tree, COORDINATOR_HEALTH_CLASS)).toHaveLength(0)
    })

    it('draws a box per node with its name, driver, counts and lease age', () => {
      const tree = renders(pictureOf(MAP))
      const boxes = all(tree, COORDINATOR_NODE_CLASS)
      expect(boxes.map((box) => box.getAttribute(COORDINATOR_ATTRIBUTE))).toEqual(['repo-a', 'repo-a~auth'])
      // The default orchestrator's own name is the project's (contract reading
      // 1): an empty label would be a box a reader cannot tell from a failed
      // read.
      expect(drawn(tree)).toContain('repo-a')
      expect(drawn(tree)).toContain('auth')
      expect(drawn(tree)).toContain('person-1')
      expect(said(tree)).toContain(`${COORDINATOR_TASKS_COUNT_WORD} 2`)
      expect(said(tree)).toContain(`${COORDINATOR_JOBS_COUNT_WORD} 1`)
      expect(drawn(tree)).toContain('1m 30s')
      const rects = all(tree, COORDINATOR_NODE_BOX_CLASS)
      expect(rects).toHaveLength(2)
      expect(rects[0]?.getAttribute('width')).toBe(String(COORDINATOR_NODE_WIDTH))
      expect(rects[0]?.getAttribute('height')).toBe(String(COORDINATOR_NODE_HEIGHT))
    })

    it('says a driver the read could not name is unknown, never nobody', () => {
      const tree = renders(
        pictureOf(withMap({ nodes: [{ ...MAP.nodes[0], driver: '' }, MAP.nodes[1]] })),
      )
      expect(drawn(tree)).toContain(COORDINATOR_UNKNOWN_DRIVER_WORD)
    })

    it('says a lease age it could not read is unreadable, never zero', () => {
      const tree = renders(pictureOf(MAP))
      const ages = all(tree, COORDINATOR_LEASE_AGE_CLASS)
      expect(ages).toHaveLength(2)
      expect(ages[0]?.textContent).toBe('1m 30s')
      expect(ages[1]?.textContent).toBe(UNAVAILABLE_MARK)
      expect(ages[1]?.getAttribute('title')).toBe(COORDINATOR_NO_AGE_WORD)
      // A zero would be a claim about a beat this reader does not have: the
      // second age is the console's own sentence, and no cell says `0s`.
      expect(ages.map((age) => age.textContent)).not.toContain('0s')
    })

    it('draws a dependency solid and an overlap dashed and marked', () => {
      const tree = renders(pictureOf(MAP))
      const edges = all(tree, COORDINATOR_EDGE_CLASS)
      expect(edges).toHaveLength(2)
      const depends = all(tree, COORDINATOR_EDGE_DEPENDS_CLASS)
      const overlaps = all(tree, COORDINATOR_EDGE_OVERLAP_CLASS)
      expect(depends).toHaveLength(1)
      expect(overlaps).toHaveLength(1)
      expect(depends[0]?.getAttribute(COORDINATOR_WARNING_ATTRIBUTE)).toBeNull()
      expect(overlaps[0]?.getAttribute(COORDINATOR_WARNING_ATTRIBUTE)).toBe('true')
      expect(overlaps[0]?.getAttribute('d')).toContain('M 0 0')
      // The overlap's own words are the tooltip on the dashed line: the one
      // thing a dash cannot say.
      expect(overlaps[0]?.childNodes()[0]?.textContent).toBe('both touch src/auth')
    })

    it('draws the overlap count and what an overlap means beside it', () => {
      const tree = renders(pictureOf(MAP))
      expect(textOf(tree, COORDINATOR_WARNING_COUNT_CLASS)).toBe('1')
      expect(drawn(tree)).toContain(COORDINATOR_OVERLAP_SINGULAR)
      expect(drawn(tree)).toContain(COORDINATOR_OVERLAP_WARNING_WORD)
    })

    it('says the sweep is off and names the fix rather than drawing no overlap as none', () => {
      const tree = renders(
        pictureOf(
          withMap({
            health: { sweep: false, started_at: null, ok_at: null, at: null, note: 'the sweep is not deployed' },
          }),
        ),
      )
      const note = one(tree, COORDINATOR_SWEEP_OFF_CLASS)
      expect(note.textContent).toBeNull()
      expect(drawn(note)).toContain(COORDINATOR_SWEEP_OFF_WORD)
      expect(drawn(note)).toContain(COORDINATOR_SWEEP_OFF_FIX_WORD)
      expect(textOf(note, COORDINATOR_SWEEP_OFF_NOTE_CLASS)).toBe('the sweep is not deployed')
      // The overlap warning is not drawn at all: with the sweep off the map
      // carries no overlap information (D3211), so a zero here would be a claim
      // the console has no right to make.
      expect(all(tree, COORDINATOR_WARNING_CLASS)).toHaveLength(0)
      expect(one(tree, COORDINATOR_HEALTH_CLASS).getAttribute(COORDINATOR_SWEEP_ATTRIBUTE)).toBe('off')
      expect(drawn(tree)).toContain(COORDINATOR_HEALTH_OFF_WORD)
      expect(drawn(tree)).not.toContain(COORDINATOR_HEALTH_BEAT_WORD)
    })

    it('shows the sweep heartbeat when there is one, and says when there is none', () => {
      const beat = renders(pictureOf(MAP))
      expect(one(beat, COORDINATOR_HEALTH_CLASS).getAttribute(COORDINATOR_SWEEP_ATTRIBUTE)).toBe('on')
      expect(drawn(beat)).toContain(COORDINATOR_HEALTH_ON_WORD)
      expect(drawn(beat)).toContain(COORDINATOR_HEALTH_BEAT_WORD)
      expect(drawn(beat)).toContain('2026-11-22T09:59:30Z')
      const never = renders(
        pictureOf(
          withMap({
            health: { sweep: true, started_at: null, ok_at: null, at: null, note: '' },
          }),
        ),
      )
      expect(drawn(never)).toContain(COORDINATOR_HEALTH_NEVER_WORD)
    })

    it('draws one lease row per node, by the name the box is headed by', () => {
      const tree = renders(pictureOf(MAP))
      const rows = all(tree, COORDINATOR_LEASE_CLASS)
      expect(rows.map((row) => row.getAttribute(COORDINATOR_ATTRIBUTE))).toEqual(['repo-a', 'repo-a~auth'])
      expect(drawn(rows[0] as ScreenElement)).toContain(COORDINATOR_LEASE_WORD)
      expect(drawn(rows[0] as ScreenElement)).toContain(COORDINATOR_LEASE_AGE_WORD)
      expect(textOf(rows[0] as ScreenElement, COORDINATOR_LEASE_AGE_CLASS)).toBe('1m 30s')
    })

    it('says nothing is being driven rather than drawing an empty canvas', () => {
      const tree = renders(pictureOf(withMap({ nodes: [], edges: [] })))
      expect(textOf(tree, COORDINATOR_EMPTY_CLASS)).toBe(COORDINATOR_EMPTY_WORD)
      expect(all(tree, COORDINATOR_GRAPH_CLASS)).toHaveLength(0)
      expect(all(tree, COORDINATOR_LIST_CLASS)).toHaveLength(0)
      // The health line stays: a quiet project is still a project whose sweep
      // is on or off.
      expect(all(tree, COORDINATOR_HEALTH_CLASS)).toHaveLength(1)
    })

    it('draws the map as a list, with its connections in words, when the worker did not place it', () => {
      const tree = renders(pictureOf(MAP, { positions: null }))
      expect(textOf(tree, COORDINATOR_NO_CANVAS_CLASS)).toBe(COORDINATOR_NO_CANVAS)
      const rows = all(tree, COORDINATOR_LIST_CLASS)
      expect(rows).toHaveLength(1)
      expect(all(tree, COORDINATOR_ROW_CLASS)).toHaveLength(2)
      expect(said(tree)).toContain(`${COORDINATOR_TASKS_COUNT_WORD} 2`)
      // One line per connection each node is an end of: both nodes are ends of
      // both edges.
      const lines = all(tree, COORDINATOR_ROW_EDGE_CLASS)
      expect(lines).toHaveLength(4)
      expect(lines.map((line) => line.getAttribute('data-kind'))).toEqual([
        'dependency',
        'overlap',
        'dependency',
        'overlap',
      ])
      expect(drawn(tree)).toContain(COORDINATOR_DEPENDS_WORD)
      expect(drawn(tree)).toContain(COORDINATOR_OVERLAP_WORD)
    })

    it('draws the CLI’s own sentence for a refused map read, and the figures it already has', () => {
      const tree = renders(pictureOf(MAP, { notice: 'the CLI refused the read: no coordinator is deployed' }))
      expect(textOf(tree, COORDINATOR_EMPTY_CLASS)).toBe('the CLI refused the read: no coordinator is deployed')
      // A refusal of the read is not a refusal of the drawing: the map on
      // screen stays under it.
      expect(all(tree, COORDINATOR_GRAPH_CLASS)).toHaveLength(1)
    })

    it('puts every document string through the display door', () => {
      const tree = renders(
        pictureOf(
          withMap({
            nodes: [
              { ...MAP.nodes[0], driver: 'a" onload="x' },
              // A name the `label` class refuses -- it is not a single line --
              // is drawn as `n/a` with the console's own reason, and the value
              // is nowhere.
              { ...MAP.nodes[1], name: 'bad\nname' },
            ],
            health: { ...MAP.health, note: 'a note\tsomebody wrote' },
          }),
        ),
      )
      const driver = all(tree, COORDINATOR_NODE_DRIVER_CLASS).find(
        (each) => each.textContent === 'a" onload="x',
      )
      expect(driver, 'the driver is drawn as text').toBeDefined()
      const refused = all(tree, COORDINATOR_NODE_NAME_CLASS).filter((each) => each.textContent === UNAVAILABLE_MARK)
      expect(refused).toHaveLength(1)
      expect(refused[0]?.getAttribute('title')).toContain('nodes[].name')
      // Nothing a document wrote reaches an attribute: `FakeScreen` throws on a
      // handler and on the navigating attribute, and no other attribute carries
      // the value either.
      for (const node of walk(tree)) {
        for (const [name, value] of node.attributes) {
          if (name === 'title') continue
          expect(value).not.toContain('onload')
          expect(value).not.toContain('bad')
        }
      }
      expect(one(tree, COORDINATOR_GRAPH_CLASS).getAttribute('aria-label')).toBe(COORDINATOR_CANVAS_LABEL)
    })

    it('draws the drill-down only when a node is open, and never a v2 section', () => {
      const tree = renders(pictureOf(MAP))
      expect(all(tree, COORDINATOR_TASKS_CLASS)).toHaveLength(0)
      const hooks = walk(tree).flatMap((node) => node.classes)
      for (const gone of [
        'coordinator-queue',
        'coordinator-totals',
        'coordinator-jobs',
        'coordinator-job-detail',
        'coordinator-transcript',
        'coordinator-description',
        'coordinator-detail',
        'coordinator-window-slot',
        'coordinator-body',
      ]) {
        expect(hooks, `the v2 hook ${gone} is drawn again`).not.toContain(gone)
      }
    })

    it('names the release the tab needs instead of drawing a map it cannot read', () => {
      const tree = renderCoordinatorFloor(coordinatorFactory, 'light', '0.9.0')
      expect((tree as FakeScreen).classes).toContain('coordinator')
      expect(tree.getAttribute('data-theme')).toBe('light')
      expect(textOf(tree, COORDINATOR_TITLE_CLASS)).toBe(COORDINATOR_TITLE)
      const note = one(tree, COORDINATOR_FLOOR_CLASS)
      expect(drawn(note)).toContain(COORDINATOR_FLOOR_WORD)
      expect(drawn(note)).toContain(COORDINATOR_FLOOR_AND_WORD)
      expect(drawn(note)).toContain('0.9.0')
      expect(all(tree, COORDINATOR_GRAPH_CLASS)).toHaveLength(0)
      expect(all(tree, COORDINATOR_EMPTY_CLASS)).toHaveLength(0)
    })

    it('draws in both themes', () => {
      for (const theme of COORDINATOR_THEMES) {
        expect(renders(pictureOf(MAP), theme).getAttribute('data-theme')).toBe(theme)
      }
    })

    it('draws a map the worker failed to place with the same figures as the canvas', () => {
      const placed = drawn(renders(pictureOf(MAP)))
      const listed = drawn(renders(pictureOf(MAP, { positions: null })))
      for (const figure of ['2', '1', '3', '1m 30s', COORDINATOR_TASKS_COUNT_WORD, COORDINATOR_JOBS_COUNT_WORD]) {
        expect(placed).toContain(figure)
        expect(listed).toContain(figure)
      }
      expect(said(renders(pictureOf(MAP)))).toContain(`${COORDINATOR_JOBS_COUNT_WORD} 3`)
    })
  })

  // -------------------------------------------------------------------------
  // The drill-down.
  // -------------------------------------------------------------------------

  describe('the drill-down', () => {
    it('opens a panel for the node that was pressed, with a mark while its read is out', () => {
      const tree = renders(pictureOf(MAP, { selected: 'repo-a~auth', tasks: { kind: 'reading' } }))
      const panel = one(tree, COORDINATOR_TASKS_CLASS)
      expect(panel.getAttribute(COORDINATOR_ATTRIBUTE)).toBe('repo-a~auth')
      expect(textOf(panel, COORDINATOR_TASKS_WORD_CLASS)).toBe(COORDINATOR_TASKS_WORD)
      expect(textOf(panel, COORDINATOR_TASKS_EMPTY_CLASS)).toBe(COORDINATOR_TASKS_BUSY)
      expect(textOf(panel, COORDINATOR_TASKS_CLOSE_CLASS)).toBe(COORDINATOR_TASKS_CLOSE_WORD)
      // The panel's own slot is where its mark and its rows go, and the mark
      // does not take the canvas away.
      expect(all(panel, BUSY_SLOT_CLASS)).toHaveLength(1)
      expect(all(tree, COORDINATOR_GRAPH_CLASS)).toHaveLength(1)
      // The pressed box says it is the open one.
      const open = all(tree, COORDINATOR_NODE_CLASS).find(
        (box) => box.getAttribute(COORDINATOR_ATTRIBUTE) === 'repo-a~auth',
      )
      expect(open?.getAttribute('aria-pressed')).toBe('true')
    })

    it('draws the orchestrator’s own tasks, and names the one they belong to', () => {
      const tree = renders(
        pictureOf(MAP, {
          selected: 'repo-a~auth',
          tasks: { kind: 'drawn', tasks: tasksOf(itemsDecoded(ITEMS)) },
        }),
      )
      const rows = all(tree, COORDINATOR_TASK_CLASS)
      expect(rows).toHaveLength(1)
      expect(textOf(rows[0] as ScreenElement, COORDINATOR_TASK_TITLE_CLASS)).toBe('a task being worked')
      expect(textOf(rows[0] as ScreenElement, COORDINATOR_TASK_STATE_CLASS)).toBe('running')
      expect(textOf(rows[0] as ScreenElement, COORDINATOR_TASK_REPO_CLASS)).toBe('owner/repo-a')
      expect(rows[0]?.getAttribute('data-key')).toBe('plan-1/task-1')
      expect(drawn(tree)).toContain('auth')
    })

    it('says an orchestrator holds no live task, and draws the CLI’s sentence for a refusal', () => {
      const empty = renders(pictureOf(MAP, { selected: 'repo-a', tasks: { kind: 'drawn', tasks: [] } }))
      expect(textOf(empty, COORDINATOR_TASKS_EMPTY_CLASS)).toBe(COORDINATOR_TASKS_EMPTY_WORD)
      const refused = renders(
        pictureOf(MAP, {
          selected: 'repo-a',
          tasks: { kind: 'refused', sentence: 'fugaro graph read: the scope is not admitted' },
        }),
      )
      expect(textOf(refused, COORDINATOR_TASKS_EMPTY_CLASS)).toBe('fugaro graph read: the scope is not admitted')
      expect(all(refused, COORDINATOR_TASK_CLASS)).toHaveLength(0)
    })

    it('draws no panel for a selection the map does not carry', () => {
      const tree = renders(pictureOf(MAP, { selected: 'repo-a~gone', tasks: { kind: 'reading' } }))
      expect(all(tree, COORDINATOR_TASKS_CLASS)).toHaveLength(0)
    })
  })

  // -------------------------------------------------------------------------
  // The screen.
  // -------------------------------------------------------------------------

  describe('the screen', () => {
    it('lays the map out when it loads one, and keeps the selection across it', async () => {
      const { screen, worker } = screenOver()
      expect(screen.current.selected).toBeNull()
      screen.change('repo-a~auth')
      const tree = await screen.load(mapDecoded(MAP))
      expect(worker.layouts).toBe(1)
      expect(tree).not.toBeNull()
      expect(all(tree as ScreenElement, COORDINATOR_NODE_CLASS)).toHaveLength(2)
      expect(screen.current.selected).toBe('repo-a~auth')
      screen.dispose()
    })

    it('drops a selection the new document does not carry', async () => {
      const { screen } = screenOver()
      await screen.load(mapDecoded(MAP))
      screen.change('repo-a~auth')
      const tree = await screen.load(mapDecoded(withMap({ nodes: [MAP.nodes[0]], edges: [] })))
      expect(screen.current.selected).toBeNull()
      expect(all(tree as ScreenElement, COORDINATOR_TASKS_CLASS)).toHaveLength(0)
      screen.dispose()
    })

    it('answers nothing for a load a later ask superseded', async () => {
      const { screen } = screenOver()
      const first = screen.load(mapDecoded(MAP))
      const second = screen.load(mapDecoded(withMap({ project: 'repo-b' })))
      expect(await first).toBeNull()
      expect(await second).not.toBeNull()
      screen.dispose()
    })

    it('draws the list when the worker refuses the layout', async () => {
      const screen = new CoordinatorScreen({
        factory: coordinatorFactory,
        client: new CoordinatorLayoutClient(() => new FakeWorker(true)),
        theme: 'light',
      })
      const tree = await screen.load(mapDecoded(MAP))
      expect(tree).not.toBeNull()
      expect(textOf(tree as ScreenElement, COORDINATOR_NO_CANVAS_CLASS)).toBe(COORDINATOR_NO_CANVAS)
      expect(all(tree as ScreenElement, COORDINATOR_LIST_CLASS)).toHaveLength(1)
      screen.dispose()
    })

    it('forgets the drawing when the console moves to another installation', async () => {
      const { screen } = screenOver()
      await screen.load(mapDecoded(MAP))
      screen.change('repo-a')
      screen.forget()
      const tree = screen.frame()
      expect(screen.current.selected).toBeNull()
      expect(all(tree, COORDINATOR_HEALTH_CLASS)).toHaveLength(0)
      expect(all(tree, COORDINATOR_GRAPH_CLASS)).toHaveLength(0)
      expect(all(tree, COORDINATOR_TASKS_CLASS)).toHaveLength(0)
      screen.dispose()
    })

    it('answers nothing after it is disposed, and disposes its worker', async () => {
      const { screen } = screenOver()
      await screen.load(mapDecoded(MAP))
      screen.dispose()
      expect(screen.change('repo-a')).toBeNull()
      expect(screen.tasksReading()).toBeNull()
      expect(screen.tasksDrawn([])).toBeNull()
      expect(screen.tasksRefused('no')).toBeNull()
      expect(screen.noticed('no')).toBeNull()
      await expect(screen.load(mapDecoded(MAP))).resolves.toBeNull()
    })

    it('puts the drill-down’s own mark up in the frame the press happened in', async () => {
      const { screen } = screenOver()
      await screen.load(mapDecoded(MAP))
      const opened = screen.change('repo-a')
      expect(textOf(opened as ScreenElement, COORDINATOR_TASKS_EMPTY_CLASS)).toBe(COORDINATOR_TASKS_BUSY)
      const drawn = screen.tasksDrawn(tasksOf(itemsDecoded(ITEMS)))
      expect(all(drawn as ScreenElement, COORDINATOR_TASK_CLASS)).toHaveLength(1)
      const closed = screen.change(null)
      expect(all(closed as ScreenElement, COORDINATOR_TASKS_CLASS)).toHaveLength(0)
      screen.dispose()
    })

    it('draws the section’s own busy sentence over the figures it already has', () => {
      const screen = new CoordinatorScreen({
        factory: coordinatorFactory,
        client: new CoordinatorLayoutClient(() => new FakeWorker()),
        theme: 'light',
      })
      expect(COORDINATOR_BUSY).toContain('orchestrators')
      expect(all(screen.frame(), BUSY_SLOT_CLASS)).toHaveLength(1)
      screen.dispose()
    })
  })
})
