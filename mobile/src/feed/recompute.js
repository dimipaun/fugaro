// The two figures the phone recomputes for itself (mobile plan, Task P3.2,
// "Resolved 2026-10-17": *the burn rate and the cap fractions are recomputed on
// the phone from `spend/$day/**` and `config/caps/**`, or not shown -- the
// choice is P3.2's, stated in its PR*).
//
// **The choice, stated.** P3.2 recomputes them. The alternative -- not showing
// them -- would leave the live screen without the two numbers it exists for,
// and the inputs are both in the grant: the day's counter (`spend/$day/**`) and
// the cap (`config/caps/**`). What is recomputed is recomputed **exactly as the
// CLI computes it**, from the same constants, so the phone and the console
// cannot disagree about the same tree:
//
//   * **the cap fraction** is `internal/watch/build.go`'s `bar`: `used/cap*100`,
//     and **100 when the cap is zero** (a cap of zero denies everything, so the
//     bar is full, not empty). No cap set is no fraction at all -- `unavailable`
//     -- never 0%.
//   * **the burn rate** is `State.Burn` (`internal/watch/state.go`): the slope
//     of the counted total over a **5-minute** window that needs a **60-second**
//     span before it says anything, `perMin = (last - first) / span`, and it is
//     unknown -- not zero -- when the window is empty, when the span is too
//     short, or when the counter went **backwards** (a rollover, a reset).
//   * **`fast`** is `burnOf`: `perMin*60 > dailyCap/DefaultBurnHours`, with
//     `DefaultBurnHours = 8`. No cap means no alert threshold and so no `fast`.
//
// **A restart of the window reports unknown, not zero** (§10.9): `SetDay`
// empties the burn windows at UTC midnight, and a reconnect starts a new one,
// so the rate is `known: false` for the first `BurnWarmup` -- which is exactly
// what `reset()` is for, and why a rate of `0` is never what "no samples" means.
//
// The sampler is per key: the project's counters and each repository's are
// separate series, and a repository's rate is not the project's.

/** `watch.BurnWindow`: how far back the slope reaches. */
export const BURN_WINDOW_MS = 5 * 60_000;

/** `watch.BurnWarmup`: the span a window needs before it reports a rate. */
export const BURN_WARMUP_MS = 60_000;

/** `watch.DefaultBurnHours`: the hours a day's cap is spread over to make the
 * alert threshold `daily/8`. */
export const DEFAULT_BURN_HOURS = 8;

/**
 * The rolling burn sampler: one window per key, appended on every counter
 * update, read as a slope per minute.
 *
 * The key is the counters node the sample came from (`''` for the project's,
 * the slug for a repository's), because a sample belongs to the series it was
 * read from: mixing two repositories' counters into one window would report the
 * project's slope as if it were one repository's.
 */
export class BurnSampler {
  #windowMs;
  #warmupMs;
  #windows = new Map();

  constructor({ windowMs = BURN_WINDOW_MS, warmupMs = BURN_WARMUP_MS } = {}) {
    this.#windowMs = windowMs;
    this.#warmupMs = warmupMs;
  }

  /** Record one reading of a counter. A sample whose instant or value is not a
   * usable number is dropped rather than stored: a window holding a bad sample
   * would report a rate built from it. */
  sample(key, atMs, countedUsd) {
    if (typeof atMs !== 'number' || !Number.isFinite(atMs)) return false;
    if (typeof countedUsd !== 'number' || !Number.isFinite(countedUsd) || countedUsd < 0) return false;
    const window = this.#windows.get(key) ?? [];
    window.push({ at: atMs, v: countedUsd });
    this.#windows.set(key, window);
    return true;
  }

  /**
   * The slope in dollars per minute, or `null` when the window cannot say.
   *
   * `null` -- not `0` -- for every way of not knowing: no samples, a span
   * shorter than the warmup, and a counter that went backwards. Prunes as it
   * reads, so a window never grows past its own span.
   */
  rate(key, nowMs) {
    if (typeof nowMs !== 'number' || !Number.isFinite(nowMs)) return null;
    const window = this.#windows.get(key);
    if (window === undefined || window.length === 0) return null;
    const cut = nowMs - this.#windowMs;
    while (window.length > 1 && window[0].at < cut) window.shift();
    const first = window[0];
    const last = window[window.length - 1];
    const span = nowMs - first.at;
    if (span < this.#warmupMs) return null;
    if (last.v < first.v) return null; // the counter went backwards: no slope
    return ((last.v - first.v) * 60_000) / span;
  }

  /** Empty every window, or one. `SetDay`'s rule: a rollover restarts the
   * slope, and a restarted window is unknown for `BurnWarmup`. */
  reset(key) {
    if (key === undefined) this.#windows.clear();
    else this.#windows.delete(key);
  }

  /** How many samples a key's window holds, for a caller that must not guess. */
  size(key) {
    return this.#windows.get(key)?.length ?? 0;
  }
}

/**
 * The cap fraction, as `bar()` computes it: `used/cap*100`, or **100** when the
 * cap is zero (a cap of zero denies everything, so the bar is full). `null` when
 * either side is unknown -- an unread cap is `unavailable`, never 0%.
 *
 * A fraction above 100 is a real fraction: the day can be over its cap, and the
 * bar is drawn past full rather than clipped here.
 */
export function percent(countedUsd, capUsd) {
  if (typeof countedUsd !== 'number' || !Number.isFinite(countedUsd) || countedUsd < 0) return null;
  if (typeof capUsd !== 'number' || !Number.isFinite(capUsd) || capUsd < 0) return null;
  if (capUsd === 0) return 100;
  return (countedUsd / capUsd) * 100;
}

/** Is the rate burning faster than the day's cap spread over
 * `DefaultBurnHours`? No cap means no threshold, and so no. */
export function fast(perMinUsd, dailyCapUsd) {
  if (typeof perMinUsd !== 'number' || !Number.isFinite(perMinUsd) || perMinUsd < 0) return false;
  if (typeof dailyCapUsd !== 'number' || !Number.isFinite(dailyCapUsd) || dailyCapUsd < 0) return false;
  return perMinUsd * 60 > dailyCapUsd / DEFAULT_BURN_HOURS;
}

/**
 * The two recomputed figures of one counters series, in the shape the document
 * carries them.
 *
 * `rate` is dollars per minute or `null`, `counted` and `cap` are dollars or
 * `null`; everything out is a number or `null`, so `document.js` can judge it
 * into figures and no `0` can appear that nobody measured.
 */
export function recompute({ counted, cap, rate } = {}) {
  const perMin = typeof rate === 'number' && Number.isFinite(rate) && rate >= 0 ? rate : null;
  return Object.freeze({
    percent: percent(counted, cap),
    burn: Object.freeze({
      known: perMin !== null,
      usdPerMin: perMin,
      fast: perMin !== null && fast(perMin, cap),
    }),
  });
}
