// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The graph's layout, and the cache that keeps it from happening twice
// (console GV2.3a; the owner's dependency graph spec §3, GD3's ruling (c)).
//
// **Why a cache, and not a call per frame.** The graph screen is redrawn from a
// live feed: `plan status` is re-read on a timer and a watch frame can change a
// node's state several times a minute. A layout is the most expensive thing the
// screen does -- it is a message to a worker, an engine run, and a reply -- and
// almost every redraw changes nothing an engine can see: a task went from
// `running` to `done`, a title gained a word, a stage moved on. None of those
// moves a box, and none of them is in the key below. So **each stream** is
// turned into a **structure hash**, and the same hash answers with the positions
// already computed.
//
// **The hash is a stream's, not a model's, and that is the ruling this module
// implements** (the owner, 2026-10-31). A stream is laid out **on its own** --
// one engine run per stream, over that stream's nodes and its own edges -- and
// the runs are then stacked as rows, in the model's order, with `GRAPH_ROW_GAP`
// between them. The key follows the run: a stream whose plans did not change is
// the same key whatever its neighbours did, so a read that adds a plan to one
// stream costs one layout and not one per stream. **Why the rows are not the
// engine's job** is below (`graphOf`): an engine asked for the whole picture at
// once packs the streams' components two-dimensionally, and a stream whose plans
// are deeper than its neighbour's then has its own plans placed in the
// neighbour's row -- which the band drawn around them, the union of that
// stream's cards, shows as a rectangle laid over another stream's cards.
//
// **Why the rows are cut here and not asked of the engine** (D2900, the owner,
// 2026-11-18). The picture runs top to bottom, so a level of a stream is a row of
// it -- and a level can be wider than the pane the console draws in (a plan of
// twelve tasks that need one task is one level of twelve cards). The engine's
// own width controls do not bound a level in this direction: `elk.layered.
// wrapping` is ignored for it, and the bounds the layerer offers
// (`elk.layered.layering.minWidth.upperBoundOnWidth`,
// `elk.layered.layering.coffmanGraham.layerBound`) are loose -- a bound of four
// came back as rows of five and six. What the engine does honour exactly is its
// layering, so the levels are worked out here, cut into rows of
// `GRAPH_ROW_CARDS`, and each row is **chained** to the one above it with
// phantom edges (`rowGraph`); the engine's layers are then those rows and
// nothing in the picture shows the chain. It is a pure function of the stream's
// nodes and edges, which are exactly what the key below is a serialization of,
// so a cached run's rows are the rows of the structure that was cached.
//
// **What the hash is, and what it is not.** It is a canonical string, not a
// digest: a collision would draw one stream's coordinates over another's, which
// is a wrong picture rather than a slow one, and the string is small enough
// that the honesty costs nothing. Two things make it a serialization rather than
// a concatenation, and both are load-bearing: every part is length-prefixed
// (`part`), so no plan id, task id or version can forge a boundary from the
// inside, and every list writes its own length first, so no boundary can be
// *moved* between two streams (`structureHash` carries the worked example).
// What goes in is everything that decides where a box sits: the box sizes, each
// plan, each node's id and release, and each edge. What stays out is everything
// the engine never sees -- a node's state, stage, mark, pull request number, run
// suffix, its title, how many nodes a bound dropped -- and everything about the
// stream's *place* among the others: the tab, the number of streams and the
// stream's own name are left out, because a run of one stream is handed none of
// them.
//
// **Why the box sizes are in it.** They are two constants today
// (`GRAPH_NODE_WIDTH`, `GRAPH_NODE_HEIGHT`), and they are in the key anyway: a
// size is the one thing about a node the engine is told, so the day a box grows
// for a longer title the key has to change with it, and a key that omitted them
// would silently reuse positions laid out for smaller boxes. Keeping them out
// of the *model* is what keeps a title out of the key.
//
// **Each layout is flat, and that is GV2.2's client, not this module's choice.**
// `LayoutGraph` has one `children` list and no nesting, so a column, a release
// box and a plan are not containers the engine knows about; what reaches it is
// one stream's nodes with a plan-qualified id, and the edges between two of them
// -- the edges of that stream's plans and no others. Two plans may name the same
// task id -- ids are a plan's own -- so `nodeKey` and `edgeKey` below qualify
// each id with the plan and with the length of the part before it, which is what
// makes the join unambiguous whatever characters an id holds.

import { LayoutClient } from './layout-client'
import type {
  LayoutEdge,
  LayoutGraph,
  LayoutNode,
  LayoutRequest,
  PlacedEdge,
  PlacedNode,
  Positions,
  RouteSection,
} from './layout-client'
import { GRAPH_MAX_PLANS, GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH } from './model'
import type { GraphColumn, GraphModel } from './model'

/**
 * CACHE_ENTRIES is how many layouts the cache keeps: **one flat bound, as wide
 * as a model can be**.
 *
 * A model holds at most `GRAPH_MAX_PLANS` plans and a stream has at least one
 * plan, so no picture the console can draw has more streams than that -- and one
 * stream is one entry. At this bound the cache holds any whole picture, whatever
 * was read before it: moving to a one-stream tab and back to All is the one
 * thing a cache over streams is most obviously for, and a bound that followed
 * the width of whatever was asked for last would have the narrower read throw
 * away the picture it came from. The bound is also as small as a bound that
 * holds every picture can be: a stream's entry is that stream's cards and routes
 * and nothing else, so sixty-four of them is not a cache to grow past.
 *
 * What the cache evicts is then only ever a structure no read has asked about
 * since -- the stream a tab left behind, the plan a task was renamed out of.
 */
const CACHE_ENTRIES = GRAPH_MAX_PLANS

/** GRAPH_ID is the graph's own name in the request. The client matches a reply
 * to the request it sent by request id, so one constant is enough. */
const GRAPH_ID = 'graph'

/**
 * GRAPH_ROW_GAP is the room left between two streams' rows.
 *
 * A row is a band (the screen's `bandOf`): the stream's cards, padded by
 * GRAPH_PAD above and below, grown upward by GRAPH_BAND_HEAD for the band's own
 * label. Two rows therefore need GRAPH_PAD * 2 + GRAPH_BAND_HEAD between their
 * card rectangles to keep their bands apart **at all** -- the gap the screen
 * already asks the engine for *within* a stream (`GRAPH_LAYER_GAP`), where the
 * same heading-and-label reasoning applies -- and at exactly that room the two
 * bands touch, edge to edge, which reads as one band with a line across it.
 * GRAPH_ROW_GAP is that room plus one more padding: the 36 px of ground a reader
 * sees between two rows.
 *
 * The number is written here rather than imported from the screen: the screen
 * imports this module (`graphOf`, `nodeKey`), so the band geometry cannot come
 * back the other way without a cycle, and `graph.test.ts` ties the two together
 * -- `GRAPH_ROW_GAP` against `GRAPH_PAD`, `GRAPH_BAND_HEAD` and the gutter a
 * drawn picture actually has.
 */
export const GRAPH_ROW_GAP = 124

/**
 * GRAPH_ROW_CARDS is how many cards one row of a stream carries: as many as the
 * pane the console opens with holds at the card's floor, and one fewer than the
 * number that would not fit it.
 *
 * The pane is the console's own window less the page's own padding -- 1180 px
 * minus 32 px at each side, 1116 px -- and a card drawn at `GRAPH_MIN_SCALE` is
 * 195 px of it. Four cards and the three gaps between them are 978 px of that
 * pane and five are 1239 px, which is 123 px past its edge, so four is the
 * widest row that reads as one row. The arithmetic is written here as the
 * ruling and pinned in `graph.test.ts`, which measures the constants and the
 * drawn picture against that same pane: the window is the main process's
 * (`CONSOLE_WINDOW_SIZE`) and the padding the theme's, and neither can be
 * imported into this module without a dependency pointing the wrong way.
 */
export const GRAPH_ROW_CARDS = 4

/**
 * LAYOUT_OPTIONS is what the engine is asked for: a layered graph whose depth
 * runs **top to bottom**, with its edges routed orthogonally, and the three
 * strategies the row bound is built on.
 *
 * **The direction is D2900's** (the owner, 2026-11-18), which reversed console
 * redesign step 6a's left-to-right canvas: a task sits **above** the tasks that
 * need it, so the layers the engine computes are the rows a person reads down,
 * and the picture is read the way a page is. What step 6a wanted is kept -- a
 * dependency is a run of the picture a reader follows -- and what changed is the
 * axis, because the graph is the one screen whose width grows with its data and
 * a picture read sideways is one the console's window has to be wide for.
 *
 * `elk.edgeRouting` is set explicitly rather than left to the engine's default:
 * the default is a spline per edge, and a dependency's arrow is read as a line
 * that leaves one card's edge and arrives at another's.
 *
 * **The three strategy names are load-bearing, and measured rather than
 * assumed** (the picture's width is a function of them). `LONGEST_PATH_SOURCE`
 * is the layering whose layers are the rows below: it gives every card the
 * longest path of needs that ends at it, which is what the row chain's edges
 * then pin exactly, and the default layerer (`NETWORK_SIMPLEX`) balances a wide
 * level back up into one line and undoes them. `SIMPLE` is the placement that
 * lays a row out as one compact line -- the default spreads a layer's cards
 * apart along the row to straighten its edges, and drew a level of seven cards
 * 5393 units wide where the bound below is 1304. And
 * `elk.separateConnectedComponents: 'false'` keeps the engine from packing the
 * **components** of one stream two-dimensionally: a plan whose tasks need
 * nothing, or a task nothing needs, is a component of its own, and packed that
 * way one row of four such cards came back 2000 units wide.
 */
const LAYOUT_OPTIONS: Readonly<Record<string, string>> = Object.freeze({
  'elk.algorithm': 'layered',
  'elk.direction': 'DOWN',
  'elk.edgeRouting': 'ORTHOGONAL',
  'elk.layered.layering.strategy': 'LONGEST_PATH_SOURCE',
  'elk.layered.nodePlacement.strategy': 'SIMPLE',
  'elk.separateConnectedComponents': 'false',
})

const REQUEST: LayoutRequest = Object.freeze({ layoutOptions: LAYOUT_OPTIONS })

/**
 * structureHash is the key the cache is kept by: everything one stream's
 * *positions* depend on, and nothing else. Two streams with the same hash draw
 * the same boxes in the same places inside their own run, whatever their text
 * says and whatever they are called.
 *
 * It is **injective over structures**, and that takes two things, not one. The
 * length prefix (`part`) stops a value from forging a boundary from the inside;
 * the **counts** stop a boundary from being *moved*. Lengths alone do not make
 * the sequence self-delimiting, because the parts are a flat run of values with
 * nothing saying where one level ends and the next begins: a plan `p` holding
 * one node whose id and release are both `1.4`, followed by an empty plan `1.4`,
 * is the same run of parts as plan `p` empty followed by plan `1.4` holding that
 * node -- and the two draw different pictures. A cache hit would then hand one
 * stream the other's positions, and the screen would find no entry for its own
 * node key. So every list writes its own length first (plans in the stream, and
 * nodes and edges in a plan), and the run is a serialization rather than a
 * concatenation.
 */
export function structureHash(column: GraphColumn): string {
  const parts: string[] = [
    part(String(GRAPH_NODE_WIDTH)),
    part(String(GRAPH_NODE_HEIGHT)),
    part(String(column.plans.length)),
  ]
  for (const plan of column.plans) {
    parts.push(part(plan.id), part(String(plan.nodes.length)), part(String(plan.edges.length)))
    for (const node of plan.nodes) parts.push(part(node.id), part(node.release))
    for (const edge of plan.edges) parts.push(part(edge.from), part(edge.to))
  }
  return parts.join('')
}

/** part is one length-prefixed piece of the hash: `3:cli`. The length is what
 * makes the join unambiguous -- without it, a plan called `ab` and a node called
 * `c` hash exactly like a plan called `a` and a node called `bc`. */
function part(text: string): string {
  return `${String(text.length)}:${text}`
}

/**
 * GraphLayout lays a model out: **one engine run per stream**, and the runs
 * stacked as rows. The cache is kept per stream, so a read that changes one
 * stream's plans lays that stream out again and reuses every other stream's
 * positions.
 *
 * It takes a `LayoutClient` rather than building one, so a test drives it with
 * the same fake worker the client's own test uses and no browser; the screen
 * owns the real one, and with it the decision to start a worker at all.
 */
export class GraphLayout {
  private readonly cache = new Map<string, Promise<Positions>>()
  private disposed = false

  constructor(private readonly client: LayoutClient) {}

  /**
   * positions resolves with the whole picture's positions -- every node placed,
   * every edge routed -- for the model's own streams and their order.
   *
   * The runs are asked for **together**: they are independent (each is handed
   * one stream and no knowledge of the others), so waiting for the first before
   * asking the second would only add the worker's latency up. A cache hit is
   * returned as it is, and the hit moves the entry to the end so the eviction at
   * the end of this method takes the entry nobody has asked about for longest --
   * and this read's own entries last of all, they being the ones just asked
   * about. A **rejected**
   * run is dropped from the cache: a worker that answered something undrawable
   * has not laid anything out, and keeping the rejection would refuse a stream
   * that a second attempt might place.
   */
  positions(model: GraphModel): Promise<Positions> {
    if (this.disposed) return Promise.reject(new Error('this layout was disposed of'))
    // The eviction is the **read's** and runs once the pass is over, with the
    // cache's own flat bound on it (`CACHE_ENTRIES`): every entry this pass
    // placed or found is now the cache's most recent, so the oldest-first
    // eviction below takes the streams this read did not ask about and never one
    // it is about to draw. Evicting per stream instead -- between two streams of
    // the same picture -- evicts what the read has not reached yet, and a picture
    // that is one stream narrower than the last walks its own eviction down the
    // whole list: every stream is laid out again, one run per stream, for a read
    // that should cost one. A bound that followed the width of *this* picture
    // would be the same mistake one read later: a one-stream tab read after a
    // wide picture would trim away every stream but its own, and going back to
    // All would lay the whole picture out again.
    const rows = model.columns.map((column) => this.row(column))
    this.evict()
    return Promise.all(rows).then((laid) => stackStreams(model.columns, laid))
  }

  /** dispose drops the cache and the client with it: the graph view is rebuilt
   * from a fresh engine rather than reused after a teardown. */
  dispose(): void {
    this.disposed = true
    this.cache.clear()
    this.client.dispose()
  }

  /** row is one stream's own layout: the run at `structureHash(column)`, in the
   * stream's own coordinates. The engine starts every stream at 0, 0 and knows
   * nothing of the rows below it -- placing the stream among the others is
   * `stackStreams`' work, and it is done per call rather than cached. */
  private row(column: GraphColumn): Promise<Positions> {
    const hash = structureHash(column)
    const cached = this.cache.get(hash)
    if (cached !== undefined) {
      this.cache.delete(hash)
      this.cache.set(hash, cached)
      return cached
    }
    const pending = this.client.layout(rowGraph(column), REQUEST)
    this.cache.set(hash, pending)
    // The rejection is handled here as well as by the caller: this `catch` is
    // what removes the entry, and a promise nobody awaits must still not report
    // an unhandled rejection -- the other streams of the same picture are still
    // outstanding when one of them refuses.
    pending.catch(() => {
      if (this.cache.get(hash) === pending) this.cache.delete(hash)
    })
    return pending
  }

  private evict(): void {
    while (this.cache.size > CACHE_ENTRIES) {
      const oldest = this.cache.keys().next()
      if (oldest.done === true) return
      this.cache.delete(oldest.value)
    }
  }
}

/**
 * graphOf is **one stream** as ELK's JSON graph: **ids and sizes only**. No
 * title, no stage, no word, no mark reaches the worker -- the engine places
 * rectangles and the screen writes the text into them afterwards, which is the
 * whole of why a hostile title cannot be anything but text on this screen.
 *
 * The stream is the unit because the engine's own packing is two-dimensional:
 * handed every stream at once, it lays their components out in one plane, and a
 * stream whose plans are deeper than its neighbour's has the neighbour's plans
 * placed in its row -- so the band drawn around that stream, which is the union
 * of its cards, covers another stream's cards. One graph per stream is what
 * makes a band a row.
 *
 * It is the stream's **own** graph and nothing more; what the engine is actually
 * handed is `rowGraph`, which is this plus the row chain.
 */
export function graphOf(column: GraphColumn): LayoutGraph {
  const children: LayoutNode[] = []
  const edges: LayoutEdge[] = []
  for (const plan of column.plans) {
    for (const node of plan.nodes) {
      children.push({ id: nodeKey(plan.id, node.id), width: GRAPH_NODE_WIDTH, height: GRAPH_NODE_HEIGHT })
    }
    for (const edge of plan.edges) {
      edges.push({
        id: edgeKey(plan.id, edge.from, edge.to),
        sources: [nodeKey(plan.id, edge.from)],
        targets: [nodeKey(plan.id, edge.to)],
      })
    }
  }
  return { id: GRAPH_ID, children, edges }
}

/** nodeKey is a node's layout id: the plan's id and the task's, with the plan's
 * length in front of it. The length is what makes the join unambiguous -- a
 * plan called `ab` with a task called `c` would otherwise be the plan `a` with
 * the task `bc` -- and an id may hold any character, so nothing else in the key
 * is relied on to separate. */
export function nodeKey(plan: string, node: string): string {
  return `${String(plan.length)}:${plan}:${node}`
}

/** edgeKey is an edge's layout id, qualified the same way and for the same
 * reason: two plans of one checkout may hold the same pair of task ids, and the
 * engine's ids must be unique across the graph it is given. */
export function edgeKey(plan: string, from: string, to: string): string {
  return `${String(plan.length)}:${plan}:${String(from.length)}:${from}:${to}`
}

/** ROW_CHAIN_PREFIX is what a row chain's edge ids begin with. An edge key
 * always begins with the plan's own length -- a digit -- so no edge a plan can
 * mint is ever mistaken for one of the chain's, whatever a plan or a task is
 * called. */
const ROW_CHAIN_PREFIX = 'row:'

/**
 * rowGraph is **the graph one stream is laid out from**: `graphOf`'s cards and
 * edges, plus the row chain -- the phantom edges that make the engine's layers
 * the rows the picture is drawn in.
 *
 * The chain's shape is the whole of the mechanism, and it is **exact** rather
 * than approximate: every card of a row is given **one** edge from a card of the
 * row above it (its own index, wrapped round the row above's own count). With
 * `LONGEST_PATH_SOURCE` the engine then gives every card the longest path of
 * needs ending at it, and by induction along the chain that is exactly its row:
 * a card of row 0 has no chain edge into it and no plan edge from a shallower
 * level, and a card of row r has its chain edge from a card of row r - 1 (level
 * r - 1, by the same induction) and plan edges only from shallower levels, whose
 * rows are earlier and whose levels are therefore at most r - 1. One edge per
 * card is what makes it exact, and it is also the cheapest: an edge from every
 * card of a row to every card of the next (measured at the same exactness, in
 * the experiments this shape comes from) costs a route per pair for nothing.
 *
 * **The chain is scaffolding and never a line.** It is in the graph the worker
 * is asked about -- the engine has to be told -- and in no picture:
 * `stackStreams` drops the route the engine answers with, because a card of one
 * row is not a dependency of a card in the next, and a line drawn between the
 * two would read as one.
 */
function rowGraph(column: GraphColumn): LayoutGraph {
  const graph = graphOf(column)
  const rows = rowsOf(column)
  const chain: LayoutEdge[] = []
  rows.forEach((row, at) => {
    const above = rows[at - 1]
    if (above === undefined || above.length === 0) return
    row.forEach((id, index) => {
      const from = above[index % above.length]
      if (from === undefined) return
      chain.push({ id: `${ROW_CHAIN_PREFIX}${String(at)}:${id}`, sources: [from], targets: [id] })
    })
  })
  return { id: graph.id, children: graph.children, edges: [...graph.edges, ...chain] }
}

/**
 * rowsOf is **one stream's cards as the rows they are drawn in**: the stream's
 * own levels, each cut into runs of at most `GRAPH_ROW_CARDS` cards, in the
 * model's own order -- the order the plans and their tasks are read in, so a row
 * is a run of the list a person was already reading rather than a set the engine
 * chose.
 *
 * A row holds cards of **one level and no other**, which is what keeps the
 * picture a reader can follow: level 0 under nothing, level 1 under it, and a
 * second row of a level below the first. The alternative -- cutting the whole
 * run of cards into fours and letting a row hold the tail of one level and the
 * head of the next -- is one row of cards that do not belong together, and a
 * dependency whose two ends are in it is then drawn as a line between two cards
 * of the same row.
 */
function rowsOf(column: GraphColumn): readonly (readonly string[])[] {
  const order: string[] = []
  const needs = new Map<string, string[]>()
  for (const plan of column.plans) {
    for (const node of plan.nodes) {
      const key = nodeKey(plan.id, node.id)
      order.push(key)
      needs.set(key, [])
    }
  }
  // A plan's `needs` name tasks of that plan, and the model drops a need that
  // names none of them, so both ends of every edge are cards of this stream --
  // the two guards below are what keeps that a guarantee of this walk rather
  // than an assumption about the document.
  const laid = new Set(order)
  for (const plan of column.plans) {
    for (const edge of plan.edges) {
      const to = needs.get(nodeKey(plan.id, edge.to))
      const from = nodeKey(plan.id, edge.from)
      if (to === undefined || !laid.has(from)) continue
      to.push(from)
    }
  }

  const levels = levelsIn(order, needs)
  const sorted = order
    .map((key, at) => ({ key, at, level: levels.get(key) ?? 0 }))
    .sort((one, two) => one.level - two.level || one.at - two.at)
  const rows: string[][] = []
  let row: string[] = []
  let level = Number.NaN
  for (const card of sorted) {
    if (card.level !== level || row.length === GRAPH_ROW_CARDS) {
      row = []
      rows.push(row)
      level = card.level
    }
    row.push(card.key)
  }
  return rows
}

/** levelsIn is each card's **level**: the longest run of needs that ends at it,
 * counted in cards. A card nothing leads to is level 0, and a card every task of
 * a plan waits on is level 0 too, which is what makes a level the rows a reader
 * reads down.
 *
 * It is the walk the engine's `LONGEST_PATH_SOURCE` layerer makes, done here
 * because the rows are cut before the engine is asked anything -- and because
 * the engine's answer is then the rows this call decided, the two have to agree
 * on what a level is (the options above name that layerer for exactly this).
 *
 * **A cycle is broken rather than followed**: a document with one is refused
 * where it is read, and this walk has to answer whatever it is handed, so the
 * edge that would close a cycle in progress is read as no edge at all.
 */
function levelsIn(
  order: readonly string[],
  needs: ReadonlyMap<string, readonly string[]>,
): ReadonlyMap<string, number> {
  const level = new Map<string, number>()
  const walking = new Set<string>()
  const walk = (key: string): number => {
    const found = level.get(key)
    if (found !== undefined) return found
    if (walking.has(key)) return 0
    walking.add(key)
    let deepest = 0
    for (const from of needs.get(key) ?? []) deepest = Math.max(deepest, walk(from) + 1)
    walking.delete(key)
    level.set(key, deepest)
    return deepest
  }
  for (const key of order) walk(key)
  return level
}

/**
 * stackStreams is the picture from the streams' own runs: each stream's row of
 * cards, one below the last in the model's own order, with GRAPH_ROW_GAP between
 * two rows.
 *
 * A row is translated twice, and both moves are the stacking's: the stream is
 * **pulled to the left edge** (its own leftmost card, so every row starts at the
 * same x whatever the engine put before it) and **moved down** to the row's top
 * (its own topmost card, so a run whose nodes start at y = 12 -- the engine's own
 * margin -- does not indent the row). Every route of that stream is moved with
 * it: an edge's coordinates come from the same run as the cards it joins.
 *
 * The rows are the model's columns **in order**, and a stream with nothing
 * placed takes no row: a column whose plans hold no card is a column with no
 * band (`bandOf` answers null), so leaving it a gap would be ground between two
 * rows that belongs to neither.
 */
function stackStreams(columns: readonly GraphColumn[], laid: readonly Positions[]): Positions {
  const nodes: PlacedNode[] = []
  const edges: PlacedEdge[] = []
  const placed = new Map<string, PlacedNode>()
  const routed = new Set<string>()
  let top = 0
  columns.forEach((column, at) => {
    const row = laid[at] ?? { nodes: [], edges: [] }
    if (row.nodes.length === 0) return
    const left = Math.min(...row.nodes.map((node) => node.x))
    const above = Math.min(...row.nodes.map((node) => node.y))
    const bottom = Math.max(...row.nodes.map((node) => node.y + node.height))
    for (const node of row.nodes) {
      const there = { id: node.id, x: node.x - left, y: node.y - above + top, width: node.width, height: node.height }
      nodes.push(there)
      placed.set(node.id, there)
    }
    for (const edge of row.edges) {
      // The row chain is scaffolding (`rowGraph`): it made the rows and is not a
      // dependency of the model, so its route is dropped rather than drawn.
      if (edge.id.startsWith(ROW_CHAIN_PREFIX)) continue
      routed.add(edge.id)
      edges.push({ id: edge.id, sections: edge.sections.map((section) => moved(section, left, above - top)) })
    }
    top += bottom - above + GRAPH_ROW_GAP
  })
  // An edge the engine never saw: its two ends were laid out in different rows,
  // and nothing has routed it (see `crossStreamRoute`).
  for (const edge of modelEdges(columns)) {
    if (routed.has(edge.id)) continue
    const from = placed.get(edge.from)
    const to = placed.get(edge.to)
    if (from === undefined || to === undefined) continue
    edges.push({ id: edge.id, sections: crossStreamRoute(from, to) })
  }
  return { nodes, edges }
}

/** moved is one route section as the row's translation moves it: the stream's own
 * coordinates are shifted by `dx` to the left and by `dy` upward -- `left` and
 * `above - top`, the two moves `stackStreams` makes to the cards themselves. */
function moved(section: RouteSection, dx: number, dy: number): RouteSection {
  const point = (at: { readonly x: number; readonly y: number }): { x: number; y: number } => ({
    x: at.x - dx,
    y: at.y - dy,
  })
  return {
    startPoint: point(section.startPoint),
    endPoint: point(section.endPoint),
    ...(section.bendPoints === undefined ? {} : { bendPoints: section.bendPoints.map(point) }),
  }
}

/**
 * crossStreamRoute is the line drawn for an edge whose two ends were laid out in
 * **different rows**. No run saw such an edge -- each is handed one stream's
 * nodes and the edges of that stream's plans -- so the route between the two
 * cards is this module's rather than the engine's.
 *
 * It is a simple orthogonal route: three segments, out of the upper card's
 * bottom and into the lower card's top, meeting halfway between the two cards.
 * It leaves and arrives on the facing edges because that is what the picture
 * already reads as -- the engine's own edges are orthogonal, and the rows are
 * stacked.
 *
 * **That midpoint is a gutter only when the two rows are neighbours.** Two cards
 * with one row between them bend on the ground between those rows; two cards
 * three rows apart bend at the midpoint between *them*, which is inside the row
 * in between, so that run is drawn across that row's cards. This function is
 * handed two rectangles and no rows, so it cannot avoid them: routing a long edge
 * through the gutters, gutter by gutter, is GV4.1's work and not this round's,
 * and the day that slice lands this is one of the functions it rewrites.
 *
 * **A model this console can build has no such edge today.** A plan's `needs`
 * name tasks of that plan (the decoder's own reading), a plan belongs to one
 * stream, and the layout's columns are the streams, so an edge's two ends are
 * always in one row -- `docs/console-dependency-graph/reconciliation.md`, F4,
 * "no cross-plan edge is possible today", and `implementation-plan.md` GV4.1 is
 * the blocked slice that would add them. The route is here because **the thing
 * that changed is what makes it necessary**: the two ends are no longer placed
 * by one engine run, so the day an edge can cross a plan boundary, no engine
 * would have routed it and the picture would simply lose the arrow.
 *
 * The two cards are always in **different rows**, and that is the caller's
 * guarantee rather than a check here: `stackStreams` marks every edge a run
 * answered as routed and hands this function only what is left, and an edge one
 * run answered has both its ends in that run -- so the pair handed in is a pair
 * from two rows, whose ground (`GRAPH_ROW_GAP`, and the two runs' own margins)
 * is between them. How many rows lie between the two, this route does not ask --
 * see the midpoint above.
 */
export function crossStreamRoute(from: PlacedNode, to: PlacedNode): readonly RouteSection[] {
  const down = to.y >= from.y + from.height
  const start = { x: from.x + from.width / 2, y: down ? from.y + from.height : from.y }
  const end = { x: to.x + to.width / 2, y: down ? to.y : to.y + to.height }
  const middle = (start.y + end.y) / 2
  return [
    {
      startPoint: start,
      endPoint: end,
      bendPoints: [
        { x: start.x, y: middle },
        { x: end.x, y: middle },
      ],
    },
  ]
}

/** modelEdges is every edge the model draws, as the id it is routed under and
 * the two node keys its ends were placed as. */
function modelEdges(
  columns: readonly GraphColumn[],
): readonly { readonly id: string; readonly from: string; readonly to: string }[] {
  const found: { id: string; from: string; to: string }[] = []
  for (const column of columns) {
    for (const plan of column.plans) {
      for (const edge of plan.edges) {
        found.push({
          id: edgeKey(plan.id, edge.from, edge.to),
          from: nodeKey(plan.id, edge.from),
          to: nodeKey(plan.id, edge.to),
        })
      }
    }
  }
  return found
}
