// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The dependency graph screen: GV2.3a's model on a canvas GV2.2's worker placed
// (console GV2.3b; the owner's dependency graph spec §3 and its addendum §3,
// docs/console-dependency-graph/).
//
// **This screen reads, and it draws.** It reads `plan.status` through GV2.1's
// operation and the live feed through the read the Live screen already makes,
// and it writes nothing anywhere: no new IPC operation, no spawn row, no flag
// and no preload method. The one thing it does that no screen before it did is
// lay a picture out in a worker (GV2.2's client, GV2.3a's cache), and the whole
// of this file's structure follows from that: the model and the positions are
// inputs, the state a person chose is this file's, and the drawing is a function
// of the three.
//
// **Why SVG, and why no coordinate is ever a style.** A box's place is a *number
// the engine answered*, and neither of the two ways a console could write one is
// open: an inline style is refused by the policy (`style-src 'self'` backs no
// inline style, and `screen-literals.test.ts` refuses the `style` property
// anywhere under `src/renderer`), and a `data-*` attribute cannot be read as a
// length by CSS. So the picture is drawn as **SVG geometry** -- a coordinate is
// an SVG *attribute* here (`transform`, `x`, `y`, `width`, `height`, `d`,
// `viewBox`), which is a value on an element and not a style. The factory
// therefore has to make elements in the SVG namespace: `GraphFactory` is the one
// interface this screen widens, and `entry.ts`'s `domSurface` is the one place
// that implements it.
//
// **Every string a document wrote goes through the model's sanitized value.**
// GV2.3a hands this screen `Sanitized` values and never a raw document string, so
// the screen's own writer (`drawInto`) has two outcomes and no third: the text
// the sanitizer drew, or the console's `n/a` with the reason in the `title` --
// the same two doors `putText` has, for a value that has already been through it
// once. A card cuts a title again for its own width
// (`GRAPH_NODE_TITLE_CODE_POINTS`), and the cut is marked; the whole title is
// still drawn in the list view.
//
// **No state is a colour.** Design §3.5, and the §3.9 amendment's own second
// rule: every state this screen paints is painted *beside its word* -- the word
// is an element of its own (`graph-node-word`, carrying `data-word`), and the
// stylesheet may colour it. That is also why the graph is readable in forced
// colours, where every fill and stroke is replaced by the system's: what still
// distinguishes a blocked node from a ready one is `stroke-dasharray`, and what
// still distinguishes a critical edge is `stroke-width`, neither of which forced
// colours touches.
//
// **The motion is the stylesheet's, and this file draws what it is drawn on.** GV2.4's
// comet, the pulse and the flow are three keyframes in `theme/screens.css`, all of them
// under `section.graph svg`; what this file adds for them is the hook the pulse selects
// (`data-critical`) and the two `aria-hidden` marks they move. No keyframe is in this file,
// and every state here is still the drawing the stylesheet's reduced-motion arm leaves.
//
// **No pull request link, and that is the design's ruling rather than an
// omission.** A node draws its pull request **number as text** and offers no
// destination at all: design §5.1 rule 2 and gap 7 -- "no document names a
// provider" -- are why the console has no URL to build, and the plan's own node
// carries a number and no URL. The number's own tooltip says so, in the console's
// words, so a reader who clicks it is told why nothing happened rather than left
// guessing. `GRAPH_PR_NO_LINK_REASON` is the same sentence `live.ts` and `run.ts`
// draw, and `graph.test.ts` holds the three equal.

import { UNAVAILABLE_MARK, formatReading } from '../../shared/availability'
import { BUSY_SLOT_CLASS } from '../section'
import { sanitizeText } from '../../shared/sanitize'
import type { Sanitized } from '../../shared/sanitize'
import type { PlanNodeView, PlanStatusView, RunView } from '../../shared/schema'
import type { ElementLike } from '../app'
import { LayoutClient } from '../graph/layout-client'
import type { LayoutGraph, LayoutRequest, Positions, RouteSection, WorkerLike } from '../graph/layout-client'
import { WORKER_URL } from '../graph/layout-client'
import { GraphLayout, edgeKey, nodeKey } from '../graph/layout'
import { buildGraphModel } from '../graph/model'
import type { GraphModel, GraphNode, GraphPlan, GraphRelease, GraphTab, StateWord } from '../graph/model'

// ---------------------------------------------------------------------------
// The factory
// ---------------------------------------------------------------------------

/**
 * ScreenElement is the narrow element a screen draws into: a text slot and
 * attributes (design §5.1 rules 1, 2 and 7). It is declared here as well as in
 * `live.ts`, `spend.ts`, `projects.ts` and `run.ts` because the five screens are
 * siblings and none owns another.
 */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

/** ScreenFactory is where elements come from, in the HTML namespace. */
export interface ScreenFactory {
  create(tag: string): ScreenElement
}

/**
 * GraphFactory is a `ScreenFactory` that can also make an element in the **SVG**
 * namespace.
 *
 * It is a second method rather than a flag on the first because the two
 * namespaces are two different calls in the DOM (`createElement` and
 * `createElementNS`), and because widening `create` would make every screen's
 * factory answer a question four screens never ask. `domSurface` implements
 * both; a test's factory implements both; nothing else does.
 */
export interface GraphFactory extends ScreenFactory {
  createNS(tag: string): ScreenElement
}

/** SVG_NAMESPACE_URI is the namespace the picture's geometry is made in. It is
 * not a URL the console fetches: the string is the namespace's name, and a
 * browser resolves it locally. `entry.ts`'s `createNS` names this constant
 * rather than a second copy of the literal. */
export const SVG_NAMESPACE_URI = 'http://www.w3.org/2000/svg'

// ---------------------------------------------------------------------------
// The screen's own words, and its constants
// ---------------------------------------------------------------------------

/** GRAPH_THEMES is the set of themes this screen is drawn for, the same two the
 * other screens name: the tokens carry both sets and `data-theme` is how one
 * reaches the document. */
export const GRAPH_THEMES = Object.freeze(['light', 'dark'] as const)

/** GraphTheme is one of them. */
export type GraphTheme = (typeof GRAPH_THEMES)[number]

/** GRAPH_TITLE is the screen's heading. It is the console's own phrase and not
 * the checkout's: the document carries no repository name this screen draws. */
export const GRAPH_TITLE = 'dependency graph'

/** GRAPH_VIEWS are the two drawings of one model a person may choose between:
 * the canvas, and the list (the reconciliation's F25 -- an SVG picture needs a
 * non-visual equivalent). */
export const GRAPH_VIEWS = Object.freeze(['graph', 'list'] as const)

/** GraphViewName is one of them. */
export type GraphViewName = (typeof GRAPH_VIEWS)[number]

/**
 * GRAPH_VIEW_LABELS is what the view switch says, keyed by the view that is
 * **on screen**: the button is a switch rather than a tab strip, so its label
 * names the drawing a click would put up, and `GRAPH_VIEW_NAMES` below is what
 * the button's own `title` says the drawing it names is. One button and not a
 * second tablist: design §4.3 gives the keyboard behaviour to the navigation and
 * the rail, and a third tablist would need it too for one pair of words.
 */
export const GRAPH_VIEW_LABELS: Readonly<Record<GraphViewName, string>> = Object.freeze({
  graph: 'List view',
  list: 'Graph view',
})

/** GRAPH_VIEW_NAMES is the two drawings' own names, for the button's `title`:
 * "List view" is what a click does, and this is what it is. */
export const GRAPH_VIEW_NAMES: Readonly<Record<GraphViewName, string>> = Object.freeze({
  graph: 'switch to the list view of this graph',
  list: 'switch to the graph view of this graph',
})

/** GRAPH_REFRESH_LABEL is the operator's own refresh, which is the second of the
 * three moments the read is made at. */
export const GRAPH_REFRESH_LABEL = 'Refresh'

/** GRAPH_FIT_LABEL is the fit-to-screen button (the spec's navigation
 * paragraph): it puts the whole picture back in the frame. */
export const GRAPH_FIT_LABEL = 'Fit to screen'

/** GRAPH_CHIP_MARK is the mark a finished release's chip carries beside its
 * name, which is what makes the chip a *finished* one in a picture read without
 * colour. */
export const GRAPH_CHIP_MARK = '✓'

/** GRAPH_NOT_SHOWN_SINGULAR and GRAPH_NOT_SHOWN_PLURAL are the note's two
 * forms. The note is drawn only when the model dropped something, and it names
 * the count rather than the things: a bound that dropped a task is a fact about
 * the picture, and the tasks are exactly what is not on it. */
export const GRAPH_NOT_SHOWN_SINGULAR = 'more task is not drawn'
export const GRAPH_NOT_SHOWN_PLURAL = 'more tasks are not drawn'

/** GRAPH_NO_PLANS is what a picture with nothing in it says. An installation
 * whose checkout holds no plan artifact is not an error and not a broken read:
 * it is a graph with nothing in it, and the console says which of the two it
 * is. */
export const GRAPH_NO_PLANS = 'this checkout has no plan the console can draw'

/** GRAPH_NO_CANVAS is what the list says above itself when the canvas could not
 * be drawn: the layout worker refused the graph, or answered something this
 * client would not draw. The model is still a graph and the list is still true,
 * so the drawing falls back to the list and says why rather than showing an
 * empty frame. */
export const GRAPH_NO_CANVAS = 'the layout worker did not place this graph, so it is drawn as a list'

/** GRAPH_NO_RUN is a node the plan gave no run: the join `plan status` makes
 * found none, so there is nothing to open. */
export const GRAPH_NO_RUN = 'this task has no run in this read of the plan'

/** GRAPH_NO_RUN_LISTED is a suffix no run in the console's runs list ends with:
 * the run the plan knew about is not in the list this console read. */
export const GRAPH_NO_RUN_LISTED = 'no run in this console’s run list ends with this task’s run suffix'

/** GRAPH_RUN_AMBIGUOUS is a suffix **two** runs end with. Four characters are a
 * run's own tail and not a unique key, so the console refuses to guess: a node
 * that opened the wrong run would be worse than a node that opens nothing. */
export const GRAPH_RUN_AMBIGUOUS = 'two runs in this console’s run list end with this task’s run suffix'

/** GRAPH_RUN_OPENS is the tooltip of a node that does open a run. It is the
 * console's own sentence about what the gesture does, and it names no run id. */
export const GRAPH_RUN_OPENS = 'opens this task’s run'

/** GRAPH_PR_NO_LINK_REASON is the pull request number's own tooltip: the number
 * is drawn and no destination is offered, because no document this console reads
 * names the provider a URL would have to be built from (design §5.1 rule 2, gap
 * 7). It is the same sentence `live.ts` and `run.ts` draw, and `graph.test.ts`
 * holds the three equal. */
export const GRAPH_PR_NO_LINK_REASON =
  'this CLI does not say which provider this run’s repository is on, so the console will not build a link from it'

/** GRAPH_CANVAS_LABEL is the canvas's accessible name: the picture is not the
 * only drawing of this model, and the name says which one this is. */
export const GRAPH_CANVAS_LABEL = 'the dependency graph, drawn as boxes and edges'

/** GRAPH_LIST_LABEL is the list's own name, for the same reason. */
export const GRAPH_LIST_LABEL = 'the dependency graph, drawn as a list'

/**
 * GRAPH_NODE_TITLE_LINE_CODE_POINTS is how much of a node's title one **line of a
 * card** holds, and GRAPH_NODE_TITLE_LINES is how many lines a card draws.
 *
 * GV2.3a already cut the sentence at 200 code points for the model; a card is
 * `GRAPH_NODE_WIDTH` wide and its title is drawn as lines rather than as one run,
 * because SVG text neither wraps nor clips and a longer title would draw outside
 * its box. The lines are filled by whole words (a word too long for one line is
 * broken at the bound, which is the only place a line is cut mid-word), and the
 * card cuts -- with the cut marked -- **only when the title does not fit the
 * lines it has**. The whole title is drawn in the list view: a card is a card,
 * and the list is where a reader goes for the whole sentence.
 */
export const GRAPH_NODE_TITLE_LINE_CODE_POINTS = 30
export const GRAPH_NODE_TITLE_LINES = 3

/** GRAPH_ZOOM_STEP is what one scroll step multiplies the zoom by, and
 * GRAPH_PINCH_STEP the finer one a trackpad's pinch arrives as -- a pinch is a
 * continuous gesture a browser reports as many small steps, so the step it is
 * read with is small. GRAPH_ZOOM_MIN/GRAPH_ZOOM_MAX bound the result: a graph
 * zoomed past either end is a graph a person cannot find their way back from
 * with one gesture. */
export const GRAPH_ZOOM_STEP = 1.25
export const GRAPH_PINCH_STEP = 1.05
export const GRAPH_ZOOM_MIN = 0.5
export const GRAPH_ZOOM_MAX = 8

/**
 * GRAPH_REFRESH_MS is how often the plan is re-read while the graph is on screen:
 * 60,000 ms, which is **the cadence GV2.1 gave this read** -- `cli.ts`'s
 * `PLAN_STATUS_MIN_INTERVAL_MS` is how long `plan.status` serves the document it
 * already read, and re-reading it more often than that would ask for a document
 * the source is going to hand back anyway. A second copy of the number rather
 * than an import, for the reason `live.ts`'s `LIVE_STALE_AFTER_SECONDS` is one:
 * the renderer does not reach into the main process's source module (which holds
 * the child-process machinery), and `graph.test.ts` holds the two equal so the
 * copies cannot drift.
 *
 * The **feed** is a different read and keeps its own cadence (`LIVE_POLL_MS` in
 * the entry): a stage is the thing that moves, and the plan is the thing that
 * does not.
 */
export const GRAPH_REFRESH_MS = 60_000

/** GRAPH_PAD is the room between a release box and the tasks in it, and between
 * the picture's own edge and the outermost box. */
export const GRAPH_PAD = 18

/** GRAPH_RELEASE_HEAD is the room a release box keeps above its tasks for the
 * two lines of its own heading: the name line, and the "n of m done" bar. The
 * layout engine places the tasks and nothing else, so the box is grown upward
 * for its head rather than drawn over the first row of cards. */
export const GRAPH_RELEASE_HEAD = 30

/** GRAPH_BAND_LABEL_LINE is the room a stream band's own label takes above the
 * release box under it, and GRAPH_BAND_HEAD is the whole head a band draws above
 * its topmost card: the line, and the room the label needs over the release
 * heading (GRAPH_RELEASE_HEAD), because both are drawn at the left of the same
 * region and the label is the outer of the two. */
export const GRAPH_BAND_LABEL_LINE = 22
export const GRAPH_BAND_HEAD = GRAPH_RELEASE_HEAD + GRAPH_BAND_LABEL_LINE

/** GRAPH_LAYER_GAP is the room this screen asks the layout engine to leave
 * between two cards: the head room a band and a release box draw above their
 * tasks (GRAPH_BAND_HEAD) and the padding they keep around them (GRAPH_PAD
 * twice). The engine's own room is 20 px, which is less than one heading needs:
 * a box grown upward for its heading -- and the band above it grown for its
 * label -- then reaches over the card drawn above it, and a card is drawn after
 * every box and paints over it. */
export const GRAPH_LAYER_GAP = GRAPH_PAD * 2 + GRAPH_BAND_HEAD

/** GRAPH_LAYER_OPTION, GRAPH_NODE_OPTION and GRAPH_COMPONENT_OPTION are the three
 * ELK settings GRAPH_LAYER_GAP is passed as: the room between two layers, the
 * room between two cards **of one layer**, and the room between two components.
 *
 * **The middle one is this direction's own.** With the picture's depth running
 * left to right, two tasks of one layer sit one above the other and the room
 * between them is the vertical one an engine calls `elk.spacing.nodeNode` --
 * which is where a release box's heading and a stream band's label are drawn.
 * They are named here rather than written inline because they are strings ELK
 * parses, so the layout test reads the same spellings the screen sends. */
export const GRAPH_LAYER_OPTION = 'elk.layered.spacing.nodeNodeBetweenLayers'
export const GRAPH_NODE_OPTION = 'elk.spacing.nodeNode'

/** GRAPH_COMPONENT_OPTION is the ELK setting GRAPH_LAYER_GAP is passed as for
 * the room between two components -- tasks no edge joins. It carries the same
 * room as GRAPH_LAYER_OPTION because a heading does not know which of the two it
 * is under: the engine's own 20 px is less than one heading needs, and a task
 * that waits on nothing is a component of its own, so an isolated task's card
 * would cover the next release's heading exactly as a row above it would. */
export const GRAPH_COMPONENT_OPTION = 'elk.spacing.componentComponent'

/** GRAPH_WORD_SLOT, GRAPH_CRITICAL_SLOT, GRAPH_PR_SLOT, GRAPH_RUN_SLOT and
 * GRAPH_MARK_SLOT are the x offsets of the cells along a card of
 * `GRAPH_NODE_WIDTH`. They are fixed slots rather than measured ones: SVG text
 * has no width a screen can read, and a slot is a place a reader's eye learns. */
const GRAPH_WORD_SLOT = 12
const GRAPH_CRITICAL_SLOT = 104
const GRAPH_PR_SLOT = 12
const GRAPH_RUN_SLOT = 46
const GRAPH_MARK_SLOT = 112

/** GRAPH_TITLE_LINE_Y and GRAPH_TITLE_LINE_HEIGHT are where a card's title
 * starts and how far apart its lines sit, and GRAPH_WORD_LINE_Y and
 * GRAPH_META_LINE_Y are the two lines under them: the state's own word, then the
 * pull request, the run and the reading's mark. They are the card's own
 * geometry -- `GRAPH_NODE_HEIGHT` is what they add up to -- and they are the
 * baseline of each line, which is what SVG writes. */
const GRAPH_TITLE_LINE_Y = 18
const GRAPH_TITLE_LINE_HEIGHT = 15
const GRAPH_WORD_LINE_Y = 64
const GRAPH_META_LINE_Y = 78

/** GRAPH_RUNNING_WORDS are the four words a task being worked on draws first, and
 * they are the four the comet's own rule selects. Three are the stages a person
 * watches; the fourth is `queued`, which `primaryWord` produces for a run whose
 * stage this screen has no word for -- `plan`, `bootstrap`, `check`, `finalize`,
 * `render`, `writeback` or none at all. `queued` is only ever a *running* task
 * ("live, and not in a stage a person watches"), so leaving it out would leave a
 * task that is genuinely running with no motion at all (design section 3.9's
 * amendment, and the spec's "extend for other known states"). */
const GRAPH_RUNNING_WORDS: readonly StateWord[] = Object.freeze(['implementing', 'in review', 'fixing', 'queued'])

/** GRAPH_NODE_RX is the card's own corner radius, written on the box and again on
 * the streak that travels it: the two have to be the same arc, or the streak
 * leaves the border at every corner. */
const GRAPH_NODE_RX = 6

/** GRAPH_NODE_RADIUS is that radius as the attribute it is written as. */
const GRAPH_NODE_RADIUS = String(GRAPH_NODE_RX)

/** GRAPH_GLOW_OFFSET is how far the glow behind a critical card reaches past its
 * own boundary: enough that the card's fill hides the middle of the glow and only
 * the spill around it shows. */
const GRAPH_GLOW_OFFSET = 6

/** GRAPH_GLOW_RADIUS is the card's radius plus that offset. Two arcs with one
 * centre keep the glow the same distance out at the corners as along the edges; a
 * glow drawn with the card's own radius would pinch shut where they meet. */
const GRAPH_GLOW_RADIUS = String(GRAPH_NODE_RX + GRAPH_GLOW_OFFSET)

// ---------------------------------------------------------------------------
// The state a person chose
// ---------------------------------------------------------------------------

/**
 * GraphState is everything about the screen that is not the document: which tab
 * is chosen, which of the two drawings is up, which node is selected, which
 * finished-release chips are open, and how far the canvas is zoomed.
 *
 * It is the screen's own and it is **not** part of the layout's key: a selection,
 * an open chip or a zoom must not re-lay anything out, and GV2.3a's
 * `structureHash` cannot see any of these -- which is what makes "a watch frame
 * that changes nothing structural does not re-lay-out" true of the state as well
 * as of the document.
 *
 * `selected` is a node's **layout key** (`nodeKey`) and `expanded` holds release
 * keys: both are qualified by the plan, because two plans of one checkout may
 * hold the same task id or the same version.
 */
export interface GraphState {
  readonly tab: GraphTab
  readonly view: GraphViewName
  readonly selected: string | null
  readonly expanded: readonly string[]
  readonly zoom: number
}

/** GRAPH_INITIAL_STATE is the screen as it opens: every stream at once (the
 * canvas), nothing selected, no chip open, and the whole picture in the frame. */
export const GRAPH_INITIAL_STATE: GraphState = Object.freeze({
  tab: null,
  view: 'graph',
  selected: null,
  expanded: Object.freeze([]),
  zoom: 1,
})

/** GraphPicture is one drawing's inputs: the model, the positions the engine
 * answered with (or `null` while it has not), the state, and the console's run
 * list -- which is what a node's door is resolved against. */
export interface GraphPicture {
  readonly model: GraphModel
  readonly positions: Positions | null
  readonly state: GraphState
  readonly runs: readonly string[]
}

// ---------------------------------------------------------------------------
// The two joins: the live stage, and a node's run
// ---------------------------------------------------------------------------

/** releaseKey is a release as this screen's own identity for it: the plan's id
 * and the version, qualified by the plan's length so no id can forge a boundary
 * from the inside -- the same rule `layout.ts`'s keys keep. */
export function releaseKey(plan: string, version: string): string {
  return `${String(plan.length)}:${plan}:${version}`
}

/**
 * runIdsIn is the console's current run list: every run id a `runs ls --json`
 * document names.
 *
 * **`rigorIndex` is not this, and that is why the reader is here.** That index is
 * keyed by run id too, but it drops every row whose rigor block it cannot show --
 * a run from before the field, or one that died before bootstrap -- and a run
 * missing from the list would make this screen answer "no run ends with that
 * suffix" about a run the CLI did print. The pair of keys read here is the same
 * pair (`run`, or the older `run_id`), in the same order.
 */
export function runIdsIn(document: unknown): readonly string[] {
  const record = objectAt(document)
  const rows = record === null ? undefined : record['runs']
  if (!Array.isArray(rows)) return Object.freeze([])
  const ids: string[] = []
  for (const row of rows) {
    const entry = objectAt(row)
    if (entry === null) continue
    const id = textAt(entry['run']) ?? textAt(entry['run_id'])
    if (id === null || id === '') continue
    if (!ids.includes(id)) ids.push(id)
  }
  return Object.freeze(ids)
}

/** runForSuffix is which run a node's four-character suffix names: the one run
 * in `runs` that ends with it, or `null` when none does and when more than one
 * does. */
export function runForSuffix(suffix: string, runs: readonly string[]): string | null {
  const matches = matchesFor(suffix, runs)
  return matches.length === 1 ? (matches[0] as string) : null
}

function matchesFor(suffix: string, runs: readonly string[]): readonly string[] {
  if (suffix === '') return []
  // A run whose whole id is the suffix is not a run the suffix names: the
  // suffix is a run id's *tail*, and `plan status` writes four characters of a
  // much longer id.
  return runs.filter((run) => run.length > suffix.length && run.endsWith(suffix))
}

/** RunDoor is what a node's gesture is worth, and what it says when it is worth
 * nothing: the run id it opens, or the console's sentence about why it opens
 * none. */
export interface RunDoor {
  readonly run: string | null
  readonly because: string
}

/** doorFor is the suffix join with the sentence a reader is owed. A node with no
 * run at all is `GRAPH_NO_RUN`; a suffix no run ends with is
 * `GRAPH_NO_RUN_LISTED`; a suffix two runs end with is `GRAPH_RUN_AMBIGUOUS` and
 * is never resolved to one of them. */
export function doorFor(node: GraphNode, runs: readonly string[]): RunDoor {
  const suffix = node.run.ok ? node.run.text : ''
  if (suffix === '') return { run: null, because: GRAPH_NO_RUN }
  const matches = matchesFor(suffix, runs)
  if (matches.length === 0) return { run: null, because: GRAPH_NO_RUN_LISTED }
  if (matches.length > 1) return { run: null, because: GRAPH_RUN_AMBIGUOUS }
  return { run: matches[0] as string, because: GRAPH_RUN_OPENS }
}

/**
 * withFeedStages is the plan document with the **live stages** the feed knows
 * folded in: a node whose run this frame is running, joined by the run suffix,
 * takes the frame's stage.
 *
 * Three rules, and each one is a way of *not* guessing:
 *
 *   - **Only a `running` node takes a stage.** The words read a stage only for a
 *     running task (`stateWords`), so a stage written onto a finished one would
 *     change nothing on the screen and everything in the model;
 *   - **Only an unambiguous suffix**, exactly as a node's door is resolved;
 *   - **Only a non-empty stage.** A frame that has not reached a stage yet
 *     carries none, and an empty stage is not `queued` -- it is "no reading", and
 *     the plan's own stage stands.
 *
 * The document's `state` is never taken from the feed. A `RunView` is a watch row
 * and not `plan.StateNode.State`: this console would have to invent the mapping
 * from `queued`/`halted` flags to one of the CLI's six states, and the whole
 * point of `schema.ts`'s `PlanState` being closed is that a word this build does
 * not know is a refusal rather than a guess.
 */
export function withFeedStages(document: PlanStatusView, feed: readonly RunView[]): PlanStatusView {
  const stages = feedStages(feed)
  if (stages.size === 0) return document
  let changed = false
  const plans = document.plans.map((plan) => {
    let planChanged = false
    const nodes = plan.nodes.map((node) => {
      const stage = stageFor(node, stages)
      if (stage === null) return node
      planChanged = true
      return { ...node, stage }
    })
    if (!planChanged) return plan
    changed = true
    return { ...plan, nodes }
  })
  return changed ? { ...document, plans } : document
}

/** feedStages is the frame's own suffix index: the stage one suffix names, with
 * every suffix two rows give different answers about removed -- so a lookup can
 * only answer a stage the frame is unambiguous about. */
function feedStages(feed: readonly RunView[]): ReadonlyMap<string, string> {
  const seen = new Map<string, string | null>()
  for (const row of feed) {
    if (row.run.length <= 4) continue
    const suffix = row.run.slice(-4)
    const stage = row.stage === '' ? null : row.stage
    const known = seen.get(suffix)
    if (known === undefined) seen.set(suffix, stage)
    else if (known !== stage) seen.set(suffix, null)
  }
  const stages = new Map<string, string>()
  for (const [suffix, stage] of seen) if (stage !== null) stages.set(suffix, stage)
  return stages
}

/** drawnStages is what a frame says about **the graph this screen draws**: the
 * `run=stage` pairs of the tasks that are running and that this frame has a stage
 * for, sorted so two frames that say the same thing about the same tasks have the
 * same key however the rows were ordered.
 *
 * **The feed's other rows are deliberately not in the key.** A `live.watch` frame
 * is installation-wide -- it carries every run the console can see, another
 * checkout's included -- and a key that took all of it would rebuild the graph
 * (re-running `buildGraphModel` over every node, and `renderGraph` over the whole
 * DOM) for a picture that did not change, on a poll that runs every second. The
 * key is therefore a function of the *fold* and not of the frame: an unchanged
 * key means `withFeedStages` would return the same document, so the model, the
 * positions and the drawing are the ones already on screen.
 *
 * A stage a plan the current tab hides would take is still in the key. The tab
 * drops whole plans and the caps drop tasks, so this is a superset of what the
 * drawing folds in: the cost of a hidden task's stage moving is one redraw, where
 * leaving it out could leave a drawn word stale. */
function drawnStages(document: PlanStatusView, feed: readonly RunView[]): string {
  const stages = feedStages(feed)
  if (stages.size === 0) return ''
  const pairs: string[] = []
  for (const plan of document.plans) {
    for (const node of plan.nodes) {
      const stage = stageFor(node, stages)
      if (stage !== null) pairs.push(`${node.run}=${stage}`)
    }
  }
  return pairs.sort().join(',')
}

function stageFor(node: PlanNodeView, stages: ReadonlyMap<string, string>): string | null {
  if (node.state !== 'running' || node.run === '') return null
  const stage = stages.get(node.run)
  return stage === undefined ? null : stage
}

function objectAt(raw: unknown): Record<string, unknown> | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

function textAt(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

// ---------------------------------------------------------------------------
// The drawing's own helpers
// ---------------------------------------------------------------------------

/** elementIn stamps the classes one element carries. It is the one place a class
 * list is joined, so the scan's vocabulary has one entry for the join rather
 * than one per helper. */
function elementIn(element: ScreenElement, classes: readonly string[]): ScreenElement {
  element.setAttribute('class', classes.join(' '))
  return element
}

/** make is a fresh element of the console's own making, in the HTML namespace. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  return elementIn(factory.create(tag), classes)
}

/** makeNS is `make` in the SVG namespace. */
function makeNS(factory: GraphFactory, tag: string, ...classes: string[]): ScreenElement {
  return elementIn(factory.createNS(tag), classes)
}

/** literal writes the console's own vocabulary -- never a document's string,
 * which comes through `drawInto` below. It is the file's only text-slot writer. */
function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

/** cell is a text cell of the console's own making, appended to a parent. */
function cell(parent: ScreenElement, factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = make(factory, tag, ...classes)
  parent.append(element)
  return element
}

/** cellNS is `cell` in the SVG namespace. */
function cellNS(parent: ScreenElement, factory: GraphFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = makeNS(factory, tag, ...classes)
  parent.append(element)
  return element
}

/**
 * drawInto writes one `Sanitized` value into an element this factory made: the
 * text the sanitizer drew, or the console's `n/a` with the reason in the
 * `title`.
 *
 * This is `putText`'s own pair of outcomes, and it is here rather than in
 * `putText` because the value has **already** been through `sanitizeText` -- the
 * model did it, with each field's own class and wire path -- and `sanitizeText`
 * refuses anything that is not a string, so handing it a `Sanitized` value would
 * draw `n/a` with "the value is a object, not text" beside every field on this
 * screen. The reasoning is the one `version.ts`'s `versionText` keeps: a drawer
 * may write a sanitizer's own answer, and it may write nothing else.
 */
function drawInto(element: ScreenElement, value: Sanitized): ScreenElement {
  if (value.ok) return literal(element, value.text)
  literal(element, UNAVAILABLE_MARK)
  element.setAttribute('title', value.reason)
  return element
}

/** drawn is `drawInto` on a fresh cell. */
function drawn(parent: ScreenElement, factory: ScreenFactory, tag: string, value: Sanitized, ...classes: string[]): ScreenElement {
  return drawInto(cell(parent, factory, tag, ...classes), value)
}

/** drawnNS is `drawn` in the SVG namespace. */
function drawnNS(parent: ScreenElement, factory: GraphFactory, tag: string, value: Sanitized, ...classes: string[]): ScreenElement {
  return drawInto(cellNS(parent, factory, tag, ...classes), value)
}

/** markWord is a state word as a cell of its own, carrying the word it draws in
 * `data-word` -- the hook a rule may colour, and the text a reader who sees no
 * colour reads. */
function markWord(element: ScreenElement, word: string): ScreenElement {
  element.setAttribute('data-word', word)
  return literal(element, word)
}

/** words is a node's words as one cell, each word its own element. */
function words(parent: ScreenElement, factory: ScreenFactory, node: GraphNode): ScreenElement {
  const element = cell(parent, factory, 'span', 'graph-words')
  for (const word of node.words) markWord(cell(element, factory, 'span', 'graph-word'), word)
  return element
}

/** wordsNS is `words` in the SVG namespace: each word its own `text` element, at
 * the slot its position in the list gives it. */
function wordsNS(parent: ScreenElement, factory: GraphFactory, node: GraphNode): void {
  for (const [index, word] of node.words.entries()) {
    const element = cellNS(parent, factory, 'text', 'graph-node-word')
    geometry(element, 'x', index === 0 ? GRAPH_WORD_SLOT : GRAPH_CRITICAL_SLOT)
    geometry(element, 'y', GRAPH_WORD_LINE_Y)
    markWord(element, word)
  }
}

/** tooltip is the sentence an element's gesture is owed, as SVG's own tooltip
 * (`<title>`, the element a browser shows on hover) -- which is why the reason is
 * an element here and a `title` attribute where an HTML cell carries one: this
 * element is inside an `<svg>`, and an SVG element's tooltip is its child. */
function tooltip(factory: GraphFactory, reason: string): ScreenElement {
  return literal(makeNS(factory, 'title'), reason)
}

/** coord writes one coordinate. Every number that reaches SVG geometry goes
 * through here, so a reply the layout client let through as a finite number is
 * the only kind of value that can be spelled into an attribute. */
function coord(value: number): string {
  return String(value)
}

/** geometry writes one SVG geometry attribute. Every coordinate this screen
 * draws reaches the document through here, so the scan's vocabulary has one
 * entry for all of them rather than one per attribute name. */
function geometry(element: ScreenElement, name: string, value: number): ScreenElement {
  element.setAttribute(name, coord(value))
  return element
}

/** shift places a group at a point: a card's own coordinates are its box's, and
 * the group carries the engine's answer as a translation. */
function shift(element: ScreenElement, x: number, y: number): ScreenElement {
  element.setAttribute('transform', `translate(${coord(x)} ${coord(y)})`)
  return element
}

/**
 * titleLines is the title as the lines a card holds: the model's cut, wrapped
 * over the card's own lines, and cut again -- with the cut marked -- only when
 * the wrap does not fit them.
 *
 * **The wrap breaks between words**, because a line broken inside a word is a
 * word a reader has to reassemble, which is the thing an ellipsis was already
 * accused of. A single word longer than a whole line is the one case a line is
 * cut mid-word, and it is broken rather than dropped.
 *
 * `[...text]` iterates code points, so a surrogate pair is never split into the
 * two lone halves a font draws as a box -- neither by the wrap nor by the cut.
 */
function titleLines(value: Sanitized): readonly string[] {
  if (!value.ok) return [UNAVAILABLE_MARK]
  const lines: string[] = []
  for (const word of wrapWords(value.text)) {
    const at = lines.length - 1
    const line = lines[at] ?? ''
    if (line !== '' && [...line].length + 1 + [...word].length <= GRAPH_NODE_TITLE_LINE_CODE_POINTS) {
      lines[at] = `${line} ${word}`
      continue
    }
    if ([...word].length <= GRAPH_NODE_TITLE_LINE_CODE_POINTS) {
      lines.push(word)
      continue
    }
    // One word longer than a line: it fills as many lines as it needs, so the
    // rest of the title is not lost behind it.
    const points = [...word]
    for (let at = 0; at < points.length; at += GRAPH_NODE_TITLE_LINE_CODE_POINTS) {
      lines.push(points.slice(at, at + GRAPH_NODE_TITLE_LINE_CODE_POINTS).join(''))
    }
  }
  if (lines.length <= GRAPH_NODE_TITLE_LINES) return lines
  const held = lines.slice(0, GRAPH_NODE_TITLE_LINES)
  const last = [...(held[GRAPH_NODE_TITLE_LINES - 1] ?? '')]
  held[GRAPH_NODE_TITLE_LINES - 1] = `${last.slice(0, GRAPH_NODE_TITLE_LINE_CODE_POINTS - 1).join('')}…`
  return held
}

/** wrapWords is a title's words, with every run of whitespace between two of them
 * read as the one space a line break takes: the wrap puts the words back with a
 * space of its own, so a tab or a run of spaces in a title is drawn as one. */
function wrapWords(text: string): readonly string[] {
  return text.split(/\s+/).filter((word) => word !== '')
}

/** barText is a release's "n of m done" as one text run. The two figures go
 * through `formatReading`, the console's only formatter, so a release whose
 * counters the document does not carry reads `n/a` and never `0 of 0`. */
function barText(release: GraphRelease): string {
  return `${formatReading(release.done, 'count')} of ${formatReading(release.total, 'count')} done`
}

/** notShownNote is the note a bound's drop is drawn as: the count, then the
 * console's own words, singular or plural. */
function notShownNote(count: number): string {
  return `${String(count)} ${count === 1 ? GRAPH_NOT_SHOWN_SINGULAR : GRAPH_NOT_SHOWN_PLURAL}`
}

interface Rect {
  readonly x: number
  readonly y: number
  readonly width: number
  readonly height: number
}

/** boxOf is the rectangle a release's tasks occupy, padded, with the head its
 * own heading needs. The layout is **flat** -- GV2.2's client is given nodes and
 * edges and no containers -- so a release is drawn as the box around the tasks in
 * it, which is the release's own answer to "where am I" and needs no second
 * layout. A release with no task on the picture has no box: there is nothing to
 * draw one around, and the chips and the list still name it. */
function boxOf(rects: readonly Rect[]): Rect | null {
  if (rects.length === 0) return null
  const left = Math.min(...rects.map((rect) => rect.x))
  const top = Math.min(...rects.map((rect) => rect.y))
  const right = Math.max(...rects.map((rect) => rect.x + rect.width))
  const bottom = Math.max(...rects.map((rect) => rect.y + rect.height))
  return {
    x: left - GRAPH_PAD,
    y: top - GRAPH_PAD - GRAPH_RELEASE_HEAD,
    width: right - left + GRAPH_PAD * 2,
    height: bottom - top + GRAPH_PAD * 2 + GRAPH_RELEASE_HEAD,
  }
}

/** place writes a rectangle's four geometry attributes. */
function place(element: ScreenElement, rect: Rect): void {
  geometry(element, 'x', rect.x)
  geometry(element, 'y', rect.y)
  geometry(element, 'width', rect.width)
  geometry(element, 'height', rect.height)
}

/** pathOf is a routed edge as one SVG path: one `M`/`L` run per section, and the
 * engine's own bend points in between. */
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

/** contentBounds is the whole picture's rectangle, or null when nothing was
 * placed -- the layout the worker did not answer, or a graph with no node.
 *
 * `extra` is what the picture draws that no node's rectangle holds: the stream
 * bands, whose heads stand above the tasks in them. A picture framed on the
 * nodes alone would crop every band's label. */
function contentBounds(model: GraphModel, placed: ReadonlyMap<string, Rect>, extra: readonly Rect[] = []): Rect | null {
  const rects: Rect[] = [...extra]
  for (const column of model.columns) {
    for (const plan of column.plans) {
      for (const node of plan.nodes) {
        const rect = placed.get(nodeKey(plan.id, node.id))
        if (rect !== undefined) rects.push(rect)
      }
    }
  }
  return boxOf(rects)
}

/** StreamBand is one stream's band: the stream it is drawn for, what the model
 * calls it, and the rectangle it covers. */
interface StreamBand {
  readonly stream: string
  readonly label: Sanitized
  readonly rect: Rect
}

/** bandOf is the rectangle a band covers: the stream's own tasks, padded, with
 * the head its label needs above them. A stream with no task on the picture has
 * no band, because there is nothing to draw one around. */
function bandOf(rects: readonly Rect[]): Rect | null {
  if (rects.length === 0) return null
  const left = Math.min(...rects.map((rect) => rect.x))
  const top = Math.min(...rects.map((rect) => rect.y))
  const right = Math.max(...rects.map((rect) => rect.x + rect.width))
  const bottom = Math.max(...rects.map((rect) => rect.y + rect.height))
  return {
    x: left - GRAPH_PAD,
    y: top - GRAPH_PAD - GRAPH_BAND_HEAD,
    width: right - left + GRAPH_PAD * 2,
    height: bottom - top + GRAPH_PAD * 2 + GRAPH_BAND_HEAD,
  }
}

/**
 * streamBands is the bands the canvas draws: one per stream of the model, in the
 * model's own order, over the tasks **the canvas draws** -- a band around the
 * tasks a shut chip hides would be a band around empty space.
 *
 * The band is the model's stream grouping made geometry: each stream is laid out
 * on a run of its own and the runs are stacked as rows (`layout.ts`), so the row
 * a stream's tasks occupy is that run's own rectangle around them, drawn behind
 * everything else -- and a band is therefore exactly a row, which is what keeps
 * one stream's band off another stream's cards.
 */
function streamBands(picture: GraphPicture, placed: ReadonlyMap<string, Rect>): readonly StreamBand[] {
  const bands: StreamBand[] = []
  for (const column of picture.model.columns) {
    const rects: Rect[] = []
    for (const plan of column.plans) {
      const shown = visibleNodes(picture, plan)
      for (const node of plan.nodes) {
        const key = nodeKey(plan.id, node.id)
        if (!shown.has(key)) continue
        const rect = placed.get(key)
        if (rect !== undefined) rects.push(rect)
      }
    }
    const rect = bandOf(rects)
    if (rect === null) continue
    bands.push({ stream: column.stream, label: column.label, rect })
  }
  return bands
}

/** band is one stream's band: the ground its tasks sit on, and the stream's own
 * name at the band's head. The name is the model's sanitized label like every
 * other document string, and the console's own words for the plans that name no
 * stream ("no stream"). */
function band(factory: GraphFactory, one: StreamBand): ScreenElement {
  const group = makeNS(factory, 'g', 'graph-band')
  group.setAttribute('data-stream', one.stream)
  const outline = makeNS(factory, 'rect', 'graph-band-box')
  place(outline, one.rect)
  group.append(outline)
  const label = drawnNS(group, factory, 'text', one.label, 'graph-band-label')
  geometry(label, 'x', one.rect.x + 8)
  geometry(label, 'y', one.rect.y + 15)
  return group
}

/**
 * viewBoxOf is what the canvas is looking at: the whole picture scaled by the
 * zoom, around the picture's own centre, so a zoom step keeps what a person is
 * looking at in the middle of the frame.
 *
 * `viewBox` is an attribute of the `<svg>` element, which is why zoom, pinch and
 * fit are three ways of writing one string and not three transforms: nothing
 * here needs a style. `clampZoom` bounds it, because a graph zoomed past either
 * end is a graph a person cannot find their way back from with one gesture.
 */
export function viewBoxOf(bounds: Rect, zoom: number): string {
  const scale = clampZoom(zoom)
  const width = bounds.width / scale
  const height = bounds.height / scale
  const x = bounds.x + (bounds.width - width) / 2
  const y = bounds.y + (bounds.height - height) / 2
  return `${coord(x)} ${coord(y)} ${coord(width)} ${coord(height)}`
}

/**
 * clampZoom is the one bound on the zoom, and it is applied **where the zoom is
 * stored** and not only where it is drawn.
 *
 * `viewBoxOf` clamps the number it is handed, which keeps the picture drawable;
 * the entry takes every wheel step from `current.zoom` (`zoom * step`), so a
 * screen that *kept* an unbounded number would need a hundred scrolls to come
 * back from one pinch, and each of them would draw the same clamped viewBox --
 * a control that looks stuck. Storing the bound is what makes the next step a
 * step from the edge.
 */
export function clampZoom(zoom: number): number {
  return Math.min(GRAPH_ZOOM_MAX, Math.max(GRAPH_ZOOM_MIN, zoom))
}

/** TAB_ALL is the All tab's key, and TAB_STREAM_PREFIX qualifies a stream's own
 * name as a stream's.
 *
 * The prefix is not decoration: a stream is called whatever a checkout likes,
 * `all` included, and a key that could name two tabs is a key the entry would be
 * reading as a guess. With the prefix the two are disjoint -- All's key never
 * begins with `stream:` and a stream's always does -- so the map is a bijection
 * and `tabOf` is its inverse. */
export const TAB_ALL = 'all'
export const TAB_STREAM_PREFIX = 'stream:'

/** tabKey is a tab as an attribute value. */
export function tabKey(tab: GraphTab): string {
  return tab === null ? TAB_ALL : `${TAB_STREAM_PREFIX}${tab}`
}

/** tabOf is `tabKey`'s inverse, and the entry is its only caller. A key that is
 * neither All nor a prefixed stream is All: the key is read off a button this
 * console drew, and a key this build does not know is not a tab it will invent. */
export function tabOf(key: string): GraphTab {
  if (key === TAB_ALL) return null
  return key.startsWith(TAB_STREAM_PREFIX) ? key.slice(TAB_STREAM_PREFIX.length) : null
}

// ---------------------------------------------------------------------------
// The drawing's parts
// ---------------------------------------------------------------------------

/** header draws the heading, the tabs, the three controls and the note. */
function header(factory: ScreenFactory, picture: GraphPicture): ScreenElement {
  const element = make(factory, 'header', 'graph-header')
  literal(cell(element, factory, 'h1', 'graph-title'), GRAPH_TITLE)
  element.append(tabs(factory, picture))

  const actions = cell(element, factory, 'div', 'graph-actions')
  const view = cell(actions, factory, 'button', 'graph-view')
  view.setAttribute('type', 'button')
  view.setAttribute('data-view', picture.state.view)
  view.setAttribute('title', GRAPH_VIEW_NAMES[picture.state.view])
  literal(view, GRAPH_VIEW_LABELS[picture.state.view])

  const refresh = cell(actions, factory, 'button', 'graph-refresh')
  refresh.setAttribute('type', 'button')
  literal(refresh, GRAPH_REFRESH_LABEL)

  // Fit is the canvas's own control: there is no viewBox to reset while the list
  // is up, and a button that did nothing would be a control that does nothing.
  if (picture.state.view === 'graph') {
    const fit = cell(actions, factory, 'button', 'graph-fit')
    fit.setAttribute('type', 'button')
    literal(fit, GRAPH_FIT_LABEL)
  }

  if (picture.model.notShown > 0) {
    // One text run and not two slots: the sentence is the console's own, and the
    // count in it is a number this console counted. A console sentence with a
    // count is a sentence, and splitting it into elements would make a reader
    // reassemble it (the same rule `live.ts`'s review heading keeps).
    literal(cell(element, factory, 'p', 'graph-not-shown'), notShownNote(picture.model.notShown))
  }
  return element
}

/** tabs draws one tab per stream, then All (the addendum's own order: the
 * document's streams, then everything at once). */
function tabs(factory: ScreenFactory, picture: GraphPicture): ScreenElement {
  const nav = make(factory, 'nav', 'graph-tabs')
  nav.setAttribute('role', 'tablist')
  for (const option of picture.model.tabs) {
    const button = cell(nav, factory, 'button', 'graph-tab')
    button.setAttribute('type', 'button')
    button.setAttribute('role', 'tab')
    button.setAttribute('data-tab', tabKey(option.tab))
    const selected = option.tab === picture.model.tab
    button.setAttribute('aria-selected', selected ? 'true' : 'false')
    button.setAttribute('tabindex', selected ? '0' : '-1')
    drawInto(button, option.label)
  }
  return nav
}

/**
 * chips draws the finished releases, grouped by stream: a chip is a button whose
 * click is the only thing that opens a box (the spec: "expand only when
 * clicked"), and an open chip draws the release's tasks as a list -- the one
 * place a finished release's tasks are drawn, because the canvas leaves them out
 * until the chip is open.
 */
function chips(factory: ScreenFactory, picture: GraphPicture): ScreenElement | null {
  if (picture.model.chips.length === 0) return null
  const element = make(factory, 'div', 'graph-chips')
  for (const group of picture.model.chips) {
    const section = cell(element, factory, 'section', 'graph-chip-group')
    section.setAttribute('data-stream', group.stream)
    drawn(section, factory, 'h2', group.label, 'graph-chip-stream')
    const list = cell(section, factory, 'ul', 'graph-chip-list')
    for (const release of group.chips) {
      list.append(chip(factory, picture, release))
    }
  }
  return element
}

/** chip is one finished release: its name, the finished mark, its version, and --
 * when it is open -- the tasks of that release. */
function chip(factory: ScreenFactory, picture: GraphPicture, release: GraphRelease): ScreenElement {
  const item = make(factory, 'li', 'graph-chip-item')
  const key = releaseKey(release.plan, release.version)
  const open = picture.state.expanded.includes(key)
  const button = cell(item, factory, 'button', 'graph-chip')
  button.setAttribute('type', 'button')
  button.setAttribute('data-chip', key)
  button.setAttribute('aria-expanded', open ? 'true' : 'false')
  drawn(button, factory, 'span', release.heading.name, 'graph-chip-name')
  literal(cell(button, factory, 'span', 'graph-chip-mark'), GRAPH_CHIP_MARK)
  drawn(button, factory, 'span', release.heading.version, 'graph-chip-version')
  if (!open) return item
  const plan = planOf(picture.model, release.plan)
  if (plan === null) return item
  const body = cell(item, factory, 'div', 'graph-chip-body')
  literal(cell(body, factory, 'p', 'graph-release-bar'), barText(release))
  const tasks = cell(body, factory, 'ul', 'graph-chip-tasks')
  for (const node of plan.nodes) {
    if (node.release !== release.version) continue
    const row = cell(tasks, factory, 'li', 'graph-chip-task')
    drawn(row, factory, 'span', node.title, 'graph-chip-task-title')
    row.append(words(row, factory, node))
  }
  return item
}

/** planOf finds a plan by its id: the chip's own body is drawn from the plan the
 * model put the release's tasks in. */
function planOf(model: GraphModel, id: string): GraphPlan | null {
  for (const column of model.columns) {
    for (const plan of column.plans) if (plan.id === id) return plan
  }
  return null
}

/**
 * canvas is the picture: the stream bands, the release boxes, the edges and the
 * nodes, in that order, so a band is behind the row it names, a box is behind
 * the tasks in it and an edge is behind every card.
 *
 * `null` is the honest answer for a graph with nothing placed -- a layout the
 * worker did not answer, or a model with no node. It is not an empty canvas: the
 * screen draws the list and `GRAPH_NO_CANVAS` instead, because an empty canvas
 * and a graph with nothing in it look the same and mean two different things.
 */
function canvas(factory: GraphFactory, picture: GraphPicture): ScreenElement | null {
  if (picture.positions === null) return null
  const placed = new Map<string, Rect>()
  for (const node of picture.positions.nodes) {
    placed.set(node.id, { x: node.x, y: node.y, width: node.width, height: node.height })
  }
  const bands = streamBands(picture, placed)
  const bounds = contentBounds(
    picture.model,
    placed,
    bands.map((one) => one.rect),
  )
  if (bounds === null) return null

  const svg = makeNS(factory, 'svg', 'graph-canvas')
  svg.setAttribute('role', 'group')
  svg.setAttribute('aria-label', GRAPH_CANVAS_LABEL)
  svg.setAttribute('viewBox', viewBoxOf(bounds, picture.state.zoom))

  for (const one of bands) svg.append(band(factory, one))

  const stop = doorStop(picture, drawnKeys(picture, 'canvas'))
  for (const column of picture.model.columns) {
    for (const plan of column.plans) drawPlan(factory, svg, picture, plan, placed, stop)
  }
  return svg
}

/** drawPlan appends one plan's release boxes, its edges and its nodes. */
function drawPlan(
  factory: GraphFactory,
  svg: ScreenElement,
  picture: GraphPicture,
  plan: GraphPlan,
  placed: ReadonlyMap<string, Rect>,
  stop: string | null,
): void {
  const shown = visibleNodes(picture, plan)
  for (const box of releaseBoxes(picture, plan, placed, shown)) {
    svg.append(releaseBox(factory, box))
  }
  // The edges next, so a card is drawn over the line that ends at it.
  for (const edge of plan.edges) {
    if (!shown.has(nodeKey(plan.id, edge.from)) || !shown.has(nodeKey(plan.id, edge.to))) continue
    const route = picture.positions?.edges.find((each) => each.id === edgeKey(plan.id, edge.from, edge.to))
    if (route === undefined) continue
    const path = pathOf(route.sections)
    if (path === '') continue
    const group = makeNS(factory, 'g', 'graph-edge')
    // A selected node highlights the cross-release edges that touch it, which is
    // the spec's "they highlight when a task is selected": the fact is on the
    // edge, so a rule can draw it without knowing which node was clicked.
    const touching = picture.state.selected === nodeKey(plan.id, edge.from) || picture.state.selected === nodeKey(plan.id, edge.to)
    group.setAttribute('data-cross', edge.crossRelease ? 'true' : 'false')
    group.setAttribute('data-critical', edge.critical ? 'true' : 'false')
    group.setAttribute('data-selected', touching ? 'true' : 'false')
    const line = makeNS(factory, 'path', 'graph-edge-path')
    line.setAttribute('d', path)
    group.append(line)
    svg.append(group)
  }
  for (const node of plan.nodes) {
    if (!shown.has(nodeKey(plan.id, node.id))) continue
    const rect = placed.get(nodeKey(plan.id, node.id))
    if (rect === undefined) continue
    svg.append(nodeCard(factory, picture, plan, node, rect, stop))
  }
}

/** visibleNodes is the nodes of a plan the canvas draws: every task of a release
 * that has not finished, plus the tasks of a finished release whose chip is open.
 *
 * A finished release's tasks are **not** drawn while its chip is shut -- that is
 * what the chip is for -- and the layout still placed them, which leaves the room
 * they take empty rather than moving every other box when a chip opens. A task
 * the plan gives no version is not a finished release's and is always drawn.
 */
function visibleNodes(picture: GraphPicture, plan: GraphPlan): ReadonlySet<string> {
  const finished = finishedVersions(picture.model, plan.id)
  const expanded = new Set(picture.state.expanded)
  const shown = new Set<string>()
  for (const node of plan.nodes) {
    if (finished.has(node.release) && !expanded.has(releaseKey(plan.id, node.release))) continue
    shown.add(nodeKey(plan.id, node.id))
  }
  return shown
}

/**
 * drawnKeys is the tasks one drawing draws, as their layout keys, in the order
 * the tab order sees them -- the order both drawings append their doors in, which
 * is the order the entry walks them with the arrow keys.
 *
 * The two drawings do not draw the same set: the canvas leaves a finished
 * release's tasks out until its chip is open (`visibleNodes`), and the list draws
 * every task of every plan.
 */
function drawnKeys(picture: GraphPicture, drawing: 'canvas' | 'list'): readonly string[] {
  const drawn: string[] = []
  for (const column of picture.model.columns) {
    for (const plan of column.plans) {
      const shown = drawing === 'canvas' ? visibleNodes(picture, plan) : null
      for (const node of plan.nodes) {
        const key = nodeKey(plan.id, node.id)
        if (shown === null || shown.has(key)) drawn.push(key)
      }
    }
  }
  return drawn
}

/**
 * doorStop is the one door a drawing puts in the tab order: the selected task
 * when this drawing draws it, and the **first** door it draws otherwise.
 *
 * **A group with no stop is a group the keyboard cannot enter at all**, which is
 * the one thing a roving `tabindex` may not do -- the rule the navigation's own
 * `markButtons` keeps, and the failure the doors had: nothing is selected when
 * the screen opens, and with every door at `-1` no task could be selected, no run
 * opened and the arrow walk never started. The fallback is a stop and not a
 * selection: the first door is where the keyboard enters, and it is not drawn as
 * chosen.
 *
 * The selected task may be a door this drawing does not draw -- a plan read can
 * finish the release it is in, and the canvas leaves a finished release's tasks
 * out -- so the fallback is what the drawing has rather than what the state says.
 */
function doorStop(picture: GraphPicture, drawn: readonly string[]): string | null {
  const selected = picture.state.selected
  if (selected !== null && drawn.includes(selected)) return selected
  return drawn[0] ?? null
}

/** finishedVersions is the versions a plan has shipped: the model's chips are
 * exactly the finished releases, and a plan's own `releases` are the unfinished
 * ones. */
function finishedVersions(model: GraphModel, plan: string): ReadonlySet<string> {
  const versions = new Set<string>()
  for (const group of model.chips) {
    for (const chip of group.chips) if (chip.plan === plan) versions.add(chip.version)
  }
  return versions
}

/** releasesOf is a plan's releases by version: its own unfinished boxes, and the
 * chips it contributed -- so a box's heading is the model's own sanitized parts
 * whichever kind of release it is. */
function releasesOf(picture: GraphPicture, plan: GraphPlan): ReadonlyMap<string, GraphRelease> {
  const releases = new Map<string, GraphRelease>()
  for (const release of plan.releases) releases.set(release.version, release)
  for (const group of picture.model.chips) {
    for (const chip of group.chips) if (chip.plan === plan.id) releases.set(chip.version, chip)
  }
  return releases
}

interface ReleaseBox {
  readonly key: string
  readonly release: GraphRelease
  readonly rect: Rect
}

/** releaseBoxes is the boxes to draw for one plan: one per release with a task on
 * the picture, in the model's own release order. */
function releaseBoxes(
  picture: GraphPicture,
  plan: GraphPlan,
  placed: ReadonlyMap<string, Rect>,
  shown: ReadonlySet<string>,
): readonly ReleaseBox[] {
  const boxes: ReleaseBox[] = []
  for (const [version, release] of releasesOf(picture, plan)) {
    const rects: Rect[] = []
    for (const node of plan.nodes) {
      if (node.release !== version) continue
      if (!shown.has(nodeKey(plan.id, node.id))) continue
      const rect = placed.get(nodeKey(plan.id, node.id))
      if (rect !== undefined) rects.push(rect)
    }
    const rect = boxOf(rects)
    if (rect === null) continue
    boxes.push({ key: releaseKey(plan.id, version), release, rect })
  }
  return boxes
}

/** RELEASE_PARTS is a release heading's three parts, in the order both drawings
 * put them: the class each part carries, and the value it holds. The class names
 * are the same in the box and in the list, so one rule styles both. */
function releaseParts(release: GraphRelease): readonly (readonly [string, Sanitized])[] {
  return [
    ['graph-release-stream', release.heading.stream],
    ['graph-release-name', release.heading.name],
    ['graph-release-version', release.heading.version],
  ]
}

/** releaseBox is one release's box: the rectangle the tasks sit in, the heading
 * above them, and the bar under it. */
function releaseBox(factory: GraphFactory, box: ReleaseBox): ScreenElement {
  const group = makeNS(factory, 'g', 'graph-release')
  group.setAttribute('data-release', box.key)
  const outline = makeNS(factory, 'rect', 'graph-release-box')
  place(outline, box.rect)
  outline.setAttribute('rx', '8')
  group.append(outline)

  const head = makeNS(factory, 'text', 'graph-release-heading')
  geometry(head, 'x', box.rect.x + 8)
  geometry(head, 'y', box.rect.y + 13)
  for (const [className, value] of releaseParts(box.release)) {
    drawnNS(head, factory, 'tspan', value, className)
  }
  group.append(head)

  const bar = makeNS(factory, 'text', 'graph-release-bar')
  geometry(bar, 'x', box.rect.x + 8)
  geometry(bar, 'y', box.rect.y + 26)
  literal(bar, barText(box.release))
  group.append(bar)
  return group
}

/**
 * cardLabel is what a reader who cannot see the card is told it is: the title
 * **whole** and every word the card draws.
 *
 * The drawing cuts the title at three lines and the model cuts it again at
 * `GRAPH_MAX_TITLE_CODE_POINTS`, so the card is not a name -- it is a picture of
 * one, and a screen reader handed the `tspan`s would read a sentence that stops
 * mid-word. `critical` is here for the same reason its close cousin
 * `data-critical` exists: its mark is the glow, which is `aria-hidden`, and a
 * word in a name is the only place that fact can survive with no colour and no
 * motion.
 */
function cardLabel(node: GraphNode): string {
  const title = cardTitle(node)
  return node.words.length === 0 ? title : [title, ...node.words].join(', ')
}

/**
 * cardTitle is the text a card is named by: the model's own title, or -- where
 * that title holds no word a reader could read -- the task's id.
 *
 * A title of whitespace alone is `ok` to the model and wraps to no word at all,
 * so naming a card by it names it by a blank, which is a card a reader is told
 * nothing about -- it is a stop in the tab order that announces no name at all.
 * The task's id is what such a card can still be told apart by.
 */
function cardTitle(node: GraphNode): string {
  if (!node.title.ok) return UNAVAILABLE_MARK
  return wrapWords(node.title.text).length === 0 ? node.id : node.title.text
}

/**
 * nodeCard is one task's box: its title, its words, its pull request number and
 * its run, and -- when the gesture would do nothing -- the sentence saying why.
 *
 * The card carries `data-key` (its layout key), `data-state` (its first word),
 * `data-critical` (whether it is on the plan's critical path, which its first
 * word cannot say) and, when a gesture opens a run, `data-run-id` (the run the
 * console's own list resolved the suffix to). It carries **no destination**: the
 * design's §5.1 rule 2 and gap 7 leave the console no URL to build, so the pull
 * request is a number and its reason.
 *
 * Its `role` is what the keyboard does with it, and nothing else: where there is
 * a run to open, Enter and Space open it and the card is a `button`; where there
 * is not, the arrow keys move over it and no key does anything, and a `button`
 * there would promise a reader a control that is not one.
 */
function nodeCard(
  factory: GraphFactory,
  picture: GraphPicture,
  plan: GraphPlan,
  node: GraphNode,
  rect: Rect,
  stop: string | null,
): ScreenElement {
  const key = nodeKey(plan.id, node.id)
  const door = doorFor(node, picture.runs)
  const selected = key === picture.state.selected
  // The roving `tabindex` is the drawing's own stop (`doorStop`) and not the
  // selection, which keeps its own attribute below.
  const stopHere = key === stop
  const group = makeNS(factory, 'g', 'graph-node')
  group.setAttribute('data-key', key)
  group.setAttribute('data-state', node.words[0] ?? '')
  group.setAttribute('data-selected', selected ? 'true' : 'false')
  // The path is a fact of its own and not a first word: a critical task that is
  // running says `implementing` in `data-state`, so the pulse's rule has nothing
  // else to select (design section 2.11's attribute table).
  group.setAttribute('data-critical', node.critical ? 'true' : 'false')
  group.setAttribute('tabindex', stopHere ? '0' : '-1')
  group.setAttribute('role', door.run !== null ? 'button' : 'group')
  group.setAttribute('aria-label', cardLabel(node))
  shift(group, rect.x, rect.y)
  if (door.run !== null) group.setAttribute('data-run-id', door.run)
  else group.append(tooltip(factory, door.because))

  // The glow is drawn **before** the card, because "behind the node" is where the
  // spec puts it: the card's own fill hides the middle of it and what a reader
  // sees is the spill around the card, which is the state's own tone on the
  // boundary and the glow outside it rather than one covering the other.
  if (node.critical) {
    const glow = makeNS(factory, 'rect', 'graph-node-pulse')
    glow.setAttribute('aria-hidden', 'true')
    geometry(glow, 'x', -GRAPH_GLOW_OFFSET)
    geometry(glow, 'y', -GRAPH_GLOW_OFFSET)
    geometry(glow, 'width', rect.width + GRAPH_GLOW_OFFSET * 2)
    geometry(glow, 'height', rect.height + GRAPH_GLOW_OFFSET * 2)
    glow.setAttribute('rx', GRAPH_GLOW_RADIUS)
    group.append(glow)
  }

  const outline = makeNS(factory, 'rect', 'graph-node-box')
  geometry(outline, 'x', 0)
  geometry(outline, 'y', 0)
  geometry(outline, 'width', rect.width)
  geometry(outline, 'height', rect.height)
  outline.setAttribute('rx', GRAPH_NODE_RADIUS)
  group.append(outline)

  // The comet is the card's own border drawn a second time, and the streak is the
  // dash the stylesheet gives it: `pathLength` is what makes that dash a fraction
  // of the perimeter rather than a length in user units, so the stylesheet never
  // has to know how big a card is. Both marks are `aria-hidden`: a moving light is
  // not a fact a reader needs twice, and the word beside it says the same thing
  // (design section 3.5).
  if (node.words[0] !== undefined && GRAPH_RUNNING_WORDS.includes(node.words[0])) {
    const comet = makeNS(factory, 'rect', 'graph-node-comet')
    comet.setAttribute('aria-hidden', 'true')
    comet.setAttribute('pathLength', '100')
    geometry(comet, 'x', 0)
    geometry(comet, 'y', 0)
    geometry(comet, 'width', rect.width)
    geometry(comet, 'height', rect.height)
    comet.setAttribute('rx', GRAPH_NODE_RADIUS)
    group.append(comet)
  }

  // The title is one `text` with a `tspan` per line, each carrying its own x and
  // y: SVG has no wrap, so the lines are the screen's, and a `tspan` is the one
  // element that draws a second line of the same run.
  const title = makeNS(factory, 'text', 'graph-node-title')
  geometry(title, 'x', GRAPH_WORD_SLOT)
  geometry(title, 'y', GRAPH_TITLE_LINE_Y)
  titleLines(node.title).forEach((line, at) => {
    const element = cellNS(title, factory, 'tspan', 'graph-node-title-line')
    geometry(element, 'x', GRAPH_WORD_SLOT)
    geometry(element, 'y', GRAPH_TITLE_LINE_Y + at * GRAPH_TITLE_LINE_HEIGHT)
    literal(element, line)
  })
  group.append(title)

  wordsNS(group, factory, node)

  if (node.prNumber !== null) {
    const number = makeNS(factory, 'text', 'graph-node-pr')
    geometry(number, 'x', GRAPH_PR_SLOT)
    geometry(number, 'y', GRAPH_META_LINE_Y)
    literal(number, `#${String(node.prNumber)}`)
    number.append(tooltip(factory, GRAPH_PR_NO_LINK_REASON))
    group.append(number)
  }
  if (node.run.ok && node.run.text !== '') {
    const run = makeNS(factory, 'text', 'graph-node-run')
    geometry(run, 'x', GRAPH_RUN_SLOT)
    geometry(run, 'y', GRAPH_META_LINE_Y)
    literal(run, `run …${node.run.text}`)
    group.append(run)
  }
  if (node.mark.ok && node.mark.text !== '') {
    const mark = makeNS(factory, 'text', 'graph-node-mark')
    geometry(mark, 'x', GRAPH_MARK_SLOT)
    geometry(mark, 'y', GRAPH_META_LINE_Y)
    drawInto(mark, node.mark)
    group.append(mark)
  }
  return group
}

/**
 * list is the same model as a list: one section per plan, one heading per
 * release with the same "n of m done" bar, and one row per task -- carrying
 * everything the card carries and the whole title with it (F25: the picture's
 * non-visual equivalent).
 */
function list(factory: ScreenFactory, picture: GraphPicture): ScreenElement {
  const element = make(factory, 'div', 'graph-list')
  element.setAttribute('role', 'list')
  element.setAttribute('aria-label', GRAPH_LIST_LABEL)
  const stop = doorStop(picture, drawnKeys(picture, 'list'))
  for (const column of picture.model.columns) {
    for (const plan of column.plans) {
      const section = cell(element, factory, 'section', 'graph-list-plan')
      drawn(section, factory, 'h2', plan.title, 'graph-list-title')
      for (const release of releasesOf(picture, plan).values()) {
        section.append(listRelease(factory, release))
      }
      const nodes = cell(section, factory, 'ul', 'graph-list-nodes')
      for (const node of plan.nodes) nodes.append(listRow(factory, picture, plan, node, stop))
    }
  }
  return element
}

/** listRelease is a release's own heading in the list: the three parts the box
 * draws, then the same bar. */
function listRelease(factory: ScreenFactory, release: GraphRelease): ScreenElement {
  const heading = make(factory, 'h3', 'graph-list-release')
  for (const [className, value] of releaseParts(release)) {
    drawn(heading, factory, 'span', value, className)
  }
  literal(cell(heading, factory, 'span', 'graph-release-bar'), barText(release))
  return heading
}

/** listRow is one task's row: its title, its words, its mark, its pull request
 * number and its run. It carries the same facts the card does, and the title
 * whole -- the card is where a title is cut, and the list is where it is not. */
function listRow(
  factory: ScreenFactory,
  picture: GraphPicture,
  plan: GraphPlan,
  node: GraphNode,
  stop: string | null,
): ScreenElement {
  const key = nodeKey(plan.id, node.id)
  const door = doorFor(node, picture.runs)
  const selected = key === picture.state.selected
  // The roving `tabindex` is the drawing's own stop (`doorStop`), as it is on a
  // card: a row and a card are the same door drawn twice.
  const stopHere = key === stop
  const row = make(factory, 'li', 'graph-row')
  row.setAttribute('role', 'listitem')
  row.setAttribute('data-key', key)
  row.setAttribute('data-state', node.words[0] ?? '')
  row.setAttribute('data-selected', selected ? 'true' : 'false')
  row.setAttribute('tabindex', stopHere ? '0' : '-1')
  // The gesture's own sentence, in the attribute an HTML element keeps a tooltip
  // in -- the same fact the card carries as a `<title>` child.
  row.setAttribute('title', door.because)
  if (door.run !== null) row.setAttribute('data-run-id', door.run)
  drawn(row, factory, 'span', node.title, 'graph-row-title')
  row.append(words(row, factory, node))
  if (node.mark.ok && node.mark.text !== '') drawn(row, factory, 'span', node.mark, 'graph-row-mark')
  if (node.prNumber !== null) {
    const number = literal(cell(row, factory, 'span', 'graph-row-pr'), `#${String(node.prNumber)}`)
    number.setAttribute('title', GRAPH_PR_NO_LINK_REASON)
  }
  if (node.run.ok && node.run.text !== '') {
    literal(cell(row, factory, 'span', 'graph-row-run'), `run …${node.run.text}`)
  }
  return row
}

/**
 * renderGraph draws one picture. It is a function of the model, the positions,
 * the state and the run list and of nothing else: two calls with the same four
 * draw the same tree, which is what lets the entry re-draw on a state change
 * without asking the worker anything.
 */
export function renderGraph(factory: GraphFactory, picture: GraphPicture, theme: GraphTheme): ScreenElement {
  const root = make(factory, 'section', 'graph')
  root.setAttribute('data-theme', theme)
  // The section's busy slot, and the first child of every drawing: the Graph's
  // own controls (a stream tab, the refresh) start reads of their own, and a
  // read that answers in twenty seconds needs somewhere to say so **above** the
  // picture rather than under a canvas nobody scrolls to (`renderer/section.ts`).
  // It is empty until a mark or a cache stamp is appended into it, and the
  // stylesheet takes an empty slot out of the layout entirely, so a drawing of a
  // graph nobody is re-reading is the drawing it always was.
  cell(root, factory, 'div', BUSY_SLOT_CLASS)
  root.append(header(factory, picture))
  const group = chips(factory, picture)
  if (group !== null) root.append(group)
  if (picture.model.columns.length === 0) {
    literal(cell(root, factory, 'p', 'graph-empty'), GRAPH_NO_PLANS)
    return root
  }
  if (picture.state.view === 'list') {
    root.append(list(factory, picture))
    return root
  }
  const drawnCanvas = canvas(factory, picture)
  if (drawnCanvas !== null) {
    root.append(drawnCanvas)
    return root
  }
  // The canvas is the only thing lost when the worker did not place the graph,
  // and the list is still true: it is drawn instead, saying why.
  literal(cell(root, factory, 'p', 'graph-empty'), GRAPH_NO_CANVAS)
  root.append(list(factory, picture))
  return root
}

// ---------------------------------------------------------------------------
// The screen
// ---------------------------------------------------------------------------

/** GraphScreenDeps is what a screen is built from: where elements come from, the
 * layout client it owns, and the theme it draws in. */
export interface GraphScreenDeps {
  readonly factory: GraphFactory
  readonly client: LayoutClient
  readonly theme: GraphTheme
}

/**
 * GraphScreen is the screen and the state a person chose.
 *
 * **It owns the layout cache, and with it the decision to lay anything out at
 * all.** `load` and `frame` are the two doors a document comes through: each
 * folds the feed's stages in, builds the model, asks the cache for positions and
 * answers a drawing. A frame that changed no structure asks the same cache the
 * same question and gets the same answer back **without a message to the
 * worker** -- GV2.3a's cache is the mechanism, and `build` below is the only
 * thing that decides when it is asked. `change` is the third door and does not
 * ask at all: a tab, a selection, an open chip and a zoom are this screen's own,
 * none of them is in the structure hash, and `redraw` is a function of what the
 * last read already placed.
 *
 * **A superseded ask draws nothing.** Two reads can be outstanding at once (the
 * poll and the operator's refresh), and the first can answer last: each ask takes
 * a ticket, and an ask whose ticket is no longer the latest answers `null` rather
 * than putting an older picture on the panel.
 */
export class GraphScreen {
  private state: GraphState = GRAPH_INITIAL_STATE
  private document: PlanStatusView | null = null
  private runs: readonly string[] = []
  private feed: readonly RunView[] = []
  private model: GraphModel | null = null
  private positions: Positions | null = null
  private asked = 0
  private disposed = false
  /** drawnStages is what the last frame said about **this graph's** stages, as
   * one string, so an identical frame is answered without drawing anything. */
  private drawnStages = ''
  private readonly layout: GraphLayout

  constructor(private readonly deps: GraphScreenDeps) {
    this.layout = new GraphLayout(deps.client)
  }

  /** current is the state the last drawing was made with. */
  get current(): GraphState {
    return this.state
  }

  /** lastModel is the model the last drawing was made from, or null before the
   * first read. */
  get lastModel(): GraphModel | null {
    return this.model
  }

  /** load takes a plan document and the console's run list and answers the next
   * drawing -- or `null` when a later ask has already superseded this one. */
  async load(document: PlanStatusView, runs: readonly string[]): Promise<ScreenElement | null> {
    this.document = document
    this.runs = runs
    this.feed = []
    this.drawnStages = ''
    return this.build()
  }

  /**
   * decorate attaches the console's run list to the picture already on screen
   * and answers the drawing it makes.
   *
   * **It is the second half of the read, and it is deliberately not part of
   * `load`.** `runs ls --json` is a read of its own -- several seconds and a
   * quarter of a megabyte for a day of runs on the owner's installation -- and
   * the plan is the graph: a canvas that waited for the run list before drawing
   * anything was a Graph tab that took both reads to answer at all. The list is
   * what a node's door is resolved against (four characters of a run id), so it
   * **decorates** a picture that is already true without it.
   *
   * Nothing is asked of the layout: the run list is not in the structure hash,
   * and the drawing it answers is the same graph with more doors in it. The
   * positions the last `build` answered with are reused as they are, which is
   * what makes this free.
   */
  decorate(runs: readonly string[]): ScreenElement | null {
    this.runs = runs
    return this.redraw()
  }

  /**
   * frame takes one live frame's rows: the stages change, the structure does
   * not, and the cache is what makes that free.
   *
   * A frame that says exactly what the last one said answers **nothing**: the
   * poll runs once a second and most frames repeat themselves, and redrawing an
   * identical tree every second would replace the picture a reader is looking at
   * with a copy of it -- losing, among other things, where the focus was. What
   * "says" means is `drawnStages` above: the stages **this graph draws**, not
   * every stage in a frame that is installation-wide.
   */
  async frame(feed: readonly RunView[]): Promise<ScreenElement | null> {
    if (this.document === null) return null
    const key = drawnStages(this.document, feed)
    if (key === this.drawnStages) return null
    this.drawnStages = key
    this.feed = feed
    return this.build()
  }

  /**
   * change applies one state change and answers the drawing it makes, **without
   * asking the layout anything at all**: the view, the selection, the open chips
   * and the zoom are the screen's own, none of them is in the structure hash, and
   * `redraw` is a function of what the last read already placed.
   *
   * A **tab** is deliberately not one of them and is not accepted here. Choosing
   * a tab changes which plans the model holds, and with it the graph the engine
   * laid out, so it goes through `narrow` below -- which asks, because it must.
   */
  change(part: Omit<Partial<GraphState>, 'tab'>): ScreenElement | null {
    this.state = { ...this.state, ...part, zoom: clampZoom(part.zoom ?? this.state.zoom) }
    return this.redraw()
  }

  /** narrow chooses a tab. The graph is a different graph afterwards -- fewer
   * plans, fewer tasks, fewer edges -- so this is a read of the same document
   * rather than a redraw of the same picture, and the cache answers it with a
   * layout of its own. */
  async narrow(tab: GraphTab): Promise<ScreenElement | null> {
    this.state = { ...this.state, tab }
    return this.build()
  }

  /** redraw is the picture the last read and the current state make. */
  redraw(): ScreenElement | null {
    if (this.model === null) return null
    return renderGraph(
      this.deps.factory,
      { model: this.model, positions: this.positions, state: this.state, runs: this.runs },
      this.deps.theme,
    )
  }

  /**
   * forget drops the document the last read drew, and the model and the
   * positions built from it, **keeping the state**: the tab, the drawing, the
   * selection, the open chips and the zoom are the operator's and a re-read
   * draws the same view of a fresh document.
   *
   * The entry calls this on every refusal. A refusal is a screen with nothing on
   * it, and the *previous* plan is still in this screen's hands: a live frame --
   * the graph's other input, folded into the document the screen holds -- would
   * draw that older plan again over the refusal, which says the plan is fine when
   * the read that just failed says it is not. The layout cache and the worker are
   * deliberately kept: they hold positions and no document, and the next read
   * lays out the graph it is handed.
   */
  forget(): void {
    this.document = null
    this.model = null
    this.positions = null
    this.feed = []
    this.drawnStages = ''
  }

  /** dispose drops the cache and the worker with it. The graph belongs to one
   * installation's checkout: the entry calls this when the console moves to
   * another one, so a plan of the old installation is never drawn from the new
   * one's positions. */
  dispose(): void {
    this.disposed = true
    this.layout.dispose()
  }

  private async build(): Promise<ScreenElement | null> {
    if (this.disposed || this.document === null) return null
    const model = buildGraphModel({ document: withFeedStages(this.document, this.feed), tab: this.state.tab })
    const ticket = (this.asked += 1)
    let positions: Positions
    try {
      positions = await this.layout.positions(model)
    } catch {
      // A worker that answered something undrawable, or one that failed: the
      // model is still a graph and the list view of it is still true, so the
      // drawing falls back to the list with `GRAPH_NO_CANVAS` rather than to a
      // refusal screen. The canvas is the only thing lost.
      positions = { nodes: [], edges: [] }
    }
    if (ticket !== this.asked || this.disposed) return null
    this.model = model
    this.positions = positions
    return this.redraw()
  }
}

/**
 * GraphLayoutClient is the layout client this screen asks for a drawing: the
 * plain client, with one option added to every request.
 *
 * The engine's own room between two layers of tasks is 20 px
 * (`elk.layered.spacing.nodeNodeBetweenLayers`' default), which is less than a
 * release box draws above its topmost task: `boxOf` grows the box upward by
 * GRAPH_PAD + GRAPH_RELEASE_HEAD, and `drawPlan` appends every box before every
 * card, so with the default room the heading is painted into the layer to the
 * left of it and that layer's cards -- drawn later -- cover it. Asking for
 * GRAPH_LAYER_GAP is what keeps the heading in the gap, so it is the request
 * that carries the fix rather than the drawing code: a shorter head would leave
 * the heading under the cards at the same 20 px.
 *
 * The same gap is asked for as the room **within** a layer
 * (`elk.spacing.nodeNode`) as well, and that is the redesign's own need: the
 * direction is left to right, so a layer is a column of stacked cards, and both
 * the heading above a release's topmost task and the band's label above a
 * stream's topmost task are drawn upward into the room between two stacked
 * cards. A heading drawn over the card above it is what the engine's 20 px
 * would give.
 *
 * The room between two components is asked for at the same gap, and for the same
 * reason: the engine's own room there is that same 20 px, and a task no edge
 * joins is a component of its own, so the next release's heading is drawn under
 * the isolated task's card with nothing in the drawing code to tell the two
 * apart.
 */
export class GraphLayoutClient extends LayoutClient {
  override layout(graph: LayoutGraph, request: LayoutRequest = {}): Promise<Positions> {
    return super.layout(graph, {
      ...request,
      layoutOptions: {
        ...request.layoutOptions,
        [GRAPH_LAYER_OPTION]: String(GRAPH_LAYER_GAP),
        [GRAPH_NODE_OPTION]: String(GRAPH_LAYER_GAP),
        [GRAPH_COMPONENT_OPTION]: String(GRAPH_LAYER_GAP),
      },
    })
  }
}

/**
 * graphLayoutClient is the screen's worker, and the only place this console
 * starts one (GV2.2): a classic worker from the served file, same-origin under
 * the unchanged `script-src 'self'`.
 *
 * It is a function rather than a constant because a worker is a process: the
 * entry builds one when the graph is first opened and disposes of it when the
 * console moves to another installation, and a module-level worker would be one
 * this file could not close.
 */
export function graphLayoutClient(): LayoutClient {
  return new GraphLayoutClient(() => new Worker(WORKER_URL) as unknown as WorkerLike)
}

/** graphScreen is the screen the entry mounts: the real cache over the real
 * worker, in the theme the console draws. */
export function graphScreen(factory: GraphFactory, theme: GraphTheme): GraphScreen {
  return new GraphScreen({ factory, client: graphLayoutClient(), theme })
}
