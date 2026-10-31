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
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH } from './model'
import type { GraphColumn, GraphModel } from './model'

/**
 * GRAPH_LAYOUT_CACHE_ENTRIES is the cache's **floor**. It is a handful and not a
 * hundred: the entries a person actually revisits are the streams they are
 * looking at now and the one they looked at a moment ago, and each entry holds
 * every node of one stream. A cache that grew with the streams a person had ever
 * opened would hold the whole session's pictures to save a layout nobody asks
 * for again.
 *
 * It is a floor and not the whole bound because a read asks for one whole
 * picture and trims the cache once, when it has it (`boundFor`, `positions`): at
 * a flat eight, a nine-stream read would trim away one of the nine streams it
 * has just asked for, so every read of that picture -- a redraw that changed
 * nothing among them -- would lay that stream out again. That is a picture a
 * document the console draws today can already have.
 */
export const GRAPH_LAYOUT_CACHE_ENTRIES = 8

/**
 * boundFor is how many entries the cache keeps for one picture's read: the floor
 * above, or the picture's own number of streams where that is larger.
 *
 * A read asks for its whole picture at once and evicts once, after the pass
 * (`positions`), so the bound has to hold the whole of what that pass has just
 * placed or found: those entries are the cache's most recent, so a bound smaller
 * than the picture would have the eviction take one of the picture's own streams
 * -- and the read after this one would lay that stream out again. Holding the
 * picture is the whole of the rule: a picture wider than the floor keeps every
 * one of its streams, and the next read, whose own bound is its own width,
 * evicts whatever it did not ask about, oldest first.
 */
function boundFor(entries: number, streams: number): number {
  return Math.max(1, entries, streams)
}

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

/** LAYOUT_OPTIONS is what the engine is asked for: a layered graph whose depth
 * runs **left to right**, with its edges routed orthogonally.
 *
 * The direction is the redesign's canvas (console redesign, step 6a): a task
 * sits to the left of the tasks that need it, so the picture is read the way a
 * sentence is, and the layers the engine computes are the columns a person reads
 * across. `elk.edgeRouting` is set explicitly rather than left to the engine's
 * default: the default is a spline per edge, and a dependency's arrow is read as
 * a line that leaves one card's side and arrives at another's. */
const LAYOUT_OPTIONS: Readonly<Record<string, string>> = Object.freeze({
  'elk.algorithm': 'layered',
  'elk.direction': 'RIGHT',
  'elk.edgeRouting': 'ORTHOGONAL',
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

  constructor(
    private readonly client: LayoutClient,
    private readonly entries: number = GRAPH_LAYOUT_CACHE_ENTRIES,
  ) {}

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
    // picture's own bound on it (`boundFor`): every entry this pass placed or
    // found is now the cache's most recent, so the oldest-first eviction below
    // takes the streams this read did not ask about and never one it is about to
    // draw. Evicting per stream instead -- between two streams of the same
    // picture -- evicts what the read has not reached yet, and a picture that is
    // one stream narrower than the last walks its own eviction down the whole
    // list: every stream is laid out again, one run per stream, for a read that
    // should cost one.
    const bound = boundFor(this.entries, model.columns.length)
    const rows = model.columns.map((column) => this.row(column))
    this.evict(bound)
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
    const pending = this.client.layout(graphOf(column), REQUEST)
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

  private evict(bound: number): void {
    while (this.cache.size > bound) {
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
