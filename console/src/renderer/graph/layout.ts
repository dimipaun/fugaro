// The graph's layout, and the cache that keeps it from happening twice
// (console GV2.3a; the owner's dependency graph spec §3, GD3's ruling (c)).
//
// **Why a cache, and not a call per frame.** The graph screen is redrawn from a
// live feed: `plan status` is re-read on a timer and a watch frame can change a
// node's state several times a minute. A layout is the most expensive thing the
// screen does -- it is a message to a worker, an engine run, and a reply -- and
// almost every redraw changes nothing an engine can see: a task went from
// `running` to `done`, a title gained a word, a stage moved on. None of those
// moves a box, and none of them is in the key below. So the model is turned
// into a **structure hash**, and the same hash answers with the positions
// already computed.
//
// **What the hash is, and what it is not.** It is a canonical string, not a
// digest: a collision would draw one graph's coordinates over another's, which
// is a wrong picture rather than a slow one, and the string is small enough
// that the honesty costs nothing. Two things make it a serialization rather than
// a concatenation, and both are load-bearing: every part is length-prefixed
// (`part`), so no plan id, task id, version or stream name can forge a boundary
// from the inside, and every list writes its own length first, so no boundary
// can be *moved* between two graphs (`structureHash` carries the worked example).
// What goes in is everything that decides where a box sits: the tab, the box
// sizes, each plan, each node's id and release, and each edge. What stays out is
// everything the engine never sees -- a node's state, stage, mark, pull request
// number, run suffix, its title, and how many nodes a bound dropped.
//
// **Why the box sizes are in it.** They are two constants today
// (`GRAPH_NODE_WIDTH`, `GRAPH_NODE_HEIGHT`), and they are in the key anyway: a
// size is the one thing about a node the engine is told, so the day a box grows
// for a longer title the key has to change with it, and a key that omitted them
// would silently reuse positions laid out for smaller boxes. Keeping them out
// of the *model* is what keeps a title out of the key.
//
// **The graph is flat, and that is GV2.2's client, not this module's choice.**
// `LayoutGraph` has one `children` list and no nesting, so a column, a release
// box and a plan are not containers the engine knows about; what reaches it is
// every node of the chosen tab with a plan-qualified id, and every edge between
// two of them. Two plans may name the same task id -- ids are a plan's own --
// so `nodeKey` and `edgeKey` below qualify each id with the plan and with the
// length of the part before it, which is what makes the join unambiguous
// whatever characters an id holds.

import { LayoutClient } from './layout-client'
import type { LayoutEdge, LayoutGraph, LayoutNode, LayoutRequest, Positions } from './layout-client'
import { GRAPH_NODE_HEIGHT, GRAPH_NODE_WIDTH } from './model'
import type { GraphModel } from './model'

/**
 * GRAPH_LAYOUT_CACHE_ENTRIES bounds the cache. It is a handful and not a
 * hundred: the entries a person actually revisits are the tabs they are looking
 * at now and the one they looked at a moment ago, and each entry holds every
 * node of a graph. A cache that grew with the tabs a person had ever opened
 * would hold the whole session's pictures to save a layout nobody asks for
 * again.
 */
export const GRAPH_LAYOUT_CACHE_ENTRIES = 8

/** GRAPH_ID is the graph's own name in the request. The client matches a reply
 * to the request it sent by request id, so one constant is enough. */
const GRAPH_ID = 'graph'

/** LAYOUT_OPTIONS is what the engine is asked for: a layered graph, top to
 * bottom, which is the direction the spec's dependency arrows are read in (a
 * task above the tasks that need it). */
const LAYOUT_OPTIONS: Readonly<Record<string, string>> = Object.freeze({
  'elk.algorithm': 'layered',
  'elk.direction': 'DOWN',
})

const REQUEST: LayoutRequest = Object.freeze({ layoutOptions: LAYOUT_OPTIONS })

/**
 * structureHash is the key the cache is kept by: everything the picture's
 * *positions* depend on, and nothing else. Two models with the same hash draw
 * the same boxes in the same places, whatever their text says.
 *
 * It is **injective over structures**, and that takes two things, not one. The
 * length prefix (`part`) stops a value from forging a boundary from the inside;
 * the **counts** stop a boundary from being *moved*. Lengths alone do not make
 * the sequence self-delimiting, because the parts are a flat run of values with
 * nothing saying where one level ends and the next begins: a plan `p` (stream
 * `s`) holding one node whose id and release are both `1.4`, followed by an
 * empty plan `1.4`, is the same run of parts as plan `p` empty followed by plan
 * `1.4` holding that node -- and the two draw different graphs (`1:p:1.4`
 * against `3:1.4:1.4` in `graphOf`). A cache hit would then hand one graph the
 * other's positions, and the screen would find no entry for its own node key. So
 * every list writes its own length first (columns, plans in a column, nodes and
 * edges in a plan), and the run is a serialization rather than a concatenation.
 */
export function structureHash(model: GraphModel): string {
  const parts: string[] = [
    part(model.tab ?? 'all'),
    part(String(GRAPH_NODE_WIDTH)),
    part(String(GRAPH_NODE_HEIGHT)),
    part(String(model.columns.length)),
  ]
  for (const column of model.columns) {
    parts.push(part(column.stream), part(String(column.plans.length)))
    for (const plan of column.plans) {
      parts.push(part(plan.id), part(String(plan.nodes.length)), part(String(plan.edges.length)))
      for (const node of plan.nodes) parts.push(part(node.id), part(node.release))
      for (const edge of plan.edges) parts.push(part(edge.from), part(edge.to))
    }
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
 * GraphLayout lays a model out, once per structure.
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
   * positions resolves with the whole graph's positions -- every node placed,
   * every edge routed -- for the model's structure.
   *
   * A cache hit is returned as it is, and the hit moves the entry to the end so
   * the eviction below takes the entry nobody has asked about for longest. A
   * **rejected** layout is dropped from the cache: a worker that answered
   * something undrawable has not laid anything out, and keeping the rejection
   * would refuse a graph that a second attempt might place.
   */
  positions(model: GraphModel): Promise<Positions> {
    if (this.disposed) return Promise.reject(new Error('this layout was disposed of'))
    const hash = structureHash(model)
    const cached = this.cache.get(hash)
    if (cached !== undefined) {
      this.cache.delete(hash)
      this.cache.set(hash, cached)
      return cached
    }
    const pending = this.client.layout(graphOf(model), REQUEST)
    this.cache.set(hash, pending)
    this.evict()
    // The rejection is handled here as well as by the caller: this `catch` is
    // what removes the entry, and a promise nobody awaits must still not report
    // an unhandled rejection.
    pending.catch(() => {
      if (this.cache.get(hash) === pending) this.cache.delete(hash)
    })
    return pending
  }

  /** dispose drops the cache and the client with it: the graph view is rebuilt
   * from a fresh engine rather than reused after a teardown. */
  dispose(): void {
    this.disposed = true
    this.cache.clear()
    this.client.dispose()
  }

  private evict(): void {
    const bound = Math.max(1, this.entries)
    while (this.cache.size > bound) {
      const oldest = this.cache.keys().next()
      if (oldest.done === true) return
      this.cache.delete(oldest.value)
    }
  }
}

/**
 * graphOf is the model as ELK's JSON graph: **ids and sizes only**. No title, no
 * stage, no word, no mark reaches the worker -- the engine places rectangles and
 * the screen writes the text into them afterwards, which is the whole of why a
 * hostile title cannot be anything but text on this screen.
 */
export function graphOf(model: GraphModel): LayoutGraph {
  const children: LayoutNode[] = []
  const edges: LayoutEdge[] = []
  for (const column of model.columns) {
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
  }
  return { id: GRAPH_ID, children, edges }
}

/** nodeKey is a node's layout id: the plan's id and the task's, with the plan's
 * length in front of it. The length is what makes the join unambiguous -- a
 * plan called `ab` with a task called `c` would otherwise be the plan `a` with
 * the task `bc` -- and an id may hold any character, so nothing else in the key
 * is relied on to separate. */
function nodeKey(plan: string, node: string): string {
  return `${String(plan.length)}:${plan}:${node}`
}

/** edgeKey is an edge's layout id, qualified the same way and for the same
 * reason: two plans of one checkout may hold the same pair of task ids, and the
 * engine's ids must be unique across the graph it is given. */
function edgeKey(plan: string, from: string, to: string): string {
  return `${String(plan.length)}:${plan}:${String(from.length)}:${from}:${to}`
}
