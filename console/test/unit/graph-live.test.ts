// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Graph's live figures (the graph plan's CG11).
//
// CG10 pinned what a **read** draws: the coordinator scope's sources, its nodes
// grouped by release and its words. This file is the other half -- what the
// screen does when the documents keep arriving. The console's feed is
// `graph --watch --json` (CG9b's `graph.watch`, whose documents are **whole
// snapshots** rather than deltas, D1360), and the entry hands each one to
// `GraphScreen.loadScope` in the frame it lands in; the one-shot `graph.read`
// goes through the same door, which is why it is both the first paint and the
// fallback when the child refuses.
//
// So the screen is a function of a **sequence** of documents, and three things
// follow from that, each one case here:
//
//   - a snapshot repaints the section and nothing else (the figures are the new
//     document's, no picture is laid out, and the scope in hand does not move);
//   - a source that answered with nothing this time keeps the figures the last
//     read drew for it, drawn as **kept** -- a down scope is a state, never an
//     empty panel (H3), and a reader must see "the scope is down" beside "what it
//     last said";
//   - the figures survive the child, because the console loses the stream and
//     never the picture: the read that answers next repaints the same section
//     through the same door.
//
// Everything runs against `test/support/fakeDom.ts` and the answering layout
// worker, exactly as `graph.test.ts` does: the screen is handed decoded views and
// the documents here are written as the wire has them, through the real decoder.

import { describe, expect, it } from 'vitest'

import { GRAPH_DOCUMENT_VERSION, decodeGraphDocument } from '../../src/shared/schema'
import type { GraphView } from '../../src/shared/schema'
import { GRAPH_SCOPE_KEPT } from '../../src/renderer/graph/model'
import { STAMP_LEAD, stampOf } from '../../src/renderer/section'
import { GRAPH_SCOPE_ALL, GraphLayoutClient, GraphScreen } from '../../src/renderer/screens/graph'
import type { GraphTheme, ScreenElement } from '../../src/renderer/screens/graph'
import { FakeDocument, everyNode } from '../support/fakeDom'
import type { FakeNode } from '../support/fakeDom'
import { FakeWorker, NodeSurface, Surface } from '../support/graphHarness'

/** liveScreen is a screen over the fake DOM and the answering worker, with the
 * worker in hand so a case can say how many layouts the drawing asked for. */
function liveScreen(theme: GraphTheme = 'light'): { screen: GraphScreen; worker: FakeWorker } {
  const document = new FakeDocument()
  const worker = new FakeWorker()
  const screen = new GraphScreen({
    factory: new Surface(document),
    client: new GraphLayoutClient(() => worker),
    theme,
  })
  return { screen, worker }
}

/** one is a node as the wire has it: the fields a coordinator document always
 * carries, with `updated` null -- no case here is about a node's own stamp. */
function one(
  key: string,
  source: string,
  kind: string,
  title: string,
  state: string,
  release: string,
): Readonly<Record<string, unknown>> {
  const id = key.slice(source.length + 1)
  return { key, source, kind, id, title, repo: 'acme/widgets', state, release, updated: null }
}

/** wire is a `graph --json` document as the wire has it: one stream, edges the
 * source has none of, and the warnings the read makes. `stream_id` is non-empty
 * for every document here, because these are the feed's. */
function wire(
  seq: number,
  sources: readonly Readonly<Record<string, unknown>>[],
  nodes: readonly Readonly<Record<string, unknown>>[],
  stream = 'stream-1',
): GraphView {
  return view({
    version: GRAPH_DOCUMENT_VERSION,
    stream_id: stream,
    seq,
    generated_at: '2026-11-09T12:00:00Z',
    sources,
    nodes,
    edges: [],
    warnings: [],
  })
}

/** view is a document through the real decoder: the screen is a function of the
 * decoded view, and a hand-built one would not be what it is handed. */
function view(raw: Readonly<Record<string, unknown>>): GraphView {
  const result = decodeGraphDocument(raw, 'graph.watch')
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
}

/** up, down and stale are the three source rows these cases use: the source's own
 * name, the state it reported, the as-of stamp it gave (`null` where it gave
 * none) and the reason beside it. */
function up(source: string, asOf: string | null): Readonly<Record<string, unknown>> {
  return { source, state: 'up', as_of: asOf, reason: '' }
}

function down(source: string, reason: string): Readonly<Record<string, unknown>> {
  return { source, state: 'down', as_of: null, reason }
}

function stale(source: string, asOf: string): Readonly<Record<string, unknown>> {
  return { source, state: 'stale', as_of: asOf, reason: 'the sweep is older than twice its period' }
}

const PULL = 'coordinator:acme/widgets#42'
const TASK = 'orchestrator:o1:t-042'
const RUN = 'coordinator:acme/widgets#20261109-090000-ab12'

/** PAINTED is the one-shot read's document: the coordinator alone, with the pull
 * request this installation's whole picture is built around. */
const PAINTED = wire(0, [up('coordinator', '2026-11-09T11:55:00Z')], [one(PULL, 'coordinator', 'pull_request', 'Tidy the widget loader', 'open', 'v1.4.0')], '')

/** FIRST is the feed's first snapshot: the same pull request, and the
 * orchestrator beside it with a task of its own. */
const FIRST = wire(1, [up('coordinator', '2026-11-09T11:59:00Z'), up('orchestrator:o1', '2026-11-09T11:58:00Z')], [
  one(PULL, 'coordinator', 'pull_request', 'Tidy the widget loader', 'open', 'v1.4.0'),
  one(TASK, 'orchestrator:o1', 'task', 'wire the loader', 'running', 'v1.4.0'),
])

/** NEXT is the same two sources one change later: both states moved, and every
 * row of the section is this document's own. */
const NEXT = wire(2, [up('coordinator', '2026-11-09T12:00:00Z'), up('orchestrator:o1', '2026-11-09T12:00:00Z')], [
  one(PULL, 'coordinator', 'pull_request', 'Tidy the widget loader, for real', 'merged', 'v1.4.0'),
  one(TASK, 'orchestrator:o1', 'task', 'wire the loader', 'succeeded', 'v1.4.0'),
])

/** QUIET is the snapshot that says the orchestrator stopped answering: it is a
 * state of the source and not an empty scope, and it draws **no** node for it. */
const QUIET = wire(3, [up('coordinator', '2026-11-09T12:01:00Z'), down('orchestrator:o1', 'the stream was withdrawn')], [
  one(PULL, 'coordinator', 'pull_request', 'Tidy the widget loader', 'open', 'v1.4.0'),
])

/** STALE is the other half of the same rule: this source **did** answer, with
 * figures older than they look, so its node is the document's own. */
const STALE = wire(4, [up('coordinator', '2026-11-09T12:02:00Z'), stale('orchestrator:o1', '2026-11-09T11:00:00Z')], [
  one(PULL, 'coordinator', 'pull_request', 'Tidy the widget loader', 'open', 'v1.4.0'),
  one(TASK, 'orchestrator:o1', 'task', 'wire the loader, from the cache', 'running', 'v1.4.0'),
])

/** READ_AGAIN is the read that answers after the feed is gone: the coordinator
 * again, with the run joined to the pull request this time. */
const READ_AGAIN = wire(0, [up('coordinator', '2026-11-09T12:03:00Z')], [
  one(PULL, 'coordinator', 'pull_request', 'Tidy the widget loader', 'open', 'v1.4.0'),
  one(RUN, 'coordinator', 'run', 'the loader rewrite', 'succeeded', 'v1.4.0'),
], '')

/** drawing is the screen's current drawing, mounted so its elements can be
 * walked. */
function drawing(screen: GraphScreen): FakeNode {
  const element: ScreenElement | null = screen.redraw()
  if (element === null) throw new Error('the screen drew nothing')
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

/** classedIn is every element of one exact class under a node, in document
 * order, and the root itself when it carries the class. */
function classedIn(root: FakeNode, name: string): readonly FakeNode[] {
  return everyNode(root).filter((each) => (each.getAttribute('class') ?? '').split(/\s+/).includes(name))
}

/** rowOf is one drawn node of a coordinator drawing, by its key. */
function rowOf(root: FakeNode, key: string): FakeNode {
  const row = classedIn(root, 'graph-scope-node').find((each) => each.getAttribute('data-key') === key)
  if (row === undefined) throw new Error(`the coordinator scope drew no node for ${key}`)
  return row
}

/** sourceOf is one drawn source row, by the source it names. */
function sourceOf(root: FakeNode, source: string): FakeNode {
  const row = classedIn(root, 'graph-source').find((each) => each.getAttribute('data-source') === source)
  if (row === undefined) throw new Error(`the coordinator scope drew no source row for ${source}`)
  return row
}

/** wordsOf is the text every word cell of a row draws, in document order. */
function wordsOf(row: FakeNode): readonly string[] {
  return classedIn(row, 'graph-word').map((each) => each.textContent ?? '')
}

describe('the Graph and the feed', () => {
  it('a snapshot repaints the section only', () => {
    const { screen, worker } = liveScreen()
    screen.loadScope(FIRST)
    const first = drawing(screen)
    expect(keys(first)).toEqual([PULL, TASK])
    expect(wordsOf(rowOf(first, TASK))).toEqual(['running'])

    screen.loadScope(NEXT)
    const next = drawing(screen)
    // **The section is what was repainted**: the same element, the scope in hand
    // where it was, and every figure under it this document's own -- a snapshot
    // is a whole picture (D1360), so nothing of the one before survives in it.
    expect(next.tagName).toBe('SECTION')
    expect((next.getAttribute('class') ?? '').split(/\s+/)).toContain('graph')
    expect(screen.current.scope).toBe(GRAPH_SCOPE_ALL)
    expect(keys(next)).toEqual([PULL, TASK])
    expect(classedIn(next, 'graph-scope-title').map((each) => each.textContent)).toEqual([
      'Tidy the widget loader, for real',
      'wire the loader',
    ])
    expect(wordsOf(rowOf(next, PULL))).toEqual(['merged'])
    expect(wordsOf(rowOf(next, TASK))).toEqual(['succeeded'])
    expect(classedIn(next, 'graph-scope-kept')).toHaveLength(0)

    // **And nothing was laid out, twice over**: the coordinator scope holds no
    // edges (F17), so neither the first paint nor a later snapshot is a reason to
    // start an engine.
    expect(worker.placements).toBe(0)
  })

  it('a down source shows its state and keeps the last figures dimmed', () => {
    const { screen } = liveScreen()
    screen.loadScope(FIRST)
    screen.loadScope(QUIET)
    const drawn = drawing(screen)

    // The source is a **state with words**, drawn beside the reason it gave and
    // no as-of at all: a down source names no time of its own (`as_of: null`).
    const quiet = sourceOf(drawn, 'orchestrator:o1')
    expect(quiet.getAttribute('data-state')).toBe('down')
    expect(wordsOf(quiet)).toEqual(['down'])
    expect(classedIn(quiet, 'graph-source-reason').map((each) => each.textContent)).toEqual([
      'the stream was withdrawn',
    ])
    expect(classedIn(quiet, 'graph-source-asof')).toHaveLength(0)

    // **The last figures stay, and they say they are the last.** Blanking this
    // row would be the panel agreeing with a source that said nothing, which is
    // the reading H3 refuses; a reader must see the state beside what it last
    // said, in words and not in a tone alone (H10).
    expect(keys(drawn)).toEqual([PULL, TASK])
    const kept = rowOf(drawn, TASK)
    expect(kept.getAttribute('data-kept')).toBe('true')
    expect(classedIn(kept, 'graph-scope-kept').map((each) => each.textContent)).toEqual([GRAPH_SCOPE_KEPT])
    expect(classedIn(kept, 'graph-scope-title')[0]?.textContent).toBe('wire the loader')
    expect(wordsOf(kept)).toEqual(['running'])
    // The kept figure keeps its release too: it is drawn in the group the read
    // that carried it put it in, and not in a group of its own.
    expect(classedIn(drawn, 'graph-scope-release').map((each) => each.getAttribute('data-release'))).toEqual(['v1.4.0'])
    // The coordinator answered with its own node, so nothing of its own is kept.
    expect(rowOf(drawn, PULL).getAttribute('data-kept')).toBeNull()

    // A source that is silent again keeps them a second time: the figures a read
    // keeps are kept until the source answers, and never dropped by the next
    // silent snapshot.
    screen.loadScope(QUIET)
    expect(keys(drawing(screen))).toEqual([PULL, TASK])
    expect(rowOf(drawing(screen), TASK).getAttribute('data-kept')).toBe('true')
  })

  it('a stale source draws the document’s own figures, never the kept ones', () => {
    // The other reading of the same state, and the reason the rule is "answered
    // with nothing" rather than "not up": a stale source **does** answer, with
    // stored figures its own `as_of` dates. Keeping the previous ones there would
    // replace newer figures with older ones and draw the result as the last read.
    const { screen } = liveScreen()
    screen.loadScope(FIRST)
    screen.loadScope(STALE)
    const drawn = drawing(screen)

    const row = sourceOf(drawn, 'orchestrator:o1')
    expect(row.getAttribute('data-state')).toBe('stale')
    expect(wordsOf(row)).toEqual(['stale'])
    // And its stamp is drawn, which is what says **how** old the figures are.
    expect(classedIn(row, 'graph-source-asof').map((each) => each.textContent)).toEqual([
      `${STAMP_LEAD} ${stampOf(Date.parse('2026-11-09T11:00:00Z'))}`,
    ])
    const task = rowOf(drawn, TASK)
    expect(task.getAttribute('data-kept')).toBeNull()
    expect(classedIn(task, 'graph-scope-kept')).toHaveLength(0)
    expect(classedIn(task, 'graph-scope-title')[0]?.textContent).toBe('wire the loader, from the cache')
  })

  it('the feed falls back to the read when the child refuses', () => {
    // The one-shot `graph.read` goes through the same door as the feed's
    // documents (the entry's `readScope` calls the same `loadScope` the poll
    // does), which is what makes it both the first paint and the fallback. What
    // the screen owes that arrangement is one thing, and this is it: a document
    // it is handed always answers a drawing of the section, and a refusal it is
    // **not** handed changes nothing at all.
    const { screen, worker } = liveScreen()

    // The first paint, with no feed anywhere: an installation that records no
    // checkout opens on this scope, and the read is what answers.
    screen.loadScope(PAINTED)
    const painted = drawing(screen)
    expect(keys(painted)).toEqual([PULL])

    // The feed's snapshot repaints it.
    screen.loadScope(FIRST)
    const fed = drawing(screen)
    expect(keys(fed)).toEqual([PULL, TASK])
    expect(wordsOf(rowOf(fed, TASK))).toEqual(['running'])

    // **Then the child refuses.** At this level that is the entry having no
    // document to hand the screen: `pollGraph` returns without drawing and leaves
    // the figures where they are, and the cadence that answers a refusal with a
    // read is the entry's own (`readScope`, `maybeRefreshGraph`). So the screen
    // is asked for the drawing it already holds, and it is the same section with
    // the same rows -- the console loses the stream and never the picture.
    expect(keys(drawing(screen))).toEqual([PULL, TASK])
    expect(wordsOf(rowOf(drawing(screen), TASK))).toEqual(['running'])

    // And the read that answers next repaints it, through the same door: the run
    // joined to the pull request is a figure only a read has drawn here.
    screen.loadScope(READ_AGAIN)
    const read = drawing(screen)
    expect(keys(read)).toEqual([PULL, RUN])
    expect(classedIn(read, 'graph-scope-kept')).toHaveLength(0)
    expect(screen.current.scope).toBe(GRAPH_SCOPE_ALL)
    expect(worker.placements).toBe(0)
  })
})
