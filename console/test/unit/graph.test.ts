// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The dependency graph screen (console GV2.3b; the owner's dependency graph
// spec §3 and its addendum §3, docs/console-dependency-graph/).
//
// The model and the layout cache are GV2.3a's and are pinned next door in
// `graph-model.test.ts`. This file is the **screen**: what it draws, what it
// refuses to draw, and -- the case the task is proven against -- **when it asks
// the engine to lay anything out**.
//
// The first case is the one to mutation-prove. A watch frame arrives once a
// second while the graph is up, and a frame changes a node's *stage* far more
// often than it changes the shape of the graph: if every frame laid the picture
// out again, a screen that looks still would keep a worker busy forever. So the
// screen owns the cache and `GraphScreen.build` is the only thing that asks it,
// and the mutation this case is proven against is exactly the defect it exists
// for --
//
//   in `GraphScreen.build`, replace
//       positions = await this.layout.positions(model)
//   with
//       positions = await this.deps.client.layout(graphOf(model.columns[0]))
//
// -- which bypasses the cache and asks the worker once per frame. The case then
// fails on its second assertion (`placements` is one row per stream per frame,
// not the one the first read asked for). Red before, green after; the PR carries
// both runs.
//
// Everything runs against `test/support/fakeDom.ts`: no browser, no Electron.
// Most of it is drawn over a fake `WorkerLike` that answers **every** layout as
// it is posted, so `placements` counts the asks and nothing else; the cases
// about the picture's geometry are drawn over the **real** engine (`ElkWorker`).

import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { measured } from '../../src/shared/availability'
import { PLAN_STATUS_MIN_INTERVAL_MS } from '../../src/source/cli'
import type { PlanStatusView, RunView } from '../../src/shared/schema'
import { GRAPH_ROW_GAP, nodeKey } from '../../src/renderer/graph/layout'
import type { WorkerLike } from '../../src/renderer/graph/layout-client'
import { GRAPH_MAX_TITLE_CODE_POINTS, STATE_WORDS, buildGraphModel } from '../../src/renderer/graph/model'
import { WRITE_UNAVAILABLE } from '../../src/renderer/writes'
import { LIVE_PR_NO_LINK_REASON } from '../../src/renderer/screens/live'
import { PR_NO_LINK_REASON } from '../../src/renderer/screens/run'
import {
  GRAPH_BAND_HEAD,
  GRAPH_COMPONENT_OPTION,
  GRAPH_FILTER_LABEL,
  GRAPH_INITIAL_STATE,
  GRAPH_INSPECTOR_NONE,
  GRAPH_LAYER_GAP,
  GRAPH_LAYER_OPTION,
  GRAPH_CRITICAL_PATH,
  GRAPH_DEPENDS_ON,
  GRAPH_NO_CANVAS,
  GRAPH_NO_PLANS,
  GRAPH_NO_RUN,
  GRAPH_NODE_OPTION,
  GRAPH_NODE_TITLE_LINES,
  GRAPH_NODE_TITLE_LINE_CODE_POINTS,
  GRAPH_NOT_SHOWN_SINGULAR,
  GRAPH_OPEN_RUN_LABEL,
  GRAPH_PAD,
  GRAPH_PAUSE_LABEL,
  GRAPH_PR_NO_LINK_REASON,
  GRAPH_REFRESH_MS,
  GRAPH_RELEASE_HEAD,
  GRAPH_RUN_AMBIGUOUS,
  GRAPH_ZOOM_MAX,
  GRAPH_ZOOM_MIN,
  GraphLayoutClient,
  GraphScreen,
  doorFor,
  releaseKey,
  renderGraph,
  runForSuffix,
  runIdsIn,
  tabKey,
  tabOf,
  viewBoxOf,
  withFeedStages,
} from '../../src/renderer/screens/graph'
import type { GraphState, GraphTheme, ScreenElement } from '../../src/renderer/screens/graph'
import { FakeDocument, everyNode, textOf } from '../support/fakeDom'
import type { FakeNode } from '../support/fakeDom'
import { FakeWorker, NodeSurface, Surface, decoded } from '../support/graphHarness'

// ---------------------------------------------------------------------------
// The documents
// ---------------------------------------------------------------------------

/** RAW is `test/fixtures/plan-status.json` as the wire has it, and DOCUMENT is
 * the same document through the real decoder: the screen is a function of the
 * decoded view, so a test that hand-built one would not be testing what the
 * screen is handed. */
const RAW: unknown = JSON.parse(readFileSync(join('test', 'fixtures', 'plan-status.json'), 'utf8'))
const DOCUMENT: PlanStatusView = decoded(RAW)

/** PLAN and SECOND are the fixture's two plans, and FINISHED is the fixture with
 * its first release shipped -- the only way a chip exists, since the model
 * builds them from `finished`. */
const PLAN = DOCUMENT.plans[0]?.id ?? ''
const SECOND = DOCUMENT.plans[1]?.id ?? ''
const FINISHED = patched(['plans', 0, 'releases', 0, 'finished'], true)
const CHIP = releaseKey(PLAN, '1.4.0')

/**
 * RUNS is the console's own run list: full run ids ending with the suffixes the
 * fixture's tasks were joined to. They are the shape the CLI mints
 * (`date-time-tail`) and belong to no run of anybody's.
 */
const RUNS: readonly string[] = Object.freeze([
  '20261025-121805-aa01',
  '20261025-121805-aa02',
  '20261025-121805-aa03',
  '20261025-121805-aa04',
  '20261025-121805-aa05',
])

/** patched is the fixture with one path replaced, as the wire has it: a patched
 * document is a document and goes through the decoder like any other. */
function patched(path: readonly (string | number)[], value: unknown): PlanStatusView {
  const copy = JSON.parse(JSON.stringify(RAW)) as Record<string, unknown>
  let at: unknown = copy
  for (const step of path.slice(0, -1)) {
    if (at === null || typeof at !== 'object') throw new Error(`no ${String(step)} in the path`)
    at = (at as Record<string, unknown>)[step]
  }
  const last = path[path.length - 1]
  if (last === undefined || at === null || typeof at !== 'object') throw new Error('path is empty')
  ;(at as Record<string, unknown>)[last] = value
  return decoded(copy)
}

/** twoRunning is the fixture with a second task running, and a run of its own:
 * the order of a frame's rows is only a claim about a frame that says two things
 * at once, and this fixture has one running task. `task-three` is the task
 * nothing depends on, so making it running moves no edge and no critical path,
 * and `aa07` is a suffix no other task of the fixture carries. */
function twoRunning(): PlanStatusView {
  const copy = JSON.parse(JSON.stringify(RAW)) as { plans: { nodes: Record<string, unknown>[] }[] }
  const node = copy.plans[0]?.nodes[2]
  if (node === undefined) throw new Error('the fixture has no third task')
  node['state'] = 'running'
  node['run'] = 'aa07'
  return decoded(copy)
}

/** isolatedAbove is the fixture with its first task cut loose: no release of its
 * own, and the one task that waited on it waiting on nothing. So `task-one` is
 * an isolated task -- the first node of the model's own order, and therefore the
 * component the engine places at the top -- and the release box below it is the
 * box whose heading the isolated task's own card would cover. `task-three` joins
 * that release so the box spans the isolated task's own column: a box drawn
 * beside it would leave the two rectangles clear of each other in `x` and the
 * case would read as room kept whatever the engine was asked for. */
function isolatedAbove(): PlanStatusView {
  const copy = JSON.parse(JSON.stringify(RAW)) as { plans: { nodes: Record<string, unknown>[] }[] }
  const nodes = copy.plans[0]?.nodes ?? []
  const first = nodes.find((node) => node['id'] === 'task-one')
  const second = nodes.find((node) => node['id'] === 'task-two')
  const third = nodes.find((node) => node['id'] === 'task-three')
  if (first === undefined || second === undefined || third === undefined) {
    throw new Error('the fixture has no first three tasks')
  }
  first['release'] = null
  second['needs'] = []
  third['release'] = '1.4.0'
  return decoded(copy)
}

/**
 * THREE_STREAMS is a document of **three streams with two plans each**, whose
 * plans are chains of different lengths. The lengths are the case's own: the
 * engine packs a graph's components in **two dimensions**, so three streams of
 * one shape stack as neatly however they are asked for as they do when each is
 * laid out alone, and only plans whose depths differ show the interleaving this
 * document exists to produce.
 */
const THREE_STREAMS: Readonly<Record<string, readonly number[]>> = Object.freeze({
  alpha: [1, 7],
  beta: [6, 1],
  gamma: [3, 3],
})

/** threeStreams is THREE_STREAMS as a plan document, through the real decoder.
 * Every node is `ready` and belongs to no release, so nothing on this picture is
 * hidden by a shut chip: the rows this case reads are the rows of the cards that
 * were placed. */
function threeStreams(): PlanStatusView {
  const plans: unknown[] = []
  let tasks = 0
  let at = 0
  for (const [stream, lengths] of Object.entries(THREE_STREAMS)) {
    at += 1
    const nodes: unknown[] = []
    for (const [plan, length] of lengths.entries()) {
      for (let node = 0; node < length; node += 1) {
        nodes.push({
          id: `${stream}-${String(plan)}-${String(node)}`,
          title: `A task of the ${stream} plans`,
          state: 'ready',
          closed: false,
          level: node,
          critical: false,
          needs: node === 0 ? [] : [`${stream}-${String(plan)}-${String(node - 1)}`],
        })
      }
    }
    tasks += nodes.length
    plans.push({
      id: `plans/2026-10-0${String(at)}-${stream}`,
      artifact: `docs/plans/2026-10-0${String(at)}-${stream}.graph.json`,
      plan: `The ${stream} plans`,
      stream,
      nodes,
      releases: [],
      tasks: nodes.length,
      closed: 0,
      open: nodes.length,
    })
  }
  return decoded({
    repo: 'example-owner/example-repo',
    plans,
    totals: { plans: plans.length, tasks, closed: 0, open: tasks },
    streams: [],
    warnings: [],
  })
}

/** runRow is one watch row. Only `run` and `stage` are read by anything on this
 * screen -- the join takes the suffix, the words take the stage -- so every
 * other field is the empty reading: the smallest row the type allows. */
function runRow(run: string, stage: string): RunView {
  return {
    run,
    slug: '',
    repo: '',
    title: '',
    stage,
    round: '',
    verify: '',
    models: '',
    recipe: '',
    auth: '',
    notional: false,
    spentUsd: measured(0),
    ageSeconds: measured(0),
    health: '',
    deadline: '',
    halted: '',
    queued: false,
    stuck: false,
    workflow: '',
    requestedBy: '',
    launchedBy: '',
    prNumber: null,
  }
}

/** screenWith is a screen over a fresh fake worker and document, with the worker
 * handed back so a case can count the asks. It is built with the screen's own
 * layout client (`GraphLayoutClient`), which is what the app hands it: a case
 * that read the asks off a plainer client would read a request the console never
 * makes. */
function screenWith(theme: GraphTheme = 'light'): { screen: GraphScreen; worker: FakeWorker } {
  const document = new FakeDocument()
  const worker = new FakeWorker()
  const screen = new GraphScreen({
    factory: new Surface(document),
    client: new GraphLayoutClient(() => worker),
    theme,
  })
  return { screen, worker }
}

/**
 * ElkWorker answers the layout protocol with the **real engine**: the pinned
 * `elkjs` devDependency, whose node entry builds its non-worker path out of
 * `elk-worker.min.js` -- the file the console copies into its served root and
 * `bundle.test.ts` pins byte for byte -- so a picture drawn over this worker is
 * laid out by the engine that ships, and not by a fake that agrees with the
 * screen about where a row goes. **That agreement is the one thing the band
 * cases may not assume**: whether two streams' plans end up in one another's
 * row is a fact about the engine's own packing.
 *
 * The reply is the worker's own (`{ id, data }`), because what is under test is
 * the screen over the shipped engine rather than a second protocol.
 */
interface ElkEngine {
  layout(
    graph: Record<string, unknown>,
    args: { layoutOptions: Readonly<Record<string, string>> },
  ): Promise<Record<string, unknown>>
  terminateWorker(): void
}

/**
 * The engine is required rather than imported: `elkjs`'s declaration does not
 * compile under this repository's `skipLibCheck: false` (`elk-api.d.ts` indexes
 * the optional `children` of its graph type, which `strict` refuses), and the
 * two methods above are the whole of its surface this worker uses. Requiring
 * also keeps what is loaded the engine the console bundles: `elkjs`'s node
 * entry is CommonJS, and its own `require('./elk-worker.min.js')` is what it
 * lays a graph out with.
 */
const Elk = createRequire(import.meta.url)('elkjs') as { new (): ElkEngine }

class ElkWorker implements WorkerLike {
  readonly sent: Record<string, unknown>[] = []
  private readonly listeners = new Map<string, ((event: { data?: unknown }) => void)[]>()
  private readonly elk = new Elk()

  /** placements counts the layouts this worker was asked to run, as the fake
   * counts them: one per `layout` command posted. */
  get placements(): number {
    return this.sent.filter((each) => each['cmd'] === 'layout').length
  }

  postMessage(message: unknown): void {
    const request = message as Record<string, unknown>
    this.sent.push(request)
    if (request['cmd'] !== 'layout') return
    const layoutOptions = (request['layoutOptions'] ?? {}) as Readonly<Record<string, string>>
    void this.elk.layout(request['graph'] as Record<string, unknown>, { layoutOptions }).then(
      (data) => this.answer({ id: request['id'], data }),
      (error: unknown) => this.answer({ id: request['id'], error: String(error) }),
    )
  }

  addEventListener(type: string, listener: (event: { data?: unknown }) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  terminate(): void {
    this.elk.terminateWorker()
  }

  private answer(reply: unknown): void {
    for (const listener of this.listeners.get('message') ?? []) listener({ data: reply })
  }
}

/** elkScreen is a screen over the real engine -- the app's own client and
 * request -- so a case reads the picture the engine that ships draws. */
function elkScreen(): GraphScreen {
  return new GraphScreen({
    factory: new Surface(new FakeDocument()),
    client: new GraphLayoutClient(() => new ElkWorker()),
    theme: 'light',
  })
}

/** rectOf is one SVG element's rectangle, as the drawing writes it. */
function rectOf(node: FakeNode): { x: number; y: number; width: number; height: number } {
  return {
    x: Number(node.getAttribute('x')),
    y: Number(node.getAttribute('y')),
    width: Number(node.getAttribute('width')),
    height: Number(node.getAttribute('height')),
  }
}

/** releaseBoxesIn is every release box in a drawing: the release it names, and
 * the band its heading is drawn in -- the box's own top, for the height of a
 * heading (`y` to `y + GRAPH_RELEASE_HEAD`). */
function releaseBoxesIn(root: FakeNode): readonly { readonly key: string; readonly band: ReturnType<typeof rectOf> }[] {
  return everyNode(root)
    .filter((each) => each.getAttribute('class') === 'graph-release')
    .map((each) => {
      const outline = each.children.find((child) => child.getAttribute('class') === 'graph-release-box')
      if (outline === undefined) throw new Error('a release box was drawn with no outline')
      const rect = rectOf(outline)
      return { key: each.getAttribute('data-release') ?? '', band: { ...rect, height: GRAPH_RELEASE_HEAD } }
    })
}

/** cardsIn is every task card in a drawing, at the coordinates its group's
 * transform places it. */
function cardsIn(root: FakeNode): readonly { readonly key: string; readonly rect: ReturnType<typeof rectOf> }[] {
  return everyNode(root)
    .filter((each) => each.getAttribute('class') === 'graph-node')
    .map((each) => {
      const at = /^translate\((-?[\d.]+) (-?[\d.]+)\)$/.exec(each.getAttribute('transform') ?? '')
      const box = each.children.find((child) => child.getAttribute('class') === 'graph-node-box')
      if (at === null || box === undefined) throw new Error('a task card was drawn with no place')
      const rect = rectOf(box)
      return {
        key: each.getAttribute('data-key') ?? '',
        rect: { ...rect, x: rect.x + Number(at[1]), y: rect.y + Number(at[2]) },
      }
    })
}

/** overlaps is whether two rectangles share any area: edges that touch are not
 * an overlap, which is the room two stacked release boxes ask for exactly. */
function overlaps(
  one: { readonly x: number; readonly y: number; readonly width: number; readonly height: number },
  other: { readonly x: number; readonly y: number; readonly width: number; readonly height: number },
): boolean {
  return (
    one.x < other.x + other.width &&
    other.x < one.x + one.width &&
    one.y < other.y + other.height &&
    other.y < one.y + one.height
  )
}

/** mounted is a drawn element in a document of its own, as the tree a case reads
 * attributes off. */
function mounted(element: ScreenElement): FakeNode {
  const document = new FakeDocument()
  const node = (element as NodeSurface).node
  document.body.appendChild(node)
  return node
}

/** keys is every `data-key` drawn under a node, in document order. */
function keys(node: FakeNode): readonly string[] {
  return everyNode(node)
    .map((each) => each.getAttribute('data-key'))
    .filter((each): each is string => each !== null)
}

/** drawnWords is every `data-word` under a node, with the text beside it. */
function drawnWords(node: FakeNode): readonly (readonly [string, string])[] {
  return everyNode(node)
    .filter((each) => each.getAttribute('data-word') !== null)
    .map((each) => [each.getAttribute('data-word') ?? '', each.textContent ?? ''] as const)
}

/** wordSet is the words a drawing carries, as a set. */
function wordSet(node: FakeNode): ReadonlySet<string> {
  return new Set(drawnWords(node).map(([word]) => word))
}

/** rendered is a model drawn straight through the screen's renderer, for the
 * cases that are about the drawing rather than about the read. */
function rendered(model: ReturnType<typeof buildGraphModel>, state: GraphState = GRAPH_INITIAL_STATE, theme: GraphTheme = 'light'): FakeNode {
  const document = new FakeDocument()
  return mounted(renderGraph(new Surface(document), { model, positions: null, state, runs: RUNS }, theme))
}

/** modelOf is the model over a document, as the screen's own read builds it. */
function modelOf(document: PlanStatusView = DOCUMENT): ReturnType<typeof buildGraphModel> {
  return buildGraphModel({ document, tab: null })
}

// ---------------------------------------------------------------------------
// The one to mutation-prove: a frame that changes no structure lays nothing out
// ---------------------------------------------------------------------------

describe('the graph screen and the layout cache', () => {
  it('a watch frame that changes a stage does not ask the engine to lay the graph out again', async () => {
    const { screen, worker } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    // One run per stream of the picture, and this document's picture is two: a
    // stream is laid out on its own, and the rows are stacked from the runs.
    expect(worker.placements).toBe(2)

    // The frame the Live screen already reads, with one node's stage moved from
    // `implement` to `fix`. The graph is the same graph -- same plans, same
    // tasks, same edges -- so no coordinate changed and the worker must not be
    // asked again.
    const after = await screen.frame([runRow('20261025-121805-aa02', 'fix')])
    expect(worker.placements).toBe(2)

    // And the frame was not dropped: it was drawn from the positions in hand.
    expect(after).not.toBeNull()
    const node = screen.lastModel?.columns[0]?.plans[0]?.nodes.find((each) => each.id === 'task-two')
    expect(node?.words).toEqual(['fixing', 'critical'])
  })

  it('a read that adds a task does ask again', async () => {
    const { screen, worker } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    // A raw node, with the wire's own key names: the decoder refuses an unknown
    // field, so a node this test adds is a node the CLI could have written.
    const raw = RAW as { readonly plans: readonly { readonly nodes: readonly unknown[] }[] }
    const bigger = patched(['plans', 0, 'nodes'], [
      ...(raw.plans[0]?.nodes ?? []),
      { id: 'task-seven', title: 'A task the first read did not have', state: 'ready', closed: false, level: 3, critical: false },
    ])
    await screen.load(bigger, RUNS)
    // Three and not four: the task was added to one stream's plan, so that
    // stream is laid out again and the stream beside it keeps its own positions.
    expect(worker.placements).toBe(3)
  })

  it('a selection, a chip, a zoom and the drawing are the screen own and lay nothing out', async () => {
    const { screen, worker } = screenWith()
    await screen.load(FINISHED, RUNS)
    screen.change({ selected: nodeKey(PLAN, 'task-two') })
    screen.change({ view: 'list' })
    screen.change({ zoom: 2 })
    screen.change({ expanded: [CHIP] })
    expect(worker.placements).toBe(2)
  })

  it('narrowing to a stream draws that stream alone, over the run it already had', async () => {
    const { screen, worker } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    expect(worker.placements).toBe(2)
    await screen.narrow('example')
    // Every stream of the picture has been laid out, and the one still drawn did
    // not change: the key is a stream's, so narrowing -- a different *picture* --
    // costs no run at all.
    expect(worker.placements).toBe(2)
    // And the picture really is narrower: one plan, not two.
    expect(screen.lastModel?.columns.map((column) => column.stream)).toEqual(['example'])
  })

  it('a read superseded while it was in flight draws nothing at all', async () => {
    const { screen } = screenWith()
    const first = screen.load(DOCUMENT, RUNS)
    const second = screen.load(patched(['plans', 0, 'stream'], 'other'), RUNS)
    const answers = await Promise.all([first, second])
    // The first ask was superseded: it answers null rather than putting an older
    // picture over a newer one.
    expect(answers[0]).toBeNull()
    expect(answers[1]).not.toBeNull()
  })
})

// ---------------------------------------------------------------------------
// The release boxes, and the room their headings need
// ---------------------------------------------------------------------------

describe('the graph screen and the room a release heading needs', () => {
  it('asks the engine for the room a heading and a band label need, in a layer, between two layers and between two components', async () => {
    const { screen, worker } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const asked = worker.sent.filter((each) => each['cmd'] === 'layout')
    // One ask per stream, and the room is asked for in all of them: a stream is
    // laid out on its own and no run knows which of the streams it is.
    expect(asked.length).toBe(2)
    // The room is the lower box's heading, the stream band's own label above it,
    // and the padding both boxes keep: less than that and the heading or the
    // label is drawn on the row above, whose cards are drawn later and paint
    // over it. **The flow is left to right, so the room a heading needs is the
    // vertical one** -- `elk.spacing.nodeNode`, the space between two cards of
    // one layer -- and the layers themselves and the components are asked for
    // the same room because neither knows which of the three it is under.
    expect(GRAPH_LAYER_GAP).toBe(GRAPH_PAD * 2 + GRAPH_BAND_HEAD)
    for (const each of asked) {
      const options = (each['layoutOptions'] ?? {}) as Readonly<Record<string, string>>
      expect(options[GRAPH_LAYER_OPTION]).toBe(String(GRAPH_LAYER_GAP))
      expect(options[GRAPH_NODE_OPTION]).toBe(String(GRAPH_LAYER_GAP))
      expect(options[GRAPH_COMPONENT_OPTION]).toBe(String(GRAPH_LAYER_GAP))
    }
  })

  it('draws every release heading clear of every task card, its own box’s and another’s', async () => {
    // The fixture's own document, with no field renamed: a heading's band is the
    // box's whole width and a fixed height whatever the heading says, so the
    // release's name is not an input this case can vary.
    const screen = elkScreen()
    await screen.load(DOCUMENT, RUNS)
    const drawing = screen.redraw()
    if (drawing === null) throw new Error('no drawing')
    const root = mounted(drawing)

    const boxes = releaseBoxesIn(root)
    const cards = cardsIn(root)
    // Both halves are about something: the fixture draws more than one release
    // box, and there are more cards than boxes, so a box that had no card of its
    // own to collide with could not make this pass on its own.
    expect(boxes.length).toBeGreaterThan(1)
    expect(cards.length).toBeGreaterThan(boxes.length)

    expectNoHeadingUnderACard(boxes, cards)
  })

  it('keeps the room a heading needs under an isolated task, which the engine places as its own component', async () => {
    // An isolated task is a component of its own, and the engine stacks
    // components at `elk.spacing.componentComponent` -- 20 px by default, the
    // same room it leaves between two rows. A release below that task draws its
    // heading in the gap, and nothing in the drawing code knows the row above is
    // a different component: the box's own growth and the append order are the
    // same, so a heading covered this way looks exactly like the one the layer
    // spacing used to cover.
    const screen = elkScreen()
    await screen.load(isolatedAbove(), RUNS)
    const drawing = screen.redraw()
    if (drawing === null) throw new Error('no drawing')
    const root = mounted(drawing)

    const boxes = releaseBoxesIn(root)
    const cards = cardsIn(root)
    // The fixture is worth reading only if the isolated task really sits above a
    // release's first card: one component of its own, and a box below it.
    expect(cards.length).toBeGreaterThan(boxes.length)
    expect(boxes.length).toBeGreaterThan(0)

    expectNoHeadingUnderACard(boxes, cards)
  })
})

/** expectNoHeadingUnderACard is the one assertion both room cases make: no
 * release box's heading band shares an area with any task card's rectangle. */
function expectNoHeadingUnderACard(
  boxes: readonly { readonly key: string; readonly band: ReturnType<typeof rectOf> }[],
  cards: readonly { readonly key: string; readonly rect: ReturnType<typeof rectOf> }[],
): void {
  for (const box of boxes) {
    for (const card of cards) {
      expect(
        overlaps(box.band, card.rect),
        `${box.key} draws its heading over ${card.key}: the box grows upward for two lines of text, and the layout has to leave it that room`,
      ).toBe(false)
    }
  }
}

// ---------------------------------------------------------------------------
// The stream bands, and the card's own title
// ---------------------------------------------------------------------------

/** bandIn is one stream's band: the rectangle it draws, the stream it is drawn
 * for, and the label it carries. */
function bandsIn(root: FakeNode): readonly { readonly stream: string; readonly label: string; readonly rect: ReturnType<typeof rectOf> }[] {
  return everyNode(root)
    .filter((each) => each.getAttribute('class') === 'graph-band')
    .map((each) => {
      const box = each.children.find((child) => child.getAttribute('class') === 'graph-band-box')
      const label = each.children.find((child) => child.getAttribute('class') === 'graph-band-label')
      if (box === undefined) throw new Error('a band was drawn with no box')
      return { stream: each.getAttribute('data-stream') ?? '', label: label?.textContent ?? '', rect: rectOf(box) }
    })
}

/** titleLinesIn is one card's title as the lines the card drew: the text of
 * every line of the title's own `text`, in the order it drew them. */
function titleLinesIn(root: FakeNode, key: string): readonly string[] {
  const card = everyNode(root).find((each) => each.getAttribute('data-key') === key)
  const title = card?.children.find((child) => child.getAttribute('class') === 'graph-node-title')
  return (title?.children ?? []).map((line) => line.textContent ?? '')
}

/** viewBoxIn is the canvas's own frame as four numbers, which is what the
 * browser crops the picture to. */
function viewBoxIn(root: FakeNode): ReturnType<typeof rectOf> {
  const canvas = everyNode(root).find((each) => each.getAttribute('class') === 'graph-canvas')
  const parts = (canvas?.getAttribute('viewBox') ?? '').split(' ').map(Number)
  if (parts.length !== 4 || parts.some((each) => !Number.isFinite(each))) {
    throw new Error(`the canvas was drawn with no frame: ${JSON.stringify(canvas?.getAttribute('viewBox') ?? null)}`)
  }
  return { x: parts[0] ?? 0, y: parts[1] ?? 0, width: parts[2] ?? 0, height: parts[3] ?? 0 }
}

/** inside is whether a rectangle holds another whole: a card is on its stream's
 * band when every one of its four edges is within the band's. */
function inside(outer: ReturnType<typeof rectOf>, inner: ReturnType<typeof rectOf>): boolean {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.width <= outer.x + outer.width &&
    inner.y + inner.height <= outer.y + outer.height
  )
}

describe('the graph screen and its stream bands', () => {
  it('draws one band per stream, behind the tasks, carrying the stream’s own name', async () => {
    // **One horizontal band per stream** (the redesign's canvas): the model's
    // stream grouping is the picture's own rows, so a reader sees which stream a
    // task belongs to without reading a tab, and the plans that name no stream
    // get a band of their own under the console's own words.
    const screen = elkScreen()
    await screen.load(DOCUMENT, RUNS)
    const drawing = screen.redraw()
    if (drawing === null) throw new Error('no drawing')
    const root = mounted(drawing)
    const model = screen.lastModel
    if (model === null) throw new Error('no model')

    const bands = bandsIn(root)
    expect(bands.map((band) => band.stream)).toEqual(model.columns.map((column) => column.stream))
    expect(bands.map((band) => band.label)).toEqual(model.columns.map((column) => (column.label.ok ? column.label.text : '')))
    expect(bands.map((band) => band.label)).toEqual(['example', 'no stream'])

    // A band is drawn **behind** the tasks: every band comes before every card in
    // the canvas, so a card's own surface is never under a band's fill.
    const drawn = everyNode(root).filter((each) => each.getAttribute('class') === 'graph-band' || each.getAttribute('class') === 'graph-node')
    expect(drawn.filter((each) => each.getAttribute('class') === 'graph-band').length).toBe(bands.length)
    expect(drawn.findIndex((each) => each.getAttribute('class') === 'graph-node')).toBeGreaterThan(bands.length - 1)

    // Every task of a stream is inside that stream's band, and the bands do not
    // overlap each other: one band per stream is a row of its own, not a box
    // drawn around whatever happened to land near it.
    const streamOf = new Map<string, string>()
    for (const column of model.columns) {
      for (const plan of column.plans) for (const node of plan.nodes) streamOf.set(nodeKey(plan.id, node.id), column.stream)
    }
    for (const card of cardsIn(root)) {
      const band = bands.find((each) => each.stream === streamOf.get(card.key))
      if (band === undefined) throw new Error(`no band for ${card.key}`)
      expect(inside(band.rect, card.rect), `${card.key} is drawn outside its stream's band`).toBe(true)
    }
    for (const one of bands) {
      for (const other of bands) {
        if (one.stream === other.stream) continue
        expect(overlaps(one.rect, other.rect), `${one.stream} and ${other.stream} overlap`).toBe(false)
      }
    }
  })

  it('lays each stream out on its own and stacks the rows in model order', async () => {
    // **The owner's ruling of 2026-10-31, and the defect it answers.** One flat
    // request packs a picture's components in two dimensions, so a stream whose
    // plans are deeper than its neighbour's ends up with its own plans in the
    // neighbour's row, and the band around them -- the union of the cards the
    // stream has on the picture -- is then a rectangle laid over another
    // stream's cards. Three streams of two plans each, whose plans are chains of
    // different lengths, is the smallest document that shows it: over the real
    // engine and one flat request, all three bands cover one another and the
    // gutter this case reads is negative. Laid out one stream at a time and
    // stacked, the bands are rows: disjoint, in the model's own order, with the
    // room between them.
    const screen = elkScreen()
    await screen.load(threeStreams(), RUNS)
    const drawing = screen.redraw()
    if (drawing === null) throw new Error('no drawing')
    const root = mounted(drawing)
    const model = screen.lastModel
    if (model === null) throw new Error('no model')

    const bands = bandsIn(root)
    const order = model.columns.map((column) => column.stream)
    expect(order).toEqual(Object.keys(THREE_STREAMS))
    expect(bands.map((band) => band.stream), 'no band per stream, in the model’s order').toEqual(order)
    expect(bands.map((band) => band.label)).toEqual(order)

    // Every card is inside its own stream's band -- the band is that stream's
    // rectangle, so a card outside it is a card the band does not describe.
    const streamOf = new Map<string, string>()
    for (const column of model.columns) {
      for (const plan of column.plans) for (const node of plan.nodes) streamOf.set(nodeKey(plan.id, node.id), column.stream)
    }
    for (const card of cardsIn(root)) {
      const band = bands.find((each) => each.stream === streamOf.get(card.key))
      if (band === undefined) throw new Error(`no band for ${card.key}`)
      expect(inside(band.rect, card.rect), `${card.key} is drawn outside its stream's band`).toBe(true)
    }

    // The rows are stacked, in order, with a gutter between them: each band
    // starts at or below the one before it and entirely below it -- which is
    // what "disjoint" means for rectangles of one width, and the thing the flat
    // request cannot give.
    expect(bands.length).toBe(order.length)
    for (let at = 1; at < bands.length; at += 1) {
      const above = bands[at - 1]?.rect
      const below = bands[at]?.rect
      if (above === undefined || below === undefined) throw new Error('a band is missing')
      expect(overlaps(above, below), `${order[at - 1]} and ${order[at]} share ground`).toBe(false)
      // The gutter is the room this screen asks for between two rows less the
      // room a band draws above its own cards: the band above ends GRAPH_PAD
      // below its lowest card, and the one below starts GRAPH_PAD +
      // GRAPH_BAND_HEAD above its highest. A row gap of GRAPH_LAYER_GAP would
      // leave the two bands *touching*, which is why the rows are asked for
      // more than the engine's own layer room.
      expect(below.y - (above.y + above.height), `${order[at - 1]} and ${order[at]} are not apart`).toBe(
        GRAPH_ROW_GAP - (GRAPH_PAD * 2 + GRAPH_BAND_HEAD),
      )
    }
    expect(GRAPH_ROW_GAP, 'the rows are not asked for more room than a band needs').toBeGreaterThan(
      GRAPH_PAD * 2 + GRAPH_BAND_HEAD,
    )
  })

  it('frames the picture on every band and every card, the band’s label included', async () => {
    // The frame is the picture's own rectangle at the initial zoom, and it is
    // the *only* crop: what falls outside it is not drawn, so the band's head --
    // where the stream's own name is written -- has to be inside it. A band is
    // not a task's rectangle, and this is the case that says the frame was
    // computed from the bands as well as from the cards.
    const screen = elkScreen()
    await screen.load(DOCUMENT, RUNS)
    const drawing = screen.redraw()
    if (drawing === null) throw new Error('no drawing')
    const root = mounted(drawing)
    const frame = viewBoxIn(root)

    const bands = bandsIn(root)
    expect(bands.length, 'no band was drawn, so the frame below is vacuous').toBeGreaterThan(0)
    for (const one of bands) {
      expect(inside(frame, one.rect), `${one.stream}'s band is drawn outside the frame`).toBe(true)
    }
    // The label is written at the band's head, so its baseline has to be below
    // the frame's top edge -- a band that fitted while its own label did not is
    // a stream with no name.
    const labels = everyNode(root).filter((each) => each.getAttribute('class') === 'graph-band-label')
    expect(labels.length).toBe(bands.length)
    for (const label of labels) {
      const baseline = Number(label.getAttribute('y'))
      expect(baseline, 'a band label is drawn above the frame').toBeGreaterThanOrEqual(frame.y)
      expect(baseline).toBeLessThanOrEqual(frame.y + frame.height)
    }
    for (const card of cardsIn(root)) {
      expect(inside(frame, card.rect), `${card.key} is drawn outside the frame`).toBe(true)
    }
  })

  it('draws the band’s label through the sanitizer, and never a stream name as markup', async () => {
    // A stream's name is a checkout's own string, so it is drawn through the
    // model's `Sanitized` label like every other document string: a hostile one
    // is text, whole, with nothing under it.
    const hostile = `<${'script'}>${'alert'}(1)</${'script'}>`
    const screen = elkScreen()
    await screen.load(patched(['plans', 0, 'stream'], hostile), RUNS)
    const drawing = screen.redraw()
    if (drawing === null) throw new Error('no drawing')
    const root = mounted(drawing)
    const labels = bandsIn(root).map((band) => band.label)
    expect(labels).toContain(hostile)
    expect(everyNode(root).every((each) => each.tagName !== 'SCRIPT')).toBe(true)
  })
})

describe('the graph screen and a card’s title', () => {
  it('wraps a title inside its card, and marks the cut only when the lines run out', async () => {
    // The card is 260 x 88 and its title is drawn in lines: **a title that fits
    // the card's lines is drawn whole** (the redesign: "wrap, don't truncate
    // with an ellipsis where it fits"), and only one that does not is cut -- and
    // then the cut is marked, because a title shortened in silence reads as the
    // whole title.
    const whole = 'a title of several words that a card wraps over its own lines'
    const { screen } = screenWith()
    await screen.load(patched(['plans', 0, 'nodes', 0, 'title'], whole), RUNS)
    const drawing = screen.redraw()
    if (drawing === null) throw new Error('no drawing')
    const lines = titleLinesIn(mounted(drawing), nodeKey(PLAN, 'task-one'))

    expect(lines.length).toBeGreaterThan(1)
    expect(lines.length).toBeLessThanOrEqual(GRAPH_NODE_TITLE_LINES)
    // The lines are the title's own words: a wrap breaks between two words, so
    // joining the lines with the space the break took puts the whole title back.
    expect(lines.join(' ')).toBe(whole)
    expect(lines.join(' ')).not.toContain('…')
    for (const line of lines) expect([...line].length).toBeLessThanOrEqual(GRAPH_NODE_TITLE_LINE_CODE_POINTS)

    // A title the card's lines cannot hold is cut, with the cut marked -- and it
    // is the card's cut and not the model's: the model's bound is 200 code
    // points, which is a good deal more than three lines hold.
    const long = 'word '.repeat(120)
    const other = screenWith()
    await other.screen.load(patched(['plans', 0, 'nodes', 0, 'title'], long), RUNS)
    const cut = other.screen.redraw()
    if (cut === null) throw new Error('no drawing')
    const cutLines = titleLinesIn(mounted(cut), nodeKey(PLAN, 'task-one'))
    expect(cutLines).toHaveLength(GRAPH_NODE_TITLE_LINES)
    expect(cutLines[cutLines.length - 1]?.endsWith('…')).toBe(true)
    expect([...cutLines.join(' ')].length).toBeLessThan([...long].length)
    for (const line of cutLines) expect([...line].length).toBeLessThanOrEqual(GRAPH_NODE_TITLE_LINE_CODE_POINTS)
    // And the cut is code-point-safe: no lone surrogate half is drawn.
    const astral = '\u{1F600}'
    const astralTitle = astral.repeat(40)
    const third = screenWith()
    await third.screen.load(patched(['plans', 0, 'nodes', 0, 'title'], astralTitle), RUNS)
    const astralCut = third.screen.redraw()
    if (astralCut === null) throw new Error('no drawing')
    const drawn = titleLinesIn(mounted(astralCut), nodeKey(PLAN, 'task-one')).join(' ')
    const lone = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/
    expect(lone.test(drawn)).toBe(false)
    expect(drawn).toContain(astral)
  })

  it('draws the mark only where the card’s lines dropped text, however short the last line is', async () => {
    // The mark is a **signal** and not a length: it says the card's three lines
    // could not hold the title, so a reader who sees it knows something was
    // dropped. A last line shorter than the line's bound is therefore not a
    // false mark -- a title of long words leaves its third line one word long
    // and still loses the rest. The pair below is the same title one word
    // longer, and it fits three lines: the mark appears with the drop and not
    // with how full the last line happens to be.
    const long = (letter: string): string => letter.repeat(GRAPH_NODE_TITLE_LINE_CODE_POINTS)
    const fits = `${long('a')} ${long('b')} c`
    const over = `${fits} ${long('d')}`

    const { screen } = screenWith()
    await screen.load(patched(['plans', 0, 'nodes', 0, 'title'], fits), RUNS)
    const drawing = screen.redraw()
    if (drawing === null) throw new Error('no drawing')
    const held = titleLinesIn(mounted(drawing), nodeKey(PLAN, 'task-one'))
    expect(held).toHaveLength(GRAPH_NODE_TITLE_LINES)
    expect(held.join(' ')).toBe(fits)
    expect(held.join(' ')).not.toContain('…')

    const other = screenWith()
    await other.screen.load(patched(['plans', 0, 'nodes', 0, 'title'], over), RUNS)
    const cut = other.screen.redraw()
    if (cut === null) throw new Error('no drawing')
    const cutLines = titleLinesIn(mounted(cut), nodeKey(PLAN, 'task-one'))
    expect(cutLines).toHaveLength(GRAPH_NODE_TITLE_LINES)
    // The last line is two code points and carries the mark, which is the case
    // the mark is read as a lie in: what it reports is the fourth line.
    expect(cutLines[cutLines.length - 1]).toBe('c…')
    const body = [...cutLines.slice(0, -1), (cutLines[cutLines.length - 1] ?? '').replace(/…$/, '')].join(' ')
    expect(over.startsWith(body)).toBe(true)
    expect([...body].length).toBeLessThan([...over].length)
  })

  it('draws a title the card cannot hold whole in the list, where the whole sentence is', async () => {
    // The two drawings of one model: the card wraps and cuts, and the list is
    // where a reader goes for the whole sentence (the reconciliation's F25). The
    // title is under the model's own bound, so nothing but the card's lines cuts
    // it -- a longer one is cut twice, and the list would carry that cut too.
    const long = 'a long task title '.repeat(6).trim()
    expect([...long].length).toBeGreaterThan(GRAPH_NODE_TITLE_LINE_CODE_POINTS * GRAPH_NODE_TITLE_LINES)
    expect([...long].length).toBeLessThanOrEqual(GRAPH_MAX_TITLE_CODE_POINTS)
    const { screen } = screenWith()
    await screen.load(patched(['plans', 0, 'nodes', 0, 'title'], long), RUNS)
    const canvas = screen.redraw()
    if (canvas === null) throw new Error('no drawing')
    expect(textOf(mounted(canvas))).not.toContain(long)

    const list = screen.change({ view: 'list' })
    if (list === null) throw new Error('no drawing')
    expect(textOf(mounted(list))).toContain(long)
  })

  it('names a card with the title whole and its words, and is a button only where a key opens it', async () => {
    // A card is a roving `tabindex` stop, and a stop that says nothing about
    // itself leaves a reader moving in the dark. The name is what the drawing
    // knows and the reader cannot: **the title whole**, which the card's three
    // lines cut, and every word the model answered beside it -- so the canvas is
    // where the whole sentence is, and the name is how it is reached there.
    //
    // The role is the gesture and nothing else: where a run resolved, Enter and
    // Space open it and the card is the `button` a reader expects to press; where
    // none did, no key does anything and `button` would promise a control that is
    // not there.
    const long = 'a long task title '.repeat(6).trim()
    const { screen } = screenWith()
    await screen.load(patched(['plans', 0, 'nodes', 0, 'title'], long), RUNS)
    const model = screen.lastModel
    if (model === null) throw new Error('no model')
    const drawing = screen.redraw()
    if (drawing === null) throw new Error('no drawing')
    const root = mounted(drawing)
    const cards = everyNode(root).filter((each) => each.getAttribute('class') === 'graph-node')
    expect(cards.length).toBeGreaterThan(1)

    const named = cards.find((each) => each.getAttribute('data-key') === nodeKey(PLAN, 'task-one'))
    if (named === undefined) throw new Error('the first task was not drawn')
    const words = model.columns.flatMap((column) => column.plans).flatMap((plan) => plan.nodes).find((node) => node.id === 'task-one')?.words ?? []
    // The card cut it -- its lines are a drawing of the sentence, not the
    // sentence -- and the name is the sentence, which is the whole of the claim.
    expect(titleLinesIn(root, nodeKey(PLAN, 'task-one')).join(' ')).not.toBe(long)
    expect(named.getAttribute('aria-label')).toBe([long, ...words].join(', '))
    // A word as well as the title: `critical` is drawn as a glow, which is
    // `aria-hidden`, so a name is the only thing that can carry it with no
    // colour and no motion.
    expect(words.length).toBeGreaterThan(0)
    for (const word of words) expect(named.getAttribute('aria-label')).toContain(word)

    const roles = new Set<string>()
    for (const card of cards) {
      const role = card.getAttribute('role') ?? ''
      roles.add(role)
      const opens = card.getAttribute('data-run-id')
      expect(role, `${card.getAttribute('data-key') ?? ''} carries data-run-id=${String(opens)}`).toBe(opens === null ? 'group' : 'button')
      expect(card.getAttribute('aria-label') ?? '', `${card.getAttribute('data-key') ?? ''} is named`).not.toBe('')
    }
    // Both kinds are in this picture -- the fixture's first task carries a run and
    // its third carries none -- so the rule above is a rule and not a constant.
    expect([...roles].sort()).toEqual(['button', 'group'])
  })

  it('names a card by its task id where the title holds no word a reader could read', async () => {
    // A title of whitespace alone is `ok` to the model and wraps to no word at
    // all, so the whole-title half of the name would itself be blank: the card
    // would be a stop that says nothing, at the head of a name that starts with
    // nothing. The task's id is what is left for it to be told apart by.
    const { screen } = screenWith()
    await screen.load(patched(['plans', 0, 'nodes', 0, 'title'], '   \t  '), RUNS)
    const model = screen.lastModel
    if (model === null) throw new Error('no model')
    const drawing = screen.redraw()
    if (drawing === null) throw new Error('no drawing')
    const root = mounted(drawing)
    const named = everyNode(root).find((each) => each.getAttribute('data-key') === nodeKey(PLAN, 'task-one'))
    if (named === undefined) throw new Error('the first task was not drawn')
    const words = model.columns.flatMap((column) => column.plans).flatMap((plan) => plan.nodes).find((node) => node.id === 'task-one')?.words ?? []
    // The words are still there and still said -- only the title, which holds no
    // word at all, gives way to the id.
    expect(words.length).toBeGreaterThan(0)
    const label = named.getAttribute('aria-label') ?? ''
    expect(label).toBe(['task-one', ...words].join(', '))
    expect(label.trim()).toBe(label)
  })
})

// ---------------------------------------------------------------------------
// The words
// ---------------------------------------------------------------------------

describe('the graph screen and the words a node carries', () => {
  it('draws every word the model answered, as its own element, beside its node', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const model = screen.lastModel
    if (model === null) throw new Error('no model')
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const root = mounted(element)

    const expected = new Set<string>()
    for (const column of model.columns) {
      for (const plan of column.plans) for (const node of plan.nodes) for (const word of node.words) expected.add(word)
    }
    expect([...wordSet(root)].sort()).toEqual([...expected].sort())
    // A state is a word on this screen, and the word drawn beside the state is
    // the state's own word (design §3.5 and the §3.9 amendment).
    for (const [word, text] of drawnWords(root)) expect(text).toBe(word)
    expect(wordSet(root).has('done')).toBe(true)
    expect(wordSet(root).has('critical')).toBe(true)
    expect(wordSet(root).has('blocked')).toBe(true)
    expect(wordSet(root).has('waiting')).toBe(true)
    expect(wordSet(root).has('halted')).toBe(true)
  })

  it('draws the word a live stage adds, and the one an empty stage leaves', async () => {
    const { screen } = screenWith()
    // The fixture's running task with no stage at all: a running task whose stage
    // the plan does not know is `queued`.
    await screen.load(patched(['plans', 0, 'nodes', 1, 'stage'], ''), RUNS)
    const queued = screen.redraw()
    if (queued === null) throw new Error('no drawing')
    expect(wordSet(mounted(queued)).has('queued')).toBe(true)

    await screen.frame([runRow('20261025-121805-aa02', 'review_first')])
    const reviewing = screen.redraw()
    if (reviewing === null) throw new Error('no drawing')
    expect(wordSet(mounted(reviewing)).has('in review')).toBe(true)

    await screen.frame([runRow('20261025-121805-aa02', 'fix')])
    const fixing = screen.redraw()
    if (fixing === null) throw new Error('no drawing')
    expect(wordSet(mounted(fixing)).has('fixing')).toBe(true)
  })

  it('takes a stage from the feed only for a running task, and only when the suffix is unambiguous', () => {
    expect(withFeedStages(DOCUMENT, [runRow('20261025-121805-aa02', 'fix')]).plans[0]?.nodes[1]?.stage).toBe('fix')
    // A finished task takes no stage: its word is `done` whatever the frame says.
    expect(withFeedStages(DOCUMENT, [runRow('20261025-121805-aa01', 'fix')]).plans[0]?.nodes[0]?.stage).toBe('')
    // An empty stage is no reading rather than `queued`, so the plan's stands.
    expect(withFeedStages(DOCUMENT, [runRow('20261025-121805-aa02', '')]).plans[0]?.nodes[1]?.stage).toBe('implement')
    // Two rows that disagree about one suffix are not an answer.
    const ambiguous = withFeedStages(DOCUMENT, [
      runRow('20261025-121805-aa02', 'fix'),
      runRow('20261026-090000-aa02', 'review'),
    ])
    expect(ambiguous.plans[0]?.nodes[1]?.stage).toBe('implement')
    // A document the frame changed nothing about comes back as **itself**, which
    // is what makes the cache hit in the first case exact rather than lucky.
    expect(withFeedStages(DOCUMENT, [])).toBe(DOCUMENT)
    expect(withFeedStages(DOCUMENT, [runRow('20261025-121805-zz99', 'fix')])).toBe(DOCUMENT)
  })
})

// ---------------------------------------------------------------------------
// The join a node's gesture is worth
// ---------------------------------------------------------------------------

describe('the graph screen and a node own run', () => {
  it('resolves a suffix to the one run that ends with it, and to nothing when two do', () => {
    expect(runForSuffix('aa01', RUNS)).toBe('20261025-121805-aa01')
    expect(runForSuffix('aa01', ['20261025-121805-aa01', '20261026-090000-aa01'])).toBeNull()
    expect(runForSuffix('aa01', [])).toBeNull()
    expect(runForSuffix('', RUNS)).toBeNull()
    // A run whose whole id is the suffix is not a run the suffix names.
    expect(runForSuffix('aa01', ['aa01'])).toBeNull()
  })

  it('says why a node opens nothing rather than guessing at a run', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const nodes = screen.lastModel?.columns[0]?.plans[0]?.nodes ?? []
    const joined = nodes.find((each) => each.id === 'task-one')
    const bare = nodes.find((each) => each.id === 'task-three')
    if (joined === undefined || bare === undefined) throw new Error('the fixture changed')

    expect(doorFor(joined, RUNS).run).toBe('20261025-121805-aa01')
    expect(doorFor(bare, RUNS).run).toBeNull()
    const two = doorFor(joined, ['20261025-121805-aa01', '20261026-090000-aa01'])
    expect(two.run).toBeNull()
    expect(two.because).toBe(GRAPH_RUN_AMBIGUOUS)
  })

  it('draws the run the join resolved, and no run id at all for an ambiguous suffix', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const runs = everyNode(mounted(element))
      .map((each) => each.getAttribute('data-run-id'))
      .filter((each): each is string => each !== null)
    expect(runs).toContain('20261025-121805-aa01')

    const other = screenWith()
    await other.screen.load(DOCUMENT, ['20261025-121805-aa01', '20261026-090000-aa01'])
    const ambiguous = other.screen.redraw()
    if (ambiguous === null) throw new Error('no drawing')
    const root = mounted(ambiguous)
    expect(everyNode(root).every((each) => each.getAttribute('data-run-id') === null)).toBe(true)
    expect(textOf(root)).toContain(GRAPH_RUN_AMBIGUOUS)
  })

  it('reads the run list the console already has, and tolerates a shape it does not know', () => {
    expect(runIdsIn({ runs: [{ run: 'a' }, { run_id: 'b' }, { run: 'a' }, { other: 'c' }, 'nonsense'] })).toEqual(['a', 'b'])
    expect(runIdsIn({ runs: [] })).toEqual([])
    expect(runIdsIn(null)).toEqual([])
    expect(runIdsIn({})).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// The finished releases: the chip, and the box it opens
// ---------------------------------------------------------------------------

describe('the graph screen and a finished release', () => {
  it('draws a shut chip and none of its tasks, and opens them only when the chip is expanded', async () => {
    const { screen } = screenWith()
    await screen.load(FINISHED, RUNS)
    const first = screen.redraw()
    if (first === null) throw new Error('no drawing')
    const shut = mounted(first)

    const chip = everyNode(shut).find((each) => each.getAttribute('data-chip') === CHIP)
    expect(chip).toBeDefined()
    expect(chip?.tagName).toBe('BUTTON')
    expect(chip?.getAttribute('aria-expanded')).toBe('false')
    // A shut chip's tasks are not on the picture at all.
    expect(keys(shut)).not.toContain(nodeKey(PLAN, 'task-one'))

    const opened = screen.change({ expanded: [CHIP] })
    if (opened === null) throw new Error('no drawing')
    const open = mounted(opened)
    expect(everyNode(open).find((each) => each.getAttribute('data-chip') === CHIP)?.getAttribute('aria-expanded')).toBe('true')
    expect(keys(open)).toContain(nodeKey(PLAN, 'task-one'))
    // The chip's own body carries the same "n of m done" the box header does.
    expect(textOf(open)).toContain('1 of 3 done')

    const closed = screen.change({ expanded: [] })
    if (closed === null) throw new Error('no drawing')
    expect(keys(mounted(closed))).not.toContain(nodeKey(PLAN, 'task-one'))
  })

  it('groups the chips by stream, and names each stream', async () => {
    const { screen } = screenWith()
    await screen.load(FINISHED, RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    // Selected by their own class rather than by `data-stream`: a stream band is
    // drawn behind its row and carries the same stream name in that attribute.
    const groups = everyNode(mounted(element)).filter((each) => each.getAttribute('class')?.includes('graph-chip-group') === true)
    expect(groups.length).toBeGreaterThan(0)
    expect(groups.every((each) => (each.getAttribute('data-stream') ?? '') !== '')).toBe(true)
    const labels = everyNode(mounted(element)).filter((each) => each.getAttribute('class')?.includes('graph-chip-stream') === true)
    expect(labels).toHaveLength(groups.length)
    expect(labels.every((each) => (each.textContent ?? '').trim() !== '')).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// The edges
// ---------------------------------------------------------------------------

describe('the graph screen and its edges', () => {
  it('marks a cross-release edge faint, and highlights the ones that touch the selected task', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const first = screen.redraw()
    if (first === null) throw new Error('no drawing')
    const root = mounted(first)

    const cross = everyNode(root).filter((each) => each.getAttribute('data-cross') === 'true')
    expect(cross.length).toBeGreaterThan(0)
    expect(cross.every((each) => each.getAttribute('data-selected') === 'false')).toBe(true)

    // `task-five` waits on `task-two` across releases: selecting one end
    // highlights that edge, which is the spec's "they highlight when a task is
    // selected".
    const selected = screen.change({ selected: nodeKey(PLAN, 'task-two') })
    if (selected === null) throw new Error('no drawing')
    const after = everyNode(mounted(selected))
    expect(
      after.filter((each) => each.getAttribute('data-cross') === 'true' && each.getAttribute('data-selected') === 'true').length,
    ).toBe(1)
    // The selection is a fact on the edge and on the node, so a rule can draw it
    // without knowing which task was clicked.
    expect(after.filter((each) => each.getAttribute('data-key') !== null && each.getAttribute('data-selected') === 'true').length).toBe(1)
  })

  it('marks the critical path apart from the rest, and carries no fact a rule cannot key on', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const edges = everyNode(mounted(element)).filter((each) => each.getAttribute('class') === 'graph-edge')
    expect(edges.length).toBeGreaterThan(0)
    expect(edges.some((each) => each.getAttribute('data-critical') === 'true')).toBe(true)
    for (const edge of edges) {
      expect([...edge.attributes.keys()].sort()).toEqual(['class', 'data-critical', 'data-cross', 'data-selected'])
    }
  })
})

// ---------------------------------------------------------------------------
// What the screen refuses to draw
// ---------------------------------------------------------------------------

describe('the graph screen and what it will not draw', () => {
  it('draws a hostile title as text, and parses nothing', async () => {
    const HOSTILE = '</text><script>alert(1)</script><img src=x onerror=alert(2)>'
    const { screen } = screenWith()
    await screen.load(patched(['plans', 0, 'nodes', 0, 'title'], HOSTILE), RUNS)
    screen.change({ view: 'list' })
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const nodes = everyNode(mounted(element))

    expect(nodes.every((each) => each.tagName !== 'SCRIPT')).toBe(true)
    expect(nodes.every((each) => each.tagName !== 'IMG')).toBe(true)
    // The hostile string is one text node, whole, with nothing under it: a text
    // slot is not a parser, which is why the model cuts a title before a screen
    // sees it and why a screen never builds markup.
    const holder = nodes.filter((each) => each.textContent === HOSTILE)
    expect(holder.length).toBe(1)
    expect(holder[0]?.children.length).toBe(0)
  })

  it('builds no destination: the pull request number is text, and no element carries one', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const root = mounted(element)

    expect(textOf(root)).toContain('#101')
    expect(everyNode(root).every((each) => each.getAttribute('data-href') === null)).toBe(true)
    // The number says why it is not a link, in the same sentence the other two
    // screens draw.
    expect(textOf(root)).toContain(GRAPH_PR_NO_LINK_REASON)
    expect(GRAPH_PR_NO_LINK_REASON).toBe(LIVE_PR_NO_LINK_REASON)
    expect(GRAPH_PR_NO_LINK_REASON).toBe(PR_NO_LINK_REASON)
  })

  it('says a checkout with no plan is empty rather than broken', async () => {
    const { screen } = screenWith()
    await screen.load(patched(['plans'], []), RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const root = mounted(element)
    expect(textOf(root)).toContain(GRAPH_NO_PLANS)
    expect(everyNode(root).some((each) => each.tagName === 'SVG')).toBe(false)
  })

  it('notes the tasks the model dropped instead of drawing a picture that looks complete', async () => {
    const many = Array.from({ length: 501 }, (_, index) => ({
      id: `task-${String(index)}`,
      title: `Task ${String(index)}`,
      state: 'ready',
      critical: false,
      closed: false,
      level: 1,
    }))
    const document = patched(['plans', 0, 'nodes'], many)
    const model = modelOf(document)
    expect(model.notShown).toBeGreaterThan(0)

    const root = rendered(model)
    expect(everyNode(root).some((each) => each.getAttribute('class') === 'graph-not-shown')).toBe(true)
    expect(textOf(root)).toContain(`${String(model.notShown)} ${GRAPH_NOT_SHOWN_SINGULAR}`)
  })

  it('forgets the document a refusal replaced, so no later frame can redraw it', async () => {
    // A refusal is the entry's screen for a plan the console could not read, and
    // the plan it read *before* the refusal is still in this screen's hands: a
    // live frame is the graph's other input and would fold its stages into that
    // older document and draw it -- a picture that says the plan is fine when the
    // read that just failed says it is not. `forget` is what the entry calls on
    // every refusal path, so a frame that arrives afterwards has nothing to draw.
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    expect(screen.lastModel).not.toBeNull()

    screen.forget()
    expect(screen.lastModel).toBeNull()
    expect(screen.redraw()).toBeNull()
    expect(await screen.frame([runRow('20261025-121805-aa02', 'fix')])).toBeNull()
  })

  it('falls back to the list, saying why, when there are no positions to draw', () => {
    // `positions: null` is the state before the first layout has answered, and
    // the state a worker that answered nothing leaves behind. The canvas is the
    // only thing lost: the model is still a graph and the list is still true.
    const root = rendered(modelOf())
    expect(textOf(root)).toContain(GRAPH_NO_CANVAS)
    expect(everyNode(root).some((each) => each.getAttribute('role') === 'list')).toBe(true)
    expect(everyNode(root).some((each) => each.tagName === 'SVG')).toBe(false)
  })

  it('names the theme it was drawn in, so one stylesheet serves both', () => {
    const model = modelOf()
    expect(rendered(model, GRAPH_INITIAL_STATE, 'dark').getAttribute('data-theme')).toBe('dark')
    expect(rendered(model, GRAPH_INITIAL_STATE, 'light').getAttribute('data-theme')).toBe('light')
  })
})

// ---------------------------------------------------------------------------
// The list, and the two drawings of one model
// ---------------------------------------------------------------------------

describe('the graph screen and its list view', () => {
  it('carries every node of the model, including the ones a shut chip hides from the canvas', async () => {
    const { screen } = screenWith()
    await screen.load(FINISHED, RUNS)
    screen.change({ view: 'list' })
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const root = mounted(element)

    const model = screen.lastModel
    if (model === null) throw new Error('no model')
    const expected = new Set<string>()
    for (const column of model.columns) {
      for (const plan of column.plans) for (const node of plan.nodes) expected.add(nodeKey(plan.id, node.id))
    }
    expect([...keys(root)].sort()).toEqual([...expected].sort())
    expect(keys(root)).toContain(nodeKey(PLAN, 'task-one'))
  })

  it('draws the same release bar in both drawings', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const canvas = screen.redraw()
    if (canvas === null) throw new Error('no drawing')
    expect(textOf(mounted(canvas))).toContain('1 of 3 done')

    screen.change({ view: 'list' })
    const list = screen.redraw()
    if (list === null) throw new Error('no drawing')
    expect(textOf(mounted(list))).toContain('1 of 3 done')
  })

  it('switches drawings without asking the engine anything', async () => {
    const { screen, worker } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const list = screen.change({ view: 'list' })
    if (list === null) throw new Error('no drawing')
    expect(everyNode(mounted(list)).some((each) => each.getAttribute('role') === 'list')).toBe(true)
    expect(worker.placements).toBe(2)
  })
})

// ---------------------------------------------------------------------------
// The tabs, the zoom and the names
// ---------------------------------------------------------------------------

describe('the graph screen and its navigation', () => {
  it('offers one segment per stream and All, with the chosen one pressed and alone in the tab order', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const segments = classedIn(mounted(element), 'graph-tab')
    expect(segments.map((each) => each.getAttribute('data-tab'))).toEqual(['stream:example', 'stream:', 'all'])
    expect(segments.map((each) => each.getAttribute('aria-pressed'))).toEqual(['false', 'false', 'true'])
    // The roving tabindex the design's §4.3 gives a group of buttons: one is in
    // the tab order and the rest are not.
    expect(segments.map((each) => each.getAttribute('tabindex'))).toEqual(['-1', '-1', '0'])
    // A segment says it is pressed and no more: `aria-selected` is a tab's, and
    // this filter is a segmented control rather than a tablist (step 6b).
    expect(segments.map((each) => each.getAttribute('aria-selected'))).toEqual([null, null, null])
  })

  it('puts the first task in the tab order while nothing is selected, and the selected one after that', async () => {
    // The doors are a roving `tabindex` too, and the one thing that rule may not
    // do is leave the group with **no** stop: nothing is selected when the screen
    // opens, no gesture but a click or an arrow key selects a task, and a door the
    // arrow keys reach is a door the tab order had to reach first. A group whose
    // every door is at `-1` is a picture and a list no keyboard can enter -- the
    // same failure the navigation's own roving `tabindex` names and forbids.
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const canvas = screen.redraw()
    if (canvas === null) throw new Error('no drawing')
    const drawn = mounted(canvas)
    const doors = everyNode(drawn).filter((each) => each.getAttribute('data-key') !== null)
    expect(doors.length).toBeGreaterThan(1)
    expect(doors.map((each) => each.getAttribute('tabindex')).filter((each) => each === '0')).toHaveLength(1)
    expect(doors[0]?.getAttribute('tabindex')).toBe('0')
    // The stop is the tab order's, and the selection is not the same fact: the
    // first door is where the keyboard enters, and it is not drawn as chosen.
    expect(doors[0]?.getAttribute('data-selected')).toBe('false')

    // A selection moves the stop to the task that was selected, which is the door
    // the reader is working with.
    const key = keys(drawn)[1] ?? ''
    expect(key, 'the fixture drew no second task: the case would prove nothing').not.toBe('')
    const list = screen.change({ selected: key, view: 'list' })
    if (list === null) throw new Error('no drawing')
    const rows = everyNode(mounted(list)).filter((each) => each.getAttribute('data-key') !== null)
    expect(rows.map((each) => each.getAttribute('tabindex')).filter((each) => each === '0')).toHaveLength(1)
    expect(rows.find((each) => each.getAttribute('tabindex') === '0')?.getAttribute('data-key')).toBe(key)
  })

  it('falls back to All for a stream the document no longer carries', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    await screen.narrow('a-stream-that-is-not-there')
    expect(screen.lastModel?.columns.length).toBe(2)
    // A plan that names no stream is a column like any other, and All draws it.
    expect(screen.lastModel?.columns.map((column) => column.stream)).toContain('')
  })

  it('keeps a zoom inside its bounds and centres it on the picture', () => {
    const bounds = { x: 0, y: 0, width: 100, height: 50 }
    expect(viewBoxOf(bounds, 1)).toBe('0 0 100 50')
    expect(viewBoxOf(bounds, 2)).toBe('25 12.5 50 25')
    expect(viewBoxOf(bounds, 0.0001)).toBe(viewBoxOf(bounds, GRAPH_ZOOM_MIN))
    expect(viewBoxOf(bounds, 1000)).toBe(viewBoxOf(bounds, GRAPH_ZOOM_MAX))
  })

  it('clamps a zoom where it is stored, so the next step is taken from a bound', () => {
    // The bound is applied where the zoom is *kept* and not only where it is
    // drawn: the entry takes each wheel step from `current.zoom`, so a screen
    // that stored an unbounded one would need a hundred scrolls to come back
    // from a pinch -- and a `viewBox` written from the stored number is a
    // picture a person cannot find their way out of.
    const { screen } = screenWith()
    screen.change({ zoom: 1000 })
    expect(screen.current.zoom).toBe(GRAPH_ZOOM_MAX)
    screen.change({ zoom: 0.0001 })
    expect(screen.current.zoom).toBe(GRAPH_ZOOM_MIN)
    // A change that carries no zoom leaves the stored one where it was.
    screen.change({ zoom: 2 })
    screen.change({ view: 'list' })
    expect(screen.current.zoom).toBe(2)
  })

  it('names a tab as an attribute value, and reads it back as the tab it was', () => {
    // The key is a bijection, `all` included: a stream is called whatever a
    // checkout likes, and a stream called `all` must not read back as All.
    for (const tab of [null, '', 'example', 'all', 'stream:all'] as const) {
      expect(tabOf(tabKey(tab))).toBe(tab)
    }
    expect(tabKey(null)).toBe('all')
    expect(tabKey('')).toBe('stream:')
    // A key this build does not know is All rather than an invented stream.
    expect(tabOf('nonsense')).toBeNull()
    expect(GRAPH_INITIAL_STATE).toEqual({ tab: null, view: 'graph', selected: null, expanded: [], zoom: 1 })
  })

  it('answers an identical frame with nothing, and a changed one with a drawing', async () => {
    const { screen, worker } = screenWith()
    // Two running tasks, because order is only a claim about a frame that says
    // more than one thing: the fixture has one running task, and a frame for a
    // task that is not running says nothing this screen draws (the case below).
    await screen.load(twoRunning(), RUNS)
    const frame = [runRow('20261025-121805-aa02', 'fix')]
    expect(await screen.frame(frame)).not.toBeNull()
    expect(await screen.frame(frame)).toBeNull()
    // The order the rows arrive in is not part of what a frame says.
    expect(
      await screen.frame([runRow('20261025-121805-aa07', 'review'), runRow('20261025-121805-aa02', 'fix')]),
    ).not.toBeNull()
    expect(await screen.frame([runRow('20261025-121805-aa02', 'fix'), runRow('20261025-121805-aa07', 'review')])).toBeNull()
    expect(worker.placements).toBe(2)
  })

  it('answers a frame that says nothing about a drawn task with nothing', async () => {
    // **The feed is installation-wide and the drawing is not.** A `live.watch`
    // frame carries every run the console can see -- another installation's, and
    // the finished tasks' of this plan -- and a key taken from all of it would
    // rebuild the graph (a fresh model over every node, and the whole DOM) once a
    // second for a picture that did not change. `aa01` is this fixture's
    // `done` task and `zz99` belongs to no task at all: neither says anything
    // about the graph, and neither is a reason to redraw it.
    const { screen, worker } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    expect(await screen.frame([runRow('20261025-121805-aa01', 'implement')])).toBeNull()
    expect(await screen.frame([runRow('20261025-121805-zz99', 'review')])).toBeNull()
    expect(worker.placements).toBe(2)
  })

  it('re-reads the plan at the cadence GV2.1 gave the read, and no oftener', () => {
    // Two copies of one number: this screen's and `cli.ts`'s. They cannot drift.
    expect(GRAPH_REFRESH_MS).toBe(PLAN_STATUS_MIN_INTERVAL_MS)
  })

  it('draws the second plan, which names no stream and no release', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    expect(keys(mounted(element))).toContain(nodeKey(SECOND, 'second-one'))
  })
})

// ---------------------------------------------------------------------------
// The toolbar, the inspector and the legend (console redesign, step 6b)
// ---------------------------------------------------------------------------

/** classedIn is every element of one exact class under a node, in document
 * order, and the root itself when it carries the class. */
function classedIn(root: FakeNode, name: string): readonly FakeNode[] {
  return everyNode(root).filter((each) => (each.getAttribute('class') ?? '').split(/\s+/).includes(name))
}

/** inspectorIn is the panel a drawing draws beside the canvas, or `null` while
 * no task is selected. */
function inspectorIn(root: FakeNode): FakeNode | null {
  return classedIn(root, 'graph-inspector')[0] ?? null
}

/** inspectorOf is the panel of one selected task, over a fresh screen: the case
 * that reads a panel says as little as it can about how it got there. */
async function inspectorOf(key: string): Promise<FakeNode> {
  const { screen } = screenWith()
  await screen.load(DOCUMENT, RUNS)
  const element = screen.change({ selected: key })
  if (element === null) throw new Error('no drawing')
  const panel = inspectorIn(mounted(element))
  if (panel === null) throw new Error('the drawing drew no inspector for the task it selected')
  return panel
}

/** related is the tasks one of the panel's two lists names, as the layout keys
 * its buttons carry. */
function related(panel: FakeNode, name: string): readonly string[] {
  return classedIn(panel, name).map((each) => each.getAttribute('data-key') ?? '')
}

/** LEGEND_WORDS is the words the legend names: every state a card can carry.
 * `stuck` is a word of the model's own list that no reading reaches, and a
 * legend entry for a word no card draws would be a word about nothing. */
const LEGEND_WORDS: readonly string[] = Object.freeze(STATE_WORDS.filter((word) => word !== 'stuck'))

describe('the graph screen and its toolbar', () => {
  it('draws the stream filter and the three controls as one toolbar', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const drawn = mounted(element)
    const toolbar = classedIn(drawn, 'graph-toolbar')[0]
    if (toolbar === undefined) throw new Error('the graph drew no toolbar')
    // One toolbar, and the four controls are in it: the filter, and the three
    // buttons beside it.
    const inside = everyNode(toolbar).map((each) => each.getAttribute('class') ?? '')
    for (const name of ['graph-filter', 'graph-actions', 'graph-view', 'graph-refresh', 'graph-fit']) {
      expect(inside, `the toolbar does not hold ${name}`).toContain(name)
    }
    // The filter is a group of ordinary buttons rather than a tablist: nothing
    // here is a tab, and a group is what carries the one name the whole set has.
    const filter = classedIn(toolbar, 'graph-filter')[0]
    expect(filter?.getAttribute('role')).toBe('group')
    expect(filter?.getAttribute('aria-label')).toBe(GRAPH_FILTER_LABEL)
    expect(classedIn(drawn, 'graph-tab').every((each) => each.getAttribute('role') === null)).toBe(true)
    // The title block is the header's own and stays outside the toolbar.
    expect(classedIn(toolbar, 'graph-title')).toHaveLength(0)
  })

  it('lays out only the stream the filter chose, and draws no band for the others', async () => {
    // **The filter is applied to the model and not to the drawing.** The engine
    // is handed one stream's nodes and edges, so the picture has one row in it;
    // hiding the other streams' cards at the drawing would leave the rows their
    // layout took standing empty under the picture -- which is what the review of
    // step 6a saw and what this case exists for.
    const { screen, worker } = screenWith()
    await screen.narrow('example')
    await screen.load(DOCUMENT, RUNS)
    expect(worker.placements, 'the engine was asked for a picture of every stream').toBe(1)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const drawn = mounted(element)
    expect(bandsIn(drawn).map((band) => band.stream)).toEqual(['example'])
    expect(screen.lastModel?.columns.map((column) => column.stream)).toEqual(['example'])
    // And nothing of the other plan is drawn: not a card, not a release box.
    expect(keys(drawn).filter((key) => key.includes('example-second'))).toEqual([])
  })
})

describe('the graph screen and the inspector', () => {
  const SELECTED = nodeKey(PLAN, 'task-two')

  it('draws no inspector while no task is selected', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const drawn = mounted(element)
    expect(inspectorIn(drawn)).toBeNull()
    // The picture is where it has always been: the panel is drawn *beside* the
    // canvas and never instead of it.
    expect(classedIn(drawn, 'graph-canvas')).toHaveLength(1)
  })

  it('draws the selected task’s own facts: its words, its stream, its run and what it waits on', async () => {
    const panel = await inspectorOf(SELECTED)
    // The words are the card's own, and they are text: state is never a tone
    // alone, and the panel is the one place the whole state is spelled.
    expect(drawnWords(panel).map(([word]) => word)).toEqual(['implementing', 'critical'])
    expect(textOf(panel)).toContain('The second task of the example plan')
    // Its stream, and the run suffix its card carries.
    expect(textOf(panel)).toContain('example')
    expect(textOf(panel)).toContain('aa02')
    // What it waits on, and what it unblocks: one button per task, carrying the
    // card's own key, because a press on one is a selection of that card.
    expect(related(panel, 'graph-inspector-need')).toEqual([nodeKey(PLAN, 'task-one')])
    expect(related(panel, 'graph-inspector-unblock')).toEqual([nodeKey(PLAN, 'task-five')])
    // The button draws the task it names: a task id is a key this screen joins
    // on and never spells (the model's own rule), so the name a reader reads is
    // the title the model already sanitized for the card.
    expect(classedIn(panel, 'graph-inspector-task-title').map((each) => each.textContent)).toEqual([
      'The first task of the example plan',
      'A task waiting on a dependency',
    ])
    // And the door: the run the button opens is the one the console's own run
    // list answered for this task's suffix.
    const open = classedIn(panel, 'graph-inspector-open')[0]
    expect(open?.getAttribute('data-run-id')).toBe('20261025-121805-aa02')
    expect(open?.textContent).toBe(GRAPH_OPEN_RUN_LABEL)
  })

  it('says what a task with nothing to wait on and nothing to unblock is', async () => {
    const panel = await inspectorOf(nodeKey(PLAN, 'task-one'))
    expect(related(panel, 'graph-inspector-unblock')).toEqual([nodeKey(PLAN, 'task-two')])
    expect(related(panel, 'graph-inspector-need')).toEqual([])
    // An empty list is said in words: a heading with nothing under it is a
    // reader wondering whether the panel failed to draw.
    expect(classedIn(panel, 'graph-inspector-none').map((each) => each.textContent)).toEqual([GRAPH_INSPECTOR_NONE])
  })

  it('says a task has no run, and draws no Open run for it', async () => {
    const panel = await inspectorOf(nodeKey(PLAN, 'task-three'))
    expect(classedIn(panel, 'graph-inspector-open')).toHaveLength(0)
    expect(textOf(panel)).toContain(GRAPH_NO_RUN)
  })

  it('offers Pause as a write this release does not make', async () => {
    // The panel is read-only in 0.118.0: the console writes nothing but the
    // operations its allow-lists name, and pausing a task is not one of them. The
    // control is drawn so the shape of the panel is the one the design asks for,
    // and it is drawn disabled -- `aria-disabled`, the console's own sentence in
    // `title`, and no listener at all (the entry never wires it).
    const panel = await inspectorOf(SELECTED)
    const pause = classedIn(panel, 'graph-inspector-pause')[0]
    if (pause === undefined) throw new Error('the inspector drew no Pause control')
    expect(pause.getAttribute('aria-disabled')).toBe('true')
    expect(pause.getAttribute('title')).toBe(WRITE_UNAVAILABLE)
    // `disabledWrite`'s own label is the control's name and the refusal in one
    // sentence, which is what a screen reader reads out.
    expect(pause.getAttribute('aria-label')).toBe(`${GRAPH_PAUSE_LABEL}: ${WRITE_UNAVAILABLE.toLowerCase()}`)
    expect(textOf(pause)).toContain(GRAPH_PAUSE_LABEL)
  })

  it('keeps the panel beside the canvas it belongs to', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const element = screen.change({ selected: SELECTED })
    if (element === null) throw new Error('no drawing')
    const drawn = mounted(element)
    const stage = classedIn(drawn, 'graph-stage')[0]
    if (stage === undefined) throw new Error('the drawing put no stage around the picture')
    expect(classedIn(stage, 'graph-canvas')).toHaveLength(1)
    expect(classedIn(stage, 'graph-inspector')).toHaveLength(1)
  })
})

describe('the graph screen and its legend', () => {
  it('names every state a card can carry, and the two kinds of edge, under the canvas', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const drawn = mounted(element)
    const legend = classedIn(drawn, 'graph-legend')[0]
    if (legend === undefined) throw new Error('the graph drew no legend')
    // Under the canvas, in the tree and on the panel: the legend explains the
    // picture, so it is read after it.
    const order = everyNode(drawn).map((each) => each.getAttribute('class') ?? '')
    expect(order.indexOf('graph-canvas')).toBeLessThan(order.indexOf('graph-legend'))
    // Every state the cards draw, and each of them words as well as a swatch:
    // the border style is the second channel and the word is the first.
    const swatches = classedIn(legend, 'graph-legend-swatch')
    expect(swatches.map((each) => each.getAttribute('data-state')).filter((each) => each !== null)).toEqual(LEGEND_WORDS)
    expect(classedIn(legend, 'graph-legend-word').map((each) => each.textContent)).toEqual([
      ...LEGEND_WORDS,
      GRAPH_DEPENDS_ON,
      GRAPH_CRITICAL_PATH,
    ])
    // And the two kinds of line the canvas draws: a dependency, and the critical
    // path through it.
    expect(swatches.map((each) => each.getAttribute('data-edge')).filter((each) => each !== null)).toEqual([
      'dependency',
      'critical',
    ])
    // Every swatch is a drawing and never a sentence: the word beside it is the
    // sentence, and a reader who hears the panel hears each word once.
    expect(swatches.every((each) => each.getAttribute('aria-hidden') === 'true')).toBe(true)
  })
})
