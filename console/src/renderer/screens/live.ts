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
import { launcherCell } from '../../shared/launcher'
import { rigorModeWord } from '../../shared/rigor'
import type { RigorView } from '../../shared/rigor'
import { sanitizeText } from '../../shared/sanitize'
import type { BurnView, FinishedView, KillView, LiveDocument, RunView, TotalsView } from '../../shared/schema'
import { putReading, putText } from '../app'
import type { ElementLike } from '../app'

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
   * carries none; the cell draws `launcherShort` of it (`shared/launcher.ts`) */
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

/** LiveModel is the whole screen as data. */
export interface LiveModel {
  readonly project: string
  readonly mode: string
  readonly day: string
  readonly now: string
  readonly connection: LiveConnection
  readonly total: TotalsView
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
      runs: document.runs.filter((run) => run.slug === repo.slug).map((run) => rowOf(run, rigor)),
    }
  })

  const known = new Set(document.repos.map((repo) => repo.slug))
  const orphans = document.runs.filter((run) => !known.has(run.slug)).map((run) => rowOf(run, rigor))

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

// The attributes a screen may set are `class`, `title` and `data-*` and nothing
// else: the first two are how a stylesheet and a tooltip find a node, and the
// third is where a reading goes when it is a shape rather than a sentence.
// `live.test.ts` asserts every attribute of every drawn node is one of them,
// over a fake element whose navigating, resource, `style` and `on*` attributes
// throw.

/** make is a fresh element with its classes. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  return element
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

/** runRow draws one run of a repository. */
function runRow(factory: ScreenFactory, row: LiveRunRow): ScreenElement {
  const classes = ['live-run']
  if (row.queued) classes.push('live-run-queued')
  if (row.stuck) classes.push('live-run-stuck')
  const element = make(factory, 'li', ...classes)
  element.setAttribute('data-tone', row.tone)
  element.setAttribute('data-queued', row.queued ? 'true' : 'false')

  textCell(element, factory, 'span', row.run, 'runId', 'runs[].run', 'live-run-id')
  textCell(element, factory, 'span', row.title, 'sentence', 'runs[].title', 'live-run-title')
  if (row.rigor !== null) modeChip(element, factory, row.rigor)
  textCell(element, factory, 'span', row.stage, 'label', 'runs[].stage', 'live-run-stage')
  textCell(element, factory, 'span', row.round, 'label', 'runs[].round', 'live-run-round')
  textCell(element, factory, 'span', row.verify, 'label', 'runs[].verify', 'live-run-verify')
  textCell(element, factory, 'span', row.models, 'label', 'runs[].models', 'live-run-models')
  if (row.recipe !== null) {
    textCell(element, factory, 'span', row.recipe, 'label', 'runs[].recipe', 'live-run-recipe')
  }
  textCell(element, factory, 'span', row.auth, 'label', 'runs[].auth', 'live-run-auth')
  numberCell(element, factory, 'span', row.spend, 'usd', 'live-run-spend')
  numberCell(element, factory, 'span', row.age, 'seconds', 'live-run-age')

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
  if (row.workflow !== null) {
    textCell(element, factory, 'span', row.workflow, 'label', 'runs[].workflow', 'live-run-workflow')
  }
  if (row.requestedBy !== null) {
    textCell(element, factory, 'span', row.requestedBy, 'person', 'runs[].requested_by', 'live-run-requester')
  }
  // Who started the run, as the short form a row has room for: the CLI's own
  // cut (`runview.LauncherShort`), drawn as a `label` -- `shared/launcher.ts`
  // says why the class is not `person` here. A run whose record carries no
  // launcher draws no cell at all, never another person's name.
  const startedBy = launcherCell(row.launchedBy)
  if (startedBy !== null) {
    textCell(element, factory, 'span', startedBy, 'label', 'runs[].launched_by', 'live-run-started-by')
  }
  return element
}

/** modeChip is one run's rigor mode, as the single mark a Live row has room
 * for -- the whole answer is on the run's own screen (`run.ts`'s `rigorLine`),
 * and the Projects screen's MODE cell is the same word in a table.
 *
 * **The word is the chip.** `data-mode` carries the wire value so the
 * stylesheet can mark the five modes apart, and the text carries the meaning,
 * so a chip whose colour a reader cannot see is still the mode (styling §3.5,
 * "never colour alone"). A mode this build has never heard of is the
 * document's own word, through `putText` with the `label` class and the
 * `runs[].rigor.mode` field -- bounded, escaped, and `n/a` when the sanitizer
 * will not have it -- with `data-mode="unknown"`, because a mode a newer CLI
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
    drawn.setAttribute('data-mode', 'unknown')
    return
  }
  literal(drawn, word)
  // `rigor.mode` is one of the five keys of `RIGOR_MODE_WORDS` on this line --
  // the branch is reached only when the lookup found it -- so the attribute
  // carries the console's own vocabulary and not a string the document chose.
  drawn.setAttribute('data-mode', rigor.mode)
}

/** repoBlock draws one repository: its name, its bar, its burn and its runs. */
function repoBlock(factory: ScreenFactory, repo: LiveRepoRow): ScreenElement {
  const element = make(factory, 'li', 'live-repo')
  element.setAttribute('data-key', sanitizedKey(repo.slug))
  element.setAttribute('data-killed', repo.kill === null ? 'false' : 'true')

  // `repoDisplayName` (internal/watch/build.go) falls back to the slash-free
  // slug when the host's config does not name the repository: the slash decides
  // the class, as `spend.ts`'s `keyClass` does (review round 2 of PR 494).
  textCell(element, factory, 'span', repo.repo, repo.repo.includes('/') ? 'repository' : 'slug', 'repos[].repo', 'live-repo-name')
  numberCell(element, factory, 'span', repo.countedUsd, 'usd', 'live-repo-counted')
  numberCell(element, factory, 'span', repo.dailyCapUsd, 'usd', 'live-repo-cap')
  bar(element, factory, repo.percent, 'live-repo-percent')
  numberCell(element, factory, 'span', repo.notionalUsd, 'usd', 'live-repo-notional')
  numberCell(element, factory, 'span', repo.burnPerMin, 'usd', 'live-burn-rate')
  labelBurnRate(element, factory, repo)
  element.append(spark(factory, repo.spark))
  if (repo.kill !== null) element.append(killBanner(factory, repo.kill))

  const runs = cell(element, factory, 'ol', 'live-runs')
  for (const row of repo.runs) runs.append(runRow(factory, row))
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

/** total draws the global cap bar with today's counted dollars and the cap. */
function total(factory: ScreenFactory, model: LiveModel): ScreenElement {
  const element = make(factory, 'div', 'live-total')
  numberCell(element, factory, 'span', model.total.countedUsd, 'usd', 'live-counted')
  numberCell(element, factory, 'span', model.total.dailyCapUsd, 'usd', 'live-cap')
  numberCell(element, factory, 'span', model.total.percent, 'percent', 'live-percent')
  numberCell(element, factory, 'span', model.total.notionalUsd, 'usd', 'live-total-notional')
  bar(element, factory, model.total.percent, 'live-total-percent')
  if (model.total.kill !== null) element.append(killBanner(factory, model.total.kill))
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
export function renderLive(factory: ScreenFactory, model: LiveModel, theme: LiveTheme): ScreenElement {
  const root = make(factory, 'section', 'live')
  root.setAttribute('data-theme', theme)
  // The arrival stamp of the frame the values came from, on the root as well as
  // in the header's own line: a test (and a later screen) reads the timestamp of
  // what it is looking at without walking the tree, and an empty value is a
  // caller that keeps no feed reading at all.
  root.setAttribute('data-received-at', model.feed === null ? '' : (model.feed.since ?? ''))
  root.append(header(factory, model))
  root.append(total(factory, model))

  const repos = cell(root, factory, 'ol', 'live-repos')
  for (const repo of model.repos) repos.append(repoBlock(factory, repo))

  if (model.orphans.length > 0) {
    const orphans = cell(root, factory, 'ol', 'live-orphans')
    for (const row of model.orphans) orphans.append(runRow(factory, row))
  }

  // The review inbox is last: the screen answers "what is spending now" first,
  // and what is waiting for the operator after it (design section 6.1 rule 6,
  // "one glance answers what is responsible"). It is drawn whether or not it
  // has a row -- see `reviewSection`.
  root.append(reviewSection(factory, model))
  return root
}
