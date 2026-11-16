// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The indicator's rendering (mobile plan, Task P5.3.6; D123, D127; the design is
// docs/mobile/design/voice-evaluation-switch.md §4.2 and §4.5).
//
// **This file draws; it does not decide.** `shared/voice-switch` is the switch
// and the badge as one pure function of the four inputs the app holds, and the
// plan's own instruction is to build on it and not to re-implement its table.
// So there is no branch about a switch, a consent, a project answer or a call
// in flight anywhere below: the shared function is asked, and its answer is
// rendered. What this file adds is the three things a *screen* needs and a pure
// rule has no opinion about:
//
//   1. **`send`**, the one field the egress path (P5.3.7) reads before it puts
//      a byte on the wire: true only for a live, admissible, consented route
//      with **no call already in flight**. The shared module says the last half
//      in as many words -- `inFlight` is "what tells the caller not to start a
//      second call at all" -- and it is one field here rather than a rule the
//      sending code has to remember.
//   2. **`persistent` and `dismissible`**, the design's §4.2 sentence turned
//      into a value: "It is not dismissible and it is not a toast". A screen
//      that renders this badge has nothing to decide about hiding it, and the
//      badge carries no way to be hidden -- the field set below is exact.
//   3. **The fail-closed badge.** A refusal is not an answer, and a badge is
//      still owed: the app may not show nothing, and "local — checking" is the
//      stand-in the shared module's own README names. So `renderIndicator`
//      never refuses and never throws -- it draws that badge and carries the
//      refusal in `refusal`, where a screen can log it and a reviewer can see
//      that something upstream is broken. **Engine `local` and `send` false**:
//      a badge the app could not build is the last one that may be read as
//      permission to send.
//
// **A badge is not a result.** There is no `ok: false` badge and no state in
// which this function returns nothing, which is why the field set carries no
// `ok`: what a caller may do with a badge is render it, and what it may do with
// `refusal` is report it.

import { DIRECTIONS } from './settings.js';
import { ENGINE, REASON, STATE, indicator as decide } from '../../../shared/voice-switch/index.js';

/**
 * The two words about the badge itself (design §4.2): it is persistent in the
 * voice screen, and it cannot be dismissed. Constants rather than literals at
 * the one place they are written, so a screen and a test name the same fact.
 */
export const INDICATOR = Object.freeze({
  persistent: true,
  dismissible: false,
});

/** The badge's field set, exactly. A field added here is a decision. */
export const INDICATOR_FIELDS = Object.freeze([
  'direction',
  'state',
  'text',
  'inFlight',
  'engine',
  'provider',
  'reason',
  'send',
  'persistent',
  'dismissible',
  'refusal',
]);

/**
 * The design's wording for the state the shared module calls `checking`
 * (§4.2), kept verbatim including the em dash. It is the shared module's own
 * string for that state as well -- `index.test.js` pins the two together, so a
 * change to the wording in either place is a failure rather than two badges.
 */
const CHECKING_TEXT = 'local — checking';

/** A frozen badge, in the field order above. */
function badge(fields) {
  return Object.freeze({
    direction: fields.direction,
    state: fields.state,
    text: fields.text,
    inFlight: fields.inFlight,
    engine: fields.engine,
    provider: fields.provider,
    reason: fields.reason,
    send: fields.send,
    persistent: INDICATOR.persistent,
    dismissible: INDICATOR.dismissible,
    refusal: fields.refusal,
  });
}

/** The direction the caller asked about, when it is one of the two. */
function askedDirection(input) {
  try {
    if (input === null || typeof input !== 'object') return null;
    const { direction } = input;
    return DIRECTIONS.includes(direction) ? direction : null;
  } catch {
    // A Proxy that throws on every trap is a refusal upstream (the shared
    // module answers `internal-error`); it must not make the badge throw here.
    return null;
  }
}

/**
 * The badge drawn when no answer could be built: local, and saying so.
 *
 * Exported because a caller can hold a refusal of its own -- the switch
 * (`switch.js`) has one when the record cannot be read at all -- and a badge is
 * owed in that case exactly as much as in the other. Nothing else about it is
 * a caller's to choose: the state, the words, the engine and `send` are fixed
 * here, which is the point.
 */
export function failClosedIndicator(input, refusal) {
  return badge({
    direction: askedDirection(input),
    state: STATE.CHECKING,
    text: CHECKING_TEXT,
    inFlight: false,
    engine: ENGINE.LOCAL,
    provider: null,
    reason: REASON.NO_ANSWER,
    send: false,
    refusal: Object.freeze({ code: refusal.code, field: refusal.field }),
  });
}

/**
 * Render the indicator for one direction.
 *
 * The input is `shared/voice-switch`'s own -- `direction`, `settings`,
 * `consent`, `status` and `lastCallInDirection` -- and it is handed over
 * unchanged: this function adds no field to it, defaults no field of it, and
 * reads nothing of it itself. `settings` and `consent` come from the record
 * (`settings.js`), `status` is the hop's last project-status answer, and
 * `lastCallInDirection` is **this** direction's last call (the shared module's
 * own honest limit: a caller that keeps one route for the whole app will be
 * told the wrong thing).
 */
export function renderIndicator(input) {
  const answer = decide(input);
  if (!answer.ok) return failClosedIndicator(input, answer);
  return badge({
    direction: answer.direction,
    state: answer.state,
    text: answer.text,
    inFlight: answer.inFlight,
    engine: answer.engine,
    provider: answer.provider,
    reason: answer.reason,
    send: answer.engine === ENGINE.REMOTE && answer.inFlight === false,
    refusal: null,
  });
}
