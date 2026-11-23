// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Coordinator tab: the project's **active** orchestrators as a graph (CG7b,
// D3210).
//
// One read -- `fugaro orchestrator map --json`, pinned in
// `COORDINATOR_SOURCE_LINE` -- and one section: every orchestrator a credential
// is driving now, drawn as a box the layout worker placed, the connections
// between them as edges, and one health line under the drawing. The document is
// C-ORCH's (`docs/console-fixes/contract-orchestrators.md`), and what it holds
// is what this screen draws and nothing else.
//
// Four rules shape the drawing, and each is a sentence the contract wrote:
//
//   - **A node is an orchestrator somebody drives now.** The map holds no
//     history (D3210): an orchestrator nobody drives is not on it, so an empty
//     `nodes` is the console's own sentence about a quiet project and never a
//     spinner over an empty frame. The screen reads no stored view and keeps no
//     session cache: the map is the installation's live state, which is the one
//     thing worth re-reading rather than serving from memory.
//   - **A dependency is solid and an overlap is dashed and a warning.** The two
//     are the document's own `kind`, drawn as two classes, and an overlap is
//     drawn **twice**: the dashed edge, and the sentence above the canvas that
//     counts them -- because a dashed line is a tone, and no state on this
//     screen is a tone alone (design section 3.5).
//   - **A sweep that is off carries no overlap at all** (D3211). `health.sweep`
//     false means nobody looked, so the absence of dashed edges is not "nothing
//     overlaps": the screen says so, names the switch that turns the sweep on,
//     and draws the CLI's own `note` beside it.
//   - **Every field is text.** A `driver`, an overlap's `reason` and the
//     health's `note` are written by whoever holds a lease and by the
//     coordinator's own sweep, so each is drawn through `putText` with the class
//     its wire key declares -- never through `literal`, and never into an
//     attribute a browser navigates from. An orchestrator's id is identity and
//     not prose (the contract's own reading): it is written as
//     `data-orchestrator` and never drawn.
//
// The counts are the document's own ints, and they enter through `measured` and
// leave through `putReading`, so there is no path by which a number reaches the
// screen without its state.
//
// **The drill-down is a read of this section and not a second screen.** A node's
// press reads `graph.read --source orchestrator:<id>` -- a scope CG7a's grammar
// already admits, so this screen adds no word to either allow-list -- and draws
// that orchestrator's own items under the canvas. It sits behind the same
// version floor as the map, because the release that reads a named orchestrator
// is the release that ships the map.

import { measured, unavailable } from '../../shared/availability'
import type { Reading } from '../../shared/availability'
import type { TextClass } from '../../shared/sanitize'
import type { GraphView, OrchestratorMapView } from '../../shared/schema'
import { ORCHESTRATOR_MAP_CLI_FLOOR } from '../../shared/version'
import type { ElementLike } from '../app'
import { putReading, putText } from '../app'
import { LayoutClient, WORKER_URL } from '../graph/layout-client'
import type {
  LayoutEdge,
  LayoutGraph,
  LayoutNode,
  LayoutRequest,
  PlacedNode,
  Positions,
  RouteSection,
  WorkerLike,
} from '../graph/layout-client'
import { BUSY_SLOT_CLASS } from '../section'

// ---------------------------------------------------------------------------
// The console's own words.
// ---------------------------------------------------------------------------

/** COORDINATOR_SOURCE_LINE is the command line this screen's document comes
 * from. It is the operation's whole argv, which is why it is one string and not
 * a table: the read fixes `--json` and nothing else, and the map takes no
 * argument a screen could turn off (`src/main/spawn.ts`). */
export const COORDINATOR_SOURCE_LINE = 'fugaro orchestrator map --json'

/** COORDINATOR_TITLE is the section's heading. It says **active**, which is the
 * one thing about this map a reader must not misread: an orchestrator that is
 * not on it is not idle, it is not being driven. */
export const COORDINATOR_TITLE = 'active orchestrators'

/** COORDINATOR_BUSY is what the section's mark says it is reading. */
export const COORDINATOR_BUSY = 'Reading the active orchestrators…'

/** COORDINATOR_TASKS_BUSY is the mark a node's press puts up while the
 * drill-down's own read is out: it names the read rather than saying "reading",
 * because the map on screen is not what the press asked for. */
export const COORDINATOR_TASKS_BUSY = 'Reading this orchestrator’s live tasks…'

/** COORDINATOR_EMPTY_WORD is the sentence an installation with nothing being
 * driven is drawn with. It is a fact and not a failure: the map holds no node
 * because no credential holds a lease, and a spinner over that would say the
 * console could not read a document it read. */
export const COORDINATOR_EMPTY_WORD = 'no orchestrator of this project is being driven'

/** COORDINATOR_NO_CANVAS is the sentence for a map the layout worker did not
 * place, for `GRAPH_NO_CANVAS`'s reason: a worker that failed is not a blank
 * diagram. The nodes are drawn as rows under it, so the same figures reach the
 * operator either way. */
export const COORDINATOR_NO_CANVAS =
  'the layout worker did not place this map, so it is drawn as a list'

/** COORDINATOR_DEPENDS_WORD and COORDINATOR_OVERLAP_WORD are the two edge kinds
 * as the console says them, in the fallback row that has no line to draw. The
 * document's own kinds are the identity the screen switches on (`dependency` and
 * `overlap`), never a word it draws. */
export const COORDINATOR_DEPENDS_WORD = 'waits on'
export const COORDINATOR_OVERLAP_WORD = 'overlaps'

/** COORDINATOR_OVERLAP_WARNING_WORD is what an overlap means, in words, beside
 * the count: the dashed line is the tone and this is the reading. */
export const COORDINATOR_OVERLAP_WARNING_WORD =
  'these orchestrators touch the same files or services, or their pull requests overlap'

/** COORDINATOR_OVERLAP_SINGULAR and COORDINATOR_OVERLAP_PLURAL are the count's
 * own words, so the sentence reads as a person writes it. */
export const COORDINATOR_OVERLAP_SINGULAR = 'overlap'
export const COORDINATOR_OVERLAP_PLURAL = 'overlaps'

/** COORDINATOR_SWEEP_OFF_WORD is the console's sentence for a map whose sweep is
 * off, and COORDINATOR_SWEEP_OFF_FIX_WORD is the switch that turns it on. The
 * two are one reading -- "nobody looked, and here is how to make somebody look"
 * -- and the fix names the installation's own variable rather than a command,
 * because the sweep is a scheduled job the installation's Terraform creates
 * (`deploy/terraform/gcp/modules/installation/coordinator.tf`). */
export const COORDINATOR_SWEEP_OFF_WORD = 'the sweep is off, so this map carries no overlap at all'
export const COORDINATOR_SWEEP_OFF_FIX_WORD =
  'set coordinator.deploy_job = true in the installation and run fugaro update'

/** COORDINATOR_HEALTH_WORD, COORDINATOR_HEALTH_ON_WORD and
 * COORDINATOR_HEALTH_OFF_WORD are the health line's own vocabulary: the sweep's
 * name and its two states, as words a reader can read off a greyscale
 * screenshot. */
export const COORDINATOR_HEALTH_WORD = 'sweep'
export const COORDINATOR_HEALTH_ON_WORD = 'on'
export const COORDINATOR_HEALTH_OFF_WORD = 'off'

/** COORDINATOR_HEALTH_BEAT_WORD introduces the heartbeat, and
 * COORDINATOR_HEALTH_NEVER_WORD is the sentence for a sweep that is on and has
 * never finished a read. The heartbeat is the CLI's own stamp, drawn as the
 * document wrote it: the console derives no age from it, because the screen
 * claims no clock it was not handed. */
export const COORDINATOR_HEALTH_BEAT_WORD = 'last woke'
export const COORDINATOR_HEALTH_NEVER_WORD = 'has not finished a sweep yet'

/** COORDINATOR_LEASE_WORD introduces a lease age, COORDINATOR_LEASE_AGE_WORD is
 * the unit it is drawn with, and COORDINATOR_NO_AGE_WORD is what a lease whose
 * node carries no readable stamp is drawn as. **Null is not zero** (the
 * contract's own reading 3): a zero would be a claim about a beat this reader
 * does not have. */
export const COORDINATOR_LEASE_WORD = 'lease'
export const COORDINATOR_LEASE_AGE_WORD = 'old'
export const COORDINATOR_NO_AGE_WORD = 'no readable lease stamp'

/** COORDINATOR_TASKS_COUNT_WORD and COORDINATOR_JOBS_COUNT_WORD are the two
 * figures a node is drawn with, and COORDINATOR_UNKNOWN_DRIVER_WORD is what a
 * node whose driver the CLI could not read says (contract reading 2: an empty
 * `driver` is a failed read, never "nobody drives this"). */
export const COORDINATOR_TASKS_COUNT_WORD = 'tasks'
export const COORDINATOR_JOBS_COUNT_WORD = 'jobs'
export const COORDINATOR_UNKNOWN_DRIVER_WORD = 'driver unknown'

/** COORDINATOR_TASKS_WORD is the drill-down panel's heading, and
 * COORDINATOR_TASKS_EMPTY_WORD is its sentence for an orchestrator whose own
 * document holds no item. */
export const COORDINATOR_TASKS_WORD = 'live tasks'
export const COORDINATOR_TASKS_EMPTY_WORD = 'this orchestrator holds no live task'

/** COORDINATOR_TASKS_NOT_UP_WORD is what the drill-down says when the read
 * answered, and the orchestrator's own row in the document's `sources[]` says
 * that source is not `up`: `down`, `stale`, `unreadable`, or a word this build
 * has no vocabulary for. The document's own state word and the reason it gave
 * are drawn beside this sentence.
 *
 * **A source that is not up is not an orchestrator holding nothing** (H3, and
 * `console/docs/json-contract.md` §6.22): a stream nobody could read answers
 * with no item for a reason of its own, and the empty sentence would report it
 * as a quiet one -- the one reading of this panel a reader would act on by
 * doing nothing. */
export const COORDINATOR_TASKS_NOT_UP_WORD = 'this orchestrator’s own source is not up'

/** COORDINATOR_TASKS_CLOSE_WORD is the panel's one control: a node press opens
 * the panel, and this closes it. It is the console's own word, so `literal` is
 * the only writer it needs. */
export const COORDINATOR_TASKS_CLOSE_WORD = 'Close'

/** COORDINATOR_FLOOR_WORD, COORDINATOR_FLOOR_AND_WORD and
 * COORDINATOR_FLOOR_VERSION are the sentence a CLI below the floor is drawn
 * with. The tab draws this **instead of** spawning a child: the map's command
 * does not exist in an older binary, so the read would be refused by the CLI
 * with a sentence about an unknown command rather than about the version this
 * tab needs. */
export const COORDINATOR_FLOOR_WORD = 'the Coordinator tab needs fugaro '
export const COORDINATOR_FLOOR_AND_WORD = ' and this CLI is '
export const COORDINATOR_FLOOR_VERSION = ORCHESTRATOR_MAP_CLI_FLOOR ?? ''

/** COORDINATOR_CANVAS_LABEL is the canvas's own accessible name, and
 * COORDINATOR_TASKS_OPERATION is the operation a refused drill-down names. */
export const COORDINATOR_CANVAS_LABEL = 'the active orchestrators, drawn as boxes and edges'
export const COORDINATOR_TASKS_OPERATION = 'graph.read'

// ---------------------------------------------------------------------------
// The hooks.
// ---------------------------------------------------------------------------

export const COORDINATOR_CLASS = 'coordinator'
export const COORDINATOR_HEADER_CLASS = 'coordinator-header'
export const COORDINATOR_TITLE_CLASS = 'coordinator-title'
export const COORDINATOR_SOURCE_CLASS = 'coordinator-source'
export const COORDINATOR_FLOOR_CLASS = 'coordinator-floor'
export const COORDINATOR_FLOOR_WORD_CLASS = 'coordinator-floor-word'
export const COORDINATOR_FLOOR_VERSION_CLASS = 'coordinator-floor-version'
export const COORDINATOR_HEALTH_CLASS = 'coordinator-health'
export const COORDINATOR_HEALTH_ITEM_CLASS = 'coordinator-health-item'
export const COORDINATOR_HEALTH_WORD_CLASS = 'coordinator-health-word'
export const COORDINATOR_HEALTH_VALUE_CLASS = 'coordinator-health-value'
export const COORDINATOR_LEASE_CLASS = 'coordinator-lease'
export const COORDINATOR_LEASE_NAME_CLASS = 'coordinator-lease-name'
export const COORDINATOR_LEASE_AGE_CLASS = 'coordinator-lease-age'
export const COORDINATOR_LEASE_WORD_CLASS = 'coordinator-lease-word'
export const COORDINATOR_SWEEP_OFF_CLASS = 'coordinator-sweep-off'
export const COORDINATOR_SWEEP_OFF_WORD_CLASS = 'coordinator-sweep-off-word'
export const COORDINATOR_SWEEP_OFF_FIX_CLASS = 'coordinator-sweep-off-fix'
export const COORDINATOR_SWEEP_OFF_NOTE_CLASS = 'coordinator-sweep-off-note'
export const COORDINATOR_WARNING_CLASS = 'coordinator-overlap-warning'
export const COORDINATOR_WARNING_WORD_CLASS = 'coordinator-overlap-warning-word'
export const COORDINATOR_WARNING_COUNT_CLASS = 'coordinator-overlap-warning-count'
export const COORDINATOR_EMPTY_CLASS = 'coordinator-empty'
export const COORDINATOR_NO_CANVAS_CLASS = 'coordinator-no-canvas'
export const COORDINATOR_GRAPH_CLASS = 'coordinator-graph'
// These two classes are passed to their emitter as the literal at the call
// site and not through the name here, the way the SVG emitters of graph.ts and
// live.ts pass theirs: the styling scan reads a class out of the call site's
// own argument, so a constant there leaves the attribute-qualified rule
// (`g.coordinator-node[aria-pressed]`, `span.coordinator-row-edge[data-kind]`)
// a rule it cannot match to any element. The names remain what the entry and
// the tests look the element up by.
export const COORDINATOR_NODE_CLASS = 'coordinator-node'
export const COORDINATOR_NODE_BOX_CLASS = 'coordinator-node-box'
export const COORDINATOR_NODE_NAME_CLASS = 'coordinator-node-name'
export const COORDINATOR_NODE_DRIVER_CLASS = 'coordinator-node-driver'
export const COORDINATOR_NODE_FACT_CLASS = 'coordinator-node-fact'
export const COORDINATOR_EDGE_CLASS = 'coordinator-edge'
export const COORDINATOR_EDGE_DEPENDS_CLASS = 'coordinator-edge-depends'
export const COORDINATOR_EDGE_OVERLAP_CLASS = 'coordinator-edge-overlap'
export const COORDINATOR_LIST_CLASS = 'coordinator-list'
export const COORDINATOR_ROW_CLASS = 'coordinator-row'
export const COORDINATOR_ROW_NAME_CLASS = 'coordinator-row-name'
export const COORDINATOR_ROW_EDGE_CLASS = 'coordinator-row-edge'
export const COORDINATOR_TASKS_CLASS = 'coordinator-tasks'
export const COORDINATOR_TASKS_HEAD_CLASS = 'coordinator-tasks-head'
export const COORDINATOR_TASKS_WORD_CLASS = 'coordinator-tasks-word'
export const COORDINATOR_TASKS_CLOSE_CLASS = 'coordinator-tasks-close'
export const COORDINATOR_TASKS_EMPTY_CLASS = 'coordinator-tasks-empty'
export const COORDINATOR_TASK_LIST_CLASS = 'coordinator-task-list'
export const COORDINATOR_TASK_CLASS = 'coordinator-task'
export const COORDINATOR_TASK_TITLE_CLASS = 'coordinator-task-title'
export const COORDINATOR_TASK_STATE_CLASS = 'coordinator-task-state'
export const COORDINATOR_TASK_REPO_CLASS = 'coordinator-task-repo'

/** COORDINATOR_ATTRIBUTE is where a node's id is written: identity, which a
 * press joins on, and never drawn. */
export const COORDINATOR_ATTRIBUTE = 'data-orchestrator'

/** COORDINATOR_WARNING_ATTRIBUTE is the mark an overlap carries for the rule
 * that dashes it and for a reader that cannot see the dash. */
export const COORDINATOR_WARNING_ATTRIBUTE = 'data-warning'

/** COORDINATOR_SWEEP_ATTRIBUTE is the health line's own state, so the sweep
 * being off is a fact in the document rather than only a colour. */
export const COORDINATOR_SWEEP_ATTRIBUTE = 'data-sweep'

/** COORDINATOR_NODE_WIDTH and COORDINATOR_NODE_HEIGHT are one box's size, and
 * they are this screen's own numbers rather than the text's: the layout worker
 * is given sizes and never text (`layout-client.ts`), so the box a node's lines
 * are drawn into and the box the engine places are the one number. */
export const COORDINATOR_NODE_WIDTH = 260
export const COORDINATOR_NODE_HEIGHT = 92

/** COORDINATOR_PAD is the room the canvas keeps around the placed boxes. */
export const COORDINATOR_PAD = 18

/** COORDINATOR_THEMES is the two themes this screen draws in. The tab's one
 * section shape is `COORDINATOR_SHAPE`, which lives in `skeleton.ts` beside
 * every other screen's. */
export const COORDINATOR_THEMES = Object.freeze(['light', 'dark'] as const)

export type CoordinatorTheme = (typeof COORDINATOR_THEMES)[number]

// ---------------------------------------------------------------------------
// The DOM this screen draws on.
// ---------------------------------------------------------------------------

/** ScreenElement is the whole of the DOM this file touches. It is declared here
 * as well as in the sibling screens rather than imported from any of them,
 * because the screens are siblings and none owns another; the declarations are
 * structurally identical, and `coordinator.test.ts` draws this screen through a
 * factory of its own -- so a drift is a failing test rather than a screen that
 * compiles and draws nothing. */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

/** ScreenFactory is where HTML elements come from. */
export interface ScreenFactory {
  create(tag: string): ScreenElement
}

/** CoordinatorFactory is a `ScreenFactory` that can also make an element in the
 * **SVG** namespace: the graph's geometry is SVG and the frame around it is not,
 * and the two are two calls in the DOM rather than one flag. */
export interface CoordinatorFactory extends ScreenFactory {
  createNS(tag: string): ScreenElement
}

/** SVG_NAMESPACE_URI is the namespace the canvas is made in. It is the
 * namespace's name and not a URL anything fetches; `entry.ts`'s `createNS`
 * names this constant rather than a second copy of the literal. */
export const SVG_NAMESPACE_URI = 'http://www.w3.org/2000/svg'

// ---------------------------------------------------------------------------
// The model.
// ---------------------------------------------------------------------------

/** COORDINATOR_DEPENDENCY and COORDINATOR_OVERLAP are the document's two edge
 * kinds as the CLI writes them. A third is drawn as neither: the contract
 * carries `kind` as a string so a relation this build has not learned is a word
 * the drawing leaves out rather than a whole map refused. */
export const COORDINATOR_DEPENDENCY = 'dependency'
export const COORDINATOR_OVERLAP = 'overlap'

/** CoordinatorNode is one active orchestrator as the drawing needs it: the id
 * (identity, written as an attribute and never drawn), the name the box is
 * headed by, the principal driving it, the two counts and the lease age. */
export interface CoordinatorNode {
  readonly id: string
  /** `name` is the document's own, `''` for the project's orchestrator. */
  readonly name: string
  readonly driver: string
  readonly activeTasks: number
  readonly runningJobs: number
  readonly leaseAgeSeconds: number | null
}

/** CoordinatorEdge is one connection: the two ids, the document's own `kind`
 * and the words an overlap carries. */
export interface CoordinatorEdge {
  readonly from: string
  readonly to: string
  readonly kind: string
  readonly reason: string
}

/** CoordinatorHealth is the coordinator's own heartbeat, plus the console's own
 * reading of the one thing it decides: whether the sweep ran at all. */
export interface CoordinatorHealth {
  readonly sweep: boolean
  readonly okAt: string | null
  readonly note: string
}

/** CoordinatorModel is one map document, with the counts the drawing needs
 * computed once: the overlaps, which are the dashed edges and the warning. */
export interface CoordinatorModel {
  readonly project: string
  readonly nodes: readonly CoordinatorNode[]
  readonly edges: readonly CoordinatorEdge[]
  readonly health: CoordinatorHealth
  readonly overlaps: number
}

/** buildCoordinatorModel folds one map document into the drawing's data. It
 * changes nothing: every field is carried as the decoder read it, and the one
 * thing it computes is the overlap count, which the warning and the fallback
 * rows both need. */
export function buildCoordinatorModel(view: OrchestratorMapView): CoordinatorModel {
  const nodes: CoordinatorNode[] = view.nodes.map((node) => ({
    id: node.id,
    name: node.name,
    driver: node.driver,
    activeTasks: node.activeTasks,
    runningJobs: node.runningJobs,
    leaseAgeSeconds: node.leaseAgeSeconds,
  }))
  const edges: CoordinatorEdge[] = view.edges.map((edge) => ({
    from: edge.from,
    to: edge.to,
    kind: edge.kind,
    reason: edge.reason,
  }))
  return {
    project: view.project,
    nodes,
    edges,
    health: { sweep: view.health.sweep, okAt: view.health.okAt, note: view.health.note },
    overlaps: edges.filter((edge) => edge.kind === COORDINATOR_OVERLAP).length,
  }
}

/** CoordinatorTask is one item of the orchestrator a node's press opened: the
 * key the document joins on, and the three strings a row draws. */
export interface CoordinatorTask {
  readonly key: string
  readonly title: string
  readonly state: string
  readonly repo: string
}

/** CoordinatorAnswer is what one orchestrator's own read is drawn as: the rows
 * its document holds, or the sentence for a scope whose own source is not up.
 * It is narrower than `CoordinatorTasks` because a read that answered is never
 * `reading` and never `closed`. */
export type CoordinatorAnswer =
  | { readonly kind: 'drawn'; readonly tasks: readonly CoordinatorTask[] }
  | { readonly kind: 'refused'; readonly sentence: string }

/** COORDINATOR_SOURCE_UP is the one state a scope's `sources[]` row can carry
 * that means its answer is this document's own. Every other word -- `down`,
 * `stale`, `unreadable`, and any word this build has no vocabulary for -- is a
 * source that is not answering now, and the contract deliberately leaves the set
 * open (`schema.ts`). It is a copy of `graph/model.ts`'s private `SOURCE_UP`
 * rather than an import: this screen draws from the document and not from the
 * Graph screen's model, and the two are siblings that own neither each other nor
 * the CLI's word. */
const COORDINATOR_SOURCE_UP = 'up'

/** coordinatorTasks reads one `graph.read --source orchestrator:<id>` document
 * as the drill-down's own state, in the document's own order. Every node that
 * document holds is one of the orchestrator's items: the scope read is that
 * orchestrator's own state node, so the console filters nothing out of it and
 * adds no row of its own.
 *
 * **`scope` is the scope the read was made with**, and the document's `sources[]`
 * row carrying it is what decides whether an empty `nodes[]` means "no live
 * task" or "nobody could read this stream": a row whose `state` is not `up` is
 * drawn as its state word and reason, and never as the empty sentence. A
 * document that lists no row for the scope is read as it always was -- this
 * build knows no reason to doubt a scope the CLI answered about, and inventing
 * one would draw a doubt the document never stated. */
export function coordinatorTasks(view: GraphView, scope: string): CoordinatorAnswer {
  const source = view.sources.find((entry) => entry.source === scope)
  if (source !== undefined && source.state !== COORDINATOR_SOURCE_UP) {
    const reason = source.reason === '' ? '' : ` — ${source.reason}`
    return {
      kind: 'refused',
      sentence: `${COORDINATOR_TASKS_NOT_UP_WORD}: ${source.state}${reason}`,
    }
  }
  return {
    kind: 'drawn',
    tasks: view.nodes.map((node) => ({
      key: node.key,
      title: node.title,
      state: node.state,
      repo: node.repo,
    })),
  }
}

/** CoordinatorTasks is the drill-down's own state: closed, reading, drawn, or
 * refused with the CLI's sentence. It is a union rather than three fields, so
 * "the read is out" and "the read answered with nothing" cannot be drawn as the
 * same panel. */
export type CoordinatorTasks =
  | { readonly kind: 'closed' }
  | { readonly kind: 'reading' }
  | { readonly kind: 'drawn'; readonly tasks: readonly CoordinatorTask[] }
  | { readonly kind: 'refused'; readonly sentence: string }

/** CoordinatorPicture is everything one drawing of the section is a function
 * of. It is a value rather than a screen's mutable state so the drawing can be
 * asserted on with no window and no worker: `model === null` is the read still
 * out, and `positions === null` is a map the worker did not place. */
export interface CoordinatorPicture {
  readonly model: CoordinatorModel | null
  readonly positions: Positions | null
  readonly selected: string | null
  readonly tasks: CoordinatorTasks
  /** notice is the CLI's or the decoder's own sentence for a refused map read,
   * drawn under the header: a refusal is not this screen's to paraphrase. */
  readonly notice: string | null
}

// ---------------------------------------------------------------------------
// The layout.
// ---------------------------------------------------------------------------

/** COORDINATOR_LAYER_OPTION, COORDINATOR_NODE_OPTION and
 * COORDINATOR_COMPONENT_OPTION are the three gaps this screen asks the engine
 * for, and COORDINATOR_GAP is the number. The engine's own room is 20 px, which
 * is less than the lines a box draws under its name: a dependency's edge would
 * be routed through the box it starts at, and an isolated orchestrator's box
 * would be drawn touching the next one. */
export const COORDINATOR_LAYER_OPTION = 'elk.layered.spacing.nodeNodeBetweenLayers'
export const COORDINATOR_NODE_OPTION = 'elk.spacing.nodeNode'
export const COORDINATOR_COMPONENT_OPTION = 'elk.spacing.componentComponent'
export const COORDINATOR_GAP = COORDINATOR_PAD * 2

/** coordinatorLayout is one model as the layout worker's own JSON graph: the
 * boxes with their sizes and the connections between them. It carries **ids and
 * sizes and never text** (`layout-client.ts`), so no orchestrator's name reaches
 * the worker. */
export function coordinatorLayout(model: CoordinatorModel): LayoutGraph {
  const children: LayoutNode[] = model.nodes.map((node) => ({
    id: node.id,
    width: COORDINATOR_NODE_WIDTH,
    height: COORDINATOR_NODE_HEIGHT,
  }))
  const edges: LayoutEdge[] = model.edges.map((edge, index) => ({
    id: coordinatorEdgeId(edge, index),
    sources: [edge.from],
    targets: [edge.to],
  }))
  return { id: 'coordinator', children, edges }
}

/** coordinatorEdgeId is one edge's layout id. It is the index and not the two
 * ids: two orchestrators may overlap **and** depend on each other, so a pair is
 * not an identity, and the worker refuses a graph whose edges share one. */
function coordinatorEdgeId(edge: CoordinatorEdge, index: number): string {
  return `edge-${String(index)}-${edge.kind}`
}

/** CoordinatorRect is one box's rectangle. */
export interface CoordinatorRect {
  readonly x: number
  readonly y: number
  readonly width: number
  readonly height: number
}

/** CoordinatorBox is one node placed: the node and the rectangle the engine
 * answered with. */
export interface CoordinatorBox {
  readonly node: CoordinatorNode
  readonly rect: CoordinatorRect
}

/** CoordinatorRoute is one edge placed: the edge and the route the engine
 * answered with. */
export interface CoordinatorRoute {
  readonly edge: CoordinatorEdge
  readonly sections: readonly RouteSection[]
}

/** CoordinatorDrawing is a placed model: the boxes and the routes, in the
 * document's own order. */
export interface CoordinatorDrawing {
  readonly boxes: readonly CoordinatorBox[]
  readonly routes: readonly CoordinatorRoute[]
  readonly width: number
  readonly height: number
}

/** coordinatorDrawing places one model with the positions the worker answered
 * with, or answers `null` for a layout this screen did not ask about -- a box or
 * a route the engine invented is not a drawing this console will make. */
export function coordinatorDrawing(
  model: CoordinatorModel,
  positions: Positions,
): CoordinatorDrawing | null {
  const placed = new Map<string, PlacedNode>()
  for (const node of positions.nodes) placed.set(node.id, node)
  const boxes: CoordinatorBox[] = []
  for (const node of model.nodes) {
    const at = placed.get(node.id)
    if (at === undefined) return null
    boxes.push({ node, rect: { x: at.x, y: at.y, width: at.width, height: at.height } })
  }
  const routes: CoordinatorRoute[] = []
  for (const [index, edge] of model.edges.entries()) {
    const found = positions.edges.find((one) => one.id === coordinatorEdgeId(edge, index))
    if (found === undefined) return null
    routes.push({ edge, sections: found.sections })
  }
  if (boxes.length === 0) return { boxes, routes, width: 0, height: 0 }
  const left = Math.min(...boxes.map((box) => box.rect.x))
  const top = Math.min(...boxes.map((box) => box.rect.y))
  const right = Math.max(...boxes.map((box) => box.rect.x + box.rect.width))
  const bottom = Math.max(...boxes.map((box) => box.rect.y + box.rect.height))
  return {
    boxes,
    routes,
    width: right - left + COORDINATOR_PAD * 2,
    height: bottom - top + COORDINATOR_PAD * 2,
  }
}

/** CoordinatorLayoutClient is the layout client this screen asks for a drawing:
 * the plain client, with the three gaps added to every request. */
export class CoordinatorLayoutClient extends LayoutClient {
  override layout(graph: LayoutGraph, request: LayoutRequest = {}): Promise<Positions> {
    return super.layout(graph, {
      ...request,
      layoutOptions: {
        ...request.layoutOptions,
        [COORDINATOR_LAYER_OPTION]: String(COORDINATOR_GAP),
        [COORDINATOR_NODE_OPTION]: String(COORDINATOR_GAP),
        [COORDINATOR_COMPONENT_OPTION]: String(COORDINATOR_GAP),
      },
    })
  }
}

/** coordinatorLayoutClient is this screen's worker, the way
 * `graphLayoutClient()` is the Graph's: a classic worker from the served file,
 * built when the tab is first opened and disposed of when the console moves to
 * another installation. */
export function coordinatorLayoutClient(): LayoutClient {
  return new CoordinatorLayoutClient(() => new Worker(WORKER_URL) as unknown as WorkerLike)
}

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

/** make is a fresh element of the console's own making, in the HTML namespace. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  return element
}

/** makeNS is `make` in the SVG namespace. */
function makeNS(factory: CoordinatorFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.createNS(tag)
  element.setAttribute('class', classes.join(' '))
  return element
}

/** literal writes the console's own vocabulary -- never a document's string,
 * which comes through `putText` (`../app.ts`) or not at all. */
function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

/** cell is a cell of the console's own making, appended to a parent. */
function cell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  ...classes: string[]
): ScreenElement {
  const element = make(factory, tag, ...classes)
  parent.append(element)
  return element
}

/** cellNS is `cell` in the SVG namespace. */
function cellNS(
  parent: ScreenElement,
  factory: CoordinatorFactory,
  tag: string,
  ...classes: string[]
): ScreenElement {
  const element = makeNS(factory, tag, ...classes)
  parent.append(element)
  return element
}

/** textCell is `cell` with a document's string written into it through
 * `putText`, with the class its wire key declares. */
function textCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  raw: unknown,
  textClass: TextClass,
  field: string,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, ...classes)
  putText(element, raw, textClass, field)
  return element
}

/** textCellNS is `textCell` in the SVG namespace: a document's string as an SVG
 * `text` element's content, through the same door. */
function textCellNS(
  parent: ScreenElement,
  factory: CoordinatorFactory,
  raw: unknown,
  textClass: TextClass,
  field: string,
  ...classes: string[]
): ScreenElement {
  const element = cellNS(parent, factory, 'text', ...classes)
  putText(element, raw, textClass, field)
  return element
}

/** countCellNS is a document's own count as an SVG `tspan`, through
 * `putReading`, so no figure is drawn without its state. */
function countCellNS(
  parent: ScreenElement,
  factory: CoordinatorFactory,
  reading: Reading,
  ...classes: string[]
): ScreenElement {
  const element = cellNS(parent, factory, 'tspan', ...classes)
  putReading(element, reading)
  return element
}

/** wordCellNS is one of the console's own words as an SVG `tspan`. */
function wordCellNS(
  parent: ScreenElement,
  factory: CoordinatorFactory,
  word: string,
  ...classes: string[]
): ScreenElement {
  return literal(cellNS(parent, factory, 'tspan', ...classes), word)
}

/** coord writes one coordinate. Every number that reaches SVG geometry goes
 * through here, so a finite number the layout client let through is the only
 * kind of value that can be spelled into an attribute. */
function coord(value: number): string {
  return String(value)
}

/** geometry writes one SVG geometry attribute. */
function geometry(element: ScreenElement, name: string, value: number): ScreenElement {
  element.setAttribute(name, coord(value))
  return element
}

/** viewBox writes a canvas's own window onto the drawing: four numbers, which is
 * why it is not `geometry`. */
function viewBox(element: ScreenElement, width: number, height: number): ScreenElement {
  element.setAttribute('viewBox', `0 0 ${coord(width)} ${coord(height)}`)
  return element
}

/** shift places a group at a point: a box's own coordinates are its rectangle's,
 * and the group carries the engine's answer as a translation. */
function shift(element: ScreenElement, x: number, y: number): ScreenElement {
  element.setAttribute('transform', `translate(${coord(x)} ${coord(y)})`)
  return element
}

/** pathOf is a routed edge as one SVG path: one `M`/`L` run per section, with
 * the engine's own bend points in between. */
function pathOf(sections: readonly RouteSection[]): string {
  return sections
    .map((section) => {
      const bends = (section.bendPoints ?? []).map((point) => `L ${coord(point.x)} ${coord(point.y)}`)
      const start = `M ${coord(section.startPoint.x)} ${coord(section.startPoint.y)}`
      const end = `L ${coord(section.endPoint.x)} ${coord(section.endPoint.y)}`
      return [start, ...bends, end].join(' ')
    })
    .join(' ')
}

/** textAt is a `text` element of a box's own, on its own line. */
function textAt(
  parent: ScreenElement,
  factory: CoordinatorFactory,
  y: number,
  ...classes: string[]
): ScreenElement {
  const element = cellNS(parent, factory, 'text', ...classes)
  geometry(element, 'x', COORDINATOR_PAD)
  geometry(element, 'y', y)
  return element
}

/** nodeName is the name a box is headed by: the document's own, or the project's
 * name for the default orchestrator, which the contract writes as the empty
 * string (contract reading 1: an empty label would be a box a reader cannot tell
 * from a failed read). */
function nodeName(model: CoordinatorModel, node: CoordinatorNode): string {
  return node.name === '' ? model.project : node.name
}

/** leaseReading is one node's lease age as a reading: the document's own number
 * in the `duration` unit, or the console's own sentence for a lease node that
 * carried no readable stamp. */
function leaseReading(node: CoordinatorNode): Reading {
  if (node.leaseAgeSeconds === null) {
    return unavailable(COORDINATOR_NO_AGE_WORD)
  }
  return measured(node.leaseAgeSeconds)
}

/** countLine draws one of a node's two counts: the console's word for what it
 * counts, then the document's own number through `putReading`. */
function countLine(
  parent: ScreenElement,
  factory: CoordinatorFactory,
  y: number,
  word: string,
  value: number,
): ScreenElement {
  const element = textAt(parent, factory, y, COORDINATOR_NODE_FACT_CLASS)
  wordCellNS(element, factory, `${word} `)
  countCellNS(element, factory, measured(value))
  return element
}

/** nodeBox draws one orchestrator: the box, its name, its driver and its two
 * counts, inside a group that carries the id a press joins on. */
function nodeBox(
  factory: CoordinatorFactory,
  model: CoordinatorModel,
  box: CoordinatorBox,
  selected: boolean,
): ScreenElement {
  const group = makeNS(factory, 'g', 'coordinator-node')
  group.setAttribute(COORDINATOR_ATTRIBUTE, box.node.id)
  group.setAttribute('role', 'button')
  group.setAttribute('tabindex', '0')
  if (selected) group.setAttribute('aria-pressed', 'true')
  shift(group, box.rect.x, box.rect.y)
  const rect = cellNS(group, factory, 'rect', COORDINATOR_NODE_BOX_CLASS)
  geometry(rect, 'width', box.rect.width)
  geometry(rect, 'height', box.rect.height)
  const name = textCellNS(
    group,
    factory,
    nodeName(model, box.node),
    'label',
    'nodes[].name',
    COORDINATOR_NODE_NAME_CLASS,
  )
  geometry(name, 'x', COORDINATOR_PAD)
  geometry(name, 'y', 24)
  if (box.node.driver === '') {
    literal(textAt(group, factory, 44, COORDINATOR_NODE_DRIVER_CLASS), COORDINATOR_UNKNOWN_DRIVER_WORD)
  } else {
    const driver = textCellNS(
      group,
      factory,
      box.node.driver,
      'person',
      'nodes[].driver',
      COORDINATOR_NODE_DRIVER_CLASS,
    )
    geometry(driver, 'x', COORDINATOR_PAD)
    geometry(driver, 'y', 44)
  }
  countLine(group, factory, 64, COORDINATOR_TASKS_COUNT_WORD, box.node.activeTasks)
  countLine(group, factory, 80, COORDINATOR_JOBS_COUNT_WORD, box.node.runningJobs)
  return group
}

/** edgePath draws one route: a `path` carrying the kind's own class, the mark an
 * overlap carries, and -- for an overlap with words -- the reason the document
 * wrote, as the path's own tooltip. */
function edgePath(factory: CoordinatorFactory, route: CoordinatorRoute): ScreenElement {
  const overlap = route.edge.kind === COORDINATOR_OVERLAP
  const path = makeNS(
    factory,
    'path',
    COORDINATOR_EDGE_CLASS,
    overlap ? COORDINATOR_EDGE_OVERLAP_CLASS : COORDINATOR_EDGE_DEPENDS_CLASS,
  )
  path.setAttribute('d', pathOf(route.sections))
  if (overlap) path.setAttribute(COORDINATOR_WARNING_ATTRIBUTE, 'true')
  if (overlap && route.edge.reason !== '') {
    // The document's own words for what the two share: an overlap's `reason` is
    // the one thing the dashed line cannot say, and it is free text the
    // coordinator's sweep wrote, so it goes through the door every document
    // string comes through -- a `<title>` inside an `<svg>` is the tooltip.
    const title = makeNS(factory, 'title')
    putText(title, route.edge.reason, 'sentence', 'edges[].reason')
    path.append(title)
  }
  return path
}

/** healthLine draws the coordinator's own heartbeat and one lease row per node:
 * the sweep's state as a word, when it last finished a read, and how long ago
 * each holder last beat. */
function healthLine(factory: CoordinatorFactory, model: CoordinatorModel): ScreenElement {
  const root = make(factory, 'div', COORDINATOR_HEALTH_CLASS)
  root.setAttribute(COORDINATOR_SWEEP_ATTRIBUTE, model.health.sweep ? 'on' : 'off')
  const sweep = cell(root, factory, 'p', COORDINATOR_HEALTH_ITEM_CLASS)
  literal(cell(sweep, factory, 'span', COORDINATOR_HEALTH_WORD_CLASS), COORDINATOR_HEALTH_WORD)
  literal(
    cell(sweep, factory, 'span', COORDINATOR_HEALTH_VALUE_CLASS),
    model.health.sweep ? COORDINATOR_HEALTH_ON_WORD : COORDINATOR_HEALTH_OFF_WORD,
  )
  if (model.health.sweep) {
    const beat = cell(root, factory, 'p', COORDINATOR_HEALTH_ITEM_CLASS)
    literal(cell(beat, factory, 'span', COORDINATOR_HEALTH_WORD_CLASS), COORDINATOR_HEALTH_BEAT_WORD)
    if (model.health.okAt === null) {
      literal(
        cell(beat, factory, 'span', COORDINATOR_HEALTH_VALUE_CLASS),
        COORDINATOR_HEALTH_NEVER_WORD,
      )
    } else {
      textCell(
        beat,
        factory,
        'span',
        model.health.okAt,
        'sentence',
        'health.ok_at',
        COORDINATOR_HEALTH_VALUE_CLASS,
      )
    }
  }
  for (const node of model.nodes) {
    const row = cell(root, factory, 'p', COORDINATOR_LEASE_CLASS)
    row.setAttribute(COORDINATOR_ATTRIBUTE, node.id)
    literal(cell(row, factory, 'span', COORDINATOR_LEASE_WORD_CLASS), COORDINATOR_LEASE_WORD)
    textCell(
      row,
      factory,
      'span',
      nodeName(model, node),
      'label',
      'nodes[].name',
      COORDINATOR_LEASE_NAME_CLASS,
    )
    const age = cell(row, factory, 'span', COORDINATOR_LEASE_AGE_CLASS)
    putReading(age, leaseReading(node), 'duration')
    literal(cell(row, factory, 'span', COORDINATOR_LEASE_WORD_CLASS), COORDINATOR_LEASE_AGE_WORD)
  }
  return root
}

/** sweepOffNote draws the console's sentence for a map whose sweep is off: what
 * it means for this drawing, the switch that turns the sweep on, and the CLI's
 * own `note` beside them. It is drawn **instead of** any claim about overlaps,
 * because a map read with the sweep off carries none (D3211). */
function sweepOffNote(factory: CoordinatorFactory, model: CoordinatorModel): ScreenElement {
  const root = make(factory, 'p', COORDINATOR_SWEEP_OFF_CLASS)
  literal(cell(root, factory, 'span', COORDINATOR_SWEEP_OFF_WORD_CLASS), COORDINATOR_SWEEP_OFF_WORD)
  literal(cell(root, factory, 'span', COORDINATOR_SWEEP_OFF_FIX_CLASS), COORDINATOR_SWEEP_OFF_FIX_WORD)
  if (model.health.note !== '') {
    textCell(
      root,
      factory,
      'span',
      model.health.note,
      'sentence',
      'health.note',
      COORDINATOR_SWEEP_OFF_NOTE_CLASS,
    )
  }
  return root
}

/** overlapWarning draws the count of dashed edges and what they mean. It is
 * drawn only over a map whose sweep ran: with the sweep off the count would be a
 * zero the console has no right to claim (D3211). */
function overlapWarning(
  factory: CoordinatorFactory,
  model: CoordinatorModel,
): ScreenElement | null {
  if (!model.health.sweep || model.overlaps === 0) return null
  const root = make(factory, 'p', COORDINATOR_WARNING_CLASS)
  const count = cell(root, factory, 'span', COORDINATOR_WARNING_COUNT_CLASS)
  putReading(count, measured(model.overlaps))
  literal(
    cell(root, factory, 'span', COORDINATOR_WARNING_WORD_CLASS),
    `${model.overlaps === 1 ? COORDINATOR_OVERLAP_SINGULAR : COORDINATOR_OVERLAP_PLURAL} — ${COORDINATOR_OVERLAP_WARNING_WORD}`,
  )
  return root
}

/** canvas draws the placed graph: one `path` per route under one `g` per box, so
 * every edge is drawn before every box and a box's own fill covers the end of a
 * route that reaches it. */
function canvas(
  factory: CoordinatorFactory,
  model: CoordinatorModel,
  drawing: CoordinatorDrawing,
  selected: string | null,
): ScreenElement {
  const svg = makeNS(factory, 'svg', COORDINATOR_GRAPH_CLASS)
  svg.setAttribute('role', 'img')
  svg.setAttribute('aria-label', COORDINATOR_CANVAS_LABEL)
  geometry(svg, 'width', drawing.width)
  geometry(svg, 'height', drawing.height)
  viewBox(svg, drawing.width, drawing.height)
  const inner = makeNS(factory, 'g')
  svg.append(inner)
  shift(inner, COORDINATOR_PAD, COORDINATOR_PAD)
  for (const route of drawing.routes) inner.append(edgePath(factory, route))
  for (const box of drawing.boxes) {
    inner.append(nodeBox(factory, model, box, box.node.id === selected))
  }
  return svg
}

/** factRow is one of a fallback row's two counts: the console's word for what it
 * counts, then the document's own number through `putReading`. */
function factRow(parent: ScreenElement, factory: ScreenFactory, word: string, value: number): void {
  const element = cell(parent, factory, 'span', COORDINATOR_NODE_FACT_CLASS)
  literal(cell(element, factory, 'span'), `${word} `)
  putReading(cell(element, factory, 'span'), measured(value))
}

/** fallbackList draws the map as rows, for a worker that did not place it: each
 * node's name, driver, counts and lease age, then one row per connection it is
 * an end of. It is the same figures and the same words the canvas draws, which is
 * what makes the sentence above it a fallback rather than a failure. */
function fallbackList(factory: ScreenFactory, model: CoordinatorModel): ScreenElement {
  const list = make(factory, 'ul', COORDINATOR_LIST_CLASS)
  for (const node of model.nodes) {
    const row = cell(list, factory, 'li', COORDINATOR_ROW_CLASS)
    row.setAttribute(COORDINATOR_ATTRIBUTE, node.id)
    textCell(
      row,
      factory,
      'span',
      nodeName(model, node),
      'label',
      'nodes[].name',
      COORDINATOR_ROW_NAME_CLASS,
    )
    if (node.driver === '') {
      literal(
        cell(row, factory, 'span', COORDINATOR_NODE_DRIVER_CLASS),
        COORDINATOR_UNKNOWN_DRIVER_WORD,
      )
    } else {
      textCell(
        row,
        factory,
        'span',
        node.driver,
        'person',
        'nodes[].driver',
        COORDINATOR_NODE_DRIVER_CLASS,
      )
    }
    factRow(row, factory, COORDINATOR_TASKS_COUNT_WORD, node.activeTasks)
    factRow(row, factory, COORDINATOR_JOBS_COUNT_WORD, node.runningJobs)
    const age = cell(row, factory, 'span', COORDINATOR_LEASE_AGE_CLASS)
    putReading(age, leaseReading(node), 'duration')
    for (const edge of model.edges) {
      if (edge.from !== node.id && edge.to !== node.id) continue
      const otherId = edge.from === node.id ? edge.to : edge.from
      const other = model.nodes.find((one) => one.id === otherId)
      const line = cell(row, factory, 'span', 'coordinator-row-edge')
      line.setAttribute('data-kind', edge.kind)
      literal(
        cell(line, factory, 'span'),
        `${edge.kind === COORDINATOR_OVERLAP ? COORDINATOR_OVERLAP_WORD : COORDINATOR_DEPENDS_WORD} `,
      )
      // The other end is drawn under the name the canvas draws it under -- the
      // project's own name for the default orchestrator -- and not as the
      // document's raw id: an id is a join key, the contract says every edge
      // joins two nodes of its own document, and the id is the fallback for a
      // document that breaks that rule rather than a second thing to draw.
      textCell(
        line,
        factory,
        'span',
        other === undefined ? otherId : nodeName(model, other),
        'label',
        'nodes[].name',
      )
    }
  }
  return list
}

/** tasksPanel draws the drill-down: the orchestrator's own items, the mark the
 * read is still out under, the CLI's sentence for a refused read, or the
 * console's own sentence for an orchestrator that holds no item. */
function tasksPanel(factory: ScreenFactory, picture: CoordinatorPicture): ScreenElement | null {
  if (picture.selected === null || picture.model === null) return null
  const selected = picture.model.nodes.find((node) => node.id === picture.selected)
  if (selected === undefined) return null
  const root = make(factory, 'section', COORDINATOR_TASKS_CLASS)
  root.setAttribute(COORDINATOR_ATTRIBUTE, selected.id)
  const head = cell(root, factory, 'div', COORDINATOR_TASKS_HEAD_CLASS)
  literal(cell(head, factory, 'h2', COORDINATOR_TASKS_WORD_CLASS), COORDINATOR_TASKS_WORD)
  textCell(
    head,
    factory,
    'span',
    nodeName(picture.model, selected),
    'label',
    'nodes[].name',
    COORDINATOR_ROW_NAME_CLASS,
  )
  const close = cell(head, factory, 'button', COORDINATOR_TASKS_CLOSE_CLASS)
  close.setAttribute('type', 'button')
  literal(close, COORDINATOR_TASKS_CLOSE_WORD)
  const slot = cell(root, factory, 'div', BUSY_SLOT_CLASS)
  if (picture.tasks.kind === 'reading') {
    literal(cell(slot, factory, 'p', COORDINATOR_TASKS_EMPTY_CLASS), COORDINATOR_TASKS_BUSY)
    return root
  }
  if (picture.tasks.kind === 'refused') {
    const note = cell(slot, factory, 'p', COORDINATOR_TASKS_EMPTY_CLASS)
    putText(note, picture.tasks.sentence, 'sentence', COORDINATOR_TASKS_OPERATION)
    return root
  }
  if (picture.tasks.kind === 'closed') return root
  if (picture.tasks.tasks.length === 0) {
    literal(cell(slot, factory, 'p', COORDINATOR_TASKS_EMPTY_CLASS), COORDINATOR_TASKS_EMPTY_WORD)
    return root
  }
  const list = cell(slot, factory, 'ul', COORDINATOR_TASK_LIST_CLASS)
  for (const task of picture.tasks.tasks) {
    const row = cell(list, factory, 'li', COORDINATOR_TASK_CLASS)
    row.setAttribute('data-key', task.key)
    textCell(
      row,
      factory,
      'span',
      task.title,
      'sentence',
      'nodes[].title',
      COORDINATOR_TASK_TITLE_CLASS,
    )
    textCell(
      row,
      factory,
      'span',
      task.state,
      'sentence',
      'nodes[].state',
      COORDINATOR_TASK_STATE_CLASS,
    )
    if (task.repo !== '') {
      textCell(
        row,
        factory,
        'span',
        task.repo,
        'repository',
        'nodes[].repo',
        COORDINATOR_TASK_REPO_CLASS,
      )
    }
  }
  return root
}

/** header is the section's heading and the command line it came from. */
function header(factory: ScreenFactory): ScreenElement {
  const element = make(factory, 'header', COORDINATOR_HEADER_CLASS)
  literal(cell(element, factory, 'h1', COORDINATOR_TITLE_CLASS), COORDINATOR_TITLE)
  literal(cell(element, factory, 'p', COORDINATOR_SOURCE_CLASS), COORDINATOR_SOURCE_LINE)
  return element
}

/** renderCoordinatorFloor draws the tab for a CLI below the floor: the header
 * and the console's own sentence naming the release the read needs. It draws no
 * canvas and no mark, because there is no read to make. */
export function renderCoordinatorFloor(
  factory: CoordinatorFactory,
  theme: CoordinatorTheme,
  version: string,
): ScreenElement {
  const root = make(factory, 'section', COORDINATOR_CLASS)
  root.setAttribute('data-theme', theme)
  root.append(header(factory))
  const note = make(factory, 'p', COORDINATOR_FLOOR_CLASS)
  literal(cell(note, factory, 'span', COORDINATOR_FLOOR_WORD_CLASS), COORDINATOR_FLOOR_WORD)
  literal(cell(note, factory, 'span', COORDINATOR_FLOOR_VERSION_CLASS), COORDINATOR_FLOOR_VERSION)
  literal(cell(note, factory, 'span', COORDINATOR_FLOOR_WORD_CLASS), COORDINATOR_FLOOR_AND_WORD)
  textCell(note, factory, 'span', version, 'label', 'cliVersion', COORDINATOR_FLOOR_VERSION_CLASS)
  root.append(note)
  return root
}

/**
 * renderCoordinator draws the tab's **one** section from one picture: the
 * header, the health line, the overlap warning, the canvas or its list fallback,
 * and the drill-down panel when a node is open.
 *
 * **It is a pure function of the picture**, which is what makes the drawing
 * assertable with no window and no worker: a caller that holds a map document
 * and a set of positions draws exactly what the screen draws, and every case in
 * `coordinator.test.ts` is a case about this function.
 *
 * The busy slot is the section's first child, for `renderGraph`'s reason: the
 * mark of a read that is running goes above the figures already drawn, and a
 * slot that is not there is a mark with nowhere to go.
 */
export function renderCoordinator(
  factory: CoordinatorFactory,
  picture: CoordinatorPicture,
  theme: CoordinatorTheme,
): ScreenElement {
  const root = make(factory, 'section', COORDINATOR_CLASS)
  root.setAttribute('data-theme', theme)
  cell(root, factory, 'div', BUSY_SLOT_CLASS)
  root.append(header(factory))
  if (picture.notice !== null) {
    const note = cell(root, factory, 'p', COORDINATOR_EMPTY_CLASS)
    putText(note, picture.notice, 'sentence', 'orchestrator.map')
  }
  if (picture.model === null) return root
  const model = picture.model
  root.append(healthLine(factory, model))
  if (!model.health.sweep) root.append(sweepOffNote(factory, model))
  const warning = overlapWarning(factory, model)
  if (warning !== null) root.append(warning)
  if (model.nodes.length === 0) {
    literal(cell(root, factory, 'p', COORDINATOR_EMPTY_CLASS), COORDINATOR_EMPTY_WORD)
  } else {
    const drawing = picture.positions === null ? null : coordinatorDrawing(model, picture.positions)
    if (drawing === null) {
      literal(cell(root, factory, 'p', COORDINATOR_NO_CANVAS_CLASS), COORDINATOR_NO_CANVAS)
      root.append(fallbackList(factory, model))
    } else {
      root.append(canvas(factory, model, drawing, picture.selected))
    }
  }
  const panel = tasksPanel(factory, picture)
  if (panel !== null) root.append(panel)
  return root
}

// ---------------------------------------------------------------------------
// The screen.
// ---------------------------------------------------------------------------

/** CoordinatorDeps is what the screen is built from: where elements come from,
 * the layout client it owns, and the theme it draws in. */
export interface CoordinatorDeps {
  readonly factory: CoordinatorFactory
  readonly client: LayoutClient
  readonly theme: CoordinatorTheme
}

/**
 * CoordinatorScreen is the tab and the state a person chose.
 *
 * **It owns the layout, and with it the decision to lay anything out at all.**
 * `load` folds one map document in, builds the model, asks the client for
 * positions and answers a drawing; `change` does not ask at all, because a
 * selection is this screen's own and is not in the structure the engine is given
 * (`coordinatorLayout`). A superseded ask draws nothing: two reads can be
 * outstanding at once, and the first can answer last, so each ask takes a ticket
 * and an ask whose ticket is no longer the latest answers `null` rather than
 * putting an older picture on the panel.
 *
 * **A map the worker did not place is still a drawing**: `positions` stays
 * `null` and the picture's fallback list is what is drawn, with the console's
 * sentence above it.
 */
export class CoordinatorScreen {
  private model: CoordinatorModel | null = null
  private positions: Positions | null = null
  private selected: string | null = null
  private tasks: CoordinatorTasks = { kind: 'closed' }
  private notice: string | null = null
  private asked = 0
  private disposed = false

  constructor(private readonly deps: CoordinatorDeps) {}

  /** current is the selection the last drawing was made with. */
  get current(): { readonly selected: string | null } {
    return { selected: this.selected }
  }

  /** frame draws the section before any read: the header and the busy slot a
   * mark goes into. */
  frame(): ScreenElement {
    return this.draw()
  }

  /** load takes one map document and answers the next drawing, or `null` when a
   * later ask has already superseded this one. */
  async load(view: OrchestratorMapView): Promise<ScreenElement | null> {
    const mine = ++this.asked
    const model = buildCoordinatorModel(view)
    this.model = model
    this.notice = null
    // A selection the new document does not hold is dropped: the panel would
    // otherwise draw an orchestrator the map no longer carries.
    if (this.selected !== null && !model.nodes.some((node) => node.id === this.selected)) {
      this.selected = null
      this.tasks = { kind: 'closed' }
    }
    const positions = await this.place(model)
    if (this.disposed || mine !== this.asked) return null
    this.positions = positions
    return this.draw()
  }

  /** change is a selection's own door: a node's press opens its panel and the
   * panel's control closes it. It asks the worker for nothing. */
  change(selected: string | null): ScreenElement | null {
    if (this.disposed) return null
    this.selected = selected
    this.tasks = selected === null ? { kind: 'closed' } : { kind: 'reading' }
    return this.draw()
  }

  /** tasksReading puts the drill-down's own mark up: the press is answered in
   * the frame it happened in, over the canvas already drawn. */
  tasksReading(): ScreenElement | null {
    if (this.disposed) return null
    this.tasks = { kind: 'reading' }
    return this.draw()
  }

  /** tasksDrawn takes the orchestrator's own document. */
  tasksDrawn(tasks: readonly CoordinatorTask[]): ScreenElement | null {
    if (this.disposed) return null
    this.tasks = { kind: 'drawn', tasks }
    return this.draw()
  }

  /** tasksRefused takes the CLI's own sentence for a refused drill-down. */
  tasksRefused(sentence: string): ScreenElement | null {
    if (this.disposed) return null
    this.tasks = { kind: 'refused', sentence }
    return this.draw()
  }

  /** noticed is the sentence a refused map read is answered with, drawn under
   * the header and over the drawing the console already has. */
  noticed(sentence: string): ScreenElement | null {
    if (this.disposed) return null
    this.notice = sentence
    return this.draw()
  }

  /** forget drops the drawing for a console that has moved to another
   * installation: the next visit reads the map rather than serving one. */
  forget(): void {
    this.model = null
    this.positions = null
    this.selected = null
    this.tasks = { kind: 'closed' }
    this.notice = null
  }

  /** dispose drops the worker and the drawing with it. */
  dispose(): void {
    this.disposed = true
    this.deps.client.dispose()
  }

  private draw(): ScreenElement {
    return renderCoordinator(
      this.deps.factory,
      {
        model: this.model,
        positions: this.positions,
        selected: this.selected,
        tasks: this.tasks,
        notice: this.notice,
      },
      this.deps.theme,
    )
  }

  /** place asks the client for positions, and answers `null` -- the fallback
   * list's own state -- for a layout that failed or that this screen did not ask
   * about. A worker failure is never a blank diagram. */
  private async place(model: CoordinatorModel): Promise<Positions | null> {
    try {
      return await this.deps.client.layout(coordinatorLayout(model))
    } catch {
      return null
    }
  }
}

/** coordinatorScreen is the screen the entry mounts: the real worker over the
 * real client, in the theme the console draws. */
export function coordinatorScreen(
  factory: CoordinatorFactory,
  theme: CoordinatorTheme,
): CoordinatorScreen {
  return new CoordinatorScreen({ factory, client: coordinatorLayoutClient(), theme })
}
