// The Spend screen: the design's screen 2, drawn from `report --json` and
// `budget show --json` (desktop-console plan Tasks 2.2 and 2.4; design sections
// 6.1 rule 1, 6.2 screen 2 and 5.1 rule 6; console/docs/json-contract.md
// sections 3 and 4).
//
// The file has two halves, separated the way Task 2.1's Live screen separates
// them: `buildSpendModel` turns the two documents into rows, three series and a
// caps panel and touches no element at all, and `renderSpend` walks that model
// through Task 2.0's writers. The part with the arithmetic in it is testable in
// plain Node, which is where a wrong dollar figure is cheapest to catch.
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
// rule: model dollars, `NOTIONAL~` (a subscription's list price, never billed,
// never capped) and compute (an estimate by design) are three accounts, and a
// screen that adds them is drawing a number nobody can be billed for. So the
// model carries them as **three readings** (`model`, `notional`, `compute`),
// each in its own availability state, and as **three series with three axes**
// -- `SPEND_ACCOUNTS` is that list, the document's own `totals` line is read the
// same way rather than recomputed, and there is no function here that adds one
// account to another. `spend.test.ts` holds it: a mutation that sums them fails
// a named test rather than a review.
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
// **The controls are wired, and they are wired to one read -- on this screen's
// side of the seam.** The range picker, the breakdown switch and the operator's
// person knob (D15) all feed `SpendControls`, and `spendArguments` turns that
// state into the argument object `spend.report` already declares --
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
// it.
//
// **Nothing in the application calls them yet, and this file says so rather
// than implying otherwise.** There is no renderer entry point in this tree: no
// shell, no router, no event handler, and `index.html` is still Task 1.1's
// script-free document, so `spendArguments`, `readSpend` and `personView` are a
// library with no caller outside the tests, and the `data-` hooks below are
// what that entry point will read. That is the seam this task was given: its
// Files hold no shell, and the plan's Files are what a task may change. What is
// finished here is the screen's half -- the arguments a control produces, the
// read they make, and the state they draw -- so the entry point a later task
// writes has nothing left to decide about the Spend screen's controls.
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
  seriesSegments,
  unavailable,
} from '../../shared/availability'
import type { Reading, Sample } from '../../shared/availability'
import type { KillView } from '../../shared/schema'
import { sanitizeRefusal } from '../../shared/sanitize'
import type { TextClass } from '../../shared/sanitize'
import type { JSONDoc } from '../../source/source'
import { putReading, putText } from '../app'
import type { ElementLike } from '../app'

// ---------------------------------------------------------------------------
// The vocabulary.
// ---------------------------------------------------------------------------

/**
 * SPEND_DIMENSIONS is `report --by`'s closed set of seven words, in
 * `internal/report/report.go`'s own order (`ByDay`, `ByWeek`, `ByMonth`,
 * `ByYear`, `ByRepo`, `ByModel`, `ByPerson`, and `ParseDim`'s list). The switch
 * draws this list; a document's `by` that is not one of them is a document from
 * a CLI this build does not know, and the screen says so rather than drawing an
 * eighth word.
 */
export const SPEND_DIMENSIONS = Object.freeze([
  'day',
  'week',
  'month',
  'year',
  'repo',
  'model',
  'person',
] as const)

/** SpendDimension is one of the seven. */
export type SpendDimension = (typeof SPEND_DIMENSIONS)[number]

/**
 * SPEND_ACCOUNTS is the three accounts the design keeps apart, in the order the
 * screen draws them: the billed model dollars, the notional `NOTIONAL~` of a
 * subscription, and compute (an estimate by design). It is a list rather than
 * three fields so that "there are exactly three charts" is a fact a test can
 * read off it.
 */
export const SPEND_ACCOUNTS = Object.freeze(['model', 'notional', 'compute'] as const)

/** SpendAccount is one of them. */
export type SpendAccount = (typeof SPEND_ACCOUNTS)[number]

/**
 * SPEND_MAX_RANGE_DAYS is the longest range this console will enumerate a day
 * series over: `reportMaxDays` (`internal/cli/report.go:40`), which the CLI
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
 * CLI's own word: `Table` in `internal/report/render.go` appends `" (partial)"`
 * to the key of a partial row, and the console says the same word about the
 * same fact rather than inventing a second one for it.
 */
export const PARTIAL_MARKER = '(partial)'

/**
 * RESERVE_LABEL is the words the caps panel puts beside `max_reserve_usd`. They
 * are the CLI's own words for the same fact: `printShow` in
 * `internal/cli/budget.go` writes "largest single lease (max reserve): %s" on a
 * line of its own, **above** the `project` block, because it is a limit on one
 * lease (`budget.Limits.MaxReserveMicros`) and not a cap. The panel keeps both
 * halves of that: the CLI's words, and a place outside the `global` block.
 */
export const RESERVE_LABEL = 'largest single lease (max reserve)'

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
  /** the height the bar is drawn at, as a share of the series' own peak */
  readonly height: Reading
  readonly partial: boolean
  readonly hollow: boolean
}

/**
 * SpendSeries is one account's chart: the points, how many segments a line is
 * drawn in (a point that is not plottable **breaks** the series --
 * `seriesSegments`), the account's own peak as the axis top, and the console's
 * sentence for a series with nothing in it.
 *
 * The peak is per account and never shared: a notional bar scaled against the
 * billed model's peak would be a comparison the design's first rule forbids, and
 * three accounts on three axes is the same statement as three readings.
 */
export interface SpendSeries {
  readonly account: SpendAccount
  /** the console's own heading for the account */
  readonly title: string
  readonly points: readonly SpendPoint[]
  readonly segments: number
  readonly peak: Reading
  /** why there is nothing to draw, or `null` when there is */
  readonly empty: string | null
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
}

/** SpendPersons says whether the person breakdown is on, and whether the
 * document's rows are being withheld because it is not. */
export interface SpendPersons {
  readonly enabled: boolean
  /** true when the document groups by person and the view is off, so the rows
   * exist and are deliberately not drawn */
  readonly withheld: boolean
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
  readonly rows: readonly SpendRow[]
  readonly series: readonly SpendSeries[]
  readonly totals: SpendTotals
  readonly caps: SpendCaps
  readonly persons: SpendPersons
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
 */
function microsReading(line: Record<string, unknown>, key: string, account: SpendAccount): Reading {
  const micros = readNumber(line[key], key)
  if (micros.value === null) return micros
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
  return {
    key: textAt(line['key']) ?? '',
    start: textAt(line['start']),
    end: textAt(line['end']),
    partial,
    hollow: partial,
    model: microsReading(line, 'model_micros', 'model'),
    notional: microsReading(line, 'notional_micros', 'notional'),
    compute: microsReading(line, 'compute_micros', 'compute'),
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
  return {
    model: microsReading(line, 'model_micros', 'model'),
    notional: microsReading(line, 'notional_micros', 'notional'),
    compute: microsReading(line, 'compute_micros', 'compute'),
    runHours: readNumber(line['run_hours'], 'run_hours'),
    runs: countReading(line, 'runs'),
    partial: boolAt(line['partial']),
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
 * notional one carries the contract's own mark, because that is the word the
 * design uses for the account and the one a reader has to recognise on sight. */
const ACCOUNT_TITLES: Readonly<Record<SpendAccount, string>> = Object.freeze({
  model: 'billed model spend',
  notional: 'NOTIONAL~ subscription list price',
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
 * nothing. It is the reading's reason, and `pointAt` carries the same sentence
 * into the point's height, because the height of a gap is the same fact.
 */
const NO_DOCUMENT_DAY =
  'the history holds no document for this day: it is outside the retained window, or it was never written'

/** dayGap is one day inside the range that the history does not hold, as a
 * value for the series: the reading is a **gap** in both senses the screen
 * draws -- the em dash where a figure would be, and the break in the line --
 * and `pointAt` gives it that state again as its height. */
function dayGap(day: string): Plotted {
  return { at: day, key: day, reading: gap(NO_DOCUMENT_DAY), partial: false }
}

/** pointAt is one value once the axis has a top: the reading, whether the row
 * it came from is partial, and its height as a share of the series' own peak. */
function pointAt(value: Plotted, peak: number): SpendPoint {
  const { at, key, reading, partial } = value
  if (reading.value === null) {
    // A point that is not plottable is a gap: the mark, no height, and the
    // reason travels with it so the tooltip says which fact it is.
    return { at, key, reading, height: gap(reading.because), partial, hollow: partial }
  }
  const share = peak === 0 ? 0 : (reading.value / peak) * 100
  return { at, key, reading, height: measured(share), partial, hollow: partial }
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
    // hidden -- it stays in the list below and `buildSpendModel` says how many
    // the chart left out (`outsideRange`).
  }

  const measuredValues: number[] = []
  for (const value of values) {
    if (plottable(value.reading) && value.reading.value !== null) measuredValues.push(value.reading.value)
  }
  const peakValue = measuredValues.length === 0 ? 0 : Math.max(...measuredValues)
  const points = values.map((value) => pointAt(value, peakValue))
  const peak =
    measuredValues.length === 0
      ? unavailable('this account has no value in the range the document covers')
      : withAccountState(peakValue, account)
  const samples: Sample[] = points.map((point) => ({ at: point.at, reading: point.reading }))
  return {
    account,
    title: ACCOUNT_TITLES[account],
    points,
    segments: seriesSegments(samples).length,
    peak,
    empty: points.length === 0 ? 'the document holds no row for this account' : null,
  }
}

/** withAccountState is `peakValue` in the account's own state, so the axis top
 * of the notional chart is notional and the one of the compute chart is
 * estimated -- three accounts, three axes, never one shared scale. */
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

// ---------------------------------------------------------------------------
// The caps panel.
// ---------------------------------------------------------------------------

/** capsOf reads `budget show --json`. A missing document is not an empty one:
 * `available: false` is a different fact from "this project has no caps", and
 * the panel draws the difference. */
function capsOf(raw: JSONDoc | null): SpendCaps {
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
 * `by model`'s is a model name and a period's is a date. A label is the class
 * for the two that are free text, and it is not a weak class: the free-text
 * rules redact an address, a URL, a path and a token out of a label exactly as
 * they do out of a sentence.
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
    case null:
      return 'label'
  }
}

/**
 * buildSpendModel is the two documents as the screen's data.
 *
 * The order of the work is the order of the design's sentences: the range and
 * the breakdown, then the rows, then the three series (never one), then the
 * totals the document itself reports, then the caps panel with the binding
 * scope named, and the person view decided last -- because it is the one thing
 * that removes rows from everything above it.
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

  const notes: string[] = []
  if (withheld) notes.push(PERSON_OFF_NOTE)
  if (!rowsReadable) {
    notes.push('the document carries no rows array, so this screen has no figures to draw')
  } else if (hasGapDay(drawn, days)) {
    // The legend for the gap points: a reader who sees a broken line is owed
    // the sentence that says what a missing day means. It is drawn when a day
    // the range covers has no row -- not merely when there are fewer rows than
    // days, because a row outside the range is a row, not a gap.
    notes.push('a day in this range with no document is a gap, not a zero: the history keeps a window and days outside it are pruned')
  }
  const stray = outsideRange(drawn, days)
  if (stray > 0) {
    notes.push(`${stray} row(s) outside the range the document covers are listed below and not drawn in the charts: a day axis places a point by its date`)
  }
  if (dimension === null) {
    notes.push('the document groups by a breakdown this console does not know, so the switch below cannot say which of its seven words it is')
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
    rows: drawn,
    series: SPEND_ACCOUNTS.map((account) =>
      withheld
        ? withheldSeries(account)
        : buildSeries(drawn, account, days),
    ),
    totals: totalsOf(report['totals']),
    caps: capsOf(budget),
    persons: { enabled: personsEnabled, withheld },
    warnings: arrayAt(report['warnings']).filter((warning): warning is string => typeof warning === 'string'),
    notes,
  }
}

/** outsideRange counts the rows whose key is not a day of the enumerated range:
 * the rows `buildSeries` does not plot. With no range there is no axis to be
 * outside of. A repeated key is one row on the axis and is not counted. */
function outsideRange(rows: readonly SpendRow[], days: readonly string[] | null): number {
  if (days === null) return 0
  const inside = new Set(days)
  const outside = new Set<string>()
  for (const row of rows) if (!inside.has(row.key)) outside.add(row.key)
  return outside.size
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
    segments: 0,
    peak: unavailable(PERSON_OFF_NOTE),
    empty: PERSON_OFF_NOTE,
  }
}

// ---------------------------------------------------------------------------
// The controls.
// ---------------------------------------------------------------------------

/**
 * SPEND_PICKER_WINDOWS is the range picker's closed set of windows, in
 * `reportRange`'s own grammar (`internal/cli/report.go`): `Nd` is the last N
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
 * `until` a day, `by` one of the seven words. `--repo` is admitted on this
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
 */
export async function readSpend(
  read: SpendReader,
  controls: SpendControls,
  caps: JSONDoc | null,
): Promise<SpendOutcome> {
  const decision = spendArguments(controls)
  if (!decision.ok) return { ok: false, args: null, reason: decision.reason }
  let document: JSONDoc
  try {
    document = await read(decision.args)
  } catch (cause) {
    return { ok: false, args: decision.args, reason: refusalSentence(cause) }
  }
  return {
    ok: true,
    args: decision.args,
    model: buildSpendModel(document, caps, { persons: controls.persons }),
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

/** header draws the project line: project, source, the range and the declared
 * week convention. */
function header(factory: ScreenFactory, model: SpendModel): ScreenElement {
  const element = make(factory, 'header', 'spend-header')
  const heading = cell(element, factory, 'h1', 'spend-project')
  putText(heading, model.project, 'project', 'project')
  if (model.source !== null) textCell(element, factory, 'span', model.source, 'label', 'source', 'spend-source')
  if (model.range.since !== null) textCell(element, factory, 'span', model.range.since, 'label', 'since', 'spend-since')
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
 * windowItem is one window of the range picker: a cell whose word the caller
 * passes as a literal (the console's own vocabulary for `reportRange`'s
 * grammar), and whether the operator chose it.
 */
function windowItem(parent: ScreenElement, factory: ScreenFactory, current: boolean): ScreenElement {
  const item = cell(parent, factory, 'li', 'spend-window')
  item.setAttribute('data-current', current ? 'true' : 'false')
  return item
}

/**
 * picker draws the range picker: `report --since`'s windows as the console's own
 * words, with the one the operator chose marked, and whether the range's end is
 * the operator's own day rather than `report`'s today.
 *
 * What it draws is the **request**; the header draws the range that came back,
 * which is the document's own `since`/`until`. The two are different statements
 * and the screen makes both: a picker that drew only what came back could not
 * say what was asked for, and a range whose days the history does not hold would
 * read as a range nobody chose.
 *
 * Every word is a literal, because they are the console's vocabulary and not a
 * document's strings -- the same rule the breakdown switch's seven words follow.
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
  // picker says so rather than marking the nearest one.
  const day = !week && !month && !quarter && !twelveWeeks
  element.setAttribute('data-day', day ? 'true' : 'false')
  // `--until` is the CLI's own today unless the operator set an end. Which of
  // the two it is, is a fact about the request; the day itself is the header's.
  const end = controls.until !== null
  element.setAttribute('data-until', end ? 'set' : 'cli')
  return element
}

/**
 * personKnob draws the person view's own switch (design D15): its word, whether
 * it is on, and the one-line note the design asks for beside a view that is off
 * by default.
 *
 * It is a control of its own rather than a state of the breakdown switch: the
 * switch's `person` word is one of the seven the CLI has, and the operator's
 * decision about it is a different thing from the breakdown the screen is
 * currently drawn by. The knob's own `data-enabled` is what the event wiring
 * reads, and the switch's `person` word is drawn disabled until it is on.
 */
function personKnob(factory: ScreenFactory, model: SpendModel): ScreenElement {
  const element = make(factory, 'div', 'spend-persons')
  const on = model.persons.enabled
  element.setAttribute('data-enabled', on ? 'true' : 'false')
  literal(cell(element, factory, 'span', 'spend-persons-word'), 'person breakdown')
  const state = cell(element, factory, 'span', 'spend-persons-state')
  if (on) literal(state, 'on')
  else literal(state, 'off')
  const note = cell(element, factory, 'p', 'spend-persons-note')
  if (on) literal(note, PERSON_ON_NOTE)
  else literal(note, PERSON_OFF_NOTE)
  return element
}

/** tabs draws the breakdown switch: the seven words, which of them is the
 * document's own grouping, and which this console may show. */
function tabs(factory: ScreenFactory, model: SpendModel): ScreenElement {
  const element = make(factory, 'ol', 'spend-switch')
  element.setAttribute('data-known', model.dimensionKnown ? 'true' : 'false')
  for (const entry of model.switches) {
    const item = cell(element, factory, 'li', 'spend-dim')
    // The seven words are the CLI's own (`ParseDim`'s help line) and this is
    // the console drawing its own vocabulary for them, not a document's string.
    literal(item, entry.dimension)
    item.setAttribute('data-dimension', entry.dimension)
    item.setAttribute('data-enabled', entry.enabled ? 'true' : 'false')
    item.setAttribute('data-current', entry.current ? 'true' : 'false')
    if (entry.note !== null) item.setAttribute('title', entry.note)
  }
  if (!model.dimensionKnown) {
    // An eighth word is a newer CLI. The switch says so in the console's own
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

/** point draws one point of one account's series. */
function point(factory: ScreenFactory, model: SpendModel, drawn: SpendPoint): ScreenElement {
  const element = make(factory, 'li', 'spend-point')
  element.setAttribute('data-state', drawn.reading.state)
  element.setAttribute('data-height', formatReading(drawn.height, 'percent'))
  element.setAttribute('data-partial', drawn.partial ? 'true' : 'false')
  element.setAttribute('data-hollow', drawn.hollow ? 'true' : 'false')
  const keyed = textCell(
    element,
    factory,
    'span',
    drawn.key,
    keyClass(model.dimension, drawn.key),
    'rows[].key',
    'spend-point-key',
  )
  element.setAttribute('data-key', drawnKey(keyed))
  numberCell(element, factory, 'span', drawn.reading, 'usd', 'spend-point-value')
  if (drawn.partial) literal(cell(element, factory, 'span', 'spend-partial'), PARTIAL_MARKER)
  return element
}

/** accountBlock draws one account's chart. Three of these, never one, and each
 * with its own peak: the accounts are never summed and they are never drawn on
 * one axis either. */
function accountBlock(factory: ScreenFactory, model: SpendModel, series: SpendSeries): ScreenElement {
  const element = make(factory, 'div', 'spend-account')
  element.setAttribute('data-account', series.account)
  const heading = cell(element, factory, 'h2', 'spend-account-title')
  // The title is the console's own word for the account, and the design's
  // headings are the console's vocabulary.
  literal(heading, series.title)
  numberCell(element, factory, 'span', series.peak, 'usd', 'spend-peak')
  element.setAttribute('data-peak', formatReading(series.peak, 'usd'))

  const list = cell(element, factory, 'ol', 'spend-series')
  list.setAttribute('data-segments', String(series.segments))
  if (series.points.length === 0) {
    // An account with nothing to draw is not an empty chart: it is the console
    // saying what it has not got, in the mark and the state of a gap.
    const empty = cell(list, factory, 'li', 'spend-point', 'spend-num')
    const none = gap(series.empty ?? 'this account has no point to draw')
    putReading(empty, none, 'usd')
    empty.setAttribute('data-state', none.state)
    empty.setAttribute('data-height', formatReading(none, 'percent'))
    return element
  }
  for (const drawn of series.points) list.append(point(factory, model, drawn))
  return element
}

/** rowLine draws one report row: its key, the three accounts in their own
 * states, the runs it covers and its `(partial)` marker. */
function rowLine(factory: ScreenFactory, model: SpendModel, row: SpendRow): ScreenElement {
  const element = make(factory, 'li', 'spend-row')
  element.setAttribute('data-partial', row.partial ? 'true' : 'false')
  element.setAttribute('data-hollow', row.hollow ? 'true' : 'false')
  const keyed = textCell(element, factory, 'span', row.key, keyClass(model.dimension, row.key), 'rows[].key', 'spend-key')
  element.setAttribute('data-key', drawnKey(keyed))
  numberCell(element, factory, 'span', row.model, 'usd', 'spend-model')
  numberCell(element, factory, 'span', row.notional, 'usd', 'spend-notional')
  numberCell(element, factory, 'span', row.compute, 'usd', 'spend-compute')
  numberCell(element, factory, 'span', row.runs, 'count', 'spend-runs')
  numberCell(element, factory, 'span', row.runHours, 'hours', 'spend-run-hours')
  if (row.tokens !== null) {
    numberCell(element, factory, 'span', row.tokens.in, 'count', 'spend-tokens-in')
    numberCell(element, factory, 'span', row.tokens.out, 'count', 'spend-tokens-out')
  }
  if (row.partial) literal(cell(element, factory, 'span', 'spend-partial'), PARTIAL_MARKER)
  return element
}

/** totalsLine draws the document's own totals row. */
function totalsLine(factory: ScreenFactory, model: SpendModel): ScreenElement {
  const element = make(factory, 'div', 'spend-totals')
  element.setAttribute('data-partial', model.totals.partial ? 'true' : 'false')
  literal(cell(element, factory, 'span', 'spend-totals-word'), 'total')
  numberCell(element, factory, 'span', model.totals.model, 'usd', 'spend-total-model')
  numberCell(element, factory, 'span', model.totals.notional, 'usd', 'spend-total-notional')
  numberCell(element, factory, 'span', model.totals.compute, 'usd', 'spend-total-compute')
  numberCell(element, factory, 'span', model.totals.runs, 'count', 'spend-total-runs')
  numberCell(element, factory, 'span', model.totals.runHours, 'hours', 'spend-total-run-hours')
  if (model.totals.partial) literal(cell(element, factory, 'span', 'spend-partial'), PARTIAL_MARKER)
  return element
}

/** scopeCell draws a cap and the scope that binds it. The scope is a word this
 * console chose from the document's own closed set, and an unknown one is drawn
 * as an explicit unknown with its sentence in the title. */
function scopeCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  reading: Reading,
  unit: Parameters<typeof putReading>[2],
  scope: SpendScope,
  attribute: string,
  ...classes: string[]
): ScreenElement {
  numberCell(parent, factory, 'span', reading, unit, ...classes)
  const element = cell(parent, factory, 'span', 'spend-scope')
  literal(element, scope.name)
  element.setAttribute(attribute, scope.known ? scope.source : 'unknown')
  element.setAttribute('data-scope-known', scope.known ? 'true' : 'false')
  if (!scope.known) {
    element.setAttribute('title', 'the document names a cap scope this console does not know, so it cannot say which scope binds')
  }
  return element
}

/** capsPanel draws the caps: global, defaults and the per-repository
 * overrides, each cap with the scope that binds it. */
function capsPanel(factory: ScreenFactory, model: SpendModel): ScreenElement {
  const caps = model.caps
  const element = make(factory, 'section', 'spend-caps')
  element.setAttribute('data-available', caps.available ? 'true' : 'false')
  if (!caps.available) {
    // The one thing this panel must not do is draw an empty one: a cap of $0
    // and a cap nobody read are different facts, and the promises the design
    // makes about caps are promises about the second.
    const none = cell(element, factory, 'p', 'spend-note')
    literal(none, 'no budget show document was read, so this console does not know the caps: every cap here is n/a, not zero')
    return element
  }
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
  if (caps.global !== null) {
    const global = make(factory, 'div', 'spend-cap-global')
    literal(cell(global, factory, 'span', 'spend-cap-word'), 'global')
    numberCell(global, factory, 'span', caps.global.dailyCap, 'usd', 'spend-cap-daily')
    numberCell(global, factory, 'span', caps.global.perRunCap, 'usd', 'spend-cap-per-run')
    numberCell(global, factory, 'span', caps.global.counted, 'usd', 'spend-cap-counted')
    numberCell(global, factory, 'span', caps.global.spent, 'usd', 'spend-cap-spent')
    // The notional account again, and again its own state: never added to the
    // counted dollars beside it.
    numberCell(global, factory, 'span', caps.global.notional, 'usd', 'spend-cap-notional')
    numberCell(global, factory, 'span', caps.global.headroom, 'usd', 'spend-cap-headroom')
    if (caps.global.kill !== null) global.append(killBanner(factory, caps.global.kill))
    element.append(global)
  }
  if (caps.defaults !== null) {
    const defaults = make(factory, 'div', 'spend-cap-defaults')
    literal(cell(defaults, factory, 'span', 'spend-cap-word'), 'repository defaults')
    numberCell(defaults, factory, 'span', caps.defaults.repoDailyCap, 'usd', 'spend-cap-daily')
    numberCell(defaults, factory, 'span', caps.defaults.repoPerRunCap, 'usd', 'spend-cap-per-run')
    element.append(defaults)
  }
  const repos = cell(element, factory, 'ol', 'spend-cap-repos')
  for (const row of caps.repos) {
    const item = cell(repos, factory, 'li', 'spend-cap-repo')
    const name = textCell(item, factory, 'span', row.repo === '' ? row.slug : row.repo, keyClass('repo', row.repo === '' ? row.slug : row.repo), 'repos[].repo', 'spend-cap-repo-name')
    item.setAttribute('data-key', drawnKey(name))
    numberCell(item, factory, 'span', row.counted, 'usd', 'spend-cap-counted')
    numberCell(item, factory, 'span', row.notional, 'usd', 'spend-cap-notional')
    numberCell(item, factory, 'span', row.headroom, 'usd', 'spend-cap-headroom')
    scopeCell(item, factory, row.dailyCap, 'usd', row.dailyScope, 'data-daily-scope', 'spend-cap-daily')
    scopeCell(item, factory, row.perRunCap, 'usd', row.perRunScope, 'data-per-run-scope', 'spend-cap-per-run')
    if (row.kill !== null) item.append(killBanner(factory, row.kill))
  }
  for (const warning of caps.warnings) {
    // The CLI's own sentences, drawn verbatim (contract section 3: "CLI-worded,
    // shown verbatim").
    textCell(element, factory, 'p', warning, 'sentence', 'warnings[]', 'spend-warning')
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
 * Every string a document wrote reaches the document through `putText` and
 * every number through `putReading`; the only thing written directly is the
 * console's own vocabulary, through `literal`. No string is parsed as markup,
 * nothing is written into the navigating attribute, and this screen offers no
 * link at all.
 */
export function renderSpend(
  factory: ScreenFactory,
  model: SpendModel,
  theme: SpendTheme,
  controls: SpendControls = SPEND_DEFAULT_CONTROLS,
): ScreenElement {
  const root = make(factory, 'section', 'spend')
  root.setAttribute('data-theme', theme)
  root.append(header(factory, model))
  root.append(picker(factory, controls))
  root.append(tabs(factory, model))
  root.append(personKnob(factory, model))

  const accounts = cell(root, factory, 'div', 'spend-accounts')
  for (const series of model.series) accounts.append(accountBlock(factory, model, series))

  const rows = cell(root, factory, 'ol', 'spend-rows')
  for (const row of model.rows) rows.append(rowLine(factory, model, row))
  root.append(totalsLine(factory, model))

  for (const note of model.notes) {
    const element = cell(root, factory, 'p', 'spend-note')
    literal(element, note)
  }
  for (const warning of model.warnings) {
    textCell(root, factory, 'p', warning, 'sentence', 'warnings[]', 'spend-warning')
  }
  root.append(capsPanel(factory, model))
  return root
}
