// The console's own states, for the four conditions design section 6.3 lists:
// a project with no Firebase budget backend, a feed that has gone quiet, no
// `fugaro` at all, and a read the CLI refused (desktop-console plan Task 3.1).
//
// **This file is not a fifth screen.** The design gives Fugaro four screens
// (section 6.1, "what we do not take"), and this is the other half of the same
// sentence: the states in which the console has **no dashboard to draw**. Two
// of them are documents -- the CLI's own degraded shape, and the plain sentence
// a read was refused with -- and two are the console's own facts (the feed is
// quiet; the binary is not there). All four are drawn here as one screen each,
// and the rule they share is design section 6.3's: **a screen with nothing to
// measure draws that fact and nothing else.** There is no partial dashboard, no
// cap bar at zero, no empty table standing in for a project the console cannot
// read. `degraded.spec.ts` asserts exactly that: `.live` is not in the document
// on the absent-binary and refused-read screens, and a cap the console cannot
// read is `n/a` rather than `$0.00`.
//
// Where the numbers come from, and what is deliberately missing:
//
//   - the degraded document carries `runview.Totals` and `[]runview.Row` and
//     **no** `connection`, **no** caps, **no** burn and **no** kill state
//     (contract section 10.8). So this screen draws the run records and their
//     sums, marks the subscription rows' model dollars notional, and says the
//     caps are not readable here rather than drawing them as zero. The kill and
//     resume affordances are absent -- not disabled, absent -- because a
//     project the CLI cannot read caps for is a project the console must not
//     offer to act on (design section 6.3, "the kill/resume affordances
//     absent").
//   - a refused read is drawn with **the CLI's own sentence, character for
//     character** (`SourceRefusal.sentence`, `src/source/source.ts`). The
//     remedy is inside it: `internal/cli/report.go`'s `firestoreErr` names
//     `roles/datastore.viewer` and `roles/serviceusage.serviceUsageConsumer`
//     in the sentence itself, which is why nothing here adds a second line
//     about roles -- a console that restated the fix could restate it wrongly,
//     and the operator would have two sentences to reconcile.
//   - the absent-binary screen is the console's own, built by
//     `src/source/cli.ts`'s `noInstallationState` from the resolution that
//     failed, and it is deliberately the *only* thing on screen: what is
//     needed, the install command, the picker, and how many places were
//     searched -- a count and never the paths themselves, because no path is
//     ever drawn on this console (the `path` class, `sanitize.ts`).
//
// Every string a document or the CLI wrote reaches the window through `putText`
// (and every number through `putReading`); only the console's own vocabulary is
// written directly, through `literal`. `screen-literals.test.ts` scans this file
// for that, and `degraded.spec.ts` draws it under the console's real CSP.

import { measured, unavailable } from '../../shared/availability'
import type { Reading } from '../../shared/availability'
import { sanitizeRefusal, sanitizeText } from '../../shared/sanitize'
import type { CostView, DegradedDocument, RecordView } from '../../shared/schema'
import { putReading, putText } from '../app'
import { feedReading, notionalReading } from './live'
import type { FeedHealth, LiveTheme, ScreenElement, ScreenFactory } from './live'

/** DEGRADED_THEMES is the console's theme tuple, restated for this screen's
 * callers: the degraded screens are drawn once per theme exactly as the four
 * screens are, and a spec that took a golden per theme needs the list here too.
 * It is the same two words as `LIVE_THEMES` and deliberately not a third
 * palette (`src/renderer/theme/tokens.css` is the plan's, in no task's Files). */
export const DEGRADED_THEMES = Object.freeze(['light', 'dark'] as const)

/** NO_CAPS_BECAUSE is why a project with no budget backend has no cap line. It
 * is the console's sentence about the *shape*, not a document's string: the
 * degraded document has no cap field at all (contract section 10.8), so there
 * is nothing to read and nothing to draw as zero. */
export const NO_CAPS_BECAUSE =
  'this project has no budget database, so there is no cap to read here: the CLI reads run records instead'

/** NO_COMPUTE_BECAUSE is why a row's compute is not a number: `runstore.Cost`
 * writes `compute_estimated: false` with `compute_usd: 0`, and the zero means
 * "not estimated", never "free" (`internal/runstore/cost.go`). */
export const NO_COMPUTE_BECAUSE =
  'this run did not have its compute estimated, so the zero on the wire is not a price'

/** KILL_ABSENT_BECAUSE is the one line the caps panel says about the switch it
 * does not draw. */
export const KILL_ABSENT_BECAUSE =
  'no kill or resume control is offered: the console will not act on a project it cannot read caps for'

/** COMPUTE_LOWER_BOUND_NOTE is what a total built from rows whose compute is
 * unknown is: a lower bound, marked `>=` on the figure itself. */
export const COMPUTE_LOWER_BOUND_NOTE =
  'runs whose compute was not estimated count as unknown, not as zero, so these figures are lower bounds'

/** NO_BINARY_HEADLINE, and the three that follow it, are this screen's own
 * words -- the console's vocabulary, never a document's. */
export const NO_BINARY_HEADLINE = 'no fugaro binary'
/** INSTALL_LEAD is the sentence the install command is offered under. */
export const INSTALL_LEAD = 'install it with'
/** PICKER_LEAD is the affordance design section 6.3 asks for: the operator can
 * point the console at another binary, and this is where that starts. The
 * screen draws it and wires nothing -- the installations list is a task this
 * one does not own, and an invented control that did nothing would be worse
 * than a sentence that says what it is for. */
export const PICKER_LEAD = 'choose a different fugaro binary'
/** SEARCHED_LEAD says what the number beside it counts. */
export const SEARCHED_LEAD = 'places the console looked in for a fugaro binary'
/** REFUSED_HEADLINE is the refused-read screen's heading. */
export const REFUSED_HEADLINE = 'the CLI refused this read'

// ---------------------------------------------------------------------------
// The no-Firebase model.
// ---------------------------------------------------------------------------

/** DegradedRunRow is one `runview.Row` of the degraded document, as this screen
 * draws it. */
export interface DegradedRunRow {
  readonly run: string
  readonly runId: string
  readonly repo: string
  readonly status: string
  readonly stage: string
  readonly branch: string
  readonly outcome: string
  readonly reason: string
  readonly account: string
  readonly created: string
  readonly modelUsd: Reading
  readonly computeUsd: Reading
  readonly totalUsd: Reading
  /** the row's model figure has a subscription's list price in it, wholly or
   * partly (`model_notional_usd`, C1.16/P12; the basis is the fallback for a
   * row written before the key): the figure drawn is the **billed** part, and
   * the row is marked so that a reader never counts the plan's part as billed. */
  readonly notionalModel: boolean
}

/** DegradedTotals is `runview.Totals`, with the two figures that are lower
 * bounds marked as such. */
export interface DegradedTotals {
  readonly runs: Reading
  readonly modelUsd: Reading
  readonly modelNotionalUsd: Reading
  readonly computeUsd: Reading
  readonly totalUsd: Reading
  readonly computeNotEstimated: Reading
}

/** NoFirebaseModel is the whole screen as data: a project the CLI reads run
 * records for, and the three things this screen must not invent. */
export interface NoFirebaseModel {
  readonly kind: 'no-firebase'
  readonly project: string
  /** the CLI's own notice for this mode, drawn as a sentence */
  readonly notice: string
  /** every non-JSON line the feed carried, in order: contract section 10.8
   * requires the reader to keep them, and a header warning is where they go */
  readonly notices: readonly string[]
  /** the document's own `warnings[]` (`runs ls --json`'s imageWarnings: "a
   * failed image rebuild runs a stale image"), job-written text the display
   * layer re-sanitizes (contract section 9.4) -- the actionable lines, and the
   * ones the three sibling screens already draw (review round 2 of PR 494) */
  readonly warnings: readonly string[]
  readonly totals: DegradedTotals
  readonly runs: readonly DegradedRunRow[]
  /** the cap line, always unavailable on this shape */
  readonly caps: Reading
  /** `false`, and not an optional field: there is no cap state to draw */
  readonly capsKnown: false
  /** `false`: the switch is absent from this screen, not drawn disabled */
  readonly killable: false
  readonly feed: FeedHealth | null
}

/** computeOf is the row's compute figure, held to the same rules `projects.ts`
 * and `run.ts` state on the same field (design section 6.1 rule 1: compute is
 * "an estimate by design", so it carries the `est.` mark whenever the document
 * says it was estimated). Review round 5 of PR 478: this screen used to draw a
 * row whose `compute_estimated` was true as a plain measured figure -- `$0.90`
 * where the other two screens draw `est. $0.90` -- and to hide a non-zero
 * figure whose flag was off, which is not a shape the CLI writes and which the
 * other two screens draw as the number it is.
 *
 * A figure with the flag off is the case the flag exists for: `runstore.Cost`
 * writes `compute_estimated: false` with `compute_usd: 0`, and the zero means
 * "not estimated", never "free" (`internal/runstore/cost.go`). */
function computeOf(cost: CostView): Reading {
  if (cost.computeEstimated) {
    return cost.computeUsd.value === null ? cost.computeUsd : { ...cost.computeUsd, state: 'estimated' }
  }
  if (cost.computeUsd.value === null || cost.computeUsd.value === 0) return unavailable(NO_COMPUTE_BECAUSE)
  return cost.computeUsd
}

/** modelReading is this screen's model figure: the row's model dollars as
 * **money** (C1.16, P12/D140). `model_usd` is the whole model figure -- the
 * money and the subscription's list price together -- and `model_notional_usd`
 * is the part of it the plan paid, so the billed dollars are the difference.
 * The basis alone cannot make that split: a mixed cascade run's basis is
 * `api-list` for the whole run while part of its figure is the plan's, which is
 * what this screen used to draw as billed money.
 *
 * A row written before C1.16 carries no `model_notional_usd`, and then the
 * subscription basis is the fallback: such a row's whole figure is the plan's.
 * The state follows the figure: the money part when the row has one, and
 * otherwise the notional figure marked notional. */
function modelReading(cost: CostView): Reading {
  const model = cost.modelUsd
  if (model.value === null) return model
  const plan = cost.modelNotionalUsd.value ?? (cost.modelBasis === 'subscription' ? model.value : 0)
  const money = Math.max(0, model.value - plan)
  if (money > 0) return { ...model, value: money }
  if (plan > 0 || cost.modelBasis === 'subscription') return { ...model, value: plan, state: 'notional' }
  return model
}

/** notionalPartOf says whether the row's model figure has the plan's list price
 * in it at all -- wholly or, for a cascade run's mixed stage, partly. A reader
 * marking rows so that a notional figure never lands in a billed sum asks this,
 * not the basis: a mixed run's basis is `api-list` (`runstore.BasisAPIList`)
 * while part of its figure is the subscription's. */
function notionalPartOf(cost: CostView): boolean {
  if (cost.modelNotionalUsd.value !== null) return cost.modelNotionalUsd.value > 0
  return cost.modelBasis === 'subscription'
}

/** costReadings is one row's three dollar accounts, and whether the model
 * figure it draws has a notional part (see `modelReading`, `notionalPartOf`). */
function costOf(cost: CostView): {
  readonly modelUsd: Reading
  readonly computeUsd: Reading
  readonly totalUsd: Reading
  readonly notionalModel: boolean
} {
  return {
    modelUsd: modelReading(cost),
    computeUsd: computeOf(cost),
    totalUsd: cost.totalUsd,
    notionalModel: notionalPartOf(cost),
  }
}

/** rowOf is one record as this screen draws it. */
function rowOf(record: RecordView): DegradedRunRow {
  const cost = costOf(record.cost)
  return {
    run: record.run,
    runId: record.runId === '' ? record.run : record.runId,
    repo: record.repo,
    status: record.status,
    stage: record.stage,
    branch: record.branch,
    outcome: record.outcome,
    reason: record.reason,
    account: record.oauthAccount,
    created: record.created,
    modelUsd: cost.modelUsd,
    computeUsd: cost.computeUsd,
    totalUsd: cost.totalUsd,
    notionalModel: cost.notionalModel,
  }
}

/**
 * buildNoFirebaseModel turns a decoded `DegradedDocument` into the screen's
 * data. `notices` is the console's own record of the lines that were not JSON
 * (`Snapshot.notices`, contract section 10.8) and `feed` is the console's
 * silence reading, when the caller keeps one.
 *
 * The two lower bounds are the one piece of arithmetic here, and it is
 * `Totals.ComputeNotEstimated` doing it: a project where every run priced its
 * compute reads exactly as it always did, and a project where one did not says
 * so on the figure rather than in a footnote nobody reads.
 *
 * Review round 5 of PR 478: `compute_not_estimated` is a `Reading` like every
 * other figure, and this used to read `?? 0` off it -- so a document whose count
 * was *unavailable* drew `$9.40` and `$0.90` unmarked beside a note that still
 * promised lower bounds, which is the skew case (Task 3.2) the module exists to
 * survive. The rule now is the count's own value: it is `0` and only `0` that
 * makes a total exact, because `null` means the console does not know whether
 * any run's compute went unpriced. An unknown count is the count drawn `n/a`
 * (the reading is passed through untouched) with the figures still marked as
 * lower bounds, which is the weaker and therefore the safe claim.
 */
export function buildNoFirebaseModel(
  document: DegradedDocument,
  notices: readonly { readonly text: string }[] = [],
  feed: FeedHealth | null = null,
): NoFirebaseModel {
  const bounded = document.totals.computeNotEstimated.value !== 0
  return {
    kind: 'no-firebase',
    project: document.project,
    notice: document.notice,
    notices: notices.map((one) => one.text),
    warnings: document.warnings,
    totals: {
      runs: document.totals.runs,
      modelUsd: document.totals.modelUsd,
      modelNotionalUsd: notionalReading(document.totals.modelNotionalUsd),
      computeUsd: bounded ? { ...document.totals.computeUsd, lowerBound: true } : document.totals.computeUsd,
      totalUsd: bounded ? { ...document.totals.totalUsd, lowerBound: true } : document.totals.totalUsd,
      computeNotEstimated: document.totals.computeNotEstimated,
    },
    runs: document.runs.map(rowOf),
    caps: unavailable(NO_CAPS_BECAUSE),
    capsKnown: false,
    killable: false,
    feed,
  }
}

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

// The four helpers below are this screen's own copies of the four every screen
// declares (`live.ts`, `run.ts`, `projects.ts`, `spend.ts`): `ScreenElement`
// and `ScreenFactory` live in `renderer/app.ts`, which is Task 2.0's file and
// not this task's to edit, and the screens are siblings of one another rather
// than of the app.

/** make is a fresh element with its classes. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  return element
}

/** literal writes the console's own vocabulary into an element. Nothing a job
 * wrote reaches this function, and every argument it is called with here is a
 * string literal or a named constant of this file. */
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

/** textCell is `cell` with a document's string written into it through
 * `putText`, with the class the wire key declares. */
function textCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  raw: unknown,
  textClass: Parameters<typeof putText>[2],
  field: string,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, ...classes)
  putText(element, raw, textClass, field)
  return element
}

/** numberCell is `cell` with a reading written through `putReading`, so a number
 * cannot be drawn without its state. */
function numberCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  reading: Reading,
  unit: Parameters<typeof putReading>[2],
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, ...classes)
  putReading(element, reading, unit)
  return element
}

/** sanitizedKey is a document string used as an attribute value rather than as
 * text -- a run id, which is the identity a later screen needs. A refusal
 * yields the empty string: an attribute is not a place to draw `n/a`. */
function sanitizedKey(raw: string): string {
  const result = sanitizeText(raw, 'runId', 'run')
  return result.ok ? result.text : ''
}

/** root is the section both of this file's states are drawn into: the theme,
 * the discriminant a stylesheet and a test key off, and the arrival stamp when
 * the caller keeps one. One helper and not two, because the two roots differ in
 * one class and nothing else -- and because the theme write belongs in exactly
 * one place for `screen-literals.test.ts` to hold. */
function root(
  factory: ScreenFactory,
  kind: 'no-firebase' | UnavailableState['kind'],
  theme: LiveTheme,
  receivedAt: string | null,
): ScreenElement {
  const element = make(factory, 'section', kind === 'no-firebase' ? 'degraded' : 'console-unavailable')
  element.setAttribute('data-theme', theme)
  element.setAttribute('data-kind', kind)
  element.setAttribute('data-received-at', receivedAt === null ? '' : receivedAt)
  return element
}

/** feedLine is the "last read" stamp, drawn whenever the caller keeps a feed
 * reading: it is the timestamp design section 6.3 asks the last-known values to
 * keep. */
function feedLine(parent: ScreenElement, factory: ScreenFactory, feed: FeedHealth): void {
  const line = cell(parent, factory, 'p', 'degraded-since')
  literal(cell(line, factory, 'span', 'degraded-since-word'), 'last read')
  textCell(line, factory, 'span', feed.since, 'label', 'feed.since', 'degraded-since-at')
  numberCell(line, factory, 'span', feedReading(feed), 'seconds', 'degraded-since-age')
}

/** header draws the project, the mode word and the CLI's own notice. */
function header(factory: ScreenFactory, model: NoFirebaseModel): ScreenElement {
  const element = make(factory, 'header', 'degraded-header')
  const heading = cell(element, factory, 'h1', 'degraded-project')
  putText(heading, model.project, 'project', 'project')
  // The connectivity word is the console's own here: the degraded document has
  // no `connection` at all, so there is no document word to draw. `polling` is
  // design section 6.2's word for exactly this mode.
  const mode = literal(cell(element, factory, 'span', 'degraded-mode'), 'polling')
  mode.setAttribute('data-state', 'polling')
  textCell(element, factory, 'span', 'run records only', 'label', 'the degraded mode', 'degraded-mode-word')
  textCell(element, factory, 'p', model.notice, 'sentence', 'notice', 'degraded-notice')
  for (const notice of model.notices) {
    textCell(element, factory, 'p', notice, 'sentence', 'a line that was not JSON', 'degraded-warning')
  }
  for (const [index, warning] of model.warnings.entries()) {
    textCell(element, factory, 'p', warning, 'sentence', `warnings[${String(index)}]`, 'degraded-document-warning')
  }
  if (model.feed !== null) feedLine(element, factory, model.feed)
  return element
}

/** caps draws the cap line the console cannot read: `n/a`, never zero, and no
 * switch beside it. */
function caps(factory: ScreenFactory, model: NoFirebaseModel): ScreenElement {
  const element = make(factory, 'div', 'degraded-caps')
  // `data-kill-capable` is `false` on this shape, and the attribute is what a
  // test reads: the affordance is absent (no element to press) and the panel
  // says so, rather than drawing a disabled control a later edit could enable.
  element.setAttribute('data-kill-capable', model.killable ? 'true' : 'false')
  const cap = cell(element, factory, 'span', 'degraded-cap')
  putReading(cap, model.caps, 'usd')
  cap.setAttribute('data-state', model.caps.state)
  literal(cell(element, factory, 'span', 'degraded-caps-word'), KILL_ABSENT_BECAUSE)
  return element
}

/** totals draws the three accounts kept apart, and the count that makes two of
 * them lower bounds. The note and the `>=` marks are drawn together or not at
 * all: a count of zero is the one document that makes these sums exact, and a
 * note promising lower bounds beside unmarked figures is the mismatch review
 * round 5 of PR 478 found. */
function totals(factory: ScreenFactory, model: NoFirebaseModel): ScreenElement {
  const element = make(factory, 'div', 'degraded-totals')
  numberCell(element, factory, 'span', model.totals.runs, 'count', 'degraded-runs')
  numberCell(element, factory, 'span', model.totals.modelUsd, 'usd', 'degraded-model-usd')
  numberCell(element, factory, 'span', model.totals.modelNotionalUsd, 'usd', 'degraded-model-notional-usd')
  numberCell(element, factory, 'span', model.totals.computeUsd, 'usd', 'degraded-compute-usd')
  numberCell(element, factory, 'span', model.totals.totalUsd, 'usd', 'degraded-total-usd')
  numberCell(element, factory, 'span', model.totals.computeNotEstimated, 'count', 'degraded-compute-unknown')
  if (model.totals.totalUsd.lowerBound) {
    literal(cell(element, factory, 'span', 'degraded-compute-word'), COMPUTE_LOWER_BOUND_NOTE)
  }
  return element
}

/** runRow draws one run record. */
function runRow(factory: ScreenFactory, row: DegradedRunRow): ScreenElement {
  const element = make(factory, 'li', 'degraded-run')
  element.setAttribute('data-run', sanitizedKey(row.run))
  element.setAttribute('data-notional-model', row.notionalModel ? 'true' : 'false')
  textCell(element, factory, 'span', row.runId, 'runId', 'runs[].run_id', 'degraded-run-id')
  textCell(element, factory, 'span', row.repo, 'repository', 'runs[].repo', 'degraded-run-repo')
  textCell(element, factory, 'span', row.status, 'label', 'runs[].status', 'degraded-run-status')
  textCell(element, factory, 'span', row.stage, 'label', 'runs[].stage', 'degraded-run-stage')
  textCell(element, factory, 'span', row.outcome, 'label', 'runs[].outcome', 'degraded-run-outcome')
  textCell(element, factory, 'span', row.branch, 'branch', 'runs[].branch', 'degraded-run-branch')
  textCell(element, factory, 'span', row.reason, 'sentence', 'runs[].reason', 'degraded-run-reason')
  textCell(element, factory, 'span', row.account, 'person', 'runs[].oauth_account', 'degraded-run-account')
  textCell(element, factory, 'span', row.created, 'label', 'runs[].created', 'degraded-run-created')
  numberCell(element, factory, 'span', row.modelUsd, 'usd', 'degraded-run-model')
  numberCell(element, factory, 'span', row.computeUsd, 'usd', 'degraded-run-compute')
  numberCell(element, factory, 'span', row.totalUsd, 'usd', 'degraded-run-total')
  return element
}

/**
 * renderNoFirebase draws the degraded screen: the project, the CLI's notice,
 * the run records, their sums with the two lower bounds marked, and a cap line
 * that says `n/a` because there is nothing to read.
 */
export function renderNoFirebase(
  factory: ScreenFactory,
  model: NoFirebaseModel,
  theme: LiveTheme,
): ScreenElement {
  const section = root(factory, model.kind, theme, model.feed === null ? null : model.feed.since)
  section.append(header(factory, model))
  section.append(caps(factory, model))
  section.append(totals(factory, model))
  const runs = cell(section, factory, 'ol', 'degraded-runs')
  for (const row of model.runs) runs.append(runRow(factory, row))
  return section
}

// ---------------------------------------------------------------------------
// The console's two states with no source at all.
// ---------------------------------------------------------------------------

/** NoBinaryState is `src/source/cli.ts`'s `NoInstallationState` as this screen
 * draws it. It is stated here rather than imported because the renderer may not
 * import a module that spawns: the *shape* is the contract, and
 * `degraded.spec.ts` assigns one to the other so a drift fails the typecheck. */
export interface NoBinaryState {
  readonly kind: 'no-binary'
  readonly reason: string
  readonly installCommand: string
  readonly searched: readonly string[]
  readonly configuredPath: string | null
}

/** RefusedState is a read the CLI refused: which read, and the CLI's sentence
 * **verbatim** -- including the remedy, which `internal/cli/report.go`'s
 * `firestoreErr` writes into that same sentence. */
export interface RefusedState {
  readonly kind: 'refused'
  readonly operation: string
  readonly sentence: string
}

/** UnavailableState is either of them. */
export type UnavailableState = NoBinaryState | RefusedState

/** REFUSAL_UNREADABLE is what the refused-read screen says when the CLI's own
 * sentence cannot be drawn at all (`sanitizeRefusal` refuses it: a sentence
 * longer than the console reads, or one that is not text). The console still
 * names the read above it, so the screen is not empty -- the operator is owed
 * the fact that there was a refusal, and that its wording could not be shown. */
export const REFUSAL_UNREADABLE =
  'the CLI refused this read, and its own sentence is longer than the console will draw'

/**
 * refusedState builds the refused screen's data from a `SourceRefusal`. This is
 * the one place the CLI's text is carried, so a later edit that trimmed or
 * reworded it would be visible in one diff -- and review round 5 of PR 478 is
 * why what happens here is `sanitizeRefusal` rather than nothing at all.
 *
 * The screen used to draw the sentence through a raw `literal`, which was right
 * about the remedy (design section 6.3: "the console shows that text unedited")
 * and wrong about the door: the Projects panel drew the same channel's text
 * through `putText`, so one input had two treatments, and neither was right.
 * `sanitizeRefusal` is the single treatment -- the credential rules and this
 * layer's own, never the pattern classes that eat `roles/datastore.viewer` --
 * so the remedy survives *and* a token a child echoed into its stderr is still
 * redacted. Sanitizing here, in the model, is what keeps `refused()`'s `literal`
 * call honest: by the time the drawing reads `state.sentence`, it is a string
 * the sanitizer produced.
 */
export function refusedState(operation: string, refusal: { readonly sentence: string }): RefusedState {
  const drawn = sanitizeRefusal(refusal.sentence, 'the CLI refusal sentence')
  return { kind: 'refused', operation, sentence: drawn.ok ? drawn.text : REFUSAL_UNREADABLE }
}

/** picker draws the binary-picker affordance: what it is for, and the path the
 * installation named when it named one. */
function picker(parent: ScreenElement, factory: ScreenFactory, state: NoBinaryState): void {
  const element = cell(parent, factory, 'div', 'unavailable-picker')
  element.setAttribute('data-picker', 'binary')
  literal(cell(element, factory, 'span', 'unavailable-picker-lead'), PICKER_LEAD)
  if (state.configuredPath !== null) {
    textCell(element, factory, 'span', state.configuredPath, 'label', 'configured binary path', 'unavailable-configured')
  }
}

/**
 * searched says **how many** places the resolution walked, and deliberately not
 * which ones.
 *
 * The first draft of this screen listed the candidates, and the `path` class is
 * why it cannot: `sanitize.ts` draws every path as the `<path>` placeholder
 * whatever it looks like -- "a path is where the operator's user name and
 * checkout live, and neither belongs in a screenshot" -- so a list of them would
 * be a column of `<path>` under a sentence saying the console looked somewhere,
 * and a list read as prose would be a column of `[redacted]` instead. The count
 * is the fact that survives that rule, and it is the whole of what the operator
 * needs from it: the console looked in the usual places and found no `fugaro`
 * there, so the answer is the install command or the picker above, and the paths
 * themselves are in the spawn log where the resolution wrote them.
 *
 * The number goes through `putReading` like every other number -- a count this
 * file computed, drawn `measured` -- and `SEARCHED_LEAD` is this file's own
 * sentence.
 */
function searched(parent: ScreenElement, factory: ScreenFactory, state: NoBinaryState): void {
  const element = cell(parent, factory, 'div', 'unavailable-searched')
  numberCell(element, factory, 'span', measured(state.searched.length), 'count', 'unavailable-searched-count')
  literal(cell(element, factory, 'span', 'unavailable-searched-lead'), SEARCHED_LEAD)
}

/** noBinary draws the absent-binary screen: what is needed, the install command
 * and the picker, and nothing else. */
function noBinary(factory: ScreenFactory, state: NoBinaryState): ScreenElement {
  const element = make(factory, 'div', 'unavailable-body')
  literal(cell(element, factory, 'h1', 'unavailable-headline'), NO_BINARY_HEADLINE)
  // The resolution's sentence is the CLI's shape of "where I looked", written by
  // `spawn.ts` rather than by a document: it goes through `putText` like every
  // other string this screen did not author itself. As prose it is read as
  // prose, so the path fragments inside the "looked at ..." form are redacted
  // (`ABS_PATH_RE`) -- deliberately, and without loss: the resolution already
  // says how many places it looked in, and `searched` below draws that count
  // rather than the paths, so no place is lost with the redaction.
  //
  // Review round 5 of PR 478: this comment used to end "the list below carries
  // the places, each one drawn as the path it is", which stopped being true when
  // the list became a count.
  textCell(element, factory, 'p', state.reason, 'sentence', 'the resolution reason', 'unavailable-reason')
  const install = cell(element, factory, 'div', 'unavailable-install')
  literal(cell(install, factory, 'span', 'unavailable-install-lead'), INSTALL_LEAD)
  // The install command is this file's sibling `cli.ts`'s constant, which is
  // the README's own line, so it is the console's vocabulary and not a
  // document's string.
  literal(cell(install, factory, 'code', 'unavailable-install-command'), state.installCommand)
  picker(element, factory, state)
  searched(element, factory, state)
  return element
}

/** drawnOrNothing is a console label after `sanitizeRefusal`, or the empty
 * string when that refuses it. */
function drawnOrNothing(label: string): string {
  const drawn = sanitizeRefusal(label, 'the operation label')
  return drawn.ok ? drawn.text : ''
}

/** refused draws the refused-read screen: the console names the read, and the
 * CLI's own sentence is the whole of the rest of it. */
function refused(factory: ScreenFactory, state: RefusedState): ScreenElement {
  const element = make(factory, 'div', 'unavailable-body')
  literal(cell(element, factory, 'h1', 'unavailable-headline'), REFUSED_HEADLINE)
  // The operation is the console's own label of what it asked for
  // (`OperationName`), never a document's string.
  //
  // `RefusedState` is a plain exported type, so a caller can build one without
  // `refusedState`: both strings are therefore passed through `sanitizeRefusal`
  // again here (it is idempotent over its own output), and a value it refuses is
  // never drawn.
  literal(cell(element, factory, 'span', 'unavailable-operation'), drawnOrNothing(state.operation))
  // And this is the CLI's sentence, already through `sanitizeRefusal` in
  // `refusedState` -- which is what makes a `literal` call honest here, since
  // the value is no longer a string this screen did not write. The reasoning is
  // worth stating because it looks like the defect Task 2.3's review found four
  // times (a raw string drawn straight into a cell):
  //
  //   - **This value is not a document's string.** Task 2.0's rule ("every
  //     string from a CLI document reaches the renderer only through the
  //     sanitizer") is about the documents; this is the child's *stderr*,
  //     delivered on the `SourceRefusal` channel (`src/source/source.ts`).
  //   - **`putText` (the `sentence` class) would destroy the sentence.** Design
  //     section 6.3 requires the CLI's remedy on screen, and `FREE_TEXT_RULES`
  //     eats it: `roles/datastore.viewer` is two long segments around a slash,
  //     so `REPO_TOKEN_RE` draws it `[redacted]`, and `project sandbox-db` is a
  //     GCP project named in a sentence, so `GCP_CONTEXT_RE` draws it
  //     `<project>`. `degraded.test.ts` asserts both halves -- the sentence
  //     unchanged through `sanitizeRefusal`, and changed by `putText` -- so the
  //     reason cannot rot into a claim nobody checks.
  //   - **The screen is the operator's only copy of it.** `report.go`'s
  //     `firestoreErr` is the one place the two role names are written, and a
  //     screen that redacted them would leave the operator with a refusal and no
  //     remedy.
  const sentence = sanitizeRefusal(state.sentence, 'the CLI refusal sentence')
  literal(cell(element, factory, 'p', 'unavailable-sentence'), sentence.ok ? sentence.text : REFUSAL_UNREADABLE)
  return element
}

/**
 * renderUnavailable draws the console's state when there is no source to read:
 * no binary, or a read the CLI refused. Neither draws a dashboard -- there is no
 * cap line, no run list and no `.live` root, because there is nothing measured
 * to put in one (design section 6.3: "a single explanatory screen ... no
 * partially drawn dashboard").
 */
export function renderUnavailable(
  factory: ScreenFactory,
  state: UnavailableState,
  theme: LiveTheme,
): ScreenElement {
  const section = root(factory, state.kind, theme, null)
  section.append(state.kind === 'no-binary' ? noBinary(factory, state) : refused(factory, state))
  return section
}
