// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The Live screen: the design's screen 1, drawn from `watch --json` alone
// (desktop-console plan Task 2.1; design sections 6.1 rules 2 and 3, 6.2
// screen 1).
//
// The file has two halves and they are separated on purpose. `buildLiveModel`
// turns a decoded `LiveDocument` into rows of readings and flags -- no element,
// no `document`, nothing that needs a window -- so the part with the arithmetic
// and the thresholds in it is testable in plain Node. `renderLive` walks that
// model into elements through Task 2.0's writers, and it is the *only* place in
// this file that touches one.
//
// **Where the flags come from, and where they do not.** `silent`, `lost` and
// the deadline's amber are computed by `watch` itself
// (`internal/watch/build.go`'s `runRow`), which is the only process holding the
// two facts they need: a run's last heartbeat and its stage's start and
// deadline. The document carries the conclusion as an enum (`health`,
// `deadline`) and `age_seconds`, which is the age of the *run* and not the age
// of its last heartbeat. So this screen does not recompute a flag: it draws the
// enum, and it does exactly one thing of its own with the numbers --
// `contradiction` below, which reports the one combination a run's own age makes
// impossible, because silence never exceeds age. A screen that "derived" silent
// from `age_seconds > 60` would call every long-running healthy run silent, and
// that is the failure this paragraph exists to prevent.
//
// The thresholds are still the console's own constants, and a test holds each
// one to the Go source it stands for, by name (`live.test.ts`). They are not
// decoration: they are what the flag's `title` sentence says, and what
// `contradiction` and the connection's own stale note are computed with. A
// constant that the CLI changed and the console did not would be a screen
// explaining a threshold nobody uses any more.
//
// **What is *not* here.** No stylesheet and no colour. The design's density
// rules (section 6.1 rule 2: ~24 px rows, tabular figures, right-aligned
// numerics) are half markup and half CSS, and this task's file list carries no
// stylesheet -- `src/renderer/theme/tokens.css` is in the plan's tree but in no
// task's Files. So the screen emits what only markup can carry: the theme on
// the root (`data-theme`, one value per theme, which is what makes one
// screenshot golden per theme a different picture once the tokens land), the
// class hooks a stylesheet keys off, and `data-` readings for the numbers a bar
// or a sparkline needs to be a shape. Nothing here sets an inline style, and
// nothing sets the `style` attribute at all: `style-src 'self'` is the console's
// policy and an inline style is what it refuses.

import {
  formatReading,
  gap,
  measured,
  notional,
  plottable,
  seriesSegments,
  unavailable,
} from '../../shared/availability'
import type { Reading, Sample } from '../../shared/availability'
import { rigorModeWord } from '../../shared/rigor'
import type { RigorView } from '../../shared/rigor'
import { sanitizeText } from '../../shared/sanitize'
import type { BurnView, FinishedView, KillView, LiveDocument, RunView, TotalsView } from '../../shared/schema'
import { putReading, putText } from '../app'
import type { ElementLike } from '../app'
import { disabledWrite } from '../writes'

// ---------------------------------------------------------------------------
// The thresholds.
// ---------------------------------------------------------------------------

/**
 * LIVE_BURN_WINDOW_SECONDS is `internal/watch/state.go`'s `BurnWindow`: the
 * rolling window the CLI's own burn rate is taken over, and therefore the one
 * the sparkline's bars are drawn over. The plan names this one above the
 * others -- "the burn window must match `watch`'s own ... and a test asserts
 * the two constants cannot drift" -- because a window that drifted would make
 * the drawn instrument a measurement of a different thing.
 */
export const LIVE_BURN_WINDOW_SECONDS = 5 * 60

/** LIVE_BURN_MAX_SAMPLES bounds one key's burn history whatever its stamps say
 * (review round 3 of PR 494). The window is the first bound -- at the CLI's
 * one document a second it holds about 300 samples -- and this is the second,
 * for a feed that sends faster than that or stamps that bunch up: past it the
 * oldest samples go first, which are the ones a window would drop next. */
export const LIVE_BURN_MAX_SAMPLES = 1024

/** LIVE_STALE_AFTER_SECONDS is `internal/watch/state.go`'s `StaleAfter`: the
 * 45 s after which the design's header calls the connection stale. */
export const LIVE_STALE_AFTER_SECONDS = 45

/** LIVE_SILENT_AFTER_SECONDS is `internal/watch/build.go`'s `SilentAfter`. */
export const LIVE_SILENT_AFTER_SECONDS = 60

/** LIVE_LOST_AFTER_SECONDS is `internal/watch/build.go`'s `LostAfter` (3 min). */
export const LIVE_LOST_AFTER_SECONDS = 3 * 60

/** LIVE_NEAR_DEADLINE_FRACTION is `internal/watch/build.go`'s `NearDeadline`:
 * the fraction of a stage's time after which the row is amber. */
export const LIVE_NEAR_DEADLINE_FRACTION = 0.8

/** LIVE_NEAR_DEADLINE_PERCENT is the same fraction as the sentence says it, so
 * a threshold reads `80%` rather than `79.99999999999999%`.
 *
 * `0.8 * 100` is exactly `80` in IEEE-754, so this rounding changes nothing
 * about the value above -- it is here because the constant is a *fraction* and
 * not every fraction someone might choose multiplies to a whole number (`0.29 *
 * 100` is `28.999999999999996`), and a flag whose explanation reads
 * `28.999999999999996% of the stage's time` is a number nobody chose. */
export const LIVE_NEAR_DEADLINE_PERCENT = Math.round(LIVE_NEAR_DEADLINE_FRACTION * 100)

/**
 * LIVE_THEMES is the set of themes a screenshot golden is taken for. The
 * design's section 6.1 rule 5 has a light and a dark palette and a small
 * phosphor set besides; the tokens are not in this task, so the screen carries
 * the two the plan's goldens name and `data-theme` is how they reach the
 * document.
 */
export const LIVE_THEMES = Object.freeze(['light', 'dark'] as const)

/** LiveTheme is one of them. */
export type LiveTheme = (typeof LIVE_THEMES)[number]

// ---------------------------------------------------------------------------
// The review inbox (design F26, desktop-console v2 plan Task V2.7.1).
// ---------------------------------------------------------------------------

/**
 * LIVE_REVIEW_CAP bounds the review list, exactly as `PROJECT_JOB_CAP` bounds
 * the Projects screen's job list: the feed's `ready[]` is deliberately
 * **unfiltered** (`internal/watch/json.go`: `--keep` and `--keep-count` bound
 * the interactive view's history, not what still needs a look), so a project
 * that nobody has caught up with could carry thousands of rows -- and a screen
 * with ten thousand rows is not a list of ten thousand rows. Past this cap the
 * oldest rows go undrawn and the count of what was not drawn is drawn instead,
 * which is the same shape the log panel and the job list use.
 */
export const LIVE_REVIEW_CAP = 50

/**
 * LIVE_REVIEW_TITLE is the section's heading, quoted from the CLI's own text
 * view (`internal/watch/render.go`'s `readyForReview`: `Ready for your
 * review (%d)`). It is the words alone: the count is not part of this string,
 * and `reviewSection` is where the two are joined into the CLI's one line.
 */
export const LIVE_REVIEW_TITLE = 'Ready for your review'

/**
 * LIVE_REVIEW_EMPTY is what the section says with no row to draw. It says it
 * about **this feed** on purpose: `ready[]` ships empty when the runs bucket
 * could not be read as well as when nothing is waiting, and the difference
 * between those two is the CLI's own `queued_note`, which the header draws
 * (`live-note`) rather than this section inventing a second reason.
 */
export const LIVE_REVIEW_EMPTY = 'no finished run in this feed is ready for review'

/**
 * LIVE_PR_NO_LINK_REASON is the sentence the section carries beneath its rows,
 * and it is **the same sentence `run.ts` draws beside a run's pull request**
 * (`PR_NO_LINK_REASON`): the console shows the number and the URL as text and
 * builds no link from either, because no document on this tree says which
 * provider the run's repository is on (v1 design section 5.1 rule 2, its gap 7,
 * and design F26). It is a second copy rather than an import because the
 * screens are siblings and none owns the others -- the same reason each screen
 * declares `ScreenElement` and `ScreenFactory` itself -- and
 * `review.test.ts` holds the two constants equal, so the copies cannot drift
 * into two different promises.
 */
export const LIVE_PR_NO_LINK_REASON =
  'this CLI does not say which provider this run’s repository is on, so the console will not build a link from it'

// ---------------------------------------------------------------------------
// The flags.
// ---------------------------------------------------------------------------

/** RunTone is the *kind* of attention a row wants, and it is deliberately a
 * name and not a colour: design section 6.1 rule 3's "changes colour only at a
 * defined threshold ... never decoratively" is a decision about which of these
 * four a row is, and the palette is somewhere else. */
export type RunTone = 'plain' | 'amber' | 'alarm' | 'queued'

/** RunFlag is one flag as the design words it (`silent`, `lost`, amber past the
 * deadline), with the sentence behind it. */
export interface RunFlag {
  /** what is drawn on the row */
  readonly name: string
  /** why, in the console's words, with the threshold this flag is about */
  readonly because: string
  readonly tone: RunTone
}

/**
 * watchHealth restates `watch`'s own silence rule
 * (`internal/watch/build.go:416-421`) with the constants above: `quiet >
 * LostAfter` is lost, else `quiet > SilentAfter` is silent, else ok. It is `>`
 * and not `>=`, which is the boundary the plan's red step names: a run silent
 * for exactly 60 s is **not** silent.
 *
 * It is not how a flag is decided -- the document's `health` is (see the header)
 * -- and it exists so that "the console's 60 s" and "the CLI's 60 s" are the
 * same comparison in code as well as in value: `contradiction` runs it against
 * a run's age, where age is the only silence bound a document carries.
 *
 * A silence nobody measured (`null`, `NaN`, a negative) is ok: the console does
 * not invent a heartbeat where the document has none.
 */
export function watchHealth(quietSeconds: number | null): 'ok' | 'silent' | 'lost' {
  if (quietSeconds === null || !Number.isFinite(quietSeconds) || quietSeconds < 0) return 'ok'
  if (quietSeconds > LIVE_LOST_AFTER_SECONDS) return 'lost'
  if (quietSeconds > LIVE_SILENT_AFTER_SECONDS) return 'silent'
  return 'ok'
}

/** HEALTH_SEVERITY orders the three states so that "this document's flag is
 * worse than its own numbers allow" is a comparison. It is a `Map`, not an
 * object literal: the key is the document's own string, and a literal answers
 * `'constructor'` and `'__proto__'` with an inherited member instead of
 * `undefined`. */
const HEALTH_SEVERITY: ReadonlyMap<string, number> = new Map([
  ['ok', 0],
  ['silent', 1],
  ['lost', 2],
])

const UNKNOWN_HEALTH_FLAG: RunFlag = Object.freeze({
  name: 'unknown health',
  tone: 'amber',
  because: 'the document names a health this console does not know, so it cannot say the run is fine',
})
const UNKNOWN_DEADLINE_FLAG: RunFlag = Object.freeze({
  name: 'unknown deadline',
  tone: 'amber',
  because: 'the document names a deadline state this console does not know, so it cannot say the run is on time',
})

/**
 * healthFlag draws the document's `health` enum. `ok` is no flag at all.
 * Anything the console does not recognise is an amber `unknown health` flag in
 * the console's own words (never the document's: the value is not echoed) -- a
 * value from a newer CLI is not an excuse to draw "fine", and nothing upstream
 * of this function records a refusal, so the row itself says it.
 */
export function healthFlag(health: string): RunFlag | null {
  switch (health) {
    case 'silent':
      return {
        name: 'silent',
        tone: 'amber',
        because: `watch calls this run silent: no heartbeat for over ${LIVE_SILENT_AFTER_SECONDS} s`,
      }
    case 'lost':
      return {
        name: 'lost',
        tone: 'alarm',
        because: `watch calls this run lost: no heartbeat for over ${LIVE_LOST_AFTER_SECONDS} s`,
      }
    case 'ok':
      return null
    default:
      return UNKNOWN_HEALTH_FLAG
  }
}

/** deadlineFlag draws the document's `deadline` enum, in the same shape. */
export function deadlineFlag(deadline: string): RunFlag | null {
  switch (deadline) {
    case 'near':
      return {
        name: 'near deadline',
        tone: 'amber',
        because: `past ${LIVE_NEAR_DEADLINE_PERCENT}% of the stage's time`,
      }
    case 'over':
      return {
        name: 'over deadline',
        tone: 'alarm',
        because: "past the stage's own deadline",
      }
    case 'ok':
      return null
    default:
      return UNKNOWN_DEADLINE_FLAG
  }
}

/** QUEUED_FLAGS are the two the bucket side contributes (`JSONRun.Queued`,
 * `JSONRun.Stuck`): a run known only from the runs bucket, and one that has
 * gone `QueuedStuckAfter` without a registry entry showing up. They are not
 * health and they are not deadlines, so they are drawn beside them rather than
 * mapped onto them. */
const QUEUED_FLAG: RunFlag = Object.freeze({
  name: 'queued',
  tone: 'queued',
  because: 'known from the runs bucket only: no registry entry yet',
})
const STUCK_FLAG: RunFlag = Object.freeze({
  name: 'stuck',
  tone: 'alarm',
  because: 'still no registry entry well past the point one should have appeared',
})

/** runFlags is every flag on a run, in the order the design lists them. */
export function runFlags(run: RunView): readonly RunFlag[] {
  const flags: RunFlag[] = []
  const health = healthFlag(run.health)
  if (health !== null) flags.push(health)
  const deadline = deadlineFlag(run.deadline)
  if (deadline !== null) flags.push(deadline)
  if (run.stuck) flags.push(STUCK_FLAG)
  if (run.queued) flags.push(QUEUED_FLAG)
  return flags
}

/** rowTone is the worst tone among a row's flags: an alarm is what a person
 * scanning the screen needs to see, and a queued row is not a problem. */
export function rowTone(flags: readonly RunFlag[]): RunTone {
  let tone: RunTone = 'plain'
  const worse = (a: RunTone, b: RunTone): RunTone => {
    const rank: Readonly<Record<RunTone, number>> = { plain: 0, queued: 1, amber: 2, alarm: 3 }
    return rank[a] >= rank[b] ? a : b
  }
  for (const flag of flags) tone = worse(tone, flag.tone)
  return tone
}

/**
 * contradiction reports the one thing a run's own age makes impossible: the
 * document claiming a silence that the age cannot support. Silence never
 * exceeds age (`runRow`'s `quiet` is measured from a heartbeat no earlier than
 * the start), so a run 45 s old cannot be `silent` at 60 s, and a run drawn
 * that way is a document the console should say something about rather than
 * quietly repeat.
 *
 * The other direction is not a contradiction and must not be reported as one:
 * a 30-minute-old run whose last heartbeat was five seconds ago is `ok`, and a
 * run of the same age that has been quiet for 100 s is `silent`.
 *
 * **An age that is not a measured number says nothing at all.** `age_seconds`
 * is a `*float64` with no `omitempty` (`internal/watch/json.go:67`) and
 * `schema.ts` reads it on the tolerant axis, so `null` is a reading this screen
 * accepts -- and `watchHealth(null)` is `ok`, so any `silent` or `lost` health
 * would "beat" it and this function would announce that a run is "only null s
 * old". A bound the document does not carry is not a bound of zero; it is the
 * absence of one, and there is nothing to contradict.
 */
export function contradiction(health: string, ageSeconds: number | null): string | null {
  const claimed = HEALTH_SEVERITY.get(health)
  if (claimed === undefined || claimed === 0) return null
  if (ageSeconds === null || !Number.isFinite(ageSeconds) || ageSeconds < 0) return null
  const supported = HEALTH_SEVERITY.get(watchHealth(ageSeconds))
  if (supported === undefined || claimed <= supported) return null
  return `watch calls this run ${health}, but it is only ${String(ageSeconds)} s old, and a run cannot have been quiet for longer than that`
}

// ---------------------------------------------------------------------------
// The burn history.
// ---------------------------------------------------------------------------

/**
 * BurnHistory is the rolling window the sparkline is drawn from: one **bounded**
 * list of `{at, reading}` per key, and a window that is `BurnWindow` wide.
 *
 * The window is taken **at a stamp the caller names** rather than at the wall
 * clock. That is not a detail: a replay of a recorded feed must produce the
 * same picture every time it is run, and a window anchored on `Date.now()`
 * would make every screenshot golden depend on when it was taken.
 *
 * `observe` prunes on the way in, at the stamp of the sample it just added, so
 * the list is bounded by how many arrivals fit in one window rather than by how
 * long the console has been open. That matters because the caller keeps one
 * history across frames and `buildLiveModel` observes a sample per repository
 * per frame: a feed running for a day at a document per second is 86,400
 * samples per repository, and every one of them older than the newest window
 * can never be drawn again. Pruning at the newest stamp is exactly the cut
 * `window` makes for a window ending there, so nothing `window` would have
 * returned is lost.
 */
export class BurnHistory {
  /** Each key's samples, in time order, each with its stamp parsed once: the
   * parse is the cost (review round 4 of PR 494: walking the list with a
   * `Date.parse` per step made 20,000 reverse-ordered samples take 5.9 s). */
  private readonly samples = new Map<string, Timed[]>()

  /**
   * observe adds one arrival and drops what has fallen out of the window.
   *
   * The list is kept **ordered by time** and **one sample per stamp**: a sample
   * at a stamp the key already holds is the same observation arriving again
   * (a stale rebuild re-reads the last document and must not draw it a second
   * time, design section 6.3), so it is dropped; and a gap that follows a gap
   * replaces it, so one silence is one gap rather than a gap per rebuild. A
   * sample stamped before the newest one is inserted where its time puts it
   * rather than at the end, so a clock that steps back cannot reorder the bars.
   *
   * **It is O(log n) to place a sample** (a binary search over parsed stamps),
   * and a sample already older than the window ending at the newest one is
   * dropped on arrival -- the prune would drop it anyway -- so a feed whose
   * clock runs backwards costs no more than one running forwards.
   */
  observe(key: string, sample: Sample): void {
    const when = Date.parse(sample.at)
    // An unparseable stamp has no place in time, so no window can ever return
    // it: keeping it would only grow the list (review round 3 of PR 494, where
    // one bad `now` kept every later sample). `schema.ts` refuses such a `now`
    // by name; this is the history's own guard.
    if (Number.isNaN(when)) return
    const existing = this.samples.get(key)
    if (existing === undefined) {
      this.samples.set(key, [{ sample, when }])
      return
    }
    const windowMs = LIVE_BURN_WINDOW_SECONDS * 1000
    const newestBefore = existing[existing.length - 1]?.when ?? when
    if (when < newestBefore - windowMs) return
    const isGap = sample.reading.state === 'gap'
    // Where the stamp puts it: after everything at or before it, so a gap that
    // shares the last measured sample's stamp still comes after it.
    const index = firstAfter(existing, when)
    // The same observation arriving again is dropped, and a gap replaces the gap
    // before it (one silence is one gap). A measured sample and a gap at one
    // stamp are two different things and both stay, measured first.
    for (let at = index; at > 0 && existing[at - 1]?.when === when; at--) {
      const held = existing[at - 1]
      if ((held?.sample.reading.state === 'gap') === isGap) {
        if (isGap) existing[at - 1] = { sample, when }
        return
      }
    }
    const last = existing[index - 1]
    if (isGap && last !== undefined && last.sample.reading.state === 'gap' && index === existing.length) {
      existing[index - 1] = { sample, when }
    } else {
      existing.splice(index, 0, { sample, when })
    }
    // The list is ordered, so the newest is last.
    const newest = Math.max(when, existing[existing.length - 1]?.when ?? when)
    const keep = Math.max(firstAtOrAfter(existing, newest - windowMs), existing.length - LIVE_BURN_MAX_SAMPLES)
    if (keep > 0) existing.splice(0, keep)
  }

  /** size is how many samples the history holds for `key`: what the bound
   * (`LIVE_BURN_MAX_SAMPLES`) is a bound on. */
  size(key: string): number {
    return this.samples.get(key)?.length ?? 0
  }

  /**
   * window is the samples a rate over the last `BurnWindow` ending at `at` is
   * computed from: those stamped at or after `at - LIVE_BURN_WINDOW_SECONDS`,
   * and no later than `at` itself.
   *
   * The boundary is `State.Burn`'s own (`internal/watch/state.go:168`): it
   * drops what is `Before(cut)`, so a sample stamped **exactly** at the cut is
   * still in the window. A console that dropped it would draw a sparkline
   * missing its oldest bar at the one instant a test can see.
   */
  window(key: string, at: string): readonly Sample[] {
    const all = this.samples.get(key) ?? []
    const end = Date.parse(at)
    if (Number.isNaN(end)) return []
    const cut = end - LIVE_BURN_WINDOW_SECONDS * 1000
    return all.slice(firstAtOrAfter(all, cut), firstAfter(all, end)).map((entry) => entry.sample)
  }
}

/** Timed is one sample of a `BurnHistory` list with its stamp parsed. */
interface Timed {
  readonly sample: Sample
  readonly when: number
}

/** firstAfter is the index of the first entry stamped after `when` (the list's
 * length when there is none): a binary search over an ordered list. */
function firstAfter(list: readonly Timed[], when: number): number {
  let low = 0
  let high = list.length
  while (low < high) {
    const middle = (low + high) >>> 1
    if ((list[middle]?.when ?? Infinity) <= when) low = middle + 1
    else high = middle
  }
  return low
}

/** firstAtOrAfter is the index of the first entry stamped at or after `when`. */
function firstAtOrAfter(list: readonly Timed[], when: number): number {
  let low = 0
  let high = list.length
  while (low < high) {
    const middle = (low + high) >>> 1
    if ((list[middle]?.when ?? Infinity) < when) low = middle + 1
    else high = middle
  }
  return low
}

/** SparkBar is one bar of the sparkline: the rate that arrived, and the height
 * that rate is drawn at. */
export interface SparkBar {
  readonly at: string
  /** the burn rate the CLI reported, in dollars per minute */
  readonly rate: Reading
  /** the height of the bar, as a share of the window's own highest rate */
  readonly height: Reading
}

/** Sparkline is the bars and the number of segments a line would be drawn in:
 * a point that is not plottable breaks the series (`seriesSegments`), and a
 * draw that joined across the break would be a line through a measurement
 * nobody made. */
export interface Sparkline {
  readonly bars: readonly SparkBar[]
  readonly segments: number
}

/**
 * sparkline turns a history into bars. The axis is the window's own highest
 * rate rather than a fixed scale, because the point of the instrument is the
 * *shape* of the last five minutes -- a fixed axis in dollars would make every
 * quiet window a row of invisible bars.
 *
 * A height of `0` is honest here and is not the zero `availability.ts` exists
 * to prevent: it says "this sample's rate is nothing against the window's
 * highest", which is a statement about a value that was measured. A sample the
 * document does not carry is a **gap** and breaks the series, which is the
 * other half.
 */
export function sparkline(history: BurnHistory, key: string, at: string): Sparkline {
  const samples = history.window(key, at)
  const rates: number[] = []
  for (const sample of samples) {
    if (plottable(sample.reading) && sample.reading.value !== null) rates.push(sample.reading.value)
  }
  const highest = rates.length === 0 ? 0 : Math.max(...rates)
  const bars = samples.map((sample): SparkBar => {
    if (!plottable(sample.reading) || sample.reading.value === null) {
      return {
        at: sample.at,
        rate: sample.reading,
        height: gap(sample.reading.because === '' ? 'no sample arrived here' : sample.reading.because),
      }
    }
    const share = highest === 0 ? 0 : (sample.reading.value / highest) * 100
    return { at: sample.at, rate: sample.reading, height: measured(share) }
  })
  return { bars, segments: seriesSegments(samples).length }
}

// ---------------------------------------------------------------------------
// The model.
// ---------------------------------------------------------------------------

/** spendReading is a run's spend, marked notional when the document says the
 * dollars are a subscription's list price. `notional: true` is the wire's own
 * statement (`JSONRun.Notional`: "SpentUSD is a subscription's list price"), and
 * design section 6.2's last sentence is why: an OAuth run's spend is not money
 * and must never be read as if it were. */
export function spendReading(run: RunView): Reading {
  const spend = run.spentUsd
  if (spend.value === null) return spend
  if (!run.notional) return spend
  return notional(spend.value, { lowerBound: spend.lowerBound })
}

/**
 * notionalReading marks the two `notional_usd` readings of the wire
 * (`JSONTotal.NotionalUSD`: "subscription list price: never billed, never
 * capped"). `schema.ts` reads the key as the number it is and cannot know what
 * it means; the meaning is in the key's own name and in the design's rule that
 * the three accounts are kept apart (section 6.1 rule 1, Phase 2's review
 * focus), so it is applied here, once, for both shapes that carry it.
 *
 * An unavailable reading stays unavailable: "not known" is not made more
 * precise by calling it notional.
 */
export function notionalReading(reading: Reading): Reading {
  if (reading.value === null) return reading
  return notional(reading.value, { lowerBound: reading.lowerBound })
}

/** burnReading is a repository's burn rate, with `known` applied.
 *
 * The wire carries the rate and a flag beside it (`JSONBurn.Known`: "false for
 * the first BurnWarmup after a (re)connect"), and the flag is the *state*: the
 * zero the document carries when it is false is not a measurement of nothing,
 * it is the absence of one. Reading the number without the flag is exactly the
 * mistake `availability.ts`'s header describes. */
export function burnReading(burn: BurnView): Reading {
  if (!burn.known) {
    return unavailable('the document says this repository has no measured burn rate yet')
  }
  return burn.usdPerMin
}

/** LiveRunRow is one run as the screen draws it. */
export interface LiveRunRow {
  readonly run: string
  readonly title: string
  readonly stage: string
  readonly round: string
  readonly verify: string
  readonly models: string
  /** the run's recipe when it is not the default; `null` when the CLI left it
   * out, which is not the same as an empty one */
  readonly recipe: string | null
  readonly auth: string
  readonly workflow: string | null
  readonly requestedBy: string | null
  /** the run's whole recorded launcher (`launched_by`), `null` when the record
   * carries none. **The runs table has no column for it** (console redesign,
   * step 2b): the value is the record's, and `shared/launcher.ts` is still the
   * module that shortens it, so a step that wants the column back has both. */
  readonly launchedBy: string | null
  readonly spend: Reading
  readonly age: Reading
  readonly flags: readonly RunFlag[]
  readonly tone: RunTone
  readonly queued: boolean
  readonly stuck: boolean
  /** the console's sentence about a flag the run's own age cannot support, or
   * `null` when there is nothing impossible to report */
  readonly contradiction: string | null
  /** the run's rigor block, or `null`. **It is not in the live document**: the
   * frame is `JSONRun`, which carries no rigor, so this is a join against
   * `runs ls --json` by run id (`shared/rigor.ts`'s `rigorIndex`) and the
   * caller is what makes it. `null` is "no record of this run's rigor" -- a
   * run from before the block, one that died before bootstrap, or a run the
   * join did not reach -- and the row draws no chip at all for it, never a
   * mode guessed from another run. */
  readonly rigor: RigorView | null
}

/** LiveRepoRow is one repository: its bar, its sparkline, its kill banner and
 * its runs. */
export interface LiveRepoRow {
  readonly slug: string
  readonly repo: string
  readonly countedUsd: Reading
  readonly spentUsd: Reading
  readonly notionalUsd: Reading
  readonly dailyCapUsd: Reading
  readonly percent: Reading
  readonly burnPerMin: Reading
  readonly burnFast: boolean
  readonly spark: Sparkline
  readonly kill: KillView | null
  readonly runs: readonly LiveRunRow[]
}

/** LiveConnection is the header's connectivity line: the CLI's own state, the
 * age it was given, and whether the console's own reading of that age is past
 * `StaleAfter`. */
export interface LiveConnection {
  readonly state: string
  readonly reason: string | null
  readonly age: Reading
  readonly staleByAge: boolean
}

/** LiveReviewRow is one finished run waiting for a look (`JSONFinished` from
 * the feed's `ready[]`): its repository, its pull request as facts, its title
 * and its age. The pull request is **three fields and not a link** -- a number
 * and a URL the screen draws as text (design F26) -- so `null` here is "the
 * run has no pull request", which the wire spells by leaving both fields out
 * (`omitempty`), and never a value nobody filled in. */
export interface LiveReviewRow {
  readonly run: string
  readonly repo: string
  readonly title: string
  readonly prNumber: number | null
  readonly prUrl: string | null
  readonly age: Reading
}

/**
 * LiveFleet is the fleet budget panel's own figures: everything the panel draws
 * that is not one of `total`'s readings, and nothing a decoder carries.
 *
 * The three counts are taken over the **rows this model built** rather than over
 * `total.runs`: the total's count is the CLI's, and a row the console could not
 * place (an orphan) is still a run that is spending, so a panel that counted the
 * CLI's number would disagree with the list under it. `heartbeatLost` is the
 * same observation one row at a time -- any drawn run carrying the `lost` flag
 * (`runFlags`, which is the only thing that turns the document's `health` enum
 * into a flag) -- and it is a fact about the fleet, not about one row: a panel
 * that drew "3 active" over a screen where one of them has lost its heartbeat
 * would be reporting a count nobody can act on.
 */
export interface LiveFleet {
  /** what is left of today's cap: `daily_cap_usd - counted_usd`, floored at
   * zero, and `unavailable` when either side is not a measurement */
  readonly headroom: Reading
  readonly burn: Reading
  readonly active: number
  readonly queued: number
  readonly heartbeatLost: boolean
}

/**
 * headroomOf is the cap less what has been counted against it.
 *
 * **The floor is zero and not a signed number.** `counted` is open leases plus
 * settled spend and a cap that is over-committed is a real state (the queue
 * holds runs until a lease settles), but "minus $4.00 of headroom" is not an
 * amount anyone can spend, and a negative width on the bar would draw the fill
 * running backwards out of its own track. An unmeasured side gives `unavailable`
 * with the console's own reason: a cap nobody read and a spend nobody counted
 * are both "the console cannot say", and neither is a headroom of zero.
 */
export function headroomOf(cap: Reading, counted: Reading): Reading {
  const limit = cap.value
  const used = counted.value
  if (limit === null) {
    return unavailable('the document carries no total.daily_cap_usd, so the console cannot say what is left of the cap')
  }
  if (used === null) {
    return unavailable('the document carries no total.counted_usd, so the console cannot say what is left of the cap')
  }
  return measured(Math.max(0, limit - used))
}

/** LiveModel is the whole screen as data. */
export interface LiveModel {
  readonly project: string
  readonly mode: string
  readonly day: string
  readonly now: string
  readonly connection: LiveConnection
  readonly total: TotalsView
  /** fleet is the budget panel's own figures, derived here rather than decoded
   * (`LiveFleet`) */
  readonly fleet: LiveFleet
  readonly repos: readonly LiveRepoRow[]
  /** runs the document lists whose slug names no repository block. The CLI
   * never writes one (`BuildJSON` appends a run inside its repository's loop),
   * and showing them rather than dropping them is the whole point of the
   * field: a row the console cannot place is still a run that is spending. */
  readonly orphans: readonly LiveRunRow[]
  /** review is the feed's `ready[]` -- the finished runs whose outcome is
   * `ready`, newest first -- capped at `LIVE_REVIEW_CAP`. It is the CLI's own
   * "Ready for your review" list (`internal/watch/filter.go`'s
   * `ReadyForReview`, drawn by `internal/watch/render.go`'s `readyForReview`),
   * and it is a field of its own rather than a filter over `finished[]`
   * because the CLI already made that choice: a finished run whose outcome is
   * `draft` or `none` is not waiting for a review.
   *
   * The rest of `finished[]` is deliberately not drawn here: this is the Live
   * screen -- what is spending now, and what is waiting on the operator -- and
   * a finished run whose outcome is `draft` or `none` is waiting on nobody.
   * Drawing the whole of `finished[]` would also make this screen a history,
   * which is not what the feed is read for; the same choice the CLI makes in
   * its own view. */
  readonly review: readonly LiveReviewRow[]
  /** reviewCount is how many ready rows the document carried, which is more
   * than `review.length` exactly when the list was capped. It is the CLI's own
   * count -- `len(ReadyRowsOf(v))` -- taken over the list this console would
   * have drawn whole. */
  readonly reviewCount: number
  readonly queuedNote: string | null
  /** the console's own reading of how long the feed has been quiet, when the
   * caller keeps one. `null` is a caller that measures nothing -- the screen
   * then draws no banner and no timestamp, which is every drawing the earlier
   * tests and the goldens were taken from. */
  readonly feed: FeedHealth | null
}

/**
 * FeedHealth is the console's own staleness reading (design section 6.3;
 * contract section 10.9's `watch.StaleAfter`, 45 s). It is quoted here from
 * `src/source/cli.ts`'s `FeedSilence`, structurally rather than by import --
 * the renderer may not import a module that spawns -- and `degraded.spec.ts`
 * hands one to `buildLiveModel` in the other type's place, so a drift between
 * the two fails the typecheck rather than the screen.
 *
 * The CLI announces its own `connection.state`, and this is **not** a second
 * opinion about that word: `connection.staleByAge` already reads the age the
 * document carries. This one reads the console's own clock against the
 * *arrival* of the newest frame, which is the only thing that can see a CLI
 * that has stopped talking altogether (contract section 10.11 item 2).
 */
export interface FeedHealth {
  /** the arrival stamp of the newest frame, or `null` with none */
  readonly since: string | null
  /** the console's own clock at the moment of the reading */
  readonly at: string
  /** whole seconds between `since` and `at`, or `null` with no frame */
  readonly seconds: number | null
  /** whether `seconds` is past `LIVE_STALE_AFTER_SECONDS` */
  readonly stale: boolean
}

/** feedReading is a feed's silence as a Reading, so the number cannot be drawn
 * without its state: a console that has heard nothing has measured nothing. */
export function feedReading(feed: FeedHealth): Reading {
  if (feed.seconds === null) {
    return unavailable('no document has arrived yet, so there is no silence to measure')
  }
  return measured(feed.seconds)
}

/** rowOf builds one run row. */
function rowOf(run: RunView, rigor: ReadonlyMap<string, RigorView>): LiveRunRow {
  const flags = runFlags(run)
  const age = run.ageSeconds.value
  return {
    run: run.run,
    title: run.title,
    stage: run.stage,
    round: run.round,
    verify: run.verify,
    models: run.models,
    recipe: run.recipe === '' ? null : run.recipe,
    auth: run.auth,
    workflow: run.workflow === '' ? null : run.workflow,
    requestedBy: run.requestedBy === '' ? null : run.requestedBy,
    launchedBy: run.launchedBy === '' ? null : run.launchedBy,
    spend: spendReading(run),
    age: run.ageSeconds,
    flags,
    tone: rowTone(flags),
    queued: run.queued,
    stuck: run.stuck,
    contradiction: contradiction(run.health, age),
    rigor: rigor.get(run.run) ?? null,
  }
}

// ---------------------------------------------------------------------------
// The pipeline and the run's own state.
// ---------------------------------------------------------------------------

/** PipelineStage is one of the five segments a run row's pipeline draws, in the
 * order a run moves through them. */
export type PipelineStage = 'queued' | 'build' | 'implement' | 'review' | 'done'

/** PIPELINE_SEGMENTS is the five segments in drawing order. */
export const PIPELINE_SEGMENTS: readonly PipelineStage[] = Object.freeze([
  'queued',
  'build',
  'implement',
  'review',
  'done',
])

/**
 * STAGE_SEGMENTS maps **the runner's own stage names** onto the five segments.
 * The names are not invented here: they are the strings `internal/runner` writes
 * onto the record (`r.rec.Stage = ...`, and `r.stage(ctx, name, ...)`'s callers
 * for `plan`, `implement`, `review`, `fix` and `review_first`), plus the
 * `"queued"` the CLI's own queued rows carry (`internal/watch/queued.go`).
 *
 * **The five segments are coarser than the stages on purpose.** A pipeline bar
 * is a glance at where a run is, not a transcript of the runner's steps, so the
 * setup stages (`provision`, `bootstrap`, `plan`) are one segment, the work
 * stages (`implement`, `fix`, `review_first`) are another, the gate and the
 * review (`check`, `review`) are a third, and the finishing stages (`finalize`,
 * `writeback`, `render`, `digest`) are the last. `build` is the segment's name
 * rather than the runner's, because the runner has no stage by that name.
 *
 * **It is a `Map` rather than an object literal** for the reason
 * `HEALTH_SEVERITY` is: a document's stage string is untrusted, and
 * `STAGE_SEGMENTS['constructor']` on an object literal answers with something
 * inherited rather than with nothing.
 */
const STAGE_SEGMENTS: ReadonlyMap<string, PipelineStage> = new Map([
  ['queued', 'queued'],
  ['provision', 'build'],
  ['bootstrap', 'build'],
  ['plan', 'build'],
  ['implement', 'implement'],
  ['fix', 'implement'],
  ['review_first', 'implement'],
  ['check', 'review'],
  ['review', 'review'],
  ['finalize', 'done'],
  ['writeback', 'done'],
  ['render', 'done'],
  ['digest', 'done'],
])

/**
 * stageSegment is the segment a stage name lights, or `null` for a stage this
 * build has never heard of. **A stage the console does not know marks no current
 * segment** -- the row still prints the stage's own text, which is the whole
 * reason the text line is not the decoration -- because guessing a segment from
 * an unknown word would draw a position on the bar that nothing stated.
 */
export function stageSegment(stage: string): PipelineStage | null {
  return STAGE_SEGMENTS.get(stage) ?? null
}

/** SegmentMark is how one segment of a pipeline is drawn. */
export type SegmentMark = 'past' | 'current' | 'future' | 'danger'

/** DrawnSegment is one segment and the mark it carries. */
export interface DrawnSegment {
  readonly stage: PipelineStage
  readonly mark: SegmentMark
}

/**
 * pipelineOf is the five segments as one row draws them: everything before the
 * run's own segment is past, the run's segment is current, and everything after
 * it is future.
 *
 * **A lost heartbeat turns the current segment into `danger` and nothing else.**
 * The run is still *at* that segment -- the position is the CLI's own stage --
 * and what changed is that the console has stopped hearing from it, which is the
 * one thing the row's colour is allowed to say. A stage this build does not know
 * lights nothing: with no current segment there is no past either, so the bar is
 * entirely future and the text line under it carries the stage.
 */
export function pipelineOf(row: LiveRunRow): readonly DrawnSegment[] {
  const known = stageSegment(row.stage)
  const at = known === null ? -1 : PIPELINE_SEGMENTS.indexOf(known)
  const lost = row.flags.some((flag) => flag.name === 'lost')
  return PIPELINE_SEGMENTS.map((stage, index): DrawnSegment => {
    if (at === -1 || index > at) return { stage, mark: 'future' }
    if (index < at) return { stage, mark: 'past' }
    return { stage, mark: lost ? 'danger' : 'current' }
  })
}

/**
 * RunState is a run's condition in one word -- the word the pipeline's own line
 * and the STATUS chip both draw.
 *
 * **It is derived from what the document carries and never from the clock**: the
 * CLI computes `health` and the deadline flags (`internal/watch/build.go`), the
 * queued flag is the queue's own, and the last segment is the stage the runner
 * recorded. A console that turned an age into "lost" would call every
 * long-running healthy run lost, which is the failure `live.ts`'s header
 * describes.
 *
 * `done` is the console's word for a run whose recorded stage is one of the
 * finishing ones: the registry still lists it -- so it is still on this screen,
 * and still spending -- and its pipeline is at its last segment. It is not a
 * claim that the run has finished, and the pipeline bar beside it says which
 * segment it is at.
 */
export type RunState = 'queued' | 'running' | 'lost' | 'done'

/** runStateOf is the one word for a run's condition. The order of the tests is
 * the order of precedence: a queued run has no stage to be lost at, and a lost
 * heartbeat is the worse fact of the two remaining. */
export function runStateOf(row: LiveRunRow): RunState {
  if (row.queued) return 'queued'
  if (row.flags.some((flag) => flag.name === 'lost')) return 'lost'
  if (stageSegment(row.stage) === 'done') return 'done'
  return 'running'
}

/**
 * RUN_QUEUED_WORD, RUN_RUNNING_WORD, RUN_LOST_WORD and RUN_DONE_WORD are the
 * console's own words for the four states, declared once each so that the copy a
 * person reads and the word the union names cannot drift. They are this file's
 * vocabulary and not a document's: `stateWordInto` is the only thing that draws
 * them, and it draws nothing else.
 */
const RUN_QUEUED_WORD = 'queued'
const RUN_RUNNING_WORD = 'running'
const RUN_LOST_WORD = 'lost'
const RUN_DONE_WORD = 'done'

/** MARK_PAST, MARK_CURRENT, MARK_FUTURE and MARK_DANGER are the four things a
 * pipeline segment can be, spelled once. They are the console's own words --
 * `pipelineOf` assigns them and nothing a document carries reaches them -- and
 * they are drawn through an if-chain rather than interpolated so that each is a
 * named constant a reader can find. */
const MARK_PAST = 'past'
const MARK_CURRENT = 'current'
const MARK_FUTURE = 'future'
const MARK_DANGER = 'danger'

/** stateWordInto writes the console's word for a run's state into an element. */
function stateWordInto(element: ScreenElement, state: RunState): void {
  if (state === 'queued') literal(element, RUN_QUEUED_WORD)
  else if (state === 'lost') literal(element, RUN_LOST_WORD)
  else if (state === 'done') literal(element, RUN_DONE_WORD)
  else literal(element, RUN_RUNNING_WORD)
}

/**
 * reviewRowOf builds one row of the review inbox from a `ready[]` entry.
 *
 * Every field is copied and none is derived: `repo`, `title`, `pr_number`,
 * `pr_url` and `age_seconds` are the document's own, and the screen draws them
 * -- the URL through `putText`'s `url` class, which redacts a token-shaped run
 * and refuses a URL that carries a credential -- rather than parsing a link out
 * of any of them (design F26; v1 section 5.1 rule 2). The title is a job's own
 * text, so it is drawn as a `sentence` like every other free string.
 *
 * The number is the one field that is **filtered**, and `prNumberAt` says why.
 */
function reviewRowOf(finished: FinishedView): LiveReviewRow {
  return {
    run: finished.run,
    repo: finished.repo,
    title: finished.title,
    prNumber: prNumberAt(finished.prNumber),
    prUrl: finished.prUrl,
    age: finished.ageSeconds,
  }
}

/** prNumberAt is a pull request's number as a row may draw one: a positive
 * integer, or nothing. `schema.ts`'s `optionalInt` guarantees a finite number
 * and not that it is a whole one, and a number is drawn here as **text** --
 * `#1.5`, `#0` and `#-3` are not pull requests, and a row that drew one would
 * be naming something that does not exist. It is the same rule `run.ts`'s
 * `numberAt` keeps for `pr`/`pr_number` (that screen draws the same fact from a
 * `runview.Row`), written here rather than imported because the screens are
 * siblings and none owns the others. */
function prNumberAt(raw: number | null): number | null {
  if (raw === null || !Number.isInteger(raw) || raw <= 0) return null
  return raw
}

/** silenceStamp is where a silence ends **on the CLI's clock**: the stamp of
 * the last document read plus the silence the console measured since it
 * arrived. The silence is a duration, so no offset between the two clocks can
 * move it; an unparseable stamp or an unmeasured silence leaves the gap at the
 * document's own stamp. */
function silenceStamp(now: string, seconds: number | null): string {
  const when = Date.parse(now)
  if (Number.isNaN(when) || seconds === null || seconds <= 0) return now
  return new Date(when + seconds * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z')
}

/** NO_RIGOR is the join a caller that has none passes: every row draws no chip.
 * It is a shared empty map rather than a fresh one per call because it is
 * read-only and never written -- `rigorIndex` (`shared/rigor.ts`) is what
 * builds the map a caller does pass. */
const NO_RIGOR: ReadonlyMap<string, RigorView> = new Map()

/**
 * buildLiveModel is one frame as the screen's data.
 *
 * `history` is the burn window the caller is keeping across frames: this
 * function **observes** the frame into it, which is what makes a replay's
 * sparkline roll rather than repeat. A caller drawing a single frame passes a
 * fresh one and gets a one-bar sparkline, which is the truth about a console
 * that has seen one document.
 *
 * `rigor` is the Live screen's one **join**, keyed by run id: the frame is
 * `JSONRun`, which carries no rigor block, so the mode on a row comes from
 * `runs ls --json` and is matched here (`rigorIndex`). A caller that has not
 * made that read passes nothing, and every row is then drawn with no chip --
 * which is the same cell a run with no block draws, and the reason the
 * parameter is a map rather than a reason string: the screen is not degraded by
 * a join it does not have, it simply says less.
 */
export function buildLiveModel(
  document: LiveDocument,
  history: BurnHistory = new BurnHistory(),
  feed: FeedHealth | null = null,
  rigor: ReadonlyMap<string, RigorView> = NO_RIGOR,
): LiveModel {
  // Every run row is built once and the row lists and the fleet's counts are
  // both read off these, so the panel counts exactly the runs the screen draws:
  // a run the console could not place under a repository is in `orphans` and is
  // still a run that is spending.
  const runRows = document.runs.map((run) => ({ slug: run.slug, row: rowOf(run, rigor) }))

  const repos: LiveRepoRow[] = document.repos.map((repo) => {
    const burn = burnReading(repo.burn)
    // The document's sample is observed once, at the document's own stamp, and
    // `observe` drops a second one at the same stamp: a stale rebuild is the
    // last document read again, not a new measurement, and must not redraw it
    // after the gap.
    history.observe(repo.slug, { at: document.now, reading: burn })
    // A feed that has gone quiet is a **gap** in every series, not a flat line
    // and not a repeat of the last sample: the console stopped observing, and
    // `seriesSegments` splits the sparkline where it did (design section 6.3,
    // "the graphs break rather than interpolate"). The last known sample stays
    // where it is, which is what makes the break visible at all.
    //
    // Two clocks meet here: `document.now` is the CLI's and `feed.at` is the
    // console's. The rule (review round 3 of PR 494): **every sample lives on
    // the CLI's clock**, and the console's clock only decides *whether* the
    // feed is stale. The gap is stamped at the last document's stamp plus the
    // silence the console measured (`silenceStamp`), and the window ends there.
    // A console clock behind the CLI's cannot put the gap before the last bar,
    // and one ahead of it -- by any amount -- cannot stamp a gap in the CLI's
    // future that would prune every fresh document on arrival. The banner still
    // shows the console's own stamp (`feed.at`), which is the reading it took.
    let at = document.now
    if (feed !== null && feed.stale) {
      at = silenceStamp(document.now, feed.seconds)
      history.observe(repo.slug, {
        at,
        reading: gap(`no document has arrived for ${String(feed.seconds ?? 0)}s, so this series breaks here`),
      })
    }
    return {
      slug: repo.slug,
      repo: repo.repo,
      countedUsd: repo.countedUsd,
      spentUsd: repo.spentUsd,
      notionalUsd: notionalReading(repo.notionalUsd),
      dailyCapUsd: repo.dailyCapUsd,
      percent: repo.percent,
      burnPerMin: burn,
      burnFast: repo.burn.fast,
      spark: sparkline(history, repo.slug, at),
      kill: repo.kill,
      runs: runRows.filter((entry) => entry.slug === repo.slug).map((entry) => entry.row),
    }
  })

  const known = new Set(document.repos.map((repo) => repo.slug))
  const orphans = runRows.filter((entry) => !known.has(entry.slug)).map((entry) => entry.row)

  // The fleet's counts, over the rows above. A queued run has no lease and so is
  // not active; `lost` is the flag `healthFlag` draws for the document's own
  // `health: 'lost'`, and its being a *flag* here rather than a reading of the
  // enum is what keeps the one threshold (`LIVE_LOST_AFTER_SECONDS`) in the one
  // place that owns it.
  let active = 0
  let queued = 0
  let heartbeatLost = false
  for (const entry of runRows) {
    if (entry.row.queued) queued += 1
    else active += 1
    if (entry.row.flags.some((flag) => flag.name === 'lost')) heartbeatLost = true
  }

  // The review list is the document's own order -- `ReadyForReview` sorts it
  // newest first (`internal/watch/filter.go`), and a console that sorted again
  // could disagree with the CLI's own view about which row is first. The count
  // is taken **before** the cap, so the section can say how many it did not
  // draw rather than quietly showing a shorter list.
  const review = document.ready.map(reviewRowOf)

  const age = document.connection.ageSeconds
  return {
    project: document.project,
    mode: document.mode,
    day: document.day,
    now: document.now,
    connection: {
      state: document.connection.state,
      reason: document.connection.reason === '' ? null : document.connection.reason,
      age,
      // The CLI's `state` is the authority on the word; this is the console's
      // own reading of the age it was handed, and it is `>` because
      // `State.conn` is (`internal/watch/state.go:226`).
      staleByAge: age.value !== null && age.value > LIVE_STALE_AFTER_SECONDS,
    },
    total: { ...document.total, notionalUsd: notionalReading(document.total.notionalUsd) },
    fleet: {
      headroom: headroomOf(document.total.dailyCapUsd, document.total.countedUsd),
      // The fleet's rate is the *total's* burn and not a repository's: the panel
      // is the whole fleet's, and a rate summed from the rows would be the
      // console's arithmetic rather than the CLI's measurement.
      burn: burnReading(document.total.burn),
      active,
      queued,
      heartbeatLost,
    },
    repos,
    orphans,
    review: review.slice(0, LIVE_REVIEW_CAP),
    reviewCount: review.length,
    queuedNote: document.queuedNote,
    feed,
  }
}

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

/**
 * ScreenElement is what a screen may draw into: ElementLike's text slot and
 * attributes (Task 2.0's `app.ts`), plus a tag, children and `append`.
 *
 * It is deliberately no wider. There is no markup-parsing slot, no navigating
 * attribute, no event handler and **no style**: the design's density rules are
 * the stylesheet's, and `style-src 'self'` refuses an inline style anyway, so a
 * screen that wanted one would have to widen this interface first -- in a diff a
 * reviewer can see -- rather than reach for it quietly.
 */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

/** ScreenFactory is where elements come from. It is an argument rather than a
 * `document` reference so that the screen can be drawn, and asserted on, in a
 * test with no window at all. */
export interface ScreenFactory {
  create(tag: string): ScreenElement
}

/**
 * LiveFactory is a `ScreenFactory` that can also make an element in the **SVG**
 * namespace: the cap bar's segments are geometry, and an element made with
 * `createElement` is in the HTML namespace and is not drawn inside an `<svg>` at
 * all.
 *
 * It is a second interface rather than a method added to `ScreenFactory` for the
 * reason `graph.ts`'s `GraphFactory` states: the two namespaces are two
 * different calls in the DOM (`createElement` and `createElementNS`), and
 * `spend.test.ts` holds the screens' factories assignable to each other in both
 * directions -- a base factory that had grown an SVG method would make that
 * guard fail for a screen that draws no SVG. `entry.ts`'s `DomScreenFactory`
 * answers both, and wraps one real node either way.
 */
export interface LiveFactory extends ScreenFactory {
  createNS(tag: string): ScreenElement
}

// The attributes a screen may set are `class`, `title` and `data-*`, the
// accessibility attributes, and -- on an SVG node alone -- the presentation
// attributes its geometry needs: the first three are how a stylesheet and a
// tooltip find a node and where a reading goes when it is a shape rather than a
// sentence, and the last two cannot make the window do anything. `live.test.ts`
// asserts every attribute of every drawn node is one of them, over a fake
// element whose navigating, resource, `style` and `on*` attributes throw.

/** make is a fresh element with its classes. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  return element
}

/** makeNS is `make` in the SVG namespace, for the geometry a screen draws
 * rather than the elements it arranges. The factory is narrowed per call so the
 * class is still written in the one place `make` writes it: a second join of the
 * same list is a second line the class travels through. */
function makeNS(factory: LiveFactory, tag: string, ...classes: string[]): ScreenElement {
  return make({ create: (name) => factory.createNS(name) }, tag, ...classes)
}

/**
 * literal writes the console's own vocabulary -- "KILLED by", a column heading
 * -- into an element. It is not `putText`: `putText` is the door a *document's*
 * string comes through, and running the console's own words through the
 * sanitizer would invite a reader to think a screen may write one without it.
 * Nothing a job wrote ever reaches this function, and that is a property of
 * this file rather than of the string.
 */
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

/** numberCell is `cell` with a reading written into it through `putReading`, so
 * the number cannot be drawn without its state. */
function numberCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  reading: Reading,
  unit: Parameters<typeof putReading>[2],
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, 'live-num', ...classes)
  putReading(element, reading, unit)
  return element
}

/**
 * sanitizedKey is a document string used as an **attribute value** rather than
 * as text -- a repository's slug, which is the identity a test and a later
 * screen need. It goes through the same sanitizer every other string does, and
 * a refusal yields the empty string: an attribute is not a place to draw `n/a`,
 * and a node whose key is empty is a node a caller cannot mistake for a matched
 * one.
 */
function sanitizedKey(raw: string): string {
  const result = sanitizeText(raw, 'slug', 'slug')
  return result.ok ? result.text : ''
}

/** bar draws a spend-against-cap bar. A reading with no value is drawn as its
 * own mark and carries its state in `data-state`, so a bar the console cannot
 * fill is visibly not a bar at zero. */
function bar(parent: ScreenElement, factory: ScreenFactory, reading: Reading, ...classes: string[]): ScreenElement {
  const element = cell(parent, factory, 'div', 'live-bar', ...classes)
  putReading(element, reading, 'percent')
  element.setAttribute('data-fill', formatReading(reading, 'percent'))
  element.setAttribute('data-state', reading.state)
  return element
}

/** killBanner is "KILLED by <who> <at> <reason>" (design section 6.2). */
function killBanner(factory: ScreenFactory, kill: KillView): ScreenElement {
  const element = make(factory, 'div', 'live-kill')
  element.setAttribute('data-killed', 'true')
  literal(cell(element, factory, 'span', 'live-kill-word'), 'KILLED by')
  textCell(element, factory, 'span', kill.by, 'person', 'kill.by', 'live-kill-by')
  textCell(element, factory, 'span', kill.at, 'label', 'kill.at', 'live-kill-at')
  textCell(element, factory, 'span', kill.reason, 'sentence', 'kill.reason', 'live-kill-reason')
  return element
}

/** spark draws the burn window's bars. */
function spark(factory: ScreenFactory, line: Sparkline): ScreenElement {
  const element = make(factory, 'ol', 'live-burn')
  element.setAttribute('data-segments', String(line.segments))
  if (line.bars.length === 0) {
    // No sample at all is not an empty chart: it is the console saying it has
    // not seen a document for this repository yet.
    // availability.ts maps that to a gap (an em dash), so the mark, the state
    // and the title all come from one gap reading rather than from three
    // separate choices.
    const empty = cell(element, factory, 'li', 'live-burn-bar', 'live-num')
    const none = gap('no document has been seen for this repository yet')
    putReading(empty, none, 'percent')
    empty.setAttribute('data-state', none.state)
    return element
  }
  for (const point of line.bars) {
    const drawn = cell(element, factory, 'li', 'live-burn-bar', 'live-num')
    putReading(drawn, point.height, 'percent')
    drawn.setAttribute('data-height', formatReading(point.height, 'percent'))
    drawn.setAttribute('data-state', point.height.state)
    // The rate is what the bar means, so it is the tooltip -- except on a bar
    // that is a gap, where `putReading` has already written *why* the sample is
    // missing into exactly this attribute. `n/a` over a reason is the wrong way
    // round: the mark is drawn either way, and the sentence is the only thing
    // that tells a person which of the two facts they are looking at.
    if (point.height.value !== null) drawn.setAttribute('title', formatReading(point.rate, 'usd'))
  }
  return element
}

/**
 * wireOrUnavailable draws one of a run's own strings, and the console's
 * unavailable mark when the feed carries none.
 *
 * The CLI writes its own dash for a field it has nothing for (`dash()` in
 * `internal/watch/build.go`, which is why a queued row reads `-` for its stage,
 * round and models), and that is a value the document chose, drawn as itself.
 * An **empty** value is the other case: it is a key the document left out of
 * this run, and `n/a` with the field's name in the `title` is what every other
 * absent value on this screen says. It goes through `putReading` because that is
 * the one writer that draws the console's mark and its reason together;
 * `formatReading` answers the mark for a reading with no value whatever its
 * unit.
 */
/** putActor writes one of the two actors a run's feed recorded -- its coder and
 * its reviewer -- or the console's own `n/a` with the reason in the `title` when
 * the feed carried none.
 *
 * **`n/a` and never an empty cell.** A missing model is not a model that ran for
 * nothing: the cell says the question was asked and could not be answered, which
 * is what `putReading` writes and what an empty span would not (design section
 * 6.1 rule 1). Nothing here is invented -- `models` and `auth` are the two
 * fields the frame carries. */
function putActor(element: ScreenElement, raw: string, field: string): void {
  if (raw === '') {
    putReading(element, unavailable(`the document carries no ${field}`), 'count')
    return
  }
  putText(element, raw, 'label', field)
}

/**
 * pipelineCell draws the PIPELINE column: the five segments, then the run's own
 * line under them.
 *
 * **The segments are decoration and the line is the meaning.** They are a
 * `aria-hidden` list -- a shape a reader cannot inspect is better hidden than
 * announced as five unlabelled marks -- and the words under them say the same
 * thing in the console's own text: the CLI's stage, the round it is on, and the
 * run's state. That is design section 3.5's rule taken at its word: no colour
 * here carries anything the text does not.
 *
 * A stage this build does not know lights no segment at all (`pipelineOf`) and
 * its own word is still drawn, so a newer CLI's stage appears as text on a bar
 * with nothing lit rather than as a bar that guessed.
 */
function pipelineCell(factory: ScreenFactory, row: LiveRunRow, state: RunState): ScreenElement {
  const element = make(factory, 'td', 'live-pipeline')

  const segments = make(factory, 'ol', 'live-pipeline-segments')
  segments.setAttribute('aria-hidden', 'true')
  for (const segment of pipelineOf(row)) {
    // **The mark is `data-mark` and not part of the class name**, which is the
    // shape every other tone on this tree uses (the fleet bar's `data-account`,
    // a row's `data-tone`): one class in common and one attribute a rule reads
    // the state off. The segment's own name is written nowhere: the bar is
    // decorative, and the stage the run is at is text in the line under it.
    const drawn = cell(segments, factory, 'li', 'live-pipeline-segment')
    // The mark is written here rather than in a helper so the element and the
    // attribute are one statement apart: a reader checking which elements carry
    // `data-mark` reads this file, and a helper would put the answer a call away.
    if (segment.mark === 'past') drawn.setAttribute('data-mark', MARK_PAST)
    else if (segment.mark === 'current') drawn.setAttribute('data-mark', MARK_CURRENT)
    else if (segment.mark === 'danger') drawn.setAttribute('data-mark', MARK_DANGER)
    else drawn.setAttribute('data-mark', MARK_FUTURE)
  }
  element.append(segments)

  const when = cell(element, factory, 'div', 'live-run-when')
  textCell(when, factory, 'span', row.stage, 'label', 'runs[].stage', 'live-run-stage')
  literal(cell(when, factory, 'span', 'live-run-dot'), RUN_DOT)
  literal(cell(when, factory, 'span', 'live-run-word'), RUN_ROUND_WORD)
  textCell(when, factory, 'span', row.round, 'label', 'runs[].round', 'live-run-round')
  literal(cell(when, factory, 'span', 'live-run-dot'), RUN_DOT)
  stateWordInto(cell(when, factory, 'span', 'live-run-state'), state)
  return element
}

/**
 * statusCell draws the STATUS column: the run's chip, the flags the CLI set
 * beside it, and -- on a run whose heartbeat is lost -- the Reap button.
 *
 * **The chip's tone is the row's condition class and not an attribute.** The
 * four states are already the row's own (`live-run-queued`, `live-run-lost`,
 * `live-run-done`, and running for a row with none), so the stylesheet paints
 * one chip from one rule set and the state has no second spelling to drift from.
 *
 * The flags are the CLI's own conclusions, drawn as words with their threshold
 * in the `title`, exactly as they were before the row became a table: they moved
 * into this cell and did not disappear.
 */
function statusCell(factory: ScreenFactory, row: LiveRunRow, state: RunState): ScreenElement {
  const element = make(factory, 'td', 'live-run-status')

  const chip = cell(element, factory, 'span', 'live-run-chip')
  if (state === 'lost') {
    // The pip is the chip's decorative half: a lost heartbeat is the one state
    // the design gives a mark beside its word, and the word is still the whole
    // of the meaning (styling design section 3.5).
    const pip = cell(chip, factory, 'span', 'live-run-pip')
    pip.setAttribute('aria-hidden', 'true')
    literal(pip, RUN_PIP)
  }
  stateWordInto(chip, state)

  for (const flag of row.flags) {
    const drawn = cell(element, factory, 'span', 'live-run-flag')
    literal(drawn, flag.name)
    drawn.setAttribute('data-flag', flag.name)
    drawn.setAttribute('data-tone', flag.tone)
    drawn.setAttribute('title', flag.because)
  }
  if (row.contradiction !== null) {
    const drawn = cell(element, factory, 'span', 'live-run-contradiction', 'live-num')
    putText(drawn, row.contradiction, 'sentence', 'runs[].health')
    drawn.setAttribute('data-contradiction', 'true')
  }
  if (state === 'lost') {
    // Drawn disabled and inert (`writes.ts`): the redesign draws Reap where a
    // lost run is, and this console performs no write but launch-from-console.
    element.append(disabledWrite(factory, 'live-reap', 'Reap'))
  }
  return element
}

/**
 * runRow draws one run of a repository, as one row of its table.
 *
 * **Seven columns, and the row is the door.** The RUN cell carries
 * `live-run-id`, which is what `entry.ts`'s `wireRuns` reads to open Run detail
 * -- the cell's own drawn text and nothing else, so a run id the sanitizer
 * refused opens nothing. The rest is the design's table: the title (with the
 * rigor chip beside it), the pipeline, the two models the feed recorded, the
 * spend, the age as a duration, and the state with its flags.
 */
function runRow(factory: ScreenFactory, row: LiveRunRow): ScreenElement {
  const state = runStateOf(row)
  const classes = ['live-run']
  if (row.queued) classes.push('live-run-queued')
  if (row.stuck) classes.push('live-run-stuck')
  if (state === 'lost') classes.push('live-run-lost')
  if (state === 'done') classes.push('live-run-done')
  const element = make(factory, 'tr', ...classes)
  element.setAttribute('data-tone', row.tone)
  element.setAttribute('data-queued', row.queued ? 'true' : 'false')

  textCell(element, factory, 'td', row.run, 'runId', 'runs[].run', 'live-run-id')

  const task = textCell(element, factory, 'td', row.title, 'sentence', 'runs[].title', 'live-run-title')
  if (row.rigor !== null) modeChip(task, factory, row.rigor)

  element.append(pipelineCell(factory, row, state))

  const actors = cell(element, factory, 'td', 'live-run-coder')
  putActor(cell(actors, factory, 'span', 'live-run-models'), row.models, 'runs[].models')
  putActor(cell(actors, factory, 'span', 'live-run-auth'), row.auth, 'runs[].auth')

  numberCell(element, factory, 'td', row.spend, 'usd', 'live-run-spend')
  // The age is a **span**, not a count of seconds: `duration` is the unit a
  // person reads (`29m 21s`), and the raw second count `seconds` draws is the
  // measurement rather than the answer.
  numberCell(element, factory, 'td', row.age, 'duration', 'live-run-age')

  element.append(statusCell(factory, row, state))
  return element
}

/** RUN_DOT is the separator the pipeline's text line is written with, once, so
 * the two dots are the same character rather than two that look alike. */
const RUN_DOT = '·'

/** RUN_ROUND_WORD is the word in front of a run's round number. */
const RUN_ROUND_WORD = 'round'

/** RUN_PIP is the mark a lost heartbeat's chip carries beside its word. It is
 * decoration and says nothing the word does not: the chip is `aria-hidden`'s
 * opposite -- a reader hears `lost` -- and a pip alone would be exactly the
 * colour-only state the design forbids. */
const RUN_PIP = '●'

/** runTableHead draws the table's seven column headings. */
function runTableHead(factory: ScreenFactory): ScreenElement {
  const head = make(factory, 'thead')
  const row = cell(head, factory, 'tr')
  literal(cell(row, factory, 'th', 'live-table-head'), 'RUN')
  literal(cell(row, factory, 'th', 'live-table-head'), 'TASK')
  literal(cell(row, factory, 'th', 'live-table-head'), 'PIPELINE')
  literal(cell(row, factory, 'th', 'live-table-head'), 'CODER / REVIEWER')
  literal(cell(row, factory, 'th', 'live-table-head'), 'COST')
  literal(cell(row, factory, 'th', 'live-table-head'), 'ELAPSED')
  literal(cell(row, factory, 'th', 'live-table-head'), 'STATUS')
  return head
}

/**
 * runTable draws a runs table: the headings and one row per run.
 *
 * **The box that scrolls is drawn by the caller** and the table goes inside it,
 * because the two boxes on this screen have different grounds and because the
 * class belongs to the caller that knows which one it is drawing (`live-runs`
 * inside a project, `live-orphans` for the repo-less rows).
 *
 * A repository with no run draws the headings and no row: an empty table says
 * "this project has nothing running", which is a fact, where a table that
 * vanished would be a fact the reader has to infer.
 */
function runTable(factory: ScreenFactory, rows: readonly LiveRunRow[]): ScreenElement {
  const table = make(factory, 'table', 'live-table')
  table.append(runTableHead(factory))
  const body = cell(table, factory, 'tbody')
  for (const row of rows) body.append(runRow(factory, row))
  return table
}

/** modeChip is one run's rigor mode, as the single mark a Live row has room
 * for -- the whole answer is on the run's own screen (`run.ts`'s `rigorLine`),
 * and the Projects screen's MODE cell is the same word in a table.
 *
 * **The word is the chip, and there is no second channel.** The five modes are
 * a dial with no colour of its own anywhere in this tree -- not in the CLI's own
 * MODE column, not in `docs/rigor-modes` -- so a chip that painted one mode
 * differently from another would be inventing a meaning, and the stylesheet's
 * rule is that every colour meaning carries a text mark (styling §3.5). Here
 * the text is the whole of it: a bounded, small mark that separates the mode
 * from the title and the stage around it.
 *
 * A mode this build has never heard of is the document's own word, through
 * `putText` with the `label` class and the `runs[].rigor.mode` field -- bounded,
 * escaped, and `n/a` when the sanitizer will not have it. A mode a newer CLI
 * resolved is not a reason to draw nothing.
 *
 * A run whose record carries no block draws **no chip at all**, not an empty
 * one: the row is about what the run is doing, and a mark for a fact nobody
 * recorded would be a mark the reader has to interpret.
 */
function modeChip(parent: ScreenElement, factory: ScreenFactory, rigor: RigorView): void {
  const drawn = cell(parent, factory, 'span', 'live-run-mode')
  const word = rigorModeWord(rigor.mode)
  if (word === null) {
    putText(drawn, rigor.mode, 'label', 'runs[].rigor.mode')
    return
  }
  literal(drawn, word)
}

/** PANEL_NAME is a project panel's accessible name. The caption inside it says
 * which project; a section's name is what a reader arriving at it hears first,
 * so it says what the whole panel is. */
const PANEL_NAME = 'Project runs'

/** PANEL_CAPTION, REPO_SPENT_WORD, REPO_COUNTED_WORD and REPO_BURN_WORD are the
 * panel's own words: the caption over the repository's identity and the three
 * labels over its figures. They are the console's vocabulary, and each one is
 * drawn through `literal` and nothing else. */
const PANEL_CAPTION = 'PROJECT'
const REPO_SPENT_WORD = 'spent'
const REPO_COUNTED_WORD = 'counted'
const REPO_BURN_WORD = 'burn'

/** REPO_SEP is the mark between what a project counted and the cap it counted
 * against. */
const REPO_SEP = '/'

/**
 * repoHead draws a project panel's header: the caption, the repository's own
 * identity, and its figures.
 *
 * **`spent` is the notional reading, and the tilde is what makes that honest.**
 * The figure is the repository's `notional_usd` -- a subscription's list price,
 * billed to nobody and counted against no cap (design section 6.1 rule 1) -- and
 * it is drawn through `formatReading`, so it arrives already marked `~$`. The
 * word beside it says what the account is and the mark says what the dollars
 * are, and neither is left to the colour of the number.
 *
 * The bar and the burn window sit under the figures rather than in them: they
 * are the same readings drawn as shapes, and a shape inside the figures line
 * would be a second place for the same number to disagree with itself.
 */
function repoHead(factory: ScreenFactory, repo: LiveRepoRow): ScreenElement {
  const element = make(factory, 'header', 'live-repo-head')
  literal(cell(element, factory, 'span', 'live-repo-label'), PANEL_CAPTION)

  // `repoDisplayName` (internal/watch/build.go) falls back to the slash-free
  // slug when the host's config does not name the repository: the slash decides
  // the class, as `spend.ts`'s `keyClass` does (review round 2 of PR 494).
  textCell(element, factory, 'span', repo.repo, repo.repo.includes('/') ? 'repository' : 'slug', 'repos[].repo', 'live-repo-name')

  const figures = cell(element, factory, 'div', 'live-repo-figures')

  const spent = cell(figures, factory, 'span', 'live-repo-figure')
  literal(cell(spent, factory, 'span', 'live-repo-figure-label'), REPO_SPENT_WORD)
  numberCell(spent, factory, 'span', repo.notionalUsd, 'usd', 'live-repo-notional')

  const counted = cell(figures, factory, 'span', 'live-repo-figure')
  literal(cell(counted, factory, 'span', 'live-repo-figure-label'), REPO_COUNTED_WORD)
  numberCell(counted, factory, 'span', repo.countedUsd, 'usd', 'live-repo-counted')
  literal(cell(counted, factory, 'span', 'live-repo-sep'), REPO_SEP)
  numberCell(counted, factory, 'span', repo.dailyCapUsd, 'usd', 'live-repo-cap')

  const burn = cell(figures, factory, 'span', 'live-repo-figure')
  literal(cell(burn, factory, 'span', 'live-repo-figure-label'), REPO_BURN_WORD)
  numberCell(burn, factory, 'span', repo.burnPerMin, 'usd', 'live-burn-rate')
  labelBurnRate(burn, factory, repo)

  // Halt project, drawn disabled and inert (`writes.ts`): the redesign draws the
  // control a project's owner expects, and this console performs no write but
  // launch-from-console.
  figures.append(disabledWrite(factory, 'live-halt', 'Halt project'))
  return element
}

/**
 * repoBlock draws one project: its header, its bar, its burn window, its kill
 * banner and its runs table.
 *
 * **The panel is a `section` with a name**, so a reader arriving at it hears
 * what it is before hearing what is in it, and the runs table is inside it with
 * its own scrolling box (`runTable`).
 */
function repoBlock(factory: ScreenFactory, repo: LiveRepoRow): ScreenElement {
  const element = make(factory, 'section', 'live-repo')
  element.setAttribute('aria-label', PANEL_NAME)
  element.setAttribute('data-key', sanitizedKey(repo.slug))
  element.setAttribute('data-killed', repo.kill === null ? 'false' : 'true')

  element.append(repoHead(factory, repo))

  const meters = cell(element, factory, 'div', 'live-repo-meters')
  bar(meters, factory, repo.percent, 'live-repo-percent')
  meters.append(spark(factory, repo.spark))

  if (repo.kill !== null) element.append(killBanner(factory, repo.kill))
  const runs = cell(element, factory, 'div', 'live-runs')
  runs.append(runTable(factory, repo.runs))
  return element
}

/** labelBurnRate names the unit of the rate beside it, in the console's own
 * words rather than concatenated onto a reading: `$0.02/min` would be a
 * formatted reading with something appended, which is how a number gets drawn
 * without its state. */
function labelBurnRate(parent: ScreenElement, factory: ScreenFactory, repo: LiveRepoRow): void {
  const element = cell(parent, factory, 'span', 'live-burn-unit')
  literal(element, 'per min')
  element.setAttribute('data-fast', repo.burnFast ? 'true' : 'false')
}

/** header draws the project line: project, mode, day, connectivity. */
function header(factory: ScreenFactory, model: LiveModel): ScreenElement {
  const element = make(factory, 'header', 'live-header')
  const heading = cell(element, factory, 'h1', 'live-project')
  putText(heading, model.project, 'project', 'project')
  textCell(element, factory, 'span', model.mode, 'label', 'mode', 'live-mode')
  textCell(element, factory, 'span', model.day, 'label', 'day', 'live-day')

  const connection = textCell(
    element,
    factory,
    'span',
    model.connection.state,
    'label',
    'connection.state',
    'live-connection',
  )
  // `data-state` is the state **as drawn**, read back off the cell, and not the
  // document's raw string: a state `putText` refused is drawn as `n/a`, and an
  // attribute echoing the raw value would put back on screen exactly what the
  // refusal kept off it -- the whole reason `app.ts` has writers and this file
  // has `sanitizedKey`.
  connection.setAttribute('data-state', connection.textContent ?? '')
  connection.setAttribute('data-stale-by-age', model.connection.staleByAge ? 'true' : 'false')
  numberCell(element, factory, 'span', model.connection.age, 'seconds', 'live-connection-age')
  if (model.connection.reason !== null) {
    textCell(element, factory, 'span', model.connection.reason, 'sentence', 'connection.reason', 'live-connection-reason')
  }
  if (model.queuedNote !== null) {
    // The one-line reason the runs bucket could not be read for queued rows
    // (`JSONDoc.QueuedNote`): without it, a screen with no queued rows looks
    // like a project with none, which is a different fact.
    textCell(element, factory, 'p', model.queuedNote, 'sentence', 'queued_note', 'live-note')
  }
  if (model.feed !== null) element.append(sinceLine(factory, model.feed))
  if (model.feed !== null && model.feed.stale) element.append(staleBanner(factory, model.feed))
  return element
}

/** sinceLine is when the values on this screen were last read: the arrival
 * stamp of the newest frame the console holds. It is drawn whenever a caller
 * keeps a feed reading, because design section 6.3's stale condition is "the
 * last-known values stay, **with their timestamps**" -- and a timestamp is only
 * honest if it is drawn before the values are stale too. */
function sinceLine(factory: ScreenFactory, feed: FeedHealth): ScreenElement {
  const element = make(factory, 'p', 'live-since')
  literal(cell(element, factory, 'span', 'live-since-word'), 'last read')
  textCell(element, factory, 'span', feed.since, 'label', 'feed.since', 'live-since-at')
  numberCell(element, factory, 'span', feedReading(feed), 'seconds', 'live-since-age')
  return element
}

/**
 * staleBanner is design section 6.3's stale condition: the console has not
 * heard from the CLI for longer than `watch.StaleAfter`, so it says so, with
 * the silence it measured and the clock reading it measured it at.
 *
 * The figure is drawn through `putReading` like every other number, and the two
 * literals around it are the console's own words -- including the threshold,
 * which is this file's `LIVE_STALE_AFTER_SECONDS`, interpolated from the
 * constant rather than typed as a second copy of it.
 */
function staleBanner(factory: ScreenFactory, feed: FeedHealth): ScreenElement {
  const element = make(factory, 'div', 'live-stale')
  element.setAttribute('data-stale', 'true')
  // `data-silence` is the measured silence as drawn, so a test reads the number
  // the screen drew rather than recomputing it from the stamps.
  element.setAttribute('data-silence', formatReading(feedReading(feed), 'seconds'))
  literal(cell(element, factory, 'span', 'live-stale-word'), 'no document has arrived for')
  numberCell(element, factory, 'span', feedReading(feed), 'seconds', 'live-stale-silence')
  literal(cell(element, factory, 'span', 'live-stale-threshold'), STALE_BANNER_TAIL)
  textCell(element, factory, 'span', feed.at, 'label', 'feed.at', 'live-stale-at')
  return element
}

/** STALE_BANNER_TAIL is the one thing the banner says about the threshold. It
 * is a constant with the number interpolated once, so a reader who changes
 * `LIVE_STALE_AFTER_SECONDS` cannot leave the sentence claiming the old one. */
const STALE_BANNER_TAIL = ` (a feed is stale after ${String(LIVE_STALE_AFTER_SECONDS)} s), so the values above are the last ones read and the graphs break where the console stopped observing`

/** FLEET_BAR_WIDTH and FLEET_BAR_HEIGHT are the cap bar's own axis: a
 * `viewBox` in the drawing's units rather than pixels, scaled to the panel by
 * `preserveAspectRatio="none"`, so a segment's width is a share of the cap and
 * the bar can be any width the panel has. 100 is the whole of it -- every share
 * below is already a percentage of the cap, so no conversion sits between the
 * arithmetic and the shape. */
const FLEET_BAR_WIDTH = 100
const FLEET_BAR_HEIGHT = 18

/** FLEET_BAR_LENGTH, FLEET_BAR_THICKNESS and FLEET_BAR_BOX are those two
 * numbers as the attribute values the bar writes, spelled once: `drawGauge`
 * writes the same three, and a length written out at every call site is a
 * length that can disagree with itself. */
const FLEET_BAR_LENGTH = String(FLEET_BAR_WIDTH)
const FLEET_BAR_THICKNESS = String(FLEET_BAR_HEIGHT)
const FLEET_BAR_BOX = `0 0 ${FLEET_BAR_LENGTH} ${FLEET_BAR_THICKNESS}`

/** axis is a share of the cap as a coordinate on the bar's own axis: the
 * attribute of a `rect` is a string, and this is where the share the model
 * computed becomes the geometry the screen draws. */
function axis(share: number): string {
  return String(Math.round(share * FLEET_BAR_WIDTH * 100) / 100)
}

/** FLEET_SPENT_WORD, FLEET_LEASED_WORD, FLEET_HEADROOM_WORD and
 * FLEET_COUNTED_WORD are the legend's own words, and they are the design's:
 * "Spent" is billed money, "Leased" is reserved for an open lease, "Headroom" is
 * what is left of the cap, and "Counted" is the one figure the document carries
 * when it does not split the first two. */
const FLEET_SPENT_WORD = 'Spent'
const FLEET_LEASED_WORD = 'Leased'
const FLEET_HEADROOM_WORD = 'Headroom'
const FLEET_COUNTED_WORD = 'Counted'

/** FLEET_HERO_NOTE is why the hero number is not on the bar: notional dollars
 * are a subscription's list price, they are billed to nobody and count toward
 * no cap (design section 6.1 rule 1), so drawing them as a segment beside the
 * billed dollars would put two different kinds of money on one axis. */
const FLEET_HERO_NOTE = 'at subscription list price, outside the cap'

/** FLEET_FULL_SENTENCE is the design's full-cap banner, with one word changed
 * and said here rather than left to be found: the design's hand-off says "fully
 * leased", and `counted_usd` is *open leases plus settled spend*, so a fleet that
 * has settled its leases and spent the cap is at its cap with nothing leased at
 * all. "Committed" is the word that is true of both. */
const FLEET_FULL_SENTENCE =
  'the cap is fully committed: new runs wait in the queue until a lease settles'

/** FLEET_CAP_LABEL is the caption over the cap line. */
const FLEET_CAP_LABEL = 'GLOBAL CAP · TODAY'

/** FLEET_NAME is the panel's accessible name. The caption above says which cap
 * this is; a section's name is what a reader arriving at it hears first, so it
 * says what the whole panel is. */
const FLEET_NAME = 'Fleet budget'

/** FLEET_STAT_LABELS are the three stat cells' captions, in drawing order. */
const FLEET_BURN_WORD = 'BURN RATE'
const FLEET_ACTIVE_WORD = 'ACTIVE'
const FLEET_QUEUED_WORD = 'QUEUED'
const FLEET_BURN_UNIT = '/min'
const FLEET_LOST_WORD = 'heartbeat lost'
const FLEET_WAITING_WORD = 'waiting for a lease'

/** FLEET_WARN_ICON is the banner's mark. It is decorative -- the sentence
 * beside it is the message -- so it is hidden from a reader who is listening
 * rather than looking, and the word is what carries the state (design section
 * 6.1 rule 3, "never colour alone"). */
const FLEET_WARN_ICON = '⚠'

/** FleetAccount is one money account of the panel: the word it is called by, the
 * reading behind it, and whether the bar draws a shape for it. */
interface FleetAccount {
  readonly name: 'counted' | 'spent' | 'leased' | 'headroom'
  readonly word: string
  readonly reading: Reading
  readonly drawn: boolean
}

/** FleetSegment is one account the bar draws, carrying the share of the cap it
 * occupies. */
interface FleetSegment extends FleetAccount {
  readonly share: number
}

/** fleetAccounts is the panel's money accounts, in drawing order: what is under
 * the cap, the lease beside it, and what is left of the cap.
 *
 * **The bar and the legend are both drawn from this one list**, so the legend
 * cannot come to describe a segment the bar does not draw.
 *
 * The CLI writes `leased_usd` on `total` and on every repository row (#1256), so
 * a document that carries it draws the three-way split. `schema.ts` still reads
 * it as optional (`optionalMoneyAt`), because a *recording* made before that
 * release does not carry it -- and on such a document the split cannot be drawn:
 * the bar then carries the one figure the document does hold -- `counted_usd` --
 * and the Leased row stays, unmeasured, as the one place the absence is visible.
 * Drawing "spent" alone and calling the rest leased would be inventing a division
 * the document does not carry.
 *
 * `counted_usd` is the sum of the split's two parts, so it is the account under
 * the cap in exactly one of the two shapes and never a rule of its own: adding
 * it beside spent and leased would put the same money on the axis twice.
 */
function fleetAccounts(model: LiveModel): readonly FleetAccount[] {
  const leased = model.total.leasedUsd
  const split = leased.value !== null
  return [
    split
      ? { name: 'spent', word: FLEET_SPENT_WORD, reading: model.total.spentUsd, drawn: true }
      : { name: 'counted', word: FLEET_COUNTED_WORD, reading: model.total.countedUsd, drawn: true },
    { name: 'leased', word: FLEET_LEASED_WORD, reading: leased, drawn: split },
    { name: 'headroom', word: FLEET_HEADROOM_WORD, reading: model.fleet.headroom, drawn: true },
  ]
}

/** fleetSegments is the accounts the bar draws, with each one's share of the cap.
 *
 * **The three money accounts are kept apart.** `counted_usd` is what is under
 * the cap; `notional_usd` is a list price that no cap counts and it is the hero
 * number instead of a segment; and a cap nobody read draws no segments at all --
 * an empty track, never a bar at zero.
 */
function fleetSegments(model: LiveModel): readonly FleetSegment[] {
  const cap = model.total.dailyCapUsd.value
  if (cap === null || cap <= 0) return []
  const share = (value: number | null): number =>
    value === null ? 0 : Math.min(1, Math.max(0, value / cap))
  return fleetAccounts(model)
    .filter((account) => account.drawn)
    .map((account) => ({ ...account, share: share(account.reading.value) }))
}

/** fleetBar draws the cap bar: the empty track, then one rect per segment with
 * the width its share of the cap asks for.
 *
 * The widths are clamped **cumulatively** as well as individually, so a document
 * whose parts do not add up -- a lease the CLI recorded beside a spend that
 * moved -- cannot draw segments reaching past the end of the cap's own axis. The
 * bar is `aria-hidden`: every value on it is written out in the legend, and a
 * shape a reader cannot inspect is better hidden than announced as an
 * unlabelled picture.
 */
function fleetBar(factory: LiveFactory, segments: readonly FleetSegment[]): ScreenElement {
  const element = makeNS(factory, 'svg', 'live-fleet-bar', 'live-total-percent')
  element.setAttribute('viewBox', FLEET_BAR_BOX)
  element.setAttribute('preserveAspectRatio', 'none')
  element.setAttribute('height', FLEET_BAR_THICKNESS)
  element.setAttribute('aria-hidden', 'true')

  const track = makeNS(factory, 'rect', 'live-fleet-track')
  track.setAttribute('width', FLEET_BAR_LENGTH)
  track.setAttribute('height', FLEET_BAR_THICKNESS)
  track.setAttribute('rx', '3')
  element.append(track)

  let drawn = 0
  for (const segment of segments) {
    const share = Math.min(segment.share, 1 - drawn)
    // The account is an attribute rather than part of the class name: the three
    // segments share one class so that `data-account` is where a rule reads
    // which money it is painting, the shape every other tone on this tree uses.
    const rect = makeNS(factory, 'rect', 'live-fleet-segment')
    rect.setAttribute('data-account', segment.name)
    // Rounded the way `drawGauge` rounds: a share of a 100-unit axis is a
    // fraction of a pixel nobody can see, and the rounding keeps a width from
    // being drawn as `10.000000000000002`.
    rect.setAttribute('x', axis(drawn))
    rect.setAttribute('width', axis(share))
    rect.setAttribute('height', FLEET_BAR_THICKNESS)
    element.append(rect)
    drawn += share
  }
  return element
}

/** fleetLegendRow is one legend row: the swatch for the shape the bar drew, the
 * account's word and its mono value.
 *
 * **The swatch is drawn only for an account the bar draws.** A swatch is a key:
 * a mark beside a word the bar never painted claims a colour that is nowhere on
 * the bar, and the Leased row is exactly that row on a document that does not
 * carry `leased_usd` -- its own `n/a`, with the reading's reason in the title,
 * is what says the question was asked and could not be answered.
 *
 * The swatch is a decorative mark and carries `aria-hidden`; the word and the
 * value are the text, which is what makes the bar's segments readable at all.
 */
function fleetLegendRow(factory: ScreenFactory, account: FleetAccount): ScreenElement {
  const element = make(factory, 'li', 'live-fleet-key')
  if (account.drawn) {
    const swatch = cell(element, factory, 'span', 'live-fleet-swatch')
    swatch.setAttribute('aria-hidden', 'true')
    swatch.setAttribute('data-account', account.name)
  }
  literal(cell(element, factory, 'span', 'live-fleet-key-word'), account.word)
  // An unmeasured value is drawn through `putReading` like every other number,
  // so it is `n/a` with its reason in the title; the class is what lets the
  // stylesheet paint it as the absent value it is, and it is written only when
  // there is no number to draw.
  numberCell(
    element,
    factory,
    'span',
    account.reading,
    'usd',
    ...(account.reading.value === null ? ['live-fleet-unread'] : []),
  )
  return element
}

/** fleetLegend is the legend under the bar: one row per money account, in the
 * order the bar draws them, and the bar's own list of accounts is what it walks.
 *
 * That list keeps the Leased row when the bar draws no lease, which is the
 * two-segment fallback's whole point: Leased is a segment of the design's bar
 * whether or not this CLI measures it, and its row -- the word, then `n/a` with
 * the reading's own reason in the title -- is what says the question was asked
 * and could not be answered. A legend built from the *segments* would drop the
 * row exactly when it is the only place the absence is visible.
 */
function fleetLegend(factory: ScreenFactory, model: LiveModel): ScreenElement {
  const element = make(factory, 'ul', 'live-fleet-legend')
  for (const account of fleetAccounts(model)) element.append(fleetLegendRow(factory, account))
  return element
}

/** fleetStatCell is one stat cell: its caption, and whatever the cell's value
 * builder appends under it through `put`. The caption is the console's own word
 * and the value is built by the caller so that a figure and the note beside it
 * -- "heartbeat lost", "waiting for a lease" -- are appended by the same code
 * that drew the number they qualify. */
function fleetStatCell(
  factory: ScreenFactory,
  caption: string,
  put: (parent: ScreenElement) => void,
): ScreenElement {
  const element = make(factory, 'div', 'live-fleet-stat')
  literal(cell(element, factory, 'span', 'live-fleet-stat-label'), caption)
  put(element)
  return element
}

/**
 * fleetStats draws the panel's three stat cells: the fleet's burn rate, and how
 * many runs are active and queued.
 *
 * **The two notes are not decorations, and neither is drawn when it is not true
 * of the number beside it.** A count of active runs with no word about a lost
 * heartbeat is a count that hides the one thing a person watching the fleet needs
 * to know; a queued count with no word about the lease says nothing about why
 * those runs are not moving; and a fleet with nothing queued is waiting for
 * nothing, so "QUEUED 0 waiting for a lease" would claim a queue that does not
 * exist. Both notes are the console's own sentences, drawn as text beside the
 * number rather than as a colour on it.
 */
function fleetStats(factory: ScreenFactory, model: LiveModel): ScreenElement {
  const element = make(factory, 'div', 'live-fleet-stats')

  element.append(
    fleetStatCell(factory, FLEET_BURN_WORD, (parent) => {
      const burn = numberCell(parent, factory, 'span', model.fleet.burn, 'usd', 'live-fleet-burn')
      literal(cell(burn, factory, 'span', 'live-fleet-burn-unit'), FLEET_BURN_UNIT)
    }),
  )

  element.append(
    fleetStatCell(factory, FLEET_ACTIVE_WORD, (parent) => {
      const active = numberCell(parent, factory, 'span', measured(model.fleet.active), 'count', 'live-fleet-active')
      if (model.fleet.heartbeatLost) {
        literal(cell(active, factory, 'span', 'live-fleet-lost'), FLEET_LOST_WORD)
      }
    }),
  )

  element.append(
    fleetStatCell(factory, FLEET_QUEUED_WORD, (parent) => {
      const queued = numberCell(parent, factory, 'span', measured(model.fleet.queued), 'count', 'live-fleet-queued')
      if (model.fleet.queued > 0) {
        literal(cell(queued, factory, 'span', 'live-fleet-waiting'), FLEET_WAITING_WORD)
      }
    }),
  )

  return element
}

/** fleetFullBanner is the design's full-cap banner: shown when the headroom is
 * *measured* and zero.
 *
 * "Measured" is load-bearing: a cap nobody read has no headroom either, and a
 * banner saying the cap is committed over a document that never carried one
 * would be the console inventing a cap it cannot see.
 */
function fleetFullBanner(factory: ScreenFactory): ScreenElement {
  const element = make(factory, 'p', 'live-fleet-full')
  const icon = cell(element, factory, 'span', 'live-fleet-warn-icon')
  icon.setAttribute('aria-hidden', 'true')
  literal(icon, FLEET_WARN_ICON)
  literal(cell(element, factory, 'span', 'live-fleet-full-word'), FLEET_FULL_SENTENCE)
  return element
}

/**
 * total draws the fleet budget panel: the redesign's total block.
 *
 * **Three accounts, and none of them added to another.** The cap line carries
 * today's cap; the hero carries notional spend, which is outside the cap and is
 * never a segment of the bar; and the bar carries what *is* under the cap --
 * spent, leased and headroom, or counted and headroom on a recording made before
 * `leased_usd` shipped. The legend repeats the bar's own values as text, so
 * the shape is not the only place a number lives.
 *
 * The kill banner is appended, as it was: this panel is where a fleet-wide kill
 * is announced, and the total is the only block that carries one.
 */
function total(factory: LiveFactory, model: LiveModel): ScreenElement {
  const element = make(factory, 'section', 'live-total', 'live-fleet')
  element.setAttribute('aria-label', FLEET_NAME)

  const wide = cell(element, factory, 'div', 'live-fleet-wide')

  const capLine = cell(wide, factory, 'p', 'live-fleet-cap')
  literal(cell(capLine, factory, 'span', 'live-fleet-cap-label'), FLEET_CAP_LABEL)
  numberCell(capLine, factory, 'span', model.total.dailyCapUsd, 'usd', 'live-cap')

  const hero = cell(wide, factory, 'p', 'live-fleet-hero')
  numberCell(hero, factory, 'span', model.total.notionalUsd, 'usd', 'live-total-notional')
  literal(cell(hero, factory, 'span', 'live-fleet-hero-note'), FLEET_HERO_NOTE)

  const segments = fleetSegments(model)
  wide.append(fleetBar(factory, segments))
  wide.append(fleetLegend(factory, model))

  const counted = cell(wide, factory, 'p', 'live-fleet-counted')
  literal(cell(counted, factory, 'span', 'live-fleet-key-word'), FLEET_COUNTED_WORD)
  numberCell(counted, factory, 'span', model.total.countedUsd, 'usd', 'live-counted')
  literal(cell(counted, factory, 'span', 'live-fleet-sep'), '·')
  numberCell(counted, factory, 'span', model.total.percent, 'percent', 'live-percent')

  if (model.fleet.headroom.value === 0) wide.append(fleetFullBanner(factory))
  if (model.total.kill !== null) wide.append(killBanner(factory, model.total.kill))

  element.append(fleetStats(factory, model))
  return element
}

/**
 * reviewSection draws the review inbox: the heading with its count, one row per
 * ready run, and the sentence about the link the console does not build.
 *
 * **The pull request is text and is not a control.** The number is drawn with
 * this screen's `literal` (it is an integer the model already filtered,
 * `prNumberAt` above), the URL goes through `putText` with the `url` class --
 * so a token-shaped run in it is redacted and a URL that carries a credential
 * is refused, drawn as the console's reason -- and neither is ever written into
 * `data-href`, which is the one attribute `src/renderer/app.ts` leaves a link
 * destination in and the only thing `link.openExternal` acts on. There is no
 * `<a>` here at all: not an inert one, and not one waiting for a provider,
 * because an element carrying a destination the console will not offer is a
 * control that does nothing (the rule `run.ts`'s `pullRequestPanel` states).
 * `review.test.ts` walks this section and asserts exactly that.
 *
 * The section is drawn whether or not it has a row: "nothing is waiting for
 * you" is an answer, and a section that vanishes when it is empty is a section
 * whose absence the operator has to interpret.
 */
function reviewSection(factory: ScreenFactory, model: LiveModel): ScreenElement {
  const element = make(factory, 'section', 'live-review')
  // **The heading is one text run, and the parentheses hug the number.** It is
  // the CLI's own line, `Ready for your review (%d)`, and the four elements a
  // word-plus-`(`-plus-count-plus-`)` drawing needs are four flex items -- the
  // heading's `gap` then spaces them out to `( 52 )`, which is text the CLI
  // never prints and both design docs disagree with (a 0.10.x review). One
  // run cannot be spaced out, and there is nothing to lose by it: the count is
  // a *reading* and it is drawn as one -- through `formatReading`, the only
  // function that turns a reading into the text a person reads -- but it is
  // `measured` and can be nothing else, because `reviewCount` is this
  // console's count of the list the document carries, and a document that
  // carried no list at all is refused by `schema.ts` before a screen sees it.
  const heading = cell(element, factory, 'h2', 'live-review-title')
  literal(heading, `${LIVE_REVIEW_TITLE} (${formatReading(measured(model.reviewCount), 'count')})`)

  const list = cell(element, factory, 'ol', 'live-review-list')
  for (const row of model.review) list.append(reviewRow(factory, row))
  if (model.reviewCount === 0) {
    literal(cell(element, factory, 'p', 'live-review-empty'), LIVE_REVIEW_EMPTY)
  }
  if (model.review.length > 0) {
    // Once per section rather than once per row: the reason is a fact about
    // every row here, and a sentence under each one would be the same sentence
    // eleven times.
    literal(cell(element, factory, 'p', 'live-review-note'), LIVE_PR_NO_LINK_REASON)
  }
  if (model.review.length < model.reviewCount) {
    // What was **not** drawn, in the shape `run.ts`'s log panel and
    // `projects.ts`'s job list use: the label, the number of rows the cap left
    // out, and the total the document carried. Drawing the number of rows that
    // *are* on the screen under this label would be the same count the heading
    // already carries, under a sentence saying the opposite of what it means.
    const note = cell(element, factory, 'p', 'live-review-capped')
    literal(note, 'rows not drawn:')
    numberCell(
      note,
      factory,
      'span',
      measured(model.reviewCount - model.review.length),
      'count',
      'live-review-dropped',
    )
    literal(cell(note, factory, 'span', 'live-review-of'), 'of')
    numberCell(note, factory, 'span', measured(model.reviewCount), 'count', 'live-review-total')
  }
  return element
}

/** reviewRow draws one finished run: its repository, its pull request as facts,
 * its title, its age and its id. The id is drawn (and goes through `putText`
 * with the `runId` class) because the row is a door to Run detail: the entry
 * wires a row to `diagnose RUN` by reading this cell's text, exactly as
 * `wireRuns` reads a live row's -- and a run id the sanitizer refused is drawn
 * as the unavailable mark, which matches no run in the model and so opens
 * nothing. */
function reviewRow(factory: ScreenFactory, row: LiveReviewRow): ScreenElement {
  const element = make(factory, 'li', 'live-review-row')
  // `repoDisplayName` falls back to the slash-free slug when the host's config
  // does not name the repository, as `repoBlock` says: the slash decides the
  // text class, and a slug is not a repository name.
  textCell(element, factory, 'span', row.repo, row.repo.includes('/') ? 'repository' : 'slug', 'ready[].repo', 'live-review-repo')
  if (row.prNumber !== null) {
    // A number is an identifier, not a measurement: drawn as text, with no link
    // and no reading (`run.ts`'s `pullRequestPanel`).
    literal(cell(element, factory, 'span', 'live-review-number'), `#${String(row.prNumber)}`)
  }
  if (row.prUrl !== null) {
    textCell(element, factory, 'span', row.prUrl, 'url', 'ready[].pr_url', 'live-review-url')
  }
  textCell(element, factory, 'span', row.title, 'sentence', 'ready[].title', 'live-review-run-title')
  numberCell(element, factory, 'span', row.age, 'seconds', 'live-review-age')
  textCell(element, factory, 'span', row.run, 'runId', 'ready[].run', 'live-review-run-id')
  return element
}

/**
 * renderLive draws the model and returns the tree it built, so a test can walk
 * what was drawn rather than read a window that no test can see.
 *
 * Every string a job wrote reaches the document through `putText` (and every
 * number through `putReading`); the only thing written directly is the
 * console's own vocabulary, through `literal`. No string is ever parsed as
 * markup, and nothing is ever written into the navigating attribute --
 * `live.test.ts` scans this directory for the first and hands the screen a fake
 * element whose navigating attribute throws for the second.
 */
export function renderLive(factory: LiveFactory, model: LiveModel, theme: LiveTheme): ScreenElement {
  const root = make(factory, 'section', 'live')
  root.setAttribute('data-theme', theme)
  // The arrival stamp of the frame the values came from, on the root as well as
  // in the header's own line: a test (and a later screen) reads the timestamp of
  // what it is looking at without walking the tree, and an empty value is a
  // caller that keeps no feed reading at all.
  root.setAttribute('data-received-at', model.feed === null ? '' : (model.feed.since ?? ''))
  root.append(header(factory, model))
  root.append(total(factory, model))

  // A `div` and not an `ol`: each child is a `section`, and a list whose items
  // are not list items is markup that says one thing and means another.
  const repos = cell(root, factory, 'div', 'live-repos')
  for (const repo of model.repos) repos.append(repoBlock(factory, repo))

  if (model.orphans.length > 0) {
    // A run the console cannot place under a repository is still a run that is
    // spending, so it is drawn -- in the same table, with no panel around it,
    // which is exactly the fact: nothing in this document says which project it
    // belongs to.
    const orphans = cell(root, factory, 'div', 'live-orphans')
    orphans.append(runTable(factory, model.orphans))
  }

  // The review inbox is last: the screen answers "what is spending now" first,
  // and what is waiting for the operator after it (design section 6.1 rule 6,
  // "one glance answers what is responsible"). It is drawn whether or not it
  // has a row -- see `reviewSection`.
  root.append(reviewSection(factory, model))
  return root
}
