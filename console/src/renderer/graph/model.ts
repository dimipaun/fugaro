// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The graph screen's model: `plan status --json` and the tab a person chose,
// turned into plain data -- stream columns, release boxes, chips, nodes and
// edges -- with no DOM in it at all (console GV2.3a; the owner's dependency
// graph spec §3 and its addendum §3, docs/console-dependency-graph/).
//
// **The split is the reason this module exists.** GV2.3 draws the screen and
// GV2.4 animates it, and both need the same answer to "what is on this
// picture"; a screen that built its own model would have to be read twice to
// know what an edge is. So the model is a function from two inputs -- the
// decoded document and the tab -- to data, and a test drives it with a document
// and asserts the data, with no browser.
//
// **Text stops here.** Every string this module carries out is either a raw
// *key* (a plan id, a task id, a release version, a stream name) that joins two
// records and is never spelled, or a `Sanitized` value from `shared/sanitize`
// with the class its wire key declares (`schema.ts`'s `WIRE_TEXT_CLASSES`). The
// model never hands a screen a document string to draw, which is what makes "a
// hostile title renders as text" a property of the type rather than a rule the
// screen has to remember.
//
// **A node's `run` is a suffix, and it is the one field that needs a shape the
// contract does not carry.** `plan status` writes the four characters a person
// types (`d204`), not the full run id (`20261025-121805-d204`) the `runId`
// class validates, so that class refuses the suffix. This module holds it to
// `^[0-9a-z]{4}$` and then runs it through the `label` class -- the smaller of
// the two answers the task allowed, and the one that leaves `TextClass` and
// `sanitize.ts` untouched. A `run` that is not four lower-case alphanumerics is
// a refusal, never a shortened value.

import { sanitizeText } from '../../shared/sanitize'
import type { Sanitized, TextClass } from '../../shared/sanitize'
import type { Reading } from '../../shared/availability'
import { wireTextClass } from '../../shared/schema'
import type { PlanNodeView, PlanReleaseView, PlanState, PlanStatusView, PlanView } from '../../shared/schema'

/**
 * GRAPH_MAX_PLANS, GRAPH_MAX_NODES_PER_PLAN and GRAPH_MAX_NODES bound one
 * model. A plan artifact is a checkout's own file and a repository's `docs/`
 * can hold any number of them, so what is bounded is the picture and not the
 * document: everything past a bound is dropped in document order -- never at
 * random, so two reads of one document draw the same graph -- and counted in
 * `notShown`, which the screen prints. A bound that truncated silently would
 * draw a graph that claims to be the whole plan.
 */
export const GRAPH_MAX_PLANS = 64
export const GRAPH_MAX_NODES_PER_PLAN = 500
export const GRAPH_MAX_NODES = 2000

/**
 * GRAPH_MAX_NEEDS bounds one node's dependencies. A task needing more than this
 * is a plan nobody wrote by hand, and a fan-in that wide is a picture with no
 * readable edges; the rest are dropped and counted, as above.
 */
export const GRAPH_MAX_NEEDS = 32

/**
 * GRAPH_MAX_TITLE_CODE_POINTS is how much of a node's title its box carries.
 * `sanitize.ts` already cut the sentence at 512 characters; a box is a card of
 * the width `GRAPH_NODE_WIDTH` gives it, so the model cuts again, and the cut
 * is marked (`CUT_MARK`) rather than silent -- a title shortened without saying
 * so reads as the whole title.
 */
export const GRAPH_MAX_TITLE_CODE_POINTS = 200

/**
 * GRAPH_NODE_WIDTH and GRAPH_NODE_HEIGHT are the box a node occupies, and they
 * are what the layout worker is told about each node (GV2.2's client takes ids
 * and sizes and never text). **They do not depend on the title**: the layout
 * cache is keyed by the graph's structure, a title that resized a box would
 * have to be in that key, and a watch frame that rewrote one word of one title
 * would then re-lay-out every node. A box is a fixed card, and a long title is
 * wrapped and -- only past what its lines hold -- cut, rather than resizing it.
 *
 * 260 x 88 is the redesign's card (console redesign, step 6a): three lines of a
 * title, the state's own word under them, and the pull request, the run and the
 * reading's mark along the foot.
 */
export const GRAPH_NODE_WIDTH = 260
export const GRAPH_NODE_HEIGHT = 88

/**
 * STATE_WORDS is every word a node can carry, in the order the design's §3.9
 * amendment lists them (`docs/design/console-styling.md`): each animated state
 * is drawn **as text** on the node, with its colour second, so a reader who
 * cannot tell two colours apart reads the same graph and a greyscale screenshot
 * is still a graph.
 *
 * The list is closed and `stateWords` below is its only producer. `stuck` is in
 * it and is unreachable from a plan node -- see `stateWords` for why.
 */
export const STATE_WORDS = Object.freeze([
  'implementing',
  'in review',
  'fixing',
  'queued',
  'waiting',
  'halted',
  'stuck',
  'blocked',
  'ready',
  'done',
  'critical',
] as const) satisfies readonly string[]

export type StateWord = (typeof STATE_WORDS)[number]

/** CUT_MARK ends a title this module cut. It is U+2026 and not the sanitizer's
 * three periods, so the two cuts stay distinguishable on screen and in a test:
 * this one is the console making a card fit, that one is the sanitizer refusing
 * to draw more than a sentence. */
const CUT_MARK = '…'

/** RUN_SUFFIX_RE is the shape `plan status` writes a joined run in: the run
 * id's last four characters, lower-case alphanumerics by construction (the CLI
 * mints the id, and the launch prints the suffix). */
const RUN_SUFFIX_RE = /^[0-9a-z]{4}$/

/** SUFFIX_CLASS is the class a run suffix is read in: `label`, spelled here
 * rather than looked up, because the wire key's own class (`runId`, from
 * `classOf('run')`) is the class of a **full** run id -- `20261025-121805-d204`
 * -- and refuses the four characters the contract actually writes. */
const SUFFIX_CLASS: TextClass = 'label'

/**
 * classOf is the class `schema.ts` gives a wire key. The keys used below are
 * literals of this file, and a `plan` field's class is the map's answer
 * (`label`) rather than its own name -- which is the whole reason the lookup is
 * here and not a class spelled at the call site.
 *
 * A key the map does not carry throws rather than falling back to a class: the
 * display layer never guesses what a string is, and a key this module invented
 * is this module's own bug -- a test drives every field below, so it fails
 * there rather than at a screen.
 */
function classOf(key: string): TextClass {
  const found = wireTextClass(key)
  if (found === undefined) throw new Error(`the graph model has no text class for the key ${key}`)
  return found
}

/** suffixRefusal is the console's sentence about a `run` that is not a suffix.
 * It names the field and never the value. */
function suffixRefusal(field: string): Sanitized {
  return { ok: false, reason: `${field} is not a four-character run suffix, so the console will not draw it` }
}

/** own is one of the console's own words, as a `Sanitized` value: a tab name
 * this module writes rather than one a document sent. */
function own(text: string): Sanitized {
  return { ok: true, text, flags: [] }
}

/**
 * GraphTab is which stream the graph is narrowed to: the name of one of the
 * document's streams, `''` for the plans that name no stream, or `null` for
 * every stream at once -- the All tab.
 *
 * `null` rather than a word for All, because a stream's name is the
 * catalogue's, and a sentinel string would be a name a document could take.
 */
export type GraphTab = string | null

/** GraphTabOption is one tab: what choosing it narrows to, and what to call it.
 * The name is the document's own (a stream's `name`) through the `stream`
 * class; the two the console writes itself -- the no-stream tab and All -- are
 * the console's own words. */
export interface GraphTabOption {
  readonly tab: GraphTab
  readonly label: Sanitized
}

/** GraphNodeState is the slice of a node the words are read from. It is a type
 * of its own because the words depend on exactly these fields and on nothing
 * else, which is the claim `stateWords` makes. */
export interface GraphNodeState {
  readonly state: PlanState
  readonly stage: string
  /** a mark is drawn beside the word (`plan.MergeUnknown`); it never renames
   * one, and it is here because the words answer for it too */
  readonly mark: string
  readonly critical: boolean
}

/** GraphReleaseHeading is a release box's heading in its three parts, each in
 * the class its wire key declares. The screen joins them; the model does not,
 * so a screen can lay them out and a test can assert them apart. */
export interface GraphReleaseHeading {
  readonly stream: Sanitized
  readonly name: Sanitized
  readonly version: Sanitized
}

/**
 * GraphRelease is one release: a target version of one plan, its heading, and
 * the two figures its bar carries. The bar reads "n of m done" from them, and
 * they are `Reading`s rather than numbers because a release whose counters the
 * document does not carry is drawn as `n/a` and not as `0 of 0` -- the
 * availability rule, and why `total` and `done` arrive here as the decoder's
 * readings rather than as integers.
 *
 * A finished release and an open one are the same value: a finished release is
 * a chip along the top and an open one is a box (the spec's addendum), and the
 * two are drawn differently by the screen rather than read differently here.
 */
export interface GraphRelease {
  readonly plan: string
  readonly stream: string
  readonly version: string
  readonly heading: GraphReleaseHeading
  readonly done: Reading
  readonly total: Reading
}

/** GraphChipGroup is the finished releases of one stream, collapsed into chips.
 * Grouped by stream because the addendum says the chips along the top are, and
 * grouped at all because a finished release is a hundred finished tasks nobody
 * wants drawn -- the box expands on click, and that click is the screen's. */
export interface GraphChipGroup {
  readonly stream: string
  readonly label: Sanitized
  readonly chips: readonly GraphRelease[]
}

/**
 * GraphNode is one task on the picture. `id` is the task's own id **as an
 * identity**: it is what this node's `needs` names and what the layout is keyed
 * by, and it is never drawn -- `title` is what a node says. `release` is a key
 * for the same reason (it groups the node into a box whose heading carries the
 * sanitized version), and `needs` is kept so that a screen can name a node's
 * dependencies without going back to the document.
 */
export interface GraphNode {
  readonly id: string
  readonly title: Sanitized
  readonly words: readonly StateWord[]
  readonly mark: Sanitized
  readonly stage: Sanitized
  readonly run: Sanitized
  readonly prNumber: number | null
  readonly critical: boolean
  readonly release: string
  readonly needs: readonly string[]
}

/**
 * GraphEdge is one dependency between two tasks of one plan: `from` is the task
 * that is needed and `to` the task that needs it -- the direction `needs` names
 * and the direction the spec's arrows point.
 *
 * `crossRelease` is the two ends sitting in different releases (a node the plan
 * gives no version is its own group, so an edge between a versioned and an
 * unversioned task is cross-release too), which the spec draws faint and thin.
 * `critical` is both ends being on the plan's critical path and one level
 * apart, which the spec draws thicker and distinct. Both are computed here
 * because both are claims about the document, and a screen that derived them
 * would be a second place they could drift.
 */
export interface GraphEdge {
  readonly from: string
  readonly to: string
  readonly crossRelease: boolean
  readonly critical: boolean
}

/** GraphPlan is one plan's picture: its open release boxes, its nodes and its
 * edges. Its finished releases are the model's chips. */
export interface GraphPlan {
  readonly id: string
  readonly title: Sanitized
  readonly stream: string
  readonly releases: readonly GraphRelease[]
  readonly nodes: readonly GraphNode[]
  readonly edges: readonly GraphEdge[]
  readonly notShown: number
}

/** GraphColumn is one stream's column: the plans that name it, or -- for the
 * `''` column -- the plans that name no stream. The All tab draws every column
 * side by side; a stream tab draws one. */
export interface GraphColumn {
  readonly stream: string
  readonly label: Sanitized
  readonly plans: readonly GraphPlan[]
}

/** GraphModel is one tab's whole picture. `notShown` is the sum of the plans',
 * plus one for every plan dropped past the `GRAPH_MAX_PLANS` cap. */
export interface GraphModel {
  readonly tab: GraphTab
  readonly tabs: readonly GraphTabOption[]
  readonly columns: readonly GraphColumn[]
  readonly chips: readonly GraphChipGroup[]
  readonly notShown: number
}

export interface GraphInputs {
  readonly document: PlanStatusView
  readonly tab: GraphTab
}

/**
 * stateWords is the words a node carries. It is **one exhaustive function over
 * `PlanState`**: a seventh state the CLI might write is a compile error here
 * rather than a node drawn with no word at all.
 *
 * The mapping, and the reason for each reading:
 *
 *   running + implement                     implementing
 *   running + review_first, review          in review
 *   running + fix                           fixing
 *   running + anything else (including "")  queued
 *   done                                    done
 *   cancelled                               halted
 *   blocked                                 blocked
 *   ready                                   ready
 *   not-started                             waiting
 *   any node, when `critical`               + critical
 *
 * The three coloured stages are the design's three (blue, purple, amber). Every
 * other stage a runner writes -- `plan`, `bootstrap`, `check`, `finalize`,
 * `render`, `writeback` -- and the empty stage of a run that has not reached
 * work yet read `queued`: "live, and not in a stage a person watches" is the
 * one thing `queued` can honestly mean for a node whose own report is only
 * "running" (`internal/plan/state.go` joins a run's *status*, not its stage
 * transition). `waiting` is `not-started`, the state a task whose run finished
 * with a draft, failed or was halted is in. `critical` is a second word beside
 * the first rather than a replacement, which is what "with its colour second"
 * means.
 *
 * **`stuck` is never produced.** The Live screen's `stuck` flag is a fact about
 * a *watch feed* -- a queued run that has not started after a while
 * (`watch.RunRow.Stuck`) -- and a plan node carries no such flag: `plan status`
 * reports a task's state, its level and whether it is on the critical path, and
 * "not started after N minutes" is derivable from none of them. So the word
 * stays in `STATE_WORDS` (the design's list is the design's) and is listed as
 * unreachable rather than guessed at from a field that is not there.
 *
 * `mark` is an input because the words answer for it -- a node with a mark
 * carries it beside its word -- and not because it can select one: a mark is
 * the reason a state is not what its word alone says, so renaming the word
 * would say the opposite of what the mark says.
 */
export function stateWords(node: GraphNodeState): readonly StateWord[] {
  const word = primaryWord(node)
  return node.critical ? [word, 'critical'] : [word]
}

function primaryWord(node: GraphNodeState): StateWord {
  switch (node.state) {
    case 'running':
      switch (node.stage) {
        case 'implement':
          return 'implementing'
        case 'review_first':
        case 'review':
          return 'in review'
        case 'fix':
          return 'fixing'
        default:
          return 'queued'
      }
    case 'done':
      return 'done'
    case 'cancelled':
      return 'halted'
    case 'blocked':
      return 'blocked'
    case 'ready':
      return 'ready'
    case 'not-started':
      return 'waiting'
  }
}

/**
 * buildGraphModel is the whole of this module: the document and the tab in,
 * plain data out.
 *
 * A tab the caller asked for that the document does not carry reads as All: a
 * tab is a stream name, and a stream the catalogue dropped between two reads
 * would otherwise draw an empty frame -- a graph that says "there is nothing
 * here" about a document full of plans. All is always one of the tabs, so the
 * fallback is always drawable.
 */
export function buildGraphModel(inputs: GraphInputs): GraphModel {
  const { document } = inputs
  const streams = streamOrder(document)
  const tabs = graphTabs(document, streams)
  const tab = tabs.some((option) => option.tab === inputs.tab) ? inputs.tab : null

  const plans: GraphPlan[] = []
  const chips: GraphRelease[] = []
  let notShown = 0
  let room = GRAPH_MAX_NODES
  let count = 0
  for (const [index, plan] of document.plans.entries()) {
    if (tab !== null && plan.stream !== tab) continue
    if (count >= GRAPH_MAX_PLANS) {
      notShown += 1
      continue
    }
    count += 1
    const built = graphPlan(plan, index, room)
    room -= built.plan.nodes.length
    plans.push(built.plan)
    chips.push(...built.chips)
    notShown += built.plan.notShown
  }

  const columns: GraphColumn[] = []
  for (const stream of streams) {
    const ofStream = plans.filter((plan) => plan.stream === stream)
    if (ofStream.length === 0) continue
    columns.push({ stream, label: streamLabel(document, stream), plans: ofStream })
  }

  return { tab, tabs, columns, chips: chipGroups(document, streams, chips), notShown }
}

/**
 * graphTabs is the tab list: one tab per column (`streamOrder`), then All. The
 * names are the document's and never hard-coded: a fourth stream is a row in the
 * catalogue and a tab here, with no console change.
 */
function graphTabs(document: PlanStatusView, streams: readonly string[]): readonly GraphTabOption[] {
  const tabs: GraphTabOption[] = streams.map((stream) => ({
    tab: stream,
    label: streamLabel(document, stream),
  }))
  tabs.push({ tab: null, label: own('All') })
  return tabs
}

/**
 * streamOrder is every column the graph can draw, catalogue first: the
 * catalogue's own order, then -- for a stream only a plan names -- the order
 * the plans appear in. The plans that name no stream follow as the `''` column,
 * and only when there are some: `''` is a column like any other here, so a plan
 * that names no stream is drawn rather than dropped -- which is what the tabs
 * list (the same function's answer) promises a person who chooses it.
 */
function streamOrder(document: PlanStatusView): readonly string[] {
  const names: string[] = []
  for (const stream of document.streams) if (!names.includes(stream.name)) names.push(stream.name)
  for (const plan of document.plans) {
    if (plan.stream !== '' && !names.includes(plan.stream)) names.push(plan.stream)
  }
  if (document.plans.some((plan) => plan.stream === '')) names.push('')
  return names
}

/** streamLabel is what a column and a tab are called. A stream's `name` is a
 * string a checkout wrote, so it goes through the `stream` class; the
 * no-stream column is the console's own two words. */
function streamLabel(document: PlanStatusView, stream: string): Sanitized {
  if (stream === '') return own('no stream')
  const at = document.streams.findIndex((candidate) => candidate.name === stream)
  if (at >= 0) return sanitizeText(stream, classOf('stream'), `streams[${String(at)}].name`)
  // A stream only a plan names: the plan's own `stream` field is the only place
  // the value was read from, so it is the field a refusal has to name.
  const plan = document.plans.findIndex((candidate) => candidate.stream === stream)
  const where = plan >= 0 ? `plans[${String(plan)}].stream` : 'streams'
  return sanitizeText(stream, classOf('stream'), where)
}

/**
 * graphPlan is one plan's picture. Nodes are dropped first -- a plan past
 * `GRAPH_MAX_NODES_PER_PLAN` or past what is left of `GRAPH_MAX_NODES` -- and
 * an edge is kept only when **both** of its ends survived, so the picture never
 * shows an arrow leaving into nothing.
 *
 * An edge whose needed task this read did not carry (a closed task, without
 * `--all`) is counted in `notShown` like a dropped one: that node is not on
 * this picture either, and the count answers "how much of the graph am I not
 * looking at".
 */
function graphPlan(plan: PlanView, index: number, room: number): { plan: GraphPlan; chips: GraphRelease[] } {
  const where = `plans[${String(index)}]`
  let notShown = 0
  // The document's own index is carried beside the node, so a refusal names the
  // field the value was read from rather than the place it ended up drawn in.
  const kept: { readonly node: PlanNodeView; readonly at: number }[] = []
  for (const [at, node] of plan.nodes.entries()) {
    if (kept.length >= GRAPH_MAX_NODES_PER_PLAN || kept.length >= room) {
      notShown += 1
      continue
    }
    kept.push({ node, at })
  }
  // The level first and the id second: the id breaks every tie, so two reads of
  // one document put their nodes in one order (C8).
  kept.sort((left, right) => left.node.level - right.node.level || compare(left.node.id, right.node.id))

  const nodes = kept.map(({ node, at }) => graphNode(node, where, at))
  const byId = new Map(kept.map(({ node }) => [node.id, node]))
  const edges: GraphEdge[] = []
  for (const { node } of kept) {
    for (const [at, id] of node.needs.entries()) {
      if (at >= GRAPH_MAX_NEEDS) {
        notShown += 1
        continue
      }
      const from = byId.get(id)
      if (from === undefined) {
        notShown += 1
        continue
      }
      edges.push({
        from: id,
        to: node.id,
        crossRelease: from.release !== node.release,
        critical: from.critical && node.critical && node.level === from.level + 1,
      })
    }
  }
  edges.sort((left, right) => compare(left.from, right.from) || compare(left.to, right.to))

  const releases: GraphRelease[] = []
  const chips: GraphRelease[] = []
  for (const [at, release] of plan.releases.entries()) {
    const summary = graphRelease(release, plan, where, at)
    if (release.finished) chips.push(summary)
    else releases.push(summary)
  }

  return {
    plan: {
      id: plan.id,
      title: sanitizeText(plan.plan, classOf('plan'), `${where}.plan`),
      stream: plan.stream,
      releases,
      nodes,
      edges,
      notShown,
    },
    chips,
  }
}

function graphNode(node: PlanNodeView, where: string, at: number): GraphNode {
  const field = `${where}.nodes[${String(at)}]`
  return {
    id: node.id,
    title: cutTitle(sanitizeText(node.title, classOf('title'), `${field}.title`)),
    words: stateWords(node),
    mark: sanitizeText(node.mark, classOf('mark'), `${field}.mark`),
    stage: sanitizeText(node.stage, classOf('stage'), `${field}.stage`),
    run: runSuffix(node.run, `${field}.run`),
    prNumber: node.prNumber,
    critical: node.critical,
    release: node.release,
    needs: node.needs.slice(0, GRAPH_MAX_NEEDS),
  }
}

/** runSuffix is the node's four-character run suffix, held to its shape and
 * then read as a `label` (the file header says why not the `runId` class). An
 * absent `run` is the decoder's empty string, which says the task has no run --
 * the `label` class draws an empty label as itself, exactly as the `sentence`
 * class draws an empty sentence. */
function runSuffix(run: string, field: string): Sanitized {
  if (run !== '' && !RUN_SUFFIX_RE.test(run)) return suffixRefusal(field)
  return sanitizeText(run, SUFFIX_CLASS, field)
}

/** cutTitle is the second cut: `sanitize.ts` cut the sentence at 512
 * characters, and this one takes it to the card's 200 **code points** --
 * `[...text]` iterates code points, so a surrogate pair is never split into the
 * two lone halves a font draws as a box. The cut is marked. */
function cutTitle(title: Sanitized): Sanitized {
  if (!title.ok) return title
  const points = [...title.text]
  if (points.length <= GRAPH_MAX_TITLE_CODE_POINTS) return title
  return { ok: true, text: `${points.slice(0, GRAPH_MAX_TITLE_CODE_POINTS).join('')}${CUT_MARK}`, flags: title.flags }
}

function graphRelease(release: PlanReleaseView, plan: PlanView, where: string, at: number): GraphRelease {
  const field = `${where}.releases[${String(at)}]`
  return {
    plan: plan.id,
    stream: plan.stream,
    version: release.version,
    heading: {
      stream: sanitizeText(release.stream, classOf('stream'), `${field}.stream`),
      name: sanitizeText(release.name, classOf('name'), `${field}.name`),
      version: sanitizeText(release.version, classOf('version'), `${field}.version`),
    },
    done: release.done,
    total: release.total,
  }
}

/** chipGroups is the finished releases, grouped by stream in the tab order and
 * ordered inside a group by version and then by plan -- two plans of one
 * checkout can name the same version, and the plan breaks that tie (C8). */
function chipGroups(
  document: PlanStatusView,
  streams: readonly string[],
  chips: readonly GraphRelease[],
): readonly GraphChipGroup[] {
  const groups: GraphChipGroup[] = []
  for (const stream of streams) {
    const ofStream = chips.filter((chip) => chip.stream === stream)
    if (ofStream.length === 0) continue
    const ordered = [...ofStream].sort(
      (left, right) => compare(left.version, right.version) || compare(left.plan, right.plan),
    )
    groups.push({ stream, label: streamLabel(document, stream), chips: ordered })
  }
  return groups
}

/** compare is a total order on two strings. `localeCompare` is not one: it
 * depends on the process's locale, and two machines must break a tie the same
 * way for two reads to draw one graph. */
function compare(left: string, right: string): number {
  return left < right ? -1 : left > right ? 1 : 0
}
