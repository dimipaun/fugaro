// The heartbeat line: what the other end is doing, as P3.6 last read it
// (mobile plan, Task P3.3; resolved 2026-10-17, tension (b1); the presence rule
// is P1.4's, `shared/presence/index.js`).
//
// **This module decides nothing about presence, and that is the whole file.**
// The rule -- `live`, `stale`, `offline`, `clock-differs`, with its bands, its
// D117 floor and its fail-closed refusals -- is P1.4's, and the app reaches it
// through P3.6's `poll`, which reads the heartbeat node, **verifies its MAC
// against the per-pairing key**, and reports
// `presence: { state, verified, reason, ageMs, agents }`. An unsigned or
// wrongly signed heartbeat is `offline` there, with its advertised agent list
// ignored. So the job list is handed the reading and renders it; it computes no
// age, applies no band and has no clock.
//
// **Why the list does not read the heartbeat itself.** P3.2's reader subscribes
// to the mailbox family and reads the node, but it "does not decide presence:
// the heartbeat node is read and passed on". A second reader here would be a
// second opinion about the same node, with no MAC check behind it -- and the
// MAC check is the reason the mailbox client exists (D113/tension (b2)). The
// list's own input is therefore the client's report, and nothing else.
//
// **`unknown` is this module's, and it is not `offline`.** No poll has happened
// yet -- the app has not read the mailbox, or the read is in flight -- and
// saying `offline` there would be a claim about the other end made from the
// absence of a reading, which is the "silence is not death" rule seen from this
// side. So the state is `unknown`, `verified` is false, and no age is carried.
// `offline` is only ever P3.6's answer, arrived at by reading a heartbeat.
//
// The vocabulary is closed (`fields.js`'s `HEARTBEAT_STATES`): a reading
// carrying a state outside it is a **refusal**, never a rendered word. A state
// this app has never heard of is a state nobody checked.

import { HEARTBEAT_STATES, LIST_CODES } from './fields.js';

/** A frozen refusal, in the shape the app's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** The states that come from a reading, as opposed to `unknown`, which is this
 * module's own word for "nobody has read it yet". */
export const READ_STATES = Object.freeze(HEARTBEAT_STATES.filter((state) => state !== 'unknown'));

/** The line shown before any poll has reported. */
const UNKNOWN = Object.freeze({ state: 'unknown', verified: false, reason: null, ageMs: null });

/**
 * One heartbeat reading, as the list carries it.
 *
 * `presence` is P3.6's `poll()`'s `presence` value -- or `null`/`undefined`
 * when the app has not polled (or has no mailbox client at all), which is
 * **`unknown`**, not `offline`.
 *
 * What is judged, and why each is judged rather than coerced:
 *
 *   * `state` must be one of P1.4's four. A string this app has never heard of
 *     would otherwise be printed verbatim as a state.
 *   * `verified` must be a boolean **when a reading is given**: it is the MAC
 *     verdict, and a `true` beside a state nothing verified is exactly the lie
 *     the check exists to stop. A missing `verified` reads as **not verified**,
 *     which is the safe direction.
 *   * `ageMs` is a non-negative finite number or `null`/absent. Nothing here
 *     computes one.
 *   * `reason` is a bounded string or `null`/absent. It is one of P1.4's
 *     reasons or one of P3.6's own (`PRESENCE_REASON`), which are static
 *     sentences -- never a database error's text -- and it is **carried, not
 *     interpreted**: a caller that switches on it switches over two
 *     vocabularies, which is P3.6's README's warning and not this module's to
 *     resolve.
 *
 * The agent count P3.6 reports is deliberately **not** carried: P3.6 already
 * ignores it for an unverified heartbeat, and no field of the list's table
 * names an agent, so surfacing it here would be a row this task was told not to
 * draw.
 */
export function heartbeatRow(presence) {
  if (presence === null || presence === undefined) return UNKNOWN;
  if (typeof presence !== 'object' || Array.isArray(presence)) {
    return refuse(LIST_CODES.BAD_PRESENCE, 'presence');
  }
  const state = presence.state;
  if (typeof state !== 'string' || !READ_STATES.includes(state)) {
    return refuse(LIST_CODES.BAD_PRESENCE, 'presence.state');
  }
  if (presence.verified !== undefined && typeof presence.verified !== 'boolean') {
    return refuse(LIST_CODES.BAD_PRESENCE, 'presence.verified');
  }
  const ageMs = presence.ageMs;
  if (ageMs !== undefined && ageMs !== null) {
    if (typeof ageMs !== 'number' || !Number.isFinite(ageMs) || ageMs < 0) {
      return refuse(LIST_CODES.BAD_PRESENCE, 'presence.ageMs');
    }
  }
  const reason = presence.reason;
  if (reason !== undefined && reason !== null && typeof reason !== 'string') {
    return refuse(LIST_CODES.BAD_PRESENCE, 'presence.reason');
  }
  return Object.freeze({
    state,
    // Absent is not verified: the safe direction, and the one the state's own
    // producer takes (`presenceOf` in P3.6 sets `verified: false` on every
    // branch that did not verify).
    verified: presence.verified === true,
    reason: reason ?? null,
    ageMs: ageMs === undefined ? null : ageMs,
  });
}
