// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Spend screen: the design's screen 2, drawn from `report --json` and
// `budget show --json` (desktop-console plan Tasks 2.2 and 2.4; design sections
// 6.1 rule 1, 6.2 screen 2 and 5.1 rule 6; console/docs/json-contract.md
// sections 3 and 4), with the **by-person view** on top of it drawn from
// `runs stats --json` (console L5b; the contract's section 6.10, the design's
// section 5.1 rule 6's thirteenth argv).
//
// The file has two halves, separated the way Task 2.1's Live screen separates
// them: `buildSpendModel` turns the documents into rows, the three series behind
// the one grouped chart, a caps panel and the by-person view, and touches no
// element at all, and `renderSpend` walks that model through Task 2.0's writers.
// The part with the arithmetic in it is testable in plain Node, which is where a
// wrong dollar figure is cheapest to catch.
//
// **Why this file reads its own two documents.** `schema.ts` (Task 2.0) admits
// the *feed*'s two shapes and refuses everything else, which is exactly right
// for `watch --json`: a live document and a `watchDegradedDoc` are the two
// things that command writes, and an unknown shape there is a skew banner, not
// a half-drawn screen. `report --json` and `budget show --json` are two more
// commands with two more documents, and this task's file list holds no
// `schema.ts`, so the readers below are the screen's own. They are **tolerant on
// the two axes the contract names, and closed on neither more nor less**: every
// number enters through `readNumber` -- the one door of `availability.ts`, so a
// `null`, an absent key or a string is an unavailable reading with a sentence
// and never a zero -- and every string is drawn through `putText`, which is the
// one door a document's text comes through (`../app.ts`). What is *not* here is
// a second opinion about a refusal: this screen draws no skew banner, because
// Task 3.2 owns the skew state and a screen that invented its own would be two
// answers to one question.
//
// **The dollars are read from `*_micros`, and the `*_usd` strings are not
// parsed.** The contract (section 4) says the pairs are "exact decimal strings
// of the integer `*_micros` (`USD6`) ... the console parses the string, and
// micros is the integer of record", and the console takes the integer of
// record: `model_micros`, `notional_micros` and `compute_micros` are JSON
// numbers, so they come through `readNumber` like every other number, while
// parsing `model_usd` would be the one thing `readNumber`'s own doc refuses
// ("a string that looks like a number -- parsing it would be trusting the
// document"). The two are the same figure by construction (`USD6` is that
// integer divided by 10^6), the integer needs no parse, and the division is the
// only arithmetic this file does to a dollar.
//
// **The three accounts are never summed, and that is the shape of this file.**
// `m9-budget-and-dashboard.md` section 10 and the design's section 6.2 are one
// rule: model dollars, `~$` (a subscription's list price, never billed,
// never capped) and compute (an estimate by design) are three accounts, and a
// screen that adds them is drawing a number nobody can be billed for. So the
// model carries them as **three readings** (`model`, `notional`, `compute`),
// each in its own availability state, and as **three series of bars grouped side
// by side on one axis** -- `SPEND_ACCOUNTS` is that list, the document's own
// `totals` line is read the same way rather than recomputed, and there is no
// function here that adds one account to another. `spend.test.ts` holds it: a
// mutation that sums them fails a named test rather than a review.
//
// **A partial day is hollow; a day the history does not hold is a gap.** The
// document's `partial` is the design's `(partial)` marker -- the day has no
// final document yet -- and it is drawn hollow with the marker beside it,
// because the figure is real but not yet complete. A day *inside the requested
// range* with no row at all is neither: `report` reads a window of the history
// and the days before it are pruned, so a missing day is a **gap** (the em
// dash), never a row of zeros. Which days those are is the console's own
// arithmetic: `dayRange` validates `since` and `until` as UTC dates, refuses a
// range longer than `reportMaxDays`, and builds the day strings itself, so the
// only thing that reaches a point's `title` through this path is a date this
// file wrote.
//
// **The person breakdown is off by default** (design D15 and section 2.3): the
// report can group by `person`, the document still carries those rows, and the
// screen draws none of them until the operator turns the view on. Even when it
// is on, a person key is drawn through `sanitize.ts`'s `person` class, so an
// address on the wire is the console's own marker (`redacted@example.invalid`)
// and never the address itself.
//
// **The by-person view is `runs stats --json`, and it is fail-private.** The
// knob above turns on a *second read* beside the report (`peopleArguments` and
// `readSpend`'s fourth parameter): `fugaro runs stats --json`, whose `view` is
// the CLI's own ruling about what the caller may see -- an operator of the
// installation gets every user's row, a plain launcher gets their own alone.
// **What this screen adds is the drawing rule that ruling implies, and it draws
// nothing it is not licensed to draw**: rows appear only when the document's own
// `by` and `view` say who they are for, the private view draws the one row the
// CLI keyed by the identity it resolved (`you`), **either view draws no row at
// all** when the document does not say who the caller is (the whole-project view
// keeps its totals, which name nobody), and the totals drawn are the ones
// that read published -- the CLI aggregates *after* it filters for the private
// view (`internal/cli/runs_stats.go`), so under `all` they are the window's and
// under `own` they are the caller's own alone, and the two states where the CLI
// publishes no row publish **no total either** (`userstats.Aggregate` builds
// `totals` out of `rows`). A total is never computed here: it is the CLI's own
// sum of the rows it published, and the panel says which read it is the sum of.
// The console cannot ask for `--all-users`: that flag is not on the operation's
// schema (`src/main/ipc.ts`), so "who may see whose rows" is the CLI's decision
// and never this window's. What the panel *withheld* is a sentence and a count
// on screen -- a reader is owed the difference between "nobody spent anything"
// and "this console will not show you whose money this was".
//
// **The controls are wired, and they are wired to reads on the allow-list --
// on this screen's side of the seam.** The range picker, the breakdown switch
// and the operator's person knob (D15) all feed `SpendControls`, and
// `spendArguments` turns that state into the argument object `spend.report`
// already declares --
// `--since` from the picker (`--until` is accepted by the schema and by
// `spendArguments`, but no control this screen draws can set the end of the
// range yet, so it is always absent and `report` uses its own today), `--by`
// from the switch -- which
// `readSpend` hands to the **existing** `runReport` operation. No new write, no
// new spawn-table row and no new channel: the renderer supplies values, main
// builds the argv (section 5.1 rule 6), and a value outside the screen's own
// closed sets is refused before anything is spawned. A control change is one
// `report --json` read and a re-render from the document that came back; the
// budget document is not re-read, because no control this screen draws is about
// it, and the by-person read happens only while the knob is on, once, with the
// picker's window translated into `parseSince`'s form (`PERSON_WINDOW_DAYS`).
//
// **The caller is `entry.ts`'s `showSpend` (Task 3.4c), and this file decides
// nothing it can decide for it.** `spendArguments`, `readSpend` and `personView`
// are the screen's half -- the arguments a control produces, the reads they make
// and the state they draw -- and the `data-` hooks below (`data-current`,
// `data-enabled`, `data-day`, `data-until`) are what that caller wires: which
// element a click landed on, and when a control change re-reads, are not this
// file's business. It was written before that caller existed and kept whole, so
// the entry point had nothing left to decide about the Spend screen's controls.
//
// **And the person view is off until the operator says otherwise.** `--by
// person` is not a read this screen makes while the knob is off, and the
// document's person rows are not drawn either (see the D15 paragraph above):
// both halves are held by `spend-controls.test.ts` against a default of `on`.
// The knob is per session, and nothing here writes it anywhere -- the console
// never writes to disk (design section 5.1 rule 10).
//
// **What is deliberately not here.** No stylesheet and no colour: the design's
// density rules are markup and CSS, and `src/renderer/theme/tokens.css` is in
// the plan's tree but in no task's Files, so this screen emits what only markup
// can carry -- the theme on the root, the class hooks a stylesheet keys off, and
// `data-` readings for the numbers a bar needs to be a shape. Nothing sets an
// inline style, and nothing sets the `style` attribute at all (`style-src
// 'self'`). No link: this screen offers none, so there is no anchor in it and
// nothing for design section 4.3's gap 7 to be about. No event handler either:
// the picker, the switch and the knob are drawn with the `data-` hooks their
// wiring reads (`data-current`, `data-enabled`, `data-day`, `data-until`), and
// the wiring itself is a pure function of the controls, so which element a click
// lands on is not this file's business. And no refusal panel: `readSpend`
// answers with the console's sentence for a refused read and the *caller* draws
// it, because Task 3.2 owns the skew and refusal states.

import {
  estimated,
  formatReading,
  gap,
  measured,
  notional,
  plottable,
  readNumber,
  unavailable,
} from '../../shared/availability'
import type { Reading } from '../../shared/availability'
import type { KillView } from '../../shared/schema'
import { sanitizeRefusal } from '../../shared/sanitize'
import type { TextClass } from '../../shared/sanitize'
import type { JSONDoc } from '../../source/source'
import { putReading, putText } from '../app'
import type { ElementLike } from '../app'
import { BUSY_SLOT_CLASS } from '../section'

// ---------------------------------------------------------------------------
// The vocabulary.
// ---------------------------------------------------------------------------

/**
 * SPEND_DIMENSIONS is `report --by`'s closed set of eight words, in
 * `ee/report/report.go`'s own order (`ByDay`, `ByWeek`, `ByMonth`,
 * `ByYear`, `ByRepo`, `ByModel`, `ByPerson`, `ByMode`, and `ParseDim`'s list).
 * The switch draws this list; a document's `by` that is not one of them is a
 * document from a CLI this build does not know, and the screen says so rather
 * than drawing a ninth word.
 *
 * `mode` groups by the rigor mode each run resolved, read from the run records
 * rather than from the spend history, so its rows are mode words (`poc`,
 * `startup`, `pro`, `critical`, `hotfix`) or the CLI's own `none` for a run
 * whose record carries no mode -- and `none` is drawn as its own label, not as
 * an error. The money grammar is unchanged: model, notional and compute stay
 * three columns and are never summed, and this breakdown carries no run-hours.
 */
export const SPEND_DIMENSIONS = Object.freeze([
  'day',
  'week',
  'month',
  'year',
  'repo',
  'model',
  'person',
  'mode',
] as const)

/** SpendDimension is one of them. */
export type SpendDimension = (typeof SPEND_DIMENSIONS)[number]

/**
 * SPEND_ACCOUNTS is the three accounts the design keeps apart, in the order the
 * screen draws them: the billed model dollars, the notional `~$` of a
 * subscription, and compute (an estimate by design). It is a list rather than
 * three fields so that "every group holds exactly three bars" is a fact a test
 * can read off it.
 */
export const SPEND_ACCOUNTS = Object.freeze(['model', 'notional', 'compute'] as const)

/** SpendAccount is one of them. */
export type SpendAccount = (typeof SPEND_ACCOUNTS)[number]

/**
 * SPEND_MAX_RANGE_DAYS is the longest range this console will enumerate a day
 * series over: `reportMaxDays` (`ee/report/cli/report.go:39`), which the CLI
 * bounds `--since` and the resolved range with, and which `src/main/ipc.ts`
 * restates as `REPORT_MAX_DAYS` for the same reason on the argument side. A
 * range longer than this is one `report` itself refuses, so a document claiming
 * one is not a series the console can trust; `spend.test.ts` holds this
 * constant to both the Go source and the IPC bound, by name.
 */
export const SPEND_MAX_RANGE_DAYS = 3660

/**
 * SPEND_THEMES is the set of themes a screenshot golden is taken for, and it is
 * the same two the Live screen names: design section 6.1 rule 5 has a light and
 * a dark palette and a small phosphor set besides, the tokens are not in this
 * task, and `data-theme` is how a theme reaches the document. The two lists are
 * held equal by a test until `theme/tokens.css` lands and the screens share it.
 */
export const SPEND_THEMES = Object.freeze(['light', 'dark'] as const)

/** SpendTheme is one of them. */
export type SpendTheme = (typeof SPEND_THEMES)[number]

/** MICROS_PER_USD is `budget.Micros`' own scale: the contract's `*_micros` are
 * millionths of a dollar (`USD6` formats the same integer as a 6-decimal
 * string). */
const MICROS_PER_USD = 1_000_000

/**
 * PARTIAL_MARKER is the marker the design draws beside a partial day. It is the
 * CLI's own word: `Table` in `ee/report/render.go` appends `" (partial)"`
 * to the key of a partial row, and the console says the same word about the
 * same fact rather than inventing a second one for it.
 */
export const PARTIAL_MARKER = '(partial)'

/**
 * UNREADABLE_MARKER is the console's sentence beside a row whose every run
 * record was unreadable (`JSONLine.Unreadable`, contract section 4). Only
 * `report --by mode` sets the mark: a record the build cannot read, or one the
 * run has not written yet, still means the run launched, so it counts once in
 * `runs` and the money keys are `null`. The words name the fact the two cases
 * share -- there is no readable record -- rather than "unreadable" alone,
 * because the usual `none` row of a window that reaches into today is a run
 * still in flight, whose record is not corrupt but simply not written
 * (`modeRecords`, `ee/report/cli/report.go`). The figures beside this sentence
 * are therefore the `n/a` the reader already draws for an absent key -- never a
 * `$0.00` that would read as a measurement. The words are the console's own;
 * `report`'s table and CSV draw `n/a` there and name no mark.
 */
export const UNREADABLE_MARKER = 'no readable record'

/**
 * NO_RECORD_REASON is the `title` an unreadable row's money cells carry, in
 * place of `readNumber`'s "the document has no <field>". Such a row's `*_micros`
 * is present and `null` on purpose -- there was no readable record to measure --
 * so naming the missing key would be the false sentence the marker exists to
 * prevent. Every other null keeps `readNumber`'s own reason.
 */
const NO_RECORD_REASON = 'no readable record for these runs'

/**
 * RESERVE_LABEL is the words the caps panel puts beside `max_reserve_usd`. They
 * are the CLI's own words for the same fact: `printShow` in
 * `internal/cli/budget.go` writes "largest single lease (max reserve): %s" on a
 * line of its own, **above** the `project` block, because it is a limit on one
 * lease (`budget.Limits.MaxReserveMicros`) and not a cap. The panel keeps both
 * halves of that: the CLI's words, and a place outside the `global` block.
 */
export const RESERVE_LABEL = 'largest single lease (max reserve)'

/** CAPS_TITLE is the caps panel's own heading. The tab above the screen says
 * Spend, and this panel is the one region of it that answers a different
 * question -- what binds the money today -- so it says its own name rather than
 * borrowing the screen's. */
const CAPS_TITLE = 'Caps today'

/** CAPS_AT_CAP_WORD is what a repository whose counted dollars have reached its
 * daily cap is drawn as, beside the bar. It is a **word** and not a tone:
 * section 3.5 forbids a state the stylesheet alone carries, and the amber of
 * the bar is redundant by construction because this word is always with it. */
const CAPS_AT_CAP_WORD = 'at cap'

/**
 * The caps panel's words for the figures it draws. A caps row carries no column
 * headers -- it is a list of labelled figures rather than a table -- so every
 * figure carries the word for what it is, and a reader never meets a run of
 * numbers that says nothing about which account each one belongs to.
 *
 * `billed` is the console's word for `spent_usd`, the money actually charged
 * (`budget show`'s own "spent" figure, `showRepo.SpentUSD`). It is drawn plain:
 * no notional mark, no estimate mark, and never added to the notional dollars
 * beside it.
 */
const CAPS_DAILY_CAP_WORD = 'daily cap'
const CAPS_PER_RUN_CAP_WORD = 'per-run cap'
const CAPS_COUNTED_WORD = 'counted'
const CAPS_BILLED_WORD = 'billed'
const CAPS_NOTIONAL_WORD = 'notional'
const CAPS_COMPUTE_WORD = 'compute'
const CAPS_HEADROOM_WORD = 'headroom'

/** CAPS_BAR_UNITS is the cap bar's own drawing unit: the `viewBox` is a hundred
 * units wide, so the filled `rect`'s width is the share of the cap the counted
 * dollars reached and never a pixel (the shape the chart above and Live's fleet
 * bar share). `preserveAspectRatio="none"` stretches the box to the panel, so
 * the height is the stylesheet's and the width is the document's. */
const CAPS_BAR_UNITS = 100

// ---------------------------------------------------------------------------
// The scope names.
// ---------------------------------------------------------------------------

/**
 * SpendScope is the *scope* a repository's cap comes from, as the console words
 * it. `budget show --json` carries the word (`daily_cap_source`: `repo`,
 * `default` or `none`; `per_run_cap_source`: `repo`, `default`, `global` or
 * `none`, `internal/cli/budget.go`'s `perRunCapSource`), and the design's
 * sentence is that the caps panel names *the binding scope* -- a cap of $20 with
 * no scope is a number a reader cannot act on, because the fix is different
 * depending on whether it is the repository's own cap or the global fallback.
 *
 * `name` is the console's own sentence about the scope and never the
 * document's word; `known` is false for a word this build does not know, which
 * is drawn as an explicit unknown rather than as one of the four.
 */
export interface SpendScope {
  /** the document's own word, kept for a test and never drawn */
  readonly source: string
  /** the console's sentence naming the scope */
  readonly name: string
  readonly known: boolean
}

/** SCOPES is the closed map from the document's words to the console's
 * sentences. A `Map` and not an object literal: the key is the document's own
 * string, and a literal answers `'constructor'` and `'__proto__'` with an
 * inherited member instead of `undefined`. */
const SCOPES: ReadonlyMap<string, string> = new Map([
  ['repo', "this repository's own cap"],
  ['default', 'the repository default'],
  ['global', 'the global fallback'],
  ['none', 'no cap is set at this scope'],
])

/** UNKNOWN_SCOPE_NAME is what an unrecognised scope word is drawn as: the
 * console's own sentence, with the document's word nowhere in it. */
const UNKNOWN_SCOPE_NAME = 'a scope this console does not know'

/** scopeOf names the scope a cap came from. */
export function scopeOf(raw: string): SpendScope {
  const name = SCOPES.get(raw)
  if (name === undefined) return { source: raw, name: UNKNOWN_SCOPE_NAME, known: false }
  return { source: raw, name, known: true }
}

// ---------------------------------------------------------------------------
// The model's types.
// ---------------------------------------------------------------------------

/** SpendTokens is a model row's token counts, each a reading: a count the
 * document does not carry is `n/a`, never `0`. */
export interface SpendTokens {
  readonly in: Reading
  readonly out: Reading
  readonly cacheRead: Reading
  readonly cacheWrite: Reading
}

/** SpendRow is one line of the report: a day, a week, a repository, a model or
 * a person, with the three accounts kept apart. */
export interface SpendRow {
  /** the document's own key, undrawn: the screen draws it through `putText`
   * with the class the dimension implies */
  readonly key: string
  /** the period's first day, when the document carries one (`start`) */
  readonly start: string | null
  readonly end: string | null
  /** the day has no final document yet: the design's `(partial)` */
  readonly partial: boolean
  /** a partial row is drawn hollow */
  readonly hollow: boolean
  /** every run record behind the row was unreadable, so its money keys are
   * `null` and the row is drawn `n/a` (`JSONLine.Unreadable`; absent -- and so
   * `false` -- on a measured row) */
  readonly unreadable: boolean
  readonly model: Reading
  readonly notional: Reading
  readonly compute: Reading
  readonly runHours: Reading
  readonly runs: Reading
  readonly calls: Reading
  readonly tokens: SpendTokens | null
}

/** SpendPoint is one point of one account's series. */
export interface SpendPoint {
  /** where the point sits on the axis: the row's `start`, or its key */
  readonly at: string
  /** the key as it is drawn: the document's row key, or a day the console
   * enumerated for a gap */
  readonly key: string
  /** the account's value at this point */
  readonly reading: Reading
  readonly partial: boolean
  readonly hollow: boolean
}

/**
 * SpendSeries is one account's series: the points, and the console's sentence
 * for a series with nothing in it. It is one of the **three** the chart groups
 * by key, and it carries no scale of its own -- the bars are drawn against
 * `SpendChart.axis`, the one axis the three accounts share, because three
 * accounts on three scales would be three charts pretending to be one.
 */
export interface SpendSeries {
  readonly account: SpendAccount
  /** the console's own heading for the account */
  readonly title: string
  readonly points: readonly SpendPoint[]
  /** why there is nothing to draw, or `null` when there is */
  readonly empty: string | null
}

/**
 * SpendBar is one bar of the grouped chart: one account's value for one key.
 *
 * Its `share` is the bar's height as a share of the chart's **one shared axis**,
 * not of the account's own peak: the three accounts are drawn as three bars side
 * by side on one scale, so a reader can see how the day's three figures sit
 * against each other. The bar is its own account's value and never a sum -- a
 * bar carrying billed + notional would be the one figure the design forbids --
 * and `reading` is what the document said about it, which is the figure the
 * table draws beside it.
 */
export interface SpendBar {
  readonly key: string
  readonly account: SpendAccount
  readonly reading: Reading
  /** the bar's height, as a percent of the chart's own shared axis */
  readonly share: Reading
  readonly partial: boolean
  readonly hollow: boolean
}

/**
 * SpendGroup is one key's group: the three accounts' bars side by side, and the
 * document's own row behind them -- or `null` for a day the history does not
 * hold, which is drawn as one hatched column labelled "no record" and never as
 * three bars of no height.
 *
 * `bars` holds one bar per account, in `SPEND_ACCOUNTS` order, always three of
 * them: a bar whose reading is `n/a` is still a bar here (the model says what
 * the document said), and the drawing is what leaves it out and writes its `n/a`
 * at its foot.
 */
export interface SpendGroup {
  readonly key: string
  /** the history holds no document for this key */
  readonly gap: boolean
  readonly partial: boolean
  /** the document's row is not final yet: hollow in the chart, tagged in the table */
  readonly hollow: boolean
  /** the document said it could not read the row's own record */
  readonly unreadable: boolean
  readonly row: SpendRow | null
  readonly bars: readonly SpendBar[]
}

/** GAP_CELL_SPAN is how many columns the gap row's one cell covers: the three
 * money columns, whose figures there is no document to carry. The counts that
 * follow them -- the runs, the runtime and the two token columns -- are drawn as
 * the em dash every other gap figure of that row is. */
const GAP_CELL_SPAN = String(SPEND_ACCOUNTS.length)

/**
 * SpendChart is the one chart the screen draws: a shared axis, one group per
 * key, and the rows the range does not cover.
 *
 * `axis` is the **largest value any account draws**, in that account's own state
 * -- a `~$` axis top is a notional figure and says so. It is one value the
 * document carries and never a sum: the three accounts are never added, on the
 * axis or anywhere else, and an axis topped by billed + notional would be a
 * figure no document holds.
 *
 * `outside` is the rows whose key is not a key of the chart (a day before
 * `since`, say). They are listed in the table and are not drawn in the chart,
 * because on a day axis the position IS the date: a row placed by its own date
 * would sit right of `until` and stretch the axis past the range.
 */
export interface SpendChart {
  readonly axis: Reading
  readonly groups: readonly SpendGroup[]
  readonly outside: readonly SpendGroup[]
}

/**
 * SpendTotals is the document's own `totals` line: three readings, the run
 * hours and the run count. Nothing here is computed by the console -- a screen
 * that summed the rows itself would be a second opinion about the document's own
 * arithmetic, and the one figure it must never compute is the sum of the three
 * accounts.
 */
export interface SpendTotals {
  readonly model: Reading
  readonly notional: Reading
  readonly compute: Reading
  readonly runHours: Reading
  readonly runs: Reading
  readonly partial: boolean
  /** every row the total sums was unreadable, so its money keys are `null` and
   * the total is drawn `n/a` with the mark -- the document's own rule for the
   * totals line (`Row.Unreadable`, `report.Build`; contract section 4, "The
   * `totals` row follows the rows the same way") */
  readonly unreadable: boolean
}

/** SpendGlobalCaps is `budget show`'s `global` block, and nothing but it: the
 * block's own two caps, the counters beside them, and the kill switch. Every cap
 * is `*float64` on the wire, so `null` is `n/a` and never `0`.
 *
 * The largest single lease is deliberately **not** here: `max_reserve_usd` is a
 * top-level field of the document (`budgetShowDoc`, `internal/cli/budget.go`),
 * and it is a *limit* rather than a cap -- the CLI prints it on its own line
 * above the `project` block, and its own mode line treats "the reserve limit" as
 * something separate from the caps. It is `SpendCaps.reserve`, drawn outside the
 * block whose word is `global`. */
export interface SpendGlobalCaps {
  readonly dailyCap: Reading
  readonly perRunCap: Reading
  readonly counted: Reading
  readonly spent: Reading
  readonly notional: Reading
  readonly headroom: Reading
  readonly kill: KillView | null
}

/** SpendDefaultCaps is `budget show`'s `defaults` block: the repository-wide
 * caps a repository inherits. */
export interface SpendDefaultCaps {
  readonly repoDailyCap: Reading
  readonly repoPerRunCap: Reading
}

/** SpendCapRow is one repository's caps, with the scope that binds each. */
export interface SpendCapRow {
  readonly slug: string
  readonly repo: string
  readonly counted: Reading
  readonly spent: Reading
  readonly notional: Reading
  /** compute is the repository's compute dollars, an **estimated** figure by
   * design (`runstore.Cost.ComputeUSD`), written by `budget show --compute`
   * since 2026-10-29. The caps panel draws it as a third labelled figure beside
   * the billed and the notional dollars -- `est. $0.12`, never the notional mark
   * and never added to either of them -- and the key is **flag-conditioned**: no
   * caller of this console's `budget.show` row passes `--compute` until the
   * Spend screen's read is flipped behind `budgetComputeSupported`
   * (`src/shared/version.ts`), so today only a document a person hands this
   * reader carries it.
   *
   * Absent, `null` and a value that is not a number are all `n/a`, never
   * `$0.00`: the caps panel refuses no document at all, so a key named ahead of
   * its release is drawn the way this reader draws a shape it cannot read
   * (contract §2), where the closed decoders refuse it by name. */
  readonly compute: Reading
  /** carriesCompute is whether this row carries `compute_usd` at all, which is
   * the flag's own trace on the wire: `budget show --compute` writes the key on
   * every repository it lists, and without the flag the CLI writes none. The
   * panel draws the compute figure only when some row carries the key, because
   * the column is the **read's** and not the row's: a document that was not read
   * with `--compute` gets no compute figure at all, rather than every repository
   * row wearing a permanent `n/a` for a flag nobody passed. */
  readonly carriesCompute: boolean
  readonly headroom: Reading
  readonly dailyCap: Reading
  readonly dailyScope: SpendScope
  readonly perRunCap: Reading
  readonly perRunScope: SpendScope
  readonly kill: KillView | null
}

/** SpendCaps is the caps panel: global, defaults, the per-repository overrides
 * and the money the document reports beside them. */
export interface SpendCaps {
  /** false when no `budget show --json` document was read: the panel then says
   * so rather than drawing a page of zeros */
  readonly available: boolean
  readonly project: string | null
  readonly day: string | null
  /** the document's own `mode`. `""` is the one value only this document
   * carries (the contract's section 3 note), and it is read as **unset** --
   * described below -- rather than drawn as an empty cell. */
  readonly mode: string | null
  /** the document's top-level `max_reserve_usd`: the largest single lease this
   * project may take (`budget.Limits.MaxReserveMicros`), which is a **limit**
   * rather than a cap and belongs to the project, not to the `global` block. */
  readonly reserve: Reading
  readonly global: SpendGlobalCaps | null
  readonly defaults: SpendDefaultCaps | null
  readonly repos: readonly SpendCapRow[]
  readonly warnings: readonly string[]
}

/** SpendSwitchEntry is one word of the breakdown switch: the CLI's own word,
 * whether this console may show it, and whether it is the document's current
 * grouping. */
export interface SpendSwitchEntry {
  readonly dimension: SpendDimension
  readonly enabled: boolean
  /** is this the breakdown the report was asked for? */
  readonly current: boolean
  /** why it is off, or `null` when it is on */
  readonly note: string | null
}

/** SpendOptions is what the caller of `buildSpendModel` decides. */
export interface SpendOptions {
  /**
   * `persons` turns the `person` breakdown on. It is **off by default** (design
   * D15): the report can name the people whose runs spent the money, and a
   * desktop window is not a terminal. The rows are in the document either way;
   * this decides whether the screen draws them.
   */
  readonly persons?: boolean
  /**
   * `people` is the `runs stats --json` document behind the by-person view
   * (`SpendPeople`, console L5b), or `null` when the console made no such read.
   * It is an option rather than a third document of this function for the same
   * reason the budget document is the *caller's*: it is a different operation,
   * and `readSpend` is what decides whether the read is made at all.
   */
  readonly people?: JSONDoc | null
  /**
   * `peopleReason` is the console's sentence for a by-person document that is
   * not there: a read the CLI refused (the sanitized sentence), a window the
   * command cannot express, or no read at all. The panel draws it instead of an
   * empty list, because "we did not read it" and "nobody spent anything" are
   * different facts.
   */
  readonly peopleReason?: string | null
}

/** SpendPersons says whether the person breakdown is on, and whether the
 * document's rows are being withheld because it is not. */
export interface SpendPersons {
  readonly enabled: boolean
  /** true when the document groups by person and the view is off, so the rows
   * exist and are deliberately not drawn */
  readonly withheld: boolean
}

/**
 * SpendPersonRow is one row of `runs stats --json` (console L5b;
 * `console/docs/json-contract.md` section 6.10): one launcher's group, with the
 * counts that add up (`runs == succeeded + failed + halted + infra_error + other
 * + no_record`) and the two money accounts kept apart exactly as the report's
 * three are.
 */
export interface SpendPersonRow {
  /** the identity the run's own `launch.json` recorded. **Untrusted display
   * text** -- a service account or a CI identity is a row like a person's -- so
   * it is drawn only through `putText` in the `person` class, and nothing is
   * authorised by it. `"unknown"` is the CLI's own key for runs that recorded no
   * launcher, and it is the *report's* spelling of that group, not a person. */
  readonly user: string
  /** the unit the money columns are in (`USD` today): the document's rows are
   * per (user, currency), because dollars are never added across currencies */
  readonly currency: string
  readonly runs: Reading
  readonly succeeded: Reading
  readonly failed: Reading
  readonly halted: Reading
  readonly infraError: Reading
  readonly other: Reading
  readonly noRecord: Reading
  /** `counted_usd`: the **model** dollars that count toward the caps */
  readonly counted: Reading
  /** `notional_usd`: the part of the model figure a subscription paid --
   * `~$`, never summed with `counted` */
  readonly notional: Reading
  /** `counted_runs`: the sample `average` is over, published because an average
   * without its sample is a figure a reader cannot weigh */
  readonly countedRuns: Reading
  readonly average: Reading
}

/** PeopleView is the view the CLI resolved, in the console's own three words.
 * `unknown` is a `view` word this build does not know, and it draws no rows --
 * the same fail-private rule the other two states follow. */
export type PeopleView = 'all' | 'own' | 'unknown'

/**
 * SpendPeople is the Spend screen's by-person view: `runs stats --json`, drawn
 * as its own panel beside the report's rows (`renderSpend`).
 *
 * **The view is the CLI's ruling, not this screen's request.** The console
 * cannot pass `--all-users` (design section 5.1 rule 6, its schema in
 * `src/main/ipc.ts`): the command decides for itself who the caller is, so an
 * operator of the installation sees every user's rows and a plain launcher sees
 * their own row alone. What this screen adds is the drawing rule that ruling
 * implies, and it is **fail private**:
 *
 *   - only a `view` of `all` read for an identity draws every row the document
 *     carries; read for no identity it draws the document's totals and **no
 *     row** (design section 2.4's state list) -- `you` empty is *unavailable*
 *     (the CLI could not tell), and the `unknown` key is a group of runs rather
 *     than a person;
 *   - a `view` of `own` draws only the row the document keyed by the identity it
 *     resolved (`you`), and **nothing at all** when the document does not say
 *     who the caller is;
 *   - a `view` or a `by` this build does not know draws no rows;
 *   - the document's own `totals` are drawn **as the document published them,
 *     and never computed here** -- and what they are the sum of follows the
 *     view, because the CLI aggregates after it filters (`runs_stats.go`): the
 *     window's under `all`, the caller's own runs alone under a licensed `own`
 *     (where `PEOPLE_OWN_TOTAL` says so on screen), and **absent** in the two
 *     states where the CLI publishes no row at all, its totals coming from its
 *     rows (`userstats.Aggregate`).
 */
export interface SpendPeople {
  /** false when there is no document behind this panel: the read was refused,
   * the picker's window has no `runs stats` form, or the console made no read */
  readonly available: boolean
  /** the console's sentence for an unavailable panel, and `null` when there is
   * a document */
  readonly reason: string | null
  readonly view: PeopleView
  /** the console's own name for the view, drawn as a literal */
  readonly viewName: string
  /** the document's own word, kept for a test and never drawn */
  readonly viewSource: string | null
  /** the document's `operator`: whether the installation's own lists place the
   * caller as an operator. Drawn as its own fact and never inferred from
   * `view` -- the CLI's contract is explicit that `--all-users` produces `all`
   * with `operator: false`, and a screen that read `all` as "you are an
   * operator" would say something the document did not. */
  readonly operator: boolean
  /** the identity the view was resolved for. `''` is *unavailable* -- the CLI
   * could not tell who the caller is -- and never anonymous. */
  readonly you: string
  /** true when the document's own fields do not license drawing rows: an
   * unknown `view` or `by`, or either view with no identity to key a row by */
  readonly failPrivate: boolean
  /** how many rows the rule above did not draw */
  readonly withheld: number
  /** the window the CLI read, as its own UTC days (`since` is `''` for
   * `--since 0`, which this screen never asks for) */
  readonly since: string | null
  readonly until: string | null
  /** the read's own bound (`--max-runs`, 5000 by default) */
  readonly maxRuns: Reading
  /** the window held more runs than that bound: every figure is over the newest
   * `maxRuns` alone, and the panel says so rather than drawing it as the
   * window's total */
  readonly truncated: boolean
  /** runs in the window that never started: in no row and in no total, and the
   * window's own count rather than the caller's */
  readonly unlaunched: Reading
  /** the rows this screen draws, after the fail-private rule */
  readonly rows: readonly SpendPersonRow[]
  /** the document's own per-currency totals (its `user` is `""`), drawn
   * exactly as published: the rows of the read that produced them, which is the
   * window under `all` and the caller's own runs under `own`, and **empty** in
   * the two states where the CLI published no row to sum */
  readonly totals: readonly SpendPersonRow[]
  /** the console's own sentences about what is drawn, in the order they are
   * drawn; every one is a console constant or is composed from them */
  readonly sentences: readonly string[]
  /** the document's own notes, drawn through `putText` and never as literals */
  readonly notes: readonly string[]
}

/** SpendModel is the whole screen as data. */
export interface SpendModel {
  readonly project: string
  /** `report --json`'s `source`: `firestore`, or `run-records` for the
   * degraded path (contract section 4) */
  readonly source: string | null
  readonly degraded: boolean
  /** the degraded document's own notice, when it carries one */
  readonly notice: string | null
  readonly range: {
    readonly since: string | null
    readonly until: string | null
    /** the document's declared week convention (`iso-utc-monday`) */
    readonly weeks: string | null
  }
  /** the document's `by`, or `null` for a word this console does not know */
  readonly dimension: SpendDimension | null
  readonly dimensionKnown: boolean
  readonly switches: readonly SpendSwitchEntry[]
  readonly series: readonly SpendSeries[]
  readonly chart: SpendChart
  readonly totals: SpendTotals
  readonly caps: SpendCaps
  readonly persons: SpendPersons
  /**
   * the by-person view (`runs stats --json`, console L5b), or `null` when the
   * person view is off: the panel exists only while the operator's own knob is
   * on (design D15), so a screen with the knob off has no question to answer
   * about whose rows it may draw and no read to make.
   */
  readonly people: SpendPeople | null
  readonly warnings: readonly string[]
  /** the console's own sentences about what is not drawn, and why */
  readonly notes: readonly string[]
}

// ---------------------------------------------------------------------------
// The readers.
// ---------------------------------------------------------------------------

/** objectAt is a JSON object, or `null` for anything else. A document's
 * `rows[3]` that is a string is not a row, and a reader that threw here would
 * turn a stranger's document into a crash. */
function objectAt(raw: unknown): Record<string, unknown> | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

/** arrayAt is a JSON array, or `[]` for anything else. */
function arrayAt(raw: unknown): readonly unknown[] {
  return Array.isArray(raw) ? raw : []
}

/** textAt is a string field, or `null`. It is deliberately not a "coerce to
 * string": a number under a text key is not text, and `putText` would rather
 * refuse it than draw `[object Object]`. */
function textAt(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

/** boolAt is a boolean field, or false. */
function boolAt(raw: unknown): boolean {
  return raw === true
}

/**
 * microsReading reads one `*_micros` integer as dollars, in the account's own
 * state. An absent or non-numeric key is the reading `readNumber` returns --
 * `n/a` with the field's own sentence -- so a null `notional_micros` ("not
 * tracked") and a null `compute_micros` ("no run was estimated") are the two
 * `n/a`s the contract asks for, and neither becomes a zero.
 *
 * The state is the account's, not the document's: a present `compute_micros` is
 * an estimate by construction (`jsonLine` sets it only under `ComputeKnown`, and
 * `compute_estimated` is true whenever it is set), and a present
 * `notional_micros` is a subscription's list price. A document that carried one
 * of them with the flag off is not a shape `report` writes; reading the number
 * would still be reading the same integer, and hiding it would be the worse
 * error of the two.
 *
 * On a row the document marks `unreadable` every money key is `null` because
 * there was no record to read, not because a key is missing, so the `n/a`
 * carries `NO_RECORD_REASON` rather than `readNumber`'s field sentence. The
 * flag is passed in, like the account, so a caller that forgets it is a line a
 * reviewer can see.
 */
function microsReading(
  line: Record<string, unknown>,
  key: string,
  account: SpendAccount,
  unreadable: boolean,
): Reading {
  const micros = readNumber(line[key], key)
  if (micros.value === null) return unreadable ? unavailable(NO_RECORD_REASON) : micros
  const usd = micros.value / MICROS_PER_USD
  switch (account) {
    case 'model':
      return measured(usd)
    case 'notional':
      return notional(usd)
    case 'compute':
      return estimated(usd)
  }
}

/** countReading reads an integer field (`runs`, `calls`, a token count) as a
 * count. */
function countReading(line: Record<string, unknown>, key: string): Reading {
  return readNumber(line[key], key)
}

/**
 * usdReading reads a `*_usd` number of the caps document in a **named account's
 * state**. `budget show`'s dollars are decimal numbers rather than micros, so
 * they come through `readNumber` as measured -- which is right for the caps,
 * the counted dollars and the headroom, and wrong for `notional_usd`: that is
 * the same account as the report's `notional_micros`, and a subscription's list
 * price drawn without its mark is a wrong number that looks right. The account
 * is passed in rather than inferred, so a caller that reaches for the wrong one
 * is a line a reviewer can see.
 */
function usdReading(
  container: Record<string, unknown>,
  key: string,
  account: SpendAccount,
): Reading {
  const reading = readNumber(container[key], key)
  if (reading.value === null) return reading
  return withAccountState(reading.value, account)
}

/** tokensOf reads a model row's `tokens` object, or `null` when the document
 * does not track tokens (`TokensTracked`; the key is absent entirely). */
function tokensOf(raw: unknown): SpendTokens | null {
  const tokens = objectAt(raw)
  if (tokens === null) return null
  return {
    in: countReading(tokens, 'in'),
    out: countReading(tokens, 'out'),
    cacheRead: countReading(tokens, 'cache_read'),
    cacheWrite: countReading(tokens, 'cache_write'),
  }
}

/** rowOf reads one `JSONLine`. A line that is not an object is not a row and is
 * dropped by the caller. */
function rowOf(raw: unknown): SpendRow | null {
  const line = objectAt(raw)
  if (line === null) return null
  const partial = boolAt(line['partial'])
  const unreadable = boolAt(line['unreadable'])
  return {
    key: textAt(line['key']) ?? '',
    start: textAt(line['start']),
    end: textAt(line['end']),
    partial,
    hollow: partial,
    unreadable,
    model: microsReading(line, 'model_micros', 'model', unreadable),
    notional: microsReading(line, 'notional_micros', 'notional', unreadable),
    compute: microsReading(line, 'compute_micros', 'compute', unreadable),
    runHours: readNumber(line['run_hours'], 'run_hours'),
    runs: countReading(line, 'runs'),
    calls: countReading(line, 'calls'),
    tokens: tokensOf(line['tokens']),
  }
}

/** totalsOf reads the document's `totals` line, which is a `JSONLine` like any
 * other and is **not** recomputed from the rows. */
function totalsOf(raw: unknown): SpendTotals {
  const line = objectAt(raw) ?? {}
  const unreadable = boolAt(line['unreadable'])
  return {
    model: microsReading(line, 'model_micros', 'model', unreadable),
    notional: microsReading(line, 'notional_micros', 'notional', unreadable),
    compute: microsReading(line, 'compute_micros', 'compute', unreadable),
    runHours: readNumber(line['run_hours'], 'run_hours'),
    runs: countReading(line, 'runs'),
    partial: boolAt(line['partial']),
    unreadable,
  }
}

/** killOf reads a `showKill` (`budget.go:233`, the same shape as `JSONKill`).
 * `null` is "the switch is off", which is a measured fact and not a missing
 * one; a kill object is three strings the screen draws through `putText`. */
function killOf(raw: unknown): KillView | null {
  const kill = objectAt(raw)
  if (kill === null) return null
  return {
    by: textAt(kill['by']) ?? '',
    at: textAt(kill['at']) ?? '',
    reason: textAt(kill['reason']) ?? '',
  }
}

// ---------------------------------------------------------------------------
// The by-person view (`runs stats --json`, console L5b).
// ---------------------------------------------------------------------------

/** PEOPLE_BY is the one breakdown `runs stats` publishes: `user`, the only
 * value its `--by` takes. A document that groups by anything else is not the
 * shape this panel draws, and its rows are not drawn at all. */
const PEOPLE_BY = 'user'

/**
 * UNATTRIBUTED_KEY is `userstats.UnknownUser`: the key `runs stats` gives the
 * runs of a window that recorded no launcher at all. It is a **row key** and not
 * a person, so it is never drawn as one -- `report`'s own person breakdown
 * spells the same group `unknown` (`budget.UnknownPerson`), and the two are
 * pinned equal by a Go test.
 */
const UNATTRIBUTED_KEY = 'unknown'

/** dayOrNull reads a UTC day the way the rest of this file does: an empty string
 * is *absent* and never a day, so `since: ""` -- the document's own spelling of
 * an unbounded window -- becomes `null` rather than a date nobody wrote. */
function dayOrNull(raw: unknown): string | null {
  const day = textAt(raw)
  return day === null || day === '' ? null : day
}

/** NO_PEOPLE_DOCUMENT is the reading's own sentence for a figure of a panel
 * with no document behind it. It is not a zero, and it is not the refusal's
 * sentence either: the sentence the *panel* draws says why there is no
 * document, and this one says only that this figure was never read. */
const NO_PEOPLE_DOCUMENT = 'no runs stats document was read, so this figure is not a zero'

/** NO_PEOPLE_READ is the console's sentence for a by-person panel it was given
 * no reader for: a caller that draws the panel without passing the read. It is
 * a fact about the console and not about the CLI, and it is not a refusal. */
const NO_PEOPLE_READ = 'this console made no by-person read, so it has nothing to draw here'

/** PERSON_AVERAGE_NO_SAMPLE is the reading for `avg_counted_usd` when the group
 * has no run to average. The document writes `0` there with `counted_runs` `0`,
 * and that zero is *unavailable* -- "no run to average" -- never "$0.00 a run",
 * which is what drawing it would say. */
const PERSON_AVERAGE_NO_SAMPLE =
  'no run in this group settled a real-dollar cost, so there is no average to draw'

/** PEOPLE_VIEW_NAMES is the console's own name for each view the CLI resolves.
 * A `Map` and not a literal object, for the reason `SCOPES` is one: the key is
 * the document's own string, and a literal answers `'constructor'`. */
const PEOPLE_VIEW_NAMES: ReadonlyMap<string, string> = new Map([
  ['all', 'everyone in this project'],
  ['own', 'you alone'],
])

/** UNKNOWN_PEOPLE_VIEW is what a `view` word this build does not know is drawn
 * as: the console's own sentence, with the document's word nowhere in it. */
const UNKNOWN_PEOPLE_VIEW = 'a view this console does not know'

/** PEOPLE_UNKNOWN_BY is the sentence for a document grouped by a breakdown this
 * console does not know: no rows are drawn, because the panel's whole subject
 * is who the launcher was. */
const PEOPLE_UNKNOWN_BY =
  'the document groups by a breakdown this console does not know, so no rows are drawn here'

/** PEOPLE_UNKNOWN_VIEW is the same sentence for an unknown `view` word. */
const PEOPLE_UNKNOWN_VIEW =
  'the document names a view this console does not know, so no rows are drawn here'

/** PEOPLE_PRIVATE is the fail-private sentence: the document did not say who
 * the read was resolved for, so the console draws no row at all. It is the
 * design's rule in the console's own words -- an identity the installation's own
 * lists do not place stays private -- and it is drawn in place of the rows. It
 * promises no totals: in this state the CLI publishes none (`you` empty means
 * every run was deleted before the aggregation, so `totals` comes back `[]`),
 * and the panel must not claim a figure the document does not carry. */
const PEOPLE_PRIVATE =
  'the CLI did not say who this read was resolved for, so no rows are drawn here: an identity the installation’s own lists do not place stays private'

/** PEOPLE_UNATTRIBUTED is the fail-private sentence for the one identity the CLI
 * resolves but will not key a private view by: the unattributed key itself
 * (`userstats.UnknownUser`, `"unknown"`), whose row holds every run of the
 * window that recorded no launcher -- nobody's own figures. The CLI shows such a
 * caller nothing at all, and so does this screen: no row **and no total**, for
 * the same reason (`runs_stats.go` filters to nothing before `Aggregate`). */
const PEOPLE_UNATTRIBUTED =
  'the identity this read resolved for you is the unattributed key itself, which holds every run of the window that recorded no launcher: those are nobody’s own figures, so no row is drawn here'

/** PEOPLE_OWN is the sentence for the private view: what it is, and what it
 * deliberately does not say. */
const PEOPLE_OWN =
  'the CLI resolved this read as your own runs alone: these are the runs you started in the window and what they cost, and it says nothing about what anyone else spent'

/** PEOPLE_OWN_TOTAL is the sentence for the private view's totals line. The CLI
 * filters the private view *before* it aggregates (`internal/cli/runs_stats.go`:
 * every run whose launcher is not the caller is deleted, then
 * `userstats.Aggregate` sums what is left), so the total beside these rows is
 * the caller's own and not the window's. A reader who took it for the window's
 * -- the totals line is one line above it and looks the same -- would read it as
 * everyone's spend, which is the one thing this view may not say. */
const PEOPLE_OWN_TOTAL =
  'the total below is this read’s own sum of the rows it published, which for this view is your own runs alone: it is not the window’s total, and it says nothing about what anyone else spent'

/** PEOPLE_ALL is the sentence for the view that shows everyone. It states the
 * ruling and not the reason for it: whether the caller is an operator is the
 * document’s own `operator`, drawn beside this line. */
const PEOPLE_ALL =
  'the CLI resolved this read as the whole project, so every launcher that started a run in the window is listed — including the unattributed runs, under the key the CLI gives them'

/** PEOPLE_NO_ROW is the sentence for a private view that found no row: the
 * contract is explicit that this is a state of its own and not a failure (a
 * window the caller started nothing in, or an identity with no key of its own). */
const PEOPLE_NO_ROW =
  'the CLI found no row for this view in the window: a window the caller started nothing in is a state of its own, not an error'

/** PEOPLE_WITHHELD_OWN and PEOPLE_WITHHELD_UNKNOWN are the two sentences for
 * rows the fail-private rule did not draw. They are drawn as a **count** (the
 * sentence is composed with it) because a reader who cannot see the rows is owed
 * the fact that they exist: "no rows" and "rows this console will not draw" are
 * different states, and the second one is the state this panel must not hide. */
const PEOPLE_WITHHELD_OWN =
  'row(s) of this document are not drawn here: the CLI resolved this view as your own row alone, so other people’s rows are withheld'
const PEOPLE_WITHHELD_UNKNOWN =
  'row(s) of this document are not drawn here: it does not say whose view they are for, and this console draws none of them'

/** PEOPLE_TRUNCATED is the sentence for the read’s own bound. It is drawn the
 * way a gap is drawn -- as a fact about the window -- because a figure over the
 * newest N runs is not the window’s total, and a reader who takes it for one
 * reads a partial window as a small one. */
const PEOPLE_TRUNCATED =
  'the window held more runs than the CLI read (its own max-runs bound, below): every figure here is over its newest run alone, and none of them is the window’s total'

/** PEOPLE_UNLAUNCHED is the sentence for the runs that never started. They are
 * the window’s count and not the caller’s: a run that never launched has no
 * launcher to attribute. The count it names is drawn, in
 * `span.spend-people-unlaunched` after the totals (`peoplePanel`). */
const PEOPLE_UNLAUNCHED =
  'runs in the window that never started are counted after the totals, on the never-started line, and in no person’s row: they have no launcher to attribute'

/** PEOPLE_UNLAUNCHED_WORD is the console's label for that count's cell. */
const PEOPLE_UNLAUNCHED_WORD = 'never started'

/** PEOPLE_ALL_UNIDENTIFIED is the fail-private sentence for the whole-project
 * view read for nobody the CLI could name (`--all-users` is admitted with no
 * identity; only the private view refuses one). Design section 2.4 and
 * desktop-console.md section 6.2: `data-view="all"` with no identity draws the
 * document's totals and no row at all -- the totals name nobody, and the rows
 * are not drawn for a reader the read does not say it was resolved for. */
const PEOPLE_ALL_UNIDENTIFIED =
  'the CLI resolved this read as the whole project but did not say who this read was resolved for, so no rows are drawn here: only the read’s own totals, which name nobody'

/** PEOPLE_YOU_UNIDENTIFIED is what the panel draws where the identity would go
 * when the CLI could not tell who the caller is. `''` is *unavailable* and not
 * anonymous, and an empty cell would say neither. */
const PEOPLE_YOU_UNIDENTIFIED = 'the CLI could not say who this read was resolved for'

/** PERSON_OPERATOR_WORDS is the console's two sentences for the document's
 * `operator`, keyed by the boolean. */
const PERSON_OPERATOR_YES = 'the installation’s own lists place you as an operator'
const PERSON_OPERATOR_NO = 'the installation’s own lists do not place you as an operator'

/** peopleViewOf reads the document's `view` into the console's closed three. */
function peopleViewOf(raw: string | null): { readonly view: PeopleView; readonly name: string } {
  const name = raw === null ? undefined : PEOPLE_VIEW_NAMES.get(raw)
  if (raw === 'all') return { view: 'all', name: name ?? UNKNOWN_PEOPLE_VIEW }
  if (raw === 'own') return { view: 'own', name: name ?? UNKNOWN_PEOPLE_VIEW }
  return { view: 'unknown', name: UNKNOWN_PEOPLE_VIEW }
}

/** personRowOf reads one `userstats.Row` -- the shape `rows` and `totals` share,
 * with `Counts` flattened into it. A line that is not an object is not a row and
 * is dropped by the caller. */
function personRowOf(raw: unknown): SpendPersonRow | null {
  const line = objectAt(raw)
  if (line === null) return null
  const countedRuns = countReading(line, 'counted_runs')
  return {
    user: textAt(line['user']) ?? '',
    currency: textAt(line['currency']) ?? '',
    runs: countReading(line, 'runs'),
    succeeded: countReading(line, 'succeeded'),
    failed: countReading(line, 'failed'),
    halted: countReading(line, 'halted'),
    infraError: countReading(line, 'infra_error'),
    other: countReading(line, 'other'),
    noRecord: countReading(line, 'no_record'),
    // `counted_usd` is the model account and `notional_usd` the notional one --
    // the same two accounts the report draws, the same marks, and never summed.
    counted: usdReading(line, 'counted_usd', 'model'),
    notional: usdReading(line, 'notional_usd', 'notional'),
    countedRuns,
    // The average is read from the document and not computed here: it is the
    // CLI's own rounding, and `counted_runs` is what says whether it exists.
    average:
      countedRuns.value === 0
        ? unavailable(PERSON_AVERAGE_NO_SAMPLE)
        : usdReading(line, 'avg_counted_usd', 'model'),
  }
}

/**
 * peopleOf reads the by-person document, or says why there is none.
 *
 * The drawing rules are `SpendPeople`'s, and the two that matter are here: rows
 * are drawn only when the document's own `by` and `view` license them, and in
 * the private view only the row keyed by the identity the CLI resolved. The
 * totals are read from the document as it published them and are never summed
 * here; what they are the sum *of* is the view's, so the private view gets a
 * sentence naming whose money that line is.
 */
function peopleOf(raw: JSONDoc | null, reason: string | null): SpendPeople {
  const document = raw === null ? null : objectAt(raw)
  if (document === null) {
    return {
      available: false,
      reason: reason ?? NO_PEOPLE_READ,
      view: 'unknown',
      viewName: UNKNOWN_PEOPLE_VIEW,
      viewSource: null,
      operator: false,
      you: '',
      failPrivate: true,
      withheld: 0,
      since: null,
      until: null,
      maxRuns: unavailable(NO_PEOPLE_DOCUMENT),
      truncated: false,
      unlaunched: unavailable(NO_PEOPLE_DOCUMENT),
      rows: [],
      totals: [],
      sentences: [],
      notes: [],
    }
  }
  const viewSource = textAt(document['view'])
  const { view, name } = peopleViewOf(viewSource)
  const byKnown = textAt(document['by']) === PEOPLE_BY
  const you = textAt(document['you']) ?? ''
  // The CLI's own contract, in `internal/cli/runs_stats.go`: `you` is in the key
  // domain of `rows[].user`, `''` is *unavailable* rather than a person, and the
  // unattributed key is a group of runs rather than a person -- a private view
  // keyed by either of them would credit the caller with runs that are not
  // theirs, which is why the CLI shows such a caller nothing and so does this.
  const identified = you !== '' && you !== UNATTRIBUTED_KEY
  const rows = arrayAt(document['rows'])
    .map(personRowOf)
    .filter((row): row is SpendPersonRow => row !== null)
  // The document's totals, read as it published them: this screen never sums a
  // row (a second sum is a figure the CLI did not write).
  const totals = arrayAt(document['totals'])
    .map(personRowOf)
    .filter((row): row is SpendPersonRow => row !== null)
  const truncated = boolAt(document['truncated'])
  const unlaunched = countReading(document, 'unlaunched')
  const maxRuns = countReading(document, 'max_runs')

  // `licensed` is the whole fail-private rule in one word: whether the
  // document's own fields say who these rows are *for*.
  // An identity is required by **both** views: the whole-project view read for
  // nobody the CLI could name draws its totals and no row (fail private, the
  // design's 2.4 state list), the same as the private view without one.
  const licensed = byKnown && identified && (view === 'all' || view === 'own')
  let drawn: readonly SpendPersonRow[] = []
  if (byKnown && identified && view === 'all') {
    drawn = rows
  } else if (byKnown && view === 'own' && identified) {
    // The private view shows the row keyed by the identity the CLI resolved --
    // and nothing else. A document that carried a row for anyone else draws
    // none of it: this is the filter, not a lookup that falls back to the first
    // row.
    drawn = rows.filter((row) => row.user === you)
  }
  const sentences: string[] = []
  if (!byKnown) sentences.push(PEOPLE_UNKNOWN_BY)
  else if (view === 'unknown') sentences.push(PEOPLE_UNKNOWN_VIEW)
  else if (view === 'own') {
    if (identified) {
      sentences.push(PEOPLE_OWN)
      // The totals line under these rows is this read's own sum of them, which
      // for the private view is the caller's own runs and not the window's.
      if (totals.length > 0) sentences.push(PEOPLE_OWN_TOTAL)
    } else sentences.push(you === UNATTRIBUTED_KEY ? PEOPLE_UNATTRIBUTED : PEOPLE_PRIVATE)
  } else sentences.push(identified ? PEOPLE_ALL : PEOPLE_ALL_UNIDENTIFIED)
  if (licensed && drawn.length === 0) sentences.push(PEOPLE_NO_ROW)
  if (rows.length > drawn.length) {
    // `licensed` decides which of the two sentences it is: a private view that
    // drew its own row withheld the other people's, and an unlicensed document
    // withheld everything it carried.
    const withheld = rows.length - drawn.length
    sentences.push(`${withheld} ${licensed ? PEOPLE_WITHHELD_OWN : PEOPLE_WITHHELD_UNKNOWN}`)
  }
  if (truncated) sentences.push(PEOPLE_TRUNCATED)
  if ((unlaunched.value ?? 0) > 0) sentences.push(PEOPLE_UNLAUNCHED)

  return {
    available: true,
    reason: null,
    view,
    viewName: name,
    viewSource,
    operator: boolAt(document['operator']),
    you,
    failPrivate: !licensed,
    // The rows the rule did not draw, and only those: it is a count of rows that
    // exist and are withheld, never a count of figures the document lacks.
    withheld: rows.length - drawn.length,
    // `''` is the unbounded window (`--since 0`) -- the one value `runs stats`
    // writes for an unbounded `since`, and a form this screen's picker never
    // produces -- so there is no day to draw and it is read as absent, exactly
    // as the caps panel reads an unset mode.
    since: dayOrNull(document['since']),
    until: dayOrNull(document['until']),
    maxRuns,
    truncated,
    unlaunched,
    rows: drawn,
    totals,
    sentences,
    notes: arrayAt(document['notes']).filter((note): note is string => typeof note === 'string'),
  }
}

// ---------------------------------------------------------------------------
// The day range.
// ---------------------------------------------------------------------------

const DAY_RE = /^(\d{4})-(\d{2})-(\d{2})$/
const MS_PER_DAY = 86_400_000

/** dayNumber is a `YYYY-MM-DD` day as a UTC epoch day, or `null`. The date is
 * round-tripped through `Date` so that a day that does not exist (`2026-02-31`)
 * is refused rather than silently rolled into March. */
function dayNumber(day: string): number | null {
  const parts = DAY_RE.exec(day)
  if (parts === null) return null
  const year = Number(parts[1])
  const month = Number(parts[2])
  const date = Number(parts[3])
  const ms = Date.UTC(year, month - 1, date)
  const back = new Date(ms)
  if (back.getUTCFullYear() !== year || back.getUTCMonth() !== month - 1 || back.getUTCDate() !== date) {
    return null
  }
  return Math.floor(ms / MS_PER_DAY)
}

/** dayText is a UTC epoch day as `YYYY-MM-DD`. It is the console's own
 * arithmetic, which is why a gap point's key can be drawn without going through
 * a document string. */
function dayText(epochDay: number): string {
  return new Date(epochDay * MS_PER_DAY).toISOString().slice(0, 10)
}

/**
 * dayRange is every UTC day of `since`..`until` inclusive, or `null` when the
 * console cannot enumerate them: a missing or malformed date, a range that runs
 * backwards, or a range longer than `reportMaxDays` (which `report` itself
 * refuses, so a document claiming one is a document this screen does not trust
 * to describe a series).
 *
 * The bound matters in both directions. Without it, a hostile or corrupt
 * `since` of `0001-01-01` would have the console build two million strings
 * before drawing anything.
 */
export function dayRange(since: string | null, until: string | null): readonly string[] | null {
  if (since === null || until === null) return null
  const from = dayNumber(since)
  const to = dayNumber(until)
  if (from === null || to === null || to < from) return null
  const count = to - from + 1
  if (count > SPEND_MAX_RANGE_DAYS) return null
  const days: string[] = []
  for (let index = 0; index < count; index++) days.push(dayText(from + index))
  return days
}

// ---------------------------------------------------------------------------
// The series.
// ---------------------------------------------------------------------------

/** ACCOUNT_TITLES are the three headings, in the console's own words. The
 * notional one carries the money mark, because the heading names the form the
 * figures beneath it are drawn in. */
const ACCOUNT_TITLES: Readonly<Record<SpendAccount, string>> = Object.freeze({
  model: 'billed model spend',
  notional: '~$ subscription list price',
  compute: 'compute (estimated)',
})

/** pick is the account's own reading off a row. */
function pick(row: SpendRow, account: SpendAccount): Reading {
  switch (account) {
    case 'model':
      return row.model
    case 'notional':
      return row.notional
    case 'compute':
      return row.compute
  }
}

/** Plotted is one value on the axis before the axis has a top: the account's
 * reading at a point, and the two flags of the row it came from. */
interface Plotted {
  readonly at: string
  readonly key: string
  readonly reading: Reading
  readonly partial: boolean
}

/** plotted is one row's value in one account. */
function plotted(row: SpendRow, account: SpendAccount): Plotted {
  return {
    at: row.start ?? row.key,
    key: row.key,
    reading: pick(row, account),
    partial: row.partial,
  }
}

/**
 * NO_DOCUMENT_DAY is the console's sentence about a gap in a day series. It
 * names no figure, because the fact it states is the console's: it has no
 * document for this day -- the day is outside the window `report` read, or it
 * was never written -- which is a different thing from a day that spent
 * nothing. It is the reading's reason, and `barOf` carries the same sentence
 * into the bar's share, because the height of a gap is the same fact.
 */
const NO_DOCUMENT_DAY =
  'the history holds no document for this day: it is outside the retained window, or it was never written'

/** NO_TOKENS_REASON is the console's sentence for a period whose document tracks
 * no tokens at all: the token breakdown is `omitempty` on the wire, so the
 * object is absent rather than zeroed, and `n/a` with this sentence is what says
 * so. It is not the sentence a period with no document gets: a period the
 * history does not hold is a gap. */
const NO_TOKENS_REASON = 'the document does not track tokens for this period'

/** dayGap is one day inside the range that the history does not hold, as a
 * value for the series: the reading is a **gap** in both senses the screen
 * draws -- the em dash where a figure would be, and the hatched column the
 * chart stands in the day's place -- and `barOf` gives it that state again as
 * the bar's share. */
function dayGap(day: string): Plotted {
  return { at: day, key: day, reading: gap(NO_DOCUMENT_DAY), partial: false }
}

/** pointAt is one value as a point of its account's series: the reading and
 * whether the row it came from is partial. A point carries **no height**: the
 * bars are sized against the chart's one shared axis (`barOf`), because the
 * height of a bar is a fact about the chart rather than about the account. */
function pointAt(value: Plotted): SpendPoint {
  const { at, key, reading, partial } = value
  return { at, key, reading, partial, hollow: partial }
}

/**
 * buildSeries turns one account's values into a chart.
 *
 * `days` is the enumerated range when the document groups by day and the range
 * could be enumerated, and `null` otherwise. With days, **every day of the
 * range is a point**: a day the history does not hold is a gap with the
 * console's own sentence, which is the design's "a day beyond the retained
 * window is a gap, not a zero" made into the series itself. Without days, the
 * points are the document's rows in the document's own order -- a repo, model
 * or person breakdown has no axis the console can enumerate, and inventing one
 * would be drawing points nobody measured.
 */
export function buildSeries(
  rows: readonly SpendRow[],
  account: SpendAccount,
  days: readonly string[] | null,
): SpendSeries {
  const values: Plotted[] = []
  if (days === null) {
    for (const row of rows) values.push(plotted(row, account))
  } else {
    const byKey = new Map<string, SpendRow>()
    for (const row of rows) byKey.set(row.key, row)
    for (const day of days) {
      const row = byKey.get(day)
      if (row === undefined) {
        values.push(dayGap(day))
        continue
      }
      byKey.delete(day)
      values.push(plotted(row, account))
    }
    // A row the range does not cover is not plotted: on a day axis the
    // position IS the date, so a row before `since` appended after the last day
    // would sit right of `until`, and a row placed by its date would stretch
    // the axis past the range the document says it covers. The row is not
    // hidden -- it stays in the table below and `buildSpendModel` says how many
    // the chart left out (they are `SpendChart.outside`).
  }

  const points = values.map((value) => pointAt(value))
  return {
    account,
    title: ACCOUNT_TITLES[account],
    points,
    empty: points.length === 0 ? 'the document holds no row for this account' : null,
  }
}

/** withAccountState is a bar height or an axis top in the state of the account
 * it was drawn for, so the axis top drawn by a notional figure is notional and
 * the one drawn by an estimate is estimated -- one shared scale, and the state
 * of the account that set it. */
function withAccountState(value: number, account: SpendAccount): Reading {
  switch (account) {
    case 'model':
      return measured(value)
    case 'notional':
      return notional(value)
    case 'compute':
      return estimated(value)
  }
}

/** CHART_AXIS_REASON is the console's sentence for a chart with nothing to draw:
 * no account holds a value in the range, so there is no top to the axis -- and
 * the axis top is `n/a` with this reason rather than a `$0.00` nobody measured. */
const CHART_AXIS_REASON = 'no account has a value to draw in the range the document covers'

/** chartAxis is the chart's one shared axis: the largest value any account
 * draws, in the state of the account that drew it. It is a value the document
 * carries, and it is never a sum. */
function chartAxis(series: readonly SpendSeries[]): Reading {
  let best: { readonly value: number; readonly account: SpendAccount } | null = null
  for (const one of series) {
    for (const point of one.points) {
      const { reading } = point
      if (!plottable(reading) || reading.value === null) continue
      if (best === null || reading.value > best.value) best = { value: reading.value, account: one.account }
    }
  }
  return best === null ? unavailable(CHART_AXIS_REASON) : withAccountState(best.value, best.account)
}

/** barOf is one value as a bar on the chart's shared axis. A value the document
 * does not carry draws no bar: its share is a gap carrying the value's own
 * reason, which is what the drawing reads to leave the bar out and write the
 * value's `n/a` at its foot. */
function barOf(
  value: { readonly key: string; readonly reading: Reading; readonly partial: boolean },
  account: SpendAccount,
  axis: number | null,
): SpendBar {
  const { key, reading, partial } = value
  if (reading.value === null) {
    return { key, account, reading, share: gap(reading.because), partial, hollow: partial }
  }
  const share = axis === null || axis === 0 ? 0 : (reading.value / axis) * 100
  return { key, account, reading, share: measured(share), partial, hollow: partial }
}

/** groupOf is the series as one group per key. The three series share a key
 * space -- `buildSeries` enumerates the range's days for every account, and
 * walks the document's rows in the document's own order for a breakdown with no
 * axis -- so the keys are the first series' points and each account's bar for a
 * key is that series' point at the same index. */
function groupOf(series: readonly SpendSeries[], rows: readonly SpendRow[], axis: Reading): readonly SpendGroup[] {
  const first = series[0]
  if (first === undefined) return []
  const held = new Map<string, SpendRow>()
  for (const row of rows) held.set(row.key, row)
  const groups: SpendGroup[] = []
  for (let at = 0; at < first.points.length; at += 1) {
    const bars: SpendBar[] = []
    for (const one of series) {
      const point = one.points[at]
      if (point === undefined) continue
      bars.push(barOf(point, one.account, axis.value))
    }
    const key = first.points[at]?.key ?? ''
    const row = held.get(key) ?? null
    groups.push({
      key,
      // The history holds no document for a key of the enumerated range, and
      // that is the whole of what a gap is: a row the range does not cover is a
      // row (`outside`), not a gap.
      gap: row === null,
      partial: row === null ? bars.some((bar) => bar.partial) : row.partial,
      hollow: row?.hollow ?? false,
      unreadable: row?.unreadable ?? false,
      row,
      bars,
    })
  }
  return groups
}

/**
 * buildChart is the three series as the one chart the screen draws.
 *
 * `days` is the enumerated range when the document groups by day, and `null`
 * otherwise -- the same argument `buildSeries` takes, and the reason the rows a
 * range does not cover can be told from the days it does: with no axis there is
 * no outside.
 */
export function buildChart(
  series: readonly SpendSeries[],
  rows: readonly SpendRow[],
  days: readonly string[] | null,
): SpendChart {
  const axis = chartAxis(series)
  const groups = groupOf(series, rows, axis)
  const inside = new Set<string>()
  for (const group of groups) inside.add(group.key)
  const outside: SpendGroup[] = []
  if (days !== null) {
    for (const row of rows) {
      if (inside.has(row.key)) continue
      outside.push({
        key: row.key,
        gap: false,
        partial: row.partial,
        hollow: row.hollow,
        unreadable: row.unreadable,
        row,
        bars: SPEND_ACCOUNTS.map((account) => barOf(plotted(row, account), account, axis.value)),
      })
    }
  }
  return { axis, groups, outside }
}

// ---------------------------------------------------------------------------
// The caps panel.
// ---------------------------------------------------------------------------

/** capsOf reads `budget show --json`. A missing document is not an empty one:
 * `available: false` is a different fact from "this project has no caps", and
 * the panel draws the difference.
 *
 * It is exported because the caps are a **section of their own**: the entry
 * paints `fillSpendCaps` from the budget document the moment that read lands,
 * which can be long before the report's, so it needs the document turned into
 * the caps the section draws. `readSpend` calls it too, for the model it hands
 * `renderSpend`, and the two are the same reading of the same document. */
export function capsOf(raw: JSONDoc | null): SpendCaps {
  const document = raw === null ? null : objectAt(raw)
  if (document === null) {
    return {
      available: false,
      project: null,
      day: null,
      mode: null,
      reserve: unavailable('no budget show document was read'),
      global: null,
      defaults: null,
      repos: [],
      warnings: [],
    }
  }
  const global = objectAt(document['global'])
  const defaults = objectAt(document['defaults'])
  const mode = textAt(document['mode'])
  return {
    available: true,
    project: textAt(document['project']),
    day: textAt(document['day']),
    // `""` is the one mode only this document carries, and the CLI's own help
    // says what it means: unset, which acts as observe. Drawing the empty
    // string would be an empty cell; drawing `observe` would be inventing a
    // setting, so it is `null` and the screen says "unset" in the console's own
    // words.
    mode: mode === null || mode === '' ? null : mode,
    // The top-level field, read at the top level: `max_reserve_usd` is not
    // inside `global` (see `SpendGlobalCaps`), and reading it from there would
    // make the panel claim a project limit is a global cap.
    reserve: readNumber(document['max_reserve_usd'], 'max_reserve_usd'),
    global:
      global === null
        ? null
        : {
            dailyCap: readNumber(global['daily_cap_usd'], 'global.daily_cap_usd'),
            perRunCap: readNumber(global['per_run_cap_usd'], 'global.per_run_cap_usd'),
            counted: readNumber(global['counted_usd'], 'global.counted_usd'),
            spent: readNumber(global['spent_usd'], 'global.spent_usd'),
            notional: usdReading(global, 'notional_usd', 'notional'),
            headroom: readNumber(global['headroom_usd'], 'global.headroom_usd'),
            kill: killOf(global['kill']),
          },
    defaults:
      defaults === null
        ? null
        : {
            repoDailyCap: readNumber(defaults['repo_daily_cap_usd'], 'defaults.repo_daily_cap_usd'),
            repoPerRunCap: readNumber(defaults['repo_per_run_cap_usd'], 'defaults.repo_per_run_cap_usd'),
          },
    repos: arrayAt(document['repos']).map(capRowOf).filter((row): row is SpendCapRow => row !== null),
    warnings: arrayAt(document['warnings']).filter((warning): warning is string => typeof warning === 'string'),
  }
}

/** capRowOf reads one `showRepo`. */
function capRowOf(raw: unknown): SpendCapRow | null {
  const repo = objectAt(raw)
  if (repo === null) return null
  return {
    slug: textAt(repo['slug']) ?? '',
    repo: textAt(repo['repo']) ?? '',
    counted: readNumber(repo['counted_usd'], 'repos[].counted_usd'),
    spent: readNumber(repo['spent_usd'], 'repos[].spent_usd'),
    notional: usdReading(repo, 'notional_usd', 'notional'),
    compute: usdReading(repo, 'compute_usd', 'compute'),
    carriesCompute: Object.prototype.hasOwnProperty.call(repo, 'compute_usd'),
    headroom: readNumber(repo['headroom_usd'], 'repos[].headroom_usd'),
    dailyCap: readNumber(repo['daily_cap_usd'], 'repos[].daily_cap_usd'),
    dailyScope: scopeOf(textAt(repo['daily_cap_source']) ?? ''),
    perRunCap: readNumber(repo['per_run_cap_usd'], 'repos[].per_run_cap_usd'),
    perRunScope: scopeOf(textAt(repo['per_run_cap_source']) ?? ''),
    kill: killOf(repo['kill']),
  }
}

// ---------------------------------------------------------------------------
// The model.
// ---------------------------------------------------------------------------

/** PERSON_OFF_NOTE is the one-line reason the design asks for beside a view
 * that is off by default (`section 2.3`: "with a one-line note on what it
 * shows"). It is the console's own sentence, drawn as a literal. */
const PERSON_OFF_NOTE =
  'the person breakdown is off by default, and when turned on it is on for this session only and not remembered: these rows name the people whose runs spent the money'

/** PERSON_ON_NOTE is the same line for the view once the operator has turned it
 * on: what it shows, and that an address on the wire is drawn as the console's
 * own marker (`sanitize.ts`'s `person` class). */
const PERSON_ON_NOTE =
  'the person breakdown is on for this session only and not remembered (off by default): it names the people whose runs spent the money, and each address is drawn as the console’s own marker'

/** dimensionOf reads the document's `by`. */
function dimensionOf(raw: string | null): SpendDimension | null {
  for (const dimension of SPEND_DIMENSIONS) {
    if (dimension === raw) return dimension
  }
  return null
}

/**
 * keyClass is the text class a row's key carries, and it is the dimension that
 * decides it: `by repo`'s key is a repository path (or, when the record has no
 * path, the bucket slug `Build` falls back to), `by person`'s is a person,
 * `by model`'s is a model name, `by mode`'s is a mode word or the CLI's own
 * `none`, and a period's is a date. A label is the class for the ones that are
 * free text, and it is not a weak class: the free-text rules redact an address,
 * a URL, a path and a token out of a label exactly as they do out of a
 * sentence.
 */
export function keyClass(dimension: SpendDimension | null, key: string): TextClass {
  switch (dimension) {
    case 'repo':
      // `CanonicalRepo` writes `owner/name`; `Build`'s `ByRepo` branch falls
      // back to the slug when the record carries no path. The slash is the
      // difference, and the two classes are the two shapes the CLI writes.
      return key.includes('/') ? 'repository' : 'slug'
    case 'person':
      return 'person'
    case 'day':
    case 'week':
    case 'month':
    case 'year':
    case 'model':
    case 'mode':
    case null:
      return 'label'
  }
}

/**
 * buildSpendModel is the two documents as the screen's data.
 *
 * The order of the work is the order of the design's sentences: the range and
 * the breakdown, then the rows, then the three series the one grouped chart
 * draws (never one), then the totals the document itself reports, then the caps
 * panel with the binding scope named, and the person view decided last --
 * because it is the one thing that removes rows from everything above it.
 */
export function buildSpendModel(
  report: JSONDoc,
  budget: JSONDoc | null,
  options: SpendOptions = {},
): SpendModel {
  const personsEnabled = options.persons === true
  const source = textAt(report['source'])
  const since = textAt(report['since'])
  const until = textAt(report['until'])
  const dimension = dimensionOf(textAt(report['by']))
  const rawRows = report['rows']
  const rowsReadable = Array.isArray(rawRows)
  const rows = rowsReadable
    ? rawRows.map(rowOf).filter((row): row is SpendRow => row !== null)
    : []
  const withheld = dimension === 'person' && !personsEnabled
  const drawn = withheld ? [] : rows
  const days = dimension === 'day' ? dayRange(since, until) : null
  const series: readonly SpendSeries[] = SPEND_ACCOUNTS.map((account) =>
    withheld ? withheldSeries(account) : buildSeries(drawn, account, days),
  )
  const chart = buildChart(series, drawn, days)

  const notes: string[] = []
  if (withheld) notes.push(PERSON_OFF_NOTE)
  if (!rowsReadable) {
    notes.push('the document carries no rows array, so this screen has no figures to draw')
  } else if (hasGapDay(drawn, days)) {
    // The legend for the chart's gap columns: a reader who sees one is owed
    // the sentence that says what a missing day means. It is drawn when a day
    // the range covers has no row -- not merely when there are fewer rows than
    // days, because a row outside the range is a row, not a gap.
    notes.push('a day in this range with no document is a gap, not a zero: the history keeps a window and days outside it are pruned')
  }
  const stray = chart.outside.length
  if (stray > 0) {
    notes.push(`${stray} row(s) outside the range the document covers are listed below and not drawn in the chart: a day axis places a bar by its date`)
  }
  if (dimension === null) {
    notes.push('the document groups by a breakdown this console does not know, so the switch below cannot say which of its eight words it is')
  }

  return {
    project: textAt(report['project']) ?? '',
    source,
    degraded: boolAt(report['degraded']),
    notice: textAt(report['notice']),
    range: { since, until, weeks: textAt(report['weeks']) },
    dimension,
    dimensionKnown: dimension !== null,
    switches: SPEND_DIMENSIONS.map((entry): SpendSwitchEntry => ({
      dimension: entry,
      enabled: entry !== 'person' || personsEnabled,
      current: entry === dimension,
      note: entry === 'person' && !personsEnabled ? PERSON_OFF_NOTE : null,
    })),
    series,
    chart,
    totals: totalsOf(report['totals']),
    caps: capsOf(budget),
    persons: { enabled: personsEnabled, withheld },
    // The by-person panel exists only while the knob is on: with the knob off
    // there is no read to have made and no question about whose rows may be
    // drawn, so the model has no panel either rather than an empty one. With the
    // knob on it is built from whatever the read produced -- a document, or the
    // console's sentence for why there is none (`readSpend`).
    people: personsEnabled ? peopleOf(options.people ?? null, options.peopleReason ?? null) : null,
    warnings: arrayAt(report['warnings']).filter((warning): warning is string => typeof warning === 'string'),
    notes,
  }
}

/** hasGapDay says whether a day of the enumerated range has no row at all,
 * which is the fact the gap legend is about. `days` is `null` unless the
 * document groups by day and the range could be enumerated, and with no range
 * there is no day the console knows is missing. */
function hasGapDay(rows: readonly SpendRow[], days: readonly string[] | null): boolean {
  if (days === null) return false
  const held = new Set<string>()
  for (const row of rows) held.add(row.key)
  return days.some((day) => !held.has(day))
}

/** withheldSeries is one account's chart when the person rows are withheld:
 * three empty charts and the console's own sentence, and **no figure at all**
 * -- not a zero, not a marker standing in for one. */
function withheldSeries(account: SpendAccount): SpendSeries {
  return {
    account,
    title: ACCOUNT_TITLES[account],
    points: [],
    empty: PERSON_OFF_NOTE,
  }
}

// ---------------------------------------------------------------------------
// The controls.
// ---------------------------------------------------------------------------

/**
 * SPEND_PICKER_WINDOWS is the range picker's closed set of windows, in
 * `reportRange`'s own grammar (`ee/report/cli/report.go`): `Nd` is the last N
 * days up to `--until` and `Nw` the last N weeks, and every one of them is a
 * `--since` the CLI parses. It is the part of the picker that is *drawn*; a UTC
 * day is the picker's other form (a range with an end the operator chose),
 * which is why `SpendControls.since` is a string and this list is the closed set
 * the drawn windows come from.
 */
export const SPEND_PICKER_WINDOWS = Object.freeze(['7d', '30d', '90d', '12w'] as const)

/** SpendWindow is one window the picker offers. */
export type SpendWindow = (typeof SPEND_PICKER_WINDOWS)[number]

/**
 * SpendControls is the operator's view state, and the whole of what this screen
 * asks for: the range the picker holds, the breakdown the switch holds, and
 * whether the person view is on.
 *
 * It is the renderer's own state and it is **not** argv. `spendArguments` turns
 * it into the validated argument object and main builds the argv from that
 * (design section 5.1 rule 6), so no string in here can become a flag: the
 * values are checked against this screen's own closed sets first.
 */
export interface SpendControls {
  /** the picker's `--since`: one of `SPEND_PICKER_WINDOWS`, or a UTC day */
  readonly since: string
  /** `--until`: a UTC day, or `null` for `report`'s own today; no control sets it yet (the picker draws windows only) */
  readonly until: string | null
  /** the breakdown switch's `--by` */
  readonly by: SpendDimension
  /** the person view's own knob (design D15) */
  readonly persons: boolean
}

/**
 * SPEND_DEFAULT_CONTROLS is the screen as it opens, and none of the three
 * defaults is the console's invention: `30d` is `reportDefaultSince` and `day`
 * is `--by`'s own default, so the first read is the read an operator's own
 * `fugaro report --json` would have made, and the person view is off (D15).
 *
 * `until` is `null` on purpose. The CLI defaults it to today in UTC and this
 * screen cannot know what today is: a date it invented would be a day nobody
 * measured, drawn as if it had been asked for.
 */
export const SPEND_DEFAULT_CONTROLS: SpendControls = Object.freeze({
  since: '30d',
  until: null,
  by: 'day',
  persons: false,
})

/** SpendArguments is the argument object this screen sends: `spend.report`'s
 * own keys, strings only, and nothing else. */
export type SpendArguments = Readonly<Record<string, string>>

/** SpendArgumentDecision is `spendArguments`'s answer: the object to send, or
 * the console's sentence for why there is none. */
export type SpendArgumentDecision =
  | { readonly ok: true; readonly args: SpendArguments }
  | { readonly ok: false; readonly reason: string }

/** WINDOW_UNKNOWN, UNTIL_NOT_A_DAY, BREAKDOWN_UNKNOWN and PERSON_OFF_BECAUSE are
 * the console's own sentences for a control value it will not send. Each is the
 * console's words and **not** the value: an out-of-schema value is never echoed
 * back into a sentence, the rule the Live screen's unknown flags follow. */
const WINDOW_UNKNOWN =
  'the range picker asked for a window this console does not know: it offers 7d, 30d, 90d and 12w, or a UTC day'
const UNTIL_NOT_A_DAY = 'the range picker’s end of the range is not a UTC day (YYYY-MM-DD)'
const BREAKDOWN_UNKNOWN = 'the breakdown switch asked for a breakdown this console does not know'
const PERSON_OFF_BECAUSE =
  'the breakdown switch asked for the person breakdown, which is off by default, and on for this session only and not remembered, until the operator turns the person view on'

/** pickerSince is the picker's `since` as `report --since`, or `null` for a
 * value outside the picker's own two forms: a window of `SPEND_PICKER_WINDOWS`,
 * or a UTC day. */
function pickerSince(since: string): string | null {
  for (const window of SPEND_PICKER_WINDOWS) if (window === since) return since
  return dayNumber(since) === null ? null : since
}

/**
 * spendArguments is the one place a control becomes an argument object, and it
 * is **closed**: every value is checked against this screen's own sets before
 * anything is spawned, and a value outside them is refused here, in the
 * console's own words, rather than handed to the allow-list to refuse. The
 * allow-list is the second lock on the same door (`ipc.ts`'s `spend.report`),
 * not the first.
 *
 * The three fields are that operation's own: `since` a day or `Nd`/`Nw`,
 * `until` a day, `by` one of the eight words. `--repo` is admitted on this
 * command by the design's allow-list and is deliberately not here -- this screen
 * draws the whole project, and a field it does not draw is a field it must not
 * send. There is no `--csv`, no path, no free-form string, no command and no
 * URL: main builds the argv from this object (design section 5.1 rule 6).
 */
export function spendArguments(controls: SpendControls): SpendArgumentDecision {
  const since = pickerSince(controls.since)
  if (since === null) return { ok: false, reason: WINDOW_UNKNOWN }
  if (controls.until !== null && dayNumber(controls.until) === null) {
    return { ok: false, reason: UNTIL_NOT_A_DAY }
  }
  const by = dimensionOf(controls.by)
  if (by === null) return { ok: false, reason: BREAKDOWN_UNKNOWN }
  // The person breakdown is off until its own knob is on (D15), and that is
  // enforced here and not only in the drawing: a switch word the operator never
  // turned on must not become a read, however the state was reached.
  if (by === 'person' && !controls.persons) return { ok: false, reason: PERSON_OFF_BECAUSE }
  // `--until` is omitted rather than sent empty: absent is `report`'s own
  // today, and an empty string is not a day.
  const args: Record<string, string> =
    controls.until === null ? { by, since } : { by, since, until: controls.until }
  return { ok: true, args: Object.freeze(args) }
}

/** SpendReader is the one read this screen makes: `spend.report` through the
 * preload bridge's `runReport`, with a validated argument object and no argv. It
 * answers with the document, and refuses with a `SourceRefusal` carrying the
 * CLI's own sentence. */
export type SpendReader = (args: SpendArguments) => Promise<JSONDoc>

/**
 * PeopleReader is the screen's second read: `runs.stats` through the preload
 * bridge's `statsRuns`, with the argument object `peopleArguments` built. It is
 * the same *shape* as `SpendReader` -- two reads of this console's own
 * allow-list, neither carrying argv -- and a distinct type so that a call site
 * that wired the report's reader into the by-person panel would be a mistake the
 * compiler names.
 */
export type PeopleReader = (args: SpendArguments) => Promise<JSONDoc>

/** SpendOutcome is what one read produced: the model to draw, or the console's
 * sentence for why there is nothing to draw. `args` is what was asked for --
 * `null` when the controls themselves were refused and nothing was spawned. */
export type SpendOutcome =
  | { readonly ok: true; readonly args: SpendArguments; readonly model: SpendModel }
  | { readonly ok: false; readonly args: SpendArguments | null; readonly reason: string }

/** READ_REFUSED and READ_REFUSED_UNDRAWABLE are the two sentences for a read
 * that refused with nothing this screen can draw. */
const READ_REFUSED = 'the read was refused, and the CLI printed no sentence this console can draw'
const READ_REFUSED_UNDRAWABLE =
  'the CLI refused this read, and its own sentence is one this console will not draw'

/**
 * refusalSentence is the CLI's own sentence out of whatever the read threw,
 * through `sanitizeRefusal` -- the same treatment the refused-read screen gives
 * a refusal (design section 6.3: the console shows that text unedited, so the
 * credential rules and this layer's own apply, and never the pattern classes
 * that eat a role name).
 *
 * The sentence is read **structurally** (`sentence`) rather than by importing
 * `SourceRefusal`: `src/source/source.ts` is main's module, and this file is
 * bundled into the window, where main's modules must not be reachable at all.
 */
function refusalSentence(cause: unknown): string {
  const raw = (cause as { readonly sentence?: unknown } | null | undefined)?.sentence
  if (typeof raw !== 'string' || raw.trim() === '') return READ_REFUSED
  const drawn = sanitizeRefusal(raw, 'the reason the spend read refused')
  return drawn.ok ? drawn.text : READ_REFUSED_UNDRAWABLE
}

/**
 * readSpend makes the screen's one read and builds the model from what came
 * back. It is a function of its arguments and touches no element: the drawing is
 * `renderSpend`'s, and the caller decides when a control change re-reads.
 *
 * The budget document is the caller's, because it is `budget.show`'s read and a
 * different operation: a control change re-reads the report and re-draws the
 * caps panel from the document the screen already holds. Reading both here would
 * make one control change two spawns, and the second one about nothing the
 * operator changed.
 *
 * `people` is the by-person read (`runs.stats`, console L5b), and it is a
 * parameter of its own for the same reason: it is a *different operation*, and
 * it is made **only while the operator's knob is on** (design D15) -- so a
 * screen with the knob off spawns once, exactly as it did before this task. It
 * is optional and defaults to `null`, which is the state "this caller wired no
 * by-person read": the panel then draws the console's own sentence for that
 * (`NO_PEOPLE_READ`) rather than an empty list, and nothing is spawned.
 *
 * **The by-person read's refusal is the panel's and not the screen's.** The
 * report read is the screen's subject -- its refusal is `SpendOutcome`'s
 * `ok: false`, drawn by the caller (Task 3.2) -- while the by-person aggregate is
 * one panel of a screen that has already been drawn: its refusal, and a window
 * the command has no form for, become the panel's own sentence, and the rest of
 * the screen is drawn from the report exactly as it would have been.
 */
export async function readSpend(
  read: SpendReader,
  controls: SpendControls,
  caps: JSONDoc | null,
  people: PeopleReader | null = null,
): Promise<SpendOutcome> {
  const decision = spendArguments(controls)
  if (!decision.ok) return { ok: false, args: null, reason: decision.reason }
  let document: JSONDoc
  try {
    document = await read(decision.args)
  } catch (cause) {
    return { ok: false, args: decision.args, reason: refusalSentence(cause) }
  }
  let peopleDocument: JSONDoc | null = null
  let peopleReason: string | null = null
  if (controls.persons) {
    const peopleDecision = peopleArguments(controls)
    if (!peopleDecision.ok) {
      peopleReason = peopleDecision.reason
    } else if (people === null) {
      peopleReason = NO_PEOPLE_READ
    } else {
      try {
        peopleDocument = await people(peopleDecision.args)
      } catch (cause) {
        peopleReason = refusalSentence(cause)
      }
    }
  }
  return {
    ok: true,
    args: decision.args,
    model: buildSpendModel(document, caps, {
      persons: controls.persons,
      people: peopleDocument,
      peopleReason,
    }),
  }
}

/**
 * personView is the operator's own knob (design D15: the person breakdown is off
 * by default and turned on per installation; **this knob is per session only
 * and is not remembered**, because persisting it needs a write channel this
 * console does not have -- an open owner question). Turning it on
 * asks for the person breakdown -- the same `runReport` with `by person` and
 * nothing more -- and turning it off leaves no read that names a person: the
 * switch's own word goes back to `day`, because `--by person` with the view off
 * is a read `spendArguments` refuses.
 */
export function personView(controls: SpendControls, on: boolean): SpendControls {
  if (on) return Object.freeze({ ...controls, by: 'person', persons: true })
  return Object.freeze({
    ...controls,
    by: controls.by === 'person' ? 'day' : controls.by,
    persons: false,
  })
}

/**
 * PERSON_WINDOW_DAYS is the picker's four windows as `runs stats --since`, in
 * `parseSince`'s own grammar (`internal/cli/runs.go`: a positive day count, a Go
 * duration, or `0` for no bound) -- which has **no week form at all**. The
 * picker's `12w` therefore becomes `84d`, `reportRange`'s own arithmetic for a
 * week (`ee/report/cli/report.go`: the unit is 7 days, so 12w is 84 days), and
 * the three day windows go through unchanged.
 *
 * A `Map` and not a literal object, for the reason `SCOPES` is one, and the keys
 * are `SPEND_PICKER_WINDOWS`'s own words rather than the document's.
 *
 * **The two commands' windows are not the same range to the day, and this table
 * does not pretend they are.** `report --since 30d` covers 30 UTC days ending
 * with today (`from = to - 30 + 1`, `reportRange`), while `runs stats --since`
 * reads back from *now* (`from = until.Add(-since)`, `runUserStats`) and labels
 * the window with the date it reached -- so the by-person panel's window can be
 * a day wider at its start than the report's, and it is drawn with the document's
 * own `since`/`until` beside it rather than with the picker's word. What this
 * table does is send the operator's window **in the form the command understands**
 * and never widen or narrow it to make two commands agree.
 */
const PERSON_WINDOW_DAYS: ReadonlyMap<string, string> = new Map([
  ['7d', '7d'],
  ['30d', '30d'],
  ['90d', '90d'],
  ['12w', '84d'],
])

/** PERSON_DAY_WINDOW and PERSON_UNTIL are the console's two sentences for a
 * picker state `runs stats` has no form for. Both are refusals rather than
 * approximations: the console asks for the operator's window or for none, and a
 * window it silently altered would be drawn as the one that was asked for. */
const PERSON_DAY_WINDOW =
  'the range picker holds a range that starts on a UTC day, and the by-person read has no day form: it takes a number of days back from now (7d, 30d, 84d), so this window is either one of the picker’s four or no by-person read is made'
const PERSON_UNTIL =
  'the by-person read has no end-of-range form (runs stats takes no --until), and this range ends on a day: no by-person read is made, and this panel says so rather than drawing a window that is not this one'

/** personSince is the picker's `since` as `runs stats --since`, or `null` for a
 * value the command has no form for: a UTC day, or anything else outside
 * `PERSON_WINDOW_DAYS`. */
function personSince(since: string): string | null {
  return PERSON_WINDOW_DAYS.get(since) ?? null
}

/**
 * peopleArguments is the by-person read's argument object, and it is the whole
 * of what this screen may ask that command for: **`since`, and nothing else**.
 *
 * `runs stats`'s other flags are each a thing this console must not be able to
 * say (design section 5.1 rule 6, and the comment on the operation in
 * `src/main/ipc.ts`): `--all-users` would make the console the one deciding who
 * may see whose rows, `--by` is `user` and only `user`. `--repo` and `--all`
 * narrow the *project* and this screen draws the whole project, `--max-runs`
 * would let a control decide how much of the window is read, and `--until` does
 * not exist on this command at all -- which is why a picker that holds an end of
 * range is a refusal here and not a quietly different window.
 *
 * `--since 0` (no bound) is in the schema's grammar and is deliberately not
 * reachable from here: no control this screen draws means "the whole history",
 * and the one form that does (`''`) is not one `parseSince` accepts either.
 */
export function peopleArguments(controls: SpendControls): SpendArgumentDecision {
  if (controls.until !== null) return { ok: false, reason: PERSON_UNTIL }
  const since = personSince(controls.since)
  if (since === null) return { ok: false, reason: PERSON_DAY_WINDOW }
  return { ok: true, args: Object.freeze({ since }) }
}

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

/**
 * ScreenElement is what a screen may draw into: Task 2.0's `ElementLike` (a
 * text slot and attributes), a tag, children and `append`. No markup-parsing
 * slot, no navigating attribute, no event handler and no style -- a screen that
 * wanted one would have to widen this interface first, in a diff a reviewer can
 * see.
 *
 * It is declared here as well as in Task 2.1's `live.ts` rather than imported
 * from it, because the two screens are siblings and neither owns the other, and
 * because the file the interface belongs in (`renderer/app.ts`, which holds the
 * writers) is not this task's to edit. The two declarations are structurally
 * identical, and `spend.test.ts` asserts that in both directions: a factory
 * that draws one screen draws the other, so a drift is a failing test rather
 * than a screen that compiles and draws nothing. The day a third screen needs
 * them they move into `app.ts` and both import them.
 */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

/** ScreenFactory is where elements come from. It is an argument rather than a
 * `document` reference so the screen can be drawn, and asserted on, in a test
 * with no window at all. */
export interface ScreenFactory {
  create(tag: string): ScreenElement
}

/**
 * SpendFactory is a `ScreenFactory` that can also make an element in the **SVG**
 * namespace: the chart's bars, its gridlines and its axis are geometry, and an
 * element made with `create` is in the HTML namespace and is not drawn inside an
 * `svg` at all.
 *
 * It is a second interface rather than a method added to `ScreenFactory`, for
 * the reason `run.ts`'s `RunFactory` states: the two namespaces are two calls in
 * the DOM (`createElement` and `createElementNS`), and the cross-screen tests
 * hold the screens' factories assignable to each other -- a base factory that
 * had grown an SVG method would make that guard fail for a screen that draws no
 * SVG. `entry.ts`'s `DomScreenFactory` answers both.
 */
export interface SpendFactory extends ScreenFactory {
  createNS(tag: string): ScreenElement
}

/** makeNS is `make` in the SVG namespace, so the class is still written in the
 * one place `make` writes it. */
function makeNS(factory: SpendFactory, tag: string, ...classes: string[]): ScreenElement {
  return make({ create: (name) => factory.createNS(name) }, tag, ...classes)
}

/** make is a fresh element with its classes. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  return element
}

/** literal writes the console's own vocabulary. It is not `putText`: that is
 * the door a *document's* string comes through, and running the console's own
 * words through the sanitizer would invite a reader to think a screen may write
 * one without it. */
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

/** sectionSlot is a section's own read-state slot: the first child the entry
 * puts a read's busy mark into, and the element a cached view's stamp is drawn
 * into.
 *
 * **It is answered rather than made when the host already has one.** The shell
 * (`renderSpendFrame`) draws both slots before either read starts, so the entry
 * has somewhere to put a mark from the first frame; `renderSpend` fills hosts
 * that have none, which is what keeps the one-shot drawing the same tree.
 */
function sectionSlot(host: ScreenElement, factory: ScreenFactory): ScreenElement {
  const found = host
    .childNodes()
    .find((child) => (child.getAttribute('class') ?? '').split(/\s+/).includes(BUSY_SLOT_CLASS))
  if (found !== undefined) return found
  const slot = make(factory, 'div', BUSY_SLOT_CLASS)
  host.append(slot)
  return slot
}

/** textCell is `cell` with a document's string written into it through
 * `putText`, with the class the wire key declares. */
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

/** numberCell is `cell` with a reading written through `putReading`, so no
 * number can be drawn without its state. */
function numberCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  reading: Reading,
  unit: Parameters<typeof putReading>[2],
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, 'spend-num', ...classes)
  putReading(element, reading, unit)
  return element
}

/** drawnKey is a document string used as an **attribute value** -- a row's key
 * on a point or a caps row, which a test and a later screen need to find. It is
 * read back off the cell that drew it, so the attribute carries what the screen
 * drew and not the raw value `putText` may have refused: an attribute echoing
 * the raw string would put back on screen exactly what the refusal kept off it.
 * (Live's connection `data-state` is the same rule, learned the same way.) */
function drawnKey(element: ScreenElement): string {
  return element.textContent ?? ''
}

/** killBanner is "KILLED by <who> <at> <reason>", the same shape the Live
 * screen draws -- a kill is a fact about a repository or about the project, and
 * it reads the same on both screens. */
function killBanner(factory: ScreenFactory, kill: KillView): ScreenElement {
  const element = make(factory, 'div', 'spend-kill')
  element.setAttribute('data-killed', 'true')
  literal(cell(element, factory, 'span', 'spend-kill-word'), 'KILLED by')
  textCell(element, factory, 'span', kill.by, 'person', 'kill.by', 'spend-kill-by')
  textCell(element, factory, 'span', kill.at, 'label', 'kill.at', 'spend-kill-at')
  textCell(element, factory, 'span', kill.reason, 'sentence', 'kill.reason', 'spend-kill-reason')
  return element
}

/** CHART_AXIS_WORD is the axis line's own word: the figure beside it is the
 * axis top, and the gridlines under it are shares of that one figure. */
const CHART_AXIS_WORD = 'axis'

/** GAP_COLUMN_WORD labels the one column a day the history does not hold draws:
 * a gap, said in words, so the hatch is never read as a measurement. */
const GAP_COLUMN_WORD = 'no record'

/** SPEND_CHART_SIZE is the chart's own drawing unit: the `viewBox` is
 * `0 0 100 100` and `preserveAspectRatio="none"` stretches it to the panel, so a
 * bar's width and its height are shares and never pixels (the shape `live.ts`'s
 * fleet bar uses). */
const SPEND_CHART_SIZE = 100

/** SPEND_GRIDLINE_SHARES are the shares of the axis the gridlines are drawn at,
 * the top one being the axis itself. They are the drawing's geometry: the axis
 * top is the one figure, and it is written once, beside the top gridline. */
const SPEND_GRIDLINE_SHARES = Object.freeze([25, 50, 75, 100])

/** SPEND_GROUP_INSET is the share of a group's own width left empty on each
 * side, so two neighbouring groups are not read as one bar of six columns. */
const SPEND_GROUP_INSET = 0.15

/** SPEND_BAR_INSET is the share of one bar's column left empty between it and
 * its neighbour in the same group: a key's three bars are siblings standing side
 * by side, and that gap is what says they are not stacked. */
const SPEND_BAR_INSET = 0.1

/** PERSON_SWITCH_WORD is the person knob's own word. It is the console's, not
 * the document's: the breakdown switch's `person` word is the CLI's, and this
 * one names the operator's decision about the by-person view. */
const PERSON_SWITCH_WORD = 'person breakdown'

/** TableGapSentence is the one cell a day the history does not hold draws across
 * the money columns: three `n/a`s would read as three measurements, and there is
 * no document at all behind the row. */
const TABLE_GAP_SENTENCE = 'No record for this day. A gap, not a zero.'

/** BarShape is one `rect`'s four lengths, in the drawing's units. */
interface BarShape {
  readonly x: string
  readonly y: string
  readonly width: string
  readonly height: string
}

/** chartUnits is a length in the chart's drawing units, rounded so an attribute
 * is a short string rather than a float's whole expansion. */
function chartUnits(value: number): string {
  return String(Math.round(value * 100) / 100)
}

/** barShape is the geometry of one bar: the `column`th bar of `columns` in the
 * `at`th group of `count`, `share` percent of the axis tall. `columns` is how
 * many bars the group holds -- the three accounts, or one for a day the history
 * does not hold -- so the same function draws a bar and a gap's own column. */
function barShape(at: number, count: number, column: number, columns: number, share: number): BarShape {
  const group = SPEND_CHART_SIZE / count
  const inner = group * (1 - 2 * SPEND_GROUP_INSET)
  const columnWidth = inner / columns
  const width = columns === 1 ? inner : columnWidth * (1 - SPEND_BAR_INSET)
  const x = at * group + group * SPEND_GROUP_INSET + (columns === 1 ? 0 : column * columnWidth)
  const height = (share / 100) * SPEND_CHART_SIZE
  return {
    x: chartUnits(x),
    y: chartUnits(SPEND_CHART_SIZE - height),
    width: chartUnits(width),
    height: chartUnits(height),
  }
}

/** gridline is one horizontal line of the grid, at `share` percent of the axis.
 * It carries no figure of its own: the axis top is the one figure, and a label
 * on every line would be four numbers saying three times what the top says. */
function gridline(factory: SpendFactory, share: number): ScreenElement {
  const element = makeNS(factory, 'line', 'spend-gridline')
  element.setAttribute('x1', '0')
  element.setAttribute('x2', '100')
  element.setAttribute('y1', chartUnits(SPEND_CHART_SIZE - (share / 100) * SPEND_CHART_SIZE))
  element.setAttribute('y2', chartUnits(SPEND_CHART_SIZE - (share / 100) * SPEND_CHART_SIZE))
  return element
}

/** axisLine is the baseline the bars stand on. It is drawn after them so it sits
 * over their feet rather than under them. */
function axisLine(factory: SpendFactory): ScreenElement {
  const element = makeNS(factory, 'line', 'spend-axis-line')
  element.setAttribute('x1', '0')
  element.setAttribute('x2', '100')
  element.setAttribute('y1', '100')
  element.setAttribute('y2', '100')
  return element
}

/** barRect is one bar: its four lengths in the drawing's units, the account it
 * draws, the reading's own state, and the **drawn** key it belongs to, so a test
 * can find a key's bars as siblings of one group. */
function barRect(factory: SpendFactory, bar: SpendBar, shape: BarShape, key: string): ScreenElement {
  const element = makeNS(factory, 'rect', 'spend-bar')
  element.setAttribute('x', shape.x)
  element.setAttribute('y', shape.y)
  element.setAttribute('width', shape.width)
  element.setAttribute('height', shape.height)
  element.setAttribute('data-key', key)
  element.setAttribute('data-account', bar.account)
  element.setAttribute('data-state', bar.reading.state)
  element.setAttribute('data-partial', bar.partial ? 'true' : 'false')
  element.setAttribute('data-hollow', bar.hollow ? 'true' : 'false')
  element.setAttribute('data-height', formatReading(bar.share, 'percent'))
  return element
}

/** gapColumn is the one column a day the history does not hold draws: the full
 * height of the plot, hatched. It is one column and not three bars of no height,
 * because a bar of no height reads as a measurement of zero. */
function gapColumn(factory: SpendFactory, column: BarShape, key: string): ScreenElement {
  const element = makeNS(factory, 'rect', 'spend-gap-column')
  element.setAttribute('x', column.x)
  element.setAttribute('y', column.y)
  element.setAttribute('width', column.width)
  element.setAttribute('height', column.height)
  element.setAttribute('data-key', key)
  return element
}

/** seriesBars is one account's bars, in one `g`: the group carries the account
 * so a stylesheet paints a bar by the money it draws, and a test reads the three
 * accounts apart without knowing the order they were drawn in. */
function seriesBars(
  factory: SpendFactory,
  account: SpendAccount,
  groups: readonly SpendGroup[],
  keys: readonly string[],
): ScreenElement {
  const element = makeNS(factory, 'g', 'spend-series')
  element.setAttribute('data-account', account)
  for (let at = 0; at < groups.length; at += 1) {
    const group = groups[at]
    if (group === undefined || group.gap) continue
    const bar = group.bars.find((one) => one.account === account)
    if (bar === undefined || bar.share.value === null) continue
    const shape = barShape(at, groups.length, SPEND_ACCOUNTS.indexOf(account), SPEND_ACCOUNTS.length, bar.share.value)
    element.append(barRect(factory, bar, shape, keys[at] ?? ''))
  }
  return element
}

/**
 * chartKey is one key of the chart's label row, under the group's own bars: the
 * document's key through `putText`, the marks a gap and an `n/a` account need,
 * and the `(partial)` marker. It returns the **drawn** key, which is what the
 * bars and the gap column carry, so an attribute never echoes a string `putText`
 * refused.
 */
function chartKey(parent: ScreenElement, factory: ScreenFactory, model: SpendModel, group: SpendGroup): string {
  const element = cell(parent, factory, 'li', 'spend-chart-key')
  element.setAttribute('data-gap', group.gap ? 'true' : 'false')
  element.setAttribute('data-partial', group.partial ? 'true' : 'false')
  const keyed = textCell(element, factory, 'span', group.key, keyClass(model.dimension, group.key), 'rows[].key', 'spend-key')
  element.setAttribute('data-key', drawnKey(keyed))
  if (group.gap) {
    literal(cell(element, factory, 'span', 'spend-chart-gap'), GAP_COLUMN_WORD)
  } else {
    for (const missing of group.bars) {
      if (missing.reading.value !== null) continue
      // A value the document does not carry draws no bar, and its `n/a` is
      // written here, at the bar's own foot.
      const none = numberCell(element, factory, 'span', missing.reading, 'usd', 'spend-chart-na')
      none.setAttribute('data-account', missing.account)
    }
  }
  if (group.partial) literal(cell(element, factory, 'span', 'spend-partial'), PARTIAL_MARKER)
  return drawnKey(keyed)
}

/** legendRow is one row of the legend: the account's own word and the swatch a
 * stylesheet paints (solid, hatched or dashed) that the word beside it names. An
 * account with nothing to draw carries the console's sentence for it instead of
 * a sample, so a legend is never a swatch with no figure behind it. */
function legendRow(parent: ScreenElement, factory: ScreenFactory, series: SpendSeries): void {
  const element = cell(parent, factory, 'li', 'spend-legend-key')
  element.setAttribute('data-account', series.account)
  const swatch = cell(element, factory, 'span', 'spend-swatch')
  swatch.setAttribute('data-account', series.account)
  literal(cell(element, factory, 'span', 'spend-legend-word'), series.title)
  if (series.empty !== null) literal(cell(element, factory, 'span', 'spend-legend-empty'), series.empty)
}

/**
 * chart draws the one grouped chart: every key's three accounts as three bars
 * standing side by side on one shared axis.
 *
 * **Grouped, never stacked.** The three bars of a key are siblings, and each
 * one's height is its own account's share of the axis top -- the largest value
 * any account draws. Nothing is summed: a stack would draw billed + notional as
 * one column, which is the one figure the design forbids, and an axis topped by
 * that sum would be a figure no document holds.
 *
 * The `svg` is `aria-hidden`: it is geometry, and every value it draws is a
 * reading in the table below, a key in the label row or a word in the legend, so
 * a reader with a screen reader loses no figure and no word.
 */
function chart(factory: SpendFactory, model: SpendModel): ScreenElement {
  const element = make(factory, 'div', 'spend-chart')
  const top = cell(element, factory, 'div', 'spend-axis')
  literal(cell(top, factory, 'span', 'spend-axis-word'), CHART_AXIS_WORD)
  numberCell(top, factory, 'span', model.chart.axis, 'usd', 'spend-axis-top')

  const svgElement = makeNS(factory, 'svg', 'spend-chart-svg')
  svgElement.setAttribute('viewBox', '0 0 100 100')
  svgElement.setAttribute('preserveAspectRatio', 'none')
  svgElement.setAttribute('height', '100')
  svgElement.setAttribute('aria-hidden', 'true')
  element.append(svgElement)

  const groups = model.chart.groups
  const labels = cell(element, factory, 'ol', 'spend-chart-keys')
  const keys: string[] = []
  for (const group of groups) keys.push(chartKey(labels, factory, model, group))

  // An axis with no value has no scale, so it has no gridlines either: the axis
  // top says `n/a` and the plot is left empty rather than drawn as a scale of
  // nothing.
  if (model.chart.axis.value !== null) {
    for (const share of SPEND_GRIDLINE_SHARES) svgElement.append(gridline(factory, share))
  }
  const gaps = makeNS(factory, 'g', 'spend-gaps')
  for (let at = 0; at < groups.length; at += 1) {
    const group = groups[at]
    if (group === undefined || !group.gap) continue
    gaps.append(gapColumn(factory, barShape(at, groups.length, 0, 1, 100), keys[at] ?? ''))
  }
  svgElement.append(gaps)
  for (const account of SPEND_ACCOUNTS) svgElement.append(seriesBars(factory, account, groups, keys))
  svgElement.append(axisLine(factory))

  const legend = cell(element, factory, 'ul', 'spend-legend')
  for (const one of model.series) legendRow(legend, factory, one)
  return element
}

/**
 * header draws the project line: the project's own name, and beside it the range
 * that came back, the week convention the document declares and the source.
 *
 * The heading is the **project** and not the word "Spend": the tab above the
 * screen already says which view this is, and the title row is where the
 * project's name belongs.
 */
function header(factory: ScreenFactory, model: SpendModel): ScreenElement {
  const element = make(factory, 'header', 'spend-header')
  const heading = cell(element, factory, 'h1', 'spend-project')
  putText(heading, model.project, 'project', 'project')
  if (model.source !== null) textCell(element, factory, 'span', model.source, 'label', 'source', 'spend-source')
  if (model.range.since !== null) textCell(element, factory, 'span', model.range.since, 'label', 'since', 'spend-since')
  // The arrow between the two ends of the range is the console's own mark; the
  // two days are the document's strings and go through `textCell`.
  if (model.range.since !== null && model.range.until !== null) {
    literal(cell(element, factory, 'span', 'spend-range-arrow'), '→')
  }
  if (model.range.until !== null) textCell(element, factory, 'span', model.range.until, 'label', 'until', 'spend-until')
  if (model.range.weeks !== null) {
    // The document declares the week convention rather than leaving it to a
    // reader to infer (`weeks: "iso-utc-monday"`), and a week row means
    // different days under a different one, so it is drawn.
    textCell(element, factory, 'span', model.range.weeks, 'label', 'weeks', 'spend-weeks')
  }
  if (model.degraded) {
    const flag = cell(element, factory, 'span', 'spend-degraded')
    literal(flag, 'degraded')
    flag.setAttribute('data-degraded', 'true')
  }
  if (model.notice !== null) {
    textCell(element, factory, 'p', model.notice, 'sentence', 'notice', 'spend-notice')
  }
  return element
}

/**
 * windowItem is one window of the range picker: a real `button` whose whole text
 * is the window's own word (the console's own vocabulary for `reportRange`'s
 * grammar), pressed when it is the window the operator chose.
 *
 * It is a button and not a cell: the picker is a segmented control, and a click
 * is the whole of what it does. `entry.ts` reads the window's word off this
 * element's `textContent` and nothing else about it.
 */
function windowItem(parent: ScreenElement, factory: ScreenFactory, current: boolean): ScreenElement {
  const item = cell(parent, factory, 'li', 'spend-item')
  const button = cell(item, factory, 'button', 'spend-window')
  button.setAttribute('type', 'button')
  button.setAttribute('aria-pressed', current ? 'true' : 'false')
  button.setAttribute('data-current', current ? 'true' : 'false')
  return button
}

/**
 * picker draws the range picker: `report --since`'s windows as the console's own
 * words, with the one the operator chose pressed, and whether the range's end is
 * the operator's own day rather than `report`'s today.
 *
 * What it draws is the **request**; the header draws the range that came back,
 * which is the document's own `since`/`until`. The two are different statements
 * and the screen makes both: a picker that drew only what came back could not
 * say what was asked for, and a range whose days the history does not hold would
 * read as a range nobody chose.
 *
 * Every word is a literal, because they are the console's vocabulary and not a
 * document's strings -- the same rule the breakdown switch's eight words follow.
 */
function picker(factory: ScreenFactory, controls: SpendControls): ScreenElement {
  const element = make(factory, 'ol', 'spend-picker')
  const week = controls.since === '7d'
  const month = controls.since === '30d'
  const quarter = controls.since === '90d'
  const twelveWeeks = controls.since === '12w'
  literal(windowItem(element, factory, week), '7d')
  literal(windowItem(element, factory, month), '30d')
  literal(windowItem(element, factory, quarter), '90d')
  literal(windowItem(element, factory, twelveWeeks), '12w')
  // A range the operator named as a day is not one of these windows, and the
  // picker says so rather than pressing the nearest one.
  const day = !week && !month && !quarter && !twelveWeeks
  element.setAttribute('data-day', day ? 'true' : 'false')
  // `--until` is the CLI's own today unless the operator set an end. Which of
  // the two it is, is a fact about the request; the day itself is the header's.
  const end = controls.until !== null
  element.setAttribute('data-until', end ? 'set' : 'cli')
  return element
}

/**
 * personKnob draws the person view's own switch (design D15): a real `button`
 * whose word names the view and whose state says whether it is on, and the
 * one-line note the design asks for beside a view that is off by default.
 *
 * It is a control of its own rather than a state of the breakdown switch: the
 * switch's `person` word is one of the eight the CLI has, and the operator's
 * decision about it is a different thing from the breakdown the screen is
 * currently drawn by. The knob's own `data-enabled` is what the event wiring
 * reads, and the switch's `person` word is drawn disabled until it is on.
 *
 * The note is a sibling of the button rather than a child: a button holds
 * phrasing content, and the note is a sentence a reader must be able to select
 * and a screen reader must be able to reach without pressing the switch.
 */
function personKnob(parent: ScreenElement, factory: ScreenFactory, model: SpendModel): void {
  const on = model.persons.enabled
  const element = cell(parent, factory, 'button', 'spend-persons')
  element.setAttribute('type', 'button')
  element.setAttribute('data-enabled', on ? 'true' : 'false')
  element.setAttribute('aria-pressed', on ? 'true' : 'false')
  literal(cell(element, factory, 'span', 'spend-persons-word'), PERSON_SWITCH_WORD)
  literal(cell(element, factory, 'span', 'spend-persons-state'), on ? 'on' : 'off')
  const note = cell(parent, factory, 'p', 'spend-persons-note')
  if (on) literal(note, PERSON_ON_NOTE)
  else literal(note, PERSON_OFF_NOTE)
}

/**
 * tabs draws the breakdown switch: the eight words as a segmented control, with
 * the document's own grouping pressed and the words this console may not show
 * drawn as plain cells that say why in their `title`.
 *
 * A disabled word is a `span` and not a disabled button: a control that cannot
 * be pressed is not a control, and the words the design lists are still all
 * drawn -- a reader sees the shape of the CLI's vocabulary and which part of it
 * this console may use.
 */
function tabs(factory: ScreenFactory, model: SpendModel): ScreenElement {
  const element = make(factory, 'ol', 'spend-switch')
  element.setAttribute('data-known', model.dimensionKnown ? 'true' : 'false')
  for (const entry of model.switches) {
    const item = cell(element, factory, 'li', 'spend-item')
    if (entry.enabled) {
      const button = cell(item, factory, 'button', 'spend-dim')
      button.setAttribute('type', 'button')
      button.setAttribute('aria-pressed', entry.current ? 'true' : 'false')
      button.setAttribute('data-dimension', entry.dimension)
      button.setAttribute('data-enabled', 'true')
      button.setAttribute('data-current', entry.current ? 'true' : 'false')
      // The eight words are the CLI's own (`ParseDim`'s help line) and this is
      // the console drawing its own vocabulary for them, not a document's
      // string.
      literal(button, entry.dimension)
      continue
    }
    const off = cell(item, factory, 'span', 'spend-dim')
    off.setAttribute('data-dimension', entry.dimension)
    off.setAttribute('data-enabled', 'false')
    off.setAttribute('data-current', entry.current ? 'true' : 'false')
    literal(off, entry.dimension)
    if (entry.note !== null) off.setAttribute('title', entry.note)
  }
  if (!model.dimensionKnown) {
    // A ninth word is a newer CLI. The switch says so in the console's own
    // words rather than drawing the word it does not know -- the value is not
    // echoed, the same rule the Live screen's unknown flags follow.
    const unknown = cell(element, factory, 'li', 'spend-dim', 'spend-dim-unknown')
    literal(unknown, 'an unknown breakdown')
    unknown.setAttribute('data-dimension', 'unknown')
    unknown.setAttribute('data-enabled', 'false')
    unknown.setAttribute('data-current', 'true')
  }
  return element
}

/** readingOf is one account's reading for a group: what the document said, or a
 * gap carrying the day's own reason when the group holds no bar for it. */
function readingOf(group: SpendGroup, account: SpendAccount): Reading {
  const bar = group.bars.find((one) => one.account === account)
  return bar === undefined ? gap(NO_DOCUMENT_DAY) : bar.reading
}

/** moneyCells are a row's three money cells, in the order the headings name
 * them: each account's own reading, in its own state. */
function moneyCells(parent: ScreenElement, factory: ScreenFactory, group: SpendGroup): void {
  numberCell(parent, factory, 'td', readingOf(group, 'model'), 'usd', 'spend-model')
  numberCell(parent, factory, 'td', readingOf(group, 'notional'), 'usd', 'spend-notional')
  numberCell(parent, factory, 'td', readingOf(group, 'compute'), 'usd', 'spend-compute')
}

/** tokenReading is one of a period's two token counts. A day the history does
 * not hold draws the em dash its runs and runtime draw; a document that tracks
 * no tokens at all (`TokensTracked`; `tokens` is `omitempty` on the wire, so the
 * object is absent entirely) draws `n/a` with the console's own reason, because
 * "this document does not count tokens" and "this period used none" are two
 * different facts and a blank cell would read as the second. */
function tokenReading(group: SpendGroup, which: 'in' | 'out'): Reading {
  const tokens = group.row?.tokens ?? null
  if (group.row === null) return gap(NO_DOCUMENT_DAY)
  if (tokens === null) return unavailable(NO_TOKENS_REASON)
  return which === 'in' ? tokens.in : tokens.out
}

/** periodRow draws one period of the table: its key (with the one `(partial)`
 * and the unreadable marker the design asks for), the three accounts, the runs,
 * the runtime and the two token counts -- or one cell across the money columns
 * for a day the history does not hold. */
function periodRow(factory: ScreenFactory, model: SpendModel, group: SpendGroup): ScreenElement {
  const element = make(factory, 'tr', 'spend-row')
  element.setAttribute('data-gap', group.gap ? 'true' : 'false')
  element.setAttribute('data-partial', group.partial ? 'true' : 'false')
  element.setAttribute('data-hollow', group.hollow ? 'true' : 'false')
  element.setAttribute('data-unreadable', group.unreadable ? 'true' : 'false')
  const keyed = textCell(element, factory, 'td', group.key, keyClass(model.dimension, group.key), 'rows[].key', 'spend-key')
  element.setAttribute('data-key', drawnKey(keyed))
  if (group.partial) literal(cell(keyed, factory, 'span', 'spend-partial'), PARTIAL_MARKER)
  if (group.unreadable) literal(cell(keyed, factory, 'span', 'spend-unreadable'), UNREADABLE_MARKER)
  if (group.gap) {
    const across = cell(element, factory, 'td', 'spend-gap-cell')
    across.setAttribute('colspan', GAP_CELL_SPAN)
    literal(across, TABLE_GAP_SENTENCE)
  } else {
    moneyCells(element, factory, group)
  }
  numberCell(element, factory, 'td', group.row?.runs ?? gap(NO_DOCUMENT_DAY), 'count', 'spend-runs')
  numberCell(element, factory, 'td', group.row?.runHours ?? gap(NO_DOCUMENT_DAY), 'hours', 'spend-run-hours')
  numberCell(element, factory, 'td', tokenReading(group, 'in'), 'count', 'spend-tokens-in')
  numberCell(element, factory, 'td', tokenReading(group, 'out'), 'count', 'spend-tokens-out')
  return element
}

/**
 * spendTable draws the periods as a table: one row per key of the chart, and
 * then the rows the range does not cover -- the chart leaves those out and the
 * note above says how many there are.
 *
 * The runs, runtime and token columns are not money and are never summed with
 * it; a figure the document does not carry is the reading's own `n/a`, never a
 * `$0.00`. The token counts are drawn even when a document tracks none, because
 * a table's columns are its rows' shape: the cell says `n/a` with the console's
 * reason rather than leaving a hole a reader would take for a zero.
 */
function spendTable(factory: ScreenFactory, model: SpendModel): ScreenElement {
  const element = make(factory, 'table', 'spend-table')
  const head = cell(element, factory, 'thead', 'spend-table-head')
  const headRow = cell(head, factory, 'tr', 'spend-table-row')
  literal(cell(headRow, factory, 'th', 'spend-th'), 'key')
  literal(cell(headRow, factory, 'th', 'spend-th'), 'billed model')
  literal(cell(headRow, factory, 'th', 'spend-th'), 'notional (list price)')
  literal(cell(headRow, factory, 'th', 'spend-th'), 'compute (estimated)')
  literal(cell(headRow, factory, 'th', 'spend-th'), 'runs')
  literal(cell(headRow, factory, 'th', 'spend-th'), 'runtime')
  literal(cell(headRow, factory, 'th', 'spend-th'), 'tokens in')
  literal(cell(headRow, factory, 'th', 'spend-th'), 'tokens out')
  const body = cell(element, factory, 'tbody', 'spend-table-body')
  for (const group of model.chart.groups) body.append(periodRow(factory, model, group))
  for (const group of model.chart.outside) body.append(periodRow(factory, model, group))
  return element
}

/** totalsLine draws the document's own totals: the four cells the design names,
 * and the one line that says what the money in them is.
 *
 * Nothing here is computed by the console -- the totals are the document's own
 * -- and the four cells are four figures, never a fifth that added two of them.
 */
function totalsLine(factory: ScreenFactory, model: SpendModel): ScreenElement {
  const element = make(factory, 'div', 'spend-totals')
  element.setAttribute('data-partial', model.totals.partial ? 'true' : 'false')
  element.setAttribute('data-unreadable', model.totals.unreadable ? 'true' : 'false')
  const billed = cell(element, factory, 'div', 'spend-total')
  literal(cell(billed, factory, 'span', 'spend-totals-word'), 'billed model')
  numberCell(billed, factory, 'span', model.totals.model, 'usd', 'spend-total-model')
  const notional = cell(element, factory, 'div', 'spend-total')
  literal(cell(notional, factory, 'span', 'spend-totals-word'), 'notional (list price)')
  numberCell(notional, factory, 'span', model.totals.notional, 'usd', 'spend-total-notional')
  const compute = cell(element, factory, 'div', 'spend-total')
  literal(cell(compute, factory, 'span', 'spend-totals-word'), 'compute (estimated)')
  numberCell(compute, factory, 'span', model.totals.compute, 'usd', 'spend-total-compute')
  const runs = cell(element, factory, 'div', 'spend-total')
  literal(cell(runs, factory, 'span', 'spend-totals-word'), 'runs · runtime')
  numberCell(runs, factory, 'span', model.totals.runs, 'count', 'spend-total-runs')
  numberCell(runs, factory, 'span', model.totals.runHours, 'hours', 'spend-total-run-hours')
  const note = cell(element, factory, 'p', 'spend-totals-note')
  // "Today is partial." is drawn only when the document says the range includes
  // a partial day: it is a fact about this range, not a permanent label.
  if (model.totals.partial) literal(cell(note, factory, 'span', 'spend-totals-partial'), 'Today is partial.')
  literal(
    cell(note, factory, 'span', 'spend-totals-rule'),
    'Billed and notional are different kinds of money, so they are never added together.',
  )
  if (model.totals.unreadable) literal(cell(note, factory, 'span', 'spend-unreadable'), UNREADABLE_MARKER)
  return element
}

/** capScopeCell is `capFigure` for a cap: the same labelled figure, with the
 * scope that binds it beside it. The scope is a word this console chose from the
 * document's own closed set, and an unknown one is drawn as an explicit unknown
 * with its sentence in the title. */
function capScopeCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  word: string,
  reading: Reading,
  scope: SpendScope,
  attribute: string,
  ...classes: string[]
): ScreenElement {
  const element = capFigure(parent, factory, word, reading, ...classes)
  const named = cell(element, factory, 'span', 'spend-scope')
  literal(named, scope.name)
  named.setAttribute(attribute, scope.known ? scope.source : 'unknown')
  named.setAttribute('data-scope-known', scope.known ? 'true' : 'false')
  if (!scope.known) {
    named.setAttribute('title', 'the document names a cap scope this console does not know, so it cannot say which scope binds')
  }
  return element
}

/** capFigure is one labelled figure of a caps row: the console's own word for
 * what the number is, and the number in its own state beside it. */
function capFigure(
  parent: ScreenElement,
  factory: ScreenFactory,
  word: string,
  reading: Reading,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, 'div', 'spend-cap-figure')
  literal(cell(element, factory, 'span', 'spend-cap-word'), word)
  numberCell(element, factory, 'span', reading, 'usd', ...classes)
  return element
}

/** capsBarUnits is a share of the cap as a length on the bar's hundred-unit
 * box. A repository past its cap draws a **full** bar and says `at cap` beside
 * it: a bar longer than its own track would draw a share of the cap larger than
 * the cap, which is not a figure this document holds. */
function capsBarUnits(share: number): string {
  return chartUnits(Math.min(1, share) * CAPS_BAR_UNITS)
}

/** capsAtCap is whether a repository is at its limit: either the headroom the
 * document reports is a **measured** value of zero or less (the CLI measures it
 * under the repository's own cap and the project-wide one, so a repository the
 * project's cap is holding back is at its limit too), or the counted dollars are
 * at or past the cap the row carries. Both grounds are figures the row draws, so
 * the word is never beside numbers that contradict it -- the first needs no cap
 * of the repository's own and therefore no bar. An unavailable headroom is not a
 * zero: a row the document reports no headroom for, under a cap its counted
 * dollars have not reached, draws neither the word nor the tone. */
function capsAtCap(row: SpendCapRow): boolean {
  if (row.headroom.value !== null && row.headroom.value <= 0) return true
  const cap = row.dailyCap.value
  const counted = row.counted.value
  return cap !== null && counted !== null && counted >= cap
}

/** capsBar draws a repository's daily-cap bar: the CLI's own `counted_usd`
 * against `daily_cap_usd`, as `aria-hidden` geometry -- the two figures it draws
 * are written as text beside it, and the words `at cap` are written when the cap
 * is reached, so the drawing loses no reader a fact.
 *
 * The row's `headroom` is written beside them too, because the word `at cap` is
 * measured from it and not from the bar: the CLI reports the room left under
 * whichever cap binds, the repository's own or the project-wide one, so a
 * repository the global cap is holding back is at its limit with its counted
 * dollars still **under** its own cap -- and, if nobody set that cap, with no bar
 * at all. A state whose figure is off-screen is a state the reader cannot check.
 *
 * **The figure the bar draws is `counted_usd` and nothing else.** `counted_usd`
 * is what the daily cap is enforced against (`budget.Counters.Counted`: the
 * money reserved by runs and not yet released, a ceiling on spend), so it is the
 * CLI's own answer to "how much of the cap is used". The renderer adds nothing:
 * billed and notional are never summed, with each other or with compute, and the
 * design's "billed + compute" is not a figure this document holds. A row the
 * document gives no cap for draws no bar at all -- a bar of no length would read
 * as a measurement of zero -- and its cap is drawn `n/a` with the reason. */
function capsBar(parent: ScreenElement, factory: SpendFactory, row: SpendCapRow, atCap: boolean): ScreenElement {
  const element = cell(parent, factory, 'div', 'spend-cap-bar')
  const cap = row.dailyCap.value
  const counted = row.counted.value
  if (cap !== null && cap > 0 && counted !== null) {
    const svgElement = makeNS(factory, 'svg', 'spend-cap-bar-svg')
    svgElement.setAttribute('viewBox', '0 0 100 100')
    svgElement.setAttribute('preserveAspectRatio', 'none')
    svgElement.setAttribute('aria-hidden', 'true')
    const track = makeNS(factory, 'rect', 'spend-cap-bar-track')
    track.setAttribute('x', '0')
    track.setAttribute('y', '0')
    track.setAttribute('width', '100')
    track.setAttribute('height', '100')
    const filled = makeNS(factory, 'rect', 'spend-cap-bar-fill')
    filled.setAttribute('x', '0')
    filled.setAttribute('y', '0')
    filled.setAttribute('width', capsBarUnits(counted / cap))
    filled.setAttribute('height', '100')
    svgElement.append(track)
    svgElement.append(filled)
    element.append(svgElement)
  }
  capFigure(element, factory, CAPS_COUNTED_WORD, row.counted, 'spend-cap-counted')
  capScopeCell(element, factory, CAPS_DAILY_CAP_WORD, row.dailyCap, row.dailyScope, 'data-daily-scope', 'spend-cap-daily')
  capFigure(element, factory, CAPS_HEADROOM_WORD, row.headroom, 'spend-cap-headroom')
  if (atCap) literal(cell(element, factory, 'span', 'spend-cap-at-cap'), CAPS_AT_CAP_WORD)
  return element
}

/**
 * fillSpendCaps draws what binds today into the caps section's own element: the
 * console's own heading, the mode as a chip, the largest single lease, the
 * repository defaults, the global row and one row per repository -- each figure
 * under its own word, each cap beside the scope that binds it, and each
 * repository's counted dollars drawn against its daily cap.
 *
 * **It fills a host rather than building one, and it answers the busy slot it
 * drew first.** The caps are one *section* of the Spend screen (the owner's rule
 * of 2026-10-31): its read is `budget show`, it starts together with the
 * report's, and it paints the moment its own answer lands -- which is a patch of
 * this element and of nothing else. The slot is the element the entry puts a
 * read's busy mark into, and it is drawn first so the mark is at the top of the
 * section rather than under a table nobody scrolls to.
 */
export function fillSpendCaps(
  factory: SpendFactory,
  caps: SpendCaps,
  element: ScreenElement,
): ScreenElement {
  const slot = sectionSlot(element, factory)
  element.setAttribute('data-available', caps.available ? 'true' : 'false')
  if (!caps.available) {
    // The one thing this panel must not do is draw an empty one: a cap of $0
    // and a cap nobody read are different facts, and the promises the design
    // makes about caps are promises about the second.
    const none = cell(element, factory, 'p', 'spend-note')
    literal(none, 'no budget show document was read, so this console does not know the caps: every cap here is n/a, not zero')
    return slot
  }
  literal(cell(element, factory, 'h2', 'spend-caps-title'), CAPS_TITLE)
  if (caps.project !== null) {
    textCell(element, factory, 'span', caps.project, 'project', 'caps.project', 'spend-caps-project')
  }
  if (caps.day !== null) textCell(element, factory, 'span', caps.day, 'label', 'caps.day', 'spend-caps-day')
  if (caps.mode !== null) {
    textCell(element, factory, 'span', caps.mode, 'label', 'caps.mode', 'spend-caps-mode')
  } else {
    const unset = cell(element, factory, 'span', 'spend-caps-mode')
    literal(unset, 'mode unset (acts as observe)')
  }
  // The largest single lease, drawn outside the block whose word is `global`
  // and above it -- the CLI prints it above the `project` block too. A
  // project-level limit inside a global block would read as a global cap, and
  // this panel's whole job is to say which scope each figure belongs to.
  const reserve = make(factory, 'div', 'spend-cap-limit')
  reserve.setAttribute('data-limit', 'max_reserve')
  literal(cell(reserve, factory, 'span', 'spend-cap-word'), RESERVE_LABEL)
  numberCell(reserve, factory, 'span', caps.reserve, 'usd', 'spend-cap-reserve')
  element.append(reserve)
  if (caps.defaults !== null) {
    const defaults = make(factory, 'div', 'spend-cap-defaults')
    literal(cell(defaults, factory, 'span', 'spend-cap-word'), 'repository defaults')
    capFigure(defaults, factory, CAPS_DAILY_CAP_WORD, caps.defaults.repoDailyCap, 'spend-cap-daily')
    capFigure(defaults, factory, CAPS_PER_RUN_CAP_WORD, caps.defaults.repoPerRunCap, 'spend-cap-per-run')
    element.append(defaults)
  }
  if (caps.global !== null) {
    // The global row's six figures, each under its own word: the two caps, the
    // counted dollars the caps are measured against, the billed and notional
    // accounts (drawn apart, never summed) and the room left under the caps.
    // `budget show --json` also carries `leased_usd` here (counted minus spent,
    // floored at 0); this console reads no such key, so it draws none.
    const global = make(factory, 'div', 'spend-cap-global')
    literal(cell(global, factory, 'span', 'spend-cap-word'), 'global')
    capFigure(global, factory, CAPS_DAILY_CAP_WORD, caps.global.dailyCap, 'spend-cap-daily')
    capFigure(global, factory, CAPS_PER_RUN_CAP_WORD, caps.global.perRunCap, 'spend-cap-per-run')
    capFigure(global, factory, CAPS_COUNTED_WORD, caps.global.counted, 'spend-cap-counted')
    capFigure(global, factory, CAPS_BILLED_WORD, caps.global.spent, 'spend-cap-spent')
    capFigure(global, factory, CAPS_NOTIONAL_WORD, caps.global.notional, 'spend-cap-notional')
    capFigure(global, factory, CAPS_HEADROOM_WORD, caps.global.headroom, 'spend-cap-headroom')
    if (caps.global.kill !== null) global.append(killBanner(factory, caps.global.kill))
    element.append(global)
  }
  const repos = cell(element, factory, 'ol', 'spend-cap-repos')
  // One row carrying the key is the whole document's answer: the key is
  // flag-conditioned and the CLI writes it on every repository or on none.
  const showsCompute = caps.repos.some((row) => row.carriesCompute)
  for (const row of caps.repos) {
    const item = cell(repos, factory, 'li', 'spend-cap-repo')
    const name = textCell(item, factory, 'span', row.repo === '' ? row.slug : row.repo, keyClass('repo', row.repo === '' ? row.slug : row.repo), 'repos[].repo', 'spend-cap-repo-name')
    item.setAttribute('data-key', drawnKey(name))
    const atCap = capsAtCap(row)
    item.setAttribute('data-at-cap', atCap ? 'true' : 'false')
    // The three money accounts of the repository, each in its own cell and each
    // with its own word: billed (what was charged), notional (a subscription's
    // list price) and compute (an estimate) are never added to one another.
    capFigure(item, factory, CAPS_BILLED_WORD, row.spent, 'spend-cap-spent')
    capFigure(item, factory, CAPS_NOTIONAL_WORD, row.notional, 'spend-cap-notional')
    // Compute is drawn only for a document that was read with the flag, because
    // an `n/a` cell on every row of a read that never asked for the figure says
    // nothing a person can act on; a row that carries the key as `null` is `n/a`
    // and never `$0.00`.
    if (showsCompute) capFigure(item, factory, CAPS_COMPUTE_WORD, row.compute, 'spend-cap-compute')
    capsBar(item, factory, row, atCap)
    capScopeCell(item, factory, CAPS_PER_RUN_CAP_WORD, row.perRunCap, row.perRunScope, 'data-per-run-scope', 'spend-cap-per-run')
    if (row.kill !== null) item.append(killBanner(factory, row.kill))
  }
  for (const warning of caps.warnings) {
    // The CLI's own sentences, drawn verbatim (contract section 3: "CLI-worded,
    // shown verbatim").
    textCell(element, factory, 'p', warning, 'sentence', 'warnings[]', 'spend-warning')
  }
  return slot
}

/** personAmounts draws the columns a person's row and a currency's total line
 * share: the currency the figures are in, the counts that add up to the runs
 * (published, not derived, so a reader can add the column and get the row), the
 * two money accounts in their own states and **never summed**, and the average
 * with the sample it is over.
 *
 * The `usd` figures are the document's own `counted_usd`/`notional_usd` -- the
 * CLI's rounding of the integer of record -- and not a division done here; the
 * model read them as readings, so one the document does not carry draws as n/a
 * with its reason rather than as `$0.00`. */
function personAmounts(factory: ScreenFactory, element: ScreenElement, row: SpendPersonRow): void {
  const currency = textCell(
    element,
    factory,
    'span',
    row.currency,
    'label',
    'rows[].currency',
    'spend-person-currency',
  )
  // Read back off the cell `putText` wrote, like every other attribute of a
  // document string on this screen: the attribute carries what was drawn.
  element.setAttribute('data-currency', drawnKey(currency))
  numberCell(element, factory, 'span', row.runs, 'count', 'spend-person-runs')
  numberCell(element, factory, 'span', row.succeeded, 'count', 'spend-person-succeeded')
  numberCell(element, factory, 'span', row.failed, 'count', 'spend-person-failed')
  numberCell(element, factory, 'span', row.halted, 'count', 'spend-person-halted')
  numberCell(element, factory, 'span', row.infraError, 'count', 'spend-person-infra')
  numberCell(element, factory, 'span', row.other, 'count', 'spend-person-other')
  numberCell(element, factory, 'span', row.noRecord, 'count', 'spend-person-no-record')
  numberCell(element, factory, 'span', row.counted, 'usd', 'spend-person-counted')
  numberCell(element, factory, 'span', row.notional, 'usd', 'spend-person-notional')
  numberCell(element, factory, 'span', row.countedRuns, 'count', 'spend-person-counted-runs')
  numberCell(element, factory, 'span', row.average, 'usd', 'spend-person-average')
}

/** personLine is one launcher's row: the identity the run's own record carried,
 * and the amounts above. The identity is drawn through `putText` in the `person`
 * class -- it is whatever a `launch.json` recorded, so an address in it is drawn
 * as the console's own marker -- and `data-user` is read back off that same cell,
 * so the attribute cannot put back on screen what the class kept off it. */
function personLine(factory: ScreenFactory, row: SpendPersonRow): ScreenElement {
  const element = make(factory, 'li', 'spend-person')
  const keyed = textCell(
    element,
    factory,
    'span',
    row.user,
    'person',
    'rows[].user',
    'spend-person-user',
  )
  element.setAttribute('data-user', drawnKey(keyed))
  personAmounts(factory, element, row)
  return element
}

/** NO_PEOPLE_TOTAL is the console's sentence for a document that published no
 * totals line at all: the panel's rows are drawn from `rows`, and a total is a
 * figure the document publishes rather than one this screen adds up (adding one
 * currency's rows is what the CLI does, and a second sum here could differ from
 * it). It is a real state of the CLI's own, not only a hostile document: the
 * private view the CLI neither keyed nor filled (`you` empty, or the
 * unattributed key) publishes `totals: []` because its `totals` are its `rows`,
 * and a licensed private view over a window the caller started nothing in
 * publishes `[]` too. The sentence says only that, and never that the read had
 * a window total it withheld. */
const NO_PEOPLE_TOTAL =
  'the document carries no totals line, so this console draws no total here: a total is the CLI’s own sum of the rows this read published, and this screen does not compute a second one'

/**
 * peoplePanel draws the by-person view: `runs stats --json` as its own panel
 * beside the report's rows (console L5b).
 *
 * The panel's whole job is to say **who the rows are for** before it draws them,
 * and the fail-private rule is `SpendPeople`'s: no document, an unknown `by` or
 * `view`, or a private view with no identity to key a row by, all draw **no row
 * at all**. A panel that drew an empty list in those states would say "nobody
 * spent anything", which is a different fact from "the console will not show you
 * whose money this was". The totals are the ones the document published and
 * never a sum computed here: they are drawn in every state that carries them,
 * and the two the CLI itself publishes none in (its `you` empty or the
 * unattributed key, where it deletes every run before aggregating) draw
 * `NO_PEOPLE_TOTAL` -- which says exactly that, and so never contradicts the
 * fail-private sentence above it.
 *
 * It is drawn only while the operator's knob is on (`renderSpend`): with the
 * knob off there is no read and no panel, which is D15's rule and not a state of
 * this function.
 */
function peoplePanel(factory: ScreenFactory, people: SpendPeople): ScreenElement {
  const element = make(factory, 'section', 'spend-people')
  element.setAttribute('data-available', people.available ? 'true' : 'false')
  element.setAttribute('data-view', people.view)
  element.setAttribute('data-operator', people.operator ? 'true' : 'false')
  element.setAttribute('data-truncated', people.truncated ? 'true' : 'false')
  literal(cell(element, factory, 'h2', 'spend-people-title'), 'by person')
  if (!people.available) {
    // The one thing this panel must not do is draw an empty one: "nobody spent
    // anything" and "the console did not read it" are two facts, and the
    // sentence says which -- the CLI's own refusal (through `sanitizeRefusal`,
    // the same door `degraded.ts` draws a refusal through) or the console's own
    // reason for having made no read.
    const none = cell(element, factory, 'p', 'spend-people-reason')
    literal(none, people.reason ?? NO_PEOPLE_READ)
    return element
  }
  // The view is the CLI's own ruling as the console's name for it -- a table
  // this file wrote (`PEOPLE_VIEW_NAMES`) and never the document's word.
  literal(cell(element, factory, 'span', 'spend-people-view'), people.viewName)
  // The identity the read was resolved for. `''` is *unavailable* -- the CLI
  // could not tell who the caller is -- and it is drawn as that sentence rather
  // than as an empty cell, which would read as anonymous.
  const you = cell(element, factory, 'span', 'spend-people-you')
  if (people.you === '') literal(you, PEOPLE_YOU_UNIDENTIFIED)
  else putText(you, people.you, 'person', 'you')
  const operator = cell(element, factory, 'span', 'spend-people-operator')
  if (people.operator) literal(operator, PERSON_OPERATOR_YES)
  else literal(operator, PERSON_OPERATOR_NO)
  if (people.since !== null) {
    textCell(element, factory, 'span', people.since, 'label', 'since', 'spend-people-since')
  }
  if (people.until !== null) {
    textCell(element, factory, 'span', people.until, 'label', 'until', 'spend-people-until')
  }
  // The read's own bound is a published figure and not a note: it is what says
  // how much of the window the numbers are over.
  literal(cell(element, factory, 'span', 'spend-people-bound'), 'max runs read')
  numberCell(element, factory, 'span', people.maxRuns, 'count', 'spend-people-max-runs')
  for (const sentence of people.sentences) {
    // Every one of these is the console's own sentence, composed in `peopleOf`
    // from this file's constants and the counts it read -- the document's own
    // `notes` are drawn below, through `putText`.
    literal(cell(element, factory, 'p', 'spend-people-note'), sentence)
  }
  const rows = cell(element, factory, 'ol', 'spend-people-rows')
  for (const row of people.rows) rows.append(personLine(factory, row))
  const totals = cell(element, factory, 'ol', 'spend-people-totals')
  for (const total of people.totals) {
    const line = cell(totals, factory, 'li', 'spend-person', 'spend-person-total')
    // The document's own totals key is `""` (`userstats` writes the empty user
    // for a currency's total), so the word here is the console's.
    literal(cell(line, factory, 'span', 'spend-person-user'), 'total')
    personAmounts(factory, line, total)
  }
  if (people.totals.length === 0) {
    const none = cell(element, factory, 'p', 'spend-person-note')
    literal(none, NO_PEOPLE_TOTAL)
  }
  // The window's runs that never started: a published count in no row and no
  // total, drawn where `PEOPLE_UNLAUNCHED` says it is.
  literal(cell(element, factory, 'span', 'spend-people-unlaunched-word'), PEOPLE_UNLAUNCHED_WORD)
  numberCell(element, factory, 'span', people.unlaunched, 'count', 'spend-people-unlaunched')
  for (const note of people.notes) {
    // The document's own lines, verbatim (contract section 6.10: the notes are
    // the CLI's sentences about whose numbers these are and what the read left
    // out), through `putText` and never as literals.
    textCell(element, factory, 'p', note, 'sentence', 'notes[]', 'spend-person-note')
  }
  return element
}

/**
 * renderSpend draws the model and returns the tree it built, so a test can walk
 * what was drawn rather than read a window no test can see.
 *
 * `controls` is the operator's view state, and it is what the picker and the
 * person knob draw; the model is what came back from the read those controls
 * asked for. A caller that has no view state (a static render, a test of the
 * drawing alone) gets `SPEND_DEFAULT_CONTROLS`, which is the state the screen
 * opens on.
 *
 * The by-person panel is drawn when the model carries one, which is exactly when
 * the operator's knob is on: with the knob off there is no panel, no by-person
 * read and no figure at all rather than a panel of empty ones.
 *
 * Every string a document wrote reaches the document through `putText` and
 * every number through `putReading`; the only thing written directly is the
 * console's own vocabulary, through `literal`. No string is parsed as markup,
 * nothing is written into the navigating attribute, and this screen offers no
 * link at all.
 */
export function renderSpend(
  factory: SpendFactory,
  model: SpendModel,
  theme: SpendTheme,
  controls: SpendControls = SPEND_DEFAULT_CONTROLS,
): ScreenElement {
  const frame = renderSpendFrame(factory, theme)
  fillSpendReport(factory, model, theme, controls, frame.report)
  fillSpendCaps(factory, model.caps, frame.caps)
  return frame.root
}

/** SPEND_REPORT_CLASS is the report section's own element: everything the
 * `report --json` read draws -- the title and the range it came back with, the
 * three controls, the totals, the chart, the table and the by-person panel --
 * and nothing the caps read draws. */
export const SPEND_REPORT_CLASS = 'spend-report'

/** DrawnSpendFrame is the Spend screen's shell: the two sections' elements and
 * the slot each one puts a read's busy mark into.
 *
 * **The shell is drawn once and patched per section**, which is the owner's rule
 * of 2026-10-31 in this screen's own terms: `budget show` and the report start
 * together, and each section is filled the moment *its* answer lands rather than
 * the whole screen being replaced when both have. A section whose read has not
 * answered yet holds its slot and nothing else. */
export interface DrawnSpendFrame {
  readonly root: ScreenElement
  readonly report: ScreenElement
  readonly reportSlot: ScreenElement
  readonly caps: ScreenElement
  readonly capsSlot: ScreenElement
}

/**
 * renderSpendFrame draws the Spend shell: one element per section and no figure
 * at all. It is what the entry mounts the moment the first of the two reads
 * lands, and `renderSpend` is this frame with both sections filled.
 *
 * **The two slots are drawn here and not by the fillers**, so the entry has
 * somewhere to put a section's busy mark or a cached view's stamp from the
 * first frame on: a section that is still being read holds its slot and nothing
 * else, and the mark is at the top of the section rather than under a table
 * nobody scrolls to.
 */
export function renderSpendFrame(factory: SpendFactory, theme: SpendTheme): DrawnSpendFrame {
  const root = make(factory, 'section', 'spend')
  root.setAttribute('data-theme', theme)
  const report = cell(root, factory, 'div', SPEND_REPORT_CLASS)
  const reportSlot = cell(report, factory, 'div', BUSY_SLOT_CLASS)
  const caps = cell(root, factory, 'section', 'spend-caps')
  const capsSlot = cell(caps, factory, 'div', BUSY_SLOT_CLASS)
  return { root, report, reportSlot, caps, capsSlot }
}

/**
 * fillSpendReport draws the report section into its own element, and answers the
 * busy slot it drew first.
 *
 * The title and the range are the **document's** (`header`), so they arrive with
 * the report and not with the shell: a title drawn from the controls would name
 * a range the history may not hold, which is the one thing this screen's two
 * range statements exist to keep apart.
 */
export function fillSpendReport(
  factory: SpendFactory,
  model: SpendModel,
  theme: SpendTheme,
  controls: SpendControls,
  element: ScreenElement,
): ScreenElement {
  const slot = sectionSlot(element, factory)
  element.append(header(factory, model))

  // The controls and their two one-line notes sit in one row under the title:
  // the picker and the breakdown switch side by side, and the person switch's
  // own note beside it.
  const controlsRow = cell(element, factory, 'div', 'spend-controls')
  controlsRow.append(picker(factory, controls))
  controlsRow.append(tabs(factory, model))
  const peopleRow = cell(controlsRow, factory, 'div', 'spend-knob')
  personKnob(peopleRow, factory, model)

  element.append(totalsLine(factory, model))
  element.append(chart(factory, model))
  element.append(spendTable(factory, model))

  // The by-person panel sits beside the report's own totals -- it is the same
  // window read a second way -- and it is drawn only while the operator's knob
  // is on: `buildSpendModel` has no panel when it is off (design D15).
  if (model.people !== null) element.append(peoplePanel(factory, model.people))

  for (const note of model.notes) {
    const drawn = cell(element, factory, 'p', 'spend-note')
    literal(drawn, note)
  }
  for (const warning of model.warnings) {
    textCell(element, factory, 'p', warning, 'sentence', 'warnings[]', 'spend-warning')
  }
  return slot
}
