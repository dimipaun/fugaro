// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The graph screen's model and its layout cache (console GV2.3a; the owner's
// dependency graph spec §3 and its addendum §3,
// docs/console-dependency-graph/).
//
// Two things are pinned here, and they are the two the screen task (GV2.3b)
// will consume:
//
//   - **the model** -- `buildGraphModel({document, tab})` -- columns, release
//     boxes, chips, nodes and edges, and the bounds it drops past, asserted
//     against the committed fixture and against documents this file builds;
//   - **the cache** -- `GraphLayout.positions(model)` lays a structure out
//     once, and the first test below is the one that says what "structure"
//     means: a redraw that changed a state does not lay anything out, and a
//     redraw that added an edge does.
//
// Everything runs with a fake `WorkerLike`, the same protocol-without-a-browser
// the layout client's own test uses: no DOM, no engine, and a reply this test
// decides rather than one ELK computes.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { PLAN_STATES, decodePlanStatus } from '../../src/shared/schema'
import type { PlanStatusView } from '../../src/shared/schema'
import type { Sanitized } from '../../src/shared/sanitize'
import {
  GRAPH_LAYOUT_CACHE_ENTRIES,
  GraphLayout,
  crossStreamRoute,
  graphOf,
  structureHash,
} from '../../src/renderer/graph/layout'
import { LayoutClient } from '../../src/renderer/graph/layout-client'
import type { LayoutGraph, PlacedNode, Point, Positions, WorkerLike } from '../../src/renderer/graph/layout-client'
import {
  GRAPH_MAX_NEEDS,
  GRAPH_MAX_NODES,
  GRAPH_MAX_NODES_PER_PLAN,
  GRAPH_MAX_PLANS,
  GRAPH_MAX_TITLE_CODE_POINTS,
  GRAPH_NODE_HEIGHT,
  GRAPH_NODE_WIDTH,
  STATE_WORDS,
  buildGraphModel,
  stateWords,
} from '../../src/renderer/graph/model'
import type { GraphColumn, GraphInputs, GraphModel, GraphNode } from '../../src/renderer/graph/model'

/** FakeWorker is the protocol without a browser: it records what was posted and
 * answers when the test says so. */
class FakeWorker implements WorkerLike {
  readonly sent: Record<string, unknown>[] = []
  terminated = false
  private readonly listeners = new Map<string, ((event: { data?: unknown; message?: string }) => void)[]>()

  postMessage(message: unknown): void {
    this.sent.push(message as Record<string, unknown>)
  }

  addEventListener(type: string, listener: (event: { data?: unknown; message?: string }) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  terminate(): void {
    this.terminated = true
  }

  /** answer replies to the layout the client asked for last, so a reply is
   * matched to the request that was actually sent rather than to a guessed id. */
  answer(data: unknown): void {
    this.answerTo(this.lastId, data)
  }

  /** answerTo replies to the layout with `id`: a picture is one ask per stream,
   * so a case that answered only the last ask would leave the other stream's
   * layout outstanding and hang. */
  answerTo(id: number, data: unknown): void {
    for (const listener of this.listeners.get('message') ?? []) listener({ data: { id, data } })
  }

  /** asked is every layout this worker was asked to run: the graph it was asked
   * about and the id to reply with. */
  get asked(): readonly { readonly id: number; readonly graph: LayoutGraph }[] {
    const found: { id: number; graph: LayoutGraph }[] = []
    for (const message of this.sent) {
      if (message['cmd'] !== 'layout') continue
      const id = message['id']
      if (typeof id !== 'number') throw new Error('a layout with no id')
      found.push({ id, graph: message['graph'] as LayoutGraph })
    }
    return found
  }

  /** placements counts the layouts this worker was actually asked to run. */
  get placements(): number {
    return this.asked.length
  }

  private get lastId(): number {
    const layouts = this.sent.filter((message) => message['cmd'] === 'layout')
    const id = layouts[layouts.length - 1]?.['id']
    if (typeof id !== 'number') throw new Error('this worker was asked for no layout')
    return id
  }
}

/** RAW is `test/fixtures/plan-status.json` as the wire has it, and DOCUMENT is
 * the same document through the real decoder: the model is a function of the
 * decoded view, so a test that hand-built one would not be testing what the
 * screen hands it. The patches below edit the *raw* document, because a patched
 * document is a document and has to survive the decoder like any other. */
const RAW: unknown = JSON.parse(readFileSync(join('test', 'fixtures', 'plan-status.json'), 'utf8'))
const DOCUMENT: PlanStatusView = decoded(RAW)

/** STREAM is the fixture's own stream name, read off the document rather than
 * spelled here. */
const STREAM = DOCUMENT.streams[0]?.name ?? ''

function decoded(raw: unknown): PlanStatusView {
  const result = decodePlanStatus(raw)
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
}

function model(tab: GraphInputs['tab']): GraphModel {
  return buildGraphModel({ document: DOCUMENT, tab })
}

/** text is a drawn string, or the empty string for a refusal. */
function text(value: Sanitized | undefined): string {
  return value !== undefined && value.ok ? value.text : ''
}

/**
 * patched is the fixture document with one path replaced. It is how the bound
 * tests build a document bigger than a bound without a fixture a person has to
 * maintain.
 */
function patched(path: readonly (string | number)[], value: unknown): unknown {
  const copy = JSON.parse(JSON.stringify(RAW)) as Record<string, unknown>
  let at: unknown = copy
  for (const step of path.slice(0, -1)) {
    if (at === null || typeof at !== 'object') throw new Error(`no ${String(step)} in the path`)
    at = (at as Record<string, unknown>)[step]
  }
  const last = path[path.length - 1]
  if (last === undefined || at === null || typeof at !== 'object') throw new Error('path is empty')
  ;(at as Record<string, unknown>)[last] = value
  return copy
}

/** built is the model over a patched document. */
function built(path: readonly (string | number)[], value: unknown, tab: GraphInputs['tab'] = null): GraphModel {
  return buildGraphModel({ document: decoded(patched(path, value)), tab })
}

/** rawPlan is one plan of the fixture **as the wire has it**, a copy: the tests
 * that build a bigger document grow a real plan rather than inventing a shape
 * the decoder would refuse. */
function rawPlan(index: number): Record<string, unknown> {
  const plans = (RAW as { readonly plans: readonly Record<string, unknown>[] }).plans
  const plan = plans[index]
  if (plan === undefined) throw new Error(`the fixture has no plan ${String(index)}`)
  return JSON.parse(JSON.stringify(plan)) as Record<string, unknown>
}

/** rawNodes is a raw plan's own node list, a copy. */
function rawNodes(plan: Record<string, unknown>): readonly Record<string, unknown>[] {
  return JSON.parse(JSON.stringify(plan['nodes'])) as readonly Record<string, unknown>[]
}

/** nodeOf is one node of the first plan of the first column, named by its task
 * id so a test does not depend on the order nodes are drawn in. */
function nodeOf(value: GraphModel, id: string): GraphNode | undefined {
  return value.columns[0]?.plans[0]?.nodes.find((candidate) => candidate.id === id)
}

/** layoutWith is a `GraphLayout` over a fake worker, and the worker, so a test
 * can count the layouts that were asked for. */
function layoutWith(): { worker: FakeWorker; layout: GraphLayout } {
  const worker = new FakeWorker()
  return { worker, layout: new GraphLayout(new LayoutClient(() => worker)) }
}

/** streamOf is one of a model's streams by index. The layout key is a stream's,
 * so a case that compares two models compares one stream of each, and a case
 * about a stream's own geometry lays that stream's graph out. */
function streamOf(value: GraphModel, index: number): GraphColumn {
  const column = value.columns[index]
  if (column === undefined) throw new Error(`the model has no stream ${String(index)}`)
  return column
}

/** bothPlans is the stream holding the two plans a boundary case is about: those
 * cases patch the fixture down to exactly two plans, and the ids a case reads
 * apart are one engine run's ids -- one stream's, not the picture's. */
function bothPlans(value: GraphModel): GraphColumn {
  const column = value.columns.find((each) => each.plans.length === 2)
  if (column === undefined) throw new Error('the model has no stream of two plans')
  return column
}

/**
 * manyStreams is a document of `count` streams of one plan each, through the
 * real decoder: it is how the bound cases read a picture wider than
 * `GRAPH_LAYOUT_CACHE_ENTRIES` without a fixture a person has to maintain. The
 * plans are chains of two tasks, which is the smallest shape that has an edge --
 * a stream with no cards would take no row and no cache entry.
 *
 * `changed` is the stream whose tasks are named differently: the same stream,
 * its own plan, with the ids the layout's own key is read from moved -- so the
 * picture one stream narrower with one of them edited is `manyStreams(count - 1,
 * 0)`.
 */
function manyStreams(count: number, changed = -1): GraphModel {
  const plans: unknown[] = []
  let tasks = 0
  for (let index = 0; index < count; index += 1) {
    const stream = `stream-${String(index)}`
    const edit = index === changed ? '-again' : ''
    const nodes = [0, 1].map((node) => ({
      id: `${stream}-task-${String(node)}${edit}`,
      title: `A task of ${stream}`,
      state: 'ready',
      closed: false,
      level: node,
      critical: false,
      needs: node === 0 ? [] : [`${stream}-task-0${edit}`],
    }))
    tasks += nodes.length
    plans.push({
      id: `plans/2026-10-31-${stream}`,
      artifact: `docs/plans/2026-10-31-${stream}.graph.json`,
      plan: `The ${stream} plan`,
      stream,
      nodes,
      releases: [],
      tasks: nodes.length,
      closed: 0,
      open: nodes.length,
    })
  }
  return buildGraphModel({
    document: decoded({
      repo: 'example-owner/example-repo',
      plans,
      totals: { plans: plans.length, tasks, closed: 0, open: tasks },
      streams: [],
      warnings: [],
    }),
    tab: null,
  })
}

/** lastAsk is the graph of the layout this worker was asked for last, and so
 * what a reply to that ask has to cover. */
function lastAsk(worker: FakeWorker): LayoutGraph {
  const asked = worker.asked[worker.asked.length - 1]
  if (asked === undefined) throw new Error('this worker was asked for no layout')
  return asked.graph
}

/** placed is a reply the client accepts for one stream's own graph: every node
 * of it at a fixed coordinate, and every edge routed with no sections. It is
 * built from the ask rather than from the model, because a picture is one ask
 * per stream and each ask is about a different graph. */
function placed(graph: LayoutGraph): unknown {
  return {
    children: graph.children.map((child, index) => ({ ...child, x: 0, y: index * 10 })),
    edges: graph.edges.map((edge) => ({ id: edge.id, sections: [] })),
  }
}

/** settle asks for `value`'s picture and answers every stream the worker was
 * actually asked about, with the graph that ask carried: a stream whose
 * structure is cached posts nothing, and which streams those are is the cache's
 * business and not something a reply can be built from the model for.
 *
 * A reply to a layout nobody asked for would drop the worker, so the asks are
 * answered as they were posted and no other reply is sent. */
async function settle(worker: FakeWorker, layout: GraphLayout, value: GraphModel): Promise<Positions> {
  const before = worker.asked.length
  const pending = layout.positions(value)
  for (const ask of worker.asked.slice(before)) worker.answerTo(ask.id, placed(ask.graph))
  return pending
}

/** orthogonal asserts a run of points is a set of horizontal and vertical
 * segments: one coordinate at a time, never both and never neither. */
function orthogonal(points: readonly Point[]): void {
  for (let at = 0; at + 1 < points.length; at += 1) {
    const one = points[at]
    const two = points[at + 1]
    if (one === undefined || two === undefined) throw new Error('a point went missing')
    expect(
      (one.x === two.x) !== (one.y === two.y),
      `the segment from ${JSON.stringify(one)} to ${JSON.stringify(two)} is neither horizontal nor vertical`,
    ).toBe(true)
  }
}

describe('the graph model', () => {
  it('reads the fixture into columns, boxes, nodes and edges', () => {
    const all = model(null)
    // The tab order is the plan entry's: the catalogue's streams, then the plans
    // that name no stream, then All.
    expect(all.tabs.map((option) => option.tab)).toEqual([STREAM, '', null])
    expect(all.columns.map((column) => column.stream)).toEqual([STREAM, ''])
    expect(all.tab).toBeNull()
    expect(all.notShown).toBe(0)
    expect(all.columns[0]?.label).toEqual({ ok: true, text: STREAM, flags: [] })

    const plan = all.columns[0]?.plans[0]
    expect(text(plan?.title)).toBe('Example graph')
    // A finished release is a chip along the top and an open one is a box, and
    // this fixture has no finished release.
    expect(plan?.releases.map((release) => release.version)).toEqual(['1.4.0', '1.5.0'])
    expect(all.chips).toEqual([])

    const box = plan?.releases[0]
    expect(text(box?.heading.stream)).toBe(STREAM)
    expect(text(box?.heading.name)).toBe('Examplefruit')
    expect(text(box?.heading.version)).toBe('1.4.0')
    // The bar's two figures are the decoder's readings, so a release the
    // document carries no counter for draws `n/a` and never `0 of 0`.
    expect(box?.done.state).toBe('measured')
    expect(box?.done.value).toBe(1)
    expect(box?.total.value).toBe(3)

    // Nodes are ordered by level and then by id, so the graph is drawn in one
    // order whatever order the document listed them in.
    expect(plan?.nodes.map((node) => node.id)).toEqual([
      'task-one',
      'task-four',
      'task-three',
      'task-two',
      'task-five',
      'task-six',
    ])
    // Both ends of every edge are in this read, so all four are drawn; `from` is
    // the task that is needed and `to` the task that needs it.
    expect(plan?.edges).toEqual([
      { from: 'task-five', to: 'task-six', crossRelease: false, critical: false },
      { from: 'task-one', to: 'task-two', crossRelease: false, critical: true },
      { from: 'task-three', to: 'task-five', crossRelease: true, critical: false },
      { from: 'task-two', to: 'task-five', crossRelease: true, critical: false },
    ])
  })

  it('draws a finished release as a chip grouped by stream, and leaves the boxes open ones', () => {
    // A finished release is a hundred finished tasks nobody wants drawn, so it
    // leaves the plan's boxes and joins its stream's chips -- and the two lists
    // are complements of one another, never both and never neither.
    const finished = built(['plans', 0, 'releases', 0, 'finished'], true)
    expect(finished.columns[0]?.plans[0]?.releases.map((release) => release.version)).toEqual(['1.5.0'])
    expect(finished.chips.map((group) => group.stream)).toEqual([STREAM])
    expect(finished.chips[0]?.label).toEqual({ ok: true, text: STREAM, flags: [] })
    expect(finished.chips[0]?.chips.map((chip) => chip.version)).toEqual(['1.4.0'])
    // A chip carries the same heading a box would, so a screen draws one with
    // the other's code.
    expect(text(finished.chips[0]?.chips[0]?.heading.name)).toBe('Examplefruit')
    // The second plan's release list is empty and the first plan's `finished`
    // flag stays false, so the unmarked case is still all boxes.
    expect(model(null).chips).toEqual([])
  })

  it('draws an edge only between two nodes this read carries, and counts the rest', () => {
    // A needed task the plan does not have is a dependency the picture cannot
    // draw, and it is counted rather than dropped in silence.
    const missing = built(['plans', 0, 'nodes', 1, 'needs'], ['task-one', 'task-absent'])
    expect(missing.columns[0]?.plans[0]?.edges).toEqual([
      { from: 'task-five', to: 'task-six', crossRelease: false, critical: false },
      { from: 'task-one', to: 'task-two', crossRelease: false, critical: true },
      { from: 'task-three', to: 'task-five', crossRelease: true, critical: false },
      { from: 'task-two', to: 'task-five', crossRelease: true, critical: false },
    ])
    expect(missing.columns[0]?.plans[0]?.notShown).toBe(1)
    // The node keeps the dependency it was told about even when nothing is drawn
    // for it: a screen names it, and the model never invents a node for it.
    expect(nodeOf(missing, 'task-two')?.needs).toEqual(['task-one', 'task-absent'])
  })

  it('reads a state word from the state, the stage and the critical path', () => {
    const table: readonly (readonly [Parameters<typeof stateWords>[0], readonly string[]])[] = [
      [{ state: 'running', stage: 'implement', mark: '', critical: false }, ['implementing']],
      [{ state: 'running', stage: 'review_first', mark: '', critical: false }, ['in review']],
      [{ state: 'running', stage: 'review', mark: '', critical: false }, ['in review']],
      [{ state: 'running', stage: 'fix', mark: '', critical: false }, ['fixing']],
      // A live run in no stage a person watches -- `plan`, `bootstrap`,
      // `check`, `finalize` -- and one that has not reached a stage at all.
      [{ state: 'running', stage: '', mark: '', critical: false }, ['queued']],
      [{ state: 'running', stage: 'finalize', mark: '', critical: false }, ['queued']],
      [{ state: 'done', stage: '', mark: '', critical: false }, ['done']],
      [{ state: 'cancelled', stage: '', mark: '', critical: false }, ['halted']],
      [{ state: 'blocked', stage: '', mark: '', critical: false }, ['blocked']],
      [{ state: 'ready', stage: '', mark: '', critical: false }, ['ready']],
      [{ state: 'not-started', stage: '', mark: '', critical: false }, ['waiting']],
      // The critical path is a second word beside the first, never a
      // replacement for it.
      [{ state: 'ready', stage: '', mark: '', critical: true }, ['ready', 'critical']],
      [{ state: 'running', stage: 'fix', mark: '', critical: true }, ['fixing', 'critical']],
    ]
    for (const [state, words] of table) {
      expect(stateWords(state), `${state.state}/${state.stage}`).toEqual(words)
    }
    // A mark is drawn beside the word and never renames one: a `done` task whose
    // merge is unknown stays `done`.
    expect(stateWords({ state: 'done', stage: '', mark: 'merge unknown', critical: false })).toEqual(['done'])
  })

  it('answers for every state the document can carry, and never with a word outside the list', () => {
    // Every member of `PLAN_STATES`, so a seventh state added there without a
    // word here fails a test rather than drawing a node in silence.
    for (const state of PLAN_STATES) {
      const words = stateWords({ state, stage: 'implement', mark: '', critical: false })
      expect(words, state).toHaveLength(1)
      for (const word of words) expect(STATE_WORDS, state).toContain(word)
    }
    // Every state, every stage a runner writes, both criticalities: the words
    // produced are the whole list except `stuck`, which no plan node reaches.
    const stages = ['', 'plan', 'bootstrap', 'implement', 'review_first', 'review', 'fix', 'check', 'finalize']
    const produced = new Set<string>()
    for (const state of PLAN_STATES) {
      for (const stage of stages) {
        for (const critical of [false, true]) {
          for (const word of stateWords({ state, stage, mark: 'merge unknown', critical })) produced.add(word)
        }
      }
    }
    expect([...produced].sort()).toEqual([...STATE_WORDS].filter((word) => word !== 'stuck').sort())
  })

  it('drops and counts every node past a plan bound', () => {
    const base = rawNodes(rawPlan(0))[0]
    if (base === undefined) throw new Error('the fixture has no node to build from')
    const many = Array.from({ length: GRAPH_MAX_NODES_PER_PLAN + 3 }, (_unused, index) => ({
      ...base,
      id: `task-${String(index)}`,
      needs: [],
    }))
    const bounded = built(['plans', 0, 'nodes'], many)
    expect(bounded.columns[0]?.plans[0]?.nodes).toHaveLength(GRAPH_MAX_NODES_PER_PLAN)
    expect(bounded.columns[0]?.plans[0]?.notShown).toBe(3)
  })

  it('drops and counts every node past the bound on the model, not just on one plan', () => {
    const base = rawPlan(1)
    const node = rawNodes(base)[0]
    if (node === undefined) throw new Error('the fixture has no second plan')
    const perPlan = GRAPH_MAX_NODES / GRAPH_MAX_NODES_PER_PLAN
    const plans = Array.from({ length: perPlan + 1 }, (_unused, plan) => ({
      ...base,
      id: `plan-${String(plan)}`,
      nodes: Array.from({ length: GRAPH_MAX_NODES_PER_PLAN }, (_each, index) => ({
        ...node,
        id: `task-${String(index)}`,
        needs: [],
      })),
    }))
    const bounded = built(['plans'], plans)
    const column = bounded.columns[0]
    expect(column?.plans).toHaveLength(perPlan + 1)
    for (const filled of column?.plans.slice(0, perPlan) ?? []) {
      expect(filled.nodes).toHaveLength(GRAPH_MAX_NODES_PER_PLAN)
    }
    // The model's whole budget is spent by the first plans, so the last one
    // keeps nothing and says so.
    expect(column?.plans[perPlan]?.nodes).toHaveLength(0)
    expect(column?.plans[perPlan]?.notShown).toBe(GRAPH_MAX_NODES_PER_PLAN)
    expect(bounded.notShown).toBe(GRAPH_MAX_NODES_PER_PLAN)
  })

  it('drops and counts every plan past its bound, and every dependency past the fan-in bound', () => {
    const base = rawPlan(1)
    const plans = Array.from({ length: GRAPH_MAX_PLANS + 2 }, (_unused, index) => ({
      ...base,
      id: `plan-${String(index)}`,
    }))
    const bounded = built(['plans'], plans)
    expect(bounded.columns[0]?.plans).toHaveLength(GRAPH_MAX_PLANS)
    expect(bounded.notShown).toBe(2)

    const needs = Array.from({ length: GRAPH_MAX_NEEDS + 4 }, (_unused, index) => `task-${String(index)}`)
    const wide = built(['plans', 0, 'nodes', 0, 'needs'], needs)
    expect(nodeOf(wide, 'task-one')?.needs).toHaveLength(GRAPH_MAX_NEEDS)
    // None of those ids is a node of the plan, so each of the 32 kept ones is a
    // dependency the picture does not draw and each of the 4 dropped ones is one
    // the model did not carry: 36 in all.
    expect(wide.columns[0]?.plans[0]?.notShown).toBe(GRAPH_MAX_NEEDS + 4)
  })

  it('cuts a long title at the code-point bound and never splits a surrogate pair', () => {
    // An astral character: **two UTF-16 units and one code point**, which is the
    // pair a `slice` on the string would split -- a `slice(0, 200)` would take
    // 100 of these and leave the last one's high half behind. A BMP character
    // would satisfy every assertion below without the code-point iteration
    // `cutTitle` does, which is why this test is built from one of these.
    const astral = '\u{1F600}'
    expect(astral).toHaveLength(2)
    const title = astral.repeat(GRAPH_MAX_TITLE_CODE_POINTS + 10)
    const cut = nodeOf(built(['plans', 0, 'nodes', 0, 'title'], title), 'task-one')?.title
    expect(cut?.ok).toBe(true)
    const drawn = text(cut)
    expect([...drawn]).toHaveLength(GRAPH_MAX_TITLE_CODE_POINTS + 1)
    expect(drawn.endsWith('…')).toBe(true)
    expect(drawn.slice(0, -1)).toBe(astral.repeat(GRAPH_MAX_TITLE_CODE_POINTS))
    // Every code point is one of the two the title is made of: a cut through the
    // middle of a pair would leave a lone half, which is a string no screen
    // draws and no JSON round-trip keeps.
    const lone = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/
    expect(lone.test(drawn)).toBe(false)
    expect(drawn.includes('�')).toBe(false)
    // A title inside the bound is drawn whole and unmarked.
    const short = nodeOf(built(['plans', 0, 'nodes', 0, 'title'], 'a short title'), 'task-one')?.title
    expect(text(short)).toBe('a short title')
  })

  it('draws a hostile title as text, through the sanitizer', () => {
    // Built at run time rather than written as a literal, so this file is not a
    // catalogue of the shapes it refuses.
    const hostile = `<${'script'}>${'alert'}(1)</${'script'}>`
    const drawn = nodeOf(built(['plans', 0, 'nodes', 0, 'title'], hostile), 'task-one')?.title
    expect(drawn?.ok).toBe(true)
    expect(text(drawn)).toBe(hostile)
  })

  it('refuses a run that is not a four-character suffix, and reads one that is', () => {
    const good = nodeOf(model(null), 'task-one')?.run
    expect(good?.ok).toBe(true)
    expect(text(good)).toBe('aa01')
    for (const bad of ['20261025-121805-d204', 'aa0', 'aa0!', 'AA01', 'aa 1']) {
      const drawn = nodeOf(built(['plans', 0, 'nodes', 0, 'run'], bad), 'task-one')?.run
      expect(drawn?.ok, bad).toBe(false)
      // A refusal never contains the value it refused.
      expect(drawn?.ok === false ? drawn.reason : '', bad).not.toContain(bad)
    }
    // A task no run joined has no run at all, which is the decoder's empty
    // string and draws as an empty label rather than a refusal.
    const none = nodeOf(built(['plans', 0, 'nodes', 0, 'run'], ''), 'task-one')?.run
    expect(none?.ok).toBe(true)
    expect(text(none)).toBe('')
  })

  it('narrows to one stream, and falls back to All for a tab the document does not carry', () => {
    const only = model(STREAM)
    expect(only.columns.map((column) => column.stream)).toEqual([STREAM])
    expect(only.tab).toBe(STREAM)
    // The second plan names no stream, so it is not in this picture at all.
    expect(only.columns[0]?.plans.map((plan) => plan.id)).toEqual([DOCUMENT.plans[0]?.id])
    // A tab the catalogue dropped between two reads is All rather than an empty
    // frame: a graph that says "nothing here" about a full document is a lie.
    const stale = model('a-stream-the-catalogue-dropped')
    expect(stale.tab).toBeNull()
    expect(stale.columns.map((column) => column.stream)).toEqual([STREAM, ''])
    // And the no-stream tab narrows to the plans that name none.
    const none = model('')
    expect(none.columns.map((column) => column.stream)).toEqual([''])
    expect(none.columns[0]?.plans.map((plan) => plan.id)).toEqual([DOCUMENT.plans[1]?.id])
  })
})

describe('the graph layout cache', () => {
  it('lays a stream out once, and again only when that stream’s structure changes', async () => {
    // **The red step.** This is the claim the whole cache is for: a watch frame
    // that changed a node's state or stage is not a reason to send a stream to
    // the engine again -- the boxes have not moved. The key is a stream's, so the
    // runs a picture costs are its streams, one each.
    const { worker, layout } = layoutWith()
    const before = model(null)
    expect(before.columns).toHaveLength(2)
    await settle(worker, layout, before)
    expect(worker.placements).toBe(2)

    // The same structure with drawn words changed: `task-two` goes from
    // `running`/`implement` to `done`, and `task-three` from `ready` to
    // `blocked`. Nothing an engine can see has moved.
    const changed = built(['plans', 0, 'nodes', 1, 'state'], 'done')
    const recoded = built(['plans', 0, 'nodes', 2, 'state'], 'blocked')
    expect(structureHash(streamOf(changed, 0))).toBe(structureHash(streamOf(before, 0)))
    expect(structureHash(streamOf(recoded, 0))).toBe(structureHash(streamOf(before, 0)))
    expect(nodeOf(changed, 'task-two')?.words).toEqual(['done', 'critical'])
    expect(nodeOf(recoded, 'task-three')?.words).toEqual(['blocked'])
    await settle(worker, layout, changed)
    await settle(worker, layout, recoded)
    expect(worker.placements).toBe(2)

    // ...and a structure that did change is laid out: `task-six` gains a
    // dependency on `task-three`, which is an edge the engine has to route. The
    // change is one stream's, so one run is added and the stream beside it keeps
    // the positions it was already given -- the key follows the run.
    const edgeAdded = built(['plans', 0, 'nodes', 5, 'needs'], ['task-three', 'task-five'])
    expect(structureHash(streamOf(edgeAdded, 0))).not.toBe(structureHash(streamOf(before, 0)))
    expect(structureHash(streamOf(edgeAdded, 1))).toBe(structureHash(streamOf(before, 1)))
    await settle(worker, layout, edgeAdded)
    expect(worker.placements).toBe(3)
  })

  it('does not keep a layout the worker refused, and bounds how many it keeps', async () => {
    const { worker, layout } = layoutWith()
    // One stream, so the refusal is one stream's and the ask after it is that
    // stream's again.
    const value = model('')
    expect(value.columns).toHaveLength(1)
    const refused = layout.positions(value)
    worker.answer({ children: [{ id: 'nowhere', x: 0, y: 0, width: 1, height: 1 }], edges: [] })
    await expect(refused).rejects.toThrow(/did not ask about/)
    // A layout that was refused has not been laid out, so the next ask goes back
    // to the worker rather than getting the rejection again.
    const again = layout.positions(value)
    expect(worker.placements).toBe(2)
    worker.answer(placed(lastAsk(worker)))
    await expect(again).resolves.toBeTruthy()

    // A handful of other structures in, the cache holds
    // `GRAPH_LAYOUT_CACHE_ENTRIES`, and the entry the bound evicted is asked for
    // again.
    const many: GraphModel[] = []
    for (let index = 0; index < GRAPH_LAYOUT_CACHE_ENTRIES + 2; index += 1) {
      many.push(built(['plans', 0, 'nodes', 0, 'id'], `task-${String(index)}`))
    }
    for (const each of many) await settle(worker, layout, each)
    const before = worker.placements
    const oldest = many[0]
    if (oldest === undefined) throw new Error('no model')
    const pending = layout.positions(oldest)
    // The evicted entry is the one stream that was patched; the stream beside it
    // is still cached, so its row costs no run.
    expect(worker.placements).toBe(before + 1)
    worker.answer(placed(lastAsk(worker)))
    await pending
  })

  it('holds every stream of a picture wider than the floor, so a second read lays nothing out', async () => {
    // **The floor is not the bound.** A read asks for its whole picture and
    // trims the cache once it has it, so a bound of `GRAPH_LAYOUT_CACHE_ENTRIES`
    // on a picture of nine streams trims away one of the nine streams it has just
    // asked for: every read of that picture, changed or not, lays that stream out
    // again. The bound is therefore the picture's own number of streams where
    // that is larger than the floor, and the case reads both sides of the gap.
    for (const count of [GRAPH_LAYOUT_CACHE_ENTRIES + 1, 25]) {
      const { worker, layout } = layoutWith()
      const value = manyStreams(count)
      expect(value.columns, `the document of ${String(count)} streams read as fewer`).toHaveLength(count)
      await settle(worker, layout, value)
      expect(worker.placements).toBe(count)

      // The same model again: one run per stream is what a picture of `count`
      // streams costs the first time, and nothing at all the second.
      const before = worker.placements
      await settle(worker, layout, value)
      expect(
        worker.placements,
        `a second read of a ${String(count)}-stream picture asked the worker again`,
      ).toBe(before)
    }
  })

  it('evicts once the read is over, so a narrower picture with one stream changed costs one run', async () => {
    // **The eviction is the read's, not the stream's.** It used to run inside
    // the read, between two streams of one picture: the first stream a miss adds
    // trims the cache to the bound, and on a picture one stream narrower than
    // the last the oldest entries are the ones this read has not reached yet --
    // so the stream about to be asked for is evicted before it is asked for,
    // every stream after that is evicted by the stream before it, and a read
    // that should cost one run lays the whole picture out again. Evicting once,
    // after the pass, leaves every entry the pass made or found the most recent:
    // the oldest-first eviction then takes the stream that is gone and the
    // stream that changed, and nothing the picture still wants.
    for (const count of [10, 25, 60]) {
      const { worker, layout } = layoutWith()
      await settle(worker, layout, manyStreams(count))
      expect(worker.placements).toBe(count)

      // The last stream gone, and the first stream of what is left is a plan the
      // engine has not placed: exactly that one stream is laid out again.
      const before = worker.placements
      await settle(worker, layout, manyStreams(count - 1, 0))
      expect(
        worker.placements - before,
        `a ${String(count)}-stream picture, one stream narrower and one changed, was laid out again`,
      ).toBe(1)

      // A picture a stream wider, and then the one before it: the read that goes
      // back is a read of entries it still holds, so the added stream is the
      // only one that can cost a run -- and going back costs none at all.
      await settle(worker, layout, manyStreams(count + 1))
      const widened = worker.placements
      await settle(worker, layout, manyStreams(count))
      expect(
        worker.placements - widened,
        `going back to the ${String(count)}-stream picture was laid out again`,
      ).toBeLessThanOrEqual(1)
    }
  })

  it('lays the graph out left to right, orthogonally, over the cards’ own box', async () => {
    // **The geometry the redesign's Graph canvas is drawn with.** The picture is
    // read left to right -- a task to the left of the tasks that need it -- and
    // its edges are routed orthogonally rather than as the engine's default
    // splines, because a dependency's arrow is read as a line that leaves one
    // card's side and arrives at another's, and a spline between two boxes is a
    // curve a reader has to follow rather than a run they can see.
    const { worker, layout } = layoutWith()
    await settle(worker, layout, model(null))
    const asks = worker.sent.filter((message) => message['cmd'] === 'layout')
    // One ask per stream, and every one of them carries the whole request: a
    // stream is laid out on its own, so no ask there is left to the engine's
    // defaults.
    expect(asks).toHaveLength(2)
    for (const ask of asks) {
      const options = (ask['layoutOptions'] ?? {}) as Readonly<Record<string, string>>
      expect(options['elk.algorithm']).toBe('layered')
      expect(options['elk.direction']).toBe('RIGHT')
      expect(options['elk.edgeRouting']).toBe('ORTHOGONAL')
    }

    // And the box the engine is given is the card the screen draws: 260 x 88,
    // which is the room a wrapped title is measured against.
    const first = asks[0]
    if (first === undefined) throw new Error('the worker was asked for no layout')
    const graph = first['graph'] as LayoutGraph
    expect(graph.children.length).toBeGreaterThan(0)
    for (const child of graph.children) {
      expect([child.width, child.height]).toEqual([GRAPH_NODE_WIDTH, GRAPH_NODE_HEIGHT])
    }
    expect([GRAPH_NODE_WIDTH, GRAPH_NODE_HEIGHT]).toEqual([260, 88])

    // The box size is in the cache key **of every stream** -- each size
    // length-prefixed, the way every part of the hash is written -- so a card
    // that grows is a different structure and no answer laid out for the smaller
    // card is reused.
    const width = String(GRAPH_NODE_WIDTH)
    const height = String(GRAPH_NODE_HEIGHT)
    for (const index of [0, 1]) {
      expect(structureHash(streamOf(model(null), index))).toContain(`${String(width.length)}:${width}`)
      expect(structureHash(streamOf(model(null), index))).toContain(`${String(height.length)}:${height}`)
    }
  })

  it('sends the worker ids and sizes and nothing a person wrote', () => {
    // No text reaches the engine: a title is a `Sanitized` value on the node and
    // is not in the graph, so a hostile title cannot be anything but text once
    // the screen writes it into the box's own element.
    const hostile = `<${'img'} ${'src'}=${'x'} ${'onerror'}=${'1'}>`
    const value = built(['plans', 0, 'nodes', 0, 'title'], hostile)
    const graph = graphOf(streamOf(value, 0))
    expect(graph.children.map((child) => child.width)).toEqual(graph.children.map(() => GRAPH_NODE_WIDTH))
    expect(graph.children.map((child) => child.height)).toEqual(graph.children.map(() => GRAPH_NODE_HEIGHT))
    expect(graph.edges).toHaveLength(4)
    const wire = JSON.stringify(graph)
    expect(wire).not.toContain(hostile)
    expect(wire).not.toContain('Example')
    // What does reach it is the id of every node, plan-qualified: ids are a
    // plan's own, and two plans may name the same one.
    for (const child of graph.children) expect(wire).toContain(child.id)
  })

  it('tells two structures apart when a part boundary could be moved', () => {
    // The parts of one stream's hash are a flat run of values, and a run of
    // lengths alone does not say where one level ends: a plan `p` holding a node
    // whose id and release are both `1.4`, followed by an empty plan `1.4`, is
    // the same sequence of parts as plan `p` empty followed by plan `1.4`
    // holding that node. The two are different pictures -- the engine's own ids
    // differ -- so a hash that could not tell them apart would hand one of them
    // the other's positions.
    const base = rawPlan(1)
    const node = rawNodes(base)[0]
    if (node === undefined) throw new Error('the fixture has no second plan')
    const plans = (first: boolean) => [
      { ...base, id: 'p', stream: 's', nodes: first ? [{ ...node, id: '1.4', release: '1.4' }] : [] },
      { ...base, id: '1.4', stream: 's', nodes: first ? [] : [{ ...node, id: '1.4', release: '1.4' }] },
    ]
    const here = built(['plans'], plans(true))
    const there = built(['plans'], plans(false))
    expect(graphOf(bothPlans(here)).children.map((child) => child.id)).toEqual(['1:p:1.4'])
    expect(graphOf(bothPlans(there)).children.map((child) => child.id)).toEqual(['3:1.4:1.4'])
    expect(structureHash(bothPlans(here))).not.toBe(structureHash(bothPlans(there)))
  })

  it('keeps two plans apart when an id and its prefix could be split two ways', () => {
    // `ab` + `c` and `a` + `bc` are the same characters joined with no length in
    // front of the first part, and the engine's ids have to tell them apart
    // within one run: both plans are the same stream's.
    const base = rawPlan(1)
    const node = rawNodes(base)[0]
    if (node === undefined) throw new Error('the fixture has no second plan')
    const plans = [
      { ...base, id: 'ab', nodes: [{ ...node, id: 'c' }] },
      { ...base, id: 'a', nodes: [{ ...node, id: 'bc' }] },
    ]
    const graph = graphOf(bothPlans(built(['plans'], plans)))
    expect(new Set(graph.children.map((child) => child.id)).size).toBe(2)
  })

  it('routes an edge whose ends were placed in two rows through the gutter between them', () => {
    // **The line no engine drew.** Each stream is laid out on its own run, so an
    // edge whose two ends were placed in different rows is an edge no run ever
    // saw, and one handed to the engine would simply have been lost. The route
    // is this module's own: three segments, out of the upper card's bottom and
    // into the lower card's top, meeting on the ground between the two rows.
    //
    // No document this console can build carries such an edge today -- a plan's
    // `needs` name tasks of that plan, and a plan belongs to one stream -- so
    // this is pinned on the function rather than through a model, and it is
    // pinned because the change that made two runs possible is the change that
    // made a route of our own necessary.
    const above: PlacedNode = { id: 'above', x: 0, y: 0, width: GRAPH_NODE_WIDTH, height: GRAPH_NODE_HEIGHT }
    const below: PlacedNode = { id: 'below', x: 400, y: 212, width: GRAPH_NODE_WIDTH, height: GRAPH_NODE_HEIGHT }
    const down = crossStreamRoute(above, below)[0]
    if (down === undefined) throw new Error('no route between the two rows')
    const middle = (GRAPH_NODE_HEIGHT + 212) / 2
    expect(down.startPoint).toEqual({ x: GRAPH_NODE_WIDTH / 2, y: GRAPH_NODE_HEIGHT })
    expect(down.endPoint).toEqual({ x: 400 + GRAPH_NODE_WIDTH / 2, y: 212 })
    expect(down.bendPoints).toEqual([
      { x: GRAPH_NODE_WIDTH / 2, y: middle },
      { x: 400 + GRAPH_NODE_WIDTH / 2, y: middle },
    ])
    // The bends are in the gutter and not in either row: the ground between a
    // card's bottom and the next row's top is the only place a horizontal run
    // crosses neither a card nor a band.
    expect(down.startPoint.y).toBeLessThan(middle)
    expect(middle).toBeLessThan(down.endPoint.y)
    orthogonal([down.startPoint, ...(down.bendPoints ?? []), down.endPoint])

    // And the same route read upward: the two ends face each other whatever the
    // model's own order put first, so the line leaves the card's top and arrives
    // at the other's bottom.
    const up = crossStreamRoute(below, above)[0]
    if (up === undefined) throw new Error('no route upward')
    expect(up.startPoint).toEqual({ x: 400 + GRAPH_NODE_WIDTH / 2, y: 212 })
    expect(up.endPoint).toEqual({ x: GRAPH_NODE_WIDTH / 2, y: GRAPH_NODE_HEIGHT })
    expect(up.bendPoints).toEqual([
      { x: 400 + GRAPH_NODE_WIDTH / 2, y: middle },
      { x: GRAPH_NODE_WIDTH / 2, y: middle },
    ])
    // The same gutter, read the other way. The midpoint is the gutter in both
    // directions and not only when the second card is the lower one: two rows
    // never overlap -- `GRAPH_ROW_GAP` is ground between them -- so both ends of
    // the pair always leave that ground to route through, and a bend above the
    // upper card's bottom could only be reached by a pair the stacking cannot
    // produce.
    expect(middle).toBeLessThan(up.startPoint.y)
    expect(middle).toBeGreaterThan(up.endPoint.y)
    orthogonal([up.startPoint, ...(up.bendPoints ?? []), up.endPoint])
  })
})
