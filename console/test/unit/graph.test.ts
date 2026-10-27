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
//       positions = await this.deps.client.layout(graphOf(model))
//
// -- which bypasses the cache and asks the worker once per frame. The case then
// fails on its second assertion (`placements` is 2, not 1). Red before, green
// after; the PR carries both runs.
//
// Everything runs against `test/support/fakeDom.ts` and a fake `WorkerLike`: no
// browser, no Electron, no ELK. The fake worker answers **every** layout as it
// is posted, so `placements` counts the asks and nothing else.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { measured } from '../../src/shared/availability'
import { decodePlanStatus } from '../../src/shared/schema'
import type { PlanStatusView, RunView } from '../../src/shared/schema'
import { nodeKey } from '../../src/renderer/graph/layout'
import { LayoutClient } from '../../src/renderer/graph/layout-client'
import type { WorkerLike } from '../../src/renderer/graph/layout-client'
import { buildGraphModel } from '../../src/renderer/graph/model'
import { LIVE_PR_NO_LINK_REASON } from '../../src/renderer/screens/live'
import { PR_NO_LINK_REASON } from '../../src/renderer/screens/run'
import {
  GRAPH_INITIAL_STATE,
  GRAPH_NO_CANVAS,
  GRAPH_NO_PLANS,
  GRAPH_NOT_SHOWN_SINGULAR,
  GRAPH_PR_NO_LINK_REASON,
  GRAPH_RUN_AMBIGUOUS,
  GRAPH_ZOOM_MAX,
  GRAPH_ZOOM_MIN,
  GraphScreen,
  SVG_NAMESPACE_URI,
  doorFor,
  releaseKey,
  renderGraph,
  runForSuffix,
  runIdsIn,
  tabKey,
  viewBoxOf,
  withFeedStages,
} from '../../src/renderer/screens/graph'
import type { GraphFactory, GraphState, GraphTheme, ScreenElement } from '../../src/renderer/screens/graph'
import { FakeDocument, everyNode, textOf } from '../support/fakeDom'
import type { FakeNode } from '../support/fakeDom'

// ---------------------------------------------------------------------------
// The fake worker, and the two-namespace factory
// ---------------------------------------------------------------------------

/**
 * FakeWorker is the layout protocol without a browser. It answers **every**
 * layout as it is posted, synchronously, so a case never waits on a reply it has
 * not decided on -- and `placements` is then the whole observable the first case
 * needs: how many times the engine was asked to lay this picture out.
 */
class FakeWorker implements WorkerLike {
  readonly sent: Record<string, unknown>[] = []
  terminated = false
  private readonly listeners = new Map<string, ((event: { data?: unknown; message?: string }) => void)[]>()

  /** placements counts the layouts this worker was actually asked to run. */
  get placements(): number {
    return this.sent.filter((message) => message['cmd'] === 'layout').length
  }

  postMessage(message: unknown): void {
    const request = message as Record<string, unknown>
    this.sent.push(request)
    if (request['cmd'] === 'layout') this.reply(request)
  }

  addEventListener(type: string, listener: (event: { data?: unknown; message?: string }) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  terminate(): void {
    this.terminated = true
  }

  /** reply places every node the request asked about and routes every edge: the
   * client refuses a reply that leaves one out, so a fake answering a subset
   * would refuse every layout rather than draw one. */
  private reply(request: Record<string, unknown>): void {
    const graph = request['graph'] as {
      readonly children: readonly { readonly id: string; readonly width: number; readonly height: number }[]
      readonly edges: readonly { readonly id: string }[]
    }
    const data = {
      children: graph.children.map((child, index) => ({
        id: child.id,
        x: index * 300,
        y: 0,
        width: child.width,
        height: child.height,
      })),
      edges: graph.edges.map((edge) => ({
        id: edge.id,
        sections: [{ startPoint: { x: 0, y: 0 }, endPoint: { x: 1, y: 1 } }],
      })),
    }
    for (const listener of this.listeners.get('message') ?? []) {
      listener({ data: { id: request['id'], data } })
    }
  }
}

/** NodeSurface is one fake node as a `ScreenElement`: a text slot and
 * attributes. It is `entry.ts`'s `domSurface`, in a test. */
class NodeSurface implements ScreenElement {
  constructor(readonly node: FakeNode) {}

  get tag(): string {
    return this.node.tagName.toLowerCase()
  }

  get textContent(): string | null {
    return this.node.textContent
  }

  set textContent(value: string | null) {
    this.node.textContent = value
  }

  setAttribute(name: string, value: string): void {
    this.node.setAttribute(name, value)
  }

  getAttribute(name: string): string | null {
    return this.node.getAttribute(name)
  }

  removeAttribute(name: string): void {
    this.node.removeAttribute(name)
  }

  append(child: ScreenElement): void {
    this.node.appendChild((child as NodeSurface).node)
  }

  childNodes(): readonly ScreenElement[] {
    return this.node.children.map((child) => new NodeSurface(child))
  }
}

/** Surface is the two-namespace factory: `create` for the console's own
 * elements and `createNS` for the picture's geometry. */
class Surface implements GraphFactory {
  constructor(private readonly document: FakeDocument) {}

  create(tag: string): ScreenElement {
    return new NodeSurface(this.document.createElement(tag))
  }

  createNS(tag: string): ScreenElement {
    return new NodeSurface(this.document.createElementNS(SVG_NAMESPACE_URI, tag))
  }
}

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

function decoded(raw: unknown): PlanStatusView {
  const result = decodePlanStatus(raw)
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
}

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
 * handed back so a case can count the asks. */
function screenWith(theme: GraphTheme = 'light'): { screen: GraphScreen; worker: FakeWorker } {
  const document = new FakeDocument()
  const worker = new FakeWorker()
  const screen = new GraphScreen({
    factory: new Surface(document),
    client: new LayoutClient(() => worker),
    theme,
  })
  return { screen, worker }
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
    expect(worker.placements).toBe(1)

    // The frame the Live screen already reads, with one node's stage moved from
    // `implement` to `fix`. The graph is the same graph -- same plans, same
    // tasks, same edges -- so no coordinate changed and the worker must not be
    // asked again.
    const after = await screen.frame([runRow('20261025-121805-aa02', 'fix')])
    expect(worker.placements).toBe(1)

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
    expect(worker.placements).toBe(2)
  })

  it('a selection, a chip, a zoom and the drawing are the screen own and lay nothing out', async () => {
    const { screen, worker } = screenWith()
    await screen.load(FINISHED, RUNS)
    screen.change({ selected: nodeKey(PLAN, 'task-two') })
    screen.change({ view: 'list' })
    screen.change({ zoom: 2 })
    screen.change({ expanded: [CHIP] })
    expect(worker.placements).toBe(1)
  })

  it('narrowing to a stream is a different graph, so it does ask again', async () => {
    const { screen, worker } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    await screen.narrow('example')
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
    const groups = everyNode(mounted(element)).filter((each) => each.getAttribute('data-stream') !== null)
    expect(groups.length).toBeGreaterThan(0)
    expect(groups.every((each) => each.getAttribute('class')?.includes('graph-chip-group'))).toBe(true)
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
    expect(worker.placements).toBe(1)
  })
})

// ---------------------------------------------------------------------------
// The tabs, the zoom and the names
// ---------------------------------------------------------------------------

describe('the graph screen and its navigation', () => {
  it('offers one tab per stream and All, with the chosen one selected and alone in the tab order', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    const tabs = everyNode(mounted(element)).filter((each) => each.getAttribute('role') === 'tab')
    expect(tabs.map((each) => each.getAttribute('data-tab'))).toEqual(['example', '', 'all'])
    expect(tabs.map((each) => each.getAttribute('aria-selected'))).toEqual(['false', 'false', 'true'])
    // The roving tabindex the design's §4.3 gives a tablist: one tab is in the
    // tab order and the rest are not.
    expect(tabs.map((each) => each.getAttribute('tabindex'))).toEqual(['-1', '-1', '0'])
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

  it('names a tab as an attribute value, and starts on All with the canvas up', () => {
    expect(tabKey(null)).toBe('all')
    expect(tabKey('example')).toBe('example')
    expect(tabKey('')).toBe('')
    expect(GRAPH_INITIAL_STATE).toEqual({ tab: null, view: 'graph', selected: null, expanded: [], zoom: 1 })
  })

  it('draws the second plan, which names no stream and no release', async () => {
    const { screen } = screenWith()
    await screen.load(DOCUMENT, RUNS)
    const element = screen.redraw()
    if (element === null) throw new Error('no drawing')
    expect(keys(mounted(element))).toContain(nodeKey(SECOND, 'second-one'))
  })
})
