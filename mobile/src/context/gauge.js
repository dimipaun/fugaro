// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The context gauge: how full the operator's session is (mobile identity and
// pairing plan, task Z1; D2789).
//
// **What the gauge draws, and where the number may come from.** Z0 (Check 732,
// `docs/mobile-identity-pairing/context-spike.md`) found the fullness reachable
// only by running `/context`: nothing on screen carries it, whether a hook or a
// status line receives it is UNVERIFIED, and C2's port (`port.js`) carries no
// member for it -- the port's members are the project, its endpoint, the role
// and the four policy values, and a session is refused for any other. So the
// gauge has exactly two states and the app is in the second one today: it draws
// a fullness when its caller hands it a **reading**, and it answers
// `unavailable` when there is none, which is every caller until a source is
// found.
//
// **The module does not invent a figure, and that is the whole point of the
// second state.** §1's first rule -- every number carries an availability
// state, and a missing value is never an implicit zero -- is the one
// `src/feed/availability.js` states, and a gauge that drew `0%` for a session
// nobody measured would be the exact bug that module exists to stop: a screen
// saying "the context is empty" about a context nobody read. The reason travels
// with the state (`GAUGE_REASONS`) so a screen says *why* rather than showing a
// blank.
//
// **One gauge belongs to one session.** A gauge is mounted per paired project,
// so it takes that project's session and names it in what it answers: a screen
// listing several projects draws `project_id` beside each figure, and two
// gauges are never confusable. A session that is not a port is refused with the
// port's own error, as `src/jobs/list.js` refuses one -- this module passes
// another module's refusal through unchanged rather than re-labelling it.
//
// **What a reading is.** The tokens the session has used and the window it is
// using them in, whole numbers, which is the shape `/context` reports:
// `{ used: 45000, total: 200000 }`. A caller that has no reading hands none --
// absence is a missing argument and never a zero -- and a reading that is
// present and unusable is refused rather than repaired, because a reading the
// app did not build is a defect in the wiring.

import { assertSessionPort } from '../../../shared/mobile-session/port.js';

/**
 * The two states a screen switches on. Nothing else is a state: a caller that
 * renders an unlisted string is rendering something this module never returns.
 */
export const GAUGE_STATES = Object.freeze({
  /** A reading was handed in: the gauge carries the window and the fraction. */
  FULLNESS: 'fullness',
  /** No reading exists: the gauge carries no number at all. */
  UNAVAILABLE: 'unavailable',
});

/**
 * Why a gauge has no figure. One value today, from Z0's capability list: the
 * only reporter is `/context`, which the app cannot read without spending a
 * turn, no screen or status line carries a figure, and whether a hook receives
 * one is UNVERIFIED -- so no source is wired and there is nothing to draw.
 */
export const GAUGE_REASONS = Object.freeze({
  NOT_REPORTED: 'not-reported',
});

/**
 * The refusals, one code per distinct way a reading can fail to be one.
 * `field` names the part that failed (`reading.used`), and no code carries a
 * value: a reading is about a session's context, and a refusal is not a place
 * to echo it.
 */
export const GAUGE_CODES = Object.freeze({
  /** The reading is not a plain object. */
  BAD_READING: 'bad-reading',
  /** A field the shape does not name is present. Fail closed, not ignore. */
  UNKNOWN_FIELD: 'unknown-field',
  /** A field the shape names is absent. */
  MISSING_FIELD: 'missing-field',
  /** `used` is not a whole, non-negative count of tokens. */
  BAD_USED: 'bad-used',
  /** `total` is not a whole count of tokens, or is zero. */
  BAD_TOTAL: 'bad-total',
  /** `used` is above `total`: a window the session has already left. */
  USED_OVER_TOTAL: 'used-over-total',
});

/** The fields a reading carries, and no others. */
const READING_FIELDS = Object.freeze(['used', 'total']);

/** The fields a gauge carries, and no others -- `isGauge` checks exactly these. */
const GAUGE_FIELDS = Object.freeze(['state', 'project_id', 'used', 'total', 'percent', 'reason']);

/** A frozen refusal, in the shape the app's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

function isNode(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** A whole count of tokens, zero allowed: the emptiness of a window is a fact. */
function isCount(value) {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0;
}

/**
 * Judge a reading somebody else built. Returns `{ ok: true, used, total }` or a
 * refusal, and every field is judged: an extra key is a reading this app did not
 * build, and an unknown member of a shape that will be read by whoever parses
 * `/context` is exactly the kind of value that gets waved through and read
 * later.
 */
function judgeReading(reading) {
  if (!isNode(reading)) return refuse(GAUGE_CODES.BAD_READING, 'reading');
  if (Object.getOwnPropertySymbols(reading).length > 0) return refuse(GAUGE_CODES.UNKNOWN_FIELD, 'reading');
  for (const field of Object.getOwnPropertyNames(reading)) {
    if (!READING_FIELDS.includes(field)) return refuse(GAUGE_CODES.UNKNOWN_FIELD, `reading.${field}`);
  }
  for (const field of READING_FIELDS) {
    if (!Object.hasOwn(reading, field)) return refuse(GAUGE_CODES.MISSING_FIELD, `reading.${field}`);
  }
  if (!isCount(reading.used)) return refuse(GAUGE_CODES.BAD_USED, 'reading.used');
  // A window of zero tokens is not a session: it would divide the fraction by
  // zero, and a gauge whose whole rule is "never a number nobody measured" must
  // not answer `NaN%` for one.
  if (!isCount(reading.total) || reading.total === 0) return refuse(GAUGE_CODES.BAD_TOTAL, 'reading.total');
  if (reading.used > reading.total) return refuse(GAUGE_CODES.USED_OVER_TOTAL, 'reading.used');
  return Object.freeze({ ok: true, used: reading.used, total: reading.total });
}

/**
 * The gauge of `session`'s context, from the reading `reading` carries.
 *
 * Returns `{ ok: true, gauge }` or a refusal. The gauge is frozen and carries
 * the state, the session's opaque project id, and -- in the `fullness` state
 * only -- the reading's tokens and the percentage a screen draws. The
 * `unavailable` state carries `null` for all three numbers and the reason
 * beside them, so "nothing was read" cannot be mistaken for a window that is
 * empty.
 *
 * The percentage is a rendering of the reading and not a second measurement:
 * it is the rounded whole percent of `used / total`, and `used` and `total`
 * stay on the gauge because a bar drawn from a rounded percentage is off by up
 * to half a percent.
 */
export function contextGauge({ session, reading = null } = {}) {
  // Throws the port's own `SessionPortError` for a session that is not one:
  // this module does not re-label another module's refusal, and the caller that
  // mounted the gauge is the one that built the session wrong.
  const project = assertSessionPort(session).project_id;
  if (reading === null || reading === undefined) {
    return Object.freeze({
      ok: true,
      gauge: Object.freeze({
        state: GAUGE_STATES.UNAVAILABLE,
        project_id: project,
        used: null,
        total: null,
        percent: null,
        reason: GAUGE_REASONS.NOT_REPORTED,
      }),
    });
  }
  const judged = judgeReading(reading);
  if (!judged.ok) return judged;
  return Object.freeze({
    ok: true,
    gauge: Object.freeze({
      state: GAUGE_STATES.FULLNESS,
      project_id: project,
      used: judged.used,
      total: judged.total,
      percent: Math.round((judged.used / judged.total) * 100),
      reason: null,
    }),
  });
}

/**
 * Judge a value somebody else built as a gauge: the fields above, each of the
 * kind above, and no others. `gaugeLabel` refuses what this refuses, so the one
 * function a screen prints with cannot be handed a value it would print as
 * something this module never returns.
 */
export function isGauge(value) {
  if (!isNode(value)) return false;
  // A symbol-keyed member has no name to check and would travel with the value:
  // the port refuses one for the same reason.
  if (Object.getOwnPropertySymbols(value).length > 0) return false;
  if (Object.getOwnPropertyNames(value).length !== GAUGE_FIELDS.length) return false;
  if (!GAUGE_FIELDS.every((field) => Object.hasOwn(value, field))) return false;
  if (typeof value.project_id !== 'string' || value.project_id.length === 0) return false;
  if (value.state === GAUGE_STATES.UNAVAILABLE) {
    if (!Object.values(GAUGE_REASONS).includes(value.reason)) return false;
    return value.used === null && value.total === null && value.percent === null;
  }
  if (value.state !== GAUGE_STATES.FULLNESS) return false;
  if (value.reason !== null) return false;
  if (!isCount(value.used) || !isCount(value.total) || value.total === 0 || value.used > value.total) return false;
  // The percentage is derived and never independent: a gauge whose percent
  // disagrees with its own tokens is one this module did not build, and
  // `gaugeLabel` prints the percent.
  return value.percent === Math.round((value.used / value.total) * 100);
}

/**
 * How a gauge is printed: the whole percentage for a session that was read, and
 * the word the state is named for when none was. One function, so two screens
 * cannot spell the missing figure differently -- and so the second state is
 * never drawn as a nought.
 */
export function gaugeLabel(gauge) {
  if (!isGauge(gauge)) throw new TypeError('not a context gauge');
  if (gauge.state === GAUGE_STATES.UNAVAILABLE) return GAUGE_STATES.UNAVAILABLE;
  return `${gauge.percent}%`;
}
