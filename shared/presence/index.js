// The staleness and offline rule (mobile plan, Task P1.4).
//
// One pure function over three values -- a heartbeat, the current time and the
// heartbeat's interval -- answering one of four states:
//
//     live            the orchestrator is writing its heartbeat on schedule
//     stale           it has missed N intervals; it may be busy or hung
//     offline         it has been silent far longer than the stale band
//     clock-differs   the heartbeat's timestamp is in the future beyond the
//                     bound, so the phone's own clock is not to be trusted
//
// Why the fourth state exists and is not folded into the others (D117, answered
// 2026-10-17): **the server's timestamp is the authority, never the app's
// clock**. A phone whose clock runs ahead computes a negative age for a
// perfectly healthy heartbeat; the naive reading -- "the age is negative, so it
// is fresh" -- reports `live` for a clock that is wrong, and the naive
// error-handling reading -- "the age did not make sense, so it is dead" --
// reports `offline` for an orchestrator that is fine. Both are wrong, and both
// make the app accept or refuse a message for the wrong reason. So a future
// timestamp beyond the tolerance is its own state, decided before either of the
// others, and never reported as `offline`.
//
// One honest limit of the rule, stated rather than hidden: a skew in the
// **past** -- a phone clock behind the server's -- is not detectable here. A
// heartbeat that looks far too old may be a dead orchestrator or a slow clock,
// and no function of one heartbeat can tell them apart. That is why the caller
// must hand this module a `now` anchored to the server (Firebase's
// `.info/serverTimeOffset`, D117's "the server's timestamp is the authority")
// rather than `Date.now()`; this module cannot verify that it did.
//
// Nothing here reads a clock, a file, the network or a random source: the
// inputs are the whole input, which is what D126 asks of everything that can be
// a pure function. `shared/README.md` is the package's own contract, and
// `../../docs/mobile/implementation-plan.md`'s P1.4 is this module's task.

/**
 * The four states the rule can answer with. Nothing else is a state: every
 * other outcome is a refusal (see `REFUSAL`), and a caller that renders an
 * unlisted string is rendering something this module never returns.
 */
export const STATE = Object.freeze({
  LIVE: 'live',
  STALE: 'stale',
  OFFLINE: 'offline',
  CLOCK_DIFFERS: 'clock-differs',
});

/** Why the state is what it is. A reason is not a fifth state. */
export const REASON = Object.freeze({
  FRESH: 'fresh',
  MISSED_INTERVALS: 'missed-intervals',
  LONG_SILENCE: 'long-silence',
  FUTURE_SKEW: 'future-skew',
  NO_HEARTBEAT: 'no-heartbeat',
});

/**
 * The refusal codes. One per distinct way an input can fail to be a heartbeat
 * rule's input, because "the phone showed offline" and "the phone was handed
 * something it could not read" are different facts and a single code for both
 * loses the second one. A refusal is never coerced into a state: the callers
 * that matter (P3.5's offline refusal, P3.6's mailbox client) decide what to do
 * with a refusal, and this module does not decide for them.
 */
export const REFUSAL = Object.freeze({
  INPUT_NOT_AN_OBJECT: 'input-not-an-object',
  UNKNOWN_FIELD: 'unknown-field',
  INVALID_HEARTBEAT: 'invalid-heartbeat',
  HEARTBEAT_MISSING_FIELD: 'heartbeat-missing-field',
  HEARTBEAT_UNKNOWN_FIELD: 'heartbeat-unknown-field',
  INVALID_HEARTBEAT_TIMESTAMP: 'invalid-heartbeat-timestamp',
  INVALID_BOOT_ID: 'invalid-boot-id',
  INVALID_NOW: 'invalid-now',
  INVALID_INTERVAL: 'invalid-interval',
  INTERVAL_BELOW_FLOOR: 'interval-below-floor',
  INTERVAL_ABOVE_CEILING: 'interval-above-ceiling',
  INVALID_BAND: 'invalid-band',
  BANDS_NOT_INCREASING: 'bands-not-increasing',
  INVALID_FUTURE_TOLERANCE: 'invalid-future-tolerance',
});

/**
 * The heartbeat's interval, in milliseconds: how often D117's writer is
 * expected to write.
 *
 * These two numbers are this task's own choice (the plan's C4 says so, and says
 * what it costs): **the interval is provisional until check 84 measures the
 * write cost**, and check 84 is owner-run and has not been run. So the number
 * here is a bounded default an operator can move through the setting, not a
 * measurement pretending to be one.
 *
 *   * `DEFAULT_INTERVAL_MS` -- one minute. One write a minute per account is
 *     the smallest cadence that still tells "busy" from "dead" within a
 *     conversational pause, which is what the spec's offline state is for.
 *   * `MIN_INTERVAL_MS` -- thirty seconds, D117's **floor**: RTDB bills per
 *     write, and a setting below the floor is refused rather than clamped, so
 *     an operator cannot quietly buy a write storm with a typo.
 */
export const DEFAULT_INTERVAL_MS = 60_000;
export const MIN_INTERVAL_MS = 30_000;

/** A ceiling, so the band arithmetic below cannot leave the safe-integer range. */
export const MAX_INTERVAL_MS = 24 * 60 * 60 * 1_000;

/**
 * The bands, in **missed intervals** rather than in milliseconds, so the rule
 * follows the interval when an operator moves it.
 *
 * `DEFAULT_MISSED_INTERVALS` (3) is P1.4's "younger than N intervals is live,
 * older is stale". `DEFAULT_OFFLINE_INTERVALS` (12) is "far older is offline" --
 * four times the stale band, so a heartbeat that is merely late twice over is
 * still only stale, and the offline state means "minutes of silence" rather
 * than "one dropped write".
 */
export const DEFAULT_MISSED_INTERVALS = 3;
export const DEFAULT_OFFLINE_INTERVALS = 12;

/**
 * How far into the future a heartbeat may be before it is a clock difference.
 * One interval by default: ordinary jitter and network latency are not a clock
 * problem, and everything beyond them is not a healthy heartbeat either way.
 */
export const DEFAULT_FUTURE_TOLERANCE_MS = DEFAULT_INTERVAL_MS;

/**
 * A band may not exceed this many intervals -- a bound on the arithmetic, not a
 * judgement about cadence. 1000 x the interval ceiling stays far inside
 * `Number.MAX_SAFE_INTEGER`, so no band can be reached by overflow.
 */
export const MAX_BAND = 1_000;

/** The boot id's length bound: enough for a UUID or a timestamp-shaped id. */
export const MAX_BOOT_ID_LENGTH = 64;

/**
 * The boot id's shape. D117's heartbeat carries a boot id beside its timestamp
 * (it is what tells a restarted orchestrator from a continuing one), and this
 * module validates it and hands it back **without letting it decide anything**:
 * the state is a function of the timestamp and the bands alone, so a writer
 * that reuses a boot id cannot make an old heartbeat look fresh. The charset is
 * deliberately narrow -- letters, digits, `.`, `_`, `:`, `-` -- because the boot
 * id reaches a screen and an id that can carry control characters or
 * directionality is a rendering hazard for no benefit; a UUID fits comfortably.
 */
const BOOT_ID_PATTERN = /^[A-Za-z0-9._:-]+$/;

/** The input's own keys. An unknown one is refused: this is not a bag. */
const INPUT_FIELDS = [
  'heartbeat',
  'now',
  'intervalMs',
  'missedIntervals',
  'offlineIntervals',
  'futureToleranceMs',
];

/** The heartbeat's keys, exactly: D117's timestamp and its boot id. */
const HEARTBEAT_FIELDS = ['at', 'bootId'];

/** A refusal: no state, a code the caller can branch on, and the field at fault. */
function refuse(code, field, detail) {
  return Object.freeze({ ok: false, code, field, detail });
}

/**
 * A plain data object -- an object literal, or one with no prototype -- and not
 * an instance of anything. The check is here rather than implied by the field
 * checks below because an instance can answer `at`/`bootId` through getters or
 * carry state this rule has no business reading: a `Date` has no own keys at
 * all, so a shape check done by field presence alone would report "missing
 * field" for what is really "this is not a heartbeat".
 */
function isPlainObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

/** A timestamp, in milliseconds: a positive safe integer and nothing else. */
function isTimestamp(value) {
  return (
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    value > 0
  );
}

/**
 * The rule. A pure function of its input object:
 *
 *     evaluatePresence({ heartbeat, now, intervalMs, missedIntervals?,
 *                        offlineIntervals?, futureToleranceMs? })
 *
 * `heartbeat` is `{ at, bootId }` -- D117's node -- or `null`/`undefined` when
 * the account has no heartbeat at all, which is `offline` and is never `live`.
 * `now` and `intervalMs` are required; the bands and the future tolerance
 * default to the constants above.
 *
 * It returns `{ ok: true, state, reason, ageMs, ... }` or
 * `{ ok: false, code, field, detail }`. It never throws, never coerces a wrong
 * type into a right one, and never guesses a state from an input it could not
 * read -- the fail-closed rule this package is built on.
 */
export function evaluatePresence(input = {}) {
  if (!isPlainObject(input)) {
    return refuse(REFUSAL.INPUT_NOT_AN_OBJECT, null, 'the input must be a plain object');
  }
  for (const key of Object.keys(input)) {
    if (!INPUT_FIELDS.includes(key)) {
      return refuse(REFUSAL.UNKNOWN_FIELD, key, `unknown input field: ${key}`);
    }
  }

  const now = input.now;
  if (!isTimestamp(now)) {
    return refuse(REFUSAL.INVALID_NOW, 'now', 'now must be a positive safe integer');
  }

  const intervalMs = input.intervalMs === undefined ? DEFAULT_INTERVAL_MS : input.intervalMs;
  if (typeof intervalMs !== 'number' || !Number.isSafeInteger(intervalMs) || intervalMs <= 0) {
    return refuse(
      REFUSAL.INVALID_INTERVAL,
      'intervalMs',
      'the interval must be a positive safe integer, in milliseconds',
    );
  }
  if (intervalMs < MIN_INTERVAL_MS) {
    return refuse(
      REFUSAL.INTERVAL_BELOW_FLOOR,
      'intervalMs',
      `an interval below ${MIN_INTERVAL_MS} ms is refused: D117's floor exists because RTDB bills per write`,
    );
  }
  if (intervalMs > MAX_INTERVAL_MS) {
    return refuse(
      REFUSAL.INTERVAL_ABOVE_CEILING,
      'intervalMs',
      `an interval above ${MAX_INTERVAL_MS} ms is refused: an interval that long makes the rule's bands meaningless`,
    );
  }

  const missedIntervals =
    input.missedIntervals === undefined ? DEFAULT_MISSED_INTERVALS : input.missedIntervals;
  const offlineIntervals =
    input.offlineIntervals === undefined ? DEFAULT_OFFLINE_INTERVALS : input.offlineIntervals;
  for (const [field, band] of [
    ['missedIntervals', missedIntervals],
    ['offlineIntervals', offlineIntervals],
  ]) {
    if (
      typeof band !== 'number' ||
      !Number.isSafeInteger(band) ||
      band < 1 ||
      band > MAX_BAND
    ) {
      return refuse(
        REFUSAL.INVALID_BAND,
        field,
        `${field} must be an integer between 1 and ${MAX_BAND}`,
      );
    }
  }
  if (offlineIntervals <= missedIntervals) {
    // Two bands that do not nest leave a heartbeat with two answers and no
    // stated tie-break. That is an ambiguity, and an ambiguity is a refusal:
    // this module will not pick one and call the other a bug later.
    return refuse(
      REFUSAL.BANDS_NOT_INCREASING,
      'offlineIntervals',
      'the offline band must be greater than the stale band',
    );
  }

  const futureToleranceMs =
    input.futureToleranceMs === undefined
      ? DEFAULT_FUTURE_TOLERANCE_MS
      : input.futureToleranceMs;
  if (
    typeof futureToleranceMs !== 'number' ||
    !Number.isSafeInteger(futureToleranceMs) ||
    futureToleranceMs < 0
  ) {
    return refuse(
      REFUSAL.INVALID_FUTURE_TOLERANCE,
      'futureToleranceMs',
      'the future tolerance must be a non-negative safe integer, in milliseconds',
    );
  }

  const staleAfterMs = intervalMs * missedIntervals;
  const offlineAfterMs = intervalMs * offlineIntervals;

  const heartbeat = input.heartbeat === undefined ? null : input.heartbeat;
  if (heartbeat === null) {
    // No heartbeat is offline, and is NEVER live: an account that has never
    // been written to has no orchestrator to show, and "we have heard nothing"
    // must not render as "everything is fine". It is also not a refusal -- a
    // missing node is a fact about the account, where garbage is a bug.
    return Object.freeze({
      ok: true,
      state: STATE.OFFLINE,
      reason: REASON.NO_HEARTBEAT,
      ageMs: null,
      bootId: null,
      heartbeatAt: null,
      intervalMs,
      staleAfterMs,
      offlineAfterMs,
    });
  }

  if (!isPlainObject(heartbeat)) {
    return refuse(
      REFUSAL.INVALID_HEARTBEAT,
      'heartbeat',
      'the heartbeat must be a plain object carrying a timestamp and a boot id',
    );
  }
  for (const key of Object.keys(heartbeat)) {
    if (!HEARTBEAT_FIELDS.includes(key)) {
      return refuse(
        REFUSAL.HEARTBEAT_UNKNOWN_FIELD,
        `heartbeat.${key}`,
        `unknown heartbeat field: ${key}`,
      );
    }
  }
  if (!('at' in heartbeat) || !('bootId' in heartbeat)) {
    return refuse(
      REFUSAL.HEARTBEAT_MISSING_FIELD,
      'heartbeat',
      'a heartbeat carries both a timestamp and a boot id (D117)',
    );
  }

  const at = heartbeat.at;
  if (!isTimestamp(at)) {
    // A zero or negative timestamp is the shape of an *unset* counter, not of a
    // heartbeat, and a string or a float is a writer that never agreed to the
    // schema. Both are refused: silently reading `at: 0` as 1970 and reporting
    // `offline` would hide the writer bug behind the state it resembles.
    return refuse(
      REFUSAL.INVALID_HEARTBEAT_TIMESTAMP,
      'heartbeat.at',
      'the heartbeat timestamp must be a positive safe integer, in milliseconds',
    );
  }

  const bootId = heartbeat.bootId;
  if (
    typeof bootId !== 'string' ||
    bootId.length === 0 ||
    bootId.length > MAX_BOOT_ID_LENGTH ||
    !BOOT_ID_PATTERN.test(bootId)
  ) {
    return refuse(
      REFUSAL.INVALID_BOOT_ID,
      'heartbeat.bootId',
      `the boot id must be 1..${MAX_BOOT_ID_LENGTH} characters of [A-Za-z0-9._:-]`,
    );
  }

  const ageMs = now - at;

  // The order of these three branches is the rule, not an implementation
  // detail. The future check comes first so that a skewed clock can never be
  // read as `live` (a negative age) and never as `offline` (an age that "makes
  // no sense"): D117 asks for exactly one answer for a skew, and it is
  // `clock-differs`. The bands are inclusive on the stale and offline side, so
  // "at N intervals" is stale rather than a boundary nobody can name.
  let state;
  let reason;
  if (ageMs < -futureToleranceMs) {
    state = STATE.CLOCK_DIFFERS;
    reason = REASON.FUTURE_SKEW;
  } else if (ageMs >= offlineAfterMs) {
    state = STATE.OFFLINE;
    reason = REASON.LONG_SILENCE;
  } else if (ageMs >= staleAfterMs) {
    state = STATE.STALE;
    reason = REASON.MISSED_INTERVALS;
  } else {
    state = STATE.LIVE;
    reason = REASON.FRESH;
  }

  return Object.freeze({
    ok: true,
    state,
    reason,
    ageMs,
    bootId,
    heartbeatAt: at,
    intervalMs,
    staleAfterMs,
    offlineAfterMs,
  });
}
