// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The plan's estimate on the Graph screen (the orchestrator graph estimates
// plan's S4; D2714, D2722, D2723; the CLI's half is S3,
// `internal/cli/plan_status.go`'s additive `estimate` block and
// `internal/plan/estimate.go`).
//
// Four things are pinned here, and the last two are the ones a console-vs-CLI
// misunderstanding would break:
//
//   - **the model carries the block**: `buildGraphModel` hands the screen the
//     plan's estimate with every figure a `Reading` (`shared/availability.ts`),
//     so no path draws a bare number and no path draws a `0` for a figure the
//     document did not carry;
//   - **the list draws it per plan**, in the console's words and through
//     `formatReading`, as the CLI's own line about the same block
//     (`planEstimateLine`) would read it: the ready-by range, then the cost
//     range, or D2723's `no history yet for N tasks` where the plan's money
//     could not be computed at all;
//   - **nothing is drawn where the block is absent**, which is what an older CLI
//     writes: the key is additive, so a console newer than the CLI that drives it
//     finds no estimate and draws no line rather than a zero-valued figure.
//
// A fourth is what the **reduced mode** does with the block, and it belongs to
// this file rather than to the drawing's because the money it takes out arrives
// with the readable one: the cost half is withheld where the console cannot
// vouch for the CLI's money fields (`spend` is a view of `REDUCED_VIEWS`,
// `shared/version.ts`), so the ready-by range is drawn alone and the withheld
// figure reads the console's `n/a` with its reason in the line's `title`.
//
// The document is `test/fixtures/plan-status.json` through the real decoder, and
// every patch is a **raw** edit that goes through it too: the fixture's own plans
// carry no estimate at all, which is exactly the older CLI this file's last case
// is about, so the cases that show one build it here.
//
// Everything runs against `test/support/fakeDom.ts`: no browser, no Electron.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { decodePlanStatus } from '../../src/shared/schema'
import type { PlanStatusView } from '../../src/shared/schema'
import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import type { Sanitized } from '../../src/shared/sanitize'
import { buildGraphModel } from '../../src/renderer/graph/model'
import type { GraphModel, GraphPlan } from '../../src/renderer/graph/model'
import {
  GRAPH_ESTIMATE_COST_HIDDEN_WORD,
  GRAPH_ESTIMATE_MINUTES,
  GRAPH_ESTIMATE_NO_HISTORY,
  GRAPH_ESTIMATE_READY,
  GRAPH_INITIAL_STATE,
  GraphLayoutClient,
  GraphScreen,
  estimateCostsWithheld,
  renderGraph,
} from '../../src/renderer/screens/graph'
import type { GraphState, GraphTheme, ScreenElement } from '../../src/renderer/screens/graph'
import { FakeDocument, everyNode, textOf } from '../support/fakeDom'
import type { FakeNode } from '../support/fakeDom'
import { FakeWorker, NodeSurface, Surface, decoded } from '../support/graphHarness'

/** RAW is the fixture as the wire has it. */
const RAW: unknown = JSON.parse(readFileSync(join('test', 'fixtures', 'plan-status.json'), 'utf8'))

/** DOCUMENT is the same document through the real decoder, which is what a
 * screen is handed. */
const DOCUMENT: PlanStatusView = decoded(RAW)

/** PLAN is the fixture's first plan, which is the one every case patches. */
const PLAN = DOCUMENT.plans[0]?.id ?? ''
const SECOND = DOCUMENT.plans[1]?.id ?? ''

/**
 * ESTIMATE is one plan's block as `plan status --json` writes it: the two ranges
 * and no unknown task, so the cost is stated.
 *
 * The numbers are invented and belong to nobody's runs: minutes and dollars are
 * the block's own units (`Estimate.ReadyInMinutes`, `Estimate.CostUSD`), and the
 * fixture is not a recording.
 */
const ESTIMATE: Readonly<Record<string, unknown>> = Object.freeze({
  ready_in_minutes: { low: 40, high: 70 },
  cost_usd: { low: 3, high: 5 },
  cost_unknown_tasks: 0,
})

/** READY_RANGE and COST_RANGE are the two ranges of ESTIMATE as this screen says
 * them: whole minutes, dollars to the cent, and an **en dash** between the ends
 * -- U+2013, the mark the CLI's own line prints, spelled here as an escape so
 * this file cannot drift from `GRAPH_ESTIMATE_RANGE` by a keystroke. */
const READY_RANGE = '40–70'
const COST_RANGE = '$3.00–$5.00'

/** patched is the fixture with one path replaced, through the decoder: a patched
 * document is a document and has to survive the read like any other. */
function patched(path: readonly (string | number)[], value: unknown): PlanStatusView {
  return decoded(patch(path, value))
}

/** patch is the same edit without the decode, for the case that asserts the
 * decoder's own refusal. */
function patch(path: readonly (string | number)[], value: unknown): unknown {
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

/** estimated is the fixture with one plan's estimate block set to `value`. The
 * block is written whole rather than merged, so a case that means an absent key
 * leaves it out. */
function estimated(value: unknown, plan = 0): PlanStatusView {
  return patched(['plans', plan, 'estimate'], value)
}

/** planOf is one plan of the model a document builds, by id. */
function planOf(document: PlanStatusView, id: string): GraphPlan {
  return planIn(buildGraphModel({ document, tab: null }), id)
}

/** planIn is one plan of a model, by id -- the same walk for a model that was
 * built from a document and one the console withheld the cost of. */
function planIn(model: GraphModel, id: string): GraphPlan {
  for (const column of model.columns) {
    for (const plan of column.plans) if (plan.id === id) return plan
  }
  throw new Error(`the model has no plan ${id}`)
}

/** headingText is what the drawing writes for a sanitized heading: the text the
 * sanitizer passed, or the console's `n/a` where it refused one. */
function headingText(value: Sanitized): string {
  return value.ok ? value.text : UNAVAILABLE_MARK
}

/** classedIn is every element of one exact class under a node, in document
 * order. */
function classedIn(root: FakeNode, name: string): readonly FakeNode[] {
  return everyNode(root).filter((each) => (each.getAttribute('class') ?? '').split(/\s+/).includes(name))
}

/** mounted is a drawing's tree, in a fake document. */
function mounted(element: ScreenElement): FakeNode {
  const document = new FakeDocument()
  const node = (element as NodeSurface).node
  document.body.appendChild(node)
  return node
}

/** listed is the model drawn as a list -- where a plan has a heading of its own
 * -- with no positions: the list needs none. `costWithheld` is the gate's
 * answer, applied to the model the way the screen applies it
 * (`estimateCostsWithheld`), so a case about the reduced mode draws the model
 * the entry's own screen draws. */
function listed(document: PlanStatusView, state: GraphState = GRAPH_INITIAL_STATE, costWithheld = false): FakeNode {
  const built = buildGraphModel({ document, tab: null })
  const model = costWithheld ? estimateCostsWithheld(built) : built
  const element = renderGraph(
    new Surface(new FakeDocument()),
    { model, positions: null, state: { ...state, view: 'list' }, runs: [], notice: null },
    'light' as GraphTheme,
  )
  return mounted(element)
}

// ---------------------------------------------------------------------------
// The model
// ---------------------------------------------------------------------------

describe('the plan estimate in the graph model (S4)', () => {
  it('carries the plan its estimate, every figure a reading, and null for a plan the document gave none', () => {
    // The fixture's own plan carries no `estimate` key at all -- it is a
    // document from before the CLI that writes one -- so the model's answer is
    // `null` rather than a block of zeros, and a screen draws nothing for it.
    expect(planOf(DOCUMENT, PLAN).estimate).toBeNull()

    const plan = planOf(estimated(ESTIMATE), PLAN).estimate
    expect(plan).not.toBeNull()
    expect(plan?.readyIn.low.value).toBe(40)
    expect(plan?.readyIn.high.value).toBe(70)
    expect(plan?.cost?.low.value).toBe(3)
    expect(plan?.cost?.high.value).toBe(5)
    expect(plan?.unknownTasks.value).toBe(0)
    // Every one of them is a reading, and a reading a document wrote is
    // `measured`: the console's type is what keeps a screen from drawing a
    // number, and `availability.test.ts` is where the states themselves are
    // pinned.
    for (const reading of [plan?.readyIn.low, plan?.readyIn.high, plan?.cost?.low, plan?.cost?.high, plan?.unknownTasks]) {
      expect(reading?.state).toBe('measured')
    }
  })

  it('carries an absent cost as null with the count beside it, and asks nothing about the rest of the block', () => {
    // D2723: a plan whose money cannot be stated has no range at all and a count
    // of the tasks it is unknown for. The two are one answer, so the model
    // carries `null` and the screen shows the count.
    const unknown = planOf(estimated({ ready_in_minutes: { low: 40, high: 70 }, cost_unknown_tasks: 3 }), PLAN).estimate
    expect(unknown?.cost).toBeNull()
    expect(unknown?.unknownTasks.value).toBe(3)
    // The block's `nodes`, `max_running` and `computed_at` are keys this console
    // draws nothing from, so they are not read -- and a document that carries
    // them decodes exactly the same.
    const full = planOf(
      estimated({ ...ESTIMATE, nodes: [{ id: 'task-one', basis: 'history' }], max_running: 3, computed_at: '2026-11-17T10:00:00Z' }),
      PLAN,
    ).estimate
    expect(full).toEqual(planOf(estimated(ESTIMATE), PLAN).estimate)
  })

  it('leaves the plans beside it alone, and refuses a block without the one key it needs', () => {
    // The estimate is a plan's own: patching one plan's block leaves the other's
    // absence alone, which is what "per plan" means for a checkout of several.
    expect(planOf(estimated(ESTIMATE), SECOND).estimate).toBeNull()
    // `ready_in_minutes` is the one key an estimate cannot do without: a block
    // without it is a shape this build does not know, refused by its own name
    // rather than drawn as a plan with no wait.
    const refusal = decodePlanStatus(patch(['plans', 0, 'estimate'], { cost_usd: { low: 3, high: 5 } }))
    expect(refusal.ok).toBe(false)
    if (!refusal.ok) {
      expect(refusal.skew.refusal.name).toBe('missing')
      expect(refusal.skew.refusal.field).toBe('plans[0].estimate.ready_in_minutes')
    }
  })
})

// ---------------------------------------------------------------------------
// The list
// ---------------------------------------------------------------------------

describe('the estimate line the list draws (S4)', () => {
  it('draws the ready-by and cost ranges under the plan they are about', () => {
    const root = listed(estimated(ESTIMATE))
    const lines = classedIn(root, 'graph-list-estimate')
    // One line, under the one plan that carries an estimate: the other plan of
    // the fixture has none and draws none.
    expect(lines).toHaveLength(1)
    expect(lines[0]?.textContent).toBe(
      `${GRAPH_ESTIMATE_READY} ${READY_RANGE} ${GRAPH_ESTIMATE_MINUTES}, ${COST_RANGE}`,
    )
    // And it is drawn in the section of the plan it is about, under that plan's
    // own heading, rather than beside the picture for a reader to guess at.
    const section = classedIn(root, 'graph-list-plan').find((each) => classedIn(each, 'graph-list-estimate').length === 1)
    const heading = section === undefined ? undefined : classedIn(section, 'graph-list-title')[0]
    expect(heading?.textContent).toBe(headingText(planOf(estimated(ESTIMATE), PLAN).title))
  })

  it('says the plan cost is unknown in the CLI words, and counts the tasks it is unknown for', () => {
    const root = listed(
      estimated({ ready_in_minutes: { low: 40, high: 70 }, cost_unknown_tasks: 3 }),
    )
    expect(classedIn(root, 'graph-list-estimate')[0]?.textContent).toBe(
      `${GRAPH_ESTIMATE_READY} ${READY_RANGE} ${GRAPH_ESTIMATE_MINUTES}, ${GRAPH_ESTIMATE_NO_HISTORY} 3 tasks`,
    )
    // The count is a reading drawn through `formatReading`, and the words around
    // it are the console's: a plan of one task reads `1 task`.
    const one = listed(estimated({ ready_in_minutes: { low: 40, high: 70 }, cost_unknown_tasks: 1 }))
    expect(classedIn(one, 'graph-list-estimate')[0]?.textContent).toBe(
      `${GRAPH_ESTIMATE_READY} ${READY_RANGE} ${GRAPH_ESTIMATE_MINUTES}, ${GRAPH_ESTIMATE_NO_HISTORY} 1 task`,
    )
  })

  it('draws a range end the document did not carry as n/a, never as a zero', () => {
    // The availability rule: an end that is not there is unknown, and a run of
    // `n/a` is what says so. Drawing `0` would say this plan finishes now and
    // costs nothing, which is a figure nobody computed.
    const root = listed(estimated({ ready_in_minutes: { high: 70 }, cost_usd: { low: 3 } }))
    expect(classedIn(root, 'graph-list-estimate')[0]?.textContent).toBe(
      `${GRAPH_ESTIMATE_READY} n/a–70 ${GRAPH_ESTIMATE_MINUTES}, $3.00–n/a`,
    )
  })

  it('draws no line at all for a document from before the block existed', () => {
    // The whole fixture is that document: an older CLI writes no `estimate` key,
    // and the console draws the plan exactly as it drew it before this screen
    // learned to read one -- no line, no `n/a`, no empty slot left behind.
    const root = listed(DOCUMENT)
    expect(classedIn(root, 'graph-list-estimate')).toEqual([])
    // Not vacuous: the same drawing over a plan that does carry one has the
    // line, so what the case above turns on is the absent block alone.
    expect(classedIn(listed(estimated(ESTIMATE)), 'graph-list-estimate')).toHaveLength(1)
  })

  it('draws it in the list and not on the canvas, which gives a plan no heading', async () => {
    // The canvas groups its boxes by release and its rows by stream, and gives a
    // *plan* no text of its own -- so the line lives in the list, where a plan has
    // a section and a heading. This case draws the real canvas over the layout
    // worker to say so, and to hold the canvas to the estimate reaching none of
    // its geometry.
    const document = new FakeDocument()
    const screen = new GraphScreen({
      factory: new Surface(document),
      client: new GraphLayoutClient(() => new FakeWorker()),
      theme: 'light',
    })
    await screen.load(estimated(ESTIMATE), [])
    const canvas = screen.redraw()
    if (canvas === null) throw new Error('the canvas drew nothing')
    expect(classedIn(mounted(canvas), 'graph-canvas')).toHaveLength(1)
    expect(classedIn(mounted(canvas), 'graph-list-estimate')).toEqual([])
    // The same read as a list carries the line, so the two drawings differ by
    // the line alone and not by the estimate being lost between them.
    expect(classedIn(listed(estimated(ESTIMATE)), 'graph-list-estimate')).toHaveLength(1)
  })
})

// ---------------------------------------------------------------------------
// The reduced mode
// ---------------------------------------------------------------------------

describe('the estimate in a reduced console (S4)', () => {
  it('draws the ready-by range and no money at all, with the console\'s reason for the cost', () => {
    // `spend` is one view of `REDUCED_VIEWS`, and this line carries the plan's
    // money: the entry asks `hidesView` and hands the answer to the screen, the
    // model the drawing is made from holds no cost, and what is left is the
    // ready-by range -- minutes are not money -- beside the console's `n/a`,
    // whose reason is in the line's `title`.
    const root = listed(estimated(ESTIMATE), GRAPH_INITIAL_STATE, true)
    const line = classedIn(root, 'graph-list-estimate')[0]
    expect(line?.textContent).toBe(`${GRAPH_ESTIMATE_READY} ${READY_RANGE} ${GRAPH_ESTIMATE_MINUTES}, n/a–n/a`)
    expect(line?.getAttribute('title')).toBe(GRAPH_ESTIMATE_COST_HIDDEN_WORD)
    // Not vacuous, and the point of the case: the dollar sign is nowhere in the
    // reduced drawing, and the very same drawing ungated reads `$3.00–$5.00`.
    expect(textOf(root)).not.toContain('$')
    expect(textOf(listed(estimated(ESTIMATE)))).toContain(COST_RANGE)
  })

  it('leaves alone the plan whose money the CLI could not compute, and asks nothing of a range the document left open', () => {
    // D2723's sentence is not money: a plan with no cost range has no dollar
    // figure to withhold, so the reduced drawing is the ungated one and the
    // count of tasks it is unknown for is still drawn.
    const unknown = { ready_in_minutes: { low: 40, high: 70 }, cost_unknown_tasks: 3 }
    expect(textOf(listed(estimated(unknown), GRAPH_INITIAL_STATE, true))).toBe(
      textOf(listed(estimated(unknown))),
    )
    // A range end the **document** did not carry is `n/a` too, and that one is
    // not the console's withholding: ungated, the line draws the figure it has
    // beside the mark and carries no `title`, because the reason for that `n/a`
    // is the document's and not this console's sentence.
    const half = listed(estimated({ ready_in_minutes: { high: 70 }, cost_usd: { low: 3 } }))
    const line = classedIn(half, 'graph-list-estimate')[0]
    expect(line?.textContent).toBe(`${GRAPH_ESTIMATE_READY} n/a–70 ${GRAPH_ESTIMATE_MINUTES}, $3.00–n/a`)
    expect(line?.getAttribute('title')).toBeNull()
  })

  it('takes the cost out at the door the entry builds its screen through', async () => {
    // The gate is asked by the caller (`graphScreen`'s third argument,
    // `hidesView(compatibility, 'spend')`), so the model the drawing would be
    // made from is the one the screen holds -- which is what this case reads.
    const screen = new GraphScreen({
      factory: new Surface(new FakeDocument()),
      client: new GraphLayoutClient(() => new FakeWorker()),
      theme: 'light',
      costWithheld: true,
    })
    await screen.load(estimated(ESTIMATE), [])
    const model = screen.lastModel
    if (model === null) throw new Error('the read drew nothing')
    const withheld = planIn(model, PLAN).estimate
    expect(withheld?.readyIn.low.value).toBe(40)
    expect(withheld?.readyIn.high.value).toBe(70)
    expect(withheld?.cost?.low.value).toBeNull()
    expect(withheld?.cost?.high.value).toBeNull()
    // And a screen with no gate in front of it -- a test, a golden, a screen
    // built by hand -- holds the figures the document carried.
    const open = new GraphScreen({
      factory: new Surface(new FakeDocument()),
      client: new GraphLayoutClient(() => new FakeWorker()),
      theme: 'light',
    })
    await open.load(estimated(ESTIMATE), [])
    const drawn = open.lastModel === null ? null : planIn(open.lastModel, PLAN).estimate
    expect(drawn?.cost?.low.value).toBe(3)
    expect(drawn?.cost?.high.value).toBe(5)
  })
})
