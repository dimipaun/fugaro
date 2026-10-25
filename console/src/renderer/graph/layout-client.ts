/**
 * layout-client is the renderer's side of the layout worker: it posts ELK's
 * JSON graph -- ids and sizes, never text -- and resolves with the positions.
 *
 * It does not import elkjs. The engine is a file the build copies verbatim into
 * the served root (`elk-worker.min.js`, a pinned devDependency, GV2.2), so the
 * renderer's bundle is unchanged (the bundler refuses a bare specifier) and this
 * client speaks the worker's own protocol over `postMessage` instead.
 *
 * Every reply is validated before a coordinate reaches a transform. The worker
 * is minified third-party code behind one `SERVED_FILES` entry: a truncated, stale
 * or not-yet-registered worker still answers *something*, and a node placed at
 * `NaN` is a blank diagram rather than a failure. A coordinate that is not a
 * finite number, or a node or edge this layout did not ask about, refuses that
 * one layout, with the reason. A reply whose request id this client never sent
 * cannot be matched to any layout, so it drops the worker: every outstanding
 * layout is refused and the worker is not asked again.
 */

export interface LayoutNode {
  readonly id: string
  readonly width: number
  readonly height: number
}

export interface LayoutEdge {
  readonly id: string
  readonly sources: readonly string[]
  readonly targets: readonly string[]
}

/** LayoutGraph is ELK's JSON graph: the boxes, their sizes, and how they join. */
export interface LayoutGraph {
  readonly id: string
  readonly children: readonly LayoutNode[]
  readonly edges: readonly LayoutEdge[]
}

export interface Point {
  readonly x: number
  readonly y: number
}

export interface PlacedNode extends Point {
  readonly id: string
  readonly width: number
  readonly height: number
}

export interface RouteSection {
  readonly startPoint: Point
  readonly endPoint: Point
  readonly bendPoints?: readonly Point[]
}

export interface PlacedEdge {
  readonly id: string
  readonly sections: readonly RouteSection[]
}

/** Positions is what a layout resolves to: every node placed, every edge routed. */
export interface Positions {
  readonly nodes: readonly PlacedNode[]
  readonly edges: readonly PlacedEdge[]
}

/** LayoutRequest is what ELK takes beside the graph: `layoutOptions` is the
 * algorithm's own settings, `options` the worker's. */
export interface LayoutRequest {
  readonly layoutOptions?: Readonly<Record<string, string>>
  readonly options?: Readonly<Record<string, string>>
}

/** WorkerLike is the slice of `Worker` this client uses, so the protocol is
 * testable without a browser and a fake can stand in. */
export interface WorkerLike {
  postMessage(message: unknown): void
  addEventListener(type: string, listener: (event: { data?: unknown; message?: string }) => void): void
  terminate(): void
}

/** WORKER_URL is the worker's name in the served root, relative to the document
 * that starts it: `app://console/elk-worker.min.js` is same-origin, so it runs
 * under the console's unchanged `script-src 'self'`. */
export const WORKER_URL = 'elk-worker.min.js'

const ALGORITHM = 'layered'

/** ReplyRefusal is a bad reply, as opposed to a failed layout: the engine
 * answered, and what it said cannot be drawn. */
class ReplyRefusal extends Error {}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function num(value: unknown, what: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw new ReplyRefusal(`the layout worker answered ${what} = ${String(value)}, which is not a finite number`)
  }
  return value
}

function point(value: unknown, what: string): Point {
  if (!isRecord(value)) throw new ReplyRefusal(`the layout worker answered ${what} as something that is not a point`)
  return { x: num(value.x, `${what}.x`), y: num(value.y, `${what}.y`) }
}

function ids(values: readonly { readonly id: string }[]): ReadonlySet<string> {
  return new Set(values.map((value) => value.id))
}

function expectIds(got: unknown, asked: ReadonlySet<string>, what: string): Record<string, unknown>[] {
  if (!Array.isArray(got)) throw new ReplyRefusal(`the layout worker answered without ${what}`)
  const seen = new Set<string>()
  for (const entry of got) {
    if (!isRecord(entry) || typeof entry.id !== 'string') {
      throw new ReplyRefusal(`the layout worker answered ${what} with an entry that has no id`)
    }
    if (!asked.has(entry.id)) {
      throw new ReplyRefusal(`the layout worker answered about ${entry.id}, which this layout did not ask about`)
    }
    if (seen.has(entry.id)) throw new ReplyRefusal(`the layout worker answered about ${entry.id} twice`)
    seen.add(entry.id)
  }
  for (const id of asked) {
    if (!seen.has(id)) throw new ReplyRefusal(`the layout worker placed no ${id}, which this layout asked about`)
  }
  return got as Record<string, unknown>[]
}

export class LayoutClient {
  private readonly pending = new Map<
    number,
    { readonly resolve: (positions: Positions) => void; readonly reject: (error: Error) => void; readonly graph: LayoutGraph }
  >()
  private readonly registerId: number
  private next: number
  private broken: Error | undefined
  private worker: WorkerLike | undefined

  constructor(private readonly createWorker: () => WorkerLike) {
    this.registerId = 1
    this.next = 1
    const worker = createWorker()
    this.worker = worker
    worker.addEventListener('message', (event) => this.onMessage(event.data))
    worker.addEventListener('error', (event) => {
      this.fail(new Error(`the layout worker failed: ${event.message ?? 'no reason given'}`))
    })
    worker.postMessage({ id: this.registerId, cmd: 'register', algorithms: [ALGORITHM] })
  }

  /** layout resolves with the positions of `graph`, or rejects: either the
   * worker failed, or it answered something this client will not draw. */
  layout(graph: LayoutGraph, request: LayoutRequest = {}): Promise<Positions> {
    if (this.broken !== undefined) return Promise.reject(this.broken)
    const worker = this.worker
    if (worker === undefined) return Promise.reject(new Error('this layout client was terminated'))
    const id = ++this.next
    return new Promise<Positions>((resolve, reject) => {
      this.pending.set(id, { resolve, reject, graph })
      worker.postMessage({
        id,
        cmd: 'layout',
        graph,
        layoutOptions: request.layoutOptions ?? {},
        options: request.options ?? {},
      })
    })
  }

  /** dispose drops the worker and rejects whatever is outstanding: the graph
   * view is rebuilt from a fresh engine rather than reused after a teardown. */
  dispose(): void {
    this.fail(new Error('this layout client was disposed of'))
  }

  private onMessage(data: unknown): void {
    const id = isRecord(data) && typeof data.id === 'number' ? data.id : undefined
    if (id === this.registerId) return
    const entry = id === undefined ? undefined : this.pending.get(id)
    if (entry === undefined) {
      this.fail(new ReplyRefusal(`the layout worker answered id ${String(id)}, which this client did not ask about`))
      return
    }
    this.pending.delete(id as number)
    try {
      entry.resolve(this.positions(entry.graph, data))
    } catch (error) {
      entry.reject(error instanceof Error ? error : new Error(String(error)))
    }
  }

  private positions(graph: LayoutGraph, reply: unknown): Positions {
    if (!isRecord(reply)) throw new ReplyRefusal('the layout worker answered something that is not a reply')
    if ('error' in reply) throw new ReplyRefusal(`the layout worker refused the graph: ${String(reply.error)}`)
    const data = reply.data
    if (!isRecord(data)) throw new ReplyRefusal('the layout worker answered without a graph')
    const nodes = expectIds(data.children, ids(graph.children), 'a position for every node').map((child) => ({
      id: child.id as string,
      x: num(child.x, `${String(child.id)}.x`),
      y: num(child.y, `${String(child.id)}.y`),
      width: num(child.width, `${String(child.id)}.width`),
      height: num(child.height, `${String(child.id)}.height`),
    }))
    const edges = expectIds(data.edges, ids(graph.edges), 'a route for every edge').map((edge) => ({
      id: edge.id as string,
      sections: this.sections(edge),
    }))
    return { nodes, edges }
  }

  private sections(edge: Record<string, unknown>): RouteSection[] {
    const got = edge.sections
    if (got === undefined) return []
    if (!Array.isArray(got)) throw new ReplyRefusal(`the layout worker answered ${String(edge.id)} with sections that are not a list`)
    return got.map((section, index) => {
      const what = `${String(edge.id)}.sections[${index}]`
      if (!isRecord(section)) throw new ReplyRefusal(`the layout worker answered ${what} as something that is not a route`)
      const bends = section.bendPoints
      if (bends !== undefined && !Array.isArray(bends)) {
        throw new ReplyRefusal(`the layout worker answered ${what}.bendPoints as something that is not a list`)
      }
      return {
        startPoint: point(section.startPoint, `${what}.startPoint`),
        endPoint: point(section.endPoint, `${what}.endPoint`),
        ...(bends === undefined
          ? {}
          : { bendPoints: (bends as unknown[]).map((bend, at) => point(bend, `${what}.bendPoints[${at}]`)) }),
      }
    })
  }

  private fail(reason: Error): void {
    if (this.broken === undefined) this.broken = reason
    const worker = this.worker
    this.worker = undefined
    worker?.terminate()
    for (const entry of this.pending.values()) entry.reject(this.broken)
    this.pending.clear()
  }
}
