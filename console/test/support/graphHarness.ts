// The dependency graph screen's test doubles: the fake DOM pair the screen draws
// through, and a layout worker that answers every layout as it is posted.
//
// `graph.test.ts` and `graph-motion.test.ts` both draw this screen and both need
// the same three things to do it -- a `ScreenElement` over `fakeDom.ts`, the
// two-namespace factory the screen is built with, and a `WorkerLike` that replies
// rather than waits -- so they live here rather than twice over, once per file.
// The classes are the console's own: `NodeSurface` is `entry.ts`'s `domSurface` in
// a test, and the worker is the layout protocol without a browser.

import { decodePlanStatus } from '../../src/shared/schema'
import type { PlanStatusView } from '../../src/shared/schema'
import type { WorkerLike } from '../../src/renderer/graph/layout-client'
import { SVG_NAMESPACE_URI } from '../../src/renderer/screens/graph'
import type { GraphFactory, ScreenElement } from '../../src/renderer/screens/graph'
import { FakeDocument } from './fakeDom'
import type { FakeNode } from './fakeDom'

/** NodeSurface is one fake node as a `ScreenElement`: a text slot and
 * attributes. It is `entry.ts`'s `domSurface`, in a test. */
export class NodeSurface implements ScreenElement {
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
export class Surface implements GraphFactory {
  constructor(private readonly document: FakeDocument) {}

  create(tag: string): ScreenElement {
    return new NodeSurface(this.document.createElement(tag))
  }

  createNS(tag: string): ScreenElement {
    return new NodeSurface(this.document.createElementNS(SVG_NAMESPACE_URI, tag))
  }
}

/**
 * FakeWorker is the layout protocol without a browser. It answers **every**
 * layout as it is posted, synchronously, so a case never waits on a reply it has
 * not decided on -- and `placements` is then the whole observable a case about
 * the cache needs: how many times the engine was asked to lay this picture out.
 */
export class FakeWorker implements WorkerLike {
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

/** decoded is a plan document as the wire has it, through the real decoder: the
 * screen is a function of the decoded view, so a test that hand-built one would
 * not be testing what the screen is handed. */
export function decoded(raw: unknown): PlanStatusView {
  const result = decodePlanStatus(raw)
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
}
