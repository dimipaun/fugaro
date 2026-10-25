import { describe, expect, it } from 'vitest'
import { LayoutClient, WORKER_URL, type LayoutGraph, type WorkerLike } from '../../src/renderer/graph/layout-client.js'

/** FakeWorker is the protocol without a browser: it records what was posted and
 * answers when the test says so, so a reply is a test's decision rather than an
 * engine's. */
class FakeWorker implements WorkerLike {
  readonly sent: unknown[] = []
  terminated = false
  private readonly listeners = new Map<string, ((event: { data?: unknown; message?: string }) => void)[]>()

  postMessage(message: unknown): void {
    this.sent.push(message)
  }

  addEventListener(type: string, listener: (event: { data?: unknown; message?: string }) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  terminate(): void {
    this.terminated = true
  }

  answer(data: unknown): void {
    for (const listener of this.listeners.get('message') ?? []) listener({ data })
  }

  fail(message: string): void {
    for (const listener of this.listeners.get('error') ?? []) listener({ message })
  }
}

const GRAPH: LayoutGraph = {
  id: 'root',
  children: [
    { id: 'a', width: 100, height: 40 },
    { id: 'b', width: 100, height: 40 },
  ],
  edges: [{ id: 'a->b', sources: ['a'], targets: ['b'] }],
}

const PLACED = {
  id: 'root',
  children: [
    { id: 'a', x: 0, y: 0, width: 100, height: 40 },
    { id: 'b', x: 0, y: 60, width: 100, height: 40 },
  ],
  edges: [
    {
      id: 'a->b',
      sections: [{ startPoint: { x: 50, y: 40 }, endPoint: { x: 50, y: 60 }, bendPoints: [{ x: 50, y: 50 }] }],
    },
  ],
}

function client(): { worker: FakeWorker; client: LayoutClient } {
  const worker = new FakeWorker()
  return { worker, client: new LayoutClient(() => worker) }
}

describe('the layout client', () => {
  it('registers the algorithm, sends the graph, and resolves with the positions', async () => {
    const { worker, client: layoutClient } = client()
    const pending = layoutClient.layout(GRAPH, { layoutOptions: { 'elk.algorithm': 'layered' } })
    expect(worker.sent[0]).toEqual({ id: 1, cmd: 'register', algorithms: ['layered'] })
    expect(worker.sent[1]).toEqual({
      id: 2,
      cmd: 'layout',
      graph: GRAPH,
      layoutOptions: { 'elk.algorithm': 'layered' },
      options: {},
    })
    worker.answer({ id: 2, data: PLACED })
    await expect(pending).resolves.toEqual({
      nodes: [
        { id: 'a', x: 0, y: 0, width: 100, height: 40 },
        { id: 'b', x: 0, y: 60, width: 100, height: 40 },
      ],
      edges: [
        {
          id: 'a->b',
          sections: [{ startPoint: { x: 50, y: 40 }, endPoint: { x: 50, y: 60 }, bendPoints: [{ x: 50, y: 50 }] }],
        },
      ],
    })
    expect(WORKER_URL).toBe('elk-worker.min.js')
  })

  it('refuses a node placed at NaN, rather than drawing it', async () => {
    const { worker, client: layoutClient } = client()
    const pending = layoutClient.layout(GRAPH)
    worker.answer({
      id: 2,
      data: { ...PLACED, children: [{ id: 'a', x: Number.NaN, y: 0, width: 100, height: 40 }, PLACED.children[1]] },
    })
    await expect(pending).rejects.toThrow(/a\.x = NaN, which is not a finite number/)
  })

  it('refuses a position for a node this layout did not ask about', async () => {
    const { worker, client: layoutClient } = client()
    const pending = layoutClient.layout(GRAPH)
    worker.answer({
      id: 2,
      data: { ...PLACED, children: [...PLACED.children, { id: 'ghost', x: 0, y: 0, width: 1, height: 1 }] },
    })
    await expect(pending).rejects.toThrow(/answered about ghost, which this layout did not ask about/)
  })

  it('refuses a reply about a request it never made, and drops the worker with it', async () => {
    const { worker, client: layoutClient } = client()
    const pending = layoutClient.layout(GRAPH)
    worker.answer({ id: 99, data: PLACED })
    await expect(pending).rejects.toThrow(/answered id 99, which this client did not ask about/)
    expect(worker.terminated).toBe(true)
    await expect(layoutClient.layout(GRAPH)).rejects.toThrow(/answered id 99/)
  })

  it('refuses the engine\'s own error with its reason', async () => {
    const { worker, client: layoutClient } = client()
    const pending = layoutClient.layout(GRAPH)
    worker.answer({ id: 2, error: 'no algorithm registered' })
    await expect(pending).rejects.toThrow(/refused the graph: no algorithm registered/)
  })

  it('rejects what is still outstanding when it is disposed of', async () => {
    const { worker, client: layoutClient } = client()
    const pending = layoutClient.layout(GRAPH)
    layoutClient.dispose()
    await expect(pending).rejects.toThrow(/disposed of/)
    expect(worker.terminated).toBe(true)
  })
})

describe('the replies it will not draw', () => {
  // Every arm below is a shape a hostile, truncated or stale worker can take.
  // The reconciliation makes "the renderer validates every reply (known ids,
  // finite numbers)" one of GV2.2's security probes, so each refusal is a case
  // rather than a line of code nothing exercises.
  const refused: readonly (readonly [string, unknown, RegExp])[] = [
    ['a node left unplaced', { ...PLACED, children: [PLACED.children[0]] }, /placed no b, which this layout asked about/],
    ['a node placed twice', { ...PLACED, children: [PLACED.children[0], PLACED.children[0]] }, /answered about a twice/],
    [
      'an entry with no id',
      { ...PLACED, children: [{ x: 0, y: 0, width: 1, height: 1 }, PLACED.children[1]] },
      /with an entry that has no id/,
    ],
    ['children that are not a list', { ...PLACED, children: 'a' }, /answered without a position for every node/],
    ['edges that are not a list', { ...PLACED, edges: {} }, /answered without a route for every edge/],
    ['an edge left unrouted', { ...PLACED, edges: [] }, /placed no a->b, which this layout asked about/],
    ['sections that are not a list', { ...PLACED, edges: [{ id: 'a->b', sections: 3 }] }, /sections that are not a list/],
    [
      'bend points that are not a list',
      { ...PLACED, edges: [{ id: 'a->b', sections: [{ startPoint: { x: 0, y: 0 }, endPoint: { x: 0, y: 1 }, bendPoints: 7 }] }] },
      /bendPoints as something that is not a list/,
    ],
    [
      'a start point that is not a point',
      { ...PLACED, edges: [{ id: 'a->b', sections: [{ startPoint: 5, endPoint: { x: 0, y: 1 } }] }] },
      /startPoint as something that is not a point/,
    ],
    [
      'a start point with no x',
      { ...PLACED, edges: [{ id: 'a->b', sections: [{ startPoint: { y: 0 }, endPoint: { x: 0, y: 1 } }] }] },
      /startPoint\.x = undefined, which is not a finite number/,
    ],
    ['data that is not a graph', 'nope', /answered without a graph/],
    ['a width that is a string', { ...PLACED, children: [{ ...PLACED.children[0], width: '100' }, PLACED.children[1]] }, /a\.width = 100, which is not a finite number/],
  ]

  for (const [what, data, message] of refused) {
    it(`refuses ${what}`, async () => {
      const { worker, client: layoutClient } = client()
      const pending = layoutClient.layout(GRAPH)
      worker.answer({ id: 2, data })
      await expect(pending).rejects.toThrow(message)
    })
  }

  it('refuses an edge the engine routed no sections for, by drawing it straight', async () => {
    // The one arm that is *not* a refusal: ELK omits `sections` for an edge it
    // routes as a straight line, and the client draws it from the two points it
    // has rather than inventing a route.
    const { worker, client: layoutClient } = client()
    const pending = layoutClient.layout(GRAPH)
    worker.answer({
      id: 2,
      data: { ...PLACED, edges: [{ id: 'a->b', sections: [{ startPoint: { x: 0, y: 0 }, endPoint: { x: 0, y: 1 } }] }] },
    })
    await expect(pending).resolves.toMatchObject({ edges: [{ id: 'a->b' }] })
  })

  it('refuses a reply for a request this client never made', async () => {
    const { worker, client: layoutClient } = client()
    const pending = layoutClient.layout(GRAPH)
    worker.answer('a string the worker had no business sending')
    await expect(pending).rejects.toThrow(/answered id undefined, which this client did not ask about/)
    expect(worker.terminated).toBe(true)
  })

  it("fails the layout when the worker's own error event fires", async () => {
    const { worker, client: layoutClient } = client()
    const pending = layoutClient.layout(GRAPH)
    worker.fail('boom')
    await expect(pending).rejects.toThrow(/the layout worker failed: boom/)
    await expect(layoutClient.layout(GRAPH)).rejects.toThrow(/the layout worker failed: boom/)
    expect(worker.terminated).toBe(true)
  })
})
